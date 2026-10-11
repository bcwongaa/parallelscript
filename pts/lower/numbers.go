package lower

import (
	"fmt"
	"math"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/pts/ir"
)

// The Number methods and statics, the global number functions, and Number, String and
// Boolean as conversions and as values (D-083).

// numberConstants are the constant properties of the global Number.
var numberConstants = map[string]float64{
	"EPSILON": math.Pow(2, -52), "MAX_SAFE_INTEGER": 1<<53 - 1, "MIN_SAFE_INTEGER": -(1<<53 - 1),
	"MAX_VALUE": math.MaxFloat64, "MIN_VALUE": 5e-324, "POSITIVE_INFINITY": math.Inf(1), "NEGATIVE_INFINITY": math.Inf(-1),
	"NaN": math.NaN(),
}

// numberTests are Number's predicates, of a number and of a dynamic value.
var numberTests = map[string][2]*ir.Builtin{
	"isInteger":     {ir.NumberIsInteger, ir.ValueIsInteger},
	"isSafeInteger": {ir.NumberIsSafeInteger, ir.ValueIsSafeInteger},
	"isFinite":      {ir.NumberIsFinite, ir.ValueIsFinite},
	"isNaN":         {ir.NumberIsNaN, ir.ValueIsNaN},
}

// numberConstant lowers a read of `Number.NAME`, if node is one.
func (l *lowerer) numberConstant(node *ast.Node) (ir.Expr, bool) {
	if node.Kind != ast.KindPropertyAccessExpression {
		return nil, false
	}
	value, ok := numberConstants[node.AsPropertyAccessExpression().Name().Text()]
	if !ok || !l.isGlobalMember(node, "Number", node.AsPropertyAccessExpression().Name().Text()) {
		return nil, false
	}
	return &ir.NumberConst{Value: value}, true
}

// numberLibraryCall lowers a call of a Number method or static, of parseInt, parseFloat,
// isNaN or isFinite, or of Number or Boolean as a conversion; handled is false if the
// call is none of these.
func (l *lowerer) numberLibraryCall(call *ast.CallExpression) (x ir.Expr, handled bool) {
	callee := ast.SkipParentheses(call.Expression)
	if callee.Kind == ast.KindPropertyAccessExpression {
		return l.numberMemberCall(call, callee)
	}
	if callee.Kind != ast.KindIdentifier || !l.isDeclaredInDefaultLib(l.target(callee)) {
		return nil, false
	}
	switch name := callee.Text(); name {
	case "parseInt", "parseFloat":
		return l.parseNumber(call, name), true
	case "isNaN", "isFinite": // of ToNumber of the argument
		if l.hasSpreadArgument(call) {
			return nil, true
		}
		x := l.expression(call.Arguments.Nodes[0])
		if x == nil {
			return nil, true
		}
		builtin := ir.NumberIsNaN
		if name == "isFinite" {
			builtin = ir.NumberIsFinite
		}
		return &ir.CallBuiltin{Builtin: builtin, Args: []ir.Expr{l.toNumberOrReport(x, call.Arguments.Nodes[0])}}, true
	case "Number":
		return l.numberConversion(call), true
	case "Boolean":
		if l.hasSpreadArgument(call) {
			return nil, true
		}
		if len(call.Arguments.Nodes) == 0 {
			return &ir.BoolConst{Value: false}, true
		}
		return l.condition(call.Arguments.Nodes[0]), true
	}
	return nil, false
}

// numberMemberCall lowers a call of a Number method or static (numberLibraryCall).
func (l *lowerer) numberMemberCall(call *ast.CallExpression, callee *ast.Node) (x ir.Expr, handled bool) {
	switch member := l.libMemberName(callee); member {
	case "Number.toFixed", "Number.toExponential", "Number.toPrecision", "Number.toString", "Number.toLocaleString", "Number.valueOf":
		return l.numberMethodCall(call, callee.Name().Text()), true
	case "NumberConstructor.isInteger", "NumberConstructor.isSafeInteger", "NumberConstructor.isFinite", "NumberConstructor.isNaN":
		return l.numberTest(call, callee.Name().Text()), true
	case "NumberConstructor.parseInt", "NumberConstructor.parseFloat":
		return l.parseNumber(call, callee.Name().Text()), true
	}
	return nil, false
}

// numberConversion lowers `Number(x)`: JS's ToNumber of x, 0 without an argument.
func (l *lowerer) numberConversion(call *ast.CallExpression) ir.Expr {
	args := call.Arguments.Nodes
	if l.hasSpreadArgument(call) {
		return nil
	}
	if len(args) == 0 {
		return &ir.NumberConst{Value: 0}
	}
	x := l.valueExpression(args[0])
	if x == nil {
		return nil
	}
	return l.toNumberOrReport(x, args[0])
}

// toNumberOrReport is JS's ToNumber of x, or nil (reported at node) if x's type has no
// dynamic value to convert.
func (l *lowerer) toNumberOrReport(x ir.Expr, node *ast.Node) ir.Expr {
	if !convertsToNumber(x.Type()) {
		l.notSupportedYet(node, fmt.Sprintf("converting %s to a number is", x.Type()))
		return nil
	}
	return toNumber(x) // other values through their dynamic value: strings parse, objects convert as their strings
}

// convertsToNumber reports whether toNumber can convert a value of type t.
func convertsToNumber(t ir.Type) bool {
	return ir.IsNumeric(t) || t == ir.String || t == ir.Bool || t == ir.Null || t == ir.Undefined || t == ir.Value ||
		isUnion(t) || isObject(t) || isFunction(t) || isMap(t)
}

// numberMethodCall lowers a call of a method of Number.prototype on a number or a value of
// an integer type.
func (l *lowerer) numberMethodCall(call *ast.CallExpression, name string) ir.Expr {
	args := call.Arguments.Nodes
	if l.hasSpreadArgument(call) {
		return nil
	}
	receiver := l.expression(ast.SkipParentheses(call.Expression).Expression())
	if receiver == nil {
		return nil
	}
	if name == "toLocaleString" {
		if len(args) > 0 && !isUndefined(args[0]) && !isEnglishLocale(args[0]) || len(args) > 1 && !isUndefined(args[1]) {
			l.notSupportedYet(args[0], "`toLocaleString` with a locale other than English or options is")
			return nil
		}
		return &ir.CallBuiltin{Builtin: ir.NumberToLocaleString, Args: []ir.Expr{toNumber(receiver)}}
	}
	arg := ir.Expr(nil)
	if len(args) > 0 && !isUndefined(args[0]) {
		if arg = l.expression(args[0]); arg == nil {
			return nil
		}
		if arg = l.convert(arg, ir.Number, args[0]); arg == nil {
			return nil
		}
	}
	switch {
	case name == "valueOf":
		return receiver
	case name == "toString" && arg == nil:
		return &ir.ToString{X: receiver} // every digit of a 64-bit integer
	case name == "toString" && receiver.Type() == ir.I64:
		return &ir.CallBuiltin{Builtin: ir.I64ToStringRadix, Args: []ir.Expr{receiver, arg}}
	case name == "toString" && receiver.Type() == ir.U64:
		return &ir.CallBuiltin{Builtin: ir.U64ToStringRadix, Args: []ir.Expr{receiver, arg}}
	case name == "toString":
		return &ir.CallBuiltin{Builtin: ir.NumberToStringRadix, Args: []ir.Expr{toNumber(receiver), arg}}
	case name == "toPrecision" && arg == nil:
		return &ir.ToString{X: toNumber(receiver)}
	case name == "toExponential" && arg == nil:
		return &ir.CallBuiltin{Builtin: ir.NumberToExponentialShortest, Args: []ir.Expr{toNumber(receiver)}}
	case name == "toFixed" && arg == nil:
		arg = &ir.NumberConst{Value: 0}
	}
	builtin := map[string]*ir.Builtin{"toFixed": ir.NumberToFixed, "toExponential": ir.NumberToExponential, "toPrecision": ir.NumberToPrecision}[name]
	return &ir.CallBuiltin{Builtin: builtin, Args: []ir.Expr{toNumber(receiver), arg}}
}

// numberTest lowers Number.isInteger, isSafeInteger, isFinite and isNaN, which take any
// value and convert none: a value that isn't a number is never one.
func (l *lowerer) numberTest(call *ast.CallExpression, name string) ir.Expr {
	if l.hasSpreadArgument(call) {
		return nil
	}
	if len(call.Arguments.Nodes) == 0 {
		return &ir.BoolConst{Value: false}
	}
	x := l.valueExpression(call.Arguments.Nodes[0])
	if x == nil {
		return nil
	}
	tests := numberTests[name]
	if ir.IsNumeric(x.Type()) {
		return &ir.CallBuiltin{Builtin: tests[0], Args: []ir.Expr{toNumber(x)}}
	}
	if x.Type() == ir.Void {
		return discard(x, &ir.BoolConst{Value: false})
	}
	return &ir.CallBuiltin{Builtin: tests[1], Args: []ir.Expr{box(x)}}
}

// parseNumber lowers parseInt(string, radix) and parseFloat(string), their argument
// converted to a string.
func (l *lowerer) parseNumber(call *ast.CallExpression, name string) ir.Expr {
	args := call.Arguments.Nodes
	if l.hasSpreadArgument(call) {
		return nil
	}
	text := l.valueExpression(args[0])
	if text == nil {
		return nil
	}
	if text = l.toString(text, args[0]); text == nil {
		return nil
	}
	if name == "parseFloat" {
		return &ir.CallBuiltin{Builtin: ir.ParseFloat, Args: []ir.Expr{text}}
	}
	radix := ir.Expr(&ir.NumberConst{Value: 0})
	if len(args) > 1 && !isUndefined(args[1]) {
		if radix = l.expression(args[1]); radix == nil {
			return nil
		}
		if radix = l.convert(radix, ir.Number, args[1]); radix == nil {
			return nil
		}
	}
	return &ir.CallBuiltin{Builtin: ir.ParseInt, Args: []ir.Expr{text, radix}}
}

// conversionFunctions are the global functions that convert a value, used as values
// (`xs.map(Number)`, `xs.filter(Boolean)`).
var conversionFunctions = map[string]ir.Type{"Number": ir.Number, "String": ir.String, "Boolean": ir.Bool}

// conversionFunction is the value of the global String, Number or Boolean: a static
// closure of a function that converts its one argument, made the first time.
func (l *lowerer) conversionFunction(name string, node *ast.Node) ir.Expr {
	key := helperKey{method: "global " + name}
	fn, ok := l.helpers[key]
	if !ok {
		at := l.pos(node)
		fn = &ir.Func{Name: name, Module: l.function.Module, Pos: at, Result: conversionFunctions[name], JSName: name, Length: 1}
		l.inFunction(fn, func() {
			value := l.newLocal("value", ir.Value)
			fn.Params = []*ir.Local{value}
			var converted ir.Expr = &ir.LocalRef{Local: value}
			switch name {
			case "Number":
				converted = toNumber(converted)
			case "String":
				converted = &ir.ToString{X: converted}
			default:
				converted = &ir.Truthy{X: converted}
			}
			fn.Body = &ir.Block{At: at, Stmts: []ir.Stmt{&ir.Return{At: at, Value: converted}}}
		})
		l.helpers[key] = fn
	}
	return &ir.MakeClosure{Func: fn, FuncType: l.funcType([]ir.Type{ir.Value}, fn.Result)}
}

// isConversionFunction reports whether node is the global String, Number or Boolean.
func (l *lowerer) isConversionFunction(node *ast.Node) bool {
	_, ok := conversionFunctions[node.Text()]
	return ok && node.Kind == ast.KindIdentifier && l.isDeclaredInDefaultLib(l.target(node))
}

// numberTestValue is Number.isInteger, isSafeInteger, isFinite or isNaN used as a value
// (`xs.some(Number.isNaN)`): a static closure of a function that tests its one argument,
// made the first time (D-089).
func (l *lowerer) numberTestValue(node *ast.Node, name string) ir.Expr {
	key := helperKey{method: "Number." + name}
	fn, ok := l.helpers[key]
	if !ok {
		at := l.pos(node)
		fn = &ir.Func{Name: "Number." + name, Module: l.function.Module, Pos: at, Result: ir.Bool, JSName: name, Length: 1}
		l.inFunction(fn, func() {
			value := l.newLocal("value", ir.Value)
			fn.Params = []*ir.Local{value}
			test := &ir.CallBuiltin{Builtin: numberTests[name][1], Args: []ir.Expr{&ir.LocalRef{Local: value}}}
			fn.Body = &ir.Block{At: at, Stmts: []ir.Stmt{&ir.Return{At: at, Value: test}}}
		})
		l.helpers[key] = fn
	}
	return &ir.MakeClosure{Func: fn, FuncType: l.funcType([]ir.Type{ir.Value}, ir.Bool)}
}
