package lower

import (
	"slices"

	"github.com/microsoft/typescript-go/internal/ast"
)

// functionSources is a proven over-approximation of a value's function declarations or
// exact wrapper creation sites. nil means unknown, not no sources. One source stands for
// all its closures and enclosing instances, never a particular capture or receiver.
type functionSources map[*ast.Node]bool

// valueFlow proves immutable sources, closed named parameters (D-201) and synchronous
// non-generic returns (D-202). Unknown sources keep the old type-compatible pairing.
type valueFlow struct {
	files    []*ast.SourceFile
	sources  map[*ast.Node]functionSources
	returns  map[*ast.Node]functionSources
	active   map[*ast.Node]bool
	uses     map[*ast.Symbol]*functionValueUses
	wrappers map[*ast.Node]bool
}

type functionValueUses struct {
	calls   []*ast.CallExpression
	escapes bool
	written bool
}

const (
	maxValueFlowDepth  = 128
	maxValueFlowSteps  = 4096
	maxFunctionSources = 256
)

// sourceMayReach filters value calls by proven declarations or exact wrapper sites.
// Named methods still dispatch conservatively; a forwarder's inner value demand names
// its captured expression, not the outer call's expression (D-205).
func (l *lowerer) sourceMayReach(d *demand, v *erasedValue) bool {
	if d.source == nil || d.name != "" || d.method != nil {
		return true
	}
	var source *ast.Node
	if v.forward != nil {
		source = v.forward.source
	} else if v.generic.field == nil {
		source = v.generic.node
	}
	if source == nil {
		return true
	}
	flow := &l.valueFlow
	if flow.sources == nil {
		flow.sources = map[*ast.Node]functionSources{}
		flow.active = map[*ast.Node]bool{}
	}
	steps := maxValueFlowSteps
	sources := l.valueSources(d.source, &steps, 0)
	return sources == nil || sources[source]
}

func (l *lowerer) valueSources(node *ast.Node, steps *int, depth int) (sources functionSources) {
	if node == nil {
		return nil
	}
	flow := &l.valueFlow
	if known, ok := flow.sources[node]; ok {
		return known
	}
	if depth >= maxValueFlowDepth || *steps == 0 || flow.active[node] {
		return nil
	}
	*steps--
	flow.active[node] = true
	defer func() {
		delete(flow.active, node)
		flow.sources[node] = sources
	}()
	if flow.wrappers[node] {
		return functionSources{node: true}
	}
	next := func(child *ast.Node) functionSources { return l.valueSources(child, steps, depth+1) }
	switch node.Kind {
	case ast.KindArrowFunction, ast.KindFunctionExpression:
		return functionSources{node: true}
	case ast.KindParenthesizedExpression, ast.KindAsExpression, ast.KindTypeAssertionExpression,
		ast.KindNonNullExpression, ast.KindSatisfiesExpression:
		return next(node.Expression())
	case ast.KindConditionalExpression:
		expr := node.AsConditionalExpression()
		return mergeFunctionSources(next(expr.WhenTrue), next(expr.WhenFalse))
	case ast.KindBinaryExpression:
		expr := node.AsBinaryExpression()
		if expr.OperatorToken.Kind == ast.KindCommaToken {
			return next(expr.Right)
		}
		if ast.IsLogicalOrCoalescingBinaryOperator(expr.OperatorToken.Kind) {
			return mergeFunctionSources(next(expr.Left), next(expr.Right))
		}
	case ast.KindNullKeyword, ast.KindTrueKeyword, ast.KindFalseKeyword,
		ast.KindNumericLiteral, ast.KindStringLiteral, ast.KindNoSubstitutionTemplateLiteral,
		ast.KindVoidExpression:
		return functionSources{} // these cannot be a function, even when asserted as one
	case ast.KindCallExpression:
		return l.callSources(node.AsCallExpression(), steps, depth+1)
	case ast.KindParameter:
		return l.parameterSources(node, steps, depth+1)
	case ast.KindIdentifier:
		symbol := l.readSymbol(node)
		if symbol == l.checker.GetUndefinedSymbol() {
			return functionSources{}
		}
		if symbol == nil || symbol.ValueDeclaration == nil {
			return nil
		}
		declaration := symbol.ValueDeclaration
		switch declaration.Kind {
		case ast.KindFunctionDeclaration:
			if len(symbol.Declarations) == 1 && declaration.Body() != nil && !l.functionValueUses(symbol).written {
				return functionSources{declaration: true}
			}
		case ast.KindVariableDeclaration:
			if declaration.Name().Kind == ast.KindIdentifier && declaration.Parent.Flags&ast.NodeFlagsConst != 0 && !l.assignedAnywhere[symbol] {
				return next(declaration.Initializer())
			}
		case ast.KindParameter:
			return next(declaration) // every read shares the parameter's bounded caller join
		}
	}
	return nil
}

func mergeFunctionSources(a, b functionSources) functionSources {
	if a == nil || b == nil {
		return nil
	}
	merged := functionSources{}
	for node := range a {
		merged[node] = true
	}
	for node := range b {
		merged[node] = true
	}
	if len(merged) > maxFunctionSources {
		return nil
	}
	return merged
}

// parameterSources joins every caller only when the declaration cannot be called through
// a value. Mutation, generic contexts, defaults, patterns, rest and missing/spread arguments
// are deliberately unknown. Cycles through callers are caught by valueSources' active set.
func (l *lowerer) parameterSources(parameter *ast.Node, steps *int, depth int) functionSources {
	fn := parameter.Parent
	if fn.Kind != ast.KindFunctionDeclaration || fn.Body() == nil || len(fn.TypeParameters()) != 0 ||
		ast.HasSyntacticModifier(fn, ast.ModifierFlagsExport) {
		return nil
	}
	for _, p := range fn.Parameters() {
		if p.Name().Kind != ast.KindIdentifier || p.Name().Text() == "this" || p.Initializer() != nil || p.AsParameterDeclaration().DotDotDotToken != nil {
			return nil
		}
	}
	if l.assignedAnywhere[l.target(parameter.Name())] {
		return nil
	}
	owner := l.target(fn.Name())
	if owner == nil || len(owner.Declarations) != 1 {
		return nil
	}
	uses := l.functionValueUses(owner)
	if uses.escapes || uses.written || len(uses.calls) == 0 {
		return nil
	}
	index := slices.Index(fn.Parameters(), parameter)
	if index < 0 {
		return nil
	}
	sources := functionSources{}
	for _, call := range uses.calls {
		if *steps == 0 {
			return nil
		}
		*steps-- // a caller join is work even when its argument proof is cached
		args := call.Arguments.Nodes
		if index >= len(args) || slices.ContainsFunc(args, func(arg *ast.Node) bool { return arg.Kind == ast.KindSpreadElement }) {
			return nil
		}
		sources = mergeFunctionSources(sources, l.valueSources(args[index], steps, depth))
		if sources == nil {
			return nil
		}
	}
	return sources
}

// functionValueUses indexes all source references once, including uses outside a
// declaration's body and shorthand values. Only its own declaration name is skipped:
// imports, export specifiers, type queries and aliased calls make a consumer non-closed.
func (l *lowerer) functionValueUses(symbol *ast.Symbol) *functionValueUses {
	flow := &l.valueFlow
	if flow.uses == nil {
		flow.uses = map[*ast.Symbol]*functionValueUses{}
		var visit ast.Visitor
		visit = func(node *ast.Node) bool {
			if node.Kind == ast.KindIdentifier {
				s := l.readSymbol(node)
				if s != nil && s.ValueDeclaration != nil && s.ValueDeclaration.Kind == ast.KindFunctionDeclaration && node != s.ValueDeclaration.Name() {
					uses := flow.uses[s]
					if uses == nil {
						uses = &functionValueUses{}
						flow.uses[s] = uses
					}
					uses.written = uses.written || ast.IsAssignmentTarget(node)
					parent := node.Parent
					for parent.Kind == ast.KindParenthesizedExpression {
						parent = parent.Parent
					}
					if parent.Kind == ast.KindCallExpression && ast.SkipParentheses(parent.Expression()) == node {
						uses.calls = append(uses.calls, parent.AsCallExpression())
					} else {
						uses.escapes = true
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
	if uses := flow.uses[symbol]; uses != nil {
		return uses
	}
	return &functionValueUses{}
}
