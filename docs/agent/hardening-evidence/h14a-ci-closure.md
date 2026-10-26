# H14a CI repair: deadline closure may be a TCP reset

2026-10-27. CI-first repair, before accepting or extending the interrupted H14b1 implementation. No runtime or public-policy change in this repair.

## Startup receipt and cause

The one startup query is retained in [h14b1-startup-ci.json](h14b1-startup-ci.json). H14a's implementation run 40327631527 and acceptance run 40328109964 were cancelled/superseded. Later main `3087b1bda` passed run **40328899585**. Main `4dba0fc57` then failed run **40337811057** only on two macOS plain-HTTP trickle closure assertions:

- One loop: **402.6025 ms**, empty response bytes, `read: connection reset by peer`.
- Four loops: **403.619417 ms**, empty response bytes, the same reset.

The configured header budget is 400 ms. D-230 requires closure without a response or dispatch; it does not promise a TCP FIN rather than RST when a trickling client's unread bytes race immediate close. The test's nil-only `io.ReadAll` assertion was wrong. The handshake test already accepted ECONNRESET. The corrected shared predicate accepts only ReadAll-normalized EOF (`nil`) or `errors.Is(err, syscall.ECONNRESET)`, not arbitrary errors or a matching message string. Empty-wire, absolute-time, no-dispatch and descriptor-cleanup assertions remain intact at all four call sites.

## Real execution

- Fresh unchanged `4dba0fc57` snapshot: `make build`, then `GOMAXPROCS=4 PTS_REQUIRE_NODE=1 go test -count=1 -json -p 4 -parallel 4 -timeout 40m ./pts/... ./cmd/pts/`: **exit 0, 6,593 pass /406 skip /0 fail**, ten tested packages PASS, empty stderr. Actual corpus **264 comparisons /36 skips**. The snapshot's only git-status changes are the six documented local third-party symlinks; no tracked source change. This Linux run does not certify native macOS.
- Parent added the eleven-case reader regression with the original nil-only predicate: **exit 1**, the raw and wrapped ECONNRESET assertions fail. This is deterministic oracle RED, not a claim to reproduce macOS kernel scheduling on Linux.
- Parent changed only the predicate, then ran `GOMAXPROCS=1 go test -count=1 -v -p 1 -parallel 1 -timeout 8m ./pts/tests/ -run '^(TestResourceClosureAcceptsEOFOrResetOnly|TestHTTPServerPreDispatchDeadlines|TestHTTPSServerHandshakeDeadline|TestHTTPServerExpiredConnectionsReleaseResources)$'`: **exit 0, 63 PASS events**, no failure. Parent repeated the same command with `PTS_HOME` set to the unchanged baseline runtime: **exit 0, 63 PASS events**. HTTP/verified HTTPS and one/four loops are exercised. New negative controls reject deadline, timeout, local close, unexpected EOF, EPIPE, unrelated error and reset-looking text.
- Named isolated reproducer supplied tests; named independent reviewer separately reported **CI-only READY**, no findings, after inspecting the exact patch. Parent independently ran the RED/GREEN above. This verdict does not accept H14b1.

Parent and both children inherit `gpt-6.1-sol`. The explicit model string was rejected by the Agent tool's alias-only schema; no alternate model was selected. The exact same workaround is documented in H14a's original evidence. Children use isolated worktrees; all heavy execution was held during the full baseline, then limited to four jobs total. No upstream package was edited. No CI wait/poll after startup; **CI: pending** for this repair.

Raw startup failure, parent oracle RED/GREEN and baseline-runtime GREEN are retained in [h14a-ci-closure-output.tar.gz](h14a-ci-closure-output.tar.gz). The interrupted H14b1 patch remains separate and unaccepted until its own gates pass.
