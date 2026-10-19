import json
import os
from pathlib import Path
import signal
import subprocess
import sys
import tempfile
import time
import unittest

ROOT = Path(__file__).resolve().parents[2]

class MonitoringProcessTests(unittest.TestCase):
    def test_sigterm_stops_detached_children(self):
        with tempfile.TemporaryDirectory(prefix='logan-smoke-signal-') as directory:
            root = Path(directory)
            pids_file = root/'pids'
            binary = root/'fake-service'
            binary.write_text('#!'+sys.executable+'\nimport os,time\nwith open(os.environ["SMOKE_TEST_PID_FILE"],"a") as f: f.write(str(os.getpid())+"\\n")\ntime.sleep(60)\n')
            binary.chmod(0o700)
            command = [sys.executable,str(ROOT/'script/smoke-monitoring.py'),'--allow-local-processes','--server',str(binary),'--prometheus',str(binary),'--alertmanager',str(binary)]
            process = subprocess.Popen(command, env={**os.environ,'SMOKE_TEST_PID_FILE':str(pids_file)}, stdout=subprocess.PIPE,stderr=subprocess.STDOUT,text=True)
            pids = []
            try:
                deadline = time.monotonic()+10
                while time.monotonic()<deadline:
                    if pids_file.exists():
                        pids = [int(n) for n in pids_file.read_text().split()]
                    if len(pids)==3: break
                    if process.poll() is not None: break
                    time.sleep(.05)
                self.assertEqual(len(pids),3,'test services never started')
                process.terminate()
                output,_ = process.communicate(timeout=15)
                self.assertEqual(process.returncode,1,'SIGTERM must unwind through cleanup')
                path = next(line[10:] for line in output.splitlines() if line.startswith('Evidence: '))
                summary = json.loads(Path(path).read_text())
                self.assertEqual(summary['status'],'failed')
                self.assertTrue(summary['all_test_processes_stopped'])
                for pid in pids:
                    with self.assertRaises(ProcessLookupError): os.kill(pid,0)
            finally:
                if process.poll() is None: process.kill(); process.wait()
                for pid in pids:
                    try: os.killpg(pid,signal.SIGKILL)
                    except ProcessLookupError: pass

if __name__=='__main__': unittest.main()
