# H09b2 review (reviewer, claude-opus-5-5, xhigh)

Saved by the parent from the child's final messages, unedited below each rule. Read-only reviews of byte-checked snapshot worktrees of the change against the base `6093263fe` (`/tmp/h09b2/base`) and Node.

---

# H09b2 review, round 1 (claude-opus-5-5, xhigh)

The slice fixes C187 and C186 on every route its tests name, and I found no crash or misread there. Lies of every kind I tried now throw TypeError instead of crashing. I found no races. But typed code can still misread dynamic values through routes the lowering change misses (F1). `leavesOut` also introduces a significant semantic regression in honest programs that use no `any` (F2).

Probes are in `/tmp/h09b2/review1/probes.tar.gz`, with `INDEX.txt` inside. Observed outputs are in `/tmp/h09b2/review1/outputs.txt`. Every probe runs with `/tmp/h09b2/review1/tri.sh <file>`, which prints base, snap2 and Node output.

## F1 (high, memory safety, verified): dynamic values still reach union-typed places unchecked
`isErasedResult` (`pts/lower/dynamic.go:58`) and `storedDynamic` (`dynamic.go:34`) only recognise the exact syntactic shapes they were written for. Everything else still takes the unchecked `ToUnion` at `pts/lower/lower.go:1015`.

**Erased union-call results inside `||` or a comma.** `unionLogical` converts `ref` with the whole `||` node (`unions.go:555`), and the comma lowers to `ir.Sequence` (`lower.go:2800`); neither check looks inside those.
- `p/a03_0.pts`: `const xs: number[] | undefined = pick(0).list<number>(seven) || undefined`. Base 15, snap2 15, Node 14.
- `p/a03_1.pts`: comma form. Base 15, snap2 15, Node 14.
- `p/a03_4.pts`: the `||` form with a string lie. Base and snap2 both exit 139 (SIGSEGV); Node prints `undefined`.
- `&&`, `??`, `?:`, return, argument, array literal and assignment are fixed (`a01`, `a03_2`, `a03_3`).

**Narrowed `unknown`/`any` values that are not plain identifiers.** This covers class and interface fields, `unknown[]` and tuple elements, `Record<string, unknown>` values and a parameter's field. It also covers narrowed identifiers inside `||` or a comma.
- `p/a05_narrowed_fields.pts`: honest parsed JSON. All 7 lines print 7.125 on both base and snap2; Node prints 6.5.
- `p/a04_narrowed.pts`: `data || "x"` and the comma form print 7.125 on snap2; Node prints 6.5.
- `p/a06_guard_lie_crash.pts`: a lying guard on `c.data` crashes both base and snap2 (exit 139). The identifier form `a07` now throws TypeError on snap2, which shows the fix only covers identifiers.

All of this is pre-existing, but D-192 and CONSOLIDATED claim "a typed read never misreads" and "narrowed dynamic values go into union-typed places with a check".

Suggested fix: decide the check from where the value comes from, not from the node's shape. One option is to recurse through `Sequence`, `Bind`/`Conditional` and logical operands. Another is to treat a property or element whose declared type (before narrowing) is `any` or `unknown` as stored-dynamic. A third is to invert the rule so only trusted typed-collection reads (`find`, `get` and similar) skip the check.

## F2 (high, JS semantics regression, verified): `leavesOut` copies honest typed objects, including class instances
`leavesOut` (`unions.go:368`, called at `:206`) fires for any property that `from` doesn't provide to the member "as it is", not only for properties holding `any`. The runtime then copies the object, because arrays imply only their own element type and function types are compared by identity.
- **`p/c06_class_copied.pts`** (no `any`; `class Pack { kind; members: Dog[] }` given to `{kind:"pack"; members: Animal[]} | {kind:"empty"}`):
  - Base: `rex+fido / true / rex+fido` (matches Node).
  - snap2: `rex / false / rex+fido`. The class instance becomes a plain-object copy: `instanceof` is false and later pushes are not seen.
- **`p/c04_subtype_arrays.pts`**:
  - Base and Node: `true / 2 / 3 / true`.
  - snap2: `false / 1 / 2 / false`. Writes are lost in both directions.
- **`p/c05_honest_patterns.pts`**:
  - Callback with fewer parameters: base `true 6` (as Node), snap2 `false 6`.
  - `number` where the member says `number | string`: base throws TypeError, snap2 prints a stale `false 5`, Node prints `true 6`.
  - In both cases, if the object were kept as it is, snap2's own read rule would read it correctly (it adapts closures and boxes numbers).
- **Cost** (`bench/leaves.pts`): every conversion copies the whole array. 20,000 calls with a 1000-element array take 0.00 s on base and 0.86 s on snap2.

None of this is in D-192's known gaps; its example is only an `any[]` field. Suggested fix:
- Restrict `leavesOut` to properties whose type holds dynamic values (a `holdsUnverified`-style test).
- Never silently turn a class instance into a plain object: report PTS1001, or keep the instance.
- The covariant `Dog[]`-as-`Animal[]` read then needs a decision, because the new read rule would throw where the base read it (the write side is the array-covariance hole).

## F3 (medium, verified): copies into the covering member drop frozenness, absence and aliasing
- **Frozenness (D-157)**, `p/b04_frozen.pts`:
  - Base and Node: `true 14`, and a write throws TypeError.
  - snap2: `false 14`, and the write succeeds (prints `none`).
  - `b05` shows interface copies already thawed on base; the new union copies extend that.
- **Absent optional properties**, `p/b11_optional_erased.pts`, "through the union": base `false tag,xs` (as Node), snap2 `true tag,value,xs`. Cause: `source_property` (`convert.c:568`) reads fields through `pts_value_of_slot`, which turns an absent value into `undefined`, so `copy_object` makes it present.
- **Aliasing with the erased side**, `p/b06_alias.pts`: base matches Node (`2 / 2 7,8 / 3 / true`); snap2 prints `2 / 1 7 / 2 / false`. This follows D-071's model, but D-192 should list it.

## F4 (medium-low, compile regression, verified)
`p/c01_leavesout_getter.pts`: base runs and matches Node. snap2 fails to compile with PTS1001 "an object type known only from object literals with getters or setters". The cause is that `leavesOut`'s `dynamicConversion` prepares every member of the union, including a getter literal's type.

## F5 (low-medium, perf, verified)
A megamorphic site that misses on a field whose declared type differs from the site's type now runs a full `pts_check_implies` on every miss (`object.c:83`). The profile shows the cost in `descriptor_implies` and `strcmp`.
- `bench/mega2.pts`: 151 → 784 Ir per iteration.
- `bench/mega3.pts` (union-typed field): 280 → 1228 Ir per iteration.

This only arises with objects that came through `any` (the static forms are PTS1001). The parent's `mega.pts` doesn't catch it because there the declared type equals the site's. A per-thread cache keyed by (declared, check), like `covering_cache`, would fix it.

## F6 (low, perf, verified)
- `covering_cache` (`convert.c:380`) is direct-mapped with 64 entries, so it thrashes once a loop uses many (union, descriptor) pairs.
- Above 256 unions, the shared table (`properties_of`, `:207`) is full, and every lookup probes all 256 slots, then recomputes the union's properties.
- Measured: `race/b40` 2408 → 3037 Ir per conversion (+26%); `b300` 2401 → 3645 (+52%).

## F7 (low, undocumented gap, verified)
An erased copy's object literal with both a method and a `T[]` property can't be copied (`copy_object` refuses checks that have methods), so it is kept as the non-covering member and the typed read throws. `p/a08_methods.pts`: base misreads `14 15`, snap2 throws TypeError, Node prints `14 14`. This is better than a misread, but it belongs in D-192's known gaps.

## F8: test quality
- `union_conversion_member_choice.pts` passes on base (the parent's `red-head.txt` shows the same), so it doesn't pin the new rule. Adding an `any` object with an `any`-typed field (`p/d01_member_order.pts`, line "two": base TypeError, snap2 and Node `5`) would fix that.
- No test pins the identity of honest class instances given to unions; such a test would have caught F2.
- In snap2, `generic_method_odd_element_arguments.pts` failed in the suite on "never[] value" (also my run). The main tree has since dropped that line and logged C194, but no skipped test reproduces it.
- The other new tests fail on base for the stated reasons and pass on snap2.

## Checked, no finding
- **Races:** `race/u40` and `race/u300` run 40 times each at `PTS_THREADS=8` were all consistent. The CAS publication in `properties_of` and the thread-local cache look correct on reading.
- **Lies:** `a10` tried 23 kinds (tuple, nested object, record, closure, optional, getter, Map, Set); all throw TypeError and none crash.
- **Semantics as before:** map and set copies keep keys, order and object keys; typed maps stay the same object (`b03`). JSON member choice and `in` narrowing match Node (`d02`). Shapes (`b01`, `b08`) and union-call forms (`a11`) are fine.
- **H09b1's restriction:** narrowing `verifiesObjects` gave no new TypeErrors versus base (`c03`).
- **Hot paths:** cache hits are unchanged.

Files are in `/tmp/h09b2/review1`:
- `probes.tar.gz`, with `INDEX.txt` inside
- `outputs.txt`
- `tri.sh`

---

# H09b2 review, round 2: snap3 re-check

All seven round-1 findings are fixed or documented in D-192, and the snap3 suite is green (1,799 pass, 307 skip, 0 fail). The fix still leaves two ways for typed code to misread dynamic values: a hole in the new trust list (G1, high, can crash) and the remaining unchecked fallback (G4). The newly checked routes also cause a regression for honest class instances (G2). G3 and test coverage are lower.

Probes are in `/tmp/h09b2/review2/probes.tar.gz` (with an `INDEX.txt`). Every probe runs with `/tmp/h09b2/review2/tri.sh <file>`, which prints base, snap3 and Node output; I re-ran all 35 round-1 probes (`rerun1.txt`).

## Round-1 findings on snap3

| | Status | Evidence |
|---|---|---|
| **F1**: unchecked routes into unions | Fixed | `a03_0`/`a03_1` (`\|\|`, comma), `a04`, `a05` (fields, `unknown[]`/tuple elements, record values, a parameter's field) and `a11` match Node. The lies in `a03_4`, `a03_5`, `a06` and `a07` now throw TypeError where base crashed with SIGSEGV. |
| **F2**: `leavesOut` copying honest objects | Fixed as decided | `c05` matches Node on every line. `c04`, `c06` and `bench/leaves.pts` now throw TypeError at the covariant `Dog[]`-as-`Animal[]` read; D-192 and `union_member_covariant_arrays.pts` document this. |
| **F3**: frozenness, absence, aliasing | Fixed | `b04`, `b09` and `b11` match Node. Aliasing (`b06`) is listed in the known gaps. |
| **F4**: PTS1001 regression | Fixed | `c01` compiles and matches Node. |
| **F5**: megamorphic miss cost | Fixed | `mega2`: 151 on base → 784 on snap2 → 194 on snap3 instructions per iteration. `mega3`: 280 → 1,228 → 327. |
| **F6**: union cache thrash | Fixed | `b40`: 2,362 instructions per conversion (base 2,408). `b300`: 2,555 (base 2,401). `race/u1200` overflows the 1,024-slot table and stays correct. |
| **F7**: erased object with a method and a `T[]` | Documented | `a08` unchanged; now in the known gaps. |

## G1 (high, memory safety): `trustsDynamic` trusts every `find`/`findLast` helper, whatever the array holds
The helper case in `pts/lower/dynamic.go` (`trustsDynamic`, `*ir.Call`) only checks the method name. It never checks the receiver's element type, which the `ArrayCall` and `MapCall` cases do check.
- **Wrong values:** `p/e01_find_predicate.pts` calls `arr.find(isNumbersOrText)` on an `unknown[]` or `any[]` holding parsed JSON:
  - `find`, `findLast`, `find` on `any[]`, find into a variable and an arrow guard print 7.125 on both base and snap3; Node prints 6.5.
  - An array-only guard into `number[] | undefined` prints 7.5; Node prints 7.
- **Crash:** `p/e02_find_lie.pts`, with a guard that lies, exits with SIGSEGV (139) on both base and snap3; Node prints `undefined` and `done`.
- **Doc gap:** D-192 says only "a collection of typed elements" is trusted, so the code contradicts it.
- **Fix:** trust the helper only if `key.array` exists and its element type holds no dynamic values (`holdsDynamicValues`).
- **Test:** add these lines as regression tests.

## G2 (medium-high, semantics regression): the newly checked routes copy honest objects, class instances included
`await`, generator values and `.then` results now go through the runtime's union conversion. That conversion copies into the member that declares an object's properties whenever the object's declared types don't imply that member's. This turns a class instance into a plain object, while the static path keeps it, as D-192 says it should.

| Probe | Base | snap3 | Node |
|---|---|---|---|
| `p/e06_checked_honest.pts`: await / generator / then / await with `any[]` members | `true true` throughout | `false false`, `false false`, `false`, `false false` | same as base |
| `p/e08_await_class.pts`: `async (): Promise<Item>` returning `new Product(5)` (`price: number` vs member `number \| undefined`) | `true function`, then a TypeError at the read | `false undefined`: no `instanceof`, no methods | `true function` |
| `p/e07_checked_honest_literals.pts`: `{v: 5}` through `await later<Val>(o)` | dies at the read with a TypeError | `false 5`: a stale copy | `true 6` |

`reduce` is unaffected. D-192 says "a class's instance … stay as [it is]", which is only true for the static path, and the known gaps don't list this.

Two possible fixes:
- Never copy a class instance into a plain object in the union conversion; take it as-is as a member it fits and let the reads verify, as the static path does.
- Accept the covering member as-is when its properties can be read as-is (closures adapted, a number boxed into a union).

## G3 (medium): an interface-typed value whose object is a class instance holding `any[]` is copied into a plain object
`leavesOut` excludes a class only when the static type is a class. `p/e04_iface_class_copied.pts` passes `Impl implements Loose { value: any[] }` through a `Loose` variable into `Opt`:
- Base: `same true instanceof true`, sees the push, `method function`.
- snap3: `same false instanceof false`, push not seen, `method undefined`.
- Node: same as base.

The known gaps mention aliasing for "an interface's object holding an `any[]`", but not the loss of the class and its methods. The runtime fix suggested for G2 would cover this too.

## G4 (medium, memory safety, documented as "unchecked"): a union dynamic data can't convert to still misreads
The fallback at `pts/lower/lower.go:1015` (`prepareDynamicTarget != ""` → unchecked `ToUnion`) still misreads.
- `p/e03_unprepared_union.pts`: an `unknown` narrowed into `typeof getterLiteral | number[]` prints 5.5 on both base and snap3; Node prints 5.
- D-192's example of this case, "a generator's" union, actually works (`e03` line 2), so the wording understates the risk.
- **Suggestions:** emit an as-is-only check for such unions, or report PTS1001; at minimum, qualify D-192's "a typed read never misreads".

## Other checks
- **No new compile errors:** I ran `emit-c` on all 571 run tests with both builds and saw no new PTS1001 or PTS9001. Seven tests gain conversions; the two not written for H09b2 are dynamic method results in `union_mixed_members` and `call_suspension_narrowed_callees`, and the suite is green.
- **Checked unions behave:** `e09` covers recursive, Map, tuple-with-optional and function unions and matches Node; base misread the tuple and printed pointer bits.
- **No problems found:** `e10` (frozen map and set copies), `e11` (narrowed `unknown` lists read by `pop`/`at`/index/`find`) and `e05` (`Object.values`/`Object.entries`, which stay dynamic) all behave.
- **No regression:** a generator loop of union class instances costs 994 instructions per iteration on base and 976 on snap3.
- **No races:** `race/u40`, `u300` and `u1200` at `PTS_THREADS=8`, 20 runs each, were all consistent.
- **Accuracy of D-192's numbers:** they match mine (40 unions 2,362, 300 unions 2,555). Its "cost what they did or less" is not true at 300 unions (+6%).

## Test quality
- The round-1 F1 routes are fixed, but no test covers them. `narrowed_unknown_into_union.pts` and `erased_objects_union_routes.pts` are unchanged since snap2. Add the `||`/comma lines from `a03`/`a04` and the field/element lines from `a05`, plus the G1 `find` lines.
- `union_conversion_keeps_typed_objects.pts` pins the static path only; adding an `await` or generator line would have caught G2.
- `generic_method_odd_element_arguments.pts` now passes on base too, so it guards only. `generic_never_value_call.pts` is skipped (C194) and reproduces as intended.
- The other new tests fail on base and pass on snap3 for the stated reasons.

The probe files are in `/tmp/h09b2/review2/`:
- probes.tar.gz
- INDEX.txt
- rerun1.txt
- rerun2_new.txt
- tri.sh

---

# H09b2 review, round 3 (snap5)

G1, G2 and G4 are fixed, and G3 now behaves as D-192 documents it. The snap5 suite is green (1,799 pass, 307 skip, 0 fail). I found no new memory-safety problem from the readable-as-is acceptance or from keeping class instances. I re-ran all 46 round-1/2 probes; every line that still differs from Node is a lie that now throws TypeError or a documented gap. There are four new findings, all low or low-medium.

Probes are in `/tmp/h09b2/review3/probes.tar.gz` (with `INDEX.txt`). Each one runs with `/tmp/h09b2/review3/tri.sh <file>`, which prints base, snap5 and Node output.

## Round-2 findings

| | snap5 | Evidence |
|---|---|---|
| **G1** | Fixed | `p/e01_find_predicate.pts` matches Node on every line (6.5, 7). `p/e02_find_lie.pts` now throws TypeError and prints `done`; base crashes with SIGSEGV. |
| **G2** | Fixed | `p/e06`, `e07` and `e08` match Node: `await`, generators and `then` keep class instances and literals. `p/e08` prints `true function` / `5` / `true`. |
| **G3** | Fixed as documented | `p/e04` keeps the class (`same true instanceof true`); the typed read of its `any[]` throws TypeError, which is in D-192's known gaps. |
| **G4** | Fixed | `p/e03_unprepared_union.pts` matches Node (5, 5). |
| **Tests** | Fixed | The new lines in `narrowed_unknown_into_union.pts` (find/findLast, getter literal) and `union_conversion_keeps_typed_objects.pts` (await, yield, then, awaited literal) fail on snap3 with exactly the round-2 symptoms (7.125, 5.5, `false`) and pass on snap5. All nine copied tests fail on base and pass on snap5. |

- **Costs:** they match the parent's figures. `b40` is 2,437 instructions per conversion, `b300` 2,468, `mega2` 194, `mega3` 327, and the generator loop 1,029 (base 994).
- **Races:** `race/u40`, `u300` and `u1200` (past the 1,024-slot table) at `PTS_THREADS=8`, 15 runs each, were all consistent.

## The readable-as-is acceptance and kept class instances: no safety hole
- **Writes go through the field's declared type** (`p/f02_readable_plain.pts`, `p/f05_readable_accesses.pts`): a narrower field converts or throws. Reads verify through element access, union keys, destructuring, `++` and optional chains.
- **JS views of kept objects stay faithful** (`f01`): `in`, `Object.keys`, `JSON.stringify` and console.log `[Getter]` all show the original object, and frozen objects stay frozen, with writes throwing as in Node.
- **Excluded cases:**
  - Iterator results are excluded.
  - The getter path drops the result-kind requirement only in readable mode, and the getter is read through `read_converted`.
  - Methods still need the same signature or a dynamic entry.
- **Instances under a non-covering member** (`p/f04_instance_noncovering.pts`): a `Pack` taken as `{kind: "empty"}` keeps identity, writes to `size` show in the original, `in` and `instanceof` work, and only the covariant `Dog[]` read throws, as decided.
- **`is_class_instance`:** an Error subclass is kept (`f08`); anonymous class expressions are PTS1001 anyway.

## New findings

### R3-1 (low-medium, regression for erased copies): copies take the member's declaration order
`p/f03_copy_order.pts`: an erased copy returns `{ value: [x], tag: "some" }`.

| | `Object.keys` | `JSON.stringify` / console.log |
|---|---|---|
| Base | `value,tag` | `{"value":[7],"tag":"some"}` |
| snap5 | `tag,value` | `{"tag":"some","value":[7]}` |
| Node | `value,tag` | `{"value":[7],"tag":"some"}` |

`copy_object` always uses the member's single copy shape (its declaration order). Parsed JSON already had this problem on base; erased objects are new. D-192's known gaps don't mention property order (C190 covers only getters). Either take the source's order the way D-108's `pts_object_take_order` does for spreads, or list it as a known gap.

### R3-2 (low): the readable relation misses optional callbacks and nested wider types
Such honest object literals coming back through `await`, a generator or `then` are still copied, so identity is lost.
- `p/f06_await_patterns.pts`:
  - "optional callback of fewer parameters", "nested wider" and "array of a narrower element" print `false` on snap5, while base and Node print `true`.
  - Optional wider, `number | undefined` and nested-with-more-properties are kept.
- `p/f01_readable.pts`: an object with `f?: (a, b) => number` is copied ("kept false"), so later writes to it are lost (`write a number 5`; Node prints 6).

Causes and context:
- **Optional callbacks:** `place_readable`'s function clause only applies when the property's check is a plain function type. An optional property's check is a union with `undefined`, so the clause never fires. Statically the same object is kept and works, because `pts_optional_get` adapts the closure (`p/f07_static_nested.pts`: 6).
- **Nested wider:** this also fails statically on both base and snap5 (TypeError at `.inner`/`.n`). Making `read_converted`'s as-is conversion use the recursive readable relation would fix both routes.
- **Arrays of a narrower element:** copied through `await` but a TypeError on the static path, which is consistent with the covariant-arrays decision.

### R3-3 (low, perf): reads of readable-kept objects never use the site cache
`bench/readable_loop.pts`: a loop reading `s.v` (`number` where `number | string` goes) costs 507 instructions per iteration on snap5, against 98 on snap3, which copied the object. Each read takes the slow path through `read_converted` and allocates a GC cell. Under 2% of the cost is `pts_check_implies`; most is the conversion, `strcmp`, the slow path and the allocation. It's worth noting for hot loops over discriminated unions.

### R3-4 (low, pre-existing, undocumented): writing the member's wider type into a kept object throws
`p/f02_readable_plain.pts`: `s.v = "six"` gives `TypeError: Cannot set property v of Object: expected number, got string`; Node prints `six`. Base threw even earlier, at the read. This is D-072's write rule applied to the objects D-192 now keeps; add it to the known gaps.

Files are in `/tmp/h09b2/review3`:
- probes.tar.gz
- INDEX.txt
- rerun.txt
- rerun_new.txt
- tri.sh

---

# H09b2 review, round 4 (snap6, delta only)

R3-2 is fixed, and I found no way for the new read-mode as-is acceptance to misread anything. The snap6 suite is green (1,799 pass, 307 skip, 0 fail). I re-ran all 54 probes from rounds 1–3 on snap6: only the three R3-2 probes changed from snap5, all for the better, and nothing regressed. One honest-program TypeError (H1) is not yet documented, and nested reads through kept objects are slow (H2).

Probes are in `/tmp/h09b2/review4/probes.tar.gz` (with `INDEX.txt`); each runs with `/tmp/h09b2/review4/tri.sh <file>`, which prints base, snap6 and Node output.

## R3-2: fixed
- **`f07_static_nested`** matches Node: the static nested read prints 5, and the optional callback prints 6.
- **`f06_await_patterns`**: everything is now kept (`true`), except "array of a narrower element", which is copied by decision (covariant arrays).
- **`f01_readable`**: the object is kept (`kept true`), and "absent optional" now matches Node. The one remaining difference is the documented R3-4: writing a string into the `number` field throws TypeError.

## The read-mode acceptance can't misread
Accepting an object as it is for an object type is safe because interface-typed values are only ever read through sites, and each site checks again against the object's own descriptor. Arrays, tuples, class instances, primitives and closures still need an exact match, an instance check, a conversion or an adapter. Iterator results are excluded.
- **Shape-typed places** (`g01_shape_types`): `typeof literal` as a local, a parameter, an interface property and an array element are all represented as interfaces. Objects of another layout, including one holding an `i32`, read correctly on both base and snap6.
- **Nested reads and writes** (`g04_nested_readable_noarray`): numbers, getters, an absent optional, a closure with fewer parameters, a class instance, a recursive list, writes back into the original, and a held nested object read later all match Node. The only differences are R3-4's documented throws on wider writes.
- **No loops:** recursion through unions and recursive types ends on `readable_as`'s coinductive stack. It shares that stack with `implies`, but only for readable questions, so site caching is unaffected.

## H1 (low-medium, regression since snap3, undocumented): one sibling property blocks every read through its parent object
`g03_sibling_blocks.pts`: an honest object (no `any`), `{kind: "t", inner: {n: 5, xs: [1, 2]}}`, is kept statically as `{kind: "t"; inner: {n: number; xs: (number | string)[]}}`. Reading `s.inner.n`, whose type matches exactly, throws:
- Base: `5`. snap2: `5` (it copied the object). Node: `5`.
- snap3, snap5 and snap6: `TypeError Cannot read property inner of Object as {n: number; xs: (number | string)[]}: it is declared {n: number; xs: number[]}`.

The read of `inner` requires every property of `inner` to be readable, and an array of a narrower element never is. So the whole sub-object becomes unreadable, even for properties the program actually reads. In `g02_nested_readable`, the same happens inside a larger object: all ten lines that read through `inner` throw on snap6, while base printed the narrower array's length, the absent optional and the callback result correctly.

D-192's known gap ("an array of a subtype's elements read through a union member as its supertype's throws") doesn't cover reads of the parent object or of its other properties. Two options:
- In read mode, accept any plain object for an object type, since each of its properties' sites verifies the declared type anyway.
- Or document this wider effect.

## H2 (low, perf): nested readable reads recompute the readability walk every time
`bench/nested_loop.pts` reads `s.inner.n` in a loop at 1,537 instructions per iteration. Most of the cost is the `object_fits` and `object_readable` walks plus a GC cell allocated per read. The R3-3 note in D-192 covers flat reads (about 500 instructions).

When a field holds an object and the read hands back the same pointer, a site could safely cache the (descriptor, offset) pair once that descriptor is known readable. That is the same reasoning that lets it cache implied pairs, since every property read still goes through its own site. A per-thread cache of the readability verdict would also work.

Files are in `/tmp/h09b2/review4`:
- probes.tar.gz
- INDEX.txt
- rerun.txt
- rerun_new.txt
- tri.sh

---

# H09b2 review, round 5 (snap7)

Both round-4 changes are fixed, and I found no way for a typed read of an object as an object type to misread anything. The snap7 suite is green (1,799 pass, 307 skip, 0 fail). I re-ran all 58 earlier probes on snap7: only `g02` and `g03` changed from snap6, both for the better. I have no new findings.

Probes are in `/tmp/h09b2/review5/probes.tar.gz`, with `INDEX.txt` inside.

## H1 and H2
- **H1, fixed:** `g03_sibling_blocks` now prints 5, matching base and Node. In `g02_nested_readable`, the only lines still different from Node are documented ones:
  - the covariant `xs` read throws TypeError;
  - writing a string into the `number` field throws (R3-4);
  - the adapter rejects the missing argument where Node gives `NaN`.
- **H2, fixed:**

| Benchmark (instructions per iteration) | snap6 | snap7 |
|---|---|---|
| `nested_loop` | 1,537 | 472 |
| `mega2` | 194 | 164 |
| `mega3` | 327 | 329 |
| `readable_loop` (the flat case R3-3 documents) | — | 507 |

## Why the cached reads can't misread
- **Only reads use the relaxed rule.** `pts_value_try_convert_as_is` is called only from `read_converted` and `optional_converted` (`object.c:117` and `:359`), so taking "any object" applies to reads alone. The new caching in `reads_as_is` requires the field to be an object kind and the site's type to be an object type that is not an iterator result.
- **Interface-typed values never bypass sites.** The code generator's reads, writes, `hasField`, method calls and iterator-result reads on them all go through the runtime's checked paths. A site that meets a cached descriptor again gets a pointer to an object, and each of that object's properties is still checked at its own site.
- **Non-plain objects fail loudly, not by misreading.** A value that is not a plain object (an array or Map held where an object type goes) has no fields in its descriptor, so a site reading it throws.
- **These paths were left alone:**
  - optional and mixed reads, which still go through `pts_check_implies`;
  - setters, which still require an exact type match;
  - frozen twins, which are separate descriptors and miss the cache.

What I ran to confirm it:
- **`h01`:** objects of other layouts (an `i32` where the type says `number`, properties in another order, a getter where a field goes) flow into a literal-shaped local, a class field initialised from a literal, a spread, destructuring defaults, `Object.assign`, JSON and keys, `in`, writes and `Object.freeze`. Base and snap7 print the same correct values.
- **`h02`:** a nested object the union keeps as it is, read through the cached site, then used in all the same ways. It prints Node's values except where the program reads the covariant `zs` array (the direct read, a spread of the whole object, and a push), which throws TypeError by decision.
- **`h03`:** interface method calls, getters and setters on the cached path, repeated three times, plus mixed-type union reads. All match Node, including the field read that base rejected with a TypeError.

Files are in `/tmp/h09b2/review5`:
- probes.tar.gz
- INDEX.txt
- rerun.txt
- rerun_new.txt
