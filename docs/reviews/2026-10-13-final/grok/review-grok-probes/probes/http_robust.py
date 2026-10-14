#!/usr/bin/env python3
# Malformed requests, a burst of connections, a slow client, and a half-close.
import os
import socket
import subprocess
import time
from pathlib import Path

ROOT = Path("/home/bryan/projects/pts-review-grok")
PTS = ROOT / "pts-bin" / "pts"
BIN = Path("/tmp/pts-review-grok/probe-bins/http_echo")
ENV = os.environ.copy()
ENV.update({
    "PTS_CACHE_DIR": str(Path.home() / ".cache/pts-review-grok"),
    "PTS_HOME": str(ROOT),
    "PTS_THREADS": "4",
})


def recvall(sock, limit=65536):
    sock.settimeout(2)
    data = b""
    try:
        while len(data) < limit:
            chunk = sock.recv(4096)
            if not chunk:
                break
            data += chunk
            if b"\r\n\r\n" in data and (
                b"content-length: 0" in data.lower()
                or int(data.lower().split(b"content-length: ")[1].split(b"\r\n")[0]) + data.find(b"\r\n\r\n") + 4
                <= len(data)
            ):
                break
    except socket.timeout:
        pass
    return data


def main():
    if not BIN.exists():
        subprocess.check_call([str(PTS), "build", str(ROOT / "review-grok-probes/probes/http_echo.pts"), "-o", str(BIN)], env=ENV)
    proc = subprocess.Popen([str(BIN)], stdout=subprocess.PIPE, stderr=subprocess.PIPE, env=ENV, text=True)
    assert proc.stdout is not None
    line = proc.stdout.readline().strip()
    print("server", line)
    port = int(line.split()[1])
    host = "127.0.0.1"

    def once(payload, label, read=True):
        sock = socket.create_connection((host, port), timeout=3)
        sock.sendall(payload)
        data = recvall(sock) if read else b""
        sock.close()
        status = data.split(b"\r\n", 1)[0].decode(errors="replace") if data else "(no response)"
        print(f"{label}: {status} ({len(data)} bytes)")

    once(b"GET /hello HTTP/1.1\r\nHost: x\r\nConnection: close\r\n\r\n", "good")
    once(b"HELLO\r\n\r\n", "garbage")
    once(b"GET /no-version\r\n\r\n", "no version")
    once(b"GET / HTTP/1.1\r\nBad Header\r\n\r\n", "bad header")
    once(b"GET / HTTP/1.1\r\nHost: x\r\nContent-Length: 5\r\n\r\nhi", "short body")
    huge = b"GET / HTTP/1.1\r\nHost: x\r\nX-Big: " + (b"a" * 200000) + b"\r\n\r\n"
    once(huge, "huge header")
    once(b"POST /echo HTTP/1.1\r\nHost: x\r\nContent-Length: 4\r\nConnection: close\r\n\r\nping", "post")

    # many connections
    ok = 0
    for i in range(40):
        sock = socket.create_connection((host, port), timeout=3)
        sock.sendall(b"GET /n HTTP/1.1\r\nHost: x\r\nConnection: close\r\n\r\n")
        data = recvall(sock)
        sock.close()
        if data.startswith(b"HTTP/1.1 200"):
            ok += 1
    print(f"burst40: {ok}/40")

    # slow client: send headers slowly, then read
    sock = socket.create_connection((host, port), timeout=5)
    for part in (b"GET /slow HTTP/1.1\r\n", b"Host: x\r\n", b"Connection: close\r\n\r\n"):
        sock.sendall(part)
        time.sleep(0.2)
    data = recvall(sock)
    sock.close()
    print("slow request:", data.split(b"\r\n", 1)[0].decode(errors="replace"), "bytes", len(data))

    # half-close after the request (known issue D-163)
    sock = socket.create_connection((host, port), timeout=3)
    sock.sendall(b"GET /half HTTP/1.1\r\nHost: x\r\nConnection: close\r\n\r\n")
    sock.shutdown(socket.SHUT_WR)
    data = recvall(sock)
    sock.close()
    print("half-close:", data.split(b"\r\n", 1)[0].decode(errors="replace") if data else "(no response)", "bytes", len(data))

    # slow reader of a larger body
    sock = socket.create_connection((host, port), timeout=3)
    sock.sendall(b"GET /big HTTP/1.1\r\nHost: x\r\nConnection: close\r\n\r\n")
    sock.settimeout(2)
    got = 0
    try:
        while True:
            chunk = sock.recv(100)
            if not chunk:
                break
            got += len(chunk)
            time.sleep(0.01)
            if got > 5000:
                break
    except socket.timeout:
        pass
    sock.close()
    print(f"slow reader got {got} bytes then closed")

    proc.kill()
    proc.wait(timeout=3)
    err = proc.stderr.read() if proc.stderr else ""
    if err.strip():
        print("server stderr:", err[:500])


if __name__ == "__main__":
    main()
