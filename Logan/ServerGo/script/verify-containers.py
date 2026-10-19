#!/usr/bin/env python3
"""Build and verify the real image on an explicitly authorized isolated Linux Docker engine.

Never uses the ambient context, publishes ports, or accesses production credentials.
Exit 0: covered checks passed; 1: failed; 2: runtime/build unavailable; 130: interrupted.
"""
import argparse
import json
import os
from pathlib import Path
import re
import signal
import subprocess
import time
import uuid
from urllib.parse import urlsplit

ROOT = Path(__file__).resolve().parents[1]
CHECKOUT = ROOT.parents[1]
LABEL = 'io.logan.container-verifier'


def validate_host(host, allowed):
    if not allowed:
        raise ValueError('--allow-isolated-runtime is required')
    if not host.startswith('unix:///'):
        raise ValueError('only an explicit absolute unix:// isolated socket is allowed')
    path = Path(host[7:])
    if '..' in path.parts or any(c in str(path) for c in ('\n', '\r', '\x00')):
        raise ValueError('invalid socket path')
    # A dedicated, task-named engine is required; the flag alone cannot select a default engine.
    if 'logan' not in str(path.resolve()).lower() or str(path.resolve()) in ('/var/run/docker.sock', '/run/docker.sock'):
        raise ValueError('socket path must identify a dedicated logan runtime')
    return host


def validate_build_proxy(value):
    if not value:
        return None
    if any(character.isspace() for character in value) or any(character in value for character in ('%', '\\', '?', '#')):
        raise ValueError('build proxy must be a plain HTTP(S) origin without credentials or query')
    parsed = urlsplit(value)
    if parsed.scheme not in ('http', 'https') or not parsed.hostname or parsed.username is not None or parsed.password is not None or parsed.path not in ('', '/'):
        raise ValueError('build proxy must contain only HTTP(S) scheme, host and optional port')
    if parsed.netloc.endswith(':') or (parsed.port is not None and not 1 <= parsed.port <= 65535):
        raise ValueError('invalid build proxy port')
    if not re.fullmatch(r'[A-Za-z0-9.:-]+', parsed.hostname):
        raise ValueError('invalid build proxy host')
    return value


def require(value, reason):
    if not value:
        raise AssertionError(reason)


class DockerError(RuntimeError):
    pass


class Verifier:
    def __init__(self, args):
        self.args = args
        self.run_id = uuid.uuid4().hex
        self.prefix = 'logan-verifier-' + self.run_id
        self.tag = self.prefix + ':local'
        self.owned = []
        self.retained_images = []
        self.checks = []
        self.serial = 0
        self.env = {k: v for k, v in os.environ.items()
                    if not k.startswith(('DOCKER_', 'BUILDX_', 'COMPOSE_'))}
        self.env['DOCKER_CONFIG'] = str(args.out / 'docker-config')
        Path(self.env['DOCKER_CONFIG']).mkdir(mode=0o700)

    def docker(self, *args, timeout=60, accept=False):
        self.serial += 1
        command = [self.args.docker, '--host', self.args.host, *args]
        process = subprocess.Popen(command, cwd=ROOT, env=self.env,
                                   stdout=subprocess.PIPE, stderr=subprocess.STDOUT,
                                   text=True, start_new_session=True)
        try:
            output, _ = process.communicate(timeout=timeout)
        except BaseException:
            try:
                os.killpg(process.pid, signal.SIGKILL)
            except ProcessLookupError:
                pass
            output, _ = process.communicate()
            self.log(command, output, process.returncode)
            raise
        self.log(command, output, process.returncode)
        if process.returncode and not accept:
            raise DockerError('Docker command failed; see command-%03d.json' % self.serial)
        return process.returncode, output

    def log(self, command, output, code):
        path = self.args.out / ('command-%03d.json' % self.serial)
        path.write_text(json.dumps({'argv': command, 'exit_code': code, 'output': output}, indent=2) + '\n')
        path.chmod(0o600)

    def check(self, name, fn):
        try:
            fn()
        except Exception as error:
            self.checks.append({'check': name, 'status': 'failed', 'reason': str(error)})
            raise
        self.checks.append({'check': name, 'status': 'passed'})

    def reserve(self, kind, name):
        require(name.startswith(self.prefix), 'refusing unowned resource name')
        self.owned.append((kind, name))
        return name

    @staticmethod
    def resource_missing(kind, name, output):
        target = re.escape(name)
        patterns = [r'(?:Error: |Error response from daemon: )No such (?:object|' + kind + r'): ' + target]
        if kind == 'network':
            patterns.append(r'Error response from daemon: network ' + target + r' not found')
        if kind == 'volume':
            patterns.append(r'Error response from daemon: get ' + target + r': no such volume')
        lines = [line.strip() for line in output.splitlines() if line.strip() and line.strip() != '[]']
        return bool(lines) and all(any(re.fullmatch(pattern, line, re.IGNORECASE) for pattern in patterns) for line in lines)

    def cleanup(self):
        failures = []
        for kind, name in reversed(self.owned):
            try:
                # Labels are checked before deletion, including after interrupted creation.
                if kind == 'container':
                    code, data = self.docker('inspect', name, accept=True)
                    if code:
                        if self.resource_missing(kind, name, data):
                            continue
                        raise DockerError('cleanup inspection failed: ' + name)
                    labels = json.loads(data)[0]['Config'].get('Labels', {})
                elif kind == 'image':
                    code, data = self.docker('image', 'inspect', name, accept=True)
                    if code:
                        if self.resource_missing(kind, name, data):
                            continue
                        raise DockerError('cleanup inspection failed: ' + name)
                    labels = json.loads(data)[0]['Config'].get('Labels', {})
                else:
                    code, data = self.docker(kind, 'inspect', name, accept=True)
                    if code:
                        if self.resource_missing(kind, name, data):
                            continue
                        raise DockerError('cleanup inspection failed: ' + name)
                    labels = json.loads(data)[0].get('Labels', {})
                require(labels.get(LABEL) == self.run_id, 'cleanup label mismatch: ' + name)
                if kind == 'image' and getattr(self.args, 'keep_image', False):
                    self.retained_images.append({'tag': name, 'image_id': json.loads(data)[0]['Id'], 'ownership_label': LABEL + '=' + self.run_id})
                    continue
                command = ('rm', '-f', name) if kind == 'container' else (kind, 'rm', name)
                self.docker(*command)
            except Exception as error:
                failures.append(str(error))
        self.checks.append({'check': 'owned_resource_cleanup', 'status': 'failed' if failures else 'passed', 'errors': failures})
        return not failures

    def readiness_blocked(self, container):
        code, output = self.docker('exec', container, 'wget', '-S', '-T', '3', '-O', '-',
                                   'http://127.0.0.1:8080/readyz', accept=True)
        statuses = re.findall(r'^\s*HTTP/[0-9.]+[ \t]+([0-9]{3})(?:[ \t]|\r?$)', output, re.MULTILINE)
        require(code != 0 and statuses == ['503'], 'readiness must return exactly HTTP 503; connection failures do not qualify')

    def execute(self):
        _, data = self.docker('info', '--format', '{{json .}}')
        require(json.loads(data)['OSType'] == 'linux', 'engine must run Linux containers')
        self.checks.append({'check': 'explicit_isolated_linux_engine', 'status': 'passed'})
        self.reserve('image', self.tag)
        build = ['build', '--label', LABEL + '=' + self.run_id, '--tag', self.tag]
        if getattr(self.args, 'build_proxy', None):
            for key in ('HTTP_PROXY', 'HTTPS_PROXY'):
                build.extend(['--build-arg', key + '=' + self.args.build_proxy])
        build.extend(['--file', str(ROOT / 'Dockerfile'), str(ROOT)])
        self.check('actual_dockerfile_build', lambda: self.docker(*build, timeout=1200))
        def image_contract():
            _, data = self.docker('image', 'inspect', self.tag)
            value = json.loads(data)[0]
            require(value['Os'] == 'linux', 'image OS')
            require(value['Architecture'] in ('amd64', 'arm64'), 'unexpected image architecture')
            require(value['Config']['User'] == '10001:10001', 'image user')
            require(value['Config']['Entrypoint'] == ['/usr/local/bin/logan-server'], 'image entrypoint')
            require(value['Config']['Labels'][LABEL] == self.run_id, 'image ownership label')
            # Dynamic linkage check runs the built binary's filesystem, not source text.
            static = self.reserve('container', self.prefix + '-static')
            self.docker('run', '--name', static, '--label', LABEL + '=' + self.run_id, '--network', 'none', '--read-only', '--cap-drop', 'ALL',
                        '--security-opt', 'no-new-privileges:true', '--memory', '1g', '--cpus', '1', '--pids-limit', '128', '--entrypoint', '/bin/sh', self.tag,
                        '-ec', 'test -x /usr/local/bin/logan-server; ! ldd /usr/local/bin/logan-server 2>&1 | grep -q "=>"')
        self.check('image_metadata_and_static_binary', image_contract)
        network = self.reserve('network', self.prefix + '-net')
        self.docker('network', 'create', '--internal', '--label', LABEL + '=' + self.run_id, network)
        volume = self.reserve('volume', self.prefix + '-secret')
        self.docker('volume', 'create', '--label', LABEL + '=' + self.run_id, volume)
        # A synthetic Linux-owned 0400 marker models a private key's permissions without host chown.
        helper = self.reserve('container', self.prefix + '-initialize')
        self.docker('run', '--name', helper, '--label', LABEL + '=' + self.run_id,
                    '--network', 'none', '--read-only', '--cap-drop', 'ALL', '--cap-add', 'CHOWN',
                    '--security-opt', 'no-new-privileges:true', '--memory', '1g', '--cpus', '1', '--pids-limit', '128', '--user', '0:0',
                    '--mount', 'type=volume,src=' + volume + ',dst=/fixture',
                    '--entrypoint', '/bin/sh', self.tag, '-ec',
                    'printf synthetic-permission-marker > /fixture/marker; chmod 0400 /fixture/marker; chown 10001:10001 /fixture/marker')
        container = self.reserve('container', self.prefix + '-app')
        options = ['run', '-d', '--name', container, '--label', LABEL + '=' + self.run_id,
                   '--network', network, '--read-only', '--user', '10001:10001',
                   '--cap-drop', 'ALL', '--security-opt', 'no-new-privileges:true',
                   '--memory', '1g', '--cpus', '1', '--pids-limit', '128',
                   '--tmpfs', '/var/lib/logan-tmp:rw,noexec,nosuid,nodev,size=536870912,uid=10001,gid=10001,mode=0700',
                   '--mount', 'type=volume,src=' + volume + ',dst=/run/logan-secrets,readonly']
        if self.args.work_dir:
            options.extend(['--mount', 'type=bind,src=' + str(self.args.work_dir) + ',dst=/run/logan-bind,readonly'])
        for value in ('LOGAN_ENV=development', 'LOGAN_HTTP_ADDRESS=127.0.0.1:8080',
                      'LOGAN_CONTEXT_PATH=/logan-web', 'LOGAN_DB_ENABLED=false',
                      'LOGAN_NATIVE_ENABLED=false', 'LOGAN_WEB_ENABLED=false',
                      'LOGAN_NATIVE_QUERY_ENABLED=false', 'LOGAN_MAINTENANCE_ENABLED=false',
                      'LOGAN_METRICS_ENABLED=true', 'LOGAN_EXIT_WAIT=60s',
                      'LOGAN_NATIVE_TEMP_DIR=/var/lib/logan-tmp', 'GOMAXPROCS=1', 'GOMEMLIMIT=384MiB'):
            options.extend(['--env', value])
        self.docker(*options, self.tag)
        def probe():
            for _ in range(40):
                code, _ = self.docker('exec', container, 'wget', '-q', '-T', '2', '-O', '-', 'http://127.0.0.1:8080/livez', accept=True)
                if code == 0:
                    return
                time.sleep(.25)
            raise AssertionError('foundation liveness did not become healthy')
        self.check('foundation_liveness', probe)
        self.check('metrics_endpoint', lambda: self.docker('exec', container, 'wget', '-q', '-T', '3', '-O', '-', 'http://127.0.0.1:8080/metrics'))
        self.check('readiness_stays_blocked', lambda: self.readiness_blocked(container))
        self.check('runtime_permissions_and_cgroup_limits', lambda: self.docker('exec', container, 'sh', '-ec', r'''
            test "$(id -u)" = 10001
            test "$(stat -c '%a:%u' /run/logan-secrets/marker)" = 400:10001
            test "$(cat /run/logan-secrets/marker)" = synthetic-permission-marker
            ! echo forbidden >> /run/logan-secrets/marker
            test "$(stat -c '%a:%u' /var/lib/logan-tmp)" = 700:10001
            touch /var/lib/logan-tmp/probe; rm /var/lib/logan-tmp/probe
            ! touch /etc/logan-verifier-probe
            test "$(cat /sys/fs/cgroup/memory.max)" = 1073741824
            test "$(cat /sys/fs/cgroup/cpu.max)" = '100000 100000'
            test "$(cat /sys/fs/cgroup/pids.max)" = 128
            grep -Eq '^CapEff:[[:space:]]+0000000000000000$' /proc/self/status
            grep -Eq '^NoNewPrivs:[[:space:]]+1$' /proc/self/status
            awk '$2 == "/var/lib/logan-tmp" && $3 == "tmpfs" && $4 ~ /size=524288k/ {ok=1} END {exit !ok}' /proc/mounts
            cat /sys/fs/cgroup/memory.max /sys/fs/cgroup/cpu.max /sys/fs/cgroup/pids.max
            cat /proc/self/status
            cat /proc/mounts
        '''))
        if self.args.work_dir:
            self.check('synthetic_readonly_bind', lambda: self.docker('exec', container, 'sh', '-ec',
                       'test "$(cat /run/logan-bind/marker)" = synthetic-bind-marker; ! echo forbidden >> /run/logan-bind/marker'))
        def graceful():
            start = time.monotonic()
            self.docker('kill', '--signal', 'TERM', container)
            _, output = self.docker('wait', container, timeout=90)
            require(output.strip() == '0', 'SIGTERM container exit must be zero')
            require(time.monotonic() - start < 90, 'shutdown exceeded 90 seconds')
            self.docker('logs', container)
        self.check('restart_tmpfs_marker_created', lambda: self.docker('exec', container, 'sh', '-ec',
                   'printf before-restart > /var/lib/logan-tmp/restart-marker; test -s /var/lib/logan-tmp/restart-marker'))
        self.check('foundation_sigterm_exit', graceful)
        self.docker('start', container)
        self.check('container_restart_liveness', probe)
        self.check('restart_tmpfs_clean', lambda: self.docker('exec', container, 'sh', '-ec',
                   'test ! -e /var/lib/logan-tmp/restart-marker; test -z "$(ls -A /var/lib/logan-tmp)"'))
        self.check('restart_sigterm_exit', graceful)


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--docker', required=True, help='explicit Docker CLI path')
    parser.add_argument('--host', required=True, help='unix:// socket for dedicated logan runtime')
    parser.add_argument('--allow-isolated-runtime', action='store_true')
    parser.add_argument('--build-proxy', type=validate_build_proxy, help='optional HTTP(S) build proxy origin, without authentication; used only as Docker build arguments')
    parser.add_argument('--keep-image', action='store_true', help='retain only this run labeled local image for follow-up rehearsal; caller must remove it')
    parser.add_argument('--out', type=Path, required=True, help='fresh private evidence directory outside checkout')
    parser.add_argument('--work-dir', type=Path, help='fresh directory outside checkout, shared into VM at same absolute path; optional synthetic bind test')
    args = parser.parse_args(argv)
    try:
        validate_host(args.host, args.allow_isolated_runtime)
        require(Path(args.docker).is_absolute(), '--docker must be absolute')
        args.out = args.out.resolve()
        require(CHECKOUT not in args.out.parents and args.out != CHECKOUT, 'evidence must be outside checkout')
        args.out.mkdir(mode=0o700, parents=True, exist_ok=False)
        args.out.chmod(0o700)
        if args.work_dir:
            args.work_dir = args.work_dir.resolve()
            require(CHECKOUT not in args.work_dir.parents and args.work_dir != CHECKOUT, 'work directory must be outside checkout')
            require(',' not in str(args.work_dir), 'commas unsupported in bind path')
            args.work_dir.mkdir(mode=0o755, parents=True, exist_ok=False)
            args.work_dir.chmod(0o755)
            marker = args.work_dir / 'marker'
            marker.write_text('synthetic-bind-marker'); marker.chmod(0o444)
    except (ValueError, AssertionError, OSError) as error:
        parser.error(str(error))
    verifier = Verifier(args)
    code = 0
    old = signal.signal(signal.SIGTERM, lambda *_: (_ for _ in ()).throw(KeyboardInterrupt()))
    try:
        verifier.execute()
    except KeyboardInterrupt:
        code = 130
        verifier.checks.append({'check': 'interruption', 'status': 'blocked'})
    except (OSError, subprocess.TimeoutExpired, DockerError) as error:
        code = 2 if not any(c['check'] == 'explicit_isolated_linux_engine' for c in verifier.checks) else 1
        verifier.checks.append({'check': 'execution', 'status': 'blocked' if code == 2 else 'failed', 'reason': str(error)})
    except Exception as error:
        code = 1
        verifier.checks.append({'check': 'execution', 'status': 'failed', 'reason': str(error)})
    finally:
        # Finish bounded owned-resource cleanup even when a second interruption arrives.
        previous_int = signal.signal(signal.SIGINT, signal.SIG_IGN)
        signal.signal(signal.SIGTERM, signal.SIG_IGN)
        if not verifier.cleanup() and code == 0:
            code = 1
        if args.work_dir:
            try:
                (args.work_dir / 'marker').unlink(missing_ok=True)
                args.work_dir.rmdir()
            except OSError as error:
                verifier.checks.append({'check': 'synthetic_bind_fixture_cleanup', 'status': 'failed', 'reason': str(error)})
                if code == 0:
                    code = 1
        summary = {'scope': 'actual Dockerfile build and isolated foundation container only',
                   'run_id': verifier.run_id, 'host': args.host, 'checks': verifier.checks,
                   'retained_images': verifier.retained_images,
                   'not_executed': ['production deployment, secrets, external database and OSS',
                                    'host bind-mounted private key ownership (synthetic named volume tested)',
                                    'active native/web worker drain and in-flight request drain',
                                    'OOM kill, heavy CPU load, tmpfs exhaustion, vulnerability scan'],
                   'exit_code': code}
        path = args.out / 'summary.json'
        path.write_text(json.dumps(summary, indent=2) + '\n'); path.chmod(0o600)
        print('Evidence: ' + str(path))
        signal.signal(signal.SIGTERM, old)
        signal.signal(signal.SIGINT, previous_int)
    return code


if __name__ == '__main__':
    raise SystemExit(main())
