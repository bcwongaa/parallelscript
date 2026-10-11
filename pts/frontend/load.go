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
	return program, errorsOf(ctx, ts)
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
