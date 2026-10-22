package lower

import (
	"strconv"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
)

// Binding elements share the variable's initializer, not their default value.
// This bounded syntactic lookup also serves the complete reference index; semantic
// eligibility and every sibling's pattern/use checks happen before publication.
func containerVariable(declaration *ast.Node) *ast.Node {
	binding := declaration.Kind == ast.KindBindingElement
	for depth := 0; declaration.Kind == ast.KindBindingElement; depth++ {
		if depth >= maxValueFlowDepth || declaration.Parent == nil || !ast.IsBindingPattern(declaration.Parent) {
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
		(binding && !ast.IsBindingPattern(declaration.Name())) {
		return nil
	}
	return declaration
}

// Every identifier leaf must select a literal-valued child container. Terminal
// destructuring (notably bound methods), defaults, rest and computed keys retain
// the erased path. A positive leaf cannot escape an unchecked sibling's failure.
func (l *lowerer) containerPatternUse(pattern *ast.Node, tree *literalContainer, contract *checker.Type, fresh bool, steps *int, depth int, check func(*ast.Node, *ast.Symbol, *literalContainer, int) bool) bool {
	if !containerStep(steps, &depth) || !ast.IsBindingPattern(pattern) ||
		(!fresh && (contract == nil || contract.Flags()&checker.TypeFlagsObject == 0)) {
		return false
	}
	array := pattern.Kind == ast.KindArrayBindingPattern
	if array {
		// Only ordinary-array contracts passed the whole literal-tree check. A
		// binding's apparent tuple or narrowed type must not replace that contract.
		if tree.node.Kind != ast.KindArrayLiteralExpression || !fresh && !l.checker.IsArrayType(contract) {
			return false
		}
	} else if tree.node.Kind != ast.KindObjectLiteralExpression {
		return false
	}
	for index, element := range pattern.AsBindingPattern().Elements.Nodes {
		memberDepth := depth
		if !containerStep(steps, &memberDepth) || element.Kind != ast.KindBindingElement || element.Name() == nil ||
			element.Initializer() != nil || element.AsBindingElement().DotDotDotToken != nil {
			return false
		}
		name := element.Name()
		key := strconv.Itoa(index)
		if !array {
			keyNode := element.AsBindingElement().PropertyName
			if keyNode == nil {
				keyNode = name
			}
			if keyNode.Kind != ast.KindIdentifier && keyNode.Kind != ast.KindStringLiteral {
				return false
			}
			var ok bool
			key, ok = literalDataKey(keyNode, true)
			if !ok {
				return false
			}
		}
		child := tree.children[key]
		if child == nil {
			return false
		}
		var source *checker.Type
		if !fresh {
			if array {
				source = l.checker.GetElementTypeOfArrayType(contract)
			} else {
				property := l.checker.GetPropertyOfType(contract, key)
				if property == nil {
					return false
				}
				source = l.checker.GetTypeOfSymbol(property)
			}
		}
		if ast.IsBindingPattern(name) {
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
