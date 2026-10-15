# H03a independent review (opus-reviewer, claude-opus-5-5, xhigh)

Six read-only rounds by the `opus-reviewer` subagent on the parent's uncommitted H03a tree against base `7f0a4131a`. Each round's report is reproduced verbatim below. The reviewer's probes and harness scripts are in [h03-review-probes.tar.gz](h03-review-probes.tar.gz) (rounds 1 and 2; `probes/`, `tools/`, expected under `/tmp/pts-h03-review/`) [h03-review3-probes.tar.gz](h03-review3-probes.tar.gz) (round 3, with its benchmark programs; expected under `/tmp/pts-h03-review3/`) and [h03-review4-probes.tar.gz](h03-review4-probes.tar.gz) (round 4).

- **Round 1** (CLI built 20:48): not ready. Blockers B1 (a `let` array pattern in a `for` initializer panicked the compiler, a regression) and B2 (the new C03 diagnostic rejected inferred `next()` getter results that the base compiled), should-fix S1 (C31 not fixed for an interface extending `Iterable`), S2 (typed arrays through `any` were snapshots; stale comments), S3 (`array_pattern_checked_close.pts` didn't discriminate). All fixed by the parent before round 2; N1 became C33 (H03b), N5a C34 (H04), N2 went to H11.
- **Round 2** (CLI built 21:17): no blockers. R2-S1, a pre-existing memory-safety hole next to C03 (`as IteratorResult<T>` of another layout), became ledger row C35 (H03b) with the skipped test `run/iterator_result_as_cast.pts`; R2-S2 (typed `Set`/`Map`/string declarations past the end threw) was fixed by making every non-tuple source's pattern names loose (`readsElement`); R2-N1 is recorded under V08 (H22); R2-N2 is this file.
- **Round 3** (CLI built 21:47, sha256 `663583e4…e0`): two blockers, both regressions of the R2-S2 change: R3-B1, a loose name bound to a map's entry was copied into a new tuple at every read (writes and identity lost; the map-iterator half already in round 2), and R3-B2, `+=` on a loose string from a `let` pattern was PTS1001; should-fix R3-S1, hot reads of newly loose `Set`/typed-array names cost 2.9×/2.8×. The parent fixed both blockers (`looseItem`; the narrowed read's type in `compoundAssignment`, which also fixes the base's array case), made the checked number conversion inline and dropped the handler for typed-array sources (reads 1.12×/1.05× of the base), fixed nits N1 and N3, and added `run/array_pattern_loose_identity.pts` and `run/array_pattern_loose_concat.pts`. N2 (a union of tuples is loose) is left: correct output, one extra check per read.
- **Round 4** (CLI sha256 `f2566013…4496`): the round-3 fixes work; one blocker, R4-B1 (loose 64-bit integers read through a double; the base did so for `int[]`), and should-fixes R4-S1 (unions with a tuple member, and the pre-existing parameter-pattern segfault, C37), R4-S2 (binding-time conversion cost in destructuring-heavy loops), R4-S3 (`+=` and `valueOf`, C38). The parent fixed all four (R4-B1 in `looseRead` rather than `checkedConversion`, so `as` keeps its rules; R4-S1 for every pattern binding, not only loose ones; R4-S2 by keeping elements read from arrays with a declared element type as they are) and nit N1 and N2, and added `run/array_pattern_union_items.pts`, `run/compound_concat_valueof.pts` and `run/array_pattern_loose_int64.pts`.
- **Round 5** (CLI sha256 `48f89ff2…1474`): no blocker; the round-4 fixes hold and the wider binding conversion fixes two more C37-family cases. Should-fix R5-S1 (a pre-existing 4 KB memset per dynamic conversion made union-typed names 1.4–5.2× the base) and R5-S2 (tests for those two cases); nits N1–N3. The parent applied the reviewer's three-line runtime change (stacks initialised by depth only), added `run/pattern_union_dynamic_items.pts`, fixed the comment and D-177, and recorded the pre-existing findings as C39, C40 and a V09 note. Probes: [h03-review5-probes.tar.gz](h03-review5-probes.tar.gz).
- **Round 6**: a final check of the round-5 delta and of the documentation: no blocker; the code and the new test are ready. Its documentation corrections (R6-S1 pre-fix numbers presented as final, R6-S2 C38 overclaim and missing C39/C40 gaps, R6-S3 PROGRESS wording, nits N1–N8) were applied before the commit; the pre-existing misleading `| undefined` hint for `(number | undefined)[]` parameter patterns is noted under H11.

---

# H03a review (opus-reviewer, claude-opus-5-5, xhigh), 2026-10-15

I reviewed the parent's uncommitted diff against base `7f0a4131a` in `/home/bryan/projects/parallelscript` without modifying it. This covers the compiler, runtime, tests and the DECISIONS/SPEC/plan/ledger edits.

**Verdict: not ready to commit.** There are two blockers, both regressions where the base compiled and matched Node, plus three should-fix items. Everything else I probed is correct, including the core lazy-iteration and IteratorClose semantics, void getters and bare `return;`.

## Setup and provenance

- **Review CLI.** `cd /home/bryan/projects/parallelscript && GOMAXPROCS=2 go build -p 2 -o /tmp/pts-h03-review/pts ./cmd/pts` exited 0. The binary is from 20:48:10. No compiler, runtime or test file in the tree is newer: I checked with `-nt` against every changed path before writing this. The parent edited `HARDENING_PLAN.md` and `CONSOLIDATED.md` after the start snapshot; I reviewed those too.
- **Base CLI.** `/home/bryan/projects/parallelscript/.claude/worktrees/agent-afb5379bc12f20b9e/pts-bin/pts`, with `PTS_HOME` set to that worktree and `PTS_CACHE_DIR=/tmp/pts-h03-repro/cache`.
- **Node.** `~/.local/node/bin/node --no-warnings --experimental-transform-types x.mts` (v24.21.0).
- **Harness.** `/tmp/pts-h03-review/tools/cmp.sh <probe.pts>` builds and runs the probe with the new CLI and the base CLI, and runs it under Node. Each run uses `ulimit -f 40960`, `ulimit -v 4194304` and a 60 s timeout. Outputs go to `/tmp/pts-h03-review/out/<probe>/{node,new,base}.{out,err,exit,build,buildexit}`.
- **Rerun everything.** `/tmp/pts-h03-review/tools/repro_all.sh` exits 0 and writes `/tmp/pts-h03-review/out/repro_all.txt`.

---

## BLOCKER B1: compiler panic on a `let` array pattern in a `for` initializer (regression)

When a classic `for` loop's `let` initializer is an array pattern over any of these values, the compiler panics:
- an `any` value;
- an iterator or iterable type (`Generator<T>`, `Iterable<T>`);
- a class with `[Symbol.iterator]`.

The base compiled all four forms below and matched Node. Since H03 is the "compiler panic" hardening item, this regression is a blocker.

| Repro (`/tmp/pts-h03-review/probes/`) | Source | Node | base | new |
|---|---|---|---|---|
| `r2a_for_init_plain.pts` | `for (let [i, n] = [1, 3] as any; i < n; i++) console.log("j", i, n);` | exit 0: `j 1 3`, `j 2 3` | build 0, run 0, same output as Node | **`pts build` exit 2**: `panic: interface conversion: ir.Stmt is *ir.If, not *ir.LocalDecl` |
| `r2b_for_init_default.pts` | `for (let [i, n = 3] = [0] as any; i < n; i++) …` | `k 0 3`, `k 1 3`, `k 2 3` | same as Node | exit 2: `… is *ir.LabeledBlock, not *ir.LocalDecl` |
| `r2c_for_init_generator.pts` | `function* src(): Generator<number> {…}`, then `for (let [i, n] = src(); i < n; i++)` | `g 0 2`, `g 1 2` | same as Node | exit 2, same panic |
| `r2d_for_init_class.pts` | `class P implements Iterable<number> {…}`, then `for (let [i, n] = new P(); …)` | `p 0 2`, `p 1 2` | same as Node | exit 2, same panic |

Two controls are unaffected: `r2e_for_init_const.pts` (a `const` pattern) and `r2g_for_init_let_array_control.pts` (a typed `number[]` value).

**Stack trace:**
- `pts/lower/lower.go:597` `declaredLocals`
- ← `lower.go:567` `forStatement` (`bindings = declaredLocals(init)`, only for `let`)
- ← `breakable` ← `statement`

**Cause.** `declaredLocals` type-asserts every statement of a `let` for-initializer to `*ir.LocalDecl`. `destructureIterable` now emits other statements there:
- the unprotected path appends `if (!done) close`;
- the protected path appends the `withFinally` statements (`LabeledBlock`, `If`).

**Fix.**
- Make `declaredLocals` collect only `LocalDecl`s (e.g. `if decl, ok := stmt.(*ir.LocalDecl); ok { … }`).
- Better, collect only the pattern's names, so that `Loop.Bindings`' per-iteration copies don't include `iteration`, `index`, `done`, `completion` and `thrown`.

**Tests to add.** `run/` tests for the four forms above, each with a closure that captures `i` per iteration (for example, push `() => i` and print the results). This pins the `Bindings` semantics after the fix. Land these only with the fix: the harness compiles in-process, so a panic kills the whole `go test` package.

---

## BLOCKER B2: the new C03 diagnostic rejects getter results that compiled and matched Node on the base (regression)

The diagnostic in `pts/lower/objects.go:326` is keyed on the literal's **contextual** type: `literalContext(node)` plus `isIteratorResultType(context)` plus any accessor. Some literals have that contextual type but are never converted to D-109's result shape.

The main case is an object-literal iterator whose `next()` method has no annotated result. tsc contextually types the result literal as `IteratorResult<T>` through `Iterator<T>.next`, but infers `next`'s result as the literal's own type. The iterator is then a dynamic value, and the runtime reads `done`/`value` lazily through `pts_dynamic_get`. On the base this worked exactly like Node.

| Repro | Node | base | new |
|---|---|---|---|
| `r1_inferred_next_getters.pts`: `const iterable: Iterable<number> = { [Symbol.iterator]() { let i = 0; return { next() { …; return { get done() {…}, get value() {…} }; } }; } }`, then `for…of` and spread | exit 0, 13 lines: `read done 0`, `read value 0`, `item 0`, …, `read done 2`, `[ 0, 10 ]` | build 0, run 0, **identical to Node** | **build exit 1**: `9:16 - error PTS1001: an object literal with getters or setters as an iterator result (\`IteratorResult<number, any>\`) is not supported yet; a \`next(): any\` can return one` |
| `r1b_iterator_literal_next.pts`: `const it: Iterator<number> = { next() { …return { get done() {…}, get value() {…} }; } }; console.log(it.next().done);` | `false` | `false` (identical) | build exit 1, same PTS1001 at 3:34 |
| `r1c_setter_as_iterator_result.pts`: `return { done: false, value: k, set extra(v: number) {} } as IteratorResult<number>;` (the accessor isn't `done` or `value`) | `1` | `1` (identical) | build exit 1, same PTS1001 |

Unaffected controls:
- `probes/c03/k_class_inferred.pts`: a class `next()` without an annotation has no contextual type, so it works on both.
- `probes/c03/j_async_inferred.pts`: the async variant works on both.

No suite test caught this. Every existing getter-result test either uses `next(): any` (`run/iterator_close_order.pts`, `run/iterator_result_getters_any.pts`) or annotates the result (`errors/iterator_result_getters.pts`).

**Fix**, either of:
- Report the new message only where the conversion actually fails, i.e. in the path that used to say `converting object4 to object1 | object2`, when the source literal has accessors and the target is an iterator-result shape or union.
- Require that the context comes from a declared result type, an assertion or a typed destination, not from an inferred method result.

In both cases, restrict it to accessors named `done` or `value` (r1c). Then add r1 as a run test that is compared with Node.

---

## SHOULD-FIX

### S1. C31 is not fixed for declarations over an interface-typed iterable, but the ledger and SPEC say it is

`probes/s1_c31_interface_iterable.pts` declares `interface Bag extends Iterable<number> { readonly size: number }`, holds a one-item `NumBag` as `Iterable<number>` (`plain`) and as `Bag`, then destructures `const [p1, p2] = plain` and `const [b1, b2] = bag`.

| | Result |
|---|---|
| Node | `Iterable 1 undefined`, `Bag 1 undefined` |
| new | prints `Iterable 1 undefined`, then exit 1: `Uncaught TypeError: Expected number, got undefined` at line 12 |
| base | exit 1, RangeError on the first pattern (pre-existing) |

**Cause.** The two code paths use different tests for "this is an iterable":
- `destructure` sends any `ir.Interface` or non-tuple `ir.Class` to `destructureIterable` (`isPlainObject`, `destructuring.go:41`), so the pattern steps lazily.
- `readsElement` (`loose_locals.go:172`) uses `isIterationSource(t)`. Its `hasIteratorMethod` rejects a `[Symbol.iterator]` declared in the default library (`Iterable`), so the names aren't loose.

**Fix.** Use the same criterion in both places (e.g. the initializer's IR type is `Value` or `isPlainObject`), or narrow the C31 wording in CONSOLIDATED, D-177 and SPEC §3.14 ("a name it declares past the end is `undefined`").

Two related limits fall under the same "as for arrays" caveat: nested names (`p40_nested_typed_past_end.pts`) and the names of a nested pattern inside a rest element (N2). Name them in D-177's known gaps.

### S2. D-177 and the code comments claim things that aren't true

**Typed arrays.** D-177 and `ir.go` say a typed array held as `any` is iterated "as a new array, which JS's iterators over them can't be told apart from". It can be told apart. `probes/s2_typed_array_any_live.pts` writes `ta[2] = 99` during `for (const x of viaAny)`:
- Node prints `loop 99`.
- new and base both print `loop 3`. This is pre-existing, both runs exit 0.

Either fix the runtime or the claim:
- **Runtime fix.** In `pts_iteration_open` (`generator.c:269`), return the typed array itself instead of `pts_value_iterable`'s copy. `pts_spread_length` and `pts_spread_element` already read typed arrays, bounds-safe. A typed `for…of` over a typed array is already live.
- **Text fix.** Correct D-177, the `ir.go` comment and SPEC §9.121 instead.

**Stale comments.** These still say a map's or set's items "are read by index". Maps and sets now use live iterators:
- `destructuring.go:80`;
- `generators.go:337` (`iteratorLoop`);
- the `IterationOpen` comment in `ir.go:1217`, whose list of stepped sources also omits maps and sets.

### S3. Test gaps and one test that doesn't discriminate

**No tests for B1 or B2.** Add them with the fixes.

**`run/array_pattern_checked_close.pts` passes on the base too, apart from the message.** Base output: `closed`, `caught Expected number, got string`. New output: `closed`, `caught Expected number | undefined, got string`.

The base's take-two-then-close also ran `finally` before the throw, so the test doesn't pin "close when the checked conversion throws mid-pattern". To make it discriminate:
- use a three-element pattern (`const [a, b, c] = …`) with a logging `yield`;
- the base logs three yields before `closed`, and the new path logs two.

**`docs/agent/hardening-evidence/h03.md` doesn't exist yet**, though `HARDENING_PLAN.md` and `CONSOLIDATED.md` link to it. It is presumably written at commit time; this is a reminder.

---

## NITS (all pre-existing unless noted)

### N1. An elision reads the result's `value` in PTS
`probes/n1_elision_reads_value.pts`. JS's elision uses IteratorStep, which reads `done` only.

| | Output |
|---|---|
| Node | `read done 0`, `read done 1`, `read value 1`, `return()`, `b 10`, … |
| new | adds `read value 0` and `read value 1` (exit 0) |
| base | also reads `value`, and drains (exit 1) |

`resume_object_iterator` reads `value` on every step that isn't done. This belongs to the getter-results family and fits H03b next to C30 and C32.

### N2. A nested pattern inside a rest element, past the end
`probes/n2_rest_nested_past_end.pts`: `const [x, ...[y, z = "z"]] = one() as any;`
- Node prints `1 undefined z`.
- new and base both exit 1: `RangeError: Array index 0 is out of range (length 0)…`.

The rest is an `any[]` read by the array path, and the nested names aren't loose. This is the C31 family; at least list it.

### N3. `destructureIterable` copies `iteratorLoop`'s jump-target argument
It passes `len(l.targets)-1` to `withFinally` (`destructuring.go:154`), taken from `iteratorLoop`, where the loop's own label is inside the protected code. A pattern contains no jumps, so this is unobservable. `len(l.targets)` would be the accurate value, or a comment saying it doesn't matter.

### N4. Wording of the `ir.IterationNext` doc
"steps past the item at its Number argument": for an index source, it tests whether item `index` exists.

### N5. Found incidentally on the base too; suggested as ledger rows, not this slice's
- **(a) Panic.** `probes/n5a_symbol_iterator_getter_panic.pts`: an object-literal getter named `[Symbol.iterator]` (`get [Symbol.iterator]() {…}`) makes `pts build` exit 2 on both CLIs, with `panic: runtime error: index out of range [-1]` in `accessorFields` (`objects.go:88/89`).
- **(b) Parameter-pattern timing.** `probes/n5b_generator_param_pattern_timing.pts`: the parameter patterns of generators and async functions run at the first `next()` or later, not at the call. Node prints `src yield 1 … src closed` before `created`; PTS prints `created` first, on both CLIs.
- **(c) Message text.** The non-iterable message for a nested pattern is `[a] is not iterable`. V8's is `number 0 is not iterable (cannot read property Symbol(Symbol.iterator))` (`p15_close_semantics.pts`, only this line differs).

### N6. Loose-local type in the error message
The expected message in `array_pattern_checked_close.pts`, `Expected number | undefined, got string`, shows the loose-local type (D-172) rather than the declared `number`. This is cosmetic and D-172/H11's, not this slice's.

---

## Classification

I agree with the H03a/H03b split and the H11 hand-off:
- C28 (methods through `any`), C30 (typed `it.next()` result) and C32 (`yield*` passthrough) are separable features.
- Typed names in parameter patterns and `for…of` bindings past the end match the array behaviour and are H11's.

I also agree with keeping C03's typed half as a diagnostic. The rejected alternatives in D-177 are sound: a dynamic result type would turn a compile-time error into a run-time TypeError for typed callers. The diagnostic just has to stop firing where nothing converts the literal (B2).

---

## Performance (D-177 claims re-measured)

Method:
- I rebuilt the parent's `/tmp/pts-h03/bench/*.pts` with both CLIs into `/tmp/pts-h03-review/bench/`, plus two programs of my own: `gen_pattern.pts` (`const [a, b] = gen()` over a typed generator, 1M times) and `any_pattern_default.pts` (the protected path).
- Both binaries print identical results.
- `PTS_THREADS=1 hyperfine -N -w 1 -r 6`, two separate rounds per program (the medians below; JSON in `bench/h_*.json` and `bench/g_*.json`).

| Program | base median (s) | new median (s) | new/base | D-177 |
|---|---|---|---|---|
| any_pattern | 0.803, 0.814 | 0.385, 0.384 | 0.48 (2.09× faster) | 2.08× faster ✔ |
| any_map_entries | 0.841, 0.894 | 0.512, 0.539 | 0.61 (1.64× faster) | 1.60× faster ✔ |
| typed_iterable | 0.288, 0.291 | 0.183, 0.182 | 0.63 (1.59× faster) | 1.54× faster ✔ |
| small_any_loops | 0.417, 0.409 | 0.486, 0.479 | 1.17 slower | 1.18× slower ✔ |
| big_any_array | 0.896, 0.880 | 0.970, 0.969 | 1.09 slower | 1.09× slower ✔ |
| gen_pattern (mine) | 0.123, 0.122 | 0.078, 0.078 | 0.64 | not claimed |
| any_pattern_default (mine) | 0.810, 0.807 | 0.459, 0.470 | 0.57 | not claimed |

All claims reproduce within noise.

**Optional idea.** The 1.09–1.18× cost on `any` arrays comes from the `_setjmp` and the `volatile` `index`/`done` locals that the close-on-throw handler needs. Only iterator sources need that handler. A runtime branch to an unprotected index loop for array sources would remove the cost, at the price of duplicating the loop body.

---

## Verified OK (probe, then result; "new == Node" unless noted)

**Earlier evidence**
- **H01b probes:** 23 C02/C03/C18 probes copied from `docs/agent/hardening-evidence/probes/h01b/` into `probes/h01b/`.
  - These all match Node: the 10 C02 probes plus the 2 parent C02 probes (7 red in total), the 6 C18 probes plus the 3 parent C18 probes, and `capped/c18-red-parent-infinite-any-generator` (run under a 4 GiB cap).
  - The C03 red fails with the new message.
- **Focused suite.** `PATH=$HOME/.local/node/bin:$PATH GOMAXPROCS=2 PTS_REQUIRE_NODE=1 go test -count=1 -p 2 -timeout 30m ./pts/tests/ -run 'TestRun(MatchesNode)?/(<the 12 new/changed tests>|any_iteration|generators_.*|iterables_.*|destructuring.*)$|TestErrors/iterator_result_getters' -v` exited 0: 67 PASS, 8 SKIP, all skips by design (`out/gotest_focus.log`).
- `go vet ./pts/lower/ ./pts/ir/ ./pts/cgen/` exited 0; `go test -count=1 -p 2 ./pts/lower/ ./pts/ir/ ./pts/cgen/` was ok.

**Test files**
- The 7 unskipped reproducer tests have the sha256 prefixes listed in `h03-reproducer.md`.
- The two skipped ones differ only by the added `// skip:` line; their staged versions match the reproducer's hashes.
- Both skip reasons are accurate.
- The parent's new tests are red on the base and bounded:
  - `iterator_result_getters_any` and `array_pattern_abrupt` exit 1 on the base, with a different output;
  - `array_pattern_checked_close` exits 0 on the base, differing only in the message; see S3.

**IteratorClose and binding order** (`p15_close_semantics.pts`, all match Node except the N5c message):
- At the normal end of a pattern, if `return()` throws, its error propagates; if `return()` gives a non-object, the result is `TypeError: Iterator result 5 is not an object`.
- If a default throws, the iterator is closed and close's error is dropped.
- If `next()` throws, the iterator isn't closed. The same holds for rest elements and elisions.
- If the `value` getter throws, the iterator isn't closed ([[Done]] is set).
- A non-iterable nested item closes the outer iterator.
- In a loop, a throwing `value` getter doesn't close the iterator, and `break` with a non-object `return()` gives a TypeError.

**Defaults and patterns**
- Defaults apply for exactly `undefined`, not `null` or `0`, in source order (`p03`; the base was wrong here).
- Element-by-element binding order holds for nested patterns, rest, rest after done, empty patterns, elision-only patterns, and strings with astral characters via `any` (`p13`).

**Scopes and frames** (`p07`, `p27`, `p28`, `p34_modules/`)
- Protected patterns work at module scope (names read by a function declared earlier), nested, and in generator frames across `yield`.
- They work in async frames, arrows and closures, and in loops with labeled `continue`/`break`.
- A protected pattern inside a `for…of` binding inside a generator stays correct when the generator is closed by `return()` mid-way.
- Exported protected patterns work across modules, including a `let` export mutated by a function.

**Suspensions and jumps** (`p08`, `p26`)
- `yield` inside an `any` `for…of` works, including a value sent in.
- `return` from inside the loop works, as do `.return()` and `.throw()` while suspended inside it.
- `await` and `throw` inside an `any` loop in async functions work.
- `switch` inside the loop, `continue`/`break` to outer labels through `switch`, and `return` from a nested loop all work.

**Closures and live collections** (`p04`, `p23`)
- Closures capture a fresh per-iteration binding through protected patterns.
- Maps and sets iterated through `any` are live: deletes, adds, `clear()` and `set` during the loop behave as in JS.
- Live array defaults (`push` inside a default) are seen by the next element.

**Typed and loose names** (`p21`, `p32`)
- Newly loose names (declarations over generators and class iterables) work in compound assignment, closures, array indexing, `switch`, swaps and tuples.
- String-, boolean- and class-typed protected names work in functions, generators, async functions and at module scope; this covers `collectStrings`' `""` constant for `LocalDecl`s without an initializer.

**Concurrency and builtins**
- Under `.par` (`p22`): shared `any` arrays, maps and an infinite generator iterated by 256 callbacks gave no crash in 8 runs. The only JS errors were D-130's racing `pop()` RangeError. The base ran out of memory draining the generator.
- `parallel for` with protected binding patterns over `any` rows and generators gives correct, stable sums (`p33`).
- Builtins in patterns (`p35`): `Headers`, `m.entries()`, `m.keys()`, an array iterator continued afterwards, and an object literal with `*[Symbol.iterator]`.
- The generated C (`out/p06.c`) marks the locals assigned inside `_setjmp` regions `volatile`, both the hoisted names and `index`/`done`/`item`, and reads only `done`/`iteration` after a longjmp. `pts_array_element` is bounds-safe, so the live by-index path is memory-safe under D-143 sharing.

**C02: void and never getters** (`p09`, `p11`, `p24`)
- Object-literal, class, static, generic and abstract getters work, as do setters paired with a `void` getter and overrides with `undefined`.
- Interface accessors (`get audit(): void` in an interface, implemented by a class and by a literal) work.
- `typeof` on them gives `"undefined"` (it was PTS1001 on the base), as do template literals, `??`, `String()` and `JSON.stringify`.
- A getter that falls off its end after a conditional `return;` works, as do private fields touched by a `void` getter.

**C27: bare `return;`** (`p10`, `p28`)
- It works in constructors, filter/find/some callbacks, inside `try/finally`, in loops, and in generic, `unknown`, `null | undefined`, function-, class- and array-typed results.
- It works in async functions, arrows, methods and object-literal methods, async generators, and with an `i32 | undefined` result (`p24`).
- `p25` (a `void`-typed function value's result discarded) is pre-existing and out of scope.

**C03 diagnostic**
- Its positions and wording are correct for annotated `next(): IteratorResult<T>`, `IteratorYieldResult<T>`, `IteratorResult<T> | undefined` and generics (`c03/a`, `b`, `f`).
- Plain shapes and inner literals that aren't results are unaffected (`c03/d`, `g`).
- Its only problem is B2.

**Already-documented gaps, confirmed**
- Typed parameter-pattern and `for…of`-binding names past the end throw (`p18`, `p19`, `p20`).
- Destructuring assignment is PTS1001 (`p36`, `p37`).
- `await` or `yield` in a pattern default gives an honest PTS1001 (`p16`, `p17`).

---

# H03a review, round 2 (opus-reviewer, claude-opus-5-5, xhigh), 2026-10-15

I rechecked the parent's fixes for my round-1 findings (`REVIEW.md`), read-only, on the uncommitted tree against base `7f0a4131a`.

**Verdict: no blockers.**
- All round-1 blockers and should-fix items (B1, B2, S1, S2, S3) are fixed, as are nits N3 and N4, and each is pinned by a test whose expect block is Node's exact output.
- The fixes broke nothing I could find: every round-1 probe gives the same output as in round 1 or now matches Node.
- Two new should-fix items. R2-S1 is a pre-existing memory-safety hole next to C03, not caused by the slice (base identical), which the C03 "decided" status should not hide. R2-S2 is that C31's claim is still broader than the code.

## Setup

- **CLI.** `cd /home/bryan/projects/parallelscript && GOMAXPROCS=2 go build -p 2 -o /tmp/pts-h03-review/pts2 ./cmd/pts` exited 0 at 21:17:15.
- **Harness.** `tools/cmp2.sh` is round 1's `cmp.sh` with `pts2` and outputs under `out2/`: new CLI, base CLI (the `7f0a4131a` worktree) and Node v24.21.0, with `ulimit -f 40960`, `ulimit -v 4194304` and a 60 s timeout.
- **Round-1 repros.** `tools/repro_all2.sh` exited 0 (`out2/repro_all.txt`).
- **All round-1 probes.** I reran `probes/p*.pts`, `probes/c03/*.pts` and `probes/t_*.pts` against Node (`out2/allprobes.txt`). For every remaining mismatch I compared stdout and stderr with round 1.
- **Not rerun.** I didn't run the full suite, since the parent is running it. Instead I re-implemented `expectBlock` and compared each H03a test's expect block with a fresh Node run, then ran the reproducer's tests with `pts2 run`.

## Round-1 findings: status

| Finding | Status | Evidence |
|---|---|---|
| **B1** `for (let [..] = any/iterable; …)` panic | **fixed** | `r2a`–`r2d`: new exit 0, same output as Node and base. `q01_for_let_bindings.pts` covers per-iteration closures over pattern names with defaults, rest, nested array and object patterns, a second declarator, typed arrays, labeled `continue`, generator and async bodies, and module scope; new and base both match Node. `declaredNames` collects only the names; hoisted protected names keep per-iteration copies. |
| **B2** C03 diagnostic rejected inferred `next()` results | **fixed** | `r1`, `r1b`, `r1c`: new exit 0, same output as Node and base. `c03/m_return_inferred_getter.pts` (an inferred `return()` with a value getter) matches Node. `errors/iterator_result_getters.pts` still gives exactly its three `// error:` lines (`pts2 build` exit 1, diff empty). The true positives `c03/a`, `b` and `f` keep the new message. `l_arrow_property` failed on the base too (two errors) and now carries the C03 message. |
| **S1** interface extending `Iterable` not loose | **fixed** | `s1_c31_interface_iterable.pts`: new matches Node; base exits 1. `isIterablePatternSource` is used for both the dispatch and `readsElement`. |
| **S2** typed arrays through `any` were snapshots; stale comments | **fixed** | `s2_typed_array_any_live.pts` and `q25_typed_array_inplace.pts` (`subarray` view, `sort`, `set` and `fill` during the loop, then a pattern) match Node; base doesn't. `q03_typed_arrays_any.pts` covers all nine kinds (clamping, wraparound, `-0`, `NaN`, `Float32` rounding) in `for…of`, patterns, rest, spread and an `Iterable<number>` holder; it matches Node. Comments in `ir.go`, `destructuring.go`, `generators.go` and `pts.h` are corrected. |
| **S3** `array_pattern_checked_close.pts` didn't discriminate | **fixed** | The base now prints `yield 1`, `yield two`, `yield 3`, `yield 4`, `closed`; the fix prints two yields, then `closed`. |
| N3 `withFinally` targets | **fixed** | `destructuring.go:154` uses `len(l.targets)`, with a comment. |
| N4 `IterationNext` wording | **fixed** | `ir.go:1218`. |
| N1 → C33 (H03b), N5a → C34 (H04), N2 and nested names → H11, N5b → D-109 gap | **recorded** | CONSOLIDATED rows C33 and C34, HARDENING_PLAN H03b/H04/H11, D-177's known gaps. All four repros behave exactly as in round 1. |

**Performance check of the S2 change.** `bench/any_typed_array.pts` runs `for…of` four times over a 5M-element `Float64Array` held as `any`. It is 1.44 ± 0.01× faster than the base (659 ms vs 456 ms; `PTS_THREADS=1 hyperfine -N -w 1 -r 5`), with identical results.

## Checks on the fixes themselves (all OK)

**`declaredNames`** (B1)
- It works at module scope and in functions, generators and async functions (`q01`).
- Hoisted protected names get correct per-iteration copies.
- A `const` initializer is unaffected (`r2e`).

**`readsElement` calling `irType`** (S1)
- *Duplicate diagnostics:* none. `report` dedups identical diagnostics (`lower.go:369`). `q05_bigint_dup.pts` gives the same three errors on base and new; an apparent doubling in my first look was my own display. `q12` (`arguments`) gives base's single PTS1002.
- *Generic functions:* `isLoose` caches per symbol, but generic functions instantiated with a generator, a class, an array, a `Set` and an `Iterable` in either order all match Node (`q06`).
- *Unusual initializers:* an intersection with a class iterable and `Generator<[number, string]>` with a nested pattern past the end match Node (`q14`).
- *Built-in iterators:* `arr.entries()`, `matchAll`, `m.entries()`/`keys()` and `Set.values()` in declarations with nested and rest patterns match Node (`q04`); the base exits 1.
- *Typed tuple:* a typed tuple past the end is a tsc error, unchanged.

**`reportedGetterResult`** (B2)
- *False positives:* none. It only rewords a conversion that already failed, and only for literals with a `done`/`value` accessor in an iterator-result context. A literal cast `as IteratorResult<number>` whose getters stay lazy still compiles, as on base (`c03r2/a_as.pts`, `q07`).
- *Nil node:* `objectCast` and `classCast` call it without `toUnion`'s nil guard, but a nil node would already have panicked in `diagnostic.At`, so there is no new risk.

**Expect blocks.** Every H03a run test's `// expect:` block equals a fresh Node run (`out2/expect/`). The exceptions are by design: `array_pattern_checked_close` is `no-diff`, and the two skipped tests' blocks also equal Node.

**Reproducer and parent tests.**
- `pts2 run` of the reproducer's tests plus `iterator_result_getters_any` and `array_pattern_abrupt` all equal Node.
- The new `array_pattern_past_end` and `typed_array_iteration_live` are red on the base (exit 1).
- `for_let_pattern_iterables` and `iterator_result_getters_inferred` are green on the base, which is expected: they guard round-1 regressions, not base defects. Don't list them as RED evidence.

---

## SHOULD-FIX

### R2-S1. A pre-existing memory-safety hole next to C03: `as IteratorResult<T>` accepts any layout; a narrowed `.value` read is a raw downcast

The base behaves identically, and the slice's diff doesn't touch this. I report it because CONSOLIDATED now says C03's typed half is "kept as an honest diagnostic", while the same construct spelled with an `as` compiles and is memory-unsafe.

**What happens.** An object literal whose shape isn't exactly an iterator-result member can be asserted to the result union with `as IteratorResult<T>`; this applies to literals with accessors and to literals with extra properties. The assertion is a relaxed cast. After narrowing on `done`, `r.value` is emitted as a raw field read through the yield-result shape:

```c
(((pts_class_object2 *)pts_value_as_object(r)))->value
```

The value inside is a different shape (e.g. `object3 { bool done; pts_closure *pts_get_value; … }`). A general union is read by name instead (`PTS_GET(…, "x", …)`), so it is safe (`q22_as_union_narrow_general.pts`, which matches Node).

| Repro (`/tmp/pts-h03-review/probes/`) | Node | base and new |
|---|---|---|
| `q17_as_cast_value_read.pts`: `return { done: false, get value(): number { return 42; } } as IteratorResult<number>;` then `if (!r.done) console.log(r.value)` | `false`, `42` | exit 0, **`[Function: get value]`** (a silent wrong value) |
| `q19_as_cast_type_confusion.pts`: the same with `const v = r.value; v + 1` | `number 42`, `43` | exit 1, `TypeError: Expected number, got function` |
| `q21_as_cast_segv.pts`: 11 `bool` properties sorting before the accessor's key `get value`, which moves the closure past offset 16 | `value is 42` | **exit 139, SIGSEGV** (`timeout: the monitored command dumped core`) |
| `q23_as_cast_extra_props.pts`: **no getters**, just extra `bool` properties and `value: 42`, `as IteratorResult<number>` | `value is 42` | **exit 139, SIGSEGV** |

Controls:
- `q24_wide_object_no_cast.pts`: the same wide object or class without the `as` is an honest PTS1001 on both CLIs.
- `q18_as_cast_getter_general.pts`: getter literals cast to other shapes, interfaces and unions match Node.

**Related observation.** With only `as IteratorResult<number>` added to its two returns (`q10_typed_c03_test_with_as.pts`), the skipped C03 target `run/iterator_result_getters_typed.pts` prints exactly its own expect block, i.e. Node's output, on new and base. Protocol consumers (`for…of`, spread, `Array.from`, `new Set`) read `done`/`value` by name through the runtime. So lazy getter results are already representable for them. Only typed reads after narrowing use the field layout.

That refines D-177's reason ("making one from the literal would run its getters at the `return`") and SPEC §3.14's wording. It also means `as` must **not** be suggested as a workaround in the diagnostic, given the rows above.

**Suggestion.**
1. Record a new ledger row with a skipped run test (q17 for the wrong value, q23 for the crash; a crashing binary is just a failed run, since the harness runs the binary out of process). The natural owner is H03b (iterator results, next to C30), or H04 if treated as a containment issue.
2. Fix either side:
   - make narrowed iterator-result reads check the runtime type, falling back to a by-name read, as general unions do; or
   - reject the assertion with the C03 message when the source shape isn't a result member.
3. Adjust the C03 row and D-177 so that "decided" doesn't read as "covered".

### R2-S2. C31's claim is still broader than the code: typed `Set`/`Map`/string declarations throw past the end

`probes/q02_past_end_kinds.pts`:

| Declaration | Node | new and base |
|---|---|---|
| `const [a, b] = new Set([1])` | `set 1 undefined` | `RangeError: Array index 1 is out of range (length 1)…` |
| `const [a, b] = new Map([["k", 1]])` | `map [ 'k', 1 ] undefined` | same RangeError |
| `const [a, b] = "x"` | `str x undefined` | same RangeError |

Typed arrays, map iterators and generic `T extends Iterable` (`q06`) are now correct.

D-177 says typed `Map`/`Set`/strings "keep their paths", which is accurate. But two other statements say more:
- SPEC §3.14: "a name it declares past the end is `undefined`, as for arrays (D-172)";
- C31: "fixed … for declarations over iterators and iterables".

Sets and maps are iterables, so both overclaim.

**Fix**, either:
- narrow the wording (add typed `Map`/`Set`/string declarations to D-177's known gaps and the C31 row); or
- let `readsElement` also count `isMap(irType(t))` and `ir.String`. The array path that `ValueToArray`/`ArrayFromString` feed already reads loose names unchecked (`destructuring.go`, the `*ir.Array` case).

---

## NITS

- **R2-N1. Old internal message in a few C03 spellings** (cosmetic; V08/H22's). These forms still report `converting object3 to object1 | object2` or `using a object3 as a object1` rather than the new message:
  - `satisfies IteratorResult<number>` (`c03r2/b_satisfies.pts`);
  - a getter literal as a property value of a typed object (`c03r2/i_property_value.pts`);
  - `as IteratorYieldResult<number>` and `as IteratorResult<string, string>` (`q11_as_other_results.pts`).

  The base gives the same messages.
- **R2-N2. Dangling link.** `HARDENING_PLAN.md` H03b cites `h03-review.md`, which doesn't exist yet. Presumably the parent copies this review there at commit time.

## Verified-OK additions this round

- **B1 behaviour:** `q01` (per-iteration closures in every scope).
- **`readsElement` and `irType`:** `q02`–`q06`, `q12`, `q14` (no spurious or duplicate diagnostics; generic instantiation order is irrelevant).
- **Typed arrays:** `q03` and `q25` (all kinds, live in-place mutation, `subarray` views). There is no resize/transfer in the runtime, and `pts_typed_array_element` is bounds-checked, so live reads are memory-safe.
- **`reportedGetterResult`:** `c03r2/*` (no false positives; positions fine, e.g. `h_paren` now points at the literal, 2:11).
- **Round-1 probes:** every probe that differed from Node in round 1 has the same output now, except `i_return_method`. It lost its second error, because the inferred `return()` getter literal now compiles; `m_return_inferred_getter` confirms that it runs like Node.

---

# H03a review, round 3 (opus-reviewer, claude-opus-5-5, xhigh), 2026-10-15

Scope: the round-3 delta on the uncommitted tree against base `7f0a4131a`. That is the broadened `readsElement` (`pts/lower/loose_locals.go:154-178`: a declaration's own array-pattern names are loose for every initializer type but a tuple, with a type parameter counted as its base constraint), plus the two test edits (`run/array_pattern_past_end.pts` additions; the new skipped `run/iterator_result_as_cast.pts`). I also checked what the delta can break. Read-only; nothing in the repo was touched.

**Verdict: two BLOCKERS, both narrow regressions against the base with small fixes, plus one SHOULD-FIX (performance).**
- **R3-B1** is a silent wrong value. A name bound to a map's entry is copied again on every read, so mutations are lost and `e === e` is `false`. The base is correct. Round 2 already had this for map iterators; round 3 extends it to typed `Map`s and generic `T extends Iterable<…>`.
- **R3-B2** is a new PTS1001 on valid code that worked on the base: `+=` on a string that a `let` pattern binds from a string, a `Set<string>` or a map iterator.
- **R3-S1** (performance): names that are now loose cost 2.8-2.9× in a hot loop that reads them (typed arrays since round 2, sets now) and 1.28× for strings. Destructuring a typed array is still 4.4× faster than on the base.
- **R2-S2 is resolved.** Typed `Set`, `Map`, string, typed-array and map-iterator declarations past the end now give `undefined`. The other ~20 probe families (below) match Node, and new and round-2 compilers give identical compile results on all 1,120 `.pts` files in the repo.
- **Test edits.** Both are correct and discriminating.

## Setup

- **CLIs.**
  - *new:* `pts-bin/pts`, built 21:47:54, newer than every edited source (`loose_locals.go` 21:31, `destructuring.go` 21:14).
  - *base:* the `agent-afb5379bc12f20b9e` worktree at `795a36949`. That commit is `7f0a4131a` plus test files only; `git diff 7f0a4131a 795a36949 -- pts/lower pts/runtime pts/cgen pts/ir cmd` is empty.
  - *r2:* my round-2 build, `/tmp/pts-h03-review/pts2`, from the round-2 tree, which differs from new only in `readsElement`.
- **Harness.**
  - `/tmp/pts-h03-review3/tools/cmp3.sh` compares new, base and Node v24 (`--experimental-transform-types`) with `ulimit -v 4194304`, `ulimit -f 40960` and a 60 s run timeout, one job at a time.
  - `tools/one.sh <new|base|r2>` builds and runs a probe with one CLI; `tools/emit.sh` emits its C.
  - Outputs are under `out/` and `out1/`.
- **Whole-repo compile sweep.** `tools/emit_all.sh` ran `emit-c` with new, r2 and base over all 1,120 `.pts` files in the repo (tests, corpus, docs examples, review probes); results are in `emitall/{new,r2,base}/index.tsv`.
  - No file that compiles on the base or r2 fails on new.
  - The set of panics on new equals r2's (only the pre-existing `p08d_panic_propertykey`, C-ledgered); the base has 11, which the slice fixes.
  - Only 6 files' C changed between r2 and new: `corpus/functional/state_monad_rng`, `run/any_iteration`, `run/array_pattern_past_end`, `run/destructuring`, `run/iterables_and_map_iterators` and `run/map_entries`. All 6 build and print Node's exact output. In `state_monad_rng` the change is only a renumbering of tuple shapes, because `readsElement` no longer calls `irType`.
- **No `go test`.** I didn't run it, since the suite is running on the host.

---

## BLOCKER

### R3-B1. A loose name bound to a map's entry is copied again on every read: mutations are lost, and `e === e` is `false`

`/tmp/pts-h03-review3/probes/min/min_b1_map_entry.pts`:
```ts
const m = new Map([["a", 1]]);
const [e] = m;
e[1] = 5;
console.log(e, e === e);
const [g] = m.entries();
g[1] = 6;
console.log(g, g === g);
```

| | output |
|---|---|
| Node | `[ 'a', 5 ] true` / `[ 'a', 6 ] true` |
| base | same as Node |
| r2 | `[ 'a', 5 ] true` / **`[ 'a', 1 ] false`** |
| new | **`[ 'a', 1 ] false`** / **`[ 'a', 1 ] false`** (exit 0, silent) |

**Cause** (verified in the emitted C, `out1/new/r14_tuple_names.c`):
- The map's entries reach the pattern as runtime arrays: `ValueToArray` for a `Map`, and iteration items for iterators and iterables.
- A loose name's local is `[string, number] | undefined`. `convert` stores a dynamic value into that union raw, via `lower.go:938`: `case isUnion(target) && value.Type() == ir.Value: return &ir.ToUnion{…}`. The result is `pts_value e = pts_array_element(destructured, 0.0);`.
- Every narrowed read is then `pts_value_convert(e, &pts_check_1)`, which copies the array into a fresh tuple object. So `e[1] += 10`, `e[1]++`, `alias[1] = 99`, `bump(e)` and `e === e` all act on throwaway copies.
- The base converted the element once, at the binding: `pts_class_tuple1 *e = …pts_value_convert(PTS_ARRAY_GET_WORD(…), &pts_check_1)`.

**Reach** (all exit 0, silent; base matches Node in every row):

| Source | First wrong in |
|---|---|
| a typed `Map` (`const [e] = m`) | new (round 3) |
| `m.entries()`, `m[Symbol.iterator]()` | round 2 |
| an `Iterable<[string, number]>` holder of a `Map` | round 2 |
| generic `<T extends Iterable<[string, number]>>(x: T)` called with a `Map` | new (r2 was right here) |

Repros: `probes/r3/r15_map_entry_identity.pts` (mutation, alias, `[e]` in an array, a mutating callee, the iterator) and `probes/r3/r19_entry_paths.pts`. The same probe kinds over sets of tuples, generator-yielded tuples, `Object.entries`, interfaces, shapes, functions and `Array.from(m)`/`[...m]` keep identity, on the base and on new (`r16_identity_kinds.pts`, `r17_entries_arrays.pts`). Map entries are the case whose runtime representation (an array) differs from the static one (a tuple class).

**Fix.** When a loose name's item is a dynamic value and its type `T` isn't checkable (a tuple; cheaply, any non-`isCheckable` `T`), bind `item === undefined ? undefined : ToUnion(convert(item, T))` instead of the raw `ToUnion`. Either do it in `bindName`/`elementPart`'s loose path and in `destructureIterable`'s binding, or let `convert` (Value → union) do a checked conversion when the union has a tuple member.

Add `min_b1_map_entry.pts` as a run test; its expected output is Node's, above.

### R3-B2. `+=` on a string bound by a `let` pattern from a typed string or set is now PTS1001

`/tmp/pts-h03-review3/probes/min/min_b2_concat.pts`:
```ts
let [first, second] = "ab";
first += second;
let [w] = new Set(["s"]);
w += "!";
console.log(first, w);
```

| | result |
|---|---|
| Node | `ab s!` |
| base | `ab s!`, exit 0 |
| r2 | `ab s!`, exit 0 |
| new | build fails: `3:1 - error PTS1001: the \`+=\` operator on string and string is not supported yet` (and at 5:1) |

`probes/r3/r10d_compound_string_inrange.pts` adds two cases, all in range:
- a map-values iterator, which r2 already rejects;
- a function's `let [first, second] = text; first += second;`.

The base runs the whole file like Node; new gives four PTS1001s. A `for (let [i, j] = "ab"; …; i += "+")` head fails the same way (`r09`, first version).

**Cause.** In `compoundAssignment`, `lower.go:2848`:
```go
case operator == ast.KindPlusToken && t == ir.String:
```
`t` is `target.valueType()`, the loose local's `string | undefined`, so concatenation falls through to `binaryOps`/`operation`, which has no string `+`. This is a pre-existing gap for loose names: `let [i, j] = "a b".split(" "); i += "+"` fails identically on the base (`r10b`). The slice now exposes it for strings, sets, maps and iterators.

Numeric compound operators (`+= -= **= ||= ??=`, `++`), plain reassignment, `s = s + i` in a loop and `s = s.repeat(2)` all work (`r10`, `r11`, `r14`).

**Fix.** Test the narrowed read instead, e.g. `t == ir.String || current.Type() == ir.String`. `value` is then converted to `t`, the union, as now. That also fixes the base's array case. Add `min_b2_concat.pts` as a run test.

---

## SHOULD-FIX

### R3-S1. Performance: newly loose names are boxed, and every typed read calls the out-of-line `pts_value_to_number_checked`; typed-array names are also `volatile`

All runs used `PTS_THREADS=1 hyperfine -N -w 1 -r 5`, alternating the order (base/new, then new/base); binaries are under `out1/{base,new,r2}`.

**Destructuring dominates.** Programs: `bench/b_typed.pts`, `b_string.pts`, `b_set.pts`.

| Program | base | new |
|---|---|---|
| 20M × `const [a, b] = float64Array` | 3.14 s | **0.71 s (4.4× faster)**; no array copy; same C as r2 |
| 10M × `const [a, b] = words[i % 3]` (string) | 1.28 s | 1.47 s (**1.15× slower**; callgrind +16.4% instructions, 1.085 G → 1.263 G) |
| 10M × `const [a, b] = set` | 1.98 s | 2.13 s (1.06-1.10× slower; callgrind +7.3%, 2.073 G → 2.226 G) |
| 20M × `len(v)`, which runs `const [x, y, z] = v` on a `Float64Array` (`v_len.pts`) | 3.22 s | **1.19 s (2.7× faster)** |
| 5M × the same over a `Set<number>` (`v_set_len.pts`) | 1.005 s | 1.110 s (1.10× slower) |

The string and set costs come from `pts_array_element` plus `pts_value_of_slot`, about 70 instructions per element, instead of `PTS_ARRAY_GET_WORD`, plus the boxed checks.

**Reading the names dominates: one destructure, then 100M reads** (`u_typed.pts`, `u_set.pts`, `u_array.pts`, and `u_string.pts` with 30M reads):

| Source | base | r2 | new |
|---|---|---|---|
| `Float64Array` | 120 ms | 337 ms | **340 ms (2.8×)** |
| `Set<number>` | 120 ms | 120 ms | **347 ms (2.9×)** |
| string | 157 ms | 157 ms | 201 ms (1.28×) |
| `number[]` (loose on the base already) | 359 ms | n/a | 359 ms |

**Cause.** A loose `number` is a `pts_value`. Each read is a call to `pts_value_to_number_checked` (`string.c:453`, declared at `pts.h:954`), which clang can't hoist out of the loop. For typed arrays and other iterator-path sources, the names are also `volatile`, from `destructureIterable`'s `_setjmp` protection. That protection is always emitted for typed element types, because the Value → `number | undefined` check counts as "may throw" (`destructuring.go:130`). Yet a typed array, array or string source can neither fail that check nor need closing.

**Remedy experiment.** I rewrote only the program's C through a `PTS_CC` wrapper (`tools/cc-inline.sh`) to add an inline fast path, `if (pts_value_is_number(v)) return pts_value_as_number(v);`, before the call:
- `u_set`: 347 ms → **135 ms**;
- `u_typed`: 334 ms → 238 ms (the `volatile` loads remain).

**Suggestion.**
1. Put that fast path in `pts.h`. It also helps the base's array-sourced loose names, which cost the same 359 ms.
2. Skip the handler for index-cursor sources (typed arrays, arrays, strings), so their names aren't `volatile`.
3. At least record these costs next to D-177's measured numbers, which currently list only the wins.

The cost model itself is D-172's, so this isn't a blocker.

---

## NITS

- **R3-N1.** `readsElement` calls `GetBaseConstraintOfType(t)` for every initializer type, in the `if`'s init statement, before testing `TypeFlagsTypeParameter`. For unions and intersections that resolves a base constraint for nothing. Test the flag first. `loose_locals.go:173`.
- **R3-N2.** A union of tuples (e.g. `flag ? k1 : k2` of `as const` tuples) is not `IsTupleType`, so its names become loose and boxed. The output is right (`r01_tuples.pts`), but there is an extra check on every read (`out1/new/r01_tuples.c`, `e1`/`e2`). Treating a union whose members are all tuples of at least the pattern's length as a tuple would avoid it.
- **R3-N3.** Stale comment: `isLoose`'s doc (`loose_locals.go:130`) still says "that an array pattern binds from an array"; the file header (lines 10-16) is similar.

## Test edits (both OK)

- **`run/array_pattern_past_end.pts`.**
  - New prints exactly its expect block, which equals a fresh Node run.
  - On the base it fails at its first line (any array).
  - Each new line run alone (`probes/pe/pe_*.pts`):
    - Set, Map and string fail on both the base and r2 with `RangeError: Array index 1 is out of range`. They discriminate R2-S2's fix.
    - Float64Array and map keys fail on the base and pass on r2, so they guard round 2's fix.
- **`run/iterator_result_as_cast.pts`.** The expect block equals Node's. On new and base the first case fails (`TypeError: Expected number, got function`, `:11`). Its second case alone (`probes/t_as_cast_extras.pts`) is SIGSEGV (exit 139) on both. So once unskipped, the test covers both halves of C35. `// skip: known issue (H03b, C35): …` is a valid run-test skip; categories are enforced only for the corpus.
- **Missing coverage.** Neither test covers R3-B1 or R3-B2. Only 3-4 suite files change loose-ness between r2 and new, so add the two minimal repros above as run tests.

## Verified OK (new matches Node; base fails only where a name is past the end)

| Area | Probes (`probes/r3/`) |
|---|---|
| Tuple-typed sources stay non-loose and correct: a conditional over equal tuple types, readonly tuples, optional-element tuples, a function returning a tuple, nested tuples in arrays, `Object.entries` | `r01` |
| Generics: `T extends number[]`, a tuple, `readonly string[]`, `Iterable<number>` (with Set, array and `Float64Array` instances), `S extends string`; intersections `number[] & {…}` and a branded tuple; `readonly number[]` and `ReadonlyArray`; tuple instances of array/iterable constraints when long enough | `r02`, `r03b`, `r12` |
| Defaults, rest, nested patterns, elisions; class, shape, boolean, `Float64Array`, `Int32Array`, string, map and map-values elements; closures (also per iteration); `let` reassignment with numeric compound operators; templates, shorthand, spread, `switch`, `typeof` | `r04` |
| Element types: unions, nullable, literal unions, enums, functions, `any`, `unknown`, `T \| undefined`, arrays, tuples, sets, `Date`, `RegExp`, `Error`, surrogate pairs, `""` | `r05`, `r12` |
| Generators, async functions, async generators, methods, static methods, `try`/`catch`/`finally`, `switch` blocks; `await` and `yield` initializers | `r06`, `r21` |
| `parallel for` and `.par.map` capturing loose numbers, strings, `Float64Array`s, class instances, arrays and maps, and declaring loose names inside the parallel body (no Node; values checked by hand: `[6,8,10,12]`, `[5,7,9,11]`, `['p1q',…]`, `[101,113,125,137]`, `[1,2,3,4]`, `[16,17,18,19]`, `[9,10]`) | `r07`, `r13` |
| Annotated patterns (`number[]` and `[number, number]` from `any`, `Iterable<string>` from a Set, `readonly number[]`); `for (let [i, j] = new Set(...); …)` with closures; several declarators | `r09` (final version) |
| Comparisons, arithmetic, `instanceof`/`in`, field stores and compound stores through loose class, typed-array, array and map names, closure assignment, indexing, generics, spread arguments, iteration, re-destructuring | `r11` |
| Undefined tests: `===`, `!`, `?.`, `??`, `\|\|`, `typeof`, optional parameters, falsy `0`/`false` elements | `r20` |
| Exported loose names across modules, including reassignment from the defining module | `probes/mod/` (all lines but the last; see the pre-existing notes below) |

## Pre-existing, not caused by the slice (base identical)

- **`?? fallback` of another type, in a function whose return type tsc infers from the name.** `const f = () => b2 ?? 0`, with `b2: string` bound past the end, throws `TypeError: Expected string, got number`. Node gives `0`. Same for array sources on the base (`r08`, `r08b`, `probes/mod/main.pts:6`). This is D-090's checked-as-T rule; the base's RangeError text even suggests `?? fallback`.
- **A tuple instance of `T extends number[]`/`Iterable<number>` destructured past its end.** It reports a misleading `PTS1001: this rest element is not supported yet`, plus ``using `b` ``, from `elementPart` → `tupleRest` (`r03`, `r09` line 8 of the first version).
- **Other unsupported forms.** `case undefined:` on a `string` is PTS1001 (`r20`, first version). `[string, ...number[]]` patterns and patterns over a union of differently long tuples are PTS1001 (`r01`, first version).

---

# H03a review, round 4 (opus-reviewer, claude-opus-5-5, xhigh), 2026-10-15

Scope: the parent's round-3 fixes on the uncommitted tree against base `7f0a4131a`, and what they can break:
- `looseItem` in `bindName` (R3-B1);
- the `+=` condition in `compoundAssignment` (R3-B2);
- the inline `pts_value_to_number_checked` and the typed-array handler skip (R3-S1);
- the R3-N1 and R3-N3 nits;
- the two new run tests.

I also re-ran round 3's probe set and swept the whole repo's compile results. This was a read-only review: nothing in the repo was touched, and scratch files are in `/tmp/pts-h03-review4/`.

**Verdict: the round-3 fixes work as described, and both new tests are correct and discriminating. But the slice still has one BLOCKER and three SHOULD-FIXes. Each has a small fix, and I verified all four fixes in an experimental build.**
- **R4-B1 (BLOCKER).** A loose name of a 64-bit integer type (`int`/`i64`/`u64`) loses precision beyond 2^53 when bound from a `Set`, a map's keys, a generator or another iterator, and `===` against the original is `false`. The base is exact for these sources. This is not caused by the round-3 fixes: it is the round-2/3 broadening of loose names meeting a pre-existing bug in `checkedConversion` (the base already loses precision for `int[]` sources). The fix is one line.
- **R4-S1.** R3-B1's fix doesn't cover a union element type that has a tuple member (`Iterable<[string, number] | null>` holding a `Map`). Writes and identity are still lost, as in round 3. The base gives memory-unsafe garbage here, so this is no regression; it is an incomplete fix, and D-177 claims more than the code does.
- **R4-S2 (performance).** The binding-time conversion makes a destructuring-heavy loop 1.7–1.9× slower than the base when the name's type is a shape, an array or a map entry.
- **R4-S3.** The R3-B2 change turns some former PTS1001s into silent wrong values. `x += obj`, where `obj` has a `valueOf`, now compiles for `string | number`, `any`/`unknown`-narrowed and loose string places, and it uses `toString`, not `valueOf`. That bug already existed for plain `string` places; binary `+` is right.
- **Confirmed.**
  - R3-B1's fix works for every non-union source and type I tried: typed `Map`, map iterators, `Iterable` holders, generics, `any`, sets of tuples, arrays, shapes, interfaces, maps and functions, generators, defaults, rest, nested patterns, `for (let …)` heads with closures, generator, async and async-generator bodies, parallel code and module exports. The conversion runs inside `destructureIterable`'s protected region, so the iterator is closed when it throws.
  - R3-B2's fix works and covers more than loose locals.
  - The parent's benchmark numbers reproduce within ±0.01×.
  - The typed-array handler skip is safe: typed arrays can't be subclassed in PTS (PTS1001), so nothing can override `[Symbol.iterator]`.
  - No repo file that compiled in round 3 (or on the base) fails now.

## Setup

- **CLIs.**
  - *new:* `pts-bin/pts`, built 22:21:01, sha256 `f2566013d179a0f5…`. No `.go`, `.c` or `.h` file under `pts/` or `cmd/` is newer.
  - *base:* the `agent-afb5379bc12f20b9e` worktree, as in round 3.
  - *r3:* `/tmp/pts-h03-review4/r3build/pts-r3`. This is the current tree built with `go build -overlay`, with the three round-3 lowering hunks reverted: the `looseItem` call, the `+=` condition and the typed-array skip (`r3build/{d,l}.go`, `overlay.json`). It uses the current runtime through `PTS_HOME`.
  - *r2:* `/tmp/pts-h03-review/pts2`.
  - *fix:* `/tmp/pts-h03-review4/fixbuild/pts-fix`, the current tree plus my three experimental fixes (`fixbuild/{d,l}.go`, marked `REVIEW-EXPERIMENT`; described under each finding).
- **Harness.**
  - `tools/cmp4.sh` builds and runs a probe with new and base (`R3=1`/`R2=1` add those CLIs) and with Node v24 (`--experimental-transform-types`), under `ulimit -v 4194304`, `ulimit -f 40960` and a 60 s timeout, one job at a time.
  - `tools/one.sh <new|base|r2|r3|fix>` runs one CLI; outputs are in `out/<name>/` and `out1/<cli>/<name>/`.
- **No `go test`, and no rebuild of `pts-bin/pts`.** The parent's suite (`/tmp/pts-h03/final2.exit`: `exit=0`) finished at 22:36, before my benchmarks, which ran on an idle host (load average 0.4–1.0, `pgrep -x go` empty).

---

## BLOCKER

### R4-B1. Loose `int`/`i64`/`u64` names lose precision beyond 2^53 for sets, map keys, generators and iterators

`/tmp/pts-h03-review4/probes/min/min_r4_int64.pts`:
```ts
const big: int = 9007199254740993;
const [a] = new Set<int>([big]);
console.log("Set<int>", a, a === big);
function* g(): Generator<int> { yield big; }
const [b] = g();
console.log("Generator<int>", b, b === big);
const [k] = new Map<int, string>([[big, "v"]]).keys();
console.log("map keys", k, k === big);
const ub: u64 = 9007199254740993;
const [u] = new Set<u64>([ub]);
console.log("Set<u64>", u, u === ub);
const arr: int[] = [big];
const [c] = arr;
console.log("int[] (loose on the base too)", c, c === big);
```

| line | base | r2 | r3 and new | fix |
|---|---|---|---|---|
| `Set<int>` | `9007199254740993 true` | `…993 true` | **`9007199254740992 false`** | `…993 true` |
| `Generator<int>` | `…993 true` | **`…992 false`** | **`…992 false`** | `…993 true` |
| map keys | `…993 true` | **`…992 false`** | **`…992 false`** | `…993 true` |
| `Set<u64>` | `…993 true` | `…993 true` | **`…992 false`** | `…993 true` |
| `int[]` | **`…992 false`** (pre-existing) | same | same | `…993 true` |

All runs exit 0 with no message. Node can't tell these apart, because its numbers are doubles. The PTS semantics are SPEC §3 and D-012: "64-bit integers keep every digit", and an `int` beyond 2^53 is boxed exactly as a dynamic value. The base does keep every digit for every source but `int[]`.

**Cause** (verified in the C, `out1/new/s1_int.c:138`):
- A loose `int | undefined` local holds the boxed value correctly: `pts_value_is_integer_of(…, PTS_FIELD_I64)` is checked at the binding.
- Every narrowed read is then `pts_number_to_i64(pts_value_to_number_checked(c))`, which goes through a double. The read comes from `checkedConversion` (`pts/lower/lower.go:2247-2248`):
  ```go
  case ir.IsNumeric(target) && target != ir.Number:
      return &ir.Convert{X: &ir.FromUnion{X: box(x), To: ir.Number}, To: target}
  ```
- That path was reached on the base only by array-sourced loose names and keyed elements. The slice makes names from sets and maps (round 3) and from iterators and generators (round 2) loose, so they now take it too.

**Fix (verified in *fix*).** Leave 64-bit targets to the checked dynamic conversion, which `cgen.fromValue` already does exactly (`pts_value_is_integer_of` + `pts_value_as_i64`/`as_u64`):
```go
case ir.IsNumeric(target) && target != ir.Number && target != ir.I64 && target != ir.U64:
```
With it, every line above is exact, and so are the existing `int[]` case and the `int set` line of `probes/s1/s1_tonumber.pts`. The other probes give identical output (list under R4-S1). Add `min_r4_int64.pts` as a run test. Its expect block is the *fix* column, PTS's own output; it is not a Node-differential case.

---

## SHOULD-FIX

### R4-S1. R3-B1's fix doesn't cover a union element type with a tuple member

`/tmp/pts-h03-review4/probes/min/min_r4_union_entry.pts`:
```ts
const m = new Map([["a", 1]]);
const it: Iterable<[string, number] | null> = m;
const [e] = it;
if (e) { e[1] = 5; console.log("tuple|null", e, e === e); }
const it2: Iterable<[string, number] | string> = m;
const [f] = it2;
if (typeof f !== "string") { f[1] = 6; console.log("tuple|string", f, f === f); }
```

| | output |
|---|---|
| Node | `[ 'a', 5 ] true` / `[ 'a', 6 ] true` |
| new, r3 | **`[ 'a', 1 ] false`** / **`[ 'a', 1 ] false`** (exit 0, silent) |
| base | **memory-unsafe:** `[ 'a', 1, undefined, … ... 4617315517961600924 more items ] true` (the entry array is reinterpreted as a tuple) |
| fix | same as Node |

**Cause** (`out1/new/b1_union_entry.c`).
- `looseItem` (`pts/lower/destructuring.go:277`) converts with `l.convert(ref, T)`. For a union `T`, Value → union is `convert`'s raw `ToUnion` (`lower.go:938`), so the "conversion once" is a no-op: `e = ({ pts_value item_2 = item; (item_2 == PTS_VALUE_UNDEFINED) ? PTS_VALUE_UNDEFINED : item_2; })`.
- Each narrowed read is then `pts_value_convert(e, &pts_check_1)`, a fresh tuple every time, as in round 3.
- D-177 says "holds the item converted to that type once … (a tuple, array, shape or interface: not `isCheckable`)". That isn't true for unions.

**Fix (verified in *fix*).** For a union `T`, use the checked dynamic conversion, which converts the array member into a tuple once:
```go
if t := l.withoutUndefined(union); isUnion(t) {
    present = l.dynamicConversion(ref, t, node)
} else {
    present = l.convert(ref, t, node)
}
```
*fix* then prints Node's output here. It is identical to new on:
- both new tests;
- `b1_sets`, `b1_gen`, `b1_patterns`, `b1_types`, `b1_exotic`, `b2_ops`, `s1_typed` and `close_conv2`;
- `s1_tonumber`, apart from R4-B1's line.

I did not run the suite on it. Add the probe as a run test, with Node's expect block.

### R4-S2. Performance: the binding-time conversion costs 1.7–1.9× in destructuring-heavy loops

Programs are in `/tmp/pts-h03-review4/bench/`. I ran `PTS_THREADS=1 hyperfine -N -w 1 -r 5` in both orders and averaged the medians; all outputs are equal.

| Program | base | new | new/base |
|---|---|---|---|
| `w_rows.pts`: 5M × `const [r] = rows; acc += r[i % 1000]` (`rows: number[][]`) | 528 ms | 968 ms | **1.83×** |
| `w_shapes.pts`: 20M × `const [p] = pts; acc += p.x + i` (array of `{x, y, name}`) | 1799 ms | 3400 ms | **1.89×** |
| `w_entries.pts`: 2M × `const [e] = m; acc += e[1] + e[1] + e[1]` (typed `Map`) | 712 ms | 1222 ms | **1.72×** |

- **Array sources** (loose on the base too, stored raw).
  - new now runs `pts_value_convert` at the binding, then again at every narrowed read. That is about 80–90 ns per conversion for a 3-field shape.
  - r3 had the base's cost here; this part comes from the R3-B1 fix.
  - A typed `T[]` array's elements already are `T` objects, so converting them can't change identity. The copy that R3-B1 is about happens only for items that are dynamic: map entries, `Value` arrays and iteration items.
- **Map sources.** The base converted each entry once and then read an unboxed tuple. new converts at the binding and again at each read.

Options, cheapest first:
- apply `looseItem` only where the item can be a representation that differs from its type: `destructureIterable` items, and elements of an `*ir.Array` whose `Elem` is `ir.Value` (`ValueToArray` of a map or set). Typed arrays then keep the base's cost.
- since a `looseItem`-bound local holds either undefined or a value already of type `T`, let its narrowed reads use an undefined test and an unbox, not the full dynamic conversion. That makes reads cheaper than the base too, but every other way of assigning the local must keep the invariant.

At minimum, record these numbers next to D-177's measured costs, which list only the number reads.

### R4-S3. `+=` concatenation uses ToString where `+` needs ToPrimitive (default hint); the R3-B2 change turns some PTS1001s into silent wrong values

`/tmp/pts-h03-review4/probes/min/min_r4_valueof.pts` (excerpt; `V` has `valueOf(): 7` and `toString(): "T"`):
```ts
let a: any = "q";
if (typeof a === "string") { a += new V(); }
let u: string | number = "u";
u += new V();
let s = "s";
s += new V();
let [w] = "w";
w += new V();
```

| place | Node | base | new | fix |
|---|---|---|---|---|
| `any` narrowed to string | `q7` | PTS1001 | **`qT`** | `q7` |
| `string \| number` narrowed by assignment | `u7` | PTS1001 | **`uT`** | `u7` |
| plain `string` local | `s7` | **`sT`** (pre-existing) | **`sT`** | `s7` |
| loose string (`let [w] = "w"`) | `w7` | **`wT`** (not loose on the base) | **`wT`** | `w7` |
| `o.s` of an `any` object | `x7` | `x7` | `x7` | `x7` |

- **Binary `+` is right on both.** `s + new V()` prints `s7` (`b2_valueof_binary.pts`): `concatenation` (`lower.go:3446`) checks `hasValueOf` and uses `ValueAdd`.
- **The compound branch doesn't.** `compoundAssignment` (`lower.go:2848-2852`) always calls `l.toString`, so the new condition sends union and `any`/`unknown` places that the base rejected down the wrong path.

**Fix (verified in *fix*).** In that branch, `if hasValueOf(y.Type())`, use `checkedConversion(ValueAdd(box(current), box(y)), ir.String, node)`, as `concatenation` does. Add the probe as a run test, with Node's expect block.

---

## NITS

- **R4-N1.** `docs/SPEC.md:1001` still lists `pts_value_to_number_checked` under `string.c`. It is now inline in `pts.h`, and its slow path is `pts_value_to_number_slow` in `string.c`. The same line already listed `pts_value_as_string`, which is inline, under `string.c`.
- **R4-N2.** The edited file header in `pts/lower/loose_locals.go:13-14` breaks its comment wrap: line 14 is 120 columns, while the rest of that paragraph wraps at about 90.
- **R4-N3.** For a union `T`, `looseItem` currently emits a `Bind` and a conditional that do nothing (`e = item == undefined ? undefined : item`). R4-S1's fix gives them a purpose.

## R3 fixes: verified

### R3-B1: identity through loose names (`looseItem`)

Probes are in `probes/b1/`. new matches Node on every line except R4-S1's union cases. r3 differs where noted, which shows each probe discriminates.

| Probe | What | r3 |
|---|---|---|
| `array_pattern_loose_identity.pts` (the new test) | typed Map, `entries()`, `Iterable` holder, generic, `[Symbol.iterator]()`, nested array value, past the end | 8 lines wrong |
| `b1_sets` | sets of tuples, arrays, shapes, interfaces, maps, sets and functions; `tuple \| string`, `number[] \| null` and `Map \| undefined` elements; `Date`; past the end; nested tuple (`=== original` too) | same as Node (these keep identity via `pts_value_convert` of real objects) |
| `b1_any` | `any as Map`, an `any` assigned to an `Iterable` | wrong (`false`, writes lost) |
| `b1_gen` | generator-yielded tuples and objects; loose names in generator, async and async-generator bodies across `yield`/`await` | 7 lines wrong |
| `b1_patterns` | defaults (taken and not), rest, nested `[[k, v]]`, `for (let [e] = m …)` and `for (let [e] = m.entries() …)` with closures, `let` reassignment | 4 lines wrong |
| `b1_types` | variadic, optional and readonly tuples, `object`, `Record`, `Date \| tuple`, `Promise`, branded tuple, generic `T`, arrays of arrays of tuples, nested deep, class, typed-array element | same as Node |
| `b1_exotic` | generator, array and map iterators, `RegExp`, `Error`, `Uint8Array`, functions, `fn \| undefined`, arrays, map of arrays (nested and entry) | 1 line wrong |
| `b1_set_of_entries`, `conv/conv_arrays`, `conv/conv_holes` | `new Set(m)`, `Array.from(m)`; intersections, `Readonly`, `Partial`, `object`, `{}`, `Iterable`, `Record`, nested shapes; holes | same as Node (holes: pre-existing `number` hole reads `0`, as on the base) |
| `probes/mod/{lib,main}.pts` | exported `const [ent, ent2, ent3] = m`, `export let [lent] = m.entries()` mutated and reassigned from both modules, exported loose string `+=` | r3: PTS1001 |
| `probes/par/par_tuple.pts` | map-entry names captured by `parallel for`, declared in its body and in `.par.map` | wrong (`[0,1,2,3]`, `[1,2,3,4]`); new `[4,5,6,7]`, `[10,11,12,13]`, `[1003,1006,1009,1012]`, as computed by hand |

**Protected region** (`probes/close/close_conv2.pts`): a `Generator<any>` held as an `Iterable<[string, number]>`, `Iterable<N>` (an interface) or `Iterable<number[]>`, whose item has the wrong type.
- new prints `ga finally` (the generator is closed) and then `threw Expected [string, number], got number`, and likewise for the interface and array cases.
- In the C, the `looseItem` binding is inside the `_setjmp` region (`out1/new/array_pattern_loose_identity.c:194`).

**Throw at binding instead of at first use: yes, but the base does the same.**
- For type-unsound `any` data, new throws at the binding: `close_conv2` and `b1_any` (`Expected [string, number], got number`).
- r3 threw lazily and so matched Node, which never throws.
- The base throws at the binding with the same messages, because its names weren't loose for these sources. This restores the base's D-070 behaviour; it is not a new regression.
- A sound program can't hit it: typed arrays and sets hold converted values.

### R3-B2: `+=` on a narrowed string

Probes are in `probes/b2/`.

- **Tests and round-3 probes.** `array_pattern_loose_concat.pts` matches Node; the base and r3 fail to build. `r10`, `r10b`, `r10c` and `r10d` match Node now.
- **`b2_ops`.** new matches Node on:
  - `string | number` narrowed by assignment, with number, template, boolean, `1.5`, `-0`, `NaN`, `1e21`, `any` array and `null` right sides;
  - `string | undefined`; `any` and `unknown` narrowed by `typeof`; an `any` property;
  - `Record<string, string | number>` element (`.k` and `["k"]`) narrowed by assignment; a class field `string | number`.

  The base gives six PTS1001s; only the `any`-property case built on the base, and it prints the same.
- **`b2_order`.** The left side is read before the right side runs, for union, loose and string locals.
- **`b2_yield`.** `v += yield 1`, loose `w += yield 2` and `+= await …` match Node; the base gives PTS1001 on the async case.
- **`b2_past_end`.** On a loose name past the end, `+=` throws a catchable `TypeError: Expected string, got undefined`, per D-090. Node gives `undefined!` and `NaN`. The base threw a RangeError at the binding.

### R3-S1: inline fast path and typed-array handler skip

- **`s1_tonumber`.** new equals the base on:
  - `int` and `i64` beyond 2^53, `u64` max, `-0`, `NaN`, `±Infinity`, `1.5` and `0` through `any → number`;
  - the six non-numbers, with the same `TypeError: Expected number, got string/boolean/null/undefined/Object/Array` texts;
  - `-0`, `NaN` and `Infinity` from loose set names and `Float64Array` names.

  The only differences are R4-B1's line and a pre-existing `Set` `-0` difference (below).
- **`s1_typed`.** It matches Node; r3 does too, and the base fails past the end. It covers:
  - a throwing default, caught;
  - three defaults, of which only the missing one runs;
  - rest into a real array; an `any` typed array, raw and `as Float32Array`;
  - generic `T extends Float64Array`;
  - names across `yield` and `await`;
  - a default that throws inside `try`/`catch`/`finally`, with `break` and `continue`;
  - `for (let [v = 0] = f64 …)` with closures; elisions.
- **The generated C.**
  - `u_typed`: 0 `volatile`, 0 `_setjmp`, against r3's 6 and 2.
  - The handler stays for `any` values that are typed arrays at run time, since their static type isn't a typed array.
- **`s1_subclass`.** `class F extends Float64Array` is `PTS1001: extending Float64Array is not supported yet`, so no typed array can have its own iterator to close.
- **Benchmarks** (`bench/`, both orders averaged; the parent's numbers in parentheses):

| | base | new | new/base |
|---|---|---|---|
| u_set | 120 ms | 134 ms | 1.12× (1.12×) |
| u_typed | 120 | 126 | 1.05× (1.05×) |
| u_string | 157 | 197 | 1.25× (1.25×) |
| u_array | 359 | 127 | 0.35× (0.35×) |
| b_set | 1917 | 2008 | 1.05× (1.04×) |
| b_string | 1262 | 1438 | 1.14× (1.13×) |
| b_typed | 3214 | 587 | 0.18× (0.19×) |
| v_len | 3292 | 845 | 0.26× (0.26×) |
| v_set_len | 1031 | 1084 | 1.05× (1.04×) |

### Nits R3-N1 and R3-N3

Both are done. `readsElement` now tests `TypeFlagsTypeParameter` before `GetBaseConstraintOfType`, and the comments are updated. R3-N2 was not changed, as the parent said.

### Whole-repo compile sweep

`tools/emit_all.sh new` ran over all 1,122 `.pts` files, and I compared the result with round 3's `emitall/{new,base}`.
- **Exit codes.** No file that compiled on r3 or on the base fails now, and nothing newly compiles.
- **Panics.** The only exit-2 file is `run/keyed_assign_value.pts`, skipped as C26/H04, with the same panic on base, r2 and r3.
- **Changed C.** The generated C changed in 12 files: the two new tests plus 10 existing files. I built and ran all 10 existing ones with new:
  - the 9 corpus and run files match Node, except `corpus/data/matrix_transpose_table`, which is `// skip: by design: D-038`; the base gives the same RangeError there;
  - the docs probe `opus/review-opus-probes/programs/csvq/csvq.pts` imports `pts:fs`, so Node can't run it. Instead I ran every line of its `cmds.txt` with new and with the base: all 118 output lines and exit codes are identical.
- **Round-3 probes.** Re-run on new: every one that matched Node in round 3 still does. The `r10*`, `r14`, `r15`, `r19`, `min_b1` and `min_b2` probes changed and now match Node. `r03`, `r07`, `r08`, `r08b` and `r13` are as in round 3: pre-existing, or Node can't run them.

## New tests

- **`run/array_pattern_loose_identity.pts`.**
  - Its expect block equals a fresh Node run, and new prints it exactly.
  - It discriminates: r3 (the round-3 lowering) gets 8 of its 10 lines wrong (`mutated [ 'a', 1 ] 1`, `alias 1 false false`, …).
  - The base fails only its last line (RangeError past the end), which matches `h03.md`'s description.
- **`run/array_pattern_loose_concat.pts`.**
  - Its expect block equals Node's, and new matches it.
  - On the base it fails to build: PTS1001 at lines 15 and 16, the array case.
  - With r3 it fails to build with six PTS1001s (lines 5, 9, 12, 15, 16 and 26).
- **Missing coverage.** Neither test covers R4-B1, R4-S1 or R4-S3. Add `probes/min/min_r4_int64.pts`, `min_r4_union_entry.pts` and `min_r4_valueof.pts` once they are fixed.

## Pre-existing, not caused by the slice (base identical)

- **Memory safety.** A function's parameter pattern over a `Map` passed as `Iterable<[string, number] | null>` segfaults (exit 139) on the base, r3 and new: `probes/min/pre_param_union_segfault.pts`.
  ```ts
  function f([e]: Iterable<[string, number] | null>) { if (e) console.log(e.length, e[0], e[1]); }
  f(new Map([["a", 1]]));
  ```
  - The cause is the same as R4-S1's base garbage: a map entry, which is an array at run time, is narrowed to a tuple by an unchecked cast.
  - Declarations no longer hit it, because their names are loose now. Parameter patterns, which aren't loose, still do.
  - It needs a ledger entry.
- **D-090's checked-as-T rule.** `return x` of a loose name past the end, where the return type is `string | undefined`, throws `TypeError: Expected string, got undefined`; Node returns `undefined` (`probes/misc/ret_undef.pts`). The base does the same for arrays; the slice adds sets and strings, where the base threw a RangeError at the binding. This is the same family as round 3's `?? fallback` note.
- **`Set` and `-0`.** `new Set([-0])` keeps `-0`; JS's `Set` normalizes it to `+0` (`s1_tonumber`, `set loose` line).
- **`Set<u64>`.** `new Set<u64>([18446744073709551615])` throws `TypeError: Expected u64[], got Array` (`probes/s1/s1_int.pts:11`).
- **Holes.** `new Array<number>(1)`'s hole reads `0`, where Node reads `undefined` (`conv_holes`).
- **Through `any`.** Iterators of maps and sets through `any` are a run-time `TypeError` ("not supported yet"). `as T[]` of an `any` array copies it, so identity with the original is lost (`b1_any`).

---

# H03a review, round 5 (opus-reviewer, claude-opus-5-5, xhigh), 2026-10-15

Scope: the parent's round-4 fixes on the uncommitted tree against base `7f0a4131a`, and what they can break:
- `looseRead` reading `i64`/`u64` with `FromUnion` (R4-B1);
- `bindName` → `patternItem` for every dynamic pattern item except typed-array elements (R4-S1, the parameter-pattern segfault C37, R4-S2). This widens the binding-time conversion to non-loose union names: parameter patterns, `for…of` bindings, nested and rest patterns, and patterns over `any`;
- the `+=` valueOf branch in `compoundAssignment` (R4-S3);
- the R4-N2 rewrap;
- the three new tests.

This was a read-only review: nothing in the repo was touched, and scratch files are in `/tmp/pts-h03-review5/`.

**Verdict: no BLOCKER. All four round-4 fixes work as described, and I found no correctness regression against the base. The widening of item 2 is sound: in about 30 probes it matches Node or the base everywhere, apart from the D-070 checks and the copies of `any` data the base already makes for non-union names. It also fixes two more memory-safety bugs of the C37 family that no test covers. Two SHOULD-FIXes remain, one about performance and one about tests, and there are three nits.**

- **R5-S1 (performance).** Destructuring loops whose names have a union type are 3.6–5.3× slower than the base. `w_entries` is unchanged at 1.73×. Most of the cost is a 4 KB `memset` on every dynamic conversion (`pts/runtime/src/convert.c:507`, and `:262`, `:268`); the slice now runs those conversions at every binding as well as at every read. That memset is pre-existing code. A fix of three lines, verified in an experimental runtime, brings `w_gen_union` to 1.03× the base, `w_param_union` to 0.60× and `w_entries` to 0.95×. `w_set_union` drops from 5.3× to 1.82×.
- **R5-S2 (tests).** Item 2 also fixes two cases that nothing pins:
  - an object pattern over JSON data with a union-typed name: base and pre4 corrupt memory, printing `list [ 1, 2, 2.75 ]` and then a TypeError;
  - an async function's parameter pattern over a `Map`: base and pre4 throw an uncaught `TypeError: Expected [string, number] | null, got Array`.
- **Nits.**
  - R5-N1: a wrong example in the comment of `patternItem`'s fallback.
  - R5-N2: D-177 should name the behaviour change for union-typed names.
  - R5-N3: D-177 should record the costs.
- **Confirmed.**
  - **R4-B1.** 64-bit loose names are exact for sets, map keys, generators, `int[]` and `u64[]`, and in `+=`, `++`, templates, comparisons, calls, `push` and returns. `x as i64` of a dynamic 1.5 still gives `as`'s `1`, unchanged from pre4.
  - **R4-S1 and C37.** The new test and both round-4 minimal probes match Node.
  - **R4-S2.** `w_rows` and `w_shapes` are back at the base: 0.98× and 1.00×.
  - **R4-S3.** It matches Node and evaluates in the right order for locals, unions, fields, elements, records, getters and calls. It also fixes inherited `valueOf` (`x:1`, where base and pre4 gave `x:X`).
  - **Sweeps.**
    - I re-ran all of round 4's probe files (36 programs; `lib.pts` is imported by `main.pts`). Each prints what it printed in round 4, or now matches Node: the five `b1_union_entry*`/`b1_ue3a`/`b2_valueof*` probes, and the three minimal probes that had no Node record. The four `int64` probes are exact now, which Node can't show.
    - The parent's sweep matches the current CLI (spot-checked), and no file's exit code changed.
    - The 9 corpus files whose C changed match Node, except `matrix_transpose_table`, which differs by design (D-038).
    - All 15 `csvq` commands give the same output as on the base.
    - The parent's suite run `final3` (same CLI, sha `48f89ff2…`) has 1,343 passes, 0 failures and 260 skips.

## Setup

- **CLIs.**
  - *new:* `pts-bin/pts`, sha256 `48f89ff21c6ae326…`, built 23:05:12. No `.go`, `.c` or `.h` file under `pts/` or `cmd/` is newer, and the sha was unchanged when the review ended.
  - *pre4:* `/tmp/pts-h03/pre4/pts`. This is the current tree with the round-4 hunks reverted through `go build -overlay`. I diffed its three overlay files against the tree: they differ by exactly the hunks listed in the scope.
  - *base:* the `agent-afb5379bc12f20b9e` worktree, with its own `PTS_HOME`.
  - *exp:* *new*'s CLI with `PTS_HOME=/tmp/pts-h03-review5/home`. That directory is a copy of the tree whose `pts/runtime` carries R5-S1's experimental patch (`/tmp/pts-h03-review5/exp-convert.patch`); everything else is a symlink into the repo.
- **Harness.** `tools/cmp5.sh` builds and runs a probe with new, base, pre4 (`PRE4=1`) and Node v24.21 (`--experimental-transform-types`), under `ulimit -v 4194304`, a 60 s timeout, one job at a time. Outputs are in `out/<name>/`. `tools/one.sh <new|base|pre4|exp>` runs a single CLI.
- **Constraints kept.** I ran no `go test` and did not rebuild `pts-bin/pts`. The parent's suite (`final3.exit`: `exit=0`) finished at 23:12:57, and its benchmarks (`bench4`) at 23:16. My benchmarks ran from 23:20 to 23:31 on an idle host: load average 0.75–1.14, `pgrep -x go` empty.

---

## SHOULD-FIX

### R5-S1. Union-typed pattern names cost 3.6–5.3× the base in hot loops; the cause is a 4 KB memset per dynamic conversion

Programs are in `/tmp/pts-h03-review5/bench/`. All CLIs and Node give equal outputs. I ran `PTS_THREADS=1 hyperfine -N -w 1 -r 5` in both orders and averaged the medians, in ms.

| Program | base | pre4 | new | new/base | exp (patch) | exp/base |
|---|---|---|---|---|---|---|
| `w_rows` (typed `number[][]`) | 542 | 970 | 529 | 0.98× | | |
| `w_shapes` (typed shape array) | 1799 | 3396 | 1802 | 1.00× | | |
| `w_entries` (`const [e] = m`, typed `Map`) | 708 | 1224 | 1223 | **1.73×** | 672 | 0.95× |
| `w_set_union`: 5M × `const [a, b] = shapes` (`Set<Circle \| Square>`) | 832 | 2972 | 4317 | **5.19×** | 2076 (1505 with all three lines) | 2.53× (1.82×) |
| `w_param_union`: 5M × `sum(s)`, `function sum([a, b]: Iterable<P \| null>)` | 866 | 405 | 1191 | **1.38×** | 503 | 0.60× |
| `w_gen_union`: 2M × `const [a, b] = g(i)`, `Generator<[number, number] \| null>` | 286 | 667 | 1010 | **3.53×** | 285 | 1.03× |

**Cause (verified).**
- Every `pts_value_convert` goes through `pts_value_try_convert`, which declares `converting c = {.depth = 0};` (`convert.c:507`). That zero-fills a 256-entry stack, 4,104 bytes.
- `objdump -dr` of the cached `libptsrt.a` (`003-convert.o`) shows `sub $0x1008,%rsp` and then `call memset` with `edx=0x1008`, in every call.
- `object_fits` and `pts_check_implies` do the same with `implying s = {.depth = 0};` (`:262`, `:268`). `object_fits` runs when a union's first member doesn't match, such as the `Square` in `w_set_union`.
- `enter`/`implies` only read `stack[i]` for `i < depth`, so zeroing the stack is unnecessary.
- The slice adds conversions to hot paths. In `w_set_union`'s C, each name gets `pts_value_convert(item, &pts_check_1)` at the binding (new; pre4 bound the item raw), and every read keeps its `pts_value_convert(a, &pts_check_1)` and `pts_value_convert(a, &pts_check_2)`. The base read the names raw (`PTS_ARRAY_GET_WORD` and `pts_field_address` only).

**Fix (verified).** In the experimental runtime (`exp-convert.patch`), the three declarations become `converting c; c.depth = 0;` and `implying s; s.depth = 0;`.
- Built with the same CLI, the 13 programs I compared give byte-identical stdout and stderr to *new*: the run tests `array_pattern_union_items`, `array_pattern_loose_int64`, `array_pattern_loose_identity` and `iterable_collections_live`, probes `w1`, `w3`, `w4`, `w6`, `w7` and `w13`, and round 4's `b1_types`, `b1_sets` and `conv_arrays`.
- The patch also speeds up every pre-existing dynamic conversion (narrowed reads of `any`, interface conversions).
- I did not run the suite on it.

**What remains.** `w_set_union`'s 1.82× is the loose union reads: every narrowed read re-checks the union. That is R4-S2's second option: a loose name bound by `patternItem` holds undefined or a value already of its type, so its reads need only an undefined test. It is optional.

At minimum, record the numbers next to D-177's measured costs, which list only number reads.

### R5-S2. Item 2 fixes two more C37-family bugs; neither has a test

Both programs are in `/tmp/pts-h03-review5/probes/min/`. new matches Node on both.

`min_r5_objpat_any.pts`:
```ts
const data: any = JSON.parse('{"list":[1,2],"obj":{"x":1}}');
const { list }: { list: number[] | null } = data;
list?.push(3);
console.log("list", list);
const { obj }: { obj: { x: number } | null } = data;
if (obj) obj.x = 9;
console.log("obj", obj);
```
| | output |
|---|---|
| Node, new | `list [ 1, 2, 3 ]` / `obj { x: 9 }` |
| base, pre4 | **`list [ 1, 2, 2.75 ]`**: a raw double is written into a dynamic array's slot. Then `Uncaught TypeError: Cannot add property x, object is not extensible` (exit 1). |

The full probe `w/w13_json.pts` segfaults on the base (exit 139). The cause: `pts_dynamic_get` of the JSON object was stored raw into a union with an array or shape member. Now it is `pts_value_convert(…, &pts_check_1)`, which copies, as D-070 already does for a non-union `number[]`. new prints `list non-union [ 1, 2, 4 ] [ 1, 2 ]`. pre4 copies there too, but after the corruption: `[ 1, 2, 2.75, 4 ] [ 1, 2, 2.75 ]`.

`min_r5_async_param.pts`:
```ts
async function f([e]: Iterable<[string, number] | null>) { if (e) e[1] = 9; return e; }
f(new Map([["a", 1]])).then((v) => console.log("async param", v));
```
| | output |
|---|---|
| Node, new | `async param [ 'a', 9 ]` |
| base, pre4 | `Uncaught TypeError: Expected [string, number] \| null, got Array` (exit 1) |

`array_pattern_union_items.pts` covers declarations, a sync parameter pattern and `for…of`. Add these two as run tests, with Node's expect blocks.

---

## NITS

- **R5-N1.** The fallback comment at `pts/lower/destructuring.go:286` says "a member dynamic data can't convert to yet (a function)". But `prepareDynamicTarget` returns `""` for `*ir.FuncType`: closures are adapted (D-075), and `(() => number) | null` compiles to `pts_value_convert`.
  - The fallback is reached by generator and iterator members: `Generator<number> | null` and `MapIterator<…> | null` compile to `g = item` (`out1/new/w11_exotic.c:356`, `:418`).
  - So C37's ledger line "No pattern binding stores dynamic data in a typed union it doesn't hold" is not strictly true for `Iterable<[string, number] | Generator<number>>`, whose map entry stays raw.
  - I couldn't make it observable: narrowing such a union to the tuple is a pre-existing clang error on all three CLIs (`w/pre_guard_union_gen.pts`, `w/w18c_fallback.pts`).
- **R5-N2.** D-177 doesn't say that union-typed names in parameter patterns, `for…of`, nested and rest patterns are now checked at the binding. Two behaviours follow, both consistent with D-070 and D-177's "Known gaps"; the text should name them:
  - **Type-unsound dynamic data now throws `TypeError` at the binding.** Base and pre4 stored it raw, and so matched Node (`w/w9_close.pts`: `p1 threw Expected null | {v: number}, got number`, where Node, base and pre4 print `p1 body 5`). The iterator is closed first (`bad closed` is printed), and an async function rejects rather than throwing synchronously (`w16_async`). The base already threw at the binding for non-union names and for `for…of` identifier bindings (`w/w9b_nonunion.pts`, the same on all three).
  - **Past the end of an iterable, non-loose union names throw `TypeError`.** The base threw RangeError on all 8 lines of `w/w10_past_end.pts`. pre4 bound undefined, as Node does, on 6 of them, because it stored the item raw; new throws `Expected [number] | null, got undefined` and similar. A checkable union (`number | null`) threw on pre4 too.
- **R5-N3.** D-177's Reason doesn't record R4-S2's residual (`w_entries` 1.73×) or R5-S1's numbers.

## Round-4 fixes: verified

### R4-B1: exact 64-bit loose reads

- **`run/array_pattern_loose_int64.pts`.** new prints the expect block exactly. pre4 fails 7 of its 9 lines (`Set<int> 9007199254740992 false`, …). The base fails `int[]` and `past the end`.
- **`probes/i/i2_int_uses.pts`.** new is exact for `+=`, `++`, templates, `String()`, `toString()`, comparisons (`true true 0`), calls, `push`, returns and `Map.get` by a loose key. pre4 gives `compare false false -1`, `map get by loose key undefined` and `call/push/return …992`.
- **`probes/i/i1_int.pts`.**
  - `d as i64`, `d as u64` and `d as int` of a dynamic 1.5 print `1 2 1 1` on new and on pre4: `checkedConversion` is unchanged.
  - `Generator<int>` yielding a JSON `3.5` throws `Expected i64, got number`, on pre4 too.
  - The past-the-end TypeError now names the type: `Expected i64, got undefined` (pre4: `Expected number, got undefined`).

### R4-S1, C37 and the item 2 widening

- **Round-4 probes.**
  - `min_r4_union_entry` and `pre_param_union_segfault` match Node.
  - `b1_union_entry`, `b1_union_entry3` and `b1_ue3a` now match Node; in round 4 they printed `[ 'a', 1 ] false` or crashed.
- **`run/array_pattern_union_items.pts`.** It equals Node and its expect block. Base and pre4 crash with SIGSEGV (exit 139) before any line reaches the file, because stdout is buffered.
- **Widening probes** (`probes/w/`). Each matches Node, or matches the base where the base already behaves the same way. All of them build on new, base and pre4 alike.

| Probe | What | Result |
|---|---|---|
| `w1_param` | parameter patterns: tuple, interface, array, shape, class-as-shape, Map, Set, function, `C \| [number]`, `Date \| [number]` | new = Node; base and pre4 garbage on the first line (`… 4621256167635550108 more items`) |
| `w2_forof` | `for…of` patterns, generator tuples, nested, rest | all = Node |
| `w4_sound`, `w6_gen`, `w7_shapes`, `w8_fn`, `w11_exotic`, `w14_tuples`, `w15_generic`, `w17_entries`, `w19_iface` | interface with methods (identity kept), getter classes (live, not snapshotted: `[ 1, 2, 2 ]`), callable interface, class-as-interface; discriminated unions; labeled, readonly and `as const` tuples; generic `first<T>`, `Box<T>`, `swap<A, B>`; `Object.entries`, `Array.from(map)`, `[...m]`, `m.entries()`, `m.values()`; interfaces with optional, readonly, inherited, generic, nested and function-typed properties; Promise, Error, `Uint8Array`, RegExp, `Record`, `object`, Generator, Iterator, `MapIterator`, `Float64Array \| number[]` | all = Node on new, base and pre4 (`w8`'s `d2 === narrowRet` is `false` on all three, Node `true`: pre-existing closure adaptation at `yield`) |
| `w3_any`, `w3b`, `w3c`, `w12_objpat`, `w13_json` | `any`/JSON data | new: copies as D-070 does (writes don't reach `data`), type-unsound items throw a catchable TypeError; base and pre4 segfault (exit 139) or corrupt memory (R5-S2) |
| `w5_variance` | `(number \| string)[]` set of a `number[]`, generic `mk<K, V>()`/`mkArr<T>()`, readonly array, tuple as `number[]`, Set as a `Set \| Map` member | identical on all three; the `v1` copy and `v6` TypeError are pre-existing |
| `w9_close`, `w9b_nonunion`, `w10_past_end`, `w16_async` | throws at the binding | R5-N2; the iterator is always closed |
| `par/par_union.pts` | union conversions in `parallel for` (64 × 2,000) and `.par.map` with parameter patterns | `259904000` / `1498500`, equal to a hand computation; 5 repeated runs identical |

- **New PTS1001s, clang errors, panics.** None.
  - Every build failure in these probes fails identically on new, base and pre4. They are listed under pre-existing.
  - The parent's whole-repo sweep (`/tmp/pts-h03/emitall/new`, 1,125 files) agrees with my emits on the 3 files I spot-checked.
  - Against round 4's sweep, only the 10 files listed in round 4 and the 3 new tests have changed C, and no exit code changed.
  - I ran the 9 changed corpus files with new: 8 match Node, and `matrix_transpose_table` differs by design (D-038).
  - `csvq` gives identical output to the base on all 15 commands.

### R4-S3: `+=` with valueOf

- **`run/compound_concat_valueof.pts`.** It equals Node and its expect block. The base fails to build (PTS1001 at 19:3 and 23:1). pre4 fails 4 lines (`qT`, `uT`, `sT`, `wT`).
- **`probes/v/v1_order.pts`.**
  - It matches Node on the read-before-right-side order for locals, unions, fields, elements, `Record` and `left().p += right()` (`LR p36`), and on `await` and a throwing `valueOf` (`throw boom t`).
  - It also matches on a `valueOf` returning a string (`xvs`; pre4 `x[object Object]`) or an object (`xts`), and on a subclass (`x9`; pre4 `xT`).
  - The `yield`, `V | null` and `V | number` lines are the pre-existing `+` family below.
- **`probes/v/v4_date.pts`.** `+=` of a `Date`, an `Error`, a RegExp and an array matches Node: a `Date` is still concatenated with its string form. Inherited `valueOf` is fixed (`x:1`; base and pre4 `x:X`).

### Other

R4-N2 is done: the header now wraps at 84–89 columns.

## Pre-existing, not caused by the slice (base identical)

- **The 4 KB memsets** in `convert.c:262`, `:268` and `:507` are base code (`convert.c` is untouched by the slice). R5-S1 is only about the slice using them on hot paths.
- **`+` and `+=` with an operand of type `any`, a union with a `valueOf` class, or an interface declaring `valueOf`, use `toString`.** Node uses `valueOf`. The base prints the same (`v/v3_binary.pts`): `+ any sT` (Node `s1`), `+ V|null uT`, `+ interface hT`, `+= any sT`. `g += yield …` with `TNext = V` gives `gT` on base and pre4 too.
- **Clang errors on all three CLIs:**
  - `await p` of a narrowed `Promise<number> | [number]` (`w/pre_async_union_await.pts`: `incompatible integer to pointer conversion`);
  - narrowing a union with a `Generator` member to a tuple, by a type guard or by `in` (`w/pre_guard_union_gen.pts`).
- **64-bit literals.** Literal elements of `new Set<i64>([9007199254740993, -9007199254740993])` lose precision (`i/pre_set_i64_literal.pts`), and so does `-(9007199254740993 as i64)` (`i/pre_neg_literal.pts`). `const z: i64 = -9007199254740993` is exact.
- **Insertion-time limits, identical on all three:**
  - `new Set<HasLen | null>([array])` → `TypeError: Expected null | {length: number}[], got Array`;
  - `new Set<Map<string, number> | null>([Map<string, any>])` → `TypeError`;
  - a `number[]` stored as `(number | string)[]` is copied (`v1 [ 1, 2 ] false`);
  - PTS1001 for `Derived` as `Base | [number]`, `number[]` as `null | {length: number}`, `string` as `number | {length: number}`, `[number, ...string[]]` (printed as `null | number | string[]`), `[number, string?]`, extending `Map`, and index signatures with other properties.
- **Round 4's list still stands:** D-090's `return x` past the end, `Set` and `-0`, `Set<u64>` max, holes reading `0`, and maps and sets through `any`.

---

# H03a review, round 6 (opus-reviewer, claude-opus-5-5, xhigh), 2026-10-16

Scope: the parent's changes since round 5 on the uncommitted tree against base `7f0a4131a`. These are R5-S1 in `convert.c`, R5-N1's comment, the new `run/pattern_union_dynamic_items.pts`, and the documentation: D-177, SPEC, the ledger, HARDENING_PLAN, PROGRESS, `h03.md` and `h03-review.md`. This was a read-only review; scratch files are in `/tmp/pts-h03-review6/`.

**Verdict: no BLOCKER. The code and the new test are ready to commit.**
- (a) The `convert.c` change is equivalent to my experiment.
- (b) The new test matches Node and discriminates on the base. Each of its three parts discriminates on its own.
- (c) The documentation is accurate in substance, but needs three cheap text fixes first (SHOULD-FIX):
  - measurements taken before the stack fix are presented as current or final;
  - D-177 claims too much for C38, and its Known gaps omit C39 and C40;
  - PROGRESS credits the speed-up to "typed-array elements".

There are eight nits.

## (a) Runtime: verified

- **Only the comments differ from my experiment.** `diff -r /tmp/pts-h03-review5/home/pts/runtime pts/runtime` shows just the three comment lines at `convert.c:262`, `:269` and `:509`. The code is `implying s; s.depth = 0;` (twice) and `converting c; c.depth = 0;`, as in `exp-convert.patch`. The round-5 copy was a full copy of the runtime, not symlinks, so nothing else in the runtime changed since round 5.
- **Against the base, the runtime diff is wider than (a)'s wording suggests.** Besides `convert.c` and the inline `pts_value_to_number_checked` (with its `string.c` rename to `pts_value_to_number_slow`), it also contains the slice's iteration API:
  - `generator.c`: `pts_iteration_*`, `is_collection`/`collection_iterator`, live map and set iterators in `pts_iterator_open`;
  - the matching `pts.h` declarations;
  - `dynamic_methods.c`: `pts_value_iterable_length`/`_element` removed;
  - `pts_iterator_take` removed.

  All of these were reviewed in rounds 1–5, and the SPEC's §9.121 runtime line lists them.
- **Safety, re-checked.**
  - `converting` and `implying` hold only `stack[256]` and `depth`.
  - The stack is read only for `i < depth` (`enter`, `convert.c:57-58`; `implies`, `:245-246`), pushed at `depth`, and popped with `depth--` (`:257`, `:504`).
  - There are no struct copies.
  - An early exit through `longjmp` abandons the frame, which is harmless.
- **The CLI is unchanged.** `pts-bin/pts` has sha256 `48f89ff2…1474`. It was rebuilt at 23:44:02, after the `destructuring.go` comment edit (23:43:43), and its sha is the same, so the Go code compiled identically. `patternItem`'s fallback comment now says "(a generator, an iterator)", so R5-N1 is fixed.
- **Suite record.** `h03-suite.jsonl.gz` counts 1,345 pass, 260 skip and 0 fail. The run went from 23:46:50 to 23:53:28, after the `convert.c` and test edits. `TestRun/pattern_union_dynamic_items` and `TestRunMatchesNode/pattern_union_dynamic_items` both pass.
- **Other records.** `h03-regressions.json` (23:48:45) and `h03-probes-after.json` (23:49:06) are newer than the runtime change. The capped infinite-generator probe, rebuilt now, prints `0 1 2 end` with exit 0 under `ulimit -v 1048576`.
- *Suspicion, no action:* bdwgc scans stacks conservatively. If a collection runs during a conversion, the 4 KB that is no longer zeroed can keep dead objects alive until the next cycle. That is benign: the same holds for any uninitialised frame slot, and it is not a safety issue.

## (b) `run/pattern_union_dynamic_items.pts`: verified

- **It matches Node.** Node v24.21.0 (`--experimental-transform-types`) output equals the `// expect:` block byte for byte, and so does the new CLI (`out/pattern_union_dynamic_items/`).
- **The base fails it.** The base CLI (`agent-afb5379bc12f20b9e`, its own `PTS_HOME`) prints `list [ 1, 2, 2.75 ]` and then `Uncaught TypeError: Cannot add property x, object is not extensible` (exit 1).
- **Each part discriminates on its own** (`probes/obj_only.pts`, `probes/async_half.pts`):
  - the object-pattern half on the base: the same TypeError, exit 1;
  - the async half on the base: `Uncaught TypeError: Expected [string, number] | null, got Array`, exit 1;
  - the new CLI matches Node on both.
- **Its listings are complete.** It is in SPEC §9.121's test list, and that list equals the 24 new test files exactly (scripted check).

## (c) Documentation

Checked and correct:
- Every relative link in the seven documents resolves.
- `h03-review.md` contains all five reports verbatim (`cmp` against `/tmp/pts-h03-review*/REVIEW*.md`).
- Every function named in SPEC §9.121 exists (`isIterablePatternSource`, `destructureIterable`, `isPlainBinding`, `bindElement`, `readsElement`, `patternItem`, `isTypedElementRead`, `looseRead`, `iteratorLoop`, `iteratorClosing`, `declaredNames`, `reportedGetterResult`, `isIteratorResultType`). `declaredLocals`, `iteratorTake` and `looseItem` are gone from the code; `looseItem` appears only in round-3 history.
- The IR names agree: `ValueIterable*` and `IteratorTake` are removed and `pts_value_iterable` is kept.
- SPEC:1001 is right: `pts_value_as_string`, `pts_value_to_number_checked` and `pts_value_as_bool` are inline in `pts.h`, and `pts_value_to_number_slow` and `pts_throw_not_primitive` are in `string.c`.
- All benchmark columns in `h03.md` attributed to my rounds match my reports:
  - round 1: 2.09, 1.64, 1.59, 1.57, 1.09, 1.17, and round 2's 1.44;
  - round 3: 2.9, 2.8, 1.28, 1.0, 1.06–1.10, 1.15, 0.23, 0.37, 1.10;
  - round 4: 1.83, 1.89, 1.72;
  - round 5: 5.19, 1.38, 3.53, 1.73, 0.98, 1.00.

  The parent's final numbers agree with my experimental runtime (0.95×, 1.03×, 0.60×, 1.82×).
- "The base paid the memset at every checked read" is confirmed. The base's and the new C for `w_rows` both read `pts_value_convert(r, &pts_check_1)` in the loop (`out1/{base,new}/w_rows.c:54`).
- Every ledger row C27–C40, and the V08 and V09 notes, has its owning task in HARDENING_PLAN: H03a, H03b, H04 (C34, C39), H11 (C31's remainder), H19 (C40), H22 (V08, V09) and H27 (`w_set_union`).
- The V09 placement is consistent with D-012: literals take their type only from an annotation or a binary operand, so `new Set<i64>([…])` elements and `-(… as i64)` are literal and `as` footguns.

### SHOULD-FIX

**R6-S1. Measurements from before the stack fix are presented as current or final.** The same programs are reported twice with different numbers.

- `docs/DECISIONS.md:1487` (D-177 Reason) says:
  > keeps destructuring-heavy loops over typed arrays at the base's cost (`const [r] = rows` of `number[][]` 1.03×, of shapes 1.00×) … a loose name bound to a typed `Map`'s entry costs 1.73× in a loop that destructures and reads it (the base converted the entry once and read an unboxed tuple; the loose name is checked again at each read)

  The same paragraph then gives 0.34×, 0.14× and 0.91× for those programs. The parenthesised cause is also stale. The per-read check is still there, and on the base too (the `w_rows` C above). It cost 1.73× only because each check zero-filled 4 KB; my experimental runtime gave 0.95× with the per-read check unchanged.

  *Correction:* put these in the past tense ("before the stack fix below, … 1.03×, 1.00× and 1.73×"), or drop them and keep only the final numbers.
- `docs/agent/hardening-evidence/h03.md:89-95`: the round-4 table's lead-in says "the parent re-measured the final build the same way", and its column is headed "Final", but it shows `w_rows` 542 ms (1.03), `w_shapes` 1,805 ms (1.00) and `w_entries` 1,264 ms (1.73). The final build is 181, 254 and 642 ms (the table at `:105-107`).

  *Correction:* head the column "After round 4's fixes" and say so in the lead-in. The `u_*`/`b_typed` row can stay: those programs do no dynamic conversion.

**R6-S2. D-177 claims too much for C38, and its Known gaps omit C39 and C40.**

- `docs/DECISIONS.md:1485` says:
  > and onto any string calls a right side's `valueOf` first, as `+` does (it called `toString`, C38)

  On the current build, `probes/r5/probes/v/v3_binary.pts` (`let s = "s"; s += av` with `av: any = new V(1)`) prints `+= any sT`, where Node prints `s1`. `+` fails the same way: `+ any sT`, `+ V|null uT`, `+ interface hT`.

  *Correction:* "for a right side of a class type with `valueOf`; with an `any`, union or interface right side, `+=`, like `+`, still calls `toString` (C40, H19)".
- `:1489` (Known gaps) lists C28, C30, C32–C35. Add C39 (clang rejects two union narrowings, H04) and C40 (H19). Both were found by this slice's review and are on the ledger.

**R6-S3. PROGRESS credits the speed-up to the wrong kind of read.**

`PROGRESS.md:244` says:
> make checked reads of typed-array elements 3–7× faster

In this codebase "typed array" means `Float64Array` and its kin (SPEC §3.14, and D-177's own "A pattern over a typed array has no close-on-throw handler"). Their element reads do no conversion, so the stack fix doesn't affect them. The 3–7× (0.34× and 0.14×) is `w_rows` and `w_shapes`: loose names destructured from `number[][]` and from a shape array, each read of which is a `pts_value_convert`.

*Correction:* "make every dynamic conversion cheaper (loose names destructured from `number[][]` and shape arrays read 3–7× faster than on the base)".

### NITS

- **R6-N1. The cost range for union-typed names leaves one program out.** "3.5–5.2× the base" omits `w_param_union` at 1.38×, which is union-typed too (`DECISIONS.md:1487`, `h03.md:59`, `h03-review.md:9`). Write "1.4–5.2×" or "up to 5.2×". My round-5 headline's "3.6–5.3×" was itself off; the table's 3.53 and 5.19 are right.
- **R6-N2. Wrong read count.** `DECISIONS.md:1487` says "100 million reads of a name destructured once cost … a string's 1.25×", but `u_string` does 30 million reads (`/tmp/pts-h03-review3/bench/u_string.pts`; `h03.md:78` says 30M).
- **R6-N3. D-177's Split bullet is stale** (`DECISIONS.md:1481`):
  > H03a (this decision: C02, C27, C03's typed half, C18 and the related C29 and C31) and H03b (C28, C30, C32, …)

  It omits C36, C37 and C38, which the same decision fixes, and C33 and C35, which its Known gaps, the ledger and the plan give to H03b.
- **R6-N4. Stale round counts.**
  - `h03-review.md:3` says "Three read-only rounds", but the file reproduces five.
  - `h03.md:8` says the second session "ran a third review round and the final evidence"; it ran rounds 3–6.
  - `h03.md:5` (Scope) lists where each row went but omits C39 → H04 and C40 → H19; they appear only in the Iterations paragraph.
- **R6-N5. "Typed array" is used for `T[]`.**
  - `DECISIONS.md:1485`: "An element of a typed array is one of its type already and is kept as it is".
  - `:1487`: "since a typed array's elements are kept as they are".
  - `SPEC.md:1547`: "not an element of a typed array, `isTypedElementRead`".
  - `h03-review.md:8`: "keeping typed arrays' elements as they are".

  `isTypedElementRead` means an element of an array whose element type isn't `any` (`T[]`), and the same D-177 bullet uses "typed array" for `Float64Array`. Write "an element read from an array with a declared element type (not `any[]`)".
- **R6-N6. The C37 acceptance doesn't hold for dynamic data.** `CONSOLIDATED.md:57` says "writes and identity of the item stay", but D-070 copies `any`/JSON data. `probes/r6/r6_identity_any.pts`, `const { list }: { list: number[] | null } = data; list?.push(3)`, prints `[ 1, 2, 3 ] [ 1, 2 ] false` (Node: `[ 1, 2, 3 ] [ 1, 2, 3 ] true`). The parameter-pattern case over a typed array held as `any` matches Node. Add "(apart from D-070's copies of dynamic data, SPEC §8)".
- **R6-N7. The sanitizers don't check what the `convert.c` change relies on.** `h03.md:66` says the C unit tests under ASan/UBSan "cover the `pts.h` and `convert.c` changes". ASan and UBSan don't detect reads of uninitialised memory; the safety of `convert.c` rests on the inspection above. "Exercise" is the accurate word.
- **R6-N8. D-177's first set of numbers comes from an early tree.** The Reason's opening figures (2.08×, 1.60×, 1.54×, 1.09×, 1.18×) were measured on the round-1 tree and not re-measured after rounds 2–5. `h03.md` labels them as such; D-177 doesn't. They are likely still valid (`any`-typed names need no conversion), but that is not verified.

## Pre-existing (not caused by the slice; verified on base and new)

- `function g([a, b]: (number | undefined)[])` called with `[1]` throws `RangeError: Array index 1 is out of range … give the element type `| undefined`` on both CLIs, though the element type already includes `undefined`. Node prints `array 1 undefined` (`probes/r6/r6_pe_arr.pts`).
  - H11's new text covers the RangeError in substance. The misleading hint could be noted there or under H22.
  - The `Iterable<number | undefined>` parameter and the `for…of` cases now match Node (`r6_pe_forof.pts`: the base threw RangeError).

## Constraints kept

I ran no `go test`, did not rebuild `pts-bin/pts` and ran no benchmarks. Builds used `GOMAXPROCS=2`. Programs ran one at a time with `ulimit -v 4194304` and a 60 s timeout, and the CLI caches were under `/tmp/pts-h03-review6/`.
