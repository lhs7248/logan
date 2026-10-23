#!/usr/bin/env python3
"""Provision the separate local Android example or verify its actual uploaded bytes.

No logs are manufactured or uploaded by this script. Use the App buttons first.
Credentials travel only over adb stdin to the application's private profile.
"""
import argparse
import hashlib
import importlib.util
import json
from pathlib import Path
import shlex
import subprocess
import time
import urllib.request

APP = 'test.logan.dianping.com.logan.workbench'
ACTIVITY = APP + '/test.logan.dianping.com.logan.WorkbenchActivity'


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('--serial', required=True, help='explicit adb device serial; not saved in reports')
    p.add_argument('--root', type=Path, help='existing private workbench runtime root')
    p.add_argument('command', choices=['provision', 'verify'])
    p.add_argument('--report', type=Path, help='sanitized verification report destination')
    p.add_argument('--baseline', type=Path, help='previous verification report to compare after restart/replay')
    a = p.parse_args()
    spec = importlib.util.spec_from_file_location('workbench_local', Path(__file__).with_name('workbench-local.py'))
    mod = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(mod)
    runtime = mod.Runtime(a.root or mod.DEFAULT_ROOT)
    cfg = runtime.config()

    def adb(*args, payload=None):
        return subprocess.run(['adb', '-s', a.serial, *args], input=payload,
                              capture_output=True, check=True).stdout

    def private(name):
        return adb('exec-out', 'run-as', APP, 'cat', 'files/' + name)

    profile = runtime.profile()
    if a.command == 'provision':
        # Refuse to change an existing installation's keys/config without inspection.
        exists = subprocess.run(['adb', '-s', a.serial, 'exec-out', 'run-as', APP,
                                 'test', '-e', 'files/workbench-profile.json'], capture_output=True)
        if exists.returncode == 0:
            if json.loads(private('workbench-profile.json')) != profile:
                raise RuntimeError('Existing App profile differs; retained unchanged. Inspect before reprovisioning.')
        else:
            remote = 'run-as ' + shlex.quote(APP) + ' sh -c ' + shlex.quote(
                'umask 077; mkdir -p files; cat > files/workbench-profile.json; chmod 600 files/workbench-profile.json')
            adb('shell', remote, payload=json.dumps(profile).encode())
        if json.loads(private('workbench-profile.json')) != profile:
            raise RuntimeError('Private profile verification failed')
        adb('reverse', 'tcp:8091', 'tcp:8091')
        print('Private App profile verified; USB loopback forwarding ready. Relaunch App if it was already running.')
        return

    if json.loads(private('workbench-profile.json')) != profile:
        raise RuntimeError('App and receiver profiles differ')
    capture = json.loads(private('workbench-last-capture.json'))
    receipt = json.loads(private('workbench-last-upload.json'))
    assert receipt['marker'] == capture['marker'], 'Receipt belongs to another capture'
    assert receipt['outcome'] == 'callback' and receipt['httpStatus'] == 200, 'No successful actual HTTP callback'
    expected = capture['messages']
    assert len(expected) == len(set(expected)) == 3

    class NoRedirect(urllib.request.HTTPRedirectHandler):
        def redirect_request(self, req, fp, code, msg, headers, newurl):
            return None

    op = urllib.request.build_opener(urllib.request.ProxyHandler({}), NoRedirect())
    base = 'http://127.0.0.1:8091/logan-web/api/workbench/v1'

    def get(path, raw=False):
        request = urllib.request.Request(base + path, headers={
            'Authorization': 'Bearer ' + cfg['adminToken'], 'X-Project-ID': cfg['projectId']})
        with op.open(request, timeout=10) as response:
            wire = response.read()
        return wire if raw else json.loads(wire)['data']

    run_id = receipt['runId']
    for _ in range(80):
        run = get('/parse-runs/' + run_id)
        if run['status'] not in ('queued', 'running'):
            break
        time.sleep(.25)
    assert run['status'] == 'succeeded' and run['errorCount'] == 0, 'Native parse not successful'
    asset = next(x for x in get('/workspace')['assets'] if x['id'] == receipt['assetId'])
    assert run['assetId'] == asset['id'] and asset['sdkReceiptId'] == receipt['receiptId']
    assert asset['origin'] == 'sdk' and asset['originalEncrypted'] and asset['identitySource'] == 'sdk_reported'
    assert asset['app'] == APP and asset['device'] == profile['deviceId']
    assert asset['fileDate'] == capture['fileDate'] and asset['platform'] == 'android'
    events = get('/parse-runs/' + run_id + '/events')
    assert not events['nextCursor'], 'Verification requires the full small test capture, not a truncated page'
    messages = [x['content'] for x in events['events']]
    assert all(messages.count(m) == 1 for m in expected), 'Exact App marker records missing or duplicated'
    wire = get('/assets/' + asset['id'] + '/raw', raw=True)
    digest = hashlib.sha256(wire).hexdigest()
    assert digest == asset['rawHash'] and len(wire) == asset['size']
    # The daily native filename is midnight epoch milliseconds in the device timezone.
    files = adb('exec-out', 'run-as', APP, 'ls', 'files/logan-workbench').decode().splitlines()
    matches = [name for name in files if name.isdigit() and
               hashlib.sha256(private('logan-workbench/' + name)).hexdigest() == digest]
    assert len(matches) == 1, 'Server ciphertext differs from the retained App native daily file'
    result = {'scope': 'Physical Android actual Java/JNI/CLogan upload; local Go service',
              'projectId': cfg['projectId'], 'appId': APP, 'marker': capture['marker'],
              'messages': expected, 'receiptId': receipt['receiptId'], 'assetId': asset['id'],
              'runId': run_id, 'parseStatus': run['status'], 'eventCount': events['total'],
              'rawBytes': len(wire), 'sha256': digest,
              'checks': {'privateProfileMatchesReceiver': True, 'exactThreeAppMessages': True,
                         'originalCiphertextMatchesApp': True, 'sdkProvenance': True}}
    if a.baseline:
        previous = json.loads(a.baseline.read_text())
        for key in ('projectId', 'appId', 'marker', 'messages', 'receiptId', 'assetId',
                    'runId', 'parseStatus', 'eventCount', 'rawBytes', 'sha256'):
            assert result[key] == previous[key], 'Retained state changed: ' + key
        result['checks']['baselineRetained'] = True
    if a.report:
        a.report.parent.mkdir(parents=True, exist_ok=True)
        a.report.write_text(json.dumps(result, ensure_ascii=False, indent=2) + '\n')
    print(json.dumps(result, ensure_ascii=False, indent=2))


if __name__ == '__main__':
    main()
