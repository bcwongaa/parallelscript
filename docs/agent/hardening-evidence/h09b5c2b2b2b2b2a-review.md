# D-211 scoped independent review — H09b5c2b2b2b2b2a

Date: 2026-10-22. Named reviewer `a12c99c879aeaedfd`, inherited `gpt-6.1-sol` (same as parent), isolated exact-base worktree `.claude/worktrees/agent-a12c99c879aeaedfd`, base `5cc375c5abc21aa1f739b4c4faeadefc0876fafc`. Requested/configured xhigh effort is not separately introspected. Reviewer results below are distinct from parent-executed verification in the [slice evidence](h09b5c2b2b2b2b2a.md).

## Final verdict

**No actionable scoped correctness, conservative-source, cache, declared-contract, JS receiver-semantics, race/GC or test-quality findings** in the final eight-file snapshot2. All eight production/test/fixture hashes were independently reverified after execution. No production byte changed between snapshots1 and2; the latter adds only the unchanged-source safe-extraction test relocations.

Only named immutable literal roots and their static immutable subtree extractions are reviewed. Parameter/returned containers, fresh inline extraction roots, destructuring, nonliteral nested origins and broader caller-return/source precision remain deferred. H09b5b's refused independent acceptance is untouched: not reviewed, retried or replaced.

## Independent evidence

| Check | Observed result |
|---|---|
| Reviewer-authored source guards | Exact base **169 pass / 14 expected positive-proof fail events** → current **183 pass / 0 fail**; empty stderr, no build failures |
| Supplied final extraction/relocation tests | **346 lower + 99 driver pass events**, zero fail/skip, both packages PASS, empty stderr |
| Exact `lower.AllInstances` natives | **20 base/current executions** for nine independent inputs plus supplied fixture, all match strict-ESM Node; no fallback |
| Four permuted known-source families | **9 → 3 pairs**, exact required/excluded targets, registrations and bound-wrapper descriptors checked |
| Four poisoned families | **9 → 9 pairs**, conservative target sets retained |
| Stateful capture/GC control | **Nine completed GC collections per compiler**, independent lexical captures and inherited/overridden receiver state preserved |
| Supplied fixture | Exact **20 expected stdout lines** match Node and both natives |

Source guards exercise whole-component poisoning through root/child/sibling views, both query orders and proof kinds, declared contract conversions, nonvacuous array controls, selected-path/kind cache separation, bounds, late unsafe siblings and deferred origins. Explicit-this and wrapped-assignment cases are source-proof guards only, not claims of new native syntax support.

The reviewer inspected both old-test diffs: unchanged former safe negative programs now have positive expectations; all unsafe input strings and conservative assertions remain. Negative-use tables have working unmodified positive controls.

## Retained probe instrumentation correction

The first native attempt wrongly named explicit `.bind` wrappers as unbound `readN_make` / `wrapN_make` descriptors. Exact-base emitted C uses `readN_bound_u0020make_` / `wrapN_bound_u0020make_`. This is a scratch probe assertion error, not a candidate defect. The scratch GC counter was also corrected to count unique completed collection numbers. The original script and every first-attempt stream remain under `d211-review/native-attempt1/`; corrected full ten-case replay exits0. No production/test hash changed.

The isolated worktree has empty third-party submodule directories. Rather than trigger parallel runtime compilation outside the one-slot allocation, the reviewer reused the parent's unchanged cached Release runtime metadata with archive hashes, serial clang, `GOMAXPROCS=1`, Go `-p1/-parallel1`, `PTS_THREADS=1` and `GC_MARKERS=1`. Runtime metadata, ordered archives, includes, build commands and environment are retained. No runtime/library edit or rebuild is claimed.

## Parent independent verification

Parent inspected and copied both reviewer-authored Go probe files, then independently ran exact-base/current production overlays: **169 pass / 14 expected positive-proof failures → 183 pass**, empty stderr, no build failures. Parent separately inspected the native inputs/generator and used a trusted parent runner, without executing the child generator: **18** base/current natives for the nine independent inputs match explicit independently derived stdout oracles and strict-ESM Node, with exact permuted target sets and class registrations. Both churn executions observe **nine GC collections**. The supplied fixture was independently verified separately on both compilers, all20 lines.

After final review completion, parent reconciled the eight reviewed hashes, both source-probe files, all nine native inputs and supplied fixture byte for byte with the already executed parent copies. No changed or additional final probe required a new compiler execution. Parent's initial packaging checks used the wrong input directory and then ambiguous suffix-based manifest lookup; corrected exact paths validate all363 manifest entries. Those are packaging assumptions, not probe/compiler failures; retained in parent validation receipt.

## Archive and replay

`h09b5c2b2b2b2b2a-review-probes.tar.gz`: **365,611 bytes / 364 UTF-8 relative regular-file members**, SHA-256:

```text
6d623eb1c2900e48e836f5fbaa745dce7ecc0c8b0cdec11808a34d5b8460f419
```

Parent independently verifies digest, all363 source/raw manifest entries, member types, relative paths, UTF-8 contents and absence of links/traversal/duplicates/binary contents before copying. No archive extraction or blindly executed child script is needed.

Key archive paths:

- `d211-review/replay.json`: exact base, final candidate hashes, command argv/cwd/environment, expected exits, build/replay instructions and caveats.
- `d211-review/archive-file-hashes.json`: per-file SHA-256 inventory.
- `d211-review/d211_independent_review_test.go`, `d211_origin_review_test.go`: independently drafted source guards.
- `d211-review/native-manifest.json`, `native-commands.json`, `native-results.json`: inputs, exact emission/clang/native/Node invocations, stdout oracles, target/registration/wrapper sets and GC observations.
- `d211-review/native-attempt1/`: original failed instrumentation and streams.
- `d211-review/runtime.json`, `runtime-archive-hashes.json`: unchanged cached Release inputs.

Replay must consistently remap the archived absolute worktree prefix in overlays/metadata/argv. Inspect sources/scripts first; run Python with `-I`. Do not run the original first-attempt instrumentation as if it were the corrected gate.

Reviewer ran no full suite, CI, sanitizer or parallel-race stress. Finite capture/GC tests do not establish absence of races or collection bugs. No parent-worktree edit, commit, task tick or report Markdown was delegated. The reviewer explicitly released their heavy slot before the parent's frozen four-job full suite. Final full-suite and delivery acceptance remain parent responsibilities.
