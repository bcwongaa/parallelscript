# D-215 named reproducer report

Parent-retained report of messages from named `reproducer` agent `a4b9444ae33aa0051`, 2026-10-22. Runtime explicitly reported: inherited gpt-6.1-sol, configured xhigh; no nested agents/skills/provider changes. Writing isolated worktree, test files only; no production/docs commits or pushes. Scope D-215 only, not blocked H09b5b.

## Independent execution

Exact source base `3b3be53a3abdc9dbe35c26aad7ba50bee4943b18`:

```sh
GOMAXPROCS=1 go test -p 1 -parallel 1 -json -count=1 -run '^TestElidedContainer' ./pts/lower ./pts/driver
```

- Original tests: exit1, **166 pass /157 fail events** (lower61/66, driver105/91), empty stderr. There are134 genuine precision/control/budget failing leaves, four unsupported-conversion leaves, nineteen failing aggregate parents.
- Genuine RED examples: leading elision's selected source is nil; direct AllInstances includes excluded `pts_fn_source0_C1(` and `pts_fn_source0_C2(`.
- Independently classifies all four exact `raw_unknown_contract` sources as pre-existing PTS1001 conversions, not target-pair successes. Retains original bytes and streams, then relocates identical source inputs into explicit same-diagnostic controls. No genuine unsafe or pair assertion changes.
- Corrected tests: exit1, **171 pass /153 fail**, empty stderr. All four explicit PTS1001 controls and their parent PASS.
- Adds one direct ordinary-array all-elided guard after corrected RED ends. Valid tree and ordinary-array contract preconditions pass. Exact required work is one pattern entry plus three omitted elements; budgets3/4 must leave0 steps, only4 succeeds, neither calls alias-publication callback. Exact-base guard: exit1, **0 pass /3 fail**, empty stderr. Base leaves1/2 steps, and budget4 remains unknown.
- Heavy worker released explicitly before parent final suite; no further heavy execution.

Final lower test SHA256: `dc8b4ada6ef1b7d9ad0e70c70edd1a56fc7598283a73bf436852971571341912`.
Final reproducer driver test SHA256: `b5434dd8aba5588d9c981395762d186b898a19c722b7bc9fc03c6b6841bb5f72`. Parent uses the same exact sources/assertion obligation with its independently named unsupported test; hashes differ and are not represented as identical files.

## Native inputs

Two independently authored strict-Node `.mts` fixtures exercise preserved receiver state/capture identity and mutation after capture. Both Node executions exit0 with expected six-line stdout; only experimental-transform warnings. Reproducer makes **no independent current-native claim**. Parent separately compiles both original `.pts` files on exact base/current through direct AllInstances/default saved-C APIs and independently compares all eight configurations with strict Node; see parent evidence.

## Retained evidence

Safe archive `h09b5c2b2b2b2b2b2b2a-reproducer-probes.tar.gz`: 26 regular-file entries,125,231 bytes, SHA256 `015b6e980bf5e3a55b10fa2a660882e5e53177742d662ddb65ad0eaf1e87f3a5`. Original/corrected/final tests, incremental correction/budget patches, raw JSONL/stderr/exit receipts, exact commands and `.pts`/`.mts`/Node streams. No recursive capture, symlinks, binary executables, credentials or agent transcript. Parent independently validates archive paths/types/bytes and source results before delivery.

This report is reproduction evidence only; it does not tick a task, supply the independent review gate, certify broader flow, or reopen refused scope.
