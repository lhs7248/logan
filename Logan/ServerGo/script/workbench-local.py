#!/usr/bin/env python3
"""Private persistent local workbench operator; Python 3.9+, macOS launchd."""
import argparse
import base64
import fcntl
import hashlib
import json
import os
from pathlib import Path
import plistlib
import re
import secrets
import shutil
import socket
import subprocess
import sys
import time
import urllib.request

APP_ID = 'test.logan.dianping.com.logan.workbench'
PROJECT = 'local-mobile'
# Legacy Android SDK byte[] -> String -> JNI UTF-8 requires portable ASCII.
NATIVE_ALPHABET = 'abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_-'
URL = 'http://127.0.0.1:8091'
API = '/logan-web/api/workbench/v1'
MODULE = Path(__file__).resolve().parents[1]
DEFAULT_ROOT = MODULE.parents[1] / '.data' / 'workbench-local'

class RuntimeError(Exception):
    pass

class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None

def private_dir(path):
    if path.is_symlink():
        raise RuntimeError('Refusing symlink directory: ' + str(path))
    path.mkdir(parents=True, exist_ok=True, mode=0o700)
    if path.stat().st_uid != os.getuid():
        raise RuntimeError('Directory is not owned by this user')
    path.chmod(0o700)

def private_json(path, value, exclusive=True):
    flags = os.O_WRONLY | os.O_CREAT | os.O_NOFOLLOW
    flags |= os.O_EXCL if exclusive else os.O_TRUNC
    try:
        fd = os.open(str(path), flags, 0o600)
    except FileExistsError:
        raise RuntimeError('File already exists; refusing to overwrite: ' + str(path))
    with os.fdopen(fd, 'w') as file:
        os.fchmod(file.fileno(), 0o600)
        json.dump(value, file, indent=2)
        file.write('\n')

def read_private(path):
    if path.is_symlink() or not path.is_file():
        raise RuntimeError('Missing regular private file: ' + str(path))
    stat = path.stat()
    if stat.st_uid != os.getuid() or stat.st_mode & 0o777 != 0o600:
        raise RuntimeError('Private file must be owned by current user with mode 0600: ' + str(path))
    return json.loads(path.read_text())

class Runtime:
    def __init__(self, root):
        self.root = Path(os.path.abspath(root))
        self.module = MODULE
        self.settings = self.root / 'settings.json'
        self.auth = self.root / 'auth.json'
        self.binary = self.root / 'bin' / 'workbench'
        self.identity = self.root / 'run.json'
        suffix = hashlib.sha256(str(self.root).encode()).hexdigest()[:16]
        self.label = 'com.logan.workbench.local.' + suffix
        self.target = 'gui/' + str(os.getuid()) + '/' + self.label
        self.plist = Path.home() / 'Library' / 'LaunchAgents' / (self.label + '.plist')

    def init(self):
        private_dir(self.root)
        for name in ('data', 'logs', 'bin'):
            private_dir(self.root / name)
        if self.settings.exists() or self.auth.exists():
            self.config()
            return
        cfg = {'version':1, 'root':str(self.root), 'projectId':PROJECT,
               'appId':APP_ID, 'actor':'local-operator-' + secrets.token_hex(8),
               'adminToken':secrets.token_urlsafe(32), 'uploadToken':secrets.token_urlsafe(32),
               'nativeKeyBase64':base64.b64encode(''.join(secrets.choice(NATIVE_ALPHABET) for _ in range(16)).encode('ascii')).decode(),
               'nativeIVBase64':base64.b64encode(''.join(secrets.choice(NATIVE_ALPHABET) for _ in range(16)).encode('ascii')).decode(),
               'deviceId':'local-android-' + secrets.token_hex(16)}
        private_json(self.settings, cfg)
        private_json(self.auth, {'projects':[{'id':PROJECT, 'name':'Local Mobile', 'demo':False, 'modelEnabled':False}],
                    'tokens':[{'token':cfg['adminToken'], 'actor':cfg['actor'], 'projects':{PROJECT:'admin'}, 'permissions':['raw', 'export']}],
                    'sdkClients':[{'id':'local-android', 'token':cfg['uploadToken'], 'projectId':PROJECT,
                                   'appId':APP_ID, 'environment':'local', 'platforms':['android']}]})

    def config(self):
        cfg = read_private(self.settings)
        auth = read_private(self.auth)
        try:
            valid = (cfg['version'] == 1 and cfg['root'] == str(self.root) and cfg['projectId'] == PROJECT
                     and cfg['appId'] == APP_ID and cfg['adminToken'] != cfg['uploadToken']
                     and len(cfg['adminToken']) >= 32 and len(cfg['uploadToken']) >= 32
                     and len(base64.b64decode(cfg['nativeKeyBase64'], validate=True)) == 16
                     and len(base64.b64decode(cfg['nativeIVBase64'], validate=True)) == 16
                     and auth['tokens'][0]['token'] == cfg['adminToken']
                     and auth['tokens'][0]['actor'] == cfg['actor']
                     and auth['tokens'][0]['projects'] == {PROJECT:'admin'}
                     and auth['tokens'][0]['permissions'] == ['raw', 'export']
                     and auth['sdkClients'][0]['token'] == cfg['uploadToken']
                     and auth['sdkClients'][0]['appId'] == APP_ID
                     and auth['sdkClients'][0]['projectId'] == PROJECT)
        except (KeyError, IndexError, ValueError, TypeError):
            valid = False
        if not valid:
            raise RuntimeError('Existing runtime configuration is incomplete or inconsistent; refusing to overwrite')
        if any(byte not in NATIVE_ALPHABET.encode('ascii')
               for field in ('nativeKeyBase64', 'nativeIVBase64')
               for byte in base64.b64decode(cfg[field], validate=True)):
            raise RuntimeError('Local Android profile native key/IV must contain 16 ASCII letters, digits, _ or - for the legacy SDK; existing keys were retained')
        return cfg

    def profile(self):
        cfg = self.config()
        return {'receiverUrl':URL + API + '/sdk/native', 'appId':APP_ID,
                'uploadToken':cfg['uploadToken'], 'nativeKeyBase64':cfg['nativeKeyBase64'],
                'nativeIVBase64':cfg['nativeIVBase64'], 'deviceId':cfg['deviceId'], 'projectId':PROJECT}

    def export_profile(self, path):
        private_json(Path(path).absolute(), self.profile())

    def environment(self):
        cfg = self.config()
        env = {key:os.environ[key] for key in ('HOME', 'TMPDIR', 'LANG', 'TZ') if key in os.environ}
        env.update({'PATH':'/usr/bin:/bin:/usr/sbin:/sbin', 'WORKBENCH_ADDR':'127.0.0.1:8091',
                    'WORKBENCH_AUTH_FILE':str(self.auth), 'WORKBENCH_DATA_DIR':str(self.root / 'data'),
                    'WORKBENCH_NATIVE_KEY_BASE64':cfg['nativeKeyBase64'],
                    'WORKBENCH_NATIVE_IV_BASE64':cfg['nativeIVBase64'], 'WORKBENCH_WEB_PROTOCOL_ENABLED':'false'})
        return env

    def build(self):
        self.init()
        if self.launch_state() is not None:
            raise RuntimeError('Stop the service before replacing its executable')
        go = shutil.which('go')
        if not go:
            raise RuntimeError('Go is required to build')
        staged = self.binary.with_name('workbench.build')
        env = dict(os.environ)
        env.setdefault('GOCACHE', '/tmp/logan-workbench-gocache-' + str(os.getuid()))
        subprocess.run([go, 'build', '-o', str(staged), './cmd/workbench'], cwd=self.module, env=env, check=True)
        staged.chmod(0o700)
        staged.replace(self.binary)
        private_json(self.root / 'build.json', {'sha256':self.binary_hash(), 'module':str(self.module),
                     'builtAt':time.strftime('%Y-%m-%dT%H:%M:%SZ', time.gmtime())}, exclusive=False)

    def binary_hash(self):
        if self.binary.is_symlink() or not self.binary.is_file():
            raise RuntimeError('Missing owned executable; run build first')
        return hashlib.sha256(self.binary.read_bytes()).hexdigest()

    def agent_spec(self):
        return {'Label':self.label, 'ProgramArguments':[str(Path(sys.executable).resolve()), str(Path(__file__).resolve()),
                '--root', str(self.root), 'run'], 'WorkingDirectory':str(self.module),
                'RunAtLoad':True, 'KeepAlive':True, 'ThrottleInterval':10, 'Umask':0o077,
                'StandardOutPath':str(self.root / 'logs' / 'service.log'),
                'StandardErrorPath':str(self.root / 'logs' / 'service.log')}

    def launchctl(self, *args, check=True):
        if sys.platform != 'darwin':
            raise RuntimeError('Persistent start/stop requires macOS user launchd')
        return subprocess.run(['/bin/launchctl', *args], capture_output=True, text=True, check=check)

    def launch_state(self):
        if sys.platform != 'darwin':
            return None
        result = self.launchctl('print', self.target, check=False)
        return result.stdout if result.returncode == 0 else None

    def check_agent(self):
        if self.plist.is_symlink() or not self.plist.is_file():
            raise RuntimeError('Cannot establish ownership of LaunchAgent')
        if self.plist.stat().st_uid != os.getuid() or plistlib.loads(self.plist.read_bytes()) != self.agent_spec():
            raise RuntimeError('LaunchAgent ownership/configuration mismatch; refusing to alter it')

    def owned_pid(self):
        state = self.launch_state()
        if state is None:
            return None
        self.check_agent()
        match = re.search(r'^\s*pid = (\d+)\s*$', state, re.M)
        if not match:
            return None
        pid = int(match.group(1))
        identity = read_private(self.identity)
        command = subprocess.run(['/bin/ps', '-p', str(pid), '-o', 'comm='], capture_output=True, text=True, check=True).stdout.strip()
        if identity.get('pid') != pid or identity.get('binary') != str(self.binary) or command != str(self.binary):
            raise RuntimeError('Running process does not match this runtime; refusing to control it')
        return pid

    def port_busy(self):
        with socket.socket() as sock:
            # Go TCP listeners also use SO_REUSEADDR: TIME_WAIT is not an active listener.
            sock.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
            try:
                sock.bind(('127.0.0.1', 8091))
                return False
            except OSError:
                return True

    def request(self, path):
        cfg = self.config()
        req = urllib.request.Request(URL + API + path, headers={'Authorization':'Bearer ' + cfg['adminToken'], 'X-Project-ID':PROJECT})
        # Never forward local credentials through inherited proxy configuration.
        with urllib.request.build_opener(urllib.request.ProxyHandler({}), NoRedirect()).open(req, timeout=2) as response:
            if response.status != 200:
                raise RuntimeError('Authenticated readiness failed')
            return json.load(response)

    def ready(self):
        session = self.request('/session').get('data', {})
        if (session.get('projectId') != PROJECT or session.get('actor') != self.config()['actor']
                or session.get('role') != 'admin' or not any(p.get('id') == PROJECT for p in session.get('projects', []))):
            raise RuntimeError('Listener is not the configured authenticated project')
        workspace = self.request('/workspace').get('data', {})
        if not all(key in workspace for key in ('assets', 'parseRuns', 'sdkUploads', 'capabilities')):
            raise RuntimeError('Configured project workspace is not ready')
        return workspace

    def install_agent(self):
        self.plist.parent.mkdir(parents=True, exist_ok=True)
        if self.plist.exists() or self.plist.is_symlink():
            self.check_agent()
            return
        fd = os.open(str(self.plist), os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600)
        with os.fdopen(fd, 'wb') as file:
            file.write(plistlib.dumps(self.agent_spec()))

    def start(self):
        self.config()
        digest = self.binary_hash()
        if self.owned_pid() is not None:
            self.ready()
            if read_private(self.identity).get('sha256') != digest:
                raise RuntimeError('Executable changed; restart required')
            return
        if self.port_busy():
            raise RuntimeError('Loopback port 8091 is occupied by an unverified listener; no process was changed')
        self.install_agent()
        if self.launch_state() is None:
            self.launchctl('enable', self.target)
            self.launchctl('bootstrap', 'gui/' + str(os.getuid()), str(self.plist))
        else:
            self.launchctl('kickstart', self.target)
        deadline = time.monotonic() + 25
        while time.monotonic() < deadline:
            try:
                if self.owned_pid() is not None:
                    self.ready()
                    return
            except (RuntimeError, OSError, ValueError, subprocess.SubprocessError):
                pass
            time.sleep(0.25)
        raise RuntimeError('Service did not become ready; inspect private logs. LaunchAgent retained for diagnosis')

    def stop(self):
        state = self.launch_state()
        if state is None:
            return
        self.check_agent()
        # Capture the verified process before unloading launchd. The job may disappear
        # before Go's graceful HTTP shutdown releases its listening socket.
        pid = None
        if re.search(r'^\s*pid = \d+\s*$', state, re.M):
            pid = self.owned_pid()
            if pid is None:
                raise RuntimeError('Cannot stop an unowned running service')
        self.launchctl('bootout', self.target)
        deadline = time.monotonic() + 12
        while True:
            unloaded = self.launch_state() is None
            exited = True
            if pid is not None:
                process = subprocess.run(['/bin/ps', '-p', str(pid), '-o', 'comm='], capture_output=True, text=True)
                # A different command means PID reuse; never signal or wait on that process.
                exited = process.returncode != 0 or process.stdout.strip() != str(self.binary)
            if unloaded and exited:
                break
            if time.monotonic() >= deadline:
                raise RuntimeError('Owned service has not finished shutting down')
            time.sleep(0.1)
        # Keep plist for next login, binary, configuration and all business data.

    def run(self):
        self.config()
        digest = self.binary_hash()
        if self.port_busy():
            raise RuntimeError('Port 8091 occupied; refusing to launch')
        os.umask(0o077)
        private_json(self.identity, {'pid':os.getpid(), 'binary':str(self.binary), 'sha256':digest}, exclusive=False)
        os.chdir(self.module)
        os.execve(str(self.binary), [str(self.binary)], self.environment())

    def status(self):
        self.config()
        pid = self.owned_pid()
        result = {'url':URL, 'projectId':PROJECT, 'root':str(self.root), 'state':'stopped',
                  'launchAgent':str(self.plist), 'label':self.label, 'source':'deferred', 'model':'deferred', 'aliyunPush':'deferred'}
        if pid is None:
            if self.port_busy():
                raise RuntimeError('Port 8091 has an unverified listener; this runtime is not ready')
            return result
        self.ready()
        identity = read_private(self.identity)
        digest = self.binary_hash()
        if identity.get('sha256') != digest:
            raise RuntimeError('Running build differs from local executable; restart required')
        result.update({'state':'ready', 'pid':pid, 'binarySHA256':digest, 'authenticatedWorkspace':True})
        return result

def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--root', type=Path, default=DEFAULT_ROOT)
    sub = parser.add_subparsers(dest='command', required=True)
    for command in ('init', 'build', 'start', 'stop', 'restart', 'status', 'run'):
        sub.add_parser(command)
    for command in ('credentials', 'app-profile'):
        p = sub.add_parser(command)
        group = p.add_mutually_exclusive_group()
        group.add_argument('--show', action='store_true', help='Explicitly print secrets for operator use; never include in evidence')
        if command == 'app-profile':
            group.add_argument('--output', type=Path, help='Create a new 0600 private profile file; never overwrite')
    args = parser.parse_args(argv)
    runtime = Runtime(args.root)
    if args.command == 'run':
        runtime.run()
        return 0
    if args.command == 'init':
        runtime.init()
        print('Private runtime initialized (existing configuration retained): ' + str(runtime.root))
        return 0
    runtime.config()
    if args.command in ('credentials', 'app-profile'):
        if getattr(args, 'output', None):
            runtime.export_profile(args.output)
            print('Private App profile created: ' + str(args.output.absolute()))
        elif args.show:
            cfg = runtime.config()
            print(json.dumps(runtime.profile() if args.command == 'app-profile' else {'url':URL, 'projectId':PROJECT, 'adminToken':cfg['adminToken']}, indent=2))
        else:
            print('Secrets hidden. Use --show explicitly' + (' or --output FILE for private App provisioning.' if args.command == 'app-profile' else '.'))
        return 0
    # Serialize operator lifecycle changes without preventing launchd from executing run.
    fd = os.open(str(runtime.root / 'operator.lock'), os.O_CREAT | os.O_RDWR | os.O_NOFOLLOW, 0o600)
    with os.fdopen(fd, 'w') as lock:
        fcntl.flock(lock, fcntl.LOCK_EX)
        if args.command == 'restart':
            runtime.stop()
            runtime.start()
        elif args.command == 'status':
            print(json.dumps(runtime.status(), indent=2))
            return 0
        else:
            getattr(runtime, args.command)()
    print(args.command + ' completed; ' + URL)
    return 0

if __name__ == '__main__':
    try:
        sys.exit(main())
    except (RuntimeError, OSError, ValueError, subprocess.SubprocessError) as exc:
        # Exception strings from subprocess requests never include credential headers/body.
        print('workbench-local: ' + str(exc), file=sys.stderr)
        sys.exit(1)
