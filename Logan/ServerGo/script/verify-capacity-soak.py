#!/usr/bin/env python3
"""Opt-in measured SDK capacity and one-hour serial Go/Java soak on owned Docker."""
import argparse
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import re
import signal
import subprocess
import sys
import time

ROOT = Path(__file__).resolve().parents[1]
APP_MEMORY = 1024 ** 3


def load(name, path):
    spec = importlib.util.spec_from_file_location(name, path)
    module = importlib.util.module_from_spec(spec); spec.loader.exec_module(module)
    return module


runtime = load('capacity_runtime', ROOT / 'script/verify-runtime-recovery.py')
base = runtime.base
measurement = load('capacity_measurement', ROOT / 'tests/load/measurement.py')
fixtures = load('capacity_fixtures', ROOT / 'tests/e2e20/fixtures.py')
require = base.require


def stage_plan(seconds, stage_seconds, soak_qps):
    if not 1 <= seconds <= 3660 or not 5 <= stage_seconds <= 120 or not 0 < soak_qps <= 10:
        raise ValueError('invalid bounded stage plan')
    def stage(name, fixture, mode, duration, qps, workers=16):
        return dict(name=name, fixture=fixture, mode=mode, seconds=duration, qps=qps, workers=workers, queue=32)
    result = [stage('protocol-warmup', 'protocol', 'mixed', 15, 1, 4)]
    result += [stage('read-' + str(q), 'protocol', 'read', stage_seconds, q, 32) for q in (50, 200, 500)]
    result += [stage('workflow-' + str(q), 'protocol', 'mixed', stage_seconds, q, 32) for q in (2, 10, 25)]
    result += [stage('sdk-' + str(q), 'sdk', 'mixed', stage_seconds, q) for q in (2, 10)]
    result.append(stage('soak-warmup', 'protocol', 'mixed', 15, 1, 4))
    result.append(stage('soak', 'sdk', 'mixed', seconds, soak_qps))
    return result


def runtime_order(selection):
    if selection == 'go': return ['go']
    if selection == 'both': return ['java', 'go']
    raise ValueError('unsupported runtime selection')


def runtime_images(selection, go_image):
    images=['mysql:8.4.11','python:3.12-alpine',go_image]
    if selection=='both': images.append('tomcat:9-jre17-temurin')
    runtime_order(selection)
    return images


def go_crypto_environment(legacy_rsa_compat):
    return ["GODEBUG=rsa1024min=0"] if legacy_rsa_compat else []


def validate_lock_errors(samples, final_database):
    if not samples: raise ValueError('database lock-error sample missing')
    for sample in [*samples, {'database':final_database}]:
        codes=sample['database'].get('mysql_error_counts',{})
        if set(codes)!={'1213','1205'} or any(codes.values()):
            raise ValueError('database deadlocks/lock timeouts or missing error counters')
    return True


def host_window(seconds, controller_elapsed, sample_span):
    # Host observations are authoritative; guest Go monotonic time can drift in VZ.
    if seconds >= 3600:
        if controller_elapsed < 3600 or sample_span - 20 < 3600:
            raise ValueError('host-observed active lower bound shorter than one hour')
        return sample_span - 20
    if controller_elapsed < seconds:
        raise ValueError('controller observed shorter than requested wall-clock window')
    return controller_elapsed


def validate_data(counts, runtime_name, fixture_mode, native_records, web_records):
    for flow, expected in [('native', native_records), ('web', web_records)]:
        if runtime_name == 'java' and fixture_mode == 'sdk' and flow == 'native':
            expected = 1
        value = counts[flow]
        if value['tasks'] == 0 or value['pending'] or value['valid_tasks'] != value['tasks'] or value['details'] != value['tasks'] * expected:
            raise ValueError('incomplete or incorrect persisted ' + flow + ' content')
    return True


def assess_soak(report, runtime_name, seconds):
    measurement.validate_report(report, seconds)
    errors = measurement.classify_errors(report, runtime_name, 'sdk')
    if report['dropped'] or report.get('report_write_errors', 0) or errors['unexpected_errors']:
        raise ValueError('soak dropped work, lost reports or had unexpected errors')
    workflow_errors = report['workflows'].get('ErrorKinds') or {}
    if any(key != 'native_content_incomplete' or runtime_name != 'java' for key in workflow_errors):
        raise ValueError('unexpected workflow errors')
    if report['workflows']['Errors'] != errors['known_content_errors']:
        raise ValueError('workflow and HTTP semantic error counts differ')
    return dict(stability_accepted=True, strict_functional_passed=errors['strict_errors'] == 0, **errors)


class Capacity(runtime.RuntimeVerifier):
    def log(self, command, output, code):
        super().log([s.replace(self.password, '<synthetic-secret>') for s in command],
                    output.replace(self.password, '<synthetic-secret>'), code)

    def dump(self, name, value):
        p = self.args.out / (name + '.json')
        p.write_text(json.dumps(value, ensure_ascii=False, indent=2) + '\n'); p.chmod(0o600)
        return value

    def validate_and_record_stage(self, name, evidence, validate):
        try:
            validate()
        except (ValueError, AssertionError) as error:
            evidence['validation_error']=str(error)
            evidence['assessment']={'stability_accepted':False,'strict_functional_passed':False}
            raise
        finally:
            self.dump(name,evidence)

    def sql(self, statement):
        _, text = self.docker('exec', '--env', 'MYSQL_PWD=' + self.password, self.mysql,
                             'mysql', '--protocol=TCP', '--host=127.0.0.1', '--user=root', '--batch',
                             '--skip-column-names', '--default-character-set=utf8mb4', '-e', statement)
        return text.strip()

    def common(self, cpus='1', memory='1g'):
        return ['--label', base.LABEL + '=' + self.run_id, '--network', self.network,
                '--read-only', '--cap-drop', 'ALL', '--security-opt', 'no-new-privileges:true',
                '--cpus', cpus, '--memory', memory, '--memory-swap', memory, '--pids-limit', '256',
                '--log-opt', 'max-size=10m', '--log-opt', 'max-file=2']

    def preflight_fixture(self, kind, path):
        name = self.reserve('container', self.prefix + '-v-' + kind)
        _, raw = self.docker('run', '--rm', '--name', name, '--label', base.LABEL + '=' + self.run_id,
            '--network', 'none', '--read-only', '--cap-drop', 'ALL', '--security-opt', 'no-new-privileges:true',
            '--cpus', '.5', '--memory', '256m', '--memory-swap', '256m', '--pids-limit', '64', '--user', self.uid,
            '--mount', 'type=bind,src=' + str(self.args.binary) + ',dst=/loader,readonly',
            '--mount', 'type=bind,src=' + str(path) + ',dst=/fixture.json,readonly', '--entrypoint', '/loader', self.args.go_image,
            '--base-url', 'http://127.0.0.1:9', '--fixture', '/fixture.json', '--allow-test-target', '--allow-writes',
            '--test-target-marker', 'logan-load-test', '--validate-only')
        result = json.loads(raw)
        require(result == dict(validated=True, native_records=len(self.fixture_data[kind]['nativeContents']), web_records=len(self.fixture_data[kind]['webContents'])), 'offline fixture validation failed')
        return result

    def setup_runtime(self, mode):
        self.mode = mode
        self.mysql = self.reserve('container', self.prefix + '-' + mode[0] + '-db')
        volume = self.reserve('volume', self.prefix + '-' + mode[0] + '-data')
        self.owned.remove(('volume', volume)); self.owned.insert(0, ('volume', volume))
        self.docker('volume', 'create', '--label', base.LABEL + '=' + self.run_id, volume)
        self.docker('run', '-d', '--name', self.mysql, *self.common(memory=getattr(self.args, 'mysql_memory', '1g')), '--user', '999:999',
                    '--mount', 'type=volume,src=' + volume + ',dst=/var/lib/mysql',
                    '--tmpfs', '/var/run/mysqld:rw,uid=999,gid=999,size=16777216',
                    '--tmpfs', '/tmp:rw,size=67108864', '--env-file', str(self.private(mode + '-mysql.env',
                        'MYSQL_ROOT_PASSWORD=' + self.password + '\nMYSQL_ROOT_HOST=%\n')),
                    'mysql:8.4.11', '--innodb-buffer-pool-size=268435456', '--max-connections=128', '--long-query-time=1',
                    '--innodb-buffer-pool-dump-at-shutdown=OFF', '--innodb-buffer-pool-load-at-startup=OFF', *getattr(self.args, 'mysql_extra_args', []))
        deadline = time.monotonic() + 150
        while True:
            try:
                if self.sql('SELECT 1') == '1': break
            except base.DockerError: pass
            require(time.monotonic() < deadline, 'isolated MySQL TCP readiness timeout'); time.sleep(.5)
        self.db = 'logan_reparse_it_' + self.run_id + mode[0]
        schema = '\n'.join((ROOT / p).read_text() for p in ('tests/integration/mysql/java_schema.sql',
                           'migrations/001_web_processing.sql', 'migrations/002_maintenance.sql'))
        self.sql('CREATE DATABASE ' + self.db + '; USE ' + self.db + ';\n' + schema)
        self.oss = self.reserve('container', self.prefix + '-' + mode[0] + '-o')
        self.docker('run', '-d', '--name', self.oss, *self.common('.25', '128m'),
                    '--network-alias', 'fixture.' + self.oss, '--user', self.uid,
                    '--mount', 'type=bind,src=' + str(self.args.work_dir / 'oss_fixture.py') + ',dst=/fixture.py,readonly',
                    'python:3.12-alpine', 'python', '-u', '/fixture.py')
        self.app = self.reserve('container', self.prefix + '-' + mode)
        tmp = ['--tmpfs', '/tmp:rw,nosuid,nodev,size=536870912,uid=' + str(os.getuid()) + ',gid=' + str(os.getgid()) + ',mode=0700']
        if mode == 'java':
            self.war = self.args.work_dir / 'java.war'
            env = {**self.env, 'CROSSRUNTIME_DISPOSABLE': 'YES',
                   'CROSSRUNTIME_JDBC_URL': 'jdbc:mysql://' + self.mysql + ':3306/' + self.db + '?useSSL=false&allowPublicKeyRetrieval=true&serverTimezone=Asia/Shanghai',
                   'CROSSRUNTIME_DB_USER': 'root', 'CROSSRUNTIME_DB_PASSWORD': self.password,
                   'CROSSRUNTIME_OSS_EMULATOR_ENDPOINT': 'http://' + self.oss + ':8080'}
            subprocess.run([sys.executable, str(ROOT / 'tests/crossruntime/build_java_war.py'), '--output', str(self.war)],
                           env=env, stdout=subprocess.PIPE, stderr=subprocess.PIPE, check=True, timeout=90)
            for path, size in (('/data/applogs/logan-web', 16777216), ('/usr/local/tomcat/conf/Catalina/localhost', 16777216),
                               ('/usr/local/tomcat/webapps', 268435456), ('/usr/local/tomcat/work', 67108864),
                               ('/usr/local/tomcat/temp', 67108864), ('/usr/local/tomcat/logs', 67108864)):
                tmp += ['--tmpfs', path + ':rw,nosuid,nodev,size=' + str(size) + ',uid=' + str(os.getuid()) + ',gid=' + str(os.getgid())]
            options = [*tmp, '--mount', 'type=bind,src=' + str(self.war) + ',dst=/fixture/app.war,readonly',
                       '-e', 'TZ=Asia/Shanghai', '-e', 'JAVA_TOOL_OPTIONS=-Xmx768m -XX:ActiveProcessorCount=1 --add-opens=java.base/java.util=ALL-UNNAMED -Duser.timezone=Asia/Shanghai -Xlog:gc*:file=/tmp/gc.log:time,uptime,level,tags:filecount=2,filesize=10M',
                       'tomcat:9-jre17-temurin', 'sh', '-c', 'cp /fixture/app.war /usr/local/tomcat/webapps/logan-web.war && exec catalina.sh run']
        else:
            f = self.fixture_data['sdk']
            values = ['LOGAN_ENV=production', 'LOGAN_DB_ENABLED=true',
                      'LOGAN_MYSQL_DSN=root:' + self.password + '@tcp(' + self.mysql + ':3306)/' + self.db + '?parseTime=true&charset=utf8mb4',
                      'LOGAN_NATIVE_ENABLED=true', 'LOGAN_NATIVE_QUERY_ENABLED=true', 'LOGAN_WEB_ENABLED=true',
                      'LOGAN_NATIVE_AES_KEY=' + f['nativeKey'], 'LOGAN_NATIVE_AES_IV=' + f['nativeIV'],
                      'LOGAN_WEB_RSA_PRIVATE_KEY_FILE=/fixture/key.pem', 'LOGAN_NATIVE_TEMP_DIR=/tmp',
                      'LOGAN_OSS_ENDPOINT=http://' + self.oss + ':8080', 'LOGAN_OSS_REGION=fixture',
                      'LOGAN_OSS_BUCKET=fixture', 'LOGAN_OSS_PREFIX=logan-logs/', 'LOGAN_OSS_PATH_STYLE=true',
                      'LOGAN_OSS_ACCESS_KEY_ID=fixture-only', 'LOGAN_OSS_ACCESS_KEY_SECRET=fixture-only',
                      'LOGAN_MAINTENANCE_ENABLED=false', 'LOGAN_METRICS_ENABLED=true', 'GOMAXPROCS=1', 'GOMEMLIMIT=768MiB']
            values += [key + '=' + value for key, value in getattr(self.args, 'go_pool_env', {}).items()]
            values += go_crypto_environment(getattr(self.args, 'legacy_rsa_compat', False))
            options = [*tmp, '--mount', 'type=bind,src=' + str(self.args.sdk_fixtures / 'test-rsa-private.pem') + ',dst=/fixture/key.pem,readonly',
                       '--env-file', str(self.private(mode + '-app.env', '\n'.join(values) + '\n')), self.args.go_image]
        self.docker('run', '-d', '--name', self.app, *self.common(), '--user', self.uid, *options)
        self.events.append({'runtime': mode, 'event': 'started', 'utc': time.time()})
        _, inspected = self.docker('inspect', self.app, self.mysql, self.oss)
        settings = []
        for item in json.loads(inspected):
            config = item['HostConfig']
            require(not config.get('PortBindings') and config['ReadonlyRootfs'], 'benchmark container exposed a port or writable root')
            if item['Name'].lstrip('/') in (self.app, self.mysql):
                expected_memory = getattr(self.args, 'mysql_memory_bytes', APP_MEMORY) if item['Name'].lstrip('/') == self.mysql else APP_MEMORY
                require(config['Memory'] == expected_memory and config['NanoCpus'] == 1000000000 and config['MemorySwap'] == expected_memory, 'actual app/DB resource limits differ')
            settings.append(dict(name=item['Name'], image_id=item['Image'], memory=config['Memory'], memory_swap=config['MemorySwap'], nano_cpus=config['NanoCpus'], readonly_root=config['ReadonlyRootfs'], ports=config.get('PortBindings')))
        self.dump(mode + '-actual-settings', settings)
        deadline = time.monotonic() + 120
        while True:
            code, text = self.http('/logan/meta/logtypes.json')
            if code == 200 and json.loads(text).get('code') == 200: break
            require(time.monotonic() < deadline, 'application readiness timeout'); time.sleep(1)
        if mode == 'go': require(self.http('/readyz', context=False)[0] == 503, 'readiness must remain blocked')

    def http(self, path, context=True):
        url = 'http://' + self.app + ':8080' + ('/logan-web' if context else '') + path
        code = ('import urllib.request,urllib.error,json;\ntry:\n r=urllib.request.urlopen(' + repr(url) + ',timeout=3)\nexcept urllib.error.HTTPError as e:r=e\nexcept OSError:print(json.dumps([0,""]));raise SystemExit(0)\nprint(json.dumps([r.status,r.read(2097152).decode()]))')
        _, raw = self.docker('exec', self.oss, 'python', '-c', code)
        return json.loads(raw)

    def db_sample(self):
        status = self.sql("SHOW GLOBAL STATUS WHERE Variable_name IN ('Threads_running','Threads_connected','Slow_queries','Questions','Innodb_row_lock_waits','Innodb_row_lock_time','Innodb_buffer_pool_reads')")
        values = {line.split('\t')[0]: int(line.split('\t')[1]) for line in status.splitlines()}
        for table, key in [('logan_task', 'native'), ('web_task', 'web')]:
            raw = self.sql('SELECT COUNT(*),COALESCE(SUM(status=0),0) FROM ' + self.db + '.' + table).split('\t')
            values[key + '_tasks'], values[key + '_pending'] = map(int, raw)
        jobs = self.sql('SELECT state,COUNT(*) FROM ' + self.db + '.logan_processing_job GROUP BY state')
        values['jobs'] = {line.split('\t')[0]: int(line.split('\t')[1]) for line in jobs.splitlines()}
        errors = self.sql("SELECT ERROR_NUMBER,SUM_ERROR_RAISED FROM performance_schema.events_errors_summary_global_by_error WHERE ERROR_NUMBER IN (1205,1213)")
        values['mysql_error_counts'] = {line.split('\t')[0]: int(line.split('\t')[1]) for line in errors.splitlines()}
        return values

    def sample(self, names):
        values = {'utc': time.time(), 'resources': {}}
        paths = ['/sys/fs/cgroup/' + p for p in ('cpu.stat', 'memory.current', 'memory.peak', 'memory.events', 'pids.current')]
        for role, name in names.items():
            code, raw = self.docker('exec', name, 'cat', *paths, '/proc/1/status', accept=role == 'client')
            if code:
                _, state = self.docker('inspect', '--format', '{{json .State}}', name)
                require(role == 'client' and not json.loads(state)['Running'], 'running participant resource sampling failed')
                continue
            item = measurement.parse_cgroup(raw); item['monotonic'] = time.monotonic()
            values['resources'][role] = item
        values['database'] = self.db_sample()
        if self.mode == 'go':
            code, raw = self.http('/metrics', context=False)
            require(code == 200, 'Go metrics sample failed')
            values['go_metrics'] = {line.split()[0]: float(line.split()[-1]) for line in raw.splitlines()
                if not line.startswith('#') and line.split() and line.split()[0].startswith(('go_goroutines', 'go_memstats_heap_alloc_bytes', 'go_gc_duration_seconds_count', 'process_resident_memory_bytes', 'logan_db_', 'logan_processing_jobs', 'logan_jobs_duration_seconds'))}
        return values

    def data_check(self, report, fixture_mode):
        prefix = report['run_prefix']; require(re.fullmatch('phase19-[a-z0-9]+', prefix), 'invalid synthetic run prefix')
        result = {}
        for flow, task, detail in [('native', 'logan_task', 'logan_log_detail'), ('web', 'web_task', 'web_detail')]:
            selected = self.sql('SELECT id,status FROM ' + self.db + '.' + task + " WHERE device_id LIKE '" + prefix + "-%' ORDER BY id")
            tasks = {int(row.split('\t')[0]): int(row.split('\t')[1]) for row in selected.splitlines()}
            raw = self.sql('SELECT d.task_id,HEX(CAST(JSON_ARRAYAGG(d.content) AS CHAR CHARACTER SET utf8mb4)) FROM ' + self.db + '.' + detail + ' d JOIN ' + self.db + '.' + task + " t ON t.id=d.task_id WHERE t.device_id LIKE '" + prefix + "-%' GROUP BY d.task_id ORDER BY d.task_id")
            details = {int(row.split('\t')[0]): json.loads(bytes.fromhex(row.split('\t')[1]).decode()) for row in raw.splitlines()}
            expected = self.fixture_data[fixture_mode][flow + 'Contents']
            if self.mode == 'java' and fixture_mode == 'sdk' and flow == 'native': expected = expected[:1]
            result[flow] = dict(tasks=len(tasks), pending=sum(v == 0 for v in tasks.values()),
                                details=sum(len(v) for v in details.values()),
                                valid_tasks=sum(status == 1 and sorted(details.get(task_id, [])) == sorted(expected) for task_id, status in tasks.items()),
                                rows_sha256=hashlib.sha256(json.dumps([tasks, details], sort_keys=True).encode()).hexdigest(),
                                task_ids_sha256=hashlib.sha256(json.dumps(sorted(tasks)).encode()).hexdigest())
            jobs = self.sql('SELECT j.state,COUNT(*) FROM ' + self.db + '.logan_processing_job j JOIN ' + self.db + '.' + task +
                            " t ON t.id=j.task_id AND j.kind='" + flow + "' WHERE t.device_id LIKE '" + prefix + "-%' GROUP BY j.state")
            result[flow]['jobs'] = {row.split('\t')[0]: int(row.split('\t')[1]) for row in jobs.splitlines()}
            attempts = self.sql('SELECT COALESCE(MAX(j.attempts),0),COALESCE(SUM(j.attempts>1),0),COALESCE(SUM(j.last_error_code<>\'\'),0) FROM ' + self.db + '.logan_processing_job j JOIN ' + self.db + '.' + task +
                            " t ON t.id=j.task_id AND j.kind='" + flow + "' WHERE t.device_id LIKE '" + prefix + "-%'").split('\t')
            result[flow]['job_attempts_max'], result[flow]['retried_tasks'], result[flow]['jobs_with_last_error'] = map(int, attempts)
        return result

    def run_stage(self, stage, index):
        name = self.mode + '-' + stage['name']
        directory = self.args.work_dir / name; directory.mkdir(mode=0o700)
        client = self.reserve('container', self.prefix + '-' + self.mode[0] + '-' + str(index))
        arguments = ['--base-url', 'http://' + self.app + ':8080/logan-web', '--fixture', '/fixture.json',
                     '--mode', stage['mode'], '--duration', str(stage['seconds']) + 's', '--qps', str(stage['qps']),
                     '--concurrency', str(stage['workers']), '--queue', str(stage['queue']), '--timeout', '5s',
                     '--parse-timeout', '15s', '--drain', '20s', '--allow-test-target', '--allow-writes',
                     '--test-target-marker', 'logan-load-test', '--output', '/evidence/report.json']
        controller_start = time.monotonic()
        self.docker('run', '-d', '--name', client, *self.common('.5', '256m'), '--user', self.uid,
                    '--tmpfs', '/tmp:rw,size=16777216', '--mount', 'type=bind,src=' + str(self.args.binary) + ',dst=/loader,readonly',
                    '--mount', 'type=bind,src=' + str(self.fixture_files[stage['fixture']]) + ',dst=/fixture.json,readonly',
                    '--mount', 'type=bind,src=' + str(directory) + ',dst=/evidence', '--entrypoint', '/loader', self.args.go_image, *arguments)
        samples = []; deadline = time.monotonic() + stage['seconds'] + 90; next_sample = time.monotonic()
        path = directory / 'report.json'; names = dict(app=self.app, mysql=self.mysql, oss=self.oss, client=client)
        report_version = None
        while True:
            if time.monotonic() >= next_sample:
                _, raw = self.docker('inspect', '--format', '{{json .State}}', client)
                if not json.loads(raw)['Running']: break
                value = self.sample(names); samples.append(value)
                with open(directory / 'resources.jsonl', 'a') as f: f.write(json.dumps(value) + '\n')
                (directory / 'resources.jsonl').chmod(0o600)
                self.dump('live-status', {'runtime': self.mode, 'phase': stage['name'], 'utc': time.time(),
                    'seconds': stage['seconds'], 'qps_cycles': stage['qps'], 'samples': len(samples), 'last_sample': value})
                next_sample += 10
            if path.exists():
                stat = path.stat(); version = (stat.st_ino, stat.st_mtime_ns)
                if version != report_version:
                    report_version = version; result = json.loads(path.read_text())
                    if result.get('final'): break
            require(time.monotonic() < deadline, 'bounded load client completion timeout'); time.sleep(1)
        _, exited = self.docker('wait', client, timeout=35); exitcode = int(exited.strip())
        controller_elapsed = time.monotonic() - controller_start
        if exitcode not in (0, 1):
            _, logs = self.docker('logs', client)
            self.dump(name + '-client-failure', dict(stage=stage, exit_code=exitcode, diagnostic_output=logs))
        require(exitcode in (0, 1), 'load client configuration/report/process failure')
        require(path.is_file(), 'load report missing')
        report = json.loads(path.read_text()); require(report.get('final'), 'load report not final')
        if stage['seconds'] < 3600: host_window(stage['seconds'], controller_elapsed, 0)
        measurement.validate_report(report, stage['seconds'])
        require(not report.get('report_write_errors'), 'load progress report lost')
        evidence = dict(runtime=self.mode, stage=stage, client_exit_code=exitcode, report=report,
                        controller_elapsed_seconds=controller_elapsed,
                        errors=measurement.classify_errors(report, self.mode, stage['fixture']),
                        resources={role: measurement.resource_summary([s['resources'][role] for s in samples if role in s['resources']]) for role in names},
                        resource_samples=len(samples), database_final=self.db_sample(), data=self.data_check(report, stage['fixture']))
        def validate():
            if stage['name'] in ('protocol-warmup', 'soak-warmup', 'soak'):
                require(report['dropped'] == 0 and evidence['errors']['unexpected_errors'] == 0, 'baseline/soak had unexpected errors or drops')
                if stage['name'] != 'soak': require(report['workflows']['Errors'] == 0, 'protocol baseline workflow did not complete correctly')
                f = self.fixture_data[stage['fixture']]
                validate_data(evidence['data'], self.mode, stage['fixture'], len(f['nativeContents']), len(f['webContents']))
            if stage['name'] == 'soak':
                evidence['assessment'] = assess_soak(report, self.mode, self.args.duration)
                sample_span = samples[-1]['resources']['app']['monotonic'] - samples[0]['resources']['app']['monotonic']
                evidence['host_active_lower_bound_seconds'] = host_window(stage['seconds'], controller_elapsed, sample_span)
                evidence['host_sample_span_seconds'] = sample_span
                evidence['host_controller_http_qps'] = report['http']['Completed'] / controller_elapsed
                evidence['guest_utc_span_seconds'] = measurement.parse_utc(report['snapshot_utc']) - measurement.parse_utc(report['start_utc'])
                evidence['guest_monotonic_minus_utc_seconds'] = report['elapsed_seconds'] - evidence['guest_utc_span_seconds']
                require(len(samples) >= self.args.duration // 12, 'resource sample coverage incomplete')
                require(all(v['oom_kills'] == 0 for v in evidence['resources'].values()), 'resource OOM observed')
                start = measurement.parse_utc(report['start_utc'])
                end = measurement.parse_utc(report['snapshot_utc'])
                for role in names:
                    measurement.validate_coverage([s for s in samples if role in s['resources']], start, end)
                ledger = report['ledger']
                require(len(ledger) == report['workflows']['Completed'] and len({v['Cycle'] for v in ledger}) == len(ledger), 'workflow ledger incomplete or duplicated')
                for flow in ('native', 'web'):
                    ids = [v['TaskID'] for v in ledger if v['Flow'] == flow]
                    require(all(v > 0 for v in ids) and len(ids) == len(set(ids)), 'missing or duplicate uploaded task IDs')
                    require(evidence['data'][flow]['tasks'] == len(ids) and hashlib.sha256(json.dumps(sorted(ids)).encode()).hexdigest() == evidence['data'][flow]['task_ids_sha256'], 'uploaded tasks differ from full workflow ledger')
                    if self.mode == 'go':
                        require(evidence['data'][flow]['jobs'] == {'done': len(ids)}, 'Go parse jobs not fully done')
                        require(evidence['data'][flow]['retried_tasks'] == evidence['data'][flow]['jobs_with_last_error'] == 0, 'Go parser retried or retained an unexpected error')
                if self.mode == 'go': validate_lock_errors(samples, evidence['database_final'])
        self.validate_and_record_stage(name, evidence, validate)
        self.events.append({'runtime': self.mode, 'phase': stage['name'], 'event': 'completed', 'utc': time.time()})
        return evidence

    def reset_before_soak(self):
        # Stop all application writers before truncation; each soak starts with identical empty tables.
        self.docker('stop', '--time', '20', self.app, timeout=40)
        tables = self.sql('SHOW TABLES FROM ' + self.db).splitlines()
        require(tables and all(re.fullmatch('[a-z_]+', table) for table in tables), 'unexpected isolated table inventory')
        self.sql('USE ' + self.db + '; ' + '; '.join('TRUNCATE TABLE ' + table for table in tables))
        self.docker('restart', '--time', '20', self.mysql, timeout=45)
        deadline = time.monotonic() + 120
        while True:
            try:
                if self.sql('SELECT 1') == '1': break
            except base.DockerError: pass
            require(time.monotonic() < deadline, 'fresh soak MySQL readiness timeout'); time.sleep(.5)
        # Replace the fixture process to release all prior in-memory objects and CPU accounting baseline.
        self.docker('restart', '--time', '10', self.oss, timeout=30)
        self.docker('start', self.app)
        deadline = time.monotonic() + 120
        while True:
            code, text = self.http('/logan/meta/logtypes.json')
            if code == 200 and json.loads(text).get('code') == 200: break
            require(time.monotonic() < deadline, 'soak restart readiness timeout'); time.sleep(1)
        self.events.append({'runtime': self.mode, 'event': 'writers_stopped_schema_objects_reset_before_soak', 'utc': time.time()})

    def execute(self):
        _, text = self.docker('info', '--format', '{{json .}}'); info = json.loads(text)
        require(info['OSType'] == 'linux' and str(info['CgroupVersion']) == '2', 'Linux cgroup2 required'); self.engine_ready = True
        for image in runtime_images(self.args.runtimes, self.args.go_image):
            self.docker('image', 'inspect', image)
        self.uid = str(os.getuid()) + ':' + str(os.getgid())
        self.network = self.reserve('network', self.prefix + '-net'); self.docker('network', 'create', '--internal', '--label', base.LABEL + '=' + self.run_id, self.network)
        self.fixture_data = dict(sdk=fixtures.make_fixture(self.args.sdk_fixtures), protocol=fixtures.make_protocol_fixture())
        require(self.fixture_data['sdk']['nativeKey'] == self.fixture_data['protocol']['nativeKey'] and self.fixture_data['sdk']['nativeIV'] == self.fixture_data['protocol']['nativeIV'], 'Native fixture key mismatch')
        self.fixture_files = {}; self.events = []; results = []
        fixture_server = self.args.work_dir / 'oss_fixture.py'
        fixture_server.write_bytes((ROOT / 'tests/e2e20/oss_fixture.py').read_bytes()); fixture_server.chmod(0o600)
        for kind, value in self.fixture_data.items():
            p = self.args.work_dir / (kind + '.json'); p.write_text(json.dumps(value)); p.chmod(0o600); self.fixture_files[kind] = p
        self.dump('fixture-preflight', {kind: self.preflight_fixture(kind, path) for kind, path in self.fixture_files.items()})
        order = runtime_order(self.args.runtimes)
        plan = stage_plan(self.args.duration, self.args.stage_seconds, self.args.soak_qps); self.dump('frozen-plan', dict(stages=plan, runtime_order=order, app_cpu=1, app_memory=APP_MEMORY, client_cpu=.5, mysql_cpu=1, mysql_memory=APP_MEMORY))
        for mode in order:
            self.setup_runtime(mode)
            stages = []
            for index, stage in enumerate(plan):
                if stage['name'] == 'soak-warmup': self.reset_before_soak()
                stages.append(self.run_stage(stage, index))
            if mode == 'java':
                _, gc = self.docker('exec', self.app, 'cat', '/tmp/gc.log'); p = self.args.out / 'java-gc.log'; p.write_text(gc); p.chmod(0o600)
            for name in (self.app, self.mysql, self.oss):
                _, state = self.docker('inspect', '--format', '{{json .State}}', name); state = json.loads(state)
                require(state['Running'] and not state['OOMKilled'], 'runtime exited or OOM during load')
                self.docker('stop', '--time', '20', name, timeout=40)
            self.events.append({'runtime': mode, 'event': 'stopped_before_next_runtime', 'utc': time.time()})
            results.append(dict(runtime=mode, stages=stages))
        self.dump('results', dict(measurement_complete=True, actual_soak_seconds_each=self.args.duration, results=results,
             java_sdk_soak_strict_functional_passed=next((r['stages'][-1]['assessment']['strict_functional_passed'] for r in results if r['runtime']=='java'), None),
             limitations=['local OSS byte fixture without signature verification','Node WebSDK emulator and shared CLogan only','one architecture/order and resource profile','not production capacity or 24h stability','internal plaintext HTTP/MySQL benchmark; no TLS latency measurement']))


def main(argv=None):
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('--docker', required=True); p.add_argument('--host', required=True); p.add_argument('--allow-isolated-runtime', action='store_true')
    p.add_argument('--binary', type=Path, required=True); p.add_argument('--go-image', required=True)
    p.add_argument('--sdk-fixtures', type=Path, required=True); p.add_argument('--legacy-rsa-compat', action='store_true', required=True)
    p.add_argument('--out', type=Path, required=True); p.add_argument('--work-dir', type=Path, required=True)
    p.add_argument('--duration', type=int, default=3660); p.add_argument('--stage-seconds', type=int, default=30)
    p.add_argument('--runtimes', choices=('go', 'both'), default='go')
    p.add_argument('--soak-qps', type=float, default=4); p.add_argument('--allow-short-diagnostic', action='store_true')
    a = p.parse_args(argv)
    try:
        base.validate_host(a.host, a.allow_isolated_runtime)
        require(Path(a.docker).is_absolute() and a.binary.is_absolute() and a.binary.is_file(), 'absolute Docker and Linux loader required')
        stage_plan(a.duration, a.stage_seconds, a.soak_qps)
        require(a.duration >= 3600 or a.allow_short_diagnostic, 'one-hour duration required without diagnostic flag')
        a.sdk_fixtures = a.sdk_fixtures.resolve(); require((a.sdk_fixtures / 'manifest.json').is_file(), 'actual SDK fixtures required')
        require(Path('/private/tmp') in a.sdk_fixtures.parents and Path('/private/tmp') in a.binary.resolve().parents, 'SDK and loader bind paths must be shared under /private/tmp')
        a.work_dir = a.work_dir.resolve(); a.out = a.out.resolve()
        require(Path('/private/tmp') in a.work_dir.parents and ',' not in str(a.work_dir), 'dedicated shared private work directory required')
        require(a.out != base.CHECKOUT and base.CHECKOUT not in a.out.parents, 'evidence outside checkout required')
        a.out.mkdir(mode=0o700, parents=True, exist_ok=False); a.work_dir.mkdir(mode=0o700, parents=True, exist_ok=False)
    except (OSError, ValueError, AssertionError) as e: p.error(str(e))
    a.keep_image = False; v = Capacity(a); code = 0; error = None
    old = signal.signal(signal.SIGTERM, lambda *_: (_ for _ in ()).throw(KeyboardInterrupt()))
    try: v.execute()
    except BaseException as e:
        code = runtime.classify_exit(e, v.engine_ready); error = type(e).__name__ + ': ' + str(e).replace(v.password, '<synthetic-secret>')
    finally:
        signal.signal(signal.SIGINT, signal.SIG_IGN); signal.signal(signal.SIGTERM, signal.SIG_IGN)
        cleaned = v.cleanup()
        if not cleaned and code == 0: code = 1
        for file in a.out.glob('*.env'): file.unlink()
        (a.work_dir / 'java.war').unlink(missing_ok=True)
        v.dump('events', getattr(v, 'events', []))
        v.dump('summary', dict(status='passed' if code == 0 else 'failed', exit_code=code, error=error,
               requested_soak_seconds_each=a.duration, selected_runtimes=runtime_order(a.runtimes), one_hour_measurement_completed=a.duration >= 3600 and code == 0,
               checks=v.checks, binary_sha256=hashlib.sha256(a.binary.read_bytes()).hexdigest(), go_image=a.go_image,
               scope='isolated synthetic measured load only; original Java retained; no production/cloud/cutover'))
        signal.signal(signal.SIGTERM, old)
    print('Evidence: ' + str(a.out / 'summary.json'))
    return code


if __name__ == '__main__': sys.exit(main())
