import importlib.util
from pathlib import Path
import unittest
import subprocess
import sys
import tempfile
import json

ROOT = Path(__file__).resolve().parents[2]
def load(name, path):
    spec = importlib.util.spec_from_file_location(name, path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module
runner = load('rehearse', ROOT / 'script/rehearse-cutover.py')
fixture = load('fixture', ROOT / 'tests/rehearsal/fixture.py')

class SafetyTests(unittest.TestCase):
    def test_switch_requires_closed_entry(self):
        with self.assertRaises(ValueError):
            runner.allowed_transition('java', 'go', False, True)

    def test_second_backend_cannot_open(self):
        for target in ('java', 'go'):
            with self.assertRaises(ValueError):
                runner.allowed_transition('closed', target, True, True)

    def test_absent_backend_cannot_open(self):
        for target in ('java', 'go'):
            with self.assertRaises(ValueError):
                runner.allowed_transition('closed', target, False, False)

    def test_closed_can_stop_all_traffic(self):
        self.assertEqual(runner.allowed_transition('go', 'closed', False, True), 'closed')

    def test_optimized_checks_still_fail(self):
        command = "import runpy; m=runpy.run_path(" + repr(str(ROOT / 'script/rehearse-cutover.py')) + "); m['require'](False, 'expected failure')"
        result = subprocess.run([sys.executable, '-O', '-c', command], capture_output=True)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn(b'expected failure', result.stderr)

    def test_default_socket_alias_rejected(self):
        with tempfile.TemporaryDirectory() as tmp:
            alias = Path(tmp) / 'logan-dedicated.sock'
            alias.symlink_to('/var/run/docker.sock')
            with self.assertRaises(ValueError):
                runner.validate_host('unix://' + str(alias), True)
        for host in ('unix:///run/docker.sock', 'unix:///tmp/logan/../docker.sock'):
            with self.assertRaises(ValueError):
                runner.validate_host(host, True)

    def test_interrupt_unwinds_finally(self):
        cleaned = []
        with self.assertRaises(KeyboardInterrupt):
            try:
                runner.interrupt(15, None)
            finally:
                cleaned.append(True)
        self.assertEqual(cleaned, [True])

    def test_cleanup_refuses_foreign_label(self):
        calls = []
        def docker(*args):
            calls.append(args)
            return json.dumps([{'Config': {'Labels': {'logan.rehearsal': 'other'}}}])
        errors = runner.cleanup_owned(docker, [('container', 'owned-name')], 'expected')
        self.assertEqual(len(errors), 1)
        self.assertEqual(len(calls), 1)

    def test_cleanup_surfaces_removal_failure(self):
        def docker(*args):
            if args[1] == 'inspect':
                return json.dumps([{'Labels': {'logan.rehearsal': 'expected'}}])
            raise RuntimeError('removal blocked')
        self.assertIn('removal blocked', runner.cleanup_owned(docker, [('network', 'own')], 'expected')[0])

    def test_interrupted_creation_reserved_for_cleanup(self):
        resources = []
        def interrupted():
            raise KeyboardInterrupt()
        with self.assertRaises(KeyboardInterrupt):
            runner.reserve_and_create(resources, ('network', 'own'), interrupted)
        self.assertEqual(resources, [('network', 'own')])

    def test_cleanup_timeout_is_reported(self):
        def docker(*args):
            raise subprocess.TimeoutExpired(args, 1)
        self.assertEqual(len(runner.cleanup_owned(docker, [('network', 'own')], 'owner')), 1)

    def test_cleanup_missing_resource_is_benign(self):
        def docker(*args):
            raise RuntimeError('Error response from daemon: No such container: own')
        self.assertEqual(runner.cleanup_owned(docker, [('container', 'own')], 'owner'), [])

    def test_cleanup_connection_failure_is_reported(self):
        def docker(*args):
            raise OSError('connection refused')
        self.assertEqual(len(runner.cleanup_owned(docker, [('container', 'own')], 'owner')), 1)

    def test_container_http_probe_preserves_failure_body_and_identity(self):
        status, body, headers = runner.decode_probe('[503,"AP8=",{"X-Rehearsal-Backend":"closed"}]')
        self.assertEqual((status, body, headers['X-Rehearsal-Backend']), (503, b'\x00\xff', 'closed'))
        with self.assertRaises(AssertionError):
            runner.decode_probe('["200", "", {}]')

    def test_legacy_uri(self):
        self.assertEqual(fixture.mapped('/logan/a/?q=1&q=2'), '/logan-web/logan/a/?q=1&q=2')
        self.assertEqual(fixture.mapped('/loganother'), '/loganother')
        self.assertEqual(fixture.mapped('/logan?q=1'), '/logan-web/logan?q=1')

if __name__ == '__main__':
    unittest.main()
