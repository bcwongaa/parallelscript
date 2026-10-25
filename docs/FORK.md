# Fork ledger

ParallelScript lives inside a fork of [microsoft/typescript-go](https://github.com/microsoft/typescript-go).

- **Base:** tag `typescript/v7.0.2` (remote `upstream`). Never push to `upstream`.
- **New code** lives in new directories or files and needs no ledger entry: `cmd/pts/`, `pts/`, `docs/` additions, `examples/`, `tools/agent/`, `CLAUDE.md`, `PROGRESS.md`, `Makefile`, `.github/workflows/pts.yml`, `.github/workflows/pts-nightly.yml`.
- **Upstream modifications** are minimal, marked with a `// PTS:` comment at the edit site (`# PTS:` in YAML, shell and git's config files; `/* PTS */` inside a Go expression), and listed below. `git grep -n "PTS:" -- ':!pts' ':!cmd/pts' ':!docs'` must match this table, apart from `CLAUDE.md` and `PROGRESS.md` (new files that mention the marker). `README.md` is replaced whole, so it carries no marker.

## Rebasing onto a newer TypeScript release

1. `git fetch upstream --tags` and pick the new `typescript/v7.x.y` tag.
2. Merge the tag into `main` (never rebase published history; never force-push).
3. Resolve conflicts at the `// PTS:` sites listed below, re-run the touched packages' Go tests and the PTS suite.
4. Update the base tag above and log the bump in `docs/DECISIONS.md`.

## Upstream modifications

| File | What changed and why | Task | Commit |
|---|---|---|---|
| `.gitattributes` | `*.pts linguist-language=TypeScript`, so GitHub highlights `.pts` sources as TS (D-O01; `.ps` until D-004). | M0.1 | 3441372, 0bf8d50 |
| `internal/tspath/extension.go` | `ExtensionPts`; `.pts` added to the TS extension lists (its own group in the grouped lists), `ExtensionIsTs`, `extensionsToRemove` and `GetDeclarationEmitExtensionForPath` (D-001, D-004). | M0.3 | 139254b, 0bf8d50 |
| `internal/core/core.go` | `GetScriptKindFromFileName`: `.pts` → `ScriptKindTS`. | M0.3 | 139254b, 0bf8d50 |
| `internal/ast/utilities.go` | `GetImpliedNodeFormatForFile`: `.pts` like `.ts`. | M0.3 | 139254b, 0bf8d50 |
| `internal/module/resolver.go` | `tryAddingExtensions`: explicit `.pts` case; extensionless imports try `.pts` first via the new untraced `tryPtsExtension`; `extensionIsOk` accepts `.pts` (D-001, D-004). | M0.3 | 139254b, 0bf8d50 |
| `internal/module/util.go` | `GetResolutionDiagnostic` allows `.pts`; `TryGetJSExtensionForFile` maps `.pts` → `.js`. | M0.3 | 139254b, 0bf8d50 |
| `testdata/baselines/reference/tsc/commandLine/extensionless-file-in-tsconfig-does-not-exist.js`, `…/extensionless-file-in-extended-tsconfig-in-different-folder-does-not-exist.js` | Accepted new baselines: TS6231 lists the extensions tried, which now end with `'.pts'`. On an upstream merge, regenerate rather than hand-merge. | M0.3 | 139254b, 0bf8d50 |
| `.gitmodules` | Pinned submodules `pts/third_party/bdwgc` (v8.2.12) and `pts/third_party/libuv` (v1.53.0). Upstream's `_submodules/TypeScript` entry is unchanged. | M0.4 | 26476ae |
| `.gitmodules` | Pinned submodule `pts/third_party/ryu` (v2.0, D-011). | M1.1 | 554c989 |
| `.gitmodules` | Pinned submodule `pts/third_party/libbacktrace` (commit 0b9b49c; it has no release tags, D-069). | M3.2b | d0b802bb0 |
| `.gitmodules` | Pinned submodule `pts/third_party/pcre2` (pcre2-10.49; its own `deps/sljit` submodule is not checked out, as the JIT is off, D-132). | M6.1a | 1d51bd14f |
| `.github/workflows/ci.yml` | `on:` reduced to `workflow_dispatch`: upstream's CI (hereby build, full test matrix, release build, extension, lint) runs only on demand. See below. | M0.7 | 6ee609b |
| `.github/dependabot.yml` | `open-pull-requests-limit: 0` for both ecosystems: no version-update PRs (the fork commits straight to `main`, D-O02). | M0.7 | 6ee609b |
| `internal/checker/pts.go` (new file) | ParallelScript's numeric types (D-048): `ptsNumericAliasType` makes the prelude's `int`, `i8 … u32`, `u64` and `f32` aliases intrinsic number types of their own; `ptsArithmeticType`, `ptsUnaryType` and `ptsNumericType` type arithmetic by `pts/numeric`'s rules. Imports `pts/lib` and `pts/numeric` (neither imports the checker). Inactive unless the PTS prelude is loaded. | M2.6a | 5ebc5408e |
| `internal/checker/pts.go` | Exports for monomorphization (D-049): `PtsTypeArgumentsOfSignature` (a resolved signature's type arguments) and `PtsInstantiateType`. | M2.6b1 | 849ee7586 |
| `internal/checker/pts.go` | Export for generic function values (D-078): `PtsInstantiateSignatureInContextOf` (a generic signature instantiated by inference from a non-generic contextual one). | M3.3d1 | fde182377 |
| `internal/checker/pts.go` | Export for generic calls whose result goes to a union of object types (D-095): `PtsInferTypeArgumentsFromResult` (a generic signature's type arguments inferred from its return type and a target type alone). | M3.7b | efc40f2eb |
| `internal/checker/pts.go` | Exports for `?.` calls of generic functions and methods (D-193): `PtsWithoutCallChain` (a call's resolved signature without the call-chain flags tsc adds in an optional chain, a generic call's re-instantiated from its unflagged candidate) and `PtsIsOuterCallChain` (whether it is the outermost call of its chain). | H09b3a | 4d1b1f450 |
| `internal/checker/pts.go` | Export for code that an instance's type arguments make dead (D-195): `PtsIntersectionType` (the intersection of types as tsc forms it) and `PtsReducedType` (a type as tsc reduces it where used: never for conflicting discriminants). | H09b4a | 0377fb2eb |
| `internal/checker/pts.go` | Export for tests of what a stale narrowing's place holds (D-197): `PtsTypePredicateParameterIndex` (the parameter a signature's type predicate, `x is T` or `asserts x is T`, narrows; -1 for none or `this`). | H09b4b2 | 441b2d300 |
| `internal/checker/pts.go` | Export for code tsc narrows to no value (D-197): `PtsFlowTypeOfReference` (control flow analysis's type of a reference starting from another declared type: what the tests leave of an enum's or literal union's representation). | H09b4b2 | 8023de8e1 |
| `internal/checker/pts.go` | Export for loose locals decided once for every instance of generic code (D-222): `PtsCouldContainTypeVariables` (whether a type may mention a type parameter: a type parameter, an indexed access, a mapped type over one, a generic object type). | H12 | a28d60dcd |
| `internal/checker/pts.go` | Export for `instanceof` narrowing to a generic class (D-218): `PtsInferInstanceTypeArguments` (the instance of the class that tsc's `C<any, …>` stands for, with the type arguments tsc infers from the type its object is held as: through the class's `extends` and `implements` clauses, then structurally, per member of a union) and its helpers `ptsInferInstance`, `ptsHeritageTypes` (unique-instance rules: review rounds 1–2). | H10a | f54f8b5c1, 105354671 |
| `internal/checker/checker.go` | Calls into `pts.go`: `getDeclaredTypeOfTypeAlias` (`ptsNumericAliasType`), `checkPrefixUnaryExpression` for `+x` and `getUnaryResultType` (`ptsUnaryType`), the arithmetic and `+` cases of `checkBinaryLikeExpression` (`ptsArithmeticType`). | M2.6a | 5ebc5408e |
| `internal/checker/nodebuilderimpl.go` | `typeToTypeNode`: a number type other than `number` itself (one of D-048's) prints as a reference to its name. | M2.6a | 5ebc5408e |
| `internal/ast/nodeflags.go` | `NodeFlagsPtsParallel` (bit 29): marks the call node of ParallelScript's `parallel e` and a `parallel for` statement (D-120). | M5.1 | 1ba3a57e7 |
| `internal/parser/parser.go` | Hooks into `internal/parser/pts.go` (new file): `parseStatement` tries `parallel for (…)` first; `parseUnaryExpressionOrHigher` and `parseSimpleUnaryExpression` try `parallel e` before an update expression (D-O05, D-120). | M5.1 | 1ba3a57e7 |
| `internal/parser/pts.go`, `internal/parser/pts_test.go` (new files) | The contextual keyword: `parallel e` parses as a call of `parallel` marked `NodeFlagsPtsParallel` (operand: a unary expression, or an arrow function), `parallel for` as the for statement marked the same way; every other `parallel` is an identifier. Tests of both forms and of `parallel` as a name. | M5.1 | 1ba3a57e7 |
| `.gitignore` | A `# PTS:` section: ignore `.venv/`, `__pycache__/`, root and `pts/**` build dirs, `*.env`, the `pts` binary (`/cmd/pts/pts`, and `/pts-bin/` where `make build` puts it); the benchmarks' latest results (`/pts/bench/results/latest/`, M6.5a); re-include `pts/tests/**/*.out`, which upstream's `*.out` rule would hide. | M0.1 | 3441372 |
| `README.md` | Replaced by ParallelScript's README; upstream's is kept as `docs/UPSTREAM_README.md`. On an upstream merge, take upstream's changes into `docs/UPSTREAM_README.md`. | M6.1d | f95508bd7 |
| `internal/project/project.go` | `ProgramCommandLine`, a hook that makes the command line a project's program is built from (memoized in the typings cache, so an edit reuses the program); `pts lsp` sets it to the compiler's fixed options plus the prelude as a root (D-168). | M7.1 | 46400426e |
| `internal/ls/diagnostics.go` | `ExtraDiagnostics`, a hook whose diagnostics `ProvideDiagnostics` adds after tsc's; `pts lsp` sets it to ParallelScript's checks (D-168). | M7.1 | 46400426e |
| `internal/ls/lsconv/converters.go`, `internal/lsp/lsproto/lsp.go` | `ptslib:///` paths are URIs as they are, as `bundled:///` ones are, so definitions in the library round-trip (D-168). | M7.1 | 46400426e |
| `internal/project/snapshotfs.go`, `internal/project/watch.go` | `.pts` files' changes are relevant to the project service; `ptslib:///` files aren't watched (D-168). | M7.1 | 46400426e |
| `internal/tsoptions/parsedcommandline.go`, `internal/lsp/server.go` | `pts` in the file globs that match new files of a project and renames (D-168). | M7.1 | 46400426e |
| `internal/lsp/pts_content.go` (new file), `internal/lsp/server.go` | LSP 3.18's `workspace/textDocumentContent` for the URI schemes in `ContentSchemes`, a hook (none in upstream's use; `pts lsp` sets `ptslib`, and `bundled` when TypeScript's libraries are embedded), read from the server's file system; in `server.go`, a line registering the handler and a line declaring the capability (D-169). | M7.2 | 4d3368aa2 |
| `internal/ls/semantictokens.go` | `isPtsParallelOperator`: the keyword of `parallel e`, which parses as a call of `parallel`, gets no semantic token, so editors show it as the grammar's keyword rather than a function (D-169). | M7.2 | 4d3368aa2 |

## Disabled or limited upstream workflows

ParallelScript's gate is `.github/workflows/pts.yml` (M0.7): on every push it builds `pts`, runs `make test` with the Node differential tests required, `make test-upstream` (the upstream packages the fork touches, plus `TestLocal` baselines) and, in a second job, `make test-upstream-cli` (upstream's CLI and LSP suites, minus the one known failure). `.github/workflows/pts-nightly.yml` (M5.7a, D-130) runs the stress programs under `pts/tests/stress/` 100 times a night.

| Workflow | State | Why |
|---|---|---|
| `ci.yml` (CI) | Manual only (`workflow_dispatch`) | Runs about 75 minutes across Windows, macOS and custom `TypeScript-1ES-*` runners, needs the TypeScript submodule and npm, and fails on the known pre-existing upstream failure. Its release job was already limited to `microsoft/typescript-go`. Run it by hand before merging a new upstream tag. |
| `codeql.yml` (Code Scanning) | Unchanged | Its job is already guarded by `if: github.repository == 'microsoft/typescript-go'`, so it is a no-op here. |
| `copilot-setup-steps.yml` | Unchanged | Manual only upstream. |
| `dependabot.yml` | Version-update PRs off | See the ledger above. |
