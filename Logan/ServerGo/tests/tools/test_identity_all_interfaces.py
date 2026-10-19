import copy
import importlib.util
from pathlib import Path
from types import SimpleNamespace
import tempfile
import unittest
from unittest.mock import patch

ROOT = Path(__file__).resolve().parents[2]
spec = importlib.util.spec_from_file_location('identity_all20', ROOT / 'script/verify-identity-all-interfaces.py')
m = importlib.util.module_from_spec(spec); spec.loader.exec_module(m)


class IdentityAllInterfacesTests(unittest.TestCase):
    def verifier(self):
        v = m.AllInterfacesAcceptance.__new__(m.AllInterfacesAcceptance)
        v.args = SimpleNamespace(fixture_mode='original', legacy_rsa_compat=True)
        v.completed_boundaries = []
        v.protected_tasks = [20, 30, 40, 50]
        v.read_mysql_tls = {'fixture': True}
        v.sql = lambda query: '1'
        v.target_identity = 'promoted/db'
        v.db = 'db'
        v.checks = []
        v.protected_state = lambda: {'task': {'amount': None}, 'audit': 'exact', 'auth': 'exact'}
        v.dump = lambda *args: None
        return v

    def test_all_boundaries_run_both_fixture_modes_on_same_verifier(self):
        v = self.verifier(); calls = []
        def run(actual, scope):
            self.assertIs(actual, v)
            self.assertEqual(actual.args.legacy_rsa_compat, actual.args.fixture_mode == 'sdk')
            calls.append((actual.args.fixture_mode, scope, actual.target_identity))
            return {'runtimeAcceptance': {'status': 'passed'}}
        with patch.object(m.runner, 'run_all_interfaces', side_effect=run):
            for boundary in m.BOUNDARIES:
                v.run_boundary(boundary)
        self.assertEqual([c[:2] for c in calls], [('protocol','gp'),('sdk','gs'),('protocol','ap'),('sdk','as'),('protocol','rp'),('sdk','rs')])
        self.assertEqual(v.completed_boundaries, list(m.BOUNDARIES))
        self.assertEqual(v.args.fixture_mode, 'original')
        with self.assertRaises(AssertionError):
            v.run_boundary('authorized')

    def test_each_run_must_preserve_every_protected_field(self):
        before = {'business': {'20': {'task': [{'status':1, 'amount':None}], 'details':[{'id':1,'content':'x','update_time':'t'}]}}, 'originalAudit':'bytes', 'authorization':'grant', 'objects':{'history':'digest'}}
        m.require_protected_unchanged(before, copy.deepcopy(before))
        for field in before:
            after = copy.deepcopy(before); after[field] = {}
            with self.subTest(field=field), self.assertRaises(AssertionError):
                m.require_protected_unchanged(before, after)

    def test_failure_does_not_mark_boundary_complete_and_restores_args(self):
        v = self.verifier(); original = v.args
        with patch.object(m.runner, 'run_all_interfaces', side_effect=RuntimeError('failed')):
            with self.assertRaises(RuntimeError): v.run_boundary('authorized')
        self.assertIs(v.args, original)
        self.assertEqual(v.completed_boundaries, [])

    def test_secure_transport_is_mandatory_before_any_all20(self):
        v = self.verifier(); v.sql = lambda query: '0'
        with patch.object(m.runner, 'run_all_interfaces') as run:
            with self.assertRaises(AssertionError): v.run_boundary('authorized')
            run.assert_not_called()
        v.sql = lambda query: '1'; v.read_mysql_tls = None
        with self.assertRaises(AssertionError): v.run_boundary('authorized')

    def test_scoped_resources_evidence_and_secrets_are_distinct(self):
        with tempfile.TemporaryDirectory() as directory:
            parent = SimpleNamespace(prefix='logan-verifier-'+'a'*32, args=SimpleNamespace(out=Path(directory)),
                                     events=[], private=lambda name, content: (name, content))
            one = m.runner.ScopedRehearsal(parent, 'gp')
            two = m.runner.ScopedRehearsal(parent, 'gs')
            self.assertNotEqual(one.prefix, two.prefix)
            self.assertTrue(one.prefix.startswith(parent.prefix))
            self.assertLess(len('fixture.'+one.prefix+'-o'), 64)
            self.assertNotEqual(one.args.out, two.args.out)
            self.assertIs(one.events, parent.events)
            self.assertEqual(one.private('go.env', 'secret'), ('gp-go.env', 'secret'))
            self.assertEqual(parent.args.out, Path(directory))

    def test_exact_audit_transport_preserves_null_unicode_and_control_characters(self):
        import json
        v = self.verifier()
        rows = [{'operation_id': 'op', 'note': None},
                {'operation_id': 'op2', 'note': 'NULL\t中文\n'}]
        queries = []
        def sql(query):
            queries.append(query)
            if query.startswith('SHOW COLUMNS'):
                return 'operation_id\tvarchar\nnote\ttext'
            return '\n'.join(json.dumps(row, ensure_ascii=False).encode().hex() for row in rows)
        v.sql = sql
        self.assertEqual(v.exact_table('logan_native_reparse_operation'), rows)
        self.assertIn('HEX(CAST(JSON_OBJECT(', queries[-1])
        self.assertTrue(queries[-1].endswith('ORDER BY operation_id'))

    def test_early_war_build_failure_removes_staged_secrets(self):
        with tempfile.TemporaryDirectory() as directory:
            v = SimpleNamespace(args=SimpleNamespace(work_dir=Path(directory)))
            work = Path(directory) / 'e2e20-gs'
            (work / 'secrets').mkdir(parents=True)
            key = work / 'secrets/fixture-rsa.pem'; key.write_text('private fixture key')
            war = work / 'logan-web.war'; war.write_text('embedded synthetic credentials')
            with patch.object(m.runner, '_run_all_interfaces', side_effect=RuntimeError('WAR assembly failed')):
                with self.assertRaisesRegex(RuntimeError, 'WAR assembly'):
                    m.runner.run_all_interfaces(v, scope='gs')
            self.assertFalse(key.exists())
            self.assertFalse(war.exists())

    def test_transport_counts_follow_only_positive_exact_native_downloads(self):
        passed = {'status': 'passed'}
        for fixture, java_download, minimum in (('sdk', False, 3), ('protocol', True, 4)):
            results = {'java': {'coveredRoutes': ['native-download'] if java_download else []},
                       'go': {'coveredRoutes': ['native-download']}}
            logs = 'OSS_FIXTURE_PUT\n' * 2 + 'OSS_FIXTURE_GET\n' * minimum
            with self.subTest(fixture=fixture):
                evidence = m.runner.transport_evidence(results, passed, logs)
                self.assertEqual(evidence['expected']['getMinimum'], minimum)
                self.assertEqual(evidence['actual'], {'puts': 2, 'gets': minimum})
                with self.assertRaises(AssertionError):
                    m.runner.transport_evidence(results, passed, 'OSS_FIXTURE_PUT\n' * 2 + 'OSS_FIXTURE_GET\n' * (minimum-1))
                with self.assertRaises(AssertionError):
                    m.runner.transport_evidence(results, {'status': 'failed'}, logs)

    def test_original_identity_hooks_remain_noops(self):
        v = m.identity.IdentityAcceptance.__new__(m.identity.IdentityAcceptance)
        self.assertIsNone(v.after_authorizations())
        self.assertIsNone(v.after_recovery())


if __name__ == '__main__': unittest.main()
