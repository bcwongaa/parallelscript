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
| `.gitattributes` | `*.pts linguist-language=TypeScript`, so GitHub highlights `.pts` sources as TS (D-O01). | M0.1 | 3441372 |
| `internal/tspath/extension.go` | `ExtensionPts`; `.pts` added to the TS extension lists (its own group in the grouped lists), `ExtensionIsTs`, `extensionsToRemove` and `GetDeclarationEmitExtensionForPath` (D-001). | M0.3 | 139254b |
| `internal/core/core.go` | `GetScriptKindFromFileName`: `.pts` → `ScriptKindTS`. | M0.3 | 139254b |
| `internal/ast/utilities.go` | `GetImpliedNodeFormatForFile`: `.pts` like `.ts`. | M0.3 | 139254b |
| `internal/module/resolver.go` | `tryAddingExtensions`: explicit `.pts` case; extensionless imports try `.pts` first via the new untraced `tryPtsExtension`; `extensionIsOk` accepts `.pts` (D-001). | M0.3 | 139254b |
| `internal/module/util.go` | `GetResolutionDiagnostic` allows `.pts`; `TryGetJSExtensionForFile` maps `.pts` → `.js`. | M0.3 | 139254b |
| `testdata/baselines/reference/tsc/commandLine/extensionless-file-in-tsconfig-does-not-exist.js`, `…/extensionless-file-in-extended-tsconfig-in-different-folder-does-not-exist.js` | Accepted new baselines: TS6231 lists the extensions tried, which now end with `'.pts'`. On an upstream merge, regenerate rather than hand-merge. | M0.3 | 139254b |
| `.gitmodules` | Pinned submodules `pts/third_party/bdwgc` (v8.2.12) and `pts/third_party/libuv` (v1.53.0). Upstream's `_submodules/TypeScript` entry is unchanged. | M0.4 | 26476ae |
| `.gitignore` | A `# PTS:` section: ignore `.venv/`, `__pycache__/`, root and `pts/**` build dirs, `*.env`, the `pts` binary; re-include `pts/tests/**/*.out`, which upstream's `*.out` rule would hide. | M0.1 | 3441372 |

## Disabled or limited upstream workflows

(Filled in by M0.7.)
