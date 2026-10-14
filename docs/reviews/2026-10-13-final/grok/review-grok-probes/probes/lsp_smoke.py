#!/usr/bin/env python3
# LSP smoke: initialize, open a .pts file with a type error and PTS2002, read diagnostics.
import json
import os
import select
import subprocess
import threading
from pathlib import Path

ROOT = Path("/home/bryan/projects/pts-review-grok")
PTS = ROOT / "pts-bin" / "pts"
SRC = Path("/tmp/pts-review-grok/lsp_sample.pts")
SRC.write_text(
    "const small: u8 = 200;\n"
    "const big: u8 = 300;\n"
    "let total = 0;\n"
    "parallel for (const n of [1, 2, 3]) {\n"
    "  total += n;\n"
    "}\n"
    "console.log(small, big, total);\n"
)

env = os.environ.copy()
env.update({
    "PTS_CACHE_DIR": str(Path.home() / ".cache/pts-review-grok"),
    "PTS_HOME": str(ROOT),
})
proc = subprocess.Popen(
    [str(PTS), "lsp", "--stdio"],
    stdin=subprocess.PIPE,
    stdout=subprocess.PIPE,
    stderr=subprocess.PIPE,
    env=env,
)
assert proc.stdin and proc.stdout and proc.stderr
out_fd = proc.stdout.fileno()
stderr_chunks: list[bytes] = []


def drain_stderr():
    while True:
        chunk = proc.stderr.read(4096)
        if not chunk:
            return
        stderr_chunks.append(chunk)


threading.Thread(target=drain_stderr, daemon=True).start()


def send(msg):
    body = json.dumps(msg).encode()
    proc.stdin.write(f"Content-Length: {len(body)}\r\n\r\n".encode() + body)
    proc.stdin.flush()


def read_exact(n, timeout=20):
    # os.read, not a buffered file: a BufferedReader tries to fill 8 KiB and blocks.
    buf = b""
    while len(buf) < n:
        ready, _, _ = select.select([out_fd], [], [], timeout)
        if not ready:
            raise TimeoutError(f"no lsp bytes within {timeout}s")
        chunk = os.read(out_fd, n - len(buf))
        if not chunk:
            raise EOFError("lsp stdout closed")
        buf += chunk
    return buf


def read_msg():
    headers = b""
    while b"\r\n\r\n" not in headers:
        headers += read_exact(1)
    length = int(headers.decode().split("Content-Length: ")[1].split("\r\n")[0])
    return json.loads(read_exact(length))


send({
    "jsonrpc": "2.0",
    "id": 1,
    "method": "initialize",
    "params": {
        "processId": os.getpid(),
        "rootUri": ROOT.as_uri(),
        "capabilities": {},
        "workspaceFolders": [{"uri": ROOT.as_uri(), "name": "pts"}],
    },
})
pending = []


def answer(msg):
    method = msg.get("method")
    if method == "workspace/configuration":
        send({"jsonrpc": "2.0", "id": msg["id"], "result": [None]})
        return True
    if method in ("client/registerCapability", "client/unregisterCapability"):
        send({"jsonrpc": "2.0", "id": msg["id"], "result": None})
        return True
    if method == "textDocument/publishDiagnostics":
        pending.extend(msg["params"].get("diagnostics", []))
        return True
    if method == "window/logMessage":
        return True
    if "id" in msg and method:
        send({"jsonrpc": "2.0", "id": msg["id"], "result": None})
        return True
    return False


def until_id(want):
    for _ in range(40):
        msg = read_msg()
        if msg.get("id") == want and "method" not in msg:
            return msg
        if not answer(msg):
            print("skip", json.dumps(msg)[:200])
    raise TimeoutError(f"no response {want}")


init = until_id(1)
result = init.get("result") or {}
caps = result.get("capabilities") or {}
print("init_keys", sorted(result.keys()))
print("diagnosticProvider", caps.get("diagnosticProvider"))
send({"jsonrpc": "2.0", "method": "initialized", "params": {}})
send({
    "jsonrpc": "2.0",
    "method": "textDocument/didOpen",
    "params": {
        "textDocument": {
            "uri": SRC.as_uri(),
            "languageId": "parallelscript",
            "version": 1,
            "text": SRC.read_text(),
        }
    },
})
send({
    "jsonrpc": "2.0",
    "id": 2,
    "method": "textDocument/diagnostic",
    "params": {"textDocument": {"uri": SRC.as_uri()}},
})
report = until_id(2)
items = []
if isinstance(report.get("result"), dict):
    items = report["result"].get("items", [])
print(f"pull {len(items)} push {len(pending)}")
for diag in items:
    code = diag.get("code")
    message = diag.get("message", "")
    if isinstance(message, dict):
        message = message.get("value", str(message))
    start = diag["range"]["start"]
    print(f"  {start['line']}:{start['character']} {json.dumps(code)} {str(message)[:160]}")
if report.get("error"):
    print("error", json.dumps(report["error"])[:400])
err = b"".join(stderr_chunks)
if err.strip():
    print("stderr", err[:400].decode(errors="replace"))
proc.kill()
