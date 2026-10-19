import subprocess
import sys
from pathlib import Path
import unittest

ROOT=Path(__file__).resolve().parents[2]

class ContractTests(unittest.TestCase):
    def test_extra_database_is_rejected_with_python_optimization(self):
        code='import runpy; namespace=runpy.run_path('+repr(str(ROOT/'script/check-deployment.py'))+'); namespace["validate_compose"]({"services":{"db":{}}})'
        result=subprocess.run([sys.executable,'-O','-c',code],stdout=subprocess.PIPE,stderr=subprocess.PIPE,text=True)
        self.assertNotEqual(result.returncode,0)
        self.assertIn('unexpected services',result.stderr)

if __name__=='__main__':unittest.main()
