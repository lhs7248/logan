import os,json,time,urllib.request,urllib.parse,threading,traceback,shutil
from pathlib import Path
out=Path('/private/tmp/phase25-ios');roots=json.loads(Path(os.environ.get('PHASE25_ROOTS',str(out/'containers.json'))).read_text());expected=int(os.environ.get('PHASE25_EXPECTED_UPLOADS','8'));seen=set();threads=[];lock=threading.Lock()
opener=urllib.request.build_opener(urllib.request.ProxyHandler({}));base='http://localhost:8888/logan-web'
def get(path):
 start=time.time()
 with opener.open(base+path,timeout=40) as r: body=json.load(r);assert r.status==200
 assert body['code']==200,body
 return body['data'],time.time()-start

def measure(case,r,root):
 dest=out/case/str(r['batch']);dest.mkdir(parents=True,exist_ok=True)
 result={'client':r,'observed_ack_unix':time.time(),'errors':[]}
 try:
  payload=json.loads(r['response']);assert r['http_status']==200 and not r['error'] and payload['code']==200,payload
  name=urllib.parse.parse_qs(urllib.parse.urlparse(payload['data']).query)['name'][0]
  tasks,_=get('/logan/task/search.json?'+urllib.parse.urlencode(dict(deviceId=r['device_id'],beginTime=0,endTime=int(time.time()*1000),platform=2)))
  matches=[t for t in tasks if t['logFileName']==name];assert len(matches)==1,matches
  task=matches[0]['taskId'];result['task_id']=task
  started=time.time();brief,dt=get(f'/logan/task/{task}/brief.json?logTypes=&keyword=');result['brief_seconds']=dt;result['brief_observed_unix']=time.time()
  ids=[v['id'] for group in brief for v in group];assert len(ids)==r['expected_rows']+1 and len(ids)==len(set(ids)),(len(ids),r['expected_rows'])
  rows=[]
  for i in range(0,len(ids),200):
   batch,_=get('/logan/task/query/details.json?detailIds='+','.join(map(str,ids[i:i+200])));rows.extend(batch)
  completed=time.time();result.update(complete_read_unix=completed,upload_ack_seconds=r['upload_ack_unix']-r['upload_start_unix'],generation_flush_seconds=r['upload_start_unix']-r['generation_start_unix'],upload_to_all_queryable_seconds=completed-r['upload_start_unix'],ack_to_all_queryable_seconds=completed-r['upload_ack_unix'],detail_read_seconds=completed-result['brief_observed_unix'],records=len(rows))
  assert len(rows)==r['expected_rows']+1 and [v['id'] for v in rows]==ids
  (dest/'api-rows.json').write_text(json.dumps(rows,ensure_ascii=False));(dest/'brief.json').write_text(json.dumps(brief))
  shutil.copy2(Path(root)/'Documents/Acceptance'/f"upload-{r['batch']}.bin",dest/'native-upload.bin')
  download,_=get(f'/logan/task/{task}/download.json');start=time.time()
  with opener.open(base+download,timeout=40) as resp: data=resp.read();assert resp.status==200
  (dest/'http-download.gz').write_bytes(data);result['http_download_seconds']=time.time()-start
  repeat,_=get(f'/logan/task/{task}/brief.json?logTypes=&keyword=');assert repeat==brief
  result['status']='measured'
 except Exception as e:
  result['status']='failed';result['errors'].append(repr(e));traceback.print_exc()
 (dest/'measurement.json').write_text(json.dumps(result,indent=2));print(json.dumps({k:result[k] for k in ['status','task_id','records','upload_ack_seconds','upload_to_all_queryable_seconds','errors'] if k in result}),flush=True)

start=time.monotonic()
while time.monotonic()-start<2400:
 for case,root in roots.items():
  p=Path(root)/'Documents/Acceptance/results.json'
  if not p.exists():continue
  try: records=json.loads(p.read_text())
  except (ValueError,OSError):continue
  for r in records:
   key=(case,r['batch'])
   if 'upload_ack_unix' not in r or key in seen:continue
   seen.add(key);thread=threading.Thread(target=measure,args=(case,r,root));thread.start();threads.append(thread)
 if len(seen)==expected and all(not t.is_alive() for t in threads):break
 time.sleep(.05)
for t in threads:t.join()
assert len(seen)==expected,len(seen)
print(f'all {expected} uploads observed (inspect per-sample status)',flush=True)
