# H12 review log

Child: `reviewer` (claude-opus-5-5, xhigh), read-only; each round's report is its final message, saved here by the parent unchanged but for the round's heading. Probes: `h12-reviewN-probes.tar.gz`.

## Round 1 (commit `752397090`, snapshot 2)

**Verdict: NOT READY.** I found one High and six Medium problems. Three of the Mediums are new regressions in this commit. The other three were already on the base, but the slice says it fixes them (C326, C327) or should have caught them.

Every finding below is verified by running a probe against Node, the base CLI (`/tmp/h12/base`) and snapshot 2 (`/tmp/h12/snap2`). Probes are in `/tmp/h12/review1/p/`. `/tmp/h12/review1/tools/rerun_findings.sh` reruns all of them, and its output is saved in `/tmp/h12/review1/rerun-findings.out`.

Some things hold up well:
- All 17 new tests' `// expect:` blocks match Node exactly.
- The reproducer's matrix against snapshot 2 has 3,099 cases right and 148 PTS1001. No case that was right on the base is now wrong.
- Many other areas check out (listed at the end).

### Findings

**R1-F1 (High, regression, verified): a rest-tuple assignment into a variable declared `T | undefined` crashes with SIGSEGV.**
- Where: `pts/lower/destructuring.go:586`, the new branch for assignment targets of a rest tuple.
- What happens:
  - Past the end, the branch reads the element with an unchecked `ArrayElement`.
  - The store's check accepts undefined, because the variable's declared type holds it, so the variable gets undefined.
  - The variable isn't loose, because `assignedValues` only collects `=` with an identifier on the left, never targets inside a pattern.
  - tsc narrows the variable to `string`, and the next read `pts_value_as_object(s)` is unchecked.
- Repro: `s27_rest_tuple_assign_declared.pts` (`const r: [number, ...string[]] = [1]; let s: string | undefined = "s"; [h, s] = r; s.length`).
  - Node: `threw TypeError`
  - Base: `PTS1001: converting number | string to number`
  - Snapshot 2: exit 139
- A generic instance crashes the same way (`s32_rest_tuple_generic.pts`, snapshot 2 exit 139).
- Member and element targets are checked and work (`s30`).
- Suggested fix: count identifier targets of a pattern as writes in `writesMissing`, or check the element against the target's narrowed type.

**R1-F2 (Medium, already on the base, C326 fix incomplete, verified): a cycle of writes between two variables is cached as "not loose" and crashes.**
- Where: `pts/lower/loose_locals.go:178`. The `looseSymbols[symbol] = false` placeholder written while the answer is being computed gets cached for the other variable in the cycle.
- Repro: `s20_mutual_loose.pts` (`let y…; let x…; y = x; x = y; y = arr[3]; x = y; x.length`).
  - Node: `threw TypeError`
  - Base: 139
  - Snapshot 2: 139
- The result depends on which variable is asked about first. With the two declarations swapped (`s20b`), snapshot 2 matches Node.
- Fix: compute a fixpoint, or don't cache answers that depended on a variable still being computed.

**R1-F3 (Medium, already on the base, C327 fix incomplete, verified): the "loose" answer is cached per symbol but computed from one generic instance's types.**
- Where: `loose_locals.go:185` and `:199`. `narrowsByWrites` checks the instance's declared type for `any`, and `missing` uses the instance's `typeAt(value)`. readsElement uses the type parameter's constraint so every instance agrees; these two don't.
- Repro: `s35_generic_unknown_first.pts` (`function f<T>(xs: T[], show) { let x: T | undefined; x = xs[3]; return show(x) }`, called first with `unknown[]`, then with `string[]`).
  - Node: `undefined` / `threw TypeError`
  - Base: 139
  - Snapshot 2: 139
- With the string instance first (`s36`) or alone (`s37`), snapshot 2 matches Node.
- An `any` instance asked first does the same (`s33`, snapshot 2 exit 139).

**R1-F4 (Medium, regression, verified): the generator starter runs the parameter defaults but has no stack check.**
- Where: `pts/cgen/generators.go:157`. The step function gets `pts_stack_check()` when `mayRecurse` (line 186); the starter that now runs the defaults doesn't.
- Repro: `g01_starter_recursion.pts` (`function* g(n, inner = g(n + 1))`).
  - Node: `threw RangeError`
  - Base: `made 0` (wrong, but no crash)
  - Snapshot 2: exit 139
- Fix: emit `pts_stack_check()` in the starter when `Prologue > 0 && mayRecurse`.

**R1-F5 (Medium, regression, verified): methods whose unannotated default may be undefined no longer match interfaces or overrides.**
- Where: `pts/lower/lower.go:1357` (`parameterType`). Keeping undefined there changes the type callers pass (now `T | undefined` instead of `T` plus the given-flag), not just the parameter's type inside the body.
- Repro: `p02_iface_method_default.pts` (`interface I { m(v?: string) }`, `class C { m(v = this.map.get("q")) }`, `const i: I = new C()`).
  - Node: `Q y`
  - Base: `Q y`
  - Snapshot 2: `PTS1001: using a C as a {m(string, given): string}…`
- `p03_override_default.pts`: Node and base `DQ Dy B-`; snapshot 2 `PTS1001: overriding B.m with other parameter or result types`.
- `p07` (abstract method): base gave a runtime TypeError, snapshot 2 now fails to compile.
- Plain functions, bind/call/apply and callbacks all work (`p08`, `p10`).
- Suggested fix: leave the caller-side type stripped and only widen the body's local, as the loose-parameter case already does.

**R1-F6 (Medium, regression, verified): copying an object-literal default (`patternDefault`) replaces honest compile errors with run-time divergence.**
- Where: `pts/lower/optional.go:200`. The copy runs even when the literal lacks a *required* member of the part's type.
- `o08_nested_default_required_forof.pts` (`for (const { opts: { size } = { size: 10 } } of items)` with `opts?: { size: number; color: string }`; tsc accepts it):
  - Node: `3 10 20`
  - Base: PTS1001
  - Snapshot 2: prints `3`, then `Uncaught TypeError: Expected {color: string; size: number}, got Object` when the default is taken.
- `o01` shows the same thing in a declaration.
- `o04_nested_default_getter_order.pts`: the copy runs the literal's getters before earlier defaults. Node prints `default x,get b`; snapshot 2 silently prints `get b,default x`; base gave PTS1001.
- Suggested fix: copy only when every member the literal lacks is optional and the literal has no accessors; otherwise keep the PTS1001.

**R1-F7 (Low, regression, verified): a record pattern with a default silently ignores inherited keys.**
- Where: `destructuring.go:348`. `MapGet` returns undefined for an inherited key such as `toString` or `__proto__`, so the default is used.
- `r01b`: Node `function`, base `RangeError`, snapshot 2 `number`.
- `r05b` (`__proto__`): Node `object`, base `RangeError`, snapshot 2 `number`.
- Reading `rec.toString` directly is PTS1001 on both. Suggest reporting PTS1001 when tsc resolves the key to an `Object` member.

**R1-F8 (Low, regression against the base's accidental result, verified): numbers newly loose throw where Node gives NaN or false.**
- `s10b_num_ops.pts` (`let n: number | undefined = 1; n = ns[0];`):

  | Expression | Node | Base | Snapshot 2 |
  |---|---|---|---|
  | `n + 1` | `NaN` | `NaN` | TypeError |
  | `n > 0` | `false` | `false` | TypeError |
  | `n === 0` | `false` | `false` | TypeError |
  | `Math.max(n, 1)` | `NaN` | `NaN` | TypeError |

- This follows D-090's rule (TypeError where JS goes on with undefined; `const [u] = ns; u * 2` already throws on the base). But D-222 explains the TypeError as "as Node's member read of undefined throws", which isn't true for numbers. Please document it.

**R1-F9 (Low, already on the base, verified):** for an integer-typed parameter, `function h(v: int | undefined = undefined)` gives `PTS2001: a number doesn't convert to int` on both CLIs (`n03`), so C306's `= undefined` form doesn't reach the semantic pass for integer types. Also on both: `Map<string, u8>.get` is treated as a plain number (`n02`, `n04`).

**R1-F10 (Low, test quality):**
- C330 (N3, the object copy) and C331 (N4, the `FromUnion{Defined}` check) are listed as fixed but have no regression test. The reproducer's `nfind` probes pass on snapshot 2 but aren't committed.
- There are no tests for R1-F1 to F6. `pattern_assignment_rest_tuples.pts` only covers full tuples into targets without undefined.
- The comment on `ir.Func.Generator` in `pts/ir/ir.go` ("returns it without running Body") is out of date now that `Prologue` exists.

### Checked and correct (snapshot 2 matches Node)
- **Defaults on exact undefined:** class getters typed `undefined` or `void`, interface getters, tuples, callbacks, `for…of`; no default for null, 0, "", false or NaN.
- **Evaluation order:** getters read once and before the default, defaults left to right, `next()` interleaved with defaults, the iterator closed when a default throws, target references evaluated first.
- **Records:** missing, present-but-undefined and numeric-like keys; shorthand and property assignments.
- **Parameter callers:** argument left out, literal `undefined`, a variable holding undefined; bind, call, apply; callbacks to array methods; generic `d: T, v = d`; numbers.
- **Generators:** `this`, `super`, generic class methods, `[Symbol.iterator]`, `yield*`, closures and cells, static methods, rest and tuple rests; async generators throw at the call while async functions reject; `parallel for` at 1 and 4 threads.
- **Generator starter safety:** it uses a plain setjmp try, and the frame is `GC_MALLOC`'d and reachable from the stack.
- **Loose reads:** declared unions written in switch, instanceof, closures, generators, async functions and module scope all throw TypeError rather than crashing.
- **No regressions:** `unknown` variables and hot loops with unannotated defaulted parameters emit identical C.
- **Unchanged on both CLIs:** destructuring assignments over plain arrays, chained assignments, `as`, `!`, `satisfies` and literals all throw RangeError or TypeError, never SIGSEGV.

### Files
- Probe archive: `/tmp/h12/review1/h12-review1-probes.tar.gz`
- Rerun script and saved output: `/tmp/h12/review1/tools/rerun_findings.sh`, `/tmp/h12/review1/rerun-findings.out`
- Comparison helpers: `/tmp/h12/review1/tools/both.sh`, `/tmp/h12/review1/tools/cmp.sh`, `/tmp/h12/review1/tools/expect_vs_node.py`

## Round 2 (commit `0cdb97302`, snapshot 3)

**Verdict: NOT READY.** All nine round-1 fixes work, but two new problems block it:
- **R2-F1 (High):** the R1-F1 crash class still has five SIGSEGV shapes.
- **R2-F2 (Medium):** the new cycle handling in `isLoose` takes exponential time, so some valid programs no longer compile in reasonable time.

One Low remains. Every finding is verified with a probe run against Node, the base, snapshot 2 and snapshot 3.

### The round-1 fixes
- **Round-1 probes:** I replayed all 95 of my round-1 probes (`replay-r1.json`). Every probe that matched Node on snapshot 2 still matches on snapshot 3.
  - These now match Node: `s27`, `s32`, `s20`, `s33`, `s35`, `g01`, `p01`, `p02`, `p03`, `p06`, `p07`, `n03`.
  - These are back to the base's honest PTS1001 or RangeError: `o01`, `o04`, `o08`, `r01b`, `r05b`.
  - `n02` now runs and gives u8's wrapping result, which is the intended behaviour for that type.
- **Matrix:** the reproducer's 3,438 cases on snapshot 3 give 3,099 OK and 148 PTS1001, identical to snapshot 2. No case got worse than on snapshot 2 or the base (`matrix-snap3.jsonl`).
- **emit-c sweep, snapshot 2 against 3 (1,461 programs):** only nine test programs changed.
  - Changed: the generator tests (they gain the starter's stack check), the new round-1 tests, and `parameter_defaults_unannotated_and_tested` (the caller-side type goes back to `string` plus the given flag).
  - No corpus program changed (`sweep23.json`).
- **Cycles:** a fuzzer generated 200 programs where declared-union variables write each other, with missing values entering through element reads, loose bindings, parameters, rest tuples, closures, conditionals and `||=`.
  - Snapshot 3 matches Node on every case except 20 from the second batch, which all fail the same way on snapshot 2. That is the `||=` gap filed as R2-F3, not the cycle logic.
  - On the first batch, snapshot 2 crashed in 8 of 100, so the fuzzer does catch this class.
- **Raw types in generic instances:** constrained type parameters, generic classes, and `unknown` or `any` instances compiled first all match Node (`s41`). Probes that try to make instances disagree are unreachable: tsc or PTS1001 rejects them.
- **Starter stack check:** methods, async generators, mutual recursion and closures all give RangeError; a finite depth of 2,000 works (`g10`).
- **Methods with every kind of default:** an optional property, `maybe()`, a number, an element read, `m.get` and a literal all match Node through interfaces (`m01`), overrides in both directions (`m02`), and loose or element-read arguments (`p20`).
- **Literal default copies:** methods, arrow properties and plain fields still work (`o10`).
- **Annotated defaults, unchanged on all three builds:** an annotated `string | undefined` or required `T | undefined` parameter implementing an interface is still PTS1001 (`m04`, `m05`, `p05`).

### Findings

**R2-F1 (High, verified): R1-F1 isn't fully fixed; five assignment-pattern shapes still crash with SIGSEGV.**

Where: `pts/lower/loose_locals.go`, `arrayPatternTargets` and `patternElementMayBeMissing`.

`arrayPatternTargets` only looks at an array literal that is directly the left side of `=`. So these targets are never collected:
- an array pattern inside an object assignment pattern;
- a nested array pattern that has its own default.

In the second case, the inner `[h, s] = [0, "d"]` is treated as a top-level assignment whose source is the default. The default is a tuple, so `patternElementMayBeMissing` returns false, but the real source is the outer element, a short rest tuple.

| Probe | Shape | Node | Base | Snapshot 2 | Snapshot 3 |
|---|---|---|---|---|---|
| `w01_obj_in_assign_array.pts` | `({ a: [h, s] } = o)`, `o.a: [number, ...string[]] = [1]`, `s: string \| undefined`, then `s.length` | `threw TypeError` | PTS1001 | 139 | 139 |
| `w07_obj_nested_deeper.pts` | `[{ a: [h, s] }] = rows` | `threw TypeError` | PTS1001 | 139 | 139 |
| `w08_shorthand_obj_rest.pts` | `({ a: [h, (s)] } = o)` | `threw TypeError` | PTS1001 | 139 | 139 |
| `w11_param_object_array.pts` | `({ a: [h, s] } = o)` where `s` is a parameter | `threw TypeError` | PTS1001 | 139 | 139 |
| `w06_nested_default_rest.pts` | `[[h, s] = [0, "d"]] = rows`, `rows: [number, ...string[]][] = [[1]]` | `threw TypeError` | PTS1001 | 139 | 139 |

- The C for `w01` reads `pts_value_as_object(s)` unchecked after storing undefined.
- These variants are fine: a parenthesized target `[h, (s)] = r`, a conditional source, a nested default typed as an array (`w09`), and member or element targets.
- Suggested fix:
  - Collect identifier targets of every array literal under a destructuring assignment's left side, including inside object-literal property values.
  - Treat an array pattern as nested, so missing is true, whenever its `=` binary is itself an element or property value of an outer pattern, rather than taking that binary's right side as the source.

**R2-F2 (Medium, regression against snapshot 2 and the base, verified): `isLoose` now takes exponential time on dense write graphs.**

Where: `loose_locals.go`, `isLoose` with `looseStack`/`looseLow`. A provisional false is never cached, so every simple path through the write graph is explored again, once inside each decision and again at each later top-level query. The answers it gives are correct; only the cost is wrong.

| Probe (emit-c only) | Snapshot 2 | Snapshot 3 |
|---|---|---|
| `dense10.pts` (10 `string \| undefined` variables, each written every other) | 0.45 s | 13.4 s |
| `dense12.pts` | 0.63 s | over 120 s (timeout) |
| `sparse40_3.pts` (40 variables, each written from 3 random others) | 0.47 s | over 120 s |
| `c10_register_machine.pts` (13 optional registers, a `case` with `r_i = r_j` for every pair) | 0.38 s | over 120 s; separately timed over 300 s |

- The base also compiles `dense12` and `sparse40_3` in about 0.5 s.
- Scaling of the register machine on snapshot 3, measured while the matrix used three cores: 9 registers 2.7 s, 10 registers 24 s, 11 registers 277 s.
- Suggested fix: when the root of a strongly connected component of writes finishes as false, cache false for every member of it (Tarjan). Alternatively, compute the loose set once as a worklist fixpoint over the write graph.

**R2-F3 (Low, regression against the base's accidentally right result, verified): the right side of a logical assignment isn't treated as a place that takes undefined.**
- Repro `l11_or_assign_loose_rhs.pts`: `v1` and `v2` are declared `string | undefined` and initialised from a loose binding, then `v2 ||= v1`.
  - Node: `undefined`
  - Base: `undefined` (it read `v1` unchecked and got lucky)
  - Snapshots 2 and 3: `Uncaught TypeError` at that line
- `v1` is newly loose, and `isPlaceValue` only treats the right side of `=` as a place value, not of `||=`, `??=` or `&&=`. So the read of `v1` is checked as a string.
- `v3 ??= lb` already threw on all three builds.
- This is D-090's TypeError where Node carries on with undefined, which R1-F8 now documents. Including logical-assignment right sides in `isPlaceValue`, when the target's type holds undefined, would match Node.

### Test quality
The seven new tests match Node and test what they claim. None covers R2-F1's object-enclosed or default-nested patterns, and none covers write graphs denser than two variables.

### Files
- Probe archive: `/tmp/h12/review2/h12-review2-probes.tar.gz`
- Rerun script: `/tmp/h12/review2/tools/rerun_findings.sh` (output in `/tmp/h12/review2/rerun-findings.out`)
- Supporting tools: `/tmp/h12/review2/tools/run3.py`, `/tmp/h12/review2/tools/gen_cycles.py`
- Results: `/tmp/h12/review2/replay-r1.json`, `/tmp/h12/review2/matrix-snap3.jsonl`, `/tmp/h12/review2/sweep23.json`
