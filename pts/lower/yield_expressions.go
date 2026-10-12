package lower

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/pts/ir"
)

// Yields inside larger expressions (D-111). A statement whose expression holds a yield
// that isn't first in it (statementYield) is lowered in two parts: statements that
// evaluate, in JS's order, each of its yields and each operand JS evaluates before one,
// keeping their values in locals (which live in the generator's frame, D-109); then the
// statement itself, whose lowering finds those values in place of the expressions
// (l.yielded, l.kept). An operand without side effects (a literal, a name, a property or
// element of one, a function expression) isn't kept but evaluated where it is, after the
// yields: only a change to it while the generator is suspended would show. A yield in a
// branch of `?:` or in the right operand of `&&`, `||` or `??` makes the expression an if
// statement that assigns a local. A yield anywhere else (a loop's condition or update, a
// case label, a later declaration of a variable statement, a default value, an optional
// chain) is reported where it is lowered.

// hoistableExpression is the expression of a statement that runs once, before the rest
// of it, so that the yields in it can run before the statement: an expression statement's,
// the first variable's initializer, a return's or throw's value, an if's or switch's
// condition, or a for…of's or for…in's iterated value; nil for any other statement.
func hoistableExpression(node *ast.Node) *ast.Node {
	switch node.Kind {
	case ast.KindExpressionStatement, ast.KindReturnStatement, ast.KindThrowStatement, ast.KindIfStatement,
		ast.KindSwitchStatement, ast.KindForOfStatement, ast.KindForInStatement:
		return node.Expression()
	case ast.KindVariableStatement:
		return node.AsVariableStatement().DeclarationList.AsVariableDeclarationList().Declarations.Nodes[0].Initializer()
	}
	return nil
}

// hasYield reports whether node holds a yield of the function being lowered that hasn't
// been lowered yet: one outside nested functions and classes.
func (l *lowerer) hasYield(node *ast.Node) bool {
	switch {
	case node == nil || ast.IsFunctionLike(node) || ast.IsClassLike(node):
		return false
	case node.Kind == ast.KindYieldExpression && l.yielded[node] == nil:
		return true
	}
	return node.ForEachChild(l.hasYield)
}

// hoistingStatement lowers a statement whose expression holds yields that aren't first
// in it: the statements that hoist them (hoistYields), then the statement.
func (l *lowerer) hoistingStatement(node *ast.Node, expression *ast.Node) []ir.Stmt {
	l.hoisting[node] = true
	mark := len(l.hoistedNodes)
	defer func() {
		delete(l.hoisting, node)
		for _, hoisted := range l.hoistedNodes[mark:] {
			delete(l.yielded, hoisted)
			delete(l.kept, hoisted)
		}
		l.hoistedNodes = l.hoistedNodes[:mark]
	}()
	hoisted, ok := l.hoistYields(expression)
	if !ok {
		return nil
	}
	if node.Kind == ast.KindExpressionStatement && ast.SkipParentheses(expression).Kind == ast.KindYieldExpression {
		return hoisted // `yield (yield x);`: its value is unused
	}
	return append(hoisted, l.statement(node)...)
}

// hoistYields lowers the yields in node, and the operands JS evaluates before each, to
// statements that keep their values for node's lowering; ok is false if it failed, which
// was reported. A yield it can't hoist is left for its lowering to report.
func (l *lowerer) hoistYields(node *ast.Node) (stmts []ir.Stmt, ok bool) {
	switch {
	case !l.hasYield(node):
		return nil, true
	case node.Kind == ast.KindYieldExpression:
		return l.hoistYield(node)
	case node.Kind == ast.KindConditionalExpression:
		return l.hoistConditional(node)
	case ast.IsLogicalOrCoalescingBinaryExpression(node) && l.hasYield(node.AsBinaryExpression().Right):
		return l.hoistShortCircuit(node)
	}
	operands := evaluationOrder(node)
	last := -1
	for i, operand := range operands {
		if l.hasYield(operand) {
			last = i
		}
	}
	for i, operand := range operands[:last+1] {
		inner, ok := l.hoistYields(operand)
		if !ok {
			return nil, false
		}
		stmts = append(stmts, inner...)
		if i < last && !l.isSettled(operand) {
			kept, ok := l.keep(operand)
			if !ok {
				return nil, false
			}
			stmts = append(stmts, kept)
		}
	}
	return stmts, true
}

// hoistYield lowers a yield (after the yields in its operand) to the statements that
// yield, and keeps what it evaluates to once the generator resumes in a local.
func (l *lowerer) hoistYield(node *ast.Node) ([]ir.Stmt, bool) {
	stmts, ok := l.hoistYields(node.Expression())
	if !ok {
		return nil, false
	}
	yielded, value := l.yieldPoint(node)
	if yielded == nil {
		return nil, false
	}
	resumed := l.newLocal("resumed", value.Type())
	l.hoisted(node)
	l.yielded[node] = &ir.LocalRef{Local: resumed}
	return append(append(stmts, yielded...), &ir.LocalDecl{At: l.pos(node), Local: resumed, Init: value}), true
}

// hoistConditional lowers `c ? a : b` whose branches hold yields to an if statement that
// assigns the branch it takes to a local, the conditional's value.
func (l *lowerer) hoistConditional(node *ast.Node) ([]ir.Stmt, bool) {
	expr := node.AsConditionalExpression()
	stmts, ok := l.hoistYields(expr.Condition)
	if !ok {
		return nil, false
	}
	t := l.irType(l.typeAt(node), node)
	cond := l.condition(expr.Condition)
	if t == nil || cond == nil {
		return nil, false
	}
	chosen := l.newLocal("chosen", t)
	then, thenOK := l.hoistedBranch(expr.WhenTrue, chosen)
	otherwise, otherwiseOK := l.hoistedBranch(expr.WhenFalse, chosen)
	if !thenOK || !otherwiseOK {
		return nil, false
	}
	at := l.pos(node)
	l.keepAs(node, chosen)
	return append(stmts, &ir.LocalDecl{At: at, Local: chosen}, &ir.If{At: at, Cond: cond, Then: then, Else: otherwise}), true
}

// hoistShortCircuit lowers `a && b`, `a || b` or `a ?? b` whose right operand holds
// yields to a, kept in a local, and an if statement whose branches give the expression's
// value: b, evaluated where JS does, or a.
func (l *lowerer) hoistShortCircuit(node *ast.Node) ([]ir.Stmt, bool) {
	expr := node.AsBinaryExpression()
	stmts, ok := l.hoistYields(expr.Left)
	if !ok {
		return nil, false
	}
	keptLeft, ok := l.keep(expr.Left)
	if !ok {
		return nil, false
	}
	t := l.irType(l.typeAt(node), node)
	if t == nil {
		return nil, false
	}
	left := l.kept[expr.Left]
	var test ir.Expr
	switch expr.OperatorToken.Kind {
	case ast.KindAmpersandAmpersandToken:
		test = l.condition(expr.Left)
	case ast.KindBarBarToken:
		if test = l.condition(expr.Left); test != nil {
			test = &ir.Unary{Op: ir.Not, X: test}
		}
	default: // ??
		test = &ir.BoolConst{Value: false} // never null or undefined
		if isDynamic(left.Type()) {
			test = &ir.IsNullish{X: left}
		}
	}
	chosen := l.newLocal("chosen", t)
	then, ok := l.hoistedBranch(expr.Right, chosen)
	if test == nil || !ok {
		return nil, false
	}
	var result ir.Expr
	if isDynamic(left.Type()) && !isDynamic(t) {
		result = fromUnion(left, t) // the test found it to be of the result's type: `name || "?"` is a string
	} else if result = l.convert(left, t, expr.Left); result == nil {
		return nil, false
	}
	at := l.pos(node)
	otherwise := &ir.Block{At: at, Stmts: []ir.Stmt{&ir.ExprStmt{At: at, X: &ir.Assign{Local: chosen, Value: result}}}}
	l.keepAs(node, chosen)
	return append(stmts, keptLeft, &ir.LocalDecl{At: at, Local: chosen}, &ir.If{At: at, Cond: test, Then: then, Else: otherwise}), true
}

// hoistedBranch is a block that evaluates node, its yields included, into the local chosen.
func (l *lowerer) hoistedBranch(node *ast.Node, chosen *ir.Local) (*ir.Block, bool) {
	stmts, ok := l.hoistYields(node)
	if !ok {
		return nil, false
	}
	x := l.expression(node)
	if x == nil {
		return nil, false
	}
	if x = l.convert(x, chosen.Type, node); x == nil {
		return nil, false
	}
	at := l.pos(node)
	return &ir.Block{At: at, Stmts: append(stmts, &ir.ExprStmt{At: at, X: &ir.Assign{Local: chosen, Value: x}})}, true
}

// keep evaluates an operand into a local that its lowering then reads: a spread's
// iterated value, or the operand itself (a call that returns nothing is run, and is
// undefined).
func (l *lowerer) keep(node *ast.Node) (ir.Stmt, bool) {
	if node.Kind == ast.KindSpreadElement {
		node = node.Expression()
	}
	x := l.expression(node)
	if x == nil {
		return nil, false
	}
	at := l.pos(node)
	if x.Type() == ir.Void {
		l.hoisted(node)
		l.kept[node] = &ir.UndefinedConst{}
		return &ir.ExprStmt{At: at, X: x}, true
	}
	local := l.newLocal("kept", x.Type())
	l.keepAs(node, local)
	return &ir.LocalDecl{At: at, Local: local, Init: x}, true
}

// keepAs makes node's lowering read local.
func (l *lowerer) keepAs(node *ast.Node, local *ir.Local) {
	l.hoisted(node)
	l.kept[node] = &ir.LocalRef{Local: local}
}

// hoisted records a node whose value hoistYields keeps, to forget once its statement is lowered.
func (l *lowerer) hoisted(node *ast.Node) {
	l.hoistedNodes = append(l.hoistedNodes, node)
}

// isSettled reports whether an operand evaluated after the yields that follow it gives
// what it gives before them: it has no side effects, or its value is kept.
func (l *lowerer) isSettled(node *ast.Node) bool {
	if l.kept[node] != nil {
		return true
	}
	switch node.Kind {
	case ast.KindYieldExpression:
		return l.yielded[node] != nil
	case ast.KindIdentifier, ast.KindThisKeyword, ast.KindSuperKeyword, ast.KindNumericLiteral, ast.KindBigIntLiteral,
		ast.KindStringLiteral, ast.KindNoSubstitutionTemplateLiteral, ast.KindTrueKeyword, ast.KindFalseKeyword,
		ast.KindNullKeyword, ast.KindRegularExpressionLiteral, ast.KindArrowFunction, ast.KindFunctionExpression:
		return true
	case ast.KindParenthesizedExpression, ast.KindAsExpression, ast.KindSatisfiesExpression, ast.KindNonNullExpression,
		ast.KindTypeAssertionExpression, ast.KindTypeOfExpression, ast.KindVoidExpression, ast.KindSpreadElement:
		return l.isSettled(node.Expression())
	case ast.KindPropertyAccessExpression:
		return !ast.IsOptionalChain(node) && l.isSettled(node.Expression())
	case ast.KindPrefixUnaryExpression:
		expr := node.AsPrefixUnaryExpression()
		return expr.Operator != ast.KindPlusPlusToken && expr.Operator != ast.KindMinusMinusToken && l.isSettled(expr.Operand)
	case ast.KindBinaryExpression:
		expr := node.AsBinaryExpression()
		return !ast.IsAssignmentOperator(expr.OperatorToken.Kind) && l.isSettled(expr.Left) && l.isSettled(expr.Right)
	}
	operands := evaluationOrder(node)
	if operands == nil || node.Kind == ast.KindCallExpression || node.Kind == ast.KindNewExpression {
		return false
	}
	for _, operand := range operands {
		if !l.isSettled(operand) {
			return false
		}
	}
	return true // an element of settled operands, an array or object literal or template of them
}

// evaluationOrder is the operands of an expression in the order JS evaluates them, where
// the yields in them can be hoisted; nil for an expression whose yields can't be.
func evaluationOrder(node *ast.Node) []*ast.Node {
	switch node.Kind {
	case ast.KindParenthesizedExpression, ast.KindAsExpression, ast.KindSatisfiesExpression, ast.KindNonNullExpression,
		ast.KindTypeAssertionExpression, ast.KindTypeOfExpression, ast.KindVoidExpression, ast.KindAwaitExpression,
		ast.KindDeleteExpression, ast.KindSpreadElement:
		return []*ast.Node{node.Expression()}
	case ast.KindPrefixUnaryExpression:
		return []*ast.Node{node.AsPrefixUnaryExpression().Operand}
	case ast.KindPostfixUnaryExpression:
		return []*ast.Node{node.AsPostfixUnaryExpression().Operand}
	case ast.KindBinaryExpression:
		expr := node.AsBinaryExpression()
		if ast.IsAssignmentOperator(expr.OperatorToken.Kind) {
			return append(referenceOperands(expr.Left), expr.Right)
		}
		return []*ast.Node{expr.Left, expr.Right}
	case ast.KindCallExpression:
		if ast.IsOptionalChain(node) {
			return nil
		}
		call := node.AsCallExpression()
		return append(referenceOperands(call.Expression), call.Arguments.Nodes...)
	case ast.KindNewExpression:
		if args := node.AsNewExpression().Arguments; args != nil {
			return args.Nodes
		}
	case ast.KindPropertyAccessExpression:
		if !ast.IsOptionalChain(node) {
			return []*ast.Node{node.Expression()}
		}
	case ast.KindElementAccessExpression:
		if !ast.IsOptionalChain(node) {
			return []*ast.Node{node.Expression(), node.AsElementAccessExpression().ArgumentExpression}
		}
	case ast.KindArrayLiteralExpression:
		return node.AsArrayLiteralExpression().Elements.Nodes
	case ast.KindObjectLiteralExpression:
		var values []*ast.Node
		for _, property := range node.AsObjectLiteralExpression().Properties.Nodes {
			switch property.Kind {
			case ast.KindPropertyAssignment:
				values = append(values, property.Initializer())
			case ast.KindSpreadAssignment:
				values = append(values, property.Expression())
			}
		}
		return values
	case ast.KindTemplateExpression:
		var values []*ast.Node
		for _, span := range node.AsTemplateExpression().TemplateSpans.Nodes {
			values = append(values, span.Expression())
		}
		return values
	}
	return nil
}

// referenceOperands are the operands of a callee or an assignment's target that JS
// evaluates before the arguments or the right side: a property's object, an element's
// object and index, or a callee of another kind itself (`make()(x)`).
func referenceOperands(node *ast.Node) []*ast.Node {
	switch node = ast.SkipParentheses(node); node.Kind {
	case ast.KindPropertyAccessExpression:
		return []*ast.Node{node.Expression()}
	case ast.KindElementAccessExpression:
		return []*ast.Node{node.Expression(), node.AsElementAccessExpression().ArgumentExpression}
	case ast.KindIdentifier, ast.KindThisKeyword, ast.KindSuperKeyword, ast.KindArrayLiteralExpression, ast.KindObjectLiteralExpression:
		return nil // a binding, or a destructuring pattern, assigned after the right side
	}
	return []*ast.Node{node}
}
