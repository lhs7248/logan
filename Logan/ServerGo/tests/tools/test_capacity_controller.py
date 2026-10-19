import importlib.util
from pathlib import Path
from types import SimpleNamespace
import tempfile
import unittest
from unittest.mock import patch

ROOT = Path(__file__).resolve().parents[2]
spec = importlib.util.spec_from_file_location('capacity_controller', ROOT / 'script/verify-capacity-soak.py')
m = importlib.util.module_from_spec(spec); spec.loader.exec_module(m)


class ControllerTests(unittest.TestCase):
    def test_database_deadlocks_and_timeouts_cannot_be_hidden(self):
        clean={'database':{'mysql_error_counts':{'1213':0,'1205':0}}}
        self.assertTrue(m.validate_lock_errors([clean], clean['database']))
        with self.assertRaises(ValueError):m.validate_lock_errors([clean], {'mysql_error_counts':{'1213':1,'1205':0}})
        for codes in ({'1213':1,'1205':0},{'1213':0,'1205':1},{}):
            with self.assertRaises(ValueError):m.validate_lock_errors([{'database':{'mysql_error_counts':codes}}], clean['database'])

    def test_rsa_compatibility_override_requires_explicit_flag(self):
        self.assertEqual(m.go_crypto_environment(False), [])
        self.assertEqual(m.go_crypto_environment(True), ["GODEBUG=rsa1024min=0"])

    def test_go_images_do_not_require_java(self):
        self.assertEqual(m.runtime_images('go', 'new-go'), ['mysql:8.4.11','python:3.12-alpine','new-go'])
        self.assertIn('tomcat:9-jre17-temurin', m.runtime_images('both','new-go'))

    def test_failed_stage_keeps_structured_evidence(self):
        v=m.Capacity.__new__(m.Capacity); saved=[]
        v.dump=lambda name,value:saved.append((name,value.copy()))
        evidence={'report':{'http':{'Errors':1}},'data':{'native':{'tasks':2}}}
        def reject(): raise ValueError('unexpected errors')
        with self.assertRaises(ValueError):v.validate_and_record_stage('go-soak',evidence,reject)
        self.assertEqual(saved[0][0],'go-soak')
        self.assertEqual(saved[0][1]['report']['http']['Errors'],1)
        self.assertFalse(saved[0][1]['assessment']['stability_accepted'])
        self.assertEqual(saved[0][1]['validation_error'],'unexpected errors')

    def test_go_only_does_not_schedule_java(self):
        self.assertEqual(m.runtime_order('go'), ['go'])
        self.assertEqual(m.runtime_order('both'), ['java','go'])
        with self.assertRaises(ValueError): m.runtime_order('java')

    def test_host_observation_requires_full_hour_excluding_drain(self):
        self.assertEqual(m.host_window(3660, 3645.0, 3630.0), 3610.0)
        for controller, samples in [(3599,3580),(3645,3619)]:
            with self.assertRaises(ValueError): m.host_window(3660, controller, samples)
        self.assertEqual(m.stage_plan(3660,30,10)[-1]['seconds'],3660)

    def test_fixture_preflight_uses_no_network_before_business_writes(self):
        v = m.Capacity.__new__(m.Capacity)
        v.prefix = 'logan-verifier-' + 'a'*32; v.run_id = 'a'*32
        v.args = SimpleNamespace(binary=Path('/private/tmp/loader'), go_image='local-image')
        v.uid = '501:20'; v.reserve = lambda kind, name: name
        calls = []
        def docker(*args, **kwargs):
            calls.append(args); return 0, '{"validated":true,"native_records":2,"web_records":1}'
        v.docker = docker
        v.fixture_data = {'sdk':{'nativeContents':['a','b'],'webContents':['w']}}
        self.assertTrue(v.preflight_fixture('sdk', Path('/private/tmp/sdk.json'))['validated'])
        self.assertIn('--validate-only', calls[0])
        i = calls[0].index('--network'); self.assertEqual(calls[0][i+1], 'none')

    def test_frozen_stage_plan_has_real_hour_and_identical_resources(self):
        stages = m.stage_plan(3600, 30, 4)
        self.assertEqual(stages[-1], {'name':'soak', 'fixture':'sdk', 'mode':'mixed', 'seconds':3600, 'qps':4, 'workers':16, 'queue':32})
        self.assertTrue(any(s['mode']=='read' for s in stages))
        self.assertTrue(any(s['mode']=='mixed' and s['fixture']=='protocol' for s in stages))
        self.assertEqual(m.APP_MEMORY, 1024**3)
        with self.assertRaises(ValueError): m.stage_plan(3661, 30, 4)

    def test_native_content_count_is_exact_and_full_semantic_error_remains(self):
        counts = {'native':{'tasks':2,'pending':0,'details':2,'valid_tasks':2}, 'web':{'tasks':2,'pending':0,'details':2,'valid_tasks':2}}
        self.assertTrue(m.validate_data(counts, 'java', 'sdk', 2, 1))
        with self.assertRaises(ValueError): m.validate_data(counts, 'go', 'sdk', 2, 1)
        counts['native']['pending'] = 1
        with self.assertRaises(ValueError): m.validate_data(counts, 'java', 'sdk', 2, 1)

    def test_soak_drops_unknown_errors_and_short_window_do_not_pass(self):
        base={'schedule_seconds':3600,'elapsed_seconds':3600.1,'attempted':4,'dropped':0,'interrupted':False,'report_write_errors':0,
              'http':{'Sent':20,'Completed':20,'Success':20,'Errors':0,'ErrorKinds':{}},
              'workflows':{'Sent':4,'Completed':4,'Success':4,'Errors':0,'ErrorKinds':{}}}
        self.assertTrue(m.assess_soak(base, 'go', 3600)['stability_accepted'])
        for key,value in [('dropped',1),('report_write_errors',1),('elapsed_seconds',3599)]:
            r=dict(base);r[key]=value
            with self.subTest(key=key), self.assertRaises(ValueError): m.assess_soak(r,'go',3600)


if __name__=='__main__':unittest.main()
