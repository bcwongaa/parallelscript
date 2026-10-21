# H09b5a review reports

The `reviewer` subagent's final messages (claude-opus-5-5, effort xhigh), saved verbatim by the parent, one section per round. Read-only; each round reviewed a byte-checked snapshot of the slice. The parent's dispositions are in [h09b5a.md](h09b5a.md).

## Round 1 (snapshot `/tmp/h09b5/snap-a`: `7428a7b0f` plus the fix commit `3b2dbf4ed`'s code)

Probes, tools and overlays: [h09b5a-review1-probes.tar.gz](h09b5a-review1-probes.tar.gz) (sha256 `4134fbc9f365c2c8…`, 145 entries; `INDEX.txt`).

---

**Verdict: not ready.** The slice meets the letter of "no internal error after the limit": I found no PTS9001 on the snapshot in 39 probes, and the base gave 6. But the C184 acceptance ("compiles with the erased copy past the limit, or an honest diagnostic") fails for common recursion shapes, because of the rule "a limit below the trial's root is foreign". There is also a gap in the depth chain and a cost problem with the 1024 cap. I found no case where the snapshot is semantically worse than the base: every program that failed or misbehaved on the snapshot also did on the base. The parent's full suite was green (2011 pass, 0 fail).

Everything marked verified I ran myself. Probe paths are relative to `/tmp/h09b5/review-a/p/`.

### Findings, by severity

#### 1. HIGH (verified): the "honest PTS1001" below a trial's root disappears in the whole-program fallback

- **Where:** `pts/lower/generics.go:1054-1056`, `belowInstanceLimits` (`!root` sets `t.foreign`).
- **What happens:** The PTS1001 survives only if every fallback level makes the same trial again. That is true for dispatched demands (override_3). It is not true for:
  - interface and literal by-name demands, which only AllInstances makes;
  - local-arrow and value demands, which DeclaredInstances doesn't make.

  In those cases the build silently succeeds at a lower level with erased copies from depth 0. Then n=3 throws, and unrelated interface calls lose their instances (C176).
- **Probes** (base, snapshot and Node outputs are in `bin/` and the emit-with results):

| Probe | Base | Snapshot | allquiet overlay | Node |
|---|---|---|---|---|
| `interface_this_rec.pts` (the slice's own `generic_polymorphic_recursion_interface` test with natural `this.rec` instead of `const self: Recursive = this; self.rec`) | TypeError | AllInstances PTS1001, ValueInstances OK, then `TypeError Expected Box<number>, got Box<any>` | TypeError | 6 |
| `iface_then_direct.pts` | both lines TypeError | both lines TypeError | unrelated `5`, recursion TypeError | 5 / 6 |
| `mutual_mixed.pts` (f direct, then g through a value, then f) | TypeError | AllInstances and ValueInstances PTS1001, DeclaredInstances OK, then `TypeError Expected Box<number[]>` | 6 | 6 |
| `localarrow_then_direct.pts` | TypeError | TypeError | TypeError | 6 |

- **Recommendation:** adopt the reproducer's option B. Past the depth limit, any call takes the erased copy with a checked result; that is the only rule that prints 6 for every form here. At minimum use P2 (every limit inside a trial belongs to the trial). Among the m8 cases, P2 changes only override_3 and override_70, from PTS1001 to compiling (`m8_levels.txt`).

#### 2. MEDIUM (verified): speculation's own recursion is treated as "the program's own"

- **Where:** the comment at `generics.go:1041-1045` says "…the program's own recursion, which stands, as it would without the trial". That is false: without the trial, these instances would never be made.
- **`spec_fn_recursion.pts`:** base and snapshot both give PTS1001 at every level; Node prints 3. The cause is only the pairing of v2 with g's `number[]` demand: the control `spec_fn_recursion_control.pts` compiles and prints 3, and allquiet prints 3. This contradicts the acceptance "only instances calls may use … count".
- **`class_first_cycle2.pts`:** the same value-demand cycle as `generic_polymorphic_recursion_value`, but entered through the class first. The base gives PTS9001, the snapshot PTS1001 at every level, and allquiet compiles and prints 6 (Node 6). Whether the program compiles depends on which declaration is entered first.

#### 3. MEDIUM (verified): erased copies break the depth chain

- **Where:** `erasedInstance` (`generics.go:826`) builds its `substitution` without `made`. Recursion through the erased copies of a generic class instance's generic methods (method tables via `any`, interfaces or dispatch) is then stopped only by `maxInstances`.
- **`erased_chain.pts`:** base 0.25 s, snapshot 10.3 s.
- **`erased_chain_heavy.pts`:** base 0.8 s, snapshot 40.5 s.
- Both fail, but the snapshot reports the count message instead of the depth message.
- **Candidate fix, verified:** `made: generic.outer` (`overlay/generics_erasedmade.go`) gives 0.28 s and the depth message.

#### 4. MEDIUM-LOW (verified): the 1024 cap counts per genericFunc, not per declaration

- A generic method has one genericFunc per class instance, so the worst case is 1024 × 1024 instances. Each of the three fallback levels redoes the work.
- **`product_blowup.pts`:** base 8.3 s / 98 MB, snapshot 86 s / 472 MB, the same PTS1001 on both.
- `branching_direct.pts`: 0.37 s → 2.2 s. `branching_dispatched.pts`: base PTS9001, snapshot honest PTS1001 after 4.9 s.
- **Suggestion:** count per declaration node, or use a global budget.

#### 5. LOW (verified): no test pins the non-root-foreign rule

- With the allquiet mutation, every slice test passes (`mut_allquiet.txt`), and override_3's PTS1001 has no test.
- The other pins hold:
  - the irType fix: reverting it fails the lower test and `errors/generic_class_instance_failure_value_calls`;
  - the depth limit: removing it fails 8 tests;
  - root-quiet: making every limit foreign fails 6 run tests and the driver test.
- The new run tests' expected output matches Node (the suite's `TestRunMatchesNode` passed).

#### 6. LOW (verified): cascade diagnostics remain after the count cap or a failed class

Examples: `a.value` (`branching_direct`), `m.size` (`generic_class_instance_failure_value_calls`), `this.x`/`this.y` (`class_member_branching`), `new Box({ k1023: 1 }).value` (`cap_uses/`). The depth-limit cases are clean.

#### 7. LOW: documentation

- **`genericInstanceFor` comment (around `generics.go:984`):** it dropped "or the instance limit", but `generics.go:1016` still returns nil, unreported, at `variants > 0 && len >= maxInstances`.
- **`run/generic_speculative_class_instances.pts` header** says "Only instances calls may use are made and count". In fact speculative instances are still made (leak_25: 5,538 lines vs 4,114 for the control) and count toward the cap; the test passes because the cap is now 1024.
- **`generic_fallback_spares_unrelated_interface_call`:** the name promises a property this slice doesn't provide (see finding 1).
- **`lower_test.go:1693`:** "1,025 … make as many instances", but 1,024 are made.
- **`docs/DECISIONS.md`:** the "64 instances" text in D-049 and D-050, and "never past the instance limit" in D-190, still need superseding text.

### Verified OK

- **Termination:** the `made` chain only points to older substitutions, so it can't cycle. Depth is bounded per declaration and the count per genericFunc, so every path is finite. Tested: mutual, mixed, member-branching, async, generator, bind and class-cycle forms.
- **No PTS9001 past the cap** in 18 syntactic positions (`cap_uses.txt`).
- **Recursion through values now prints Node's 6** where the base threw a TypeError: async (`async_value_rec`), generator (`generator_value_rec`) and bind (`bind_rec`).
- **`mini_query_engine`** loses only duplicate errors (100:11 and 100:34) that the base's typed nil caused.
- **The base got no worse anywhere**, apart from the compile-time cases in findings 3 and 4.

### Files

- **Probes, tools, overlays and results:** `/tmp/h09b5/review-a/` (start with `INDEX.txt`).
- **Archive:** `/tmp/h09b5/h09b5a-review1-probes.tar.gz` (created last). The tool binaries are deleted; `INDEX.txt` has the commands to rebuild them.
- **Changed code reviewed:**
  - `/tmp/h09b5/snap-a/pts/lower/generics.go`
  - `/tmp/h09b5/snap-a/pts/lower/generic_classes.go`
  - `/tmp/h09b5/snap-a/pts/lower/instances.go`
  - `/tmp/h09b5/snap-a/pts/lower/lower.go`
  - `/tmp/h09b5/snap-a/pts/lower/lower_test.go`
  - `/tmp/h09b5/snap-a/pts/driver/driver_test.go`

## Round 2 (snapshot `/tmp/h09b5/snap-b`: `3b2dbf4ed` plus the round-1 changes, committed as `909af48b7`)

Probes, tools and overlays: [h09b5a-review2-probes.tar.gz](h09b5a-review2-probes.tar.gz) (sha256 `0c34fd05663a5b62…`, 188 entries; `INDEX.txt`).

---

**Verdict: not ready.** The round-1 fixes work: every F1, F2 and F3 probe from round 1 now matches Node, F4's tree stops in 1.75 s, and there is no PTS9001 anywhere on snap-b. But option B can now change results silently past the depth limit, which the base refused to compile. And three programs that compiled and ran correctly on the base now fail.

Each fix is small. Everything below I ran myself, unless it says it comes from reading the code. Probe paths are relative to `/tmp/h09b5/review-a/r2/`.

### Findings, by severity

#### 1. MEDIUM-HIGH (verified): option B silently changes results past the depth limit

- **What happens:** Past depth 32, the erased copy builds new arrays and objects as `any[]` / `{…: any}`. `fromErasedCall`, then `fromErased`, converts them for the typed caller by a checked *copy*. The caller and whatever the callee kept then hold different objects. No TypeError is raised.
- **`p/b_identity_array.pts`:** snap-b prints `not the same array at 9`, then `true 1 41`. Node prints only `true 1 41`. The base gave PTS1001.
- **`p/b_identity_object.pts`:** the caller's `inner.hits++` updates a copy. Snap-b prints `11111111011…10`, Node prints `11111111111…10`.
- **Control `p/b_identity_passthrough.pts`:** returning the array the caller passed in keeps identity, so only objects the erased copy *makes* are affected.
- **Doc consequence:** the header of `run/generic_polymorphic_recursion_direct.pts` ("runs on through it where its results hold no generic class instance") isn't accurate.
- **Options:**
  - restrict option B to calls whose instantiated result holds no object that the erased copy would make (primitives, void, dynamic results), and report PTS1001 otherwise; or
  - keep it, and write it down in D-198, SPEC §3.7 and the GUIDE as D-078's known boundary copy.

#### 2. MEDIUM (verified regression vs base): depth 32 breaks finite chains 33 to 64 deep

| Probe | Base | snap-b | Depth 64 (overlay) | Node |
|---|---|---|---|---|
| `p/b_finite_chain_40.pts` (peels `number[]`×40 down to `number`) | `40`, `1` | `40`, then `TypeError Expected Box<number[][][][][][][][]>, got Box<any>` | `40`, `1` | `40`, `1` |
| `p/b_finite_class_chain_40.pts` | `true true` | PTS1001 "polymorphic recursion more than 32 instances deep (`Chain`)" | `true true` | `true true` |

Cost of 64 instead of 32 (`depth_cost.txt`):

| Program | Depth 32 | Depth 64 |
|---|---|---|
| `generic_polymorphic_recursion_in_trials` | 33,274 lines, 9.3 s | 65,914 lines, 21.1 s |
| `generic_polymorphic_recursion_direct` | 10,353 lines, 2.2 s | 19,921 lines, 2.9 s |
| `branching_dispatched` | 17.6 s | 17.6 s (bound by the count, not the depth) |

**Suggestion:** use 64 for direct calls and classes, where each level costs one instance and the base allowed it. A lower depth, if wanted, only for trials, whose cross-pairing cost is C172's growth (H09b5c).

#### 3. MEDIUM-LOW (verified regression vs base): one 1024 budget per method declaration

- **What happens:** all instances of a generic class now share one count for each generic method. The base allowed 64 per class instance.
- **`p/c_method_instances_per_declaration.pts`:** 33 `Holder` instances × 32 `pick` types = 1,056 instances, with no recursion. The base compiles it and prints `33264`, matching Node. Snap-b reports PTS1001 32 times: "more than 1024 instances (`Holder.pick`)".
- **Suggestion:** count toward `maxInstances` only the instances made by recursion (instanceDepth ≥ 1 for their own declaration). That keeps F4's bound, since product_blowup's tree is all made by recursion, and keeps C172's "side-by-side instances don't count". The lower cap test would then need changing.

#### 4. MEDIUM-LOW (verified): branching recursion through demands is slow to compile

`p1/branching_dispatched.pts` recurses 3 levels at run time and prints 6. It now compiles to 95,349 lines in 17.6 s using 418 MB (44.7 s while the suite was running). The base failed with PTS9001 in 0.38 s; round 0 gave PTS1001 in 4.9 s. A smaller budget for instances made by recursion (128 to 256) would cut this proportionally.

#### 5. MEDIUM, test quality (verified by mutation)

| Mutation | Result |
|---|---|
| `nooptionb`: direct calls report PTS1001 past the depth | fails `_direct` and `_in_trials`. Option B is pinned. |
| `noconvert`: `fromErasedCall` returns the call unconverted | every slice test passes. Yet `p/d_direct_use.pts` (`first([x], n - 1)[0][0]`) then gives PTS9001 "interface conversion: ir.Type is *ir.Primitive, not *ir.Array". Snap-b matches Node there. The tests only use past-depth results through typed declarations; add a direct use. |
| `nodecrement`: no count decrement on trial rollback | every test passes. |
| `pergenericfunc`: count per genericFunc again | every test passes, just slower. The "half a second" in `generic_instance_cap_methods` isn't pinned. |
| `depth64` | only the erased-copies error test's message fails. |

#### 6. LOW (verified): diagnostics where option B can't apply

- **`p/d_erased_body_fails.pts`:** the base said "a generic function with more than 64 instances (`rec`)". Snap-b says "`new Holder<T>(x).size` is not supported yet", because the erased copy's body fails, and it no longer mentions the recursion.
- **`p/d_value_context.pts`:** stays PTS1001 at the depth, by design, while the same recursion through a direct call compiles.

#### 7. LOW, docs (from reading)

- **D-198** (`docs/DECISIONS.md:1888-1900` in snap-b) still describes round 0: `trial.root`, depth 64, and override_3's PTS1001. It also lists option B under "Alternatives rejected", though it is now the adopted design.
- **`docs/SPEC.md:238`** still says "a compile error after 64".

#### 8. F6 cascades

I agree with deferring them to H22; I found no cheap, safe fix by reading.

### Verified OK

- **Round-1 probes:** all 24, rerun on snap-b (`round1_probes_on_snapb.txt`). These now match Node: interface_this_rec, iface_then_direct, mutual_mixed, localarrow_then_direct, class_first_cycle2 and spec_fn_recursion, plus the async, generator and bind forms. erased_chain runs in 0.23 s with the depth message.
- **Option B paths that are correct:**
  - rest, optional and default parameters;
  - overloads;
  - static methods and module-scope consts called by name;
  - nested generic functions;
  - generic class instance methods with `this`;
  - `super` calls;
  - callbacks whose parameters mention T;
  - numbers, strings, booleans and 2^53;
  - async functions returning arrays, and generators.
- **Generic class results past the depth:** a checked TypeError, as designed (`b_box_past_depth`: n=31 prints 6, 32 and above throw; `b_async_box` rejects).
- **Count consistency (from reading):** counts only go up in `genericInstanceFor` and down for `t.instances` when a trial is taken back, which is the only removal path. The forwarding takeBack removes no instances, and the map is per lowerer, so per fallback level. I found no way for them to drift.
- **Still open, out of scope:** `spec_class_recursion` and `spec_class_recursion2` are still PTS1001. A class depth met during member declaration in a speculative trial counts as foreign, because trials don't take back class instances (H09b5b).

Probes, overlays, results and `INDEX.txt` are in `/tmp/h09b5/review-a/r2/`, archived last as `/tmp/h09b5/h09b5a-review2-probes.tar.gz`. The CLIs and binaries I built are deleted; `INDEX.txt` has the rebuild command.

## Round 3 (snapshot `/tmp/h09b5/snap-c`: `909af48b7` plus the round-2 changes, committed as `aa42491a5`)

Probes, tools and overlays: [h09b5a-review3-probes.tar.gz](h09b5a-review3-probes.tar.gz) (sha256 `3adf66083c814fc7…`, 140 entries; `INDEX.txt`).

---

**Verdict: ready.** I found no correctness defect, no silent wrong value past the depth for direct calls, no internal error, and no regression against the base. The three findings below are a design gap, a compile-cost backstop and a missing test. I recommend the cheap fixes for the first and third before committing.

Everything marked verified I ran myself. Probe paths are relative to `/tmp/h09b5/review-a/r3/`.

### Findings

#### 1. MEDIUM (verified): one recursion budget is shared by separate linear chains, and a direct call past it is PTS1001

- **What happens:** maxRecursiveInstances is meant to stop branching recursion. But each call of a linear (non-branching) recursion for a new base type uses 63 of the 256 before the depth limit. The main loop lowers instances breadth-first, so all chains spend the budget together.
- **`p/five_chains.pts`:** one linear `rec` called directly for 5 base types, recursing 3 levels. Snap-c gives PTS1001 "polymorphic recursion that makes more than 256 instances (`rec`)"; Node prints `4 4 4 4 4`. The base gave PTS1001 too, so this is not a regression.
- **`p/five_chains_dispatched.pts`** (the same recursion through trials) prints Node's output: at the count a trial fails quietly, and every chain is still about 51 levels deep by then.
- **Suggested fix, tried as an overlay** (`overlay/countlikedepth`): `calledInstance` takes the erased copy past the count as it does past the depth, when `erasedResultCopies` is false.
  - five_chains then prints Node's output, and `branching_direct` compiles and prints 5 (Node 5).
  - Only the two tests that pin the count's PTS1001 fail: `TestPolymorphicRecursionCountsTheInstancesItMakesPerDeclaration` and `errors/generic_instance_cap_methods`. product_blowup then stops at Box's class depth instead.
- **Why the old objection no longer applies:** D-198 rejects this alternative because "past the count a direct call is a genuine side-by-side call". Now that side-by-side instances don't count, that reason is gone.
- **Alternative:** make linear chains free, so that only a parent's second and later recursive children count.

#### 2. LOW-MEDIUM (verified): nothing bounds side-by-side instances, and polymorphic recursion multiplies them by 64

- **Setup:** f recurses 64 instances deep. Each f instance calls a chain of helpers g1…gD, each calling the next for `[U]` and `{ v: U }`. All outputs match Node.

| Probe | Base | Snap-c |
|---|---|---|
| `p/fanout2_4.pts` | PTS1001, 0.28 s | 27,304 lines of C, 2.3 s |
| `p/fanout2_7.pts` | PTS1001, 0.43 s | 231,619 lines, 15.6 s, 350 MB |
| `p/fanout2_9.pts` | PTS1001, 0.16 s | 26.4 s, 1.13 GB |

- Each extra helper level doubles the instances. Round 1's 1024-per-declaration cap also stopped these.
- **Suggestion:** a global instance budget with an honest PTS1001 as a backstop, or a ledger item for H09b5c.

#### 3. LOW, test gap (verified): the take-back decrement is still unpinned, and here is a test that pins it

- With the decrement removed (`overlay/nodecrement`), every slice test passes.
- **Suggested run test:** `p/takeback_count_test.pts`.
  - The calls for u16 and u32 sit behind a run-time-false gate. Their trials make 63 recursive instances each, then fail on `T | i16` (a union of two numeric types, the trial's own failure), so they are taken back.
  - The calls for i8, i32, f32 and string then use 252 of the 256.
- **Results:** snap-c prints `i8 3 / i32 4 / f32 6 / string s`, the same as Node. With the decrement removed, `f32` and `string` throw `TypeError Expected Box<…>, got Box<any>`.
- It depends on `u16|i16` and `u32|i16` being PTS1001 while `u8`, `i8`, `i32` and `f32` with `i16` are not, and on depth 64 and the 256 cap.

### copiesFromDynamic: checked in both directions

- **Results it calls non-copying keep their identity past the depth:** `Promise<Box<T>>`, `Iterable<Box<T>>` and `Generator<Box<T>>` (`id_promise`, `id_iterable`, `id_generator` all match Node). This matches `prepareDynamicTarget`:
  - program classes are checkable, so they are cast;
  - arrays, tuples, shapes, maps and interfaces are copied (`copyShape`).
- **Conservative cases:** library classes, functions, interfaces and enum objects all count as copying, so they become PTS1001. `typeAt` instantiates the call's type, so no bare type parameter reaches the check.
- **Arguments the erased copy mutates and keeps** (arrays, objects, closures) stay shared by reference: `args_mutated` prints `77 213 0`, the same as Node.
- **Async job order across the boundary** matches Node (`async_order`).
- **Mutations:** forcing `erasedResultCopies` to false fails `copied_results`; forcing `copiesFromDynamic` to true fails `_direct`, `_in_trials` and `_past_the_depth`. Both are pinned.
- **Remaining silent copy, from before this slice:** in the demand forms' erased-copy fallback. `id_array_dispatched` prints `false 71` on both base and snap-c, Node `true 71`: with no generic class instances there is no trial, so the whole recursion runs in the erased copy (D-101).

### The recursion-only count

- **Termination (from reading):** every infinite made-chain repeats a declaration, and that declaration's depth and count bound it. Erased copies are one per genericFunc, and their `made` is now `generic.outer`. Only the side-by-side fan-out in finding 2 is unbounded, and it is finite for a given program.
- **Consistency (from reading):** counts go up only for recursive instances (functions and classes) and down only for recursive `t.instances` when a trial is taken back. Class instances are never taken back. I found no way for the counts to drift.

### Regressions and earlier probes

- **Round-2 probes rerun on snap-c** (`round2_probes_on_snapc.txt`):
  - fixed: `b_finite_chain_40`, `b_finite_class_chain_40`, `c_method_instances_per_declaration` (33264 in 36.6 s with 1.0 GB RSS; the base took 75 s under load), and `b_box_past_depth`, which now prints all four lines since 40 < 64;
  - now honest PTS1001s, as on the base: the identity probes, plus the conservative T, tuple and array results (`b_identity_passthrough`, `b_numbers`, `b_overloads`, `b_generator`, `b_async_arrays`, `d_direct_use`, `d_nested_generic`).
- **Parent's matrix:** nothing is worse, except the intended trade: `decl_70`, `method_70`, `override_70`, `static_70` and `arrowconst_70` go from PTS1001 to a checked TypeError at run time past depth 64.

### Docs

D-198 in snap-c is still round 1's version (depth 32, 1024 per declaration, the "count cap's too" rejection). SPEC §3.7 needs the same update; the parent said it will do both.

### Side note (from before this slice, verified)

`Array.isArray` on a tuple-typed value returns false. `p/fanout_isarray_control.pts` prints `0 1 0 0` on both base and snap-c; Node prints `1 2 1 0`. It should go in the ledger.

### Files

- **Probes, overlays and results:** `/tmp/h09b5/review-a/r3/` (start with `INDEX.txt`).
- **Archive:** `/tmp/h09b5/h09b5a-review3-probes.tar.gz` (created last). The CLIs I built are deleted; `INDEX.txt` has the rebuild command.

## Round 4 (snapshot `/tmp/h09b5/snap-d`: `aa42491a5` plus the round-3 changes, committed as `0bdaa4d8f`)

Probes, tools and overlays: [h09b5a-review4-probes.tar.gz](h09b5a-review4-probes.tar.gz) (sha256 `fff4658a6487f159…`, 93 entries; `INDEX.txt`).

---

**Verdict: not ready.** The per-root design fixes everything I reported in round 3, but its give-back opens a new hole. A class instance that stays after a failed trial gets its budget back, so a valid program can make the compiler run effectively forever. The base fails on that program in 0.3 s. A small fix, prototyped below, closes it.

Everything below I ran myself. Probe paths are relative to `/tmp/h09b5/review-a/r4/`.

### 1. HIGH (verified): the compiler effectively never finishes once kept class instances get their budget back

- **What happens:**
  - A trial that is taken back keeps the class instances it made but gives back their count (`trial.counted`).
  - Those classes' bodies are lowered later, outside the trial, and can make new typed calls.
  - The new calls' trials chain back to the same root, whose budget is back at zero. If those trials fail too, their class instances also stay, uncounted.
  - The class instances then form a binary tree that only the class depth (64) bounds: about 2^64.
- **`p/giveback_classes.pts`:**
  - Every call through `g` pairs with `val` (D-190).
  - `val`'s instance makes a `Node<U>`, then fails. Converting `Map<string, U>` to `Map<string, unknown>` is PTS1001 for a typed U, the trial's own failure.
  - `Node`'s methods `left` and `right` make typed calls through `g` for `[T]` and `{ v: T }`.
  - Node prints `1 7`.
- **Results:**

| Build | Result |
|---|---|
| Base | PTS9001 in 0.31 s |
| snap-d `emit-c` | still running at the 900 s timeout, 1.5 GB RSS (`giveback_long.txt`) |
| depth limit lowered to 10 (overlay) | 25.5 s, 532,185 lines of C, 2,046 distinct `Node<…>` instances (2^11 − 2) |
| depth limit lowered to 12 (overlay) | still running at the 300 s timeout |

- **Why simply keeping class counts isn't the fix:** with class counts never given back (`overlay/keepclasscounts`), the probe ends in 1.4 s, but the parent's `trial_counts` test then fails (`f32` and `string` throw). That confirms the parent's reason for the give-back.
- **Prototype fix (`overlay/classcap`):** a second bound on the class instances recursion makes of one class declaration, across all roots, never given back (4 × 256).
  - `giveback_classes` then stops in 1.7 s with a PTS1001.
  - `trial_counts` still prints Node's output.
  - Every slice Go, run and error test passes (`mut_classcap.txt`).
  - A real fix could instead let trials own the bodies of the class instances they make (H09b5b's plan). Either way, add `giveback_classes` as a test.

### 2. MEDIUM (verified): branching recursion with class results, reached through a value call, throws at depth 0

- **What happens:**
  - Inside a value call's trial the whole tree is made by direct calls. Its `Box` instances pass the root's class budget there, and a class has no erased copy, so that is PTS1001 and counts as the trial's own failure.
  - The whole trial is then taken back, and the top-level call keeps the erased copy from depth 0.
- **`p/branch_box_via_value.pts`:** the base reports PTS1001 at compile time. snap-d compiles, then prints `TypeError Expected Box<number>, got Box<any>`; Node prints `7`.
- **`p/speculative_roots_3x6.pts`:** the same at the first call; Node prints `15`.
- **Contrast:** the same recursion called directly is an honest PTS1001.
- **Scope:** not new in round 3; trials have handled class limits this way since round 1. But it turns the base's compile error into a run-time TypeError at depth 0.
- **Suggestion:** where a direct call's instance fails only because a class it needs is past a limit, take the erased copy (when `erasedResultCopies` is false) instead of failing the trial.

### 3. LOW-MEDIUM (verified): compile cost grows with the number of roots

- Each root gets 256 instances per declaration, so cost is linear in the number of the program's calls of a branching recursion.
- **`p/branch_roots_40.pts`** (40 one-line calls, 3 deep): 24.7 s and 707 MB, printing Node's `320`. The base reports PTS1001 in 0.2 s.
- Every trial for a typed call in plain code is a root of its own, so speculative pairings add roots as well.
- **Suggestion:** a global backstop on the total number of instances recursion makes.

### 4. LOW (verified, by design): past the count, the erased copy is reached at shallow run-time depth

- **`p/branching_dispatched_deep.pts`:** n=3 and n=6 print `5`; n=9 throws `TypeError Expected Box<[[[[[[[[number]]]]]]]]>…`. Node prints `5` for all three.
- **`p/branch_numbers_deep.pts`:** with number results the output is correct (`41984 19968`, as Node).
- **Doc consequence:** D-198's known gaps say the TypeError comes "past 64 levels". Branching recursion reaches its budget after about 8 levels of a binary tree, and the docs should say so.

### 5. Docs

- **D-198 and the `maxRecursiveInstances` comment** call a root "one the program's own code made". Roots also include each trial's instance for a typed call in plain code, which is speculative and may be dropped.
- **D-198** should state the bound on kept class instances once finding 1 is fixed.
- Apart from these, D-198 and SPEC §3.7 now describe the design accurately.

### Verified OK

- **Round-3 probes rerun on snap-d** (`round3_probes_on_snapd.txt`):
  - match Node: `five_chains` (`4 4 4 4 4`), `five_chains_dispatched`, `fanout_4`, `fanout_7`, `fanout2_4`, `fanout2_7`, `takeback_count_test`, the identity probes (`id_promise`, `id_iterable`, `id_generator`), `args_mutated` and `async_order`;
  - honest or conservative PTS1001: `branching_direct` and `product_blowup` (class limits), `id_class_union`;
  - from before this slice, as on the base: `fanout_isarray_control` and `id_array_dispatched`.
- **Chains can't grow without repeating a declaration.** Chain length is bounded by the number of generic declaration nodes; arrows inside instances and methods of class instances share their node.
- **Where roots come from:** the program's own calls, each trial for a typed call in plain code, class instances made by plain code, and instances made in erased copies of top-level generics (`made: nil`). All of these are finite. Function instances from failed trials are dropped, so kept class instances (finding 1) are the only way past the budgets.
- **Erased copies past the count** use the same checked conversions as past the depth; I found no silent wrong value.

### Files

- **Probes, overlays and results:** `/tmp/h09b5/review-a/r4/` (start with `INDEX.txt`).
- **Archive:** `/tmp/h09b5/h09b5a-review4-probes.tar.gz` (created last). The CLIs built from overlays are deleted; `INDEX.txt` has the rebuild command.

## Round 5 (snapshot `/tmp/h09b5/snap-e`: `0bdaa4d8f` plus the round-4 changes, committed as `e40e4d3c7`)

Probes, tools and overlays: [h09b5a-review5-probes.tar.gz](h09b5a-review5-probes.tar.gz) (sha256 `ccfba7fbcca272e4…`, 179 entries; `INDEX.txt`).

---

**Verdict: ready, once a one-line change goes in (finding 1).** Everything is bounded now. Round 4's hang is fixed, and I found no program that compiled correctly on the base and does worse on snap-e. But one remaining path still costs 150 s where the base takes 0.36 s, and a one-line prototype brings it to 4 s with every slice test passing. If the parent prefers to keep the code as it is, finding 1 should go into D-198's known gaps.

Everything below I ran myself. Probe paths are relative to `/tmp/h09b5/review-a/r5/`.

### 1. MEDIUM (verified): failed trials can rebuild whole function trees, because a function's total is given back with the trial

- **What happens:**
  - A failed trial gives back both its per-root budget and the declaration's total.
  - The class instances it made stay. Their bodies, lowered later, start new typed calls, and each new trial rebuilds a helper's branching tree up to 256 instances, then fails and gives the counts back.
  - Only Box's class total (1024) stops this, after roughly 2,000 trials × 256 instances of work.
- **`p/giveback_work.pts`** (round 4's `giveback_classes` plus a branching helper inside `val`):

| Build | Result |
|---|---|
| Base | PTS9001 in 0.36 s |
| snap-e | 150 s and 1.05 GB, then PTS1001 for Box's total |
| Prototype `overlay/keeptotals` | 4.1 s, same PTS1001 |
| Node | `1 7` |

- **The prototype** is one line: give back the per-root budgets but keep the declaration totals. Every slice Go, run and error test passes (`mut_keeptotals.txt`), `trial_counts` included.
- **Trade-off:** instances from failed trials then use up a declaration's total for good. After more than about 16 failed recursive trees of one declaration, its real calls get an honest PTS1001.

### 2. LOW-MEDIUM (verified, same as the base): the total's stand-in-a-trial rule brings back the fallback cliff

- **What happens:** when an interface call (made only in AllInstances) meets a total inside its trial, the report stands, AllInstances fails, and ValueInstances compiles silently.
- **`p/total_cliff_interface.pts`:** 6 classes implement one interface generic method, each a branching recursion with Box results, beside an unrelated interface call.
  - The per-level tool shows AllInstances failing on Box's total, then ValueInstances and DeclaredInstances compiling without the interface instances.
  - Both lines throw `TypeError Expected Box<number>, got Box<any>`; Node prints `5` and `6`. The base prints the same two TypeErrors.
  - Without the rule (`overlay/noforeign`) it prints Node's output, but then `speculative_roots_3x6` throws at its first level, which is why the rule was added.
- **Docs:** D-198 says past a total "every case is PTS1001". That isn't true here, and the reason D-198 gives for rejecting option A (the PTS1001 survives only where every fallback makes the same trial) applies to this rule too.
- **Suggestion:** list it in D-198's known gaps for H09b5b, or make a total's PTS1001 not fall back, so the user gets the error rather than silent TypeErrors.

### 3. LOW (verified, by design)

- **Direct branching recursion now reaches the erased copy at run time.** Classes no longer have a per-root budget, so a function's budget runs out first. `p4/branch_box_deep.pts`: the base reports PTS1001, round 4 reported PTS1001, snap-e compiles and throws the checked TypeError at n=9; Node prints `1`. D-198 states this gap only for `branching_dispatched_deep`; the same applies to direct calls.
- **Distinct linear chains meet the total.** `p/chains_distinct_17.pts` (17 base types, primitive results) is PTS1001 on both base and snap-e; Node prints `68`; round 3 compiled it. For primitive results the erased copy would be exactly right past the total, but this is a design choice.

### Boundedness (from reading, plus the probes)

- **Class instances made by recursion:** at most 1024 per class declaration, never given back. That closes round 4's hang; `giveback_classes` now stops in about 4.3 s with PTS1001.
- **Kept function instances:** at most 256 per (root, declaration) and 1024 per declaration. Function instances from failed trials are dropped.
- **Instances no recursion makes:** limited by chains of distinct declarations, so by program size.
- **Roots** come from the program's own calls, trials for typed calls in code outside instances, class instances that plain code makes, and erased copies of top-level generics. All are finite.
- **The only amplification left** is finding 1, and it is bounded.

### Tests

- **Pinned:** `noforeign` fails `errors/generic_polymorphic_recursion_past_the_total`. `trial_counts`, `class_results_via_value` and `kept_classes` passed under both overlays I ran.
- **`kept_classes` pins the hang only by hanging:** the error-test harness has no time limit, so a regression would show up as a suite timeout rather than a clean failure.
- **The totals choice is unpinned:** `keeptotals` passes every slice test. If that change is adopted, `giveback_work` (expecting the PTS1001, in seconds) would pin it.

### Regressions

- **Round-4 probes** (`round4_probes_on_snape.txt`):
  - now match Node: `branch_box_via_value` (`7`), `branch_numbers_deep`, `roots_from_values`;
  - now PTS1001 for a total, as on the base: `branch_roots_40`, `speculative_roots_3x6`, `giveback_classes`;
  - `branching_dispatched_deep` is unchanged, by design.
- **61 probes from rounds 1–3** (`all_probes_on_snape.txt`): 36 match Node. The other 25 are the known honest or conservative PTS1001s and issues the base has too (`Array.isArray` on tuples, `id_array_dispatched`, the cascades).
- **`branching_direct`** now compiles and prints `5`, like Node.
- **The parent's suite on snap-e** passed (`suite-e.done`: exit 0).

### Docs

- **D-198 and SPEC §3.7 (line 238)** match the code except for finding 2's sentence and the known gaps named in findings 1 and 3.
- **`h09b5a.md`** is still the draft the parent described: it ends at "Round 4: see below" and doesn't cover this round yet.

### Files

- **Probes, overlays, mutation results and `INDEX.txt`:** `/tmp/h09b5/review-a/r5/`.
- **Archive:** `/tmp/h09b5/h09b5a-review5-probes.tar.gz` (created last). The binaries I built are deleted; `INDEX.txt` has the rebuild command.
