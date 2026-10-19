"""Isolation-only HTTP gateway / Java SIMULATOR. Never a Java compatibility oracle."""
import argparse
import http.client
import json
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path


def mapped(path):
    if path == '/logan' or path.startswith('/logan/') or path.startswith('/logan?'):
        return '/logan-web/logan' + path[len('/logan'):]
    return path


class Handler(BaseHTTPRequestHandler):
    def do_GET(self):
        if self.server.role == 'java-simulator':
            self.reply(200, json.dumps({'backend': 'JAVA_SIMULATOR_NOT_ORIGINAL_JAVA', 'uri': self.path}).encode())
            return
        state = json.loads(Path(self.server.state).read_text())
        # Rehearsal explicitly permits ONLY the diagnostic ping while Go readiness fails.
        if state['backend'] == 'closed' or (state['backend'] == 'go' and self.path.split('?')[0] != '/ping'):
            self.reply(503, b'ENTRY_CLOSED: production readiness not satisfied')
            return
        upstream = state[state['backend']]
        conn = http.client.HTTPConnection(upstream, timeout=3)
        try:
            conn.request('GET', mapped(self.path), headers={'Host': self.headers.get('Host', 'rehearsal')})
            response = conn.getresponse()
            self.reply(response.status, response.read(), state['backend'])
        except OSError:
            self.reply(502, b'isolated upstream unavailable')
        finally:
            conn.close()

    def do_POST(self):
        self.reply(503, b'REHEARSAL: all writes prohibited')

    def reply(self, status, body, backend='closed'):
        self.send_response(status)
        self.send_header('Content-Type', 'application/json')
        self.send_header('X-Rehearsal-Backend', backend)
        self.send_header('Content-Length', str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def log_message(self, *_):
        pass


if __name__ == '__main__':
    parser = argparse.ArgumentParser()
    parser.add_argument('--role', choices=['gateway', 'java-simulator'], required=True)
    parser.add_argument('--port', type=int, default=8080)
    parser.add_argument('--state', default='/state/state.json')
    args = parser.parse_args()
    server = ThreadingHTTPServer(('0.0.0.0', args.port), Handler)
    server.role, server.state = args.role, args.state
    server.serve_forever()
