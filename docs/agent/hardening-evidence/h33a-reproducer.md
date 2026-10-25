# H33a reproducer report (verbatim)

Child: `reproducer` (claude-opus-5-5, xhigh), isolated worktree `.claude/worktrees/agent-a125c731616982e9a` from `37a9ce909`, 2026-10-25. Assignment: the RED side of H33a (the HTTPS client): reproduce the gap and design the failing regression tests against the contract the parent is implementing, with Node as the oracle. Probes: `h33a-reproducer-probes.tar.gz` (from `/tmp/h33/reproducer/`). The child's final message follows, unedited except that HTML entities for `<` and `>` are written as the characters.

---

I've written the RED-side tests for H33a. Against the current compiler, all five new tests fail with the expected "https: is not supported yet" cause. Node 24 reproduced the recorded oracle in 6 runs, all identical. Nothing is committed or pushed, and I didn't touch the plan, PROGRESS or decision docs.

The most important finding isn't TLS-specific. **On a cross-origin redirect, pts keeps the `Authorization` and `Cookie` headers, while Node drops them.** It already happens today over plain HTTP (`credentials, http to http on another port`). Once TLS lands, an https→http redirect would send credentials in cleartext. `fetch()` in `pts/lib/http.pts` never deletes `authorization`, `proxy-authorization` or `cookie` when the origin changes (undici does; the Fetch standard requires it for Authorization). The test pins Node's behaviour, so H33a has to fix this.

## Files (worktree `/home/bryan/projects/parallelscript/.claude/worktrees/agent-a125c731616982e9a`)
- `pts/tests/https_test.go` (new, gofmt- and vet-clean):
  - **The lab**: it makes throwaway CAs and leaves with `crypto/x509` at test time and starts about 45 named Go servers. Base URLs go to the programs as `name=url` arguments.
  - **Request guard**: servers whose certificate must fail record any request that reaches them. A client that checks the hostname after writing the request fails the test.
  - **Raw servers**: a handshake-closing, a handshake-holding and an "answers like plain HTTP" server confirm they received a ClientHello, so the loose protocol-failure lines can't pass vacuously.
  - **Optional servers**: `[::1]`, `127.0.0.2` (absent on macOS) and the `*.localhost` wildcard names (checked with `getent`, because Go's resolver answers `*.localhost` itself) get dropped from both the arguments and the expected text when unavailable.
  - **Tests**: `TestFetchHTTPSMatchesNode`, `TestFetchHTTPSEveryLoop`, `TestFetchHTTPSUnderSanitizers`, `TestFetchHTTPSTrustStore`, `TestFetchHTTPSPublicSite`.
  - **Recorded oracle**: `MatchesNode` checks both Node and pts against the recorded `fetchTLSOutput`, so a missing Node can't make it vacuous; `PTS_REQUIRE_NODE` is honoured.
- `pts/tests/http/fetch_tls.pts` (new): the differential scenario program.
- `pts/tests/http/fetch_tls_parallel.pts` (new, pts only):
  - an abort during the handshake that must close the connection;
  - 32 good-plus-untrusted fetch pairs on one loop, from `.par.map` and from `parallel`;
  - 64 random-stage aborts;
  - 1000 more fetches, then checks that file descriptors are unchanged and RSS grew less than 40 MB.
  - I checked its logic under Node with `parallel`/`.par` removed: every check prints "ok".
- `pts/tests/http/fetch_tls_one.pts` (new): one fetch, used by the trust-store and public-site tests.
- `pts/tests/README.md`: one added paragraph in the `http/*.pts` entry.
- Not to commit: the worktree's `pts/third_party/*` symlinks (show as `T` in git status). The temporary probe package is removed, with a copy in the tarball.

## RED evidence
Command (pinned to 2 CPUs, so `heavyJobs` allows only 2 builds):
`GOMAXPROCS=2 PTS_REQUIRE_NODE=1 taskset -c 0,1 go test -count=1 -p 1 -timeout 40m -v -run TestFetchHTTPS ./pts/tests/` → **exit 1**
```
--- FAIL: TestFetchHTTPSTrustStore (1.91s)
--- FAIL: TestFetchHTTPSPublicSite (0.67s)
--- FAIL: TestFetchHTTPSEveryLoop (14.56s)
--- FAIL: TestFetchHTTPSMatchesNode (28.52s)
--- FAIL: TestFetchHTTPSUnderSanitizers (0.00s)
    --- FAIL: TestFetchHTTPSUnderSanitizers/fetch_tls (20.61s)
    --- FAIL: TestFetchHTTPSUnderSanitizers/fetch_tls_parallel (20.20s)
FAIL	github.com/microsoft/typescript-go/pts/tests	48.736s
```
- **MatchesNode**: the Node assertion (`https_test.go:760`) passed. pts failed at `:765`; its first lines are:
  ```
  -get TypeError: fetch failed | undefined | https: is not supported yet: fetch speaks plain HTTP
  -by IPv4 address TypeError: fetch failed | undefined | https: is not supported yet: fetch speaks plain HTTP
  ```
  59 of 66 lines differ: 52 "not supported yet" lines plus the credential leak, the server-state line and both aborts:
  ```
  credentials, http to http on another port 200 redirected <plain-other>/echo GET tls=false length=0 type="" authorization="Bearer secret" cookie="a=1" x-kept="yes" body=""
  their servers read: none, none, none, none
  abort during the handshake held: false, TypeError
  abort during the body stalled: false, TypeError, the connection closed: false
  ```
  The 7 lines that match today:
  - the four loose protocol-failure lines, guarded by "their servers read", which fails;
  - "https server at http" and its blank continuation line, a plain-HTTP path that genuinely works;
  - "requests that reached … none", vacuous until TLS connects.
- **EveryLoop and the sanitized `fetch_tls_parallel`**: exit 1, `Uncaught TypeError: fetch failed at fetchAll`, with every outcome `TypeError: fetch failed | undefined`.
- **TrustStore**: all 9 cases print `TypeError: fetch failed | undefined | https: is not supported yet: fetch speaks plain HTTP`.
- **PublicSite**: Node gets `200` from example.com; pts gets the same error in all three environments.

## Node's oracle (v24.21.0, undici 7.29.1, OpenSSL 3.5.8), verbatim
The line after `list:` in "wrong host, by IP address" and "IP address as a DNS name" ends in a space.
```
get 200 <localhost>/hello GET sni="localhost" alpn="http/1.1" TLS 1.3, offered alpn=["http/1.1"] versions=["TLS 1.3" "TLS 1.2"]
by IPv4 address 200 <127.0.0.1>/hello GET sni="" alpn="http/1.1" TLS 1.3, offered alpn=["http/1.1"] versions=["TLS 1.3" "TLS 1.2"]
by IPv6 address 200 <[::1]>/hello GET sni="" alpn="http/1.1" TLS 1.3, offered alpn=["http/1.1"] versions=["TLS 1.3" "TLS 1.2"]
TLS 1.2 only 200 <tls12>/hello GET sni="localhost" alpn="http/1.1" TLS 1.2, offered alpn=["http/1.1"] versions=["TLS 1.3" "TLS 1.2"]
TLS 1.3 only 200 <tls13>/hello GET sni="localhost" alpn="http/1.1" TLS 1.3, offered alpn=["http/1.1"] versions=["TLS 1.3" "TLS 1.2"]
RSA 200 <rsa>/hello GET sni="localhost" alpn="http/1.1" TLS 1.3, offered alpn=["http/1.1"] versions=["TLS 1.3" "TLS 1.2"]
RSA, TLS 1.2 200 <rsa-tls12>/hello GET sni="localhost" alpn="http/1.1" TLS 1.2, offered alpn=["http/1.1"] versions=["TLS 1.3" "TLS 1.2"]
P-384 CA 200 <p384>/hello GET sni="localhost" alpn="http/1.1" TLS 1.3, offered alpn=["http/1.1"] versions=["TLS 1.3" "TLS 1.2"]
hello retry 200 <hello-retry>/hello GET sni="localhost" alpn="http/1.1" TLS 1.3, offered alpn=["http/1.1"] versions=["TLS 1.3" "TLS 1.2"]
client certificate asked for 200 <asks-for-client-cert>/hello GET sni="localhost" alpn="http/1.1" TLS 1.3, offered alpn=["http/1.1"] versions=["TLS 1.3" "TLS 1.2"]
post 200 <localhost>/echo POST tls=true length=8 type="text/x-test" authorization="" cookie="" x-kept="" body="body ✓"
chunked 200 <localhost>/chunked one two three
20 MB 200 20971520 end
until close 200 <localhost>/close read until the end
until close without close_notify 200 <localhost>/close-abruptly read until the end
truncated TypeError | other side closed
truncated without close_notify TypeError | other side closed
reset during the body TypeError | read ECONNRESET
http to https 200 redirected <localhost>/hello GET sni="localhost" alpn="http/1.1" TLS 1.3, offered alpn=["http/1.1"] versions=["TLS 1.3" "TLS 1.2"]
https to http 200 redirected <plain>/hello GET plain
https to https 200 redirected <intermediate-sent>/hello GET sni="localhost" alpn="http/1.1" TLS 1.3, offered alpn=["http/1.1"] versions=["TLS 1.3" "TLS 1.2"]
307 post, http to https 200 redirected <localhost>/echo POST tls=true length=4 type="text/plain;charset=UTF-8" authorization="" cookie="" x-kept="" body="kept"
303 post, https to http 200 redirected <plain>/echo GET tls=false length=0 type="" authorization="" cookie="" x-kept="" body=""
credentials, https to https on the same origin 200 redirected <localhost>/echo GET tls=true length=0 type="" authorization="Bearer secret" cookie="a=1" x-kept="yes" body=""
credentials, https to http 200 redirected <plain>/echo GET tls=false length=0 type="" authorization="" cookie="" x-kept="yes" body=""
credentials, http to http on another port 200 redirected <plain-other>/echo GET tls=false length=0 type="" authorization="" cookie="" x-kept="yes" body=""
https to an untrusted server TypeError: fetch failed | UNABLE_TO_VERIFY_LEAF_SIGNATURE | unable to verify the first certificate; if the root CA is installed locally, try running Node.js with --use-system-ca
untrusted CA TypeError: fetch failed | UNABLE_TO_VERIFY_LEAF_SIGNATURE | unable to verify the first certificate; if the root CA is installed locally, try running Node.js with --use-system-ca
untrusted CA, root sent TypeError: fetch failed | SELF_SIGNED_CERT_IN_CHAIN | self-signed certificate in certificate chain
untrusted CA, intermediate sent TypeError: fetch failed | UNABLE_TO_GET_ISSUER_CERT_LOCALLY | unable to get local issuer certificate
self-signed TypeError: fetch failed | DEPTH_ZERO_SELF_SIGNED_CERT | self-signed certificate; if the root CA is installed locally, try running Node.js with --use-system-ca
expired TypeError: fetch failed | CERT_HAS_EXPIRED | certificate has expired
not yet valid TypeError: fetch failed | CERT_NOT_YET_VALID | certificate is not yet valid
wrong host TypeError: fetch failed | ERR_TLS_CERT_ALTNAME_INVALID | Hostname/IP does not match certificate's altnames: Host: localhost. is not in the cert's altnames: DNS:other.example
wrong host, by IP address TypeError: fetch failed | ERR_TLS_CERT_ALTNAME_INVALID | Hostname/IP does not match certificate's altnames: IP: 127.0.0.1 is not in the cert's list: 
untrusted CA and wrong host TypeError: fetch failed | UNABLE_TO_VERIFY_LEAF_SIGNATURE | unable to verify the first certificate; if the root CA is installed locally, try running Node.js with --use-system-ca
expired and wrong host TypeError: fetch failed | CERT_HAS_EXPIRED | certificate has expired
expired and untrusted CA TypeError: fetch failed | CERT_HAS_EXPIRED | certificate has expired
expired and self-signed TypeError: fetch failed | CERT_HAS_EXPIRED | certificate has expired
intermediate expired TypeError: fetch failed | CERT_HAS_EXPIRED | certificate has expired
intermediate not sent TypeError: fetch failed | UNABLE_TO_VERIFY_LEAF_SIGNATURE | unable to verify the first certificate; if the root CA is installed locally, try running Node.js with --use-system-ca
intermediate sent 200 <intermediate-sent>/hello GET sni="localhost" alpn="http/1.1" TLS 1.3, offered alpn=["http/1.1"] versions=["TLS 1.3" "TLS 1.2"]
issuer not a CA TypeError: fetch failed | INVALID_PURPOSE | unsuitable certificate purpose
for clients only TypeError: fetch failed | INVALID_PURPOSE | unsuitable certificate purpose
common name only 200 <common-name-only>/hello GET sni="localhost" alpn="http/1.1" TLS 1.3, offered alpn=["http/1.1"] versions=["TLS 1.3" "TLS 1.2"]
common name only, wrong TypeError: fetch failed | ERR_TLS_CERT_ALTNAME_INVALID | Hostname/IP does not match certificate's altnames: Host: localhost. is not cert's CN: other.example
no names TypeError: fetch failed | ERR_TLS_CERT_ALTNAME_INVALID | Hostname/IP does not match certificate's altnames: Cert does not contain a DNS name
IP addresses and a common name 200 <ips-and-common-name>/hello GET sni="localhost" alpn="http/1.1" TLS 1.3, offered alpn=["http/1.1"] versions=["TLS 1.3" "TLS 1.2"]
IP address as a DNS name TypeError: fetch failed | ERR_TLS_CERT_ALTNAME_INVALID | Hostname/IP does not match certificate's altnames: IP: 127.0.0.1 is not in the cert's list: 
by another IP address TypeError: fetch failed | ERR_TLS_CERT_ALTNAME_INVALID | Hostname/IP does not match certificate's altnames: IP: 127.0.0.2 is not in the cert's list: 127.0.0.1, ::1
only a wildcard, at localhost TypeError: fetch failed | ERR_TLS_CERT_ALTNAME_INVALID | Hostname/IP does not match certificate's altnames: Host: localhost. is not in the cert's altnames: DNS:*.localhost
wildcard 200 <wildcard>/hello GET sni="a.pts.localhost" alpn="http/1.1" TLS 1.3, offered alpn=["http/1.1"] versions=["TLS 1.3" "TLS 1.2"]
wildcard, two labels deep TypeError: fetch failed | ERR_TLS_CERT_ALTNAME_INVALID | Hostname/IP does not match certificate's altnames: Host: a.b.pts.localhost. is not in the cert's altnames: DNS:*.pts.localhost
wildcard, the bare domain TypeError: fetch failed | ERR_TLS_CERT_ALTNAME_INVALID | Hostname/IP does not match certificate's altnames: Host: pts.localhost. is not in the cert's altnames: DNS:*.pts.localhost
wildcard of a top-level domain TypeError: fetch failed | ERR_TLS_CERT_ALTNAME_INVALID | Hostname/IP does not match certificate's altnames: Host: a.localhost. is not in the cert's altnames: DNS:*.localhost
wildcard, partial 200 <wildcard-partial>/hello GET sni="ab.pts.localhost" alpn="http/1.1" TLS 1.3, offered alpn=["http/1.1"] versions=["TLS 1.3" "TLS 1.2"]
requests that reached servers whose certificates don't verify: none
plain HTTP server at https TypeError: fetch failed | the cause is an Error: true
https server at http 400 <localhost-as-http>/hello Client sent an HTTP request to an HTTPS server.

closed during the handshake TypeError: fetch failed | the cause is an Error: true
TLS 1.0 only TypeError: fetch failed | the cause is an Error: true
client certificate required TypeError: fetch failed | the cause is an Error: true
their servers read: hello, hello, hello, hello
abort during the handshake held: true, AbortError
abort during the body stalled: true, AbortError, the connection closed: true
```
Node's full causes for the loosely checked protocol cases:
- plain HTTP server at https: `ERR_SSL_WRONG_VERSION_NUMBER` "…:SSL routines:tls_validate_record_header:wrong version number:…"
- closed during the handshake: `ECONNRESET` "Client network socket disconnected before secure TLS connection was established". This one is Node's own text, so the implementation could match it exactly.
- TLS 1.0 only: `ERR_SSL_TLSV1_ALERT_PROTOCOL_VERSION`.
- client certificate required: undici `SocketError` `UND_ERR_SOCKET` "other side closed". The TLS 1.3 alert only arrives after the request has been written.

Node trust-store behaviour (with the trusted CA only in `SSL_CERT_FILE` or `SSL_CERT_DIR`):

| Setting | Result |
|---|---|
| Default | `SSL_CERT_FILE` and `SSL_CERT_DIR` ignored: `UNABLE_TO_VERIFY_LEAF_SIGNATURE` |
| `--use-openssl-ca` | honours `SSL_CERT_FILE`; `SSL_CERT_DIR` needs OpenSSL hash names, so a plain `test-cas.pem` fails |
| `--use-system-ca` or `NODE_USE_SYSTEM_CA=1` | honours both and reads plain file names, but does **not** split `SSL_CERT_DIR` on `:` (stderr: "Cannot open directory a:b to load OpenSSL certificates.") |
| `NODE_EXTRA_CA_CERTS` pointing at a missing file | stderr `Warning: Ignoring extra certs from \`…\`, load failed: error:80000002:system library::No such file or directory`; continues |

`TestFetchHTTPSTrustStore` follows the contract's OpenSSL/Go semantics (colon-separated dirs, every file read), not Node's.

## Node behaviour that matters for the implementation
- **Handshake**:
  - SNI is sent for DNS names only; none for 127.0.0.1 or `[::1]`.
  - ALPN offers exactly `["http/1.1"]`.
  - Versions offered are TLS 1.3 and 1.2 (1.2 minimum); a TLS-1.0-only server fails.
  - Hello-retry and client-certificate-requested servers work.
- **Which error wins**: Node reports the *last* chain error OpenSSL finds. Expired beats untrusted and beats self-signed. The hostname is checked only when the chain verifies (untrusted plus wrong host gives the untrusted code). A non-CA issuer and a clientAuth-only EKU both give `INVALID_PURPOSE`.
- **Node 24 message hint**: Node 24 appends "; if the root CA is installed locally, try running Node.js with --use-system-ca" to `UNABLE_TO_VERIFY_LEAF_SIGNATURE` and `DEPTH_ZERO_SELF_SIGNED_CERT` only. CI uses Node 24, so the recorded text includes it. If you'd rather not emit it, strip it in the program and change `nodeCAHint`.
- **Hostname rules** (Node's `checkServerIdentity`; several differ from mbedTLS's built-in check):
  - An IP literal matches IP SANs only: a DNS SAN or CN spelling "127.0.0.1" is rejected.
  - The CN is used whenever there's no DNS SAN, even if IP SANs exist.
  - Partial wildcards (`a*.x.y`) are accepted; `*.tld` is rejected; a wildcard covers one label only.
- **Bodies**:
  - EOF without close_notify is accepted as the end of a read-until-close body.
  - A truncated body gives "other side closed" with or without close_notify; a reset gives "read ECONNRESET".
  - Node rejects `text()` with "terminated" where pts rejects `fetch` (D-160), so those lines compare only the error name and the cause message.
- **Aborts**: on abort during the handshake, undici keeps the socket open (pool semantics). That check is therefore pts-only, in `fetch_tls_parallel`. During the body, Node closes the connection. Undici also keeps idle keep-alive sockets: file descriptors went from 24 to 74 in the Node run of the parallel logic.

## Risks for the implementation
- **Hostname checking**: don't rely on mbedTLS's CN check; implement Node's rules yourself, and before the request is written (the "requests that reached" line catches a late check).
  - mbedTLS 3.6.3 and later fail the handshake if `mbedtls_ssl_set_hostname` was never called while verification is required. For IP literals, call it with NULL (no SNI, which matches Node) and verify the IP yourself. Worth confirming against 3.6.7's docs.
- **Error mapping**: mbedTLS's single `BADCERT_NOT_TRUSTED` flag has to become four Node codes depending on the presented chain (depth-0 self-signed, self-signed root in the chain, leaf only, intermediate sent). A non-CA issuer also shows up as `NOT_TRUSTED` in mbedTLS but must report `INVALID_PURPOSE`.
- **Thread safety**:
  - Call `psa_crypto_init` once (`uv_once`) before any loop handshakes; TLS 1.3 in mbedTLS 3.6 needs PSA.
  - Build with `MBEDTLS_THREADING_C` and `MBEDTLS_THREADING_PTHREAD` for PSA's global state and any shared random-number generator.
  - Load the shared `ssl_config` and CA chain once and treat them as read-only afterwards.
- **Trust-store cost**: load it once, lazily, never per fetch. `/etc/ssl/certs` here has 241 symlinks duplicating the 120-certificate bundle, so a Go-style directory search parses each root about 3 times. A certificate that fails to parse must not fail the whole load.
- **Memory and aborts**:
  - Each handshake holds about two 16 KB record buffers plus its state; keep that in malloc, not the collector's heap.
  - `fetch.c` has one `uv_write_t` per client today; TLS will have several writes in flight, and their buffers must live until the write callbacks (`UV_ECANCELED` on abort).
  - The sanitizer runs use `detect_leaks=0`, so only the RSS check (1000 requests) catches leaks, and only above about 40 kB per connection.
- **Read loop**: mbedTLS's TLS 1.3 client may return `MBEDTLS_ERR_SSL_RECEIVED_NEW_SESSION_TICKET` from a read (Go servers send tickets); treat it as a retry. Map `MBEDTLS_ERR_SSL_CONN_EOF` and the peer-close-notify code to end of stream as above.
- **Build options** the tests exercise: RSA-PSS (TLS 1.3 with RSA certificates), P-384/SHA-384 and hello-retry.
- **Existing tests that change when TLS lands**:
  - `pts/tests/run/fetch_local.pts` (lines 115 and 154 expect "not supported").
  - `docs/examples/guide_fetch.pts` and `docs/GUIDE.md:1175`: the example fetches `https://example.com/` inside `TestRun`, which would make the suite depend on the network.
  - SPEC §7.1's "until TLS is added" text.

Probes (scripts, the Node `.mts` runs, the recorded oracle, RED diffs and logs, the lab copy) are in `/tmp/h33/reproducer-probes.tar.gz`, from `/tmp/h33/reproducer/`. The probe lab is stopped and no stray processes are left.
