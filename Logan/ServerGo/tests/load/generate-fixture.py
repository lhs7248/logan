#!/usr/bin/env python3
"""Copy the existing synthetic Java native vector; no user logs or credentials."""
import argparse
import base64
import json
from pathlib import Path
p = argparse.ArgumentParser()
p.add_argument('--output-dir', required=True)
p.add_argument('--task-id', required=True, type=int)
p.add_argument('--detail-id', required=True, type=int)
a = p.parse_args()
if not (0 < a.task_id <= 2147483647 and 0 < a.detail_id <= 2147483647):
    p.error('IDs must refer to preseeded synthetic fixture rows and fit int32')
root = Path(__file__).resolve().parents[2]
vectors = json.loads((root / 'testdata/protocol/java-native-vectors.json').read_text())
wire = next(c['wire'] for c in vectors['cases'] if c['name'] == 'zero-padded')
out = Path(a.output_dir).resolve()
out.mkdir(parents=True, exist_ok=True)
body = out / 'synthetic-native.bin'
body.write_bytes(base64.b64decode(wire))
body.chmod(0o600)
fixture = {'synthetic': True, 'TaskID': a.task_id, 'DetailID': a.detail_id,
           'UploadFile': str(body), 'Headers': {'appId': 'logan-load-test',
           'deviceId': 'synthetic-load-test', 'platform': '1', 'fileDate': '2020-01-01'}}
f = out / 'fixture.json'
f.write_text(json.dumps(fixture, indent=2) + '\n')
f.chmod(0o600)
print(f)
