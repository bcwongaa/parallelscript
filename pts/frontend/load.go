package frontend

import (
	"context"
	"slices"
	"strings"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/bundled"
	"github.com/microsoft/typescript-go/internal/collections"
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
	errors := append(errorsOf(ctx, ts), internalImports(ts)...)
	if program.Entry != nil {
		program.Modules = evaluationOrder(ts, nil, program.Entry)
		if len(errors) == 0 {
			if globals := globalModulesUsed(ctx, ts, program.Modules); len(globals) > 0 {
				program.Modules = evaluationOrder(ts, globals, program.Entry)
			}
		}
	}
	return program, errors
}

// internalImports reports the imports of the library's internal modules (lib.IsInternalModule)
// outside the library.
func internalImports(ts *compiler.Program) []diagnostic.Diagnostic {
	var errors []diagnostic.Diagnostic
	for _, file := range ts.GetSourceFiles() {
		if lib.IsModulePath(file.FileName()) || lib.IsLibPath(file.FileName()) {
			continue
		}
		for _, statement := range file.Statements.Nodes {
			var specifier *ast.Node
			switch statement.Kind {
			case ast.KindImportDeclaration:
				specifier = statement.AsImportDeclaration().ModuleSpecifier
			case ast.KindExportDeclaration:
				specifier = statement.AsExportDeclaration().ModuleSpecifier
			}
			if specifier != nil && ast.IsStringLiteralLike(specifier) && lib.IsInternalModule(specifier.Text()) {
				errors = append(errors, diagnostic.At(specifier, diagnostic.Unsupported, "`%s` is internal to ParallelScript's library", specifier.Text()))
			}
		}
	}
	return errors
}

// globalModulesUsed are the pts: modules whose default export is a global (lib.GlobalModules)
// that one of modules uses, and pts:parallel if one uses `.par` (D-123): the program runs
// them first, as Node sets its globals up before any module runs.
func globalModulesUsed(ctx context.Context, ts *compiler.Program, modules []*ast.SourceFile) []*ast.SourceFile {
	var used []*ast.SourceFile
	for name, specifier := range lib.GlobalModules {
		module := ts.GetSourceFile(lib.ModulePath(specifier))
		if module != nil && !slices.Contains(modules, module) && slices.ContainsFunc(modules, func(file *ast.SourceFile) bool { return usesGlobal(ctx, ts, file, name) }) {
			used = append(used, module)
		}
	}
	parallel := ts.GetSourceFile(lib.ModulePath(lib.ParallelModule))
	if parallel != nil && !slices.Contains(modules, parallel) && slices.ContainsFunc(modules, func(file *ast.SourceFile) bool { return usesParallelView(ctx, ts, file) }) {
		used = append(used, parallel)
	}
	slices.SortFunc(used, func(a, b *ast.SourceFile) int { return strings.Compare(a.FileName(), b.FileName()) })
	return used
}

// usesGlobal reports whether file refers to the global variable name.
func usesGlobal(ctx context.Context, ts *compiler.Program, file *ast.SourceFile, name string) bool {
	if _, ok := file.Identifiers[name]; !ok {
		return false
	}
	checker, release := ts.GetTypeCheckerForFile(ctx, file)
	defer release()
	global := checker.GetGlobalSymbol(name, ast.SymbolFlagsValue, nil)
	var visit func(node *ast.Node) bool
	visit = func(node *ast.Node) bool {
		if node.Kind == ast.KindIdentifier && node.Text() == name && checker.GetSymbolAtLocation(node) == global {
			return true
		}
		return node.ForEachChild(visit)
	}
	return global != nil && file.AsNode().ForEachChild(visit)
}

// usesParallelView reports whether file reads the prelude's Array.par.
func usesParallelView(ctx context.Context, ts *compiler.Program, file *ast.SourceFile) bool {
	if _, ok := file.Identifiers["par"]; !ok {
		return false
	}
	checker, release := ts.GetTypeCheckerForFile(ctx, file)
	defer release()
	var visit func(node *ast.Node) bool
	visit = func(node *ast.Node) bool {
		if node.Kind == ast.KindPropertyAccessExpression && node.AsPropertyAccessExpression().Name().Text() == "par" {
			if symbol := checker.GetSymbolAtLocation(node); symbol != nil && symbol.ValueDeclaration != nil && lib.IsLibPath(ast.GetSourceFileOfNode(symbol.ValueDeclaration).FileName()) {
				return true
			}
		}
		return node.ForEachChild(visit)
	}
	return file.AsNode().ForEachChild(visit)
}

// evaluationOrder lists the modules reachable from first and then entry in ES module
// evaluation order: a module runs after the modules it imports, in the order it imports
// them, and only once. In an import cycle, a module that is already being evaluated is
// skipped, so the module that imports it runs first, as in JS.
func evaluationOrder(ts *compiler.Program, first []*ast.SourceFile, entry *ast.SourceFile) []*ast.SourceFile {
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
	for _, module := range first {
		visit(module)
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
// and the ES library without the DOM, plus ParallelScript's prelude; `pts:` specifiers
// resolve to the library's modules (D-116).
func compilerOptions() *core.CompilerOptions {
	paths := collections.NewOrderedMapWithSizeHint[string, []string](len(lib.ModulePaths))
	for pattern, substitutions := range lib.ModulePaths {
		paths.Set(pattern, substitutions)
	}
	return &core.CompilerOptions{
		Paths:                      paths,
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
