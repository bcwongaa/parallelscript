package lower

import (
	"fmt"
	"slices"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/internal/scanner"
	"github.com/microsoft/typescript-go/pts/ir"
)

// ---------------------------------------------------------------- methods in object literals

// objectMethod lowers a method of an object literal (node) to the closure its field holds
// (D-044): a closure function that takes the object first, as `this`, then the method's
// parameters, and may capture variables like any closure.
func (l *lowerer) objectMethod(node *ast.Node, field *ir.Field) ir.Expr {
	flags := ast.GetFunctionFlags(node)
	switch {
	}
	var generic *genericFunc
	if typeParameters := l.checker.GetSignatureFromDeclaration(node).TypeParameters(); len(typeParameters) > 0 {
		if l.specialization == AllInstances && (l.subst == nil || len(l.subst.erased) == 0) {
			// Its instances run with the closures of its erased copy, as a generic arrow
			// function's do (D-190, D-191); in erased code, its type arguments would mention
			// the erased ones. As a method, they take the literal first.
			generic = &genericFunc{node: node, module: l.function, parameters: typeParameters, outer: l.subst}
			if field.Method {
				generic.class = field.Class
			}
		}
		outer := l.subst
		l.subst = l.erasing(typeParameters, nil) // its own, or its context's (D-078)
		defer func() { l.subst = outer }()
	}
	ft := fieldFuncType(field)
	if ft == nil {
		l.notSupportedYet(node.Name(), fmt.Sprintf("a method for a property of type %s is", field.Type))
		return nil
	}
	name := field.Name
	fn := &ir.Func{Name: l.closureName(name, node), Module: l.function.Module, Pos: l.pos(node), Result: ft.Result,
		Closure: true, Outer: l.function, JSName: jsMemberName(name), Length: jsLength(node)}
	stateMachine(fn, flags)
	l.funcNodes[fn] = node
	outer := l.function
	l.function = fn             // parameters are locals of fn
	asFunction := !field.Method // `push(x) { … }` for a `push: (x: number) => void`, called without `this`
	if asFunction && usesThis(node.Body()) {
		l.function = outer
		l.notSupportedYet(node.Name(), "a method that uses `this` as a property of a function type is")
		return nil
	}
	if !asFunction {
		fn.Params = []*ir.Local{l.newThis(fn, field.Class)}
	}
	differs := false // its parameters, as declared, aren't its field's
	for _, param := range node.Parameters() {
		locals := l.parameter(param)
		if locals == nil {
			l.function = outer
			return nil
		}
		for _, local := range locals {
			differs = differs || len(fn.Params) >= len(ft.Params) || local.Type != ft.Params[len(fn.Params)]
			fn.Params = append(fn.Params, local)
		}
	}
	for i := len(fn.Params); i < len(ft.Params) && !differs; i++ {
		fn.Params = append(fn.Params, l.newLocal("unused", ft.Params[i]))
	}
	l.function = outer
	l.inFunction(fn, func() { fn.Body = l.block(node.Body(), node.Body().Statements()) })
	if generic != nil {
		generic.name, generic.jsName, generic.closure = fn.Name, fn.JSName, fn
		if field.Method {
			generic.field = field // its calls are a method's, by name
		}
		if l.addErasedValue(fn, generic) {
			generic.scope = l.scopeOf(node)
		}
	}
	var closure ir.Expr = &ir.MakeClosure{Func: fn, FuncType: ft}
	if differs { // `deposit(amount, memo)` for `deposit(amount: number, memo?: string)`: adapted to the field's type
		closure = l.adapt(&ir.MakeClosure{Func: fn, FuncType: l.typeOfFunc(fn)}, ft, node)
	}
	if closure == nil || field.Type == ft {
		return closure
	}
	return l.convert(closure, field.Type, node) // `error(e) { … }` for an `error?: (e: unknown) => R`
}

// fieldFuncType is the function type of the field an object literal's method fills: the
// field's type, or the function member of an optional function-typed property's union.
func fieldFuncType(field *ir.Field) *ir.FuncType {
	switch t := field.Type.(type) {
	case *ir.FuncType:
		return t
	case *ir.Union:
		for _, member := range t.Members {
			if ft, ok := member.(*ir.FuncType); ok {
				return ft
			}
		}
	}
	return nil
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
	if classMethod, isMethod := l.membersAt(access).methods[member]; isMethod {
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
	if m, ok := l.objectTypeMethod(receiver.Type(), name); ok {
		return m, true
	}
	l.notSupportedYet(access, fmt.Sprintf("calling a method of an object type on a %s is", receiver.Type()))
	return method{}, true
}

// objectTypeMethod is the method name of values of t, a shape (a literal's method field)
// or an interface, if it has one.
func (l *lowerer) objectTypeMethod(t ir.Type, name string) (method, bool) {
	switch t := t.(type) {
	case *ir.Class:
		if field := t.Field(name); t.Shape && field != nil && field.Method && field.Kind == ir.Plain {
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
	return method{}, false
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
	return l.boundMethod(node, m, receiver, nil, m.name, false, l.objectMethodForward(node, m))
}

// boundMethod makes a closure that calls m on receiver with the arguments bound (values
// for its first parameters) and then its own: a method value, or what `bind` makes. A
// closure that binds a function value (binds) shows as "bound " and that function's name.
// forward, if not nil, is what the closure forwards its calls to, given the local it keeps
// receiver in: its closures list instances that make those calls for typed calls (D-194).
func (l *lowerer) boundMethod(node *ast.Node, m method, receiver ir.Expr, bound []ir.Expr, name string, binds bool, forward func(receiver *ir.Local) *forwarder) ir.Expr {
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
	for _, param := range params { // a method value's length: its parameters before the first that may be left out
		if isRest(param) {
			break
		}
		if param == ir.Given {
			fn.Length-- // the one before it
			break
		}
		fn.Length++
	}
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
	if forward != nil {
		if f := forward(captured[0]); f != nil {
			f.source = l.wrapperSource(node)
			l.addForwarder(fn, f)
		}
	}
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

// functionMethods are the methods of functions that give them a `this`.
var functionMethods = map[string]bool{"call": true, "apply": true, "bind": true}

// dynamicMethodWithThis lowers `o.m.call(self, …)`, `o.m.apply(self, …)` or `o.m.bind(self,
// …)` (call, of callee) for target `o.m`, a method of o held as a dynamic value (a Box<any>
// place's, an any's): the method o's class has, bound to self where self's class has the same
// one (DynamicMethodFor), so it runs on self as JS's does, then call, apply or bind through any
// (R2-F6). ok is false for other calls.
func (l *lowerer) dynamicMethodWithThis(call *ast.CallExpression, callee, target *ast.Node) (ir.Expr, bool) {
	args := call.Arguments.Nodes
	if target.Kind != ast.KindPropertyAccessExpression || target.AsPropertyAccessExpression().Name().Kind == ast.KindPrivateIdentifier || len(args) == 0 ||
		slices.ContainsFunc(args, isSpreadElement) || !l.isAny(target.Expression()) && !l.isNarrowedDynamic(target.Expression()) {
		return nil, false
	}
	object, self := l.expression(target.Expression()), l.valueExpression(args[0])
	key := l.dynamicKey(target)
	if object == nil || self == nil || key == nil {
		return nil, true
	}
	l.wantDynamicMember(target)
	objectLocal, selfLocal := l.newLocal("object", ir.Value), l.newLocal("self", ir.Value)
	method := &ir.CallBuiltin{Builtin: ir.DynamicMethodFor, Args: []ir.Expr{&ir.LocalRef{Local: objectLocal}, key, &ir.LocalRef{Local: selfLocal}}}
	values := []ir.Expr{method, box(&ir.StringConst{Value: callee.AsPropertyAccessExpression().Name().Text()}), &ir.StringConst{Value: scanner.GetTextOfNode(callee)}, &ir.LocalRef{Local: selfLocal}}
	for _, argument := range args[1:] {
		value := l.valueExpression(argument)
		if value == nil {
			return nil, true
		}
		values = append(values, box(value))
	}
	if l.firstDynamicCall == nil {
		l.firstDynamicCall = call.AsNode()
	}
	result := l.narrowedValue(call.AsNode(), &ir.CallBuiltin{Builtin: ir.DynamicCallMethod, Args: values})
	return &ir.Bind{Local: objectLocal, Value: box(object), In: &ir.Bind{Local: selfLocal, Value: box(self), In: result}}, true
}

// functionMethodCall lowers `f.call(thisArg, args…)`, `f.apply(thisArg, [args…])` and
// `f.bind(thisArg, args…)` (D-044, D-045). For a method `o.m`, thisArg is the object it
// runs on; a function ignores it (it is still evaluated). `apply` takes its arguments as
// an array literal or a tuple (`appliedTuple`).
func (l *lowerer) functionMethodCall(call *ast.CallExpression) ir.Expr {
	callee := ast.SkipParentheses(call.Expression)
	if target := ast.SkipParentheses(callee.Expression()); l.isInstanceofDynamicLocalRef(target) {
		// `h.call(null, x)` for an h holding an instanceof narrowing's function as it is (R8-F1):
		// found at run time, as through any
		fn := l.expression(target)
		if fn == nil {
			return nil
		}
		if x := l.dynamicMethodCall(call, callee, fn); x != nil {
			return l.narrowedValue(call.AsNode(), x)
		}
		return nil
	}
	name := callee.AsPropertyAccessExpression().Name().Text()
	args := call.Arguments.Nodes
	if (name != "call" || len(args) > 0 && isSpreadElement(args[0])) && l.hasSpreadArgument(call) {
		return nil // `call` spreads its arguments as other calls do
	}
	var tuple *ast.Node // the tuple `apply` passes the elements of
	if name == "apply" {
		switch literal := appliedArguments(args); {
		case literal != nil:
			args = literal
		case len(args) == 2 && l.tupleShapeOf(args[1]) != nil:
			args, tuple = args[:1], args[1]
		default:
			l.notSupportedYet(call.Arguments.Nodes[1], "`apply` with arguments that are neither an array literal nor a tuple is")
			return nil
		}
		name = "call"
	}
	target := ast.SkipParentheses(callee.Expression())
	if target.Kind == ast.KindPropertyAccessExpression && (l.isInstanceMethod(target) || l.isGenericInstanceMethod(target) && l.runsOnThisArg(args, target)) {
		return l.methodCallOrBind(call, args, tuple, target, name)
	}
	if x, ok := l.dynamicMethodWithThis(call, callee, target); ok {
		return x
	}
	fn := l.expression(target)
	if fn == nil {
		return nil
	}
	if fn.Type() == ir.Value && tuple == nil {
		// `b.m.bind(b)` for a b held as a dynamic value (a Box<any> place's object, H10b): the
		// function it holds, its call, apply or bind found at run time, as through any
		if x := l.dynamicMethodCall(call, callee, fn); x != nil {
			return l.narrowedValue(call.AsNode(), x)
		}
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
	if name == "bind" && bindsRest(ft.Params, len(args)) {
		l.notSupportedYet(call.Arguments.Nodes[len(call.Arguments.Nodes)-1], "binding arguments of a rest parameter is")
		return nil
	}
	values, applied := l.callArguments(args, tuple, passedParams(ft.Params, name, len(args)))
	if values == nil {
		return nil
	}
	if name == "call" {
		return l.withReceiver(fn, func(f ir.Expr) ir.Expr { // JS evaluates f, then thisArg, then the arguments
			return discard(thisArg, applied(&ir.CallValue{Callee: f, Args: values}))
		})
	}
	m := method{name: "", receiver: ft, params: ft.Params, result: ft.Result, call: func(f ir.Expr, args []ir.Expr) ir.Expr {
		return &ir.CallValue{Callee: f, Args: args}
	}}
	var forward func(*ir.Local) *forwarder
	if len(values) == 0 && l.genericSignature(target) != nil {
		// `make.bind(undefined)`, a value of the generic function type make has: its calls
		// are make's, for the same signatures (D-194).
		forward = func(wrapped *ir.Local) *forwarder { return &forwarder{call: l.valueForward(wrapped, ft, target)} }
	}
	return l.withReceiver(fn, func(f ir.Expr) ir.Expr {
		return discard(thisArg, l.boundMethod(callee, m, f, values, "bound", true, forward))
	})
}

// callArguments lowers the arguments a `call` passes after thisArg, converted to params:
// rest, or the elements of tuple (`apply` with a tuple; rest is empty then), which applied
// evaluates, into a local, around the call that uses them.
func (l *lowerer) callArguments(rest []*ast.Node, tuple *ast.Node, params []ir.Type) (values []ir.Expr, applied func(ir.Expr) ir.Expr) {
	if tuple == nil {
		return l.typedArguments(rest, params), func(call ir.Expr) ir.Expr { return call }
	}
	value := l.expression(tuple)
	if value == nil {
		return nil, nil
	}
	if value.Type() == ir.Value { // a dynamic value tsc narrowed to the tuple, whose elements apply only reads (D-179)
		t := l.irType(l.typeAt(tuple), tuple)
		if t == nil {
			return nil, nil
		}
		if value = l.dynamicConversion(value, t, tuple); value == nil {
			return nil, nil
		}
	}
	shape, ok := value.Type().(*ir.Class)
	if !ok {
		l.notSupportedYet(tuple, fmt.Sprintf("`apply` with arguments of type %s is", value.Type()))
		return nil, nil
	}
	local := l.newLocal("applied", shape)
	elements, at := make([]ir.Expr, len(shape.Fields)), make([]*ast.Node, len(shape.Fields))
	for i, field := range shape.Fields {
		elements[i], at[i] = &ir.FieldRef{X: &ir.LocalRef{Local: local}, Field: field}, tuple
	}
	return l.convertArguments(elements, at, params), func(call ir.Expr) ir.Expr {
		return &ir.Bind{Local: local, Value: value, In: call}
	}
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

// runsOnThisArg reports whether `call`, `apply` or `bind` of the class's generic method
// access names, given args, runs it on thisArg (methodCallOrBind), as for other class
// methods (D-044): where the method may use its object, whose thisArg then converts to the
// class (with a check from `any`, else PTS1001: JS would run it on another object). A
// method that can't use its object runs on its own, as before D-194, which no program can
// tell apart; so does an abstract or overridden one given no object.
func (l *lowerer) runsOnThisArg(args []*ast.Node, access *ast.Node) bool {
	generic := l.genericMethodAt(access)
	if (generic.dispatched || generic.node.Body() == nil) && (len(args) == 0 || isNoObject(l.typeAt(args[0]))) {
		// No object: JS runs o's method without one, which only a method that doesn't use
		// `this` survives; that is o's own, found by name (review round 3's R3-2).
		return false
	}
	return mayUseThis(generic)
}

// isNoObject reports whether t, a thisArg's type, is null, undefined or void.
func isNoObject(t *checker.Type) bool {
	return t.Flags()&(checker.TypeFlagsNull|checker.TypeFlagsUndefined|checker.TypeFlagsVoid) != 0
}

// mayUseThis reports whether a class's generic method may use its object: `this` or `super`
// in its parameters or body (usesObject), or it is abstract or overridden, whose calls run the
// method of thisArg's class (D-044).
func mayUseThis(generic *genericFunc) bool {
	return generic.dispatched || generic.node.Body() == nil || usesObject(generic.node)
}

// usesObject reports whether a method's declaration uses its object: `this` or `super` in its
// parameters (their defaults) or body, arrow functions included, but not in the functions,
// classes and object literals' methods and accessors it declares, which have their own.
func usesObject(method *ast.Node) bool {
	found := false
	var visit func(node *ast.Node) bool
	visit = func(node *ast.Node) bool {
		switch node.Kind {
		case ast.KindThisKeyword, ast.KindSuperKeyword:
			found = true
		case ast.KindFunctionDeclaration, ast.KindFunctionExpression, ast.KindClassDeclaration, ast.KindClassExpression:
		case ast.KindMethodDeclaration, ast.KindGetAccessor, ast.KindSetAccessor:
			if name := node.Name(); name != nil && name.Kind == ast.KindComputedPropertyName {
				visit(name) // evaluated where the literal is
			}
		default:
			node.ForEachChild(visit)
		}
		return found
	}
	for _, param := range method.Parameters() {
		visit(param)
	}
	if body := method.Body(); body != nil {
		visit(body)
	}
	return found
}

// isGenericInstanceMethod reports whether a property access names a class's generic
// instance method, which `call`, `apply` and `bind` run on their thisArg too (D-194).
func (l *lowerer) isGenericInstanceMethod(access *ast.Node) bool {
	generic := l.genericMethodAt(access)
	return generic != nil && generic.static == nil
}

// isInstanceMethod reports whether a property access names a method that runs on its
// object: a class's instance method or a method of an object type.
func (l *lowerer) isInstanceMethod(access *ast.Node) bool {
	member := l.memberDeclaration(access)
	if classMethod, ok := l.membersAt(access).methods[member]; ok {
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

// passedParams are the parameters (of types params) that `call` passes all of, or that
// `bind` with n arguments binds: the first n, each with its Given flag if it has one.
func passedParams(params []ir.Type, name string, n int) []ir.Type {
	if name == "call" {
		return params
	}
	end := 0
	for ; end < len(params) && n > 0 && !isRest(params[end]); n-- {
		end++
		if hasDefault(params, end-1) {
			end++
		}
	}
	return params[:end]
}

// bindsRest reports whether `bind` with n arguments binds some of them to a rest
// parameter, the last of params.
func bindsRest(params []ir.Type, n int) bool {
	return len(params) > 0 && isRest(params[len(params)-1]) && n >= arity(params)
}

// methodCallOrBind lowers `o.m.call(thisArg, args…)`, which calls m on thisArg, and
// `o.m.bind(thisArg, args…)`, which makes a closure bound to thisArg. thisArg must be of
// o's type: for a class's method, the method thisArg's class has runs; for a method of an
// object type, thisArg must be o itself. o is evaluated for its effects, as JS reads o.m.
func (l *lowerer) methodCallOrBind(call *ast.CallExpression, args []*ast.Node, tuple *ast.Node, target *ast.Node, name string) ir.Expr {
	object := l.expression(target.Expression())
	if object == nil {
		return nil
	}
	var m method
	var ok bool
	var forward func(*ir.Local) *forwarder
	var source *ast.Node
	if len(args) > 0 {
		source = args[0] // bind captures thisArg, not the method lookup's object (D-207)
	}
	generic := l.isGenericInstanceMethod(target)
	if generic {
		m, forward, ok = l.genericMethodOf(target, l.genericMethodAt(target), source) // run on thisArg too, as JS does (D-194)
	} else {
		m, ok = l.methodOf(target, object)
		forward = l.objectMethodForwardFrom(target, l.typeAt(target.Expression()), m, source)
	}
	if !ok || m.call == nil {
		return nil // reported
	}
	if len(args) == 0 {
		l.notSupportedYet(call.Expression, "calling a method without an object (`this` undefined) is")
		return nil
	}
	if _, isClass := l.membersAt(target).methods[l.memberDeclaration(target)]; !isClass && !generic && !sameReference(target.Expression(), args[0]) {
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
	if name == "bind" && bindsRest(m.params, len(rest)) {
		l.notSupportedYet(rest[len(rest)-1], "binding arguments of a rest parameter is")
		return nil
	}
	values, applied := l.callArguments(rest, tuple, passedParams(m.params, name, len(rest)))
	if values == nil {
		return nil
	}
	var result ir.Expr
	switch {
	case name == "call" && tuple != nil:
		result = l.withReceiver(receiver, func(r ir.Expr) ir.Expr { return applied(m.call(r, values)) }) // thisArg before the tuple
	case name == "call":
		result = m.call(receiver, values)
	default:
		if len(values) > 0 {
			forward = nil // `bind` with arguments: tsc's type of its value isn't generic
		}
		result = l.boundMethod(call.Expression, m, receiver, values, "bound "+m.name, false, forward)
	}
	return discard(object, result)
}
