#!/usr/bin/env python3
"""Isolated committed-but-unacknowledged repair/rollback and real Java/Go reads."""
import argparse
import base64
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import re
import shutil
import signal
import sys
import time

ROOT=Path(__file__).resolve().parents[1]
def load(name,path):
 spec=importlib.util.spec_from_file_location(name,path);module=importlib.util.module_from_spec(spec);spec.loader.exec_module(module);return module
previous=load('previous_reparse_acceptance',ROOT/'script/verify-native-reparse.py')
readers=load('reparse_real_reads',ROOT/'tests/reparse/real_reads.py')
require=previous.require

def verify_commit_witness(gate,before,after,audit,action,task_id,records):
 require(gate.get('serverCommitOKObserved') is True and gate.get('commitOKForwarded') is False,'successful COMMIT ACK must be withheld')
 require(gate.get('sequence')==1 and gate.get('payloadBytes',0)>0,'real COMMIT OK packet metadata required')
 require(audit.get('task_id')==task_id and audit.get('state')==('applied' if action=='apply' else 'rolled_back'),'committed audit state required on independent connection')
 if action=='rollback':
  require(after==before,'committed rollback must restore complete original task/detail rows');return
 require(action=='apply','unknown mutation action')
 require(before['task']==after['task'],'committed apply changed task metadata')
 old=before['details'];rows=after['details']
 require(rows[:len(old)]==old and len(rows)==len(records),'committed apply must preserve full prefix and exact count')
 require([(r['content'],r['log_type'],r['log_time']) for r in rows]==[(r['c'],r['f'],r['l']) for r in records],'committed rows differ from independent source records')
 ids=[r['id'] for r in rows[len(old):]]
 require(ids and len(set(ids))==len(ids) and all(i>0 for i in ids) and ids==json.loads(audit['append_ids']),'audit must identify exactly the inserted suffix')

class ConfirmationAcceptance(previous.Acceptance):
 def dump(self,name,value):
  p=self.args.out/(name+'.json');p.write_text(json.dumps(value,ensure_ascii=False,indent=2)+'\n');p.chmod(0o600);return value
 def business(self):
  result={}
  for key,table,owner in (('task','logan_task','id'),('details','logan_log_detail','task_id')):
   cols=[line.split('\t')[0] for line in self.sql('SHOW COLUMNS FROM '+self.db+'.'+table).splitlines()]
   require(all(re.fullmatch('[a-z_]+',c) for c in cols),'unexpected fixture column')
   fields=','.join("'%s',`%s`"%(c,c) for c in cols)
   raw=self.sql('SELECT HEX(CAST(JSON_OBJECT('+fields+') AS CHAR CHARACTER SET utf8mb4)) FROM '+self.db+'.'+table+' WHERE '+owner+'=20 ORDER BY id')
   result[key]=[json.loads(bytes.fromhex(line).decode()) for line in raw.splitlines()]
  return result
 def audit(self,op):
  require(re.fullmatch('[a-f0-9]{32}',op),'safe operation ID required')
  raw=self.sql("SELECT HEX(CAST(JSON_OBJECT('task_id',task_id,'state',state,'fingerprint',fingerprint,'before_hash',before_hash,'after_hash',after_hash,'append_ids',append_ids) AS CHAR)) FROM "+self.db+".logan_native_reparse_operation WHERE operation_id='"+op+"'")
  require(bool(raw),'committed audit row missing')
  return json.loads(bytes.fromhex(raw).decode())
 def start_proxy(self,action):
  name=self.reserve('container',self.prefix+'-ack-'+action)
  source=self.work/'commit_proxy.py'
  if not source.exists():shutil.copyfile(ROOT/'tests/reparse/commit_proxy.py',source)
  self.docker('run','-d','--name',name,'--label',previous.base.LABEL+'='+self.run_id,'--network',self.network,
   '--read-only','--cap-drop','ALL','--security-opt','no-new-privileges:true','--memory','128m','--cpus','1','--pids-limit','64',
   '--tmpfs','/state:rw,noexec,nosuid,nodev,size=1048576,mode=0700',
   '--mount','type=bind,src='+str(source)+',dst=/proxy.py,readonly','python:3.12-alpine','python','/proxy.py','--backend-host',self.mysql,'--deadline-seconds','180')
  deadline=time.monotonic()+20
  while True:
   code,_=self.docker('exec',name,'python','-c',"import socket;s=socket.create_connection(('127.0.0.1',3306),1);s.close()",accept=True)
   if code==0:return name
   require(time.monotonic()<deadline,'proxy listener not ready');time.sleep(.2)
 def lost_confirmation(self,action,plan,op,original):
  proxy=self.start_proxy(action)
  dsn='root:'+self.password+'@tcp('+proxy+':3306)/'+self.db+'?parseTime=true&tls=false'
  flags=['--action',action,'--allow-isolated-writes','--offline']
  if action=='apply':flags+=['--task-id','20','--plan','/evidence/'+plan.name]
  else:flags+=['--operation-id',op]
  client=self.cli(*flags,extra=('LOGAN_MYSQL_DSN='+dsn,),detached=True)
  deadline=time.monotonic()+45
  while True:
   code,raw=self.docker('exec',proxy,'cat','/state/commit-held.json',accept=True)
   if code==0:gate=json.loads(raw);break
   _,running=self.docker('inspect','--format','{{.State.Running}}',client)
   require(running.strip()=='true','client exited before a successful COMMIT was withheld')
   require(time.monotonic()<deadline,'successful COMMIT ACK barrier not reached');time.sleep(.2)
  # These reads bypass the proxy. Visibility to this new connection proves COMMIT.
  committed=self.business();audit=self.audit(op)
  verify_commit_witness(gate,original,committed,audit,action,20,self.records)
  _,running=self.docker('inspect','--format','{{.State.Running}}',client)
  require(running.strip()=='true','unacknowledged client must still be blocked')
  _,output=self.docker('logs',client);require(not output.strip(),'client must not have reported success before termination')
  self.docker('kill','--signal','KILL',client)
  _,exitcode=self.docker('wait',client);require(exitcode.strip()=='137','unacknowledged client must exit137')
  _,output=self.docker('logs',client);require(not output.strip(),'killed client must have no success output')
  before_retry=self.snapshot('ack-'+action+'-committed-before-retry')
  result=self.apply(plan,20) if action=='apply' else self.rollback(op)
  require(result.get('noop') is True,'same operation after lost confirmation must return noop')
  after_retry=self.snapshot('ack-'+action+'-after-retry')
  require(before_retry==after_retry,'retry after committed operation changed data/audit/objects')
  report={'action':action,'operationId':op,'proxy':gate,'independentCommittedWitness':{'business':committed,'audit':audit},
   'clientExitCode':137,'clientSuccessOutputAbsent':True,'retry':result,'retryFullSnapshotUnchanged':True,
   'cutpoint':'server COMMIT success OK withheld from live client, committed audit/business visible via independent direct connection, then client SIGKILL'}
  self.dump('lost-confirmation-'+action,report)
  self.checks.append({'check':action+'_committed_ack_withheld_client_killed_retry_noop','status':'passed'})
  return committed
 def execute(self):
  super().execute()
  # The old matrix has stopped every CLI writer. Readers and repair phases are serial.
  self.docker('network','disconnect',self.network,self.oss)
  self.docker('network','connect','--alias','fixture.'+self.oss,self.network,self.oss)
  self.sql("USE "+self.db+"; INSERT INTO logan_task(id,platform,device_id,log_file_name,log_date,add_time,status,update_time) VALUES(20,1,'ack-compat','history.log',1792022400000,1792022400000,1,'2026-01-01 00:00:00'); INSERT INTO logan_log_detail(id,task_id,log_type,content,log_time,add_time,update_time) VALUES(2000,20,1,'old-prefix',1792022400000,'2026-01-01 00:00:00','2026-01-01 00:00:00')")
  _,raw=self.docker('exec',self.oss,'python','-c',"import pathlib,base64;print(base64.b64encode(pathlib.Path('/objects/history.log').read_bytes()).decode())")
  self.read_fixture={'object_bytes':base64.b64decode(raw),'deviceId':'ack-compat'}
  self.read_business_snapshot=self.business
  original=self.dump('compat-original-business',self.business())
  self.read_results=[]
  self.read_results.append(readers.check_reads(self,'pre',20,original['details'],'history.log'))
  plan,metadata=self.plan(20,'lost-confirmation')
  applied=self.lost_confirmation('apply',plan,metadata['operation_id'],original)
  self.read_results.append(readers.check_reads(self,'post',20,applied['details'],'history.log'))
  restored=self.lost_confirmation('rollback',plan,metadata['operation_id'],original)
  self.read_fixture['deletedDetailIds']=[r['id'] for r in applied['details'][len(original['details']):]]
  self.read_results.append(readers.check_reads(self,'undo',20,restored['details'],'history.log'))
  require(self.business()==original,'final business state differs from original prefix')
  self.dump('real-reader-summary',{'status':'passed','phases':self.read_results,'businessOriginalRestored':True,
   'scope':'actual original Java/Tomcat9 JRE17 and verified production Go image; seeded completed Native prefix; local unsigned OSS; no production'})
  self.checks.append({'check':'original_java_go_reads_before_after_apply_and_rollback','status':'passed'})

def finish(v,a,code,error):
 signal.signal(signal.SIGINT,signal.SIG_IGN);signal.signal(signal.SIGTERM,signal.SIG_IGN)
 try:v.cleanup()
 finally:
  for p in a.out.glob('*.env'):p.unlink()
  if any(c['status']=='failed' for c in v.checks):code=1
  report={'status':'passed' if code==0 else ('blocked' if code==2 else 'failed'),'exit_code':code,'error':error,'checks':v.checks,
   'binarySha256':hashlib.sha256(a.binary.read_bytes()).hexdigest(),'goImage':a.go_image,
   'scope':'real committed ACK withheld for apply/rollback; seeded Native prefix; actual Java/Go HTTP readers; isolated MySQL and unsigned local OSS; no cloud/production/cutover'}
  v.dump('summary',report);print('Evidence: '+str(a.out/'summary.json'))
 return code

def main():
 p=argparse.ArgumentParser(description=__doc__)
 p.add_argument('--docker',required=True);p.add_argument('--host',required=True);p.add_argument('--allow-isolated-runtime',action='store_true')
 p.add_argument('--binary',type=Path,required=True);p.add_argument('--go-image',required=True);p.add_argument('--out',type=Path,required=True);p.add_argument('--work-dir',type=Path,required=True)
 a=previous.validate_args(p.parse_args())
 for d in (a.out,a.work_dir):d.mkdir(mode=0o700,parents=True)
 a.out=a.out.resolve();a.work_dir=a.work_dir.resolve()
 v=ConfirmationAcceptance(a);code=0;error=None
 signal.signal(signal.SIGTERM,previous.interrupted)
 try:v.execute()
 except BaseException as e:code=previous.runtime.classify_exit(e,v.engine_ready);error=type(e).__name__+': '+str(e).replace(v.password,'<synthetic-secret>')
 finally:code=finish(v,a,code,error)
 return code
if __name__=='__main__':sys.exit(main())
