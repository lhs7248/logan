import contextlib
import importlib.util
import io
import json
import os
from pathlib import Path
import plistlib
import socket
import tempfile
import unittest
from unittest.mock import patch

SCRIPT = Path(__file__).with_name('workbench-local.py')

class RuntimeTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        spec = importlib.util.spec_from_file_location('workbench_local', SCRIPT)
        self.m = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(self.m)
        self.r = self.m.Runtime(Path(self.tmp.name) / 'runtime')

    def test_init_reuses_private_random_credentials_and_preserves_business_data(self):
        self.r.init()
        initial = self.r.settings.read_bytes()
        auth = json.loads(self.r.auth.read_text())
        self.assertNotEqual(auth['tokens'][0]['token'], auth['sdkClients'][0]['token'])
        self.assertEqual(auth['sdkClients'][0]['projectId'], 'local-mobile')
        self.assertEqual(auth['sdkClients'][0]['platforms'], ['android'])
        self.assertEqual(auth['tokens'][0]['permissions'], ['raw', 'export'])
        marker = self.r.root / 'data' / 'keep'
        marker.write_text('business data')
        self.r.init()
        self.assertEqual(initial, self.r.settings.read_bytes())
        self.assertEqual(marker.read_text(), 'business data')
        self.assertEqual(self.r.root.stat().st_mode & 0o777, 0o700)
        for path in (self.r.settings, self.r.auth):
            self.assertEqual(path.stat().st_mode & 0o777, 0o600)
        self.assertEqual(self.r.profile()['deviceId'], self.r.profile()['deviceId'])
        for field in ('nativeKeyBase64', 'nativeIVBase64'):
            decoded = self.m.base64.b64decode(self.r.profile()[field])
            self.assertEqual(len(decoded), 16)
            self.assertTrue(all(byte in b'abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_-' for byte in decoded))

    def test_legacy_android_profile_rejects_binary_keys_without_rotation(self):
        self.r.init()
        cfg = self.r.config()
        cfg['nativeKeyBase64'] = self.m.base64.b64encode(bytes(range(16))).decode()
        self.m.private_json(self.r.settings, cfg, exclusive=False)
        original = self.r.settings.read_bytes()
        with self.assertRaisesRegex(self.m.RuntimeError, 'ASCII'):
            self.r.init()
        self.assertEqual(original, self.r.settings.read_bytes())

    def test_partial_state_and_symlinks_fail_without_overwrite(self):
        self.r.root.mkdir()
        self.r.settings.write_text('{}')
        with self.assertRaises(self.m.RuntimeError):
            self.r.init()
        self.assertEqual(self.r.settings.read_text(), '{}')

    def test_secrets_are_only_exported_explicitly(self):
        self.r.init()
        out = io.StringIO()
        with contextlib.redirect_stdout(out):
            self.m.main(['--root', str(self.r.root), 'app-profile'])
            self.m.main(['--root', str(self.r.root), 'credentials'])
        self.assertNotIn(self.r.profile()['uploadToken'], out.getvalue())
        target = Path(self.tmp.name) / 'profile.json'
        self.m.main(['--root', str(self.r.root), 'app-profile', '--output', str(target)])
        self.assertEqual(json.loads(target.read_text()), self.r.profile())
        self.assertEqual(target.stat().st_mode & 0o777, 0o600)
        with self.assertRaises(self.m.RuntimeError):
            self.r.export_profile(target)

    def test_business_profile_retains_example_and_persists_independent_identity(self):
        self.r.init()
        settings = self.r.settings.read_bytes()
        original = json.loads(self.r.auth.read_text())
        marker = self.r.root / 'data' / 'keep'
        marker.write_bytes(b'original')
        first = self.r.business_profile()
        self.assertEqual(first, self.r.business_profile())
        self.assertEqual(set(first), set(self.r.profile()))
        self.assertEqual(first['appId'], 'com.hxl.xlb.test')
        self.assertNotEqual(first['uploadToken'], self.r.profile()['uploadToken'])
        self.assertNotEqual(first['deviceId'], self.r.profile()['deviceId'])
        self.assertEqual(first['nativeKeyBase64'], self.r.profile()['nativeKeyBase64'])
        self.assertEqual(first['nativeIVBase64'], self.r.profile()['nativeIVBase64'])
        auth = json.loads(self.r.auth.read_text())
        self.assertEqual(auth['sdkClients'][0], original['sdkClients'][0])
        self.assertEqual(auth['tokens'], original['tokens'])
        self.assertEqual(auth['projects'], original['projects'])
        self.assertEqual(auth['sdkClients'][1]['environment'], 'test-local')
        self.assertEqual(auth['sdkClients'][1]['platforms'], ['android'])
        self.assertEqual(self.r.auth.stat().st_mode & 0o777, 0o600)
        self.assertEqual(settings, self.r.settings.read_bytes())
        self.assertEqual(marker.read_bytes(), b'original')
        self.r.init()
        self.assertEqual(first, self.r.business_profile())

    def test_business_profile_rejects_partial_state_and_collisions_without_changes(self):
        self.r.init()
        self.r.business_profile()
        valid = json.loads(self.r.auth.read_text())
        mutations = [lambda a: a['sdkClients'][1].update(environment='prod'),
                     lambda a: a['sdkClients'][1].update(token=a['tokens'][0]['token']),
                     lambda a: a['sdkClients'].append(dict(a['sdkClients'][1])),
                     lambda a: a.pop('businessProfiles'),
                     lambda a: a.update(businessProfiles=[])]
        for mutate in mutations:
            auth = json.loads(json.dumps(valid))
            mutate(auth)
            self.m.private_json(self.r.auth, auth, exclusive=False)
            before = self.r.auth.read_bytes()
            with self.assertRaises(self.m.RuntimeError):
                self.r.business_profile()
            self.assertEqual(before, self.r.auth.read_bytes())

    def test_business_profile_atomic_failure_preserves_auth(self):
        self.r.init()
        before = self.r.auth.read_bytes()
        with patch.object(self.m.os, 'replace', side_effect=OSError('write failure')):
            with self.assertRaises(OSError):
                self.r.business_profile()
        self.assertEqual(before, self.r.auth.read_bytes())

    def test_business_export_never_prints_secrets_or_overwrites(self):
        self.r.init()
        target = Path(self.tmp.name) / 'business.json'
        out = io.StringIO()
        with contextlib.redirect_stdout(out):
            self.m.main(['--root', str(self.r.root), 'business-app-profile', '--output', str(target)])
        profile = json.loads(target.read_text())
        self.assertNotIn(profile['uploadToken'], out.getvalue())
        self.assertNotIn(profile['nativeKeyBase64'], out.getvalue())
        self.assertEqual(target.stat().st_mode & 0o777, 0o600)
        before = self.r.auth.read_bytes()
        with self.assertRaises(self.m.RuntimeError):
            self.m.main(['--root', str(self.r.root), 'business-app-profile', '--output', str(target)])
        self.assertEqual(before, self.r.auth.read_bytes())
        self.assertEqual(profile, json.loads(target.read_text()))

    def test_runtime_environment_defers_external_connectors(self):
        self.r.init()
        with patch.dict(os.environ, {'WORKBENCH_MYSQL_DSN':'secret', 'WORKBENCH_SOURCE_REPOS':'bad', 'WORKBENCH_MODEL_ENDPOINT':'bad', 'HTTP_PROXY':'bad'}):
            env = self.r.environment()
        self.assertEqual(env['WORKBENCH_ADDR'], '127.0.0.1:8091')
        for key in ('WORKBENCH_MYSQL_DSN', 'WORKBENCH_SOURCE_REPOS', 'WORKBENCH_MODEL_ENDPOINT', 'HTTP_PROXY'):
            self.assertNotIn(key, env)

    def test_agent_contains_no_secrets_and_has_persistent_restart_policy(self):
        self.r.init()
        agent = self.r.agent_spec()
        self.assertTrue(agent['RunAtLoad'])
        self.assertTrue(agent['KeepAlive'])
        self.assertEqual(agent['ProgramArguments'][-1], 'run')
        self.assertNotIn(self.r.profile()['uploadToken'].encode(), plistlib.dumps(agent))
        self.assertEqual(Path(agent['WorkingDirectory']), self.r.module)

    def test_collision_fails_and_stop_never_signals_unowned_process(self):
        self.r.init()
        self.r.binary.write_bytes(b'executable')
        with patch.object(self.r, 'owned_pid', return_value=None), patch.object(self.r, 'port_busy', return_value=True):
            with self.assertRaises(self.m.RuntimeError):
                self.r.start()
        with patch.object(self.r, 'launch_state', return_value='pid = 123'), patch.object(self.r, 'check_agent'), patch.object(self.r, 'owned_pid', return_value=None), patch.object(self.r, 'launchctl') as ctl:
            with self.assertRaises(self.m.RuntimeError):
                self.r.stop()
            ctl.assert_not_called()

    def test_http_redirect_never_forwards_credentials(self):
        handler = self.m.NoRedirect()
        request = self.m.urllib.request.Request('http://127.0.0.1:8091', headers={'Authorization':'Bearer local-secret'})
        self.assertIsNone(handler.redirect_request(request, None, 302, 'Found', {}, 'https://example.invalid'))

    def test_stop_only_boots_out_matching_agent_and_preserves_all_files(self):
        self.r.init()
        snapshot = self.r.settings.read_bytes()
        with patch.object(self.r, 'launch_state', side_effect=['pid = 123', None, None]), patch.object(self.r, 'check_agent') as check, patch.object(self.r, 'owned_pid', return_value=123), patch.object(self.r, 'launchctl') as ctl, patch.object(self.m.subprocess, 'run', return_value=self.m.subprocess.CompletedProcess([], 1, stdout='')):
            self.r.stop()
            check.assert_called_once()
            ctl.assert_called_once_with('bootout', self.r.target)
        self.assertEqual(snapshot, self.r.settings.read_bytes())

    def test_symlink_configuration_is_rejected(self):
        self.r.init()
        target = self.r.settings.with_name('other.json')
        self.r.settings.rename(target)
        self.r.settings.symlink_to(target)
        with self.assertRaises(self.m.RuntimeError):
            self.r.config()

    def test_stop_waits_for_verified_process_after_launchd_unloads(self):
        self.r.init()
        alive = self.m.subprocess.CompletedProcess([], 0, stdout=str(self.r.binary) + '\n')
        exited = self.m.subprocess.CompletedProcess([], 1, stdout='')
        with patch.object(self.r, 'launch_state', side_effect=['pid = 321', None, None]), patch.object(self.r, 'check_agent'), patch.object(self.r, 'owned_pid', return_value=321), patch.object(self.r, 'launchctl'), patch.object(self.m.subprocess, 'run', side_effect=[alive, exited]) as ps, patch.object(self.m.time, 'sleep') as sleep:
            self.r.stop()
            self.assertEqual(ps.call_count, 2)
            sleep.assert_called_once()

    def test_stop_treats_pid_reuse_as_owned_process_exit_without_signals(self):
        self.r.init()
        reused = self.m.subprocess.CompletedProcess([], 0, stdout='/unrelated/process\n')
        with patch.object(self.r, 'launch_state', side_effect=['pid = 321', None]), patch.object(self.r, 'check_agent'), patch.object(self.r, 'owned_pid', return_value=321), patch.object(self.r, 'launchctl') as ctl, patch.object(self.m.subprocess, 'run', return_value=reused), patch.object(self.m.os, 'kill') as kill:
            self.r.stop()
            ctl.assert_called_once_with('bootout', self.r.target)
            kill.assert_not_called()

    def test_port_probe_allows_tcp_time_wait_rebinding(self):
        with patch.object(self.m.socket, 'socket') as factory:
            sock = factory.return_value.__enter__.return_value
            self.assertFalse(self.r.port_busy())
            sock.setsockopt.assert_called_once_with(self.m.socket.SOL_SOCKET, self.m.socket.SO_REUSEADDR, 1)

    def test_start_is_idempotent_for_owned_authenticated_build(self):
        self.r.init()
        self.r.binary.write_bytes(b'test binary')
        self.m.private_json(self.r.identity, {'sha256':self.r.binary_hash()})
        with patch.object(self.r, 'owned_pid', return_value=321), patch.object(self.r, 'ready') as ready, patch.object(self.r, 'launchctl') as ctl:
            self.r.start()
            ready.assert_called_once()
            ctl.assert_not_called()

    def test_status_requires_running_build_identity_and_workspace(self):
        self.r.init()
        self.r.binary.write_bytes(b'test binary')
        self.m.private_json(self.r.identity, {'sha256':'wrong'})
        with patch.object(self.r, 'owned_pid', return_value=321), patch.object(self.r, 'ready'):
            with self.assertRaises(self.m.RuntimeError):
                self.r.status()
        self.m.private_json(self.r.identity, {'sha256':self.r.binary_hash()}, exclusive=False)
        with patch.object(self.r, 'owned_pid', return_value=321), patch.object(self.r, 'ready'):
            result = self.r.status()
        self.assertEqual(result['state'], 'ready')
        self.assertEqual(result['pid'], 321)
        self.assertTrue(result['authenticatedWorkspace'])

    def test_process_path_must_match_exact_binary_before_control(self):
        self.r.init()
        self.r.plist = self.r.root / 'test.plist'
        self.r.plist.write_bytes(plistlib.dumps(self.r.agent_spec()))
        self.m.private_json(self.r.identity, {'pid':321, 'binary':str(self.r.binary)})
        result = self.m.subprocess.CompletedProcess([], 0, stdout='/another/workbench\n')
        with patch.object(self.r, 'launch_state', return_value='pid = 321'), patch.object(self.m.subprocess, 'run', return_value=result):
            with self.assertRaises(self.m.RuntimeError):
                self.r.owned_pid()
        result.stdout = str(self.r.binary) + '\n'
        with patch.object(self.r, 'launch_state', return_value='pid = 321'), patch.object(self.m.subprocess, 'run', return_value=result):
            self.assertEqual(self.r.owned_pid(), 321)

    def test_readiness_rejects_generic_200_and_wrong_project(self):
        self.r.init()
        with patch.object(self.r, 'request', return_value={'data':{}}):
            with self.assertRaises(self.m.RuntimeError):
                self.r.ready()
        with patch.object(self.r, 'request', return_value={'data':{'projectId':'other','actor':self.r.config()['actor'], 'role':'admin','projects':[{'id':'local-mobile'}]}}):
            with self.assertRaises(self.m.RuntimeError):
                self.r.ready()

class AndroidProvisionTests(unittest.TestCase):
    def test_missing_example_profile_uses_shell_exit_status_then_private_stdin(self):
        spec = importlib.util.spec_from_file_location('example_android', SCRIPT.with_name('workbench-android.py'))
        module = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(module)
        with tempfile.TemporaryDirectory() as tmp:
            runtime_spec = importlib.util.spec_from_file_location('local_for_provision', SCRIPT)
            local = importlib.util.module_from_spec(runtime_spec)
            runtime_spec.loader.exec_module(local)
            runtime = local.Runtime(Path(tmp) / 'runtime')
            runtime.init()
            profile = runtime.profile()
            calls = []

            def adb_run(command, **kwargs):
                calls.append((command, kwargs))
                if 'test' in command:
                    # Real adb exec-out can report success despite a missing remote file.
                    return module.subprocess.CompletedProcess(command, 1 if 'shell' in command else 0, stdout=b'')
                if 'cat' in command:
                    return module.subprocess.CompletedProcess(command, 0, stdout=json.dumps(profile).encode())
                return module.subprocess.CompletedProcess(command, 0, stdout=b'')

            argv = ['workbench-android.py', '--serial', 'test-device', '--root', str(runtime.root), 'provision']
            with patch.object(module.subprocess, 'run', side_effect=adb_run), patch('sys.argv', argv), contextlib.redirect_stdout(io.StringIO()):
                module.main()
            probes = [command for command, kwargs in calls if 'test' in command]
            self.assertEqual(probes, [['adb', '-s', 'test-device', 'shell', 'run-as', module.APP, 'test', '-e', 'files/workbench-profile.json']])
            writes = [(command, kwargs) for command, kwargs in calls if kwargs.get('input')]
            self.assertEqual(len(writes), 1)
            self.assertEqual(json.loads(writes[0][1]['input']), profile)
            self.assertNotIn(profile['uploadToken'], ' '.join(writes[0][0]))

class BusinessVerifierTests(unittest.TestCase):
    def setUp(self):
        spec = importlib.util.spec_from_file_location('business_android', SCRIPT.with_name('workbench-business-android.py'))
        self.m = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(self.m)
        self.event = {'eventName':'app_start', 'activityCategory':'application', 'sessionId':'session', 'installationId':'installation', 'timestamp':1}
        self.profile = {'appId':'com.hxl.xlb.test', 'projectId':'local-mobile', 'deviceId':'installation'}
        self.journal = {'schemaVersion':1, 'installationId':'installation', 'events':[self.event]}
        self.receipt = {'schemaVersion':1, 'state':'http_success', 'attemptId':'attempt', 'httpStatus':200,
                        'appId':'com.hxl.xlb.test', 'appVersion':'6.0.1', 'buildVersion':'6001',
                        'deviceId':'installation', 'projectId':'local-mobile', 'date':'2026-10-23',
                        'events':[self.event], 'sourceSha256':self.m.hashlib.sha256(b'ciphertext').hexdigest(), 'sourceBytes':10,
                        'response':{'assetId':'asset', 'runId':'run', 'receiptId':'receipt'}}
        self.asset = {'id':'asset', 'sdkReceiptId':'receipt', 'sdkClientId':'business-android-test',
                      'origin':'sdk', 'originalEncrypted':True, 'identitySource':'sdk_reported',
                      'app':'com.hxl.xlb.test', 'device':'installation', 'platform':'android', 'environment':'test-local',
                      'version':'6.0.1', 'build':'6001', 'fileDate':'2026-10-23', 'rawHash':self.receipt['sourceSha256'], 'size':10}
        self.run = {'id':'run','assetId':'asset','status':'succeeded','mode':'strict','format':'native','errorCount':0,'eventCount':1}
        self.data = {'/parse-runs/run':self.run, '/workspace':{'assets':[self.asset]},
                     '/parse-runs/run/events?limit=1000':{'events':[{'content':json.dumps(self.event)}], 'total':1, 'nextCursor':''},
                     '/assets/asset/raw':b'ciphertext'}

    def verify(self, baseline=None):
        return self.m.verify_snapshot(self.profile, self.journal, self.receipt, lambda path, raw=False:self.data[path], baseline, source_bytes=b'ciphertext')

    def test_exact_snapshot_survives_new_lifecycle_events_and_restart(self):
        result = self.verify()
        self.journal['events'].append(dict(self.event, eventName='activity_resumed', timestamp=2))
        again = self.verify(result)
        self.assertTrue(again['checks']['baselineRetained'])
        self.assertEqual(result['sha256'], again['sha256'])
        self.assertNotIn('session', json.dumps(result))

    def test_exact_native_header_is_accounted_without_masking_business_events(self):
        page = self.data['/parse-runs/run/events?limit=1000']
        page['events'].insert(0, {'content':'clogan header', 'logType':1})
        page['total'] = self.run['eventCount'] = 2
        result = self.verify()
        self.assertEqual(result['eventCount'], 2)
        self.assertEqual(result['businessEventCount'], 1)
        self.assertEqual(result['nativeHeaderCount'], 1)
        page['events'][0]['content'] = 'clogan header with arbitrary data'
        with self.assertRaises(ValueError): self.verify()

    def test_actual_retained_app_ciphertext_must_match_send_snapshot(self):
        with self.assertRaises(ValueError):
            self.m.verify_snapshot(self.profile, self.journal, self.receipt,
                                   lambda path, raw=False:self.data[path], source_bytes=b'changed')

    def test_rejects_extra_or_modified_events_and_non_strict_or_wrong_origin(self):
        for key, value in [('mode','recovery'), ('errorCount',1)]:
            before = dict(self.run)
            self.run[key] = value
            with self.assertRaises(ValueError): self.verify()
            self.run.update(before)
        self.asset['origin'] = 'import'
        with self.assertRaises(ValueError): self.verify()
        self.asset['origin'] = 'sdk'
        self.data['/parse-runs/run/events?limit=1000']['events'][0]['content'] = '{}'
        with self.assertRaises(ValueError): self.verify()

    def test_bounded_paging_rejects_truncated_or_repeated_cursor_and_hash_mismatch(self):
        page = self.data['/parse-runs/run/events?limit=1000']
        page['nextCursor'] = 'same'
        self.data['/parse-runs/run/events?limit=1000&cursor=same'] = page
        with self.assertRaises(ValueError): self.verify()
        page['nextCursor'] = ''
        self.receipt['sourceSha256'] = '0' * 64
        with self.assertRaises(ValueError): self.verify()

if __name__ == '__main__':
    unittest.main()
