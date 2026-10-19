#!/usr/bin/env python3
"""Independent fixture sanity check; server integration remains a separate requirement."""
import argparse
import base64
import gzip
import json
from pathlib import Path
import subprocess
from urllib.parse import unquote


def openssl(args, data):
    return subprocess.run(['openssl', *args], input=data, check=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE).stdout


def main():
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument('out', type=Path)
    ap.add_argument('--private-key', type=Path)
    args = ap.parse_args()
    manifest = json.loads((args.out / 'manifest.json').read_text())
    native = manifest['native']
    wire = (args.out / 'native.bin').read_bytes()
    pos, chunks = 0, []
    while pos < len(wire):
        assert wire[pos] == 1, ('native frame start', pos)
        length = int.from_bytes(wire[pos + 1:pos + 5], 'big')
        cipher = wire[pos + 5:pos + 5 + length]
        assert len(cipher) == length and wire[pos + 5 + length] == 0
        plain = openssl(['enc', '-d', '-aes-128-cbc', '-nopad', '-K', native['key'].encode().hex(), '-iv', native['iv'].encode().hex()], cipher)
        pad = plain[-1]
        if 0 < pad <= 16 and plain[-pad:] == bytes([pad]) * pad:
            plain = plain[:-pad]
        chunks.append(plain)
        pos += 6 + length
    # CLogan may emit a separate gzip member for its internal header.
    decoded = gzip.decompress(b''.join(chunks)).decode()
    (args.out / 'native-plain.bin').write_bytes(decoded.encode())
    rows = [json.loads(line) for line in decoded.splitlines() if line.strip()]
    matching = [r for r in rows if r.get('c') == native['marker']]
    assert len(matching) == 1 and matching[0]['f'] == 7 and matching[0]['l'] == native['timestamp'], rows
    web = json.loads((args.out / 'web-upload.json').read_text())
    private = args.private_key or args.out / 'test-rsa-private.pem'
    rows_web = []
    for entry in web['logArray'].split(','):
        ob = json.loads(unquote(entry))
        assert ob['v'] == 1
        key = openssl(['pkeyutl', '-decrypt', '-inkey', str(private), '-pkeyopt', 'rsa_padding_mode:pkcs1'], base64.b64decode(ob['k']))
        plaintext = openssl(['enc', '-d', '-aes-128-ctr', '-K', key.hex(), '-iv', ob['iv'].encode().hex()], base64.b64decode(ob['l']))
        log = json.loads(plaintext)
        assert unquote(log['c']) == manifest['web']['marker'] and log['t'] == 7, log
        rows_web.append(plaintext.decode())
    assert len(rows_web) == 1
    (args.out / 'web-plain.json').write_text(json.dumps(rows_web, ensure_ascii=False) + '\n')
    print(json.dumps({'nativeSdkMarkerRecovered': True, 'nativeRows': len(rows), 'webSdkMarkerRecovered': True, 'webRows': len(rows_web)}))

if __name__ == '__main__':
    main()
