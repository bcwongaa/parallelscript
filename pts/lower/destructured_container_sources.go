package lower

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
)

// Binding elements share the variable's initializer, not their default value.
// This bounded syntactic lookup also serves the complete reference index; semantic
// eligibility and every sibling's pattern/use checks happen before publication.
func containerVariable(declaration *ast.Node) *ast.Node {
	binding := declaration.Kind == ast.KindBindingElement
	for depth := 0; declaration.Kind == ast.KindBindingElement; depth++ {
		if depth >= maxValueFlowDepth || declaration.Parent == nil || declaration.Parent.Kind != ast.KindObjectBindingPattern {
			return nil
		}
		declaration = declaration.Parent.Parent
		if declaration == nil {
			return nil
		}
	}
	if declaration.Kind != ast.KindVariableDeclaration || declaration.Parent == nil ||
		declaration.Parent.Flags&ast.NodeFlagsConst == 0 ||
		(!binding && declaration.Name().Kind != ast.KindIdentifier) ||
		(binding && declaration.Name().Kind != ast.KindObjectBindingPattern) {
		return nil
	}
	return declaration
}

// Every identifier leaf must select a literal-valued child container. Terminal
// destructuring (notably bound methods), defaults, rest and computed keys retain
// the erased path. A positive leaf cannot escape an unchecked sibling's failure.
func (l *lowerer) containerPatternUse(pattern *ast.Node, tree *literalContainer, contract *checker.Type, fresh bool, steps *int, depth int, check func(*ast.Node, *ast.Symbol, *literalContainer, int) bool) bool {
	if !containerStep(steps, &depth) || pattern.Kind != ast.KindObjectBindingPattern || tree.node.Kind != ast.KindObjectLiteralExpression ||
		(!fresh && (contract == nil || contract.Flags()&checker.TypeFlagsObject == 0)) {
		return false
	}
	for _, element := range pattern.AsBindingPattern().Elements.Nodes {
		memberDepth := depth
		if !containerStep(steps, &memberDepth) || element.Kind != ast.KindBindingElement ||
			element.Initializer() != nil || element.AsBindingElement().DotDotDotToken != nil {
			return false
		}
		name := element.Name()
		keyNode := element.AsBindingElement().PropertyName
		if keyNode == nil {
			keyNode = name
		}
		if keyNode.Kind != ast.KindIdentifier && keyNode.Kind != ast.KindStringLiteral {
			return false
		}
		key, ok := literalDataKey(keyNode, true)
		child := tree.children[key]
		if !ok || child == nil {
			return false
		}
		var source *checker.Type
		if !fresh {
			property := l.checker.GetPropertyOfType(contract, key)
			if property == nil {
				return false
			}
			source = l.checker.GetTypeOfSymbol(property)
		}
		if name.Kind == ast.KindObjectBindingPattern {
			if !l.containerPatternUse(name, child, source, fresh, steps, memberDepth, check) {
				return false
			}
			continue
		}
		alias, symbol := l.containerBinding(name)
		if alias != element {
			return false
		}
		target := l.checker.GetTypeOfSymbol(symbol)
		if fresh {
			if !l.nestedContainerTypes(child, target, steps, memberDepth) {
				return false
			}
		} else if !l.containerDataTypes(source, target, steps) {
			return false
		}
		if !check(alias, symbol, child, memberDepth) {
			return false
		}
	}
	return true
}
