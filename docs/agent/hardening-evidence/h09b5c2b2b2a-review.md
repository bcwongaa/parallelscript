# H09b5c2b2b2a — independent captured-receiver review

Date: 2026-10-22. Decision: D-207. Scoped reviewer: `ab251d7a56b5f3e01`, inherited `gpt-6.1-sol`, xhigh. Verdict: **READY** for this child only. Parent final full-suite and push acceptance are recorded separately in [task evidence](h09b5c2b2b2a.md).

## Scope and independence

- Review only actual captured receiver provenance in method-forwarder synthetic demands, explicit bind's actual thisArg, existing receiver-proof conservatism, runtime capture identity and the tests required for this slice.
- Exact source base `be71aac864884bef6eadfe4cdf8bb5f1b7ce12db`. Reviewer uses an isolated detached worktree and immutable Go overlays; no parent production edits, commits, pushes, task ticks, full suite, CI queries or nested agents. One serial heavy slot, explicitly released after the final gates.
- H09b5b's provider-refused independent acceptance remains blocked, unchecked and untouched. This is not a retry or replacement review of that task. All enclosing receiver-flow tasks remain open.
- Initial snapshot covers four production files. Corrected round two adds only `outside_assignments.go`; the original four hashes remain unchanged. Final six test/fixture files are also frozen. Reviewer verified all eleven snapshot hashes, then independently rechecked them against the parent's final bytes before READY.

## Review rounds and verified finding

Round one independently confirms direct-AllInstances overpairing on the exact base and precision improvements on the four-file snapshot. It also reproduces the already-identified unsupported contextual fixture and repeated-two-class source-count fixture mistakes; these failures are retained, not called optimization regressions. The final contextual driver guard is a checker-generic **returned receiver origin**, with wrapper creation outside the generic function. It retains typed targets on the exact base. Explicit contextual-generic bind/returned/in-body forms that lose their typed result remain pre-existing C255 controls, not new support claims.

**C258: wrapped parameter writes missing from the shared assignment index.** The exact synthetic-demand source probe still proves A after a closed parameter is assigned B through `as`, type assertion, non-null or satisfies wrappers. `parameterSources` reads `assignedAnywhere`, but `markAssignedOutside` originally stripped only parentheses; C257's separate function/class declaration-binding reference index does not cover parameters. The frontend accepts these inputs. Reviewer and parent independently retain genuine source-proof REDs before the fix.

Parent fixes only the shared assignment-target descent, then returns a five-file immutable snapshot to the **same reviewer**. Both receiver and ordinary function parameter proofs now stay unknown. Own-function/nested-closure writes and array-pattern nesting preserve assignment-index and checked narrowed-read tracking. The four wrapped native programs remain unsupported on the exact base and current compiler; strict Node succeeds. This is a verified conservative-proof defect, **not demonstrated supported native wrong output**, and the change does not claim to implement wrapped native assignment syntax.

Round-two independent observations:

- Original assignment index plus final parent guards and reviewer's exact seam probe: **22 fail events**, expected exit **1**, empty stderr. Tests execute assertions, not a build failure.
- Exact base plus corrected precision/table tests: **26 pass / 20 fail events**, expected exit **1**; the corrected contextual returned-origin guards pass. A separate exact-base literal-candidate test is also RED. Commands/selections are retained; counts are not substituted for the parent's differently selected final-fixture RED.
- Final full affected packages including additional reviewer-only overlay tests: lower **301** plus driver **191**, **492 pass / 0 fail / 0 skip** events, exit **0**, empty stderr. The extra 38 events over the parent's 454 are **33 seam/cache/budget controls** and **five exact assertion-seam events**.
- No remaining verified D-207 finding after the correction. The initial RED, unsupported/contextual controls and all earlier probes remain in the archive.

## Native, descriptor and deterministic-growth probes

Reviewer exact-base/final direct-AllInstances replay validates **40 / 40 checks**, including **28 supported programs native-matching strict `.mts` Node on both versions**. Target definitions and return-registered class entries are checked independently of successful native output.

- Diagonal captured sources: 9 → 3 method targets.
- Shared wrapper AST/caller union: 6 → 4 targets; same-class distinct runtime captures: 6 → 2. The two runtime objects keep independent labels/state even though they share a class source.
- Actual differing bind-thisArg: 6 → 2; unknown lookup/known thisArg: 3 → 1. Known lookup/unknown thisArg retains all three. Bind chains retain the first capture; lookup and capture are evaluated once in order.
- Immutable aliases/ordinary returns, every return arm, mutable/any origins, lexical generic function/class contexts, nested shared factories, inherited implementations and overrides behave as required. A checked-any override still rejects in `M1.make`, not an ancestor; this is an intentional checked-boundary control, not a Node-equivalence claim.
- Six unchanged unsupported compiler inputs are classified separately (three wrapped parameter writes, an invalid cross-class destructuring conversion, two generic class-property forms). Three contextual bind failures remain C255 controls. A corrected same-class destructuring input and both supplied fixtures match Node.
- Independent known24 family: **576 → 24** targets and descriptor entries, **11,584 → 5,512 C lines**. Unknown12 retains all **144** and byte-identical C (**4,262 lines**). These inputs differ from the parent's actual-receiver growth family; measurements are not relabeled.
- Reviewer seam controls verify actual `ir.Local` capture preservation, both function/receiver cache query orders, mutable/dynamic/replacement/cycle/generic-context unknowns, cached depth/work exhaustion, and exactly 256 distinct class sources versus conservative unknown at 257.

## Parent-independent replay

The parent independently rebuilds its own exact-base/final direct-AllInstances helpers and executes the reviewer's original matrix/followup/growth sources, strict Node comparisons and target/registration assertions: **40 / 40 checks**, **28 supported native/Node matches on both versions**, no failures. The helpers never narrow or fall back.

The helper CLI transport differs: parent's helper omits the third argument for emission-only and returns 1 for diagnostics, whereas reviewer's helper takes `-` and returns 3. An archived adapter drops only `-` and normalizes 1 → 3 **only when stderr contains PTS1001**, logging each original command/exit/stdout/stderr. Actual recorded invocations: **68 original/normalized 0/0**, **12 original/normalized 1/3**. No source, native result, pair assertion or unsupported classification is changed by the adapter.

Parent also executes the reviewer-only seam/cache/budget source through a test-only overlay without production replacement: **33 pass / 0 fail**, exit **0**, empty stderr. All eleven reviewed source/test hashes are rechecked before the final frozen suite; both child heavy slots are released. Raw archives, exact hashes and final task acceptance are recorded in the companion evidence.

## Final reviewed production SHA-256

| File | SHA-256 |
| --- | --- |
| `pts/lower/forwarding.go` | `19097407b321de920a527b97d994b4afc532fbd1545f25be597b224f35a3de0a` |
| `pts/lower/generics.go` | `e23ab3c146f2359a0cdb5fb65ab866ef8f121f01c3ef4e7a238db46e1977157d` |
| `pts/lower/methods.go` | `fd89b32893f95938682e8a823a67f77bc86ce9e11df2cae0ae90f06986c760e8` |
| `pts/lower/destructuring.go` | `196561860548fac765cf21d0e3271d523602655a5e690242d5f15a8bac069fb8` |
| `pts/lower/outside_assignments.go` | `93a2367fcf101ea6b9e49e72ef774ffbe92ee21732d0353577ef1cfdfea109ab` |

The full eleven-file manifest, exact commands, original failures, raw results and probe sources are in `h09b5c2b2b2a-review-probes.tar.gz` (SHA-256 `4ac0210504bd168694d03c04aaf8ca111329899404a522d5aaa9283e29a2dd17`). Parent validates all **379 regular UTF-8 members**, no links/ELF, against `h09b5c2b2b2a-review-probes-manifest.json` (SHA-256 `7d318dc2f476e693d13eaad087496e1ee95c04a8b0d6b4701ca79f5e01a4f21f`): 378 payload hashes plus the byte-identical embedded manifest. The first parent validator mistakenly expected the external manifest to hash itself; corrected validation checks that extra member explicitly without changing the archive bytes. This READY verdict is scoped local review, not a full-suite, exact-SHA CI, parent-task or H09b5b acceptance claim.
