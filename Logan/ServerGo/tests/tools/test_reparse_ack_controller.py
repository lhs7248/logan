import importlib.util
from pathlib import Path
import unittest

ROOT=Path(__file__).resolve().parents[2]
spec=importlib.util.spec_from_file_location('ack_controller',ROOT/'script/verify-reparse-confirmation.py')
m=importlib.util.module_from_spec(spec);spec.loader.exec_module(m)

class AckWitnessTests(unittest.TestCase):
 def state(self):
  old={'id':100,'task_id':20,'log_type':1,'content':'prefix','log_time':1,'update_time':'old'}
  tail={'id':101,'task_id':20,'log_type':2,'content':'tail','log_time':2,'update_time':'new'}
  before={'task':[{'id':20,'status':1}],'details':[old]}
  after={'task':before['task'],'details':[old,tail]}
  gate={'serverCommitOKObserved':True,'commitOKForwarded':False,'sequence':1,'payloadBytes':7}
  audit={'state':'applied','task_id':20,'append_ids':'[101]'}
  records=[{'c':'prefix','f':1,'l':1},{'c':'tail','f':2,'l':2}]
  return gate,before,after,audit,records
 def test_commit_proof_requires_unforwarded_success_and_exact_publication(self):
  g,b,a,o,r=self.state();m.verify_commit_witness(g,b,a,o,'apply',20,r)
  for broken in ({**g,'commitOKForwarded':True},{**g,'serverCommitOKObserved':False}):
   with self.assertRaises(AssertionError):m.verify_commit_witness(broken,b,a,o,'apply',20,r)
  with self.assertRaises(AssertionError):m.verify_commit_witness(g,b,b,o,'apply',20,r)
  with self.assertRaises(AssertionError):m.verify_commit_witness(g,b,a,{**o,'state':'applying'},'apply',20,r)
 def test_rollback_requires_complete_before_state_and_retained_audit(self):
  g,b,a,o,r=self.state();o['state']='rolled_back';m.verify_commit_witness(g,b,b,o,'rollback',20,r)
  with self.assertRaises(AssertionError):m.verify_commit_witness(g,b,a,o,'rollback',20,r)
 def test_prefix_change_or_wrong_inserted_ids_cannot_pass(self):
  g,b,a,o,r=self.state();a={'task':a['task'],'details':[dict(x) for x in a['details']]};a['details'][0]['update_time']='changed'
  with self.assertRaises(AssertionError):m.verify_commit_witness(g,b,a,o,'apply',20,r)
  g,b,a,o,r=self.state();o['append_ids']='[100]'
  with self.assertRaises(AssertionError):m.verify_commit_witness(g,b,a,o,'apply',20,r)
