# H09b4b1 review reports

The `reviewer` subagent's reports, saved by the parent from its final messages (it is read-only). Probes: one archive per round, linked under each round. Findings are claims until the parent reproduces them; the parent's disposition of each is in [h09b4b1.md](h09b4b1.md).

## Round 1 (snap1, then snap3)

Snapshots: `/tmp/h09b4b/s2/snap1` (the change as first reviewed) and `/tmp/h09b4b/s2/snap3` (after the parent narrowed the C230 rule twice during the round, which the parent told the reviewer about; snap3's code is the fix commit's). Archive: [h09b4b1-review1-probes.tar.gz](h09b4b1-review1-probes.tar.gz) (`review1/INDEX.txt` maps probes to findings, with a per-CLI status table).

> I found no new regressions on snap3, but I'd hold the slice: writes through a property that `in` or a constraint discriminant added still fail with PTS9001, so C222 and C223 are only fixed for reads.
>
> **What I checked against.** All four parts of the slice behave as Node on every shape I tried (brands, `in` reads, constraint discriminant reads, narrowed `?.` chains, honest array intersections, stale reads; table in the tarball). All 16 tests fail on base (only the guard `narrowed_never_defaults_that_run` passes there) and match Node on snap1 and snap3. Each probe below was run on Node, base, snap1 and snap3. Where I give one result for snap1/snap3, the two were identical. The `===` regression probes also ran on the main tree's CLI (sha cf045b17…), which still had the regression.
>
> **The snap1 regression (not counted as a finding).** On snap1, `o.n === 1` after `typeof o.n === "number"` was PTS1001 `converting number | string to number`. So were `this.id !== undefined && this.id === 0`, a numeric enum, and a module `let` assigned by another function. Base compiled all of them. Snap3 boxes the operand and fixes all of them: probes r01–r05, s01, t01–t03, k01 and s07 match Node on snap3. No test pins this yet; add r01/r03/r05 as a guard.
>
> ### Findings (all on snap1 and snap3; every one is pre-existing unless I say otherwise)
>
> **F1 · High · `in`/constraint writes are PTS9001.** Writes through a property that `in` or a constraint discriminant added are PTS9001 ("passing 'pts_object *' to parameter of type 'pts_value'"). This covers `x.k = 10` after `"k" in x`, `x.side += 1` / `x.side = 9` / `x.r++` in `T extends Sq | Ci` instances, and the generic `T extends object` form. Only reads were fixed.
> - In c03 the change makes things slightly worse: base stopped at PTS1001 on `kept = x`; that cast now compiles and the write then hits PTS9001.
> - The keyed form `x["k"] = 10` (i09) works, which points at the cause. Two dynamic place sites in `/tmp/h09b4b/s2/snap3/pts/lower/lower.go` pass the object unboxed:
>   - line 3298: `place{object: object, key: l.dynamicKey(node), dynamic: true}`
>   - line 3317: the same.
> - Fix: use `box(object)` at both, as `dynamic.go:161` already does. Add a test.
> - Probes: i01, i05, i08, c02, c03, c05, c06.
>
> **F2 · Medium · C223 with a union type argument is PTS1001.** When the type argument is a union narrower than the constraint (T = `Sq | Tr` for `T extends Sq | Ci | Tr`), reading `s.r` in the ci branch fails with "a property of an object type on a {…} | {…} is not supported yet". Node runs it, and a lie reaches it. Fix: when no member of the union has the property, read it like the shape path does (`valueProperty`, boxed). Probes: c07, c04.
>
> **F3 · Medium-low · Spread and destructuring are PTS1001.** Spread and destructuring of a value narrowed by `in` or a constraint fail: `{ ...x, side: x.side + 1 }` in the Ci instance, `const { k } = x`, `{ ...x }`. Probes: c10, i10.
>
> **F4 · Medium-low · `in` on a class is PTS1001.** `"bark" in a` for `a: Animal` (only a subclass has `bark`) gives "`a.bark` is not supported yet". C222 is fixed for object types only. Probes: i06, i02.
>
> **F5 · Medium · C226 in typed places is PTS1001.** It fails wherever the place's type takes undefined: `const n: number | undefined = o?.v`, a `number | undefined` argument, a return, an array element. The error is now `converting string | undefined to number | undefined` (base said `converting string to undefined`). The cause is `chains.go:148`: `!l.takesAnyValue(node)` skips the checked conversion whenever the context takes undefined. Suggested fix: in the `!holds` case, skip it only for any/unknown contexts and the left of `??`. I haven't tried this fix. Probe: o06.
>
> **F6 · Medium-low · C227 `for…of` in an instance is PTS1001.** `for (const s of x)` over T = `number[]` narrowed to `number[] & string[]` fails with `converting number to string`. The read keeps the instance's `number[]` while `typeAt(x)` says any. Spread and destructuring work. Probe: a06.
> - Separately, `for…of` over a mixed array narrowed to `string[]` is PTS1001 in plain code and in instances (a05). C228 only records aliasing and callbacks, so it should list this too.
>
> **F7 · Medium · `Record<Brand, V>` is PTS1001.** Index signatures keyed by a brand (`Record<UserId, User>`, `{ [k: UserId]: number }`) fail with "index signatures with keys of type string & {…}". This is a common brand idiom. Fix: unbrand the key type at `records.go:86`. Probes: b09, b07.
>
> **F8 · Low · A brand's own property is PTS1001 (changed by the slice).** `id.__brand` read directly is now PTS1001 "a property of an object type on a string". Base gave a PTS9001 or a runtime `TypeError: Expected string, got undefined`; Node prints undefined. D-196 covers only reads through `any`, so this should be listed as a known gap. Probes: b14, b01.
>
> **F9 · Low-medium · C230 misses `==`/`!=` and `switch`.** On a stale narrowed read, `o.v == "s"`, `o.v != "t"` and `switch (o.v)` still throw `Expected string, got number`, where Node gives false or the default case. Fix: count loose-equality operands and a switch's discriminant in `testsAnyValue`. Probes: s05, s02.
>
> **F10 · Low · Perf cost of the snap3 C230 rule.** Every `===`/`!==`/`typeof` operand of a narrowed property, and of a variable another function assigns, is now boxed and compared dynamically, stale or not. A micro-loop (`bench/t02small.pts`) went from 73,362,567 to 81,160,327 instructions under callgrind (+10.6%). Output is correct.
>
> ### Code quality (snap3)
> - **Q1:** `isNarrowedProperty` (`lower.go:2429`) uses `GetSymbolAtLocation` of the key expression. For `p[this.k]` that is `k`'s own property symbol, so the check is true for the wrong reason. It's harmless (k01 matches Node), but should look at a literal key only.
> - **Q2:** `holds` (`chains.go:174`) returns true for any non-union object `t`, and for a union `t` with a non-primitive `from`, without checking membership. Its doc comment doesn't say so.
> - **Q3:** `widestOfIRType` calls `brandedPrimitive` twice (`dead_code.go:252`).
> - **Q4:** the reflowed `literalFree` comment leaves an over-long line (`dead_code.go:291`).
> - **Q5:** `isReferenceNarrowedTo` also makes predicate intersections work (`A` narrowed by `x is B`, passed where `A & B` goes). n01/n03 now match Node with identity kept; base was PTS1001. The comment and D-196 describe only the constraint case; document and test this.
> - **Q6:** some tests repeat each other: `narrowed_array_intersections` and `array_type_intersections` share `f` and its expected line; `narrowed_optional_chains` repeats two functions of `optional_chains_narrowed_by_predicates`.
> - **Q7:** missing tests: the numeric `===` guard (r01/r03/r05); stale `===`/`!==` (s07, which works on snap3 but `narrowed_stale_test_operands` has no `===` case); predicate intersections (n01/n03); writes after `in` and constraint narrowing once F1 is fixed.
> - The constraint branch without a trap is consistent with D-195's rejected alternative, and the D-071 copy note holds: n02/n04 keep identity in the live instances.
>
> ### Out of scope, worth a ledger row
> **O1 · High · silent wrong output.** After `Array.isArray(a)` on an `a: any` (also `unknown`), every mutation goes to a fresh copy. `a.push(1); a[0] = 9` leaves the caller's array unchanged and `a.length` stays 1; Node mutates the caller's array. It is the same on base, snap1 and snap3. Ledger row C66 says this case was fixed in H04, so it looks like a later regression of C66 rather than anything in this slice. Probes: x01, x02, a07.
>
> Everything is in `/tmp/h09b4b/s2/review1-probes.tar.gz` (`review1/INDEX.txt` maps each probe to its finding and has the per-CLI status table; probes in `probes/`, outputs in `out/<probe>/`, the runner scripts and the bench file). I deleted the built binaries and caches. Probes s03, b05, b11 and b06 test things that are unsupported or intentional and are kept only for the record.
>
> VERDICT: not ready

## Round 2 (snap4)

Snapshot: `/tmp/h09b4b/s2/snap4`, main at `03cd1cba0` plus round 1's fixes (committed as the round-1 fix commit). Archive: [h09b4b1-review2-probes.tar.gz](h09b4b1-review2-probes.tar.gz) (`review2/INDEX.txt`).

> I'd hold the slice for one finding: the round-1 change to object spreads now gives silently wrong output in a case base and snap3 refused to compile. Every round-1 finding you said was fixed now matches Node on snap4, and I found no other regressions.
>
> ### 1. Round-1 probes re-run on snap4
> - **Now matching Node** (all differed on snap3): a06, b07, b09, c02, c04, c05, c06, c08, c10, i01, i05, i06, i08, i10, o06, s02 (the `==` part), s05.
> - **Still different, with changed output, as expected:**
>   - a03 and a05: only the C228 parts are left (callbacks and `for…of` over a mixed array).
>   - c03 and c07: the lies (objects without Sq's `side`, cast to Ci or Sq) now throw D-070's checked-cast TypeError.
>   - i02: only C233 is left (`converting C to A | B`).
> - **Unchanged and as recorded:** b01 and b14 (C232), a07, x01 and x02 (C231), a01 and a04 (C228). b06, b11 and s03 are out of scope or invalid, as in round 1.
> - **New tests:** each one fails on base and matches Node on snap4. The exception is `narrowed_property_equality`, which passes on base; that is its purpose, since it guards the snap1 regression.
>
> ### 2. Findings
>
> **R2-F1 · Medium · new silent wrong output (introduced by the round-1 change).** Spreading an object narrowed by `"k" in x` copies a prototype getter's value or a method in as an own property. The `missingProperty` fallback in `spreadObject` (`/tmp/h09b4b/s2/snap4/pts/lower/objects.go:612`) reads with DynamicGet, which follows the prototype. A JS spread copies own enumerable properties only.
> - Repro `probes/g03_in_spread_getter.pts`: an interface `NoK { m }`, a class `G` with `get k()`, a class `M` with a method `k()`, and `{ ...x }` after `"k" in x`.
> - Node: `copy.k undefined own m … copy.k undefined own m`.
> - snap4: `copy.k number own m,k … copy.k function own m,k`.
> - base and snap3: PTS1001.
> - Destructuring (`const { k } = x`) is correct, because it reads through the prototype in JS too.
> - There is no own-property read at run time to reuse: `{ ...anyValue }` is itself PTS1001 (probe `g09`).
> - Suggested fix: a run-time "own data property" read for spreads that gives undefined for accessors and methods. Even then `Object.keys(copy)` would still list k, because shapes have fixed fields. If that's too much, keep PTS1001 for spreads or record the gap with a skipped test.
>
> **R2-F2 · Low · worse than snap3 (introduced by the round-1 F5 fix).** A stale chain narrowed to undefined and passed where `string | undefined` goes now throws.
> - Repro `probes/w11_stale_chain_typed_context.pts`: `o: { v?: string } | undefined`, then `if (o?.v === undefined) { change(); return opt(o?.v); }`, where `change` sets `o.v`.
> - Node and snap3: `opt s opt undefined`. snap4: `caught Expected undefined, got string`. base: PTS1001.
> - Suggested fix: where the context takes undefined, convert the chain with a check to the context's type (`string | undefined`, `number | undefined`) rather than to the narrowed type. That keeps o06 working and runs w11 as Node.
>
> **R2-F3 · Low · pre-existing, C227's family.** An optional call on an element of an instance's `number[] & string[]` is PTS1001 `converting undefined to string`.
> - Repro `probes/a10_intersection_element_chain.pts`: `x[0]?.toUpperCase() ?? "none"` for T = `number[]`. Base gives a different PTS1001. The mixed-array control a11 matches Node.
> - Suggested fix: in `optionalChainFrom`, a dynamic object should widen the result with undefined, as `mayBeMissing` does.
>
> **R2-F4 · Low · pre-existing.** A rest element after `in` is PTS1001 with a misleading message: `const { m, ...rest } = x` gives "rest properties holding the method `k` are not supported yet", though k isn't a method. Repro `probes/d01_in_destructure_forms.pts`; without the rest element (d03) it matches Node.
>
> **R2-F5 · Low · pre-existing, C222's family.** On a union where one member declares k as a string and the other member's object holds a number after `"k" in x`:
> - `x.k` throws `Cannot read property k of Object as string: it is declared number`. tsc types `x.k` as unknown and Node reads 5. Probe `g04`, same on base.
> - A spread of a union narrowed by `in` uses one member's shape and throws. Probe `g07`, same on base.
>
> **Notes (not silent, so not findings, but new run-time behaviour worth a line in D-196).** Several former PTS1001s are now TypeErrors where Node succeeds:
> - Writing `a.bark` after `in` where the subclass has `bark` as a method or a number field (probe `g02`). That is D-072's checked writes, and class methods are fixed.
> - Writing an absent property on a class object through `C & Partial<Extra>`: "object is not extensible" (probes `w06`, `w08`). The read in w08 now matches Node.
>
> **Checked and matching Node on snap4:**
> - g01: getter, method and field subclasses read after `in`.
> - d02 and d03: constraint and `in` destructuring with defaults and renames.
> - w01: `switch` on a boxed discriminant with numeric enum, string enum, -0, NaN, non-constant cases, fallthrough, a default in the middle, and a hot loop.
> - w02 and w03: `for…of` over `as any` typed arrays, strings, maps, sets, generators, and over erased generic iterables.
> - w04: chains where every value goes (`console.log`, an unknown parameter, `??`, an `any` const).
> - w07 and w10: mapped intersections, including `Readonly<C> & Partial<X>`.
> - b15: `Record` keyed by a union of brands, a number brand, and `Partial<Record<Id, V>>`.
>
> ### 3. Code quality of the new code
> - **Q1:** The comments on `shapeProperty`'s new class and union branches (`objects.go:924–933`) and on `isMappedProperty` name only `in` and the constraint. The branches also cover any class object reached through a mapped or shape property it lacks, so `C & Partial<Extra>` reads now compile (w08). Say so in D-196 and add a test.
> - **Q2:** There are now four near-synonymous predicates: `takesAnyValue`, `takesEveryValue`, `takesAnyValueAsIs` and `testsAnyValue`. `takesAnyValue`'s doc says "any value" but it means "undefined or any/unknown". Renaming it to something like `takesUndefined` would make `optionalChainFrom`'s condition readable.
> - **Q3:** The `forOfStatement` comment names only C227, but it boxes every iterable typed as any. That's a no-op for values that are already dynamic; worth one more clause in the comment.
> - **Missing tests:** g03's spread behaviour (fixed, or skipped as a known gap), w11, and the w08 read.
>
> Probes, outputs and the runner are in `/tmp/h09b4b/s2/review2-probes.tar.gz`; `review2/INDEX.txt` maps each probe to its finding and has the per-CLI status table. I built no binaries and removed the caches.
>
> VERDICT: not ready (R2-F1 needs a fix, or a recorded gap with a skipped test; everything else is low or already recorded)

## Round 3 (snap5)

Snapshot: `/tmp/h09b4b/s2/snap5`, main at `05bfdc261` plus round 2's fixes. Archive: [h09b4b1-review3-probes.tar.gz](h09b4b1-review3-probes.tar.gz) (`review3/INDEX.txt`).

> Round 3 finds no new correctness or regression issues. Every round-2 finding is fixed or recorded, and all that remains is minor comment and readability cleanup.
>
> ### 1. Earlier probes re-run on snap5
> I ran all 100 round-1 and round-2 probes on snap5 and compared them with snap4:
> - **Now match Node:** a10 (R2-F3), d01 (R2-F4: rest elements now work and the misleading "method" message is gone), w11 (R2-F2).
> - **Changed but still different from Node:**
>   - g03 (R2-F1): the values are now right (`copy.k` is undefined for a prototype getter or method). The copy still lists k; that's recorded as C234, and the skipped test reproduces it.
>   - a03: only C228's callback and `for…of` errors are left.
> - **Everything else:** same result as on snap4. That covers the C231, C232, C233, C235 and C228 rows, and the lies c03 and c07, which now hit the intended checked-cast TypeError.
> - **Tests:** the new and changed tests (class_mapped_intersections, in_narrowing_writes_and_copies, narrowed_array_intersections, narrowed_optional_chains) fail on base and match Node on snap5. The two skipped tests (in_narrowing_spread_prototype_members, in_narrowing_union_member_types) still reproduce, and Node's output equals their `// expect:` lines.
>
> ### 2. Regression hunt on the round-2 changes
> **The hasOwn branch (spreads and rest elements):**
> - **h01:** object literals with an own field, an own getter (called once per read, the same count as Node), a frozen object, a class's own field, a class's prototype getter, an own undefined, and an object without k. Only Proto's key count differs (C234).
> - **h07:** rest elements whose source is a property read, an array element or a tuple element.
> - **h10:** class objects whose k is declared in a base class, is an optional field with no initializer, or is set in the constructor. The values match Node. Only the key order differs: the copy lists keys in the static type's order, which is pre-existing on base (control h12) and follows D-L10's fixed layouts, so it's not a finding.
> - **h02:** `{ ...this }` and a spread of a union, both after `in`. Both are PTS1001 on base and snap4 as well, so nothing changed.
>
> **The context-typed conversion of narrowed chains:**
> - **h05:** generic instances, overloads, `string | null | undefined`, an explicit `generic<number>`, an optional parameter, and a return of type `T | undefined`. All match Node.
> - **h09:** the chain as the right operand of `??` and `||`, and as a `?:` branch, in typed places that take undefined. Matches Node.
> - **h03:** a chain used as a default parameter's initializer is PTS1001 on base, snap4 and snap5 alike, so it's pre-existing.
>
> **Widening chains whose object is a dynamic value:**
> - **h06:** `?.` calls on `any`, `a.find(…)?.x`, `JSON.parse` values, and `a?.b?.c` through any. Matches Node.
> - **h08:** an unknown narrowed by a predicate, and elements of an `any[]`, whose chain results are a class, an object, a string, a number or a method call in typed places. These were PTS1001 on base and snap4 ("converting undefined to C") and now match Node.
> - **h04:** `const s: string = x?.s` for `x: any` holding undefined throws a TypeError, as it did on base (D-070's checked conversion), so it isn't a regression.
>
> ### 3. Code quality of the round-2 code (low, no behaviour change)
> - **Q1:** `missingOwnProperty`'s doc comment (`/tmp/h09b4b/s2/snap5/pts/lower/dynamic.go`) says "for a spread", but rest elements use it too. Its two lines run 104 and 101 characters, where nearly all doc comments in `pts/lower` wrap at about 95.
> - **Q2:** in `optionalChainFrom` (`chains.go`), `l.contextTakesUndefined(node)` is called twice, once in the outer condition and again in the new inner `if`. Hoisting it into a local would read better.
> - **Q3:** `restProperties` (`destructuring.go`) still uses the "rest properties holding the method `k`" message when the source isn't pure and k isn't a method. I couldn't build such a source, since narrowing never applies to call results, so this is cosmetic only.
>
> The renames (`takesAnyValue` to `takesUndefined`) and the comments for Q1–Q3 of round 2 now match what the code does.
>
> Probes, outputs, the runner and `INDEX.txt` (per-CLI status table and probe map) are in `/tmp/h09b4b/s2/review3-probes.tar.gz`. I built no binaries and removed the caches.
>
> VERDICT: ready

The parent applied round 3's Q1–Q3 (comment, a local for `contextTakesUndefined`, and a message that names a property the object's type lacks rather than a method) before the final snapshot.
