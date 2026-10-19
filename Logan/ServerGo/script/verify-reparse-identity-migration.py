#!/usr/bin/env python3
"""Isolated audited single-operation identity migration across real TLS GTID failover."""
import argparse
import concurrent.futures
import hashlib
import importlib.util
import json
from pathlib import Path
import signal
import sys

ROOT=Path(__file__).resolve().parents[1]
spec=importlib.util.spec_from_file_location('tls_failover',ROOT/'script/verify-reparse-tls-failover.py')
base=importlib.util.module_from_spec(spec);spec.loader.exec_module(base)
require=base.require
AUTH='logan_native_reparse_identity_authorization'

def unchanged_except_authorization(before,after):
 require(before['objects']==after['objects'],'identity migration changed objects')
 require(before['tables'].keys()==after['tables'].keys(),'identity migration changed table inventory')
 require(all(after['tables'][t]==v for t,v in before['tables'].items() if t!=AUTH),'identity migration changed business or original audit')

class IdentityAcceptance(base.TLSFailover):
 def setup(self):
  super().setup()
  self.sql('USE '+self.db+'; '+(ROOT/'migrations/004_native_reparse_identity.sql').read_text())
 def before_failover(self,plans):
  self.witnesses={};self.plan_hashes={}
  before=self.snapshot('before-source-witnesses')
  for task,(p,m) in plans.items():
   witness=self.work/('identity-'+str(task)+'.json')
   metadata=self.cli('--action','identity-export','--task-id',str(task),'--plan','/evidence/'+p.name,'--witness','/evidence/'+witness.name,'--offline')
   require(metadata['operation_id']==m['operation_id'] and len(metadata['digest'])==64,'source witness output mismatch')
   self.witnesses[task]=(witness,metadata)
   self.plan_hashes[task]=hashlib.sha256(p.read_bytes()).hexdigest()
   raw=witness.read_bytes();require(b'old-prefix' not in raw and b'duplicate-tail' not in raw,'witness contains raw log body')
  self.alternative=self.work/'identity-30-alternative.json'
  self.alternative_meta=self.cli('--action','identity-export','--task-id','30','--plan','/evidence/'+plans[30][0].name,'--witness','/evidence/'+self.alternative.name,'--offline')
  require(self.snapshot('after-source-witnesses')==before,'source witness export wrote database/object data')
  self.source_identity=self.sql('SELECT @@server_uuid')+'/'+self.db
  self.dump('source-witnesses',{'status':'passed','originalPlanSHA256':self.plan_hashes,'witnesses':{str(k):v[1] for k,v in self.witnesses.items()},'sourceDatabaseUnchanged':True})
 def identity_flags(self,task,action='identity-migrate',witness=None,digest=None):
  path,meta=self.witnesses[task]
  args=['--action',action,'--witness','/evidence/'+(witness or path).name,'--witness-sha256',digest or meta['digest'],'--expect-source',self.source_identity,'--expect-target',self.target_identity,'--offline']
  if action=='identity-migrate':args+=['--allow-isolated-writes','--allow-identity-migration','--source-fenced']
  return args
 def migrate(self,task,action='identity-migrate',**kw):return self.cli(*self.identity_flags(task,action),**kw)
 def rejection(self,name,flags):
  before=self.snapshot(name+'-before');self.cli(*flags,ok=False);require(self.snapshot(name+'-after')==before,'rejected identity operation wrote data')
 def changed_witness(self,task,name,change):
  original,meta=self.witnesses[task];obj=json.loads(original.read_text());change(obj)
  obj['digest']='';digest=hashlib.sha256(json.dumps(obj,ensure_ascii=False,separators=(',',':')).encode()).hexdigest();obj['digest']=digest
  path=self.work/(name+'.json');path.write_text(json.dumps(obj,separators=(',',':')));path.chmod(0o600)
  return path,digest
 def after_authorizations(self):
  pass
 def after_recovery(self):
  pass
 def after_failover(self,plans,before,reads,deleted50,context):
  self.target_identity=self.sql('SELECT @@server_uuid')+'/'+self.db
  before=self.snapshot('identity-target-before-negatives')
  self.sql('DROP TRIGGER '+self.db+'.pause_ha_apply; DROP TRIGGER '+self.db+'.pause_ha_rollback')
  context['crossUUIDUnauthorisedRefused']=True
  # Trusted source digests are supplied separately; corrupt/incorrect proofs cannot grant access.
  flags=self.identity_flags(30)
  for flag in ('--source-fenced','--allow-identity-migration','--offline'):
   self.rejection('missing-'+flag[2:],[f for f in flags if f!=flag])
  bad=list(flags);bad[bad.index('--witness-sha256')+1]='0'*64;self.rejection('wrong-trusted-digest',bad)
  bad=list(flags);bad[bad.index('--expect-target')+1]=self.source_identity;self.rejection('wrong-target-identity',bad)
  bad=list(flags);bad[bad.index('--expect-target')+1]='aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa/'+self.db;self.rejection('actual-target-mismatch',bad)
  bad=list(flags);bad[bad.index('--expect-source')+1]=self.target_identity;self.rejection('wrong-source-identity',bad)
  proof,digest=self.changed_witness(30,'unknown-gtid',lambda w:w.update(source_gtid='ffffffff-ffff-ffff-ffff-ffffffffffff:1'))
  self.rejection('missing-source-gtid',self.identity_flags(30,witness=proof,digest=digest))
  # Exact source/target archive and business checks, with full-state refusal witnesses.
  self.sql("UPDATE "+self.db+".logan_log_detail SET content='manual-identity-conflict' WHERE id=4000")
  self.rejection('changed-target-business',self.identity_flags(40))
  self.sql("UPDATE "+self.db+".logan_log_detail SET content='old-prefix',add_time='2026-01-01 00:00:00',update_time='2026-01-01 00:00:00' WHERE id=4000")
  op40=plans[40][1]['operation_id'];archived=self.sql("SELECT HEX(before_snapshot) FROM "+self.db+".logan_native_reparse_operation WHERE operation_id='"+op40+"'")
  self.sql("UPDATE "+self.db+".logan_native_reparse_operation SET before_snapshot='{}' WHERE operation_id='"+op40+"'")
  self.rejection('changed-target-audit-archive',self.identity_flags(40))
  self.sql("UPDATE "+self.db+".logan_native_reparse_operation SET before_snapshot=CONVERT(UNHEX('"+archived+"') USING utf8mb4) WHERE operation_id='"+op40+"'")
  require(self.snapshot('after-restoring-negative-fixtures')==before,'negative fixture restoration differs from promoted original')
  for task in (30,40,50):
   b=self.snapshot('identity-preview-'+str(task)+'-before');result=self.migrate(task,'identity-dry-run');require(result['dry_run'] is True,'migration preview did not report dry-run');require(self.snapshot('identity-preview-'+str(task)+'-after')==b,'identity preview wrote data')
  self.sql('USE '+self.db+'; CREATE TRIGGER coerce_identity_grant BEFORE INSERT ON '+AUTH+" FOR EACH ROW SET NEW.witness_digest=REPEAT('0',64)")
  self.rejection('coerced-authorization-publication',self.identity_flags(30))
  self.sql('DROP TRIGGER '+self.db+'.coerce_identity_grant')
  self.sql('USE '+self.db+'; CREATE TRIGGER mutate_business_during_identity BEFORE INSERT ON '+AUTH+" FOR EACH ROW UPDATE logan_task SET amount='trigger-coercion' WHERE id=30")
  self.rejection('identity-trigger-business-publication',self.identity_flags(30))
  self.sql('DROP TRIGGER '+self.db+'.mutate_business_during_identity')
  # Crash the promoted target before the authorization INSERT is published.
  self.sql('USE '+self.db+'; CREATE TRIGGER pause_identity_grant BEFORE INSERT ON '+AUTH+" FOR EACH ROW SET @logan_identity_pause=IF(NEW.operation_id='"+plans[30][1]['operation_id']+"',SLEEP(120),0)")
  b=self.snapshot('identity-crash-before');client=self.cli(*self.identity_flags(30),extra=('LOGAN_MYSQL_DSN=repair:'+self.password+'@tcp('+self.mysql+':3306)/'+self.db+'?parseTime=true&readTimeout=90s',),detached=True)
  connection=self.wait_transaction(client)
  _,info=self.docker('inspect',self.mysql);mounts=json.loads(info)[0]['Mounts'];self.volume=next(m['Name'] for m in mounts if m['Destination']=='/var/lib/mysql' and m['Type']=='volume')
  crash=self.crash_restart();self.sql('STOP REPLICA');exit_code=self.wait_client(client)
  self.sql('DROP TRIGGER '+self.db+'.pause_identity_grant')
  require(self.snapshot('identity-crash-recovered')==b,'failed identity transaction left authorization/business/audit writes')
  migration30=self.migrate(30);require(migration30['noop'] is False,'uncommitted identity grant retry must create authorization')
  after=self.snapshot('identity-30-granted');unchanged_except_authorization(b,after)
  require(self.migrate(30)['noop'] is True,'same identity migration must noop');require(self.snapshot('identity-30-repeat')==after,'repeat migration wrote data')
  self.dump('identity-grant-crash',{'status':'passed','connection':connection,'targetCrash':crash,'clientExitCode':exit_code,'partialWritesAbsent':True,'retry':migration30,'repeatFullSnapshotUnchanged':True})
  self.rejection('conflicting-witness-same-operation',self.identity_flags(30,witness=self.alternative,digest=self.alternative_meta['digest']))
  # Concurrent requests may deadlock and safely fail, but retries must converge to one exact grant.
  b=self.snapshot('identity-40-before-concurrent')
  with concurrent.futures.ThreadPoolExecutor(2) as pool:results=list(pool.map(lambda _:self.migrate(40,ok=None),range(2)))
  require(any(isinstance(r,dict) for r in results),'no concurrent migration succeeded')
  self.migrate(40);after=self.snapshot('identity-40-after-concurrent');unchanged_except_authorization(b,after)
  require(self.migrate(40)['noop'] is True and self.snapshot('identity-40-repeat')==after,'concurrent grant did not converge idempotently')
  b=self.snapshot('identity-50-before');self.migrate(50);after=self.snapshot('identity-50-granted');unchanged_except_authorization(b,after)
  require(self.migrate(50)['noop'] is True and self.snapshot('identity-50-repeat')==after,'rolled-back identity grant not idempotent')
  require(self.sql('SELECT COUNT(*) FROM '+self.db+'.'+AUTH)=='3','authorization count differs from exact three operations')
  self.after_authorizations()
  recovery=[]
  for task in (30,40,50):
   self.target=task;plan,meta=plans[task];original_audit=self.audit_optional(meta['operation_id']);original_business=self.business()
   if task==30:
    result=self.apply(plan,task);require(result['operation_id']==meta['operation_id'] and not result['noop'],'pending old plan did not execute with original operation ID')
    appended=[r['id'] for r in self.business()['details'][1:]];self.verify_audit_snapshots(meta['operation_id'],original_business,self.business(),'apply')
    reads.append(self.reads('m30a',task));result_snapshot=self.snapshot('old30-applied')
    require(self.apply(plan,task)['noop'] and self.migrate(task)['noop'],'old30 repeated apply/migration did not noop');require(self.snapshot('old30-repeat-applied')==result_snapshot,'old30 repeat changed full state')
    self.rollback(meta['operation_id']);self.verify_audit_snapshots(meta['operation_id'],original_business,self.business(),'rollback');reads.append(self.reads('m30r',task,appended))
   elif task==40:
    b=self.snapshot('old40-before-repeat');require(self.apply(plan,task)['noop'],'old applied operation must retry noop');require(self.snapshot('old40-applied-noop')==b,'old applied retry wrote data');reads.append(self.reads('m40a',task))
    appended=[r['id'] for r in self.business()['details'][1:]]
    self.sql("UPDATE "+self.db+".logan_log_detail SET content='post-authorization-conflict' WHERE id=4000")
    b=self.snapshot('old40-conflict-before');self.rollback(meta['operation_id'],ok=False);require(self.snapshot('old40-conflict-after')==b,'authorization bypassed snapshot rollback protection')
    self.sql("UPDATE "+self.db+".logan_log_detail SET content='old-prefix',add_time='2026-01-01 00:00:00',update_time='2026-01-01 00:00:00' WHERE id=4000")
    self.rollback(meta['operation_id']);reads.append(self.reads('m40r',task,appended))
   else:
    b=self.snapshot('old50-before-noop');require(self.rollback(meta['operation_id'])['noop'],'old rolled-back operation must retry noop');require(self.snapshot('old50-after-noop')==b,'old rolled-back retry wrote data');reads.append(self.reads('m50r',task,deleted50))
   b=self.snapshot('old'+str(task)+'-before-final-retries');require(self.rollback(meta['operation_id'])['noop'] and self.migrate(task)['noop'],'repeated rollback/migration must noop after valid state advancement');require(self.snapshot('old'+str(task)+'-after-final-retries')==b,'final retries changed full state')
   audit=self.audit(meta['operation_id']);require(self.sql("SELECT database_identity FROM "+self.db+".logan_native_reparse_operation WHERE operation_id='"+meta['operation_id']+"'")==self.source_identity,'old audit origin identity was rewritten')
   require(hashlib.sha256(plan.read_bytes()).hexdigest()==self.plan_hashes[task],'old plan bytes changed')
   recovery.append({'task':task,'operationId':meta['operation_id'],'originalPlanUnchanged':True,'originalAuditIdentityPreserved':True,'finalAudit':audit,'finalBusiness':self.business()})
  self.after_recovery()
  self.dump('identity-recovery',{'status':'passed','context':context,'targetIdentity':self.target_identity,'sourceIdentity':self.source_identity,'operations':recovery,'concurrentMigrationResults':results,'controlledCrossUUIDRecovery':True,'automaticFencing':False})
  self.dump('real-reader-summary',{'status':'passed','phases':reads,'TLSVerifiedTargetClientSessions':True})
  self.checks.append({'check':'controlled_identity_migration_original_operations_recovery_rollback_and_actual_TLS_reads','status':'passed'})
  return True

def main():
 p=argparse.ArgumentParser(description=__doc__)
 p.add_argument('--docker',required=True);p.add_argument('--host',required=True);p.add_argument('--allow-isolated-runtime',action='store_true');p.add_argument('--binary',type=Path,required=True);p.add_argument('--go-image',required=True);p.add_argument('--out',type=Path,required=True);p.add_argument('--work-dir',type=Path,required=True)
 a=base.prior.previous.validate_args(p.parse_args())
 for d in (a.out,a.work_dir):d.mkdir(mode=0o700,parents=True)
 a.out=a.out.resolve();a.work_dir=a.work_dir.resolve();v=IdentityAcceptance(a);code=0;error=None
 signal.signal(signal.SIGTERM,base.prior.previous.interrupted)
 try:v.execute()
 except BaseException as e:code=base.prior.previous.runtime.classify_exit(e,v.engine_ready);error=type(e).__name__+': '+str(e).replace(v.password,'<synthetic-secret>')
 finally:
  code=base.prior.finish(v,a,code,error)
  for folder in (a.out/'tls-private',a.work_dir/'tls'):
   if folder.exists():
    for f in folder.iterdir():f.unlink()
    folder.rmdir()
  report=json.loads((a.out/'summary.json').read_text());report['scope']='single-operation trusted-witness identity authorization after true TLS GTID failover; original plan/audit identity retained; isolated synthetic data only, no production/cloud/cutover';v.dump('summary',report)
 return code
if __name__=='__main__':sys.exit(main())
