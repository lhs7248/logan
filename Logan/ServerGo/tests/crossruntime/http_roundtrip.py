"""Optional real Tomcat/production-Go HTTP reads on the disposable roundtrip DB.

Call run_http_roundtrip(verifier) while DataRehearsal's MySQL is still alive.
Requires args.http_go_image and pre-pulled tomcat:9-jre17-temurin/python:3.12-alpine.
Readiness remains 503 and is explicitly checked; direct business probes are isolated
rehearsal evidence, never permission to open a production gateway. No real OSS.
"""
import json
import os
from pathlib import Path
import subprocess
import sys
import time
from urllib.parse import urlencode

TOMCAT_IMAGE = 'tomcat:9-jre17-temurin'
PROBE_IMAGE = 'python:3.12-alpine'


def require(condition, reason):
    if not condition:
        raise AssertionError(reason)



def http_hostnames(prefix):
    names = {'oss': prefix + '-oss'}
    for phase, suffix in (('java-before', 'j0'), ('go', 'g'), ('java-rollback', 'j1')):
        names[phase] = prefix + '-h' + suffix
        names['probe-' + phase] = prefix + '-hp' + suffix
    require(all(len(name) <= 63 for name in names.values()), 'HTTP fixture DNS label exceeds 63 bytes')
    return names


def cases_for(seed, go):
    cases = []
    datasets = [('java', seed['nativeCompleted'], seed['nativeCompletedDetail'],
                 seed['webCompleted'], seed['webCompletedDetail'], seed['payload'], seed['payload']),
                ('go', go['nativeTask'], go['nativeDetail'], go['webTask'], go['webDetail'],
                 go['nativePayload'], go['webPayload'])]
    for origin, nt, nd, wt, wd, np, wp in datasets:
        for label, suffix, fields in [
                ('native-task', '/task/%d/info.json' % nt, ['taskId', 'deviceId', 'status', 'logDate']),
                ('native-index', '/task/%d/brief.json?' % nt + urlencode({'logTypes': '1', 'keyword': np}), None),
                ('native-detail', '/task/%d/detail.json' % nd, ['id', 'taskId', 'content', 'logType', 'logTime', 'formatContent']),
                ('native-details', '/task/query/details.json?detailIds=%d' % nd, ['id', 'taskId', 'content', 'logType', 'logTime', 'simpleContent']),
                ('web-task', '/web/taskDetail.json?tasks=%d' % wt, ['taskId', 'deviceId', 'status', 'pageNum', 'logDate']),
                ('web-index', '/web/detailIndex.json?' + urlencode({'tasks': wt, 'logTypes': '1', 'keyword': wp}), None),
                ('web-detail', '/web/logDetail.json?detailId=%d' % wd, ['id', 'taskId', 'content', 'logType', 'logTime', 'minuteOffset']),
                ('web-details', '/web/details.json?detailIds=%d' % wd, ['id', 'taskId', 'content', 'logType', 'logTime', 'minuteOffset'])]:
            cases.append({'name': origin + '-' + label, 'path': '/logan-web/logan' + suffix,
                          'fields': fields, 'payload': np if label.startswith('native') else wp,
                          'expectedDetailId': nd if label.startswith('native') else wd})
    return cases


def projection(response, fields):
    require(response['status'] == 200, 'business HTTP response must be 200')
    require('application/json' in response['contentType'].lower(), 'JSON content type required')
    body = response['body']
    require(body.get('code') == 200, 'business envelope code must be 200')
    data = body.get('data')
    require(data is not None and data != [], 'fixture data must be present')
    if fields is None:
        return data
    def select(row):
        require(isinstance(row, dict), 'object data required')
        require(all(field in row for field in fields), 'selected compatibility fields missing')
        return {field: row[field] for field in fields}
    return [select(row) for row in data] if isinstance(data, list) else select(data)


def require_index_id(data, expected):
    def ids(value):
        if isinstance(value, dict):
            for key, item in value.items():
                if key in ('id', 'detailId'):
                    yield item
                yield from ids(item)
        elif isinstance(value, list):
            for item in value:
                yield from ids(item)
    require(expected in list(ids(data)), 'index must contain the expected published detail ID')


# A helper talks only inside the verifier's --internal Docker network. Startup
# polling never records failed attempts as successful business responses.
PROBE = r'''
import json,sys,time,urllib.request,urllib.error
host,mode,cases=sys.argv[1],sys.argv[2],json.loads(sys.argv[3])
opener=urllib.request.build_opener(urllib.request.ProxyHandler({}))
def get(path):
    remaining=end-time.monotonic()
    if remaining<=0: raise TimeoutError('HTTP scenario overall deadline exceeded')
    try:
        r=opener.open('http://'+host+':8080'+path,timeout=min(4,remaining))
    except urllib.error.HTTPError as e:
        r=e
    raw=r.read()
    return {'status':r.status,'contentType':r.headers.get('Content-Type',''),'body':json.loads(raw)}
end=time.monotonic()+80
last='no response'
while True:
    try:
        first=get(cases[0]['path'])
        last=repr(first)
        if first['status']==200 and first['body'].get('code')==200: break
    except Exception as e: last=type(e).__name__+': '+str(e)
    if time.monotonic()>end: raise RuntimeError('actual business HTTP startup timeout; last='+last)
    time.sleep(.5)
result={'responses':{}}
if mode=='go':
    guard=get('/readyz')
    if guard['status']!=503: raise RuntimeError('readiness guard must remain 503')
    result['readinessGuard']=guard
for c in cases:
    response=get(c['path'])
    if response['status']!=200 or response['body'].get('code')!=200: raise RuntimeError('business response failed: '+c['name']+' '+str(response))
    result['responses'][c['name']]=response
print(json.dumps(result,ensure_ascii=False))
'''

OSS_FIXTURE = r'''
from http.server import BaseHTTPRequestHandler,HTTPServer
class Handler(BaseHTTPRequestHandler):
    def do_GET(self):
        print('OSS_FIXTURE_REQUEST',flush=True)
        self.send_response(404);self.end_headers();self.wfile.write(b'<Error><Code>NoSuchKey</Code></Error>')
    do_PUT=do_GET
    do_DELETE=do_GET
    do_HEAD=do_GET
    def log_message(self,*args): pass
print('OSS_FIXTURE_READY',flush=True)
HTTPServer(('0.0.0.0',8080),Handler).serve_forever()
'''



def capture_app_diagnostics(v, name):
    """Persist owned application state/logs before controller cleanup, best effort."""
    for args in (('inspect', name), ('logs', name)):
        try:
            v.docker(*args, timeout=10, accept=True)
        except Exception:
            # Diagnostics must not replace the original probe/assertion failure.
            pass


def run_http_roundtrip(v):
    names = http_hostnames(v.prefix)
    go_image = v.args.http_go_image
    require(go_image, 'explicit production Go HTTP image required')
    for image in (TOMCAT_IMAGE, PROBE_IMAGE, go_image):
        v.docker('image', 'inspect', '--format', '{{.Id}}', image)
    seed = json.loads((v.shared / 'java-seed.json').read_text())
    go = json.loads((v.shared / 'go-produced.json').read_text())
    cases = cases_for(seed, go)
    uid = str(os.getuid())
    work = v.args.work_dir / 'http'
    work.mkdir(mode=0o700)
    secrets = work / 'secrets'
    secrets.mkdir(mode=0o700)
    key = secrets / 'fixture-rsa.pem'
    subprocess.run(['openssl', 'genpkey', '-algorithm', 'RSA', '-pkeyopt', 'rsa_keygen_bits:2048', '-out', str(key)],
                   check=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=30)
    key.chmod(0o600)
    oss = v.reserve('container', names['oss'])
    common = ['--label', '', '--network', v.network,
              '--read-only', '--cap-drop', 'ALL', '--security-opt', 'no-new-privileges:true',
              '--user', uid + ':' + uid, '--cpus', '1', '--pids-limit', '256']
    # Reuse the controller's exact owned-resource label from its MySQL container.
    _, labels_json = v.docker('inspect', '--format', '{{json .Config.Labels}}', v.mysql)
    labels = json.loads(labels_json)
    owned_label = next((k for k, value in labels.items() if value == v.run_id), None)
    require(owned_label is not None, 'owned MySQL label missing')
    common[1] = owned_label + '=' + v.run_id
    v.docker('run', '-d', '--name', oss, *common, '--memory', '128m', PROBE_IMAGE, 'python', '-u', '-c', OSS_FIXTURE)
    env = dict(v.env)
    env.update({'CROSSRUNTIME_DISPOSABLE': 'YES',
                'CROSSRUNTIME_JDBC_URL': 'jdbc:mysql://' + v.mysql + ':3306/' + v.db + '?useSSL=false&allowPublicKeyRetrieval=true&serverTimezone=Asia/Shanghai',
                'CROSSRUNTIME_DB_USER': 'root', 'CROSSRUNTIME_DB_PASSWORD': v.password,
                'CROSSRUNTIME_OSS_EMULATOR_ENDPOINT': 'http://' + oss + ':8080'})
    war = work / 'logan-web.war'
    builder = Path(__file__).with_name('build_java_war.py')
    subprocess.run([sys.executable, str(builder), '--output', str(war)], env=env, check=True,
                   stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=60)
    results = {}
    for phase in ('java-before', 'go', 'java-rollback'):
        name = v.reserve('container', names[phase])
        tmp = ['--tmpfs', '/tmp:rw,nosuid,nodev,size=134217728,uid=' + uid + ',gid=' + uid + ',mode=0700']
        if phase.startswith('java'):
            options = [*tmp, '--memory', '768m',
                       '--tmpfs', '/data/applogs/logan-web:rw,nosuid,nodev,size=16777216,uid=' + uid + ',gid=' + uid + ',mode=0700',
                       '--tmpfs', '/usr/local/tomcat/conf/Catalina/localhost:rw,nosuid,nodev,size=16777216,uid=' + uid + ',gid=' + uid + ',mode=0700',
                       '--tmpfs', '/usr/local/tomcat/webapps:rw,nosuid,nodev,size=268435456,uid=' + uid + ',gid=' + uid,
                       '--tmpfs', '/usr/local/tomcat/work:rw,nosuid,nodev,size=67108864,uid=' + uid + ',gid=' + uid,
                       '--tmpfs', '/usr/local/tomcat/temp:rw,nosuid,nodev,size=67108864,uid=' + uid + ',gid=' + uid,
                       '--tmpfs', '/usr/local/tomcat/logs:rw,nosuid,nodev,size=67108864,uid=' + uid + ',gid=' + uid,
                       '--mount', 'type=bind,src=' + str(war) + ',dst=/fixture/logan-web.war,readonly',
                       '-e', 'TZ=Asia/Shanghai', '-e', 'JAVA_TOOL_OPTIONS=--add-opens=java.base/java.util=ALL-UNNAMED -Duser.timezone=Asia/Shanghai',
                       TOMCAT_IMAGE, 'sh', '-c', 'cp /fixture/logan-web.war /usr/local/tomcat/webapps/logan-web.war && exec catalina.sh run']
        else:
            values = ['LOGAN_ENV=production', 'LOGAN_DB_ENABLED=true',
                      'LOGAN_MYSQL_DSN=root:' + v.password + '@tcp(' + v.mysql + ':3306)/' + v.db + '?parseTime=true&charset=utf8mb4',
                      'LOGAN_NATIVE_ENABLED=true', 'LOGAN_NATIVE_QUERY_ENABLED=true', 'LOGAN_WEB_ENABLED=true',
                      'LOGAN_NATIVE_AES_KEY=fixture-key-1234', 'LOGAN_NATIVE_AES_IV=fixture-iv--1234',
                      'LOGAN_NATIVE_TEMP_DIR=/tmp', 'LOGAN_WEB_RSA_PRIVATE_KEY_FILE=/secrets/fixture-rsa.pem',
                      'LOGAN_OSS_ENDPOINT=http://' + oss + ':8080', 'LOGAN_OSS_REGION=fixture',
                      'LOGAN_OSS_BUCKET=fixture', 'LOGAN_OSS_PREFIX=logan-logs/', 'LOGAN_OSS_PATH_STYLE=true',
                      'LOGAN_OSS_ACCESS_KEY_ID=fixture-only', 'LOGAN_OSS_ACCESS_KEY_SECRET=fixture-only',
                      'LOGAN_MAINTENANCE_ENABLED=false', 'GOMAXPROCS=1']
            envfile = v.private('http-go.env', '\n'.join(values) + '\n')
            options = [*tmp, '--memory', '512m', '--mount', 'type=bind,src=' + str(secrets) + ',dst=/secrets,readonly',
                       '--env-file', str(envfile), go_image]
        v.docker('run', '-d', '--name', name, *common, *options)
        v.events.append({'phase': 'http-' + phase, 'event': 'started', 'unix_time': time.time()})
        probe = v.reserve('container', names['probe-' + phase])
        try:
            v.docker('run', '-d', '--name', probe, *common, '--memory', '128m', PROBE_IMAGE,
                     'python', '-c', PROBE, name, 'go' if phase == 'go' else 'java', json.dumps(cases))
            _, exitcode = v.docker('wait', probe, timeout=100)
            _, raw = v.docker('logs', probe)
            require(exitcode.strip() == '0', 'actual HTTP probe failed: ' + phase)
            capture = json.loads(raw.strip().splitlines()[-1])
            for case in cases:
                selected = projection(capture['responses'][case['name']], case['fields'])
                if case['name'].endswith('-index'):
                    require_index_id(selected, case['expectedDetailId'])
                if 'detail' in case['name']:
                    rows = selected if isinstance(selected, list) else [selected]
                    require(all(row['content'] == case['payload'] for row in rows), 'HTTP payload mismatch: ' + case['name'])
            results[phase] = capture
            target = v.args.out / ('http-' + phase + '.json')
            target.write_text(json.dumps(capture, indent=2, ensure_ascii=False)); target.chmod(0o600)
        except BaseException:
            capture_app_diagnostics(v, name)
            raise
        v.docker('stop', '--time', '15', name, timeout=35)
        _, state_json = v.docker('inspect', '--format', '{{json .State}}', name)
        state = json.loads(state_json)
        require(not state['Running'] and not state['OOMKilled'], 'previous HTTP writer must stop before next starts')
        require(state['ExitCode'] in ([0] if phase == 'go' else [0, 143]), 'HTTP backend must not need forced SIGKILL')
        v.events.append({'phase': 'http-' + phase, 'event': 'stopped', 'exit_code': state['ExitCode'], 'unix_time': time.time()})
    for case in cases:
        expected = projection(results['java-before']['responses'][case['name']], case['fields'])
        for phase in ('go', 'java-rollback'):
            require(projection(results[phase]['responses'][case['name']], case['fields']) == expected,
                    'HTTP semantic compatibility mismatch: ' + phase + ' ' + case['name'])
    _, oss_logs = v.docker('logs', oss)
    _, oss_running = v.docker('inspect', '--format', '{{.State.Running}}', oss)
    require('OSS_FIXTURE_READY' in oss_logs and oss_running.strip() == 'true', 'explicit OSS fixture must remain available')
    require('OSS_FIXTURE_REQUEST' not in oss_logs, 'completed HTTP reads unexpectedly called the explicit OSS fixture')
    evidence = {'status': 'passed', 'datasets': ['java-completed', 'go-produced'], 'readEndpointsPerDataset': 8,
                'sequentialProcesses': ['original-java-tomcat9', 'production-go', 'original-java-tomcat9-rollback'],
                'comparison': 'HTTP200, envelope200, JSON content type and selected stable task/detail/index fields',
                'readinessGuard': 'Go /readyz remained 503; no public gateway opened', 'oss': 'explicit fixture; zero object requests',
                'notExecuted': ['SDK/encrypted/RSA upload', 'real OSS or signed URL download', 'production cutover', 'all 20 route certification']}
    target = v.args.out / 'http-roundtrip.json'
    target.write_text(json.dumps(evidence, indent=2)); target.chmod(0o600)
    return evidence
