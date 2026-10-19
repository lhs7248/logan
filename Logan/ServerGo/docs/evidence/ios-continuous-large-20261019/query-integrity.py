import json,subprocess,hashlib
from pathlib import Path
out=Path('/private/tmp/phase25-ios');summary=json.loads((out/'verification-summary.json').read_text());tasks=[r['task_id'] for r in summary];idlist=','.join(map(str,tasks))
sql=f"SELECT task_id,id,log_type,log_time,OCTET_LENGTH(content),SHA2(CAST(content AS BINARY),256) FROM logan_log_detail WHERE task_id IN ({idlist}) ORDER BY task_id,id;\n"
sql+=f"SELECT kind,task_id,state,IF(owner='','-',owner),lease_until,fence,source_revision,attempts,LENGTH(source_digest),IF(last_error_code='','-',last_error_code) FROM logan_processing_job WHERE kind='native' AND task_id IN ({idlist}) ORDER BY task_id;\n"
sql+="SELECT 'failed32_tasks',COUNT(*) FROM logan_task WHERE device_id='phase25-ios-20261019-large32';\nSELECT 'failed32_objects',COUNT(*) FROM logan_managed_object WHERE name LIKE '%phase25-ios-20261019-large32%';\nSELECT NAME,COUNT,STATUS FROM information_schema.innodb_metrics WHERE NAME IN ('lock_deadlocks','lock_timeouts');\n"
(out/'integrity.sql').write_text(sql)
p=subprocess.run(['python3','/private/tmp/phase24-runtime/query.py'],input=sql,text=True,stdout=subprocess.PIPE,stderr=subprocess.PIPE,check=True);(out/'integrity.tsv').write_text(p.stdout)
lines=[line.split('\t') for line in p.stdout.splitlines()];details=[line for line in lines if len(line)==6];jobs=[line for line in lines if len(line)==10]
expected={}
for r in summary:
 case=r['scenario'];d=out/case/str(r['batch']); rows=json.loads((d/'expected-sql.json').read_text())
 for v in rows:expected[(str(r['task_id']),str(v['id']))]=[str(v['type']),str(v['time']),str(v['bytes']),v['sha256']]
assert len(details)==len(expected)==3811
for row in details:assert expected[(row[0],row[1])]==row[2:],row
assert len(jobs)==7
for row in jobs:assert row[2:]==['done','-','0','1','1','1','64','-'],row
assert ['failed32_tasks','0'] in lines and ['failed32_objects','0'] in lines
for row in lines:
 if row[0] in ('lock_deadlocks','lock_timeouts'):assert row[1:] == ['0','enabled'],row
result={'status':'passed','sql_records_matched':len(details),'jobs_done_with_empty_owner_zero_lease_fence1_revision1_attempt1':7,'failed32_task_count':0,'failed32_managed_object_count':0,'innodb_lock_metrics':[r for r in lines if r[0].startswith('lock_')]};(out/'database-verification.json').write_text(json.dumps(result,indent=2));print(json.dumps(result,indent=2))
