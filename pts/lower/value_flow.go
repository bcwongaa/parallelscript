package lower

import (
	"slices"

	"github.com/microsoft/typescript-go/internal/ast"
)

// functionSources is a proven over-approximation of source identities: function
// declarations/wrapper sites, or class declarations in a separate receiver proof (D-206).
// nil means unknown, not no sources. An identity covers every runtime value at its site.
type functionSources map[*ast.Node]bool

// valueFlow proves immutable sources, closed named parameters (D-201) and synchronous
// non-generic returns (D-202). Unknown sources keep the old type-compatible pairing.
type valueFlow struct {
	sourceCache
	receivers sourceCache // a function-return summary is never a receiver-return summary
	files     []*ast.SourceFile
	uses      map[*ast.Symbol]*functionValueUses
	wrappers  map[*ast.Node]bool
}

type sourceCache struct {
	sources map[*ast.Node]functionSources
	returns map[*ast.Node]functionSources
	active  map[*ast.Node]bool
}

// sourceProof shares traversal and budgets, but not summaries, between the two kinds.
// A receiver call still resolves its callees with a function proof.
type sourceProof struct {
	l         *lowerer
	receivers bool
}

func (p sourceProof) cache() *sourceCache {
	cache := &p.l.valueFlow.sourceCache
	if p.receivers {
		cache = &p.l.valueFlow.receivers
	}
	if cache.sources == nil {
		cache.sources = map[*ast.Node]functionSources{}
		cache.active = map[*ast.Node]bool{}
	}
	return cache
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
	steps := maxValueFlowSteps
	sources := l.valueSources(d.source, &steps, 0)
	return sources == nil || sources[source]
}

func (l *lowerer) valueSources(node *ast.Node, steps *int, depth int) functionSources {
	return (sourceProof{l: l}).valueSources(node, steps, depth)
}

func (p sourceProof) valueSources(node *ast.Node, steps *int, depth int) (sources functionSources) {
	if node == nil {
		return nil
	}
	l, flow := p.l, p.cache()
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
	if p.receivers && (l.isDynamicSource(node) || l.storedDynamic(node)) {
		return nil
	}
	if !p.receivers && l.valueFlow.wrappers[node] {
		return functionSources{node: true}
	}
	next := func(child *ast.Node) functionSources { return p.valueSources(child, steps, depth+1) }
	switch node.Kind {
	case ast.KindArrowFunction, ast.KindFunctionExpression:
		if !p.receivers {
			return functionSources{node: true}
		}
	case ast.KindNewExpression:
		if p.receivers {
			return l.constructedReceiver(node, steps, depth+1)
		}
	case ast.KindParenthesizedExpression, ast.KindNonNullExpression, ast.KindSatisfiesExpression:
		return next(node.Expression())
	case ast.KindAsExpression, ast.KindTypeAssertionExpression:
		if !p.receivers {
			return next(node.Expression()) // checked object assertions may change representation
		}
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
		return p.callSources(node.AsCallExpression(), steps, depth+1)
	case ast.KindParameter:
		return p.parameterSources(node, steps, depth+1)
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
			if !p.receivers && len(symbol.Declarations) == 1 && declaration.Body() != nil && !l.functionValueUses(symbol).written {
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
func (p sourceProof) parameterSources(parameter *ast.Node, steps *int, depth int) functionSources {
	l := p.l
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
		sources = mergeFunctionSources(sources, p.valueSources(args[index], steps, depth))
		if sources == nil {
			return nil
		}
	}
	return sources
}

// functionValueUses indexes all source references once, including uses outside a
// declaration's body and shorthand values. Only its own declaration name is skipped:
// imports, export specifiers, type queries and aliased calls make a consumer non-closed.
// Class references share the write index: assignedAnywhere does not track class bindings.
func (l *lowerer) functionValueUses(symbol *ast.Symbol) *functionValueUses {
	flow := &l.valueFlow
	if flow.uses == nil {
		flow.uses = map[*ast.Symbol]*functionValueUses{}
		var visit ast.Visitor
		visit = func(node *ast.Node) bool {
			if node.Kind == ast.KindIdentifier {
				s := l.readSymbol(node)
				if s != nil && s.ValueDeclaration != nil && (s.ValueDeclaration.Kind == ast.KindFunctionDeclaration || s.ValueDeclaration.Kind == ast.KindClassDeclaration) && node != s.ValueDeclaration.Name() {
					uses := flow.uses[s]
					if uses == nil {
						uses = &functionValueUses{}
						flow.uses[s] = uses
					}
					uses.written = uses.written || sourceBindingWritten(node)
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

// Assertions are legal assignment targets, but IsAssignmentTarget does not climb them
// from their identifier. An exhausted wrapper walk conservatively marks a binding written.
func sourceBindingWritten(node *ast.Node) bool {
	for depth := 0; depth < maxValueFlowDepth; depth++ {
		parent := node.Parent
		if parent == nil {
			return ast.IsAssignmentTarget(node)
		}
		switch parent.Kind {
		case ast.KindParenthesizedExpression, ast.KindNonNullExpression, ast.KindAsExpression,
			ast.KindTypeAssertionExpression, ast.KindSatisfiesExpression:
			node = parent
		default:
			return ast.IsAssignmentTarget(node)
		}
	}
	return true
}
