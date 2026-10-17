# H06 reviews

Independent `opus-reviewer` rounds over the H06a slice (claude-opus-5-5, effort xhigh; the Agent call passed the `opus` alias, which the session's environment maps to claude-opus-5-5). Read-only: each round built its own CLI from the parent's tree into `/tmp/h06/review<n>/` and wrote nothing in the repository. Probes: [round 1](h06-review-probes.tar.gz), [round 2](h06-review2-probes.tar.gz), [round 3](h06-review3-probes.tar.gz). The parent's response to each finding is in [h06.md](h06.md).

## Round 1 (tree: the first fix, code diff sha256 `37a5617f…f075`)


Slice: the uncommitted tree in `/home/bryan/projects/parallelscript` against `HEAD` `3999ab359` (code diff `git diff -- pts | sha256sum` = `37a5617f…f075`). Every code file predates my CLI build (12:06; the latest code edit was `pts/lower/arrays.go` at 12:04). CLI: `/tmp/h06/review1/pts`, built from the main tree. Base: `/tmp/h06-base/pts-bin/pts`. Probes are under `/tmp/h06/review1/` and archived in `/tmp/h06/review1/h06-review-probes.tar.gz`. The helper `run3.sh <file> [threads]` runs the new CLI, the base CLI and Node (the `.oracle.mts` twin if there is one).

## Verdict

**Not ready: one blocker, three should-fix.** The core of the slice is right. The runtime's SortCompare order, comparators never seeing undefined or a hole, the copies, the dynamic path and the parallel library all match Node across a generated matrix at 1, 2 and 4 threads. But the new handling of an undefined comparator silently breaks typed arrays (F1): base rejected that code with PTS1001, and now it sorts in string order. Fix F1 and add typed cases to `sort_optional_comparator.pts` before committing. F2 and F4 are also worth fixing in H06a; the rest can be recorded.

## Verified evidence (commands run, results)

- **New tests vs Node.** I copied each of the 8 sequential new tests to `.mts` and ran it in Node; Node's output equals the expect block for all 8 (`/tmp/h06/review1/expect/*.diff`, empty). For the 2 parallel tests I wrote my own sequential twins (`.par.sort` changed to `.sort`, AtomicInt replaced by a counter class); both expect blocks equal Node.
- **New tests, both CLIs.** All 10 non-skipped new tests are red on the base CLI at `PTS_THREADS=1` and `2` (5–12 differing lines each; `sort_optional_comparator` fails to compile on base). On the new CLI, `parallel_sort_undefined` and `parallel_sort_holes` match their expect blocks at 1, 2 and 4 threads (`diff -q` clean, exit 0). The other 8 pass through the harness (below).
- **Skipped tests.** All 10 skipped tests: the expect block equals Node, they are still red on the new CLI, and their output is byte-identical between base and new (H06a changes nothing there).
- **Generated differential matrix** (`/tmp/h06/review1/diff/gen.pts`, with a Node twin).
  - Arrays: any[], `(string|number|null|undefined)[]`, string[] with holes, `(O|undefined)[]` and number[].
  - Sizes: 0, 1, 2, 7, 100, 2047, 2048, 2049, 4500 and 9000.
  - Operations: sort(), sort(cmp), toSorted(), toSorted(cmp), .par.sort(), .par.sort(cmp), and through `any`. The comparator is consistent but has many equal keys, so stability is exercised.
  - Each result is digested position by position. A hole is detected as `a.slice(i,i+1).flat().length === 0`, since `flat` skips holes but keeps undefined.
  - Results: 230 digests per run. Seed 12345 has 0 differences from Node at 1, 2 and 4 threads; seeds 777 and 99991 have 0 differences at 1 and 4 threads. The base binary of the same program exits 1 early, so the digest does discriminate.
- **Unit and Go tests.**
  - `go test -count=1 -v -run 'TestRuntimeUnitTestsPassUnderSanitizers/array_test.c' ./pts/toolchain/`: PASS (2.26 s). My first run used the filter `/array_test` without `.c` and matched no subtest.
  - `go test ./pts/lower/... ./pts/cgen/...`: ok. `go vet ./pts/lower/ ./pts/cgen/`: clean.
- **Regression sweep.** The 38 run tests that use sort, toSorted, toReversed, toSpliced, with, sortBy or `.par.sort`, through the real harness (`-parallel 2`): `TestRun` gave 44 PASS (12 skipped as "compared by TestRunMatchesNode"), and `TestRunMatchesNode` on those 12 gave 12 PASS (`PTS_REQUIRE_NODE=1`).
- **Performance** (`bench/`, 1M elements, `PTS_THREADS=4`, new and base interleaved ×3): default `.par.sort()` on strings took 435/451/445 ms new vs 536/491/510 ms base; `.par.sort(cmp)` on numbers took 109/149/91 vs 104/92/115 ms (noise). No regression.
- **Correct paths I probed.** All of these match Node:
  - match arrays (`.par.sort`, toReversed, with, toSpliced), at 1 and 2 threads;
  - tuples with optional (ABSENT) elements;
  - generic code and an erased generic arrow with `compare?`;
  - optional comparators read from an optional property, an optional class field, an optional tuple element, a `Record`, `?.` and an array element;
  - `.par.sort(undefined)` and `.par.sort(maybe)`;
  - unions without undefined (`(string|null)[]`, `(P|null)[]`, `(number|string)[]`): their copies now give undefined, as in Node;
  - checked conversions from `any` of such copies (no new TypeErrors).
- **Race and GC.**
  - `pts_array_sort` now reads each slot once (before H06a it read each slot twice: `element_load`, then `element_string` on the buffer).
  - Scratch memory is `GC_MALLOC`, so it is scanned.
  - `pts_array_copy_as_read` writes only into an unpublished copy.
  - In `store_if_moved`, the second load only decides whether to store; the value stored comes from the first read.
  - Disguised non-arrays (`typed as any as number[]`, a tuple as an array) are stopped by the checked conversion before `pts_parallel_gather_sortable` casts its argument (`par_disguised.pts`).
  - Union reads of the new explicit undefined are tag-checked: `TypeError: Expected string, got undefined`, never a wild pointer (`union_without_undefined.pts`).
- **Generated C** (`probes/emit_optional.c`): the optional path is one readable statement-expression with `#line`.

## Findings

### F1: blocker, new in H06a. Typed arrays' `sort(undefined)`, `toSorted(undefined)` and optional comparators sort in string order

- `pts/lower/typed_arrays.go:576-591` (`typedSortWith`) calls `l.sortWith(&ir.LocalRef{Local: numbers}, compare)`.
- H06a's `sortWith` (`pts/lower/arrays.go:412-414`) and `sortWithOptional` (`:432-446`) emit `ir.ArraySort` for the undefined case, which is Array's string order, on the typed array's `number[]` copy. A typed array's default order is numeric.
- Repro: `/tmp/h06/review1/probes/typed_sort_undefined.pts`.

```
NODE: Float64Array(6) [ -0, 0, 1, 9, 10, 100 ]      Int32Array(4) [ 1, 9, 10, 100 ] …  Uint8Array(4) [ 1, 9, 10, 100 ]
PTS : Float64Array(6) [ -0, 0, 1, 10, 100, 9 ]      Int32Array(4) [ 1, 10, 100, 9 ] …  Uint8Array(4) [ 1, 10, 100, 9 ]
BASE: error PTS1001: converting undefined to (number, number) => number is not supported yet (×4)
```

- Base gave an honest compile error; H06a gives a silent wrong result. That is a regression, on a path the SPEC change now claims ("an undefined comparator (`sort(undefined)`, `compare?: …`) is the default order").
- Fix: in `typedSortWith`, handle undefined before calling `sortWith`.
  - A literal `undefined` should call `pts_typed_array_sort` (or `toSorted`'s copy, then `pts_typed_array_sort`).
  - A comparator whose union holds undefined should get a run-time `Conditional` between `pts_typed_array_sort(sorted)` and the existing numbers path.
- Add `Float64Array`/`Int32Array` cases to `run/sort_optional_comparator.pts`: `sort(undefined)`, `toSorted(undefined)`, and a `compare?` parameter, both left out and given.

### F2: should-fix, new in H06a. `.par.sort` changes the array before the comparator or toString runs

- `pts/lib/parallel.pts:184` calls `pts_parallel_gather_sortable(items)` in place, before any user code runs.
- JS (and the base, at 1 thread and for arrays shorter than one piece at 2 and 4 threads) reads the elements, sorts them, and writes back only if nothing throws. The comparator also sees the original array.
- With the in-place gather, the array is left compacted when the comparator or a `toString` throws, and the comparator sees the compacted array. This happens at every thread count.
- Repros: `/tmp/h06/review1/probes/par_sort_edges.pts` (first two lines) and `/tmp/h06/review1/probes/par_observe.pts`.

```
thrown = [undefined, 3, undefined, 1, 2]; thrown.par.sort(() => { throw … })
NODE / BASE (t=1,2,4): after throw [ undefined, 3, undefined, 1, 2 ]
PTS (t=1,2,4):         after throw [ 3, 1, 2, undefined, undefined ]
throwing toString, default order:  NODE [ undefined, Bad { n: 1 }, undefined, Bad { n: 2 } ]   PTS [ Bad { n: 1 }, Bad { n: 2 }, undefined, undefined ]
comparator reading b[0] of [undefined, 2, 1]:  NODE seen [ 'undefined' ]   PTS seen [ '2' ]
```

- For arrays longer than one piece at 2 or more threads, the base already wrote sorted pieces back before the merges, so a throw there was never all-or-nothing.
- Fix: gather into a scratch array rather than in place.
  - A runtime helper would return a compact array of the compared elements and the undefined count.
  - Sort that array with `sortInPlace` and the existing merge, or `permute` for the default order.
  - A second helper would then write back the compared elements, then the undefineds, then the holes, frozen check included.
- The cheaper fallback is to skip the gather at 1 thread, since `items.sort` already handles undefined and holes. That still leaves small arrays at 2+ threads changed.

### F3: minor, new in H06a. A frozen array's `.par.sort` names the wrong index and throws before the comparator runs

- In `pts/runtime/src/array.c:921-931` (`store_if_moved`), the gather throws at the first slot it moves. Node, and PTS's sequential sort, throw at `'0'`, after the comparator calls.
- Repro: `/tmp/h06/review1/probes/frozen_messages.pts`.

```
NODE (and PTS a.sort()):  TypeError: Cannot assign to read only property '0' of object '[object Array]'
PTS a.par.sort() / a.par.sort(cmp), t=1,2:   … read only property '1' …     (BASE par sort: '0')
```

- The F2 fix makes this go away.
- Related, pre-existing (`frozen_holes.pts`, `par_sort_edges.pts`):
  - The sequential `sort()`/`sort(cmp)` of a frozen all-holes array throws; Node doesn't, since it only deletes absent properties. The frozen check at `array.c:853` and `:907` could be `length >= 2 && count + undefineds > 0`.
  - `Object.freeze([1]).par.sort()` throws; Node returns `[ 1 ]`, because V8 returns early for length < 2.
  - With H06a, `.par.sort()` of a frozen all-holes array matches Node while `.par.sort(cmp)` throws.

### F4: should-fix, pre-existing, in C95's scope. Comparators of dynamic type that hold undefined throw

- `sortWith` (`pts/lower/arrays.go:421`) branches at run time only when the comparator's IR type is a `*ir.Union` holding undefined.
- A comparator that is `any`, or a `Map.get(k)` result (tsc: `C | undefined`, IR: a dynamic value), goes through `l.convert` instead and throws where JS sorts in the default order.
- Repros: `/tmp/h06/review1/probes/sort_any_comparator.pts` and `/tmp/h06/review1/probes/optional_map_get.pts`.

```
[3,20,100].sort(anyUndefined) / .toSorted(anyUndefined):  NODE [ 100, 20, 3 ]   PTS/BASE TypeError: Expected (number, number) => number, got undefined
[10,9,1,100].sort(m.get("x")) (empty Map<string, C>):     NODE [ 1, 10, 100, 9 ] PTS/BASE same TypeError
sort(anyNull):  NODE TypeError: The comparison function must be either a function or undefined: null   PTS TypeError: Expected (number, number) => number, got null
```

- Fix: give `compare.Type() == ir.Value` the same treatment as the optional union. Bind the value, test `== PTS_VALUE_UNDEFINED` and take `ArraySort`; otherwise do the checked conversion. Ideally, a non-function would throw Node's message, as `dynamic_methods.c`'s `array_sort` does.
- The SPEC sentence "an undefined comparator … is the default order" currently overclaims.

### F5: minor. `isUndefined` goes by the identifier's text (new for `sort`, pre-existing for `toSorted`)

- `pts/lower/arrays.go:497-499` and `:412`: a parameter named `undefined` (legal in strict code) is taken for the global, so the comparator is ignored.
- Repro: `/tmp/h06/review1/probes/shadowed_undefined.pts`.

```
function f(undefined: C) { return [[3,20,100].sort(undefined), [3,20,100].toSorted(undefined)]; }  f((a,b)=>a-b)
NODE [ [ 3, 20, 100 ], [ 3, 20, 100 ] ]   PTS [ [ 100, 20, 3 ], [ 100, 20, 3 ] ]   BASE [ [ 3, 20, 100 ], [ 100, 20, 3 ] ]
```

- The reverse case: `sort((undefined))`, `sort(void 0)` and `const u = undefined; sort(u)` are still PTS1001 (`sort_undefined_spellings.pts`), which is honest but inconsistent.
- Fix: decide by the lowered argument's IR type (`ir.Undefined`: evaluate it, then `ArraySort`), or check that the identifier resolves to the default library's `undefined` (`l.isDeclaredInDefaultLib(l.target(arg))`), after `ast.SkipParentheses`.

### F6: minor, pre-existing, and the new optional path inherits it. `toSorted` and `toSpliced` copy before evaluating their arguments

- In `pts/lower/array_library.go:24-38`, `ArrayCopy` of the receiver is bound before the comparator or items are evaluated.
- JS evaluates the arguments first and copies inside the method, so an argument whose evaluation changes the receiver is in JS's copy but not in PTS's.
- Repros: `/tmp/h06/review1/probes/copy_order.pts`, and `copy_order_base.pts` for the base.

```
a=[3,1,2]; a.toSorted(optional())  // optional() pushes 0, returns undefined
NODE [ 0, 1, 2, 3 ]  PTS [ 1, 2, 3 ]      (required comparator: same; toSpliced(0,1,item()): NODE [ 9, 1, 2, 0 ] PTS/BASE [ 9, 1, 2 ]; with() is right)
```

- `sort_optional_comparator.pts` checks that the receiver is evaluated before the comparator. That's true, but its `comparator()` doesn't touch the receiver, so the test can't see this.
- Fix: bind the receiver to a local, evaluate the arguments, then copy that local.

### F7: minor, pre-existing, in the function H06a rewrote. `pts_array_sort` writes back into a stale buffer

- At `array.c:833` and `:856-859`, the buffer is loaded before the keys are computed, and keys can call a user `toString`. If that `toString` pushes and the array is reallocated, the write-back goes to the old buffer, so the sort is lost silently.
- `pts_array_sort_with` reloads the buffer after its user calls (`:911-913`); `pts_array_sort` doesn't.
- Repro: `/tmp/h06/review1/probes/tostring_mutates.pts`. NODE `[ T{1}, T{2}, T{3}, undefined, undefined ] 104`; PTS and BASE `[ T{3}, undefined, T{1}, T{2}, undefined ] 104`.
- Fix: reload `buffer` and `readable_length` after the keys and clamp, as `sort_with` does.

### F8: note, pre-existing design. A comparator that shrinks the array gets a shorter result than JS

- At `array.c:913`, the write-back stops at `now`. JS's `Set` re-extends the array to the number of sorted items.
- Repro: `/tmp/h06/review1/probes/comparator_mutates.pts`.
  - `pop` in the comparator: NODE `[1,2,3,undefined,undefined]` (length 5); PTS `[1,2,3,undefined]` (length 4).
  - `length = 0` in the comparator: NODE `['a','b','c',undefined]`; PTS `[]`.
- This was deliberate ("as far as it reaches"), but it's not listed in SPEC §7. Record it there or in H06b.

### F9: minor, docs and test quality

1. The ledger row C96, D-182 as SPEC and REVIEW_LATER use it, and the comment in `run/array_copies_read_holes.pts` imply that holes stay in copies whenever the element type has no undefined. C96 explicitly lists `(string | null)[]`.
   - The code converts holes in every dynamic-value array, including `(string|null)[]`, `(P|null)[]` and `(number|string)[]`, and matches Node there (`probes/union_copies.pts`, `probes/union_without_undefined.pts`).
   - Only `string[]` and arrays of one object type (non-union element kinds) keep holes.
   - Fix the wording, and pin a `(string | null)[]` case in `array_copies_read_holes.pts`.
2. D-182 is cited by SPEC, the plan, REVIEW_LATER and a test, but it isn't in `docs/DECISIONS.md` yet.
3. `parallel_sort_undefined.pts` and `parallel_sort_holes.pts` cite `h06-probes/parallel/*.oracle.mts`. Those files exist only under `/tmp/h06/repro/…` and a `.claude/worktrees/` copy. Archive them in the repository or reword the comments.
4. Stale comments:
   - `pts/ir/ir.go:869`: `ArrayCopy` "a new array of a's elements" (it now reads holes as undefined).
   - `array.c:700`: `element_string` "the string join and sort use … "" for undefined, null and holes" (sort no longer gives those to it, and null is `"null"` for sort).
   - D-039's sort clause is superseded; D-182 should say so.
5. `pts_array_gather_sortable` has no C unit test. Missing cases: a frozen array with nothing to move doesn't throw; one with something to move throws; only changed slots are stored; the order is compared, then undefined, then holes.
6. The `big` case of `parallel_sort_undefined.pts` only checks ascending order and a few slices, so a merge that duplicated or dropped elements while staying ascending would pass. A checksum, or a position digest like `diff/gen.pts`'s, would catch it.
7. `sort_optional_comparator.pts` has no typed-array case (that is F1).

## Split and by-design choice

- **The split is defensible.** H06a covers C06's sort paths, the copying methods (which share `toSorted`'s copy) and C95. H06b's items (callbacks, `find*`, `includes`/`indexOf`, keys, `in`, iteration, `concat` through `any`, match spreads) touch different runtime functions, and none of them depends on the sort changes. The skipped RED tests pin each one, and their output is unchanged by H06a.
  - The plan text says "audit … with generated differential cases". The reproducer's matrix is the stated evidence. A small generated regression test in the suite, like `/tmp/h06/review1/diff/gen.pts` (hole-exact digests, sizes around 2048, run at 1, 2 and 4 threads), would keep the audit from rotting; it could go in with H06b.
- **D-182 (string and object copies keep holes) is defensible.** A `string[]` or `P[]` slot has no representation for undefined beyond zero, which D-038 makes a hole. The divergence only shows for arrays that have holes, which come from `new Array<T>(n)` left partly unfilled, or from racing pushes. Reads where any value goes still give undefined.
  - The alternative REVIEW_LATER mentions is real: a "zero slot is undefined" twin descriptor, as `pts_type_match_copy` is for strings. It would make printing match Node, and a required read would throw a TypeError, as Node's property access on undefined does, rather than a RangeError.
  - The wording needs to be narrowed to the kinds that actually keep holes (F9.1).

## Round 2 (tree: the fixes for round 1, code diff sha256 `be52510c…989e`; probes [h06-review2-probes.tar.gz](h06-review2-probes.tar.gz))


**What I reviewed:** the main tree against `HEAD` `3999ab359`. The code diff hash (`git diff -- pts | sha256sum`) was `be52510c…989e` both when I built the CLI and when I finished.

**Tools:**
- CLI: `/tmp/h06/review2/pts`, built from that tree; programs run with `PTS_HOME=/home/bryan/projects/parallelscript`.
- Base: `/tmp/h06-base`.
- Helper: `/tmp/h06/review2/run3.sh <file> [threads]` runs the new CLI, base and Node (a `.oracle.mts` twin if present).
- Archive: probes in `/tmp/h06/review2/h06-review2-probes.tar.gz`, without binaries.

## Verdict

**Ready.** No blockers and no should-fix findings. Each of round 1's F1–F9 is fixed or honestly recorded in D-182. I found no regression in the rework's behaviour, apart from a 13–18% slowdown of `.par.sort(cmp)` (R2-2, minor). There are two minor issues and two notes; all are pre-existing or performance.

## Verified evidence

### Round-1 findings, re-checked

**F1. Typed arrays: fixed.**
- `typed_sort_undefined.pts` matches Node at 1, 2 and 4 threads.
- New probe `typed_kinds.pts` covers `toSorted(undefined)`, `toSorted(cmp)`, `sort(undefined)` and an optional comparator on Int8, Uint16, Float32 and Uint8Clamped arrays, plus a Float64Array `sort(undefined)` through `any`. All match Node, including -0 and NaN.
- A typed array with a comparator through `any` is still the documented "not supported yet" TypeError.

**F2. `.par.sort` writes all or nothing: fixed.**
- `par_sort_edges.pts` and `par_observe.pts` match Node at 1, 2 and 4 threads.
- After a throwing comparator or `toString`, the array is unchanged.
- A comparator that reads the array sees it as it was.

**F3. Frozen arrays: fixed.**
- `frozen_messages.pts` matches Node at 1, 2 and 4: index `'0'`, thrown after the comparator calls.
- `par_sort_edges.pts`: `Object.freeze([1]).par.sort()` gives `[ 1 ]`.
- `frozen_holes.pts`: a frozen array of holes only doesn't throw on any path. Its one remaining difference is `toSorted` of a `string[]`, which is the by-design D-182 case.
- `frozen_match.pts`: a frozen match array throws `'0'` on both paths, at 1 and 4 threads. Node agrees.

**F4. Dynamic comparators: fixed.**
- `optional_map_get.pts`, `sort_any_comparator.pts` (undefined and function cases), and the new `lowering_regress.pts` all match Node. The last covers an `any` property, an `any` element and a missing `Record` key.
- Still different: a non-function's message (`Expected (number, number) => number, got null`, where V8 says `The comparison function must be either a function or undefined`). D-182's known gaps record it.

**F5. Spellings and a shadowed `undefined`: fixed.**
- `shadowed_undefined.pts` and `sort_undefined_spellings.pts` match Node.
- `identifier()` (`lower.go:1522-1528`) binds a local named `undefined` before the global one, so the decision by lowered type is sound.

**F6. Copy order: fixed for `toSorted` and `toSpliced`.**
- `copy_order.pts` matches Node.
- The generated C (`probes/emit_shapes.c`) shows receiver, then arguments, then copy.
- One residue remains for typed arrays' in-place `sort(cmp)` (R2-1).

**F7. Default sort after a growing `toString`: fixed.** `tostring_mutates.pts` matches Node.

**F8. Comparator that shrinks the array: recorded, not fixed.**
- `comparator_mutates.pts` still differs in `pop` and `shrink`.
- D-182's known gaps record it.

**F9. Docs and tests: fixed.**
- D-182 is now in `docs/DECISIONS.md`.
- The wording is narrowed in D-182, C96, SPEC and REVIEW_LATER, and the stale comments are fixed.
- The two new unit tests run in `main`.
- The cited oracles exist in `h06-reproducer-probes.tar.gz`; see R2-3 for one gap.

**Other round-1 probes.** All 24 re-run at 1, 2 and 4 threads. The only differences from Node are:
- by design (D-182): `by_design.pts`, and the `toSorted` line of `frozen_holes.pts`;
- recorded (F8): `comparator_mutates.pts`;
- unrelated to sort, and the same on base: `par_disguised.pts`, `union_copy_checked.pts`, `union_without_undefined.pts`.

### Generated matrix

- I ran `gen.pts` with three seeds (12345, 777 and 99991), each at 1, 2 and 4 threads: 231 output lines per run, 0 differing lines from Node in all 9 runs (`/tmp/h06/review2/diff/`).

### Tests

- **Against Node.** I checked all 17 non-skipped H06a run tests (7 new, 10 from round 1). Each expect block equals Node's output. For the 3 `.par` tests I used my own sequential twins: `.par.sort` changed to `.sort`, and AtomicInt replaced by a counter class.
- **Both CLIs.** Each test passes on the new CLI at every thread count its `// threads:` line lists, and fails on base at those counts.
- **Harness sweep.** The 59 run tests that use sort, the copying methods, `.par` or `pts:parallel` went through `TestRun` and `TestRunMatchesNode` (`PTS_REQUIRE_NODE=1`, `-parallel 2`): 145 PASS, 0 FAIL, exit 0. The skips are the expected "no expected output" cases and Node-comparison skips of ParallelScript-only programs.
- **Unit and Go tests.**
  - `TestRuntimeUnitTestsPassUnderSanitizers/array_test.c`: PASS. The test program is compiled fresh each run.
  - `go test ./pts/lower/... ./pts/cgen/...`: ok. `go vet ./pts/lower/ ./pts/cgen/ ./pts/ir/`: clean.

### Rework regressions I looked for and didn't find

- **Order and side effects in the new Bind/Conditional trees:**
  - `copy_order.pts`;
  - `typed_compare_order.pts`: optional typed comparators, `toSorted`, `Array` `sort`, and receiver/comparator logging;
  - `lowering_regress.pts`: optional-chain receivers `?.sort` and `?.toSorted`, a missing receiver, a comparator that needs an adapter, and each receiver evaluated once;
  - `emit_shapes.c`: `void f()` is called after the receiver is bound; `toSpliced` keeps non-constant operands in locals, in order, before the copy. `l.splice` fills a left-out delete count with the constant `toEnd`, so hoisting the operands can't refer to the copy before it exists.
- **Generic and erased code:**
  - `sort_generic_optional.pts`;
  - an erased `Box<T>.sorted(c?)` at two instantiations;
  - a generic function with an `any` comparator, `undefined` or a function.
- **Frozen and match arrays through the write-back:** `par_match.pts` and `frozen_match.pts` at 1, 2 and 4 threads.
- **GC and races:**
  - `sorted = items.slice()` is an ordinary GC'd array held in a library local while the pool works.
  - The gather writes only that private copy.
  - `write_sorted` reads each slot of the copy once and stores each slot of the array once.
  - The original isn't written until the sort succeeds, so comparators on other threads read it unchanged.
- **Readability:** the C for `toSorted(cmp)`, a typed `toSorted(cmp?)`, `sort(void f())` and `toSpliced` is one readable statement-expression per call, under its `#line`.

## Findings

### R2-1: minor, pre-existing (the residue of F6). A typed array's in-place `sort(cmp)` copies its elements before evaluating the comparator

**Where:** `pts/lower/arrays.go:447`. When the comparator can't be undefined and nothing is copied, `sortWith` calls `steps.byComparator(receiver, compare)` directly. The typed array's `byComparator` (`pts/lower/typed_arrays.go:582-590`) binds `sorted`, then copies `numbers = pts_typed_array_to_array(sorted)`, and only then evaluates `compare` inside `ArraySortWith`.

**Effect:** an argument expression that writes to the typed array is lost, because the write-back overwrites it.

**Repros:** `/tmp/h06/review2/probes/typed_compare_order.pts` and `typed_compare_order_base.pts`.

```
const ta = new Int32Array([3, 1, 2]);
function changes() { ta[0] = 0; return (a, b) => a - b; }
ta.sort(changes())
NODE Int32Array(3) [ 0, 1, 2 ]     PTS (new and base) Int32Array(3) [ 1, 2, 3 ]
```

**Paths that are already right:** the optional comparator (`tb.sort(changesOptional())` gives `[ 0, 1, 2 ]`), typed `toSorted(cmp)`, and Array `sort(cmp)`.

**Fix:** don't take the `steps.copy == nil` shortcut for typed steps; the Bind path evaluates the comparator before `byComparator`. Alternatively, have the typed `byComparator` bind `compare` after `sorted` and before the copy.

**Test:** add one line to `copying_methods_argument_order.pts` or `sort_typed_array_undefined_comparator.pts`.

### R2-2: minor, new in the rework (performance). `.par.sort(cmp)` on plain arrays is 13–18% slower than base at 2–4 threads; the default order is faster

**Where:** `pts/lib/parallel.pts:189-199`. On more than one thread, the calling thread now makes three sequential O(n) passes: `items.slice()`, `pts_parallel_gather_sortable` (`array.c:923`) and `pts_array_write_sorted` (`array.c:946`). `sortInPlace`'s own `items.slice(0, count)` merge buffer comes on top of those.

**Measurement** (`/tmp/h06/review2/bench/bench2.pts`): best of 5 runs inside each program, 1M elements, new and base interleaved ×3. The host was busier than in round 1, so absolute numbers are higher.

| Threads | Comparator, numbers (new / base) | Default order, strings (new / base) | 10% undefined, comparator (new / base) |
|---|---|---|---|
| 4 | 146–169 / 123–135 ms | 794–824 / 894–956 ms | 149–166 / 154–158 ms |
| 2 | 167–171 / 148–151 ms | 1078–1082 / 1276–1297 ms | equal |
| 1 | within noise | 1159–1258 / 2056–2203 ms | — |

- At 1 thread `.par.sort` is now Array's own sort, hence the large gain in the default order.
- Against round 1 (`bench.pts`, one sample per run): the comparator path's new/base ratio went from about 1.1 to about 1.2–1.36, because of the added copy and write-back. The default order's ratio (about 0.88) is unchanged.
- Peak memory is one more n-element array than base.

**Suggested fixes:**
- Skip the gather for element kinds that can't hold holes or undefined (number, boolean, the integer kinds): `count` is just the length.
- Do the write-back with `pts_parallel_for`, or fold it into the final copy `sortRun` already makes.

None of the project's tracked benchmarks use `.par.sort`, so I'm calling this minor.

### R2-3: note. The archived oracle for `parallel_sort_undefined` predates its new `big kept` line

`pts/tests/run/parallel_sort_undefined.pts:7-8` cites `h06-probes/parallel/` in `docs/agent/hardening-evidence/h06-reproducer-probes.tar.gz`. That oracle prints everything except the expect block's new first line, `big kept true true true`.

The comment says "with review round 1's sums", and my own twin (`/tmp/h06/review2/expect/parallel_sort_undefined.mts`) reproduces the whole block. Still, archive the current twins of the three `.par` tests next to the evidence, so each expect line has stored Node evidence.

Comparison: `/tmp/h06/review2/oracle/` (`parallel_sort_holes` matches its archived oracle).

### R2-4: note, pre-existing, outside sort. `arrayOperands` still decides "left out" by the identifier's text

**Where:** `pts/lower/arrays.go:490`. `isUndefined(args[i])` treats a parameter named `undefined` as a left-out operand. `toSpliced`, `slice` and the other fixed-operand methods go through this.

**Repro:** `/tmp/h06/review2/probes/shadowed_operand.pts`.

```
function f(undefined: number) { return [[1,2,3].toSpliced(undefined, 1), [1,2,3].slice(undefined)]; }  f(2)
NODE [ [ 1, 2 ], [ 3 ] ]    PTS (new and base) [ [ 2, 3 ], [ 1, 2, 3 ] ]
```

The other text-based `isUndefined` callers (`maps.go`, `console.go`, `numbers.go`, `strings.go`, `unions.go`, `records.go`) have the same property. This is the F5 pattern outside H06a's methods: give it to H29, or decide by lowered type there too.

## On the split and D-182

The split stays defensible. H06b's RED tests are untouched by the rework and still fail as before.

The D-182 text now matches the code:
- copies of dynamic-value arrays, `(string | null)[]` included, get undefined; `string[]` and arrays of a single object type keep the hole;
- its Alternatives section records the twin-descriptor option;
- its Known gaps section records F8 and the non-function message.

## Round 3 (tree: the fixes for round 2, code diff sha256 `6401ab53…a17c`, the committed code; probes [h06-review3-probes.tar.gz](h06-review3-probes.tar.gz))


The tree's code diff hash (`git diff -- pts | sha256sum`) was `6401ab53…a17c` both when I built the CLI (`/tmp/h06/review3/pts`) and when I finished. The helper `/tmp/h06/review3/run3.sh` runs the new CLI, base and Node. The probe archive is `/tmp/h06/review3/h06-review3-probes.tar.gz`, without binaries.

## Verdict

**Ready to commit.** R2-1 is fixed, and the gather shortcut is safe by construction and in practice. The matrix is clean at 2 and 4 threads, and the docs' claims are backed by the code and by my measurements. One note: the archive the `.par` tests cite doesn't exist yet.

## Verified evidence

### R2-1: fixed
`/tmp/h06/review3/probes/r21_variants.pts` matches Node line for line. It covers:
- a typed `sort(req())` whose argument writes `a[0] = 0`: `[ 0, 1, 2 ]`, where it was `[ 1, 2, 3 ]`;
- an optional comparator, both a function and undefined;
- typed `toSorted(cmp)`;
- a comparator that needs an adapter (`(x: number|undefined, …)`);
- an `any` comparator;
- a generic `<T extends Int32Array>` caller;
- receiver-then-comparator logging.

Base rejects two of those lines with PTS1001. Base's result for the original case (`[ 1, 2, 3 ]`) is in round 2's `typed_compare_order_base.pts`.

The new `typed sort` line in `run/copying_methods_argument_order.pts`: Node's output equals the expect block, the test passes on the new CLI and fails on base.

### R2-2: the gather shortcut is safe
**By construction.** `pts_array_gather_sortable` (`array.c:928-930`) returns the length when `!holds_pointers(kind)`. For those kinds:
- `is_hole` is false, since it requires `holds_pointers`;
- `sort_place_of`'s undefined test applies only to `PTS_FIELD_VALUE`.

So the old loop classified every element as compared, stored each in place and also returned the length. The shortcut is equivalent for number, bool, f32 and i8 through u64.

**In practice.** `/tmp/h06/review3/probes/par_int_kinds.pts` runs `.par.sort()` and `.par.sort(cmp)` on:
- `int[]`, `i8[]`, `u8[]`, `i16[]`, `u16[]`, `i32[]`, `u32[]`, `i64[]`, `f32[]` and `boolean[]`;
- sizes 0, 1, 7, 2047, 2049 and 5000;
- digests of every position.

The Node twin declares the types as `number` aliases, and every value stays in its type's range. Result: 84 lines, 0 differences at 1, 2 and 4 threads. `u64[]` isn't in the probe; it is covered by the construction argument.

**A pitfall I hit on the way.** My first version used `(a, b) => a - b`. On `u8`, `i8` and `u16` that subtraction wraps, as SPEC §121–129 specifies: `3 - 5` is 254 as a `u8`. That makes the comparator inconsistent, and its results differ from Node and between thread counts. Base gives byte-identical output, so it's not a sort defect. The probe now uses three-way comparisons. This is worth remembering when writing tests with integer comparators.

### Matrix and tests
- **Matrix:** `gen.pts` (seed 12345) at 2 and 4 threads: 231 lines each, 0 differences from Node.
- **Parent oracles** (`/tmp/h06/parent/oracles/`): each reproduces its test's expect block exactly, for `parallel_sort_undefined`, `parallel_sort_holes` and `parallel_sort_all_or_nothing`. All three tests pass on the new CLI at 1, 2 and 4 threads and fail on base.

### Performance
`bench2.pts`, best of 5 runs inside each program, 1M elements, 4 threads, new and base interleaved ×3:

| Case | New | Base |
|---|---|---|
| Comparator, numbers | 128–133 ms | 110–141 ms |
| Default order, strings | 755–823 ms | 879–921 ms |
| 10% undefined, comparator | 155–160 ms | 163–171 ms |

This is consistent with D-182's known-gaps text: about 12% slower with a comparator and about 13% faster in the default order. My undefined case is within noise of base.

### Docs
I skimmed D-182, C95–C98 in CONSOLIDATED.md and SPEC §9.124. They match the code:
- the frozen rule;
- `(string | null)[]` copies get undefined;
- the typed array's comparator is evaluated before the copy;
- decisions are made by the lowered type;
- the copy and write-back of `.par.sort`, and its performance numbers;
- C98's repro: `probes/shadowed_operand.pts` is in `docs/agent/hardening-evidence/h06-review2-probes.tar.gz`.

## Findings

### R3-1: note. The archive the `.par` tests cite doesn't exist yet
`pts/tests/run/parallel_sort_undefined.pts:7-8`, `parallel_sort_holes.pts:7-8` and `parallel_sort_all_or_nothing.pts:8` cite `oracles/*.oracle.mts` in `docs/agent/hardening-evidence/h06-parent-probes.tar.gz`. That file isn't in the tree yet; the evidence directory holds `h06-baseline`, `h06-reproducer*`, `h06-review*`, `h06-review2-probes` and `h06.md`. The coordinator says it will be archived at commit time; the commit needs it so the comments don't point at nothing.

### R3-2: note, wording
D-182 and SPEC §9.124 say the gather skips "numbers and booleans". The code also skips the integer kinds and f32 (`!holds_pointers`), and that is safe. The code comment says the same as the docs. "numbers (and the integer kinds), booleans" would be exact. Optional.

### R3-3: note, ledger status
C95 still reads "open — H06a". C06 still reads "open — H06". I assume both become fixed at commit time, like the earlier fixed rows (C06 perhaps "fixed — H06a", with H06b covering the rest of the audit).
