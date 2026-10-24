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
