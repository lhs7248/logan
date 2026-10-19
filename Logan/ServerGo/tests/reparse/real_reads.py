"""Sequential original-Java/production-Go reads for isolated repair evidence."""
import base64
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import subprocess
import sys
import time
from urllib.parse import quote, urlencode, urlsplit

HERE = Path(__file__).resolve().parent
spec = importlib.util.spec_from_file_location('crossruntime_http', HERE.parent / 'crossruntime/http_roundtrip.py')
http = importlib.util.module_from_spec(spec)
spec.loader.exec_module(http)


def require(value, reason):
    if not value:
        raise AssertionError(reason)


def canonical_rows(rows):
    require(bool(rows), 'expected published rows must not be empty')
    keys = ('id', 'taskId', 'content', 'logType', 'logTime')
    aliases = {'task_id': 'taskId', 'log_type': 'logType', 'log_time': 'logTime'}
    rows = [{aliases.get(k, k): v for k, v in row.items()} for row in rows]
    result = []
    for row in rows:
        require(isinstance(row, dict) and all(k in row for k in keys), 'partial detail record')
        result.append({k: row[k] for k in keys})
    require(len({r['id'] for r in result}) == len(result), 'duplicate physical detail IDs')
    return sorted(result, key=lambda r: (r['logTime'], r['id']))


def cases_for(task_id, rows, object_name, device_id, deleted_ids=()):
    prefix = '/logan-web/logan'
    ids = ','.join(str(r['id']) for r in rows)
    cases = [('info', '/task/%s/info.json' % task_id), ('latest', '/latest.json'),
             ('search', '/task/search.json?' + urlencode({'deviceId': device_id, 'beginTime': 1, 'endTime': 4102444800000, 'platform': 1})),
             ('types', '/meta/logtypes.json'), ('brief', '/task/%s/brief.json' % task_id),
             ('details', '/task/query/details.json?' + urlencode({'detailIds': ids})),
             ('downloadurl', '/task/%s/download.json' % task_id),
             ('signed-download', '/downing?name=' + quote(object_name, safe=''))]
    cases += [('detail-%s' % row['id'], '/task/%s/detail.json' % row['id']) for row in rows]
    require(not ({row['id'] for row in rows} & set(deleted_ids)), 'deleted IDs overlap preserved physical rows')
    cases += [('deleted-%s' % detail_id, '/task/%s/detail.json' % detail_id) for detail_id in deleted_ids]
    return [{'name': name, 'path': prefix + path} for name, path in cases]


def validate_capture(capture, task_id, expected_rows, object_name, expected_object_bytes=None):
    expected = canonical_rows(expected_rows)
    responses = capture['responses']
    for name, response in responses.items():
        if name == 'signed-download':
            require(response['status'] == 302, 'download redirect status')
            require(urlsplit(response['location']).path in ('/logan-logs/' + object_name, '/fixture/logan-logs/' + object_name), 'wrong signed object path')
            continue
        require(response['status'] == 200 and response['mime'] == 'application/json', 'JSON HTTP status/MIME: ' + name)
        require(response['body'].get('code') == 200, 'business failure: ' + name)
        if name.startswith('deleted-'):
            require('data' in response['body'] and response['body']['data'] is None, 'deleted physical detail remains readable')
    data = lambda name: responses[name]['body']['data']
    require(data('info')['taskId'] == task_id and data('info')['status'] == 1, 'wrong physical task or status')
    for name in ('latest', 'search'):
        require(any(r['taskId'] == task_id for r in data(name)), 'task absent: ' + name)
    require(bool(data('types')), 'empty log types')
    require(canonical_rows(data('details')) == expected, 'published detail rows differ')
    for row in expected:
        require(canonical_rows([data('detail-%s' % row['id'])]) == [row], 'individual detail differs')
    indices = [row for group in data('brief') for row in group]
    require(indices and len(indices) == len(expected), 'empty or partial index')
    require(sorted((r['id'], r['logType'], r['logTime']) for r in indices) ==
            sorted((r['id'], r['logType'], r['logTime']) for r in expected), 'index IDs/type/time differ')
    require(data('downloadurl') == '/logan/downing?name=' + object_name, 'wrong download URL')
    require(capture['object']['status'] == 200, 'signed object unavailable')
    if expected_object_bytes is not None:
        require(base64.b64decode(capture['object']['bytes']) == expected_object_bytes, 'download bytes differ')


def compare_captures(java, go):
    # Signed query values contain legitimate SDK/runtime expiry and signature differences.
    def normalized(capture):
        result = json.loads(json.dumps(capture))
        result.pop('readinessStatus', None)
        redirect = result['responses']['signed-download']
        location = urlsplit(redirect.pop('location'))
        path = location.path
        if path.startswith('/fixture/logan-logs/'):
            path = path[len('/fixture'):]
        require(path.startswith('/logan-logs/'), 'unexpected signed addressing prefix')
        redirect['objectPath'] = path
        return result
    require(normalized(java) == normalized(go), 'full HTTP JSON/header/body compatibility differs')


def compare_business_snapshots(before, after):
    """Assert immutable business rows and return a non-sensitive evidence digest."""
    serialized = json.dumps(before, sort_keys=True, separators=(',', ':'), ensure_ascii=False)
    require(before == after, 'HTTP runtime modified task/detail business rows')
    return {'equal': True, 'sha256': hashlib.sha256(serialized.encode()).hexdigest()}


def reader_database_config(v):
    """Build explicit disposable reader transport settings; TLS never falls back."""
    host = getattr(v, 'mysql_read_host', v.mysql)
    user = getattr(v, 'mysql_read_user', 'root')
    password = getattr(v, 'mysql_read_password', v.password)
    jdbc = 'jdbc:mysql://' + host + ':3306/' + v.db + '?'
    dsn = user + ':' + password + '@tcp(' + host + ':3306)/' + v.db + '?parseTime=true&charset=utf8mb4'
    result = {'user': user, 'password': password, 'dsn': dsn, 'java_mounts': [], 'go_mounts': [], 'go_env': []}
    tls = getattr(v, 'read_mysql_tls', None)
    if tls is None:
        result['jdbc'] = jdbc + 'useSSL=false&allowPublicKeyRetrieval=true&serverTimezone=Asia/Shanghai'
        return result
    require(isinstance(tls, dict) and all(tls.get(k) for k in ('ca', 'truststore', 'password')), 'incomplete reader TLS configuration')
    for field in ('ca', 'truststore'):
        path = Path(tls[field])
        require(path.is_absolute() and path.is_file() and ',' not in str(path), 'reader TLS trust file must be an existing absolute bind-safe path')
    require(tls['password'] == 'fixture-only', 'reader TLS truststore must use disposable fixture password')
    result['jdbc'] = (jdbc + 'sslMode=VERIFY_IDENTITY&serverTimezone=Asia/Shanghai'
                      '&trustCertificateKeyStoreUrl=file:/fixture/mysql-truststore.p12'
                      '&trustCertificateKeyStoreType=PKCS12&trustCertificateKeyStorePassword=fixture-only')
    result['dsn'] += '&tls=true'
    result['java_mounts'] = ['--mount', 'type=bind,src=' + str(tls['truststore']) + ',dst=/fixture/mysql-truststore.p12,readonly']
    result['go_mounts'] = ['--mount', 'type=bind,src=' + str(tls['ca']) + ',dst=/fixture/mysql-ca.pem,readonly']
    result['go_env'] = ['SSL_CERT_FILE=/fixture/mysql-ca.pem']
    return result


def observe_reader_tls(v, runtime, container, events):
    hook = getattr(v, 'verify_reader_tls', None)
    if hook:
        evidence = hook(runtime, container)
        events.append({'runtime': runtime, 'event': 'mysql-tls-verified', 'evidence': evidence})


def check_reads(v, phase, task_id, expected_rows, object_name, expected_object_bytes=None, device_id=None):
    expected_rows = canonical_rows(expected_rows)
    if expected_object_bytes is None:
        expected_object_bytes = getattr(v, 'read_fixture', {}).get('object_bytes')
    require(expected_object_bytes is not None, 'exact fixture object bytes required')
    work = v.work / ('reads-' + phase)
    work.mkdir(mode=0o700)
    war = work / 'logan-web.war'
    key = work / 'fixture-rsa.pem'
    _, labels_json = v.docker('inspect', '--format', '{{json .Config.Labels}}', v.mysql)
    labels = json.loads(labels_json)
    label = next(k + '=' + val for k, val in labels.items() if val == v.run_id)
    uid = str(os.getuid())
    common = ['--label', label, '--network', v.network, '--read-only', '--cap-drop', 'ALL',
              '--security-opt', 'no-new-privileges:true', '--user', uid + ':' + uid, '--cpus', '1', '--pids-limit', '256']
    events = []
    results = {}
    builder = HERE.parent / 'crossruntime/build_java_war.py'
    env = dict(os.environ)
    database = reader_database_config(v)
    env.update(CROSSRUNTIME_DISPOSABLE='YES', CROSSRUNTIME_JDBC_URL=database['jdbc'],
               CROSSRUNTIME_DB_USER=database['user'], CROSSRUNTIME_DB_PASSWORD=database['password'],
               CROSSRUNTIME_OSS_EMULATOR_ENDPOINT='http://' + v.oss + ':8080')
    active = None
    try:
        subprocess.run([sys.executable, str(builder), '--output', str(war)], env=env, check=True, capture_output=True, timeout=60)
        subprocess.run(['openssl', 'genpkey', '-algorithm', 'RSA', '-pkeyopt', 'rsa_keygen_bits:2048', '-out', str(key)], check=True, capture_output=True, timeout=30)
        key.chmod(0o600)
        fixture = getattr(v, 'read_fixture', {})
        device_id = device_id or fixture.get('deviceId', 'ack-compat')
        cases = cases_for(task_id, expected_rows, object_name, device_id, fixture.get('deletedDetailIds', ()))
        snapshot = getattr(v, 'read_business_snapshot', None)
        for runtime in ('java', 'go'):
            before = snapshot() if snapshot else None
            name = v.reserve('container', v.prefix + '-' + phase + '-' + runtime)
            options = ['--tmpfs', '/tmp:rw,uid=' + uid + ',gid=' + uid + ',mode=0700', '--memory', '768m']
            if runtime == 'java':
                options += database['java_mounts']
                for directory in ('webapps', 'work', 'temp', 'logs', 'conf/Catalina/localhost'):
                    options += ['--tmpfs', '/usr/local/tomcat/' + directory + ':rw,uid=' + uid + ',gid=' + uid]
                options += ['--tmpfs', '/data/applogs/logan-web:rw,uid=' + uid + ',gid=' + uid,
                            '--mount', 'type=bind,src=' + str(war) + ',dst=/fixture/logan-web.war,readonly',
                            '-e', 'TZ=Asia/Shanghai', '-e', 'JAVA_TOOL_OPTIONS=--add-opens=java.base/java.util=ALL-UNNAMED -Duser.timezone=Asia/Shanghai',
                            http.TOMCAT_IMAGE, 'sh', '-c', 'cp /fixture/logan-web.war /usr/local/tomcat/webapps/logan-web.war && exec catalina.sh run']
            else:
                values = ['LOGAN_ENV=production', 'LOGAN_DB_ENABLED=true', 'LOGAN_MYSQL_DSN=' + database['dsn'],
                          'LOGAN_NATIVE_ENABLED=true', 'LOGAN_NATIVE_QUERY_ENABLED=true', 'LOGAN_WEB_ENABLED=true', 'LOGAN_NATIVE_AES_KEY=fixture-key-1234', 'LOGAN_NATIVE_AES_IV=fixture-iv--1234',
                          'LOGAN_NATIVE_TEMP_DIR=/tmp', 'LOGAN_WEB_RSA_PRIVATE_KEY_FILE=/fixture/fixture-rsa.pem', 'LOGAN_OSS_ENDPOINT=http://' + v.oss + ':8080',
                          'LOGAN_OSS_REGION=fixture', 'LOGAN_OSS_BUCKET=fixture', 'LOGAN_OSS_PREFIX=logan-logs/', 'LOGAN_OSS_PATH_STYLE=true',
                          'LOGAN_OSS_ACCESS_KEY_ID=fixture-only', 'LOGAN_OSS_ACCESS_KEY_SECRET=fixture-only', 'LOGAN_MAINTENANCE_ENABLED=false', 'GOMAXPROCS=1']
                values += database['go_env']
                options += database['go_mounts']
                envfile = work / 'go.env'; envfile.write_text('\n'.join(values) + '\n'); envfile.chmod(0o600)
                options += ['--mount', 'type=bind,src=' + str(key) + ',dst=/fixture/fixture-rsa.pem,readonly', '--env-file', str(envfile), v.args.go_image]
            v.docker('run', '-d', '--name', name, *common, *options)
            active = name
            events.append({'runtime': runtime, 'event': 'started', 'unix_time': time.time()})
            probe = v.reserve('container', name + '-probe')
            v.docker('run', '-d', '--name', probe, *common, '--memory', '128m', http.PROBE_IMAGE, 'python', '-c', (HERE / 'read_probe.py').read_text(), json.dumps([name, runtime, cases, [v.oss, 'fixture.' + v.oss]]))
            _, code = v.docker('wait', probe, timeout=100)
            _, raw = v.docker('logs', probe)
            if code.strip() != '0':
                failure = v.args.out / (phase + '-' + runtime + '-failure.json')
                failure.write_text(json.dumps({'runtime': runtime, 'exitCode': code.strip(), 'probeOutput': raw}, indent=2))
                failure.chmod(0o600)
            require(code.strip() == '0', 'actual HTTP probe failed: ' + runtime)
            capture = json.loads(raw.strip())
            target = v.args.out / (phase + '-' + runtime + '.json'); target.write_text(json.dumps(capture, indent=2)); target.chmod(0o600)
            validate_capture(capture, task_id, expected_rows, object_name, expected_object_bytes)
            results[runtime] = capture
            observe_reader_tls(v, runtime, name, events)
            v.docker('stop', '--time', '15', name, timeout=35)
            _, raw = v.docker('inspect', '--format', '{{json .State}}', name)
            state = json.loads(raw)
            require(not state['Running'] and not state['OOMKilled'] and state['ExitCode'] in ([0, 143] if runtime == 'java' else [0]), 'backend did not stop cleanly')
            active = None
            if snapshot:
                equality = compare_business_snapshots(before, snapshot())
                events.append({'runtime': runtime, 'event': 'business-rows-unchanged', **equality})
            events.append({'runtime': runtime, 'event': 'stopped', 'exitCode': state['ExitCode'], 'unix_time': time.time()})
        compare_captures(results['java'], results['go'])
        evidence = {'status': 'passed', 'phase': phase, 'events': events, 'endpoints': len(cases), 'comparison': 'full JSON, HTTP status, semantic MIME, fixed CORS and exact signed object bytes',
                    'signedAddressing': {runtime: {'host': urlsplit(capture['responses']['signed-download']['location']).hostname, 'path': urlsplit(capture['responses']['signed-download']['location']).path} for runtime, capture in results.items()}}
        target = v.args.out / (phase + '-diff.json'); target.write_text(json.dumps(evidence, indent=2)); target.chmod(0o600)
        return evidence
    finally:
        ledger = v.args.out / (phase + '-events.json')
        ledger.write_text(json.dumps(events, indent=2)); ledger.chmod(0o600)
        if active:
            try:
                v.docker('stop', '--time', '15', active, timeout=35, accept=True)
            except Exception:
                pass  # The controller still owns final resource cleanup.
        for path in (war, key, work / 'go.env'):
            path.unlink(missing_ok=True)
