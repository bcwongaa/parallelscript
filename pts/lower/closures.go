package lower

import (
	"fmt"
	"slices"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/pts/ir"
)

// ---------------------------------------------------------------- function types

// functionType maps a checker type with one call signature and nothing else to its
// function type, or reports why it can't (at node) and returns nil. ok is false if t
// isn't such a type.
func (l *lowerer) functionType(t *checker.Type, node *ast.Node) (ft *ir.FuncType, ok bool) {
	if t.Flags()&checker.TypeFlagsObject == 0 || t.ObjectFlags()&checker.ObjectFlagsClass != 0 {
		return nil, false
	}
	signatures := l.checker.GetSignaturesOfType(t, checker.SignatureKindCall)
	if len(signatures) == 0 || len(l.checker.GetSignaturesOfType(t, checker.SignatureKindConstruct)) > 0 ||
		len(l.checker.GetPropertiesOfType(t)) > 0 || len(l.checker.GetIndexInfosOfType(t)) > 0 {
		return nil, false
	}
	if len(signatures) > 1 {
		l.notSupportedYet(node, fmt.Sprintf("overloaded function types (%s) are", l.checker.TypeToString(t)))
		return nil, true
	}
	if l.funcTypesInProgress[t] {
		l.notSupportedYet(node, fmt.Sprintf("recursive function types (%s) are", l.checker.TypeToString(t)))
		return nil, true
	}
	l.funcTypesInProgress[t] = true
	defer delete(l.funcTypesInProgress, t)
	return l.signatureType(signatures[0], node), true
}

// signatureType is the function type of a call signature, or nil (reported at node).
func (l *lowerer) signatureType(signature *checker.Signature, node *ast.Node) *ir.FuncType {
	parameters := signature.Parameters()
	restIsTuple := signature.HasRestParameter() && checker.IsTupleType(l.typeOfSymbol(parameters[len(parameters)-1]))
	if restIsTuple {
		parameters = l.checker.GetExpandedParameters(signature, true)[0] // a rest parameter of a tuple type, as `bind` leaves, is its elements
	}
	if typeParameters := signature.TypeParameters(); len(typeParameters) > 0 {
		outer := l.subst
		l.subst = l.erasing(typeParameters, nil) // a generic function type's values are erased copies (D-078)
		defer func() { l.subst = outer }()
	}
	switch {
	case restIsTuple && len(parameters) > 0 && parameters[len(parameters)-1].CheckFlags&ast.CheckFlagsRestParameter != 0:
		l.notSupportedYet(node, "rest parameters of tuples with rest elements are")
		return nil
	}
	params := make([]ir.Type, 0, len(parameters))
	for i, param := range parameters {
		t := l.typeOfSymbol(param)
		rest := signature.HasRestParameter() && !restIsTuple && i == len(parameters)-1
		if rest {
			t = l.instantiateMade(l.checker.GetTypeOfSymbol(param)) // in erased code, any[] for a T[] (D-078)
		}
		optional := !rest && (!restIsTuple && i >= signature.MinArgumentCount() || isOptionalParameter(param))
		if optional {
			t = l.checker.RemoveMissingOrUndefinedType(t)
		}
		paramType := l.irType(t, node)
		switch {
		case paramType == nil:
			return nil
		case rest:
			array, ok := paramType.(*ir.Array)
			if !ok {
				l.notSupportedYet(node, fmt.Sprintf("rest parameters of type %s are", paramType))
				return nil
			}
			paramType = l.restOf(array) // D-057
		}
		params = append(params, paramType)
		if optional {
			params = append(params, ir.Given) // D-055
		}
	}
	result := l.returnTypeOf(signature)
	if result.Flags()&checker.TypeFlagsVoid != 0 {
		return l.funcType(params, ir.Void)
	}
	resultType := l.irType(result, node)
	if resultType == nil {
		return nil
	}
	return l.funcType(params, resultType)
}

// isOptionalParameter reports whether a call may leave out a parameter of a signature,
// or pass undefined for it: one with a default or a question mark, or an optional
// element of a tuple rest parameter.
func isOptionalParameter(param *ast.Symbol) bool {
	if param.CheckFlags&ast.CheckFlagsOptionalParameter != 0 {
		return true
	}
	declaration := param.ValueDeclaration
	return declaration != nil && declaration.Kind == ast.KindParameter &&
		(declaration.Initializer() != nil || declaration.AsParameterDeclaration().QuestionToken != nil)
}

// funcType is the one function type that takes params and returns result.
func (l *lowerer) funcType(params []ir.Type, result ir.Type) *ir.FuncType {
	for _, ft := range l.funcTypes {
		if ft.Result == result && slices.Equal(ft.Params, params) {
			return ft
		}
	}
	ft := &ir.FuncType{Params: params, Result: result}
	l.funcTypes = append(l.funcTypes, ft)
	return ft
}

// typeOfFunc is the function type of a function's values: its parameters (after `this`
// for a method) and its result.
func (l *lowerer) typeOfFunc(fn *ir.Func) *ir.FuncType {
	params := make([]ir.Type, len(fn.Params))
	for i, param := range fn.Params {
		params[i] = param.Type
	}
	return l.funcType(params, fn.Result)
}

func isFunction(t ir.Type) bool {
	_, ok := t.(*ir.FuncType)
	return ok
}

// ---------------------------------------------------------------- function values

// functionExpression lowers an arrow function or function expression to a new closure
// (D-041). Its body is lowered at once, as a function of its own that the current one
// makes closures of. If its context is a function type that takes more parameters or
// returns nothing, the function takes those parameters too, or discards what it returns,
// so that it is a value of that type without an adapter.
func (l *lowerer) functionExpression(node *ast.Node) ir.Expr {
	flags := ast.GetFunctionFlags(node)
	switch {
	case flags&ast.FunctionFlagsAsync != 0:
		l.notSupportedYet(node, "async functions are")
		return nil
	}
	if typeParameters := l.checker.GetSignatureFromDeclaration(node).TypeParameters(); len(typeParameters) > 0 {
		outer := l.subst
		l.subst = l.erasing(typeParameters, nil) // its own, or its context's (D-078)
		defer func() { l.subst = outer }()
	}
	name := l.jsName(node)
	fn := &ir.Func{Name: l.closureName(name, node), Module: l.function.Module, Pos: l.pos(node), Closure: true, Outer: l.function, JSName: name, Length: jsLength(node),
		Generator: flags&ast.FunctionFlagsGenerator != 0}
	l.funcNodes[fn] = node
	if !l.closureSignature(fn, node) {
		return nil
	}
	l.arrows[fn] = node.Kind == ast.KindArrowFunction
	if name := node.Name(); name != nil { // a named function expression's own name, bound in its body
		fn.Self = l.newLocal(name.Text(), l.typeOfFunc(fn))
		l.locals[l.declaredSymbol(node)] = fn.Self
	}
	l.inFunction(fn, func() { fn.Body = l.functionExpressionBody(node) })
	ft := l.typeOfFunc(fn)
	return &ir.MakeClosure{Func: fn, FuncType: ft}
}

// closureSignature gives fn, the function of an arrow function or function expression
// (node), its parameters and result type, widened to its context's function type where
// that takes more parameters or returns nothing. It reports what it can't lower.
func (l *lowerer) closureSignature(fn *ir.Func, node *ast.Node) bool {
	outer := l.function
	l.function = fn // parameters are locals of fn
	defer func() { l.function = outer }()
	for _, param := range node.Parameters() {
		if param.Name().Kind == ast.KindIdentifier && param.Name().Text() == "this" {
			l.notSupportedYet(param, "`this` parameters are")
			return false
		}
		locals := l.parameter(param)
		if locals == nil {
			return false
		}
		fn.Params = append(fn.Params, locals...)
	}
	if fn.Result = l.resultType(node); fn.Result == nil {
		return false
	}
	context := l.contextualFuncType(node)
	if context == nil {
		return true
	}
	if flags := alwaysPassed(fn, context); len(flags) > 0 {
		if node.Name() != nil {
			return true // a named function expression may call itself leaving the argument out
		}
		l.bindAlwaysPassed(fn, flags)
	}
	for i := len(fn.Params); i < len(context.Params); i++ {
		fn.Params = append(fn.Params, l.newLocal("unused", context.Params[i]))
	}
	_, contextInterface := context.Result.(*ir.Interface)
	switch {
	case fn.Generator: // its calls make a generator object, whatever the context does with it
	case context.Result == ir.Void:
		fn.Result = ir.Void
	case context.Result == ir.Number && ir.IsNumeric(fn.Result) && node.Type() == nil:
		fn.Result = ir.Number // an inferred integer result widens to the context's number, losslessly
	case contextInterface && node.Type() == nil:
		fn.Result = context.Result // `(b) => (b ? new Square() : new Circle())` for a `(b: boolean) => Shape`
	}
	return true
}

// contextualFuncType is the function type node's context expects, if it is one that
// node's function can take the shape of: the same parameter types, then perhaps more,
// where a parameter with a default may be one the context always passes, but not the
// other way round. It reports nothing: a context that doesn't map just doesn't shape the
// function.
func (l *lowerer) contextualFuncType(node *ast.Node) *ir.FuncType {
	contextual := l.contextualType(node)
	if contextual == nil {
		return nil
	}
	reported := l.errors
	ft, ok := l.functionType(contextual, node)
	l.errors = reported
	if !ok || ft == nil || ft.Result == ir.Value && l.function.Result != ir.Value {
		return nil // filter's predicate returns unknown: it gives nothing a shape to take
	}
	params := l.function.Params
	j := 0 // in ft.Params
	for i, param := range params {
		if param.Type == ir.Given {
			continue // with its parameter
		}
		if j >= len(ft.Params) || ft.Params[j] != param.Type {
			return nil
		}
		passedOrNot := hasDefault(ft.Params, j)
		if passedOrNot && (i+1 == len(params) || params[i+1].Type != ir.Given) {
			return nil // the context may leave out an argument the function needs
		}
		j++
		if passedOrNot {
			j++
		}
	}
	return ft
}

// alwaysPassed are the Given flags of fn's parameters with defaults that the function
// type ft, which fn takes the shape of, always passes.
func alwaysPassed(fn *ir.Func, ft *ir.FuncType) []*ir.Local {
	var flags []*ir.Local
	j := 0 // in ft.Params
	for _, param := range fn.Params {
		if param.Type == ir.Given && (j >= len(ft.Params) || ft.Params[j] != ir.Given) {
			flags = append(flags, param)
			continue
		}
		j++
	}
	return flags
}

// bindAlwaysPassed drops flags, Given flags of fn's parameters whose arguments every call
// passes: their defaults never apply, so each such parameter is its argument.
func (l *lowerer) bindAlwaysPassed(fn *ir.Func, flags []*ir.Local) {
	for _, binding := range l.bindings[fn] {
		if binding.given == nil || !slices.Contains(flags, binding.given) {
			continue
		}
		i := slices.Index(fn.Params, binding.arg)
		fn.Params = slices.Delete(fn.Params, i+1, i+2) // the flag
		fn.Params[i] = binding.value
		binding.arg, binding.given = nil, nil
	}
}

// functionExpressionBody lowers the body of an arrow function or function expression into
// the function being lowered. An expression body is the value the function returns.
func (l *lowerer) functionExpressionBody(node *ast.Node) *ir.Block {
	body := node.Body()
	if body.Kind == ast.KindBlock {
		return l.block(body, body.Statements())
	}
	at := l.pos(body)
	value := l.expression(body)
	if value == nil {
		return &ir.Block{At: at}
	}
	if l.function.Result == ir.Void {
		if isPure(value) {
			return &ir.Block{At: at}
		}
		return &ir.Block{At: at, Stmts: []ir.Stmt{&ir.ExprStmt{At: at, X: value}}}
	}
	if value = l.convert(value, l.function.Result, body); value == nil {
		return &ir.Block{At: at}
	}
	return &ir.Block{At: at, Stmts: []ir.Stmt{&ir.Return{At: at, Value: value}}}
}

// closureName is the IR name of a closure's function: the name it has in JS, or "arrow"
// or "function" if it has none, qualified with the function that makes it unless that is
// a module's top-level code.
func (l *lowerer) closureName(jsName string, node *ast.Node) string {
	name := jsName
	if name == "" {
		name = "function"
		if node.Kind == ast.KindArrowFunction {
			name = "arrow"
		}
	}
	if l.function == l.function.Module.Init {
		return name
	}
	return l.function.Name + "." + name
}

// jsName is the `name` JS gives a function expression (node): its own, or the one
// NamedEvaluation gives an anonymous function from where it is written: the variable,
// property or field it initialises, the variable it is assigned to, or "default" for an
// `export default`. Parentheses and type assertions around it don't matter.
func (l *lowerer) jsName(node *ast.Node) string {
	if name := node.Name(); name != nil {
		return name.Text()
	}
	child, parent := node, node.Parent
	for parent.Kind == ast.KindParenthesizedExpression || parent.Kind == ast.KindAsExpression || parent.Kind == ast.KindSatisfiesExpression ||
		parent.Kind == ast.KindTypeAssertionExpression || parent.Kind == ast.KindNonNullExpression {
		child, parent = parent, parent.Parent
	}
	switch parent.Kind {
	case ast.KindVariableDeclaration, ast.KindPropertyAssignment, ast.KindPropertyDeclaration, ast.KindParameter:
		if parent.Initializer() == child && parent.Name() != nil {
			return constantPropertyName(parent.Name())
		}
	case ast.KindBinaryExpression:
		expr := parent.AsBinaryExpression()
		switch expr.OperatorToken.Kind {
		case ast.KindEqualsToken, ast.KindAmpersandAmpersandEqualsToken, ast.KindBarBarEqualsToken, ast.KindQuestionQuestionEqualsToken:
			if expr.Right == child && expr.Left.Kind == ast.KindIdentifier {
				return expr.Left.Text()
			}
		}
	case ast.KindExportAssignment:
		return "default"
	}
	return ""
}

// constantPropertyName is the text of a name that is known before the program runs: an
// identifier, a string or numeric literal (tsc keeps a number's as JS's string of it), or
// a computed name of a literal; "" for any other.
func constantPropertyName(name *ast.Node) string {
	if name.Kind == ast.KindIdentifier {
		return name.Text()
	}
	if isIteratorName(name) {
		return jsIteratorName
	}
	if name.Kind == ast.KindComputedPropertyName {
		name = ast.SkipParentheses(name.Expression())
	}
	switch name.Kind {
	case ast.KindStringLiteral, ast.KindNumericLiteral, ast.KindNoSubstitutionTemplateLiteral:
		return name.Text()
	}
	return ""
}

// functionValue is the value of a function declaration (node names it): the closure of a
// nested one, held by the local its block made it in, or a top-level one's static closure.
func (l *lowerer) functionValue(node *ast.Node, fn *ir.Func) ir.Expr {
	ft := l.typeOfFunc(fn)
	if fn.Closure {
		return l.closureOf(node, fn)
	}
	return &ir.MakeClosure{Func: fn, FuncType: ft}
}

// closureOf is the closure of a nested function declaration that a call or a reference
// (node) in the function being lowered uses.
func (l *lowerer) closureOf(node *ast.Node, fn *ir.Func) ir.Expr {
	if fn.Self == nil {
		panic("lower: a nested function is used before its block made its closure")
	}
	if local := l.ownLocal(node, fn.Self); local != nil {
		return &ir.LocalRef{Local: local}
	}
	return nil
}

// hoistedFunctions makes the closures of the function declarations among a block's
// statements that are nested in a function or block, as JS does when it enters the block,
// each in a local of the function being lowered (the function's Self).
func (l *lowerer) hoistedFunctions(statements []*ast.Node) []ir.Stmt {
	var stmts []ir.Stmt
	for _, statement := range statements {
		if statement.Kind != ast.KindFunctionDeclaration || statement.Body() == nil {
			continue
		}
		fn := l.funcs[l.declaredSymbol(statement)]
		if fn == nil || !fn.Closure {
			continue
		}
		fn.Outer = l.function
		ft := l.typeOfFunc(fn)
		fn.Self = l.newLocal(fn.JSName, ft)
		stmts = append(stmts, &ir.LocalDecl{At: l.pos(statement), Local: fn.Self, Init: &ir.MakeClosure{Func: fn, FuncType: ft}})
	}
	return stmts
}

// isNestedFunction reports whether a function declaration is inside a function or a
// block, where it makes a closure each time its block is entered, rather than at a
// module's top level.
func isNestedFunction(declaration *ast.Node) bool {
	return declaration.Parent.Kind != ast.KindSourceFile
}

// valueCall lowers a call of a function value: a variable, field, element or result of a function type.
func (l *lowerer) valueCall(call *ast.CallExpression) ir.Expr {
	callee := l.expression(call.Expression)
	if callee == nil {
		return nil
	}
	if isFunctionUnion(callee.Type()) { // `(() => number) | (() => string)`: through the members' dynamic entries (D-096)
		if l.firstDynamicCall == nil {
			l.firstDynamicCall = call.AsNode()
		}
		result := l.dynamicCallWith(call, call.Expression, ir.DynamicCall, []ir.Expr{box(callee)})
		if result == nil {
			return nil
		}
		return l.fromErased(result, call.AsNode())
	}
	ft, ok := callee.Type().(*ir.FuncType)
	if !ok {
		l.notSupportedYet(call.Expression, fmt.Sprintf("calling a %s is", callee.Type()))
		return nil
	}
	args := l.typedArguments(call.Arguments.Nodes, ft.Params)
	if args == nil {
		return nil
	}
	result := ir.Expr(&ir.CallValue{Callee: callee, Args: args})
	if ft.Result == ir.Value {
		return l.fromErased(result, call.AsNode()) // a generic function type's result (D-078)
	}
	return result
}

// isFunctionUnion reports whether t is a union of function types (perhaps with others,
// such as undefined, which tsc has ruled out where it is called).
func isFunctionUnion(t ir.Type) bool {
	u, ok := t.(*ir.Union)
	return ok && slices.ContainsFunc(u.Members, isFunction)
}

// fromErased converts value, the dynamic result of a call of an erased copy (node), to
// the type tsc gives the call, with a check (D-078), or to the union of object types its
// result goes to (objectUnionContext).
func (l *lowerer) fromErased(value ir.Expr, node *ast.Node) ir.Expr {
	t := l.typeAt(node)
	if contextual := l.objectUnionContext(node); contextual != nil && l.checker.IsTypeAssignableTo(t, contextual) {
		t = contextual
	}
	target := l.irType(t, node)
	switch {
	case target == nil:
		return nil
	case target == ir.Value:
		return value
	}
	return l.dynamicConversion(value, target, node)
}

// isValueCall reports whether a call's callee is a function value of the program rather
// than a library function.
func (l *lowerer) isValueCall(callee *ast.Node) bool {
	if symbol := l.target(callee); symbol != nil && (l.isDeclaredInDefaultLib(symbol) || l.isDeclaredInPtsLib(symbol)) {
		return false
	}
	return len(l.checker.GetSignaturesOfType(l.typeAt(callee), checker.SignatureKindCall)) > 0
}

// ---------------------------------------------------------------- captures and `this`

// capture makes the functions between the one being lowered and the owner of local, a
// local of an enclosing function, capture it: each closure on the way holds it, so that
// it can make the next. It returns false if local isn't a local of an enclosing function.
func (l *lowerer) capture(local *ir.Local) bool {
	owner := l.owners[local]
	for fn := l.function; fn != owner; fn = fn.Outer {
		switch {
		case fn.Self == local:
			return true // the function's own closure
		case !fn.Closure || fn.Outer == nil:
			return false
		case !slices.Contains(fn.Captures, local):
			fn.Captures = append(fn.Captures, local)
		}
	}
	return true
}

// declareAhead makes the local of a variable that a closure uses above its declaration,
// which JS allows as long as the closure runs after the declaration: a local of the
// enclosing function that declares it. It does nothing for anything else.
func (l *lowerer) declareAhead(symbol *ast.Symbol) {
	if symbol == nil || symbol.ValueDeclaration == nil || symbol.ValueDeclaration.Kind != ast.KindVariableDeclaration {
		return
	}
	declaration := symbol.ValueDeclaration
	container := ast.GetContainingFunction(declaration)
	if container == nil {
		container = ast.GetSourceFileOfNode(declaration).AsNode()
	}
	for fn := l.function; fn != nil; fn = fn.Outer {
		if l.funcNodes[fn] == container {
			t := l.bindingType(symbol, declaration.Name())
			if t == nil {
				l.locals[symbol] = nil // reported
				return
			}
			local := &ir.Local{Name: declaration.Name().Text(), Type: t}
			l.owners[local] = fn
			l.locals[symbol] = local
			return
		}
	}
}

// thisFunction is the function whose `this` is `this` in the function being lowered:
// the function itself, or for an arrow function, the function it is written in.
func (l *lowerer) thisFunction() *ir.Func {
	fn := l.function
	for l.arrows[fn] {
		fn = fn.Outer
	}
	return fn
}

// thisLocal is the `this` parameter of the constructor or method that `this` in the
// function being lowered is, captured if that function is an arrow function in it; nil
// outside constructors and methods.
func (l *lowerer) thisLocal() *ir.Local {
	this := l.this[l.thisFunction()]
	if this != nil {
		l.capture(this)
	}
	return this
}

// ---------------------------------------------------------------- adapters

// adapt converts a function value to another function type that takes the same
// parameters and perhaps more, or ones that convert to its own, and returns nothing or
// what its result converts to (D-041). The value becomes an adapter: a closure that calls
// it with the parameters it takes and converts its result. A parameter with a default
// that the other type doesn't pass is left out, and one it always passes is passed
// (D-055). It reports (at node) and returns nil if the types don't fit.
func (l *lowerer) adapt(value ir.Expr, to *ir.FuncType, node *ast.Node) ir.Expr {
	return l.adaptSkipping(value, to, 0, node)
}

// functionAsMethod is a function value given for a method field of a shape (`{ combine: (a, b)
// => a + b }` for a `combine(a, b)` method), whose function type takes `this` first
// (D-044): an adapter that leaves `this` out, as JS calls an arrow without it.
func (l *lowerer) functionAsMethod(value ir.Expr, field *ir.Field, node *ast.Node) ir.Expr {
	if !isFunction(value.Type()) {
		l.notSupportedYet(node, fmt.Sprintf("a %s as the method %s is", value.Type(), field.Name))
		return nil
	}
	return l.adaptSkipping(value, field.Type.(*ir.FuncType), 1, node)
}

// adaptSkipping is adapt for an adapter whose first skip parameters the wrapped
// function doesn't take.
func (l *lowerer) adaptSkipping(value ir.Expr, to *ir.FuncType, skip int, node *ast.Node) ir.Expr {
	from := value.Type().(*ir.FuncType)
	wrapped := l.newLocal("adapted", from)
	fn := &ir.Func{Name: l.closureName("adapter", node), Module: l.function.Module, Pos: l.pos(node), Result: to.Result,
		Closure: true, Outer: l.function, Adapts: true, Captures: []*ir.Local{wrapped}}
	failed := false
	l.inFunction(fn, func() {
		for i, t := range to.Params {
			fn.Params = append(fn.Params, l.newLocal(fmt.Sprintf("arg%d", i+1), t))
		}
		args := l.adaptedArguments(fn.Params[skip:], from, &ir.FuncType{Params: to.Params[skip:], Result: to.Result}, node)
		if failed = args == nil; failed {
			return
		}
		call := ir.Expr(&ir.CallValue{Callee: &ir.LocalRef{Local: wrapped}, Args: args})
		if to.Result == ir.Void {
			fn.Body = &ir.Block{At: fn.Pos, Stmts: []ir.Stmt{&ir.ExprStmt{At: fn.Pos, X: call}}}
			return
		}
		var result ir.Expr
		switch {
		case from.Result == ir.Void:
			result = l.voidResult(call, to.Result) // `() => cart.clear()` as a `() => unknown`
		case from.Result == ir.Value && to.Result != ir.Value:
			result = l.dynamicConversion(call, to.Result, node) // what an erased copy returns, or an any (D-078)
		default:
			result = l.convert(call, to.Result, node)
		}
		failed = result == nil
		fn.Body = &ir.Block{At: fn.Pos, Stmts: []ir.Stmt{&ir.Return{At: fn.Pos, Value: result}}}
	})
	if failed {
		return nil
	}
	return &ir.Bind{Local: wrapped, Value: value, In: &ir.MakeClosure{Func: fn, FuncType: to}}
}

// voidResult is call, of a function that returns nothing, as a call returning result:
// undefined where result holds it, as in JS; anywhere else tsc took the function to
// return never (`() => { throw e; }` as a `() => string`), so nothing is ever returned.
func (l *lowerer) voidResult(call ir.Expr, result ir.Type) ir.Expr {
	if result == ir.Value || result == ir.Undefined || isUnion(result) && result.(*ir.Union).Has(ir.Undefined) {
		return &ir.Sequence{X: call, Y: l.convert(&ir.UndefinedConst{}, result, nil)}
	}
	return &ir.Sequence{X: call, Y: &ir.LeftOut{T: result}}
}

// adaptedArguments are the arguments an adapter from the function type from to the
// function type to passes the function it wraps, from its own parameters params (of the
// types of to's), or nil if they don't fit (reported at node).
func (l *lowerer) adaptedArguments(params []*ir.Local, from, to *ir.FuncType, node *ast.Node) []ir.Expr {
	args := make([]ir.Expr, 0, len(from.Params))
	i := 0        // in params
	unpacked := 0 // elements taken from params[i] if it is a rest parameter
	for j := 0; j < len(from.Params); j++ {
		t, optional := from.Params[j], hasDefault(from.Params, j)
		if optional {
			j++ // past the flag
		}
		if rest, ok := t.(*ir.Rest); ok && (i >= len(params) || !isRest(params[i].Type)) { // the last: pack what is left
			packed := l.packedArguments(params[i:], rest, from, to, node)
			if packed == nil {
				return nil
			}
			return append(args, packed)
		}
		if i < len(params) && isRest(params[i].Type) && !isRest(t) { // take each from the array the adapter gets
			if optional {
				l.notSupportedYet(node, fmt.Sprintf("using a %s as a %s, which passes its arguments as an array, is", from, to))
				return nil
			}
			element := &ir.ArrayCall{Op: ir.ArrayElementChecked, Args: []ir.Expr{&ir.RestArgs{X: &ir.LocalRef{Local: params[i]}}, &ir.NumberConst{Value: float64(unpacked)}}}
			unpacked++
			arg := l.convert(element, t, node)
			if arg == nil {
				return nil
			}
			args = append(args, arg)
			continue
		}
		if i >= len(params) {
			if !optional {
				l.notSupportedYet(node, fmt.Sprintf("using a %s as a %s, which passes fewer arguments, is", from, to))
				return nil
			}
			args = append(args, notPassed(t)...)
			continue
		}
		passedOrNot := hasDefault(to.Params, i)
		if u, ok := t.(*ir.Union); passedOrNot && !optional && ok && u.Has(ir.Undefined) { // `(a, b) => …` for `(a: T, b?: U) => …`
			arg := l.convert(&ir.LocalRef{Local: params[i]}, u, node)
			if arg == nil {
				return nil
			}
			args = append(args, &ir.Conditional{Cond: &ir.LocalRef{Local: params[i+1]}, Then: arg, Else: &ir.ToUnion{X: &ir.UndefinedConst{}, To: u}})
			i += 2
			continue
		}
		if optional && !passedOrNot && l.maybeUndefinedFor(params[i].Type, t) { // `(state = [], a) => …` for `(state: S | undefined, a: A) => …`
			passed := l.maybePassed(&ir.LocalRef{Local: params[i]}, t, node) // undefined leaves the argument out (D-055)
			if passed == nil {
				return nil
			}
			args = append(args, passed...)
			i++
			continue
		}
		if passedOrNot && !optional {
			l.notSupportedYet(node, fmt.Sprintf("using a %s as a %s, which may leave out an argument it needs, is", from, to))
			return nil
		}
		arg := l.convert(&ir.LocalRef{Local: params[i]}, t, node)
		if arg == nil {
			return nil
		}
		args = append(args, arg)
		i++
		switch {
		case passedOrNot:
			args = append(args, &ir.LocalRef{Local: params[i]}) // its flag
			i++
		case optional:
			args = append(args, &ir.GivenConst{Value: true})
		}
	}
	return args
}

// packedArguments is the array an adapter from the function type from to the function
// type to passes the rest parameter of type rest: its own parameters params, each
// converted to the element type. It reports (at node) and returns nil if one of them is
// optional or a rest parameter itself.
func (l *lowerer) packedArguments(params []*ir.Local, rest *ir.Rest, from, to *ir.FuncType, node *ast.Node) ir.Expr {
	elems := make([]ir.Expr, len(params))
	for i, param := range params {
		if param.Type == ir.Given || isRest(param.Type) || i+1 < len(params) && params[i+1].Type == ir.Given {
			l.notSupportedYet(node, fmt.Sprintf("using a %s as a %s, which may leave out arguments of its rest parameter, is", from, to))
			return nil
		}
		if elems[i] = l.convert(&ir.LocalRef{Local: param}, rest.Array.Elem, node); elems[i] == nil {
			return nil
		}
	}
	return &ir.NewArray{Array: rest.Array, Elems: elems}
}

// ---------------------------------------------------------------- cells

// markCells decides which captured locals live in cells (D-041): those assigned after
// their declaration, and those that a closure may capture before their declaration has
// run (a hoisted function declaration, or a closure in the local's own initialiser). The
// others are copied into the closures that capture them, since they can't change.
func markCells(funcs []*ir.Func) {
	captured := map[*ir.Local]bool{}
	for _, fn := range funcs {
		for _, local := range fn.Captures {
			captured[local] = true
		}
	}
	for _, fn := range funcs {
		if fn.Body == nil {
			continue // it failed to lower (reported)
		}
		pass := &cellPass{captured: captured, declared: map[*ir.Local]bool{}}
		for _, local := range append(append(slices.Clone(fn.Params), fn.Captures...), fn.Self) {
			pass.declared[local] = true
		}
		pass.stmts(fn.Body.Stmts)
	}
}

// cellPass walks a function's body in the order it runs, tracking which locals have
// been declared, and marks the captured locals that need cells.
type cellPass struct {
	captured map[*ir.Local]bool
	declared map[*ir.Local]bool
}

func (p *cellPass) stmts(stmts []ir.Stmt) {
	for _, stmt := range stmts {
		switch s := stmt.(type) {
		case *ir.Block:
			p.stmts(s.Stmts)
		case *ir.ExprStmt:
			p.expr(s.X)
		case *ir.LocalDecl:
			p.expr(s.Init)
			p.declared[s.Local] = true
		case *ir.Return:
			p.expr(s.Value)
		case *ir.If:
			p.expr(s.Cond)
			p.stmts(s.Then.Stmts)
			if s.Else != nil {
				p.stmts([]ir.Stmt{s.Else})
			}
		case *ir.Loop:
			p.expr(s.Cond)
			p.stmts(s.Body.Stmts)
			p.expr(s.Update)
		case *ir.Switch:
			for _, c := range s.Cases {
				p.expr(c.Match)
				p.stmts(c.Body)
			}
		case *ir.LabeledBlock:
			p.stmts(s.Body.Stmts)
		case *ir.Try:
			p.stmts(s.Body.Stmts)
			p.declared[s.Exception] = true
			p.stmts(s.Catch.Stmts)
		case *ir.Throw:
			p.expr(s.Value)
		case *ir.Yield:
			p.expr(s.Value)
		}
	}
}

func (p *cellPass) expr(x ir.Expr) {
	ir.WalkExpr(x, func(x ir.Expr) {
		switch x := x.(type) {
		case *ir.Assign:
			if p.captured[x.Local] {
				x.Local.Cell = true
			}
		case *ir.Bind:
			p.declared[x.Local] = true // a temporary, which its value never uses
		case *ir.MakeClosure:
			for _, local := range x.Func.Captures {
				if !p.declared[local] {
					local.Cell = true
				}
			}
		}
	})
}
