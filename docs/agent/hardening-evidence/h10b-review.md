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

## Round 4 (snapshot 4: `8539bd6d4` plus the round-3 fixes, committed as `af348bc6e`; the same reviewer, resumed)

# H10b review, round 4 (snapshot `/tmp/h10b/snap4`, HEAD `8539bd6d4` plus the staged round-4 change)

Not ready. All five round-3 findings are fixed for the cases they named, and nothing else changed against snapshot 3. But three of this round's fixes leave regressions against HEAD, two of them silently wrong. A fourth (spread copies) is a new silent path where HEAD stopped at PTS1001.

## What I checked
- **777-case matrix:** **751 OK, 24 PTS1001, 2 TYPEERR**, identical to rounds 1–3. Nothing got worse than HEAD.
- **748-case H10b matrix:** **732 OK, 12 PTS1001, 4 WRONG**, identical to rounds 2–3. The 4 WRONG are R1-F13's recorded cases.
- **Suite:** suite4 finished with no failures.
- **Re-runs:** all 170 earlier files (repros r1/r2/r3, probes p1/q/s), in `old_outputs_snap4.txt`. Against snapshot 3 (`bin/cmpsnaps.py`), the only changes are the round-3 fixes, plus timing noise in the speed probes.
- **New probes:** t01–t09 and 6 `r4_*` repros. Each repro is compared on snapshot 4, snapshot 3 and base (`r4_outputs.txt`).

## Earlier findings
- **Round 3, all fixed:**
  - R3-F1 for local and parameter roots (`s02`, `s13c`).
  - R3-F2 (`s04`, `s04c`, `s04d`, `s13`, `s13b`, `s15`).
  - R3-F3 (`s06`, `s06b`, `r3_fresh_nested`).
  - R3-F4 for `{...b.rec}` and `{...b.meta}` (`r3_spread_member_record`).
  - R3-F5 (`s03b`, `r3_adapter_defaults_rest`).
- **Rounds 1–2:** unchanged from round 3. Fixed ones stay fixed, intended ones stay as intended, and the flows end in explicit TypeErrors. Same out-of-scope items.

## New findings
Repros are in `/tmp/h10b/review4/r/`. "Base" is HEAD before H10b.

**R4-F1 · High · silently wrong where HEAD was right.** Function identity is still lost when the `C<any>` root isn't a local or a parameter.
- Repro: `r4_identity_non_local_roots.pts`, with `Emitter<any>` objects only and no `instanceof`.
- Node and base print `0 0 0 0 0`; snapshot 4 prints `1 2 2 2 1`. So `off(f)` removes nothing when the emitter is reached through:
  - a field (`svc.bus`, and `this.bus` inside a class)
  - an array element (`buses[0]`)
  - a call (`busOf()`)
  - a getter
- `t01` shows the same, while local, parameter, `?.`, `| null`, subclass and generic-subclass roots keep identity.
- Root cause: `anyInstanceFunction` (`any_instances.go`) falls back to the as-is adapter whenever `!isPure(rootValue)`. A field read, an element read or a call is not pure, so the function is wrapped in a new closure. This is the most common emitter shape, `this.bus.on(h)` and `this.bus.off(h)`.

**R4-F2 · High · TypeError where HEAD and snapshot 3 ran.** The restricted generic-method route now catches H10a's dynamic locals.
- Repro `r4_flow_local_generic_method.pts`: `const m = o; m.map(f)` after `o instanceof Box`, for an `object` source and for a non-generic interface source.
- Repro `r4b_dynamic_local_generic_method.pts`: `const n = o.next!; n.map(f)`, and `const m = x` for a narrowed array element.
- Node, base and snapshot 3 print `9 6` and `3 5`. Snapshot 4 throws `Expected Box<any>, got Box<number>`.
- Root cause: `callsAnyInstanceGenericMethod` excludes only the narrowed reference itself (`isUninferredNarrowing`) and receivers declared `any`/`unknown`. H10a held these locals as dynamic values (`isInstanceofDynamicLocal`), which dispatched at run time. Now they get the checked downcast to the instance for any.
- `t08` confirms the `unknown`, `any` and `catch` locals behave as on base (they threw there too).

**R4-F3 · High · silently wrong where HEAD (and snapshot 3) stopped at PTS1001.** An object spread of a dynamic value's member copies that member's arrays.
- Repro: `r4_member_spread_copies.pts`.
  - `{ ...b.meta }` for a `Box<any>` place holding a `Box<number>`.
  - `{ ...u.meta }` for an `unknown` narrowed to Box.
- Node shares `meta.tags` and prints `[2,3]`; snapshot 4 prints `[]`. Base and snapshot 3 were PTS1001.
- The place case breaks D-219's own rule, "never a silent copy of another instance's objects": it should be the as-is TypeError.
- Root cause: `spreadObject` (`objects.go`) uses `convertMember(…, ofAnyInstance)` with `ofAnyInstance = holdsAnyInstance(part.node.Expression())`. That is false for a member (`b.meta`), so the plain `convert` copies.
- For the `unknown` source the copy is D-179's, but the program used to be rejected and now loses writes.

**R4-F4 · Medium · regression (Box<any> objects only).** Member spreads through the new `spreadNames` case.
- Repro: `r4_member_spread_optional_method.pts`; part 1 alone is `r4b_member_spread_optional.pts`.
- Part 1: an optional property that is absent at run time gets added as a key. Snapshot 4 prints `Object.keys` = `name,opt` and `"opt" in` = `true`; Node and base print `name` and `false`.
- Part 2: a member whose type has a method is PTS1001 `an object literal whose spreads don't give every property`. Node and base copy it (`lit f,x 4`).
- Root cause: the new case in `spreadNames` (`destructuring.go:429-436`) lists every non-method property tsc knows and reads each one at run time. Missing optional keys are still written, and methods are dropped from a literal whose type requires them.

## Checked and fine
- Typed-context literals (`t04`): tuples, `| undefined`, `readonly`, generic contexts, spreads inside, nested, arguments, returns, property initializers and `map` results all work for `Box<any>` objects; a `Box<number>` gets honest TypeErrors.
- Into typed element types (`t07b`), behaviour is the same as base: D-071's intended copies.
- Narrowings still get PTS1001 or dynamic containers. `ident(o)` into a typed literal is an honest TypeError (`t09`).
- The generic-method route for parameters, locals, fields, elements and chains with `Box<any>` objects, and for narrowings themselves from interface, object, `unknown` and `any` sources (`t03`).
- `#f in o` and earlier private-member probes are unchanged.
- **Noted, not counted:** an `Emitter<number>` in an `Emitter<any>` place gets the adapter, so `off()` misses there too (`t02`). That is D-075's documented adapter behaviour (the C288 class), and HEAD was PTS1001 for it.

**VERDICT: NOT READY**

Files are in `/tmp/h10b/review4/`:
- `r/` (the six repros), `p/` (t01–t09), `old/` (the 170 earlier files)
- `r4_outputs.txt`, `t_outputs_snap4.txt`, `old_outputs_snap4.txt`
- `m777_snap4.jsonl`, `m748_snap4.jsonl`, `bin/`

The archive is `/tmp/h10b/h10b-review4-probes.tar.gz` (no binaries), and `/tmp/h10b/review4.done` is touched.

## Round 5 (snapshot 6: `5d0dd7618` plus the round-4 follow-up, committed as `5db31d972`; a fresh reviewer, round 4's having ended with its session)

# H10b review, round 5 (snapshot 6: `5d0dd7618` plus `/tmp/h10b/snap6.patch`)

**VERDICT: NOT READY**

The four round-4 fixes work for the cases they name. Both matrices and every earlier probe are unchanged. But I found six new problems, all silent:
- One is a regression in code that has nothing to do with H10b: `"k" in v` (R5-F1).
- Three are copies of another instance's arrays that D-219 says can't happen: rest patterns, spreads reached through a call or a cast, and destructuring assignments (R5-F2 to R5-F4). Base gave PTS1001 or a TypeError for each.
- One is a kept root that is evaluated twice (R5-F5).
- One is the key order of a spread (R5-F6, low).

## What I checked
- **777-case matrix on snap6:** 751 OK, 24 PTS1001, 2 TYPEERR. This is identical to round 4, with no class or detail changes.
- **748-case matrix on snap6:** 732 OK, 12 PTS1001, 4 WRONG. The 4 WRONG are the recorded R1-F13 cases (`K_{unknown,any}__{then_ref,pending_then}__number`). No changes.
- **Suite 6 (the parent's run, finished before my heavy jobs):** 5,810 pass / 381 skip / 0 fail, 10 packages PASS, empty stderr.
- **emit-c sweep, snap4 against snap6, 1,574 programs:** 1,565 produce identical C. Of the 9 that differ:
  - 5 are the new tests.
  - 3 are intended changes: an as-is member spread, `in` now looked up at run time, a kept root in `callback_own_methods`.
  - 1 is the corpus program `functional/transducers.pts`. It loses an order variant and its `pts_object_take_order` call. I checked this one: the program's output is unchanged, and with readings in another own key order snap6 still prints Node's order (`u22`).
- **Re-runs:**
  - The 170 files from rounds 1–3: 166 are identical to snap5, the other 4 differ only in timing.
  - Round 4's 20 files: identical to snap5.
  - The parent's 9 probes in `round5/`: as `h10b.md` describes.
- **New probes:** `u01`–`u24`, each compared on Node, snap6, snap4 and base. The six repros in `r/` were also run on snap5, which behaves the same as snap6 on all of them.

## Earlier findings re-checked
- **R4-F1 fixed.** `r4_identity_non_local_roots` matches Node. Stores, `i++` keys, key order against the right side, chained and value-used assignments, computed element calls, `as`, `!`, conditional, nested and comma roots, `super.bus`, `await`/`yield` roots, and `||=` `&&=` `??=` all match Node (`u06`, `u09`, `u24`). The one exception is R5-F5.
- **R4-F2 fixed.** `r4_flow_local_generic_method` and `r4b` match Node. The exclusion is never worse than base (`u07`, `u19`). The array-element, `ident(o)`, destructured, callback and annotated flows still throw D-219's documented R2-F5 TypeError, as on base. Snap6 also fixes two PTS1001s snap4 had (`as`, ternary).
- **R4-F3:** fixed for `b.meta`, `b.metas[0]`, `b.sub.inner` and `u.meta`. Still open for rests, calls and casts (R5-F2, R5-F3).
- **R4-F4:** `r4_member_spread_optional*` match Node.
- **PTS9001 on `buses[i++].on(f)`:** gone.
- **Rounds 1–3:** unchanged.

## New findings
Repros are in `/tmp/h10b/review5/r/` and all outputs are in `r5_outputs.txt`. Snap5 behaves the same as snap6 on every repro.

**R5-F1 · Medium · silent · a regression against base and snap4, also outside H10b.** `"k" in v` gives false for keys that library objects have on their prototype.
- Repro: `r5_in_declared_property_dynamic.pts`.
- Node, snap4 and base print `box members true true true true true` and `guarded true`.
- Snap6 prints `false false false false false` and `guarded false`.
- The keys tested are `size` of a Map, `has` of a Set, `length` of a function, `then` of a Promise and `push` of an array. The values are a `Box<any>`'s members, and also an `unknown` narrowed by a user type guard (`u is { size: number }`), which has no `C<any>` in it at all.
- Root cause: `optional.go:110-112` (new) sends every dynamic right operand to `DynamicHas`. The runtime's `pts_dynamic_has` (`runtime/src/dynamic.c:692-710`) only knows arrays' `length` and indices, and returns `default: false` for maps, sets, closures and promises.
- That runtime gap is older than H10b. `"then" in promise` on an `object` or `any` value is already false on base (`u02b`, `u23`). Before snap5, a declared required key was simply true.
- A possible fix: look the key up at run time only for optional properties, or fix `pts_dynamic_has`.

**R5-F2 · High · silent · since snap4.** A rest pattern of a member copies the member's arrays.
- Repro: `r5_rest_member_copies.pts`.
- Node prints `object 1 / place 2 / unknown 3 / tags ["o","p","u"]`.
- Snap6 and snap4 print `1 / 1 / 1 / tags []`.
- Base gives PTS1001 `spreading a value into an object` for the `object` narrowing. Snap1–3 gave PTS1001 too.
- The `object` form is the rest-pattern twin of snapshot 6's new `errors/generic_instanceof_member_spread_copies.pts`.
- The other two forms (a `Box<any>` place, an `unknown` narrowing) contradict D-219's text: "spreads and rest patterns … each value is taken as it is".
- Root cause: `restProperties` (`destructuring.go:463-466`) sets `ofAnyInstance` only when the pattern's own type is `C<any>`. It has no `narrowedByInstanceof` guard, so lines 495 and 497 convert plainly. `spreadObject`'s new logic (`objects.go:612-621`) wasn't applied here.

**R5-F3 · High · silent · since snap4.** R4-F3's rule doesn't reach a member obtained through a call, a cast or `find(…)!`.
- Repro: `r5_spread_call_cast_copies.pts`, with `{ ...b.getMeta() }`, `{ ...(b.meta as Meta) }` and `{ ...b.metas.find(…)! }` for a `Box<number>` in a `Box<any>` place, and `{ ...u.getMeta() }` for an `unknown` narrowing.
- Node prints `1 / 2 / 3 / 4 / tags ["c","x","f","u"]`.
- Snap6 and snap4 print `1 / 1 / 1 / 1 / tags []`.
- Base gives PTS1001.
- `const m: Meta = b.getMeta()` does give the as-is TypeError, so the spread path is inconsistent with it.
- Object-source narrowings get PTS1001 on snap6, which is correct (`u12`).
- Root cause: `objects.go:614` relies on `anyInstanceRoot` (`any_instances.go:175-184`), which only walks property/element accesses and `!`. A call or an `as` has no root.

**R5-F4 · High · silent · since snap1.** Two destructuring-assignment forms copy silently.
- Repro: `r5_destructuring_assignment_copies.pts`, with a `Box<number>` in a `Box<any>` place.
- Node prints `shorthand 1 / member 1 / member renamed 2 / renamed 2 / number ["s","i"] ["m","r"]`.
- Snap6 and snap4 print `1 / 1 / 1 / TypeError / number [] []`. The shorthand `({ items } = b)` and the member forms `({ tags } = b.meta)` copy, while only `({ items: it } = b)` gives the TypeError. Declarations are fine.
- Base gives PTS1001 for the place.
- Root cause:
  - `destructuringAssignment` (`destructuring.go:686`) uses `holdsAnyInstance(valueNode)`, which is false for members.
  - `assignShorthand` (`destructuring.go:805`) always uses `l.convert` and never takes `asIs`.

**R5-F5 · Medium · silent · new in snap5.** A kept root is evaluated twice when a suspension sits in the arguments and the root is deeper than the receiver.
- Repro: `r5_kept_root_suspension_twice.pts`, with `busOf().handlers.push(await f)`, `busOf().handlers[1] = await f`, a getter root, and `yield` too.
- Node and base print `yield get` and `push,store,get 0 2`.
- Snap6 prints `yield get,get` and `push,push,store,store,get,get 0 2`.
- Snap4 evaluated once but lost identity (`0 0`).
- Without a suspension, or when the root is the receiver itself, it's correct (`u05`, `u05b`).
- Root cause: `keepingAnyInstanceRoot` (`any_instances.go:195-198`) only checks `l.kept[root]`. The suspension hoisting keeps the callee or receiver node instead (`keepCallee`, `yield_expressions.go:576`), so the root is evaluated again, after the suspension.

**R5-F6 · Low · silent · since snap4.** A full spread of a `Box<any>` member copies keys in tsc's declaration order, not the object's own order.
- Repro: `r5_spread_key_order.pts`.
- Node and base print `{"count":1,"name":"m"}` and `count,name`.
- Snap6 and snap4 print `{"name":"m","count":1}` and `name,count`.
- Root cause: `orderOf` (`objects.go:607`) needs a typed object source. A spread with extra keys or absent optional keys has the same gap on base (`u04b`), and so do getter read orders (`u03`).

## Checked and found fine
- `copiedOwnProperty`:
  - It keeps earlier values and stacks spreads over spreads correctly.
  - An own `undefined` keeps its earlier value for a field that can't hold undefined. That is spreadOver's documented rule, and base does the same.
  - Rest patterns with defaults work.
  - Spreads before an `await`/`yield` work (`u03`, `u18`).
  - Getter objects are converted when they're written into typed fields, so own getters never reach the spread (`u16`).
- **Methods listed for dynamic object types:** a class instance's prototype method gives `Expected (…) => string, got undefined` at the spread, the same as base (`u01b`). Rests with methods are PTS1001 on base too.
- **Snapshot 6's new PTS1001:** it rejects read-only narrowed spreads that snap4 ran (`u08a`, `u08d`). Base rejected those too, so it rejects nothing that base ran.
- **Realistic store and registry** (`u15`): matches Node except a `Store<number>` in a `Store<any>` place, which loses identity through D-075's adapter (round 4's noted t02 class), and R5-F6's key order.

**Pre-existing on every build, not counted:**
- A function `la(rows: any)` given to `on`/`off` through `any`, or through a narrowing, loses identity (`u10`).
- `hasLen("abc")` with `T extends { length: number }` doesn't throw (`u17`).
- `"then" in promise` and `"size" in map` are false for `any` and `object` values (`u23`).

Files are in `/tmp/h10b/review5/`:
- the six repros: `r/`
- the probes `u01`–`u24`: `p/`
- outputs: `r5_outputs.txt`, `old_outputs_snap6.txt`, `new_outputs_snap6.txt`, `round5_probes_snap6.txt`
- matrices: `m777_snap6.{jsonl,log}`, `m748_snap6.{jsonl,log}`
- the sweep: `sweep/results.txt`
- tooling: `bin/`

The archive is `/tmp/h10b/h10b-review5-probes.tar.gz` (no binaries), and `/tmp/h10b/review5.done` is touched.

## Round 6 (snapshot 8: `5db31d972` plus the round-5 fixes, committed as `4f1b34621`; the round-5 reviewer, resumed)

# H10b review, round 6 (snapshot 8: `5db31d972` plus `/tmp/h10b/snap8.patch`)

**VERDICT: NOT READY**

All six round-5 findings are fixed for the cases they named. Both matrices, every earlier probe and the emit-c sweep are clean. One new silent copy remains: destructuring assignments whose target has a default (R6-F1). I'm keeping High for it, consistent with how round 5 rated its non-default twin R5-F4, even though it only bites another instance held in a `C<any>` place, or a narrowing. There is also one Low behaviour change in `in` (R6-F2).

## What I checked
- **777-case matrix on snap8:** 751 OK, 24 PTS1001, 2 TYPEERR. Identical to snap6 and round 4: no class or detail changes.
- **748-case matrix on snap8:** 732 OK, 12 PTS1001, 4 WRONG. Identical to snap6; the 4 WRONG are the recorded R1-F13 cases.
- **Suite 8 (the parent's run):** 5,818 pass / 382 skip / 0 fail, 10 packages PASS, empty stderr.
- **emit-c sweep, snap6 against snap8, 1,579 programs:** 1,571 produce identical C. Of the 8 that differ, 5 are the new tests. The other 3 are intended changes:
  - a PTS1001 whose column moves to the spread's `...` (`member_spread_copies`);
  - `pts_object_take_order_of_value` (`own_properties`);
  - `"k" in x` back to `(x, true)` for a required key (`object_operations`).
  - No other program changed.
- **Re-runs on snap8:**
  - The 170 files of rounds 1–3: 166 identical to snap6, the other 4 differ only in timing.
  - Round 4's 20 files plus the parent's 9 round-5 probes: 28 identical; p07 only moves its PTS1001 column.
  - My 41 round-5 files plus the parent's 3 round-6 probes: 22 changed, all of them the R5 fixes (now Node's output, the designed TypeError, or PTS1001) or PTS1001 columns moving. The parent's `p10b` behaves as designed; `p10`'s array pattern is a pre-existing PTS1001.
- **New probes:** about 45 files in `p/v01`–`p/v13`, each run on Node, snap8, snap6 and base.

## Round 5's findings re-checked
- **R5-F1 fixed.** Required keys are `true` again on every value kind, including the type-guarded `unknown` (`r5_in…`, `u02`).
- **R5-F2 fixed.**
  - Rests of a `Box<any>` place's members and of an `unknown` narrowing's give the as-is TypeError.
  - Rests of a narrowing's member are PTS1001, as on base.
  - The same holds for nested patterns, `for…of` declarations, callback parameters, parameter defaults, `for (let …)` and nested declaration defaults (`v03`; snap6 copied four of these silently).
- **R5-F3 fixed.** Spreads through `getMeta()`, a cast, `find(…)!`, `??`, `||`, `?:`, a comma and `await` give the as-is TypeError (`r5_spread…`, `u11`, `u12b`, `v07`; snap6 copied the `?:` and comma forms silently).
- **R5-F4 fixed, except with defaults (R6-F1).** Shorthand, member, renamed, rest and nested-rest assignments all give the as-is TypeError.
- **R5-F5 fixed.** The root is evaluated once and identity is kept in every form I tried: `&&`/`||`/`?:` with the branch taken or not, `??`, templates, spreads, comparisons, arguments, `+=`, `??=`, `&&=`, two levels deep, parenthesized, `!`, and `yield` forms (`v04`, `v04b`, `v09`). Snap6 evaluated each of these twice.
- **R5-F6 fixed.** Spreads of literals, frozen objects (the copy stays writable), records, absent optional keys and over-spreads all match Node or base (`v05`, `v05d`). A class instance in the member comes out in the type's order, but that is base's own D-108 gap (`v05b`, `v05c`).

## New findings
Repros are in `/tmp/h10b/review6/r/`.

**R6-F1 · High · silent · since snap1 (the narrowing form is silent on base too).** A destructuring assignment whose target has a default copies the member, so later writes are lost.
- Repro `r6_assignment_default_copies.pts`: `({ opt = [] } = b)`, `({ opt: o = [] } = b)` and `({ meta: { tags = [] } } = b)` for a `Box<number>` in a `Box<any>` place, and `({ opt = [] } = u)` for an `unknown` narrowing.
  - Node prints `["s","r","u"] ["n"]`.
  - Snap8 and snap6 print `[] []`.
  - The same assignment without a default gives the TypeError.
  - Base: PTS1001 (it can't hold the `Box<number>`).
- Repro `r6_assignment_default_narrowing.pts`: `({ opt = [] } = o)` for an `object` narrowing.
  - Node prints `1 [1]`; snap8, snap6 and base print `1 []`.
  - Without the default, snap8 gives D-218's PTS1001.
- Root cause: `assignParts` calls `withDefault`, which calls `dynamicWithDefault` (`dynamic.go:584`). That converts the present value with a plain `l.convert(ref, t, name)`, which is D-071's copy, before `assignShorthand`/`assignPart` reach `convertMember`. By then the value is no longer dynamic, so the as-is or PTS1001 rule never applies. Declarations with defaults are correct (they give the TypeError).
- Fix direction: pass the `memberCopy` rule into `withDefault`/`dynamicWithDefault` for the present value.

**R6-F2 · Low · silent · since snap5, a trade against base rather than a strict regression.** `"k" in v` for an optional key on a dynamic value is looked up at run time, and that lookup misses keys library objects have on their prototype.
- Repro: `p/v06/v06_in_optional_library_objects.pts`, with `unknown`s narrowed by type guards to `{ size?: number }`, `{ name?: string }`, `{ then?: unknown }` and `{ length?: number }`.
- Node: Map `true`, `{}` `false`; function `true`, `{}` `false`; promise `true`, `{}` `false`.
- Snap8 (same as snap6): the three library objects give `false`; the plain objects are right.
- Base: `true` for everything, which is wrong for each `{}`.
- Root cause: `optional.go:352-357` together with `pts_dynamic_has` (`runtime/src/dynamic.c:692-710`), which only knows arrays' `length` and indices and returns false for maps, sets, closures and promises. That runtime gap predates H10b (`"then" in promise` is false through `any` on base). Fixing it in the runtime would make both base's cases and these right.

## What snap8 now rejects (the parent asked for this)
- **Destructuring assignments from an `object` or interface narrowing that would copy an array are now PTS1001** (`v01`). The forms are renamed `({ items: it } = o)`, shorthand `({ items } = o)` and member `({ tags } = o.meta)`, whether they only read or also write.
  - Base and snap6 compiled all of them. They were right when only reading, or for a `Box<any>` object.
  - Base lost `Box<number>` writes silently in the renamed and member forms; snap6 lost them in the member form.
  - This matches D-218: `tags = o.meta.tags` and `const tags: any[] = o.meta.tags` are already PTS1001 on base (`v01b`).
- **Read-only rest patterns over narrowings are now PTS1001**, in declarations, `for…of` and callback parameters (`v01` rest forms, `v08a`, `v12a`, `v12c`). Snap6 ran them; base was already PTS1001.
- **Spreads, rests and assignments of a fresh method result give the as-is TypeError for a `Box<number>`** (`v02`). Snap6 had already thrown for the plain, annotated and declaration forms of the same value.

## Checked and found fine
- Destructuring assignments whose right side is a literal (`v10a`, `v10b`) and plain swaps: unchanged from snap6 and base.
- Element defaults where the default is actually used (`v03b`).
- Rests and spreads of the narrowing itself with scalar fields (`v08b`, `v08c`).
- The realistic Redux-style program with `Store<any>` (`v11`) matches Node exactly; snap6 read the getter one time too many.

Files are in `/tmp/h10b/review6/`:
- the two repros: `r/`
- the probes: `p/`
- re-run outputs: `old_outputs_snap8.txt`, `r4_outputs_snap8.txt`, `r5_outputs_snap8.txt`
- matrices: `m777_snap8.*`, `m748_snap8.*`
- the sweep: `sweep/results.txt`
- tooling: `bin/`

The archive is `/tmp/h10b/h10b-review6-probes.tar.gz` (no binaries), and `/tmp/h10b/review6.done` is touched.

## Round 7 (snapshot 9: `4f1b34621` plus the round-6 fixes, committed as `74d1cc4b1`; the same reviewer, resumed)

# H10b review, round 7 (snapshot 9: `4f1b34621` plus `/tmp/h10b/snap9.patch`)

**VERDICT: NOT READY, for one performance regression only.**

The behaviour is clean. R6-F1 and R6-F2 are fixed, and both matrices, every earlier probe and the emit-c sweep show no wrong answer. But the R6-F2 runtime change makes `in` up to about 24× slower on arrays, for every program and not only H10b's (R7-F1). The fix is small and local. If you decide to defer it to H27, as R1-F5 was, everything else is READY.

## What I checked
- **777-case matrix on snap9:** 751 OK, 24 PTS1001, 2 TYPEERR. Identical to snap8: no class or detail changes.
- **748-case matrix on snap9:** 732 OK, 12 PTS1001, 4 WRONG. Identical to snap8; the 4 WRONG are the recorded R1-F13 cases.
- **Suite 9 (the parent's run):** 5,822 pass / 383 skip / 0 fail, 10 packages PASS, empty stderr.
- **emit-c sweep, snap8 against snap9, 1,582 programs:** 1,579 produce identical C.
  - 2 of the 3 that differ are the new tests.
  - The third is `corpus/functional/transducers.pts`. Its C is nondeterministic on every build, base included: over 40 runs, base gives 30/10 between two outputs, snap6 36/4, snap9 37/3. So it's pre-existing.
  - Correction to round 5: I attributed this program's C change to snapshot 6; it was this nondeterminism. Both outputs print the same.
  - The runtime change produces no C difference, so the sweep can't see it; the suite and probes cover it.
- **Re-runs on snap9, against snap8:**

  | Files | Identical | Changed |
  |---|---|---|
  | 170 from rounds 1–3 | 166 | 4, timing only |
  | 29 from round 4 and the parent's round-5 probes | 29 | 0 |
  | 44 from round 5 and the parent's round-6 probes | 42 | 2, both now Node's `in` answers |
  | 47 from round 6 | 41 | 6, all R6-F1/R6-F2 fixes |

- **New probes:** `p/w01`–`w05`, run on Node, snap9, snap8 and base. Round 6's repros are also in `r/`.

## Round 6's findings re-checked
- **R6-F1 fixed.**
  - `r6_assignment_default_copies`: every form with a default (shorthand, renamed, nested, `unknown`) gives the as-is TypeError for a `Box<number>`. A `Box<any>` shares its arrays (the new test).
  - `r6_assignment_default_narrowing` and a narrowing's nested default (`w04b`) are PTS1001. Base and snap8 lost the write silently there.
  - Defaults that are actually used, member targets (`{ opt: holder.list = [] }`), scalar parts and a narrowing's scalar defaults all match Node (`w04`, `w04c`).
  - Evaluation order of target references, getters and defaults matches Node for typed sources and `Box<any>` places, including nested defaults (used or not) and a default that reads an earlier target (`w03`).
- **R6-F2 fixed.**
  - `v06` now matches Node on every line.
  - `in` through `any` across 16 library kinds and 100 keys (`w01`): 37 answers that base got wrong now match Node, and none newly disagrees.
  - The 23 still wrong are false negatives that base shares: `Object.prototype` members (`toString`, `constructor`, `hasOwnProperty`, `valueOf`), a function's `prototype` and `toString`, typed arrays' `length`, `map`, `set` and `byteLength`, `Set.prototype.union`, `copyWithin`, `toLocaleDateString`, and a tuple's `push` (arrays now answer `true` for it).
  - Computed keys on typed values (`k in arr`, `k in map`, a capabilities filter, `w02`) match Node; base and snap8 didn't.
  - The method tables hold only names JS has on each kind, and WeakMap/WeakSet aren't implemented, so there are no false `true`s.

## New findings

**R7-F1 · Medium · not silent (performance) · new in snap9.** `in` on arrays, maps and sets is much slower.

Repros: `p/w05/w05_in_index_hot_loop.pts` (`i in arr` over 5,000,000 keys, typed and through `any`) and `w05c_in_object_hot_loop.pts`.

| Case | base | snap8 | snap9 |
|---|---|---|---|
| `i in arr`, 5M keys, typed | 79–92 ms | 61–72 ms | 1,486–1,853 ms |
| `k in map` through `any`, 3M keys | ~30 ms | ~30 ms | 234–264 ms |
| `k in obj` through `any` | ~117 ms | ~110 ms | ~135 ms |
| `k in rec` (record) | unchanged | unchanged | unchanged |

- The answers are the same on every build.
- Root cause: `pts_dynamic_has` (`runtime/src/dynamic.c:692`) computes `pts_builtin_method_has(object, key_text(key, buffer))` before the `switch`, for every key.
  - `key_text` calls `pts_value_to_string`, which formats a numeric index into a new GC string on each call.
  - The lookup itself is a linear `strcmp` over up to 40 names.
  - Plain objects now call `key_text` twice per `in`.
- Fix direction: test `size`, `length`, indices and own keys first; look up builtin names only after those fail, and only for string keys.

## Out of scope, pre-existing on base (for the record)
- **Array-pattern defaults in assignments are ignored when the element is present but `undefined`** (`w03b`). Silent.
  - `[a = 1] = [undefined]` leaves `a` undefined; Node gives 1.
  - `[c = 3, d = 4] = [undefined, 5]` from `any[]`: same.
  - Past the end of the array the default works, and object-pattern defaults work.
- **`new Array<number>(n)` has no holes:** `0 in a` is `true` (`w05b`).
- **emit-c of `transducers.pts` is nondeterministic** (see the sweep above).

Files are in `/tmp/h10b/review7/`:
- round 6's repros: `r/`
- the probes: `p/w01`–`w05`
- re-run outputs: `*_outputs_snap9.txt`, plus `r6_outputs_snap8.txt` for comparison
- matrices: `m777_snap9.*`, `m748_snap9.*`
- the sweep: `sweep/results.txt`
- tooling: `bin/`

The archive is `/tmp/h10b/h10b-review7-probes.tar.gz` (no binaries), and `/tmp/h10b/review7.done` is touched.

## Round 8 (snapshot 10: `297a6aa43`, R7-F1's runtime fix; the same reviewer, resumed)

# H10b review, round 8 (snapshot 10: `297a6aa43`, runtime only)

**VERDICT: READY**

R7-F1 is fixed: `in` on arrays and objects is back to base speed. The `in` answers are byte-for-byte unchanged from snap9, and the new C is memory-safe under ASan/UBSan. One Low edge case remains: a key with embedded NULs on a generator (R8-F1). It's a one-line fix, and I don't think it should block.

## What I checked
- **Snapshot:** the three runtime files in `/tmp/h10b/snap10` match commit `297a6aa43`.
- **Timings:** `w05` and `w05c` built on base, snap9 and snap10, three runs interleaved on an idle host (load 0.9). Results are in `timings.txt`.
- **`in` answers:** 11 programs on snap9 and snap10: my `w01` and `w02`, `u02`, `u02b`, `u23`, `v06`, `r5_in_declared_property_dynamic`, the two new `in` tests, and the parent's `p05`/`p06`.
- **Memory safety:**
  - I read the new C.
  - New probes against Node, snap10, snap9 and base:
    - `x01`: embedded NULs, empty and 5,000-byte keys, Unicode, numeric strings and numbers, on 11 kinds.
    - `x03`: booleans, `null`, `undefined` and object keys.
    - `x04`: the generator buffer at 14–17 bytes, NULs at each position, async generators, a 100,000-byte key.
    - `x02`: symbol keys.
  - `x01`, `x03` and `x04` built with the Sanitize profile (ASan + UBSan) through a `go test -overlay` test that adds nothing to the repo. The runtime's own unit tests ran under sanitizers in the same run.
- **Matrices:** only one case of the 1,525 uses `in` (`A_union_ifaces__pass_src`). I re-ran it: OK on snap10, as on snap9.

## R7-F1 re-checked: fixed

| Case | base | snap9 | snap10 |
|---|---|---|---|
| `i in arr`, typed | 42–56 ms | 1,021–1,090 ms | 38–51 ms |
| `i in arr`, through `any` | 39–42 ms | ~1,030–1,090 ms | 36–38 ms |
| `k in obj` | 71–91 ms | 94–120 ms | 70–84 ms |
| `k in rec` (record) | 63–67 ms | 62–71 ms | 61–64 ms |
| `k in map` | 20–22 ms | 146–152 ms | 52–71 ms |

The Map case is the cost of answering method names correctly, as the parent recorded: base answered `false` for all of them.

## `in` answers
- All 11 programs print identically on snap9 and snap10.
- In `x01`, snap10 also removes snap9's false `true`s from text truncated at a NUL. `"push\0x" in arr`, `"get\0" in map`, `"exec\0" in re` and `"getTime\0" in date` now all match Node.
- Every number, numeric string, empty, long and Unicode key matches Node on every build.

## Memory safety
- **Non-generator path:** it compares the first byte, then `strlen(methods->name) == byte_len`, then `memcmp` over `byte_len` bytes. It never reads the key past `byte_len`, never runs `strlen` on it, and handles empty keys first. That matters because `pts_string.bytes` points into a shared append buffer and isn't NUL-terminated.
- **Generator path:** a key of 16 bytes or more returns false before the `memcpy`, and the buffer is NUL-terminated.
- **Non-string keys:** these never reach the method lookup. Symbols can't get there at all: `Symbol(…)` is PTS1002, and `Symbol.iterator` with `in` is PTS1001.
- **Sanitizers:** all three probes exit 0 with no ASan or UBSan report, and print exactly what the plain builds print. The runtime unit tests pass under sanitizers too.

## New findings

**R8-F1 · Low · silent · from snap9's generator path (not new in snap10).** A key that is `"next"`, `"return"` or `"throw"` followed by NULs, and under 16 bytes in all, reports `true` on generators and async generators.
- Repro: `p/x04_in_generator_boundaries.pts`.
- `"next\0" in gen` and `"next" + 11 NULs` give 1 on snap10; Node and base give 0. At 16 bytes snap10 gives 0.
- Root cause: `pts_builtin_method_named` (`dynamic_methods.c`, the generator branch) copies the key into a NUL-terminated buffer and calls `pts_iterator_has_method`, which uses `strcmp`. A NUL inside the key cuts the comparison short.
- It's memory-safe.
- Fix direction: return false when `memchr(name->bytes, 0, name->byte_len)` finds a NUL, or compare by length as the table path does.

**Note, not counted.** An object used as an `in` key whose string form names a method now answers as base does: `["push"] in arr` is `false` on base and snap10, `true` on snap9 and Node. Snap9 got it right only because it converted every key to text, which was R7-F1's cost. Index-valued object keys (`["0"] in arr`) are false on every build.

## Pre-existing, not counted
- `"\0push" in { "": 2 }` is `true` on every build, base included: the object path's `key_text` truncates at the NUL.
- Still missing on every build: a typed array's `length`, the `Object.prototype` members, and the other gaps listed in round 7.

Files are in `/tmp/h10b/review8/`:
- the probes: `p/` (`x01`–`x04`, plus `w05` and `w05c`)
- the timings: `timings.txt`
- the `in` comparisons: `in_snap9.txt`, `in_snap10.txt`
- the sanitizer runs: `san/` (the overlay test, the emitted C, the `*.san.out` files)
- the one matrix case: `m777_in_cases.jsonl`

The archive is `/tmp/h10b/h10b-review8-probes.tar.gz` (no binaries), and `/tmp/h10b/review8.done` is touched.
