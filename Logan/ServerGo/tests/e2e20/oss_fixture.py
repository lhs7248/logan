"""Local OSS-shaped byte transport, deliberately NOT a cryptographic signature verifier."""
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import hashlib
import threading
import urllib.parse

OBJECTS = {}
LOCK = threading.Lock()


class Handler(BaseHTTPRequestHandler):
    def log_message(self, *args):
        pass

    def key(self):
        path = urllib.parse.unquote(urllib.parse.urlsplit(self.path).path).lstrip('/')
        return path.removeprefix('fixture/')

    def answer(self, status, body=b''):
        self.send_response(status)
        self.send_header('Content-Length', str(len(body)))
        self.send_header('ETag', '"' + hashlib.md5(body).hexdigest() + '"')
        self.send_header('x-oss-request-id', 'local-fixture')
        self.end_headers()
        if self.command != 'HEAD':
            self.wfile.write(body)

    def do_PUT(self):
        length = int(self.headers.get('Content-Length', '0'))
        body = self.rfile.read(length)
        with LOCK:
            OBJECTS[self.key()] = body
        print('OSS_FIXTURE_PUT bytes=' + str(len(body)), flush=True)
        self.answer(200)

    def do_GET(self):
        with LOCK:
            body = OBJECTS.get(self.key())
        if body is None:
            self.answer(404, b'<Error><Code>NoSuchKey</Code></Error>')
        else:
            print('OSS_FIXTURE_GET bytes=' + str(len(body)), flush=True)
            self.answer(200, body)

    do_HEAD = do_GET

    def do_DELETE(self):
        with LOCK:
            OBJECTS.pop(self.key(), None)
        self.answer(204)


if __name__ == '__main__':
    print('OSS_FIXTURE_READY signature_verification=false', flush=True)
    ThreadingHTTPServer(('0.0.0.0', 8080), Handler).serve_forever()
