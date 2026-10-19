"""Isolated HTTP replay/limit probe; synthetic identities only, no production URL."""
import argparse
import base64
from concurrent.futures import ThreadPoolExecutor
import http.client
import json
from pathlib import Path
import re
import struct
import hashlib
import time
import urllib.parse
import urllib.request


def require(ok,message):
    if not ok:raise ValueError(message)


def collect_ids(value):
    if isinstance(value,list):return [n for v in value for n in collect_ids(v)]
    if isinstance(value,dict):
        if 'id' in value:return [value['id']]
        if 'detailId' in value:return [value['detailId']]
        return [n for v in value.values() for n in collect_ids(v)]
    return []


def oversize_bodies(fixture):
    seed=base64.b64decode(fixture["nativeBase64"]);cursor=0
    while cursor<len(seed):
        require(len(seed)-cursor>=7 and seed[cursor]==1,"verified Native frame seed required")
        n=struct.unpack(">I",seed[cursor+1:cursor+5])[0]
        require(n>=16 and n%16==0 and cursor+n+5<len(seed) and seed[cursor+n+5]==0,"verified Native frame length required")
        cursor+=n+6
    require(seed and len(seed)<=1<<20,"small verified Native seed required")
    native=seed*(((32<<20)//len(seed))+1)
    web=dict(fixture["webBody"],customInfo="x"*(32<<20))
    return {"native":native,"web":json.dumps(web).encode()}

def run(host,fixture,device,mode):
    require(re.fullmatch(r'logan-verifier-[a-f0-9]{32}-go',host),'owned Go hostname required')
    require(re.fullmatch(r'phase21-[a-f0-9]{32}-repeat',device),'synthetic identity required')
    origin='http://'+host+':8080/logan-web'
    def call(path,body=None,headers=None):
        opener=urllib.request.build_opener(urllib.request.ProxyHandler({}))
        request=urllib.request.Request(origin+path,data=body,headers=headers or {})
        with opener.open(request,timeout=30) as response:
            require(response.status==200,'HTTP success required');value=json.loads(response.read(64<<20))
        require(value.get('code')==200,'business success required');return value['data']
    web=dict(fixture['webBody'],deviceId=device)
    web_body=json.dumps(web).encode();web_headers={'Content-Type':'application/json'}
    search='/logan/web/search.json?'+urllib.parse.urlencode({'deviceId':device,'beginTime':1,'endTime':4102444800000})
    def index(task):
        deadline=time.monotonic()+30
        while True:
            ids=collect_ids(call('/logan/web/detailIndex.json?tasks='+str(task)+'&logTypes=&keyword=&beginTime=00:00&endTime=23:59'))
            if len(ids)==len(fixture['webContents']):return ids
            require(time.monotonic()<deadline,'publication deadline');time.sleep(.1)
    if mode=='web-seed':
        call('/logan/web/upload.json',web_body,web_headers);rows=call(search)
        require(len(rows)==1,'one Web identity required');task=rows[0]['taskId'];ids=index(task)
        return dict(web_task_id=task,detail_ids=ids)
    if mode=='web-repeat':
        rows=call(search);require(len(rows)==1,'seed required');task=rows[0]['taskId']
        with ThreadPoolExecutor(max_workers=8) as pool:
            result=list(pool.map(lambda _:call('/logan/web/upload.json',web_body,web_headers),range(16)))
            indexes=list(pool.map(lambda _:index(task),range(16)))
        rows=call(search);require(len(rows)==1 and rows[0]['taskId']==task,'Web replay duplicated identity')
        require(all(v is True for v in result) and all(v==indexes[0] for v in indexes),'Web replay publication identity changed')
        return dict(web_task_id=task,requests=32,detail_ids=indexes[0])
    if mode=='native-repeat':
        payload=base64.b64decode(fixture['nativeBase64'])
        headers={'Content-Type':'application/octet-stream','appId':'phase21','deviceId':device,'platform':'1','fileDate':'2026-10-19','unionId':'synthetic','buildVersion':'1','appVersion':'1'}
        with ThreadPoolExecutor(max_workers=2) as pool:urls=list(pool.map(lambda _:call('/logan/upload.json',payload,headers),range(2)))
        require(len(set(urls))==2,'Native legacy repeat must create distinct object/tasks')
        rows=call('/logan/task/search.json?'+urllib.parse.urlencode({'deviceId':device,'beginTime':1,'endTime':4102444800000,'platform':1}));require(len(rows)==2,'Native repeated upload semantics changed')
        tasks=[r['taskId'] for r in rows]
        def brief(task):
            ids=collect_ids(call('/logan/task/'+str(task)+'/brief.json?logTypes=&keyword='));require(len(ids)==len(fixture['nativeContents']),'Native complete publication required');return ids
        with ThreadPoolExecutor(max_workers=8) as pool:observed=list(pool.map(brief,tasks*8))
        for task in tasks:
            ids=brief(task);rows=call('/logan/task/query/details.json?detailIds='+','.join(map(str,ids)))
            require(sorted(r['content'] for r in rows)==sorted(fixture['nativeContents']),'Native replay content mismatch')
        return dict(native_task_ids=tasks,concurrent_parse_queries=len(observed),distinct_uploads=2)
    if mode=='over-limit':
        results={};bodies=oversize_bodies(fixture)
        for flow,path,headers in [('native','/logan/upload.json',{'Content-Type':'application/octet-stream','deviceId':device,'platform':'1','fileDate':'2026-10-19','appId':'phase21','appVersion':'1','buildVersion':'1','unionId':'synthetic'}),('web','/logan/web/upload.json',{'Content-Type':'application/json'})]:
            conn=http.client.HTTPConnection(host,8080,timeout=30)
            try:
                try:conn.request('POST','/logan-web'+path,body=bodies[flow],headers=headers)
                except (BrokenPipeError,ConnectionResetError):pass
                response=conn.getresponse();raw=response.read(1<<20);code=None
                try:code=json.loads(raw).get('code')
                except (ValueError,AttributeError):pass
                require(response.status==413 or (flow=='native' and response.status==200 and code==500),'oversize must be rejected with existing semantics')
                results[flow]=dict(status=response.status,business_code=code,body_bytes=len(bodies[flow]),construction='repeat independently verified complete SDK frames' if flow=='native' else 'valid Web seed with oversized JSON metadata',body_sha256=hashlib.sha256(bodies[flow]).hexdigest())
            finally:conn.close()
        return results
    raise ValueError('invalid mode')

if __name__=='__main__':
    p=argparse.ArgumentParser();p.add_argument('host');p.add_argument('fixture',type=Path);p.add_argument('device');p.add_argument('mode',choices=('web-seed','web-repeat','native-repeat','over-limit'));a=p.parse_args()
    try:
        f=json.loads(a.fixture.read_text());print(json.dumps(dict(status='passed',result=run(a.host,f,a.device,a.mode))))
    except Exception as e:
        print(json.dumps(dict(status='failed',error_type=type(e).__name__,reason=str(e) if isinstance(e,ValueError) else 'HTTP/probe failed')));raise SystemExit(1)
