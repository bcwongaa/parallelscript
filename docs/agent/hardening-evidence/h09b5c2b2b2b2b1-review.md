# D-210 scoped independent review — H09b5c2b2b2b2b1

Date: 2026-10-22. Reviewer `a7715efd3b03fb115`, inherited gpt-6.1-sol (same as parent), isolated exact-base worktree `.claude/worktrees/reviewer-d210`, HEAD `e631cca68`. Requested/inherited xhigh effort is not separately surfaced. This is the reviewer's reported evidence; parent-executed replay is recorded separately in the slice evidence.

## Final verdict

No open actionable correctness/race/GC/JS-semantics findings in frozen snapshot4 for this bounded D-210 slice. One verified test-quality finding was fixed during review. Neither that finding nor the parent-discovered readonly contract gap establishes a wrong-native-output bug. H09b5b was not reviewed, retried or replaced; its refused acceptance gate remains blocked.

The reviewer independently verified all nine snapshot4 hashes. Only `nested_container_sources_test.go` differs from snapshot3; production bytes are identical. Final focused lower/driver/reviewer overlay: **492 test/subtest pass events**, zero failures, empty stderr. No full suite or CI query was run by the reviewer. All supplied worktree files remain at the initial snapshot1 hashes; the reviewer wrote only their own evidence/probe inputs, with no parent writes, commits or task ticks. One heavy-worker slot released; no review processes remain running.

## Resolved P2 test-quality finding: vacuous array-safety fixtures

Snapshot3 `nested_container_sources_test.go:147` used `literalProjectionPrelude(false)` for `[first, second]`. Its distinct inferred callable contracts make the unmodified origin unknown before use validation, so the ten negative assertions did not demonstrate that mutation/escape guards worked.

Independent evidence:

1. An expected-positive assertion on the original unmodified origin fails.
2. Shared explicit `Value` terminal contracts make the unmodified control resolve `first`; all ten unsafe uses remain unknown.
3. A reviewer-only overlay replacing the whole `literalContainer.projectionRead` body with `return true` leaves the original table green: **11 pass events**.
4. The strengthened snapshot4 supplied table catches the same compiling behavioral mutant: the unmodified positive control passes, all ten unsafe subtests fail (**11 fail events** including parent), with actual assertions rather than a build failure.

Final correction at `nested_container_sources_test.go:151–158`: use the existing typed prelude, retain every original unsafe input string/nil assertion, and add the unmodified positive control for all four selected/root/plain projections. No production byte changed. Parent independently reproduced the reachability failure and original-versus-strengthened mutation behavior before acceptance.

Intentional original-fixture RED command retained in the archive:

```sh
GOMAXPROCS=1 PTS_HOME=/home/bryan/projects/parallelscript \
go -C /home/bryan/projects/parallelscript/.claude/worktrees/reviewer-d210 \
test -overlay /home/bryan/projects/parallelscript/.claude/worktrees/reviewer-d210/review-d210/inputs/array-controls-overlay.json \
-count=1 -p 1 -parallel 1 ./pts/lower \
-run '^TestReviewD210OriginalArrayFixtureShouldBeKnown$'
```

The reviewer also independently replayed the parent-discovered mutable/readonly common-element contract gap as snapshot1 RED/final GREEN; this is a conservative-proof contract correction, not demonstrated native corruption.

## Independent source and native evidence

- Reviewer-owned source probes: complete mixed-tree target propagation and parentheses, selected conditionals, optional/union/common method/accessor/readonly/tuple exclusions, nonliteral origins, contextual/generic lexical barriers, late sibling escapes/mutations, both query orders, budgets, active-cache cleanup and separate function/receiver memoization. **110 reviewer-owned pass events** on final production.
- Eight strict `lower.AllInstances` mixed-tree matrices, four families (ordinary function, direct receiver, captured receiver, returned wrapper), each closed versus escaped. Exact-base closed **9 → 3** pairs; escaped **9 → 9**. Required/excluded targets, class registrations, bound-wrapper signatures and receiver GC layouts checked.
- All eight exact emitted-C programs compile and native stdout matches strict-ESM Node. A ninth stateful program covers shared closure sites, captures, inheritance/override, actual bind `thisArg`, evaluation order, mutation/dynamic readback and allocation churn. Baseline and final both match Node through **15 observed BDWGC collections each**, with raw `GC_PRINT_STATS` stderr retained.
- Initial Node strip-only parameter-property failures and unsupported interface-other-this exploration are preserved as setup/unsupported outcomes, not D-210 defects or successful checks.

## Replay and archive

Safe archive: `h09b5c2b2b2b2b1-review-probes.tar.gz`, **359,157 bytes**, **252 UTF-8 relative regular-file members**, SHA-256 `631a5e072d415ccbd21c865e27731025aed3887366ab513071c4d5d7fa5f2b67`. Parent independently validates its digest, membership, UTF-8 contents and absence of links/traversal/binaries before copying it into this directory. No archive extraction or uninspected child script execution is needed for parent replay.

Archive includes final nine input copies/hash manifest, snapshots1/3/base overlays, reviewer probes, deliberate mutant, commands and raw RED/GREEN/native/Node/GC streams. Exact command inventory: `review-d210/inputs/command-inventory.json`; nested comparison inventory: `review-d210/results/comparison/commands.json`; final overlay: `review-d210/inputs/snapshot4-overlay.json`. The reviewer executed the constituent commands and syntax-checked the combined replay wrapper; they did not claim to have executed that wrapper as a whole.

```sh
GOMAXPROCS=1 PTS_HOME=/home/bryan/projects/parallelscript \
python3 -I /home/bryan/projects/parallelscript/.claude/worktrees/reviewer-d210/review-d210/tools/replay.py \
/home/bryan/projects/parallelscript/.claude/worktrees/reviewer-d210 \
/home/bryan/projects/parallelscript/.claude/worktrees/reviewer-d210/review-d210
```

Parent uses their own trusted strict-AllInstances helper and replay script after inspecting the supplied test sources and native-case generator. Final full-suite acceptance remains a parent responsibility.
