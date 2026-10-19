import importlib.util
from pathlib import Path
import base64
import gzip
import json
import tempfile
import unittest
from urllib.parse import quote,unquote

ROOT=Path(__file__).resolve().parents[2]
spec=importlib.util.spec_from_file_location('large_load_fixtures',ROOT/'tests/load/large_fixtures.py')
m=importlib.util.module_from_spec(spec);spec.loader.exec_module(m)

class LargeFixtureTests(unittest.TestCase):
    def test_profile_bounds_and_record_bytes(self):
        for n,w in [(0,1),(33,1),(1,9)]:
            with self.assertRaises(ValueError):m.validate_profile(n,w)
        values=m.contents('native',8192,4096)
        self.assertEqual(sum(len(s.encode()) for s in values),8192)
        self.assertEqual(len(set(values)),len(values))
        self.assertLess(len(gzip.compress(''.join(values).encode())),8192*.8)
        self.assertGreater(len(gzip.compress(''.join(values).encode())),8192*.4)
    def test_decode_every_native_frame_and_web_record(self):
        with tempfile.TemporaryDirectory() as d:
            key=m.test_key(Path(d))
            f=m.make_fixture(8192,4096,key,frame_plain_bytes=4096)
            result=m.verify_fixture(f,key)
            self.assertEqual(result['native_records'],2)
            self.assertEqual(result['web_records'],1)
            self.assertGreater(result['native_frames'],1)
            bad=dict(f,nativePlainBase64=base64.b64encode(b'wrong').decode())
            with self.assertRaises(ValueError):m.verify_fixture(bad,key)
    def test_oracle_rejects_version_and_metadata_tampering(self):
        with tempfile.TemporaryDirectory() as d:
            key=m.test_key(Path(d)); f=m.make_fixture(8192,4096,key)
            bad=json.loads(json.dumps(f)); items=[json.loads(unquote(v)) for v in bad["webBody"]["logArray"].split(",")]
            for item in items:item["v"]=0
            bad["webBody"]["logArray"]=",".join(quote(json.dumps(v,separators=(",",":")),safe="") for v in items)
            with self.assertRaises(ValueError):m.verify_fixture(bad,key)
            bad=json.loads(json.dumps(f));bad["profile"]["native_content_bytes"]=1
            with self.assertRaises(ValueError):m.verify_fixture(bad,key)

    def test_input_profile_sizes_are_exact_and_upload_bounded(self):
        self.assertEqual(m.validate_profile(32,8),(32<<20,8<<20))
        with self.assertRaises(ValueError):m.contents('native',4097,4096)

if __name__=='__main__':unittest.main()
