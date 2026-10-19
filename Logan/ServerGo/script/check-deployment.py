#!/usr/bin/env python3
"""Offline candidate validation only. Never build, start, migrate or switch traffic."""
import argparse
import json
import os
import re
from pathlib import Path
import shutil
import subprocess
import tempfile

ROOT = Path(__file__).resolve().parents[1]

def require(condition, reason):
    if not condition:
        raise ValueError(reason)

def validate_compose(model, release=False):
    services = model['services']
    require(set(services) == {'logan-go', 'prometheus', 'alertmanager'}, 'unexpected services')
    go = services['logan-go']
    if release:
        require(not go.get('build'), 'release must not build')
        require(bool(re.fullmatch(r'[^@\s]+@sha256:[0-9a-f]{64}', go['image'])), 'release image must be immutable')
        require(go['pull_policy'] == 'always', 'release pull policy')
    else:
        require(Path(go['build']['context']).resolve() == ROOT, 'wrong build context')
        require(go['build']['dockerfile'] == 'Dockerfile', 'wrong Dockerfile')
    require(go['user'] == '10001:10001' and go['read_only'], 'Go permissions')
    require('ALL' in go['cap_drop'] and 'no-new-privileges:true' in go['security_opt'], 'Go privilege isolation')
    require('/livez' in ' '.join(go['healthcheck']['test']), 'liveness healthcheck')
    require('/readyz' not in ' '.join(go['healthcheck']['test']), 'readiness must remain blocked')
    require(go['environment']['LOGAN_MAINTENANCE_ENABLED'] == 'false', 'automatic cleanup enabled')
    require(go['environment']['LOGAN_METRICS_ENABLED'] == 'true', 'metrics disabled')
    require(go['environment']['LOGAN_CONTEXT_PATH'] == '/logan-web', 'legacy context path')
    # Compose canonical output doubles dollars for safe re-reading; no value was interpolated.
    require(go['environment']['LOGAN_TEST_LITERAL'].replace('$$', '$') == 'dollar$not_expanded#literal', 'raw env changed')
    require(int(go['mem_limit']) == 1073741824 and float(go['cpus']) == 1, 'candidate resource envelope')
    require(go['stop_grace_period'] in ('1m30s', '90s'), 'shutdown budget')
    require(any(('uid=10001' in p and 'size=536870912' in p for p in go['tmpfs'])), 'temporary quota/ownership')
    require(all((v['read_only'] for v in go['volumes'] if v['type'] == 'bind')), 'writable secrets')
    for name, service in services.items():
        require(service.get('read_only'), 'writable service root: ' + name)
        require('logan-go-ops' in service['networks'], 'missing monitoring network')
        for port in service.get('ports', []):
            require(port['host_ip'] == '127.0.0.1', 'non-loopback port: ' + name)
    for name in ('prometheus', 'alertmanager'):
        require(services[name]['profiles'] == ['monitoring'], 'monitoring must be opt-in')
    mounts = services['prometheus']['volumes']
    require(any((v.get('source') == str(ROOT / 'deploy/monitoring/prometheus.yml') and v.get('target') == '/etc/prometheus/prometheus.yml' for v in mounts)), 'wrong monitoring mount base')

def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--compose', default=shutil.which('docker-compose'))
    parser.add_argument('--promtool', default=shutil.which('promtool'))
    parser.add_argument('--amtool', default=shutil.which('amtool'))
    parser.add_argument('--out', type=Path)
    args = parser.parse_args()
    out = args.out or Path(tempfile.mkdtemp(prefix='logan-deployment-'))
    if args.out:
        out.mkdir(mode=0o700, parents=True, exist_ok=False)
    out.chmod(0o700)
    results = []
    def check(name, command, cwd=ROOT):
        if not command[0]:
            results.append({'check': name, 'status': 'blocked', 'reason': 'required validator unavailable'})
            return None
        try:
            r = subprocess.run(command, cwd=cwd, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True, timeout=60)
            log = out / (name + '.log')
            log.write_text(r.stdout); log.chmod(0o600)
            results.append({'check': name, 'status': 'passed' if r.returncode == 0 else 'failed', 'exit_code': r.returncode, 'log': log.name})
            return r if r.returncode == 0 else None
        except (OSError, subprocess.TimeoutExpired):
            results.append({'check': name, 'status': 'blocked', 'reason': 'validator execution unavailable or timed out'})
            return None
    with tempfile.TemporaryDirectory(prefix='logan-compose-input-') as base:
        work = Path(base)
        env_file = work / 'go.env'
        env_file.write_text((ROOT / 'deploy/go.env.example').read_text() + '\nLOGAN_TEST_LITERAL=dollar$not_expanded#literal\n')
        env_file.chmod(0o600)
        secrets = work / 'keys'; secrets.mkdir(mode=0o700)
        interpolation = work / 'compose.env'
        interpolation.write_text('LOGAN_GO_ENV_FILE=' + str(env_file) + '\nLOGAN_GO_SECRET_DIR=' + str(secrets) + '\nLOGAN_GO_ENABLE_MAINTENANCE=false\nLOGAN_GO_BIND_ADDRESS=127.0.0.1\nLOGAN_GO_PORT=18080\n')
        # Explicit process values take priority over inherited deployment settings.
        previous = os.environ.copy()
        os.environ.update({'LOGAN_GO_ENV_FILE': str(env_file), 'LOGAN_GO_SECRET_DIR': str(secrets), 'LOGAN_GO_ENABLE_MAINTENANCE':'false', 'LOGAN_GO_BIND_ADDRESS':'127.0.0.1', 'LOGAN_GO_PORT':'18080', 'LOGAN_GO_IMAGE':'logan-servergo:config-check', 'LOGAN_GO_RELEASE_IMAGE':'registry.invalid/logan/servergo@sha256:'+('0'*64)})
        command = [args.compose, '--env-file', str(interpolation), '-p', 'logan-go-config-check', '-f', str(ROOT/'deploy/compose.go.yaml'), '-f', str(ROOT/'deploy/monitoring/compose.monitoring.yaml'), '--profile', 'monitoring', 'config', '--format', 'json']
        r = check('compose', command)
        if r:
            try:
                validate_compose(json.loads(r.stdout))
                results.append({'check':'compose_contract', 'status':'passed'})
            except (ValueError, KeyError, AssertionError, TypeError) as error:
                results.append({'check':'compose_contract', 'status':'failed', 'reason': str(error)})
        release_command = command[:]
        offset = release_command.index('--profile')
        release_command[offset:offset] = ['-f', str(ROOT/'deploy/compose.release.yaml')]
        release = check('compose_release', release_command)
        if release:
            try:
                validate_compose(json.loads(release.stdout), release=True)
                results.append({'check':'release_contract', 'status':'passed'})
            except (ValueError, KeyError, TypeError) as error:
                results.append({'check':'release_contract', 'status':'failed', 'reason':str(error)})
        os.environ.clear(); os.environ.update(previous)
        monitoring = ROOT/'deploy/monitoring'
        check('prometheus_syntax', [args.promtool, 'check', 'config', '--syntax-only', 'prometheus.yml'], monitoring)
        check('alert_rules', [args.promtool, 'check', 'rules', 'alerts.yml'], monitoring)
        check('alert_scenarios', [args.promtool, 'test', 'rules', 'alerts.test.yml'], monitoring)
        check('alertmanager', [args.amtool, 'check-config', 'alertmanager.yml'], monitoring)
    summary = {'scope':'offline candidate configuration only', 'checks':results, 'not_executed':['image build/pull and vulnerability scan', 'Linux container startup/volume permissions/OOM/SIGTERM', 'live scrape and external notification delivery', 'real OSS and production cutover/rollback']}
    path = out/'summary.json'; path.write_text(json.dumps(summary, indent=2)+'\n'); path.chmod(0o600)
    for result in results:
        print(result['check'] + ': ' + result['status'])
    print('Evidence: ' + str(path))
    states = [r['status'] for r in results]
    return 1 if 'failed' in states else 2 if 'blocked' in states else 0

if __name__ == '__main__':
    raise SystemExit(main())
