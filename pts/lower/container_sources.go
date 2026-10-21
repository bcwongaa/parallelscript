package lower

import (
	"strconv"

	"github.com/microsoft/typescript-go/internal/ast"
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
	container := node.Expression()
	for container.Kind == ast.KindParenthesizedExpression {
		if !containerStep(steps, &depth) {
			return nil
		}
		container = container.Expression()
	}
	var members map[string]*ast.Node
	if container.Kind == ast.KindIdentifier {
		symbol := l.readSymbol(container)
		if symbol == nil || len(symbol.Declarations) != 1 || symbol.ValueDeclaration == nil {
			return nil
		}
		declaration := symbol.ValueDeclaration
		if declaration.Kind != ast.KindVariableDeclaration || declaration.Name().Kind != ast.KindIdentifier ||
			declaration.Parent.Flags&ast.NodeFlagsConst == 0 || l.assignedAnywhere[symbol] ||
			ast.GetCombinedModifierFlags(declaration)&ast.ModifierFlagsExport != 0 ||
			l.isDynamicSource(container) || l.storedDynamic(container) {
			return nil
		}
		members = l.closedContainer(declaration, symbol, steps, depth)
	} else {
		members = literalContainerMembers(container, steps, depth)
	}
	key, ok := literalProjectionKey(node)
	selected := members[key]
	if !ok || selected == nil || !containerProjectionRead(node, steps, depth) ||
		l.isDynamicSource(selected) || l.storedDynamic(selected) {
		return nil
	}
	return p.valueSources(selected, steps, depth+1)
}

func (l *lowerer) closedContainer(declaration *ast.Node, symbol *ast.Symbol, steps *int, depth int) (members map[string]*ast.Node) {
	flow := &l.valueFlow
	if flow.containers == nil {
		flow.containers = map[*ast.Node]map[string]*ast.Node{}
	}
	if known, ok := flow.containers[declaration]; ok {
		return known
	}
	defer func() { flow.containers[declaration] = members }()
	literal := declaration.Initializer()
	for literal != nil && literal.Kind == ast.KindParenthesizedExpression {
		if !containerStep(steps, &depth) {
			return nil
		}
		literal = literal.Expression()
	}
	data := literalContainerMembers(literal, steps, depth)
	if data == nil {
		return nil
	}
	uses := l.containerReferences(symbol)
	if uses.overflow {
		return nil
	}
	for _, read := range uses.reads {
		// Each reference starts at the same depth; a wide join is work, not recursion.
		referenceDepth := depth
		if !containerStep(steps, &referenceDepth) {
			return nil
		}
		for read.Parent != nil && read.Parent.Kind == ast.KindParenthesizedExpression {
			if !containerStep(steps, &referenceDepth) {
				return nil
			}
			read = read.Parent
		}
		projection := read.Parent
		if projection == nil || !ast.IsAccessExpression(projection) || projection.Expression() != read {
			return nil
		}
		key, ok := literalProjectionKey(projection)
		if !ok || data[key] == nil || !containerProjectionRead(projection, steps, referenceDepth) {
			return nil
		}
	}
	return data
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
