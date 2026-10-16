# H04 reproducer report

- **Child:** the named `opus-reproducer` subagent (Agent call with the `opus` alias, which the session's environment maps to claude-opus-5-5; effort xhigh from `CLAUDE_CODE_EFFORT_LEVEL`), 2026-10-16 21:4x to 22:3x, in its own worktree `.claude/worktrees/agent-ad1c2f8c7439f4e52` (branch `worktree-agent-ad1c2f8c7439f4e52`, base `243f89415`), with its own CLI and runtime (`PTS_HOME` set to the worktree). It committed nothing, pushed nothing and ticked nothing; its tests and probes stayed uncommitted in its worktree.
- **Its probes:** [h04-reproducer-probes.tar.gz](h04-reproducer-probes.tar.gz), the worktree's `h04-probes/` as it left it (1,011 files: `a/` the four ledger rows, `b/` variants, `c/` controls, `d/` the panic hunt, `scripts/`, `logs/`, `smoke-out/`, `overlay/`, `irdump/`).
- **What the parent took:** the ten run tests and `compiler_smoke_test.go` named below, byte for byte (sha256 below); then, in the parent's tree, `run/object_literal_iterator_accessors.pts` lost its `settable[Symbol.iterator] = …` write (an honest PTS1001 after the fix, pinned by `errors/symbol_iterator_element_writes.pts`), and the smoke test reports the internal errors `driver.EmitC` now returns, defaults to one mutant per seed and leaves `pts/diagnostic` out of its frames. The parent re-ran every RED claim itself on the base compiler and runtime ([h04.md](h04.md), RED).

```
2b5521c6b0520f243ea45387ceae1e8dc6790290b0c3745d08b4004eb3ae567e  pts/tests/run/keyed_assign_value.pts
56700d2539e12b7d2eb490062b3e872187382c2736afa405bfc7c6f47ef3782b  pts/tests/run/keyed_assign_value_types.pts
38b229a38ea6182842a99804bf39e6c25f91e4d50049625fcdc14344044595c2  pts/tests/run/dynamic_assign_value_types.pts
a6f2843877698183e3fd497e85c4455f35dc68b71db4d686d32826218e22e499  pts/tests/run/object_literal_iterator_accessors.pts
aa55125a5b61a1d3e350ffa7872d976b7dab160a01e9fb7039025be1a9bca933  pts/tests/run/narrowed_tuples_of_dynamic_unions.pts
8d1535aace5741b79fc04182e2a69eac37b9bcb865bfa127007b626c39bc197e  pts/tests/run/narrowed_arrays_of_dynamic_unions.pts
a07fcaca708fd4c8e7c1b25f2dc8c89fb461b8cc0c8315650927a99e35568afd  pts/tests/run/narrowed_records_of_dynamic_unions.pts
85ad8047f671d10ef8343a7fe4dfa0f8d622926af94feb7dac9a390557b6608e  pts/tests/run/object_rest_empty.pts
21a426fc171eb9f43dd0c18ee6d5cdc6808334896ac48f127c88f9233b18af72  pts/tests/run/object_rest_assignment.pts
b07fb0786060232adcf8555b05b594b468ee937d7ab1faff9dc13df04ddc1acc  pts/tests/run/interface_method_tuple_rest.pts
```

The child's final report follows unchanged.

---

All four ledger rows reproduce on base 243f89415. Each has RED run tests with Node's expected output that fail for the right reason. The panic hunt found one root-cause family much wider than C26/C39, plus three separate new panics. Nothing is committed: everything is uncommitted in my worktree, nothing pushed, no tasks ticked.

## Worktree and files
- Worktree `/home/bryan/projects/parallelscript/.claude/worktrees/agent-ad1c2f8c7439f4e52`, branch `worktree-agent-ad1c2f8c7439f4e52`. The five third-party submodules are initialised with `--reference`; CLI built at `pts-bin/pts` (ignored by git).
- Changed: `pts/tests/run/keyed_assign_value.pts` (skip line removed).
- New tests in `pts/tests/run/`: `keyed_assign_value_types.pts`, `dynamic_assign_value_types.pts`, `object_literal_iterator_accessors.pts`, `narrowed_tuples_of_dynamic_unions.pts`, `narrowed_arrays_of_dynamic_unions.pts`, `narrowed_records_of_dynamic_unions.pts`, `object_rest_empty.pts`, `object_rest_assignment.pts`, `interface_method_tuple_rest.pts`.
- New harness: `pts/tests/compiler_smoke_test.go`.
- `h04-probes/` holds every probe with its pts and Node outputs, plus:
  - `a/`: the four ledger rows.
  - `b/`: variants.
  - `c/`: controls.
  - `d/`: the hunt.
  - `cli/`, `exp/`, `logs/` (every go test and smoke log), `smoke-out/`.
  - `overlay/`: two experiments, stored as `.go.txt` so `go build ./...` doesn't pick them up; their json files point at `/tmp/h04`.
  - `irdump/main.go`: an in-process IR/C dumper. It is a `main` package inside the module.
  - `scripts/`: `classify.sh` gives pts exit, Node exit and a verdict per probe; `ccheck.sh` emits C in process and runs `clang -fsyntax-only`; `nodeexpect.sh` diffs Node against a test's `// expect:` block; also `gotest.sh` and `smoke.sh`.

Each probe was run as `PTS_HOME=<wt> <wt>/pts-bin/pts run X.pts`. Node ran a `.mts` copy with `--no-warnings --experimental-transform-types`, as the differential test does.

## A. Ledger rows on the base
| Row | pts exit | First lines | Node |
|---|---|---|---|
| C26 (`run/keyed_assign_value.pts`, unskipped) | 2 | `panic: interface conversion: ir.Type is *ir.Primitive, not *ir.Array` in `cgen.(*emitter).elementType` at `pts/cgen/arrays.go:12`, called from `arrayCall` (`arrays.go:32`) | `1 b { open: [ 'a', 'b' ], closed: [ 'z' ] }` |
| C34 | 2 | `panic: runtime error: index out of range [-1]` in `lower.(*lowerer).accessorFields` at `pts/lower/objects.go:88`, called from `shape` (`objects.go:70`) | `get @@iterator` / `item 1` / `item 2` / `get @@iterator` / `1` / `get @@iterator` / `[ 1, 2 ]` |
| C39 await | 1 | `c39a.pts:1:85: error: incompatible integer to pointer conversion passing 'pts_value' … to parameter of type 'const void *'` at `pts_field_address(pts_frame->p, …)` | `e3 promise\|tuple 3` |
| C39 guard | 1 | the same clang error at 4:160 and 4:283 | `plain union local b 2` |
| C50 | 1 | `c50.pts:5:58: error: too few arguments to function call … pts_value_from_number()` | `1 { b: 2 }` |

## B. Variant boundaries
**C26.** Probes `b/c26/v01`–`v39`.
- **Root cause:** a plain `=` on a dynamic place gets IR type `Value`, but tsc types the expression as the right side's type. Consumers that dispatch on the tsc type then get an untyped value.
- **Go panics:**
  - `.length` or `[i]` on the value used as an array: `cgen/arrays.go:12`.
  - Element write `(l[k] = x)[0] = …`: `lower/lower.go:2903`.
  - Map or Set method `.get` / `.has`: `lower/maps.go:244`.
  - `.size`: `lower/maps.go:298`.
  - The array panics occur in functions, generic functions, class methods, with enum keys, `keyof` keys, class-field keys, and on `any` receivers (`(o.x = [1, 2]).length`).
- **Clang rejections:**
  - Used as a string: `.toUpperCase()`, `.length`, `[1]`.
  - String `+=` then `.length`.
  - Tuple `[0]`.
  - Typed array `[1]`.
  - `(o[key] = "abc").length` on `any`.
  - A recursive-union entry, `(json.b = "xyz").length`.
- **Work:** numbers (arithmetic, `toFixed`, compare), booleans, object `.n`, class method calls, typed local/argument/return, `push`/`map`/`slice`/`join`/`includes`/`reduce`, `for…of`, spread, number `+=`, `??=`, `Record<string, T>` keys.
- **Honest diagnostics:** `?.length` gives PTS1001 "converting undefined to number"; use as an array index gives PTS1001.

**C34.** Probes `b/c34/v01`–`v18`.
- **Root cause:** `accessorFields` matches tsc's internal symbol name against the field's `@@iterator` name.
- **Panic:** any object-literal getter or setter named `[Symbol.iterator]` or `[Symbol.asyncIterator]` panics at `objects.go:88`. It does not matter whether the object is typed `any`, untyped or `Iterable<number>`, or never iterated: v04 only reads `o.n`.
- **Not affected:**
  - Other well-known symbols get an honest PTS1002.
  - Class accessors with computed names get PTS1001.
  - Generator methods `*[Symbol.iterator]` work.
- **Overlay experiment** (use `propertyName(property)` in `accessorFields`):
  - The panic is gone.
  - Next blocker: `settable[Symbol.iterator] = f` gives an honest PTS1001.
  - Without that line, the literal fails silently and is caught by the existing net: `object literal expression (which the compiler failed to lower without saying why)`. So the fix is more than the rename.

**C39.** Probes `b/c39/v01`–`v27`.
- **Root cause:** a union with a Promise, Generator or Iterator member collapses to `Value`, as does `unknown`. `narrowedValue` (`lower/exceptions.go:224`) converts only to classes, primitives and `any[]`. Narrowed to a tuple or a typed array, the expression stays `Value`.
- **Tuples, clang rejects:** await branch, `Array.isArray`, type guard, `in`, `Iterator|[number]`, `unknown` narrowed by a guard, tuple write.
- **Arrays, panics:**
  - `Promise|number[]` index: raised in `ir.(*ArrayCall).Type` at `ir/ir.go:1553`, via `narrowed` (`lower/unions.go:134`).
  - `Generator|number[]` `.length`: `cgen/arrays.go:12`.
  - `unknown` narrowed by a guard to `number[]` / `[string, number][]`: the same two sites.
- **Work:** Map/Set paired with a tuple (they stay real IR unions), destructuring or `.length` of a narrowed tuple, objects via `in`, classes, `typeof` strings, `as` casts.

**C50.** Probes `b/c50/v01`–`v30`.
- **Real trigger:** an empty rest type `{}` from an interface or object-literal source. A class source works (v20, v30).
- **Root cause:** `spreadNames` returns nil for zero properties. `restProperties` (`destructuring.go:416`) reads that nil as "failed, already reported". The declaration is dropped with no diagnostic.
  - At module scope the predeclared locals emit empty C, so clang rejects it.
  - In a function the symptom is PTS1001 ``using `rest` is not supported yet``.
- **Why no diagnostic:** the drop happens in `destructure`, outside `l.expression`, so the `unexplainedFailure` net (`lower.go:1295`) misses it.
- **Overlay experiment:** with one line (`if names == nil && len(shape.Fields) != 0`), `object_rest_empty` matches Node. The exact C50 program then prints **`1 {}`**, not Node's `1 { b: 2 }`.
- **Decision for you:** D-058 and SPEC §8 line 600 copy only the static type's properties. So C50's ledger expectation (Node's output) conflicts with a locked decision. v03 already diverges silently today (pts `{ b: 2 }`, Node `{ b: 2, c: 3 }`). I deliberately did not put the extra-properties program in a differential test.
- **New panic:** any object rest in a destructuring **assignment** `({ a, ...rest } = x)` panics with a nil dereference. Source can be a literal, a class or `any`. Site: `lower.(*lowerer).typeAt` at `generics.go:133`, via `restProperties` (`destructuring.go:411`, `element.Name()` is nil for a spread assignment), `assignParts` (`:670`), `destructuringAssignment` (`:617`).

## C. RED regressions
Each was run as `go test ./pts/tests/ -run 'TestRun/<name>$' -count=1` (GOMAXPROCS=2, `-p 1`); every one exits 1. Every `// expect:` block matches Node byte for byte. Each test has a control in `h04-probes/c/` that does the same work without the defect (read-back, typed local, `as` cast, or method instead of accessor); every control builds and prints exactly Node's output on the base.

| Test | RED reason on the base |
|---|---|
| `keyed_assign_value` | panic `cgen/arrays.go:12` |
| `keyed_assign_value_types` | panic `lower/maps.go:244`; the string, tuple, element-write and other parts were shown separately in `b/c26` |
| `dynamic_assign_value_types` | panic `lower/maps.go:298` (`mapProperty`) |
| `object_literal_iterator_accessors` | panic `lower/objects.go:88` |
| `narrowed_tuples_of_dynamic_unions` | clang `-Wint-conversion` at every narrowed site (lines 5, 13, 16, 20, 29, 30, 35, 39) |
| `narrowed_arrays_of_dynamic_unions` | panic `ir/ir.go:1553` via `lower/unions.go:134` |
| `narrowed_records_of_dynamic_unions` | panic `lower/records.go:187` |
| `object_rest_empty` | ``15:27 - error PTS1001: using `left` is not supported yet``; the module-scope clang error is hidden behind it but shows in the harness |
| `object_rest_assignment` | nil-dereference panic `lower/generics.go:133` |
| `interface_method_tuple_rest` | panic `lower/objects.go:255` |

Because TestRun builds in process, a panicking RED test aborts the whole test binary; only `-run`-isolated runs show clean per-test results.

## D. Panic hunt
**Verified through the CLI (exit 2).** Probes in `h04-probes/d/`, with Node's output next to each.
- **Family, value read** (the same root cause as C26/C39). Sites, with what reaches them:
  - `ir/ir.go:1553`: `any` or a catch variable narrowed by a guard to `number[]`; generic `T[] | Promise<T[]>`; `asserts x is number[]`.
  - `cgen/arrays.go:12`: also reached from a catch variable narrowed by a guard.
  - `lower/maps.go:244`: a keyed value used as a Set.
  - `lower/maps.go:298`: `Map|Generator` narrowed by `instanceof Map`, then `.size`.
  - `lower/records.go:187`: `unknown` narrowed by a guard to `Record<string, number>`.
  - `lower/records.go:204`: `Promise|Record`, `Iterator|Record`, `unknown` narrowed to `Record<string, number[]>`.
  - `lower/methods.go:330`: `f.apply(null, t)` with `t` a narrowed or keyed-value tuple.
- **Family, clang:** a keyed value used as a typed array; an `any` parameter narrowed by a guard to a tuple (h15, h53).
- **Separate root causes:**
  - `objects.go:255`: an interface or object-type method with a tuple rest parameter, `log(...args: [string, number])`, generic or not.
  - The `generics.go:133` assignment-rest panic above.
- **Verified in process only** (irdump runs EmitC's pipeline without clang; hunt5):
  - Records: `in` (`records.go:350`), `delete` (`records.go:382`), write (`lower.go:2907`).
  - Maps: `for…of` (`maps.go:373`), `.keys()` (`maps.go:496`).
  - Arrays: `.entries()` (`array_library.go:210`), `.pop()` (`cgen/arrays.go:12`), element and length writes (`lower.go:2903`).
  - Sets: `.has()` (`maps.go:244`).
- **Suspected, same pattern, not exercised:**
  - Unchecked IR type assertions: `cgen/arrays.go:43/86/137`; `lower/arrays.go:341/401/496/520`; `callbacks.go:90-92/150`; `records.go:284/402/437`; `maps.go:360/410/434/468/512`; `typed_arrays.go:501/577`; `lower.go:2950`.
  - `generics.go:631`: same code as `objects.go:255`, but my probes reached `objects.go:255` instead.
  - 16 explicit `panic("cgen: …")` / `panic("lower: …")` invariants.
- **Malformed programs** (40 cases, including truncated or misused `parallel`, numeric types, templates, decorators, namespaces, `with`, `eval`): all give tsc or PTS diagnostics, no panics. Jumps through nested `finally` (10 cases) all produce C that clang accepts.

**Harness** (`compiler_smoke_test.go`, `TestCompilerFailsCleanly`):
- **What it does:** compiles every `run/`, `errors/`, corpus and docs program plus fixed-seed mutants with `driver.EmitC`. It recovers panics in the compile goroutine, reports hangs after 1 minute, groups failures by message and top compiler frame, and keeps the smallest program per group. With `PTS_SMOKE_CLANG=1` it also runs `clang -fsyntax-only` on the C (about 0.1-0.3 s each).
- **Mutation operators:** delete line, truncate after line, duplicate line, swap lines, annotation to `any`, `const x` to `const x: any`, annotation to `unknown`.
- **Knobs:** `PTS_SMOKE_MUTANTS` (default 2), `_SEED`, `_FILTER`, `_CLANG`, `_JOBS`, `_OUT`.
- **Timings** (2 jobs, host busy):

| Run | Programs | Compiled to C | Time |
|---|---|---|---|
| Seeds only | 821 | 707 | 67 s |
| 2 mutants per seed | 2,427 | 1,440 | 2 m 50 s |
| 8 mutants per seed | 7,245 | 3,638 | 7 m 40 s |
| 8 mutants per seed + clang | 7,245 | 3,638 | 9 m 40 s |

- **Result:** every failure in every run is one of my RED tests or a mutant of one. The harness also independently caught C50's module-scope clang error and the C39 clang errors. The 815 existing seeds and roughly 7,000 mutants of them across two seeds compile cleanly (diagnostics, or C clang accepts).
- **Assessment:** the generic mutations found nothing new on the base. Targeted family probing found every new site listed above, so the harness works as a regression net, not a hunter. An operator that converts narrowed values to `unknown` or unions with a Promise member would aim it at this family.

## Other things you should know
- A Go panic exits with **2**, the same code the CLI uses for a malformed command line. It prints about 40 lines of goroutine trace and no source location. This is the same for `run`, `build`, `emit-c` and `test`.
- `pts test` builds files in `sync.WaitGroup.Go` goroutines: one file's panic kills the whole run ("recovered, repanicked"). A recover in `main` cannot catch it, so containment has to sit inside `driver.EmitC` or each build goroutine.
- TestRun calls `driver.Build` in process, so one compiler panic aborts the whole `go test ./pts/tests` binary and loses every other result.
- My first draft of `dynamic_assign_value_types` was wrong by design: `const o: any = {}` has a fixed empty layout in pts, so adding properties throws. The control exposed it, and the test now gives the object its properties up front.
