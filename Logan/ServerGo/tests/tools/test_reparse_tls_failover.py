import importlib.util
from pathlib import Path
import unittest
ROOT=Path(__file__).resolve().parents[2]
spec=importlib.util.spec_from_file_location('tls_ha',ROOT/'script/verify-reparse-tls-failover.py');m=importlib.util.module_from_spec(spec);spec.loader.exec_module(m)
class TLSFailoverTests(unittest.TestCase):
 def test_actual_target_tls_required(self):
  self.assertEqual(m.validate_tls_rows('42\tTLSv1.3\tTLS_AES_256_GCM_SHA384\trepair')[0]['connectionID'],42)
  for raw in ('','42\t\t\trepair','42\tTLSv1\tAES\trepair','42\tTLSv1.3\tAES\troot','x\tTLSv1.3\tAES\treader'):
   with self.assertRaises(AssertionError):m.validate_tls_rows(raw)
 def test_cli_requires_verified_tls_without_skip(self):
  v=m.TLSFailover.__new__(m.TLSFailover);v.password='fixture';v.mysql='source';v.db='logan_reparse_it_test'
  calls=[];previous=m.prior.previous.Acceptance.cli
  try:
   m.prior.previous.Acceptance.cli=lambda *args,**kw:calls.append(kw)
   v.cli('--action','rollback',extra=('LOGAN_MYSQL_DSN=root:fixture@tcp(source:3306)/logan_reparse_it_test?parseTime=true&readTimeout=90s',))
  finally:m.prior.previous.Acceptance.cli=previous
  env=calls[0]['extra'];dsn=[x for x in env if x.startswith('LOGAN_MYSQL_DSN=')][0]
  self.assertIn('repair:fixture@tcp',dsn);self.assertIn('tls=true',dsn);self.assertNotIn('skip-verify',dsn);self.assertIn('SSL_CERT_FILE=/evidence/tls/ca.pem',env)
 def test_server_requires_tls_and_real_replication(self):
  v=m.TLSFailover.__new__(m.TLSFailover);args=v.server_options(2)
  for flag in ('--require-secure-transport=ON','--gtid-mode=ON','--enforce-gtid-consistency=ON','--server-id=2'):self.assertIn(flag,args)
 def test_replication_password_redacted_alongside_admin_password(self):
  v=m.TLSFailover.__new__(m.TLSFailover);v.password='a'*48;captured=[]
  previous=m.old.DatabaseRecovery.log
  try:
   m.old.DatabaseRecovery.log=lambda self,command,output,code:captured.append((command,output))
   v.log(['sql',v.password,v.password[:32]],v.password+' '+v.password[:32],0)
  finally:m.old.DatabaseRecovery.log=previous
  self.assertNotIn('a'*32,str(captured));self.assertIn('<synthetic-replication-secret>',str(captured))
 def test_replica_status_requires_named_healthy_fields(self):
  raw='\n'.join(k+': '+v for k,v in {'Replica_IO_Running':'Yes','Replica_SQL_Running':'Yes','Source_SSL_Allowed':'Yes','Source_SSL_Verify_Server_Cert':'Yes','Last_SQL_Errno':'0','Last_IO_Errno':'0'}.items())
  self.assertEqual(m.validate_replica_status(raw)['Last_IO_Errno'],'0')
  for bad in ('Yes\nYes\n0',raw.replace('Last_IO_Errno: 0','Last_IO_Errno: 1045'),raw.replace('Replica_SQL_Running: Yes','Replica_SQL_Running: No'),raw.replace('Source_SSL_Verify_Server_Cert: Yes','Source_SSL_Verify_Server_Cert: No')):
   with self.assertRaises(AssertionError):m.validate_replica_status(bad)
 def test_fixture_environment_overrides_have_no_duplicate_keys(self):
  raw=m.prior.previous.merge_cli_environment('LOGAN_MYSQL_DSN=root:secret@tcp(source)/db\nSSL_CERT_FILE=old\n',('LOGAN_MYSQL_DSN=repair:secret@tcp(source)/db?tls=true','SSL_CERT_FILE=new'))
  self.assertEqual(raw.count('LOGAN_MYSQL_DSN='),1);self.assertEqual(raw.count('SSL_CERT_FILE='),1)
  self.assertIn('LOGAN_MYSQL_DSN=repair:',raw);self.assertIn('SSL_CERT_FILE=new',raw)
  with self.assertRaises(AssertionError):m.prior.previous.merge_cli_environment('',('bad-key=x',))
if __name__=='__main__':unittest.main()
