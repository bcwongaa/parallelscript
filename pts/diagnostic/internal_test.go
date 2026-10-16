package diagnostic_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/microsoft/typescript-go/pts/diagnostic"
	"gotest.tools/v3/assert"
)

// failingPhase runs a phase that reports earlier errors and then panics with value at
// position at, under Locate, inside a compiler boundary that Contains it.
func failingPhase(phase string, at diagnostic.Diagnostic, earlier []diagnostic.Diagnostic, value any) (err error) {
	defer diagnostic.Contain(&err)
	defer diagnostic.Locate(phase, func() (diagnostic.Diagnostic, []diagnostic.Diagnostic) { return at, earlier })
	panic(value)
}

func internalError(t *testing.T, err error) *diagnostic.InternalError {
	t.Helper()
	var internal *diagnostic.InternalError
	assert.Assert(t, errors.As(err, &internal), "expected an internal error, got %v", err)
	return internal
}

func TestAPanicInAPhaseIsAnInternalErrorWhereThePhaseWas(t *testing.T) {
	t.Parallel()
	at := diagnostic.Diagnostic{File: "/p/main.pts", Line: 3, Column: 9, Start: 20, End: 31}

	internal := internalError(t, failingPhase("lowering", at, nil, "index out of range [-1]"))

	assert.Equal(t, internal.Diagnostic, diagnostic.Diagnostic{File: "/p/main.pts", Line: 3, Column: 9, Start: 20, End: 31,
		Code: diagnostic.Internal, Message: "internal compiler error while lowering: index out of range [-1]"})
	assert.Equal(t, internal.Error(), "/p/main.pts:3:9 - error PTS9001: internal compiler error while lowering: index out of range [-1]")
}

func TestAnInternalErrorKeepsTheStackOfThePanic(t *testing.T) {
	t.Parallel()

	internal := internalError(t, failingPhase("lowering", diagnostic.Diagnostic{}, nil, "boom"))

	assert.Assert(t, strings.HasPrefix(internal.Detail, "panic: boom\n\n"), internal.Detail)
	assert.Assert(t, strings.Contains(internal.Detail, "diagnostic_test.failingPhase"), internal.Detail)
}

func TestAnInternalErrorKeepsTheErrorsReportedBeforeIt(t *testing.T) {
	t.Parallel()
	earlier := []diagnostic.Diagnostic{{File: "/p/main.pts", Line: 1, Column: 1, Code: "PTS1001", Message: "debugger statement is not supported yet"}}

	internal := internalError(t, failingPhase("lowering", diagnostic.Diagnostic{}, earlier, "boom"))

	assert.DeepEqual(t, internal.Earlier, earlier)
}

func TestAPanicOutsideAnyPhaseIsAnInternalErrorWithoutAPosition(t *testing.T) {
	t.Parallel()
	compile := func() (err error) {
		defer diagnostic.Contain(&err)
		var values []int
		_ = values[2]
		return nil
	}

	internal := internalError(t, compile())

	assert.Equal(t, internal.Error(), "error PTS9001: internal compiler error: runtime error: index out of range [2] with length 0")
}

func TestTheInnermostPhaseLocatesTheInternalError(t *testing.T) {
	t.Parallel()
	inner := diagnostic.Diagnostic{File: "/p/lib.pts", Line: 8}
	compile := func() (err error) {
		defer diagnostic.Contain(&err)
		defer diagnostic.Locate("lowering", func() (diagnostic.Diagnostic, []diagnostic.Diagnostic) {
			return diagnostic.Diagnostic{File: "/p/main.pts", Line: 2}, nil
		})
		return failingPhase("checking", inner, nil, "boom")
	}

	internal := internalError(t, compile())

	assert.Equal(t, internal.Error(), "/p/lib.pts:8 - error PTS9001: internal compiler error while checking: boom")
}

func TestAPhaseThatDoesNotPanicLeavesTheResultAlone(t *testing.T) {
	t.Parallel()
	compile := func() (err error) {
		defer diagnostic.Contain(&err)
		defer diagnostic.Locate("lowering", func() (diagnostic.Diagnostic, []diagnostic.Diagnostic) { return diagnostic.Diagnostic{}, nil })
		return errors.New("type errors")
	}

	assert.Error(t, compile(), "type errors")
}
