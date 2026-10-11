package lower

import (
	"fmt"
	"math"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/pts/ir"
)

// mathFunctions are the functions of Math (D-060, D-084). Math.max, Math.min and
// Math.hypot take any number of arguments (variadicMath); the others a fixed number.
var mathFunctions = map[string]*ir.Builtin{
	"sqrt": ir.MathSqrt, "abs": ir.MathAbs, "floor": ir.MathFloor, "ceil": ir.MathCeil, "round": ir.MathRound,
	"trunc": ir.MathTrunc, "sign": ir.MathSign, "max": ir.MathMax, "min": ir.MathMin,
	"acos": ir.MathAcos, "acosh": ir.MathAcosh, "asin": ir.MathAsin, "asinh": ir.MathAsinh, "atan": ir.MathAtan,
	"atanh": ir.MathAtanh, "atan2": ir.MathAtan2, "cbrt": ir.MathCbrt, "clz32": ir.MathClz32, "cos": ir.MathCos,
	"cosh": ir.MathCosh, "exp": ir.MathExp, "expm1": ir.MathExpm1, "fround": ir.MathFround, "imul": ir.MathImul,
	"log": ir.MathLog, "log10": ir.MathLog10, "log1p": ir.MathLog1p, "log2": ir.MathLog2, "pow": ir.MathPow,
	"random": ir.MathRandom, "sin": ir.MathSin, "sinh": ir.MathSinh, "tan": ir.MathTan, "tanh": ir.MathTanh,
	"hypot": ir.MathHypot,
}

// variadicMath are the builtins of Math's functions that take any number of
// arguments, as one array.
var variadicMath = map[string]*ir.Builtin{"max": ir.MathMaxOf, "min": ir.MathMinOf, "hypot": ir.MathHypot}

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

// mathConstant lowers a property of Math read as a value: a constant (`Math.PI`) or a
// function (`xs.map(Math.sqrt)`).
func (l *lowerer) mathConstant(node *ast.Node, name string) ir.Expr {
	if value, ok := mathConstants[name]; ok {
		return &ir.NumberConst{Value: value}
	}
	if _, ok := mathFunctions[name]; ok {
		return l.mathFunctionValue(node, name)
	}
	l.notSupportedYet(node, fmt.Sprintf("`Math.%s` as a value is", name))
	return nil
}

// mathCall lowers a call of a function of Math: each argument converted to a number (JS's
// ToNumber), in order. Math.max and Math.min of no arguments are -Infinity and Infinity,
// of one its value, and of more the pairwise maximum or minimum, left to right; with a
// spread argument, and for Math.hypot, they take the arguments as one array.
func (l *lowerer) mathCall(call *ast.CallExpression, name string) ir.Expr {
	builtin, ok := mathFunctions[name]
	if !ok {
		l.notSupportedYet(call.Expression, fmt.Sprintf("`Math.%s` is", name))
		return nil
	}
	var values []ir.Expr
	var at []*ast.Node
	spread := false
	for _, argument := range call.Arguments.Nodes {
		lowered := l.argumentValues(argument)
		if lowered == nil {
			return nil
		}
		for _, value := range lowered {
			if _, ok := value.(*spreadValue); ok {
				spread = true
			} else if value = l.toNumberOrReport(value, argument); value == nil {
				return nil
			}
			values, at = append(values, value), append(at, argument)
		}
	}
	if array, ok := variadicMath[name]; ok && (spread || builtin == ir.MathHypot) {
		args := l.restArray(values, at, ir.NumberArray)
		if args == nil {
			return nil
		}
		return &ir.CallBuiltin{Builtin: array, Args: []ir.Expr{args}}
	}
	if spread {
		l.notSupportedYet(call.Expression, fmt.Sprintf("spreading arguments into `Math.%s` is", name))
		return nil
	}
	if _, ok := variadicMath[name]; !ok {
		if len(values) != len(builtin.Params) {
			l.notSupportedYet(call.Expression, fmt.Sprintf("`Math.%s` with %d arguments is", name, len(values)))
			return nil
		}
		return &ir.CallBuiltin{Builtin: builtin, Args: values}
	}
	switch {
	case len(values) == 0 && builtin == ir.MathMax:
		return &ir.NumberConst{Value: math.Inf(-1)}
	case len(values) == 0:
		return &ir.NumberConst{Value: math.Inf(1)}
	case len(values) == 1:
		return values[0] // a number's maximum and minimum are itself, NaN and -0 included
	}
	result := values[0]
	for _, arg := range values[1:] {
		result = &ir.CallBuiltin{Builtin: builtin, Args: []ir.Expr{result, arg}}
	}
	return result
}

// mathFunctionValue is a function of Math used as a value: a static closure of a function
// that calls it, with its numbers as parameters (a rest parameter for Math.max, Math.min
// and Math.hypot), made the first time.
func (l *lowerer) mathFunctionValue(node *ast.Node, name string) ir.Expr {
	key := helperKey{method: "Math." + name}
	fn, ok := l.helpers[key]
	if !ok {
		at := l.pos(node)
		fn = &ir.Func{Name: "Math." + name, Module: l.function.Module, Pos: at, Result: ir.Number, JSName: name}
		l.inFunction(fn, func() {
			var result ir.Expr
			if array, ok := variadicMath[name]; ok {
				values := l.newLocal("values", l.restOf(ir.NumberArray))
				fn.Params = []*ir.Local{values}
				result = &ir.CallBuiltin{Builtin: array, Args: []ir.Expr{&ir.RestArgs{X: &ir.LocalRef{Local: values}}}}
			} else {
				builtin := mathFunctions[name]
				args := make([]ir.Expr, len(builtin.Params))
				for i := range builtin.Params {
					param := l.newLocal(fmt.Sprintf("x%d", i+1), ir.Number)
					fn.Params = append(fn.Params, param)
					args[i] = &ir.LocalRef{Local: param}
				}
				result = &ir.CallBuiltin{Builtin: builtin, Args: args}
			}
			fn.Body = &ir.Block{At: at, Stmts: []ir.Stmt{&ir.Return{At: at, Value: result}}}
		})
		l.helpers[key] = fn
	}
	params := make([]ir.Type, len(fn.Params))
	for i, param := range fn.Params {
		params[i] = param.Type
	}
	return &ir.MakeClosure{Func: fn, FuncType: l.funcType(params, ir.Number)}
}
