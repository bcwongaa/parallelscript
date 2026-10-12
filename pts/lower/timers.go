package lower

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/pts/ir"
	"github.com/microsoft/typescript-go/pts/lib"
)

// Timers, immediates, queueMicrotask and performance.now() (D-114), which the prelude
// declares. Timeouts and immediates are dynamic values, objects of the runtime's event
// loop; the callbacks are boxed closures that it calls through their dynamic entries.

// isTimerType reports whether t is the prelude's Timeout or Immediate.
func (l *lowerer) isTimerType(t *checker.Type) bool {
	symbol := t.Symbol()
	return symbol != nil && t.Flags()&checker.TypeFlagsObject != 0 && (symbol.Name == "Timeout" || symbol.Name == "Immediate") && isPreludeSymbol(symbol)
}

// isPreludeSymbol reports whether symbol is declared in the prelude.
func isPreludeSymbol(symbol *ast.Symbol) bool {
	return len(symbol.Declarations) > 0 && lib.IsLibPath(ast.GetSourceFileOfNode(symbol.Declarations[0]).FileName())
}

// timerCall lowers a call of the prelude's timer functions and of the methods of
// timeouts and performance; handled is false for other calls.
func (l *lowerer) timerCall(call *ast.CallExpression) (x ir.Expr, handled bool) {
	callee := ast.SkipParentheses(call.Expression)
	var name string
	switch {
	case callee.Kind == ast.KindIdentifier:
		symbol := l.checker.GetSymbolAtLocation(callee)
		if symbol == nil || symbol.Flags&ast.SymbolFlagsFunction == 0 || !isPreludeSymbol(symbol) {
			return nil, false
		}
		name = symbol.Name
	case callee.Kind == ast.KindPropertyAccessExpression:
		name = l.libMemberName(callee)
	default:
		return nil, false
	}
	args := call.Arguments.Nodes
	if l.hasSpread(args) {
		switch name {
		case "setTimeout", "setInterval", "setImmediate", "clearTimeout", "clearInterval", "clearImmediate", "queueMicrotask":
			l.notSupportedYet(call.AsNode(), "spread arguments of `"+name+"` are")
			return nil, true
		}
		return nil, false
	}
	switch name {
	case "setTimeout", "setInterval":
		values := l.boxedArguments(args, 3)
		if values == nil {
			return nil, true
		}
		rest := l.anyArray(values[2:])
		return &ir.CallBuiltin{Builtin: ir.TimerSet, Args: []ir.Expr{values[0], values[1], rest, &ir.BoolConst{Value: name == "setInterval"}}}, true
	case "setImmediate":
		values := l.boxedArguments(args, 1)
		if values == nil {
			return nil, true
		}
		return &ir.CallBuiltin{Builtin: ir.ImmediateSet, Args: []ir.Expr{values[0], l.anyArray(values[1:])}}, true
	case "clearTimeout", "clearInterval", "clearImmediate", "queueMicrotask":
		values := l.boxedArguments(args, 1)
		if values == nil {
			return nil, true
		}
		builtins := map[string]*ir.Builtin{"clearTimeout": ir.TimerClear, "clearInterval": ir.TimerClear, "clearImmediate": ir.ImmediateClear, "queueMicrotask": ir.QueueMicrotask}
		return &ir.CallBuiltin{Builtin: builtins[name], Args: values[:1]}, true
	case "Timeout.ref", "Timeout.unref", "Timeout.hasRef", "Timeout.refresh":
		timer := l.expression(callee.Expression())
		if timer == nil {
			return nil, true
		}
		switch name {
		case "Timeout.hasRef":
			return &ir.CallBuiltin{Builtin: ir.TimerHasRef, Args: []ir.Expr{box(timer)}}, true
		case "Timeout.refresh":
			return &ir.CallBuiltin{Builtin: ir.TimerRefresh, Args: []ir.Expr{box(timer)}}, true
		}
		return &ir.CallBuiltin{Builtin: ir.TimerRef, Args: []ir.Expr{box(timer), &ir.BoolConst{Value: name == "Timeout.ref"}}}, true
	case "Performance.now":
		return &ir.CallBuiltin{Builtin: ir.PerformanceNow}, true
	}
	return nil, false
}

// boxedArguments lowers a call's arguments to dynamic values, undefined for those left
// out up to count; nil if one failed, which was reported.
func (l *lowerer) boxedArguments(args []*ast.Node, count int) []ir.Expr {
	var values []ir.Expr
	for _, arg := range args {
		value := l.valueExpression(arg)
		if value == nil {
			return nil
		}
		values = append(values, box(value))
	}
	for len(values) < count {
		values = append(values, box(&ir.UndefinedConst{}))
	}
	return values
}

// anyArray is a new any[] of values.
func (l *lowerer) anyArray(values []ir.Expr) ir.Expr {
	return &ir.NewArray{Array: ir.ValueArray, Elems: values}
}
