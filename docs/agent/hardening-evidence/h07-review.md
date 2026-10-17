# H07a review (opus-reviewer, claude-opus-5-5, xhigh)

## Round 1

Returned as the child's final message on 2026-10-17 and saved verbatim by the parent (the named
subagents can't write report files). The reviewer read the main tree's uncommitted change on top
of `0ddc0a60f` and the base worktree `/tmp/h07/base`, read-only; its probes are
[h07-review-probes.tar.gz](h07-review-probes.tar.gz) (sha256
`eea62c7937c2f9401c5642a9ebbd17117b1ac3d182f7ed390ebfe51a009895f4`). The parent's handling of each
finding is in [h07.md](h07.md).

---

**Verdict: changes needed, but only in the slice's records and tests. The fix in `pts/lib/internal/abort.pts` is correct and I found no regression.** Two things block calling H07a done: an `AbortSignal.timeout` gap on task loops that sits inside H07a's acceptance but is tracked nowhere, and C119's rejection, which rests on the wrong comparison for `AbortSignal.any`.

### Verified: the fix

- **No construction state is left.** `const SIGNAL_KEY = {}` (abort.pts:207) is checked at :223 and passed at :253, :260, :267 and :294. The generated C stores it in a static global during `pts_module_abort()`, which `main` runs before `pts_main()` (emitted C lines 1212–1213 and 1267).
- **The key is unique and safe from the collector.**
  - It is a fresh `pts_object_new` allocation. cgen's only shared statics are `PTS_STATIC_STRING`.
  - `!==` on objects compares pointers (`string.c:470-494`).
  - The global sits in the executable's data segment, which bdwgc scans by default.
  - Stress probe `p5/gc_key.pts` (1 MB heap, 8M `{}` literals passed to the constructor at 1 and 4 threads) printed `forged 0, made 400000` every run.
- **No program can forge a signal.**
  - Importing or re-exporting `pts:internal/abort` is PTS1002.
  - A class as a value, `new` through `any` and dynamic `import()` are all PTS1001, so there is no reflective route.
  - Subclasses with explicit or implicit constructors, `super(x)` and arguments under `@ts-ignore` all throw `TypeError: Illegal constructor`, as Node does (`p1/forge_typed.pts`, `p1/subclass.pts`).
  - `console.log` of signals, `instanceof` and the error's name and message match Node. The only difference is Node's `code: 'ERR_ILLEGAL_CONSTRUCTOR'`, which the base lacks too.
- **One visible type change, and D-184 records it.** `new AbortSignal({})` now compiles; the base gives TS2554, matching Node's `new(): AbortSignal` (`p1/typed_arg.pts`). The reproducer's private-class key would instead turn `@ts-ignore`'d literal arguments into PTS1001, which supports choosing `unknown`.
- **The audit holds up on spot checks.**
  - The library's module-level `let`s are only static-block slots (pts:internal/abort, pts:http) plus pts:test's registry (C117).
  - `Map.get` only reads, using atomics (`map.c:184-293`).
  - libbacktrace state is created with `threaded=1` (`error.c:212`).
  - The server's disconnect callback runs on the connection's own loop (`http.c:484-494`, :596, :844).
  - A fetch started on a pool thread is posted to its operation's loop (`fetch.c:597-611`). The callback's `addEventListener` therefore happens before the loop's `removeEventListener`.
- **Every red probe now matches Node.** The 3 H01b C07 probes matched 10/10 and the 11 H07 probes 5/5, each at 1, 2 and 4 threads.
- **My own probes pass on the fix and fail on the base at 2 and 4 threads:**
  - fired timeouts in 8 tasks, each with a ref'd timer: `ok 4808`, 10 of 10 runs (`p2/timeouts_fire_tasks.pts`)
  - `request.signal` aborts on client disconnect and on `stop(true)` on every loop: 64 of 64 (`p3/*`)
  - `AbortSignal.timeout` firing in reusePort handlers: 256 of 256 (`p3/handler_timeout.pts`)
  - fetches aborted mid-flight from `.par.map` and from tasks, equal to Node (`p4/fetch_abort_parallel.pts`)
  - a throwing listener inside `.par` callbacks is reported as uncaught with exit 1, as in Node (`p2/listener_throws_par.pts`)

### Verified: the tests

- **They fail on the base for the right reason.** In `/tmp/h07/base`, `-count=3`: every 2- and 4-thread subtest failed 3 of 3 with "Illegal constructor" (for example `"TypeError: Illegal constructor": 32`), and every 1-thread subtest passed (`base_new.log`).
- **They pass with the fix:** 5 of 5 in `fix_new.log`, and 4 of 4 while another CPU-heavy job ran (`fix_new_load.log`). Each run takes about 0.15–0.35 s.
- **The expect blocks equal Node's output.** I ran all three twins in `oracles/` and they matched.
- **Nothing regressed:**
  - all `abort_signals`, `fetch_*` and `TestRunMatchesNode` cases passed (`reg_run.log`)
  - `TestHTTPServer`, `TestHTTPServerEveryLoop`, `TestHTTPServerLifecycle` and `TestFetchFromGoServer` passed (`fix_http.log`)
  - `gofmt` and `go vet` are clean
- **The skipped C116 test is still red with the fix:** 9,850–9,978 of 10,000 followers aborted at 2 and 4 threads.

### Findings

**F1 — Medium, existed before the fix, in H07a's scope but tracked nowhere.**
- A `parallel` task that waits for its own `AbortSignal.timeout` never sees it fire at 2 or 4 threads. This is D-121's known gap (DECISIONS.md:1011, "a loop that sleeps doesn't run its unreferenced timers"), and `timeout` timers are unref'd.
- Repro: `p6/timeout_in_task_wait.pts`, with its Node twin `.mts`.
  - Node and 1 thread print `TimeoutError,…` 8 times.
  - At 2 and 4 threads, 4 of 4 runs print `keep-alive fired: some task's timeout never did`, then `Warning: Detected unsettled top-level await`. The base behaves the same.
- A timeout signal made in a task and handed to the main loop sometimes never fires either: `aborted: ynnynnyn` (`p6/timeout_handoff2.pts`).
- It is missing from PROGRESS known issues and from H07a and H07b. The new tests only construct 1-hour timeouts, so they can't catch it.
- Needed: fix it, or add a known issue, a skipped RED test (this probe) and a task row.

**F2 — Low–Medium: C119's rejection is wrong for `AbortSignal.any`.**

| 40,000 followers of one signal, 1 thread | Node | PTS |
|---|---|---|
| `AbortSignal.any([shared])` | 72 ms (linear: its followers are a Set) | 14,517 ms (quadratic) |
| `new Request(url, { signal })` | 18.4 s | about Node's speed (10k: 663 ms) |
| plain `addEventListener` | 8.0 s | 12.5 s |

- So Node is quadratic for `Request` and for plain listeners, but not for `any`. That is roughly 200× at 40k (`c119/*`).
- Suggest reclassifying it to H07b, which rebuilds `any`'s follower list anyway.

**F3 — Low, new and existing before the fix: `AbortSignal.any` aborts in the wrong order compared with the DOM spec and Node.**
- In PTS a follower aborts as an ordinary listener on its source.
- So its listeners fire before the source's later listeners, and a source listener added before the follower sees `aborted: false`. In Node, followers' reasons are set before the source's event and their events fire after it.
- Repro: `side/any_order.pts`.
  - Node: `source's listener; follower aborted: true` comes first, and `follower2 aborted: true`.
  - PTS (fix and base alike): `follower's listener` comes first, and `follower2 aborted: false`.
- Route to H07b or H29.

**F4 — Low test gap: no test passes an argument to `new AbortSignal(...)`.**
- The only calls in the tests are `new AbortSignal()` (abort_signals.pts:28 and abort_signals_parallel.pts:72).
- A broken check such as `key === undefined` would pass every test, by inspection; I didn't build a mutant.
- Suggest adding `new AbortSignal({})`, `new AbortSignal(undefined)` and a subclass calling `super(x)` to `run/abort_signals.pts`, which is compared against Node.

**F5 — Low coverage gaps.**
- No regression test makes a timeout actually fire on task or handler loops.
- None covers disconnect or `stop(true)` aborts on several loops.
- Probes `p2/timeouts_fire_tasks.pts` and `p3/*` are cheap candidates.

**F6 — Information for H29: the library itself has a C118 case.**
- `exchange`'s `onAbort` (http.pts:514) is typed `() => void`. The generated C wraps it in a new adapter at both the remove (:517) and the add (:525), so the listener is never removed.
- It is harmless today, because `fetch.c` ignores a stale request id. Typing it as `EventListener` would fix it.

**F7 — Suspicion only: ThreadSanitizer finds nothing here.** A race I planted in a generated program (`tsan/tsan_sanity.pts`) wasn't reported (`races=0`). So I couldn't use TSan as evidence for this slice. The project's "TSan clean" claims may also be blind for generated code; I didn't investigate further.

**F8 — Doc nits.**
- `h07-reproducer.md` links to `h07.md`, which doesn't exist yet.
- The `experiments/*.out` files are gitignored, but they are inside `h07-reproducer-probes.tar.gz`, so nothing is lost.

### The split

H07a and H07b together cover H07's original text. C116 and C117 in H07b and C118 in H29 are sound. Only F1 (the timeout gap) and C119's `any` half fall outside both slices.

Probes: `/tmp/h07/review1/`, archived in `/tmp/h07/review1/h07-review-probes.tar.gz` (sha256 `eea62c7937c2f9401c5642a9ebbd17117b1ac3d182f7ed390ebfe51a009895f4`). It holds the sources, the Node twins, the Python clients, the logs and `cmds.txt`; compiled binaries are left out.

## Round 2

The same reviewer, resumed with round 1's context, reviewed the parent's changes for round 1's findings (still read-only). Saved verbatim from its final message; probes [h07-review2-probes.tar.gz](h07-review2-probes.tar.gz) (sha256 `d1d1e4b2cadcb1b3771abf9b0bdd81fabf27378ab881b883fe250dd10b3f5766`).

---

**Round 2 verdict: ready.** Every round-1 finding is handled correctly, the new tests are correct and deterministic, and their expect blocks equal Node. One thing must happen before the commit: add `docs/agent/hardening-evidence/h07-parent-probes.tar.gz`. It doesn't exist yet, but four records point to it. `abort.pts` is unchanged since round 1 (mtime 16:43:30), and the CLI is still sha `858f8927…2a95`.

### Verified

- **F4, `run/abort_signals.pts`:**
  - The fix's output equals Node's.
  - The mutant CLI (`/tmp/h07/mutant`, `key === undefined`) prints `constructed` 4 times where Node prints `TypeError Illegal constructor`: for `{}`, a signal, the `any` value and `Forged({})`.
  - The base fails with TS2554, as the old constructor took no arguments.
  - `TestRunMatchesNode/abort_signals` passed 5 of 5.
- **F5, `run/abort_signals_parallel.pts`:**
  - The regenerated twin prints the expect block exactly, including `fired timeouts in tasks: ok 804`, and its diff against the test is only the expected substitutions.
  - The new section can't hang or race: each task's 10 s ref'd keep-alive bounds it, and `onabort` is registered before each promise's listener.
- **F5, `TestHTTPServerSignalsEveryLoop` with its `/timeout`, `/hold` and `/count` routes:**
  - Waits are bounded by `eventually` (5 s), and stderr stays empty.
  - I drove the server myself with `http/client.py`. On the fix, 1 and 4 threads gave `ok 800`, `TimeoutError 64`, `16 0` then `16 16`.
  - On the base at 4 threads, only the `/` section fails (11–12 "Illegal constructor" of 800); `/timeout` and `/hold` pass. So the new routes are coverage, not RED tests for C07, which is what `h07.md` RED §3 says.
- **Focused runs:**
  - Main tree, `-count=5`, exit 0 (`fix_r2.log`): every subtest passed 5 of 5 at 1, 2 and 4 threads, taking 0.18–0.45 s. The three H07b tests are skipped.
  - Base, `-count=2`, exit 1 (`base_r2.log`): the 2- and 4-thread subtests fail with "Illegal constructor" (including `constructed 87–243` in the mixed case), `abort_signals` fails with TS2554, and 1 thread passes.
  - `gofmt` and `go vet` are clean.
- **Skipped RED tests fail for the reasons their skip lines give:**
  - `abort_timeouts_sleeping_loops.pts` (C120): Node prints the expect block. The fix passes at 1 thread 5 of 5; at 2 and 4 threads, 5 of 5 runs print `a task's timeout never fired` and `Warning: Detected unsettled top-level await`, exit 13. The skip line is accurate. The handed-back half is only reached once the first half works.
  - `abort_signal_any_order.pts` (C121): fix and base agree with each other and differ from Node exactly as the skip line says. It has no expect block, so it is compared with Node; `isPlainTypeScript` holds.
  - `abort_signal_shared_parallel.pts` (C116): still red, as in round 1.
- **Records:** the H07a and H07b plan rows, C07, C116–C122, D-184, SPEC §9.126, `h07.md`, PROGRESS and the ledger summary all match what I re-ran. `h07-review.md` is my round-1 report verbatim, and the round-1 tarball's sha256 is unchanged (`eea62c79…95f4`). The routing (C119, C120, C121 to H07b; C118 with F6 to H29; C122 to H20) is sound, and nothing in H07's scope is left untracked. Timers started in `.par` callbacks are documented in SPEC §415.

### Remaining notes (none of them block)

1. **Must do before committing:** `h07-parent-probes.tar.gz` is referenced in CONSOLIDATED C119, `h07.md` (twice) and PROGRESS, but the file is missing.
2. **Low: a regression of the `/timeout` route would hang the suite instead of failing.** `fresh` (http_test.go:273) has no `Timeout`, and `getAll` (:206) calls `client.Get` with no deadline. If handler timeouts stopped firing, line 276 would block until go test's `-timeout`. Giving that client `Timeout: 10 * time.Second` would make it fail within seconds with a message.
3. **Information for C120: the gap is general.** A plain `setTimeout(...).unref()` in a `parallel` task never fires at 2 or 4 threads either (3 of 3 runs, exit 13; Node and 1 thread print `fired` 4 times; probe `c120/unref_task.pts`). The row's acceptance already covers any unreferenced timer, but a plain-timer case in H07b's regression would keep the fix aimed at the scheduler rather than the signals.
4. **Wording nit in D-184 and PROGRESS:** they call pts:test's registry "state the program shares". It is library module state that independent calls change, not something the program shares.

Probes are in `/tmp/h07/review2/`, archived in `/tmp/h07/review2/h07-review2-probes.tar.gz` (sha256 `d1d1e4b2cadcb1b3771abf9b0bdd81fabf27378ab881b883fe250dd10b3f5766`). It holds the sources, the Node twins, the HTTP client, the outputs, the logs and `cmds.txt`.
