#!/usr/bin/env python3
"""Read-only actual emulator RN/native upload verification. No event creation or uploads."""
import argparse
from collections import Counter
import hashlib
import importlib.util
import json
from pathlib import Path
import re
import subprocess
import time
import urllib.request

APP='com.hermesdemo'
SCHEMA='xlb.page-diagnostic'
REQUIRED={'schema','schemaVersion','eventId','pageAttemptId','pageName','eventName','stage','outcome','occurredAtMs','monotonicMs','source','level','module'}
OPTIONAL={'durationMs','errorCode','retryOf','scenario','testInjected','dataState'}

def require(ok):
    if not ok:raise ValueError('Actual RN upload evidence differs')

def canonical(value):
    require(isinstance(value,dict) and REQUIRED <= set(value) <= REQUIRED|OPTIONAL)
    require(value['schema']==SCHEMA and value['schemaVersion']==1)
    return json.dumps(value,sort_keys=True,separators=(',',':'))

def check_payloads(receipt,events,ciphertext):
    require(hashlib.sha256(ciphertext).hexdigest()==receipt['sourceSha256'] and len(ciphertext)==receipt['sourceBytes'])
    expected=Counter(canonical(e) for e in receipt['events'])
    business=[e for e in events if e['content']!='clogan header']
    require(Counter(canonical(json.loads(e['content'])) for e in business)==expected)
    for event in business:
        payload=json.loads(event['content'])
        require('diagnosticWarning' not in event and all(event.get(k)==v for k,v in payload.items()))
    require(all(e.get('logType')==1 for e in events if e['content']=='clogan header'))
    return {'exactEvents':True,'normalizedFieldsMatch':True,'originalCiphertext':True}

def module(name,path):
    spec=importlib.util.spec_from_file_location(name,path);m=importlib.util.module_from_spec(spec);spec.loader.exec_module(m);return m

def main():
    parser=argparse.ArgumentParser(description=__doc__);parser.add_argument('--serial',required=True);parser.add_argument('--report',type=Path,required=True);parser.add_argument('--baseline',type=Path);args=parser.parse_args()
    local=module('local_runtime',Path(__file__).with_name('workbench-local.py')); paging=module('business_paging',Path(__file__).with_name('workbench-business-android.py'))
    runtime=local.Runtime(local.DEFAULT_ROOT);cfg=runtime.config();profile=runtime.hermes_profile(create=False)
    def private_bytes(name):
        p=subprocess.run(['adb','-s',args.serial,'exec-out','run-as',APP,'cat','files/'+name],capture_output=True,check=True);require(len(p.stdout)<=5*1024*1024);return p.stdout
    def private(name):return json.loads(private_bytes(name))
    require(private('workbench-profile.json')==profile)
    receipt=private('workbench-last-upload.json');journal=private('workbench-events.json')
    require(receipt['state']=='http_success' and receipt['httpStatus']==200 and receipt['appId']==APP and receipt['projectId']==cfg['projectId'])
    require(receipt['deviceId']==profile['deviceId']==journal['installationId'])
    require(0<len(receipt['events'])<=240)
    require(not Counter(canonical(e) for e in receipt['events'])-Counter(canonical(e) for e in journal['events']))
    attempt=receipt['attemptId'];require(isinstance(attempt,str) and re.fullmatch('[A-Za-z0-9_-]{1,128}',attempt))
    require(receipt['sourcePath']=='workbench-uploads/'+attempt+'.logan')
    source=private_bytes(receipt['sourcePath'])
    opener=urllib.request.build_opener(urllib.request.ProxyHandler({}),local.NoRedirect())
    def get(path,raw=False):
        req=urllib.request.Request(local.URL+local.API+path,headers={'Authorization':'Bearer '+cfg['adminToken'],'X-Project-ID':cfg['projectId']})
        with opener.open(req,timeout=10) as res:wire=res.read(32*1024*1024+1)
        require(len(wire)<=32*1024*1024);return wire if raw else json.loads(wire)['data']
    response=receipt['response'];run_id=response['runId'];asset_id=response['assetId']
    require(all(isinstance(v,str) and re.fullmatch('[A-Za-z0-9_-]{1,128}',v) for v in [run_id,asset_id,response['receiptId']]))
    for _ in range(80):
        run=get('/parse-runs/'+run_id)
        if run['status'] not in ['running','queued']:break
        time.sleep(.25)
    require(run['status']=='succeeded' and run['mode']=='strict' and run['format']=='native' and run['errorCount']==0 and run['assetId']==asset_id)
    asset=next(a for a in get('/workspace')['assets'] if a['id']==asset_id)
    require(asset['app']==APP and asset['sdkClientId']=='hermes-rn-demo' and asset['environment']=='emulator-local' and asset['sdkReceiptId']==response['receiptId'])
    require(asset['origin']=='sdk' and asset['originalEncrypted'] is True)
    require(asset['version']==receipt['appVersion'] and asset['build']==receipt['buildVersion'])
    package=subprocess.check_output(['adb','-s',args.serial,'shell','dumpsys','package',APP],text=True)
    require(re.search(r'\bversionName='+re.escape(receipt['appVersion'])+r'(?:\s|$)',package) and re.search(r'\bversionCode='+re.escape(receipt['buildVersion'])+r'(?:\s|$)',package))
    events=paging.paged_events(get,run_id);wire=get('/assets/'+asset_id+'/raw',True)
    require(wire==source and hashlib.sha256(wire).hexdigest()==asset['rawHash'] and len(wire)==asset['size'])
    checks=check_payloads(receipt,events,wire);require(len(events)==run['eventCount'])
    helper=Path(__file__).resolve().parents[1]/'internal/workbenchui/static/page-diagnostics.mjs'
    code="import {groupPageAttempts} from "+json.dumps(helper.as_uri())+";let input='';for await(const c of process.stdin)input+=c;const x=JSON.parse(input);console.log(JSON.stringify(groupPageAttempts(x.events,x.runId).map(a=>({pageAttemptId:a.pageAttemptId,status:a.status,slow:a.slow,testInjected:a.testInjected,scenarios:a.scenarios,eventCount:a.events.length,gaps:a.gaps}))));"
    attempts=json.loads(subprocess.check_output(['node','--input-type=module','-e',code],input=json.dumps({'events':events,'runId':run_id}).encode()))
    result={'scope':'Actual HermesDemo Android emulator RN → repository Java/JNI SDK → strict Go parse; explicit injected cases','appId':APP,'projectId':cfg['projectId'],'appVersion':receipt['appVersion'],'buildVersion':receipt['buildVersion'],'assetId':asset_id,'runId':run_id,'receiptId':response['receiptId'],'uploadAttemptId':attempt,'fileDate':receipt['date'],'eventCount':len(events),'diagnosticEventCount':len(receipt['events']),'rawBytes':len(wire),'sha256':hashlib.sha256(wire).hexdigest(),'pageAttempts':attempts,'checks':checks}
    if args.baseline:
        before=json.loads(args.baseline.read_text());require(all(result[k]==v for k,v in before.items() if k!='checks'));checks['baselineRetained']=True
    local.private_json(args.report.absolute(),result)
    print(json.dumps(result,indent=2))

if __name__=='__main__':
    try:main()
    except Exception as error:
        print('RN page evidence verification failed ('+type(error).__name__+'); inspect private evidence locally.',file=__import__('sys').stderr);raise SystemExit(1)
