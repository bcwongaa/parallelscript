# H06b reproducer report

Written by the `opus-reproducer` subagent (claude-opus-5-5, xhigh) on 2026-10-17 in its own worktree `.claude/worktrees/agent-ae746c1dbf6eb5660` (base `aa13ed5b0`, nothing committed). The harness wouldn't let it write report files, so it returned this report as its final message; the parent saved it here unchanged apart from formatting. Its probes, proposed tests and receipts: [h06b-reproducer-probes.tar.gz](h06b-reproducer-probes.tar.gz) (sha256 `aeb3cd04dfea66a3b6c42c9cac68ce154cb26f6fcbf99a7c533223bd1e6fe19e`, 92 entries). Claims here are the child's; the parent's own runs are in [h06b.md](h06b.md).

## Summary

- **RED tests:** all 9 fail for their defect, as 18 stdout diffs; every RangeError is at a hole index. Two expectations need a design call (section 1).
- **Matrix:** the H06b families total **1,895** cases on the base, the number the fix must drive to 0. Another 108 cases are wrongly counted as by design because H06's classifier predates D-182 (DQ4).
- **New defect families** outside the ledger:
  - F13: `.par` views at holes.
  - F14: callbacks that change the array's length. Through `any`, a forEach that pushes never ends.
  - F15: array patterns at holes.
  - F16: `new Set(array)` drops holes.
  - F17: typed callbacks over match arrays ignore a parameter type that takes undefined.
  - Also `Object.hasOwn` at holes, `Array.from` with a mapper, and typed `Object.values` of a match copy.
- **Proposed tests:** 10 new RED tests, all failing for their reason in the harness. The compiler smoke test over them passes.

## Setup

- **Worktree:** `/home/bryan/projects/parallelscript/.claude/worktrees/agent-ae746c1dbf6eb5660`, base `aa13ed5b0`, clean at the start.
- **Submodules:** ryu, bdwgc, libuv, libbacktrace and pcre2, each initialised with `--reference …/.git/modules/pts/third_party/<lib>`. All five exited 0.
- **Build:** `GOMAXPROCS=2 make build` exited 0. `pts-bin/pts` sha256 is `79c64033…1ff0`.
- **Toolchain:** go1.26.9, Node v24.21.0, clang 18.1.3.
- **How programs ran:** `PTS_HOME` was the worktree, with Node and Go first on PATH and `TZ=America/New_York`; Node ran a `.mts` copy with `--no-warnings --experimental-transform-types`; each native binary ran under a 4 GiB address-space cap and a 60 s timeout; `.par` probes compare with a sequential Node twin (`<stem>.oracle.mts`).
- **Jobs:** never more than 2 heavy jobs at once. The matrix ran with `--jobs 1` while probes ran one at a time; its results don't depend on the job count.

## Commands (exit codes)

| # | Command | Exit |
|---|---|---|
| 1 | submodule init ×5 | 0 ×5 |
| 2 | `GOMAXPROCS=2 make build` | 0 |
| 3 | `tools/red.sh` = `GOMAXPROCS=2 PTS_REQUIRE_NODE=1 go test -count=1 -parallel 2 -timeout 40m -json -run '^(TestRun\|TestRunMatchesNode)$/^(<9 names>)$' ./pts/tests/` | **1** (18 fail, stderr empty) |
| 4 | `tools/pair.py red-msgs/*.pts` (copies that also print `e.message`) | 1 (every RangeError is at a hole index) |
| 5 | `python3 -I h06-probes/gen.py --out /tmp/h06b/repro/matrix --seed 0` | 0 (240 programs, 17,748 cases) |
| 6 | `python3 -I h06-probes/run_matrix.py --dir /tmp/h06b/repro/matrix --out /tmp/h06b/repro/matrix-base.json --scratch /tmp/h06b/repro/matrix-work --jobs 1` (7 minutes) | **0** |
| 7 | `tools/families_run.py`, `tools/breakdown.py` | 0 |
| 8 | `tools/all_probes.sh` (19 probes) | 1 (probes differ, as expected) |
| 9 | `h06-probes/mkexpect.py --write` ×9; `check_expect.py --oracle-dir red-tests` | 0; **0** (every expect block is Node's stdout) |
| 10 | `tools/newred.sh` (the 10 proposed tests in `pts/tests/run/`) | **1** (23 fail, 2 skip by design) |
| 11 | `PTS_SMOKE_FILTER=… go test -run '^TestCompilerFailsCleanly$' ./pts/tests/` | **0** (20 programs, no ICE) |

## 1. RED confirmation

Every one of the 18 subtests fails with `assertion failed` on stdout: 18 `got.stdout` diffs, with no exit-code, compile, panic or signal failure. Node's output equals each test's expect block. The lines that already match are each test's own controls.

| Test | TestRun / MatchesNode | What pts prints instead |
|---|---|---|
| array_callbacks_skip_holes | fail / fail | all 22 lines `threw RangeError` (F5) |
| any_array_callbacks_skip_holes | fail / fail | 9 of 11, e.g. `map [ 'undefined!', 'b!', 'undefined!', 'd!' ]`, `reduce undefinedbundefinedd`, `only holes reduce no initial NaN`; filter and find match (F6) |
| array_find_holes | fail / fail | 7 of 8 `threw RangeError`; `via any` matches (F7) |
| array_search_undefined_holes | fail / fail | `typed false -1 -1`, `any[] false -1 -1`, `via any true 1 3`, `via any with undefined true 1 3`, `strings via any true 0 0` (F8) |
| array_keys_skip_holes | fail / fail | 8 of 9, e.g. keys `[ '0', '1', '2', '3' ]`, values throw `TypeError Expected string[] / P[], got Array` (F9) |
| array_in_skips_holes | fail / fail | `true true true false` / `true true true` / `true true true` |
| array_iteration_reads_holes | fail / fail | spread, from and values keep `<1 empty item>`; entries and for…of throw RangeError (F10) |
| any_concat_keeps_holes | fail / fail | `[ undefined, 2, undefined, 4 ] …`, `[ 'a', undefined, 'b' ]` (F11) |
| match_spreads_keep_undefined | fail / fail | 10 of 13 put `<1 empty item>` for the group; `via any filter [ 'b', 'b' ]` drops it (F12) |

**Expectations that need a decision:**
1. **`points map [ <1 empty item>, 1, <1 empty item> ]`** is impossible under D-085, because the result is a `number[]`. The D-085-consistent result is `[ 0, 1, 0 ]`. Suggestion: change the callback to `"p" + p.x` and add the proposed `no-diff` test for number and boolean results.
2. **`push`, `push copy`, `unshift` and `String.match push`** in match_spreads_keep_undefined push into an *existing* `string[]`. They only work if that array switches to the `pts_type_match_copy` descriptor in place. There are precedents: D-180's `push_leaf` switches new arrays (`array.c:1085–1092`), and freeze swaps an object's type atomically (`object.c:577`). This is not representable if the receiver already has holes (DQ2). The other lines are new arrays and representable.

## 2. Matrix on the base (240 programs, 17,748 cases: 14,815 OK, 2,768 WRONG, 165 DIAG, nothing else)

| Family | H06 final | **Base** |
|---|---|---|
| F5 typed callbacks throw at holes | 540 | **540** |
| F6 callbacks through `any` visit holes | 433 | **433** |
| F7 typed `find*` throw at holes | 156 | **156** |
| F8 search for undefined | 135 | **135** |
| F9 own keys and for…in | 360 | **360** |
| F10 iteration: spread, from, values, entries, for…of | 407 | **195** (39 per method; H06a fixed the 210 toReversed/toSpliced/with cases) |
| F11 concat through `any` | 60 | **60** |
| F12 match spreads | 16 | **16** |
| **H06b total** | 2,107 | **1,895** |

By-design buckets (H06 final → base): D-085 332 → 332; UNSUPPORTED (`copyWithin`) 330 → 330; UNREPR 237 → 137; REQUIRED 126 → 126; D-133 96 → 96; D-086 7 → 7.

- **Classifier artifacts (10 cases).** The residual sort cases (F1+F3 4, F2+F3 4) and 2 F10 `toSpliced` cases are all `cls`/`obj` `dH` cases. They are D-182 by design: console.log wraps 4 and 5 items differently, which defeats `classify.py`'s comparison. With whitespace collapsed, all 10 are UNREPR.
- **Union element types (108 cases, DQ4).** `(string|null)[]`, `(number|null)[]`, `(P|null)[]` and `(number|string)[]` contribute 36 cases now bucketed UNREPR (spread, from, values) and 72 bucketed REQUIRED (find*, for…of, entries). Under D-182's rule that arrays of unions hold dynamic values, these are defects. H06a's toReversed and with already give undefined for these types.
- **Re-run against a fixed tree.** `h06-probes/` must sit in the tree under test, because run_matrix uses `../pts-bin/pts` and `PTS_HOME`:
  ```sh
  tar xzf docs/agent/hardening-evidence/h06-reproducer-probes.tar.gz -C <tree>
  python3 -I <tree>/h06-probes/gen.py --out /tmp/h06b/matrix --seed 0
  python3 -I <tree>/h06-probes/run_matrix.py --dir /tmp/h06b/matrix --out /tmp/h06b/matrix-fixed.json --scratch /tmp/h06b/matrix-work --jobs 2
  python3 -I /tmp/h06b/repro/tools/families_run.py <tree>/h06-probes /tmp/h06b/matrix-fixed.json
  python3 -I /tmp/h06b/repro/tools/breakdown.py <tree>/h06-probes /tmp/h06b/matrix-fixed.json /tmp/h06b/repro/results/matrix-base.json.gz
  ```
  `families_run.py` exists because `families.py` imports from its own directory, which `-I` doesn't put on the path. The base JSON is `results/matrix-base.json.gz`, sha256 `f22dab07…0187`.

## 3. Probe results (`results/probes-all.txt`)

Classes: **D** is a defect, **BD** is by design, **DIAG** is an honest diagnostic, **OK** already matches Node.

### `.par` views (F13, new, D)
The library reads `items[i]` at every index; only flatMap tests `pts_parallel_has`. Output is identical at 1, 2 and 4 threads.
- **`string[]` and `P[]` throw at the first hole** in map, forEach, filter, some, every, find, reduce and count, small and over 6000 slots. For example `strings map` throws `RangeError … index 0` where the twin gives `[ <1 empty item>, 'b!', … ]`.
- **`(T|undefined)[]` and `any[]` pass holes to the callback:** `maybe count` twin 1, pts 3; big `maybe count` twin 572, pts 2572; `maybe filter` twin `[ 'a', undefined ]`, pts `[ 'a', undefined, undefined, undefined ]`; `maybe forEach` (marks) twin `[ 1, 0, 1, 0 ]`, pts `[ 1, 1, 1, 1 ]`.
- **sum, min and max over a `string[]` with holes give NaN;** the twin gives 7, 2 and 5.
- **Match arrays with a parameter that takes undefined:** `m.par.map(…)` throws TypeError where the twin gives `[ 'b', 'u', 'b' ]`.
- **BD:** big `points find` throws RangeError where Node throws TypeError (the REQUIRED rule).

### Changing the length mid-iteration (F14, new, D)

| Case | Node | pts |
|---|---|---|
| typed map after `length = 2` | `[ 'a!', 'b!', <2 empty items> ]` | `[ 'a!', 'b!' ]` |
| typed find over `(string\|undefined)[]` after shortening (visited) | `[ 'a', 'b', 'undefined', 'undefined' ]` | `[ 'a', 'b' ]` |
| typed findLastIndex after shortening (visited) | `[ 'd', 'undefined', 'undefined', 'a' ]` | `[ 'd', 'a' ]` |
| `any` map after shortening | `[ 'a!', 'b!', <2 empty items> ]` | `[ 'a!', 'b!' ]` |
| `any` reduce after shortening | `ab` | `abundefinedundefined` |
| `any` reduceRight after shortening | `da` | `dundefinedundefineda` |
| `any` some after shortening (visited) | `[ 0, 1 ]` | `[ 0, 1, 2, 3 ]` |
| `any` forEach after pushing | 4 visits | 6 |
| `any` filter after pushing | 4 elements | 6 |
| `any` every after pushing | 4 | 6 |
| `any_grow`: forEach that pushes, guarded at 100,000 | `2 4` | `100000 100000` |

The last row is the important one: without the guard, a forEach through `any` that pushes never ends in pts. Typed forEach, filter and reduce (shortening) and typed map, forEach and every (pushing) already match Node.

### reduce at the edges, holes only, map to numbers (F5 and F6, D)
- **reduce without an initial value, first and last slots holes:** Node `bc`. pts typed throws RangeError; through `any` it gives `undefinedbcundefined`.
- **every and some over holes only:** Node `true` and `false`. pts typed throws RangeError; through `any` it gives `false` and `true`.
- **reduceRight with no initial value over holes only:** Node throws TypeError, pts typed throws RangeError.
- **`map((x) => x.length)` over holes:** Node `[ <1 empty item>, 1, 1, <1 empty item> ]`. The D-085 result would be `[ 0, 1, 1, 0 ]`, and `[ false, true, false, false ]` for booleans. Through `any` the holes stay representable; pts throws TypeError there today (F6).

### Match-array callbacks (F17, new, D)
D-133 says a group that took no part reaches "a parameter that takes undefined" as undefined.
- `m.map((x: string | undefined) => x ?? "u")` throws D-133's TypeError where Node gives `[ 'b', 'u', 'b' ]`; on a copy it throws RangeError. forEach, some, every, filter, findIndex and reduce behave the same way.
- **BD:** a callback with tsc's `string` parameter throws D-133's TypeError.
- **OK through `any`:** every method, plus `indexOf(undefined)` gives 1. These are guards: the F6 and F8 fixes must keep a match's zero slot as present.

### Own keys and HasProperty
- **D: `Object.hasOwn(holes, i)`** gives `[ true, true, true ]`, Node `[ false, true, false ]`, typed and through `any`.
- **D: typed `Object.values(m.slice())` and `Object.entries(m.slice())`** throw `TypeError Expected string[], got Array`, Node `[ 'b', undefined, 'b' ]`.
- **BD (D-086 analogue):** typed `Object.entries(m)` of exec's array.
- **OK on match arrays and copies,** typed and through `any`: `Object.keys`, `in`, `hasOwn` and for…in. JSON, join and String of holes are also OK.
- **DIAG:** `Object.assign({}, holes)` and object spread of an array are PTS1001.

### Through `any` and into `any[]`
- **OK:** spread, `Array.from`, `Array.from` with a mapper, for…of, entries, values, keys, iterator `next()` and destructuring through `any`; typed holes spread or pushed into `any[]`/`unknown[]`; `console.log(...holes)`.
- **D (F10):** the typed `maybe().entries()` iterator used as a value throws RangeError.

### concat
- **OK:** typed concat, with holes in either the receiver or the arguments; match receivers.
- **D (F11):** through `any`, holes become undefined. `viaAny.concat(m)` must keep the holes and the match's undefined in one result.
- **UNREPR (DQ2):** typed `strings().concat(m)`, because a `string[]` can't hold both holes and undefined.

### Adjacent
- **OK:** `at`, `indexOf`/`lastIndexOf` with a start index, `fill`, `keys()`, `toString`.
- **D (F8):** `includes(undefined, from)`.
- **D (F10):** `Array.from(maybe(), f)` and `Array.from(anyArr, f)` throw RangeError; the `string[]` case is the REQUIRED rule. `new Map(maybe().entries())` throws RangeError.
- **D (F15, new): array patterns.** A rest element over `(T|undefined)[]` keeps holes; `const [p = "P"] = strings()` throws RangeError where Node gives `'P'`; `[p, q = "D"] = maybe()` throws RangeError; a match default (`[, g = "D"] = m`) throws TypeError, or RangeError on a copy.
- **D (F16, new):** `new Set(maybeHoles())` gives `Set(1) { 'b' }`, Node `Set(2) { undefined, 'b' }`. The same happens for `any[]` and for match arrays.
- **D under DQ4:** `(string|null)[]` and `(number|string)[]` spread, from and values keep holes; entries, for…of, find*, map and forEach throw RangeError. In the same program, toReversed and with give undefined.
- **DIAG:** `delete a[i]` is PTS1002, and its message "arrays have no holes" has been stale since D-085; casts of kept arrays and `flat` of mixed arrays are PTS1001 (D-094); an array-pattern default over a `(T|undefined)[]` element is PTS1001.

## 4. Code responsible (from reading the code)

- **Typed callbacks, F5, F7 and F17** (`pts/lower/callbacks.go`): `loopFrom` (387) tests `present` as `index < length` only (403); `pts_array_has` is used only for flatMap (404). The element is read by `h.element` (565–570) as `ArrayElementChecked`, into a local of tsc's element type (407), before the arguments are converted. map builds its result by `push` (257), so the result loses holes and length. reduce without an initial value starts at slot 0 or `length - 1` (363). `find` (326). `Array.from(a, f)` is the `"map from"` helper (156, 425) and shares map's body, so skipping holes for map must not reach it.
- **Dynamic path, F6 and F14** (`pts/runtime/src/dynamic_methods.c`): `call_back` (582) reads `pts_array_element`, so a hole is undefined; `array_for_each` (587) and `array_every` (666) skip nothing and test the live length at each step; `array_map` (595) pushes; `array_filter` (604) tests the live length and skips by `pts_array_load`, which also drops match groups; `array_some` (662) reuses `find_index` (632), which visits holes; `reduce` (677) skips nothing.
- **Search, F8:** typed: `array.c` `find` (625, test at 633) skips holes for `pts_array_includes` (659) too; dynamic: `search_forward` (492) and `array_last_index_of` (511) read holes as undefined.
- **Own keys and `in`, F9:** `own_keys` (`dynamic_methods.c` 1157, array case around 1179) lists every index; it is behind Object.keys, values and entries, for…in (`records.go` `forInKeys` 618) and `pts_value_has_own` (1321). `in` goes to `pts_dynamic_has` (`dynamic.c` 682, test at 696), reached from `union_objects.go` `computedKeyIn` (293). The fix should use `pts_array_has`, which keeps match groups as present.
- **Iteration, F10:** spreads lower through `arrays.go` `spreadArray` and `spreadPart` (133, 171) to the C emitter's concat (`cgen/arrays.go` 122, 128), which calls `pts_array_append` (`array.c` 580), copying zero slots as they are. `Array.from(a)` is `ArraySlice` (`arrays.go` 1339). `[...a.values()]` is the array itself (`array_library.go` 233). `entriesHelper` (264) reads checked. for…of reads `ArrayElementChecked` (`arrays.go` 1452).
- **concat through `any`, F11:** `array_concat` (`dynamic_methods.c` 458) appends `pts_spread_element` (`dynamic.c` 756), which reads a hole as undefined.
- **Match spreads, F12 and C78:** `pts_array_append` copies a match's zero slots into a `string[]`, where zero is a hole. The fix is `push_leaf`'s switch to `pts_type_match_copy` (`array.c` 1091). Through `any`, `array_filter` drops groups (above). `.par.filter`'s join, `push(...right)` (`parallel.pts` around 65), has the same problem.
- **`.par`, F13** (`pts/lib/parallel.pts`): `map` (33), `forEach` (43), `filter` (53), `reduce` (73, whose piece starts at `items[from]`), `count` (103), `sum`, `min` and `max` (118–157), and `findFound` (222, used by some, every and find) all read every index. Only `flatMap` (167) tests `pts_parallel_has`.
- **Array patterns, F15:** `destructuring.go` `elementPart` (457). A rest element is `ArraySlice` (472). An element with a default is `inRange ? ArrayElementChecked : fallback` (477–489).
- **Set of an array, F16:** `map.c` `pts_set_of_array` (260) adds only where `pts_array_load` succeeds. It is lowered from `maps.go` `newMapOf` (162) and `cgen/maps.go:94`.

## 5. Proposed RED tests (`red-tests/` in the archive)

All fail in the harness for their defect. Each failure is a stdout diff; the two skips are by design (a pts feature, and `no-diff`).

| Test | Family | TestRun / MatchesNode |
|---|---|---|
| `parallel_views_skip_holes.pts` (`// threads: 1 2 4`), with twin `.oracle.mts` and `.twin.diff` | F13 | fail at 1, 2 and 4 threads (identical diffs) / skip |
| `array_callbacks_length_once.pts` | F14 | fail / fail |
| `array_from_mapper_reads_holes.pts` | F10 (Array.from with a mapper; guards the shared helper) | fail / fail |
| `array_patterns_read_holes.pts` | F15 | fail / fail |
| `set_of_array_holes.pts` | F16 | fail / fail |
| `array_has_own_skips_holes.pts` | hasOwn and match-copy values, plus match guards for C90 | fail / fail |
| `match_callbacks_wide_parameters.pts` | F17 | fail / fail |
| `union_arrays_iteration_reads_holes.pts` | DQ4 | fail / fail |
| `any_concat_match_groups.pts` | F11 with a match guard | fail / fail |
| `no-diff/array_map_numbers_at_holes.pts` | D-085 replacement for the `points map` line | fail / skip |

The no-diff test's expect block is the reproducer's D-085 derivation, not Node's output. Its first line, `[ [ 0, 0 ], [ false, false ] ]`, is pts's executed D-085 behaviour. Some tests depend on a design question: the `.par` test on DQ3, the union test on DQ4, the match-callback test and the match lines of the patterns test on DQ5. If a question goes the other way, drop the affected lines rather than editing expectations.

## 6. Design questions and recommendations

1. **DQ1: map results of `number[]` or `boolean[]` over holes.** Recommend D-085's 0 or false slots, with the callback not called there. Fix the `points map` line and add the no-diff test.
2. **DQ2: match groups going into a `string[]` that isn't new.** New results become match copies, as D-180's flatten results do. An existing receiver *without* holes switches to the match-copy descriptor atomically, as freeze swaps twins. A receiver with holes, and typed `holes.concat(m)`, keep the group as a hole: document as a D-182 extension and add it to REVIEW_LATER.
3. **DQ3: `.par` at holes.** map keeps holes; forEach, filter, some, every and count skip them; find skips them too (its value can't differ: a hole and "not found" are both undefined). sum, min and max fold only the elements there are, as `.par.reduce` equals `reduce` (D-151); today they give NaN. reduce's pieces need an "empty piece" result for pieces that are all holes.
4. **DQ4: unions without undefined.** Apply D-182's union rule wherever JS reads with Get: spreads, from, values, entries, for…of, find*, Array.from with a mapper, patterns and Set. This adds 108 matrix cases to H06b. Also stop counting the union reps (clsN, numN, sn, strN) as element types without undefined in `classify.NO_UNDEFINED`.
5. **DQ5: match groups and typed callbacks or patterns.** Where the parameter type holds undefined, read the element as a dynamic value (D-133's own "a parameter that takes undefined" rule); treat a pattern default as a context that tests undefined; keep D-133's TypeError for tsc's `string` parameter.
6. **DQ6: changing the length mid-iteration.** Read the length once on both paths and test `pts_array_has` at each index. map's result keeps the original length. find* visit every original index.
7. **DQ7: `new Set` of a `string[]` with holes, or of a match array.** A `Set<string>` can't hold undefined. Recommend D-085's RangeError at a hole and D-133's TypeError at a group, instead of silently dropping the element. For `(T|undefined)[]` and `any[]`, adding undefined is the F16 fix.
8. **DQ8: Object.values and entries of a match copy** should give match-copy results. Exec's own match array stays under D-086.
9. **Minor:** reword the stale PTS1002 message for `delete`, and log array-pattern defaults over `(T|undefined)[]` elements as a PTS1001 gap.

## Files (in the archive)

- `probes/`: the 19 probes and their `.oracle.mts` twins.
- `red-tests/`: the 10 proposed tests.
- `red-msgs/`: copies of 5 RED tests that print `e.message`.
- `tools/`: `pair.py`, `red.sh`, `newred.sh`, `matrix.sh`, `families_run.py`, `breakdown.py`, `bucket_reps.py` and the others.
- `results/`: `red.jsonl`, `red-diffs.txt`, `red-msgs-pair.txt`, `matrix-base.json.gz`, `families-base.txt`, `breakdown-base.txt`, `probes-all.txt` and `.json`, `newred2.jsonl`, `newred2-diffs.txt`, `smoke.txt`.
