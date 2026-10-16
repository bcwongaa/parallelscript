package driver_test

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/pts/diagnostic"
	"github.com/microsoft/typescript-go/pts/driver"
	"gotest.tools/v3/assert"
)

// writeSource writes main.pts into a fresh directory and returns its path.
func writeSource(t *testing.T, source string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "main.pts")
	assert.NilError(t, os.WriteFile(path, []byte(source), 0o644))
	return path
}

// compileErrors returns the diagnostics in err, which must be a *diagnostic.Error.
func compileErrors(t *testing.T, err error) []diagnostic.Diagnostic {
	t.Helper()
	var compileErr *diagnostic.Error
	assert.Assert(t, errors.As(err, &compileErr), "expected compile errors, got %v", err)
	return compileErr.Diagnostics
}

func TestHelloWorldBuildsAndRuns(t *testing.T) {
	t.Parallel()
	entry := writeSource(t, "console.log(\"Hello, ParallelScript\");\n")
	exe := filepath.Join(t.TempDir(), "hello")

	assert.NilError(t, driver.Build(t.Context(), entry, exe))

	out, err := exec.CommandContext(t.Context(), exe).Output()
	assert.NilError(t, err)
	assert.Equal(t, string(out), "Hello, ParallelScript\n")
}

func TestLocalsAndPrimitivesPrintLikeNode(t *testing.T) {
	t.Parallel()
	entry := writeSource(t, `const greeting = "héllo";
let count = 0.1;
const done: boolean = false;
{
  const count = 1e21;
  console.log(greeting, count, done, null, undefined);
}
console.log(count);
console.log();
`)
	exe := filepath.Join(t.TempDir(), "prog")

	assert.NilError(t, driver.Build(t.Context(), entry, exe))

	out, err := exec.CommandContext(t.Context(), exe).Output()
	assert.NilError(t, err)
	assert.Equal(t, string(out), "héllo 1e+21 false null undefined\n0.1\n\n")
}

func TestEmittedCPointsBackToTheSource(t *testing.T) {
	t.Parallel()
	entry := writeSource(t, "\n\nconsole.log(1);\n")

	c, err := driver.EmitC(t.Context(), entry)

	assert.NilError(t, err)
	assert.Assert(t, strings.Contains(c, "#line 3 \""+entry+"\"\n  pts_console_log("), c)
}

func TestTypeErrorsStopTheBuild(t *testing.T) {
	t.Parallel()
	entry := writeSource(t, `const n: number = "one";`)

	err := driver.Build(t.Context(), entry, filepath.Join(t.TempDir(), "prog"))

	diagnostics := compileErrors(t, err)
	assert.Equal(t, len(diagnostics), 1)
	assert.Equal(t, diagnostics[0].Code, "TS2322")
}

func TestUnsupportedSyntaxStopsTheBuild(t *testing.T) {
	t.Parallel()
	entry := writeSource(t, `debugger; console.log(1);`)

	_, err := driver.EmitC(t.Context(), entry)

	diagnostics := compileErrors(t, err)
	assert.Equal(t, len(diagnostics), 1)
	assert.Equal(t, diagnostics[0].Format(filepath.Dir(entry)), "main.pts:1:1 - error PTS1001: debugger statement is not supported yet")
}

// internalError returns err as an internal compiler error, which it must be.
func internalError(t *testing.T, err error) *diagnostic.InternalError {
	t.Helper()
	var internal *diagnostic.InternalError
	assert.Assert(t, errors.As(err, &internal), "expected an internal compiler error, got %v", err)
	return internal
}

func TestAPanicWhileCompilingIsReturnedAsAnInternalError(t *testing.T) {
	t.Parallel()
	entry := writeSource(t, "const words = [\"a\"];\nconsole.log(words.length + 1);\n")
	ctx := diagnostic.WithInjectedFailure(t.Context(), func(phase string, node *ast.Node) bool {
		return phase == "lowering" && node.Kind == ast.KindBinaryExpression
	})

	_, err := driver.EmitC(ctx, entry)

	internal := internalError(t, err)
	assert.Equal(t, internal.Format(filepath.Dir(entry)), "main.pts:2:13 - error PTS9001: internal compiler error while lowering: injected failure")
}
