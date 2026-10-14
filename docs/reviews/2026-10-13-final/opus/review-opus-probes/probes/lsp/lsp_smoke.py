import json, subprocess, sys, os, time, threading
exe, path = sys.argv[1], sys.argv[2]
p = subprocess.Popen([exe, "lsp", "--stdio"], stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
def send(msg):
    b = json.dumps(msg).encode()
    p.stdin.write(b"Content-Length: %d\r\n\r\n" % len(b) + b); p.stdin.flush()
msgs = []
def reader():
    while True:
        hdr = b""
        while not hdr.endswith(b"\r\n\r\n"):
            c = p.stdout.read(1)
            if not c: return
            hdr += c
        n = int([l for l in hdr.split(b"\r\n") if l.lower().startswith(b"content-length")][0].split(b":")[1])
        m = json.loads(p.stdout.read(n))
        if "method" in m and "id" in m:
            res = [None] * len(m["params"].get("items", [1])) if m["method"] == "workspace/configuration" else None
            send({"jsonrpc": "2.0", "id": m["id"], "result": res})
        msgs.append(m)
threading.Thread(target=reader, daemon=True).start()
uri = "file://" + os.path.abspath(path)
send({"jsonrpc":"2.0","id":1,"method":"initialize","params":{"processId":None,"rootUri":"file://"+os.path.dirname(os.path.abspath(path)),"capabilities":{"textDocument":{"publishDiagnostics":{},"hover":{"contentFormat":["plaintext"]}}}}})
t0=time.time()
while not any(m.get("id")==1 for m in msgs) and time.time()-t0<20: time.sleep(0.05)
init=[m for m in msgs if m.get("id")==1]
print("initialize:", "ok" if init and "result" in init[0] else init, "serverInfo:", init[0]["result"].get("serverInfo") if init else None)
send({"jsonrpc":"2.0","method":"initialized","params":{}})
send({"jsonrpc":"2.0","method":"textDocument/didOpen","params":{"textDocument":{"uri":uri,"languageId":"parallelscript","version":1,"text":open(path).read()}}})
t0=time.time()
def diags():
    return [m for m in msgs if m.get("method")=="textDocument/publishDiagnostics" and m["params"]["uri"]==uri]
while time.time()-t0<30:
    d=diags()
    if time.time()-t0>6: break
    time.sleep(0.1)
# pull diagnostics too (LSP 3.17)
send({"jsonrpc":"2.0","id":2,"method":"textDocument/diagnostic","params":{"textDocument":{"uri":uri}}})
send({"jsonrpc":"2.0","id":3,"method":"textDocument/hover","params":{"textDocument":{"uri":uri},"position":{"line":4,"character":7}}})
send({"jsonrpc":"2.0","id":4,"method":"textDocument/definition","params":{"textDocument":{"uri":uri},"position":{"line":0,"character":10}}})
time.sleep(4)
print("methods received:", sorted(set(m.get("method","<response %s>" % m.get("id")) for m in msgs)))
for d in diags()[-1:]:
    for x in d["params"]["diagnostics"]:
        print("push  L%d:%d %s %s" % (x["range"]["start"]["line"]+1, x["range"]["start"]["character"]+1, x.get("code"), x["message"][:110]))
for m in msgs:
    if m.get("id")==2:
        items = m.get("result",{}).get("items",[]) if m.get("result") else m
        for x in (items if isinstance(items,list) else []):
            print("pull  L%d:%d %s %s" % (x["range"]["start"]["line"]+1, x["range"]["start"]["character"]+1, x.get("code"), x["message"][:110]))
        if not isinstance(items,list): print("pull:", str(items)[:200])
    if m.get("id")==3: print("hover:", json.dumps(m.get("result"))[:200])
    if m.get("id")==4: print("definition:", json.dumps(m.get("result"))[:200])
send({"jsonrpc":"2.0","id":9,"method":"shutdown"}); send({"jsonrpc":"2.0","method":"exit"})
try: print("exit code:", p.wait(timeout=5))
except Exception: p.kill(); print("killed")
