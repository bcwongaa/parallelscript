# H09b5c2a independent review

2026-10-22. **Corrected c2a review: ready, no remaining verified blocker.** The parent's separate fresh final full suite also passed (2,271 pass / 335 skip / 0 fail); parent accepted c2a after implementation `5c0dce22c` was pushed to origin/main. CI pending; see parent evidence. This report covers only D-202's new returned ordinary function-value source proof. It does not retry, replace or accept the provider-refused H09b5b review.

## Isolation and scope

Named reviewer `a8664b3b7e1824ceb` confirmed surfaced runtime `gpt-6.1-sol`; no model/provider switch. Isolated worktree based at documentation-only `9988a04c3`, copying six frozen source/test files from the parent. Production source baseline is `79eb752b9`. The scoped `/code-review` completed with an empty findings list and no provider refusal; reviewer static assessment and actual probe execution are recorded separately.

The initial static review found no soundness blocker: all explicit returns are over-approximated, nested callable/class bodies are excluded, return/parameter identities are declaration-shared rather than call-specific, and nil remains unknown under uncertainty/cycles/bounds. Methods, wrappers, containing-code caps and H09b5b acceptance remain outside this review.

## Contextual generic boundary correction

Reviewer identified that the initial generic exclusion checked only `len(fn.TypeParameters())`. Contextually generic arrows/function expressions can have zero syntactic parameters and one checker-signature type parameter:

```ts
const returned: <U>(x: U) => Fn = x => first;
const probe = returned(0);
```

Independent original-snapshot source probe: **exit 1 / 3 fail events**, both forms log syntax 0 / checker 1 and proven `[first]` instead of D-202's required unknown. Empty compile stderr; assertion-inspection diagnostics from the virtual overlay filename are retained. An earlier one-second background deadline killed an invocation before output; retained as a tool-timeout setup attempt, not a provider refusal or a test result. A command-validator rejection before execution is likewise not counted as a test.

Parent independently added two lower source-proof and two direct-AllInstances retention cases: **RED exit 1 / 6 fail events**, no fixture/build errors. Parent correction uses `checker.GetSignatureFromDeclaration(fn).TypeParameters()`; identical parent command **GREEN exit 0 / 6 pass**, both packages, empty stderr and unchanged source hashes. This restores the explicit nongeneric scope. No missing-required-instance/native-output regression was established.

## Extra guards and parent verification

Reviewer prepared cached-callee summary-join budget and 256/257 source-union boundary tests, nine additional unknown/async/generator/mutation/cycle exclusions, and a shared generic-enclosing/captured-closure program. Parent integrated the guards as `returned_value_bounds_test.go` and `generic_value_flow_returned_enclosing_captures.pts` without weakening existing tests.

Parent actual follow-up execution:

- complete lower/driver packages: **233 pass / 0 fail**, exit 0;
- native/Node guards: **26 pass / 2 documented no-diff skips / 0 fail**, exit 0;
- generic clang smoke: **178 programs / 136 emitted C**, exit 0;
- direct-AllInstances emission, native compilation and execution of the enclosing/capture program: exit 0, exact Node `.mts` match for all five output lines;
- Go overlay removing only the cached-callee join charge: **exit 1 / 1 behavioral assertion failure**, not a build failure. Disk source hashes unchanged.

## Independent corrected-snapshot execution and verdict

Reviewer actual execution, all sequential with one heavy worker:

| Check | Actual result |
|---|---|
| Original contextual generic overlay | exit 1, 3 fail events; syntax 0 / checker 1 / sources `[first]` |
| Same overlay after correction | exit 0, 3 pass events |
| Extra bounds/exclusion overlay | exit 0, 14 pass events (charged cached joins, 256/257 union, nine exclusions) |
| Refreshed eight-group lower/driver focused checks | exit 0, 86 pass events; empty stderr |
| Enclosing/capture control, direct AllInstances | emitted and clang-compiled 59,701 C bytes, no fallback; native/strict-ESM Node exit 0, identical five output lines |

Corrected production SHA-256 `e4026f0db56f6a069df282614feb7af98c445078b65fa92a4f3c21cc225a9564` matches parent. C0/C1 have different layouts; generic function/class instances and separately returned captured closures preserve their own captures. No production implementation edits or writes to the parent checkout by the reviewer, no tools/agent edits, no commits/pushes. Heavy slot released before the parent's final four-job suite.

**Verdict:** ready on corrected c2a; no remaining verified correctness/race/GC/JS-semantics blocker in this bounded diff. The only verified finding was the contextual-generic exclusion gap, fixed and independently rechecked; it was not a native wrong-output or missing-required-instance claim. Safe archive `h09b5c2a-review-probes.tar.gz`, SHA-256 `63364a966d87fd2546e05a470a0b782193fea92ae3d01f302e02b21914137377`: parent validated all 45 members as safe UTF-8 regular files without binaries/symlinks, and independently recounted original RED 3 fail, corrected GREEN 3 pass, bounds 14 pass and final focused 86 pass from raw JSONL. Initial broad filter's 83 pass events include one existing panic test in addition to the 82 new initial cases; not conflated with the exact final focused count. Relocatable contextual replay, initial/corrected source snapshots, commands/manifest, direct-AllInstances harness and capture source/C are retained. No sanitizer/race-stress run is attributed to the reviewer.

The parent's separate fresh required four-job full suite after all source/test follow-ups passed: build/suite exit 0, **2,271 pass / 335 skip / 0 fail**, all ten tested packages PASS, corpus **263 pass / 37 skip**, empty stderr. All 6,664 source hashes and final CLI SHA-256 `6dbdf066a392e0369075007e0c691d1f96452e1223fa02a126e307b0b55e1c22` unchanged. The earlier 2,251-pass full suite is retained but does not cover the final follow-up files. Parent raw stream and manifest-validated archive are linked from [parent evidence](h09b5c2a.md); CI pending, not polled. H09b5b's refused acceptance, c2b and all later tasks remain outside this verdict.
