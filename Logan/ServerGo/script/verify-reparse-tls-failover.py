#!/usr/bin/env python3
"""Isolated verified MySQL TLS, GTID promotion, and UUID-fenced repair recovery."""
import argparse
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import re
import signal
import subprocess
import sys
import time

ROOT=Path(__file__).resolve().parents[1]
spec=importlib.util.spec_from_file_location('database_recovery',ROOT/'script/verify-reparse-database-recovery.py')
old=importlib.util.module_from_spec(spec);spec.loader.exec_module(old)
prior=old.prior
require=old.require


def validate_tls_rows(raw):
 rows=[line.split('\t') for line in raw.splitlines() if line]
 require(rows and all(len(r)==4 and r[0].isdigit() and r[1] in ('TLSv1.2','TLSv1.3') and r[2] and r[3] in ('repair','reader','repl') for r in rows),'target client TLS version/cipher evidence required')
 return [{'connectionID':int(r[0]),'version':r[1],'cipher':r[2],'user':r[3]} for r in rows]

def validate_replica_status(raw):
 fields={}
 for line in raw.splitlines():
  if ':' in line:
   key,value=line.split(':',1);fields[key.strip()]=value.strip()
 for key in ('Replica_IO_Running','Replica_SQL_Running','Source_SSL_Allowed','Source_SSL_Verify_Server_Cert'):
  require(fields.get(key)=='Yes','healthy verified TLS replication required: '+key)
 for key in ('Last_SQL_Errno','Last_IO_Errno'):
  require(fields.get(key)=='0','replication error must be zero: '+key)
 return fields

class TLSFailover(old.DatabaseRecovery):
 def log(self,command,output,code):
  def redact(value):return value.replace(self.password,'<synthetic-secret>').replace(self.password[:32],'<synthetic-replication-secret>')
  super().log([redact(x) for x in command],redact(output),code)
 def local(self,*args):
  subprocess.run(args,check=True,stdout=subprocess.PIPE,stderr=subprocess.PIPE,timeout=45)
 def certificates(self):
  tls=self.work/'tls';tls.mkdir(mode=0o700)
  secret=self.args.out/'tls-private';secret.mkdir(mode=0o700)
  ca=tls/'ca.pem';key=secret/'ca-key.pem';server=secret/'server.pem';serverkey=secret/'server-key.pem';csr=secret/'server.csr';ext=secret/'extensions'
  self.local('openssl','req','-x509','-newkey','rsa:2048','-nodes','-days','2','-subj','/CN=Logan-Isolated-CA','-keyout',str(key),'-out',str(ca))
  self.local('openssl','req','-newkey','rsa:2048','-nodes','-subj','/CN=Logan-Isolated-MySQL','-keyout',str(serverkey),'-out',str(csr))
  ext.write_text('basicConstraints=CA:FALSE\nkeyUsage=digitalSignature,keyEncipherment\nextendedKeyUsage=serverAuth\nsubjectAltName=DNS:'+self.mysql+',DNS:'+self.prefix+'-replica,DNS:localhost,IP:127.0.0.1\n')
  self.local('openssl','x509','-req','-in',str(csr),'-CA',str(ca),'-CAkey',str(key),'-CAcreateserial','-days','2','-extfile',str(ext),'-out',str(server))
  wrong=tls/'wrong-ca.pem';wrongkey=secret/'wrong-key.pem'
  self.local('openssl','req','-x509','-newkey','rsa:2048','-nodes','-days','2','-subj','/CN=Logan-Wrong-CA','-keyout',str(wrongkey),'-out',str(wrong))
  self.local('keytool','-importcert','-noprompt','-alias','mysql-fixture','-file',str(ca),'-keystore',str(tls/'truststore.p12'),'-storetype','PKCS12','-storepass','fixture-only')
  self.certvolume=self.reserve('volume',self.prefix+'-certs');self.owned.remove(('volume',self.certvolume));self.owned.insert(0,('volume',self.certvolume))
  self.docker('volume','create','--label',prior.previous.base.LABEL+'='+self.run_id,self.certvolume)
  helper=self.reserve('container',self.prefix+'-certinit')
  script="import pathlib,shutil,os; pairs=[('/ca.pem','ca.pem'),('/secret/server.pem','server.pem'),('/secret/server-key.pem','server-key.pem')];[(shutil.copyfile(a,'/certs/'+b),os.chown('/certs/'+b,999,999),os.chmod('/certs/'+b,0o400)) for a,b in pairs]"
  self.docker('run','--rm','--name',helper,'--label',prior.previous.base.LABEL+'='+self.run_id,'--network','none','--mount','type=bind,src='+str(ca)+',dst=/ca.pem,readonly','--mount','type=bind,src='+str(secret)+',dst=/secret,readonly','--mount','type=volume,src='+self.certvolume+',dst=/certs','python:3.12-alpine','python','-c',script)
  for p in secret.iterdir():p.unlink()
  secret.rmdir()
  self.tls=tls
 def mysql_storage(self):
  self.volume=self.reserve('volume',self.prefix+'-dbdata');self.owned.remove(('volume',self.volume));self.owned.insert(0,('volume',self.volume))
  self.docker('volume','create','--label',prior.previous.base.LABEL+'='+self.run_id,self.volume)
  self.certificates()
  return ['--memory','896m','--cpus','1','--pids-limit','256','--mount','type=volume,src='+self.volume+',dst=/var/lib/mysql','--mount','type=volume,src='+self.certvolume+',dst=/tls,readonly']
 def server_options(self,server_id):
  return ['--server-id='+str(server_id),'--gtid-mode=ON','--enforce-gtid-consistency=ON','--log-replica-updates=ON','--require-secure-transport=ON','--ssl-ca=/tls/ca.pem','--ssl-cert=/tls/server.pem','--ssl-key=/tls/server-key.pem','--innodb-buffer-pool-size=134217728','--max-connections=40']
 def mysql_server_options(self):return self.server_options(1)
 def sql_on(self,host,query):
  _,raw=self.docker('exec','--env','MYSQL_PWD='+self.password,host,'mysql','--protocol=TCP','--host='+host,'--ssl-mode=VERIFY_IDENTITY','--ssl-ca=/tls/ca.pem','--user=root','--batch',*([] if query.startswith('SHOW REPLICA STATUS') else ['--skip-column-names']),'-e',query)
  return raw.strip()
 def sql(self,query):
  if query.startswith('KILL CONNECTION '):
   connection=int(query.split()[-1]);self.dump('tls-before-kill-'+str(connection),self.tls_connections('repair',[connection]))
  return self.sql_on(self.mysql,query)
 def cli(self,*flags,extra=(),ok=True,detached=False):
  dsn=next((x.split('=',1)[1] for x in extra if x.startswith('LOGAN_MYSQL_DSN=')), 'repair:'+self.password+'@tcp('+self.mysql+':3306)/'+self.db+'?parseTime=true')
  dsn=re.sub(r'^root:', 'repair:',dsn)
  # Only this fixture override normalizes inherited plaintext fault DSNs.
  dsn=re.sub(r'([?&])tls=[^&]*',r'\1',dsn).rstrip('?&')
  dsn+=('&' if '?' in dsn else '?')+'tls=true'
  env=['SSL_CERT_FILE=/evidence/tls/ca.pem']+[x for x in extra if not x.startswith('LOGAN_MYSQL_DSN=')]+['LOGAN_MYSQL_DSN='+dsn]
  return prior.previous.Acceptance.cli(self,*flags,extra=env,ok=ok,detached=detached)
 def business(self):
  result={};task=getattr(self,'target',20)
  for key,table,owner in (('task','logan_task','id'),('details','logan_log_detail','task_id')):
   cols=[line.split('\t')[0] for line in self.sql('SHOW COLUMNS FROM '+self.db+'.'+table).splitlines()]
   require(all(re.fullmatch('[a-z_]+',c) for c in cols),'unexpected fixture columns')
   fields=','.join("'%s',`%s`"%(c,c) for c in cols)
   raw=self.sql('SELECT HEX(CAST(JSON_OBJECT('+fields+') AS CHAR CHARACTER SET utf8mb4)) FROM '+self.db+'.'+table+' WHERE '+owner+'='+str(task)+' ORDER BY id')
   result[key]=[json.loads(bytes.fromhex(line).decode()) for line in raw.splitlines()]
  return result
 def tls_connections(self,user,ids=None):
  require(user in ('repair','reader','repl'),'fixture user required')
  where="t.PROCESSLIST_USER='"+user+"'"
  if ids:where+=' AND t.PROCESSLIST_ID IN ('+','.join(str(int(i)) for i in ids)+')'
  q="SELECT t.PROCESSLIST_ID,MAX(IF(s.VARIABLE_NAME='Ssl_version',s.VARIABLE_VALUE,NULL)),MAX(IF(s.VARIABLE_NAME='Ssl_cipher',s.VARIABLE_VALUE,NULL)),t.PROCESSLIST_USER FROM performance_schema.threads t JOIN performance_schema.status_by_thread s ON s.THREAD_ID=t.THREAD_ID WHERE "+where+" GROUP BY t.THREAD_ID,t.PROCESSLIST_ID,t.PROCESSLIST_USER"
  return validate_tls_rows(self.sql(q))
 def verify_reader_tls(self,runtime,container):
  rows=self.tls_connections('reader')
  return {'runtime':runtime,'container':container,'sessions':rows,'certificateMode':'VERIFY_IDENTITY','serverRequiresSecureTransport':True}
 def running(self,client):
  super().running(client)
  # During the held SQL statement, observe the real CLI session rather than admin TLS.
  rows=self.sql("SELECT ID FROM information_schema.PROCESSLIST WHERE USER='repair' AND DB='"+self.db+"'")
  if rows:self.dump('last-cli-tls-observation',self.tls_connections('repair',[int(x) for x in rows.splitlines()]))
 def seed(self,task,device):
  self.sql("USE "+self.db+"; INSERT INTO logan_task(id,platform,device_id,log_file_name,log_date,add_time,status,update_time) VALUES("+str(task)+",1,'"+device+"','history.log',1792022400000,1792022400000,1,'2026-01-01 00:00:00'); INSERT INTO logan_log_detail(id,task_id,log_type,content,log_time,add_time,update_time) VALUES("+str(task*100)+","+str(task)+",1,'old-prefix',1792022400000,'2026-01-01 00:00:00','2026-01-01 00:00:00')")
 def reads(self,phase,task,deleted=()):
  self.target=task;self.read_fixture['deviceId']='ack-compat' if task==20 else 'ha'+str(task);self.read_fixture['deletedDetailIds']=list(deleted)
  return prior.readers.check_reads(self,phase,task,self.business()['details'],'history.log')
 def start_replica(self):
  self.replica=self.reserve('container',self.prefix+'-replica')
  volume=self.reserve('volume',self.prefix+'-repdata');self.owned.remove(('volume',volume));self.owned.insert(0,('volume',volume))
  self.docker('volume','create','--label',prior.previous.base.LABEL+'='+self.run_id,volume)
  self.docker('run','-d','--name',self.replica,'--label',prior.previous.base.LABEL+'='+self.run_id,'--network',self.network,'--memory','896m','--cpus','1','--pids-limit','256','--env-file',str(self.args.out/'mysql.env'),'--mount','type=volume,src='+volume+',dst=/var/lib/mysql','--mount','type=volume,src='+self.certvolume+',dst=/tls,readonly','mysql:8.4.11',*self.server_options(2))
  deadline=time.monotonic()+120
  while True:
   try:
    if self.sql_on(self.replica,'SELECT 1')=='1':break
   except prior.previous.base.DockerError:pass
   require(time.monotonic()<deadline,'replica TCP TLS readiness timeout');time.sleep(.5)
  # Official image initialization differs per node. Replicate only the complete isolated schema;
  # accounts are created explicitly on both nodes, and relay/admin state is not business data.
  self.sql_on(self.replica,"SET SESSION sql_log_bin=0; SET GLOBAL super_read_only=OFF; SET GLOBAL read_only=OFF; CREATE USER 'repair'@'%' IDENTIFIED BY '"+self.password+"' REQUIRE SSL; GRANT ALL ON "+self.db+".* TO 'repair'@'%'; CREATE USER 'reader'@'%' IDENTIFIED BY '"+self.password+"' REQUIRE SSL; GRANT ALL ON "+self.db+".* TO 'reader'@'%'; SET GLOBAL read_only=ON; SET GLOBAL super_read_only=ON")
  self.sql_on(self.replica,"CHANGE REPLICATION FILTER REPLICATE_WILD_DO_TABLE=('"+self.db+".%'); CHANGE REPLICATION SOURCE TO SOURCE_HOST='"+self.primary+"',SOURCE_USER='repl',SOURCE_PASSWORD='"+self.password[:32]+"',SOURCE_AUTO_POSITION=1,SOURCE_SSL=1,SOURCE_SSL_CA='/tls/ca.pem',SOURCE_SSL_VERIFY_SERVER_CERT=1; START REPLICA")
 def caught_up(self):
  gtid=self.sql('SELECT @@GLOBAL.gtid_executed');require(re.fullmatch('[a-f0-9:,\n-]+',gtid),'invalid GTID fixture')
  require(self.sql_on(self.replica,"SELECT WAIT_FOR_EXECUTED_GTID_SET('"+gtid+"',30)")=='0','replica did not catch up')
  raw=self.sql_on(self.replica,'SHOW REPLICA STATUS\\G')
  fields=validate_replica_status(raw)
  self.dump('replication-status',{'status':'passed','gtid':gtid,'replicaStatus':fields,'sourceSessions':self.tls_connections('repl')})
  return gtid
 def wait_transaction(self,client,known=()):
  deadline=time.monotonic()+30
  while True:
   raw=self.sql("SELECT p.ID FROM information_schema.PROCESSLIST p JOIN information_schema.INNODB_TRX t ON t.trx_mysql_thread_id=p.ID JOIN performance_schema.threads auth ON auth.PROCESSLIST_ID=p.ID WHERE auth.PROCESSLIST_USER='repair' AND p.DB='"+self.db+"' AND p.STATE='User sleep' AND t.trx_rows_locked>0")
   ids=[int(x) for x in raw.splitlines() if x and int(x) not in known]
   if len(ids)==1:
    identity=self.sql("SELECT p.ID,p.USER,auth.PROCESSLIST_USER,p.STATE,t.trx_rows_locked FROM information_schema.PROCESSLIST p JOIN information_schema.INNODB_TRX t ON t.trx_mysql_thread_id=p.ID JOIN performance_schema.threads auth ON auth.PROCESSLIST_ID=p.ID WHERE p.ID="+str(ids[0]))
    self.dump('ha-barrier-identity-'+str(ids[0]),{'row':identity})
    self.dump('ha-cli-tls-'+str(ids[0]),self.tls_connections('repair',ids));return ids[0]
   self.dump('ha-barrier-observation',{'knownConnections':list(known),'rows':self.sql("SELECT p.ID,p.USER,p.DB,p.STATE,p.COMMAND,IF(p.USER='repair',LEFT(p.INFO,180),''),t.trx_state,t.trx_rows_locked FROM information_schema.PROCESSLIST p LEFT JOIN information_schema.INNODB_TRX t ON t.trx_mysql_thread_id=p.ID")})
   self.running(client)
   if time.monotonic()>=deadline:
    self.docker('logs',client)
    self.docker('top',client,'-eo','pid,ppid,stat,wchan:40,args')
   require(time.monotonic()<deadline,'HA active transaction barrier timeout');time.sleep(.2)
 def negative_tls(self):
  before=self.snapshot('tls-negative-before');cases=[]
  for name,dsn,ca in (
   ('plaintext','repair:'+self.password+'@tcp('+self.mysql+':3306)/'+self.db+'?parseTime=true&tls=false','ca.pem'),
   ('wrong-ca','repair:'+self.password+'@tcp('+self.mysql+':3306)/'+self.db+'?parseTime=true&tls=true','wrong-ca.pem'),
   ('wrong-host','repair:'+self.password+'@tcp(bad.'+self.mysql+':3306)/'+self.db+'?parseTime=true&tls=true','ca.pem')):
   self.docker('network','disconnect',self.network,self.mysql)
   self.docker('network','connect','--alias','bad.'+self.mysql,self.network,self.mysql)
   result=prior.previous.Acceptance.cli(self,'--task-id','20','--plan','/evidence/negative-'+name+'.json',extra=('LOGAN_MYSQL_DSN='+dsn,'SSL_CERT_FILE=/evidence/tls/'+ca),ok=False)
   require(self.snapshot('tls-negative-'+name)==before,'rejected TLS connection wrote data')
   cases.append({'case':name,'rejected':True,'fullSnapshotUnchanged':True})
  self.dump('tls-negative',{'status':'passed','cases':cases})
 def before_failover(self,plans):
  pass
 def after_failover(self,plans,before,reads,deleted50,context):
  return False
 def execute(self):
  self.setup();self.primary=self.mysql;self.target=20
  self.sql("SET SESSION sql_log_bin=0; CREATE USER 'repair'@'%' IDENTIFIED BY '"+self.password+"' REQUIRE SSL; GRANT ALL ON "+self.db+".* TO 'repair'@'%'; CREATE USER 'reader'@'%' IDENTIFIED BY '"+self.password+"' REQUIRE SSL; GRANT ALL ON "+self.db+".* TO 'reader'@'%'; CREATE USER 'repl'@'%' IDENTIFIED BY '"+self.password[:32]+"' REQUIRE SSL; GRANT REPLICATION SLAVE ON *.* TO 'repl'@'%'")
  self.docker('network','disconnect',self.network,self.oss);self.docker('network','connect','--alias','fixture.'+self.oss,self.network,self.oss)
  self.seed(20,'ack-compat');self.read_fixture={'object_bytes':self.payload,'deviceId':'ack-compat'};self.read_business_snapshot=self.business
  self.read_mysql_tls={'ca':self.tls/'ca.pem','truststore':self.tls/'truststore.p12','password':'fixture-only'};self.mysql_read_user='reader';self.mysql_read_password=self.password
  self.negative_tls();self.checks.append({'check':'TLS_plaintext_wrong_CA_wrong_hostname_rejected','status':'passed'})
  original=self.business();plan,meta=self.plan(20,'tls-fault');reads=[]
  applied=self.fault('t-a','apply','connection',False,plan,meta['operation_id'],original)
  require(self.sql('SELECT @@require_secure_transport')=='1','secure transport must be required')
  reads.append(self.reads('t-a',20));deleted=[r['id'] for r in applied['details'][1:]]
  self.fault('t-r','rollback','connection',False,plan,meta['operation_id'],original);reads.append(self.reads('t-r',20,deleted))
  plans={};deleted50=[]
  for task in (30,40,50):
   self.seed(task,'ha'+str(task));self.target=task;p,m=self.plan(task,'ha-'+str(task));plans[task]=(p,m)
   if task in (40,50):
    self.apply(p,task)
    if task==50:
     deleted50=[r['id'] for r in self.business()['details'][1:]];self.rollback(m['operation_id'])
  self.before_failover(plans)
  self.start_replica()
  self.sql('USE '+self.db+'; CREATE TRIGGER pause_ha_apply BEFORE INSERT ON logan_log_detail FOR EACH ROW SET @logan_ha_pause=IF(NEW.task_id=30,SLEEP(120),0); CREATE TRIGGER pause_ha_rollback BEFORE DELETE ON logan_log_detail FOR EACH ROW SET @logan_ha_pause=IF(OLD.task_id=40,SLEEP(120),0)')
  gtid=self.caught_up();before=self.snapshot('ha-before-fault');source_uuid=self.sql('SELECT @@server_uuid')
  p30,m30=plans[30];p40,m40=plans[40]
  apply_client=self.cli('--action','apply','--task-id','30','--plan','/evidence/'+p30.name,'--allow-isolated-writes','--offline',extra=('LOGAN_MYSQL_DSN=repair:'+self.password+'@tcp('+self.mysql+':3306)/'+self.db+'?parseTime=true&readTimeout=90s',),detached=True)
  a_id=self.wait_transaction(apply_client)
  rollback_client=self.cli('--action','rollback','--operation-id',m40['operation_id'],'--allow-isolated-writes','--offline',extra=('LOGAN_MYSQL_DSN=repair:'+self.password+'@tcp('+self.mysql+':3306)/'+self.db+'?parseTime=true&readTimeout=90s',),detached=True)
  r_id=self.wait_transaction(rollback_client,(a_id,));require(self.snapshot('ha-paused')==before,'uncommitted HA operations became visible')
  self.docker('kill','--signal','KILL',self.primary);_,exitcode=self.docker('wait',self.primary);require(exitcode.strip()=='137','old primary must be fenced by SIGKILL')
  exits=[self.wait_client(c) for c in (apply_client,rollback_client)]
  _,raw=self.docker('inspect',self.primary);state=json.loads(raw)[0]['State'];require(not state['Running'] and not state['OOMKilled'],'old primary must remain stopped and non-OOM')
  require(self.sql_on(self.replica,"SELECT WAIT_FOR_EXECUTED_GTID_SET('"+gtid+"',30)")=='0','confirmed source GTID missing before promotion')
  self.sql_on(self.replica,'STOP REPLICA; SET GLOBAL super_read_only=OFF; SET GLOBAL read_only=OFF');self.mysql=self.replica
  promoted_uuid=self.sql('SELECT @@server_uuid');require(promoted_uuid!=source_uuid,'promotion must use a distinct real server UUID')
  require(self.snapshot('ha-promoted')==before,'promoted business/audit/object snapshot differs from confirmed source')
  refused=[]
  for task in (30,40,50):
   p,m=plans[task];self.target=task;self.apply(p,task,ok=False)
   if task!=30:self.rollback(m['operation_id'],ok=False)
   require(self.snapshot('ha-refused-'+str(task))==before,'cross-UUID refusal mutated data or audit');refused.append(task)
  reads.append(self.reads('h30',30));reads.append(self.reads('h40',40));reads.append(self.reads('h50',50,deleted50))
  if self.after_failover(plans,before,reads,deleted50,{'sourceUUID':source_uuid,'promotedUUID':promoted_uuid,'confirmedGTID':gtid,'sourceExitCode':137,'oldPrimaryFenced':True,'activeApplyConnection':a_id,'activeRollbackConnection':r_id,'clientExitCodes':exits}):return
  self.sql('DROP TRIGGER '+self.db+'.pause_ha_apply; DROP TRIGGER '+self.db+'.pause_ha_rollback')
  self.target=30;newplan,newmeta=self.plan(30,'promoted-new-plan');orig30=self.business();before30=self.snapshot('new-primary-before-apply');self.apply(newplan,30);applied_snapshot=self.snapshot('new-primary-applied');apply_audit=self.verify_audit_snapshots(newmeta['operation_id'],orig30,self.business(),'apply');require(self.apply(newplan,30)['noop'],'new-primary repeat apply must noop');require(self.snapshot('new-primary-repeat-apply')==applied_snapshot,'new-primary repeat apply changed full snapshot')
  applied30=self.business();added30=[r['id'] for r in applied30['details'][1:]];reads.append(self.reads('n-a',30))
  self.rollback(newmeta['operation_id']);restored_snapshot=self.snapshot('new-primary-rolled-back');rollback_audit=self.verify_audit_snapshots(newmeta['operation_id'],orig30,self.business(),'rollback');require(all(restored_snapshot['tables'][t]==v for t,v in before30['tables'].items() if t!='logan_native_reparse_operation') and restored_snapshot['objects']==before30['objects'],'new-primary rollback changed original full business state');require(self.rollback(newmeta['operation_id'])['noop'],'new-primary repeat rollback must noop');require(self.snapshot('new-primary-repeat-rollback')==restored_snapshot,'new-primary repeat rollback changed full snapshot');require(self.business()==orig30,'new-primary rollback did not restore complete original prefix');reads.append(self.reads('n-r',30,added30));self.dump('new-primary-audit',{'operationId':newmeta['operation_id'],'applyAudit':apply_audit,'rollbackAudit':rollback_audit,'repeatApplyAndRollbackFullSnapshotsUnchanged':True})
  self.dump('ha-promotion',{'status':'passed','sourceUUID':source_uuid,'promotedUUID':promoted_uuid,'confirmedGTID':gtid,'sourceExitCode':137,'oldPrimaryFenced':True,'activeApplyConnection':a_id,'activeRollbackConnection':r_id,'clientExitCodes':exits,'fullPromotedSnapshotMatches':True,'oldPlansAndAuditsCrossUUIDRefusedForTasks':refused,'newPrimaryFreshPlanApplyRollbackPassed':True,'crossUUIDAutomaticRecoverySupported':False})
  self.dump('real-reader-summary',{'status':'passed','phases':reads,'TLSVerifiedTargetClientSessions':True})
  self.checks.append({'check':'TLS_GTid_replication_promotion_consistency_UUID_fence_and_new_plan_recovery','status':'passed'})

def main():
 p=argparse.ArgumentParser(description=__doc__)
 p.add_argument('--docker',required=True);p.add_argument('--host',required=True);p.add_argument('--allow-isolated-runtime',action='store_true');p.add_argument('--binary',type=Path,required=True);p.add_argument('--go-image',required=True);p.add_argument('--out',type=Path,required=True);p.add_argument('--work-dir',type=Path,required=True)
 a=prior.previous.validate_args(p.parse_args())
 for d in (a.out,a.work_dir):d.mkdir(mode=0o700,parents=True)
 a.out=a.out.resolve();a.work_dir=a.work_dir.resolve();v=TLSFailover(a);code=0;error=None
 signal.signal(signal.SIGTERM,prior.previous.interrupted)
 try:v.execute()
 except BaseException as e:code=prior.previous.runtime.classify_exit(e,v.engine_ready);error=type(e).__name__+': '+str(e).replace(v.password,'<synthetic-secret>')
 finally:
  code=prior.finish(v,a,code,error)
  for folder in (a.out/'tls-private',a.work_dir/'tls'):
   if folder.exists():
    for f in folder.iterdir():f.unlink()
    folder.rmdir()
  path=a.out/'summary.json';report=json.loads(path.read_text());report['scope']='verified TLS and real asynchronous GTID replica promotion; UUID fence intentionally refuses old operations; fresh-plan recovery on new primary; isolated only, no automatic HA/production/cloud';v.dump('summary',report)
 return code
if __name__=='__main__':sys.exit(main())
