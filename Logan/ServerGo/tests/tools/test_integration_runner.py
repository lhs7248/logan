import importlib.util
import json
from pathlib import Path
import unittest

spec = importlib.util.spec_from_file_location("runner", Path(__file__).resolve().parents[2] / "script/integration.py")
runner = importlib.util.module_from_spec(spec)
spec.loader.exec_module(runner)

class EvidenceTests(unittest.TestCase):
    def test_no_tests_and_skipped_are_not_passes(self):
        self.assertEqual(runner.classify('', 0)[0], 'failed')
        skipped = json.dumps({'Test':'TestRealDependency','Action':'skip'})
        self.assertEqual(runner.classify(skipped, 0)[0], 'failed')
        passed = json.dumps({'Test':'TestRealDependency','Action':'pass'})
        self.assertEqual(runner.classify(passed, 0)[0], 'passed')
        self.assertEqual(runner.classify(passed, 1)[0], 'failed')
    def test_redacts_values_not_names(self):
        value = 'user:private@tcp(localhost)/logan_it_control'
        self.assertEqual(runner.redact('DSN='+value, {'LOGAN_IT_MYSQL_DSN':value}), 'DSN=[redacted]')
    def test_generated_signed_url_is_not_preserved(self):
        result = runner.redact('GET https://bucket.example/object?x-oss-signature=private-generated-value failed', {})
        self.assertEqual(result, 'GET [redacted-url] failed')

if __name__ == '__main__':
    unittest.main()
