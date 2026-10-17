# H06b review rounds

Independent `opus-reviewer` (claude-opus-5-5, xhigh), read-only. Each round reviewed a byte-checked snapshot worktree of the uncommitted change (`/tmp/h06b-review1`, `/tmp/h06b-review2`, …, base `aa13ed5b0`). Rounds 2 and later were sent to the same agent, so it kept its probes and context. The harness blocks report files, so each report came back as a message, which the parent saved here; the parent's resolution follows each finding. Probes: round 1 [h06b-review-probes.tar.gz](h06b-review-probes.tar.gz) (sha256 `badd04d5…7bbd`; `cmp.sh` runs Node, the new CLI and the base CLI on a probe).

## Round 1 (snapshot `/tmp/h06b-review1`): not ready

The reviewer's verdict: one blocker (R1-1). R1-2 and R1-3 are smaller regressions to fix in the same round, and R1-4 and R1-5 are performance regressions worth fixing.

### Regressions introduced by the change

- **R1-1 (blocker): a callback whose element parameter takes undefined no longer compiled for `filter`, `find!`/`findLast!` and `reduce`/`reduceRight` without an initial value.**
  - Cause: the widened element was converted back to the element or accumulator type with `l.convert`, and a union with undefined doesn't convert to its narrower type, so the helper failed to build.
  - Evidence: `p12_isdefined_idiom.pts` (`ps.filter(isDefined)` on a `P[]`, with `isDefined<T>(x: T | undefined): x is T`) gave `PTS1001: converting P | undefined to P is not supported yet`, exit 1, where the base and Node print `2`. `p09` (reduce) failed the same way. So did p08, p10, p11, p15, p20, p23 and p28, all of which the base runs and Node matches.
  - **Resolution, fixed.** `widened()` now applies only where it changes what the callback sees:
    - find and its kin and `Array.from`'s mapping over string or object arrays, which visit holes;
    - every method over a `string[]`, for match groups;
    - only for a parameter that is a union with undefined or `unknown`/`any`.
    
    Widened reads are the inline word read typed as the parameter (`ArrayElementOrUndefined` with `As`). `narrowedElement` converts back through a dynamic value. Regression test `run/array_callbacks_wide_parameters.pts` (expect = Node) is PTS1001 on the round-1 CLI (`red/red-r1-1.txt` in the parent archive). The listed probes now match Node.
- **R1-2 (should-fix): a match's undefined group added to a `string[]` that already has holes turned those holes into undefined.**
  - Cause: `keep_undefined_groups` switched the whole receiver to `pts_type_match_copy`.
  - Evidence: `p02_switch_real_holes.pts`. After `push(...m)`, keys listed every index, `0 in a` was true and printing showed undefined; `forEach` made 3 calls where Node makes 2.
  - **Resolution, fixed.** `takes_undefined_groups` decides before the elements go in and switches only a receiver without holes of its own. Otherwise the group becomes a hole there, documented in D-183 as the reproducer's DQ2 proposed. The no-diff test `run/match_groups_into_holes.pts` shows the round-1 CLI's wrong keys and a RangeError in `forEach` (`red/red-r1-2.txt`).
- **R1-3 (should-fix, race): the descriptor switch could unfreeze an array frozen at the same time.**
  - Cause: a relaxed store after a check, without a compare-and-swap.
  - Evidence: `race/freeze_race.pts` joins `a.push(...m)` with `Object.freeze(a)` 300,000 times. At `PTS_THREADS=2` the round-1 build left 4,234, 2,999 and 5,429 arrays unfrozen; the base left none.
  - **Resolution, fixed.** The switch is a compare-and-swap from the plain descriptor, so a racing freeze's twin wins and the groups are then holes. Freeze itself is unchanged. Stress test `stress/race_freeze_match_push.pts` (`threads: 2 4`): 0 unfrozen now, and 478, 595 and 371 per 50,000 rounds on the round-1 build (`red/red-r1-3.txt`).
- **R1-4 (should-fix, performance): widened reads doubled the affected loops.**
  - Cause: they went through `pts_array_element` and `pts_value_of_slot` plus a checked conversion.
  - Evidence: callgrind on `bench/widened.pts`: 608M → 1,168M instructions (+92%).
  - **Resolution, fixed.** Widened reads are the inline word read with no conversion (a pointer slot holds its own dynamic value, D-002): +0.7% against the base.
- **R1-5 (should-fix, performance and scaling): pts:parallel scanned for holes on one thread before forking.**
  - Evidence: callgrind on `bench/parscan.pts` (`.par.count` over 2M strings, 30 rounds): +11.6% instructions. At 4 threads the wall clock was about +11% (noisy).
  - **Resolution, fixed in part.** Each piece now scans its own slots inside the leaf (`pts_parallel_has_holes_in`, four slots per test). `reduce` picks its path by element kind in O(1).
  - Remaining cost on this worst case (a trivial predicate): +7.1% instructions at one thread, and at 4 threads a mean of 198 against 184 ms over 8 interleaved runs (noisy). It is recorded in D-183; a sticky "may have holes" flag is the later option (H27).
- **R1-6 (note): typed callback loops over pointer arrays pay about 8 more instructions per element** (`bench/loops.pts` +1.2%). Recorded in D-183. On the final tree `loops` measures +0.1%.

### Documentation, tests and code quality

- **R1-7:** an optional parameter `(p?: P)` isn't widened for object arrays. Recorded in D-183 as a known gap.
- **R1-8:** "an array gains no holes once made" was false: a spread of another array's holes (`push(...a)`, `unshift(...a)`) adds them. Fixed in `pts.h`, `parallel.pts`, `dynamic_methods.c` and D-183.
- **R1-9:** `run/parallel_callbacks_skip_holes.pts` said `.par` find visits holes. Fixed: it skips them.
- **R1-10:** `pts/ir/ir.go` needed gofmt. Fixed.
- **R1-11:** a stale pointer comment in `pts.h`. Removed.

### Pre-existing or by design (recorded)

- Number results of `map` at holes are silent zeros (D-085). The base threw RangeError there.
- `new Set` of a `string[]` with holes, or of a match array's group, leaves them out (D-130).
- A typed `find` over a `string[]` or number array whose callback shortens the array stops at the current length (`p06`). Recorded in D-183.
- Match copies give RangeError where exec's array gives D-133's TypeError (C97, H22).

### Verified correct by the reviewer

- The base crash is real and fixed: the base exits 139 on `nullable_copies_keep_holes`. Rejecting DQ4 is sound.
- The 22 new and changed tests are honest. The two no-diff tests differ from Node only as their headers say, and the `.par` tests match sequential Node twins at 1, 2 and 4 threads.
- Through `any`, `p16` (every method at holes, with mutating callbacks) is identical to Node.
- Type soundness: `any` converts to `any[]` by a copy, so a static element type that holds undefined always means an array of dynamic values. Identity survives widened reads (`p14`), and rest parameters, mixed spreads and own-keys consumers match Node (`p19`, `p22`, `p21`).
- Races: `.par` callbacks that sort, unshift holes or shorten the array never crashed at 2 and 4 threads.
- The runtime unit tests and the lower/cgen tests pass.

## Round 2 (snapshot `/tmp/h06b-review2`): not ready

Probes: [h06b-review2-probes.tar.gz](h06b-review2-probes.tar.gz) (sha256 `f34758d8…1a53`).

- **R2-1 (blocker, regression, memory safety): widened reads put an element in a parameter slot whose type is stored differently.**
  - Cause: round 2's `ArrayElementOrUndefined{As: param}` handed the slot's word to the callback with no conversion. That breaks for parameters of a wider array, tuple, function or Map type.
  - Evidence, SIGSEGV where Node runs: `q13`, a `[number, string][]` read as `(number|string)[]`. `q14`, a function called without its adapter (the base printed `number 42`). `q17`, a `string[][]` given undefined.
  - Evidence, type confusion and GC hazard: `q12` pushed a string pointer into a `number[]`'s unscanned buffer and printed `4.75e-310`. `q15` did the same with a Map.
  - Wrong results: `q02` and `q03`.
  - **Resolution, fixed.** `widened()` requires the parameter to hold the element as it is: a dynamic value, or a union with undefined for which `holdsAsItIs(param, elem)` holds. All other parameters read the element typed and convert it as the base does. The reviewer's q00–q24 now give exactly the base's output (`/tmp/h06b/r2check`). New tests:
    - `run/array_callbacks_converted_parameters.pts` passes, as on the base, and exits 139 on the round-2 build (`red/red-r2-1-run.txt`);
    - `errors/callback_parameters_kept.pts` (q12/q13/q15/q17's shapes stay PTS1001) was compiled by the round-2 build (`red/red-r2-1-errors.txt`).
- **R2-2 (should-fix, performance): `.par.reduce` over pointer arrays without holes was about 65% slower.**
  - Cause: a `T | undefined` accumulator and a checked `as T` for each element.
  - Evidence: `bench/parreduce_t.pts` +26% in instructions, and 152 → 253 ms at 4 threads.
  - **Resolution, fixed in part.** The leaf scans its piece, skips to the first element there is and folds with a typed accumulator. The result is +10.0% in instructions (the scan and the in-loop test); at 4 threads the means are 139 and 139 ms over 6 interleaved runs. Recorded in D-183.
- **R2-3 (note): the receiver scan in pushes of a match is quadratic** for a long `string[]` with a hole far from its start (`bench/quad2.pts`: 5 → 899 ms). Recorded as C115 (H27).
- **R2-4 (note): a match array or copy, or a switched `string[]`, reads holes that `concat` adds later as undefined** (`q18`; `m.concat(holey())` did so on the base). Documented in D-183 and as C113 (H22).
- **R2-5 (note): docs overclaimed.** D-183's widening sentence, its pts:parallel paragraph, README and GUIDE, and the evidence draft were all fixed.
- **R2-6 (note): missing ledger rows.** Added C111 (optional parameters, R1-7), C112 (typed find whose callback shortens a `string[]` or number array, p06), C113 (R2-4) and C114 (frozen pop/shift message, pre-existing), plus C115 for R2-3. The ranged scan now has a unit test covering sub-ranges and the four-slot tail.
- **Verified by the reviewer:**
  - R1-1: p08–p28 and more shapes equal Node.
  - R1-2: `p02` matches what is documented.
  - R1-3: 0 unfrozen arrays.
  - R1-4: +0.7%.
  - R1-5: the scan runs in the leaves, and 2,691 single-hole placements match the sequential methods at 1, 2 and 4 threads.
  - pop/shift (`q10`) equal Node apart from D-085's and the base's frozen messages.
  - The new tests' expect blocks are Node's; gofmt, vet and the unit tests pass.

## Round 3 (snapshot `/tmp/h06b-review3`, the committed code): ready

Probes: [h06b-review3-probes.tar.gz](h06b-review3-probes.tar.gz) (sha256 `a5ef9cf5…bd0c`).

- **R2-1 closed.**
  - The round-2 probes q00–q24 equal the base's output, apart from the intended H06b changes (q10, q18, q22, q23).
  - A generated matrix of 49 programs (`gen/gen.py`) crosses 10 element families with four kinds of callback parameter (the same type with `| undefined`, `unknown`, `any`, and wider types), each over an array with a hole, through find, findIndex, findLast, findLastIndex, `Array.from`'s mapper, forEach, filter, some and map. Nothing crashed, compile errors appear exactly where the base has them, and every program that runs equals Node, apart from map's zeros (D-085).
  - `r01_holds_as_it_is.pts` checks identity, including writes made through the parameter.
  - The reviewer's reasoning: `holdsAsItIs` compares IR types by identity, so a mismatch only means "not widened", which takes the safe conversion path. Every type it accepts is stored as a pointer or as the same dynamic value.
- **R3-1 (note):** the C113 wording claimed spreads differ too, but a spread reads holes as undefined in JS as well. Fixed in D-183 and the ledger.
- **R3-2 (note, measurement):** `.par.reduce`'s wall clock on this host follows code layout. Recompiling the base's C with `-falign-functions=16` or `64` moves it from 266–302 ms to 220–236 ms at 1 thread. So instruction counts are the measure (`parreduce_t` +7.8% in the reviewer's build). D-183 reworded.
- **R3-3 (note):** PROGRESS cited an earlier snapshot's suite. It now cites the final tree's run (1,606 / 277 / 0), whose events go in with the commit.
- **R3-4 (note):** the README now names unions without undefined.
- **Verified:** the new tests pass in the harness (`TestErrors/callback_parameters_kept`, `TestRun`/`TestRunMatchesNode` for `array_callbacks_converted_parameters`, `TestStress/race_freeze_match_push` at 2 and 4 threads); `go test ./pts/lower ./pts/cgen` and the runtime C tests pass; gofmt and vet are clean. The runtime is unchanged since round 2, so R1-3 and pop/shift stand.
