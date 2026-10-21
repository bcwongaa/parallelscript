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
