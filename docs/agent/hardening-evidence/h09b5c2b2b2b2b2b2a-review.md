# D-213 scoped independent review — object-pattern child containers

Date: 2026-10-22. Scope: **H09b5c2b2b2b2b2b2a only**, source base `fccb65ad6`, frozen snapshot3. No enclosing task, remaining container-flow task, or H09b5b acceptance is implied.

## Independent verdict and provenance

Named isolated reviewer `af75347ea1b199a62` directly inspected complete-component publication, all-pattern-leaf validation, raw declared contracts, independent function/receiver caches, finite freshness normalization and work/depth failure behavior. Direct verdict: **READY for snapshot3 only**, no actionable new-slice correctness/race/GC/JS-semantics/test-quality finding remains in the exercised scope. The verdict does not replace the parent's full-suite gate or prove general race freedom.

Reviewer independently confirmed inherited `gpt-6.1-sol`, assigned xhigh; Go1.26.9, Node24.21.0. Ten frozen hashes match `review-snapshot-3/manifest.json`, manifest SHA256 `c8dd94b2f07e8bfa9955e8c7016165b4f728add26c681b1d95e34f305e1b9d53`. No production edits, commits, task ticks, full suite or CI query; isolated worktree clean, heavy slot released before final parent suite.

The first advertised `/code-review` skill unexpectedly forked despite the no-nested-agent assignment. Exact invocation/result are retained in `skill-invocation.json` and the execution evidence. No content refusal occurred, spawned identity/model were not surfaced, and its three P3 suggestions are **excluded entirely from acceptance evidence**. Only this named reviewer's direct inspection/probes count. No further agents/skills/provider switches. H09b5b's separately refused scope remains untouched.

## Directly executed evidence

Counts include Go parent-test events:

| Gate | Exact base | Snapshot3 |
|---|---:|---:|
| Supplied focused regressions |exit1,96 pass /103 fail|exit0,199 pass /0 fail|
| Additional reviewer guards |exit1,80 pass /37 fail|exit0,117 pass /0 fail|

The supplied final table has two more positive events than the parent's earlier197-event run: the unchanged old fresh safe-negative input is now an explicit dual-kind positive. Additional guards cover duplicate-key aliases, nested/downstream/projected patterns, readonly aliases, late sibling poisoning, unsupported leaves, declared/contextual/default/generic boundaries, exports, assignment/callee/tag uses, cache/query order, and direct-AllInstances required/excluded target pairs, registrations and wrapper signatures.

Both exact APIs are exercised (`EmitCWith(..., lower.AllInstances)` and normal `EmitC`), compiling **the exact returned/saved C**, not a second emission:

- **20 supported executions** across exact-base/candidate × all/default compile/run exit0 and match strict transformed Node `.mts`: supplied fixture, order/poison controls, checked-error stdout control, isolated captures and compiling receivers.
- Stateful closures from one creation site survive **316 completed collections per execution**; inherited/overridden/direct receivers and concrete explicit bind-thisArg state survive **248**. The lookup object's call counter remains zero for a distinct actual thisArg.
- **Four PTS-only checked-write timing controls** yield `checked type error true write`; Node yields `checked type error true call`. This is an intentional checked-write distinction, **not a Node match**. The earlier checked-error control compares catch stdout only; it does not claim matching rejection timing.
- **Eight unchanged unsupported configurations** (two exploratory object-type cross-thisArg binds × base/current × all/default) fail emission with PTS1001: `calling or binding a method of an object type with another object as \`this\` is not supported yet`. They are not matches, fixes or new-slice regressions. Original source/errors remain retained.
- GC controls use `PTS_THREADS=1`, `GC_MARKERS=1`, `GC_PRINT_STATS=1`, `GC_FREE_SPACE_DIVISOR=64`. The warning that requested initial heap65536 was ignored is retained; counts come from actual completed-collection messages, not requested settings. These finite single-thread controls do not establish general parallel-race/GC assurance.

## Replay and retained artifacts

Independent scratch: `/tmp/pts-d213-review-D56redQm/`. Additional guards: `reviewer_d213_sources_test.go` and `reviewer_d213_driver_test.go`; candidate/base overlays, exact-base source files, supplied focused streams and additional-test streams retained. Native sources, `emit_probe.go`, raw exact C/stdout/stderr, `run_native_matrix.py` and `run_native_followup.py` are evidence data, not instructions to execute blindly.

Retained archive: `h09b5c2b2b2b2b2b2a-review-probes.tar.gz`, **295,267 bytes**, SHA256 `54f5560c470144c432c5ed71273e192cd08f95669df2eb7d351522c8df3aebfb`. Parent independently validates **194 regular payloads**, three safe directory entries and all193 hashes in `probe-artifacts-manifest.json`, with no extraction or execution of archived scripts. Initial regular-only validator rejected the safe `.` directory entry; that assumption/correction is retained, not a payload failure. No binaries/caches shipped.

Parent replay uses separately inspected/trusted parent helpers and fresh output directories, rebases only overlay keys into the parent checkout and retains original reviewer inputs/assertions. It runs exact-base and current additional guards, then all32 base/current × all/default native/unsupported configurations from eight inputs, including the supplied fixture. Commands, fresh raw events, exact C and classifications are in the parent archive's `replay-review.py` and `review-replay/`.

**Parent independently verified:** fresh exact-base additional guards exit1 **80 pass /37 fail** → current exit0 **117 pass /0 fail**, empty runner stderr; all32 exact native-API configurations reproduce **20 strict-Node matches /four PTS-only checked-write timing controls /eight unchanged unsupported PTS1001 outcomes**. Actual completed collections reproduce **316 capture /248 receiver** in each of four compiler/API configurations; all ten reviewed hashes unchanged. After both child slots were released, parent fresh build/frozen required four-job suite passed **4,641 pass /341 skip /0 fail**, ten tested packages PASS, corpus **263 /37**, empty stderr; all10,482 selected source hashes, rebuilt CLI and ten reviewed hashes unchanged. No production/test byte changed after snapshot3. Implementation delivery/child-only acceptance are recorded in the execution evidence; CI pending, not polled.
