import importlib.util,json,unittest
from pathlib import Path
ROOT=Path(__file__).resolve().parents[2]
def load():
 spec=importlib.util.spec_from_file_location('processing_measurement',ROOT/'tests/load/processing_measurement.py')
 m=importlib.util.module_from_spec(spec);spec.loader.exec_module(m);return m
class ProcessingMeasurementTests(unittest.TestCase):
 def test_only_native_sanitized_events_and_exact_percentiles(self):
  m=load();lines=['HERTZ: debug',json.dumps({'msg':'http request','content':'not retained'})]
  for i in range(1,101):lines.append(json.dumps(dict(msg='native processing stages',task_id=i,outcome='none',duration_ms=i,stages_ms={'download':1,'decompress':1,'parse_stage':i/2,'publication':i/2,'stage_decode':i/10,'db_insert':i/4})))
  result=m.summarize('\n'.join(lines),range(1,101))
  self.assertTrue(result['complete']);self.assertEqual(result['claimed_tasks'],100)
  self.assertEqual(result['stages_ms']['parse_stage']['p95'],47.5);self.assertEqual(result['duration_ms']['p99'],99)
  self.assertNotIn('content',json.dumps(result))
 def test_missing_failed_stage_not_silently_zero_or_success(self):
  m=load();line=json.dumps(dict(msg='native processing stages',task_id=1,outcome='deadline',duration_ms=6000,stages_ms={'decompress':5999}))
  result=m.summarize(line,[1,2])
  self.assertFalse(result['complete']);self.assertEqual(result['missing_task_ids'],[2]);self.assertEqual(result['outcomes'],{'deadline':1})
  self.assertNotIn('db_insert',result['stages_ms'])
 def test_duplicate_unknown_or_invalid_trace_rejected(self):
  m=load();v=dict(msg='native processing stages',task_id=1,outcome='none',duration_ms=1,stages_ms={'parse_stage':1})
  for changes in [{'duration_ms':float('nan')},{'task_id':True},{'stages_ms':{'object-name':1}},{'stages_ms':{'db_insert':-1}}]:
   with self.assertRaises(ValueError):m.summarize(json.dumps(dict(v,**changes)),[1])
  result=m.summarize(json.dumps(v)+'\n'+json.dumps(v),[1])
  self.assertFalse(result['complete']);self.assertEqual(result['repeated_task_ids'],[1])
 def test_cumulative_container_logs_are_filtered_by_nanosecond_stage_window(self):
  m=load();rows=[]
  for stamp,duration in [('2026-10-19T00:00:00.123456788Z',1),('2026-10-19T00:00:00.123456789Z',2),('2026-10-19T00:00:00.123456790Z',3)]:
   rows.append(json.dumps(dict(msg='native processing stages',time=stamp,task_id=1,outcome='none',duration_ms=duration,stages_ms={'download':1,'decompress':1,'parse_stage':1,'publication':duration,'db_insert':1,'stage_decode':1})))
  r=m.summarize('\n'.join(rows),[1],'2026-10-19T00:00:00.123456789Z','2026-10-19T00:00:00.123456789Z')
  self.assertTrue(r['complete']);self.assertEqual(r['duration_ms']['mean'],2)
 def test_successful_event_without_segment_fields_is_incomplete(self):
  m=load();v=dict(msg='native processing stages',task_id=1,outcome='none',duration_ms=1,stages_ms={})
  r=m.summarize(json.dumps(v),[1]);self.assertFalse(r['complete']);self.assertTrue(r['event_accounting_complete']);self.assertFalse(r['segmentation_complete'])
  self.assertIn('decompress',r['missing_stages'][0]['stages'])
 def test_unknown_task_inside_stage_window_rejected(self):
  m=load();v=dict(msg='native processing stages',task_id=2,outcome='none',duration_ms=1,stages_ms={})
  with self.assertRaises(ValueError):m.summarize(json.dumps(v),[1])
 def e2e_report(self):
  rows=[];flows={}
  for flow in ['native','web']:
   rows.append({'Flow':flow,'Cycle':0,'TaskID':1})
   flows[flow]={'CompletedObservations':1,'ActiveUploads':0,'Final':True,'Queryable':1,'Censored':0,'P95MS':2.0,'P99MS':2.0,'Samples':[{'Cycle':0,'TaskID':1,'Queryable':True,'ElapsedMS':2.0,'Failure':''}]}
  return dict(ledger=rows,upload_to_all_details=flows)
 def test_final_e2e_report_recomputes_percentiles_and_checks_ledger(self):
  m=load();r=self.e2e_report();self.assertEqual(m.validate_e2e(r)['native']['p99'],2.0)
  for key,value in [('ActiveUploads',1),('Censored',1),('P99MS',1),('Final',False)]:
   r=self.e2e_report();r['upload_to_all_details']['native'][key]=value
   with self.assertRaises(ValueError):m.validate_e2e(r)
  r=self.e2e_report();r['upload_to_all_details']['native']['Samples'][0]['TaskID']=2
  with self.assertRaises(ValueError):m.validate_e2e(r)
