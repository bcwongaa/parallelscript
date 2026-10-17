# H07b1 review (opus-reviewer, claude-opus-5-5, xhigh)

## Round 1

Returned as the child's final message on 2026-10-17 and saved verbatim by the parent (the named
subagents can't write report files). The reviewer read the main tree's uncommitted change on top
of `8f5439c05` (library code byte-identical to snapshot `/tmp/h07b/snap1`) and the base worktree
`/tmp/h07b/base`, read-only; its probes are
[h07b-review-probes.tar.gz](h07b-review-probes.tar.gz) (sha256
`2e6befbcb689e4ebfa63a1921acce7a012e1850414df9ce718d81992feb01d1f`). The parent's handling of each
finding is in [h07b.md](h07b.md).

---

**Verdict: changes needed.** One small fix is needed. In a frozen signal, a throw inside `#settle` leaves the signal's lock held, and the next use of that signal hangs. D-185 says that can't happen. The rest of the design held up under every probe: per-target locks, the DOM standard's dependent signals, add-then-check at the check-then-act sites, and `OWN_SIGNAL_LOCK`. The new tests fail on the base for the right reasons and passed every run on the fix at 1, 2, 3, 4 and 8 threads.

## Findings, by severity

### 1. A frozen signal: `abort()` throws with the lock held, and the next use hangs (medium-low, verified)
- **Where:** `pts/lib/internal/abort.pts`, `#settle` (about lines 381–395). `(signal as { aborted: boolean }).aborted = true` compiles to a checked field write (`PTS_FIELD_SET(bool, …, "aborted", …)`, in `logs/freeze.fix.c`) between `pts_sync_lock` and `pts_sync_unlock`, with no `finally`.
- **Why it matters:** D-185, ARCHITECTURE §4.13 rule 9 and the comment on `#lock` all say "nothing can leave it held". This is the only throw a program can reach inside a critical section: `Object.defineProperty` is PTS1001, and the other sections touch only private state and runtime built-ins.
- **Repro:** `probes/lockheld/freeze_stderr.pts` is `Object.freeze(controller.signal); try { controller.abort("frozen") } catch {…}; controller.signal.addEventListener("abort", …)`.
  - Fix: prints `abort threw TypeError: Cannot assign to read only property 'aborted'…`, then `adding a listener after that...`, then hangs (`timeout 5` gives exit 124).
  - gdb: `futex_wait` ← `pts_sync_lock` ← `pts_fn_abort_EventTarget_private_add` (abort:223).
  - Base: throws, then keeps going, exit 0. Node 24 throws TypeError on `abort` and again on `addEventListener`.
- **Fix:**
  - Put `#settle`'s section in try/finally. That costs one setjmp per abort.
  - On failure, either restore `#reason`, or have `#follow` return `source.aborted ? source.#reason : undefined`. Otherwise a half-settled signal makes `any()` abort with a reason its source never took.
  - Add a regression test.

### 2. Two concurrent races the tests don't cover; the fix handles both (low, verified)
- **Where:** `probes/stress/extra.pts`, 2,000 rounds each:
  - (a) `onabort` set from two threads at once.
  - (e) the two sources of one follower aborted on two threads at once.
  - (h) a follower made while both of its sources abort.
- **Results:**

  | | Fix (1/2/4 threads) | Base, 2 threads | Base, 4 threads |
  |---|---|---|---|
  | (a) | wrong 0 | wrong 14 | wrong 28 |
  | (e) | wrong 0 | wrong 353 | wrong 371 |
  | (h) | wrong 0 | wrong 13 | wrong 0 |

- (e) is the common real case: `fetch` given `AbortSignal.any([userSignal, timeoutSignal])`. I suggest adding (a) and (e) to `abort_signal_shared_threads.pts`.

### 3. The `{ signal }` section only sometimes catches a missing re-check (low, verified by mutation)
I built CLIs with `go build -overlay` and a mutated library (`mut/`):

| Mutant | Caught |
|---|---|
| M1: no re-check after `{ signal }`'s remover is added | 12 of 20 runs, by 1–3 listeners each time |
| M2: no re-check in `exchange` | 10 of 10 (`hung 172–312`) |
| M3: `once` not claimed under the lock | 19 of 20 |
| M4: `#follow` doesn't check `aborted` | 20 of 20 |
| M5: `#settle` checks `aborted` outside the lock | 20 of 20 |

A single CI pass would catch M1 about 84% of the time. Repeating the section a few times (it's deterministic and takes milliseconds on the fix) would make that near-certain.

### 4. At 1 thread the fix follows the DOM standard where Node 24.21 doesn't (low, verified; document rather than copy)
- **Cause:** Node 24.21's `AbortSignal.any` follows its sources lazily. It registers a follower only when the follower gets its first abort listener, and otherwise works out `aborted`/`reason` when they're read (`logs/node_abort_controller.js`: `followCompositeSignal`, `refreshCompositeSignal`).
- **Repro:** `probes/sem/semantics2.pts` and `semantics.pts`, sections B, U, R1 and R4.
  - A listener or `onabort` added, during the source's abort event, to a follower that had no listener yet: Node doesn't call it; the fix does. The base matched Node here by accident.
  - A follower of `a` and `b` with no listener, after `b.abort("b first")` then `a.abort("a second")`: Node gives `reason` "a second"; the fix and the base give "b first", as the standard says.
- **What to do:** the new tests always add listeners first, so they stay robust against Node. SPEC §7.1 says "as Node's globals", so add a line to D-185 and SPEC naming this divergence.

### 5. Things the change didn't introduce (information only, verified)
These are identical on base and fix:
- **The signal itself as `{ signal }`:** `addEventListener("abort", l, { signal: sameSignal })` calls `l`; Node removes it first.
- **A duplicate listener given `{ signal }`:** an abort doesn't remove the existing listener; Node does.
- **Reflection:** `JSON.stringify` and `Object.keys` of a signal show `aborted`.
- **C118 adapters:** listeners typed `() => void` get wrapped, so `removeEventListener` can't find them. That made sections C/P/X of my first probe misleading; they match Node once typed as `EventListener`.

## Your questions
- **`OWN_SIGNAL_LOCK`:** acceptable.
  - The server's hot path is unchanged. `receive` makes `new Request(url, { method, body })` with no signal, so the lock is taken only on a first `.signal` read and on a disconnect.
  - With 8 threads hammering first reads at once (`probes/perf/reqsig.pts`), the lock costs about 10% over the same allocation with no lock: 435–497 against 408–448 ns/op. Both scale the same way, limited by allocation rather than the lock.
  - Per-connection striped locks would remove even that at no allocation cost, but I don't think it's needed. I didn't run the server benchmark, since nothing on its path changed.
- **Cost (callgrind, instructions per operation, base → fix):**

  | Operation | Base | Fix | Change |
  |---|---|---|---|
  | `new AbortController` | 713 | 887 | +24% (the 32-byte lock buffer) |
  | add + remove a listener | 1,503 | 1,731 | +15% |
  | dispatch to one listener | 810 | 839 | +4% |
  | `once` add + dispatch | 2,586 | 2,549 | −1% |
  | controller + listener + abort | 15,237 | 16,759 | +10% |
  | controller + `any` + add/remove (what each fetch does) | 4,151 | 5,567 | +34% |

  These are small next to any real fetch. `abort_signal_any_cost` takes 0.38–0.48 s against the 30 s limit (Node: 1.86 s).
  - One caveat: an O(n) `includes`-style scan for duplicates might stay under 30 s on a fast machine. 400,000 followers would leave more margin.
- **Retention:** I agree with recording it as C123 and not fixing it here. It isn't a regression.
  - My fetch probe (`probes/retention/fetch_retention.pts`, a long-lived signal given to every fetch): base about 0.9 KB per fetch (22 → 58 MB over 40,000), fix about 0.6–0.75 KB (26 → 49 MB). The control without a signal stays flat at 9 MB.
  - A cheap fix for the commonest case, for C123: when `fetch` gets a URL string, the `Request` it makes is never visible to the program, so `exchange` could listen on the given signal directly and remove the listener when the fetch ends. No dependent would be needed. Alternatively, keep dependents in a `Set` and drop the internal Request's follower when its fetch ends.

## Verified working
- **Locking:** locks are never nested, and no listener runs under a lock. The `once` claim keeps the "removed ⇔ not in the list" invariant, so `indexOf` can't return −1. A signal aborts once and its followers take one reason.
- **GC:** waiters in the futex lock are woken by the collector's signals, so a collection while a lock is held doesn't deadlock. Lock words are 16-aligned, in buffers that never move.
- **Memory order:** `#settle` writes the reason before `aborted`, so readers outside the lock (`throwIfAborted`, `any`'s first check) rely only on §4.13 rule 6.
- **Fetch:** `pts_http_fetch_abort` ignores ids that have ended (generation check), so the double abort in `exchange` is harmless.
- **RED on base:** every section of `shared_threads` fails at 2 and 4 threads (lost followers, `RangeError`s, double events, `once` called twice, leftover `{ signal }` listeners). The fetch test hangs 1 or 5 of 200 shared-signal fetches and up to about 300 of the 1,000 single ones. `any_dependents` fails on order.
- **GREEN on the fix:** `shared_threads` 40 of 40 runs at each of 2/3/4/8 threads, `shared_parallel` 40 of 40 at 2/4/8, fetch 10 of 10 at 1/2/4/8. The targeted `go test` run passed, including the Node comparisons, `TestHTTPServerLifecycle`, `TestHTTPServerSignalsEveryLoop`, `TestFetchFromGoServer` and the skip-classification tests.

I made no changes to the repo; the main tree's code files are byte-identical to `/tmp/h07b/snap1`. The diff hash differs from the one you gave only because of your doc edits (README, ARCHITECTURE, DECISIONS, GUIDE, SPEC). The archive is `/tmp/h07b/review1/h07b-review-probes.tar.gz` (sha256 `2e6befbc…1d1f`; probes, twins, logs, tools, mutants and `cmds.txt`, no binaries), and `/tmp/h07b/review1/DONE` is touched.

## Round 2

The same reviewer, resumed with round 1's context, reviewed the parent's changes for round 1's findings (still read-only; the main tree's code byte-identical to snapshot `/tmp/h07b/snap2`). Saved verbatim from its final message; probes [h07b-review2-probes.tar.gz](h07b-review2-probes.tar.gz) (sha256 `4da503f0c8da3c1ce20089fa99908eb8096f8e9580b372cf7299732e3157cba8`).

---

**H07b1 review, round 2. Verdict: ready.** All my round-1 findings are fixed or documented, and every mutant is now caught. What's left are two small divergences from Node in the `{ signal }` option's removal listener, plus doc nits. None needs to block H07b1; each can be fixed in a line or logged as a known gap.

## Round-1 findings, re-checked

**F1, frozen signals: fixed (verified).**
- **New test:** `run/abort_signal_frozen.pts` hangs on round 1's CLI (exit 124). On the base it fails with an uncaught `TypeError` from the frozen follower. On the fix it passes 5 of 5 runs.
- **No hangs in the other cases** (`probes/frozen/frozen_more.pts`):
  - `any()` of a frozen source, then that source aborted twice: throws both times; the follower stays unaborted.
  - One frozen follower among 100: 99 abort, 99 events fire, `abort()` throws once.
  - Two frozen followers: the first one's error is thrown; the follower between them aborts.
  - `onabort` and a `once` listener set before the freeze: neither runs on the abort; `onabort` can still be set afterwards.
  - A frozen follower of two sources: each source's `abort()` throws.
  - A frozen follower's `{ signal }` listeners and `throwIfAborted`: both behave.
- **No lock left held anywhere a program can reach.** The `aborted` write in `#settle` was the only throw a program could cause inside a section, and it's now caught. Every other locked section only touches private state and runtime built-ins.
- **Threads:** followers made from 3 threads while a fourth keeps trying to abort a frozen source give 0 aborted, 0 hangs, 6 of 6 runs.
- **The half-settled reason can't leak through the library:** `#follow` reads it under the lock, after it was put back.
- **Info, not blocking:** a reader *without* the lock does see the attempted reason. With one thread retrying `abort()` and another reading `reason` in a loop, about 99% of reads saw `"half-settled"` while `aborted` stayed false (`frozen_race.pts`). The reason: the TypeError, stack trace included, is built while the reason is in place. Two options:
  - qualify D-185's "the signal as it was" as true for locked readers and after the throw;
  - or check `Object.isFrozen(signal)` (supported) before writing, keeping the catch as a backstop.
- **Node by comparison:** with a frozen follower, `abort()` throws before the source's own listener runs. So "Node throws too, part-way" is accurate, and our behaviour is the gentler one.

**F2 and F3, the new sections and mutants: now caught reliably (verified).** I rebased each mutant on the new `abort.pts`, built it with `go build -overlay`, and ran 10 runs each at 2 and 4 threads:

| Mutant | Caught | Section that catches it |
|---|---|---|
| M1: no `{ signal }` re-check | 18 of 20 (10 of 10 at 4 threads) | the 5× `{ signal }` section; was 12 of 20 |
| M2: no `exchange` re-check | 10 of 10 | fetch test (`hung 172–312`) |
| M3: `once` not claimed under the lock | 20 of 20 | `once` listener section |
| M4: `#follow` ignores `aborted` | 20 of 20 | follower against an abort a moment later |
| M5: `#settle` checks `aborted` outside the lock | 20 of 20 | two-thread abort, and the new two-sources section |
| M6 (new): `#claimHandler` without the lock | 20 of 20 | new `onabort` section |

- On the base, the new sections fail: `onabort` wrong 51 and 61, two sources wrong 14 and 2, `{ signal }` leftovers 7–131 (plus 1,355 `RangeError`s in one run).
- The regenerated Node twin prints the expect block exactly.

**F4, Node's lazy `any()`: documented correctly** in D-185's known gaps and the new SPEC §8 bullet; both match what I verified in round 1.

**Cost:** `abort_signal_any_cost` now takes 0.93–1.23 s for 500,000 followers, using 240–280 MB, against the 30 s limit. The new try/catch adds roughly 300–400 instructions per abort, about 2%.

## The new `{ signal }` order against Node
`probes/sem/signal_option.pts`, run on Node, the fix and the base:
- **Now matches Node (the base didn't):**
  - a self-signal `once` listener;
  - the same listener given two signals;
  - a listener added again with a signal (round-1 G and I, now C124 and C125).
- **Unchanged and matching:** `once` with a signal (a removal listener left over still removes a listener added again later, in Node too); a program's abort listener added earlier still runs before the removal; a follower used as the signal; `stopImmediatePropagation` with a self-signal.

**Remaining divergences** (low, verified). Our removal listener is neither `once` nor resistant to `stopImmediatePropagation`, while Node's is both:
1. **An earlier abort listener calls `stopImmediatePropagation()`:** Node still removes the `{ signal }` listener; we leave it attached, and it keeps firing after the abort.
   - The library itself isn't exposed to this: fetch's internal signal can't be reached by programs, and the library doesn't use `{ signal }` internally.
   - It does break the promise of auto-removal for programs that stop propagation on abort. That's rare, so this is low; fixing it takes an internal "resists" flag on the registration, or record it as a known gap.
2. **Untrusted `abort` events:**
   - A program dispatching `new Event("abort")` to the signal before the real abort: Node uses up its `once` removal listener then; ours runs again on the real abort and removes a listener added back meanwhile.
   - The same after the real abort, then a listener added back, then a manual dispatch: ours removes it, Node's doesn't.
   - This is contrived. Passing `{ once: true }` when adding the removal listener fixes both cases in one line.

The concurrency of the new order is sound. In every interleaving, either the abort's removal listener runs after `#add`, or the re-check sees `aborted`, because the two threads' lock acquisitions order the stores. The 5× section passed 15 of 15 runs at each of 1, 2, 4 and 8 threads.

## Test quality nits
- **Vacuous assertion:** in `run/abort_listener_signal_option.pts`, block 3's line "listeners the null listener added to its signal: none" can't fail. `removals` counts only the program's own listener, and the library's internal removal listener isn't visible that way (removing `null` does nothing). The base passes that line too. Drop it or reword it as a smoke check.
- **`abort_signal_frozen.pts`** runs at 1 thread only; that's fine for its purpose.

## Doc claims
- **ARCHITECTURE §4.13 rule 9:** "held for plain reads and writes". The `aborted` write is a checked field write, and when it fails it builds a TypeError (stack included) while the lock is held. That's harmless, but worth a clause.
- **D-185, C123 sizing:** "about 300 bytes each" holds per `any()` follower. Per finished fetch I measured about 0.6–0.9 KB (similar to the base).
- The rest of D-185, SPEC §7.1, §8 and §9.127, the GUIDE and the README match what I verified.

## Verified working
- **Determinism:** `shared_threads` passed 15 of 15 runs at each of 1, 2, 4 and 8 threads; fetch and `shared_parallel` 5 of 5 at 1, 2 and 4; `any_dependents` 3 of 3.
- **Go tests:** the targeted run passed (`TestRun` and `TestRunMatchesNode` over `abort_*`, `fetch_abort*`, `fetch_signals*`, plus `TestCorpusSkipsAreClassified`). `abort_listener_signal_option` matches Node; Node's only output on stderr is its warning about the null listener.
- **Round-1 semantics probes on the new CLI:** they now differ from Node only in F4's documented cases and in pre-existing C118 adapter effects.
- **Repo:** the code files are byte-identical to `/tmp/h07b/snap2`, and I wrote nothing into the repo.

Archive: `/tmp/h07b/review2/h07b-review2-probes.tar.gz` (sha256 `4da503f0…cba8`; probes, twins, logs, tools, mutant sources and overlays, and `cmds.txt`; no binaries). `/tmp/h07b/review2/DONE` is touched.

## Round 3

The same reviewer, resumed, reviewed the parent's changes for round 2's notes (read-only; the code byte-identical to snapshot `/tmp/h07b/snap3`). Saved verbatim from its final message; probes [h07b-review3-probes.tar.gz](h07b-review3-probes.tar.gz) (sha256 `38126caaf3dcb752137a8488eaa77e8fd62abe05d202bba77ed39ec3fe4a386e`).

---

**H07b1 review, round 3. Verdict: one small change needed.** The stop/resist logic matches Node in every case I tried, the frozen pre-check closes the `frozen_race.pts` observation, and none of M1–M6 escape. But making the `{ signal }` removal listener `once` introduced a performance regression: aborting a signal used by n `{ signal }` listeners is now O(n²). I prototyped a fix that restores O(n) and keeps Node's behaviour.

## Finding: a quadratic `abort()` with many `{ signal }` listeners (medium-low, verified)
- **Where:** `pts/lib/internal/abort.pts:178`, the removal listener is now added with `once: true`.
- **Cause:** each `once` registration that runs goes through `#take`, which does `indexOf` plus `splice` on the signal's listener array. An abort with n removal listeners does n splices of up to n elements.
- **Repro:** `probes/perf/signal_option_cost2.pts`. Each of n targets adds one listener with `{ signal: c.signal }`, then `c.abort()`. Times at 1 thread:

| n | Round 3 add / abort | Round 2 add / abort | Base add / abort | Node add / abort |
|---|---|---|---|---|
| 10,000 | 538 / 296 ms | 505 / 2 ms | 536 / 2 ms | 2,905 / 29 ms |
| 20,000 | 2,220 / 1,264 ms | 2,259 / 5 ms | 2,008 / 4 ms | 12,729 / 19 ms |

`signal_option_cost.pts`, abort only, at 50,000: round 3 22,878 ms, round 2 46 ms, Node 70 ms.
- **Context:** adding was already quadratic everywhere (a duplicate scan on the signal's list; Node is worst). What's new is that `abort()` itself stalls the loop: 1.3 s at 20,000 and 23 s at 50,000, against 5 ms and 46 ms in round 2. A long-lived shutdown signal used as `{ signal }` on many targets is where this would bite.
- **Prototype fix** (`proto/abort.pts`, built with `go build -overlay`, nothing written to the repo):
  - The removal listener stays non-`once`, but its closure carries a `used` flag. That gives the same Node semantics (an untrusted `abort` event uses it up) without a splice.
  - It is appended through a scan-free `#append`, since its closure is always new.
  - Results: add 12 ms / abort 4 ms at 20,000; 27 / 14 ms at 50,000. `resist.pts` still matches Node line for line. `abort_listener_signal_option` passes, and `abort_signal_shared_threads` passes 3 of 3 at 2 and at 4 threads.
- **Alternative:** have `#take` only flip `removed` under the lock and compact the array once after the dispatch. That also fixes the old O(n²) for the program's own `once` listeners, but `#find` would then have to skip removed entries.

## Verified working

**Stop/resist logic matches Node** (`probes/sem/resist.pts`, output identical to Node; round 2 differed in 5 places). I checked it against Node's own loop in `internal/event_target`, which skips a handler when it's removed, or when the event is stopped and the handler doesn't resist; ours does the same.
1. Events other than abort: listeners after a stop are skipped. A `once` listener after the stop isn't used up and runs on the next dispatch.
2. Abort stopped by an earlier listener: the removal still runs. Later plain and `once` listeners don't run, and the `once` one is kept for a later untrusted dispatch.
3. Two `{ signal }` removals on either side of the stopping listener: both run.
4. Re-entry: the stopping listener dispatches an untrusted `abort` to the same signal. The nested dispatch uses the removal up, and the outer dispatch skips it as removed.
5. Re-entry: a listener dispatches an untrusted `abort` (using the removal up) and adds the listener back before the real abort. The added-back listener stays, as in Node.
6. A self-signal `once` listener with a stopper before it.
7. `preventDefault` together with a stop: the return value is unchanged.
8. A follower used as the signal, with a stopper on the follower.

A resisting listener that itself stops can't occur, since only the library sets `resists` and its removal closure never stops the event. Callgrind costs are unchanged from round 2: dispatch 839, add+remove 1,717, abort 17,081 instructions.

**Non-signals passed as `{ signal }`** through `any` (`{}`, `{ aborted: false }`, an `EventTarget`, `42`, `null`): all throw `TypeError`, as in Node and on the base. The private `signal.#add` call is never reached with a wrong type.

**Frozen signals:**
- `frozen_race.pts` now gives "none (0 of 117–175 million)" unlocked reads of the put-back reason, in 6 of 6 runs at 2 and 4 threads (round 2: about 99% of reads saw it). The followers part stays at 0 aborted.
- `frozen_more.pts` output is identical to round 2.
- New `freeze_vs_abort.pts`: one thread freezes while another aborts, 5,000 rounds × 4 runs. Every outcome is consistent: either aborted, with the reason, the follower aborted and nothing thrown; or untouched, with the reason undefined and one TypeError. No hangs.

**Tests:**
- `abort_listener_signal_option`: Node prints the expect block exactly; the fix passes; round 2's CLI fails both new blocks; the base fails four lines.
- Fixed-tree loops: `shared_threads` 8 of 8 runs at each of 1, 2, 4 and 8 threads; fetch and `shared_parallel` 3 of 3 at 1, 2 and 4; `frozen` and `any_dependents` 3 of 3.

**Mutants, rebased on round 3's `abort.pts`** (5 runs each at 2 and at 4 threads):

| Mutant | Caught |
|---|---|
| M1: no `{ signal }` re-check | 7 of 10 (5 of 5 at 4 threads; round 2 was 18 of 20) |
| M2: no `exchange` re-check | 6 of 6 |
| M3: `once` not claimed | 10 of 10 |
| M4: `#follow` ignores `aborted` | 10 of 10 |
| M5: `#settle` checks outside the lock | 10 of 10 |
| M6: `#claimHandler` without the lock | 10 of 10 |

**Docs:** D-185's `{ signal }` and frozen bullets, the C123 sizing (about 300 B per follower, 0.6–0.9 KB per fetch), ARCHITECTURE §4.13 rule 9's clause about the checked write, and SPEC §9.127 all match what I verified. If you take the prototype's approach, the `{ signal }` bullet's "that listener runs once" should then say "is used up once" (by a flag).

I wrote nothing to the repo (`PROGRESS.md` is your edit). One mishap: a `pkill -f` I ran matched my own shell and killed my first cost runs; the numbers above are from clean re-runs. The 100,000-listener runs were cut off because the adds are quadratic for everyone. Archive: `/tmp/h07b/review3/h07b-review3-probes.tar.gz` (sha256 `38126caa…386e`; probes, twins, logs, tools, mutant and prototype sources with their overlays, `cmds.txt`; no binaries). `/tmp/h07b/review3/DONE` is touched.

## Round 4

The same reviewer, resumed, reviewed the parent's port of its round-3 prototype (read-only; the code byte-identical to snapshot `/tmp/h07b/snap4`). Saved verbatim from its final message; probes [h07b-review4-probes.tar.gz](h07b-review4-probes.tar.gz) (sha256 `c2789fdbb5e860597687ea50ca6ab1f67aee6783e9963ec279ba116d994b1fc5`).

---

**H07b1 review, round 4. Verdict: ready.** The round-3 regression is fixed: `abort()` is O(n) again. The `used` flag holds up across threads, and the docs are accurate. Two things are worth a follow-up, neither blocking: the test that catches a missing `{ signal }` re-check (my mutant M1) is now weaker, and used removal listeners keep their targets alive (C123's kind, same as the base). The delta in `pts/lib/internal/abort.pts` is your port of my prototype, and `http.pts` hasn't changed since round 3.

## Verified
- **The `used` flag across threads** (`probes/used_flag_threads.pts`): 300 rounds × 200 targets. One thread dispatches 5 untrusted `abort` events to the signal, a second runs the real abort, and a third adds the second half of the `{ signal }` listeners at the same time. Result: listeners left 0, thrown 0, at 1, 2, 4 and 8 threads, 3 runs each.
  - When two dispatches both see `used` false, both call `removeEventListener` with the same type and callback, which does nothing the second time.
  - The only odd case needs a three-way race: the program adds the same callback back on a third thread between those two removals.
- **Cost:**
  - `abort_listener_signal_option_cost.pts` takes 0.14–0.15 s and 65 MB here; your 0.76 s figure probably reflects load.
  - Round 3's `resist.pts` still matches Node line for line.
  - `abort_signal_shared_threads` passes 5 of 5 at each of 1, 2, 4 and 8 threads.
- **Docs:** D-185's `{ signal }` bullet and SPEC §9.127 (`#append`, used up by a flag, the cost test) match what I measured.

## Worth a follow-up (low)

**1. The test for a missing `{ signal }` re-check is weaker now (verified).**
- I rebuilt M1 (no re-check after adding) on round 4's `abort.pts`. Against the current `abort_signal_shared_threads.pts` it's caught 0 of 10 runs at 2 threads and 6 of 10 at 4. In rounds 2 and 3 it was 18 of 20 and 7 of 10.
- The cause: the removal listener is now appended with no scan, so adding is fast and the window it opens is short.
- A swept section like the existing "a follower against an abort a moment later" fixes this (`probes/signal_option_window.pts`): 2,000 rounds where one thread adds a `{ signal }` listener while the other aborts after a pause of `(round × 7919) % 300` spins.

| Threads | M1: listeners left (3 runs) | Fix: listeners left |
|---|---|---|
| 1 | 0, 0, 0 | 0 |
| 2 | 0, 0, 0 | 0 |
| 4 | 7, 13, 28 | 0 |

It adds about 0.2 s. A CI pass runs threads 1, 2 and 4, so the 4-thread run would catch M1 every time.

**2. Used removal listeners stay in an aborted signal's list and keep their targets alive (verified; same as the base and round 2).**
- `probes/retention.pts`: 3 batches of 20,000 targets add listeners with one signal and are dropped, then that signal aborts but stays referenced, then 3 more batches use a second signal.

| | Before abort | After abort | After 3 more batches |
|---|---|---|---|
| Round 4 | 50 MB | 58 MB | 102 MB |
| Base | 50 MB | 60 MB | 92 MB |
| Round 3 | 50 MB | 112 MB | 114 MB |

  Round 4 and the base keep the first batches; round 3 released them and reused that memory.
- **Cheap fix if wanted:** after the real abort event's dispatch, drop the `resists` registrations from the signal's list in one O(n) filter under its lock. They have all run by then. A listener added after the snapshot was already taken off its target by the re-check, so dropping its removal listener is safe.
- **The bigger case is C123's:** a long-lived signal that never aborts keeps a removal listener for every `{ signal }` add, each holding its target, in every version. Node's holds the target weakly. D-185's known gaps describe C123 only for dependents; a clause about `{ signal }` removal listeners would complete it.

I wrote nothing to the repo. Archive: `/tmp/h07b/review4/h07b-review4-probes.tar.gz` (sha256 `c2789fdb…1fc5`; probes, logs, tools, the M1 source and overlay, `cmds.txt`; no binaries). `/tmp/h07b/review4/DONE` is touched.
