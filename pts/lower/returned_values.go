package lower

import "github.com/microsoft/typescript-go/internal/ast"

// callSources joins every possible callee's returned sources. Parameter proofs still join
// all callers, never a call-specific environment that a declaration cache could confuse.
func (l *lowerer) callSources(call *ast.CallExpression, steps *int, depth int) functionSources {
	callees := l.valueSources(call.Expression, steps, depth)
	if callees == nil {
		return nil
	}
	sources := functionSources{}
	for fn := range callees {
		if *steps == 0 {
			return nil
		}
		*steps-- // joining cached return summaries still consumes work
		sources = mergeFunctionSources(sources, l.returnSources(fn, steps, depth))
		if sources == nil {
			return nil
		}
	}
	return sources
}

// returnSources caches a synchronous non-generic function's entire return union (D-202).
// Async/generator calls return wrappers, not what their bodies return. A source identity
// stands for every enclosing generic instance, as in the ordinary value-source proof.
func (l *lowerer) returnSources(fn *ast.Node, steps *int, depth int) (sources functionSources) {
	flow := &l.valueFlow
	if known, ok := flow.returns[fn]; ok {
		return known
	}
	if depth >= maxValueFlowDepth || *steps == 0 || flow.active[fn] {
		return nil
	}
	*steps--
	flow.active[fn] = true
	defer func() {
		delete(flow.active, fn)
		if flow.returns == nil {
			flow.returns = map[*ast.Node]functionSources{}
		}
		flow.returns[fn] = sources
	}()
	if fn.Kind != ast.KindFunctionDeclaration && fn.Kind != ast.KindFunctionExpression && fn.Kind != ast.KindArrowFunction {
		return nil
	}
	if fn.Body() == nil || fn.BodyData().AsteriskToken != nil || ast.HasSyntacticModifier(fn, ast.ModifierFlagsAsync) {
		return nil
	}
	if len(l.checker.GetSignatureFromDeclaration(fn).TypeParameters()) != 0 {
		return nil // a factory can be generic through context, with no syntactic parameters
	}
	if fn.Body().Kind != ast.KindBlock {
		return l.valueSources(fn.Body(), steps, depth+1)
	}
	return l.bodyReturnSources(fn.Body(), steps, depth+1)
}

// bodyReturnSources joins every explicit return, including unreachable and finally arms.
// Falling through or returning nothing adds no functions; any unknown arm makes the union
// unknown. Nested callables and classes have no returns belonging to this body.
func (l *lowerer) bodyReturnSources(body *ast.Node, steps *int, depth int) functionSources {
	sources := functionSources{}
	var visit func(*ast.Node, int) bool
	visit = func(node *ast.Node, depth int) bool {
		if depth >= maxValueFlowDepth || *steps == 0 {
			sources = nil
			return true
		}
		*steps--
		if ast.IsFunctionLike(node) || ast.IsClassLike(node) {
			return false
		}
		if node.Kind == ast.KindReturnStatement {
			if expression := node.Expression(); expression != nil {
				sources = mergeFunctionSources(sources, l.valueSources(expression, steps, depth+1))
			}
			return sources == nil
		}
		return node.ForEachChild(func(child *ast.Node) bool { return visit(child, depth+1) })
	}
	visit(body, depth)
	return sources
}
