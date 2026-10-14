import json, os, select, socket, subprocess
from pathlib import Path
ROOT=Path(os.environ.get('PTS_REVIEW_SCRATCH', '/tmp/pts-review-gpt'))
ENV=dict(os.environ,GOMAXPROCS='2',PTS_THREADS='4',PTS_CACHE_DIR=str(Path.home()/'.cache/pts-review-gpt'),PTS_HOME=os.environ.get('PTS_HOME', '/home/bryan/projects/pts-review-gpt'))
cases={
 'ambiguous-pipeline':b'POST /json HTTP/1.1\r\nHost: localhost\r\nContent-Length: 2\r\nTransfer-Encoding: chunked\r\n\r\n2\r\n{}\r\n0\r\n\r\nGET / HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n',
 'nonfinal-chunked':b'POST /json HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\nTransfer-Encoding: chunked, gzip\r\n\r\n2\r\n{}\r\n0\r\n\r\n',
 'unsupported-transfer':b'POST /json HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\nContent-Length: 2\r\nTransfer-Encoding: gzip\r\n\r\n{}',
 'signed-length':b'POST /json HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\nContent-Length: +2\r\n\r\n{}',
}
results={}
for kind,cmd in [('pts',[str(ROOT/'http-wire')]),('node',[str(Path.home()/'.local/node/bin/node'),str(ROOT/'node-http-wire.mjs')])]:
 server=subprocess.Popen(cmd,env=ENV,stdout=subprocess.PIPE,stderr=subprocess.PIPE,text=True)
 try:
  if not select.select([server.stdout],[],[],5)[0]: raise RuntimeError('no port')
  port=int(server.stdout.readline()); results[kind]={}
  for label,wire in cases.items():
   with socket.create_connection(('127.0.0.1',port),timeout=2) as client:
    client.settimeout(1);client.sendall(wire);data=b'';closed=False
    try:
     while True:
      block=client.recv(8192)
      if not block: closed=True;break
      data+=block
    except socket.timeout: pass
   results[kind][label]={'wire':wire.decode('latin1'),'response':data.decode('latin1'),'closed':closed}
   print(kind,label,data.split(b'\r\n')[0].decode(), 'responses',data.count(b'HTTP/1.1'),'closed',closed,flush=True)
 finally:
  server.terminate();server.communicate(timeout=5)
(ROOT/'http-framing-compare.json').write_text(json.dumps(results,indent=2))
