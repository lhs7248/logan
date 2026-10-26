#!/usr/bin/env python3
"""Offline local-disk workbench snapshots; no archive extraction or secret output."""
import argparse
import fcntl
import hashlib
import json
import os
from pathlib import Path
import shutil
import sys


def checked(path, directory=False):
    path = Path(os.path.abspath(path))
    for part in [path, *path.parents]:
        if part.is_symlink():
            raise ValueError('symlinks are forbidden')
    if not (path.is_dir() if directory else path.is_file()):
        raise ValueError('required regular input is missing')
    return path


def new_output(path):
    path = Path(os.path.abspath(path))
    checked(path.parent, True)
    if path.exists() or path.is_symlink():
        raise ValueError('output must be a new directory')
    return path


def validate_payload(files):
    state = json.loads(files['data/state.json'])
    if not isinstance(state, dict) or not isinstance(state.get('objects'), dict) or not isinstance(state.get('idempotency'), dict):
        raise ValueError('invalid state')
    for bucket in state['objects'].values():
        if not isinstance(bucket, dict) or any(not isinstance(r, dict) for r in bucket.values()):
            raise ValueError('invalid state objects')
    for receipt in state['idempotency'].values():
        if not isinstance(receipt, dict) or not isinstance(receipt.get('hash'), str) or type(receipt.get('status')) is not int or 'body' not in receipt:
            raise ValueError('invalid receipt')
    auth = json.loads(files['auth.json'])
    projects = auth.get('projects', [])
    tokens = auth.get('tokens', [])
    ids = [p.get('id') for p in projects]
    if not ids or any(not isinstance(p, str) or not p.strip() for p in ids) or len(set(ids)) != len(ids) or not tokens:
        raise ValueError('invalid auth projects')
    seen = set()
    for t in tokens:
        token, actor, grants = t.get('token'), t.get('actor'), t.get('projects')
        if not isinstance(token, str) or len(token) < 16 or token in seen or not isinstance(actor, str) or not actor.strip() or not isinstance(grants, dict) or not grants:
            raise ValueError('invalid auth tokens')
        seen.add(token)
        if any(p not in ids or role not in ('viewer', 'investigator', 'reviewer', 'admin') for p, role in grants.items()):
            raise ValueError('invalid auth grants')


def write_private(path, data):
    path.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
    with path.open('xb') as f:
        os.chmod(path, 0o600)
        f.write(data)
        f.flush()
        os.fsync(f.fileno())


def materialize(output, files):
    output.mkdir(mode=0o700)
    try:
        for name, value in files.items():
            write_private(output / name, value)
        directories = sorted((p for p in output.rglob('*') if p.is_dir()), key=lambda p: len(p.parts), reverse=True)
        for directory in [*directories, output, output.parent]:
            fd = os.open(directory, os.O_RDONLY)
            try:
                os.fsync(fd)
            finally:
                os.close(fd)
    except BaseException:
        shutil.rmtree(output)
        raise


def backup(data_dir, auth_file, output, config_files):
    data = checked(data_dir, True)
    output = new_output(output)
    if output == data or data in output.parents:
        raise ValueError('backup must be outside data directory')
    lock_path = data / '.lock'
    if lock_path.is_symlink():
        raise ValueError('symlink lock forbidden')
    fd = os.open(lock_path, os.O_CREAT | os.O_RDWR | os.O_NOFOLLOW, 0o600)
    with os.fdopen(fd, 'r+') as lock:
        try:
            fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError:
            raise ValueError('data directory is locked; stop the instance first') from None
        files = {'data/state.json': checked(data / 'state.json').read_bytes(), 'auth.json': checked(auth_file).read_bytes()}
        for source in config_files:
            source = checked(source)
            name = 'config/' + source.name
            if name in files:
                raise ValueError('duplicate configuration basename')
            files[name] = source.read_bytes()
        validate_payload(files)
        manifest = {'version': 1, 'files': {name: hashlib.sha256(value).hexdigest() for name, value in files.items()}}
        files['manifest.json'] = (json.dumps(manifest, sort_keys=True) + '\n').encode()
        materialize(output, files)


def verify(backup_dir):
    root = checked(backup_dir, True)
    for entry in root.rglob('*'):
        if entry.is_symlink():
            raise ValueError('symlinks are forbidden')
    manifest = json.loads(checked(root / 'manifest.json').read_bytes())
    if manifest.get('version') != 1 or not isinstance(manifest.get('files'), dict):
        raise ValueError('unsupported backup manifest')
    files = {}
    for name, digest in manifest['files'].items():
        p = Path(name)
        if name not in ('data/state.json', 'auth.json') and not (len(p.parts) == 2 and p.parts[0] == 'config' and p.parts[1] not in ('.', '..')):
            raise ValueError('unsafe manifest path')
        if p.is_absolute() or '..' in p.parts or str(p) != name:
            raise ValueError('unsafe manifest path')
        value = checked(root / p).read_bytes()
        if hashlib.sha256(value).hexdigest() != digest:
            raise ValueError('backup checksum mismatch')
        files[name] = value
    if not {'data/state.json', 'auth.json'} <= files.keys():
        raise ValueError('incomplete backup')
    validate_payload(files)
    return files


def restore(backup_dir, destination):
    destination = new_output(destination)
    files = verify(backup_dir)
    materialize(destination, files)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest='command', required=True)
    b = commands.add_parser('backup')
    b.add_argument('--data-dir', required=True)
    b.add_argument('--auth-file', required=True)
    b.add_argument('--output', required=True)
    b.add_argument('--config-file', action='append', default=[])
    v = commands.add_parser('verify'); v.add_argument('--backup', required=True)
    r = commands.add_parser('restore'); r.add_argument('--backup', required=True); r.add_argument('--destination', required=True)
    args = parser.parse_args()
    try:
        if args.command == 'backup': backup(args.data_dir, args.auth_file, args.output, args.config_file)
        elif args.command == 'verify': verify(args.backup)
        else: restore(args.backup, args.destination)
    except (OSError, ValueError, KeyError, TypeError, AttributeError):
        print('Operation refused: invalid input, integrity failure, existing output, or locked/unavailable storage.', file=sys.stderr)
        return 1
    print('Operation completed.')
    return 0

if __name__ == '__main__':
    sys.exit(main())
