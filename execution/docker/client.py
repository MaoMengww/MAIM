#!/usr/bin/env python3
"""One bounded node-local request, also suitable for an SSH forced command."""
import argparse
import json
import os
import socket
import sys

MAX_REQUEST = 100000
MAX_RESPONSE = 1048576


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--socket", default="/run/aim-executor/control.sock")
    args = parser.parse_args()
    if os.environ.get("SSH_ORIGINAL_COMMAND"):
        parser.error("remote commands are forbidden; send one execution JSON request on stdin")
    request = sys.stdin.buffer.readline(MAX_REQUEST + 1)
    if len(request) > MAX_REQUEST or not request.endswith(b"\n"):
        parser.error("one bounded newline-terminated JSON request required")
    with socket.socket(socket.AF_UNIX, socket.SOCK_STREAM) as connection:
        connection.settimeout(30)
        connection.connect(args.socket)
        connection.sendall(request)
        connection.shutdown(socket.SHUT_WR)
        with connection.makefile("rb") as response:
            value = response.readline(MAX_RESPONSE + 1)
    if len(value) > MAX_RESPONSE or not value.endswith(b"\n"):
        raise RuntimeError("execution response incomplete; query recorded identity, do not replay")
    decoded = json.loads(value)
    sys.stdout.buffer.write(value)
    return 0 if decoded.get("ok") is True else 1


if __name__ == "__main__":
    sys.exit(main())
