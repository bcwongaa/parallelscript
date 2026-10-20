# H09b3a review reports (reviewer, claude-opus-5-5, xhigh)

Saved by the parent from the reviewer's final messages, unedited below each rule. The reviewer was read-only; each round reviewed a byte-checked snapshot worktree of the change.

## Round 1

Snapshot `/tmp/h09b3/snap1r` (base `4ae2915e9` plus the change). Probes: [h09b3a-review-probes.tar.gz](h09b3a-review-probes.tar.gz) (sha256 `5c493c8d…`; `INDEX.txt` inside).

---

# H09b3a review, round 1 (reviewer, claude-opus-5-5)

**Verdict: not ready to commit.** I found two strict regressions, both with the same cause and a small fix: a program that ran correctly on the base now crashes, and a receiver now runs twice. Two other issues need fixing too: a key whose value differs from its type now calls the wrong method silently, and seven of H09b2's tests have silently stopped testing what they were written for. Everything else I tried matches Node.

Every claim below comes from a real run unless I call it a suspicion. Probes, outputs and tools are in `/tmp/h09b3/review1/probes.tar.gz` (`INDEX.txt` inside), and `/tmp/h09b3/review1/DONE` is touched. Neither the repository nor the snapshot was modified.

**How I ran things.** `tools/tri.sh` runs each probe with base, change and Node. Both CLIs were debug builds, made with `go build -overlay`, that print every driver fallback as `PTS-FALLBACK from N` (0 = AllInstances, 1 = ValueInstances, 2 = DeclaredInstances), so a silent fallback shows up in the output.

## Findings

### 1. HIGH (regression): `o?.["m"]?.(x)` is now PTS1001, and the fallback drops every instance in the program
- **Repro:** `p/e11_fallback_loss.pts`. Other forms: `p/e08_optcall_after_elem.pts` (class), `p/e10_optcall_after_elem_iface.pts` (interface).
- **Base:** exit 0, prints `unrelated 6`.
- **Change:** AllInstances and ValueInstances fail with `PTS1001 converting Parent | undefined to Parent` (interface version: `calling a method of an object type on a undefined | {make…}`). The driver falls back to DeclaredInstances, so an unrelated, correct `m.make(5)` call loses its instance. Result: `Uncaught TypeError: Expected Box<number>, got Box<any>`, exit 1.
- **Node:** `unrelated 6`.
- **Cause:** `constantKeyCall` (`pts/lower/element_calls.go`) also routes calls that have their own `?.(`. The chain has already evaluated the callee `maybe?.["make"]` as the chain object (`l.chainObjects[callee]`). `genericMethodCall` / `interfaceMethodCall` then lower the receiver node `maybe` again, still typed nullable.
- The dotted `maybe?.make?.(4)` is PTS1001 on the base too (`p/e09`), but the base compiled the element form. The 496-case matrix has no `?.(` on an element access, so it missed this.

### 2. HIGH (regression): `o["m"]?.(x)` and `o[K]?.(x)` run the receiver twice
- **Repro:** `p/e31_optcall_id.pts` uses `id<T>(x: T): T`, a generic method whose result holds no class instance:
  - base: `literal key 5 1`, `named key 6 1`
  - change: `… 5 2`, `… 6 2`
  - Node: `… 1`, `… 1`
- Same for results that are boxes (`p/e25s/e25_class.pts`, `e25_interface.pts`, `p/e29_optcall_named.pts`: change 2, Node 1).
- **Cause:** the same as finding 1. The dotted `o.m?.(x)` already runs the receiver twice on the base, generic or not (finding 8). Routing the element form to the dotted path imports that bug.
- **Suggested fix for 1 and 2:** in `constantKeyCall`, return `(nil, false)` when the call is itself `?.()` (`ast.IsOptionalChainRoot(call.AsNode())`, or `l.chainObjects[call.Expression]` is set). Add `o?.["m"]?.(x)`, `o["m"]?.(x)` and `recv()["m"]?.(x)` (counting receiver calls) to a test or to the matrix.

### 3. MEDIUM: a key whose value isn't the name its type says now calls the wrong method, silently
- **Repro:** `p/f1_lying_key.pts`, `p/f1b_lying_key_id.pts`, `p/e02_as_key.pts`.
- **The cases:** `t[s as "id"](2)` with `s = "other"`; `const k: "id" = anyValue`; a parameter `key: "id"` passed an `any`.
- **Base:** throws TypeError in every case.
- **Change:** calls `make`/`id` silently: `4`, `5`, `1 2 3`, and no `other called`.
- **Node:** calls `other` (`other called`), or throws `TypeError` where no such method exists (`byParameter(m, missing)`).
- **Cause:** two things in `element_calls.go`:
  - `constantKey` trusts the key's *type*.
  - `isNamedKey` accepts `as` and `<T>` assertions around a name, and PTS doesn't check string-literal types when converting from `any`.
- `readsSafely` skips the key read entirely for local and parameter keys, so nothing at run time compares the value.
- **Suggested fix:** pick one of these:
  - accept only names that resolve to a `const` initialized with a string literal and no type annotation, and drop the assertion cases from `isNamedKey`;
  - or compare the key with the name at run time (`withKeyRead` already reads it) and otherwise fall back to the old element-access path, or throw.

### 4. MEDIUM (tests): seven H09b2 regression tests no longer reach the erased copy they were written to test
- These tests get their "erased copy's object" from `Ops["wrap"](7)` (C185), and some also from `?.`. With the change those calls find instances, so the tests still pass but no longer cover D-192's checked copies, verified reads and member choice.
- **Evidence:** gdb counts of how often each erased copy runs (`out/erased_hits.txt`, `tools/gdb_count.sh`), base → change:

  | Test | Erased-copy calls |
  |---|---|
  | `maps_sets_convert_by_copy` | index 1→0, unique 1→0 |
  | `erased_objects_integer_reads` | wrap 2→0 |
  | `erased_objects_par` | mk 1→0 |
  | `erased_objects_typed_writes` | wrap and cell 1→0 |
  | `union_conversion_member_choice` | absent and frozen 1→0 |
  | `erased_objects_typed_reads` | computed, keyed, lazy, maybe, nested and pair 1→0; wrap 8→3 |
  | `erased_objects_union_routes` | five of six → 0 |

- `erased_objects_closure_calls.pts:6` still says "the erased copy comes from an element access (C185)".
- `generic_method_erased_objects.pts`: the header was updated, but line 87 and its expect line still say "an array an erased copy made, through a union", and that array is now made by the instance (wrap 6→3).
- **Suggested fix:** move these tests to a route that keeps the erased copy by design, such as an `any` argument with explicit type arguments (D-191), and fix their headers and labels.

### 5. LOW: static methods by key skip the class's TDZ, and the error names the wrong binding
- **Repro:** `p/e05_static_tdz.pts`. `Later["make"](5)` before `class Later` has run:
  - change: prints `5`
  - Node: `ReferenceError … 'Later'`
  - base: PTS1001
- `Later[KEY](5)` reports `KEY` instead of `Later`.
- The dotted `Later.make(5)` prints `5` on the base too (`p/e06`), so the missing check itself predates this slice.
- The `withKeyRead` comment ("a class (static) runs nothing, so the key is read first") is inaccurate: reading a class binding can throw.

### 6. LOW (docs): `docs/FORK.md` doesn't list the two new checker exports
`PtsWithoutCallChain` and `PtsIsOuterCallChain` are missing; every other export in `internal/checker/pts.go` has a row. The new doc comment cites D-193, which isn't written yet.

### 7. LOW: `super[K](await x)` and `Class[K](await x)` are still PTS1001, though the forms without `await` now compile
- **Repro:** `p/e26_super_static_await.pts`. The base fails both forms, so this is not a regression.
- **Cause:** `isSettledCallee` only resolves literal keys. `hoistYields` therefore keeps the callee as a value read, which `constantKeyCall` then ignores. For interface receivers, that kept read is a wasted bound-wrapper allocation.

### 8. INFO (pre-existing, outside this slice): the dotted `o.m?.(x)` runs `o` twice
Base and change both print 2 where Node prints 1, for generic and non-generic methods alike (`p/e25s/e25_classdot.pts`, `e25_nongeneric.pts`). This deserves a ledger entry; it is also the root of finding 2.

### 9. INFO (pre-existing, outside this slice): a by-name call with `yield` in its arguments, inside a generator, keeps the erased copy
`im.make(yield 2)` throws `Expected Box<number>, got Box<any>` on base and change (`p/e13_yield_iface.pts`). The `await` version works.

### 10. INFO: `?.` now does better than the dotted form in D-095 contexts
`p/a04`: `const r: Res<number> | undefined = mm?.ok(5)` now prints 6, while `m.ok(5)` and `m["ok"](5)` still throw. That's because chained demands skip the `erasedTarget` comparison. The result is correct, but the forms are inconsistent and the difference should be documented. `p/a05` is PTS1001 on both builds.

### 11. INFO (gap): object literals as rest elements still keep the erased copy
`vi.opts<number>({tag:"some",value:1}, …)` with `...xs: Opt<T>[]` throws on base and change (`p/r01_rest_elements.pts`). C188's fix doesn't cover rest elements.

## Checked and fine
- **The new tests:** all 10 new or unskipped tests fail on the base, pass on the change and match Node, except the no-diff guard, as designed (`out/newtests.txt`).
- **No collateral change** (emit-c of the whole tree on both builds):
  - `run/`: 565 of 580 programs produce identical C. The other 15 are this slice's tests and the H09b2 tests in finding 4.
  - corpus, stress, errors, http and examples: only `corpus/functional/transducers.pts` changed (one spread key-order variant dropped), and its output is identical on base, change and Node.
  - No exit code got worse anywhere.
- **`?.` calls (C179):** result types (function, tuple, Map, Set, Record, nested arrays, `| null`, object types), syntactic positions (statement, argument, `any`/`unknown`, template, `??`, ternary, `!`, typeof, spread, for-of, `await`), and inner links on generic classes (C195). Arguments are skipped when the receiver is nullish. All match Node (`p/a01`–`a03`).
- **Element-access calls (C185):** super, this, arrow this, statics, overloads, explicit type arguments, `?.[]`, template keys, quoted and non-identifier names, unions (class | class, class | object type), narrowed receivers, type-parameter receivers, inherited generic-class methods, chains like `o?.inner[K](x)`, TDZ of global and captured keys, and `await`/`yield` in arguments and receivers. All match Node (`p/e07`, `p/e12`, `p/e14`–`p/e22`, `p/e24`, `p/e27`, `p/e28`, `s01`). Library generic methods and array unions behave as before.
- **Object literal arguments (C188):** optional members, same-field members, methods, interface values, extra properties, frozen objects, identity, getters running once, D-192's `Box<any>` inside a literal still throwing, and `any`-returning getters (typed reads verify them). Nothing unchecked reaches an instance (`p/c01`–`c03`).
- **Rest parameters (C170):** unions, `int`/`u8`, subclasses, `null`, `any` (stays on the erased copy), order, mutation and spreads. The rest array never aliases the caller's array, and the erased branch's copy works. D-095 with rest signatures only improves things (`p/r01`–`r06`).
- **Checker exports:** `PtsWithoutCallChain` keeps the declaration, parameters, `minArgumentCount` and the type-argument mapping. No caching or identity problem found.

## Round 2

Snapshot `/tmp/h09b3/snap2r`. Probes: [h09b3a-review2-probes.tar.gz](h09b3a-review2-probes.tar.gz) (sha256 `0d7bc09f…`; `INDEX.txt` inside).

---

# H09b3a review, round 2 (reviewer, claude-opus-5-5)

**Verdict: not ready to commit.** The round-1 fixes all hold, but the new receiver keeping in `optionalChain` breaks programs that compiled on the base. Optional calls of static methods, of `Math` methods, of a namespace import's functions, and `this.m?.()` inside a static method are now PTS1001 (finding 1). It is a hard compile error in every specialization, so the driver can't fall back. Everything else I found is pre-existing or low.

Everything below comes from real runs: base CLI against the round-2 debug build of `/tmp/h09b3/snap2r` (it prints every driver fallback) against Node. Probes and outputs are in `/tmp/h09b3/review2/probes.tar.gz` (`INDEX.txt` inside), and `/tmp/h09b3/review2/DONE` is touched. Neither tree was modified.

## Round-1 findings, re-checked
I re-ran all 161 round-1 probes with round 2 and Node (`out/r1rerun.txt`, `out/r1rerun_summary.txt`). Every remaining difference from Node is by design or pre-existing.

- **F1/F2 (`?.(` after an element access): fixed.**
  - `p/e11_fallback_loss.pts` exits 0 again with `unrelated 6`. Only the non-null-assertion message differs from Node, as on the base.
  - `e08` and `e10` are back to the base's TypeError, with no fallback.
  - `e07`'s `?.[]?.()` line no longer makes the program fall back.
- **F3 (lying keys): fixed.**
  - `f1`, `f1b`, `e02`, `e03` and `e04` give the base's TypeErrors again.
  - The new `keyValue` held up in every form I tried (`p/k01/`, `p/k02_key_forms.pts`). Accepted, and calling the method the value names: imports, aliased exports, re-exports, a const of a const across modules, template-literal consts, parenthesized keys, an inner `const` shadowing an outer one. Refused, so they keep the base's path: `S as "make"` with `S = "other" as string`, `const A: "make" = anyValue`, destructured consts, `export default` consts, `keys.K`.
  - One case of a correct program is lost by design: `let local: "make" = "make"` (round-1 `e12`, "await local key") now throws, as on the base.
- **Receiver evaluated once in `o["m"]?.(x)`, `o.m?.(x)` and `o[K]?.(x)` (C203): fixed.** `p/o03_counts.pts` covers class, interface and `any` receivers, parenthesized receivers, generic and non-generic methods, and literal and const keys. `e31`, `e25s` and `e29` match Node. Not-there methods skip their arguments, getters run once, and hoisted receivers before an `await` are evaluated once (`p/o05`).
- **F4 (lost erased-copy coverage): fixed.** gdb counts (`out/erased_hits.txt`) show every erased copy runs again in all nine affected tests, as many times as on the base. The exceptions are deliberate: `typed_reads`' `wrap` (8→6) and `generic_method_erased_objects`' `wrap` (6→4), whose element-access and `?.` lines now test instances. All reworked tests pass and match their expect blocks (`out/tests.txt`).
- **F5/F6:** the comment is corrected. The FORK.md row is present, but its commit column reads "this slice's fix commit"; it needs the hash at commit time.
- **F11 (object literals as rest elements): fixed.** `r01`'s "union literal elements" prints `1,3`, as Node does. `p/r03_rest_of_boxes.pts`: an instance is found, and when none is listed, the erased branch converts the rest array correctly (`opts, no instance 101,103`).
- **The new receiver-once test** fails on the base (`receivers 2`) and passes on round 2.

## New findings

### 1. HIGH (regression): `?.()` on a static, `Math`, namespace-import or static-`this` receiver no longer compiles

| Program | Base | Round 2 |
|---|---|---|
| `Util.twice?.(3)` (static method) | `static 6` | `PTS1001 the class 'Util' as a value is not supported yet` |
| `Util.box?.(4)` (generic static) | `4` | same PTS1001 |
| `Math.max?.(1, 5)` | `5` | `PTS1001 the global 'Math' is not supported yet` |
| `lib.twice?.(3)`, with `import * as lib` | `6` | `PTS1001 using the module namespace 'lib' as a value` |
| `this.helper?.(4)` inside a static method | `8` | `PTS1001 'this' as a value in a static member` |

- **Repros:** `p/o01s/o01_0.pts`–`o01_2.pts`, `p/o02s/3/main.pts`, `p/o08_static_this.pts`.
- Node prints the base's values in every case. These are hard compile errors in all three specializations, so the program doesn't build at all.
- **Cause:** `calledMethodReceiver` (`pts/lower/chains.go`) keeps every receiver except `super` and optional chains, and `optionalChain` lowers it with `l.expression(receiverNode)` up front. The method-call paths for statics, namespaces and library globals never evaluate their receiver, and some of these receivers can't be lowered as values at all.
- The existing tests and corpus don't cover this. Every emit-c exit code is unchanged, and the only C changes outside this slice's tests are a benign extra `receiver` local, for example in `abort.pts`'s `this.onabort?.(event)` and in corpus programs.
- **Suggested fix:** pick one of these:
  - leave out receivers that are names or `this` (reading them twice can't be observed);
  - or leave out receivers whose symbol is a class, an alias or namespace, or a global object (`isNamespace`, class symbols, `isGlobalMember`/`mathMember`, `this` in a static member).
- **Add test lines for:** `C.m?.(x)`, `C.g?.(x)` (generic static), `Math.max?.(…)`, `ns.f?.(x)`, and `this.m?.()` in a static method.

### 2. INFO (pre-existing): a computed key on an `any` receiver is still evaluated twice in `o[k()]?.(x)`
`p/o03s/o03_counts_10`/`_11`: `anyRecv()[key()]?.(8)` gives `k2` on base and round 2; Node gives `k1`. Round 2 fixes the receiver count (now `r1`) but not the key.

### 3. INFO (pre-existing): `o?.m?.(x)` on a non-nullable `o` evaluates `o` twice
`p/o10s/o10_more_chain_forms_01`: base and round 2 print `r2`, Node `r1`. `calledMethodReceiver` excludes callees that are optional chains, so this form keeps the old double evaluation.

### 4. INFO (pre-existing): `(o.m)?.(x)` is PTS1001 on both builds
`p/o10s/o10_more_chain_forms_00` fails with `converting (number) => number to C is not supported yet`.

### 5. INFO (pre-existing): an optional argument that may be undefined is evaluated before the receiver
`p/r02s/*_00` and `_03`: `recv().g(maybe())` runs `maybe,recv` on base and round 2; Node runs `recv,maybe`. The cause is `inTemporaries` plus `withArgumentTemps`. It happens for non-generic methods too, and such calls also stay on the erased copy.

### 6. LOW (defensive): `restAsIs` doesn't explicitly reject a `*spreadValue`
If `passesAsIs(spread array type, element type)` were ever true, the spread array would become a single element of the rest array. I couldn't reach this: `takesUnverifiedObjects` and `restArray`'s spread rule block every case I tried (`p/r01_spread_any_rest.pts`, `p/r04_spread_opts.pts`). Returning nil for `*spreadValue` would make it safe by construction.

## Checked and fine
- **Receiver keeping:**
  - getters returning functions run once;
  - optional function properties work, including the not-there case;
  - array elements (`a[0]?.(x)`) and record elements work, including a missing key;
  - `l.kept` composes with suspension: an `await` after the chain, and arrays holding the chain and an `await`;
  - inside a generic function's instance the receiver now runs once (base ran it twice);
  - `super` (excluded) and library-method receivers behave as on the base, and are unsupported on both where they were before (`p/o09`, `p/o11`, `p/o12`).
- **`argumentList` / `restAsIs`:**
  - order is receiver, optional parameter, then rest elements (`recv,a,x1`);
  - a left-out optional before the rest gives an empty rest array;
  - temporaries never cover rest values;
  - tuple spreads expand into the rest correctly;
  - `any` rest elements stay on the erased copy;
  - typed array spreads keep the base's d06 PTS1001.
- **Generated C:** 581 run tests and 419 other programs keep their emit-c exit codes. Outside this slice's tests and the reworked ones, the only C changes are the extra `receiver` local.

## Round 3

Snapshot `/tmp/h09b3/snap3r` (the final code). Probes: [h09b3a-review3-probes.tar.gz](h09b3a-review3-probes.tar.gz) (sha256 `8ac3d81a…`; `INDEX.txt` inside).

---

# H09b3a review, round 3 (reviewer, claude-opus-5-5)

**Verdict: the slice is ready.** R2-1 and R2-6 are fixed, `mayHaveEffect` held up against every receiver form I threw at it, and nothing outside this slice's own tests changed. The only thing left before committing: `docs/FORK.md`'s new row still says "this slice's fix commit" where the commit hash goes.

Everything below comes from runs of base vs round 3 (`/tmp/h09b3/snap3r`, as a debug build that prints any fallback) vs Node. Probes and outputs are in `/tmp/h09b3/review3/probes.tar.gz` (`INDEX.txt` inside), and `/tmp/h09b3/review3/DONE` is touched. Nothing was edited in the repo or the snapshots.

## R2-1 (HIGH): fixed
- Every program from round 2's finding 1 compiles and runs again with the base's output, and Node's:

  | Program | Output |
  |---|---|
  | `Util.twice?.(3)` | `6` |
  | `Util.box?.(4)` | `4` |
  | `Math.max?.(1, 5)` | `5` |
  | `lib.twice?.(3)` (namespace import) | `6` |
  | `this.helper?.(4)` in a static method | `8` |

  Evidence: `out/r2_1_verify.txt` and `out/o02_verify.txt`.
- `JSON.stringify?.()`, `console.log?.()`, `Object.keys?.()` and `Array.isArray?.()` are back to the base's own PTS1001 messages.
- The updated `optional_method_calls_evaluate_receiver_once.pts` matches its expect block, and so does Node. On the base, its first five lines print `receivers 2`; the new guard lines pass there too.

## R2-6 (LOW): fixed
`restAsIs` now returns nil for a `*spreadValue`. Round 2's `r01_spread_any_rest.pts` gives the same results as before.

## `mayHaveEffect`, probed adversarially
All results are in `out/m01.txt`, `out/m02.txt` and `out/g01.txt`.

**Not kept, so the call takes the base's path, with the same output as base and Node:**
- a namespace import's class static `ns.Cls.sm?.(1)`;
- a namespace import's function `ns.f?.(1)`;
- a re-exported namespace's function and class static (`re.inner.f?.(1)`, `re.inner.Cls.sm?.(1)`);
- a renamed re-exported class (`re.Renamed.sm?.(1)`);
- `this` inside arrow functions of static and instance members.

**Kept, so the receiver now runs once (the base ran it twice):**

| Receiver | Base | Round 3 | Node |
|---|---|---|---|
| static getter `Util.counted.m?.(1)` | `g2` | `g1` | `g1` |
| instance getter `this.viaGetter.m?.(1)`, in a method or an arrow | `g2` | `g1` | `g1` |
| generic class instance getter `new Holder(c).counted.m?.(2)` | `r2` | `r1` | `r1` |
| inside a generic function's instance | `r2` | `r1` | `r1` |
| an `any` member receiver | `r2` | `r1` | `r1` |
| a union member receiver | `r2` | `r1` | `r1` |

**Kept with no behaviour change:** the static field object `Util.obj.m?.(1)` and `ns.obj.m?.(1)` print the right values.

**PTS1001 on both builds, only the message changes:** `(Util as typeof Util).twice?.(3)`, `Util!.twice?.(3)`, `globalThis.Math.max?.(1, 2)`, `ns["Cls"].sm?.(1)`, `Util["obj"].m?.(1)`, `ns["f"]?.(1)`, and an enum member's `toFixed?.(1)`. A static field holding a class is unsupported as a declaration in the first place.

## Re-checks
- **Round-2 probes:** I re-ran all 80 against round 3 and Node (`out/r2rerun.txt`). Every remaining difference from Node is one round 2 already classed as pre-existing or by design: the keys that may lie keep the base's TypeError; the gaps you recorded as C202, C206, C207 and C197; library methods as values; the "using a `Box` as a `Box`" error for rests of class instances; and d06 spreads.
- **Generated C:** I emitted C for all 581 run tests and 419 programs in corpus, stress, errors, http and examples. No exit code got worse. The only programs whose C differs from the base are this slice's own tests, the reworked tests, and `corpus/functional/transducers` (the benign key-order change from round 1, with output identical to base and Node). Round 2's extra `receiver` local in unrelated programs (`abort.pts`'s `this.onabort?.(event)`, fetch, http, other corpus programs) is gone, because those receivers are `this` or plain names.

## Still open, not blocking
- `docs/FORK.md`: fill in the commit hash at commit time.
- The pre-existing gaps you have recorded (C197, C201, C202, C206, C207, and F9's generator calls by name with `yield` in their arguments) are unchanged, as expected.
