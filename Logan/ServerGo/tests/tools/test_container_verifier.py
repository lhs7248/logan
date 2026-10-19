"""Guard and failure tests: these do not claim actual Docker execution."""
import argparse
import importlib.util
import json
import os
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

SOURCE = Path(__file__).resolve().parents[2] / 'script' / 'verify-containers.py'
spec = importlib.util.spec_from_file_location('container_verifier', SOURCE)
v = importlib.util.module_from_spec(spec)
spec.loader.exec_module(v)


class ContainerVerifierTests(unittest.TestCase):
    def test_explicit_isolated_endpoint_only(self):
        for host in ('tcp://127.0.0.1:2375', 'ssh://logan-vm', 'unix:///var/run/docker.sock',
                     'unix:///Users/test/.colima/default/docker.sock', 'unix://relative',
                     'unix:///tmp/logan/../docker.sock'):
            with self.subTest(host=host), self.assertRaises(ValueError):
                v.validate_host(host, True)
        with self.assertRaises(ValueError):
            v.validate_host('unix:///tmp/logan-isolated/docker.sock', False)
        self.assertEqual(v.validate_host('unix:///tmp/logan-isolated/docker.sock', True),
                         'unix:///tmp/logan-isolated/docker.sock')

    def make_verifier(self, directory):
        return v.Verifier(argparse.Namespace(out=Path(directory), docker='/fake/docker',
                          host='unix:///tmp/logan-isolated/docker.sock', work_dir=None))

    def test_ambient_context_and_credentials_are_removed(self):
        with tempfile.TemporaryDirectory() as directory, patch.dict(os.environ, {
                'DOCKER_HOST': 'tcp://production:2375', 'DOCKER_CONTEXT': 'production',
                'DOCKER_CONFIG': '/production/credentials', 'BUILDX_BUILDER': 'remote'}):
            verifier = self.make_verifier(directory)
            self.assertNotIn('DOCKER_HOST', verifier.env)
            self.assertNotIn('DOCKER_CONTEXT', verifier.env)
            self.assertNotIn('BUILDX_BUILDER', verifier.env)
            self.assertEqual(verifier.env['DOCKER_CONFIG'], directory + '/docker-config')

    def test_cleanup_checks_labels_and_only_owned_names(self):
        with tempfile.TemporaryDirectory() as directory:
            verifier = self.make_verifier(directory)
            with self.assertRaises(AssertionError):
                verifier.reserve('container', 'production')
            name = verifier.reserve('container', verifier.prefix + '-app')
            calls = []
            def docker(*args, **kwargs):
                calls.append(args)
                if args[0] == 'inspect':
                    return 0, json.dumps([{'Id': 'sha256:' + 'a' * 64, 'Config': {'Labels': {v.LABEL: verifier.run_id}}}])
                return 0, ''
            verifier.docker = docker
            self.assertTrue(verifier.cleanup())
            self.assertEqual(calls, [('inspect', name), ('rm', '-f', name)])

    def test_cleanup_refuses_label_mismatch(self):
        with tempfile.TemporaryDirectory() as directory:
            verifier = self.make_verifier(directory)
            verifier.reserve('container', verifier.prefix + '-app')
            calls = []
            def docker(*args, **kwargs):
                calls.append(args)
                return 0, json.dumps([{'Config': {'Labels': {v.LABEL: 'another-run'}}}])
            verifier.docker = docker
            self.assertFalse(verifier.cleanup())
            self.assertEqual(len(calls), 1)

    def test_missing_engine_is_blocked_and_private_evidence(self):
        with tempfile.TemporaryDirectory() as directory:
            output = Path(directory) / 'evidence'
            code = v.main(['--docker', '/missing/logan-docker', '--host',
                           'unix:///tmp/logan-isolated/docker.sock',
                           '--allow-isolated-runtime', '--out', str(output)])
            summary = json.loads((output / 'summary.json').read_text())
            self.assertEqual(code, 2)
            self.assertEqual(summary['exit_code'], 2)
            self.assertTrue(any(c['status'] == 'blocked' for c in summary['checks']))
            self.assertEqual(output.stat().st_mode & 0o777, 0o700)
            self.assertEqual((output / 'summary.json').stat().st_mode & 0o777, 0o600)

    def test_fake_cli_receives_only_explicit_host_and_wrong_engine_cannot_build(self):
        with tempfile.TemporaryDirectory() as directory:
            fake = Path(directory) / 'docker'
            fake.write_text('#!/usr/bin/env python3\nimport json, sys\nprint(json.dumps({"OSType": "windows", "argv": sys.argv[1:]}))\n')
            fake.chmod(0o700)
            output = Path(directory) / 'evidence'
            code = v.main(['--docker', str(fake), '--host', 'unix:///tmp/logan/socket',
                           '--allow-isolated-runtime', '--out', str(output)])
            self.assertEqual(code, 1)
            logs = list(output.glob('command-*.json'))
            self.assertEqual(len(logs), 1)
            invocation = json.loads(logs[0].read_text())
            self.assertEqual(invocation['argv'][1:3], ['--host', 'unix:///tmp/logan/socket'])
            self.assertEqual(invocation['argv'][3], 'info')

    def test_readiness_requires_exact_http_503(self):
        with tempfile.TemporaryDirectory() as directory:
            verifier = self.make_verifier(directory)
            for status, output in ((1, 'connection refused'), (1, '  HTTP/1.1 500 Error\n'),
                                   (0, '  HTTP/1.1 200 OK\n'),
                                   (1, '  HTTP/1.1 302 Redirect\n  HTTP/1.1 503 Unavailable\n')):
                verifier.docker = lambda *args, result=(status, output), **kwargs: result
                with self.subTest(output=output), self.assertRaises(AssertionError):
                    verifier.readiness_blocked('app')
            verifier.docker = lambda *args, **kwargs: (1, '  HTTP/1.1 503 Service Unavailable\r\n')
            verifier.readiness_blocked('app')

    def test_keep_image_preserves_only_labeled_image(self):
        with tempfile.TemporaryDirectory() as directory:
            verifier = self.make_verifier(directory)
            verifier.args.keep_image = True
            image = verifier.reserve('image', verifier.tag)
            container = verifier.reserve('container', verifier.prefix + '-app')
            calls = []
            def docker(*args, **kwargs):
                calls.append(args)
                return 0, json.dumps([{'Id': 'sha256:' + 'a' * 64, 'Config': {'Labels': {v.LABEL: verifier.run_id}}}])
            verifier.docker = docker
            self.assertTrue(verifier.cleanup())
            self.assertIn(('rm', '-f', container), calls)
            self.assertNotIn(('image', 'rm', image), calls)
            self.assertEqual(verifier.retained_images[0]['tag'], image)

    def test_cleanup_transport_failure_is_not_missing(self):
        with tempfile.TemporaryDirectory() as directory:
            verifier = self.make_verifier(directory)
            verifier.reserve('container', verifier.prefix + '-app')
            verifier.docker = lambda *args, **kwargs: (1, 'Cannot connect to the Docker daemon: connection refused')
            self.assertFalse(verifier.cleanup())
            self.assertEqual(verifier.checks[-1]['status'], 'failed')

    def test_cleanup_skips_only_explicit_missing_resource(self):
        with tempfile.TemporaryDirectory() as directory:
            verifier = self.make_verifier(directory)
            name = verifier.reserve('container', verifier.prefix + '-app')
            verifier.docker = lambda *args, **kwargs: (1, '[]\nError: No such object: ' + name)
            self.assertTrue(verifier.cleanup())
            self.assertFalse(verifier.resource_missing('container', name, 'Error: No such object: ' + name + '\ntransport failure'))

    def test_build_proxy_allows_only_nonauthenticated_http_origins(self):
        for value in ('http://192.168.5.2:18080', 'https://proxy.local:443', 'http://[::1]:8080', 'http://localhost'):
            with self.subTest(value=value):
                self.assertEqual(v.validate_build_proxy(value), value)
        for value in ('socks5://localhost:1080', 'http://user:password@localhost:80',
                      'http://user@localhost', 'http://host/path', 'http://host?token=secret',
                      'http://host#secret', 'http://host:0', 'http://host:65536',
                      'http://host:', 'http://ho%73t', 'http://host\n', 'http://'):
            with self.subTest(value=value), self.assertRaises(ValueError):
                v.validate_build_proxy(value)

    def test_existing_output_directory_is_rejected(self):
        with tempfile.TemporaryDirectory() as directory:
            with self.assertRaises(SystemExit) as raised:
                v.main(['--docker', '/fake/docker', '--host', 'unix:///tmp/logan/socket',
                        '--allow-isolated-runtime', '--out', directory])
            self.assertEqual(raised.exception.code, 2)


if __name__ == '__main__':
    unittest.main()
