# H07 reproducer report (opus-reproducer, claude-opus-5-5, xhigh)

Returned as the child's final message on 2026-10-17 and saved verbatim by the parent (the named
subagents can't write report files). The child worked in the isolated worktree
`.claude/worktrees/agent-a812d2515ec2b93a8` at `0ddc0a60f`, scratch `/tmp/h07-repro/`. Its archive
is [h07-reproducer-probes.tar.gz](h07-reproducer-probes.tar.gz) (sha256
`14c85404e95ffc24df9b3992b089d4bfc53b5514483fa86dc8f29e5bb0bf8e6e`). The parent's own runs of these
claims are in [h07.md](h07.md).

---

I reproduced C07 on the base. Every red probe fails at 2 and 4 threads and passes at 1, and the new RED tests failed in 20 of 20 runs at 2 and 4 threads. A throwaway candidate fix turns them all green. The audit also turned up a second, separate race on shared signals that removing the flag does not fix.

Base is `0ddc0a60f`. The CLI `pts-bin/pts` has sha256 `2908debc…bd2bba`; I restored it to that clean build at the end. Runs used `PTS_HOME=<worktree>`, Node v24.21.0, go1.26.9 and clang 18.1.3. `$TMPDIR` was empty, so scratch is `/tmp/h07-repro/`. Nothing was committed or pushed. `pts/lib/internal/abort.pts` is back to the base version (sha `c8d368bf…`).

## A. H01b C07 probes on the base (verified)

```
cd /home/bryan/projects/parallelscript/.claude/worktrees/agent-a812d2515ec2b93a8
PTS_HOME=$PWD python3 -I docs/agent/hardening-evidence/probes/h01b/run_probes.py --pts "$PWD/pts-bin/pts" \
  --node "$HOME/.local/node/bin/node" --scratch /tmp/h07-repro/a/scratch --out /tmp/h07-repro/a/probes.json \
  docs/agent/hardening-evidence/probes/h01b/c07-*.pts docs/agent/hardening-evidence/probes/h01b/parent/c07-*.pts
```
Exit 0: "4 probes, 0 not behaving as their kind says". Failures per run below are from the final run (`final-base`).

| Probe | 1 thread | 2 threads | 4 threads |
|---|---|---|---|
| control (sequential) | 0/5 runs fail | – | 0/5 |
| red-abortsignal-abort-par | 0/20 | 20/20 (1,133–1,859 of 80,000) | 20/20 (4,235–6,953) |
| red-local-abortcontroller-par | 0/20 | 20/20 (1,378–1,793) | 20/20 (4,109–5,155) |
| parent red | 0/10 | 10/10 (280–490 of 20,000) | 10/10 (724–1,189) |

Every failure is `TypeError: Illegal constructor`, and every exit code is 0.

## B. New probes (verified)

They are in `docs/agent/hardening-evidence/probes/h07/`. Each has an H07 header, a sequential Node oracle `.mts`, and runs under a copy of the H01b runner that accepts `// H07` headers and records `PTS_HOME`. The final command was `/tmp/h07-repro/bin/final_runs.sh`: the same runner on `probes/h07/*.pts` plus the H01b C07 probes, once with the base CLI and once with the candidate CLI.

Failing runs, with failures per run in brackets, on the base:

| Probe | 1t | 2t | 4t |
|---|---|---|---|
| **B1: no HTTP** | | | |
| `.par.forEach` controllers | 0/20 | 20/20 [539–929 of 40k] | 20/20 [1,652–2,120] |
| `parallel for` controllers | 0/20 | 20/20 [627–872] | 20/20 [1,764–2,125] |
| `parallel` tasks: controllers with `onabort` and a listener | 0/20 | 20/20 [993–2,348 of 80k] | 20/20 [3,693–5,818] |
| `AbortSignal.any` in `.par.map` | 0/20 | 20/20 [2,359–2,953 of 40k] | 20/20 [5,184–7,093] |
| `AbortSignal.timeout` in `parallel` tasks | 0/20 | 20/20 [370–488 of 20k] | 20/20 [771–1,328] |
| `new AbortSignal()` while other threads make controllers (illegal-constructor leak) | 0/20 | 20/20 | 20/20 |
| control: `Event`, `EventTarget`, `DOMException`, dispatch in `.par` and `parallel` | 0/10 | 0/10 | 0/10 |
| **B2: reusePort server** | | | |
| handler reads `request.signal`, makes controllers and a `new Request(url, {signal})` | 0/10 | 10/10 [4–11 of 2,000] | 10/10 [56–107] |
| handler throws: one server has an error handler, one doesn't | 0/10 | 8/10 [0–7] | 10/10 [27–45] |
| **B3: parallel fetch** | | | |
| fetches with a new signal each, from `.par.map` and `parallel` tasks | 0/10 | 10/10 [14–21 of 1,600] | 10/10 [60–94] |
| control: the same fetches without a signal | 0/10 | 0/10 | 0/10 |

- **The leak goes the other way too.** `new AbortSignal()` should always throw. At 2 threads it succeeded 4,063–17,600 times out of 20,000, because another thread had the flag set. At 4 threads it succeeded 3,258–8,808 times, and the controllers in the other tasks failed 1,988–3,437 times.
- **Server internals never hit the flag.** `receive()` builds its `Request` without a signal. Every server stack trace points at handler code: `Request.signal (pts:http:238)` or `new AbortController` in the handler. With an `error` handler, the failures reach it and it answers 503. Without one, the error goes to stderr and the client gets 500 "Internal Server Error".
- **Fetch without a signal is safe.** It never constructs a signal, so the control passes everywhere.

## D. RED regressions (verified)

| File | What it covers |
|---|---|
| `pts/tests/run/abort_signals_parallel.pts` (`// threads: 1 2 4`) | `.par.map`, `.par.forEach`, `parallel for` and `parallel` tasks, plus `new AbortSignal()` mixed with controllers. About 0.15 s per run. |
| `pts/tests/run/fetch_signals_parallel.pts` (`// threads: 1 2 4`) | reusePort handlers and fetches with signals from the pool and the loops. About 0.25 s per run. |
| `pts/tests/http/signals_every_loop.pts` plus `TestHTTPServerSignalsEveryLoop` and `tallyBodies` in `pts/tests/http_test.go` | Go client, 16×50 requests on fresh connections, at PTS_THREADS 1, 2 and 4; also asserts empty stderr. |

Their Node twins are in `probes/h07/oracles/`, and their output equals the expect blocks.

```
GOMAXPROCS=2 go test -count=1 -v -run '^(TestRun|TestRunMatchesNode)$/^(abort_signals_parallel|fetch_signals_parallel|abort_signals)$' ./pts/tests/   # exit 1
GOMAXPROCS=2 go test -count=1 -v -run '^TestHTTPServerSignalsEveryLoop$' ./pts/tests/   # exit 1
```
- threads=1 passes for all three. Both new run tests are skipped under `TestRunMatchesNode` because they use ParallelScript features.
- threads 2 and 4 fail, for example `-par.map controllers: TypeError: Illegal constructor 583, ok 19417` and `-from .par.map: client: TypeError: Illegal constructor 36, ok 964`.
- The HTTP test fails with `"TypeError: Illegal constructor": 23, "ok": 777` at 2 threads and 28 at 4.

**Determinism:** `-count=20 -run '^(TestRun|TestHTTPServerSignalsEveryLoop)$/^(abort_signals_parallel|fetch_signals_parallel|PTS_THREADS=.)$'` exited 1 in 37 s. Every 2- and 4-thread subtest failed in 20/20 runs, and every 1-thread subtest passed 20/20. The HTTP test got 15–50 bad answers per failing run.

**Throwaway candidate fix** (`/tmp/h07-repro/candidate-fix.diff`; the binary is `/tmp/h07-repro/pts-candidate`, sha `a3a36b65…`). The flag is replaced by a module-private token:
```ts
class Permission { readonly #brand = true; }
const PERMISSION = new Permission();
function newSignal(): AbortSignal { return new AbortSignal(PERMISSION); }
constructor(permission?: Permission) { super(); if (permission !== PERMISSION) throw new TypeError("Illegal constructor"); }
```
- With it, `-count=10` over the new tests plus `abort_signals` (the Node diff), `fetch_abort`, `fetch_many` and `TestHTTPServerEveryLoop` exits 0, and every subtest passes 10/10.
- All 12 C07 reds (H01b's 3 plus my 9) now match Node; the shared-signal probes still fail (below).
- One type-level side effect: the constructor now takes an optional parameter, so `new AbortSignal(x)` is a type error with a different message than with Node's own types. `new AbortSignal()` still compiles and throws.

## Separate defect: one signal shared across threads (verified)

This one is not the flag. A signal the program only reads, but follows from many threads, races on its own listener list. `AbortSignal.any([shared])`, which every `fetch(u, { signal: shared })` does through its `Request`, calls `shared.addEventListener` from each thread.

- **Lost listeners.** With 20,000 `.par.map` followers, 23–80 lost listeners at 2 threads and 86–141 at 4 on the base. With the candidate fix it is still 21–155 and 86–1,263, so the fix does not touch it.
- **Missed aborts on real fetches.** 400 fetches share one signal and wait at a server; aborting it should reject all of them. With the candidate, 0–4 at 2 threads and 3–12 at 4 never see the abort and get the server's late answer instead.
- **Crash.** A lost push leaves a hole in the list. In one run in 10 at 4 threads, on both CLIs, the main thread's `shared.abort()` died with an uncaught `RangeError: Array index 513 is out of range (length 516)` at `EventTarget.dispatchEvent (pts:internal/abort:178)`, exit 1. The same hole can also make `addEventListener` throw.

SPEC §4.1 allows lost pushes on a shared array, and the acceptance says not to serialize user objects, so you need to decide whether this belongs in H07 or gets its own row. I drafted `pts/tests/run/abort_signal_shared_parallel.pts`, currently marked `// skip: bug: …`. With the skip removed it fails at 2 and 4 threads 5/5 and passes at 1, on both the base and the candidate.

## C. Audit of module and runtime state

Library modules:

| Where | Verdict |
|---|---|
| `internal/abort.pts:205` `let constructing`, written at :209/:213, read at :228 (C: `static bool pts_global_abort_constructing`) | **Real race (C07).** Written at run time by every new signal on any thread; verified in both directions above. |
| `abort.pts:36–39` `beginDispatch`, `endDispatch`, `stopped`, `markTrusted`; `:206` `signalAbort` | Benign. Set by static blocks, which run in `pts_module_abort()` from `main` before `pts_main`; I confirmed this in the emitted C. |
| `abort.pts:6` `DOM_EXCEPTION_CODES` Map | Benign. Built at startup and only read with `.get` afterwards; reads don't mutate it (`map.c`). |
| `http.pts:63` `addReceived`; `:204–208` `bodyOf`, `signalOf`, `disconnect` | Benign. Static blocks, set once at startup. |
| `http.pts:23` `TOKEN_CODES`, `:444` `BAD_PORTS`, `:449` `OWN_HEADERS` | Benign. Read-only after startup. |
| `http.pts:236–244`, the `Request.signal` getter, which makes its controller on first read | Per-object. Racy only if the program shares one `Request` across threads: two controllers could be made, and a client disconnect would abort only one. That is user sharing under SPEC §4.1, and I did not probe it. |
| `abort.pts:155` `#registrations`, mutated at :163, :171 and through `any` (:280), the `Request` (:231) and `exchange` (:525) | Per-object, but racy when the library adds listeners to a shared signal. This is the separate defect above. |
| `test.pts:78–82` `root`, `counts`, `failures`, `current`, `started` | **Real race** when `test`/`describe` are called from `parallel` tasks. At 4 threads the runner started 3 or more times and tests were filed under the wrong suite (`experiments/test_registry_parallel.base.out`). The registry is single-threaded by design, as Node's is. Low priority: document it or reject calls off the main loop. |
| `process.pts` `env`, `#exitCode`, and `process.c:18` `exit_code` (a plain int) | Process-wide by design; a one-word plain store, as the ARCHITECTURE §4.13 shared-memory rules allow. |
| `fs.pts:436`, `fs/promises.pts:356`, `path.pts:531`, `assert.pts:353`, `internal/fs.pts` constants, `parallel.pts` consts; `sync.pts` has none | Benign: read-only singletons and constants. |

Runtime C, all benign:

- **Set once at startup:** `error.c:186–187` (function names), `loop.c:383` (`time_origin`), `process.c:16–17` (argc/argv).
- **Set once behind `pthread_once`:** `error.c:205` (backtrace state), `regexp.c:48`, `scheduler.c:305`, and `pool.c:63` `pool_threads`, written inside the once.
- **Designed for races:** `error.c:323` `cause_site` is a one-word inline cache.
- **Under locks or atomics:** `console.c` counts, timers and group indent; `dynamic.c` bound types; `object.c` frozen twins and table (readers lock too); the `http.c` server table; the `fetch.c` request table; `pool.c` and `scheduler.c` state.
- **Atomic publish:** regex literal caches (`regexp_object.c:68`).
- **Thread-local:** `math.c`, `promise.c`, `generator.c`, `array.c` join state, `dynamic_methods.c` sort kind, `runtime.c` stack limit, `loop.c` state, the `http.c` connection table and date line, PCRE2 match data.
- **Generated code:** the `pts_global_*_ready` flags are set at startup.

## Side findings, not C07 (verified)

- **`AbortSignal.timeout` inside a `.par` callback** throws "Timers and asynchronous I/O can't be started from a parallel operation's callback yet" on pool workers. SPEC §5 documents this. Which tasks hit it varies from run to run, because the calling thread also runs some pieces (`experiments/timeout_pool.base.out`). The candidate fix doesn't change it. So timeouts in `.par` callbacks won't work even after H07; my probes and tests only use them in `parallel` tasks.
- **Listener identity.** A `() => void` listener passed where an `EventListener` goes is wrapped in a fresh adapter each time. It gets added twice and `removeEventListener` never removes it: PTS counts 8 calls where Node counts 4, even at 1 thread (`experiments/event_calls.*`). SPEC §8 documents the adapter identity behaviour.
- **Suspicion, not compared with Node:** `addEventListener` scans the whole list for duplicates, so 20,000 followers of one signal take about 1.8 s at 1 thread.

## Files

All are untracked in the worktree except `http_test.go`, which is modified:
- `docs/agent/hardening-evidence/probes/h07/`: `run_probes.py`; 15 `c07-*.pts` probes, each with its `.oracle.mts`; `oracles/` with `abort_signals_parallel.mts`, `fetch_signals_parallel.mts` and `abort_signal_shared_parallel.mts`; `experiments/` with `event_calls`, `timeout_pool` and `test_registry_parallel`, as `.pts` plus `.out`.
- `pts/tests/run/abort_signals_parallel.pts`
- `pts/tests/run/fetch_signals_parallel.pts`
- `pts/tests/run/abort_signal_shared_parallel.pts` (skipped)
- `pts/tests/http/signals_every_loop.pts`
- `pts/tests/http_test.go` (diff at `/tmp/h07-repro/http_test.go.diff`)

Everything is in `/tmp/h07-repro/h07-reproducer-probes.tar.gz` (sha256 `14c85404e95ffc24df9b3992b089d4bfc53b5514483fa86dc8f29e5bb0bf8e6e`): the files above, `candidate-fix.diff`, all runner JSON records under `results/` (base, candidate and the final runs), the go test logs under `results/gotest/`, and the helper scripts under `tools/`. `/tmp` is wiped on reboot, so archive the tarball into the repo.

## Caveats

- The final run's record says "1 not behaving as its kind says" (exit 1): one crashing run of the shared-signal probe wasn't covered by its failure pattern. I widened the pattern and re-ran both shared-signal probes (`final-shared-*`, exit 0).
- The HTTP test shows that requests fail when handlers run on several loops at once. It cannot tell a fixed build from one where handlers ran on a single loop; `TestHTTPServerEveryLoop` is what checks that work spreads over the loops.
