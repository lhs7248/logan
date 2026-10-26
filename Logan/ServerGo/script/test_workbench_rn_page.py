import importlib.util
import json
from pathlib import Path
import unittest

class RNPageEvidenceTests(unittest.TestCase):
    def test_exact_payloads_and_ciphertext_are_required(self):
        spec=importlib.util.spec_from_file_location('rn_page',Path(__file__).with_name('workbench-rn-page.py'))
        m=importlib.util.module_from_spec(spec);spec.loader.exec_module(m)
        event={'schema':'xlb.page-diagnostic','schemaVersion':1,'eventId':'event','pageAttemptId':'attempt','pageName':'MiniApp','eventName':'content_ready','stage':'content','outcome':'succeeded','occurredAtMs':1792970000000,'monotonicMs':10,'source':'rn','level':'info','module':'page'}
        receipt={'events':[event],'sourceSha256':m.hashlib.sha256(b'actual-native').hexdigest(),'sourceBytes':13}
        parsed=[dict(event,content=json.dumps(event),time='2026-10-26T00:00:00Z')]
        self.assertTrue(m.check_payloads(receipt,parsed,b'actual-native')['exactEvents'])
        with self.assertRaises(ValueError):m.check_payloads(receipt,parsed,b'changed')
        altered=[dict(parsed[0],outcome='failed')]
        with self.assertRaises(ValueError):m.check_payloads(receipt,altered,b'actual-native')
        with self.assertRaises(ValueError):m.check_payloads(receipt,parsed+parsed,b'actual-native')

if __name__=='__main__':unittest.main()
