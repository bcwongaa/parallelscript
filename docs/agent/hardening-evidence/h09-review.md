# H09 review (opus-reviewer, claude-opus-5-5, xhigh)

Saved by the parent from the child's final messages, unedited below each rule. Probes: [h09-review-probes.tar.gz](h09-review-probes.tar.gz) (round 1), [h09-review2-probes.tar.gz](h09-review2-probes.tar.gz) (round 2), [h09-review3-probes.tar.gz](h09-review3-probes.tar.gz) (round 3), [h09-review4-probes.tar.gz](h09-review4-probes.tar.gz) (round 4), [h09-review5-probes.tar.gz](h09-review5-probes.tar.gz) (round 5), [h09-review6-probes.tar.gz](h09-review6-probes.tar.gz) (round 6) and [h09-review7-probes.tar.gz](h09-review7-probes.tar.gz) (round 7), `INDEX.txt` inside each. Snapshots reviewed: round 1 `/tmp/h09/snap3` (HEAD `14ad0e6b7` plus the change; patch sha256 `b46177957c2fedb4…`); round 2 `/tmp/h09/snap4` (patch sha256 `d23a400d5a3c8a23…`, CLI `9063c807…`), by a new reviewer instance, since round 1's couldn't be resumed across sessions; round 3 `/tmp/h09/snap5` (`git diff HEAD -- pts` sha256 `1d4c9adf…`, CLI `d33d4b4c…`), round 2's reviewer resumed, its report recovered by the third session from the second's transcript (the second was cut off after it arrived); round 4 `/tmp/h09/snap6` (`git diff HEAD -- pts` sha256 `18ed99d2…`, CLI `01a68785…`), a new instance in the third session, given rounds 2 and 3's reports and probes; round 5 `/tmp/h09/snap7` (`git diff HEAD -- pts` sha256 `c61e9a2e…`, CLI `aefd0ec9…`), round 4's instance, resumed; round 6 `/tmp/h09/snap8` (`git diff HEAD -- pts` sha256 `674dcaf7…`, CLI `3c039ef6…`), the same instance, resumed; round 7 `/tmp/h09/snap9` (`git diff HEAD -- pts` sha256 `f333e191…`, CLI `f192de25…`), the same instance, resumed: the committed change.

## Round 1

---

# H09a review (opus-reviewer, claude-opus-5-5)

**Verdict: not ready.** The closure machinery works; every closure probe matched Node. The problem is the trials. They don't protect programs that compiled on the base, and several programs that compile and match Node on base now fail on the snapshot with PTS1001 or PTS9001. D-148's guarantee, "no program that compiled before fails to compile now", no longer holds. Every regression comes from speculative pairing: every demand is paired with every generic arrow of the same erased type, even arrows that never reach that call.

The probes are in `/tmp/h09/review/h09-review-probes.tar.gz` (`INDEX.txt` inside; paths below are relative to `/tmp/h09/review/`). Commands: `tools/tri.sh FILE` runs base, snapshot and Node; `tools/bs.sh FILE` compares base with the snapshot. "Verified" means I ran it; "suspicion" means I reasoned it from the code.

## Regression sweep
- `tools/gen_spec.py` and `gen_spec2.py` generate 212 programs. In each, a local generic arrow is used only through `any`, plus one unrelated typed demand; spec2 adds the arrow passed for real.
- Base against snapshot: 35 programs that exit 0 on base break on the snapshot, 19 with PTS1001 and 16 with PTS9001 (`fuzz/spec1.txt`, `fuzz/spec2.txt`).
- Across all 962 existing run, corpus, error and stress programs, no exit code changes (`csize/sizes.txt`).

## Findings

**F1 (high, verified): class instances made in a trial are lowered outside it.**
- `lowerPending` doesn't drain `classInstanceBodies`, and `ensureMembers` reports with `l.function = Module.Init`, which counts as foreign.
- A dropped trial still leaves its class instances behind (`Weird<string>`), and their bodies fail later.
- Repro: `p/f01_foreign_class_body.pts`. A local `<T>(x)=>new Weird(x)` is used through `any`, and `Weird.inc` narrows `typeof v === "number"`. The only demand is `use(make)` with a string.
  - Base and Node: `2` / `s`.
  - Snapshot: `7:14 - error PTS1001: converting string to number is not supported yet`.
- Also in `fuzz/spec1/cell_class__*`.
- Fix: lower class instance bodies inside the trial and roll back the trial's class instances (`generic.instances`, `classes`, `l.instanceClasses`, `l.classInstances`, `classInstanceBodies`). Do this together with F8's prefilter.

**F2 (high, verified): an unexplained failure in the trial's own code makes the trial stand.** `case …, l.unexplained != unexplained: return` keeps the trial's errors.
- **(a) Cascades.** This is the common idiom: a local declared in a narrowed branch.
  - Repro: `p/f02c_cascade_stands.pts`, `if (Array.isArray(x)) { const n = x.length; console.log(n) }`. `x.length` reports, then using `n` leaves an unexplained failure.
  - Base and Node: `an array of 2` / `2 2`. Snapshot: `13:17 - error PTS1001: x.length is not supported yet`.
  - 19 fuzz cases fail this way, including `typeof x === "number"` for an array or class type argument (`number[] & number` isn't `never`, so `narrowsAway` misses it).
- **(b) Lowerer context missing from the scope snapshot: `staticContext`.**
  - Repro: `p/f02b_static_this_base_runs.pts`, `static make = <T>(x: T) => { console.log(this.tag); … }`.
  - Base and Node: `S` / `2`. Snapshot: `7:17 - error PTS1001: property access expression (which the compiler failed to lower without saying why)`.
- Fix:
  - Roll back when the unexplained node is inside the trial's instances' code, and restore `l.unexplained`.
  - Snapshot `staticContext` in `scope` and restore it in `instanceBody`.
  - Evidence: `dbg/fix2-experiment.patch` (overlay build only) cures (a) and (b); `fuzz/fix2.txt` shows what remains: F1, F3 and F4.

**F3 (medium, verified): D-066 relaxed casts from trial code are checked after lowering.**
- Neither the rollback nor the trial covers `l.relaxedCasts`.
- Repros:
  - `p/f03_relaxed_cast_dropped.pts`: the instance is dropped because it doesn't fit, but its cast stays.
  - `p/f03b_relaxed_cast_fits.pts`: the instance is kept.
  - Base and Node: `1 1 5` and `1 5`. Snapshot: `PTS1001 using a Base as a {id: number; tag?: …}, when it may refer to a Derived…`.
- Fix: record the function in each `relaxedCast` and filter dropped ones by `t.owns`. For kept trials, a failing check should unlist the instance rather than report.

**F4 (high, verified): ICE regression.**
- In an instance, a branch that tsc types as an intersection (`T & string` with `T = number[]`) lowers to invalid C.
- This is pre-existing for direct calls and for declarations used as values (`p/f06_intersection_direct.pts`, `p/f06c_…`; base already PTS9001). The change makes it reachable from every generic arrow, speculatively.
- Repro: `p/f06b_intersection_speculative.pts`, a local `typeof x === "string" ? x.length : 0` arrow plus an unrelated `f([1,2])` demand.
  - Base compiles (and throws C09's TypeError).
  - Snapshot: `PTS9001 … no member named 'utf16_len' in 'struct pts_array'`.
- 10 fuzz cases go from exit 0 to exit 3 (for example `fuzz/spec2/ternary_len__array__local.pts`, `spec1/string_methods__nullable.pts`).
- Fix: before H09a lands, either extend `narrowsAway` to treat an intersection of the concrete argument with a primitive it can't be as dead, or make the lowering report instead of miscompiling, so the trial can drop it. It can't wait for H09b.

**F5 (medium, verified): an over-limit or non-lowering instance now becomes a runtime TypeError in fully typed code.**
- `inv/i10` (70 demands that really need instances): base `PTS1001 more than 64 instances`; snapshot `Uncaught TypeError: Expected Map<string, C64>, got Map`.
- `p/f04_declaration_fallback.pts`: a declaration through a value whose instance doesn't lower. Base `PTS1001 x.length is not supported yet`; snapshot `TypeError Expected Box<number>, got Box<any>`.
- The flip side: `p/f04b_unrelated_declaration.pts` now matches Node, where base gave a spurious PTS1001 for an unrelated pairing.
- My answer on `maxInstances`: not acceptable as is. Keep the limit diagnostic when the demand's own generic hits it and its instantiated signature fits. Record the trade-off in D-190 and in SPEC §3.7, and pin it with a declaration test.

**F6 (medium, verified): code size and compile time.**
- Every recorded arrow gets an instance for every demand type, even arrows only called by name.
- `gen/blow_150x50.pts` (150 local arrows, 50 types, one arrow passed): 85,957 vs 8,806 lines of C, 25 s build, 10 MB binary. 60×40: 30,277 vs 5,396 lines, 9.2 s vs 2.2 s.
- The scope snapshot clones all of `l.locals` per arrow and again per instance. `gen/scope_b.pts` (20k ordinary locals, 300 arrows × 20 types): lowering takes 3.87 s and 656 MB, against 0.53 s and 78 MB without the filler locals.
- Fix:
  - Snapshot only the arrow's free symbols, or overlay the snapshot instead of copying it.
  - Monomorphize function-local consts that are only ever called by name (as D-102 does) instead of recording them.
  - Prefilter pairs with `fitsInstance` on the instantiated signature before making the instance.

**F7 (low, verified): signature-key collision for dynamic results.**
- `p/f05_signature_collision.pts`: `f<string>(2)` finds the `T=number` instance and reports `TypeError Expected Box<string[]>, got Box<number[]>`.
- It's still checked, so never silently wrong in my probes, but the message names the wrong type.
- A generator declaration with T only in its result gets a `gens_value` (T=any) instance listed, which blocks a correct one.
- Fix: key entries by signature plus result type arguments, and don't list instances whose type arguments are `any`/`unknown`.

**F8 (low, suspicion from code, partly verified):** things a dropped trial leaves behind:
- extra captures added to enclosing closures by `capture()`, which can mark cells and add ready checks;
- `l.helpers[key] = nil` entries whose report was taken back, so a later use gets nil with no diagnostic;
- `firstDynamicCall` / `firstDynamicWrite` / `firstResult` set by dropped code, giving extra descriptors;
- the trial's class instances and their generic methods;
- class instances made while `making` > 0 whose member errors were taken back.

**F9 (info): docs.**
- SPEC §3.7 still says generic arrows elsewhere keep the erased copy (a known issue).
- D-148's "never fails to compile" claim no longer holds.
- The identity table should mention tuples next to `any[]` (C70 is a checked copy).
- My emit-c count for transducers on the snapshot is 4,845 lines, not 4,620 (base 5,138).

**F10 (medium): tests.**
- No run test covers:
  - async or generator closures that capture variables across `await`/`yield` (`p/a2_async_capture.pts` passes);
  - a named function expression calling itself (the `self = pts_env` path, `p/a1`);
  - static or field arrows using `this` (`p/a4`, would have caught F2b);
  - constructor or `super` arrows (`p/a7`);
  - generic class field arrows (`p/a8`).
- `generic_instances_that_dont_lower` covers none of the cascade, class-body, relaxed-cast or intersection failures.
- The key invariant has no test: a local arrow used only through `any`, plus an unrelated demand, must compile as on base.
- There's no lowering unit test of the rollback itself.
- Expectations otherwise come from Node, and the no-diff tests are justified.

## What checked out (verified)
- Closure environments: capture lists shared and in order, cells, early cells, `this`, loops, curried and nested arrows, outer substitution, generic class instances, async and generator closures, a 200k-closure GC stress, `.par.map`, cross-module `let`, name collisions, and identity/`.name`/`.length` all match Node.
- The D-155 `any` boundaries are unchanged (reproducer probe `a04` is the same on base and snapshot; `generic_values_any_boundaries` passes).
- The runtime change (C70) is sound. It is a checked copy; a union tries its tuple member without a copy first; a self-containing tuple is guarded. A frozen tuple thaws when copied, the same as the existing array copy behaviour.
- All 7 new test expectations pass on the snapshot.

## Scope
- F1–F4 and F6's prefilter belong in H09a: they are regressions this change introduces.
- The full dead-branch work (N3) can stay in H09b, but F4's intersection guard can't.
- Nothing in H09a's scope seems to be missing otherwise.

## Round 2

---

# H09a review, round 2 (opus-reviewer, claude-opus-5-5, xhigh)

**Verdict: not ready.** Every round-1 finding is now fixed, mitigated by the fallback, or a documented trade-off. Closure sharing held up under every probe I wrote. But I found two new runtime regressions. Both are fully typed programs that compile on both trees: they match Node on the base and throw an uncaught TypeError on snap4. The driver fallback doesn't help, because nothing fails to compile. Both are small to fix, and both contradict D-190's "none worse".

What I reviewed: `/tmp/h09/snap4`. Its `git diff HEAD -- pts` has sha256 `d23a400d…`, the same as the main tree's code right now. Its CLI is `9063c807…`. I compared it with the base (`7fad8ae1…`) and Node v24 (.mts copies). "Verified" means I ran it. Probes are in `/tmp/h09/review2/h09-review2-probes.tar.gz` (`INDEX.txt` inside); paths below are relative to `/tmp/h09/review2/`.

## New regressions

### R1 (high, verified): `isUninferred` drops legitimate instances when one of several type arguments is `unknown` or `any`

- **Where:** `pts/lower/instances.go:225` skips the pairing if *any* inferred argument is any or unknown.
  - With one type parameter this is harmless: `Box<unknown>` is the same IR class as the erased copy's `Box<any>` (probes u01–u03 pass).
  - With two or more, the erased copy erases the concrete ones too. A demand like `<string, unknown>` then gets `Pair<any, any>`, and the call's conversion rejects it.
- **What it breaks:** declarations and module-scope consts used as values. Base made instances for these.
- **Fallback:** none. `DeclaredInstances` applies the same filter, and the program compiles anyway.
- **Repro:** `p/u05_result_error_unknown.pts`. It has no `any` anywhere: a `Result<T, E>` factory called as `f(41)` through `<T, E>(value: T) => Result<T, E>`, so tsc infers E as unknown. This is a common shape.
  - Base and Node: `42`.
  - snap4: `Uncaught TypeError: Expected Result<number, unknown>, got Result<any, any>`, exit 1.
- **Also:** `p/u04_partly_unknown.pts` covers `unknown` inferred, `unknown` explicit and `any` inferred, for a declaration and a module-scope const. All six lines match Node on the base and throw on snap4.
- **Suggested fix:** skip only when the any or unknown comes from failed inference, not from the call. For example, skip only if the demand's own resolved type arguments (`PtsTypeArgumentsOfSignature(d.signature)`) contain no any or unknown. That keeps F7's purpose (the `gens_value` instance for `g<string>`).

### R2 (high, verified): dynamic-result instances break calls the base served correctly

- **Where:** `pts/lower/instances.go:94`. Under `AllInstances`, a call whose result is a promise or generator of generic class instances is now a demand, and its table entry is keyed by the IR signature alone.
- **Why it breaks:** a call with T as any or unknown shares the signature `(number) => value` with a typed call. Because `isUninferred` makes no instance for it, the runtime lookup finds the typed call's instance. That instance makes `Box<number>`, which the `await` rejects as a `Box<any>`.
- **Minimal repro:** `p/d06_dynamic_collision_base_runs.pts`. It calls `f(5)` (T inferred unknown, T only in the result), and has `f<number>(6)` behind a branch that never runs.
  - Base and Node: `inferred 5`.
  - snap4: `Uncaught TypeError: Expected Box<any>, got Box<number>`, exit 1.
- **Other probes:**
  - `p/d02_dynamic_collision_any.pts`: `f<any>` and `f<unknown>`, for a declaration and a local arrow. Base runs them; snap4 crashes at the first `await`.
  - `p/d03_dynamic_collision_generator.pts`: generators. `g<any>(1)` runs `g<string>`'s instance, whose `n as any` is checked as a string: `TypeError: Expected string, got number`. Base and Node print `any 1`.
- **Effect on D-190:** its known gap C173 says only that "the second call's `await` rejects" a value. It doesn't say that calls which worked on the base now throw, so "with none worse" is false.
- **Suggested fix for H09a:** list a dynamic-result instance only if no other demand of the same erased value has its IR signature with different type arguments; otherwise leave all of them on the erased copy. The proper fix (C173) is to key entries by type arguments.

## Round-1 findings, re-checked on snap4

| | Status | Evidence |
|---|---|---|
| **F1** (class instance from a dropped trial) | Fixed by the fallback | `f01` matches base and Node; the trace CLI shows the fallback ran (`converting string to number`) |
| **F2a** (cascade in the trial's own code) | Fixed in lowering, no fallback | `f02c` matches Node |
| **F2b** (`staticContext`) | Fixed | `f02b` matches; `f02` now matches Node (base threw) |
| **F3** (relaxed cast in a dropped instance) | Fixed in lowering | `f03` |
| **F3b** (relaxed cast in a kept instance) | Fixed by the fallback | `f03b` matches |
| **F4** (intersection miscompile) | Mitigated, not fixed | `f06b` is avoided by `calledOnlyName`. Other shapes compile to C that clang rejects; the build falls back and runs clang twice. `spec2/ternary_len__array__local.pts` matches the base. Direct calls are still PTS9001 on both (`f06`, `f06c`). |
| **F5** (instance limit) | Fixed: the limit stands and falls back | `inv/i10`: base and snap4 both give the PTS1001 |
| **F5 / f04** (instance that doesn't lower) | Trade-off, in the D-190 draft | Base PTS1001 → snap4 TypeError at run time. **Also applies to dispatched generic methods** (`specializeMethod` now uses `tryInstance`, `instances.go:386`): `p/t01_dispatched_method_trial.pts`, base PTS1001, snap4 `3` then `TypeError Expected Box<number>, got Box<any>`, Node `3 3`. No test pins either case. |
| **F6** (code size, scope cost) | Partly fixed | See F6 residual below. |
| **F7** (signature collision) | Open: C173 known gap | R2 shows it reaches further than documented, and `isUninferred` caused R1. |
| **F8** (state a dropped trial leaves) | Covered by the fallback where it leads to an error | Silent leftovers only add dead code: class instances, cached helpers, dynamic-type descriptors (`prepareDynamicTarget` never reports). |
| **F9** (docs) | In progress | D-190 is being written in the main tree. It should cover R1, R2, the dispatched-method trade-off and the F6 residual. |
| **F10** (tests) | Largely addressed | `generic_arrow_contexts` (a1/a4/a7/a8), `generic_arrow_suspensions` (a2), `generic_instances_fall_back`, `generic_instances_keep_compiling`, and the driver and lowering tests. The latter two lowering tests and the two driver tests pass (`go test -run`). |

**F6 details:**
- The scope snapshot cost is gone. Round 1's `scope_b` now takes 190 MB and 2.2 s, against the base's 149 MB and 1.5 s (round 1: 656 MB, 3.9 s).
- Arrows called only by name are fixed: `blow_150x50` is 11,457 lines against the base's 8,806 (round 1: 85,957).
- **Arrows that escape still pair with every demand.** `gen/esc_150x50.pts` (`tools/gen_escape.py`: 150 arrows pushed into an `any[]` plus one passed to `use`, 50 types):
  - 92,388 lines of C against the base's 8,784;
  - build 25.7 s against 2.2 s;
  - binary 9.8 MB against 4.3 MB.
- `esc_60x40` is 32,418 lines against 5,374. D-190's "Code grows with the pairs that are real" isn't accurate.

## Other findings

**N1 (medium, verified, documented): the fallback is all or nothing.**
- `p/c10_cliff.pts`: an unrelated F1-pattern line (`Weird` reached through `any`) makes `main`'s fixed arrow throw its old TypeError again.
- `p/c10b_cliff_alone.pts`, the same arrow alone: `fixed by H09a 6`.
- The D-190 draft does mention this. A narrower fallback (exclude only the failing erased values) would keep the feature robust in larger programs.

**N2 (low-medium, verified): the suite can't see failures that only `AllInstances` hits.**
- `TestRun` and `TestRunMatchesNode` build through `driver.Build` with the fallback (`pts/tests/run_test.go:283`).
- `TestCompilerFailsCleanly` uses `AllInstances` alone but flags only internal errors, not PTS1001.
- My sweep with a tracing overlay (`fb/emitc.txt`, 973 programs): today only `run/generic_instances_fall_back.pts` needs the lowering-level fallback among programs that compile. Nothing pins that.
- Suggestion: assert that `EmitCWith(AllInstances)` lowers every run and corpus program that `EmitC` lowers, apart from an allowlist.

**N3 (low, verified): `pts emit-c` doesn't fall back for clang failures, so it can print C that `pts build` doesn't use.**
- On `fuzz/spec2/ternary_len__array__local.pts`, emit-c prints 372 lines of `AllInstances` C, which clang rejects. The build falls back and compiles the base's 354.
- The cost of the fallback otherwise is small: 0.48 s → 0.80 s for that build. On a 42k-line program with an ordinary error, the second lowering adds about 0.1–0.4 s.

**S1 (low, suspicion, not reproduced): a foreign unexplained failure without a report can be lost.**
- In `tryInstance` (`instances.go:267`), the "stands" case requires `l.reports > reports`.
- If foreign code only noted an unexplained failure, the rollback at line 272 restores `l.unexplained`, so that failure goes unreported.
- I found no path that lowers foreign expressions during a trial. Worth a one-line fix: stand on `t.foreign` alone.

## What checked out (verified)

**Sweeps:**
- Round 1's 212 generated programs (`fuzz/sweep.txt`): 199 unchanged, 13 differ from the base and all 13 now match Node; none worse.
- C size over 973 programs (`csize/sizes.txt`): +0.23% in total. `transducers` is 10.1% smaller (4,620 lines). The only exit-code change is the intended `generic_instances_keep_compiling`.
- Emitted C is deterministic across runs.

**Closure sharing.** These match Node (or a hand-computed result where Node can't run them):
- `c02` nested function declarations across three instances of an outer generic function;
- `c03` `this` and `super` in constructors, fields, methods and getters;
- `c04` a named function expression recursing, with a closure capturing its name;
- `c05` loop variables, cells, generators and async;
- `c06` `calledOnlyName` edges: `?.()`, `!`, `typeof`, closure, element, parentheses, `.call`, shadowing, shorthand;
- `c07` names in scope: a `let` after the arrow, catch, for-of, blocks, a hoisted function, a nested generic arrow;
- `c08` generic class contexts;
- `c09` 200k calls with GC pressure and `.par.map`; stable under `PTS_THREADS` 1/2/4/8 with `GC_FREE_SPACE_DIVISOR=400`;
- `c11`–`c15`, the modules probe in `m/`, and `d01` (dynamic results with `this`, generators).
- Round 1's a1–a11 and the reproducer's probes behave as before or better. The `anyp/a04` lies still throw, and `generic_values_any_boundaries` passes.

**C70.**
- `p/r01_c70_more_edges.pts` matches Node: optional elements in the middle and left out at the end, variadic, self-containing, nested, a union target, the empty tuple, and a generic rest parameter through a value.
- Frozen tuples thaw when copied, and writes through the copy aren't seen by the original (the existing D-157/D-071 behaviour).
- D-190 or the SPEC should list that a tuple held in `any` now silently copies where it used to throw.

**Tests.**
- Every new test with an `// expect:` block passes on snap4.
- The tests that aren't `no-diff` match Node; the two tests with no expectations of their own match Node through `tri.sh`.
- The `no-diff` reasons are justified.

## Before it is ready
1. Fix R1 and add `u05` as a run test.
2. Fix or contain R2 and add `d06`.
3. Pin the f04 and t01 trade-off with a test, and say in D-190 that it covers dispatched methods.
4. Correct D-190's "none worse" and "pairs that are real" claims, and add the escaping-arrow blowup to the known gaps.
5. Optional: N2's assertion, and S1's one-line guard.

Archive: `/tmp/h09/review2/h09-review2-probes.tar.gz` (`INDEX.txt` inside); `/tmp/h09/review2/DONE` is touched.

## Round 3

---

# H09a review, round 3 (opus-reviewer, claude-opus-5-5, xhigh)

**Verdict: not ready, but close.** Keying the dynamic results is correct and fixes R2 and C173. The S1 change, the smoke test's fallback check and nearly all of the docs check out. **R1 is only partly fixed:** `uninferred` still drops instances that the base made and the call needs. I found three fully typed shapes that match Node on the base and throw on snap5. An overlay experiment shows that dropping the filter entirely fixes all three and costs nothing measurable, so the remaining fix is a deletion plus three test lines.

What I reviewed: `/tmp/h09/snap5`. Its `git diff HEAD -- pts` has sha256 `1d4c9adf…`, the same as the main tree; the CLI is `d33d4b4c…`. I compared it with the base and with Node v24 on .mts copies. Paths below are relative to `/tmp/h09/review3/`. "Verified" means I ran it.

## Open: R1′ (high, verified) — `uninferred` still skips needed instances

`uninferred` (`pts/lower/instances.go`) skips a pairing when the value's inferred type arguments include any or unknown and the call's own type arguments don't. But an `unknown` can be correct without appearing among the call's type arguments. All three cases below are typed results (no key), and the `DeclaredInstances` fallback applies the same filter, so it can't rescue them:

| Probe | What makes the unknown correct | Base and Node | snap5 |
|---|---|---|---|
| `p/u06_unknown_in_demand_type.pts` | The parameter's function type fixes it: `wrap(f: <T>(value: T) => Result<T, unknown>)` with `wrap(ok)`, where `ok<T, E>` | `42 42 2` | `Uncaught TypeError: Expected Result<number, unknown>, got Result<any, any>`, exit 1 |
| `p/u07_unknown_from_enclosing_instance.pts` | It comes from an enclosing instance: `outer<unknown>(pair)`, with `f: <T>(k: T) => Pair<T, U>` | `6 6` | `TypeError: Expected Pair<number, unknown>, got Pair<any, any>` |
| `p/u09_phantom_explicit_only.pts` | A type parameter the signature doesn't mention, called only with explicit arguments: `f<number, string>(1)`, `f<number, boolean>(2)` | `3 3` | `TypeError: Expected Box<number>, got Box<any>` |

**Experiment (verified; overlays only, the tree was not touched; `ov/instances_none.go`, `ov/instances_keyed.go`):**
- **With `uninferred` always false:**
  - These probes match Node: u04–u09, d02, d04–d06, k01–k03, round 1's f05, and the run tests `generic_dynamic_results_by_type`, `generic_instances_unknown_type_arguments`, `generic_factory_promises_generators` and `generic_arrow_suspensions`.
  - The one exception is d03, a deliberate lie (`n as any` read as a string). It throws a TypeError as the base does.
  - Every other `run/generic*.pts` test also matches Node, or snap5 where Node isn't compared (`out-none-generic-tests.txt`).
  - Over 1,091 programs (round 1's fuzz plus run, corpus and stress; `fb-compare.txt`), fallback use and exit codes are identical to snap5. The C is identical in size everywhere except `generic_dynamic_results_by_type` (+46 lines).
- **Why the filter is no longer needed:** it existed for F7's collision. With keys, an instance inferred as any is listed only under the key of the demand that made it, and it makes the same values the erased copy would.
- **Restricting the filter to keyed (dynamic) demands** also fixes u06, u07 and u09. It still misses k01's phantom-parameter line (`f<number, string>` on a `Promise<Box<T>>` result), which the base also failed on.

**Recommendation:** remove `uninferred`, and add the u06, u07 and u09 shapes to `run/generic_instances_unknown_type_arguments.pts`.

## Re-checking the round-2 responses

**R1 (u04, u05): fixed for those shapes (verified).** Base, snap5 and Node all print the same. The new test passes and matches Node; it fails on the base because of its local-arrow line.

**R2 and C173: fixed (verified).**
- d02, d04, d05, d06 and f05 match Node. d03 throws as the base does.
- Key probes `p/k01`–`k03` match Node, apart from k01's phantom line (see R1′, above):
  - an alias signature against a direct one;
  - `Promise<Box<T> | undefined>`;
  - instances of an enclosing generic function and class;
  - `Generator<Box<T>, void, unknown>`, `Promise<Map<string, Box<T>>>` and `Promise<[Box<T>, number]>`.
- `run/generic_dynamic_results_by_type.pts` passes and matches Node; the base exits 1.

**Is the key correct?** Yes. Each call's own demand creates its own entry, so a call always finds the instance made for it, or none and the erased copy.
- A shared key means the same checker type object (same id), so the same result type.
- Types that print alike but differ (aliases, unions, other modules) get other ids. That only adds entries; it never mixes instances up.
- Check names come from a counter, and `keyedFuncTypes` keeps each key's check a separate object.
- The emitted C is deterministic over three runs of k01, `keys_literal_200`, the dynamic-results test and the modules probe.
- One residual, which typed results already have on the base: type parameters that the result type doesn't show share an entry.

**Do keys multiply table entries? Yes: medium, verified, performance only (`gen/measure-keys.txt`).**
- The key uses checker identity, so each call site that passes a fresh object literal gets its own key.
- `keys_literal_200` (200 sites):
  - 200 table entries, each with a trampoline, and 205 checks;
  - 8,099 lines of C against 3,264 on the base; build 5.5 s against 2.8 s;
  - the same program with numbers has 1 entry.
- Combined with C172's escaping values, `keys_esc_50x200` (50 escaping async arrows, 200 sites) gives 10,200 entries: 32,515 lines, a 24.8 s build and a 10.4 MB binary. The same program with numbers is 7,816 lines and 2.8 s.
- Each call also scans its value's table linearly.
- The output matches Node, where the base threw. Suggestion: key by the IR type of the result's type arguments, which is canonical for structurally equal object types. Otherwise record this in C172.

**t01/f04 trade-off: pinned and stated (verified).**
- `generic_instances_that_dont_lower.pts` passes its expectations. Its `no-diff` reason is justified, and the base gives PTS1001 at lines 58 and 69.
- D-190 states the trade-off for dispatched methods.

**N2, the smoke test's fallback check: works both ways (verified).**
- With `PTS_SMOKE_MUTANTS=0` and `PTS_SMOKE_FILTER=generic` it passes.
- With an overlay that moves `needsFallback` to `generic_instances_keep_compiling`, both failure kinds fire with the right messages: "compiles only with the driver's fallback (D-190)" and "compiles without the fallback: remove it from needsFallback".
- Limit (low): it covers only lowering-level fallbacks. A seed whose `AllInstances` C clang rejects is caught only with `PTS_SMOKE_CLANG=1`, which CI doesn't set.

**S1:** `case t.foreign` alone. The sweep classification is unchanged.

**F6 residual, N1 and N3: recorded as C172 and C176.**
- C172's claim that escaping generic declarations already blew up before H09 is true. My `gen/esc_decl_60x40.pts` gives 32,015 lines on the base and 32,419 on snap5.
- `c10_cliff` behaves as before; it is documented.

## Regression sweep (verified)
- **Round 1's 212 fuzz programs:** 162 match on all three, 37 unchanged (base and snap5 differ from Node alike), 13 improved, 0 worse. This is the same classification as round 2.
- **Round-1 probes, my round-2 probes and the reproducer's probes:** the same classification as round 2.
  - f05 now matches Node.
  - The `.par` and modules probes give the same values as before.
  - The `anyp/a04` lies still throw.
- **C size:** run, corpus and stress grow +0.257% over the base. Only three new tests changed between snap4 and snap5.

## Docs
- **D-190 (Which pairs) and h09.md ("R1 (high): fixed"):** inaccurate. u06, u07 and u09 aren't covered, and with keys the stated reason for `uninferred` (an unrelated value, or a T only in a dynamic result) no longer needs the filter.
- **The rest checked out:** D-190 on keys, the fallback, C70 and the known gaps; SPEC §3.7 and §9.129; the ARCHITECTURE driver line; HARDENING_PLAN H09a/H09b; CONSOLIDATED C09, C70, C164, C169–C176. They match the code and my runs.
- **Missing:** key growth for fresh anonymous result types, unless the key changes.
- **h09.md:** still has its TBD placeholders, as expected.

## Before it is ready
1. Remove `uninferred` (or fix it) and add u06, u07 and u09 to the unknown-type-arguments test.
2. Correct D-190 and h09.md on R1.
3. Either canonicalize keys or record per-call-site key growth in C172.

Archive: `/tmp/h09/review3/h09-review3-probes.tar.gz` (`INDEX.txt` inside); `/tmp/h09/review3/DONE` is touched. I made no edits to the main tree, the snapshots or the base; the experiments were overlays only. The cleanup of `/tmp/h09/review3/run/` was refused, so it still holds stray temporary files; they aren't in the archive.

## Round 4

---

# H09a review, round 4 (opus-reviewer, claude-opus-5-5, xhigh)

**Verdict: not ready.** Removing `uninferred` is correct: it fixes R1′ and costs nothing measurable. But the new `instanceKey` gives the same key to calls that need different instances. That breaks code that snap5 ran correctly, and on some lines code that the base ran. The fix is small; an overlay that fixes it changes no C in 1,091 programs. Everything else checks out.

I reviewed `/tmp/h09/snap6`, after checking it byte for byte: `git diff HEAD -- pts` has sha256 `18ed99d2…`, the same as the main tree, the untracked tests are identical, and the CLI is `01a68785…`. I compared it with snap5, the base and Node v24 on `.mts` copies. "Verified" means I ran it. Paths are under `/tmp/h09/review4/`. I edited no tree; the experiments are `go build -overlay` only.

## F1 (high, verified): the key ignores type arguments that come from outside the call's own signature

`instanceKey` builds its identity from `resolved.Declaration()` plus the call's own type arguments. Two kinds of type argument never reach it:
- a type alias's arguments (`Loader<number>` and `Loader<string>` share one function-type node);
- an enclosing generic function's or class's instance arguments.

So calls through `<T>(k: T) => Promise<Pair<T, U>>` with the same T get one key, whatever U is. The first call to be lowered creates the table entry (`specializeValue` returns early when the entry exists), and the second call reuses that instance. The trigger is a value that is generic in a type parameter appearing only in its result, for example `async <K, V>(k: K) => new Pair<K, V>(k, [])`.

| Probe | Base | snap5 | snap6 | Node |
|---|---|---|---|---|
| `p/k04` (enclosing function) | both lines TypeError | `2 0` / `2 0` | line 2: `rejected Expected Pair<number, string>, got Pair<number, number>` | `2 0` / `2 0` |
| `p/k05` (type alias) | TypeError | `1` / `1` | `Uncaught TypeError … got Pair<number, number>` | `1` / `1` |
| `p/k06` (enclosing class) | TypeError | = Node | line 2 rejected | |
| `p/k07` (alias, `f<any>`) | **`any: 1 0`** | = Node | **`any: rejected Expected Pair<any, any>, got Pair<any, number>`** | `any: 1 0` |
| `p/k08` (enclosing, `f<any>`) | **`any: 1 0`** | = Node | **`any: rejected …`** | `any: 1 0` |

- k04–k06 are regressions against snap5. The base also failed them, with C09's TypeError.
- k07 and k08 are regressions against the base on their `any` lines. That is the same symptom as round 2's R2.
- Every failure is a checked TypeError, not a memory error.
- The claim "the same IR types … name the same instance (as `genericInstance`'s do)" is therefore false. `genericInstance` dedupes by the *value's* inferred type arguments, and those depend on the call's whole instantiated signature.

**Fix, verified by overlay** (`ov/instances_keyfix2.go`): build the identity from the call's type arguments *and* the instantiated result type, through a canonical walk. The walk:
- recurses into library references and generic-class references by their target and type arguments, so it makes no class instance;
- recurses into unions by their members;
- uses the literal's own type for literals;
- uses `irType`, with the take-back, for everything else.

Results with that overlay:
- k01–k08 and both changed run tests match Node.
- `keys_literal_200` stays at 3,323 lines and `keys_esc_50x200` at 7,839.
- The C is byte-identical to snap6 on all 1,091 programs (`out/keyfix2-ccmp.txt`).
- `go test ./pts/lower` passes.
- With the result in the key, the declaration could perhaps be dropped. That would merge entries such as `later(2)` and `pass`'s `f(1)`, which are now separate (see Other).

Please add a k05/k07-style block to `run/generic_dynamic_results_by_type.pts`; the suite misses F1 today.

## F2 (medium, verified; in the slice since snap4, not introduced by this delta): `genericInstance` merges type arguments that share an IR type

T = `Promise<number>` and T = `any` both lower to `value`, so they get one instance, lowered for whichever came first. For a typed result, `fits` catches the mismatch; for a dynamic result nothing does.

- **`p/g01`** (`Promise<Box<Awaited<T>>>`):
  - Base prints `any: 2`.
  - snap4, snap5 and snap6 print `any: rejected Expected Box<any>, got Box<number>`.
  - This is a regression against the base on that line, and it contradicts D-190's "an instance for `any` makes what the erased copy makes".
- **`p/g02`**: `string: rejected Expected number, got string`. The base fails here too.
- **`p/g03`**: the same merge on direct calls gives PTS1001 on the base, so the limit itself predates H09.

An overlay that treats `value`-typed arguments by their checker type, both in the key and in `genericInstance` (`ov/*_keyfix3.go`), fixes g01 and g02. It changes C in 4 of 1,091 programs (`lazy_thunk_streams`, `fetch_abort`, `generic_dynamic_results_by_type`, `recursive_unions_generic_consts`), and their outputs stay the same. Because it also affects typed calls and the 64-instance limit, either adopt it limited to keyed demands, or record it as a gap with g01 as a skipped test.

## F3 (low, verified): `instanceKey`'s `irType` makes class instances that stay

Calling `irType` on a type argument that is a generic class creates that class instance, and it isn't taken back. In `p2/s03`, a call `f<Holder<string>>()` that is never awaited creates `Holder<string>`, which doesn't lower. The build falls back and an unrelated C09 line throws. snap5 printed what Node prints. The probe is contrived, and experiment 2's walk avoids it.

## F4 (low, verified): a taken-back failure leaves a cached nil

When `irType` fails inside `instanceKey`, `interfaceOf` caches `nil` and the report is taken back. A later use of the same type then fails without a report. In `p2/s08` the first lowering says "identifier (which the compiler failed to lower without saying why)", and the fallback then prints the base's error. Only `EmitCWith` would show the vague message.

## Verified as claimed

- **R1′ is fixed.** Each new block of `generic_instances_unknown_type_arguments.pts`, run alone, fails on snap5 and passes on snap6; the base matches Node. u06, u07 and u09 match Node; the other u, d and k probes behave as on snap5.
- **Removing `uninferred` changes nothing else:**
  - 1,091 programs: fallback use and exit codes are identical to snap5. C differs only in the two edited tests and in `generic_arrow_suspensions` (+26 lines) and `generic_factory_promises_generators` (+64 lines); those extra entries come from per-declaration keys, and both tests match Node.
  - The 212-program sweep: snap5 and snap6 give the same output for every program (162 match Node on all trees, 37 unchanged, 13 improved, 0 worse).
  - Both reproducer matrices (754 + 432): C byte-identical to snap5.
  - The generator programs from rounds 1–3: C identical apart from the keys ones.
  - Every earlier probe (rounds 1–3 and the reproducer's): snap5 = snap6, except u06, u07, u09 and k01, which now match Node.
- **Key growth figures are confirmed:** 3,323, 7,839 and 7,816 lines.
- **The C is deterministic:** three runs of 7 programs gave identical output.
- **The `%p` identities are sound:** every `ir.Type` is a pointer, and `genericInstance` compares the same pointers.
- **The lowering tests pass,** including the new key test.

## Docs

- Inaccurate given F1 and F2:
  - D-190's "Dynamic results" ("whatever object literals or aliases spell them", "as `genericInstance` tells instances apart");
  - D-190's "Which pairs" ("an instance for `any` makes what the erased copy makes");
  - the `instanceKey` doc comment;
  - h09.md's Keys line;
  - C173's "Each call finds the instance of its own type arguments".
- Wrong cause: D-190, h09.md and C173 say the phantom `f<number, string>`/`f<number, boolean>` calls failed on snap5 because they shared an entry. Round 3's no-filter overlay passed k01 with snap5's keys, so the cause was `uninferred`.
- Stale test comment: the header of `generic_dynamic_results_by_type.pts` still says "told apart by the result's type". It also says all calls have the signature `(number) => value`, which the literals block's calls don't.
- Correct: the rest of the delta (SPEC §9.129, the ir and demand comments, C176's clang note, h09.md's round-3 section, and the round-3 report saved into `h09-review.md`, which matches the recovered copy).

## Other (cost only, verified)

The same instance reached through two function-type declarations gets two entries: `later(2)` called directly, and `pass`'s `f(1)`.

## Before it is ready

1. Fix F1 (experiment 2 is one validated way) and add a k05/k07-style test block.
2. Fix F2 for keyed demands, or record it with g01 as a skipped test.
3. Correct the docs listed above.

Archive: `/tmp/h09/review4/h09-review4-probes.tar.gz` (sha256 `a10b7847…`, `INDEX.txt` inside; overlay binaries not included). `/tmp/h09/review4/DONE` is touched.

## Round 5

---

# H09a review, round 5 (opus-reviewer, claude-opus-5-5, xhigh)

**Verdict: not ready to commit as it stands, but close.** Every round-4 response checks out: k04–k08, g01, g02, s03 and p01 match Node, and so do each of the parent's red4 blocks. Nothing regressed in the sweeps or the matrices. Three narrower cases of the same two failure modes remain:
- **R5-1 and R5-2** each make one line throw that the base ran.
- **R5-3** is a trade-off of the cap: worse than snap6, no worse than the base.

One small overlay fixes R5-1 and R5-2 at almost no cost (details below). Either apply that approach, or record both as known gaps with RED tests; after either, the slice is ready.

What I reviewed: `/tmp/h09/snap7`. Its `git diff HEAD -- pts` has sha256 `c61e9a2e…`, the same as the main tree, and the CLI is `aefd0ec9…`. I compared it with snap6, the base and Node v24 on `.mts` copies. "Verified" means I ran it. Paths are under `/tmp/h09/review5/`. I edited no tree; experiments are overlays only.

## R5-1 (medium, verified): the key still merges calls through an intersection-typed parameter

`p/q04` is the parent's p01 with `Tagged<U> = Promise<U> & { label?: string }` as the parameter type. `keyPart` doesn't walk intersections, so it falls through to `irType`, which gives `value` whatever U is.
- The IR dump (`out/q04*.ir`) shows both calls with key `Promise<Box<any>>#1`, and only one instance, `feed_number_value`.
- Base and Node print both lines; snap6 and snap7 print `string rejected Expected number, got string`.
- So "anything else by its IR type, the same object for the same types" (D-190, the `keyPart` comment) doesn't hold when the IR type is `value`.

The same merge happens for an object-type parameter `{ p: Promise<U> }` (`p/q02`, one key in `out/q02*.ir`). It can't show at run time today, because the value goes through an adapter (C169). Function-type parameters (`q01`) are hidden the same way.

## R5-2 (medium, verified): `madeFor` only compares the top level of each type argument

`madeFor` compares checker types only where the IR type argument is `ir.Value` itself. A dynamic value inside an array or an object type isn't compared.
- **`p/q10`**: `first<T extends unknown[]>(xs: T): Promise<Box<Awaited<T[number]>>>`, called with T = `Promise<number>[]` and with `any[]`. Both are an array of `value`, so `f<any[]>` gets the `Promise<number>[]` instance. The base prints `any: 2`; snap7 prints `any: rejected Expected Box<any>, got Box<number>`.
- **`p/q03`** shows the same with T = `{ p: Promise<number> }` against `{ p: any }`.
- These are g01's failure, one level deeper. C177's wording ("dynamic results tell them apart since H09a") needs the same caveat.

## Experiment for R5-1 and R5-2 (verified, `ov/fix5`)

The overlay makes four changes:
- `keyPart` walks intersections by their members.
- `keyPart` walks type-literal and object-literal types by their members and call signatures.
- `keyPart` writes a leaf whose IR type is `value` by its checker type.
- `madeFor` compares the `keyPart` identities of every one of the instance's own type arguments.

Results:
- q03, q04 and q10 match Node, as do k01–k08, g01, g02, s03, p01, `many_value_keys_70` and both dynamic-results tests.
- The C is identical to snap7 on 1,091 of the 1,092 programs and on all 1,186 matrix cases. The one exception is `generic_dynamic_results_by_type` (+156 lines), which still matches its expected output.
- `keys_literal_200` stays at 3,323 lines and `many_value_keys_70` at 3,415.
- `go test ./pts/lower` passes.

## R5-3 (low, verified): past the cap, a call that sharing served correctly now throws

In `p/q08`, six calls pass `Promise<1>` through `Promise<6>` to `<T>(x: T) => Promise<Box<Awaited<T>>>`.
- snap6 printed what Node prints, because one shared instance making `Box<number>` fit every call.
- snap7 is right for calls 1–4. Calls 5 and 6 keep the erased copy and throw `Expected Box<1>, got Box<any>`.
- The base throws on all six.

D-190 states "past them the call keeps the erased copy, as the base's did", so this is a stated trade-off. Please add q08 as its example in C172. The cap's `nil` leaves the trial clean: nothing has been made yet, `tryInstance` rolls back nothing, and dynamic instances are only ever requested from `tryInstance` itself.

## Your focus questions (verified)

- **`this` types:** `this` parameters are PTS1001 everywhere (`q09`), so there is no hole there. A `this` type inside the key goes through `irType` to its class.
- **Optional parameters (`q06`):** fixed; the union with undefined is walked.
- **Dispatched generic methods (`q07`, Awaited with `any`):** fixed on snap7; snap6 failed it.
- **Instances of enclosing classes (k06 and the test's block):** fixed.
- **Overloads:** a function type with more than one call signature is never a demand, so overloads don't reach the key.

## Regression sweep (verified)

- **Fallback compare:** over 1,092 programs, fallback use and exit codes are identical to snap6. C differs only in the two dynamic-results tests.
- **The 212-program sweep:** identical to snap6 on every program (162 match Node on all trees, 37 unchanged, 13 improved, 0 worse).
- **Both matrices (1,186 cases):** C identical to snap6.
- **Generator programs:** C identical apart from `many_value_keys_70` (1,699 lines on snap6, 3,415 on snap7).
- **Every earlier probe** (rounds 1–3 and the reproducer's): snap6 = snap7. Round-4 probes are all fixed.
- **Determinism:** the C is deterministic over three runs of six programs.

## Docs

The delta is accurate apart from these three points:
- D-190, the `keyPart` comment and C173 claim that parameters "whose IR type is a dynamic value" are covered; R5-1 shows they aren't when the type is an intersection.
- D-190, C173 and C177, and the new test's header ("never gets an instance made for another such type argument"), hold only at the top level of each type argument (R5-2).
- C172 should give q08 as the example of what happens past four variants.

Archive: `/tmp/h09/review5/h09-review5-probes.tar.gz` (sha256 `5fbc2197…`, `INDEX.txt` inside; overlay binaries not included). `/tmp/h09/review5/DONE` is touched.

## Round 6

---

# H09a review, round 6 (opus-reviewer, claude-opus-5-5, xhigh)

**Verdict: not ready to commit as it stands.** Every round-5 response checks out, and nothing regressed in the sweeps or the matrices. But snap8's structural walk is coarser than the IR in two ways, and its cut-offs split calls that should share an instance:
- **R6-1:** two lines that snap6 and snap7 ran correctly now throw, and one of them the base ran too.
- **R6-2:** two programs that snap6 and snap7 ran correctly now throw from the fifth call on.

A three-edit overlay (`fix6b`) fixes all five probes and changes nothing else. With it, plus test blocks for these probes, the slice is ready.

What I reviewed: `/tmp/h09/snap8`. Its `git diff HEAD -- pts` has sha256 `674dcaf7…`, the same as the main tree, and the CLI is `3c039ef6…`. I compared it with snap7, snap6, the base and Node v24 on `.mts` copies. "Verified" means I ran it. Paths are under `/tmp/h09/review6/`. I edited no tree; experiments are overlays only.

## R6-1 (medium-high, verified): `objectKeyParts` writes types of different IR types the same way

The identities below come from a debug overlay that prints each key (`out/key-identities.txt`).

- **`p/r01`: a method against a function-valued property.** `{ m(): number }` and `{ m: () => number }` both come out as `{m,{,0(),<number>}}`. Their IR types differ: the IR dump shows an interface method in one and a field of function type in the other. When both calls go through one function-type declaration, they share an entry.
  - snap8 prints `function rejected Expected {m(): number}, got Object`.
  - snap6, snap7 and Node print `function 2`.
- **`p/r03` and `p/r03b`: the prelude's `Timeout` against a structural twin.** `isLibInterface` is false for the prelude (`ptslib:///prelude.d.ts`), so `Timeout`, whose IR type is a dynamic value, is walked by its members exactly like a user `interface MyTimer` with the same members, whose IR type is a shape. The two calls get one key.
  - In r03, the MyTimer line throws.
  - In r03b, MyTimer's call is lowered first, and the Timeout line throws: `timeout rejected Expected {hasRef(): boolean; …}, got Timeout`. **The base runs that line** (it prints `timeout boolean`).

## R6-2 (medium, verified): the walk splits call sites that should share an instance, and past four variants the cap leaves later calls on the erased copy

- **`p/r05`: generic signature type parameters.** Six sites call `wrap(<X>(x: X) => x)`. Each site's `X` is a fresh type parameter written by its checker id, so `madeFor` treats every site as a new variant. Calls 5 and 6 keep the erased copy and throw `Expected Box<<X>(x: X) => X>, got Box<any>`.
- **`p/r08`: the depth and budget cut-offs.** Six sites pass an 8-level nested object literal. Past `maxKeyDepth`, each site's fresh literal types are written by checker id (`t<id>`). That is not "only a cost": through `madeFor` and the cap, calls 5 and 6 throw.

snap6, snap7 and Node print all six values in both programs; the base throws on all six.

## The fix, verified by overlay (`ov/fix6b/instances.go`)

The overlay makes three edits to `instances.go`:
1. For an object type that `objectKeyParts` walks, write `irTypeKeyPart` first. This makes the key never coarser than the IR.
2. Write a signature's own type parameters by position (`$depth.index`).
3. At `maxKeyDepth` or `maxKeyParts`, write `irTypeKeyPart` instead of the checker id.

Results:
- r01, r03, r03b, r05 and r08 match Node.
- The C is byte-identical to snap8 on all 1,093 programs, 1,186 matrix cases, 19 generator programs and every earlier probe; my five probes are the only ones whose C changes.
- emit-c time rises about 1%: 209.2 s to 210.9 s over the 1,093 programs.
- `go test ./pts/lower` passes.

Writing the constraints of signature type parameters would close the remaining gap there, though I know of no case that needs it.

## Your focus questions

Verified by running:
- **Getters and setters:** a getter's IR type is the same as a field's (the IR dump shows both as `{p: number}`), so merging them is fine.
- **Methods:** this is R6-1.
- **Generic signatures:** this is R6-2 (`r05`).
- **Namespaces:** PTS1002 everywhere. Module namespace values are PTS1001 everywhere. Neither is a hole.

By reading the code, not run:
- **Readonly properties:** IR types are the same, so merging is fine. Readonly arrays and tuples have their own targets, so they get finer keys: a cost only.
- **Tuples:** they are references, and their target carries the readonly flag; fine.
- **Enums:** their members are literal types written by id, so two enums never merge.
- **`typeof C`:** its `prototype` property is a reference to the class, so two classes never merge.
- **`this` inside interfaces:** an interface that mentions `this` is a reference, keyed by its target.

`isNominal`:
- The pts:* modules are compiled as program sources. Their classes are in `l.classes`, and their interfaces are walked structurally, which is right because PTS lowers them structurally.
- The prelude is the problem case (R6-1).
- Lib interfaces with structural IR types are keyed by their IR pointer. A user twin gets another key (a cost only). I know of no two lib interfaces that share an IR shape but differ in their dynamic members.

## Regression sweep and cost (verified)

- **Fallback compare:** over 1,093 programs, fallback use and exit codes are identical to snap7. C differs only in `generic_dynamic_results_by_type` (+156 lines) and the new test. Total emit-c time is 167.8 s against 168.7 s.
- **The 212-program sweep:** identical to snap7 on every program (162 match Node on all trees, 37 unchanged, 13 improved, 0 worse).
- **Matrices and generator programs:** C identical to snap7.
- **123 earlier probes:** snap8 = snap7 apart from five improvements (q03, q04, q10, p02, p03). q08 is still the stated cap trade-off.
- **New test:** each of the six blocks in `s3/red5/` fails alone on snap7 and passes on snap8.
- **Determinism:** the C is deterministic over three runs of five programs.
- **`wide_types_keys`:** snap8 is no slower than snap7, about 8.4 s against 8.3 s on a loaded host. But the base takes 2.1–3.1 s, so H09a makes this program roughly 3–4 times slower to compile. That was already so on snap5, before `keyPart` existed, so it comes from the pairing, not the walk. h09.md's "3.2 s, same as snap7" doesn't mention the base.

## Docs

- D-190 and `keyPart`'s comment say it writes the same thing "for types of the same IR types … but for dynamic values". The converse fails too: different IR types are written the same (R6-1).
- "Checker ids from there" at the cut-offs leads to R6-2.
- The rest of the delta is accurate: SPEC §9.129, C172's q08 example, C173, C177's caveat, the outer-type-arguments test's header and h09.md's round-5 section.

## Before it is ready

1. Apply fix6b's three edits (or something equivalent).
2. Add r01, r03b, r05 and r08 as test blocks.
3. Correct the `keyPart` sentence in D-190 and the code comment.

Archive: `/tmp/h09/review6/h09-review6-probes.tar.gz` (sha256 `9998c4e8…`, `INDEX.txt` inside; overlay binaries not included). `/tmp/h09/review6/DONE` is touched.

## Round 7

---

# H09a review, round 7 (opus-reviewer, claude-opus-5-5, xhigh)

**Verdict: ready.** Everything the parent asked me to verify checks out, and I found nothing new of any severity.

What I reviewed: `/tmp/h09/snap9`. Its `git diff HEAD -- pts` has sha256 `f333e191…`, the same as the main tree, and the CLI is `f192de25…`. I compared it with snap8, round 6's overlay `fix6b`, snap6, snap5, the base and Node v24. "Verified" means I ran it. Paths are under `/tmp/h09/review7/`. I edited no tree.

## The code is my fix6b in effect (verified)

- Diffing `pts/lower/instances.go` against my overlay shows two real differences besides comments, and neither changes what is written:
  - **Moved `irTypeKeyPart` call:** it is now the first line of `objectKeyParts` rather than just before its call in `keyPart`. `objectKeyParts` has only that one caller, and the identity string comes out the same.
  - **Order of the two early checks:** the binders check now runs after the path check instead of before it. A binder returns before anything is pushed onto the path, so the path check can never match one.
- **C:** snap9's C is byte-identical to fix6b's on all 1,094 programs, the new test included (1,056 identical C files; 38 programs fail with the same errors on both).

## No regression or new fallback use (verified)

- **Fallback compare:** over 1,094 programs, fallback use and exit codes are identical to snap8. C is identical except for the new test. Total emit-c time is 208.5 s against 212.3 s on a busy host, which is within noise.
- **Matrices (1,186) and 20 generator programs:** C identical to snap8.
- **The 212-program sweep:** the 198 programs that compile have identical C to snap8, and the other 14 fail with the same exit codes. Round 6's classification therefore still holds: 162 match Node on all trees, 37 unchanged, 13 improved, 0 worse.
- **139 earlier probes:** C is identical to snap8 except for round 6's five probes and the four `s3/red6` blocks. All nine match Node on snap9 and fail on snap8; snap6 matches Node on all of them. r03b's Timeout line, which the base runs, matches Node.
- **New test:** `generic_dynamic_results_ir_identity.pts` matches Node, and each of its blocks fails alone on snap8 and passes on snap9.
- **Other checks:** `go test ./pts/lower` passes, and the C is deterministic over three runs.

## C178 (verified)

My numbers were measured with the host's load around 6–7, so my times run higher than the parent's. Memory and the ratios agree with the C178 row.

| Program | Base | snap9 | Notes |
|---|---|---|---|
| `wide_types_direct` | 11.9 s, 2.5 GB | 9.9 s, 2.5 GB | The base pays the same cost |
| `wide_types_keys` | 2.7–2.8 s, 0.18–0.19 GB | 5.6–6.2 s, 0.81–1.0 GB | snap5, which has no `keyPart`, is already 5.5 s and 0.8 GB |

- **It grows exponentially with interface depth.** On the base, the direct form takes 0.48 s and 67 MB at 4 levels, 1.7 s and 310 MB at 5, and about 10 s and 2.5 GB at 6.
- **The profile supports the parent's attribution.** In `/tmp/h09/s3/prof/cpu.out`:
  - `ir.(*Interface).String` takes 33.5% cumulative, almost all of it inside `fieldList` and `Union.String`.
  - Its callers are cgen's `checkDefinitions` (35.6% cumulative) and lower's `typeArgumentsName` (16.4%).
  - `keyPart` takes only 2.3%.
- So the cost is the IR printer's, and H09a now reaches it through values. Recording it under H23 is the right home.

## Docs (verified)

The following match the code and my runs:
- D-190's `keyPart` sentence: "never the same for other IR types", and past six levels or 256 types a type is written by its IR type alone;
- the C178 gap and the H23 note;
- SPEC §9.129's test list;
- C173's round-6 entry and the new C178 row;
- PROGRESS's known issues;
- h09.md's round-6 section.

## Low-severity notes (no change needed)

- **Stated trade-off:** a dynamic value deeper than six levels or 256 types isn't told apart. D-190 and the code comment both say so.
- **Timing:** h09.md's absolute times for C178 depend on host load. The ratios and the memory figures are the robust part.

Archive: `/tmp/h09/review7/h09-review7-probes.tar.gz` (sha256 `34e7ad9b…`, `INDEX.txt` inside). `/tmp/h09/review7/DONE` is touched.
