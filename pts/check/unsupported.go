package check

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/pts/diagnostic"
)

// unsupportedGlobals are the ES library globals left out by design (docs/SPEC.md §3.10), with why.
var unsupportedGlobals = map[string]string{
	"eval":     "`eval` is not supported: programs are compiled ahead of time, so there is no compiler at run time",
	"Function": "`Function` is not supported: programs are compiled ahead of time, so there is no compiler at run time",
	"Proxy":    "`Proxy` is not supported: objects have fixed layouts, so operations on them can't be intercepted",
	"Reflect":  "`Reflect` is not supported: objects have fixed layouts, so their properties can't be enumerated or redefined at run time",
}

// supportedSymbolMembers are the only well-known symbols ParallelScript has (D-O14).
var supportedSymbolMembers = map[string]bool{"iterator": true, "asyncIterator": true}

// unsupportedFeatures reports the features ParallelScript leaves out (docs/SPEC.md
// §3.10), each with the reason, wherever they appear under node.
func (c *pass) unsupportedFeatures(node *ast.Node) {
	c.unsupportedFeature(node)
	node.ForEachChild(func(child *ast.Node) bool {
		c.unsupportedFeatures(child)
		return false
	})
}

func (c *pass) unsupportedFeature(node *ast.Node) {
	switch node.Kind {
	case ast.KindVariableDeclarationList:
		if node.Flags&ast.NodeFlagsBlockScoped == 0 {
			at := node
			if node.Parent.Kind == ast.KindVariableStatement {
				at = node.Parent
			}
			c.report(at, diagnostic.Unsupported, "`var` is not supported; use `let` or `const`")
		}
	case ast.KindIdentifier:
		c.unsupportedIdentifier(node)
	case ast.KindPropertyAccessExpression:
		c.unsupportedProperty(node)
	case ast.KindBinaryExpression:
		if ast.IsAssignmentOperator(node.AsBinaryExpression().OperatorToken.Kind) && assignsToPrototype(node.AsBinaryExpression().Left) {
			c.report(node, diagnostic.Unsupported, "assigning to `.prototype` is not supported: classes have fixed layouts and method tables; declare members in the class")
		}
	case ast.KindModuleDeclaration:
		c.report(node, diagnostic.Unsupported, "namespaces are not supported; use ES modules (`import` and `export`)")
	case ast.KindBigIntLiteral, ast.KindBigIntKeyword:
		c.report(node, diagnostic.Unsupported, "BigInt is not supported yet")
	case ast.KindSymbolKeyword:
		c.report(node, diagnostic.Unsupported, "`symbol` is not supported, except `Symbol.iterator` and `Symbol.asyncIterator`")
	case ast.KindRegularExpressionLiteral:
		c.report(node, diagnostic.NotSupportedYet, "RegExp arrives in M6")
	case ast.KindCallExpression:
		c.parallelOperator(node)
	case ast.KindClassDeclaration, ast.KindFunctionDeclaration, ast.KindEnumDeclaration, ast.KindInterfaceDeclaration:
		c.declarationMerging(node)
	}
}

// unsupportedIdentifier reports `arguments` and the globals left out by design.
func (c *pass) unsupportedIdentifier(node *ast.Node) {
	if ast.IsDeclarationName(node) {
		return
	}
	symbol := c.tc.GetSymbolAtLocation(node)
	switch {
	case symbol == nil:
	case node.Text() == "arguments" && len(symbol.Declarations) == 0:
		c.report(node, diagnostic.Unsupported, "`arguments` is not supported; use a rest parameter (`...args`)")
	case unsupportedGlobals[node.Text()] != "" && c.isGlobal(symbol):
		c.report(node, diagnostic.Unsupported, "%s", unsupportedGlobals[node.Text()])
	case node.Text() == "Symbol" && c.isGlobal(symbol) && !isSupportedSymbolMember(node):
		c.report(node, diagnostic.Unsupported, "`Symbol` is not supported, except `Symbol.iterator` and `Symbol.asyncIterator`")
	}
}

// isGlobal reports whether symbol is declared in the ES library (not shadowed by the program).
func (c *pass) isGlobal(symbol *ast.Symbol) bool {
	for _, declaration := range symbol.Declarations {
		if !c.program.IsSourceFileDefaultLibrary(ast.GetSourceFileOfNode(declaration).Path()) {
			return false
		}
	}
	return len(symbol.Declarations) > 0
}

func isSupportedSymbolMember(node *ast.Node) bool {
	parent := node.Parent
	return parent.Kind == ast.KindPropertyAccessExpression && parent.Expression() == node &&
		supportedSymbolMembers[parent.AsPropertyAccessExpression().Name().Text()]
}

// unsupportedProperty reports prototype manipulation: `__proto__` and Object.setPrototypeOf.
func (c *pass) unsupportedProperty(node *ast.Node) {
	name := node.AsPropertyAccessExpression().Name().Text()
	switch {
	case name == "__proto__":
		c.report(node, diagnostic.Unsupported, "`__proto__` is not supported: an object's class is fixed when it is created")
	case name == "setPrototypeOf" && node.Expression().Kind == ast.KindIdentifier && node.Expression().Text() == "Object" &&
		c.isGlobalSymbolAt(node.Expression()):
		c.report(node, diagnostic.Unsupported, "`Object.setPrototypeOf` is not supported: an object's class is fixed when it is created")
	}
}

func (c *pass) isGlobalSymbolAt(node *ast.Node) bool {
	symbol := c.tc.GetSymbolAtLocation(node)
	return symbol != nil && c.isGlobal(symbol)
}

// assignsToPrototype reports whether an assignment target is `x.prototype` or a member of it.
func assignsToPrototype(target *ast.Node) bool {
	for target.Kind == ast.KindPropertyAccessExpression || target.Kind == ast.KindElementAccessExpression {
		if target.Kind == ast.KindPropertyAccessExpression && target.AsPropertyAccessExpression().Name().Text() == "prototype" {
			return true
		}
		target = target.Expression()
	}
	return false
}

// declarationMerging reports a declaration that merges with an earlier one of the same
// name. Function overloads and interfaces merging only with interfaces are fine: neither
// changes what exists at run time.
func (c *pass) declarationMerging(node *ast.Node) {
	name := node.Name()
	if name == nil {
		return
	}
	symbol := c.tc.GetSymbolAtLocation(name)
	if symbol == nil || len(symbol.Declarations) < 2 || symbol.Declarations[0] == node {
		return
	}
	allFunctions, allInterfaces := true, true
	for _, declaration := range symbol.Declarations {
		allFunctions = allFunctions && declaration.Kind == ast.KindFunctionDeclaration
		allInterfaces = allInterfaces && declaration.Kind == ast.KindInterfaceDeclaration
	}
	if !allFunctions && !allInterfaces {
		c.report(name, diagnostic.Unsupported, "declaration merging is not supported: `%s` is declared more than once", name.Text())
	}
}
