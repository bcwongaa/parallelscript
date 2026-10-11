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
		if declaration.Kind == ast.KindFunctionDeclaration && declaration.Body() != nil {
			return declaration
		}
	}
	return nil
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
		if n, ok := c.annotationType(declaration.Type()); ok {
			c.setResult(declaration, fn, n)
		} else {
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
