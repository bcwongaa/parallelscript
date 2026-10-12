package frontend

import (
	"context"
	"fmt"
	"slices"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/astnav"
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/internal/compiler"
	"github.com/microsoft/typescript-go/internal/diagnostics"
	"github.com/microsoft/typescript-go/pts/lib"
)

// Hints explain tsc errors that ParallelScript's own features lead to, with the fix
// (D-170): tsc's message says what is wrong in TypeScript's terms, the hint in
// ParallelScript's.

// parallelOnlyMethods are the methods of `.par`'s views that arrays don't have, with
// how a call of each is written.
var parallelOnlyMethods = map[string]string{
	"count": "count(predicate)", "sum": "sum()", "min": "min()", "max": "max()", "sortBy": "sortBy(key)", "toArray": "toArray()",
}

var typedArrayNames = []string{
	"Int8Array", "Uint8Array", "Uint8ClampedArray", "Int16Array", "Uint16Array",
	"Int32Array", "Uint32Array", "Float32Array", "Float64Array",
}

// Hint is a sentence to show after tsc's error d, or "" if ParallelScript has nothing to add.
func Hint(ctx context.Context, program *compiler.Program, d *ast.Diagnostic) string {
	file := d.File()
	if file == nil {
		return ""
	}
	switch d.Code() {
	case diagnostics.Property_0_does_not_exist_on_type_1.Code():
		tc, release := program.GetTypeCheckerForFile(ctx, file)
		defer release()
		return parallelMethodHint(tc, astnav.GetTokenAtPosition(file, d.Pos()))
	case diagnostics.This_expression_is_not_callable.Code():
		tc, release := program.GetTypeCheckerForFile(ctx, file)
		defer release()
		return parallelCallHint(tc, astnav.GetTokenAtPosition(file, d.Pos()))
	}
	return ""
}

// parallelMethodHint is the hint for a method of `.par`'s views called on an array:
// `xs.par.filter(f).count()` (`.par.filter` returns an array) or `xs.sum()`.
func parallelMethodHint(tc *checker.Checker, name *ast.Node) string {
	access := name.Parent
	if name.Kind != ast.KindIdentifier || access == nil || !ast.IsPropertyAccessExpression(access) || access.Name() != name ||
		parallelOnlyMethods[name.Text()] == "" || !isArrayOrTypedArray(tc, tc.GetTypeAtLocation(access.Expression())) {
		return ""
	}
	method := name.Text()
	if producer := parallelViewMethodOf(access.Expression()); producer != "" {
		if method == "count" && producer == "filter" {
			return "`.par.filter` returns an array: count in parallel with `.par.count(predicate)` instead of filtering, or take the array's `.length`."
		}
		return fmt.Sprintf("`.par.%s` returns an array, whose parallel methods are on `.par` again: `.par.%s(…).par.%s`.", producer, producer, parallelOnlyMethods[method])
	}
	if method == "count" {
		return "Arrays have no `count`: take `.length`, or count matches in parallel with `.par.count(predicate)`."
	}
	return fmt.Sprintf("Arrays have no `%s`; their parallel view has: `.par.%s`.", method, parallelOnlyMethods[method])
}

// parallelViewMethodOf is m when expression is a call `….par.m(…)`, else "".
func parallelViewMethodOf(expression *ast.Node) string {
	if !ast.IsCallExpression(expression) {
		return ""
	}
	callee := expression.Expression()
	if !ast.IsPropertyAccessExpression(callee) {
		return ""
	}
	view := callee.Expression()
	if !ast.IsPropertyAccessExpression(view) || view.Name().Text() != "par" {
		return ""
	}
	return callee.Name().Text()
}

func isArrayOrTypedArray(tc *checker.Checker, t *checker.Type) bool {
	if tc.IsArrayLikeType(t) {
		return true
	}
	symbol := t.Symbol()
	return symbol != nil && slices.Contains(typedArrayNames, symbol.Name)
}

// parallelCallHint is the hint for `parallel (f)(x)`: a `(` after `parallel` starts a call
// of `parallel` (D-O05), so the second `(…)` calls the promise that call returns.
func parallelCallHint(tc *checker.Checker, token *ast.Node) string {
	inner := token.Parent
	if token.Kind != ast.KindIdentifier || token.Text() != "parallel" || inner == nil || !ast.IsCallExpression(inner) ||
		inner.Expression() != token || inner.Parent == nil || !ast.IsCallExpression(inner.Parent) || inner.Parent.Expression() != inner ||
		!isPreludeParallel(tc.GetSymbolAtLocation(token)) {
		return ""
	}
	return "A `(` right after `parallel` starts a call of `parallel`, so the `(…)` after it calls the promise that call returns. " +
		"To run the call as a task, put it inside the parentheses, `parallel((async () => { … })())`, or name the function and write `parallel f()`."
}

func isPreludeParallel(symbol *ast.Symbol) bool {
	return symbol != nil && len(symbol.Declarations) > 0 &&
		ast.GetSourceFileOfNode(symbol.Declarations[0]).FileName() == lib.PreludePath
}
