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

## Round 3 (commit `3f62244ff`, snapshot 4)

A fresh `reviewer` (the round-1/2 reviewer was killed with session 1 during its round 3, before it reported; this one was briefed with the log, the earlier probe dirs and the killed round's leads in `/tmp/h12/review3/`).

**Verdict: NOT READY.**

Round 2's three fixes work, and nothing that matched Node on snapshot 2 or 3 is worse on snapshot 4. But the class this slice says it fixes still crashes:
- **R3-F1 (High, pre-existing, in scope):** an element read or a loose local inside `a || b`, `a && b` or `(x, b)` that is written into a variable declared `T | undefined` still crashes with SIGSEGV. The ledger marks C327 "fixed — Never a crash". `best = cached || items[i]` is ordinary code.
- **R3-F2 (Medium):** looseness decided outside a generic instance is cached for the instance. That gives a SIGSEGV (pre-existing) and one regression from `752397090`: a RangeError where the base matched Node.

There are two Lows and some test-quality notes. I ran every finding on Node, base, snapshot 2, snapshot 3 and snapshot 4 (`tools/rerun_findings.sh`, saved as `rerun-findings.out` / `.json`).

## Findings

### R3-F1 (High, verified, pre-existing, in H12's scope: C327/C326)
**What happens:** the value written is a logical or comma expression. The lowering lets its element read or loose local through as undefined, but the variable is never made loose. tsc narrows it to `T`, and the read is unchecked.

**Where:**
- `pts/lower/loose_locals.go:371–387`: `missingSources` only looks at a conditional, an element read or an identifier.
- The lowering lets undefined through in two places:
  - `contextTakesUndefined` (`unions.go:787`, via `fallbackOf`). The right side of `||`/`??` and of `&&`/`,` gets the whole expression's contextual type, so `lower.go:1587` reads the element with `ArrayElement`.
  - `sameType`/`takesOperandsUndefined` (`lower.go:3803`). A loose local on the left of `&&` goes as its union.
- C for `x01`: the write is `x = (…truthy(a) ? a : pts_array_element(arr, 3.0))`, and the read is `pts_value_as_object(x)` with no check.

Every probe below gives exit 139 on base and on snapshots 2, 3 and 4. Node prints a TypeError.

| Probe (`p/…`) | Shape |
|---|---|
| `x/x01_or_fallback_elem.pts` | `x = a \|\| arr[3]` |
| `x/x03_and_elem.pts` | `x = c && arr[3]` |
| `x/x04_init_or_fallback.pts` | `const x: string \| undefined = a \|\| arr[3]` |
| `x/x08_comma_elem.pts` | `x = (side(), arr[3])` |
| `x/x15_or_assign_or.pts` | `x \|\|= a \|\| arr[3]` |
| `x/x20_loose_and_left.pts`, `x21` | `x = lb && "s"`, `const x: string \| undefined = lb && lb.trim()` |
| `x/x22`, `x24`, `x25` | a parameter; a generic `T extends string`; a record's and a string's element |
| `pr/pr02`–`pr05` | returned value, generator across a yield, try/finally, a callback |
| `d/d02_05.pts` | a pattern default `[s = e \|\| arr[3]] = strs` |

`x/x16_number_or.pts` and `x41`: a number written the same way prints `NaN` silently on every build, where Node throws a TypeError.

These are safe today:
- `??` (an honest PTS1001)
- `as` and `satisfies` (D-085's RangeError)
- `await` and `!` (TypeError at the write)
- `x = y = arr[3]` and `x = (y ||= lb)` (TypeError at the write)
- property targets

**Suggested fix:** in `missingSources`, look inside the operands the lowering lets through:
- `||` and `??`: the right operand;
- `&&`: both operands;
- `,`: the right operand.

Add these shapes to `element_reads_assigned_to_declared_unions.pts`.

### R3-F2 (Medium, verified: the SIGSEGV is pre-existing, the RangeError is a regression since `752397090`)
**What happens:** `decideLooseness` explores across functions. When a module-scope variable is decided first, a generic function's local is decided outside its instance.
- `isElementRead` → `isArrayExpression` → `typeAt` sees `T` there, not `string[]`, for `xs: T extends string[]`.
- That "not loose" answer is cached in `l.looseSymbols` (`loose_locals.go:305`) and then used inside the instance.
- Where: `loose_locals.go:379` (`missingSources`) and `:428` (`readsElement`); `arrays.go:58`.
- C for `g20`: `pts_value_as_object(x)` unchecked. For `g22`, the same function with no module variable, it is `pts_value_defined(x, …)`.

| Probe | Node | base | snapshot 2 | snapshot 3 | snapshot 4 |
|---|---|---|---|---|---|
| `lead/g20_generic_dep_from_module.pts` (`M = x` from a generic local) | `threw TypeError / true` | 139 | 139 | 139 | 139 |
| `lead/g24`, `g/g30` (generic class method), `g/g32` | TypeError | 139 | 139 | 139 | 139 |
| `g/g34_generic_tested.pts` (`const x = xs[3]` tested, `M = x`) | `none / threw TypeError` | **matches Node** | RangeError | RangeError | RangeError |
| controls `lead/g22`, `g/g36` (no module variable); `lead/g23` (`xs: T[]`) | — | — | — | match Node | match Node |

**Suggested fix:** decide `isElementRead` for looseness with tsc's own types, using a type parameter's base constraint, as `readsElement` already does for binding patterns and `iteratesHoles` does. Or don't cache answers for a generic function's symbols that were computed outside its instance.

### R3-F3 (Low, verified, pre-existing, in scope: assignment-pattern defaults)
**What happens:** `({ p = arr[3] } = o)` with `p: string | undefined` throws D-085's RangeError on every build. Node prints `true` for `p === undefined`. The renamed form `({ p: q = arr[3] } = o)` matches Node (`d/d02_04`).
- Probe: `d/d03_shorthand_default_tested.pts`.
- Where: `isPlaceValue` (`loose_locals.go:660`) and `assignedVariable` (`:61`) don't handle a ShorthandPropertyAssignment's `ObjectAssignmentInitializer`.
- Suggested fix: handle it in both. If only `isPlaceValue` changes, it becomes a SIGSEGV, because `p` would not be loose.

### R3-F4 (Low, verified, pre-existing, outside scope; no crash)
**What happens:** a loose binding read through a namespace import or a default export is checked at the write (an uncaught TypeError), where Node throws later inside the `try`.
- `m/mod3`: `z = A.first`.
- `m/mod6`: `export default first`.
- Base gives the same.
- The killed round's `mod1` crashed on base only through its named-import half, which this slice fixed.

### Leads from the killed round
- `f01`, `f06`: pre-existing (base gives the same). This is D-140's checked read of a variable a closure wrote. No crash.
- `la02`'s "str plain": C335, a documented gap.
- `w30`–`w32`: honest PTS1001 on every build. Assigning to `s!` is PTS1001 even outside patterns (`w/w40`–`w43`).

## What holds up
- **R2-F1:** `w01`, `w06`, `w07`, `w08`, `w11`, `w22`, `w24`, `w25` and `w33` match Node. The parent lists `w24` as a snapshot-3 crash; it now matches Node.
- **R2-F2:**
  - No compile time blows up any more. 200, 1,000, 3,000 and 6,000 variables with 3 writes each take 0.7, 2.3, 7.0 and 12.4 s on snapshot 4, against 1.2, 2.0, 6.2 and 14.8 s on base. Snapshot 3 runs past 300 s at 200 variables.
  - The answers are right: the big graphs, and 440 fuzzed programs on snapshot 4, match Node except `cyc21_038` (D-140, pre-existing). Contexts covered: module, function, generator, async, generic, loops, try/finally, `||=`/`??=`/`&&=`, patterns.
  - The fuzzers catch this class: snapshot 2 fails 73 of `fuzz5`'s 120.
- **R2-F3:** `la10`–`la12` show numbers, booleans, `string | number`, objects, `any`, `unknown` and null all match Node. The exceptions are documented: `n + 1` (R1-F8) and an inferred `string` (C335). Non-loose operands are unchanged on every build.
- **Replays:** all 96 round-1 and 37 round-2 probes. Nothing that matched Node on snapshot 3 differs on snapshot 4; nine more match.
- **emit-c sweep, snapshot 3 against 4 (1,464 programs):** only the three new tests changed.
- **Reproducer's matrix:** a sample of every 8th case (430) on snapshot 4 is identical to the killed round's full run. Classes are the same as snapshot 3, and none is worse than base.

## Test quality
- All three new tests match Node and pass on snapshot 4. The first two fail on snapshot 3 (139; TypeError).
- I built mutants with `go build -overlay`:

  | Mutant | Killed by |
  |---|---|
  | no propagation in the fixpoint | `loose_writes_cycles_and_instances`, `loose_locals_into_declared_unions` |
  | no object-literal case | `nested_assignment_patterns_rest_tuples` |
  | no `isPatternElement` check | `nested_assignment_patterns_rest_tuples` |
  | no default case | `nested_assignment_patterns_rest_tuples` |
  | no logical-assignment place | `logical_assignments_loose_values` |

- **`loose_writes_dense_graph` passes with no propagation at all.** Its declaration order lets each variable find a source that is already decided. So it only guards compile time, and on regression it fails by hanging until the package's 10-minute timeout. Its second graph ("keeps its variables plain") can't be seen in the output either.
- No test covers R3-F1, R3-F2 or R3-F3.

Everything is in `/tmp/h12/review3b/`:
- archive: `h12-review3-probes.tar.gz` (probes, tools, results, mutant overlays; the mutant binaries are deleted)
- `DONE` is touched
- the main tree is untouched

## Round 4 (commit `024674187`, snapshot 5)

The round-3 `reviewer`, resumed.

**Verdict: NOT READY.** Round 3's three fixes work for the shapes they target, and no probe that matched Node on snapshot 4 got worse except one family:
- **R4-F1 (High, regression of `024674187`):** looseness is now decided with types that ignore the instance, but only a bare type parameter or indexed access is replaced by its constraint. A type parameter inside an intersection or a union isn't, so those shapes crash again. Snapshot 4 matched Node on six of these probes because it decided inside the instance.
- **R4-F2 (Medium, pre-existing):** `satisfies T | undefined` is one more operand position that `missingSources` doesn't look through.

I tried a one-line fix for each as a `go build -overlay` build of snapshot 5. Both make every finding probe match Node, and the R4-F1 fix changes no result across 200 related run tests.

All finding probes were rerun on Node, base, snapshot 4 and snapshot 5 (`tools/rerun_findings.sh`, saved as `rerun-findings.out`). No probe binaries are left running (`pgrep -x p` finds none). Every subprocess now runs in its own process group, killed on timeout, and `g02` is excluded.

## Findings

### R4-F1 (High, verified, regression of `024674187` against snapshot 4)
**Where:** `instanceFreeType` (`pts/lower/generics.go`, new). It replaces only a type whose top-level flags are `TypeParameter` or `IndexedAccess`.

**What happens:** some shapes give a type parameter inside an intersection or union:
- `typeof v === "string"` gives `T & string`;
- a type predicate gives `T & string[]`;
- `T | string[]`, or a declared `T & string[]`.

These stay generic, so `isStringTyped` / `isArrayType` / `isRecordExpression` say no and the local isn't loose. In the instance, the element read still stores undefined, and the read is unchecked: `pts_value_as_object(x)`, where snapshot 4 emitted `pts_value_defined(x, "string")`.

| Probe (`p/b3/…`) | Shape | Node | base | snapshot 4 | snapshot 5 |
|---|---|---|---|---|---|
| `b30_typeof_string_alone` | `if (typeof v === "string") x = v[9]` for `v: T` | TypeError | 139 | matches Node | **139** |
| `b31_typeof_tested_init` | tested `const c = v[9]` (D-172) | `none not` | matches Node | matches Node | **RangeError** |
| `b32_predicate_array` | `v is string[]` predicate | TypeError | 139 | matches Node | **139** |
| `b37_union_with_param` | `xs: T \| string[]` | TypeError | 139 | matches Node | **139** |
| `b39_declared_intersection` | `xs: T & string[]` | TypeError | 139 | matches Node | **139** |
| `b41_predicate_record` | predicate to `T & Record<string, string>` | TypeError | 139 | matches Node | **139** |
| `b34` (b30 plus a module variable), `b40` (b30 with `e \|\| v[9]`) | | TypeError | 139 | 139 | 139 (not fixed) |

**Suggested fix (verified as overlay `mut/F1`):** also take the base constraint of intersections and unions:
```go
if t.Flags()&(checker.TypeFlagsTypeParameter|checker.TypeFlagsIndexedAccess|checker.TypeFlagsIntersection|checker.TypeFlagsUnion) != 0 {
```
- All eight probes above then match Node.
- 200 related run tests (`loose|pattern|assignment|rest_tupl|logical|element_reads|destructuring_default|generic_any_instance|parallel_sort|narrow`) give identical results on F1 and snapshot 5.
- Add `b30`, `b31`, `b32`, `b37` to `loose_writes_generic_locals_module_variables.pts`.

### R4-F2 (Medium, verified, pre-existing, in scope: C327's class)
**Where:** `missingSources` (`loose_locals.go`) skips only parentheses.

**What happens:** for `x = arr[3] satisfies string | undefined`:
- tsc hands the satisfies type down as the operand's context, so the element read gives undefined;
- the whole expression is typed `string`, so x is narrowed;
- the read is unchecked.

| Probe (`p/a/split/…`) | Shape | Node | base | snapshot 4 | snapshot 5 |
|---|---|---|---|---|---|
| `a01_satisfies_union_00` | `x = arr[3] satisfies string \| undefined` | TypeError | 139 | 139 | 139 |
| `a01_satisfies_union_01` | `x = (e \|\| arr[3]) satisfies string \| undefined` | TypeError | 139 | 139 | 139 |
| `a01_satisfies_union_03` | the same as an initializer | TypeError | 139 | 139 | 139 |

**Suggested fix (verified as overlay `mut/F2`):** in `missingSources`, add `case value.Kind == ast.KindSatisfiesExpression: return l.missingSources(value.AsSatisfiesExpression().Expression)`. All four `a01` cases then match Node. `!`, `as T` and `await` stay safe (checked at the write).

### R4-F3 (Low, test gap, verified by mutation)
Mutant N7 removes `isPlaceValue`'s new shorthand-initializer case. It survives every test. Probe `p/d/split/d04_shorthand_element_loose_01.pts` (`({ p = lb } = o); p === undefined`) kills it: Node and snapshot 5 print `true`, N7 throws at the default. Add that probe's shape to `assignment_pattern_shorthand_element_defaults.pts`.

### Notes (not findings)
- **Numbers now throw:** `a05_numbers_objects_05` (`n = z || ns[3]; n + 1`) now gives D-090's TypeError where Node, base and snapshot 4 print `NaN`. This is R1-F8's documented rule, applied now that `||` makes the variable loose.
- **Loose local on the right of `||` or `,`:** `p/r/split/r01_*` (`x = e || lb`, `x = (s, lb)`) throw at the write on every build where Node stores undefined. This is pre-existing D-090 behaviour and safe.
- **Instance order (`c01_1`):** `g<string>` after `g<string | undefined>` now throws D-085's RangeError, as `g<string>` alone does on every build. Snapshot 4 threw a TypeError that depended on the order instances were lowered.

## What I verified holds up
- **R3-F1:** every round-3 shape (`x01`–`x25`, `pr02`–`pr05`, `d02_05`) matches Node. So do the delta probes:
  - nested logicals (`a02_01`, `_02`, `_07`, `_09`)
  - comma chains (`a03_03`–`_05`)
  - logical-assignment and default values (`a04`)
  - numbers (`a05_00`, `_01`)

  The other shapes are honest PTS1001s (C341's class) or checked at the write.
- **R3-F2:** these match Node: `g20`, `g24`, `g30`, `g32`, `g34`, and `b07`–`b09`, `b11`, `b12`, `b20`, `b21`, `b23`–`b26`, `b28`, `b35`, `b36`. The covered cases are generic class fields, indexed access, erased copies, readonly arrays, narrowing to `T & {}`, nested arrays, generic arrow consts, static and async generics, tuple instances, and a branded constraint.
- **Leaks of `decidingLooseness`:**
  - The `narrowingAnswers` cache is cleared at depth 0, and no `isLoose` caller sits inside a narrowing question.
  - The emit-c sweep (snapshot 4 → 5, 1,467 programs) changed 7, exactly as you described:
    - the 3 new tests;
    - the generic-module test (snapshot 4 can't emit it: a PTS1001);
    - 4 `.par` programs whose `pivot` is now a typed local.
- **`declaredWithUndefined`:** `c01`–`c03` match Node or are unchanged, apart from the `c01_1` order note above.
- **R3-F3:**
  - Shorthand defaults (`d03`, `d04_*`) match Node, apart from `d04_04`: a plain `string` target, which keeps D-085's RangeError on every build.
  - `d02`, `d05`, `d09`, `d10` now match Node (numbers, ints, functions, a class instance, any/unknown targets, generics, nested patterns).
  - `d01` gets better: the literal default is now made as the declared type. Before, it threw `Cannot add property b`.
  - Getter order (`d07`), union, array and tuple targets (`d08`) and left-to-right order (`d06`) are unchanged.
  - An unannotated arrow default is tsc's TS7006 on every build, which confirms tsc gives a shorthand default no contextual type.
- **Replays:** 103 round-3 probes and 132 round-1/2 probes (without `g02`). None is worse than snapshot 4; 28 are better.
- **Fuzzing:** 530 earlier fuzz programs give the same results as on snapshot 4. The only differences from Node are `cyc21_038` (D-140) and nine programs my generator wrote with tsc errors.
  - A new fuzzer, `gen5`: 160 generic/logical programs, 0 crashes. Its differences are honest PTS1001s, generator tsc errors and the pre-existing `r01` TypeError.
- **Matrix sample:** 430 cases, identical to snapshot 4.
- **Cost:** 3,000 and 6,000 variables compile in 1.8 and 3.0 s, against 1.5 and 3.2 s on snapshot 4.

## Test quality
- All three new tests and the changed dense test match Node and pass on snapshot 5. The three new tests fail on snapshot 4: a crash, a PTS1001, a RangeError.
- Mutants built on snapshot 5:

  | Mutant | Killed by |
  |---|---|
  | N1 (no constraint replacement), N2 (no instance-free `typeAt`) | `loose_writes_generic_locals_module_variables` |
  | N3 (`&&` right side only), N4 (no `\|\|`/`??`/`,` case) | `element_reads_through_logical_operators_declared_unions` |
  | N5 (no shorthand write), N6 (no shorthand context) | `assignment_pattern_shorthand_element_defaults` (N4 kills it too) |
  | M1 (no propagation) | `loose_writes_dense_graph`, `loose_writes_cycles_and_instances` (139) |
  | N7 (no shorthand place value) | **survives** (R4-F3) |

- On snapshot 4 and N1, `loose_writes_generic_locals_module_variables` fails by its `defaulted` case's PTS1001 at compile time. Without that case, the other cases still crash (139) on both, so the test is effective. Splitting the default case out would make each case's failure visible.

Everything is in `/tmp/h12/review4/`:
- archive: `h12-review4-probes.tar.gz` (probes, tools, mutant and fix overlays; the binaries are deleted)
- `DONE` is touched
- the main tree is clean
