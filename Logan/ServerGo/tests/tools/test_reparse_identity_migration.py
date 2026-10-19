import importlib.util
import json
from pathlib import Path
import tempfile
import unittest
ROOT=Path(__file__).resolve().parents[2]
spec=importlib.util.spec_from_file_location('identity_acceptance',ROOT/'script/verify-reparse-identity-migration.py');m=importlib.util.module_from_spec(spec);spec.loader.exec_module(m)
class IdentityAcceptanceTests(unittest.TestCase):
 def test_identity_grant_may_only_change_authorization_table(self):
  before={'objects':{'x':'sha'},'tables':{'logan_task':'original',m.AUTH:''}}
  after={'objects':before['objects'],'tables':{'logan_task':'original',m.AUTH:'one grant'}}
  m.unchanged_except_authorization(before,after)
  for changed in ({'objects':{},'tables':after['tables']},{'objects':before['objects'],'tables':{'logan_task':'modified',m.AUTH:'grant'}},{'objects':before['objects'],'tables':{m.AUTH:'grant'}}):
   with self.assertRaises(AssertionError):m.unchanged_except_authorization(before,changed)
 def test_migration_flags_require_explicit_scope_and_attestations(self):
  v=m.IdentityAcceptance.__new__(m.IdentityAcceptance);v.source_identity='source/db';v.target_identity='target/db';v.witnesses={30:(Path('/tmp/evidence.json'),{'digest':'a'*64})}
  args=v.identity_flags(30)
  for flag in ('--allow-identity-migration','--source-fenced','--offline','--allow-isolated-writes','--witness-sha256','--expect-source','--expect-target'):self.assertIn(flag,args)
  readonly=v.identity_flags(30,'identity-dry-run')
  for flag in ('--allow-isolated-writes','--source-fenced','--allow-identity-migration'):self.assertNotIn(flag,readonly)
 def test_mutation_keeps_private_file_and_digest_binding(self):
  with tempfile.TemporaryDirectory() as d:
   v=m.IdentityAcceptance.__new__(m.IdentityAcceptance);v.work=Path(d)
   p=v.work/'original.json';p.write_text(json.dumps({'version':'v1','source_gtid':'uuid:1','digest':'old'}));v.witnesses={30:(p,{})}
   changed,digest=v.changed_witness(30,'changed',lambda w:w.update(source_gtid='other:1'))
   self.assertEqual(changed.stat().st_mode&0o777,0o600);self.assertEqual(json.loads(changed.read_text())['digest'],digest);self.assertEqual(json.loads(p.read_text())['source_gtid'],'uuid:1')
if __name__=='__main__':unittest.main()
