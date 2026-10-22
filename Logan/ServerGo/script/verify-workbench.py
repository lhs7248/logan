#!/usr/bin/env python3
"""Real HTTP acceptance against an explicitly supplied isolated workbench server.

Creates synthetic issues/assets in the selected project. Never starts production
services, migrates databases, or contacts push/model/source providers.
"""
import argparse
import hashlib
import json
import os
import secrets
import time
import urllib.error
import urllib.parse
import urllib.request
import uuid


def key():
    return str(uuid.UUID(int=(int(time.time() * 1000) << 80) | (7 << 76) |
                         (secrets.randbits(12) << 64) | (2 << 62) | secrets.randbits(62)))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--base', default='http://127.0.0.1:8091/logan-web/api/workbench/v1')
    parser.add_argument('--project', required=True)
    parser.add_argument('--token-env', default='WORKBENCH_ACCEPTANCE_TOKEN')
    parser.add_argument('--report', required=True)
    args = parser.parse_args()
    token = os.environ.get(args.token_env)
    if not token:
        parser.error('the selected token environment variable is required')
    report = {'scope': 'isolated real HTTP workbench API with synthetic inputs', 'checks': [], 'objects': {}}

    def request(method, path, body=None, expected=(200,), idem=None, revision=None, auth=True, raw=False, content_type=None):
        headers = {'X-Project-ID': args.project}
        if auth:
            headers['Authorization'] = 'Bearer ' + token
        if method != 'GET':
            headers['Idempotency-Key'] = idem or key()
        if revision is not None:
            headers['If-Match'] = '"' + str(revision) + '"'
        if content_type:
            headers['Content-Type'] = content_type
        elif body is not None:
            headers['Content-Type'] = 'application/json'
            body = json.dumps(body).encode()
        req = urllib.request.Request(args.base.rstrip('/') + path, body, headers, method=method)
        try:
            with urllib.request.urlopen(req, timeout=20) as response:
                status, wire = response.status, response.read()
        except urllib.error.HTTPError as error:
            status, wire = error.code, error.read()
        assert status in expected, (method, path, status, wire[:1500].decode(errors='replace'))
        return wire if raw else json.loads(wire).get('data', json.loads(wire))

    def checked(name):
        report['checks'].append({'name': name, 'passed': True})

    try:
        request('GET', '/workspace', auth=False, expected=(401,))
        checked('unauthenticated API access rejected')
        session = request('GET', '/session')
        assert session.get('actor') and session.get('projects') and session.get('role')
        checked('authenticated project session')
        issue_body = {'title': 'HTTP 验收：上传、解析与人工调查', 'description': 'Synthetic acceptance only',
                      'owner': 'acceptance', 'priority': 'P1',
                      'context': {'app': 'acceptance', 'platform': 'Web', 'environment': 'isolated',
                                  'device': None, 'version': None, 'build': None}}
        idem = key()
        issue = request('POST', '/issues', issue_body, (200, 201), idem=idem)
        issue_id = issue['id']
        report['objects']['issueId'] = issue_id
        repeated = request('POST', '/issues', issue_body, (200, 201), idem=idem)
        assert repeated['id'] == issue_id
        request('POST', '/issues', {**issue_body, 'title': 'conflicting replay'}, (409,), idem=idem)
        checked('durable issue creation and idempotency content conflict')
        request('PATCH', '/issues/' + issue_id, {'owner': 'must-not-save'}, (409,), revision=-1)
        checked('stale issue revision rejected')

        rows = [{'time': '2026-10-21T10:00:00+08:00', 'level': 'INFO', 'module': 'Login',
                 'message': 'start request', 'requestId': 'acceptance-req'},
                {'time': '2026-10-21T10:00:05+08:00', 'level': 'ERROR', 'module': 'Network',
                 'message': 'timeout', 'requestId': 'acceptance-req'},
                {'time': '2026-10-21T10:00:06+08:00', 'level': 'WARN', 'module': 'Login',
                 'message': 'please retry', 'requestId': 'acceptance-req'}]
        wire = ('\n'.join(json.dumps(row) for row in rows) + '\ninvalid-json\n').encode()
        boundary = 'workbench-acceptance-' + secrets.token_hex(8)
        metadata = json.dumps({'issueId': issue_id, 'platform': 'Web', 'device': None, 'version': None, 'build': None})
        multipart = (f'--{boundary}\r\nContent-Disposition: form-data; name="metadata"\r\n\r\n{metadata}\r\n'
                     f'--{boundary}\r\nContent-Disposition: form-data; name="files"; filename="acceptance.jsonl"\r\n'
                     'Content-Type: application/octet-stream\r\n\r\n').encode() + wire + f'\r\n--{boundary}--\r\n'.encode()
        batch = request('POST', '/imports', multipart, (200, 201), content_type='multipart/form-data; boundary=' + boundary)
        asset = batch['assets'][0]
        asset_id = asset['id']
        report['objects']['assetId'] = asset_id
        assert asset.get('build') in (None, '')
        assert asset['rawHash'] == hashlib.sha256(wire).hexdigest()
        assert request('GET', '/assets/' + asset_id + '/raw', raw=True) == wire
        checked('original upload bytes/hash persisted and unknown identity preserved')

        def parse(mode):
            run = request('POST', '/assets/' + asset_id + '/parse-runs', {'format': 'jsonl', 'mode': mode}, (200, 201, 202))
            deadline = time.monotonic() + 20
            while run['status'] in ('queued', 'running') and time.monotonic() < deadline:
                time.sleep(.05)
                run = request('GET', '/parse-runs/' + run['id'])
            assert run['status'] not in ('queued', 'running'), run
            return run

        strict = parse('strict')
        assert strict['status'] == 'failed'
        partial = parse('skip_invalid_records')
        assert partial['id'] != strict['id'] and partial['status'] == 'partial'
        assert partial['eventCount'] == 3 and partial['errorCount'] == 1
        report['objects'].update(strictRunId=strict['id'], readableRunId=partial['id'])
        checked('strict parsing fails and skip-mode reparse publishes only valid records')
        comparison = request('GET', '/parse-runs/' + strict['id'] + '/comparison?otherRunId=' + partial['id'])
        assert comparison['sameAsset'] and comparison['addedCount'] == 3
        assert comparison['left']['status'] == 'failed'
        checked('parse comparison preserves failed version and independent readable publication')
        read = request('GET', '/parse-runs/' + partial['id'] + '/events')
        assert read['total'] == 3 and len(read['events']) == 3
        zero = request('GET', '/parse-runs/' + partial['id'] + '/events?query=never-present-marker')
        assert zero['total'] == 0 and not zero['events']
        errors = request('GET', '/parse-runs/' + partial['id'] + '/events?level=ERROR')
        assert errors['total'] == 1
        checked('event publication, zero-match and level filtering')
        event = errors['events'][0]
        evidence = request('POST', '/issues/' + issue_id + '/evidence',
                           {'kind': 'log', 'label': 'Observed timeout',
                            'originRef': {'runId': partial['id'], 'eventIds': [event['id']]}}, (200, 201))
        issue = request('GET', '/issues/' + issue_id)
        if evidence['id'] not in issue.get('evidenceIds', []):
            request('PATCH', '/issues/' + issue_id + '/context',
                    {'add': [evidence['id']], 'remove': []}, revision=issue['contextRevision'])
        checked('log evidence is tied to immutable run/event identity')
        request('POST', '/collections', {}, (503,))
        checked('deferred Aliyun connector cannot fabricate push success')
        capture = request('POST', '/collection-drafts',
                          {'app': 'com.hxl.xlb.test', 'targetType': 'alias',
                           'targetValue': '13800000000', 'bindingConfirmed': True,
                           'issueId': issue_id, 'title': 'Synthetic unsent draft',
                           'begin': '2026-10-21T10:00:00+08:00'}, (201,))
        assert capture['status'] == 'draft' and capture['targetValue'] != '13800000000'
        capture_id = capture['id']
        request('PATCH', '/collection-drafts/' + capture_id,
                {'targetValue': capture['targetValue'], 'title': 'must not corrupt target'},
                (422,), revision=capture['revision'])
        capture = request('PATCH', '/collection-drafts/' + capture_id,
                          {'title': 'Purpose edited without replacing masked target'}, revision=capture['revision'])
        assert capture['title'] == 'Purpose edited without replacing masked target'
        assert capture['bindingConfirmed'] is True
        assert request('GET', '/collection-drafts/' + capture_id)['id'] == capture_id
        checked('unfinished collection draft persists without masked target overwrite or fake push')
        capability = request('GET', '/capabilities')
        report['capabilities'] = capability
        checked('capabilities expose configuration state')
        if not capability.get('model', {}).get('configured'):
            issue = request('GET', '/issues/' + issue_id)
            turn = request('POST', '/issues/' + issue_id + '/turns',
                           {'question': 'Can timeout alone establish a UI state defect?',
                            'contextRevision': issue['contextRevision']}, (200, 201))
            assert turn['status'] == 'failed' and not turn.get('answer')
            assert turn['error']['code'] == 'CONNECTOR_UNCONFIGURED'
            checked('unconfigured model creates explicit failed round and never fabricates an answer')
        draft = request('POST', '/issues/' + issue_id + '/handoff-drafts',
                        {'mode': 'manual', 'summary': 'Incomplete draft retained before preview',
                         'owner': 'acceptance', 'status': 'investigating'}, (200, 201))
        restored = request('GET', '/handoff-drafts/' + draft['id'])
        assert restored['summary'] == draft['summary'] and restored['editable']
        checked('pre-preview manual draft is durable and recoverable')

        # A different known build/device is an explicit investigation decision,
        # never a silent rewrite of an uploaded asset's original identity.
        other_issue = request('POST', '/issues',
                              {**issue_body, 'title': 'HTTP 验收：差异日志关联',
                               'context': {'platform': 'android', 'device': 'other-device'}}, (201,))
        link_path = '/issues/' + other_issue['id'] + '/log-links'
        request('POST', link_path, {'assetId': asset_id}, (409,))
        linked = request('POST', link_path,
                         {'assetId': asset_id, 'confirmed': True, 'reason': 'Explicit cross-platform comparison'},
                         revision=other_issue['revision'])
        assert asset_id in linked['assetIds']
        history = request('GET', '/issues/' + other_issue['id'] + '/history')
        assert any(x.get('reason') == 'Explicit cross-platform comparison' for x in history)
        checked('different log identity requires confirmation and appends relationship history')

        issue = request('GET', '/issues/' + issue_id)
        preview = request('POST', '/issues/' + issue_id + '/handoff-previews',
                          {'mode': 'manual', 'summary': 'Observed timeout; cause and fix remain unverified.',
                           'owner': 'acceptance', 'status': 'investigating', 'issueRevision': issue['revision'],
                           'contextRevision': issue['contextRevision']}, (200, 201))
        handoff_key = key()
        handoff_body = {'draftId': preview['id'], 'version': preview['version'], 'hash': preview['hash'], 'confirmed': True}
        update = request('POST', '/issues/' + issue_id + '/handoffs', handoff_body,
                         (200, 201), idem=handoff_key, revision=issue['revision'])
        repeat_update = request('POST', '/issues/' + issue_id + '/handoffs', handoff_body,
                                (200, 201), idem=handoff_key, revision=issue['revision'])
        assert update['id'] == repeat_update['id']
        report['objects']['updateId'] = update['id']
        checked('manual investigation handoff works without model and replays once')
        workspace = request('GET', '/workspace')
        assert any(x['id'] == issue_id for x in workspace['issues'])
        assert any(x['id'] == asset_id for x in workspace['assets'])
        checked('workspace reflects durable linked business objects')
        report['passed'] = True
    except Exception as error:
        report['passed'] = False
        report['failure'] = str(error)
        raise
    finally:
        with open(args.report, 'w', encoding='utf8') as output:
            json.dump(report, output, ensure_ascii=False, indent=2)
        print(json.dumps({'passed': report.get('passed'), 'checks': len(report['checks']), 'report': args.report}))


if __name__ == '__main__':
    main()
