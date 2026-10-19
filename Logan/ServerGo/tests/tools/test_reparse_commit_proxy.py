import importlib.util
import json
from pathlib import Path
import socket
import tempfile
import threading
import time
import unittest

PATH = Path(__file__).resolve().parents[1] / 'reparse' / 'commit_proxy.py'


def packet(payload, sequence=0):
    return len(payload).to_bytes(3, 'little') + bytes([sequence]) + payload


def read_exact(sock, size):
    received = bytearray()
    while len(received) < size:
        chunk = sock.recv(min(65536, size - len(received)))
        if not chunk:
            raise AssertionError('unexpected EOF before complete packet')
        received.extend(chunk)
    return bytes(received)


class CommitProxyTests(unittest.TestCase):
    def setUp(self):
        self.assertTrue(PATH.exists(), 'COMMIT acknowledgment proxy fixture is missing')
        spec = importlib.util.spec_from_file_location('commit_proxy', PATH)
        self.proxy = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(self.proxy)
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.client, relay_client = socket.socketpair()
        relay_backend, self.backend = socket.socketpair()
        self.client.settimeout(.15)
        self.backend.settimeout(1)
        self.gate = self.proxy.CommitGate(Path(self.tmp.name))
        self.session = self.proxy.Relay(relay_client, relay_backend, self.gate, 12, time.monotonic() + 3)
        self.thread = threading.Thread(target=self.session.run)
        self.thread.start()
        self.addCleanup(self.cleanup)

    def cleanup(self):
        for sock in (self.client, self.backend):
            try:
                sock.shutdown(socket.SHUT_RDWR)
            except OSError:
                pass
            sock.close()
        self.thread.join(2)
        self.assertFalse(self.thread.is_alive(), 'relay leaked after client EOF')

    def test_fragmented_packets_preserve_bytes_both_directions(self):
        request = packet(b'\x03SELECT 1')
        for byte in request:
            self.client.sendall(bytes([byte]))
        self.assertEqual(read_exact(self.backend, len(request)), request)
        response = packet(b'\x00\x00\x00\x02\x00\x00\x00', 1)
        for byte in response:
            self.backend.sendall(bytes([byte]))
        self.assertEqual(read_exact(self.client, len(response)), response)
        self.assertFalse(self.gate.path.exists())

    def test_commit_error_is_forwarded_without_barrier(self):
        request = packet(b'\x03COMMIT')
        self.client.sendall(request)
        self.assertEqual(read_exact(self.backend, len(request)), request)
        response = packet(b'\xff\x01\x00synthetic error', 1)
        self.backend.sendall(response)
        self.assertEqual(read_exact(self.client, len(response)), response)
        self.assertFalse(self.gate.path.exists())

    def test_commit_ok_is_held_until_client_eof(self):
        request = packet(b'\x03cOmMiT')
        self.client.sendall(request)
        self.assertEqual(read_exact(self.backend, len(request)), request)
        response = packet(b'\x00\x00\x00\x02\x00\x00\x00', 1)
        self.backend.sendall(response)
        until = time.monotonic() + 1
        while not self.gate.path.exists() and time.monotonic() < until:
            time.sleep(.005)
        evidence = json.loads(self.gate.path.read_text())
        self.assertEqual(evidence, dict(serverCommitOKObserved=True, commitOKForwarded=False,
                                       sequence=1, payloadBytes=7, connectionId=12))
        with self.assertRaises(socket.timeout):
            self.client.recv(100)
        self.assertTrue(self.thread.is_alive())
        self.client.shutdown(socket.SHUT_RDWR)
        self.thread.join(1)
        self.assertFalse(self.thread.is_alive())
        self.assertEqual(self.backend.recv(100), b'')

    def test_tls_request_is_rejected_without_forwarding(self):
        self.client.sendall(packet((0x800).to_bytes(4, 'little') + bytes(28), 1))
        self.assertEqual(self.backend.recv(100), b'')
        self.thread.join(1)
        self.assertFalse(self.thread.is_alive())
        self.assertFalse(self.gate.path.exists())

    def test_incomplete_packet_eof_closes_backend(self):
        self.client.sendall(packet(b'123456')[:6])
        self.client.shutdown(socket.SHUT_WR)
        self.assertEqual(self.backend.recv(100), b'')
        self.thread.join(1)
        self.assertFalse(self.thread.is_alive())

    def test_large_packet_is_relayed_with_exact_framing(self):
        wire = packet(b'\x03' + b'x' * 300000, 0)
        sender = threading.Thread(target=self.client.sendall, args=(wire,))
        sender.start()
        received = read_exact(self.backend, len(wire))
        sender.join(1)
        self.assertFalse(sender.is_alive())
        self.assertEqual(received, wire)

    def test_gate_claims_only_one_success(self):
        header = packet(bytes(7), 1)[:4]
        self.assertTrue(self.gate.claim(header, bytes(7), 12))
        self.assertFalse(self.gate.claim(header, bytes(7), 13))
        self.assertEqual(json.loads(self.gate.path.read_text())['connectionId'], 12)

    def test_only_exact_commit_is_intercepted(self):
        request = packet(b'\x03COMMIT;')
        self.client.sendall(request)
        self.assertEqual(read_exact(self.backend, len(request)), request)
        response = packet(b'\x00\x00\x00\x02\x00\x00\x00', 1)
        self.backend.sendall(response)
        self.assertEqual(read_exact(self.client, len(response)), response)
        self.assertFalse(self.gate.path.exists())

if __name__ == '__main__':
    unittest.main()
