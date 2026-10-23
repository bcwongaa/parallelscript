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
	dynamic := l.mentionsPrototypeInstance(declared)
	if !dynamic {
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
		return callbackSource(declaration)
	}
	for declaration.Kind == ast.KindBindingElement {
		declaration = declaration.Parent.Parent // the pattern's declaration or binding element; a default is a new value (R4-F2)
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
		holdsDynamicValues(target, map[ir.Type]bool{}) && node != nil && l.madeDynamicByInstanceof(node) && !l.isReadOnlyArgument(node) && !isReadOnlyParallelView(node)
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

// callbackSource is, for a parameter declared without a type of a function or arrow written
// as an argument of a method call (`c.items.forEach((x, i, arr) => …)`), the call's receiver,
// whose items and self the method hands its callback; otherwise nil.
func callbackSource(parameter *ast.Node) *ast.Node {
	fn := parameter.Parent
	if parameter.Type() != nil || fn.Kind != ast.KindArrowFunction && fn.Kind != ast.KindFunctionExpression {
		return nil
	}
	argument := fn
	for argument.Parent.Kind == ast.KindParenthesizedExpression {
		argument = argument.Parent
	}
	call := argument.Parent
	if call.Kind != ast.KindCallExpression || !slices.Contains(call.Arguments(), argument) {
		return nil
	}
	callee := ast.SkipParentheses(call.Expression())
	if !ast.IsAccessExpression(callee) {
		return nil
	}
	return callee.Expression()
}

// isCopiedCallbackParameter reports whether parameter, declared with a type that holds
// dynamic values (`(row: any[]) => …`), is a parameter of a callback that a method of a
// dynamic value an instanceof narrowing makes is given (madeDynamicByInstanceof): the
// method hands it the instance's typed objects, which its type would copy (R4-F2).
func (l *lowerer) isCopiedCallbackParameter(parameter *ast.Node, t ir.Type) bool {
	if parameter.Type() == nil || t == nil || t == ir.Value || isCheckable(t) || isFunction(t) || !holdsDynamicValues(t, map[ir.Type]bool{}) {
		return false
	}
	fn := parameter.Parent
	if fn.Kind != ast.KindArrowFunction && fn.Kind != ast.KindFunctionExpression {
		return false
	}
	call := fn.Parent
	for call.Kind == ast.KindParenthesizedExpression {
		call = call.Parent
	}
	if call.Kind != ast.KindCallExpression || !ast.IsAccessExpression(ast.SkipParentheses(call.Expression())) {
		return false
	}
	return l.madeDynamicByInstanceof(ast.SkipParentheses(call.Expression()).Expression())
}
