# H09b5c2b1 — isolated reproducer handoff

Date: 2026-10-22. Named `reproducer` agent `a7468a53b04a6775a`, inherited current `gpt-6.1-sol` runtime/xhigh configuration. Exact source base `9e60deebfd6a6b26d24224fdbe9753750ab8e6f0`; isolated worktree `agent-a7468a53b04a6775a`. The child wrote only the three assigned new test files, no production/runtime/docs/instruction changes, commits, pushes or task ticks. Its heavy slot was released before the parent's final suite.

## Current RED, historical controls

| Exact-base case | Build | Node | Native | Meaning |
|---|---:|---:|---:|---|
| Unchanged historical C251 | 0 | 0 | 0 | Both print `by name 5`; not a fresh RED |
| Fresh containing-value pairing | 0 | 0 | 1 | Node `by name 1 0`; native TypeError |
| Generic-class containing owner | 0 | 0 | 1 | Same genuine divergence |
| Shared own/value roots, before/after placement | 0 | 0 | 0 | Node matches; retain later-fallback availability |
| Generic shared-own root | 0 | 0 | 0 | Node match |
| Unknown-value-owner historical variant | 0 | 0 | 0 | Both print `by name 5` |
| Exploratory containing-site program | 0 | 0 | 0 | **Not a current source-language site RED** |

The new fixture places the property-sourced value demand inside `Bad.make<Dog[]>`, an instance only AllInstances makes. Main's escaping poison arrow is outside that method, so the already-existing outer-value alternative cannot incidentally solve containment. The compatible poison instance creates a delayed `Weird<Dog[]>` method-body failure. A needless whole-program fallback then loses the unrelated interface-method instance:

```text
Uncaught TypeError: Expected Box<Dog[]>, got Box<any>
```

Corrected comparisons use strict-ESM `.mts` and Node `--no-warnings --experimental-transform-types`. The isolated fresh base compiler reads parent runtime inputs through `PTS_HOME`; initial missing-transform/missing-Ryu-checkout attempts are retained separately as setup failures, not semantic evidence.

## Final public-API tests

```sh
GOMAXPROCS=1 PTS_REQUIRE_NODE=1 go -C <isolated-worktree> test -count=1 -json -p 1 -parallel 1 -run '^TestContainingCode' -timeout 10m ./pts/lower ./pts/driver
```

Actual exit **1**, **9 fail / 9 pass events**, no build failures:

- Six failing children: plain/generic variants of the lower cap assertion and two driver seams, plus their three failed parent containers.
- Eight passing children: plain/generic owners, own/value shared roots and before/after placement, plus their passing parent container.
- The lower cap assertion is nonfatal so the independent clean ValueInstances and explicitly narrowed AllInstances checks execute and pass even in RED. Expected cap truth is not weakened.
- Narrowed-driver tests fail on the delayed `Dog[]` → `Animal[]` PTS1001. Ordinary-driver tests emit C but lack `pts_method_instance_find`.

Textual module order is placement coverage, not by itself proof of late descriptor discovery. Parent graph tests directly exercise late own/ValueInstances hits. No current source-language site RED was established; source site controls and direct graph/site guards cover the implemented registration behavior without inventing a failure.

Frozen final hashes, independently matched in the parent and the archived copies:

| File | SHA-256 |
|---|---|
| `pts/lower/containing_code_caps_test.go` | `9223a1d524900c5a79d85cb7378bdb97bc4dc1e0d6f4b283bb6a4d4eaf4195d0` |
| `pts/driver/containing_code_caps_test.go` | `83f2c68e6214e332c6ba461176f0da5616654f00e9e20de7c6fcb03c66d21559` |
| `pts/tests/run/generic_containing_value_pairing.pts` | `431b6a813280dc48bc0dcb8912aa225d8edd81f0c011619b68ddeb494e832d6e` |

## Retained artifact

`h09b5c2b1-reproducer-probes.tar.gz`: **22,950 bytes**, **129 regular files / 140 entries**, SHA-256 `6b4e1798e658fb69f5c22fb6c522f2aab31c2b1c2e8f4bf117a6efd5f695b01d`. Contains final sources/patches, manifest, native receipts, strict-ESM runner, exact historical inputs/outputs and final focused RED JSONL. No absolute/traversal members, links, CLI/native binaries or nested archives. Parent verified the digest, safely extracted to a new empty directory, inspected the programs and matched all three final sources; archive scripts were not executed from extraction.

The child independently reproduced only exact-base RED/controls; it did **not** execute the parent's implementation GREEN. Parent execution and scoped independent review are recorded separately in [slice evidence](h09b5c2b1.md) and [review](h09b5c2b1-review.md). No broader source-flow work or retry/replacement acceptance of the provider-refused H09b5b assignment occurred.
