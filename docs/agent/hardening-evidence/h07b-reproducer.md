# H07b1 reproducer report (opus-reproducer, claude-opus-5-5, xhigh)

Returned as the child's final message on 2026-10-17 and saved verbatim by the parent (the named
subagents can't write report files). The child worked in the isolated worktree
`.claude/worktrees/agent-ac2576d660f32ee23` at `8f5439c05`, scratch `/tmp/h07b/repro/`, against the
parent's base worktree `/tmp/h07b/base`. Its archive is
[h07b-reproducer-probes.tar.gz](h07b-reproducer-probes.tar.gz) (sha256
`5f75fe1c629bd15d734838e030bcf8afc2a26719848f52a6df28f42aeb5f9709`); its probes, twins and tools are
also unpacked in [probes/h07b/](probes/h07b/). The parent's own runs of these claims are in
[h07b.md](h07b.md).

---

I reproduced every C116 facet (a-i) on the base. All four new regression tests fail there and match Node's output.

- **Base:** `/tmp/h07b/base` at `8f5439c05`, CLI sha256 `60d96b45…349ce4`; Node v24.
- **How probes were run:** `tools/runp.sh` builds with `PTS_HOME=/tmp/h07b/base /tmp/h07b/base/pts-bin/pts build <probe> -o bin/<name>` and runs `PTS_THREADS=<t> bin/<name>`. The C121 matrix used `pts run`.
- **Exit codes:** every probe exited 0. The probes catch errors and print tallies, so the failures are in the output lines below.
- **Not changed:** nothing committed, nothing in the main tree, no tasks ticked.
- **The host was loaded** (your suite was running), so timings are inflated.

## C116 facets (base; 1 thread passes every probe)

Unless noted, the probe is `probes/h07b/c116-shared-threads.pts` (twin `.oracle.mts`), run with `tools/runp.sh … 1 2 4 2 4`. Node and 1 thread print the expected line for every section.

| Facet | Expected | 2 threads | 4 threads |
|---|---|---|---|
| a. `any([shared])` in `parallel for` | aborted 10000 | 8436/1564 not aborted | 9593/407 |
| a. `any([shared])` in `parallel` tasks | aborted 10000 | 7827/2173 | 9023/977 |
| b. followers made while a thread aborts half-way (triggered by an AtomicInt count) | all aborted | 6 and 11 not aborted | 205 and 435 |
| c. the program's own 3,000 listeners from threads | called 1 3000 | `called 0 24` | `called 0 81`, `called 0 150`; once `RangeError: Array index 257 is out of range (length 265)` thrown by `addEventListener` |
| d. one controller aborted from 2 threads at once, 1,000 rounds | wrong 0 | wrong 225, 48 | wrong 158, 112 |
| e. removes racing adds | nothing lost, nothing thrown | `RangeError` from add/remove (1,308 of 3,000 in one go-test run), removed listeners called (1–6), added ones lost (226 of 1,500) | similar |
| f. `{ signal: s }` while `s` aborts half-way | called 0 everywhere | `called 1 8`; another run 4,870 `RangeError`s from `addEventListener` | `called 1 576` |
| g. `{ once: true }` on a shared EventTarget, 2 threads dispatching, 1,000 rounds | wrong 0 | wrong 668, 71 | wrong 677; an earlier 4-way version also had 19 throws |

- **`abort()` throws** in d: `RangeError: Array index 2 is out of range (length 2)` 26/2/7/11 times, from the `once` listener's removal racing. In e, `abort threw RangeError`.
- **First versions of b and f didn't hit.** They aborted at index `COUNT/2`, which is the start of a pool piece, so later callbacks just saw the signal already aborted. Triggering the abort by a half-way count fixed that.
- **b, `any`'s own check-then-add** (`c116-any-window.pts`): one thread makes `any([c.signal])`, the other aborts after a 0–8,000 spin sweep. That loses 37 of 4,000 at 2 threads and 34 at 4. With a 0–300 sweep (`MAX=300`) it loses 411 at 2 threads and 603 at 4; 1 thread loses 0.

**h. fetch**
- **Shared signal** (`c07-shared-signal-fetch-par.pts`, `RUNS=5`): 1 thread passes 5/5. 2 threads fail 4 of 5 runs (`AbortError 399, late 1`; late 4, 3, 1). 4 threads fail 5 of 5 (late 8, 9, 4, 2, 10).
- **The `exchange` window is easy to see.** One `.par` thread starts a fetch to a handler that never answers; the other aborts after a varying pause.
  - `c116-fetch-abort-window.pts`, 300 iterations: 2 threads `hung 76, stop 224`.
  - `fetch_abort_other_thread.pts`, 1,000 fetches: 2 threads `hung 291/335/329`, 4 threads `hung 315/462/337`; 1 thread `rejected with the reason 1000`.
- **Controls never hang:** aborting from the same thread right after `fetch()` gives `stop 200` in all three modes (pool, main, pool with a pause) at 1, 2 and 4 threads (`c116-fetch-abort-sequential-pool.pts`).
- **Which window it is (inferred, not instrumented):** `any`'s window alone loses about 1% at the same sweep. So most of the 25–35% comes from the gap between fetch's `throwIfAborted` and `exchange`'s `addEventListener`.

**i. `Request.signal` first read on a task while the client disconnects: hit, at a low rate.**
- Probe: `c116-request-signal-disconnect.pts`, logs `facet-i-sweep*.log`. The handler aborts the client's fetch, and a `parallel` task reads `request.signal` after a swept pause.
- **Results:**
  - `MAX_SPINS=20000`: 6 of 8 runs leave 1–3 of 3,000 signals never aborted (e.g. `requests 3000, signals aborted 2997`).
  - The misses cluster at 12,124–15,519 spins, with the signal read (`signal read true`).
  - Sweeps up to 5,000 and 50,000 spins: 0 misses in 16 runs.
  - Narrowed to 10,000–18,000: 7 of 8 runs miss 1–3.
- **Not made a test:** the window is calibrated to this machine.
- **Mechanism (inference):** the getter's store of `#controller` then load of `#gone`, against `disconnect`'s store of `#gone` then load of `#controller`.

## C119
`c119/any_cost.pts` at 1 thread, against Node:

| Followers | PTS follow | PTS abort | Node follow | Node abort |
|---|---|---|---|---|
| 10k | 1,414 ms | 10 ms | 83 ms | 0–1 ms |
| 20k | 4,348 ms | 6 ms | 61 ms | 0–1 ms |
| 40k | 21,535 ms | 23 ms | 99 ms | 0–1 ms |
| 100k | 130,346 ms | 57 ms | 169 ms | 0–1 ms |
| 150k | – | – | 310 ms | 0–1 ms |

Aborting a source with n followers is linear in PTS too; only following is quadratic.

## Retention (information only)
- **Node:** three batches of 1M dropped `any([longLived])` leave RSS flat (61.2 → 72.2 → 72.5 → 72.7 MiB). 1M kept signals add about 154 MiB.
- **PTS base:** 1M kept signals add 128.5 MiB (about 135 B each). Dropped followers of a long-lived signal grow RSS 3.5 → 7.7 → 10.1 → 13.8 MiB per 10k batch, about 250–430 B each retained. 1M isn't feasible on the base because it's quadratic.
- **What this means for the fix:** a strong O(1) dependents list will keep about 130–140 MiB per 1M dropped followers until the source aborts or is collected. Node keeps about zero.

## C121 (`c121/order_matrix.{mts,pts}`, Node outputs checked)
The base differs from Node in 6 of 9 sections:
- order
- flattening (PTS: outer, then inner, then a's listener)
- the source's `onabort` against followers
- abort during dispatch: `e` gets the reason `from a's listener` where Node keeps `a's reason`
- a follower's listener aborting another source: `g.reason` is `b from d` where Node has `a`
- timeout as a source (order)

So the bug changes reasons, not only order. Duplicates `any([inner, a, inner])` and `any([a, a])`, two sources aborting in turn, already-aborted inputs, reason identity, and `onabort`'s place among a follower's listeners already match Node; I kept them as guards.

## Regression tests (uncommitted in my worktree, no skip lines)
- `pts/tests/run/abort_signal_shared_threads.pts` (1/2/4 threads): facets a (`parallel for`, tasks), b, b's window, c, d, e, f, g.
- `pts/tests/run/fetch_abort_other_thread.pts` (1/2/4 threads): the `exchange` window, plus 200 `.par.map` fetches sharing one signal.
- `pts/tests/run/abort_signal_any_cost.pts`: 100k followers plus 25k followers of a follower; Node takes 0.44 s.
- `pts/tests/run/abort_signal_any_dependents.pts`: the C121 matrix; plain TypeScript, so it also runs under `TestRunMatchesNode`.

Every expect block equals its Node twin's output; the twins are in `probes/h07b/oracles/`.

**RED run:** `GOMAXPROCS=2 go test -count=3 -timeout 40m -v -run '^(TestRun|TestRunMatchesNode)$/^(abort_signal_shared_threads|abort_signal_any_cost|abort_signal_any_dependents|fetch_abort_other_thread)$' ./pts/tests/` exited 1 after 147.9 s (`logs/gotest-red-base.log`). In each of the 3 runs:
- shared_threads and fetch fail at 2 and 4 threads and pass at 1. Example: `-a follower against an abort a moment later: aborted 1774, not aborted 226`, `-once listener, two threads dispatching: rounds 1000, wrong 560`, `-aborted on another thread as it starts: hung 304, rejected with the reason 696`, `-sharing one signal: hung 5`.
- any_cost fails under both tests: killed at the 30 s limit with empty stdout.
- dependents fails under both tests: `-a listener 1; d1 false d2 false`.

## Outside this scope
Nothing new. Two compile errors I hit while writing probes are existing limits: assigning `resolve` to a `() => void` is PTS1001 (callback arity), and `parallel f(x).then(...)` takes the whole chain as its operand, as the spec says.

## Caveats
- **Post-fix runtimes are estimates;** I had no fixed build. Shared_threads at 1 thread should be about 0.3–0.5 s (1.7–1.9 s on the base, mostly the quadratic `any`). Fetch takes 0.11 s at 1 thread.
- **C119's RED is the 30 s timeout.** Unloaded, the 100k part alone would take about 47 s (extrapolated from H07's 7.6 s at 40k), a 1.6× margin. Under today's load it took 130 s.
- **Spin calibration varies by machine.** The fetch and b-window sweeps are broad, but a CI machine will hit them at a different rate. When fixed they are deterministic.
- **Overlap with the committed skipped tests:** `abort_signal_shared_parallel.pts` covers the `.par.map` case, and `abort_signal_any_order.pts` is a subset of the dependents test. Whether to keep, unskip or drop them is your call.

Files:
- Tests: `/home/bryan/projects/parallelscript/.claude/worktrees/agent-ac2576d660f32ee23/pts/tests/run/{abort_signal_shared_threads,fetch_abort_other_thread,abort_signal_any_cost,abort_signal_any_dependents}.pts`
- Probes, twins and tools: `/home/bryan/projects/parallelscript/.claude/worktrees/agent-ac2576d660f32ee23/docs/agent/hardening-evidence/probes/h07b/`
- Archive: `/tmp/h07b/repro/h07b-reproducer-probes.tar.gz` (sha256 `5f75fe1c629bd15d734838e030bcf8afc2a26719848f52a6df28f42aeb5f9709`, no binaries). It holds the tests, probes, oracles, logs and `cmds.txt`. `/tmp/h07b/repro/DONE` is touched.
