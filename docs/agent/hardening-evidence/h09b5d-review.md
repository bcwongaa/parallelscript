# H09b5d review

The `reviewer` subagent's reports (claude-opus-5-5, effort xhigh), saved verbatim by the parent. Round 1 reviewed implementation commit `ebf0f8bb1` (base `fd90928f0`) read-only with pinned binaries `/tmp/h09b5d/pts-{base,v1,final}`; round 2 (the same reviewer, resumed) reviewed `011c7f6a8` (`/tmp/h09b5d/pts-final2`) and the parent's uncommitted `instanceKey` change (`pts-r3`); round 3 reviewed `fb68242c6` (`pts-final3`). Probes and outputs: [h09b5d-review1-probes.tar.gz](h09b5d-review1-probes.tar.gz), [h09b5d-review2-probes.tar.gz](h09b5d-review2-probes.tar.gz), [h09b5d-review3-probes.tar.gz](h09b5d-review3-probes.tar.gz). The parent's checks and dispositions are in [h09b5d.md](h09b5d.md).

---

## Round 1

### H09b5d review, round 1 (commit `ebf0f8bb1` against base `fd90928f0`)

## Verdict: NOT READY

The six RED tests and the two new tests are fixed, and the `narrowedInstance` change holds up. But splitting generic classes breaks conversions that TypeScript allows and that compiled and matched Node on the base. That covers common patterns: a `get<K extends keyof T>(k: K): T[K]` store, a typed event emitter, a `JSON.parse` value going into a typed parameter, `Awaited<T>`, `NonNullable<T>` and `Partial<T>` members. Splitting functions by checker type also brings a polymorphic-recursion regression, phantom class instances, and a large build-time cost for Redux-style dispatchers.

Every result below was run with `/tmp/h09b5d/pts-{base,final}` and Node. The probes and their `.out` files are in `/tmp/h09b5d/review1/h09b5d-review1-probes.tar.gz`, under `p/`, with `run.sh`.

## Findings, most severe first

### F1 (High, regression): TS-legal conversions between class instances that are now split are PTS1001 or a TypeError

A class is split wherever a computed member's IR type differs. Nothing converts between two instance classes of one generic: you get `using a X as a X`, or a failing checked conversion. Any type argument whose IR type is dynamic (`any`, `unknown`, `Promise<…>`, `Json`) gives a different computed member than the others, and so does a literal under a distributive conditional. TypeScript accepts all of these programs.

| Probe | Pattern | Base | Final | Node |
|---|---|---|---|---|
| `c01_awaited_widen_unknown` | `Task<Promise<number>>` → `Task<unknown>`; member is `Awaited<T>` | OK | PTS1001 `using a Task as a Task` | OK |
| `c02_any_to_awaited` | `new Task(JSON.parse("1"))` passed to a `Task<Promise<number>>` parameter | OK | PTS1001 | OK |
| `c03_any_unknown_conditional` | `Holder<unknown> = new Holder<any>(1)` | OK | PTS1001 | OK |
| `c05_erased_copy_any` | an erased copy called through `any` makes `Task<any>`; a typed `Task<Promise<number>>` takes it | OK | `TypeError: Expected Task<Promise<number>>, got Task<any>` | OK |
| `c06_partial_any_unknown` | `Partial<T>` member; `Store<any>` → `Store<unknown>` | OK | PTS1001 | OK |
| `c08_nonnullable_unknown` | `NonNullable<T>` (`T & {}`); `Promise` or `any` → `Cell<unknown>` | OK | PTS1001 | OK |
| `c09_literal_to_wide_conditional` | `Holder<true>` → `Holder<boolean>` (`R<boolean>` is a union) | OK | PTS1001 | OK |

The control `c05b` (same program with `result: T`, no computed member) is still OK on final.

- **Root cause:** `generic_classes.go:172-189` splits whenever `classMembersIdentity` (`:231`) differs. `memberPart` (`:354`, `:366`) writes `any` and `unknown` as `"value"` while `Awaited<any>`, `Partial<any>` and `NonNullable` resolve to other parts, and it never asks whether one instance type is assignable to the other.
- **Possible fix:** keep instances in one class wherever `IsTypeAssignableTo` holds in either direction (as the base did), and split only types that can't convert, like the motivating `Holder<1>` and `Holder<number>`. Alternatively, give computed members the widest IR type of their instances. `any` needs its own rule either way.

### F2 (High, regression): types left unresolved under a method's own type parameter split structurally identical type arguments

`T[K]` with `K extends keyof T` (a method's own parameter), and likewise unresolved conditionals and mapped types, stays generic after instantiation. `memberPart` then writes it by checker type id (`generic_classes.go:399-400`, `t%d`). `Point[K]`, `Pt[K]` and `{x,y}[K]` get different ids, so `Store<Point>`, `Store<Pt>` and the literal-inferred `Store<{x:number;y:number}>` become three classes.

| Probe | Base | Final | Node |
|---|---|---|---|
| `k01_store_get_indexed` (`new Store({x:1,y:2})` passed to `sum(s: Store<Point>)`) | `3 7 11` | PTS1001 at 21:17 and 22:30 | `3 7 11` |
| `k02_store_interface_alias` | `11 7 / 2 18` | PTS1001 | `11 7 / 2 18` |
| `k03_store_controls`: `get(k: keyof T): T[keyof T]` | OK | OK | OK |
| `k03_store_controls`: typed `Emitter<Events>` with `on<K extends keyof Events>` | `ready 42` | PTS1001 `using a Emitter as a Emitter` | `ready 42` |

This contradicts `memberPart`'s own doc ("the same for types whose IR types are the same"). Fix: write unresolved types by their structure (object part plus index part, check/extends/branch parts) rather than by id.

### F3 (High, cost): function instances now multiply with every checker-distinct type argument, even where nothing computes from it

`typeArgumentsIdentity` keys on keyPart, so literal-typed properties, brands and promise contents each get an instance. That happens even when the signature and body compute nothing from them; classes, by contrast, use `computesTypes`.

`g03`/`g06` is a 25-line `dispatch<A extends Action>(a: A): A`, which is Redux's `Dispatch` shape, called with N action literal types. Output is identical everywhere.

| N | C lines (base → final) | `pts run` time | Max RSS |
|---|---|---|---|
| 40 | 294 → 5,520 | ~1–5 s → 11–15 s | 101 → 256 MB |
| 200 | 615 → 27,281 | 0.86 s → 66.3 s | 104 MB → 1.02 GB |

`g01` (direct calls) grew 1,448 → 2,111 lines. Calls through values (`g02`, `g04`, `g05`) did not grow. The numbers are in `p/growth.txt`. These numbers need to be either mitigated (split only where the declaration's types compute from the type parameter, or where literals and brands are actually inspected) or recorded as a cost in the decision.

### F4 (Medium, regression): polymorphic recursion through a dynamic wrapper no longer converges

- `r01`/`r04`: `nest<T>` calls `nest<Promise<T>>`, with a `T[]` or `{v:T, n}` result. Base `1 1` / `3 100`; final PTS1001 `polymorphic recursion more than 64 instances deep`; Node `1 1` / `3 100`.
- `r02` (number result) runs, but C grows from 68 to 1,832 lines (64 instances). `r03` (through a value) grows from 283 to 1,039 lines.
- **Root cause:** once the walk passes `maxKeyDepth` or the budget, `keyPart` (`instances.go:260`) falls through to `irTypeKeyPart`, which writes a dynamic value by its checker type id (`:364`, `",v%d"`). That contradicts the comment at `:242` ("by its IR type alone, so a dynamic value deeper than that isn't told apart"). With `maxDynamicVariants` gone, nothing bounds it short of the depth limit.
- **Fix:** in identity mode, write the cut-off part as its IR type alone, so the walk converges after about 8 levels.

### F5 (Medium, regression, same class as H09b4's R3-F1): phantom class instances

`generics.go:1224` computes `typeArgumentsIdentity` for every new instance. Its `objectKeyParts` → `irTypeKeyPart` calls `irType` on object types nested inside promises, which makes their shapes and the class instances those shapes' fields name. Restoring reports doesn't undo a class instance.

- `ph03_phantom_single_call` (`keep<Promise<{ w: Weird<string> }>>(0)`, where `Weird<string>.isOne` does `this.value == 1`): base `kept true`; final PTS1001 `the == operator on string and number`; Node `kept true`. `ph02` fails the same way.

### F6 (Medium, C177 still open, not regressions; worth recording as known gaps)

All of these fail on both base and final:

- **`u01`:** a member typed through a generic interface that computes from T (`w: Wrap<T>`). `computesTypes` only looks at a reference's type arguments (`generic_classes.go:288`), and `memberPart` writes references by target and arguments, so `Wrap<1>` and `Wrap<number>` look equal.
- **`m03`:** the superclass's type argument is computed (`extends Base2<Awaited<T>>`): `Sub2<Promise<string>>` shares a class whose superclass is `Base2<number>`. In `m02` this is `TypeError: Expected number, got string`.
- **`u02`:** a class whose types are computed only in a method body.
- **`u07`:** the keyPart budget (256) is shared by all type arguments, so the twin second argument after a big first one shares an instance.

### F7 (Low): function-value identity diverges more

`f01`, base vs final vs Node:

| | `f === g` (two instance values) | Set size |
|---|---|---|
| Base | `true` | 2 |
| Final | `false` | 4 |
| Node | `true` | 1 |

`f === id` was already wrong on the base, so this only widens an existing divergence.

### F8 (Low): code quality (`RULES.md`)

- **Duplication:**
  - `typeArgument` (`generics.go:373-389`) repeats `instantiate`'s substitution switch; this is the second-instance rule, so extract it.
  - `memberPart`'s report and foreign rollback (`generic_classes.go:402-410`) duplicates `irTypeKeyPart`'s.
  - `memberWalk` duplicates `keyIdentity`'s walk state.
- **Unclear sentinel:** `identity == ""` means "not computed", but `""` is also a real class identity, so the identity gets recomputed through the cache on every loop pass.
- **Stale comments and docs:**
  - `dead_code.go:399` still says "Instances are told apart by IR types (a function's also by literal type arguments)".
  - `instances.go:242` contradicts `:364` (see F4).
  - `docs/SPEC.md:1616` still names `madeFor` and `maxDynamicVariants`.
  - `memberPart`'s doc has a broken line wrap ("a type that isn't" on its own line).

### Test quality

The unskipped and new tests are meaningful: both `generic_narrowed_instances_*` fail on `pts-v1` or the base and pass on final. The lower unit test passes and would catch the class or function identity being removed. Nothing covers the negative side: conversions between instances of one IR type with computed members (F1, F2), recursion through dynamic wrappers (F4), or phantom arguments (F5).

### Pre-existing, outside this slice

`u05b`: a dispatched (abstract or overridden) generic method returning `Box<Awaited<U>>` throws `Expected Box<number>, got Box<any>` even with a single call, on both binaries. The same method with plain `U` (`u05c`) is fine. It doesn't look tracked.

## Checked and fine

- **Function instances:** the generic method of a generic class (`u04`, which was PTS1001 on base) and the nested generic arrow under `outer` substitutions (`u06`) are fine. Through an interface or literal (`u05`) the first line works; the rest fails as above.
- **Structurally identical non-generic classes chosen by a conditional (`u03`):** now correct (base was PTS1001).
- **Twin discriminants with narrowing and lies, wide type first (`w01`):** same as Node.
- **Brands:** maps, sets, records and arrays keyed by brands, through generic functions and classes (`b01`), are fine.
- **Split class instances:** computed members in `#private` fields, accessors and constructor parameters split correctly (`m01`). Statics stay shared, and `instanceof` and inheritance from split instances work (`s03`). An `unknown` narrowed by `instanceof` to a split `Task<any>` reads correctly (`c10`).
- **`narrowedInstance` (`n01`):** lies reaching the `Array.isArray`, `typeof number/function/object`, `in` and `instanceof` branches of a `Promise<number>` instance all match Node (base and v1 were PTS1001). Non-dynamic arguments and `any`/`unknown` keep the old path, and I found no unsoundness.
- **Dynamic results through generic values (`g02`, `g04`, `g05`):** no growth, and correct.
- **Display names:** the `Holder<Id>` display names show up only in TypeError messages.

Probes and outputs: `/tmp/h09b5d/review1/h09b5d-review1-probes.tar.gz`. Done marker: `/tmp/h09b5d/review1/DONE`.

---

## Round 2

### H09b5d review, round 2 (commit `011c7f6a8`, with the uncommitted r3 build)

## Verdict: NOT READY

The round-2 fix does what it set out to do. All the round-1 regression probes match Node again, the direct-call growth is gone, and the keyed demands, `storedDynamic` and the r3 key change hold up. But splitting generic classes still breaks programs the base ran correctly, in three ways:
- **R2-F1:** functions and classes that only refer to a split class don't split themselves.
- **R2-F2:** which instances share a class depends on the order they were made.
- **R2-F3:** an erased copy can only make one of the split classes.

There is also new growth through generic values, with a measured 4–6x runtime cost.

Each row below shows output from `pts-base` / `pts-final2` / `pts-r3` and Node, all of which I ran. Probes and outputs are in `/tmp/h09b5d/review2/h09b5d-review2-probes.tar.gz` (round-2 probes under `p/`, round-1 reruns under `r1/`, growth numbers in `p/growth2.txt`).

## Round-1 findings on final2

| Round 1 | Status | Evidence |
|---|---|---|
| F1 (TS-legal conversions between split instances) | Fixed for the round-1 probes | c01, c02, c03, c05, c06, c08, c09 all match Node. Order-dependent leftover: R2-F2. |
| F2 (`T[K]` with a method's own K) | Fixed | k01–k03 match Node |
| F3 (growth from direct calls) | Fixed for non-computing generics, still there for computing ones | g01 1,448, g03 294, g06 615 lines, all equal to base. Computing generics: R2-F5. |
| F4 (recursion through `Promise<T>`) | Fixed | r01 and r04 match Node; r02 converges at 13 instances (68 → 236 lines) |
| F5 (phantom class instances) | Fixed | ph02, ph03 match Node |
| F6 (holes still open) | m01, m02, m03, u03, u04, u07 fixed | u01, u02 (C260), u05b (C264) and `ph01` (which also fails on base) still fail exactly as on base |
| F7 (function value identity) | Back to base | f01 output identical to base |
| F8 (code quality) | Done | Shared `substituted`/`normalized` helpers, `irTypeOrNone`, `typeWalk`, the `identified` flag |

Every round-1 probe whose base output matched Node also matches Node on final2 and r3.

## Findings

### R2-F1 (High, regression vs base): functions and classes that only refer to a split class still share one instance

`sharesClass` now splits `Task<Promise<number>>` from `Task<Promise<string>>`, because their `Awaited<T>` members are number and string. But a generic function or class that merely names `Task<T>` counts as "computing nothing": in `computesTypes` (`generic_classes.go:373`), a reference only looks at its own type arguments, so `Task<T>` doesn't count. Its one instance is then built for whichever class came first.

| Probe | Base | final2 / r3 | Node |
|---|---|---|---|
| `h01b_class_result_split_reads`: `make<T>(p: T): Task<T>` called with `Promise<number>`, then `Promise<string>` | `undefined undefined true true / 2` | PTS1001 `using a Task as a Task` (12:11) | same as base |
| `h06_method_arrow_factories`: the same through a generic method, a static method and a generic arrow const | OK | three PTS1001 errors | OK |
| `h05_class_member_of_split_class`: `class Outer<T> { inner: Task<T> }` | OK | PTS1001 (also failed in round 1) | OK |
| `sc02_mutual_classes`: `B<T> { a?: A<T> }` where A splits | OK | PTS1001 | OK |
| `h08_factory_brand_twins`: a factory over `Holder<Id>`/`Holder<number>` and over twins (base fails differently; round 1 passed) | PTS1001 | PTS1001 | `s 3 / t 4` |

- **Root causes:** `computesTypes` (`generic_classes.go:373`) is used both by `computesFromTypeParameters` (`generics.go:1287`, checked at `:1214`) and by `classMembersIdentity` (`generic_classes.go:309`). Separately, `memberPart` writes a reference by its target and arguments with promises as `"value"`. So even if `Outer` were checked, `Task<Promise<number>>` and `Task<Promise<string>>` would look the same.
- **Direction:** count a reference to a program generic class that can split (one with computed members or a computed superclass), or to an interface whose members compute types, as computing. Make `memberPart` write such a reference by the split-relevant parts of the class it names. That would also cover the h02b and u01 holes (`interface Wrap<X> { v: X extends A ? … }`).

### R2-F2 (High, regression vs base): which instances share a class depends on the order they were made

`sharesClass` compares a new instance with existing ones in creation order and joins the first it can share with. `unknown` (or `any`) can join only one group. The `Promise<number>` and `Promise<string>` groups can't merge, so a TypeScript-legal widening of the other group to `Task<unknown>` has no class to go to.

| Probe | Base | final2 / r3 | Node |
|---|---|---|---|
| `c13_widen_two_splits_order`: a `Task<Promise<number>>` parameter declared first, then `wide(t: Task<unknown>)` called with both | `numbers undefined wide true undefined wide true undefined wide false undefined` | PTS1001 at 16:39 | same as base |
| `sc01b_inheritance_no_writes`: `Leaf<T> extends Mid<T> extends Task<T>` | OK | PTS1001 `using a Leaf as a Task` | OK |

The same program with `Task<unknown>` made first (`c12`) runs correctly, so whether a program compiles depends on declaration order. Root cause: the loop at `generic_classes.go:172` and `sharesClass` (`:220`, the check at `:230`).

Possible fixes:
- Build a group per IR type around its widest type argument (e.g. `widestOfIRType`), so dynamic-IR arguments join that group, and split only what can't convert to it.
- Or record this as a known gap with these tests.

### R2-F3 (Medium, regression vs base): an erased copy's `Task<any>` can only be one of the split classes

`c11_erased_copy_two_splits` calls `wrap` through `any`, typed as both `Task<Promise<string>>` and `Task<Promise<number>>`:

| Base | final2 / r3 | Node |
|---|---|---|
| `undefined undefined undefined true` | `TypeError: Expected Task<Promise<string>>, got Task<Promise<number>>` | same as base |

This widens D-078's erased-copy gap to same-IR splits. It is probably inherent to splitting; I'd record it.

### R2-F4 (Medium, growth and runtime): keying every typed demand multiplies instance-table entries, and lookup is a linear scan

Each distinct key adds an entry: a wrapper, an adapter and a check object, even when every entry points at the same instance function (`pts_fn_mk0_n_number_tag_string`). `pts_instance_find` (`pts/runtime/src/object.c:211-221`) scans the table linearly on every call through a value; there is no site cache, unlike methods.

| Probe | Base | final2 | r3 |
|---|---|---|---|
| g02 (C lines) | 1,349 | 2,363 | 2,363 |
| g04 | 1,335 | 2,263 | 2,263 |
| g05 | 513 | 1,441 | 1,441 |
| `g07_values_typed_200` (5 values, 200 literal types) | 853 lines, build 5.7 s | 7,221 lines, 10.2 s | 7,221 |
| `g08_value_one_200` (1 value) | 629 lines, build 4.4 s | 5,405 lines, 6.8 s | 5,405 |
| `g09_value_lookup_hot` (5M calls; the hot site's entry is the 201st) | 0.27–0.48 s | 1.37–1.75 s | 1.26–1.44 s |
| `g09b` control (1 entry) | 0.23–0.24 s | 0.23–0.24 s | n/a |

This is the shape of a Redux `dispatch` held as a value. The key is set at `instances.go:145` (and `forwarding.go:295`); entries are matched at `:881` and `:1123`. Options:
- Key typed demands by IR types plus literal types plus the type-argument identity only where some paired generic computes types.
- Give one entry to all keys that resolve to the same instance.
- Use a hash or a per-site cache in `pts_instance_find`.

### R2-F5 (Medium, cost): computing generics still make one instance per checker type

`g10_computing_dispatch_200` is g06 with an `A["type"]` result. It goes from 615 to 27,281 lines (also on r3), and `pts run` from 0.75 s / 104 MB to 61.1 s / 1.03 GB. Every one of those instances has the same IR, because `A["type"]` is a string literal. The identity is the type arguments' keyParts rather than the IR of the computed types. Comparing the computed types' IR, the way `memberPart` does for classes, would bring this back to one instance.

### R2-F6 (Low, holes that remain; not regressions vs base)

- **`h03_nested_call_body`:** `outer<T>` makes a direct call to `inner<U>`, and only `inner`'s body computes. The call's type arguments are a plain `T`, so `outer` isn't counted as computing. Base, final2 and r3 throw `TypeError: Expected number, got string`; round 1 matched Node. Calls through methods and literals are fine (`h09` `viaHelper`), because those demands are keyed.
- **`h02b_interface_result_twins`:** a result of type `Pick2<T>` (an interface member `X extends A ? …`) throws a TypeError on base, final2 and r3. This is the function side of C263.
- **`h04_overloads`:** a computed type that appears only in an overload signature gives PTS1001 on all builds.

### R2-F7 (Low, code quality)

`keyIdentity.typeArguments` is a bool whose name reads like a list; something like `forTypeArguments` would be clearer. Everything else in round 1's F8 is addressed (`dead_code.go`/SPEC are in the pending docs commit).

## The r3 question (keys without the function type's declaration)

I could not build two typed demands with equal instantiated types but different function-type declarations that need different instances. The value's type arguments are inferred against the demand's instantiated signature (`PtsInstantiateSignatureInContextOf`), and the key already covers the call's type arguments, parameters and result.

`ik02_two_function_types` exercises this with a type alias, an interface call signature, `G<string>`, and method signatures of two interfaces over values that compute in their bodies. Twins and promises go through each, including union receivers. Base, final2, r3 and Node all agree. r3 leaves my growth cases unchanged (above), and every round-1 and round-2 probe gives the same output on r3 as on final2.

## Checked and fine

- **Keyed demands in each call form (`ik01`):** `?.`, rest parameters, a wrapper, a forwarder, `bind`, adapters to non-generic function types, and an interface or literal method all match Node, where the base threw.
- **What `computesFromTypeParameters` detects (`h09`):** `infer`-style casts in the body, a default type argument, a nested non-generic arrow, and a helper object's generic method.
- **Twin store factory (`h07`):** works, because the instances share a class through assignability.
- **`storedDynamic` (`sd01`, `sd03`):** for `unknown`, `any` and `Promise` instances, `typeof`, `Array.isArray`, `instanceof Map`/`Set`, `in`, writes to `x`, closures, generators and async instances all match Node. The base gave PTS9001 here.
  - `sd02`'s Json `instanceof Map` is PTS1001 on base and final2 alike.
  - `sd04`/`sd05` show lost array writes after `Array.isArray`, identical on base. That is the known C231 (plain code too).
- **Split classes:** statics are shared and `instanceof` and inheritance work (`s03`, `sc01b` up to the widening).
- **Direct-call growth:** back at base for non-computing generics.
- **Async generic awaiting `A extends Action` (`g11`):** no growth.
- **Test quality:** the new tests cover every round-1 probe, as guards or as skipped known gaps. Nothing covers R2-F1, R2-F2, R2-F3 or the R2-F4 runtime cost.

`/tmp/h09b5d/review2/DONE` is touched. The probe archive is `/tmp/h09b5d/review2/h09b5d-review2-probes.tar.gz` (round 1's is at `/tmp/h09b5d/review1/h09b5d-review1-probes.tar.gz`).

---

## Round 3

### H09b5d review, round 3 (commit `fb68242c6`)

## Verdict: READY, provided the gaps below are recorded with skipped tests before the slice is ticked

I found no regression against base. Of 69 earlier probes plus 16 new ones, every probe whose base output matched Node still matches on final3, and 16 base failures now match Node. Going back to shared classes by default fixes R2-F1, R2-F2 and R2-F3, and `computedTypes` fixes R2-F5.

The catch: final3 is back to base behaviour for several C177 class cases that final2 ran correctly, and the retry design has holes (R3-F1 to R3-F4). None of them makes a program that worked on base fail, so recording them meets the bar. Fixing R3-F1 looks cheap if you want to do it now. I also found a pre-existing D-195 defect (R3-P1) that needs its own C-number.

Each row below shows output from `pts-base` / `pts-final2` / `pts-final3` and Node, all of which I ran. Everything is in `/tmp/h09b5d/review3/h09b5d-review3-probes.tar.gz`: new probes in `p/`, the earlier probes re-run in `prev/` (`summary.txt`), and the overlay test in `overlay/`.

## Round-2 findings on final3

| Round 2 | Status | Evidence |
|---|---|---|
| R2-F1 (factories and classes referring to a split class) | Fixed | h01b, h05, h06, sc02 match Node |
| R2-F2 (sharing depended on creation order) | Fixed | c12, c13, sc01b match Node |
| R2-F3 (erased copy's `Task<any>`) | Fixed | c11 matches Node |
| R2-F4 (entries per key, linear lookup) | Unchanged, deferred as C266 | g02 2,363, g04 2,263, g05 1,441, g07 7,221, g08 5,405 lines (base 1,349 / 1,335 / 513 / 853 / 629) |
| R2-F5 (computing generics, one instance per checker type) | Fixed | g10 615 lines, same as base (final2 had 27,281) |
| R2-F6 holes | Partly fixed | h02, h02b, h09 match Node; h03 (C267), h04 (C268), h08 and sc01 (C263) still fail as on base |
| R2-F7 (`typeArguments` field name) | Not a blocker | |

Other growth on final3: r02 82 lines (base 68), r03 283 (base 283), g01, g03, g06 and g11 equal to base.

**C266 deferral: I agree.** A demand site can't know whether the value it calls computes types, so typed keys need to stay precise. The cheapest fix is a per-site cache in `pts_instance_find` (`pts/runtime/src/object.c:211`), the same kind `pts_method_instance_find` already has. Another option is to drop literal precision from a demand's key after specialize, when no value it can reach computes types. Please record the g07–g09 numbers in C266, including the 4–6x hot-loop cost.

## Findings, most severe first

### R3-F1 (Medium-High; C177 for classes not fixed where the conflict is in a speculative instance)

When the conflicting use of the shared class sits inside the instance of a generic value, an interface or literal generic method, or an overridden method, that instance's trial fails and is taken back. The call then takes the erased copy, which throws at run time. Nothing in the lowering fails, so the driver never retries with classes split.

| Probe | Base | final2 | final3 | Node |
|---|---|---|---|---|
| `d01_split_conflict_in_value` | `TypeError: Expected Box<string>, got Box<number>` | `s 3 number` | same TypeError | `s 3 number` |
| `d06_split_conflict_dispatched` (interface literal and abstract override) | same TypeError | `s 3 / t 5` | same TypeError | `s 3 / t 5` |

- **Evidence that no lowering fails:** a read-only `go test -overlay` test (`overlay/zz_review3_test.go`, result in `overlay/result.txt`) shows `EmitCWith` and `EmitCNarrowedWith` returning no error at all three of `lower.Fallbacks`.
- **Root cause:** the trial rollback in `tryInstance` (`instances.go:995`), together with `compileC` (`driver.go:87`), which retries only when the whole shared chain fails.
- **Fix:** when a trial is taken back because of a conversion between instances of one generic class (`using a X as a X`), mark the lowering. `compileC` then tries split mode at the first fallback and keeps it if it lowers without such a rollback. Otherwise, record d01 and d06 as a known gap.

### R3-F2 (Medium, by design; record it)

Conflicts that only show at run time aren't fixed while the shared lowering compiles. `d02_runtime_only_conflict` writes the `Awaited<T>` member through `JSON.parse`:

| Base | final2 | final3 | Node |
|---|---|---|---|
| `TypeError: Expected number \| undefined, got string` | `1 s string` | same as base | `1 s string` |

### R3-F3 (Medium): split mode applies to the whole program

When one class needs splitting, every class with computed members splits. `d04_split_changes_elsewhere` combines a Holder conflict, which needs split, with an unrelated `Task` erased-copy result (the c11 pattern):

| Base | final3 | Node |
|---|---|---|
| PTS1001 at compile time | prints `s 3`, then `TypeError: Expected Task<Promise<string>>, got Task<Promise<number>>` | `s 3 / undefined undefined true` |

So a program the base refused at compile time now builds and fails at run time in code Node runs. Splitting only the classes the shared failures blame would fix this; otherwise record it.

`d05` is the same setup with a widening that fails in split mode. Both lowerings fail and the shared error is reported, as designed; base reports the same error.

### R3-F4 (Medium): `widenLiterals` loses a computed literal that flows into a callee's literal type argument

`w06_widened_literal_to_callee`: `outer<T>(t, k: Key<T>)` calls `inner(k)`, where `Key<T>` is `"x"` or `"y"` and `inner<K>` computes `Out<K>` in its body. Once widened, the twins' computed types are both `string`, so they share `outer`'s instance, which calls `inner<"x">` for both.

| Base | final2 | final3 | Node |
|---|---|---|---|
| `TypeError: Expected number, got string` | `number:1 string:s` | same TypeError as base | `number:1 string:s` |

- **Root cause:** `typeArgumentsIdentity` (`generics.go:1270`, `widenLiterals: widen` at `:1282`).
- **Fix:** keep literals unwidened in the types that `computedTypes` (`:1324`) collects from calls' type arguments, or record this under C267.

The rest of the widening holds up. Computed literal keys for `o[k]` (`w01`), a computed literal reaching an inner `pick` (`w02`), and a switch on a computed literal, enum literals and template literals (`w03`) match Node on all builds.

### R3-P1 (High, pre-existing on base, final2 and final3; needs a new C-number): `deadTest` turns legitimate code into a trap where a conditional type is computed from T

`w05_dead_test_conditional_single` has a single call, `mode(a, "fast")`, with `Mode<T> = T extends A ? "fast" : "slow"`. All three builds throw `TypeError: code that can't run for T = unknown ran: m narrows to "fast" here`. Node prints `fast FAST / slow 4`.

- **Root cause:** `deadTest` (`dead_code.go:405`, `:412`) widens `{kind:"a"}` to `unknown`, and `Mode<unknown>` is `"slow"`. A conditional type doesn't keep the "dead for the widest argument means dead for every argument" property D-195 relies on.
- **Fix:** with `computedTypes` now available, `deadTest` could keep the arguments those computed types depend on, or never trap reads whose narrowed type mentions a computed type.
- `w04` shows the same trap with two calls.

### R3-F5 (Low)

- **Failing programs compile a little slower:** t02 (both lowerings fail) goes from 0.16–0.18 s to 0.23–0.29 s. For the three largest skipped corpus programs, the difference is within ±0.06 s.
- **Suspected, not measured:** a C-compiler rejection now re-runs the whole fallback chain with clang a second time.
- **Comment wraps:** stray line breaks at `generic_classes.go:31` and `:379`, and `instances.go:258`.

## The questions you asked

- **Diagnostics when both lowerings fail:** the first chain's last error is reported (`d05`), as designed.
- **emit-c and build agree:** both go through `compileC`. For the split RED test, emit-c has `pts_type_Holder` and `pts_type_Holder_2`, and the build runs.
- **Compile time:** negligible extra cost for succeeding programs; failing programs see the small increase above.

## Checked and fine

- **`computedTypes`:**
  - `infer`-style casts, default type arguments, closures and nested non-generic arrows are all seen (`h09`).
  - A generic arrow's computed type that mentions the enclosing function's parameter works (`ct01`, first line). Its second line, a class method, fails as C260.
  - Class methods' own type parameters work (`u04`).
- **`isProgramInterface`:** a recursive interface, a merged (two-declaration) interface, and an interface with a call signature, each computing `Awaited<T>`, match Node (`i01`; base gave PTS1001).
- **r3 instance keys:** `ik02` matches Node on all builds.
- **`storedDynamic`:** the `sd` family matches final2 (C231, the lost array writes after `Array.isArray`, is unchanged).
- **Remaining earlier failures:** c04, c07, f01, ph01, s01, s02, u01, u02, u05, u05b, h03, h04, h08, sc01, sd01, the sd02 Json/number/string variants and sd04/sd05 fail as on base. None regressed.

`/tmp/h09b5d/review3/DONE` is touched.
