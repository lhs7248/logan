#!/usr/bin/env python3
"""Exercise ONLY isolated topology/order. Never authorizes production cutover."""
import argparse
import base64
from datetime import datetime, timezone
import json
import os
from pathlib import Path
import signal
import subprocess
import tempfile
import time
import urllib.error
import urllib.request
import uuid

ROOT = Path(__file__).resolve().parents[1]


def require(value, reason):
    if not value:
        raise AssertionError(reason)


def validate_host(host, allowed):
    if not allowed or not host.startswith('unix:///'):
        raise ValueError('requires authorization and an absolute unix:/// isolated socket')
    path = Path(host[7:])
    if '..' in path.parts or any(c in str(path) for c in ('\n', '\r', '\x00')):
        raise ValueError('invalid socket path')
    resolved = str(path.resolve())
    if 'logan' not in resolved.lower() or resolved in ('/var/run/docker.sock', '/run/docker.sock', '/private/var/run/docker.sock'):
        raise ValueError('socket must resolve to a dedicated logan runtime')
    return 'unix://' + resolved


def interrupt(*_):
    raise KeyboardInterrupt('rehearsal interrupted')


def reserve_and_create(resources, resource, create):
    # Reserve first: a daemon may complete creation after its client is interrupted.
    resources.append(resource)
    return create()


def run_cli(command, env):
    process = subprocess.Popen(command, env=env, text=True, stdout=subprocess.PIPE,
                               stderr=subprocess.PIPE, start_new_session=True)
    try:
        stdout, stderr = process.communicate(timeout=120)
    except BaseException:
        try:
            os.killpg(process.pid, signal.SIGKILL)
        except ProcessLookupError:
            pass
        process.communicate()
        raise
    if process.returncode:
        raise RuntimeError(stderr.strip() or stdout.strip())
    return stdout.strip()


def cleanup_owned(docker, resources, owner):
    errors = []
    for kind, name in reversed(resources):
        try:
            metadata = json.loads(docker(kind, 'inspect', name))[0]
            labels = metadata.get('Config', {}).get('Labels', {}) if kind == 'container' else metadata.get('Labels', {})
            require(labels.get('logan.rehearsal') == owner, 'refusing cleanup of resource with different owner: ' + name)
            docker(kind, 'rm', *(['-f'] if kind == 'container' else []), name)
        except (RuntimeError, AssertionError, ValueError, KeyError, OSError, subprocess.TimeoutExpired) as error:
            # Only Docker's explicit missing-object error is benign; daemon/transport failures fail cleanup.
            if isinstance(error, RuntimeError) and ('No such container: ' + name in str(error) or 'No such network: ' + name in str(error) or 'network ' + name + ' not found' in str(error)):
                continue
            errors.append(name + ': ' + str(error))
    return errors


def decode_probe(output):
    result = json.loads(output)
    require(isinstance(result, list) and len(result) == 3, 'invalid isolated HTTP probe response')
    require(isinstance(result[0], int) and isinstance(result[2], dict), 'invalid isolated HTTP probe metadata')
    return result[0], base64.b64decode(result[1], validate=True), result[2]


def allowed_transition(current, target, java_running, go_running):
    if target == 'java' and (go_running or not java_running):
        raise ValueError('Java may open only after Go is stopped')
    if target == 'go' and (java_running or not go_running):
        raise ValueError('Go diagnostic ping may open only after Java simulator is stopped')
    if current != 'closed' and target != 'closed':
        raise ValueError('entry must close before changing backend')
    return target


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('--docker', required=True, help='explicit Docker CLI path')
    p.add_argument('--host', required=True, help='explicit dedicated unix:// socket')
    p.add_argument('--allow-isolated-runtime', action='store_true')
    p.add_argument('--go-image', required=True, help='prebuilt local Go foundation image; never pulled')
    p.add_argument('--fixture-image', default='python:3.12-alpine')
    p.add_argument('--work-dir', type=Path, required=True, help='fresh /private/tmp directory shared into isolated VM at identical path')
    p.add_argument('--report', default='/private/tmp/logan-rehearsal-report.md')
    a = p.parse_args()
    try:
        a.host = validate_host(a.host, a.allow_isolated_runtime)
        require(Path(a.docker).is_absolute(), '--docker must be an absolute CLI path')
        work = a.work_dir.resolve()
        require(Path('/private/tmp') in work.parents, '--work-dir must be under /private/tmp')
        require(',' not in str(work), 'commas unsupported in mount path')
        work.mkdir(mode=0o700, parents=True, exist_ok=False)
    except (ValueError, AssertionError, OSError) as error:
        p.error(str(error))
    previous_signals = {sig: signal.signal(sig, interrupt) for sig in (signal.SIGINT, signal.SIGTERM)}
    prefix = 'logan-rehearsal-' + uuid.uuid4().hex[:12]
    names = {k: prefix + '-' + k for k in ('java', 'go', 'gateway')}
    events, observations, owned = [], [], []
    env = {k: v for k, v in os.environ.items() if not k.startswith(('DOCKER_', 'BUILDX_', 'COMPOSE_'))}
    config = work / 'docker-config'
    config.mkdir(mode=0o700)
    env['DOCKER_CONFIG'] = str(config)
    def docker(*args):
        return run_cli([a.docker, '--host', a.host, *args], env)
    probe_container = names['java']
    def get(url):
        # Probes execute inside the isolated network; no published ports or VM forwarding.
        code = """import base64,json,sys,urllib.request,urllib.error
opener=urllib.request.build_opener(urllib.request.ProxyHandler({}))
try:
 r=opener.open(sys.argv[1],timeout=3)
except urllib.error.HTTPError as e:
 r=e
print(json.dumps([r.code,base64.b64encode(r.read()).decode(),dict(r.headers)]))
"""
        return decode_probe(docker('exec', probe_container, 'python', '-c', code, url))
    def wait(url):
        for _ in range(100):
            try:
                return get(url)
            except (OSError, RuntimeError):
                time.sleep(.1)
        raise RuntimeError('isolated listener did not start: ' + url)
    def address(name):
        return 'http://' + name + ':8080'
    def event(name):
        events.append(datetime.now(timezone.utc).isoformat() + ' ' + name)
    success = False
    with tempfile.TemporaryDirectory(prefix=prefix, dir=str(work)) as tmp:
        # Only synthetic state is shared; the fixture runs as UID10001 without DAC capabilities.
        Path(tmp).chmod(0o755)
        fixture_dir = Path(tmp) / 'fixture'
        fixture_dir.mkdir(mode=0o755)
        fixture_dir.chmod(0o755)
        fixture_file = fixture_dir / 'fixture.py'
        fixture_file.write_bytes((ROOT / 'tests/rehearsal/fixture.py').read_bytes())
        fixture_file.chmod(0o644)
        statepath = Path(tmp) / 'state.json'
        state = {'backend': 'closed', 'java': names['java'] + ':8080', 'go': names['go'] + ':8080'}
        running = {'java': False, 'go': False}
        def route(target):
            allowed_transition(state['backend'], target, running['java'], running['go'])
            state['backend'] = target
            # Atomic replacement prevents gateway reading partially written JSON.
            staging = statepath.with_suffix('.tmp')
            staging.write_text(json.dumps(state))
            staging.chmod(0o644)
            staging.replace(statepath)
            event('ENTRY_CLOSED' if target == 'closed' else target.upper() + '_DIAGNOSTIC_OPEN')
        def run(kind, image, *command):
            owned.append(('container', names[kind]))
            docker('run', '-d', '--pull=never', '--name', names[kind], '--label', 'logan.rehearsal=' + prefix,
                   '--network', prefix, '--read-only', '--user', '10001:10001', '--cap-drop=ALL', '--security-opt', 'no-new-privileges',
                   '--pids-limit', '64', '--memory', '256m', '--cpus', '1', '--tmpfs', '/tmp:rw,noexec,nosuid,size=16m',
                   *(['--mount', 'type=bind,src=' + str(fixture_dir) + ',dst=/fixture,readonly', '--mount', 'type=bind,src=' + tmp + ',dst=/state,readonly'] if kind != 'go' else []), image, *command)
        try:
            require(docker('info', '--format', '{{.OSType}}') == 'linux', 'isolated engine must run Linux containers')
            for image in (a.go_image, a.fixture_image):
                image_id = docker('image', 'inspect', '--format', '{{.Id}}', image)
                observations.append('Local image ' + image + ': ' + image_id)
            reserve_and_create(owned, ('network', prefix), lambda: docker('network', 'create', '--internal', '--label', 'logan.rehearsal=' + prefix, prefix))
            route('closed')
            run('java', a.fixture_image, 'python', '/fixture/fixture.py', '--role', 'java-simulator')
            running['java'] = True
            wait(address(names['java']) + '/ping')
            run('gateway', a.fixture_image, 'python', '/fixture/fixture.py', '--role', 'gateway')
            probe_container = names['gateway']
            gateway = address(names['gateway'])
            wait(gateway + '/ping')
            route('java')
            status, body, _ = get(gateway + '/logan/test/?a=1&a=2')
            require(status == 200 and json.loads(body)['uri'] == '/logan-web/logan/test/?a=1&a=2', "rehearsal check failed: status == 200 and json.loads(body)['uri'] == '/logan-web/logan/test/?a=1&a=2'")
            observations.append('Java simulator path rewrite preserves trailing slash and repeated query parameters.')
            route('closed')
            require(get(gateway + '/logan/test')[0] == 503, "rehearsal check failed: get(gateway + '/logan/test')[0] == 503")
            docker('stop', '-t', '10', names['java'])
            running['java'] = False
            event('JAVA_STOPPED')
            run('go', a.go_image)
            running['go'] = True
            event('GO_STARTED')
            go = address(names['go'])
            require(wait(go + '/ping')[0] == 200, "rehearsal check failed: wait(go + '/ping')[0] == 200")
            require(get(go + '/readyz')[0] == 503, "rehearsal check failed: get(go + '/readyz')[0] == 503")
            observations.append('Actual Go image ping=200; readyz=503. Business entry remains blocked.')
            route('go')
            require(get(gateway + '/ping')[0] == 200, "rehearsal check failed: get(gateway + '/ping')[0] == 200")
            require(get(gateway + '/logan/test')[0] == 503, "rehearsal check failed: get(gateway + '/logan/test')[0] == 503")
            route('closed')
            docker('stop', '-t', '10', names['go'])
            running['go'] = False
            event('GO_STOPPED')
            docker('start', names['java'])
            running['java'] = True
            event('JAVA_STARTED')
            wait(address(names['java']) + '/ping')
            route('java')
            require(get(gateway + '/logan/rollback?proof=1')[0] == 200, "rehearsal check failed: get(gateway + '/logan/rollback?proof=1')[0] == 200")
            success = True
        except BaseException as error:
            observations.append('FAILURE: ' + type(error).__name__ + ': ' + str(error))
            # Capture only owned synthetic container diagnostics, before deleting evidence.
            for kind, name in owned:
                if kind != 'container':
                    continue
                try:
                    metadata = json.loads(docker('container', 'inspect', name))[0]
                    require(metadata.get('Config', {}).get('Labels', {}).get('logan.rehearsal') == prefix, 'foreign diagnostics refused')
                    observations.append('Container ' + name + ' state: ' + json.dumps(metadata.get('State', {})))
                    observations.append('Container ' + name + ' logs: ' + docker('logs', '--tail', '40', name))
                except (Exception,) as diagnostic_error:
                    observations.append('Diagnostics unavailable for ' + name + ': ' + str(diagnostic_error))
            raise
        finally:
            # Prevent a second interrupt from abandoning ownership-checked cleanup.
            for sig in previous_signals:
                signal.signal(sig, signal.SIG_IGN)
            cleanup_errors = cleanup_owned(docker, owned, prefix)
            if cleanup_errors:
                success = False
                observations.extend('CLEANUP FAILED: ' + error for error in cleanup_errors)
            report_fd = os.open(a.report, os.O_WRONLY | os.O_CREAT | os.O_TRUNC | os.O_NOFOLLOW, 0o600)
            os.fchmod(report_fd, 0o600)
            with os.fdopen(report_fd, 'w') as report:
                report.write('# Isolated topology rehearsal\n\n' + ('PASS' if success else 'FAIL') + '\n\n'
                + 'Scope: actual Go container; synthetic Python gateway; Java SIMULATOR, not original Java.\n'
                + 'No production cutover, original Java stop, DB/OSS access, business-write compatibility, real gateway reload, or cross-version single-writer data proof.\n'
                + 'Go readiness remains a release blocker. Diagnostic ping routing is rehearsal-only.\n\n'
                + '\n'.join('- ' + x for x in observations) + '\n\nEvents:\n' + '\n'.join('- ' + x for x in events) + '\n')
            for sig, previous in previous_signals.items():
                signal.signal(sig, previous)
            if cleanup_errors:
                raise RuntimeError('cleanup failed: ' + '; '.join(cleanup_errors))
    print(a.report)


if __name__ == '__main__':
    main()
