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
