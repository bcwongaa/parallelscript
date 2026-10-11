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
	switch {
	case len(signature.TypeParameters()) > 0:
		l.notSupportedYet(node, "generic function types are")
		return nil
	case signature.HasRestParameter():
		l.notSupportedYet(node, "rest parameters are")
		return nil
	case signature.MinArgumentCount() < len(signature.Parameters()):
		l.notSupportedYet(node, "optional parameters are")
		return nil
	}
	params := make([]ir.Type, len(signature.Parameters()))
	for i, param := range signature.Parameters() {
		if params[i] = l.irType(l.checker.GetTypeOfSymbol(param), node); params[i] == nil {
			return nil
		}
	}
	result := l.checker.GetReturnTypeOfSignature(signature)
	if result.Flags()&checker.TypeFlagsVoid != 0 {
		return l.funcType(params, ir.Void)
	}
	resultType := l.irType(result, node)
	if resultType == nil {
		return nil
	}
	return l.funcType(params, resultType)
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

// hasOtherNumericTypes reports whether a function type takes or returns an integer type
// or f32. Values of such types can't be made yet: the checker sees those types as
// number, so the semantic pass can't check the conversions at their calls (M2.6).
func hasOtherNumericTypes(ft *ir.FuncType) bool {
	for _, t := range append([]ir.Type{ft.Result}, ft.Params...) {
		if ir.IsNumeric(t) && t != ir.Number {
			return true
		}
	}
	return false
}

func (l *lowerer) integerSignature(node *ast.Node) {
	l.notSupportedYet(node, "function values that take or return integer types or f32 are")
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
	case node.TypeParameterList() != nil:
		l.notSupportedYet(node, "generic functions are")
		return nil
	case flags&ast.FunctionFlagsGenerator != 0:
		l.notSupportedYet(node, "generator functions are")
		return nil
	case flags&ast.FunctionFlagsAsync != 0:
		l.notSupportedYet(node, "async functions are")
		return nil
	}
	name := l.jsName(node)
	fn := &ir.Func{Name: l.closureName(name, node), Module: l.function.Module, Pos: l.pos(node), Closure: true, Outer: l.function, JSName: name}
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
	if hasOtherNumericTypes(ft) {
		l.integerSignature(node)
		return nil
	}
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
		local := l.parameter(param)
		if local == nil {
			return false
		}
		fn.Params = append(fn.Params, local)
	}
	if fn.Result = l.resultType(node); fn.Result == nil {
		return false
	}
	context := l.contextualFuncType(node)
	if context == nil {
		return true
	}
	for i := len(fn.Params); i < len(context.Params); i++ {
		fn.Params = append(fn.Params, l.newLocal("unused", context.Params[i]))
	}
	switch {
	case context.Result == ir.Void:
		fn.Result = ir.Void
	case context.Result == ir.Number && ir.IsNumeric(fn.Result) && node.Type() == nil:
		fn.Result = ir.Number // an inferred integer result widens to the context's number, losslessly
	}
	return true
}

// contextualFuncType is the function type node's context expects, if it is one that
// node's function can take the shape of: the same parameter types, then perhaps more.
// It reports nothing: a context that doesn't map just doesn't shape the function.
func (l *lowerer) contextualFuncType(node *ast.Node) *ir.FuncType {
	contextual := l.checker.GetContextualType(node, checker.ContextFlagsNone)
	if contextual == nil {
		return nil
	}
	reported := len(l.errors)
	ft, ok := l.functionType(contextual, node)
	l.errors = l.errors[:reported]
	if !ok || ft == nil || len(ft.Params) < len(l.function.Params) {
		return nil
	}
	for i, param := range l.function.Params {
		if ft.Params[i] != param.Type {
			return nil
		}
	}
	return ft
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
		if parent.Initializer() == child && parent.Name() != nil && (parent.Name().Kind == ast.KindIdentifier || parent.Name().Kind == ast.KindStringLiteral) {
			return parent.Name().Text()
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

// functionValue is the value of a function declaration (node names it): the closure of a
// nested one, held by the local its block made it in, or a top-level one's static closure.
func (l *lowerer) functionValue(node *ast.Node, fn *ir.Func) ir.Expr {
	ft := l.typeOfFunc(fn)
	if hasOtherNumericTypes(ft) {
		l.integerSignature(node)
		return nil
	}
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
	ft, ok := callee.Type().(*ir.FuncType)
	if !ok {
		l.notSupportedYet(call.Expression, fmt.Sprintf("calling a %s is", callee.Type()))
		return nil
	}
	args := l.typedArguments(call.Arguments.Nodes, ft.Params)
	if args == nil {
		return nil
	}
	return &ir.CallValue{Callee: callee, Args: args}
}

// isValueCall reports whether a call's callee is a function value of the program rather
// than a library function.
func (l *lowerer) isValueCall(callee *ast.Node) bool {
	if symbol := l.target(callee); symbol != nil && (l.isDeclaredInDefaultLib(symbol) || l.isDeclaredInPtsLib(symbol)) {
		return false
	}
	return len(l.checker.GetSignaturesOfType(l.checker.GetTypeAtLocation(callee), checker.SignatureKindCall)) > 0
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
// it with the parameters it takes and converts its result. It reports (at node) and
// returns nil if the types don't fit.
func (l *lowerer) adapt(value ir.Expr, to *ir.FuncType, node *ast.Node) ir.Expr {
	from := value.Type().(*ir.FuncType)
	if hasOtherNumericTypes(from) || hasOtherNumericTypes(to) {
		l.integerSignature(node)
		return nil
	}
	if len(from.Params) > len(to.Params) {
		l.notSupportedYet(node, fmt.Sprintf("using a %s as a %s, which passes fewer arguments, is", from, to))
		return nil
	}
	wrapped := l.newLocal("adapted", from)
	fn := &ir.Func{Name: l.closureName("adapter", node), Module: l.function.Module, Pos: l.pos(node), Result: to.Result,
		Closure: true, Outer: l.function, Adapts: true, Captures: []*ir.Local{wrapped}}
	failed := false
	l.inFunction(fn, func() {
		args := make([]ir.Expr, len(from.Params))
		for i, t := range to.Params {
			param := l.newLocal(fmt.Sprintf("arg%d", i+1), t)
			fn.Params = append(fn.Params, param)
			if i < len(from.Params) {
				args[i] = l.convert(&ir.LocalRef{Local: param}, from.Params[i], node)
				failed = failed || args[i] == nil
			}
		}
		if failed {
			return
		}
		call := ir.Expr(&ir.CallValue{Callee: &ir.LocalRef{Local: wrapped}, Args: args})
		if to.Result == ir.Void {
			fn.Body = &ir.Block{At: fn.Pos, Stmts: []ir.Stmt{&ir.ExprStmt{At: fn.Pos, X: call}}}
			return
		}
		if from.Result == ir.Void {
			l.notSupportedYet(node, fmt.Sprintf("using a %s as a %s, whose result it doesn't return, is", from, to))
			failed = true
			return
		}
		result := l.convert(call, to.Result, node)
		failed = result == nil
		fn.Body = &ir.Block{At: fn.Pos, Stmts: []ir.Stmt{&ir.Return{At: fn.Pos, Value: result}}}
	})
	if failed {
		return nil
	}
	return &ir.Bind{Local: wrapped, Value: value, In: &ir.MakeClosure{Func: fn, FuncType: to}}
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
