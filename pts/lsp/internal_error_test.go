package lsp

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/core"
	"github.com/microsoft/typescript-go/internal/ls/lsconv"
	"github.com/microsoft/typescript-go/internal/lsp/lsproto"
	"github.com/microsoft/typescript-go/internal/parser"
	"github.com/microsoft/typescript-go/pts/diagnostic"
	"gotest.tools/v3/assert"
)

// shownFailure is the diagnostic the server shows on /p/main.pts, whose text is source,
// when the checks fail with failure.
func shownFailure(source string, failure any) *lsproto.Diagnostic {
	file := parser.ParseSourceFile(ast.SourceFileParseOptions{FileName: "/p/main.pts", Path: "/p/main.pts"}, source, core.ScriptKindTS)
	converters := lsconv.NewConverters(lsproto.PositionEncodingKindUTF16, func(string) *lsconv.LSPLineMap { return lsconv.ComputeLSPLineStarts(source) })
	return internalError(file, converters, failure)
}

func checksFailure(file string, line, column, start, end int) *diagnostic.InternalError {
	return &diagnostic.InternalError{Diagnostic: diagnostic.Diagnostic{File: file, Line: line, Column: column, Start: start, End: end, Code: diagnostic.Internal,
		Message: "internal compiler error while running ParallelScript's checks: boom"}}
}

func TestAnInternalErrorOfTheChecksIsShownAtItsNode(t *testing.T) {
	t.Parallel()
	source := "let n = 1;\nn = n * 2;\n"

	shown := shownFailure(source, checksFailure("/p/main.pts", 2, 5, 15, 20))

	assert.Equal(t, shown.Range, lsproto.Range{Start: lsproto.Position{Line: 1, Character: 4}, End: lsproto.Position{Line: 1, Character: 9}})
	assert.Equal(t, *shown.Message.String, "internal compiler error while running ParallelScript's checks: boom (a bug in pts)")
	assert.Equal(t, *shown.Severity, lsproto.DiagnosticSeverityWarning)
}

func TestAnInternalErrorOfTheChecksInAnotherFileIsShownAtTheStart(t *testing.T) {
	t.Parallel()

	shown := shownFailure("let n = 1;\n", checksFailure("/p/other.pts", 2, 5, 15, 20))

	assert.Equal(t, shown.Range, lsproto.Range{})
	assert.Equal(t, *shown.Message.String, "internal compiler error while running ParallelScript's checks: boom (a bug in pts, at /p/other.pts:2:5)")
}
