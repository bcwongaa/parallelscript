# H33b review rounds

## Round 1 (verbatim)

Child: `reviewer` (claude-opus-5-5, xhigh), read-only, on snapshot 1 (`/tmp/h33b/snap1`: `8fe8a7b1e` plus the change, the implementation committed as `d393fae47`). Probes: [h33b-review1-probes.tar.gz](h33b-review1-probes.tar.gz). The child's final message follows, unedited except that HTML entities are written as their characters.

---

VERDICT: NOT READY

The HTTPS server itself held up: no memory or race defects under ASan, UBSan or TSan, and every claim I re-measured matched. Two things should be dealt with before acceptance. Any client can kill the server remotely (R1-F1); that is a pre-existing bug, but HTTPS inherits it. And the server doesn't answer a client's close_notify (R1-F2), which is new in this slice. The code and tests I reviewed in `/tmp/h33b/snap1` are byte-identical to the commit `d393fae47`; only SPEC's added §9.149 and the re-archived reproducer tarball differ.

## Findings

**R1-F1 · high · pre-existing (base `8fe8a7b1e` plain HTTP fails the same way) · confirmed.** A client that pipelines requests for large answers and then resets the connection kills any `pts:http` server, plain or HTTPS, by SIGPIPE: exit 141 with nothing on stderr. The runtime never ignores SIGPIPE, and libuv writes with `write()`.
- **Repro** (6 of 6 runs died): `/tmp/h33b/review1/repro_f1_sigpipe.sh /tmp/h33b/base/pts-bin/pts plain`, then the same with `/tmp/h33b/snap1/pts-bin/pts plain` and `… tls`. Each prints `server exit status 141 PIPE`.
- **Mechanism** (strace): within one `process()` pass, a write fails with `ECONNRESET`. libuv reports that only in the deferred callback, so the server answers the next pipelined request on the dead socket:
  ```
  write(13, "HTTP/1.1 200 OK…", 100120) = -1 ECONNRESET
  write(13, "HTTP/1.1 200 OK…", 100120) = -1 EPIPE
  --- SIGPIPE --- +++ killed by SIGPIPE +++
  ```
- **Not affected:** a ClientHello followed by a reset (3,000 tries survived), and `fetch` when the server resets mid-body.
- **Fix:** ignore SIGPIPE when the runtime starts, as Node and Bun do. To keep `prog | head` behaving, re-raise SIGPIPE when a write to stdout or stderr gets EPIPE (Go's approach). Add a RED test: a client pipelines 100 `GET /big` (100 KB answers) and resets, 20 times, then the server must still answer and exit 0. If it isn't fixed in H33b, record it as a C-number with a skipped RED test.

**R1-F2 · low to medium · new in the slice · confirmed.** When a client sends close_notify on an idle connection, the server closes TCP without a close_notify of its own. RFC 8446 §6.1 and RFC 5246 §7.2.1 require one, and Node sends it.
- **Repro:** `/tmp/h33b/review1/repro_f2_close_notify.sh`

  | | Records sent after the client's close_notify | Python `ssl` `unwrap()` |
  |---|---|---|
  | pts, TLS 1.2 and 1.3 | none | `SSLEOFError [SSL: UNEXPECTED_EOF_WHILE_READING] EOF occurred in violation of protocol` |
  | Node 24.21, TLS 1.2 | `[21]` (alert) | succeeds |
  | Node 24.21, TLS 1.3 | `[23]` (encrypted alert) | succeeds |

- **Where:** `http.c` `end_sealed`. On `PTS_TLS_CLOSED`, `send_sealed(c, true)` finds no output and calls `close_connection` at once.
- **Fix:** have `unseal` report CLOSED apart from FAILED. In `end_sealed`, when the request is absent and the peer sent close_notify, call `pts_tls_close` before `send_sealed(c, true)`. Test it with `CloseWrite()` on an idle connection followed by `endedWithCloseNotify`. Also adjust "after mbedTLS's alert" in ARCHITECTURE and D-224: no alert goes out for a close_notify, nor after a client's KeyUpdate (the connection just closes).

**R1-F3 · low · new · confirmed.** Each loop's identity decrypts the key again, so the cost of an encrypted key multiplies with the loops. With a 1M-iteration PBKDF2 key (`opts/slowkey.pts`), `serve` blocks for 1,404 ms on one loop, 5,183 ms on four and 10,542 ms on eight. OpenSSL's default of 2,048 iterations doesn't notice. Suggestion: decrypt once, export to DER in a buffer that is wiped afterwards, and parse the DER on each loop. Or document the cost.

**R1-F4 · low · test gap.** No test pins D-224's `serve's tls cert: a certificate in it can't be parsed` (`identity_failure`'s `error > 0` branch). I checked it by hand: a corrupt intermediate, bad base64, and a corrupt first certificate all refuse correctly (`opts/partial.pts`). Also, no test runs an RSA key on several loops, which is the case the per-loop identity exists for; my TSan probe of it was clean.

**R1-F5 · low · pre-existing · confirmed.** `stop()` treats a connection whose answers are still queued as idle and closes it, dropping the queued part. A 5,000,117-byte answer arrives as 2,588,672 bytes, the same on the base and the change; over TLS it is 2,572,252 bytes and then `unexpected EOF`. Probe: `slowread/main.go`. This belongs to D-163's semantics, not H33b.

## Verified sound
- **Lifetime and memory:**
  - Up to 64 mixed clients on four loops at PTS_THREADS=4 with `reusePort`, release and ASan+UBSan builds, with SIGPIPE ignored so R1-F1 doesn't mask everything: 119,308 and 64,931 good requests, 0 bad, empty stderr. The mix covered fresh connections, keep-alive, pipelining, resets mid-handshake, mid-request and mid-response, close_notify while a request is held or queued, garbage, TLS followed by garbage, requests split at random record sizes, idle connections, 400s and handler throws.
  - `stop()` and `stop(true)`, from another loop and from a pool thread, under that load: 14 runs (8 on the ASan build at 48 workers), every one exited 0 with empty stderr.
- **Threading:** TSan with the collector off, EC and RSA keys on four loops, including `stop(true)` under load. The only reports were my probe's own unsynchronised counter. Nothing in `tls.c`, `http.c` or mbedTLS; the shared PSA start-up is fine. No nested `uv_run`, so the per-thread read buffer is safe.
- **Protocol:**
  - curl: TLS 1.2 and 1.3, connection reuse, a 3 MB upload with 100-continue, HTTP/1.0.
  - `openssl s_client`: P-384, P-521, X448 and ffdhe2048 groups, including HelloRetryRequest; GCM, ChaCha20 and CCM suites; TLS 1.2 suites for each key type. The only failures were key-type and signature mismatches, plus mbedTLS's lack of RSA-PSS signatures in TLS 1.2.
  - TCP delivering every record one byte at a time, through the handshake and the request; a 512-byte maximum fragment length.
  - Renegotiation gets a `no_renegotiation` warning; a KeyUpdate closes the connection. Both fail safe.
  - The 64 MB body limit and a 60 MB answer behave as over plain HTTP, with the same peak memory (about 200 MB).
  - Half-close after a request matches plain HTTP (D-163's documented gap).
- **Options:** EACCES, ELOOP, ENOTDIR, ENOENT (including an empty path and a dangling symlink) match pts's own `readFileSync` field for field. `/dev/null`, a 50 MB junk file and `/proc` files give mbedTLS's description. Junk around PEM blocks and a combined cert-plus-key file work. After warm-up, 6,000 rounds of each refusal grew RSS by 0–4 kB.
- **Plain HTTP and docs:**
  - Throughput unchanged: about 54–57k requests a second, base and change interleaved.
  - Sizes exactly as D-224 says: hello world 1,428,822 bytes on both; a server 1,522,557 → 2,174,386; `guide_fetch` +8,168 bytes.
  - Handshake rates in line with D-224: RSA 384–430 a second and P-256 834–926 on four loops.
  - The GUIDE snippet compiles and serves `https://localhost:8443/…` as it says.
  - The C349 skipped test reproduces (`false false false true` / `false false`; Node gives all `true`), and C348 and C349 are recorded in CONSOLIDATED.
- **Tests:** the parent's suite was green (6,005 pass / 401 skip / 0 fail, empty stderr). `TestHTTPSServer*` passed 3 of 3 runs on the snapshot. The tests are bounded and not vacuous.

Probes are archived at `/tmp/h33b/review1-probes.tar.gz`. The two repro scripts are `/tmp/h33b/review1/repro_f1_sigpipe.sh` and `repro_f2_close_notify.sh`; the Go clients they call are kept built in `/tmp/h33b/review1/`. Nothing was left running, and neither the snapshot nor the repository was modified. `/tmp/h33b/review1.done` is touched.


## Round 2 (verbatim)

The same `reviewer`, resumed, read-only, on snapshot 2 (`/tmp/h33b/snap2`: `d393fae47` plus round 1's fixes, committed as `61c3dd061`). Probes: [h33b-review2-probes.tar.gz](h33b-review2-probes.tar.gz).

---

VERDICT: READY

All five round-1 findings are fixed or properly recorded, and I found no high or medium regressions. The fixes did introduce two low-severity items, a duplicate close_notify and a wrong statement about Node in the docs, both cheap to fix. Snapshot 2's own suite is green: 6,014 pass / 401 skip / 0 fail, ten packages, empty stderr (snapshot 1: 6,005 / 401 / 0). Both snapshots are byte-intact against the parent's checksums, and the repository is clean.

## Findings

**R2-F1 · low · new, introduced by the R1-F2 fix · confirmed.** The server can send close_notify twice. This happens when the client's close_notify arrives in the same read as a request whose answer already closes the connection (`Connection: close`, HTTP/1.0, or a 400/413/431). `seal` already wrote close_notify, and then `end_sealed(c, true)` calls `pts_tls_close` again; `mbedtls_ssl_close_notify` has no guard against a second call.
- **Repro:** `/tmp/h33b/review2/dupcn/run.sh` (sends a `Connection: close` request, then `CloseWrite()`):

  | | TLS 1.2 records | TLS 1.3 records |
  |---|---|---|
  | snapshot 1 | `[23(150) 23(21) 21(18)]` | `[23(160) 23(32) 23(32)]` |
  | snapshot 2 | `[23(150) 23(21) 21(18) 21(18)]` | four records, the last 32-byte record repeated |

- **Impact:** harmless in practice. RFC 8446 §6.1 says data after a closure alert is ignored, and the tests don't notice because Go stops at the first alert.
- **Fix:** make `pts_tls_close` idempotent with a `close_sent` flag in `pts_tls`, which covers every caller.

**R2-F2 · low · docs.** SPEC §8 and D-224's SIGPIPE paragraph say Node "ignores the error and goes on". That holds for `console.log` only. With Node 24.21 writing 300,000 lines into a reader that leaves (`goign/node_out.py`), `console.log` exits 0, but `process.stdout.write` ends in an unhandled `EPIPE` error event and exits 1. Also worth one line in the docs: pts now dies by SIGPIPE even when started with SIGPIPE ignored, which systemd does by default. Snapshot 1 ran on (exit 0); Go behaves like snapshot 2; coreutils `yes` reports "Broken pipe" and exits 1 (`goign/ign.py`).

**Informational, not defects:**
- **A failing session now cuts a partly queued answer.** Snapshot 1 queued its alert behind a pending answer; `send_last` skips it when anything is queued. With a 3 MB answer followed by a corrupt record, snapshot 1 delivered all 3,000,121 bytes and then `bad record MAC`; snapshot 2 delivers 2,539,486 bytes, then `unexpected EOF` (`queuedfail/main.go`). A client's close_notify cut it the same way on both snapshots. This matches D-224's rule that a session's end works like a plain connection's end.
- **One unexplained failure, not reproduced.** In my first run of `repro_f1_sigpipe.sh` the stress tool's final health check failed once (`bad=1`, sample not kept). It did not recur in 8 more runs, nor in 3 mixed runs with 150,995 good requests and 0 bad.

## Each fix verified, snapshot 2 against snapshot 1

**R1-F1 (SIGPIPE) fixed.**
- `repro_f1_sigpipe.sh`: snapshot 2 survives every run, plain and HTTPS; snapshot 1's HTTPS server still dies with 141.
- Mixed load with SIGPIPE at its default: 105,457 good requests on the release build and 58,719 under ASan+UBSan, 0 bad.
- Eight `stop()`/`stop(true)` runs under ASan at 48 workers: all exit 0 with empty stderr.
- TSan, four loops, a DER-derived RSA key: 0 reports.
- Output behaviour is unchanged from snapshot 1 in every case I tried (`out/out.py`):
  - SIGPIPE when the reader leaves, for `console.log`, `process.stdout.write` and `console.error`, from a `parallel` task, from `.par.forEach`, and when the reader is gone before the first write.
  - Exit 0 for `/dev/full`, a closed stdout or stderr, and a file (complete).
- No false SIGPIPE: a server logging every request to `/dev/full` or a healthy pipe, under resets that make its sockets fail with EPIPE, exits 0 (`stale/run_stale.sh`).
- Improvement: `pts:fs` sync, append and promise writes into a FIFO whose reader left now throw Node's exact error (`EPIPE: broken pipe, write`, -32, `write`), where snapshot 1 died. `fetch` to a server that resets mid-upload still fails cleanly with `ECONNRESET`.

**R1-F2 (close_notify) fixed.** The server now answers a client's close_notify with its own, matching Node byte for byte, and Python's `unwrap()` succeeds. Alerts and FIN are unchanged on every failure path, including with 200 KB of unread input (`lastrec/main.go`).

**R1-F3 (key per loop) fixed.** With the 1M-iteration key on a loaded host, `serve` took 2.9 / 3.5 / 3.4 s at 1 / 4 / 8 loops on snapshot 2, against 2.2 / 12.3 / 21.8 s on snapshot 1. The DER path works across the board (`der/matrix.sh`):
- 10 keys × 4 forms × 1 and 8 loops, all serving with 0 bad handshakes.
- Keys: P-256, P-384, P-521, RSA 2048/3072/4096/8192 (8192 is mbedTLS's largest).
- Forms: PKCS#8, PKCS#1/SEC1, encrypted PKCS#8, legacy encrypted PEM.
- Four of the keys were chosen so that mbedTLS's own DER ends in 0x00 (`der/derlast.c` confirms the reparse and pair check).
- No leak on the refusal paths or the serving path; both snapshots grow identically.

**R1-F4 and R1-F5.** The partial-chain refusal is now tested and its output is identical on both snapshots. C351 is recorded under H14, and the CONSOLIDATED, HARDENING_PLAN and ARCHITECTURE text is accurate.

**The new tests are sound.**
- `TestHTTPSServerDecryptsItsKeyOnce`: ratio 0.95–1.10 over five plain runs, and 0.90–1.48 over ten runs sharing the cores with real HTTPS load (179,867 requests), against a threshold of 3; snapshot 1 gives 8.41. (I dropped a CPU-burner variant of this check after the permission system denied it.)
- `TestHTTPServersSurviveClientsThatResetMidPipeline`: its client logic missed snapshot 1's crash in 1 of 15 plain runs and 0 of 15 HTTPS runs, so the test as a whole goes RED reliably.
- `TestOutputToAReaderThatLeftEndsTheProgram` and `TestHTTPSServerAnswersCloseNotify` are deterministic.

Probes are in `/tmp/h33b/review2-probes.tar.gz`; the binaries are deleted except `dupcn/dupcn`, which R2-F1's repro script uses. Nothing is left running, and `/tmp/h33b/review2.done` is touched.


## Round 3 (verbatim)

The same `reviewer`, resumed, read-only, on snapshot 3 (`/tmp/h33b/snap3`: `61c3dd061` plus round 2's fixes, committed as `a6bc9f487`). Probes: [h33b-review3-probes.tar.gz](h33b-review3-probes.tar.gz).

---

VERDICT: READY

Both round-2 fixes check out on snapshot 3, nothing else changed, and the parent's full suite there is green: 6,014 pass / 401 skip / 0 fail across all ten packages, with empty stderr. The two findings below are minor and neither blocks.

## Findings
- **R3-F1 · low · pre-existing in the slice since `d393fae47`, not caused by round 2.** Sometimes a 400 answer and a corrupt record reach the server in one read. Then under TLS 1.2 it sends `[21 bad_record_mac, 23 the 400, 21 close_notify]`: mbedTLS writes its fatal alert while decrypting, and the 400 is sealed after it. RFC 8446 §6.2 says a connection closes once a fatal alert is sent, so nothing should follow it. Snapshots 2 and 3 behave the same: 9–10 of 10 runs, and the rest when the two writes are read separately. It's harmless, since clients stop at the alert. If you want to fix it, `seal`/`send_parts` could refuse to write once the session has failed. Probe: `cnpaths 400-then-garbage-at-once`.
- **R3-F2 · low · docs.** SPEC §8 and D-224 say pts ends by SIGPIPE "as C and Go programs do, also when it was started with SIGPIPE ignored". That reads as if C programs die too. Only Go does; a C program started with SIGPIPE ignored gets EPIPE instead (coreutils `yes` printed `Broken pipe` and exited 1 in round 2). Suggested wording: "… as C and Go programs do; also when started with SIGPIPE ignored, as Go (a C program then gets EPIPE)". The Node statements now match what I measured on 24.21: `console.log` exits 0, and `process.stdout.write` exits 1 on the unhandled `EPIPE` event. The pts:fs FIFO sentence matches too.

## Verified
- **Every `pts_tls_close` path** (`seal`, `end_sealed`, `close_idle`), TLS 1.2 and 1.3, snapshots 2 and 3 side by side (`paths.log`). Identical record sequences and FIN endings for:
  - answers that close (`Connection: close`, HTTP/1.0);
  - 400, 413 and 431;
  - the client's close_notify on an idle connection, and on a busy one whose request is held (close_notify answered, request aborted);
  - close_notify while a large answer is still queued (cut, no alert, as before);
  - a session failing after the server's close;
  - an idle connection closed by `stop()`.

  The only differences are the two fixed cases:

  | Case (10 runs each) | Snapshot 2 | Snapshot 3 |
  |---|---|---|
  | `Connection: close` request + close_notify read at once | 4 records, 2 alerts | 3 records, 1 alert |
  | 400 + close_notify read at once | 3 records (9 of 10) | 2 records (10 of 10) |

  TLS 1.3 shows the same one-record drop.
- **dupcn's scenario:** 2 alerts under TLS 1.2 on snapshot 2, exactly 1 on snapshot 3. The extra TLS 1.3 record is gone too.
- **The new `closesAndNotifiesAtOnce` check:**
  - Not vacuous: run against snapshot 2's runtime with `go test -overlay`, it fails 2 of 2 with `2 != 1` alerts. Snapshot 2's tree stayed untouched, since tests build into a temp directory.
  - Deterministic: 5 of 5 passes on snapshot 3, and 5 of 5 under load (311,282 HTTPS requests on the same 2 cores, with the parent's suite also running).
  - Its hand-made single segment guarantees both records land in one read.
  - Its alert count only runs under TLS 1.2, which is enough because the fix doesn't depend on the version.
- **Integrity:** snapshots 2 and 3 match the parent's checksums, and the repo copies of my earlier archives contain no private keys.

Probes are in `/tmp/h33b/review3-probes.tar.gz`, with no keys or binaries: `cnpaths/main.go`, `paths.sh`/`paths.log`, `repeat.sh`/`repeat.log`, `test_under_load.sh` and `overlay.json`. Note that `/tmp/h33b/review2-probes.tar.gz` in /tmp still holds the throwaway test keys; the repo copies don't. `/tmp/h33b/review3.done` is touched.


## Round 4 (verbatim)

The same `reviewer`, resumed, read-only, on snapshot 4 (`/tmp/h33b/snap4`: `a6bc9f487` plus round 3's fixes). Probes: [h33b-review4-probes.tar.gz](h33b-review4-probes.tar.gz).

---

VERDICT: READY

Round 3's fixes do what they claim, nothing else changed, and I found no new problems (no R4 findings). The parent's full suite on snapshot 4 is green: 6,015 pass / 401 skip / 0 fail across all ten packages, with empty stderr (one more pass than snapshot 3, the new test).

- **R3-F1 (nothing after a fatal alert) is fixed.** I ran 19 closing scenarios under TLS 1.2 and 1.3 on snapshots 3 and 4 (`paths.log`):
  - **What changed:** only the cases where the session fails while a request is being answered. `400-then-garbage-at-once` and the new held-segment `400-garbage-held` went from `[21 bad_record_mac, 23 the 400, 21 close_notify]` to `[21]`. `hello-garbage-held` (a keep-alive answer plus a corrupt record) went from `[21, 23, 23]` to `[21]`. Under TLS 1.3 the record counts drop the same way (3 to 1).
  - **What didn't:** every other path is identical, record for record, with a FIN at the end:
    - answers that close and HTTP/1.0;
    - 400, 413 and 431;
    - the client's close_notify on idle and busy connections, and both at-once cases;
    - `stop()` on an idle connection;
    - a failure after the server's own close.
  - **Held and queued answers:** a held request whose session fails (in the same segment or later) and an async handler answering after the failure both get just the alert; all 4 held requests saw their abort, with empty stderr (`held.sh`). A partly queued 3 MB answer followed by a corrupt record is still cut with no alert, as before.
- **The client side is unaffected.** `tls->error` is only set on failure (`status_of`, the name-check guard, a failed write). `fetch.c` calls `pts_tls_write` once, right after a successful handshake, and never calls `pts_tls_close`. All six `TestFetchHTTPS*` tests pass in the suite.
- **The new test is sound.** `TestHTTPSServerSendsNothingAfterAFatalAlert`, run against snapshot 3's runtime via `go test -overlay`, fails 2 of 2 with records `[21 23 21]` against `[21]`; snapshot 3's tree stayed untouched. On snapshot 4 it passed 5 of 5. Together with the other two tests that share `sized.pts`, it passed 5 of 5 each under load (304,509 HTTPS requests alongside), which also exercised the shared build.
- **The shared build covers the only collision.** No other server program is built by two tests (each `buildServer` name is used once), so the `text file busy` race can't recur elsewhere.
- **R3-F2's wording is now accurate:** Go programs die even with SIGPIPE ignored, and C programs get EPIPE instead.

Snapshots 3 and 4 match their checksums. Probes are in `/tmp/h33b/review4-probes.tar.gz`: `cnpaths/main.go`, `paths.sh`, `paths.log`, `held.sh`, `test_under_load.sh` and `overlay.json`. The archive holds no keys or binaries; a leftover server binary got in at first and I removed it. Nothing is left running, and `/tmp/h33b/review4.done` is touched.
