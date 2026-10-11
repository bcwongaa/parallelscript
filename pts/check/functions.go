package check

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/pts/diagnostic"
)

// function is what the pass knows about a function declaration's numbers.
type function struct {
	params      []*Numeric // each parameter's numeric type, or nil if it isn't a number
	result      *Numeric   // the numeric result type, or nil if the function doesn't return a number (or isn't inferred yet)
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
		if isFunctionWithBody(declaration) || isAbstractMethod(declaration) {
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

// inheritedMember returns the declaration of the instance member of the same name that
// a class member redeclares or overrides, in the nearest superclass that has one, or nil.
func (c *pass) inheritedMember(member *ast.Node) *ast.Node {
	name := member.Name()
	if name == nil || name.Kind != ast.KindIdentifier {
		return nil
	}
	for class := c.superclass(member.Parent); class != nil; class = c.superclass(class) {
		for _, other := range class.Members() {
			if other.Name() != nil && other.Name().Kind == ast.KindIdentifier && other.Name().Text() == name.Text() && !ast.IsStatic(other) {
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
	if inherited == nil || inherited.Kind != ast.KindMethodDeclaration {
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

// isAbstractMethod reports whether node declares an abstract method, which has no body.
func isAbstractMethod(node *ast.Node) bool {
	return node.Kind == ast.KindMethodDeclaration && ast.HasSyntacticModifier(node, ast.ModifierFlagsAbstract)
}

// isFunctionWithBody reports whether node declares a function, method or constructor with a body.
func isFunctionWithBody(node *ast.Node) bool {
	switch node.Kind {
	case ast.KindFunctionDeclaration, ast.KindMethodDeclaration, ast.KindConstructor:
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
		if name := param.Name(); name.Kind == ast.KindIdentifier && c.isNumber(name) {
			n, ok := c.annotationType(param.Type())
			if !ok {
				n = Number
			}
			t = &n
			c.symbols[c.tc.GetSymbolAtLocation(name)] = n
			c.info.types[name] = n
		}
		fn.params = append(fn.params, t)
	}
	if c.returnsNumber(declaration) {
		n, ok := c.annotationType(declaration.Type())
		if !ok && declaration.Kind == ast.KindMethodDeclaration {
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

func (c *pass) returnsNumber(declaration *ast.Node) bool {
	result := c.tc.GetReturnTypeOfSignature(c.tc.GetSignatureFromDeclaration(declaration))
	return result.Flags()&checker.TypeFlagsNumberLike != 0
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
	for _, statement := range declaration.Body().Statements() {
		c.visit(statement)
	}
	c.current = outer
	if !fn.inferResult {
		return
	}
	result := Number
	for i, t := range body.typed {
		if i == 0 {
			result = t
		} else if unified, ok := unify(result, t); ok {
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
		if result.IsInteger() && !isIntegerConstant(constant) {
			result = Number
		}
	}
	for _, constant := range body.untyped {
		c.expr(constant, &result)
	}
	c.setResult(declaration, fn, result)
}

// returnStatement types `return value` in a function that returns a number.
func (c *pass) returnStatement(node *ast.Node) {
	value, body := node.Expression(), c.current
	switch {
	case value == nil || (body.fn.result == nil && !body.fn.inferResult):
		node.ForEachChild(func(child *ast.Node) bool {
			c.visit(child)
			return false
		})
	case body.fn.inferResult:
		if isUntyped(value) {
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
	for i, arg := range node.Arguments() {
		if i < len(fn.params) && fn.params[i] != nil {
			c.requireConvertible(arg, c.expr(arg, fn.params[i]), *fn.params[i])
		} else {
			c.visit(arg)
		}
	}
	if fn.result == nil {
		return Number // a recursive call while the result type is being inferred; tsc requires an annotation there
	}
	return *fn.result
}
