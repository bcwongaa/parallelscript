package lower

import (
	"fmt"
	"math"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/pts/ir"
)

// Destructuring (D-054) holds the value in a local and binds each name of the pattern
// to a part of it: an object's property (a field, or a class's getter), a tuple's element
// (a field), or an array's element (a checked read, D-015, unless the element has a
// default for when the array is too short), recursively for nested patterns.

// parameterBinding is a parameter that its function's body starts by binding: one with
// a default, which value takes if the call left the argument out or passed undefined
// (given is false), and arg otherwise (D-055); or one written as a pattern, which the
// body destructures from value (the argument itself, if the parameter has no default).
type parameterBinding struct {
	node       *ast.Node // the parameter declaration
	value      *ir.Local
	arg, given *ir.Local // nil without a default
}

// destructure binds the names of a binding pattern to the parts of value, declaring their
// locals. It reports what it can't lower and returns false.
func (l *lowerer) destructure(pattern *ast.Node, value ir.Expr, at ir.Pos) ([]ir.Stmt, bool) {
	whole := l.newLocal("destructured", value.Type())
	stmts := []ir.Stmt{&ir.LocalDecl{At: at, Local: whole, Init: value}}
	for i, element := range pattern.AsBindingPattern().Elements.Nodes {
		if element.Kind == ast.KindOmittedExpression || element.Name() == nil {
			continue // an elision: `[a, , c]`
		}
		binding := element.AsBindingElement()
		var part ir.Expr
		if pattern.Kind == ast.KindObjectBindingPattern {
			part = l.propertyPart(&ir.LocalRef{Local: whole}, element, binding.PropertyName, binding.DotDotDotToken != nil)
		} else {
			part = l.elementPart(&ir.LocalRef{Local: whole}, element, i, binding.Initializer, binding.DotDotDotToken != nil)
		}
		if part == nil {
			return nil, false
		}
		if binding.Name().Kind != ast.KindIdentifier {
			nested, ok := l.destructure(binding.Name(), part, l.pos(element))
			if !ok {
				return nil, false
			}
			stmts = append(stmts, nested...)
			continue
		}
		stmt := l.bindName(binding.Name(), part, element)
		if stmt == nil {
			return nil, false
		}
		stmts = append(stmts, stmt)
	}
	return stmts, true
}

// bindName declares the local of a name that a pattern binds (a module-scope binding's
// exists already), initialised with value; nil if it failed (reported).
func (l *lowerer) bindName(name *ast.Node, value ir.Expr, node *ast.Node) ir.Stmt {
	symbol := l.checker.GetSymbolAtLocation(name)
	local, declared := l.locals[symbol]
	if !declared {
		if t := l.bindingType(symbol, name); t != nil {
			local = l.newLocal(name.Text(), t)
		}
		l.locals[symbol] = local
	}
	if local == nil {
		return nil
	}
	if value = l.convert(value, local.Type, node); value == nil {
		l.locals[symbol] = nil
		return nil
	}
	return &ir.LocalDecl{At: l.pos(node), Local: local, Init: value}
}

// propertyPart reads from source the property that an element of an object pattern (or
// of an object literal being assigned to) names: property, or the element's own name. A
// default never applies, since a property's type can't hold undefined yet.
func (l *lowerer) propertyPart(source ir.Expr, element *ast.Node, property *ast.Node, rest bool) ir.Expr {
	if rest {
		l.notSupportedYet(element, "rest properties (`...`) in object patterns are")
		return nil
	}
	if property == nil {
		property = element.Name()
	}
	if property.Kind != ast.KindIdentifier && property.Kind != ast.KindStringLiteral {
		l.notSupportedYet(property, "computed property names in object patterns are")
		return nil
	}
	name := property.Text()
	switch t := source.Type().(type) {
	case *ir.Class:
		if field := t.Field(name); field != nil && !field.Method {
			return &ir.FieldRef{X: source, Field: field}
		}
		if getter := t.Method(name, ir.Getter); getter != nil && !t.Shape {
			if getter.Virtual {
				return &ir.VirtualCall{Method: getter, Args: []ir.Expr{upcast(source, getter.Slot().Class)}}
			}
			return &ir.Call{Func: getter.Func, Args: []ir.Expr{upcast(source, getter.Class)}}
		}
	case *ir.Interface:
		if field := t.Field(name); field != nil {
			return &ir.FieldRef{X: source, Field: field}
		}
	}
	l.notSupportedYet(property, fmt.Sprintf("destructuring the property `%s` of a %s is", name, source.Type()))
	return nil
}

// elementPart reads from source the element at index of an array or tuple pattern (or of
// an array literal being assigned to): a tuple's field, or an array's element. With a
// default (initializer), an array too short to have the element gives the default; a
// rest element is the elements from index on.
func (l *lowerer) elementPart(source ir.Expr, element *ast.Node, index int, initializer *ast.Node, rest bool) ir.Expr {
	switch t := source.Type().(type) {
	case *ir.Class:
		if t.Tuple && !rest && index < len(t.Fields) {
			return &ir.FieldRef{X: source, Field: t.Fields[index]} // a tuple's elements are never undefined, so a default can't apply
		}
		if t.Tuple && !rest && initializer != nil {
			return l.expression(initializer) // past the tuple's end, so undefined: the default applies
		}
		if t.Tuple {
			return l.tupleRest(source, t, index, restTarget(element))
		}
	case *ir.Array:
		position := &ir.NumberConst{Value: float64(index)}
		if rest {
			return &ir.ArrayCall{Op: ir.ArraySlice, Args: []ir.Expr{source, position, &ir.NumberConst{Value: math.Inf(1)}}}
		}
		read := &ir.ArrayCall{Op: ir.ArrayElementChecked, Args: []ir.Expr{source, position}}
		if initializer == nil {
			return read
		}
		fallback := l.expression(initializer)
		if fallback == nil {
			return nil
		}
		if fallback = l.convert(fallback, t.Elem, initializer); fallback == nil {
			return nil
		}
		inRange := &ir.Binary{Op: ir.Lt, X: position, Y: &ir.ArrayCall{Op: ir.ArrayLength, Args: []ir.Expr{source}}}
		return &ir.Conditional{Cond: inRange, Then: read, Else: fallback}
	}
	l.notSupportedYet(element, fmt.Sprintf("destructuring a %s with an array pattern is", source.Type()))
	return nil
}

// parameterPrologue binds the parameters of fn that need it, in order, at the start of
// its body, declaring their names' locals before the body is lowered: a parameter with a
// default takes it when the call didn't pass the argument, which runs in the function's
// scope after the parameters before it are bound, as in JS; a pattern is destructured.
func (l *lowerer) parameterPrologue(fn *ir.Func) []ir.Stmt {
	var stmts []ir.Stmt
	for _, binding := range l.bindings[fn] {
		if binding.given != nil {
			value := l.parameterDefault(binding)
			if value == nil {
				continue // reported
			}
			stmts = append(stmts, &ir.LocalDecl{At: l.pos(binding.node), Local: binding.value, Init: value})
		}
		if pattern := binding.node.Name(); pattern.Kind != ast.KindIdentifier {
			destructured, ok := l.destructure(pattern, &ir.LocalRef{Local: binding.value}, l.pos(pattern))
			if !ok {
				continue // reported
			}
			stmts = append(stmts, destructured...)
		}
	}
	return stmts
}

// parameterDefault is the value of a parameter with a default: the argument if the call
// passed it, and otherwise the default; nil if the default fails to lower (reported).
func (l *lowerer) parameterDefault(binding *parameterBinding) ir.Expr {
	initializer := binding.node.Initializer()
	value := l.expression(initializer)
	if value == nil {
		return nil
	}
	if value = l.convert(value, binding.value.Type, initializer); value == nil {
		return nil
	}
	return &ir.Conditional{Cond: &ir.LocalRef{Local: binding.given}, Then: &ir.LocalRef{Local: binding.arg}, Else: value}
}

// isDestructuringSource reports whether node is the value a pattern destructures (or a
// literal nested in one): an initializer of a pattern, the right side of an assignment to
// an array or object literal, or what `for…of` with a pattern iterates.
func isDestructuringSource(node *ast.Node) bool {
	parent := node.Parent
	for parent != nil && parent.Kind == ast.KindParenthesizedExpression {
		node, parent = parent, parent.Parent
	}
	if parent == nil {
		return false
	}
	switch parent.Kind {
	case ast.KindVariableDeclaration, ast.KindParameter:
		return parent.Initializer() == node && parent.Name().Kind != ast.KindIdentifier
	case ast.KindBinaryExpression:
		binary := parent.AsBinaryExpression()
		left := ast.SkipParentheses(binary.Left)
		return binary.OperatorToken.Kind == ast.KindEqualsToken && binary.Right == node &&
			(left.Kind == ast.KindArrayLiteralExpression || left.Kind == ast.KindObjectLiteralExpression)
	case ast.KindArrayLiteralExpression:
		return isDestructuringSource(parent)
	case ast.KindPropertyAssignment:
		return isDestructuringSource(parent.Parent)
	}
	return false
}

// restTarget is what a rest element binds or assigns: `rest` in `...rest`.
func restTarget(element *ast.Node) *ast.Node {
	if element.Kind == ast.KindSpreadElement {
		return element.Expression()
	}
	return element.Name()
}

// tupleRest is `...rest` in an array pattern on a tuple: the elements from index on, as
// the array or tuple the rest's type (target's) is.
func (l *lowerer) tupleRest(source ir.Expr, tuple *ir.Class, index int, target *ast.Node) ir.Expr {
	element := target
	var elements []ir.Expr
	for _, field := range tuple.Fields[index:] {
		elements = append(elements, &ir.FieldRef{X: source, Field: field})
	}
	switch t := l.irType(l.typeAt(target), element).(type) {
	case *ir.Array:
		for i, x := range elements {
			if elements[i] = l.convert(x, t.Elem, element); elements[i] == nil {
				return nil
			}
		}
		return &ir.NewArray{Array: t, Elems: elements}
	case *ir.Class:
		if t.Tuple {
			return &ir.NewObject{Shape: t, Values: elements}
		}
	}
	l.notSupportedYet(element, "this rest element is")
	return nil
}

// destructuringAssignment lowers `[a, b] = value` or `({ x, y } = value)` (pattern is the
// left side): value is held in a local, then each target is assigned its part, in order;
// the assignment's value is value.
func (l *lowerer) destructuringAssignment(pattern *ast.Node, valueNode *ast.Node) ir.Expr {
	value := l.expression(valueNode)
	if value == nil {
		return nil
	}
	whole := l.newLocal("destructured", value.Type())
	stores, ok := l.assignParts(pattern, &ir.LocalRef{Local: whole})
	if !ok {
		return nil
	}
	var result ir.Expr = &ir.LocalRef{Local: whole}
	for i := len(stores) - 1; i >= 0; i-- {
		result = &ir.Sequence{X: stores[i], Y: result}
	}
	return &ir.Bind{Local: whole, Value: value, In: result}
}

// assignParts assigns each target of an array or object literal on the left of an
// assignment its part of source.
func (l *lowerer) assignParts(pattern *ast.Node, source ir.Expr) ([]ir.Expr, bool) {
	var stores []ir.Expr
	var elements []*ast.Node
	if pattern.Kind == ast.KindObjectLiteralExpression {
		elements = pattern.AsObjectLiteralExpression().Properties.Nodes
	} else {
		elements = pattern.AsArrayLiteralExpression().Elements.Nodes
	}
	for i, element := range elements {
		var target, initializer, property *ast.Node
		var part ir.Expr
		switch element.Kind {
		case ast.KindOmittedExpression:
			continue
		case ast.KindSpreadElement:
			target = element.Expression()
			part = l.elementPart(source, element, i, nil, true)
		case ast.KindShorthandPropertyAssignment: // `{ x }`, which assigns the variable x
			if part = l.propertyPart(source, element, element.Name(), false); part == nil {
				return nil, false
			}
			store := l.assignShorthand(element, part)
			if store == nil {
				return nil, false
			}
			stores = append(stores, store)
			continue
		case ast.KindPropertyAssignment: // `{ x: target }`
			target, property = element.Initializer(), element.Name()
			if binary := ast.SkipParentheses(target); ast.IsBinaryExpression(binary) && binary.AsBinaryExpression().OperatorToken.Kind == ast.KindEqualsToken {
				target = binary.AsBinaryExpression().Left // a default, which can't apply to a property (D-054)
			}
			part = l.propertyPart(source, element, property, false)
		case ast.KindSpreadAssignment:
			part = l.propertyPart(source, element, nil, true)
		default: // an array literal's element: a target, or `target = default`
			target = element
			if ast.IsBinaryExpression(element) && element.AsBinaryExpression().OperatorToken.Kind == ast.KindEqualsToken {
				target, initializer = element.AsBinaryExpression().Left, element.AsBinaryExpression().Right
			}
			part = l.elementPart(source, element, i, initializer, false)
		}
		if part == nil {
			return nil, false
		}
		store := l.assignPart(target, part)
		if store == nil {
			return nil, false
		}
		stores = append(stores, store)
	}
	return stores, true
}

// assignPart assigns value to a target of a destructuring assignment: a place, or a
// nested array or object literal.
func (l *lowerer) assignPart(target *ast.Node, value ir.Expr) ir.Expr {
	if nested := ast.SkipParentheses(target); nested.Kind == ast.KindArrayLiteralExpression || nested.Kind == ast.KindObjectLiteralExpression {
		inner := l.newLocal("destructured", value.Type())
		stores, ok := l.assignParts(nested, &ir.LocalRef{Local: inner})
		if !ok {
			return nil
		}
		var result ir.Expr = &ir.LocalRef{Local: inner}
		for i := len(stores) - 1; i >= 0; i-- {
			result = &ir.Sequence{X: stores[i], Y: result}
		}
		return &ir.Bind{Local: inner, Value: value, In: result}
	}
	destination, ok := l.assignmentTarget(target)
	if !ok {
		return nil
	}
	if value = l.convert(value, destination.valueType(), target); value == nil {
		return nil
	}
	if destination.local != nil || destination.field != nil || destination.index != nil || destination.arrayLength {
		return destination.store(value, false)
	}
	return l.withObject(destination, func(destination place) ir.Expr { return destination.store(value, false) })
}

// assignShorthand assigns value to the variable a shorthand property `{ x }` of an object
// literal on the left of an assignment names.
func (l *lowerer) assignShorthand(element *ast.Node, value ir.Expr) ir.Expr {
	local, ok := l.locals[l.followAliases(l.checker.GetShorthandAssignmentValueSymbol(element))]
	if !ok {
		l.notSupportedYet(element, "assigning to `"+element.Name().Text()+"` is")
		return nil
	}
	if local = l.ownLocal(element.Name(), local); local == nil {
		return nil
	}
	if value = l.convert(value, local.Type, element); value == nil {
		return nil
	}
	return place{local: local}.store(value, false)
}
