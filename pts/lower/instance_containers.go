package lower

import (
	"slices"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/pts/ir"
)

// A new container that holds objects of an instance an instanceof narrowing holds as a
// dynamic value (`[...c.matrix]`, `new Set(c.matrix)`, `Array.from(c.store.values())`, for
// a MapLRU<string, number> whose matrix tsc types any[][]) would convert them to its tsc
// element type, an any[] the instance's number[] reaches only as a copy, so writes through
// the container would miss the instance (C285). Such a container holds dynamic values
// instead, and is itself held as a dynamic value, whose elements are found at run time, as
// the member it was made from is (isInstanceofNestedObject): the objects stay the
// instance's own (H10b).

// isInstanceofContainer reports whether node makes such a container: an array literal,
// `Array.of(…)`, `new Map(x)`, `new Set(x)`, `Array.from(x)`, `Object.values(x)` or
// `Object.entries(x)` whose tsc element type (a map's key or value type) would copy
// (copiesInstanceObjects) an element, a spread's item or x's item that an instanceof
// narrowing holds as a dynamic value (holdsInstanceofDynamicValue), or an iterator's step
// (isInstanceofIteratorStep).
func (l *lowerer) isInstanceofContainer(node *ast.Node) bool {
	return l.instanceofContainer(node, false)
}

// instanceofContainerKey is a node and whether instanceofContainer asks for values an
// instanceof narrowing made dynamic, in the instance being lowered, whose types are its own.
type instanceofContainerKey struct {
	node  *ast.Node
	made  bool
	subst *substitution
}

func (l *lowerer) instanceofContainer(node *ast.Node, made bool) bool {
	node = ast.SkipParentheses(node)
	switch {
	case len(l.genericClasses) == 0:
		return false
	case node.Kind != ast.KindArrayLiteralExpression && node.Kind != ast.KindNewExpression && node.Kind != ast.KindCallExpression:
		return false
	}
	key := instanceofContainerKey{node, made, l.subst}
	if known, ok := l.instanceofContainers[key]; ok {
		return known
	}
	if l.instanceofContainers == nil {
		l.instanceofContainers = map[instanceofContainerKey]bool{} // a lowerer a test makes
	}
	l.instanceofContainers[key] = false // an element that holds the container itself
	known := l.makesInstanceofContainer(node, made)
	l.instanceofContainers[key] = known
	return known
}

func (l *lowerer) makesInstanceofContainer(node *ast.Node, made bool) bool {
	switch node.Kind {
	case ast.KindArrayLiteralExpression:
		return l.copiesElementsOf(node) && slices.ContainsFunc(node.Elements(), func(element *ast.Node) bool {
			if element.Kind == ast.KindSpreadElement {
				element = element.Expression()
			}
			return element.Kind != ast.KindOmittedExpression && l.instanceofDynamicSource(element, made)
		})
	case ast.KindNewExpression:
		if !l.isMapConstructor(node) || len(node.Arguments()) != 1 {
			return false
		}
		mt, ok := l.irType(l.typeAt(node), node).(*ir.MapType)
		return ok && (copiesInstanceObjects(mt.Key) || mt.Value != nil && copiesInstanceObjects(mt.Value)) && l.instanceofDynamicSource(node.Arguments()[0], made)
	case ast.KindCallExpression:
		callee := ast.SkipParentheses(node.Expression())
		switch {
		case l.isArrayStatic(callee, "from") || l.isObjectStatic(callee, "values", "entries"):
			return len(node.Arguments()) == 1 && l.copiesElementsOf(node) && l.instanceofDynamicSource(node.Arguments()[0], made)
		case l.isArrayStatic(callee, "of"):
			return l.copiesElementsOf(node) && slices.ContainsFunc(node.Arguments(), func(argument *ast.Node) bool {
				if argument.Kind == ast.KindSpreadElement {
					argument = argument.Expression()
				}
				return l.instanceofDynamicSource(argument, made)
			})
		}
		return l.isInstanceofIteratorStep(node, made)
	}
	return false
}

// isInstanceofIteratorStep reports whether call is `it.next()`, `it.return(v)` or
// `it.throw(e)` of an iterator an instanceof narrowing holds as a dynamic value
// (`c.store.values().next()`), whose value tsc's result type would copy: its result holds
// the value as it is, and is held as a dynamic value (C285).
func (l *lowerer) isInstanceofIteratorStep(call *ast.Node, made bool) bool {
	callee := ast.SkipParentheses(call.Expression())
	if callee.Kind == ast.KindNonNullExpression {
		callee = ast.SkipParentheses(callee.Expression())
	}
	if callee.Kind != ast.KindPropertyAccessExpression {
		return false
	}
	switch callee.AsPropertyAccessExpression().Name().Text() {
	case "next", "return", "throw":
	default:
		return false
	}
	iterator := l.checker.GetNonNullableType(l.typeAt(callee.Expression()))
	if !l.isIteratorType(iterator) {
		return false
	}
	yield, _, _ := l.iterationTypes(iterator)
	return copiesInstanceObjects(l.irType(yield, call)) && l.instanceofDynamicSource(callee.Expression(), made)
}

// copiesElementsOf reports whether node, of an array type to tsc, holds elements that a
// dynamic value's object converts to by a copy (copiesInstanceObjects: `any[][]`).
func (l *lowerer) copiesElementsOf(node *ast.Node) bool {
	t := l.typeAt(node)
	return l.checker.IsArrayType(t) && copiesInstanceObjects(l.irType(l.checker.GetTypeArguments(t)[0], node))
}

// instanceofContainerLiteral lowers an array literal isInstanceofContainer holds: a new array
// of dynamic values, the elements and spreads' items as they are, held as a dynamic value.
func (l *lowerer) instanceofContainerLiteral(node *ast.Node) ir.Expr {
	array := l.arrayLiteralOf(node, ir.ValueArray)
	if array == nil {
		return nil
	}
	return box(array)
}

// dynamicCollectionOf is mt, the map or set type of a container isInstanceofContainer
// holds, with dynamic values for its key and value types that would copy an instance's
// objects.
func (l *lowerer) dynamicCollectionOf(mt *ir.MapType) *ir.MapType {
	key, value := mt.Key, mt.Value
	if copiesInstanceObjects(key) {
		key = ir.Value
	}
	if value != nil && copiesInstanceObjects(value) {
		value = ir.Value
	}
	return l.mapOf(key, value, mt.Set)
}
