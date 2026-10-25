package check

import (
	"slices"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/pts/diagnostic"
	"github.com/microsoft/typescript-go/pts/numeric"
)

// function is what the pass knows about a function declaration's numbers.
type function struct {
	params      []*Numeric // each parameter's numeric type, or nil if it isn't a number
	result      *Numeric   // the numeric result type, or nil if the function doesn't return a number (or isn't inferred yet)
	returned    *Numeric   // for a union result with one numeric type (`u8 | null`), that type, which returned numbers convert to
	inferResult bool       // it returns a number without saying which type: the return statements decide
	visited     bool       // its body has been checked, or is being checked
}

// functionBody is the function whose body is being checked, with the return
// statements seen so far when its result type is inferred.
type functionBody struct {
	fn      *function
	typed   []Numeric
	untyped []*ast.Node // returned constants, typed once the others decide the result type
}

// calledFunction returns the declaration of the function a call calls, or nil if it
// calls something other than a function declared in the program.
func (c *pass) calledFunction(call *ast.Node) *ast.Node {
	symbol := c.target(ast.SkipParentheses(call.AsCallExpression().Expression))
	if symbol == nil {
		return nil
	}
	for _, declaration := range symbol.Declarations {
		if isFunctionWithBody(declaration) || isSignature(declaration) {
			return declaration
		}
	}
	return nil
}

// constructorOf returns the constructor a `new` expression runs, if its class or a superclass declares one.
func (c *pass) constructorOf(node *ast.Node) *ast.Node {
	symbol := c.target(node.Expression())
	if symbol == nil || symbol.ValueDeclaration == nil || symbol.ValueDeclaration.Kind != ast.KindClassDeclaration {
		return nil
	}
	return c.classConstructor(symbol.ValueDeclaration)
}

// superConstructor returns the constructor `super(args)` runs, if the superclass or one of its superclasses declares one.
func (c *pass) superConstructor(call *ast.Node) *ast.Node {
	return c.classConstructor(c.superclass(ast.GetContainingClass(call)))
}

// classConstructor returns the constructor that runs for a class: its own, or if it
// declares none, its nearest superclass's; nil if none declares one.
func (c *pass) classConstructor(class *ast.Node) *ast.Node {
	for ; class != nil; class = c.superclass(class) {
		for _, member := range class.Members() {
			if member.Kind == ast.KindConstructor && member.Body() != nil {
				return member
			}
		}
	}
	return nil
}

// superclass returns the declaration of the class a class extends, or nil.
func (c *pass) superclass(class *ast.Node) *ast.Node {
	heritage := ast.GetExtendsHeritageClauseElement(class)
	if heritage == nil {
		return nil
	}
	symbol := c.target(heritage.Expression())
	if symbol == nil || symbol.ValueDeclaration == nil || symbol.ValueDeclaration.Kind != ast.KindClassDeclaration {
		return nil
	}
	return symbol.ValueDeclaration
}

// inheritedMember returns the declaration of the member of the same name and kind (and
// static or not) that a class member redeclares or overrides, in the nearest superclass
// that has one, or nil.
func (c *pass) inheritedMember(member *ast.Node) *ast.Node {
	name := member.Name()
	if name == nil || name.Kind != ast.KindIdentifier {
		return nil
	}
	for class := c.superclass(member.Parent); class != nil; class = c.superclass(class) {
		for _, other := range class.Members() {
			if other.Kind == member.Kind && other.Name() != nil && other.Name().Kind == ast.KindIdentifier && other.Name().Text() == name.Text() && ast.IsStatic(other) == ast.IsStatic(member) {
				return other
			}
		}
	}
	return nil
}

// inheritedFieldType returns the numeric type of the inherited field that a field
// declaration redeclares, if it does.
func (c *pass) inheritedFieldType(node *ast.Node) (Numeric, bool) {
	inherited := c.inheritedMember(node)
	if inherited == nil || inherited.Kind != ast.KindPropertyDeclaration {
		return 0, false
	}
	return c.bindingType(c.tc.GetMergedSymbol(inherited.Symbol()))
}

// overriddenResultType returns the numeric result type of the method that a method
// declaration overrides, if it overrides one that returns a number.
func (c *pass) overriddenResultType(declaration *ast.Node) (Numeric, bool) {
	inherited := c.inheritedMember(declaration)
	if inherited == nil {
		return 0, false
	}
	if fn := c.functionOf(inherited); fn.result != nil {
		return *fn.result, true
	}
	return 0, false
}

// isSuperCall reports whether node is `super(args)`.
func isSuperCall(node *ast.Node) bool {
	return node.Kind == ast.KindCallExpression && node.Expression().Kind == ast.KindSuperKeyword
}

// isSignature reports whether node declares a method or accessor without a body that
// calls still go to: an abstract one, or one of an interface or type literal.
func isSignature(node *ast.Node) bool {
	switch node.Kind {
	case ast.KindMethodSignature:
		return true
	case ast.KindMethodDeclaration, ast.KindGetAccessor, ast.KindSetAccessor:
		return node.Body() == nil && (ast.HasSyntacticModifier(node, ast.ModifierFlagsAbstract) || isObjectTypeMember(node))
	}
	return false
}

// isObjectTypeMember reports whether node is a member of an interface or type literal.
func isObjectTypeMember(node *ast.Node) bool {
	return node.Parent.Kind == ast.KindInterfaceDeclaration || node.Parent.Kind == ast.KindTypeLiteral
}

// isFunctionWithBody reports whether node declares a function, method, accessor or
// constructor with a body, or is an arrow function or function expression.
func isFunctionWithBody(node *ast.Node) bool {
	switch node.Kind {
	case ast.KindFunctionDeclaration, ast.KindMethodDeclaration, ast.KindConstructor, ast.KindGetAccessor, ast.KindSetAccessor,
		ast.KindArrowFunction, ast.KindFunctionExpression:
		return node.Body() != nil
	}
	return false
}

// functionOf returns what the pass knows about a function, checking its body first if
// the result type depends on it.
func (c *pass) functionOf(declaration *ast.Node) *function {
	fn, ok := c.functions[declaration]
	if !ok {
		fn = c.signature(declaration)
		c.functions[declaration] = fn
	}
	if fn.inferResult && !fn.visited {
		c.checkBody(declaration, fn)
	}
	return fn
}

// signature types a function's parameters and, if annotated, its result.
func (c *pass) signature(declaration *ast.Node) *function {
	fn := &function{}
	for _, param := range declaration.Parameters() {
		var t *Numeric
		if name := param.Name(); name.Kind == ast.KindIdentifier {
			symbol := c.tc.GetSymbolAtLocation(name)
			if n, ok := c.parameterNumeric(symbol); ok { // annotated, or typed by context: x in `bytes.map(x => …)` is a u8
				t = &n
				c.symbols[symbol] = n
				c.info.types[name] = n
			} else if n, ok := NumericContext(c.tc.GetTypeOfSymbol(symbol)); ok {
				t = &n // a union parameter (`x: u8 | null`): arguments that are numbers convert to its numeric member
			}
		}
		fn.params = append(fn.params, t)
	}
	if declaration.Type() != nil && !c.returnsNumber(declaration) {
		if n, ok := NumericContext(c.tc.GetTypeFromTypeNode(declaration.Type())); ok {
			fn.returned = &n
		}
	}
	if c.returnsNumber(declaration) {
		n, ok := c.annotationType(declaration.Type())
		if !ok && (declaration.Kind == ast.KindMethodDeclaration || declaration.Kind == ast.KindGetAccessor) {
			n, ok = c.overriddenResultType(declaration) // an override returns what the method it overrides does
		}
		switch {
		case ok:
			c.setResult(declaration, fn, n)
		case declaration.Body() == nil:
			c.setResult(declaration, fn, Number)
		default:
			fn.inferResult = true
		}
	}
	return fn
}

// parameterNumeric returns the numeric type of a parameter, if it is a number: for an
// optional parameter (`x?: u8` in a signature, or `x = 1` where the context's is
// optional), of the value a call passes.
func (c *pass) parameterNumeric(symbol *ast.Symbol) (Numeric, bool) {
	t := c.tc.GetTypeOfSymbol(symbol)
	if declaration := symbol.ValueDeclaration; declaration != nil && declaration.Kind == ast.KindParameter && passedOrNot(declaration) && !c.defaultHoldsUndefined(declaration) {
		t = c.tc.RemoveMissingOrUndefinedType(t)
	}
	if !IsNumber(t) {
		return 0, false
	}
	if n, ok := NumericOf(t); ok {
		return n, true
	}
	return Number, true
}

// passedOrNot reports whether a parameter declaration holds undefined only when the
// call leaves its argument out: one with a question mark, and one with a default and no
// type, which takes the contextual type `T | undefined` of an optional parameter.
func passedOrNot(declaration *ast.Node) bool {
	param := declaration.AsParameterDeclaration()
	return param.QuestionToken != nil || param.Initializer != nil && param.Type == nil
}

// defaultHoldsUndefined reports whether a parameter declaration's default has undefined
// among its values (`v = m.get(k)`), so that tsc gives the parameter undefined in the body
// too: it isn't a number there.
func (c *pass) defaultHoldsUndefined(declaration *ast.Node) bool {
	initializer := declaration.Initializer()
	if initializer == nil {
		return false
	}
	t := c.tc.GetTypeAtLocation(initializer)
	const undefined = checker.TypeFlagsUndefined | checker.TypeFlagsVoid
	if t.Flags()&checker.TypeFlagsUnion != 0 {
		return slices.ContainsFunc(t.Types(), func(member *checker.Type) bool { return member.Flags()&undefined != 0 })
	}
	return t.Flags()&undefined != 0
}

func (c *pass) returnsNumber(declaration *ast.Node) bool {
	result := c.tc.GetReturnTypeOfSignature(c.tc.GetSignatureFromDeclaration(declaration))
	return IsNumber(result)
}

func (c *pass) setResult(declaration *ast.Node, fn *function, n Numeric) {
	fn.result = &n
	c.info.results[declaration] = n
}

// checkBody checks a function's body; an inferred result type is the common type of
// what its return statements return.
func (c *pass) checkBody(declaration *ast.Node, fn *function) {
	fn.visited = true
	outer := c.current
	body := &functionBody{fn: fn}
	c.current = body
	c.parameterDefaults(declaration, fn)
	if declaration.Body().Kind == ast.KindBlock {
		for _, statement := range declaration.Body().Statements() {
			c.visit(statement)
		}
	} else {
		c.returnValue(declaration.Body(), declaration.Body()) // an arrow function's expression body is what it returns
	}
	c.current = outer
	if !fn.inferResult {
		return
	}
	result := Number
	for i, t := range body.typed {
		if i == 0 {
			result = t
		} else if unified, ok := numeric.Unify(result, t); ok {
			result = unified
		} else {
			name, at := "the default export", declaration
			if declaration.Name() != nil {
				name, at = "`"+declaration.Name().Text()+"`", declaration.Name()
			}
			c.report(at, diagnostic.NumericMix, "%s returns both %s and %s, and neither converts to the other without loss; annotate its return type", name, result, t)
		}
	}
	for _, constant := range body.untyped {
		if result.IsInteger() && !numeric.IsIntegerConstant(constant) {
			result = Number
		}
	}
	for _, constant := range body.untyped {
		c.expr(constant, &result)
	}
	c.setResult(declaration, fn, result)
}

// parameterDefaults types the default values of a function's parameters: a number
// parameter's default must convert to its type.
func (c *pass) parameterDefaults(declaration *ast.Node, fn *function) {
	for i, param := range declaration.Parameters() {
		value := param.Initializer()
		switch {
		case value == nil:
		case fn.params[i] != nil && !c.defaultHoldsUndefined(param): // `v: int | undefined = undefined` holds undefined (R1-F9)
			c.requireConvertible(value, c.expr(value, fn.params[i]), *fn.params[i])
		default:
			c.visit(value)
		}
	}
}

// returnStatement types `return value` in a function that returns a number.
func (c *pass) returnStatement(node *ast.Node) {
	c.returnValue(node.Expression(), node)
}

// returnValue types a value a function returns (nil for none), which node returns.
func (c *pass) returnValue(value *ast.Node, node *ast.Node) {
	body := c.current
	switch {
	case value != nil && body.fn.returned != nil && c.isNumber(value):
		c.requireConvertible(value, c.expr(value, body.fn.returned), *body.fn.returned)
	case value == nil || (body.fn.result == nil && !body.fn.inferResult):
		if value == node {
			c.visit(value)
			return
		}
		node.ForEachChild(func(child *ast.Node) bool {
			c.visit(child)
			return false
		})
	case body.fn.inferResult:
		if numeric.IsUntyped(value) {
			body.untyped = append(body.untyped, value)
		} else {
			body.typed = append(body.typed, c.expr(value, nil))
		}
	default:
		c.requireConvertible(value, c.expr(value, body.fn.result), *body.fn.result)
	}
}

// call types a call of a declared function: each argument converts to its parameter's type.
func (c *pass) call(node *ast.Node, declaration *ast.Node) Numeric {
	fn := c.functionOf(declaration)
	c.visit(node.Expression()) // the callee: a.filter(f) in a.filter(f).reverse()
	for i, arg := range node.Arguments() {
		if i < len(fn.params) && fn.params[i] != nil && c.tc.GetTypeAtLocation(arg).Flags()&checker.TypeFlagsUndefined == 0 { // undefined means a parameter's default
			c.requireConvertible(arg, c.expr(arg, fn.params[i]), *fn.params[i])
		} else {
			c.visit(arg)
		}
	}
	if fn.result != nil {
		return *fn.result
	}
	if n, ok := NumericOf(c.tc.GetTypeAtLocation(node)); ok {
		return n // a generic function's result, such as reduce's T
	}
	return Number // a recursive call while the result type is being inferred; tsc requires an annotation there
}

// functionConversion reports a function value (node) whose context expects a function
// type that passes it arguments its parameters can't take without loss, or expects a
// result its own doesn't convert to without loss. tsc relates every numeric type to every
// other, but the lowering wraps such a value in an adapter that converts each argument
// and the result (D-041), which must not change them.
func (c *pass) functionConversion(node *ast.Node) {
	contextual := c.tc.GetContextualType(node, checker.ContextFlagsNone)
	if contextual == nil {
		return
	}
	target := c.singleCallSignature(contextual)
	source := c.singleCallSignature(c.tc.GetTypeAtLocation(node))
	if target == nil || source == nil {
		return
	}
	params, result := c.signatureNumerics(source)
	for i, param := range target.Parameters() {
		if i >= len(params) {
			break
		}
		if from, ok := c.parameterNumeric(param); ok && params[i] != nil && !numeric.ImplicitlyConvertible(from, *params[i]) {
			c.report(node, diagnostic.NumericConversion,
				"this function takes a %s where its context passes a %s, which doesn't convert to %s implicitly because the value could change", *params[i], from, *params[i])
			return
		}
	}
	if to, ok := NumericOf(c.tc.GetReturnTypeOfSignature(target)); ok && result != nil && !numeric.ImplicitlyConvertible(*result, to) {
		c.report(node, diagnostic.NumericConversion,
			"this function returns a %s where its context expects a %s; a %s doesn't convert to %s implicitly because the value could change", *result, to, *result, to)
	}
}

func (c *pass) singleCallSignature(t *checker.Type) *checker.Signature {
	if signatures := c.tc.GetSignaturesOfType(t, checker.SignatureKindCall); len(signatures) == 1 {
		return signatures[0]
	}
	return nil
}

// signatureNumerics returns the numeric types of a signature's parameters (nil for those
// that aren't numbers) and result: the pass's own for a function it types, whose result it
// may infer narrower than tsc does; otherwise tsc's.
func (c *pass) signatureNumerics(signature *checker.Signature) ([]*Numeric, *Numeric) {
	if declaration := signature.Declaration(); declaration != nil && (isFunctionWithBody(declaration) || isSignature(declaration)) {
		fn := c.functionOf(declaration)
		return fn.params, fn.result
	}
	var params []*Numeric
	for _, param := range signature.Parameters() {
		if n, ok := c.parameterNumeric(param); ok {
			params = append(params, &n)
		} else {
			params = append(params, nil)
		}
	}
	if n, ok := NumericOf(c.tc.GetReturnTypeOfSignature(signature)); ok {
		return params, &n
	}
	return params, nil
}
