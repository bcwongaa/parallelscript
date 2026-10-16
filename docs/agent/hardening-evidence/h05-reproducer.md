# H05 reproducer report

- **Child:** the named `opus-reproducer` subagent (Agent call with the `opus` alias, which the session's environment maps to claude-opus-5-5; effort xhigh from `CLAUDE_CODE_EFFORT_LEVEL`), 2026-10-17 00:5x to 01:1x, in its own worktree `.claude/worktrees/agent-aa9b2c9ea41b3d6b2` (branch `worktree-agent-aa9b2c9ea41b3d6b2`, base `b9ac7c970`), with its own CLI (sha256 `9f5aa502946745a880d62c0aa2b66cd0a8deb0928b5d7ed69662e6172dcbdec1`) and runtime (`PTS_HOME` its worktree). It committed nothing, pushed nothing and ticked nothing; its tests and probes stayed uncommitted in its worktree.
- **Its probes:** [h05-reproducer-probes.tar.gz](h05-reproducer-probes.tar.gz), the worktree's `h05-probes/` as it left it (130 probe programs, `run_matrix.py`, `tools/make_tests.py`, `tools/run_red.sh`, `results/` with the matrix runs on the base).
- **What the parent took:** the 16 run tests below, byte for byte. In the parent's tree, before the commit: `flat_depths.pts` swaps `odd.flat(1.5)` for `odd.flat(-0)` and says why in its header (a fractional literal depth is PTS1001 by D-180, pinned by `errors/flat_odd_depths.pts`; the expect block regenerated from Node); `flat_negative_depths.pts` is dropped for the same reason (its `-1` cases are in `errors/flat_odd_depths.pts`, `-0` in `flat_depths.pts`); `parallel_flatmap_unions.pts` keeps the C20 line, the conditional and new-tuple callbacks and the 10,000-element line, with its expect block regenerated from the sequential Node oracle, and the two callbacks that return a tuple they didn't make or are declared to return one move to `errors/parallel_flatmap_tuple_results.pts` (D-094's copy rule; D-180). The parent re-ran every RED claim itself on the base compiler and runtime ([h05.md](h05.md), RED). The child's suggestion to keep `errors/arrays_not_yet.pts`'s non-constant depth pinned with a `let` wasn't taken: a depth of any number type works now.

```
90fbaea30c6dc976bdea881aa0f58ff33b843b3152add1ae4040abe0c2586bb9  pts/tests/run/any_flat_holes.pts
c059f1b00b41b3822470d333772928746764c69b075b2ab293b6e0811ed02eb4  pts/tests/run/flat_any_elements.pts
5ad88c125e91d306d6a6a32b3ff375e30b4591a0a58504eeb98ed33931341fc8  pts/tests/run/flat_depths.pts (changed by the parent, above)
2dff90f2f1dc4578e64afb2ac24343aee29fab15296c9e01922c5f13871de687  pts/tests/run/flat_generic.pts
e7081e6a39b1ee13509804b999393b84daab934c7249dc72cc5e621b6df63d76  pts/tests/run/flat_holes.pts
b5d96642a0babb6cc8ff5e287c4905e585e99de70f45394f381224ad6bf84794  pts/tests/run/flat_integer_unions.pts
7d7f6cf894f2da63cb17e0b45134490d7b74e09143f1ae0d7f4344a16a19d416  pts/tests/run/flat_literal_depths.pts
a75388dcb33f74f1fa245e3a90f637e379274ebe889551732eeddabd6ea1cb59  pts/tests/run/flat_negative_depths.pts (dropped by the parent, above)
30cb13ba9d0e1544383cbed27dc7e81039d7fa6820a23a7501474a6f4b1cebb8  pts/tests/run/flat_tuple_receiver.pts
20f371f507d620db8166eb844bf0a7119ebc7fe05bdd90cc7ddca0e32e81db19  pts/tests/run/flat_tuples.pts
3c38f19facfcfbc77e34f8ccb8f464073212275bbde500ac70e38ac69eaf5aba  pts/tests/run/flat_typed_results.pts
c3e211665c409d10ffba7388d37db12e5266f7fe652093b965a75411d8c2415e  pts/tests/run/flat_unions.pts
a75f77086e61769dc333e91dd18f2cd88fe82118bb088bbcf1a318138910d4e6  pts/tests/run/flatmap_holes.pts
d0b51f4444f046dc4f0361013313fa8ddbaa5d336fcb2f777d4bcddacb302b1c  pts/tests/run/flatmap_union_results.pts
2cca8a2ef420b9a6b7324558b3b48dcda30ff4eca9216eaea0339abccbabc4ba  pts/tests/run/parallel_flatmap_holes.pts
f9d7f3c68aefa33f7d85169338a59f1928873b353dde9785acd671ca06ae90ed  pts/tests/run/parallel_flatmap_unions.pts (changed by the parent, above)
```

The child's final report follows unchanged.

---

I reproduced every defect family behind C05 and C20 on base `b9ac7c970`, wrote 16 RED run tests that fail for the right reasons, and found no internal compiler errors. I committed nothing and changed no compiler, runtime or library code.

**Setup.** Worktree `/home/bryan/projects/parallelscript/.claude/worktrees/agent-aa9b2c9ea41b3d6b2`. Submodules ryu, bdwgc, libuv, libbacktrace and pcre2 were initialised with `--reference`. `GOMAXPROCS=2 make build` exited 0; the CLI's sha256 is `9f5aa502946745a880d62c0aa2b66cd0a8deb0928b5d7ed69662e6172dcbdec1`. Toolchain: Node v24.21.0, go1.26.9. Node ran with `--no-warnings --experimental-transform-types` on `.mts` copies, as the test harness does.

## A. H01b probes re-run
Command: `PTS_HOME=<wt> python3 docs/agent/hardening-evidence/probes/h01b/run_probes.py --pts <wt>/pts-bin/pts --node ~/.local/node/bin/node --scratch /tmp/h05repro.ykncB8/h05probes --out …/h05probes.json` on the 11 C05/C20 probes. It **exited 0**: "11 probes, 0 not behaving as their kind says".
- **Controls OK:** `c05-control-flat-any`, `-flat-homogeneous`, `-par-flatmap-union` (at 1, 2 and 4 threads).
- **Reds, all `run-mismatch` as claimed** (first differing lines):
  - `heterogeneous-depths`: Node `[ 1, 2, [ 3 ] ]`, PTS `[ 1, [ 2 ], [ [ 3 ] ] ]`.
  - `nested-union-depth`: Node `[ 1, 2, 3 ]`, PTS `[ 1, [ 2 ], 3 ]`.
  - `tuple-array`: PTS `[ [ 'b', 2 ], [ 'a', 1 ] ]`.
  - `union-array`: PTS `[ 1, [ 2, 3 ], 4, [ 5 ] ]`.
  - `union-holes`: PTS `… [ 1, [ 2, 3 ] ]`.
  - `flatmap-union`: PTS `[ 1, [ 2, 3 ], 4 ]`.
  - `parent-flat-union`: PTS `[ 1, [ 2, 3 ], 4 ]`.
- **C20 red, `build-error` as claimed:** `11:51 - error PTS1001: converting number | number[][] to number | number | number[][] | number[] is not supported yet`.

## B. Node differential matrix
130 probes are in `h05-probes/`, run by `h05-probes/run_matrix.py`. Results and JSON are archived in `h05-probes/results/`, and the generator and runner scripts in `h05-probes/tools/`. Each runner invocation exited 0.

Totals: **49 WRONG, 17 CRASH, 17 DIAG, 0 ICE, 3 TSERR (not valid TS), 44 OK.**

| Family | WRONG / CRASH / DIAG | OK (controls) |
|---|---|---|
| Typed `flat`, union elements | WRONG fu01, fu02, fu04–fu10, fu12, fu13, fe03, fe05, fe06, fe07 (`(int\|int[])[]`), fe09 (ReadonlyArray of a union), fd15 (1.5); CRASH fu03 (`Expected number \| string[], got Array`); DIAG fu11 (an unrelated literal conversion) | fe13, fd14, fd16 |
| `any[]`, `unknown[]`, `object[]`, `Iterable[]` elements | WRONG fe10, fe11, fe14, fs01, fs04, fs05; DIAG fs02, fs03 (unrelated literal conversions) | — |
| Tuples | WRONG ft01–ft04, ft06, ft07; DIAG ft05 (`calling \`t.flat\` is not supported yet`) | ft08 (rest tuple), fm11, fm12 |
| Heterogeneous / depths | WRONG fh01, fh02; DIAG fd02, fd03, fd05, fd06, fd08, fd10, fd13, fd19 (`-1`, `-0`, `-Infinity`, `const D`, `o.d`, `1 + 1`), fd04 (`let`) | fd01, fd07 (enum), fd09, fd11 (NaN), fd12 (1.5, 2.9), fd18 |
| Holes | WRONG fo02, fe15, fy02, fy04, fy09, fp11; CRASH fo08, fo09, fp10 (`RangeError: Array index 1 is out of range`). fo04/fo07 are by design (number slots start as 0, D-085) | fo01, fo03, fo05, fo06, fo10 |
| Typed `flatMap` | WRONG fm01, fm02, fm06, fm07, fm08, fm09, fm10, fm13, fm14, fm17; DIAG fm15 (`thisArg`, already pinned) | fm03 (readonly), fm04, fm05 |
| Results used in typed code | CRASH fr01–fr06 (`TypeError: Expected number[], got Array`) | — |
| Erased/generic code | CRASH fg01, fg03, fg04, fg06 | fg02, fg05 |
| Dynamic (`any`) | WRONG fy02, fy04, fy09 (holes become `undefined`); CRASH fy12, fy15 (tuple via any: `a.flat is not a function`, and `a.map` too) | fy01, fy03 (`"2"`, `undefined`, `null`, `{}`…), fy05–fy08, fy10, fy11, fy13, fy14 |
| `.par.flatMap` (1, 2, 4 threads) | DIAG fp03 (C20), fp02 (tuple results), fp08 (tuple source); CRASH fp10; WRONG fp11 | fp01, fp04 (typed array), fp05 (range), fp06, fp07, fp12, fp13 |

**What tsc types each depth as** (deliberate TS2322 probes fd17 and fd20):
- `const D = 2`, `o.d` (`as const`) and `E.Two` → `number[]`, exactly like `flat(2)`.
- `let d`, `NaN` and `Infinity` (all typed `number`) → `FlatArray<number[][], -1|0|…|20>[]`.
- `-1` → `number[][][][]`. That is unsound: JS returns a copy, `number[][][]`.
- `1.5` → `number[]`. Also unsound: JS's depth 1 gives `number[][]`.
- `-0` → `number[][][]`.
- `[string, number][]`'s `flat()` and a tuple-returning `flatMap` → `(string | number)[]`.

## C. RED tests (`pts/tests/run/`, untracked)
Every expect block is Node's actual stdout; `.par` tests use a sequential oracle (`.par.` removed).

Command: `go test ./pts/tests/ -count=1 -parallel 2 -v -run '^(TestRun|TestRunMatchesNode)$/^(<16 names>)$'`, run via `h05-probes/tools/run_red.sh`. It **exited 1**: all 16 failed `TestRun`, 13 failed `TestRunMatchesNode`, and 3 PTS-only tests were skipped there. `TestCompilerFailsCleanly` filtered to these files (3 mutants each, 64 programs) **passed**, so none of them causes an internal compiler error.

| File | Pins | RED evidence (actual vs expected) | sha256 |
|---|---|---|---|
| `flat_unions.pts` | union, set, class, typed-array, readonly and array-or-tuple elements | stdout `[ 1, [ 2, 3 ], 4, [ 5 ] ] 4` vs `[ 1, 2, 3, 4, 5 ] 5`; 13/13 lines differ | c3e21166… |
| `flat_depths.pts` | depths 0/1/2/3/Infinity, 1.5, NaN, enum over heterogeneous nesting | `[ 1, [ 2, [ 3, [Array] ] ] ]` vs `[ 1, 2, [ 3, [ 4 ] ] ]` | 5ad88c12… |
| `flat_tuples.pts` | pair, optional, readonly, tuple-of-arrays at depths 1/2, `Object.entries`, Map | `[ [ 'b', 2 ], [ 'a', 1 ] ]` vs `[ 'b', 2, 'a', 1 ]` (the rest-tuple line already matches) | 20f371f5… |
| `flat_tuple_receiver.pts` | `t.flat()` on a tuple | `4:13 PTS1001: calling \`t.flat\` is not supported yet` | 30cb13ba… |
| `flat_any_elements.pts` | `any[]`, `unknown[]`, `any[][]`, `object[]`, `Iterable[]` | `[ 1, [ 2, [ 3 ] ] ]` vs `[ 1, 2, [ 3 ] ]` | c059f1b0… |
| `flat_holes.pts` | holes beside and inside union arrays | `[ 1, [ 2, 3 ] ]` vs `[ 1, 2, 3 ]`, `[ 'x', [ 'a', <1 empty item>, 'c' ] ]` vs `[ 'x', 'a', 'c' ]` | e7081e6a… |
| `flatmap_union_results.pts` | union, tuple, explicit `<number \| number[]>`, `any` and `any[]` source | 11/11 lines differ, e.g. `[ 1, [ [ 2 ] ] ]` vs `[ 1, [ 2 ] ]` | d0b51f44… |
| `flat_typed_results.pts` | push, map, reduce, `typeof`, JSON on the results | exit 1, `TypeError: Expected number[], got Array` at line 6; empty stdout | 3c38f19f… |
| `flat_generic.pts` | monomorphized and erased generics | exit 1, `Expected number[], got Array at spread (…:5)` | 2dff90f2… |
| `flatmap_holes.pts` | holey source and holey callback results | exit 1, `RangeError: Array index 1 is out of range (length 3)` at `Array.flatMap` | a75f7708… |
| `any_flat_holes.pts` | dynamic flat/flatMap holes | `[ 1, undefined, 2 ]` vs `[ 1, 2 ]`; `[ 'a', undefined, 'c' ] 3` vs `[ 'a', 'c' ] 2` | 90fbaea3… |
| `parallel_flatmap_unions.pts` (`threads: 1 2 4`) | C20 type argument, tuple results, tuple source, 10k elements | 4× PTS1001 (`8:51 converting number \| number[][] to …`, `9:32 converting [number, string] …`) | f9d7f3c6… |
| `parallel_flatmap_holes.pts` (`threads: 1 2 4`) | holey source and results | exit 1, `RangeError … at pts:parallel:161`; probe fp11 shows `[ <1 empty item>, 'a', … ]` vs `[ 'a', 'b' ]` | 2cca8a2e… |
| `flat_literal_depths.pts` | `const D = 2`, `o.d`, `const ONE` | 3× PTS1001 "`flat` with a depth that isn't a constant" | 7d7f6cf8… |
| `flat_negative_depths.pts` | `-1`, `-0` (a copy) | 3× the same PTS1001 | a75388dc… |
| `flat_integer_unions.pts` | `(int\|int[])[]`, `(u8\|u8[])[]` | `[ 1, [ 2, 3 ] ]` vs `[ 1, 2, 3 ]` | b5d96642… |

`flat_literal_depths.pts` conflicts with `pts/tests/errors/arrays_not_yet.pts:8–9`, which pins `const depth = 2; [[1]].flat(depth)` as PTS1001. If you adopt it, change that line to `let depth = 2;` so the non-constant diagnostic stays pinned at the same position (9:25). Expressions tsc types as plain `number` (`let`, `1 + 1`, `-Infinity`) should stay PTS1001; I wrote no run test for them. Because tsc types `-1` and fractional depths unsoundly, keeping a diagnostic for `flat_negative_depths` is defensible; it is a separate file so you can drop it.

## D. Root cause
1. **The lowering derives the result type itself** (`pts/lower/arrays.go:510–529`).
   - Lines 520–527 peel `*ir.Array` layers `depth` times and stop at the first non-array element type: a union, a tuple shape (D-053) or `ir.Value`. So `To` stays the receiver's type, and tsc's `FlatArray` type is ignored.
   - Wherever tsc's type is required, the value goes through a checked conversion that throws `Expected number[], got Array` (the fr* and fg* crashes).
   - Line 513 accepts only a literal `ir.NumberConst`. Unary minus, `const` identifiers and property reads are PTS1001 (line 515); NaN and Infinity work only because they are lib constants (`lower.go:62–65`).
2. **The runtime's flatten** (`pts/runtime/src/array.c:871–888`).
   - Line 882 recurses only when the slot kind is `PTS_FIELD_OBJECT` and the object is `PTS_KIND_ARRAY`. Union, `any`, `unknown` and `object` elements are `PTS_FIELD_VALUE` (`cgen/classes.go:648–650`), and tuples are `PTS_KIND_TUPLE`, so neither is ever spread.
   - Line 885 pushes the raw slot bytes, and `pts_array_push` copies the *result's* kind size (`array.c:376–377`). Today that is memory-safe only because recursion happens when the kinds match. A fix that recurses into VALUE slots or tuples must convert each leaf to the result's kind: box with `pts_value_of_slot`, or unbox. `cgen/arrays.go:73–74` just passes `call.To`'s descriptor.
3. **Typed `flatMap`** (`pts/lower/callbacks.go`).
   - Lines 216–220 take the result type from the callback's lowered type, not tsc's `U[]`.
   - Line 261 flattens only when the result is statically `ir.Array`. Unions, tuples and `any` are pushed whole (line 264).
   - `appendAll` (511–528) reads with `ArrayElementChecked` (line 516), which throws RangeError on a hole and doesn't handle tuples.
   - The shared loop reads the source with `ArrayElementChecked` (lines 404 and 554), so holes throw instead of being skipped.
4. **Dynamic path** (`pts/runtime/src/dynamic_methods.c`). `flatten_into` (544–553) uses `pts_spread_element`, which reads a hole as `undefined`. `array_flat_map` (633–645) calls the callback for holes and pushes the holes of callback results. `filter` already skips holes via `pts_array_load` (623); that is the model. `is_array_like` (450–456) is correct: arrays and tuples, not typed arrays.
5. **`.par.flatMap`** (`pts/lib/parallel.pts:156–175`).
   - The callback type is `U | U[]`, where Array's is `U | ReadonlyArray<U>`. A readonly result is TS2322 (fp09).
   - With `U = number | number[]`, the arrow's `number | number[][]` or a tuple must convert to `U | U[]`. That is an element-type-changing array conversion the lowering refuses (C20).
   - Line 161's `items[i]` throws on holes. Lines 162–163, `Array.isArray` plus `push(...value)`, keep holes.
   - The typed-array (362–365) and range (551–553) versions delegate to it and work for array and union results.
   - The doubled `number` in C20's message is a printing artifact: `ir/ir.go:292–297` prints `(number | number[])[]` as `number | number[][]` (no parentheses). That is V08/H22 territory.
6. **Tuple receiver.** `pts/lower/tuples.go:210–215` (`tupleCopyMethods`) lacks `"flat"`, so `arrays.go:541` rejects `t.flat()`.

**What a correct design needs (my proposal, not verified):**
- Compute the result element type FlatArray-style over union members: an array or tuple member is replaced by its element union, recursively to `ToIntegerOrInfinity(depth)` levels; typed arrays, strings, objects and Sets stay as leaves; `any` stays dynamic.
- Treat tsc's literal-typed depths (`const D`, `o.d`) as constants, but compute the type yourself, because tsc's `FlatArray` is unsound for `-1` and fractions.
- At run time, check each element, whether it sits in an OBJECT or VALUE slot, for being an array or tuple; spread tuple fields up to `pts_tuple_length`; convert each leaf to the result's slot kind; skip holes at every level.
- Give `flatMap` the same per-result dispatch with tsc's `U[]` (or map, then a corrected `flat(1)`), and skip holes.

## Adjacent, not H05 (verified)
- `map` over a holey `string[]` throws RangeError (fo11; same loop at `callbacks.go:404`).
- Tuples through `any` have no array methods: `dynamic_methods.c:1417–1445` has no `PTS_KIND_TUPLE` case (fy12, fy15).
- `thisArg` is PTS1001 and already pinned.
- Holes in number arrays are 0 by design (D-085).

**Dropped as not valid TS:** fm16 (`number[] | number[][]` callback result, TS2322) and fp09 (TS2322 against the library's own signature; I report it as a signature gap). The two `tsc`-type probes are information only.

**Not determined:** whether the shapes currently masked by an early crash or diagnostic in the same RED file (e.g. the later lines of `flat_typed_results`) hide further defects. Each of those lines was checked in its own probe (fr02–fr06, fg03–fg06, fo09, fp11), so this is a suspicion, not evidence.
