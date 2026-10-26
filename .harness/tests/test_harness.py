import importlib.util
import json
import shutil
import sys
import subprocess
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

SCRIPT = Path(__file__).resolve().parents[1] / 'run.py'
spec = importlib.util.spec_from_file_location('harness', SCRIPT)
harness = importlib.util.module_from_spec(spec)
spec.loader.exec_module(harness)


class HarnessTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name)
        tool_lookup = patch.object(harness.shutil, "which", return_value="available-tool")
        tool_lookup.start()
        self.addCleanup(tool_lookup.stop)
        for name, value in (('identity', {'commit': 'fixture'}), ('versions', {})):
            mock = patch.object(harness.runtime, name, return_value=value)
            mock.start()
            self.addCleanup(mock.stop)
        shutil.copytree(SCRIPT.parent, self.root / '.harness')
        (self.root / 'AGENTS.md').write_text('See [.harness](.harness/README.md)')
        for name in ('README.md', 'Makefile', 'docs/progress.md'):
            p = self.root / 'Logan/ServerGo' / name
            p.parent.mkdir(parents=True, exist_ok=True)
            p.touch()
        p = self.root / 'Logan/LoganSite/package.json'
        p.parent.mkdir(parents=True)
        p.write_text('{}')
        for name in (
            'docs/product/2026-10-21-logan-workbench/product-design.md',
            'docs/product/2026-10-21-logan-workbench/aliyun-retrieval-flow.md',
            'prototypes/logan-investigation/README.md',
        ):
            p = self.root / name
            p.parent.mkdir(parents=True, exist_ok=True)
            p.touch()

        for item in harness.read_registry(self.root):
            p = self.root / item['script']
            p.parent.mkdir(parents=True, exist_ok=True)
            p.touch()

    def test_required_registry_route_cannot_disappear(self):
        p = self.root / '.harness/registry.json'
        doc = json.loads(p.read_text())
        doc['integrations'] = [x for x in doc['integrations'] if x['id'] != 'manual-http']
        p.write_text(json.dumps(doc))
        self.assertTrue(harness.check(self.root))

    def test_registry_route_cannot_change_profile(self):
        p = self.root / '.harness/registry.json'
        doc = json.loads(p.read_text())
        doc['integrations'][0]['profile'] = 'full'
        p.write_text(json.dumps(doc))
        self.assertTrue(harness.check(self.root))

    def test_valid(self):
        self.assertEqual(harness.check(self.root), [])

    def test_missing_document(self):
        (self.root / '.harness/rules/compatibility.md').unlink()
        self.assertTrue(harness.check(self.root))

    def test_missing_design_baseline_is_reported(self):
        (self.root / 'docs/product/2026-10-21-logan-workbench/product-design.md').unlink()
        errors = harness.check(self.root)
        self.assertTrue(any('design-fidelity.md' in error and 'product-design.md' in error
                            for error in errors), errors)

    def test_broken_link(self):
        (self.root / 'AGENTS.md').write_text('[missing](missing.md)')
        self.assertTrue(harness.check(self.root))

    def test_unknown_case_reference(self):
        p = self.root / '.harness/evals/cases.json'
        cases = json.loads(p.read_text())
        cases[0]['references'] = ['nonexistent']
        p.write_text(json.dumps(cases))
        self.assertTrue(harness.check(self.root))

    def test_machine_path(self):
        (self.root / 'AGENTS.md').write_text('/Users/example/project')
        self.assertTrue(harness.check(self.root))

    def test_invalid_profile(self):
        p = self.root / '.harness/manifest.json'
        data = json.loads(p.read_text())
        data['profiles']['quick'] = ['deploy']
        p.write_text(json.dumps(data))
        self.assertTrue(harness.check(self.root))

    def test_interruption_report(self):
        evidence = self.root / 'interrupted'
        evidence.mkdir()
        with patch.object(harness.tempfile, 'mkdtemp', return_value=str(evidence)), patch.object(harness, 'execute', side_effect=KeyboardInterrupt):
            self.assertEqual(harness.verify(self.root, 'quick'), 130)
        report = json.loads((evidence / 'summary.json').read_text())
        self.assertEqual(report['steps'][0]['status'], 'interrupted')
        self.assertEqual(report['unexecuted'], ['vet'])

    def test_weakened_full_profile_rejected(self):
        p = self.root / '.harness/manifest.json'
        data = json.loads(p.read_text())
        data['profiles']['full'] = ['test']
        p.write_text(json.dumps(data))
        self.assertTrue(harness.check(self.root))

    def test_unregistered_file_rejected(self):
        (self.root / '.harness/rules/forgotten.md').write_text('Unregistered')
        self.assertTrue(harness.check(self.root))

    def test_malformed_configuration_rejected(self):
        p = self.root / '.harness/manifest.json'
        original = p.read_text()
        for value in ([], None, {'assets': [], 'profiles': []}):
            with self.subTest(value=value):
                p.write_text(json.dumps(value))
                self.assertTrue(harness.check(self.root))
        p.write_text(original)
        p = self.root / '.harness/evals/cases.json'
        p.write_text('[{"id": "bad", "assertions": "not a list", "references": []}]')
        self.assertTrue(harness.check(self.root))

    def test_unexpected_error_never_reports_success(self):
        evidence = self.root / 'error'
        evidence.mkdir()
        with patch.object(harness.tempfile, 'mkdtemp', return_value=str(evidence)), patch.object(harness, 'execute', side_effect=RuntimeError('runner failure')):
            self.assertEqual(harness.verify(self.root, 'quick'), 1)
        report = json.loads((evidence / 'summary.json').read_text())
        self.assertEqual(report['exit_code'], 1)
        self.assertEqual(report['steps'][0]['status'], 'error')

    def test_missing_tool_blocks_without_execution(self):
        evidence = self.root / 'blocked'
        evidence.mkdir()
        with patch.object(harness.tempfile, 'mkdtemp', return_value=str(evidence)), patch.object(harness.shutil, 'which', return_value=None), patch.object(harness, 'execute') as execute:
            self.assertEqual(harness.verify(self.root, 'java'), 1)
            execute.assert_not_called()
        report = json.loads((evidence / 'summary.json').read_text())
        self.assertEqual(report['steps'][0]['status'], 'blocked')
        self.assertIn('mvn', report['steps'][0]['reason'])

    def test_empty_test_suite_fails(self):
        with patch.object(harness.unittest.defaultTestLoader, 'discover', return_value=unittest.TestSuite()):
            self.assertEqual(harness.self_test(self.root), 1)

    def test_cli_from_other_directory_and_invalid_config(self):
        command = [sys.executable, '-B', str(self.root / '.harness/run.py'), 'check']
        result = subprocess.run(command, cwd='/', capture_output=True, text=True)
        self.assertEqual(result.returncode, 0, result.stderr)
        (self.root / '.harness/manifest.json').write_text('[]')
        result = subprocess.run(command, cwd='/', capture_output=True, text=True)
        self.assertEqual(result.returncode, 1)
        self.assertNotIn('Traceback', result.stderr)

    def test_deleted_asset_and_registration_rejected(self):
        p = self.root / '.harness/manifest.json'
        data = json.loads(p.read_text())
        name = '.harness/workflows/bugfix.md'
        data['assets'].remove(name)
        (self.root / name).unlink()
        p.write_text(json.dumps(data))
        self.assertTrue(harness.check(self.root))

    def test_command_results(self):
        for exit_code in (0, 7):
            result = harness.execute([sys.executable, '-c', 'raise SystemExit(%d)' % exit_code], self.root, self.root / 'run.log')
            self.assertEqual(result['exit_code'], exit_code)
            self.assertEqual(result['status'], 'passed' if exit_code == 0 else 'failed')
        result = harness.execute([str(self.root / 'missing-tool')], self.root, self.root / 'run.log')
        self.assertEqual(result['status'], 'blocked')

    def test_failure_report_and_continuation(self):
        evidence = self.root / 'evidence'
        evidence.mkdir()
        with patch.object(harness.tempfile, 'mkdtemp', return_value=str(evidence)), patch.object(harness, 'execute', side_effect=[{'status': 'failed', 'exit_code': 2}, {'status': 'passed', 'exit_code': 0}]):
            self.assertEqual(harness.verify(self.root, 'quick'), 1)
        report = json.loads((evidence / 'summary.json').read_text())
        self.assertEqual(report['exit_code'], 1)
        self.assertEqual(len(report['steps']), 2)
        self.assertEqual(report['unexecuted'], [])


if __name__ == '__main__':
    unittest.main()
