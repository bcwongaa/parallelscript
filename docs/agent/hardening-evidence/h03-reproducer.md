# H03 reproduction and RED regressions (opus-reproducer)

Reproducer: subagent for H03, model `claude-opus-5-5`, effort xhigh, 2026-10-15.
Scope: H03 in `docs/agent/HARDENING_PLAN.md`, ledger rows C02, C03 (typed half) and C18, and the parent's new C27.
Deliverable: nine RED suite regressions under `pts/tests/run/` and this report. No compiler, runtime, library, harness or `tools/agent/` file was changed, no plan item was ticked, and PROGRESS/DECISIONS/plan are untouched.

## Summary

| File | Row | Today on the base (exact first failure) | Controls inside |
|---|---|---|---|
| `getter_void_never.pts` | C02 | `pts run` exit 2: `panic: cgen: no check for void` at `pts/cgen/checks.go:305` (`checkDefinition` ← `checks.go:277` ← `cgen.go:159` ← `cgen.go:38` ← `driver.go:39`) | static `void` getter; getters typed `undefined`/`number`; `void` method; `void` data property |
| `getter_void_interface.pts` | C02 (through an interface) | exit 1, 3 × `error PTS1001: using a \`object1\` as a \`{audit: undefined}\`, whose \`audit\` it doesn't provide with the same types, is not supported yet` (13:26; also `Ledger` at 29:25 and 31:11) | an `undefined` getter and a `void` data property through interfaces |
| `return_bare_undefined.pts` | C27 | exit 1: clang `error: non-void function 'pts_fn_maybeNumber' should return a value [-Wreturn-type]` and 15 more (16 errors; every bare `return;` site but the one inside `try/finally`) | `return undefined`, falling off the end, `: void`, generator, async |
| `any_iteration_lazy.pts` | C18 (`for…of` over `any`) | exit 0, wrong stdout from line 2: `break yield 1`, `yield 2`, `yield 3`, `ran to the end`, `closed`, then `break item 0`, `break item 1` (Node: `break item 0` second) | `any` array pushed to in the loop (live), `any` string, map, set |
| `array_pattern_lazy.pts` | C18 (array patterns) | exit 0, wrong stdout from line 3: `any generator yield 2` (Node: `any generator closed`) | `Generator<number>` value/parameter declarations, typed map iterator, rest elements, patterns longer than the iterator, `any` array and string |
| `iteration_unbounded_lazy.pts` | C18 (unbounded) | exit 0, wrong stdout from line 1: `any generator loop closed after 1000000`, `any generator loop escaped: any generator loop ran past the safety bound`; 11 of 13 paths drain to the 1,000,000-item bound | typed `for…of` with break; `Generator<number>` parameter declaration |
| `iterable_collections_live.pts` | new, C18-adjacent (N2 below) | exit 0, wrong stdout from line 2: `any map entry b 2` (deleted before the loop reached it; Node `any map entry c 3`) | typed Map, Set, and `m.keys()` straight in `for…of` |
| `iterator_result_getters_typed.pts` | C03 typed half — **parent decides** | exit 1, 2 × `error PTS1001: converting object4 to object1 \| object2 is not supported yet` (20:24, 54:24) | none (all through the typed layout) |
| `iterator_next_result_getters.pts` | new, C03-adjacent (N3 below) — **parent decides** | exit 0, wrong stdout from line 3: `manual read value 0` before `first done: false`; later `manual escaped: the value of a done result was read` | the same results through `for…of` |

Every file's `// expect:` block is Node's exact stdout (Node exit 0 for all), produced by Node and checked byte for byte against a fresh Node run by re-implementing `expectBlock`'s parsing (`checkexpect.sh` below, exit 0); no file trips the harness's ParallelScript-feature pattern, so `TestRunMatchesNode` compares all nine. Every file is reachable: a twin that changes only the RED construct to one that works today prints exactly that expectation under today's PTS (see "Expectations are reachable").

**Integration warning.** `getter_void_never.pts` panics inside the `go test` process (the harness compiles in-process): the process dies with `panic: cgen: no check for void [recovered, repanicked]` and package `FAIL`, and no later test reports. Land it only with the C02 fix. The other eight fail normally (both `TestRun/<name>` and `TestRunMatchesNode/<name>`).

**Ordering pins a fix must respect.** The C18 files pin JS's element-by-element order: a nested pattern takes from its item before the outer pattern steps again or closes its iterator (`nested`, `Generator nested`, `for-of binding`). Today's `pts_iterator_take` takes all values, closes, then binds; the base already prints `Generator nested closed` before the inner `Generator nested row yield 0`, so a fix that only makes the inner take lazy still fails `Generator nested`. Defaults are printed only where no default/next interleaving is observable (H12's).

## Root causes (verified in code at `7f0a4131a`)

- **C02.** When descriptors carry declared types, `collectDeclaredTypes` asks for a run-time check of each accessor's type: an object literal's getter through `shapeAccessorType` (`pts/cgen/checks.go:110`, `classes.go:594-597` returns the getter's result) and a class's getter through `accessorType` (`checks.go:115`). A getter typed `void` (inferred for a body that only throws, or declared with `return;`/`return undefined`) or `never` has result `ir.Void` (`never` panics with the same "void" message), and `checkDefinition`'s primitive switch (`checks.go:284-305`) has no case for it. Static getters aren't instance accessors and don't reach the table (they work). The interface variant fails earlier, in lowering: the getter's IR type (`void`) isn't the interface property's (`undefined`), so the conversion is PTS1001; an `undefined`-typed getter converts fine.
- **C27.** `returnStatement` (`pts/lower/lower.go:1235-1238`) lowers a bare `return;` to `leaveFunction(at, nil)` whatever the function's result type; cgen's `returnStatement` (`pts/cgen/cgen.go:589-596`) then writes `return;` in a C function whose result is a `pts_value` (or `pts_string *` …), which clang rejects (`-Wreturn-type` is an error for this case). Only `ir.Void` results should get a value-less return; others need the result type's undefined. The `try/finally` site compiles today (its return goes through the finally lowering).
- **C18, `for…of` over `any`.** `pts/lower/arrays.go:706-714` lowers it to an index loop over `ValueIterable`, which is `pts_value_iterable` (`pts/runtime/src/dynamic_methods.c:1313`): a generator is drained by `pts_iterator_to_array` (`:1341-1342`), an object with `[Symbol.iterator]` by `pts_iterator_to_array(pts_iterator_open(…))` (`:1345`), and a map or set is copied into a snapshot array (`:1324-1340`). No `return()` is ever called, a generator runs to its end before the body, and an infinite iterator allocates without bound.
- **C18, array patterns.** `destructure` (`pts/lower/destructuring.go:33-44`) takes the lazy, closing `IteratorTake` path only when the value is `ir.Value` **and** `isIteratorSource` (`pts/lower/generators.go:386-389`) holds: the pattern is a declaration's name **with an initializer** whose static type is an iterator type (`Generator`, `IterableIterator`, map/array iterators). Everything else — `any` values, `Iterable<T>` values and parameters, class instances (`isPlainObject`), and also `Generator<T>`-typed items bound without an initializer (parameter patterns, `for…of` bindings, nested elements) — goes through `ValueToArray` → `pts_value_to_array` (`dynamic_methods.c:1359`) → `pts_value_iterable`, which drains. `pts_iterator_take` itself (`pts/runtime/src/generator.c:440-455`) collects all values and closes before any element is bound.
- **N2 (maps/sets through `Iterable<T>`/`any`).** `pts_iterator_open` (`generator.c:239-247`) wraps a map or set in an items iterator over `pts_value_iterable`'s snapshot, while `pts_iterator_get` (`generator.c:217-237`) makes the live `pts_map_iterator_new`. Typed `Map`/`Set` loops use D-056's direct live loops, so only `any` and `Iterable<T>` holders snapshot.
- **C03 (typed half).** D-146's known gap: an object literal with accessors can't become the `IteratorResult` union's shapes (`converting object4 to object1 | object2`).
- **N3 (`it.next()` on a typed iterator).** The call is lowered to `pts_iterator_resume` followed by a new D-109 result shape whose `value` is `pts_iterator_value(…)` and whose `done` is the step's result (visible in the generated C, e.g. `pts_tmp93->value = …pts_iterator_value(iterator_2)…; pts_tmp93->done = done_2;`): the object iterator reads `done` and then `value` at the call, even for a done result.

## Environment and provenance

- Worktree `/home/bryan/projects/parallelscript/.claude/worktrees/agent-afb5379bc12f20b9e`, branch `worktree-agent-afb5379bc12f20b9e`, base `7f0a4131a2fba0d857a6317dad118a34ccf0fba3`.
- Submodules (none in a fresh worktree), each exit 0: `git submodule update --init --reference /home/bryan/projects/parallelscript/.git/modules/pts/third_party/<lib> pts/third_party/<lib>` for ryu (`72e18e8d1`), bdwgc (`4fab5386d`), libuv (`840404ce8`), libbacktrace (`0b9b49cf4`), pcre2 (`6f9d7c137`).
- CLI: `GOMAXPROCS=2 go build -p 2 -o pts-bin/pts ./cmd/pts`, exit 0. `pts version`: `pts 0.7.0 (commit 7f0a4131a; typescript-go 7.0.2)`, exit 0; sha256 `8eaff4cd869a12e7a0cac79b771f4ad69b3f5bd2fb3f6248c3238fcbcc1f680d`.
- `go version go1.26.9 linux/amd64`; Node `v24.21.0` (`~/.local/node/bin/node`); `Ubuntu clang version 18.1.3 (1ubuntu1)`. Disk before starting: 130G free of 234G.
- Every PTS run used `PTS_HOME=<worktree>` and `PTS_CACHE_DIR=/tmp/pts-h03-repro/cache`, so the runtime is the base's, not the parent checkout's.
- Scratch `/tmp/pts-h03-repro`: test sources `scratch/{c02,c03,c18,c27}/`, twins `scratch/twins/`, probes `scratch/{c02,c18,new}/p*`, scripts `tools/`, `pts run` outputs `red/<name>.{stdout,stderr,exit}` and `red/summary.txt`, `go test` logs `gotest/<name>.log` and `gotest-summary.txt`.

Test files (sha256): `getter_void_never` `29a1dd53…`, `getter_void_interface` `993c8301…`, `return_bare_undefined` `11bc217f…`, `any_iteration_lazy` `d0e6fa54…`, `array_pattern_lazy` `900f838f…`, `iteration_unbounded_lazy` `32d08f6b…`, `iterable_collections_live` `7bd7fd8c…`, `iterator_result_getters_typed` `d698f3a7…`, `iterator_next_result_getters` `634b14d9…`.

## Commands and exit codes

All scripts use `set -o pipefail`, cap written files at 20 MiB (`ulimit -f`) and time out each run.

1. `tools/mkall.sh` (writes each test as scratch source + `// expect:` built from Node's stdout by `tools/mkexpect.sh`, which refuses trailing whitespace and directive-like lines): exit 0; Node exit 0 for all nine.
2. `tools/checkexpect.sh` (parses each committed file's expect block as `spec_test.go`'s `expectBlock` does and compares it byte for byte with a fresh `node --no-warnings --experimental-transform-types` run of a `.mts` copy, `TZ=America/New_York`; counts ParallelScript-feature matches outside comments): exit 0 — 9 × `expect==node yes`, 0 feature matches.
3. `tools/redrun.sh` (`pts-bin/pts run pts/tests/run/<name>.pts` for each, 8 GiB address-space cap): exit 0, meaning all nine RED. Per file `pts run` exit: `getter_void_never` 2, `getter_void_interface` 1, `return_bare_undefined` 1, `any_iteration_lazy` 0 (176 lines vs Node's 134), `array_pattern_lazy` 0 (179 vs 122), `iteration_unbounded_lazy` 0 (26 vs 37), `iterable_collections_live` 0 (25 vs 25, 5 lines differ), `iterator_result_getters_typed` 1, `iterator_next_result_getters` 0 (24 vs 21).
4. `tools/gotest.sh` — for each file, separately: `PATH=$HOME/.local/go/bin:$HOME/.local/node/bin:$PATH PTS_HOME=<worktree> PTS_CACHE_DIR=/tmp/pts-h03-repro/cache GOMAXPROCS=2 go test -count=1 -p 2 -timeout 20m ./pts/tests/ -run 'TestRun/<name>$' -v`. Exit 1 for all nine:
   - `getter_void_never`: `--- FAIL: TestRun/getter_void_never`, then `panic: cgen: no check for void [recovered, repanicked]`; the process dies (`TestRunMatchesNode/getter_void_never` never reports).
   - `getter_void_interface`, `iterator_result_getters_typed`: `run_test.go:69: assertion failed: error is not nil: …PTS1001…` (the build), and the same in `TestRunMatchesNode`.
   - `return_bare_undefined`: `run_test.go:69: assertion failed: error is not nil: clang -O2 … exit status 1` with the 16 `-Wreturn-type` errors.
   - `any_iteration_lazy`, `array_pattern_lazy`, `iteration_unbounded_lazy`, `iterable_collections_live`, `iterator_next_result_getters`: `run_test.go:69` (`TestRun`, `--- got.stdout +++ spec.stdout`) and `run_test.go:98` (`TestRunMatchesNode`, "pts and Node print different output") both fail.
5. `tools/unittests.sh` (`go test -count=1 -p 2 -v ./pts/tests/ -run 'Directive|ExpectBlock|PlainTypeScript|SkipCauses|SkipsAreClassified'`): exit 0, 7 PASS.
6. `tools/twincheck.sh` (builds and runs every twin on the base, compares with the committed expectation): exit 0, 9 × `yes`.
7. Bounded allocation on the base: `pts build pts/tests/run/iteration_unbounded_lazy.pts` exit 0, then `/usr/bin/time -v` on the binary: exit 0, 0.83 s, max RSS 48,396 KiB (`PTS_THREADS=1`) and 64,480 KiB (default threads).

## The files

### `getter_void_never.pts` (C02)
Object-literal getters typed `void` (`return;`), `void` (`return undefined`), inferred `void` (only throws) and `never`, read directly (`=== undefined`), through `any`, and caught when they throw; an unread one; `console.log` of the literal (Node: a multi-line `{ count: 1, audited: [Getter], cleared: [Getter], missing: [Getter], impossible: [Getter] }`, getters not called); `JSON.stringify` of a literal whose `void` getter logs (`serialised audited read`, `{"count":3}`) and of one whose getter throws (the getters run in order until `literal missing` is thrown); class getters of the three kinds, the instance printed (`Settings { count: 4 }`) and stringified (`{"count":4}`, prototype getters not called), and read through `any`; a generic class's `void` getter (at `number` and `string`) and `never` getter (at `boolean`). `typeof` of a `void` getter was left out: `typeof` on any `void` expression is a separate PTS1001 (below), which would hide the panic.

### `getter_void_interface.pts` (C02)
A literal and a class whose `audit` getter is `void`, used as `interface Audited { readonly audit: void }` directly and as a parameter. Kept apart because its PTS1001 comes from lowering, before cgen, and would hide the panic in the file above.

### `return_bare_undefined.pts` (C27)
Bare `return;` with results `number | undefined`, `any`, `unknown`, `undefined`, `number | null | undefined`, `number | void`, in methods (`string | undefined` method and getter, `number | undefined` static and object-literal methods), arrows, a returned closure, a generic function at two instantiations, inside `try/finally`, inside a loop, a function expression, and results stored in an array; each value printed, compared with `=== undefined` and used with `??`.

### `any_iteration_lazy.pts` (C18)
A generator held as `any` with `break`, run to completion, `return` from a function inside the loop, a body throw caught outside, labelled `continue`/`break` of an outer loop, nested loops, and a body that advances the same generator (through a typed handle, see N1); an `any` object iterator whose `[Symbol.iterator]`, `next` and `return` log (break, completion, throw); results with logging `done`/`value` getters (done read first; the done result's value never read; with and without break); `return()` throwing after `break` (its error propagates: `close throws caught return failed`), a body throw with `return()` throwing (`body wins caught body failed`), `next()` throwing (no `return()`), a non-object result (`TypeError: Iterator result 1 is not an object`, no `return()`) and `return()` giving a non-object after `break` (`TypeError: Iterator result 5 is not an object`). Every case but the final controls runs inside `attempt()`, so each divergence shows on the base.

### `array_pattern_lazy.pts` (C18)
Patterns over: an `any` generator and object iterator; an iterable class instance; `Iterable<number>` values, parameters and parameter patterns; `any` parameter patterns; `Generator<number>` **parameter patterns** and **`for…of` bindings** (both drain today); nested `[[a, b], c]` over `any` and `[[a, b]]` over a typed `Generator<Generator<number>>` (both pin bind-as-you-step order); elision; defaults that aren't needed (`return()` still called) and defaults past the end (no more `next()`, no `return()`); a pattern longer than the iterator (`c = "none"`); rest elements (drain, correct, no `return()`); getter results; `for (const [a, b] of …)` over `any` and `Iterable<Iterable<number>>` tables of generators; map and array iterators taken through `any` and then continued (`a b`, `10 20`).

### `iteration_unbounded_lazy.pts` (C18)
Every source would be infinite but throws after 1,000,000 items. Lazy in Node, drained today: `for…of` with `break` over an `any` generator and object iterator; patterns over an `any` generator (`[a, b, c]`), an `any` object (`[a, , c]`), an iterable class instance, an `Iterable<number>` parameter, `for…of` bindings over `any` and `Iterable<Iterable<number>>` rows, a nested pattern, a `Generator<number>` parameter pattern and a `Generator<number>` `for…of` binding. Controls: typed `for…of` and a `Generator<number>` parameter declaration. On the base it finishes in under a second at about 48–64 MiB (each drain stops at the bound), so it is safe without a memory cap.

### `iterable_collections_live.pts` (N2, new)
Maps and sets held as `any` or `Iterable<T>`, and `m.keys()` held as `any`, iterated while entries are deleted and added: JS skips deleted entries and visits added ones; PTS iterates a snapshot. Typed controls pass.

### `iterator_result_getters_typed.pts` (C03 typed half — parent decides)
`next(): IteratorResult<number>` returning `{ get done(): boolean, get value(): number }` (the value getter throws if read on a done result) through `for…of` (with and without `break`), spread, `Array.from` and `new Set`, plus `get done(): true` with a throwing value getter. Node prints, for example, `for-of read done 0`, `for-of read value 0`, `for-of item 0`, …, `for-of read done 2` (never `read value 2`), `spread [ 0, 10 ]`, `Array.from [ 0 ]`, `Set 1`, `finished read done`, `end`. Today: honest source-located PTS1001 (D-146's known gap), no panic. It stays a candidate run test; if the parent keeps a diagnostic instead, the wording issue is V08/H22's (internal `objectN` names).

### `iterator_next_result_getters.pts` (N3, new — parent decides)
`it.next()` on a typed `Iterator<number>` whose `next(): any` returns getter results: Node returns the object, so getters run when and in the order the caller reads them (`first done: false` then `read value 0`; only `read done 1` for the second result; `result.value, result.done` reads value first; an unread result runs no getter). Today the result is converted to a `{value, done}` shape at the call, reading `done` then `value` even when done, which throws here. It may be judged a D-109 representation limit; the file isolates it from C03 by using `next(): any`.

## Expectations are reachable

Each twin changes only the RED construct; `tools/twincheck.sh` builds it on the base and compares its stdout with the committed expectation: all nine identical, every build and run exit 0. `tools/mktwins.sh` derives eight twins by `sed` from the test sources:

| Twin | Change |
|---|---|
| `getter_void_never` | getters typed `undefined`/`number` instead of `void`/`never` (and `return;` → `return undefined;`) |
| `getter_void_interface` | getters typed `undefined` |
| `return_bare_undefined` | every `return;` → `return undefined;` |
| `any_iteration_lazy` | the same values typed `Iterable<number>` at each loop (typed `for…of` is lazy and closes today) |
| `array_pattern_lazy` | hand-written: each pattern replaced by `take(values, count)`, a typed `for…of` that breaks after `count` items, which is the order of JS's pattern (`next()` per element, `return()` if not done); nested patterns step the outer loop by hand; `Generator<number>` parameter patterns become parameter declarations |
| `iteration_unbounded_lazy` | typed loops and `take()` for patterns |
| `iterable_collections_live` | the typed `Map`/`Set` variables in the loops |
| `iterator_result_getters_typed` | `next(): any` instead of `next(): IteratorResult<number>` |
| `iterator_next_result_getters` | the iterator held as `any` (a dynamic `next()` returns the object itself) |

## New findings (minimal repros, Node vs base)

- **N1. A generator or runtime iterator held as `any` has no methods.** `function* g(): Generator<number> { yield 1; } const handle: any = g(); console.log(handle.next());` — Node `{ value: 1, done: false }`; PTS exit 1, `Uncaught TypeError: handle.next is not a function`. Same for `handle.return(7)`, for `m.keys()` held as `any`, and `typeof handle.next` is `undefined`. The parent's suggested `const it: any = m.keys(); const [first] = it; it.next().value` therefore fails for this reason too; the tests continue through typed handles to stay on C18. Not covered by a test file here.
- **N2. Maps and sets iterated through `any` or `Iterable<T>` are snapshots** (root cause above). Test: `iterable_collections_live.pts`.
- **N3. `it.next()` on a typed iterator reads the result eagerly** (root cause above). Test: `iterator_next_result_getters.pts`.
- **N4. A pattern element past the end of an `any` value throws.** `const pair: any = [1, 2]; const [p, q, r] = pair; console.log(p, q, r);` — Node `1 2 undefined`; PTS exit 1, `Uncaught RangeError: Array index 2 is out of range (length 2); if it may be missing, read it with \`?? fallback\`, …`. Same for an `any` string or generator. D-038 gives `undefined` where any value can go and D-109's known gap is about typed locals, so this looks like a defect (H12 or H11 territory). The C18 tests avoid it by giving past-the-end elements defaults.
- **N5. `yield*` doesn't pass the inner result object through.** A sync generator's `yield*` hands the inner `next()` result to the caller as is (GeneratorYield(innerResult)), so with getter results Node reads `read done 0` twice (once in `yield*`, once in the consumer); PTS reads it once. Repro: `scratch/c18/p8_yield_star_passthrough.pts` (inner `any` iterable with logging getters, `const result = yield* inner;` in a `for…of`). Minor; not covered by a test file.
- **Known, out of H03's scope, confirmed:** defaults for items that are exactly `undefined` aren't applied (`function* gives(): Generator<number | undefined> { yield undefined; } const [a = 7] = gives();` — Node `7`; PTS `Uncaught TypeError: Expected number, got undefined`; with `any` items PTS prints `undefined`) — H12. Destructuring assignment `[a, b] = counted();` is `PTS1001: destructuring a value with an array pattern is not supported yet`. `typeof` of any `void` expression (`function nothing(): void {} console.log(typeof nothing());`) is `PTS1001: typeof on void is not supported yet` — honest, no test here.
- **Correction to the brief:** a `Generator<T>` **parameter** used as a pattern declaration's initializer (`function f(g: Generator<number>) { const [a, b] = g; }`) is lazy and closes today; the parameter **pattern** `function f([a, b]: Generator<number>)` and `for (const [a, b] of gens)` with `gens: Generator<number>[]` drain. Both kinds are in the tests (control and RED).

## Incident during reproduction

A draft of `any_iteration_lazy` had an arithmetic slip that made one object iterator infinite; its first run wrote 434 MB (Node) and 5.6 GB (PTS) of stdout under `/tmp/pts-h03-repro/out/` before the 60 s/120 s timeouts killed both processes. The files were deleted at once (disk back to 130G free) and every later run capped written files at 20 MiB. The committed tests are all bounded (Node and the base finish in under a second each).
