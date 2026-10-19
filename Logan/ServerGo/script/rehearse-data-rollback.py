#!/usr/bin/env python3
"""Original Java services/mappers -> Go data services -> Java on disposable MySQL.

This is a data-layer rehearsal, not full HTTP/Tomcat/OSS or production cutover.
"""
import argparse
import importlib.util
import json
import os
from pathlib import Path
import signal
import time

spec = importlib.util.spec_from_file_location('runtime_recovery', Path(__file__).with_name('verify-runtime-recovery.py'))
runtime = importlib.util.module_from_spec(spec)
spec.loader.exec_module(runtime)
base = runtime.base
require = base.require


class DataRehearsal(runtime.RuntimeVerifier):
    def launch_phase(self, phase):
        name = self.reserve('container', self.prefix + '-' + phase)
        uid = str(os.getuid())
        env = ['CROSSRUNTIME_DISPOSABLE=YES', 'CROSSRUNTIME_DIR=/evidence', 'TMPDIR=/tmp', 'TZ=Asia/Shanghai']
        if phase.startswith('java'):
            image = 'eclipse-temurin:17-jre'
            env += ['CROSSRUNTIME_PHASE=' + ('seed' if phase == 'java-seed' else 'verify'),
                    'CROSSRUNTIME_JDBC_URL=jdbc:mysql://' + self.mysql + ':3306/' + self.db + '?useSSL=false&allowPublicKeyRetrieval=true&serverTimezone=Asia/Shanghai',
                    'CROSSRUNTIME_DB_USER=root', 'CROSSRUNTIME_DB_PASSWORD=' + self.password]
            extra = ['--mount', 'type=bind,src=' + str(self.args.java_bundle) + ',dst=/java,readonly', '-w', '/java']
            command = ['sh', '/java/run.sh']
        else:
            image = self.tag
            env += ['CROSSRUNTIME_DSN=root:' + self.password + '@tcp(' + self.mysql + ':3306)/' + self.db + '?parseTime=true', 'GOMAXPROCS=1']
            extra, command = [], []
        self.docker('run', '-d', '--name', name, '--label', base.LABEL + '=' + self.run_id,
                    '--network', self.network, '--user', uid + ':' + uid, '--read-only', '--cap-drop', 'ALL',
                    '--security-opt', 'no-new-privileges:true', '--memory', '768m', '--cpus', '1', '--pids-limit', '128',
                    '--tmpfs', '/tmp:rw,nosuid,nodev,size=134217728,uid=' + uid + ',gid=' + uid + ',mode=0700',
                    '--mount', 'type=bind,src=' + str(self.shared) + ',dst=/evidence',
                    '--env-file', str(self.private(phase + '.env', '\n'.join(env) + '\n')),
                    *extra, image, *command)
        self.events.append({'phase': phase, 'event': 'started', 'unix_time': time.time()})
        self.wait_exit(name, 0)
        _, logs = self.docker('logs', name)
        if phase.startswith('java'):
            marker = 'JAVA_SEED_VERIFIED' if phase == 'java-seed' else 'JAVA_ROLLBACK_VERIFIED'
            require('OK (1 test)' in logs and marker in logs, 'Java phase must execute exactly one non-skipped test')
        else:
            require('GO_DATA_ROUNDTRIP_VERIFIED' in logs and '\nPASS\n' in logs and 'SKIP' not in logs, 'Go data test must execute, not skip')
        _, running = self.docker('inspect', '--format', '{{.State.Running}}', name)
        require(running.strip() == 'false', 'old phase must stop before starting next phase')
        self.events.append({'phase': phase, 'event': 'stopped_exit0', 'unix_time': time.time()})

    def execute(self):
        _, info = self.docker('info', '--format', '{{json .}}')
        require(json.loads(info)['OSType'] == 'linux', 'Linux engine required')
        self.engine_ready = True
        for image in ('mysql:8.4.11', 'eclipse-temurin:17-jre'):
            self.docker('image', 'inspect', '--format', '{{.Id}}', image)
        self.shared = self.args.work_dir / 'synthetic-data'
        self.shared.mkdir(mode=0o700)
        context = self.args.work_dir / 'context'
        context.mkdir(mode=0o700)
        import shutil
        shutil.copyfile(self.args.binary, context / 'crossruntime.test')
        (context / 'crossruntime.test').chmod(0o555)
        (context / 'Dockerfile').write_text('FROM scratch\nCOPY crossruntime.test /crossruntime.test\nENTRYPOINT ["/crossruntime.test","-test.v","-test.run=^TestOriginalJavaGoJavaDataRoundTrip$","-test.timeout=100s"]\n')
        self.reserve('image', self.tag)
        self.docker('build', '--label', base.LABEL + '=' + self.run_id, '-t', self.tag, str(context), timeout=120)
        self.network = self.reserve('network', self.prefix + '-net')
        self.docker('network', 'create', '--internal', '--label', base.LABEL + '=' + self.run_id, self.network)
        self.mysql = self.reserve('container', self.prefix + '-mysql')
        mysql_env = self.private('mysql.env', 'MYSQL_ROOT_PASSWORD=' + self.password + '\nMYSQL_ROOT_HOST=%\n')
        self.docker('run', '-d', '--name', self.mysql, '--label', base.LABEL + '=' + self.run_id,
                    '--network', self.network, '--read-only', '--user', '999:999', '--cap-drop', 'ALL',
                    '--security-opt', 'no-new-privileges:true', '--memory', '1g', '--cpus', '1', '--pids-limit', '256',
                    '--tmpfs', '/var/lib/mysql:rw,nosuid,nodev,size=536870912,uid=999,gid=999',
                    '--tmpfs', '/var/run/mysqld:rw,nosuid,nodev,size=16777216,uid=999,gid=999',
                    '--tmpfs', '/tmp:rw,noexec,nosuid,nodev,size=67108864,uid=999,gid=999',
                    '--env-file', str(mysql_env), 'mysql:8.4.11')
        deadline = time.monotonic() + 90
        while True:
            try:
                if self.sql('SELECT 1') == '1':
                    break
            except base.DockerError:
                pass
            require(time.monotonic() < deadline, 'MySQL readiness timeout')
            time.sleep(.5)
        self.db = 'logan_crossruntime_' + self.run_id
        schema = '\n'.join((base.ROOT / p).read_text() for p in ('tests/integration/mysql/java_schema.sql', 'migrations/001_web_processing.sql', 'migrations/002_maintenance.sql'))
        self.sql('CREATE DATABASE ' + self.db + ';\nUSE ' + self.db + ';\n' + schema + '\n')
        self.events = [{'event': 'ENTRY_CLOSED_NO_GATEWAY', 'unix_time': time.time()}]
        self.check('original_java_seed_real_mysql', lambda: self.launch_phase('java-seed'))
        require((self.shared / 'java-seed.json').is_file(), 'Java seed evidence missing')
        self.check('go_reads_and_processes_java_data', lambda: self.launch_phase('go-process'))
        require((self.shared / 'go-produced.json').is_file(), 'Go publication evidence missing')
        self.check('original_java_reads_go_publications_on_rollback', lambda: self.launch_phase('java-verify'))
        _, result = self.docker('logs', self.prefix + '-java-verify')
        require('OK (1 test)' in result and 'JAVA_ROLLBACK_VERIFIED' in result, 'Java verification must actually execute')
        (self.args.out / 'event-ledger.json').write_text(json.dumps(self.events, indent=2))
        for name in ('java-seed.json', 'go-produced.json', 'java-recheck.json'):
            target = self.args.out / name
            target.write_bytes((self.shared / name).read_bytes())
            target.chmod(0o600)
        if getattr(self.args, 'with_http', False):
            module_spec = importlib.util.spec_from_file_location('http_roundtrip', base.ROOT / 'tests/crossruntime/http_roundtrip.py')
            module = importlib.util.module_from_spec(module_spec)
            module_spec.loader.exec_module(module)
            self.check('actual_java_go_http_roundtrip', lambda: module.run_http_roundtrip(self))


def main(argv=None):
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('--docker', required=True)
    p.add_argument('--host', required=True)
    p.add_argument('--allow-isolated-runtime', action='store_true')
    p.add_argument('--binary', type=Path, required=True)
    p.add_argument('--java-bundle', type=Path, required=True)
    p.add_argument('--work-dir', type=Path, required=True, help='fresh /private/tmp path shared at identical guest path')
    p.add_argument('--out', type=Path, required=True)
    p.add_argument('--with-http', action='store_true', help='also run actual staged Java WAR and production Go HTTP read comparison')
    p.add_argument('--http-go-image', help='explicit preloaded actual Go production image for optional HTTP phase')
    a = p.parse_args(argv)
    try:
        base.validate_host(a.host, a.allow_isolated_runtime)
        require(Path(a.docker).is_absolute(), 'Docker CLI must be absolute')
        require(a.binary.is_absolute() and a.binary.is_file(), 'compiled Linux Go test binary required')
        require(not a.with_http or bool(a.http_go_image), '--with-http requires explicit --http-go-image')
        a.java_bundle = a.java_bundle.resolve()
        require((a.java_bundle / 'run.sh').is_file(), 'prepared original Java bundle required')
        a.work_dir = a.work_dir.resolve()
        require(Path('/private/tmp') in a.work_dir.parents and ',' not in str(a.work_dir), 'work path must be dedicated shared /private/tmp path')
        a.out = a.out.resolve()
        require(a.out != base.CHECKOUT and base.CHECKOUT not in a.out.parents, 'evidence outside checkout required')
        a.out.mkdir(mode=0o700, parents=True, exist_ok=False)
        a.work_dir.mkdir(mode=0o700, parents=True, exist_ok=False)
    except (ValueError, AssertionError, OSError) as e:
        p.error(str(e))
    verifier = DataRehearsal(a)
    previous = signal.signal(signal.SIGTERM, lambda *_: (_ for _ in ()).throw(KeyboardInterrupt()))
    code = 0
    try:
        verifier.execute()
    except BaseException as e:
        code = runtime.classify_exit(e, verifier.engine_ready)
        verifier.checks.append({'check': 'execution', 'status': 'blocked' if code == 2 else 'failed', 'reason': str(e).replace(verifier.password, '<synthetic-secret>')})
    finally:
        old_int = signal.signal(signal.SIGINT, signal.SIG_IGN)
        signal.signal(signal.SIGTERM, signal.SIG_IGN)
        if not verifier.cleanup() and code == 0:
            code = 1
        if hasattr(verifier, 'events'):
            ledger = a.out / 'event-ledger.json'
            ledger.write_text(json.dumps(verifier.events, indent=2)); ledger.chmod(0o600)
        for f in a.out.glob('*.env'):
            f.unlink()
        for fixture in (a.work_dir / 'http/logan-web.war', a.work_dir / 'http/secrets/fixture-rsa.pem'):
            fixture.unlink(missing_ok=True)
        summary = {'scope': 'original Java services/MyBatis and production Go data services on real isolated MySQL',
                   'checks': verifier.checks, 'exit_code': code, 'status': 'passed' if code == 0 else ('blocked' if code == 2 else 'failed'),
                   'not_executed': ([] if a.with_http and code == 0 else ['actual Java Tomcat/Go HTTP read subset']) + ['all 20 HTTP interfaces including upload/download under real OSS', 'real OSS and signed URL/RSA/SDK traffic', 'production switch or data restore']}
        f = a.out / 'summary.json'
        f.write_text(json.dumps(summary, indent=2)); f.chmod(0o600)
        signal.signal(signal.SIGTERM, previous)
        signal.signal(signal.SIGINT, old_int)
    print('Evidence: ' + str(f))
    return code


if __name__ == '__main__':
    raise SystemExit(main())
