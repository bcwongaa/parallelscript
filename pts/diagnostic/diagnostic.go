// Package diagnostic is the one error format of the pts compiler: tsc's own
// diagnostics and ParallelScript's are both reported as a Diagnostic.
package diagnostic

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/diagnosticwriter"
	"github.com/microsoft/typescript-go/internal/locale"
	"github.com/microsoft/typescript-go/internal/scanner"
)

// Codes of ParallelScript's own diagnostics. tsc's keep their numbers with a "TS" prefix.
const (
	// NotSupportedYet marks valid TypeScript that the compiler cannot lower yet.
	NotSupportedYet = "PTS1001"
	// Unsupported marks a feature ParallelScript leaves out on purpose (docs/SPEC.md §3.10).
	Unsupported = "PTS1002"

	// NumericConversion marks an implicit numeric conversion that could change the value (§3.2).
	NumericConversion = "PTS2001"
	// NumericLiteral marks a literal that its numeric type can't hold.
	NumericLiteral = "PTS2002"
	// NumericMix marks an operator on two numeric types with no lossless common type.
	NumericMix = "PTS2003"

	// ParallelAssignment marks the single guardrail (§5.6): a parallel body assigning a
	// variable declared outside it.
	ParallelAssignment = "PTS3001"
	// ParallelFor marks a `parallel for` that isn't a `for…of` loop, or a jump out of its
	// body (§5.5).
	ParallelFor = "PTS3002"
)

// Diagnostic is one compile error.
type Diagnostic struct {
	File    string // absolute path; empty for errors not tied to a file
	Line    int    // 1-based
	Column  int    // 1-based, in UTF-16 code units as tsc counts them
	Code    string // "TS2322", "PTS1001", …
	Message string
}

// At reports a diagnostic at the start of node, skipping its leading trivia.
func At(node *ast.Node, code string, format string, args ...any) Diagnostic {
	file := ast.GetSourceFileOfNode(node)
	line, column := scanner.GetECMALineAndUTF16CharacterOfPosition(file, scanner.GetTokenPosOfNode(node, file, false))
	return Diagnostic{File: file.FileName(), Line: line + 1, Column: int(column) + 1, Code: code, Message: fmt.Sprintf(format, args...)}
}

// FromTS converts one of tsc's diagnostics, flattening its message chain.
func FromTS(d *ast.Diagnostic) Diagnostic {
	var message strings.Builder
	diagnosticwriter.WriteFlattenedASTDiagnosticMessage(&message, d, "\n", locale.Default)
	result := Diagnostic{Code: fmt.Sprintf("TS%d", d.Code()), Message: message.String()}
	if file := d.File(); file != nil {
		line, column := scanner.GetECMALineAndUTF16CharacterOfPosition(file, d.Pos())
		result.File, result.Line, result.Column = file.FileName(), line+1, int(column)+1
	}
	return result
}

// Format renders the diagnostic as "file:line:col - error CODE: message", with the
// file relative to dir when it lies below it.
func (d Diagnostic) Format(dir string) string {
	if d.File == "" {
		return fmt.Sprintf("error %s: %s", d.Code, d.Message)
	}
	return fmt.Sprintf("%s:%d:%d - error %s: %s", displayPath(d.File, dir), d.Line, d.Column, d.Code, d.Message)
}

func displayPath(file string, dir string) string {
	rel, err := filepath.Rel(dir, file)
	if err != nil || strings.HasPrefix(rel, "..") {
		return file
	}
	return rel
}

// Error is a non-empty list of diagnostics returned as an error.
type Error struct {
	Diagnostics []Diagnostic
}

func (e *Error) Error() string {
	lines := make([]string, len(e.Diagnostics))
	for i, d := range e.Diagnostics {
		lines[i] = d.Format("")
	}
	return strings.Join(lines, "\n")
}
