package check

import (
	"maps"
	"slices"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/pts/diagnostic"
	"github.com/microsoft/typescript-go/pts/lib"
)

// parallelOperator reports the form of `parallel e` that can't mean what it says (D-120):
// the operator where a binding named parallel is in scope (the parser made it a call,
// which would call the binding). Any operand is the operator's, a function too, whose
// task makes the function (D-150, ruling 1).
func (c *pass) parallelOperator(node *ast.Node) {
	if node.Kind != ast.KindCallExpression || node.Flags&ast.NodeFlagsPtsParallel == 0 {
		return
	}
	symbol := c.tc.GetSymbolAtLocation(node.Expression())
	if symbol == nil || len(symbol.Declarations) == 0 || !lib.IsLibPath(ast.GetSourceFileOfNode(symbol.Declarations[0]).FileName()) {
		c.report(node, diagnostic.Unsupported, "`parallel e` here would call the binding named `parallel` in scope; rename the binding to use the operator")
	}
}

// parallelBodies checks, under node, every `parallel for` (D-127) and the single
// guardrail (D-L17) on every parallel body: the operand of `parallel`, a `parallel for`
// body, and a function passed for a parameter of a ParallelCallback type (the `.par`
// methods, join).
func (c *pass) parallelBodies(node *ast.Node) {
	switch {
	case node.Kind == ast.KindCallExpression && (node.Flags&ast.NodeFlagsPtsParallel != 0 || c.isParallelCall(node)):
		operand := node.Arguments()[0]
		c.guardrail(operand, operand)
	case node.Flags&ast.NodeFlagsPtsParallel != 0:
		if c.parallelFor(node) {
			c.guardrail(node.Statement(), node)
		}
	case node.Kind == ast.KindCallExpression || node.Kind == ast.KindNewExpression:
		for _, argument := range node.Arguments() {
			if fn := c.parallelCallback(argument); fn != nil {
				c.guardrail(fn, fn)
			}
		}
	}
	node.ForEachChild(func(child *ast.Node) bool {
		c.parallelBodies(child)
		return false
	})
}

// isParallelCall reports whether node calls the prelude's `parallel`: `parallel(e)`, which
// is the operator too (D-O05).
func (c *pass) isParallelCall(node *ast.Node) bool {
	callee := ast.SkipParentheses(node.Expression())
	if callee.Kind != ast.KindIdentifier || callee.Text() != "parallel" || len(node.Arguments()) != 1 {
		return false
	}
	symbol := c.tc.GetSymbolAtLocation(callee)
	return symbol != nil && symbol.Flags&ast.SymbolFlagsFunction != 0 && len(symbol.Declarations) > 0 &&
		lib.IsLibPath(ast.GetSourceFileOfNode(symbol.Declarations[0]).FileName())
}

// parallelFor reports a `parallel for` that isn't a `for…of` loop over a declared
// variable, and the jumps out of its body (D-L16); it returns whether the loop is a
// `for…of` whose body is checked.
func (c *pass) parallelFor(node *ast.Node) bool {
	if node.Kind != ast.KindForOfStatement || node.AsForInOrOfStatement().AwaitModifier != nil {
		c.report(node, diagnostic.ParallelFor, "`parallel for` takes a `for...of` loop: `parallel for (const x of items) { … }`")
		return false
	}
	if node.Initializer().Kind != ast.KindVariableDeclarationList {
		c.report(node.Initializer(), diagnostic.ParallelFor, "`parallel for` declares its variable: `parallel for (const x of items)`")
		return false
	}
	var own string // the loop's label, which a `continue` may name
	if node.Parent.Kind == ast.KindLabeledStatement {
		own = node.Parent.AsLabeledStatement().Label.Text()
	}
	c.parallelForJumps(node.Statement(), own, false, map[string]bool{})
	return true
}

// parallelForJumps reports, under node in a `parallel for` body, what would leave the
// body: return, yield, await, break out of it and labelled jumps to statements outside it.
// inner is whether node is in a loop or switch of the body; labels are the body's own.
func (c *pass) parallelForJumps(node *ast.Node, own string, inner bool, labels map[string]bool) {
	why := "its iterations run at once, so nothing can leave it but `continue` and a throw"
	switch {
	case ast.IsFunctionLike(node) || ast.IsClassLike(node):
		return
	case node.Kind == ast.KindReturnStatement:
		c.report(node, diagnostic.ParallelFor, "`return` can't leave a `parallel for` body: %s", why)
	case node.Kind == ast.KindYieldExpression || node.Kind == ast.KindAwaitExpression:
		if node.Kind == ast.KindAwaitExpression {
			c.report(node, diagnostic.ParallelFor, "`await` can't be in a `parallel for` body, whose iterations run at once on the fork-join pool's threads; to wait for async work per item, start a task for each and await them together: `await Promise.all(items.map((x) => parallel work(x)))`")
			return
		}
		c.report(node, diagnostic.ParallelFor, "`%s` can't be in a `parallel for` body, whose iterations run at once on the fork-join pool's threads", scannerText(node))
	case node.Kind == ast.KindForOfStatement && node.AsForInOrOfStatement().AwaitModifier != nil:
		c.report(node, diagnostic.ParallelFor, "`for await` can't be in a `parallel for` body, whose iterations run at once on the fork-join pool's threads")
	case node.Kind == ast.KindBreakStatement:
		label := node.AsBreakStatement().Label
		if label != nil && !labels[label.Text()] || label == nil && !inner {
			c.report(node, diagnostic.ParallelFor, "`break` can't leave a `parallel for` body: %s", why)
		}
	case node.Kind == ast.KindContinueStatement:
		if label := node.AsContinueStatement().Label; label != nil && !labels[label.Text()] && label.Text() != own {
			c.report(node, diagnostic.ParallelFor, "`continue %s` can't leave a `parallel for` body: %s", label.Text(), why)
		}
	case node.Kind == ast.KindLabeledStatement:
		labels = maps.Clone(labels)
		labels[node.AsLabeledStatement().Label.Text()] = true
	case ast.IsIterationStatement(node, false) || node.Kind == ast.KindSwitchStatement:
		inner = true
	}
	node.ForEachChild(func(child *ast.Node) bool {
		c.parallelForJumps(child, own, inner, labels)
		return false
	})
}

// scannerText is `yield` or `await`, the keyword of a suspension.
func scannerText(node *ast.Node) string {
	if node.Kind == ast.KindYieldExpression {
		return "yield"
	}
	return "await"
}

// parallelCallback is the function an argument passes for a parameter of a ParallelCallback
// type (D-123): a function expression, or a function declaration or a const's function
// expression that it names; nil for any other argument.
func (c *pass) parallelCallback(argument *ast.Node) *ast.Node {
	fn := ast.SkipParentheses(argument)
	if fn.Kind != ast.KindArrowFunction && fn.Kind != ast.KindFunctionExpression && fn.Kind != ast.KindIdentifier {
		return nil
	}
	if !isParallelCallbackType(c.tc.GetContextualType(argument, checker.ContextFlagsNone)) {
		return nil
	}
	if fn.Kind != ast.KindIdentifier {
		return fn
	}
	symbol := c.tc.GetSymbolAtLocation(fn)
	if symbol == nil || symbol.ValueDeclaration == nil {
		return nil
	}
	declaration := symbol.ValueDeclaration
	switch {
	case declaration.Kind == ast.KindFunctionDeclaration && declaration.Body() != nil:
		return declaration
	case declaration.Kind == ast.KindVariableDeclaration && declaration.Initializer() != nil:
		if initializer := ast.SkipParentheses(declaration.Initializer()); initializer.Kind == ast.KindArrowFunction || initializer.Kind == ast.KindFunctionExpression {
			return initializer
		}
	}
	return nil
}

// isParallelCallbackType reports whether t is pts:parallel's ParallelCallback<F> (F &
// ParallelBrand), or a union with one (an optional parameter's).
func isParallelCallbackType(t *checker.Type) bool {
	switch {
	case t == nil:
		return false
	case t.Flags()&checker.TypeFlagsUnion != 0:
		return slices.ContainsFunc(t.Types(), isParallelCallbackType)
	case t.Flags()&checker.TypeFlagsIntersection != 0:
		return slices.ContainsFunc(t.Types(), func(member *checker.Type) bool {
			symbol := member.Symbol()
			return symbol != nil && symbol.Name == "ParallelBrand" && len(symbol.Declarations) > 0 &&
				ast.GetSourceFileOfNode(symbol.Declarations[0]).FileName() == lib.ModulePath(lib.ParallelModule)
		})
	}
	return false
}

// guardrail reports (D-L17) each assignment under root, a parallel body, to a variable
// declared outside boundary, the node that holds the body and what it declares.
func (c *pass) guardrail(root *ast.Node, boundary *ast.Node) {
	if c.guarded[root] {
		return
	}
	c.guarded[root] = true
	var visit func(node *ast.Node) bool
	visit = func(node *ast.Node) bool {
		switch node.Kind {
		case ast.KindBinaryExpression:
			if expr := node.AsBinaryExpression(); ast.IsAssignmentOperator(expr.OperatorToken.Kind) {
				c.assignedOutside(expr.Left, boundary)
			}
		case ast.KindPrefixUnaryExpression:
			if expr := node.AsPrefixUnaryExpression(); expr.Operator == ast.KindPlusPlusToken || expr.Operator == ast.KindMinusMinusToken {
				c.assignedOutside(expr.Operand, boundary)
			}
		case ast.KindPostfixUnaryExpression:
			c.assignedOutside(node.AsPostfixUnaryExpression().Operand, boundary)
		case ast.KindForOfStatement, ast.KindForInStatement:
			if initializer := node.Initializer(); initializer.Kind != ast.KindVariableDeclarationList {
				c.assignedOutside(initializer, boundary)
			}
		}
		return node.ForEachChild(visit)
	}
	visit(root)
}

// assignedOutside reports the variables an assignment's target (a name, or a pattern of
// names) assigns that are declared outside boundary: a `let` or a parameter of the code
// around the parallel body. Fields and elements may be assigned.
func (c *pass) assignedOutside(target *ast.Node, boundary *ast.Node) {
	target = ast.SkipParentheses(target)
	switch target.Kind {
	case ast.KindIdentifier:
		c.reportOuterVariable(target, c.tc.GetSymbolAtLocation(target), boundary)
	case ast.KindShorthandPropertyAssignment:
		c.reportOuterVariable(target.Name(), c.tc.GetShorthandAssignmentValueSymbol(target), boundary)
	case ast.KindPropertyAssignment:
		c.assignedOutside(target.Initializer(), boundary)
	case ast.KindSpreadElement, ast.KindSpreadAssignment:
		c.assignedOutside(target.Expression(), boundary)
	case ast.KindArrayLiteralExpression:
		for _, element := range target.AsArrayLiteralExpression().Elements.Nodes {
			c.assignedOutside(element, boundary)
		}
	case ast.KindObjectLiteralExpression:
		for _, property := range target.AsObjectLiteralExpression().Properties.Nodes {
			c.assignedOutside(property, boundary)
		}
	case ast.KindBinaryExpression: // a pattern's default: `[x = 1] = …`
		c.assignedOutside(target.AsBinaryExpression().Left, boundary)
	}
}

// reportOuterVariable reports name, an assignment's target, if its symbol is a variable
// or parameter declared outside boundary.
func (c *pass) reportOuterVariable(name *ast.Node, symbol *ast.Symbol, boundary *ast.Node) {
	if symbol == nil || symbol.ValueDeclaration == nil {
		return
	}
	declaration := symbol.ValueDeclaration
	if declaration.Kind != ast.KindVariableDeclaration && declaration.Kind != ast.KindParameter && declaration.Kind != ast.KindBindingElement {
		return
	}
	for n := declaration; n != nil; n = n.Parent {
		if n == boundary {
			return
		}
	}
	c.report(name, diagnostic.ParallelAssignment,
		"`%s` is declared outside this parallel body, which runs on several threads at once, so assigning it here would race; combine the results with `.par.reduce` or `.par.sum`, or use pts:sync: an `AtomicInt` for an integer count, a `Mutex` for anything else", name.Text())
}
