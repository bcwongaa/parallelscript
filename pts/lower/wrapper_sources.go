package lower

import "github.com/microsoft/typescript-go/internal/ast"

// wrapperSource names a creation site only after boundMethod actually makes its wrapper.
// A property read is not a receiver proof: the site represents every object it can bind.
// For bind it is the whole call, not the .bind member. Other/synthetic reads stay unknown.
func (l *lowerer) wrapperSource(node *ast.Node) *ast.Node {
	node = ast.SkipParentheses(node)
	if node.Kind != ast.KindPropertyAccessExpression {
		return nil
	}
	if l.isFunctionMethod(node) && node.Name().Text() == "bind" {
		callee := node
		for depth := 0; callee.Parent != nil && callee.Parent.Kind == ast.KindParenthesizedExpression; depth++ {
			if depth == maxValueFlowDepth {
				return nil
			}
			callee = callee.Parent
		}
		if callee.Parent == nil || callee.Parent.Kind != ast.KindCallExpression || callee.Parent.Expression() != callee {
			return nil
		}
		node = callee.Parent
	}
	steps := maxValueFlowSteps
	if l.sourceOutsideGenerics(node, &steps) {
		return node
	}
	return nil
}

// A shared generic AST can lower differently in different instances. Creation proofs
// exclude it (D-205/D-206), including contextually generic functions with no syntax params.
func (l *lowerer) sourceOutsideGenerics(node *ast.Node, steps *int) bool {
	for parent, depth := node.Parent, 0; parent != nil && depth < maxValueFlowDepth; parent, depth = parent.Parent, depth+1 {
		if *steps == 0 {
			return false
		}
		*steps--
		if ast.IsClassLike(parent) && len(parent.TypeParameters()) != 0 ||
			ast.IsFunctionLike(parent) && len(l.checker.GetSignatureFromDeclaration(parent).TypeParameters()) != 0 {
			return false
		}
		if parent.Kind == ast.KindSourceFile {
			return true
		}
	}
	return false
}
