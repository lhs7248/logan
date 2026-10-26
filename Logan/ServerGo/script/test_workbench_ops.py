import importlib.util
import json
import pathlib
import tempfile
import unittest
import fcntl
spec = importlib.util.spec_from_file_location('ops', pathlib.Path(__file__).with_name('workbench-ops.py'))
ops = importlib.util.module_from_spec(spec)
if spec.loader and pathlib.Path(spec.origin).exists(): spec.loader.exec_module(ops)
class Operations(unittest.TestCase):
 def setUp(self):
  self.tmp=tempfile.TemporaryDirectory(dir='/tmp');self.addCleanup(self.tmp.cleanup);self.root=pathlib.Path(self.tmp.name).resolve();self.data=self.root/'data';self.data.mkdir();(self.data/'state.json').write_text('{"objects":{},"idempotency":{}}');self.auth=self.root/'auth.json';self.auth.write_text('{"projects":[{"id":"p"}],"tokens":[{"token":"0123456789abcdef","actor":"a","projects":{"p":"admin"}}]}');self.out=self.root/'backup'
 def backup(self): ops.backup(self.data,self.auth,self.out,[])
 def test_roundtrip_private(self):
  key=self.root/'keys.env';key.write_text('SYNTHETIC_SECRET');ops.backup(self.data,self.auth,self.out,[key]);dest=self.root/'restored';ops.restore(self.out,dest);self.assertEqual((dest/'data/state.json').read_bytes(),(self.data/'state.json').read_bytes());self.assertEqual((dest/'config/keys.env').read_bytes(),key.read_bytes());self.assertEqual(dest.stat().st_mode&0o777,0o700);self.assertEqual((dest/'auth.json').stat().st_mode&0o777,0o600)
 def test_locked(self):
  with (self.data/'.lock').open('a') as lock:
   fcntl.flock(lock,fcntl.LOCK_EX|fcntl.LOCK_NB)
   with self.assertRaises(ValueError): self.backup()
  self.assertFalse(self.out.exists())
 def test_checksum_and_no_restore_output(self):
  self.backup();(self.out/'auth.json').write_text('tamper');dest=self.root/'restore'
  with self.assertRaises(ValueError):ops.restore(self.out,dest)
  self.assertFalse(dest.exists())
 def test_existing_destination(self):
  self.backup();dest=self.root/'restore';dest.mkdir();(dest/'keep').write_text('keep')
  with self.assertRaises(ValueError):ops.restore(self.out,dest)
  self.assertEqual((dest/'keep').read_text(),'keep')
 def test_symlink(self):
  self.auth.unlink();self.auth.symlink_to(self.data/'state.json')
  with self.assertRaises(ValueError):self.backup()
 def test_malformed_state(self):
  (self.data/'state.json').write_text('{"objects":null}')
  with self.assertRaises(ValueError):self.backup()
 def test_unsafe_manifest_path(self):
  self.backup();p=self.out/'manifest.json';m=json.loads(p.read_text());m['files']['../escape']='x';p.write_text(json.dumps(m))
  with self.assertRaises(ValueError):ops.verify(self.out)
 def test_existing_backup(self):
  self.out.mkdir()
  with self.assertRaises(ValueError):self.backup()
 def test_backup_symlink_entry(self):
  self.backup();(self.out/'unexpected').symlink_to(self.auth)
  with self.assertRaises(ValueError):ops.verify(self.out)
 def test_bad_auth_grant(self):
  auth=json.loads(self.auth.read_text());auth['tokens'][0]['projects']={'missing':'admin'};self.auth.write_text(json.dumps(auth))
  with self.assertRaises(ValueError):self.backup()
 def test_bad_receipt(self):
  (self.data/'state.json').write_text('{"objects":{},"idempotency":{"x":{"status":"200"}}}')
  with self.assertRaises(ValueError):self.backup()
 def test_materialize_directory_sync(self):
  from unittest.mock import patch
  import stat
  original=ops.os.fsync
  seen=[]
  def sync(fd):
   if stat.S_ISDIR(ops.os.fstat(fd).st_mode):seen.append(fd)
   return original(fd)
  with patch.object(ops.os,'fsync',side_effect=sync):self.backup()
  self.assertGreaterEqual(len(seen),3)
if __name__=='__main__':unittest.main()
