# H11 reproducer report (C11, C31's rest, C102, C111, C112, C224, the hint)

Child: `reproducer` (claude-opus-5-5, xhigh), isolated worktree of `476ad4cf8`, 2026-10-24. Returned as its final message (children can't write report files) and saved here by the parent unchanged. Archive: [h11-reproducer-probes.tar.gz](h11-reproducer-probes.tar.gz) (generator, runner, report script, cases, results, probes, tests, prototype patches, cost data; no binaries).

---

I reproduced every H11 defect on HEAD `476ad4cf8` and found three new ones. The work is 14 test files in my worktree (11 RED, 3 guards) and a 1,639-case matrix.

**Summary**
1. All seven claims reproduce. Of the 1,636 valid cases, 448 match Node; 3 more are generator errors (tsc rejects `S extends any` patterns) and are excluded.
2. C11: 347 of 569 Node-expected loose-local cases throw `TypeError: Expected T, got undefined` at destinations that admit undefined. C31's rest: 297 of 381 throw (219 RangeError, 78 TypeError at the binding).
3. C102: defaults at holes fail in every binding form. C111 also affects `string[]`, not only object arrays. C112 affects `string[]`, number arrays and object arrays. C224: all 7 sources fail in all 18 `unknown`/`any` contexts (126 RangeErrors).
4. Item 3 (the hint) reproduces: a `(number | undefined)[]` pattern in a parameter, a `for…of` binding or a nested pattern throws `RangeError: Array index 1 is out of range (length 1); if it may be missing, read it with `?? fallback`, give the element type `| undefined`, or check the length first`.
5. New: N1 and N2 are compile failures (PTS1001) on valid code; N3 is a gap in C31's "fixed for `any`" claim. The biggest risk for the fix: making more pattern names loose the way declarations already are costs about 3.8× in hot loops.

**Matrix on HEAD** ("prototype OK" = the throwaway fixes described under root causes)

| family / finding | expected | cases | OK | WRONG | TYPEERR | RANGEERR | PTS1001 | prototype OK |
|---|---|---|---|---|---|---|---|---|
| A C11 | node | 569 | 221 | 0 | 347 | 0 | 1 (N1) | 519/569 |
| A C11 | by-design | 69 | 0 | 0 | 68 | 0 | 1 (N1) | unchanged |
| B C31 | node | 381 | 84 | 0 | 78 | 219 | 0 | 381/381 |
| B C31 | by-design | 144 | 0 | 0 | 57 | 87 | 0 | 144 TypeError |
| C C102 | node | 44 | 7 | 1 | 6 | 24 | 6 (N2) | 7/44 (not targeted) |
| C C102 | by-design | 16 | 0 | 14 | 0 | 2 | 0 | — |
| D C111 | node | 75 | 18 | 0 | 0 | 57 | 0 | — |
| D C111 | by-design | 45 | 12 | 18 | 0 | 15 | 0 | — |
| E C112 | node | 66 | 42 | 24 | 0 | 0 | 0 | — |
| E C112 | Node's output or RangeError | 33 | 15 | 18 | 0 | 0 | 0 | — |
| F C224 | node | 168 | 42 | 0 | 0 | 126 | 0 | 161/168 |
| F C224 | by-design | 7 | 0 | 0 | 0 | 7 | 0 | unchanged |
| G extras | node / by-design | 18 | 7 | 0 | 1 | 10 | 0 | — |

- **What already works for C11:** passing the name as an argument to the program's own function, method or arrow whose parameter is optional, `T | undefined`, defaulted, `unknown`, `any` or a rest; also `??`, `||`, `console.log`, templates and `===`.
- **What fails for C11:** optional properties (shorthand and `k: v`, pushed, returned, `Partial`), a `new` call's optional parameter, `T | undefined` / `unknown` / `any` locals, results and arrows, `(T | undefined)[]` and `unknown[]` literals, `push`, `Map.set`, `Set.add`, `Box<T | undefined>`, assignments, conditional branches, `opt(c ? v : x)`, `opt(k && v)`, `JSON.stringify({ k, v })` and `console.log([k, v])`.
- **By-design rows:**
  - C11: the 68 checks are D-090's TypeError, as documented.
  - C31: 87 of the 144 throw RangeError where the acceptance asks for D-090's TypeError when the name is used as its type.
  - C102: number arrays have no holes (D-085), and rests and spreads of `string[]` or object arrays keep holes (D-182, D-183).
  - C111: a required callback parameter at a hole throws D-085's RangeError.
  - C224: required places throw D-015's RangeError.
- **Stability:** 45 random cases rerun gave 0 differences.

**RED tests** (in `pts/tests/run/`; `// expect:` checked with Node; all fail in `TestRun`/`TestRunMatchesNode`)
- `loose_locals_undefined_destinations.pts` (C11): 15 sections print `threw TypeError`.
- `loose_locals_declared_shorthand.pts` (N1): PTS1001 at 15:15 and 16:30.
- `pattern_names_past_end_parameters.pts` (C31, item 3): RangeError for arrays, `(number | undefined)[]`, strings, split, Set, Map, defaults, generics, arrows, constructors, methods and callbacks; TypeError for Float64Array, Generator and Iterable.
- `pattern_names_past_end_for_of.pts` (C31): 8 lines throw RangeError or TypeError.
- `pattern_names_past_end_nested.pts` (C31): 8 lines throw RangeError. The last line is N5; drop it if you don't want error-class parity.
- `array_pattern_defaults_at_holes.pts` (C102: parameter, `for…of`, nested, object-nested, inside a rest, objects, match): 6 RangeErrors and 2 D-133 TypeErrors.
- `array_pattern_defaults_maybe_elements.pts` (N2): four PTS1001s.
- `optional_callback_parameters_at_holes.pts` (C111): 9 RangeErrors; the union and type-predicate controls pass.
- `find_shrinking_array.pts` (C112): `find`, `findIndex` and `findLast` stop at the new length.
- `element_reads_unknown_contexts.pts` (C224): 7 RangeErrors; the controls pass.
- `for_of_reads_holes_typed.pts`: RangeError for `string[]` and object arrays. D-183 assigns this to H11 but the plan line doesn't mention it — your call.
- The existing skipped `array_patterns_read_holes.pts`, run without its skip line, is still RED (6 lines).

**Guard tests** (pass on HEAD)
- `loose_locals_required_destinations.pts` (`no-diff`): D-090's TypeErrors.
- `element_reads_required_places.pts` (`no-diff`): D-015/D-085's RangeErrors with the hint.
- `array_patterns_typed_sources.pts` (compared with Node): tuples, map entries, `entries()` and in-range loops.
- `TestCompilerFailsCleanly` over all 14 files plus 3 mutants each passed (56 programs).

**Root causes, suggested fixes and risks**
- **C11:** `pts/lower/unions.go:179-185` (`narrowed`) and `pts/lower/objects.go:847-850` (`shorthandValue`) do the checked `looseRead` unless the name is a tested operand. Arguments only count as tested in calls (`loose_locals.go:114-118`, `CallExpression` only, so `new` is missed), and builtins' signatures are excluded (`loose_locals.go:75-77`).
  - Prototype (`proto/c11_c224_prototype.patch`): skip the check when `contextTakesUndefined || takesEveryValue`. A went from 221 to 519 OK with no by-design check lost.
  - It added 41 new PTS1001s, because `sameType` (`lower.go:3708`) rejects a union operand next to a `T` for `c ? v : x` and `k && v`, and `[k, v]` is made as a `string[]`. A real fix must widen the other branch or operand to the union there.
  - Literals without a contextual type (`JSON.stringify({ k, v })`) keep tsc's narrowed member types and still check; decide whether that stays by design.
  - No new loose names, so no cost.
- **C31:** `readsElement` (`loose_locals.go:166-173`) only makes names loose in a variable declaration with an initializer. Parameter, `for…of`, nested and rest-element names get `ArrayElementChecked` (`destructuring.go:535-538`) or a checked conversion of the dynamic item at the binding (`destructuring.go:261-263`).
  - Prototype (`proto/c31_prototype.patch`, 6 lines): use `l.typeAt(pattern)`, which is the parent declaration's type for any pattern position. B went from 84 to 381/381; all 144 by-design cases became TypeError (the acceptance); the hint cases pass 3/3; guards pass 6/6.
  - Cost (callgrind instructions on the 20M-iteration loops): `for (const [x, y] of rows)` goes from 1.24 G to 4.66 G (3.75×, 118 to 528 ms); a `dot([x1, y1], [x2, y2])` parameter pattern goes from 2.38 G to 9.21 G (3.87×).
  - The cost is the out-of-line `pts_array_element` (kind dispatch and boxing, `pts/runtime/src/array.c:288`) plus a redundant `number | undefined` check at the binding, before the inline unbox at each use. The fix needs an inline typed read that keeps "missing" separate (a value plus a presence flag, or an inline box with an undefined sentinel), or this lands on every row-destructuring loop.
- **Item 3:** the same checked read; with C31 fixed the hint can no longer reach a `(T | undefined)[]` pattern. Sharpening the hint text itself (`pts.h:1070`, `array.c:154-161`) is optional.
- **C102:** `elementPart` (`destructuring.go:538-550`) reads an element under a default as `inRange ? checked read : default`, so a hole within range throws (D-085, or D-133 for a match). A rest is an `ArraySlice` that keeps holes (`:532-533`). Suggested: read pointer and match elements with `ArrayElement` and apply `withDefault` on undefined, as `destructureIterable` already does; for `(T | undefined)[]` rests, read holes as undefined (D-182's as-read copy).
- **N2:** in the same code, the default is converted to the element type (`:546`), so the result is `T | undefined` and can't become the name's `T`. Convert the default to the name's type instead.
- **C111:** `widened()` (`callbacks.go:690-702`) only accepts `Value` or a union with undefined; an optional parameter's IR type is `P` followed by a `Given` flag. Suggested: count an optional parameter as taking undefined and call it with `Given = !isUndefined(element)` in `callbackArguments` (`callbacks.go:517`). Only `find` and its kin and `Array.from` over pointer arrays are touched. Odd detail: `Array.from(points(), (p?: P, i?: number) => …)` already works but `(p?: P) => …` doesn't.
- **C112:** `loopFrom` (`callbacks.go:464-473`) tests `i < current length` unless `readsEveryIndex` (`:666-672`), which needs undefined in the element type or a widened callback. Number arrays are never widened (`:695`), so even `(n: number | undefined)` stops. Suggested: `find` and its kin always read every starting index, with a checked read (D-085 RangeError) where the parameter can't take undefined.
- **C224:** `lower.go:1553` only reads undefined when the context takes undefined or the element type holds it. Add `takesEveryValue(node)` there and at `:1560` (in the prototype: F from 42 to 161/168). The 7 left are `unknown ? s[5] : 1` conditionals whose own type lacks undefined.

**New findings**
- **N1 (PTS1001 on valid code):**
  ```ts
  const args = ["--out"];
  const value: string | undefined = args[1];
  console.log({ value }); // also JSON.stringify({ value })
  ```
  This fails with `PTS1001: converting string | undefined to string is not supported yet`. `objects.go:848` converts to the declared type instead of the narrowed one, which by accident is why optional-property shorthands work for such locals.
- **N2 (PTS1001 on valid code):**
  ```ts
  const parts: (string | undefined)[] = ["a"];
  const [first = "x", second = "y"] = parts;
  ```
  Same PTS1001 in declarations, parameters, `for…of` and nested patterns.
- **N3:** `const src: any = ["x"]; const [a, ...[b]] = src;` throws RangeError where Node gives undefined. The ledger's C31 row says H03a fixed `any` values in every pattern; this is the gap, and it falls under H11's "patterns inside a rest element".
- **N4 (performance, already on HEAD):** loose declaration patterns in hot loops cost 3.75× typed reads (`const [x, y] = rows[r]`: 4.66 G vs 1.24 G instructions, 611 vs 118 ms).
- **N5 (minor):** destructuring a missing nested element (`const [[a], [b]] = rows` with one row) throws RangeError where JS throws TypeError.
- **Not a bug:** `for (const x of match)` throws D-133's TypeError at an undefined group, as D-133 documents. It is the same shape as the D-183 `for…of` note.

**Commands** (no case binaries kept; `df` showed 32 GB free at the end)
- Build: `GOMAXPROCS=2 make build` in the worktree. Prototype binaries were built to `/tmp/h11/repro/proto{,2,3}/pts` and the source reverted.
- Matrix: `python3 -I gen.py /tmp/h11/repro/matrix`, then `python3 -I run_matrix.py cases.jsonl results.jsonl --jobs 3`. It ran 1 job at a time until `baseline.done` appeared.
- Prototype runs: add `--pts /tmp/h11/repro/proto/pts --filter '"family": "[AF]"'`, or `proto2/pts` with `"[BCG]"`.
- Summaries: `python3 -I report.py results.jsonl [--family X --by form|src|dest | --list CLASS]`.
- Tests: `python3 -I probes/check_expect.py [--unskip] <files>` (with `PTS_BIN=` for a prototype), `bash probes/harness.sh <names>`, `bash probes/smoke.sh`.
- Cost measurements: `bash probes/perf.sh` (callgrind and hyperfine, with `PTS_BIN=` for a prototype).

**Worktree:** `/home/bryan/projects/parallelscript/.claude/worktrees/agent-ab8a9f5ac0c519f83`. It holds the 14 untracked test files on HEAD `476ad4cf8`. The five `pts/third_party` entries show as type changes because they are symlinks to the main checkout; don't copy those.

**Archive:** `/tmp/h11/h11-reproducer-probes.tar.gz` (generator, runner, report script, cases, all results files, probes, the 14 tests, the three prototype patches, cost data). `/tmp/h11/repro.done` is written.
