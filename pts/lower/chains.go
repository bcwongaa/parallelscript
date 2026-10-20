package lower

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/pts/ir"
)

// chainObject is the object of a `?.` link while the rest of its chain is lowered: its
// value, a temporary known not to be null or undefined, and that checker type.
type chainObject struct {
	value       ir.Expr
	checkerType *checker.Type
}

// optionalChain lowers an optional chain (D-064) whose outermost node is node: the
// object before its outermost `?.` link is evaluated once into a temporary; if that is
// null or undefined the whole chain is undefined without evaluating the rest, and
// otherwise the rest is lowered with the object as its non-nullable type. An object that
// is itself an optional chain (`a?.b.c?.d`) is lowered as one first. For a method's
// optional call (`o.m?.(x)`, `o["m"]?.(x)`), an o whose evaluation may have an effect is
// evaluated once, before m is read, and the call is the method's call on it (C203).
func (l *lowerer) optionalChain(node *ast.Node) ir.Expr {
	link := node
	for !ast.IsOptionalChainRoot(link) {
		link = link.Expression()
	}
	receiverNode := calledMethodReceiver(link)
	if receiverNode == nil || !l.mayHaveEffect(receiverNode) {
		return l.optionalChainFrom(node, link)
	}
	receiver := l.expression(receiverNode)
	if receiver == nil {
		return nil
	}
	local := l.newLocal("receiver", receiver.Type())
	kept, wasKept := l.kept[receiverNode]
	l.kept[receiverNode] = &ir.LocalRef{Local: local} // what the method's read and its call evaluate
	chain := l.optionalChainFrom(node, link)
	if wasKept {
		l.kept[receiverNode] = kept
	} else {
		delete(l.kept, receiverNode)
	}
	if chain == nil {
		return nil
	}
	return &ir.Bind{Local: local, Value: receiver, In: chain}
}

// calledMethodReceiver is the object of a method's optional call, link (`o` of `o.m?.(x)`
// and `o["m"]?.(x)`), or nil: another link, a call of something else, `super`'s method, or
// an object or method read that is an optional chain itself, which is lowered as one.
func calledMethodReceiver(link *ast.Node) *ast.Node {
	if link.Kind != ast.KindCallExpression {
		return nil
	}
	callee := link.Expression()
	if !ast.IsAccessExpression(callee) || ast.IsOptionalChain(callee) {
		return nil
	}
	receiver := callee.Expression()
	if receiver.Kind == ast.KindSuperKeyword || ast.IsOptionalChain(receiver) {
		return nil
	}
	return receiver
}

// mayHaveEffect reports whether evaluating receiver, the object of a member's read, may
// have an effect that evaluating it twice would repeat: not a name or `this`, nor a member
// read naming a class, a namespace, a module or an enum (`Util.m?.(x)`, `ns.f?.(x)`), whose
// static member or function the call reaches without evaluating it as a value.
func (l *lowerer) mayHaveEffect(receiver *ast.Node) bool {
	switch receiver = ast.SkipParentheses(receiver); receiver.Kind {
	case ast.KindIdentifier, ast.KindThisKeyword:
		return false
	case ast.KindPropertyAccessExpression:
		symbol := l.target(receiver)
		return symbol == nil || symbol.Flags&(ast.SymbolFlagsClass|ast.SymbolFlagsValueModule|ast.SymbolFlagsNamespaceModule|ast.SymbolFlagsEnum) == 0
	}
	return true
}

// optionalChainFrom is optionalChain's lowering of node, whose `?.` link is link.
func (l *lowerer) optionalChainFrom(node, link *ast.Node) ir.Expr {
	objectNode := link.Expression()
	var object ir.Expr
	element := l.isElementRead(objectNode)
	// tsc's type of the object, and so of the chain, may lack the undefined it holds: an
	// element past the end, or a loose local (`const raw = args[2]; raw?.length`, D-090, D-172).
	mayBeMissing := element || l.isLooseIdentifier(objectNode)
	if element {
		object = l.valueExpression(objectNode) // `terms[t]?.length` past the end is undefined, as in JS
	} else {
		object = l.expression(objectNode)
	}
	if object == nil {
		return nil
	}
	tmp := l.newLocal("object", object.Type())
	ref := &ir.LocalRef{Local: tmp}
	nonNullable := l.checker.GetNonNullableType(l.typeAt(objectNode))
	var value ir.Expr = ref
	dynamic := isUnion(object.Type()) || object.Type() == ir.Value // a Value: what `a.find(…)` gives
	if dynamic {
		t := l.irType(nonNullable, objectNode)
		if t == nil {
			return nil
		}
		value = fromUnion(ref, t)
	}
	l.chainObjects[objectNode] = chainObject{value: value, checkerType: nonNullable}
	l.chainRests[node] = true
	l.chainDepth++
	rest := l.valueExpression(node) // `a?.[9]` out of range is undefined, as the chain's own result may be
	l.chainDepth--
	delete(l.chainRests, node)
	delete(l.chainObjects, objectNode)
	if rest == nil {
		return nil
	}
	if !dynamic && object.Type() != ir.Null && object.Type() != ir.Undefined {
		return &ir.Bind{Local: tmp, Value: object, In: rest} // never null or undefined: `?.` is `.`
	}
	var whenNullish, whenNot ir.Expr = &ir.UndefinedConst{}, rest
	if rest.Type() == ir.Void {
		whenNot = &ir.Sequence{X: rest, Y: &ir.UndefinedConst{}} // `o?.m()` for a method that returns nothing
	} else {
		result := l.irType(l.typeAt(node), node)
		if result == nil {
			return nil
		}
		required := result
		if mayBeMissing {
			result = l.withUndefined(result)
		}
		if whenNullish, whenNot = l.convert(whenNullish, result, node), l.convert(rest, result, node); whenNullish == nil || whenNot == nil {
			return nil
		}
		if result != required && !l.takesAnyValue(node) {
			chain := &ir.Bind{Local: tmp, Value: object, In: &ir.Conditional{Cond: &ir.IsNullish{X: ref}, Then: whenNullish, Else: whenNot}}
			return l.checkedConversion(chain, required, node)
		}
	}
	return &ir.Bind{Local: tmp, Value: object, In: &ir.Conditional{Cond: &ir.IsNullish{X: ref}, Then: whenNullish, Else: whenNot}}
}

// takesAnyValue reports whether node's context takes any value, undefined included: the
// left of `??`, or a place whose type is any, unknown or a union with undefined.
func (l *lowerer) takesAnyValue(node *ast.Node) bool {
	if isNullishLeft(node) || l.contextTakesUndefined(node) {
		return true
	}
	contextual := l.contextualType(node)
	return contextual != nil && contextual.Flags()&(checker.TypeFlagsAny|checker.TypeFlagsUnknown) != 0
}

// isUnloweredOptionalChain reports whether node is part of an optional chain that isn't
// being lowered past its `?.` test, which only optionalChain lowers.
func (l *lowerer) isUnloweredOptionalChain(node *ast.Node) bool {
	return ast.IsOptionalChain(node) && l.chainDepth == 0
}
