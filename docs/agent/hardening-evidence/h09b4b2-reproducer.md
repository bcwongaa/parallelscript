# H09b4b2 reproducer report (C220's rest)

The `reproducer` subagent's final message (claude-opus-5-5, effort xhigh; its own worktree of `7668a5518`, left without source changes; 2026-10-21, about 54 minutes). Saved verbatim by the parent; HTML entities the message carried (`&gt;`, `&amp;`, `&lt;`) are written as characters. Archive: [h09b4b2-reproducer-probes.tar.gz](h09b4b2-reproducer-probes.tar.gz) (sha256 `3d091924…72d7`, 2,123 entries; `repro/INDEX.txt`).

---

I reproduced everything on the base: both skipped tests fail as their skip lines say (apart from one stale line, noted below), the four guards pass, and a 2,046-case matrix plus 10 RED and 4 GUARD tests are ready. The main result for the rule: a read that can't be stale is **not** enough to justify a trap. Several `const` and parameter reads that tsc types `never` run in Node with no cast. No compiler or runtime fix is in the deliverables, my worktree has no source changes, and nothing was committed.

**Setup.**
- **Base:** `7668a5518` in my worktree; submodules initialised from the parent's modules with `--reference`; built with `GOMAXPROCS=2 go build -p 2 -o pts-bin/pts ./cmd/pts`.
- **Toolchain:** go1.26.9 linux/amd64, Node v24.21.0, Ubuntu clang 18.1.3.
- **Node runs:** harness style (`.mts` copy, `--no-warnings --experimental-transform-types`, `TZ=America/New_York`).
- **Load:** at most 2 jobs at a time; every case binary was deleted right after its run (no `.bin` files left).

## Step 2: skipped tests and guards on the base
- `narrowed_dead_branches_plain.pts`: RED as its skip line says. The build exits 1 with 18 PTS1001 errors (`converting string to number`, `` `x.length` ``, `converting number[] to Pt`, `using a Pt as a Other`, `converting string to null`, `Box and Box` conditional, the `>` operator, `converting number | string to boolean`).
- `generic_typed_never_dead_branches.pts`: RED, but with 4 PTS1001 errors, not 5. Its `arrays<T extends number>` part, which the header says fails, **already runs on the base** (probe `g_arrays_only`); the plain twin `g_arrays_plain` does fail. That line of the header is stale.
- `narrowed_never_branches_that_run`, `narrowed_never_defaults_that_run`, `stale_narrowings_never`, `narrowed_stale_never_reads_plain`: all PASS.

## Matrix (base)
Commands: `python3 -I gen.py DIR`, then `run.py cases.json results-base.json --jobs 2`, then `summarize.py`. Each case also records `want`: either "node" (must match Node), or "node|trap" where a lie's value is used as its narrowed type and D-195's TypeError is the documented outcome. "Meets plan" counts cases already at their `want`.

| family | cases | OK | PTS1001 | TYPEERR | WRONG | other | meets plan |
|---|---|---|---|---|---|---|---|
| form (36 plain dead forms × 19 uses) | 612 | 458 | 152 | | | 2 TSERR (TS2358, intended) | 458 |
| lie (lies, enums, literal casts, D-090 loose locals × uses) | 348 | 258 | 58 | 24 | 8 | | 276 |
| stale (21 reference kinds × dead/lie) | 210 | 148 | 61 | 1 | | | 149 |
| stalelive (8 second tests × 12 change routes, plus no-change twins) | 300 | 209 | 51 | 40 | | | 209 |
| region (40 shapes × dead / lie-typed / lie-any) | 121 | 40 | 78 | 3 | | | 43 |
| tail (hoisted functions, captured bindings) | 11 | 0 | 10 | | | 1 BUILDFAIL (`var`, PTS1002 by design) | 0 |
| generic (code tsc already types never) | 220 | 171 | 47 | 2 | | | 173 |
| decl (20 declared types × 6 tests) | 224 | 138 | 84 | | 2 | | 138 |
| **total** | **2046** | **1422** | **541** | **70** | **10** | 3 | **1446** |

What the dimensions show:
- **Uses in dead regions (form family):** the typed uses fail: `x + 1`, a typed argument, a typed const, `?:` over boxes (1 of 35 OK), `x > 1`, a member read, and a cast to the tested type. Everything else is already OK on the base: an untyped copy, `new Box(x)`, every any-value use, `assertNever`, a `never` const, `return x` from a never function, a cast to the declared type, `as any`, `as unknown as T`.
- **Staleness:** all 21 reference kinds behave the same on the base. With a single declared type (string, number) the region is dead whether or not the narrowing can be stale. Probe `c02` shows why: aliasing through a wider property or tuple type is PTS1001, so a typed place can't hold another kind.
- **Lies:** every any-value use matches Node (96/96) and so does every cast to the declared type, any or unknown (48/48). Typed uses split 44 OK / 18 TypeError / 46 PTS1001. Cast lies through `unknown` or `any` at a call site never reach the region: the cast itself throws `TypeError: Expected string, got number` (probe `c01b`).

## RED tests (fail on the base; Node passes)
All are in `/tmp/h09b4b2/repro/tests/`. Results are in `tests-base.txt`; full build outputs (114 PTS1001 errors) are in `tests-base-builds.txt`.

| test | base failure |
|---|---|
| `narrowed_dead_plain_forms.pts` | PTS1001: 17 forms the skipped test lacks (`!Array.isArray`, `!(x instanceof Pt)`, a union member, `typeof … "undefined"`, `== null`, falsy array, an honest predicate to `number & unknown[]` or `number[] & Pt`, switch fallthrough, `!("k" in x)`, an aliased condition, boolean exhaustion). Also one unexplained error: `as expression (which the compiler failed to lower without saying why)`. |
| `narrowed_dead_plain_uses.pts` | PTS1001: typed uses over `never`, `number & any[]` and `Pt & Other`. |
| `narrowed_dead_plain_regions.pts` | PTS1001: 31 region shapes plus a generator and an async function. |
| `narrowed_dead_plain_references.pts` | PTS1001: every staleness kind of reference, dead, used as the narrowed type. |
| `narrowed_dead_plain_tail_bindings.pts` | PTS1001: hoisted helper after the dead statement, a closure made before it capturing the tail, later switch cases, a method, side effects before the dead statement. |
| `generic_typed_never_regions.pts` | PTS1001: `<T extends string>(x: T)` tested as a number, `T & string`, `T & number[]` with instanceof, `T & Pt` falsy, a predicate, a union, `?:` boxes, and a call through a generic value (`` `new Box(x).value` ``). |
| `stale_narrowings_live_intersections.pts` | PTS1001, live with no lie: `o.v.length` after a stale `typeof` then `Array.isArray`, through a property, a closure, a getter and an await; `using a Base as a Sub2` for `Sub1 & Sub2`; `?:` boxes. |
| `stale_narrowings_predicate_operands.pts` | Runtime TypeError, live with no lie: a type predicate's stale argument is converted to the stale narrowed type. `isCi(o.v)` gives `Expected {kind: string; side: number}, got Object`; `isArr(o.v)` gives `Expected number, got Array`. |
| `narrowed_never_destinations_keep_values.pts` | Wrong output, partly with no lie: a function declared `never` that returns an out-of-range enum value gives `undefined` (Node `7`); the same after a lying predicate. A D-090 loose local passed to `assertNever` throws `Expected string, got undefined` where Node throws `unexpected undefined`. |
| `narrowed_never_loose_locals_typed.pts` | PTS1001, no lie: `const u: undefined = y` and `und(y)` in a loose local's never branch (`converting string \| undefined to undefined`). |

## GUARD tests (pass on the base; must keep passing)
- `narrowed_never_enum_literal_defaults.pts`: a numeric enum holding 7 **with no cast** (`const c: Color = n`) reaches the exhaustive default, where `assertNever`, a never const, `c + 1`, `col(c)`, `const n: number = c` and templates all run. Also a literal union from `raw as Dir` with typed uses, and a discriminant lie through `shp(s)`.
- `narrowed_never_lies_that_print.pts`: lying predicates and an assertion narrowing to `never`, `number & unknown[]`, `number[] & Pt`, `Pt & Other` and null. The uses are every any-value form, a never const, `assertNever`, and casts back (`x as string`, `x as unknown as string`).
- `narrowed_never_loose_locals.pts`: D-090 loose locals' never branches: logging, String, concat, typeof, an unknown parameter, `??`, a never const, `new Box(y)`, `return y`.
- `stale_narrowings_never_routes.pts`: stale-and-live never reads through every change route (callback, nested property, tuple and array element, `this`, static, closure-assigned `let` and parameter, a module `let`, await, yield, getter), plus `=== null` and `!x` second tests, a sibling-class union, and `number & any[]` passed as an argument.

## Throwaway prototype (not a fix; `proto/`, overlay build, not in any tree)
The rule I tried: in plain code, a read becomes a trap only when all of these hold:
- tsc's narrowed type has no values;
- the reference can't be stale (not `mayHaveChanged`);
- it isn't a D-090 loose local;
- its declared type passes `literalFree` (no enum, literal or discriminant types);
- its context isn't `never` or any-value, and it isn't cast to its declared type, any or unknown.

A non-union place that may be stale and is typed uninhabited is boxed instead.

Results:
- Matrix: 1422 → 1686 OK; meets plan 1446 → 1810. Transitions: 278 PTS1001→OK, 91 PTS1001→TypeError.
- Tests: all 4 of my guards and all 4 existing guards pass; the regions and tail-bindings REDs pass fully.
- 14 cases went OK→TypeError, all of them lies that the base ran correctly:
  - `x + 1`, `x > 1` and `new Box(x)` where the declared type satisfies the use (`number & unknown[]` read as its number);
  - `return x` or `y = x` where the destination takes the declared type;
  - the copy `const y = x` (inferred never) followed by `String(y)`.
- 2 cases went PTS1001→WRONG: the trap's TypeError was caught by the program's own `catch` (a lie with a typed use).

What the prototype still leaves:
- `?:` with a dead branch: tsc types it `Box<never> | Box<string>`, so the whole needs the live branch's type, not only a trap for the dead branch.
- Generic code tsc already types never: `isDeadRead` deliberately skips it today, so it needs the trap too.
- A cast of a boxed read to its declared union (`` `as` from value to number[] | string ``).
- Members of stale `number & any[]` boxes, and class places narrowed to `Sub1 & Sub2`.
- Stale predicate arguments; returns from never functions; loose locals used as undefined.

## Side findings (repros in `probes/hand/`, outputs in `BASE.txt`)
1. `instanceof` whose left operand is a Map, Set, function or Record is PTS1001 for the test itself, where Node gives false (true for `m instanceof Map`). Repros `s02`, `s02b`. These are dead tests in this slice's own forms.
2. A `let x: string` that a closure reads before assignment reads `""`, and `x === undefined` is folded to false; Node reads undefined and runs a branch tsc types never, with no cast (`c06`). Two siblings behave the same: a definite-assignment field `name!: string` (`c04`), and a field read by an override during the base constructor (`c05b`). These aren't in the ledger.
3. `x as object | null` fails with PTS1001 "failed to lower without saying why", even in live code (`s03`). Low severity.
4. `Array.isArray` of a tuple-typed value is false, a silent wrong output (`s01`, matrix `decl-tuple-isarray`). This is already C79 (H29).
5. The stale `arrays` line in `generic_typed_never_dead_branches.pts`'s header (step 2).

## My view of the rule
**May be a trap.** A read whose tsc type has no values, from a const or a parameter/`let` only its own function assigns, used as its narrowed type in a way its declared type can't satisfy (`num(x)` or `x + 1` for a string x, `x.length` for a number x), and only when no value of the declared type's IR type passes the tests (typeof, instanceof, `Array.isArray`, null checks, truthiness, predicates, assertions on string/number/class/array declared types).

Even a read that can't be stale must not trap:
- **Enums:** they hold any number with no cast.
- **Literal unions and discriminated object unions:** the IR type holds other strings or kinds via unchecked or layout-only casts. Read the declared value, as the base does; typed uses that don't fit the declared type may still trap under such a cast.
- **D-090 loose locals:** they hold undefined; read their union.
- **Destinations that take the declared type:** returns, assignments, arguments of the declared type, and an inferred-never copy `const y = x`.

**Must read the value they hold.** Places that may be stale (properties, elements, `this`, getters, statics, variables another function or a closure assigns): box the value and convert with a check where it's used, including single-type declared places and their intersection members. A stale operand of any test, including a type predicate's or assertion's argument (`testsAnyValue` lacks these). And a never function's `return x`: its result must be the value, not void.

**Must keep running exactly as Node, even under a lie.** Any-value uses (console, String, JSON.stringify, templates, typeof, `===`, `+` with a string, `unknown` parameters); never destinations (`assertNever(x)`, `const u: never = x`, `return x` in a never function); casts to the declared type, any or unknown.

Everything is in `/tmp/h09b4b2/repro.tar.gz` (start with `INDEX.txt`); scratch files are under `/tmp/h09b4b2/repro/`, and the marker `/tmp/h09b4b2/repro.done` is written. Key files:
- `/tmp/h09b4b2/repro/matrix/summary-base.txt` (tables by family and dimension)
- `/tmp/h09b4b2/repro/matrix/compare-base-vs-proto.txt`
- `/tmp/h09b4b2/repro/tests/` (the 14 tests)
- `/tmp/h09b4b2/repro/proto/proto.patch`
