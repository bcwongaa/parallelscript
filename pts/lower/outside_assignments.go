package lower

import (
	"github.com/microsoft/typescript-go/internal/ast"
)

// tsc keeps a variable narrowed across calls, so a closure that assigns it can change it
// behind a narrowed read (`if (typeof v === "string") { reset(); v.length }`). The lowering
// checks narrowed reads of such variables (D-140), as it checks narrowed properties.

// collectOutsideAssignments records the variables and parameters that a function other than
// the one declaring them assigns: their narrowed reads are checked. It also records every
// variable and parameter assigned at all, which a suspended function's reads need (D-145).
func (l *lowerer) collectOutsideAssignments(files []*ast.SourceFile) {
	l.assignedOutside = map[*ast.Symbol]bool{}
	l.assignedAnywhere = map[*ast.Symbol]bool{}
	var visit ast.Visitor
	visit = func(node *ast.Node) bool {
		for _, target := range assignmentTargets(node) {
			l.markAssignedOutside(target, functionOf(node))
		}
		node.ForEachChild(visit)
		return false
	}
	for _, file := range files {
		file.AsNode().ForEachChild(visit)
	}
}

// assignmentTargets are the targets node assigns: an assignment's left side, an
// increment's operand, or a for…of or for…in loop's target that isn't a declaration.
func assignmentTargets(node *ast.Node) []*ast.Node {
	switch node.Kind {
	case ast.KindBinaryExpression:
		if expr := node.AsBinaryExpression(); ast.IsAssignmentOperator(expr.OperatorToken.Kind) {
			return []*ast.Node{expr.Left}
		}
	case ast.KindPrefixUnaryExpression:
		if expr := node.AsPrefixUnaryExpression(); expr.Operator == ast.KindPlusPlusToken || expr.Operator == ast.KindMinusMinusToken {
			return []*ast.Node{expr.Operand}
		}
	case ast.KindPostfixUnaryExpression:
		return []*ast.Node{node.AsPostfixUnaryExpression().Operand}
	case ast.KindForOfStatement, ast.KindForInStatement:
		if initializer := node.Initializer(); initializer.Kind != ast.KindVariableDeclarationList {
			return []*ast.Node{initializer}
		}
	}
	return nil
}

// markAssignedOutside marks each variable a target (a name, or a pattern of names) assigns
// if it is declared outside fn, the function the assignment is in.
func (l *lowerer) markAssignedOutside(target *ast.Node, fn *ast.Node) {
	target = ast.SkipParentheses(target)
	switch target.Kind {
	case ast.KindIdentifier:
		l.markIfOutside(l.checker.GetSymbolAtLocation(target), fn)
	case ast.KindShorthandPropertyAssignment:
		l.markIfOutside(l.checker.GetShorthandAssignmentValueSymbol(target), fn)
	case ast.KindPropertyAssignment:
		l.markAssignedOutside(target.Initializer(), fn)
	case ast.KindSpreadElement, ast.KindSpreadAssignment,
		ast.KindAsExpression, ast.KindTypeAssertionExpression, ast.KindNonNullExpression, ast.KindSatisfiesExpression:
		l.markAssignedOutside(target.Expression(), fn)
	case ast.KindArrayLiteralExpression:
		for _, element := range target.AsArrayLiteralExpression().Elements.Nodes {
			l.markAssignedOutside(element, fn)
		}
	case ast.KindObjectLiteralExpression:
		for _, property := range target.AsObjectLiteralExpression().Properties.Nodes {
			l.markAssignedOutside(property, fn)
		}
	case ast.KindBinaryExpression: // a pattern's default: `[x = 1] = …`
		l.markAssignedOutside(target.AsBinaryExpression().Left, fn)
	}
}

func (l *lowerer) markIfOutside(symbol *ast.Symbol, fn *ast.Node) {
	if symbol == nil || symbol.ValueDeclaration == nil {
		return
	}
	switch symbol.ValueDeclaration.Kind {
	case ast.KindVariableDeclaration, ast.KindParameter, ast.KindBindingElement:
		l.assignedAnywhere[symbol] = true
		if functionOf(symbol.ValueDeclaration) != fn {
			l.assignedOutside[symbol] = true
		}
	}
}

// functionOf is the function whose body node is in (a parameter's own function), or the
// source file for module-scope code; class static blocks and field initializers count as
// functions, since they run apart from the code around them.
func functionOf(node *ast.Node) *ast.Node {
	for n := node.Parent; n != nil; n = n.Parent {
		if ast.IsFunctionLike(n) || n.Kind == ast.KindClassStaticBlockDeclaration || n.Kind == ast.KindPropertyDeclaration || n.Kind == ast.KindSourceFile {
			return n
		}
	}
	return nil
}
