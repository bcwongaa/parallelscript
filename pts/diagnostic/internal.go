package diagnostic

import (
	"context"
	"fmt"
	"runtime/debug"

	"github.com/microsoft/typescript-go/internal/ast"
)

// Internal is the code of an internal compiler error: a bug in pts, not in the program.
const Internal = "PTS9001"

// InternalError is a bug the compiler ran into: a panic in one of its phases, or C it
// generated that the C compiler rejects (D-179). It is reported where the compiler was
// working when it failed, if that is known.
type InternalError struct {
	Diagnostic // Code is Internal; File is empty when the position is unknown
	// Detail is what a bug report needs: the Go stack of the panic, or the C compiler's
	// command line and output.
	Detail string
	// Earlier are the errors the failing phase had reported, which may explain the failure.
	Earlier []Diagnostic
}

func (e *InternalError) Error() string { return e.Format("") }

// Locate is deferred at the start of a compiler phase. A panic in the phase becomes an
// *InternalError at the position state gives (a zero Diagnostic if unknown), with the
// errors the phase reported before it, and Locate panics with that. One from a phase
// nested in this one passes through as it is.
func Locate(phase string, state func() (at Diagnostic, earlier []Diagnostic)) {
	failure := recover()
	if failure == nil {
		return
	}
	if internal, ok := failure.(*InternalError); ok {
		panic(internal)
	}
	at, earlier := state()
	panic(newInternalError("internal compiler error while "+phase, failure, at, earlier))
}

// Contain is deferred at the compiler's boundary: a panic becomes the *InternalError
// returned through err, without a position if no phase located it. Nothing else is
// recovered from: the compilation stops either way.
func Contain(err *error) {
	failure := recover()
	if failure == nil {
		return
	}
	if internal, ok := failure.(*InternalError); ok {
		*err = internal
		return
	}
	*err = newInternalError("internal compiler error", failure, Diagnostic{}, nil)
}

// newInternalError is called in the deferred function that recovered failure, so the
// stack it records still has the frames that panicked.
func newInternalError(what string, failure any, at Diagnostic, earlier []Diagnostic) *InternalError {
	at.Code = Internal
	at.Message = fmt.Sprintf("%s: %v", what, failure)
	return &InternalError{Diagnostic: at, Detail: fmt.Sprintf("panic: %v\n\n%s", failure, debug.Stack()), Earlier: earlier}
}

// injectedFailureKey is the context key of WithInjectedFailure's function.
type injectedFailureKey struct{}

// WithInjectedFailure returns ctx under which the compiler's phases panic on the nodes
// fail selects, for tests of how internal compiler errors are reported.
func WithInjectedFailure(ctx context.Context, fail func(phase string, node *ast.Node) bool) context.Context {
	return context.WithValue(ctx, injectedFailureKey{}, fail)
}

// InjectedFailure is the part for phase of the function WithInjectedFailure put in ctx,
// or nil.
func InjectedFailure(ctx context.Context, phase string) func(node *ast.Node) bool {
	fail, _ := ctx.Value(injectedFailureKey{}).(func(string, *ast.Node) bool)
	if fail == nil {
		return nil
	}
	return func(node *ast.Node) bool { return fail(phase, node) }
}
