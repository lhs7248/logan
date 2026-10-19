#!/usr/bin/env python3
"""All20 protocol and actual SDK ingestion during audited TLS identity recovery."""
import argparse
import hashlib
import importlib.util
import json
import re
from pathlib import Path
import signal
import sys
from types import SimpleNamespace

ROOT = Path(__file__).resolve().parents[1]

def load(name, path):
    spec = importlib.util.spec_from_file_location(name, path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module

identity = load('all20_identity', ROOT / 'script/verify-reparse-identity-migration.py')
runner = load('identity_all20_runner', ROOT / 'tests/e2e20/runner.py')
require = identity.require
BOUNDARIES = ('authorized', 'applied30', 'rolledback')


def require_protected_unchanged(before, after):
    require(before == after, 'all20 changed pre-existing full task/detail rows, original audit, authorization or objects')
    digest = hashlib.sha256(json.dumps(before, sort_keys=True, ensure_ascii=False).encode()).hexdigest()
    return {'equal': True, 'sha256': digest}


class AllInterfacesAcceptance(identity.IdentityAcceptance):
    def exact_table(self, table):
        columns = [line.split('\t')[0] for line in self.sql('SHOW COLUMNS FROM ' + self.db + '.' + table).splitlines()]
        require(columns and all(re.fullmatch('[a-z_]+', c) for c in columns), 'unexpected protected columns')
        fields = ','.join("'%s',`%s`" % (c, c) for c in columns)
        raw = self.sql('SELECT HEX(CAST(JSON_OBJECT(' + fields + ') AS CHAR CHARACTER SET utf8mb4)) FROM '
                       + self.db + '.' + table + ' ORDER BY operation_id')
        return [json.loads(bytes.fromhex(line).decode()) for line in raw.splitlines()]

    def protected_state(self):
        saved = self.target
        try:
            business = {}
            for task in self.protected_tasks:
                self.target = task
                business[str(task)] = self.business()
            return {'business': business,
                    'originalAudit': self.exact_table('logan_native_reparse_operation'),
                    'authorization': self.exact_table(identity.AUTH),
                    'objects': self.snapshot('all20-protected-object-observation')['objects']}
        finally:
            self.target = saved

    def run_boundary(self, boundary):
        require(boundary in BOUNDARIES and boundary not in self.completed_boundaries, 'duplicate or unknown all20 boundary')
        require(self.sql('SELECT @@require_secure_transport') == '1', 'all20 requires secure MySQL transport')
        require(self.read_mysql_tls, 'all20 requires verified reader TLS')
        results = []
        original_args = self.args
        try:
            for mode in ('protocol', 'sdk'):
                before = self.protected_state()
                self.args = SimpleNamespace(**vars(original_args))
                self.args.fixture_mode = mode
                self.args.legacy_rsa_compat = mode == 'sdk' and original_args.legacy_rsa_compat
                scope = {'authorized': 'g', 'applied30': 'a', 'rolledback': 'r'}[boundary] + mode[0]
                self.dump('identity-all20-' + scope + '-protected-before', before)
                result = runner.run_all_interfaces(self, scope=scope)
                after = self.protected_state()
                self.dump('identity-all20-' + scope + '-protected-after', after)
                equality = require_protected_unchanged(before, after)
                require(result['runtimeAcceptance']['status'] == 'passed', 'all20 matrix acceptance failed')
                results.append({'fixtureMode': mode, 'acceptance': result['runtimeAcceptance'], 'protectedState': equality})
        finally:
            self.args = original_args
        self.completed_boundaries.append(boundary)
        self.dump('identity-all20-' + boundary, {'status': 'passed', 'boundary': boundary,
                  'databaseIdentity': self.target_identity, 'protectedTaskIDs': self.protected_tasks, 'runs': results})
        self.checks.append({'check': 'identity_all20_' + boundary, 'status': 'passed'})

    def after_authorizations(self):
        self.completed_boundaries = []
        # Capture every pre-existing Native fixture, including task20 and all recovery tasks.
        self.protected_tasks = [int(line) for line in self.sql('SELECT id FROM ' + self.db + '.logan_task ORDER BY id').splitlines()]
        require({20, 30, 40, 50} <= set(self.protected_tasks), 'recovery fixture tasks absent')
        self.events = []
        self.run_boundary('authorized')

    def reads(self, phase, task, deleted=()):
        result = super().reads(phase, task, deleted)
        if phase == 'm30a':
            self.run_boundary('applied30')
        return result

    def after_recovery(self):
        self.run_boundary('rolledback')
        require(tuple(self.completed_boundaries) == BOUNDARIES, 'all20 lifecycle boundary matrix incomplete')
        self.dump('identity-all20-events', self.events)


def main(argv=None):
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('--docker', required=True)
    p.add_argument('--host', required=True)
    p.add_argument('--allow-isolated-runtime', action='store_true')
    p.add_argument('--binary', type=Path, required=True)
    p.add_argument('--go-image', required=True)
    p.add_argument('--java-bundle', type=Path, required=True)
    p.add_argument('--sdk-fixtures', type=Path, required=True)
    p.add_argument('--legacy-rsa-compat', action='store_true', required=True,
                   help='explicitly permit unchanged original 1023-bit SDK RSA only for SDK runs')
    p.add_argument('--out', type=Path, required=True)
    p.add_argument('--work-dir', type=Path, required=True)
    a = identity.base.prior.previous.validate_args(p.parse_args(argv))
    a.java_bundle = a.java_bundle.resolve()
    a.sdk_fixtures = a.sdk_fixtures.resolve()
    require((a.java_bundle / 'run.sh').is_file(), 'prepared original Java bundle required')
    require((a.sdk_fixtures / 'manifest.json').is_file(), 'actual SDK manifest required')
    a.http_go_image = a.go_image
    a.namespace_prefix = 'identity'
    a.historical_objects = False
    for d in (a.out, a.work_dir):
        d.mkdir(mode=0o700, parents=True)
    a.out = a.out.resolve()
    a.work_dir = a.work_dir.resolve()
    v = AllInterfacesAcceptance(a)
    code, error = 0, None
    signal.signal(signal.SIGTERM, identity.base.prior.previous.interrupted)
    try:
        v.execute()
    except BaseException as e:
        code = identity.base.prior.previous.runtime.classify_exit(e, v.engine_ready)
        error = type(e).__name__ + ': ' + str(e).replace(v.password, '<synthetic-secret>')
    finally:
        if hasattr(v, 'events'):
            v.dump('identity-all20-events', v.events)
        code = identity.base.prior.finish(v, a, code, error)
        for folder in (a.out / 'tls-private', a.work_dir / 'tls'):
            if folder.exists():
                for f in folder.iterdir():
                    f.unlink()
                folder.rmdir()
        report = json.loads((a.out / 'summary.json').read_text())
        report['scope'] = 'isolated promoted TLS MySQL; original Java/Go all20 protocol and actual SDK at authorization, old30 apply and all old-operation rollback boundaries; no production/cloud'
        report['all20Boundaries'] = getattr(v, 'completed_boundaries', [])
        v.dump('summary', report)
    return code


if __name__ == '__main__':
    sys.exit(main())
