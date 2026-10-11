package lower

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/pts/ir"
)

// JSON (D-080): JSON.parse makes dynamic values and JSON.stringify reads any value, at
// run time, through descriptors.

// jsonParse lowers JSON.parse(text): the runtime's parse of text, converted to a string
// as JS's ToString does if it is a dynamic value. A reviver is PTS1001.
func (l *lowerer) jsonParse(call *ast.CallExpression) ir.Expr {
	args := call.Arguments.Nodes
	switch {
	case l.hasSpreadArgument(call):
		return nil
	case len(args) > 1:
		l.notSupportedYet(args[1], "the reviver argument of `JSON.parse` is")
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
	return &ir.CallBuiltin{Builtin: ir.JSONParse, Args: []ir.Expr{text}}
}

// jsonStringify lowers JSON.stringify(value, replacer, space): the runtime's, of the
// arguments boxed. tsc types the result as a string, but it is undefined for undefined
// and functions: where the context takes any value (console.log's arguments) or
// undefined it stays dynamic, and anywhere else it is converted to a string with a check.
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
	return &ir.FromUnion{X: result, To: ir.String}
}
