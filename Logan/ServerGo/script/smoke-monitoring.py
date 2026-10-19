#!/usr/bin/env python3
"""Local native-process smoke only; no DB/OSS, containers, gateway changes or external notifications."""
import argparse
import json
import os
from pathlib import Path
import signal
import socket
import subprocess
import tempfile
import time
import urllib.error
import urllib.parse
import urllib.request

ROOT = Path(__file__).resolve().parents[1]
HTTP = urllib.request.build_opener(urllib.request.ProxyHandler({}))

def port():
    with socket.socket() as s:
        s.bind(('127.0.0.1', 0))
        return s.getsockname()[1]

def get(url):
    with HTTP.open(url, timeout=2) as response:
        return json.load(response)

def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--server', required=True, type=Path)
    parser.add_argument('--prometheus', required=True, type=Path)
    parser.add_argument('--alertmanager', required=True, type=Path)
    parser.add_argument('--allow-local-processes', action='store_true')
    args = parser.parse_args()
    if not args.allow_local_processes:
        parser.error('--allow-local-processes is required')
    for binary in (args.server, args.prometheus, args.alertmanager):
        if not binary.is_file() or not os.access(binary, os.X_OK):
            parser.error('executable unavailable: ' + binary.name)
    out = Path(tempfile.mkdtemp(prefix='logan-monitoring-smoke-')); out.chmod(0o700)
    processes, logs, checks = [], [], []
    started = time.monotonic()
    ports = set()
    while len(ports) < 3:
        ports.add(port())
    go_port, prom_port, am_port = sorted(ports)
    monitoring = ROOT/'deploy/monitoring'
    local_rule = out/'local-smoke.yml'
    local_rule.write_text('groups:\n  - name: local-smoke\n    rules:\n      - alert: LoganLocalSmoke\n        expr: vector(1)\n        labels:\n          service: logan-go\n          reason: local_test\n')
    # Generate loopback-only inputs rather than loading optional remote receivers,
    # remote_write or extra targets from an operator-edited configuration.
    configuration = 'global:\n  scrape_interval: 1s\n  evaluation_interval: 1s\nrule_files:\n  - '+json.dumps(str(monitoring/'alerts.yml'))+'\n  - '+json.dumps(str(local_rule))+'\nalerting:\n  alertmanagers:\n    - static_configs:\n        - targets: [127.0.0.1:'+str(am_port)+']\nscrape_configs:\n  - job_name: logan-go\n    scrape_timeout: 500ms\n    static_configs:\n      - targets: [127.0.0.1:'+str(go_port)+']\n'
    (out/'prometheus.yml').write_text(configuration)
    # Test-only route: never load a potentially edited external receiver.
    (out/'alertmanager.yml').write_text('global:\n  resolve_timeout: 5m\nroute:\n  receiver: blackhole\nreceivers:\n  - name: blackhole\n')
    env = {k:v for k,v in os.environ.items() if not k.startswith('LOGAN_')}
    env.update({'LOGAN_ENV':'development', 'LOGAN_HTTP_ADDRESS':'127.0.0.1:'+str(go_port), 'LOGAN_EXIT_WAIT':'2s', 'LOGAN_METRICS_ENABLED':'true', 'LOGAN_DB_ENABLED':'false', 'LOGAN_NATIVE_ENABLED':'false', 'LOGAN_NATIVE_QUERY_ENABLED':'false', 'LOGAN_WEB_ENABLED':'false', 'LOGAN_MAINTENANCE_ENABLED':'false'})
    def start(name, command):
        log = open(out/(name+'.log'), 'w'); logs.append(log)
        processes.append(subprocess.Popen(command, env=env, stdout=log, stderr=subprocess.STDOUT, start_new_session=True))
    def wait(predicate, name):
        deadline = time.monotonic()+45
        while time.monotonic()<deadline:
            if any(p.poll() is not None for p in processes):
                raise RuntimeError('a local test process exited before '+name)
            try:
                if predicate():
                    checks.append({'check':name,'status':'passed'}); return
            except (OSError, ValueError, KeyError):
                pass
            time.sleep(.2)
        raise RuntimeError('timeout waiting for '+name)
    status, reason = 'passed', ''
    def terminate(signum, frame):
        raise InterruptedError('local smoke interrupted by SIGTERM')
    old_term = signal.signal(signal.SIGTERM, terminate)
    try:
        start('server', [str(args.server.resolve())])
        start('alertmanager', [str(args.alertmanager.resolve()), '--config.file='+str(out/'alertmanager.yml'), '--storage.path='+str(out/'am-data'), '--web.listen-address=127.0.0.1:'+str(am_port), '--cluster.listen-address='])
        start('prometheus', [str(args.prometheus.resolve()), '--config.file='+str(out/'prometheus.yml'), '--storage.tsdb.path='+str(out/'prom-data'), '--storage.tsdb.retention.time=1h', '--web.listen-address=127.0.0.1:'+str(prom_port)])
        origin = 'http://127.0.0.1:'+str(go_port)
        wait(lambda:get(origin+'/ping').get('message') == 'pong', 'local_http')
        try:
            get(origin+'/readyz')
            raise RuntimeError('readiness unexpectedly permitted traffic')
        except urllib.error.HTTPError as error:
            if error.code != 503: raise
            checks.append({'check':'readiness_still_503','status':'passed'})
        def query(expression):
            data = get('http://127.0.0.1:'+str(prom_port)+'/api/v1/query?'+urllib.parse.urlencode({'query':expression}))
            return data['data']['result']
        wait(lambda:any(float(r['value'][1]) == 1 for r in query('up{job="logan-go"}')), 'live_local_scrape')
        wait(lambda:bool(query('logan_http_requests_total{job="logan-go",route="/ping",status="200"}')), 'live_http_metric')
        wait(lambda:any(a['labels'].get('alertname') == 'LoganLocalSmoke' for a in get('http://127.0.0.1:'+str(am_port)+'/api/v2/alerts')), 'synthetic_alert_forwarded_to_local_alertmanager')
    except (Exception, KeyboardInterrupt) as error:
        status = 'failed'
        reason = str(error) or 'interrupted'
    finally:
        signal.signal(signal.SIGTERM, signal.SIG_IGN)
        for process in reversed(processes):
            if process.poll() is None:
                try: os.killpg(process.pid, signal.SIGTERM)
                except ProcessLookupError: pass
                try: process.wait(timeout=10)
                except subprocess.TimeoutExpired:
                    try: os.killpg(process.pid, signal.SIGKILL)
                    except ProcessLookupError: pass
                    process.wait()
        for log in logs: log.close()
        signal.signal(signal.SIGTERM, old_term)
    summary = {'scope':'local native processes; synthetic alert; no external notification', 'status':status, 'checks':checks, 'reason':reason, 'elapsed_seconds':round(time.monotonic()-started,3), 'all_test_processes_stopped':all(p.poll() is not None for p in processes), 'not_executed':['Docker image/container validation', 'real DB/OSS', 'production cutover or rollback', 'external notification delivery']}
    path = out/'summary.json'; path.write_text(json.dumps(summary,indent=2)+'\n'); path.chmod(0o600)
    print('Local monitoring smoke: '+status)
    print('Evidence: '+str(path))
    return 0 if status == 'passed' else 1

if __name__ == '__main__':
    raise SystemExit(main())
