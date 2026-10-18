# H08c reproducer report: spreads before a later operand that changes them (C152); spreads through `any` (C153)

The `opus-reproducer` child (claude-opus-5-5, xhigh) wrote this as its final message; the parent saved it verbatim apart from this heading line's provenance note. It ran in its own worktree (`.claude/worktrees/agent-ada8309aafb8cc260`, left clean, nothing committed) against the base `faa56928b`. Its archive is [h08c-reproducer-probes.tar.gz](h08c-reproducer-probes.tar.gz) (sha256 `099854ab…`; paths below are inside its `repro/`). The parent's dispositions are in [h08c.md](h08c.md).

---

I've reproduced both defects on `faa56928b`. Three RED tests are ready and all fail on output, not on build. Sites that get a spread right today are listed in the inventory below.

- **C152:** at every place that appends a spread array, the array's elements are read only after the later operands have run.
- **C153:** a call through `any` throws for every iterable except arrays, tuples and typed arrays.

One cgen change fixes most of C152 (typed calls and literals). A lowering change to how calls through `any` and console spread their arguments fixes the rest of C152 and all of C153.

## Setup (verified)
- **Base:** my worktree `/home/bryan/projects/parallelscript/.claude/worktrees/agent-ada8309aafb8cc260`, commit `faa56928bff282e57687a43edd645247de159e03`. Submodules were added with `--reference` one at a time. `GOMAXPROCS=2 make build` exited 0.
- **CLI:** `pts-bin/pts` sha256 `2c8f5fb0f57d3973451272b6fccd58f1ff6ab89409bf070fd8bcf695984e3c77`.
- **Toolchain:** Node v24.21.0, go1.26.9, clang 18.1.3.
- **How I ran programs:** `tools/cmp.sh` builds and runs with `PTS_HOME=<worktree>`, and runs Node on an `.mts` copy with `node --no-warnings --experimental-transform-types`.
- **Load:** never more than 2 heavy jobs at once. The worktree is clean and nothing was committed.

## 1. RED: the skipped test without its skip line (verified)
File: `red/skipped_test.cmp.txt`. Both sides exit 0. 10 of 13 lines differ from Node:

| case | PTS | Node |
|---|---|---|
| `list(...xs, grow())` | -5/2/100/10 | 1/2/10 |
| `[...xs, grow()]` | -5/2/100/10 | 1/2/10 |
| `[0, ...xs, grow(), ...xs]` | 0/-5/2/100/10/-5/2/100 | 0/1/2/10/-5/2/100 |
| `new Made(...)`, `Array.of(...)` | -5/2/100/10 | 1/2/10 |
| `out.push(...)` | 0/-5/2/100/10 | 0/1/2/10 |
| `Math.max` / `Math.min` | 100 / -5 | 10 / 1 |
| `console.log(...xs, grow())` | -5 2 100 10 | 1 2 10 |
| `any(...xs, grow())` | -5/2/100/10 | 1/2/10 |

The three matching lines are guards: `three(...pair, …)`, `[...set, add()]` and `{ ...o, z: bump() }`.

## 2. Causes, file:line on faa56928b
- **ArraySpread's C (verified, generated C in `inv/c_shape.c:123`).**
  - `arrayCall` evaluates every operand first (`pts/cgen/arrays.go:31`). When order matters, `operands` puts each spread array into a temporary as a reference only (`cgen.go:1223-1237`).
  - Only then does `concat` copy and append (`arrays.go:138-152`, the append at `:148`). So `pts_array_append(t, xs)` runs after `grow()`.
  - Every typed call reaches this path: `argumentValues` hands over the array itself (`lower/lower.go:1975-1976`), then `restArray` builds an `ArraySpread` (`:2039`). Literals reach it through `spreadArray` (`lower/arrays.go:166`).
  - `a.values()` lowers to the array itself (`array_library.go:233-234`).
- **SpreadArg (verified, `c_shape.c:124-125`).**
  - The value is only boxed (`cgen.go:894-895`). `callBuiltin` evaluates all operands (`:1562-1580`). The runtime then reads the spread at the call: `dynamic.c:769-793` and `console.c:86-104`.
  - Lowering does no iteration for these: `dynamic.go:172-178` passes the raw value, and `console.go:66-67` passes the array.
- **C153 (verified).** `pts_spread_length` (`dynamic.c:740-753`) accepts arrays, tuples and typed arrays only, and throws `Cannot spread X …` for anything else.
  - This contradicts D-078 as well as SPEC §3.4. D-078 says that spreading a dynamic value "into … a call through `any`" takes `pts_value_to_array`'s items. D-075 describes the arrays-only `SpreadArg` that is implemented.
  - Without a suspension there is no `DynamicCallFound`: it is only made when the callee is kept (`dynamic.go:161-163`).

## 3. Inventory (verified by probes and the matrix unless marked)
**Wrong (C152), every site:**
- Program functions: arrow, declared, generic, function value, closure, IIFE, optional call `f?.()`, `.call`.
- Methods: plain, generic, static, interface, a union's, `super.m`, `this.m`, a function-typed property, a method through `?.`.
- Constructors: `new`, `super(...)`.
- Builtins: `Math.max`, `Math.min`, `Math.hypot`, `String.fromCharCode`, `String.fromCodePoint`, `Array.of`, `new Array`, `push`, `unshift`, `Object.assign(t, ...anyArr, x)`.
- Array literals: plain, element before, two spreads, the same spread twice, a nested inner literal, in a template, as a destructuring source, iterated by `for…of`, inside `new Set`/`new Map`/`new Float64Array`/`Array.from`.
- Console methods: log, info, debug, group, warn, error, assert.
- Calls through `any`: function and method.
- Later operands that trigger it, besides a call: a getter, a later spread of a generator, `...xs.splice()`, a later spread of a call.

**Element kinds that are wrong everywhere:** arrays (property, call result, readonly, `string[]`, objects, pairs), `a.values()`, holes in `(number|undefined)[]` (Node keeps undefined), `xs ?? []`, and a later operand that runs `pop` or `length = 0`.

**Wrong only through `any`:** tuples, rest tuples, typed arrays, arrays/tuples/typed arrays held in `any`, `number[] | Set` holding an array, `Iterable<number>` holding an array.

**Right on base (also verified):**
- Reassigning the variable (`xs = [9]`): the temporary still holds the old array.
- Tuples, rest tuples and typed arrays at typed sites and in `console.log`. Typed sites read tuples per element (`FieldRef`) or slice them at the spread, and copy typed arrays with `pts_typed_array_to_array`. H08b's report said "tuples and typed arrays … through console.log"; that part is wrong (`console/log_tuple_write`, `log_typed_write`, `log_rest_tuple_write` are OK).
- `a.keys()` and `a.entries()`: they make a new array.
- `any`, unions, iterables and generators: `ValueToArray` copies at the spread.
- Sets, maps and strings in literals: `mapItemsArray` and `ArrayFromString` copy at the spread.
- An array of another element type in a literal (a checked copy).
- `[...[...xs], g()]`, a whole `[...xs]` argument, a tuple into a tuple literal, `str.concat`, object spreads.

**Compile errors (PTS1001) at these sites:** `splice` (D-058), `new` through `any` (a class as a value), and a typed Set/Map/string into typed arguments or console (D-057).

**C153 (verified):** all of the following throw `Cannot spread X into the arguments of a call through any`, typed or held in `any`, for both function and method callees: sets, maps, set/map/array/typed-array iterators, strings, generators, class iterables, object-literal iterables, `Iterable<number>` of a set, `number[] | Set` holding a set.
- **Callee found before an await:** typed set/map/string throw there too. Generator, class iterable and an `any` set work, because H08b drains them first. So the two forms disagree.
- **Typed set/map/string:** the call through `any` is a runtime TypeError, not PTS1001. The same spread into `console.log` is PTS1001, but `console.log(...set.values())` works.
- **Non-iterables:** PTS throws `Cannot spread number …` after the later arguments have run. Node throws before them, with V8 messages that change by call site (three forms seen, `nodeonly/msgs.node.txt`).
- **The runtime already handles every kind:** `pts_value_to_array` handles every C153 kind when reached through a typed rest parameter of an `any` value (`c153/any_spreads_dynamic`, `value_to_array_kinds`).

## 4. Matrix (verified)
- **Changes from H08b's `matrix.py`** (now `matrix/matrix.py`, all marked H08c):
  - a `plain` mode (the statement in a plain function);
  - `MODES` and `VARIANTS` filters;
  - 9 kinds: `any` holding a string, a generator, a Map, a map iterator, an object-literal iterable, a tuple, a Float64Array; plus number[] and any-array cleared with `length = 0`;
  - 11 plain-only sites: hypot, fromCodePoint, `super`, generic method, union method, Float64Array literal, `Array.from`, `new` through any, and "spread again after" for a rest fn, an any fn and console.log;
  - `summarize_sync` and `classify_sync`.
- **Run:** `MODES=plain python3 -I matrix.py gen p1` gives 103 programs and 3,965 cases. The manifest sha256 is `a21a5b03…`, and regenerating it gives the same file. Then `VARIANTS=sync,const … run p1 2` (exit 0, about 8 minutes), `classify_sync`, and `analyze_h08c.py`. A first run crashed on a reporting KeyError after 22 seconds and was rerun from scratch.
- **Sync verdicts:** OK 2,323, WRONG 1,172, DIAG 446, ICE 6, TSERR 18, CRASH 0.

Every WRONG case is explained:

| label | cases | what it is |
|---|---|---|
| C152 | 925 | sync wrong, const right; PTS printed what Node prints with the mutation made first. One `new Array` case is the same bug, verified by hand |
| C153 | 117 | 39 kinds × 3 sites through `any`; the const twin throws too |
| C154 | 76 | optional tuple element (known, H29) |
| D-085 | 43 | number[] holes read 0 (intentional) |
| C148 | 11 | computed key of a literal type never evaluated (known) |

All DIAG/ICE/TSERR cases are the same in their const twin; the 6 ICEs are C155.

**By site:**
- Each typed call site, console.log and each literal site: 21 C152 cells, all of them array kinds.
- `any function` / `any method`: 37 C152 plus 39 C153 each.
- Smaller counts where the result depends on the values: Math.max/min 6, fromCharCode 14, hypot/fromCodePoint/Float64Array literal 13, `new Map` literal 2.
- No C152: nested spread, `[...xs]` argument, fixed parameters, head-plus-rest, tuple-typed literal, str.concat (tables in `matrix/p1/c152_tables.txt` and `analysis.txt`).

**Cross-check:** every one of H08b's 5,598 yield/await cells gives the same sync and const verdict as the plain run at the same site, kind and mutation, on both H08b's base and its fixed matrix.

## 5. Proposed RED tests (`/tmp/h08c/repro/tests/`)
Node prints each expect block exactly, exits 0, and writes nothing to stderr (`check_expect.py`).

1. **`spread_elements_read_before_later_arguments.pts`:** the existing test, skip line removed, all original lines kept. I added 9 lines for kinds it missed: `xs.values()`, `?? []`, a readonly view, `string[]`, an array of objects, a filled hole in `(number|undefined)[]`, `length = 0`, `console.log(...xs.values())`, and a replaced-variable guard. On base 18 of 22 lines differ; the 4 matching lines are guards.
2. **`any_call_spreads_iterables.pts` (C153), 61 lines:**
   - every iterable kind, typed and held in `any`, for function, method and found-before-await callees, plus a function before an await;
   - five cases where a later argument changes an array, set, tuple, typed array or `any` array (position);
   - non-iterables: only the error name is printed, and it pins that the later argument does not run.
   - Entries print with `JSON.stringify` to avoid side finding S2.

   On base 57 lines differ. The 4 matching lines are the after-await forms H08b already drains.
3. **`spreads_read_before_later_operands_at_every_site.pts` (C152 sites), 34 lines:** every site above that test (a) misses, plus 4 guards. On base 30 lines differ.

**Harness:** `GOMAXPROCS=2 PTS_REQUIRE_NODE=1 go test -count=1 -timeout 40m -v -run '^(TestRun|TestRunMatchesNode)$/^(<three>)$' ./pts/tests/` exits 1. All 6 subtests fail at `run_test.go:69`/`:98` on stdout diffs, with 0 build errors (`tests/red_harness_base.txt`). The files were then removed from the worktree by name, and the original restored with `git checkout --` on that one file.

**Simulated fix:** I ran copies with each spread rewritten `...[...E]` (a copy made at the spread) on the base CLI. They print exactly each expect block, so the tests ask for nothing beyond the position rule (`tests/simulated/`).

## 6. Side findings (verified, all pre-existing)
- **S1:** `new Array<number>(...[3]).length` is 1 in PTS and 3 in Node. Not H08c; H29 (`side/new_array_single_spread.pts`).
- **S2:** a tuple held in `any` has no array methods (`join`/`map` "is not a function"). The pairs from an array's `entries()` iterator are such tuples (`side/tuple_methods_any`, `entries_pair_any`). H22/H29.
- **S3:** `console.log`/`error`/`info(...typedSet|Map|string)` is PTS1001 as D-057 documents, where Node prints. This is outside C153 strictly; you decide whether H08c takes it (`console/`).
- **S4:** `f.apply(null, set)` through `any` throws a TypeError where Node calls with no arguments. Out of scope, but it matters for the fix (risk 3).
- **S5:** the C153 message is not Node's; only the name is pinned.

## 7. Fix risks (inferred unless marked)
1. **concat:** for `ArraySpread`, evaluate and append each part in statement order. Keep `ArrayConcat` evaluating all operands first, since JS's `concat` does. The first operand is always the empty `NewArray`. `readsHoles`/`append_as_read` don't change. No added cost, and GC behaviour is the same as today's statement-expression locals.
2. **Calls through `any`:** in `dynamicCallWith`, wrap each spread in `ValueToArray(box(x), text)`, which is what D-078 says. This fixes C152 through `any`, all of C153 and the throw order, and also covers H08b's kept copies on the `DynamicCallFound` path. It needs the spread's source text for the "is not iterable" message. For console, copy arrays at the spread (or only when order matters).
3. **Don't widen `pts_spread_length` / `spread_arguments` in the runtime.** `function_apply` uses them with array-like semantics: `apply(null, "ab")` must stay a TypeError, which matches Node today (verified). `array_concat`, own keys, `generator.c` and `pts_value_to_array` use them too.
4. **Don't iterate at the call.** `effectsOf(SpreadArg)` (`order.go:150`) treats a spread as a plain read. If the runtime iterated generators or iterables at the call, their code would run after later operands that cgen didn't order (`af(...gen, x)` where the generator sets `x`). Iterating at lowering time is classed `writes|mayThrow`, which avoids this.
5. **Cost:** one `any[]` per spread through `any`, and two copies per spread before a suspension, since H08b's copy is then copied again by `ArraySpread`.
6. **Holes keep reading as today** (0 for number[], D-085), verified. C154 through `any` might change once a tuple goes through `pts_value_to_array`; I didn't test that.

Files are in /tmp/h08c:
- h08c-reproducer-probes.tar.gz (sha256 `099854ab9375504ea7d24741fca021c1b43f66d9d7b5a75557711d8ce27dd9a0`, recorded in repro.done). It leaves out the copies of H08b's evidence archives and the regenerable `matrix/p1/work`.
- repro/tests/ (the three proposed tests; `simulated/` holds the rewritten copies)
- repro/matrix/ (matrix.py, analyze_h08c.py, p1/)
- repro/red/, repro/inv/, repro/c153/, repro/console/, repro/side/, repro/tools/
- repro/env.txt (commit, CLI sha, toolchain versions)
