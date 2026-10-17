# H07b2 reproducer report (opus-reproducer, claude-opus-5-5, xhigh)

Returned as the child's final message on 2026-10-17 and saved verbatim by the parent (the named
subagents can't write report files). The child worked in the isolated worktree
`.claude/worktrees/agent-a3f0dbcfb2085c7a7` at `ec24125a8`, scratch `/tmp/h07b2/repro/`, against the
parent's base worktree `/tmp/h07b2/base`. Its archive is
[h07b2-reproducer-probes.tar.gz](h07b2-reproducer-probes.tar.gz) (sha256
`30288ad2e0682a4f1ae70215cf702466e6cea982c8cc5f33db50a54e61f694ac`); its proposed tests are under
`deliverables/` there. The parent's own runs of these claims are in [h07b2.md](h07b2.md).

---

# H07b2 reproducer report: C120 (unreferenced timers on sleeping loops) and C117 (pts:test registry off the main thread)

All of C120 and C117 reproduce on the base. Along the way I found two more pre-existing bugs that the fix needs to know about: timers count from a stale loop clock, so they fire early (this one breaks C120's own tests), and a segfault when a timer is set in a `.par` callback on a helper loop. The RED tests are ready to drop in. Nothing is committed and nothing is ticked. The probe archive is `/tmp/h07b2/repro/h07b2-reproducer-probes.tar.gz` (sources and text outputs only, no binaries).

All runs used the base CLI (`PTS_HOME=/tmp/h07b2/base /tmp/h07b2/base/pts-bin/pts build x.pts -o bin/x`, then `PTS_THREADS=n bin/x`, 3 runs per thread count unless noted). Node is `~/.local/node/bin/node --no-warnings --experimental-transform-types twin.mts`, where the twin replaces `parallel e` with `Promise.resolve(e)`.

Harness and C-test runs:
- **Harness, my worktree (same code as base):** `go test ./pts/tests/ -run '^(TestRun|TestRunMatchesNode)$/^(…new names…)$'`, exit 1.
- **C test:** `go test ./pts/toolchain -run 'TestThreadedRuntimeTestsPassUnderThreadSanitizer/scheduler_test.c|TestRuntimeUnitTestsPassUnderSanitizers/scheduler_test.c'`, exit 1.
- **Go test:** `go test ./cmd/pts -run TestTestRegistrationOffTheMainThreadThrows`, exit 1.

## C120: confirmed

| Test or probe | Base at 1 thread | Base at 2 and 4 threads | Node |
|---|---|---|---|
| Committed `abort_timeouts_sleeping_loops` (skip line removed) | matches (552 ms) | 3/3 exit 13 after about 3005 ms: `a task's unref'd timer never fired`, stderr `Detected unsettled top-level await` | matches 3/3 |
| Handed-back timeouts (`p6/timeout_handoff2`) | `yyyyyyyy` | varies run to run: `ynyyyyyy`, `ynnnnyny`, `ynnnnnnn`, `yynnnnnn` | `yyyyyyyy` |
| **New `run/timers_unref_sleeping_loops.pts`** | matches 3/3 (1.22 s); 0/40 differ with 8 copies running at once | 3/3 differ (6.16 s), see below | matches 3/3 |
| **New `run/timers_unref_program_end.pts`** | matches (104 ms) | matches 3/3 | matches |

On the base at 2 and 4 threads, `timers_unref_sleeping_loops` prints:

```
a task's unref'd interval: never ticked 5 times
cleared from another thread: stopped
a task's timer unref'd from another thread: fired 60 ms early
a task's unref'd timer refreshed from another thread: never fired
tasks' later unref'd timers: never fired and those cleared from another thread: never fired
the main loop's unref'd timer while a task works: never fired
```

`timers_unref_program_end` is a guard that already passes on the base, not a RED test. It checks that the program ends promptly with only unreferenced timers left (10 s and 2,000,000,000 ms timers, 5 ms intervals, `AbortSignal.timeout`, a timer unref'd from another thread), and that a timer ref'd from another thread keeps it running. With only unreferenced timers the base ends in 5–6 ms (hyperfine 2.8 ± 0.2 ms at 4 threads).

Answers to the specific items:
- **When a sleeping loop's unref'd timer fires on the base (item 8):** only when that loop later gets referenced work, otherwise never. In `late.pts` a 20 ms timer fired at 300 ms, when the main loop sent tasks with a referenced timer; with 4 threads some never fired. One thread and Node fire at 20–24 ms.
- **Woken but not alive (item 9):** a loop woken by messages that leave nothing referenced (`late_msg.pts`) never fires its overdue timers. This is because `uv_run` doesn't run timers at all, in any mode, when nothing referenced is left (libuv 1.53, `core.c:432-445`).
- **ref(), unref() and refresh() from another thread (item 4):** `ref()` from another thread works on the base (the guard covers it). `unref()` and `refresh()` from another thread are covered in the sleeping-loops test.
- **CPU (item 3):** 1 ms intervals on 3 loops for 2 s, at 4 threads: unref'd on sleeping loops gives 0 ticks and 0 CPU on the base; referenced gives 5,362 ticks and 0.09 s CPU. That second figure is a fair budget for the fix. Node: 5,331 ticks, 0.30 s including startup.

Test-design trap: the main loop takes an unstarted task whenever it has nothing referenced. My first interval probe "passed" at 2 and 4 threads in 8 of 9 runs because the main loop ran the task itself. The new tests keep a referenced `hold` timer on the main loop until the last part.

**Timing tolerance I'd propose:** only lower bounds (at least delay − 5 ms) and orderings between events. Upper bounds only as deadlines of 2 s or more whose expiry prints a "never …" line. In C, a 10× margin (30 ms against 300 ms).

**C unit test** (in my worktree, `pts/runtime/tests/scheduler_test.c`): after the tasks finish,
- loop 1 gets a 30 ms unreferenced timer, loop 2 a 10-minute one, loop 3 a 5 ms interval;
- loop 2 also gets 200 unreferenced timers that the main loop clears at about 20 ms;
- the main loop has a referenced 300 ms timer.

It fails on the base under both ASan and TSan on exactly three checks: `unref_fired == 1`, `unref_fired_by_main == 1` and `ticks_by_main >= 3`. There are no TSan reports, and it takes 1.4 s under ASan and 2.5 s under TSan.

## New finding A: timers count from a stale loop clock and fire early

`pts_timer_set` and `refresh` count from `uv_now`, which isn't updated after synchronous work or after a loop has slept. This contradicts what D-114 says Node does: Node 24 counts each timer from when it is set.

| Test | Base | Node |
|---|---|---|
| **New `run/timers_count_from_when_set.pts`** (plain TypeScript, so TestRunMatchesNode compares it with Node) | at every thread count: `100 ms early`, timer order reversed (`due at 21 ms, due at 3 ms`), `99 ms early` in a callback. Both TestRun and TestRunMatchesNode fail. | matches 3/3 |
| **New `run/timers_after_sleep.pts`** (threads 1 2 4) | 1 thread: main loop's timer 1 of 1 early. 2 threads: tasks' timers 6 of 6 early. 4 threads: 4–6 of 6 early. | matches 3/3 |

In `stale.pts`, 200 ms timers set by tasks fired after 0 ms.

This also breaks C120's own test. With loops kept awake, the 4-thread runs still showed a timer unref'd from another thread `fired 60 ms early`, a refreshed timer firing 40 ms before the refresh, and a "cleared" timer firing before it was cleared.

As an experiment I added `uv_update_time` before `schedule` in `pts_timer_set` and `reschedule` (then reverted it; the patch is in the archive as `c120/experiment_update_time.patch.txt`). Both stale-clock tests pass, all 24 TestRun and 12 TestRunMatchesNode timer, abort and fetch tests still pass, and only C120's own test still fails. My recommendation is to fix this inside H07b2: at least update the loop time after every wake.

## New finding B: segfault with timers set in `.par` callbacks

Probe `c120/par_timers.pts`: four tasks run `.par.map` at once, the callbacks call `setTimeout`, and the main thread clears the timers it gets back.
- **4 threads:** exit 139 in 3/3 runs (and 4/4 under gdb), SIGSEGV in `pts_post(loop=32769 or 32771)` called from `clearTimeout` (`par_timers.pts:38`). A loop thread that helps another loop's operation records `timer->loop` with `PTS_LOOP_FOREIGN` set, so the clear indexes `slots[0x8000+X]`.
- **2 threads:** no crash, but about 46% of callbacks throw "Timers and asynchronous I/O can't be started from a parallel operation's callback yet". Whether `setTimeout` works depends on which thread runs the callback.

I didn't write a test for this, because the intended semantics (throw everywhere, or work on the operation's loop) aren't decided. C117's fix needs an "inside a parallel operation" flag, and the same flag could make timers consistent.

## C117: confirmed (no crashes)

- **Committed probe:** 1 thread gives 1 run, 800 tests. 2 and 4 threads give 4 runner starts, each rerunning everything registered so far (cumulative totals 200, 600, 1200, 2000).
- **Overlapping suite bodies** (`c117/registry_overlap.pts`, 4 threads, 3/3 runs): suite 1 nests inside suite 0, tests land in the wrong suite or at the root, and the main thread's own later test lands in suite 0 because `current` is left pointing there. Totals are 3, 7 and 12 over three starts; Node runs once with 5 tests.
- **Silent never-run:** when the program's first registration happens in a `.par` callback on a pool thread, `start()` sets `started` and then `setImmediate` throws. The runner then never runs: no output, no summary, exit 0 in 3/3 runs. Under `pts test` it shows as "ended before its tests reported", exit 1.
- **Hooks from tasks:** hooks registered from tasks ran 72–80 times.
- **New `run/pts_test_registry_threads.pts`** (threads 1 2 4): every registration function from tasks, `.par.map`, `.par.forEach`, `parallel for` and `join` is expected to print `Error`, with `suite bodies run: 0`. On the base it prints `registered` (sometimes `.par.map callbacks: Error 1, registered 63`), 60 suite bodies run, 1–5 runner starts and 258–4166 extra lines. It needs no `no-diff` line: the harness already skips the Node comparison for programs that use `parallel` or pts:test. It prints only the error's name, so the message can change freely.
- **New Go test `TestTestRegistrationOffTheMainThreadThrows`** (in `cmd/pts/test_test.go`, threads 1 2 4): it checks the exact runner output, with durations normalised, of a file that tries to register from other threads while the main thread is inside a suite's body. It fails at all three thread counts on the base (tests 12, suites 9, 1 failing, where 4 tests and 1 suite are expected). It has to be a Go test, because run tests compare output exactly and can't handle the runner's durations.
- **Late registration (one thread, separate issue):** `test("a")`, wait 200 ms, `test("b")` gives two summaries (tests 1, then tests 3) with "a" running twice. Node runs a and b once each, prints `tests 2` and one summary.

## Edge cases and risks for the fix

- **Running the timers:** after a timed wake the fix has to run the due timers itself, since `uv_run` won't. A loop that isn't alive but is busy with a flood of tasks also never runs them (`busy_unref`: never on the base; a referenced timer fires at 316 ms, between 64-task batches). The fix should run due timers after each batch too, not only after sleeping.
- **Rejoining the awake count:** rejoin with a compare-and-swap that only increments when the count is above zero, never after `finished`. Flip `sleeping` and bump `awake` under the slot lock, so a message arriving at the same moment can't bump it twice (that would leak a count and hang the program).
- **Waiting:** use absolute deadlines and recheck after spurious wakeups (collector signals), and update the loop time before computing a deadline. Use `CLOCK_MONOTONIC` through `pthread_condattr_setclock`. libuv's loop time uses the coarse monotonic clock here (1 ms resolution, HZ=1000), so wait at least 1 ms to avoid spinning when the coarse clock lags. The CI kernel's resolution is unverified.
- **Clear racing the wake:** a cleared timer stays in the heap until its message arrives, so on a timed wake the loop must check `active` (as `on_timer` does). The C test's 200 racing clears give TSan coverage.
- **At the end:** loops in a timed wait after `finished` must not run callbacks while the main thread exits.

## Not verified

Nothing has run GREEN at 2 or 4 threads, because no fix exists. The expected outputs come from Node, from 1-thread runs, and from a variant that keeps the loops awake (all lines except the main-loop one matched in 18/18 runs at 2 threads). The C test and the Go test are unverified against a fixed runtime.

## Files

Worktree `/home/bryan/projects/parallelscript/.claude/worktrees/agent-a3f0dbcfb2085c7a7` (uncommitted; copies in the archive under `deliverables/`):
- `pts/tests/run/timers_unref_sleeping_loops.pts`
- `pts/tests/run/timers_unref_program_end.pts`
- `pts/tests/run/timers_count_from_when_set.pts`
- `pts/tests/run/timers_after_sleep.pts`
- `pts/tests/run/pts_test_registry_threads.pts`
- `pts/runtime/tests/scheduler_test.c`
- `cmd/pts/test_test.go`

In `/tmp/h07b2/repro/h07b2-reproducer-probes.tar.gz`, the per-test, per-thread-count results against the expect blocks are in `final/comparison.txt`.
