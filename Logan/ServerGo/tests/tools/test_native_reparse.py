import argparse
import importlib.util
from pathlib import Path
import signal
import tempfile
import unittest
from unittest.mock import patch

ROOT=Path(__file__).resolve().parents[2]
spec=importlib.util.spec_from_file_location('native_reparse_runner',ROOT/'script/verify-native-reparse.py')
runner=importlib.util.module_from_spec(spec);spec.loader.exec_module(runner)

class NativeReparseControllerTests(unittest.TestCase):
    def args(self,root):
        return argparse.Namespace(host='unix:///private/tmp/logan-test/sock/docker.sock',allow_isolated_runtime=True,
            docker='/usr/bin/true',binary=Path('/usr/bin/true'),out=root/'out',work_dir=root/'work')

    def test_explicit_runtime_and_private_external_paths(self):
        with tempfile.TemporaryDirectory() as d:
            root=Path(d);a=self.args(root);runner.validate_args(a)
            a.allow_isolated_runtime=False
            with self.assertRaises(ValueError):runner.validate_args(a)
            a=self.args(root);a.out=runner.base.CHECKOUT/'new-evidence'
            with self.assertRaises(ValueError):runner.validate_args(a)
            a=self.args(root);a.out=root/'comma,path'
            with self.assertRaises(ValueError):runner.validate_args(a)
            a=self.args(root);a.work_dir=a.out/'nested'
            with self.assertRaises(ValueError):runner.validate_args(a)
            a=self.args(root);a.binary=Path('relative')
            with self.assertRaises(ValueError):runner.validate_args(a)

    def test_sigterm_uses_interrupt_path(self):
        with self.assertRaises(KeyboardInterrupt):runner.interrupted(signal.SIGTERM,None)

    def test_cleanup_masks_signals_removes_credentials_and_records_interruption(self):
        with tempfile.TemporaryDirectory() as d:
            root=Path(d);a=self.args(root);a.out.mkdir();(a.out/'cli.env').write_text('synthetic-secret')
            class V:
                checks=[]
                def cleanup(self):self.checks.append({'check':'cleanup','status':'passed'})
            with patch.object(runner.signal,'signal') as signals,patch('builtins.print'):
                self.assertEqual(runner.finish(V(),a,130,'KeyboardInterrupt'),130)
                self.assertEqual(signals.call_args_list[0].args,(signal.SIGINT,signal.SIG_IGN))
                self.assertEqual(signals.call_args_list[1].args,(signal.SIGTERM,signal.SIG_IGN))
            self.assertFalse((a.out/'cli.env').exists())
            self.assertEqual((a.out/'summary.json').stat().st_mode&0o777,0o600)
            self.assertIn('KeyboardInterrupt',(a.out/'summary.json').read_text())

if __name__=='__main__':unittest.main()
