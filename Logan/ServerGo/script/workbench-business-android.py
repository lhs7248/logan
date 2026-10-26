#!/usr/bin/env python3
"""Verify an actual business App upload. Never creates events or uploads log files.

Reads private App evidence with run-as; reports IDs/counts/hashes, never credentials,
raw event content, installation identity, or the physical device serial.
"""
import argparse
from collections import Counter
import hashlib
import importlib.util
import json
from pathlib import Path
import re
import subprocess
import time
import urllib.parse
import urllib.request

APP = 'com.hxl.xlb.test'
MAX_EVENTS = 512
MAX_HEADERS = 32
EVENT_KEYS = {'eventName', 'activityCategory', 'sessionId', 'installationId', 'timestamp'}
EVENT_NAMES = {'app_start', 'activity_created', 'activity_resumed', 'activity_paused', 'activity_destroyed'}
CATEGORIES = {'application', 'home', 'login', 'container', 'debug'}


def require(condition, message):
    if not condition:
        raise ValueError(message)


def canonical(event):
    require(isinstance(event, dict) and set(event) == EVENT_KEYS, 'Unexpected event fields')
    require(event['eventName'] in EVENT_NAMES and event['activityCategory'] in CATEGORIES, 'Unexpected event category')
    require(type(event['timestamp']) is int and event['timestamp'] > 0, 'Invalid event timestamp')
    require(all(isinstance(event[k], str) and 0 < len(event[k]) <= 128 for k in ('sessionId', 'installationId')), 'Invalid event identity')
    return json.dumps(event, sort_keys=True, separators=(',', ':'))


def paged_events(get, run_id):
    path = '/parse-runs/' + urllib.parse.quote(run_id, safe='') + '/events?limit=1000'
    all_events, seen, total = [], set(), None
    for _ in range(16):
        page = get(path)
        require(type(page['total']) is int and 0 <= page['total'] <= MAX_EVENTS + MAX_HEADERS, 'Event total exceeds bounded verification')
        require(total is None or total == page['total'], 'Event total changed during paging')
        total = page['total']
        all_events.extend(page['events'])
        require(len(all_events) <= MAX_EVENTS + MAX_HEADERS, 'Event page bound exceeded')
        cursor = page.get('nextCursor', '')
        if not cursor:
            require(not page.get('truncated') and len(all_events) == total, 'Incomplete event pages')
            return all_events
        require(cursor not in seen, 'Repeated pagination cursor')
        seen.add(cursor)
        path = '/parse-runs/' + urllib.parse.quote(run_id, safe='') + '/events?limit=1000&cursor=' + urllib.parse.quote(cursor, safe='')
    raise ValueError('Pagination bound exceeded')


def verify_snapshot(profile, journal, receipt, get, baseline=None, source_bytes=None):
    require(isinstance(source_bytes, bytes) and hashlib.sha256(source_bytes).hexdigest() == receipt['sourceSha256']
            and len(source_bytes) == receipt['sourceBytes'], 'Retained App upload ciphertext differs from send snapshot')
    require(journal['schemaVersion'] == receipt['schemaVersion'] == 1, 'Unknown App evidence schema')
    require(receipt['state'] == 'http_success' and receipt['httpStatus'] == 200, 'No successful App HTTP callback')
    require(profile['appId'] == APP and profile['projectId'] == 'local-mobile', 'Wrong business profile')
    for key in ('appId', 'deviceId', 'projectId'):
        require(receipt[key] == profile[key], 'Upload identity differs from private profile')
    require(journal['installationId'] == profile['deviceId'], 'Journal installation differs from private profile')
    expected = receipt['events']
    require(0 < len(expected) <= MAX_EVENTS and len(journal['events']) <= MAX_EVENTS, 'Invalid snapshot/journal bound')
    expected_messages = Counter(canonical(e) for e in expected)
    journal_messages = Counter(canonical(e) for e in journal['events'])
    require(not expected_messages - journal_messages, 'Upload snapshot events missing from retained journal')
    require(all(e['installationId'] == profile['deviceId'] for e in expected + journal['events']), 'Event installation mismatch')
    response = receipt['response']
    run_id, asset_id = response['runId'], response['assetId']
    require(all(isinstance(v, str) and re.fullmatch(r'[A-Za-z0-9_-]{1,128}', v) for v in (run_id, asset_id, response['receiptId'])), 'Invalid receipt identifiers')
    run = get('/parse-runs/' + run_id)
    require(run['status'] == 'succeeded' and run['errorCount'] == 0 and run['mode'] == 'strict' and run['format'] == 'native', 'Native strict parse not successful')
    require(run['assetId'] == asset_id, 'Parse run belongs to another asset')
    assets = [x for x in get('/workspace')['assets'] if x['id'] == asset_id]
    require(len(assets) == 1, 'Uploaded asset missing or ambiguous')
    asset = assets[0]
    require(asset['sdkReceiptId'] == response['receiptId'] and asset['sdkClientId'] == 'business-android-test', 'Wrong SDK receipt/client')
    require(asset['origin'] == 'sdk' and asset['originalEncrypted'] is True and asset['identitySource'] == 'sdk_reported', 'Wrong SDK provenance')
    for key, value in {'app':APP, 'device':profile['deviceId'], 'platform':'android', 'environment':'test-local', 'version':receipt['appVersion'], 'build':receipt['buildVersion'], 'fileDate':receipt['date']}.items():
        require(asset.get(key) == value, 'Asset metadata mismatch: ' + key)
    events = paged_events(get, run_id)
    headers = [e for e in events if e['content'] == 'clogan header']
    require(len(headers) <= MAX_HEADERS and all(e.get('logType') == 1 for e in headers), 'Unexpected native header metadata/count')
    business_events = [e for e in events if e['content'] != 'clogan header']
    require(len(events) == run['eventCount'] and len(business_events) == len(expected), 'Parsed event count differs from App upload snapshot')
    require(Counter(canonical(json.loads(e['content'])) for e in business_events) == expected_messages, 'Exact parsed App events differ')
    wire = get('/assets/' + asset_id + '/raw', raw=True)
    digest = hashlib.sha256(wire).hexdigest()
    require(digest == asset['rawHash'] == receipt['sourceSha256'] and len(wire) == asset['size'] == receipt['sourceBytes'], 'Original ciphertext differs from actual App send snapshot')
    result = {'scope':'Actual business Android App upload; local Go strict parser', 'projectId':profile['projectId'], 'appId':APP,
              'appVersion':receipt['appVersion'], 'buildVersion':receipt['buildVersion'], 'attemptId':receipt['attemptId'],
              'assetId':asset_id, 'runId':run_id, 'receiptId':response['receiptId'], 'fileDate':receipt['date'],
              'parseStatus':run['status'], 'eventCount':len(events), 'businessEventCount':len(business_events), 'nativeHeaderCount':len(headers), 'rawBytes':len(wire), 'sha256':digest,
              'snapshotEventsSHA256':hashlib.sha256(json.dumps(sorted(expected_messages.items())).encode()).hexdigest(),
              'checks':{'exactAppSnapshotEvents':True, 'originalCiphertextMatchesSendSnapshot':True, 'sdkProvenance':True, 'strictParse':True}}
    if baseline is not None:
        for key in result.keys() - {'checks', 'scope'}:
            require(result[key] == baseline[key], 'Retained state changed: ' + key)
        result['checks']['baselineRetained'] = True
    return result


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--serial', required=True, help='Explicit adb device, never written to report')
    parser.add_argument('--root', type=Path)
    parser.add_argument('--report', type=Path, help='New report file; never overwrite')
    parser.add_argument('--baseline', type=Path, help='Prior report; compare before another upload')
    args = parser.parse_args(argv)
    spec = importlib.util.spec_from_file_location('workbench_local', Path(__file__).with_name('workbench-local.py'))
    mod = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(mod)
    runtime = mod.Runtime(args.root or mod.DEFAULT_ROOT)
    cfg = runtime.config()
    profile = runtime.business_profile(create=False)

    def private_bytes(name):
        process = subprocess.run(['adb', '-s', args.serial, 'exec-out', 'run-as', APP, 'cat', 'files/' + name], capture_output=True)
        require(process.returncode == 0 and len(process.stdout) <= 2 * 1024 * 1024, 'Cannot read bounded private App evidence')
        return process.stdout

    def private(name):
        return json.loads(private_bytes(name))

    require(private('workbench-profile.json') == profile, 'App private profile differs from receiver')
    receipt = private('workbench-last-upload.json')
    journal = private('workbench-events.json')
    require(private('workbench-last-upload.json') == receipt, 'Upload receipt changed while reading evidence')
    package = subprocess.run(['adb', '-s', args.serial, 'shell', 'dumpsys', 'package', APP], capture_output=True)
    text = package.stdout.decode(errors='replace')
    require(package.returncode == 0 and re.search(r'\bversionName=' + re.escape(receipt['appVersion']) + r'(?:\s|$)', text)
            and re.search(r'\bversionCode=' + re.escape(receipt['buildVersion']) + r'(?:\s|$)', text), 'Installed package version differs from upload receipt')
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}), mod.NoRedirect())

    def get(path, raw=False):
        request = urllib.request.Request(mod.URL + mod.API + path, headers={'Authorization':'Bearer ' + cfg['adminToken'], 'X-Project-ID':cfg['projectId']})
        with opener.open(request, timeout=10) as response:
            wire = response.read(32 * 1024 * 1024 + 1)
        require(len(wire) <= 32 * 1024 * 1024, 'Receiver response exceeds verification bound')
        return wire if raw else json.loads(wire)['data']

    run_id = receipt['response']['runId']
    for _ in range(80):
        if get('/parse-runs/' + urllib.parse.quote(run_id, safe=''))['status'] not in ('queued', 'running'):
            break
        time.sleep(.25)
    baseline = json.loads(args.baseline.read_text()) if args.baseline else None
    require(isinstance(receipt['attemptId'], str) and re.fullmatch(r'[A-Za-z0-9_-]{1,128}', receipt['attemptId']), 'Invalid attempt identity')
    require(receipt['sourcePath'] == 'workbench-uploads/' + receipt['attemptId'] + '.logan', 'Unexpected retained snapshot path')
    result = verify_snapshot(profile, journal, receipt, get, baseline, source_bytes=private_bytes(receipt['sourcePath']))
    if args.report:
        mod.private_json(args.report.absolute(), result)
    print(json.dumps(result, indent=2))
    return 0


if __name__ == '__main__':
    try:
        raise SystemExit(main())
    except (ValueError, KeyError, TypeError, OSError) as exc:
        # Avoid printing arbitrary malformed App fields or HTTP response bodies.
        print('Business upload verification failed (' + type(exc).__name__ + '); inspect private evidence locally.', file=__import__('sys').stderr)
        raise SystemExit(1)
