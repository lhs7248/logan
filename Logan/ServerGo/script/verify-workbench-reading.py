#!/usr/bin/env python3
"""Synthetic real-HTTP acceptance for complete RN attempt scope.

Uses only a temporary isolated service. Never reads operator auth/log directories.
--serve retains a private fixture for optional browser walkthroughs.
"""
import argparse
import atexit
import json
import os
import pathlib
import secrets
import shutil
import signal
import socket
import subprocess
import tempfile
import time
import urllib.error
import urllib.request
import uuid


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--binary', required=True)
    parser.add_argument('--report', required=True)
    parser.add_argument('--serve', action='store_true')
    args = parser.parse_args()
    root = pathlib.Path(tempfile.mkdtemp(prefix='logan-web-review-')).resolve()
    os.chmod(root, 0o700)
    token = secrets.token_urlsafe(32)
    auth = {'projects': [{'id': 'web-review', 'name': 'Web 优化验收 · 合成日志'}], 'tokens': [{'token': token, 'actor': 'web-reviewer', 'projects': {'web-review': 'reviewer'}, 'permissions': ['raw', 'export']}]}

    def private(path, obj):
        fd = os.open(str(path), os.O_CREAT | os.O_TRUNC | os.O_WRONLY, 0o600)
        with os.fdopen(fd, 'w') as f:
            json.dump(obj, f)
    private(root / 'auth.json', auth)
    with socket.socket() as sock:
        sock.bind(('127.0.0.1', 0))
        port = sock.getsockname()[1]
    url = f'http://127.0.0.1:{port}'
    base = url + '/logan-web/api/workbench/v1'
    env = {k: v for (k, v) in os.environ.items() if not k.startswith('WORKBENCH_')}
    env.update(WORKBENCH_ADDR=f'127.0.0.1:{port}', WORKBENCH_PROFILE='manual-v1', WORKBENCH_AUTH_FILE=str(root / 'auth.json'), WORKBENCH_DATA_DIR=str(root / 'data'))
    log = open(root / 'service.log', 'w')
    proc = subprocess.Popen([str(pathlib.Path(args.binary).resolve())], env=env, stdout=log, stderr=log)

    def cleanup():
        if proc.poll() is None:
            proc.terminate()
            proc.wait(timeout=15)
        log.close()
        if not args.serve:
            shutil.rmtree(root, ignore_errors=True)
    atexit.register(cleanup)

    def key():
        return str(uuid.UUID(int=int(time.time() * 1000) << 80 | 7 << 76 | secrets.randbits(12) << 64 | 2 << 62 | secrets.randbits(62)))

    def req(method, path, body=None, raw=False, ctype=None):
        headers = {'Authorization': 'Bearer ' + token, 'X-Project-ID': 'web-review', 'Idempotency-Key': key()}
        if body is not None:
            headers['Content-Type'] = ctype or 'application/json'
            if not ctype:
                body = json.dumps(body).encode()
        with urllib.request.urlopen(urllib.request.Request(base + path, body, headers, method=method), timeout=20) as r:
            data = r.read()
        return data if raw else json.loads(data)['data']
    for _ in range(100):
        try:
            req('GET', '/session')
            break
        except Exception:
            if proc.poll() is not None:
                raise RuntimeError((root / 'service.log').read_text())
            time.sleep(0.1)
    issue = req('POST', '/issues', {'title': 'MiniApp 打不开 · 阅读连续性验收', 'description': '合成数据，不是真实用户日志', 'context': {'app': 'HermesDemo', 'platform': 'ios', 'environment': 'isolated', 'device': 'synthetic-ios', 'version': '1.0', 'build': '1'}, 'owner': 'reviewer'})
    req('POST', '/issues', {'title': '其他工单 · 搜索对照', 'description': '合成对照工单'})
    rows = []
    for i in range(450):
        attempt = 'attempt_a' if i < 260 else 'attempt_b'
        name = 'page_open_started'
        if i == 258:
            name = 'content_ready'
        if i == 259:
            name = 'page_interactive'
        if i == 449:
            name = 'bundle_load_failed'
        d = {'schema': 'xlb.page-diagnostic', 'schemaVersion': 1, 'eventId': f'e_{i}', 'pageAttemptId': attempt, 'pageName': 'MiniApp', 'eventName': name, 'stage': 'bundle' if i == 449 else 'page', 'outcome': 'failed' if i == 449 else 'started', 'occurredAtMs': 1792980000123 + i * 100, 'monotonicMs': float(i * 100), 'source': 'rn', 'level': 'error' if i == 449 else 'info', 'module': 'bundle' if i == 449 else 'page'}
        if i == 449:
            d['errorCode'] = 'REMOTE_LOAD_FAILED'
        rows.append({'content': json.dumps(d)})
    wire = ('\n'.join((json.dumps(r) for r in rows)) + '\n').encode()
    boundary = 'synthetic-' + secrets.token_hex(8)
    meta = {'issueId': issue['id'], 'app': 'HermesDemo', 'platform': 'ios', 'environment': 'isolated', 'device': 'synthetic-ios', 'version': '1.0', 'build': '1', 'declaredFormat': 'jsonl'}
    body = (f'--{boundary}\r\nContent-Disposition: form-data; name="metadata"\r\n\r\n' + json.dumps(meta) + f'\r\n--{boundary}\r\nContent-Disposition: form-data; name="files"; filename="synthetic-page-attempts.jsonl"\r\nContent-Type: application/octet-stream\r\n\r\n').encode() + wire + f'\r\n--{boundary}--\r\n'.encode()
    batch = req('POST', '/imports', body, ctype='multipart/form-data; boundary=' + boundary)
    run = req('POST', '/assets/' + batch['assets'][0]['id'] + '/parse-runs', {'format': 'jsonl', 'mode': 'strict'})
    for _ in range(200):
        r = req('GET', '/parse-runs/' + run['id'])
        if r['status'] not in ['queued', 'running']:
            break
        time.sleep(0.1)
    assert r['status'] == 'succeeded', r
    summaries = req('GET', '/parse-runs/' + run['id'] + '/page-attempts')
    assert len(summaries['attempts']) == 2
    assert summaries['attempts'][0]['status'] == 'succeeded'
    assert summaries['attempts'][0]['eventCount'] == 260
    assert len(summaries['attempts'][0]['events']) <= 48 and summaries['attempts'][0]['stagesTruncated'] is True
    assert summaries['attempts'][1]['status'] == 'failed'
    for level, matches in [('INFO', [260, 189]), ('ERROR', [0, 1])]:
        filtered = req('GET', '/parse-runs/' + run['id'] + '/page-attempts?level=' + level)
        assert filtered['summaryScope'] == 'entire_run'
        assert [a['status'] for a in filtered['attempts']] == ['succeeded', 'failed']
        assert [a['matchingEventCount'] for a in filtered['attempts']] == matches
    for (attempt, count) in [('attempt_a', 260), ('attempt_b', 190)]:
        ev = req('GET', '/parse-runs/' + run['id'] + '/events?pageAttemptId=' + attempt)
        hist = req('GET', '/parse-runs/' + run['id'] + '/histogram?bins=24&pageAttemptId=' + attempt)
        exp = req('POST', '/exports', {'kind': 'events', 'runId': run['id'], 'pageAttemptId': attempt})
        payload = json.loads(req('GET', '/exports/' + exp['id'] + '/content', raw=True))
        assert ev['total'] == hist['total'] == payload['total'] == count, (ev['total'], hist['total'], payload)
        assert ev['filterHash'] == hist['filterHash'] == payload['filterHash']
    fixture = {'url': url, 'project': 'web-review', 'token': token, 'issueId': issue['id'], 'runId': run['id'], 'root': str(root), 'pid': proc.pid}
    private(root / 'fixture.json', fixture)
    report = {'passed': True, 'scope': 'synthetic real HTTP only', 'checks': ['complete attempt summary includes readiness after event 200', 'bounded stage navigation preserves full eventCount', 'INFO and ERROR filters retain full attempt conclusions and accurate matching counts', 'attempt_a events/histogram/export count and filterHash agree: 260', 'attempt_b events/histogram/export count and filterHash agree: 190'], 'totalEvents': 450, 'originalServicesTouched': False}
    pathlib.Path(args.report).write_text(json.dumps(report, ensure_ascii=False, indent=2) + '\n')
    print(json.dumps({'url': url, 'fixture': str(root / 'fixture.json'), 'httpChecks': 'full summary and attempt event/histogram/export scope passed'}), flush=True)

    def stop(*args):
        proc.terminate()
        proc.wait(timeout=15)
        raise SystemExit(0)
    signal.signal(signal.SIGTERM, stop)
    signal.signal(signal.SIGINT, stop)
    if args.serve:
        while proc.poll() is None:
            time.sleep(1)
    else:
        proc.terminate()
        proc.wait(timeout=15)
        log.close()
        shutil.rmtree(root)
        print(json.dumps({'passed': True, 'checks': len(report['checks'])}), flush=True)


if __name__ == '__main__':
    main()
