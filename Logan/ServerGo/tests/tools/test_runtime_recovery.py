import importlib.util
import json
from urllib.parse import unquote_plus
from pathlib import Path
import subprocess
import unittest

path = Path(__file__).resolve().parents[2] / 'script/verify-runtime-recovery.py'
spec = importlib.util.spec_from_file_location('runtime_recovery', path)
m = importlib.util.module_from_spec(spec)
spec.loader.exec_module(m)


class RuntimeRecoveryTests(unittest.TestCase):
    def test_barrier_requires_whole_line(self):
        self.assertTrue(m.exact_barrier('prefix\nPARSER_ENTERED\n', 'PARSER_ENTERED'))
        for text in ('PARSER_ENTERED false', 'prefix PARSER_ENTERED', 'PARSER_ENTERED_TOO'):
            self.assertFalse(m.exact_barrier(text, 'PARSER_ENTERED'))

    def test_unavailable_is_never_success(self):
        for error in (FileNotFoundError(), m.base.DockerError('offline'), subprocess.TimeoutExpired('docker', 1)):
            self.assertEqual(m.classify_exit(error, False), 2)
            self.assertEqual(m.classify_exit(error, True), 1)
        self.assertEqual(m.classify_exit(AssertionError('wrong rows'), False), 1)
        self.assertEqual(m.classify_exit(KeyboardInterrupt(), True), 130)

    def test_host_guards_are_reused(self):
        for host, allowed in [('unix:///var/run/docker.sock', True), ('tcp://localhost:2375', True), ('unix:///tmp/logan/docker.sock', False)]:
            with self.assertRaises(ValueError):
                m.base.validate_host(host, allowed)

    def test_unavailable_engine_never_reserves_image(self):
        verifier = object.__new__(m.RuntimeVerifier)
        verifier.owned = []
        verifier.engine_ready = False
        def unavailable(*args, **kwargs):
            raise m.base.DockerError('unavailable')
        verifier.docker = unavailable
        with self.assertRaises(m.base.DockerError):
            verifier.execute()
        self.assertEqual(verifier.owned, [])

    def test_web_fixture_uses_production_nested_string_encoding(self):
        outer = json.loads(m.web_fixture_content())
        self.assertEqual(len(outer), 1)
        self.assertIsInstance(outer[0], str)
        item = json.loads(outer[0])
        self.assertEqual(item, {'t': '1', 'c': 'web%20recovered', 'd': '1700000000000'})
        self.assertEqual(unquote_plus(item['c']), 'web recovered')

    def test_fixture_schema_comments_have_statement_boundaries(self):
        verifier = object.__new__(m.RuntimeVerifier)
        verifier.run_id = 'a' * 32
        queries = []
        verifier.sql = queries.append
        db = verifier.fixture('native-drain')
        self.assertTrue(queries[0].startswith('CREATE DATABASE ' + db + ';\nUSE ' + db + ';\n-- Original'))
        self.assertIn('CONVERT(0x' + m.web_fixture_content().encode().hex() + ' USING utf8mb4)', queries[0])

    def test_sql_uses_tcp_to_exclude_bootstrap_socket(self):
        verifier = object.__new__(m.RuntimeVerifier)
        verifier.password = 'synthetic'
        verifier.mysql = 'isolated-mysql'
        commands = []
        def docker(*args, **kwargs):
            commands.append(args)
            return 0, '1\n'
        verifier.docker = docker
        self.assertEqual(verifier.sql('SELECT 1'), '1')
        self.assertIn('--protocol=TCP', commands[0])
        self.assertIn('--host=127.0.0.1', commands[0])
