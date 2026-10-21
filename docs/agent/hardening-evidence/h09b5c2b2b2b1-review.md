# H09b5c2b2b2b1 — scoped independent review

Date: 2026-10-22. Decision: D-208. Reviewer: `a489372664f6d0453`, inherited actual runtime `gpt-6.1-sol`, isolated worktree based on `e7c7a748e`. This report transcribes the same named reviewer's final handoff and distinguishes parent replay. It is not a replacement review of the provider-refused H09b5b scope, nor acceptance of any enclosing task.

## Verdict and frozen scope

**No open findings on corrected snapshot4 or its final snapshot5 fixture-only supplement.** All fourteen snapshot4 production/test files were independently hash-verified before and after execution; the same reviewer independently verified all sixteen final snapshot5 hashes, with those prior fourteen byte-identical. The reviewed production is only `pts/lower/container_sources.go` and the source-proof switch/structural caches in `pts/lower/value_flow.go`; no runtime, fallback/blame or checker-identity change. The complete manifest is retained in `h09b5c2b2b2b1-reviewer-manifest.json`.

The review covers bounded fresh literal and complete-use const-container projections only. Container aliases, parameters, returned/nested containers and broader capture/caller-return flow remain H09b5c2b2b2b2. Unknown/any/mutable/escaped and unsupported/exhausted proofs retain compatible instances.

## Resolved finding: C259 — instantiation expressions preserve implicit `this`

The original `containerProjectionRead` wrapper ascent omitted `KindExpressionWithTypeArguments`. A call such as:

```ts
function replace<T>(this: { value: Value }, value: T): void {
  this.value = second;
}
const held = { value: first, replace };
(held.replace<number>)(1);
const probe = held.value;
```

incorrectly proved only the original `first` source. JavaScript passes `held` as `this` through the instantiation/parentheses, so the complete-use proof must reject this container.

The original three reviewer variants independently RED, as did supplemental instantiated tag, tuple-element call and assertion-wrapped call controls. Extracting an instantiated function without calling it correctly stayed precise. Parent independently executed the unchanged original overlay **three leaf failures plus parent**, then permanent regressions in both function/receiver modes **six leaf failures plus three parents**, before editing production. Snapshot4 adds exactly the missing transparent wrapper and clarifies that numeric key text can already be parser-normalized. Original reviewer source remains unchanged; parent adds three dual-mode regression rows.

The same reviewer independently rechecked the unchanged original and supplemental controls GREEN. Parent replays them separately. This is a verified **conservative-proof/boundary gap, not a demonstrated supported native wrong-output regression**: strict `.mts` Node prints `second\n7\n`; exact-base and corrected direct-AllInstances both reject the realistic explicit-this executable with PTS1001, including `` `this` outside a class is not supported yet ``.

## Executed reviewer evidence

Counts below exclude package-pass events:

- **115** independent source test/subtest passes, zero failures: complete-use guards, both function/receiver cache orders, normalized numeric and escaped-string keys/collisions, original and supplemental instantiation cases.
- **693** affected lower/driver test/subtest passes, zero failures/stderr (plus two package passes).
- **32** exact-base/current native inputs: base **27**, corrected current **28** strict-Node matches; zero native regressions or target-row mismatches. Known function/direct/captured receiver selections prune three compatible rows to one; shared captures retain two; mutation/alias/nested/dynamic controls retain all three.
- Four current direct-AllInstances nonmatches remain unchanged unsupported inputs: two numeric-object-key shapes, an interface-method bind with a different thisArg, and the historical containing fixture requiring fallback. The unrelated-total fixture newly succeeds under AllInstances.
- Both corrected historical default-build controls independently match strict Node on both versions.

Historical const-to-let fixture corrections preserve every assertion/expected stdout while restoring genuinely uncertain container origins. Removing the obsolete unrelated-total smoke exemption strengthens AllInstances coverage.

No introduced race/GC issue was found by inspection/executed scoped probes; this is not a full-suite/sanitizer/CI claim. The reviewer ran no full suite or CI, committed/pushed/ticked nothing and explicitly released their one serial heavy slot before the parent's final four-job suite. Both children are finished. The reviewer's mandatory early code-review Skill reported forked execution but returned only a waiting/preflight response, not additional review evidence; no Agent tool calls or additional independent-review agents were launched by the reviewer. That preflight is not acceptance.

## Parent independent replay and retained failures

Parent-built exact-base/corrected direct-AllInstances helpers independently replay all **32** inputs, matching all target rows and the **27 / 28** strict-Node agreements. Parent executes **110 + 5 = 115** independent source-control pass events, including unchanged original/supplemental C259 cases. Post-fix parent affected tests **693 pass**, explicit generic clang **191 programs / 154 C**, both compiling behavioral mutants rejected. All twelve re-emitted final-current growth C hashes equal their measured versions.

The first parent replay completed all32 inputs, then failed an overly literal diagnostic-byte assertion because the diagnostic quotes `this` with backticks. Correcting only the verifier string and replaying in a fresh directory passes all210 commands. Original failed verifier receipt is retained, not silently counted green.

Other retained reviewer attempts: original missing-submodule affected run (eight d2s.c failures), successful unchanged-runtime rerun; initial matrix expectations poisoned by unused consumers, corrected by isolating independent inputs while retaining original source/hash receipts; initial strip-only Node parameter-property error, corrected to harness-compatible transform-types. None is relabeled a slice regression or a successful run.

## Safe archive and replay

- `h09b5c2b2b2b1-reviewer-probes.tar.gz`: **1,301,837 bytes**, SHA-256 `74f70cc70b2b0f771e468d3ede9c01a5825a60850971191568a4c4e99782c149`.
- `h09b5c2b2b2b1-reviewer-manifest.json`: SHA-256 `432fd82c891ee63f946302a1219ec1a0c34c2bb0d8b070475d1fad271b5daa61`.
- Parent validates **364 regular relative-path UTF-8 members**, no links/ELF: **363 payload hashes** plus the checked embedded manifest. Both frozen snapshots, unchanged original tests, supplementary controls, native/Node inputs, helper/runner source, emitted C and original/final receipts retained; executables excluded.

After unpacking into a new empty data directory, keep any locally authored replay scripts elsewhere and invoke Python with `-I`. The retained `replay_source_tests.py` accepts the source worktree path; `--snapshot3 --run '^TestReviewContainerInstantiationThisEscape$'` replays original source RED without production edits; omitting `--snapshot3` selects corrected GREEN. Rebuild the retained strict helper source against the named exact base or final snapshot rather than expecting archived binaries.

## Final snapshot5 fixture-only supplement

After the first parent full suite completed with one obsolete diagnostic-fixture leaf failure (plus its parent), the same reviewer resumed in the same isolated worktree/runtime. Snapshot5 adds only the corrected negative and a complete original-program positive; all fourteen snapshot4 hashes remain unchanged. They independently hash-verified all sixteen final files before/after execution and reported **no additional or open findings**, with the previous corrected-proof review unchanged.

The reviewer independently strips comments/blank lines to prove the positive's executable text equals the original safe program, including all six classes/constructions and `.value + 1`. The negative's only executable change is `const recs` → `let recs`; its expected Box-total PTS1001 directive is retained. Original safe and new positive each retain exact-base direct-AllInstances PTS1001 total1024, while current direct-AllInstances native compiled with explicit clang matches strict `.mts` Node `recursion 6`. The mutable negative preserves that diagnostic on both compilers. Current targeted error/run/Node/clang harness: **seven test/subtest passes** plus package; exact-base corrected error assertion: **two passes** plus package. Targeted smoke: **two programs / one C**, no new exemption. Harness/runner stderr is empty, native script has zero problems, and their serial heavy slot was explicitly released. No full suite or CI was run by the reviewer.

Parent independently executed the same original-safe/negative/positive classifications before this handoff: current/base corrected error assertions **two pass each**, positive run/Node **four pass**, original safe current strict native/Node agreement and exact-base PTS1001. Parent default CLI controls give **five native/Node matches** plus the new positive's expected exact-base total diagnostic (14 commands); expanded explicit generic clang passes **192 programs / 154 C**, exit **0**.

Supplemental archive `h09b5c2b2b2b1-reviewer-snapshot5-probes.tar.gz`: **230,018 bytes**, SHA-256 `4c753556577ac6de1bce406d1aeb4cef1f8eda355bd91239b3a0461a20ffd7f2`. External `h09b5c2b2b2b1-reviewer-snapshot5-manifest.json`: SHA-256 `60c772af6a24fa4fb987448ffd78c655b4019a7ffecd5183cf165e72388ca852`. Parent validates **24 regular safe relative-path UTF-8 members**: **23 payload hashes** and embedded manifest SHA-256 `09b42fb66f4c2845376b1020db163e940064c3c59cc778b7befe2d790ab2d387`; no links or ELF. All sixteen final hashes match the parent. Original snapshot4 archive/report provenance is preserved, not overwritten. Runners `snapshot5-recheck/recheck_fixtures.py` and `replay_harness_checks.py`, original input, frozen fixtures, source overlays, emitted C and raw receipts are retained. Apply the same empty-directory/`python3 -I` replay safety rule above.

Parent final full-suite and push/acceptance receipts belong in `h09b5c2b2b2b1.md`; this scoped review does not tick the task.
