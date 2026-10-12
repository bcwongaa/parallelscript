// Package lsp is ParallelScript's language server, `pts lsp` (D-168): typescript-go's own
// server, whose projects build their programs as the compiler does (its fixed options, the
// prelude as a root and the library's ptslib:/// files), and whose diagnostics add
// ParallelScript's own checks to tsc's.
package lsp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"sync"
	"time"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/bundled"
	"github.com/microsoft/typescript-go/internal/compiler"
	"github.com/microsoft/typescript-go/internal/core"
	"github.com/microsoft/typescript-go/internal/diagnostics"
	"github.com/microsoft/typescript-go/internal/ls"
	"github.com/microsoft/typescript-go/internal/ls/lsconv"
	"github.com/microsoft/typescript-go/internal/lsp"
	"github.com/microsoft/typescript-go/internal/lsp/lsproto"
	"github.com/microsoft/typescript-go/internal/project"
	"github.com/microsoft/typescript-go/internal/tsoptions"
	"github.com/microsoft/typescript-go/internal/tspath"
	"github.com/microsoft/typescript-go/internal/vfs"
	"github.com/microsoft/typescript-go/pts/check"
	"github.com/microsoft/typescript-go/pts/frontend"
	"github.com/microsoft/typescript-go/pts/lib"
)

// progressDelay is how long a request runs before the client is shown its progress, as tsgo's server does.
const progressDelay = 250 * time.Millisecond

var installHooks sync.Once

// ServerOptions are the options of a server talking to its client over in and out and
// reading files from fs (the library's files are added to it). Automatic type acquisition
// is off: ParallelScript programs don't use npm packages.
func ServerOptions(in lsp.Reader, out lsp.Writer, errOut io.Writer, cwd string, fs vfs.FS) lsp.ServerOptions {
	installHooks.Do(func() {
		project.ProgramCommandLine = programCommandLine
		ls.ExtraDiagnostics = ptsDiagnostics
		lsp.ContentSchemes = contentSchemes()
	})
	return lsp.ServerOptions{
		In:                 in,
		Out:                out,
		Err:                errOut,
		Cwd:                cwd,
		FS:                 lib.WrapFS(bundled.WrapFS(fs)),
		DefaultLibraryPath: bundled.LibPath(),
		NpmInstall: func(string, []string) ([]byte, error) {
			return nil, errors.New("ParallelScript doesn't install npm packages")
		},
		ProgressDelay: progressDelay,
	}
}

// Run serves one client over in and out until it exits or ctx ends.
func Run(ctx context.Context, in io.Reader, out io.Writer, errOut io.Writer, cwd string, fs vfs.FS) error {
	options := ServerOptions(lsp.ToReader(in), lsp.ToWriter(out), errOut, cwd, fs)
	return lsp.NewServer(&options).Run(ctx)
}

// contentSchemes are the schemes of the library files that exist only in the server's file
// system, whose text a client asks the server for (D-169): ParallelScript's, and
// TypeScript's when they are embedded in the binary.
func contentSchemes() []string {
	if bundled.Embedded {
		return []string{lib.URIScheme, "bundled"}
	}
	return []string{lib.URIScheme}
}

// programCommandLine is the command line a project's program is built from: the project's
// root files and the prelude, with the compiler's fixed options in place of the project's
// (D-005), so that the editor sees the program `pts build` sees.
func programCommandLine(commandLine *tsoptions.ParsedCommandLine, currentDirectory string, useCaseSensitiveFileNames bool) *tsoptions.ParsedCommandLine {
	roots := commandLine.FileNames()
	if !slices.Contains(roots, lib.PreludePath) {
		roots = append([]string{lib.PreludePath}, roots...)
	}
	return tsoptions.NewParsedCommandLine(frontend.CompilerOptions(), roots, tspath.ComparePathsOptions{
		UseCaseSensitiveFileNames: useCaseSensitiveFileNames,
		CurrentDirectory:          currentDirectory,
	})
}

// ptsDiagnostics are ParallelScript's own diagnostics of file: hints on its tsc errors
// (frontend.Hint) when it has any, else ParallelScript's checks (pts/check: the numeric
// rules, the guardrail and the features left out), computed only when the modules file
// imports have no tsc errors either, as the compiler checks only such programs.
func ptsDiagnostics(ctx context.Context, program *compiler.Program, file *ast.SourceFile, converters *lsconv.Converters) (result []*lsproto.Diagnostic) {
	if file.IsDeclarationFile || lib.IsLibPath(file.FileName()) {
		return nil
	}
	if tscErrors := errorsOf(ctx, program, file); len(tscErrors) > 0 {
		return hints(ctx, program, file, converters, tscErrors)
	}
	ptsProgram := frontend.Of(program, file)
	for _, module := range ptsProgram.Modules {
		if hasErrors(ctx, program, module) {
			return nil
		}
	}
	defer func() {
		if r := recover(); r != nil { // a bug in a check must not take the editor's server down
			result = []*lsproto.Diagnostic{internalError(file, converters, r)}
		}
	}()
	_, errs := check.Program(ctx, ptsProgram)
	for _, d := range errs {
		if d.File != file.FileName() {
			continue
		}
		code := d.Code
		result = append(result, &lsproto.Diagnostic{
			Range:    converters.ToLSPRange(file, core.NewTextRange(d.Start, d.End)),
			Severity: new(lsproto.DiagnosticSeverityError),
			Code:     &lsproto.IntegerOrString{String: &code},
			Source:   new("pts"),
			Message:  lsproto.StringOrMarkupContent{String: &d.Message},
		})
	}
	return result
}

// hasErrors reports whether tsc finds an error in file.
func hasErrors(ctx context.Context, program *compiler.Program, file *ast.SourceFile) bool {
	return len(errorsOf(ctx, program, file)) > 0
}

// errorsOf are tsc's errors in file.
func errorsOf(ctx context.Context, program *compiler.Program, file *ast.SourceFile) []*ast.Diagnostic {
	isError := func(d *ast.Diagnostic) bool { return d.Category() == diagnostics.CategoryError }
	all := slices.Concat(program.GetSyntacticDiagnostics(ctx, file), program.GetSemanticDiagnostics(ctx, file))
	return slices.DeleteFunc(all, func(d *ast.Diagnostic) bool { return !isError(d) })
}

// hints are ParallelScript's hints on tsc's errors in file, as information at each error's range.
func hints(ctx context.Context, program *compiler.Program, file *ast.SourceFile, converters *lsconv.Converters, tscErrors []*ast.Diagnostic) []*lsproto.Diagnostic {
	var result []*lsproto.Diagnostic
	for _, d := range tscErrors {
		if hint := frontend.Hint(ctx, program, d); hint != "" {
			result = append(result, &lsproto.Diagnostic{
				Range:    converters.ToLSPRange(file, core.NewTextRange(d.Pos(), d.End())),
				Severity: new(lsproto.DiagnosticSeverityInformation),
				Source:   new("pts"),
				Message:  lsproto.StringOrMarkupContent{String: &hint},
			})
		}
	}
	return result
}

// internalError is a diagnostic at the start of file saying that ParallelScript's checks failed.
func internalError(file *ast.SourceFile, converters *lsconv.Converters, failure any) *lsproto.Diagnostic {
	return &lsproto.Diagnostic{
		Range:    converters.ToLSPRange(file, core.NewTextRange(0, 0)),
		Severity: new(lsproto.DiagnosticSeverityWarning),
		Source:   new("pts"),
		Message:  lsproto.StringOrMarkupContent{String: new(fmt.Sprintf("ParallelScript's checks failed on this file (a compiler bug): %v", failure))},
	}
}
