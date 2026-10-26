import importlib.util
import json
from pathlib import Path
import tempfile
import unittest

PATH=Path(__file__).with_name('workbench-rn-page-ios.py')

class IOSReadOnlyEvidenceTests(unittest.TestCase):
    def load(self):
        self.assertTrue(PATH.exists(), 'iOS verifier is required')
        spec=importlib.util.spec_from_file_location('ios_evidence',PATH)
        mod=importlib.util.module_from_spec(spec);spec.loader.exec_module(mod)
        return mod

    def test_private_snapshot_rejects_escape_symlink_and_oversize(self):
        m=self.load()
        with tempfile.TemporaryDirectory() as d:
            root=Path(d);(root/'ok').write_bytes(b'actual')
            self.assertEqual(m.private_bytes(root,'ok'),b'actual')
            (root/'link').symlink_to(root/'ok')
            for name in ['../ok','/etc/passwd','link']:
                with self.assertRaises(ValueError):m.private_bytes(root,name)
            with self.assertRaises(ValueError):m.private_bytes(root,'ok',5)

    def test_ios_asset_platform_rejects_android_or_missing(self):
        m=self.load()
        self.assertEqual(m.check_asset_platform({'platform':'ios'}),'ios')
        for asset in [{'platform':'android'},{}]:
            with self.assertRaises(ValueError):m.check_asset_platform(asset)

    def test_installed_identity_requires_all_three_fields(self):
        m=self.load();receipt={'appVersion':'1.0','buildVersion':'5'}
        installed={'CFBundleIdentifier':m.APP,'CFBundleShortVersionString':'1.0','CFBundleVersion':'5'}
        m.check_installed_identity(installed,receipt)
        for key in installed:
            with self.assertRaises(ValueError):m.check_installed_identity(dict(installed,**{key:'wrong'}),receipt)

    def test_ios_envelope_header_and_exact_business_events(self):
        m=self.load();event={'schema':'xlb.page-diagnostic','schemaVersion':1,'eventId':'event','pageAttemptId':'attempt','pageName':'MiniApp','eventName':'content_ready','stage':'content','outcome':'succeeded','occurredAtMs':1792970000000,'monotonicMs':10,'source':'rn','level':'info','module':'page'}
        wire=b'native';receipt={'events':[event],'sourceSha256':m.hashlib.sha256(wire).hexdigest(),'sourceBytes':len(wire)}
        parsed=[{'content':'clogan header','logType':1},dict(event,content=json.dumps(event))]
        self.assertTrue(m.check_payloads(receipt,parsed,wire)['exactEvents'])
        for bad in [parsed+[parsed[-1]],[dict(parsed[0],logType=2),parsed[-1]],[parsed[0],dict(parsed[-1],outcome='failed')]]:
            with self.assertRaises(ValueError):m.check_payloads(receipt,bad,wire)
        with self.assertRaises(ValueError):m.check_payloads(receipt,parsed,b'changed')

if __name__=='__main__':unittest.main()
