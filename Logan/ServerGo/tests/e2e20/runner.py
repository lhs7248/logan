"""Owned-container all-route rehearsal, using DataRehearsal's guarded Docker client."""
import importlib.util
import json
import os
import shutil
from pathlib import Path
import subprocess
import sys
import time
from types import SimpleNamespace

HERE = Path(__file__).resolve().parent
ROOT = HERE.parents[1]


def load(name, path):
    spec = importlib.util.spec_from_file_location(name, path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def require(value, message):
    if not value:
        raise AssertionError(message)



def compare_flows(results, routes):
    output = {}
    for flow in ('native', 'web'):
        expected = {route for route in routes if route.startswith(flow + '-')}
        positive = {mode: set(capture['coveredRoutes']) & expected for mode, capture in results.items()}
        shared = positive['java'] & positive['go']
        mismatches = sorted(route for route in shared
                            if results['java']['responses'][route] != results['go']['responses'][route])
        failures = {mode: [failure for failure in capture.get('flowFailures', []) if failure.get('flow') == flow]
                    for mode, capture in results.items()}
        passed = all(positive[mode] == expected and not failures[mode] for mode in ('java', 'go')) and not mismatches
        output[flow] = {'status': 'passed' if passed else 'failed', 'expectedRoutes': sorted(expected),
                        'positiveRoutes': {mode: sorted(value) for mode, value in positive.items()},
                        'missingPositiveRoutes': {mode: sorted(expected - value) for mode, value in positive.items()},
                        'sharedPositiveRoutesCompared': sorted(shared), 'mismatches': mismatches,
                        'failures': failures,
                        'comparison': 'complete normalized JSON, HTTP status, semantic MIME/charset and fixed CORS headers; payload assertions must pass'}
    return output

def acceptance(results, routes, fixture_mode):
    """Keep strict Java capture failed; accept only the documented SDK native loss."""
    flows = compare_flows(results, routes)
    runtimes = {mode: {'status': 'passed' if set(c['coveredRoutes']) == routes and not c.get('flowFailures') else 'failed',
                       'positiveRoutes': len(c['coveredRoutes']), 'missingRoutes': sorted(routes - set(c['coveredRoutes']))}
                for mode, c in results.items()}
    differences = []
    java, go = results['java'], results['go']
    shared_mismatches = [r for r in set(java['responses']) & set(go['responses']) if java['responses'][r] != go['responses'][r]]
    strict = all(r['status'] == 'passed' for r in runtimes.values()) and not shared_mismatches
    accepted = strict
    if fixture_mode == 'protocol' and strict:
        raw = {mode: c.get('observations', {}).get('nativeDownloadBytes') for mode, c in results.items()}
        if all(raw.values()) and raw['java']['sha256'] != raw['go']['sha256']:
            differences.append({'id': 'native-cbc-download-padding-preserved', 'status': 'expected-difference',
                                'java': raw['java'], 'go': raw['go'],
                                'semantics': 'identical full plaintext; each runtime exact bytes independently verified'})
    if fixture_mode == 'sdk':
        initial_native = {'native-upload', 'native-search', 'native-latest', 'native-types'}
        expected_java = {r for r in routes if r.startswith('web-')} | initial_native
        declared_failure = [{'flow': 'native', 'route': 'native-brief', 'errorType': 'AssertionError',
                             'reason': 'native parse must publish every uploaded record'}]
        publication = java.get('observations', {}).get('nativePublication', {})
        known_loss = (publication.get('expectedRecords', 0) > 1 and publication.get('observedRecords') == 1
                      and publication.get('firstRecordMatchesOracle') is True)
        known = (known_loss and set(java['coveredRoutes']) == expected_java and java.get('flowFailures') == declared_failure
                 and runtimes['go']['status'] == 'passed' and not shared_mismatches)
        accepted = known
        if known:
            differences.append({'id': 'original-java-native-sdk-member-loss', 'status': 'expected-difference',
                                'java': 'strict native payload assertion failed; original CLogan later records absent',
                                'go': 'all uploaded records recovered; exact decrypted download bytes verified',
                                'missingJavaPositiveRoutes': sorted(routes - expected_java)})
    return {'status': 'passed' if accepted else 'failed', 'strictParityStatus': 'passed' if strict else 'failed',
            'runtimes': runtimes, 'declaredDifferences': differences, 'unexpectedResponseMismatches': sorted(shared_mismatches),
            'flows': flows}


def transport_evidence(results, assessment, logs):
    """Require parse GETs plus downloads that passed their exact payload oracle."""
    require(assessment['status'] == 'passed', 'transport counts cannot accept failed runtime assessment')
    require(set(results) == {'java', 'go'}, 'both runtime captures required for transport counts')
    downloads = sum('native-download' in capture['coveredRoutes'] for capture in results.values())
    expected = {'putMinimum': 2, 'getMinimum': 2 + downloads,
                'nativeLazyParseGETs': 2, 'positiveNativeDownloadGETs': downloads}
    actual = {'puts': logs.count('OSS_FIXTURE_PUT'), 'gets': logs.count('OSS_FIXTURE_GET')}
    require(actual['puts'] >= expected['putMinimum'] and actual['gets'] >= expected['getMinimum'],
            'both uploads, native lazy parse GETs and every positive signed download must reach fixture')
    return {'expected': expected, 'actual': actual}


def seed_historical(v, fixtures, fixture_data, fixture, oss, common, work):
    cases = fixtures.historical_objects()
    for case in cases:
        case['object'] = v.prefix + '-history-' + case['gap'] + '.log'
        for field, suffix in (('javaTaskId', 'java-baseline'), ('pendingTaskId', 'go-pending')):
            device = v.prefix + '-history-' + case['gap'] + '-' + suffix
            sql = ("USE " + v.db + "; INSERT INTO logan_task(platform,device_id,log_file_name,log_date,add_time,status) "
                   "VALUES(1,'%s','%s',1792022400000,1792022400000,0); SELECT LAST_INSERT_ID();" % (device, case['object']))
            case[field] = int(v.sql(sql).splitlines()[-1])
    fixture_data['historicalObjects'] = cases
    fixture.write_text(json.dumps(fixture_data))
    uploader = v.reserve('container', v.prefix + '-history-put')
    code = """import json,base64,urllib.request,time
f=json.load(open('/fixture.json'))
for c in f['historicalObjects']:
    request=urllib.request.Request('http://HOST:8080/fixture/logan-logs/'+c['object'],data=base64.b64decode(c['objectBase64']),method='PUT')
    for attempt in range(40):
        try:
            urllib.request.urlopen(request,timeout=5).read()
            break
        except OSError:
            if attempt==39: raise
            time.sleep(.25)
""".replace('HOST', oss)
    v.docker('run', '-d', '--name', uploader, *common, '--memory', '128m',
             '--mount', 'type=bind,src=' + str(fixture) + ',dst=/fixture.json,readonly',
             'python:3.12-alpine', 'python', '-c', code)
    _, status = v.docker('wait', uploader, timeout=40)
    require(status.strip() == '0', 'historical pre-existing object PUT failed')
    return cases


def historical_snapshot(v, cases):
    ids = ','.join(str(c[field]) for c in cases for field in ('javaTaskId', 'pendingTaskId'))
    columns = {
        'logan_task': ('id', 'platform', 'amount', 'app_id', 'union_id', 'device_id', 'app_version',
                       'build_version', 'log_file_name', 'log_date', 'add_time', 'status', 'update_time'),
        'logan_log_detail': ('id', 'task_id', 'log_type', 'content', 'log_time', 'add_time', 'update_time'),
    }
    rows = {}
    for table, names in columns.items():
        fields = ','.join("'%s',`%s`" % (name, name) for name in names)
        owner = 'id' if table == 'logan_task' else 'task_id'
        # Hex transport preserves every UTF-8 character and SQL null without mysql batch escaping.
        raw = v.sql('USE ' + v.db + '; SELECT ' + owner + ',HEX(CAST(JSON_OBJECT(' + fields + ') AS CHAR CHARACTER SET utf8mb4)) '
                    'FROM ' + table + ' WHERE ' + owner + ' IN (' + ids + ') ORDER BY ' + owner + ',id;')
        for line in raw.splitlines():
            task_id, encoded = line.split('\t')
            entry = rows.setdefault(int(task_id), {'task': None, 'details': []})
            value = json.loads(bytes.fromhex(encoded).decode('utf-8'))
            if table == 'logan_task':
                entry['task'] = value
            else:
                entry['details'].append(value)
    return rows


def require_historical_unchanged(before, after):
    require(before == after, 'completed Java partial full task and ordered detail rows must remain unchanged')


class ScopedRehearsal:
    """Give repeated runs distinct evidence, resources and device namespaces."""
    def __init__(self, parent, scope):
        require(scope and len(scope) <= 12 and scope.replace('-', '').isalnum(), 'safe all20 scope required')
        self.parent = parent
        self.prefix = parent.prefix + '-' + scope
        self.args = SimpleNamespace(**vars(parent.args))
        self.args.out = parent.args.out / ('all20-' + scope)
        self.args.out.mkdir(mode=0o700)
        self.scope = scope

    def __getattr__(self, name):
        return getattr(self.parent, name)

    def private(self, name, content):
        return self.parent.private(self.scope + '-' + name, content)


def run_all_interfaces(v, scope=None):
    if scope is not None:
        require(scope and len(scope) <= 12 and scope.replace('-', '').isalnum(), 'safe all20 scope required')
    # Cover failures during key staging, image checks and WAR assembly as well as probes.
    work = v.args.work_dir / ('e2e20' if scope is None else 'e2e20-' + scope)
    try:
        return _run_all_interfaces(v, scope)
    finally:
        for secret in (work / 'logan-web.war', work / 'secrets/fixture-rsa.pem'):
            secret.unlink(missing_ok=True)


def _run_all_interfaces(v, scope=None):
    if scope is not None:
        v = ScopedRehearsal(v, scope)
    require(v.args.http_go_image, 'explicit production Go image required')
    require(v.args.namespace_prefix.replace('-', '').isalnum(), 'safe namespace prefix required')
    work = v.args.work_dir / ('e2e20' if scope is None else 'e2e20-' + scope)
    work.mkdir(mode=0o700)
    for source in ('probe.py', 'oss_fixture.py'):
        shutil.copyfile(HERE / source, work / source)
        (work / source).chmod(0o600)
    fixtures = load('e2e20_fixtures', HERE / 'fixtures.py')
    fixture = work / 'fixture.json'
    fixture_data = fixtures.make_fixture(v.args.sdk_fixtures) if v.args.fixture_mode == 'sdk' else fixtures.make_protocol_fixture()
    fixture.write_text(json.dumps(fixture_data)); fixture.chmod(0o600)
    uid = str(os.getuid())
    secrets = work / 'secrets'; secrets.mkdir(mode=0o700)
    key = secrets / 'fixture-rsa.pem'
    if v.args.fixture_mode == 'sdk':
        shutil.copyfile(v.args.sdk_fixtures / 'test-rsa-private.pem', key)
    else:
        subprocess.run(['openssl', 'genpkey', '-algorithm', 'RSA', '-pkeyopt', 'rsa_keygen_bits:2048', '-out', str(key)],
                       stdout=subprocess.PIPE, stderr=subprocess.PIPE, check=True, timeout=30)
    key.chmod(0o600)
    _, labels = v.docker('inspect', '--format', '{{json .Config.Labels}}', v.mysql)
    label = next(k + '=' + val for k, val in json.loads(labels).items() if val == v.run_id)
    common = ['--label', label, '--network', v.network, '--read-only', '--cap-drop', 'ALL',
              '--security-opt', 'no-new-privileges:true', '--user', uid + ':' + uid,
              '--cpus', '1', '--pids-limit', '256']
    tmp = ['--tmpfs', '/tmp:rw,nosuid,nodev,size=134217728,uid=' + uid + ',gid=' + uid + ',mode=0700']
    for image in ('python:3.12-alpine', 'tomcat:9-jre17-temurin', v.args.http_go_image):
        v.docker('image', 'inspect', '--format', '{{.Id}}', image)
    oss = v.reserve('container', v.prefix + ('-e20oss' if scope is None else '-o'))
    require(len('fixture.' + oss) < 64, 'fixture hostname length exceeded')
    v.docker('run', '-d', '--name', oss, *common, '--network-alias', 'fixture.' + oss, '--memory', '128m',
             '--mount', 'type=bind,src=' + str(work / 'oss_fixture.py') + ',dst=/fixture.py,readonly',
             'python:3.12-alpine', 'python', '-u', '/fixture.py')
    historical = seed_historical(v, fixtures, fixture_data, fixture, oss, common, work) if getattr(v.args, 'historical_objects', False) else []
    historical_java_state = None
    database = load('all20_database_transport', ROOT / 'tests/reparse/real_reads.py').reader_database_config(v)
    env = dict(v.env)
    env.update({'CROSSRUNTIME_DISPOSABLE': 'YES',
                'CROSSRUNTIME_JDBC_URL': database['jdbc'],
                'CROSSRUNTIME_DB_USER': database['user'], 'CROSSRUNTIME_DB_PASSWORD': database['password'],
                'CROSSRUNTIME_OSS_EMULATOR_ENDPOINT': 'http://' + oss + ':8080'})
    war = work / 'logan-web.war'
    subprocess.run([sys.executable, str(ROOT / 'tests/crossruntime/build_java_war.py'), '--output', str(war)],
                   env=env, check=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=60)
    results = {}
    try:
        for mode in ('java', 'go'):
            name = v.reserve('container', v.prefix + '-e20' + mode)
            if mode == 'java':
                options = [*tmp, *database['java_mounts'], '--memory', '768m']
                for path, size in (('/data/applogs/logan-web', 16777216), ('/usr/local/tomcat/conf/Catalina/localhost', 16777216),
                                   ('/usr/local/tomcat/webapps', 268435456), ('/usr/local/tomcat/work', 67108864),
                                   ('/usr/local/tomcat/temp', 67108864), ('/usr/local/tomcat/logs', 67108864)):
                    options += ['--tmpfs', path + ':rw,nosuid,nodev,size=' + str(size) + ',uid=' + uid + ',gid=' + uid]
                options += ['--mount', 'type=bind,src=' + str(war) + ',dst=/fixture/logan-web.war,readonly',
                            '-e', 'TZ=Asia/Shanghai', '-e', 'JAVA_TOOL_OPTIONS=--add-opens=java.base/java.util=ALL-UNNAMED -Duser.timezone=Asia/Shanghai',
                            'tomcat:9-jre17-temurin', 'sh', '-c',
                            'cp /fixture/logan-web.war /usr/local/tomcat/webapps/logan-web.war && exec catalina.sh run']
            else:
                values = ['LOGAN_ENV=production', 'LOGAN_DB_ENABLED=true',
                          'LOGAN_MYSQL_DSN=' + database['dsn'],
                          'LOGAN_NATIVE_ENABLED=true', 'LOGAN_NATIVE_QUERY_ENABLED=true', 'LOGAN_WEB_ENABLED=true',
                          'LOGAN_NATIVE_AES_KEY=' + fixture_data['nativeKey'], 'LOGAN_NATIVE_AES_IV=' + fixture_data['nativeIV'],
                          'LOGAN_NATIVE_TEMP_DIR=/tmp', 'LOGAN_WEB_RSA_PRIVATE_KEY_FILE=/secrets/fixture-rsa.pem',
                          'LOGAN_OSS_ENDPOINT=http://' + oss + ':8080', 'LOGAN_OSS_REGION=fixture',
                          'LOGAN_OSS_BUCKET=fixture', 'LOGAN_OSS_PREFIX=logan-logs/', 'LOGAN_OSS_PATH_STYLE=true',
                          'LOGAN_OSS_ACCESS_KEY_ID=fixture-only', 'LOGAN_OSS_ACCESS_KEY_SECRET=fixture-only',
                          'LOGAN_MAINTENANCE_ENABLED=false', 'GOMAXPROCS=1']
                values += database['go_env']
                if v.args.legacy_rsa_compat:
                    values.append('GODEBUG=rsa1024min=0')
                options = [*tmp, *database['go_mounts'], '--memory', '512m', '--mount', 'type=bind,src=' + str(secrets) + ',dst=/secrets,readonly',
                           '--env-file', str(v.private('e20-go.env', '\n'.join(values) + '\n')), v.args.http_go_image]
            v.docker('run', '-d', '--name', name, *common, *options)
            v.events.append({'event': 'all20-started', 'target': mode, 'unix_time': time.time()})
            probe = v.reserve('container', v.prefix + '-e20probe' + mode)
            device = v.args.namespace_prefix + '-' + v.run_id + '-' + (scope + '-' if scope else '') + mode
            v.docker('run', '-d', '--name', probe, *common, '--memory', '128m',
                     '--mount', 'type=bind,src=' + str(work / 'probe.py') + ',dst=/probe.py,readonly',
                     '--mount', 'type=bind,src=' + str(fixture) + ',dst=/fixture.json,readonly',
                     'python:3.12-alpine', 'python', '/probe.py', name, mode, device, '/fixture.json')
            _, status = v.docker('wait', probe, timeout=150)
            _, raw = v.docker('logs', probe)
            if status.strip() != '0':
                # Original application logs can contain signed URLs, therefore do not collect them.
                raise AssertionError('all20 ' + mode + ' probe failed: ' + raw.strip())
            capture = json.loads(raw.strip().splitlines()[-1])
            routes = {r['id'] for r in json.loads((ROOT / 'tests/contract/cases.json').read_text())['routes']}
            require(set(capture['coveredRoutes']) <= routes and len(routes) == 20, 'only declared manifest routes may count')
            if historical:
                snapshot = historical_snapshot(v, historical)
                if mode == 'java':
                    for case in historical:
                        require(snapshot[case['javaTaskId']]['task']['status'] == 1 and len(snapshot[case['javaTaskId']]['details']) == 1, 'original Java historical clone must be completed with one record')
                        require(snapshot[case['pendingTaskId']]['task']['status'] == 0 and not snapshot[case['pendingTaskId']]['details'], 'historical Go pending row must remain untouched by Java')
                    historical_java_state = snapshot
                else:
                    for case in historical:
                        require_historical_unchanged(historical_java_state[case['javaTaskId']], snapshot[case['javaTaskId']])
                        require(snapshot[case['pendingTaskId']]['task']['status'] == 1 and len(snapshot[case['pendingTaskId']]['details']) == len(case['contents']), 'same pending row must recover all historical records')
            results[mode] = capture
            target = v.args.out / ('all20-' + mode + '.json')
            target.write_text(json.dumps(capture, ensure_ascii=False, indent=2)); target.chmod(0o600)
            if getattr(v, 'read_mysql_tls', None) is not None:
                evidence = v.verify_reader_tls(mode, name)
                v.events.append({'event': 'all20-mysql-tls-verified', 'scope': scope, 'target': mode, 'evidence': evidence})
            v.docker('stop', '--time', '15', name, timeout=35)
            _, state = v.docker('inspect', '--format', '{{json .State}}', name)
            state = json.loads(state)
            require(not state['Running'] and not state['OOMKilled'] and state['ExitCode'] in (0, 143), 'previous all20 process must stop cleanly')
            v.events.append({'event': 'all20-stopped', 'target': mode, 'unix_time': time.time()})
        flow_comparison = {'fixtureMode': v.args.fixture_mode,
                           'sdkFixtureIngestionScope': 'host CLogan and Node WebSDK with emulated IndexedDB/intercepted XHR; no real browser/device/cloud certification',
                           'legacyRSACompatibilityOverride': v.args.legacy_rsa_compat,
                           'flows': compare_flows(results, routes),
                           'cloudGate': {'status': 'blocked', 'reason': 'real Aliyun credentials absent; local transport does not verify signatures'},
                           'browserGate': 'not-executed', 'nativeDeviceGate': 'not-executed',
                           'sdkFixtureIngestionAll20ComparisonPassed': False if v.args.fixture_mode != 'sdk' else all(flow['status'] == 'passed' for flow in compare_flows(results, routes).values())}
        flow_target = v.args.out / 'all20-flow-comparison.json'
        flow_target.write_text(json.dumps(flow_comparison, ensure_ascii=False, indent=2)); flow_target.chmod(0o600)
        if historical:
            report = {'status': 'passed', 'scope': 'isolated pre-existing OSS decoded objects and pending SQL tasks',
                      'strategy': 'distinct Java baseline clone and untouched Go pending row share the exact object; no completed row reset',
                      'cases': [{k: value for k, value in case.items() if k != 'objectBase64'} for case in historical],
                      'java': results['java']['historicalObjects'], 'go': results['go']['historicalObjects'],
                      'javaCompletedRowsBeforeGo': {str(c['javaTaskId']): historical_java_state[c['javaTaskId']] for c in historical},
                      'javaCompletedRowsAfterGo': {str(c['javaTaskId']): snapshot[c['javaTaskId']] for c in historical},
                      'completedPartialRows': 'full task columns and ordered full detail rows preserved byte-exact via SQL JSON hex transport; no automatic historical backfill'}
            path = v.args.out / 'historical-object-acceptance.json'
            path.write_text(json.dumps(report, indent=2)); path.chmod(0o600)
        assessment = acceptance(results, routes, v.args.fixture_mode)
        difference_target = v.args.out / 'all20-runtime-acceptance.json'
        difference_target.write_text(json.dumps(assessment, ensure_ascii=False, indent=2)); difference_target.chmod(0o600)
        require(assessment['status'] == 'passed', 'runtime acceptance failed; see strict captures and all20-runtime-acceptance.json')
        _, oss_logs = v.docker('logs', oss)
        transport = transport_evidence(results, assessment, oss_logs)
        evidence = {'status': 'passed', 'fixtureMode': v.args.fixture_mode, 'sdkFixtureIngestion': v.args.fixture_mode == 'sdk', 'sdkScope': 'host-compiled shared CLogan and Node WebSDK with emulated IndexedDB/intercepted XMLHttpRequest; server HTTP ingestion only; no real browser/device/cloud certification', 'legacyRSACompatibilityOverride': v.args.legacy_rsa_compat, 'routes': 20, 'javaAll20Passed': assessment['runtimes']['java']['status'] == 'passed', 'goAll20Passed': assessment['runtimes']['go']['status'] == 'passed', 'strictJavaGoParityPassed': assessment['strictParityStatus'] == 'passed', 'runtimeAcceptance': assessment, 'transportEvidence': transport, 'targets': ['original Java/Tomcat9', 'production Go'],
                    'comparison': 'HTTP status, normalized MIME/JSON charset and fixed CORS headers, and complete JSON after task/detail IDs, upload times and object keys normalization; download payloads verified',
                    'storage': 'local OSS-shaped transport; actual SDK PUT/GET and signed GET exercised; cryptographic signature verification absent',
                    'cloudGate': {'status': 'blocked', 'reason': 'real Aliyun credentials not supplied; no real cloud claim'},
                    'readiness': 'Go remained 503; no public gateway', 'namespaces': 'separate per-target device IDs; sequential writers', 'sdkEvidence': fixture_data['sdkEvidence']}
        target = v.args.out / 'all20-summary.json'
        target.write_text(json.dumps(evidence, indent=2)); target.chmod(0o600)
        return evidence
    finally:
        war.unlink(missing_ok=True)
        key.unlink(missing_ok=True)
