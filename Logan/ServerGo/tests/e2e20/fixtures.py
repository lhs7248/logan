"""Load genuine unchanged SDK output and independently decrypt expected payloads."""
import base64
import gzip
import hashlib
import json
import struct
import subprocess
from pathlib import Path
import re
from urllib.parse import unquote, unquote_plus


def openssl(arguments, payload):
    return subprocess.run(['openssl', *arguments], input=payload, stdout=subprocess.PIPE,
                          stderr=subprocess.PIPE, check=True, timeout=30).stdout


def java_download_frame(encrypted, key, iv):
    """Independent OpenSSL oracle for original Java's initial-IV final-block padding probe."""
    probe = openssl(['enc', '-d', '-aes-128-cbc', '-nopad', '-K', key, '-iv', iv], encrypted[-16:])
    padding = probe[-1]
    use_padding = 1 <= padding <= 16 and probe[-padding:] == bytes([padding]) * padding
    arguments = ['enc', '-d', '-aes-128-cbc', '-K', key, '-iv', iv]
    if not use_padding:
        arguments.append('-nopad')
    return openssl(arguments, encrypted)


SDK_ROOT = Path(__file__).resolve().parents[3]


def expected_sources():
    croot = SDK_ROOT / 'Clogan'
    sources = [croot / (name + '.c') for name in ('clogan_core', 'mmap_util', 'cJSON', 'construct_data', 'zlib_util', 'json_util', 'aes_util', 'directory_util', 'base_util', 'console_util')]
    sources += [SDK_ROOT / 'mbedtls/library/aes.c', SDK_ROOT / 'mbedtls/library/aesni.c']
    sources += sorted(croot.glob('*.h')) + [SDK_ROOT / 'mbedtls/include/mbedtls/aes.h', SDK_ROOT / 'WebSDK/package.json']
    sources += [p for p in sorted((SDK_ROOT / 'WebSDK/src').rglob('*')) if p.is_file()]
    return {p.relative_to(SDK_ROOT).as_posix(): p for p in sources}


def validate_source_hashes(hashes):
    sources = expected_sources()
    if not isinstance(hashes, dict) or set(hashes) != set(sources):
        raise AssertionError('SDK provenance must contain exactly all expected original source paths')
    for name, digest in hashes.items():
        path = Path(name)
        if path.is_absolute() or '..' in path.parts or path.as_posix() != name:
            raise AssertionError('unsafe SDK provenance path')
        source = sources[name]
        if SDK_ROOT.resolve() not in source.resolve().parents:
            raise AssertionError('SDK provenance source escaped original root')
        if not isinstance(digest, str) or not re.fullmatch('[a-f0-9]{64}', digest):
            raise AssertionError('invalid SDK source digest')
        if hashlib.sha256(source.read_bytes()).hexdigest() != digest:
            raise AssertionError('SDK source provenance digest mismatch: ' + name)


def validate_metadata(manifest):
    native, web = manifest.get('native', {}), manifest.get('web', {})
    if (manifest.get('schema') != 1 or
            native.get('producer') != 'unchanged shared CLogan SDK, host C compiler' or
            native.get('artifact') != 'native.bin' or native.get('type') != 7 or
            not isinstance(native.get('marker'), str) or not native['marker'].startswith('SDK_CHAIN_NATIVE_') or
            web.get('producer') != 'unchanged WebSDK public index.logWithEncryption and report APIs' or
            web.get('artifact') != 'web-upload.json' or web.get('type') != 7 or
            web.get('version') != json.loads((SDK_ROOT / 'WebSDK/package.json').read_text())['version'] or
            web.get('environment') != 'Node with fake-indexeddb emulator and intercepted XMLHttpRequest; not a real browser' or
            not isinstance(web.get('marker'), str) or not web['marker'].startswith('SDK_CHAIN_WEB_') or
            not isinstance(manifest.get('limitations'), list) or not manifest['limitations']):
        raise AssertionError('expected actual SDK producer metadata required')
    validate_source_hashes(manifest.get('sourceSha256'))


def make_fixture(directory):
    manifest = json.loads((directory / 'manifest.json').read_text())
    validate_metadata(manifest)
    for filename in ('native.bin', 'web-upload.json'):
        expected = manifest['artifactSha256'][filename]
        if hashlib.sha256((directory / filename).read_bytes()).hexdigest() != expected:
            raise AssertionError('SDK artifact digest mismatch: ' + filename)
    native = (directory / 'native.bin').read_bytes()
    key = manifest['native']['key'].encode().hex()
    iv = manifest['native']['iv'].encode().hex()
    cursor = 0
    compressed = bytearray()
    frame_plain = bytearray()
    java_frame_plain = bytearray()
    while cursor < len(native):
        if native[cursor] != 1:
            cursor += 1
            continue
        length = struct.unpack('>I', native[cursor + 1:cursor + 5])[0]
        cursor += 5
        block = native[cursor:cursor + length]
        cursor += length
        decrypted = openssl(['enc', '-d', '-aes-128-cbc', '-nopad', '-K', key, '-iv', iv], block)
        frame_plain.extend(decrypted)
        java_frame_plain.extend(java_download_frame(block, key, iv))
        padding = decrypted[-1]
        if 0 < padding <= 16 and decrypted[-padding:] == bytes([padding]) * padding:
            decrypted = decrypted[:-padding]
        compressed.extend(decrypted)
    # CLogan uses zero padding after complete gzip streams. gzip accepts zero tails.
    plain = gzip.decompress(compressed)
    records = [json.loads(line) for line in plain.decode().splitlines() if line.strip()]
    contents = [record.get('c', record.get('C')) for record in records]
    if manifest['native']['marker'] not in contents:
        raise AssertionError('actual native SDK marker absent')
    web = json.loads((directory / 'web-upload.json').read_text())
    logs = []
    for item in web['logArray'].split(','):
        encoded = json.loads(unquote(item))
        if encoded.get('v', 0) != 1:
            raise AssertionError('genuine encrypted Web SDK v1 required')
        aes = openssl(['pkeyutl', '-decrypt', '-inkey', str(directory / 'test-rsa-private.pem'),
                       '-pkeyopt', 'rsa_padding_mode:pkcs1'], base64.b64decode(encoded['k']))
        raw = openssl(['enc', '-d', '-aes-128-ctr', '-K', aes.hex(), '-iv', encoded['iv'].encode().hex()],
                      base64.b64decode(encoded['l'])).decode()
        logs.append(raw)
    web_contents = [unquote_plus(json.loads(raw)['c']) for raw in logs]
    if manifest['web']['marker'] not in web_contents:
        raise AssertionError('actual Web SDK marker absent')
    return {'nativeBase64': base64.b64encode(native).decode(), 'nativePlainBase64': base64.b64encode(plain).decode(),
            'nativeDownloadBase64': base64.b64encode(frame_plain).decode(),
            'nativeJavaDownloadBase64': base64.b64encode(java_frame_plain).decode(),
            'nativeContents': contents, 'webContents': web_contents, 'webRawLogs': logs,
            'webBody': web, 'nativeKey': manifest['native']['key'], 'nativeIV': manifest['native']['iv'],
            'sdkEvidence': {'artifactSha256': manifest['artifactSha256'], 'sourceSha256': manifest['sourceSha256'],
                            'limitations': manifest['limitations']}}


def make_protocol_fixture():
    """Synthetic protocol vectors for route compatibility; never SDK evidence."""
    from urllib.parse import quote
    contents = ['e2e20 native first', 'e2e20 native second']
    plain = ''.join(json.dumps({'c': text, 'f': i + 1, 'l': 1792022400000 + i * 1000}, separators=(',', ':')) + '\n'
                    for i, text in enumerate(contents)).encode()
    demo = b'0123456789012345'
    compressed = gzip.compress(plain, mtime=0)
    encrypted = openssl(['enc', '-aes-128-cbc', '-K', demo.hex(), '-iv', demo.hex()], compressed)
    native = b'\x01' + struct.pack('>I', len(encrypted)) + encrypted + b'\x00'
    web_contents = ['e2e20 web first', 'e2e20 web second']
    logs = [json.dumps({'t': str(i + 1), 'c': quote(text, safe=''), 'd': str(1792022400000 + i * 1000)}, separators=(',', ':'))
            for i, text in enumerate(web_contents)]
    array = ','.join(quote(json.dumps({'v': 0, 'l': base64.b64encode(raw.encode()).decode()}, separators=(',', ':')), safe='') for raw in logs)
    return {'nativeBase64': base64.b64encode(native).decode(), 'nativePlainBase64': base64.b64encode(plain).decode(),
            'nativeDownloadBase64': base64.b64encode(openssl(['enc', '-d', '-aes-128-cbc', '-nopad', '-K', demo.hex(), '-iv', demo.hex()], encrypted)).decode(),
            'nativeJavaDownloadBase64': base64.b64encode(java_download_frame(encrypted, demo.hex(), demo.hex())).decode(),
            'nativeContents': contents, 'nativeKey': demo.decode(), 'nativeIV': demo.decode(),
            'webContents': web_contents, 'webRawLogs': logs,
            'webBody': {'logPageNo': 1, 'webSource': 'e2e20', 'environment': 'fixture', 'logArray': array,
                        'fileDate': '2026-10-15', 'client': 'e2e20', 'customInfo': 'fixture'},
            'sdkEvidence': {'status': 'not-exercised', 'producer': 'synthetic single-frame native and Web v0 protocol vectors; not SDK output'}}


def historical_objects():
    """Pre-uploaded decoded objects, matching old CBC zero/PKCS inter-member tails."""
    cases = []
    for gap in ('zero', 'pkcs'):
        contents = ['history-' + gap + '-first', 'history-' + gap + '-recovered', 'history-' + gap + '-last']
        lines = [(json.dumps({'c': text, 'f': 1, 'l': 1792022400000 + i * 1000}, separators=(',', ':')) + '\n').encode()
                 for i, text in enumerate(contents)]
        first, rest = gzip.compress(lines[0], mtime=0), gzip.compress(b''.join(lines[1:]), mtime=0)
        padding = 16 - len(first) % 16
        raw = first + (b'\x00' * padding if gap == 'zero' else bytes([padding]) * padding) + rest
        cases.append({'gap': gap, 'objectBase64': base64.b64encode(raw).decode(), 'contents': contents,
                      'objectSha256': hashlib.sha256(raw).hexdigest()})
    return cases
