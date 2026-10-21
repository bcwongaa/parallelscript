# H09b5b reproducer report

The `reproducer` subagent's final message (claude-opus-5-5, effort xhigh, 2026-10-21), saved verbatim by the parent. It ran in its own worktree of `042cd9610` (`.claude/worktrees/agent-af8c2d6cf3dc1ad6a`; it changed only `pts/tests/compiler_smoke_test.go` and added test files, nothing committed), in parallel with the parent's fix (`3dde86f42`), which it didn't see. Its probes, generators, matrix (m9), runner, RED test copies and logs are in [h09b5b-reproducer-probes.tar.gz](h09b5b-reproducer-probes.tar.gz) (sha256 `d31cf76b635d98da…`, 2,152 entries; `INDEX.txt`). The parent's checks of its claims are in [h09b5b.md](h09b5b.md).

---

I've reproduced the current state on HEAD, found new triggers, and written the RED tests, guards and the matrix. Nothing is committed and no tracked file changed except `pts/tests/compiler_smoke_test.go`. Everything I list as ran, I ran myself.

## 1. Summary

| Item | Result on HEAD |
|---|---|
| Old c176/ probes (15) | 7 match Node, 8 differ. c176_limit and the m6 limit cases are fixed (by H09b5a). Class body, relaxed cast and call site (`c176_callsite`, `r02`, `a05`) still fall back. x01 and x03 fail at every level, so they are not C176. |
| m6 (25) | 16 OK, 9 TypeError. classbody ×5 and relaxed ×5 go to Declared, limit ×10 and none ×5 stay at All. The decl victim survives, but classbody__decl still throws on the trigger's own line. |
| Committed skipped tests, unskipped | All 3 run tests and both driver tests fail for their stated reasons. |
| Pool scan (1,824: the old 1,712 + 112 new from H09b5a's archives) | 1,515 All, 4 Value, 4 Declared, 299 fail at every level, 2 time out at 90 s. No clang rejection of All's C, no PTS9001. Versus the H09b5 scan, 7 programs improved and none got worse. |
| Smoke tests (`PTS_SMOKE_CLANG=1`) | Generic seeds with 6 mutants each: 1,031 programs, pass. All seeds with 2 mutants each: 3,398 programs (2,009 to C), pass. No internal error, panic, hang or clang rejection that only AllInstances hits. |
| New triggers | 13 more mechanisms (section 3). |
| Matrix m9 (291 cases) | 58 OK, 233 TypeError, nothing else. 194 victim lines lost, 79 kept, each exactly as the build level predicts. emit-c and build levels never differ naturally. |
| RED tests | 12 new run tests and 3 driver tests fail on HEAD. 6 errors guards and 2 driver guards pass. |

## 2. Why each trigger falls back (file:line on HEAD)

**The driver.**
- `driver.go:31` (EmitC) and `:57` (Build) loop over the fallback levels for the whole program.
- `fallsBack` (`driver.go:94`) falls back on any compile or internal error.
- In Build, a clang or linker failure becomes an internal error through `generatedCFailure` (`:139`, `:160`) and also falls back. EmitC never runs clang, which is where emit-c and build diverge.

**Class bodies** (first All diagnostic `…:19:14 PTS1001 using a Dog[] as a Animal[] where it may be changed or kept`):
- The report comes from `arrays.go:1190` (reportKeptCopy), via `lower.go:1043` (convert) and `lower.go:1377` (returnStatement), inside `Weird<Dog[]>.animals`.
- That body is lowered by `classInstanceBody` (`generic_classes.go:256`) from the main loop at `lower.go:247`, where no trial is active, so the report stands.
- `classInstanceOf` queues the body at `generic_classes.go:198`. It records only `l.subst` (`made`), not the trial that made it.
- `tryInstance` (`instances.go:872–915`) takes back instances, functions, demands, values, captures and counts, but never class instances or their bodies. This holds for trials that were taken back and for trials that were kept.

**Relaxed casts:**
- Casts are recorded at `objects.go:974` and `arrays.go:1150` with `in: l.function`.
- They are checked after lowering (`lower.go:258`, `optional.go:194–210`), which skips only dropped functions (`optional.go:200`), so a cast in a kept instance stands.
- `interfaceCasts` (`optional.go:233`) collects sources from every lowered function, kept trial instances and class instance bodies included. That is how the program's own cast can fail (t08, t14).

**Call sites:**
- **By name:** the `converting boolean to given` report (`lower.go:1049`) comes from `keyedInstanceCall`'s conversions at `instances.go:474–475`, reached through `namedInstanceCall` (`:416`) from `objects.go:1141` or through `keyedCall` (`element_calls.go:263`). It is lowered in the program's own function, not tentatively.
- **Value calls (new):** `instanceValueCall` (`:368`) → `instanceCall` (`:332`) → `erasedArguments` (`:360`). This path exists at Value too, so the program goes to Declared. The union-key element call takes this path at Value.
- **a05:** the call itself lowers. The failure is in using its result: the variable initializer's convert (`lower.go:816` → `:1024` → `unions.go:282`).

**Total (D-198):** `reportInstanceLimit` (`generics.go:96–98`) sets `foreign` past the total, so `tryInstance` returns with the reports kept (`instances.go:888`). The report comes from `classInstanceOf` (`generic_classes.go:176`).

**Wrappers:** forwarders and adapters exist at All only (`forwarding.go:43`, `:252`). Their take-backs (`forwarding.go:82`, `:336`) cover the wrapper's own lowering, not the class instances its forwarded demands' trials make.

## 3. Triggers and the narrowest unit to drop

All probes are in `probes/`.

| Probe | Mechanism | Falls to | Narrowest unit |
|---|---|---|---|
| m6 classbody, t12 | class instance from a trial that was taken back or kept | Declared | the pairing (demand × value) |
| relaxed | relaxed cast in a kept instance | Declared | the pairing |
| t01 / t11 | total met in an All-only trial (interface classes / literals) | Value | the pairings past the total; or the call site, lowered as Value lowers it |
| t02 | forwarder (method value) | Value | the pairing demand × forwarder |
| t16 | functionAsMethod adapter | Value | the pairing by-name demand × adapter |
| t03 | literal method by name | Value | the pairing |
| twin (p14-like) | structurally equal class | Value | the pairing |
| t06 | instantiating adapter | Value | the pairing adapter demand × w |
| t09 | dynamic-result demand | Declared | the pairing |
| t07 | chain through a class instance body | Declared | the leaf pairing (count's demand × w), or the root (useDog × h) |
| t08 / t14 | program's own cast, source from a kept instance / a trial's class instance | Declared | the pairing that contributed the source |
| c176_callsite, t04 | by-name / constant-key call site | Value | the call site, lowered as Value does |
| t05 | union-key call site | Declared | the call site, lowered as Declared does |
| t13 (new) | value call site | Declared | the call site, lowered as Declared does |
| a05 | `?.` result conversion | Value | the call site (the failure is in its use) |
| t10 | several independent failures | Declared | all three units |

- **PTS9001 only at All:** none found.
- **Granularity:** in t12 the failing value w also has a real, working instance for number. Dropping the whole value loses it; only the pairing should go.
- **Chains:** t07's first design reported 128 failures in one lowering, so a fix should collect every failing unit per lowering rather than re-lowering once per unit.
- **bind:** confounded. `w.bind` makes w itself an erased value, so the trigger reduces to classbody.

## 4. Must not change (fail at every level today; guards pass on HEAD)

All under `pts/tests/errors/`:
- `generic_fallback_keeps_dispatched_failures.pts`: dispatched demand × the override it reaches.
- `generic_fallback_keeps_declaration_failures.pts`: value demand × declaration.
- `generic_fallback_keeps_speculative_declaration_failures.pts`: the same, speculative.
- `generic_fallback_keeps_dispatched_totals.pts`: total in a dispatched demand's trial.
- `generic_fallback_keeps_declaration_relaxed_casts.pts`
- `generic_fallback_keeps_speculative_override_failures.pts`

The existing `errors/generic_polymorphic_recursion_{kept_classes,failed_trial_work,past_the_total}.pts` are guards of the same kind. Rule for the fix: drop a unit only if DeclaredInstances doesn't make it.

## 5. RED tests and guards

The 12 new run tests are under `pts/tests/run/` with Node's expect blocks and no `// skip:` line. Each fails TestRun and TestRunMatchesNode on HEAD; the TypeErrors are all of the form `Expected Box<…>, got Box<any…>`:

| Test (`generic_fallback_spares_…`) | Falls to | Lines that throw on HEAD |
|---|---|---|
| `unrelated_total` | Value | unrelated interface call |
| `unrelated_forwarder` | Declared | victim, dogs, dog maker |
| `unrelated_literal_method` | Value | victim, dogs |
| `unrelated_keyed_call_sites` | Declared | local arrow, interface call, the method's other site |
| `unrelated_value_call_site` | Declared | victim, the same value's other site |
| `unrelated_optional_call_site` | Value | unrelated |
| `unrelated_adapter` | Value | victim, adapted |
| `unrelated_chain` | Declared | victim, dog |
| `unrelated_program_cast` | Declared | victim, num, key |
| `unrelated_dynamic_result` | Declared | victim, dogs |
| `unrelated_several` | Declared | both victims, dogs |
| `the_values_other_instances` | Declared | same value, dogs |

**Driver tests** (`pts/driver/narrow_fallback_more_test.go`):
- RED:
  - `TestEmitCPrintsTheCThatBuildLinks`: "emit-c printed C that the build didn't link".
  - `TestAFailingPairingCostsItsValueNoOtherInstance`: `pts_fn_main_w_number` missing.
  - `TestAFailingCallSiteCostsItsMethodNoOtherSite`: no `pts_method_instance_find` in the C.
- Guards (pass): `TestAFailureNoFallbackLevelCompilesStopsTheBuild` and `TestAFailureAFallbackDropsIsNotReportedBesideTheProgramsOwnError`.

**`compiler_smoke_test.go`:** `needsFallback` has 12 new entries. The filtered smoke run on these 21 seeds passes.

**Expected outputs are reachable:** each of the 15 RED tests (12 new and 3 committed), with its trigger neutralised in a way Node can't see, compiles at All on HEAD and prints exactly its expect block (`red/defused/`).

**Caveats:**
- The call-site tests depend on C248 (`converting boolean to given`, H17) and on a05's union conversion. If those are fixed first, the triggers disappear and the tests need another site failure.
- When committing, add `// skip:` lines.
- The existing pins `TestABuildThatOnlyItsSpeculativeInstancesStop…` and `…DemandsByNameStop…` assert that `EmitCWith(All)` errors, so they need revisiting if the narrowing happens inside lowering.

## 6. Matrix m9

18 triggers × 7 victims (value, decl, iface, literal, local arrow, method value, adapter) × 2 placements (own function, trigger's function), plus 14 same-value victims and 25 controls. The full table is `matrix/report_final.txt`.

- The decl victim survives everywhere.
- The All-level victims (iface, literal, method value, adapter) are lost for every trigger.
- Value-level victims are lost for the 11 triggers that go to Declared.
- All 14 same-value victims are lost.
- With the trigger neutralised: 291/291 OK at All.
- Two harness fixes during the run: the call-site same-value victims now read a typed result (an untyped read hid the loss), and the chain design was changed to avoid side finding 2.

## 7. emit-c vs build, and timings

**Divergence (simulated with wrappers).** With a `PTS_CC` that rejects C containing `pts_method_instance_find`, emit-c prints All's C (275 lines, marker once). The build silently compiles Value's C (238 lines, no marker) and prints `victim TypeError Expected Box<number>, got Box<any>` with exit 0. A wrapper that compiles but fails the link with `undefined reference to 'pts_fn_x'` gives the same result. When every level's C is rejected, the build exits 3 with PTS9001 ("doesn't compile" or "doesn't link"). No natural trigger exists: 0 rejections across the pool and the smoke runs.

**Timings** (seconds, minimum of 3 runs; the large rows are 1 run):

| Program | Lines of C | emit-c | `clang -fsyntax-only` | full build |
|---|---|---|---|---|
| none__iface | 275 | 0.11 | 0.11 | 0.48 |
| sql_query_parser | 1,782 | 0.14 | 0.15 | 1.26 |
| array_pattern_lazy | 4,651 | 0.14 | 0.17 | 1.34 |
| generic_polymorphic_recursion_dispatched | 5,232 | 0.17 | 0.23 | 2.62 |
| abort_signal_shared_threads | 9,608 | 0.26 | 0.29 | 5.02 |
| generic_instances_many_calls | 12,487 | 0.20 | 0.30 | 3.38 |
| esc_40x50 | 28,368 | 0.32 | 0.67 | 9.04 |
| decls_60 | 47,541 | 0.74 | 1.05 | 15.18 |
| esc_150x50 | 92,388 | 1.15 | 2.06 | 27.97 |

- Syntax-only with the build's own `-O2 -g` flags costs about the same.
- Checking syntax adds about 0.1–0.3 s per level for typical programs and 2 s for the largest; that is 6–10% of a full build.
- Syntax-only cannot catch link failures. Compiling and linking at `-O0` takes about 0.9–1.1 s for 10k lines and 6–8 s for 92k lines.

## 8. Side findings (not C176)

1. `new Weird(x).size()` in an escaping arrow's erased copy is PTS1001 at every level (`side/s01`).
2. A typed call through a generic function type inside a class instance that an erased copy made (`Holder<any>.count`) throws `Expected Box<any[]>, got Box<any>` at every level (the first chain design's rows in `results_defused.tsv`).
3. a05's `?.` call at Value and Declared throws `Expected undefined | {…} | {…}, got Object` (`side/a05_value_only`).

The defused chain compiles to 61,992 lines of C at All (the Holder chain runs to the depth limit), so narrowed chain builds can be large.

## Paths

- Worktree: `/home/bryan/projects/parallelscript/.claude/worktrees/agent-af8c2d6cf3dc1ad6a`
  - `pts/tests/run/generic_fallback_spares_{the_values_other_instances,unrelated_adapter,unrelated_chain,unrelated_dynamic_result,unrelated_forwarder,unrelated_keyed_call_sites,unrelated_literal_method,unrelated_optional_call_site,unrelated_program_cast,unrelated_several,unrelated_total,unrelated_value_call_site}.pts`
  - `pts/tests/errors/generic_fallback_keeps_*.pts`
  - `pts/driver/narrow_fallback_more_test.go`
  - `pts/tests/compiler_smoke_test.go` (modified)
- Archive (written last; 2,152 entries, sha256 `d31cf76b635d98da…`, with `INDEX.txt`): `/tmp/h09b5b/repro2/h09b5b-reproducer-probes.tar.gz`
