import socket, struct, sys, threading
port = int(sys.argv[1])
def rst_after(payload):
    s = socket.create_connection(("127.0.0.1", port))
    s.setsockopt(socket.SOL_SOCKET, socket.SO_LINGER, struct.pack("ii", 1, 0))
    s.send(payload)
    s.close()  # RST
reqs = [b"GET /slow HTTP/1.1\r\nHost: x\r\n\r\n", b"POST /todos HTTP/1.1\r\nHost: x\r\nContent-Length: 1000\r\n\r\n{\"ti",
        b"GET /todos HTTP/1.1\r\nHost: x\r\n\r\n", b"GET /health HTTP/1.1\r\n"]
def worker():
    for i in range(500):
        rst_after(reqs[i % len(reqs)])
ts = [threading.Thread(target=worker) for _ in range(8)]
[t.start() for t in ts]; [t.join() for t in ts]
print("sent 4000 reset connections")
