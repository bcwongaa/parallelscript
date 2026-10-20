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
