# Corpus classification and executed baseline

H01a, 2026-10-15; fresh compiler/runtime snapshot `1b13cfd954c0f6e62a250ffaab8f79ce0aa7b377`. The inherited 2026-10-14 baseline at `a35aff6f8` is retained separately with its original provenance. This report separates inventory labels from actual execution. It does not remove skip directives or repair feature detection; that is H18. H01b's independent defect-reproduction scope remains blocked and unchecked; corpus success does not complete H01.

## Inventory (300 programs)

`pts/tests/corpus/report.sh` derives these numbers from `// skip:` directives, not from running the programs:

| Classification | Count |
|---|---:|
| Classified compatible | 267 |
| Intentional differences (`by design`) | 8 |
| Unsupported | 24 |
| Known bug | 1 |

The script's historical label `Node-identical` means **classified compatible**, not that it just executed that many Node comparisons. Its stronger execution-derived reporting is an H18 acceptance item.

## Fresh execution

`PATH="$HOME/.local/node/bin:$PATH" GOMAXPROCS=2 PTS_REQUIRE_NODE=1 go test -count=1 -json -p 2 -parallel 2 -timeout 40m ./pts/... ./cmd/pts/` passed (exit 0). Within `TestCorpusMatchesNode`, there were **262 passing comparisons, 38 skips, 0 failures**, excluding the parent test event from the comparison count. Of those skips, 33 carry classification directives; five are additional false positives in the raw-text PTS-feature detector.

The parent independently built and ran each false positive and compared stdout and exit code with Node 24.21.0 (`--no-warnings --experimental-transform-types`):

| Program | PTS exit | Node exit | Exact stdout match |
|---|---:|---:|---|
| `functional/debounce_throttle_clock.pts` | 0 | 0 | yes |
| `algorithms/lcs_diff.pts` | 0 | 0 | yes |
| `text/word_search_puzzle.pts` | 0 | 0 | yes |
| `text/spell_suggest.pts` | 0 | 0 | yes |
| `oop/memento_game_save.pts` | 0 | 0 | yes |

Thus H01a independently verified **267 compatible programs** (262 in the suite plus five manual comparisons), while the default suite still enforces only 262. The 33 classified divergences were not all re-executed in H01a; their labels are not fresh confirmation of their current diagnostics/results. H18 must execute their documented outcomes and fail on crashes.

Current commands and retained outputs: [H01 evidence](../../../docs/agent/hardening-evidence/h01.md), [baseline counts and per-path outcomes](../../../docs/agent/hardening-evidence/h01a-baseline.json), [raw baseline events](../../../docs/agent/hardening-evidence/h01a-baseline.jsonl.gz), and [additional comparisons](../../../docs/agent/hardening-evidence/h01a-additional-comparisons.json). Historical records remain in `h01-baseline.json` and `h01-additional-comparisons.json`, not relabeled as this run. Final all-probe/classification acceptance remains H18/H34; no skipped or blocked scope is declared finished here.
