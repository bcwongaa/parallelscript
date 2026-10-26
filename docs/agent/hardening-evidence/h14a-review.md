# H14a independent review

2026-10-26. Scope: D-230's absolute pre-dispatch handshake/header deadlines only. H14b/H14c, H13b's provider-refused scope and H36/H09's blocked/deferred acceptance are not reviewed or accepted here. One named same-runtime `reviewer` (`a074c4180c59ce21a`), resumed between rounds; isolated snapshot `.claude/worktrees/h14a-independent-review`.

## Round 1 — NOT READY

Frozen implementation/public-policy patch SHA256: `87c7334c775f5bf2983b7a99940663ceb544400404f10394f8a825216272343d` (base `d5250355b`). The supplied v1 regressions passed **55 test events**, exit 0, empty stderr. Independent blocked-loop completion, pipeline/body/100-Continue and direct state controls passed **11 events**, exit 0, empty stderr.

**P2 / C374:** `on_deadline` closed unconditionally although libuv's millisecond clock can deliver its callback before the nanosecond `deadline_at`. The real-libuv production-wrapper observation reported `early_count=27/32 maximum_early_ns=471956` for supported 2 ms budgets. That original observational test exited 0; it was not an assertion RED. Its actually executed Go/C sources are retained separately. The child froze a stronger post-callback `early_count=0/32` assertion but did not run it under the parent's HOLD.

Parent independently replayed the frozen stronger inputs against v1: **11 pass /1 fail**, exit 1, empty stderr; `early_count=32/32 maximum_early_ns=646734`. Thus the production callback really closed early. Before that replay the parent accidentally compiled during a child fixture edit and hit an unused `strings` import; the build-failure stream is retained and is not behavioral RED.

The reviewer's first network-control input incorrectly required graceful EOF even when closing unread bytes legitimately caused `ECONNRESET`. Its corrected probe accepts EOF or reset but still requires actual closure, zero response bytes and no expired/follower dispatch. Original EOF-only input and failed raw results are retained; no production defect is inferred from this fixture mistake.

Safe frozen archive: [h14a-review1-probes.tar.gz](h14a-review1-probes.tar.gz), SHA256 `1fe0d1452152eb2ba997c59c15e4c4f90bf632c6641030e069f030d707f225a3`. Parent independently inspected all 17 regular members in a fresh extraction directory: no links/path traversal, ELF binaries or private-key PEM material.

## Fix and deterministic RED → GREEN

The callback now samples `uv_hrtime()`, rearms the rounded-up remaining milliseconds if early, and preserves the original absolute timestamp. It closes only once that timestamp has elapsed. No progress reset or additional response write was introduced.

Parent's new ordinary-suite native regression `TestHTTPServerDeadlineTimerRespectsAbsoluteClock` simulates delivery of an early one-shot callback and requires a live rearmed timer with unchanged absolute budget, then drives actual expiry and verifies detached timer data, removed connection id and close ownership. The zero-disable control requires no timer allocation. It failed before the production fix in both release and ASan/UBSan, then passed both (**3 pass events** including the group), empty stderr. The test's initial mistaken `string(profile)` setup was rejected by Go vet; that original source and build output are retained separately, not counted as RED.

Frozen round-2 patch SHA256: `7c274bd3ab0dd2bbea2864eaf67644a5e557a2485f5272878fa56d1855bafd6f`. Production `http.c` SHA256: `20d791124762947a2ccb759c4b8c841c6d18f4f86b1c618092e1926aaa8064a4`; parent/snapshot compared byte-for-byte. The reproducer's v2 regression and final adoption-origin/public-verification clarifications are also in that snapshot. Parent independently ran final v2 release and sanitizer regressions: **59 pass events each**, exit 0, unchanged 7,221-file manifests.

## Round 2 — READY

The same reviewer independently reran its frozen probes and the deterministic regression, serially against cached runtimes:

```sh
GOMAXPROCS=1 PTS_REQUIRE_NODE=1 go test -count=1 -json -p 1 -parallel 1 -timeout 4m ./pts/tests \
  -run '^(TestH14aReview.*|TestHTTPServerDeadlineTimerRespectsAbsoluteClock)$'
```

**Exit 0: 15 pass /0 fail**, 22.934 seconds, empty stderr. Strengthened real-libuv probe: `early_count=0/32 maximum_early_ns=0`. No unresolved finding in H14a; the original absolute timestamp, cancellation, detached close ownership and GC-root lifetime are preserved. The reviewer verified the frozen v2 patch and `http.c` hashes above; all 16 prior frozen inputs/logs stayed byte-identical. No review source edits occurred in this round.

Parent independently replayed the same 15 events after the fix: **exit 0, 15 pass /0 fail**, empty stderr, `early_count=0/32 maximum_early_ns=0`. Before the fix, the frozen strengthened rounding test genuinely failed while the other 11 independent events passed, as recorded above.

Safe final archive: [h14a-review2-probes.tar.gz](h14a-review2-probes.tar.gz), SHA256 `4d0171ad9705623cc933ba2f7db92511f55c9ac7494d1dcf998c47c8ee9a09a0`. Parent inspected all 25 regular members in a new empty extraction directory; no links/path traversal, ELF binaries or private-key PEM material. Both children explicitly released their heavy-job budgets before the exclusive final build/full suite. Review READY does not by itself tick H14a; full-gate/implementation-push acceptance is recorded in [H14a evidence](h14a.md).
