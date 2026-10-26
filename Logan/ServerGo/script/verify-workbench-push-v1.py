#!/usr/bin/env python3
"""Isolated real-HTTP acceptance for the provisional push adapter and callbacks.

The internal service is a localhost protocol double. No Aliyun/client delivery is
performed or claimed. Existing runtimes, private configuration and logs are not
read. --serve keeps the isolated fixture running for browser acceptance.
"""
import argparse
import datetime
import hashlib
import hmac
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import importlib.util
import json
import os
from pathlib import Path
import secrets
import signal
import socket
import subprocess
import tempfile
import threading
import time
import urllib.error
import urllib.parse
import urllib.request

HERE = Path(__file__).resolve().parent
BASE = '/logan-web/api/workbench/v1'
CALLBACK = BASE + '/integrations/push/events'
spec = importlib.util.spec_from_file_location('manual_acceptance', HERE / 'verify-workbench.py')
helpers = importlib.util.module_from_spec(spec)
spec.loader.exec_module(helpers)


def require(condition, message):
    if not condition:
        raise RuntimeError(message)


def now():
    return datetime.datetime.now(datetime.timezone.utc).isoformat()


def private_json(path, value):
    fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
    with os.fdopen(fd, 'w') as output:
        json.dump(value, output)


class Fixture:
    def __init__(self, binary, root):
        self.binary, self.root = binary, root
        self.process, self.log = None, None
        self.token, self.viewer, self.other = (secrets.token_urlsafe(32) for _ in range(3))
        self.outbound, self.callback_secret = (secrets.token_urlsafe(32) for _ in range(2))
        self.requests, self.errors, self.guard = [], [], threading.Lock()
        self.hold = threading.Event()
        self.auth, self.push = root / 'auth.json', root / 'push.json'
        with socket.socket() as probe:
            probe.bind(('127.0.0.1', 0))
            self.port = probe.getsockname()[1]
        self.url = 'http://127.0.0.1:' + str(self.port)
        private_json(self.auth, {'projects': [{'id': 'push-pilot', 'name': 'Push V1 · 隔离协议演练'},
                                             {'id': 'other', 'name': 'Isolated project'}],
                                 'tokens': [{'token': self.token, 'actor': 'push-reviewer', 'projects': {'push-pilot': 'reviewer'}, 'permissions': ['raw', 'export']},
                                            {'token': self.viewer, 'actor': 'push-viewer', 'projects': {'push-pilot': 'viewer'}, 'permissions': []},
                                            {'token': self.other, 'actor': 'other-reviewer', 'projects': {'other': 'reviewer'}, 'permissions': []}]})
        fixture = self

        class Provider(BaseHTTPRequestHandler):
            def log_message(self, *args):
                pass

            def do_POST(self):
                try:
                    payload = json.loads(self.rfile.read(int(self.headers.get('Content-Length', '0'))))
                    require(self.path == '/push-jobs', 'unexpected provider endpoint')
                    require(self.headers.get('Authorization') == 'Bearer ' + fixture.outbound, 'outbound authentication missing')
                    require(self.headers.get('Idempotency-Key') == payload['attemptId'], 'upstream attempt idempotency missing')
                    require(payload['schemaVersion'] == 1 and payload['projectId'] == 'push-pilot', 'outbound scope/version incorrect')
                    require(payload['callbackURL'] == fixture.url + CALLBACK, 'callback URL not operator configured')
                    with fixture.guard:
                        fixture.requests.append(payload)
                    target = payload['target']['value']
                    if target.startswith('synthetic-early'):
                        fixture.callback(fixture.event(payload, 'early-' + payload['attemptId']), expected=200)
                    if target.startswith('synthetic-hold'):
                        fixture.hold.wait(timeout=30)
                    if target.startswith('synthetic-unknown'):
                        self.send_response(503)
                        self.end_headers()
                        return
                    if target.startswith('synthetic-reject'):
                        self.send_response(400)
                        self.end_headers()
                        return
                    data = json.dumps({'schemaVersion': 1, 'status': 'accepted', 'providerRequestId': 'job-' + payload['attemptId']}).encode()
                    self.send_response(202)
                    self.send_header('Content-Type', 'application/json')
                    self.send_header('Content-Length', str(len(data)))
                    self.end_headers()
                    self.wfile.write(data)
                    if target.startswith('synthetic-browser'):
                        event = fixture.event(payload, 'browser-' + payload['attemptId'])
                        # Deliberate protocol double; never presented as device receipt.
                        timer = threading.Timer(.8, lambda: fixture.callback(event, expected=200))
                        timer.daemon = True
                        timer.start()
                except (BrokenPipeError, ConnectionResetError):
                    pass
                except Exception as error:
                    with fixture.guard:
                        fixture.errors.append(str(error))
                    self.send_response(500)
                    self.end_headers()

        self.provider = ThreadingHTTPServer(('127.0.0.1', 0), Provider)
        threading.Thread(target=self.provider.serve_forever, daemon=True).start()
        private_json(self.push, {'endpoint': 'http://127.0.0.1:' + str(self.provider.server_port) + '/push-jobs',
                                 'token': self.outbound, 'callbackSecret': self.callback_secret,
                                 'callbackURL': self.url + CALLBACK, 'projects': ['push-pilot'], 'deadlineSeconds': 600})

    def start(self):
        env = {k: v for k, v in os.environ.items() if not k.startswith('WORKBENCH_')}
        env.update(WORKBENCH_ADDR='127.0.0.1:' + str(self.port), WORKBENCH_PROFILE='manual-v1',
                   WORKBENCH_AUTH_FILE=str(self.auth), WORKBENCH_PUSH_CONFIG_FILE=str(self.push),
                   WORKBENCH_DATA_DIR=str(self.root / 'data'))
        self.log = tempfile.TemporaryFile()
        self.process = subprocess.Popen([str(self.binary)], env=env, stdout=self.log, stderr=self.log)
        deadline = time.monotonic() + 15
        while time.monotonic() < deadline:
            require(self.process.poll() is None, 'isolated workbench exited before startup')
            try:
                self.api('GET', '/session')
                return
            except (OSError, RuntimeError):
                time.sleep(.05)
        raise RuntimeError('isolated workbench startup timeout')

    def stop(self, crash=False):
        if self.process:
            if self.process.poll() is None:
                self.process.kill() if crash else self.process.terminate()
                try:
                    self.process.wait(timeout=15)
                except subprocess.TimeoutExpired:
                    self.process.kill()
                    self.process.wait()
                    raise RuntimeError('isolated workbench shutdown timeout')
            self.process = None
        if self.log:
            self.log.close()
            self.log = None

    def close(self):
        self.hold.set()
        self.stop()
        self.provider.shutdown()
        self.provider.server_close()

    def api(self, method, path, body=None, expected=200, key=None, revision=None, token=None, project='push-pilot'):
        headers = {'Authorization': 'Bearer ' + (token or self.token), 'X-Project-ID': project}
        if method != 'GET':
            headers['Idempotency-Key'] = key or helpers.key()
        if revision is not None:
            headers['If-Match'] = str(revision)
        wire = None
        if body is not None:
            headers['Content-Type'] = 'application/json'
            wire = json.dumps(body).encode()
        req = urllib.request.Request(self.url + BASE + path, wire, headers, method=method)
        try:
            with urllib.request.urlopen(req, timeout=20) as response:
                status, wire = response.status, response.read()
        except urllib.error.HTTPError as error:
            status, wire = error.code, error.read()
        require(status == expected, method + ' ' + path + ' returned ' + str(status) + ', expected ' + str(expected))
        return json.loads(wire).get('data') if wire else None

    def draft(self, target):
        return self.api('POST', '/collection-drafts', {'targetValue': target}, expected=201)

    def send(self, draft, key=None):
        return self.api('POST', '/collections', {'draftId': draft['id'], 'draftRevision': draft['revision']}, expected=201, key=key)

    def await_collection(self, cid, status):
        deadline = time.monotonic() + 10
        while time.monotonic() < deadline:
            c = self.api('GET', '/collections/' + cid)
            if c['status'] == status:
                return c
            time.sleep(.025)
        raise RuntimeError('collection did not reach ' + status)

    def payload(self, aid):
        deadline = time.monotonic() + 10
        while time.monotonic() < deadline:
            with self.guard:
                match = next((p for p in self.requests if p['attemptId'] == aid), None)
            if match:
                return match
            time.sleep(.025)
        raise RuntimeError('internal service did not receive attempt')

    def event(self, payload, event_id, kind='push_succeeded'):
        return {'schemaVersion': 1, 'eventId': event_id, 'requestId': payload['requestId'],
                'attemptId': payload['attemptId'], 'providerRequestId': 'job-' + payload['attemptId'],
                'eventType': kind, 'occurredAt': now()}

    def callback(self, event, expected=200, valid=True, stamp=None):
        wire = json.dumps(event, sort_keys=True).encode()
        stamp = stamp or str(int(time.time()))
        signature = hmac.new(self.callback_secret.encode(), stamp.encode() + b'\n' + wire, hashlib.sha256).hexdigest()
        req = urllib.request.Request(self.url + CALLBACK, wire,
                                     {'X-Logan-Push-Timestamp': stamp, 'X-Logan-Push-Signature': signature if valid else 'invalid', 'Content-Type': 'application/json'}, method='POST')
        try:
            with urllib.request.urlopen(req, timeout=10) as response:
                status = response.status
                response.read()
        except urllib.error.HTTPError as error:
            status = error.code
            error.read()
        require(status == expected, 'callback status ' + str(status) + ', expected ' + str(expected))


def verify(f, report):
    def checked(name):
        report['checks'].append({'name': name, 'passed': True})
    require(f.api('GET', '/capabilities')['collections']['configured'], 'connector not configured')
    require(not f.api('GET', '/capabilities', project='other', token=f.other)['collections']['configured'], 'connector leaked to other project')
    checked('connector is explicitly configured per project')
    draft = f.draft('synthetic-accept')
    f.api('POST', '/collections', {'draftId': draft['id'], 'draftRevision': 1}, token=f.viewer, expected=403)
    key = helpers.key()
    c = f.send(draft, key)
    repeated = f.send(draft, key)
    require(c['id'] == repeated['id'], 'duplicate collection')
    c = f.await_collection(c['id'], 'waiting_push')
    payload = f.payload(c['currentAttemptId'])
    require(payload['context']['begin'] == '' and payload['context']['title'] == '', 'optional context was invented')
    f.api('GET', '/collections/' + c['id'], project='other', token=f.other, expected=404)
    f.api('GET', '/collections/' + c['id'] + '/events', project='other', token=f.other, expected=404)
    require(len([p for p in f.requests if p['attemptId'] == c['currentAttemptId']]) == 1, 'duplicate outgoing call')
    checked('minimal device draft sends once with bearer, attempt identity and unchanged optional context')
    checked('viewer writes and authorized alternate-project object access are rejected')
    f.api('POST', '/collections', {'draftId': draft['id'], 'draftRevision': 1}, expected=409)
    event = f.event(payload, 'confirmed-send')
    f.callback(event, expected=401, valid=False)
    f.callback(event, expected=401, stamp='1')
    require(f.api('GET', '/collections/' + c['id'])['status'] == 'waiting_push', 'forged callback mutated state')
    checked('forged and stale signatures do not update progress')
    f.callback(event)
    f.callback(event)
    f.callback({**event, 'reasonCode': 'CHANGED'}, expected=409)
    c = f.await_collection(c['id'], 'waiting_logs')
    history = f.api('GET', '/collections/' + c['id'] + '/events')
    require(len(history) == 1 and history[0].get('receivedAt'), 'duplicate callback or missing server receive time')
    require(not f.api('GET', '/workspace')['assets'], 'push manufactured a file')
    checked('signed callback commits once, changed duplicate rejects, push success waits for logs without fake asset')
    f.callback(f.event(payload, 'contradictory-send', 'push_failed'))
    require(f.api('GET', '/collections/' + c['id'])['status'] == 'waiting_logs', 'contradictory callback regressed state')
    checked('contradictory terminal push observation is retained without state regression')
    early = f.send(f.draft('synthetic-early'))
    f.await_collection(early['id'], 'waiting_logs')
    checked('callback can arrive before HTTP acknowledgement without lock deadlock or regression')
    unknown_draft = f.draft('synthetic-unknown')
    unknown = f.send(unknown_draft)
    unknown = f.await_collection(unknown['id'], 'dispatch_unknown')
    old = f.payload(unknown['currentAttemptId'])
    f.api('POST', '/collections', {'draftId': unknown_draft['id'], 'draftRevision': 1}, expected=409)
    f.api('POST', '/collections/' + unknown['id'] + '/retries', {'confirmed': False, 'reason': 'test'}, revision=unknown['revision'], expected=422)
    retry = f.api('POST', '/collections/' + unknown['id'] + '/retries', {'confirmed': True, 'reason': 'Old send may still reach device'}, revision=unknown['revision'], expected=201)
    retry = f.await_collection(retry['id'], 'dispatch_unknown')
    require(retry['currentAttemptId'] != unknown['currentAttemptId'], 'retry reused old attempt')
    current_payload = f.payload(retry['currentAttemptId'])
    f.callback(f.event(old, 'late-previous-attempt'))
    require(f.api('GET', '/collections/' + retry['id'])['status'] == 'dispatch_unknown', 'old callback activated new attempt')
    checked('uncertain send needs explicit retry, duplicate new request is blocked, old callback cannot activate new attempt')
    cancelled = f.api('POST', '/collections/' + retry['id'] + '/cancel', {'confirmed': True, 'reason': 'Stop subsequent processing'}, revision=retry['revision'])
    f.callback(f.event(current_payload, 'late-cancelled-attempt'))
    require(f.api('GET', '/collections/' + retry['id'])['status'] == 'cancelled', 'cancelled state revived')
    checked('cancellation persists and late callback cannot revive it')
    rejected = f.send(f.draft('synthetic-reject'))
    f.await_collection(rejected['id'], 'push_failed')
    checked('definitive internal HTTP rejection remains distinct from unknown outcome')
    snapshots = {cid: f.api('GET', '/collections/' + cid) for cid in [c['id'], early['id'], cancelled['id'], rejected['id']]}
    events = f.api('GET', '/collections/' + c['id'] + '/events')
    f.stop()
    count = len(f.requests)
    f.start()
    for cid, saved in snapshots.items():
        require(f.api('GET', '/collections/' + cid) == saved, 'ended state changed on restart')
    f.callback(event)
    require(f.api('GET', '/collections/' + c['id'] + '/events') == events, 'duplicate receipt lost across restart')
    require(len(f.requests) == count, 'restart resent completed calls')
    checked('restart retains collection/attempt/history and callback idempotency without resending')
    pending = f.send(f.draft('synthetic-hold'))
    retained = f.payload(pending['currentAttemptId'])
    f.stop(crash=True)
    f.hold.set()
    f.start()
    pending = f.await_collection(pending['id'], 'dispatch_unknown')
    require(len([p for p in f.requests if p['attemptId'] == pending['currentAttemptId']]) == 1, 'interrupted request resent')
    f.callback(f.event(retained, 'interrupted-completion'))
    f.await_collection(pending['id'], 'waiting_logs')
    checked('interrupted in-flight send restarts as unknown and can accept a later genuine signed observation')
    require(not f.errors, 'protocol double rejected platform request')
    report['passed'] = True
    report['outgoingAttempts'] = len(f.requests)
    report['limitations'] = ['Loopback protocol double only; no actual internal service/Aliyun/device delivery.',
                             'This increment does not automatically correlate SDK uploads with collection attempts.',
                             'Deadline, concurrency and storage-failure bounds additionally covered by focused Go tests.']


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--binary', required=True, type=Path)
    parser.add_argument('--report', required=True, type=Path)
    parser.add_argument('--serve', action='store_true', help='Keep fixture for browser acceptance; writes private fixture.json under its temporary root')
    args = parser.parse_args()
    fd = os.open(args.report, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    report = {'scope': 'isolated real HTTP provisional push adapter and signed callback protocol', 'passed': False, 'checks': []}
    fixture = None
    try:
        with tempfile.TemporaryDirectory(prefix='logan-push-v1-') as scratch:
            root = Path(scratch).resolve()
            fixture = Fixture(args.binary.resolve(), root)
            fixture.start()
            verify(fixture, report)
            print(json.dumps({'passed': True, 'checks': len(report['checks'])}), flush=True)
            if args.serve:
                private_json(root / 'fixture.json', {'url': fixture.url, 'token': fixture.token, 'project': 'push-pilot'})
                print('Isolated browser fixture directory: ' + str(root), flush=True)
                stopped = threading.Event()
                signal.signal(signal.SIGTERM, lambda *_: stopped.set())
                signal.signal(signal.SIGINT, lambda *_: stopped.set())
                while not stopped.wait(.5):
                    pass
            fixture.close()
            fixture = None
    except Exception as error:
        report['failure'] = str(error)
        raise
    finally:
        if fixture:
            fixture.close()
        with os.fdopen(fd, 'w') as output:
            json.dump(report, output, ensure_ascii=False, indent=2)
            output.write('\n')


if __name__ == '__main__':
    main()
