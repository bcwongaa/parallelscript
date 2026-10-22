# H09b5c2b2b2b2a — scoped independent review

Date: 2026-10-22. Decision: D-209. Reviewer: `a72de7c73c2112d71`, surfaced inherited runtime `gpt-6.1-sol`. Actual review runs in isolated `/home/bryan/projects/parallelscript/.claude/worktrees/review-d209-a72`, detached at exact source base `cc17630fb`; its five production/test files are copied from the parent's frozen snapshot1 and hash-verified. The initial clean auto-worktree was removed after the design-only interim handoff; on resume the reviewer detected the parent cwd before edits and created this separate worktree. No parent edits by children.

## Scope and status

Review only D-209's immutable container-alias component proof, complete transitive uses, per-edge static data contracts, query/cache/budget ordering and shared function/receiver/wrapper creation sites. No runtime, fallback/blame, checker-instance identity or upstream change. H09b5b's provider-refused independent acceptance is not retried, replaced or accepted. Parameter/returned/nested-container and broader caller-return flow remain H09b5c2b2b2b2b; every enclosing parent remains unchecked.

**Scoped READY; no verified findings for D-209.** Parent independently replays the frozen source controls against current and exact-base production: **88 test/subtest pass events + 1 package pass** on each, exit **0**, empty stderr. The reviewer's initial “89 pass events” included the package; the parent's first postprocessing assertion mistakenly treated all 89 as tests, then the corrected counter separated them. No source test failed. Parent independently executes all **27** native inputs: **26 supported native/strict-Node matches per compiler**, one unchanged PTS1001 control, **25 required/excluded target matrices and 9 descriptor matrices per compiler**. Collecting-GC reruns also match Node with **4 observed collections on each compiler**. Final five-file hashes match; both reviewer heavy slots are explicitly released. The required parent full suite is separate from this review verdict.

## Independently replayed source controls

Probe `pts/lower/review_d209_alias_test.go`, SHA-256 `8072b02e51c20133e856a444a5208b70dd26ae4e8ee938a72e9fdc17526b87d3`, uses the pre-D-209 proof API. Parent copies its verified bytes into scratch and uses Go overlays, never editing production/test sources for replay. Current command: `GOMAXPROCS=1 go test -count=1 -p 1 -parallel 1 -timeout 10m -json -overlay <scratch>/reviewer-replay1/current-overlay.json -run '^TestReviewD209' ./pts/lower`. Exact-base command adds `D209_REVIEW_BASE=1` and substitutes `base-overlay.json`, restoring exact-base `container_sources.go` hash `f7edb3ad3e95aed914120cba7bfa061dbcabd1aa05c67de97a7185704aeae18a`. Both actual executions pass all 88 tests.

Independent controls cover identical/mapped/readonly/reordered/optional data contracts, changed method/accessor/index/optional/callable contracts, whole-component and cross-kind query orders, shadowing/nested writes/late escapes, fanout 300/1,024/1,400, low-budget poisoning and 256/257 selected-source unions. Original and parent raw JSON/stderr, overlays, exact-base file and summaries are retained; unknown controls remain unknown on both compilers.

## Independently replayed native/C and GC controls

The reviewer executes **26 supported programs × 2 compilers**, all matching strict-ESM Node, then the parent independently rebuilds exact-base/current helpers from the inspected `cmd/main.go` and replays all inputs. Helpers use strict `driver.EmitCWith(..., lower.AllInstances)` followed directly by `CompileProgram`, never fallback. Parent source/test bytes remain unchanged; only helper/replay paths are adapted to independent scratch directories. Exact argv, stdout/stderr, C, exits, original/corrected metadata and result assertions are retained.

The initial 23 inputs cover typed/readonly/finite-record and numeric-key aliases, arrays, sibling/dynamic mutation, assertions and changed contracts, deferred parameter/returned/nested-container origins, shared function/closure and receiver sites, capture before/after mutation, inherited/overridden methods and differing explicit-bind thisArg. **22** compile and match Node on both compilers. Function-property → generic-method annotation conversion remains the same source-facing **PTS1001** on both, not a successful native program.

The reviewer retains initial overly optimistic `.make.bind(receiver)` expectations and original failed pair-check metadata, plus an explicit correction: both versions keep all six compatible method target/descriptor pairs. Parent independently confirms this unchanged conservative behavior, including byte-identical C for the shared-site bind control. It is not a correctness finding or a claimed precision improvement. Four supplementary executed native inputs add three **stateless** direct extracted-method target/descriptor-union controls and one stateful closure allocation-churn control. Stateless extracted methods do not claim equivalence of Node's and ParallelScript's stateful automatic method capture; stateful closures/explicit bind are tested separately.

Across all supported rows parent verifies **25** target matrices and **9** descriptor matrices per compiler, without weakening missing/excess-pair assertions. The additional allocation-churn rerun sets `GC_PRINT_STATS=1 GC_INITIAL_HEAP_SIZE=65536`; exact-base/current output still matches Node, with **4 observed GC collections each**. This finite exercised check is not a proof of general GC/race safety. Reviewer additionally runs the unchanged focused projection tests: **651 test/subtest passes + 2 package passes**, zero failures/stderr.

## Retained review artifacts and limits

Parent independently validates `h09b5c2b2b2b2a-review-probes.tar.gz`: **391,654 bytes**, SHA-256 `5a3507b612be5146f64f22c7aea566173870285c4759b33819dcbcd6f85e0ea4`, **419 regular relative-path UTF-8 files**, no links/ELF/absolute/traversal entries. Per-file hashes: `h09b5c2b2b2b2a-review-manifest.json`. It retains all 27 original inputs, frozen files, independent source controls, helpers/generators/replay commands, original and corrected expectations, emitted C and native/Node/GC raw evidence. `.review-d209/replay.sh ROOT` was syntax-checked; its constituent commands were actually executed, not an extra aggregate replay claim. Parent independent replay commands, overlays and raw receipts are separately retained in the parent probe archive.

No reviewer full suite, CI, or ThreadSanitizer run is claimed. The final scoped verdict concerns only this immutable-alias precision slice; H09b5b's refused review and every broader parent remain untouched.

Frozen snapshot1 hashes:

| File | SHA-256 |
| --- | --- |
| `pts/lower/container_sources.go` | `bc984d24d9fd7855a25caf5360f555bd5b84a7b270cf049828b9aa766c1ef5f5` |
| `pts/lower/literal_projection_sources_test.go` | `b42504da942ac858bc0d1ca94d8b67c1596f1b212884a3e22c7622537e2a4fbc` |
| `pts/lower/container_alias_sources_test.go` | `776a3c399f73f3c48fea2d8c5bcb81eea013cbba70ad71e6d1fbabf0164f9824` |
| `pts/driver/container_alias_flow_test.go` | `ead69d05fee7806be932c64ea2d0509a2ecefd8a338797036371aa04b4d2725c` |
| `pts/tests/run/generic_container_alias_sources.pts` | `4e8a862b7ff05e7986411cf98520773e1f12a913031befc167432beeaa71ce38` |

Parent full-suite, push and child-only task acceptance belong in `h09b5c2b2b2b2a.md`; this scoped review does not tick the task.
