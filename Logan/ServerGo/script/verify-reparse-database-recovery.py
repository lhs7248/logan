#!/usr/bin/env python3
"""Opt-in repair/rollback recovery at real MySQL crash and connection cutpoints."""
import argparse
import base64
import datetime
import hashlib
import importlib.util
import json
from pathlib import Path
import signal
import sys
import time

ROOT=Path(__file__).resolve().parents[1]
spec=importlib.util.spec_from_file_location('confirmation',ROOT/'script/verify-reparse-confirmation.py')
prior=importlib.util.module_from_spec(spec);spec.loader.exec_module(prior)
require=prior.require

def assert_recovery(before, recovered, committed):
 require(before==recovered, 'committed state lost during recovery' if committed else 'uncommitted transaction published partial data or audit')

def validate_server(row):
 fields=row.split('\t')
 require(len(fields)==4 and fields[0] and fields[1:3]==['1','1'] and fields[3] in ('ON','1'),'durable InnoDB/binlog settings required')
 return {'serverUUID':fields[0],'innodbFlushLogAtTrxCommit':1,'syncBinlog':1,'logBin':True}

class DatabaseRecovery(prior.ConfirmationAcceptance):
 def mysql_storage(self):
  self.volume=self.reserve('volume',self.prefix+'-dbdata')
  self.owned.remove(('volume',self.volume));self.owned.insert(0,('volume',self.volume))
  self.docker('volume','create','--label',prior.previous.base.LABEL+'='+self.run_id,self.volume)
  return ['--memory','1536m','--cpus','2','--pids-limit','256','--mount','type=volume,src='+self.volume+',dst=/var/lib/mysql']
 def verify_audit_snapshots(self,op,original,current,action):
  raw=self.sql("SELECT HEX(before_snapshot),HEX(after_snapshot),before_hash,after_hash FROM "+self.db+".logan_native_reparse_operation WHERE operation_id='"+op+"'")
  before,after,bh,ah=raw.split('\t')
  b=bytes.fromhex(before);a=bytes.fromhex(after)
  require(hashlib.sha256(b).hexdigest()==bh and hashlib.sha256(a).hexdigest()==ah,'stored audit snapshots do not match hashes')
  def canonical(rows):
   result=[]
   for row in rows:
    row=dict(row)
    for key in ('update_time','add_time'):
     if isinstance(row.get(key),str):row[key]=datetime.datetime.fromisoformat(row[key]).replace(tzinfo=None).isoformat(sep=' ',timespec='microseconds')
    result.append(row)
   return result
  def business(value):return {'task':canonical([value['Task']]),'details':canonical(value['Details'])}
  require(business(json.loads(b))==original,'audit before snapshot differs from original complete business rows')
  stored_after=business(json.loads(a))
  audit=dict(self.audit(op));audit['state']='applied'
  prior.verify_commit_witness({'serverCommitOKObserved':True,'commitOKForwarded':False,'sequence':1,'payloadBytes':7},original,stored_after,audit,'apply',int(original['task'][0]['id']),self.records)
  if action=='apply':require(stored_after==current,'audit after snapshot differs from committed complete business rows')
  else:require(current==original,'rolled back business differs from audited original')
  return {'beforeSHA256':bh,'afterSHA256':ah,'snapshotsMatchBusiness':True}
 def wait_mysql(self):
  deadline=time.monotonic()+120
  while True:
   try:
    if self.sql('SELECT 1')=='1':return
   except prior.previous.base.DockerError:pass
   require(time.monotonic()<deadline,'MySQL TCP recovery timeout');time.sleep(.5)
 def server(self):
  return validate_server(self.sql('SELECT @@server_uuid,@@innodb_flush_log_at_trx_commit,@@sync_binlog,@@log_bin'))
 def audit_optional(self,op):
  if self.sql("SELECT COUNT(*) FROM "+self.db+".logan_native_reparse_operation WHERE operation_id='"+op+"'")=='0':return None
  return self.audit(op)
 def wait_client(self,client):
  _,raw=self.docker('wait',client,timeout=45)
  code=int(raw.strip());_,output=self.docker('logs',client)
  require(code!=0,'interrupted client unexpectedly reported success')
  require('"operation_id"' not in output,'interrupted client emitted success result')
  return code
 def crash_restart(self):
  before=self.server()
  _,raw=self.docker('inspect',self.mysql)
  info=json.loads(raw)[0]
  mounts=[m for m in info['Mounts'] if m['Destination']=='/var/lib/mysql']
  require(len(mounts)==1 and mounts[0]['Type']=='volume' and mounts[0]['Name']==self.volume,'persistent owned datadir volume required')
  self.docker('kill','--signal','KILL',self.mysql)
  _,raw=self.docker('wait',self.mysql);require(raw.strip()=='137','MySQL must exit137')
  _,raw=self.docker('inspect',self.mysql);stopped=json.loads(raw)[0]
  require(stopped['State']['OOMKilled'] is False,'crash must not be OOM')
  self.docker('start',self.mysql);self.wait_mysql()
  after=self.server();require(after==before,'server UUID/durability changed on restart')
  _,raw=self.docker('inspect',self.mysql);restarted=json.loads(raw)[0]
  require(restarted['Mounts']==info['Mounts'],'datadir mount changed on restart')
  return {'exitCode':137,'OOMKilled':False,'sameContainer':info['Id']==restarted['Id'],'sameOwnedVolume':self.volume,'before':before,'after':after}
 def running(self,client):
  _,state=self.docker('inspect','--format','{{.State.Running}}',client)
  if state.strip()!='true':
   self.docker('logs',client)
   raise AssertionError('client exited before fault barrier')
 def flags(self,action,plan,op):
  flags=['--action',action,'--allow-isolated-writes','--offline']
  return flags+(['--task-id','20','--plan','/evidence/'+plan.name] if action=='apply' else ['--operation-id',op])
 def fault(self,case,action,kind,committed,plan,op,original):
  before=self.snapshot(case+'-before');business=self.business();audit=self.audit_optional(op)
  self.dump(case+'-before-state',{'business':business,'audit':audit})
  proxy=None;gate=None;connection=None
  if committed:
   proxy=self.start_proxy(case)
   dsn='root:'+self.password+'@tcp('+proxy+':3306)/'+self.db+'?parseTime=true&tls=false&readTimeout=90s'
   client=self.cli(*self.flags(action,plan,op),extra=('LOGAN_MYSQL_DSN='+dsn,),detached=True)
   deadline=time.monotonic()+45
   while True:
    code,raw=self.docker('exec',proxy,'cat','/state/commit-held.json',accept=True)
    if code==0:gate=json.loads(raw);break
    self.running(client);require(time.monotonic()<deadline,'COMMIT OK barrier timeout');time.sleep(.2)
   prior.verify_commit_witness(gate,original,self.business(),self.audit(op),action,20,self.records)
   self.running(client)
   _,output=self.docker('logs',client);require(not output.strip(),'client reported success before held ACK')
   expected=self.snapshot(case+'-committed-witness')
  else:
   statement='INSERT' if action=='apply' else 'DELETE'
   self.sql('USE '+self.db+'; CREATE TRIGGER pause_repair BEFORE '+statement+' ON logan_log_detail FOR EACH ROW SET @logan_fault_pause=SLEEP(120)')
   dsn='root:'+self.password+'@tcp('+self.mysql+':3306)/'+self.db+'?parseTime=true&readTimeout=90s'
   client=self.cli(*self.flags(action,plan,op),extra=('LOGAN_MYSQL_DSN='+dsn,),detached=True)
   deadline=time.monotonic()+40
   while True:
    raw=self.sql("SELECT p.ID FROM information_schema.PROCESSLIST p JOIN information_schema.INNODB_TRX t ON t.trx_mysql_thread_id=p.ID WHERE p.DB='"+self.db+"' AND p.STATE='User sleep' AND t.trx_rows_locked>0")
    if raw:
     ids=raw.splitlines();require(len(ids)==1 and ids[0].isdigit(),'unique active transactional statement required');connection=int(ids[0]);break
    self.dump(case+'-barrier-observation',{'rows':self.sql("SELECT p.ID,p.DB,p.STATE,p.COMMAND,LEFT(p.INFO,100),t.trx_rows_locked FROM information_schema.PROCESSLIST p LEFT JOIN information_schema.INNODB_TRX t ON t.trx_mysql_thread_id=p.ID WHERE p.DB='"+self.db+"'")})
    self.running(client);require(time.monotonic()<deadline,'transaction statement barrier timeout');time.sleep(.2)
   require(self.business()==business and self.audit_optional(op)==audit,'uncommitted changes visible to independent reader')
   expected=before
  if kind=='crash':
   fault=self.crash_restart()
   if proxy:self.docker('kill','--signal','KILL',proxy)
  else:
   identity=self.server()
   if committed:self.docker('kill','--signal','KILL',proxy)
   else:self.sql('KILL CONNECTION '+str(connection))
   require(self.server()==identity,'connection interruption changed server identity')
   fault={'serverStillRunning':True,'server':identity,'connectionID':connection,'proxyConnectionClosed':committed}
  exit_code=self.wait_client(client)
  if not committed:self.sql('DROP TRIGGER '+self.db+'.pause_repair')
  recovered=self.snapshot(case+'-recovered-before-retry')
  assert_recovery(expected,recovered,committed)
  recovered_business=self.business();recovered_audit=self.audit_optional(op)
  result=self.apply(plan,20) if action=='apply' else self.rollback(op)
  require(result.get('noop') is committed,'first retry noop must match whether operation committed')
  final_business=self.business();final_audit=self.audit(op)
  # Verify all preserved columns, exact source semantics and audited physical IDs.
  witness={'serverCommitOKObserved':True,'commitOKForwarded':False,'sequence':1,'payloadBytes':7}
  prior.verify_commit_witness(witness,original,final_business,final_audit,action,20,self.records)
  snapshot_audit=self.verify_audit_snapshots(op,original,final_business,action)
  final=self.snapshot(case+'-after-retry')
  if committed:assert_recovery(recovered,final,True)
  require(recovered['objects']==final['objects'],'first retry changed source objects')
  require(recovered['tables'].keys()==final['tables'].keys(),'first retry changed table inventory')
  require(all(final['tables'][t]==value for t,value in recovered['tables'].items() if t not in ('logan_log_detail','logan_native_reparse_operation')),'first retry changed unrelated tables')
  second=self.apply(plan,20) if action=='apply' else self.rollback(op)
  require(second.get('noop') is True,'repeat retry must noop')
  require(self.snapshot(case+'-after-repeat')==final,'repeat retry changed business/audit/object state')
  self.dump(case,{'status':'passed','action':action,'kind':kind,'committedBeforeFault':committed,'operationId':op,'barrierConnectionID':connection,'commitGate':gate,'fault':fault,'clientExitCode':exit_code,'recoveredBusiness':recovered_business,'recoveredAudit':recovered_audit,'recoveredFullSnapshotMatchesExpected':True,'retry':result,'repeat':second,'repeatFullSnapshotUnchanged':True,'finalBusiness':final_business,'finalAudit':final_audit,'auditSnapshotVerification':snapshot_audit})
  self.checks.append({'check':case,'status':'passed'})
  return final_business
 def execute(self):
  self.setup()
  self.docker('image','inspect','tomcat:9-jre17-temurin')
  self.dump('durability',self.server())
  self.docker('network','disconnect',self.network,self.oss)
  self.docker('network','connect','--alias','fixture.'+self.oss,self.network,self.oss)
  self.sql("USE "+self.db+"; INSERT INTO logan_task(id,platform,device_id,log_file_name,log_date,add_time,status,update_time) VALUES(20,1,'ack-compat','history.log',1792022400000,1792022400000,1,'2026-01-01 00:00:00'); INSERT INTO logan_log_detail(id,task_id,log_type,content,log_time,add_time,update_time) VALUES(2000,20,1,'old-prefix',1792022400000,'2026-01-01 00:00:00','2026-01-01 00:00:00')")
  self.read_fixture={'object_bytes':self.payload,'deviceId':'ack-compat'}
  self.read_business_snapshot=self.business
  original=self.dump('original-business',self.business());reads=[]
  for n,(kind,committed) in enumerate((('crash',False),('crash',True),('connection',False),('connection',True))):
   plan,meta=self.plan(20,'fault-'+str(n));op=meta['operation_id']
   for action in ('apply','rollback'):
    case=str(n)+('-a' if action=='apply' else '-r')
    rows=self.fault(case,action,kind,committed,plan,op,original)['details']
    if action=='apply':
     appended=[r['id'] for r in rows[len(original['details']):]]
     self.read_fixture['deletedDetailIds']=[]
    else:self.read_fixture['deletedDetailIds']=appended
    reads.append(prior.readers.check_reads(self,case,20,rows,'history.log'))
   require(self.business()==original,'repair/rollback pair failed to restore original')
  self.dump('real-reader-summary',{'status':'passed','phases':reads,'originalBusinessRestored':True})
  self.checks.append({'check':'eight_recovered_states_actual_java_go_reads','status':'passed'})

def main():
 p=argparse.ArgumentParser(description=__doc__)
 p.add_argument('--docker',required=True);p.add_argument('--host',required=True);p.add_argument('--allow-isolated-runtime',action='store_true')
 p.add_argument('--binary',type=Path,required=True);p.add_argument('--go-image',required=True);p.add_argument('--out',type=Path,required=True);p.add_argument('--work-dir',type=Path,required=True)
 a=prior.previous.validate_args(p.parse_args())
 for d in (a.out,a.work_dir):d.mkdir(mode=0o700,parents=True)
 a.out=a.out.resolve();a.work_dir=a.work_dir.resolve();v=DatabaseRecovery(a);code=0;error=None
 signal.signal(signal.SIGTERM,prior.previous.interrupted)
 try:v.execute()
 except BaseException as e:code=prior.previous.runtime.classify_exit(e,v.engine_ready);error=type(e).__name__+': '+str(e).replace(v.password,'<synthetic-secret>')
 finally:
  code=prior.finish(v,a,code,error)
  path=a.out/'summary.json';report=json.loads(path.read_text());report['scope']='repair/rollback pre/post COMMIT mysqld SIGKILL persistent-volume restart and connection interruption; actual Java/Go reads; isolated synthetic Native prefix and local OSS; no cloud/production/cutover';v.dump('summary',report)
 return code
if __name__=='__main__':sys.exit(main())
