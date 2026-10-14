# Independent review programs and evidence

Snapshot: `f2323ccf4`, reviewed 2026-10-13. See `../REVIEW-gpt.md` for the verdict and numbered findings. This directory is an **untracked review deliverable**, not a proposed compiler fix. No tracked project files were changed.

## Rerun safely

From **this review checkout**, not the original project or another reviewer's checkout:

```sh
export GOMAXPROCS=2
export PTS_THREADS=4
export PTS_CACHE_DIR="$HOME/.cache/pts-review-gpt"
export PTS_HOME=/home/bryan/projects/pts-review-gpt
python3 -I review-gpt-probes/rerun.py
```

`rerun.py` creates a new `/tmp/pts-review-gpt/rerun-*` directory, copies the inputs there, builds a compiler with `go build -p 1`, and runs programs sequentially. Results and generated binaries stay in that fresh scratch directory; the evidence here is never overwritten. It uses the Node installation at `$HOME/.local/node/bin/node` and sets the review-specific cache and resource caps itself. Servers are local, bounded subprocesses, and are cleaned up by their own process handles. The rerun does **not** repeat the full baseline, extension packaging, or benchmarks.

It covers the final three programs, the twenty targeted probes (HTTP is handled by its own driver), minimized rechecks, HTTP/Node framing comparisons, scripted LSP diagnostics/lifecycle, the five falsely excluded corpus programs, the ten original previous-review repros plus three current regressions and three residuals, the known LRU failure, the guide tutorial, and positive/negative `pts test` cases. The packaged runner was executed successfully in fresh scratch; `validation/` retains its complete log, summary and selected raw results. Both telemetry outputs and all eleven ordinary-TypeScript probe comparisons matched again, while the reported defects reproduced. Helpers honor `PTS_REVIEW_SCRATCH` and `PTS_HOME`; set those only when invoking helpers against a scratch directory containing the compiler and input files. Do not invoke the helpers with this retained-evidence directory as the scratch directory.

**An exit-0 runner or probe is not a correctness verdict.** Some probes catch and print defects. In particular, AbortController's race is observed through nonzero `failed` counts, not through an abnormal process exit. Read the emitted output/JSON and compare it with the report. Expected nonzero commands include:

- Ledger: exit 1 because the fixture contains two rejected rows; Node agrees. A missing option argument exits 2.
- Exact-undefined destructuring: PTS build/run rejects with PTS1001; Node prints 4.
- Typed IteratorResult residual and known LRU: compile/runtime failures described in the report.
- Intentional `review-failure.test.pts`: exit 1 with an AssertionError, demonstrating correct test-runner failure behavior.
- Graceful LSP lifecycle: the recorded session exits 1 with `pts: context canceled`, despite receiving the shutdown response.

Abort failure counts and the LSP lifecycle result can vary with scheduling. A zero-failure concurrent run alone does not invalidate the retained multi-run reproduction.

## Main programs and fixtures

| Files | Purpose and recorded result |
|---|---|
| `ledger-v1.pts`, `ledger.pts`, `ledger.mts` | 221-line CSV expense CLI, unchanged first build; Node-identical text/JSON, 5250 cents, five accepted/two rejected. |
| `task-service-v1.pts`, `task-service-v2.pts`, `task-service-v3.pts`, `task-service.pts` | Initial 204-line HTTP JSON service/client through final 207-line version. Initial callback typing and doubled-slash URL errors were reviewer mistakes; final one/four-thread runs succeed. |
| `telemetry-v1.pts`, `telemetry.pts`, `telemetry.mts` | 206-line parallel NDJSON processor, unchanged first build; 777 valid/four rejected; one/four-thread JSON matches Node byte-for-byte. |
| `expenses.csv`, `expenses-last-quote.csv`, `events.ndjson` | Synthetic inputs, including Unicode, quoted fields, refunds, invalid rows, and mixed C/F readings. |
| `report-1.json`, `report-4.json`, `report-node.json` | Recorded telemetry outputs. |

The `.mts` versions of ledger and telemetry replace `pts:` file/path imports with `node:` imports; telemetry's `.par.map` becomes sequential `.map`. No Node equivalent is claimed for the native HTTP service/synchronization API.

`new-user-run.py` is the original authoring harness, retained for provenance. It generates fixtures and rewrites its own `*-v1.pts` snapshots from the current inputs. **Do not run it over the original evidence**; `rerun.py` deliberately runs the final programs without doing that.

## Targeted probes and findings

`01`–`20` are the original probe set. `probe-run.py` runs nineteen; `http-wire.py` builds/runs `18-http-wire.pts`. Eleven ordinary-TypeScript probes have Node strip-types comparisons. Native-only probes exercise intentional checked-any behavior, integer types, synchronization, parallelism/races, and checked error precedence.

- `abort-local-min.pts`: finding 1, independent-controller construction race; `recheck-results.json` records one-thread and three four-thread repeats.
- `previous/06_residual.pts`: finding 2, getter/any/method snapshots across suspension; `previous/residual_results.jsonl` records the divergent Node output.
- `previous/08_residual.pts`, `known-lru.pts`: finding 3, inline generic specialization and generic instanceof. LRU's exact Node twin needs `--experimental-transform-types`, not strip-only, because of constructor parameter properties.
- `http-wire.py`, `http-framing-compare.py`, `node-http-wire.mjs`: finding 4 and positive HTTP wire cases. Malformed framing tests are bounded and sent only to these scripts' own local servers. Their JSON retains requests and responses; deliberate server termination is cleanup, not a reported crash.
- `destructuring-undefined-min.pts`: finding 5. `04-destructuring.pts` shows the union-annotation workaround; `04-destructuring-original.pts` retains the original failure.
- `excluded-corpus/`, `check-excluded-corpus.py`, `excluded-corpus-results.json`: finding 7; all five falsely excluded programs match Node.
- `lsp-smoke.py`, `lsp-transcript.json`, `lsp-stderr.txt`: finding 8 and diagnostic coverage. The client answers server-origin requests, sends a parameterless exit after shutdown, and leaves stdin open while awaiting process termination. Ordinary TS2322, numeric PTS2002 and guardrail PTS3001 are present; the lowering-only destructuring failure is absent from editor diagnostics.

`initial-probe-results/` preserves first-run outcomes. `08-generics-original.pts` uses a constructor parameter property accepted by PTS but not Node's strip-only mode; the final explicit field version compares correctly. `12-optional-order-original.pts` has an overly broad key type rightly rejected by strict TypeScript. These reviewer/tooling errors are not language findings. The exact-undefined failure is a genuine compiler limitation, not one of those corrections.

`previous/` contains fingerprinted original sources, verification reports and raw/concise outputs for all eleven earlier findings. The original sources are unchanged; the runner was adjusted only to honor scratch/home environment variables. Native binaries, caches, dependencies and extension build artifacts are intentionally excluded.

## Baseline and tooling evidence

`baseline.jsonl` is the complete corrected capped Go test stream; `baseline-summary.json` supplies package/event counts. Parent tests and subtests are separate events, so 1274 pass events do not mean 1274 independent programs. `baseline-aborted.jsonl` is the earlier run stopped when its incorrect skip regex let eight-thread subtests run; do not use it as the baseline.

To repeat the baseline separately, using the environment above and a **new** output file:

```sh
go test -json -p 1 -parallel 2 -timeout 40m \
  -skip='.*/.*/threads=([5-9]|[1-9][0-9]+)$' ./pts/... ./cmd/pts/ \
  > /tmp/pts-review-gpt/baseline-rerun.jsonl
```

This is the correct nested-subtest skip pattern. No full upstream suite was run. High-thread tests are intentionally outside this review's coverage. The original corrected baseline passed ten tested packages, zero failed, with 1274 pass/247 skip result events and 19 no-test packages; corpus execution was 262 pass/38 skip before the five independent comparisons.

`logs/` retains compiler build/timeout, extension packaging, initial/final service, guide/test, recheck, HTTP, LSP and benchmark output. `pts-test-failure.txt` retains the intentional failed assertion. VS Code typechecking, four grammar tests and packaging passed in scratch; GUI installation/completion is unverified. `startup-spot.json` and `fib-spot.json` are only two short `hyperfine -r 3` checks on a shared host, not replacements for published benchmark numbers.

No memory corruption was reproduced. The report's sanitizer/race-assurance item is an explicitly identified coverage/platform gap, not a new crash finding. Non-x86 targets, universal race safety and actual reverse-proxy exploitation were not established by these tests.
