package driver

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/microsoft/typescript-go/pts/diagnostic"
	"github.com/microsoft/typescript-go/pts/toolchain"
	"gotest.tools/v3/assert"
)

// compileC compiles C source as a program's main.c with the runtime, returning the
// C compiler's failure as Build reports it.
func compileC(t *testing.T, source string) error {
	t.Helper()
	tc, err := toolchain.New()
	assert.NilError(t, err)
	rt, err := tc.Runtime(t.Context(), toolchain.Release)
	assert.NilError(t, err)
	dir := t.TempDir()
	cFile := filepath.Join(dir, "main.c")
	assert.NilError(t, os.WriteFile(cFile, []byte(source), 0o644))
	err = tc.CompileProgram(t.Context(), rt, []string{cFile}, filepath.Join(dir, "main"))
	assert.Assert(t, err != nil, "the C compiled")
	return generatedCFailure(err, cFile)
}

func internalError(t *testing.T, err error) *diagnostic.InternalError {
	t.Helper()
	var internal *diagnostic.InternalError
	assert.Assert(t, errors.As(err, &internal), "expected an internal compiler error, got %v", err)
	return internal
}

func TestGeneratedCThatClangRejectsIsAnInternalErrorAtItsSourceLine(t *testing.T) {
	t.Parallel()
	source := "#include <stddef.h>\n#line 7 \"/p/main.pts\"\nint main(void) { int *p = 1.5; return 0; }\n"

	internal := internalError(t, compileC(t, source))

	assert.Equal(t, internal.Format("/p"), "main.pts:7 - error PTS9001: internal compiler error: the generated C doesn't compile: initializing 'int *' with an expression of incompatible type 'double'")
	assert.Assert(t, strings.Contains(internal.Detail, "1 error generated."), internal.Detail)
}

func TestAnErrorInGeneratedCOutsideTheSourceHasNoPosition(t *testing.T) {
	t.Parallel()
	source := "int main(void) { int *p = 1.5; return 0; }\n"

	internal := internalError(t, compileC(t, source))

	assert.Equal(t, internal.Format("/p"), "error PTS9001: internal compiler error: the generated C doesn't compile: initializing 'int *' with an expression of incompatible type 'double'")
}

func TestGeneratedCThatDoesNotLinkIsAnInternalError(t *testing.T) {
	t.Parallel()
	source := "extern void pts_no_such_function(void);\nint main(void) { pts_no_such_function(); return 0; }\n"

	internal := internalError(t, compileC(t, source))

	assert.Equal(t, internal.Format("/p"), "error PTS9001: internal compiler error: the generated C doesn't link: undefined reference to `pts_no_such_function'")
}

func TestAnUndefinedReferenceInTheRuntimeIsNotACompilerBug(t *testing.T) {
	t.Parallel()
	tc, err := toolchain.New()
	assert.NilError(t, err)
	rt, err := tc.Runtime(t.Context(), toolchain.Release)
	assert.NilError(t, err)
	withoutLibuv := *rt
	withoutLibuv.Archives = slices.DeleteFunc(slices.Clone(rt.Archives), func(archive string) bool { return strings.Contains(filepath.Base(archive), "uv") })
	assert.Assert(t, len(withoutLibuv.Archives) == len(rt.Archives)-1)
	dir := t.TempDir()
	cFile := filepath.Join(dir, "main.c")
	source := "extern void pts_runtime_init(int argc, char **argv);\nint main(int argc, char **argv) { pts_runtime_init(argc, argv); return 0; }\n"
	assert.NilError(t, os.WriteFile(cFile, []byte(source), 0o644))

	err = generatedCFailure(tc.CompileProgram(t.Context(), &withoutLibuv, []string{cFile}, filepath.Join(dir, "main")), cFile)

	var internal *diagnostic.InternalError
	assert.Assert(t, !errors.As(err, &internal), "a broken runtime archive was reported as a compiler bug: %v", err)
	assert.ErrorContains(t, err, "undefined reference to `uv_")
}
