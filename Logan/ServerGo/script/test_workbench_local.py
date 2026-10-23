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

if __name__ == '__main__':
    unittest.main()
