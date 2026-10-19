import copy
import importlib.util
from pathlib import Path
import re
import unittest

ROOT = Path(__file__).resolve().parents[1]
spec = importlib.util.spec_from_file_location('real_reads', ROOT / 'reparse/real_reads.py')
reads = importlib.util.module_from_spec(spec)
spec.loader.exec_module(reads)


class RealReadTests(unittest.TestCase):
    def setUp(self):
        self.rows = [{'id': 2000, 'taskId': 20, 'content': 'old-prefix', 'logType': 1, 'logTime': 1000}]

    def test_partial_empty_and_duplicate_rows_rejected(self):
        for rows in ([], [{}], self.rows * 2):
            with self.assertRaises(AssertionError):
                reads.canonical_rows(rows)

    def test_database_column_names_normalized(self):
        self.assertEqual(reads.canonical_rows([{'id': 2000, 'task_id': 20, 'content': 'old-prefix', 'log_type': 1, 'log_time': 1000}]), self.rows)

    def test_duplicate_content_keeps_distinct_physical_ids(self):
        second = dict(self.rows[0], id=2001)
        self.assertEqual(len(reads.canonical_rows([second] + self.rows)), 2)

    def test_full_comparison_rejects_unselected_body_difference(self):
        java = {'responses': {'detail': {'body': {'data': dict(self.rows[0], formatContent='a')}}, 'signed-download': {'status': 302, 'location': 'http://fixture.oss:8080/logan-logs/history.log?signature=a'}}}
        go = copy.deepcopy(java)
        go['readinessStatus'] = 503
        go['responses']['signed-download']['location'] = 'http://oss:8080/fixture/logan-logs/history.log?signature=b'
        reads.compare_captures(java, go)
        bad = copy.deepcopy(go)
        bad['responses']['signed-download']['location'] = 'http://oss:8080/arbitrary/logan-logs/history.log?signature=b'
        with self.assertRaises(AssertionError):
            reads.compare_captures(java, bad)
        go['responses']['detail']['body']['data']['formatContent'] = 'b'
        with self.assertRaises(AssertionError):
            reads.compare_captures(java, go)
        go = copy.deepcopy(java)
        go['responses']['detail']['body']['data']['id'] = 9999
        with self.assertRaises(AssertionError):
            reads.compare_captures(java, go)

    def test_business_snapshot_detects_mutation(self):
        before = {'tasks': [{'id': 20, 'status': 1}], 'details': self.rows}
        self.assertTrue(reads.compare_business_snapshots(before, copy.deepcopy(before))['equal'])
        after = copy.deepcopy(before)
        after['details'][0]['content'] = 'changed'
        with self.assertRaises(AssertionError):
            reads.compare_business_snapshots(before, after)

    def test_cases_include_all_physical_details_and_nine_routes(self):
        cases = reads.cases_for(20, self.rows, 'history.log', 'ack-compat')
        self.assertEqual(len(cases), 9)
        search = next(c['path'] for c in cases if c['name'] == 'search')
        self.assertIn('beginTime=1', search)
        self.assertIn('endTime=4102444800000', search)
        self.assertIn('platform=1', search)
        self.assertIn('detail-2000', [c['name'] for c in cases])

    def test_deleted_physical_ids_are_probed_and_rejected_if_readable(self):
        cases = reads.cases_for(20, self.rows, 'history.log', 'ack-compat', [2001])
        self.assertIn('deleted-2001', [c['name'] for c in cases])
        stale = {'responses': {'deleted-2001': {'status': 200, 'mime': 'application/json', 'body': {'code': 200, 'data': self.rows[0]}}}}
        with self.assertRaisesRegex(AssertionError, 'deleted physical detail'):
            reads.validate_capture(stale, 20, self.rows, 'history.log')
        with self.assertRaises(AssertionError):
            reads.cases_for(20, self.rows, 'history.log', 'ack-compat', [2000])

    def test_builder_disposable_schema_guard(self):
        source = (ROOT / 'crossruntime/build_java_war.py').read_text()
        pattern = re.search(r"re.fullmatch\(r'([^']+)'", source).group(1)
        for schema in ('logan_reparse_it_abc123', 'logan_crossruntime_abc123'):
            self.assertIsNotNone(re.fullmatch(pattern, 'jdbc:mysql://owned:3306/' + schema))
        for schema in ('production', 'logan_reparse_it_', 'logan_reparse_it_ABC', 'logan_reparse_it_a/production'):
            self.assertIsNone(re.fullmatch(pattern, 'jdbc:mysql://owned:3306/' + schema))
        self.assertIn("os.environ.get('CROSSRUNTIME_DISPOSABLE') != 'YES'", source)


if __name__ == '__main__':
    unittest.main()
