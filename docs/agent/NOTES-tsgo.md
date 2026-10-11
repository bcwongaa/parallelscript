# Study notes: typescript-go internals (fork base `typescript/v7.0.2`)

Module path: `github.com/microsoft/typescript-go`. `internal/` packages can only be imported from inside this module, so PTS code lives in `cmd/pts/` and `pts/` within the same module.

## 1. Where things live

| Piece | Package | Entry points |
|---|---|---|
| Scanner | `internal/scanner` | `NewScanner()`, `SetText`, `Scan() ast.Kind`; position helpers (§5) |
| Parser | `internal/parser` | `ParseSourceFile(opts ast.SourceFileParseOptions, text string, kind core.ScriptKind) *ast.SourceFile` (parser.go:137). Panics on `core.ScriptKindUnknown`. |
| Binder | `internal/binder` | `BindSourceFile(file)` |
| Checker | `internal/checker` | `type Checker`, `NewChecker(program, tracer)` (checker.go:902) |
| Program | `internal/compiler` | `NewProgram(ProgramOptions{Host, Config, SingleThreaded, …})` (program.go:274) |
| Host | `internal/compiler/host.go` | `NewCompilerHost(cwd, fs, libPath, extendedConfigCache, trace)`, `NewCachedFSCompilerHost` |
| Config | `internal/tsoptions` | `GetParsedCommandLineOfConfigFile`, `ParsedCommandLine`, `core.CompilerOptions` |
| CLI | `cmd/tsgo` → `internal/execute` | `execute.CommandLine(ctx, sys, args, nil)`; compile path in `internal/execute/tsc.go` |
| LSP | `internal/lsp`, `internal/project`, `internal/ls` | `lsp.NewServer`, `Server.Run`; started from `cmd/tsgo/lsp.go` |
| Lib `.d.ts` | `internal/bundled` | `bundled.LibPath()` = `"bundled:///libs"`, `bundled.WrapFS(fs)` serves embedded libs |
| JS numbers | `internal/jsnum` | `jsnum.Number.String()` is exact JS number-to-string in Go: an oracle for M1.1 tests and constant folding |

## 2. Building a Program and getting types

```go
fs := bundled.WrapFS(osvfs.FS())                 // tests: vfstest.FromMap(map[string]string{...}, false)
host := compiler.NewCompilerHost(cwd, fs, bundled.LibPath(), nil, nil)
// Without a tsconfig:
cfg := &tsoptions.ParsedCommandLine{ParsedConfig: &core.ParsedOptions{
    FileNames: roots, CompilerOptions: &core.CompilerOptions{ /* Strict, Target, ... */ }}}
// With one: tsoptions.GetParsedCommandLineOfConfigFile(path, &core.CompilerOptions{}, nil, host, nil)
p := compiler.NewProgram(compiler.ProgramOptions{Config: cfg, Host: host})

sf  := p.GetSourceFile("/p/a.ps")                // or p.GetSourceFiles()
syn := p.GetSyntacticDiagnostics(ctx, sf)        // nil file = all files
sem := p.GetSemanticDiagnostics(ctx, sf)         // also GetBindDiagnostics, GetGlobalDiagnostics, GetProgramDiagnostics

c, done := p.GetTypeChecker(ctx); defer done()   // (*checker.Checker, release func)
t := c.GetTypeAtLocation(node)                   // *checker.Type
s := c.TypeToString(t)
sym := c.GetSymbolAtLocation(node)               // also GetTypeOfSymbol, SymbolToString
```

- Never mix types from different checkers. `GetTypeCheckerForFile` and `GetTypeCheckerForFileExclusive` also exist.
- Diagnostics print with `diagnosticwriter.FormatDiagnosticsWithColorAndContext`, or use `d.String()`, `d.Code()`, `d.Pos()`.
- Working examples: `internal/checker/checker_test.go`, `cmd/tsgo/sys.go`.

## 3. Lib files and the PTS prelude

- Embedded libs are in `internal/bundled/libs/` (108 files). `libs_generated.go` and `embed_generated.go` are generated from the TypeScript submodule (which is not checked out here, so don't regenerate).
- `fileloader.go:157` (`processAllProgramFiles`) loads `GetDefaultLibFileName` when `Lib == nil`, else `GetLibFileName` names from `LibMap` (unknown names are silently ignored).
- **Prelude options:** the simplest is appending `prelude.d.ts` to the root `FileNames` (no upstream edit). Making it a real lib means registering it in `LibMap` plus the generated files. `LibReplacement` and `Types`/`typeRoots` are lighter alternatives.

## 4. File extensions (everything `.ps` touches)

- `internal/tspath/extension.go`: a new `ExtensionPs`; add it to `SupportedTSExtensions` (tsconfig `include`), `SupportedTSExtensionsFlat` (`HasTSFileExtension`), `SupportedTSImplementationExtensions`, `supportedTSExtensionsForExtractExtension`, `AllSupportedExtensions`, `ExtensionIsTs`, `GetDeclarationEmitExtensionForPath`, and **`extensionsToRemove`** (drives `RemoveFileExtension`, `TryGetExtensionFromPath` and `ChangeExtension`; without it emit could overwrite the source).
- `internal/core/core.go` `GetScriptKindFromFileName`: `.ps` → `ScriptKindTS`; otherwise the parser panics.
- Root files: `tsoptions.GetSupportedExtensions` → `getFileNamesFromConfigSpecs` → `vfsmatch.ReadDirectory`. `extraFileExtensions` is forced empty (tsconfigparsing.go:1676), so editing `tspath` is the way. `fileLoader.isSupportedExtension` (fileloader.go:378) rejects unknown extensions.
- Imports, `internal/module/resolver.go`: `tryAddingExtensions` (try `.ps` first for extensionless imports, D-O01), `extensionIsOk`, `extensions.Array()` in types.go; `module/util.go` `GetResolutionDiagnostic` and `TryGetJSExtensionForFile`.
- Also: `outputpaths.GetOutputExtension`, `ast.GetImpliedNodeFormatForFile`, the LSP watch glob (`lsp/server.go:86`), `ls/string_completions.go`, `modulespecifiers`. The LSP ones can wait for M7.

## 5. AST basics

- `ast.Node{Kind, Flags, Loc core.TextRange, Parent}`; compare `node.Kind == ast.KindIdentifier`; casts like `node.AsSourceFile()`, `node.AsCallExpression()`; accessors `Name()`, `Expression()`.
- Children: `node.ForEachChild(func(n *ast.Node) bool { … })` (return `true` to stop), `node.IterChildren()`, `VisitEachChild` for transforms.
- `node.Pos()` includes leading trivia; real start: `scanner.GetTokenPosOfNode(node, sf, false)` or `scanner.SkipTrivia(text, pos)`.
- Line and column (0-based): `scanner.GetECMALineAndUTF16CharacterOfPosition(sf, pos)`; line only: `scanner.GetECMALineOfPosition`. Node text: `scanner.GetSourceTextOfNodeFromSourceFile`.
- `ast.SourceFile`: `FileName()`, `Text()`, `Statements.Nodes`, `ScriptKind`, `IsDeclarationFile`; `ast.GetSourceFileOfNode(n)`.

## 6. Upstream tests

- `npx hereby test` = `gotestsum ./...` (or `go test ./...`). Flags: `--tests=` (→ `-run`), `--race`, `--noembed`, `--coverage`, `--concurrentTestPrograms`. Sets `GODEBUG=tracebackancestors=10`.
- `internal/testrunner`:
  - `TestLocal` runs `testdata/tests/cases/{compiler,conformance}` without the submodule (2026-10-11 baseline: about 324 subtests passing, about 3.5 s).
  - `TestSubmodule` runs the full TypeScript corpus, but `_submodules/TypeScript` is **not initialised**, so it skips.
  - Baselines go to `testdata/baselines/local`; accept them with `hereby baseline-accept`.
- Parser: `internal/parser` has two small unit tests plus a fuzz test and a benchmark that both need the submodule. The checker and program have unit tests in `internal/checker`, `internal/compiler`.
- **Suites to keep green when touching the front end:** `go test ./internal/tspath/... ./internal/core/... ./internal/module/... ./internal/parser/... ./internal/scanner/... ./internal/tsoptions/... ./internal/compiler/... ./internal/checker/...` and `go test ./internal/testrunner/ -run TestLocal`.
