#!/usr/bin/env python3
"""Run synthetic manual investigation/restart/restore acceptance in temporary storage.

Requires an explicit built workbench binary. Never connects to the operator's
runtime, imports real logs, or reads existing credentials. Report excludes tokens.
"""
import argparse
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import secrets
import socket
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.request
import urllib.parse

HERE = Path(__file__).resolve().parent
spec = importlib.util.spec_from_file_location('http_acceptance', HERE / 'verify-workbench.py')
http_acceptance = importlib.util.module_from_spec(spec)
spec.loader.exec_module(http_acceptance)


def require(condition, message):
    if not condition:
        raise RuntimeError(message)


class Runtime:
    def __init__(self, binary, root, auth, token, viewer):
        self.binary, self.root, self.auth = binary, root, auth
        self.token, self.viewer = token, viewer
        self.process = None
        self.log = None

    def start(self):
        with socket.socket() as probe:
            probe.bind(('127.0.0.1', 0))
            port = probe.getsockname()[1]
        self.url = 'http://127.0.0.1:' + str(port)
        # Do not inherit optional provider/database/decoder settings.
        env = {k: v for k, v in os.environ.items() if not k.startswith('WORKBENCH_')}
        env.update(WORKBENCH_ADDR='127.0.0.1:' + str(port), WORKBENCH_DATA_DIR=str(self.root),
                   WORKBENCH_AUTH_FILE=str(self.auth), WORKBENCH_PROFILE='manual-v1')
        self.log = tempfile.TemporaryFile()
        self.process = subprocess.Popen([str(self.binary)], env=env, stdout=self.log, stderr=self.log)
        deadline = time.monotonic() + 15
        while time.monotonic() < deadline:
            require(self.process.poll() is None, 'isolated runtime exited during startup')
            try:
                self.call('GET', '/session')
                return
            except (OSError, RuntimeError):
                time.sleep(.05)
        raise RuntimeError('isolated runtime did not become available')

    def stop(self, crash=False):
        if self.process:
            if self.process.poll() is None:
                self.process.kill() if crash else self.process.terminate()
                try:
                    self.process.wait(timeout=15)
                except subprocess.TimeoutExpired:
                    self.process.kill()
                    self.process.wait()
                    raise RuntimeError('isolated runtime did not shut down gracefully')
            self.process = None
        if self.log:
            self.log.close()
            self.log = None

    def call(self, method, path, body=None, status=200, viewer=False, project='manual-v1', revision=None, raw=False, content_type=None, credential=None):
        headers = {'Authorization': 'Bearer ' + (credential or (self.viewer if viewer else self.token)), 'X-Project-ID': project}
        if method != 'GET':
            headers['Idempotency-Key'] = http_acceptance.key()
        if revision is not None:
            headers['If-Match'] = '"' + str(revision) + '"'
        wire = None
        if body is not None:
            headers['Content-Type'] = content_type or 'application/json'
            wire = body if content_type else json.dumps(body).encode()
        request = urllib.request.Request(self.url + '/logan-web/api/workbench/v1' + path, wire, headers, method=method)
        try:
            with urllib.request.urlopen(request, timeout=20) as response:
                code, result = response.status, response.read()
        except urllib.error.HTTPError as error:
            code, result = error.code, error.read()
        require(code == status, method + ' ' + path + ': unexpected status ' + str(code))
        return result if raw else json.loads(result).get('data')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--binary', required=True, type=Path)
    parser.add_argument('--report', required=True, type=Path)
    args = parser.parse_args()
    # Exclusive report creation also rejects symlink destinations.
    fd = os.open(args.report, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    report = {'scope': 'isolated synthetic enterprise-intranet single-instance candidate', 'passed': False, 'checks': []}
    runtime = None
    started = time.monotonic()
    def checked(name):
        report['checks'].append({'name': name, 'passed': True})
    try:
        with tempfile.TemporaryDirectory(prefix='logan-manual-v1-') as scratch:
            root = Path(scratch).resolve()
            auth = root / 'auth.json'
            token, viewer, other_token = (secrets.token_urlsafe(32) for _ in range(3))
            auth.write_text(json.dumps({'projects': [{'id': 'manual-v1', 'name': 'Synthetic acceptance'},
                                                     {'id': 'other', 'name': 'Isolated project'}],
                                        'tokens': [{'token': token, 'actor': 'reviewer', 'projects': {'manual-v1': 'reviewer'},
                                                    'permissions': ['raw', 'export']},
                                                   {'token': viewer, 'actor': 'viewer', 'projects': {'manual-v1': 'viewer'},
                                                    'permissions': []},
                                                   {'token': other_token, 'actor': 'other-reviewer', 'projects': {'other': 'reviewer'},
                                                    'permissions': ['raw', 'export']}]}))
            auth.chmod(0o600)
            runtime = Runtime(args.binary.resolve(), root / 'data', auth, token, viewer)
            runtime.start()
            with urllib.request.urlopen(runtime.url + '/readyz', timeout=5) as response:
                require(response.status == 200, 'readiness failed')
            checked('manual-v1 private configuration starts and readiness responds')
            env = {k: v for k, v in os.environ.items() if not k.startswith('WORKBENCH_')}
            env['WORKBENCH_ACCEPTANCE_TOKEN'] = token
            business_report = root / 'business.json'
            result = subprocess.run([sys.executable, str(HERE / 'verify-workbench.py'), '--base',
                                     runtime.url + '/logan-web/api/workbench/v1', '--project', 'manual-v1',
                                     '--report', str(business_report), '--manual-only'], env=env, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
            business = json.loads(business_report.read_text())
            report['businessChecks'] = business['checks']
            require(result.returncode == 0, 'existing real HTTP workflow failed: ' + business.get('failure', 'unknown failure'))
            ids = business['objects']
            iid, aid, rid = ids['issueId'], ids['assetId'], ids['readableRunId']
            path = '/issues/' + iid
            runtime.call('POST', '/issues', {'title': 'forbidden'}, status=403, viewer=True)
            runtime.call('GET', path, status=403, project='other')
            runtime.call('GET', path, status=404, project='other', credential=other_token)
            runtime.call('GET', '/assets/' + aid + '/raw', status=404, project='other', credential=other_token)
            runtime.call('GET', '/parse-runs/' + rid + '/events', status=404, project='other', credential=other_token)
            other_workspace = runtime.call('GET', '/workspace', project='other', credential=other_token)
            require(not other_workspace['issues'] and not other_workspace['assets'],
                    'authorized other-project workspace leaked foreign objects')
            runtime.call('GET', '/assets/' + aid + '/raw', status=403, viewer=True)
            checked('viewer cannot write or download raw data and cross-project access is rejected')
            hypothesis = runtime.call('POST', path + '/hypotheses', {'text': 'Timeout may explain delayed first render; cause remains uncertain.'}, status=201)
            evidence = runtime.call('POST', path + '/evidence', {'kind': 'manual', 'label': 'Synthetic reproduction',
                                   'content': 'Synthetic check: retry produced content. This is not a business-device test.'}, status=201)
            runtime.call('POST', path + '/verifications', {'purpose': 'hypothesis', 'hypothesisId': hypothesis['id'],
                         'hypothesisVersion': hypothesis['version'], 'method': 'manual comparison', 'expected': 'timeout correlates with delay',
                         'observed': 'insufficient timing evidence', 'result': 'uncertain', 'evidenceIds': [evidence['id']]}, status=201)
            # Hypothesis verification alone must not authorize issue resolution.
            issue = runtime.call('GET', path)
            runtime.call('PATCH', path, {'status': 'resolved'}, revision=issue['revision'], status=422)
            checked('manual hypothesis preserves uncertainty and cannot substitute for fix verification')
            runtime.call('POST', path + '/verifications', {'purpose': 'fix', 'method': 'synthetic regression',
                         'expected': 'content renders', 'observed': 'content renders', 'result': 'pass', 'build': 'synthetic-2',
                         'scenario': 'retry after timeout', 'evidenceIds': [evidence['id']]}, status=201)
            def handoff(status):
                issue = runtime.call('GET', path)
                draft = runtime.call('POST', path + '/handoff-previews', {'mode': 'manual',
                                     'summary': 'Synthetic manual acceptance; root cause remains uncertain.', 'owner': 'reviewer',
                                     'status': status, 'issueRevision': issue['revision'],
                                     'contextRevision': issue['contextRevision']}, status=201)
                return runtime.call('POST', path + '/handoffs', {'draftId': draft['id'], 'version': draft['version'],
                                    'hash': draft['hash'], 'confirmed': True}, revision=issue['revision'], status=201)
            handoff('awaiting_verification')
            handoff('resolved')
            checked('fix verification and explicitly confirmed manual handoff persist a resolved internal issue')
            # A bounded representative fixture checks pagination and combined
            # filtering. Its measurements describe this fixture, not a load SLA.
            events = [{'time': '2026-10-26T00:00:%02dZ' % (i % 60),
                       'level': 'ERROR' if i % 10 == 0 else 'INFO', 'module': 'SyntheticPage',
                       'message': 'synthetic row ' + str(i), 'requestId': 'request-' + str(i // 10)}
                      for i in range(2000)]
            wire = ('\n'.join(json.dumps(row) for row in events) + '\n').encode()
            boundary = 'manual-v1-' + secrets.token_hex(8)
            multipart = (f'--{boundary}\r\nContent-Disposition: form-data; name="metadata"\r\n\r\n{{"platform":"web"}}\r\n'
                         f'--{boundary}\r\nContent-Disposition: form-data; name="files"; filename="bounded.jsonl"\r\n'
                         'Content-Type: application/octet-stream\r\n\r\n').encode() + wire + f'\r\n--{boundary}--\r\n'.encode()
            batch = runtime.call('POST', '/imports', multipart, status=201,
                                 content_type='multipart/form-data; boundary=' + boundary)
            load_asset = batch['assets'][0]['id']
            load_run = runtime.call('POST', '/assets/' + load_asset + '/parse-runs',
                                    {'format': 'jsonl', 'mode': 'strict'}, status=201)
            deadline = time.monotonic() + 30
            while load_run['status'] in ('queued', 'running') and time.monotonic() < deadline:
                time.sleep(.05)
                load_run = runtime.call('GET', '/parse-runs/' + load_run['id'])
            require(load_run['status'] == 'succeeded' and load_run['eventCount'] == 2000,
                    'bounded fixture did not parse completely')
            load_path = '/parse-runs/' + load_run['id']
            seen, cursor = set(), ''
            while True:
                page = runtime.call('GET', load_path + '/events?limit=137&cursor=' + urllib.parse.quote(cursor))
                require(page['total'] == 2000, 'pagination changed total')
                for event in page['events']:
                    require(event['id'] not in seen, 'pagination duplicated an event')
                    seen.add(event['id'])
                cursor = page['nextCursor']
                if not cursor:
                    break
            require(len(seen) == 2000, 'pagination omitted events')
            filtered = runtime.call('GET', load_path + '/events?level=ERROR&module=SyntheticPage')
            histogram = runtime.call('GET', load_path + '/histogram?level=ERROR&module=SyntheticPage&bins=24')
            require(filtered['total'] == histogram['total'] == 200 and
                    filtered['filterHash'] == histogram['filterHash'], 'filter/histogram identity mismatch')
            checked('2000-event strict import, complete pagination and combined histogram filters agree')
            routes = [path, path + '/hypotheses', path + '/verifications', path + '/history',
                      '/parse-runs/' + rid + '/events', load_path + '/events?level=ERROR', '/workspace']
            snapshot = {p: runtime.call('GET', p) for p in routes}
            original = runtime.call('GET', '/assets/' + aid + '/raw', raw=True)
            def compare():
                for p, expected in snapshot.items():
                    require(runtime.call('GET', p) == expected, 'business state changed after recovery at ' + p)
                require(runtime.call('GET', '/assets/' + aid + '/raw', raw=True) == original, 'original bytes changed after recovery')
            runtime.stop()
            runtime.start()
            compare()
            checked('graceful process restart retains original bytes, parsed events and complete manual investigation history')
            runtime.stop(crash=True)
            runtime.start()
            compare()
            checked('abrupt idle process termination retains all acknowledged business records')
            # Check bounded small-scale access without claiming a production SLA.
            samples = []
            for _ in range(30):
                tick = time.monotonic()
                runtime.call('GET', load_path + '/events?level=ERROR')
                samples.append((time.monotonic() - tick) * 1000)
            report['measurement'] = {'syntheticEvents': 2000, 'sequentialFilteredReads': 30,
                                     'p95Milliseconds': round(sorted(samples)[28], 3),
                                     'limitation': 'loopback bounded fixture; not capacity or soak certification'}
            runtime.stop()
            ops = [sys.executable, str(HERE / 'workbench-ops.py')]
            def operate(*arguments):
                result = subprocess.run(ops + list(map(str, arguments)), stdout=subprocess.PIPE, stderr=subprocess.PIPE)
                require(result.returncode == 0, 'offline recovery operation failed: ' + str(arguments[0]))
            backup, restored = root / 'backup', root / 'restored'
            operate('backup', '--data-dir', root / 'data', '--auth-file', auth, '--output', backup)
            operate('verify', '--backup', backup)
            operate('restore', '--backup', backup, '--destination', restored)
            runtime = Runtime(args.binary.resolve(), restored / 'data', restored / 'auth.json', token, viewer)
            runtime.start()
            compare()
            checked('offline checksum-verified backup restores authentication, originals and complete business state into a new directory')
            report['rawDigest'] = hashlib.sha256(original).hexdigest()
            report['passed'] = True
    except Exception as error:
        report['failure'] = str(error)
        raise
    finally:
        if runtime:
            runtime.stop()
        report['elapsedSeconds'] = round(time.monotonic() - started, 3)
        with os.fdopen(fd, 'w') as output:
            json.dump(report, output, ensure_ascii=False, indent=2)
            output.write('\n')
        print(json.dumps({'passed': report['passed'], 'checks': len(report['checks']), 'report': str(args.report)}))


if __name__ == '__main__':
    main()
