package lower

import (
	"fmt"
	"slices"
	"strings"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/pts/check"
	"github.com/microsoft/typescript-go/pts/ir"
)

// unionType maps a union of checker types to the IR union of its members' IR types
// (D-062), or to the one IR type they all map to (`1 | 2` is a number); it reports why
// it can't (at node) and returns nil.
func (l *lowerer) unionType(t *checker.Type, node *ast.Node) ir.Type {
	if l.recursiveUnions[t] {
		return ir.Value
	}
	l.unionsInProgress[t]++
	defer func() {
		if l.unionsInProgress[t]--; l.unionsInProgress[t] == 0 {
			delete(l.unionsInProgress, t)
		}
	}()
	var members []ir.Type
	for _, constituent := range t.Types() {
		member := l.irType(constituent, node)
		if member == nil {
			return nil
		}
		if !slices.Contains(members, member) {
			members = append(members, member)
		}
	}
	if l.recursiveUnions[t] {
		return ir.Value // a recursive union (`type Json = … | Json[]`) holds dynamic values (D-089)
	}
	if slices.Contains(members, ir.Type(ir.Value)) {
		return ir.Value // a dynamic value (`unknown`) holds every member's values
	}
	if len(members) == 1 {
		return members[0]
	}
	if numeric := slices.DeleteFunc(slices.Clone(members), func(m ir.Type) bool { return !ir.IsNumeric(m) }); len(numeric) > 1 {
		l.notSupportedYet(node, fmt.Sprintf("unions of several numeric types (%s) are", l.checker.TypeToString(t)))
		return nil
	}
	return l.unionOf(members)
}

// holdsMembersOf reports whether the values of node's type are all of members of u, its
// members that no value has aside (`NIL: List<never>` as a `List<number>`).
func (l *lowerer) holdsMembersOf(node *ast.Node, u *ir.Union) bool {
	t := l.typeAt(node)
	if t.Flags()&checker.TypeFlagsUnion == 0 {
		return false
	}
	return !slices.ContainsFunc(t.Types(), func(member *checker.Type) bool {
		return !l.isUninhabited(member) && !u.Has(l.irType(member, node))
	})
}

// isUninhabited reports whether no value has the object type t: one with a required
// property of type never (the cons member of a `List<never>`, `{ head: never; … }`).
func (l *lowerer) isUninhabited(t *checker.Type) bool {
	if t.Flags()&checker.TypeFlagsObject == 0 || l.isArrayType(t) || checker.IsTupleType(t) {
		return false
	}
	return slices.ContainsFunc(l.checker.GetPropertiesOfType(t), func(property *ast.Symbol) bool {
		return property.Flags&ast.SymbolFlagsOptional == 0 && l.checker.GetTypeOfSymbol(property).Flags()&checker.TypeFlagsNever != 0
	})
}

// recurring is the IR type of an array or hash-map object type met again while mapping
// it inside a union, which makes the unions being mapped recursive: their values are
// dynamic (D-089). ok is false outside unions.
func (l *lowerer) recurring() (t ir.Type, ok bool) {
	for u := range l.unionsInProgress {
		l.recursiveUnions[u] = true
	}
	return ir.Value, len(l.unionsInProgress) > 0
}

func isUnion(t ir.Type) bool {
	_, ok := t.(*ir.Union)
	return ok
}

// unionOf is the one union of members, which must be at least two types other than unions.
func (l *lowerer) unionOf(members []ir.Type) *ir.Union {
	for _, u := range l.unions {
		if len(u.Members) == len(members) && !slices.ContainsFunc(members, func(m ir.Type) bool { return !u.Has(m) }) {
			return u
		}
	}
	sorted := slices.Clone(members)
	slices.SortStableFunc(sorted, func(a, b ir.Type) int { return strings.Compare(a.String(), b.String()) })
	u := &ir.Union{Members: sorted}
	l.unions = append(l.unions, u)
	return u
}

// isNarrowing reports whether to, the type of a value of union type from, is narrower:
// one of from's members or a subclass of one (`p instanceof King` for `p: Piece | null`),
// or a union of such types.
func isNarrowing(from *ir.Union, to ir.Type) bool {
	if u, ok := to.(*ir.Union); ok {
		return u != from && !slices.ContainsFunc(u.Members, func(m ir.Type) bool { return !isNarrowedMember(from, m) })
	}
	return isNarrowedMember(from, to)
}

// isNarrowedMember reports whether t is a member of from or a subclass of a class member.
func isNarrowedMember(from *ir.Union, t ir.Type) bool {
	if from.Has(t) {
		return true
	}
	class, ok := t.(*ir.Class)
	return ok && !class.Shape && slices.ContainsFunc(from.Members, func(m ir.Type) bool {
		ancestor, ok := m.(*ir.Class)
		return ok && !ancestor.Shape && class.IsSubclassOf(ancestor)
	})
}

// narrowed is x, the lowered reference node (a variable, property or element), as the
// type the checker narrowed it to there (`x` after `x !== null`), if x is a union and
// that type is narrower (D-062); otherwise x.
func (l *lowerer) narrowed(node *ast.Node, x ir.Expr) ir.Expr {
	if x == nil {
		return nil
	}
	if x.Type() == ir.Value {
		return l.narrowedValue(node, x)
	}
	union, ok := x.Type().(*ir.Union)
	if !ok {
		return x
	}
	t := l.typeAt(node)
	if t.Flags()&checker.TypeFlagsNever != 0 {
		return x // unreachable code, as tsc sees it
	}
	narrow := l.irType(t, node)
	if narrow == ir.Value {
		return box(x) // narrowed to a type only dynamic values hold (`E & Error` after `e instanceof Error`, D-095)
	}
	if narrow != nil && isNarrowing(union, narrow) {
		if node.Kind == ast.KindIdentifier && l.isLoose(l.readSymbol(node)) {
			if l.isTestedOperand(node) {
				return x // tested for undefined: the union it holds
			}
			return l.looseRead(x, narrow, node) // it may be undefined, which tsc doesn't know (D-090)
		}
		if !isClass(narrow) && (node.Kind != ast.KindIdentifier || l.assignedOutside[l.readSymbol(node)]) {
			// A property tsc narrowed, or a variable another function assigns, may have
			// changed since, through a call that tsc doesn't follow, so its value is
			// checked as a dynamic value is (classes' always are) (D-089, D-140).
			return &ir.FromUnion{X: box(x), To: narrow}
		}
		return &ir.FromUnion{X: x, To: narrow}
	}
	return x
}

// readSymbol is the variable an identifier reads: the shorthand property `{ x }`'s value
// symbol, which isn't the symbol at its name (the property's).
func (l *lowerer) readSymbol(node *ast.Node) *ast.Symbol {
	if node.Parent != nil && node.Parent.Kind == ast.KindShorthandPropertyAssignment {
		return l.followAliases(l.checker.GetShorthandAssignmentValueSymbol(node.Parent))
	}
	return l.target(node)
}

// toUnion converts value, of a member of u or a union of some of them, to u; nil if it
// is neither (reported at node).
func (l *lowerer) toUnion(value ir.Expr, u *ir.Union, node *ast.Node) ir.Expr {
	switch from := value.Type().(type) {
	case *ir.Union:
		if !slices.ContainsFunc(from.Members, func(m ir.Type) bool { return !u.Has(m) }) || l.holdsMembersOf(node, u) {
			return &ir.ToUnion{X: value, To: u}
		}
		if isResultUnion(u) && !slices.ContainsFunc(from.Members, func(m ir.Type) bool { return !isObjectOfProgram(m) }) {
			return asResult(value, u) // `IteratorResult<T, undefined>` where an `IteratorResult<T>` goes
		}
	default:
		if u.Has(from) {
			return &ir.ToUnion{X: value, To: u}
		}
		if class, ok := from.(*ir.Class); ok && !class.Shape && node != nil {
			// `e instanceof A ? e : new B()` where an `A | B` goes: e as the subclass the
			// checker narrowed it to (checked), as the member that subclass is or extends.
			if narrow, ok := l.irType(l.typeAt(node), node).(*ir.Class); ok && narrow != class && narrow.IsSubclassOf(class) {
				for _, m := range u.Members {
					if member, ok := m.(*ir.Class); ok && !member.Shape && narrow.IsSubclassOf(member) {
						return &ir.ToUnion{X: upcast(downcast(value, narrow), member), To: u}
					}
				}
			}
		}
		if isResultUnion(u) && isObjectOfProgram(from) {
			return asResult(value, u)
		}
		if member := l.memberFor(from, u); member != nil {
			if leavesOut(from, member, u) && l.prepareCheckedUnion(u, node) {
				// An object holding dynamic values in a property that member leaves out and
				// another member declares (`{ tag: "some", value: anyArray }` where `{ tag:
				// "none" }` is all it provides as it is): tsc may narrow it to the other member,
				// so it converts as dynamic data does, copied into that member (C187, D-192).
				return &ir.FromUnion{X: box(value), To: u}
			}
			if converted := l.convert(value, member, node); converted != nil {
				return &ir.ToUnion{X: converted, To: u}
			}
			return nil
		}
	}
	l.notSupportedYet(node, fmt.Sprintf("converting %s to %s is", value.Type(), u))
	return nil
}

// asResult converts value, an object of the program's of another layout than D-109's
// result shapes (an object literal with getters or more properties, a class's instance),
// to an iterator result's type t, a Result shape or a union of them, as it is: reads of a
// result's done and value find them by name on such an object (ir.Class.Result, H03b), so
// no getter runs before it is read.
func asResult(value ir.Expr, t ir.Type) ir.Expr {
	if u, ok := t.(*ir.Union); ok {
		return &ir.ToUnion{X: value, To: u}
	}
	return &ir.ClassCast{X: value, To: t}
}

// isResultUnion reports whether every member of u is a shape of iterator results.
func isResultUnion(u *ir.Union) bool {
	return !slices.ContainsFunc(u.Members, func(m ir.Type) bool {
		class, ok := m.(*ir.Class)
		return !ok || !class.Result
	})
}

// isObjectOfProgram reports whether values of t are objects with descriptors the program
// makes, whose properties can be found by name: instances of classes and shapes, and what
// interfaces refer to.
func isObjectOfProgram(t ir.Type) bool {
	switch t.(type) {
	case *ir.Class, *ir.Interface:
		return true
	}
	return false
}

// memberFor is the member of u that a value of type t converts to: the one numeric
// member for a number, the one function type for a function, or the one member that
// is an object type for an object; nil if there isn't exactly one. Among several array
// members an array goes to the one holding its elements as they are, and a tuple of
// another layout than u's tuples to the array member holding its elements (D-180).
func (l *lowerer) memberFor(t ir.Type, u *ir.Union) ir.Type {
	if tuple, ok := t.(*ir.Class); ok && tuple.Tuple {
		if array := tupleArray(tuple, u); array != nil {
			return array // a tuple of another layout than u's tuples, which no cast makes one of them
		}
	}
	var fits func(ir.Type) bool
	switch {
	case ir.IsNumeric(t):
		fits = ir.IsNumeric
	case isFunction(t):
		fits = isFunction
	case isObject(t) && !isArray(t):
		fits = func(m ir.Type) bool { return isObject(m) && !isArray(m) }
	case isArray(t):
		fits = isArray
	default:
		return nil
	}
	var found ir.Type
	for _, m := range u.Members {
		if fits(m) {
			if found != nil && isArray(t) {
				return holdingArray(t.(*ir.Array), u)
			}
			if found != nil {
				return providedMember(t, u) // several object members: the one t provides as it is
			}
			found = m
		}
	}
	return found
}

// tupleArray is the array member of u whose elements hold every element of tuple as it is,
// or nil if that is not one: a new [number, string] where a `number | string | (number |
// string)[]` goes is a (number | string)[] (D-180).
func tupleArray(tuple *ir.Class, u *ir.Union) ir.Type {
	var found ir.Type
	for _, m := range u.Members {
		member, ok := m.(*ir.Array)
		if !ok || slices.ContainsFunc(tuple.Fields, func(f *ir.Field) bool { return !holdsAsItIs(member.Elem, f.Type) }) {
			continue
		}
		if found != nil {
			return nil
		}
		found = member
	}
	return found
}

// holdsAsItIs reports whether a place of type place holds a value of type t without
// converting it beyond ToUnion: t itself, a member of the union place, or a union of its
// members (a (number | string)[]'s elements where a (number | string | number[])[]'s go).
func holdsAsItIs(place, t ir.Type) bool {
	union, ok := place.(*ir.Union)
	if place == t || !ok {
		return place == t
	}
	if members, ok := t.(*ir.Union); ok {
		return !slices.ContainsFunc(members.Members, func(m ir.Type) bool { return !union.Has(m) })
	}
	return union.Has(t)
}

// holdingArray is the array member of u whose elements hold array's as they are (its
// element type, or a union of it), or nil if that is not one: a number[][] where a
// `number | number[] | (number | number[])[]` goes is a (number | number[])[] (D-180).
func holdingArray(array *ir.Array, u *ir.Union) ir.Type {
	var found ir.Type
	for _, m := range u.Members {
		member, ok := m.(*ir.Array)
		if !ok {
			continue
		}
		if holdsAsItIs(member.Elem, array.Elem) {
			if found != nil {
				return nil
			}
			found = member
		}
	}
	return found
}

// providedMember is the interface member of u whose members values of type t provide as
// they are (`unprovided`): the one with the most properties, or nil if that is not one.
func providedMember(t ir.Type, u *ir.Union) ir.Type {
	var found *ir.Interface
	ambiguous := false
	for _, m := range u.Members {
		iface, ok := m.(*ir.Interface)
		switch {
		case !ok || unprovided(t, iface) != "":
		case found == nil || len(iface.Fields) > len(found.Fields):
			found, ambiguous = iface, false // `{ type: "num", n: 1 }` for `{ type: "num"; n: number } | { type: "end" }` is the first (D-089)
		case len(iface.Fields) == len(found.Fields) && !sameFields(iface, found):
			ambiguous = true // members with the same fields (`{ kind: "add"; … } | { kind: "mul"; … }`) are alike at run time
		}
	}
	if found == nil || ambiguous {
		return nil
	}
	return found
}

// leavesOut reports whether member, the member of u that an object of the object type or
// shape t is one of, leaves out a property of t that holds dynamic values inside it (an
// `any[]`, a `{ n: any }`) and that another object type among u's members declares. A
// property of another typed type (a `Dog[]` where the other member says `Animal[]`, a
// function of fewer parameters) or of `any` itself, and an instance of a class t names, is
// left as it is, as before: a copy would lose its class and identity, and typed reads of it
// as the other member verify it, a dynamic value converted as it is (D-192).
func leavesOut(t, member ir.Type, u *ir.Union) bool {
	if class, ok := t.(*ir.Class); ok && (!class.Shape || class.Tuple) {
		return false
	}
	for _, m := range u.Members {
		other, ok := m.(*ir.Interface)
		if !ok || m == member {
			continue
		}
		for _, field := range other.Fields {
			if property := propertyType(t, field.Name); property != nil && property != ir.Value && propertyType(member, field.Name) == nil && holdsDynamicValues(property, map[ir.Type]bool{}) {
				return true
			}
		}
	}
	return false
}

// propertyType is the type an object type, a class or a shape declares its property name
// as (a getter's result), or nil.
func propertyType(t ir.Type, name string) ir.Type {
	switch t := t.(type) {
	case *ir.Interface:
		if field := t.Field(name); field != nil {
			return field.Type
		}
	case *ir.Class:
		if field := t.Field(name); field != nil {
			return field.Type
		}
		if getter := t.Method(name, ir.Getter); getter != nil {
			return getter.Func.Result
		}
	}
	return nil
}

// holdsDynamicValues reports whether values of t may hold dynamic values: t is the dynamic
// type, or an array, map, object type, shape or union holding it.
func holdsDynamicValues(t ir.Type, seen map[ir.Type]bool) bool {
	if seen[t] {
		return false
	}
	seen[t] = true
	holds := func(f *ir.Field) bool { return holdsDynamicValues(f.Type, seen) }
	switch t := t.(type) {
	case *ir.Primitive:
		return t == ir.Value
	case *ir.Array:
		return holdsDynamicValues(t.Elem, seen)
	case *ir.MapType:
		return holdsDynamicValues(t.Key, seen) || (t.Value != nil && holdsDynamicValues(t.Value, seen))
	case *ir.Interface:
		return slices.ContainsFunc(t.Fields, holds)
	case *ir.Class:
		return t.Shape && slices.ContainsFunc(t.Fields, holds)
	case *ir.Union:
		return slices.ContainsFunc(t.Members, func(m ir.Type) bool { return holdsDynamicValues(m, seen) })
	}
	return false
}

// sameFields reports whether two interfaces have the same fields, of the same types.
func sameFields(a, b *ir.Interface) bool {
	if len(a.Fields) != len(b.Fields) {
		return false
	}
	for i, field := range a.Fields {
		if other := b.Fields[i]; other.Name != field.Name || other.Type != field.Type || other.Optional != field.Optional {
			return false
		}
	}
	return true
}

// fromUnion is x, a value of a union, as type t, which the checker says it holds: a
// member, a union of some of its members, or (where a test excluded the others, as
// `x || y` does) any type sharing the representation.
func fromUnion(x ir.Expr, t ir.Type) ir.Expr {
	if x.Type() == t {
		return x
	}
	return &ir.FromUnion{X: x, To: t}
}

// isNullishTest recognises `x === null`, `x === undefined`, `x == null` and the like
// (either way round, negated with !== and !=), and returns x's node and which values
// match: null, undefined or (for == and !=, nil) both.
func isNullishTest(expr *ast.BinaryExpression) (operand *ast.Node, matches ir.Type, ok bool) {
	loose := expr.OperatorToken.Kind == ast.KindEqualsEqualsToken || expr.OperatorToken.Kind == ast.KindExclamationEqualsToken
	for _, pair := range [][2]*ast.Node{{expr.Left, expr.Right}, {expr.Right, expr.Left}} {
		other := ast.SkipParentheses(pair[1])
		switch {
		case loose && (other.Kind == ast.KindNullKeyword || isUndefined(other)):
			return pair[0], nil, true
		case other.Kind == ast.KindNullKeyword:
			return pair[0], ir.Null, true
		case isUndefined(other):
			return pair[0], ir.Undefined, true
		}
	}
	return nil, nil, false
}

// nullishTest lowers a test of operand against null or undefined (isNullishTest): of a
// union or dynamic value at run time, and otherwise by its type, which tsc narrowed.
func (l *lowerer) nullishTest(op ir.BinaryOp, operand *ast.Node, matches ir.Type) ir.Expr {
	x := l.valueExpression(operand) // a missing key or index is undefined
	if x == nil {
		return nil
	}
	var test ir.Expr
	switch t := x.Type(); {
	case isUnion(t) || t == ir.Value:
		test = &ir.IsNullish{X: x, Only: matches}
	case (isObject(t) || isFunction(t) || isMap(t)) && matches != ir.Null:
		test = &ir.Unary{Op: ir.Not, X: &ir.Truthy{X: x}} // undefined only while a class's field is unset (D-141)
	default:
		nullish := t == ir.Null || t == ir.Undefined
		test = discard(x, &ir.BoolConst{Value: nullish && (matches == nil || matches == t)})
	}
	if op == ir.Ne {
		test = &ir.Unary{Op: ir.Not, X: test}
	}
	return test
}

// unionEquality lowers x === y (or !==) where either is a union: both are compared as
// dynamic values, with JS's rules.
func unionEquality(op ir.BinaryOp, x ir.Expr, y ir.Expr) ir.Expr {
	var test ir.Expr = &ir.CallBuiltin{Builtin: ir.ValueStrictEquals, Args: []ir.Expr{box(x), box(y)}}
	if op == ir.Ne {
		test = &ir.Unary{Op: ir.Not, X: test}
	}
	return test
}

// looseEquality lowers x == y (or !=) where either is a union: both are compared as
// dynamic values, with JS's conversions (D-067).
func looseEquality(op ir.BinaryOp, x ir.Expr, y ir.Expr) ir.Expr {
	var test ir.Expr = &ir.CallBuiltin{Builtin: ir.ValueLooseEquals, Args: []ir.Expr{box(x), box(y)}}
	if op == ir.Ne {
		test = &ir.Unary{Op: ir.Not, X: test}
	}
	return test
}

// nullishCoalescing lowers `x ?? y`: x unless it is null or undefined, else y, which is
// evaluated only then. The result has the type tsc gives the expression.
func (l *lowerer) nullishCoalescing(node *ast.Node) ir.Expr {
	expr := node.AsBinaryExpression()
	var x ir.Expr
	if left := ast.SkipParentheses(expr.Left); l.isArrayElement(left) || l.isTypedArrayElement(left) || l.isStringElement(left) || ast.IsAccessExpression(left) && l.isNarrowedDynamic(left.Expression()) {
		x = l.valueExpression(left) // `digits[i] ?? 0`: undefined past the end, as in JS (D-089, D-179)
	} else {
		x = l.expression(expr.Left)
	}
	if x == nil {
		return nil
	}
	right := func() ir.Expr {
		if trap := l.deadOperand(expr.Right); trap != nil {
			return trap // an instance's type arguments make it dead (D-195)
		}
		return l.expression(expr.Right)
	}
	switch t := x.Type(); {
	case t == ir.Null || t == ir.Undefined:
		y := right()
		if y == nil {
			return nil
		}
		return discard(x, y)
	case isObject(t) || isFunction(t) || isMap(t):
		return l.unsetFieldFallback(x, expr.Right)
	case !isDynamic(t):
		return x // never null or undefined, so y is never evaluated
	}
	t := l.typeAt(node)
	if l.untypedCollectionContext(ast.SkipParentheses(expr.Right)) != nil {
		t = l.checker.GetNonNullableType(l.typeAt(expr.Left)) // `m.get(k) ?? new Map()` is a Map<any, any> to tsc
	}
	required := t
	if !mayHoldUndefined(l.typeAt(expr.Left)) {
		// tsc took x to be never undefined, so its result has x's type, though x may be an
		// element that isn't there (`sorted[1]?.amount ?? "none"`): y's type is added.
		t = l.checker.GetUnionType([]*checker.Type{t, l.typeAt(expr.Right)})
	}
	result := l.irType(t, node)
	y := right()
	if result == nil || y == nil {
		return nil
	}
	if y = l.convert(y, result, expr.Right); y == nil {
		return nil
	}
	tmp := l.newLocal("value", x.Type())
	ref := &ir.LocalRef{Local: tmp}
	coalesced := &ir.Bind{Local: tmp, Value: x, In: &ir.Conditional{Cond: &ir.IsNullish{X: ref}, Then: y, Else: fromUnion(ref, result)}}
	if t != required && !l.takesAnyValue(node) {
		if requiredType := l.irType(required, node); requiredType != nil && requiredType != result {
			return l.checkedConversion(coalesced, requiredType, node)
		}
	}
	return coalesced
}

// unionLogical lowers `x || y` and `x && y` where x is a union, or where the result is
// one: x is kept in a temporary, tested for truth, and the branch taken becomes the
// result's type (`name || "anonymous"` is a string: a truthy name isn't null).
func (l *lowerer) unionLogical(node *ast.Node, op ir.LogicalOp, x ir.Expr, y ir.Expr, right *ast.Node) ir.Expr {
	result := l.irType(l.typeAt(node), node)
	if result == nil {
		return nil
	}
	if y = l.convert(y, result, right); y == nil {
		return nil
	}
	if t := x.Type(); t == ir.Null || t == ir.Undefined {
		if op == ir.Or {
			return discard(x, y) // never truthy
		}
		return l.convert(x, result, node)
	}
	tmp := l.newLocal("value", x.Type())
	ref := &ir.LocalRef{Local: tmp}
	var kept ir.Expr = ref
	if isUnion(x.Type()) {
		kept = fromUnion(ref, result)
	} else if kept = l.convert(ref, result, node); kept == nil {
		return nil
	}
	then, otherwise := y, kept
	if op == ir.Or {
		then, otherwise = kept, y
	}
	return &ir.Bind{Local: tmp, Value: x, In: &ir.Conditional{Cond: truthOf(ref), Then: then, Else: otherwise}}
}

// truthOf is JS's ToBoolean of x, a local's value or a constant.
func truthOf(x ir.Expr) ir.Expr {
	switch t := x.Type(); {
	case t == ir.Bool:
		return x
	case t == ir.Null || t == ir.Undefined:
		return &ir.BoolConst{Value: false}
	case ir.IsNumeric(t) || t == ir.String || t == ir.Value || isUnion(t), isObject(t) || isFunction(t) || isMap(t):
		return &ir.Truthy{X: x} // an object is true but for a class's field the constructor hasn't set yet (D-141)
	}
	return &ir.BoolConst{Value: true}
}

// involvesUnion reports whether `left || right` or `left && right` (node) has a union
// operand or result, as tsc types them.
func (l *lowerer) involvesUnion(node *ast.Node, left *ast.Node, right *ast.Node) bool {
	if l.hasTypeFlags(left, checker.TypeFlagsNull|checker.TypeFlagsUndefined) {
		return true
	}
	return isUnionValue(l.typeAt(node)) || isUnionValue(l.typeAt(left)) || isUnionValue(l.typeAt(right))
}

// isUnionValue reports whether the IR type of values of checker type t is a union (D-062):
// t is a union other than a boolean, one of strings or one of numbers.
func isUnionValue(t *checker.Type) bool {
	if t.Flags()&checker.TypeFlagsUnion == 0 || t.Flags()&checker.TypeFlagsBooleanLike != 0 || allOf(t.Types(), checker.TypeFlagsStringLike) {
		return false
	}
	_, numeric := check.NumericOf(t)
	return !numeric
}

// isMemberOfAll reports whether the property access node reads a member that a union
// receiver's types share as one declaration (an enum's members, a namespace's), which
// lowers as for any receiver; reading a property of the members of a union is M3.1c's.
func (l *lowerer) isMemberOfAll(node *ast.Node) bool {
	symbol := l.checker.GetSymbolAtLocation(node.AsPropertyAccessExpression().Name())
	return symbol != nil && symbol.Flags&(ast.SymbolFlagsEnumMember|ast.SymbolFlagsAlias) != 0
}

// asUnion lowers `x as U` for a union U (of type t) that x's type is a member of, or a
// union of some of its members: an upcast, which is free (docs/SPEC.md §3.5).
func (l *lowerer) asUnion(node *ast.Node, t *checker.Type) ir.Expr {
	u, ok := l.irType(t, node).(*ir.Union)
	x := l.expression(node.Expression())
	if !ok || x == nil {
		return nil
	}
	if isResultUnion(u) && isObjectOfProgram(x.Type()) {
		return asResult(x, u) // a class's instance as an iterator result
	}
	if fits := x.Type() == u || u.Has(x.Type()) || isUnion(x.Type()) && !isNarrowing(x.Type().(*ir.Union), u); !fits {
		l.notSupportedYet(node, fmt.Sprintf("`as` from %s to %s (a downcast) is", x.Type(), u))
		return nil
	}
	return l.convert(x, u, node)
}

// contextTakesUndefined reports whether the context of an expression expects a union that
// includes undefined, which an element read out of range gives there, as anywhere a value
// of any type can go (D-015, D-067).
func (l *lowerer) contextTakesUndefined(node *ast.Node) bool {
	if fallback := fallbackOf(node); fallback != nil {
		// tsc types y in `x ?? y` and `x || y` by x where nothing types the whole, but
		// the result never holds x's undefined, so y's context is the whole's.
		return l.contextTakesUndefined(fallback)
	}
	contextual := l.contextualType(node)
	if contextual == nil || contextual.Flags()&checker.TypeFlagsUnion == 0 || !isUnionValue(contextual) {
		return false
	}
	return slices.ContainsFunc(contextual.Types(), func(t *checker.Type) bool { return t.Flags()&checker.TypeFlagsUndefined != 0 })
}

// fallbackOf is the `x ?? y` or `x || y` whose right operand y is node, or nil.
func fallbackOf(node *ast.Node) *ast.Node {
	for node.Parent.Kind == ast.KindParenthesizedExpression {
		node = node.Parent
	}
	if node.Parent.Kind != ast.KindBinaryExpression {
		return nil
	}
	binary := node.Parent.AsBinaryExpression()
	if operator := binary.OperatorToken.Kind; binary.Right != node || operator != ast.KindQuestionQuestionToken && operator != ast.KindBarBarToken {
		return nil
	}
	return node.Parent
}

// withUndefined is t | undefined.
func (l *lowerer) withUndefined(t ir.Type) ir.Type {
	if u, ok := t.(*ir.Union); ok {
		if u.Has(ir.Undefined) {
			return u
		}
		return l.unionOf(append(slices.Clone(u.Members), ir.Undefined))
	}
	if t == ir.Undefined || t == ir.Value {
		return t
	}
	return l.unionOf([]ir.Type{t, ir.Undefined})
}

// withoutUndefined is u without undefined: its one other member, or a smaller union.
func (l *lowerer) withoutUndefined(u *ir.Union) ir.Type {
	members := slices.DeleteFunc(slices.Clone(u.Members), func(m ir.Type) bool { return m == ir.Undefined })
	if len(members) == 1 {
		return members[0]
	}
	return l.unionOf(members)
}

// nullishAssignment builds `x ??= value` for the place target (read once): x unless it
// is null or undefined, else x = value, which is evaluated only then.
func (l *lowerer) nullishAssignment(target place, valueNode *ast.Node) ir.Expr {
	current, t := target.read(), target.valueType()
	if isObject(t) || isFunction(t) || isMap(t) { // never null or undefined but for an unset class field (D-141)
		value := l.expression(valueNode)
		if value == nil {
			return nil
		}
		if value = l.convert(value, t, valueNode); value == nil {
			return nil
		}
		return &ir.Conditional{Cond: &ir.Truthy{X: current}, Then: current, Else: target.store(value, false)}
	}
	if !isDynamic(t) && t != ir.Null && t != ir.Undefined {
		return current // never null or undefined
	}
	value := l.expression(valueNode)
	if value == nil {
		return nil
	}
	if value = l.convert(value, t, valueNode); value == nil {
		return nil
	}
	if t == ir.Null || t == ir.Undefined {
		return target.store(value, false)
	}
	return &ir.Conditional{Cond: &ir.IsNullish{X: current}, Then: target.store(value, false), Else: current}
}

// assignedValue is x, the value of `target ??= value`, `||=`, `&&=` or `=` (node), as the
// type assignedValueType gives it, with a check, as a narrowed property and a dynamic
// place's read are (D-089, D-140): `&&=` gives whatever falsy value the place holds, and
// a variable or field without a value holds undefined outside its type (D-141). A value
// nothing uses (`x &&= v;`) is left as it is.
func (l *lowerer) assignedValue(target place, x ir.Expr, node *ast.Node) ir.Expr {
	if x == nil || isDiscarded(node) || l.hasTypeFlags(node, checker.TypeFlagsNever) {
		return x
	}
	var p ir.Type // the place's type
	switch u, ok := x.Type().(*ir.Union); {
	case target.dynamic && x.Type() == ir.Value:
		if l.hasTypeFlags(node, checker.TypeFlagsAny|checker.TypeFlagsUnknown) {
			return x
		}
		p = l.irType(l.typeAt(node.AsBinaryExpression().Left), node)
	case ok:
		p = u
	default:
		return l.narrowed(node, x)
	}
	if p == nil {
		return nil
	}
	switch t := l.assignedValueType(p, node); t {
	case nil:
		return nil
	case x.Type():
		return x
	default:
		return l.checkedConversion(x, t, node)
	}
}

// assignedValueType is the type of the value of node, `x ??= value`, `||=`, `&&=` or `=`,
// for a place of type p: tsc's type of node where that is p or narrows it; otherwise p
// without null and undefined where node can't give them (`(cache ??= new Map())` is a
// `Map<any, any>` to tsc, but its value is the place's or was stored there, and `[]` a
// `never[]`); otherwise p.
func (l *lowerer) assignedValueType(p ir.Type, node *ast.Node) ir.Type {
	t := l.irType(l.typeAt(node), node)
	u, ok := p.(*ir.Union)
	switch {
	case t == nil || t == p || ok && isNarrowing(u, t):
		return t
	case ok && l.storesNonNullish(node):
		if rest := l.withoutNullish(u); rest != nil {
			return rest
		}
	}
	return p
}

// isDiscarded reports whether nothing uses node's value: it is an expression statement, a
// comma's left operand (or right, if the comma's value is discarded), void's operand or a
// for loop's update.
func isDiscarded(node *ast.Node) bool {
	child, parent := node, node.Parent
	for parent != nil && parent.Kind == ast.KindParenthesizedExpression {
		child, parent = parent, parent.Parent
	}
	if parent == nil {
		return false
	}
	switch parent.Kind {
	case ast.KindExpressionStatement, ast.KindVoidExpression:
		return true
	case ast.KindBinaryExpression:
		comma := parent.AsBinaryExpression()
		return comma.OperatorToken.Kind == ast.KindCommaToken && (comma.Left == child || isDiscarded(parent))
	case ast.KindForStatement:
		return parent.AsForStatement().Incrementor == child
	}
	return false
}

// storesNonNullish reports whether node, `x ??= value`, `x ||= value` or `x = value`,
// gives x or value, and value can't be null or undefined, so its value is never null or
// undefined either.
func (l *lowerer) storesNonNullish(node *ast.Node) bool {
	expr := node.AsBinaryExpression()
	switch expr.OperatorToken.Kind {
	case ast.KindQuestionQuestionEqualsToken, ast.KindBarBarEqualsToken, ast.KindEqualsToken:
		return !mayBeNullish(l.typeAt(expr.Right))
	}
	return false
}

// mayBeNullish reports whether a value of type t may be null or undefined.
func mayBeNullish(t *checker.Type) bool {
	isNull := func(t *checker.Type) bool { return t.Flags()&checker.TypeFlagsNull != 0 }
	return mayHoldUndefined(t) || isNull(t) || t.Flags()&checker.TypeFlagsUnion != 0 && slices.ContainsFunc(t.Types(), isNull)
}

// withoutNullish is the union u without null and undefined: its one other member, a
// smaller union, or nil if it has no other members.
func (l *lowerer) withoutNullish(u *ir.Union) ir.Type {
	members := slices.DeleteFunc(slices.Clone(u.Members), func(m ir.Type) bool { return m == ir.Null || m == ir.Undefined })
	switch len(members) {
	case 0:
		return nil
	case 1:
		return members[0]
	}
	return l.unionOf(members)
}

// unsetFieldFallback lowers `x ?? y` for x of an object type, which is never null or
// undefined but for a class's field the constructor hasn't set (D-141): for a field of a
// local's object (`this.cache ?? new Map()`), y stands in for it while it is unset, if y
// has its type. Otherwise y is never evaluated, as before.
func (l *lowerer) unsetFieldFallback(x ir.Expr, right *ast.Node) ir.Expr {
	field, ok := x.(*ir.FieldRef)
	if !ok || !isPure(field.X) || !l.checker.IsTypeAssignableTo(l.typeAt(right), l.typeAt(right.Parent.AsBinaryExpression().Left)) {
		return x
	}
	y := l.deadOperand(right) // an instance's type arguments make it dead (D-195)
	if y == nil {
		y = l.expression(right)
	}
	if y == nil {
		return nil
	}
	if y = l.convert(y, x.Type(), right); y == nil {
		return nil
	}
	return &ir.Conditional{Cond: &ir.Truthy{X: x}, Then: &ir.FieldRef{X: field.X, Field: field.Field}, Else: y}
}

// takesUnion reports whether a parameter of type param takes values of the union u as
// they are.
func (l *lowerer) takesUnion(param ir.Type, u *ir.Union) bool {
	p, ok := param.(*ir.Union)
	return ok && !slices.ContainsFunc(u.Members, func(m ir.Type) bool { return !p.Has(m) })
}

// maybePassed is the argument and Given flag (D-055) for an optional parameter of type
// param of x, a value of a union that includes undefined or a dynamic value: undefined
// means the argument is left out, as in JS. x is read twice, so it must be pure
// (inTemporaries puts a computed one in a temporary).
func (l *lowerer) maybePassed(x ir.Expr, param ir.Type, node *ast.Node) []ir.Expr {
	present := x
	if u, ok := x.Type().(*ir.Union); ok {
		present = fromUnion(x, l.withoutUndefined(u))
	}
	value := l.convert(present, param, node)
	if value == nil {
		return nil
	}
	missing := &ir.IsNullish{X: x, Only: ir.Undefined}
	return []ir.Expr{&ir.Conditional{Cond: missing, Then: &ir.LeftOut{T: param}, Else: value}, &ir.Unary{Op: ir.Not, X: missing}}
}
