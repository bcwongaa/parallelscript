package lower

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/pts/ir"
)

// JSON (D-080): JSON.parse makes dynamic values and JSON.stringify reads any value, at
// run time, through descriptors.

// jsonParse lowers JSON.parse(text, reviver): the runtime's parse of text, converted to a
// string as JS's ToString does if it is a dynamic value. A reviver is converted to the
// function type (key: string, value: any) => any, which the runtime calls.
func (l *lowerer) jsonParse(call *ast.CallExpression) ir.Expr {
	args := call.Arguments.Nodes
	if l.hasSpreadArgument(call) {
		return nil
	}
	text := l.expression(args[0])
	if text == nil {
		return nil
	}
	if text.Type() != ir.String {
		if text = l.toString(text, args[0]); text == nil {
			return nil
		}
	}
	if len(args) > 1 {
		reviver := l.expression(args[1])
		if reviver == nil {
			return nil
		}
		if reviver = l.convert(reviver, l.funcType([]ir.Type{ir.String, ir.Value}, ir.Value), args[1]); reviver == nil {
			return nil
		}
		return &ir.CallBuiltin{Builtin: ir.JSONParseRevived, Args: []ir.Expr{text, box(reviver)}}
	}
	return &ir.CallBuiltin{Builtin: ir.JSONParse, Args: []ir.Expr{text}}
}

// jsonStringify lowers JSON.stringify(value, replacer, space): the runtime's, of the
// arguments boxed. tsc types the result as a string, but it is undefined for undefined
// and functions: where the context takes any value (console.log's arguments) or
// undefined, or it is the left of `??` or an operand of `===`, it stays dynamic, and
// anywhere else it is converted to a string with a check.
func (l *lowerer) jsonStringify(call *ast.CallExpression) ir.Expr {
	if l.hasSpreadArgument(call) {
		return nil
	}
	args := make([]ir.Expr, 3)
	for i := range args {
		args[i] = &ir.Box{X: &ir.UndefinedConst{}}
		if i < len(call.Arguments.Nodes) {
			x := l.valueExpression(call.Arguments.Nodes[i])
			if x == nil {
				return nil
			}
			args[i] = box(x)
		}
	}
	result := ir.Expr(&ir.CallBuiltin{Builtin: ir.JSONStringify, Args: args})
	if contextual := l.contextualType(call.AsNode()); contextual != nil && (contextual.Flags()&(checker.TypeFlagsAny|checker.TypeFlagsUnknown) != 0 || l.contextTakesUndefined(call.AsNode())) {
		return result
	}
	if isNullishLeft(call.AsNode()) || isEqualityOperand(call.AsNode()) {
		return result
	}
	return &ir.FromUnion{X: result, To: ir.String}
}

// isEqualityOperand reports whether node is an operand of `===`, `!==`, `==` or `!=`,
// which take any value.
func isEqualityOperand(node *ast.Node) bool {
	for node.Parent.Kind == ast.KindParenthesizedExpression {
		node = node.Parent
	}
	if node.Parent.Kind != ast.KindBinaryExpression {
		return false
	}
	switch node.Parent.AsBinaryExpression().OperatorToken.Kind {
	case ast.KindEqualsEqualsEqualsToken, ast.KindExclamationEqualsEqualsToken, ast.KindEqualsEqualsToken, ast.KindExclamationEqualsToken:
		return true
	}
	return false
}

// isNullishLeft reports whether node is the left operand of `??`, which takes any value.
func isNullishLeft(node *ast.Node) bool {
	for node.Parent.Kind == ast.KindParenthesizedExpression {
		node = node.Parent
	}
	parent := node.Parent
	return parent.Kind == ast.KindBinaryExpression && parent.AsBinaryExpression().OperatorToken.Kind == ast.KindQuestionQuestionToken && parent.AsBinaryExpression().Left == node
}
