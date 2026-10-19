"""Real HTTP positive business chain. Runs inside the isolated Docker network."""
import base64
import gzip
import hashlib
import json
import sys
import time
import urllib.error
import urllib.parse
import urllib.request
import zlib


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, *args, **kwargs):
        return None


def require(value, message):
    if not value:
        raise AssertionError(message)


def nested_ids(value):
    if isinstance(value, dict):
        for key, item in value.items():
            if key in ('id', 'detailId') and isinstance(item, int):
                yield item
            yield from nested_ids(item)
    elif isinstance(value, list):
        for item in value:
            yield from nested_ids(item)


def normalize(value, ids, device):
    if isinstance(value, dict):
        return {k: normalize(v, ids, device) for k, v in value.items() if k not in ('addTime', 'updateTime')}
    if isinstance(value, list):
        return [normalize(v, ids, device) for v in value]
    if isinstance(value, str):
        value = value.replace(device, '<device>')
        if value.startswith('/logan/downing?name='):
            return '/logan/downing?name=<object>'
        if value.startswith('/logan/web/download.json?tasks='):
            return '/logan/web/download.json?tasks=<web-task>'
        for number, token in ids.items():
            if value == str(number):
                return token
        # Native UUID-bearing object filenames differ for each independent upload.
        if value.endswith('.log') and '_<device>_' in value:
            return '<native-object>'
        return value
    return value




def semantic_headers(headers):
    content_type = headers.get('Content-Type', '')
    parts = [part.strip().lower() for part in content_type.split(';')]
    mime = parts[0]
    charset = next((p.split('=', 1)[1].strip('"').replace('-', '') for p in parts[1:] if p.startswith('charset=')), None)
    if mime == 'application/json' and charset is None:
        charset = 'utf8'
    result = {'contentType': {'mime': mime, 'charset': charset}}
    for key in ('Access-Control-Allow-Origin', 'Access-Control-Allow-Credentials', 'Access-Control-Max-Age'):
        result[key] = headers.get(key, '').strip()
    for key in ('Vary', 'Access-Control-Allow-Methods', 'Access-Control-Allow-Headers'):
        result[key] = sorted(token.strip().lower() for token in headers.get(key, '').split(',') if token.strip())
    return result


def record_flow_failure(responses, failures, route, flow, error):
    # A successful envelope is provisional until its fixture-specific assertions pass.
    responses.pop(route, None)
    failures.append({'flow': flow, 'route': route, 'errorType': type(error).__name__,
                     'reason': str(error) if isinstance(error, AssertionError) else 'HTTP/fixture processing failed'})

def require_single_detail(row, detail_id, expected_content, label):
    require(isinstance(row, dict) and row.get('id') == detail_id,
            label + ': returned detail ID must match requested fixture')
    require(row.get('content') == expected_content,
            label + ': returned detail content must match uploaded fixture')


def await_published_index(fetch, expected_count, timeout=15, pause=.25, clock=time.monotonic, sleep=time.sleep):
    """Empty/partial asynchronous indexes never count as positive coverage."""
    deadline = clock() + timeout
    attempts = 0
    first_empty = None
    while True:
        value = fetch()
        attempts += 1
        if value == [] and first_empty is None:
            first_empty = {'status': 200, 'body': {'code': 200, 'data': []}}
        if len(list(nested_ids(value))) == expected_count:
            return value, {'attempts': attempts, 'firstEmptyResponse': first_empty,
                           'comparison': 'steady-state published fixture only'}
        require(clock() < deadline, 'web-index: fixture publication deadline exceeded')
        sleep(min(pause, max(0, deadline - clock())))

def decode_native_members(payload):
    """Independent zlib oracle: complete gzip members separated by <=16 CBC pad bytes."""
    output = bytearray()
    remaining = payload
    while remaining:
        require(remaining.startswith(b'\x1f\x8b'), 'native gzip member header required')
        decoder = zlib.decompressobj(16 + zlib.MAX_WBITS)
        try:
            output.extend(decoder.decompress(remaining))
            output.extend(decoder.flush())
        except zlib.error as error:
            raise AssertionError('native gzip member invalid') from error
        require(decoder.eof, 'native gzip member truncated')
        remaining = decoder.unused_data
        if not remaining:
            break
        if remaining.startswith(b'\x1f\x8b'):
            continue
        # Padding is accepted only at a verified gzip boundary, never by searching.
        pad = remaining[0]
        if pad == 0:
            size = len(remaining) - len(remaining.lstrip(b'\x00'))
            require(1 <= size <= 16, 'native zero padding exceeds CBC block')
        else:
            size = pad
            require(1 <= size <= 16 and remaining[:size] == bytes([pad]) * size,
                    'native PKCS padding invalid')
        remaining = remaining[size:]
    return bytes(output)


def run(host, mode, device, fixture):
    root = 'http://' + host + ':8080/logan-web'
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}), NoRedirect())
    responses = {}
    observations = {}
    current_route = ['startup']
    run.responses = responses

    def request(path, method='GET', body=None, headers=None, absolute=False):
        url = path if absolute else root + path
        request_headers = dict(headers or {})
        if not absolute:
            request_headers.setdefault('Origin', 'http://localhost:3000')
        req = urllib.request.Request(url, data=body, headers=request_headers, method=method)
        try:
            r = opener.open(req, timeout=20)
        except urllib.error.HTTPError as error:
            r = error
        return r.status, r.headers, r.read()

    def call(route, path, method='GET', body=None, headers=None, allow_empty=False, save=True):
        current_route[0] = route
        status, hs, raw = request(path, method, body, headers)
        require(status == 200, route + ': expected HTTP 200, got ' + str(status))
        require('application/json' in hs.get('Content-Type', '').lower(), route + ': JSON required')
        value = json.loads(raw)
        require(value.get('code') == 200, route + ': business success required')
        require(value.get('data') is not None and (allow_empty or value.get('data') != []), route + ': positive fixture data required')
        call.last_response = {'status': status, 'headers': semantic_headers(hs), 'body': value}
        if save:
            responses[route] = call.last_response
        return value['data']

    deadline = time.monotonic() + 85
    while True:
        try:
            status, _, raw = request('/logan/meta/logtypes.json')
            if status == 200 and json.loads(raw).get('code') == 200:
                break
        except Exception:
            pass
        require(time.monotonic() < deadline, 'application startup deadline exceeded')
        time.sleep(.5)
    if mode == 'go':
        status, _, _ = request('http://' + host + ':8080/readyz', absolute=True)
        require(status == 503, 'readiness guard must remain closed')
    failures = []
    nt, wt, nd, wd = 0, 0, [], []
    try:
        native = base64.b64decode(fixture['nativeBase64'])
        upload_url = call('native-upload', '/logan/upload.json', 'POST', native,
                          {'Content-Type': 'application/octet-stream', 'appId': 'e2e20', 'deviceId': device,
                           'platform': '1', 'fileDate': '2026-10-15', 'unionId': 'e2e20',
                           'buildVersion': '1', 'appVersion': '1'})
        require(upload_url.startswith('/logan/downing?name='), 'native upload download address required')
        rows = call('native-search', '/logan/task/search.json?' + urllib.parse.urlencode(
            {'deviceId': device, 'beginTime': 1, 'endTime': 4102444800000, 'platform': 1}))
        require(len(rows) == 1 and rows[0]['deviceId'] == device, 'one uploaded native task required')
        nt = rows[0]['taskId']
        latest = call('native-latest', '/logan/latest.json')
        selected = [r for r in latest if r.get('deviceId') == device]
        require(len(selected) == 1, 'latest must contain uploaded native task')
        responses['native-latest']['body']['data'] = selected
        call('native-types', '/logan/meta/logtypes.json')
        pages = call('native-brief', '/logan/task/%s/brief.json?logTypes=&keyword=' % nt)
        nd = list(nested_ids(pages))
        observations['nativePublication'] = {'expectedRecords': len(fixture['nativeContents']), 'observedRecords': len(nd)}
        if mode == 'java' and len(nd) == 1 and len(fixture['nativeContents']) > 1:
            partial = call('native-details', '/logan/task/query/details.json?detailIds=' + str(nd[0]), save=False)
            observations['nativePublication']['firstRecordMatchesOracle'] = (len(partial) == 1 and partial[0]['content'] == fixture['nativeContents'][0])
            current_route[0] = 'native-brief'
        require(len(nd) == len(fixture['nativeContents']), 'native parse must publish every uploaded record')
        call('native-info', '/logan/task/%s/info.json' % nt)
        details = call('native-details', '/logan/task/query/details.json?detailIds=' + ','.join(map(str, nd)))
        require(sorted(r['content'] for r in details) == sorted(fixture['nativeContents']), 'native decrypted detail payload mismatch')
        row = call('native-detail', '/logan/task/%s/detail.json' % nd[0])
        expected_content = next(r['content'] for r in details if r['id'] == nd[0])
        require_single_detail(row, nd[0], expected_content, 'native-detail')
        download_url = call('native-download-url', '/logan/task/%s/download.json' % nt)
        require(download_url == upload_url, 'native download address must refer to uploaded object')
        current_route[0] = 'native-download'
        status, hs, _ = request(download_url)
        require(status == 302, 'native object download must redirect')
        location = hs.get('Location', '')
        parsed = urllib.parse.urlsplit(location)
        query = urllib.parse.parse_qs(parsed.query)
        require(parsed.scheme in ('http', 'https') and parsed.netloc and query, 'actual SDK signed URL required')
        require(any(k.lower() in ('signature', 'x-oss-signature') for k in query), 'SDK signature query required')
        status2, _, compressed = request(location, absolute=True)
        require(status2 == 200, 'signed GET must retrieve uploaded object')
        require(compressed == base64.b64decode(fixture.get('nativeJavaDownloadBase64', fixture['nativeDownloadBase64']) if mode == 'java' else fixture['nativeDownloadBase64']), 'signed GET exact decrypted frame bytes mismatch')
        plaintext = decode_native_members(compressed)
        require(plaintext == base64.b64decode(fixture['nativePlainBase64']), 'signed GET gzip payload mismatch')
        observations['nativeDownloadBytes'] = {'sha256': hashlib.sha256(compressed).hexdigest(), 'exactIndependentCBCOracle': True, 'bytes': len(compressed)}
        responses['native-download'] = {'status': status, 'headers': semantic_headers(hs), 'signedGETStatus': status2,
                                        'plainSha256': hashlib.sha256(plaintext).hexdigest(),
                                        'signatureQueryPresent': True}
    except Exception as error:
        record_flow_failure(responses, failures, current_route[0], 'native', error)
    try:
        web = dict(fixture['webBody'], deviceId=device)
        call('web-upload', '/logan/web/upload.json', 'POST', json.dumps(web).encode(), {'Content-Type': 'application/json'})
        rows = call('web-search', '/logan/web/search.json?' + urllib.parse.urlencode(
            {'deviceId': device, 'beginTime': 1, 'endTime': 4102444800000}))
        require(len(rows) == 1, 'one uploaded web task group required')
        wt = rows[0]['taskId']
        latest = call('web-latest', '/logan/web/latest.json')
        selected = [r for r in latest if r.get('deviceId') == device]
        require(len(selected) == 1, 'latest must contain uploaded web task')
        responses['web-latest']['body']['data'] = selected
        index_path = '/logan/web/detailIndex.json?tasks=%s&logTypes=&keyword=&beginTime=00:00&endTime=23:59' % wt
        pages, observations['webIndexPublication'] = await_published_index(
            lambda: call('web-index', index_path, allow_empty=True, save=False), len(fixture['webContents']))
        responses['web-index'] = call.last_response
        wd = list(nested_ids(pages))
        require(len(wd) == len(fixture['webContents']), 'web parse must publish every uploaded record')
        call('web-task', '/logan/web/taskDetail.json?tasks=%s' % wt)
        details = call('web-details', '/logan/web/details.json?detailIds=' + ','.join(map(str, wd)))
        require([r['content'] for r in details] == fixture['webContents'], 'web decoded detail payload mismatch')
        row = call('web-detail', '/logan/web/logDetail.json?detailId=%s' % wd[0])
        expected_content = next(r['content'] for r in details if r['id'] == wd[0])
        require_single_detail(row, wd[0], expected_content, 'web-detail')
        call('web-exception', '/logan/web/exception.json')
        url = call('web-download-url', '/logan/web/getDownLoadUrl.json?tasks=%s' % wt)
        require(url == '/logan/web/download.json?tasks=%s' % wt, 'web download literal must match task')
        current_route[0] = 'web-download'
        status, hs, raw = request(url)
        require(status == 201 and 'application/octet-stream' in hs.get('Content-Type', ''), 'web file HTTP201 required')
        require(json.loads(raw) == fixture['webRawLogs'], 'web downloaded raw logs mismatch')
        responses['web-download'] = {'status': status, 'headers': semantic_headers(hs), 'sha256': hashlib.sha256(raw).hexdigest(), 'rawLogs': json.loads(raw)}
    except Exception as error:
        record_flow_failure(responses, failures, current_route[0], 'web', error)
    ids = {nt: '<native-task>', wt: '<web-task>'}
    # IDs are table-local; normalize by field and route, never globally by number.
    for route, response in responses.items():
        native_route = route.startswith('native')
        task = nt if native_route else wt
        detail_ids = nd if native_route else wd
        def clean(v, field=None):
            if isinstance(v, dict):
                return {k: clean(x, k) for k, x in v.items() if k not in ('addTime', 'updateTime')}
            if isinstance(v, list):
                return [clean(x, field) for x in v]
            if field == 'taskId' and v == task:
                return '<task>'
            if field in ('id', 'detailId') and v in detail_ids:
                return '<detail-%s>' % detail_ids.index(v)
            if field == 'tasks' and str(v) == str(task):
                return '<task>'
            return normalize(v, {}, device)
        responses[route] = clean(response)
    return {'status': 'failed' if failures else 'passed', 'flowFailures': failures,
            'responses': responses, 'observations': observations, 'coveredRoutes': sorted(responses), 'namespace': device,
            'objectStorage': 'SDK HTTP PUT/GET and signed query presence; local fixture does not verify signature cryptography'}


def run_historical(host, mode, cases):
    root = 'http://' + host + ':8080/logan-web'
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
    def get(path):
        with opener.open(root + path, timeout=20) as response:
            value = json.loads(response.read())
        require(value.get('code') == 200, 'historical business success required')
        return value.get('data')
    output = []
    for case in cases:
        task = case['javaTaskId'] if mode == 'java' else case['pendingTaskId']
        pages = get('/logan/task/%s/brief.json?logTypes=&keyword=' % task)
        ids = list(nested_ids(pages))
        expected = case['contents'][:1] if mode == 'java' else case['contents']
        require(len(ids) == len(expected), 'historical ' + mode + ' exact record count required')
        details = get('/logan/task/query/details.json?detailIds=' + ','.join(map(str, ids)))
        require(sorted(row['content'] for row in details) == sorted(expected), 'historical exact content required')
        output.append({'gap': case['gap'], 'taskId': task, 'object': case['object'],
                       'records': len(ids), 'contents': expected, 'status': 'passed'})
    return output


if __name__ == '__main__':
    try:
        fixture = json.loads(open(sys.argv[4]).read())
        result = run(sys.argv[1], sys.argv[2], sys.argv[3], fixture)
        if fixture.get('historicalObjects'):
            result['historicalObjects'] = run_historical(sys.argv[1], sys.argv[2], fixture['historicalObjects'])
        print(json.dumps(result, ensure_ascii=False))
    except Exception as error:
        # Never print a request URL or signed query/authorization material on failure.
        print(json.dumps({'status': 'failed', 'errorType': type(error).__name__,
                          'reason': str(error) if isinstance(error, AssertionError) else 'HTTP/fixture processing failed',
                          'coveredRoutesBeforeFailure': sorted(getattr(run, 'responses', {}))}))
        raise SystemExit(1)
