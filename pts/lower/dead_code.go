package lower

import (
	"fmt"
	"slices"
	"strings"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/internal/scanner"
	"github.com/microsoft/typescript-go/pts/ir"
)

// Code that an instance's type arguments make dead (C171, D-195). tsc narrows a generic
// function's references in the generic code: `x` is `T & number` after `typeof x ===
// "number"` and `T & any[]` after `Array.isArray(x)`. For an instance's type arguments such
// a type may be one that no value has (`string & number`, which is never, or `number &
// any[]`), and then no run reaches the read: the test that narrowed it is false for every
// value of the instance's types, which the instance's typed parameters and checked
// conversions guarantee. A unit of code that holds such a read where it starts (a
// statement, a branch of `?:`, the right operand of `&&` or `||`, an arrow function's
// expression body) lowers as a trap that throws TypeError, which only a type predicate,
// assertion, cast or declared result that lied can reach, instead of code that the
// instance's types can't express.
//
// Code that tsc itself narrows to a type no value of the read's declared type has (plain
// code's `typeof x === "number"` for an `x: string`, generic code's for an `x: T & string`)
// is dead the same way where it holds no value its tests didn't rule out (C220, D-197): the
// read is of a variable that only its own function assigns and reads, not a loose local;
// no value of its declared type's IR type passes the tests either; and kind tests narrowed
// it, not tests of its value against others (testsValues): an enum holds any number, so an
// exhausted switch over one runs. Elsewhere it may be live without a lie (a property, an
// element, a variable another function assigns: tsc keeps narrowings across calls and
// awaits it doesn't follow) and reads what it holds (narrowed). Uses that take its declared
// type (a cast back to it, an `assertNever`, a log) read it as before (takesAnyValueAsIs).

// deadRead is a read in unit, evaluated where unit starts with no narrowing in between,
// that has no value there (isDeadRead): unit can't run. It is nil if unit has no such read.
func (l *lowerer) deadRead(unit *ast.Node) *ast.Node {
	var dead *ast.Node
	var visit func(node *ast.Node) bool
	visit = func(node *ast.Node) bool {
		if l.isDeadRead(node) && !l.takesAnyValueAsIs(node) {
			dead = node
			return true
		}
		return slices.ContainsFunc(evaluatedFirst(node), visit)
	}
	visit(unit)
	return dead
}

// takesAnyValueAsIs reports whether node is read where any value goes as it is, so its
// narrowed type doesn't matter: where its context's type is any or unknown (an argument of
// `String`, `JSON.stringify` or a `console` method, an element of a literal there, an
// `unknown` parameter), the operand of `typeof`, a template literal's placeholder, and an
// operand of `+` whose other operand is a string or of `===` and `!==`; not a copy (`const
// y = x`), whose type is the narrowed one that later code reads; and a read tsc itself
// narrows to no value where its declared type goes (goesAsItsDeclaredType: `assertNever(x)`,
// a cast back to it, D-197). A dead read there (one a type predicate that lied lets run: `if
// (isNumber(x)) console.log("number", x)`) lowers as the value x holds, as before, rather
// than making its unit a trap (D-195).
func (l *lowerer) takesAnyValueAsIs(node *ast.Node) bool {
	for node.Parent.Kind == ast.KindParenthesizedExpression {
		node = node.Parent
	}
	for value := node; ; {
		if contextual := l.checker.GetContextualType(value, checker.ContextFlagsNone); contextual != nil && contextual.Flags()&(checker.TypeFlagsAny|checker.TypeFlagsUnknown) != 0 {
			return true
		}
		switch parent := value.Parent; { // an element or property of a literal that goes where any value does: `console.log([x])`
		case parent.Kind == ast.KindArrayLiteralExpression || parent.Kind == ast.KindParenthesizedExpression ||
			parent.Kind == ast.KindSpreadElement && value.Kind == ast.KindArrayLiteralExpression: // a spread of the read itself iterates it
			value = parent
			continue
		case parent.Kind == ast.KindShorthandPropertyAssignment || parent.Kind == ast.KindPropertyAssignment && parent.Initializer() == value:
			value = parent.Parent
			continue
		}
		break
	}
	if l.goesAsItsDeclaredType(node) {
		return true
	}
	switch parent := node.Parent; parent.Kind {
	case ast.KindTypeOfExpression, ast.KindTemplateSpan:
		return true
	case ast.KindBinaryExpression:
		binary := parent.AsBinaryExpression()
		other := binary.Left
		if other == node {
			other = binary.Right
		}
		switch operator := binary.OperatorToken.Kind; operator {
		case ast.KindEqualsEqualsEqualsToken, ast.KindExclamationEqualsEqualsToken:
			return true // any two values compare (neverStrictlyEqual)
		case ast.KindPlusToken, ast.KindPlusEqualsToken:
			return (operator == ast.KindPlusToken || other == binary.Left) && l.checker.GetTypeAtLocation(other).Flags()&checker.TypeFlagsStringLike != 0
		}
	}
	return false
}

// evaluatedFirst lists the children of node that run whenever node does, before anything
// that could narrow: not nested statements and functions, nor the branches of `?:`, the
// right operands of `&&`, `||` and `??`, an optional chain past its `?.`, a loop's body,
// condition after the first iteration or update, or a switch's cases, whose own reads may
// be narrowed by what runs before them.
func evaluatedFirst(node *ast.Node) []*ast.Node {
	if ast.IsOptionalChain(node) {
		return []*ast.Node{node.Expression()}
	}
	var children []*ast.Node
	switch node.Kind {
	case ast.KindExpressionStatement, ast.KindReturnStatement, ast.KindThrowStatement, ast.KindIfStatement,
		ast.KindWhileStatement, ast.KindForOfStatement, ast.KindForInStatement, ast.KindSwitchStatement, ast.KindExportAssignment:
		children = []*ast.Node{node.Expression()}
	case ast.KindForStatement:
		children = []*ast.Node{node.Initializer()}
	case ast.KindVariableStatement:
		children = []*ast.Node{node.AsVariableStatement().DeclarationList}
	case ast.KindVariableDeclaration:
		children = []*ast.Node{node.Initializer()} // not its binding pattern's defaults
	case ast.KindConditionalExpression:
		children = []*ast.Node{node.AsConditionalExpression().Condition}
	case ast.KindBinaryExpression:
		binary := node.AsBinaryExpression()
		if operator := binary.OperatorToken.Kind; ast.IsLogicalOrCoalescingBinaryOperator(operator) || ast.IsLogicalOrCoalescingAssignmentOperator(operator) {
			children = []*ast.Node{binary.Left}
			break
		}
		children = allChildren(node)
	default:
		if ast.IsStatement(node) || ast.IsFunctionLike(node) || ast.IsClassLike(node) || ast.IsTypeNode(node) {
			return nil
		}
		children = allChildren(node)
	}
	return slices.DeleteFunc(children, func(child *ast.Node) bool { return child == nil })
}

// allChildren lists node's children.
func allChildren(node *ast.Node) []*ast.Node {
	var children []*ast.Node
	node.ForEachChild(func(child *ast.Node) bool {
		children = append(children, child)
		return false
	})
	return children
}

// isDeadRead reports whether node reads a variable or property (`o.v`, a tuple's `t[0]`)
// that has no value there: tsc narrows it to a type no value of its declared type has,
// where it holds no value its tests didn't see (narrowsToNoValue, mayHoldUntestedValue),
// or the instance being lowered does. For an instance, no value of its declared type has the type of the tests
// that narrow it (their intersection: `string & number` for a string T's `x` after `typeof
// x === "number"`, `string & null` and `Ci & Sq` where tsc narrows a `T extends string |
// null` to null, or a `T extends Sq | Ci` to Sq, through the constraint), while in the
// generic code some value has tsc's narrowed type, and the declared type has values for
// the instance (an element of a `never[]`, of type never, is undefined). The tests' type is
// tsc's narrowed type without the instance's type parameters it intersects (testedType):
// tsc keeps a narrowing across what it doesn't follow (a closure's assignment, a call that
// changes a property, an await, a getter), so the value may have changed since, though
// never to one its declared type doesn't have, since stores are typed and checked.
func (l *lowerer) isDeadRead(node *ast.Node) bool {
	declared, narrowed := l.readTypes(node)
	switch {
	case declared == nil:
		return false
	case l.narrowsToNoValue(declared, narrowed):
		return !l.mayHoldUntestedValue(node, declared, narrowed)
	case l.subst == nil || len(l.subst.parameters) == 0 || l.uninhabited(l.checker.PtsIntersectionType([]*checker.Type{declared, narrowed})):
		return false
	}
	instance := l.subst
	l.subst = l.deadTest()
	defer func() { l.subst = instance }()
	if declared = l.instantiate(declared); l.uninhabited(declared) {
		return false
	}
	return l.uninhabited(l.checker.PtsIntersectionType([]*checker.Type{declared, l.instantiate(l.testedType(narrowed))}))
}

// readTypes are the declared type and tsc's narrowed type of node, a read of a variable or
// property (`o.v`, a tuple's `t[0]`, `o["v"]`), or nils if node is none: a write, an
// optional chain (whose own undefined isn't the property's) or another expression.
func (l *lowerer) readTypes(node *ast.Node) (declared, narrowed *checker.Type) {
	var symbol *ast.Symbol
	switch node.Kind {
	case ast.KindIdentifier:
		if ast.IsDeclarationName(node) && node.Parent.Kind != ast.KindShorthandPropertyAssignment {
			return nil, nil
		}
		if symbol = l.readSymbol(node); symbol == nil || symbol.Flags&ast.SymbolFlagsVariable == 0 {
			return nil, nil
		}
	case ast.KindPropertyAccessExpression: // a field or a getter
		if symbol = l.checker.GetSymbolAtLocation(node.Name()); symbol == nil || symbol.Flags&(ast.SymbolFlagsProperty|ast.SymbolFlagsGetAccessor) == 0 {
			return nil, nil
		}
	case ast.KindElementAccessExpression: // `t[0]` of a tuple, `o["v"]`: a literal key that names a property, not `o[c.key]`'s key
		key := node.AsElementAccessExpression().ArgumentExpression
		if !ast.IsStringOrNumericLiteralLike(key) {
			return nil, nil
		}
		if symbol = l.checker.GetSymbolAtLocation(key); symbol == nil || symbol.Flags&ast.SymbolFlagsProperty == 0 {
			return nil, nil
		}
	default:
		return nil, nil
	}
	if ast.IsWriteOnlyAccess(node) || ast.IsOptionalChain(node) {
		return nil, nil
	}
	return l.checker.GetTypeOfSymbol(symbol), l.checker.GetTypeAtLocation(node)
}

// narrowsToNoValue reports whether narrowed, tsc's narrowed type of a read, has no value
// of declared, the read's declared type, which has values: never (`typeof x === "number"`
// for an `x: string`, an exhausted union), or an intersection of disjoint types (`number &
// any[]` after Array.isArray for a number, `Pt & Other` after instanceof).
func (l *lowerer) narrowsToNoValue(declared, narrowed *checker.Type) bool {
	return narrowed != declared && l.uninhabited(l.checker.PtsIntersectionType([]*checker.Type{declared, narrowed})) && !l.uninhabited(declared)
}

// noValueOfItsIRTypePasses reports whether no value of declared's IR type has the type the
// tests before node, a read of that declared type, narrow it to, where tsc narrows it to
// narrowed, which no value of declared has. An intersection of disjoint kinds or classes
// (`Color & any[]`) has no value of the IR type either. tsc's never may only mean that the
// tests ran out of declared's own values (an enum's members, a literal union's strings, an
// object union's discriminants), where its IR type holds others (an enum holds any number,
// and a cast to a literal union or an object type isn't checked for them): so the tests
// are run again from the widest type of declared's IR type (widestOfIRType), and pass none
// of its values only if tsc narrows that to no value too (`typeof d === "number"` for a `d:
// "up" | "down"` does; an exhausted switch over an enum doesn't). In an instance, declared
// is for its type arguments widened (deadTest).
func (l *lowerer) noValueOfItsIRTypePasses(node *ast.Node, declared, narrowed *checker.Type) bool {
	if !isNever(narrowed) && !isNever(l.checker.PtsReducedType(narrowed)) {
		return true
	}
	at := node
	if l.subst != nil && len(l.subst.parameters) > 0 {
		instance := l.subst
		l.subst = l.deadTest()
		defer func() { l.subst = instance }()
		declared, at = l.instantiate(declared), l.subst.declaration
	}
	widest := l.widestOfIRType(declared, at)
	return widest == declared || l.uninhabited(l.checker.PtsFlowTypeOfReference(node, widest))
}

// mayBeStale reports whether node, a reference tsc narrowed, may hold a value the tests
// that narrowed it didn't see: it may have changed through a call tsc doesn't follow
// (mayHaveChanged), or it is a variable assigned after its declaration and read in another
// function than its own, which may have run in between while this one awaited or yielded.
func (l *lowerer) mayBeStale(node *ast.Node) bool {
	if l.mayHaveChanged(node) {
		return true
	}
	symbol := l.readSymbol(node)
	return l.assignedAnywhere[symbol] && functionOf(symbol.ValueDeclaration) != functionOf(node)
}

// mayHoldUntestedValue reports whether node, a read that tsc narrows to narrowed, which no
// value of its declared type has, may hold a value its tests didn't rule out: one they
// didn't see (mayBeStale), one its declared type leaves out (a loose local's undefined:
// `const [a, b] = xs` past xs's end, D-090, D-172), or one of its declared type's IR type
// that tsc's narrowing ran out of without testing (noValueOfItsIRTypePasses: an enum's
// other numbers, a string an `any` or a cast made a `"up" | "down"`), as where a test
// compared its value with others (testsValues: an exhausted switch on an enum or a class's
// literal field, `code === expected` for an enum `expected`).
func (l *lowerer) mayHoldUntestedValue(node *ast.Node, declared, narrowed *checker.Type) bool {
	return l.mayBeStale(node) || node.Kind == ast.KindIdentifier && l.isLoose(l.readSymbol(node)) ||
		!l.noValueOfItsIRTypePasses(node, declared, narrowed) || l.testsValues(node)
}

// narrowsReadToNoValue reports whether node is a read that tsc narrows to a type no value
// of its declared type has (narrowsToNoValue).
func (l *lowerer) narrowsReadToNoValue(node *ast.Node) bool {
	declared, narrowed := l.readTypes(node)
	return declared != nil && l.narrowsToNoValue(declared, narrowed)
}

// readsAsItsDeclaredType reports whether node, a read that tsc narrows to a type no value
// of its declared type has (narrowsReadToNoValue), reads as its declared type, as before:
// where that type goes (goesAsItsDeclaredType) or indexes (`names[c]` for an enum's other
// number), and if it may hold no untested value (code that can't run), also where any value
// goes (takesAnyValueAsIs). Elsewhere it holds what it holds, as a dynamic value (narrowed).
func (l *lowerer) readsAsItsDeclaredType(node *ast.Node) bool {
	declared, narrowed := l.readTypes(node)
	if l.mayHoldUntestedValue(node, declared, narrowed) {
		return l.goesAsItsDeclaredType(node) || l.isKeyOfItsType(node, declared) // stale reads' tests (`o.v === 5`) compare dynamic values (D-196)
	}
	return l.takesAnyValueAsIs(node)
}

// isKeyOfItsType reports whether node is the key of an element access (`i` of `a[i]`),
// whose declared type's IR type, number or string, is one a key can be; a union's isn't
// (`Kind | "none"`), and is found at run time from the value boxed.
func (l *lowerer) isKeyOfItsType(node *ast.Node, declared *checker.Type) bool {
	for node.Parent.Kind == ast.KindParenthesizedExpression {
		node = node.Parent
	}
	if node.Parent.Kind != ast.KindElementAccessExpression || node.Parent.AsElementAccessExpression().ArgumentExpression != node {
		return false
	}
	var key ir.Type
	l.quietly(func() bool {
		key = l.irType(declared, node)
		return key != nil
	})
	return key != nil && (ir.IsNumeric(key) || key == ir.String)
}

// goesAsItsDeclaredType reports whether node is a read that tsc narrows to a type no value
// of its declared type has (narrowsToNoValue), where its declared type goes too: where
// only such reads go, declared never (`assertNever(s)`, `const u: never = s`, not an
// argument whose never tsc inferred, as `new Box(s)`'s), or where its context's type takes
// its declared type (`s as Shape`, `return x` from a function of x's declared type). Such
// a read lowers as its declared type, as live code's does, which a lie that reaches it
// gets as in Node.
func (l *lowerer) goesAsItsDeclaredType(node *ast.Node) bool {
	if !l.narrowsReadToNoValue(node) {
		return false
	}
	declared, _ := l.readTypes(node)
	for node.Parent.Kind == ast.KindParenthesizedExpression || node.Parent.Kind == ast.KindNonNullExpression {
		node = node.Parent
	}
	contextual := l.checker.GetContextualType(node, checker.ContextFlagsNone)
	switch {
	case contextual == nil:
		return false
	case isNever(contextual):
		return !l.isInferredParameter(node)
	}
	return l.checker.IsTypeAssignableTo(declared, contextual)
}

// isInferredParameter reports whether node is an argument whose parameter tsc typed never
// by inference, not by its declaration: a type parameter's (`new Box(x)`'s T), where the
// called signature's declaration doesn't declare it never (`exhaustive<T>(x: never,
// fallback: T)`'s x is declared so).
func (l *lowerer) isInferredParameter(node *ast.Node) bool {
	call := node.Parent
	if !ast.IsCallOrNewExpression(call) || node == call.Expression() {
		return false
	}
	signature := l.checker.GetResolvedSignature(call)
	if signature == nil {
		return false
	}
	for signature.Target() != nil {
		signature = signature.Target()
	}
	index, parameters := slices.Index(call.Arguments(), node), signature.Parameters()
	if index < 0 || len(parameters) == 0 {
		return false
	}
	return !isNever(l.checker.GetTypeOfSymbol(parameters[min(index, len(parameters)-1)]))
}

// testedType is narrowed, tsc's narrowed type of a read, without the members of its
// intersections that mention the instance's type parameters (`T & number` is number,
// `T["a"] & number` too, `(T & undefined) | T` unknown): what the tests that narrowed the
// read say of its value; such members are the declared type's, which the tests may have
// narrowed before the value changed. A type that isn't an intersection stays (`keyof T`).
func (l *lowerer) testedType(narrowed *checker.Type) *checker.Type {
	switch {
	case slices.Contains(l.subst.parameters, narrowed):
		return l.checker.GetUnknownType()
	case narrowed.Flags()&checker.TypeFlagsUnion != 0:
		members := make([]*checker.Type, len(narrowed.Types()))
		for i, member := range narrowed.Types() {
			members[i] = l.testedType(member)
		}
		return l.checker.GetUnionType(members)
	case narrowed.Flags()&checker.TypeFlagsIntersection != 0:
		members := slices.DeleteFunc(slices.Clone(narrowed.Types()), l.mentionsParameters)
		if len(members) == 0 {
			return l.checker.GetUnknownType()
		}
		return l.checker.PtsIntersectionType(members)
	}
	return narrowed
}

// mentionsParameters reports whether t mentions one of the instance's type parameters:
// replacing them by unknown changes it.
func (l *lowerer) mentionsParameters(t *checker.Type) bool {
	unknowns := make([]*checker.Type, len(l.subst.parameters))
	for i := range unknowns {
		unknowns[i] = l.checker.GetUnknownType()
	}
	return l.checker.PtsInstantiateType(t, l.subst.parameters, unknowns) != t
}

// deadTest is the substitution isDeadRead tests reads with: the instance's, each type
// argument the widest type of its IR type (widestOfIRType). Instances are told apart by IR
// types (a function's also by literal type arguments), so one instance's code runs for
// every type argument of those IR types: `Holder<Id>`'s methods for a branded string Id are
// `Holder<string>`'s, and `Holder<Json>`'s `Holder<unknown>`'s, which hold numbers too.
// Code is dead only if it is for all of them.
func (l *lowerer) deadTest() *substitution {
	if test, ok := l.deadTests[l.subst]; ok {
		return test
	}
	test := *l.subst
	test.arguments = make([]*checker.Type, len(l.subst.arguments))
	for i, argument := range l.subst.arguments {
		test.arguments[i] = l.widestOfIRType(argument, l.subst.declaration)
	}
	l.deadTests[l.subst] = &test
	return &test
}

// widestOfIRType is the widest type whose values have t's IR type (at node, for any
// report), or unknown where that isn't one this can name: number or string for an enum
// type, a primitive's base type for a literal type, t itself for a type that holds no
// literal types and no values of dynamic IR types, a union of those, any, unknown and
// never as they are.
func (l *lowerer) widestOfIRType(t *checker.Type, node *ast.Node) *checker.Type {
	if primitive := l.brandedPrimitive(t); primitive != nil {
		t = primitive // `string & { __brand: "id" }` is a string
	}
	switch {
	case t.Flags()&(checker.TypeFlagsAny|checker.TypeFlagsUnknown|checker.TypeFlagsNever) != 0:
		return t
	case l.isDynamicIRType(t, node):
		return l.checker.GetUnknownType() // a recursive union's (`Json`), a promise's: unknown's
	case t.Flags()&checker.TypeFlagsEnumLike != 0:
		return l.enumRepresentation(t) // an enum holds any number (or string) of its IR type
	case t.Flags()&checker.TypeFlagsPrimitive != 0:
		return l.checker.GetBaseTypeOfLiteralType(t) // `"a"` and `string` are one IR type
	case t.Flags()&checker.TypeFlagsUnion != 0:
		members := make([]*checker.Type, len(t.Types()))
		for i, member := range t.Types() {
			if members[i] = l.widestOfIRType(member, node); members[i].Flags()&checker.TypeFlagsUnknown != 0 {
				return members[i]
			}
		}
		return l.checker.GetUnionType(members)
	case l.literalFree(t, map[*checker.Type]bool{}, 0):
		return t
	}
	return l.checker.GetUnknownType()
}

// isDynamicIRType reports whether t's IR type is that of dynamic values, unknown's, or t
// has none (at node). t is a type argument of the instance being lowered, or the declared
// type of a read, whose IR type was already made: asking makes nothing new.
func (l *lowerer) isDynamicIRType(t *checker.Type, node *ast.Node) bool {
	var irType ir.Type
	reports := l.reports
	lowered := l.quietly(func() bool {
		irType = l.irType(t, node)
		return irType != nil && l.reports == reports
	})
	return !lowered || irType == ir.Value
}

// enumRepresentation is the primitive type an enum type t's values have: number for a
// numeric enum, string for a string one; unknown for an enum of both.
func (l *lowerer) enumRepresentation(t *checker.Type) *checker.Type {
	switch {
	case t.Flags()&checker.TypeFlagsNumberLike != 0 || allMembers(t, checker.TypeFlagsNumberLike):
		return l.checker.GetNumberType()
	case t.Flags()&checker.TypeFlagsStringLike != 0 || allMembers(t, checker.TypeFlagsStringLike):
		return l.checker.GetStringType()
	}
	return l.checker.GetUnknownType()
}

// allMembers reports whether t is a union whose members all have one of flags.
func allMembers(t *checker.Type, flags checker.TypeFlags) bool {
	return t.Flags()&checker.TypeFlagsUnion != 0 && !slices.ContainsFunc(t.Types(), func(member *checker.Type) bool { return member.Flags()&flags == 0 })
}

// maxLiteralFreeDepth bounds how deep literalFree looks into a type's properties.
const maxLiteralFreeDepth = 4

// literalFree reports whether t holds no literal types (`{ kind: "sq" }`'s, which shares
// its IR type with `{ kind: "ci" }`'s) or branded primitives (string's IR type), and no
// types of dynamic values' IR type other than any and unknown (a promise, `{}`, a union
// that holds itself, as `Json` does), in its type arguments, properties, index signatures
// and call signatures: then no other type of its IR type is narrower. It asks no IR types,
// which a phantom type argument (`Tagged<Weird<string>>`'s) would make. A class of the
// program or the library is its own IR type.
func (l *lowerer) literalFree(t *checker.Type, seen map[*checker.Type]bool, depth int) bool {
	return l.literalFreeIn(t, seen, map[*checker.Type]bool{}, depth)
}

// literalFreeIn is literalFree, unions holding the unions being looked into.
func (l *lowerer) literalFreeIn(t *checker.Type, seen, unions map[*checker.Type]bool, depth int) bool {
	flags := t.Flags()
	switch {
	case unions[t]:
		return false // a union that holds itself: D-089's dynamic values
	case seen[t]:
		return true
	case flags&(checker.TypeFlagsBoolean|checker.TypeFlagsAny|checker.TypeFlagsUnknown) != 0:
		return true
	case flags&checker.TypeFlagsPrimitive != 0:
		return flags&(checker.TypeFlagsLiteral|checker.TypeFlagsEnumLike|checker.TypeFlagsTemplateLiteral|checker.TypeFlagsStringMapping|checker.TypeFlagsUniqueESSymbol) == 0
	case flags&checker.TypeFlagsUnion != 0:
		unions[t] = true
		defer delete(unions, t)
		return !slices.ContainsFunc(t.Types(), func(member *checker.Type) bool { return !l.literalFreeIn(member, seen, unions, depth) })
	case flags&checker.TypeFlagsObject == 0 || depth > maxLiteralFreeDepth: // a type parameter (a generic signature's), an intersection
		return false
	case l.isPromiseType(t) || l.isIteratorType(t) || l.isIterableType(t) || l.isAsyncIteratorType(t) || l.isTimerType(t) || l.checker.IsEmptyAnonymousObjectType(t):
		return false // values of dynamic IR type
	}
	seen[t] = true
	free := func(u *checker.Type) bool { return l.literalFreeIn(u, seen, unions, depth+1) }
	if t.ObjectFlags()&checker.ObjectFlagsReference != 0 && slices.ContainsFunc(l.checker.GetTypeArguments(t), func(u *checker.Type) bool { return !free(u) }) {
		return false
	}
	symbol := t.Symbol()
	if l.checker.IsArrayType(t) || checker.IsTupleType(t) || symbol != nil && (symbol.Flags&ast.SymbolFlagsClass != 0 || l.valueKind(t) == kindInstance) {
		return true // a class of the program's or the library's (Uint8Array, Error, Map) is its own IR type
	}
	for _, signature := range l.checker.GetSignaturesOfType(t, checker.SignatureKindCall) {
		if !free(l.checker.GetReturnTypeOfSignature(signature)) {
			return false // `() => 1` shares `() => number`'s IR type
		}
		for _, parameter := range signature.Parameters() {
			if !free(l.checker.GetTypeOfSymbol(parameter)) {
				return false
			}
		}
	}
	for _, property := range l.checker.GetPropertiesOfType(t) {
		if !free(l.checker.GetTypeOfSymbol(property)) {
			return false
		}
	}
	for _, info := range l.checker.GetIndexInfosOfType(t) {
		if !free(info.KeyType()) || !free(info.ValueType()) {
			return false // `{ [k: string]: J2 }` of a `J2` that recurs through it
		}
	}
	return true
}

// uninhabited reports whether no value has type t: never, a union of such types, or an
// intersection of two disjoint types, which tsc keeps as it is: `number & any[]` after
// Array.isArray, `string & Function` after `typeof x === "function"`, `Pt & any[]`,
// `string & Error` or `Pt & Other` after instanceof. A primitive with an object type that lists properties (`string & {
// __brand: "id" }`) has values: branded types; so has a string that is a String.
func (l *lowerer) uninhabited(t *checker.Type) bool {
	switch {
	case isNever(t) || isNever(l.checker.PtsReducedType(t)): // `Ci & Sq` of conflicting discriminants
		return true
	case t.Flags()&checker.TypeFlagsUnion != 0:
		return !slices.ContainsFunc(t.Types(), func(member *checker.Type) bool { return !l.uninhabited(member) })
	case t.Flags()&checker.TypeFlagsIntersection != 0:
		members := t.Types()
		for i, a := range members {
			for _, b := range members[i+1:] {
				if l.disjoint(a, b) {
					return true
				}
			}
		}
	}
	return false
}

// disjoint reports whether no value has both types a and b, neither of which includes the
// other: they are of different kinds, or instances of different classes (a value is of one
// class and its superclasses).
func (l *lowerer) disjoint(a, b *checker.Type) bool {
	kindA, kindB := l.valueKind(a), l.valueKind(b)
	if kindA == kindUnknown || kindB == kindUnknown || kindA == kindB && (kindA != kindInstance || a.Symbol() == b.Symbol()) {
		return false
	}
	return !l.checker.IsTypeAssignableTo(a, b) && !l.checker.IsTypeAssignableTo(b, a)
}

// valueKind is what a value of a type is, where the type says: no value is of two kinds.
type valueKind int

const (
	kindUnknown   valueKind = iota // a type parameter, an object type that lists properties, …
	kindPrimitive                  // a string, number, boolean, null, undefined, …
	kindArray                      // an array or tuple
	kindFunction                   // a function
	kindInstance                   // an instance of a class: the program's (none extends Array) or the library's
)

// valueKind is the kind of t's values: a function's for a type with call or construct
// signatures and the library's Function, an instance's for a class of the program or one
// of the library's (an interface with a constructor of its name: Map, Date, Error).
func (l *lowerer) valueKind(t *checker.Type) valueKind {
	switch {
	case t.Flags()&checker.TypeFlagsPrimitive != 0:
		return kindPrimitive
	case t.Flags()&checker.TypeFlagsObject == 0:
		return kindUnknown
	case l.checker.IsArrayType(t) || checker.IsTupleType(t):
		return kindArray
	case len(l.checker.GetSignaturesOfType(t, checker.SignatureKindCall)) > 0 || len(l.checker.GetSignaturesOfType(t, checker.SignatureKindConstruct)) > 0:
		return kindFunction
	}
	symbol := t.Symbol()
	switch {
	case symbol == nil:
		return kindUnknown
	case symbol.Flags&ast.SymbolFlagsClass != 0:
		return kindInstance
	case symbol.Flags&ast.SymbolFlagsInterface == 0 || symbol.Flags&ast.SymbolFlagsVariable == 0 || !l.isDeclaredInDefaultLib(symbol):
		return kindUnknown
	case symbol.Name == "Function":
		return kindFunction
	}
	return kindInstance
}

// isPrimitive reports whether values of t are primitives: numbers, strings, booleans, null
// and undefined.
func isPrimitive(t ir.Type) bool {
	return ir.IsNumeric(t) || t == ir.String || t == ir.Bool || isNullish(t)
}

// neverStrictlyEqual reports whether values of types x and y are never strictly equal: a
// primitive of one kind and a primitive of another (a number and a string, null and
// undefined) or an object, an array, a function or a map.
func neverStrictlyEqual(x, y ir.Type) bool {
	if !isPrimitive(x) {
		x, y = y, x
	}
	switch {
	case !isPrimitive(x) || x == y || ir.IsNumeric(x) && ir.IsNumeric(y):
		return false
	case isPrimitive(y):
		return true
	}
	return isObject(y) || isFunction(y) || isMap(y)
}

// isNever reports whether t is never.
func isNever(t *checker.Type) bool { return t.Flags()&checker.TypeFlagsNever != 0 }

// trap is what code that can't run lowers to (deadRead found read in it): a TypeError that
// names the instance's type arguments and the narrowing, should a lie reach it.
func (l *lowerer) trap(read *ast.Node) ir.Expr {
	if l.narrowsReadToNoValue(read) {
		message := fmt.Sprintf("code that can't run ran: `%s` narrows to `%s` here; a type predicate, assertion or cast was wrong",
			scanner.GetTextOfNode(read), l.checker.TypeToString(l.checker.GetTypeAtLocation(read)))
		return &ir.CallBuiltin{Builtin: ir.ThrowError, Args: []ir.Expr{&ir.StringConst{Value: "TypeError"}, &ir.StringConst{Value: message}}}
	}
	var arguments []string
	test := l.deadTest()
	for i, parameter := range test.parameters {
		arguments = append(arguments, l.checker.TypeToString(parameter)+" = "+l.checker.TypeToString(test.arguments[i]))
	}
	message := fmt.Sprintf("code that can't run for %s ran: `%s` narrows to `%s` here; a type predicate, assertion or cast was wrong", strings.Join(arguments, ", "),
		scanner.GetTextOfNode(read), l.checker.TypeToString(l.checker.GetTypeAtLocation(read)))
	return &ir.CallBuiltin{Builtin: ir.ThrowError, Args: []ir.Expr{&ir.StringConst{Value: "TypeError"}, &ir.StringConst{Value: message}}}
}

// statementOrTrap lowers a statement, or a trap if it can't run (deadRead); dead says which.
func (l *lowerer) statementOrTrap(node *ast.Node) (stmts []ir.Stmt, dead bool) {
	sites := l.statementSites
	l.statementSites = nil // its own (usedSite)
	defer func() { l.statementSites = sites }()
	if read := l.deadRead(node); read != nil {
		return []ir.Stmt{&ir.ExprStmt{At: l.pos(node), X: l.trap(read)}}, true
	}
	return l.statementOf(node), false
}

// statementList lowers statements in order. Once one can't run, nor can those after it,
// which lower to nothing but their function declarations' bodies, hoisted to where the
// statements start, and their variables, never initialized, which closures made before
// them may capture.
func (l *lowerer) statementList(statements []*ast.Node) []ir.Stmt {
	var stmts []ir.Stmt
	for i, statement := range statements {
		outer := l.lowering(statement)
		lowered, dead := l.statementOrTrap(statement)
		l.leaving(statement, outer)
		stmts = append(stmts, lowered...)
		if dead {
			for _, rest := range statements[i:] {
				switch rest.Kind {
				case ast.KindFunctionDeclaration:
					l.statement(rest)
				case ast.KindVariableStatement:
					stmts = append(stmts, l.uninitialized(rest)...)
				}
			}
			break
		}
	}
	return stmts
}

// uninitialized declares the variables a variable statement that can't run declares,
// without their initializers: a closure or hoisted function may capture them.
func (l *lowerer) uninitialized(statement *ast.Node) []ir.Stmt {
	var stmts []ir.Stmt
	var declare func(name *ast.Node)
	declare = func(name *ast.Node) {
		if name.Kind != ast.KindIdentifier {
			for _, element := range name.Elements() {
				if element.Kind == ast.KindBindingElement {
					declare(element.Name())
				}
			}
			return
		}
		symbol := l.checker.GetSymbolAtLocation(name)
		local, declared := l.locals[symbol]
		if !declared {
			l.quietly(func() bool {
				if t := l.bindingType(symbol, name); t != nil {
					local = l.newLocal(name.Text(), t)
				}
				return local != nil
			})
			if local == nil {
				return // a type that doesn't lower: a capture of it fails as before
			}
			l.locals[symbol] = local
		}
		if local != nil {
			stmts = append(stmts, &ir.LocalDecl{At: l.pos(name), Local: local})
		}
	}
	for _, declaration := range statement.AsVariableStatement().DeclarationList.AsVariableDeclarationList().Declarations.Nodes {
		declare(declaration.Name())
	}
	return stmts
}

// deadOperand is a trap if node, an operand evaluated only under a condition (a branch of
// `?:`, the right operand of `&&` or `||`, an arrow function's expression body), can't run;
// otherwise nil.
func (l *lowerer) deadOperand(node *ast.Node) ir.Expr {
	if read := l.deadRead(node); read != nil {
		return l.trap(read)
	}
	return nil
}
