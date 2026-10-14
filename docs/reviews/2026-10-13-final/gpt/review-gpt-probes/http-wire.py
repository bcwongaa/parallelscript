import concurrent.futures
import json
import os
from pathlib import Path
import select
import socket
import subprocess
import time

ROOT=Path(os.environ.get('PTS_REVIEW_SCRATCH', '/tmp/pts-review-gpt'))
ENV=dict(os.environ,GOMAXPROCS='2',PTS_THREADS='4',PTS_CACHE_DIR=str(Path.home()/'.cache/pts-review-gpt'),PTS_HOME=os.environ.get('PTS_HOME', '/home/bryan/projects/pts-review-gpt'))
build=subprocess.run([str(ROOT/'pts'),'build',str(ROOT/'18-http-wire.pts'),'-o',str(ROOT/'http-wire')],env=ENV,capture_output=True,text=True,timeout=240)
print('build',build.returncode,build.stderr,flush=True)
if build.returncode: raise SystemExit(build.returncode)
server=subprocess.Popen([str(ROOT/'http-wire')],env=ENV,stdout=subprocess.PIPE,stderr=subprocess.PIPE,text=True)
if not select.select([server.stdout],[],[],10)[0]: server.kill(); raise RuntimeError('no server port')
port=int(server.stdout.readline())
results=[]

def request(label,wire,delay=0):
    with socket.create_connection(('127.0.0.1',port),timeout=2) as client:
        client.settimeout(1)
        client.sendall(wire)
        if delay: time.sleep(delay)
        received=b''
        try:
            while True:
                chunk=client.recv(65536)
                if not chunk: break
                received+=chunk
        except socket.timeout: pass
    result={'case':label,'sent':wire.decode('latin1'),'received':received.decode('latin1')}
    return result

try:
    cases=[
      ('healthy',b'GET / HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n'),
      ('head',b'HEAD /head HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n'),
      ('no-content',b'GET /no-content HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n'),
      ('nul-header',b'GET / HTTP/1.1\r\nHost: localhost\r\nX: a\x00b\r\n\r\n'),
      ('duplicate-length',b'POST /json HTTP/1.1\r\nHost: localhost\r\nContent-Length: 2\r\nContent-Length: 3\r\n\r\n{}'),
      ('length-and-chunks',b'POST /json HTTP/1.1\r\nHost: localhost\r\nContent-Length: 2\r\nTransfer-Encoding: chunked\r\n\r\n2\r\n{}\r\n0\r\n\r\n'),
      ('bad-chunk',b'POST /json HTTP/1.1\r\nHost: localhost\r\nTransfer-Encoding: chunked\r\n\r\nG\r\nabc\r\n0\r\n\r\n'),
      ('oversized-body',b'POST /json HTTP/1.1\r\nHost: localhost\r\nContent-Length: 999999999\r\n\r\n'),
      ('chunked',b'POST /json HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\nTransfer-Encoding: chunked\r\n\r\n7\r\n{"x":1}\r\n0\r\n\r\n'),
      ('ambiguous-pipeline',b'POST /json HTTP/1.1\r\nHost: localhost\r\nContent-Length: 2\r\nTransfer-Encoding: chunked\r\n\r\n2\r\n{}\r\n0\r\n\r\nGET / HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n'),
      ('nonfinal-chunked',b'POST /json HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\nTransfer-Encoding: chunked, gzip\r\n\r\n2\r\n{}\r\n0\r\n\r\n'),
      ('unsupported-transfer',b'POST /json HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\nContent-Length: 2\r\nTransfer-Encoding: gzip\r\n\r\n{}'),
      ('signed-length',b'POST /json HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\nContent-Length: +2\r\n\r\n{}'),
    ]
    for label,wire in cases:
        result=request(label,wire); results.append(result)
        print(label,repr(result['received'][:500]),flush=True)
    slow=[]
    for _ in range(8):
        client=socket.create_connection(('127.0.0.1',port),timeout=2); client.sendall(b'GET / HTTP/1.1\r\nHost:'); slow.append(client)
    healthy=request('healthy-with-eight-slow-clients',cases[0][1]);results.append(healthy)
    print(healthy['case'],repr(healthy['received']),flush=True)
    for client in slow: client.close()
    with concurrent.futures.ThreadPoolExecutor(max_workers=4) as executor:
        replies=list(executor.map(lambda i:request(f'connection-{i}',cases[0][1]),range(64)))
    ok=sum(r['received'].startswith('HTTP/1.1 200') for r in replies)
    print('64 connections: 200=',ok,flush=True)
    results.append({'case':'64-connections','success':ok})
finally:
    if server.poll() is None: server.terminate()
    try: stdout,stderr=server.communicate(timeout=5)
    except subprocess.TimeoutExpired: server.kill();stdout,stderr=server.communicate()
    print('server stopped',server.returncode,stderr,flush=True)
    results.append({'case':'server-exit','exit':server.returncode,'stdout':stdout,'stderr':stderr})
    (ROOT/'http-wire-results.json').write_text(json.dumps(results,indent=2))
