package lower

import (
	"fmt"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/pts/ir"
)

// ---------------------------------------------------------------- methods in object literals

// objectMethod lowers a method of an object literal (node) to the closure its field holds
// (D-044): a closure function that takes the object first, as `this`, then the method's
// parameters, and may capture variables like any closure.
func (l *lowerer) objectMethod(node *ast.Node, field *ir.Field) ir.Expr {
	flags := ast.GetFunctionFlags(node)
	switch {
	case node.TypeParameterList() != nil:
		l.notSupportedYet(node.Name(), "generic methods are")
		return nil
	case flags&ast.FunctionFlagsGenerator != 0:
		l.notSupportedYet(node.Name(), "generator methods are")
		return nil
	case flags&ast.FunctionFlagsAsync != 0:
		l.notSupportedYet(node.Name(), "async methods are")
		return nil
	}
	ft := field.Type.(*ir.FuncType)
	name := field.Name
	fn := &ir.Func{Name: l.closureName(name, node), Module: l.function.Module, Pos: l.pos(node), Result: ft.Result,
		Closure: true, Outer: l.function, JSName: name}
	l.funcNodes[fn] = node
	outer := l.function
	l.function = fn // parameters are locals of fn
	fn.Params = []*ir.Local{l.newThis(fn, field.Class)}
	failed := false
	for i, param := range node.Parameters() {
		local := l.parameter(param)
		if local == nil {
			failed = true
			break
		}
		if i+1 >= len(ft.Params) || local.Type != ft.Params[i+1] {
			l.notSupportedYet(param, fmt.Sprintf("a method whose parameter types differ from its object type's (%s) is", ft))
			failed = true
			break
		}
		fn.Params = append(fn.Params, local)
	}
	for i := len(fn.Params); i < len(ft.Params) && !failed; i++ {
		fn.Params = append(fn.Params, l.newLocal("unused", ft.Params[i]))
	}
	l.function = outer
	if failed {
		return nil
	}
	l.inFunction(fn, func() { fn.Body = l.block(node.Body(), node.Body().Statements()) })
	return &ir.MakeClosure{Func: fn, FuncType: ft}
}

// withReceiver evaluates receiver once and builds an expression that uses it twice:
// directly if it is a local, and otherwise through a temporary.
func (l *lowerer) withReceiver(receiver ir.Expr, build func(object ir.Expr) ir.Expr) ir.Expr {
	if isPure(receiver) {
		return build(receiver)
	}
	object := l.newLocal("object", receiver.Type())
	return &ir.Bind{Local: object, Value: receiver, In: build(&ir.LocalRef{Local: object})}
}

// ---------------------------------------------------------------- methods as values

// method is a method that `o.m` names, with what calling it on an object takes.
type method struct {
	name     string
	receiver ir.Type                                        // the type of the object it is called on
	params   []ir.Type                                      // after `this`
	result   ir.Type                                        // Void if it returns nothing
	call     func(receiver ir.Expr, args []ir.Expr) ir.Expr // calls it on receiver, a value of type receiver
}

// methodOf returns the method a property access names on its object (lowered as
// receiver): a class's method, a method of an object type through an interface, or an
// object literal's method; ok is false for anything else. It reports (and returns ok
// with a nil call) a method that can't be called this way.
func (l *lowerer) methodOf(access *ast.Node, receiver ir.Expr) (m method, ok bool) {
	member := l.memberDeclaration(access)
	name := access.AsPropertyAccessExpression().Name().Text()
	if classMethod, isMethod := l.methods[member]; isMethod {
		if classMethod == nil || classMethod.Kind != ir.Plain {
			return method{}, classMethod == nil
		}
		params := make([]ir.Type, len(classMethod.Func.Params)-1)
		for i, param := range classMethod.Func.Params[1:] {
			params[i] = param.Type
		}
		return method{name, receiver.Type(), params, classMethod.Func.Result, func(object ir.Expr, args []ir.Expr) ir.Expr {
			return l.callMethod(object, access.Expression(), classMethod, args)
		}}, true
	}
	if !l.isObjectTypeMethod(member) {
		return method{}, false
	}
	switch t := receiver.Type().(type) {
	case *ir.Class:
		if field := t.Field(name); t.Shape && field != nil && field.Method {
			ft := field.Type.(*ir.FuncType)
			return method{name, t, ft.Params[1:], ft.Result, func(receiver ir.Expr, args []ir.Expr) ir.Expr {
				return l.withReceiver(receiver, func(object ir.Expr) ir.Expr {
					return &ir.CallValue{Callee: &ir.FieldRef{X: object, Field: field}, Args: append([]ir.Expr{object}, args...)}
				})
			}}, true
		}
	case *ir.Interface:
		if interfaceMethod := t.Method(name); interfaceMethod != nil {
			return method{name, t, interfaceMethod.Params, interfaceMethod.Result, func(object ir.Expr, args []ir.Expr) ir.Expr {
				return &ir.InterfaceCall{Method: interfaceMethod, Args: append([]ir.Expr{object}, args...)}
			}}, true
		}
	}
	l.notSupportedYet(access, fmt.Sprintf("calling a method of an object type on a %s is", receiver.Type()))
	return method{}, true
}

// methodValue lowers `o.m` for a method m used as a value (D-044): a static method is a
// function; any other method gives a closure bound to o, which calls o.m with its
// arguments, as `o.m.bind(o)` would.
func (l *lowerer) methodValue(node *ast.Node) ir.Expr {
	if fn, ok := l.staticMethods[l.memberDeclaration(node)]; ok {
		if fn == nil {
			return nil // reported
		}
		return l.functionValue(node, fn)
	}
	receiver := l.expression(node.Expression())
	if receiver == nil {
		return nil
	}
	m, ok := l.methodOf(node, receiver)
	if !ok || m.call == nil {
		if !ok {
			l.notSupportedYet(node, "this method as a value is")
		}
		return nil
	}
	return l.boundMethod(node, m, receiver, nil, m.name, false)
}

// boundMethod makes a closure that calls m on receiver with the arguments bound (values
// for its first parameters) and then its own: a method value, or what `bind` makes. A
// closure that binds a function value (binds) shows as "bound " and that function's name.
func (l *lowerer) boundMethod(node *ast.Node, m method, receiver ir.Expr, bound []ir.Expr, name string, binds bool) ir.Expr {
	if len(bound) > len(m.params) {
		bound = bound[:len(m.params)] // JS ignores the extra ones; tsc rejects them anyway
	}
	captured := []*ir.Local{l.newLocal("receiver", m.receiver)}
	values := []ir.Expr{receiver}
	for i, value := range bound {
		captured = append(captured, l.newLocal(fmt.Sprintf("bound%d", i+1), m.params[i]))
		values = append(values, value)
	}
	fn := &ir.Func{Name: l.closureName(name, node), Module: l.function.Module, Pos: l.pos(node), Result: m.result,
		Closure: true, Outer: l.function, JSName: name, Captures: captured, Binds: binds}
	params := m.params[len(bound):]
	l.inFunction(fn, func() {
		args := make([]ir.Expr, 0, len(m.params))
		for _, local := range captured[1:] {
			args = append(args, &ir.LocalRef{Local: local})
		}
		for i, t := range params {
			param := l.newLocal(fmt.Sprintf("arg%d", i+1), t)
			fn.Params = append(fn.Params, param)
			args = append(args, &ir.LocalRef{Local: param})
		}
		call := m.call(&ir.LocalRef{Local: captured[0]}, args)
		if m.result == ir.Void {
			fn.Body = &ir.Block{At: fn.Pos, Stmts: []ir.Stmt{&ir.ExprStmt{At: fn.Pos, X: call}}}
		} else {
			fn.Body = &ir.Block{At: fn.Pos, Stmts: []ir.Stmt{&ir.Return{At: fn.Pos, Value: call}}}
		}
	})
	var closure ir.Expr = &ir.MakeClosure{Func: fn, FuncType: l.funcType(params, m.result)}
	for i := len(captured) - 1; i >= 0; i-- {
		closure = &ir.Bind{Local: captured[i], Value: values[i], In: closure}
	}
	return closure
}

// ---------------------------------------------------------------- bind and call

// isFunctionMethod reports whether a callee is `f.bind`, `f.call` or `f.apply` of the
// ES library's Function.prototype, on a function or method f.
func (l *lowerer) isFunctionMethod(callee *ast.Node) bool {
	if callee.Kind != ast.KindPropertyAccessExpression {
		return false
	}
	symbol := l.checker.GetSymbolAtLocation(callee)
	if symbol == nil || symbol.Parent == nil || !l.isDeclaredInDefaultLib(symbol) {
		return false
	}
	switch symbol.Parent.Name {
	case "CallableFunction", "Function":
		return symbol.Name == "bind" || symbol.Name == "call" || symbol.Name == "apply"
	}
	return false
}

// functionMethodCall lowers `f.call(thisArg, args…)`, `f.apply(thisArg, [args…])` and
// `f.bind(thisArg, args…)` (D-044, D-045). For a method `o.m`, thisArg is the object it
// runs on; a function ignores it (it is still evaluated). `apply` takes its arguments as
// an array literal: other arrays would need tuple types (M2.6).
func (l *lowerer) functionMethodCall(call *ast.CallExpression) ir.Expr {
	callee := ast.SkipParentheses(call.Expression)
	name := callee.AsPropertyAccessExpression().Name().Text()
	if l.hasSpreadArgument(call) {
		return nil
	}
	args := call.Arguments.Nodes
	if name == "apply" {
		if args = appliedArguments(args); args == nil {
			l.notSupportedYet(call.Arguments.Nodes[1], "`apply` with arguments that aren't an array literal is")
			return nil
		}
		name = "call"
	}
	target := ast.SkipParentheses(callee.Expression())
	if target.Kind == ast.KindPropertyAccessExpression && l.isInstanceMethod(target) {
		return l.methodCallOrBind(call, args, target, name)
	}
	fn := l.expression(target)
	if fn == nil {
		return nil
	}
	ft, ok := fn.Type().(*ir.FuncType)
	if !ok {
		l.notSupportedYet(target, fmt.Sprintf("`%s` on a %s is", name, fn.Type()))
		return nil
	}
	var thisArg ir.Expr = &ir.UndefinedConst{}
	if len(args) > 0 {
		if thisArg = l.expression(args[0]); thisArg == nil {
			return nil
		}
		args = args[1:]
	}
	values := l.typedArguments(args, ft.Params[:min(len(args), len(ft.Params))])
	if values == nil && len(args) > 0 {
		return nil
	}
	if name == "call" {
		return l.withReceiver(fn, func(f ir.Expr) ir.Expr { // JS evaluates f before thisArg
			return discard(thisArg, &ir.CallValue{Callee: f, Args: values})
		})
	}
	m := method{name: "", receiver: ft, params: ft.Params, result: ft.Result, call: func(f ir.Expr, args []ir.Expr) ir.Expr {
		return &ir.CallValue{Callee: f, Args: args}
	}}
	return l.withReceiver(fn, func(f ir.Expr) ir.Expr {
		return discard(thisArg, l.boundMethod(callee, m, f, values, "bound", true))
	})
}

// appliedArguments are the arguments `apply(thisArg, [a, b])` passes, as `call`'s:
// thisArg, a, b; nil if the arguments aren't an array literal without spreads or holes.
func appliedArguments(args []*ast.Node) []*ast.Node {
	if len(args) < 2 {
		return args
	}
	list := ast.SkipParentheses(args[1])
	if len(args) > 2 || list.Kind != ast.KindArrayLiteralExpression {
		return nil
	}
	elements := list.AsArrayLiteralExpression().Elements.Nodes
	for _, element := range elements {
		if element.Kind == ast.KindSpreadElement || element.Kind == ast.KindOmittedExpression {
			return nil
		}
	}
	return append([]*ast.Node{args[0]}, elements...)
}

// isInstanceMethod reports whether a property access names a method that runs on its
// object: a class's instance method or a method of an object type.
func (l *lowerer) isInstanceMethod(access *ast.Node) bool {
	member := l.memberDeclaration(access)
	if classMethod, ok := l.methods[member]; ok {
		return classMethod == nil || classMethod.Kind == ir.Plain
	}
	return l.isObjectTypeMethod(member)
}

// sameReference reports whether two expressions name the same object: `this`, or the
// same variable, possibly through the same chain of property reads.
func sameReference(a *ast.Node, b *ast.Node) bool {
	a, b = ast.SkipParentheses(a), ast.SkipParentheses(b)
	switch {
	case a.Kind != b.Kind:
		return false
	case a.Kind == ast.KindThisKeyword:
		return true
	case a.Kind == ast.KindIdentifier:
		return a.Text() == b.Text()
	case a.Kind == ast.KindPropertyAccessExpression:
		return a.AsPropertyAccessExpression().Name().Text() == b.AsPropertyAccessExpression().Name().Text() && sameReference(a.Expression(), b.Expression())
	}
	return false
}

// methodCallOrBind lowers `o.m.call(thisArg, args…)`, which calls m on thisArg, and
// `o.m.bind(thisArg, args…)`, which makes a closure bound to thisArg. thisArg must be of
// o's type: for a class's method, the method thisArg's class has runs; for a method of an
// object type, thisArg must be o itself. o is evaluated for its effects, as JS reads o.m.
func (l *lowerer) methodCallOrBind(call *ast.CallExpression, args []*ast.Node, target *ast.Node, name string) ir.Expr {
	object := l.expression(target.Expression())
	if object == nil {
		return nil
	}
	m, ok := l.methodOf(target, object)
	if !ok || m.call == nil {
		return nil // reported
	}
	if len(args) == 0 {
		l.notSupportedYet(call.Expression, "calling a method without an object (`this` undefined) is")
		return nil
	}
	if _, isClass := l.methods[l.memberDeclaration(target)]; !isClass && !sameReference(target.Expression(), args[0]) {
		// JS runs o's own method, which may be a closure of o's, on the other object.
		l.notSupportedYet(args[0], "calling or binding a method of an object type with another object as `this` is")
		return nil
	}
	receiver := l.expression(args[0])
	if receiver == nil {
		return nil
	}
	if receiver = l.convert(receiver, m.receiver, args[0]); receiver == nil {
		return nil
	}
	rest := args[1:]
	values := l.typedArguments(rest, m.params[:min(len(rest), len(m.params))])
	if values == nil && len(rest) > 0 {
		return nil
	}
	var result ir.Expr
	if name == "call" {
		result = m.call(receiver, values)
	} else {
		result = l.boundMethod(call.Expression, m, receiver, values, "bound "+m.name, false)
	}
	return discard(object, result)
}
