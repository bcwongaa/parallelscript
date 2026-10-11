# ParallelScript progress

Read this first every session, then `docs/MASTER_PLAN.md`, `docs/DECISIONS.md` and the relevant parts of `docs/SPEC.md`.

## Current state

- **Milestone:** M0 (Bootstrap)
- **Next task:** M0.2 Study notes (`docs/agent/NOTES-tsgo.md`, `docs/agent/NOTES-rayon.md`)
- **Fork base:** `typescript/v7.0.2` (see `docs/FORK.md`)
- **Main is green:** yes (upstream build and the front-end package tests pass)

## Done

- M0.1 Memory files: `docs/SPEC.md`, `docs/DECISIONS.md`, `docs/FORK.md`, `PROGRESS.md`, `docs/BENCHMARKS.md`, `.gitattributes` entry for `.ps`.

## Next

1. M0.2 Study notes.
2. M0.3 `.ps` in the front end (tspath extension lists, script kind, module resolver).
3. M0.4 Runtime skeleton (C11 runtime, bdwgc and libuv submodules, cached build).

## Build and test commands

Run from the repo root. Go, Node, Bun and clang are on PATH (see `CLAUDE.md` → Environment).

| What | Command | Baseline (2026-10-11) |
|---|---|---|
| Build everything | `go build ./...` | ok, about 3 min cold, seconds warm |
| Front-end upstream tests | `go test ./internal/tspath/... ./internal/module/... ./internal/core/... ./internal/parser/... ./internal/scanner/...` | 5/5 packages ok |

Commands that don't exist yet and arrive with their tasks: `make build`, `make test`, `make test-diff`, `make bench` (M0.6), `go test ./pts/...` (M0.5+), `pts/bench/run.sh` (M1.5).

## Known issues

(none)

## Needs from owner

(none)

## Session log

- 2026-10-11: M0.1 done. Baseline: build ok; front-end package tests 5/5 ok.
