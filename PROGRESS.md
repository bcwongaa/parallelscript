# ParallelScript progress

Read this first every session, then `docs/MASTER_PLAN.md`, `docs/DECISIONS.md` and the relevant parts of `docs/SPEC.md`.

## Current state

- **Milestone:** M2 (Data and memory); M0 and M1 tagged `m0`, `m1`
- **Next task:** M2.4c Array methods with callbacks
- **Fork base:** `typescript/v7.0.2` (see `docs/FORK.md`)
- **Main is green:** yes, locally and in CI (`PTS` workflow)

## Done

- M2.5a Closures and arrow functions: function types as `pts_closure *` values (descriptor, code, captures); arrow functions, function expressions (named ones see themselves) and nested function declarations (hoisted to their block) capture enclosing variables by value, or in shared cells when they may change; for loops copy `let` cells per iteration; arrows use their method's `this` (and `super.m()`); function declarations as values; calls of function values; adapters between function types (fewer parameters, void results, class parameter types) and arrow literals shaped by their context; `console.log` (`[Function: name]` with JS's NamedEvaluation names), `typeof`, `===` (D-041, SPEC §9.20). M2.5 split by D-040 (M2.4c next, then M2.5b `this` in methods, M2.5c `Map`/`Set`). Fixed: a closure using its variable in its own initialiser was silently dropped.
- M2.4b Array methods without callbacks: `split`, `slice`, `splice`, `concat`, `indexOf`, `lastIndexOf`, `includes`, `join` (and arrays as strings), `reverse`, `sort` (stable, by the elements' strings), `flat` (constant depth), `fill`, `Array.from` of an array or a string, all matching Node; class instances convert to strings with their `toString()` (D-039, SPEC §9.19). Fixed: a recursive array type crashed the compiler (now PTS1001).
- M2.4a Array basics: `T[]` arrays with typed, unboxed element buffers, race-safe per brief §2.5 (capacity in the buffer, buffers published before lengths, unwritten pointer slots read as holes); literals, `length` (and shrinking it), `a[i]` reads by D-015's rule and writes (in range or appending), `push`, `pop`, `shift`, `unshift`, `pop()!`/`shift()!`, `for...of` over arrays, `Array.isArray`, and `console.log` of arrays ported from Node (grouping into columns, `... n more items`, `[Array]`, cycles) (D-038, SPEC §9.18). M2.4 split by D-037 (callback methods after M2.5); arrays of integer types moved to M2.6.
- M2.3b2 Methods and accessors through interfaces: interfaces and type literals with methods and accessor signatures; class methods provide interface methods and getters/setters provide properties (same IR types), called through per-site caches over each descriptor's table of entry points; properties some class provides with accessors go through `PTS_GET`/`PTS_SET`, others stay plain field sites; writing a getter-only property through an interface throws TypeError as in JS; `?:` of unrelated classes meets in the contextual interface (D-036, SPEC §9.17). M2.3 is complete. Fixed: the semantic pass didn't type `#private` fields, so a `#count: u8` read as a number.
- M2.3b1 Interfaces over fields: object types are structural interfaces (one-word pointers); class instances and objects of other shapes whose fields provide an interface's properties (with the same types) convert to it; property access through per-site caches of (descriptor, offset), race-safe; contextually typed literals take the context's shape (D-035, SPEC §9.16). M2.3b split by D-034.
- M2.3a Object literals and exact object types: object literals as anonymous struct types (shapes) shared by structurally identical object types, one descriptor per property order so `console.log` prints Node's `{ x: 1 }` in each literal's order, property reads and writes, the numeric rules through object types and literal contexts (D-033, SPEC §9.15). M2.3 split by D-032 (optional properties moved to M3.1).
- M2.2d2 Statics and `#private`: static fields as module-scope globals initialised where the class declaration runs (TDZ included), static methods and blocks, `this` in static code, inherited statics read through subclasses, `#private` fields and methods (hidden from `console.log`), `#x in o` (D-031, SPEC §9.14). M2.2 (classes) is complete.
- M2.2d1 Accessors and parameter properties: getters and setters (overridable, dispatched through the method tables, `super.p`), compound assignment and `++` on accessor properties with one getter call, abstract fields implemented by subclass fields or accessors, parameter properties in Node's property order, `private`/`protected`/`readonly` (D-030, SPEC §9.13). M2.2d split by D-029.
- M2.2c Inheritance: `extends` (across modules too), `super(...)` with the subclass's field initialisers after it, implicit constructors that forward their arguments, `super.method()`, overriding with typed method tables (`pts_vtable_<C>`, two loads per virtual call) only for hierarchies that override, direct calls wherever no subclass of the static type overrides, abstract classes and methods, `instanceof`, narrowing to subclasses, redeclared fields, overrides inheriting numeric result types (D-028, SPEC §9.12).
- M2.2b `console.log` of instances: Node's `util.inspect` for class instances, ported to the runtime over the type descriptors (line breaking, depth, circular references, string quoting), byte for byte equal to Node on the corpus (D-027).
- M2.2a Class basics: classes at module scope with fields (declaration order, initialisers), constructors, methods, `new`, `this`, field reads and writes (compound, `++`), method calls, across modules, with the numeric rules (D-026, SPEC §9.11). M2.2 split by D-025.
- M2.1 GC and the object model: `pts_type` descriptors carry the GC layout, size, fields and methods; `pts_object_new` allocates gcj-style (precise, thread-local) or atomic by layout; bdwgc built with gcj support (D-024, SPEC §9.10). C tests cover collection and threads allocating while another collects.
- M1.6 Milestone M1: checklist run, CI green on `4b7eb10`, M1 table in `docs/BENCHMARKS.md`, tag `m1`, Telegram report.
- M1.5 Benchmarks setup: `pts/bench/run.sh` (hyperfine; ParallelScript, Node, Bun and Go on the same program, outputs checked equal), `fib` and `fib_int`; results in `docs/BENCHMARKS.md` (fib: 3.0× Node, 1.14× Go). nbody moved to M2.7 (D-023).
- M1.4b2 Modules: `import`/`export` in every static form between `.pts` files (named, default, namespace, side-effect, re-exports, `export *`, `export * as`), live bindings, `export default <expression>` as a snapshot binding, modules run in ES evaluation order with cycles as in Node (TDZ ReferenceError included), C names qualified per module (D-022, SPEC §6, §9.9). Fixed: a function declared above an integer-typed module-scope binding it uses got a wrong PTS2001. The test harness runs multi-module tests under Node (helper modules in `run/<name>/`).
- M1.4b1 Module-scope bindings in functions: top-level `let`/`const` that functions use are C globals with a ready flag, so a use before the declaration throws ReferenceError as in Node; function bodies are lowered after the top-level code (D-021, SPEC §9.8). M1.4b split by D-021.
- M1.4a2 Functions: declarations (hoisted, nested if they capture nothing), parameters and returns with the numeric rules, inferred integer result types, recursion, calls in JS order; top-level code is now `pts_main`; the features left out by design (SPEC §3.10) are PTS1002 errors from `pts/check` with the reason (D-020, SPEC §9.8).
- M1.4a1 Statements and operators: `if`, `while`, `do`, `for`, `switch` (any type, fallthrough), labels, `break`/`continue`; assignment, compound assignment, `++`/`--`, `?:`, `&&`/`||`, comma, `typeof`, `void`, bitwise operators (JS int32 on numbers; D-018 on integer types), truthiness; the numeric rules on all of them; JS's left-to-right evaluation order kept in C with temporaries where it matters (D-019, SPEC §9.7). Plan split D-017.
- M1.3b String methods: `s[i]` (JS's `undefined` where the context takes any value, `RangeError` where a `string` is required, D-015), `charCodeAt`, `slice`, `substring`, `indexOf`, `includes`, `startsWith`, `endsWith`, `toUpperCase`/`toLowerCase` (full Unicode mappings and Final_Sigma from tables generated out of Node's ICU), `trim*`, `padStart`/`padEnd`, `repeat`, with UTF-16 indices (D-016, SPEC §9.6). Generated differential corpus `TestGeneratedStringCorpusMatchesNode`. Fixed quadratic local naming in `cgen` for many same-named locals.
- M1.3a Booleans and string basics: `!`, concatenation with ToString, template literals, `.length`, string equality and ordering (UTF-16 order), lone surrogates kept as WTF-8, joined on concatenation and printed as U+FFFD (D-014, SPEC §9.5). The lone-surrogate known issue is fixed.
- M1.2 Integer types: prelude aliases; the semantic pass `pts/check` (numeric types from annotations, untyped literals typed by context, lossless-only implicit conversions, PTS2001–PTS2003); wrapping integer arithmetic, truncating `/`, `RangeError` on division by zero and negative exponents; Rust `as`; exact 64-bit printing (D-012, SPEC §9.4).
- M1.1 Numbers: arithmetic, unary minus/plus, comparisons and equality on numbers (and equality on booleans), `NaN`/`Infinity`; Ryu (`pts/third_party/ryu` v2.0) for Number::toString; `-ffp-contract=off`; generated differential corpus against Node (D-011, SPEC §9.3).
- M0.8 Milestone M0: checklist run, CI green (run 38105342986 on `057efbe`), hello-world baseline numbers in `docs/BENCHMARKS.md`, tag `m0`, Telegram report.
- M0.7 CI: `.github/workflows/pts.yml` (PTS tests with the Node differential required, touched upstream suites, upstream CLI/LSP suites); upstream `ci.yml` on demand only, dependabot version PRs off (D-010, `docs/FORK.md`).
- M0.6 Test harness: corpus in `pts/tests/run` and `pts/tests/errors` with directive comments (format in `pts/tests/README.md`, D-009); `TestRun`, `TestRunMatchesNode` (Node differential) and `TestErrors` in `go test ./pts/tests/`; root `Makefile` with `build`, `test`, `test-diff`, `test-upstream`, `bench`.
- M0.5 Pipeline end to end: `pts/lib` (embedded prelude), `pts/frontend.Load`, `pts/diagnostic`, `pts/ir`, `pts/lower`, `pts/cgen` (C with `#line`), `pts/driver`, and the `cmd/pts` CLI (`build`, `run`, `emit-c`). Hello world compiles and runs; `pts run` takes about 0.2 s. Pipeline notes in SPEC §9.2; decisions D-005 to D-008.
- M0.1 Memory files: `docs/SPEC.md`, `docs/DECISIONS.md`, `docs/FORK.md`, `PROGRESS.md`, `docs/BENCHMARKS.md`, `.gitattributes` entry for `.pts`.
- M0.4 Runtime skeleton: `pts/runtime` (C11: values, strings, number formatting, `console.log` of primitives, panics), bdwgc v8.2.12 and libuv v1.53.0 submodules, `pts/toolchain` (cached clang builds, D-003; layout in SPEC §9.1).
- M0.3 `.pts` in the front end: `// PTS:` edits in `tspath`, `core`, `ast`, `module` (D-001, ledger in `docs/FORK.md`); tests in `pts/frontend/pts_extension_test.go`.
- M0.2 Study notes: `docs/agent/NOTES-tsgo.md` (where the front end lives, building a Program, the `.pts` touch points, upstream suites) and `docs/agent/NOTES-rayon.md` (join, Chase–Lev, adaptive splitting, helping while waiting).

## Next

1. M2.4c Array methods with callbacks: `map`, `filter`, `reduce`, `forEach`, `find`, `findIndex`, `some`, `every`, `flatMap`, `sort` with a comparator, `Array.from` with a mapper. Callbacks are `ir.FuncType` values (D-041); an arrow literal argument is lowered at its contextual type's shape (`contextualFuncType`), but `map`'s contextual type has the type parameter `U`, which `functionType` can't map yet: the lowering will need the instantiated signature (`GetResolvedSignature` of the call) to type the callback and the result array. Generated code can call the callback inline in a loop, or the runtime can call it per element kind.
2. Optional cleanup: the generated corpora could put each case in a function now; tsc's flow analysis and clang slow down sharply on one huge top-level function (`pts/tests/README.md`).

## Build and test commands

Run from the repo root. Go, Node, Bun and clang are on PATH (see `CLAUDE.md` → Environment).
A fresh clone needs the vendored C libraries: `git submodule update --init pts/third_party` (don't init `_submodules/TypeScript`; it's huge and only upstream's `TestSubmodule` uses it).
Built C archives are cached in `~/.cache/pts/` (override with `PTS_CACHE_DIR`); deleting it is always safe.

| What | Command | Baseline (2026-10-11) |
|---|---|---|
| Build everything | `go build ./...` | ok, about 3 min cold, seconds warm |
| Front-end upstream tests | `go test ./internal/tspath/... ./internal/module/... ./internal/core/... ./internal/parser/... ./internal/scanner/...` | 5/5 packages ok |
| PTS Go tests (front end, lowering, C emitter, driver, runtime C unit tests under ASan/UBSan, number-format differential) and the CLI | `go test ./pts/... ./cmd/pts/` | ok (about 10 s with a cold cache) |
| Build the CLI | `make build` (= `go build -o pts-bin/pts ./cmd/pts`; `pts-bin/` is ignored), then `pts-bin/pts run file.pts`. A copy outside the repo needs `PTS_HOME` set to the repo root. | ok |
| Corpus only | `go test ./pts/tests/` (run, differential and error tests, the generated number and string corpora); `make test-diff` for the Node differential tests alone | ok (74 run tests, 1 skipped; 43 error tests; 5 number-corpus and 11 string-corpus parts; about 25 s on an idle machine) |
| All PTS tests | `make test` (= `go test ./pts/... ./cmd/pts/`) | ok |
| Touched upstream suites | `make test-upstream` | ok (see the rows above) |
| Upstream suites the fork touches | `go test ./internal/tspath/... ./internal/core/... ./internal/ast/... ./internal/module/... ./internal/compiler/... ./internal/tsoptions/... ./internal/checker/... ./internal/modulespecifiers/... ./internal/parser/... ./internal/scanner/... ./internal/binder/...` | ok |
| Upstream CLI/LSP suites | `make test-upstream-cli` (= `go test ./internal/execute/... ./internal/project/... ./internal/ls/... ./internal/lsp/...` with the known failure skipped; slow) | ok |
| CI | `gh run list -L 3 --workflow PTS` (the repo's default for `gh` is set to `bcwongaa/parallelscript`; without that, `gh` picks the `upstream` remote) | green on `057efbe`: both jobs about 4.5 min |
| Local compiler baselines | `go test ./internal/testrunner/ -run TestLocal` | ok (about 324 subtests; the TypeScript submodule is not checked out, so `TestSubmodule` skips) |

`make bench` (= `pts/bench/run.sh [name …]`) builds every benchmark under `pts/bench/`, checks the implementations agree, runs hyperfine and prints the table for `docs/BENCHMARKS.md` (needs hyperfine, Node, Bun, Go and python3; about 30 s for fib and fib_int).

## Known issues

- **Captured variables have no TDZ check:** a closure that runs before the declaration of a variable it captures has run (e.g. a hoisted function called above a `const` it reads) sees the type's zero value, and a NULL string or object crashes, where Node throws `ReferenceError: Cannot access 'x' before initialization`. Needs a ready flag for the cells that the cell pass marks as captured early.
- **Overrides must keep the overridden method's parameter and result types** (PTS1001 otherwise): no covariant class results, no overrides with fewer parameters (D-028). A base constructor that calls an overridden method reading a subclass field sees the field's zero value (`""`, 0, NULL for an object), where Node sees `undefined`, since the subclass's initialisers run after `super()` returns; `console.log(this)` there lists the subclass's fields too, where Node doesn't yet.
- **Classes have no TDZ check:** `new C()` in a function called before `class C` has run works, where Node throws `ReferenceError`. And a class-typed field read before the constructor assigns it is NULL, so it crashes (tsc rejects most such reads; a method called from the constructor before the assignment can still do it). Both need the null and ready checks that come with unions (M3.1) or a class ready flag.
- **Stack overflow crashes** with SIGSEGV (exit 139) instead of throwing `RangeError: Maximum call stack size exceeded`; `pts/tests/run/stack_overflow.pts` is skipped until it does. Needs a guard (a depth counter or an alternate-stack SIGSEGV handler); natural with exceptions (M3.2).
- **Indexing a non-ASCII string is O(n)** (a scan from the start); ASCII strings are O(1). A lazy UTF-16 index needs a GC-traced field in `pts_string` (D-016): M2.1 or M8.
- **Integer constant expressions wrap at run time** instead of failing to compile: `const b: u8 = 200 + 56` is 0 (D-012). A literal operand of `as` is a `number`, so `9007199254740993 as int` is 9007199254740992; annotate instead (`const x: int = 9007199254740993` is exact).
- **Upstream, pre-existing:** `go test ./internal/execute/tsctests/ -run TestTscCommandline/tsc/show_help_with_ExitStatus.DiagnosticsPresent_OutputsSkipped` fails at the fork base too (verified with the PTS edits stashed). The help header's padding depends on the real version string, which changed on the 7.1 nightly branch after the baseline was written. Not ours; CI excludes it (M0.7).

## Needs from owner

(none)

## Session log

- 2026-10-11: M2.5a done (M2.5 split by D-040).
- 2026-10-11: M2.4b done.
- 2026-10-11: M2.4a done (M2.4 split by D-037).
- 2026-10-11: M2.3b2 done.
- 2026-10-11: M2.3b1 done (M2.3b split by D-034).
- 2026-10-11: M2.3a done (M2.3 split by D-032).
- 2026-10-11: M2.2d2 done.
- 2026-10-11: M2.2d1 done (M2.2d split by D-029).
- 2026-10-11: M2.2c done.
- 2026-10-11: M2.2b done.
- 2026-10-11: M2.2a done (M2.2 split by D-025).
- 2026-10-11: M2.1 done.
- 2026-10-11: M1.6 done; tagged `m1`.
- 2026-10-11: M1.5 done (nbody moved to M2.7, D-023).
- 2026-10-11: M1.4b2 done.
- 2026-10-11: M1.4b1 done (M1.4b split by D-021).
- 2026-10-11: M1.4a2 done.
- 2026-10-11: M1.4a1 done (M1.4a split by D-017).
- 2026-10-11: M1.3b done.
- 2026-10-11: M1.3a done.
- 2026-10-11: M1.2 done.
- 2026-10-11: M1.1 done.
- 2026-10-11: M0.8 done; tagged `m0`.
- 2026-10-11: M0.7 done.
- 2026-10-11: M0.6 done.
- 2026-10-11: M0.5 done. Plan change D-008: M1.4 split into M1.4a (control flow) and a new M1.4b (modules).
- 2026-10-11: M0.4 done.
- 2026-10-11: M0.3 done (two tsc baselines re-accepted for the new `.pts` entry in TS6231).
- 2026-10-11: M0.2 done.
- 2026-10-11: M0.1 done. Baseline: build ok; front-end package tests 5/5 ok.
