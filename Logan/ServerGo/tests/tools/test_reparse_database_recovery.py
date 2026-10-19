import hashlib
import json
import importlib.util
from pathlib import Path
import unittest
ROOT=Path(__file__).resolve().parents[2]
spec=importlib.util.spec_from_file_location('database_recovery',ROOT/'script/verify-reparse-database-recovery.py')
m=importlib.util.module_from_spec(spec);spec.loader.exec_module(m)
class DatabaseRecoveryTests(unittest.TestCase):
 def test_uncommitted_full_snapshot_must_match(self):
  m.assert_recovery({'business':[1],'audit':[]},{'business':[1],'audit':[]},False)
  for value in ({'business':[1,2],'audit':[]},{'business':[1],'audit':['applying']}):
   with self.assertRaises(AssertionError):m.assert_recovery({'business':[1],'audit':[]},value,False)
 def test_committed_audit_must_survive(self):
  with self.assertRaises(AssertionError):m.assert_recovery({'audit':'applied'},{'audit':None},True)
 def test_server_requires_durable_settings(self):
  self.assertEqual(m.validate_server('uuid\t1\t1\tON')['serverUUID'],'uuid')
  self.assertEqual(m.validate_server('uuid\t1\t1\t1')['serverUUID'],'uuid')
  for row in ('uuid\t2\t1\tON','uuid\t1\t0\tON','uuid\t1\t1\tOFF','\t1\t1\tON'):
   with self.assertRaises(AssertionError):m.validate_server(row)
 def test_named_volume_is_cleaned_after_containers(self):
  v=m.DatabaseRecovery.__new__(m.DatabaseRecovery);v.prefix='logan-verifier-test';v.run_id='test';v.owned=[('container','logan-verifier-test-mysql')]
  v.docker=lambda *a,**kw:(0,'')
  args=v.mysql_storage()
  self.assertEqual(v.owned[0],('volume',v.volume));self.assertIn('type=volume,src='+v.volume+',dst=/var/lib/mysql',args)
  self.assertNotIn('--tmpfs',args)
 def audit_fixture(self):
  v=m.DatabaseRecovery.__new__(m.DatabaseRecovery);v.db='logan_reparse_it_test'
  v.records=[{'c':'old','f':1,'l':1},{'c':'same','f':2,'l':2},{'c':'same','f':2,'l':2}]
  original={'task':[{'id':20}],'details':[{'id':1,'content':'old','log_type':1,'log_time':1}]}
  after={'task':original['task'],'details':original['details']+[{'id':i,'content':'same','log_type':2,'log_time':2} for i in (2,3)]}
  b=json.dumps({'Task':original['task'][0],'Details':original['details']}).encode()
  a=json.dumps({'Task':after['task'][0],'Details':after['details']}).encode()
  raw='\t'.join([b.hex(),a.hex(),hashlib.sha256(b).hexdigest(),hashlib.sha256(a).hexdigest()])
  v.sql=lambda query:raw
  v.audit=lambda op:{'state':'applied','task_id':20,'append_ids':'[2,3]'}
  return v,original,after,raw
 def test_audit_full_snapshots_and_duplicate_physical_ids(self):
  v,b,a,_=self.audit_fixture()
  self.assertTrue(v.verify_audit_snapshots('op',b,a,'apply')['snapshotsMatchBusiness'])
  self.assertTrue(v.verify_audit_snapshots('op',b,b,'rollback')['snapshotsMatchBusiness'])
 def test_audit_digest_corruption_rejected(self):
  v,b,a,raw=self.audit_fixture();parts=raw.split('\t');parts[2]='0'*64;v.sql=lambda query:'\t'.join(parts)
  with self.assertRaises(AssertionError):v.verify_audit_snapshots('op',b,a,'apply')
 def test_audit_current_business_divergence_rejected(self):
  v,b,a,_=self.audit_fixture();a=dict(a,details=a['details'][:-1])
  with self.assertRaises(AssertionError):v.verify_audit_snapshots('op',b,a,'apply')
 def test_audit_append_ids_divergence_rejected(self):
  v,b,a,_=self.audit_fixture();v.audit=lambda op:{'task_id':20,'append_ids':'[2,4]'}
  with self.assertRaises(AssertionError):v.verify_audit_snapshots('op',b,a,'apply')
if __name__=='__main__':unittest.main()
