# H03b reviews (opus-reviewer, claude-opus-5-5 xhigh)

Verbatim reports of the independent read-only review rounds of H03b. Paths under `/tmp/` are the reviewers' scratch at the time. The host rebooted before rounds 1–3's scratch was archived; [h03b-review-probes.tar.gz](h03b-review-probes.tar.gz) holds what session 4 rebuilt of it from the session transcripts (`tmp/pts-h03b-review*/`: the probe sources and scripts the reviewers wrote, not their binaries or outputs; see its `README.md`) and rounds 4's and 5's scratch as it was (`round4/`, `round5/`: probes, outputs, sanitizer runs and benchmarks, without binaries). Dispositions: [h03b.md](h03b.md#reviews).

---

## Round 1: iterator results through `any` and typed `next()`

Reviewer: claude-opus-5-5 (xhigh), read-only, 2026-10-16. Tree: the parent's uncommitted working tree at
`/home/bryan/projects/parallelscript` on base `a65a75d62`. CLI built from that tree into
`/tmp/pts-h03b-review/pts`; base CLI `/tmp/pts-h03b/base/pts-bin/pts`.

## Verdict: NOT READY

Two blockers. (1) The working tree fails a run test (`run/any_maps_error.pts`). The parent's green full
run (`/tmp/pts-h03b/full1.jsonl`, 01:07 to 01:13) finished before the last edit of `generator.c`, `pts.h` and
`dynamic_methods.c` (01:14:52), so it says nothing about this tree. (2) Three of the five acceptance
rows (C28, C32, C33) and the IteratorClose change (C41) have no regression test in the tree. The parent's
probes for them exist only in `/tmp/pts-h03b/probes/`.

Apart from these, the core design holds up. Every probe of the acceptance rows matches Node exactly:
getter read counts through nested `yield*`, a `done` getter that changes its answer, `return`/`throw`
forwarding, elisions, closing, methods through `any`, and threads. I found no memory-safety bug: no
segfault or garbage read in about 40 probes. The callgrind claims reproduce to the digit. The
SHOULD-FIX items are semantic gaps, all on paths outside the result types' own field reads. Most are
newly reachable but fail safely with a TypeError, and none of them is listed in D-178's known gaps.

## Ranked findings (one line each)

1. **BLOCKER** `run/any_maps_error.pts` fails: it still expects `keys -> TypeError: Iterators of maps and sets through 'any' are not supported yet` (C28 removed that error). Not on base.
2. **BLOCKER** No in-tree regression tests for C28 (methods through `any`), C32 (`yield*` hands results through), C33 (an elision reads only `done`), C41 (closing reads nothing of `return()`'s result), or `keys`/`values`/`entries` through `any`. Not on base.
3. **SHOULD-FIX** A conversion from `any`/`unknown` to a result type (`return anyValue` from `next(): IteratorResult<T>`, `x as IteratorResult<T>`, an annotated local, `await it.next()`) still copies the object into a D-109 shape. That runs the getters right away, including a done result's `value` getter inside `for…of`. This is C30's and D-146's defect through `any`, and D-178 doesn't list it. Pre-existing.
4. **SHOULD-FIX** A by-name read of `value` converts with cgen's union conversion to `T | undefined`. Dynamic data whose `T` is an interface throws `TypeError: Expected undefined | {name: string}, got Object`, and other object types are copied again at each read, so identity and writes are lost (`r.value === r.value` is false). New: base gave PTS1001.
5. **SHOULD-FIX** A result object of another layout that leaves the result types (to an interface parameter, or to a union with another object type) is read by the declared field kind: `TypeError: Cannot read property value of Object as number | undefined: it is declared number`, a message the runtime says "only a compiler bug makes". `{…, extra} as IteratorYieldResult<T>` passed to an interface parameter worked on base (base copied it) and now throws, so that part is a regression. D-178's gap names only getters.
6. **SHOULD-FIX (perf)** The fast path of `resultRead`/`resultStore` tests only the shape's first descriptor (`pts_type_object1`). It misses the shape's other property-order descriptors, which share the same struct (`pts_type_object1_2`). Literals written `{ done, value }` (done first) therefore always go by name, and `done` (a bool field) is never cached. A read loop over stored one-shape results costs 3.56x the base in instructions.
7. **NOTE** Spreading `{...r}` or object rest of a handed result drops the object's other own properties. This is consistent with how base spreads interface-typed values, and is newly reachable for results.
8. **NOTE** Docs: SPEC §9 has no H03b change-log entry (H02 and H03a have §9.120 and §9.121). §9.121 still lists the deleted `errors/iterator_result_getters.pts` and calls the now-unskipped tests "skipped known issues". `PROGRESS.md` still lists C28 to C35 as open. D-178's known gaps miss items 3 to 7.
9. **NOTE** Through `any`, arrays' `keys()` and `entries()` are snapshots taken at the call, with all pairs built in O(n). That matches typed code (D-110), but JS's are live. Tuples and typed arrays have no `keys`/`values`/`entries` through `any` (pre-existing: tuples have no methods through `any` at all).
10. **NOTE** `iteratorResultShape` sets `existing.Result = true` on any program shape with the same fields, so user data such as `{done: boolean; value: number | undefined}` pays the descriptor test and the by-name slow path on every read and write.
11. **NOTE** The interface reads in item 5 are safe only because descriptors carry declared types (`e.declaredTypes`). I found none of 384 emitted test programs without them, but `pts_field_address(…, NULL)` would read a double field as a `pts_value`, and a denormal would become a pointer. Worth an assertion or a comment.
12. **NOTE** In the threaded fallback of `last_step`/`handed_value` (`last_resumed` names another iterator), `it->value` and `it->result` are two words read apart. A race could pair an ABSENT value with an ABSENT result and leak `PTS_VALUE_ABSENT`. This needs a shared generator plus a `done` getter that resumes another iterator. Exotic, not reproduced.
13. **NOTE (pre-existing, other slices)** `const { a, ...rest } = i` with `i` typed as an interface emits C that clang rejects (`pts_value_from_number()` with no argument). Belongs to H04.

## Commands

`/tmp/pts-h03b-review/run.sh X.pts` copies X.pts to X.mts and runs, in order:
- Node: `node --no-warnings --experimental-transform-types X.mts`
- new: `PTS_HOME=/home/bryan/projects/parallelscript PTS_CACHE_DIR=/tmp/pts-h03b-review/cache /tmp/pts-h03b-review/pts run X.pts`
- base: `PTS_HOME=/tmp/pts-h03b/base PTS_CACHE_DIR=/tmp/pts-h03b-review/cache-base /tmp/pts-h03b/base/pts-bin/pts run X.pts`

It uses `timeout`, `ulimit -f 40960` and `pipefail`. Every probe is in `/tmp/pts-h03b-review/p/`. The three outputs of each one cited here are in `/tmp/pts-h03b-review/out/<name>.txt`.

---

## 1. BLOCKER: `run/any_maps_error.pts` fails on this tree

- **Command:** `cd /home/bryan/projects/parallelscript && PATH=$HOME/.local/go/bin:$HOME/.local/node/bin:$PATH GOMAXPROCS=2 go test -p 2 ./pts/tests/ -run 'TestRun/any_maps_error' -count=1` (output: `out/any_maps_error.test.txt`)
- **new:** FAIL. Got `keys ok`; the expect block wants `keys -> TypeError: Iterators of maps and sets through \`any\` are not supported yet`.
- **base:** passes.
- **Node:** the test is `no-diff`. JS has map iterators, so `keys ok` is the right output.
- **At fault:** the test's expectation at `pts/tests/run/any_maps_error.pts:32`, and its header comment ("their iterators come with M4.1"). The behaviour changed in `pts/runtime/src/dynamic_methods.c:959-973` (`map_values`/`map_keys`/`map_entries`). The test pinned the old limitation, so updating it is legitimate; the commit message should say so.
- **Process:** the parent's `full1` run predates the final runtime edits (file mtimes: `dynamic_methods.c`, `generator.c` and `pts.h` at 01:14:52, the run ended at 01:13:56). After the fix the full suite must be rerun on the final tree.
- **My runs on this tree:** `^TestRun$` gave 417 pass, 92 skip, 1 fail (this test); `^TestRunMatchesNode$` gave 293 pass, 124 skip, 0 fail; `TestErrors` plus an iterator-themed corpus subset gave 86 pass, 0 fail.

## 2. BLOCKER: rows fixed without in-tree tests

The tree has tests for C30 (`run/iterator_next_result_getters.pts`, unskipped), C35 (`run/iterator_result_as_cast.pts`,
unskipped) and C03's typed half (`run/iterator_result_getters_typed.pts`, unskipped, and the new
`run/iterator_result_getter_literals.pts`). Nothing in `pts/tests/` exercises:
- C28: `h.next/return/throw` through `any` on generators, async generators and runtime iterators, and `h.return` being "not a function" on array iterators;
- C32: getter read counts through `yield*`, nested `yield*`, forwarded `return`/`throw` results handed through;
- C33: an elision reading only `done`. `run/array_pattern_lazy.pts:172` has an elision, but over results without getters;
- C41: IteratorClose reading nothing of `return()`'s result;
- `keys()`/`values()`/`entries()` of maps, sets and arrays through `any`;
- D-143 with handed results (threads stepping generators whose `yield*` hands results through).

The project rule says "a feature is done only when it has tests". Ready-made, Node-checked programs:
`p/c01_yield_star.pts`, `p/c02_yield_star_return_throw.pts`, `p/c03_lying_done.pts`, `p/c04b_close_any_return.pts`,
`p/c07_missing_methods.pts`, `p/d01_any_methods.pts`, `p/d05_any_iter_consumers.pts` and `p/f01_async.pts` (first half) all match Node
exactly on the new tree. The thread programs (`p/e01_threads_private.pts`, `p/e02_threads_shared.pts`, `p/e03_threads_forof_shared.pts`,
which print `bad 0` with PTS_THREADS=4 and 8) could become `// threads: 1 2 4` tests.
The parent's `/tmp/pts-h03b/probes/c28|c32|c33.pts` are the same idea.

## 3. SHOULD-FIX (pre-existing): `any`/`unknown` → result type copies, running getters early

- **Repros:** `p/c06_any_done_value_throws.pts` (the D-146 case through `any`), `p/c05_any_to_result.pts`, `p/f02_async_await_getters.pts`
- **c06.** Node: `done read` / `loop done`. New and base: `done read` / `Uncaught Error: value of a done result was read`.
  A `next(): IteratorResult<number>` whose body returns an `any` getter result has its `value` getter run although the result is done.
- **c05.** Node reads nothing until asked (`got r`, then `next done read`). New and base read `done` and `value` at the conversion:
  at `return make()` inside `next()`, at `make() as IteratorResult<number>`, and at `const x: IteratorYieldResult<number> = make()`.
- **f02** (`await ui.next()` on a typed `AsyncIterator`): new and base read both getters at the await.
- **At fault:** a dynamic value converted to a result type still goes through `fromValue` and `pts_value_convert`
  (`pts/cgen/cgen.go:1106`), whose `copy_object` calls getters (`pts/runtime/src/convert.c:287`). `ir.ResultObject`
  (`pts/cgen/classes.go:845`) keeps the program's objects as they are, but only at the `IteratorCall` site.
  Using ResultObject's rule (keep a `PTS_KIND_OBJECT`, copy only other dynamic data) for every Value-to-result conversion would close this.
- Not in D-178's known gaps, although D-178's reason ("getters on a typed `next()`'s result run when the caller reads them") and SPEC's
  "`x as IteratorResult<T>` … read by name" read as unconditional.

## 4. SHOULD-FIX (new): by-name `value` reads convert to `T | undefined` with the union's check

- **Repro:** `p/h07_class_any_value.pts`. A class `Entry { done = false as const; constructor(public value: any) {} }` holds JSON data and is used as `IteratorResult<Row>`.
  - Node: `array 2` / `row a` / `one a`
  - new: `array 2` / `row Expected undefined | {name: string}, got Object` / `one Expected undefined | {name: string}, got Object`
  - base: PTS1001 ×3 (`converting Entry to object1 | object2 …`)
  - For comparison, `const x: Row | undefined = rows[0]` works on both trees: the lowering converts to the member `Row`, not to the union (`p/h04_any_to_union_iface.pts`).
- **Repro:** `p/h02_any_array_values.pts`. A literal `{ done: false, value: raw /* any[] */, extra: true } as IteratorResult<[number, string], undefined>`.
  - Node: `true` / `99`
  - new: `false` / `1`. Each read of `r.value` converts the array again, so identity is lost and `r.value[0] = 99` is lost.
  - base: PTS1001.
- **Realistic shape of the bug:** `p/h03_json_rows.pts`, an iterator over `JSON.parse` rows whose results are class instances. Node mutates and prints;
  new throws `TypeError: Expected undefined | {n: number; name: string}, got Object`; base PTS1001.
- **At fault:** `resultValue` (`pts/cgen/classes.go:822-829`) calls `e.fromValue(value, t)` with `t` the union `T | undefined`
  (D-109's shapes always hold `value: T | undefined`). This is the same per-read conversion that D-177 moved to binding time for
  patterns (C37), for the same identity reasons. A member-aware conversion (undefined stays undefined, otherwise convert to `T` as
  `l.convert` does), or keeping the program's objects as they are, would fix both repros.

## 5. SHOULD-FIX (new reachability; partly a regression): result objects of another layout read through other types

- **Repro (regression):** `p/a01_iface_foreign.pts`. `{ done: false as const, value: 1e-310, extra: 7 } as IteratorYieldResult<number>`
  passed to `show(r: HasValue)` with `interface HasValue { value: number | undefined }`.
  - Node: `1e-310` / `show 1e-310`
  - base: `1e-310` / `show 1e-310` (base copied the literal at the `as`: `pts_value_convert(…, &pts_check_1)`)
  - new: `1e-310` / `Uncaught TypeError: Cannot read property value of Object as number | undefined: it is declared number`
- **Repro (new route):** `p/a04_iface_handed_one.pts`. A typed `next(): IteratorResult<number, number>` returns a literal with an extra
  property, and the caller passes `it.next()` to the same interface parameter. Node: `show 1.5` / `show 3`. New: the same TypeError.
  Base: PTS1001 (honest).
- **Repro (mixed union):** `p/a06_mixed_union2.pts`, `IteratorResult<number, number> | Plain`. Node: `1 false` / `3 false`. New: the same TypeError. Base: PTS1001.
- **At fault:** `asResult` (`pts/lower/unions.go:571-581`, reached from `pts/lower/classes.go:1064`, `pts/lower/objects.go:887`,
  `pts/lower/unions.go:184/202/516`) puts objects of any layout into result-typed values. Only `FieldRef`/`SetField` on result types
  were taught about this (`pts/cgen/classes.go:810-843`). An interface conversion from a result shape is still a static `ClassCast`,
  read by `fieldPlace` (`pts/cgen/classes.go:719`) through `pts_field_address`. The runtime's kind check `wrong_kind`
  (`pts/runtime/src/object.c:79`) throws, with the "only a compiler bug makes one" message.
- **Safety:** I tried to get a garbage read (a denormal double read as a `pts_value` pointer). Every program I emitted, including the 384
  test programs, carries declared types, so the kind check is always on (finding 11). D-178's known gap says only "a TypeError for a
  getter". A plain data property of another representation fails too, and that is the common case: a literal's `value: 1` is a double
  field, while the result types' `value` slot is a `pts_value`. Either convert such reads by name, keep the copy for conversions that
  leave the result types, or give an honest PTS1001.

## 6. SHOULD-FIX (perf): the fast path misses the shape's other property-order descriptors

- **Repro:** `bench/r4_donefirst_reads.pts`. A class iterator returns `{ done: false, value: this.i++ }`, the results are stored as
  `IteratorResult<number, number>[]`, and `r.done`/`r.value` are read 2,000,000 times.
  Callgrind (`PTS_THREADS=1 valgrind --tool=callgrind ./r4_donefirst_reads.{base,new}`): base 107,127,156, new 381,252,150 (**3.56×**).
  The same loop over results the runtime made (`bench/r2_shape_reads.pts`) is 1.205×.
- **Cause, verified in the emitted C (`bench/r4.new.c`):** the literals are allocated as `pts_type_object1_2`, the done-first order of
  shape `object1` (`pts_orders_object1 = {&pts_type_object1, &pts_type_object1_2}`), which uses the same struct `pts_class_object1`.
  Every read tests `type == &pts_type_object1` (`pts/cgen/classes.go:814`; stores at `:838`), so it goes to `pts_result_get`. There
  `done`, a bool field, is never cached (`pts/runtime/src/object.c:326-345` caches only `PTS_FIELD_VALUE` fields), so each read is
  `find_field` with strcmp, then `pts_value_of_slot`.
- **Suggested fix:** test every order descriptor of the shape, all of which share its struct. Frozen twins must still take the slow path
  for stores. Or cache field kinds in the site.
- **The draft's claims reproduce exactly** with 1M steps (`bench/callgrind-summary.txt`): `for…of` over a generator 1.030, `yield*`
  1.032, typed `next()` over one shape 1.085, over a union 0.851, over a class's iterator 0.570 (0.555 done-first), `for…of` over a
  class's iterable 1.033. Reads of stored union results got faster (`r1_union_reads` 0.716).

## 7. NOTE: spread and object rest of a handed result

- **Repro:** `p/b04_spread_rest.pts`
  - Node: `{ extra: 'e', value: 7 }` and `{ extra: 'e', done: false, value: 7, x: 1 }`
  - new: `{ value: 7 }` and `{ done: false, value: 7, x: 1 }`
  - base: PTS1001
- Base does the same for interface-typed values (`p/b05_spread_iface.pts`: Node `{ a: 1, b: 2 }`, new and base `{ a: 1 }`). So this is the
  existing static-spread limitation, now reachable for results. Worth a line in D-178's known gaps.

## 8. NOTE: docs

- `docs/SPEC.md` gains no §9 entry for H03b (compare §9.120 H02 and §9.121 H03a). §9.121's test list (around line 1550) still names
  the deleted `errors/iterator_result_getters.pts` and calls the three now-unskipped tests "skipped known issues".
- `PROGRESS.md:211` still lists C28/C30/C32/C33/C35 as open. Presumably that is updated with the commit.
- D-178's known gaps miss items 3 to 7. The other gaps it lists check out: a missing `done` reads as `false` through one shape and as
  `undefined` through a union; a write of a missing `value` throws `Cannot add property value, object is not extensible`
  (`p/i01_missing_done.pts`).

## 9. NOTE: array iterators through `any`

- `p/d02_array_iter_live.pts`: Node gives `[ 0, 1, 2 ]` and `[ [ 0, 10 ], [ 1, 2 ], [ 2, 3 ] ]` after a write and a push. New gives `[ 0, 1 ]` and
  `[ [ 0, 1 ], [ 1, 2 ] ]` both typed and through `any`. Typed code was the same on base (D-110). `array_entries`
  (`pts/runtime/src/dynamic_methods.c:742`) builds every pair at the call, so `bigArray.entries()` through `any` costs O(n) before the first `next()`.
  Its comment says "the array itself, read live; its indices; its pairs", but only `values()` is live.
- `p/d04_typed_arrays_any.pts`: tuples and typed arrays through `any` have no `entries`/`values` (a tuple not even `map`). Pre-existing,
  but tuples are arrays in JS, so "arrays' keys/values/entries through `any`" is partial.

## 10. NOTE: `existing.Result = true`

`pts/lower/generators.go:79` marks any earlier program shape with the key `{done: boolean; value: T}` as a result shape. That is
semantically harmless: all reads go by name off foreign layouts. But every field read and write of such user data now pays the descriptor test,
and, per finding 6, the slow path when the literal was written in another order. The comment ("an object type of the program's with
the same fields is one") could say so.

## 11. NOTE: interface-read safety depends on declared types

See finding 5. With `e.declaredTypes` false, `siteCheck` returns `NULL` (`pts/cgen/classes.go:857-861`) and `pts_field_address_slow`
skips the kind check. A foreign result's double field would then be read as a `pts_value`, and a denormal such as `1e-310` is a pointer in
JSC encoding. I could not make a program without declared types: all 384 emitted run tests and every probe carry checks. The
safety of item 5 therefore rests on that; worth an assertion or a sentence in D-178.

## 12. NOTE: two-word fallback in the threaded step memory

`last_step` (`pts/runtime/src/generator.c:77-84`) and `handed_value` read `it->value` and `it->result` separately when `last_resumed`
names another iterator. With a generator shared by threads, whose handed result's `done` getter resumes a second iterator between the
step and the read, the pair can tear: an ABSENT value with an ABSENT result returns `PTS_VALUE_ABSENT` to typed code. D-143 already
accepts skipped or repeated items. Leaking ABSENT is a little worse, but it needs a shared generator plus a reentrant getter. Not
reproduced: `p/e01`, `e02` and `e03` print `bad 0` at 4 and 8 threads, three runs each.

## 13. NOTE (pre-existing, out of scope): clang rejects object rest of an interface value

`p/z01_rest_iface_clang.pts` (`const { a, ...rest } = i` with `i: I`): Node prints `1 { b: 2 }`. New and base fail in clang:
`error: too few arguments to function call … pts_value_from_number()`. Route to H04.

---

## Verified good (evidence)

- **C28** (`out/d01_any_methods.txt`, `out/d05_any_iter_consumers.txt`, `out/f01_async.txt`): generator `next(v)`/`return(v)`/`throw(e)` through `any`,
  array/string/map/set iterators' `next()`, `ai.return` → `TypeError ai.return is not a function`, `keys/values/entries` of maps, sets
  and arrays, consumers (`Array.from`, `new Set/Map`, patterns, `for…of`, `Promise.all`), and async generators: all identical to Node.
  Base: `TypeError: h.next is not a function`. Only `typeof h.next` differs (`undefined`, as documented).
- **C30** (`p/b02_consumers.pts`, `p/b03_class_results.pts`, `p/j01_generics.pts`, `p/l01_result_types.pts`): handed results of other layouts (extra
  properties, getters, class instances with inherited getters, private `#value`) are read by name through FieldRef, generics, a generic class field,
  `.map` callbacks, `Map` values, compound and logical assignments, `Object.freeze`, `JSON.stringify`, `Object.keys/values`, `in` and optional chains.
  All match Node; base rejects them with PTS1001.
- **C32** (`out/c01_yield_star.txt`, `out/c02_yield_star_return_throw.txt`, `out/c03_lying_done.txt`, `out/c07_missing_methods.txt`): read counts at
  every level of nested `yield*` for `for…of`, typed `next()`, spread and patterns; a `done` getter answering false to `yield*` and true to the
  consumer (the generator stays suspended and a later `next()` resumes it); `return("keep")` handing a not-done result through; throw without
  a `throw` method. All identical to Node; base differs.
- **C33/C41** (`out/c04b_close_any_return.txt`): elisions read only `done`; break, pattern end and throw close without reading `return()`'s result;
  a non-object `return()` result gives Node's TypeError on break and is dropped on a throw. Identical to Node; base read `value` and `done`.
- **C35** (`p/a01`'s direct read, `run/iterator_result_as_cast.pts`): narrowed reads of asserted literals go by name.
- **Threads (D-143)** (`p/e01_threads_private.pts`, `p/e02_threads_shared.pts`, `p/e03_threads_forof_shared.pts`): private generators with handed
  results in 400 `parallel for` iterations, and 32 to 64 workers racing on one shared generator and one shared object iterator through
  typed `next()` and `for…of`: no crash, every value valid, at PTS_THREADS=4 and 8.
- **Lowering:** unusual call forms (`p/m01_call_forms.pts`) give the base's diagnostics and no panic. `as` from `T | undefined` to a result
  shape still throws TypeError on undefined (`p/n01_as_from_undefined.pts`).
- **GC:** the new `pts_iterator.result` lives in `GC_MALLOC`'d, conservatively scanned frames and iterators. `last_resumed` is uncollectable.
  `PTS_VALUE_ABSENT == 0` makes zero-filled iterators (`from_sync`, async generators) start with no result.

## Code-quality remarks (minor)

- `pts_generator_delegate` repeats `last_step`'s `last_resumed` test inline before calling it. That is fine for the fast path, but a one-line comment
  saying so would help.
- `method_mode` casts away `const` (`pts_value_from_object((pts_iterator *)it)`). `pts_value_is_async_generator` could take the type pointer instead.
- The new code otherwise matches the surrounding style: comments in the codebase's voice, D-numbers cited, and helpers named after their outcome
  (`resultRead`, `asResult`, `isObjectOfProgram`). `isObjectOfProgram`'s doc line ("which their properties can be found by") reads awkwardly.

---

## Round 2: iterator results through `any` and typed `next()`

Reviewer: claude-opus-5-5 (xhigh), read-only, 2026-10-16. Tree: the parent's uncommitted working tree on base
`a65a75d62`, including the runtime change made during this review (`call_object_method`, `generator.c` mtime
02:36:16). I rebuilt the CLI after that change: `/tmp/pts-h03b-review2/pts`. Base CLI: `/tmp/pts-h03b/base/pts-bin/pts`.
Probe outputs from before the rebuild (clang `call_method` arity errors) were thrown away and rerun.

## Verdict: NOT READY (no blocker; three small SHOULD-FIX defects to fix and pin first)

The slice is in much better shape than in round 1. Both round-1 blockers are closed, and so are findings 3, 4, 6, 9, 10
and 12. Finding 5 is only partly closed. All 20 slice tests pass (`TestRun` plus `TestRunMatchesNode`), the threads test
passes 10 of 10 runs at 4 and 8 threads, and the compiler unit tests and `TestErrors` pass. I found no crash, garbage
read or GC or thread-safety defect.

Three SHOULD-FIX defects remain, each a few lines to fix:
- **S1 (new).** A typed `it.return!()` or `it.throw!()` on the runtime's iterators (arrays, maps, sets, strings) doesn't
  throw. It silently finishes the iterator. Base gave PTS1001 here, and D-178's text claims the TypeError.
- **S2 (round-1 #5, partly open).** Round-1 #5 survives on two lowering routes. One of them is a regression: base ran
  these programs correctly, and now they throw the "only a compiler bug" TypeError.
- **S3 (new).** A postfix `r.value++` on a result runs its getter twice.

## Round-1 findings: status

| # | Round 1 | Now | Evidence |
|---|---|---|---|
| 1 | BLOCKER `any_maps_error` failed | **fixed** | expects `keys ok`; passes `TestRun` (skipped in `TestRunMatchesNode` by its `no-diff`) |
| 2 | BLOCKER no tests for C28/C32/C33/C41 | **fixed** | 13 new and imported tests, all pass in both tests; see "Test quality" |
| 3 | `any` to result type copies (getters early) | **fixed for result types**; residual NOTE N1 | `c05`, `c06`, `f02` now print Node's output |
| 4 | by-name `value` reads used the union's check | **fixed** (the per-read copy is now ledger C51, declared) | `h07` matches Node; `h02`, `h03` differ only by C51 |
| 5 | result of another layout read through an interface | **partly fixed**: S2 below | `a01`, `a02`, `a04`, `a06` match Node; `p12`, `p14`, `routes/r_cond` fail |
| 6 | fast path missed done-first orders (3.56x) | **fixed** | `r4_donefirst_reads` 1.280x (callgrind, below) |
| 7 | spread drops properties | declared gap (C54) | `b04` unchanged |
| 8 | docs | **mostly fixed**; NOTE N5 (stale C47, stale bench numbers) | SPEC §9.122, D-178, ledger C41 to C54 |
| 9, 10 | comments | **fixed** | `dynamic_methods.c` `array_values`, `generators.go` `iteratorResultShape` |
| 11 | interface reads safe only through declared types | unchanged, still relevant to S2's routes | every program I emitted carries checks, including a minimal one (`p/p22_min.pts`) |
| 12 | ABSENT leak in threaded fallback | **fixed** | `handed_value` returns undefined; `pts_generator_delegate`'s fallback then reads as undefined too |
| 13 | clang rejects interface object rest | pre-existing, ledger C50 (H04) | `z01` unchanged |

All 58 round-1 probes were rerun against Node (`out/r1_*.txt`). Every difference is a declared gap (C46, C51 to C54,
C44, D-110) or pre-existing (`d03`/`d04` tuples and typed arrays through `any`, `m01` call forms, `z01`).

---

## S1. SHOULD-FIX (new): a typed `return!`/`throw!` on a runtime iterator silently finishes it

- **Repro:** `p/p03_return_bang_runtime_iters.pts` (array, map-keys and string iterators, and an array iterator typed `Iterator<number>`)
- **Node:** `ai TypeError ai.return is not a function`, then `{ value: 2, done: false }` (the iterator goes on); the same for
  `mi.return`, `si.throw` and `it.return`.
- **New:** `{ value: undefined, done: true }` for each `return!()`, and the iterator is finished (every later `next()` says done).
  `si.throw!(e)` throws `The iterator does not provide a 'throw' method.` and also finishes the iterator.
- **Base:** PTS1001 `` `ai.return` is not supported yet `` (4x), an honest diagnostic.
- **Cause:** `pts_iterator_call` (`pts/runtime/src/generator.c:536-546`) sends runtime iterators to `resume_runtime_iterator`
  (`:446`), which follows yield*'s rules. A missing `return` finishes the iterator with the value, and a missing `throw`
  closes it and throws yield*'s TypeError. A direct method call needs call semantics. The parent made this split for
  object iterators (`call_object_method` against `call_object_iterator`) but not for the runtime's own iterators.
  Through `any`, `method_mode` (`:593`) gets this right.
- **Contradicts D-178** ("An object without the method throws JS's TypeError (`it.return is not a function`), named by the
  call's source text"). No test covers it: `yield_star_result_edges.pts`'s new section uses a program object.
- **Fix:** in `pts_iterator_call`, for a runtime iterator (not a Generator, not an object iterator) and `mode != PTS_RESUME_NEXT`,
  throw `"%.*s is not a function"` with `text`. Then add the four lines of `p03` to a test.

## S2. SHOULD-FIX (round-1 #5 still open on two routes; a regression for asserted literals)

A result object of another layout still reaches an interface through a plain `ClassCast` (no `FromResult`) on two routes:

1. **The relaxed cast** (an interface with an optional property the result type lacks):
   `pts/lower/objects.go:898-900` (`holdsOptionally`) returns `&ir.ClassCast{X: value, To: target}` without going through `interfaceCast`.
   - `p/p12_relaxed_nocheck.pts`: `{ done: false as const, value: 1e-310, extra: true } as IteratorYieldResult<number>`, then passed to
     `show(r: { value: number | undefined; note?: string })`. Node `show 1e-310`; **base `show 1e-310`** (base copied at the `as`);
     new `Uncaught TypeError: Cannot read property value of Object as number | undefined: it is declared number`. **Regression.**
   - `p/p14_optional_value_iface.pts`: a typed `next(): IteratorResult<number, number>` returns `{ done, value: n * 1.5, extra: "x" }`
     through a local, and `it.next()` is passed to `showNote(r: WithNote)` with `WithNote = { value: number | undefined; note?: string }`.
     Node `note 1.5 undefined`; new: the same TypeError; base PTS1001 (honest). A realistic program. `{ value?: number }` works (`opt 1.5`).
2. **The contextual-interface conditional:** `sameType` (`pts/lower/lower.go:3335-3339`) runs `convertedResult` before
   `asContextualInterface` (`:3372-3381`), whose two `ClassCast`s are therefore never marked.
   - `p/routes/r_cond.pts`: `const a: HV = flag ? res : new Other(2)` with `res` the asserted literal above.
     Node and **base** `cond 1e-310`; new: the TypeError above. **Regression.**
- Twelve other routes (a return, a field, a Map value, a Set, a tuple, a rest parameter, an optional parameter, an array literal,
  a `readonly HV[]` parameter, `map` with an `HV` callback, a generic constraint, equality) match Node (`p/routes/r_*.pts`).
- The message is the runtime's "only a compiler bug makes one". The h03b.md table says #5 is closed.
- **Fix:** give the relaxed branch the same `FromResult` treatment (or route it through `interfaceCast`, if its copy
  shape handles optional properties), and apply `convertedResult` after `asContextualInterface` (or have it build casts
  through `interfaceCast`). Pin `p12`, `p14` and `r_cond` in `run/iterator_results_other_types.pts`.

## S3. SHOULD-FIX (new): postfix `++`/`--` on a result reads its getter twice

- **Repro:** `p/p08_incr_accessors.pts`; also `p/p07_order.pts` line 4 of the output
- **Node:** `result 6 get,set 6` and `union 8 get,set 8`. **New:** `result 6 get,get,set 6`, `union 8 get,get,set 8` (the union case is
  narrowed to the yield result's shape). Prefix `++r.value` and compound `r.value += x` read once, as do class, interface and
  object-literal receivers. **Base:** can't build the program (`Expected object1, got Object` at run time).
- **Cause:** `readAndStore` (`pts/lower/lower.go:2977-2980`) treats a Result class receiver as a plain field
  (`target.field != nil && !viaInterface`) and caches no `current`. cgen's postfix `setField` (`pts/cgen/classes.go:886-894`)
  then reads `old` with `fieldRead` while the stored value holds a second `FieldRef`. Each `fieldRead` on a result receiver
  is `resultRead`, a getter call for a handed object.
- **Fix:** in `readAndStore`, treat `isResultReceiver`-like receivers (a `Class.Result` object) like `viaInterface` so the read
  is bound once. Getter read counts are H03b's acceptance theme, so this belongs in the slice. Pin with `p08`.

---

## NOTEs

- **N1 (pre-existing; C42 residual).** The `fromValue` keep-object rule applies only when the target *is* a result type
  (`isResultType`, `pts/cgen/cgen.go:1106`, `classes.go:810`). Other targets still copy, running both getters at the conversion,
  as base does:
  - a `IteratorResult<T> | undefined` local from `any`;
  - an `any` array converted to `IteratorResult<T>[]`;
  - a write through `any` into a `Map<string, IteratorResult<T>>`;
  - `await Promise.all([ui.next(), ui.next()])` into a tuple of results.

  Probes: `p/p02_union_undefined_from_any.pts` (Node `got u` then `u done read`; new `u done read`, `u value read`, `got u`;
  arrays and the Map write likewise) and `p/p15_async_then.pts` (the `Promise.all` lines). D-178 states the rule exactly, so
  this is not a contradiction. Extending `isResultType` to unions whose other members are `undefined`/`null` is cheap. The
  array, tuple and Map cases are worth a ledger line.
- **N2 (declared, but its consequences are not spelled out).** Converting a result of another layout to an interface
  (`ClassCast.FromResult`) copies it when its declared types don't fit. Identity and writes through the interface are lost,
  and getters run at the call even if the callee never reads them. `p/p04_iface_identity.pts`: Node `result true 99`,
  `literal true 99`, `getter reads after ignore 0`; new `result false 6`, `literal false 7`, `getter reads after ignore 1`
  (the own-shape case keeps identity). A plain object of the same layout gets PTS1001 on both trees ("whose `value` it
  doesn't provide with the same types"). D-178's "copied otherwise, as D-071 converts dynamic data" is accurate. It could
  add "so identity, writes through the interface and the getters' timing differ from JS".
- **N3 (pre-existing).** A `yield*` whose value is unused never reads the final done result's `value`, which JS always does
  (IteratorValue in yield*'s step). `p/p01b_yield_star_stmt_any.pts`: Node `done read 2`, `value read 2`, `after`; new and base skip
  `value read 2`. The delegation's `value` expression (`pts/lower/generators.go:209`) is emitted only in the return path or
  where the value is used. A getter-count divergence of the kind H03b is about. Ledger-worthy.
- **N4.** A non-boolean `done`, read through a union result type, throws `Expected boolean | undefined, got number`. Through one
  shape it reads as its truth (`true`, where Node prints `1`). `p/p20_nonbool_done.pts`. D-178's "a `done` that isn't a boolean reads as
  its truth" holds for one shape only (`resultValue`'s `ir.Bool` case, `pts/cgen/classes.go:851`). The typed source is lying, so this
  is a docs nuance.
- **N5 (docs accuracy).**
  - **C47 is fixed, not open.** `p/gaps/g3_print_order.pts` and the reproducer's own `p/repro/adj_p7_misc.pts` print
    `{ done: false, value: 1 }` on the new tree, as Node does; base `{ value: 1, done: false }`. The literal keeps its
    done-first order descriptor and is handed back as it is. D-178's known gap ("a result literal that is a D-109 shape prints
    value first") and the ledger's C47 "open — H29" are stale. A generator's own results print value first in both.
  - **Benchmark numbers in D-178 and h03b.md for `b3`/`b3d` predate the final runtime.** `/tmp/pts-h03b-s2/bench/b3_user_forof.new`
    was built at 02:31:32 and `generator.c` last changed at 02:36:16. On the final tree: `b3_user_forof` **1.049x**
    (D-178: 1.03x), `b3d` **1.051x** (D-178: 1.06x). The +14 instructions a step come from `call_object_method` being a separate,
    non-inlined call (24M + 10M instructions in the profile), where round 1's `call_object_iterator` cost 40M. `static inline` would
    probably recover it. `b2_next_shape` is 1.119x (D-178 says 1.12x, which is consistent; round 1 was 1.085x).
- **N6.** No test pins the declared gaps C51 (per-read copy) and N2 (identity through an interface). `iterator_results_other_types.pts`
  reads `row.value.name` once, so it would pass whichever way they go. That is acceptable for declared gaps. A skipped
  known-issue test with Node's output, as the project does elsewhere, would keep them visible.

## Verified good (new evidence this round)

- **Getter read counts and order:** argument order, compound and logical assignments, template literals, conditionals
  (`p/p07_order.pts`, all except S3's postfix line). Array patterns with defaults, nested patterns, holes and rest, typed and
  through `any` (`p/p23_pattern_reads.pts`, base read `value` at the hole). A statement-position `yield*` reads `done` twice,
  as Node does (`p/p01_yield_star_stmt_value.pts`, apart from N3).
- **Result types holding handed objects:** unions with `undefined`/`null`/a class/a number, `instanceof`/`typeof`/`in`
  narrowing, optional chains (`p/p10_mixed_unions.pts`). Narrowing to a member and passing it on as a parameter, an
  assertion, an array element or a return (`p/p19_narrow_member.pts`). Identity of by-name values: classes, interfaces,
  arrays, tuples, Maps, functions, nested objects (`p/p06_byname_identity.pts`). Value types: function types, classes with
  methods, generic classes, tuples, Maps, Promises, `Float64Array`, nested generators, unions (`p/p05_value_types.pts`).
  `Object.keys`, `JSON.stringify`, `hasOwnProperty`, a custom `toString` (`p/p18_union_methods.pts`). All match Node.
- **Frozen results:** writes to frozen own-shape results (both orders) and to frozen handed objects throw TypeError, as in
  Node (`p/p09_frozen.pts`). Base wrote through a frozen handed object (`no throw`, `6`).
- **Async:** `then()` callbacks on a typed `AsyncIterator`'s `next()` read nothing early (`p/p15_async_then.pts`, apart
  from N1's `Promise.all`).
- **Realistic utilities:** zip, take, a `Peekable<T>` holding `IteratorResult<T, undefined> | undefined`, a class
  `IterableIterator`, manual stepping of map/set/string iterators (`p/p21_realistic_utils.pts`): identical to Node.
- **Threads (D-143):** `run/iterator_results_threads.pts` built once and run 5x at `PTS_THREADS=4` and 5x at 8: 10/10 match
  the expect block. Round-1 `e01` to `e03`: `bad 0` at 4 and 8.
- **GC:** every `pts_iterator` subtype is `GC_MALLOC`'d and scanned whole (`generator.c`, `promise.c` `allocate`).
  `generator->result` is reset before each step, so a handed result stays alive for at most one step, and `last_resumed` is
  uncollectable. `from_sync` (`step == NULL`) is rejected by `method_mode` and is never visible to programs.
- **IR plumbing:** every `*ir.Yield` consumer (`ir.Walk`, `cellPass`, the printer, cgen frames and emission) handles `Delegate`.
- **Unit tests:** `go test ./pts/lower/ ./pts/cgen/ ./pts/ir/` ok. `TestErrors` ok. Focused
  `^(TestRun|TestRunMatchesNode)$/(iterator|yield_star|array_pattern|any_maps|generator|for_of)`: 102 pass, 6 skip, 0 fail.

## Performance (callgrind, `PTS_THREADS=1`, final tree including the 02:36 runtime change; base counts from round 1)

| Program | Base | Round-1 new | Final | Ratio |
|---|---|---|---|---|
| `b4_user_next` (typed `next()`, class iterator) | 1,341,618,634 | 765,233,793 | 760,227,095 | 0.567 |
| `b4d_user_next_donefirst` | 1,341,606,538 | 744,229,679 | 731,228,166 | 0.545 |
| `b1_next_union` | 489,956,963 | 416,964,434 | 417,969,392 | 0.853 |
| `b2_next_shape` | 247,951,440 | 268,950,883 | 277,346,026 | 1.119 |
| `b3_user_forof` | 899,970,156 | 929,971,744 | 943,973,078 | 1.049 |
| `b3d_user_forof_donefirst` | 866,965,599 | 896,972,426 | 910,972,984 | 1.051 |
| `b5_delegate` | 247,799,450 | 255,799,302 | 255,799,349 | 1.032 |
| `b6_forof_gen` | 133,788,732 | 137,789,075 | 137,788,707 | 1.030 |
| `r1_union_reads` | 599,493,819 | 429,416,960 | 429,417,924 | 0.716 |
| `r2_shape_reads` | 107,257,863 | 129,270,587 | 129,273,546 | 1.205 |
| `r3_user_shape` | 180,178,071 | 180,189,487 | 182,201,718 | 1.011 |
| `r4_donefirst_reads` | 107,127,156 | 381,252,150 | 137,142,146 | **1.280** (round-1 #6 fixed) |

All outputs equal the base's. Programs, binaries, `cg.*` and logs are in `/tmp/pts-h03b-review2/bench/`.

## Test quality

- All 13 Node-checked new or imported files pass both `TestRun` and `TestRunMatchesNode`, so each expect block is Node's
  stdout. `run/iterator_results_threads.pts` checks itself (counts of bad values), is skipped by `TestRunMatchesNode`
  (`pts:parallel`), and passes at 1, 2 and 4 threads in the suite and 10 of 10 runs at 4 and 8 here.
- They pin the acceptance rows well. Read counts at each level of `yield*`, lying `done`, `return`/`throw` forwarding,
  elisions, IteratorClose, methods through `any` (generators, async generators, runtime iterators, `keys`/`values`/`entries`),
  conversions from `any`, done-first literals, interface parameters and conditionals.
- Gaps: S1 (no runtime iterator in the typed missing-method section), S2's two routes, S3 (no postfix on a result), and the
  declared C51/N2 (N6).
- Overlap: `iterator_result_getter_literals.pts` and `iterator_result_getters_typed.pts` both cover typed getter literals, but
  through different consumers. I see no duplicate without value.
- The `any_maps_error.pts` edit is legitimate: the old line pinned a limitation, and the header comment now says why.

## Commands

`/tmp/pts-h03b-review2/run.sh X.pts` copies X.pts to X.mts and runs, under `pipefail`, `ulimit -f 40960` and timeouts:
- Node: `node --no-warnings --experimental-transform-types X.mts`
- new: `PTS_HOME=/home/bryan/projects/parallelscript PTS_CACHE_DIR=/tmp/pts-h03b-review2/cache /tmp/pts-h03b-review2/pts run X.pts`
- base: `PTS_HOME=/tmp/pts-h03b/base PTS_CACHE_DIR=/tmp/pts-h03b-review2/cache-base /tmp/pts-h03b/base/pts-bin/pts run X.pts`

The probes are in `/tmp/pts-h03b-review2/p/` (`routes/`, `gaps/`, `repro/`). The three outputs of each cited probe are in
`/tmp/pts-h03b-review2/out/<name>.txt`, and the reruns of round-1 probes are in `out/r1_*.txt`. The focused suite events are in
`/tmp/pts-h03b-review2/gotest-focus.jsonl`.

---

## Round 3: check of the round-2 fixes

Reviewer: claude-opus-5-5 (xhigh), read-only, 2026-10-16. Tree: the parent's uncommitted working tree on base `a65a75d62`.
No compiler or runtime source changed after my build (`find … -newer` was empty at 03:24). CLI: `/tmp/pts-h03b-review3/pts`
(built 03:04:27), run with `PTS_HOME=/home/bryan/projects/parallelscript`. Base CLI: `/tmp/pts-h03b/base/pts-bin/pts`.
For before/after checks of compiler-only changes I also ran round 2's CLI (`/tmp/pts-h03b-review2/pts`) against the current
runtime. Round 2's compiler emits no `.result` flag, so that run shows the tree without N1 and S3.

## Verdict: NOT READY (no blocker; two small regressions, plus one older hole now cheap to close)

All five round-2 fixes work as described (S1, S2, S3, N1, N3), and none of the round-2 or round-1 probes changed for the worse.
Two of the fixes added a regression each, and each needs only a few lines:
- **R1 (N1, a regression from base).** Through `any`, a union of a result type and another object type now keeps an object as
  the result member when the other member needed a copy. Reads after narrowing then throw the "only a compiler bug"
  TypeError. Base printed Node's output.
- **R2 (S3, a regression from round 2).** `r.value += yield v` on a result reads the getter twice, once before and once after
  the yield. Round 2 read it once, as Node does.
- **R3 (in the slice since round 1).** A result-typed field holding an object of another layout leaks into an interface
  through `any`, because `pts_check_implies` trusts the result check's declared types. This contradicts D-178's claim that
  the conversion "holds for every route to an interface". Base gave PTS1001. With the new `result` flag the fix is one line.

I tried fixes for R1 and R3 in a scratch copy of the runtime (`/tmp/pts-h03b-review3/tree`, diff in
`experiment-convert.diff`). With them, all four R1/R3 repros print Node's output. The 20 slice tests still print their
expect blocks, and `n1_kinds` and `s2_routes` are unchanged. I didn't run the full suite with them.

## Round-2 findings: status

| # | Round 2 | Now | Evidence |
|---|---|---|---|
| S1 | typed `return!`/`throw!` on runtime iterators finished them | **fixed** | `p03` matches Node. `p/s1_kinds.pts` matches Node for every kind: array `values`/`entries`/`keys`, set `values`/`entries`/`[Symbol.iterator]`, map `entries`/`values`/`[Symbol.iterator]`, string, `Iterator<number>`- and `IterableIterator`-typed. In each case the iterator goes on afterwards. `p/s1_any.pts` (through `any`, `Float64Array.values()`, `yield*` forwarding) and `matchAll`'s iterator also match. Generators (before start, suspended, done, typed `Iterator<T>`), object iterators and a class's generator method are unchanged. |
| S2 | relaxed cast and contextual conditional skipped `FromResult` | **fixed** | `p11`, `p12`, `p14`, `routes/r_cond` match Node. `p/s2_routes.pts` also matches: relaxed casts of an asserted literal, a literal with `note`, a getter literal (1 read), own-shape yield and return results, conditionals (both orders, nested, result vs result), an arrow returning a conditional, and an assignment of a conditional. |
| S3 | postfix `r.value++` read the getter twice | **fixed** (but see R2) | `p08`, `p07` match Node. `p/s3_ops.pts` matches Node for 38 forms: `++`/`--` both ways, every arithmetic, bitwise and shift compound operator, `\|\|=`/`&&=` on truthy and falsy values, `??=`, element access `r["value"]++`, statement position, `done \|\|=`/`&&=`, own-shape receivers, a narrowed union receiver and an unnarrowed `IteratorResult<number, number>` receiver. |
| N1 | `fromValue` kept objects only for exact result types | **fixed** (but see R1) | `p02` and `p15` match Node. `p/n1_kinds.pts`: class instances, unrelated instances, `Error`s and frozen objects are now kept as JS keeps them (the base copied them or threw). A frozen object kept as a result throws on a write, as Node does. Arrays, tuples, primitives, `null` where only `undefined` is allowed, functions, Maps and Dates still fail the conversion, as on the base. JSON objects are still copied (identity `false`, as on the base). Writes through `any` into result fields, arrays and Map values keep the object. |
| N2 | identity/writes lost through an interface copy | **declared and pinned** (`run/iterator_results_copied.pts`, D-178) | `p04` unchanged; D-178 now spells out the consequences |
| N3 | a `yield*` statement skipped the done result's `value` | **fixed** | `p01b` and `p01` match Node. `p/n3_yield_star.pts` and `p/n3b_places.pts` also match Node: two `yield*` in one expression, a call argument, loops, `try/finally` with `return()`/`throw()` forwarding, discarded `yield*` in `if`, comma and conditional, nested generators, arrays/strings/sets, `catch`/`finally` blocks, `switch`, labeled blocks, templates, and `yield (yield* …)`. Async generators never reach `delegation` (PTS1001 on both trees, `p/n3_async.pts`). |
| N4 | non-boolean `done` through a union | docs (D-178 now states both behaviours) | `p20` unchanged |
| N5 | stale docs (C47, bench) | C47 fixed in the docs; the **bench numbers are still stale**: NOTE 1 | `g3`, `adj_p7_misc` match Node |
| N6 | no pins of the C51/N2 gaps | **fixed** (`iterator_results_copied.pts`, `no-diff` with Node's output in its comment) | |

All 48 round-2 probes were rerun (`out2/`). 43 match Node. The other five are the declared `g1`, `g2`, `g4` (PTS1001),
`p04` (N2) and `p20` (N4), and their new-tree outputs equal round 2's. All 55 round-1 probes were rerun too (`out1/`). Every
change from round 2 is a path in a message, so the set of differences from Node is round 2's: declared gaps and
pre-existing issues.

---

## R1. SHOULD-FIX (regression from base): the `result` flag wins over a union's other object member

- **Repro:** `p/n1_union_min.pts` (members in the order the type is written) and `p/n1_union_min2.pts` (the other order);
  `p/n1_union_anyfields.pts` is a realistic variant, where the object's fields come from parsed JSON (`{ x: data.x, y: data.y }`).
  ```ts
  interface Point { x: number; y: number }
  const loose: { x: number | string; y: number } = { x: 3, y: 4 };
  const raw: any = loose;
  const u: IteratorResult<number> | Point = raw;
  if ("x" in u) console.log("point", u.x, u.y);
  ```
- **Node:** `point 3 4`. **Base:** `point 3 4`. **Round-2 compiler on today's runtime:** `point 3 4`.
- **New:** `Uncaught TypeError: Cannot read property x of Object as number: it is declared number | string`. The anyfields
  variant says `… it is declared value`. Both member orders fail, because cgen puts the result shapes first in the
  union's check.
- **Cause:** `convert` (`pts/runtime/src/convert.c:476`) returns `true` for any `PTS_KIND_OBJECT` when `check->result` is
  set, even in the union's first, no-copy pass (`:454-466`). The result member therefore takes every program object
  before the copy pass can copy it into `Point`. Reads through `Point`'s sites then meet a field of another kind
  (`wrong_kind`). An object that fits `Point` as it is was unaffected, since it is the same pointer either way. That
  covers instances narrowed by `instanceof` and literals that fit, which is why `p/n1_unions_rest.pts` matches Node.
  JSON (hash-map objects) is unaffected too.
- **Fix (validated in the scratch tree):** keep an object as a result only after the union's other members have failed,
  both as it is and by a copy. My experiment adds a `results_last` flag to `converting`, which is set for the member
  calls of the first two passes and cleared at the top of `convert`, so nested conversions still keep results. A third
  pass then runs the result members. An alternative is to emit the flag only where cgen's `isResultType` holds, so that
  the runtime's rule matches `fromValue`'s.
- **Pin:** add `n1_union_min` (or the anyfields variant) to `run/iterator_results_from_any.pts`.

## R2. SHOULD-FIX (regression from round 2): a compound assignment with a yield on its right reads the getter twice

- **Repro:** `p/s3_yield_min.pts`
  ```ts
  let reads = 0; let v = 5;
  const r = { done: false, get value() { reads++; return v; }, set value(x: number) { v = x; } } as IteratorYieldResult<number>;
  function* g(): Generator<number, void, number> { r.value += yield 1; }
  const it = g(); it.next(); it.next(10);
  console.log(v, "reads", reads);
  ```
- **Node:** `15 reads 1`. **Round-2 compiler:** `15 reads 1`. **New:** `15 reads 2`. **Base:** PTS1001 (the `as`).
- `p/s3_yield.pts` and `p/s3_yield_logical.pts` show the same for `+=`, `*=` and `-=` in generators. The trace is
  `get,resume1,get,set`, where Node gives `get,resume1,set`.
- **Cause:** S3 makes `readAndStore` (`pts/lower/lower.go:2978-2980`) bind `current = <read>` for result receivers. Then
  `compoundAssignment` (`:2826-2828`) replaces the read with D-145's `keptCurrent` value, which was read before the yield,
  but the `Bind` around the expression still runs the read after the resume, unused. You can see it in the emitted C:
  `pts_value current_2 = …pts_result_get(…)` while the sum uses `pts_frame->current`. Interface receivers already behave
  this way: the probe's `HV` part shows `get,resume,get,set` on round 2 as well.
- **Fix:** when `keptCurrent[node]` exists, bind `current` to the kept value (or skip the read). This also fixes the
  interface case.
- **Pin:** add the repro's generator to `run/iterator_results_other_types.pts`'s update section.
- Related, already ledgered: `r.value ||= yield 1` yields before reading (`resume1,get,set` in both rounds), and in
  general `x ||= yield 1` yields even when it short-circuits (`p/d145_logical_yield.pts`, base too). That is
  CONSOLIDATED C25 (open, H08), not this slice.

## R3. SHOULD-FIX (in the slice since round 1; base PTS1001): result checks imply interfaces by their declared types

- **Repro:** `p/i11d_typed.pts`, which has no assertion except the `as any` it converts through:
  ```ts
  interface Wrapper { r?: { value: unknown } }
  const it: Iterator<number, number> = { next(): IteratorResult<number, number> { const own = { done: false, value: 3.5, extra: "x" }; return own; } };
  class Holder { r: IteratorResult<number, number> = it.next(); }
  const w: Wrapper = new Holder() as any;
  console.log("typed", w.r?.value);
  ```
  Also `p/i11b_optional.pts` (an optional property) and `p/i11c_union_prop.pts` (a required `{ value: unknown } | undefined`
  property, through a field and through a getter).
- **Node:** `typed 3.5` (`value 2.5`; `field 1.5`, `getter 2.5`). **New and round-2 compiler:**
  `Uncaught TypeError: Cannot read property value of Object as value: it is declared number`. **Base:** PTS1001 (the
  literal can't become a result there).
- **Cause:** converting `Holder` to `Wrapper` asks `object_fits`, which reaches `place_implies` and then `implies`
  (`convert.c:213`) with the field's declared check (the result union) and the property's check. `properties_imply` holds
  for the result shapes, so `Holder` is kept as a `Wrapper`. The field's actual object, however, is of another layout, a
  thing D-178 allows only for result types. `w.r` then gives that literal as `{ value: unknown }`, whose site meets a
  `number` field where it expects a `pts_value`. When the property is required and of object type (`p/i11_nested_implies.pts`),
  the slots differ and the holder is copied, so that case works.
- **Fix (validated):** in `implies`, a result check implies no non-result object check:
  `if (sub->kind == PTS_CHECK_OBJECT && sub->result && super->kind == PTS_CHECK_OBJECT && !super->result) return false;`.
  The holder is then copied (D-071), and its field is converted with the interface's check. `pts_optional_get_slow`
  (`object.c:266/273`) uses the same implication but is reached only through static casts, which the compiler already
  rejects for result-typed fields (`providesProperty` wants the same type). If the parent would rather defer this, D-178's
  "every route" sentence should then say "every static route", and the ledger should get a line.

---

## NOTEs

1. **Bench numbers in the docs are stale** (callgrind, `PTS_THREADS=1`, final tree, `bench/`):

   | Program | Base | Round 2 | Final | Ratio | vs round 2 |
   |---|---|---|---|---|---|
   | `b4_user_next` | 1,341,618,634 | 760,227,095 | 733,469,469 | 0.547 | −26.8M (S1's `always_inline`) |
   | `b4d_user_next_donefirst` | 1,341,606,538 | 731,228,166 | 704,469,007 | 0.525 | −26.8M |
   | `b1_next_union` | 489,956,963 | 417,969,392 | 415,964,994 | 0.849 | −2.0M |
   | `b2_next_shape` | 247,951,440 | 277,346,026 | 275,951,376 | 1.113 | −1.4M |
   | `b3_user_forof` | 899,970,156 | 943,973,078 | 929,976,691 | 1.033 | −14.0M (N5's inline, as predicted) |
   | `b3d_user_forof_donefirst` | 866,965,599 | 910,972,984 | 896,972,071 | 1.035 | −14.0M |
   | `b5_delegate` | 247,799,450 | 255,799,349 | 257,803,892 | 1.040 | +2.0M (NOTE 3) |
   | `b6_forof_gen` | 133,788,732 | 137,788,707 | 137,788,783 | 1.030 | 0 |
   | `r1_union_reads` | 599,493,819 | 429,417,924 | 429,415,860 | 0.716 | 0 |
   | `r2_shape_reads` | 107,257,863 | 129,273,546 | 129,276,018 | 1.205 | 0 |
   | `r3_user_shape` | 180,178,071 | 182,201,718 | 182,201,981 | 1.011 | 0 |
   | `r4_donefirst_reads` | 107,127,156 | 137,142,146 | 137,142,162 | 1.280 | 0 |
   | `i1_own_incr` (new: 40M read-modify-writes of an own-shape result) | 1,780,795,818 | 2,160,791,600 | 2,120,796,171 | 1.191 | −40.0M (S3's bound read helps) |

   - `docs/agent/hardening-evidence/h03b.md`'s table is round 2's tree: b4 0.567, b2 1.121, b5 1.032, b3d 1.060.
   - D-178 says "1.06× done first" and "`yield*` 1.03×". The final numbers are 1.035 and 1.040.
   - The parent's own 03:10 log for b3d (918,972,179) doesn't reproduce. Its binary
     (`/tmp/pts-h03b-s2/bench/b3d_user_forof_donefirst.new`) counts 896,972,503 under my run, like my two builds
     (896,972,071 and 896,974,898). That run was probably perturbed by GC helper spinning under host load.
   - D-178 could add that read-modify-writes of own-shape results cost about what reads do: `i1` at 1.19×, about 8
     instructions per operation for the descriptor tests on read and store.
2. **S1's message text is the callee's raw source text** (cosmetic; the same mechanism as object iterators' message in
   round 2). In `p/s1_text.pts`, `(av).return!()` gives `(av).return is not a function`, and a call split over two lines
   keeps its newline and indentation. Node prints `av.return` in both cases. Stripping parentheses and whitespace from
   `text` (or using the property access's expression text plus `.name`) would match.
3. **N3's frame slot.** Every `yield*` adds a `pts_value delegated` frame member, and its value stays reachable until the
   frame dies. In `b5`, clang now saves `r14` in `pts_step_fn_outer` (a push and a pop), which is +2 instructions per
   delegated step: 34.0M becomes 36.0M, and b5 goes from 1.032 to 1.040 against the base. It is harmless; NOTE 1's table
   includes it.
4. **Pre-existing, outside H03b, apparently unledgered:** `a ?? b` / `a || b` with an interface-typed `a` and a class
   instance `b`, where a third interface goes, casts `a` to `b`'s class. `p/x_nullish_iface.pts`: Node `nullish 2.5`;
   new **and base** `Uncaught TypeError: The value narrowed to Other is not an instance of it`. The emitted C is
   `pts_value_as_instance(value_2, &pts_type_Other)` for the left operand. It hits results too
   (`p/s2_nullish.pts`, `p/s2_or.pts`, `p/s2_nullish_own.pts`; the same on base), but the cause isn't result-specific
   (`p/s2_nullish_ctl.pts`). It is worth a ledger line.
5. **Pre-existing conditional limitation (not results):** `flag ? t : { value: 7 }` or `flag ? t : new Other(5)` where an
   interface with an optional property goes is PTS1001 for plain classes too (`p/s2_ctl.pts`). It is an honest
   diagnostic.

## Verified good (new evidence this round)

- **S1:** every runtime iterator kind, generators and object iterators (see the table). `pts_throw_new` is `_Noreturn`,
  so the uninitialized `done` in `pts_iterator_call`'s third branch can't be read.
- **S2:** besides the routes in the table, equality (`commonClass` in `operation`) stays a plain pointer cast, which is
  right for identity. `interfaceCast`'s relaxed path still records its `relaxedCast`, and `checkRelaxedCasts` sees the
  Result class as the source. An own-shape result passes as it is, and `note` reads `undefined`.
- **S3:** all 38 operator forms match Node (`p/s3_ops.pts`). For an own-shape receiver the bound read is a C local inside
  the statement expression, not a frame member, and it is 40M instructions cheaper than round 2 (`i1`).
- **N1:** `pts_check` gained a trailing `bool`, and every runtime initializer is designated, so the field defaults to
  `false`. `pts_check_same` is pointer identity, so the flag doesn't affect it. `pts_storable` (writes through `any`, D-072)
  keeps objects for result fields, array elements and Map values (`n1_kinds`), and fails for arrays, tuples and numbers as
  the base does. The inline path of `fromValue` for `IteratorResult<T> | undefined/null` agrees with `convert`.
- **N3:** the post-loop read never runs on the `return()` path (`leaveFunction` leaves first). D-145 sees the new local as a
  plain local of the function and keeps no second copy. `return yield* …` inside `try/finally` and two `yield*` in one
  expression keep their values across later yields (`n3_yield_star`, `n3b_places`).
- **Threads (D-143):** `run/iterator_results_threads.pts`, built from this tree, passed 10 of 10 runs (5 at
  `PTS_THREADS=4` and 5 at 8). Round-1 `e01`-`e03` give `bad 0` at 4 and 8.
- **Tests:** focused `^(TestRun|TestRunMatchesNode)$/(iterator|yield_star|array_pattern|any_maps|generator|for_of)`: **105
  pass, 7 skip, 0 fail** (`gotest-focus.jsonl`). The 7 skips are expected: `any_maps` has no expect block, and the rest
  are `no-diff`/`parallel` or older known skips. `go test ./pts/lower/ ./pts/cgen/` ok; `TestErrors` ok.

## Test quality

- The new and extended files pin the round-2 defects well. They cover runtime iterators' `return!`/`throw!` (array, map
  keys, string), a `yield*` statement's value read, the relaxed and contextual routes with the 1e-310 literal, the
  postfix, prefix, compound and narrowed updates, a union with `undefined`, arrays, an `any` array, a Map write through
  `any`, and `Promise.all`.
- `iterator_results_copied.pts` is a fair `no-diff` pin: Node's output is in its comment, and the test exercises both
  copies (C51 and the interface's).
- Gaps: R1 (a union with a non-result object member, through `any`), R2 (a compound assignment with a yield on its right
  side) and R3 (a result-typed field read through an interface after `as any`) aren't covered. Sets, entries iterators and
  typed arrays for S1 are covered only by my probes, which is acceptable since the runtime test is by type.

## Commands and files

- `run.sh X.pts` prints Node, new and base outputs (`NOBASE=1` skips the base). `batch.sh` runs probes 2 at a time.
- Probes: `p/` (this round), `r2p/` (round 2's, including `routes/`, `gaps/`, `repro/`), `r1p/` (round 1's). Outputs:
  `out/` (this round), `out2/` (round-2 probes), `out1/` (round-1 probes), `out-exp/` (the scratch-runtime experiment).
- Bench: `bench/` (programs, binaries, `cg.*`; `i1_own_incr.{base,r2,new}`; `o.r2.s`/`o.new.s`, the disassembly of b5's
  step function).
- Experiment: `tree/` (a copy of `pts/runtime`, `pts/third_party` and `pts/lib`, run with `PTS_HOME=/tmp/pts-h03b-review3/tree`).
  Its patch is `experiment-convert.diff`. Nothing in the repository was edited.

---

## Round 4: H03b, check of the round-3 fixes (R1–R3) and a hunt over the whole slice

Reviewer: opus-reviewer (claude-opus-5-5, xhigh), read-only, 2026-10-16. Tree: the parent's uncommitted working tree on base `a65a75d62`, including the R1–R3 changes (`convert.c` and `lower.go`, 03:28) and their pins (03:48). Tree CLI: `/tmp/pts-h03b-review4b/pts`, run with `PTS_HOME=/home/bryan/projects/parallelscript`. Base CLI: `/tmp/pts-h03b-review4b/pts-base`, built from a detached worktree of `a65a75d62` that I have since removed. Node v24.21.0. Nothing in the repository was edited (`git status` shows no file of mine; the sanitizer helper's C files were moved into my scratch directory).

## Verdict: NOT READY (no blocker; two small new SHOULD-FIX defects, plus stale suite evidence)

R1 and R3 are fixed, and my variants of both match Node. R2 is only partly fixed. I found no memory-safety defect: 16 probes and all 18 slice tests ran under ASan/UBSan with no ASan report. The only UBSan reports are three pre-existing ones that need no iterator results (NOTE 7). The defects I did find:

- **Finding 1 (R2 residual):** a compound assignment with a yield on its right still runs the getter twice when the result's value type is a union. Element access does the same, though for element access the base already behaves this way on interfaces.
- **Finding 2:** by-name reads don't find class methods, so a valid program throws a TypeError where Node returns the method.
- **Finding 3 (evidence):** the full-suite receipt in the evidence is from the tree before R1–R3.

## Ranked findings

1. **SHOULD-FIX (new)**: R2 residual. `r.value += yield v` still reads the getter twice when `T` is a union (`IteratorYieldResult<number | string>`). `r["value"] += yield v` also reads it twice, as interface receivers do on the base. D-178 says the property is read once.
2. **SHOULD-FIX (new, low impact)**: by-name reads skip class methods. Take a class instance whose `value` is a method, held as `IteratorYieldResult<() => number>`. Reading `.value` throws `TypeError: Expected () => number, got undefined`. Node returns the method, and PTS also finds it through `any`.
3. **SHOULD-FIX (evidence, new in this slice's docs)**: h03b.md's GREEN full-suite counts and `h03b-suite.json` come from the post-round-2 tree. Their events run 03:03:40–03:10:29, before the R1–R3 code (03:28) and the pins (03:48). No receipt of the final tree is in the evidence.
4. **NOTE (evidence)**: the benchmark row `b3d` (918,972,179, 1.06×) and D-178's "1.06× done first" don't reproduce. Round 3 measured 1.035, and I measure 1.040 for both b3 and b3d.
5. **NOTE (evidence)**: smaller accuracy and format issues in h03b.md:
   - The doc says "18 records", but `h03b-red.json` has 22 records of 18 tests.
   - The C42 RED row describes an earlier version of the file.
   - Table rows 41–42 lack the list indentation.
   - The tarball and `/tmp` paths it cites are gone.
6. **NOTE (new reachability, memory-safe, pre-existing class)**: TS's unsound `in` narrowing over a union of a result and an object type. An asserted result reaches the other member's reads and gets the kind check's TypeError, where Node prints values. The base throws the same TypeError for interfaces.
7. **NOTE (pre-existing, unledgered)**: three UBSan reports in ordinary programs, none involving results:
   - `convert_tuple` computes `&NULL->fields[i]`.
   - `pts_call_getter` and `pts_call_setter` call accessor entries through function pointers of another type.
8. **NOTE (code quality)**:
   - `isIteratorResultType` and `isIteratorResultUnion` are dead code.
   - Comments, D-178 and SPEC say a by-name read gives "a field, a getter, or undefined" (finding 2).
   - `readKeptAndStore`'s doc doesn't match its fallback (finding 1).
   - `interfaceCast` silently drops `FromResult` if `prepareDynamicTarget` fails (I couldn't reach that case).
   - `PROGRESS.md` is stale.
9. **NOTE (pre-existing or limitation, not in D-178's gaps)**: two forms are honest PTS1001s but print internal names:
   - narrowing a result with `instanceof` to its class (`converting object1 | object2 to Entry`);
   - a conditional between a class instance and a result literal where a result goes (PTS1001 on the base too).

---

## 1. SHOULD-FIX (new): R2 residual, union-typed values and element access

- **Repro (union value, new):** `/tmp/pts-h03b-review4b/p/r2_min_union.pts`
  ```ts
  let reads = 0;
  let v: number | string = 5;
  const r = {
    done: false,
    get value(): number | string { reads++; return v; },
    set value(x: number | string) { v = x; },
  } as IteratorYieldResult<number | string>;
  function* g(): Generator<number, void, string> {
    r.value += yield 1;
  }
  const it = g();
  it.next();
  it.next("x");
  console.log(v, "reads", reads);
  ```
- **Command:** `/tmp/pts-h03b-review4b/run.sh /tmp/pts-h03b-review4b/p/r2_min_union.pts` (output: `out/r2_min_union.txt`)
- **Node:** `5x reads 1`. **PTS:** `5x reads 2`. **Base:** PTS1001 `using a object2 as a object1` (honest).
- **Repro (element access):** `/tmp/pts-h03b-review4b/p/r2_min_element.pts` (`r["value"] += yield 1` on an `IteratorYieldResult<number>` getter literal): Node `15 reads 1`, PTS `15 reads 2`, base PTS1001.
  - Control `p/r2_element_iface_ctl.pts`: the same with an interface receiver gives `reads 2` on both base and tree. So the element-access half is a pre-existing D-145 gap, newly reachable for results.
  - Control `p/r2_union_iface_ctl.pts`: a union-typed interface property reads once on the tree and twice on the base. R2 fixed that case for interfaces, but not for results.
- **Variants:** `p/r2_variants.pts` matches Node for an unnarrowed union receiver, a call around the yield, `**=`, `<<=`, and a value also read on the right side. It reads twice for `union value`, `union string` and `element` (`get,yielded,get,set` against Node's `get,yielded,set`).
- **Cause:** `readKeptAndStore` (`pts/lower/lower.go:2978-2997`) uses the kept read only if `kept.Type() == t` or the place's union `t` has `kept.Type()` as a member. Otherwise it silently falls back to `target.read()`, which runs again after the resume and whose value is unused. Two cases miss:
  - A D-109 shape's field is `T | undefined`. For a union `T`, the kept read's type is `T` itself, which is neither equal to the field's type nor a member of it.
  - `r["value"]` on a result is a dynamic place (`t` is Value). The emitted C (`out/r2_min_element.c`) binds `current_2 = pts_dynamic_get(object, key)` after the resume and then uses `pts_frame->current`.

  The union case's C (`out/r2_min_union.c:328`) shows the second `pts_result_get` the same way.
- **Contradicts** D-178: "a read-modify-write … reads the property once, also when its right side yields". It is also H03b's acceptance theme (getters run when and as the caller reads them).
- **Fix suggestion:** when `kept != nil`, always bind `read` to `kept` converted to the place's type: `l.convert(kept, t, …)` handles a union into a wider union, and `box` handles a dynamic place. Pin with the two repros.

## 2. SHOULD-FIX (new, low impact): by-name reads don't find class methods

- **Repro:** `/tmp/pts-h03b-review4b/p/s6b_method_any_ctl.pts`
  ```ts
  class E {
    done = false as const;
    value() { return 42; }
  }
  const a: any = new E();
  console.log("any", typeof a.value, a.value());
  const viaAny = a.value;
  console.log("any value read", typeof viaAny);
  const one: IteratorYieldResult<() => number> = new E();
  const f = one.value;
  console.log("one", typeof f);
  ```
- **Command:** `/tmp/pts-h03b-review4b/run.sh /tmp/pts-h03b-review4b/p/s6b_method_any_ctl.pts` (output: `out/s6b_method_any_ctl.txt`)
- **Node:** `any function 42` / `any value read function` / `one function`.
- **PTS:** the two `any` lines match, then `Uncaught TypeError: Expected () => number, got undefined` (line 11).
- **Base:** PTS1001 `using a E as a object1`.
- The union form fails the same way (`p/s6_value_method.pts`: `IteratorResult<() => number, undefined>` from a class instance), with the same TypeError at line 12. An object literal's own method works (`call 41`).
- **Cause:** `pts_result_get_slow` (`pts/runtime/src/object.c:326-345`) finds a field or a getter. Otherwise it returns `PTS_VALUE_UNDEFINED` ("an object without it"), so a prototype method is never found. The runtime's other by-name reads of results do find it, because they use `pts_dynamic_get`: `handed_value` and `result_done` in `generator.c`, and reads through `any`.
- **Fix suggestion:** fall back to `pts_dynamic_get` (or a method lookup) instead of returning undefined. Fix the wording of the `pts.h` comment, D-178 and SPEC ("a field, a getter, or undefined if there is none") to match.

## 3. SHOULD-FIX (evidence): the GREEN suite receipt predates R1–R3

- `docs/agent/hardening-evidence/h03b-suite.jsonl.gz` has event times from `2026-10-16T03:03:40` to `03:10:29`, and `h03b-suite.json` says "uncommitted H03b working tree after review round 2". The R1–R3 changes and pins came later:
  ```
  2026-10-16 03:28:30 pts/runtime/src/convert.c
  2026-10-16 03:28:53 pts/lower/lower.go
  2026-10-16 03:48:44 pts/tests/run/iterator_results_from_any.pts
  2026-10-16 03:48:44 pts/tests/run/iterator_results_other_types.pts
  ```
- h03b.md's session-3 narrative says the full suite ran again, but GREEN still cites the earlier 1,381/256/0 run and its receipt. The counts may come out identical, since the pins extend existing files. Record the final tree's run and its CLI sha.
- My own runs on the final tree are below ("Verified good"). They are focused, not a full suite.

## 4. NOTE (evidence): b3d's 1.06× doesn't reproduce

h03b.md's table shows `b3d_user_forof_donefirst` at 866,965,482 → 918,972,179 (1.06), and D-178 says "(1.06× done first)". Round 3 already measured 896,972,071 (1.035) for the parent's own binary. My programs (`bench/b3_user_forof.pts` and `bench/b3d_user_forof_donefirst.pts`; the originals are lost) give identical counts for both orders, 742.94M → 772.94M (**1.040**). The doc says the table was re-measured, but this row and D-178's sentence still carry the number that doesn't reproduce. The direction holds (a small slowdown).

## 5. NOTE (evidence): smaller accuracy and format issues in h03b.md

- "[h03b-red.json] (18 records, from both sessions)": `records` holds **22** records of 18 distinct tests (`python3 … len(d['records'])` → 22; `yield_star_result_edges` ×3, `iterator_results_other_types` ×2, `iterator_results_from_any` ×2).
- The RED row for `run/iterator_results_from_any.pts` (C42) shows the earlier version's failure (`next done read` … `Error: value of a done result was read`). The final file fails on the base with PTS1001 at 138:12 (``using a `{done: boolean; extra: string; value: number}` as a `object2` ``; my base run, `tests/base/iterator_results_from_any.err`). The `yield_star_result_edges` row notes this kind of change; this row doesn't.
- The RED table sits inside list item 3, and its last two rows (lines 41–42, the `iterator_results_other_types` round-3 tail and `any_maps_error`) lack the three-space indent, so they will render outside the table.
- `h03b-review-probes.tar.gz` (cited in h03b.md lines 48, 74 and 100, and in h03b-review.md line 3) and D-178's `/tmp/pts-h03b-s2/bench` no longer exist. `REVIEW4_PLACEHOLDER` and `COMMIT_PLACEHOLDER` remain.

## 6. NOTE (new reachability; safe; pre-existing behaviour class): `in` narrowing across a result and another object member

- **Repro:** `p/m01b_in_shape_kinds.pts`. In `show(u: IteratorResult<number> | { x: number; y: number })`, the branch `if ("y" in u)` reads `u.x`, then `u.y`. The argument is `{ done: false, value: 1, x: 2.5, y: "s" } as IteratorYieldResult<number>`.
  - Node: `x 2.5`, `y s string`, …
  - PTS: `x 2.5`, then `Uncaught TypeError: Cannot read property y of Object as number: it is declared string`.
  - Base: PTS1001 at the `as`.
- `p/m01_in_shape.pts` and `p/m02_in_iface.pts` (no `x`): PTS gives `Cannot read property 'x' of #<Object>: it has none, though a type assertion says it has`, where Node prints `pt undefined s`.
- **Control without results** (`p/m01d_in_iface_base.pts`, `A | B` interfaces, an object with `y: string` passed as `A`): base and tree both throw the same TypeError, where Node prints `b 2.5 s`. So this is TS's unsound `in` narrowing hitting D-072's kind check, now reachable for results too.
- **Memory safety:** the narrowed read is a checked site. In `out/m08.c`, the narrowed `u.y` read emits `pts_field_address(…, &pts_site1, "y", &pts_check_4)`, so no offset read of a foreign layout occurs. `p/m04_guard.pts` (a user type guard) behaves the same way (`Expected number, got undefined`).

## 7. NOTE (pre-existing, unledgered): UBSan reports in ordinary programs

I built probes with the toolchain's `Sanitize` flags (`-fsanitize=address,undefined -fno-sanitize-recover=undefined`). The helper is `/tmp/pts-h03b-review4b/sanbuild/sanbuild`, which mirrors `pts/tests/http_test.go:buildSanitized`. Three UBSan reports abort programs that use no iterator results:

- `p/u1_entries_ub.pts` (`Object.entries({a: 1, b: 2})`): `convert.c:347:31: runtime error: applying zero offset to null pointer` in `convert_tuple`. `&object->type->fields[i]` is computed for an array source, whose `fields` is NULL. The same line is at base `convert.c:338`.
- `p/u2_getter_fn_type.pts` (a class getter returning a class, copied from `any`): `dynamic.c:242:55: call to function pts_entry_H_get_p through pointer to incorrect function type 'void *(*)(struct pts_object *)'`.
- `p/u3_setter_fn_type.pts` (a string setter written through `any`): `dynamic.c:568:26: call to function … through pointer to incorrect function type 'void (*)(struct pts_object *, void *)'`.

All three are benign on x86-64 SysV, and the `convert.c` and `dynamic.c` code involved is unchanged by the slice. They make 3 of the 18 slice tests stop under UBSan (`iterator_results_from_any`, `iterators_through_any_consumers`: the `convert_tuple` report; `iterator_result_getter_literals`: the setter report). I found nothing in CONSOLIDATED, PROGRESS or DECISIONS about them (`grep "zero offset\|convert_tuple"` found nothing).

## 8. NOTE (code quality)

- **Dead code:** `isIteratorResultType` and `isIteratorResultUnion` (`pts/lower/union_objects.go:128-150`). Their only caller was the removed `reportedGetterResult` (`grep -rn` finds no other use).
- **`readKeptAndStore` (`lower.go:2970-2977`):** the doc says the kept read is used so that the place "isn't read again after the yield". On a type mismatch it silently reads again (finding 1).
- **Wording about by-name reads:** the `pts.h` comment of `pts_result_get` and D-178's "a field, a getter, or undefined if there is none" skip methods (finding 2).
- **`interfaceCast` (`objects.go:916`):** if `prepareDynamicTarget` fails, it silently emits the plain cast that leads to the "compiler bug" kind error. I tried 16 interface property types (`void`, `MapIterator<string>`, `Promise<number>`, `Float64Array`, `Iterator<number>`, `never`, `{}`, `Record<…>`, …). All got the conversion, and `bigint`/`symbol`/`WeakMap` are rejected earlier, so I couldn't reach the case. A PTS1001 in that branch would make the invariant explicit.
- **Docs:**
  - D-177 isn't marked as partly superseded by D-178.
  - `PROGRESS.md` lines 9, 211 and 234 still list C28–C35 as open and getter-literal results as PTS1001. Presumably that changes with the commit.

## 9. NOTE (limitations, honest PTS1001 with internal names, not in D-178's gaps)

- `p/m05_instanceof.pts`: `if (r instanceof Entry) r.extra` on a result gives `PTS1001: converting object1 | object2 to Entry is not supported yet`. Node prints `e entry 4`. The bare `r instanceof Entry` works (`p/m05b_instanceof_bool.pts` matches Node).
- `p/t2b_cond.pts`: `i === 0 ? new Entry(i) : { done: false, value: i }` where a result goes gives `converting object1 to Entry | {done: boolean; value: number}`. The base fails the same way, so this belongs with C57/H29.

---

## Round-3 findings: status

| # | Round 3 | Now | Evidence |
|---|---|---|---|
| R1 | union of a result and another object member kept the object as the result | **fixed** | Pin RED on a scratch runtime with round-3 `convert.c` (`r3tree/`: results_last and the `implies` line removed). Same failure as the parent's record: line 39 `point 3 4` missing, `TypeError: Cannot read property x of Object as number: it is declared number \| string` at :124. Green on the final tree. My variants `p/r1_variants.pts` all match Node: arrays, an `any` array into `Array<Point \| IteratorResult<number>>`, tuples, a Map write through `any`, an optional property, `IteratorYieldResult \| IteratorReturnResult \| Point`, a nested field union, a union member holding a result, and a getter result kept lazily (`got gr 0`, `gr false 1`; base `2`, `2`). The same file fails at its first case on the round-3 runtime. A `Record<string, number>` member (`p/r1_record.pts`) prints the same as the base (D-071's copy, pre-existing). |
| R2 | `r.value += yield v` read the getter twice | **partly fixed** | Pin RED with a CLI built with only the R2 hunk reverted (`go build -overlay`, `pts-r3lower`). Line 27 reads `get,yielded,get,set 14,resumed,get,yielded,get,set 42`, exactly the parent's record. Green on the final tree. Union-typed values and element access still read twice: finding 1. |
| R3 | result checks implied interfaces by declared types | **fixed** | Twin RED on the round-3 runtime: line 39 `wrapped 3.5` missing, `Cannot read property value of Object as value: it is declared number`. Green on the final tree. My variants `p/r3_variants.pts` all match Node: an array field, a nested object, `\| undefined`, the same result type kept as is (identity `true true`), a wider result type, and an object-typed getter. On the round-3 runtime they fail at `deep`. Static routes stay honest PTS1001, as on the base (`p/r3_static_cast.pts`). |

## Verified good (evidence)

- **The 18 regression files plus the 3 modified ones** (`runtests.sh`): PTS stdout equals each `// expect:` block for all 21. The threads file matches at 1, 2 and 4 threads, plus six more runs at 4 and 8. Node's stdout equals the expect block for the 18 files without `no-diff` or `parallel`. `iterator_results_copied`'s comment ("JS prints `row 10 true …` and `literal true 99`") matches Node.
- **Focused `go test`:** `^(TestRun|TestRunMatchesNode)$/^(<21 files>)$`, `PTS_REQUIRE_NODE=1`: exit 0, 44 pass / 3 skip / 0 fail (`tests/gotest-focus.jsonl`).
- **Broader `go test`:** `^(TestRun|TestRunMatchesNode|TestErrors)$/^(any|dynamic|union|json|convert|interface|generator|iterator|yield|for_of|async_gen|array_pattern|destructur|shape|optional|relaxed|record)`: exit 0, 175 pass / 49 skip / 0 fail.
- **Other checks:**
  - `go test ./pts/lower/ ./pts/cgen/`: ok.
  - `go vet` on lower, cgen and ir: exit 0.
  - `TestRuntimeUnitTestsPassUnderSanitizers`: ok.
- **RED on the base** (`runbase.sh`, my base CLI): the table's first failures reproduce for these rows:
  - `iterator_methods_through_any`: `next escaped: h.next is not a function`.
  - `iterator_next_result_identity`: `same object false`.
  - `iterator_next_result_getters`: line 3 `manual read value 0`.
  - `iterator_result_as_layouts`: `parameter escaped: Expected number, got function`, then `console.log got a malformed value 0x0`.
  - `iterator_result_as_cast`: `TypeError: Expected number, got function`.
  - `array_pattern_elision_done_only`: `class read value 0`.
  - `iterator_close_result_unread`: `P value 1`.
  - `iterators_through_any_consumers`: `TypeError: h.next is not a function`.
  - `iterator_results_threads`: ``using a `object6` as a `object1` ``.
  - `iterator_results_copied`: `converting Entry to object1 | object2`.
  - `any_maps_error`: `keys -> TypeError…` against the new `keys ok`.
  - The PTS1001 rows: `return_throw_results`, `as_one_shape`, `getters_typed`, `getter_literals`, `yield_star_result_edges`, `other_types`.
- **Suite arithmetic** (recounted from the two jsonl.gz files): baseline 1,345 pass / 260 skip, suite 1,381 / 256. The per-test deltas are exactly as h03b.md lists them: 31 new events (14 TestRun files, plus the threads file with its parent and 3 subtests, plus 13 MatchesNode), 6 skip→pass, `TestErrors/iterator_result_getters` removed, 2 new MatchesNode skips. That gives +36 / −4.
- **H01b probes:** rerun with my CLI (`probes-h01b/run.log`): "22 probes, 14 not behaving as their kind says". All 14 red probes now `run-match` and all 8 controls `ok`, as h03b.md says.
- **Memory safety under ASan/UBSan** (`sanrun.sh`, `san/`): no ASan report anywhere. These all run with no report and match Node or the expect block:
  - 15 slice tests, including both halves of `iterator_result_as_cast`, which SIGSEGV'd on the base;
  - the threads test at 1 and 4 threads;
  - the probes `s2c_consumers_noentries`, `r1_variants_notuple`, `r3_variants_nogetter`, `j01_counts`, `s3_async`, `s4b_keys` and `g1_gc_stress` (GC pressure while handed results wait, 2×200k steps).

  The other 3 slice tests stop only at the pre-existing UBSan reports of NOTE 7.
- **Results of foreign layouts through every consumer I tried** (`p/s2b_consumers.pts`, matches Node): `Object.keys`/`values`/`entries`, `JSON.stringify`, `for…in`, `in`, `hasOwnProperty`, templates, `map`/`sort`, `Set`, `Map.get(...)?.value`, `keyof` generics (`prop(lit, "value")`), closures, class fields, `Object.freeze`, and `for…of` over arrays of results. Spreads and destructuring stay the declared C44 PTS1001. Computed keys (`o[k]` read and write with `k: "done" | "value"`) match (`p/s4b_keys.pts`). `delete` is the existing PTS1002.
- **Getter counts and order** (`p/j01_counts.pts`, identical to Node on every line) through a `yield*` of an object iterator with getter results:
  - consumers: `for…of`, array patterns with elisions and close, spread, `Array.from`, `new Set`;
  - break with `return()`;
  - typed `next`/`return`/`throw`, `next`/`return` through `any`, and nested `yield*`.
- **Runtime iterators** (`p/k01_runtime_iters.pts`): typed `ai.return!()` and `return`/`throw` through `any` give Node's TypeErrors and the iterator goes on. Array, map, string and generator iterators through `any` print Node's results. The only difference is C46's `h.throw === undefined`.
- **Async** (`p/s3_async.pts`): `await ai.next()` on a typed `AsyncIterator` whose results have getters, `for await` over it and over a sync iterable of getter results, and async generators through `any`. All identical to Node.
- **Threads:**
  - `p/t1_par_reads.pts`: 400 `parallel for` tasks read 5 result layouts at one by-name site, `bad 0` at 1, 4 and 8×3 threads.
  - `p/t2_par_view.pts`: `.par.map` and `.par.reduce` over 3,000 mixed-layout results, correct at 1, 4 and 8×2 threads.
  - `convert`'s `results_last` lives in the per-call `converting` struct (one entry point, which initialises it), so it can't leak across threads or calls.
- **No panic or clang error** in any probe. Unusual call forms (`p/d01_call_forms.pts`) give the base's PTS1001s.

## Performance

Callgrind instruction counts, `PTS_THREADS=1`, my own programs (`bench/*.pts`; the parent's originals are lost), base binaries against tree binaries, same stdout. Summary in `bench/summary.txt`:

| Program | Base | Tree | Ratio | h03b.md |
|---|---|---|---|---|
| `b4_user_next` (typed `next()` over a class implementing `Iterator<number, undefined>`, 1M) | 365,913,719 | 279,914,032 | **0.765** | 0.547 (another program) |
| `b6_forof_gen` (`for…of` over a generator, 1M) | 133,768,249 | 137,768,064 | **1.030** | 1.03 |
| `r1_union_reads` (1M reads of stored `IteratorResult<number, void>`) | 282,228,346 | 196,237,115 | **0.695** | 0.716 |
| `b2_next_shape` (typed `next()` over `Generator<number, number>`, 1M) | 246,763,166 | 275,921,336 | **1.118** | 1.113 |
| `b3_user_forof` (`for…of` over a class's iterable, value first) | 742,938,441 | 772,940,989 | **1.040** | 1.033 |
| `b3d_user_forof_donefirst` (done first) | 742,943,382 | 772,941,292 | **1.040** | 1.06 (NOTE 4) |

The directions all reproduce. `b2`'s +29M is `pts_iterator_call` (64.0M of 275.9M in `callgrind_annotate`).

## Commands

- **Builds:**
  - Tree CLI: `cd /home/bryan/projects/parallelscript && GOMAXPROCS=2 go build -p 2 -o /tmp/pts-h03b-review4b/pts ./cmd/pts`.
  - Base CLI: from the worktree of `a65a75d62` (submodules from the repo's modules), `pts-base`.
  - CLI without R2: `go build -overlay overlay/overlay.json -o pts-r3lower ./cmd/pts`, with `overlay/lower.go` being the tree's file minus the kept-read lines.
  - Round-3 runtime: `r3tree/pts/runtime` (copy, `convert.c` without results_last and the `implies` line), with `third_party` and `lib` symlinked.
- `run.sh X.pts` prints Node (`node --no-warnings --experimental-transform-types X.mts`), tree (`PTS_HOME=<repo> pts run`) and base (`PTS_HOME=<base worktree> pts-base run`; `NOBASE=1` skips it). Outputs of the cited probes are in `out/<name>.txt`.
- `runtests.sh <names>` compares the tree's stdout and Node's with each test's expect block (`checkexpect.py`, the README's rules), with `PTS_THREADS` per `// threads:`. `runbase.sh` does the same on the base.
- `sanrun.sh X.pts [threads]` builds with ASan/UBSan (`sanbuild/sanbuild`, which mirrors `buildSanitized`) and runs with `ASAN_OPTIONS=detect_leaks=0:detect_stack_use_after_return=0`. Outputs are in `san/` and the emitted C in `san/c/`.
- **Focused tests:** `PATH=$HOME/.local/node/bin:$PATH GOMAXPROCS=2 PTS_REQUIRE_NODE=1 go test -count=1 -json -p 1 -parallel 2 ./pts/tests/ -run '<pattern>'`; events are in `tests/gotest-focus.jsonl` and `tests/gotest-broad.jsonl`.
- **H01b probes:** `python3 -I docs/agent/hardening-evidence/probes/h01b/run_probes.py --pts /tmp/pts-h03b-review4b/pts --node ~/.local/node/bin/node --scratch probes-h01b --out probes-h01b/after.json c02-* c03-* c18-* parent/c02-* parent/c18-*`.
- **Callgrind:** `PTS_THREADS=1 valgrind --tool=callgrind --callgrind-out-file=cg.<prog>.<base|new> ./<prog>.<base|new>` in `bench/`.
- The base worktree was removed with `git worktree remove --force /tmp/pts-h03b-review4b/base`.

---

## Round 5: H03b, confirmation of the round-4 fixes (readKeptAndStore, pts_result_get_slow, interfaceCast, docs)

Reviewer: opus-reviewer (claude-opus-5-5, xhigh), read-only, 2026-10-16. Tree: the parent's uncommitted working tree on `a65a75d62`. The last code mtime is `pts/lower/objects.go` at 20:58:48. Review CLI: `/tmp/pts-h03b-review5/pts`, built with `GOMAXPROCS=2 go build -p 2 -o /tmp/pts-h03b-review5/pts ./cmd/pts` and run with `PTS_HOME=/home/bryan/projects/parallelscript`. Base CLI: `/tmp/pts-h03b-s4/base/pts-bin/pts` (`a65a75d62`). Node v24.21.0. I changed nothing in the repository: `git status` shows only the parent's files. Scratch: `/tmp/pts-h03b-review5/` (`p/` probes, `out/` outputs, `run.sh` prints Node, then the tree, then the base).

## Verdict: NOT READY (one small SHOULD-FIX performance regression, new in change 2; everything else confirmed)

- Both round-4 defects are fixed, pinned, and match Node in every variant I tried.
- Change 1 (`readKeptAndStore` always uses the kept read) is safe. I tried 14 probe files covering every place kind, with `yield` and with `await`. None regressed against the base: no new PTS1001, no clang error, no panic, no new wrong output.
- The slice tests, the D-145 subset, `TestErrors` and the `pts/lower` and `pts/cgen` unit tests are all green.
- The suite receipt is now from the final code.

One problem remains. Change 2's fallback allocates a key string and does a full dynamic lookup on every slow-path read. A valid typed iterator whose results omit `done` now runs 2.7× the instructions of the base (and of the round-4 tree), with one GC allocation per read. The fix is cheap (below). If the parent prefers to keep the code, D-O15 makes benchmarks non-gating, so recording the cost as a known gap in D-178 would also close it.

## Ranked findings

1. **SHOULD-FIX (regression, performance, new in change 2):** `pts_result_get_slow`'s fallback calls `pts_string_from_cstr(name)` and then `pts_dynamic_get` on every read of a property that an object of another layout lacks.
   - Example: `r.done` on a hand-written `{ value: x }` result. This is valid TS, because `IteratorYieldResult.done` is optional. The type is `IteratorResult<number, undefined>`.
   - Cost: 2M reads take 1,212,981,392 instructions, against 445,182,914 on the base (2.72×) and 455,177,655 with the round-4 fallback emulated (2.67×).
2. **NOTE (pre-existing, D-145/C08 family, not in the ledger by name):** `arr[i++] -= yield 1` (and `+= await`) evaluates the index's `i++` twice and writes the wrong element. Node gives `i 1 95,200,300`; PTS gives `i 2 100,95,300`, and so does the base.
3. **NOTE (verified documented behaviour):** a class method read by name through a result is bound, as through `any` (D-144, D-178). `const m = t.value; m()` returns 9 where Node throws a TypeError. The base does the same through `any`.
4. **NOTE (verified documented gap):** a result without `done` reads `done` as `false` through a one-shape type (`IteratorResult<number, number>`) and as `undefined` through a union type. Both match D-178 and the SPEC text.

---

## 1. SHOULD-FIX (regression, performance): the by-name fallback allocates per read

- **Repro:** `/tmp/pts-h03b-review5/p/perf_nodone_noas.pts`
  ```ts
  class Counter {
    i = 0;
    next(): IteratorResult<number, undefined> {
      if (this.i >= 2000000) return { done: true, value: undefined };
      return { value: this.i++ };
    }
  }
  const c = new Counter();
  let sum = 0;
  for (;;) {
    const r = c.next();
    if (r.done) break;
    sum += r.value;
  }
  console.log("nodone", sum);
  ```
- **Output:** `nodone 1999999000000` on all three builds, so it is correct everywhere.
- **Instruction counts** (`PTS_THREADS=1 valgrind --tool=callgrind`):

  | Build | Instructions |
  |---|---|
  | Base (`perf_nodone_noas_base`) | 445,182,914 |
  | Tree (`perf_nodone_noas`) | **1,212,981,392** |
  | Tree runtime with the round-4 fallback emulated (`home-r4obj`: `return PTS_VALUE_UNDEFINED` for `PTS_KIND_OBJECT`) | 455,177,655 |
  | Tree runtime with static `"done"`/`"value"` keys only (`home-static`) | 789,162,917 |

  The same program with `as IteratorYieldResult<number>` (`perf_nodone.pts`) gives 1,212,980,950 on the tree and 445,183,764 on the base. With `done: false` written (`perf_withdone.pts`), the tree is faster than the base: 503,178,380 against 671,182,832.
- **Profile** (`callgrind_annotate --inclusive=yes out/cg_noas.out`):
  - `pts_result_get_slow`: 878,575,296 inclusive, about 439 instructions a read.
  - Of that, `pts_string_from_cstr` is 462,574,477 and `pts_string_from_utf8` is 418,573,708.
  - Self cost also includes `GC_malloc_kind` 104M, `object_get` 108M and `strcmp` 80M.
  - So 2M reads produce 2M garbage strings.
- **Cause:** `pts/runtime/src/object.c` (`pts_result_get_slow`, last line):
  ```c
  return pts_dynamic_get(pts_value_from_object((pts_object *)object), pts_value_from_string(pts_string_from_cstr(name)));
  ```
  `result_done` and `handed_value` in `generator.c`, which h03b.md cites as the model, use `PTS_STATIC_STRING` keys. Before change 2, a `PTS_KIND_OBJECT` without the field or getter returned undefined at once.
- **Fix suggestion:**
  - For `PTS_KIND_OBJECT`, fall back to `pts_dynamic_get` only when `method_index(type, name, PTS_METHOD) >= 0`, and otherwise return `PTS_VALUE_UNDEFINED`. Inherited methods must be in the subclass's table; my `m1_methods.pts` shows inherited getters are found that way, but check methods with the same probe.
  - Use `PTS_STATIC_STRING` keys for `"done"` and `"value"`, which are the only names the compiler passes.
  - Pin it with a callgrind-style check or a benchmark row in h03b.md, or record the cost in D-178's known gaps.

## 2. NOTE (pre-existing): a compound target's index with side effects is evaluated twice across a suspension

- **Repro:** `/tmp/pts-h03b-review5/p/n1_index_update_twice.pts` (`arr[i++] -= yield 1;` in a generator, `arr[j++] += await Promise.resolve(1);` in an async function).
- **Output:**
  - Node: `gen i 1 95,200,300` / `async j 1 101,200,300`
  - Tree: `gen i 2 100,95,300` / `async j 2 100,101,300`
  - Base: the same as the tree.
- **Cause:** `keepCurrent` (`yield_expressions.go:384-406`) reads `l.expression(target)` before the yield, and `assignmentTarget` evaluates the index again after the resume. Calls are kept: `box(0)`, `idx(1)` and `key("b")` each logged once in `p/c10_side_effects.pts`. An update expression is not kept.
- This is unchanged by the slice, and it belongs to C08's "key ordering" acceptance, but no ledger row names it. It is a silent wrong write, so it is worth adding to C08 or H08.

## Round-4 findings: status

| # | Round 4 | Now | Evidence |
|---|---|---|---|
| 1 | R2 residual: union-typed value and element access read the getter twice | **Fixed** | Node and tree both print `5x reads 1` for `r2_min_union` and `15 reads 1` for `r2_min_element`; the base gives PTS1001. The controls `r2_union_iface_ctl` and `r2_element_iface_ctl` print `reads 1` (base `reads 2`). `r2_variants` matches Node on 9 of 10 lines; the 10th, `iface`, is D-178's documented copy at the conversion to an interface, and the base also differs from Node there. In `out/r2_min_union.c:328` the resume binds `current_2 = pts_frame->current`, with no second `pts_result_get`. |
| 2 | Methods not found by name | **Fixed** | `s6b_method_any_ctl` and `s6_value_method` match Node. In `m1_methods.pts`, inherited methods, overrides, union types, superclass getters (union and one-shape) and frozen instances all match Node. Missing properties still give undefined: `missing value undefined true`, and through a union type `missing done union undefined 3` (`m2_kinds.pts`). Setter-only properties read undefined (`m3_setter_only.pts`). |
| 3 | Suite receipt predated the fixes | **Fixed** | `h03b-suite.jsonl.gz` events run 2026-10-16T20:59:47 to 21:06:40, after the last code mtime (20:58:48). Recounted: 1381 pass / 256 skip / 0 fail. It is byte-identical to `/tmp/pts-h03b-s4/suite2.jsonl`. The receipt's `cli_sha256` equals `sha256sum pts-bin/pts` (`450ed016…1774`, built 20:59:46). |
| 4 | `b3d` 1.06× | Withdrawn | h03b.md Performance and D-178 ("an earlier 1.06× didn't reproduce"). |
| 5 | h03b.md accuracy | Fixed | It now says "22 records of the 18 tests". The tarball exists (554 entries, including `session4/bench/b3d_user_forof_donefirst.pts` and `summary-final.txt`). No placeholders are left outside round 4's verbatim text. |
| 6, 7, 9 | Ledger | Done | C59, C60, C61 and C57's extension. I re-verified C62's "the same on `a65a75d62`": `p/c62_call_value.pts` gives `calling r.value is not supported yet` on both, where Node prints `call 41`. |
| 8 | Code quality | Done | `isIteratorResultType` and `isIteratorResultUnion` are gone (`grep -rn` finds nothing). `interfaceCast` reports a PTS1001 (`objects.go:918-920`), and `sameType` returns nil when `convertedResult` does (`lower.go:3353`). The `readKeptAndStore` doc now matches the code. The `pts.h` comment and D-178 mention methods. |

## Verified good (evidence)

**Change 1.** I compared Node, the tree and the base with `run.sh`; outputs are in `out/*.txt`. Every probe below matched the base, and also Node except where noted:

| Probe | What it covers | Result |
|---|---|---|
| `c1_arrays` | `a[i] += yield`, `**=`, `string[]` `+=`, `c.length -= yield`, an `int[]` generator with `+= *= <<= /=` | Matches Node, except that `int /=` truncates by design (`3,6,9` against Node's `3,6.4,9`) |
| `c2_typed` | Float64Array, Int32Array and Uint8Array (wrapping) | Matches Node |
| `c3_records` | `rec["a"]`, `rec[k]`, index-signature `m.x` and `m["y"]`, string records | Matches Node |
| `c4_union_narrowed` | Interface `number \| string` narrowed by `typeof` to number and to string, optional `z` narrowed by `!== undefined`, `number \| undefined` | Matches Node. `o.z! += yield` is PTS1001 on both the base and the tree, so I removed that line. |
| `c5_class_nested` | Class fields and strings, a nested `c.inner.b`, an interface's number and string, `o.a.b`, `o.arr[1]` | Matches Node. The array-growing `length +=` throws the same RangeError on the base and the tree (a pre-existing limitation). |
| `c6_logical` | `??=`, `\|\|=`, `&&=` with yield on interfaces and arrays (the other path) | Matches Node |
| `c7_any` | `x.n`, `x["m"]`, `o.a: any`, `x.o.k` | Matches Node |
| `c8_loose` | Loose destructured locals | Matches Node |
| `c9_await` | The same place kinds with `await` in an async function, and `yield`/`await` in an async generator | Matches Node |
| `c11_exotic` | `unknown` narrowed, an `any` field, `string \| string[]` narrowed, tuple elements, a class accessor, a static field, an interface backed by a class getter, a union receiver, `this.f` in a generator method | Matches Node. Improvement: the getter-backed interface logs `get,set9,get` like Node (base `get,get,set9,get`). |
| `c12_results_kinds` | Getter results of `int`, `string`, narrowed `number \| undefined` and `**=`, plus `+= await`/`-= await` on a result and an `IteratorResult<number, number>` | Matches Node; the base gives PTS1001 |
| `c13_left_yield` | Yields on both sides (`a[(yield 1) as number] += yield 2`, `os[…].v *=`, `rec["k" + (yield 5)] -=`) | Matches Node |
| `c14_rec_union` | A `Record<string, number \| undefined>` narrowed by `.k` and by `["j"]`, and `Record<string, int>` | Matches Node |

The emitted C for the union case: `PTS_HOME=… pts emit-c p/r2_min_union.pts -o out/r2_min_union.c`. Line 304 reads once before the yield, through `pts_result_get` and a check. Line 328 binds `current_2 = pts_frame->current` after the resume and computes from `pts_frame->current`, with no second read. `current_2` is an unused local, which is harmless.

**Change 2.** Covered by `m1_methods`, `m2_kinds`, `m2b_kinds_any` and `m3_setter_only`:

- Through `any` into a result union, the tree is the same as the base or closer to Node. Arrays, maps, functions and dates still fail the checked conversion as on the base. An `Error` now reads `undefined undefined` as in Node. A class whose `value` is a method fails the `number | undefined` check because the method is found; that is D-L09's checked cast, not a defect.
- `iterator_results_threads` passes on 1, 2 and 4 threads.
- `pts_dynamic_get` is the same path that `any` reads already use, and the object stays live in the caller's frame, so I see no GC or race concern.

**Change 3.** I found no new PTS1001 from `interfaceCast`. I tried Pick, Partial, intersection, Readonly, `Record<"value", …>` and generic-interface targets (`p/i_*.pts`) with a runtime result and an asserted getter literal:

- Partial now works where the base gave PTS1001.
- The others give the same pre-existing `doesn't provide with the same types` PTS1001 on both builds.
- The new message never appears.

**Tests** (`PTS_REQUIRE_NODE=1`, `-count=1 -p 1 -parallel 2`):

| Run | Result |
|---|---|
| Slice filter `^(iterator\|yield_star\|array_pattern_elision\|any_maps_error\|iterators_through)` | Exit 0; 48 PASS lines / 3 SKIP / 0 FAIL. All 21 slice files ran: the 18 new ones, plus `iterator_next_result_getters`, `iterator_result_as_cast`, `iterator_result_getters_*` and `any_maps_error`. The skips are `TestRunMatchesNode` for `any_maps_error`, `iterator_results_threads` and `iterator_results_copied` (by design). |
| D-145 subset `(yield\|await\|suspen\|compound\|logical_assign)` | Exit 0; 35 PASS / 3 SKIP / 0 FAIL. The skips are `logical_assign_suspensions` (C25, H08) and `TestRunMatchesNode/logical_assign_any_checks`. |
| `^(async_functions\|suspension_snapshots)$` | Pass |
| `^TestErrors$` | Pass (83 subtests) |
| `go test ./pts/lower/... ./pts/cgen/... ./pts/ir/...` and `go vet ./pts/lower/` | ok, clean |

**Docs.** I checked the claims against the code and the evidence:

- h03b.md's round-4 paragraph agrees with the code, the RED records (`round4_records`: 2 records, red on the session-4 starting tree) and my reruns.
- h03b.md's GREEN section: the receipt time window and the CLI sha agree with the files on disk.
- D-178's changed sentences: by-name reads find "a field, a getter, or as through `any` (a class's method, bound…; undefined if there is none)", and the kept read is "converted to the place's type". Both match the code.
- SPEC §9.122 matches the code; the line about the one-shape `done` gap matches `m1_methods`.
- CONSOLIDATED C57 and C59–C62 match the probes.

The one mismatch is in h03b.md's round-4 #2 text: "falls back to `pts_dynamic_get` … as `handed_value` and `result_done` read". Those two use static keys and the new fallback doesn't (finding 1).

## Commands

```sh
# build
cd /home/bryan/projects/parallelscript && GOMAXPROCS=2 go build -p 2 -o /tmp/pts-h03b-review5/pts ./cmd/pts
# differential probes (Node, tree, base)
/tmp/pts-h03b-review5/run.sh /tmp/pts-h03b-review5/p/<probe>.pts
# C of the union case
cd /tmp && PTS_HOME=/home/bryan/projects/parallelscript PTS_CACHE_DIR=/tmp/pts-h03b-review5/cache /tmp/pts-h03b-review5/pts emit-c /tmp/pts-h03b-review5/p/r2_min_union.pts -o /tmp/pts-h03b-review5/out/r2_min_union.c
# finding 1: counts (tree, base, emulated round-4 runtime)
cd /tmp && PTS_HOME=/home/bryan/projects/parallelscript PTS_CACHE_DIR=/tmp/pts-h03b-review5/cache /tmp/pts-h03b-review5/pts build /tmp/pts-h03b-review5/p/perf_nodone_noas.pts -o /tmp/pts-h03b-review5/out/perf_nodone_noas
cd /tmp && PTS_HOME=/tmp/pts-h03b-s4/base PTS_CACHE_DIR=/tmp/pts-h03b-review5/cache-base /tmp/pts-h03b-s4/base/pts-bin/pts build /tmp/pts-h03b-review5/p/perf_nodone_noas.pts -o /tmp/pts-h03b-review5/out/perf_nodone_noas_base
cd /tmp && PTS_HOME=/tmp/pts-h03b-review5/home-r4obj PTS_CACHE_DIR=/tmp/pts-h03b-review5/cache-r4obj /tmp/pts-h03b-review5/pts build /tmp/pts-h03b-review5/p/perf_nodone_noas.pts -o /tmp/pts-h03b-review5/out/perf_nodone_noas_r4obj
cd /tmp/pts-h03b-review5/out && PTS_THREADS=1 valgrind --tool=callgrind --callgrind-out-file=cg_X.out ./perf_nodone_noas[_base|_r4obj]
callgrind_annotate --inclusive=yes /tmp/pts-h03b-review5/out/cg_noas.out
# (home-r4obj / home-static: symlink copies of pts/runtime with only object.c edited, plus a pts/third_party symlink; the repo is untouched)
# tests
PATH=$HOME/.local/node/bin:$PATH GOMAXPROCS=2 PTS_REQUIRE_NODE=1 go test -count=1 -p 1 -parallel 2 -timeout 40m ./pts/tests/ -run '^(TestRun|TestRunMatchesNode)$/^(iterator|yield_star|array_pattern_elision|any_maps_error|iterators_through)' -v   # gotest-slice.log
PATH=$HOME/.local/node/bin:$PATH GOMAXPROCS=2 PTS_REQUIRE_NODE=1 go test -count=1 -p 1 -parallel 2 -timeout 40m ./pts/tests/ -run '^(TestRun|TestRunMatchesNode)$/(yield|await|suspen|compound|logical_assign)' -v   # gotest-d145.log
GOMAXPROCS=2 go test -count=1 -p 1 ./pts/tests/ -run '^TestErrors$'
GOMAXPROCS=2 go test -count=1 -p 1 ./pts/lower/... ./pts/cgen/... ./pts/ir/...
# receipt
python3 -I (time range and counts of docs/agent/hardening-evidence/h03b-suite.jsonl.gz); cmp with /tmp/pts-h03b-s4/suite2.jsonl; sha256sum pts-bin/pts
```
