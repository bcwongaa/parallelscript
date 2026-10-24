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
	if l.holdsAnyInstance(node) && !l.storedDynamic(node) {
		// Any value tsc types C<any, …>, narrowed here or flowing on from a narrowing (H10b);
		// not a variable declared `unknown` or `any` or a member of one, a dynamic value
		// before, whose narrowings D-179 converts (R4-F1).
		return true
	}
	return l.isUninferredNarrowing(node)
}

// isUninferredNarrowing is isUninferredInstanceof for the references `instanceof` itself
// narrowed (D-218).
func (l *lowerer) isUninferredNarrowing(node *ast.Node) bool {
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
		// quietly: a library function's parameter (`Array.from`'s ArrayLike<T>) may be a type
		// no value is converted to, its argument lowered by the function's own rules
		l.quietly(func() bool { target = l.irType(t, node); return target != nil })
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
// type), a function type, whose conversion is an adapter, or a generic class's C<any, …>,
// which no other instance converts to. It holds what
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
	takesAnyInstance := l.destructuresAnyInstance(symbol.ValueDeclaration)
	if source == nil && !takesAnyInstance {
		return false
	}
	if !takesAnyInstance && !l.holdsInstanceofDynamicValue(source) {
		return false // checked first: storedDynamic asks of every variable, which irType mustn't make types for
	}
	declared := l.typeOfSymbol(symbol)
	var dynamic bool
	if annotated := symbol.ValueDeclaration.Kind == ast.KindParameter && symbol.ValueDeclaration.Type() != nil; annotated {
		// `(row: any[]) => …`: a type a copy of the object would have, where the method hands
		// the parameter its objects; one of the instance's own types (`(row: number[]) => …`)
		// takes the object as it is (R3-F2), and reduce's accumulator is the callback's own
		t := l.irType(declared, symbol.ValueDeclaration.Name())
		dynamic = l.handsInstanceObjects(symbol.ValueDeclaration) && (copiesInstanceObjects(t) || t == ir.Value) // `(row: unknown) => (row as any[]).push(x)` too
	} else if dynamic = l.mentionsPrototypeInstance(declared); !dynamic {
		// also a function type, whose adapter would be another function than the instance's
		// (`const h = s.listeners[0]; s.listeners.indexOf(h)`, R8-F1)
		t := l.irType(declared, symbol.ValueDeclaration.Name())
		dynamic = t != nil && t != ir.Value && !isCheckable(t) &&
			(!isFunction(t) || symbol.ValueDeclaration.Kind == ast.KindParameter || source != nil && l.isInstanceOwnValue(source)) || // `flag ? c.list[0] : addRow` is an adapter, judged where it goes (R9-F1)
			t == ir.Value && declared.Flags()&(checker.TypeFlagsAny|checker.TypeFlagsUnknown) == 0 // an iterator or promise of such values (`const it = c.matrix.values()`, C285)
	}
	l.instanceofLocals[key] = dynamic
	return dynamic
}

// destructuresAnyInstance reports whether declaration is a name an object pattern binds
// from a value tsc types C<any, …> (`for (const { items } of boxes)` over a Box<any>[], `({
// items }: Box<any>) => …`, nested in an array pattern too), or one a pattern nested in it
// binds: that object's member or what it holds, held as a dynamic value as `b.items` is
// (H10b).
func (l *lowerer) destructuresAnyInstance(declaration *ast.Node) bool {
	if declaration == nil || declaration.Kind != ast.KindBindingElement {
		return false
	}
	for pattern := declaration.Parent; ; pattern = pattern.Parent.Parent { // also `{ meta: { tags } }`, `{ rows: [first] }` (R2-F4)
		if pattern.Kind == ast.KindObjectBindingPattern && l.anyInstanceIn(l.typeAt(pattern)) != nil {
			return true
		}
		if pattern.Parent == nil || pattern.Parent.Kind != ast.KindBindingElement {
			return false
		}
	}
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

// patternSource is the expression whose value a binding pattern takes apart: its
// declaration's initializer, the iterable a for…of declaration takes items of, what a method
// hands the callback whose parameter it is (callbackSource), or for a nested pattern its
// outermost pattern's; for an object or array literal on the left of an assignment, also
// nested, the assignment's right side (`({ name, ...r } = b.meta)`); nil otherwise.
func (l *lowerer) patternSource(pattern *ast.Node) *ast.Node {
	if pattern.Kind == ast.KindObjectLiteralExpression || pattern.Kind == ast.KindArrayLiteralExpression {
		for node := pattern; ; node = node.Parent {
			switch parent := node.Parent; parent.Kind {
			case ast.KindPropertyAssignment, ast.KindObjectLiteralExpression, ast.KindArrayLiteralExpression, ast.KindParenthesizedExpression:
			case ast.KindBinaryExpression:
				expr := parent.AsBinaryExpression()
				if expr.OperatorToken.Kind != ast.KindEqualsToken || expr.Left != node {
					return nil
				}
				if outer := parent.Parent.Kind; outer != ast.KindPropertyAssignment && outer != ast.KindArrayLiteralExpression {
					return expr.Right // not an element's default (`{ meta: { ...r } = fallback }`)
				}
			default:
				return nil
			}
		}
	}
	declaration := pattern.Parent
	for declaration.Kind == ast.KindBindingElement {
		declaration = declaration.Parent.Parent
	}
	switch list := declaration.Parent; {
	case declaration.Kind == ast.KindParameter:
		return l.callbackSource(declaration)
	case declaration.Kind != ast.KindVariableDeclaration:
		return nil
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
	return l.instanceofDynamicSource(node, sourceHeld)
}

// madeDynamicByInstanceof is holdsInstanceofDynamicValue for the values H10a made dynamic:
// not the members of a variable declared `unknown` or `any` or of a `catch` binding, which
// were dynamic values before and convert to typed places by copies, as D-179 says (R4-F1).
// It includes the values of places tsc types C<any, …> (H10b).
func (l *lowerer) madeDynamicByInstanceof(node *ast.Node) bool {
	return l.instanceofDynamicSource(node, sourceMade)
}

// narrowedByInstanceof is madeDynamicByInstanceof for the values an instanceof narrowing
// itself made dynamic (D-218), not those of a place tsc types C<any, …> without one (`b:
// Box<any>`, `const m = x`, `ident(x)`), which may hold any instance of C (H10b, D-219).
func (l *lowerer) narrowedByInstanceof(node *ast.Node) bool {
	return l.instanceofDynamicSource(node, sourceNarrowed)
}

// sourceMode is which values instanceofDynamicSource asks about.
type sourceMode uint8

const (
	sourceHeld     sourceMode = iota // holdsInstanceofDynamicValue's
	sourceMade                       // madeDynamicByInstanceof's
	sourceNarrowed                   // narrowedByInstanceof's
)

// made is mode, or for sourceHeld, sourceMade: what a member of an `unknown` narrowed to a
// generic class asks of its object.
func (mode sourceMode) made() sourceMode {
	if mode == sourceHeld {
		return sourceMade
	}
	return mode
}

func (l *lowerer) instanceofDynamicSource(node *ast.Node, mode sourceMode) bool {
	kind := askDynamicSource + narrowingAsk(mode)
	return l.narrowingAnswers.answer(narrowingQuestion{node, kind}, func() bool { return l.dynamicSource(node, mode) })
}

// uninferredSource is isUninferredInstanceof for mode: for sourceNarrowed, only a reference
// an instanceof narrowing itself narrowed (isUninferredNarrowing).
func (l *lowerer) uninferredSource(node *ast.Node, mode sourceMode) bool {
	if mode == sourceNarrowed {
		return l.isUninferredNarrowing(node)
	}
	return l.isUninferredInstanceof(node)
}

// narrowingAnswers keeps the answers to isNarrowedDynamic and instanceofDynamicSource while
// one of them is being answered, which asks both again for each object of a member chain
// (through isMemberOfNarrowedInstance and storedDynamic): without them, a chain of n members
// took 2ⁿ steps. They are dropped when the outermost question is answered, since lowering
// state they depend on (`?.` chains' objects, the instance) changes between questions.
type narrowingAnswers struct {
	depth   int
	answers map[narrowingQuestion]bool
}

type narrowingQuestion struct {
	node *ast.Node
	kind narrowingAsk
}

type narrowingAsk uint8

const (
	askNarrowedDynamic narrowingAsk = iota
	askDynamicSource                // and askDynamicSource + each sourceMode
)

// answer is the answer to q, compute's, or the one already given in the outermost question.
func (a *narrowingAnswers) answer(q narrowingQuestion, compute func() bool) bool {
	if known, ok := a.answers[q]; ok {
		return known
	}
	if a.answers == nil {
		a.answers = map[narrowingQuestion]bool{}
	}
	a.depth++
	known := compute()
	a.depth--
	if a.depth == 0 {
		clear(a.answers)
	} else {
		a.answers[q] = known
	}
	return known
}

func (l *lowerer) dynamicSource(node *ast.Node, mode sourceMode) bool {
	node = ast.SkipParentheses(node)
	if l.instanceofContainer(node, mode) {
		return true // `[...c.matrix]`, `new Set(c.matrix)`, `Array.from(c.store.values())` (C285)
	}
	switch node.Kind {
	case ast.KindIdentifier:
		return l.dynamicLocalSource(l.readSymbol(node), mode) || l.uninferredSource(node, mode) // readSymbol: also a shorthand property's variable
	case ast.KindPropertyAccessExpression, ast.KindElementAccessExpression:
		switch {
		case l.isInstanceofDynamicMember(node) || l.uninferredSource(node, mode):
			return true
		case l.isMemberOfNarrowedInstance(node):
			return mode == sourceHeld || l.uninferredSource(ast.SkipParentheses(node.Expression()), mode) || l.instanceofDynamicSource(node.Expression(), mode.made())
		}
		return l.instanceofDynamicSource(node.Expression(), mode)
	case ast.KindCallExpression:
		callee := ast.SkipParentheses(node.Expression())
		if l.isInstanceofDynamicMember(node) || ast.IsAccessExpression(callee) && l.instanceofDynamicSource(callee.Expression(), mode) {
			return true
		}
		if !ast.IsAccessExpression(callee) && l.instanceofDynamicSource(callee, mode) {
			return true // `mk()` for a `const mk = c.mkList` (R9-F3)
		}
		// `Promise.resolve(c.items)`, whose awaited value is the array (R4-F2), and
		// `Promise.all(c.matrix)`, whose awaited array's elements are its rows (C285)
		return callee.Kind == ast.KindPropertyAccessExpression && l.isPromiseConstructor(callee.Expression()) && promiseOfArguments[callee.AsPropertyAccessExpression().Name().Text()] &&
			len(node.Arguments()) == 1 && l.instanceofDynamicSource(node.Arguments()[0], mode)
	case ast.KindNonNullExpression, ast.KindAsExpression, ast.KindSatisfiesExpression, ast.KindTypeAssertionExpression, ast.KindAwaitExpression:
		return l.instanceofDynamicSource(node.Expression(), mode)
	case ast.KindPropertyAssignment:
		return l.instanceofDynamicSource(node.Initializer(), mode) // an object literal's `it: it`
	case ast.KindShorthandPropertyAssignment:
		return l.dynamicLocalSource(l.checker.GetShorthandAssignmentValueSymbol(node), mode) // `{ it }`
	case ast.KindConditionalExpression:
		conditional := node.AsConditionalExpression()
		return l.instanceofDynamicSource(conditional.WhenTrue, mode) || l.instanceofDynamicSource(conditional.WhenFalse, mode)
	case ast.KindBinaryExpression:
		expr := node.AsBinaryExpression()
		switch op := expr.OperatorToken.Kind; {
		case op == ast.KindQuestionQuestionToken || op == ast.KindBarBarToken || op == ast.KindAmpersandAmpersandToken:
			return l.instanceofDynamicSource(expr.Left, mode) || l.instanceofDynamicSource(expr.Right, mode)
		case op == ast.KindCommaToken || ast.IsAssignmentOperator(op):
			return l.instanceofDynamicSource(expr.Right, mode)
		}
	}
	return false
}

// promiseOfArguments are the Promise statics whose promise is fulfilled with what their
// argument holds: the value itself, or its items.
var promiseOfArguments = map[string]bool{"resolve": true, "all": true, "allSettled": true, "any": true, "race": true}

// dynamicLocalSource reports whether symbol is a variable isInstanceofDynamicLocal holds,
// whose value is one mode asks about.
func (l *lowerer) dynamicLocalSource(symbol *ast.Symbol, mode sourceMode) bool {
	if symbol == nil || !l.isInstanceofDynamicLocal(symbol) {
		return false
	}
	if mode == sourceHeld {
		return true
	}
	source := l.bindingSource(symbol)
	return source != nil && l.instanceofDynamicSource(source, mode)
}

// copiesInstanceofDynamicValue reports whether converting value, the lowered node, to target
// copies a dynamic value that an instanceof narrowing gives (holdsInstanceofDynamicValue):
// the object is an instance's, whose members are typed, so a target that holds dynamic
// values (`any[]`, an object type with an `any[]` property) takes it only as a copy, which
// writes to the object itself (`const a = c.items ?? []`, a default parameter, a field,
// `yield`) would miss silently; a parameter that only reads it, or a new array (`c.items.filter(…)`),
// may take a copy, as D-094 says, unless the copy's elements are copies too (copiesElements).
// A target of the instance's own types (`number[]`, `{ items: number[] }`) keeps the
// object as it is (R3-F2).
func (l *lowerer) copiesInstanceofDynamicValue(value ir.Expr, target ir.Type, node *ast.Node) bool {
	if node != nil && node.Kind == ast.KindParenthesizedExpression {
		node = ast.SkipParentheses(node)
	}
	return value.Type() == ir.Value && target != ir.Value && !isCheckable(target) && !isFunction(target) &&
		holdsDynamicValues(target, map[ir.Type]bool{}) && node != nil && l.madeDynamicByInstanceof(node) &&
		!((l.isReadOnlyArgument(node) || l.isNewArray(node)) && !copiesElements(target)) &&
		!(isReadOnlyParallelView(node) && l.holdsPrimitiveElements(node))
}

// copiesInstanceElements reports whether converting node, a typed member of an instanceof
// narrowing's instance that copiesDynamicValue keeps typed (`c.matrix` as an `any[][]`
// argument), to target copies objects of the instance's inside it too (copiesElements), so
// that D-094's copy for a new array or a parameter only read, which reading `rows[0]` and
// then writing to it passes, would miss writes to them (R6-F1).
func (l *lowerer) copiesInstanceElements(target ir.Type, node *ast.Node) bool {
	return node != nil && copiesElements(target) && l.madeDynamicByInstanceof(ast.SkipParentheses(node))
}

// holdsPrimitiveElements reports whether node, an array an instanceof narrowing holds as a
// dynamic value, holds primitives in the instance (`c.items` of a MapLRU<string, number>,
// `c.matrix[0]` of its `number[][]`): a copy of it is the array's values, where a copy of
// arrays or objects is of them too, so writes to its elements would miss the instance's
// (R5-F3).
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
	t := l.instanceTypeOf(node)
	if t == nil || !l.checker.IsArrayType(t) {
		return false
	}
	elem, ok := l.irType(t, node).(*ir.Array)
	return ok && isPrimitive(elem.Elem) || ok && ir.IsNumeric(elem.Elem)
}

// instanceTypeOf is node's type in the instance an instanceof narrowing resolves
// (instanceofInstance), for a member of the narrowed reference (`c.items`) or a property or
// element of one (`c.meta.tags`, `c.matrix[0]`), where tsc types it with C<any, …>'s; nil for
// other nodes.
func (l *lowerer) instanceTypeOf(node *ast.Node) *checker.Type {
	node = ast.SkipParentheses(node)
	if node.Kind == ast.KindNonNullExpression {
		return l.instanceTypeOf(node.Expression())
	}
	if !ast.IsAccessExpression(node) && node.Kind != ast.KindCallExpression {
		return nil
	}
	if node.Kind == ast.KindCallExpression {
		return l.instanceResultType(node)
	}
	receiver := ast.SkipParentheses(node.Expression())
	object := l.instanceofInstance(receiver, l.instantiate(l.checker.GetTypeAtLocation(receiver)))
	if object == nil {
		object = l.instanceTypeOf(receiver)
	}
	if object == nil {
		return nil
	}
	object = l.checker.GetNonNullableType(object)
	if node.Kind == ast.KindPropertyAccessExpression {
		return l.checker.GetTypeOfPropertyOfType(object, node.AsPropertyAccessExpression().Name().Text())
	}
	key := node.AsElementAccessExpression().ArgumentExpression
	if checker.IsTupleType(object) && ast.IsNumericLiteral(key) {
		return l.checker.GetTypeOfPropertyOfType(object, key.Text())
	}
	if l.checker.IsArrayType(object) {
		return l.checker.GetTypeArguments(object)[0]
	}
	return nil
}

// instanceResultType is instanceTypeOf for call, a call of an array's method on an array whose
// type in the instance is known: the array's type for a method that makes an array of its
// elements (`c.items.filter(…)`, `slice`, `toSorted`), its element type for one that gives an
// element (`at`, `find`); nil otherwise.
func (l *lowerer) instanceResultType(call *ast.Node) *checker.Type {
	callee := ast.SkipParentheses(call.Expression())
	if callee.Kind != ast.KindPropertyAccessExpression {
		return nil
	}
	array := l.instanceTypeOf(callee.Expression())
	if array == nil || !l.checker.IsArrayType(l.checker.GetNonNullableType(array)) {
		return nil
	}
	switch callee.AsPropertyAccessExpression().Name().Text() {
	case "filter", "slice", "toSorted", "toReversed", "toSpliced", "with":
		return array
	case "at", "find", "findLast", "pop", "shift":
		return l.checker.GetTypeArguments(l.checker.GetNonNullableType(array))[0]
	}
	return nil
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
	if node.Kind == ast.KindIdentifier || len(l.genericClasses) == 0 {
		return false
	}
	t := l.checker.GetNonNullableType(l.typeAt(node))
	if !l.mentionsAny(t, 0) || !l.holdsInstanceofDynamicValue(node) {
		return false // the instance's own types are what tsc says where they don't mention any
	}
	if t.Flags()&checker.TypeFlagsObject == 0 || l.isCheckedValueType(t) || len(l.checker.GetSignaturesOfType(t, checker.SignatureKindCall)) > 0 {
		return false
	}
	irType := l.irType(t, node)
	return copiesInstanceObjects(irType) || irType != nil && !isFunction(irType) && holdsFunctions(irType, map[ir.Type]bool{}) // a container of functions converts by adapters (H10b)
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
	family, _, kind := l.familyPrivateMember(node)
	if kind != ast.KindPropertyDeclaration {
		return nil, false // a private accessor, which method tables hold by name, or a method
	}
	object := l.expression(node.Expression())
	if object == nil {
		return nil, true
	}
	x = &ir.FamilyField{X: box(object), Family: family, Name: node.AsPropertyAccessExpression().Name().Text()}
	if l.mentionsPrototypeInstance(l.typeAt(node)) || l.isInstanceofNestedObject(node) {
		// tsc's C<any, …> (`o.#next`), which another instance converts to only as a dynamic
		// value, or an array or map of the instance's that a conversion would copy (`o.#rows`,
		// whose writes must reach it: H10b)
		return x, true
	}
	if t := l.irType(l.typeAt(node), node); t != nil && t != ir.Value {
		return l.checkedConversion(x, t, node), true
	}
	return x, true
}

// familyPrivatePlace is the place `o.#f` (node) names for an o familyPrivateField reads from:
// the private field of whichever of C's instances o is, written with a check (FamilySetField,
// C274). handled is false for other targets.
func (l *lowerer) familyPrivatePlace(node *ast.Node) (target place, ok bool, handled bool) {
	family, _, kind := l.familyPrivateMember(node)
	if kind != ast.KindPropertyDeclaration {
		return place{}, false, false
	}
	object := l.expression(node.Expression())
	return place{object: box(object), family: family, private: node.AsPropertyAccessExpression().Name().Text(), dynamic: true}, object != nil, true
}

// familyPrivateCall lowers `o.#m(…)` (call, of callee `o.#m`) for an o familyPrivateField reads
// from: the private method of whichever of C's instances o is (FamilyCall), or, for a private
// field holding a function (`o.#cb(1)`), the function it holds called through any (C274).
// ok is false for other calls.
func (l *lowerer) familyPrivateCall(call *ast.CallExpression, callee *ast.Node) (x ir.Expr, ok bool) {
	family, class, kind := l.familyPrivateMember(callee)
	switch kind {
	case ast.KindPropertyDeclaration:
		function, _ := l.familyPrivateField(callee)
		if function == nil {
			return nil, true
		}
		return l.dynamicCallWith(call, callee, ir.DynamicCall, []ir.Expr{box(function)}), true
	case ast.KindMethodDeclaration:
	default:
		return nil, false
	}
	name := callee.AsPropertyAccessExpression().Name().Text()
	if declaration := privateDeclaration(callee.AsPropertyAccessExpression().Name()); declaration != nil && len(declaration.TypeParameters()) > 0 {
		l.wantInMethodTables(name) // a generic private method: each instance's erased copy, which FamilyCall finds by name (D-078)
	}
	object := l.expression(callee.Expression())
	if object == nil {
		return nil, true
	}
	privateCall := &ir.FamilyCall{X: box(object), Family: family, Class: class, Name: name}
	for _, argument := range call.Arguments.Nodes {
		if argument.Kind == ast.KindSpreadElement {
			l.notSupportedYet(argument, "a spread argument of a private method called on a value held as a dynamic value is")
			return nil, true
		}
		value := l.valueExpression(argument)
		if value == nil {
			return nil, true
		}
		privateCall.Args = append(privateCall.Args, box(value))
	}
	return privateCall, true
}

// familyPrivateMember is, for node, `o.#name` of an o held as a dynamic value (a variable
// declared any or unknown or a member of one, a value tsc types C<any, …>: storedDynamic,
// isUninferredInstanceof, isAny), the classes whose objects have the private member (the
// instances of a generic class, or the one class), the class's name and the kind of the
// member's declaration; otherwise nil, "" and KindUnknown. A static member, which only the
// class itself has, is the class's.
func (l *lowerer) familyPrivateMember(node *ast.Node) (family *[]*ir.Class, class string, kind ast.Kind) {
	if node.Kind != ast.KindPropertyAccessExpression {
		return nil, "", ast.KindUnknown
	}
	name := node.AsPropertyAccessExpression().Name()
	receiver := ast.SkipParentheses(node.Expression())
	if name.Kind != ast.KindPrivateIdentifier || !l.isAny(receiver) && !l.storedDynamic(receiver) && !l.isUninferredInstanceof(receiver) {
		return nil, "", ast.KindUnknown
	}
	member := privateDeclaration(name)
	if member == nil || ast.HasSyntacticModifier(member, ast.ModifierFlagsStatic) {
		return nil, "", ast.KindUnknown
	}
	if generic := l.genericClasses[l.declaredSymbol(member.Parent)]; generic != nil {
		return &generic.classes, generic.node.Name().Text(), member.Kind
	}
	if one := l.classes[l.declaredSymbol(member.Parent)]; one != nil {
		return &[]*ir.Class{one}, one.Name, member.Kind
	}
	return nil, "", ast.KindUnknown
}

// privateDeclaration is the declaration of the private member that name (`#v`) names: the
// innermost enclosing class's member of that name, since private names are lexical (tsc
// gives no symbol for one on an `any`).
func privateDeclaration(name *ast.Node) *ast.Node {
	for node := name.Parent; node != nil; node = node.Parent {
		if !ast.IsClassLike(node) {
			continue
		}
		for _, member := range node.Members() {
			if member.Name() != nil && member.Name().Kind == ast.KindPrivateIdentifier && member.Name().Text() == name.Text() {
				return member
			}
		}
	}
	return nil
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
		// instantiated: `this.next`'s ListNode<T> | null is a ListNode<any> | null in the
		// instance for any, which tsc's type for it holds, no narrowing (H10b)
		if t := l.checker.GetTypeOfPropertyOfType(object, name.Text()); t != nil {
			return l.instantiate(t)
		}
		if infos := l.checker.GetIndexInfosOfType(object); len(infos) > 0 {
			return l.instantiate(infos[0].ValueType()) // `rec.main` of a Record<string, …>
		}
	case ast.KindElementAccessExpression:
		object := l.typeAt(node.Expression())
		if key := node.AsElementAccessExpression().ArgumentExpression; ast.IsStringOrNumericLiteralLike(key) {
			if t := l.checker.GetTypeOfPropertyOfType(object, key.Text()); t != nil {
				return l.instantiate(t) // a tuple's element, an object type's property
			}
		}
		if infos := l.checker.GetIndexInfosOfType(object); len(infos) > 0 {
			return l.instantiate(infos[0].ValueType()) // an array's or record's element
		}
	}
	return nil
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
// a method call (callbackArgument), what the method hands the callback (callSource); nil for
// a rest parameter, whose array is a new one, and for other parameters.
func (l *lowerer) callbackSource(parameter *ast.Node) *ast.Node {
	if parameter.AsParameterDeclaration().DotDotDotToken != nil {
		return nil
	}
	call, _ := callbackArgument(parameter.Parent)
	if call == nil {
		return nil
	}
	return l.callSource(call)
}

// callSource is what the method a call calls hands the callbacks it is given: the call's
// receiver, whose items and self an array's method hands them (`c.items` of
// `c.items.forEach((x, i, arr) => …)`), or the method itself where it is the narrowed
// instance's own, called through `any` (callsInstanceofMethodDynamically: `c.each((items) =>
// …)`), or one of a dynamic value narrowed to a generic class (isMemberOfNarrowedInstance:
// `u.each(…)` for an `unknown` u), whose code hands them the instance's values; nil for a call
// of something else.
func (l *lowerer) callSource(call *ast.Node) *ast.Node {
	callee := ast.SkipParentheses(call.Expression())
	if !ast.IsAccessExpression(callee) {
		return nil
	}
	if l.callsInstanceofMethodDynamically(call.AsCallExpression()) || l.isMemberOfNarrowedInstance(callee) {
		return callee
	}
	return callee.Expression()
}

// handsInstanceObjects reports whether the method that the callback of parameter is given to
// (callbackArgument) may hand the parameter objects of its receiver or of its instance: an
// array's method its first argument's element and array parameters
// (arrayCallbackObjectParameters, but not an accumulator it owns), any other method, and an
// array's method that keeps it (`c.list.push(f)`), any parameter.
func (l *lowerer) handsInstanceObjects(parameter *ast.Node) bool {
	fn := parameter.Parent
	call, argument := callbackArgument(fn)
	if call == nil {
		return false
	}
	method, ok := l.arrayMethodName(ast.SkipParentheses(call.Expression()))
	if !ok || keepingArrayMethods[method] {
		return true
	}
	index := slices.Index(fn.Parameters(), parameter)
	if index == 0 && parameter.Name().Kind == ast.KindIdentifier && l.ownsAccumulator(method, call.Arguments(), l.irType(l.typeOfSymbol(l.checker.GetSymbolAtLocation(parameter.Name())), parameter.Name())) {
		return false
	}
	return call.Arguments()[0] == argument && slices.Contains(l.arrayCallbackObjectParameters(method, call.Arguments()), index)
}

// keepingArrayMethods are the array methods that keep their arguments in the array, or in the
// array they make of its elements and them (`concat`, `with`, `toSpliced`, whose elements code
// reads as the instance's own: R10-F1), where code that reads it may call a kept function with
// any of its objects (R7-F2).
var keepingArrayMethods = map[string]bool{"push": true, "unshift": true, "splice": true, "fill": true, "concat": true, "with": true, "toSpliced": true}

// returnsToDynamicInstanceCall reports whether fn, a function written without a result type,
// is a callback (callbackArgument) of a method whose code an instanceof narrowing's dynamic
// value has (callSource: `c.items.reduce`, `o.each`, the narrowed instance's own method): the
// method's code takes what it returns as a dynamic value, which may be an object of the
// instance's that it was handed (`return acc`), so a copy would miss its writes.
func (l *lowerer) returnsToDynamicInstanceCall(fn *ast.Node) bool {
	if fn.Type() != nil {
		return false
	}
	call, _ := callbackArgument(fn)
	if call == nil {
		return false
	}
	source := l.callSource(call)
	return source != nil && l.holdsInstanceofDynamicValue(source)
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
// other method may hand a callback anything, or keep it for code that does (`c.handlers.set(k,
// f)`, called later with the instance's arrays: R6-F6). A function written there or named there whose
// code is known (callbackFunction) takes a copy at a parameter it only reads, as D-094 says,
// where the copy's elements aren't copies too (copiesElements: `rows: any[][]`, whose
// `rows[0].push(x)` reads rows only); a parameter written there that would copy is a dynamic
// value (isInstanceofDynamicLocal).
//
// A callback given to a method of a value a C<any, …> place holds without a narrowing (H10b)
// converts as one given through `any` does (D-075, D-071), since the place may hold C's
// instance for any, whose objects it takes as they are (D-219).
func (l *lowerer) copiesCallbackArgument(call *ast.CallExpression, callee *ast.Node, index int, argument *ast.Node, value ir.Expr) bool {
	t, _ := l.callbackCopy(call, callee, index, argument, value, true)
	if t != nil {
		l.reportCallbackCopy(t, argument)
	}
	return t != nil
}

// instanceCallbackArgument is value, a function (or a value holding functions) given to a method of
// callee's object that an instanceof narrowing made dynamic (copiesCallbackArgument's
// PTS1001: nil, reported), or of a C<any, …> place's value (H10b): there, a function given
// by reference whose parameter would get a copy of an object the method hands it takes its
// arguments as they are (asIsAdapter); others, as through `any`.
func (l *lowerer) instanceCallbackArgument(call *ast.CallExpression, callee *ast.Node, index int, argument *ast.Node, value ir.Expr) ir.Expr {
	if l.narrowedByInstanceof(callee) {
		if l.copiesCallbackArgument(call, callee, index, argument, value) {
			return nil
		}
		return value
	}
	if !l.madeDynamicByInstanceof(callee) {
		return value
	}
	t, function := l.callbackCopy(call, callee, index, argument, value, false)
	if t == nil {
		return value
	}
	if function {
		return l.anyInstanceFunction(value, argument, l.anyInstanceRoot(callee))
	}
	l.reportCallbackCopy(t, argument)
	return nil
}

// callbackCopy is copiesCallbackArgument's test for callee's object, one an instanceof
// narrowing made dynamic (narrowed) or else a C<any, …> place's: the type of the copying
// parameter it finds, if any, and whether value is the function itself (not a dynamic value
// or a literal holding one).
func (l *lowerer) callbackCopy(call *ast.CallExpression, callee *ast.Node, index int, argument *ast.Node, value ir.Expr, narrowed bool) (ir.Type, bool) {
	if value.Type() == ir.Value && narrowed && !l.isInstanceOwnValue(argument) && l.holdsCopyingFunction(argument) {
		// `c.list.push(flag ? c.list[0] : addRow)`: a dynamic value that may be the program's
		// function, judged by tsc's type (R9-F1); a C<any, …> place's converts as through any
		return copyingParameterIn(l.irType(l.typeAt(argument), argument), map[ir.Type]bool{}), false
	}
	if !holdsFunctions(value.Type(), map[ir.Type]bool{}) || l.isInstanceOwnValue(argument) {
		return nil, false // `c.list.concat([c.list[0]])`: the instance's own functions (R11-F2)
	}
	var parameters []int // those the method hands its objects; nil for every one
	method, isArrayMethod := l.arrayMethodName(callee)
	if isArrayMethod && !keepingArrayMethods[method] {
		parameters = l.arrayCallbackObjectParameters(method, call.Arguments.Nodes)
		if index != 0 || parameters == nil {
			return nil, false // an array's method calls no other argument
		}
	}
	fn, ok := value.Type().(*ir.FuncType)
	if !ok {
		if parameters != nil {
			return nil, false
		}
		return copyingParameterIn(value.Type(), map[ir.Type]bool{}), false
	}
	declaration := l.callbackFunction(argument)
	for i := range parameterCount(fn) {
		if declaration != nil && !declaresArgument(declaration, i) {
			break // a parameter the function's type adds for the callback type's, which its code doesn't have
		}
		t := parameterTypeAt(fn, i)
		if parameters != nil && !slices.Contains(parameters, i) || !copiesInstanceObjects(t) || declaration != nil && !copiesElements(t) && l.onlyReadsParameter(declaration, i) ||
			i == 0 && l.ownsAccumulator(method, call.Arguments.Nodes, t) {
			continue
		}
		return t, true
	}
	return nil, false
}

// copiesSpreadCallbacks is copiesCallbackArgument for spread, a spread argument of a call
// through `any` of callee (`c.items.forEach(...fns)`): its items, whatever position they take,
// are reported if they hold a function with a parameter a dynamic value converts to by a copy.
func (l *lowerer) copiesSpreadCallbacks(callee, spread *ast.Node) bool {
	t := l.irType(l.typeAt(spread.Expression()), spread.Expression())
	if t == nil || !holdsFunctions(t, map[ir.Type]bool{}) || !l.narrowedByInstanceof(callee) {
		return false
	}
	if copying := copyingParameterIn(t, map[ir.Type]bool{}); copying != nil {
		l.reportCallbackCopy(copying, spread)
		return true
	}
	return false
}

// reportCallbackCopy reports, at argument, the function copiesCallbackArgument finds, whose
// parameter of type t would get a copy of the instance's objects.
func (l *lowerer) reportCallbackCopy(t ir.Type, argument *ast.Node) {
	l.notSupportedYet(argument, fmt.Sprintf("giving a function whose parameter is a %s to a method of the object an `instanceof` narrowing holds as a dynamic value, which may hand it that object's own values (the parameter would get a copy, which wouldn't show changes; a parameter of type `any` takes them as they are), is", t))
}

// isInstanceOwnValue reports whether node always gives a value an instanceof narrowing holds
// (holdsInstanceofDynamicValue), never one of the program's: every branch of a conditional,
// `??`, `||` and `&&`, and every element or property of a literal that holds a function with
// a parameter a dynamic value converts to by a copy (`[c.list[1], c.list[0]]`). Storing it in
// the instance needs no adapter, and the instance's code calling it hands it nothing it would
// copy; `c.maybe ?? addRow` may be the program's `addRow` (R9-F1, R9-F2).
func (l *lowerer) isInstanceOwnValue(node *ast.Node) bool {
	node = ast.SkipParentheses(node)
	switch node.Kind {
	case ast.KindConditionalExpression:
		conditional := node.AsConditionalExpression()
		return l.isInstanceOwnValue(conditional.WhenTrue) && l.isInstanceOwnValue(conditional.WhenFalse)
	case ast.KindBinaryExpression:
		expr := node.AsBinaryExpression()
		switch op := expr.OperatorToken.Kind; {
		case op == ast.KindQuestionQuestionToken || op == ast.KindBarBarToken || op == ast.KindAmpersandAmpersandToken:
			return l.isInstanceOwnValue(expr.Left) && l.isInstanceOwnValue(expr.Right)
		case op == ast.KindCommaToken || ast.IsAssignmentOperator(op):
			return l.isInstanceOwnValue(expr.Right)
		}
		return false
	case ast.KindNonNullExpression, ast.KindAsExpression, ast.KindSatisfiesExpression, ast.KindTypeAssertionExpression, ast.KindAwaitExpression:
		return l.isInstanceOwnValue(node.Expression())
	case ast.KindArrayLiteralExpression:
		return !slices.ContainsFunc(node.Elements(), func(element *ast.Node) bool {
			if element.Kind == ast.KindSpreadElement {
				return !l.isInstanceOwnValue(element.Expression())
			}
			return element.Kind != ast.KindOmittedExpression && !l.isInstanceOwnValue(element) && l.holdsCopyingFunction(element)
		})
	case ast.KindObjectLiteralExpression:
		return !slices.ContainsFunc(node.Properties(), func(property *ast.Node) bool {
			switch property.Kind {
			case ast.KindPropertyAssignment:
				return !l.isInstanceOwnValue(property.Initializer()) && l.holdsCopyingFunction(property.Initializer())
			case ast.KindShorthandPropertyAssignment:
				return !l.isInstanceOwnValue(property.Name()) && l.holdsCopyingFunction(property.Name())
			case ast.KindSpreadAssignment:
				return !l.isInstanceOwnValue(property.Expression())
			}
			return l.holdsCopyingFunction(property) // a method or accessor, the program's own
		})
	case ast.KindCallExpression:
		// `c.list.concat([f])`, `c.list.with(0, f)`: an array of the instance's elements and the
		// arguments' (R9-F1)
		if callee := ast.SkipParentheses(node.Expression()); callee.Kind == ast.KindPropertyAccessExpression && addingArrayMethods[callee.AsPropertyAccessExpression().Name().Text()] &&
			slices.ContainsFunc(node.Arguments(), func(argument *ast.Node) bool {
				return !l.isInstanceOwnValue(argument) && l.holdsCopyingFunction(argument)
			}) {
			return false
		}
	case ast.KindIdentifier:
		symbol := l.readSymbol(node)
		if symbol == nil || !l.isInstanceofDynamicLocal(symbol) {
			return l.isUninferredInstanceof(node)
		}
		source := l.bindingSource(symbol)
		if symbol.ValueDeclaration.Kind == ast.KindParameter {
			return source != nil // a callback's: what the method hands it
		}
		return source != nil && l.isInstanceOwnValue(source)
	}
	return l.holdsInstanceofDynamicValue(node) // a member, element or call result of one
}

// addingArrayMethods are the array methods whose results hold their arguments too.
var addingArrayMethods = map[string]bool{"concat": true, "with": true, "toSpliced": true}

// isInstanceOwnFunction reports whether node, a `??`, `||` or `&&` of functions, gives only the
// instance's own (isInstanceOwnValue: `c.maybe ?? c.list[1]`), which stays the function it is
// rather than an adapter of tsc's type, so `===` and `indexOf` find it (R10-F2).
func (l *lowerer) isInstanceOwnFunction(node *ast.Node) bool {
	t := l.checker.GetNonNullableType(l.typeAt(node))
	return len(l.checker.GetSignaturesOfType(t, checker.SignatureKindCall)) > 0 && l.isInstanceOwnValue(node)
}

// holdsCopyingFunction reports whether tsc types node as, or as holding, a function with a
// parameter a dynamic value converts to by a copy (copyingParameterIn).
func (l *lowerer) holdsCopyingFunction(node *ast.Node) bool {
	t := l.typeAt(node)
	if !l.mentionsAny(t, 0) {
		return false
	}
	it := l.irType(t, node)
	return it != nil && holdsFunctions(it, map[ir.Type]bool{}) && copyingParameterIn(it, map[ir.Type]bool{}) != nil
}

// isInstanceofDynamicPlace reports whether target, an assignment's target, is a place in a
// dynamic value an instanceof narrowing made (`c.list[2]`, `c.cfg.onRows`, `c.list`), or a
// pattern with one (`[c.list[0]] = …`, `({ f: c.cfg.onRows } = …)`).
func (l *lowerer) isInstanceofDynamicPlace(target *ast.Node) bool {
	target = ast.SkipParentheses(target)
	switch target.Kind {
	case ast.KindArrayLiteralExpression:
		return slices.ContainsFunc(target.Elements(), l.isInstanceofDynamicPlace)
	case ast.KindObjectLiteralExpression:
		return slices.ContainsFunc(target.Properties(), func(property *ast.Node) bool {
			switch property.Kind {
			case ast.KindPropertyAssignment:
				return l.isInstanceofDynamicPlace(property.Initializer())
			case ast.KindSpreadAssignment:
				return l.isInstanceofDynamicPlace(property.Expression())
			}
			return false
		})
	case ast.KindSpreadElement:
		return l.isInstanceofDynamicPlace(target.Expression())
	case ast.KindBinaryExpression: // a target with a default, `[c.list[0] = f] = …`
		return l.isInstanceofDynamicPlace(target.AsBinaryExpression().Left)
	}
	return ast.IsAccessExpression(target) && (l.narrowedByInstanceof(target) || l.narrowedByInstanceof(target.Expression()))
}

// storesCopyingFunction reports, at value, a function stored by an assignment into a dynamic
// value an instanceof narrowing made (`c.list[2] = f`, `c.cfg.onRows = f`, `c.list = [f]`, a
// destructuring assignment with such a target, `Object.assign(c.cfg, { onRows: f })`) that has
// a parameter a dynamic value converts to by a copy: the instance's code may call it later
// with its own objects, which it would get as copies (R7-F2, R8-F3).
func (l *lowerer) storesCopyingFunction(target, value *ast.Node) bool {
	if !l.isInstanceofDynamicPlace(target) || !l.mentionsAny(l.typeAt(value), 0) || l.isInstanceOwnValue(value) {
		return false // no parameter type a dynamic value converts to by a copy, or the instance's own functions (`c.cfg.onRows = c.list[0]`, R8-F2)
	}
	t := l.irType(l.typeAt(value), value)
	if t == nil || !holdsFunctions(t, map[ir.Type]bool{}) {
		return false
	}
	copying := copyingParameterIn(t, map[ir.Type]bool{})
	if copying == nil {
		return false
	}
	l.notSupportedYet(value, fmt.Sprintf("storing a function whose parameter is a %s in the object an `instanceof` narrowing holds as a dynamic value, whose code may call it with that object's own values (the parameter would get a copy, which wouldn't show changes; a parameter of type `any` takes them as they are), is", copying))
	return true
}

// storedFunctionValue is value, the lowered right side (valueNode) of an assignment to
// target, a member of a C<any, …> place's value (`m.cfg.onRows = addAll`, `m.list[0] =
// addRow`), as asIsAdapter makes it where it is a function with a parameter that would get a
// copy of the instance's objects when its code calls it (H10b); otherwise value, also where
// an instanceof narrowing made the object dynamic (storesCopyingFunction's PTS1001).
func (l *lowerer) storedFunctionValue(target, valueNode *ast.Node, value ir.Expr) ir.Expr {
	target = ast.SkipParentheses(target)
	fn, ok := value.Type().(*ir.FuncType)
	if !ok || !ast.IsAccessExpression(target) || l.isInstanceOwnValue(valueNode) || copyingParameterIn(fn, map[ir.Type]bool{}) == nil ||
		!l.madeDynamicByInstanceof(target) && !l.madeDynamicByInstanceof(target.Expression()) {
		return value
	}
	return l.anyInstanceFunction(value, valueNode, l.anyInstanceRoot(target))
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
// array's method that it hands objects it didn't make: an element and the array itself, and
// reduce's accumulator, which is the first element where the call (of arguments) gives no
// initial value and otherwise that value, unless the value is the accumulator's own
// (ownsAccumulator). nil for a method that calls no callback.
func (l *lowerer) arrayCallbackObjectParameters(method string, arguments []*ast.Node) []int {
	switch method {
	case "reduce", "reduceRight":
		return []int{0, 1, 3} // the accumulator unless ownsAccumulator
	case "sort", "toSorted":
		return []int{0, 1}
	case "every", "filter", "find", "findIndex", "findLast", "findLastIndex", "flatMap", "forEach", "map", "some":
		return []int{0, 2}
	}
	return nil
}

// ownsAccumulator reports whether the accumulator of type t of a callback that call, of an
// array's method (method), is given is the callback's own: reduce's initial value is a new
// array, object or collection (`[]`, `{}`, `new Map()`), or a value of the accumulator's type
// that an instanceof narrowing doesn't give, which the callback gets as it is, not as a copy
// (R7-F1). Another initial value (`c.tags`, a `number[]` where the accumulator is an `any[]`)
// may be the instance's, or convert by a copy (R6-F2).
func (l *lowerer) ownsAccumulator(method string, arguments []*ast.Node, t ir.Type) bool {
	if method != "reduce" && method != "reduceRight" || len(arguments) < 2 {
		return false
	}
	initial := ast.SkipParentheses(arguments[1])
	for initial.Kind == ast.KindAsExpression || initial.Kind == ast.KindSatisfiesExpression {
		initial = ast.SkipParentheses(initial.Expression())
	}
	switch initial.Kind {
	case ast.KindArrayLiteralExpression, ast.KindObjectLiteralExpression, ast.KindNewExpression:
		return true
	}
	return l.isNewArray(initial) || !l.holdsInstanceofDynamicValue(initial) && l.irType(l.typeAt(initial), initial) == t
}

// copiesInstanceObjects reports whether t is a type that a dynamic value holding an
// instance's typed object converts to by a copy: one that holds dynamic values (`any[]`, `{
// items: any[] }`), not checked as it is (a primitive, a class) or adapted (a function).
func copiesInstanceObjects(t ir.Type) bool {
	return t != nil && t != ir.Value && !isCheckable(t) && !isFunction(t) && holdsDynamicValues(t, map[ir.Type]bool{})
}

// copiesElements reports whether converting a dynamic value to t, an array type, copies its
// elements too (`any[][]`, `{ list: any[] }[]`), so that writes to them, which reading the
// array allows, would miss the instance's.
func copiesElements(t ir.Type) bool {
	array, ok := t.(*ir.Array)
	return ok && copiesInstanceObjects(array.Elem)
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

// functionTypeIn reports whether t is, or holds as an array's element, an object type's,
// shape's or tuple's field or a union's member, a function type that test accepts; test is given the
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
		return (t.Shape || t.Tuple) && inFields(t.Fields)
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

// isRestParameter reports whether a parameter declaration is a rest parameter.
func isRestParameter(parameter *ast.Node) bool {
	return parameter.AsParameterDeclaration().DotDotDotToken != nil
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
	if parameter.AsParameterDeclaration().DotDotDotToken != nil || parameter.Name().Kind != ast.KindIdentifier {
		return false
	}
	symbol := l.checker.GetSymbolAtLocation(parameter.Name())
	return l.onlyReads(fn, symbol) && !l.handsToCallback(fn, symbol)
}

// handsToCallback reports whether fn calls an array method on its parameter param with a
// callback that the method may hand param itself (forEach's third parameter, reduce's
// fourth): a callback whose code isn't known (knownFunction), or one that declares that
// parameter, through which writes reach the array (`row.forEach((x, i, a) => { a[i] = 8; })`;
// R6-F1's class).
func (l *lowerer) handsToCallback(fn *ast.Node, param *ast.Symbol) bool {
	hands := false
	var visit func(node *ast.Node) bool
	visit = func(node *ast.Node) bool {
		if node.Kind == ast.KindCallExpression {
			callee := ast.SkipParentheses(node.Expression())
			if callee.Kind == ast.KindPropertyAccessExpression && l.checker.GetSymbolAtLocation(ast.SkipParentheses(callee.Expression())) == param && len(node.Arguments()) > 0 {
				arrayAt := 2
				if name := callee.AsPropertyAccessExpression().Name().Text(); name == "reduce" || name == "reduceRight" {
					arrayAt = 3
				}
				known := l.knownFunction(ast.SkipParentheses(node.Arguments()[0])) // written there, or named (R7-F3)
				if known == nil || len(known.Parameters()) > arrayAt || slices.ContainsFunc(known.Parameters(), isRestParameter) {
					hands = true
					return true
				}
			}
		}
		return node.ForEachChild(visit)
	}
	visit(fn.Body())
	return hands
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
