import copy
import importlib.util
import json
from pathlib import Path
import sys
import tempfile
import unittest
from unittest.mock import patch

SCRIPT = Path(__file__).resolve().parents[1] / 'run.py'
spec = importlib.util.spec_from_file_location('harness_closure', SCRIPT)
h = importlib.util.module_from_spec(spec)
spec.loader.exec_module(h)


class ClosureTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.source = {'commit': 'abc', 'workspace_sha256': 'content'}
        artifact = self.root/'log';artifact.write_text('test evidence')
        self.artifacts = [{'path': str(artifact), 'sha256': h.closure.file_hash(artifact)}]
        self.plan = {'task':'task-a', 'source':self.source, 'recommended':['quick'],
                     'integrations':[], 'design_acceptance_required':False, 'unmapped':[]}
        self.report = {'kind':'verification', 'task':'task-a', 'profile':'quick',
                       'source_start':self.source, 'source_end':self.source,
                       'status':'passed', 'exit_code':0, 'unexecuted':[], 'artifacts':self.artifacts,
                       'steps':[{'id':step, 'status':'passed','exit_code':0} for step in h.PROFILES['quick']]}

    def summary(self, reports=None):
        rows = [(self.root/str(i), report) for i,report in enumerate(reports if reports is not None else [self.report])]
        return h.closure.summarize(self.plan, rows, self.source, h.PROFILES)

    def test_matching_report_passes(self):
        self.assertEqual(self.summary()['status'],'passed')

    def test_missing_wrong_task_and_stale_are_distinct(self):
        self.assertEqual(self.summary([])['checks'][0]['status'],'missing')
        self.report['task']='other'
        self.assertEqual(self.summary()['checks'][0]['status'],'wrong_task')
        self.report['task']='task-a';self.report['source_end']={'commit':'older','workspace_sha256':'content'}
        self.assertEqual(self.summary()['checks'][0]['status'],'stale')

    def test_failed_and_partial_report_cannot_pass(self):
        self.report['status']='failed';self.report['exit_code']=1
        self.assertEqual(self.summary()['checks'][0]['status'],'failed')
        self.report['status']='passed';self.report['exit_code']=0;self.report['steps'].pop()
        self.assertEqual(self.summary()['checks'][0]['status'],'invalid')

    def test_duplicate_reports_are_not_cherry_picked(self):
        bad=copy.deepcopy(self.report);bad['status']='failed'
        self.assertEqual(self.summary([self.report,bad])['checks'][0]['status'],'ambiguous')

    def test_evidence_changed_or_removed_fails(self):
        path=Path(self.artifacts[0]['path']);path.write_text('changed')
        self.assertEqual(self.summary()['checks'][0]['status'],'invalid')
        path.unlink()
        self.assertEqual(self.summary()['checks'][0]['status'],'invalid')

    def test_external_and_design_require_separate_evidence(self):
        self.plan['integrations']=[{'id':'sdk','profile':None}]
        self.plan['design_acceptance_required']=True
        rows=self.summary()['checks']
        self.assertEqual([(r['check'],r['status']) for r in rows],[('quick','passed'),('integration:sdk','missing'),('design','missing')])

    def test_unbound_and_unmapped_plan_fails(self):
        self.plan['source']={}
        self.assertEqual(self.summary()['status'],'incomplete')
        self.plan['source']=self.source;self.plan['unmapped']=['unknown/file']
        self.assertEqual(self.summary()['status'],'incomplete')

    def design_record(self):
        path=self.root/'design.json'
        row={'id':'reader/normal','baseline_revision':'approved-version','implementation':'app.js',
             'comparison_conditions':'same viewport/data','interaction_result':'reviewed',
             'functional_result':'passed','design_result':'passed','differences':[],
             **{field:'log' for field in ('baseline','design_screenshot','implementation_screenshot','interaction_evidence')}}
        doc={'schema_version':1,'task':'task-a','code_revision':'abc','code_workspace_sha256':'content',
             'reviewer':'tester','scope':['reader/normal'],'records':[row]}
        path.write_text(json.dumps(doc))
        return path,doc

    def test_design_declaration_is_preserved_and_bound(self):
        path,doc=self.design_record()
        report=h.acceptance.report(path,self.source,self.source,'task-a')
        self.assertEqual(report['status'],'passed')
        self.assertEqual(report['declared'],doc)
        self.assertEqual(report['record_sha256'],h.closure.file_hash(path))
        self.plan['design_acceptance_required']=True
        self.assertEqual(self.summary([self.report,report])['status'],'passed')

    def test_old_design_version_and_wrong_task_rejected(self):
        path,doc=self.design_record();doc['code_workspace_sha256']='old';path.write_text(json.dumps(doc))
        report=h.acceptance.report(path,self.source,self.source,'task-a')
        self.assertEqual(report['completeness'],'complete')
        self.assertEqual(report['binding'],'stale');self.assertEqual(report['exit_code'],1)
        doc['code_workspace_sha256']='content';path.write_text(json.dumps(doc))
        self.assertEqual(h.acceptance.report(path,self.source,self.source,'different')['exit_code'],1)

    def test_edited_acceptance_record_invalidates_report(self):
        path,doc=self.design_record()
        report=h.acceptance.report(path,self.source,self.source,'task-a')
        self.plan['design_acceptance_required']=True
        doc['reviewer']='different';path.write_text(json.dumps(doc))
        self.assertEqual(self.summary([self.report,report])['checks'][-1]['status'],'invalid')

    def test_explicit_scope_preserves_excluded_paths(self):
        with patch.object(h.runtime,'git',side_effect=[b'.harness/run.py\0Logan/ServerGo/go.mod\0',b'']), patch.object(h,'read_registry',return_value=[]):
            result=h.plan(self.root,task='task-a',selected=['.harness'])
        self.assertEqual(result['recommended'],['self-test'])
        self.assertEqual(result['out_of_scope'],['Logan/ServerGo/go.mod'])
        self.assertEqual(result['task'],'task-a')


if __name__ == '__main__':
    unittest.main()
