# Fork ledger

ParallelScript lives inside a fork of [microsoft/typescript-go](https://github.com/microsoft/typescript-go).

- **Base:** tag `typescript/v7.0.2` (remote `upstream`). Never push to `upstream`.
- **New code** lives in new directories or files and needs no ledger entry: `cmd/pts/`, `pts/`, `docs/` additions, `examples/`, `tools/agent/`, `CLAUDE.md`, `PROGRESS.md`, `Makefile`, `.github/workflows/pts.yml`.
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
| `.gitmodules` | Pinned submodule `pts/third_party/ryu` (v2.0, D-011). | M1.1 | 554c989 |
| `.gitmodules` | Pinned submodule `pts/third_party/libbacktrace` (commit 0b9b49c; it has no release tags, D-069). | M3.2b | d0b802bb0 |
| `.github/workflows/ci.yml` | `on:` reduced to `workflow_dispatch`: upstream's CI (hereby build, full test matrix, release build, extension, lint) runs only on demand. See below. | M0.7 | 6ee609b |
| `.github/dependabot.yml` | `open-pull-requests-limit: 0` for both ecosystems: no version-update PRs (the fork commits straight to `main`, D-O02). | M0.7 | 6ee609b |
| `internal/checker/pts.go` (new file) | ParallelScript's numeric types (D-048): `ptsNumericAliasType` makes the prelude's `int`, `i8 … u32`, `u64` and `f32` aliases intrinsic number types of their own; `ptsArithmeticType`, `ptsUnaryType` and `ptsNumericType` type arithmetic by `pts/numeric`'s rules. Imports `pts/lib` and `pts/numeric` (neither imports the checker). Inactive unless the PTS prelude is loaded. | M2.6a | 5ebc5408e |
| `internal/checker/pts.go` | Exports for monomorphization (D-049): `PtsTypeArgumentsOfSignature` (a resolved signature's type arguments) and `PtsInstantiateType`. | M2.6b1 | 849ee7586 |
| `internal/checker/pts.go` | Export for generic function values (D-078): `PtsInstantiateSignatureInContextOf` (a generic signature instantiated by inference from a non-generic contextual one). | M3.3d1 | fde182377 |
| `internal/checker/checker.go` | Calls into `pts.go`: `getDeclaredTypeOfTypeAlias` (`ptsNumericAliasType`), `checkPrefixUnaryExpression` for `+x` and `getUnaryResultType` (`ptsUnaryType`), the arithmetic and `+` cases of `checkBinaryLikeExpressionWorker` (`ptsArithmeticType`). | M2.6a | 5ebc5408e |
| `internal/checker/nodebuilderimpl.go` | `typeToTypeNode`: a number type other than `number` itself (one of D-048's) prints as a reference to its name. | M2.6a | 5ebc5408e |
| `.gitignore` | A `# PTS:` section: ignore `.venv/`, `__pycache__/`, root and `pts/**` build dirs, `*.env`, the `pts` binary; re-include `pts/tests/**/*.out`, which upstream's `*.out` rule would hide. | M0.1 | 3441372 |

## Disabled or limited upstream workflows

ParallelScript's gate is `.github/workflows/pts.yml` (M0.7): on every push it builds `pts`, runs `make test` with the Node differential tests required, `make test-upstream` (the upstream packages the fork touches, plus `TestLocal` baselines) and, in a second job, `make test-upstream-cli` (upstream's CLI and LSP suites, minus the one known failure).

| Workflow | State | Why |
|---|---|---|
| `ci.yml` (CI) | Manual only (`workflow_dispatch`) | Runs about 75 minutes across Windows, macOS and custom `TypeScript-1ES-*` runners, needs the TypeScript submodule and npm, and fails on the known pre-existing upstream failure. Its release job was already limited to `microsoft/typescript-go`. Run it by hand before merging a new upstream tag. |
| `codeql.yml` (Code Scanning) | Unchanged | Its job is already guarded by `if: github.repository == 'microsoft/typescript-go'`, so it is a no-op here. |
| `copilot-setup-steps.yml` | Unchanged | Manual only upstream. |
| `dependabot.yml` | Version-update PRs off | See the ledger above. |
