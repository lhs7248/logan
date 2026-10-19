import importlib.util
from pathlib import Path
import unittest
from types import SimpleNamespace

ROOT=Path(__file__).resolve().parents[2]
spec=importlib.util.spec_from_file_location('all20_controller',ROOT/'script/verify-all20-large-load.py')
m=importlib.util.module_from_spec(spec);spec.loader.exec_module(m)

class All20ControllerTests(unittest.TestCase):
    def report(self):
        return {'final':True,'interrupted':False,'report_write_errors':0,'dropped':0,'http':{'Errors':0},'workflows':{'Errors':0},'routes':{r:{'Sent':2,'Success':2,'Errors':0} for r in m.ROUTES}}
    def test_mysql_budget_is_explicit_and_keeps_application_budget(self):
        self.assertEqual(m.mysql_budget(1024),("1024m",1<<30))
        self.assertEqual(m.mysql_budget(2048),("2048m",2<<30))
        for n in (0,512,4096,"2048"):
            with self.assertRaises(ValueError):m.mysql_budget(n)

    def test_bounded_profile_and_pool(self):
        self.assertEqual(m.pool_budget(20,10), {'LOGAN_DB_MAX_OPEN':'20','LOGAN_DB_MAX_IDLE':'10'})
        for pair in [(0,0),(21,2),(4,5),(4,-1)]:
            with self.assertRaises(ValueError):m.pool_budget(*pair)
        args=m.mysql_profile(True)
        self.assertIn('--max-connections=16',args)
        self.assertIn('--innodb-redo-log-capacity=134217728',args)
        self.assertFalse(any('skip-log-bin' in a for a in args))
        self.assertEqual(m.mysql_profile(False),[])
    def test_actual_pool_metrics_required(self):
        metrics={'logan_db_connections{state="max_open"}':4,'logan_db_connections{state="idle"}':2}
        m.validate_pool_metrics(metrics,4,2)
        with self.assertRaises(AssertionError):m.validate_pool_metrics(metrics,20,10)
        with self.assertRaises(AssertionError):m.validate_pool_metrics({},4,2)
    def test_large_only_plan(self):
        plan=m.load_plan(False,True)
        self.assertEqual([x['fixture'] for x in plan],['large1','large8','large32'])
        self.assertTrue(all(x['seconds']==220 and x['mode']=='all20' for x in plan))
    def test_disk_guard_and_memory_decomposition(self):
        with self.assertRaises(m.DiskBudgetError):m.require_disk_budget(3*(1<<30)-1,'host')
        m.require_disk_budget(3*(1<<30),'host')
        with self.assertRaises(m.DiskBudgetError):m.require_disk_budget(4*(1<<30)-1,'initial',4*(1<<30))
        m.require_disk_budget(1<<30,'guest',1<<30)
        self.assertEqual(m.memory_stat('anon 12\nfile 24\nfile_dirty 3\n'),{'anon':12,'file':24,'file_dirty':3})
    def test_every_positive_route_required(self):
        m.validate_routes(self.report())
        for r in m.ROUTES:
            value=self.report();value['routes'].pop(r)
            with self.assertRaises(ValueError):m.validate_routes(value)
        value=self.report();value['routes']['native-download']['Success']=0
        with self.assertRaises(ValueError):m.validate_routes(value)
    def test_scheduled_arrivals_and_unique_ledger_required(self):
        stage={"seconds":20,"qps":.2}
        r={"attempted":4,"workflows":{"Sent":4},"ledger":[{"Cycle":i} for i in range(4)]}
        m.validate_arrivals(r,stage)
        for n in (2,3,5):
            bad=dict(r,attempted=n)
            with self.assertRaises(ValueError):m.validate_arrivals(bad,stage)
        bad=dict(r,ledger=[{"Cycle":0}]*4)
        with self.assertRaises(ValueError):m.validate_arrivals(bad,stage)

    def test_errors_and_drops_fail_closed(self):
        for key in ('dropped','report_write_errors','interrupted'):
            value=self.report();value[key]=1
            with self.assertRaises(ValueError):m.validate_routes(value)
        value=self.report();value['http']['Errors']=1
        with self.assertRaises(ValueError):m.validate_routes(value)
    def test_short_diagnostic_cannot_look_like_formal_window(self):
        self.assertEqual(m.load_plan(True)[0]['seconds'],20)
        p=m.load_plan(False)
        self.assertGreaterEqual(p[0]['seconds'],660)
        self.assertTrue(all(s['mode']=='all20' for s in p))
        self.assertEqual([s['fixture'] for s in p],['sdk','large1','large8','large32'])
        self.assertTrue(all(s['qps']*s['seconds']>=4 for s in p))
    def test_probe_bind_uses_staged_shared_directory(self):
        v=m.All20.__new__(m.All20);v.prefix="logan-verifier-"+"a"*32;v.run_id="a"*32;v.network="isolated";v.uid="501:20";v.app=v.prefix+"-go"
        v.args=SimpleNamespace(work_dir=Path("/private/tmp/phase21-work"));v.fixture_files={"sdk":Path("/private/tmp/sdk.json")}
        v.reserve=lambda kind,name:name;calls=[]
        def docker(*args,**kw):calls.append(args);return 0,'{"status":"passed","result":{}}'
        v.docker=docker;v.protection_probe("web-seed")
        self.assertIn("type=bind,src=/private/tmp/phase21-work/protection_probe.py,dst=/probe.py,readonly",calls[0])

    def test_content_hash_verifies_each_task_not_aggregate_only(self):
        expected=['a','b']; good=[(4,1,'a'),(4,2,'b'),(5,3,'a'),(5,4,'b')]
        rows=[(t,i,len(v),m.digest(v)) for t,i,v in good]
        self.assertEqual(m.validate_hash_rows({4:1,5:1},rows,expected,'native'),2)
        with self.assertRaises(ValueError):m.validate_hash_rows({4:1,5:1},rows[:-1],expected,'native')
        with self.assertRaises(ValueError):m.validate_hash_rows({4:0,5:1},rows,expected,'native')
        bad=rows.copy();bad[0]=(4,1,1,m.digest('z'))
        with self.assertRaises(ValueError):m.validate_hash_rows({4:1,5:1},bad,expected,'native')

if __name__=='__main__':unittest.main()
