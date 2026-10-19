#!/usr/bin/env python3
"""Go-only all20 concurrent/large synthetic tests on an explicitly owned engine."""
import argparse
import collections
from decimal import Decimal,ROUND_CEILING
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
    spec=importlib.util.spec_from_file_location(name,path);m=importlib.util.module_from_spec(spec);spec.loader.exec_module(m);return m
cap=load('all20_capacity',ROOT/'script/verify-capacity-soak.py')
large=load('large_load_vectors',ROOT/'tests/load/large_fixtures.py')
base=cap.base
processing=load('all20_processing_measurement',ROOT/'tests/load/processing_measurement.py')
ROUTES={flow+'-'+route for flow,names in [('native','upload search latest types brief info details detail download-url download'),('web','upload search latest index task details detail exception download-url download')] for route in names.split()}
def mysql_budget(mib):
    if type(mib) is not int or mib not in (1024,2048):raise ValueError("explicit MySQL memory must be1024 or2048MiB")
    return str(mib)+"m",mib<<20

class DiskBudgetError(ValueError):pass

def require_disk_budget(free,location,minimum=3*(1<<30)):
    if free < minimum:raise DiskBudgetError(location+' free bytes '+str(free)+' below required '+str(minimum))
    return dict(location=location,free_bytes=free,minimum_free_bytes=minimum)
def memory_stat(raw):
    fields={'anon','file','kernel','kernel_stack','pagetables','sock','shmem','file_mapped','file_dirty','file_writeback','slab','slab_reclaimable','slab_unreclaimable'}
    return {k:int(v) for k,v in (line.split() for line in raw.splitlines()) if k in fields}
def a_pool_keys():return ('LOGAN_DB_MAX_OPEN','LOGAN_DB_MAX_IDLE')
def pool_budget(opened,idle):
    if type(opened) is not int or type(idle) is not int or not 1<=opened<=20 or not 0<=idle<=opened:raise ValueError('pool requires 1<=max-open<=20 and 0<=max-idle<=max-open')
    return {'LOGAN_DB_MAX_OPEN':str(opened),'LOGAN_DB_MAX_IDLE':str(idle)}
def validate_pool_metrics(metrics,opened,idle):
    base.require(metrics.get('logan_db_connections{state="max_open"}')==opened,'actual Go pool maximum differs or missing')
    actual_idle=metrics.get('logan_db_connections{state="idle"}')
    base.require(actual_idle is not None and 0<=actual_idle<=idle,'actual Go idle connections exceed configured maximum or missing')
def mysql_profile(enabled):
    return ['--max-connections=16','--innodb-redo-log-capacity=134217728','--max-binlog-size=67108864','--binlog-expire-logs-seconds=60'] if enabled else []

def digest(s):return hashlib.sha256(s.encode()).hexdigest()
def validate_routes(report):
    if not report['final'] or report['interrupted'] or report['report_write_errors'] or report['dropped'] or report['http']['Errors'] or report['workflows']['Errors']:raise ValueError('load errors, dropped work or incomplete report')
    if set(report['routes'])!=ROUTES:raise ValueError('all twenty positive route labels required')
    if any(v['Sent']<1 or v['Success']!=v['Sent'] or v['Errors'] for v in report['routes'].values()):raise ValueError('every route must succeed with positive data')
def validate_arrivals(report,stage):
    expected=int((Decimal(str(stage["qps"]))*stage["seconds"]).to_integral_value(rounding=ROUND_CEILING))
    ledger=report["ledger"]
    if report["attempted"]!=expected or report["workflows"]["Sent"]!=expected or len(ledger)!=expected or len({v["Cycle"] for v in ledger})!=expected:raise ValueError("scheduled arrivals or complete unique ledger mismatch")

def validate_hash_rows(tasks,rows,expected,flow):
    grouped=collections.defaultdict(list);seen=set()
    for task_id,detail_id,size,sha in rows:
        if detail_id in seen or task_id not in tasks:raise ValueError('unexpected or repeated detail identity')
        seen.add(detail_id);grouped[task_id].append((size,sha))
    oracle=[(len(v.encode()),digest(v)) for v in expected]
    if flow=='native':oracle.sort()
    if not tasks:raise ValueError('no uploaded tasks')
    for task_id,status in tasks.items():
        actual=grouped[task_id]
        if flow=='native':actual.sort()
        if status!=1 or actual!=oracle:raise ValueError('persisted full content mismatch or incomplete task')
    return len(tasks)
def load_plan(diagnostic,large_only=False):
    plan = [dict(name='all20-mixed',fixture='sdk',mode='all20',seconds=20 if diagnostic else 660,qps=4,workers=8,queue=16,burst=8,large=False),*[
        dict(name='large-'+n,fixture=n,mode='all20',seconds=20 if diagnostic else 220,qps=.2,workers=2,queue=4,burst=2,large=True) for n in ('large1','large8','large32')]]
    return plan[1:] if large_only else plan

class All20(cap.Capacity):
    def disk_guard(self,guest=False):
        evidence={'host':require_disk_budget(shutil.disk_usage(self.args.work_dir.parent).free,'host')}
        if guest:
            _,raw=self.docker('exec',self.mysql,'df','-Pk','/var/lib/mysql')
            evidence['guest']=require_disk_budget(int(raw.splitlines()[-1].split()[3])*1024,'owned MySQL filesystem',1<<30)
        return evidence
    def setup_runtime(self,mode):
        self.disk_guard()
        super().setup_runtime(mode)
    def sample(self,names):
        disk=self.disk_guard(True)
        value=super().sample(names);value['disk_budget']=disk
        for role,name in names.items():
            if role in value['resources']:
                code,raw=self.docker('exec',name,'cat','/sys/fs/cgroup/memory.stat',accept=role=='client')
                if code==0:value['resources'][role]['memory_stat']=memory_stat(raw)
        validate_pool_metrics(value.get('go_metrics',{}),self.args.pool_max_open,self.args.pool_max_idle)
        return value
    def common(self,cpus='1',memory='1g'):
        if memory=='128m':memory='512m'
        if memory=='256m':memory='1g'
        return super().common(cpus,memory)
    def db_sample(self):
        value=super().db_sample()
        raw=self.sql("SELECT EVENT_NAME,CURRENT_NUMBER_OF_BYTES_USED,HIGH_NUMBER_OF_BYTES_USED,COUNT_ALLOC,COUNT_FREE FROM performance_schema.memory_summary_global_by_event_name WHERE CURRENT_NUMBER_OF_BYTES_USED>=1048576 ORDER BY CURRENT_NUMBER_OF_BYTES_USED DESC LIMIT 20")
        value['memory_instrumentation_top']=[dict(event=n,current_bytes=int(c),high_bytes=int(h),allocations=int(a),frees=int(f)) for n,c,h,a,f in (v.split('\t') for v in raw.splitlines())]
        raw=self.sql("SELECT m.THREAD_ID,t.NAME,SUM(m.CURRENT_NUMBER_OF_BYTES_USED) FROM performance_schema.memory_summary_by_thread_by_event_name m LEFT JOIN performance_schema.threads t USING(THREAD_ID) GROUP BY m.THREAD_ID,t.NAME ORDER BY SUM(m.CURRENT_NUMBER_OF_BYTES_USED) DESC LIMIT 8")
        value['memory_threads_top']=[dict(thread_id=int(i),name=n,current_bytes=int(c)) for i,n,c in (v.split('\t') for v in raw.splitlines())]
        raw=self.sql("SELECT COUNT(*),COALESCE(SUM(OCTET_LENGTH(SQL_TEXT)),0) FROM performance_schema.prepared_statements_instances").split('\t')
        value['prepared_count'],value['prepared_sql_bytes']=map(int,raw)
        if not hasattr(self,'memory_settings'):
            raw=self.sql("SELECT @@max_allowed_packet,@@net_buffer_length,@@sort_buffer_size,@@join_buffer_size,@@tmp_table_size,@@temptable_max_ram,@@innodb_log_buffer_size").split('\t')
            self.memory_settings=dict(zip(('max_allowed_packet','net_buffer_length','sort_buffer_size','join_buffer_size','tmp_table_size','temptable_max_ram','innodb_log_buffer_size'),map(int,raw)))
        value['memory_settings']=self.memory_settings
        raw=self.sql("SELECT COALESCE(SUM(DATA_LENGTH+INDEX_LENGTH),0) FROM information_schema.tables WHERE TABLE_SCHEMA='"+self.db+"'")
        value['database_allocated_bytes']=int(raw)
        raw=self.sql('SHOW BINARY LOGS')
        logs=[line.split('\t') for line in raw.splitlines()]
        _,disk_raw=self.docker('exec',self.mysql,'du','-sk','/var/lib/mysql')
        value['mysql_data_directory_bytes']=int(disk_raw.split()[0])*1024
        value['binary_log_count']=len(logs);value['binary_log_bytes']=sum(int(row[1]) for row in logs)
        if not hasattr(self,'actual_mysql_settings'):
            keys=('max_connections','innodb_redo_log_capacity','max_binlog_size','binlog_expire_logs_seconds','log_bin','sync_binlog','innodb_flush_log_at_trx_commit')
            raw=self.sql('SELECT '+','.join('@@'+key for key in keys)).split('\t')
            self.actual_mysql_settings=dict(zip(keys,map(int,raw)))
            if self.args.bounded_mysql_profile:
                base.require([self.actual_mysql_settings[k] for k in keys[:4]]==[16,134217728,67108864,60],'actual MySQL bounded settings differ')
            base.require(self.actual_mysql_settings['log_bin']==1 and self.actual_mysql_settings['sync_binlog']==1 and self.actual_mysql_settings['innodb_flush_log_at_trx_commit']==1,'durable MySQL commit settings required')
        value['actual_mysql_settings']=self.actual_mysql_settings
        return value
    def preflight(self,kind,path):
        name=self.reserve('container',self.prefix+'-validate-'+kind)
        args=['--base-url','http://127.0.0.1:9','--fixture','/fixture.json','--mode','all20','--allow-test-target','--allow-writes','--test-target-marker','logan-load-test','--validate-only']
        if kind!='sdk':args.append('--large-fixture')
        _,raw=self.docker('run','--rm','--name',name,'--label',base.LABEL+'='+self.run_id,'--network','none','--read-only','--cap-drop','ALL','--cpus','.5','--memory','1g','--memory-swap','1g','--user',self.uid,'--mount','type=bind,src='+str(self.args.binary)+',dst=/loader,readonly','--mount','type=bind,src='+str(path)+',dst=/fixture.json,readonly','--entrypoint','/loader',self.args.go_image,*args,timeout=120)
        value=json.loads(raw);base.require(value.get('validated') and value['native_records']==len(self.fixture_data[kind]['nativeContents']) and value['web_records']==len(self.fixture_data[kind]['webContents']),'offline fixture validation failed')
        return value
    def collect_latency(self,name,report):
        # Docker logs survive app restarts and task IDs are reused after reset.
        # Both Docker bounds and nanosecond event filtering isolate each stage.
        _,logs=self.docker('logs','--since',report['start_utc'],'--until',report['snapshot_utc'],self.app)
        self.dump(name+'-app-stages',dict(output=logs,start_utc=report['start_utc'],end_utc=report['snapshot_utc']))
        ids=[row['TaskID'] for row in report['ledger'] if row['Flow']=='native']
        native=processing.summarize(logs,ids,report['start_utc'],report['snapshot_utc'])
        result=dict(native_processing=native,e2e_validated=False)
        try:
            result['upload_to_all_details']=processing.validate_e2e(report)
            result['e2e_validated']=True
        except (KeyError,TypeError,ValueError) as error:
            result['e2e_error']=type(error).__name__+': upload-to-details evidence incomplete or inconsistent'
        return result

    def data_check(self,report,fixture_mode):
        prefix=report['run_prefix'];base.require(re.fullmatch('phase19-[a-z0-9]+',prefix),'invalid synthetic run prefix')
        result={}
        for flow,task,detail in [('native','logan_task','logan_log_detail'),('web','web_task','web_detail')]:
            where="t.device_id LIKE '"+prefix+"-%'"
            raw=self.sql('SELECT t.id,t.status FROM '+self.db+'.'+task+' t WHERE '+where+' ORDER BY t.id')
            tasks={int(v.split('\t')[0]):int(v.split('\t')[1]) for v in raw.splitlines()}
            raw=self.sql('SELECT d.task_id,d.id,OCTET_LENGTH(d.content),SHA2(CAST(d.content AS BINARY),256) FROM '+self.db+'.'+detail+' d JOIN '+self.db+'.'+task+' t ON t.id=d.task_id WHERE '+where+' ORDER BY d.task_id,d.id')
            rows=[(int(a),int(b),int(c),d) for a,b,c,d in (v.split('\t') for v in raw.splitlines())]
            valid=validate_hash_rows(tasks,rows,self.fixture_data[fixture_mode][flow+'Contents'],flow)
            raw=self.sql("SELECT j.state,j.owner,j.lease_until,j.fence,j.attempts,j.source_revision,j.last_error_code,LENGTH(j.source_digest) FROM "+self.db+'.logan_processing_job j JOIN '+self.db+'.'+task+" t ON t.id=j.task_id AND j.kind='"+flow+"' WHERE "+where+' ORDER BY j.task_id')
            jobs=[v.split('\t') for v in raw.splitlines()]
            base.require(len(jobs)==len(tasks) and all(v==['done','','0','1','1','1','','64'] for v in jobs),'completed job ownership/fence/revision/retry invariants changed')
            ids=[v['TaskID'] for v in report['ledger'] if v['Flow']==flow]
            base.require(len(ids)==len(set(ids))==len(tasks) and sorted(ids)==sorted(tasks),'uploaded task ledger differs from persisted tasks')
            result[flow]=dict(tasks=len(tasks),details=len(rows),valid_tasks=valid,pending=0,jobs={'done':len(jobs)},job_attempts_max=1,retried_tasks=0,jobs_with_last_error=0,task_ids_sha256=hashlib.sha256(json.dumps(sorted(tasks)).encode()).hexdigest(),content_hash_rows_sha256=hashlib.sha256(json.dumps(rows).encode()).hexdigest(),all_job_fence_revision_one=True)
        return result
    def run_stage(self,stage,index):
        directory=self.args.work_dir/('go-'+stage['name']);directory.mkdir(mode=0o700)
        client=self.reserve('container',self.prefix+'-g-load-'+str(index))
        args=['--base-url','http://'+self.app+':8080/logan-web','--fixture','/fixture.json','--mode','all20','--duration',str(stage['seconds'])+'s','--qps',str(stage['qps']),'--concurrency',str(stage['workers']),'--queue',str(stage['queue']),'--arrival-burst',str(stage['burst']),'--timeout','30s' if stage['large'] else '5s','--parse-timeout','60s' if stage['large'] else '15s','--drain','60s' if stage['large'] else '20s','--allow-test-target','--allow-writes','--test-target-marker','logan-load-test','--output','/evidence/report.json']
        if stage['large']:args.append('--large-fixture')
        start=time.monotonic()
        self.docker('run','-d','--name',client,*self.common('.5','256m'),'--user',self.uid,'--tmpfs','/tmp:rw,size=16777216','--mount','type=bind,src='+str(self.args.binary)+',dst=/loader,readonly','--mount','type=bind,src='+str(self.fixture_files[stage['fixture']])+',dst=/fixture.json,readonly','--mount','type=bind,src='+str(directory)+',dst=/evidence','--entrypoint','/loader',self.args.go_image,*args)
        names=dict(app=self.app,mysql=self.mysql,oss=self.oss,client=client);samples=[];deadline=time.monotonic()+stage['seconds']+150;next_sample=time.monotonic();path=directory/'report.json'
        while True:
            if time.monotonic()>=next_sample:
                _,raw=self.docker('inspect','--format','{{json .State}}',client)
                if not json.loads(raw)['Running']:break
                sample=self.sample(names);samples.append(sample)
                with (directory/'resources.jsonl').open('a') as f:f.write(json.dumps(sample)+'\n')
                self.dump('live-status',dict(runtime='go',phase=stage['name'],samples=len(samples),last_sample=sample,utc=time.time()))
                next_sample+=10
            if path.exists() and json.loads(path.read_text()).get('final'):break
            base.require(time.monotonic()<deadline,'bounded load client completion timeout');time.sleep(1)
        _,raw=self.docker('wait',client,timeout=70);exitcode=int(raw.strip());elapsed=time.monotonic()-start
        if not path.exists():
            _,logs=self.docker('logs',client);self.dump(stage['name']+'-client-failure',dict(exit_code=exitcode,diagnostic_output=logs));raise ValueError('client report missing')
        report=json.loads(path.read_text());final_db=self.db_sample()
        evidence=dict(runtime='go',stage=stage,report=report,client_exit_code=exitcode,controller_elapsed_seconds=elapsed,resource_samples=len(samples),memory_stat_summary={role:{key:max(s['resources'][role].get('memory_stat',{}).get(key,0) for s in samples if role in s['resources']) for key in ('anon','file','kernel','file_dirty','file_writeback','sock','slab')} for role in names},database_final=final_db,resources={role:cap.measurement.resource_summary([s['resources'][role] for s in samples if role in s['resources']]) for role in names})
        evidence['latency']=self.collect_latency('go-'+stage['name'],report)
        def validate():
            if getattr(self.args,'require_latency_evidence',False):
                base.require(evidence['latency']['native_processing']['complete'] and evidence['latency']['e2e_validated'],'complete segmented/E2E latency evidence required')
            base.require(exitcode==0,'load client failed');cap.measurement.validate_report(report,stage['seconds']);validate_routes(report);validate_arrivals(report,stage)
            base.require(report.get('max_concurrent_workflows',0)>=2,'actual overlapping workflows not observed')
            cap.validate_lock_errors(samples,final_db)
            for role in names:
                base.require(evidence['resources'][role]['oom_kills']==0,'sampled OOM')
                cap.measurement.validate_coverage([s for s in samples if role in s['resources']],cap.measurement.parse_utc(report['start_utc']),cap.measurement.parse_utc(report['snapshot_utc']))
            span=samples[-1]['resources']['app']['monotonic']-samples[0]['resources']['app']['monotonic']
            evidence['host_sample_span_seconds']=span;evidence['host_active_lower_bound_seconds']=max(0,span-(60 if stage['large'] else 20))
            if not self.args.diagnostic:base.require(evidence['host_active_lower_bound_seconds']>=(120 if stage['large'] else 600),'host active lower bound too short')
            evidence['data']=self.data_check(report,stage['fixture'])
            evidence['assessment']=dict(stability_accepted=True,strict_functional_passed=True,all20_positive=True)
        self.validate_and_record_stage('go-'+stage['name'],evidence,validate)
        self.events.append(dict(runtime='go',event='stage_completed',stage=stage['name'],utc=time.time()))
        return evidence
    def protection_probe(self,mode):
        name=self.reserve('container',self.prefix+'-protection-'+mode)
        _,raw=self.docker('run','--rm','--name',name,*self.common('.5','256m'),'--user',self.uid,'--mount','type=bind,src='+str(self.args.work_dir/'protection_probe.py')+',dst=/probe.py,readonly','--mount','type=bind,src='+str(self.fixture_files['sdk'])+',dst=/fixture.json,readonly','python:3.12-alpine','python','/probe.py',self.app,'/fixture.json','phase21-'+self.run_id+'-repeat',mode,timeout=150)
        value=json.loads(raw);base.require(value.get('status')=='passed','protection HTTP probe failed');return value['result']
    def web_snapshot(self,task):
        base.require(isinstance(task,int) and task>0,'invalid synthetic task')
        task=str(task)
        queries=["SELECT id,status,device_id,log_date,page_num,SHA2(CAST(content AS BINARY),256) FROM "+self.db+'.web_task WHERE id='+task,
                 "SELECT id,task_id,log_type,log_time,log_level,add_time,minute_offset,OCTET_LENGTH(content),SHA2(CAST(content AS BINARY),256) FROM "+self.db+'.web_detail WHERE task_id='+task+' ORDER BY id',
                 "SELECT kind,task_id,state,owner,lease_until,fence,attempts,source_revision,source_digest,last_error_code FROM "+self.db+".logan_processing_job WHERE kind='web' AND task_id="+task]
        return [self.sql(q) for q in queries]
    def protection_checks(self):
        seed=self.protection_probe('web-seed');before=self.web_snapshot(seed['web_task_id'])
        repeated=self.protection_probe('web-repeat');after=self.web_snapshot(seed['web_task_id'])
        base.require(before==after and seed['detail_ids']==repeated['detail_ids'],'completed Web replay changed task/detail/job semantic snapshot')
        native=self.protection_probe('native-repeat')
        for task in native['native_task_ids']:
            raw=self.sql("SELECT state,owner,lease_until,fence,attempts,source_revision,IF(last_error_code='',1,0) FROM "+self.db+".logan_processing_job WHERE kind='native' AND task_id="+str(task))
            base.require(raw.split('\t')==['done','','0','1','1','1','1'],'concurrent repeated Native parse changed ownership invariants')
            raw=self.sql('SELECT COUNT(*),COUNT(DISTINCT id) FROM '+self.db+'.logan_log_detail WHERE task_id='+str(task))
            base.require(list(map(int,raw.split('\t')))==[len(self.fixture_data['sdk']['nativeContents'])]*2,'duplicate/missing Native details')
        counts=lambda:self.sql('SELECT (SELECT COUNT(*) FROM '+self.db+'.logan_task),(SELECT COUNT(*) FROM '+self.db+'.web_task),(SELECT COUNT(*) FROM '+self.db+'.logan_log_detail),(SELECT COUNT(*) FROM '+self.db+'.web_detail),(SELECT COUNT(*) FROM '+self.db+'.logan_processing_job)')
        before_counts=counts();_,before_logs=self.docker('logs',self.oss)
        limits=self.protection_probe('over-limit')
        _,after_logs=self.docker('logs',self.oss)
        base.require(before_counts==counts(),'oversize request wrote business/job rows')
        base.require(before_logs.count('OSS_FIXTURE_PUT')==after_logs.count('OSS_FIXTURE_PUT'),'oversize request wrote an object')
        final=self.db_sample();cap.validate_lock_errors([{'database':final}],final)
        return self.dump('protection-checks',dict(status='passed',web_repeat=repeated,native_repeat=native,oversize=limits,web_semantic_snapshot_unchanged=True,web_snapshot_sha256=hashlib.sha256(json.dumps(before).encode()).hexdigest(),oversize_tables_and_object_puts_unchanged=True,database_final=final,native_repeat_semantics='each repeated upload creates a new task/object; parsing each task remains idempotent'))
    def execute(self):
        self.disk_guard()
        _,raw=self.docker('info','--format','{{json .}}');info=json.loads(raw);base.require(info['OSType']=='linux' and str(info['CgroupVersion'])=='2','Linux cgroup2 required');self.engine_ready=True
        for image in cap.runtime_images('go',self.args.go_image):self.docker('image','inspect',image)
        self.uid=str(os.getuid())+':'+str(os.getgid());self.network=self.reserve('network',self.prefix+'-net');self.docker('network','create','--internal','--label',base.LABEL+'='+self.run_id,self.network)
        public=large.openssl(['pkey','-pubin','-in',str(self.args.sdk_fixtures/'test-rsa-public.pem'),'-text','-noout']).decode()
        base.require(re.search(r'\(2048 bit\)',public),'2048-bit test RSA required without legacy override')
        self.dump('crypto-configuration',dict(test_rsa_bits=2048,legacy_rsa_override=False))
        self.events=[];self.fixture_data={'sdk':cap.fixtures.make_fixture(self.args.sdk_fixtures)};self.fixture_files={};profiles={}
        for name,n,w in [('large1',1,1),('large8',8,4),('large32',32,8)]:
            f=large.make_fixture(*large.validate_profile(n,w),self.args.sdk_fixtures/'test-rsa-private.pem');profiles[name]=dict(f['profile'],oracle=large.verify_fixture(f,self.args.sdk_fixtures/'test-rsa-private.pem'));self.fixture_data[name]=f
        self.dump('large-fixture-profiles',profiles)
        staged_probe=self.args.work_dir/'protection_probe.py';staged_probe.write_bytes((ROOT/'tests/load/protection_probe.py').read_bytes());staged_probe.chmod(0o600)
        fixture_server=self.args.work_dir/'oss_fixture.py';fixture_server.write_bytes((ROOT/'tests/e2e20/oss_fixture.py').read_bytes());fixture_server.chmod(0o600)
        for kind,f in self.fixture_data.items():
            path=self.args.work_dir/(kind+'.json');path.write_text(json.dumps(f));path.chmod(0o600);self.fixture_files[kind]=path
        self.dump('fixture-preflight',{kind:self.preflight(kind,path) for kind,path in self.fixture_files.items()})
        frozen={str(path):hashlib.sha256(path.read_bytes()).hexdigest() for path in [self.args.binary,staged_probe,*self.fixture_files.values(),ROOT/'script/verify-all20-large-load.py',ROOT/'script/verify-capacity-soak.py',ROOT/'tests/tools/test_all20_large_controller.py',ROOT/'tests/load/large_fixtures.py',ROOT/'tests/load/processing_measurement.py',ROOT/'tests/tools/test_processing_measurement.py',ROOT/'tests/tools/test_processing_capture.py',ROOT/'tests/load/protection_probe.py',*sorted((ROOT/'cmd/soaktest').glob('*.go'))]}
        self.dump('frozen-inputs',frozen)
        plan=load_plan(self.args.diagnostic,self.args.large_only);self.dump('frozen-plan',dict(stages=plan,runtime_order=['go'],app_cpu=1,app_memory=1<<30,mysql_cpu=1,mysql_memory=self.args.mysql_memory_bytes,pool=self.args.go_pool_env,mysql_extra_args=self.args.mysql_extra_args,large_only=self.args.large_only,client_cpu=.5,client_memory=1<<30,oss_cpu=.25,oss_memory=512<<20,diagnostic=self.args.diagnostic))
        self.setup_runtime('go')
        _,configured=self.docker('inspect','--format','{{json .Config.Env}}',self.app)
        env_values=json.loads(configured)
        actual_pool={key:value for entry in env_values for key,_,value in [entry.partition('=')] if key in a_pool_keys()}
        base.require(actual_pool==self.args.go_pool_env,'actual configured pool differs')
        self.dump('pool-configuration',dict(requested=self.args.go_pool_env,actual=actual_pool))
        base.require(not any(v.startswith('GODEBUG=') for v in json.loads(configured)),'unexpected RSA compatibility override')
        results=[]
        for index,stage in enumerate(plan):
            if index:self.reset_before_soak()
            results.append(self.run_stage(stage,index))
        protections=self.protection_checks()
        base.require(all(hashlib.sha256(Path(path).read_bytes()).hexdigest()==sha for path,sha in frozen.items()),'load sources/binary/fixtures changed during execution')
        self.dump('frozen-inputs-verified',dict(status='passed',files=len(frozen)))
        self.dump('results',dict(protection_checks=protections,measurement_complete=True,runtime='go',formal=not self.args.diagnostic,stages=results,limitations=['synthetic large protocol fixtures; SDK small artifacts replay only','local OSS byte fixture without signature authentication','internal HTTP/MySQL without TLS','one resource/arm64 profile; not 24h or production capacity']))


def main(argv=None):
    p=argparse.ArgumentParser(description=__doc__)
    for key in ('docker','host','go-image'):p.add_argument('--'+key,required=True)
    for key in ('binary','sdk-fixtures','out','work-dir'):p.add_argument('--'+key,type=Path,required=True)
    p.add_argument('--allow-isolated-runtime',action='store_true');p.add_argument('--diagnostic',action='store_true');p.add_argument('--mysql-memory-mib',type=int,choices=(1024,2048),default=1024)
    p.add_argument('--pool-max-open',type=int,default=20);p.add_argument('--pool-max-idle',type=int,default=10)
    p.add_argument('--bounded-mysql-profile',action='store_true');p.add_argument('--large-only',action='store_true')
    p.add_argument('--require-latency-evidence',action='store_true',help='require complete Native stage events and final upload-to-all-details samples')
    a=p.parse_args(argv)
    try:
        a.go_pool_env=pool_budget(a.pool_max_open,a.pool_max_idle)
        a.mysql_extra_args=mysql_profile(a.bounded_mysql_profile)
        require_disk_budget(shutil.disk_usage(a.work_dir.parent).free,'host initial',4*(1<<30))
        base.validate_host(a.host,a.allow_isolated_runtime)
        a.mysql_memory,a.mysql_memory_bytes=mysql_budget(a.mysql_memory_mib)
        for path in (a.binary,a.sdk_fixtures,a.work_dir,a.out):base.require(path.is_absolute() and Path('/private/tmp') in path.resolve().parents,'private absolute shared paths required')
        base.require(a.binary.is_file() and (a.sdk_fixtures/'manifest.json').is_file(),'loader/SDK fixtures missing')
        a.out.mkdir(mode=0o700,parents=True,exist_ok=False);a.work_dir.mkdir(mode=0o700,parents=True,exist_ok=False)
    except (AssertionError,OSError,ValueError) as e:p.error(str(e))
    a.keep_image=False;a.runtimes='go';a.legacy_rsa_compat=False
    v=All20(a);code=0;error=None
    old=signal.signal(signal.SIGTERM,lambda *_:(_ for _ in ()).throw(KeyboardInterrupt()))
    try:v.execute()
    except BaseException as e:code=cap.runtime.classify_exit(e,v.engine_ready);error=type(e).__name__+': '+str(e).replace(v.password,'<synthetic-secret>')
    finally:
        signal.signal(signal.SIGINT,signal.SIG_IGN);signal.signal(signal.SIGTERM,signal.SIG_IGN)
        if code and v.engine_ready:
            states={}
            for role in ('app','mysql','oss'):
                name=getattr(v,role,None)
                if name:
                    state_code,raw=v.docker('inspect','--format','{{json .State}}',name,accept=True)
                    if state_code==0:states[role]=json.loads(raw)
                    _,logs=v.docker('logs',name,accept=True);v.dump('failure-'+role+'-logs',dict(output=logs))
            v.dump('failure-runtime-states',states)
        if not v.cleanup() and code==0:code=1
        for path in a.out.glob('*.env'):path.unlink()
        v.dump('events',getattr(v,'events',[]));v.dump('summary',dict(status='passed' if code==0 else 'failed',exit_code=code,error=error,selected_runtimes=['go'],formal=not a.diagnostic,mysql_memory_bytes=a.mysql_memory_bytes,checks=v.checks,go_image=a.go_image,binary_sha256=hashlib.sha256(a.binary.read_bytes()).hexdigest(),scope='isolated synthetic Go-only; original Java retained; no production/cloud/cutover'))
        signal.signal(signal.SIGTERM,old)
    print('Evidence: '+str(a.out/'summary.json'));return code
if __name__=='__main__':sys.exit(main())
