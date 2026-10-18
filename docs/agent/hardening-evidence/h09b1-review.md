# H09b1 review reports (opus-reviewer, claude-opus-5-5, xhigh)

Saved by the parent from the reviewer's final messages, unedited below each rule. Round 1 reviewed the byte-checked snapshot `/tmp/h09b/snap1` against the base worktree `/tmp/h09b/base/tree` (`64564a9a2`); its probes are [h09b1-review-probes.tar.gz](h09b1-review-probes.tar.gz) (`INDEX.txt` inside; paths below are relative to `/tmp/h09b1-review/`).

## Round 1

---

# H09b1 review, round 1 (opus-reviewer): snapshot `/tmp/h09b/snap1` against base `/tmp/h09b/base/tree` and Node v24.21.0

## Summary
- **Slice goal: met.** The four new run tests match Node on the snapshot. So do the reproducer's inventory probes, apart from the known C-a `?.` case. The first three lines of `generic_method_factories.pts` now pass.
- **Safety: no memory-, thread- or GC-safety problems found.** Both the runtime lookup and the `self` cast in the entries hold up.
- **D-155:** lies through `any` still throw.
- **D-101:** dispatched calls behave the same as on the base.
- **Code size and compile time:** unchanged for ordinary programs. I emitted C for all 872 run/corpus programs with AllInstances alone (no fallback). Exit status is the same as the base for every program. C size changed in only 8 files: 7 tests that use generic interface or literal methods, and `corpus/functional/option_maybe.pts` (+422 lines).
- **Findings:**
  - Two medium regressions push programs that worked on the base onto the whole-program fallback (F1, F2).
  - One medium behaviour change on the erased path (F3).
  - One medium per-call performance regression (F5).
  - Two low issues (F4, F6).
  - Test-quality and documentation notes follow the findings.

## Findings

### F1 · Medium · verified: converting arguments to the resolved parameter types can now fail with PTS1001, so the build falls back and loses its H09a instances
- **Where:** `pts/lower/instances.go:283`. `instanceCall` runs `typedArguments(call.Arguments.Nodes, d.want.Params)` for every named demand. The demands come from `namedInstanceCall` (`instances.go:361`), which is called at `objects.go:1076`, `objects.go:1099` and `union_objects.go:174`.
- **Mechanism:** On the base, an interface, shape or union call boxed its arguments to the erased copy's `any` parameters exactly as they were. Now each argument is first converted to the parameter type of the resolved signature. For a `Dog[]` where T is inferred as `Animal[]` (from the other argument, a `Box<Animal[]>`), that conversion is D-094's PTS1001 "using a Dog[] as a Animal[] where it may be changed or kept". The error happens only under AllInstances, so the build falls back to DeclaredInstances, and every H09a instance in the program is lost.
- **Repro:** `/tmp/h09b1-review/probes/p03_fallback_by_argument.pts`
  - base: `generic arrow (H09a) 6` / `keeper cat true`
  - snapshot: `generic arrow (H09a) TypeError Expected Box<number>, got Box<any>` / `keeper cat true`
  - Node: same as base
  - AllInstances alone: base compiles; the snapshot gives PTS1001 at 36:35 (`outputs/allinstances-alone.txt`).
- **Other forms:**
  - The literal's own-type call does the same (`p04_copied_argument_identity.pts`, PTS1001 at 31:33 and 33:36).
  - A tuple for `number[]` with explicit type arguments does too (`p02_arg_conversions.pts`).
- **Context:** `p03c_value_call_same_on_base.pts` shows that value calls (H09a) already report an honest PTS1001 in both modes on the base. What is new is that interface, shape and union calls now get the direct-call rules in AllInstances only.
- **Suggested fix:** Make the demand only if every argument converts to its `want` parameter without D-094 copies or reports. For example, lower the arguments in a sandbox and fall back to the base form if anything is reported.

### F2 · Medium · verified: by-name pairing reaches unrelated classes and literals, whose untakeable failures trigger the fallback (C176 made worse)
- **Where:** the `specialize` loop at `instances.go:404` pairs an interface or union demand with every class generic method of that name (`specializeMethod`). `reaches` at `instances.go:448` pairs it with every literal method of that name. Neither checks whether the class or literal can be the receiver.
- **Mechanism:** An unrelated `class Unrelated { make<T>(x: T) { new Weird(x)… } }` (never used as a `Maker`) gets a `make<string>` instance from `m.make("s")`. That instance makes `Weird<string>`, whose `typeof v === "number"` body doesn't lower, and a trial can't take that back. So AllInstances fails with PTS1001 at 13:14 and the program falls back.
- **Repro:** `p14_unrelated_by_name_fallback.pts` (class) and `p14b_unrelated_literal_fallback.pts` (literal)
  - base: `generic arrow (H09a) 6`
  - snapshot: `generic arrow (H09a) TypeError Expected Box<number>, got Box<any>`
  - Node: `6`
  - The interface call itself still throws in both trees.
- **Same class of trigger:** the reproducer's polymorphic recursion probes `i08` and `i09` (copied to `probes/`) compile under AllInstances on the base. On the snapshot they hit PTS9001 under AllInstances: `fromErased` gets a typed-nil `erasedTarget` and panics in `isCheckable` (`lower/dynamic.go:62`, stack via `DETAIL=1 allinst/ptsall_snap1`), after Box's 64-instance limit. The fallback hides it, at the same cost to H09a.
- **Suggested fix:** Pair an interface or union demand only with classes, and literals whose type, is assignable to the receiver's static type. This also cuts C172's speculative growth for this slice.

### F3 · Medium (behaviour change; decide and document) · verified: the erased path now gets converted arguments, so `any` is checked or copied and functions are adapted where the base matched Node
- **Where:** the same conversion at `instances.go:283`. `Otherwise` then receives `erasedArguments` of arguments that were already converted. This happens even when no instance exists:
  - the class's method makes no generic instances, so its entry has no function;
  - or the literal's method isn't registered at all.
- **Repros** (base vs snapshot; Node matches the base in each):
  - `p33_json_argument.pts`: `store.put<Config>(k, JSON.parse(…), entry)` through an interface.
    - base: `b true {"name":"a","size":2,"extra":true}` / `stored {"name":"a"}`
    - snapshot: `b false {"name":"a","size":2}` / `TypeError Expected {name: string; size: number}, got Object`
  - `p18_any_argument_checked.pts`: `any` for a class-typed T.
    - base: `cat` (interface and literal)
    - snapshot: `TypeError Expected Animal, got string`
  - `p26_erased_path_adapter_identity.pts`: a function with an optional parameter passed to `(x: number) => U`.
    - base: `1 true` / `2 true`
    - snapshot: `1 false` / `2 false`
- **Context:** `p33b_json_argument_direct.pts` shows that a direct call through the class already behaves like the snapshot on the base (D-071, D-155). So the change makes interface and literal calls consistent with direct calls, but it is undocumented and changes behaviour where the base was faithful.
- **Options:**
  - Convert to the `want` types only on the found-instance branch, and box the original values for `Otherwise`.
  - Or record the rule in D-191 and SPEC §8.

### F5 · Medium (performance) · verified: every call that can't find an instance pays a linear `find_field` with `strcmp`, with no site cache
- **Where:** `pts/runtime/src/object.c:172`. The field fallback runs for any `PTS_KIND_OBJECT`, class instances included. That contradicts the `pts.h:1042–1048` comment ("object is an object literal's").
- **Affected calls:**
  - Every typed call on a literal whose generic method makes no generic instances. Those calls used to be a direct field-closure call.
  - Every interface call on a class that implements the generic method non-generically. Those used to be a cached method lookup.
- **Repros** (5M calls each; `outputs/perf.txt`):
  - `p30_lookup_cost_no_instances.pts` (40 fields): literal 0.04 s → 0.92 s; plain class 0.02 s → 0.87 s.
  - `p30b_lookup_cost_3_fields.pts` (3 fields): literal 0.03 s → 0.15 s; class 0.02 s → 0.12 s.
  - callgrind, plain class: 276,016,527 → 1,176,016,697 instructions (+180 per call; 39.5% in `__strcmp_avx2`). Literal: 476,017,253 → 1,661,017,350 (+237 per call).
  - Calls that do find an instance on a 40-field literal cost about 200 ns (`p29_lookup_cost.pts`).
- **Suggested fix:** Cache the result per call site, keyed by descriptor (the field offset, or "no closure"), and/or limit the fallback to shape descriptors.

### F6 · Low · verified: dispatched and interface demands share a class entry, so the result depends on source order
- **Where:** `instances.go:586–587`. `specializeMethod` appends the entry before trying the instance and dedupes on (name, signature, key). The first demand to pair decides the entry. Dispatched demands instantiate by position; interface demands use inference.
- **Repro:** `Sub.make<T, U = string>` overrides an abstract `make<T>`.
  - `p32_entry_shared_with_dispatched.pts` (interface call first): snapshot `6 / 7 / 2`, same as Node. The dispatched call benefits too.
  - `p32b_dispatched_first.pts` (dispatched call first): the by-position entry has no function, so the interface call misses. Snapshot `TypeError … / TypeError … / 2`; Node `7 / 6 / 2`.
- **Impact:** not a regression (the base threw on all of these), but the same call depends on another call's position. Retry inference when an existing entry has no function, or use inference for dispatched demands too.

### F4 · Low · verified, gap: element-access calls keep the erased copy
- **Repro:** `p13_element_access_call.pts`. `quoted["make"](6)` and `m["make"](7)` still throw `Expected Box<number>, got Box<any>` (Node `7` and `8`).
- **Context:** `new ClassMaker()["make"](8)` throws on the base too, so this is a pre-existing element-access gap. It isn't listed among the known gaps, so it should be recorded.

## Notes
- **D-191:** the snapshot's code cites D-191, but the snapshot's `docs/DECISIONS.md` has no such entry. The parent's main tree is editing `DECISIONS.md` and `SPEC.md`, so it is probably pending.
- **Comment mismatch:** the `pts.h` comment for the field fallback doesn't match the code (see F5).
- **Test quality** (the new tests do pin the slice: wrong `this`, wrong env, ancestor-instead-of-override and erased-copy regressions would all fail):
  - Nothing pins the AllInstances gate. If it were removed, i08 and i09 would fail in both modes. A `needsFallback` run test (i08 or i09) or a driver test would guard it.
  - Nothing tests "a NULL entry ends the search" end to end through an interface. `p24_null_entry_ends_search.pts` works correctly today and could become one.
  - Nothing covers F1, F3 or F5 behaviour.
  - The unit test has no frozen-twin case.
  - In "captures a local", `counter` is a module global, not a capture. Real captures are covered elsewhere ("a factory's literal", "in a generic function's instance").

## Checked and found correct
- **The `self` cast is safe** (`p01`, `p06b`, `p07b`, `p16`, `p22`, `p22b`, `p09`, `p10`). A literal's raw method closure never leaves its object:
  - Dynamic reads, `Object.values` and property descriptors all give bound closures; spread, rest, `Reflect` and `freeze` are PTS1001.
  - A method field rewritten through `any` holds a bound or adapted closure with no named entries.
  - An object of another layout reached through `any` uses its own closure, so the instance runs on its real object.
  - Unnamed (H09a) and named entries never match each other's lookups.
- **Null receivers** throw TypeError rather than crashing (`p08`).
- **Field-read order** is pre-existing and identical to the base (`p05`).
- **Threads and GC:** the threads test is stable over 5 runs at `PTS_THREADS=8`. Fresh capturing literals made inside `.par` on 8 threads are stable over 6 runs and match Node's sequential output (`p23`). The lookup is read-only, and conservative stack scanning covers the closure local.
- **Language forms that match Node** (`p11b`, `p12c`, `p15`, `p17`, `p20`, `p21b`, `p25`, `p27b`, `p28`, `p34`, `p35`, `mm/`):
  - parameters: defaulted and optional parameters, fewer parameters than declared (`differs`), spread arguments and evaluation order, function arguments;
  - method kinds and names: async and generator methods, quoted names, `toString`, `valueOf`, `constructor`, intersections;
  - closures and recursion: captured locals written later (shared cells), recursion through `this`;
  - receivers: unions of literal shapes, and of a class and a literal; literals made in loops; nested literals; literals in class fields, static fields, getters and generic-class instances; contextually generic methods for generic interfaces; Error subclasses; literals from another module;
  - generics: overloads, rest parameters and extra defaulted type parameters among unrelated pairings;
  - D-155: lies through explicit `Box<any>` and cached `any` still throw.
- **Unsupported on both trees** (PTS1001 or PTS1002, no change): optional interface methods, computed method names, `this:` parameters, local classes, a class used as a value.
- **Fallback output is unchanged:** DeclaredInstances C differs from the base only in the entry convention and the lookup function (checked on `generic_instances_that_dont_lower.pts`).
- **Existing tests behave as on the base:** `dispatched_generic_methods`, `generic_factories`, `generic_tuple_rest_results`, `generic_methods_values`, `generic_methods_checks` and `option_maybe` all give the same output on both trees.

## Files
- Archive: `/tmp/h09b1-review/h09b1-review-probes.tar.gz` (80 entries; `INDEX.txt` maps each finding to its probes).
- Probes: `/tmp/h09b1-review/probes/`; saved three-way outputs: `/tmp/h09b1-review/outputs/`.
- Three-way runner: `/tmp/h09b1-review/cmp3.sh`.
- AllInstances-only emitter (built read-only through `go build -overlay`, nothing written to either tree): `/tmp/h09b1-review/allinst/main.go`.
- Corpus size comparison: `/tmp/h09b1-review/outputs/growth-summary.txt`.

I wrote nothing to either tree or to the main repository; the snapshot's and the base's `git status` are unchanged.

## Round 2

Reviewed `/tmp/h09b/snap3` (the parent's note about snap4's fix arrived after the round ended); probes [h09b1-review2-probes.tar.gz](h09b1-review2-probes.tar.gz) (`round2/INDEX.txt` inside).

---

# H09b1 review, round 2 (opus-reviewer): snapshot `/tmp/h09b/snap3` against base `/tmp/h09b/base/tree`, round-1 snapshot `/tmp/h09b/snap1` and Node v24.21.0

## Summary
- **All six round-1 findings are fixed or recorded.** I reran every round-1 probe against snap3 to check.
- **The rework holds up.** I found no memory-, thread- or GC-safety problems, no compile regressions, and no change in fallback output.
- **One new behaviour change (R2-1, low–medium, verified):** calls through a union of object types now check their result, so they can throw where the base matched Node. This answers the question you asked about the union conversion.
- **Two low-priority notes:** `mayReceive` adds compile time on big programs, and calls that omit an optional argument no longer find their instance (snap1 did).
- **Docs:** the snapshot's own D-191 is stale; the main tree's version describes snap3.

## Round-1 findings against snap3 (saved outputs in `round2/outputs/out_*.txt`)

| Finding | Probe | snap3 result | Status |
|---|---|---|---|
| F1 | `p03`, `p04`, `p02` | Same output as base and Node. AllInstances alone compiles `p03` (it gave PTS1001 on snap1). | Fixed |
| F2 | `p14`, `p14b` | Same output as base: the H09a arrow prints `6`. Both now fall back to ValueInstances, not to DeclaredInstances. | Fixed |
| F3 | `p18`, `p26`, `p33` | Same as base, which matches Node (identity kept, no check, `extra` kept). In `p18` only the direct class call still throws, as it does on the base. | Fixed |
| F5 | `p30b` (callgrind, 5M calls) | Literal 476M (base) → 1,661M (snap1) → 781M (snap3), +61 instructions per call. Plain class 276M → 1,176M → 466M, +38 per call. | Fixed |
| F6 | `p32`, `p32b` | Both source orders now give Node's `6 / 7 / 2`. | Fixed |
| F4 | `p13` | Unchanged, as expected. | Recorded as C185 |

- **Polymorphic recursion:** `i08` and `i09` still hit PTS9001 under AllInstances alone (C172's typed nil). ValueInstances compiles both, so the program keeps H09a's instances.
- **Other round-1 probes** (`p01`, `p05`–`p12c`, `p15`–`p17`, `p20`, `p21b`, `p25`, `p27b`, `p28`, `p34`, `p35`): unchanged from round 1, except `p25`. Function arguments with another parameter list now take the erased copy (same output as base). That is the intended effect of `passesAsIs`.

## New finding

### R2-1 · Low–medium (behaviour change, documented in the main tree's D-191) · verified: a union-of-objects call now checks its result where the base passed it on unchecked
- **Where:** `unionMethodCall` (`pts/lower/union_objects.go:174`) goes through `namedInstanceCall`. Under AllInstances, both the no-demand branch (`passed == nil`) and the `Otherwise` branch now wrap the erased call in `fromErased`. On the base, a union call returned the raw `Value`, and only a typed context converted it.
- **When you can see it:** the result is used in a dynamic context (`console.log`, a property read on the result, a template literal, an `any` variable), and no instance runs. That happens when:
  - an argument doesn't pass as is, e.g. an `any` argument with an explicit type argument (no demand);
  - or the implementation lists no entry, e.g. a plain `make(x: any): any` (the `Otherwise` branch).
- **Repro:** `/tmp/h09b1-review/round2/probes/q02_union_result_conversion.pts`

| Line | base | snap3 | Node |
|---|---|---|---|
| `console.log(pick(0).make<number>(a))` | `Box { value: 5 }` | `TypeError Expected Box<number>, got Box<any>` | `Box { value: 5 }` |
| `pick(0).make<number>(a).value + 1` | `6` | `TypeError …` | `6` |
| `` `${pick(2).make(7).value}` `` (plain implementation) | `7` | `TypeError …` | `7` |
| the same call through one interface | `TypeError …` | logged | logged |

- **Not visible in typed contexts:** there the base's own conversion applies the same check.
- **Consistency:** in the ValueInstances and DeclaredInstances fallback builds, union calls keep the base's dynamic result. So whether such a line throws depends on which fallback level the build ended at.
- **Options:**
  - Keep union calls dynamic on the erased paths: return the base expression when `passed == nil`, and give the union `InstanceCall` a `Value` result that boxes the instance's typed result.
  - Or keep the check (it makes union calls consistent with interface calls) and say in D-191 that a dynamic use of the result can now throw where the base and Node didn't.

## Notes (low)
- **N1 · `mayReceive` compile time.** It recomputes `classType` and runs `IsTypeAssignableTo` for every class, for every (demand, same-named method) pair. That costs demands × methods × classes, with no memoization.
  - It also builds a fresh `append(l.classList, l.instanceClasses...)` slice per call. If `classList` has spare capacity, that writes into its backing array; it's harmless today, but `slices.Concat` would be safer.
  - Measurements (AllInstances alone):

| Probe | base | snap1 | snap3 |
|---|---|---|---|
| `q09` | 0.98 s | 1.04 s | 1.02 s |
| `q14` (100 implementers, 1,500 generic classes, 500 calls) | 4.23 s | 4.32 s | 5.09 s |
| `q14b` (same, 1,500 calls) | 45.7 s | 46.1 s | 56.2 s |

  - The +10 s on `q14b` is `mayReceive`. The base's own 45 s is pre-existing and superlinear, and not part of this slice.
  - Suggested fix: memoize by (receiver type, class), and compute `classType` only after the `IsSubclassOf` test (today it runs first, in the `if` statement's initializer).
- **N2 · Narrower coverage than snap1 (not a regression against the base).**
  - `o.maybe<number>()` with the optional argument omitted takes the erased copy and throws (`q10`). The omitted argument isn't an `ir.Box`, so `passedAsIs` returns nil. snap1 found the instance.
  - All the other forms I tried still find their instance and match Node (`q01`, 18 forms): literals, object, array and tuple arguments, inline arrows, named functions of the exact type, upcasts, interfaces, unions, enums, `null`, `undefined`, explicit `any`. So do the parameter shapes `T[]`, `Box<T>`, callback, `Map<string, T>`, `[T, T]` and an optional argument that is given (`q10`).
- **N3 · Docs.** The snapshot's `docs/DECISIONS.md` D-191 still describes snap1: "any class", "the fallback build makes none", `pts_method_instance_find` without a site. The main tree's D-191 covers `passesAsIs`, `mayReceive`, ValueInstances, the site cache and the union conversion. Make sure the main tree's version is the one that gets committed.
- **N4 · Fallback test coverage.** `TestCompilerFailsCleanly` (`fallbackFailure`) checks AllInstances alone and then DeclaredInstances, never ValueInstances. If a program that fails AllInstances also failed ValueInstances, it would still pass (H09a instances silently lost). Today the only differences between ValueInstances and the base's AllInstances are dispatched inference and the C emitter, and the corpus shows no such program. Still, a check that needs-fallback programs compile with ValueInstances would pin the new two-step guarantee.
- **N5 · Pre-existing, unrelated:** the C for `corpus/functional/transducers.pts` is non-deterministic on both trees. In about 1 of 6–30 runs a second property order of `object1` appears, with `pts_object_take_order`; the base once gave 4,636 lines instead of 4,620. Possibly a source of flaky output.

## Checked and found correct
- **Corpus (874 run/ and corpus/ programs, each mode alone):**
  - Exit status is the same in every mode (base All/Declared against snap3 All/Value/Declared).
  - snap3 ValueInstances emits the same C as the base's AllInstances, except the new overrides test (+40 lines: F6's instances) and the non-deterministic `transducers.pts`.
  - snap3 DeclaredInstances emits the same C as the base's, except the overrides test.
  - So dispatched inference changed no existing D-101 program. `q06` (explicit, reordered, phantom, callback, generic-base and generic-instance dispatched calls) gives the same output as base and Node.
- **`passesAsIs` edge cases (`q05`, `q11`):** union widening finds its instance. Library interfaces, an interface with a union-typed property, a tuple-to-interface and a class with an optional interface property make no demand, as on the base. I found no case where `passesAsIs` accepts a conversion that `convert` would copy, check or adapt.
- **The ancestor-entry hazard (`q04`):** a subclass not assignable to the receiver can't reach the call. PTS already rejects the conversion ("may refer to a X that holds tag as another type"), so a class `mayReceive` leaves out can't find its base class's entry.
- **Site cache:**
  - It's written only when the class chain has no entry. Each emitted call site has one (name, signature), so a cached "no entry" stays valid. A single-word relaxed atomic with a deterministic value per descriptor rules out torn or stale results. Offset 0 can't be a real field.
  - `q07` (one site, 7 receiver descriptors including a frozen class twin, `PTS_THREADS` 8, 1 and 2, 8 runs) is deterministic (`70000 1150000`). The base gives `70000 7000000`, where all 70,000 calls throw.
  - The unit test covers literal twins (`Object.freeze` of a literal with methods is still PTS1001).
- **Three-level fallback:**
  - When every level fails, the user sees the last level's ordinary error once (`q13`).
  - A 5,600-line erroneous program takes 0.29 s against the base's 0.25 s for its three lowerings (`q03`).
  - emit-c prints the first level that lowers (C176, as known).
- **Type-parameter receivers** constrained to an interface, in instances for an interface, a class and a literal's type, match Node (`q12`).
- **Unsupported on both trees:** class expressions and anonymous default-exported classes (`q08`, `mm2/`).
- **New and touched tests:** `generic_interface_methods_arguments`, `_overrides`, `generic_interface_methods`, `generic_object_literal_methods` and `dispatched_generic_methods` match Node. `generic_instances_that_dont_lower` (no-diff) matches its expect block, including the new interface lines. `generic_method_any_boundaries` gives the same output as base.

## Files
- Archive: `/tmp/h09b1-review/h09b1-review2-probes.tar.gz` (77 entries; `round2/INDEX.txt` maps each probe).
- Probes: `/tmp/h09b1-review/round2/probes/`. Outputs, including reruns of all round-1 probes: `/tmp/h09b1-review/round2/outputs/` (`growth-summary.txt`, `perf-and-compile-time.txt`).
- Three-way runner: `/tmp/h09b1-review/round2/cmp3.sh`. AllInstances, ValueInstances and DeclaredInstances emitter: `/tmp/h09b1-review/allinst3/main.go` (built with `go build -overlay`).

Nothing was written to the snapshot, the base or the main repository; snap3's `git status` is unchanged at 31 entries.

## Round 3

Reviewed `/tmp/h09b/snap5`; probes [h09b1-review3-probes.tar.gz](h09b1-review3-probes.tar.gz) (`round3/INDEX.txt` inside).

---

# H09b1 review, round 3 (opus-reviewer): snapshot `/tmp/h09b/snap5` against base `/tmp/h09b/base/tree` and Node v24.21.0, with snap1 and snap3 for history

## Summary
- **The round-2 fixes all check out.** R2-1, N1 and N2 work, `LeftOut` matches Node for every parameter type I tried, and dynamic union results behave correctly in typed places and around suspensions.
- **New: one high-severity finding (R3-1).**
  - Instances found by name read arrays and Maps that erased copies made as if they were typed.
  - This gives silently wrong numbers in honest programs with no `any` at all (7 → 7.5, 8 → 9). With a lie through `any` it is a segfault. The base gives Node's output, or a TypeError.
  - It has been there since snap1; I missed it in rounds 1 and 2.
  - The root cause is pre-existing: a runtime check gap that H09a already hits on the base. But H09b1 widens it to method calls that the base got right.
- **New: one low–medium finding (R3-2).** A FromUnion check now rejects a value the base and Node accept.
- **I can't recommend ticking H09b1 until R3-1 is fixed or explicitly accepted.**

## Earlier findings against snap5
- I reran every round-1 and round-2 probe (`round3/outputs/re_*.txt`, `rerun-summary.txt`):

| Item | Probe | snap5 |
|---|---|---|
| R2-1 | `q02` | Matches Node line for line. |
| N2 | `q10` | Matches Node, including the omitted optional argument. |
| F1, F3 | `p03`, `p04`, `p26`, `p33` | Same as base and Node. |
| F2 | `p14`, `p14b` | Same as base. |
| F6 | `p32`, `p32b` | Matches Node. |
| D-101 | `q06` | Same as base and Node. |

- **N1** is in the code (memoized per receiver and class).
- **New since snap3:** `p20` (tuple spread arguments) now takes the erased copy, with base's TypeError. That's intended ("spread arguments make no demand"), but snap1 and snap3 found those instances.
- **Corpus** (875 run/ and corpus/ programs, each mode alone):
  - Exit status in All, Value and Declared modes matches the base everywhere.
  - Value and Declared C match the base except for the new tests, which changed source.
  - The one other size difference is the pre-existing non-deterministic `transducers.pts`.
- **The parent's probes:** `om2` regresses as recorded in C186 (`om`, `om3` as described).

## R3-1 · High · verified: typed instances read objects that erased copies made, giving wrong numbers or a crash

**Mechanism**
- H09b1 leaves some calls on the erased copy, and those calls make objects whose `T[]` arrays are `any[]` and whose `Map<string, T>` values are dynamic:
  - element-access calls (C185);
  - `?.` calls (C179);
  - method values (H09b2);
  - since snap3, calls with an `any` argument (F3).
- Such an object reaches the instance of another call found by name in one of two ways:
  - **Route A:** a union of object-literal types is a `Value` parameter, so it passes as is.
  - **Route B (snap5):** `passesDynamicAsIs` lets a dynamic value through to an unchecked `ir.ToUnion` (`convert`, `lower.go`: "a dynamic value tsc types as the union").
- The instance then reads it typed. `pts_field_address_slow` (`pts/runtime/src/object.c`) checks only `field->kind != check->slot`, so an `any[]` field read as `number[]` passes. `PTS_ARRAY_GET_WORD` then reinterprets the slots: numbers come back wrong, and a number read as a `pts_string *` crashes.
- On the base, these calls (for methods) all took the erased copy, which reads dynamically.

**Repros** (all under `/tmp/h09b1-review/round3/probes/`):

| Probe | What it does | base | snap5 | Node |
|---|---|---|---|---|
| `r15_honest_partial_coverage.pts` | Honest values, no `any`: an element access, an optional call or a method value makes the object, then a dotted call consumes it | `[7]` / `[8]` / `[9]` | **`[7.5]` / `[9]` / `[10]`** | same as base |
| `r08_shape_union_regression.pts` | Route A, with an `any` argument and with a JSON value | `[7]` / `[2]` | `[7.5]` / `[2.25]` | same as base |
| `r12_shape_union_map_field.pts` | Map field | Map `7` | Map `7.5` | Map `7` |
| `r12_shape_union_map_field.pts` | Nested object field | prints | TypeError "it is declared value" (C186 as written) | prints |
| `r14_shape_union_crash.pts` | Route A with a lie (`wrap<string>(raw)`, `raw = 7`) | prints `[7]` | **SIGSEGV (exit 139)** | prints `[7]` |
| `r02_part2.pts` | Route B: a union call's `any[]` into an interface call's instance, then `.length` | TypeError | **SIGSEGV** | prints |
| `r04_to_union_honest_numbers.pts` | Route B with honest numbers | TypeError | `5.5` | `5` |

- **History:**
  - `r15`: wrong on snap1, snap3 and snap5.
  - `r14`: TypeError on snap1, segfault on snap3 and snap5.
  - `r08`: correct on snap1, wrong on snap3 and snap5.
  - `r04`: wrong on snap1, TypeError on snap3, wrong again on snap5.
- **Pre-existing on the base, for context:**
  - `r02b`: the base segfaults through a declaration that takes a union call's `any[]` result.
  - `r07`: 8 reads as 8.5 on the base.
  - `r04`'s "base route" line gives 8.5 on the base.
  - `r16`: generic arrows (H09a) on the base read 8 as 9.
- **What H09b1 adds:** C186's text ("may … throw 'it is declared value'") understates it. Arrays and Maps don't throw; they are silently wrong or crash.

**Suggested fixes**
- **The real fix (also covers H09a):** make typed reads verify what they get.
  - `pts_field_address_slow` should compare the declared check for OBJECT-kind fields, not just the kind, and throw as `wrong_kind` does (or convert).
  - Array and Map extraction from a union (and `ToUnion`) should check the element kind.
  - That turns every row above into a TypeError at worst.
- **Minimum for H09b1 itself:**
  - Restrict `passesDynamicAsIs`'s union case to members with no array, Map or function anywhere inside.
  - Accept or record route A explicitly. It can't be fixed at compile time, because which calls fed a value isn't known statically.
  - If the runtime fix is deferred, file it as its own high-severity C-number with `r15` as the repro, and say in D-191 that H09b1 widens it.

## R3-2 · Low–medium · verified: a FromUnion check rejects an erased `Box<any>` that the base passed through
- **Repro:** `r10_checked_dynamic_argument.pts`. `console.log(pick(1).make(pick(0).make<number>(a)))` with `a: any`.
- **What happens:** the inner union call takes the erased copy and returns `Box<any>`, which tsc types `Box<number>`. `passesDynamicAsIs` sends it to the outer call's instance, which checks it.
  - base and Node: `Box { value: Box { value: 5 } }`
  - snap5: `TypeError Expected Box<number>, got Box<any>`
- **Impact:** a TypeError rather than corruption, and consistent with a direct call. It belongs to C186's family; worth one line there.

## N4 (pinning the two-step fallback in-tree)
A cheap structural pin: in `pts/lower/lower_test.go` (external package, so it works on the returned `*ir.Program`), lower the H09b1 run tests with `lower.ValueInstances` and assert two things.
- No `ir.InstanceCall` has a non-empty `Name` unless its `Otherwise` is a dispatched call (the `ClassCast` to an empty `ir.Interface`).
- No closure's `Instances` has a non-empty `Name`.

That pins "ValueInstances makes no demands by name and no literal-method instances", which is the part of the guarantee that can rot. The rest (ValueInstances' C equals the base's AllInstances) needs the base compiler, as you say.

## Checked and found correct
- **Dynamic union results** (`r01`, `r11`) match Node:
  - typed variable, parameter and return; nested and chained calls; union-typed variables and conditionals;
  - effects in order (`pick4,arg1,arg2`); arrays and spread;
  - `await` in the receiver, the argument and a promise result; `yield` inside an argument; a call used as a statement; `.par` on 6 threads.
  - `?.` on a union that may be undefined keeps the known C179 TypeError.
- **`LeftOut`** (`r09`, `r13`) matches Node:
  - a class, an object-literal type, an interface, a union, an array or a function parameter, left out and given;
  - a defaulted parameter left out and given `undefined`; two optionals with the first given;
  - through an interface and a literal's own type;
  - on the erased branch (no instance) through an interface, a literal and a union call.
- **Function and Map union members reached through instances** (`r05`) match Node, and the Map case even fixes a base miscount.
- **Honest strings and booleans** through route B (`r03`) come out right; only numbers corrupt.
- **Compile time and the fallback:** the corpus timing shows no differences.

## Files
- Archive: `/tmp/h09b1-review/h09b1-review3-probes.tar.gz` (99 entries; `round3/INDEX.txt` maps each finding to its probes).
- New probes: `/tmp/h09b1-review/round3/probes/` (`r01`–`r16`, `r02_part*.pts`).
- Outputs: `/tmp/h09b1-review/round3/outputs/`. That includes reruns of rounds 1 and 2, `earlier-snapshots.txt` with the history above, and `growth-summary.txt`.
- The snap5 CLI was built read-only to `/tmp/h09b1-review/round3/pts-snap5` with `go build -o`.

Nothing was written to snap5, the base or the main repository; snap5's `git status` is unchanged at 35 entries.

## Round 4

Reviewed `/tmp/h09b/snap6`; probes [h09b1-review4-probes.tar.gz](h09b1-review4-probes.tar.gz) (`round4/INDEX.txt` inside).

---

# H09b1 review, round 4 (opus-reviewer): snapshot `/tmp/h09b/snap6` against base `/tmp/h09b/base/tree`, snap5 and Node v24.21.0

## Summary
- **R3-1 and R3-2 are fixed.** Every round-3 repro now prints the base's output: `r15`, `r14`, `r08` and `r12` match the base and Node; `r02`, `r04` and `r10` match the base.
- **The restriction makes nothing worse than the base.** I compared every probe from rounds 1–4 line by line: there is no line where the base matches Node and snap6 doesn't. The corpus and the slice matrix show no regression either.
- **It does cost coverage (noted, not a defect).** Calls whose type argument is an interface or object type no longer find instances; details below.
- **The residual is acceptable for H09b1, with one correction to its D-191 wording.** There's also an optional cheap guard that would close its direct form.

## Fixes against snap6
- **All earlier probes rerun:** 66 probes from rounds 1–3 (`round4/outputs/re_*.txt`, `rerun-summary.txt`).
  - In every case where line counts line up, no line has `base = Node` and `snap6 ≠ Node`.
  - The R3-1 routes now give the base's output: `r08` 7 and 2, `r12` Map 7 and the object, `r14` prints `[7]` (no segfault), `r15` 7, 8, 9.
  - Route B (`r02` part 2, `r04`) and R3-2 (`r10`) are back to the base's TypeError.
  - R2-1 (`q02`), N2 (`q10`, `r09`), LeftOut on the erased branch (`r13`), F1/F3/F6 and `q06` are unchanged: correct.
- **Slice matrix:** the reproducer's 86 interface-method and object-literal-method cases give identical verdicts on snap5 and snap6: 26 OK (the 18 slice cases plus 8), 54 method-value TYPEERR (H09b3), 4 TSERR, 2 DIAG.
- **Corpus:** I emitted C with AllInstances alone for all 875 run/ and corpus/ programs. Exit status matches the base and snap5 everywhere. The only size change against snap5 is `corpus/functional/option_maybe.pts` (−352 lines, 14 → 2 instance lookups). It still matches Node.

## What the restriction drops (vs snap5), and whether it's worse than the base
- **Repro:** `round4/probes/s02_dropped_forms.pts`.
- **Lines snap5 matched Node on, and snap6 now gives the base's C169 TypeError:**
  - `repo.save(user)` with `user: User`, an interface;
  - `repo.save({ id: 2 })`, an object-literal type;
  - `saveAll([user])` (`User[]`);
  - `index(new Map([["a", user]]))` (`Map<string, User>`);
  - `maybe<User>(user)` (`User | undefined`).
- **Also dropped:**
  - `q01`'s object-literal and interface-type-argument forms;
  - `r01`'s nested union call `pick(0).make(pick(1).make(7))`: the inner call's result is dynamic, so it no longer passes;
  - `r05`'s new-route lines;
  - `option_maybe`'s `Option<T>` parameters.
- **Still found:** number, string, class, array-of-primitive and function arguments, so the matrix is unchanged.
- **None of these is worse than the base**, because each drop takes the base's erased path.
- **But the cost is real:** interfaces and object types are the usual way TS code models data, so until C187 is fixed H09b1 mostly helps calls with primitive, class or array type arguments.
- **Suggestion:** list these forms in D-191's known gaps, and add a skipped test (`s02` as written) to re-enable when C187 lands in H09b2.
- **On a narrower rule:** plain, non-union object types from an erased copy are converted correctly (a copy) at `fromErased` on both trees (`s03`), so the hazard really comes from unions of object types. However, a plain interface value can still lie when it was read from a union-narrowed erased object (nested `{ ids: T[] }`), which would widen the residual. So I'd keep snap6's conservative rule for H09b1 and lift it with C187.

## The residual (`r3/residual.pts`): my judgment

| Probe | base | snap6 | Node |
|---|---|---|---|
| `s00` (the parent's) | `first` TypeError / `typed read 8.5` | `first 7.5` / `typed read 8.5` | `first 7` / `typed read 8` |
| `s01` (my variant) | `Box { value: 7 }` / `[ 7 ]` | **`Box { value: 7.5 }`** / `[ 7 ]` | same as base |

- **What `s01` shows:** a variant where the base matches Node. The lying `number[]` (C187's typed declaration of an element-access call's `any[]`) is consumed by a call through a union of object types. That call has a dynamic result, so the base's erased copy read the array dynamically and printed correctly; snap6's instance misreads it.
- **D-191's wording needs a correction.** Its "where the base threw C169's TypeError" isn't always true; `s01` is a counter-example.
- **Why it's acceptable:**
  - Every residual case needs an upstream C187 lie already sitting in a typed variable. On the base, any typed reader of that value already misreads it; the instance is just one more typed reader, exactly like a plain typed function.
  - H09b2 (C187) fixes it at the source.
  - Neither a dynamic value nor a union-of-objects argument can reach an instance any more.
- **Cheap way to close the direct form (optional):** add a run-time guard in `instanceCall`.
  - For each argument whose `want` type is an array, Map or tuple, compare the argument's descriptor with the expected one. Arrays carry `pts_array_types[kind]` or a per-class array descriptor; an `any[]` has `pts_array_types[PTS_FIELD_VALUE]`.
  - On a mismatch, take `Otherwise`.
  - That's one pointer compare per such argument, and it routes `s00` and `s01` to the base's path.
  - It doesn't cover nested cases (a class instance whose array field holds the lie); those need C187.
  - If you take it, add `s01` as the test.

## Checked and found correct
- Rerun probes across rounds 1–3, beyond the fixes above: dynamic union results (`r01`, except the dropped nesting line and the known C179 `?.` line), LeftOut (`r09`, `r13`), constrained receivers (`q12`), the passesAsIs edge cases (`q05`), fallbacks (`p14`, `p14b`, `i08`, `i09`, `q13`) and boundaries (`p35`, `generic_method_any_boundaries`).
- New tests: `run/generic_method_erased_objects.pts` and `corpus/functional/option_maybe.pts` match Node.
- `takesUnverifiedObjects` maps erased parameters to declared ones correctly around Given flags. With `any` or `unknown` resolved types it allows the demand, since T=`any` reads dynamically. Union calls still find instances (their resolved signature has a declaration).

## Files
- Archive: `/tmp/h09b1-review/h09b1-review4-probes.tar.gz` (85 entries; `round4/INDEX.txt`).
- New probes: `/tmp/h09b1-review/round4/probes/` (`s00`–`s03`).
- Outputs: `/tmp/h09b1-review/round4/outputs/`. That includes the reruns, matrix results (`results-snap5.json`, `results-snap6.json`), `growth-results.tsv` and `s02_dropped_forms.snap5.txt`.
- The snap6 CLI was built read-only to `/tmp/h09b1-review/round4/pts-snap6` with `go build -o`.

Nothing was written to snap6, the base or the main repository; snap6's `git status` is unchanged at 37 entries.

## Round 5

Reviewed `/tmp/h09b/snap7`; probes [h09b1-review5-probes.tar.gz](h09b1-review5-probes.tar.gz) (`round5/INDEX.txt` inside).

---

# H09b1 review, round 5 (opus-reviewer): snapshot `/tmp/h09b/snap7` against base `/tmp/h09b/base/tree`, snap6 and Node v24.21.0

## Verdict
**Not ready to commit yet: one blocker (T1), and the fix is one line.** The new array guard reads `->type` of an array argument that was left out, which is NULL in the C.
- Release builds (`-O2`) quietly fold that undefined behaviour to "false" and drop the instance. A D-148 value call and a D-101 dispatched call that worked on the base now throw C169's TypeError.
- `-O0` builds segfault.
- PTS's sanitizer profile reports a null member access.

**Once the guard tests for NULL** (and `t03` is added as a test), I consider H09b1 ready to commit. The known gaps (C187 for H09b2, the forms it drops, C185, C179) are documented.

## T1 · Blocker · verified: the guard dereferences a left-out (NULL) array argument
- **Where:** `pts/cgen/classes.go`, `instanceCall`. The guard is written as `((const pts_object *)<arg>)->type->element == <fieldKind>`. For an optional `xs?: T[]` that the call doesn't pass, `<arg>` is the `LeftOut` value, which is `0`:
  - Interface call: the C reads `((const pts_object *)0)->type->element`.
  - D-148 and D-101 calls: the left-out value goes into a local first (`argument2`), and the guard reads `argument2->type`.
- **Repro:** `/tmp/h09b1-review/round5/probes/t03_guard_null_arrays.pts`

| Line | base | snap7 | Node |
|---|---|---|---|
| D-148 value call, array left out | `7` | **TypeError** | `7` |
| D-101 dispatched, array left out | `9` | **TypeError** | `9` |
| H09a arrow, array left out | `8` | `8` (the fold happens to go the other way here) | `8` |
| Interface call, array left out | TypeError | TypeError (snap6 gave `6`) | `6` |

- **The undefined behaviour itself** (`t03b_left_out_array_minimal.pts`; its emitted C and the commands are in `round5/outputs/t03-ub.txt`). I compiled snap7's C by hand against snap7's runtime archives:
  - `-O0`: segmentation fault (exit 139).
  - `-O1 -fsanitize=undefined`: "runtime error: member access within null pointer of type 'const pts_object'".
- **Fix:** emit `(arg == NULL || ((const pts_object *)arg)->type->element == kind)`. A NULL array argument means "left out", and the instance handles that. Add `t03`'s lines as a test; Node's output is above.

## Earlier rounds against snap7
- **69 probes from rounds 1–4, compared line by line:** no line where the base matches Node and snap7 doesn't.
- **Changes from snap6:**
  - `s00` now prints the base's output.
  - `s01` now matches Node.
  - `r09`'s "array left out" line regressed: that's T1.
- **Full 754-case H09 matrix on snap7, against the base's results** (`results-full-head.json`, 64564a9a2): 556 OK / 122 TYPEERR / 74 TSERR / 2 DIAG. Exactly the 18 slice cases (interface method and object method, `direct`/`explicit`) moved from TYPEERR to OK; nothing else changed. So no D-148 or H09a matrix case is affected.
- **Corpus** (875 run/ and corpus/ programs, AllInstances alone):
  - Exit status matches snap6 everywhere.
  - Only 5 programs' C contains an array guard: `generic_arrow_factories`, `generic_dynamic_results_nested_dynamic_values`, `generic_instances_keep_compiling`, and the two new tests.
  - None of them has a left-out array argument, which is why the suite doesn't catch T1.

## Guard false negatives (honest arrays that would lose a legitimate instance)
- **Coverage** (`t01`, `t02`): about 40 array sources, each through four kinds of demand: a D-148 declaration used as a value, an H09a generic arrow, a D-101 dispatched method and an H09b1 interface call. The sources:
  - literals, pushed-onto empty literals, `new Array(n).fill`, `Array.from` with a mapper and of a Set, Set spreads;
  - `map`, `filter`, `slice`, `concat`, `toSorted`, `Array.of`, `flat`, `splice` results, `Object.freeze` (frozen twin);
  - `split` (match copies), `Object.keys`, `Object.values`, `match`, JSON arrays (`as number[]`), `pts:parallel`'s `par.map`;
  - arrays of booleans, unions, functions, Maps, classes, arrays, tuples, entries and `undefined`;
  - `i32[]`, `u8[]`, `int[]`, `f32[]` and integer `map` results;
  - a generic class's array field, Map value spreads, rest parameters and generator spreads.
- **Result:** no false negatives. Every source took the instance with Node's values, and every value, arrow and dispatched line equals the base. That fits the code: every runtime array descriptor I found (`pts_array_types[...]`, `pts_type_match_array`, `pts_type_match_copy`, cgen's `arrayDescriptor`) uses `fieldKind` of the element type, and frozen twins copy `element`.
- **Odd element types** (`t04`: `never[]`, `void[]`, `undefined[]`, `null[]`, string literal unions, enums): the C compiles (no empty kind) and the results equal the base or Node. The `never[]` RangeError is pre-existing on the base.
- **Thread safety and cost:** a single load and compare. Freezing swaps in a twin with the same `element`, and the match-copy compare-and-swap keeps `PTS_FIELD_STRING`.

## Checked and found correct
- The residual is closed: `s00` and `s01` equal the base or Node, and `s01` is in `run/generic_method_erased_objects.pts`.
- The skipped `run/generic_method_object_arguments.pts` has the right skip reason, and its expected output is Node's.
- D-191's "Array arguments" bullet describes the guard accurately.
- Once the NULL case is handled, the guard only changes behaviour where an array argument's elements aren't of the parameter's kind. Those are C187 lies, which now take the erased copy instead of being misread.

## Files
- Archive: `/tmp/h09b1-review/h09b1-review5-probes.tar.gz` (91 entries; `round5/INDEX.txt`).
- New probes: `/tmp/h09b1-review/round5/probes/` (`t01`–`t04`, `t03b_left_out_array_minimal.pts`).
- Outputs: `/tmp/h09b1-review/round5/outputs/`. That includes the reruns (`re_*.txt`, `rerun-summary.txt`), `results-full-snap7.json`, `guard-census.*`, `t03-ub.txt` and snap7's emitted C for `t03b`.
- The snap7 CLI was built read-only to `/tmp/h09b1-review/round5/pts-snap7` with `go build -o`.

Nothing was written to snap7, the base or the main repository; snap7's `git status` is unchanged at 39 entries.

## Round 6

A second session's new `opus-reviewer` instance (claude-opus-5-5, xhigh; the first session's reviewer couldn't be resumed across sessions) reviewed `/tmp/h09b/snap8`, the commit candidate's code; probes [h09b1-review6-probes.tar.gz](h09b1-review6-probes.tar.gz) (`INDEX.txt` inside; paths below are relative to `/tmp/h09b1-review6/`). The parent took N1 (the test's new lines; RED on snap7, GREEN on the final code) and N2 (SPEC §9.130) after the round.

---

**Verdict: ready to commit.** T1 is fixed. I found nothing that blocks the commit: one low test-quality point, one documentation nit, and a pre-commit checklist item for the evidence file.

## 1. T1 is fixed (checked by running it)
- **The guard now tests NULL.** `/tmp/h09b/snap8/pts/cgen/classes.go:974-975` emits `(%s == NULL || ((const pts_object *)%s)->type->element == %s)`. In t03b's C the interface call gets `(0 == NULL || ...)` and the value calls get `(argument2 == NULL || ...)`.
- **Round 5's probes now match Node** (`outputs/t03.txt`):

| Line | base | snap7 | snap8 | Node |
|---|---|---|---|---|
| D-148 value call, array left out | 7 | TypeError | 7 | 7 |
| D-101 dispatched, array left out | 9 | TypeError | 9 | 9 |
| Interface call, array left out | TypeError | TypeError | 6 | 6 |

  t03b's output on snap8 equals Node's. t03's last line, the non-null assertion on `undefined`, differs from Node on base and snap8 alike; it is pre-existing.
- **Undefined behaviour is gone.** I compiled snap8's C for t03b by hand against snap8's runtime archives three ways: `-O0`, `-O1 -fsanitize=undefined -fno-sanitize-recover=undefined`, and `-O0 -fsanitize=undefined,null`. All three exit 0, print Node's output and report nothing. `-Wall -Wextra` gives no warnings on the guard lines.
- **The new test passes.** `run/generic_instance_left_out_arrays.pts` with snap8's CLI prints exactly its expect block, which equals Node's output. The base fails its interface line.

## 2. The guard's argument runs once (checked by construction and by running it)
- **By construction:**
  - `lower.instanceCall` (D-148 value calls, D-101 dispatched calls, and D-190 arrows through `instanceValueCall`) binds every argument and the receiver to `argumentN` / `callee` locals.
  - `namedInstanceCall` (D-191 calls) passes either a local, a `LeftOut` (emitted as `0`), or `convert(LocalRef, want[i])`. For an array parameter that `convert` returns the local unchanged, because `passesAsIs` only accepts the identical array type and `convert` returns early when the types are equal.
  - The receiver is always `LocalRef{callee}`.
- **Running it:**
  - `u01`: array arguments with side effects (`mk()` calls, `i++`, getter reads, `[i++, i++]`, `??`, `?:`) through value, arrow, dispatched, interface, literal and union calls. snap8 equals Node, and the counters show each argument evaluated once.
  - `u02`: `await` and `yield` operands. All guard operands are locals.
  - `u05`: D-190 arrows reached as values (a `let`, a property, an array element, a parameter, a local), with the array left out and given. snap8 equals the base and Node.
- **Census:** I ran `emit-c` over 1,713 programs (run/, corpus/, the 754 matrix cases, all earlier probes). It found 765 guards in 615 programs. Every operand is a C identifier or `0`, and the same on both sides of `||`.

## 3. Earlier probes rerun on snap8
- **72 probes** (rounds 1–4, plus t01, t02, t04): snap8's output equals snap7's on 71. The one change is `r09` line 14, which went from snap7's TypeError to Node's `6`. There are 0 lines where the base or snap7 matched Node and snap8 doesn't.
- **Full H09 matrix:** 556 OK / 122 TYPEERR / 74 TSERR / 2 DIAG. The verdicts and the PTS output are identical to round 5's snap7 results.

## 4. The snap7 → snap8 delta and findings
The delta is the guard line, the D-191 and SPEC wording, the new test and the evidence file. Nothing in it is wrong.

- **N1 · Low · test quality (checked).**
  - **The arrow line isn't an instance call.** The new test's "H09a arrow, array left out" line compiles to a direct call, `pts_fn_arrowFirst_number(7.0, 0, false)`, so it never reaches a guard (see `emitted/newtest_copy.snap8.c`). That is why snap7 "passed" it.
  - **Some forms snap7 broke aren't pinned.** The test has no object-literal line and no explicit-`undefined` line. snap7 broke all of these, and snap8 fixes them (`outputs/u04_*.snap7.txt`, `outputs/u05_*.snap7.txt`):
    - `lit.opt(x)`;
    - `optValue(x, undefined, t)`, and the same with `undefined` through `d.opt`, `o.opt` and `lit.opt`;
    - arrows reached as values with the array left out.
  - **Why it isn't blocking:** every kind shares the one emitter line, so the D-148 and D-101 lines catch a regression of it.
  - **Suggestion:** call the arrow through a value (for example `const viaValue: <T>(x: T, xs?: T[]) => Box<T> = arrowFirst;`), and add a literal line and an explicit-`undefined` line.
- **N2 · Nit · docs.** In `docs/SPEC.md`, §9.130's "IR and C" bullet still says "takes any instance only if each array argument's elements are of the parameter's kind". It doesn't mention the left-out (NULL) case that D-191 now states.
- **N3 · Pre-commit checklist.** `docs/agent/hardening-evidence/h09b1.md` still contains the placeholders `FINAL_GREEN`, `INSERT_IFACE` and `REVIEW_ROUNDS`. It also links `h09b1-baseline.jsonl.gz` and `h09b1-parent-probes.tar.gz`, which don't exist yet. I assume that's expected while the suite runs; fill them in before committing.
- **Not regressions** (base and snap8 behave the same, and D-191's known gaps cover all but the last):
  - `oq?.pick(...)` on a receiver typed `Opts | undefined` (C179, H09b3);
  - an interface or literal call whose argument is a `yield` operand (a dynamic argument takes the erased copy);
  - rest parameters (H09b3);
  - a `number[] | undefined` variable passed to `xs?: T[]` throws C169's TypeError. For the D-148 and D-101 calls it is a pre-existing PTS1001, "converting boolean to given".

## Commands
```sh
cd /tmp/h09b/snap8 && go build -o /tmp/h09b1-review6/pts-snap8 ./cmd/pts
/tmp/h09b1-review6/tools/cmp3.sh <probe.pts>    # base / snap8 / Node, with PTS_CACHE_DIR=/tmp/h09b1-review6/cache-*
PTS_HOME=/tmp/h09b/snap8 /tmp/h09b1-review6/pts-snap8 emit-c ub/t03b.pts -o ub/t03b.snap8.c
clang -O0 -g -std=c11 $INC t03b.snap8.c $ARCHIVES -lpthread -ldl -lm -static-libgcc    # also with -fsanitize=undefined variants
xargs -P 3 -I{} tools/snaponly.sh outputs/rerun {} < outputs/rerun-list.txt; python3 -I tools/compare_rerun.py
python3 -I tools/run.py matrix/cases outputs/results-full-snap8.json 3; python3 -I tools/matrix_compare.py
xargs -P 2 -I{} tools/census1.sh {} < census/list.txt
```
Here `$INC` is snap8's runtime include directories and `$ARCHIVES` is the six archives in `/tmp/h09b1-review6/cache-snap8`.

Nothing was written to snap8, the base or the repository. snap8's `git status` is still 42 entries, and its tracked diff is byte-identical to `snap8.patch`.

## Files
- Archive: `/tmp/h09b1-review6/h09b1-review6-probes.tar.gz` (121 entries, sha256 `9f10f3d3…7d9e36`; `INDEX.txt` inside).
- New probes: `/tmp/h09b1-review6/probes/u01_guard_side_effect_args.pts`, `u02_guard_await_yield_args.pts`, `u03_optional_chain_receiver.pts`, `u04_guard_rest_and_optional_args.pts`, `u05_guard_arrow_values.pts`.
- Outputs: `/tmp/h09b1-review6/outputs/` (`t03.txt`, `new_test.txt`, `rerun-summary.txt`, `matrix-compare.txt`, `results-full-snap8.json`), `/tmp/h09b1-review6/census/clean.txt`, `/tmp/h09b1-review6/emitted/`, `/tmp/h09b1-review6/ub/t03b.snap8.c`.
