# H09b5 reproducer report

The `reproducer` subagent's final message (claude-opus-5-5, effort xhigh, 2026-10-21), saved verbatim by the parent. It ran in its own worktree of `7428a7b0f` (`.claude/worktrees/agent-a2054115b267da7c4`; no tracked file changed, nothing committed). Its probes, generators, matrix, prototypes (`go build -overlay` files) and RED test copies are in [h09b5-reproducer-probes.tar.gz](h09b5-reproducer-probes.tar.gz) (sha256 `876cdc8ce61f46c6…`, 2,527 entries; `INDEX.txt`). The parent's checks of its claims are in [h09b5.md](h09b5.md).

---

I reproduced all four findings on HEAD and tightened two of them: g03 mixes C177 with a separate D-078 failure, and C176 has five current triggers (four trigger kinds), not the old ones. I wrote 23 test files: 20 RED (17 run tests, 2 error tests, 3 Go tests across the two driver files) and 3 guards. Every RED test fails on HEAD for the reason its finding names, and the guards pass.

I built HEAD (7428a7b0f, same code as 27d1f0b07) in my worktree and used Node v24 on `.mts` copies as the oracle. Everything marked "ran" below I ran myself; anything I'm guessing is marked "suspected". To test causes I built three throwaway prototypes as `go build -overlay` files under `proto/`. They were never applied to the worktree and are not proposed as the fix:
- **P1:** `irType` returns a real nil instead of a typed nil.
- **P2:** the function limit inside a trial counts as the trial's own failure.
- **P3:** `maxInstances` raised to 1024, only to measure what a higher count costs.

## 1. Summary

| Finding | Reproduced? | Probe | HEAD (ran) | Node |
|---|---|---|---|---|
| C172(a) function limit | yes | `c172/c172_fn_limit_2x33.pts` (also m1 `decl_*`) | PTS1001 "a generic function with more than 64 instances (`mapOf0`)", exit 1. Each function needs only 33 instances; pairing gives each 66 | `528 528` |
| C172(a) class limit | yes | `c172/c172_class_leak_25.pts` | compiles; calls 18–24 throw `Expected Box<A18>, got Box<any>`, then an uncaught TypeError, exit 1. The control without `mkB` matches Node | 0..24, then `1` |
| C172(a) as given | yes | `orig/i09`, `orig/i10` | i09: PTS1001 ×2 then PTS9001, exit 3. i10: PTS1001 ×6, exit 1. **These are 70 real uses, not speculation.** Plain code with 65 `Box` instances is PTS1001 too | 2415 |
| C172(b) PTS9001 | yes | i09, m3 `viavalue_65/70`, `c172/c172b_extends_typed_nil.pts` | PTS1001, then PTS9001 nil dereference. The stack (PTS_DEBUG=1) shows it in the last fallback, `ProgramWith(…, DeclaredInstances)`. It also happens with no limit at all: `extending Map is not supported yet` then PTS9001 | 2415 / 1 |
| C172(c) growth | yes | m4 | esc_150x50: 92,388 lines of C, 21.6 s, 386 MB. methods_60 (= s_60): 93,741 lines, 49.0 s, 613 MB. decls_60: 47,541 lines, 10.0 s. esc_0x20: 2,115 lines | all OK |
| C172(d) maxDynamicVariants | yes | `orig/q08`, m5 `literal_{num,str}_{5,6,8}` | calls 5+ throw `Expected Box<1>` (or `Box<"k0">`) `, got Box<any>`. many_value_keys_70 and unions_70 are correct (3,415 and 3,015 lines) | all lines |
| C176 fallback | yes, 5 current triggers | `c176/*.pts` + controls, m6 (25 cases) | 19 of 20 trigger × victim cases TYPEERR; every control matches Node. Details below | victims print 6 |
| C176 emit-c | yes, by simulation only | `tools/emitc_vs_build.sh` with `PTS_CC=rejecting_cc.sh` | emit-c prints 275 lines with `pts_method_instance_find`; build silently compiles 238 lines without it; the program prints `victim TypeError Expected Box<number>, got Box<any>`, exit 0 | `victim 6` |
| C177 one IR type | yes | m7 (98 cases), `matrix/m7_summary.txt` | clean pairs (each call alone passes, together they fail): see section 3 | values |
| C184 polymorphic recursion | yes | `c184/*.pts`, m8 (24 cases) | per form: see section 4 | 6 |

**i09_literal_polymorphic_recursion, classified.** It is C184's family: recursion through an object literal's generic method, which is a demand by name (D-191). Under AllInstances it hits C172(b)'s typed nil (PTS9001). The driver then falls back to ValueInstances, where literals' methods list no instances, so the call takes the erased copy and throws. P1 alone makes it print 6.

**c10_cliff and t04** pass on HEAD (verified).

**C176's current triggers.** Each is verified with a control that matches Node:
1. **A class instance body.** Pairing makes `Weird<Dog[]>`, whose method fails on D-094, and class bodies are lowered outside every trial. The whole build falls back to DeclaredInstances.
2. **A relaxed cast (D-066) in a kept instance.** It is checked after lowering, so the build falls back to DeclaredInstances.
3. **The limit's typed nil** (literal-method or local-arrow recursion). The build falls back to ValueInstances or DeclaredInstances.
4. **An AllInstances call site that fails to lower** (`converting boolean to given` at an optional-before-rest interface call; the pool's `a05` union conversion). The build falls back to ValueInstances.
5. **A silent case.** m1 `arrow_1x65` prints "OK" only because it fell back to DeclaredInstances: 5,628 lines of C against 9,708 for `arrow_1x64`.

Scan of 1,712 distinct H09 probes from the evidence archives: 9 need a fallback (5 go to ValueInstances, 4 to DeclaredInstances). None has AllInstances C that clang rejects. `TestCompilerFailsCleanly` with `PTS_SMOKE_CLANG=1 PTS_SMOKE_MUTANTS=0` passes (127 s). So emit-c and build diverge today only when clang or the linker rejects the C, and I found no natural trigger for that.

## 2. Root causes (file:line on HEAD)

- **C172(b), the typed nil.** `pts/lower/lower.go:3975` does `return l.classInstanceOf(generic, t, node)`. A failed `*ir.Class` (the limit at `generic_classes.go:175`, or the superclass failure in `declareInstanceSuperclass`) comes back as a non-nil `ir.Type`. `closures.go:498` (`target == nil`) misses it, and the chain `closures.go:503` → `dynamic.go:337` (`prepareDynamicTarget`) → `dynamic.go:98` (`t.(*ir.Class).Shape`) dereferences it. Fix: return a real nil. That is P1; it also removes the cascading "`inner.value` is not supported yet".
- **C172(a), the count.** `specialize` (`instances.go:737`) pairs every demand with every erased value of the same erased function type (`instances.go:744`, `reaches` at 807/813, which compares types only) and every same-named method (756). The only flow filter is `calledOnlyName` (`generics.go:130`). Both limits count every instance ever made:
  - Functions (`generics.go:997`): in a trial, `t.foreign = true` (999) makes the speculative limit stand for the whole program.
  - Classes (`generic_classes.go:175`): `tryInstance` (`instances.go:872–915`) never takes back class instances, `instanceClasses` or `classInstanceBodies`, so unused but successful speculative class instances fill the limit (`class_leak`).
- **C172(c), growth.** The same W×D pairing. Forwarders (D-194, `forwarding.go`) double the cost per pair.
- **C172(d).** `generics.go:986–995`: a dynamic-result call reuses an instance only when `madeFor` finds identical keyParts, and literal types are their own keyParts. Past 4 variants it returns nil, which means the erased copy, even when the existing instance's IR result is the same class.
- **C176.**
  - `driver.go:31` and `:57` loop `lower.Fallbacks` for the whole program; `fallsBack` (`driver.go:94–97`) falls back on any compile or internal error.
  - Failures a trial can't own: class instance bodies (`lower.go:244–247`, main loop, outside trials); relaxed casts (`lower.go:258`, checked after lowering); the limit (`generics.go:999`); call-site lowering under AllInstances (`keyedInstanceCall`, `instances.go:423ff`, whose `l.convert` reports in non-trial code).
  - emit-c: `EmitC` (`driver.go:31`) falls back on lowering errors only; `Build` (57) also falls back on clang and linker errors.
- **C177.**
  - Functions: `genericInstanceFor` (`generics.go:986–990`) reuses an instance when its IR arguments and literals are equal and the call is typed (`!dynamic`), so the second checker type gets the first one's signature.
  - Classes: `classInstanceOf` (`generic_classes.go:171`) matches on IR arguments only, without even the literals.
  - Brands (suspected): `instantiate` (`generics.go:206`, `unbranded`), called at `generics.go:977`, makes the instance for `Id` an instance for `number`.
- **C184.**
  - Dispatched demands (`instances.go:390`) and declarations' value demands are made at every fallback level. Demands by name are AllInstances only (`instances.go:423`).
  - Once the typed nil is fixed, the class limit inside a trial is that trial's own report, so the demand past the limit keeps the erased copy.

## 3. C177 by pair (ran; `matrix/m7_summary.txt`)

**Clean pairs** (each call alone matches Node):
- `infer` and `awaited` with Promise<number> / Promise<string>, and `twin` with `{kind:"a"}` / `{kind:"b"}`:
  - decl, method, arrowconst and klass forms: PTS1001 `using a Box as a Box`, in both orders.
  - value form: TYPEERR (the second demand's trial doesn't fit, so it takes the erased copy).
- `literal` (`1` vs `number`):
  - functions: OK (they compare literal types).
  - klass form: PTS1001 (classes don't).

**Confounded pairs:**
- `any` / `unknown`: that call fails on its own (D-078: a `Box<number>` argument where `R<any>` = `Box<any>` goes).
- Brand `Id`: fails on its own (the instance made for `number`, above).
- Narrowed `arrays`: depends on order (D-195's gap).

**g03:** its error at 7:10 is the body's `new Box(v as any)` (D-078, not C177; `c177/g03_promise_only.pts` fails with one call). Only the 11:11 error is C177. Don't use g03 as C177's RED test.

## 4. C184 per form, and what "honest" means for each (ran; m8, n=3 / n=70)

| Form | HEAD | P1 |
|---|---|---|
| decl, method, static, arrowconst, declplain | PTS1001, plus the `inner.value` cascade | same |
| dispatched, value | PTS9001 | n=3 prints 6; n=70 is a checked TypeError past the limit |
| interface, literal | AllInstances PTS9001 → ValueInstances → TypeError | n=3 prints 6; n=70 TypeError |
| arrowlocal | AllInstances and ValueInstances PTS9001 → DeclaredInstances → TypeError | n=3 prints 6; n=70 TypeError |
| valuemap | PTS1001 (function limit) | P2 is needed: OK for n=3 and n=70 |
| override | PTS1001 | **TypeError even at n=3** (risk 1 below) |

- **Option A:** keep D-049's PTS1001 for chains of direct calls, and make a trial that dies at the limit report it.
- **Option B:** past the limit, any call takes the erased copy with a checked result. Every form then prints 6 at n=3 and throws a checked TypeError past depth 64.

I recommend B, through a decision that supersedes D-049's sentence on the limit.

P3 (count 1024) makes the recursion forms compile, but with 78k–116k lines of C, 31–62 s builds and about 0.5 GB for an n=3 program. Raising the count is not the fix. A depth or chain limit kept near 64 would keep these at about 1.7 s, as P1 does, and also let i09, i10 and `plainboxes_65` compile; under P3 those print Node's output.

## 5. Matrix (197 cases)

Run with `tools/gen_matrix.py` then `tools/run_matrix.py --jobs 2 --loc`; the HEAD run took about 10 minutes.

| Family | HEAD | P1 | P1+P2 | P1+P2+P3 |
|---|---|---|---|---|
| m1 fnlimit (16) | OK 12, PTS1001 4 | same | OK 16 | OK 16 |
| m2 classleak (8) | OK 6, TYPEERR 2 | same | same | OK 8 |
| m3 limit (5) | OK 1, PTS1001 2, PTS9001 2 | OK 1, PTS1001 4 | same | OK 5 |
| m4 growth (12) | OK 12 | | | |
| m5 variants (9) | OK 4, WRONG 5 | | | |
| m6 fallback (25) | OK 6, TYPEERR 19 | OK 16, TYPEERR 9 | same | |
| m7 one IR type (98) | OK 21, PTS1001 69, TYPEERR 8 | | | |
| m8 polyrec (24) | PTS1001 14, PTS9001 4, TYPEERR 6 | OK 5, PTS1001 12, TYPEERR 7 | OK 7, PTS1001 10, TYPEERR 7 | OK 12, PTS1001 10, TYPEERR 2 |

There were no CRASH or BUILD-TIMEOUT results; HEAD's totals are OK 62, PTS1001 89, TYPEERR 35, PTS9001 6, WRONG 5. m5's WRONG lines are the same TypeError, printed through `e.message`. The P1 and P1+P2 columns stay red on class bodies, relaxed casts, C177 and the dynamic variants.

With P1+P2, all 108 `run/generic*` tests pass except my unfixed REDs, and exactly one existing pin fails as expected: `TestInterfaceCallsMakeNoDemandsInTheFallbackBuilds` (driver_test.go:189, "the first build reaches the instance limit").

## 6. RED tests

All are uncommitted in my worktree (paths below are relative to it); copies are in `/tmp/h09b5/repro/red/`. All run tests' `// expect:` blocks come from Node's output, and the Node twin `TestRunMatchesNode` fails on HEAD for all 18 run files except the guard.

**Run tests** (`pts/tests/run/`). Each fails `TestRun` on HEAD:

| Test | Finding | HEAD failure | Turns green under |
|---|---|---|---|
| `generic_polymorphic_recursion_dispatched` | C184 | PTS9001 | P1 |
| `generic_polymorphic_recursion_value` | C184 | PTS9001 | P1 |
| `generic_polymorphic_recursion_interface` | C184 | prints a TypeError where Node prints `6` | P1 |
| `generic_polymorphic_recursion_literal_method` | C184 | prints a TypeError where Node prints `6` | P1 |
| `generic_polymorphic_recursion_local_arrow` | C184 | prints a TypeError where Node prints `6` | P1 |
| `generic_fallback_spares_unrelated_interface_call` | C176 + C184 | TypeError on both lines | P1 |
| `generic_speculative_function_instances` | C172(a) | PTS1001 mapOf0 / mapOf1 | P1+P2 |
| `generic_speculative_class_instances` | C172(a) | lines 18–24 throw | needs flow pairing or a depth limit |
| `generic_fallback_spares_unrelated_class_body` | C176 | `victim threw …` and `dogs 1` missing | narrower fallback |
| `generic_fallback_spares_unrelated_relaxed_cast` | C176 | `victim threw …` | narrower fallback |
| `generic_fallback_spares_unrelated_call_site` | C176 | `victim TypeError …` (the trigger's own call prints the same either way) | narrower fallback |
| `generic_dynamic_variants_past_cap` | C172(d) | `5 threw` / `6 threw` | |
| `generic_instances_same_ir_type_promises` | C177 | PTS1001 `using a Box as a Box` | |
| `generic_instances_same_ir_type_twins` | C177 | PTS1001 `using a Box as a Box` | |
| `generic_class_instances_literal_type_arguments` | C177 | PTS1001 `using a Box as a Box` | |
| `generic_instances_branded_type_arguments` | C177 | PTS1001 `using a Box as a Box` | |
| `generic_values_same_ir_type_promises` | C177 | `Promise<string>: TypeError …` | |

**Error tests** (`pts/tests/errors/`). Each fails `TestErrors` on HEAD with "expected compile errors, got … PTS9001", and passes under P1:
- `generic_class_instance_failure_value_calls`: C172(b) without the limit; does not depend on how the limit is defined.
- `generic_class_limit_value_calls`: C172(b) at the limit. It pins D-049's count limit, so it must become a run test (Node 2415) if the limit changes.

**Go tests:**
- `pts/driver/narrow_fallback_test.go`:
  - `TestEmitCPrintsTheCThatBuildCompiles` uses a `PTS_CC` wrapper. HEAD: "emit-c printed C that the build didn't compile (AllInstances' lookup in it: true; in the built C: false)".
  - `TestAValueWhoseClassInstanceDoesntLowerCostsNoOtherValueItsInstances`. HEAD: `pts_fn_main_named_number` missing.
- `pts/driver/speculative_instances_test.go`: `TestValuesThatNoTypedCallReachesGetNoInstances`. HEAD: "pts_fn_main_a0_C0 is an instance no call can use".

**Guards** (pass on HEAD, must keep passing):
- `errors/generic_polymorphic_recursion_direct` (`// error: with more than 64 instances`).
- `run/generic_values_read_back_from_any`.
- The C177 solo controls and the m6 `none__*` controls in the matrix.

These RED tests will also make `TestCompilerFailsCleanly` fail until fixed: some need the fallback, some are PTS9001.

## 7. Proposed split (dependency order)

Flow-based pairing is **not** needed first. P1+P2 fix the internal error and the limit-in-trials without it. The narrower fallback needs trials to own the class instances they make (and their bodies), relaxed casts dropped by the trial that owns them, and AllInstances call sites lowered tentatively; flow analysis wouldn't remove the relaxed-cast or call-site triggers anyway. Flow pairing then fixes growth and, together with a depth limit instead of a count, the class leak.

1. **H09b5a: no internal error at the limit; the limit in a trial is the trial's.**
   - Scope: P1 + P2 + no cascade diagnostic; decide option A or B for direct calls (override).
   - Accept: the C184 runs ×5, `…_interface_call`, `generic_speculative_function_instances`, both errors tests and the guard pass; m8 has no PTS9001 and no new TYPEERR at n=3; update the pin `TestInterfaceCallsMakeNoDemandsInTheFallbackBuilds`.
2. **H09b5b: a narrower fallback; emit-c prints what build compiles.**
   - Scope: per-value or per-call take-back for class bodies, relaxed casts and call sites. For emit-c, either share Build's chosen level or run `clang -fsyntax-only` in emit-c's loop.
   - Accept: the three `…_spares_unrelated_*` tests and the two `narrow_fallback_test.go` tests pass; m6 is all OK; the pool scan shows no whole-program fallbacks other than intended errors.
   - Pins to revisit: `TestABuildThatOnlyItsSpeculativeInstancesStopIsLoweredAgainWithoutThem`, `TestABuildThatOnlyItsDemandsByNameStopKeepsTheGenericArrowsInstances`, `needsFallback`/`run/generic_instances_fall_back.pts` (suspected).
3. **H09b5c: only instances calls may use; the limit.**
   - Scope: flow-based pairing (values that escape through `any` still meet demands whose callee comes from `any`), plus a depth or chain limit with a large total cap (a new decision superseding D-049/D-050's count).
   - Accept: `generic_speculative_class_instances` and `TestValuesThatNoTypedCallReachesGetNoInstances` pass; the read-back guard still passes; esc_150x50 is close to esc_0x20 (2,115 lines); methods_60 and decls_60 grow linearly; i09, i10 and `plainboxes_65` either print 2415 (if the limit changes; convert the errors test then) or stay an honest PTS1001.
4. **H09b5d: one IR type.**
   - Scope: tell instances apart where the IR signature differs for the same IR arguments (functions and classes, literals included); key dynamic results by the IR types of their payload instead of `maxDynamicVariants`; keep brands in the substitution.
   - Accept: the five C177 tests and `generic_dynamic_variants_past_cap` pass; m7's clean pairs are all OK; the solo controls don't change.

**Recommendation:** a, b, c, d in that order. a is small and verified by prototype. b removes the cliff before the riskier flow work, so failures in c stay local. d is independent and can run in parallel.

## 8. Risks and open questions

1. P1 alone turns `override_3` from PTS1001 into a run-time TypeError at depth 0: the trial dies at the class limit and the dispatched call takes the erased copy. H09b5a has to pick option A or B.
2. Changing the limit to a depth limit reopens D-049/D-050's "more than 64 instances" (a superseding decision; not a D-L).
3. Side defects I found, outside H09b5's scope:
   - `converting boolean to given` at an interface call with an optional parameter before a rest parameter (AllInstances only; first seen in the H09b3a review's r02).
   - The evaluation order of `recv().g(maybe())` (C207, known).
   - The branded-instance case is suspected from reading `generics.go:206`; I did not confirm it in a debugger.
4. No natural clang-rejected AllInstances C exists today; the emit-c finding rests on the `PTS_CC` simulation.

Everything (probes, generators, runner, results, prototypes as overlay files, RED copies and logs, indexed in `INDEX.txt`) is in `/tmp/h09b5/h09b5-reproducer-probes.tar.gz`. The tables are `matrix/table.tsv` and `matrix/report.txt`; `bin/` and the tool binaries are left out.

The RED tests are in `/home/bryan/projects/parallelscript/.claude/worktrees/agent-a2054115b267da7c4`, under `pts/tests/run/`, `pts/tests/errors/` and `pts/driver/`. Nothing is committed and no tracked file was changed.
