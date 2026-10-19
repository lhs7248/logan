#!/usr/bin/env python3
"""Generate SDK-produced fixtures without writing to SDK sources. No network implicit."""
import argparse
import hashlib
import json
import os
import re
import textwrap
from pathlib import Path
import subprocess
import tempfile

HERE = Path(__file__).resolve().parent
ROOT = HERE.parents[2]

def run(args, **kwargs):
    subprocess.run([str(x) for x in args], check=True, **kwargs)

def main():
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument('--out', type=Path, required=True)
    ap.add_argument('--node-modules', type=Path, required=True, help='Directory containing isolated npm dependencies')
    ap.add_argument('--public-key', type=Path, help='Optional public key matching server configuration')
    ap.add_argument('--web-marker', default='SDK_CHAIN_WEB_20261016 中文 🌏 + percent%', help='Synthetic content passed into unchanged SDK logWithEncryption')
    ap.add_argument('--native-key', default='0123456789012345')
    ap.add_argument('--native-iv', default='0123456789012345')
    ap.add_argument('--java-reference-rsa', action='store_true', help='Use already-public RSA reference material embedded in unchanged Java source')
    args = ap.parse_args()
    if len(args.native_key.encode()) != 16 or len(args.native_iv.encode()) != 16:
        ap.error('Native AES key and IV must each be 16 bytes')
    out = args.out.resolve()
    out.mkdir(parents=True, exist_ok=True)
    if (out / 'native.bin').exists():
        raise SystemExit('Use a fresh output directory: native SDK appends to existing files')
    croot = ROOT / 'Clogan'
    mbed = ROOT / 'mbedtls'
    sources = [croot / (name + '.c') for name in ('clogan_core', 'mmap_util', 'cJSON', 'construct_data', 'zlib_util', 'json_util', 'aes_util', 'directory_util', 'base_util', 'console_util')]
    sources += [mbed / 'library' / 'aes.c', mbed / 'library' / 'aesni.c']
    with tempfile.TemporaryDirectory(prefix='logan-sdk-build-') as tmp:
        binary = Path(tmp) / 'native-producer'
        run(['cc', '-I', croot, '-I', mbed / 'include', HERE / 'driver/native-producer.c', *sources, '-lz', '-o', binary])
        run([binary, out, args.native_key, args.native_iv])
    public = args.public_key
    if public is None:
        private = out / 'test-rsa-private.pem'
        if args.java_reference_rsa:
            java = ROOT / 'Server/src/main/java/com/meituan/logan/web/parser/WebLogDecryptHelper.java'
            der = re.search(r'RSA_PRIVATE_KEY_H5 = \"([^\"]+)\"', java.read_text()).group(1)
            private.write_text('-----BEGIN RSA PRIVATE KEY-----\n' + '\n'.join(textwrap.wrap(der, 64)) + '\n-----END RSA PRIVATE KEY-----\n')
        else:
            run(['openssl', 'genrsa', '-out', private, '2048'])
        private.chmod(0o600)
        public = out / 'test-rsa-public.pem'
        run(['openssl', 'rsa', '-in', private, '-pubout', '-out', public])
    env = dict(os.environ, NODE_PATH=str(args.node_modules.resolve()))
    run(['node', HERE / 'web-producer.cjs', ROOT / 'WebSDK', out, public.resolve(), args.web_marker], env=env)
    inputs = sources + sorted(croot.glob('*.h')) + [mbed / 'include/mbedtls/aes.h', ROOT / 'WebSDK/package.json'] + sorted((ROOT / 'WebSDK' / 'src').rglob('*'))
    hashes = {str(p.relative_to(ROOT)): hashlib.sha256(p.read_bytes()).hexdigest() for p in inputs if p.is_file()}
    manifest = {
        'schema': 1,
        'native': {'producer': 'unchanged shared CLogan SDK, host C compiler', 'artifact': 'native.bin', 'marker': 'SDK_CHAIN_NATIVE_20261016 中文 🌏 + percent%', 'type': 7, 'timestamp': 1792108800123, 'key': args.native_key, 'iv': args.native_iv, 'keyClassification': 'public synthetic test material'},
        'web': {'producer': 'unchanged WebSDK public index.logWithEncryption and report APIs', 'version': json.loads((ROOT / 'WebSDK/package.json').read_text())['version'], 'artifact': 'web-upload.json', 'environment': 'Node with fake-indexeddb emulator and intercepted XMLHttpRequest; not a real browser', 'marker': args.web_marker, 'type': 7, 'publicKey': str(public.resolve())},
        'limitations': ['Android Java/JNI wrapper and Android device not exercised', 'iOS Objective-C/Swift wrapper and iOS device not exercised', 'Web browser native IndexedDB and real network upload not exercised', 'Encryption uses runtime randomness; semantic markers are stable, ciphertext is not byte deterministic'],
        'sourceSha256': hashes,
        'artifactSha256': {n: hashlib.sha256((out / n).read_bytes()).hexdigest() for n in ('native.bin', 'web-upload.json')}
    }
    (out / 'manifest.json').write_text(json.dumps(manifest, indent=2, ensure_ascii=False) + '\n')
    print(out / 'manifest.json')

if __name__ == '__main__':
    main()
