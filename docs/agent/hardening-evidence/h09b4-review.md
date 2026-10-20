# H09b4a review

Saved by the parent from the `reviewer` subagent's final messages (claude-opus-5-5, xhigh; the Agent call passed the `opus` alias), 2026-10-20. The reviewer was read-only and reviewed byte-checked snapshot worktrees of the change. The parent's responses follow each round; see [h09b4.md](h09b4.md).

## Round 1 (snapshot `/tmp/h09b4/snap3`, the fix commit `0377fb2eb`)

Archive: [h09b4-review-probes.tar.gz](h09b4-review-probes.tar.gz) (sha256 `5c4c13ad0c39e01f5dd9a7ad5b8a785efbefef53283af72c7ef1f8ee89ecbad4`; `round1/INDEX.txt` inside).

---

**Verdict: not ready.**

The change mostly works: dead branches lower, `never` type arguments return undefined, and nothing in the corpus got worse. But the core safety claim is false. Programs with no lie at all now reach the trap: tsc keeps stale narrowings across mutations, and some instances are shared between type arguments of the same IR type. 12 of my probes ran exactly like Node on the base and now throw the trap TypeError (8 with no lie, 4 lying-predicate prints).

**Archive:** `/tmp/h09b4/review/h09b4a-review-probes.tar.gz`, sha256 `5c4c13ad0c39e01f5dd9a7ad5b8a785efbefef53283af72c7ef1f8ee89ecbad4`. It includes `INDEX.txt`, `run.sh`, the probes and the recorded `out/<name>/{node,base,snap}.txt`. `/tmp/h09b4/review/DONE1` was touched last. All probe paths below are under `/tmp/h09b4/review/round1/probes/`. Every output was recorded on Node, base and snapshot.

### Findings

**F1 — High, introduced. A stale narrowing reaches the trap without any lie.**
- **Cause:** tsc keeps a narrowing across closure assignments, calls, `await`s and getter reads. `isDeadRead` intersects the variable's declared type (`T | number`) with that stale narrowed type (`T & number`), which is never for a string T. At run time the value really is a number, which the declared type allows.
- **Repros:**
  - `s01_closure_stale_let.pts`: a closure assigns a captured `let`.
  - `s03_method_changes_field.pts`: a method sets `this.v = 0` between two tests.
  - `s10_async_stale_field.pts`: another task changes a field across an `await`.
  - `s11_loop_callback_stale.pts`: a `forEach` callback assigns an accumulator.
  - `s16_getter_stale_no_call.pts`: an interface property implemented by a getter, with no call at all.
  - `s02_property_mutated_by_call.pts`: the base gave PTS1001 here, not Node's output.
- **Outputs:**
  - s01: Node and base print `number now 6`. The snapshot prints ``caught code that can't run for T = string ran: `y` narrows to `T & number` here; a type predicate, assertion or cast was wrong``.
  - s03: Node and base print `1`. The snapshot prints the same trap message for `` `this.v` ``.
  - s11: Node and base print `last doubled 6`. The snapshot ends with an uncaught trap TypeError and exit 1.
- **Direction:** test deadness against the declared type and the test's own type, i.e. `instantiate(declared) ∩ (narrowed with T → unknown)`. Stores are typed and checked, so staleness can't produce a value outside the declared type. Only `T`-declared reads would then be dead. Alternatively, limit dead reads to references whose narrowing can't go stale (consts, and parameters never assigned).

**F2 — Medium, introduced. Shared instances break the "dead for all of them" rule.**
- **Repros:**
  - `s05_function_literal_shared.pts`: `run<() => 1>` and `run<() => number>` share an instance, with an honest `v is 2` predicate. Node and base print `two 12`. The snapshot prints ``caught code that can't run for T = () => 1 ran: `r` narrows to `ReturnType<T> & 2` here…``.
  - `s15_function_type_class_shared.pts`: the same for a generic class `Holder<F>`.
  - `s04d_json_no_boolean.pts`: `describe<Json>` (a recursive union, IR value, no booleans) and `describe<unknown>(undefined)` share an instance. Node and base print `undefined true`. The snapshot prints the trap message for `T & undefined`.
- **Cause:** `literalFree` returns true for any type with call signatures without looking at them. `widestOfIRType` keeps a recursive union itself although its IR type is `ir.Value`, the same as unknown's.
- **Direction:** return unknown whenever `irType(t) == ir.Value`, and treat function types (or literal types in their signatures) as not literal-free.

**F3 — Medium, introduced. The dead tail causes PTS9001 where the base gave PTS1001.**
- **Repros:** `s07c_dead_tail_let_hoisted.pts`, `s07e_dead_tail_let_closure_before.pts`, `s07f_switch_clause_dead_tail.pts`.
- **Output:** `PTS9001: internal compiler error: the generated C doesn't compile: too few arguments to function call, single argument 'z' was not specified`.
- **Cause:** `statementList` drops the declarations after a dead statement. Closures made by `hoistedFunctions`, and arrows created earlier (or in a later switch clause), still capture those `let`/`const` bindings.
- **Direction:** still declare the dead tail's lexical bindings that closures capture, or keep lowering the tail's declarations.

**F4 — Medium, introduced. Lying-predicate branches that only print or copy the value now throw.** D-195 meant to keep these working.
- **Repros:**
  - `o01_lie_concat.pts`: `"n=" + x`.
  - `o02_lie_copy_print.pts`: `const y = x`.
  - `o03_lie_push_unknown.pts`: an `unknown` parameter and `unknown[].push`.
  - `o07_lie_json_array_print.pts`: `console.log([x], {value: x})` and `JSON.stringify`.
- **Outputs:** Node and base print `n=s` / `lied` (and so on). The snapshot prints the trap message.
- **Doc impact:** D-195's "(the base rejected such a program with PTS1001)" is false for these shapes. The base lowered the never-typed read as a boxed value.
- **Direction:** widen `takesAnyValueAsIs` (string `+`, initializers of locals tsc infers never, `unknown`/`any` parameters and elements), or keep the base's dynamic read wherever it lowers.

**F5 — Medium, introduced. An optional chain's own `undefined` is intersected away.**
- **Repro:** `s19b_optional_chain_predicate.pts`. An honest `v is number | undefined` predicate narrows `o?.v` to `(T & undefined) | (T & number) | undefined`, and `f<string>(undefined)` reaches the read.
- **Outputs:** Node prints `n undefined`. The snapshot prints the trap message. The base gave PTS1001.
- **Direction:** skip reads that are optional chains, or add `undefined` to the declared type for chains.

**F6 — Low, still fails (the base failed too).** `l01b_and_or_default.pts`: `(typeof x === "number" && x * 2) || 7` is now `PTS1001: converting boolean to number`. `logicalWithDeadRight` gives the result the left operand's IR type, not the type of the whole expression.

**F7 — Low, gaps D-195 doesn't list.**
- `c02_readonly_array_isarray.pts`: a live `Array.isArray` branch for `readonly number[]` is still PTS1001 (`` `x.length` ``). `narrowedInstance`'s check that the member is assignable to `any[]` fails for readonly arrays.
- `c03_typed_array_isarray.pts`: dead branches for `Uint8Array` and `Error` type arguments aren't traps and stay PTS1001. Lib types with `unknown` or literal-typed members widen to unknown.

**F8 — Low.** `l06_void_result_strict_equality.pts`: `r === 5` on a void-typed result that holds 5 is now constant false. Node prints `same true false`, the snapshot `same false true`; the base gave PTS1001. This rests on the existing rule that a `void` call's result is undefined.

**F9 — Docs.**
- SPEC §8, the GUIDE bullet and D-195 all say "Only a type predicate, assertion or cast that lied can reach it". F1, F2 and F5 show otherwise.
- D-195's "a trap only where it is dead for all of them" is false (F2).
- D-195's list of type arguments without traps is incomplete (F7), and its claim about the base rejecting such programs is wrong (F4).

**F10 — Test gaps.** The new tests contain no stale narrowing, no function-type or recursive-union sharing, no captured dead-tail bindings, no optional chain and no concat-only lie. The tests I ran agree with their expect blocks (I checked four), and none pins wrong behaviour.

### What held up
- Dead `&&`/`||`/`??` right operands, `?:` branches (whose live branch is undefined, null, a union, an object, void or a closure) and the unset-field fallback (`l01c`, `l01d`, `l02`, `l03`, `s08`).
- `===`/`!==` constants with side effects kept (`l04`), and `instanceof` on primitives keeping side effects (`l05`).
- Shorthand properties and spreads (`l07`); loop heads, `for…in`, `switch`, `do`/`while` and labels (`l08`).
- An async arrow's dead body rejects its promise rather than throwing at the call (`l09`).
- `never` type arguments: getters, async, generators, records, `never[]` (`n01`, `n02`, `n04`, `n05`).
- `typeof null` is `"object"` (`s06`); a hoisted function in a dead tail (`s07d`); dead `yield`s and `await`s (`s12`–`s14`).
- Generic methods, statics, generic methods of generic classes and generic arrows in closures (`c04`).
- Live controls for `void`/`undefined`, `Date`, `Pt | string`, `TypeError`/`Error` and `Pt | null` (`c01`).
- Corpus: no run test went from compiling to failing. With AllInstances alone and no fallback, nothing that lowered on the base fails, so no fallback is hidden (`emit/nofb_all.txt`). The C changed only for the new tests and three existing ones (+2.6% to +14.7%). Compile time is unchanged (16.1 s against 15.8 s on a 400-statement × 30-instance stress).

The snapshot and the main repo are untouched. My scratch builds came from `go build -overlay` into `/tmp/h09b4/review/nofb`.

### Parent's response to round 1

All findings but F6–F8's pre-existing parts were fixed in `fe2a0dcf6` (full suite on its snapshot `snap5`: 1,880 pass / 311 skip / 0 fail): F1 (`isDeadRead` intersects the declared type with the tests' type, `testedType`; an intermediate version erased T to unknown everywhere, which made `keyof T` never and broke two corpus programs and `logical_assign_erased`, caught by the full suite), F2 (`isDynamicIRType`, call signatures in `literalFree`), F3 (`uninitialized`), F4 (any/unknown contexts, literals there, `+` with a string, `===`; a copy deliberately stays a trap), F5 (no test inside optional chains), F6 (`logicalWithDeadRight`'s union type), F7 (readonly arrays; library classes are their own IR type), F8 (no constant for void), F9 (docs), F10 (four new tests, two extended). Of the 58 probes, 46 then matched Node; the other 12 are the intended lies (l09, o02, o04, o06) or print exactly what the base prints (l06 → C225, s19b → C226, s02 → C220's note, l10 and s18 D-141, n03 D-155, s07 and s07b unsupported `var`).

## Round 2 (snapshot `/tmp/h09b4/snap5`, commit `fe2a0dcf6`)

Archive: [h09b4-review2-probes.tar.gz](h09b4-review2-probes.tar.gz) (sha256 `00f6457bb8aa9dc1bb06ace85f649faaffee43926b824ee8767de34a157a0368`; `round2/INDEX.txt` inside).

---

**Round 2 verdict: not ready.** All 12 round-1 regressions are fixed, and so are the PTS9001 dead-tail crashes. But the two no-lie holes (F1 stale narrowing, F2 shared instances) are only partly closed, and the fix adds one internal compiler error and one compile-error regression against round 1.

**Round-1 probes re-run on snap5:** 46 match Node. The other 12 are as you describe: intended lies `l09`, `o02`, `o04`, `o06`; same output as the base: `l06`, `l10`, `n03`, `s02`, `s07`, `s07b`, `s18`, `s19b`. These all run as Node now: `s01`, `s03`, `s04d`, `s05`, `s10`, `s11`, `s15`, `s16`, `o01`, `o03`, `o07`, `s07c`, `s07e`, `s07f`.

### Findings

**R2-F1 — High (the round-1 F1 hole, not fully closed). A stale narrowing of an indexed-access type still reaches the trap with no lie.** `testedType` drops only bare type parameters from intersections; `T["a"] & number` and `T[number] & number` keep their T-mentioning member, which the instance turns into `string & number`. Repros `r2/a09_stale_indexed_literal_key.pts` (a `let v: T["a"] | number` that a closure sets to 5) and `r2/a10_stale_array_element_type.pts` (`T extends unknown[]`, `let v: T[number] | number`): Node and base print `number now 6`; snap5 prints the trap. Direction: drop every intersection member that mentions an instance type parameter. Held up: `a01` (`T[K]`), `a07` (`keyof T & string`), `a08` (`Extract<T, string>`). Not this bug: `a02`/`a03` hit the existing stale-narrowing checks, as plain code does (base PTS1001).

**R2-F2 — Medium (the round-1 F2 hole, not fully closed). A recursive union nested inside a type argument still shares unknown's instance.** `isDynamicIRType` looks only at the top-level type, and `literalFree` accepts a nested `Json` and nested any/unknown leaves: `{v: Json}`/`{v: unknown}`, `() => Json`/`() => unknown`, `Box<Json>`/`Box<unknown>`, `[Json]`/`[unknown]` share one IR type and instance. `r2/a04`–`a06` regress from the base's correct output (trap on snap5); `a11` was PTS1001 on the base. Direction: reject nested types whose IR type is dynamic, other than any/unknown themselves.

**R2-F3 — Medium, introduced in round 2.** `r2/i01_array_predicate_intersection.pts`: an honest `v is string[]` predicate on T = `number[]`; "arrays include arrays" keeps `number[]` where tsc's type is `number[] & string[]`, and `x[0].length` lowers on a double: PTS9001 (`member reference type 'double' is not a pointer`), where Node prints `strings ab-c 2 other strings  -1 other` and the base gave PTS1001. Direction: count an array as included only where the other's element type takes the member's elements.

**R2-F4 — Medium-low, introduced in round 2.** `r2/x01_exempt_spread_into_console.pts`, `r2/x04_exempt_spreads_unknown.pts`: a dead `...x` under an any context is exempt, so not a trap, but can't be lowered: PTS1001 `spreading a string into arguments` (base too); round 1's snapshot ran both as Node. Direction: don't exempt reads under a spread.

**R2-F5 — Low, docs.** D-195's placeholder "MATRIX_FINAL"; SPEC §8 and the GUIDE's "only a lie can reach it" (contradicted by R2-F1/F2); the GUIDE's "printing it … works" vs a copy that traps (`o02`); D-195's known gaps; SPEC §9.134's "arrays include arrays"; no tests for R2-F1–F4.

### What held up
Uninitialized dead-tail variables (TDZ ReferenceError kept, destructuring, generators, async: `u01`, `u03`–`u05`); the strict-equality constant (`e01`–`e03`); the exemptions (`+`/`+=` with strings, templates, `JSON.stringify`, `String`, unknown parameters, `Map<string, unknown>.set`, `unknown[]` literals, `as any`; a cast `x as number` still traps, intended: `e04`, `e04b`, `e05`, `x02`, `x03`); live controls (`NonNullable<T>`, template-literal type arguments, `Partial<T>` with `keyof T`, rest tuples, nested generic arrows, `this` types: `c05`); 936 corpus and run programs: none went from compiling to failing on emit-c; with AllInstances only, none regressed and 12 improved; C differs only in the generic tests and three never-typed element reads, which now return undefined past the end; compile time 15.5 s base / 15.9 s snap5. Pre-existing: `typeof` of a TDZ variable doesn't throw (`u06`/`u07`), switch-scoped TDZ (`u08`/`u09`), a primitive held as an interface value is PTS1001 (`e06`).

### Parent's response to round 2

All four fixed as the reviewer suggested: R2-F1 (`testedType` drops every intersection member that mentions an instance type parameter, `mentionsParameters`), R2-F2 (`literalFree` rejects nested types of dynamic IR type other than any and unknown), R2-F3 (`includes`: an array includes an array only if the other's elements take its own), R2-F4 (a spread of the read is no any-value use; a spread of an array literal that holds it still is, which round 2's `e05` needs); R2-F5 (docs). The first version of R2-F2's fix asked for the IR type of a generic signature's own type parameter inside a type argument and kept the report (`quietly` keeps reports when its function succeeds), so five call-form run tests became PTS1001 `values of type T` in the suite on its snapshot (`snap6`); `literalFree` now rejects non-object types before asking for IR types, and `isDynamicIRType` takes back any report. New and extended tests: `generic_narrowed_stale_narrowings.pts` (indexed-access stale narrowings), `generic_dead_branches_shared_instances.pts` (nested `Json`), `generic_narrowed_dead_spreads.pts`. `i01` (an honest `v is string[]` on a `number[]`) is PTS1001 as on the base: C227 (H09b4b). The 91 probes of both rounds re-run with this code: 69 match Node; the other 22 are the intended lies (`l09`, `o02`, `o04`, `o06`, `e04`), print what the base prints (`l06`, `l10`, `n03`, `s02`, `s07`, `s07b`, `s18`, `s19b`, `a11`, `e06`, `i01`, `u07`, `u09`), hit the existing stale-narrowing checks as plain code does (`a02`, `a03`), or show the pre-existing TDZ gaps in code the base rejected (`u06`, `u08`).

## Round 3 (snapshot `/tmp/h09b4/snap7`, commit `87f407d6a`)

Archive: [h09b4-review3-probes.tar.gz](h09b4-review3-probes.tar.gz) (sha256 `20576b4e5ec56b244dc16da9d39afd84ef2143d51100d757f0f9edd8045604e1`; `round3/INDEX.txt` inside).

---

**Round 3 verdict: not ready, but only for one narrow regression the round-3 fix introduced (R3-F1), which is a small fix.** Every round-2 finding is fixed. I found no remaining way for a program without a lie to reach the trap, and no new IR-sharing hole.

**Earlier probes on snap7:** 69 of the 91 match Node; the other 22 are intended lies (`l09`, `o02`, `o04`, `o06`, `e04`), base-identical (`l06`, `l10`, `n03`, `s02`, `s07`, `s07b`, `s18`, `s19b`, `a11`, `e06`, `i01`, `u07`, `u09`), the existing stale-narrowing check (`a02`, `a03`) or pre-existing TDZ gaps (`u06`, `u08`). `a04`, `a05`, `a06`, `a09`, `a10`, `x01` and `x04` now match Node.

**R3-F1 — Medium, introduced in round 3. `literalFree` asking `isDynamicIRType` about nested types has side effects on the program.** It asks about every nested object or union, and visits the type arguments of interface references; for a phantom type parameter (one no property uses) the base never asks for that type's IR type, and asking makes class instances the program never uses. `quietly` takes back the reports made during the call, not a class instance; when that instance's code doesn't lower, the error is reported later and the program stops compiling. Repros: `r3/d01_phantom_tag_failing_class.pts` (`interface Tagged<Tag> { v: number }`, `t: Tagged<Weird<string>>`, `Weird<T>.isOne()` does `this.value == 1`): Node and base `other number true`, snap7 PTS1001 `the == operator on string and number`; `r3/d05_phantom_lib_weakref.pts` (the same through a type alias property): the same; `r3/d02_phantom_tag_code_growth.pts` (`Ref<Box<string>>`): output as Node but an unused `Box<string>` class instance, 130 → 178 lines of C. Direction: handle class references before asking, don't visit non-class references' type arguments, or only ask for parts the argument's own IR type covers. Nit: `isDynamicIRType`'s `|| l.reports != reports` is always false.

**What held up:** R2-F1 (`mentionsParameters`; non-intersection narrowed types can't be stale-dead; `r3/s20`, an unset object field typed T, hits D-141's check as on the base); R2-F2 (nested `Json` in function types, properties, class type arguments, tuples; `d03` a phantom union is taken back, PTS1002 as on the base; `d04` overloads PTS1001 as on the base; `d06` no report, same C); R2-F3 (`i01` PTS1001 as on the base, C227); R2-F4 (`x01`, `x04` as Node, `e05`'s literal spread still exempt); 937 corpus and run programs: 915 identical C, the 22 that differ are the new and `generic_*` tests and three never-typed element reads, none goes from compiling to failing, AllInstances-only 0 regressions and 13 improvements, and the phantom side effect occurs nowhere in the corpus; the six new or extended tests agree with their expect blocks; docs (D-195's matrix figures match v5 and v7; SPEC §8, §9.134 and GUIDE match the code). "With R3-F1 fixed, I know of no counterexample left to 'only a lie can reach it'."

### Parent's response to round 3

R3-F1 fixed by making `literalFree` structural (`literalFreeIn`): it asks no IR types; dynamic shapes are recognized by the checker's predicates (promises, iterators, timers, `{}`), and a union reached again while it is looked into (`Json`) counts as dynamic; only the type argument itself, whose IR type its instance already made, is asked (`isDynamicIRType`, with the nit fixed). d01, d02 (130 lines of C, as on the base) and d05 match Node; `generic_dead_branches_shared_instances.pts` gains d01's shape (RED on snap7). All 97 probes of the three rounds re-run: 73 match Node; the other 24 are as classified above, plus `d03`, `d04`, `s20` printing what the base prints.

## Round 4 (snapshot `/tmp/h09b4/snap8`, commit `51119cce9`)

Archive: [h09b4-review4-probes.tar.gz](h09b4-review4-probes.tar.gz) (sha256 `a223057c7602f50e129e010b98929b6c47f076903ff95b6fdb99d0892640bc42`; `round4/INDEX.txt` inside).

---

**Round 4 verdict: not ready.** The R3-F1 fix works, but making `literalFree` purely structural loosened one case: a union that recurs only through an index signature (a hash-map object type) now counts as literal-free. That brings back the round-2 F2 trap, with no lie in the program. It's a one-line fix.

The 98 earlier probes on snap8: 73 match Node; `o02` (an intended lie) is the only one where the base matches Node and snap8 doesn't; the other 24 differ from Node on the base too. Against snap7 only `d01` and `d05` changed (both now match Node); `d02`'s C is byte-identical to the base's.

**R4-F1 — Medium, introduced in round 4.** `type J2 = string | number | { [k: string]: J2 }` recurs through a hash-map object, so its IR type is the dynamic value's (D-089); `literalFreeIn` walks type arguments, properties and call signatures but not index signatures, never meets J2 again, and counts it literal-free, so `{v: J2}`, `() => J2`, `Box<J2>` and `[J2]` share unknown's instance tested as if literal-free. Repros `r4/h04`–`h07`: Node and base print the live branch (`unset object`, `undefined true`, `empty box object`, `holds undefined object`); snap8 throws the trap. Direction: walk the value types of index signatures (`GetIndexInfosOfType`); stays structural.

**What held up:** the R3-F1 fix (phantom type arguments create nothing); nested branded primitives, `object`, `{}`, promises, iterables and iterators and unions holding them, unions recurring through arrays, maps or function types, `Map`/`Set` of dynamic values, `Readonly<>`/`Partial<>` stay dynamic or own-IR; library class-likes keep their IR types nested (`ArrayBuffer`, `Error`, `Date`, `RegExp`, `Set`, `Int32Array`, `Map`; `Object`, `Number`, `Boolean`, `WeakMap`, `WeakRef`, `Function` are PTS1001 on both); index signatures with literal or dynamic values and `Ctx` recurring through arrays in a record (`r4/h01`–`h03`) reduce to nothing dead, PTS1001 as on the base; recursive interfaces (`Tree`, `Node`, `Expr` in `r4/c06`) get no traps and the base's PTS1001s; 937 corpus and run programs: emit-c 915 same / 22 different (the same set; only the shared-instances test's C changed since snap7), AllInstances-only 0 regressions, 13 improvements. Outside this slice: `corpus/functional/transducers` emits different C from run to run on the base itself (a second field-order variant of an object-literal shape; `scratch/t.*.c`).

### Parent's response to round 4

R4-F1 fixed as suggested: `literalFreeIn` walks index signatures' key and value types. h04–h07 match Node; `generic_dead_branches_shared_instances.pts` gains the shape (RED on snap8). All probes of the four rounds re-run: 77 match Node; no earlier probe's output changed; `c06` and `h01`–`h03` print the base's PTS1001s (recursive or index-signature type arguments get no traps, which D-195 now states). The `transducers` nondeterminism is the known C85 (map order).
