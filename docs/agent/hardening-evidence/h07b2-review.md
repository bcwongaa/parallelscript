# H07b2 review log (opus-reviewer, claude-opus-5-5, xhigh)

Each round's report was returned as the child's final message and saved verbatim by the parent
(the named subagents can't write report files). The reviewer was read-only: it reviewed the main
tree's uncommitted change and the clean base worktree `/tmp/h07b2/base` (`ec24125a8`). The parent's
handling of each finding is in [h07b2.md](h07b2.md).

## Round 1 (2026-10-17)

Reviewed diff sha256 `2bae56d80fd6b06a5346d0d1ff478ed90d1ad79bce5a37f6d47afb58ad20b219` (CLI sha256
`25d60c3e…a3f3`). Probes: [h07b2-review-probes.tar.gz](h07b2-review-probes.tar.gz) (sha256
`1faa6a7185cdf822328713e61d4d649eae02c9e8bfef8d32c197eade9759bb3d`).

---

No blockers or majors. The sleeping/waking protocol held up under analysis and every stress probe, C117's check has no false rejections, and the new tests pass on the fix and fail on the base as designed. I found two minor timer-ordering divergences, one of them a timing regression caused by Finding A, plus doc and quality nits and three issues that existed before this change. Probe archive: `/tmp/h07b2/review1/h07b2-review-probes.tar.gz` (sha256 `1faa6a71…bb3d`, 677 text files, no binaries). Probes are built with `/tmp/h07b2/review1/tools/build.sh fix|base <abs .pts>`, which wraps `PTS_HOME=… pts build x.pts -o bin/<which>/x`.

### Findings

**1. Minor: intervals still restart from the batch's stale time, and Finding A makes that visible (regression).**
- **Cause:** `on_timer` (loop.c:219) reschedules a repeating timer from `now`, the time read when the batch started. Node takes `start = binding.getLibuvNow()` just before each interval's callback and reschedules from that; I checked this in Node 24's `internal/timers` source.
- **What changed:** before, the stale loop time also delayed the next poll, so the gap matched Node by accident. Now that `setTimeout` reads the time afresh, an interval that shares a batch with a 50 ms callback and sets any timer fires its next tick at once.
- **Probe:** `probes/order/interval_after_long_callback_settimeout.{pts,mts}`, run with the binary directly or `node --no-warnings x.mts`.
- **Results:**

  | | "gap between the first two ticks" |
  |---|---|
  | Fix | 0 ms in 19 of 20 runs |
  | Base | about 10 ms in 20 of 20 runs |
  | Node | about 10 ms in 10 of 10 runs |

- **Already wrong on the base:** the matrix scenario "interval after a long callback, with a timer set" gives Node `I1,T,I2` but `I1,I2,T` on both fix and base.
- **Suggested fix:** for repeating timers, take `now_of(s)` just before `run_callback` and reschedule from it. Update the wording of D-114 (2) and SPEC.md:350 ("due its delay after it is set").

**2. Minor: on a sleeping loop, a timer callback's immediate runs after a timer it set.**
- **Cause:** timers fired by `pts_loop_run_due_timers` run outside `uv_run`. `run()` then re-enters libuv with `UV_RUN_DEFAULT`, whose first step runs due timers before the check phase.
- **Effect:** a callback that does `setTimeout(T, 1); setImmediate(I);` and then works 5 ms prints `timeout,immediate` at 2 and 4 threads. 1 thread and Node print `immediate,timeout`.
- **Probe:** `probes/order/immediate_in_unref_cb.{pts,mts}`.
- **Results:**
  - Fix: 1 thread `immediate,timeout` 3/3; 2 and 4 threads `timeout,immediate` 3/3 each.
  - Node: `immediate,timeout` 3/3.
  - Without the 5 ms of work (`immediate_in_unref_cb_nospin`): correct in 40 of 40 runs at 4 threads.
- **Already on the base:** stolen tasks and inbox batches were already followed by `UV_RUN_DEFAULT`, so the same ordering applies there.
- **Suggested fix (unverified):** after running due timers outside libuv, do one `uv_run(UV_RUN_NOWAIT)` pass first (no leading timers run), or loop on `UV_RUN_ONCE`.

**3. Minor (docs): registration is not rejected "wherever it runs".**
- **Probe:** `probes/c117/reject_outcomes.pts`. At 1 thread, a task's code after an `await` and a reaction queued by a task are both accepted (4 each). At every thread count, an async `.par` callback's code after an `await` is accepted (8). The registered tests then run: 16 times at 1 thread, 8 at 2 and 4.
- **Docs:** SPEC.md:577 and :623, GUIDE and ARCHITECTURE.md:417 say registration throws in tasks and callbacks wherever they run. They don't mention this gap, and the async `.par` case isn't mentioned anywhere.
- **Impact:** these cases all run on the main thread, so they are race-free. It is only a documentation issue.
- **Suggested fix:** state it in D-186's known gaps and the user docs ("in a task's or callback's synchronous code").

**4. Nit: the error message and its coverage.**
- `it()` reports `test()` and `suite()` reports `describe()`.
- No test pins the message text (grep finds it only in `test.pts`), although C117's acceptance asks for a clear error. Pin it once, for example in the Go test.

**5. Nit: stale comment.** `pts/cgen/cgen.go:171` says "timers count from the program's start (D-114)", which no longer holds. D-186 should say that it supersedes D-114 (2)'s "after the loop's cached time (`uv_now`)".

### Issues that predate this change
- **Leak when timers are refused on pool threads (minor, verified).** `pts_loop_init` allocates its uncollectable state before `pts_uv_loop()` throws.
  - Probe `probes/leak/refused_timer_leak_160.pts` at 4 threads: 292,752 refusals grow RSS by 97 MB. `thrown_error_160.pts` (3.2 M plain throws) grows it by 0 MB.
  - Base and fix grow the same (20 MB each over 40 rounds), and `timers_par_callbacks` exercises this path.
  - Fix: call `pts_uv_loop()` first. Measured about 330 B per refusal; how that splits up is unverified.
- **Node's per-duration timer lists.** Matrix scenario "Node's lists": Node `A,B,C`, fix `A,C,B`, base `C,A,B`. D-114's claim that the heap gives Node's order wherever Node's is deterministic doesn't hold here.
- **Suspicion, not reproduced.** `pts_http_listen` called from a `.par` callback on a loop thread that is helping another loop records the operation's loop (`(pts_current_loop + i) % …`), but `start_listening` opens the handle on the thread's own loop. This is the same class of bug as Finding B, outside this slice.
- **Bookkeeping.** The main-thread late-registration rerun is now documented in SPEC but has no ledger row. `/tmp/h07b2/red/scheduler-test-base.txt` is from an older draft of the C test (`keeper_fired`). I re-ran the current test on the base: it fails on exactly `unref_fired==1`, `unref_fired_by_main==1` and `ticks_by_main>=3` under both sanitizers (`out/ctest-base.txt`).

### Checked and fine
- **Sleeping/waking protocol.** By analysis: the count can't leak or reach 0 early, a timed wake and a message can't both raise it, and no loop runs program code once the program has decided to end. Stress (`probes/stress/pingpong_held`, `pingpong_with_unref`, 40 runs each at 2, 4 and 8 threads) had 0 failures, 0 hangs and a 78 ms maximum.
  - `unref_cb_spawns`: tasks, immediates and promises started from unref'd callbacks on sleeping loops all finish and the program ends, at 1, 2, 4 and 8 threads.
  - `ref_toggle_interval` matches Node at 1, 2 and 4 threads.
  - Uncaught errors and rejections from such callbacks exit 1 with the message, as Node does.
- **No busy spin.** This host's coarse clock is 1 ms, so libuv uses it; waits are always at least 1 ms.
  - `cpu_unref_interval` at 4 threads: 5,520 ticks in 0.03 s CPU on the fix, against 4,425 ticks in 0.31 s for Node and 0 ticks on the base.
  - strace: 1,387 timed-out futex waits for 4,167 ticks.
  - Idle loops and loops with only a 10-minute timer use 0.00 s CPU, the same as the base.
  - Task-heavy benchmarks (`probes/perf`) are within noise.
- **Finding A.** On the 11-scenario ordering matrix the fix matches Node on 9 (the base on 6), stable over 3 runs each. The 22 existing timer, abort, fetch and immediate run tests pass twice each at their thread counts.
- **Finding B.** `own_index` is right for the main thread, loop threads and helping loops. Pool threads never reach it, because `own_slot` throws first. `timers_par_callbacks` segfaults on the base at 4 threads (exit 139) and passes on the fix.
- **C117.**
  - No false rejections: `probes/c117/legit_outcomes.pts` accepts all 16 main-thread contexts at 1, 2 and 4 threads. That includes registrations after a `.par.map`, a join, nested operations or a task that threw, which shows the nesting count is restored.
  - Every pts:parallel entry goes through `operate`.
  - Generator bodies and the synchronous part of async functions in tasks, tasks started by tasks (including ones placed on the main loop), and tasks' `.par` callbacks are all rejected.
  - `pts test` shows a clear error and fails the file.
- **Tests.**
  - All new tests pass on the fix: 11 sequential runs per thread count, 3 rounds of 6 concurrent copies, and up to 8 copies plus 4 CPU burners pinned to 2 CPUs.
  - Harness in a copy of the tree: 5 runs of TestRun, plus TestRunMatchesNode for `timers_count_from_when_set`. The Go test passed 3 times and the C test 4 times under ASan/UBSan and TSan.
  - On the base they fail as designed; `program_end` is labelled as a guard and passes there.
  - My own Node twins (`tools/twin.py`) match the expect blocks 3/3 for 5 tests.
  - `timers_unref_sleeping_loops` depends on two timing windows of about 260 ms (refresh, clear) but never flaked even under that contention.
- **Code style** matches the surrounding C and library files.

Copies of the base and fix trees used for the Go and C tests are in `/tmp/h07b2/review1/copies/` (about 1 GB, safe to delete).

## Round 2 (2026-10-18, resumed with SendMessage)

Reviewed diff sha256 `4bbd1dbc0c0db94bcd50ed136502480e8c4a30296e22ebd9e66dcdfd52b943d5` (CLI sha256
`731f62fa…0159`). Probes: [h07b2-review2-probes.tar.gz](h07b2-review2-probes.tar.gz) (sha256
`7d1355b4ef28bc48acdfabcdade76203232bd86a96ebdc34b5452c6db87092a7`).

---

One minor regression remains from the round-2 `run()` change; everything else from round 1 is fixed. Archive: `/tmp/h07b2/review2/h07b2-review2-probes.tar.gz` (sha256 `7d1355b4…92a7`, 579 text files, no binaries). The code stayed at `4bbd1dbc…` throughout; only `DECISIONS.md`, `HARDENING_PLAN.md` and `CONSOLIDATED.md` changed while I worked, and I left nothing in the main tree.

### Findings

**1. Minor, a regression from the base: the new `run()` can still run a timer before an immediate that Node runs first.**
- **Cause:** `UV_RUN_NOWAIT` ends with a timers phase. It is followed straight away by `pts_loop_run_due_timers()` and then by `UV_RUN_DEFAULT`'s first step, which also runs timers. Up to three timer runs happen with no check phase between them. A timer callback in the first of them that sets a short timer and an immediate, then works past the short delay, gets its timer before its immediate.
- **Probe:** `probes/order2/immediate_after_task_timer.{pts,mts}`. A `parallel` task sets a referenced 1 ms timer and works 3 ms. That timer's callback sets a 1 ms timer and an immediate, then works 5 ms.

  | | Output |
  |---|---|
  | Fix | `timeout,immediate`, 10/10 at 1, 2 and 4 threads |
  | Base | `immediate,timeout`, 10/10 at each |
  | Node | `immediate,timeout`, 5/5 |

- **Second case:** `probes/order2/immediate_after_stolen_task.pts`. An unref'd timer comes due while the loop runs tasks from its inbox or tasks it took from another loop. The fix gives `timeout,immediate` 10/10 at 1 to 3 threads and 2/10 at 4. Node's phase order guarantees the immediate first; the sequential twin isn't a usable oracle here, because Node exits with nothing referenced.
- **Suggested fix, tested in a scratch copy of the runtime** (main CLI with `PTS_HOME` pointing at the copy; diff in `out/experiment-run-fix.diff`):
  - Call `uv_run(UV_RUN_DEFAULT)` once, before the `for` loop.
  - In the loop, call `if (uv_run(slot->uv, UV_RUN_NOWAIT) == 0) pts_loop_run_due_timers();`. This is safe because a pending immediate keeps the loop alive.
  - Then loop on `while (uv_run(slot->uv, UV_RUN_ONCE) != 0) {}`, which is `DEFAULT` without its leading timers phase.
- **Results with that fix:**
  - All four ordering probes print `immediate,timeout` 10/10 at 1, 2 and 4 threads.
  - The ordering matrix still matches Node on 10 of 11, and the interval probes are unchanged.
  - The 29 existing timer, abort, fetch and serve run tests pass twice at their thread counts.
  - The new tests pass 3/3 at 1, 2 and 4 threads, and the stress probes had 0 failures in 120 runs.
  - Not run with it: the C test and the full suite.
- **Docs either way:** D-186's "one pass … runs the immediates that this work queued before the next timers" and SPEC §9.128 need adjusting.

**2. Minor, predates this change: a long interval callback delays the next tick by the full period.** A 10 ms interval whose callback works 25 ms ticks again after 35 ms on both fix and base (5/5); Node ticks after 25 ms (5/5).
- **Cause:** `arm()` computes the wait from the cached `uv_now`, so the poll waits the full delay from the real time.
- **Experiment:** reading the time afresh in `arm()` (`now_of(s)`, diff in `out/experiment-arm-fresh-time.diff`) makes all 6 scenarios in `probes/interval/interval_semantics` match Node. The matrix stays at 10/11 and the 29 existing tests pass. This is optional; it could also go to the ledger.

**3. Nit (test): `serve_par_callbacks` fails on the base only some of the time.** The base hung in 2 of 6 direct runs at 4 threads (the parent saw 3/5 and 5/8). A fix run takes 40–115 ms. My estimate is that 10–20 rounds instead of 3 would make the base hang almost every time (about 97% at 20 rounds).

**4. Nit (docs):**
- D-186's Reason says registering this way "doesn't pass with one thread and throw with four". Its own known gap contradicts that: code after an `await` in a task registers at 1 thread and throws at 2 and 4 (`reject_outcomes`: accepted 4 at 1 thread, rejected 4 at 2 and 4).
- README:628 still says registration throws "in a `parallel` task or a `.par` callback", without "before its first `await`".
- D-186 says "a libuv turn starts with timers". Only `UV_RUN_DEFAULT`'s first step does; libuv 1.53's turns run timers after poll and check.

**5. Predates this change, for the ledger:** `refresh()` on a `setTimeout` inside its own callback. Fix and base fire once; Node fires 3 times, because it reactivates a fired timer (`probes/interval/timeout_refresh_in_callback`).

### Checked and fine
- **F1 (interval restart):** `interval_after_long_callback_settimeout` gives "about 10 ms" 20/20 and the plain version 10/10; the matrix matches Node on 10 of 11. On 5 runs each, PTS agrees with Node for: clearInterval and refresh inside the callback, two intervals with a long first callback, an interval beside a timeout of the same delay, and a clearInterval from a timeout. A throwing unref'd interval on a sleeping loop exits 1 with the message at 1, 2 and 4 threads, as Node does.
- **F2 (immediate after an off-libuv timer):** `immediate_in_unref_cb` prints `immediate,timeout` 10/10 and `_nospin` 20/20, at 1, 2 and 4 threads.
- **`NOWAIT` can't block:** in libuv 1.53 only `DEFAULT`, or `ONCE` when it can sleep, computes a poll timeout; `NOWAIT` polls with 0 and returns at once when the loop isn't alive.
- **Messages aren't delayed:** a message posted early, such as a refresh, wakes the loop before the timer is due. Only a message arriving at the same instant as a due timer runs after it, and that ordering across threads is racy anyway.
- **Cost:** `many_tasks` and `pingpong` at 4 and 8 threads are within noise (at most 4%). The `epoll_pwait` count is unchanged (19,993 against 19,992).
- **Leak fix:** `timers_refused_no_leak` fails on the base in 6 of 6 runs (61–97 MB) and passes on the fix 6/6. Actual growth on the fix is 0.3–1.0 MB at 2, 4 and 8 threads, and it passes under contention 8/8.
- **http:**
  - `serve_par_callbacks` passes on the fix in 12 direct runs, 12 runs under contention and two harness runs at each thread count.
  - reusePort servers started from loops that are helping another loop's operation (`probes/http/reuseport_par_callbacks.pts`): every fetch is answered, a stop from another loop's task and from the main thread works, fetches after `stop()` are refused, and the program ends at 1, 2 and 4 threads.
  - The base passes that probe too, because its modulo still maps onto every loop; its wrong-thread close of the first listener isn't observable there.
- **`it` and `suite`:** I compared `probes/itsuite/aliases.test.pts` with a twin written with `test`/`describe`. It covers skip as `true` or a reason, todo, dynamic skip, options without a function, a nested skipped suite, a throwing body, a failing `before` and a todo suite. Output, `test at` locations, stacks and exit code are identical apart from the file name. They also match the base, where `it` and `suite` were plain aliases, and look right under `pts test`.
- **C117:** all 16 main-thread contexts are accepted and none rejected, at 1, 2 and 4 threads. Rejections are unchanged and match D-186's known gaps.
- **Stress and CPU:**
  - The ping-pong probes had 0 failures in 180 runs; one 224 ms run was host noise (60 reruns, maximum 16 ms).
  - `unref_cb_spawns` is fine at 1, 2, 4 and 8 threads, and `ref_toggle_interval` matches Node.
  - The unref'd intervals use 0.10 s CPU for 5,518 ticks.
- **Full copy at round 2:** the C test passed 3 times under ASan/UBSan and TSan. The harness passed twice for the 14 new and timer tests, and `cmd/pts` `TestTest*` passed twice. The 29 existing timer-related run tests pass twice on the fix.

## Round 3 (2026-10-18, resumed with SendMessage)

Reviewed diff sha256 `23cc59cd6e4f65832a038022e9e870feba0bdf5ea436ccedf9ae585ae594c433` (CLI sha256
`731f62fa…0159`). Probes: [h07b2-review3-probes.tar.gz](h07b2-review3-probes.tar.gz).

---

Both round-2 fixes are in and working, with no blockers, majors or regressions. The runtime is my round-2 experiment copy apart from comments. Archive: `/tmp/h07b2/review3/h07b2-review3-probes.tar.gz` (sha256 `c3d57e84…8872`, 431 text files). The code stayed at `23cc59cd…` throughout; only `PROGRESS.md` changed while I worked, and I left nothing in the main tree.

### Findings
**1. Minor (cost): `arm()`'s fresh clock adds about 4% on timer-heavy code.** Probe `probes/perf/bulk_small.pts` sets and clears timers in bulk, then fires 10 k; callgrind counts:

| Runtime | Instructions |
|---|---|
| Base | 64.36 M |
| Round 2 plus the new `run()` | 65.22 M |
| Fix | 67.63 M (+3.7% over round 2) |

- `arm()` costs about 32 more instructions per call (8.27 M to 10.67 M over about 76 k calls).
- `pts_timer_set` reads the clock twice: `now_of` for `schedule`, then again in `arm()`. That is about 2% of this benchmark.
- On the 1 M set/clear version (`bulk_timers`), interleaved minimum of 20 runs: base 298 ms, round 2 plus `run()` 341 ms, fix 362 ms; the host was loaded.
- Suggestion, not required: let `pts_timer_set` pass its fresh time to `arm()`, or have `arm()` take `now`. The fresh clock changes no ordering, because the heap order is untouched.

**2. Nit (docs):**
- SPEC §9.128 says `timers_refused_no_leak` makes "3.2 million refused timers". It runs 3.2 M callbacks, of which only 0.20 M (2 threads) to 0.33 M (4 threads) are refused (`refused_timer_leak_160`).
- D-186 and SPEC list the steps as `NOWAIT`, then `ONCE`, then the loop's own timer run. The code runs its own timers between the two, and only when `NOWAIT` leaves nothing referenced.
- C131 could cite the round-2 regression probes `order2/immediate_after_task_timer` and `order2/immediate_after_stolen_task`; the first one passed on the base.

**3. For the record: an intended behaviour change from the base.** After `await parallel x`, code that sets a 0 ms timer and an immediate and then works now prints `immediate,timeout` at 1, 2 and 4 threads. The base printed `timeout,immediate`. Node agrees with the fix when the await is real I/O (`await readFile`), but the sequential twin (`Promise.resolve`) gives `timeout,immediate`. So an expect block taken from a twin can differ for this pattern (`probes/first/after_await_task*`). D-186 covers this ("as after I/O"); no existing test depends on it.

### Checked and fine
- **New `run()` can't block, starve or delay the end:**
  - By libuv 1.53's code, a loop of `UV_RUN_ONCE` calls is `UV_RUN_DEFAULT` without its first timers phase: the same poll timeout, blocking only while something referenced is alive, and a return of 0 when nothing is. `NOWAIT` polls with 0.
  - A task's idle server on a loop: 400 tasks placed there, 20 fetches and a stop all work, and the program ends at 1, 2 and 4 threads, as on the base (`probes/idle/server_loop_takes_tasks`).
  - Idle server for 2 s: 0.00 s CPU and 16 voluntary context switches, the base 15 (`idle_server_cpu`).
  - `cpu_idle` and `cpu_long_unref` use 0.00 s. The unref'd intervals use 0.04 s for 5,592 ticks, and at 2 threads there is about one timed wait per 1.2 ms.
- **First turn on the main thread:** `probes/first/toplevel_first_turn` gives `A,E,B,C,D` on fix and base at 1, 2 and 4 threads, the same as Node 5/5.
- **Ordering:**
  - All four probes from rounds 1 and 2 (`immediate_after_task_timer`, `immediate_after_stolen_task`, `immediate_in_unref_cb`, `_nospin`) give `immediate,timeout` 10/10 at 1–4 threads.
  - The matrix matches Node on 10 of 11; the per-duration lists case is C133.
  - `interval_semantics` matches Node 3/3, including the 25 ms gap (C136), and `interval_after_long_callback_settimeout` gives about 10 ms 3/3.
- **Stress:**
  - The ping-pong probes had 0 failures in 150 runs at 2, 4 and 8 threads.
  - `unref_cb_spawns` is fine at 1, 2, 4 and 8 threads, and `ref_toggle_interval` matches Node.
  - Throwing unref'd timers and intervals on sleeping loops exit 1 with the message.
- **Performance:** `many_tasks` and `pingpong` at 4 and 8 threads are within noise.
- **New tests on this build:** all pass 4 times at 1, 2 and 4 threads; `timers_refused_no_leak` passes 3 times at 2 and 4.
- **`serve_par_callbacks` at 8 rounds:**
  - The fix failed 0 of 10 runs at 4 threads, 0 of 8 under contention and 0 of 12 sequential runs.
  - The base failed 12 of 26 runs at the current load: 10 hung, and 2 aborted on `uv__finish_close: Assertion '!(handle->flags & UV_HANDLE_CLOSED)'` (`out/base_serve.12.err`). That double close is extra evidence for C134.
  - Failure on the base still depends on load, as the parent saw.
- **Full copy at this build** (`review2/copies/fixcopy2` with this build's three changed files): `scheduler_test.c` passed 3 times under ASan/UBSan and TSan, and the focused harness run of 17 timer, task and new tests passed (52 PASS lines, no failures).
- **Docs:**
  - D-186's Reason ("registering there doesn't pass with one thread and throw with four (code after an `await` still can…)") and its libuv-turn wording are now right.
  - Its account of C130 and C136 matches my measurements.
  - Ledger rows C126–C136 match what I measured; C135 and C133 are correctly open for H19, C126 for H29 and C129 for H20.

## Round 4 (2026-10-18, resumed with SendMessage)

Reviewed diff sha256 `fbd5832f4375729993554da07207007bb4cd8926a21e3e9002b6ae6053d70947`. Probes:
[h07b2-review4-probes.tar.gz](h07b2-review4-probes.tar.gz).

---

The `arm()` change keeps every round-3 behaviour, the instruction count is confirmed, and the docs are accurate. I found nothing new. Archive: `/tmp/h07b2/review4/h07b2-review4-probes.tar.gz` (sha256 `d51e5e2e…5f75`, 319 text files). The tree stayed at `fbd5832f…` throughout, and I left nothing in it.

**The change:** only `pts/runtime/src/loop.c` changed in code. Each of `arm()`'s five call sites now passes a fresh reading:
- `pts_timer_set` and `reschedule` read `now_of` once and pass it to both `schedule` and `arm()`.
- The end of `on_timer`, `remove_cleared` and `apply_ref` pass `now_of(s)`.

**Behaviour, same as round 3:**
- **Ordering:** the four ordering probes give `immediate,timeout` 10/10 at 1–4 threads.
- **Matrix:** matches Node on 10 of 11 (the per-duration lists case is C133).
- **Intervals:** `interval_semantics` matches Node 3/3, including the 25 ms gap (C136), and `interval_after_long_callback_settimeout` gives about 10 ms 3/3.
- **First turn:** `A,E,B,C,D` at 1, 2 and 4 threads, as Node, and `after_await_task` gives `immediate,timeout`.
- **C135:** still open as recorded (fires once).

**Instruction count:** callgrind on `probes/perf/bulk_small.pts` gives 66.21 M, against 67.63 M in round 3 and 64.36 M on the base, so the cost over the base falls from +5.1% to +2.9%. The parent measured 66.24 M. `uv_update_time` falls from 3.80 M to 2.66 M inclusive.

**Tests:**

| What | Result |
|---|---|
| The 8 new run tests | 3 runs each at 1, 2 and 4 threads, no failures |
| `timers_refused_no_leak` | 2 runs each at 2 and 4 threads, passed |
| Ping-pong stress | 0 failures in 90 runs at 2, 4 and 8 threads |
| `unref_cb_spawns`, `server_loop_takes_tasks` | fine |
| Unref'd intervals CPU | 0.13 s for 5,609 ticks |
| `scheduler_test.c` in my full copy with this `loop.c` | passed 3/3 under ASan/UBSan and 3/3 under TSan |

**Docs, read once:**
- D-186's step order now matches the code, and its new "Code after `await parallel …`" bullet is accurate.
- SPEC §9.128 now gives the right step order, describes `arm()`'s wait as taking the caller's reading, and words `timers_refused_no_leak` as "3.2 million callbacks, a few hundred thousand of whose timers are refused".
- C131 cites the round-2 probes, and C134 cites the 12-of-26 base failures and the `uv__finish_close` double close.
