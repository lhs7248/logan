"""Bounded synthetic large protocol vectors, not SDK/device output.

OpenSSL encrypts/decrypts independently of the Go server. Keys are public test
material. Per-record SHAKE data prevents a repeated-string compression shortcut.
"""
import base64
import gzip
import hashlib
import json
from pathlib import Path
import struct
import subprocess
from urllib.parse import quote, unquote, unquote_plus

KEY=b'0123456789012345'
IV=KEY
WEB_KEY=b'large-fixturekey'
WEB_IV=b'0123456789012345'
RECORD_BYTES=4096


def openssl(args,data=b''):
    return subprocess.run(['openssl',*args],input=data,stdout=subprocess.PIPE,stderr=subprocess.PIPE,check=True,timeout=60).stdout


def test_key(directory):
    private=directory/'test-rsa-private.pem'
    openssl(['genrsa','-out',str(private),'2048']);private.chmod(0o600)
    openssl(['rsa','-in',str(private),'-pubout','-out',str(directory/'test-rsa-public.pem')])
    return private


def validate_profile(native_mib,web_mib):
    if type(native_mib) is not int or type(web_mib) is not int or not 1<=native_mib<=32 or not 1<=web_mib<=8:
        raise ValueError('large profile outside bounded synthetic limits')
    return native_mib<<20,web_mib<<20


def contents(flow,size,record_bytes=RECORD_BYTES):
    if flow not in ('native','web') or not 1<=record_bytes<=65535 or size<record_bytes or size%record_bytes:
        raise ValueError('exact bounded record sizes required')
    if size>32<<20:raise ValueError('content budget exceeded')
    result=[]
    for i in range(size//record_bytes):
        prefix='LARGE_%s_%08d_'%(flow.upper(),i)
        if len(prefix)>=record_bytes:raise ValueError('record too small')
        result.append(prefix+hashlib.shake_256((flow+str(i)).encode()).hexdigest(record_bytes)[:record_bytes-len(prefix)])
    return result


def make_fixture(native_bytes,web_bytes,private_key,frame_plain_bytes=4<<20):
    native=contents('native',native_bytes);web=contents('web',web_bytes)
    lines=[(json.dumps({'c':s,'f':7,'l':1792368000000+i},separators=(',',':'))+'\n').encode() for i,s in enumerate(native)]
    plain=b''.join(lines);wire=bytearray();download=bytearray()
    per_frame=max(1,frame_plain_bytes//RECORD_BYTES)
    for start in range(0,len(lines),per_frame):
        compressed=gzip.compress(b''.join(lines[start:start+per_frame]),mtime=0)
        encrypted=openssl(['enc','-aes-128-cbc','-K',KEY.hex(),'-iv',IV.hex()],compressed)
        wire.extend(b'\x01'+struct.pack('>I',len(encrypted))+encrypted+b'\x00')
        pad=16-len(compressed)%16;download.extend(compressed+bytes([pad])*pad)
    if len(wire)>32<<20 or len(plain)>128<<20:raise ValueError('Native server budget exceeded')
    public=private_key.parent/'test-rsa-public.pem'
    wrapped=openssl(['pkeyutl','-encrypt','-pubin','-inkey',str(public),'-pkeyopt','rsa_padding_mode:pkcs1'],WEB_KEY)
    # Each envelope starts at the same public synthetic IV; this is test data,
    # not an encryption example for production. XOR against one CTR keystream.
    stream=openssl(['enc','-aes-128-ctr','-K',WEB_KEY.hex(),'-iv',WEB_IV.hex()],bytes(RECORD_BYTES*3))
    logs=[json.dumps({'t':'7','c':quote(s,safe=''),'d':str(1792368000000+i)},separators=(',',':')) for i,s in enumerate(web)]
    envelopes=[]
    for raw in logs:
        data=raw.encode();encrypted=bytes(a^b for a,b in zip(data,stream))
        if len(encrypted)!=len(data):raise ValueError('CTR fixture bound')
        envelopes.append(quote(json.dumps({'v':1,'iv':WEB_IV.decode(),'k':base64.b64encode(wrapped).decode(),'l':base64.b64encode(encrypted).decode()},separators=(',',':')),safe=''))
    body={'logPageNo':1,'webSource':'large-synthetic','environment':'fixture','logArray':','.join(envelopes),'fileDate':'2026-10-19','client':'large-fixture','customInfo':'synthetic-not-sdk'}
    if len(json.dumps(body).encode())>32<<20:raise ValueError('Web server body budget exceeded')
    b64=lambda v:base64.b64encode(v).decode()
    return {'nativeBase64':b64(wire),'nativePlainBase64':b64(plain),'nativeDownloadBase64':b64(download),'nativeContents':native,'webContents':web,'webRawLogs':logs,'webBody':body,'nativeKey':KEY.decode(),'nativeIV':IV.decode(),'profile':{'producer':'synthetic protocol vectors; not SDK output','native_content_bytes':native_bytes,'native_plain_bytes':len(plain),'native_wire_bytes':len(wire),'web_content_bytes':web_bytes,'web_wire_bytes':len(json.dumps(body).encode()),'record_bytes':RECORD_BYTES,'native_content_sha256':hashlib.sha256(''.join(native).encode()).hexdigest(),'web_content_sha256':hashlib.sha256(''.join(web).encode()).hexdigest()}}


def verify_fixture(f,private_key):
    wire=base64.b64decode(f['nativeBase64']);cursor=0;download=bytearray();plain=bytearray();frames=0
    while cursor<len(wire):
        if len(wire)-cursor<7 or wire[cursor]!=1:raise ValueError('Native frame header')
        n=struct.unpack('>I',wire[cursor+1:cursor+5])[0]
        if n<16 or n%16 or cursor+n+5>=len(wire):raise ValueError('Native frame bounds')
        encrypted=wire[cursor+5:cursor+5+n]
        if len(encrypted)!=n or wire[cursor+5+n]!=0:raise ValueError('Native frame length')
        raw=openssl(['enc','-d','-aes-128-cbc','-nopad','-K',KEY.hex(),'-iv',IV.hex()],encrypted)
        pad=raw[-1]
        if not 1<=pad<=16 or raw[-pad:]!=bytes([pad])*pad:raise ValueError('Native padding')
        download.extend(raw);plain.extend(gzip.decompress(raw[:-pad]));cursor+=n+6;frames+=1
    if bytes(download)!=base64.b64decode(f['nativeDownloadBase64']) or bytes(plain)!=base64.b64decode(f['nativePlainBase64']):raise ValueError('Native independent byte oracle mismatch')
    records=[json.loads(s)['c'] for s in plain.splitlines()]
    if records!=f['nativeContents']:raise ValueError('Native content mismatch')
    items=[json.loads(unquote(s)) for s in f['webBody']['logArray'].split(',')]
    key=openssl(['pkeyutl','-decrypt','-inkey',str(private_key),'-pkeyopt','rsa_padding_mode:pkcs1'],base64.b64decode(items[0]['k']))
    stream=openssl(['enc','-aes-128-ctr','-K',key.hex(),'-iv',items[0]['iv'].encode().hex()],bytes(RECORD_BYTES*3))
    logs=[]
    for item in items:
        if item.get('v')!=1:raise ValueError('encrypted Web v1 required')
        if item['iv']!=WEB_IV.decode() or item['k']!=items[0]['k']:raise ValueError('Web envelope oracle key mismatch')
        encrypted=base64.b64decode(item['l']);logs.append(bytes(a^b for a,b in zip(encrypted,stream)).decode())
    if logs!=f['webRawLogs'] or [unquote_plus(json.loads(s)['c']) for s in logs]!=f['webContents']:raise ValueError('Web independent oracle mismatch')
    profile=f['profile']
    actual={'native_content_bytes':sum(len(v.encode()) for v in records),'native_plain_bytes':len(plain),'native_wire_bytes':len(wire),'web_content_bytes':sum(len(v.encode()) for v in f['webContents']),'web_wire_bytes':len(json.dumps(f['webBody']).encode()),'record_bytes':RECORD_BYTES,'native_content_sha256':hashlib.sha256(''.join(records).encode()).hexdigest(),'web_content_sha256':hashlib.sha256(''.join(f['webContents']).encode()).hexdigest()}
    if any(profile.get(k)!=v for k,v in actual.items()):raise ValueError('fixture metadata mismatch')
    return {'native_records':len(records),'web_records':len(logs),'native_frames':frames,'all_bytes_and_content_verified':True}
