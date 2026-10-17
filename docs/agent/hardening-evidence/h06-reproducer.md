# H06 reproducer report

- **Child:** the named `opus-reproducer` subagent (Agent call with the `opus` alias, which the session's environment maps to claude-opus-5-5; effort xhigh from `CLAUDE_CODE_EFFORT_LEVEL`), 2026-10-17 11:2x to 12:0x, in its own worktree `.claude/worktrees/agent-a47d57c03beddb3d4` (base `3999ab359`), with its own CLI (sha256 `cdc431b60b20bbc30c428d1695dabd4d4af1f34ef543fa63d814f33749625cd3`) and runtime (`PTS_HOME` its worktree). It committed nothing, pushed nothing and ticked nothing; its 17 tests and its probes stayed untracked in its worktree.
- **Its probes:** [h06-reproducer-probes.tar.gz](h06-reproducer-probes.tar.gz) (sha256 `e7050634a223052a23af4332e47382604faf8b35206a17ef4090dde497d1759c`): the generator `gen.py` and runner `run_matrix.py` (re-run with `python3 -I h06-probes/gen.py --out <dir> --seed 0`, then `python3 -I h06-probes/run_matrix.py --dir <dir> --out <json> --jobs 2`), `classify.py`, `families.py`, the repros, the parallel oracles, and `results/` (H01b JSON, `matrix-final.json.gz`, `families-final.txt`, `red.jsonl`).
- **What the parent took:** the 17 run tests, byte for byte, with these changes in the parent's tree: the eight C06 tests run (`sort_undefined_last`, `sort_null_strings`, `sort_holes`, `sort_comparator_never_undefined`, `tosorted_holes`, `sort_any_arrays`, `parallel_sort_undefined`, `parallel_sort_holes`); `array_copies_read_holes` is split, its toReversed/toSpliced/with cases staying (H06a fixes them; a `(string | null)[]` case added after review round 1) and its spread/`Array.from`/`values()`/`entries()`/`for…of` cases moved, unchanged with their Node output, to `array_iteration_reads_holes` (skipped, H06b); the other eight get a `// skip: known issue (H06b, …)` or `(H22, C93)` line; `parallel_sort_undefined`'s big case gained sum and tail checks after review round 1 (its expect block regenerated from the sequential Node twin), and both parallel tests cite the archive for their oracles. The parent also wrote `array_in_skips_holes` (skipped, C90) and the tests of review round 1's findings.

The child's final report follows unchanged.

---

# H06 (Array sort) reproducer report

opus-reproducer, claude-opus-5-5, 2026-10-17. Everything below was executed unless it is marked **suspicion**. No compiler, runtime or library code was changed; nothing was committed, pushed or ticked.

## Setup

- **Worktree:** `/home/bryan/projects/parallelscript/.claude/worktrees/agent-a47d57c03beddb3d4`, base `3999ab359` (main), clean at start.
- **Submodules:** ryu, bdwgc, libuv, libbacktrace, pcre2, each from the main checkout's module reference. All five exited 0.
- **Build:** `GOMAXPROCS=2 make build` exited 0. `pts-bin/pts` sha256 is `cdc431b60b20bbc30c428d1695dabd4d4af1f34ef543fa63d814f33749625cd3`.
- **Toolchain:** go1.26.9 linux/amd64, Node v24.21.0, Ubuntu clang 18.1.3.
- **How programs ran:** `PTS_HOME` set to the worktree. Node ran a strict-ESM `.mts` copy with `--no-warnings --experimental-transform-types`. Every native binary ran under a 4 GiB address-space cap and a 60 s timeout. At most 2 heavy jobs ran at a time.
- **Scratch:** `/tmp/h06/repro/`.
- **Probes and tools:** in `h06-probes/` in the worktree, archived as `/tmp/h06/repro/h06-reproducer-probes.tar.gz`.
- **Worktree state:** the only changes are untracked files, `h06-probes/` and the 17 RED tests in `pts/tests/run/`. No tracked file is modified.

## Commands (exit codes)

| # | Command (from the worktree) | Exit | Result |
|---|---|---|---|
| 1 | `git submodule update --init --reference …/modules/pts/third_party/<lib> pts/third_party/<lib>` ×5 | 0 ×5 | |
| 2 | `GOMAXPROCS=2 make build` | 0 | sha above |
| 3 | `python3 docs/agent/hardening-evidence/probes/h01b/run_probes.py --pts …/pts-bin/pts --node ~/.local/node/bin/node --scratch /tmp/h06/repro/h01b --out /tmp/h06/repro/h01b.json …/h01b/c06-*.pts …/h01b/parent/c06-*.pts` | **0** | 12 probes, 0 misbehaving |
| 4 | `python3 h06-probes/cmp.py h06-probes/repro/{c74_typed,c74_any,c78}.pts` | 1 | WRONG, WRONG, DIAG (`splice(…, ...m)` is PTS1001; split out) |
| 5 | `python3 h06-probes/cmp.py h06-probes/repro/c78.pts h06-probes/repro/c78_splice_spread.pts` | 1 | WRONG; DIAG PTS1001 |
| 6 | `python3 h06-probes/cmp.py h06-probes/repro/reps.pts` | 1 | only `number[]` differs (D-085) |
| 7 | `python3 h06-probes/gen.py --out /tmp/h06/repro/matrix --seed 0` (final generator) | 0 | 240 programs, 17,748 cases |
| 8 | `python3 h06-probes/run_matrix.py --dir /tmp/h06/repro/matrix --out /tmp/h06/repro/matrix-final.json --jobs 2` | **0** | see the matrix section |
| 9 | `python3 h06-probes/cmp.py --oracle-dir h06-probes/parallel h06-probes/parallel/par_*.pts h06-probes/repro/sort_*comparator*.pts` (two calls) | 1 | WRONG (parallel); DIAG (typed `sort(undefined)`); OK (`sort(5)` through `any`); WRONG (`any` `sort(undefined)` with holes) |
| 10 | `python3 h06-probes/cmp.py h06-probes/repro/conversion_holes.pts` (third version) | 1 | WRONG (conversion copies lose holes) |
| 11 | `python3 h06-probes/mkexpect.py --write pts/tests/run/<t>.pts` ×15; `… --oracle h06-probes/parallel/<t>.oracle.mts …` ×2 | 0 ×17 | |
| 12 | `GOMAXPROCS=2 go test -count=1 -parallel 2 -timeout 40m -json ./pts/tests/ -run '^(TestRun\|TestRunMatchesNode)$/^(<17 names>)$' > /tmp/h06/repro/red.jsonl` | **1** | 38 subtests fail, 2 skip (see the RED section) |
| 13 | `python3 h06-probes/check_expect.py --oracle-dir h06-probes/parallel <17 tests>` | **0** | every expect block is exactly Node's stdout |
| 14 | `PTS_SMOKE_FILTER='run/(<17 names>)\.pts' PTS_SMOKE_JOBS=2 go test -count=1 ./pts/tests/ -run '^TestCompilerFailsCleanly$'` | **0** | 34 programs (17 plus a mutant each), no ICE |

Earlier matrix runs were superseded by command 8. The first run had generator bugs: unparenthesized union array types, and tsc picking `reduce`'s non-generic overload. Both were fixed by changing the generator, never the expectations.

## A. H01b probes on the base (command 3, exit 0)

All 12 behave as their kind says: the 9 red probes still fail as claimed, and the 3 controls pass.

| Probe | Kind | Outcome | PTS vs Node (first differing lines) |
|---|---|---|---|
| c06-control-par-sort-default | control | match at 1/2/4 threads, 3/3 runs each | (`.par.sort()` with null/undefined, no holes: correct) |
| c06-control-sort-defined | control | match | |
| c06-control-sort-null-comparator | control | match | |
| c06-red-par-sort-undefined-comparator | red | mismatch, 3/3 at each of 1/2/4 threads | `last [ 5997, 5998, 5999 ]` vs `last [ undefined, undefined, undefined ]`; `comparator saw undefined: true` vs `false` |
| c06-red-sort-any-arrays | red | mismatch | `[ undefined, null, 1, 3, 'b' ]` vs `[ 1, 3, 'b', null, undefined ]` |
| c06-red-sort-holes | red | mismatch | `[ undefined, <2 empty items>, 1, 3 ]` vs `[ 1, 3, undefined, undefined, undefined ]` (toSorted) |
| c06-red-sort-null | red | mismatch | `[ null, 1, 20, 3 ]` vs `[ 1, 20, 3, null ]` |
| c06-red-sort-undefined-comparator | red | mismatch | `calls with undefined: 5` vs `0` |
| c06-red-sort-undefined-default | red | mismatch | `[ undefined, undefined, 1, 20, 3 ]` vs `[ 1, 20, 3, undefined, undefined ]` |
| c06-red-sort-undefined-other-kinds | red | mismatch | `undefined a b` vs `a b undefined`; `[ undefined, false, true ]` vs `[ false, true, undefined ]` |
| c06-red-tosorted-undefined | red | mismatch | `[ undefined, 1, 20, 3 ]` vs `[ 1, 20, 3, undefined ]`; comparator saw undefined |
| parent/c06-red-parent-sort-undefined | red | mismatch | `[ undefined, 1, 3 ]` vs `[ 1, 3, undefined ]` |

Receipt: `h06-probes/results/h01b.json` (source_sha `3999ab359…`, cli_sha256 as above).

## B. C74 and C78 reproduced (commands 4–5)

### C74 is confirmed, and it is wider than recorded

Typed, in `h06-probes/repro/c74_typed.pts`, every callback method throws at the first hole. This covers `string[]`, `P[]` and `any[]`, and the matrix adds `(T | undefined)[]`, `(T | null)[]`, `unknown[]` and union arrays. The affected methods are `map`, `forEach`, `filter`, `some`, `every`, `reduce` and `reduceRight` (with and without an initial value). `find`, `findIndex`, `findLast` and `findLastIndex` also throw at holes; see F7.

```
node: s.map [ <1 empty item>, 'b!', <1 empty item>, 'd!' ]
pts : s.map threw RangeError: Array index 0 is out of range (length 4); if it may be missing, read it with `?? fallback`, give the element type `| undefined`, or check the length first
node: d.forEach [ 0, 2 ]          (any[])
pts : d.forEach threw RangeError: Array index 1 is out of range (length 3); …
```

Through `any` (`c74_any.pts`), nothing throws, but the callbacks are called for holes, as undefined. This is wrong for `map`, `forEach`, `some`, `every`, `reduce` and `reduceRight`.

```
node: S.map [ <1 empty item>, 'b!', <1 empty item>, 'd!' ]     pts: S.map [ 'undefined!', 'b!', 'undefined!', 'd!' ]
node: S.forEach [ 1, 3 ]                                        pts: S.forEach [ 0, 1, 2, 3 ]
node: S.reduce bd                                               pts: S.reduce undefinedbundefinedd
```

Through `any`, `filter`, `find`, `findIndex` and `flatMap` are already correct. `reduce` with no initial value over only holes gives `NaN` or `undefined` where Node throws `TypeError` (from `any_array_callbacks_skip_holes`). Typed, the same case throws RangeError where Node throws TypeError.

Root cause, from reading the code (not changed):
- **Typed path:** `pts/lower/callbacks.go` `loopFrom` (around line 404) skips holes (`pts_array_has`) only when `baseMethod == "flatMap"`. Every other method reads the element with the checked read.
- **Dynamic path:** in `pts/runtime/src/dynamic_methods.c`, `array_for_each`, `array_map`, `array_some`, `array_every` and `reduce` loop without `pts_array_has`. Only `array_filter` (line 611) and `flatMap` (line 624) skip holes.

### C78 is confirmed, and it is wider than recorded

From `h06-probes/repro/c78.pts`, where each case is in its own try/catch:

```
node: push [ 'x', 'b', undefined, 'b' ]        pts: push [ 'x', 'b', <1 empty item>, 'b' ]
node: literal [ 'x', 'b', undefined, 'b' ]     pts: literal [ 'x', 'b', <1 empty item>, 'b' ]
node: push slice [ 'y', 'b', undefined, 'b' ]  pts: push slice [ 'y', 'b', <1 empty item>, 'b' ]
node: spread [ 'b', undefined, 'b' ]           pts: spread [ 'b', <1 empty item>, 'b' ]   (also `const e: string[] = [...m]`)
node: concat [ 'q', 'b', undefined, 'b' ]      pts: concat [ 'q', 'b', <1 empty item>, 'b' ]
node: unshift [ 'b', undefined, 'b', 'k' ]     pts: unshift [ 'b', <1 empty item>, 'b', 'k' ]
node: match push [ 'b', undefined, 'b' ]       pts: match push [ 'b', <1 empty item>, 'b' ]   ("xb".match(re))
node: toSorted [ 'b', 'b', undefined ]         pts: toSorted [ undefined, 'b', 'b' ]           (C06)
```

The matrix adds two cases:
- Typed `[...m.values()]` also turns the group into a hole.
- Through `any`, `m.filter(() => true)` drops undefined groups: Node `[ '', undefined ]`, PTS `[ '' ]`.

These are correct already: `m.slice()`, `Array.from(m)`, `m.toSpliced`, `m.toReversed`, `m.join`, and `push(...m)` into an `any` array.

These are D-133 by design, not C78:
- Typed `m.map`, `m.forEach`, `for…of` and `m.filter(…)` throw `TypeError: Expected string, got undefined (group 1 of the match took no part in it)`. D-133 makes a group that took no part a checked string where a string is required, and a typed callback parameter or binding is such a place. This includes `m.filter(() => true)`, whose callback never reads the element (minor).
- The `filter` throw in the first `c78.pts` version hid every later case. That is why every case now has its own try/catch.

Side findings:
- `sp.splice(1, 1, ...m)` is PTS1001 ("spread arguments of `splice` are not supported yet"). This is an honest diagnostic.
- `m.indexOf(undefined as unknown as string)` throws `TypeError: Expected string, got undefined`. That comes from my cast (a checked conversion), so it is not a defect.

## C. Generated differential matrix

### How it is built

- **Generator:** `h06-probes/gen.py`. It is deterministic: a fixed product, seed 0, and the seed only shuffles value order.
- **Element representations (20), typed and through `any`:**
  - `string[]`, `(string|undefined)[]`, `(string|null)[]`, `(string|null|undefined)[]`
  - `number[]`, `(number|undefined)[]`, `(number|null)[]`
  - `boolean[]`, `(boolean|undefined)[]`
  - class `P[]`, `(P|undefined)[]`, `(P|null)[]`
  - interface `Rec[]`, `(Rec|undefined)[]`
  - `any[]`, `unknown[]`, `(string|number)[]`, `(string|number|undefined)[]`
  - match arrays (`re.exec(s)!`) and match copies (`.slice()`)
- **Contents (13):** empty; one element; one undefined; one hole; defined with ties; with undefined; with null; mostly null; with holes; undefined and holes; null, undefined and holes; all undefined; all holes. Match representations use 4 regex-made contents instead. `number[]` and `boolean[]` get only two hole contents, to show D-085.
- **Groups (6):**
  - sort: `sort()`, `sort(cmp)`, `toSorted()`, `toSorted(cmp)`. The comparator call count with undefined and the sorted distinct arguments are printed, never the call sequence.
  - callbacks: `map`, `forEach`, `filter`, `some`, `every`, `reduce`/`reduceRight` with and without an initial value, `flatMap`.
  - search: `find`, `findIndex`, `findLast`, `findLastIndex`, `indexOf`/`lastIndexOf`/`includes` of a value, of undefined and of null, `at`, index reads.
  - strings: `console.log`, `join()`, `join("-")`, `toString`, `String`, a template, `JSON.stringify`, `Object.keys`, `Object.values`.
  - copy: `concat`, `slice`, `splice`, `toSpliced`, `toReversed`, `reverse`, `with`, `fill`, `copyWithin`, `flat`.
  - iter: `for…of`, `for…in`, spread, `Array.from`, destructuring, `[...keys()]`, `[...values()]`, `[...entries()]`.
- **Programs:** one per (representation, mode, group), 240 in all. Each case is its own function with a try/catch and prints one labelled line, or `threw <Name>` plus a `#msg` line that isn't compared.
- **Runner:** `h06-probes/run_matrix.py` compares each labelled case with Node.
  - A case with a compile error is removed and the program rebuilt. Diagnostics map to case spans, and ICEs are isolated by bisection.
  - After a crash or an uncaught throw, the remaining cases run again, so one failure can't hide others.
  - Classes are OK, WRONG, CRASH, DIAG, ICE, TIMEOUT, and GENERR (a `TS…` error: invalid TypeScript from the generator).
- **Buckets:** `h06-probes/classify.py` assigns each non-OK case a bucket with the decision it cites, and `family()` names the defect family. `h06-probes/families.py` does the counting.

### Counts (final run, command 8)

| | Count |
|---|---|
| Programs | 240 |
| Cases | 17,748 |
| OK | 14,085 |
| WRONG | 3,498 |
| DIAG | 165 |
| CRASH, ICE, TIMEOUT, GENERR | 0 |

The worst class per program is WRONG for 232, DIAG for 2 and OK for 6.

Non-OK cases by bucket:

| Bucket | Cases | Meaning |
|---|---|---|
| DEFECT | 2,535 | defects, every one assigned to a family below (no unexplained residue) |
| D-085 (by design) | 332 | `number[]` and `boolean[]` have no holes; their slots start as 0 or false |
| UNSUPPORTED | 330 | `copyWithin`: 165 typed PTS1001 ("the array method `copyWithin` is not supported yet"), 165 through `any` (`TypeError: x.copyWithin is not a function`). All 165 DIAGs are these. |
| UNREPR (design question) | 237 | the JS result has undefined elements in a new `T[]` whose T has no undefined; see DQ1 |
| REQUIRED (by design, D-038/D-085) | 126 | a hole reaches typed code that needs a T without undefined (`find*` callbacks, `for…of` bindings, `entries()` pairs) and PTS throws RangeError where JS passes undefined |
| D-133 (by design) | 96 | a match's undefined group reaches a typed `string` callback, binding or `find*`: exec's array gives D-133's TypeError, a match copy gives RangeError (inconsistent, minor) |
| D-086 (by design) | 7 | typed `Object.values(m)` of a match array: the checked copy into tsc's `string[]` meets `index`, `input` and `groups` (`TypeError: Expected string[], got Array`) |

Inputs without holes are clean. Over undefined and null without holes, every audited method matches Node except `sort`/`toSorted` and the unsupported `copyWithin`. Every other defect involves holes or a match's undefined groups.

These already match Node on every representation: `join`, `toString`, `String`, templates, `JSON.stringify`, `console.log`, `slice`, `splice`, `reverse`, `fill`, `flat`, `at`, index reads, destructuring, the `keys()` iterator, spread and `Array.from` through `any`, `find*` through `any`, a typed `indexOf` of undefined, and stable sorting with ties (consistent comparators).

### Defect families

Each family below has its matrix case count and a minimal executed repro (Node vs PTS). The full per-case listing is in `h06-probes/results/families-final.txt` and `matrix-final.json.gz`.

**C06 (sort):**

- **F1 (156 cases), plus F1+F3 (92) for `toSorted()` over holes.** `sort()` and `toSorted()` without a comparator sort undefined, null and holes first, on every representation and both paths. `element_string` in `array.c:688` maps all three to `""`; that is right for `join` but wrong for sort keys.
  - `[20, undefined, 3, null, 100]` through `any`, `toSorted()`: Node `[ 100, 20, 3, null, undefined ]`, PTS `[ undefined, null, 100, 20, 3 ]`.
  - A `string[]` with holes, `.sort()`: Node `[ 'a', 'nuk', 'nz', <2 empty items> ]`, PTS `[ <2 empty items>, 'a', 'nuk', 'nz' ]`.
- **F2 (96 cases), plus F2+F3 (84) for `toSorted(cmp)` over holes.** `sort(cmp)` and `toSorted(cmp)` pass undefined to the comparator. `pts_array_sort_with` skips only holes (`array.c:848–853`); the dynamic path reaches the same function from `dynamic_methods.c:576`.
  - `[3, undefined, 1, undefined, 2].sort(byNumber)`: Node `[ 1, 2, 3, undefined, undefined ] args=1,2,3`, PTS `[ undefined, undefined, 1, 2, 3 ] args=1,2,3,undefined`.
- **F3 (in the 176 combined cases).** `toSorted` keeps holes where JS reads them as undefined, for element types that can hold undefined. `toSorted` is ArrayCopy plus sort (D-085), and the copy keeps zero slots.
  - `new Array<number|undefined>(2).toSorted()`: Node `[ undefined, undefined ]`, PTS `[ <2 empty items> ]`.
- **Parallel (`pts:parallel`), from the probes in `h06-probes/parallel/` and the RED tests.**
  - `.par.sort(cmp)` passes undefined to the comparator at every thread count.
  - At 2 and 4 threads it also turns holes into undefineds and scatters them. The result depends on the thread count: the 6000-slot tail is `[ undefined, undefined ]` at 2 threads where Node has `[ <2 empty items> ]`.
  - For arrays of strings and objects with holes it throws an **uncaught** `RangeError … at pts:parallel:640` at 2 and 4 threads, even for a 4-element array (one piece). `sortInPlace` writes each sorted piece back with `items[from + i] = sorted[i]`, reading a hole as a required element. With the default thread count this hits ordinary programs. Executed: the `par_small_holes` binary without `PTS_THREADS` on this 8-core host printed `maybe numbers [ undefined, 1, 3, undefined, undefined ]`, `strings threw RangeError`, `objects threw RangeError`, exit 0.
  - `.par.sort()` without a comparator throws RangeError at every thread count for any array with holes, because its key pass `items.map(…)` hits F5.
  - `.par.sort()` without holes is correct (H01b control).
- **Comparator form.** Typed `a.sort(undefined)`, `toSorted(undefined)` and an optional comparator (`((a,b)=>number) | undefined`) are PTS1001: "converting undefined to (number, number) => number is not supported yet". Through `any`, `sort(undefined)` uses the default order (correct apart from F1). Through `any`, `sort(5)` and `sort(null)` throw TypeError as in Node (OK).

**C74 (callbacks):**

- **F5 (540 cases).** Typed `map`, `forEach`, `filter`, `some`, `every`, `reduce` and `reduceRight` (with and without an initial value) throw RangeError at holes, for every hole-holding element type, including `(T|undefined)[]`, `any[]` and `unknown[]`. Repro under B.
- **F6 (433 cases).** Through `any`, `map`, `forEach`, `some`, `every`, `reduce` and `reduceRight` call the callback for holes. Repro under B.

**Audit families (new rows to record):**

- **F7 (156 cases).** Typed `find`, `findIndex`, `findLast` and `findLastIndex` throw RangeError at holes even where T admits undefined (`(T|undefined)[]`, `any[]`, `unknown[]`).
  - `maybe().findIndex(x => x === undefined)`: Node `0`, PTS `threw RangeError`.
  - For T without undefined, the same throw is the REQUIRED bucket (by design).
- **F8 (135 cases).** Typed `includes(undefined)` misses holes. Through `any`, `indexOf` and `lastIndexOf(undefined)` match holes. The two paths are wrong in opposite directions.
  - Typed `holes().includes(undefined)`: Node `true`, PTS `false`.
  - Through `any`, `indexOf(undefined)`: Node `-1`, PTS `1`.
- **F9 (360 cases).** `Object.keys`, `Object.values`, `Object.entries` and `for…in` list holes, typed and through `any`. Typed `Object.values` of `P[]` or `string[]` with holes throws `TypeError: Expected P[], got Array`.
  - `Object.keys(strings())`: Node `[ '1', '3' ]`, PTS `[ '0', '1', '2', '3' ]`.
- **F10 (407 cases).** For element types that can hold undefined, these keep holes where JS reads undefined, or throw:
  - `toReversed`, `toSpliced` and `with` keep holes on both paths.
  - Typed spread, `Array.from` and `[...values()]` keep holes.
  - Typed `[...entries()]` and `for…of` throw RangeError.
  - `maybe().toReversed()`: Node `[ 3, undefined, undefined, 1 ]`, PTS `[ 3, undefined, <1 empty item>, 1 ]`.
- **F11 (60 cases).** `concat` through `any` turns holes into undefined; typed `concat` is right.
  - Node `[ <1 empty item>, 2, <1 empty item>, 4 ]`, PTS `[ undefined, 2, undefined, 4 ]`.
- **F12 (16 matrix cases, plus the B repros).** C78 as above.
- **Conversion copies (outside the matrix; `h06-probes/repro/conversion_holes.pts`, RED test `array_conversion_keeps_holes`).** D-094's copies of new arrays, and of arrays passed to a read-only parameter, into a wider element type turn holes into undefined. A `Q[]` with a hole copied to `P[]` throws TypeError.
  - `const b: (number|string|undefined)[] = maybe().slice()`: Node `[ <1 empty item>, 2, <1 empty item> ]`, PTS `[ undefined, 2, undefined ]`.
  - A read-only parameter's `indexOf(undefined)`: Node `-1`, PTS `0`.
  - Converting a kept array is PTS1001 (D-094), as documented.

## D. RED tests

All 17 are in `pts/tests/run/` in the worktree, with copies in `h06-probes/red-tests/`. Each is a header comment, the program, then an `// expect:` block that is Node's stdout, checked exactly by command 13.

- The 15 plain-TypeScript tests run under both `TestRun` and `TestRunMatchesNode`.
- `parallel_sort_undefined` and `parallel_sort_holes` use `.par`, have `// threads: 1 2 4`, and take their expect blocks from sequential Node oracles: `h06-probes/parallel/parallel_sort_undefined.oracle.mts` (sha256 `ff40b594…a6a`) and `parallel_sort_holes.oracle.mts` (`894955dd…08a`). The oracles differ from the tests only by `.par.sort` → `.sort` and the AtomicInt → a counter (diffed).

Every case is RED for its defect's reason: a stdout diff showing the defect, never a compile error. For each test, `TestRun` fails, and `TestRunMatchesNode` fails too except for the two `.par` tests, which are skipped there by design.

| Test | sha256 | Defect | What the failure shows |
|---|---|---|---|
| sort_undefined_last | d1cbca5aaa82f15c2d1072e16646f3015ee2260a7c7cf05bddabc3c4ca1bb1b6 | C06 F1 | undefined first in every representation, through `any`, and in a match `toSorted`/`sort` |
| sort_null_strings | 6f8f9131eec404883f4f26d355ee05a61448d5fa218ddcdba941138bb6d17615 | C06 F1 | `[ null, 100, 20, 3 ]` for `[ 100, 20, 3, null ]` |
| sort_holes | 52f6ced39aff5628e467cf08990ba85f15120c0d0402199a5c019adcdca01bae | C06 F1 | `[ <2 empty items>, 'a', 'nuk', 'nz' ]`; the comparator lines are controls that already pass |
| sort_comparator_never_undefined | 5898d415a944bf27186064d6affc2e076a98ff514203d812846ca303a2bd0af2 | C06 F2 | `args=1,2,3,undefined`; undefined sorted first |
| tosorted_holes | 4bd5fe4ee0bd9c96d0309dac5ba43386119b79958a3b12e0cfead2754bf80db6 | C06 F1+F3, F2+F3 | `[ <2 empty items>, 20, 3 ]` for `[ 20, 3, undefined, undefined ]` |
| sort_any_arrays | afd08cd1b6fed38fa3e5e82747f75228c59f1e4fc9a925fe7c3c339937612584 | C06 dynamic path | `[ undefined, null, 100, 20, 3 ] … true` |
| parallel_sort_undefined (`threads: 1 2 4`) | 6682a3acb4b05f31ddddd6c56d10625646b937626473259caa8dd2861c9a241b | C06 `.par.sort(cmp)` | all 3 thread counts: comparator saw undefined, unstable, wrong tail; at 2/4 threads the holes become undefineds |
| parallel_sort_holes (`threads: 1 2 4`) | b5353fc6963fa8b703ba9d8530c35d4ddef172e075b176d38bdd29e60b414a59 | C06 `.par` and C74 | `default … threw RangeError` at 1/2/4 threads; `compare … threw RangeError` at 2/4 |
| array_callbacks_skip_holes | 37583512f4e2ce954e95c23bbd470390061545c104ade16dd37a277662b793b0 | C74 typed (F5) | 22 lines of `threw RangeError` |
| any_array_callbacks_skip_holes | 4a29a4ab0b10e0509a2ad6aba91b4b80c2f60655e9178c590c00133632288c16 | C74 dynamic (F6) | `map [ 'undefined!', …]`, `… no initial NaN`; `filter`/`find` lines are controls that pass |
| array_find_holes | 1cab81302df1f9a9721f4b3fc18c64a24394d3990343601341147c76151302f2 | F7 | `maybe find threw RangeError`, …; the through-`any` line is a passing control |
| array_search_undefined_holes | bc979235a59a9fc4d2ce157fe6fd2adbd7d3b489d8913a0362c0f1f6cc11463a | F8 | `typed false -1 -1`, `via any true 1 3` |
| array_keys_skip_holes | ba44f18f1275a3fc79b5b9acb55a699bf4bd90bee4603a2766206172554f882e | F9 | `keys [ [ '0', '1', '2', '3' ], …`, `values points threw TypeError` |
| array_copies_read_holes | d1f6feac30df1215beda0f02e2e85d6a30e4326e3022b9f4539cf2a1e0d724c3 | F10 | `toReversed [ 3, undefined, <1 empty item>, 1 ]`, `entries threw RangeError` |
| any_concat_keeps_holes | ac141be6b9e4495ff7246530559839e26ec26ff7daa3380adba43407f35f686f | F11 | `[ undefined, 2, undefined, 4 ]` |
| match_spreads_keep_undefined | dc7ba8840e52d6c43e99c397ee9517be0668e821694b9a70cec2d292214daf40 | C78 (F12) | `push [ 'x', 'b', <1 empty item>, 'b' ]`, `via any filter [ 'b', 'b' ]` |
| array_conversion_keeps_holes | f151549815fea2e4998596ee07bd0c660fd26b75e53c42d21294f52b85dabbcd | conversion copies | `[ undefined, 2, undefined ]`, `Q[] to P[] threw TypeError` |

Per-test results from `/tmp/h06/repro/red.jsonl` (also `h06-probes/results/red.jsonl`):
- `TestRun`: 17 of 17 fail. For each `.par` test, all three `threads=1/2/4` subtests fail.
- `TestRunMatchesNode`: 15 fail; `parallel_sort_undefined` and `parallel_sort_holes` are SKIP, because the harness doesn't compare pts-feature programs with Node.
- Totals: 38 fail, 2 skip. Every failure is `assertion failed` on stdout, with the diffs summarised in the table.

Command 14 shows the new tests add no ICE under the compiler smoke test, including one mutant each.

What isn't a RED test, deliberately:
- The UNREPR cases (DQ1).
- The REQUIRED, D-133 and D-086 by-design cases.
- `copyWithin`, typed `sort(undefined)` and `splice(…, ...spread)`, which are honest PTS1001s.
- A test pinning comparator call order (not required to match V8).

## E. Design questions and recommendations

1. **UNREPR: copies JS fills with undefined, when T has no undefined (237 cases).** The affected calls are `toSorted`, `toSorted(cmp)`, `toReversed`, `toSpliced`, `with`, `[...a]`, `Array.from(a)` and `[...a.values()]`. The affected element types are `string[]`, `P[]`, `Rec[]`, `(string|null)[]`, `(number|null)[]` and `(string|number)[]`, each with holes. For a `string[]` with holes, `toSorted((p, q) => …)`: Node `[ 'a', 'nz', 'nuk', undefined, undefined ]`, PTS `[ 'a', 'nz', 'nuk', <2 empty items> ]`. A `string[]` can't hold undefined.
   - (a) Keep the holes and document this as a D-085 extension. Printing then shows `<n empty items>`, and after the F9 fix `Object.keys` and `forEach` skip those slots where JS has undefined elements.
   - (b) Give such copies the "zero slot reads as undefined" twin that match copies already have for strings (`pts_type_match_copy`, D-133), adding twins for the pointer kinds. Printing, `Object.keys`, iteration and `includes` would then match JS, and reads where an element is required throw, as for match copies.
   - (c) Throw at copy time when a hole is met. This is honest, but breaks programs that never read those slots.
   - **Recommendation:** (b). It follows an existing decision and makes these results Node-identical except where typed code needs an element. If it's too large for H06, take (a) with a decision and REVIEW_LATER entry. Either way, don't pretend in tests.
2. **REQUIRED (126 cases).** Typed `find*` callbacks, `for…of` bindings and `entries()` pairs over a T without undefined receive a hole as undefined in JS, and PTS throws RangeError (D-038/D-085).
   - Recommendation: keep it. `for…of` bindings are H11's loose-name scope (C31, D-177).
   - Separately, the message is misleading for a hole inside the length: "Array index 1 is out of range (length 4)". Suggest a hole-specific wording (H22).
3. **The `.par.sort` design.** Every path needs SortIndexedProperties' shape, which the library can't express today because PTS code can't write a hole.
   - Recommendation: a runtime helper, used by both `pts_array_sort_with` and `pts:parallel`, that stably moves the defined elements to the front, then the undefineds, then the holes, and returns the defined count. The parallel merge then sorts only `[0, count)`, so the comparator never sees undefined or a hole, and pieces are never written back through required reads.
   - `.par.sort()`'s key pass must skip holes too. It currently fails through F5.
4. **Typed `sort(undefined)` and optional comparators (PTS1001).** SortCompare allows comparefn to be undefined.
   - Recommendation: support it in H06. A literal undefined lowers to plain `sort`. A comparator whose type includes undefined branches at run time between `ArraySort` and `ArraySortWith`.
   - Otherwise keep the PTS1001 and log the gap.
5. **Conversion copies losing holes.** This is adjacent: a regular-conversion copy gap, like C80–C86.
   - Recommendation: record a new row and give it to H22 with the other D-094 copy gaps, or fix it in H06 if the "zero slot" machinery from DQ1(b) lands. The RED test exists either way.
6. **`copyWithin`.** It is in the audit list but unsupported: PTS1001 typed, and `TypeError: x.copyWithin is not a function` through `any`.
   - Recommendation: implement it (small; JS's hole-preserving copy) or make the dynamic error say "not supported yet". It is not part of C06, C74 or C78.
7. **Minor inconsistencies.** These are D-133 territory, so no RED tests. Typed `m.filter(() => true)` over a match with an undefined group throws D-133's TypeError even though the callback never reads the element. A match copy's undefined group read as a required string gives RangeError, where exec's own array gives TypeError.

## Files

- `/tmp/h06/reproducer-report.md`: this report.
- `/tmp/h06/repro/h06-reproducer-probes.tar.gz`: archive of `h06-probes/`.
- In `h06-probes/` (worktree):
  - Tools: `gen.py`, `run_matrix.py`, `classify.py`, `families.py`, `summarize.py`, `query.py`, `cmp.py`, `mkexpect.py`, `check_expect.py`, `red_summary.py`.
  - Repros: `repro/` (C74, C78, representations, comparator forms, conversions) and `parallel/` (probes and oracles).
  - Copies of the RED tests: `red-tests/`.
  - Receipts in `results/`: H01b JSON, `matrix-final.json.gz`, `families-final.txt`, `red.jsonl`, `smoke.txt`.
- RED tests: `pts/tests/run/{sort_undefined_last,sort_null_strings,sort_holes,sort_comparator_never_undefined,tosorted_holes,sort_any_arrays,parallel_sort_undefined,parallel_sort_holes,array_callbacks_skip_holes,any_array_callbacks_skip_holes,array_find_holes,array_search_undefined_holes,array_keys_skip_holes,array_copies_read_holes,any_concat_keeps_holes,match_spreads_keep_undefined,array_conversion_keeps_holes}.pts`.
