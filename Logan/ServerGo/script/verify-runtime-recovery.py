#!/usr/bin/env python3
"""Opt-in isolated Docker signal/recovery proof using a compiled runtimefault test binary."""
import argparse
import importlib.util
import json
import os
from pathlib import Path
import signal
import subprocess
import tempfile
import time

spec = importlib.util.spec_from_file_location('container_verifier', Path(__file__).with_name('verify-containers.py'))
base = importlib.util.module_from_spec(spec)
spec.loader.exec_module(base)
require = base.require


def classify_exit(error, engine_ready):
    if isinstance(error, KeyboardInterrupt):
        return 130
    if isinstance(error, (OSError, subprocess.TimeoutExpired, base.DockerError)) and not engine_ready:
        return 2
    return 1


def exact_barrier(output, marker):
    return marker in output.splitlines()


def web_fixture_content():
    return json.dumps([json.dumps({'t': '1', 'c': 'web%20recovered', 'd': '1700000000000'}, separators=(',', ':'))], separators=(',', ':'))


class RuntimeVerifier(base.Verifier):
    def __init__(self, args):
        super().__init__(args)
        self.password = os.urandom(24).hex()
        self.engine_ready = False

    def log(self, command, output, code):
        super().log([s.replace(self.password, '<synthetic-secret>') for s in command], output.replace(self.password, '<synthetic-secret>'), code)

    def private(self, name, content):
        path = self.args.out / name
        path.touch(mode=0o600, exist_ok=False)
        path.write_text(content)
        return path

    def wait_marker(self, container, marker, timeout=60):
        deadline = time.monotonic() + timeout
        while time.monotonic() < deadline:
            _, logs = self.docker('logs', container, timeout=max(.1, min(10, deadline - time.monotonic())))
            if exact_barrier(logs, marker):
                return
            _, state = self.docker('inspect', '--format', '{{.State.Running}}', container, timeout=max(.1, min(10, deadline - time.monotonic())))
            require(state.strip() == 'true', 'container exited before exact barrier ' + marker)
            time.sleep(.2)
        raise AssertionError('timed out waiting for exact barrier ' + marker)

    def running(self, container):
        _, state = self.docker('inspect', '--format', '{{.State.Running}}', container)
        require(state.strip() == 'true', 'barrier-blocked process must still run after TERM')

    def wait_exit(self, container, expected):
        _, value = self.docker('wait', container, timeout=70)
        self.docker('inspect', container)
        self.docker('logs', container)
        require(value.strip() == str(expected), 'unexpected container exit: ' + value.strip())

    def sql(self, query):
        _, result = self.docker('exec', '--env', 'MYSQL_PWD=' + self.password, self.mysql,
                                'mysql', '--protocol=TCP', '--host=127.0.0.1', '--user=root', '--batch', '--skip-column-names', '-e', query)
        return result.strip()

    def launch(self, suffix, image, command=(), env=None):
        name = self.reserve('container', self.prefix + '-' + suffix)
        options = ['run', '-d', '--name', name, '--label', base.LABEL + '=' + self.run_id,
                   '--network', self.network, '--read-only', '--cap-drop', 'ALL',
                   '--security-opt', 'no-new-privileges:true', '--memory', '512m', '--cpus', '1',
                   '--pids-limit', '128', '--user', '10001:10001',
                   '--tmpfs', '/tmp:rw,noexec,nosuid,nodev,size=67108864,uid=10001,gid=10001,mode=0700']
        if env:
            options += ['--env-file', str(self.private(suffix + '.env', '\n'.join(env) + '\n'))]
        self.docker(*options, image, *command)
        return name

    def request(self, target, port, path):
        script = 'import urllib.request; r=urllib.request.urlopen(' + repr('http://' + target + ':' + str(port) + path) + ',timeout=5); print(r.status); print(r.read().decode())'
        helper = self.launch('probe-' + str(self.serial), 'python:3.12-alpine', ('python', '-c', script))
        self.wait_exit(helper, 0)

    def worker(self, kind, mode, db, suffix):
        return self.launch(suffix, self.tag, env=['LOGAN_RUNTIME_ALLOW_WRITES=1', 'LOGAN_RUNTIME_MODE=' + kind + '-' + mode,
            'LOGAN_RUNTIME_TASK_ID=1', 'LOGAN_RUNTIME_MYSQL_DSN=root:' + self.password + '@tcp(' + self.mysql + ':3306)/' + db + '?parseTime=true', 'GOMAXPROCS=1', 'TMPDIR=/tmp'])

    def snapshot(self, kind, db):
        table = 'logan_log_detail' if kind == 'native' else 'web_detail'
        value = self.sql('USE ' + db + '; SELECT state,fence,attempts,LENGTH(owner),(SELECT COUNT(*) FROM ' + table + ' WHERE task_id=1) FROM logan_processing_job WHERE kind=\'' + kind + '\' AND task_id=1;')
        return value.split('\t')

    def fixture(self, suffix):
        db = 'logan_it_' + self.run_id + '_' + suffix.replace('-', '_')
        schema = '\n'.join((base.ROOT / p).read_text() for p in ('tests/integration/mysql/java_schema.sql', 'migrations/001_web_processing.sql', 'migrations/002_maintenance.sql'))
        content_hex = web_fixture_content().encode('utf-8').hex()
        self.sql('CREATE DATABASE ' + db + ';\nUSE ' + db + ';\n' + schema + "\nINSERT INTO logan_task(id,platform,log_file_name,add_time) VALUES(1,1,'synthetic',1); INSERT INTO web_task(id,page_num,content,add_time,log_date,status) VALUES(1,1,CONVERT(0x" + content_hex + " USING utf8mb4),1,1,0);")
        return db

    def execute(self):
        _, info = self.docker('info', '--format', '{{json .}}')
        require(json.loads(info)['OSType'] == 'linux', 'Linux engine required')
        self.engine_ready = True
        self.checks.append({'check': 'explicit_isolated_linux_engine', 'status': 'passed'})
        # Pull failures occur before any owned image is reserved or built.
        self.docker('image', 'inspect', 'mysql:8.4.11')
        self.docker('image', 'inspect', 'python:3.12-alpine')
        with tempfile.TemporaryDirectory(prefix='logan-runtime-context-') as context:
            import shutil
            shutil.copyfile(self.args.binary, Path(context) / 'runtimefault.test')
            (Path(context) / 'runtimefault.test').chmod(0o555)
            (Path(context) / 'Dockerfile').write_text('FROM scratch\nCOPY runtimefault.test /runtimefault.test\nUSER 10001:10001\nENTRYPOINT ["/runtimefault.test","-test.run=^TestRuntimeFaultContainerEntry$","-test.v","-test.timeout=100s"]\n')
            self.reserve('image', self.tag)
            self.docker('build', '--label', base.LABEL + '=' + self.run_id, '-t', self.tag, context, timeout=120)
        self.network = self.reserve('network', self.prefix + '-net')
        self.docker('network', 'create', '--internal', '--label', base.LABEL + '=' + self.run_id, self.network)
        self.mysql = self.reserve('container', self.prefix + '-mysql')
        mysql_env = self.private('mysql.env', 'MYSQL_ROOT_PASSWORD=' + self.password + '\nMYSQL_ROOT_HOST=%\n')
        self.docker('run', '-d', '--name', self.mysql, '--label', base.LABEL + '=' + self.run_id, '--network', self.network,
            '--read-only', '--user', '999:999', '--cap-drop', 'ALL', '--security-opt', 'no-new-privileges:true', '--memory', '1g', '--cpus', '1', '--pids-limit', '256',
            '--tmpfs', '/var/lib/mysql:rw,nosuid,nodev,size=536870912,uid=999,gid=999', '--tmpfs', '/var/run/mysqld:rw,nosuid,nodev,size=16777216,uid=999,gid=999',
            '--tmpfs', '/tmp:rw,noexec,nosuid,nodev,size=67108864,uid=999,gid=999', '--env-file', str(mysql_env), 'mysql:8.4.11')
        deadline = time.monotonic() + 90
        while True:
            try:
                if self.sql('SELECT 1') == '1':
                    break
            except base.DockerError:
                pass
            require(time.monotonic() < deadline, 'MySQL readiness timeout')
            time.sleep(.5)
        def http_drain():
            app = self.launch('http', self.tag, env=['LOGAN_RUNTIME_ALLOW_WRITES=1', 'LOGAN_RUNTIME_MODE=http-drain', 'TMPDIR=/tmp'])
            self.wait_marker(app, 'RUNTIME_STARTING http-drain')
            code = "import urllib.request,urllib.error,time\nfor i in range(50):\n try:\n  r=urllib.request.urlopen('http://" + app + ":8080/fault/slow',timeout=30)\n  if r.status!=200 or r.read()!=b'drained-complete': raise RuntimeError('incomplete HTTP response')\n  print('HTTP_COMPLETE'); break\n except urllib.error.URLError: time.sleep(.1)\nelse: raise RuntimeError('no response')"
            client = self.launch('http-client', 'python:3.12-alpine', ('python', '-c', code))
            self.wait_marker(app, 'HTTP_ENTERED')
            self.docker('kill', '--signal', 'TERM', app)
            time.sleep(.5)
            self.running(app)
            self.request(app, 8081, '/release')
            self.wait_exit(client, 0)
            self.wait_marker(client, 'HTTP_COMPLETE')
            self.wait_exit(app, 0)
        self.check('active_http_term_drain', http_drain)
        for kind in ('native', 'web'):
            def drain(kind=kind):
                db = self.fixture(kind + '_drain')
                app = self.worker(kind, 'drain', db, kind + '-drain')
                self.wait_marker(app, 'PARSER_ENTERED')
                self.docker('kill', '--signal', 'TERM', app)
                time.sleep(.5)
                self.running(app)
                self.request(app, 8081, '/release')
                self.wait_exit(app, 0)
                require(self.snapshot(kind, db) == ['done', '1', '1', '0', '1'], 'drain publication snapshot mismatch')
            self.check(kind + '_active_term_drain', drain)
            def recover(kind=kind):
                db = self.fixture(kind + '_kill')
                app = self.worker(kind, 'block', db, kind + '-block')
                self.wait_marker(app, 'PARSER_ENTERED')
                require(self.snapshot(kind, db) == ['running', '1', '1', '32', '0'], 'pre-kill snapshot mismatch')
                remaining = int(self.sql('USE ' + db + "; SELECT lease_until-CAST(UNIX_TIMESTAMP(CURRENT_TIMESTAMP(3))*1000 AS SIGNED) FROM logan_processing_job WHERE kind='" + kind + "' AND task_id=1"))
                require(20000 <= remaining <= 31000, 'expected real production 30 second lease')
                lease_start = time.monotonic()
                self.docker('kill', '--signal', 'KILL', app)
                self.wait_exit(app, 137)
                require(self.snapshot(kind, db) == ['running', '1', '1', '32', '0'], 'killed worker partially published')
                replacement = self.worker(kind, 'recover', db, kind + '-recover')
                self.wait_marker(replacement, kind.upper() + '_RECOVERED', 65)
                self.wait_exit(replacement, 0)
                require(time.monotonic() - lease_start >= remaining / 1000 - 1, 'replacement bypassed the original lease')
                require(self.snapshot(kind, db) == ['done', '2', '2', '0', '1'], 'recovery publication snapshot mismatch')
                table = 'logan_log_detail' if kind == 'native' else 'web_detail'
                ids = self.sql('USE ' + db + '; SELECT id FROM ' + table + ' WHERE task_id=1')
                repeat = self.worker(kind, 'recover', db, kind + '-repeat')
                self.wait_exit(repeat, 0)
                require(ids == self.sql('USE ' + db + '; SELECT id FROM ' + table + ' WHERE task_id=1'), 'repeat delivery changed publication IDs')
                require(self.snapshot(kind, db) == ['done', '2', '2', '0', '1'], 'repeat delivery changed job state')
            self.check(kind + '_sigkill_lease_recovery', recover)


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--docker', required=True)
    parser.add_argument('--host', required=True)
    parser.add_argument('--allow-isolated-runtime', action='store_true')
    parser.add_argument('--binary', type=Path, required=True)
    parser.add_argument('--out', type=Path, required=True)
    args = parser.parse_args(argv)
    try:
        base.validate_host(args.host, args.allow_isolated_runtime)
        require(Path(args.docker).is_absolute(), '--docker must be absolute')
        require(args.binary.is_absolute() and args.binary.is_file(), '--binary must be an existing absolute Linux test binary')
        args.out = args.out.resolve()
        require(args.out != base.CHECKOUT and base.CHECKOUT not in args.out.parents, 'evidence must be outside checkout')
        args.out.mkdir(mode=0o700, parents=True, exist_ok=False)
    except (ValueError, AssertionError, OSError) as error:
        parser.error(str(error))
    verifier = RuntimeVerifier(args)
    code = 0
    old = signal.signal(signal.SIGTERM, lambda *_: (_ for _ in ()).throw(KeyboardInterrupt()))
    try:
        verifier.execute()
    except BaseException as error:
        code = classify_exit(error, verifier.engine_ready)
        verifier.checks.append({'check': 'execution', 'status': 'blocked' if code == 2 else 'failed', 'reason': str(error)})
    finally:
        previous = signal.signal(signal.SIGINT, signal.SIG_IGN)
        signal.signal(signal.SIGTERM, signal.SIG_IGN)
        if not verifier.cleanup() and code == 0:
            code = 1
        for path in args.out.glob('*.env'):
            path.unlink()
        (args.out / 'summary.json').write_text(json.dumps({'exit_code': code, 'run_id': verifier.run_id, 'checks': verifier.checks,
             'scope': 'actual container signals, real MySQL leases, production workers; synthetic storage only; no cloud OSS'}, indent=2) + '\n')
        (args.out / 'summary.json').chmod(0o600)
        signal.signal(signal.SIGTERM, old)
        signal.signal(signal.SIGINT, previous)
    print('Evidence: ' + str(args.out / 'summary.json'))
    return code


if __name__ == '__main__':
    raise SystemExit(main())
