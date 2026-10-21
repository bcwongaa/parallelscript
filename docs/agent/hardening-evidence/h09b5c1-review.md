# H09b5c1 independent review

Date: 2026-10-21. Scope is only D-201's new ordinary function-value source proof. This is not a review, retry or acceptance of provider-refused H09b5b. Named reviewer `abf1391354a0f7c8d`, isolated worktree `agent-abf1391354a0f7c8d`, confirmed surfaced runtime **gpt-6.1-sol**, no model/provider switch. Parent retains responsibility for integration and verification.

## Round 1: two confirmed findings

### C252: shorthand-assignment default hid a consumer escape

The source-reference index used `readSymbol` on every identifier. That helper used the shorthand property's value symbol for **both** its name and an identifier in its default initializer. In `({ alias = use } = optionsWithoutAlias)`, the read of `use` was therefore attributed to `alias`, so `use` appeared closed. Its parameter proof included only its direct caller's factory and omitted another factory called through the alias.

Reviewer source-only probe `TestReviewShorthandDefaultEscapesConsumer` returned one known source instead of unknown. Reviewer native probe `default_escape.pts` printed one `42`, then threw `Expected Box<number>, got Box<any>`; strict-ESM Node and exact-base `7b3b240da` printed two `42` lines and exited 0. Parent independently ran all three frozen compilers/oracles before changing production code with the same results (`review-native-red.json`, stdout/stderr receipts).

Parent regressions: `TestShorthandDefaultEscapesTheConsumer`; direct-AllInstances `TestUnknownConsumersKeepCompatibleValues/shorthand_default` requires both first/second instances; active run/Node fixture `generic_value_flow_shorthand_default_escape.pts`. Parent's corrected RED (`review-red2.*`) failed the source proof and required-instance assertion. Fix is a one-condition intended-contract correction in `unions.go/readSymbol`: use the shorthand value symbol only when the identifier is the shorthand's **name**. Initializer identifiers resolve normally. No union conversion or checked-any rule changed.

### C253: cached arguments left repeated caller joins unbounded

Each distinct parameter-read AST node recomputed the entire caller union. Argument proofs were cached, but their merges were not charged, so N reads × N calls repeated N² work while consuming only the read's one budget step. Reviewer measured 500×499 = 0.127 s, 1,000×999 = 0.509 s, 2,000×1,999 = 1.992 s, 4,000×3,999 = 8.322 s after warming the first proof.

Parent deterministic RED `TestParameterSourceJoinsChargeCachedArguments` warms every argument proof, then gives the parameter query only three steps; the old implementation incorrectly completes the caller union. `TestParameterSourceProofIsSharedAcrossReads` controls reuse of the same parameter at separate reads. The first attempt used an invalid message argument to `assert.DeepEqual` and failed to build; that fixture error is retained in `review-red.*` but is **not** counted as a behavioral RED. Corrected `review-red2.*`: 4 fail / 1 pass events, including the enclosing retention test, no build failure.

Fix: route the parameter declaration through `valueSources`' cache/active set, so its joined proof is shared across all reads, and charge every caller visit against the 4,096-step budget even when its argument is cached. Parent independently measured the original flow source with a Go overlay and the final implementation, one worker each:

| Callers / reads | Original repeated reads | Fixed repeated reads | Fixed first proof |
|---|---:|---:|---|
| 500 / 500 | 116.6 ms | 0.451 ms | known |
| 1,000 / 1,000 | 446.5 ms | 0.882 ms | known |
| 2,000 / 2,000 | 2,696.2 ms | 1.140 ms | known |
| 4,000 / 4,000 | 9,313.7 ms | 2.095 ms | unknown: cold caller analysis exhausts the charged budget |

First-proof times are retained separately. These are bounded shared-host timing observations, not statistical latency guarantees. Unknown at the work limit is deliberate and keeps compatible instances; it is not a proof of no functions.

## Conservative precision observation (not a correctness blocker)

`TestReviewBudgetDoesNotPoisonShortSharedProof` demonstrates that a depth-limited alias query can cache unknown for an intermediate alias and make a later short query less precise than with a fresh cache. Reviewer explicitly classified this as conservative precision/order dependence, not a missing-required-instance or soundness finding. Parent separately reran and retained its expected failing precision assertion (`cache-precision-observation.*`). This probe is **not claimed green** and its assertion is not weakened or silently removed. Disposition: documented D-201 analysis limitation; cached unknown keeps the old compatible pairing. Improving contextual precision is not required by this bounded c1 slice.

## Parent GREEN and independent re-review

After fixes, affected lower/driver packages: `review-green.*`, **133 pass / 0 fail**, exit 0, empty stderr; rebuild exit 0. Direct native shorthand-default fixture prints `42\n42\n`, exit 0, empty stderr. Focused source/run/Node guards plus generic clang smoke: `review-smoke-green.*`, **24 pass / 0 fail**, exit 0, empty stderr, **169 programs / 129 emitted C**, two smoke jobs, no mutants.

Independent reviewer applied the supplied fix patch only in their isolated worktree and rebuilt their own CLI with one worker. P1 now exits 0 with `42\n42\n`, matching exact base and Node; the three original expression/unknown-origin/unknown-parameter runtime fixtures also match Node before and after. Their focused post-fix run records **34 pass / 0 fail / 0 skip**, two packages PASS. With argument proofs warmed, reviewer repeated-read times were 0.259 / 0.509 / 1.068 / 2.041 ms at 500 / 1,000 / 2,000 / 4,000 callers, respectively. No remaining c1 correctness blocker; heavy slot released before the parent full suite. The separate conservative alias-cache precision probe remains explicitly non-green.

[Retained reviewer archive](h09b5c1-review-probes.tar.gz): SHA256 `85471b57e08a1b1205383b2787fd657841a52ab13a758af4e93ee3c04b6595a6`, 10,252 bytes, 28 regular members. Parent independently verified exact SHA, unique safe paths, regular/directory types only, no ELF payloads, and extracted into a new empty scratch directory. Archived runner scripts were not used as substitutes for parent acceptance. First parent full suite exited 1 on the obsolete known-source error-fixture precondition (one leaf and its parent, corpus still 263 / 37). Parent is preserving the unknown-source diagnostic guard and adding the original program as a positive native/Node control; the reviewer was resumed only for that narrow c1 test disposition. At that point full acceptance required the repeated parent suite; its completed outcome is recorded below.

## Narrow fixture follow-up after the first full suite

Reviewer resumed with context intact, only for c1's now-obsolete speculative-declaration error precondition. Independently ran the original program with their existing reviewed CLI and strict `.mts` Node: both exit 0, stdout exactly `weird 0\nthrough any 1\ndogs 1\n`, empty stderr. The any[] argument variant exits 1 with the unchanged `using a Dog[] as a Animal[] where it may be changed or kept` PTS1001. Disposition: **approve the fixture correction**, retaining the original error substring and adding the original known-source program as a positive run control. No production/parent edits, no full suite and no b review. Heavy slot released before the parent acceptance repeat. Reviewer executed equivalent variants in their isolated snapshot, not the parent's new comment/file; the eight parent harness events remain parent-executed evidence, not reviewer evidence. Eleven supplemental source/runner/result/stdout/stderr files were copied with SHA256 provenance into `reviewer-fixture-supplement/` in the parent archive; parent checked the result JSON and did not execute the copied runner. The separate [supplemental archive](h09b5c1-review-fixture-probes.tar.gz) is also retained unchanged: SHA256 `084530a12f07da50fe8e3c692a498659be153817b0ef3add151cf2ca33522cde`, 1,959 bytes, 11 flat regular members. Parent independently verified SHA, safe paths/types, no ELF, and exact payload equality with all eleven copied files.

## Final parent verification

Final build exit 0; required four-job full suite (`acceptance-suite.*`) exit 0: **2,165 pass / 335 skip / 0 fail**, all ten packages PASS, corpus **263 pass / 37 skip / 0 fail**, empty stderr. The command wrapper exited 1 only after the suite, because its post-check compared existing tracked submodule directories against its file-only pre-check. Parent independently applied identical regular-file filtering: all **1,548** files and CLI SHA256 unchanged, no added source files; original/corrected receipts are both retained. This bookkeeping error is not relabeled as a passing wrapper or a test failure. Full stream, source hashes and parent probes are linked in [c1 evidence](h09b5c1.md). No remaining bounded c1 correctness blocker; b acceptance remains blocked and c2/d are not completed by this review.
