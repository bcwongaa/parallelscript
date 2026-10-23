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
