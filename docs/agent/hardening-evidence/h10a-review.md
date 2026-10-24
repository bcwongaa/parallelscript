# H10a review reports

Child: `reviewer` (claude-opus-5-5, xhigh), read-only, resumed between rounds. Each report is the child's final message, saved here by the parent unchanged (children can't write report files); probes in `h10a-review<N>-probes.tar.gz`.

## Round 1: `f54f8b5c1`

NOT READY

# H10a review, round 1: commit `f54f8b5c1` (against base `972e29adc`)

I found seven defects. Five make things worse than the base: a program that ran as Node now throws, crashes the compiler, or no longer compiles. One silent-wrong-output case is still there, and three are unsound-inference or coverage gaps.

Every finding below is verified: I ran each repro with `/tmp/h10/pts-base`, `/tmp/h10/pts-fix1` (with `PTS_HOME` set to the repo) and Node 24 `--experimental-transform-types` on a `.mts` copy.

- **Probes:** in `/tmp/h10/review1/p/`. `/tmp/h10/review1/bin/cmp.sh FILE.pts` reprints all three outputs. Archive: `/tmp/h10/review1-probes.tar.gz`.
- **Line numbers:** they refer to the code at `f54f8b5c1`. The working tree has your uncommitted change to `instanceof.go`.

## Broader checks I ran
- **Matrix:** I re-ran the reproducer's 777 cases on pts-fix1 (`/tmp/h10/review1/matrix/fix1.jsonl`). Result: 604 OK, 120 PTS1001, 53 TYPEERR, and none worse or wrong by class compared with `head.jsonl`. The commit's numbers hold.
  - 49 cases moved from TYPEERR to PTS1001. The runtime failure became a compile-time one for every flows-out use (`const_copy`, `return_inferred`, `array_literal`, `generic_call`, `par_map`). See F3.
- **emit-c sweep:** I compiled all 328 run/corpus/docs programs that declare a generic class with both binaries (`/tmp/h10/review1/sweep/result.txt`). The C changes only for the `generic_instanceof_*` tests and `lru_cache_map.pts`, so nothing else in the suite moved.
- **LRU corpus:** the fix prints exactly what Node prints.

## Findings

### R1-F1 (High, regression): the inference cache ignores the instance being lowered
- **Where:** `pts/lower/instanceof.go:20-23, 94-98`. `instanceofKey{node, t}` doesn't include the declared type. In every instance of a generic function or class, `t` is the same `MapLRU<any, any>`, so the first instance's inference is reused for all the others.
- **Repro 1, a regression:** `p/p47b_cache_key_any_instance.pts`. `show<string, number>` is only reached behind a false flag, and `show<any, any>(new MapLRU<any, any>(...))` is what runs.
  - Node: `b:2`. Base: `b:2`.
  - Fix: `Uncaught TypeError: Expected MapLRU<string, number>, got MapLRU<any, any>`, exit 1.
- **Repro 2:** `p/p01_generic_fn_instances.pts` (your new test). The second call fails on the fix with `Expected MapLRU<string, number>, got MapLRU<number, string>`.
- **Repro 3:** `p/p35_class_method_two_instances.pts`, where `MapLRU<K, V>`'s own method `same(o: Cache<K, V>)` runs in two class instances.
  - Node: `string:true number:true` / `number:true string:false`.
  - Fix: the first line, then `TypeError: Expected MapLRU<string, number>, got MapLRU<number, string>`.
  - Base: PTS1001 `using o from outside the function`.
- Your working-tree change, adding `declared` to the key, should cover all three. Please add p35 (class methods) and p47b (the any instance) as tests too; the untracked test only covers generic functions.

### R1-F7 (High, regression): a union whose other member holds the value as a dynamic value is inferred from one member alone
- **Where:** `internal/checker/pts.go`, `PtsInferInstanceTypeArguments`. A union member that implies no instance is skipped (`case inferred == nil:`). But `object`, `{}` or a plain base class can hold any `MapLRU`. These unions are dynamic values on the base, where the members typed `any` were read correctly.
- **Repro 1:** `p/p31_union_object_type_confusion.pts`, with `c: Cache<string, number> | object` holding `MapLRU<number, string>`.
  - Node and base: `key 12345.5 len undefined` / `key ok len 2`.
  - Fix: `Uncaught TypeError: Expected string, got number`, exit 1.
- **Repro 2:** `p/p36_union_empty_object.pts` (`| {}` and `| object | null`).
  - Node and base: `empty true b` / `null 3 4`.
  - Fix: `threw Expected string, got boolean` / `threw Expected string, got number`.
- **Repro 3:** `p/p30_union_object_member.pts` has the same shape.
- The reads are checked (`pts_value_as_string` checks), so this is a TypeError, not memory unsafety.
- **Fix idea:** treat a member that implies no instance but can hold C as "no instance" for the whole union. Also never infer for a reference whose IR value is dynamic.

### R1-F4 (High, regression, compiler crash): tuple member elements
- **What happens:** reading `c.pair[0]` or `c.pair[1]`, where the member is `pair: [K, V]`, on an inferred narrowing crashes the compiler with PTS9001.
- **Repro:** `p/p11_tuple_member_crash.pts`.
  - Node: `2`. Base: compiles, then `TypeError: Expected MapLRU<any, any>…` at run time.
  - Fix: `error PTS9001: internal compiler error while emitting C: … nil pointer dereference`, exit 3.
- **Stack:** `cgen.basePath` (`classes.go:274`) ← `fieldPath` ← `fieldPlace` ← `checkedFieldRead`. The read uses the tuple class tsc gives the element (`[any, any]`) on an object of the instance's tuple class (`[string, number]`).
- `const p = c.pair` instead gives PTS1001 `using a [string, number] as a [value, value]` (`p/p12_tuple_member_variants.pts`).

### R1-F5 (Medium-High, regression): private getters read through a dynamic narrowing now throw
- **Where:** `familyPrivateField` (`instanceof.go:171`) catches every `#name` read. `cgen.familyField` only looks for fields, so a private accessor falls through to the "did not declare it" throw.
- **Repro:** `p/p19_private_static.pts`, with `static getterUnknown(o: unknown) { return o instanceof Box ? o.#doubled : "no" }`.
  - Node: `22`. Base: `22` (the dynamic lookup finds the getter).
  - Fix: `threw Cannot read private member #doubled from an object whose class did not declare it`. The class does declare it.
- The `object` source (`p/p18_private_getter.pts`) gives the same error.

### R1-F2 (Medium, regression): a `Box<any>` object held where no instance is inferred loses private writes and private method calls
- **Why:** on the base, the checked downcast to `Box<any>` succeeded for any-instance objects (`new Box(JSON.parse("5"))`), so every typed use worked. Now the reference is dynamic, and D-218's acknowledged C274 gaps apply to objects that used to work.
- **Repro 1:** `p/p05_any_instance_object_source.pts` (`assign(o: object) { if (o instanceof Box) o.#v = this.#v }`).
  - Node and base: `1` / `1 renamed`.
  - Fix: `Uncaught TypeError: Cannot add property #v, object is not extensible`, which isn't V8's message either.
- **Repro 2:** `p/p04_any_instance_private_method.pts`. Node and base: `tick x`. Fix: `TypeError: s.#tick is not a function`.
- **Repro 3:** `p/p03_any_instance_in_base.pts` covers the same failures.

### R1-F3 (Medium, regression): unreached narrowed branches no longer compile
- **Why:** an inferred narrowing makes the branch typed code. Many uses that the base compiled, failing only at run time if the branch ran, are now PTS1001 at compile time. A program whose branch never sees a `MapLRU` ran as Node on the base and no longer builds. The matrix can't see this because every case enters the branch.
- **Repro 1:** `p/p39_unreached_const_copy.pts`. Node and base: `fixed 1`. Fix: `PTS1001: using a MapLRU as a MapLRU`.
- **Repro 2:** `p/p08_unreached_branch.pts`. Node and base: `fixed`. Fix: `PTS1001: using a string[] as a value[] …` and `[string, number][] as [value, value][]`.
- **Other uses that are now compile-time PTS1001**, verified:
  - `const s = c.store` for a `Map<K, V>` member (p46);
  - `const p = c.pair` (p12);
  - a fluent `this` return, `c.self().key` (p09);
  - a callback returning an object literal, `c.update("x", o => ({ a: … }))` (p28);
  - every H10b flows-out form, which accounts for the matrix's 49 TYPEERR→PTS1001 moves.
- D-218 mentions the `any[]` case as "the usual PTS1001", but doesn't account for what this does to unreached code.

### R1-F6 (High, not a regression; C274's fix is incomplete): `Number(o.#v)`, `String(o.#v)`, templates and `typeof` still read `undefined`
- **Where:** `FamilyField` is only reached through `propertyAccess`. `valueExpression`'s dynamic case (`lower.go:1602-1603`, `return l.dynamicRead(inner)`) skips it, so the C emits `pts_dynamic_get(o, "#v")`.
- **Repro 1:** `p/p27_private_read_uses.pts`.
  - Node: `number 7`, `string 7`, `tmpl 7 number`.
  - Fix, and base too: `number NaN`, `string undefined`, `tmpl undefined undefined`.
  - Only the `+`, `typed` and comparison forms are right.
- **Repro 2:** `p/p26_private_in_callback.pts`, `objs.map(o => o instanceof Box ? Number(o.#v) : 0)`. Node: `[ 1, 2, 0 ]`. Fix: `[ NaN, NaN, 0 ]`.
- **Repro 3:** `p/p25_par_private.pts` gives `NaN` at threads 1, 2 and 4, against Node's `817`.

### R1-F8 (Medium, not a regression): the inference can be unsound
- **Why:** "assignable to the declared type" doesn't make the instance unique.
- **Repro:** `p/p33_unsound_inference.pts`.
  - A phantom parameter (`Tagged<T> implements Named<T>`, where `Named` doesn't use `T`): `Named<string>` holding a `Tagged<number>` gives `TypeError: Expected Tagged<string>, got Tagged<number>`.
  - A base class that absorbs the argument (`Weird<A> extends Bag<A | string>`): `Bag<number | string>` holding a `Weird<number | string>` gives `TypeError: Expected Weird<number>, got Weird<string | number>`.
  - Node runs both. The base was PTS1001 for this program.

### R1-F9 (Low-Medium, gap in H10a's stated scope): `S & C<any>` with `S = object` or `{}` still throws
- **Where:** `isNarrowedDynamic` (`exceptions.go:271`) still returns false for an intersection type, as the reproducer noted.
- **Repro:** `p/p14_typeparam_object_alone.pts`. Node: `lru true o 1`. Base and fix: `TypeError: Expected MapLRU<any, any>, got MapLRU<boolean, string>`.

### R1-F10 (Medium, gap): `declaredTypeOfReference` misses common references
- **Repro:** `p/p43_reference_gaps.pts`. Each of these still throws `Expected MapLRU<any, any>` on the fix:
  - `this.#cache`: a private name isn't found by its text through `GetTypeOfPropertyOfType`;
  - `rec.main` on a `Record<string, Cache<…>>`: no index-signature fallback, although `rec["main"]` has one.
- **Repro:** `p/p42_imported_reference.pts`. An imported const has an alias symbol, which the `Variable|Property` flag test rejects.
- A static property (`Wrapper.global`) works.

### R1-F11 (Low; H10b-adjacent, notes only)
- **Members that are themselves generic instances:**
  - `c.byKey.get("k")?.v` with `Map<K, Inner<V>>` gives `Expected Inner<any>, got Inner<number>` (p16).
  - `c.next?.val` gives PTS1001 (p21).
  - `c.next!.val` and `let n = c.next` throw `The value narrowed to MapLRU is not an instance of it`, a misleading message (p22).
- **Message quality:** in the generic-instance case the downcast TypeError in p01 has no `at show (…)` lines, while the base printed them.

## Checked and fine
- Explicit `Box<any>` declarations and unions holding one, under `instanceof`, a discriminant, `in`, a type predicate, `typeof`, `as` and truthiness (p32): identical on all three.
- Dead-branch intersections, `S & MapLRU<any, any>` for an unrelated twin class, a subclass, `number` and `Cache` (p13), now run as Node.
- Subclass objects through the inferred instance, including private reads (p40).
- The C276 fix: own-class instances in constructors, getters, static methods and generic methods, and destructured, default and rest parameters (p34b).
- The C273 dynamic array, Map and tuple members of `unknown` sources (p23, p37) now match Node, `.par` included at threads 1, 2 and 4 (p24).
- Optional calls (p38) and Promise and function members (p46b).
- Private family dispatch over generic and non-generic subclasses and an own-`#v` subclass (p17b).
- Thread safety: lowering has no goroutines (no `go func` in lower, driver or check), and `FamilyField`'s temporary lives in a C statement-expression like `familyInstanceOf`'s.
- Generated code: `familyField` grows linearly with the instance count per read; I found no compile-time blow-up.

## Test quality
- The 13 tests without an `// expect:` block are only checked by `TestRunMatchesNode`, which is fine since CI sets `PTS_REQUIRE_NODE`.
- The `expect` blocks of `par_callbacks` and `union_conflicting_instances` match Node's output for their sequential twins.
- `dynamic_member_copies` records a Node divergence on purpose (D-179).
- Missing coverage, which is why "none worse" missed F1–F7: generic class methods with two instances, any-instance objects, unions with `object`, tuple members, private getters, `Number`/`String`/template reads of `#f`, and unreached branches. The matrix has none of these forms.

## Round 2: `105354671`

NOT READY

# H10a review, round 2: commit `105354671` (against `f54f8b5c1`)

All ten round-1 fixes hold. But the new dynamic-member treatment silently loses writes in common code that the base rejected with a checked TypeError, and that blocks acceptance. I also found two smaller regressions, one questionable trade-off, and some performance and compile-time costs.

Everything below was run with `/tmp/h10/pts-fix2`, `pts-fix1` and `pts-base` (with `PTS_HOME` set) against Node on a `.mts` copy.
- **Probes:** `/tmp/h10/review2/p/q*.pts`. `/tmp/h10/review2/bin/cmp.sh FILE` reprints all outputs.
- **Archive:** `/tmp/h10/review2-probes.tar.gz`.

## Round-1 fixes: verified
- **Probe rerun:** I re-ran all 60 round-1 probes against Node on fix2 (`/tmp/h10/review2/rerun/`). Every one matches Node except p06, p15, p15b, p16, p17, p20, p21, p22, p29, p34, p41 and p45, which are exactly the pre-existing and H10b cases you listed.
  - F1: p01, p35, p47b.
  - F2: p03–p05.
  - F3: p08, p39, p46 (except R2-F2 below).
  - F4: p11, p12.
  - F5: p18, p19.
  - F6: p25 at threads 4, p26, p27.
  - F7: p30, p31, p36.
  - F8: p33.
  - F9: p14.
  - F10: p42, p43; also `other.#cache` of another instance's type arguments (q10).
- **Matrix:** I re-ran the 777 cases on fix2 (`review2/matrix/fix2.jsonl`). Result: 608 OK, none worse than the base or fix1, no WRONG.
  - From base: 45 cases moved PTS1001 → TYPEERR.
  - From fix1: 91 cases moved PTS1001 → TYPEERR.
  - The matrix never checks aliasing, which is why it misses R2-F1.
- **emit-c sweep, base vs fix2:** over 306 generic-class programs outside the `generic_instanceof_*` tests, the C changes only in `generic_narrowed_instances_{dynamic_arguments,lies}.pts`. Both just stop making an unused `Box<any>` class, an effect of the R1-F9 intersection change; their outputs are unchanged.
- **New tests:** I compared the `// expect:` blocks of all 28 new tests with Node's output, and every one matches.

## Findings

### R2-F1 (High, regression: a checked TypeError became silently wrong output): writes through member values are lost
- **What happens:** a member value that tsc types with `any` (an array, tuple, object type or Map) goes into an any-typed place and is converted by a copy (D-179). Every write through the copy is lost, with no error.
- **Places affected:** a `const`/`let` local, a function's inferred return, the array argument `forEach` passes, `for…of` element bindings, an `any[]` parameter, a field, or the result of a call fix2 makes dynamic.
- **When it started:** fix2 extends this to inferred references (interface and generic-base sources). Uninferred sources (`Shape`, `object`, `Error`) already had it in `f54f8b5c1`; I missed that in round 1. The base threw `TypeError: Expected MapLRU<any, any>` at the narrowing.
- **q12b:** `const p = c.pair; p[1] = 99`, `const meta = c.meta; meta.weight = 7`, `for (const e of c.entries) e.v += 1`, `const first = c.entries[0]; first.k = "renamed"`.
  - Node: `after [ 'k', 99 ] { label: 'k', weight: 7 } [ { k: 'renamed', v: 2 } ]`.
  - Base: TypeError.
  - fix2: `after [ 'k', 1 ] { label: 'k', weight: 1 } [ { k: 'k', v: 1 } ]`.
- **q08:** `const it = c.items; it.push(2)`, `const all = c.all(); all.push(3)`, `const idx = c.index; idx.set(…)`.
  - Node: `in true true 3 1` / `after [ 1, 2, 3 ] 1`.
  - fix1: PTS1001 `… where it may be changed or kept (a copy wouldn't show the changes…)`.
  - fix2: `in false false 1 0` / `after [ 1 ] 0`.
- **q15b:** `let list = c.items; list[0] = 9`, `c.items.forEach((x, i, arr) => (arr[i] = x * 2))`, and a keeper that stores `c.items` in an `any[]` field.
  - Node: `[ 18, 2, 6, 77 ] … [ 18, 2, 6, 77 ]`.
  - fix2: `[ 1, 1, 3 ] … [ 1, 1, 3, 77 ]`.
- **q22:** `itemsOf(m).push(2)`, where `itemsOf` returns `c.items` and its return type is inferred.
  - Node: `[ 1, 2 ]`. Base: TypeError. fix1: PTS1001. fix2: `[ 1 ]`.
- **q07:** `const got = c.addAll([1, 2]); got.push(3)`. Node: `1 true [ 1, 2, 3 ] 3`. fix2: `1 false [ 1, 2 ] 3`.
- **q09:** a member passed to a function that pushes onto its `any[]` parameter loses the push (`fillAny(c.items)`: Node `[ 5, 5, 42, 43 ]`, fix2 `[ 5, 5, 42 ]`).
- **q19:** the uninferred `Shape` source: fix1 and fix2 both print `after [ 1 ] { label: 'k', weight: 1 }`, against Node's `[ 1, 2 ] { … weight: 7 }`.
- **Why it's inconsistent:** the compiler already refuses this copy for a typed `number[]` going to `value[]`. q20 gets that PTS1001 on all three binaries, and so does `const { items } = c` on fix2. Only a dynamic value's conversion to `any[]`, shapes or Maps skips the guard.
- **Fix idea:** for these members, give the local or return the same dynamic value instead of converting it to tsc's `any[]` or shape (no copy). Or keep the PTS1001 where a copy would be kept or changed.
- **Note:** the `unknown`-source version is pre-existing on the base (q12c prints the same wrong output). D-179 accepts it and `generic_instanceof_dynamic_member_copies.pts` records it, but it isn't listed as a known gap.

### R2-F2 (Medium, regression for unreached code; what's left of R1-F3): `mentionsAny` misses mapped and index-signature types
- **Where:** `instanceof.go`, `mentionsAny`. It only looks at type arguments of references and the signatures and properties of anonymous types. So a `Record<string, V>` member (tsc: `Record<string, any>`) or a `{ [k: string]: V }` member stays typed, and binding it to a local is PTS1001 at compile time.
- **Repro:** q21, where the branch is never entered.
  - Node and base: `fixed 1`.
  - fix1 and fix2: `PTS1001: converting Record<string, number> to Record<string, value> is not supported yet`.
- q01 shows the same failure where the branch is reached.

### R2-F3 (Medium, regression against fix1; the base threw a TypeError): private methods called with a literal argument
- **Where:** `callsInstanceofMethodDynamically` turns `c.#apply((v) => v + 1)` and `c.#add([1, 2])` on an inferred reference into dynamic calls, and dynamic calls can't find private methods.
- **Repro:** q14.
  - Node and fix1: `2 2`.
  - fix2: `threw c.#apply is not a function`, which is also a misleading message.
  - Base: `Expected MapLRU<any, any>…`.
- **Fix:** skip `PrivateIdentifier` callees there.

### R2-F4 (Low-Medium, honesty trade-off): `classCast` now accepts conversions that can never succeed
- **Where:** `classes.go`, the new `classCast` case. It turns the narrowed reference into a checked downcast to *any* class `to`, not just to C's instances.
- **Repro:** q04, passing the reference where tsc structurally accepts an unrelated `Twin` class.
  - Base and fix1: PTS1001.
  - fix2 compiles, then throws `Expected Twin, got MapLRU<string, number>` every time it runs.
  - A no-member `Sub` subclass and `MapLRU<string, number | string>` now compile and throw unless the object really is one (`sub u` works).
- **Assessment:** for C<any, …> places (H10b's flows out) the trade-off is fine. Interface sources threw that same TypeError on the base, union sources were PTS1001 there, and the matrix shows nothing that ran as Node got worse. I'd restrict `to` to C's instances and their subclasses, and keep PTS1001 for conversions that can't succeed.

### R2-F5 (Low, acknowledged in D-218's known gaps): the "unique instance" rule doesn't hold for absorbed members
- **Repro:** q05. `Cache<string, number>` holding a `MapLRU<string, number | undefined>` throws `Expected MapLRU<string, number>, got MapLRU<string, number | undefined>`, where Node prints `optional m undefined`. The base threw too.
- This is the canonical `get(key: K): V | undefined` shape, so D-218's known-gap example (`get(): T | string`) could name it.
- Literal-union and enum keys work (q05).

### R2-F6 (Low, performance and compile time)
- **Hot loop (q02):** summing `c.items[i]` through the narrowing takes about 1.1 s on fix2, about 0.8 s on fix1, about 0.22 s through a typed parameter, and 64 ms in Node. The host was loaded, so read the ratios, not the absolute times.
- **Dynamic calls (q16):** 2M `c.update((o) => o + 1)` calls take about 280 ms on fix2, about 120 ms on fix1, and about 10 ms typed.
- **Compile time (q17b):** `mentionsAny` isn't memoized and walks anonymous types to depth 8 on every member read. 800 reads of a recursive anonymous-type member take 6.6 s to emit, against 1.85 s on the base, roughly 6 ms per read. Caching the answer per type would fix it.

## Checked and fine on fix2
- Dynamic calls keep `this`, defaults, rest parameters, generic methods, fluent `this` returns and callback fields. Ill-typed arguments throw a checked `Expected number[], got Array` (q03).
- Optional-chain dynamic calls (q11).
- Members declared with `any` in the class keep aliasing (q13).
- Aliasing writes made directly on a member, `c.items = shared` and `typeof` narrowing of a member (q06).
- Calls of private function-valued fields (q18).

## Test quality
- None of the 27 new tests checks aliasing after binding a member to a local or returning it, so R2-F1 passes them all.
- `generic_instanceof_unreached_branch.pts` and the `member_uses_*` tests bind `c.keys()` and `c.pairs()` to locals and only read lengths. They would pass with copies.

## Round 3: `5e5f838b0`

NOT READY

# H10a review, round 3: commit `5e5f838b0` (against `105354671`)

The round-2 fixes hold, and nothing outside the instanceof tests changed. But the new dynamic locals only fix one sink: the direct `const it = c.items; it.push(…)`. Writes are still lost silently through 16 other sinks, every one of which threw a checked TypeError on the base where I ran it on the base. And the dynamic-local cache ignores the generic instance, so one instance's answer is reused for another; with one call order that also loses writes silently.

All runs used `/tmp/h10/pts-fix3`, `pts-fix2` and `pts-base` (with `PTS_HOME` set) against Node on a `.mts` copy.
- **Probes:** `/tmp/h10/review3/p/r*.pts`. `/tmp/h10/review3/bin/cmp.sh FILE [versions]` reprints the outputs; programs with a `// threads:` line run at each count.
- **Archive:** `/tmp/h10/review3-probes.tar.gz`.

## What holds (verified)
- **Round-1 and round-2 probes on fix3** (`review3/rerun/`): everything matches Node except p06, p17, p20, p29, p34, p41, q03, q04, q05, q09, q15, q15b, q20 and q22, plus q02 and q16, which only differ because they print timings. That's exactly your list.
  - The round-2 fixes: q01 and q21 now run as Node (R2-F2); q14 prints `2 2` (R2-F3); q04 is PTS1001 `using a MapLRU as a Twin` (R2-F4).
- **Matrix (3 jobs, `review3/matrix/fix3.jsonl`):** 622 OK, none worse than the base, fix1 or fix2, no WRONG. 14 cases moved TYPEERR → OK since fix2.
- **emit-c sweep, base vs fix3:** over 306 generic-class programs outside the instanceof tests, only the same two benign files from round 2 (`generic_narrowed_instances_{dynamic_arguments,lies}`) and the LRU corpus program change.
- **Compile time** is back to the base's: q17b took 1.13 s against 1.11 s, and the four largest generic programs are within ±0.03 s.
- **New tests:** the `// expect:` blocks of all 18 run tests match Node's output, and the 3 error tests expect honest PTS1001s.
- **Dynamic locals work correctly** for every form I tried:
  - closures and nested function declarations, also at module scope (r07, r12);
  - across `await`, `yield` and async generators (r08);
  - reassigned with a typed `number[]`, which keeps aliasing (r07);
  - `let` in loops, and spreads, `Set`, `Float64Array.from`, `sort`, writes to `length`, a typed `number[]` parameter (r10);
  - `.par` reading them or mapping over them at threads 1, 2 and 4 (r09).

## Findings

### R3-F2 (High, regression: a checked TypeError became silently lost writes): every sink except the direct local still copies
- **Main repro:** `r15_sinks_each.pts`. Each line makes one write that JS makes to `c.items` itself. Node prints `true` on all 15 lines. fix3 prints `true` only for the control `const it = c.items; it.push(1)` and `false` for the other 14.
  - **Members going straight into a sink:**
    - `const a = flag ? c.items : []`;
    - `c.items ?? []`, `c.items || []`;
    - `c.items as any[]`, `c.items satisfies any[]`;
    - a default parameter `(a = c.items) => …`;
    - `a ||= c.items`;
    - `for (const { list } of c.groups) list.push(…)`.
  - **A dynamic local going on into a sink:** an `any[]` parameter, a field, an array-literal element, object-literal shorthand `{ it }`, an annotated `const t: any[] = it`, `flag ? it : []`.
- **More repros, all `false` or lost on fix3:**
  - `r16b_more_sinks.pts`: `yield c.items` from a generator, and a returned conditional.
  - `r04b_copy_sinks.pts`: Node `items [0,1,2,3,4,5,6,8]`, fix3 `items [ 0 ]`, base TypeError. It also covers `await c.items`.
  - `r05_dynamic_local_sinks.pts`: Node `[0,1,2,3,4,5,6]`, fix3 `[ 0 ]`, base TypeError.
  - `r07_dynamic_local_lifetimes.pts`: for…of destructuring and the generator conditional lose their writes.
  - `r11_uninferred_sinks.pts`: the same with a `Shape` source. Node `[ 0, 1, 2 ] [{"list":[0,3]}]`, fix3 `[ 0 ] [{"list":[0]}]`, base TypeError.
- **Root cause:** a dynamic value holding a typed array, Map or shape is converted to `value[]` or a shape by a copy (D-179). That is pre-existing for `any` values: `r06_any_value_sinks_baseline.pts`, which has no instanceof at all, loses its writes on the base too. But H10a routes values the base threw on into that copy. `copiesDynamicValue` and `isInstanceofDynamicLocal` recognise a fixed list of parent nodes, and anything else falls through to the copy.
- **Suggested fix:** make it hold at the conversion, not by listing syntax. When a Value that an instanceof narrowing produced (a member, an uninferred reference, a dynamic local, or anything derived from them) is converted to dynamic data or an object type, either keep it as a Value or report D-094's PTS1001.
- **Already handled honestly by fix3 (PTS1001):** spread arguments, object-literal properties, `ident(c.items)`, optional `any[]` parameters, `for (const it of [c.items])` (r13, r16).

### R3-F1 (Medium-High, silent wrong output depending on call order): `instanceofLocals` is keyed by symbol, not by instance
- **Where:** `instanceof.go`, `isInstanceofDynamicLocal`. Its answer depends on the instance being lowered (whether `x` is narrowed, inferred or typed, and on `typeOfSymbol` under `l.subst`), but it is cached in `l.instanceofLocals[symbol]`. This is the same bug class as R1-F1.
- **Repro 1, silent:** `r03_local_cache_silent.pts`. `grow<S>(x)` does `const it = x.items; it.push(9)`. `grow<Box<any>>` is lowered first (x typed, an `any[]` local that needs no copy), then `grow<Shape>` (x dynamic).
  - Node: `2 2 [ 0, 9 ] [ 2, 9 ]`.
  - fix3: `2 2 [ 0, 9 ] [ 2 ]`.
  - Base: TypeError.
- **Repro 1, control:** `r03b_local_cache_control.pts` makes the same calls in the other order, and fix3 then prints Node's output.
- **Repro 2, compile success depends on order:** `r02_local_cache_order.pts` (`grow<Shape>` first) runs as Node on fix3. `r02b_local_cache_order_reversed.pts` (`grow<Box<number>>` first) is PTS1001 on fix3.
- **Fix:** key the cache by symbol and substitution, or don't cache across instances.

### R3-F3 (Low, accepted trade-off): honest PTS1001s now reject some unreached branches the base compiled
- `copiesDynamicValue`'s PTS1001s are right compared with a silent copy. But `fillAny(c.items)`, `return c.items` with an inferred return type, spread arguments and object-literal properties now fail to compile even in branches that never run. The base compiled those and ran as Node there (R1-F3's class). Worth a line in D-218's known gaps.

### Notes (not blocking)
- R2-F5 (C281) and R2-F6's run-time cost (C282) are recorded as deferred.
- q03's checked TypeError for a deliberately ill-typed argument is D-070's by design.

## Test quality
- The new tests only bind members directly to locals. Nothing covers the R3-F2 sinks (conditional, `??`/`||`, `as`, default parameters, `||=`, `yield`, `await`, for…of destructuring, or onward flows of a dynamic local), or a generic function with two instances whose locals differ (R3-F1).
- `r15_sinks_each.pts`, `r03_local_cache_silent.pts` and `r03b_local_cache_control.pts` are ready-made RED tests with Node's output.

## Round 4: `77a63f2f6`

NOT READY

# H10a review, round 4: commit `77a63f2f6` (against `5e5f838b0`)

R3-F1 is fixed, and the guard catches most round-3 sinks. Two blockers remain:
- **Rejects code the base ran:** the new PTS1001 rejects programs with `unknown`, `catch` and `any` sources that compiled and printed what Node prints on the base and on fix3.
- **Still silently loses writes:** six conversion paths bypass the guard, where the base threw a checked TypeError.

All runs used `/tmp/h10/pts-fix4`, `pts-fix3` and `pts-base` (with `PTS_HOME` set) against Node on a `.mts` copy.
- **Probes:** `/tmp/h10/review4/p/s*.pts`. `/tmp/h10/review4/bin/cmp.sh FILE [versions]` reprints the outputs.
- **Archive:** `/tmp/h10/review4-probes.tar.gz`.

## What holds (verified)
- **Probe rerun:** I re-ran all round 1–3 probes on fix4 (`review4/rerun/`), and the results match your list.
  - R3-F1 is fixed: r03 now prints Node's output. r02 and r02b both get the same PTS1001 as the base, so the result no longer depends on call order.
  - Of r15's 14 sinks, 8 are now reported as PTS1001 (nullish, `as`, default parameter, `||=`, `any[]` parameter, field, array literal, annotated variable). Five now keep the object and run as Node (conditional, `||`, `satisfies`, for…of destructuring, local conditional, in `s07`). One still copies silently (shorthand, R4-F2).
- **Matrix (3 jobs, `review4/matrix/fix4.jsonl`):** 622 OK, identical to fix3 case by case, none worse, no WRONG.
- **emit-c sweep:** base vs fix4 over 488 programs (every generic-class or instanceof program outside the H10 tests).
  - Changed: `generic_statics.pts` and `generic_narrowed_instances_wider_first.pts`, both newly changed in this round, plus round 3's two.
  - All four only stop making an unused `Box<any>` class, because an `unknown` source narrowed to `Box<any>` now reads its members dynamically. Both new ones still print their expected output on fix4.
- **Paths the guard handles honestly (PTS1001):** setters, `super(…)` arguments, `map` callbacks returning the member, `any[]` parameters (s03, s04).
- **Paths that keep the object correctly:** `Object.assign` into the member, a typed callback parameter of a dynamic method call (s03b), and the destructuring forms `const [first] = …`, nested and renamed patterns, and `for (const [, list] of map)` (s06b).

## Findings

### R4-F1 (High, regression: programs that ran as Node on the base no longer compile): false-positive PTS1001s for `unknown`, `catch` and `any` sources
- **Where:** `copiesInstanceofDynamicValue` (`instanceof.go`, called from `convert`). Its source test, `holdsInstanceofDynamicValue` through `isMemberOfNarrowedInstance`, also covers storedDynamic sources (`unknown`, `any`, a `catch` binding) narrowed by instanceof. On the base those members were D-179 copies, and programs that only read them printed what Node prints.
- **s02_catch_generic_error.pts:** `catch (e) { if (e instanceof ApiError) return e.details; }`, from a function declared `: any[]`.
  - Node, base and fix3: `[] [ 'name missing', 'age < 0' ]`.
  - fix4: `PTS1001: using the object an instanceof narrowing holds as a dynamic value as a value[] where it may be changed or kept…`.
- **s01_unknown_read_only.pts:** an `x: unknown` whose members are only read, through `return x instanceof Box ? x.items : []`, `const arr: any[] = x.items`, `r.rows = x.items`, `x.items ?? []`, an annotated object-type local, and `[1, 2].map(() => x.items)`.
  - Node, base and fix3: `1 1 1 1 1 2 1 0`.
  - fix4: six PTS1001s.
- **s05_any_source_read_only.pts:** an `x: any` source, giving `itemsOf(x): any[]` and `const list: any[] = x.items`.
  - Node, base and fix3: `[ 's' ] [] s`.
  - fix4: two PTS1001s.
- **Fix idea:** limit the guard to values H10a made dynamic: inferred members, uninferred references and their dynamic locals. Sources the base already held dynamically (storedDynamic `unknown`, `any`, `catch`) should keep D-179's copy, as they do outside instanceof.
- For inferred and uninferred sources the same PTS1001 in unreached code is R3-F3's accepted trade-off. For these sources it isn't: the base compiled them and their reached branches ran as Node.

### R4-F2 (High, regression: a checked TypeError became silently lost writes): six conversion paths still copy silently
Every line here is Node `true`, fix4 `false`. The base threw `Expected MapLRU<any, any>` for each one.
- **s03b_bypass_paths.pts:**
  - a destructuring default: `const { tags = [] } = c.meta; tags.push(1)`. `bindingSource` returns nil for a binding element with an initializer, so `tags` is a typed `any[]` local and gets a copy;
  - `forEach`'s third argument: `c.items.forEach((_x, _i, arr) => arr.push(1))`;
  - an annotated callback parameter in a dynamic `forEach`: `c.matrix.forEach((row: any[]) => row.push(1))`.
- **s06b_more_paths.pts:**
  - the third argument of `map` and of `filter` (`(_x, _i, arr) => arr.push(1)`). The callback adapter of a dynamic array-method call converts the array to the closure's `value[]` parameter outside `convert`;
  - `(await Promise.resolve(c.items)).push(1)`.
- **s08_object_literal_forms.pts:** object-literal shorthand of a dynamic local, `const it = c.items; const o = { it }; o.it.push(1)`. It loses the write untyped, typed `{ it: number[] }`, and returned from an arrow.
  - `s08b_object_literal_explicit.pts`: `{ it: it }` loses it too, except typed as `{ it: number[] }`.
  - This is r15's "local shorthand" line. It sits in `errors/generic_instanceof_dynamic_value_sinks.pts`, whose other lines' PTS1001s hide it (see Test quality).
- **s04b_field_from_any.pts:** a dynamic local stored in an `any` variable and later read by a field initializer `kept: any[] = pending`.
  - fix4: `false`. Base: TypeError.
  - The copy itself is D-179's general behaviour for `any` values (r06, no instanceof, does the same on the base). H10a now sends instance arrays into it where the base threw.

### Notes
- Things that run as Node on the base now behave differently in R4-F1's PTS1001s and in the four benign emit-c diffs (output unchanged). I found nothing else in the 488-program sweep or the matrix.
- The guard makes no difference to the matrix: it is identical to fix3, since the matrix covers neither write-through-copy nor read-only `unknown` patterns.

## Test quality
- The three new error tests each assert one `// error:` substring. In `generic_instanceof_dynamic_value_sinks.pts`, the "local shorthand" sink still loses its write silently, and the other lines' errors hide it.
- That file's header says every listed sink is PTS1001. In fact the conditional, `||`, `satisfies`, for…of destructuring and local-conditional sinks now compile and run as Node (`s07`), and the shorthand one copies.
- Splitting it into one error test per sink, plus run tests for the sinks that now work, would catch both.
- Nothing tests R4-F1's read-only `unknown`, `catch` and `any` sources. `s01`, `s02` and `s05` are ready as run tests with Node's output; all three pass on the base and fix3.

## Round 5: `253d98b2c`

NOT READY

# H10a review, round 5: commit `253d98b2c` (against `77a63f2f6`)

**Not ready yet.** I'd accept H10a with D-218's recorded gaps and with H10b's scope (values flowing into tsc's `C<any, …>` places), but three things block it:
- **Silent lost writes:** there are still three ways to lose writes silently in programs where the base threw a checked TypeError.
- **New false-positive PTS1001s:** some code that ran as Node on fix4 no longer compiles.
- **Not recorded:** none of these is in D-218's known gaps.

All of them sit in callback and `.par` parameter handling, so the remaining work is narrow.

All runs used `/tmp/h10/pts-fix5`, `pts-fix4` and `pts-base` (with `PTS_HOME` set) against Node on a `.mts` copy.
- **Probes:** `/tmp/h10/review5/p/t*.pts`. `/tmp/h10/review5/bin/cmp.sh FILE [versions]` reprints the outputs; programs with a `// threads:` line run at 1, 2 and 4.
- **Archive:** `/tmp/h10/review5-probes.tar.gz`.

## What holds (verified)
- **R4-F1 is fixed:** s01, s02 and s05 (read-only `unknown`, `catch` and `any` sources) print Node's output again.
- **Round-4 silent paths now fixed:**
  - Dynamic keeps the write: `forEach`/`map`/`filter`'s `arr`, a destructuring default, `await Promise.resolve(c.items)`. Nested, reassigned, captured, async and defaulted callback parameters also keep their writes (t02).
  - PTS1001: shorthand `{ it }` and `{ it: it }`, and an annotated `(row: any[])`.
  - Typed `{ it: number[] }` and callbacks annotated with the instance's own types keep the object (t05, t06).
- **Probe rerun:** I re-ran all round 1–4 probes on fix5 (`review5/rerun/`). Everything matches Node or is an honest PTS1001, except your listed pre-existing cases and s04b.
- **Matrix (`review5/matrix/fix5.jsonl`):** 622 OK, identical to fix4 case by case, none worse, no WRONG.
- **emit-c sweep:** base vs fix5 over 488 generic-class and instanceof programs; only round 4's four benign diffs change.

## Findings

### R5-F2 (High, regression: a checked TypeError became silently lost writes): callbacks the syntactic rules miss
- **Destructured callback parameters** (`t02_callback_param_forms.pts`):
  - `c.groups.forEach(({ list }) => list.push(1))` gives `destructured element false`. Node: `true`. Base: TypeError.
  - Cause: `bindingSource` walks a binding element up to its `Parameter` and then returns nil, because only a direct parameter takes the `callbackSource` branch.
- **Callbacks given by reference** (`t04_callback_references.pts`): `c.items.forEach(pushOne)` with a function declaration, with a `const` arrow, and with an object method `keeper.push`.
  - Each one's `arr: any[]` parameter gets a copy: fix5 prints `false` three times. Node: `true`. Base: TypeError.
  - Only an inline arrow or function expression is handled (`function expression true`).
- **What would cover all of these:** check at the call site instead of at the callback. When a function value whose parameter types hold dynamic values is passed to a method of a value `madeDynamicByInstanceof`, report PTS1001 unless those parameters are Value.

### R5-F3 (High, silent): `.par` callbacks lose writes to the elements
- **Where:** `instanceof.go`, `isReadOnlyParallelView`. It exempts `x.items.par.map/filter(…)` from the guard on the grounds that the method only reads the array. But the conversion copies deeply, so a callback that writes to its element writes to a copy.
- **`t03_par_callback_params.pts`** (threads 1, 2 and 4):
  - `c.rows.par.map((row) => { row.push(0); return row.length; })` leaves `c.rows[63].length` at `2`. Node: `3`. The same on fix4.
- **`t07_par_local_element_writes.pts`:** through a dynamic local and through `.par.filter`. Node: `[ 0, 1, 2 ] [ 31, 1, 2 ]`. fix5: `[ 0 ] [ 31 ]` at every thread count.
- **Control:** `t03b_par_typed_control.pts`, the same over a typed `number[][]` and an `any[][]` without instanceof, keeps the writes on the base and on fix5, so this is H10-specific.
- **Fix:** keep the exemption only where the element type is checkable (no nested arrays or objects).

### R5-F1 (Medium, regression against fix4; base: TypeError, and it compiled unreached code): false-positive PTS1001s on annotated callback parameters
- **Where:** `isCopiedCallbackParameter` flags every annotated parameter whose type holds dynamic values in a callback to a method of a made-dynamic receiver, even when the method doesn't hand it the receiver's objects, or when the parameter is only read.
- **Repro:** `t01_annotated_callback_false_positives.pts`.
  - `c.items.reduce((acc: any[], x) => { acc.push(x * 2); return acc; }, [])`: the accumulator is a new array.
  - `c.matrix.map((row: any[]) => row.length)`: only read.
  - Node and fix4: `[ 4, 6, 2 ] [ 2, 1 ] [ 1, 2, 3 ]`. fix5: two PTS1001s.

## Test quality
- No test covers destructured or by-reference callbacks, `.par` element writes, or annotated callback parameters that aren't the receiver's objects.
- t01, t02 (the destructured line), t03, t04 and t07 have Node's output and are ready as RED tests.
- The per-sink error tests are now one PTS1001 per file, as asked.

## What READY would take
1. Fix or reject R5-F2 and R5-F3.
2. Narrow R5-F1 to the parameters the array methods hand their receiver's objects: elements of array-typed elements, and `arr`.
3. Add D-218 known-gap lines for anything left deliberately. At that point every remaining divergence is a recorded known gap or H10b's, and I'd call it READY.

## Round 6: `842c97cf3`

NOT READY

# H10a review, round 6: commit `842c97cf3` (fix6) against `3d96ccc53`/`9053861f6` (fix5)

(A fresh `reviewer` for round 6: the earlier one's context ended with the previous session. Its probes: [h10a-review6-probes.tar.gz](h10a-review6-probes.tar.gz).)

Every claim and finding below was run by me, with `PTS_HOME=/tmp/h10/s6/base` and `/tmp/h10/s6/base/pts-bin/pts` (fix5) and `/tmp/h10/s6/snap1/pts-bin/pts` (fix6), and `/tmp/h10/pts-base` (base); Node on a `.mts` copy; `// threads:` programs at 1, 2 and 4. Probes `/tmp/h10/s6/review6/p/`, minimal repros `p/min/`. (The parent's working tree had uncommitted lowering changes while I worked; my results are for the snap1 binary.)

The C283 and C284 rules work for the forms the tests use. Two blocking problems: silent lost writes remain (F1–F3), and regressions against fix5 (F4, F6, and exponential compile time F5).

### R6-F1 (High, silent; base threw; not a regression vs fix5): the "only reads" copy exemptions are shallow, but the copy is deep
`forEachOf(c.matrix, (row) => row.push(1))` (a generic `forEachOf<T>(xs: T[], f)`), `firstOf(c.matrix).push(2)`, `c.matrix.forEach(addToFirst)` with `addToFirst(_row: any, _i: number, all: any[][]) { all[0].push(3); }`: Node `[ [ 1, 2, 3, 3 ], [ 1 ] ]`, fix5 and fix6 `[ [], [] ]`, base TypeError. `isReadOnlyArgument` and the new `onlyReadsParameter` rely on `onlyReads`/`isArrayRead`, which accept `xs[0]`, `xs.forEach(f)`, `all[0]`, but converting `number[][]` to `value[][]` copies the inner arrays too (R5-F3's class). Typed code reports PTS1001 for the same by-reference callback (`v27b`); `firstPush(mm: number[][])` with `rows: any[][]` loses writes in typed code on the base too (`v27c`, pre-existing D-094, outside this slice). Fix: the only-read exemption only where the copied type's elements are primitive in the instance.

### R6-F2 (High, silent; base threw): reduce whose initial value is the instance's own object
`c.items.reduce((acc: any[], x) => { acc.push(x); return acc; }, c.tags)`, `c.items.reduce(collect, c.tags)`, `c.matrix.reduce((acc: any[], row) => …, c.matrix[1])`: Node `[ 7, 7 ] [ [], [ 0, 1 ] ]`, fix5 and fix6 `[] [ [], [] ]`. `arrayCallbackObjectParameters` assumes an initial value is the callback's own.

### R6-F3 (High, silent; base threw): C284 is incomplete for spreads, iterators and lib copies
`[...c.matrix][0].push(1)`, `[...c.matrix, []][1].push(2)`, `for (const [, row] of c.matrix.entries()) row.push(3)`, `for (const row of new Set(c.matrix)) row.push(4)`, `Array.from(c.store.values())[0].push(5)`, `[...c.store][0][1].push(6)`, `new Map(c.store).get("k")!.push(7)`, `c.store.values().next().value!.push(8)`: Node `[ [ 1, 3, 4 ], [ 2, 3, 4 ] ] [ 5, 6, 7, 8 ]`, fix5 and fix6 `[ [], [] ] []`. The copy is per element inside the container's conversion. Spread arguments: `c.matrix.forEach(...args)` with `const args = [pushRow] as const` skips `copiesCallbackArgument` (Node `[ [ 9 ], [ 9 ] ]`, fix5 and fix6 `[ [], [] ]`, base PTS1001: worse than the base). Working on fix6: `[...c.items]`, rest patterns, `slice`, `concat`, `splice`, `pop`, `values()` for…of.

### R6-F4 (Medium, regression vs fix5, unrecorded): new PTS1001s for nested objects and method results that are only read, fresh, or never reached
`function firstRow(c): any[] { if (c instanceof MapLRU) return c.matrix[0]; return []; }`, `return c.items.filter((x) => x > 0)` in an `any[]` function, `c.items.filter((x) => x > 0).par.map((x) => x * 2).length`: Node and fix5 `[ 1 ] [] [ 3 ] 1`, fix6 three PTS1001s; an unreached branch alone is PTS1001 too (`r6f4b`). Others: an annotated local, `{ row: c.matrix[0] }`, `[c.matrix[0], …]`, a field, `kept.push(…)`, `store.set(k, …)`, `yield`, an arrow body; `const e: any[] = c.items.filter(…)` and `return c.items.slice()` (fresh arrays, which D-094 copies); `c.matrix[0].par.map(…)`, `c.meta.tags.par.filter(…)`, `c.items.filter(…).par.map(…)`. Where the object is the instance's and is kept or changed, R3-F3's trade-off (fix5 lost the writes there): needs a known-gap line and an error test; fresh method results and nested arrays of primitives should be exempted, as `holdsPrimitiveElements` does for members.

### R6-F5 (Medium, compile time in every program that declares a generic class): exponential in member-chain depth
`class Box<T>` plus `class N { a: N = this; v = 7 }` and `n.a.a…a.v`: depth 20 fix5 0.30 s, fix6 5.7 s; depth 22 fix5 0.26 s, fix6 26.0 s, base 0.49 s; inside a narrowing the same. `isNarrowedDynamic` → `isInstanceofNestedObject` → `holdsInstanceofDynamicValue` walks `isMemberOfNarrowedInstance` → `storedDynamic(object)` → `isNarrowedDynamic` and also recurses into `instanceofDynamicSource(node.Expression())`: two branches per level. Fix: memoize, or the cheap type test first.

### R6-F6 (Low-Medium, regression vs fix5 and the base; partly recorded): functions given to methods that only store them are PTS1001
`o.on("x", handler)` (a user method that stores it) and `o.handlers.set("y", handler)` for an `o: object` holding an `Emitter<any>`: Node, fix5 and base `[ 2, 3 ] [ 1, 2, 0, 0 ]`, fix6 two PTS1001s. Lib collection methods that never call their argument (`Map.set`, `Set.add`) are knowable and should be skipped. The message, which names "the object an instanceof narrowing holds" at a function argument, is misleading.

### R6-F7 (Low, pre-existing D-179 class, unrecorded for these forms)
`c.matrix.forEach(pushRow as any)`, `g: any = pushRow`, `fns[0]` from an `any[]`: Node 1, fix5/fix6 0 (typed code without instanceof too). `(row: object) => (row as any[]).push(1)`: fix6 0, Node 1 (control without instanceof loses it on the base too). For an `unknown` holding a `Grid<any>`/`Box<unknown>`, `const r: number[] = u.matrix[0]; r === u.matrix[0]` is now `false` where Node says `true` and base and fix5 reported PTS1001 (`v06`, `v06b`); the writes were already lost on the base (`v06c`). These belong next to D-218's `s04b` line.

### Notes (pre-existing on fix5, checked TypeErrors)
`c.make(() => ({ list: [3] }))` throws `Expected {list: number[]}, got Object` (`v09`); `c.pending.then(…)` on a Promise member throws `c.pending.then is not a function` (`v24`); converting an any-instance's `value[]` to `number[]` loses writes in typed code too (`v07`).

### What holds (verified)
C283 inline callbacks (untyped and annotated, defaults, patterns, tuples, `readonly`, unions, nested; `some`, `every`, `flatMap`, `findLast`, `sort`, async; reduce with an own initial value, `reduceRight` without; Map and Set `forEach`'s third parameter; own methods and uninferred narrowings' methods; generic instances, generators, async functions); C283 results (`c.pick((all) => all[1])` keeps the object, fix5 lost it); by-reference functions report PTS1001 in every call form; C284 for typed targets, reads, nested `Set`/`Record`/`Map`/generic members, optional chains, `===`, `indexOf`/`includes`, aliasing by assignment, `parallel grow(c.matrix[1])`, `.par` callbacks reading captured nested objects at 1/2/4 threads; read-only `unknown`/`any`/`catch` sources; the 128 probes of rounds 1–5 change only `s03b` and `t04`; the matrix 622 / 130 / 25.

### Test quality
The 11 error tests compile on fix5 and give one PTS1001 each on fix6; the 5 run tests' expectations equal Node's, 4 fail on fix5 (`callback_references` is a guard). Gaps: nested objects at copying sinks, `.par` over nested arrays and method results, reduce with an instance initial value, spreads/iterators/lib copies, shallow only-reads, compile time on long chains.

### What READY would take
1. F1: only-read exemptions only for primitive-element copies. 2. F2: count reduce's accumulator when the initial value holds an instanceof dynamic value. 3. F3: guard or keep as values the element conversions of spreads, `Array.from`, `Set`/`Map` constructors and iterator results, and check spread arguments; or record them, with justification. 4. F5: memoize and re-time. 5. F4, F6: exempt fresh results, nested arrays of primitives (with `.par`) and lib methods that only store their argument; record the rest as R3-F3's trade-off with one error test. 6. F7: a known-gap line next to `s04b`.

## Round 7: `f801eb17d`

NOT READY

# H10a review, round 7: commit `f801eb17d` (fix7) against `842c97cf3` (fix6)

(The round-6 reviewer, resumed. Probes: [h10a-review7-probes.tar.gz](h10a-review7-probes.tar.gz); `bin/cmp.sh FILE [fix5 fix6 fix7 base]`.)

fix7 fixes R6-F1, R6-F2, R6-F4, R6-F5 and the spread-argument part of R6-F3, and keeps nothing new from fix6 broken outside instanceof code. The reasoning for not taking R6-F6 as suggested holds. Blocking: R7-F2 (silent lost writes: R6-F6's rule is enforced only for call arguments) and R7-F1 (a regression: the new reduce rule rejects `groupBy`-style reducers).

### R7-F1 (Medium, regression against fix5 and fix6): by-reference reducers with a fresh non-array accumulator are PTS1001
`c.items.reduce(groupInto, {})` with `groupInto(acc: Record<string, any[]>, x)`, `c.items.reduce(count, new Map<any, any[]>())`, `c.items.reduce(collect, start)` for a local `const start: any[] = []`: Node, fix5 and fix6 `{ n: [ 3, 3 ] } Map(1) { 3 => [ 3 ] } [ 3, 3 ]`, fix7 PTS1001. `arrayCallbackObjectParameters` counts the accumulator unless `isNewArray(initial)`. Suggested: count it only without an initial value, or where the initial value holds an instanceof dynamic value, or converts to the accumulator's type by a copy (N9's own `number[]` needs counting). Inline reducers still work.

### R7-F2 (High, silent; base threw; same on fix5 and fix6): functions kept by members through push or assignment
`c.list.push(addRow)`, `c.list.push((rows: any[][]) => { rows.push([8]); })`, `c.list[2] = addRow`, `c.cfg.onRows = addRow`, then the instance's `fire()` calls each with `this.matrix`: Node `6`, fix5/fix6/fix7 `2`, base TypeError. One form per file in `kept/`: `k_array_push`, `k_array_unshift`, `k_array_splice`, `k_element_assign`, `l_inline_push_annotated`, `l_inline_assign_annotated`, `n_nested_field_assign`, `n_array_literal_assign` (`c.list = [addRow]`), `n_concat_assign`: Node 3, fix6 and fix7 2. Already PTS1001 on fix7: `c.list.push(...fs)`, `c.handlers.set(k, addRow)`, `c.onRows = addRow`, `c.cfg = { onRows: addRow }`. Working: an untyped inline `c.list.push((rows) => …)`, an annotated inline given to `Map.set`. Fix: apply `copyingParameterIn` wherever a typed function is boxed into a value made dynamic by an instanceof narrowing (dynamic element and property sets, array mutators, array literals assigned to members), and make an annotated inline callback given to a keeping method a dynamic local.

### R7-F3 (Low, regression against fix5 and fix6): `handsToCallback` counts every non-literal callback as handing the array
`function each(_x, i, arr: any[]) { if (i === 0) arr.forEach(show) }` with a one-parameter `show`: Node, fix5, fix6 `item 3` twice, fix7 PTS1001. Fix: use `knownFunction` and its declared parameter count. Message: for inline rest callbacks the PTS1001 says "a function written there takes them as they are", which is false there.

### Recorded gaps: justified?
R6-F6 (kept handlers stay PTS1001): justified (the kept-handler program prints 2 on fix5 where Node prints 3; typed code without instanceof reports PTS1001 for the same `set`; an uninferred source may hold any instance). C285 (container flows → H10b): acceptable as recorded; the same copies happen without instanceof for an `any` holding typed rows cast to `any[][]` (`c01_container_any_control.pts`), the D-179/`s04b` class, and H10b's design C won't cover `any[][]` containers, so H10b's acceptance must cover C285 explicitly, as the plan line does. C288, C286, C287 and the deep read-only PTS1001s: consistent with earlier trade-offs; read-only deep helpers on an uninferred source (`total(o.matrix)` with `rows: any[][]`) ran as Node on the base, fix5 and fix6 and are now PTS1001 (`z_deep_read_only_param.pts`): honest, deserves its own known-gap line. The F7 revert: agreed.

### What holds (verified)
R6-F5: `u01_chain_depth22` 65.7 s (fix6) → 0.14 s (fix7), depth 40 0.18 s, depth 80 0.17 s, `u02_instanceof_chain_depth16` 1.43 → 0.10 s, dynamic-member chains at depth 40 about 0.5 s. R6-F1, R6-F2, N10: honest PTS1001s. R6-F4: fresh `filter`/`slice` results, `.par` over nested and fresh arrays of primitives, `concat` run as Node. N15: `u.each` callbacks keep writes (the remaining `u.mapRows((row) => row)` identity miss is lost on the base too, D-179). New forms matching Node (`h01`, `h03`): pattern defaults, `map` identity into `number[][]`, `typeof`/`Array.isArray` narrowing of nested values, `Record` dot access, `for…in`, computed keys, nested generic members, closures, `as unknown as any[]`, `splice`, `length` writes, compound writes, generic-class methods narrowing other instances. rerun5 changes only `s03b` and `t04`; the matrix 622 / 130 / 25. Programs without instanceof: emit-c identical on fix6 and fix7 for 60 random programs and all 21 using `.concat(`; the `arrayValues` change fixes a predicate-narrowed `a.concat(u)` that threw on fix6; `rows.concat(t)` with an `Array.isArray`-narrowed `t` still nests (pre-existing C287).

### Test quality
All 18 changed or new error tests give exactly one PTS1001 on fix7 with the expected text; the 7 new ones compile on fix6 except `nested_object_returned`. The 3 new run tests equal Node and fail on fix6; the skipped container test matches Node and fails on fix7. Gaps: kept functions (R7-F2), reducers with `{}`/`new Map()` (R7-F1), a named callback in a by-reference function (R7-F3).

### What READY would take
1. R7-F2: report a typed function with a copying parameter wherever it is stored into a made-dynamic value (array mutators, element and property sets, array literals assigned to members); annotated inline callbacks given to keeping methods as dynamic locals. 2. R7-F1: narrow the reduce rule (fresh objects, collections and same-typed locals don't count) with a run guard. 3. Optional: R7-F3, and a known-gap line for deep read-only helpers. "With 1 and 2 done, every remaining divergence I know of is a recorded gap, and I'd expect READY."

## Round 8: `8099aadb5`

NOT READY

# H10a review, round 8: commit `8099aadb5` (fix8) against `f801eb17d` (fix7)

(The same reviewer, resumed. Probes: [h10a-review8-probes.tar.gz](h10a-review8-probes.tar.gz).)

R7-F1, R7-F2 and R7-F3 are fixed as claimed, and nothing that matched Node on fix7 among the round 6–7 probes or the matrix got worse. Left: one regression against fix7 and two silent divergences where the base threw, none recorded.

### R8-F1 (Medium, silent; base threw for the inferred source; not recorded): functions read through a narrowed member lose their identity
`const h = s.listeners[0]; const i = s.listeners.indexOf(h); console.log("index", i, h === s.listeners[0]); if (i >= 0) s.listeners.splice(i, 1);` for an `Emitter<T>` with `listeners: ((value: T) => void)[]`: Node `index 0 true` / `[ 'b1' ] 1`; fix5–fix8 `index -1 false` / `[ 'a1', 'b1' ] 2` (never removed); base TypeError. Also `const f = c.cb; f === c.cb` false, `new Set([c.cb, c.cb]).size` 2, `c.list.filter((f) => f !== first)` keeps both, `indexOf` plus `splice(-1, 1)` removes the wrong listener. Cause: a function value read from the dynamic member into a place whose tsc type mentions any gets an adapter; dynamic locals exclude function types (`isFunction`) and `isInstanceofNestedObject` excludes callable types. Same on the base for `any`, `object` and `unknown` sources (C288's class), but H10a routes inferred narrowings, where the base threw, into it. Close by keeping function-typed reads as dynamic values (C284's fix for functions), or record.

### R8-F2 (Medium, regression against fix7, unrecorded): re-storing the instance's own functions is PTS1001
`c.cfg.onRows = c.list[0]; c.list = c.list.toReversed();` (also `c.list = c.list.slice(1)`, `c.list[0] = c.list[1]`): Node, fix5 and fix7 `6 2`, fix8 two PTS1001s. These are the instance's own closures, which need no adapter; `storesCopyingFunction` judges them by tsc's type. Fix: skip values that `holdsInstanceofDynamicValue` (a typed local holding an adapter, `const h = c.list[0]; c.cfg.onRows = h`, stays PTS1001, rightly).

### R8-F3 (Low, silent; base threw; unrecorded): destructuring assignment and `Object.assign` bypass the R7-F2 store check
`[c.list[0]] = [addRow]; ({ f: c.cfg.onRows } = { f: addRow }); Object.assign(c.more, { onRows: addRow });`: Node 5, fix5/fix7/fix8 2, base TypeError. Already PTS1001: `??=`, `||=`, aliases (`const l = c.list; l.push(f)`), conditional targets, nested `c.cfgs[0].onRows`, objects holding the function, a `let` function, method values, uninferred sources (`kept/q_*.pts`); `unknown` sources print 2 on the base too (D-179).

### What holds (verified)
R7-F1 (`{}`, `new Map()`, same-typed locals; literal initial values holding instance objects are honest PTS1001s), R7-F2 (all 15 round-7 `kept/` probes honest or Node's), R7-F3. Rerun of the 145 round 6–7 probes, fix7 against fix8: 19 forms move to an honest PTS1001 or OK; other PTS1001 changes are the shortened hint; remaining differences are C285, C287, C288, D-179 controls and round-6 TypeError notes; v15d (`parallel`, which Node can't parse) and v25 (a non-associative `.par.reduce`) are wrong probes. Matrix `new6` identical to `new5`; `rerun7` changes only `s03b` and `t04`; emit-c identical on fix7 and fix8 for 50 random programs without instanceof.

### Test quality
The 4 new error tests compile on fix7 and print 2 where Node prints 3; one PTS1001 each on fix8. The run guard matches Node and fails on fix7. Gaps: function identity, re-storing own functions, destructuring and `Object.assign` stores.

### What READY would take
1. R8-F2: don't report values read through the narrowing; `r8f2` as a run guard. 2. R8-F1: keep function-typed reads as dynamic values, or record with C288. 3. R8-F3: extend the store check to `Object.assign` and destructuring targets, or record. "With those done, every remaining divergence I know of is a recorded gap or H10b's, and I'd expect READY."

## Round 9: `3d72b7177`

NOT READY

# H10a review, round 9: commit `3d72b7177` (fix9) against `8099aadb5` (fix8)

(The same reviewer, resumed. Probes: [h10a-review9-probes.tar.gz](h10a-review9-probes.tar.gz).)

The round-8 fixes hold for the forms they target, but two new mechanisms turn fix8's honest PTS1001s into silent wrong output where the base threw (R9-F1, R9-F3), and there is one small false positive (R9-F2).

### R9-F1 (High, regression: fix8 PTS1001, fix9 silent, base threw): values that only partly hold the instance's own functions
`c.cfg.onRows = c.maybe ?? addRow`; `const h = flag ? c.list[0] : addRow; c.list.push(h)`; `const r = flag ? c.reader : pushRow; c.matrix.forEach(r)`: Node `4 1`, fix8 three PTS1001s, fix9 `2 0`. One form per file in `mix/m_*.pts` (`flag ? c.list[0] : addRow`, `c.maybe || addRow`, `c.list[0] = c.maybe ?? addRow`, such locals given to push or a callback); round 7's `n_concat_assign` (`c.list = c.list.concat([addRow])`) also went from PTS1001 to a silent 2 (Node 3); `c.list.push(flag ? c.list[0] : addRow)` was silent on fix8 too. Cause: R8-F2's skip takes any value that `holdsInstanceofDynamicValue`, true when any branch of a conditional, `??` or `||` holds one, or for a `concat` on a member; and function-typed values from such sources are now dynamic locals, which `copiesCallbackArgument` doesn't look into. Fix: skip only values whose every part is the instance's own; don't make mixed function initializers dynamic locals, or check them where given or stored.

### R9-F3 (High, regression: fix8 PTS1001, fix9 silent, base threw): results of calling a function local are copies
`const mk = c.mkList; mk().push(2); const ys = mk(); ys.push(3); console.log(c.items, mk() === c.items, c.mkList() === c.items)`: Node `[ 1, 2, 3 ] true true`, fix8 PTS1001, fix9 `[ 1 ] false true`; also `const xs: number[] = mk()` and `mkObj().list.push(3)` (`f04_results_of_calls.pts`). The new `call()` path's `narrowedValue` converts an `any[]` result to `value[]` (D-089's FromUnion, a copy), and such a call isn't an `instanceofDynamicSource`. Fix: make calls of such callees sources.

### R9-F2 (Low-Medium, regression against fix8): the instance's own functions stored through literals are PTS1001
`[c.list[0], c.list[1]] = [c.list[1], c.list[0]]; Object.assign(c.cfg, { onRows: c.list[0] })`: Node and fix8 `6 2`, fix9 two PTS1001s. The R8-F2 skip doesn't look inside the literal.

### What holds (verified)
R8-F1 (`r8f1`, listener identity for `object` and `unknown` sources; only `new Set([c.cb, c.cb]).size` differs, recorded with C285); function locals in general (calls, `typeof`, `length`, `map(f)`, templates, generators and async functions returned through them, optional calls, `??` callees, `bind`/`call`/`apply`, closures, `let` reassignment, recursion, IIFEs, `unknown`/`any`/`catch`/`object` sources, three instances of a generic function, `.par.map(f)` at 1/2/4 threads, returning and storing in a typed outer variable), several new fixes over fix8 for `unknown`, `any` and `catch` sources; R8-F2 (`o_filter_listeners` was silently wrong on fix7); R8-F3. Rerun of 180 round 6–8 probes: 18 change, 17 improve, one regresses (`n_concat_assign`, R9-F1). Matrix `new8` identical to `new6`; `rerun8` changes only `s03b`, `t04`; emit-c identical on fix8 and fix9 for 50 random programs without instanceof.

### What READY would take
1. R9-F1: an "every part is the instance's own" test for the skip, judging the rest by type; keep mixed-source function initializers out of dynamic locals or check them where given or stored. 2. R9-F3: calls of callees holding instance values as `instanceofDynamicSource`s. 3. R9-F2: the same test inside literals, `r9f2` as a run guard. 4. Re-run `mix/`, `kept/`, `own/` and `f04*`. "With those green, I know of nothing else blocking."

## Round 10: `29444bf3c`

NOT READY

# H10a review, round 10: commit `29444bf3c` (fix10) against `3d72b7177` (fix9)

(The same reviewer, resumed. Probes: [h10a-review10-probes.tar.gz](h10a-review10-probes.tar.gz).)

All three round-9 findings are fixed for the forms reported, and none of 199 earlier probes got worse. Two problems remain; the first blocks.

### R10-F1 (High, regression: fix8 PTS1001, fix9 and fix10 silent, base threw): reads of a mixed `concat`/`with` result count as the instance's own
`c.cfg.onRows = c.list.concat([addRow])[1]; const h = c.list.with(0, addRow).at(0)!; c.list.push(h); c.list.concat([addRow]).concat([]).forEach((f, i) => { if (i === 1) c.list.push(f); });`: Node 5, fix8 three PTS1001s, fix9/fix10 2. One form per file in `own/a_*.pts` (`.find(…)` stored, `.at(-1)` pushed, a `forEach` parameter of the result, `.slice(1)[0]`, `with(0, addRow)[0]`, a local of the result indexed). Honest PTS1001s: `for (const f of c.list.concat([addRow]))`, `const all = [...c.list, addRow]; … all[1]`. Cause: `isInstanceOwnValue` checks adding methods' arguments only at the outermost node. Fix: follow reads to their receivers and callback parameters to their call's source, or report copying functions given to `concat`/`with`/`toSpliced` where given, as keeping methods do.

### R10-F2 (Medium, silent; base threw; same on fix8–fix10; not recorded): `??` and `||` between the instance's own functions give adapters
`const b = c.maybe ?? c.list[1]; const o = c.maybe || c.list[1];` then `b === c.list[1]`, `c.list.indexOf(b)`, …: Node `true 1 true 1 b1 true`, fix8–fix10 `false -1 false -1 b1 false`. A conditional between the same values keeps identity (`b01_own_positive_forms.pts`). Fix: lower `??`/`||` of own function values as dynamic values, or record with R8-F1's typed-local gap.

### Note (pre-existing, not H10a's)
Storing the program's `(v: any) => void` into a `(value: number) => void` array creates an adapter, so `indexOf`/`includes` miss it: through the narrowing (`b03_program_function_identity.pts`: Node `0 1 true`, fix10 `0 -1 false`) and in typed code on the base too (`b03b_typed_control.pts`): the general adapter-identity class, next to C288.

### What holds (verified)
R9-F1 (all `mix/m_*`, `r9f1`, round 7's `n_concat_assign` honest PTS1001s), R9-F2 (`r9f2`, `d_swap_own`), R9-F3 (`r9f3`, `f04`, `f04b`); positive own forms (a conditional between two instance functions, `c.maybe ?? undefined`, `.find(…)` over the instance's list, callback parameters of `forEach` and of the instance's own method, re-storing `[...c.list]`, two instances of a generic function) match Node except R10-F2's `??` line. Rerun of 199 round 6–9 probes: 16 change, all improvements. Matrix `new9` identical to `new8`; `rerun9` changes only `s03b`, `t04`; emit-c identical on fix9 and fix10 for 50 random programs without instanceof.

### What READY would take
1. R10-F1 (one of the two fixes), `r10f1` as an error test. 2. R10-F2: keep `??`/`||` of own function values dynamic, or record. "With those, every divergence I know of is a recorded gap or belongs to H10b/H29, and I'd expect READY."
