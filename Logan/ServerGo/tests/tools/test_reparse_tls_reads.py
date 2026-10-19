import importlib.util
from pathlib import Path
from types import SimpleNamespace
import tempfile
import unittest

spec = importlib.util.spec_from_file_location('tls_reads', Path(__file__).resolve().parents[1] / 'reparse/real_reads.py')
reads = importlib.util.module_from_spec(spec)
spec.loader.exec_module(reads)


class TLSReadConfigTests(unittest.TestCase):
    def test_plaintext_default_unchanged(self):
        v = SimpleNamespace(mysql='mysql', db='logan_reparse_it_fixture', password='secret')
        config = reads.reader_database_config(v)
        self.assertIn('useSSL=false', config['jdbc'])
        self.assertNotIn('tls=', config['dsn'])
        self.assertEqual(config['java_mounts'], [])
        self.assertEqual(config['go_env'], [])

    def test_tls_requires_identity_and_mounted_trust(self):
        with tempfile.TemporaryDirectory() as directory:
            ca = Path(directory) / 'ca.pem'; ca.write_text('fixture CA')
            trust = Path(directory) / 'trust.p12'; trust.write_bytes(b'fixture store')
            v = SimpleNamespace(mysql='mysql', mysql_read_host='writer.fixture', db='logan_reparse_it_fixture', password='secret',
                                mysql_read_user='reader', mysql_read_password='reader-secret',
                                read_mysql_tls={'ca': ca, 'truststore': trust, 'password': 'fixture-only'})
            config = reads.reader_database_config(v)
            self.assertIn('mysql://writer.fixture:3306/', config['jdbc'])
            self.assertIn('sslMode=VERIFY_IDENTITY', config['jdbc'])
            self.assertNotIn('useSSL=false', config['jdbc'])
            self.assertIn('trustCertificateKeyStoreUrl=file:/fixture/mysql-truststore.p12', config['jdbc'])
            self.assertIn('trustCertificateKeyStoreType=PKCS12', config['jdbc'])
            self.assertIn('trustCertificateKeyStorePassword=fixture-only', config['jdbc'])
            self.assertIn('reader:reader-secret@tcp(writer.fixture:3306)', config['dsn'])
            self.assertTrue(config['dsn'].endswith('&tls=true'))
            self.assertEqual(config['go_env'], ['SSL_CERT_FILE=/fixture/mysql-ca.pem'])
            self.assertIn('dst=/fixture/mysql-ca.pem,readonly', config['go_mounts'][1])
            self.assertIn('dst=/fixture/mysql-truststore.p12,readonly', config['java_mounts'][1])
            self.assertEqual(config['user'], 'reader')
            self.assertEqual(config['password'], 'reader-secret')

    def test_incomplete_tls_fails_without_plaintext_fallback(self):
        v = SimpleNamespace(mysql='mysql', db='logan_reparse_it_fixture', password='secret', read_mysql_tls={})
        with self.assertRaisesRegex(AssertionError, 'TLS'):
            reads.reader_database_config(v)
        v.read_mysql_tls = {'ca': '/absent', 'truststore': '/absent', 'password': 'fixture-only'}
        with self.assertRaisesRegex(AssertionError, 'TLS'):
            reads.reader_database_config(v)

    def test_tls_observation_hook_runs_before_shutdown_and_records_evidence(self):
        calls = []
        v = SimpleNamespace(verify_reader_tls=lambda runtime, container: calls.append((runtime, container)) or {'cipher': 'TLS_AES_256_GCM_SHA384'})
        events = []
        reads.observe_reader_tls(v, 'go', 'owned-go', events)
        self.assertEqual(calls, [('go', 'owned-go')])
        self.assertEqual(events, [{'runtime': 'go', 'event': 'mysql-tls-verified', 'evidence': {'cipher': 'TLS_AES_256_GCM_SHA384'}}])
        reads.observe_reader_tls(SimpleNamespace(), 'java', 'owned-java', events)
        self.assertEqual(len(events), 1)


if __name__ == '__main__':
    unittest.main()
