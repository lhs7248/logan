#!/usr/bin/env python3
"""Opt-in original Java / production Go all20 positive HTTP rehearsal on an isolated engine."""
import argparse
import importlib.util
import json
from pathlib import Path
import signal

ROOT = Path(__file__).resolve().parents[1]


def load(name, path):
    spec = importlib.util.spec_from_file_location(name, path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def main(argv=None):
    data = load('all20_data', ROOT / 'script/rehearse-data-rollback.py')
    runner = load('all20_runner', ROOT / 'tests/e2e20/runner.py')
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('--docker', required=True)
    p.add_argument('--host', required=True)
    p.add_argument('--allow-isolated-runtime', action='store_true')
    p.add_argument('--binary', type=Path, required=True)
    p.add_argument('--java-bundle', type=Path, required=True)
    p.add_argument('--work-dir', type=Path, required=True)
    p.add_argument('--out', type=Path, required=True)
    p.add_argument('--http-go-image', required=True)
    p.add_argument('--namespace-prefix', required=True)
    p.add_argument('--historical-objects', action='store_true', help='seed isolated pre-existing decoded Native objects, Java baseline clones and untouched Go pending rows')
    p.add_argument('--sdk-fixtures', type=Path)
    p.add_argument('--fixture-mode', choices=('sdk', 'protocol'), default='sdk')
    p.add_argument('--legacy-rsa-compat', action='store_true', help='SDK diagnostic only: explicitly permit original 1023-bit RSA key in Go via GODEBUG=rsa1024min=0')
    a = p.parse_args(argv)
    a.with_http = False
    try:
        data.require(not a.legacy_rsa_compat or a.fixture_mode == 'sdk', '--legacy-rsa-compat is allowed only with SDK mode')
        data.base.validate_host(a.host, a.allow_isolated_runtime)
        data.require(Path(a.docker).is_absolute(), 'absolute Docker CLI required')
        data.require(a.binary.is_absolute() and a.binary.is_file(), 'compiled Linux crossruntime binary required')
        a.java_bundle = a.java_bundle.resolve()
        data.require((a.java_bundle / 'run.sh').is_file(), 'prepared original Java bundle required')
        if a.fixture_mode == 'sdk':
            data.require(a.sdk_fixtures is not None, '--sdk-fixtures required in SDK mode')
            a.sdk_fixtures = a.sdk_fixtures.resolve()
            data.require((a.sdk_fixtures / 'manifest.json').is_file(), 'actual SDK fixture manifest required')
        data.require(a.namespace_prefix.replace('-', '').isalnum() and len(a.namespace_prefix) <= 32, 'namespace must be 1-32 alphanumeric/hyphen characters')
        a.work_dir = a.work_dir.resolve()
        data.require(Path('/private/tmp') in a.work_dir.parents and ',' not in str(a.work_dir), 'dedicated shared /private/tmp work path required')
        a.out = a.out.resolve()
        data.require(a.out != data.base.CHECKOUT and data.base.CHECKOUT not in a.out.parents, 'evidence outside checkout required')
        a.out.mkdir(mode=0o700, parents=True, exist_ok=False)
        a.work_dir.mkdir(mode=0o700, parents=True, exist_ok=False)
    except (OSError, AssertionError, ValueError) as error:
        p.error(str(error))
    class AllInterfaces(data.DataRehearsal):
        def execute(self):
            super().execute()
            self.check('all20_actual_java_go_http', lambda: runner.run_all_interfaces(self))
    verifier = AllInterfaces(a)
    code = 0
    previous = signal.signal(signal.SIGTERM, lambda *_: (_ for _ in ()).throw(KeyboardInterrupt()))
    try:
        verifier.execute()
    except BaseException as error:
        code = data.runtime.classify_exit(error, verifier.engine_ready)
        verifier.checks.append({'check': 'execution', 'status': 'blocked' if code == 2 else 'failed',
                                'reason': str(error).replace(verifier.password, '<synthetic-secret>')})
    finally:
        old_int = signal.signal(signal.SIGINT, signal.SIG_IGN)
        signal.signal(signal.SIGTERM, signal.SIG_IGN)
        if not verifier.cleanup() and code == 0:
            code = 1
        if hasattr(verifier, 'events'):
            ledger = a.out / 'event-ledger.json'
            ledger.write_text(json.dumps(verifier.events, indent=2)); ledger.chmod(0o600)
        for path in a.out.glob('*.env'):
            path.unlink()
        result = {'status': 'passed' if code == 0 else ('blocked' if code == 2 else 'failed'), 'exit_code': code,
                  'fixture_mode': a.fixture_mode, 'historical_objects': a.historical_objects, 'legacy_rsa_compatibility_override': a.legacy_rsa_compat, 'sdk_fixture_ingestion': a.fixture_mode == 'sdk' and code == 0,
                  'checks': verifier.checks, 'scope': 'all20 original Java and Go HTTP using local OSS byte fixture',
                  'cloud_gate': {'status': 'blocked', 'reason': 'no real Aliyun credentials; local fixture does not verify signature cryptography'},
                  'sdk_scope': 'host-compiled shared CLogan and Node WebSDK with emulated IndexedDB/intercepted XMLHttpRequest; server HTTP ingestion only; no real browser/device/cloud certification'}
        target = a.out / 'summary.json'
        target.write_text(json.dumps(result, indent=2)); target.chmod(0o600)
        signal.signal(signal.SIGTERM, previous)
        signal.signal(signal.SIGINT, old_int)
    print('Evidence: ' + str(target))
    return code


if __name__ == '__main__':
    raise SystemExit(main())
