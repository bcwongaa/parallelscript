# H33b reproducer report (verbatim)

Child: `reproducer` (claude-opus-5-5, xhigh), isolated worktree `.claude/worktrees/agent-ae78911cb3d97b8d8` from `8fe8a7b1e`, 2026-10-26. Assignment: the RED side of H33b (HTTPS for `serve`): design and write failing regression tests against the contract the parent is implementing, and probe the edges with independent clients. Probes: `h33b-reproducer-probes.tar.gz` (from `/tmp/h33b/reproducer/`). The child's final message follows, unedited except that HTML entities for `<` and `>` are written as the characters.

---

# H33b reproducer report: failing tests for HTTPS in `serve`

All five new tests fail on the base compiler, because the programs don't compile there. I checked the tests themselves against stand-in servers so they don't pass or fail for the wrong reason. I didn't commit, didn't edit runtime, library or compiler code, and didn't touch the plan, progress or decision docs or `tools/agent/`.

## Files written
Worktree: `/home/bryan/projects/parallelscript/.claude/worktrees/agent-ae78911cb3d97b8d8`
- **New:**
  - `pts/tests/https_server_test.go`
  - `pts/tests/http/server_tls.pts`, `every_loop_tls.pts`, `lifecycle_tls.pts`, `fetch_self_tls.pts`, `serve_tls_options.pts`
  - `pts/tests/errors/serve_tls_options.pts`
- **Modified:** `pts/tests/README.md` (one paragraph added).
- **Don't commit:** the six `pts/third_party/*` entries in that worktree are symlinks to the main checkout. They show as `T` in `git status`.

## What each test pins
- **`TestHTTPSServer`** (`server_tls.pts` runs five servers, one per form of the option: `certFile`+`keyFile` with a chain through an intermediate and a PKCS#8 P-256 key; `cert`+`key` as text with PKCS#1 RSA; `certFile`+`key` with SEC1 P-384; `cert`+`keyFile` with encrypted PKCS#8; legacy encrypted PEM RSA). Go's client trusts only the throwaway root. It checks:
  - **Routes and URLs:** status, `date`, HEAD, `request.url` = `https://<Host><target>` (or the server's address without a Host header), `server.url`, `server.protocol`.
  - **Keys and versions:** every server under forced TLS 1.2 and forced TLS 1.3, with the key type checked. TLS 1.1 gets "protocol version". No session is resumed, under 1.2 or 1.3. No client certificate is requested.
  - **Keep-alive:** `httptrace` shows `Reused` as false, true, true, true.
  - **ALPN:** `h2,http/1.1` and `http/1.1` negotiate `http/1.1`; none offered gives `""`; `h2` alone gets "no application protocol". Go's client with `ForceAttemptHTTP2` ends up on HTTP/1.1.
  - **SNI:** `localhost`, `anything.example` or no SNI all get the one certificate, still verified for localhost.
  - **Raw protocol inside TLS**, under 1.2 and 1.3: pipelining in order, HTTP/1.0 with and without keep-alive, chunked bodies, a request sent one byte per record, 100-continue followed by the body, and the exact 400 / 400 (bare CR) / 413 / 431 texts. Each ends with close_notify and then the TCP close.
  - **Large bodies:** 5 MB posted and echoed, 6 MB answered.
  - **Plain HTTP and garbage at the TLS port:** no HTTP response, at most an alert, then a close.
  - **Stalled handshakes:** 24 clients sit or leave mid-handshake and 8 reject the certificate, while 32 good requests succeed. Two clients still mid-handshake when `stop()` comes must not keep the program alive.
  - **Node as a second client:** `fetch`, `https.get` (keep-alive reuse, TLS 1.2) and `tls.connect` (ALPN), with `NODE_EXTRA_CA_CERTS`. It honours `PTS_REQUIRE_NODE`.
  - **At the end:** stderr is empty.
- **`TestHTTPSServerEveryLoop`** (`PTS_THREADS` 1 and 4, `reusePort`): 16×50 fresh TLS connections all succeed; two handlers run at once on 4 loops; keep-alive clients, a timer and a task work; `stop()` reaches every loop; stderr is empty.
- **`TestHTTPSServerLifecycle`** (1, 4, and 4 under ASan+UBSan):
  - A client that leaves a held request, by closing, by sending **close_notify alone** or by a reset, aborts its signal and its descriptor closes. That also holds when the handler ignores the signal.
  - 5,000 pipelined requests are answered in order.
  - 64 unread 1 MB responses grow the server by less than 24 MB.
  - 1,008 fresh TLS connections after a warm-up leave descriptors unchanged and grow VmRSS by under 12 MB and VmData by under 16 MB.
  - `stop()`: the stopper's response and the idle connection end with close_notify; new connections are refused.
  - `stop(true)` from a pool thread closes the active connection.
- **`TestHTTPSServerFetchesItself`** (1, 4, and sanitized): fetches from the main loop, `.par.map` and `parallel`, https→https redirects to another origin (authorization dropped) and the same origin (307 POST kept), a 1 MB POST, and a handler fetching its own server. Fetching by 127.0.0.1, which the certificate (localhost only) doesn't name, gives `ERR_TLS_CERT_ALTNAME_INVALID`; a self-signed server gives `DEPTH_ZERO_SELF_SIGNED_CERT`.
- **`TestHTTPSServerOptions`:**
  - The four TypeError texts.
  - ENOENT for `certFile` and `keyFile`, also with `reusePort`. EISDIR for a directory. Both compared field by field with what `readFileSync` throws.
  - `serve's tls cert: … (mbedTLS -0x…)` for non-PEM, a key given as the cert, and an Ed25519 certificate.
  - `serve's tls key: …` for non-PEM, a cert given as the key, and a wrong or missing passphrase for both encrypted forms.
  - "doesn't match the certificate" for another key, an RSA certificate with a P-256 key, and a chain with the intermediate first.
  - The forms that should serve do, including `tls: undefined` (plain HTTP) and 0.0.0.0 (url says localhost).
  - Afterwards descriptors are unchanged and the port is free.
- **`TestErrors/serve_tls_options`:** an unknown option `ca` gives TS2353 at 8:52, and assigning `server.protocol` gives TS2540 at 11:8.

## RED evidence
I ran your command from `/tmp/h33b/reproducer/red.sh`; the log is `red.log`. HEAD is `8fe8a7b1e`.
- `-run 'TestHTTPSServer'`: all five tests fail in 0.1 s. There are 23 tsc errors: `'tls' does not exist in type 'ServeOptions'`, `Property 'protocol' does not exist on type 'Server'`, and `no exported member 'TLSOptions'`. Exit code 1. Nothing is skipped.
- `TestErrors/serve_tls_options` fails, because the base reports `'tls'` at 8:3, not `'ca'`.

## How I checked the tests (logs in the archive)
- **Every program minus the TLS bits compiles on the base**, so a failure after your change will be about TLS, not unsupported syntax. The plain run of `serve_tls_options` printed "files as before: true" and "the port is free: true". `fetch_self_tls` over plain HTTP matches every expected line except the two TLS-only ones.
- **A small server built on the same vendored mbedTLS 3.6.7 and the same config** (`standin/mbedtls_standin.c`) confirmed:
  - All five key formats parse, and wrong or missing passphrases fail with -0x3B80 / -0x3C00. OpenSSL agrees on the encrypted ones.
  - Ed25519 certificates fail (-0x3C80); mismatched keys fail the pair check.
  - `h2` alone gets "no application protocol"; TLS 1.1 gets "protocol version not supported".
  - No resumption without a session cache; RSA, P-384 and P-256 handshake under both versions.
  - `openssl s_client -msg` sees close_notify, and a fatal alert for `h2` alone.
- **Go TLS negative control:** the close_notify check gives true with close_notify and false without, under both versions.
- **Pts's own plain server behind a Go TLS proxy:**
  - The whole lifecycle flow passes. The one failure is "connection refused", because the proxy keeps listening.
  - The raw-protocol checks pass (`TestH33bRawThroughProxy`).
  - The full `TestHTTPSServer` check also ran through the proxy. Its failures are proxy or base artifacts: the proxy's port and certificate, Go's session resumption and ALPN, and the base's http:// URLs.
  - The refusal texts and `oversizedHead` also check out against the plain server.
- **Bun 1.4.3 running the program adapted:** keys, keep-alive, SNI, bodies, garbage, stalled handshakes and the Node client logic all pass. Its failures are its own behaviour, and they show the checks catch real differences: it lowercases `utf-8` in the content type, resumes TLS 1.2 sessions, does no ALPN, keeps HTTP/1.0 connections open, and doesn't exit after `stop()` while handshakes are stalled.

## Problems in my first draft that the checks caught (all fixed)
- **close_notify detection:** mbedTLS pads TLS 1.3 records to 16 bytes, so its alert record is 32 bytes, not 19. I replaced the size guess with "Go's client stopped at a record before reading the TCP end, and the TCP end follows". Under TLS 1.2 the test also checks the record type is 21.
- **Garbage input:** a handshake record that claims a long message makes mbedTLS wait for the rest, so I use a malformed 4-byte ClientHello instead.
- **Two smaller fixes:** `/url?q=1` had been routed to 404, and `tls.Client` needed a `ServerName`.

## Contract points to decide (I pinned these; change them if you disagree)
1. A client that sends close_notify alone, keeping TCP open, counts as gone. This mirrors D-163's rule for a half-close.
2. `tls: {}` gives "needs cert or certFile", so the cert check comes first.
3. TLS option and file errors come before bind errors: a missing file on a busy port throws ENOENT, not EADDRINUSE. That's my reading of "before it listens".
4. A property set to `undefined` counts as absent. A passphrase given for an unencrypted key is ignored, as Node and Bun do.
5. Connections still mid-handshake count as idle for `stop()`. Bun differs here.
6. **Not pinned, underspecified:** a chain file where a later certificate is broken. `mbedtls_x509_crt_parse` returns a positive count, not an error, so a server could silently drop a bad intermediate. I'd suggest throwing `serve's tls cert: …`. Also not pinned: whether `cert: ""` counts as missing or as unparsable.

## Existing defects I found (recorded, not fixed)
- **IPv6 URLs:** `serve({ hostname: "::1" }).url` is `http://::1:PORT/` without brackets; Bun gives `http://[::1]:PORT/`. A request without a Host header gets the same unbracketed form. The https URL will inherit this. Probe: `probes/ipv6_url.pts`.
- **Not a defect:** each loop keeps one `/dev/null` descriptor after its first listen (libuv reserves it for running out of descriptors). The options test warms every loop up first so descriptor counts stay comparable.

## Left out on purpose
- **Handshake timeout:** that's H14.
- **Some TLS edge cases:** TLS 1.3 KeyUpdate and renegotiation, client certificates, and SNI-based certificate selection.
- **Leak bound:** it uses VmData, not an exact count. A kept session would add at least 33 MB per thousand connections; the plain server grows by 0 kB here after its warm-up.
- **Every-loop under sanitizers:** not run; the lifecycle and fetch-itself tests are sanitized.

## Archive and cleanup
- **Archive:** `/tmp/h33b/reproducer-probes.tar.gz`. It holds the RED log, every probe and its log, the stand-in, the Bun, OpenSSL and Node probes, the base-compatible copies, and copies of the new files. `files/zz_h33b_probe_test.go` is the probe test, build tag `h33bprobe`, already taken out of the worktree.
- **Cleanup:** build outputs (including the worktree's `pts-bin/pts`) are deleted, and no server or probe processes are left running.
- `/tmp/h33b/reproducer.done` is touched.
