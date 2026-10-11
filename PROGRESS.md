# ParallelScript progress

Read this first every session, then `docs/MASTER_PLAN.md`, `docs/DECISIONS.md` and the relevant parts of `docs/SPEC.md`.

## Current state

- **Milestone:** M0 (Bootstrap)
- **Next task:** M0.7 CI
- **Fork base:** `typescript/v7.0.2` (see `docs/FORK.md`)
- **Main is green:** yes (upstream build and the front-end package tests pass)

## Done

- M0.6 Test harness: corpus in `pts/tests/run` and `pts/tests/errors` with directive comments (format in `pts/tests/README.md`, D-009); `TestRun`, `TestRunMatchesNode` (Node differential) and `TestErrors` in `go test ./pts/tests/`; root `Makefile` with `build`, `test`, `test-diff`, `test-upstream`, `bench`.
- M0.5 Pipeline end to end: `pts/lib` (embedded prelude), `pts/frontend.Load`, `pts/diagnostic`, `pts/ir`, `pts/lower`, `pts/cgen` (C with `#line`), `pts/driver`, and the `cmd/pts` CLI (`build`, `run`, `emit-c`). Hello world compiles and runs; `pts run` takes about 0.2 s. Pipeline notes in SPEC §9.2; decisions D-005 to D-008.
- M0.1 Memory files: `docs/SPEC.md`, `docs/DECISIONS.md`, `docs/FORK.md`, `PROGRESS.md`, `docs/BENCHMARKS.md`, `.gitattributes` entry for `.pts`.
- M0.4 Runtime skeleton: `pts/runtime` (C11: values, strings, number formatting, `console.log` of primitives, panics), bdwgc v8.2.12 and libuv v1.53.0 submodules, `pts/toolchain` (cached clang builds, D-003; layout in SPEC §9.1).
- M0.3 `.pts` in the front end: `// PTS:` edits in `tspath`, `core`, `ast`, `module` (D-001, ledger in `docs/FORK.md`); tests in `pts/frontend/pts_extension_test.go`.
- M0.2 Study notes: `docs/agent/NOTES-tsgo.md` (where the front end lives, building a Program, the `.pts` touch points, upstream suites) and `docs/agent/NOTES-rayon.md` (join, Chase–Lev, adaptive splitting, helping while waiting).

## Next

1. M0.7 CI: `.github/workflows/pts.yml` (install clang, `git submodule update --init pts/third_party`, `make test` with `PTS_REQUIRE_NODE=1`, `make test-upstream`); limit upstream workflows; note in `docs/FORK.md`.
2. M0.8 Milestone M0.
3. M1.1 Numbers.

## Build and test commands

Run from the repo root. Go, Node, Bun and clang are on PATH (see `CLAUDE.md` → Environment).
A fresh clone needs the vendored C libraries: `git submodule update --init pts/third_party` (don't init `_submodules/TypeScript`; it's huge and only upstream's `TestSubmodule` uses it).
Built C archives are cached in `~/.cache/pts/` (override with `PTS_CACHE_DIR`); deleting it is always safe.

| What | Command | Baseline (2026-10-11) |
|---|---|---|
| Build everything | `go build ./...` | ok, about 3 min cold, seconds warm |
| Front-end upstream tests | `go test ./internal/tspath/... ./internal/module/... ./internal/core/... ./internal/parser/... ./internal/scanner/...` | 5/5 packages ok |
| PTS Go tests (front end, lowering, C emitter, driver, runtime C unit tests under ASan/UBSan, number-format differential) and the CLI | `go test ./pts/... ./cmd/pts/` | ok (about 10 s with a cold cache) |
| Build the CLI | `make build` (= `go build -o pts-bin/pts ./cmd/pts`; `pts-bin/` is ignored), then `pts-bin/pts run file.pts`. A copy outside the repo needs `PTS_HOME` set to the repo root. | ok |
| Corpus only | `go test ./pts/tests/` (run, differential and error tests); `make test-diff` for the Node differential tests alone | ok (7 run tests, 1 skipped as a known issue; 6 error tests) |
| All PTS tests | `make test` (= `go test ./pts/... ./cmd/pts/`) | ok |
| Touched upstream suites | `make test-upstream` | ok (see the rows above) |
| Upstream suites the fork touches | `go test ./internal/tspath/... ./internal/core/... ./internal/ast/... ./internal/module/... ./internal/compiler/... ./internal/tsoptions/... ./internal/checker/... ./internal/modulespecifiers/... ./internal/parser/... ./internal/scanner/... ./internal/binder/...` | ok |
| Upstream CLI/LSP suites | `go test ./internal/execute/... ./internal/project/... ./internal/ls/... ./internal/lsp/...` (slow, several minutes) | ok except the one pre-existing failure under Known issues |
| Local compiler baselines | `go test ./internal/testrunner/ -run TestLocal` | ok (about 324 subtests; the TypeScript submodule is not checked out, so `TestSubmodule` skips) |

`make bench` prints a placeholder until `pts/bench/run.sh` arrives with M1.5.

## Known issues

- **Lone surrogates in string literals** (`"\uD800"`) reach the runtime as WTF-8 bytes and `console.log` writes them raw; Node writes U+FFFD. Fix with the string work in M1.3. Reproduced by the skipped test `pts/tests/run/lone_surrogate.pts`.
- **Upstream, pre-existing:** `go test ./internal/execute/tsctests/ -run TestTscCommandline/tsc/show_help_with_ExitStatus.DiagnosticsPresent_OutputsSkipped` fails at the fork base too (verified with the PTS edits stashed). The help header's padding depends on the real version string, which changed on the 7.1 nightly branch after the baseline was written. Not ours; CI excludes it (M0.7).

## Needs from owner

(none)

## Session log

- 2026-10-11: M0.6 done.
- 2026-10-11: M0.5 done. Plan change D-008: M1.4 split into M1.4a (control flow) and a new M1.4b (modules).
- 2026-10-11: M0.4 done.
- 2026-10-11: M0.3 done (two tsc baselines re-accepted for the new `.pts` entry in TS6231).
- 2026-10-11: M0.2 done.
- 2026-10-11: M0.1 done. Baseline: build ok; front-end package tests 5/5 ok.
