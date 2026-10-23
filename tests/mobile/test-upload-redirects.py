#!/usr/bin/env python3
"""Actual Android/Objective-C transport redirect checks, two loopback servers only."""
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
import subprocess
import tempfile
import threading

root = Path(__file__).resolve().parents[2]
sdk = root/'Example/Logan-Android/logan/src/main/java/com/dianping/logan'
sink_requests, initial_requests = [], []

class QuietHandler(BaseHTTPRequestHandler):
    def log_message(self, *args): pass
    def read_body(self):
        return self.rfile.read(int(self.headers.get('Content-Length', '0')))

class Sink(QuietHandler):
    def do_POST(self): self.receive()
    def do_GET(self): self.receive()
    def receive(self):
        sink_requests.append((self.headers.get('X-Logan-Upload-Token'), self.read_body()))
        self.send_response(200); self.send_header('Content-Length', '0'); self.end_headers()

sink = ThreadingHTTPServer(('127.0.0.1', 0), Sink)

class Redirect(QuietHandler):
    def do_POST(self):
        initial_requests.append((self.headers.get('X-Logan-Upload-Token'), self.read_body()))
        self.send_response(302)
        self.send_header('Location', f'http://127.0.0.1:{sink.server_port}/sink')
        self.send_header('Content-Length', '0'); self.end_headers()

redirect = ThreadingHTTPServer(('127.0.0.1', 0), Redirect)
threads = [threading.Thread(target=s.serve_forever, daemon=True) for s in (sink, redirect)]
for thread in threads: thread.start()
try:
    with tempfile.TemporaryDirectory(prefix='logan-redirect-') as temporary:
        work = Path(temporary)
        fixture = work/'synthetic.bin'; original = bytes([0, 1, 255, 0, 2]); fixture.write_bytes(original)
        objc = work/'ios-redirect'
        subprocess.run(['xcrun', 'clang', '-fobjc-arc', '-fblocks', '-Wno-deprecated-declarations',
            '-Wno-nullability-completeness', '-framework', 'Cocoa', '-I', str(root/'Logan/Clogan'),
            str(root/'tests/mobile/upload_redirects.m'), '-o', str(objc)], check=True)
        subprocess.run(['javac', '-d', str(work), str(root/'tests/mobile/java/android/util/Log.java'),
            str(root/'tests/mobile/java/com/dianping/logan/SendLogRunnable.java'),
            str(root/'tests/mobile/java/AndroidRedirectTest.java'), str(sdk/'SendLogDefaultRunnable.java'),
            str(sdk/'SendLogCallback.java')], check=True)
        url = f'http://127.0.0.1:{redirect.server_port}/redirect'
        failures = []
        for platform, client in [('Android', ['java', '-cp', str(work), 'AndroidRedirectTest']), ('iOS', [str(objc)])]:
            for mode, status in [('credential', '302'), ('legacy', '200')]:
                initial_requests.clear(); sink_requests.clear()
                result = subprocess.run(client+[url, str(fixture), mode, status], capture_output=True, text=True, timeout=20)
                expected_token = 'synthetic-redirect-token' if mode == 'credential' else None
                valid = (result.returncode == 0 and initial_requests == [(expected_token, original)] and
                         (not sink_requests if mode == 'credential' else len(sink_requests) == 1))
                if not valid:
                    failures.append(f'{platform} {mode}: exit={result.returncode}, initial={len(initial_requests)}, redirected={len(sink_requests)}, credential_at_sink={any(x[0] for x in sink_requests)}')
                else: print(f'{platform} {mode} redirect: passed')
        if failures: raise AssertionError('; '.join(failures))
finally:
    for server in (redirect, sink): server.shutdown(); server.server_close()
    for thread in threads: thread.join()
