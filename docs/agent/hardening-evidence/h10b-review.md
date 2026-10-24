# H10b review reports

Child: `reviewer` (claude-opus-5-5, xhigh), read-only, one per round unless noted. Each report was returned as the child's final message and is saved here unchanged. Archives: `h10b-review<n>-probes.tar.gz`.

## Round 1 (snapshot 1 of `df3828bc7`'s change, 2026-10-24)

# H10b review, round 1 (snapshot `/tmp/h10b/snap1`, HEAD `d365370a6` plus the staged change)

The change does what H10b set out to do: on the H10a 777-case matrix it moves 622 OK to 751 OK, and no case got worse. But design C, "tsc's `C<any>` is a dynamic value everywhere", also reaches programs that use `Box<any>` with no `instanceof` at all. Those programs compiled and ran correctly on HEAD; several now fail to compile or run much slower. In other places the change turns a TypeError or PTS1001 into silently wrong output. One new code path also crashes the program with a segfault.

## What I checked
- **777-case matrix** on the snapshot (one job): **751 OK, 24 PTS1001, 2 TYPEERR.** Against HEAD's recorded results, 128 went from TypeError to OK and 1 from PTS1001 to OK. Nothing got worse and nothing is WRONG. What's left is C280 (24 cases, H29) and two D-070 cases that fail by design.
- **748-case H10b matrix:** I re-ran the promise cases only. The `promise_all` cases from `unknown` and `any` are fixed. 4 cases are still silently wrong (F13).
- **Go unit tests:** `go test ./pts/lower/... ./pts/cgen/... ./pts/ir/...` pass on the snapshot.
- **emit-c sweep:** I compared emit-c for the 86 tests that mention `C<any>`. 14 differ. All of them are the expected switch to value arrays and value fields.
- **Things that work and match Node** (probe files under `/tmp/h10b/review1/p/`):
  - type shapes: `C<any> | undefined`, `Readonly`, unions of two `C<any>`s, unions mixed with typed classes, tuples, Map/Set keys and values
  - subclasses and statics, `super`, abstract generic bases, generic Error subclasses
  - private writes, compound updates and calls on subclass objects, `any` receivers and `unknown` sources, with V8's messages for wrong objects
  - the same private name redeclared in a subclass (each class reaches its own member)
  - private async and generator methods, and private methods with default or rest parameters
  - `.par` over a `Box<any>[]` of mixed instances at 1, 2 and 4 threads, stable over 15 runs
  - the C285 forms `concat`, `slice`, `flat`, `toReversed`, `Array.of(...)`, `[...new Set]`, `allSettled`, `any`, `race`
  - compile time stays under about 1 s for chains and nesting 22–40 deep
- **Controls that showed no problem:** `r11` (object literals into shapes) and `r13b` (private arrays when the objects really are `Box<any>`).

## Findings
All repros are in `/tmp/h10b/review1/r/`. Every repro's Node / snapshot / base output is recorded in `/tmp/h10b/review1/r_outputs.txt`.

**R1-F1 · High · regression.** H10a's `instanceof` guards now fire on every `C<any>` value, including in programs with no `instanceof`.
- Repros: `r01_any_member_to_any_array.pts`, `r03_any_instance_to_interface.pts`, `r07_any_instance_callbacks.pts`.
- Node and base print the expected output. The snapshot is PTS1001 with ``using the object an `instanceof` narrowing holds as a dynamic value…``, or ``giving a function whose parameter is a value[] to a method of the object an `instanceof` narrowing holds…``. The message is also misleading here, since there is no narrowing.
- The forms hit, with only `Box<any>` objects in the program:
  - `return b.items` with an `any[]` return type
  - `const kept: any[] = a.items`
  - `pushTo(a.items)`
  - `Box<any>` passed to an interface (`Getter<any>`) or an object shape (`{v: any; items: any[]}`)
  - a named function with an `any[]` parameter given to `b.each(f)`, `b.handlers.push(f)` or `b.rows.forEach(f)`
- Root cause: `instanceof.go:42-49`, `isUninferredInstanceof`, returns `holdsAnyInstance && !storedDynamic` for every such value. That feeds `copiesInstanceofDynamicValue`, `copiesCallbackArgument` and `storesCopyingFunction`.
- Code inside generic instances (where T is `any`) is not affected (`p18`, `p45`).
- A possible fix: a conversion with no copy and a run-time check (TypeError unless the array already holds dynamic values) instead of the compile-time PTS1001.

**R1-F2 · Medium · regression.** `#v in other`, where `other: Box<T>` in the class's instance for `any`, is now PTS1001.
- Repro: `r02_private_in_any_instance.pts`. Node and base print `true`; the snapshot says ``#private in` on a value is not supported yet``.
- Root cause: `classes.go:1145` (`!isObject(x.Type())`).
- Also seen, and the same on HEAD:
  - `#v in o` for an `o` declared `any` is PTS1001 (an H10b gap).
  - `#v in o` in a static method of a generic class is **PTS9001**, because `currentClass()` is nil at `classes.go:1137-1139` (`r02b`; out of scope, H29).

**R1-F3 · Medium · regression.** Several operations on a `Box<any>` value are now PTS1001: object spread `{...b}`, rest destructuring `const {v, ...rest} = b`, and `b.add.bind(b)`, `.call` and `.apply`.
- Repro: `r04_any_instance_spread_bind.pts`. Base matches Node.
- These are existing limits on dynamic values; design C now routes explicit `Box<any>` places into them.

**R1-F4 · High · regression.** A generic private method called on a dynamic receiver always throws.
- Repro: `r05_generic_private_method_dynamic.pts`, line 1 is `other.#pair(tag)` with `other: Box<T>` in the instance for `any`.
- Node and base print `["b","t"]`. The snapshot throws `TypeError: Receiver must be an instance of class Box`.
- The same call on an `object` source also throws, with a misleading message (base threw `Expected Box<any>…`).
- Root cause: `cgen.go:1155` uses `class.Method(x.Name, ir.Plain)`, which never finds a generic method. `familyPrivateCall` (`instanceof.go:690`) doesn't reject or route that case.

**R1-F5 · Medium-High · regression (run time).** Hot loops over `Box<any>` places are much slower.

| Repro | Base | Snapshot | Slowdown |
|---|---|---|---|
| `r06` local `new Box<any>` | 88–95 ms | 580–650 ms | 6.5× |
| `r06` `Box<any>` parameter | 150–174 ms | 890–1018 ms | 5.5× |
| `r06` `Box<any>[]` | 124–134 ms | 329–373 ms | 2.7× |
| `r06b` `new Stack<any>()` push/pop | 141–161 ms | 494–566 ms | 3.4× |
| `r06c` a generic list's own traversal in its `any` instance | 10–14 ms | 153–229 ms | ~15× |

- Timings were run interleaved, three runs each.
- Root cause: `lower.go:4104-4106` makes `irType(C<any>)` a dynamic value everywhere. The guard test's 200k-iteration loop only checks output, not speed.

**R1-F6 · High · worse than HEAD (TypeError or PTS1001 became silently wrong).** Names bound by destructuring from `C<any>` values are copies, so writes through them are lost.
- Repros: `r16_any_place_destructuring_copies.pts`, `r16b_narrowed_flow_destructuring.pts`, `r16c_flow_destructuring_forms.pts`.

| Repro | Node | Snapshot | Base |
|---|---|---|---|
| `r16` | `[1,2,3,4,5]` | `[4,5]` | PTS1001 |
| `r16b` | `[1,2]` | `[]` | TypeError |
| `r16c` | `[1..7]` | `[2,3,6]` | TypeError |

- Forms that lose the write:
  - a `for…of` pattern over `Box<any>[]` or over `[c]`
  - a parameter pattern (`take({ items }: MapLRU<any, any>)`)
  - a destructuring assignment (`({ items: it } = boxes[0])`)
  - a destructured `forEach` parameter
  - a nested `for…of` pattern over a Map
  - an array pattern (`const [{ items }] = arr`)
  - `const { items } = h.m!`
- Root cause: these binding sources aren't recognised by `isInstanceofDynamicLocal` / `bindingSource` (`instanceof.go:123`). Plain `const { items } = b` and `const items = b.items` do keep the array.

**R1-F7 · Medium-High · worse than HEAD (PTS1001 became silently wrong).** A `Box<any>` place holding a `Box<number>` copies when its members go into typed places, or when values are stored into its fields.
- Repros: `r08_any_instance_typed_sink_copies.pts`, `r08b_store_into_any_place_copies.pts`.
- `r08`: Node `1 1 1`, snapshot `0 0 0`. Base was PTS1001 for the tuple array and the Map; the `number[]` line was a silent copy on HEAD too.
- `r08b`: storing an `any[]`, or an object holding one, into a `Box<number>` field through a `Box<any>` place loses later writes (Node `[5]`, `[6]`, `[7,8]`).
- This is the same D-070/D-179 copy that `any` already makes (`p16b`). H10a's guard only covers targets that hold dynamic values.

**R1-F8 · High · H10b gap (rest of C274), silently wrong.** Writes through a private array or Map field of an instance held as a dynamic value are lost.
- Repros: `r13c_private_array_object_source.pts` and `r13_private_array_member_writes.pts`.
- `r13c` (an `object` source): Node `[1] 1`, snapshot and base `[] 0`.
- `r13` (a `Box<any>` place): the writes are lost and then a RangeError is thrown.
- Root cause: `instanceof.go:668-669`. `familyPrivateField` converts the `FamilyField` to tsc's `any[]` / `Map` type with `checkedConversion`, which copies.

**R1-F9 · High · H10b defect, crash.** Unbounded recursion through a private method call on a dynamic receiver kills the process with a segfault.
- Repro: `r12_family_call_recursion.pts`.
- Node prints `RangeError`. The snapshot exits 139 (core dumped). Base threw a catchable TypeError.
- Root cause: `mayRecurse` (`cgen.go:675-681`) doesn't count `*ir.FamilyCall`, so no `pts_stack_check()` is emitted. The generator path at `generators.go:183` uses the same function.
- Fix: add `*ir.FamilyCall` to the call list in `mayRecurse`.

**R1-F10 · Low-Medium · regression.** Read-only uses of C285 containers are now PTS1001.
- Repro: `r09_container_par_read_only.pts`, run at threads 1, 2 and 4.
- The uses: `count([...c.matrix])` where the parameter is only read, `rows.par.map(...)`, and `[...new Set(c.matrix)].par.map(...)`.
- Base ran all three correctly; it made a copy, but nothing wrote to it.

**R1-F11 · Medium · H10b gap (C285), silently wrong, same on HEAD.** An iterator step bound to a local copies its value.
- Repro: `r10_iterator_local_step.pts`, e.g. `const s = c.store.values().next(); s.value!.push(1)`.
- Node: `true true`, `[1,2,3]`. Snapshot: `false false`, `[3]`. Base: `[]`.
- Inline steps, `for…of`, spreads and patterns over the iterator do work (`p38`).

**R1-F12 · Medium · H10b gap (C285 "spreads into calls"), silently wrong, same on HEAD.** A narrowed instance's rows spread into a rest parameter are copied.
- Repro: `r14_spread_into_rest_parameter.pts`, `pushRest(...c.matrix)` with `...rows: any[][]`.
- Node `[[1],[]]`; snapshot and base `[[],[]]`.

**R1-F13 · Medium · H10b gap (C285 `.then(f)`), silently wrong, same on HEAD.** `.then(pushRow)` by reference loses the write for `any` and `unknown` sources.
- Repro: `p/m02_any_then_ref.pts`, plus the matrix cases `K_{any,unknown}__{then_ref,pending_then}__number`.
- The same forms from interface or base-class sources are an honest PTS1001.

**R1-F14 · Low · H10b defect.** `o.#m(args)` on a dynamic receiver evaluates the arguments before the receiver check; V8 checks first.
- Repro: `r15_family_call_argument_order.pts`. For a wrong receiver, Node logs no arguments; the snapshot logs `arg1,arg2` before throwing.

**Out of scope (pre-existing, same on HEAD):**
- `o["#g"]` on `any` finds a private getter by name (`p24`; H29).
- A static private read on an instance through `any` gives `undefined` instead of a TypeError (`p52`; H29).
- `this.next ? this.next.items : []` in a generic class's `any` instance is PTS1001 with the `instanceof` message (`p13`; an H10a regression, not this slice).
- Nested generic places (`Box<Box<any>>`) and intersections (`Box<any> & {tag}`) are PTS1001 (`p05`).

## Test quality
The new tests cover the RED forms but miss:
- explicit `Box<any>` programs that HEAD ran (F1–F3)
- generic private methods (F4)
- a stack check or recursion through private calls (F9)
- destructuring of flowed values (F6)
- writes through private array or Map fields (F8)
- iterator-step locals and rest spreads (F11, F12)

The hot-loop guard checks correctness only, so F5's slowdown passes it.

**VERDICT: NOT READY**

Files are in `/tmp/h10b/review1/`:
- `r/` (the repros, F1–F14)
- `r_outputs.txt` (Node / snapshot / base output for every repro)
- `m777_snap1.jsonl` (the matrix results)
- `bin/cmp.sh`, `bin/cmp777.py`

The archive is `/tmp/h10b/h10b-review1-probes.tar.gz` (no binaries), and `/tmp/h10b/review1.done` is touched.

## Round 2 (snapshot 2: `df3828bc7` plus the round-1 fixes, committed as `1cce8a08c`; the same reviewer, resumed)

# H10b review, round 2 (snapshot `/tmp/h10b/snap2`, HEAD `df3828bc7` plus the staged round-2 change)

Most round-1 findings are fixed and nothing in either matrix got worse. But several fixes leave holes, and in the new holes writes are lost silently. Some of them are worse than HEAD: where HEAD threw a TypeError or stopped at PTS1001, the program now runs and gives wrong output. Some programs that only make `Box<any>` objects (no `instanceof`) now throw or fail to compile where HEAD ran them correctly.

## What I checked
- **777-case matrix** on the snapshot: **751 OK, 24 PTS1001, 2 TYPEERR**, case for case the same as round 1. Nothing got worse than HEAD (622 OK) and nothing is WRONG.
- **748-case H10b matrix** on the snapshot: **732 OK, 12 PTS1001, 4 WRONG**, the same as the parent's run on its intermediate CLI (`/tmp/h10b/mxrun/h10b_c3.jsonl`). The 4 WRONG are R1-F13's `.then(f)` cases from `any` and `unknown` sources.
- Neither matrix covers the silent paths below. They need a value that left the narrowing, or a `Box<any>` place.
- **Suite:** suite2 finished with no failures.
- **Re-runs:** all 24 round-1 repros and 77 round-1 probes against snapshot 2 (`r1_outputs_snap2.txt`, `p1_outputs_snap2.txt`). New probes: q01–q22 and 18 `r2_*` repros.
- Node / snapshot 2 / base outputs are in `/tmp/h10b/review2/r2_outputs.txt` and `q_outputs_snap2.txt`.

## Round-1 findings
| Finding | Status | Evidence |
|---|---|---|
| F1 guards on every `C<any>` | **Fixed** for direct members, interfaces, callbacks | r01, r03, r07 match Node |
| F1 (follow-on) | **Still wrong** for new containers of those members | R2-F1 |
| F2 `#f in o` | **Fixed** (also HEAD's PTS9001 in static methods) | r02, r02b match Node |
| F2 (follow-on) | Static-private brand and primitive operands still wrong | R2-F7 |
| F3 spread / rest / bind | **Fixed** (compiles and runs) | r04 matches Node |
| F3 (follow-on) | `call`/`apply` ignore the `this` they are given | R2-F6 |
| F4 generic private method | **Fixed** | r05, q06, q22 |
| F5 hot-loop speed | **Deferred** to H27, as decided | r06 still about 6–11× slower than HEAD |
| F6 destructuring | **Fixed** for the forms reported | r16b, r16c match Node; r16's destructuring assignment is now an honest TypeError |
| F6 (follow-on) | Two-level patterns, rest objects and spreads still copy | R2-F4 |
| F7 typed sinks copy | **Intended** (as through `any`, D-071) | r08, r08b still lose writes, as decided |
| F8 private container writes | **Fixed** for arrays, objects, Maps and Sets | r13, r13c |
| F8 (follow-on) | Arrays of functions still copy | R2-F8 |
| F9 recursion crash | **Fixed** | r12 prints RangeError |
| F10 read-only container uses | **Kept** as D-218's rule | r09 is PTS1001 |
| F11 iterator step in a local | **Fixed** | r10, q07 |
| F12 rows spread into a rest parameter | **Fixed** for the narrowed reference itself (PTS1001, r14) | |
| F12 (follow-on) | Flows and `C<any>` places still copy silently | R2-F2 |
| F13 `.then(f)` from `any`/`unknown` | **Recorded** (C288) | |
| F14 argument order | **Fixed** | r15 |

## New findings
All repros are in `/tmp/h10b/review2/r/`. "Base" is HEAD before H10b (`d365370a6`).

**R2-F1 · High · regression (HEAD ran these as Node).** The as-is conversion rejects new containers built from a `Box<any>` object's members.
- Repros: `r2_as_is_fresh_containers.pts`, `r2b_as_is_fresh_collections.pts`. The programs make only `Box<any>` objects and use no `instanceof`.
- Snapshot 2 throws `TypeError: Expected value[][], got Array, which converts to it only as a copy that wouldn't show later changes` (or `Map<string, value[]>`, `Set<value[]>`) for 11 forms going into annotated places:
  - `const all: any[][] = [b.items]`, and a returned `[x.items]`
  - `b.matrix.map((r) => r)`, `[...b.matrix]`, `Array.of(...b.matrix)`, `b.matrix.concat([])`
  - `new Set(b.matrix)`, `new Map(b.store)`
  - `Object.values(b.rec)`, `Object.entries(b.rec)`, `Array.from(b.store.values())`
  - `g([b.items])`, where `g` takes `rows: any[][]`
- Base prints Node's output. `filter`, `slice` and tuple or object literals pass.
- Root cause: these containers hold their elements as dynamic values (an array of values, or a runtime array result). `pts_value_convert_as_is` (`convert.c:1134`) rejects them for an `any[][]` target. The only copy needed is of the new outer container, which nothing else can see.

**R2-F2 · High · worse than HEAD (TypeError became silently wrong).** Values that flowed out of a narrowing lose H10a's protections. The guards for callbacks given by reference, stored functions and rest-parameter spreads now apply only to the narrowed reference itself (`narrowedByInstanceof`).
- Repro `r2_flow_callbacks_and_stores.pts`, inferred `Cache<string, number>` source, then `const m2 = c` and `ident(c)`:
  - `m2.matrix.forEach(addRow)`, `ident(c).each(addRow)`, `m2.handlers.push(addAll)` with `addRow(row: any[])`.
  - Node `[[1,1],[1,1,2]]`. Snapshot 2 `[[],[]]`. Base throws `TypeError: Expected MapLRU<any, any>…`. Snapshot 1 was PTS1001.
- Repro `r2b_flow_stored_functions.pts`: `m2.cfg.onRows = addAll; m2.list[0] = addRow`.
  - Node `[[1],[2]]`. Snapshot 2 `[[],[]]`. Base TypeError. Snapshot 1 PTS1001.
- Repro `r2_rest_spread_flow_and_place.pts`: `pushRest(...m2.matrix)`, and `pushRest(...b.matrix.slice(1))` through a `MapLRU<any, any>` place.
  - Node `[[9],[9]]`. Snapshot 2 `[[],[]]`.
- Root cause:
  - `copiesCallbackArgument` (`instanceof.go:1097,1103`), `copiesSpreadCallbacks` (`:1146`), `isInstanceofDynamicPlace` (`:1270`) and `restArray` (`lower.go:2191`) test `narrowedByInstanceof`.
  - That test is false for `const m2 = c` from an inferred source, for `ident(c)`, and for `C<any>` places.
  - Such values then convert as through `any` (D-071/D-075), which copies.
- From an `object` source, `const m2 = o` keeps the protection (`q01b` is PTS1001), but `ident(o)` doesn't.

**R2-F3 · High · worse than HEAD (PTS1001 became silently wrong).** The new interface exemption also covers H10a's own narrowings.
- Repro: `r2_interface_member_copies.pts`. `if (o instanceof Box) { const l: Lister<any> = o; l.items.push(5) }` from an `object` source, and the same through a `Box<any>` place.
- Node `[ 5, 6 ]`. Snapshot 2 `[]`. Base was PTS1001 at both.
- Root cause: `instanceof.go:390-392` skips the guard for every interface target whose value `holdsAnyInstance`. Reading `l.items` through the interface then copies `number[]` into `any[]`.

**R2-F4 · High · worse than HEAD.** Destructuring holes the R1-F6 fix leaves open.
- Repro `r2_nested_patterns_two_levels.pts`, object patterns two levels deep:
  - a parameter `({ meta: { tags } }: Box<any>)`, a callback `({ meta: { tags: t } }) => …`, and `for (const { meta: { tags: t } } of [b])`.
  - Node `[1,2,3,4,5]`. Snapshot 2 `[2,5]`. Base PTS1001.
  - `destructuresAnyInstance` (`instanceof.go` near 170) only looks at the immediate parent pattern.
- Repro `r2_nested_pattern_rest_spread.pts`, a `Box<any>` place and an inferred flow:
  - a `for…of` nested pattern, a rest object `const { key, ...rest } = b; rest.items.push(2)`, and an object spread `{ ...b }.items.push(3)`.
  - Node `[[2,3],[1,4,5]]`. Snapshot 2 `[[],[4]]`.
  - The rest and spread paths read members through `missingOwnProperty`/`spreadNames` and then convert them by a copy.

**R2-F5 · High · regression (also in snapshot 1; my round-1 probes missed it).** Generic methods that return `Box<R>`, called on a `Box<any>` value.
- Repro `r2b_generic_method_map_only.pts`: `const m = a.map((v) => [v])` with `a = new Box<any>(1)`. Snapshot 2 throws `TypeError: Expected Box<any[]>, got Box<any>`; so does `const s: Box<string> = a.map(String)`. Base and Node run it.
- Repro `r2c_generic_method_pair_only.pts`: `pair<U>(u: U): Box<[T, U]>` is now PTS1001 `polymorphic recursion more than 64 instances deep`. Base compiles and runs it.
- Root cause: the call now goes through the dynamic path and the erased copy. That copy makes a `Box<any>`, and the checked conversion to tsc's `Box<R>` rejects it. Erased copies in method tables also recurse on `Box<[T, U]>`.
- The guard test only covers the chained form `a.map(…).get()`.

**R2-F6 · Medium · regression (silently wrong where HEAD was right).** `b.m.call(other, …)` and `b.m.apply(other, …)` on a `Box<any>` value ignore `other`.
- Repro: `r2_call_apply_this.pts`. Node and base `1 o b 0 1`; snapshot 2 `1 b b 1 0`.
- A method read from a dynamic value comes back already bound to `b`. `functionMethodCall` (`methods.go:326`) now sends `Box<any>` values down that path.
- `any` values do the same on HEAD (`r2b_call_apply_this_any.pts`).
- The new test `generic_any_instance_object_operations` only calls `x.add.call(x, …)`, passing the same object, so it can't see this.

**R2-F7 · Low-Medium · remaining defect in the rewritten `privateIn`.**
- Repro `r2_private_in_static_brand.pts`:
  - `#count in instance`, for a **static** private, is `true`; Node says `false` (the same for a static private method).
  - `#v in 5` and `#v in null` are `false`; Node throws TypeError.
- The static case is also wrong on base for typed receivers (`r2b_private_in_static_brand_typed.pts`).

**R2-F8 · Medium · remaining H10b gap, silently wrong.** A push onto a private **array of functions** of a dynamically held instance is lost.
- Repro: `r2_private_function_array.pts`. Node `2`, snapshot 2 `0`.
- The R1-F8 fix keeps data arrays, but `((x: T) => T)[]` still converts through a copy with adapters.

**R2-F9 · Low · regression.** The `partsAsIs` flag leaks into a nested destructuring assignment of an unrelated `any` value.
- Repro: `r2_parts_as_is_leak.pts`. The flag is set at `destructuring.go:672` and stays set while a default's function is lowered.
- Snapshot 2 throws the as-is TypeError at line 10. The identical statement outside the default works. Base and Node print `2 2 0`.

## Out of scope / noted
- An object spread of a subclass object drops the subclass's fields (`r2_spread_subclass_fields.pts`). HEAD does the same for typed class places (`q18`); this is a general spread limitation.
- `Getter<any>` from a `Box<number>` is a checked TypeError (`q09`). The new test names this as C280.
- The as-is message names internal types (`value[]`, `Array`) rather than `any[]` (C277's class, H22).
- Flows and places going into annotated `any[]` places now get the honest as-is TypeError instead of PTS1001 (`q08`, `q19`), which is not silent.

**VERDICT: NOT READY**

Files are in `/tmp/h10b/review2/`:
- `r/` (the round-2 repros), `p/` (q01–q22), `r1/`, `p1/` (the round-1 files re-run)
- `r2_outputs.txt`, `q_outputs_snap2.txt`, `r1_outputs_snap2.txt`, `p1_outputs_snap2.txt`
- `m777_snap2.jsonl`, `m748_snap2.jsonl`, `bin/`

The archive is `/tmp/h10b/h10b-review2-probes.tar.gz` (no binaries), and `/tmp/h10b/review2.done` is touched.

## Round 3 (snapshot 3: `1cce8a08c` plus the round-2 fixes, committed as `8539bd6d4`; the same reviewer, resumed)

# H10b review, round 3 (snapshot `/tmp/h10b/snap3`, HEAD `1cce8a08c` plus the staged round-3 change)

Not ready. Every round-2 finding is fixed or now ends in an explicit TypeError or PTS1001, and the round-2 silent paths are gone. But the new code breaks five things that HEAD ran correctly, all in programs that only make instances for `any` (no `instanceof`). One of them is silently wrong; the other four now throw or fail to compile.

## What I checked
- **777-case matrix:** **751 OK, 24 PTS1001, 2 TYPEERR**, identical to rounds 1 and 2. Nothing got worse than HEAD (622 OK).
- **748-case H10b matrix:** **732 OK, 12 PTS1001, 4 WRONG**, identical to round 2. The 4 WRONG are R1-F13's recorded `.then(f)` cases from `any` and `unknown` sources.
- **Suite:** suite3 finished with no failures.
- **Re-runs:** all 144 earlier files (round-1 and round-2 repros and probes) against snapshot 3, in `old_outputs_snap3.txt`.
- **New probes:** s01–s15, plus 6 `r3_*` repros. For each repro, HEAD's output matches Node and snapshot 3's doesn't (`r3_outputs.txt`, checked with `bin/diffbase.py`).

## Earlier findings on snapshot 3
- **Fixed, matching Node:**
  - R1: F1, F2, F3, F4, F6 (plain forms), F8, F9, F11, F14.
  - R2-F1: `r2_as_is_fresh_containers`, `r2b_as_is_fresh_collections`.
  - R2-F4: `r2_nested_patterns_two_levels`.
  - R2-F5, direct calls: `r2_generic_method_result`, map, pair, and the chained or `?.` or element forms in `s04b`.
  - R2-F6: `r2_call_apply_this`; `s05c` covers subclasses without an override.
  - R2-F7: `r2_private_in_static_brand*`, `s07`.
  - R2-F8: `r2_private_function_array`.
  - R2-F9: `r2_parts_as_is_leak`.
- **Intended or recorded:**
  - R1-F5 speed (H27).
  - R1-F7: `r08`, `r08b`, `p16*`, `p25*`.
  - R1-F10: `r09` is PTS1001.
  - R1-F12: `r14` is PTS1001.
  - R1-F13 (C288).
  - `f.call(other)` on a method read as a value stays bound, D-044 (`r2b_call_apply_this_any`).
  - Subclass spreads, C280 and message wording are your out-of-scope notes.
- **Now an explicit TypeError instead of silently wrong** (another instance in a `C<any>` place):
  - flows with callbacks or stored functions: `r2_flow_callbacks_and_stores`, `r2b_flow_stored_functions`, `q01`, `q10`
  - rest spreads: `r2_rest_spread_flow_and_place`, `q12`
  - destructuring: `r2_nested_pattern_rest_spread`, `r16`, `p46`, `q05`
  - `.call` with an instance of another class: `q04`, `s05`, `s05b`. For overrides, HEAD silently ran the override; now it's a TypeError, which is better.
- **PTS1001 as on HEAD:** R2-F3 for a narrowing's object (`r2_interface_member_copies`, `q09`).

## New findings
All are regressions against HEAD: HEAD ran each program as Node does. Repros are in `/tmp/h10b/review3/r/`.

**R3-F1 · High · silently wrong where HEAD was right (new this round).** The as-is adapter breaks function identity.
- Repro: `r3_adapter_identity.pts`, with `Emitter<any>` objects only and `log(rows: any[])`.
- Node and HEAD print `off true 0`, `indexOf 0 true`, `stored true`, `same true`. Snapshot 3 prints `off false 1`, `indexOf -1 false`, `stored false`, `same false`.
- So `off()` never removes a handler. The realistic `s13c` shows the same: `bus after off` still calls the handler (`1 [ 1, 1 ]` against Node's `0 [ 1 ]`).
- Root cause: a function with an `any[]` parameter given to a `C<any>` value's method, or stored in it, is wrapped in a new closure. The wrapping is `instanceCallbackArgument` → `asIsAdapter` (`instanceof.go`, `any_instances.go`) and `storedFunctionValue` (`lower.go:3092`). Snapshot 2 kept identity.

**R3-F2 · High · TypeError where HEAD ran (R2-F5's fix is incomplete).** A generic method called on a `C<any>` local or parameter goes through `any` and its erased copy.
- Repro `r3_generic_method_chain.pts`: `const x = a.map(f); x.map(g)`. Also `s04d` c2–c5 and c7, and `s15` "local".
- Repro `r3_recursive_generic_method.pts`: `Tree<T>.mapTree<R>` calling `k.mapTree(f)` on its children in the instance for `any`. Also `s15` "inClass" and the realistic `s13`/`s13b`.
- Snapshot 3 throws `Expected Box<number>, got Box<any>` (or `Tree<string>` / `Option<number>`).
- Root cause: `lower.go:1740-1744` marks the receiver in `classReceivers`, but `call()` keeps going. The later `isNarrowedDynamic(callee.Expression())` branch (`lower.go:1766`) still takes `dynamicCall` for a receiver that is a dynamic local or a `C<any>` parameter. The earlier `holdsInstanceofDynamicValue` and `callsInstanceofMethodDynamically` branches have the same problem.
- Calls on a module-level `const a = new Box<any>(…)` work, which is why `s04b` passes.

**R3-F3 · Medium-High · TypeError where HEAD ran (R2-F1's fix is incomplete).** Fresh containers nested in fresh containers, or inside object literals, throw.
- Repro: `r3_fresh_nested.pts`. Also `s06b` (nestedFresh, setOfSets, objLitArr).
- Forms:
  - `const c: any[][][] = [[b.items]]`
  - `const s: Set<any[]>[] = [new Set(b.matrix)]`
  - `const o: { rows: any[][] } = { rows: [b.items] }`, and the same as an argument
- Snapshot 3 throws `Expected value[][][], got Array, whose items convert to it only as copies…` or `Expected value[][], got Array, which converts to it only as a copy…`.
- Root cause:
  - `pts_value_convert_fresh` (`convert.c:973-978`) copies only the outer container (and tuples at depth 1), not inner containers that are fresh too.
  - `asIsConversion` marks `Fresh` only from the top node, so an object literal holding a fresh array gets the plain as-is check.

**R3-F4 · Medium · PTS1001 where HEAD ran.** An object spread of a `Box<any>` object's member is rejected.
- Repro: `r3_spread_member_record.pts`, with `{ ...b.rec }` into a `Record<string, any[]>` and `{ ...b.meta, extra: true }`.
- Snapshot 3: `spreading a value into an object is not supported yet`.
- The R1-F3 fix covers spreading the `C<any>` value itself (`spreadObject` / `spreadIntoRecord`, gated on `holdsAnyInstance` of the spread expression), not its dynamic members.

**R3-F5 · Medium · PTS1001 where HEAD ran, with a misleading message.** Functions with a default, optional or rest parameter can't be given by reference to a `C<any>` value's method.
- Repro: `r3_adapter_defaults_rest.pts`, with `withDefault(row: any[], tag = "d")`, `withOptional(row: any[], i?: number)` and `withRest(...rs: any[][])` given to `b.run(f)`.
- Snapshot 3 is PTS1001 ``giving a function whose parameter is a value[] to a method of the object an `instanceof` narrowing holds…``. The program has no `instanceof`.
- Root cause: `asIsAdapter` returns nil for `ir.Given` and rest parameters (`any_instances.go`), and `instanceCallbackArgument` then reports.

**Cosmetic:** in `s15` "inClass", the TypeError names `LinkedList<"bigint" | "boolean" | … >` where tsc's type is `LinkedList<string>`. The instance class is shared and its display name comes from another instantiation (C277's class).

## Checked and fine
- Generic methods on narrowed values from `object`, `unknown`, `any` and a base class (`s01`).
- Promise `.then` and `.catch` adapters, and generic results (`s03c`).
- `.par` over a `Box<any>` object's members, at 1, 2 and 4 threads (`q17`).
- Generic private methods, including on non-generic classes (`q06`, `q22`).
- `privateIn`, including primitives and static brands (`s07`).
- Fresh conversion into unions, tuples, entries and maps of rows (`s06b`, other than R3-F3's forms).
- The realistic `LinkedList` / `Observable` / `Graph` program (`s14`).

**VERDICT: NOT READY**

Files are in `/tmp/h10b/review3/`:
- `r/` (the six repros), `p/` (s01–s15), `old/` (the earlier files)
- `r3_outputs.txt`, `s_outputs_snap3.txt`, `old_outputs_snap3.txt`
- `m777_snap3.jsonl`, `m748_snap3.jsonl`, `bin/`

The archive is `/tmp/h10b/h10b-review3-probes.tar.gz` (no binaries), and `/tmp/h10b/review3.done` is touched.
