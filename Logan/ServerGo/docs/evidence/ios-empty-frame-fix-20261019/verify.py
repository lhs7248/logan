import sys,json,zlib,subprocess,hashlib
from pathlib import Path
out=Path('/private/tmp/phase26');verified=[];failed=[]
for p in sorted(out.glob('*/*/measurement.json')):
 d=p.parent;m=json.loads(p.read_text());
 if m['status']!='measured':
  failed.append({'evidence_case':p.parent.parent.name,'measurement':m});print('FAILED_SAMPLE_RETAINED',str(p),m.get('errors'),flush=True);continue
 r=m['client'];case=r['scenario']
 meta=json.loads(subprocess.check_output(['node',str(out/'decode.js'),str(d/'native-upload.bin'),str(d/'independent-decrypted.gz')]))
 compressed=(d/'independent-decrypted.gz').read_bytes();assert compressed==(d/'http-download.gz').read_bytes()
 rest=compressed;parts=[];members=0
 while rest:
  if not rest.startswith(b'\x1f\x8b\x08'):
   n=rest[0];gap=0
   if n==0:
    while gap<len(rest) and rest[gap]==0 and gap<16:gap+=1
   elif 1<=n<=16 and rest[:n]==bytes([n])*n:gap=n
   assert gap and (len(rest)==gap or rest[gap:].startswith(b'\x1f\x8b\x08')),'invalid gzip boundary'
   rest=rest[gap:]
   if not rest:break
  dec=zlib.decompressobj(31);parts.append(dec.decompress(rest)+dec.flush());assert dec.eof
  rest=dec.unused_data;members+=1
 plain=b''.join(parts)
 records=[json.loads(line) for line in plain.splitlines() if line.strip()];actual=json.loads((d/'api-rows.json').read_text())
 assert len(records)==len(actual)==r['expected_rows']+1 and records[0]['c']=='clogan header'
 for i,(original,api) in enumerate(zip(records,actual)):
  assert (original['c'],original['f'],original['l'])==(api['content'],api['logType'],api['logTime'])
  assert api['taskId']==m['task_id']
  if i:
   assert original['c'].startswith(f'P26|{case}|{i-1:08d}|') and len(original['c'].encode())==r['content_bytes_per_row']
 detail=[{'id':v['id'],'type':v['logType'],'time':v['logTime'],'bytes':len(v['content'].encode()),'sha256':hashlib.sha256(v['content'].encode()).hexdigest()} for v in actual]
 (d/'expected-sql.json').write_text(json.dumps(detail))
 result=dict(evidence_case=p.parent.parent.name,task_id=m['task_id'],scenario=case,batch=r['batch'],records=len(records),expected_payload_rows=r['expected_rows'],content_bytes=r['expected_rows']*r['content_bytes_per_row'],json_plain_bytes=len(plain),aes_frames=meta['frames'],gzip_members=members,wire_bytes=meta['wire_bytes'],download_bytes=len(compressed),wire_sha256=hashlib.sha256((d/'native-upload.bin').read_bytes()).hexdigest(),download_sha256=hashlib.sha256(compressed).hexdigest(),all_content_type_time_match=True,ordered_sequence_complete=True,http_download_equals_independent_decryption=True,measurement=m)
 (d/'verification.json').write_text(json.dumps(result,indent=2));verified.append(result)
 print(case,r['batch'],'PASS',len(records),'frames',meta['frames'],'wire',meta['wire_bytes'],flush=True)
(out/'verification-summary.json').write_text(json.dumps(verified,indent=2))
assert len(verified)+len(failed)==10,(len(verified),len(failed))
(out/'chain-result.json').write_text(json.dumps(dict(status='failed' if failed else 'passed',accepted_verified=len(verified),failed_uploads=failed),indent=2))
sys.exit(1 if failed else 0)
