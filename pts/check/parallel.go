package check

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/pts/diagnostic"
	"github.com/microsoft/typescript-go/pts/lib"
)

// parallelOperator reports the forms of `parallel e` that can't mean what they say
// (D-120): the operator where a binding named parallel is in scope (the parser made it
// a call, which would call the binding), and a function as the operand (the task would
// only make the function).
func (c *pass) parallelOperator(node *ast.Node) {
	if node.Kind != ast.KindCallExpression || node.Flags&ast.NodeFlagsPtsParallel == 0 {
		return
	}
	symbol := c.tc.GetSymbolAtLocation(node.Expression())
	if symbol == nil || len(symbol.Declarations) == 0 || !lib.IsLibPath(ast.GetSourceFileOfNode(symbol.Declarations[0]).FileName()) {
		c.report(node, diagnostic.Unsupported, "`parallel e` here would call the binding named `parallel` in scope; rename the binding to use the operator")
		return
	}
	if operand := ast.SkipParentheses(node.Arguments()[0]); operand.Kind == ast.KindArrowFunction || operand.Kind == ast.KindFunctionExpression {
		c.report(operand, diagnostic.Unsupported, "the operand of `parallel` is a function, so its task would only make the function; give it the call instead (`parallel work()`)")
	}
}
