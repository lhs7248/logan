#!/usr/bin/env python3
"""Synthetic native SDK HTTP acceptance. Requires an isolated configured project.

Consumes a file produced by tests/sdk/driver/native-workbench-producer.c.
Never contacts Aliyun, source or model providers; no production credentials.
"""
import argparse
import hashlib
import json
import os
import pathlib
import secrets
import time
import urllib.error
import urllib.parse
import urllib.request
import uuid


def key():
    return str(uuid.UUID(int=(int(time.time()*1000)<<80) | (7<<76) |
                         (secrets.randbits(12)<<64) | (2<<62) | secrets.randbits(62)))


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('--base', default='http://127.0.0.1:8091/logan-web/api/workbench/v1')
    p.add_argument('--project', required=True)
    p.add_argument('--native', type=pathlib.Path, required=True)
    p.add_argument('--report', type=pathlib.Path, required=True)
    p.add_argument('--upload-id', help='stable identifier shared with a preceding actual mobile sender upload')
    a = p.parse_args()
    target = urllib.parse.urlsplit(a.base)
    if target.hostname not in ('127.0.0.1', 'localhost', '::1') or target.scheme not in ('http', 'https') or target.username or target.query or target.fragment:
        p.error('this synthetic mutation acceptance is restricted to an explicit loopback server')
    admin, upload = os.environ['WORKBENCH_ACCEPTANCE_TOKEN'], os.environ['WORKBENCH_SDK_TOKEN']
    class NoRedirect(urllib.request.HTTPRedirectHandler):
        def redirect_request(self, req, fp, code, msg, headers, newurl):
            return None

    opener = urllib.request.build_opener(NoRedirect)
    report = {'scope': 'Actual CLogan encrypted bytes over local HTTP; synthetic data only', 'checks': [], 'objects': {}}

    def req(method, path, body=None, expected=(200,), headers=None, raw=False, workspace=True, revision=None):
        h = {'Authorization': 'Bearer '+admin, 'X-Project-ID': a.project} if workspace else {}
        if method != 'GET' and workspace:
            h['Idempotency-Key'] = key()
        if revision is not None:
            h['If-Match'] = '"'+str(revision)+'"'
        if body is not None and not isinstance(body, bytes):
            h['Content-Type'] = 'application/json'
            body = json.dumps(body).encode()
        h.update(headers or {})
        r = urllib.request.Request(a.base+path, body, h, method=method)
        try:
            with opener.open(r, timeout=20) as response:
                code, wire = response.status, response.read()
        except urllib.error.HTTPError as e:
            code, wire = e.code, e.read()
        assert code in expected, (path, code, wire[:500].decode(errors='replace'))
        return wire if raw else json.loads(wire).get('data', json.loads(wire))

    def check(label):
        report['checks'].append({'name': label, 'passed': True})

    def done(run):
        deadline = time.monotonic()+20
        while run['status'] in ('queued', 'running') and time.monotonic()<deadline:
            time.sleep(.05)
            run = req('GET', '/parse-runs/'+run['id'])
        assert run['status'] not in ('queued', 'running'), run
        return run

    try:
        cap = req('GET', '/capabilities')
        assert cap['sdkReceiver']['configured']
        assert not cap['model']['configured'] and not cap['source']['configured']
        wire = a.native.read_bytes()
        h = {'X-Logan-Upload-Token': upload, 'X-Logan-Upload-ID': a.upload_id or 'triad-'+key(),
             'appId': 'com.hxl.xlb.test', 'deviceId': 'triad-device', 'platform': '1',
             'fileDate': '2026-10-19', 'appVersion': '1.0-triad', 'buildVersion': 'triad-42'}
        req('POST', '/sdk/native', wire, (401,), {**h, 'X-Logan-Upload-Token': 'invalid'}, workspace=False)
        req('GET', '/workspace', expected=(401,), headers={'Authorization':'Bearer '+upload})
        req('POST', '/sdk/native', wire, (403,), {**h, 'X-Project-ID':'other'}, workspace=False)
        check('upload-only authentication and server-bound project scope')
        receipt = req('POST', '/sdk/native', wire, headers=h, workspace=False)
        assetid, runid = receipt['assetId'], receipt['runId']
        report['objects'].update(assetId=assetid, runId=runid, receiptId=receipt['receiptId'])
        run = done(req('GET', '/parse-runs/'+runid))
        assert run['status']=='succeeded', run
        ws = req('GET', '/workspace')
        asset = next(x for x in ws['assets'] if x['id']==assetid)
        assert asset['origin']=='sdk' and asset['identitySource']=='sdk_reported' and not asset['issueId']
        assert asset['rawHash']==hashlib.sha256(wire).hexdigest()
        assert req('GET', '/assets/'+assetid+'/raw', raw=True)==wire
        assert asset['app']=='com.hxl.xlb.test' and asset['device']=='triad-device' and asset['build']=='triad-42'
        read = req('GET', '/parse-runs/'+runid+'/events')
        assert read['total']==4, read
        messages = [x['content'] for x in read['events']]
        assert sorted(messages) == sorted([
            'clogan header',
            'TRIAD|001|LoginView: user tapped login; requestId=triad-login-1',
            'TRIAD|002|AuthClient: POST /login timed out; requestId=triad-login-1',
            'TRIAD|003|LoginViewModel: terminal state not observed; root cause unknown']), messages
        check('SDK encrypted original retained, auto parsed and visible without manual import')
        again = req('POST', '/sdk/native', wire, headers=h, workspace=False)
        assert again['assetId']==assetid and again['runId']==runid
        req('POST', '/sdk/native', wire+b'x', (409,), h, workspace=False)
        check('duplicate replays same receipt; changed bytes conflict')
        issue = req('POST', '/issues', {'title':'SDK 贯通验收：登录超时待调查', 'description':'仅合成日志；超时不能证明 UI 根因', 'owner':'acceptance', 'priority':'P1', 'context':{k:asset[k] for k in ('app','platform','environment','device','version','build')}}, (201,))
        iid = issue['id']; report['objects']['issueId']=iid
        linked = req('POST', '/issues/'+iid+'/log-links', {'assetId':assetid}, revision=issue['revision'])
        assert assetid in linked['assetIds']
        event = next(x for x in read['events'] if x['content'].startswith('TRIAD|002|'))
        ev = req('POST', '/issues/'+iid+'/evidence', {'kind':'log','label':'SDK 原始超时记录','originRef':{'runId':runid,'eventIds':[event['id']]}}, (201,))
        report['objects']['evidenceId'] = ev['id']
        issue = req('GET','/issues/'+iid)
        if ev['id'] not in issue['evidenceIds']:
            req('PATCH','/issues/'+iid+'/context', {'add':[ev['id']], 'remove':[]}, revision=issue['contextRevision'])
        check('explicit issue link and immutable log evidence')
        issue = req('GET','/issues/'+iid)
        turn = req('POST','/issues/'+iid+'/turns', {'question':'超时是否足以证明界面状态错误？','contextRevision':issue['contextRevision']}, (201,200))
        assert turn['status']=='failed' and turn['error']['code']=='CONNECTOR_UNCONFIGURED' and not turn.get('answer')
        check('unconfigured AI explicitly fails without fabricated diagnosis')
        hypothesis = req('POST','/issues/'+iid+'/hypotheses', {'text':'登录超时可能导致终态未更新，现有日志不足以确认', 'evidenceIds':[ev['id']]}, (201,))
        v = req('POST','/issues/'+iid+'/verifications', {'purpose':'hypothesis','result':'uncertain','hypothesisId':hypothesis['id'],'hypothesisVersion':hypothesis['version'],'method':'复核三条 SDK 记录','expected':'需要终态变更和对应源码证据','observed':'仅见超时，缺少终态和精确源码映射','evidenceIds':[ev['id']]}, (201,))
        report['objects']['verificationId']=v['id']
        issue = req('GET','/issues/'+iid)
        preview = req('POST','/issues/'+iid+'/handoff-previews', {'mode':'manual','summary':'确认请求超时。界面根因未验证，需补充终态日志和对应构建源码。','owner':'acceptance','status':'investigating','issueRevision':issue['revision'],'contextRevision':issue['contextRevision']}, (201,))
        update = req('POST','/issues/'+iid+'/handoffs', {'draftId':preview['id'],'version':preview['version'],'hash':preview['hash'],'confirmed':True}, (201,200), revision=issue['revision'])
        report['objects']['updateId']=update['id']
        check('hypothesis remains uncertain and manual internal handoff preserves evidence')
        bad = req('POST','/sdk/native', b'not-native', headers={**h,'X-Logan-Upload-ID':'bad-'+key()}, workspace=False)
        failed=done(req('GET','/parse-runs/'+bad['runId'])); assert failed['status']=='failed'
        retry=done(req('POST','/assets/'+bad['assetId']+'/parse-runs', {'format':'native','mode':'strict'}, (200,201,202)))
        assert retry['id']!=failed['id'] and retry['status']=='failed'
        assert req('GET','/assets/'+bad['assetId']+'/raw',raw=True)==b'not-native'
        check('invalid native file retains original and independent failed reparse')
        report['passed']=True
    except Exception as e:
        report['passed']=False; report['failure']=str(e); raise
    finally:
        a.report.parent.mkdir(parents=True,exist_ok=True)
        a.report.write_text(json.dumps(report,ensure_ascii=False,indent=2))
        print(json.dumps({'passed':report.get('passed'),'checks':len(report['checks']),'report':str(a.report)}))


if __name__=='__main__':
    main()
