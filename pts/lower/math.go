package lower

import (
	"fmt"
	"math"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/pts/ir"
)

// mathFunctions are the functions of Math pulled forward from M3.5 for the benchmarks
// (M2.7); Math.max and Math.min take any number of arguments.
var mathFunctions = map[string]*ir.Builtin{
	"sqrt": ir.MathSqrt, "abs": ir.MathAbs, "floor": ir.MathFloor, "ceil": ir.MathCeil, "round": ir.MathRound,
	"trunc": ir.MathTrunc, "sign": ir.MathSign, "max": ir.MathMax, "min": ir.MathMin,
}

// mathConstants are the constant properties of Math.
var mathConstants = map[string]float64{
	"E": math.E, "LN10": math.Ln10, "LN2": math.Ln2, "LOG10E": math.Log10E, "LOG2E": math.Log2E,
	"PI": math.Pi, "SQRT1_2": math.Sqrt2 / 2, "SQRT2": math.Sqrt2,
}

// mathMember returns the name of the member of the global Math that the property access
// node reads, or "".
func (l *lowerer) mathMember(node *ast.Node) string {
	if node.Kind != ast.KindPropertyAccessExpression {
		return ""
	}
	object := node.Expression()
	if object.Kind != ast.KindIdentifier || object.Text() != "Math" || !l.isDeclaredInDefaultLib(l.target(object)) {
		return ""
	}
	return node.AsPropertyAccessExpression().Name().Text()
}

// mathConstant lowers a constant property of Math (`Math.PI`), or reports another.
func (l *lowerer) mathConstant(node *ast.Node, name string) ir.Expr {
	if value, ok := mathConstants[name]; ok {
		return &ir.NumberConst{Value: value}
	}
	l.notSupportedYet(node, fmt.Sprintf("`Math.%s` as a value is", name))
	return nil
}

// mathCall lowers a call of a function of Math: each argument converted to a number, in
// order. Math.max and Math.min of no arguments are -Infinity and Infinity, of one its
// value, and of more the pairwise maximum or minimum, left to right.
func (l *lowerer) mathCall(call *ast.CallExpression, name string) ir.Expr {
	builtin, ok := mathFunctions[name]
	if !ok {
		l.notSupportedYet(call.Expression, fmt.Sprintf("`Math.%s` is", name))
		return nil
	}
	if l.hasSpreadArgument(call) {
		return nil
	}
	args := make([]ir.Expr, len(call.Arguments.Nodes))
	for i, argument := range call.Arguments.Nodes {
		arg := l.expression(argument)
		if arg == nil {
			return nil
		}
		if !ir.IsNumeric(arg.Type()) {
			l.notSupportedYet(argument, fmt.Sprintf("`Math.%s` of a %s is", name, arg.Type()))
			return nil
		}
		args[i] = l.convert(arg, ir.Number, argument)
	}
	if len(builtin.Params) == 1 {
		if len(args) != 1 {
			l.notSupportedYet(call.Expression, fmt.Sprintf("`Math.%s` with %d arguments is", name, len(args)))
			return nil
		}
		return &ir.CallBuiltin{Builtin: builtin, Args: args}
	}
	switch {
	case len(args) == 0 && builtin == ir.MathMax:
		return &ir.NumberConst{Value: math.Inf(-1)}
	case len(args) == 0:
		return &ir.NumberConst{Value: math.Inf(1)}
	case len(args) == 1:
		return args[0] // a number's maximum and minimum are itself, NaN and -0 included
	}
	result := args[0]
	for _, arg := range args[1:] {
		result = &ir.CallBuiltin{Builtin: builtin, Args: []ir.Expr{result, arg}}
	}
	return result
}
