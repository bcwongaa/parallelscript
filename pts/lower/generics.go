package lower

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"unicode"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/pts/ir"
)

// Generic functions are monomorphized (D-049): each distinct list of IR types a generic
// function declaration is called with makes an instance, a function of its own whose
// signature and body are lowered with the declaration's type parameters replaced by the
// call's type arguments, in every checker type the lowering asks about.

// maxInstanceDepth bounds polymorphic recursion: how many instances of one generic function
// or class may make each other (made), which a function that calls itself with ever larger
// type arguments (f<T> calling f<T[]>) would do without end. Instances that the program's
// own calls make side by side don't count against it, however many there are (C172).
const maxInstanceDepth = 64

// maxRecursiveInstances bounds the instances of one generic function declaration that
// polymorphic recursion makes from one root (the outermost instance on their chain: one the
// program's code made, or a trial's for a typed call in code outside instances): those
// whose chain repeats a declaration (instanceChain), so that recursion that branches (f<T>
// calling f<[T]> and f<T[]>), or that calls other generic functions side by side at each
// level, stops too. Each root has its own budget; instances no recursion makes don't count.
const maxRecursiveInstances = 256

// maxRecursiveTotal bounds the instances of one generic function or class declaration that
// polymorphic recursion makes from all roots together, failed trials' included. A class's
// instances count only here (D-198).
const maxRecursiveTotal = 4 * maxRecursiveInstances

// recursionBudget is a root's budget for one declaration's instances (maxRecursiveInstances),
// or with no root, the declaration's total (maxRecursiveTotal).
type recursionBudget struct {
	root        *substitution
	declaration *ast.Node
}

// instanceLimit is which limit an instance would be past, if any (D-198).
type instanceLimit int

const (
	withinLimits instanceLimit = iota
	pastDepth                  // maxInstanceDepth
	pastCount                  // maxRecursiveInstances
	pastTotal                  // maxRecursiveTotal
)

// instanceLimitAt is the limit code here (l.subst) would be past by making another
// instance of a generic function or class (class) declaration, and whether recursion makes
// it (it then counts: countInstance).
func (l *lowerer) instanceLimitAt(declaration *ast.Node, class bool) (limit instanceLimit, recursive bool) {
	depth, root, recursive := instanceChain(l.subst, declaration)
	switch {
	case depth >= maxInstanceDepth:
		return pastDepth, recursive
	case !recursive:
		return withinLimits, false
	case l.instanceCounts[recursionBudget{nil, declaration}] >= maxRecursiveTotal:
		return pastTotal, true
	case !class && l.instanceCounts[recursionBudget{root, declaration}] >= maxRecursiveInstances:
		return pastCount, true
	}
	return withinLimits, true
}

// countInstance counts an instance of a generic function or class (class) declaration that
// recursion makes here against its declaration's total, for good, and a function's against
// its root's budget too, which taking back a trial that made it gives back (D-190, D-198).
// The total is kept: the class instances a failed trial made stay, and their bodies may
// start trials that make the same trees again.
func (l *lowerer) countInstance(declaration *ast.Node, class bool) {
	l.instanceCounts[recursionBudget{nil, declaration}]++
	if class {
		return
	}
	_, root, _ := instanceChain(l.subst, declaration)
	budget := recursionBudget{root, declaration}
	l.instanceCounts[budget]++
	if t := l.trial; t != nil {
		t.counted = append(t.counted, budget)
	}
}

// reportInstanceLimit reports at node that an instance of a generic function or class
// (named name) would be past limit. Past a declaration's total, which only a program's
// pathological recursion or many speculative pairings reach, the report stands in a trial,
// as the program's own, rather than leave a call the erased copy at its first level (D-198).
func (l *lowerer) reportInstanceLimit(limit instanceLimit, name string, node *ast.Node) {
	if t := l.trial; t != nil && limit == pastTotal {
		t.foreign = true
	}
	switch limit {
	case pastDepth:
		l.notSupportedYet(node, fmt.Sprintf("polymorphic recursion more than %d instances deep (`%s`) is", maxInstanceDepth, name))
	case pastCount:
		l.notSupportedYet(node, fmt.Sprintf("polymorphic recursion that makes more than %d instances (`%s`) from one call is", maxRecursiveInstances, name))
	default:
		l.notSupportedYet(node, fmt.Sprintf("polymorphic recursion that makes more than %d instances (`%s`) is", maxRecursiveTotal, name))
	}
}

// erasedResultCopies reports whether a call (node) of generic's erased copy would get its
// result as a copy of what the erased copy made: generic's result type mentions its own
// type parameters, so the erased copy's result is dynamic, and the call's type holds
// objects that converting a dynamic value copies (copiesFromDynamic), which the program
// could tell from the objects JS returns by their identity (D-198).
func (l *lowerer) erasedResultCopies(generic *genericFunc, node *ast.Node) bool {
	result := l.checker.GetReturnTypeOfSignature(l.checker.GetSignatureFromDeclaration(generic.node))
	anys := make([]*checker.Type, len(generic.parameters))
	for i := range anys {
		anys[i] = l.checker.GetAnyType()
	}
	if l.checker.PtsInstantiateType(result, generic.parameters, anys) == l.checker.PtsInstantiateType(result, generic.parameters, generic.parameters) {
		return false
	}
	return l.copiesFromDynamic(l.typeAt(node), map[*checker.Type]bool{})
}

// copiesFromDynamic reports whether converting a dynamic value to t may copy an object: t
// is or holds, as a member or as what a promise or an iterator gives, an object type
// other than a class's instances (an array, tuple, map, set, function or object literal's
// type, which conversions copy: D-071, D-078). A class's instance converts with a check, a
// primitive by its value.
func (l *lowerer) copiesFromDynamic(t *checker.Type, seen map[*checker.Type]bool) bool {
	if seen[t] {
		return false
	}
	seen[t] = true
	if t.Flags()&(checker.TypeFlagsUnion|checker.TypeFlagsIntersection) != 0 {
		return slices.ContainsFunc(t.Types(), func(member *checker.Type) bool { return l.copiesFromDynamic(member, seen) })
	}
	if t.Flags()&checker.TypeFlagsObject == 0 {
		return false
	}
	if l.isPromiseType(t) || l.isIteratorType(t) || l.isAsyncIteratorType(t) || l.isIterableType(t) {
		return slices.ContainsFunc(l.checker.GetTypeArguments(t), func(argument *checker.Type) bool { return l.copiesFromDynamic(argument, seen) })
	}
	_, class := l.classes[t.Symbol()]
	_, genericClass := l.genericClasses[t.Symbol()]
	return t.Symbol() == nil || !class && !genericClass
}

// genericFunc is a generic function or method declaration and its instances so far.
type genericFunc struct {
	node       *ast.Node
	name       string          // its qualified IR name, which an instance's extends
	module     *ir.Func        // the top-level code of its module
	parameters []*checker.Type // its type parameters
	instances  []*instance
	class      *ir.Class     // for a method, the class whose instances it runs on (`this`)
	static     *ir.Class     // for a static method, its class
	outer      *substitution // for a method of a generic class's instance, the instance's
	erased     *instance     // its erased copy, if made (D-078)
	inTable    bool          // for an instance method, its erased copy is in its class's method table
	dispatched bool          // abstract, or overridden: its calls find the receiver's class's method by name (D-101)
	jsName     string        // its instances' name in JS, if its node has none (a const's arrow function, D-102)
	// For a generic arrow function or function expression other than a module-scope
	// const's (D-190): the function of its erased copy, whose closures its instances run
	// with; what its names stood for when it was lowered, which its instances' bodies,
	// lowered later, see; and the const it initialises if every use of it calls it by
	// name, whose calls are the only demands its value meets.
	closure    *ir.Func
	scope      *scope
	calledOnly *ast.Symbol
	// For an object literal's generic method (D-191), as for such an arrow function: the
	// field whose closures are its erased copy; its instances take the literal first, as
	// `this` (class is the field's shape), and calls of methods of that name find them.
	field *ir.Field
}

// scope is what the names in a generic arrow function stand for where it is lowered: the
// locals of variables and parameters and the functions of nested function declarations
// (which another instance of an enclosing generic function declares again), and the class
// whose static initialisers it is in, if any.
type scope struct {
	locals map[*ast.Symbol]*ir.Local
	funcs  map[*ast.Symbol]*ir.Func
	static *ir.Class
}

// scopeOf is what the names node uses stand for now (D-190).
func (l *lowerer) scopeOf(node *ast.Node) *scope {
	s := &scope{locals: map[*ast.Symbol]*ir.Local{}, funcs: map[*ast.Symbol]*ir.Func{}, static: l.staticContext}
	note := func(symbol *ast.Symbol) {
		if local, ok := l.locals[symbol]; ok {
			s.locals[symbol] = local
		}
		if fn, ok := l.funcs[symbol]; ok {
			s.funcs[symbol] = fn
		}
	}
	note(l.declaredSymbol(node)) // a named function expression's own name
	var visit func(child *ast.Node) bool
	visit = func(child *ast.Node) bool {
		if child.Kind == ast.KindIdentifier {
			note(l.checker.GetSymbolAtLocation(child))
			note(l.readSymbol(child))
		}
		return child.ForEachChild(visit)
	}
	node.ForEachChild(visit)
	return s
}

// enterScope makes the names of s stand for what they did, and returns what undoes it.
func (l *lowerer) enterScope(s *scope) (leave func()) {
	locals, funcs := map[*ast.Symbol]*ir.Local{}, map[*ast.Symbol]*ir.Func{}
	for symbol, local := range s.locals {
		if outer, ok := l.locals[symbol]; ok {
			locals[symbol] = outer
		}
		l.locals[symbol] = local
	}
	for symbol, fn := range s.funcs {
		if outer, ok := l.funcs[symbol]; ok {
			funcs[symbol] = outer
		}
		l.funcs[symbol] = fn
	}
	static := l.staticContext
	l.staticContext = s.static
	return func() {
		for symbol := range s.locals {
			if outer, ok := locals[symbol]; ok {
				l.locals[symbol] = outer
			} else {
				delete(l.locals, symbol)
			}
		}
		for symbol := range s.funcs {
			if outer, ok := funcs[symbol]; ok {
				l.funcs[symbol] = outer
			} else {
				delete(l.funcs, symbol)
			}
		}
		l.staticContext = static
	}
}

// calledOnlyName is the const that node, a function-local const's generic arrow function
// or function expression, initialises if every use of it calls it by name (`make(1)`, not
// `use(make)` or `{ make }`), else nil (D-190).
func (l *lowerer) calledOnlyName(node *ast.Node) *ast.Symbol {
	declaration := node.Parent
	for declaration.Kind == ast.KindParenthesizedExpression {
		declaration = declaration.Parent
	}
	if declaration.Kind != ast.KindVariableDeclaration || declaration.Name().Kind != ast.KindIdentifier || declaration.Parent.Flags&ast.NodeFlagsConst == 0 {
		return nil
	}
	symbol := l.target(declaration.Name())
	container := ast.GetContainingFunction(declaration)
	if symbol == nil || container == nil || container.Body() == nil {
		return nil
	}
	if signatures := l.checker.GetSignaturesOfType(l.checker.GetTypeOfSymbol(symbol), checker.SignatureKindCall); len(signatures) != 1 || len(signatures[0].TypeParameters()) == 0 {
		return nil // `const t: (x: number) => Box<number> = <T>(x: T) => …`: an adapter's demand calls it (D-194)
	}
	calledOnly := true
	var visit func(child *ast.Node) bool
	visit = func(child *ast.Node) bool {
		if child.Kind == ast.KindIdentifier && child != declaration.Name() && l.readSymbol(child) == symbol {
			call := child.Parent
			for call.Kind == ast.KindParenthesizedExpression {
				call = call.Parent
			}
			calledOnly = call.Kind == ast.KindCallExpression && ast.SkipParentheses(call.Expression()) == child
		}
		return !calledOnly || child.ForEachChild(visit)
	}
	container.Body().ForEachChild(visit)
	if !calledOnly {
		return nil
	}
	return symbol
}

// instance is a generic function's instance for a list of type arguments.
type instance struct {
	generic   *genericFunc
	arguments []ir.Type
	literals  []*checker.Type // the type arguments that are literal types
	identity  string          // its type arguments' typeArgumentsIdentity, where its declaration computes types from them
	fn        *ir.Func        // nil if it failed, which was reported
	subst     *substitution
}

// substitution replaces type parameters by type arguments in the checker types of an
// instance's declaration. In code whose type parameters take dynamic values (an erased
// copy, D-078), a type that mentions one of the erased type parameters is any.
type substitution struct {
	declaration *ast.Node
	parameters  []*checker.Type
	arguments   []*checker.Type
	erased      []*checker.Type
	mentions    map[*checker.Type]bool // whether a type mentions an erased type parameter, as found so far
	instance    bool                   // a generic function's or class's instance, not code extended to erase (erasing) or an erased copy
	made        *substitution          // what was being lowered or declared where it was made: the instance that made it, if any
	// For an instance, a class instance or an erased copy, the code that made or found it
	// (use, here): used once it is made, ownUse once the program's own code has (C176).
	used   bool
	ownUse bool
	users  []use
	outer  *substitution // for a method of a generic class's instance, the class instance's, without which it isn't made
}

// instanceChain is, for an instance of declaration that the code subst is lowering (or
// declaring) makes, how many instances of declaration that code was made in, its own
// included (one less than the new instance's depth, maxInstanceDepth); the outermost
// instance on the chain, its root; and whether polymorphic recursion makes it: the chain
// with the new instance repeats a declaration (maxRecursiveInstances).
func instanceChain(subst *substitution, declaration *ast.Node) (depth int, root *substitution, recursive bool) {
	seen := map[*ast.Node]bool{declaration: true}
	for ; subst != nil; subst = subst.made {
		if !subst.instance {
			continue
		}
		if subst.declaration == declaration {
			depth++
		}
		recursive = recursive || seen[subst.declaration]
		seen[subst.declaration] = true
		root = subst
	}
	return depth, root, recursive
}

// instantiate is t in the instance being lowered, if any: with its type parameters
// replaced (in an intersection that narrows one, as narrowedInstance says), or any if it
// mentions an erased one. Readonly<X> of a class or library type
// X is X: Object.freeze's result is its argument, and only tsc reads the modifiers. A
// branded primitive is its primitive, also in a union (unbranded, C221), and an
// intersection of array types one of them or any (withoutArrayIntersection).
func (l *lowerer) instantiate(t *checker.Type) *checker.Type {
	t, erased := l.substituted(t)
	if erased {
		return t
	}
	return l.normalized(t)
}

// substituted is t with the type parameters of the instance being lowered, if any,
// replaced (in an intersection that narrows one, as narrowedInstance says), or any, and
// erased, if it mentions an erased one.
func (l *lowerer) substituted(t *checker.Type) (_ *checker.Type, erased bool) {
	switch {
	case l.subst == nil:
	case l.mentionsErased(t):
		return l.checker.GetAnyType(), true
	case len(l.subst.parameters) > 0:
		if narrowed := l.narrowedInstance(t); narrowed != nil {
			return narrowed, false
		}
		return l.checker.PtsInstantiateType(t, l.subst.parameters, l.subst.arguments), false
	}
	return t, false
}

// normalized is t, a type with no type parameters of the instance being lowered, as
// instantiate gives it.
func (l *lowerer) normalized(t *checker.Type) *checker.Type {
	if instance := l.readonlyInstance(t); instance != nil {
		return instance // Readonly<Point>, which Object.freeze gives, is a Point (D-157)
	}
	if primitive := l.unbranded(t); primitive != nil {
		return primitive
	}
	if t.Flags()&checker.TypeFlagsIntersection != 0 && l.uninhabited(t) {
		// `number & any[]`, `Sub1 & Sub2`: no value has it, so what a read of it holds is
		// what a stale narrowing left there, found at run time as a dynamic value's (C220).
		return l.checker.GetAnyType()
	}
	return l.withoutArrayIntersection(t)
}

// typeArgument is t, a type argument of an instance being made (in terms of the instance
// being lowered, if any), as instantiate gives it, but a branded primitive keeps its brand
// (`Id`, `number & { __brand: "id" }`): the instance's types may tell it from its
// primitive (`T extends Id ? string : number`), and instantiate makes it the primitive
// wherever an IR type is made of it (C177).
func (l *lowerer) typeArgument(t *checker.Type) *checker.Type {
	t, erased := l.substituted(t)
	if erased || l.unbranded(t) != nil {
		return t
	}
	return l.normalized(t)
}

// narrowedInstance is t, an intersection of one of the instance's type parameters with other
// types, which tsc narrows a value of the parameter's type to (`T & any[]` after
// Array.isArray(x) for an x of type T), for the instance's type arguments: each member of
// the parameter's type argument that the other types already include is itself (`number[]`
// or `readonly number[]` where tsc keeps `number[] & any[]`), and each that no value of is left drops out (`string &
// any[]` for a `string | number[]`; never if none is left, D-195). It is nil if t is no such
// intersection.
func (l *lowerer) narrowedInstance(t *checker.Type) *checker.Type {
	if t.Flags()&checker.TypeFlagsIntersection == 0 {
		return nil
	}
	index := -1
	var others []*checker.Type
	for _, member := range t.Types() {
		if i := slices.Index(l.subst.parameters, member); i >= 0 {
			if index >= 0 {
				return nil
			}
			index = i
			continue
		}
		others = append(others, l.checker.PtsInstantiateType(member, l.subst.parameters, l.subst.arguments))
	}
	if index < 0 {
		return nil
	}
	argument := l.subst.arguments[index]
	members := []*checker.Type{argument}
	if argument.Flags()&checker.TypeFlagsUnion != 0 {
		members = argument.Types()
	}
	var left []*checker.Type
	for _, member := range members {
		if !slices.ContainsFunc(others, func(other *checker.Type) bool { return !l.includes(other, member) }) {
			left = append(left, member)
			continue
		}
		arguments := slices.Clone(l.subst.arguments)
		arguments[index] = member
		if narrowed := l.checker.PtsInstantiateType(t, l.subst.parameters, arguments); !l.uninhabited(narrowed) {
			left = append(left, l.withoutArrayIntersection(narrowed))
		}
	}
	if len(left) == 0 && argument.Flags()&checker.TypeFlagsUnknown == 0 && l.isDynamicIRType(argument, l.subst.declaration) {
		// No value of the type argument is left (`Promise<number> & any[]` after
		// Array.isArray), but its values are dynamic ones, and code is dead only if it is
		// for unknown, the widest type of that IR type (deadTest, D-195): where unknown's
		// values pass (its `any[]`), the code reads the value as that narrows it, converted
		// with a check, not as never, whose members it couldn't read (`x.length`).
		arguments := slices.Clone(l.subst.arguments)
		arguments[index] = l.checker.GetUnknownType()
		if narrowed := l.checker.PtsInstantiateType(t, l.subst.parameters, arguments); !l.uninhabited(narrowed) {
			return l.withoutArrayIntersection(narrowed)
		}
	}
	return l.checker.GetUnionType(left)
}

// withoutArrayIntersection is t, or for an intersection of array types (`number[] &
// string[]`, which an honest `v is string[]` narrows a number[] to, in plain code and for
// a T of number[]), the member whose elements every other member's take (`(string |
// number)[] & string[]` is a string[], as tsc narrows it in plain code), or any where none
// does: no IR type holds both a number[] and a string[], which only an array of no elements
// is, so such a value is read dynamically (C227).
func (l *lowerer) withoutArrayIntersection(t *checker.Type) *checker.Type {
	if t.Flags()&checker.TypeFlagsIntersection == 0 || slices.ContainsFunc(t.Types(), func(member *checker.Type) bool {
		return !l.checker.IsArrayType(member) && !checker.IsTupleType(member)
	}) {
		return t
	}
	for _, member := range t.Types() {
		if l.checker.IsArrayType(member) && !slices.ContainsFunc(t.Types(), func(other *checker.Type) bool {
			return !l.checker.IsArrayType(other) || !l.checker.IsTypeAssignableTo(l.checker.GetElementTypeOfArrayType(member), l.checker.GetElementTypeOfArrayType(other))
		}) {
			return member
		}
	}
	return l.checker.GetAnyType()
}

// includes reports whether every value of member is one of t's, for narrowedInstance: it
// is assignable, or both are arrays whose elements are (a `readonly number[]` is an `any[]`
// to Array.isArray; a `number[]` isn't a `string[]`).
func (l *lowerer) includes(t, member *checker.Type) bool {
	return l.checker.IsTypeAssignableTo(member, t) || l.checker.IsArrayType(member) && l.checker.IsArrayType(t) &&
		l.checker.IsTypeAssignableTo(l.checker.GetElementTypeOfArrayType(member), l.checker.GetElementTypeOfArrayType(t))
}

// instantiateMade is the type of a value that code makes (an array or object literal, a
// `new` expression, a rest parameter's array), t, in the instance being lowered: in erased
// code, with the erased type parameters replaced by any, so that `[x]` for a T[] is an
// any[] (D-078).
func (l *lowerer) instantiateMade(t *checker.Type) *checker.Type {
	if l.subst == nil || !l.mentionsErased(t) {
		return l.instantiate(t)
	}
	parameters := append(slices.Clone(l.subst.parameters), l.subst.erased...)
	arguments := slices.Clone(l.subst.arguments)
	for range l.subst.erased {
		arguments = append(arguments, l.checker.GetAnyType())
	}
	return l.checker.PtsInstantiateType(t, parameters, arguments)
}

// mentionsErased reports whether t mentions a type parameter that the code being lowered
// erases. tsc interns instantiations, so t mentions one if replacing the erased type
// parameters by any gives another type than replacing them by themselves.
func (l *lowerer) mentionsErased(t *checker.Type) bool {
	subst := l.subst
	if subst == nil || len(subst.erased) == 0 {
		return false
	}
	if mentions, ok := subst.mentions[t]; ok {
		return mentions
	}
	anys := make([]*checker.Type, len(subst.erased))
	for i := range anys {
		anys[i] = l.checker.GetAnyType()
	}
	mentions := l.checker.PtsInstantiateType(t, subst.erased, anys) != l.checker.PtsInstantiateType(t, subst.erased, subst.erased)
	subst.mentions[t] = mentions
	return mentions
}

// erasing is the substitution of the code being lowered, extended to erase a generic
// signature's type parameters, in code whose declaration is declaration.
func (l *lowerer) erasing(parameters []*checker.Type, declaration *ast.Node) *substitution {
	subst := &substitution{declaration: declaration, erased: parameters, mentions: map[*checker.Type]bool{}, made: l.subst}
	if outer := l.subst; outer != nil {
		subst.parameters, subst.arguments = outer.parameters, outer.arguments
		subst.erased = append(slices.Clone(outer.erased), parameters...)
		if declaration == nil {
			subst.declaration = outer.declaration
		}
	}
	return subst
}

// typeAt, typeOfSymbol, contextualType and returnTypeOf are the checker's answers,
// instantiated for the instance being lowered.
func (l *lowerer) typeAt(node *ast.Node) *checker.Type {
	if t, ok := l.chainObjects[node]; ok {
		return t.checkerType // the object of a `?.` that was neither null nor undefined (D-064)
	}
	switch node.Kind {
	case ast.KindArrayLiteralExpression, ast.KindObjectLiteralExpression, ast.KindNewExpression:
		return l.instantiateMade(l.checker.GetTypeAtLocation(node))
	}
	if l.chainDepth > 0 && ast.IsOptionalChain(node) && !l.chainRests[node] {
		// A link inside the chain being lowered, whose object wasn't null or undefined: tsc
		// marks its type as possibly undefined for the chain's sake (`s?.split(",")[0]`).
		return l.instantiate(l.checker.GetNonOptionalType(l.checker.GetTypeAtLocation(node)))
	}
	t := l.instantiate(l.checker.GetTypeAtLocation(node))
	if instance := l.instanceofInstance(node, t); instance != nil {
		return instance // `x` after `x instanceof C`, as its declared type implies C's type arguments (H10)
	}
	return t
}

func (l *lowerer) typeOfSymbol(symbol *ast.Symbol) *checker.Type {
	return l.instantiate(l.checker.GetTypeOfSymbol(symbol))
}

func (l *lowerer) contextualType(node *ast.Node) *checker.Type {
	if isDestructuringSource(node) {
		return nil // the type tsc derives from the pattern (`{ x: any }`) says nothing about the value
	}
	t := l.checker.GetContextualType(node, checker.ContextFlagsNone)
	switch {
	case t == nil:
		return nil
	case node.Kind == ast.KindArrayLiteralExpression || node.Kind == ast.KindObjectLiteralExpression:
		return l.instantiateMade(t) // the type of the value it makes, as typeAt's
	}
	return l.instantiate(t)
}

func (l *lowerer) returnTypeOf(signature *checker.Signature) *checker.Type {
	return l.instantiate(l.checker.GetReturnTypeOfSignature(signature))
}

// declareGenericConsts records each module-scope `const` of a statement that holds a
// generic arrow function or anonymous function expression (`const make = <T>(x: T) =>
// new Box(x)`) as a generic function whose calls by the const's name are monomorphized,
// as a generic function declaration's are (D-102); the const itself holds the erased copy
// that other uses of it see (D-078).
func (l *lowerer) declareGenericConsts(statement *ast.Node) {
	list := statement.AsVariableStatement().DeclarationList
	if list.Flags&ast.NodeFlagsConst == 0 {
		return
	}
	for _, declaration := range list.AsVariableDeclarationList().Declarations.Nodes {
		initializer := declaration.Initializer()
		if declaration.Name().Kind != ast.KindIdentifier || initializer == nil {
			continue
		}
		fn := ast.SkipParentheses(initializer)
		if fn.Kind != ast.KindArrowFunction && (fn.Kind != ast.KindFunctionExpression || fn.Name() != nil) || fn.TypeParameterList() == nil {
			continue
		}
		generic := &genericFunc{node: fn, name: declaration.Name().Text(), module: l.function, jsName: declaration.Name().Text()}
		for _, parameter := range fn.TypeParameters() {
			generic.parameters = append(generic.parameters, l.checker.GetDeclaredTypeOfSymbol(parameter.Symbol()))
		}
		l.genericConsts[l.declaredSymbol(declaration)] = generic
		l.genericConstNodes[fn] = generic
	}
}

// makesGenericInstances reports whether a generic const's function makes instances of
// generic classes (`new Box(x)`), directly or by calling another such const: only then
// are its calls monomorphized (D-102), since its erased copy would make them for any.
// Others keep the erased copy, whose dynamic values follow JS where an instance's
// narrowed branches couldn't type-check (`typeof x === "number" ? x + 1 : …` for a
// string x).
func (l *lowerer) makesGenericInstances(generic *genericFunc, seen map[*genericFunc]bool) bool {
	if seen[generic] {
		return false
	}
	seen[generic] = true
	if l.returnsGenericObjects(generic) {
		return true
	}
	found := false
	var visit func(node *ast.Node) bool
	visit = func(node *ast.Node) bool {
		switch node.Kind {
		case ast.KindNewExpression:
			if symbol := l.typeAt(node).Symbol(); symbol != nil && l.genericClasses[symbol] != nil {
				found = true
			}
		case ast.KindCallExpression:
			if callee := ast.SkipParentheses(node.Expression()); callee.Kind == ast.KindIdentifier {
				if other, ok := l.genericConsts[l.target(callee)]; ok && l.makesGenericInstances(other, seen) {
					found = true
				}
			}
		}
		return found || node.ForEachChild(visit)
	}
	visit(generic.node.Body())
	return found
}

// returnsGenericObjects reports whether a generic function's result may hold objects of a
// type that mentions its type parameters other than an array's (`List<T>`, `Parser<T>`,
// `Map<K, V>`, `Box<T>[]`): its erased copy would make them for any (`List<any>`), which
// don't convert to an instance's type, as instances of generic classes don't (D-102).
func (l *lowerer) returnsGenericObjects(generic *genericFunc) bool {
	return l.holdsGenericObjects(l.checker.GetReturnTypeOfSignature(l.checker.GetSignatureFromDeclaration(generic.node)), generic.parameters)
}

// isGeneric reports whether a declaration has type parameters of its own, other than as
// the instance being lowered.
func (l *lowerer) isGeneric(declaration *ast.Node) bool {
	return declaration.TypeParameterList() != nil && (l.subst == nil || l.subst.declaration != declaration)
}

// declareGeneric records a generic function declaration (named qualified) at module
// scope, whose instances are made as calls need them. Nested ones are reported.
func (l *lowerer) declareGeneric(node *ast.Node, qualified string) {
	if l.subst != nil {
		l.notSupportedYet(nameOf(node), "generic functions inside generic functions or classes are")
		l.generics[l.declaredSymbol(node)] = nil
		return
	}
	generic := &genericFunc{node: node, name: qualified, module: l.function}
	for _, parameter := range node.TypeParameters() {
		generic.parameters = append(generic.parameters, l.checker.GetDeclaredTypeOfSymbol(parameter.Symbol()))
	}
	l.generics[l.declaredSymbol(node)] = generic
}

// genericCall lowers a call of a generic function or static method: a call of its
// instance for the type arguments tsc inferred or the call gives.
func (l *lowerer) genericCall(call *ast.CallExpression, generic *genericFunc) ir.Expr {
	fn := l.calledInstance(call, generic)
	if fn == nil {
		return nil
	}
	return l.fromErasedCall(l.userCall(call, fn), fn, generic, call)
}

// genericMethodCall lowers `o.m(args)` for a generic method m of a class: a direct call
// of m's instance for the call's type arguments, on o.
func (l *lowerer) genericMethodCall(call *ast.CallExpression, generic *genericFunc) ir.Expr {
	if receiverNode := call.Expression.Expression(); generic.dispatched && receiverNode.Kind != ast.KindSuperKeyword {
		return l.dispatchedGenericCall(call, generic, receiverNode)
	}
	fn := l.calledInstance(call, generic)
	if fn == nil {
		return nil
	}
	receiverNode := call.Expression.Expression()
	var receiver ir.Expr
	if receiverNode.Kind == ast.KindSuperKeyword {
		receiver = &ir.LocalRef{Local: l.thisLocal()}
	} else if receiver = l.expression(receiverNode); receiver != nil {
		receiver = l.asInstanceOf(receiver, generic.class, receiverNode)
	}
	args := l.arguments(call.Arguments.Nodes, fn.Params[1:])
	if receiver == nil || args == nil {
		return nil
	}
	return l.fromErasedCall(&ir.Call{Func: fn, Args: append([]ir.Expr{upcast(receiver, generic.class)}, args...)}, fn, generic, call)
}

// calledInstance is the instance of a generic function or method that a call calls,
// for the type arguments of tsc's resolved signature, or its erased copy if they mention
// type parameters whose values are dynamic or the instance would be polymorphic
// recursion past the depth limit; nil if it failed (reported).
func (l *lowerer) calledInstance(call *ast.CallExpression, generic *genericFunc) *ir.Func {
	resolved := l.checker.GetResolvedSignature(call.AsNode())
	arguments := l.checker.PtsTypeArgumentsOfSignature(resolved)
	if declaration := resolved.Declaration(); declaration != nil && declaration != generic.node && isOverloadSignature(declaration) {
		// An overload's type arguments aren't the implementation's (it may have other type
		// parameters, or the same in another order): infer those from the overload's
		// instantiated signature, as for a generic function in a context (D-078).
		implementation := l.checker.GetSignatureFromDeclaration(generic.node)
		arguments = l.checker.PtsTypeArgumentsOfSignature(l.checker.PtsInstantiateSignatureInContextOf(implementation, resolved))
	}
	if len(arguments) != len(generic.parameters) {
		l.notSupportedYet(call.AsNode(), "this call of a generic function is")
		return nil
	}
	if slices.ContainsFunc(arguments, l.mentionsErased) {
		return l.erasedInstance(generic, call.AsNode())
	}
	if fromResult := l.resultTypeArguments(call, generic, arguments); fromResult != nil {
		arguments = fromResult
	}
	fn, limit := l.genericInstanceFor(generic, arguments, call.AsNode())
	if (limit == pastDepth || limit == pastCount) && !l.erasedResultCopies(generic, call.AsNode()) {
		// Polymorphic recursion: the call maxInstanceDepth instances deep, or past its root's
		// budget, calls the erased copy, its result checked (fromErasedCall), which only a
		// call that runs that deep or that wide reaches (D-198). Past the declaration's total
		// a root would have none of its own: that is an error, rather than a call that
		// throws at its first level.
		return l.erasedInstance(generic, call.AsNode())
	}
	if limit != withinLimits {
		l.reportInstanceLimit(limit, generic.name, call.AsNode())
	}
	return fn
}

// fromErasedCall converts x, a call of fn that genericCall or genericMethodCall made, to
// the type tsc gives the call if fn is generic's erased copy with a dynamic result (D-078):
// past the instance limit (calledInstance), and in erased code, where that type is any.
func (l *lowerer) fromErasedCall(x ir.Expr, fn *ir.Func, generic *genericFunc, call *ast.CallExpression) ir.Expr {
	if x == nil || generic.erased == nil || generic.erased.fn != fn || fn.Result != ir.Value {
		return x
	}
	return l.fromErased(x, call.AsNode())
}

// objectUnionContext is the contextual type of an expression whose value goes where a
// union with object types among its members goes (a `Result<T, E>`), or nil. Where tsc
// types the expression as a narrower union of object types of its own (the literals a
// conditional or a function returns, with each other's properties added as optional), or
// a generic call's result as an instance of other type arguments, the value is made for
// that union instead (D-095).
func (l *lowerer) objectUnionContext(node *ast.Node) *checker.Type {
	return l.objectUnion(l.contextualType(node))
}

// objectUnion is t if it is a union of object types (iterator results among them),
// perhaps with null or undefined, else nil (`U | readonly U[]`, a flatMap callback's
// result, is none). Promises in it are left out: an async function's return takes
// `T | PromiseLike<T>` (D-115).
func (l *lowerer) objectUnion(t *checker.Type) *checker.Type {
	isObject := func(member *checker.Type) bool {
		_, isIteratorResult := l.iteratorResultValue(member)
		return isIteratorResult || l.isObjectType(member)
	}
	if t == nil || t.Flags()&checker.TypeFlagsUnion == 0 || !slices.ContainsFunc(t.Types(), isObject) {
		return nil
	}
	var kept []*checker.Type
	for _, member := range t.Types() {
		switch {
		case l.isPromiseType(member):
			continue
		case !isObject(member) && member.Flags()&(checker.TypeFlagsNull|checker.TypeFlagsUndefined) == 0:
			return nil
		}
		kept = append(kept, member)
	}
	if len(kept) < len(t.Types()) {
		return l.checker.GetUnionType(kept)
	}
	return t
}

// contextualResult is the result type of the function type that fn, an arrow function or
// function expression without a declared result type, goes where (`(input) => …` as a
// `Parser<string>`), if that is an objectUnion that inferred, the result tsc inferred for
// fn, is assignable to; else nil (D-095).
func (l *lowerer) contextualResult(fn *ast.Node, inferred *checker.Type) *checker.Type {
	if fn.Type() != nil || fn.Kind != ast.KindArrowFunction && fn.Kind != ast.KindFunctionExpression {
		return nil
	}
	contextual := l.contextualType(fn)
	if contextual == nil {
		return nil
	}
	signatures := l.checker.GetSignaturesOfType(l.checker.GetNonNullableType(contextual), checker.SignatureKindCall)
	if len(signatures) != 1 || len(signatures[0].TypeParameters()) > 0 {
		return nil
	}
	result := l.objectUnion(l.returnTypeOf(signatures[0]))
	if result == nil || !l.checker.IsTypeAssignableTo(inferred, result) {
		return nil
	}
	return result
}

// resultTypeArguments are the type arguments of a generic call inferred from where its
// result goes, where that is a union with object types among its members and the call
// type-checks with them, or nil (D-095). For `return err({ kind: "empty" })` where a
// `Result<number, ParseError>` goes, tsc infers `{ kind: "empty" }` from the argument,
// whose instance makes an `Err` of another representation than the union's member
// `Err<ParseError>`, which the result's instance makes; JS's values are the same.
func (l *lowerer) resultTypeArguments(call *ast.CallExpression, generic *genericFunc, arguments []*checker.Type) []*checker.Type {
	contextual := l.objectUnionContext(call.AsNode())
	if contextual == nil {
		return nil
	}
	signature := l.checker.GetSignatureFromDeclaration(generic.node)
	inferred := l.checker.PtsInferTypeArgumentsFromResult(signature, contextual)
	if inferred == nil || slices.Equal(inferred, arguments) || slices.ContainsFunc(inferred, l.mentionsErased) {
		return nil
	}
	instantiated := func(t *checker.Type) *checker.Type {
		return l.checker.PtsInstantiateType(t, generic.parameters, inferred)
	}
	if !l.checker.IsTypeAssignableTo(instantiated(l.checker.GetReturnTypeOfSignature(signature)), contextual) {
		return nil
	}
	params := signature.Parameters()
	for i, argument := range call.Arguments.Nodes {
		if signature.HasRestParameter() && i >= len(params)-1 {
			// An argument for the rest parameter (C170): of its element type, or a spread of its type.
			rest := instantiated(l.typeOfSymbol(params[len(params)-1]))
			if !l.checker.IsArrayType(rest) {
				return nil // a tuple's elements: as before
			}
			if argument.Kind == ast.KindSpreadElement {
				if !l.checker.IsTypeAssignableTo(l.typeAt(argument.Expression()), rest) {
					return nil
				}
			} else if !l.checker.IsTypeAssignableTo(l.typeAt(argument), l.checker.GetElementTypeOfArrayType(rest)) {
				return nil
			}
			continue
		}
		if i >= len(params) || !l.checker.IsTypeAssignableTo(l.typeAt(argument), instantiated(l.typeOfSymbol(params[i]))) {
			return nil
		}
	}
	return inferred
}

// genericValue lowers a generic function declaration used as a value (node names it): in
// a context of a function type that isn't generic, the instance for the type arguments
// tsc infers from it, as it does when it relates the two (`[1, 2].map(identity)`);
// anywhere else, the erased copy (D-078).
func (l *lowerer) genericValue(node *ast.Node, generic *genericFunc) ir.Expr {
	fn := l.valueInstance(node, generic, l.checker.GetTypeOfSymbol(l.declaredSymbol(generic.node)))
	if fn == nil {
		return nil
	}
	l.addErasedValueOf(fn, generic)
	return l.functionValue(node, fn)
}

// addErasedValueOf records fn as an erased value (D-148) if it is generic's erased copy.
func (l *lowerer) addErasedValueOf(fn *ir.Func, generic *genericFunc) {
	if generic.erased != nil && generic.erased.fn == fn {
		l.addErasedValue(fn, generic)
	}
}

// valueInstance is the function a generic function or method of type t (node names it)
// is as a value: the instance its context implies, or else its erased copy; nil if it
// failed (reported).
func (l *lowerer) valueInstance(node *ast.Node, generic *genericFunc, t *checker.Type) *ir.Func {
	if arguments := l.contextualTypeArguments(node, generic, t); arguments != nil {
		return l.genericInstance(generic, arguments, node)
	}
	return l.erasedInstance(generic, node)
}

// genericMethodValue lowers `o.m` or `C.m` for a generic method m used as a value: as
// genericValue does for functions, the instance the context implies or the erased copy,
// bound to o for an instance method as other methods are (D-044). An abstract or overridden
// method's value calls the method o's class has (D-101), its erased copy found by name. A
// value of the erased copy forwards its typed calls to the instance of o's class's method
// for their signatures (D-194).
func (l *lowerer) genericMethodValue(node *ast.Node, generic *genericFunc) ir.Expr {
	if generic.static != nil {
		fn := l.valueInstance(node, generic, l.checker.GetTypeAtLocation(node))
		if fn == nil {
			return nil
		}
		l.addErasedValueOf(fn, generic)
		return l.functionValue(node, fn)
	}
	receiver := l.expression(node.Expression())
	if receiver == nil {
		return nil
	}
	return l.boundGenericMethod(node, generic, l.asInstanceOf(receiver, generic.class, node.Expression()))
}

// boundGenericMethod is a generic instance method's value bound to receiver, an instance of
// its class (genericMethodValue): node reads it (`o.m`, or a pattern's `{ m }`).
func (l *lowerer) boundGenericMethod(node *ast.Node, generic *genericFunc, receiver ir.Expr) ir.Expr {
	var source *ast.Node
	if node.Kind == ast.KindPropertyAccessExpression {
		source = node.Expression() // a pattern's name is not its captured receiver (D-207)
	}
	m, forward, ok := l.genericMethodOf(node, generic, source)
	if !ok || receiver == nil {
		return nil
	}
	return l.boundMethod(node, m, upcast(receiver, generic.class), nil, m.name, false, forward)
}

// genericMethodOf is a generic instance method read at node (`o.m`, a pattern's `{ m }`) as
// a method of its class's instances (D-044): its instance the context implies or its erased
// copy, or for an abstract or overridden one, the erased copy that the receiver's class has,
// found by name (D-101); with what a value of the erased copy forwards its typed calls to,
// the instances of the receiver's class's method for their signatures (D-194), or nil. ok
// is false if it failed (reported). source is the actual receiver expression, or nil for
// a synthetic/pattern read; an explicit bind supplies its thisArg instead (D-207).
func (l *lowerer) genericMethodOf(node *ast.Node, generic *genericFunc, source *ast.Node) (m method, forward func(*ir.Local) *forwarder, ok bool) {
	m.name, m.receiver = generic.node.Name().Text(), generic.class
	erased := generic.dispatched
	if generic.dispatched {
		// Not the method tsc names, which another class's may override, nor an abstract one's
		// erased copy, which has no body: the receiver's class's, found by name (D-101).
		method := l.erasedMethodSignature(generic)
		if method == nil {
			return m, nil, false
		}
		m.params, m.result = method.Params, method.Result
		m.call = func(object ir.Expr, args []ir.Expr) ir.Expr {
			return &ir.InterfaceCall{Method: method, Args: append([]ir.Expr{&ir.ClassCast{X: object, To: &ir.Interface{}}}, args...)}
		}
	} else {
		fn := l.valueInstance(node, generic, l.checker.GetTypeAtLocation(node))
		if fn == nil {
			return m, nil, false
		}
		m.params = make([]ir.Type, len(fn.Params)-1)
		for i, param := range fn.Params[1:] {
			m.params[i] = param.Type
		}
		m.result, erased = fn.Result, generic.erased != nil && generic.erased.fn == fn
		m.call = func(object ir.Expr, args []ir.Expr) ir.Expr {
			return &ir.Call{Func: fn, Args: append([]ir.Expr{object}, args...)}
		}
	}
	if erased && m.result == ir.Value && l.genericSignature(node) != nil {
		forward = func(receiver *ir.Local) *forwarder {
			return &forwarder{call: l.methodForward(receiver, m, generic, nil, nil, source)}
		}
	}
	return m, forward, true
}

// instantiationExpression lowers `f<T>`, a generic function given type arguments: the
// instance for them of a generic function declaration f, or else f's value as a value of
// the instantiated function type.
func (l *lowerer) instantiationExpression(node *ast.Node) ir.Expr {
	expression := ast.SkipParentheses(node.Expression())
	if generic := l.generics[l.target(expression)]; generic != nil && expression.Kind == ast.KindIdentifier {
		signatures := l.checker.GetSignaturesOfType(l.checker.GetTypeAtLocation(node), checker.SignatureKindCall)
		if len(signatures) == 1 {
			if arguments := l.checker.PtsTypeArgumentsOfSignature(signatures[0]); len(arguments) == len(generic.parameters) {
				var fn *ir.Func
				if slices.ContainsFunc(arguments, l.mentionsErased) {
					fn = l.erasedInstance(generic, node)
				} else {
					fn = l.genericInstance(generic, arguments, node)
				}
				if fn == nil {
					return nil
				}
				return l.functionValue(expression, fn)
			}
		}
	}
	value := l.expression(node.Expression())
	target := l.irType(l.typeAt(node), node)
	if value == nil || target == nil {
		return nil
	}
	return l.convert(value, target, node)
}

// contextualTypeArguments are the type arguments of a generic function or method of type
// t (node names it) that its context's function type implies, or nil if the context isn't
// a function type with one call signature that isn't generic, or they mention erased type
// parameters.
func (l *lowerer) contextualTypeArguments(node *ast.Node, generic *genericFunc, t *checker.Type) []*checker.Type {
	contextual := l.checker.GetContextualType(node, checker.ContextFlagsNone)
	if contextual == nil {
		return nil
	}
	contextualSignatures := l.checker.GetSignaturesOfType(l.checker.GetNonNullableType(contextual), checker.SignatureKindCall)
	signatures := l.checker.GetSignaturesOfType(t, checker.SignatureKindCall)
	if len(contextualSignatures) != 1 || len(contextualSignatures[0].TypeParameters()) > 0 || len(signatures) != 1 {
		return nil
	}
	instantiated := l.checker.PtsInstantiateSignatureInContextOf(signatures[0], contextualSignatures[0])
	arguments := l.checker.PtsTypeArgumentsOfSignature(instantiated)
	if len(arguments) != len(generic.parameters) || slices.ContainsFunc(arguments, l.mentionsErased) {
		return nil
	}
	return arguments
}

// erasedInstance returns the function of a generic function's erased copy, whose type
// parameters take dynamic values (D-078), making it if it is new; nil if it failed
// (reported at node).
func (l *lowerer) erasedInstance(generic *genericFunc, node *ast.Node) *ir.Func {
	if inst := generic.erased; inst != nil {
		l.use(inst.subst)
		return inst.fn
	}
	subst := &substitution{declaration: generic.node, erased: generic.parameters, mentions: map[*checker.Type]bool{}, made: generic.outer, outer: generic.outer}
	if outer := generic.outer; outer != nil {
		subst.parameters, subst.arguments = outer.parameters, outer.arguments
	}
	inst := &instance{generic: generic, subst: subst}
	generic.erased = inst
	l.use(subst)
	l.declareInstance(inst, generic.name+"_dynamic")
	return inst.fn
}

// genericMethodAt returns the generic method a callee (`o.m` or `C.m`) names, or nil.
func (l *lowerer) genericMethodAt(callee *ast.Node) *genericFunc {
	if callee.Kind != ast.KindPropertyAccessExpression {
		return nil
	}
	member := l.memberDeclaration(callee)
	if generic := l.membersAt(callee).genericMethods[member]; generic != nil {
		return generic
	}
	return l.genericStatics[member]
}

// declareGenericMethod records a generic method (or a static one) of class, whose
// instances calls make. One that overrides or is abstract is reported.
func (l *lowerer) declareGenericMethod(member *ast.Node, class *ir.Class) {
	name := member.Name().Text()
	abstract := member.Body() == nil
	switch {
	case member.Kind != ast.KindMethodDeclaration:
		l.notSupportedYet(nameOf(member), "generic accessors are")
		return
	case !ast.IsStatic(member) && class.Parent != nil && class.Parent.Method(name, ir.Plain) != nil && l.inheritedGenericMethod(class.Parent, name) == nil:
		// (A generic method's erased copy in a method table is a plain method.)
		l.notSupportedYet(nameOf(member), "overriding a method with a generic method is")
		return
	}
	generic := &genericFunc{node: member, name: class.Name + "." + name, module: class.Module.Init, class: class, outer: l.subst, dispatched: abstract}
	if overridden := l.inheritedGenericMethod(class.Parent, name); !ast.IsStatic(member) && class.Parent != nil && overridden != nil {
		overridden.dispatched = true
	}
	if abstract || generic.dispatched || !ast.IsStatic(member) && class.Parent != nil && l.inheritedGenericMethod(class.Parent, name) != nil {
		// Calls of an abstract or overridden generic method find the method of the
		// receiver's class by name, through erased copies in method tables (D-101).
		l.wantInMethodTables(name)
	}
	for _, parameter := range member.TypeParameters() {
		generic.parameters = append(generic.parameters, l.checker.GetDeclaredTypeOfSymbol(parameter.Symbol()))
	}
	if abstract {
		l.declaringMembers().genericMethods[member] = generic // no instances: its calls are dispatched
		return
	}
	if ast.IsStatic(member) {
		generic.class, generic.static = nil, class
		l.genericStatics[member] = generic
		return
	}
	l.declaringMembers().genericMethods[member] = generic
	l.genericMethodList = append(l.genericMethodList, generic)
	if l.tableMethodNames[name] || l.allMethodsInTables {
		l.addErasedMethod(generic)
	}
}

// dispatchedGenericCall lowers `o.m(…)` for an abstract or overridden generic method m
// (D-101): a call of the method named m in the method table of o's class, the erased copy
// that class's m has there, as an interface site calls a generic method (D-079); its
// result converts to the call's type with a check.
func (l *lowerer) dispatchedGenericCall(call *ast.CallExpression, generic *genericFunc, receiverNode *ast.Node) ir.Expr {
	method := l.erasedMethodSignature(generic)
	receiver := l.expression(receiverNode)
	if method == nil || receiver == nil {
		return nil
	}
	if x, ok := l.dispatchedInstanceCall(call, generic, receiver, method); ok {
		return x // the instance of the receiver's class's method, if it lists one (D-148)
	}
	args := l.typedArguments(call.Arguments.Nodes, method.Params)
	if args == nil {
		return nil
	}
	object := &ir.ClassCast{X: receiver, To: &ir.Interface{}} // any object, whose method is found by name
	result := ir.Expr(&ir.InterfaceCall{Method: method, Args: append([]ir.Expr{object}, args...)})
	if method.Result == ir.Value {
		return l.fromErased(result, call.AsNode())
	}
	return result
}

// erasedMethodSignature is the signature a generic method's erased copy has in its
// class's method table, as an interface method, or nil if it fails (reported).
func (l *lowerer) erasedMethodSignature(generic *genericFunc) *ir.InterfaceMethod {
	outer := l.subst
	l.subst = generic.outer
	l.subst = l.erasing(generic.parameters, generic.node)
	defer func() { l.subst = outer }()
	method := &ir.InterfaceMethod{Name: generic.node.Name().Text()}
	var ok bool
	if method.Params, ok = l.methodParamTypes(generic.node.Parameters()); !ok {
		return nil
	}
	if method.Result = l.resultType(generic.node); method.Result == nil {
		return nil
	}
	return method
}

// wantInMethodTables records that the program finds methods named name at run time, by
// an interface's generic method of that name or through `any`: each generic method of a
// class so named, declared so far or later, gets its erased copy in its class's method
// table (D-078), whose entry such calls find. Other generic methods stay out of the
// tables (D-051), so their bodies are never lowered erased unless something needs them.
func (l *lowerer) wantInMethodTables(name string) {
	if l.tableMethodNames[name] {
		return
	}
	l.tableMethodNames[name] = true
	for _, generic := range l.genericMethodList {
		if generic.node.Name().Text() == name {
			l.addErasedMethod(generic)
		}
	}
}

// addErasedMethod adds a generic method's erased copy to its class's method table.
func (l *lowerer) addErasedMethod(generic *genericFunc) {
	if generic.inTable {
		return
	}
	generic.inTable = true
	if fn := l.erasedInstance(generic, generic.node); fn != nil {
		l.addMethod(&ir.Method{Name: generic.node.Name().Text(), Kind: ir.Plain, Class: generic.class, Func: fn}, generic.node)
	}
}

// inheritedGenericMethod returns the generic method of the given name that class declares
// or inherits, or nil.
func (l *lowerer) inheritedGenericMethod(class *ir.Class, name string) *genericFunc {
	for ; class != nil; class = class.Parent {
		for member, generic := range l.membersOf(class).genericMethods {
			if generic.class == class && member.Name().Text() == name {
				return generic
			}
		}
	}
	return nil
}

// genericInstance returns the function of a generic function's instance for some type
// arguments (in terms of the instance being lowered, if any), making it if it is new; nil
// if it failed or would be past an instance limit (reported at node).
func (l *lowerer) genericInstance(generic *genericFunc, typeArguments []*checker.Type, node *ast.Node) *ir.Func {
	fn, limit := l.genericInstanceFor(generic, typeArguments, node)
	if limit != withinLimits {
		l.reportInstanceLimit(limit, generic.name, node)
	}
	return fn
}

// genericInstanceFor is genericInstance, or past an instance limit nil and which limit,
// unreported: the caller decides (D-198), and the call keeps the erased copy. Instances are
// told apart by their type arguments' IR types and literal types (`pick(o, "a")` and
// `pick(o, "b")` return different types, D-089), and where the declaration computes types
// from its type parameters (computesFromTypeParameters), by their checker types too
// (typeArgumentsIdentity): `Promise<number>` and `Promise<string>` are both dynamic values,
// but an instance's `Box<Awaited<T>>` is a Box<number> for one and a Box<string> for the
// other, as `T extends { kind: "a" } ? …` tells `{ kind: "a" }` from `{ kind: "b" }`, one
// shape, and `T extends Id ? …` a branded Id from its number (C177). Where it computes
// none, an instance's types are made of its type arguments' IR types alone.
func (l *lowerer) genericInstanceFor(generic *genericFunc, typeArguments []*checker.Type, node *ast.Node) (*ir.Func, instanceLimit) {
	if t := l.trial; t != nil {
		t.making++ // what it reports is the trial's (D-190)
		defer func() { t.making-- }()
	}
	arguments := make([]ir.Type, len(typeArguments))
	concrete := make([]*checker.Type, len(typeArguments))
	literals := make([]*checker.Type, len(typeArguments))
	for i, t := range typeArguments {
		concrete[i] = l.typeArgument(t)
		if arguments[i] = l.irType(concrete[i], node); arguments[i] == nil {
			return nil, withinLimits
		}
		if literal := l.normalized(concrete[i]); isLiteralType(literal) {
			literals[i] = literal
		}
	}
	computes := l.computesFromTypeParameters(generic)
	identity, identified := "", false
	for _, existing := range generic.instances {
		if !slices.Equal(existing.arguments, arguments) || !slices.Equal(existing.literals, literals) {
			continue
		}
		if computes && !slices.Equal(existing.subst.arguments[len(existing.subst.arguments)-len(concrete):], concrete) { // after an enclosing instance's
			if !identified {
				identity, identified = l.typeArgumentsIdentity(generic, concrete, node), true
			}
			if existing.identity != identity {
				continue
			}
		}
		if t := l.trial; t != nil && t.root == nil {
			t.root = existing.subst
		}
		l.use(existing.subst)
		return existing.fn, withinLimits
	}
	limit, recursive := l.instanceLimitAt(generic.node, false)
	if limit != withinLimits {
		return nil, limit
	}
	if computes && !identified {
		identity = l.typeArgumentsIdentity(generic, concrete, node)
	}
	if recursive {
		l.countInstance(generic.node, false)
	}
	subst := instanceSubstitution(generic, concrete)
	subst.made = l.subst
	inst := &instance{generic: generic, arguments: arguments, literals: literals, identity: identity, subst: subst}
	generic.instances = append(generic.instances, inst)
	if t := l.trial; t != nil && t.root == nil {
		t.root = subst
	}
	l.use(subst)
	l.declareInstance(inst, generic.name+"_"+typeArgumentsName(arguments))
	if t := l.trial; t != nil {
		t.instances = append(t.instances, inst)
		if inst.fn != nil {
			t.funcs[inst.fn] = true
		}
	}
	return inst.fn, withinLimits
}

// typeArgumentsIdentity is what tells apart instances for type arguments (concrete) of the
// same IR types and literal types where their declaration computes types from them: the
// keyParts of the types it computes (computedTypes) for those type arguments, each with
// its own budget, brands included, a literal type as its primitive and making no IR type
// (keyIdentity.typeArguments, widenLiterals), the same where those types have the same
// structure and IR types (`A["type"]` for 200 action types is a string for each), and
// for dynamic values the same checker types (`Box<Awaited<T>>` for Promise<number> and
// Promise<string>, C177). Past maxComputedTypes, the type arguments' own keyParts.
func (l *lowerer) typeArgumentsIdentity(generic *genericFunc, concrete []*checker.Type, node *ast.Node) string {
	computed := l.computedTypes(generic)
	types, widen := computed.types, true
	if computed.overflow {
		types, widen = concrete, false
	} else {
		outer := l.subst
		l.subst = instanceSubstitution(generic, concrete)
		defer func() { l.subst = outer }()
	}
	parts := make([]string, len(types))
	for i, t := range types {
		identity := &keyIdentity{typeWalk: typeWalk{budget: maxKeyParts}, brands: true, typeArguments: true, widenLiterals: widen}
		l.keyPart(identity, t, node)
		parts[i] = identity.String()
	}
	return strings.Join(parts, "|")
}

// instanceSubstitution is the substitution of generic's instance for type arguments
// (concrete), after its class instance's, if any.
func instanceSubstitution(generic *genericFunc, concrete []*checker.Type) *substitution {
	subst := &substitution{declaration: generic.node, parameters: generic.parameters, arguments: concrete, instance: true, outer: generic.outer}
	if outer := generic.outer; outer != nil {
		subst.parameters = append(slices.Clone(outer.parameters), subst.parameters...)
		subst.arguments = append(slices.Clone(outer.arguments), subst.arguments...)
	}
	return subst
}

// maxComputedTypes bounds the types computedTypes lists of one declaration.
const maxComputedTypes = 64

// declarationTypes are the types a generic function's declaration computes from its type
// parameters (computedTypes), or past maxComputedTypes, overflow.
type declarationTypes struct {
	types    []*checker.Type
	overflow bool
}

// computesFromTypeParameters reports whether generic's declaration computes types from its
// type parameters (computedTypes).
func (l *lowerer) computesFromTypeParameters(generic *genericFunc) bool {
	computed := l.computedTypes(generic)
	return len(computed.types) > 0 || computed.overflow
}

// computedTypes are the types generic's declaration computes from its type parameters
// (computesTypes, `Box<Awaited<T>>`, `T extends Id ? string : number`): in its signature,
// the types of the expressions and type nodes in its body, or the type arguments of the
// calls there, remembered. Where it computes none, its types are made of its type
// parameters by unions, references, arrays, object and function types, whose IR types are
// made of the type arguments' IR types; narrowings to intersections with them are an
// instance's as D-195 says, for every type argument of their IR types.
func (l *lowerer) computedTypes(generic *genericFunc) *declarationTypes {
	if computed, ok := l.computingDecls[generic.node]; ok {
		return computed
	}
	anys := make([]*checker.Type, len(generic.parameters))
	for i := range anys {
		anys[i] = l.checker.GetAnyType()
	}
	computed := &declarationTypes{}
	note := func(t *checker.Type) {
		switch {
		case t == nil || computed.overflow || slices.Contains(computed.types, t) || !l.computesTypes(t, map[*checker.Type]bool{}) || l.checker.PtsInstantiateType(t, generic.parameters, anys) == t:
		case len(computed.types) == maxComputedTypes:
			computed.types, computed.overflow = nil, true
		default:
			computed.types = append(computed.types, t)
		}
	}
	signature := l.checker.GetSignatureFromDeclaration(generic.node)
	for _, parameter := range signature.Parameters() {
		note(l.checker.GetTypeOfSymbol(parameter))
	}
	note(l.checker.GetReturnTypeOfSignature(signature))
	var visit func(node *ast.Node) bool
	visit = func(node *ast.Node) bool {
		switch {
		case ast.IsTypeNode(node):
			note(l.checker.GetTypeFromTypeNode(node))
		case ast.IsExpressionNode(node):
			note(l.checker.GetTypeAtLocation(node))
			if node.Kind == ast.KindCallExpression || node.Kind == ast.KindNewExpression {
				if resolved := l.checker.GetResolvedSignature(node); resolved != nil {
					for _, argument := range l.checker.PtsTypeArgumentsOfSignature(resolved) {
						note(argument)
					}
				}
			}
		}
		return computed.overflow || node.ForEachChild(visit)
	}
	generic.node.ForEachChild(visit)
	l.computingDecls[generic.node] = computed
	return computed
}

// isLiteralType reports whether t is a literal type or a union of them.
func isLiteralType(t *checker.Type) bool {
	if t.Flags()&checker.TypeFlagsUnion != 0 {
		return !slices.ContainsFunc(t.Types(), func(member *checker.Type) bool { return !isLiteralType(member) })
	}
	return t.Flags()&(checker.TypeFlagsStringLiteral|checker.TypeFlagsNumberLiteral|checker.TypeFlagsEnumLiteral) != 0
}

// declareInstance declares the function of a generic function's instance, named name,
// whose body is lowered with the others' later; its fn stays nil if it failed (reported).
func (l *lowerer) declareInstance(inst *instance, name string) {
	generic := inst.generic
	outerSubst, outerFunction, outerParams := l.subst, l.function, l.parameterLocals(generic.node)
	l.subst, l.function = inst.subst, generic.module
	inst.fn = l.declareFunction(generic.node, name, generic.class)
	l.subst, l.function = outerSubst, outerFunction
	maps.Copy(l.locals, outerParams) // the instance being lowered may be another of this function's
	if inst.fn != nil {
		inst.fn.JSName = "default" // `export default function <T>() {}`
		if name := generic.node.Name(); name != nil {
			inst.fn.JSName = name.Text()
		} else if generic.jsName != "" {
			inst.fn.JSName = generic.jsName
		}
		if generic.static != nil {
			l.staticClasses[inst.fn] = generic.static
		}
		if closure := generic.closure; closure != nil {
			// It runs with the erased copy's closure, the value its calls find it through:
			// its own name in its body is that closure (D-190).
			inst.fn.Closure, inst.fn.Outer, inst.fn.Shares, inst.fn.Self = true, closure.Outer, closure, closure.Self
			inst.fn.JSName = closure.JSName
			l.arrows[inst.fn] = l.arrows[closure]
		}
		l.instances = append(l.instances, inst)
	}
}

// instanceBody lowers an instance's body, and the bodies of the functions declared in it,
// whose declarations are the instance's own.
func (l *lowerer) instanceBody(inst *instance) {
	node, fn := inst.generic.node, inst.fn
	l.subst = inst.subst
	defer func() { l.subst = nil }()
	if scope := inst.generic.scope; scope != nil {
		defer l.enterScope(scope)() // the code around a generic arrow function, as it was (D-190)
	}
	l.forgetLocals(node) // another instance's
	l.bindParameters(node, fn)
	outerBodies, outerFunction := l.bodies, l.function
	l.bodies, l.function = nil, inst.generic.module
	if node.Body().Kind == ast.KindBlock {
		l.declareFunctions(node.Body(), fn.Name)
	}
	l.function = outerFunction
	l.inFunction(fn, func() {
		if node.Body().Kind != ast.KindBlock {
			fn.Body = l.functionExpressionBody(node) // an arrow function's expression (D-102)
			return
		}
		fn.Body = l.block(node.Body(), node.Body().Statements())
	})
	for len(l.bodies) > 0 {
		body := l.bodies[0]
		l.bodies = l.bodies[1:]
		l.functionBody(body)
	}
	l.bodies = outerBodies
}

// typeArgumentsName names an instance's type arguments for its function's name:
// "number", "string_array", "int_string".
func typeArgumentsName(arguments []ir.Type) string {
	names := make([]string, len(arguments))
	for i, t := range arguments {
		name := strings.ReplaceAll(t.String(), "[]", "_array")
		name = strings.Map(func(r rune) rune {
			if r < unicode.MaxASCII && (unicode.IsLetter(r) || unicode.IsDigit(r)) {
				return r
			}
			return '_'
		}, name)
		for strings.Contains(name, "__") {
			name = strings.ReplaceAll(name, "__", "_")
		}
		names[i] = strings.Trim(name, "_")
	}
	return strings.Join(names, "_")
}

// forgetLocals forgets the locals of the declarations under node, which belong to
// another instance of the generic function node is in.
func (l *lowerer) forgetLocals(node *ast.Node) {
	node.ForEachChild(func(child *ast.Node) bool {
		if ast.IsDeclaration(child) && child.Name() != nil && child.Name().Kind == ast.KindIdentifier {
			delete(l.locals, l.declaredSymbol(child))
		}
		l.forgetLocals(child)
		return false
	})
}

// parameterLocals are the locals a function's parameters have now, by symbol.
func (l *lowerer) parameterLocals(node *ast.Node) map[*ast.Symbol]*ir.Local {
	locals := map[*ast.Symbol]*ir.Local{}
	for _, param := range node.Parameters() {
		symbol := parameterSymbol(param)
		if local, ok := l.locals[symbol]; ok {
			locals[symbol] = local
		}
	}
	return locals
}

// bindParameters makes the locals of a function's parameters (after `this`, for a method
// or constructor) those of its declaration's parameters again: the declaration of another
// instance of a generic function or class may have bound them since.
func (l *lowerer) bindParameters(declaration *ast.Node, fn *ir.Func) {
	for i, local := range l.paramLocals[fn] {
		if local != nil {
			l.locals[parameterSymbol(declaration.Parameters()[i])] = local
		}
	}
}
