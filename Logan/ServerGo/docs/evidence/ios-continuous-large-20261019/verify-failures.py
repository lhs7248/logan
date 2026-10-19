from pathlib import Path
import json,zlib,hashlib
out=Path('/private/tmp/phase25-ios');results=[]
for name in ['large32','large32-retry','large32-single']:
 p=out/name/'1';remaining=(p/'valid-prefix-decrypted.gz').read_bytes();parts=[];members=0
 while remaining:
  if not remaining.startswith(b'\x1f\x8b\x08'):
   n=remaining[0];gap=0
   if n==0:
    while gap<len(remaining) and remaining[gap]==0 and gap<16:gap+=1
   elif 1<=n<=16 and remaining[:n]==bytes([n])*n:gap=n
   assert gap and (len(remaining)==gap or remaining[gap:].startswith(b'\x1f\x8b\x08'))
   remaining=remaining[gap:]
   if not remaining:break
  d=zlib.decompressobj(31);parts.append(d.decompress(remaining)+d.flush());assert d.eof;remaining=d.unused_data;members+=1
 plain=b''.join(parts);rows=[json.loads(v) for v in plain.splitlines() if v.strip()];assert len(rows)==8193 and rows[0]['c']=='clogan header'
 for i,row in enumerate(rows[1:]):assert len(row['c'])==4096 and row['c'].startswith(f'P25|large32|{i:08d}|')
 r=json.loads((p/'failure-diagnosis.json').read_text());assert r['trailing_bytes']=='0100000000' and not r['legacy_padding_probe_rejections'];r.update(valid_prefix_complete_expected_rows=8193,gzip_members=members,json_plain_bytes=len(plain),wire_sha256=hashlib.sha256((p/'native-upload.bin').read_bytes()).hexdigest(),decoded_prefix_sha256=hashlib.sha256((p/'valid-prefix-decrypted.gz').read_bytes()).hexdigest(),scenario=name,chain_status='failed',note='Diagnostic prefix extraction only; original unchanged file rejected. No trimmed file was uploaded.');(p/'failure-diagnosis.json').write_text(json.dumps(r,indent=2));results.append(r)
(out/'failure-summary.json').write_text(json.dumps(results,indent=2));print('All three failed original files retain complete 8193-row valid gzip prefixes followed by a 5-byte empty frame header; chain remains FAILED.')
