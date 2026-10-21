package lower

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/pts/ir"
)

func (l *lowerer) receiverSources(node *ast.Node, steps *int, depth int) functionSources {
	return (sourceProof{l: l, receivers: true}).valueSources(node, steps, depth)
}

// receiverMayReach refines the type-only receiver filter without changing its cache key.
// A constructed subclass can call its ancestors' implementations, including entries that
// block ancestor lookup at an override. A known construction cannot be an object literal.
func (l *lowerer) receiverMayReach(d *demand, class *ir.Class) bool {
	if d.receiverSource == nil {
		return true
	}
	steps := maxValueFlowSteps
	sources := l.receiverSources(d.receiverSource, &steps, 0)
	if sources == nil {
		return true
	}
	if class == nil {
		return false // literal methods and function-as-method forwarders
	}
	for source := range sources {
		exact := l.classes[l.declaredSymbol(source)]
		if exact == nil || exact.IsSubclassOf(class) {
			return true // an unavailable class mapping is not a proof of exclusion
		}
	}
	return false
}

// constructedReceiver proves actual construction, never the expression's static type.
// Constructor aliases, mutable/generic classes and replacement returns remain unknown.
func (l *lowerer) constructedReceiver(node *ast.Node, steps *int, depth int) functionSources {
	if !l.sourceOutsideGenerics(node, steps) {
		return nil
	}
	declaration := l.constructedClass(node.Expression())
	if declaration == nil {
		return nil
	}
	for ancestor := declaration; ancestor != nil; depth++ {
		if depth >= maxValueFlowDepth || *steps == 0 {
			return nil
		}
		*steps--
		for _, member := range ancestor.Members() {
			if *steps == 0 {
				return nil
			}
			*steps--
			if member.Kind == ast.KindConstructor && member.Body() != nil && !constructorKeepsReceiver(member.Body(), steps, depth+1) {
				return nil
			}
		}
		heritage := ast.GetExtendsHeritageClauseElement(ancestor)
		if heritage == nil {
			return functionSources{declaration: true}
		}
		ancestor = l.constructedClass(heritage.Expression())
		if ancestor == nil {
			return nil
		}
	}
	return nil
}

func (l *lowerer) constructedClass(expression *ast.Node) *ast.Node {
	expression = ast.SkipParentheses(expression)
	if expression.Kind != ast.KindIdentifier {
		return nil
	}
	symbol := l.target(expression)
	if symbol == nil || len(symbol.Declarations) != 1 || symbol.ValueDeclaration == nil ||
		symbol.ValueDeclaration.Kind != ast.KindClassDeclaration || len(symbol.ValueDeclaration.TypeParameters()) != 0 || l.functionValueUses(symbol).written {
		return nil
	}
	return symbol.ValueDeclaration
}

// Even return this is deliberately unknown. Nested callable returns do not replace the
// constructed object, but all returns in its own body (including unreachable ones) count.
func constructorKeepsReceiver(node *ast.Node, steps *int, depth int) bool {
	if depth >= maxValueFlowDepth || *steps == 0 {
		return false
	}
	*steps--
	if ast.IsFunctionLike(node) || ast.IsClassLike(node) {
		return true
	}
	if node.Kind == ast.KindReturnStatement && node.Expression() != nil {
		return false
	}
	return !node.ForEachChild(func(child *ast.Node) bool { return !constructorKeepsReceiver(child, steps, depth+1) })
}
