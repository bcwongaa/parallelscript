package lower

import (
	"strconv"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
)

type containerUses struct {
	reads    []*ast.Node
	overflow bool
}

// projectionSources selects data, not a property's declared type. A const binding
// alone proves nothing about its object: every reference must be a supported read.
func (p sourceProof) projectionSources(node *ast.Node, steps *int, depth int) functionSources {
	l := p.l
	if l.isDynamicSource(node) || l.storedDynamic(node) {
		return nil
	}
	var path []*ast.Node
	container := node
	for {
		if !containerStep(steps, &depth) {
			return nil
		}
		if container.Kind == ast.KindParenthesizedExpression {
			container = container.Expression()
			continue
		}
		if !ast.IsAccessExpression(container) {
			break
		}
		path = append(path, container)
		container = container.Expression()
	}
	var tree *literalContainer
	if container.Kind == ast.KindIdentifier {
		declaration, symbol := l.containerBinding(container)
		if declaration == nil {
			return nil
		}
		tree = l.closedContainer(declaration, symbol, steps, depth)
	} else {
		tree = l.containerTree(container, steps, depth)
		if tree != nil && !tree.projectionRead(container, steps, depth) {
			return nil
		}
	}
	for i := len(path) - 1; i >= 0; i-- {
		if tree == nil || !containerStep(steps, &depth) {
			return nil
		}
		key, ok := literalProjectionKey(path[i])
		selected := tree.members[key]
		if !ok || selected == nil {
			return nil
		}
		if i == 0 {
			if tree.children[key] != nil || !containerProjectionRead(node, steps, depth) ||
				l.isDynamicSource(selected) || l.storedDynamic(selected) {
				return nil
			}
			return p.valueSources(selected, steps, depth+1)
		}
		tree = tree.children[key]
	}
	return nil
}

func (l *lowerer) containerBinding(node *ast.Node) (*ast.Node, *ast.Symbol) {
	if node == nil || node.Kind != ast.KindIdentifier {
		return nil, nil
	}
	symbol := l.readSymbol(node)
	if symbol == nil || len(symbol.Declarations) != 1 || symbol.ValueDeclaration == nil {
		return nil, nil
	}
	declaration := symbol.ValueDeclaration
	if declaration.Kind != ast.KindVariableDeclaration || declaration.Name().Kind != ast.KindIdentifier ||
		declaration.Parent.Flags&ast.NodeFlagsConst == 0 || l.assignedAnywhere[symbol] ||
		ast.GetCombinedModifierFlags(declaration)&ast.ModifierFlagsExport != 0 ||
		l.isDynamicSource(node) || l.storedDynamic(node) {
		return nil, nil
	}
	return declaration, symbol
}

func (l *lowerer) closedContainer(declaration *ast.Node, symbol *ast.Symbol, steps *int, depth int) (members *literalContainer) {
	flow := &l.valueFlow
	if flow.containers == nil {
		flow.containers = map[*ast.Node]*literalContainer{}
	}
	if known, ok := flow.containers[declaration]; ok {
		return known
	}
	// Resolve backwards to one literal, then check the complete forward component.
	// Publish nothing positive until unsafe siblings and every transitive use pass.
	requested := declaration
	component := map[*ast.Node]*literalContainer{}
	complete := false
	defer func() {
		for alias, tree := range component {
			if !complete {
				tree = nil
			}
			flow.containers[alias] = tree
		}
	}()
	var literal *ast.Node
	for {
		if _, seen := component[declaration]; seen {
			return nil
		}
		component[declaration] = nil
		if !containerStep(steps, &depth) {
			return nil
		}
		literal = declaration.Initializer()
		projected := false
		for literal != nil {
			if literal.Kind == ast.KindParenthesizedExpression {
				if !containerStep(steps, &depth) {
					return nil
				}
				literal = literal.Expression()
			} else if ast.IsAccessExpression(literal) {
				if _, ok := literalProjectionKey(literal); !ok || !containerStep(steps, &depth) {
					return nil
				}
				projected = true
				literal = literal.Expression()
			} else {
				break
			}
		}
		if literal == nil || literal.Kind != ast.KindIdentifier {
			if projected {
				return nil // extraction roots must be named immutable literal bindings
			}
			break
		}
		origin, source := l.containerBinding(literal)
		if origin == nil {
			return nil
		}
		declaration, symbol = origin, source
	}
	data := l.containerTree(literal, steps, depth)
	if data == nil {
		return nil
	}
	checked := map[*ast.Node]bool{}
	var check func(*ast.Node, *ast.Symbol, *literalContainer, int) bool
	check = func(declaration *ast.Node, symbol *ast.Symbol, tree *literalContainer, depth int) bool {
		if done, seen := checked[declaration]; seen {
			return done // a cycle is unknown, not a provisional positive
		}
		checked[declaration] = false
		component[declaration] = tree
		uses := l.containerReferences(symbol)
		if uses.overflow {
			return false
		}
		aliasUse := func(selected *literalContainer, read *ast.Node, depth int) bool {
			parent := read.Parent
			alias, target := l.containerBinding(parent.Name())
			return alias == parent && l.containerDataTypes(l.containerReadType(read, steps, depth), l.checker.GetTypeOfSymbol(target), steps) &&
				check(alias, target, selected, depth)
		}
		for _, read := range uses.reads {
			// Each sibling starts at the same depth; fanout is work, not recursion.
			if !tree.projectionUse(read, steps, depth, aliasUse) {
				return false
			}
		}
		checked[declaration] = true
		return true
	}
	if !check(declaration, symbol, data, depth) {
		return nil
	}
	complete = true
	return component[requested]
}

// An annotation may adapt functions into methods or change an array's layout.
// Accept exact checker types, or data-only objects with identical member contracts;
// readonly alone neither proves closure nor licenses a representation guess.
func (l *lowerer) containerDataTypes(from, to *checker.Type, steps *int) bool {
	if from == nil || to == nil || from.Flags()&checker.TypeFlagsObject == 0 || to.Flags()&checker.TypeFlagsObject == 0 {
		return false
	}
	if from == to {
		return true
	}
	if l.checker.IsArrayLikeType(from) || l.checker.IsArrayLikeType(to) ||
		l.checker.TypeHasCallOrConstructSignatures(from) || l.checker.TypeHasCallOrConstructSignatures(to) ||
		len(l.checker.GetIndexInfosOfType(from)) != 0 || len(l.checker.GetIndexInfosOfType(to)) != 0 {
		return false
	}
	properties := l.checker.GetPropertiesOfType(from)
	if len(properties) > *steps || len(properties) != len(l.checker.GetPropertiesOfType(to)) {
		return false
	}
	for _, property := range properties {
		*steps--
		other := l.checker.GetPropertyOfType(to, property.Name)
		if other == nil || property.Flags&ast.SymbolFlagsProperty == 0 || other.Flags&ast.SymbolFlagsProperty == 0 ||
			(property.Flags|other.Flags)&(ast.SymbolFlagsMethod|ast.SymbolFlagsAccessor) != 0 ||
			(property.Flags^other.Flags)&ast.SymbolFlagsOptional != 0 ||
			l.checker.GetTypeOfSymbol(property) != l.checker.GetTypeOfSymbol(other) {
			return false
		}
	}
	return true
}

// This kind-independent cache contains syntax only. Function and receiver source
// summaries still live in separate caches. Validate the entire literal, not just
// the selected prefix: a later spread/accessor/duplicate can invalidate that value.
func literalContainerMembers(literal *ast.Node, steps *int, depth int) map[string]*ast.Node {
	if literal == nil || depth >= maxValueFlowDepth {
		return nil
	}
	members := map[string]*ast.Node{}
	switch literal.Kind {
	case ast.KindObjectLiteralExpression:
		for _, property := range literal.AsObjectLiteralExpression().Properties.Nodes {
			if *steps == 0 {
				return nil
			}
			*steps--
			var value *ast.Node
			switch property.Kind {
			case ast.KindPropertyAssignment:
				value = property.Initializer()
			case ast.KindShorthandPropertyAssignment:
				if property.AsShorthandPropertyAssignment().ObjectAssignmentInitializer != nil {
					return nil
				}
				value = property.Name()
			default:
				return nil
			}
			key, ok := literalDataKey(property.Name(), true)
			if !ok || key == "__proto__" || members[key] != nil || value == nil {
				return nil
			}
			members[key] = value
		}
	case ast.KindArrayLiteralExpression:
		for index, element := range literal.AsArrayLiteralExpression().Elements.Nodes {
			if *steps == 0 || element.Kind == ast.KindSpreadElement || element.Kind == ast.KindOmittedExpression {
				return nil
			}
			*steps--
			members[strconv.Itoa(index)] = element
		}
	default:
		return nil
	}
	return members
}

func literalProjectionKey(node *ast.Node) (string, bool) {
	switch node.Kind {
	case ast.KindPropertyAccessExpression:
		return literalDataKey(node.Name(), true)
	case ast.KindElementAccessExpression:
		return literalDataKey(node.AsElementAccessExpression().ArgumentExpression, false)
	}
	return "", false
}

// Accept only canonical nonnegative integer keys from the parser's numeric text;
// source spellings may already be normalized. Other values remain unknown.
func literalDataKey(node *ast.Node, identifier bool) (string, bool) {
	if node == nil {
		return "", false
	}
	switch node.Kind {
	case ast.KindIdentifier:
		return node.Text(), identifier
	case ast.KindStringLiteral:
		return node.Text(), true
	case ast.KindNumericLiteral:
		text := node.Text()
		value, err := strconv.ParseUint(text, 10, 32)
		return text, err == nil && strconv.FormatUint(value, 10) == text
	}
	return "", false
}

// A function invoked as h.f() (even through assertions/parentheses or as a tag)
// receives h as this and can mutate it without another reference to its binding.
// Walk assignment patterns too; wrappers can otherwise hide a write/delete/update.
func containerProjectionRead(node *ast.Node, steps *int, depth int) bool {
	for containerStep(steps, &depth) {
		parent := node.Parent
		if parent == nil {
			return false
		}
		switch parent.Kind {
		case ast.KindParenthesizedExpression, ast.KindNonNullExpression, ast.KindAsExpression,
			ast.KindTypeAssertionExpression, ast.KindSatisfiesExpression, ast.KindExpressionWithTypeArguments,
			ast.KindArrayLiteralExpression, ast.KindSpreadElement:
			node = parent
		case ast.KindPropertyAssignment:
			if parent.Initializer() != node {
				return true
			}
			node = parent.Parent
		case ast.KindSpreadAssignment:
			node = parent.Parent
		case ast.KindBinaryExpression:
			return !ast.IsAssignmentOperator(parent.AsBinaryExpression().OperatorToken.Kind) || parent.AsBinaryExpression().Left != node
		case ast.KindPrefixUnaryExpression:
			operator := parent.AsPrefixUnaryExpression().Operator
			return operator != ast.KindPlusPlusToken && operator != ast.KindMinusMinusToken
		case ast.KindPostfixUnaryExpression, ast.KindDeleteExpression:
			return false
		case ast.KindForInStatement, ast.KindForOfStatement:
			return parent.Initializer() != node
		case ast.KindCallExpression:
			return parent.Expression() != node
		case ast.KindTaggedTemplateExpression:
			return parent.AsTaggedTemplateExpression().Tag != node
		default:
			return true
		}
	}
	return false
}

func containerStep(steps *int, depth *int) bool {
	if *steps == 0 || *depth >= maxValueFlowDepth {
		return false
	}
	*steps--
	*depth++
	return true
}

// Like the named-caller index, this is one complete pass over all modules. Keep
// only const-binding references and cap storage per binding; overflow is unknown.
func (l *lowerer) containerReferences(symbol *ast.Symbol) *containerUses {
	flow := &l.valueFlow
	if flow.containerUses == nil {
		flow.containerUses = map[*ast.Symbol]*containerUses{}
		var visit ast.Visitor
		visit = func(node *ast.Node) bool {
			if node.Kind == ast.KindIdentifier {
				s := l.readSymbol(node)
				if s != nil && s.ValueDeclaration != nil {
					declaration := s.ValueDeclaration
					if declaration.Kind == ast.KindVariableDeclaration && declaration.Name().Kind == ast.KindIdentifier &&
						declaration.Parent.Flags&ast.NodeFlagsConst != 0 && node != declaration.Name() {
						uses := flow.containerUses[s]
						if uses == nil {
							uses = &containerUses{}
							flow.containerUses[s] = uses
						}
						if len(uses.reads) < maxValueFlowSteps {
							uses.reads = append(uses.reads, node)
						} else {
							uses.overflow = true
						}
					}
				}
			}
			node.ForEachChild(visit)
			return false
		}
		for _, file := range flow.files {
			file.AsNode().ForEachChild(visit)
		}
	}
	if uses := flow.containerUses[symbol]; uses != nil {
		return uses
	}
	return &containerUses{}
}
