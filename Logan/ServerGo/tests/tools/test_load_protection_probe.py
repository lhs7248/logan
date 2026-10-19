import importlib.util
from pathlib import Path
import unittest
import base64
import json
import struct
ROOT=Path(__file__).resolve().parents[2]
spec=importlib.util.spec_from_file_location('protection_probe',ROOT/'tests/load/protection_probe.py')
m=importlib.util.module_from_spec(spec);spec.loader.exec_module(m)
class ProtectionProbeTests(unittest.TestCase):
    def test_non_owned_targets_rejected_before_network(self):
        for host in ('production.example','127.0.0.1','logan-verifier-'+('a'*32)+'-java'):
            with self.assertRaises(ValueError):m.run(host,{},'phase21-'+('a'*32)+'-repeat','web-seed')
    def test_real_user_identity_rejected_before_network(self):
        with self.assertRaises(ValueError):m.run('logan-verifier-'+('a'*32)+'-go',{},'real-device','web-seed')
    def test_oversize_bodies_keep_valid_wire_and_json(self):
        seed=b"\x01"+struct.pack(">I",16)+b"z"*16+b"\x00"
        bodies=m.oversize_bodies({"nativeBase64":base64.b64encode(seed).decode(),"webBody":{"logArray":"verified-seed"}})
        self.assertGreater(len(bodies["native"]),32<<20)
        self.assertEqual(len(bodies["native"])%len(seed),0)
        self.assertEqual(bodies["native"],seed*(len(bodies["native"])//len(seed)))
        self.assertGreater(len(bodies["web"]),32<<20)
        self.assertEqual(json.loads(bodies["web"])["logArray"],"verified-seed")
        with self.assertRaises(ValueError):m.oversize_bodies({"nativeBase64":base64.b64encode(b"bad").decode(),"webBody":{}})

    def test_nested_published_ids_preserve_full_collection(self):
        self.assertEqual(m.collect_ids([[{'id':8},{'detailId':9}],[{'id':10}]]),[8,9,10])
if __name__=='__main__':unittest.main()
