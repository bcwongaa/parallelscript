package lower

import (
	"fmt"
	"slices"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/pts/ir"
)

// `x instanceof C`, for a generic class C of the program, narrows x to C's prototype's
// type, C<any, …>, wherever x's declared type isn't C or derived from it: an interface C
// implements (`Cache<string, number>` for `MapLRU<K, V> implements Cache<K, V>`), a generic
// base class (`Option<number>` for `Some<T> extends Option<T>`). C<any, …> is an instance of
// its own (D-050), which x's object isn't, so the checked downcast to it threw where Node
// goes on (D-149's gap). Such a reference is instead the instance of C for the type
// arguments tsc infers from its declared type, which is what its object is (H10).

// instanceofKey is a reference, tsc's type for it and its declared type, in the instance
// being lowered: in a generic function's instances, the node and tsc's C<any, …> are the
// same, and the declared type (`Cache<K, V>`) is each instance's own.
type instanceofKey struct {
	node     *ast.Node
	t        *checker.Type
	declared *checker.Type
}

// instanceofInstance returns the type of what a reference (node) that tsc types t holds,
// if t is the C<any, …> that `instanceof C` narrows to and the reference's declared type
// implies C's type arguments: C for them. Otherwise nil.
func (l *lowerer) instanceofInstance(node *ast.Node, t *checker.Type) *checker.Type {
	inferred, _ := l.instanceofNarrowing(node, t)
	return inferred
}

// isUninferredInstanceof reports whether node is a reference that `instanceof C` narrowed
// to C<any, …> from a declared type that doesn't imply C's type arguments (a non-generic
// base class, `object`, an interface that doesn't mention all of them): its object may be
// C's instance for any of them, so it stays what it holds, as a dynamic value whose members
// are found at run time, as an `unknown`'s narrowings are (isNarrowedDynamic, D-179).
func (l *lowerer) isUninferredInstanceof(node *ast.Node) bool {
	node = ast.SkipParentheses(node)
	if !isReference(node) {
		return false
	}
	inferred, narrowed := l.instanceofNarrowing(node, l.instantiate(l.checker.GetTypeAtLocation(node)))
	return narrowed && inferred == nil
}

// instanceofMember is x, the lowered read of a member or call of a method (node) of a
// reference whose instanceof narrowing instanceofInstance resolves, as a dynamic value where
// tsc's type for it mentions any (isInstanceofDynamicMember): what uses it takes it as tsc
// says, as any (`c.key + c.val`), checked where a typed place takes it. Where it goes into
// a typed place that a dynamic value would be copied into (an `any[]` parameter, a return,
// an annotated variable: copiesDynamicValue), it stays the instance's typed value, which
// converts there as typed values do (D-094): copied where nothing can see the copy change
// (a new array, a parameter only read), and otherwise PTS1001, as a copy that writes would
// miss isn't JS (R2-F1).
func (l *lowerer) instanceofMember(node *ast.Node, x ir.Expr) ir.Expr {
	if x == nil || x.Type() == ir.Value || !l.isInstanceofDynamicMember(node) || l.copiesDynamicValue(node) {
		return x
	}
	return box(x)
}

// copiesDynamicValue reports whether node, an expression, goes as a value into a place
// whose type a dynamic value converts to by a copy (dynamic data, an object type), not as
// the object of a member access or call, an operand, or a variable that holds it as a
// dynamic value (isInstanceofDynamicLocal).
func (l *lowerer) copiesDynamicValue(node *ast.Node) bool {
	for node.Parent.Kind == ast.KindParenthesizedExpression || node.Parent.Kind == ast.KindNonNullExpression {
		node = node.Parent
	}
	var target ir.Type
	switch parent := node.Parent; parent.Kind {
	case ast.KindVariableDeclaration:
		if parent.Initializer() != node || parent.Name().Kind != ast.KindIdentifier {
			return false
		}
		local := l.checker.GetSymbolAtLocation(parent.Name())
		if local == nil || l.isInstanceofDynamicLocal(local) {
			return false
		}
		target = l.irType(l.typeOfSymbol(local), parent.Name())
	case ast.KindReturnStatement, ast.KindCallExpression, ast.KindNewExpression, ast.KindPropertyAssignment, ast.KindShorthandPropertyAssignment, ast.KindArrayLiteralExpression, ast.KindArrowFunction:
		if parent.Kind == ast.KindCallExpression && parent.Expression() == node || parent.Kind == ast.KindArrowFunction && parent.Body() != node {
			return false
		}
		t := l.contextualType(node)
		if t == nil {
			return true // a return of a function without a declared type: its type's place
		}
		target = l.irType(t, node)
	case ast.KindBinaryExpression:
		expr := parent.AsBinaryExpression()
		if expr.OperatorToken.Kind != ast.KindEqualsToken || expr.Right != node {
			return false
		}
		target = l.irType(l.typeAt(expr.Left), expr.Left)
	default:
		return false
	}
	return target != nil && target != ir.Value && !isCheckable(target)
}

// isInstanceofDynamicLocal reports whether symbol is a variable declared without a type
// whose value is a dynamic value that an instanceof narrowing gives (holdsInstanceofDynamicValue):
// its initializer's (`const it = c.items`), or a for…of binding's over one (`for (const e of
// c.entries)`), of a type a dynamic value converts to by a copy (dynamic data, an object
// type) or a generic class's C<any, …>, which no other instance converts to. It holds what
// it is given, as a dynamic value, as a variable declared unknown does, so that writes
// through it reach the object, as in JS, where a copy would miss them (R2-F1).
func (l *lowerer) isInstanceofDynamicLocal(symbol *ast.Symbol) bool {
	if l.instanceofLocals == nil {
		l.instanceofLocals = map[instanceofLocal]bool{} // a lowerer a test makes
	}
	key := instanceofLocal{symbol, l.subst} // an instance's narrowings are its own (R3-F1)
	if dynamic, ok := l.instanceofLocals[key]; ok {
		return dynamic
	}
	l.instanceofLocals[key] = false
	source := l.bindingSource(symbol)
	if source == nil {
		return false
	}
	if !l.holdsInstanceofDynamicValue(source) {
		return false // checked first: storedDynamic asks of every variable, which irType mustn't make types for
	}
	declared := l.typeOfSymbol(symbol)
	var dynamic bool
	if annotated := symbol.ValueDeclaration.Kind == ast.KindParameter && symbol.ValueDeclaration.Type() != nil; annotated {
		// `(row: any[]) => …`: a type a copy of the object would have, where the method hands
		// the parameter its objects; one of the instance's own types (`(row: number[]) => …`)
		// takes the object as it is (R3-F2), and reduce's accumulator is the callback's own
		dynamic = l.handsInstanceObjects(symbol.ValueDeclaration) && copiesInstanceObjects(l.irType(declared, symbol.ValueDeclaration.Name()))
	} else if dynamic = l.mentionsPrototypeInstance(declared); !dynamic {
		t := l.irType(declared, symbol.ValueDeclaration.Name())
		dynamic = t != nil && t != ir.Value && !isCheckable(t) && !isFunction(t)
	}
	l.instanceofLocals[key] = dynamic
	return dynamic
}

// instanceofLocal is a variable in the instance being lowered.
type instanceofLocal struct {
	symbol *ast.Symbol
	subst  *substitution
}

// bindingSource is what a variable declared without a type annotation is given: its
// declaration's initializer, or the iterable a for…of declaration takes items of; for a
// name a pattern binds (`const { list } = …`, `for (const { list } of …)`), what the
// pattern takes apart. Otherwise nil.
func (l *lowerer) bindingSource(symbol *ast.Symbol) *ast.Node {
	declaration := symbol.ValueDeclaration
	if declaration == nil || declaration.Name() == nil || declaration.Name().Kind != ast.KindIdentifier {
		return nil
	}
	if declaration.Kind == ast.KindParameter {
		return l.callbackSource(declaration)
	}
	for declaration.Kind == ast.KindBindingElement {
		declaration = declaration.Parent.Parent // the pattern's declaration or binding element; a default is a new value (R4-F2)
	}
	if declaration.Kind == ast.KindParameter {
		return l.callbackSource(declaration) // `forEach(({ list }) => …)` (R5-F2)
	}
	if declaration.Kind != ast.KindVariableDeclaration || declaration.Type() != nil {
		return nil
	}
	switch list := declaration.Parent; {
	case declaration.Initializer() != nil:
		return declaration.Initializer()
	case list.Kind == ast.KindVariableDeclarationList && list.Parent.Kind == ast.KindForOfStatement:
		return list.Parent.AsForInOrOfStatement().Expression
	}
	return nil
}

// holdsInstanceofDynamicValue reports whether node gives a dynamic value that an instanceof
// narrowing makes: a member tsc types with any of a reference whose narrowing is inferred
// (isInstanceofDynamicMember), a reference whose narrowing isn't (isUninferredInstanceof) or
// a member of one or of an `unknown` narrowed so (isMemberOfNarrowedInstance), or what a
// member access, element, call, conditional, `??`, `||`, `as` or `await` of one gives.
func (l *lowerer) holdsInstanceofDynamicValue(node *ast.Node) bool {
	return l.instanceofDynamicSource(node, false)
}

// madeDynamicByInstanceof is holdsInstanceofDynamicValue for the values H10a made dynamic:
// not the members of a variable declared `unknown` or `any` or of a `catch` binding, which
// were dynamic values before and convert to typed places by copies, as D-179 says (R4-F1).
func (l *lowerer) madeDynamicByInstanceof(node *ast.Node) bool {
	return l.instanceofDynamicSource(node, true)
}

func (l *lowerer) instanceofDynamicSource(node *ast.Node, made bool) bool {
	node = ast.SkipParentheses(node)
	switch node.Kind {
	case ast.KindIdentifier:
		return l.dynamicLocalSource(l.readSymbol(node), made) || l.isUninferredInstanceof(node) // readSymbol: also a shorthand property's variable
	case ast.KindPropertyAccessExpression, ast.KindElementAccessExpression:
		switch {
		case l.isInstanceofDynamicMember(node) || l.isUninferredInstanceof(node):
			return true
		case l.isMemberOfNarrowedInstance(node):
			return !made || l.isUninferredInstanceof(ast.SkipParentheses(node.Expression())) || l.instanceofDynamicSource(node.Expression(), true)
		}
		return l.instanceofDynamicSource(node.Expression(), made)
	case ast.KindCallExpression:
		callee := ast.SkipParentheses(node.Expression())
		if l.isInstanceofDynamicMember(node) || ast.IsAccessExpression(callee) && l.instanceofDynamicSource(callee.Expression(), made) {
			return true
		}
		// `Promise.resolve(c.items)`, whose awaited value is the array (R4-F2)
		return callee.Kind == ast.KindPropertyAccessExpression && l.isPromiseConstructor(callee.Expression()) && callee.AsPropertyAccessExpression().Name().Text() == "resolve" &&
			len(node.Arguments()) == 1 && l.instanceofDynamicSource(node.Arguments()[0], made)
	case ast.KindNonNullExpression, ast.KindAsExpression, ast.KindSatisfiesExpression, ast.KindTypeAssertionExpression, ast.KindAwaitExpression:
		return l.instanceofDynamicSource(node.Expression(), made)
	case ast.KindPropertyAssignment:
		return l.instanceofDynamicSource(node.Initializer(), made) // an object literal's `it: it`
	case ast.KindShorthandPropertyAssignment:
		return l.dynamicLocalSource(l.checker.GetShorthandAssignmentValueSymbol(node), made) // `{ it }`
	case ast.KindConditionalExpression:
		conditional := node.AsConditionalExpression()
		return l.instanceofDynamicSource(conditional.WhenTrue, made) || l.instanceofDynamicSource(conditional.WhenFalse, made)
	case ast.KindBinaryExpression:
		expr := node.AsBinaryExpression()
		switch op := expr.OperatorToken.Kind; {
		case op == ast.KindQuestionQuestionToken || op == ast.KindBarBarToken || op == ast.KindAmpersandAmpersandToken:
			return l.instanceofDynamicSource(expr.Left, made) || l.instanceofDynamicSource(expr.Right, made)
		case op == ast.KindCommaToken || ast.IsAssignmentOperator(op):
			return l.instanceofDynamicSource(expr.Right, made)
		}
	}
	return false
}

// dynamicLocalSource reports whether symbol is a variable isInstanceofDynamicLocal holds,
// whose value an instanceof narrowing made dynamic if made says so.
func (l *lowerer) dynamicLocalSource(symbol *ast.Symbol, made bool) bool {
	if symbol == nil || !l.isInstanceofDynamicLocal(symbol) {
		return false
	}
	if !made {
		return true
	}
	source := l.bindingSource(symbol)
	return source != nil && l.instanceofDynamicSource(source, true)
}

// copiesInstanceofDynamicValue reports whether converting value, the lowered node, to target
// copies a dynamic value that an instanceof narrowing gives (holdsInstanceofDynamicValue):
// the object is an instance's, whose members are typed, so a target that holds dynamic
// values (`any[]`, an object type with an `any[]` property) takes it only as a copy, which
// writes to the object itself (`const a = c.items ?? []`, a default parameter, a field,
// `yield`) would miss silently; a parameter that only reads it may take a copy, as D-094
// says. A target of the instance's own types (`number[]`, `{ items: number[] }`) keeps the
// object as it is (R3-F2).
func (l *lowerer) copiesInstanceofDynamicValue(value ir.Expr, target ir.Type, node *ast.Node) bool {
	if node != nil && node.Kind == ast.KindParenthesizedExpression {
		node = ast.SkipParentheses(node)
	}
	return value.Type() == ir.Value && target != ir.Value && !isCheckable(target) && !isFunction(target) &&
		holdsDynamicValues(target, map[ir.Type]bool{}) && node != nil && l.madeDynamicByInstanceof(node) && !l.isReadOnlyArgument(node) &&
		!(isReadOnlyParallelView(node) && l.holdsPrimitiveElements(node))
}

// holdsPrimitiveElements reports whether node, an array an instanceof narrowing holds as a
// dynamic value, holds primitives in the instance (`c.items` of a MapLRU<string, number>):
// a copy of it is the array's values, where a copy of arrays or objects is of them too, so
// writes to its elements would miss the instance's (R5-F3).
func (l *lowerer) holdsPrimitiveElements(node *ast.Node) bool {
	node = ast.SkipParentheses(node)
	if node.Kind == ast.KindIdentifier {
		if symbol := l.readSymbol(node); symbol != nil && l.isInstanceofDynamicLocal(symbol) {
			if source := l.bindingSource(symbol); source != nil {
				return l.holdsPrimitiveElements(source)
			}
		}
		return false
	}
	if node.Kind != ast.KindPropertyAccessExpression {
		return false
	}
	receiver := ast.SkipParentheses(node.Expression())
	instance := l.instanceofInstance(receiver, l.instantiate(l.checker.GetTypeAtLocation(receiver)))
	if instance == nil {
		return false
	}
	member := l.checker.GetTypeOfPropertyOfType(instance, node.AsPropertyAccessExpression().Name().Text())
	if member == nil || !l.checker.IsArrayType(member) {
		return false
	}
	elem, ok := l.irType(member, node).(*ir.Array)
	return ok && isPrimitive(elem.Elem) || ok && ir.IsNumeric(elem.Elem)
}

// isReadOnlyParallelView reports whether node is the array of a `.par` view whose method
// only reads it (`x.items.par.map(f)`), which a copy serves as the array itself.
func isReadOnlyParallelView(node *ast.Node) bool {
	view := node.Parent
	if view.Kind != ast.KindPropertyAccessExpression || view.Expression() != node || view.AsPropertyAccessExpression().Name().Text() != "par" {
		return false
	}
	method := view.Parent
	return method.Kind == ast.KindPropertyAccessExpression && method.Expression() == view && readOnlyArrayMethods[method.AsPropertyAccessExpression().Name().Text()]
}

// isInstanceofNestedObject reports whether node, not a variable, reads an object held inside
// a dynamic value an instanceof narrowing gives (holdsInstanceofDynamicValue): an element,
// property or method result of one (`c.matrix[0]`, `c.meta.tags`, `c.store.get(k)!`), of a
// type that converting it to would copy it (copiesInstanceObjects). It may be an object of
// the instance's own types, so it stays the dynamic value it is, whose members are found at
// run time, as the member itself does (isInstanceofDynamicMember): writes through it reach
// the object, and `===` compares it, where a copy would miss them.
func (l *lowerer) isInstanceofNestedObject(node *ast.Node) bool {
	node = ast.SkipParentheses(node)
	if node.Kind == ast.KindIdentifier || len(l.genericClasses) == 0 || !l.holdsInstanceofDynamicValue(node) {
		return false
	}
	t := l.checker.GetNonNullableType(l.typeAt(node))
	if t.Flags()&checker.TypeFlagsObject == 0 || l.isCheckedValueType(t) || len(l.checker.GetSignaturesOfType(t, checker.SignatureKindCall)) > 0 {
		return false
	}
	return copiesInstanceObjects(l.irType(t, node))
}

// isInstanceofDynamicLocalRef reports whether node reads a variable isInstanceofDynamicLocal holds.
func (l *lowerer) isInstanceofDynamicLocalRef(node *ast.Node) bool {
	if node = ast.SkipParentheses(node); node.Kind != ast.KindIdentifier {
		return false
	}
	symbol := l.readSymbol(node) // also `{ it }`'s variable
	return symbol != nil && l.isInstanceofDynamicLocal(symbol)
}

// isInstanceofDynamicMember reports whether node reads a member of, or calls a method of, a
// reference whose instanceof narrowing instanceofInstance resolves, where tsc's type for it
// mentions any: C<any, …>'s member for C's type parameters (`c.val`, `c.items` an `any[]`,
// `c.pair` an `[any, any]`, `c.self()` a C<any, …>), where the instance's is typed. It is
// the instance's own value, held as a dynamic value, whose members are read, written and
// called as a dynamic value's are (isNarrowedDynamic): `c.items.push(x)` reaches the
// instance's array, `c.pair[1]` its tuple's element, and bound to a typed local or passed to
// a typed parameter it converts as a dynamic value does (D-179), which compiles wherever
// tsc's type goes.
func (l *lowerer) isInstanceofDynamicMember(node *ast.Node) bool {
	node = ast.SkipParentheses(node)
	return l.readsInstanceofMember(node) && l.mentionsAny(l.typeAt(node), 0)
}

// mentionsAny reports whether t is any or holds it: as a member of a union or intersection,
// as a type argument (an array's element, a tuple's, a class's or a map's), or in a function
// type's parameters or result or an object literal type's properties.
func (l *lowerer) mentionsAny(t *checker.Type, depth int) bool {
	if t == nil || depth > 8 {
		return false
	}
	if l.anyMentions == nil {
		l.anyMentions = map[*checker.Type]bool{} // a lowerer a test makes
	}
	if known, ok := l.anyMentions[t]; ok {
		return known
	}
	l.anyMentions[t] = false // a recursive type mentions any elsewhere if at all
	mentions := func(t *checker.Type) bool { return l.mentionsAny(t, depth+1) }
	found := false
	switch {
	case t.Flags()&checker.TypeFlagsAny != 0:
		found = true
	case t.Flags()&checker.TypeFlagsUnionOrIntersection != 0:
		found = slices.ContainsFunc(t.Types(), mentions)
	case t.Flags()&checker.TypeFlagsObject == 0:
	case t.ObjectFlags()&checker.ObjectFlagsReference != 0:
		found = slices.ContainsFunc(l.checker.GetTypeArguments(t), mentions)
	default: // a function type, an object literal type, a mapped type (`Record<string, V>`)
		for _, signature := range l.checker.GetSignaturesOfType(t, checker.SignatureKindCall) {
			found = found || mentions(l.checker.GetReturnTypeOfSignature(signature)) || slices.ContainsFunc(signature.Parameters(), func(parameter *ast.Symbol) bool { return mentions(l.checker.GetTypeOfSymbol(parameter)) })
		}
		for _, info := range l.checker.GetIndexInfosOfType(t) {
			found = found || mentions(info.ValueType())
		}
		for _, property := range l.checker.GetPropertiesOfType(t) {
			found = found || mentions(l.checker.GetTypeOfSymbol(property))
		}
	}
	if depth == 0 || found {
		l.anyMentions[t] = found
	} else {
		delete(l.anyMentions, t) // not known yet: an enclosing type's walk may have cut it short
	}
	return found
}

// readsInstanceofMember reports whether node reads a member of, or calls a method of, a
// reference whose instanceof narrowing instanceofInstance resolves: tsc types the read as
// C<any, …>'s member, any where C's type parameters are, and the instance's member is typed.
func (l *lowerer) readsInstanceofMember(node *ast.Node) bool {
	node = ast.SkipParentheses(node)
	if node.Kind == ast.KindCallExpression {
		node = ast.SkipParentheses(node.Expression())
	}
	if !ast.IsAccessExpression(node) {
		return false
	}
	receiver := ast.SkipParentheses(node.Expression())
	return l.instanceofInstance(receiver, l.instantiate(l.checker.GetTypeAtLocation(receiver))) != nil
}

// instanceofNarrowing reports whether t, tsc's type for a reference (node), is the C<any,
// …> that `instanceof C` narrowed it to from its declared type (narrowed), and the
// instance of C for the type arguments the declared type implies, if it implies them.
func (l *lowerer) instanceofNarrowing(node *ast.Node, t *checker.Type) (inferred *checker.Type, narrowed bool) {
	if !isReference(node) {
		return nil, false
	}
	prototype := t
	if t.Flags()&checker.TypeFlagsIntersection != 0 {
		prototype = nil // `S & C<any, …>` for an `x: S` of a type parameter's type (getNarrowedType)
		for _, member := range t.Types() {
			if l.isPrototypeInstance(member) {
				prototype = member
				break
			}
		}
	}
	if prototype == nil || !l.isPrototypeInstance(prototype) {
		return nil, false
	}
	declared := l.declaredTypeOfReference(node)
	key := instanceofKey{node, t, declared}
	if l.instanceofInstances == nil {
		l.instanceofInstances = map[instanceofKey]instanceofResult{} // a lowerer a test makes
	}
	if known, ok := l.instanceofInstances[key]; ok {
		return known.inferred, known.narrowed
	}
	l.instanceofInstances[key] = instanceofResult{}
	var result instanceofResult
	switch {
	case declared == nil:
	case declared.Flags()&(checker.TypeFlagsAny|checker.TypeFlagsUnknown) != 0:
		// A property or element declared any or unknown (`holder.c`), whose value is a
		// dynamic value as a variable's is (storedDynamic): any C, found at run time.
		result.narrowed = node.Kind != ast.KindIdentifier
	case l.narrowsByInstanceof(declared, t):
		result = instanceofResult{inferred: l.checker.PtsInferInstanceTypeArguments(declared, prototype), narrowed: true}
	}
	l.instanceofInstances[key] = result
	return result.inferred, result.narrowed
}

// isPrototypeInstance reports whether t is the type of a generic class of the program's
// prototype, the instance for any (`C<any, any>`), which instanceof narrows to.
func (l *lowerer) isPrototypeInstance(t *checker.Type) bool {
	if t.Flags()&checker.TypeFlagsObject == 0 || t.ObjectFlags()&checker.ObjectFlagsReference == 0 || t.Symbol() == nil {
		return false
	}
	generic, ok := l.genericClasses[l.checker.GetMergedSymbol(t.Symbol())]
	if !ok || generic == nil {
		return false
	}
	arguments := l.checker.GetTypeArguments(t)
	if len(arguments) < len(generic.parameters) {
		return false
	}
	for _, argument := range arguments[:len(generic.parameters)] {
		if argument.Flags()&checker.TypeFlagsAny == 0 {
			return false
		}
	}
	return true
}

// instanceofClass is the class of the instance that a reference (node) narrowed by
// instanceof holds, where its declared type implies it (instanceofInstance); otherwise nil.
func (l *lowerer) instanceofClass(node *ast.Node) *ir.Class {
	instance := l.instanceofInstance(node, l.instantiate(l.checker.GetTypeAtLocation(node)))
	if instance == nil {
		return nil
	}
	class, _ := l.irType(instance, node).(*ir.Class)
	return class
}

// isMemberOfNarrowedInstance reports whether node reads a member of a dynamic value that
// tsc narrowed to a generic class of the program (`x.items` after `x instanceof C` for an
// unknown x, or one isUninferredInstanceof keeps): an object of an instance whose members
// have their own types, so one tsc types as dynamic data (an `any[]`) may be a typed array,
// which converting to any[] would copy (D-071) and writes through it would miss.
func (l *lowerer) isMemberOfNarrowedInstance(node *ast.Node) bool {
	if !ast.IsAccessExpression(node) {
		return false
	}
	if l.isInstanceofDynamicMember(node) {
		return true // a member of an instance whose type tsc gives as C<any, …>'s
	}
	object := ast.SkipParentheses(node.Expression())
	if !l.storedDynamic(object) && !l.isUninferredInstanceof(object) {
		return false
	}
	t := l.typeAt(object)
	if t.Flags()&checker.TypeFlagsIntersection != 0 {
		return slices.ContainsFunc(t.Types(), l.isPrototypeInstance)
	}
	return l.isPrototypeInstance(t)
}

// familyPrivateField lowers `o.#f` (node) for an o that tsc narrowed to a generic class C
// of the program's C<any, …> and that is a dynamic value (a variable declared unknown, or one
// isUninferredInstanceof keeps): the private field of whichever of C's instances o is
// (FamilyField), converted with a check to tsc's type, since a dynamic value's properties
// don't hold private names. ok is false for other accesses.
func (l *lowerer) familyPrivateField(node *ast.Node) (x ir.Expr, ok bool) {
	name := node.AsPropertyAccessExpression().Name()
	receiver := ast.SkipParentheses(node.Expression())
	if name.Kind != ast.KindPrivateIdentifier || !l.storedDynamic(receiver) && !l.isUninferredInstanceof(receiver) {
		return nil, false
	}
	if symbol := l.checker.GetSymbolAtLocation(name); symbol == nil || symbol.ValueDeclaration == nil || symbol.ValueDeclaration.Kind != ast.KindPropertyDeclaration {
		return nil, false // a private accessor or method, which method tables hold by name
	}
	generic := l.prototypeGeneric(l.typeAt(receiver))
	if generic == nil {
		return nil, false
	}
	object := l.expression(receiver)
	if object == nil {
		return nil, true
	}
	x = &ir.FamilyField{X: box(object), Family: &generic.classes, Name: name.Text()}
	if l.mentionsPrototypeInstance(l.typeAt(node)) {
		return x, true // tsc's C<any, …> (`o.#next`), which another instance converts to only as a dynamic value
	}
	if t := l.irType(l.typeAt(node), node); t != nil && t != ir.Value {
		return l.checkedConversion(x, t, node), true
	}
	return x, true
}

// prototypeGeneric is the generic class whose prototype's type, C<any, …>, t is or is an
// intersection with (isPrototypeInstance); otherwise nil.
func (l *lowerer) prototypeGeneric(t *checker.Type) *genericClass {
	if t.Flags()&checker.TypeFlagsIntersection != 0 {
		for _, member := range t.Types() {
			if l.isPrototypeInstance(member) {
				t = member
				break
			}
		}
	}
	if !l.isPrototypeInstance(t) {
		return nil
	}
	return l.genericClasses[l.checker.GetMergedSymbol(t.Symbol())]
}

// instanceofResult is what instanceofNarrowing found for a reference.
type instanceofResult struct {
	inferred *checker.Type
	narrowed bool
}

// isReference reports whether node is a reference tsc narrows: a variable or parameter, a
// property, or an element at a literal key (`arr[0]`).
func isReference(node *ast.Node) bool {
	switch node.Kind {
	case ast.KindIdentifier, ast.KindPropertyAccessExpression:
		return true
	case ast.KindElementAccessExpression:
		return ast.IsStringOrNumericLiteralLike(node.AsElementAccessExpression().ArgumentExpression)
	}
	return false
}

// narrowsByInstanceof reports whether tsc's type for a reference, t (C<any, …>), differs
// from its declared type as `instanceof C` makes it differ: declared is neither t nor a
// union holding t.
func (l *lowerer) narrowsByInstanceof(declared, t *checker.Type) bool {
	if declared == t {
		return false
	}
	if declared.Flags()&checker.TypeFlagsUnion != 0 {
		for _, member := range declared.Types() {
			if member == t {
				return false
			}
		}
	}
	return true
}

// declaredTypeOfReference is the type a variable, parameter, property or element that node
// reads is declared with, before narrowings, in the instance being lowered; nil for other
// nodes.
func (l *lowerer) declaredTypeOfReference(node *ast.Node) *checker.Type {
	switch node.Kind {
	case ast.KindIdentifier:
		symbol := l.followAliases(l.checker.GetSymbolAtLocation(node)) // an imported binding's
		if symbol == nil || symbol.Flags&(ast.SymbolFlagsVariable|ast.SymbolFlagsProperty) == 0 {
			return nil
		}
		return l.typeOfSymbol(symbol)
	case ast.KindPropertyAccessExpression:
		object, name := l.typeAt(node.Expression()), node.AsPropertyAccessExpression().Name()
		if name.Kind == ast.KindPrivateIdentifier {
			if symbol := l.checker.GetSymbolAtLocation(name); symbol != nil {
				return l.typeOfSymbol(symbol) // `this.#cache`
			}
			return nil
		}
		if t := l.checker.GetTypeOfPropertyOfType(object, name.Text()); t != nil {
			return t
		}
		if infos := l.checker.GetIndexInfosOfType(object); len(infos) > 0 {
			return infos[0].ValueType() // `rec.main` of a Record<string, …>
		}
	case ast.KindElementAccessExpression:
		object := l.typeAt(node.Expression())
		if t := l.checker.GetTypeOfPropertyOfType(object, node.AsElementAccessExpression().ArgumentExpression.Text()); t != nil {
			return t // a tuple's element, an object type's property
		}
		if infos := l.checker.GetIndexInfosOfType(object); len(infos) > 0 {
			return infos[0].ValueType() // an array's or record's element
		}
	}
	return nil
}

// isPrivateMemberChange reports whether node is the object of a private member that is
// written or called there (`o.#v = x`, `o.#v++`, `o.#tick()`), which an instance held as a
// dynamic value has no way to reach yet (H10b): such an object keeps the downcast to tsc's
// C<any, …> it had before H10 (D-149), which an instance for any passes.
func (l *lowerer) isPrivateMemberChange(node *ast.Node) bool {
	for node.Parent.Kind == ast.KindParenthesizedExpression {
		node = node.Parent
	}
	access := node.Parent
	if access.Kind != ast.KindPropertyAccessExpression || access.Expression() != node || access.AsPropertyAccessExpression().Name().Kind != ast.KindPrivateIdentifier {
		return false
	}
	for access.Parent.Kind == ast.KindParenthesizedExpression {
		access = access.Parent
	}
	switch parent := access.Parent; parent.Kind {
	case ast.KindCallExpression:
		return ast.SkipParentheses(parent.Expression()) == ast.SkipParentheses(access)
	case ast.KindBinaryExpression:
		return ast.IsAssignmentOperator(parent.AsBinaryExpression().OperatorToken.Kind) && parent.AsBinaryExpression().Left == access
	case ast.KindPrefixUnaryExpression, ast.KindPostfixUnaryExpression:
		return true
	}
	return false
}

// callsInstanceofMethodDynamically reports whether call calls a member of a reference whose
// instanceof narrowing instanceofInstance resolves that is found at run time: a field
// holding a function whose type mentions any, or a method with an argument that tsc types
// for C<any, …>'s parameter where the instance's differs, a function, object or array
// literal (or a spread) for a parameter whose type mentions any (`(old: any) => any` for the
// instance's `(old: V | undefined) => V`), which converts to the instance's only at run time.
func (l *lowerer) callsInstanceofMethodDynamically(call *ast.CallExpression) bool {
	callee := ast.SkipParentheses(call.Expression)
	if !ast.IsAccessExpression(callee) || !l.readsInstanceofMember(callee) {
		return false
	}
	if callee.Kind == ast.KindPropertyAccessExpression && callee.AsPropertyAccessExpression().Name().Kind == ast.KindPrivateIdentifier {
		return false // a private method, which only the instance's own code finds (R2-F3)
	}
	if declaration := l.memberDeclaration(callee); declaration == nil || declaration.Kind != ast.KindMethodDeclaration {
		return l.isInstanceofDynamicMember(callee) // a field holding a function (`c.cb(2)`), which reads as a dynamic value
	}
	signature := l.checker.GetResolvedSignature(call.AsNode())
	if signature == nil {
		return false
	}
	parameters := signature.Parameters()
	for i, argument := range call.Arguments.Nodes {
		switch ast.SkipParentheses(argument).Kind {
		case ast.KindArrowFunction, ast.KindFunctionExpression, ast.KindObjectLiteralExpression, ast.KindArrayLiteralExpression, ast.KindSpreadElement:
		default:
			continue
		}
		if len(parameters) == 0 {
			return true
		}
		if l.mentionsAny(l.typeOfSymbol(parameters[min(i, len(parameters)-1)]), 0) {
			return true
		}
	}
	return false
}

// sameGenericClass reports whether to is an instance of the generic class whose instance
// from is, or a subclass of one: where tsc's C<any, …> goes, a conversion that may succeed.
func (l *lowerer) sameGenericClass(from, to *ir.Class) bool {
	inst := l.classInstances[from]
	if inst == nil {
		return false
	}
	for class := to; class != nil; class = class.Parent {
		if other := l.classInstances[class]; other != nil && other.generic == inst.generic {
			return true
		}
	}
	return false
}

// mentionsPrototypeInstance reports whether t is or holds (as a union's member) a generic
// class's prototype's type, C<any, …> (`MapLRU<any, any> | undefined`).
func (l *lowerer) mentionsPrototypeInstance(t *checker.Type) bool {
	if t.Flags()&checker.TypeFlagsUnion != 0 {
		return slices.ContainsFunc(t.Types(), func(member *checker.Type) bool { return l.prototypeGeneric(member) != nil })
	}
	return l.prototypeGeneric(t) != nil // also `Shape & Box<any>`, a type parameter's narrowing in an instance
}

// reportInstanceofCopy reports, at node, the copy copiesInstanceofDynamicValue finds.
func (l *lowerer) reportInstanceofCopy(target ir.Type, node *ast.Node) {
	l.notSupportedYet(node, fmt.Sprintf("using the object an `instanceof` narrowing holds as a dynamic value as a %s where it may be changed or kept (a copy wouldn't show the changes; one passed to a parameter that is only read is copied) is", target))
}

// callbackSource is, for a parameter of a callback, a function written as an argument of
// a method call (callbackArgument), what the method hands the callback: the call's receiver,
// whose items and self an array's method hands it (`c.items` of `c.items.forEach((x, i, arr)
// => …)`), or the method itself where it is the narrowed instance's own, called through `any`
// (callsInstanceofMethodDynamically: `c.each((items) => …)`), whose code hands it the
// instance's values; otherwise nil.
func (l *lowerer) callbackSource(parameter *ast.Node) *ast.Node {
	call, _ := callbackArgument(parameter.Parent)
	if call == nil {
		return nil
	}
	callee := ast.SkipParentheses(call.Expression())
	if !ast.IsAccessExpression(callee) {
		return nil
	}
	if l.callsInstanceofMethodDynamically(call.AsCallExpression()) {
		return callee
	}
	return callee.Expression()
}

// handsInstanceObjects reports whether the method that the callback of parameter is given to
// (callbackArgument) may hand the parameter objects of its receiver or of its instance: an
// array's method its first argument's element and array parameters
// (arrayCallbackObjectParameters), any other method any parameter.
func (l *lowerer) handsInstanceObjects(parameter *ast.Node) bool {
	fn := parameter.Parent
	call, argument := callbackArgument(fn)
	if call == nil {
		return false
	}
	method, ok := l.arrayMethodName(ast.SkipParentheses(call.Expression()))
	if !ok {
		return true
	}
	return call.Arguments()[0] == argument && slices.Contains(arrayCallbackObjectParameters(method, len(call.Arguments())), slices.Index(fn.Parameters(), parameter))
}

// returnsToDynamicInstanceCall reports whether fn, a function written without a result type,
// is a callback (callbackArgument) of a method that a call through `any` of a dynamic value an
// instanceof narrowing made calls (`c.items.reduce`, `o.each`, the narrowed instance's own
// method): the method's code takes what it returns as a dynamic value, which may be an object
// of the instance's that it was handed (`return acc`), so a copy would miss its writes.
func (l *lowerer) returnsToDynamicInstanceCall(fn *ast.Node) bool {
	if fn.Type() != nil {
		return false
	}
	call, _ := callbackArgument(fn)
	if call == nil {
		return false
	}
	callee := ast.SkipParentheses(call.Expression())
	return ast.IsAccessExpression(callee) && (l.callsInstanceofMethodDynamically(call.AsCallExpression()) || l.madeDynamicByInstanceof(callee.Expression()))
}

// callbackArgument is, for fn, an arrow function or function expression written as an
// argument of a call, the call and the argument (fn, or the parentheses around it); otherwise
// nils. A function in an object or array literal argument is a value of the literal's type,
// which copiesCallbackArgument checks.
func callbackArgument(fn *ast.Node) (call, argument *ast.Node) {
	if fn.Kind != ast.KindArrowFunction && fn.Kind != ast.KindFunctionExpression {
		return nil, nil
	}
	argument = fn
	for argument.Parent.Kind == ast.KindParenthesizedExpression {
		argument = argument.Parent
	}
	if call = argument.Parent; call.Kind == ast.KindCallExpression && slices.Contains(call.Arguments(), argument) {
		return call, argument
	}
	return nil, nil
}

// copiesCallbackArgument reports whether argument (at index) of call, a call through `any` of
// callee, a method of a dynamic value an instanceof narrowing made (madeDynamicByInstanceof:
// `c.items.forEach(f)`, `o.each(f)`, the narrowed instance's own method called with a
// literal), is a function or holds one (an object's property, an array's element) with a
// parameter that would get a copy of an object the method may hand it: a parameter of a type a
// dynamic value converts to by a copy (`arr: any[]`, copiesInstanceObjects), which writes
// through would miss silently (R5-F2). It reports it, at argument. An array's method hands its
// callback, its first argument, its elements and itself (arrayCallbackObjectParameters); any
// other method may hand a callback anything. A function written there or named there whose
// code is known (callbackFunction) takes a copy at a parameter it only reads, as D-094 says;
// a parameter written there that would copy is a dynamic value (isInstanceofDynamicLocal).
func (l *lowerer) copiesCallbackArgument(call *ast.CallExpression, callee *ast.Node, index int, argument *ast.Node, value ir.Expr) bool {
	if !holdsFunctions(value.Type(), map[ir.Type]bool{}) || !l.madeDynamicByInstanceof(callee) {
		return false
	}
	var parameters []int // those the method hands its objects; nil for every one
	if method, ok := l.arrayMethodName(callee); ok {
		parameters = arrayCallbackObjectParameters(method, len(call.Arguments.Nodes))
		if index != 0 || parameters == nil {
			return false // an array's method calls no other argument
		}
	}
	fn, ok := value.Type().(*ir.FuncType)
	if !ok {
		if parameters != nil {
			return false
		}
		if t := copyingParameterIn(value.Type(), map[ir.Type]bool{}); t != nil {
			l.reportInstanceofCopy(t, argument)
			return true
		}
		return false
	}
	declaration := l.callbackFunction(argument)
	for i := range parameterCount(fn) {
		if declaration != nil && !declaresArgument(declaration, i) {
			break // a parameter the function's type adds for the callback type's, which its code doesn't have
		}
		t := parameterTypeAt(fn, i)
		if parameters != nil && !slices.Contains(parameters, i) || !copiesInstanceObjects(t) || declaration != nil && l.onlyReadsParameter(declaration, i) {
			continue
		}
		l.reportInstanceofCopy(t, argument)
		return true
	}
	return false
}

// arrayMethodName is the name of the method callee calls, if its receiver is an array or a
// tuple (`c.items.forEach`).
func (l *lowerer) arrayMethodName(callee *ast.Node) (string, bool) {
	if callee.Kind != ast.KindPropertyAccessExpression {
		return "", false
	}
	receiver := l.checker.GetNonNullableType(l.typeAt(callee.Expression()))
	if !l.checker.IsArrayType(receiver) && !checker.IsTupleType(receiver) {
		return "", false
	}
	return callee.AsPropertyAccessExpression().Name().Text(), true
}

// arrayCallbackObjectParameters is the indexes of the parameters of the callback of an
// array's method that it hands the array's objects: an element and the array itself, and
// reduce's accumulator where the call (of arguments arguments) gives no initial value, which
// is then the first element. nil for a method that calls no callback.
func arrayCallbackObjectParameters(method string, arguments int) []int {
	switch method {
	case "reduce", "reduceRight":
		if arguments < 2 {
			return []int{0, 1, 3}
		}
		return []int{1, 3}
	case "sort", "toSorted":
		return []int{0, 1}
	case "every", "filter", "find", "findIndex", "findLast", "findLastIndex", "flatMap", "forEach", "map", "some":
		return []int{0, 2}
	}
	return nil
}

// copiesInstanceObjects reports whether t is a type that a dynamic value holding an
// instance's typed object converts to by a copy: one that holds dynamic values (`any[]`, `{
// items: any[] }`), not checked as it is (a primitive, a class) or adapted (a function).
func copiesInstanceObjects(t ir.Type) bool {
	return t != nil && t != ir.Value && !isCheckable(t) && !isFunction(t) && holdsDynamicValues(t, map[ir.Type]bool{})
}

// holdsFunctions reports whether values of t are or hold functions: t is a function type, or
// an array, object type, shape or union holding one.
func holdsFunctions(t ir.Type, seen map[ir.Type]bool) bool {
	return functionTypeIn(t, seen, func(*ir.FuncType, int) bool { return true })
}

// copyingParameterIn is the type of a parameter of a function held in t (holdsFunctions)
// that copiesInstanceObjects says a dynamic value converts to by a copy; nil if none.
func copyingParameterIn(t ir.Type, seen map[ir.Type]bool) ir.Type {
	var found ir.Type
	functionTypeIn(t, seen, func(fn *ir.FuncType, skip int) bool {
		for i := skip; i < parameterCount(fn); i++ {
			if t := parameterTypeAt(fn, i); copiesInstanceObjects(t) {
				found = t
				return true
			}
		}
		return false
	})
	return found
}

// functionTypeIn reports whether t is, or holds as an array's element, an object type's or
// shape's field or a union's member, a function type that test accepts; test is given the
// number of leading parameters the function's callers don't pass (a shape's method takes
// its object first, as `this`).
func functionTypeIn(t ir.Type, seen map[ir.Type]bool, test func(fn *ir.FuncType, skip int) bool) bool {
	if seen[t] {
		return false
	}
	seen[t] = true
	inFields := func(fields []*ir.Field) bool {
		return slices.ContainsFunc(fields, func(field *ir.Field) bool {
			if fn, ok := field.Type.(*ir.FuncType); ok && field.Method {
				return test(fn, 1)
			}
			return functionTypeIn(field.Type, seen, test)
		})
	}
	switch t := t.(type) {
	case *ir.FuncType:
		return test(t, 0)
	case *ir.Array:
		return functionTypeIn(t.Elem, seen, test)
	case *ir.Interface:
		return inFields(t.Fields)
	case *ir.Class:
		return t.Shape && inFields(t.Fields)
	case *ir.Union:
		return slices.ContainsFunc(t.Members, func(member ir.Type) bool { return functionTypeIn(member, seen, test) })
	}
	return false
}

// parameterCount is the number of parameters of a function type fn, a rest parameter one,
// leaving out the flags that follow parameters with defaults.
func parameterCount(fn *ir.FuncType) int {
	n := 0
	for _, t := range fn.Params {
		if t != ir.Given {
			n++
		}
	}
	return n
}

// parameterTypeAt is the type of the argument at index i that a function of type fn takes:
// its parameter's, or the element type of a rest parameter at or before i; nil past them.
func parameterTypeAt(fn *ir.FuncType, i int) ir.Type {
	for _, t := range fn.Params {
		if t == ir.Given {
			continue
		}
		if rest, ok := t.(*ir.Rest); ok {
			return rest.Array.Elem
		}
		if i == 0 {
			return t
		}
		i--
	}
	return nil
}

// declaresArgument reports whether fn, a function declaration or expression, has a parameter
// that takes its argument at index i: one of its own, or a rest parameter before it.
func declaresArgument(fn *ast.Node, i int) bool {
	parameters := fn.Parameters()
	return i < len(parameters) || len(parameters) > 0 && parameters[len(parameters)-1].AsParameterDeclaration().DotDotDotToken != nil
}

// onlyReadsParameter reports whether fn, a function declaration or expression, only reads
// its parameter at index i (onlyReads), so a copy there can't be told apart from the object
// (D-094). A rest parameter's elements aren't checked.
func (l *lowerer) onlyReadsParameter(fn *ast.Node, i int) bool {
	if i >= len(fn.Parameters()) {
		return false
	}
	parameter := fn.Parameters()[i]
	return parameter.AsParameterDeclaration().DotDotDotToken == nil && parameter.Name().Kind == ast.KindIdentifier && l.onlyReads(fn, l.checker.GetSymbolAtLocation(parameter.Name()))
}

// callbackFunction is the function that argument always is, whose code is known: a function
// written there, a function declaration or a const's function, named there; nil for other
// values, which may be any function, and for functions with a `this` parameter, whose
// parameters aren't their arguments one for one.
func (l *lowerer) callbackFunction(argument *ast.Node) *ast.Node {
	fn := l.knownFunction(ast.SkipParentheses(argument))
	if fn == nil || slices.ContainsFunc(fn.Parameters(), func(parameter *ast.Node) bool {
		return parameter.Name().Kind == ast.KindIdentifier && parameter.Name().Text() == "this"
	}) {
		return nil
	}
	return fn
}

// knownFunction is the function node always is: a function written there, or a function
// declaration or a const's function, named there; otherwise nil.
func (l *lowerer) knownFunction(node *ast.Node) *ast.Node {
	switch node.Kind {
	case ast.KindArrowFunction, ast.KindFunctionExpression:
		return node
	case ast.KindIdentifier:
		symbol := l.checker.GetSymbolAtLocation(node)
		if symbol == nil || symbol.ValueDeclaration == nil {
			return nil
		}
		switch declaration := symbol.ValueDeclaration; declaration.Kind {
		case ast.KindFunctionDeclaration:
			if declaration.Body() != nil && len(symbol.Declarations) == 1 {
				return declaration // not an overloaded one's implementation, whose parameters may differ
			}
		case ast.KindVariableDeclaration:
			if declaration.Parent.Flags&ast.NodeFlagsConst != 0 && declaration.Initializer() != nil {
				if fn := ast.SkipParentheses(declaration.Initializer()); fn.Kind == ast.KindArrowFunction || fn.Kind == ast.KindFunctionExpression {
					return fn
				}
			}
		}
	}
	return nil
}
