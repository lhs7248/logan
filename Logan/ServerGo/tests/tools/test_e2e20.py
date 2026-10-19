"""Focused controller/probe invariants; real app evidence requires the opt-in runner."""
import contextlib
import importlib.util
import io
import json
import hashlib
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

ROOT = Path(__file__).resolve().parents[2]


def load(name):
    spec = importlib.util.spec_from_file_location('e20_' + name, ROOT / 'tests/e2e20' / (name + '.py'))
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


class E2E20Tests(unittest.TestCase):
    def test_native_members_require_complete_members_and_bounded_declared_padding(self):
        probe = load('probe')
        import gzip
        first, second = gzip.compress(b'first'), gzip.compress(b'second')
        for gap in (b'\x00' * 8, b'\x08' * 8):
            self.assertEqual(probe.decode_native_members(first + gap + second), b'firstsecond')
        for invalid in (first + b'junk' + second, first + b'\x00' * 17 + second, first[:-2]):
            with self.assertRaises(AssertionError):
                probe.decode_native_members(invalid)

    def test_sdk_acceptance_requires_exact_java_loss_and_go_all20(self):
        runner = load('runner')
        routes = {'native-' + str(i) for i in range(10)} | {'web-' + str(i) for i in range(10)}
        routes -= {'native-' + str(i) for i in range(5)}; routes |= {'native-brief', 'native-upload', 'native-search', 'native-latest', 'native-types'}
        java_routes = {r for r in routes if r.startswith('web-')} | {'native-upload', 'native-search', 'native-latest', 'native-types'}
        def capture(names, failures):
            return {'coveredRoutes': sorted(names), 'responses': {r: {'status': 200} for r in names}, 'flowFailures': failures}
        results = {'java': capture(java_routes, [{'flow': 'native', 'route': 'native-brief', 'errorType': 'AssertionError', 'reason': 'native parse must publish every uploaded record'}]),
                   'go': capture(routes, [])}
        results['java']['observations'] = {'nativePublication': {'expectedRecords': 2, 'observedRecords': 1, 'firstRecordMatchesOracle': True}}
        result = runner.acceptance(results, routes, 'sdk')
        self.assertEqual(result['status'], 'passed')
        self.assertEqual(result['runtimes']['java']['status'], 'failed')
        self.assertEqual(result['runtimes']['go']['status'], 'passed')
        self.assertEqual(runner.acceptance(results, routes, 'protocol')['status'], 'failed')
        results['go']['responses']['web-0']['status'] = 500
        self.assertEqual(runner.acceptance(results, routes, 'sdk')['status'], 'failed')

    def test_historical_objects_are_two_members_with_exact_gap_oracles(self):
        fixtures, probe = load('fixtures'), load('probe')
        import base64
        cases = fixtures.historical_objects()
        self.assertEqual({case['gap'] for case in cases}, {'zero', 'pkcs'})
        for case in cases:
            plain = probe.decode_native_members(base64.b64decode(case['objectBase64']))
            self.assertEqual([json.loads(line)['c'] for line in plain.splitlines()], case['contents'])
            self.assertEqual(len(case['contents']), 3)

    def test_historical_unchanged_rejects_same_count_content_or_timestamp_change(self):
        runner = load('runner')
        import copy
        original = {'task': {'id': 7, 'status': 1, 'update_time': '2026-10-15 00:00:00'},
                    'details': [{'id': 8, 'task_id': 7, 'content': 'first', 'add_time': '2026-10-15 00:00:00'}]}
        runner.require_historical_unchanged(original, copy.deepcopy(original))
        for target, field, value in [('details', 'content', 'changed'), ('details', 'id', 9),
                                     ('details', 'add_time', '2026-10-16 00:00:00'),
                                     ('task', 'update_time', '2026-10-16 00:00:00')]:
            changed = copy.deepcopy(original)
            row = changed[target][0] if target == 'details' else changed[target]
            row[field] = value
            with self.assertRaisesRegex(AssertionError, 'full task and ordered detail rows'):
                runner.require_historical_unchanged(original, changed)

    def test_java_download_oracle_uses_original_iv_final_block_probe(self):
        fixtures = load('fixtures')
        key = iv = b'0123456789012345'
        observed = set()
        for size in range(1, 200):
            plain = bytes(range(size))
            encrypted = fixtures.openssl(['enc', '-aes-128-cbc', '-K', key.hex(), '-iv', iv.hex()], plain)
            raw = fixtures.openssl(['enc', '-d', '-aes-128-cbc', '-nopad', '-K', key.hex(), '-iv', iv.hex()], encrypted)
            probe = fixtures.openssl(['enc', '-d', '-aes-128-cbc', '-nopad', '-K', key.hex(), '-iv', iv.hex()], encrypted[-16:])
            pad = probe[-1]
            selects_padding = 1 <= pad <= 16 and probe[-pad:] == bytes([pad]) * pad
            expected = plain if selects_padding else raw
            self.assertEqual(fixtures.java_download_frame(encrypted, key.hex(), iv.hex()), expected)
            observed.add(selects_padding)
            if len(observed) == 2:
                break
        self.assertEqual(observed, {True, False})

    def test_table_local_ids_are_discovered(self):
        probe = load('probe')
        self.assertEqual(list(probe.nested_ids([[{'id': 4, 'logType': 1}], [{'id': 8}]])), [4, 8])
        self.assertEqual(probe.normalize('/logan/downing?name=secret-object', {}, 'fixture'), '/logan/downing?name=<object>')
        self.assertEqual(probe.normalize({'deviceId': 'fixture-java', 'addTime': 123, 'status': 1}, {}, 'fixture-java'),
                         {'deviceId': '<device>', 'status': 1})

    def test_fixture_byte_transport_and_secret_free_log(self):
        fixture = load('oss_fixture')
        class MemoryHandler(fixture.Handler):
            def __init__(self, path, method='GET', body=b''):
                self.path, self.command = path, method
                self.headers = {'Content-Length': str(len(body))}
                self.rfile, self.wfile = io.BytesIO(body), io.BytesIO()
                self.sent_headers = {}
            def send_response(self, status):
                self.status = status
            def send_header(self, key, value):
                self.sent_headers[key] = value
            def end_headers(self):
                pass
        output = io.StringIO()
        with contextlib.redirect_stdout(output):
            body = b'\x1f\x8b\x00binary\xff'
            put = MemoryHandler('/fixture/logan-logs/a', 'PUT', body)
            put.do_PUT()
            self.assertEqual(put.status, 200)
            get = MemoryHandler('/logan-logs/a?Signature=TOPSECRET&OSSAccessKeyId=KEYSECRET')
            get.do_GET()
            self.assertEqual(get.wfile.getvalue(), body)
            head = MemoryHandler('/fixture/logan-logs/a', 'HEAD')
            head.do_HEAD()
            self.assertEqual(int(head.sent_headers['Content-Length']), len(body))
            self.assertEqual(head.wfile.getvalue(), b'')
        self.assertNotIn('TOPSECRET', output.getvalue())
        self.assertNotIn('KEYSECRET', output.getvalue())
        self.assertIn('OSS_FIXTURE_PUT bytes=', output.getvalue())

    def test_all20_manifest_has_exactly_twenty_distinct_routes(self):
        routes = json.loads((ROOT / 'tests/contract/cases.json').read_text())['routes']
        self.assertEqual(len({route['id'] for route in routes}), 20)
        self.assertEqual({route['method'] for route in routes}, {'GET', 'POST'})

    def test_changed_sdk_artifact_is_rejected_before_upload(self):
        fixtures = load('fixtures')
        with tempfile.TemporaryDirectory() as temporary:
            directory = Path(temporary)
            (directory / 'native.bin').write_bytes(b'tampered')
            (directory / 'manifest.json').write_text(json.dumps({'artifactSha256': {'native.bin': '0' * 64}}))
            with patch.object(fixtures, 'validate_metadata'):
                with self.assertRaisesRegex(AssertionError, 'SDK artifact digest mismatch'):
                    fixtures.make_fixture(directory)

    def test_single_detail_rejects_wrong_id_and_wrong_content(self):
        probe = load('probe')
        probe.require_single_detail({'id': 42, 'content': 'uploaded marker'}, 42, 'uploaded marker', 'native-detail')
        for row in ({'id': 43, 'content': 'uploaded marker'}, {'id': 42, 'content': 'other payload'}):
            with self.assertRaises(AssertionError):
                probe.require_single_detail(row, 42, 'uploaded marker', 'web-detail')

    def test_original_source_digest_mismatch_and_unknown_paths_rejected(self):
        fixtures = load('fixtures')
        hashes = {name: hashlib.sha256(path.read_bytes()).hexdigest()
                  for name, path in fixtures.expected_sources().items()}
        fixtures.validate_source_hashes(hashes)
        bad = dict(hashes)
        bad[next(iter(bad))] = '0' * 64
        with self.assertRaisesRegex(AssertionError, 'digest mismatch'):
            fixtures.validate_source_hashes(bad)
        for name in ('../outside', '/absolute/path', 'WebSDK/unknown.ts'):
            bad = dict(hashes, **{name: '0' * 64})
            with self.assertRaises(AssertionError):
                fixtures.validate_source_hashes(bad)

    def test_async_empty_index_waits_for_publication_without_positive_count(self):
        probe = load('probe')
        values = iter([[], [[{'id': 42}]]])
        result, observation = probe.await_published_index(lambda: next(values), 1, sleep=lambda _: None)
        self.assertEqual(result, [[{'id': 42}]])
        self.assertEqual(observation['attempts'], 2)
        self.assertEqual(observation['firstEmptyResponse']['body']['data'], [])

    def test_permanently_empty_index_fails(self):
        probe = load('probe')
        with self.assertRaisesRegex(AssertionError, 'publication deadline exceeded'):
            probe.await_published_index(lambda: [], 1, timeout=0, sleep=lambda _: None)

    def test_failed_payload_assertion_revokes_provisional_positive_route(self):
        probe = load('probe')
        responses = {'native-brief': {'body': {'code': 200}}, 'native-download-url': {'body': {'code': 200}}}
        failures = []
        probe.record_flow_failure(responses, failures, 'native-brief', 'native', AssertionError('missing marker'))
        self.assertNotIn('native-brief', responses)
        probe.record_flow_failure(responses, failures, 'native-download', 'native', AssertionError('gzip mismatch'))
        self.assertIn('native-download-url', responses)
        self.assertEqual([failure['route'] for failure in failures], ['native-brief', 'native-download'])

    def test_semantic_header_case_and_json_charset_normalization(self):
        probe = load('probe')
        a = {'Content-Type': 'Application/JSON; Charset=UTF-8', 'Access-Control-Allow-Headers': 'Accept, Content-Type'}
        b = {'Content-Type': 'application/json;charset=utf8', 'Access-Control-Allow-Headers': 'content-type,accept'}
        self.assertEqual(probe.semantic_headers(a), probe.semantic_headers(b))
        b['Content-Type'] = 'application/json'
        self.assertEqual(probe.semantic_headers(a), probe.semantic_headers(b))

    def test_web_flow_comparison_can_pass_with_native_failed(self):
        runner = load('runner')
        routes = {'web-' + str(i) for i in range(10)} | {'native-' + str(i) for i in range(10)}
        def capture():
            names = sorted(route for route in routes if route.startswith('web-'))
            return {'coveredRoutes': names, 'responses': {name: {'status': 200, 'body': {'code': 200}} for name in names},
                    'flowFailures': [{'flow': 'native', 'route': 'native-brief', 'reason': 'marker absent'}]}
        results = {'java': capture(), 'go': capture()}
        comparison = runner.compare_flows(results, routes)
        self.assertEqual(comparison['web']['status'], 'passed')
        self.assertEqual(comparison['native']['status'], 'failed')
        self.assertEqual(len(comparison['web']['sharedPositiveRoutesCompared']), 10)
        results['go']['responses']['web-0']['body']['code'] = 501
        self.assertEqual(runner.compare_flows(results, routes)['web']['status'], 'failed')


if __name__ == '__main__':
    unittest.main()
