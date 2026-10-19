#!/usr/bin/env python3
"""Opt-in offline Native repair acceptance on a dedicated labelled Docker engine.
No production credentials, public ports, automatic migration, or ambient context.
"""
import argparse, base64, concurrent.futures, gzip, hashlib, importlib.util, json, os
from pathlib import Path
import sys, time, uuid, signal, re

HERE = Path(__file__).resolve().parent
spec = importlib.util.spec_from_file_location('runtime', HERE / 'verify-runtime-recovery.py')
runtime = importlib.util.module_from_spec(spec); spec.loader.exec_module(runtime)
base = runtime.base
require = base.require

OSS = '''from http.server import BaseHTTPRequestHandler,ThreadingHTTPServer
from pathlib import Path
class H(BaseHTTPRequestHandler):
 def do_GET(self):
  p=Path('/objects') / self.path.split('?')[0].split('/')[-1]
  b=p.read_bytes() if p.is_file() else b''
  self.send_response(200 if p.is_file() else 404);self.send_header('Content-Length',str(len(b)));self.end_headers();self.wfile.write(b)
 def log_message(self,*a):pass
ThreadingHTTPServer(('0.0.0.0',8080),H).serve_forever()
'''

def merge_cli_environment(content, extra):
    env = {}
    for line in content.splitlines() + list(extra):
        if not line:
            continue
        key, sep, value = line.partition('=')
        require(sep and re.fullmatch('[A-Z_][A-Z0-9_]*', key), 'invalid fixture environment entry')
        env[key] = value
    return ''.join(key+'='+value+'\n' for key,value in env.items())

class Acceptance(runtime.RuntimeVerifier):
    def log(self, command, output, code):
        path=self.args.out / ("command-"+uuid.uuid4().hex+".json")
        path.write_text(json.dumps({"argv":[x.replace(self.password,"<synthetic-secret>") for x in command],"exit_code":code,"output":output.replace(self.password,"<synthetic-secret>")},indent=2)+"\n")
        path.chmod(0o600)

    def object(self,name,data):
        self.docker('exec',self.oss,'python','-c',"import pathlib,base64,sys;pathlib.Path('/objects',sys.argv[1]).write_bytes(base64.b64decode(sys.argv[2]))",name,base64.b64encode(data).decode())

    def snapshot(self, name):
        tables = self.sql('SHOW TABLES FROM '+self.db).splitlines()
        value = {t:self.sql('SELECT * FROM '+self.db+'.`'+t+'` ORDER BY 1') for t in tables}
        _,raw=self.docker('exec',self.oss,'python','-c',"import pathlib,hashlib,json;print(json.dumps({p.name:hashlib.sha256(p.read_bytes()).hexdigest() for p in pathlib.Path('/objects').iterdir()}))")
        objects=json.loads(raw)
        data={'tables':value,'objects':objects}
        path=self.args.out/(name+'.json');path.write_text(json.dumps(data,indent=2)+'\n');path.chmod(0o600)
        return data

    def cli(self, *flags, extra=(), ok=True, detached=False):
        # Private plans need CLI ownership to match the bind-mounted host owner.
        name=self.reserve('container',self.prefix+'-cli-'+uuid.uuid4().hex[:8])
        run=['run','--rm','--name',name]
        if detached:
            run=['run','-d','--name',name]
        cli_env=self.private('cli-'+uuid.uuid4().hex+'.env',merge_cli_environment(self.envfile.read_text(),extra))
        code,out=self.docker(*run,'--label',base.LABEL+'='+self.run_id,
            '--network',self.network,'--user',str(os.getuid())+':'+str(os.getgid()),
            '--read-only','--cap-drop','ALL','--security-opt','no-new-privileges:true',
            '--tmpfs','/tmp:rw,nosuid,nodev,size=134217728,mode=1777',
            '--mount','type=bind,src='+str(self.args.binary)+',dst=/tool,readonly',
            '--mount','type=bind,src='+str(self.work)+',dst=/evidence',
            '--env-file',str(cli_env),
            '--entrypoint','/tool',self.args.go_image,*flags,accept=True)
        if detached:return name
        require(ok is None or (code==0)==ok,'unexpected CLI exit: '+out)
        return json.loads(out) if code==0 else out

    def plan(self, task=1, name='plan', extra=()):
        p=self.work/(name+'.json')
        result=self.cli('--task-id',str(task),'--plan','/evidence/'+p.name,extra=extra)
        return p,result

    def apply(self,p,task=1,ok=True,detached=False):
        return self.cli('--action','apply','--task-id',str(task),'--plan','/evidence/'+p.name,'--allow-isolated-writes','--offline',ok=ok,detached=detached)

    def rollback(self,op,ok=True):
        return self.cli('--action','rollback','--operation-id',op,'--allow-isolated-writes','--offline',ok=ok)

    def mysql_storage(self):
        return ['--tmpfs','/var/lib/mysql:rw,size=536870912']

    def mysql_server_options(self):
        return []

    def setup(self):
        self.docker('info'); self.engine_ready=True
        for image in ('mysql:8.4.11','python:3.12-alpine',self.args.go_image):self.docker('image','inspect',image)
        self.work=self.args.work_dir; self.objects=self.work/'objects';self.objects.mkdir()
        self.network=self.reserve('network',self.prefix+'-net')
        self.docker('network','create','--internal','--label',base.LABEL+'='+self.run_id,self.network)
        self.mysql=self.reserve('container',self.prefix+'-mysql')
        credentials=self.private('mysql.env','MYSQL_ROOT_PASSWORD='+self.password+'\nMYSQL_ROOT_HOST=%\n')
        self.docker('run','-d','--name',self.mysql,'--label',base.LABEL+'='+self.run_id,'--network',self.network,
            '--env-file',str(credentials),*self.mysql_storage(),'mysql:8.4.11',*self.mysql_server_options())
        deadline=time.monotonic()+120
        while True:
            try:
                if self.sql('SELECT 1')=='1':break
            except base.DockerError:pass
            require(time.monotonic()<deadline,'MySQL readiness timeout');time.sleep(.5)
        self.db='logan_reparse_it_'+self.run_id
        schema='\n'.join((base.ROOT/p).read_text() for p in ('tests/integration/mysql/java_schema.sql','migrations/001_web_processing.sql','migrations/002_maintenance.sql','migrations/003_native_reparse.sql'))
        self.sql('CREATE DATABASE '+self.db+';\nUSE '+self.db+';\n'+schema)
        oss=self.reserve('container',self.prefix+'-oss');self.oss=oss
        self.docker('run','-d','--name',oss,'--label',base.LABEL+'='+self.run_id,'--network',self.network,
             '--mount','type=bind,src='+str(self.objects)+',dst=/objects','python:3.12-alpine','python','-c',OSS)
        self.envfile=self.private('cli.env','\n'.join(['LOGAN_NATIVE_TEMP_DIR=/tmp','LOGAN_REPARSE_ISOLATED=YES','LOGAN_MYSQL_DSN=root:'+self.password+'@tcp('+self.mysql+':3306)/'+self.db+'?parseTime=true',
            'LOGAN_OSS_ENDPOINT=http://'+oss+':8080','LOGAN_OSS_REGION=fixture','LOGAN_OSS_BUCKET=fixture','LOGAN_OSS_ACCESS_KEY_ID=fixture-only','LOGAN_OSS_ACCESS_KEY_SECRET=fixture-only','LOGAN_OSS_PATH_STYLE=true'])+'\n')
        # Stored decoded gzip: first member is the Java-visible prefix. Duplicate tail
        # records must remain duplicated. Seed is explicit, not a Java execution claim.
        self.records=[{'c':'old-prefix','f':1,'l':1792022400000},{'c':'duplicate-tail','f':2,'l':1792022401000},{'c':'duplicate-tail','f':2,'l':1792022401000}]
        first=gzip.compress((json.dumps(self.records[0])+'\n').encode(),mtime=0)
        tail=gzip.compress(''.join(json.dumps(r)+'\n' for r in self.records[1:]).encode(),mtime=0)
        payload=first+b'\0'*(16-len(first)%16)+tail
        self.payload=payload
        self.object('history.log',payload)
        for task in range(1,9):
            self.sql("USE "+self.db+"; INSERT INTO logan_task(id,platform,log_file_name,add_time,status,update_time) VALUES(%d,1,'history.log',1792022400000,1,'2026-01-01 00:00:00'); INSERT INTO logan_log_detail(id,task_id,log_type,content,log_time,add_time,update_time) VALUES(%d,%d,1,'old-prefix',1792022400000,'2026-01-01 00:00:00','2026-01-01 00:00:00');"%(task,task*100,task))
    def execute(self):
        self.setup()
        payload=self.payload
        before=self.snapshot('before-default-dry-run');p,meta=self.plan();require(meta['append_records']==2,'expected duplicate tail')
        require(self.snapshot('after-default-dry-run')==before,'dry-run mutated database/object');self.checks.append({'check':'default_dry_run_full_state_unchanged','status':'passed'})
        self.cli('--action','apply','--task-id','1','--plan','/evidence/'+p.name,ok=False)
        tampered=self.work/'tampered.json';bad=json.loads(p.read_text());bad['append_records']+=1;tampered.write_text(json.dumps(bad));tampered.chmod(0o600)
        self.apply(tampered,ok=False);require(self.snapshot('after-tampered-plan')==before,'tampered plan wrote data')
        self.apply(p);after=self.snapshot('after-apply')
        require(after['tables']['logan_task']==before['tables']['logan_task'],'original task fields/timestamps/status changed')
        require(set(before['tables']['logan_log_detail'].splitlines())<=set(after['tables']['logan_log_detail'].splitlines()),'an original detail field changed')
        require(self.sql('SELECT COUNT(*) FROM '+self.db+'.logan_log_detail WHERE task_id=1')=='3','tail missing')
        require(self.sql("SELECT COUNT(*) FROM "+self.db+".logan_log_detail WHERE id=100 AND content='old-prefix' AND add_time='2026-01-01 00:00:00' AND update_time='2026-01-01 00:00:00'")=='1','prefix changed')
        require(self.apply(p)['noop'],'repeat apply must noop');require(self.snapshot('after-repeat-apply')==after,'repeat mutation')
        self.rollback(meta['operation_id']); restored=self.snapshot('after-rollback')
        require(all(restored['tables'][t]==v for t,v in before['tables'].items() if t!='logan_native_reparse_operation') and restored['objects']==before['objects'],'rollback did not restore original data')
        require(self.rollback(meta['operation_id'])['noop'],'repeat rollback noop required')
        p2,m2=self.plan(2,'concurrent');
        with concurrent.futures.ThreadPoolExecutor(2) as pool:list(pool.map(lambda _:self.apply(p2,2),range(2)))
        require(self.sql('SELECT COUNT(*) FROM '+self.db+'.logan_log_detail WHERE task_id=2')=='3','concurrent apply duplicates')
        self.sql('UPDATE '+self.db+".logan_log_detail SET content='manual-change' WHERE task_id=2 AND id=200")
        snap=self.snapshot('before-refused-rollback');self.rollback(m2['operation_id'],ok=False);require(self.snapshot('after-refused-rollback')==snap,'refused rollback wrote data')
        p3,_=self.plan(3,'stale');self.sql('UPDATE '+self.db+".logan_log_detail SET content='conflict' WHERE task_id=3")
        snap=self.snapshot('before-stale-plan');self.apply(p3,3,ok=False);require(self.snapshot('after-stale-plan')==snap,'stale apply wrote data')
        self.cli('--task-id','3','--plan','/evidence/conflict.json',ok=False)
        p4,_=self.plan(4,'changed-object');self.object('history.log',payload+b'bad')
        snap=self.snapshot('before-changed-object');self.apply(p4,4,ok=False);require(self.snapshot('after-changed-object')==snap,'changed object apply wrote data')
        self.object('history.log',payload)
        self.sql("UPDATE "+self.db+".logan_task SET log_file_name='different-object.log' WHERE id=4")
        snap=self.snapshot('before-wrong-object-binding');self.apply(p4,4,ok=False)
        require(self.snapshot('after-wrong-object-binding')==snap,'wrong object binding wrote data')
        refusal=self.snapshot('before-resource-refusals')
        self.cli('--task-id','5','--plan','/evidence/limits.json',extra=('LOGAN_NATIVE_QUERY_MAX_RECORDS=1',),ok=False)
        corrupt=bytearray(payload);corrupt[-8]^=1;self.object('history.log',corrupt)
        self.cli('--task-id','5','--plan','/evidence/corrupt.json',ok=False);self.object('history.log',payload)
        require(self.snapshot('after-resource-refusals')==refusal,'resource refusal wrote data')
        p6,_=self.plan(6,'forced-failure')
        self.sql("USE "+self.db+"; CREATE TRIGGER refuse_second_tail BEFORE INSERT ON logan_log_detail FOR EACH ROW SET NEW.log_type=IF(NEW.task_id=6 AND (SELECT COUNT(*) FROM logan_log_detail WHERE task_id=6)>=2,NULL,NEW.log_type)")
        snap=self.snapshot('before-transaction-failure');self.apply(p6,6,ok=False)
        require(self.snapshot('after-transaction-failure')==snap,'failed transaction left partial detail/audit writes')
        self.sql('DROP TRIGGER '+self.db+'.refuse_second_tail')
        p7,_=self.plan(7,'different-operation-a');q7,_=self.plan(7,'different-operation-b')
        with concurrent.futures.ThreadPoolExecutor(2) as pool:list(pool.map(lambda p:self.apply(p,7,ok=None),(p7,q7)))
        require(self.sql('SELECT COUNT(*) FROM '+self.db+'.logan_log_detail WHERE task_id=7')=='3','different operations duplicated tail')
        p8,_=self.plan(8,'trigger-coercion')
        self.sql("USE "+self.db+"; CREATE TRIGGER coerce_tail BEFORE INSERT ON logan_log_detail FOR EACH ROW SET NEW.content=IF(NEW.task_id=8,'coerced',NEW.content)")
        snap=self.snapshot('before-coercion');self.apply(p8,8,ok=False)
        require(self.snapshot('after-coercion')==snap,'coerced insert committed')
        self.sql('DROP TRIGGER '+self.db+'.coerce_tail')
        oversized=gzip.compress((''.join(json.dumps(r)+'\n' for r in [self.records[0],{'c':'x'*70000,'f':2,'l':1792022401000}])).encode(),mtime=0)
        self.object('oversize.log',oversized)
        self.sql("USE "+self.db+"; INSERT INTO logan_task(id,platform,log_file_name,add_time,status) VALUES(9,1,'oversize.log',1792022400000,1); INSERT INTO logan_log_detail(task_id,log_type,content,log_time) VALUES(9,1,'old-prefix',1792022400000); SET GLOBAL sql_mode=''")
        p9,_=self.plan(9,'nonstrict-text');snap=self.snapshot('before-nonstrict-text');self.apply(p9,9,ok=False)
        require(self.snapshot('after-nonstrict-text')==snap,'non-strict TEXT truncation committed')
        self.checks.append({'check':'postinsert_trigger_coercion_and_nonstrict_text_refusal','status':'passed'})
        self.sql("USE "+self.db+"; INSERT INTO logan_task(id,platform,log_file_name,add_time,status) VALUES(10,1,'history.log',1792022400000,1); INSERT INTO logan_log_detail(task_id,log_type,content,log_time) VALUES(10,1,'old-prefix',1792022400000)")
        p10,_=self.plan(10,'interrupted');snap=self.snapshot('before-interrupted-apply')
        self.sql("USE "+self.db+"; CREATE TRIGGER pause_insert BEFORE INSERT ON logan_log_detail FOR EACH ROW SET @logan_pause=IF(NEW.task_id=10,SLEEP(8),0)")
        killed=self.apply(p10,10,detached=True);deadline=time.monotonic()+30
        while self.sql("SELECT COUNT(*) FROM information_schema.PROCESSLIST WHERE STATE='User sleep'")=='0':
            require(time.monotonic()<deadline,'insert sleep barrier not reached');time.sleep(.2)
        self.docker('kill','--signal','KILL',killed)
        _,exitcode=self.docker('wait',killed);require(exitcode.strip()=='137','expected SIGKILL exit')
        deadline=time.monotonic()+30
        while self.sql("SELECT COUNT(*) FROM information_schema.PROCESSLIST WHERE STATE='User sleep'")!='0':
            require(time.monotonic()<deadline,'killed connection rollback timed out');time.sleep(.2)
        require(self.snapshot('after-interrupted-apply')==snap,'SIGKILL before commit left writes')
        self.sql('DROP TRIGGER '+self.db+'.pause_insert');self.apply(p10,10)
        require(self.apply(p10,10)['noop'],'interrupted operation retry duplicated')
        self.checks.append({'check':'sigkill_before_commit_atomicity_and_same_plan_retry','status':'passed'})
        self.snapshot('final')
        self.checks.append({'check':'transaction_failure_atomicity_and_competing_operations','status':'passed'})
        self.checks.append({'check':'apply_idempotency_concurrency_stale_object_prefix_limits_crc_rollback','status':'passed'})


def validate_args(a):
    a.host=base.validate_host(a.host,a.allow_isolated_runtime)
    for name in ('docker','binary'):
        path=Path(getattr(a,name))
        if not path.is_absolute() or not path.is_file() or not os.access(path,os.X_OK) or ',' in str(path):
            raise ValueError(name+' must be an absolute executable file without commas')
        setattr(a,name,str(path.resolve()) if name=='docker' else path.resolve())
    for d in (a.out,a.work_dir):
        resolved=d.resolve()
        if not d.is_absolute() or ',' in str(d) or d.exists() or resolved==base.CHECKOUT or base.CHECKOUT in resolved.parents:
            raise ValueError('out/work must be new absolute directories outside checkout without commas')
    if a.out.resolve()==a.work_dir.resolve() or a.out.resolve() in a.work_dir.resolve().parents or a.work_dir.resolve() in a.out.resolve().parents:
        raise ValueError('out/work must be separate directories')
    return a


def interrupted(signum,frame):
    raise KeyboardInterrupt()


def finish(v,a,code,error):
    signal.signal(signal.SIGINT,signal.SIG_IGN);signal.signal(signal.SIGTERM,signal.SIG_IGN)
    try:v.cleanup()
    finally:
        for p in a.out.glob('*.env'):p.unlink()
        if any(c['status']=='failed' for c in v.checks):code=1
        report={'status':'passed' if code==0 else 'failed','exit_code':code,'error':error,'checks':v.checks,
            'binary_sha256':hashlib.sha256(a.binary.read_bytes()).hexdigest(),
            'scope':'Explicit Java-shaped prefix seed; local unsigned OSS transport; real MySQL; current CLI bound into prior verified base image; no Java execution, cloud OSS, cutover, production or commit-ack interruption claim.'}
        path=a.out/'summary.json';path.write_text(json.dumps(report,indent=2)+'\n');path.chmod(0o600)
        print(json.dumps(report))
    return code


def main():
    p=argparse.ArgumentParser(description=__doc__)
    p.add_argument('--docker',required=True);p.add_argument('--host',required=True);p.add_argument('--allow-isolated-runtime',action='store_true')
    p.add_argument('--binary',type=Path,required=True);p.add_argument('--go-image',required=True);p.add_argument('--out',type=Path,required=True);p.add_argument('--work-dir',type=Path,required=True)
    a=validate_args(p.parse_args())
    for d in (a.out,a.work_dir):d.mkdir(mode=0o700,parents=True)
    a.out=a.out.resolve();a.work_dir=a.work_dir.resolve()
    v=Acceptance(a);code=0;error=None
    signal.signal(signal.SIGTERM,interrupted)
    try:v.execute()
    except BaseException as e:code=runtime.classify_exit(e,v.engine_ready);error=type(e).__name__+': '+str(e)
    finally:code=finish(v,a,code,error)
    return code
if __name__=='__main__':sys.exit(main())
