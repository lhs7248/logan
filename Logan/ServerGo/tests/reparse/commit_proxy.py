#!/usr/bin/env python3
"""Isolated plaintext MySQL fixture: withhold one successful COMMIT reply.

Only packet framing, SSLRequest capabilities, and the exact COMMIT command are
inspected. No query, authentication, or response payload is logged or saved.
"""
import argparse
import json
import os
from pathlib import Path
import socket
import threading
import time

MAX_PACKET = 0xffffff


def read_exact(sock, size, stop, deadline):
    data = bytearray()
    while len(data) < size:
        if stop.is_set() or time.monotonic() >= deadline:
            raise EOFError
        try:
            chunk = sock.recv(min(size - len(data), 65536))
        except socket.timeout:
            continue
        if not chunk:
            raise EOFError
        data.extend(chunk)
    return bytes(data)


def read_packet(sock, stop, deadline):
    header = read_exact(sock, 4, stop, deadline)
    length = int.from_bytes(header[:3], 'little')
    if length > MAX_PACKET:
        raise ValueError('packet limit exceeded')
    return header, read_exact(sock, length, stop, deadline)


class CommitGate:
    def __init__(self, state_dir):
        self.path = Path(state_dir) / 'commit-held.json'
        self.lock = threading.Lock()
        self.used = self.path.exists()

    def claim(self, header, payload, connection_id):
        with self.lock:
            if self.used:
                return False
            self.path.parent.mkdir(parents=True, exist_ok=True)
            evidence = dict(serverCommitOKObserved=True, commitOKForwarded=False,
                            sequence=header[3], payloadBytes=len(payload),
                            connectionId=connection_id)
            temporary = self.path.with_suffix('.tmp')
            fd = os.open(temporary, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
            with os.fdopen(fd, 'w') as out:
                json.dump(evidence, out)
                out.flush()
                os.fsync(out.fileno())
            os.replace(temporary, self.path)
            self.used = True
            return True


class Relay:
    def __init__(self, client, backend, gate, connection_id, deadline):
        self.client, self.backend = client, backend
        self.gate, self.connection_id = gate, connection_id
        self.deadline = deadline
        self.stop = threading.Event()
        self.pending = False
        self.lock = threading.Lock()

    def close(self):
        self.stop.set()
        for sock in (self.client, self.backend):
            try:
                sock.shutdown(socket.SHUT_RDWR)
            except OSError:
                pass
            sock.close()

    def forward(self, sock, header, payload):
        for part in (header, payload):
            view = memoryview(part)
            while view:
                if self.stop.is_set() or time.monotonic() >= self.deadline:
                    raise EOFError
                try:
                    sent = sock.send(view)
                except socket.timeout:
                    continue
                if not sent:
                    raise EOFError
                view = view[sent:]

    def client_to_backend(self):
        try:
            while not self.stop.is_set():
                header, payload = read_packet(self.client, self.stop, self.deadline)
                # A TLS handshake cannot be relayed by this packet-aware fixture.
                if header[3] == 1 and len(payload) == 32 and int.from_bytes(payload[:4], 'little') & 0x800:
                    raise ValueError('TLS is unsupported by this plaintext fixture')
                if header[3] == 0 and payload.upper() == b'\x03COMMIT':
                    with self.lock:
                        self.pending = True  # Must precede forwarding to avoid a fast reply race.
                self.forward(self.backend, header, payload)
        except (OSError, EOFError, ValueError):
            pass
        finally:
            self.close()

    def backend_to_client(self):
        try:
            while not self.stop.is_set():
                header, payload = read_packet(self.backend, self.stop, self.deadline)
                with self.lock:
                    pending, self.pending = self.pending, False
                # Protocol 4.1 OK has a marker and at least six more bytes.
                if pending and len(payload) >= 7 and payload[0] == 0 and self.gate.claim(header, payload, self.connection_id):
                    self.stop.wait(max(0, self.deadline - time.monotonic()))
                    return
                self.forward(self.client, header, payload)
        except (OSError, EOFError, ValueError):
            pass
        finally:
            self.close()

    def run(self):
        remaining = max(.001, self.deadline - time.monotonic())
        self.client.settimeout(min(.2, remaining))
        self.backend.settimeout(min(.2, remaining))
        worker = threading.Thread(target=self.client_to_backend)
        worker.start()
        self.backend_to_client()
        worker.join()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--backend-host', default=os.environ.get('BACKEND_HOST', 'mysql'))
    parser.add_argument('--backend-port', type=int, default=int(os.environ.get('BACKEND_PORT', '3306')))
    parser.add_argument('--listen-port', type=int, default=int(os.environ.get('LISTEN_PORT', '3306')))
    parser.add_argument('--state-dir', type=Path, default=Path(os.environ.get('STATE_DIR', '/state')))
    parser.add_argument('--deadline-seconds', type=float, default=float(os.environ.get('DEADLINE_SECONDS', '180')))
    args = parser.parse_args()
    if not 0 < args.deadline_seconds <= 3600:
        parser.error('deadline must be between zero and 3600 seconds')
    gate = CommitGate(args.state_dir)
    deadline = time.monotonic() + args.deadline_seconds
    workers = []
    connection_id = 0
    with socket.socket() as listener:
        listener.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
        listener.bind(('0.0.0.0', args.listen_port))
        listener.listen(16)
        listener.settimeout(.25)
        while time.monotonic() < deadline:
            try:
                client, _ = listener.accept()
            except socket.timeout:
                continue
            workers = [worker for worker in workers if worker.is_alive()]
            if len(workers) >= 32:
                client.close()
                continue
            try:
                backend = socket.create_connection((args.backend_host, args.backend_port), timeout=min(5, max(.001, deadline - time.monotonic())))
            except OSError:
                client.close()
                continue
            connection_id += 1
            relay = Relay(client, backend, gate, connection_id, deadline)
            worker = threading.Thread(target=relay.run)
            workers.append(worker)
            worker.start()
    for worker in workers:
        worker.join()


if __name__ == '__main__':
    main()
