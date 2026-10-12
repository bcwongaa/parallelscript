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
		if node.Kind == ast.KindIdentifier && l.isLoose(l.target(node)) {
			if l.isTestedOperand(node) {
				return x // tested for undefined: the union it holds
			}
			return l.looseRead(x, narrow, node) // it may be undefined, which tsc doesn't know (D-090)
		}
		if node.Kind != ast.KindIdentifier && isCheckable(narrow) && !isClass(narrow) && !isUnion(narrow) {
			// A property tsc narrowed may have changed since, through a call that tsc
			// doesn't follow, so its value is checked (classes' always are) (D-089).
			return &ir.FromUnion{X: box(x), To: narrow}
		}
		return &ir.FromUnion{X: x, To: narrow}
	}
	return x
}

// toUnion converts value, of a member of u or a union of some of them, to u; nil if it
// is neither (reported at node).
func (l *lowerer) toUnion(value ir.Expr, u *ir.Union, node *ast.Node) ir.Expr {
	switch from := value.Type().(type) {
	case *ir.Union:
		if !slices.ContainsFunc(from.Members, func(m ir.Type) bool { return !u.Has(m) }) || l.holdsMembersOf(node, u) {
			return &ir.ToUnion{X: value, To: u}
		}
	default:
		if u.Has(from) {
			return &ir.ToUnion{X: value, To: u}
		}
		if member := l.memberFor(from, u); member != nil {
			if converted := l.convert(value, member, node); converted != nil {
				return &ir.ToUnion{X: converted, To: u}
			}
			return nil
		}
	}
	l.notSupportedYet(node, fmt.Sprintf("converting %s to %s is", value.Type(), u))
	return nil
}

// memberFor is the member of u that a value of type t converts to: the one numeric
// member for a number, the one function type for a function, or the one member that
// is an object type for an object; nil if there isn't exactly one.
func (l *lowerer) memberFor(t ir.Type, u *ir.Union) ir.Type {
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
			if found != nil {
				return providedMember(t, u) // several object members: the one t provides as it is
			}
			found = m
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
	if left := ast.SkipParentheses(expr.Left); l.isArrayElement(left) || l.isTypedArrayElement(left) || l.isStringElement(left) {
		x = l.valueExpression(left) // `digits[i] ?? 0`: undefined past the end, as in JS (D-089)
	} else {
		x = l.expression(expr.Left)
	}
	if x == nil {
		return nil
	}
	switch t := x.Type(); {
	case t == ir.Null || t == ir.Undefined:
		y := l.expression(expr.Right)
		if y == nil {
			return nil
		}
		return discard(x, y)
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
	y := l.expression(expr.Right)
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
	case ir.IsNumeric(t) || t == ir.String || t == ir.Value || isUnion(t):
		return &ir.Truthy{X: x}
	}
	return &ir.BoolConst{Value: true} // an object or function, never null
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
	if !isUnion(t) && t != ir.Null && t != ir.Undefined {
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
