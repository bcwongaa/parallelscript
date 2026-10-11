package frontend

import (
	"context"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/bundled"
	"github.com/microsoft/typescript-go/internal/compiler"
	"github.com/microsoft/typescript-go/internal/core"
	"github.com/microsoft/typescript-go/internal/diagnostics"
	"github.com/microsoft/typescript-go/internal/tsoptions"
	"github.com/microsoft/typescript-go/internal/tspath"
	"github.com/microsoft/typescript-go/internal/vfs"
	"github.com/microsoft/typescript-go/pts/diagnostic"
	"github.com/microsoft/typescript-go/pts/lib"
)

// Program is a parsed and type-checked ParallelScript program.
type Program struct {
	TS    *compiler.Program
	Entry *ast.SourceFile
	// Modules are the program's source files in the order their top-level code runs; the
	// entry module is last. Declaration files have no code, so they aren't modules here.
	Modules []*ast.SourceFile
}

// Load parses and type-checks the program whose entry module is the file at entry
// (an absolute path), reading sources from fs. The program is usable only if no
// diagnostics come back.
func Load(ctx context.Context, fs vfs.FS, entry string) (*Program, []diagnostic.Diagnostic) {
	entry = tspath.NormalizePath(entry)
	host := compiler.NewCompilerHost(tspath.GetDirectoryPath(entry), lib.WrapFS(bundled.WrapFS(fs)), bundled.LibPath(), nil, nil)
	config := tsoptions.NewParsedCommandLine(compilerOptions(), []string{lib.PreludePath, entry}, tspath.ComparePathsOptions{
		UseCaseSensitiveFileNames: host.FS().UseCaseSensitiveFileNames(),
		CurrentDirectory:          host.GetCurrentDirectory(),
	})
	ts := compiler.NewProgram(compiler.ProgramOptions{Config: config, Host: host})
	program := &Program{TS: ts, Entry: ts.GetSourceFile(entry)}
	if program.Entry != nil {
		program.Modules = evaluationOrder(ts, program.Entry)
	}
	return program, errorsOf(ctx, ts)
}

// evaluationOrder lists the modules reachable from entry in ES module evaluation order:
// a module runs after the modules it imports, in the order it imports them, and only
// once. In an import cycle, a module that is already being evaluated is skipped, so the
// module that imports it runs first, as in JS.
func evaluationOrder(ts *compiler.Program, entry *ast.SourceFile) []*ast.SourceFile {
	var order []*ast.SourceFile
	seen := map[*ast.SourceFile]bool{}
	var visit func(file *ast.SourceFile)
	visit = func(file *ast.SourceFile) {
		seen[file] = true
		for _, specifier := range moduleRequests(file) {
			if !ast.IsStringLiteralLike(specifier) {
				continue // a syntax error, which tsc reports
			}
			resolved := ts.GetResolvedModuleFromModuleSpecifier(file, specifier)
			if !resolved.IsResolved() {
				continue // tsc reports it
			}
			imported := ts.GetSourceFileForResolvedModule(resolved.ResolvedFileName)
			if imported != nil && !imported.IsDeclarationFile && !seen[imported] {
				visit(imported)
			}
		}
		order = append(order, file)
	}
	visit(entry)
	return order
}

// moduleRequests are the specifiers of the modules a file imports or re-exports from, in
// source order. `import type` and `export type … from` request nothing: Node erases them.
// Other imports are kept even if they import only types, as Node keeps them.
func moduleRequests(file *ast.SourceFile) []*ast.Node {
	var specifiers []*ast.Node
	for _, statement := range file.Statements.Nodes {
		switch statement.Kind {
		case ast.KindImportDeclaration:
			if !ast.IsExclusivelyTypeOnlyImportOrExport(statement) {
				specifiers = append(specifiers, statement.AsImportDeclaration().ModuleSpecifier)
			}
		case ast.KindExportDeclaration:
			if specifier := statement.AsExportDeclaration().ModuleSpecifier; specifier != nil && !ast.IsExclusivelyTypeOnlyImportOrExport(statement) {
				specifiers = append(specifiers, specifier)
			}
		}
	}
	return specifiers
}

// compilerOptions are fixed for every program (D-005): strict TypeScript, ES modules,
// and the ES library without the DOM, plus ParallelScript's prelude.
func compilerOptions() *core.CompilerOptions {
	return &core.CompilerOptions{
		Strict:                     core.TSTrue,
		Target:                     core.ScriptTargetESNext,
		Module:                     core.ModuleKindESNext,
		ModuleResolution:           core.ModuleResolutionKindBundler,
		ModuleDetection:            core.ModuleDetectionKindForce,
		AllowImportingTsExtensions: core.TSTrue,
		NoEmit:                     core.TSTrue,
		Lib:                        []string{"lib.esnext.d.ts"},
		Types:                      []string{},
	}
}

// errorsOf collects the program's errors the way tsc does: semantic errors only
// when there are no syntax errors.
func errorsOf(ctx context.Context, ts *compiler.Program) []diagnostic.Diagnostic {
	all := compiler.GetDiagnosticsOfAnyProgram(ctx, ts, nil, true, ts.GetBindDiagnostics, ts.GetSemanticDiagnostics)
	var result []diagnostic.Diagnostic
	for _, d := range compiler.SortAndDeduplicateDiagnostics(all) {
		if d.Category() == diagnostics.CategoryError {
			result = append(result, diagnostic.FromTS(d))
		}
	}
	return result
}
