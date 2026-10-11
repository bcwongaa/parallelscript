# Fork ledger

ParallelScript lives inside a fork of [microsoft/typescript-go](https://github.com/microsoft/typescript-go).

- **Base:** tag `typescript/v7.0.2` (remote `upstream`). Never push to `upstream`.
- **New code** lives in new directories and needs no ledger entry: `cmd/pts/`, `pts/`, `docs/` additions, `examples/`, `tools/agent/`, `CLAUDE.md`, `PROGRESS.md`, `Makefile`.
- **Upstream modifications** are minimal, marked with a `// PTS:` comment at the edit site (`# PTS:` in YAML and shell), and listed below. `git grep -n "PTS:" -- ':!pts' ':!cmd/pts' ':!docs'` must match this table.

## Rebasing onto a newer TypeScript release

1. `git fetch upstream --tags` and pick the new `typescript/v7.x.y` tag.
2. Merge the tag into `main` (never rebase published history; never force-push).
3. Resolve conflicts at the `// PTS:` sites listed below, re-run the touched packages' Go tests and the PTS suite.
4. Update the base tag above and log the bump in `docs/DECISIONS.md`.

## Upstream modifications

| File | What changed and why | Task | Commit |
|---|---|---|---|
| `.gitattributes` | `*.ps linguist-language=TypeScript`, so GitHub highlights `.ps` sources as TS (D-O01). | M0.1 | (this commit) |
| `.gitignore` | A `# PTS:` section: ignore `.venv/`, `__pycache__/`, root and `pts/**` build dirs, `*.env`, the `pts` binary; re-include `pts/tests/**/*.out`, which upstream's `*.out` rule would hide. | M0.1 | (this commit) |

## Disabled or limited upstream workflows

(Filled in by M0.7.)
