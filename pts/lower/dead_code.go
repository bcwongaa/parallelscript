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
// instance's types can't express. Code that tsc already narrows to never in the generic
// code (an exhaustive switch's default) is the program's and lowers as before.

// deadRead is a read in unit, evaluated where unit starts with no narrowing in between,
// that the instance being lowered narrows to a type no value has, though tsc's narrowed
// type in the generic code and the variable's or property's declared type have values for
// the instance: unit can't run. It is nil outside instances and if unit has no such read.
func (l *lowerer) deadRead(unit *ast.Node) *ast.Node {
	if l.subst == nil || len(l.subst.parameters) == 0 {
		return nil
	}
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
// y = x`), whose type is the narrowed one that later code reads. A dead read there (one a
// type predicate that lied lets run: `if (isNumber(x)) console.log("number", x)`) lowers as
// the value x holds, as before, rather than making its unit a trap (D-195).
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
// that has no value there for the instance being lowered: no value of its declared type
// has the type of the tests that narrow it (their intersection: `string & number` for a
// string T's `x` after `typeof x === "number"`, `string & null` and `Ci & Sq` where tsc
// narrows a `T extends string | null` to null, or a `T extends Sq | Ci` to Sq, through the
// constraint), while in the generic code some value has tsc's narrowed type, and the
// declared type has values for the instance (an element of a `never[]`, of type never, is
// undefined). The tests' type is tsc's narrowed type without the instance's type parameters
// it intersects (testedType): tsc keeps a narrowing across what it doesn't follow (a closure's assignment, a
// call that changes a property, an await, a getter), so the value may have changed since,
// though never to one its declared type doesn't have, since stores are typed and checked.
func (l *lowerer) isDeadRead(node *ast.Node) bool {
	var symbol *ast.Symbol
	switch node.Kind {
	case ast.KindIdentifier:
		if ast.IsDeclarationName(node) && node.Parent.Kind != ast.KindShorthandPropertyAssignment {
			return false
		}
		if symbol = l.readSymbol(node); symbol == nil || symbol.Flags&ast.SymbolFlagsVariable == 0 {
			return false
		}
	case ast.KindPropertyAccessExpression:
		if symbol = l.checker.GetSymbolAtLocation(node.Name()); symbol == nil || symbol.Flags&ast.SymbolFlagsProperty == 0 {
			return false
		}
	case ast.KindElementAccessExpression: // `t[0]` of a tuple, `o["v"]`: a key that names a property
		if symbol = l.checker.GetSymbolAtLocation(node.AsElementAccessExpression().ArgumentExpression); symbol == nil || symbol.Flags&ast.SymbolFlagsProperty == 0 {
			return false
		}
	default:
		return false
	}
	if ast.IsWriteOnlyAccess(node) || ast.IsOptionalChain(node) { // a chain's own undefined isn't the property's
		return false
	}
	declared, narrowed := l.checker.GetTypeOfSymbol(symbol), l.checker.GetTypeAtLocation(node)
	if l.uninhabited(l.checker.PtsIntersectionType([]*checker.Type{declared, narrowed})) {
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
		test.arguments[i] = l.widestOfIRType(argument)
	}
	l.deadTests[l.subst] = &test
	return &test
}

// widestOfIRType is the widest type whose values have t's IR type, or unknown where that
// isn't one this can name: a primitive's base type for a literal or enum type, t itself
// for a type that holds no literal types and no values of dynamic IR types, a union of
// those, any, unknown and never as they are.
func (l *lowerer) widestOfIRType(t *checker.Type) *checker.Type {
	switch {
	case t.Flags()&(checker.TypeFlagsAny|checker.TypeFlagsUnknown|checker.TypeFlagsNever) != 0:
		return t
	case l.brandedPrimitive(t) != nil:
		return l.checker.GetBaseTypeOfLiteralType(l.brandedPrimitive(t)) // `string & { __brand: "id" }` is a string
	case l.isDynamicIRType(t):
		return l.checker.GetUnknownType() // a recursive union's (`Json`), a promise's: unknown's
	case t.Flags()&checker.TypeFlagsPrimitive != 0:
		return l.checker.GetBaseTypeOfLiteralType(t) // `"a"` and `string` are one IR type
	case t.Flags()&checker.TypeFlagsUnion != 0:
		members := make([]*checker.Type, len(t.Types()))
		for i, member := range t.Types() {
			if members[i] = l.widestOfIRType(member); members[i].Flags()&checker.TypeFlagsUnknown != 0 {
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
// has none. t is a type argument of the instance being lowered, whose IR type its
// instance already made: asking makes nothing new.
func (l *lowerer) isDynamicIRType(t *checker.Type) bool {
	var irType ir.Type
	reports := l.reports
	lowered := l.quietly(func() bool {
		irType = l.irType(t, l.subst.declaration)
		return irType != nil && l.reports == reports
	})
	return !lowered || irType == ir.Value
}

// maxLiteralFreeDepth bounds how deep literalFree looks into a type's properties.
const maxLiteralFreeDepth = 4

// literalFree reports whether t holds no literal types (`{ kind: "sq" }`'s, which shares
// its IR type with `{ kind: "ci" }`'s) or branded primitives (string's IR type), and no
// types of dynamic values' IR type other than any and unknown (a promise, `{}`, a union
// that holds itself, as `Json` does), in its type arguments, properties, index signatures and call signatures:
// then no other type of its IR type is narrower. It asks no IR types, which a phantom type argument
// (`Tagged<Weird<string>>`'s) would make. A class of the program or the library is its
// own IR type.
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
