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
