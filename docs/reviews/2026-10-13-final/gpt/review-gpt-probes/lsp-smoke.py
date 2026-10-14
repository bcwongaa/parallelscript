import json
import os
from pathlib import Path
import queue
import subprocess
import threading
import time

ROOT = Path(os.environ.get('PTS_REVIEW_SCRATCH', '/tmp/pts-review-gpt'))
ENV = dict(os.environ, GOMAXPROCS='2', PTS_THREADS='4', PTS_CACHE_DIR=str(Path.home()/'.cache/pts-review-gpt'), PTS_HOME=os.environ.get('PTS_HOME', '/home/bryan/projects/pts-review-gpt'))
server = subprocess.Popen([str(ROOT/'pts'), 'lsp', '--stdio'], stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.PIPE, env=ENV)
messages=queue.Queue()
transcript=[]

def reader():
    while True:
        headers={}
        while True:
            line=server.stdout.readline()
            if not line: return
            if line==b'\r\n': break
            name,value=line.decode().split(':',1); headers[name.lower()]=value.strip()
        body=server.stdout.read(int(headers['content-length']))
        message=json.loads(body)
        transcript.append(message)
        if 'method' in message and 'id' in message:
            result = [None for _ in message.get('params', {}).get('items', [])] if message['method'] == 'workspace/configuration' else None
            send({'jsonrpc': '2.0', 'id': message['id'], 'result': result})
        else:
            messages.put(message)

threading.Thread(target=reader,daemon=True).start()

def send(message):
    payload=json.dumps(message).encode()
    server.stdin.write(f'Content-Length: {len(payload)}\r\n\r\n'.encode()+payload)
    server.stdin.flush()

def response(identifier):
    deadline=time.time()+40
    while time.time()<deadline:
        message=messages.get(timeout=deadline-time.time())
        if message.get('id')==identifier: return message
    raise TimeoutError(identifier)

try:
    root=ROOT.as_uri()
    send({'jsonrpc':'2.0','id':1,'method':'initialize','params':{'processId':os.getpid(),'rootUri':root,'workspaceFolders':[{'uri':root,'name':'review'}],'capabilities':{'textDocument':{'diagnostic':{'dynamicRegistration':False}}}}})
    initialized=response(1)
    print('initialize error:',initialized.get('error'),flush=True)
    send({'jsonrpc':'2.0','method':'initialized','params':{}})
    documents=[('type-error.pts','const n: number = "wrong";\nconsole.log(n);\n'),('numeric-error.pts','const byte: u8 = 256;\nconsole.log(byte);\n'),('guardrail-error.pts','import { range } from "pts:parallel";\nlet count = 0;\nrange(0, 10).par.forEach(() => { count++; });\n'),('destructuring-editor.pts','const { value = 4 } = { value: undefined };\nconsole.log(value);\n')]
    for identifier,(name,text) in enumerate(documents,start=2):
        path=ROOT/name; path.write_text(text)
        uri=path.as_uri()
        send({'jsonrpc':'2.0','method':'textDocument/didOpen','params':{'textDocument':{'uri':uri,'languageId':'parallelscript','version':1,'text':text}}})
        send({'jsonrpc':'2.0','id':identifier,'method':'textDocument/diagnostic','params':{'textDocument':{'uri':uri}}})
        reply=response(identifier)
        print(name,json.dumps(reply,ensure_ascii=False),flush=True)
    send({'jsonrpc':'2.0','id':9,'method':'shutdown','params':None})
    response(9)
    send({'jsonrpc':'2.0','method':'exit'})
    server.wait(timeout=10)
    print('lsp exit:',server.returncode,flush=True)
finally:
    if server.poll() is None: server.kill(); server.wait()
    (ROOT/'lsp-transcript.json').write_text(json.dumps(transcript,ensure_ascii=False,indent=2))
    stderr=server.stderr.read().decode()
    (ROOT/'lsp-stderr.txt').write_text(stderr)
    print(stderr,flush=True)
