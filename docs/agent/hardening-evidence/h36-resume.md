# H36 interrupted-port recovery and independent review

2026-10-26. Parent and both named children use the session's inherited `gpt-6.1-sol` runtime. Base main: `f0a3cf56d6492d6540387b6a9a8c078a8030da58` (D-227). This continues H36 only; H09's blocked/deferred acceptance is untouched.

## Recovery and startup

- `git status` found an interrupted, uncommitted macOS port: 53 tracked paths and `pts/third_party/libbacktrace-config-darwin/`. It was not discarded. Before edits, the binary diff and untracked config were archived in `/tmp/pts-h36-resume-20261026/`: `inherited.patch` SHA256 `31603f048781d6d2fbf5febcc8ba4161320a02fe57eaf288c7db9043acc5a997`; `inherited-untracked.tar.gz` SHA256 `532b3eee87ef391e27973afc5bee983802f02baef7dc1935c828e3c78d8a8e66`.
- One non-blocking startup CI query: [receipt](h36-resume-ci.json). Main's D-227 commit `f0a3cf56d` passed run **40288979269**, superseding the earlier native-arm numeric failures; its nightly invocation was skipped. Scratch port `35089901e` passed **40286550840**, superseding four failed development rounds. No later CI query, polling or waiting.
- **Next-session CI backfill (H13 startup):** D-227's `f0a3cf56d` also passed run **40295068495**. H36's implementation checkpoint `6dc797bb1` has runs **40304536209** and **40304356582** still in progress, not claimed green. [Startup receipt](h13-startup-ci.json); no later query or wait. The independent Mac-port acceptance remains blocked.
- **H13b startup CI backfill:** implementation checkpoint `6dc797bb1` passed **40304536209**, as reported by the one startup query ([receipt](h13b-startup-ci.json)). That run certifies its named SHA only; the provider-refused independent H36 acceptance remains blocked. No polling or waiting.
- The named `reproducer` and `reviewer` recovered the patch into separate worktrees under `.claude/worktrees/`, based at `f0a3cf56d`. Neither writes the parent's tree. Explicit model override `gpt-6.1-sol` was rejected by the Agent tool's enum (`expected one of "sonnet"|"opus"|"haiku"|"fable"`); launching without that override inherited the configured runtime, which both children confirmed as `gpt-6.1-sol`. This was tool-schema validation, not a provider content refusal.
- Source files are frozen throughout each full-suite execution. Children initially perform static review only, with their heavy-test budget closed while the parent's four-job baseline runs.

## Historical Mac execution independently checked

This host is Linux x86-64, not a Mac. The actual Mac receipt is scratch SHA `35089901ea49e5c697fe47a998b93ad3abc9ef56`, run **40286550840**, macOS 14.8.9 / Apple clang 15.0.0 / Apple M1. It is not exact-SHA CI for the recovered main change or subsequent fixes.

- Parent reparsed the raw `suite.jsonl`: **6,042 test pass / 416 test skip / 0 fail**, all ten tested packages pass; corpus **269 Node-identical / 8 intentional / 23 unsupported / 0 failing**. `suite.stderr` contains only two Go module-download notices (87 bytes), not empty stderr.
- Parent byte-compared the 381-line Node and PTS numeric artifacts: identical, SHA256 `1d9e18e8a045e240721a63ed7dce64b6a6f02519e73bc86a5cbf56eb1e0bcd6b`.
- Recovered production source matches the Mac-tested source except comments in `date.c`, `ieee754.c`, `number_methods.c` and `v8_fma_internal.h`; the Darwin config headers match too. The raw historical logs retain the libSystem-only dependency probe, `.pts` stack trace, lldb breakpoint and interleaved stack-guard cost probe described in [H36](h36.md).
- Corrected inherited documentation that falsely said the recovered port had already landed and the scratch branch was deleted. The branch and worktree still exist; this session does not clean up unrelated retained worktrees.

## Local baseline

An initial `GOMAXPROCS=4 go build -p 4 ./...` failed before reaching the suite because ignored historical scratch sources under `pts-bin/` contain mixed Go packages and standalone C files (`d210-helper.go`, `d214`, `h09b5c2a`). The original failure is retained in `baseline-build.log`; those files were not deleted or changed. This broad wildcard build is **not claimed green**.

The required baseline is a separate `GOMAXPROCS=4 make build` followed by:

```
GOMAXPROCS=4 PTS_REQUIRE_NODE=1 go test -count=1 -json -p 4 -parallel 4 -timeout 40m ./pts/... ./cmd/pts/
```

Result: **exit 0, 6,332 test pass / 403 test skip / 0 fail**, all ten tested packages pass, empty stderr. Corpus: **269 Node-identical / 8 intentional / 23 unsupported / 0 failing**. All **2,095 monitored source hashes** stayed unchanged. Raw streams, exit marker and source hashes are in `/tmp/pts-h36-resume-20261026/baseline-*`.

## C372: coherent process memory counters, RED → GREEN

Both children identified that the new `memoryUsage()` path calls bdwgc's explicitly unsynchronized `GC_get_heap_size` / `GC_get_free_bytes` and samples them separately around RSS measurement. Vendored `gc.h:726–740` requires the allocator lock to avoid multiprocessor data races. Its documented `GC_get_heap_usage_safe` alternative returns one coherent snapshot under that lock; individually locking two calls would not fix the sample mismatch.

The reproducer supplied `TestMemoryUsageKeepsHeapCountersFromOneSnapshot`, a public-API fixture plus GNU linker interposition at `uv_resident_set_memory`, which grows the real collector heap with `GC_expand_hp`. Parent and reproducer executed the recovered implementation independently, **exit 1**:

```
true false true
incoherent heap 4259840 -12533760
```

The shim printed `test grew collector by 12648448 bytes at RSS sample`. Parent then replaced the three getters with one `pts_process_memory_usage` intrinsic: RSS from libuv, heap total and free bytes from **one** `GC_get_heap_usage_safe` call, returned in a published number array. `Process.memoryUsage()` constructs a fresh result; `external` and `arrayBuffers` remain 0 because their storage is in the collector's heap. Same regression **GREEN, exit 0**, independently in both trees. The ordinary API fixture also passes strict Node comparison of shape, invariant counts, fresh objects and mutation isolation (not exact V8/collector byte counts).

Parent command prefix: `GOMAXPROCS=1 PTS_REQUIRE_NODE=1 go test -count=1 -v -p 1 -parallel 1 -timeout 10m ./pts/tests/`, selector `-run '^TestMemoryUsageKeepsHeapCountersFromOneSnapshot$'`. Raw parent logs: `memory-red-behavior.txt`, `memory-green.txt`, `fixes-green.txt`. The initial `memory-red.txt` was a shim **setup failure**, not behavioral RED: an unnecessary `uv.h` include under strict C11 exposed unavailable `pthread_rwlock_t`; removing the unused include allowed the real negative-counter reproduction above.

## C373: adoption before listener startup, RED → GREEN

The partial reviewer delivered a controlled schedule reproduction: the first started worker accepts three real TCP clients while the caller pauses just before posting the last listener's startup. Round-robin handoff sends the third accepted connection to that last loop before its listener-start message. Its initialized server/listener state is sufficient to handle a connection; a NULL listening handle does not mean the server stopped.

Parent integrated `TestMacOSHandoffBeforeListenerStartup`, compiling the actual Apple-specific `http.c` branches over the real Linux scheduler, queues, GC, libuv and TCP. Only `http.c`'s posting seam forces the legal queue order. **This is a Linux surrogate, not native-Mac execution evidence.** Parent's corrected RED:

```
startup: exit 1
mode=startup before: listening=0 stopped=0; after: connection_kept=0 duplicate_open=0
stopped: exit 0
mode=stopped before: listening=0 stopped=1; after: connection_kept=0 duplicate_open=0
```

The initial new stopped-control assertion incorrectly expected `listening=1`; stop clears the listening handle. Parent checked the child's raw control output, corrected that assertion to `0`, and reran RED before touching production. This test-authoring mistake is retained separately in `handoff-red.txt`; corrected behavioral RED is `handoff-red-corrected.txt`.

Fix: `adopt` rejects a **stopped server**, not a missing listener handle. Parent then reran both modes and the memory regression, **exit 0** (`fixes-green.txt`): startup retains its connection/descriptor, stopped + forced collection retains neither. The partial reviewer inspected server/listener uncollectable lifetime and startup's lack of a connection-list reset; no complete independent acceptance is inferred from that partial work.

## Other focused verification and test-design corrections

- Integrated the reproducer's real-path emission tests (file/directory/chained symlinks, quoting, unresolved-name controls), recording-tool target/library/cache tests, exact process target/executable-link identity, ordinary memory API fixture and native fs constants/string flags. Parent's corrected focused Go run passed all selected tests (`focused-corrected.jsonl`). The recording tools verify commands/cache selection, **not Mac compilation**.
- Child exact-base emitter overlay: three symlink cases fail, three unresolved-name controls pass; recovered emitter passes all six. Exact-base target overlay: both tests fail with `unknown target "aarch64-macos"`; recovered target/library/cache cases pass. These are the reproducer's scoped RED/GREEN controls, not a whole-port review. Parent independently ran the integrated GREEN cases and both new runtime behavioral RED/GREEN regressions.
- The first integrated focused command used an invalid Go slash-separated `-run` regex; the new target test also passed an unsupported message argument to `assert.DeepEqual`. These are retained setup/build failures (`focused-new.jsonl`), not production RED. Parent removed only that test-only argument and used separate valid selectors.
- The first new fs fixture incorrectly assumed numeric flags in read/write option objects; the existing API declares `flag?: string`. Parent's suggested numeric `openSync`/`promises.open` alternative was also mistaken: file-descriptor APIs are explicitly absent (SPEC §8). No production API was widened. The original input/diagnostics are retained; the corrected fixture compares native numeric constants and every **supported** write/read string alias against Node. Parent's corrected fs/memory run passed both strict Node comparisons (`focused-programs-corrected.jsonl`); fixed-output fs is intentionally skipped because constants vary by platform.
- Linux's original **200 simultaneous connects** restored in both signal tests; macOS alone uses waves of 100 to isolate signal correctness from C367. Parent passed `TestRun/{fetch_abort_other_thread,fetch_signals_parallel}` at **1/2/4 threads** (`focused-programs.jsonl`, whose overall exit 1 came only from the original unsupported fs fixture). The two signal programs use ParallelScript features and are not strict-Node harness cases.
- `GOMAXPROCS=1 go build -p 1 ./pts/... ./cmd/pts/` and `GOMAXPROCS=1 go vet -p 1 ./pts/... ./cmd/pts/`: **exit 0**, empty output. The broad historical-scratch wildcard failure above remains recorded.
- `npm --prefix pts/editors/vscode test`: **exit 0**, TypeScript typecheck and all **4 grammar tests pass**. This is not VS Code end-to-end execution on Mac; the unchanged extension uses a `pts` binary built on the Mac, as documented. No group/final acceptance or full matrix is claimed.

The [finite reproducer report and selected raw archive](h36-resume-reproducer.md) preserve its inputs, logs and original test-design errors; parent verified the five exact-base overlay source files byte-for-byte against `git show f0a3cf56d:<path>`. The reproducer completed its finite assignment and released its one-job budget before the parent's final four-job suite. Neither child committed, pushed, queried new CI or modified the parent's working tree.

## Independent acceptance BLOCKED

The named reviewer terminated with a provider HTTP 400 content flag before returning a final whole-port verdict. [The exact error and already-delivered partial findings](h36-port-review.md) are retained verbatim. The assignment is **not retried, rephrased, reassigned or moved to another model/provider**; the reproducer was not made a replacement reviewer. H36 remains **unchecked**. This session checkpoints implementation and locally verified fixes only; H13 is the next independent task, not started here. CI for the new checkpoint is **pending**, not queried or polled.

C367's macOS burst resets remain open under H14, retained as skipped `run/macos_fetch_burst.pts` rather than being called fixed by throttled signal tests. Historical native RED is the earlier burst probe, not a new native run of that retained fixture. Intel Macs, native keychain trust, non-Linux publication checking and macOS versions before the tested 14.8.9 remain outside the execution evidence (the target names deployment version 11). The new memory/handoff fixes have no native-Mac execution receipt yet.

## Final local gate

After all production/test edits and both child budget releases, `GOMAXPROCS=4 make build` and the exact required four-job full-suite command above completed **exit 0**: **6,350 test pass / 406 test skip / 0 fail**, all **ten tested packages PASS**, empty stderr. Corpus: **269 Node-identical / 8 intentional / 23 unsupported / 0 failing**, 300 programs. All **7,217 monitored source/input/build hashes**, including the rebuilt CLI, remained unchanged. No source edits or concurrent child heavy jobs during execution.

Raw baseline/final JSON streams, source manifests, exit markers, exact parent commands, both runtime regressions' RED/GREEN, original setup/control/test-design errors, build/vet/extension logs and recovery patch are retained in [h36-resume-parent-probes.tar.gz](h36-resume-parent-probes.tar.gz): **62 files**, SHA256 `eea914ae7da17028effbb29e5a21208bf06c38ac598ac251f5fe6ba0107a22c7`. Archive has no binaries, symlinks or private-key material. Reproducer archive/provenance is linked above. No new native-Mac run, full matrix, VS Code end-to-end or independent whole-port READY verdict is claimed. H36 is **not ticked**; CI for this locally green implementation checkpoint is **pending**, not queried or polled.
