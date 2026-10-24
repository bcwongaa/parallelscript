# H11b review

Reviewer: `reviewer` (claude-opus-5-5, xhigh), read-only. Each round's report is its final message (children can't write report files), saved here by the parent unchanged.

## Round 1 (snapshot 2 = `813a156cc`; [archive](h11b-review1-probes.tar.gz))

**Verdict: NOT READY.** One High finding: loose locals' narrowed reads can now crash with SIGSEGV where the base threw TypeError. The rest is Medium or lower. Every claim below comes from a command I ran. The suite gate (`snap2-suite.done`, 0 failures) was already green. Probes are archived at `/tmp/h11b/h11b-review1-probes.tar.gz` and the marker `/tmp/h11b/review1.done` is written. Nothing in the snapshot or the repo was touched.

## Findings

**R1-F1 · High · new for D-090/D-172/D-220 loose locals; the same hole already existed for ordinary locals.**
Narrowed reads of a loose local to a member of its own union are no longer checked, so a wrong value is cast without a test and crashes.
- **Where:**
  - `pts/lower/loose_locals.go:459-463`: `looseRead` routes `isUnion(own) && isNarrowing` to `narrowedUnionRead(defined, …)`; the base used `checkedConversion`.
  - `pts/lower/unions.go:207-213`: `narrowedUnionRead` skips the boxed check when lower's `isClass(narrow)` (`lower.go:4107`) is true, and that includes shapes and tuples.
  - `pts/cgen/cgen.go:1314`: cgen's `isClass` excludes shapes, so a tuple is cast with an unchecked `pts_value_as_object`.
- **Emitted C (m20):**
  - snapshot: `(pts_class_tuple1 *)pts_value_as_object(pts_value_defined((*v), …))->pts_element_1->utf16_len`
  - base: `pts_value_as_object(pts_value_convert((*v), &pts_check_1))`
- **Repro m20** (the D-140 scenario: a closure changes the local after it was narrowed to a tuple):
  ```ts
  type Pair = [number, string];
  const xs: (Pair | string)[] = [[1, "a"]];
  function run(): void {
    let v = xs[0]; if (v === undefined) return;
    const change = () => { v = "str"; };
    if (typeof v !== "string") { change();
      try { console.log("d090", v[1].length, v[0] + 1); } catch (e) { console.log("caught", (e as Error).name, (e as Error).message); } } }
  run(); console.log("end");
  ```
  - Node: `d090 1 s1`, `end`
  - base: `caught TypeError Expected [number, string], got string`, `end`
  - snapshot: SIGSEGV (exit 139)
- **The same crash through a lying type predicate:**
  - m26: a predicate narrows `Pair | number` to `Pair`.
  - m12: `function isStr(v: string|number): v is string { return true }`, with `const v = xs[i]` tested for undefined, then `v.toUpperCase()`.
  - m13: the same with an array-pattern name.
  - In all three the base prints `caught Expected …, got number` and the snapshot gets SIGSEGV. Node gives TypeError.
- **Already crashing in the base:** an ordinary local narrowed by a lying predicate (m15), a stale tuple narrowing on an ordinary local (m21), and the `for…of` variable form (m23). Class narrowings stay checked (m14, m18).
- **Suggested fix:** in `looseRead`, keep the checked read for narrowings to a member that isn't a non-shape class; this costs only on narrowed union loose locals. Separately, make `narrowedUnionRead` box tuples and shapes, which fixes the existing D-140 hole (m21/m23).

**R1-F2 · Medium · `+` next to a union, any or type-parameter side gives a silently wrong value.**
- **Where:** `lower.go:3953-3968`. `addsAsNumber` treats `string | number`, `any`, `T` and branded numbers as "not a number", so the missing side concatenates.
- **Repro s03:** `for (const s of holey) console.log(s + sn)` with `sn: string | number = 5`, and `s + an` with `an: any = 5`.
  - Node: `NaN`
  - snapshot: `undefined5`
  - base: RangeError for the `for…of` form, so this is new there. For element reads (`strs[0] + sn`) the base already gave `undefined5`.
- D-221's N2 fix covers only statically numeric other sides.

**R1-F3 · Low (already failing in the base, but within N1's scope) · `case undefined` over a number tag is PTS1001.**
- **w02:** `function k(n: number) { switch (n) { case undefined: …; case 1: … } }` gives `PTS1001: converting undefined to number` in both builds. Node prints `one d`.
- **w03:** with a loose number local (`const [a, b] = one` where `one: number[]`, or `const c = one[3]`) and `case undefined`, both builds fail. The snapshot adds `converting number | undefined to number` at the switch.
- D-221 says N1 is fixed generally; `switch_case_undefined.pts` covers only string tags.
- **Where:** `caseMatch` (`lower.go:771`) leads into `operation`'s numeric conversion.

**R1-F4 · Low (already wrong in the base; C111 scope) · a defaulted `unknown`/`any` parameter never takes its default.**
- **Where:** `maybeUndefinedFor` (`lower.go:2084`) returns false when the parameter is `ir.Value`, so `callbackArguments` (`callbacks.go:550`) passes `Given=true`.
- **Repro r05:**
  - `holes.find((s: unknown = "D") => …)`: Node `['D','b','D']`, both builds `[undefined,'b',undefined]`.
  - The same happens for `findLast` with `any`, for `Array.from(holes, (s: unknown = "D") => s)`, and for `Array.from({length: 2}, (v = 5) => v)`: Node `[5, 5]`, both builds `[undefined, undefined]`.
  - Ordinary calls do it too: `g(u)` with `u: unknown = undefined` gives `undefined` where Node gives `5`.
- The comment in `find_shrinking_array_more.pts` claims `unknown`/`any` parameters take their default.

**R1-F5 · Low · `s += "x"` and `void s` on a loose local throw TypeError.**
- Node gives `"undefinedx"` and `undefined` (s01, s02).
- This contradicts D-221's own rule that a `+` with a string side concatenates "undefined".
- It already failed this way for D-090 locals; it now reaches every `for…of` variable (the base threw RangeError there).

**R1-F6 · Low · a parenthesized `Array.from` mapper loses its default.**
- **Where:** `isArrayFromMapper` (`closures.go:901`) doesn't skip parentheses.
- **Repro c07:** `Array.from(holey, ((s = "d") => s))` throws RangeError in both builds; Node gives `['d','q']`.
- The emitted signature is `pts_fn_arrow(pts_closure*, pts_string *s, double unused)`, so the default was dropped and the parameter list padded. The parenthesized `findIndex` callback works.

**R1-F7 · Note (cost) · string[] callback methods with an optional or defaulted element parameter are slower.**
- These methods now read each element as `string | undefined` and compute its Given flag; this is for match arrays' groups.
- `strs.forEach((s = "") => …)` over 1M strings ×10: 730.0M → 800.0M Ir (1.10×). This case is not in D-221's cost table.
- Unchanged: `findIndex((n?: number) => …)` over numbers (840.0M → 839.7M). Faster: `find((s) => …)` over strings (610.0M → 519.0M).

**R1-F8 · Note (already failing in the base, unrelated) · `I & string` narrowing fails to compile.**
- A lying `v is string` predicate on an interface-typed local is PTS9001 in both builds (m16, m17); in the snapshot it's `no member named 'utf16_len'`.

## Test quality
- The `// expect:` blocks of every non-skipped, non-no-diff new test match Node (`node_vs_expect.py`). The no-diff and skipped tests are justified in their headers. The `race_arrays.pts` edit (skip undefined items) is sound.
- **Gaps:** nothing pins R1-F1 (a lying predicate or a stale tuple narrowing on a loose local), R1-F3 (number tags), R1-F2 (a union or `any` other side), or R1-F4 (`unknown`/`any` defaults).

## Checked and fine
- **Writes into loose locals:** every write to a loose local's union is checked. I tried `any`, `unknown as`, closures, destructuring from `any`, a racing write through a function from `parallel` tasks (direct writes are PTS3001), and a `Box<string>` arriving through `any`.
- **Other safety paths:** captured locals before initialization give ReferenceError; class narrowings stay checked; `parallel` racing reads don't crash.
- **Generic instances:** all match Node, including number, string, class, union and integer instances (exact i64/u64).
- **`for…of` at holes:** about 40 use forms and 15 element kinds match Node, as do the ways of reaching the array (`!`, `as`, a field, a getter, `?? []`, `values()`, `slice`, spread, readonly).
- **find and its kin over shortened arrays:** strings, numbers, booleans, objects and i32/i64/u8 arrays all match Node. `find!` gives TypeError, and inferred parameters give RangeError, as designed.
- **`Array.from` mapper:** growing, shrinking and `shift` all match Node.
- **Callbacks with defaults:** I tried every callback method, tuples, readonly arrays, typed arrays, `Array.from` over sets, maps, generators, strings and `{length}`, `.par`, dynamic and `any[]` receivers, generic, async and generator callbacks, and `reduce`'s overloads. I found no shape mismatch.
- **emit-c sweep** of 1,062 run tests plus 329 other programs (base vs snapshot): no new PTS1001 or PTS9001; one skipped test (`narrowed_array_aliases`) now compiles further.
- **`f?.()`:** works on `for…of` variables, element reads and pattern names.
- **D-221's cost numbers reproduced:** strings 980.0M → 1021.0M (1.04×), interfaces 1440.0M → 1600.0M (1.11×), generic number instance 1000.0M → 1160.0M (1.16×). D-221's HEAD figures come from the reproducer's run and sit within about 10M of the parent's own HEAD run.

Probe archive: `/tmp/h11b/h11b-review1-probes.tar.gz`. Probe files: `/tmp/h11b/review1/{m,s,c,r,f,w,a}/` (`.pts` sources with `.out` captures). Cost results: `/tmp/h11b/review1/cost/results.txt` and `/tmp/h11b/review1/cost/d221/results.txt`.

## Round 2 (snapshot 3 = `594341ee3`; [archive](h11b-review2-probes.tar.gz))

**Verdict: NOT READY.** The round-1 fixes work, but the R1-F4 fix introduced a Medium regression: arguments are now evaluated before the receiver, and in a chain the later call's arguments run first. Every claim below comes from a command I ran on snapshot 3, snapshot 2, the base and Node. Probes are archived at `/tmp/h11b/h11b-review2-probes.tar.gz`; the marker `/tmp/h11b/review2.done` is written. Snapshot 3 is untouched.

## Findings

**R2-F1 · Medium · the argument order regressed against snapshot 2 and the base for `unknown`, `any` and `T | undefined` optional parameters. The same bug already existed for `p?: T` parameters.**
- **Where:** `inTemporaries` and `lastMaybePassed` (`lower.go:2048-2083`, used at 2249) bind an argument's temporary before the receiver or callee is evaluated. R1-F4's `maybeUndefined` now sends more calls through this path.
- **Emitted C (snapshot 3):** `({ pts_value arg_3 = pts_fn_s("2"…); ({ … arg_4 = pts_fn_s("1"…); … pts_fn_hold() …`
- **Repro d11:**
  ```ts
  const log: string[] = [];
  function s(tag: string, x: string | undefined): string | undefined { log.push(tag); return x; }
  class Holder { items: string[] = []; addN(x: string | undefined = "+"): Holder { this.items.push(String(x)); return this; } }
  const h = new Holder();
  function hold(): Holder { log.push("h"); return h; }
  hold().addN(s("1", undefined)).addN(s("2", "b"));
  console.log(h.items, log.join(""));
  ```
  - Node, base and snapshot 2: `[ '+', 'b' ] h12`
  - snapshot 3: `[ '+', 'b' ] 21h`
- For `addN` the values were already right on snapshot 2, because the callee applies a union parameter's default itself. The reorder buys nothing there.
- With `addU(x: unknown = "-")` the value is now fixed, but the order is still `21h`.
- **Single call (d08):** `recv("r").unk(v("1", undefined))`. Node, base and snapshot 2 give `r1`; snapshot 3 gives `1r`.
- **Callee computed by a call (d09):** `pickUnknown()(v(…))`. Node gives `c1`; snapshot 3 gives `1c`.
- **Already wrong on all three CLIs:** `recv().str(s(…))` with `str(a?: string)` gives `1r`, and the chained `add(x?: string)` gives `21h`.
- Short-circuits are not affected (d10): `&&`, `?:`, `?.` and a `while` condition evaluate nothing, as in Node.
- **Fix options:** bind the receiver or callee before the argument temporaries, which also fixes the existing `p?: T` cases. Alternatively, keep the old rule where the callee already handles undefined (`T | undefined` with a default) and apply `unknown`/`any` defaults in the callee.

**R2-F2 · Low · `+` still gives two wrong values. Element reads and pattern names already did this on the base; `for…of` variables are newly exposed (the base threw RangeError there).**
- **Where:** `concatenatesAsString` counts `{}` (TypeFlagsObject) as "always an object", although a `{}` can hold a number. It also trusts the other side's string type when that side may itself be missing.
- **Repro y03** (Node gives `NaN` for every line):

  | Expression | Snapshot 3 |
  |---|---|
  | `words[3] + empty`, with `const empty: {} = 5` | `undefined5` |
  | `for (const s of holey) s + empty` | `undefined5` |
  | `words[3] + words[4]` | `undefinedundefined` |
  | `s + s` | `undefinedundefined` |
  | `q + r` (pattern names) | `undefinedundefined` |
  | `s += s` (y02) | `undefinedundefined` |

**R2-F3 · Low (already wrong on all three CLIs) · a closure's default is lost when its contextual parameter is `unknown` or `any`.**
- `const f: (x: unknown) => unknown = (x = "D") => x; f(undefined)` gives `undefined` on all three CLIs; Node gives `D` (d04).
- `bindAlwaysPassed` drops the default here. D-221's sentence "a default takes undefined arguments, whatever the parameter's type" overclaims.
- The `T | undefined` context and function adapters (d05) are fine.

**R2-F4 · Note (by design under the updated D-221) · some uses that snapshot 2 got right now throw.**
- `s + an`, with `an: any` holding `"q"`, now throws TypeError where Node and snapshot 2 give `undefinedq` (y01).
- `s += anyStr` and `s += sn` (with `sn: string | number`) also throw TypeError (y02).

**R2-F5 · Note (already failing on all three CLIs) · two PTS9001 cases came up along the way.**
- A lying predicate narrowing an interface to an array (n04).
- A lying predicate narrowing a loose `HasLen | number` local to a tuple, in the element, `for…of` and pattern forms (n06). The ordinary-local form compiles and throws TypeError.

**R2-F6 · Note (cost) · R1-F1's checked reads cost a little on `for…of` over union arrays narrowed at each use.**
- `(string | number)[]` with `typeof`: 1080.0M on the base → 1105.0M on snapshot 2 → 1125.0M on snapshot 3 (1.04× the base).
- `(Sq | Ci)[]` with a discriminant: 1760.0M → 1838.5M (1.045×; the same on snapshots 2 and 3).
- D-221's cost table doesn't cover either case. D-221's own probes are unchanged on snapshot 3: strings 1020.0M, interfaces 1600.0M, tested 1040.0M.

**R2-F7 · Note (a suspicion, not reproduced) · `maybePassed` reads a shared module variable up to three times.**
- It reads it once for the undefined test, once for the cast and once for the Given flag.
- A write from another thread between the test and the cast would pass `(pts_string *)0x0a`. SPEC §4.1 requires reading a slot once before checking it.
- The q01 probe (three tasks toggling a global) didn't crash in 6 runs, but its main loop never saw a change, so the race was never exercised. This was already the case for `p?: T` parameters.

## Fixes verified
- **R1-F1:**
  - The round-1 crash probes m12, m13, m20 and m26, and the already-crashing m21 and m23, now throw TypeError.
  - n01 (stale narrowings to an interface, an array, a function type, a union of shapes, a tuple pattern, a `for…of` array): all TypeError; snapshot 2 crashed.
  - n02 (lying predicates to the same kinds): all TypeError; snapshot 2 and the base crashed.
  - n03 and n07 (lying predicates to types outside the union): checked.
  - m07 and m15 still crash, as recorded under C321.
- **R1-F2:** the union and `any` sides throw instead of giving wrong values. Branded strings, string enums and template-literal types match Node.
- **R1-F3:** w02 and w03 match Node. x01–x04 match Node for union, `any`, enum, literal, object, boolean, integer and i64 tags, and for fallthrough with `default` in the middle. The C for non-loose union switches is identical to snapshot 2.
- **R1-F4:**
  - r05, d01, d05 (including the adapter for an `unknown` parameter) and d06 (spread, generic, `?.()` and constructor calls) match Node.
  - i64 values stay exact through `maybePassed` (d02).
  - Hot-call costs are unchanged: 1114.3M → 1113.3M, 1126.7M → 1126.7M, 1013.3M → 1013.3M.
- **R1-F5:** `+=` with a string, an object, inside a closure and on a pattern name, and every `void` form, match Node.
- **R1-F6:** double parentheses, function expressions, `satisfies` and `{ length }` match Node. An `as` cast to a required-parameter type keeps D-085's RangeError, by design.
- **Replay of all 55 round-1 probes:** nothing that matched Node on snapshot 2 or the base differs now. The 10 outputs that changed against snapshot 2 are all improvements or the TypeErrors D-221 intends.
- **emit-c sweeps** (1,062 run tests plus 329 other programs, snapshot 2 against snapshot 3): no new diagnostics. All 58 changed C files are explained by checked narrowed reads, `maybePassed` at union or `any` arguments, `+=`/`void`, and reordered check tables and adapters.
- The new tests' `// expect:` blocks match Node, and their no-diff markers are justified.

## Round 3 (snapshot 5 = `873ef9448`; [archive](h11b-review3-probes.tar.gz))

**Verdict: READY.** Both round-2 fixes hold and nothing regressed against the base. Two Low findings and two Notes remain. One Low finding is a narrow regression against snapshot 3; it throws an error rather than giving a wrong value or crashing. Every claim below comes from a command I ran on snapshot 5, snapshot 3, the base and Node. Probes are archived at `/tmp/h11b/h11b-review3-probes.tar.gz` and the marker `/tmp/h11b/review3.done` is written. I made no changes to the snapshot.

## Findings

**R3-F1 · Low · `s += <a value that may be missing>` throws again for a loose `s` that is undefined. This regressed against snapshot 3; the base also threw.**
- **Where:** `compoundAssignment` (`lower.go`, the `+=` string case) now asks `concatenatesWithUndefined(expr.Right)`. That is false when the right side is itself an element read or a loose local, so `s` is read as a string and throws. Unlike `concatenation`, the `+=` path has no `ValueAdd` branch for the case where both sides may be missing.
- **Repro g02** (`const w = ["a"]`, with `s` or `v` undefined):

  | Form | Node | snapshot 3 | snapshot 5 | base |
  |---|---|---|---|---|
  | `v += w[0]`, element-read local | `undefineda` | `undefineda` | TypeError | TypeError |
  | `q += w[0]`, pattern name | `undefineda` | `undefineda` | TypeError | TypeError |
  | `s += w[0]`, `for…of` variable | `undefineda` | `undefineda` | TypeError | RangeError |
  | `v = v + w[0]` | `undefineda` | `undefineda` | `undefineda` | `undefineda` |

- The C for `run/array_pattern_loose_concat.pts`'s `w1 += w2` changed the same way. That test still passes, because `w1` is present there.
- **Fix:** in `+=`, use the same `ValueAdd` branch as `+` when both sides may be missing.

**R3-F2 · Low (cost) · `+` of two element reads or loose locals now goes through `pts_value_add`, even when both values are present.**
- Snapshot 3 used `pts_string_concat(pts_value_to_string(a), pts_value_to_string(b))`; snapshot 5 emits `pts_value_as_string(pts_value_add(a, b))`. That costs about 48 more instructions per `+` (callgrind, loop only, 1M ×10):
  - `strs[i] + strs[i + 1]`: base 4732.9M, snapshot 3 4676.2M, snapshot 5 5162.9M (1.10× snapshot 3)
  - `text[i] + text[i + 1] === "ab"`: base 4136.7M, snapshot 3 4140.9M, snapshot 5 4624.9M (1.12×)
- Tokenizers and `parts[0] + parts[1]` are common uses, and D-221's cost table doesn't include this case.
- **Fix:** concatenate directly when neither side is undefined, and use `ValueAdd` only otherwise.

**R3-F3 · Note · emit-c output for one corpus program is nondeterministic, on the base too; this is not H11b's doing.**
- `pts/tests/corpus/functional/transducers.pts` sometimes gets an extra order type and `pts_object_take_order` for `{ ...r, value }`: 1 of 12 emits on snapshot 5, 1 of 8 on the base (`t/determinism.txt` in the archive).
- Program output was the same in my run, but printed key order could flake. This is worth a known-issue entry.

**R3-F4 · Note · two small text issues.**
- When both sides are missing, the TypeError reads `Expected string, got number`; mentioning NaN would be clearer.
- One header line in `concatenation_missing_values.pts` runs past the file's wrap width.

## Fixes verified
- **R2-F1 (argument order):**
  - d07 and d11 match Node: the chain gives `h12` again, and the values are right.
  - Only the `p?: string` cases already wrong on the base still differ (d08, d09): C323, pinned by the skipped `optional_argument_receiver_order.pts`.
  - The `reject(failure, true)` calls are back to the base's C.
- **Defaults of `unknown` and `any` parameters, now applied in the callee (`parameterDefault`):** e01 matches Node across every kind of function. That covers functions with default-evaluation counts, `null` passing through without taking the default, constructors including `super(undefined)`, methods, static methods, field arrows, closures, function expressions, generics, async functions, generators, object and array parameter patterns, a default referring to an earlier parameter, a dynamic call, `Array.from` holes and `{ length }`. The base gets most of these wrong.
  - e02: a generic `x: T = d` instantiated with `unknown` matches Node.
  - C324 (a closure shaped by an `unknown` context) is unchanged, as recorded.
- **R2-F2 (concatenation):**
  - When both sides are missing, the result is TypeError where Node gives NaN.
  - When one side is missing, the result is `aundefined` for elements of arrays, strings, records and typed arrays.
  - A `{}` side is read as a string first (D-221's checked read) instead of concatenating `undefined5`.
  - g01 and g03 match Node for templates, optional chains, string-literal unions, `+=` into elements, and mixed present pairs (numbers, booleans, `toString`, Dates, arrays, objects, null elements).
  - `valueOf + string` is now right (`3a`), where the base and snapshot 3 gave `Va`.
  - i64 and u64 stay exact (g05). An interface can't hold a primitive in pts (g04, PTS1001), so treating interface sides as objects is safe.
- **Replay of all 85 earlier probes:** nothing that matched Node on snapshot 3 or the base differs on snapshot 5. m07 and m15 still crash (C321, as recorded).
- **emit-c sweeps** (1,062 run tests plus 329 other programs, snapshot 3 against snapshot 5): no new diagnostics. The 18 + 14 changed C files are all explained by the `maybePassed` revert, the callee-side undefined test for `unknown`/`any` defaults, `ValueAdd` for two possibly-missing sides, the checked `+=` read (R3-F1), and the R3-F3 nondeterminism.
- **Costs unchanged:** D-221's strings probe 1020.0M, round 2's `typeof`-narrowed union `for…of` 1125.0M.
- **New tests:** `default_arguments_evaluation_order.pts` matches Node; the skipped test's expect block is Node's output; the no-diff updates to `concatenation_missing_values.pts` are justified.

## Round 4 (snapshot 6 = `da004a459`; [archive](h11b-review4-probes.tar.gz))

**Verdict: READY.** Both round-3 fixes hold. The one remaining finding is Low: the new fast path brings back, for element pairs, a valueOf bug the base already had. The parent's suite on snapshot 6 finished with 0 failures and 5,918 passes. Probes are archived at `/tmp/h11b/h11b-review4-probes.tar.gz` and the marker `/tmp/h11b/review4.done` is written. I made no changes to the snapshot.

## Findings

**R4-F1 · Low · when both sides may be missing, the fast path calls `toString` on an object whose class has `valueOf`. Snapshot 5 got this right; snapshot 3 and the base got it wrong the same way.**
- **Where:** `missingConcatenation` (`lower.go`) concatenates `pts_value_to_string(left)` and `pts_value_to_string(right)` whenever neither side is undefined. JS's `+` calls ToPrimitive, which tries `valueOf` first, and snapshot 5's `ValueAdd` did that.
- **Repro h02:**
  ```ts
  class Val { valueOf(): number { return 3; } toString(): string { return "V"; } }
  const vals = [new Val()];
  const strs = ["a"];
  console.log(vals[0] + strs[0], strs[0] + vals[0]);
  ```
  - Node and snapshot 5: `3a a3`
  - snapshot 6, snapshot 3 and the base: `Va aV`
  - The same holds when one side is a loose local: `const [v] = vals; v + strs[0]`.
- **The wider bug that already existed:** a `valueOf` class read where any value goes uses `toString` on all three CLIs. `vals[0] + "s"` gives `Vs` where Node gives `3s`, and `s += vals[0]` with `s` present gives `pV` where Node gives `p3`.
- **Fix:** keep the fast path for primitives and take `ValueAdd` when either side's type may be an object. Alternatively, record it with the existing `valueOf` gap.

## Fixes verified
- **R3-F1 (`+=` of a loose local whose right side may be missing):** g02 now matches Node. That covers the element-read local, the pattern name and the `for…of` variable (`undefineda`).
  - h01 also matches Node for `s += rec["k"]; s += rec["missing"]` (`undefinedKundefined`), for `s += t[1]; s += t[7]` on a string (`undefinedzundefined`), and for a present `s` (`paundefined`).
  - The left side is read first, before the right side runs: a right side that assigns `s` still gives `undefineda`, as in Node.
  - `s += f64[0]` (a number) throws TypeError where Node gives NaN, which is D-221's intended behaviour.
- **R3-F2 (cost):** the fast path is cheap again (callgrind, loop only, 1M ×10):

  | Probe | base | snapshot 3 | snapshot 5 | snapshot 6 |
  |---|---|---|---|---|
  | `strs[i] + strs[i + 1]` | 4732.9M | 4676.2M | 5162.9M | 4783.5M (1.01× base) |
  | `text[i] + text[i + 1]` | 4136.7M | 4140.9M | 4624.9M | 4255.0M (1.03× base) |

- **Evaluation order and single evaluation (h01, matches Node):**
  - `w[i++] + w[i++]` gives `ab 2`, and `cundefined 4` when the second is missing.
  - Calls in both indices run left then right (`LR`). The getter receivers `box.items[0] + box.items[9]` run once each (`get,get`). Record keys from calls run `LR`.
  - A chain of three string indexes with `i++` gives `xyundefined 3`.
  - In the C, `left` and `right` are each bound once, in order.
- **Replay of all 92 earlier probes:** nothing that matched Node on snapshot 5 or the base differs, apart from R4-F1 (g03's `valueOf` line). Two other outputs changed from snapshot 5 and both now match Node: g01's loose `+=` of an element and g02.
  - y02's `s += s` still throws TypeError (Node gives NaN); only the message changed.
  - m07 and m15 still crash (C321, as recorded).
- **emit-c sweep** of the 1,062 run tests, snapshot 5 against snapshot 6: only 4 C files changed (`array_pattern_loose_concat`, `builtin_property_patterns`, `evaluation_order`, `string_indexing`), all at concatenations, with no new diagnostics.
- **Tests:** the extended `loose_locals_concatenation.pts` matches Node, and `concatenation_missing_values.pts`'s rewrapped header and no-diff marker are fine.

## Round 5 (snapshot 7 = `288ba50be`; [archive](h11b-review5-probes.tar.gz))

**Verdict: READY.** R4-F1 is fixed, nothing else changed against snapshot 6, and I found no new problems. The parent's suite on snapshot 7 finished with 0 failures and 5,918 passes. Probes are archived at `/tmp/h11b/h11b-review5-probes.tar.gz` and the marker `/tmp/h11b/review5.done` is written. I made no changes to the snapshot.

## Findings
None.

## R4-F1 verified
- **h02 now matches Node:**
  - `vals[0] + strs[0]` and `strs[0] + vals[0]` give `3a a3` (snapshot 6 and the base gave `Va aV`).
  - A pattern name next to an element gives `3a`.
  - `s += vals[0]` with `s` present gives `p3` (snapshot 6 and the base gave `pV`).
- **New probe k01** checks string-like and non-string-like kinds, with values present and missing. Snapshot 7 matches Node in every case except two, where JS gives NaN and pts throws TypeError as designed (`s += mixed[0]` with `s` undefined, and `lits[5] + tpls[5]`):
  - unions of strings and a class with `valueOf`: `3a a3`
  - string enums: `xa xundefined ax`
  - branded strings: `i1a i1undefined`
  - template-literal types: `t-1a t-1p undefinedp`
  - string-literal unions: `pa pundefined`
  - loose `+=` of a `valueOf` union, an enum (`undefinedxundefined`) and a branded string (`bi1`)
- **The gap from round 4 that the base already had is unchanged:** a `valueOf` class next to a side that can't be missing, such as `vals[0] + "s"`, still gives `Vs` where Node gives `3s`. The base and snapshot 6 give the same.

## Nothing else changed
- **Replay of all 94 earlier probes** on snapshot 7, snapshot 6, the base and Node: nothing that matched Node on snapshot 6 or the base differs now. Only two outputs changed from snapshot 6, and both now match Node: g03's `valueOf` line and h02. m07 and m15 still crash (C321, as recorded).
- **emit-c sweep** of the 1,062 run tests: no C file differs from snapshot 6.
- **Cost is the same as round 4** (callgrind, loop only, 1M ×10): `strs[i] + strs[i + 1]` 4783.5M, `text[i] + text[i + 1]` 4255.0M (1.01× and 1.03× the base).
- `loose_locals_concatenation.pts` with its new `valueOf` case matches Node.
