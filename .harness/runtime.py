"""Local evidence and bounded POSIX process execution (Python standard library)."""
import hashlib
import json
import os
from pathlib import Path
import signal
import subprocess
from datetime import datetime, timezone


def now():
    return datetime.now(timezone.utc).isoformat()


def save(path, value):
    temporary = path.with_suffix('.tmp')
    temporary.write_text(json.dumps(value, ensure_ascii=False, indent=2) + '\n')
    os.chmod(temporary, 0o600)
    os.replace(temporary, path)


def git(root, *args):
    result = subprocess.run(['git', *args], cwd=root, capture_output=True, timeout=15)
    if result.returncode:
        raise RuntimeError('Cannot inspect Git state')
    return result.stdout


def identity(root):
    """Hash contents, never persist raw diffs or environment secrets."""
    try:
        commit = git(root, 'rev-parse', 'HEAD').decode().strip()
        names = set(git(root, 'diff', '--name-only', '-z', 'HEAD').split(b'\0'))
        names.update(git(root, 'ls-files', '--others', '--exclude-standard', '-z').split(b'\0'))
        digest = hashlib.sha256(git(root, 'diff', '--binary', 'HEAD'))
        files = []
        for name in sorted(names - {b''}):
            relative = os.fsdecode(name)
            path = root / relative
            item = {'path': relative}
            if path.is_symlink():
                value = os.readlink(path).encode()
                item['sha256'] = hashlib.sha256(value).hexdigest()
            elif path.is_file():
                h = hashlib.sha256()
                with path.open('rb') as source:
                    for chunk in iter(lambda: source.read(1024 * 1024), b''):
                        h.update(chunk)
                item['sha256'] = h.hexdigest()
            else:
                item['sha256'] = 'deleted'
            digest.update(json.dumps(item, sort_keys=True).encode())
            files.append(item)
        return {'commit': commit, 'dirty': bool(files), 'changes': files, 'workspace_sha256': digest.hexdigest(),
                'limitations': 'Ignored files and external dependencies excluded; not an atomic filesystem snapshot'}
    except (OSError, RuntimeError, subprocess.TimeoutExpired) as exc:
        return {'error': str(exc)}


def versions():
    result = {}
    for tool, args in {'python3': ['--version'], 'go': ['version'], 'node': ['--version'], 'make': ['--version'], 'java': ['-version'], 'mvn': ['--version']}.items():
        try:
            proc = subprocess.run([tool, *args], capture_output=True, timeout=5)
            result[tool] = (proc.stdout + proc.stderr).decode(errors='replace').splitlines()[:3]
        except (OSError, subprocess.TimeoutExpired):
            result[tool] = ['unavailable']
    return result


def stop_group(proc):
    try:
        os.killpg(proc.pid, signal.SIGTERM)
    except ProcessLookupError:
        pass
    try:
        proc.wait(timeout=1)
    except subprocess.TimeoutExpired:
        pass
    # A parent may exit before a descendant that ignores SIGTERM.
    try:
        os.killpg(proc.pid, signal.SIGKILL)
    except ProcessLookupError:
        pass
    proc.wait()


def execute(command, cwd, log, timeout=600):
    if os.name != 'posix':
        return {'status': 'blocked', 'exit_code': None, 'reason': 'Process-group cleanup requires POSIX'}
    proc = None
    try:
        with log.open('w') as stream:
            proc = subprocess.Popen(command, cwd=cwd, stdout=stream, stderr=subprocess.STDOUT, start_new_session=True)
            try:
                code = proc.wait(timeout=timeout)
                return {'status': 'passed' if code == 0 else 'failed', 'exit_code': code}
            except subprocess.TimeoutExpired:
                return {'status': 'timeout', 'exit_code': None, 'timeout_seconds': timeout}
            finally:
                stop_group(proc)
    except OSError as exc:
        log.write_text(str(exc))
        return {'status': 'blocked', 'exit_code': None, 'reason': str(exc)}
