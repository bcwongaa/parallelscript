package lower

import (
	"fmt"
	"math"
	"slices"
	"strings"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/internal/jsnum"
	"github.com/microsoft/typescript-go/internal/scanner"
	"github.com/microsoft/typescript-go/pts/diagnostic"
	"github.com/microsoft/typescript-go/pts/ir"
)

// arrayOf returns the array type of an element type: one per element type.
func (l *lowerer) arrayOf(elem ir.Type) *ir.Array {
	array, ok := l.arrays[elem]
	if !ok {
		array = &ir.Array{Elem: elem}
		l.arrays[elem] = array
	}
	return array
}

// arrayType maps an array type of the checker (T[], Array<T>, readonly T[]) to its IR
// type, or reports why it can't (at node) and returns nil. The elements of an empty
// literal's type (never[]) are dynamic values, which they can't be anyway.
func (l *lowerer) arrayType(t *checker.Type, node *ast.Node) ir.Type {
	elem := l.checker.GetElementTypeOfArrayType(t)
	if elem.Flags()&checker.TypeFlagsNever != 0 {
		return l.arrayOf(ir.Value)
	}
	if l.arraysInProgress[t] {
		if elem, ok := l.recurring(); ok {
			return l.arrayOf(elem) // `type Json = … | Json[]` (D-089)
		}
		l.notSupportedYet(node, fmt.Sprintf("recursive array types (%s) are", l.checker.TypeToString(t)))
		return nil
	}
	l.arraysInProgress[t] = true
	elemType := l.irType(elem, node)
	delete(l.arraysInProgress, t)
	if elemType == nil {
		return nil
	}
	return l.arrayOf(elemType)
}

func isArray(t ir.Type) bool {
	_, ok := t.(*ir.Array)
	return ok
}

// isArrayExpression reports whether tsc types node as an array.
func (l *lowerer) isArrayExpression(node *ast.Node) bool {
	t := l.typeAt(node)
	return (l.isArrayType(t) || isRestTuple(t)) && !l.isNarrowedDynamic(node) // `[number, ...number[]]` is an array (D-100)
}

// isRestTuple reports whether t is a tuple type with a rest element.
func isRestTuple(t *checker.Type) bool {
	return checker.IsTupleType(t) && slices.ContainsFunc(t.TargetTupleType().ElementFlags(), func(f checker.ElementFlags) bool {
		return f&checker.ElementFlagsVariable != 0
	})
}

// arrayLiteral lowers `[a, b, c]`: an array of the literal's contextual type if that is
// an array type, so that `const s: Shape[] = [circle]` holds Shapes, and otherwise of its own.
func (l *lowerer) arrayLiteral(node *ast.Node) ir.Expr {
	t := l.typeAt(node)
	if contextual := l.contextualType(node); contextual != nil && checker.IsTupleType(contextual) {
		t = contextual // [1, "a"] where a [number, string] goes
	}
	if checker.IsTupleType(t) {
		switch made := l.irType(t, node).(type) {
		case *ir.Class:
			return l.tupleLiteral(node, made)
		case *ir.Array:
			return l.arrayLiteralOf(node, made) // a tuple with a rest element (D-100)
		}
		return nil // reported
	}
	if contextual := l.contextualType(node); contextual != nil && l.checker.IsArrayType(contextual) {
		t = contextual
	} else if contextual == nil {
		t = l.literalTarget(node) // `[{ xs: [1] }, { xs: [] }]`'s second `xs`, and the widened `[{ a: 1 }, { b: 2 }]` (D-089)
	}
	if !l.checker.IsArrayType(t) {
		l.notSupportedYet(node, fmt.Sprintf("array literals of type %s are", l.checker.TypeToString(t)))
		return nil
	}
	arrayType := l.arrayType(t, node)
	if arrayType == nil {
		return nil
	}
	return l.arrayLiteralOf(node, arrayType.(*ir.Array))
}

// arrayLiteralOf lowers an array literal to a new array of type array, or reports why it
// can't and returns nil.
func (l *lowerer) arrayLiteralOf(node *ast.Node, array *ir.Array) ir.Expr {
	elements := node.AsArrayLiteralExpression().Elements.Nodes
	if slices.ContainsFunc(elements, isSpreadElement) {
		return l.spreadArray(elements, array)
	}
	elems := make([]ir.Expr, len(elements))
	for i, element := range elements {
		switch element.Kind {
		case ast.KindOmittedExpression:
			l.notSupportedYet(node, "arrays with holes are")
			return nil
		}
		elem := l.expression(element)
		if elem == nil {
			return nil
		}
		if elems[i] = l.convert(elem, array.Elem, element); elems[i] == nil {
			return nil
		}
	}
	return &ir.NewArray{Array: array, Elems: elems}
}

func isSpreadElement(node *ast.Node) bool { return node.Kind == ast.KindSpreadElement }

// spreadArray lowers elements with spreads (`...x`, of an array literal or of push's
// arguments) to a new array of type array (D-057): each element converted to the element
// type, and in place of each spread, the items of an array of that type (holes undefined
// where the element type holds undefined, as iterating reads them, D-183), of a tuple
// (a literal, or a variable read once per element), of a string (its code points) or of
// a map or set (its entries or elements). It reports what it can't lower and returns nil.
func (l *lowerer) spreadArray(elements []*ast.Node, array *ir.Array) ir.Expr {
	var parts []ir.Expr
	for _, element := range elements {
		var values []ir.Expr
		switch {
		case element.Kind == ast.KindOmittedExpression:
			l.notSupportedYet(element, "arrays with holes are")
			return nil
		case element.Kind != ast.KindSpreadElement:
			values = l.argumentValues(element)
		default:
			values = l.spreadParts(element, array)
		}
		if values == nil {
			return nil
		}
		for _, value := range values {
			if s, ok := value.(*spreadValue); ok {
				part := l.spreadPart(s, array, element)
				if part == nil {
					return nil
				}
				parts = append(parts, part)
				continue
			}
			converted := l.convert(value, array.Elem, element)
			if converted == nil {
				return nil
			}
			parts = append(parts, converted)
		}
	}
	return &ir.ArrayCall{Op: ir.ArraySpread, Args: append([]ir.Expr{&ir.NewArray{Array: array}}, parts...)}
}

// spreadPart is the array of type array whose elements a spread (s, at node) adds: an
// array of that type as it is, or a dynamic value's items converted to it with a check
// (D-078). It reports what it can't spread and returns nil.
func (l *lowerer) spreadPart(s *spreadValue, array *ir.Array, node *ast.Node) ir.Expr {
	switch s.x.Type() {
	case array:
		return s.x
	case ir.ValueArray: // a dynamic value's items
		return l.dynamicConversion(box(s.x), array, node)
	}
	if isArray(s.x.Type()) { // `["a", ...numbers]`: a spread only reads its array, so a checked copy of it can't be told apart (D-094)
		return l.dynamicConversion(box(s.x), array, node)
	}
	l.notSupportedYet(node, fmt.Sprintf("spreading a %s into a %s is", s.x.Type(), array))
	return nil
}

// spreadParts lowers a spread element `...x` into an array of type array: as an argument
// is (argumentValues), or for a string or a map or set, a new array of its items.
func (l *lowerer) spreadParts(element *ast.Node, array *ir.Array) []ir.Expr {
	if it, ok := l.mapIterableOf(element.Expression()); ok {
		if it.m == nil {
			return nil // reported
		}
		items := l.mapItemsArray(it, element)
		if items == nil {
			return nil
		}
		return []ir.Expr{&spreadValue{items}}
	}
	if l.typeAt(element.Expression()).Flags()&checker.TypeFlagsStringLike != 0 {
		s := l.expression(element.Expression())
		if s == nil {
			return nil
		}
		return []ir.Expr{&spreadValue{&ir.CallBuiltin{Builtin: ir.ArrayFromString, Args: []ir.Expr{s}}}}
	}
	return l.argumentValues(element)
}

// isArrayLiteral reports whether x makes a new array or tuple of the elements it lists.
func isArrayLiteral(x ir.Expr) bool {
	switch literal := x.(type) {
	case *ir.NewArray:
		return literal.Length == nil
	case *ir.NewObject:
		return literal.Shape.Tuple
	}
	return false
}

// isEmptyArrayLiteral reports whether x is the array of a literal without elements.
func isEmptyArrayLiteral(x ir.Expr) bool {
	literal, ok := x.(*ir.NewArray)
	return ok && len(literal.Elems) == 0
}

// isArrayElement reports whether node is a[i] for an array a.
func (l *lowerer) isArrayElement(node *ast.Node) bool {
	return node.Kind == ast.KindElementAccessExpression && l.isArrayExpression(node.Expression())
}

// arrayElement lowers a[i]: ArrayElement where any value can go, which is undefined
// for an index out of range, and ArrayElementChecked elsewhere, which throws (D-015).
func (l *lowerer) arrayElement(node *ast.Node, op ir.ArrayOp) ir.Expr {
	array, index := l.arrayElementOperands(node)
	if array == nil {
		return nil
	}
	return &ir.ArrayCall{Op: op, Args: []ir.Expr{array, index}}
}

// arrayElementOperands lowers the array and the index of a[i], or reports the access and returns nils.
func (l *lowerer) arrayElementOperands(node *ast.Node) (ir.Expr, ir.Expr) {
	if l.isUnloweredOptionalChain(node) {
		l.notSupportedYet(node, "optional chaining `?.` is")
		return nil, nil
	}
	access := node.AsElementAccessExpression()
	array := l.expression(access.Expression)
	index := l.expression(access.ArgumentExpression)
	if array == nil || index == nil {
		return nil, nil
	}
	if !ir.IsNumeric(index.Type()) {
		l.notSupportedYet(access.ArgumentExpression, fmt.Sprintf("indexing an array with a %s is", index.Type()))
		return nil, nil
	}
	if index = l.convert(index, ir.Number, access.ArgumentExpression); index == nil {
		return nil, nil
	}
	return array, index
}

// arrayProperty lowers `a.length`, the one property of arrays (methods are calls).
func (l *lowerer) arrayProperty(node *ast.Node) ir.Expr {
	if name := node.AsPropertyAccessExpression().Name().Text(); name != "length" {
		l.notSupportedYet(node, fmt.Sprintf("the array property `%s` is", name))
		return nil
	}
	array := l.arrayObject(node)
	if array == nil {
		return nil
	}
	return &ir.ArrayCall{Op: ir.ArrayLength, Args: []ir.Expr{array}}
}

// arrayObject lowers the array of `a.p` or `a.m(…)`, or reports why it can't and returns nil.
func (l *lowerer) arrayObject(access *ast.Node) ir.Expr {
	if l.isUnloweredOptionalChain(access) {
		l.notSupportedYet(access, "optional chaining `?.` is")
		return nil
	}
	x := l.expression(access.Expression())
	if x != nil && isTuple(x.Type()) {
		return l.tupleArray(x, l.typeAt(access.Expression()), access.Expression()) // `pair.map(f)`
	}
	return x
}

// arrayRemovers are the array methods that return an element or undefined: the dynamic
// op, then the one a `!` after the call asks for.
var arrayRemovers = map[string][2]ir.ArrayOp{"pop": {ir.ArrayPop, ir.ArrayPopChecked}, "shift": {ir.ArrayShift, ir.ArrayShiftChecked}}

// arrayParam is a parameter of an array method: its type (nil for the element type) and
// the argument a call that leaves it out gets (nil if it can't be left out).
type arrayParam struct {
	t       ir.Type
	leftOut ir.Expr
}

var (
	elementParam = arrayParam{}
	startParam   = arrayParam{ir.Number, fromStart}
	endParam     = arrayParam{ir.Number, toEnd}
)

// arrayMethods are the array methods with fixed parameters, and their ops.
var arrayMethods = map[string]struct {
	op     ir.ArrayOp
	params []arrayParam
}{
	"slice":       {ir.ArraySlice, []arrayParam{startParam, endParam}},
	"indexOf":     {ir.ArrayIndexOf, []arrayParam{elementParam, startParam}},
	"lastIndexOf": {ir.ArrayLastIndexOf, []arrayParam{elementParam, endParam}},
	"includes":    {ir.ArrayIncludes, []arrayParam{elementParam, startParam}},
	"join":        {ir.ArrayJoin, []arrayParam{{ir.String, &ir.StringConst{Value: ","}}}},
	"toString":    {ir.ArrayJoin, []arrayParam{{ir.String, &ir.StringConst{Value: ","}}}}, // tsc passes it nothing
	"reverse":     {ir.ArrayReverse, nil},
	"sort":        {ir.ArraySort, nil},
	"fill":        {ir.ArrayFill, []arrayParam{elementParam, startParam, endParam}},
	"at":          {ir.ArrayAt, []arrayParam{startParam}},
}

// arrayMethodCall lowers `a.m(args)` for an array a. checked is true under `!`
// (`stack.pop()!`), where pop and shift return the element itself and throw if a is empty.
func (l *lowerer) arrayMethodCall(call *ast.CallExpression, checked bool) ir.Expr {
	callee := ast.SkipParentheses(call.Expression)
	name := callee.AsPropertyAccessExpression().Name().Text()
	method, fixed := arrayMethods[name]
	removers, removes := arrayRemovers[name]
	sortWith := name == "sort" && len(call.Arguments.Nodes) > 0
	if l.isArrayIteration(call.AsNode()) {
		return l.arrayIterator(call)
	}
	if !fixed && !removes && !callbackMethods[name] && !copyingMethods[name] && name != "push" && name != "unshift" && name != "splice" && name != "concat" && name != "flat" {
		l.notSupportedYet(callee, fmt.Sprintf("the array method `%s` is", name))
		return nil
	}
	for _, argument := range call.Arguments.Nodes {
		if argument.Kind == ast.KindSpreadElement && name != "push" && name != "unshift" {
			l.notSupportedYet(argument, fmt.Sprintf("spread arguments of `%s` are", name))
			return nil
		}
	}
	array := l.arrayObject(callee)
	if array == nil {
		return nil
	}
	if array.Type() == ir.Value { // an unknown that Array.isArray narrowed, or a recursive type's array (D-089)
		t := l.irType(l.typeAt(call.AsNode()), call.AsNode())
		result := l.dynamicMethodCall(call, callee, array)
		if t == nil || result == nil || t == ir.Void || t == ir.Value {
			return result
		}
		return l.checkedConversion(result, t, call.AsNode())
	}
	arrayType := array.Type().(*ir.Array)
	args := call.Arguments.Nodes
	switch {
	case callbackMethods[name]:
		return l.callbackMethodCall(call, name, array, checked)
	case copyingMethods[name]:
		return l.copyingMethodCall(call, name, array)
	case sortWith:
		return l.sortWith(array, args[0], l.arraySortSteps(arrayType, false))
	case removes:
		op := removers[0]
		if checked {
			op = removers[1]
		}
		return &ir.ArrayCall{Op: op, Args: []ir.Expr{array}}
	case fixed:
		operands := l.arrayOperands(args, method.params, arrayType)
		if operands == nil {
			return nil
		}
		return &ir.ArrayCall{Op: method.op, Args: append([]ir.Expr{array}, operands...)}
	case (name == "push" || name == "unshift") && slices.ContainsFunc(args, isSpreadElement):
		values := l.spreadArray(args, arrayType) // a new array, of the values in order
		if values == nil {
			return nil
		}
		op := ir.ArrayPushAll
		if name == "unshift" {
			op = ir.ArrayUnshiftAll
		}
		return &ir.ArrayCall{Op: op, Args: []ir.Expr{array, values}}
	case name == "push" || name == "unshift":
		values := l.arrayValues(args, arrayType, false)
		if values == nil {
			return nil
		}
		op := ir.ArrayPush
		if name == "unshift" {
			op = ir.ArrayUnshift
		}
		return &ir.ArrayCall{Op: op, Args: append([]ir.Expr{array}, values...)}
	case name == "splice":
		return l.splice(array, args)
	case name == "concat":
		values := l.arrayValues(args, arrayType, true)
		if values == nil {
			return nil
		}
		return &ir.ArrayCall{Op: ir.ArrayConcat, Args: append([]ir.Expr{array}, values...)}
	}
	return l.flat(call, array, args)
}

// sortSteps are how a receiver of one kind is sorted: by a comparator of type comparator,
// in the default order, and, for toSorted, on a copy of it.
type sortSteps struct {
	comparator   *ir.FuncType
	copy         func(receiver ir.Expr) ir.Expr // nil to sort the receiver itself
	byDefault    func(receiver ir.Expr) ir.Expr
	byComparator func(receiver, compare ir.Expr) ir.Expr
}

// arraySortSteps are an array's: Array's sort, with or without a comparator.
func (l *lowerer) arraySortSteps(array *ir.Array, copied bool) sortSteps {
	steps := sortSteps{
		comparator: l.funcType([]ir.Type{array.Elem, array.Elem}, ir.Number),
		byDefault:  func(receiver ir.Expr) ir.Expr { return &ir.ArrayCall{Op: ir.ArraySort, Args: []ir.Expr{receiver}} },
		byComparator: func(receiver, compare ir.Expr) ir.Expr {
			return &ir.ArrayCall{Op: ir.ArraySortWith, Args: []ir.Expr{receiver, compare}}
		},
	}
	if copied {
		steps.copy = func(receiver ir.Expr) ir.Expr { return &ir.ArrayCall{Op: ir.ArrayCopy, Args: []ir.Expr{receiver}} }
	}
	return steps
}

// sortWith lowers receiver.sort(compare), or toSorted's, by steps (SortCompare): the
// receiver is evaluated, then compare, and only then copied. An undefined compare is the
// default order, told at run time where compare may be undefined (`compare?: …`, a
// dynamic value); any other is converted to steps.comparator.
func (l *lowerer) sortWith(receiver ir.Expr, arg *ast.Node, steps sortSteps) ir.Expr {
	compare := l.expression(arg)
	if compare == nil {
		return nil
	}
	if _, ok := compare.(*ir.UndefinedConst); ok {
		return steps.byDefault(withCopy(receiver, steps))
	}
	t := compare.Type()
	u, _ := t.(*ir.Union)
	mayBeUndefined := t == ir.Value || u != nil && u.Has(ir.Undefined)
	if t != ir.Undefined && !mayBeUndefined && steps.copy == nil {
		if compare = l.convert(compare, steps.comparator, arg); compare == nil {
			return nil
		}
		return steps.byComparator(receiver, compare)
	}
	array := l.newLocal("array", receiver.Type())
	target := func() ir.Expr { return withCopy(&ir.LocalRef{Local: array}, steps) }
	if t == ir.Undefined {
		return &ir.Bind{Local: array, Value: receiver, In: discard(compare, steps.byDefault(target()))}
	}
	value := l.newLocal("compare", t)
	given := ir.Expr(&ir.LocalRef{Local: value})
	if u != nil && u.Has(ir.Undefined) {
		given = fromUnion(given, l.withoutUndefined(u))
	}
	if given = l.convert(given, steps.comparator, arg); given == nil {
		return nil
	}
	sorted := steps.byComparator(target(), given)
	if mayBeUndefined {
		sorted = &ir.Conditional{Cond: &ir.IsNullish{X: &ir.LocalRef{Local: value}, Only: ir.Undefined}, Then: steps.byDefault(target()), Else: sorted}
	}
	return &ir.Bind{Local: array, Value: receiver, In: &ir.Bind{Local: value, Value: compare, In: sorted}}
}

// withCopy is receiver, or for toSorted's steps a copy of it.
func withCopy(receiver ir.Expr, steps sortSteps) ir.Expr {
	if steps.copy == nil {
		return receiver
	}
	return steps.copy(receiver)
}

// arrayOperands lowers the arguments of an array method with fixed parameters, giving
// left-out ones their defaults, or reports one and returns nil.
func (l *lowerer) arrayOperands(args []*ast.Node, params []arrayParam, array *ir.Array) []ir.Expr {
	operands := make([]ir.Expr, len(params))
	for i, param := range params {
		t := param.t
		if t == nil {
			t = array.Elem
		}
		if i >= len(args) || isUndefined(args[i]) && param.leftOut != nil {
			operands[i] = param.leftOut // tsc checked that required arguments are there
			continue
		}
		value := l.expression(args[i])
		if value == nil {
			return nil
		}
		if param.leftOut != nil {
			value = l.orLeftOut(value, param.leftOut)
		}
		if operands[i] = l.convert(value, t, args[i]); operands[i] == nil {
			return nil
		}
	}
	return operands
}

// orLeftOut is value, an argument of a library method, or leftOut, what the method takes
// for an argument left out, where value is undefined at run time (`a.slice(i, n ??
// undefined)`, D-099): value itself unless it is of a union of one type and undefined.
func (l *lowerer) orLeftOut(value ir.Expr, leftOut ir.Expr) ir.Expr {
	u, ok := value.Type().(*ir.Union)
	if !ok || len(u.Members) != 2 || !u.Has(ir.Undefined) {
		return value
	}
	other := u.Members[0]
	if other == ir.Undefined {
		other = u.Members[1]
	}
	tmp := l.newLocal("argument", u)
	ref := &ir.LocalRef{Local: tmp}
	converted := l.convert(leftOut, other, nil)
	if converted == nil {
		return value
	}
	return &ir.Bind{Local: tmp, Value: value, In: &ir.Conditional{Cond: &ir.IsNullish{X: ref}, Then: converted, Else: fromUnion(ref, other)}}
}

// isUndefined reports whether node is the identifier undefined, which a left-out argument is.
func isUndefined(node *ast.Node) bool {
	return node.Kind == ast.KindIdentifier && node.Text() == "undefined"
}

// arrayValues lowers arguments that are values of an array's element type, or with
// arrays true, also arrays of its type (concat's).
func (l *lowerer) arrayValues(args []*ast.Node, array *ir.Array, arrays bool) []ir.Expr {
	values := make([]ir.Expr, len(args))
	for i, arg := range args {
		var value ir.Expr
		if literal := ast.SkipParentheses(arg); arrays && literal.Kind == ast.KindArrayLiteralExpression {
			value = l.arrayLiteralOf(literal, array) // tsc types [7] in a.concat([7]) number[], whatever a's elements are
		} else {
			value = l.expression(arg)
		}
		if value == nil {
			return nil
		}
		if arrays && value.Type() == array {
			values[i] = value
			continue
		}
		if arrays && isArray(value.Type()) && value.Type() != array.Elem && !isArray(array.Elem) {
			// `a.concat(new Array(n).fill(0))`: an array of other elements, copied as concat reads it (D-094)
			if values[i] = l.convert(value, array, arg); values[i] == nil {
				return nil
			}
			continue
		}
		if values[i] = l.convert(value, array.Elem, arg); values[i] == nil {
			return nil
		}
	}
	return values
}

// splice lowers a.splice(start, deleteCount, items…). Without arguments it removes
// nothing; without a delete count it removes everything from start on.
func (l *lowerer) splice(array ir.Expr, args []*ast.Node) ir.Expr {
	arrayType := array.Type().(*ir.Array)
	if len(args) == 0 {
		return &ir.ArrayCall{Op: ir.ArraySplice, Args: []ir.Expr{array, &ir.NumberConst{Value: 0}, &ir.NumberConst{Value: 0}}}
	}
	bounds := l.arrayOperands(args[:min(len(args), 2)], []arrayParam{startParam, endParam}, arrayType)
	items := l.arrayValues(args[min(len(args), 2):], arrayType, false)
	if bounds == nil || items == nil {
		return nil
	}
	return &ir.ArrayCall{Op: ir.ArraySplice, Args: append(append([]ir.Expr{array}, bounds...), items...)}
}

// flat lowers a.flat(depth) (depth 1 if left out): a new array of tsc's type for the call,
// with the arrays and tuples depth levels down spread into it. The runtime tells them by
// their descriptors, so elements of union, tuple and dynamic types flatten as JS's do (D-180).
func (l *lowerer) flat(call *ast.CallExpression, array ir.Expr, args []*ast.Node) ir.Expr {
	result := l.flattenedResult(call, "flat")
	if result == nil {
		return nil
	}
	depth := ir.Expr(&ir.NumberConst{Value: 1})
	if len(args) > 0 {
		if depth = l.flatDepth(args[0]); depth == nil {
			return nil
		}
	}
	return &ir.ArrayCall{Op: ir.ArrayFlat, Args: []ir.Expr{array, depth}, To: result}
}

// flattenedResult is tsc's type for a flat or flatMap call, the array it makes, without the
// undefined of an optional chain (`xs?.flat()`), whose lowering adds that.
func (l *lowerer) flattenedResult(call *ast.CallExpression, method string) *ir.Array {
	node := call.AsNode()
	t := l.irType(l.checker.GetNonNullableType(l.typeAt(node)), node)
	if t == nil {
		return nil // reported
	}
	result, ok := t.(*ir.Array)
	if !ok {
		l.notSupportedYet(node, fmt.Sprintf("`%s` whose result has the type %s is", method, t))
		return nil
	}
	l.prepareDynamicTarget(result.Elem, node, map[ir.Type]bool{}) // dynamic data stored in it is copied into its interfaces' shapes (D-071); what can't take any is a TypeError there
	return result
}

// spreadsOtherElements reports, and reports whether, the callback of the flatMap call may
// return an array or a tuple with elements that aren't of the call's U: JS's flatMap spreads
// it into the result, which tsc types as a U[] all the same (`xs.flatMap<number[]>((x) =>
// [x])` holds numbers), so typed code would read its elements as what they aren't.
func (l *lowerer) spreadsOtherElements(call *ast.CallExpression) bool {
	result := l.checker.GetNonNullableType(l.typeAt(call.AsNode()))
	callback := call.Arguments.Nodes[0]
	signatures := l.checker.GetSignaturesOfType(l.typeAt(callback), checker.SignatureKindCall)
	if !l.checker.IsArrayType(result) || len(signatures) != 1 || len(signatures[0].TypeParameters()) > 0 {
		return false // a generic function's result type is its type parameters' until the call instantiates it; the runtime checks its leaves
	}
	elem := l.checker.GetElementTypeOfArrayType(result)
	returns := l.returnTypeOf(signatures[0])
	members := []*checker.Type{returns}
	if returns.Flags()&checker.TypeFlagsUnion != 0 {
		members = returns.Types()
	}
	for _, member := range members {
		if slices.ContainsFunc(l.elementTypesOf(member), func(e *checker.Type) bool { return !l.checker.IsTypeAssignableTo(e, elem) }) {
			l.report(diagnostic.At(callback, diagnostic.NotSupportedYet, "`flatMap` with a callback that returns a %s is not supported yet: JS's flatMap spreads it into the result, which TypeScript types as a %s as if it didn't; return it in an array to keep it whole", l.checker.TypeToString(member), l.checker.TypeToString(result)))
			return true
		}
	}
	return false
}

// keepsLeaves reports, and reports whether, every typed leaf that flatMap may store from the
// values of callback, of type t, stays the value it is where the result's element type elem
// goes (keepsLeaf). An annotated callback's values, and a named function's, are of its result
// type, converted where it returns them; otherwise the leaves are found in the values it
// returns (keepsLeavesOf).
func (l *lowerer) keepsLeaves(callback *ast.Node, t, elem ir.Type) bool {
	if !ast.IsFunctionExpressionOrArrowFunction(callback) || callback.Type() != nil {
		return l.keepsLeafTypes(t, elem, callback)
	}
	return l.keepsReturnedValues(callback, func(value *ast.Node) bool { return l.keepsLeavesOf(value, elem, true, callback) })
}

// keepsReturnedValues reports whether keeps holds for each value fn, a function expression or
// an arrow function, returns.
func (l *lowerer) keepsReturnedValues(fn *ast.Node, keeps func(value *ast.Node) bool) bool {
	body := fn.Body()
	if body.Kind != ast.KindBlock {
		return keeps(body)
	}
	kept := true
	ast.ForEachReturnStatement(body, func(stmt *ast.Node) bool {
		if value := stmt.Expression(); value != nil && !keeps(value) {
			kept = false
		}
		return !kept
	})
	return kept
}

// keepsLeavesOf is keepsLeaves for the leaves flatMap takes from value, a value fn returns or
// one whose elements are such a value's: an array's or a tuple's elements, or value itself,
// each stored where elem goes. made says whether value's literals are made as the context of
// fn's values gives them. The elements of an array literal are its elements, of a map's or
// Array.from's with a function the values the function returns, of a sorted, reversed,
// filtered or sliced array its receiver's, of a concat its receiver's and its arguments', of
// Array.of its arguments, of a flatMap with a function the leaves of the values the function
// returns, of a literal's flat() its elements' leaves, and of a const fn makes at each call
// (constMadeIn) its initializer's, unless the const is annotated: its elements are converted
// to its type where it is declared, so they are checked by that type (an `any[]`'s are held
// as they are, and copied where flatMap converts dynamic data, D-071; H05 review round 12).
func (l *lowerer) keepsLeavesOf(value *ast.Node, elem ir.Type, made bool, fn *ast.Node) bool {
	value = ast.SkipParentheses(value)
	switch value.Kind {
	case ast.KindArrayLiteralExpression:
		return l.keepsElements(value, elem, made, fn)
	case ast.KindAsExpression, ast.KindSatisfiesExpression:
		if literal := castLiteral(value); literal != nil {
			return l.keepsElements(literal, elem, false, fn) // made as the cast's type
		}
	case ast.KindConditionalExpression:
		branches := value.AsConditionalExpression()
		return l.keepsLeavesOf(branches.WhenTrue, elem, made, fn) && l.keepsLeavesOf(branches.WhenFalse, elem, made, fn)
	case ast.KindObjectLiteralExpression:
		return l.keepsValue(value, elem, made, fn) // a leaf itself
	case ast.KindIdentifier:
		if initializer := l.constMadeIn(value, fn); initializer != nil && initializer.Parent.Type() == nil {
			return l.keepsLeavesOf(initializer, elem, false, fn) // an annotated one's elements are converted to its type where it is declared
		}
	case ast.KindCallExpression:
		if mapper := l.arrayMapper(value); mapper != nil {
			return l.keepsReturnedValues(mapper, func(mapped *ast.Node) bool { return l.keepsValue(mapped, elem, false, mapper) })
		}
		if receiver := l.sameElementsReceiver(value); receiver != nil {
			return l.keepsLeavesOf(receiver, elem, false, fn)
		}
		if parts := l.concatParts(value); parts != nil {
			return !slices.ContainsFunc(parts, func(part *ast.Node) bool { return !l.keepsLeavesOf(part, elem, false, fn) })
		}
		if callee := ast.SkipParentheses(value.AsCallExpression().Expression); callee.Kind == ast.KindPropertyAccessExpression && l.libMemberName(callee) == "ArrayConstructor.of" {
			return !slices.ContainsFunc(value.Arguments(), func(item *ast.Node) bool { return !l.keepsValue(item, elem, false, fn) })
		}
		if inner := l.flatMapper(value); inner != nil {
			return l.keepsReturnedValues(inner, func(mapped *ast.Node) bool { return l.keepsLeavesOf(mapped, elem, false, inner) })
		}
		if literal := l.flattenedLiteral(value); literal != nil {
			return !slices.ContainsFunc(literal.AsArrayLiteralExpression().Elements.Nodes, func(item *ast.Node) bool { return !l.keepsLeavesOf(item, elem, false, fn) })
		}
	}
	t := l.irType(l.typeAt(value), value)
	at := value
	if returns(fn, value) {
		at = fn // a callback's own values are reported where the callback is
	}
	return t == nil || l.keepsLeafTypes(t, elem, at)
}

// returns reports whether value is a value fn returns: its expression body, a return
// statement's expression, or a branch of a conditional that is one.
func returns(fn, value *ast.Node) bool {
	for value.Parent.Kind == ast.KindParenthesizedExpression || value.Parent.Kind == ast.KindConditionalExpression && value != value.Parent.AsConditionalExpression().Condition {
		value = value.Parent
	}
	return value.Parent == fn || value.Parent.Kind == ast.KindReturnStatement && ast.GetContainingFunction(value.Parent) == fn
}

// concatParts is the receiver and the arguments of call if it is an array's concat, whose
// result holds the receiver's elements and each argument's, or the argument itself if it
// isn't an array; nil otherwise.
func (l *lowerer) concatParts(call *ast.Node) []*ast.Node {
	callee := ast.SkipParentheses(call.AsCallExpression().Expression)
	if !l.isArrayMethodCall(call) || callee.AsPropertyAccessExpression().Name().Text() != "concat" {
		return nil
	}
	return append([]*ast.Node{callee.Expression()}, call.Arguments()...)
}

// keepsElements is keepsLeavesOf for the elements of an array literal, each stored where
// elem goes, made as elem's values if made: a spread's are an array's or a tuple's leaves.
func (l *lowerer) keepsElements(literal *ast.Node, elem ir.Type, made bool, fn *ast.Node) bool {
	for _, element := range literal.AsArrayLiteralExpression().Elements.Nodes {
		switch element.Kind {
		case ast.KindOmittedExpression:
			continue // a hole
		case ast.KindSpreadElement:
			if !l.keepsSpread(element, literal, elem, fn) {
				return false
			}
			continue
		}
		if !l.keepsValue(element, elem, made, fn) {
			return false
		}
	}
	return true
}

// keepsSpread is keepsElements for spread, an element of literal: an array's or a tuple's
// leaves, or an iterable's values, which are of the literal's element type.
func (l *lowerer) keepsSpread(spread, literal *ast.Node, elem ir.Type, fn *ast.Node) bool {
	if t := l.irType(l.typeAt(spread.Expression()), spread); isArray(t) || isTuple(t) {
		return l.keepsLeavesOf(spread.Expression(), elem, false, fn)
	}
	if array, ok := l.irType(l.typeAt(literal), literal).(*ir.Array); ok {
		return l.keepsLeaf(array.Elem, elem, false, spread)
	}
	return true
}

// keepsTupleElements is keepsElements for an array literal made as the tuple type tuple, each
// element stored in the field at its position. A spread of a tuple stores its elements in the
// fields from its position on; after a spread of an array, or of a tuple with optional
// elements, an element may be stored in any later field.
func (l *lowerer) keepsTupleElements(literal *ast.Node, tuple *ir.Class, made bool, fn *ast.Node) bool {
	position, exact := 0, true
	keeps := func(check func(field ir.Type) bool) bool {
		last := len(tuple.Fields)
		if exact {
			last = min(position+1, last)
		}
		for _, field := range tuple.Fields[min(position, last):last] {
			if !check(field.Type) {
				return false
			}
		}
		return true
	}
	for _, element := range literal.AsArrayLiteralExpression().Elements.Nodes {
		if element.Kind != ast.KindSpreadElement {
			if !keeps(func(field ir.Type) bool { return l.keepsValue(element, field, made, fn) }) {
				return false
			}
			position++
			continue
		}
		spread := l.irType(l.typeAt(element.Expression()), element)
		if array, ok := spread.(*ir.Array); ok {
			exact = false
			if !keeps(func(field ir.Type) bool { return l.keepsLeaf(array.Elem, field, false, element) }) {
				return false
			}
			continue
		}
		spreadTuple, ok := spread.(*ir.Class)
		if !ok || !spreadTuple.Tuple {
			exact = false
			if !keeps(func(field ir.Type) bool { return l.keepsSpread(element, literal, field, fn) }) {
				return false
			}
			continue
		}
		for _, f := range spreadTuple.Fields {
			if !keeps(func(field ir.Type) bool { return l.keepsLeaf(f.Type, field, false, element) }) {
				return false
			}
			if f.Optional {
				exact = false
			} else {
				position++
			}
		}
	}
	return true
}

// keepsValue is keepsLeaf for the value of node, stored where target goes; made says whether
// a literal there is made as target's value. A literal made so is target's value, an array
// literal's elements stored where target's go; a new array or tuple of another type is copied
// into target's, its elements stored where target's go (keepsLeavesOf), and another object
// literal is checked by the type it is made as (keepsObjectLiteral).
func (l *lowerer) keepsValue(node *ast.Node, target ir.Type, made bool, fn *ast.Node) bool {
	node = ast.SkipParentheses(node)
	switch node.Kind {
	case ast.KindConditionalExpression:
		branches := node.AsConditionalExpression()
		return l.keepsValue(branches.WhenTrue, target, made, fn) && l.keepsValue(branches.WhenFalse, target, made, fn)
	case ast.KindObjectLiteralExpression:
		if made {
			return true // its properties converted where it is made
		}
		return l.keepsObjectLiteral(node, target, fn)
	case ast.KindArrayLiteralExpression:
		if made {
			switch to := l.nestedLiteralTarget(target, node).(type) {
			case *ir.Array:
				return l.keepsElements(node, to.Elem, true, fn)
			case *ir.Class:
				return l.keepsTupleElements(node, to, true, fn)
			}
			return true
		}
	}
	t := l.irType(l.typeAt(node), node)
	if t == nil {
		return true
	}
	isNew := l.isNewArrayIn(node, fn)
	if to := l.arrayTarget(t, target); isNew && to != nil && t != to && !isDynamicArray(t) {
		switch {
		case isTuple(t) && isArray(to):
			l.notSupportedYet(node, fmt.Sprintf("using a `%s` as a `%s` is", t, to)) // the runtime converts arrays to arrays only
			return false
		case isArray(t) && isArray(to):
			return l.keepsLeavesOf(node, to.(*ir.Array).Elem, false, fn) // the copy holds its elements
		case isTuple(t) && isTuple(to):
			if literal := castLiteral(node); literal != nil {
				return l.keepsTupleElements(literal, to.(*ir.Class), false, fn)
			}
		}
	}
	return l.keepsLeaf(t, target, isNew, node)
}

// arrayTarget is the array or tuple type that a value of the array or tuple type t stored
// where target goes is converted to: target, or the union member memberFor finds; nil if t
// isn't an array or a tuple type or there is none.
func (l *lowerer) arrayTarget(t, target ir.Type) ir.Type {
	if !isArray(t) && !isTuple(t) {
		return nil
	}
	if u, ok := target.(*ir.Union); ok {
		if u.Has(t) {
			return t
		}
		target = l.memberFor(t, u)
	}
	if isArray(target) || isTuple(target) {
		return target
	}
	return nil
}

// keepsObjectLiteral is keepsValue for an object literal not made as target's value. It is
// made as the type its own context gives it (recordContext, literalType): one that is
// target's, or a member of it, stays the object it is, its properties converted where it
// is made. A new object going where a hash-map object goes is copied into one, its values
// stored where that one's go, unless the type it is made as has optional properties or it
// has a spread, a method or an accessor. Another is checked by the type it is made as, as
// any leaf is: a copy into an interface has the interface's shape, which a new object's
// extra, misordered or left-out properties, or its hash maps, maps and sets, wouldn't
// survive as they are (H05 review rounds 8-10).
func (l *lowerer) keepsObjectLiteral(literal *ast.Node, target ir.Type, fn *ast.Node) bool {
	made := l.recordContext(literal)
	if made == nil {
		made = l.literalType(literal)
	}
	t := l.irType(made, literal)
	if t == nil || t == target {
		return true
	}
	to := target
	if u, ok := target.(*ir.Union); ok {
		if u.Has(t) {
			return true
		}
		if to = l.memberFor(t, u); to == nil {
			to = onlyRecordMember(u)
		}
	}
	record, ok := to.(*ir.MapType)
	if !ok || !record.Record {
		return l.keepsLeaf(t, target, false, literal)
	}
	if from, ok := t.(*ir.MapType); ok && from.Record {
		return l.keepsLeaf(from.Value, record.Value, false, literal)
	}
	if hasOptionalFields(t) {
		return l.keepsLeaf(t, target, false, literal) // made as a type with properties it may leave out
	}
	for _, property := range literal.AsObjectLiteralExpression().Properties.Nodes {
		var value *ast.Node
		switch property.Kind {
		case ast.KindPropertyAssignment:
			value = property.Initializer()
		case ast.KindShorthandPropertyAssignment:
			value = property.Name()
		default:
			return l.keepsLeaf(t, target, false, literal) // a spread, a method or an accessor
		}
		if !l.keepsValue(value, record.Value, false, fn) {
			return false
		}
	}
	return true
}

// onlyRecordMember is u's one hash-map object member, or nil if it has none or several.
func onlyRecordMember(u *ir.Union) ir.Type {
	var record ir.Type
	for _, member := range u.Members {
		if isRecord(member) {
			if record != nil {
				return nil
			}
			record = member
		}
	}
	return record
}

// hasOptionalFields reports whether t is an object type with optional properties.
func hasOptionalFields(t ir.Type) bool {
	var fields []*ir.Field
	switch t := t.(type) {
	case *ir.Class:
		fields = t.Fields
	case *ir.Interface:
		fields = t.Fields
	}
	return slices.ContainsFunc(fields, func(field *ir.Field) bool { return field.Optional })
}

// castLiteral is the array literal node is, under parentheses and `as` and `satisfies` casts
// (`[c, 1] as const`), or nil if it isn't one.
func castLiteral(node *ast.Node) *ast.Node {
	for {
		switch node = ast.SkipParentheses(node); node.Kind {
		case ast.KindArrayLiteralExpression:
			return node
		case ast.KindAsExpression, ast.KindSatisfiesExpression:
			node = node.Expression()
		default:
			return nil
		}
	}
}

// arrayMapper is the function expression or arrow function that call, `xs.map(f)` or
// `Array.from(xs, f)`, makes its array's elements with, or nil if it isn't such a call.
func (l *lowerer) arrayMapper(call *ast.Node) *ast.Node {
	args := call.Arguments()
	callee := ast.SkipParentheses(call.AsCallExpression().Expression)
	var mapper *ast.Node
	switch {
	case l.isArrayMethodCall(call) && callee.AsPropertyAccessExpression().Name().Text() == "map" && len(args) > 0:
		mapper = args[0]
	case l.isArrayStatic(callee, "from") && len(args) > 1:
		mapper = args[1]
	}
	if mapper == nil || !ast.IsFunctionExpressionOrArrowFunction(mapper) {
		return nil
	}
	return mapper
}

// flatMapper is the function expression or arrow function of call, `xs.flatMap(f)`, whose
// values' leaves its array holds, or nil if it isn't such a call.
func (l *lowerer) flatMapper(call *ast.Node) *ast.Node {
	args := call.Arguments()
	callee := ast.SkipParentheses(call.AsCallExpression().Expression)
	if !l.isArrayMethodCall(call) || callee.AsPropertyAccessExpression().Name().Text() != "flatMap" || len(args) == 0 || !ast.IsFunctionExpressionOrArrowFunction(args[0]) {
		return nil
	}
	return args[0]
}

// flattenedLiteral is the array literal that call, `[…].flat()`, flattens one level, whose
// elements' leaves its array holds, or nil if it isn't such a call (a depth, a spread).
func (l *lowerer) flattenedLiteral(call *ast.Node) *ast.Node {
	callee := ast.SkipParentheses(call.AsCallExpression().Expression)
	if !l.isArrayMethodCall(call) || callee.AsPropertyAccessExpression().Name().Text() != "flat" || len(call.Arguments()) > 0 {
		return nil
	}
	literal := castLiteral(callee.Expression())
	if literal == nil || slices.ContainsFunc(literal.AsArrayLiteralExpression().Elements.Nodes, func(item *ast.Node) bool { return item.Kind == ast.KindSpreadElement }) {
		return nil
	}
	return literal
}

// sameElementsMethods are the methods of arrays whose result holds elements of the array
// they are called on only.
var sameElementsMethods = map[string]bool{
	"sort": true, "reverse": true, "filter": true, "slice": true, "toSorted": true, "toReversed": true,
}

// sameElementsReceiver is the array that call, a sameElementsMethods call whose result is of
// its type (not a type guard's narrower one), is made on, or nil if it isn't such a call.
func (l *lowerer) sameElementsReceiver(call *ast.Node) *ast.Node {
	if !l.isArrayMethodCall(call) {
		return nil
	}
	callee := ast.SkipParentheses(call.AsCallExpression().Expression).AsPropertyAccessExpression()
	if !sameElementsMethods[callee.Name().Text()] || l.typeAt(call) != l.typeAt(callee.Expression) {
		return nil
	}
	return callee.Expression
}

// nestedLiteralTarget is the array or tuple type that elem gives an array literal stored
// where it goes: elem, or the union member memberFor finds for the literal's type; nil if neither.
func (l *lowerer) nestedLiteralTarget(elem ir.Type, literal *ast.Node) ir.Type {
	if u, ok := elem.(*ir.Union); ok {
		t := l.irType(l.typeAt(literal), literal)
		if t == nil {
			return nil
		}
		elem = l.memberFor(t, u)
	}
	if isArray(elem) || isTuple(elem) {
		return elem
	}
	return nil
}

// keepsLeafTypes is keepsLeaves for values of type t that aren't array literals, none of
// whose arrays is known to be new.
func (l *lowerer) keepsLeafTypes(t, elem ir.Type, node *ast.Node) bool {
	for _, leaf := range leafTypes(t) {
		if !l.keepsLeaf(leaf, elem, false, node) {
			return false
		}
	}
	return true
}

// keepsLeaf reports, and reports at node whether, a leaf of type leaf stored where elem goes
// stays the value it is. The runtime converts numbers, adapts functions (D-075) and copies
// dynamic data (D-071), but would copy a typed object into an interface whose members it
// doesn't provide as they are or into a union of interfaces none of which it provides, an
// instance into a class it isn't one of, an array into an array or tuple of other
// elements, or a hash-map object into one of other values or an object into one, which JS
// doesn't: reported as the same conversions elsewhere report them (`const js: J[] = [c]`,
// D-094); a map or a set of other types isn't converted at all. A new array (made) is
// copied as it is made, but its elements are stored where elem's go. Each member of a union
// leaf is such a leaf.
func (l *lowerer) keepsLeaf(leaf, elem ir.Type, made bool, node *ast.Node) bool {
	if u, ok := leaf.(*ir.Union); ok {
		return !slices.ContainsFunc(u.Members, func(member ir.Type) bool { return !l.keepsLeaf(member, elem, made, node) })
	}
	if u, ok := elem.(*ir.Union); ok && !u.Has(leaf) {
		if isResultUnion(u) && isObjectOfProgram(leaf) {
			return true // an iterator result, as iterators hand them back: any of the program's objects as it is (D-178)
		}
		member := l.memberFor(leaf, u)
		if member == nil && (isObject(leaf) || isMap(leaf)) {
			l.notSupportedYet(node, fmt.Sprintf("converting %s to %s is", leaf, u))
			return false
		}
		if member == nil {
			return true // left to the runtime's check
		}
		elem = member
	}
	switch {
	case leaf == elem || leaf == ir.Value || elem == ir.Value || isUnion(elem):
		return true
	case isMap(leaf) && isMap(elem):
		l.notSupportedYet(node, fmt.Sprintf("converting %s to %s is", leaf, elem)) // a map or a set converts to its own type only, a hash-map object by copying it
		return false
	case isArray(leaf) || isTuple(leaf):
		switch {
		case isDynamicArray(leaf) || !isArray(elem) && !isTuple(elem):
			return true
		case isTuple(leaf) && isArray(elem), !made && isTuple(elem):
			l.notSupportedYet(node, fmt.Sprintf("using a `%s` as a `%s` is", leaf, elem)) // the runtime converts arrays to arrays only, and tuples to tuples by copying them
			return false
		case !made:
			l.reportKeptCopy(leaf, elem, node)
			return false
		}
		return l.keepsElementsOf(leaf, elem, node)
	case isObject(leaf):
		switch target := elem.(type) {
		case *ir.Interface:
			missing := unprovided(leaf, target)
			if missing != "" && !holdsOptionally(leaf, target) {
				l.notSupportedYet(node, fmt.Sprintf("using a `%s` as a `%s`, whose `%s` it doesn't provide with the same types, is", leaf, target, missing))
				return false
			}
			if missing != "" {
				l.relaxedCasts = append(l.relaxedCasts, relaxedCast{from: leaf, to: target, node: node, in: l.function}) // checked against the subclasses that hold it otherwise, as objectCast's (D-066)
			}
		case *ir.Class:
			if class, ok := leaf.(*ir.Class); ok && !target.Result && !class.IsSubclassOf(target) {
				l.notSupportedYet(node, fmt.Sprintf("using a `%s` as a `%s` is", leaf, target))
				return false
			}
		case *ir.MapType:
			l.notSupportedYet(node, fmt.Sprintf("using a `%s` as a hash-map object (`%s`) is", leaf, target)) // copied into one
			return false
		}
	}
	return true
}

// keepsElementsOf is keepsLeaf for the elements of a new array or tuple of type from, copied
// into one of type to, an array into an array or a tuple into a tuple: its elements are the
// values they are, stored where to's go.
func (l *lowerer) keepsElementsOf(from, to ir.Type, node *ast.Node) bool {
	switch from := from.(type) {
	case *ir.Array:
		if to, ok := to.(*ir.Array); ok {
			return l.keepsLeaf(from.Elem, to.Elem, false, node)
		}
	case *ir.Class:
		if to, ok := to.(*ir.Class); ok {
			for i := range min(len(from.Fields), len(to.Fields)) {
				if !l.keepsLeaf(from.Fields[i].Type, to.Fields[i].Type, false, node) {
					return false
				}
			}
		}
	}
	return true
}

// reportKeptCopy reports a conversion of an array of type from to an array type to that
// would copy it where it may be changed or kept, which D-094 allows only where no one else
// holds the array.
func (l *lowerer) reportKeptCopy(from, to ir.Type, node *ast.Node) {
	l.notSupportedYet(node, fmt.Sprintf("using a %s as a %s where it may be changed or kept (a copy wouldn't show the changes; a new array, or one passed to a parameter that is only read, is copied) is", from, to))
}

// leafTypes is the types of the values flatMap stores from a callback value of type t: the
// elements of an array or a tuple, or the value itself, for each member of a union.
func leafTypes(t ir.Type) []ir.Type {
	switch t := t.(type) {
	case *ir.Array:
		return []ir.Type{t.Elem}
	case *ir.Union:
		var leaves []ir.Type
		for _, member := range t.Members {
			leaves = append(leaves, leafTypes(member)...)
		}
		return leaves
	case *ir.Class:
		if t.Tuple {
			leaves := make([]ir.Type, len(t.Fields))
			for i, field := range t.Fields {
				leaves[i] = field.Type
			}
			return leaves
		}
	}
	return []ir.Type{t}
}

// elementTypesOf is the element type of t if it is an array type, its elements' types if it
// is a tuple type, and nothing otherwise.
func (l *lowerer) elementTypesOf(t *checker.Type) []*checker.Type {
	switch {
	case l.checker.IsArrayType(t):
		return []*checker.Type{l.checker.GetElementTypeOfArrayType(t)}
	case checker.IsTupleType(t):
		return l.checker.GetTypeArguments(t)
	}
	return nil
}

// flatDepth lowers flat's depth argument to a number: undefined is the default depth, 1,
// and another value converts to a number at run time, as in JS (`depth?: number`). A depth
// tsc types as a negative or fractional number is reported: tsc's FlatArray types the
// result by it as JS's flat doesn't (`xs.flat(-1)` as an array of xs's type, where JS
// copies xs), so typed code would read the elements as what they aren't (D-180).
func (l *lowerer) flatDepth(arg *ast.Node) ir.Expr {
	if t := l.typeAt(arg); l.isOddDepth(arg, t) {
		return nil
	}
	depth := l.expression(arg)
	if depth == nil {
		return nil
	}
	switch t := depth.Type(); {
	case t == ir.Number:
		return depth
	case ir.IsNumeric(t):
		return &ir.Convert{X: depth, To: ir.Number}
	case t == ir.Undefined:
		return &ir.Sequence{X: depth, Y: &ir.NumberConst{Value: 1}}
	}
	return box(depth) // converted by the runtime after it reads the length, as JS converts it
}

// isOddDepth reports, and reports whether, t, the type of flat's depth arg, is or has a
// number literal that tsc's FlatArray doesn't take as JS's flat does.
func (l *lowerer) isOddDepth(arg *ast.Node, t *checker.Type) bool {
	odd, ok := oddDepth(t)
	if !ok {
		return false
	}
	taken := jsnum.Number(max(0, math.Trunc(odd))) // ToIntegerOrInfinity, where a depth below 1 flattens nothing
	if t.Flags()&checker.TypeFlagsUnion != 0 {
		l.report(diagnostic.At(arg, diagnostic.NotSupportedYet, "`flat` with a depth of type %s is not supported yet: TypeScript's type for its result isn't what JS's flat returns for %s, which JS takes as %s", l.checker.TypeToString(t), jsnum.Number(odd), taken))
	} else {
		l.report(diagnostic.At(arg, diagnostic.NotSupportedYet, "`flat` with a depth of %s is not supported yet: TypeScript's type for its result isn't what JS's flat returns; JS flattens as `flat(%s)` does, so write that", jsnum.Number(odd), taken))
	}
	return true
}

// oddDepth is a number literal of t (or of its union's members) that is negative or not
// whole, as tsc's FlatArray doesn't take a depth.
func oddDepth(t *checker.Type) (float64, bool) {
	types := []*checker.Type{t}
	if t.Flags()&checker.TypeFlagsUnion != 0 {
		types = t.Types()
	}
	for _, member := range types {
		if member.Flags()&checker.TypeFlagsNumberLiteral == 0 {
			continue
		}
		if n := float64(member.AsLiteralType().Value().(jsnum.Number)); n < 0 || n != math.Trunc(n) {
			return n, true
		}
	}
	return 0, false
}

// isArrayMethodCall reports whether node calls a method of an array.
func (l *lowerer) isArrayMethodCall(node *ast.Node) bool {
	if node.Kind != ast.KindCallExpression {
		return false
	}
	callee := ast.SkipParentheses(node.AsCallExpression().Expression)
	if callee.Kind != ast.KindPropertyAccessExpression {
		return false
	}
	return l.isArrayExpression(callee.Expression()) ||
		checker.IsTupleType(l.typeAt(callee.Expression())) && tupleCopyMethods[callee.AsPropertyAccessExpression().Name().Text()]
}

// isArrayStatic reports whether a callee is the static method name of the global Array.
func (l *lowerer) isArrayStatic(callee *ast.Node, name string) bool {
	if callee.Kind != ast.KindPropertyAccessExpression || callee.AsPropertyAccessExpression().Name().Text() != name {
		return false
	}
	object := callee.Expression()
	return object.Kind == ast.KindIdentifier && object.Text() == "Array" && l.isDeclaredInDefaultLib(l.target(object))
}

// arrayIsArray lowers Array.isArray(x), which the static type of x decides, unless x is
// a dynamic value or a union, whose value does (D-076).
func (l *lowerer) arrayIsArray(call *ast.CallExpression) ir.Expr {
	x := l.valueExpression(call.Arguments.Nodes[0])
	if x == nil {
		return nil
	}
	if x.Type() == ir.Value || isUnion(x.Type()) {
		return &ir.CallBuiltin{Builtin: ir.ValueIsArray, Args: []ir.Expr{box(x)}}
	}
	return discard(x, &ir.BoolConst{Value: isArray(x.Type())})
}

// arrayFrom lowers Array.from(x) for an array x (a copy) or a string (its code points).
func (l *lowerer) arrayFrom(call *ast.CallExpression) ir.Expr {
	args := call.Arguments.Nodes
	if len(args) > 2 {
		l.notSupportedYet(args[2], "the `thisArg` argument of `Array.from` is")
		return nil
	}
	if length := lengthObject(args[0]); length != nil {
		return l.arrayFromLength(call, length)
	}
	if it, ok := l.mapIterableOf(args[0]); ok {
		if it.m == nil {
			return nil // reported
		}
		return l.arrayFromMap(call, it)
	}
	x := l.iterable(args[0])
	if x != nil && (x.Type() == ir.Value || l.hasIteratorMethod(l.typeAt(args[0]))) { // a generator object's values, a dynamic value's items, an object's by its [Symbol.iterator]: an any[] (D-078, D-109, D-111)
		x = &ir.CallBuiltin{Builtin: ir.ValueToArray, Args: []ir.Expr{box(x), &ir.StringConst{Value: scanner.GetTextOfNode(args[0])}}}
	}
	switch {
	case x == nil:
		return nil
	case len(args) == 2 && (isArray(x.Type()) || x.Type() == ir.String):
		return l.arrayFromWithMapper(call, x)
	case isArray(x.Type()):
		return &ir.ArrayCall{Op: ir.ArrayCopy, Args: []ir.Expr{x}} // its iterator's items: holes undefined where they fit (D-183)
	case x.Type() == ir.String:
		return &ir.CallBuiltin{Builtin: ir.ArrayFromString, Args: []ir.Expr{x}}
	}
	l.notSupportedYet(args[0], fmt.Sprintf("`Array.from` of a %s is", x.Type()))
	return nil
}

// nonNull lowers `x!`. Under it, a.pop() and a.shift() give the element itself, and
// throw if a is empty; a union becomes its type without null and undefined, and throws
// TypeError if it is either (D-062); elsewhere `!` changes nothing.
func (l *lowerer) nonNull(node *ast.Node) ir.Expr {
	inner := ast.SkipParentheses(node.Expression())
	if l.isArrayMethodCall(inner) {
		name := ast.SkipParentheses(inner.Expression()).AsPropertyAccessExpression().Name().Text()
		if _, removes := arrayRemovers[name]; removes || name == "find" || name == "findLast" {
			return l.arrayMethodCall(inner.AsCallExpression(), true)
		}
	}
	if l.isMapMethodCall(inner) && ast.SkipParentheses(inner.Expression()).AsPropertyAccessExpression().Name().Text() == "get" {
		return l.mapMethodCall(inner.AsCallExpression(), true)
	}
	x := l.expression(inner)
	if x == nil || !isUnion(x.Type()) {
		return x
	}
	t := l.irType(l.checker.GetNonNullableType(l.typeAt(node)), node) // in a chain, tsc's type keeps the chain's undefined
	if t == nil {
		return nil
	}
	return &ir.FromUnion{X: x, To: t, Checked: true} // throws on null and undefined (docs/SPEC.md §3.5)
}

// forOfBinding declares the local that the variable declaration of a for…of statement
// binds each item to: its name's, or for a pattern, a local of type elemType that the
// destructured statements take apart. ok is false if it failed (reported).
func (l *lowerer) forOfBinding(declaration *ast.Node, elemType ir.Type) (element *ir.Local, destructured []ir.Stmt, ok bool) {
	name := declaration.Name()
	if name.Kind != ast.KindIdentifier { // `for (const [k, v] of pairs)`: each element is destructured
		element = l.newLocal("element", elemType)
		destructured, ok = l.destructure(name, &ir.LocalRef{Local: element}, l.pos(declaration))
		return element, destructured, ok
	}
	symbol := l.checker.GetSymbolAtLocation(name)
	t := l.bindingType(symbol, name)
	if t == nil {
		return nil, nil, false
	}
	element = l.newLocal(name.Text(), t)
	l.locals[symbol] = element
	return element, nil, true
}

// forOfStatement lowers `for (const x of a) body` for an array a to a loop over its
// indices: like JS's array iterator, it reads the length before each iteration, so it
// sees elements pushed meanwhile. A string's code points come from a new array of them.
// Iterators, iterables and dynamic values are iterated through their iterators
// (iteratorLoop), which a generator or an object's next() may give lazily.
func (l *lowerer) forOfStatement(node *ast.Node, label *ir.Label) []ir.Stmt {
	s := node.AsForInOrOfStatement()
	switch {
	case s.Initializer.Kind != ast.KindVariableDeclarationList || len(s.Initializer.AsVariableDeclarationList().Declarations.Nodes) != 1:
		l.notSupportedYet(s.Initializer, "`for...of` without a declaration of its variable is")
		return nil
	}
	declaration := s.Initializer.AsVariableDeclarationList().Declarations.Nodes[0]
	if node.Flags&ast.NodeFlagsPtsParallel != 0 {
		return l.parallelFor(node, label, declaration) // D-127
	}
	if s.AwaitModifier != nil {
		return l.asyncForOf(node, label, declaration) // D-115
	}
	if it, ok := l.mapIterableOf(s.Expression); ok {
		if it.m == nil {
			return nil // reported
		}
		return l.mapForOf(node, label, it, declaration.Name(), s.Statement)
	}
	if t := l.typeAt(s.Expression); l.isIterationSource(t) && !l.isArrayIteration(s.Expression) {
		return l.iteratorForOf(node, label, declaration)
	}
	var iterable ir.Expr
	if l.typedArrayClass(s.Expression) != nil {
		iterable = l.expression(s.Expression) // read live, as JS's iterator does
	} else {
		iterable = l.iterable(s.Expression)
	}
	if iterable == nil {
		return nil
	}
	if iterable.Type() == ir.String {
		iterable = &ir.CallBuiltin{Builtin: ir.ArrayFromString, Args: []ir.Expr{iterable}} // its code points
	}
	if isTuple(iterable.Type()) {
		if iterable = l.tupleArray(iterable, l.typeAt(s.Expression), s.Expression); iterable == nil {
			return nil
		}
	}
	if isUnion(iterable.Type()) {
		iterable = box(iterable) // `index.get(k) ?? []`: a set or an array, iterated as a dynamic value (D-089)
	}
	if iterable.Type() == ir.Value { // a dynamic value's items (D-078), or its iterator's, one next() at a time
		return l.iteratorLoop(node, label, declaration, iterable, ir.Value)
	}
	// The loop reads element index of source while index is below its length.
	var elemType ir.Type
	var length func(source ir.Expr) ir.Expr
	var elementAt func(source, index ir.Expr) ir.Expr
	switch t := iterable.Type(); {
	case isArray(t):
		elemType = t.(*ir.Array).Elem
		length = func(source ir.Expr) ir.Expr { return &ir.ArrayCall{Op: ir.ArrayLength, Args: []ir.Expr{source}} }
		read := ir.ArrayElementChecked // a hole throws RangeError where an element is required (D-085)
		if ir.HoldsUndefined(elemType) {
			read = ir.ArrayElementOrUndefined // and is undefined where one fits, as JS's iterator reads it
		}
		elementAt = func(source, index ir.Expr) ir.Expr {
			return &ir.ArrayCall{Op: read, Args: []ir.Expr{source, index}}
		}
	case l.isTypedArray(t):
		elemType = ir.Number
		length = func(source ir.Expr) ir.Expr { return l.callExtern("pts_typed_array_length", ir.Number, source) }
		elementAt = l.typedArrayRead
	default:
		l.notSupportedYet(s.Expression, fmt.Sprintf("`for...of` over a %s is", iterable.Type()))
		return nil
	}
	at := l.pos(node)
	source, index := l.newLocal("array", iterable.Type()), l.newLocal("index", ir.Number)
	element, destructured, ok := l.forOfBinding(declaration, elemType)
	if !ok {
		return nil
	}
	value := l.convert(elementAt(&ir.LocalRef{Local: source}, &ir.LocalRef{Local: index}), element.Type, declaration)
	if value == nil {
		return nil
	}
	body := l.statementBlock(s.Statement)
	body.Stmts = append(append([]ir.Stmt{&ir.LocalDecl{At: l.pos(declaration), Local: element, Init: value}}, destructured...), body.Stmts...)
	loop := &ir.Loop{
		At:     at,
		Label:  label,
		Cond:   &ir.Binary{Op: ir.Lt, X: &ir.LocalRef{Local: index}, Y: length(&ir.LocalRef{Local: source})},
		Update: &ir.Assign{Local: index, Value: &ir.Binary{Op: ir.Add, X: &ir.LocalRef{Local: index}, Y: &ir.NumberConst{Value: 1}}},
		Body:   body,
	}
	return []ir.Stmt{&ir.Block{At: at, Stmts: []ir.Stmt{
		&ir.LocalDecl{At: at, Local: source, Init: iterable},
		&ir.LocalDecl{At: at, Local: index, Init: &ir.NumberConst{Value: 0}},
		loop,
	}}}
}

// newArrayMembers are the library functions and methods (as libMemberName names them)
// whose result is a new array that nothing else holds.
var newArrayMembers = map[string]bool{
	"ArrayConstructor.from": true, "ArrayConstructor.of": true, "String.split": true,
	"ObjectConstructor.keys": true, "ObjectConstructor.values": true, "ObjectConstructor.entries": true,
}

// newArrayMethods are the methods of arrays whose result is a new array.
var newArrayMethods = map[string]bool{
	"map": true, "filter": true, "slice": true, "concat": true, "flat": true, "flatMap": true, "splice": true,
	"toSorted": true, "toReversed": true, "toSpliced": true, "with": true,
}

// sameArrayMethods are the methods of arrays that return the array they are called on.
var sameArrayMethods = map[string]bool{"sort": true, "reverse": true, "fill": true, "copyWithin": true}

// isNewArray reports whether node makes an array that nothing else holds, which a copy
// can't be told apart from (D-094): an array literal, a call of a function or method that
// makes a new array, or a const whose only use is node and whose initializer is one.
func (l *lowerer) isNewArray(node *ast.Node) bool { return l.isNewArrayIn(node, nil) }

// isNewArrayIn is isNewArray for node in fn, a function flatMap calls for each element (nil:
// none): node makes an array each time fn runs it, so a const counts only if fn makes it
// there (constMadeIn).
func (l *lowerer) isNewArrayIn(node *ast.Node, fn *ast.Node) bool {
	if node == nil {
		return false
	}
	node = ast.SkipParentheses(node)
	switch node.Kind {
	case ast.KindArrayLiteralExpression:
		return true
	case ast.KindAsExpression, ast.KindSatisfiesExpression, ast.KindNonNullExpression:
		return l.isNewArrayIn(node.Expression(), fn)
	case ast.KindNewExpression:
		return l.typeAt(node).Symbol() != nil && l.typeAt(node).Symbol().Name == "Array"
	case ast.KindCallExpression:
		callee := ast.SkipParentheses(node.AsCallExpression().Expression)
		if callee.Kind != ast.KindPropertyAccessExpression {
			return false
		}
		member := l.libMemberName(callee)
		if newArrayMembers[member] {
			return true
		}
		owner, method, _ := strings.Cut(member, ".")
		if owner != "Array" && owner != "ReadonlyArray" {
			return false
		}
		return newArrayMethods[method] || sameArrayMethods[method] && l.isNewArrayIn(callee.Expression(), fn)
	case ast.KindIdentifier:
		if fn != nil {
			initializer := l.constMadeIn(node, fn)
			return initializer != nil && l.isNewArrayIn(initializer, fn)
		}
		return l.isSingleUseConst(node)
	}
	return false
}

// isSingleUseConst reports whether node names a local const whose initializer makes a
// new array (isNewArray) and which node is the only use of.
func (l *lowerer) isSingleUseConst(node *ast.Node) bool {
	initializer := l.singleUseConstInitializer(node)
	return initializer != nil && l.isNewArray(initializer)
}

// singleUseConstInitializer is the initializer of the local const node names if node is its
// only use, nil otherwise: an exported const's other uses may be in other files.
func (l *lowerer) singleUseConstInitializer(node *ast.Node) *ast.Node {
	symbol := l.valueSymbolAt(node)
	if symbol == nil || symbol.ValueDeclaration == nil || symbol.ValueDeclaration.Kind != ast.KindVariableDeclaration {
		return nil
	}
	declaration := symbol.ValueDeclaration
	if declaration.Parent.Flags&ast.NodeFlagsConst == 0 || declaration.Initializer() == nil || ast.HasSyntacticModifier(declaration.Parent.Parent, ast.ModifierFlagsExport) {
		return nil
	}
	uses := 0
	var visit func(n *ast.Node) bool
	visit = func(n *ast.Node) bool {
		if n.Kind == ast.KindIdentifier && n != declaration.Name() && n.Text() == node.Text() && l.valueSymbolAt(n) == symbol {
			uses++
		}
		return uses > 1 || n.ForEachChild(visit)
	}
	ast.GetSourceFileOfNode(node).AsNode().ForEachChild(visit)
	if uses != 1 {
		return nil
	}
	return declaration.Initializer()
}

// valueSymbolAt is the symbol of the value an identifier names: in a shorthand property
// (`{ a }`) or an export specifier (`export { a }`), the local's, not the property's or the
// export's.
func (l *lowerer) valueSymbolAt(name *ast.Node) *ast.Symbol {
	switch name.Parent.Kind {
	case ast.KindShorthandPropertyAssignment:
		if name == name.Parent.Name() { // not a default's (`{ a = ns } = o`)
			return l.checker.GetShorthandAssignmentValueSymbol(name.Parent)
		}
	case ast.KindExportSpecifier:
		return l.checker.GetExportSpecifierLocalTargetSymbol(name.Parent)
	}
	return l.checker.GetSymbolAtLocation(name)
}

// constMadeIn is the initializer of the local const node names if node is its only use and
// fn makes it each time it runs node: fn declares it, and no loop or function between the
// declaration and node runs node again with the same const (a loop doesn't run a returned
// value again). A const of the enclosing code is the same at each of flatMap's calls of fn
// (H05 review round 6).
func (l *lowerer) constMadeIn(node *ast.Node, fn *ast.Node) *ast.Node {
	initializer := l.singleUseConstInitializer(node)
	if initializer == nil {
		return nil
	}
	declaration := initializer.Parent
	if declaration.Pos() < fn.Pos() || declaration.End() > fn.End() {
		return nil
	}
	returned := false
	for scope, child := node.Parent, node; ; scope, child = scope.Parent, scope {
		returned = returned || scope.Kind == ast.KindReturnStatement
		if !returned && repeats(scope, child) {
			return nil
		}
		if scope.Pos() <= declaration.Pos() && declaration.End() <= scope.End() {
			return initializer
		}
		if ast.IsFunctionLike(scope) || ast.IsClassLike(scope) {
			return nil
		}
	}
}

// repeats reports whether statement, a loop, runs its child child more than once: all of a
// loop's parts but a for statement's initializer and a for-in or for-of statement's expression.
func repeats(statement *ast.Node, child *ast.Node) bool {
	switch statement.Kind {
	case ast.KindForStatement:
		return child != statement.AsForStatement().Initializer
	case ast.KindForInStatement, ast.KindForOfStatement:
		return child != statement.AsForInOrOfStatement().Expression
	}
	return ast.IsIterationStatement(statement, false)
}

// readOnlyArrayMethods are the methods of arrays that don't change the array.
var readOnlyArrayMethods = map[string]bool{
	"at": true, "concat": true, "entries": true, "every": true, "filter": true, "find": true, "findIndex": true,
	"findLast": true, "findLastIndex": true, "flat": true, "flatMap": true, "forEach": true, "includes": true,
	"indexOf": true, "join": true, "keys": true, "lastIndexOf": true, "map": true, "reduce": true, "reduceRight": true,
	"slice": true, "some": true, "toReversed": true, "toSorted": true, "toSpliced": true, "toString": true,
	"values": true, "with": true,
}

// isReadOnlyArgument reports whether node is an argument of a call of a function of the
// program whose parameter for it the function only reads (onlyReads), so a copy passed
// there can't be told apart from the array (D-094).
func (l *lowerer) isReadOnlyArgument(node *ast.Node) bool {
	call := node.Parent
	if call == nil || call.Kind != ast.KindCallExpression && call.Kind != ast.KindNewExpression {
		return false
	}
	index := slices.Index(call.Arguments(), node)
	signature := l.checker.GetResolvedSignature(call)
	if index < 0 || signature == nil || signature.Declaration() == nil {
		return false
	}
	fn := signature.Declaration()
	if !ast.IsFunctionLike(fn) || fn.Body() == nil || index >= len(fn.Parameters()) {
		return false // a library function has no body
	}
	param := fn.Parameters()[index]
	if param.AsParameterDeclaration().DotDotDotToken != nil || param.Name().Kind != ast.KindIdentifier || ast.IsParameterPropertyDeclaration(param, fn) {
		return false
	}
	return l.onlyReads(fn, l.checker.GetSymbolAtLocation(param.Name()))
}

// onlyReads reports whether every use of the array parameter param in the body of fn
// reads it: its elements, its length, the methods that don't change it, `for…of` and
// spreads, in fn itself or in callbacks of array methods, which can't keep it.
func (l *lowerer) onlyReads(fn *ast.Node, param *ast.Symbol) bool {
	readOnly := true
	var visit func(node *ast.Node) bool
	visit = func(node *ast.Node) bool {
		if node.Kind == ast.KindIdentifier && node.Text() == param.Name && l.checker.GetSymbolAtLocation(node) == param {
			readOnly = l.isInCallbacksOf(node, fn) && isArrayRead(node)
			return !readOnly
		}
		return node.ForEachChild(visit)
	}
	visit(fn.Body())
	return readOnly
}

// isInCallbacksOf reports whether node is in fn, directly or in functions passed to array
// methods there (`as.map(() => bs.map(…))`), which run before the method returns.
func (l *lowerer) isInCallbacksOf(node *ast.Node, fn *ast.Node) bool {
	for inner := ast.GetContainingFunction(node); inner != fn; inner = ast.GetContainingFunction(inner) {
		if inner == nil || inner.Parent == nil || inner.Parent.Kind != ast.KindCallExpression || !slices.Contains(inner.Parent.Arguments(), inner) {
			return false
		}
		call := inner.Parent
		callee := ast.SkipParentheses(call.Expression())
		owner, _, _ := strings.Cut(l.libMemberName(callee), ".")
		if callee.Kind != ast.KindPropertyAccessExpression || owner != "Array" && owner != "ReadonlyArray" {
			return false
		}
	}
	return true
}

// isArrayRead reports whether the identifier of an array is used only to read it there.
func isArrayRead(identifier *ast.Node) bool {
	node := identifier
	for node.Parent.Kind == ast.KindParenthesizedExpression {
		node = node.Parent
	}
	parent := node.Parent
	switch parent.Kind {
	case ast.KindElementAccessExpression:
		return parent.Expression() == node && !ast.IsWriteAccess(parent)
	case ast.KindPropertyAccessExpression:
		if parent.Expression() != node || ast.IsWriteAccess(parent) {
			return false
		}
		name := parent.Name().Text()
		isCalled := parent.Parent.Kind == ast.KindCallExpression && parent.Parent.Expression() == parent
		return name == "length" && !isCalled || isCalled && readOnlyArrayMethods[name]
	case ast.KindForOfStatement:
		return parent.Expression() == node
	case ast.KindSpreadElement:
		return true
	}
	return false
}
