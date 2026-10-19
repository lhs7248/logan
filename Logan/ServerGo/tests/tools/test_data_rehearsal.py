"""Negative safety/ordering tests. No Docker daemon or database is contacted."""
import importlib.util
from pathlib import Path
from types import SimpleNamespace
import unittest

path = Path(__file__).resolve().parents[2] / 'script/rehearse-data-rollback.py'
spec = importlib.util.spec_from_file_location('data_rehearsal', path)
m = importlib.util.module_from_spec(spec)
spec.loader.exec_module(m)


class DataRehearsalTests(unittest.TestCase):
    def verifier(self):
        v = object.__new__(m.DataRehearsal)
        v.prefix = 'fixture'
        v.run_id = 'a' * 32
        v.mysql = 'fixture-mysql'
        v.db = 'logan_crossruntime_' + v.run_id
        v.password = 'synthetic-only'
        v.network = 'fixture-network'
        v.tag = 'fixture-image'
        v.events = []
        v.shared = Path('/private/tmp/fixture-evidence')
        v.args = SimpleNamespace(java_bundle=Path('/private/tmp/fixture-java'))
        v.reserve = lambda kind, name: name
        v.private = lambda name, content: Path('/private/tmp/' + name)
        return v

    def test_old_phase_must_exit_before_stop_event(self):
        v = self.verifier()
        v.wait_exit = lambda *_: None
        v.docker = lambda *args, **kwargs: (0, 'true' if args[0] == 'inspect' else ('OK (1 test)\nJAVA_SEED_VERIFIED\n' if args[0] == 'logs' else ''))
        with self.assertRaisesRegex(AssertionError, 'old phase must stop'):
            v.launch_phase('java-seed')
        self.assertEqual([e['event'] for e in v.events], ['started'])

    def test_failed_phase_cannot_record_successful_stop(self):
        v = self.verifier()
        v.docker = lambda *_args, **_kwargs: (0, '')
        def failed(*_):
            raise AssertionError('unexpected container exit: 1')
        v.wait_exit = failed
        with self.assertRaisesRegex(AssertionError, 'unexpected container exit'):
            v.launch_phase('go-process')
        self.assertEqual([e['event'] for e in v.events], ['started'])

    def test_java_container_receives_explicit_phase_and_restricted_mount(self):
        v = self.verifier()
        calls, private = [], []
        v.private = lambda name, content: private.append((name, content)) or Path('/private/tmp/' + name)
        v.wait_exit = lambda *_: None
        def docker(*args, **kwargs):
            calls.append(args)
            return 0, 'false' if args[0] == 'inspect' else ('OK (1 test)\nJAVA_ROLLBACK_VERIFIED\n' if args[0] == 'logs' else '')
        v.docker = docker
        v.launch_phase('java-verify')
        launch = calls[0]
        self.assertIn('--read-only', launch)
        self.assertIn('type=bind,src=/private/tmp/fixture-java,dst=/java,readonly', launch)
        self.assertIn('CROSSRUNTIME_PHASE=verify', private[0][1])
        self.assertIn('CROSSRUNTIME_DISPOSABLE=YES', private[0][1])
        self.assertNotIn('CROSSRUNTIME_PHASE=seed', private[0][1])
        self.assertEqual([e['event'] for e in v.events], ['started', 'stopped_exit0'])

    def test_unavailable_engine_creates_no_resources(self):
        v = self.verifier()
        resources = []
        v.reserve = lambda *args: resources.append(args)
        v.engine_ready = False
        def unavailable(*_args, **_kwargs):
            raise m.base.DockerError('unavailable')
        v.docker = unavailable
        with self.assertRaises(m.base.DockerError):
            v.execute()
        self.assertEqual(resources, [])
        self.assertFalse(v.engine_ready)

    def test_non_linux_engine_creates_no_resources(self):
        v = self.verifier()
        resources = []
        v.reserve = lambda *args: resources.append(args)
        v.docker = lambda *_args, **_kwargs: (0, '{"OSType":"windows"}')
        with self.assertRaisesRegex(AssertionError, 'Linux engine required'):
            v.execute()
        self.assertEqual(resources, [])

    def test_shared_default_or_tcp_host_never_accepted(self):
        for host in ('unix:///var/run/docker.sock', 'tcp://127.0.0.1:2375'):
            with self.subTest(host=host), self.assertRaises(ValueError):
                m.base.validate_host(host, True)


http_path = path.parents[1] / 'tests/crossruntime/http_roundtrip.py'
http_spec = importlib.util.spec_from_file_location('http_roundtrip', http_path)
http = importlib.util.module_from_spec(http_spec)
http_spec.loader.exec_module(http)


class HTTPProjectionTests(unittest.TestCase):
    def response(self, data=None, status=200, code=200, content_type='application/json;charset=UTF-8'):
        return {'status': status, 'contentType': content_type,
                'body': {'code': code, 'data': {'id': 7, 'content': '中文 + %'} if data is None else data}}

    def test_unavailable_or_error_envelope_never_passes(self):
        for response in (self.response(status=503), self.response(status=501),
                         self.response(status=500), self.response(code=500), self.response(code=400)):
            with self.subTest(response=response), self.assertRaises(AssertionError):
                http.projection(response, ['id', 'content'])

    def test_html_and_empty_data_cannot_pass(self):
        for response in (self.response(content_type='text/html'), self.response(data=[]),
                         {'status': 200, 'contentType': 'application/json', 'body': {'code': 200}}):
            with self.subTest(response=response), self.assertRaises(AssertionError):
                http.projection(response, ['id'])

    def test_missing_stable_id_is_rejected(self):
        for data in ({'content': '中文 + %'}, [{'content': '中文 + %'}]):
            with self.subTest(data=data), self.assertRaisesRegex(AssertionError, 'fields missing'):
                http.projection(self.response(data=data), ['id', 'content'])

    def test_payload_and_ids_remain_visible_in_projection(self):
        original = http.projection(self.response(), ['id', 'content'])
        changed_payload = http.projection(self.response(data={'id': 7, 'content': 'wrong'}), ['id', 'content'])
        changed_id = http.projection(self.response(data={'id': 8, 'content': '中文 + %'}), ['id', 'content'])
        self.assertNotEqual(original, changed_payload)
        self.assertNotEqual(original, changed_id)
        self.assertEqual(original, {'id': 7, 'content': '中文 + %'})

    def test_index_projection_preserves_exact_order_and_ids(self):
        data = [[{'id': 7, 'logType': 1, 'logTime': 123}, {'id': 8, 'logType': 1, 'logTime': 124}]]
        self.assertEqual(http.projection(self.response(data=data), None), data)
        reversed_rows = [list(reversed(data[0]))]
        self.assertNotEqual(http.projection(self.response(data=data), None),
                            http.projection(self.response(data=reversed_rows), None))

    def test_empty_nested_or_unrelated_index_cannot_pass(self):
        for data in ([[]], [], [[{'logType': 1}]], [[{'id': 8}]], [[{'detailId': 8}]]):
            with self.subTest(data=data), self.assertRaisesRegex(AssertionError, 'expected published detail ID'):
                http.require_index_id(data, 7)

    def test_expected_native_id_and_web_detail_id_are_accepted(self):
        native = [[{'id': 7, 'logType': 1, 'logTime': 123}]]
        web = [[{'detailId': 12, 'logType': 1, 'logTime': 124}]]
        http.require_index_id(native, 7)
        http.require_index_id(web, 12)
        # These different DTO key names must still identify the exact publication.
        with self.assertRaises(AssertionError):
            http.require_index_id(native, 12)
        with self.assertRaises(AssertionError):
            http.require_index_id(web, 7)

    def test_owned_http_hostnames_fit_real_dns_label_limits(self):
        prefix = 'logan-verifier-' + 'a' * 32
        names = http.http_hostnames(prefix)
        self.assertEqual(len(names), 7)
        self.assertEqual(len(set(names.values())), 7)
        self.assertTrue(all(name.startswith(prefix) and len(name) <= 63 for name in names.values()))
        with self.assertRaisesRegex(AssertionError, 'DNS label'):
            http.http_hostnames('a' * 63)

    def test_failed_diagnostics_do_not_hide_original_error(self):
        calls = []
        class Verifier:
            def docker(self, *args, **kwargs):
                calls.append((args, kwargs))
                raise OSError('diagnostic connection failed')
        http.capture_app_diagnostics(Verifier(), 'owned-app')
        self.assertEqual([call[0] for call in calls], [('inspect', 'owned-app'), ('logs', 'owned-app')])
        self.assertTrue(all(call[1]['accept'] for call in calls))


if __name__ == '__main__':
    unittest.main()
