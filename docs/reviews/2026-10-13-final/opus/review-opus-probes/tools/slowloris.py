import socket, sys, time
port = int(sys.argv[1]); n = int(sys.argv[2])
socks = []
for i in range(n):
    s = socket.create_connection(("127.0.0.1", port))
    s.send(b"GET /health HTTP/1.1\r\nHost: x\r\n")  # never finish headers
    socks.append(s)
t0 = time.time()
# while they hang, a normal client
c = socket.create_connection(("127.0.0.1", port), timeout=5)
c.send(b"GET /health HTTP/1.1\r\nHost: x\r\nConnection: close\r\n\r\n")
data = c.recv(4096)
print("normal client during slowloris:", data.split(b"\r\n")[0], "in %.3fs" % (time.time() - t0))
# slow body: send one byte a second for 3s
s = socket.create_connection(("127.0.0.1", port), timeout=10)
s.send(b"POST /todos HTTP/1.1\r\nHost: x\r\nContent-Length: 15\r\n\r\n")
for ch in b'{"title":"slow"}'[:15]:
    s.send(bytes([ch])); time.sleep(0.15)
print("slow body:", s.recv(4096).split(b"\r\n")[0])
# how many slowloris sockets are still open after 12 s?
time.sleep(12)
alive = 0
for s in socks:
    s.setblocking(False)
    try:
        d = s.recv(1)
        if d == b"": pass
        else: alive += 1
    except BlockingIOError:
        alive += 1
    except Exception:
        pass
print("slowloris sockets still open after 12s:", alive, "of", n)
