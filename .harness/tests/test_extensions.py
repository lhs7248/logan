import importlib.util
import json
import os
from pathlib import Path
import signal
import subprocess
import sys
import tempfile
import time
import unittest
from unittest.mock import patch

SCRIPT = Path(__file__).resolve().parents[1] / 'run.py'
spec = importlib.util.spec_from_file_location('harness_ext', SCRIPT)
h = importlib.util.module_from_spec(spec)
spec.loader.exec_module(h)


class Extensions(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)

    def test_timeout_kills_descendant(self):
        # The descendant ignores TERM and would write a marker after the timeout.
        marker = self.root / 'survived'
        child = "import signal,time,pathlib; signal.signal(signal.SIGTERM, signal.SIG_IGN); time.sleep(1.5); pathlib.Path(%r).touch(); time.sleep(10)" % str(marker)
        parent = "import subprocess,sys,time; subprocess.Popen([sys.executable,'-c',%r]); time.sleep(20)" % child
        result = h.execute([sys.executable, '-c', parent], self.root, self.root / 'log', timeout=.5)
        self.assertEqual(result['status'], 'timeout')
        time.sleep(1.6)
        self.assertFalse(marker.exists())

    def test_sigterm_cleans_real_child(self):
        marker = self.root / 'survived'
        ready = self.root / 'ready'
        child = "import time,pathlib; pathlib.Path(%r).touch(); time.sleep(2); pathlib.Path(%r).touch()" % (str(ready), str(marker))
        runner = "import sys,signal,pathlib; sys.path.insert(0,%r); import runtime; signal.signal(signal.SIGTERM,lambda *a: (_ for _ in ()).throw(KeyboardInterrupt())); runtime.execute([sys.executable,'-c',%r],pathlib.Path(%r),pathlib.Path(%r))" % (str(SCRIPT.parent), child, str(self.root), str(self.root/'log'))
        proc = subprocess.Popen([sys.executable, '-B', '-c', runner], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        try:
            deadline = time.monotonic() + 5
            while not ready.exists() and time.monotonic() < deadline:
                time.sleep(.05)
            self.assertTrue(ready.exists())
            proc.terminate()
            self.assertNotEqual(proc.wait(timeout=5), 0)
            time.sleep(2.1)
            self.assertFalse(marker.exists())
        finally:
            if proc.poll() is None:
                proc.kill()
                proc.wait()

    def test_identity_changes_with_untracked_content(self):
        subprocess.run(['git','init','-q',str(self.root)],check=True)
        def git(*args):
            subprocess.run(['git','-c','user.name=Test','-c','user.email=test@example.invalid',*args],cwd=self.root,check=True,capture_output=True)
        (self.root/'tracked').write_text('base')
        git('add','tracked'); git('-c','core.hooksPath=/dev/null','commit','-qm','fixture')
        first = h.runtime.identity(self.root)
        (self.root/'new').write_text('one')
        second = h.runtime.identity(self.root)
        (self.root/'new').write_text('two')
        third = h.runtime.identity(self.root)
        self.assertFalse(first['dirty'])
        self.assertNotEqual(first['workspace_sha256'], second['workspace_sha256'])
        self.assertNotEqual(second['workspace_sha256'], third['workspace_sha256'])

    def test_preflight_failure_writes_report(self):
        out = self.root/'report';out.mkdir()
        with patch.object(h.tempfile,'mkdtemp',return_value=str(out)), patch.object(h.runtime,'identity',return_value={}), patch.object(h.runtime,'versions',return_value={}):
            self.assertEqual(h.verify(self.root,'full'),1)
        report=json.loads((out/'summary.json').read_text())
        self.assertEqual(report['status'],'blocked')
        self.assertEqual(report['unexecuted'],h.PROFILES['full'])

    def test_running_checkpoint_is_not_success(self):
        out = self.root/'report';out.mkdir()
        def execute(*args, **kwargs):
            state=json.loads((out/'summary.json').read_text())
            self.assertIsNone(state['exit_code'])
            self.assertEqual(state['steps'][-1]['status'],'running')
            return {'status':'passed','exit_code':0}
        with patch.object(h,'check',return_value=[]), patch.object(h.tempfile,'mkdtemp',return_value=str(out)), patch.object(h.runtime,'identity',return_value={}), patch.object(h.runtime,'versions',return_value={}), patch.object(h.shutil,'which',return_value='present'), patch.object(h,'execute',side_effect=execute):
            self.assertEqual(h.verify(self.root,'quick'),0)

    def test_changed_source_invalidates_success(self):
        out = self.root/'report';out.mkdir()
        with patch.object(h,'check',return_value=[]), patch.object(h.tempfile,'mkdtemp',return_value=str(out)), patch.object(h.runtime,'identity',side_effect=[{'commit':'before'},{'commit':'after'}]), patch.object(h.runtime,'versions',return_value={}), patch.object(h.shutil,'which',return_value='present'), patch.object(h,'execute',return_value={'status':'passed','exit_code':0}):
            self.assertEqual(h.verify(self.root,'quick'),1)
        report=json.loads((out/'summary.json').read_text())
        self.assertEqual(report['status'],'stale')
        self.assertTrue(report['source_changed_during_run'])

    def test_shared_dependencies_recommend_workbench(self):
        for path in ('go.mod', 'go.sum', 'Makefile', 'build.sh', 'internal/config/config.go'):
            with self.subTest(path=path), patch.object(h.runtime,'git',side_effect=[('Logan/ServerGo/' + path + '\0').encode(), b'']), patch.object(h,'read_registry',return_value=[]):
                profiles = h.plan(self.root)['recommended']
                self.assertIn('workbench', profiles)
                self.assertIn('workbench-http', profiles)

    def test_node_expansion_and_zero_tests(self):
        with self.assertRaises(ValueError):
            h.command_for(self.root,'workbench-node',self.root)
        path=self.root/'Logan/ServerGo/internal/workbenchui';path.mkdir(parents=True)
        (path/'a.test.mjs').touch()
        cmd,tools=h.command_for(self.root,'workbench-node',self.root)
        self.assertEqual(cmd,['node','--test','internal/workbenchui/a.test.mjs'])
        self.assertEqual(tools,['node'])

    def test_plan_includes_untracked_and_unknown(self):
        with patch.object(h.runtime,'git',side_effect=[b'Logan/ServerGo/internal/workbenchui/static/app.js\0',b'other/new\0']), patch.object(h,'read_registry',return_value=[]):
            report=h.plan(self.root)
        self.assertIn('workbench',report['recommended'])
        self.assertTrue(report['design_acceptance_required'])
        self.assertEqual(report['unmapped'],['other/new'])

    def test_acceptance_requires_all_states_and_evidence(self):
        evidence=self.root/'evidence';evidence.write_text('fixture')
        row={'id':'reader/normal','baseline_revision':'abc','implementation':'app.js',
             'comparison_conditions':'same viewport/data','interaction_result':'reviewed',
             'functional_result':'passed','design_result':'passed','differences':[],
             **{field:'evidence' for field in ('baseline','design_screenshot','implementation_screenshot','interaction_evidence')}}
        doc={'schema_version':1,'task':'reader','code_revision':'abc','reviewer':'tester','scope':['reader/normal'],'records':[row]}
        path=self.root/'record.json'
        def check():
            path.write_text(json.dumps(doc));return h.acceptance.validate(path)
        errors,files=check();self.assertEqual(errors,[]);self.assertEqual(len(files),4)
        doc['scope'].append('reader/empty');self.assertTrue(check()[0]);doc['scope'].pop()
        row['differences']=['layout changed'];self.assertTrue(check()[0]);row['differences']=[]
        row['design_result']='not_verified';self.assertTrue(check()[0]);row['design_result']='passed'
        evidence.unlink();self.assertTrue(check()[0])

    def test_template_cannot_pass(self):
        self.assertTrue(h.acceptance.validate(SCRIPT.parent/'acceptance-template.json')[0])


if __name__ == '__main__':
    unittest.main()
