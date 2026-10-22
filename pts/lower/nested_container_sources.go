package lower

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
)

// A tree is published only after every literal and root-component use passes.
// It contains declaration-shared contracts, never instantiated source summaries.
type literalContainer struct {
	node     *ast.Node
	members  map[string]*ast.Node
	children map[string]*literalContainer
}

func (l *lowerer) containerTree(node *ast.Node, steps *int, depth int) *literalContainer {
	tree := literalContainerTree(node, steps, depth)
	if tree != nil && len(tree.children) != 0 &&
		(!l.sourceOutsideGenerics(node, steps) || !l.nestedContainerTypes(tree, nil, steps, depth)) {
		return nil
	}
	return tree
}

func literalContainerTree(node *ast.Node, steps *int, depth int) *literalContainer {
	members := literalContainerMembers(node, steps, depth)
	if members == nil {
		return nil
	}
	tree := &literalContainer{node: node, members: members, children: map[string]*literalContainer{}}
	for key, value := range members {
		memberDepth := depth
		for value.Kind == ast.KindParenthesizedExpression {
			if !containerStep(steps, &memberDepth) {
				return nil
			}
			value = value.Expression()
		}
		wrapped := value
		for wrapped.Kind == ast.KindAsExpression || wrapped.Kind == ast.KindTypeAssertionExpression ||
			wrapped.Kind == ast.KindNonNullExpression || wrapped.Kind == ast.KindSatisfiesExpression ||
			wrapped.Kind == ast.KindParenthesizedExpression {
			if !containerStep(steps, &memberDepth) {
				return nil
			}
			wrapped = wrapped.Expression()
		}
		if wrapped != value && (wrapped.Kind == ast.KindObjectLiteralExpression || wrapped.Kind == ast.KindArrayLiteralExpression) {
			return nil // an assertion/context wrapper can change a nested representation
		}
		if value.Kind == ast.KindObjectLiteralExpression || value.Kind == ast.KindArrayLiteralExpression {
			child := literalContainerTree(value, steps, memberDepth+1)
			if child == nil {
				return nil
			}
			tree.children[key] = child
		}
	}
	return tree
}

// Only terminal values may escape. Named closed components can additionally
// check immutable aliases of literal-valued prefixes, including their entire uses.
func (tree *literalContainer) projectionRead(read *ast.Node, steps *int, depth int) bool {
	return tree.projectionUse(read, steps, depth, nil)
}

func (tree *literalContainer) projectionUse(read *ast.Node, steps *int, depth int, alias func(*literalContainer, *ast.Node, int) bool) bool {
	for containerStep(steps, &depth) {
		for read.Parent != nil && read.Parent.Kind == ast.KindParenthesizedExpression {
			if !containerStep(steps, &depth) {
				return false
			}
			read = read.Parent
		}
		parent := read.Parent
		if alias != nil && parent != nil && parent.Kind == ast.KindVariableDeclaration && parent.Initializer() == read {
			return alias(tree, read, depth)
		}
		if parent == nil || !ast.IsAccessExpression(parent) || parent.Expression() != read {
			return false
		}
		key, ok := literalProjectionKey(parent)
		if !ok || tree.members[key] == nil {
			return false
		}
		child := tree.children[key]
		if child == nil {
			return containerProjectionRead(parent, steps, depth)
		}
		tree, read = child, parent
	}
	return false
}

// Nested proofs initially cover inferred, uncontextualized trees only. Contextual
// methods can replace a closure with an adapter; array common types can change a
// child's layout. Raw checker contracts keep this shared cache independent of subst.
func (l *lowerer) nestedContainerTypes(tree *literalContainer, target *checker.Type, steps *int, depth int) bool {
	// Inferred binding-pattern pseudo-context is not a declared conversion. Actual
	// annotations/default parameter contexts still apply with SkipBindingPatterns.
	if !containerStep(steps, &depth) || l.checker.GetContextualType(tree.node, checker.ContextFlagsSkipBindingPatterns) != nil {
		return false
	}
	own := l.checker.GetWidenedType(l.checker.GetTypeAtLocation(tree.node))
	if target == nil {
		target = own
	}
	if own.Flags()&checker.TypeFlagsObject == 0 || target.Flags()&checker.TypeFlagsObject == 0 {
		return false
	}
	if tree.node.Kind == ast.KindArrayLiteralExpression {
		if !l.checker.IsArrayType(own) || !l.checker.IsArrayType(target) || own.Target() != target.Target() {
			return false
		}
		element := l.checker.GetElementTypeOfArrayType(target)
		ownElement := l.checker.GetElementTypeOfArrayType(own)
		if element == nil || ownElement == nil ||
			(element.Flags()|ownElement.Flags())&(checker.TypeFlagsAny|checker.TypeFlagsUnknown|checker.TypeFlagsUnion|checker.TypeFlagsIntersection) != 0 {
			return false
		}
		for key, value := range tree.members {
			memberDepth := depth
			if !containerStep(steps, &memberDepth) {
				return false
			}
			if child := tree.children[key]; child != nil {
				if !l.nestedContainerTypes(child, element, steps, memberDepth) {
					return false
				}
			} else if t := l.checker.GetWidenedType(l.checker.GetTypeAtLocation(value)); t != element && t.Flags()&checker.TypeFlagsPrimitive == 0 {
				return false
			}
		}
		return true
	}
	if l.checker.IsArrayLikeType(own) || l.checker.IsArrayLikeType(target) ||
		l.checker.TypeHasCallOrConstructSignatures(own) || l.checker.TypeHasCallOrConstructSignatures(target) ||
		len(l.checker.GetIndexInfosOfType(own)) != 0 || len(l.checker.GetIndexInfosOfType(target)) != 0 {
		return false
	}
	properties := l.checker.GetPropertiesOfType(own)
	if len(properties) != len(tree.members) || len(properties) != len(l.checker.GetPropertiesOfType(target)) {
		return false
	}
	for _, property := range properties {
		memberDepth := depth
		if !containerStep(steps, &memberDepth) {
			return false
		}
		other := l.checker.GetPropertyOfType(target, property.Name)
		if other == nil || property.Flags&ast.SymbolFlagsProperty == 0 || other.Flags&ast.SymbolFlagsProperty == 0 ||
			(property.Flags|other.Flags)&(ast.SymbolFlagsMethod|ast.SymbolFlagsAccessor|ast.SymbolFlagsOptional) != 0 {
			return false
		}
		contract := l.checker.GetTypeOfSymbol(other)
		if child := tree.children[property.Name]; child != nil {
			// Freshness is normalized only along this finite literal syntax tree,
			// never by recursively comparing arbitrary structural checker types.
			if !l.nestedContainerTypes(child, contract, steps, memberDepth) {
				return false
			}
		} else if contract != l.checker.GetTypeOfSymbol(property) || contract.Flags()&(checker.TypeFlagsAny|checker.TypeFlagsUnknown) != 0 {
			return false
		}
	}
	return true
}
