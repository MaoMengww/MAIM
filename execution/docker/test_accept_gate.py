#!/usr/bin/env python3
"""Real Unix-stream proxy behavior; no Docker daemon or workload execution."""
import socket
import tempfile
import threading
import unittest
from pathlib import Path

from accept import SocketGate


class SocketGateHalfClose(unittest.TestCase):
    def test_response_survives_input_half_close(self):
        with tempfile.TemporaryDirectory() as directory:
            path = str(Path(directory) / 'upstream.sock')
            with socket.socket(socket.AF_UNIX) as listener:
                listener.bind(path)
                listener.listen(1)
                listener.settimeout(3)
                received = []
                errors = []

                def respond():
                    try:
                        connection, _ = listener.accept()
                        with connection:
                            connection.settimeout(3)
                            chunks = []
                            while chunk := connection.recv(4096):
                                chunks.append(chunk)
                            received.append(b''.join(chunks))
                            connection.sendall(b'exact-result-after-input-eof')
                    except Exception as error:
                        errors.append(error)

                server = threading.Thread(target=respond, daemon=True)
                server.start()
                gate = SocketGate(path)
                try:
                    with socket.socket(socket.AF_UNIX) as client:
                        client.settimeout(3)
                        client.connect(str(gate.path))
                        client.sendall(b'exact-input')
                        client.shutdown(socket.SHUT_WR)
                        chunks = []
                        while chunk := client.recv(4096):
                            chunks.append(chunk)
                    server.join(timeout=4)
                    self.assertFalse(server.is_alive(), 'upstream must observe input EOF')
                    self.assertEqual(errors, [])
                    self.assertEqual(received, [b'exact-input'])
                    self.assertEqual(b''.join(chunks), b'exact-result-after-input-eof')
                finally:
                    gate.close()


if __name__ == '__main__':
    unittest.main()
