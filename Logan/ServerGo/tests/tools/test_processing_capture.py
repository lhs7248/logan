import importlib.util,json,unittest
from pathlib import Path
ROOT=Path(__file__).resolve().parents[2]
spec=importlib.util.spec_from_file_location('latency_controller',ROOT/'script/verify-all20-large-load.py');m=importlib.util.module_from_spec(spec);spec.loader.exec_module(m)
class CaptureTests(unittest.TestCase):
 def test_stage_capture_has_time_bounds_and_keeps_missing_e2e_explicit(self):
  v=m.All20.__new__(m.All20);v.app='owned-go';calls=[];saved={}
  row=dict(time='2026-10-19T00:00:00.1Z',msg='native processing stages',task_id=1,outcome='none',duration_ms=1,stages_ms={key:1 for key in ['download','decompress','parse_stage','stage_decode','db_insert','publication']})
  v.docker=lambda *args,**kw:(calls.append(args) or (0,json.dumps(row)))
  v.dump=lambda name,value:saved.setdefault(name,value)
  report={'start_utc':'2026-10-19T00:00:00Z','snapshot_utc':'2026-10-19T00:00:01Z','ledger':[{'Flow':'native','TaskID':1,'Cycle':0}]}
  result=v.collect_latency('largest',report)
  self.assertTrue(result['native_processing']['complete'])
  self.assertFalse(result['e2e_validated']);self.assertIn('e2e_error',result)
  self.assertIn('--since',calls[0]);self.assertIn('--until',calls[0]);self.assertIn('largest-app-stages',saved)
 def test_missing_trace_is_not_reported_as_complete(self):
  v=m.All20.__new__(m.All20);v.app='owned-go';v.docker=lambda *a,**k:(0,'')
  v.dump=lambda name,value:value
  result=v.collect_latency('largest',{'start_utc':'2026-10-19T00:00:00Z','snapshot_utc':'2026-10-19T00:00:01Z','ledger':[{'Flow':'native','TaskID':1,'Cycle':0}]})
  self.assertFalse(result['native_processing']['complete']);self.assertEqual(result['native_processing']['missing_task_ids'],[1])
