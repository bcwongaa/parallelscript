# H11b reproducer report (C111, C112, the typed `for…of` at holes)

Child: `reproducer` (claude-opus-5-5, xhigh), isolated worktree of `fa0c9f80b`, 2026-10-25. Returned as its final message (children can't write report files) and saved here by the parent below the rule, unchanged but for its title line (`# H11b reproducer report (HEAD `fa0c9f80b`)`). Archive: [h11b-reproducer-probes.tar.gz](h11b-reproducer-probes.tar.gz) (generators, runner, report scripts, cases, results, probes, tests, the three prototypes' patches, cost data; no binaries).

---

All three defects reproduce on HEAD. I found eight related gaps: N1 is a compiler crash and N2 a silently wrong result. My recommendation for the `for…of` binding is design A (every binding over an array that can have holes is loose), but only once the loose-local gaps it exposes are fixed: N2, N3, and the PTS1001s listed under the risks.

Nothing in the worktree's sources was changed. The prototypes were built with `go build -overlay`, and `git status` shows only the 7 new test files plus the 5 `third_party` symlinks.

## 1. The three skipped tests on HEAD (`check_expect.py --unskip`)

- **C111** (`optional_callback_parameters_at_holes.pts`): 9 lines throw RangeError instead of Node's values. These are `findIndex`, `find`, `findLast`, `findLastIndex`, `Array.from`, the function expression, the named function, `interface` and `string[]`. The type-predicate and union controls pass.
- **C112** (`find_shrinking_array.pts`): `find string[] [ 'a' ]` vs Node `[ 'a', undefined, undefined ]`; `findIndex number[] [ 1, 2 ] -1` vs `[ 1, 2, undefined ] 2`; `findLast objects [ 3, 1 ]` vs `[ 3, undefined, 1 ]`. pts stops quietly instead of throwing.
- **Typed `for…of`** (`for_of_reads_holes_typed.pts`): `string[] threw RangeError` and `objects threw RangeError`. The control passes.

## 2. Matrix results on HEAD

Each case is classified against Node. "By design" means D-085, D-090 or D-183 says pts should throw or differ there.

**H11's families D, E and G's `forof_*`, regenerated (229 cases).** Only one result changed since H11's base: G1627 (the C31 hint) went from RangeError to OK, fixed by H11a.

| family | expected | cases | OK | WRONG | TypeError | RangeError |
|---|---|---|---|---|---|---|
| D C111 | Node | 75 | 18 | 0 | 0 | 57 |
| D C111 | by design | 45 | 12 | 18 | 0 | 15 |
| E C112 | Node | 66 | 42 | 24 | 0 | 0 |
| E C112 | Node or RangeError | 33 | 15 | 18 | 0 | 0 |
| G forof + guards | Node | 10 | 5 | 0 | 1 (match, D-133) | 4 |

**New families (1,758 cases, seed 20261025).**

| family | expected | cases | OK | WRONG | TypeError | RangeError | PTS1001 | PTS9001 |
|---|---|---|---|---|---|---|---|---|
| H `for…of` at holes | Node | 592 | 58 | 0 | 0 | 530 | 3 | 1 |
| H | by design | 68 | 0 | 1 | 0 | 67 | 0 | 0 |
| I C111+ | Node | 504 | 126 | 0 | 0 | 378 | 0 | 0 |
| I | by design | 192 | 37 | 50 | 0 | 105 | 0 | 0 |
| J C112+ | Node | 291 | 179 | 112 | 0 | 0 | 0 | 0 |
| J | Node or RangeError | 45 | 4 | 41 | 0 | 0 | 0 | 0 |
| J controls | Node / by design | 18 / 3 | 18 / 0 | 0 / 3 | 0 | 0 | 0 | 0 |
| K controls | Node / by design | 19 / 7 | 19 / 0 | 0 / 5 | 0 / 1 | 0 / 1 | 0 | 0 |
| L extras | Node / by design | 16 / 3 | 10 / 0 | 1 / 3 | 1 | 4 | 0 | 0 |

**H: `for…of` at holes.**
- On HEAD, every array kind throws RangeError at a hole. That covers `string[]`, classes, interfaces, unions of objects, `(string | null)[]`, arrays of functions, arrays of arrays, readonly arrays, class fields, readonly fields, `values()`, generic `T[]`, spread and `Array.from` copies.
- It does so for `let` and `const`, in every context (plain, generator, async, break/continue, labels), and for every use, tested or not.
- Only `(string | undefined)[]` (51 of 51) and arrays held in `any` (4 of 4) work.
- The 3 PTS1001s are an unrelated limitation (a function in a template literal). The PTS1001 is N1.

**I: C111+.** Over the 7 kinds of array whose slots can be holes:

| parameter form | result on HEAD |
|---|---|
| `any`, `unknown`, `(p: P \| undefined, i: number)` | 35 of 35 OK each |
| type predicate `(p?: P): p is P` | 14 of 14 OK |
| `(p?: P, i?: number)` | OK only for `Array.from` (7 of 35) |
| optional, default, default with index, function expression, named function, const arrow, static / instance / object-literal method, generic helper | RangeError, 35 of 35 each |
| by design: required, inferred, `P \| null` | RangeError |
| by design: `number[]` | holes read as 0 (D-085) |

**J: C112+.**
- Optional and default parameters stop quietly (WRONG) over every element kind whenever an index is cut off.
- `T | undefined`, `unknown` and `any` parameters are WRONG only over `number[]` and `boolean[]`. Over string and object arrays they are OK.
- Inferred `(x, i)`: 41 of 45 stop quietly. D-183 says a parameter that can't take undefined should get RangeError for the cut-off index.
- Every cut-off change reproduces it: `length = 1`, `pop`, `splice`, `shift` and `length = 0`.
- Through `any` (48 of 48) and growing the array (41 of 41) are OK.
- `pts:parallel` is consistent with D-183. `.par.find`, `some`, `filter` and `count` with an optional parameter all skip holes (probe `probes/par_find_holes.pts`, not a Node program).

**Controls (K):** all 19 Node-expected cases are OK: number and boolean `for…of`, tuples, Map and Set, string code points, `Float64Array`, the callback methods, and `find` over arrays without holes.

## 3. The two designs: matrix and cost

- **(A)** Every `for…of` binding over an array whose slots can be holes is loose. It is read inline with `pts_array_get_or_undefined` and checked where it is used as its type.
- **(B)** The binding is loose only where the program tests it for undefined (D-090's rule).
- **A2** is A plus a presence-only check: where a loose local is used as its type, only undefined is checked for. I built it to bound the cost; as built it is not safe (see the risks).

**Matrix, family H (660 cases):**

| | Node-expected OK | by-design cases |
|---|---|---|
| HEAD | 58 of 592 | 67 RangeError |
| A | 572 of 592 | 67 now D-090's TypeError |
| B | 335 of 592 | 67 still RangeError |

- No case that passes on HEAD fails under A or B.
- Under B, every untested use still throws RangeError: printed, template, closure, outer assignment, `T | undefined` and `unknown` locals, member access (JS throws TypeError there), returned, stored.
- What still fails under A:
  - object and array patterns at a hole give RangeError where JS gives TypeError (9 cases);
  - `entries()` at a hole (5 cases, N4);
  - `x + 1` prints `undefined1` where Node prints `NaN` (N2);
  - `x < "c"` throws D-090's TypeError where Node prints `false`;
  - `switch (x)` with `case undefined` is PTS9001 (N1);
  - the 3 unrelated function-in-template cases.

**Cost: callgrind instructions in the loop only** (the 20-repetition run minus a 0-repetition setup run; 1M elements ×20, the closure probe 100k ×20). Every probe's output matched Node.

| probe | HEAD | A | A / HEAD | A2 | A2 / HEAD |
|---|---|---|---|---|---|
| `for (const s of strs) n += s.length` | 980.0M | 1120.0M | 1.14× | 1060.0M | 1.08× |
| same, tested `if (s !== undefined)` | 980.0M | 1180.0M | 1.20× | 1120.0M | 1.14× |
| four uses of `s` | 2560.0M | 2800.0M | 1.09× | 2640.0M | 1.03× |
| class objects, `p.x + p.y` | 960.0M | 1340.0M | 1.40× | 1180.0M | 1.23× |
| interface objects, `p.x + p.y` | 1440.0M | **4380.0M** | **3.04×** | 1540.0M | 1.07× |
| binding captured by a closure | 338.7M | 350.7M | 1.04× | 346.7M | 1.02× |
| `number[]` (control) | 860.0M | 860.0M | 1.00× | 860.0M | 1.00× |
| generic `T[]`, number instance | 1000.0M | 1240.0M | 1.24× | 1240.0M | 1.24× |
| generic `T[]`, string instance | 1340.0M | 1500.0M | 1.12× | 1440.0M | 1.07× |

- **Under B,** the emitted C is byte-identical to HEAD for the untested probes (I compared the files) and identical to A's loop for the tested probe. So B costs nothing for untested bindings and A's cost for tested ones.
- **Where A's cost comes from** (from the emitted C):
  - Strings: one `pts_value_as_string` per use.
  - Classes: `pts_value_as_instance` per use.
  - Interfaces: an out-of-line `pts_value_convert` (a structural check) per use, which causes the 3.04×.
- **The library pays too:** A also makes the `for…of` loops in `pts/lib` loose (process env parsing, http, abort, sync, test).

**Regression check:** I ran `TestRun` and `TestRunMatchesNode` over the 258 run tests that bind a `for…of` name, under each prototype's overlay.

| | pass | fail | skip |
|---|---|---|---|
| A | 410 | 7 | 99 |
| B | 416 | 1 | 99 |
| A2 | 399 | 18 | 99 |

- **Under A:**
  - Three tests no longer compile (PTS1001):
    - `generic_instanceof_guard_lying_predicate.pts:26` (a type guard narrows the binding to a class);
    - `generic_dispatched_method_values.pts:45` (`shape.make.bind(shape)`: `converting Shape | undefined to Shape`);
    - `generic_constraint_discriminant_writes.pts:41` (a generic dead branch).
  - `union_narrowing_error` changes its TypeError message.
  - `nullable_copies_keep_holes` pins RangeError where A now gives TypeError. That is JS's error class, and there is no crash.
  - The first PTS1001 also happens on HEAD with an ordinary D-090 loose local (probe `n_narrow/loose_narrowing.pts`), so it is an existing gap that A would expose widely.
- **Under B:** only `nullable_copies_keep_holes` fails, for the same reason as under A.
- **Under A2:** applying the presence-only check to every loose local also broke `regexp_exec`, `yield_star_result_edges`, `union_objects`, `value_conversions_more`, `iterator_result_getters_any` and `narrowed_array_intersections`. Their output stops early. It is safe only where the union is known to hold exactly the array's element type.

**Recommendation: A, with A2's cheap check limited to that case.**
- A is what gives Node parity: 572 of 592 against B's 335. Untested uses such as printing, closures and `T | undefined` places are common, and B leaves all of them as RangeError.
- A's cost is 1.04–1.40×, except for interface arrays (3.04×). A presence-only check limited to non-generic `for…of` bindings would bring those to 1.03–1.23×.
- Before landing A, these must be fixed, because A exposes them in ordinary loops: the three PTS1001s, N2 and N3.

## 4. Tests written

Files are in `/home/bryan/projects/parallelscript/.claude/worktrees/agent-a626b83f05c2c9be2/pts/tests/run/`. Every `// expect:` block was set from Node's own output.

**RED tests** (each has a `// skip:` line):
- `for_of_holes_tested_bindings.pts`: 30 of 35 lines RED on HEAD. Passes under A and B except the `functions` line (`f?.()`), which needs N3.
- `for_of_holes_untested_bindings.pts`: 25 of 27 RED on HEAD. Under A only `compared` (N3) still fails. Under B, 23 lines still fail.
- `for_of_hole_patterns_and_entries.pts`: covers `entries()` (N4) and pattern error class (N5). RED under HEAD, A and B.
- `callback_parameter_defaults_at_holes.pts` (C111+): 12 RangeErrors. Controls pass.
- `find_shrinking_array_more.pts` (C112+): `splice`, `length = 0` over booleans, default over `number[]`. Controls pass.
- `holes_where_an_element_is_required.pts` (`no-diff`, by design): expects RangeError for cut-off indices over `string[]` and `number[]` (HEAD stops quietly) and D-090's TypeError for a binding passed to a `string` parameter or pushed into a `string[]`. It assumes design A; under B those two lines stay RangeError.

**Guard:** `for_of_typed_sources_unchanged.pts` passes on HEAD and under A and B.

**Harness runs:**
- `bash probes/gotest_new.sh` (`TestRun` and `TestRunMatchesNode` for these 7 files): exit 0, the guard passes and the 6 RED tests are skipped.
- `TestCompilerFailsCleanly` over the 7 files plus their mutants: 14 programs, pass.

## 5. New findings

- **N1, compiler crash.** `switch (x) { case undefined: … }` when x's type has no undefined is PTS9001. This hits a `string` parameter, a D-090 local or a `for…of` binding alike. Cause: `caseMatch` (`lower.go:771-777`) passes the case clause to `operation`, whose never-strictly-equal branch calls `node.AsBinaryExpression()` (`lower.go:3064`). Also, `case undefined` isn't counted as an undefined test (`testedOperands`, `loose_locals.go:85-123`). Probe: `n_switch/`.
- **N2, silent wrong output.** A loose local that is undefined, used in `x + 1`, prints `undefined1` where JS gives `NaN`. This already happens on HEAD for D-090 locals and pattern names (`n_concat/loose_concat.pts`). `concatenation` (`lower.go:3917-3931`) reads both operands as they are and converts them to strings. Suggested fix: check the loose operand when the other operand isn't string-like.
- **N3.** On HEAD, a loose local compared with a string by `===`, `!==` or `==`, or called with `fn?.()`, throws TypeError where JS gives `false` / `true` / `undefined` (`n_concat/loose_compare_call.pts`).
- **N4.** `entries()` of a `string[]` or object array at a hole throws RangeError in every use: `for…of` with `[i, x]`, a pair binding, a spread, `Array.from`. `entriesHelper` (`array_library.go:270-289`) reads with a checked element read, and the `[number, T]` pair can't hold undefined. Neither design fixes this.
- **N5.** `for…of` object and array patterns at a hole throw RangeError where JS throws TypeError. This has the same shape as H11a's N5 (assigned to H22).
- **N6.** `Array.from(arr, mapper)` with a mapper that pushes stops at the starting length: pts prints `['a']`, Node `['a','n0','n1']`. `mapFrom` (`callbacks.go:319-324`) reads the length once.
- **N7.** `Array.from(a as any, (w?: string) => …)` throws `TypeError: Expected string, got undefined` where Node gives `['-','b','-']` (case L1757).
- **N8 (shape of C111).** An arrow `(p?: P)` lowers to `(P p_arg, bool p_given)` and the helper always passes `true`. The type-predicate arrow and `(p?: P, i?: number)` lower to a union parameter with no flag, which is why those already work.

## 6. Root causes and suggested fixes

- **C111.**
  - Cause:
    - `widened()` (`callbacks.go:690-702`) only accepts a dynamic or `T | undefined` element parameter, so `element()` (`:638-650`) does a checked read.
    - `callbackArguments` (`:517-557`) always passes the given flag as true.
  - Fix:
    - Treat an element parameter with a given flag (optional or defaulted) as taking undefined. Read the element as `T | undefined`.
    - Pass the flag as `arg !== undefined` using `maybeUndefinedFor` / `maybePassed`, as `convertArguments` does (`lower.go:2268-2276`). That gives `p?:` undefined and `p = d` its default, which is JS's behaviour.
    - Also check the path where a closure takes its context's shape and drops the flag (`closures.go:237-281`, `contextualFuncType` / `alwaysPassed`).
- **C112.**
  - Cause:
    - `loopFrom` (`callbacks.go:464-473`) tests `i < current length` unless `readsEveryIndex` (`:666-672`) holds.
    - `widened()` excludes number and boolean arrays (`:695`).
  - Fix:
    - Make `find` and its kin always visit every starting index.
    - Read `T | undefined` where the parameter takes undefined; numbers and booleans would use `pts_array_number_or_undefined` / `pts_array_element`.
    - Use the checked read elsewhere, which gives D-085's RangeError for a cut-off index.
- **Typed `for…of`.**
  - Cause:
    - `forOfStatement` reads with `ArrayElementChecked` (`arrays.go:1481-1484`).
    - The binding is never loose, because `readsElement` (`loose_locals.go:165-170`) requires a declaration with an initializer.
  - Fix:
    - Prototype A (`proto/A/forof_design_A.patch`) adds `isForOfArrayBinding` to `readsElement` and `isLoose`, and an inline `ArrayElementOrUndefined` read with `As` set to the binding's union.
    - Patterns over a hole need a non-null check, so they throw TypeError like JS.
- **Risks.**
  - The three PTS1001s above: narrowing a loose interface or generic binding, and `bind`.
  - The interface-array cost (3.04×).
  - The library's `for…of` loops change too.
  - Generic `T[]` loops instantiated for numbers start boxing.
  - Two existing tests pin the old behaviour and would need updating: `nullable_copies_keep_holes` (RangeError to TypeError) and `union_narrowing_error` (message).

## 7. Commands and locations

- Build: `GOMAXPROCS=2 make build`.
- Prototypes: `go build -overlay proto/{A,B,A2}/overlay.json -o proto/X/bin/pts ./cmd/pts`.
- Matrix: `python3 -I matrix/gen_h11b.py matrix`, then `python3 -I matrix/run_matrix.py matrix/cases.jsonl matrix/results_head.jsonl --jobs 2` (add `--pts proto/A/bin/pts` for a prototype).
- Summaries: `report.py`, `pivot.py`, `compare.py`.
- Cost: `bash cost/cost.sh LABEL PTS` and `bash cost/cost_extra.sh LABEL PTS`.
- Full list: `COMMANDS.txt` in the archive. No case binaries were kept.

Worktree: `/home/bryan/projects/parallelscript/.claude/worktrees/agent-a626b83f05c2c9be2` (the 7 new test files are untracked).
Archive: `/tmp/h11b/h11b-reproducer-probes.tar.gz`. Marker: `/tmp/h11b/repro.done`.
