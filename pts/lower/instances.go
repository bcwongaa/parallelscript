package lower

import (
	"fmt"
	"slices"
	"strings"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/pts/ir"
)

// Typed calls find instances where values are erased copies (D-148). A generic function
// used as a value of a generic function type is its erased copy (D-078), and a dispatched
// generic method's calls go to the erased copies in method tables (D-101), so what they
// make for the erased type parameters is made for any (`new Box(x)` a `Box<any>`), which
// doesn't convert to the instance the call's type names (`Box<number>`). A call that knows
// its type arguments, of a generic function type or a dispatched method whose result may
// hold such objects, looks up the instance of its signature that its closure's descriptor
// (or its receiver's class's) lists, and calls it with typed arguments; with none, it
// calls the erased copy as before. The instances are made for every erased value and
// class method a call may reach, once the program's bodies are lowered (specialize).

// demand is a typed call that looks up an instance: of a value of a generic function type
// (whose erased signature is erased), of a dispatched generic method (method), or of a
// generic method named name through an interface or of an object literal's type (D-191).
type demand struct {
	in        *ir.Func    // the function the call is in
	callee    *ast.Symbol // what the call's callee names, if it is a name
	dropped   bool        // in code a failed trial dropped (D-190)
	erased    *ir.FuncType
	method    *genericFunc
	name      string             // a method's: a dispatched one's, or one that a class's or an object literal's generic method of that name implements
	shape     *ir.Class          // for a call of an object literal's method through the literal's type, its shape: only its literals' methods
	receiver  *checker.Type      // for a call by name (D-191), its object's type, which only classes and literals of types assignable to it may be
	signature *checker.Signature // the call's resolved signature
	subst     *substitution      // the instance being lowered where the call is, if any
	want      *ir.FuncType       // the instance's function type, after `this`: the resolved signature's
	key       string             // for a dynamic result, its type arguments' (ir.Instance.Key, instanceKey)
	chained   bool               // the outermost call of a `?.` chain, whose type is the chain's (C179)
	node      *ast.Node
}

// erasedValue is a function whose closures are a generic function's erased copy as a
// value, whose descriptor lists the generic function's instances that calls look up; or a
// wrapper's (forward), whose closures forward their calls, listing the instances that make
// those calls as demands (D-194).
type erasedValue struct {
	fn      *ir.Func
	generic *genericFunc
	forward *forwarder
	dropped bool // in code a failed trial dropped (D-190)
}

// addErasedValue records fn, a function whose closures are generic's erased copy as a
// value, if the erased copy makes generic class instances, which only an instance makes
// for the type arguments a call has. It reports whether fn is recorded.
func (l *lowerer) addErasedValue(fn *ir.Func, generic *genericFunc) bool {
	if slices.ContainsFunc(l.erasedValues, func(v *erasedValue) bool { return v.fn == fn }) {
		return true
	}
	if !l.makesGenericInstances(generic, map[*genericFunc]bool{}) {
		return false
	}
	l.erasedValues = append(l.erasedValues, &erasedValue{fn: fn, generic: generic})
	return true
}

// holdsGenericObjects reports whether values of type t may be objects of a type that
// mentions the type parameters, other than arrays (`Box<T>`, `[T, T]`, `Map<K, V>`), or
// arrays of them: an erased copy makes them for any (`Box<any>`), and they don't convert
// to an instance's types (D-078, D-102).
func (l *lowerer) holdsGenericObjects(t *checker.Type, parameters []*checker.Type) bool {
	members := []*checker.Type{t}
	if t.Flags()&checker.TypeFlagsUnion != 0 {
		members = t.Types()
	}
	anys := make([]*checker.Type, len(parameters))
	for i := range anys {
		anys[i] = l.checker.GetAnyType()
	}
	return slices.ContainsFunc(members, func(m *checker.Type) bool {
		switch {
		case m.Flags()&checker.TypeFlagsObject == 0:
			return false
		case l.checker.IsArrayType(m):
			return l.holdsGenericObjects(l.checker.GetElementTypeOfArrayType(m), parameters)
		}
		return l.checker.PtsInstantiateType(m, parameters, anys) != l.checker.PtsInstantiateType(m, parameters, parameters)
	})
}

// demandAt is the demand of a call of an erased copy taking erasedParams, if the call's
// type arguments don't mention erased type parameters and its result converts from the
// erased copy's to the type of the resolved signature's (without `?.`'s undefined); else
// nil. A dynamic result is one too: a promise or generator object, whose values an
// instance makes for the call's type arguments (D-190). A rest parameter is one of the
// instance's elements, which the call passes as a new array of its own (C170).
func (l *lowerer) demandAt(call *ast.CallExpression, erasedParams []ir.Type) *demand {
	resolved := l.checker.GetResolvedSignature(call.AsNode())
	// A `?.` call's signature returns the chain's undefined too (or inside the chain, its
	// optional marker), which no instance does: its demand is the one the call without `?.`
	// makes, and the chain adds the undefined (C179). The outermost call's type is the
	// chain's, so its erased copy's result converts to the instance's.
	chained := l.checker.PtsIsOuterCallChain(resolved)
	resolved = l.checker.PtsWithoutCallChain(resolved)
	// Calls with a rest argument make instances the base never made: not in the last
	// fallback, which makes what the base made (C170).
	if resolved.HasRestParameter() && l.specialization == DeclaredInstances || slices.ContainsFunc(l.checker.PtsTypeArgumentsOfSignature(resolved), l.mentionsErased) {
		return nil
	}
	want := l.signatureType(resolved, call.AsNode())
	if want == nil || want.Result == ir.Value && l.specialization == DeclaredInstances || !chained && want.Result != l.erasedTarget(call.AsNode()) || len(want.Params) != len(erasedParams) {
		return nil
	}
	for i, param := range want.Params {
		if erasedParams[i] != param && erasedParams[i] != ir.Value && !isErasedRest(erasedParams[i], param) {
			return nil
		}
	}
	d := &demand{in: l.function, signature: resolved, subst: l.subst, want: want, node: call.AsNode(), chained: chained}
	if want.Result == ir.Value {
		d.key = l.instanceKey(resolved, call.AsNode()) // its values are made for its type arguments (D-190)
	}
	return d
}

// isErasedRest reports whether erased, an erased copy's parameter, is a rest parameter
// (`...xs: T[]` erased, of dynamic values, or `...xs: Opt<T>[]`, of objects holding them)
// where param, the instance's, is one of other elements, which the call passes as a new
// array of its own (C170).
func isErasedRest(erased, param ir.Type) bool {
	_, ok := erased.(*ir.Rest)
	_, isRest := param.(*ir.Rest)
	return ok && isRest && erased != param
}

// fromErasedFor converts value, the result of a demand d's call of an erased copy, to the
// type of its instance's result: the call's type (fromErased), or for the outermost call of
// a `?.` chain, the result of the call without `?.`, which the chain makes undefined where
// its object is (C179).
func (l *lowerer) fromErasedFor(d *demand, value ir.Expr, node *ast.Node) ir.Expr {
	if !d.chained {
		return l.fromErased(value, node)
	}
	if d.want.Result == ir.Value {
		return value
	}
	return l.dynamicConversion(value, d.want.Result, node)
}

// instanceKey is the key (ir.Instance.Key) of the entries a call with a dynamic result
// finds, resolved: the same for calls through the same function type whose type
// arguments, parameters and result are the same types (keyPart), else another one. The
// value's instance depends on all of them: a type argument can reach it from outside the
// call's own (a type alias's, `Loader<U>`, or an enclosing instance's, `outer<U>`),
// through the result or a parameter whose IR type is or holds a dynamic value
// (`Promise<U>`). The key names the first such call's result type, for reading the IR.
func (l *lowerer) instanceKey(resolved *checker.Signature, node *ast.Node) string {
	identity := &keyIdentity{budget: maxKeyParts}
	fmt.Fprintf(identity, "%p", resolved.Declaration())
	for _, t := range l.checker.PtsTypeArgumentsOfSignature(resolved) {
		l.keyPart(identity, t, node)
	}
	identity.WriteString("|")
	for _, param := range resolved.Parameters() {
		l.keyPart(identity, l.typeOfSymbol(param), node)
	}
	identity.WriteString("|")
	l.keyPart(identity, l.checker.GetReturnTypeOfSignature(resolved), node)
	key, ok := l.instanceKeys[identity.String()]
	if !ok {
		key = fmt.Sprintf("%s#%d", l.checker.TypeToString(l.instantiate(l.checker.GetReturnTypeOfSignature(resolved))), len(l.instanceKeys)+1)
		l.instanceKeys[identity.String()] = key
	}
	return key
}

// maxKeyDepth and maxKeyParts bound keyPart's walk of a type: the levels it goes down
// (a generic type alias can make a new type at each), and the types it writes by their
// parts; past them, a type is written by its IR type.
const (
	maxKeyDepth = 6
	maxKeyParts = 256
)

// keyIdentity is the identity keyPart writes.
type keyIdentity struct {
	strings.Builder
	path    []*checker.Type   // the types being written, outermost first
	budget  int               // the types it may still write by their parts
	binders [][]*checker.Type // the type parameters of the signatures being written, outermost first
}

// keyPart writes t's identity for instanceKey and madeFor: the same for types of the
// same IR types and literals, but for dynamic values, which `Promise<number>` and `any`
// both are, and anything holding them, and never the same for other IR types. So it
// writes a literal type by itself, a union's or an intersection's members, a type
// reference's (`Promise<Box<T>>`, `Pair<K, V>`, an array) target and type arguments,
// without making a class instance for it, a class or a library interface by its IR type,
// another object type (a literal's, an interface's, a mapped type's, a function type's)
// by its IR type and its properties, index signatures and signatures, whose own type
// parameters by their places, and anything else by its IR type, or if that is a dynamic
// value or it has none (reported at node, then taken back), by its checker type. A type
// inside itself is written by how far out it is, and past maxKeyDepth or maxKeyParts a
// type by its IR type alone, so a dynamic value deeper than that isn't told apart.
func (l *lowerer) keyPart(identity *keyIdentity, t *checker.Type, node *ast.Node) {
	t = l.instantiate(t)
	if i := slices.Index(identity.path, t); i >= 0 {
		fmt.Fprintf(identity, ",^%d", len(identity.path)-i)
		return
	}
	for depth := len(identity.binders) - 1; depth >= 0; depth-- {
		if i := slices.Index(identity.binders[depth], t); i >= 0 {
			fmt.Fprintf(identity, ",$%d.%d", len(identity.binders)-depth, i) // the same for each call site's `<X>(x: X) => X`
			return
		}
	}
	if len(identity.path) > maxKeyDepth || identity.budget == 0 {
		l.irTypeKeyPart(identity, t, node) // the same for fresh literals of one shape
		return
	}
	identity.budget--
	identity.path = append(identity.path, t)
	defer func() { identity.path = identity.path[:len(identity.path)-1] }()
	objectFlags := checker.ObjectFlags(0)
	if t.Flags()&checker.TypeFlagsObject != 0 {
		objectFlags = t.ObjectFlags()
	}
	switch {
	case isLiteralType(t):
		fmt.Fprintf(identity, ",=%d", t.Id())
	case t.Flags()&(checker.TypeFlagsUnion|checker.TypeFlagsIntersection) != 0:
		fmt.Fprintf(identity, ",(%d", t.Flags()&(checker.TypeFlagsUnion|checker.TypeFlagsIntersection))
		for _, member := range t.Types() {
			l.keyPart(identity, member, node)
		}
		identity.WriteString(")")
	case objectFlags&checker.ObjectFlagsReference != 0 && objectFlags&checker.ObjectFlagsAnonymous == 0:
		fmt.Fprintf(identity, ",r%d<", t.Target().Id())
		for _, argument := range l.checker.GetTypeArguments(t) {
			l.keyPart(identity, argument, node)
		}
		identity.WriteString(">")
	case objectFlags != 0 && !l.isNominal(t):
		l.objectKeyParts(identity, t, node)
	default:
		l.irTypeKeyPart(identity, t, node)
	}
}

// isNominal reports whether t, an object type, is a class's or a library interface's,
// whose IR type is its own.
func (l *lowerer) isNominal(t *checker.Type) bool {
	if t.ObjectFlags()&checker.ObjectFlagsClassOrInterface == 0 || t.Symbol() == nil {
		return false
	}
	_, class := l.classes[t.Symbol()]
	return class || l.isLibInterface(t.Symbol())
}

// objectKeyParts writes an object type's IR type, properties, index signatures and call
// and construct signatures for keyPart: the IR type tells apart what the members don't
// (a method and a function-valued property; the prelude's Timeout, a dynamic value, and
// a structural twin, a shape).
func (l *lowerer) objectKeyParts(identity *keyIdentity, t *checker.Type, node *ast.Node) {
	l.irTypeKeyPart(identity, t, node)
	identity.WriteString(",{")
	for _, property := range l.checker.GetPropertiesOfType(t) {
		identity.WriteString(property.Name)
		if property.Flags&ast.SymbolFlagsOptional != 0 {
			identity.WriteString("?")
		}
		l.keyPart(identity, l.checker.GetTypeOfSymbol(property), node)
	}
	for _, info := range l.checker.GetIndexInfosOfType(t) {
		identity.WriteString(",[")
		l.keyPart(identity, info.KeyType(), node)
		l.keyPart(identity, info.ValueType(), node)
		identity.WriteString("]")
	}
	for i, kind := range []checker.SignatureKind{checker.SignatureKindCall, checker.SignatureKindConstruct} {
		for _, signature := range l.checker.GetSignaturesOfType(t, kind) {
			fmt.Fprintf(identity, ",%d<%d>(", i, len(signature.TypeParameters()))
			identity.binders = append(identity.binders, signature.TypeParameters())
			for _, parameter := range signature.Parameters() {
				l.keyPart(identity, l.checker.GetTypeOfSymbol(parameter), node)
			}
			identity.WriteString(")")
			l.keyPart(identity, l.checker.GetReturnTypeOfSignature(signature), node)
			identity.binders = identity.binders[:len(identity.binders)-1]
		}
	}
	identity.WriteString("}")
}

// irTypeKeyPart writes t by its IR type for keyPart, or by its checker type if that is a
// dynamic value or it has none (the report taken back, with the trial's mark).
func (l *lowerer) irTypeKeyPart(identity *keyIdentity, t *checker.Type, node *ast.Node) {
	errors, reports, unexplained := l.errors, l.reports, l.unexplained
	foreign := l.trial != nil && l.trial.foreign
	irType := l.irType(t, node)
	switch {
	case irType == nil:
		l.errors, l.reports, l.unexplained = errors, reports, unexplained
		if l.trial != nil {
			l.trial.foreign = foreign // the report that marked it is taken back
		}
		fmt.Fprintf(identity, ",t%d", t.Id())
	case irType == ir.Value:
		fmt.Fprintf(identity, ",v%d", t.Id())
	default:
		fmt.Fprintf(identity, ",%p", irType)
	}
}

// instanceCall lowers a demand's call of x, the closure or (for a method named name) the
// receiver: a call of the instance x lists for the call's signature, or else erasedCall,
// given x and the arguments, each of the call's parameter type, in locals.
func (l *lowerer) instanceCall(d *demand, call *ast.CallExpression, x ir.Expr, name string, erasedCall func(x ir.Expr, args []ir.Expr) ir.Expr) ir.Expr {
	args := l.typedArguments(call.Arguments.Nodes, d.want.Params)
	if args == nil {
		return nil
	}
	object := l.newLocal("callee", x.Type())
	locals := make([]*ir.Local, len(args))
	refs := make([]ir.Expr, len(args))
	for i, arg := range args {
		locals[i] = l.newLocal(fmt.Sprintf("argument%d", i+1), arg.Type())
		refs[i] = &ir.LocalRef{Local: locals[i]}
	}
	otherwise := erasedCall(&ir.LocalRef{Local: object}, refs)
	if otherwise == nil {
		return nil
	}
	l.demands = append(l.demands, d)
	result := ir.Expr(&ir.InstanceCall{X: &ir.LocalRef{Local: object}, Name: name, Signature: d.want, Key: d.key, Args: refs, Otherwise: otherwise})
	for i := len(args) - 1; i >= 0; i-- {
		result = &ir.Bind{Local: locals[i], Value: args[i], In: result}
	}
	return &ir.Bind{Local: object, Value: x, In: result}
}

// erasedArguments converts a demand's arguments to the parameter types of the erased copy.
func (l *lowerer) erasedArguments(args []ir.Expr, params []ir.Type, node *ast.Node) []ir.Expr {
	converted := make([]ir.Expr, len(args))
	for i, arg := range args {
		if rest, ok := params[i].(*ir.Rest); ok {
			if _, passedOn := arg.Type().(*ir.Rest); passedOn {
				arg = &ir.RestArgs{X: arg} // a forwarding instance's or adapter's rest parameter: its array (D-194)
			}
			converted[i] = arg // the call's own new array
			if arg.Type() != rest.Array {
				// of typed elements, which the erased copy takes as one of dynamic values: a
				// copy no one can tell from it (C170)
				converted[i] = l.dynamicConversion(box(arg), rest.Array, node)
			}
			continue
		}
		converted[i] = l.convert(arg, params[i], node)
	}
	return converted
}

// instanceValueCall lowers a call of callee, a value of a generic function type (ft, its
// erased signature) whose result may hold generic class instances, as a demand (D-148);
// ok is false if the call isn't one.
func (l *lowerer) instanceValueCall(call *ast.CallExpression, callee ir.Expr, ft *ir.FuncType) (x ir.Expr, ok bool) {
	signatures := l.checker.GetSignaturesOfType(l.typeAt(call.Expression), checker.SignatureKindCall)
	if len(signatures) != 1 || len(signatures[0].TypeParameters()) == 0 || ft.Result != ir.Value ||
		!l.holdsGenericObjects(l.checker.GetReturnTypeOfSignature(signatures[0]), signatures[0].TypeParameters()) {
		return nil, false
	}
	d := l.demandAt(call, ft.Params)
	if d == nil {
		return nil, false
	}
	d.erased = ft
	if name := ast.SkipParentheses(call.Expression); name.Kind == ast.KindIdentifier {
		d.callee = l.target(name)
	}
	return l.instanceCall(d, call, callee, "", func(closure ir.Expr, args []ir.Expr) ir.Expr {
		return l.fromErasedFor(d, &ir.CallValue{Callee: closure, Args: l.erasedArguments(args, ft.Params, call.AsNode())}, call.AsNode())
	}), true
}

// dispatchedInstanceCall lowers a call of a dispatched generic method (D-101) on receiver,
// whose erased copies take method's parameters, as a demand (D-148) if its result may hold
// generic class instances; ok is false if the call isn't one.
func (l *lowerer) dispatchedInstanceCall(call *ast.CallExpression, generic *genericFunc, receiver ir.Expr, method *ir.InterfaceMethod) (x ir.Expr, ok bool) {
	if method.Result != ir.Value || !l.holdsGenericObjects(l.checker.GetReturnTypeOfSignature(l.checker.GetSignatureFromDeclaration(generic.node)), generic.parameters) {
		return nil, false
	}
	d := l.demandAt(call, method.Params)
	if d == nil {
		return nil, false
	}
	d.method, d.name = generic, method.Name
	return l.instanceCall(d, call, receiver, method.Name, func(object ir.Expr, args []ir.Expr) ir.Expr {
		object = &ir.ClassCast{X: object, To: &ir.Interface{}} // any object, whose method is found by name
		return l.fromErasedFor(d, &ir.InterfaceCall{Method: method, Args: append([]ir.Expr{object}, l.erasedArguments(args, method.Params, call.AsNode())...)}, call.AsNode())
	}), true
}

// namedInstanceCall lowers a call of the method name on receiver, through an interface, a
// union of object types or an object literal's type (its shape, else nil), of a generic
// method whose result may hold generic class instances and whose erased copies take
// params: as a demand by name (D-148, D-191), a call of the instance that the object's
// class, or its object literal's method closure, lists, if every argument passes to the
// instance's parameter as it is (passedAsIs); else, or if there is none, the erased copy's
// call, erasedCall, given the object and the arguments of params, with its result
// converted to the call's type, as before; a union's call's (dynamicResult) stays dynamic,
// as before, and its instance's is boxed. ok is false if the method isn't such a one.
func (l *lowerer) namedInstanceCall(call *ast.CallExpression, receiver ir.Expr, name string, shape *ir.Class, params []ir.Type, result ir.Type, dynamicResult bool,
	erasedCall func(object ir.Expr, args []ir.Expr) ir.Expr) (x ir.Expr, ok bool) {
	return l.keyedInstanceCall(call, receiver, []string{name}, nil, shape, params, result, dynamicResult, erasedCall)
}

// keyedInstanceCall is namedInstanceCall of a method named by one of names: the one byKey,
// a local holding a string, holds at run time (D-194), or if byKey is nil, the one name.
func (l *lowerer) keyedInstanceCall(call *ast.CallExpression, receiver ir.Expr, names []string, byKey ir.Expr, shape *ir.Class, params []ir.Type, result ir.Type, dynamicResult bool,
	erasedCall func(object ir.Expr, args []ir.Expr) ir.Expr) (x ir.Expr, ok bool) {
	if l.specialization != AllInstances || result != ir.Value {
		return nil, false
	}
	signatures := l.checker.GetSignaturesOfType(l.typeAt(call.Expression), checker.SignatureKindCall)
	if len(signatures) != 1 || len(signatures[0].TypeParameters()) == 0 ||
		!l.holdsGenericObjects(l.checker.GetReturnTypeOfSignature(signatures[0]), signatures[0].TypeParameters()) {
		return nil, false
	}
	d := l.demandAt(call, params)
	if d == nil {
		return nil, false
	}
	values, at, ok := l.argumentList(call.Arguments.Nodes, params)
	if !ok {
		return nil, true
	}
	args := l.convertArguments(values, at, params) // as the erased copy takes them, which the base passed
	if args == nil {
		return nil, true
	}
	erased := func(object ir.Expr, args []ir.Expr) ir.Expr {
		return l.fromErasedFor(d, erasedCall(object, args), call.AsNode())
	}
	nodes := argumentNodes(call.Arguments.Nodes, params)
	passed := l.passedAsIs(d, args, values, nodes, params)
	if passed == nil {
		if dynamicResult {
			return erasedCall(receiver, args), true
		}
		return l.withReceiver(receiver, func(object ir.Expr) ir.Expr { return erased(object, args) }), true
	}
	d.name, d.shape = names[0], shape
	d.receiver = l.instantiate(l.checker.GetNonNullableType(l.typeAt(ast.SkipParentheses(call.Expression).Expression())))
	object := l.newLocal("callee", receiver.Type())
	locals := make([]*ir.Local, len(passed))
	erasedArgs, typedArgs := make([]ir.Expr, len(passed)), make([]ir.Expr, len(passed))
	for i, value := range passed {
		node := nodes[i]
		if node == nil {
			node = call.AsNode()
		}
		if _, leftOut := value.(*ir.LeftOut); leftOut {
			erasedArgs[i], typedArgs[i] = &ir.LeftOut{T: params[i]}, value // an optional argument not given, whose flag says so
			continue
		}
		locals[i] = l.newLocal(fmt.Sprintf("argument%d", i+1), value.Type())
		if isRest(params[i]) { // the call's new array, of the instance's elements (passedAsIs, C170)
			erasedArgs[i] = l.erasedArguments([]ir.Expr{&ir.LocalRef{Local: locals[i]}}, params[i:i+1], node)[0]
			typedArgs[i] = &ir.LocalRef{Local: locals[i]}
			continue
		}
		erasedArgs[i] = l.convert(&ir.LocalRef{Local: locals[i]}, params[i], node)
		typedArgs[i] = l.convert(&ir.LocalRef{Local: locals[i]}, d.want.Params[i], node)
	}
	otherwise := erasedCall(&ir.LocalRef{Local: object}, erasedArgs)
	if !dynamicResult {
		otherwise = erased(&ir.LocalRef{Local: object}, erasedArgs)
	}
	if otherwise == nil {
		return nil, true
	}
	l.demands = append(l.demands, d)
	for _, other := range names[1:] { // each name byKey may hold
		named := *d
		named.name = other
		l.demands = append(l.demands, &named)
	}
	x = &ir.InstanceCall{X: &ir.LocalRef{Local: object}, Name: names[0], Signature: d.want, Key: d.key, Args: typedArgs, Otherwise: otherwise, Dynamic: dynamicResult}
	if byKey != nil {
		x.(*ir.InstanceCall).Name, x.(*ir.InstanceCall).ByKey = "", byKey
	}
	for i := len(passed) - 1; i >= 0; i-- {
		if locals[i] != nil {
			x = &ir.Bind{Local: locals[i], Value: passed[i], In: x}
		}
	}
	return &ir.Bind{Local: object, Value: receiver, In: x}, true
}

// argumentNodes are the argument nodes that typedArguments matches to params, one each, nil
// for a given flag (D-055) or an argument left out; nil if one is a spread.
func argumentNodes(arguments []*ast.Node, params []ir.Type) []*ast.Node {
	nodes := make([]*ast.Node, len(params))
	next := 0
	for i, param := range params {
		if param == ir.Given {
			continue
		}
		if next < len(arguments) {
			if arguments[next].Kind == ast.KindSpreadElement {
				return nil
			}
			nodes[i] = arguments[next]
		}
		next++
	}
	return nodes
}

// passedAsIs are args, each of params' type as an erased copy takes it, as the instance of
// a demand d takes them: each of its parameter's type as it is, and each other one as it
// was before it was boxed (D-191). It is nil if one of those isn't a value of its
// parameter's type as it is (passesAsIs) or is a dynamic value: then the call takes the
// erased copy, which the base passed it to boxed, rather than an instance that a copy, a
// check or an adapter would pass it to (`Dog[]` where `Animal[]` goes, `any` where an
// `Animal` goes), or that a dynamic value an erased copy made (`Box<any>` where tsc says
// `Box<number>`) would reach unchecked or fail a check. It is nil too if a parameter typed
// with the method's type parameters is of a type that conversions don't verify (`o:
// Option<Box<T>>`, a union of object types holding a generic class's instance): an object
// an erased copy made may be one of it only as a member that leaves its `Box<any>` out,
// which the instance would read as another member's (verifiesObjects; C187, D-192).
func (l *lowerer) passedAsIs(d *demand, args, values []ir.Expr, nodes []*ast.Node, params []ir.Type) []ir.Expr {
	want := d.want.Params
	if nodes == nil || len(args) != len(params) || len(args) != len(want) || l.takesUnverifiedObjects(d, params) {
		return nil
	}
	passed := make([]ir.Expr, len(args))
	for i, arg := range args {
		boxed, isBoxed := arg.(*ir.Box)
		_, leftOut := arg.(*ir.LeftOut)
		switch {
		case want[i] == params[i]:
			passed[i] = arg
		case isErasedRest(params[i], want[i]):
			// The rest arguments, the values the call passes after the other parameters', as
			// they were before the erased copy's conversion, in a new array of the instance's
			// elements (C170).
			if passed[i] = l.restAsIs(values[min(arity(params[:i]), len(values)):], want[i].(*ir.Rest).Array, d.node); passed[i] == nil {
				return nil
			}
		case params[i] != ir.Value:
			return nil
		case leftOut:
			passed[i] = &ir.LeftOut{T: want[i]}
		case isBoxed && passesAsIs(boxed.X.Type(), want[i]):
			passed[i] = boxed.X
		case !isBoxed && arg.Type() == ir.Value && nodes[i] != nil && l.typedPrimitive(nodes[i], want[i]):
			// A dynamic value that tsc types as a primitive (a yield's, `0 + (yield 1)`):
			// checked, as a direct call of a class's method checks it (C205, D-194).
			if passed[i] = l.convert(arg, want[i], nodes[i]); passed[i] == nil {
				return nil
			}
		default:
			return nil
		}
	}
	return passed
}

// typedPrimitive reports whether node, an argument whose value is dynamic, is typed by tsc
// as neither any nor unknown, and t, the instance's parameter it goes to, is a primitive
// type or a union of them, which a check gives as it is (D-070).
func (l *lowerer) typedPrimitive(node *ast.Node, t ir.Type) bool {
	if l.instantiate(l.typeAt(node)).Flags()&(checker.TypeFlagsAny|checker.TypeFlagsUnknown) != 0 {
		return false
	}
	primitive := func(t ir.Type) bool {
		return t == ir.Number || t == ir.String || t == ir.Bool || t == ir.Null || t == ir.Undefined || ir.IsNumeric(t)
	}
	if u, ok := t.(*ir.Union); ok {
		return !slices.ContainsFunc(u.Members, func(m ir.Type) bool { return !primitive(m) })
	}
	return primitive(t)
}

// restAsIs is a new array of the instance's elements array holding values, the rest
// arguments of a call, each as it is (passesAsIs), or nil if one is a dynamic value or isn't
// one of the elements as it is.
func (l *lowerer) restAsIs(values []ir.Expr, array *ir.Array, node *ast.Node) ir.Expr {
	elems := make([]ir.Expr, len(values))
	for j, value := range values {
		if _, spread := value.(*spreadValue); spread || value.Type() == ir.Value || !passesAsIs(value.Type(), array.Elem) {
			return nil // a spread, which makes no demand (D-191), or a dynamic value
		}
		if elems[j] = l.convert(value, array.Elem, node); elems[j] == nil { // as it is (a union's member, a wider number)
			return nil
		}
	}
	return &ir.NewArray{Array: array, Elems: elems}
}

// takesUnverifiedObjects reports whether the instance of a demand d takes a parameter (of
// params' places, which a Given flag follows for an optional parameter) whose declared
// type mentions the method's type parameters, is in d's call neither any nor unknown, and
// is of an IR type that conversions don't verify (verifiesObjects).
func (l *lowerer) takesUnverifiedObjects(d *demand, params []ir.Type) bool {
	declaration := d.signature.Declaration()
	if declaration == nil {
		return true
	}
	generic := l.checker.GetSignatureFromDeclaration(declaration)
	typeParameters := generic.TypeParameters()
	anys := make([]*checker.Type, len(typeParameters))
	for i := range anys {
		anys[i] = l.checker.GetAnyType()
	}
	declared, resolved := generic.Parameters(), d.signature.Parameters()
	next := 0
	for i, param := range params {
		if param == ir.Given {
			continue
		}
		k := next
		next++
		if verifiesObjects(d.want.Params[i], map[ir.Type]bool{}) || k >= len(declared) || k >= len(resolved) {
			continue
		}
		t := l.checker.GetTypeOfSymbol(declared[k])
		if l.checker.PtsInstantiateType(t, typeParameters, anys) != t &&
			l.instantiate(l.checker.GetTypeOfSymbol(resolved[k])).Flags()&(checker.TypeFlagsAny|checker.TypeFlagsUnknown) == 0 {
			return true
		}
	}
	return false
}

// verifiesObjects reports whether a value converted to type t is one of t all through, so
// that an instance may read it as typed code does. Conversions verify an object's fields
// by their declared types or copy it into t's representation, also an object an erased
// copy made with dynamic values for the type parameters (an `any[]`, a Map of dynamic
// values), and typed reads verify what a union took as it is (C187, D-192). Not verified
// are a dynamic value, which the erased copy takes as it is (D-191: `never[]`'s elements,
// which the instance reads typed), and an erased copy's instance of a generic class
// (`Box<any>` where t says `Box<number>`), which never converts: inside a union of object
// types, its object is taken as a member that leaves it out, which the instance would read
// as another member's and throw at, where the erased copy reads it.
func verifiesObjects(t ir.Type, seen map[ir.Type]bool) bool {
	return !holdsUnverified(t, false, seen)
}

// holdsUnverified reports whether t holds a dynamic value, or inside a union of object
// types (or inUnion, inside one already) an instance of a generic class (verifiesObjects).
func holdsUnverified(t ir.Type, inUnion bool, seen map[ir.Type]bool) bool {
	if seen[t] {
		return false
	}
	seen[t] = true
	defer delete(seen, t) // t may be met again inside a union
	holds := func(t ir.Type) bool { return holdsUnverified(t, inUnion, seen) }
	switch t := t.(type) {
	case *ir.Primitive:
		return t == ir.Value
	case *ir.FuncType: // a closure of another type is adapted, and its results checked
		return false
	case *ir.Class:
		if !t.Shape {
			return inUnion && t.Display != ""
		}
		return slices.ContainsFunc(t.Fields, func(f *ir.Field) bool { return holds(f.Type) })
	case *ir.Interface:
		return slices.ContainsFunc(t.Fields, func(f *ir.Field) bool { return holds(f.Type) })
	case *ir.Array:
		return holds(t.Elem)
	case *ir.Rest: // the call's new array (C170)
		return holds(t.Array)
	case *ir.MapType:
		return holds(t.Key) || (t.Value != nil && holds(t.Value))
	case *ir.Union:
		objects := 0
		for _, member := range t.Members {
			if isObject(member) && !isArray(member) && !isFunction(member) {
				objects++
			}
		}
		inUnion = inUnion || objects > 1
		return slices.ContainsFunc(t.Members, func(m ir.Type) bool { return holdsUnverified(m, inUnion, seen) })
	}
	return true
}

// passesAsIs reports whether a value of type from is one of type to as it is, which
// convert gives without a copy, a check or an adapter: the same type, a dynamic value, a
// wider number, a union's member, or a reference to an object where its class's ancestor or
// an interface it provides goes.
func passesAsIs(from, to ir.Type) bool {
	switch {
	case from == to || to == ir.Value:
		return true
	case from == ir.Value:
		return false
	case ir.IsNumeric(from) && to == ir.Number:
		return true
	}
	if u, ok := to.(*ir.Union); ok {
		if members, ok := from.(*ir.Union); ok {
			return !slices.ContainsFunc(members.Members, func(m ir.Type) bool { return !u.Has(m) })
		}
		if u.Has(from) {
			return true
		}
		// An object of the member of a union of object types that it provides as it is,
		// which toUnion takes it as (`{ tag: "some", value: 3 }` where an `Opt<number>`
		// goes), holding no dynamic value a typed read would convert (C188).
		member := providedMember(from, u)
		return member != nil && isObject(from) && !isArray(from) && !leavesOut(from, member, u) &&
			verifiesObjects(from, map[ir.Type]bool{}) && passesAsIs(from, member)
	}
	if class, ok := from.(*ir.Class); ok && class.Result {
		return false // an iterator result's object, which may be of another layout (D-178)
	}
	switch to := to.(type) {
	case *ir.Class:
		class, ok := from.(*ir.Class)
		return ok && !to.Result && class.IsSubclassOf(to)
	case *ir.Interface:
		return isObject(from) && !isArray(from) && unprovided(from, to) == ""
	}
	return false
}

// specialize makes the instances that the demands so far look up, of the erased values
// and generic methods so far, each pair once. It reports whether it met a new pair: the
// bodies of the instances it made, which may add demands, values and methods, are then
// still to be lowered.
func (l *lowerer) specialize() bool {
	met := false
	for i, d := range l.demands {
		if d.dropped {
			continue
		}
		if d.method == nil {
			for j, v := range l.erasedValues {
				if key := [3]int{i, j, 0}; !l.specialized[key] {
					l.specialized[key], met = true, true
					if !v.dropped && (v.generic == nil || v.generic.calledOnly == nil || v.generic.calledOnly == d.callee) {
						l.specializeValue(d, v)
					}
				}
			}
		}
		if d.method == nil && (d.name == "" || d.shape != nil) {
			continue // no class's method
		}
		for j, g := range l.genericMethodList {
			if key := [3]int{i, j, 1}; !l.specialized[key] {
				l.specialized[key], met = true, true
				l.specializeMethod(d, g)
			}
		}
	}
	return met
}

// specializeValue lists, in an erased value's descriptor, the instance of its generic
// function that a demand's call looks up: for the type arguments tsc infers relating the
// function to the call's resolved signature, if that instance can be called as one of the
// call's signature.
func (l *lowerer) specializeValue(d *demand, v *erasedValue) {
	if !l.reaches(d, v) || slices.ContainsFunc(v.fn.Instances, func(inst *ir.Instance) bool {
		return inst.Name == d.name && inst.Signature == d.want && inst.Key == d.key
	}) {
		return
	}
	if v.forward != nil {
		l.forwardingInstance(d, v) // a wrapper's (D-194)
		return
	}
	outer := l.subst
	l.subst = d.subst
	defer func() { l.subst = outer }()
	instantiated := l.checker.PtsInstantiateSignatureInContextOf(l.checker.GetSignatureFromDeclaration(v.generic.node), d.signature)
	arguments := l.checker.PtsTypeArgumentsOfSignature(instantiated)
	if len(arguments) != len(v.generic.parameters) || slices.ContainsFunc(arguments, l.mentionsErased) ||
		l.narrowsAway(v.generic, l.instantiateAll(arguments), map[*genericFunc]bool{}) {
		return
	}
	params := 0
	if d.name != "" {
		params = 1 // after `this`
	}
	fits := func(fn *ir.Func) bool {
		return (!fn.Closure || fn.Shares == v.fn) && fitsInstance(fn.Params[params:], fn.Result, d.want)
	}
	if fn := l.tryInstance(d, v.generic, arguments, fits); fn != nil {
		v.fn.Instances = append(v.fn.Instances, &ir.Instance{Name: d.name, Signature: d.want, Key: d.key, Func: fn})
	}
}

// reaches reports whether a demand's call may call an erased value: a call of a value of
// its erased function type, or for an object literal's method (or an adapter given for one,
// D-194), a call of a method of its name through an interface, a union or its literal's
// shape, whose object may be the literal (D-191).
func (l *lowerer) reaches(d *demand, v *erasedValue) bool {
	if f := v.forward; f != nil {
		if f.name == "" {
			return d.name == "" && l.typeOfFunc(v.fn) == d.erased
		}
		return d.name == f.name && (d.shape == nil || d.shape == f.shape) && l.checker.IsTypeAssignableTo(f.literal, d.receiver)
	}
	field := v.generic.field
	if field == nil {
		return d.name == "" && l.typeOfFunc(v.fn) == d.erased
	}
	if d.name != field.Name || d.shape != nil && d.shape != field.Class {
		return false
	}
	literal := l.checker.GetWidenedType(l.checker.GetTypeAtLocation(v.generic.node.Parent))
	if outer := v.generic.outer; outer != nil {
		literal = l.checker.PtsInstantiateType(literal, outer.parameters, outer.arguments)
	}
	return l.checker.IsTypeAssignableTo(literal, d.receiver)
}

// mayReceive reports whether an instance of class, or of a subclass, may be the object of a
// demand by name (D-191): its type is assignable to the demand's receiver's. Pairing the
// demand with any class's method of its name would make instances no call uses, whose
// failures a trial can't always take back (C176).
func (l *lowerer) mayReceive(d *demand, class *ir.Class) bool {
	key := receiving{d.receiver, class}
	if receives, ok := l.receives[key]; ok {
		return receives
	}
	receives := slices.ContainsFunc(slices.Concat(l.classList, l.instanceClasses), func(other *ir.Class) bool {
		if !other.IsSubclassOf(class) {
			return false
		}
		t := l.classType(other)
		return t != nil && l.checker.IsTypeAssignableTo(t, d.receiver)
	})
	l.receives[key] = receives
	return receives
}

// receiving is a receiver's type and a class, which mayReceive remembers.
type receiving struct {
	receiver *checker.Type
	class    *ir.Class
}

// classType is the checker type of class's instances, or nil.
func (l *lowerer) classType(class *ir.Class) *checker.Type {
	if inst := l.classInstances[class]; inst != nil {
		declared := l.checker.GetDeclaredTypeOfSymbol(l.declaredSymbol(inst.generic.node))
		return l.checker.PtsInstantiateType(declared, inst.subst.parameters, inst.subst.arguments)
	}
	if node := l.classNodes[class]; node != nil {
		return l.checker.GetDeclaredTypeOfSymbol(l.declaredSymbol(node))
	}
	return nil
}

// tryInstance returns the instance of generic for a demand's type arguments if it fits
// the demand (fits) and lowers, else nil. It makes it in a trial: the instance's body, and
// the bodies of the instances it calls, are lowered at once, and if they fail to lower,
// or the instance doesn't fit, everything the trial made is dropped, so the call keeps
// the erased copy (D-190). Pairing every demand with every erased value infers instances
// no call can use, and an instance's code may not lower where its erased copy did (a
// branch tsc narrows to an intersection for its type arguments), where the program
// compiled with the erased copy. A failure the trial can't take back (another function's)
// stands; the build then falls back (Fallbacks).
func (l *lowerer) tryInstance(d *demand, generic *genericFunc, arguments []*checker.Type, fits func(fn *ir.Func) bool) *ir.Func {
	t := &trial{funcs: map[*ir.Func]bool{}}
	errors, reports, unexplained := l.errors, l.reports, l.unexplained // report only appends to errors
	lowered, demands, values := len(l.lowered), len(l.demands), len(l.erasedValues)
	l.trial = t
	fn := l.genericInstanceFor(generic, arguments, d.node, d.key != "")
	if fn != nil && l.reports == reports && fits(fn) {
		l.lowerPending()
	} else {
		fn = nil
	}
	l.trial = nil
	switch {
	case t.foreign:
		return nil // another function failed: that stands, as it would without the trial
	case fn != nil && l.reports == reports && l.unexplained == unexplained:
		return fn
	}
	l.errors, l.unexplained = errors, unexplained
	for _, inst := range t.instances {
		inst.generic.instances = slices.DeleteFunc(inst.generic.instances, func(other *instance) bool { return other == inst })
	}
	l.instances = slices.DeleteFunc(l.instances, func(inst *instance) bool { return slices.Contains(t.instances, inst) }) // not lowered, if it didn't fit
	for fn := range t.funcs {
		l.droppedFuncs[fn] = true
	}
	l.lowered = append(l.lowered[:lowered], slices.DeleteFunc(slices.Clone(l.lowered[lowered:]), func(f *ir.Func) bool {
		if t.owns(f) {
			l.droppedFuncs[f] = true
		}
		return t.owns(f)
	})...)
	for _, other := range l.demands[demands:] {
		other.dropped = t.owns(other.in)
	}
	for _, other := range l.erasedValues[values:] {
		other.dropped = t.owns(other.fn) // a closure in the dropped code; not, say, a declaration's erased copy it named
	}
	for _, c := range t.captures {
		c.fn.Captures = slices.DeleteFunc(c.fn.Captures, func(local *ir.Local) bool { return local == c.local })
	}
	return nil
}

// trial is what tryInstance makes while it tries an instance (D-190).
type trial struct {
	instances []*instance       // the instances made in it
	funcs     map[*ir.Func]bool // their functions
	captures  []capturedBy      // the locals its code made functions outside it capture
	making    int               // above 0 while an instance is made, whose reports are the trial's
	foreign   bool              // code that isn't the trial's reported
}

// capturedBy is a local a function captures.
type capturedBy struct {
	fn    *ir.Func
	local *ir.Local
}

// owns reports whether fn is the function of an instance the trial made, or of a closure
// or function declaration in one.
func (t *trial) owns(fn *ir.Func) bool {
	for ; fn != nil; fn = fn.Outer {
		if t.funcs[fn] {
			return true
		}
	}
	return false
}

// lowerPending lowers the function bodies and instances that wait to be lowered: in a
// trial, those the trial made (specialize runs once nothing else waits).
func (l *lowerer) lowerPending() {
	outerSubst, outerFunction := l.subst, l.function
	defer func() { l.subst, l.function = outerSubst, outerFunction }()
	for len(l.bodies) > 0 || len(l.instances) > 0 {
		if len(l.bodies) > 0 {
			body := l.bodies[0]
			l.bodies = l.bodies[1:]
			l.functionBody(body)
			continue
		}
		inst := l.instances[0]
		l.instances = l.instances[1:]
		l.instanceBody(inst)
	}
}

// shareCaptures gives each instance of a generic arrow function or function expression
// and its erased copy the same captures, all that either uses, since the instance runs
// with the erased copy's closures (D-190).
func shareCaptures(funcs []*ir.Func) {
	for _, fn := range funcs {
		if erased := fn.Shares; erased != nil {
			for _, local := range fn.Captures {
				if !slices.Contains(erased.Captures, local) {
					erased.Captures = append(erased.Captures, local)
				}
			}
		}
	}
	for _, fn := range funcs {
		if fn.Shares != nil {
			fn.Captures = slices.Clone(fn.Shares.Captures)
		}
	}
}

// specializeMethod lists, in the class of g, a generic method that implements the one a
// demand's call dispatches (its class's own of that name, in the method's class or a
// subclass) or calls through an interface (any class's of that name, D-191), g's instance
// for the call's type arguments, if it can be called as one of the call's signature.
// Otherwise, or where g's erased copy makes no generic class instances, the entry has no
// instance, so that the call takes g's erased copy rather than finding an ancestor's
// instance.
func (l *lowerer) specializeMethod(d *demand, g *genericFunc) {
	if g.static != nil || g.node.Name().Text() != d.name || d.method != nil && !descendsFrom(g.class, d.method.class) || d.method == nil && !l.mayReceive(d, g.class) ||
		slices.ContainsFunc(g.class.Instances, func(inst *ir.Instance) bool {
			return inst.Name == d.name && inst.Signature == d.want && inst.Key == d.key
		}) {
		return
	}
	entry := &ir.Instance{Name: d.name, Signature: d.want, Key: d.key}
	g.class.Instances = append(g.class.Instances, entry)
	if !l.makesGenericInstances(g, map[*genericFunc]bool{}) {
		return
	}
	outer := l.subst
	l.subst = d.subst
	defer func() { l.subst = outer }()
	// g's type arguments as tsc infers them relating g to the call's resolved signature: an
	// interface's method or an overridden one may have other type parameters than g.
	arguments := l.checker.PtsTypeArgumentsOfSignature(l.checker.PtsInstantiateSignatureInContextOf(l.checker.GetSignatureFromDeclaration(g.node), d.signature))
	if len(arguments) != len(g.parameters) || slices.ContainsFunc(arguments, l.mentionsErased) ||
		l.narrowsAway(g, l.instantiateAll(arguments), map[*genericFunc]bool{}) {
		return
	}
	entry.Func = l.tryInstance(d, g, arguments, func(fn *ir.Func) bool { return fitsInstance(fn.Params[1:], fn.Result, d.want) })
}

// narrowsAway reports whether the instance of generic for concrete type arguments (with
// its class instance's, for a method) has code that narrows a variable to a type that no
// value of the instance has (`typeof x === "number"` for a string x), directly or in a
// generic function it calls by name: instances don't lower such code (D-102), so a demand
// takes the erased copy instead of making the instance.
func (l *lowerer) narrowsAway(generic *genericFunc, concrete []*checker.Type, seen map[*genericFunc]bool) bool {
	if seen[generic] || generic.node.Body() == nil {
		return false
	}
	seen[generic] = true
	parameters, arguments := generic.parameters, concrete
	if outer := generic.outer; outer != nil {
		parameters = append(slices.Clone(outer.parameters), parameters...)
		arguments = append(slices.Clone(outer.arguments), arguments...)
	}
	instantiate := func(t *checker.Type) *checker.Type { return l.checker.PtsInstantiateType(t, parameters, arguments) }
	isNever := func(t *checker.Type) bool { return t.Flags()&checker.TypeFlagsNever != 0 }
	found := false
	var visit func(node *ast.Node) bool
	visit = func(node *ast.Node) bool {
		switch node.Kind {
		case ast.KindIdentifier:
			symbol := l.checker.GetSymbolAtLocation(node)
			if symbol != nil && symbol.Flags&ast.SymbolFlagsVariable != 0 && !ast.IsDeclarationName(node) {
				found = isNever(instantiate(l.checker.GetTypeAtLocation(node))) && !isNever(instantiate(l.checker.GetTypeOfSymbol(symbol)))
			}
		case ast.KindCallExpression:
			callee := ast.SkipParentheses(node.Expression())
			if callee.Kind == ast.KindIdentifier {
				target := l.target(callee)
				other := l.generics[target]
				if other == nil {
					other = l.genericConsts[target]
				}
				if other != nil {
					var typeArguments []*checker.Type
					for _, t := range l.checker.PtsTypeArgumentsOfSignature(l.checker.GetResolvedSignature(node)) {
						typeArguments = append(typeArguments, instantiate(t))
					}
					found = len(typeArguments) == len(other.parameters) && l.narrowsAway(other, typeArguments, seen)
				}
			}
		}
		return found || node.ForEachChild(visit)
	}
	visit(generic.node.Body())
	return found
}

// instantiateAll is instantiate of each type.
func (l *lowerer) instantiateAll(types []*checker.Type) []*checker.Type {
	instantiated := make([]*checker.Type, len(types))
	for i, t := range types {
		instantiated[i] = l.instantiate(t)
	}
	return instantiated
}

// descendsFrom reports whether class is ancestor or one of its subclasses.
func descendsFrom(class, ancestor *ir.Class) bool {
	for ; class != nil; class = class.Parent {
		if class == ancestor {
			return true
		}
	}
	return false
}

// fitsInstance reports whether a function taking params and returning result can be
// called as a function of type want: it takes want's parameters, or the first of them
// (but not a parameter without its given flag), and returns want's result or an instance
// of a subclass.
func fitsInstance(params []*ir.Local, result ir.Type, want *ir.FuncType) bool {
	if len(params) > len(want.Params) || len(params) < len(want.Params) && want.Params[len(params)] == ir.Given {
		return false
	}
	for i, param := range params {
		if param.Type != want.Params[i] {
			return false
		}
	}
	return isCovariantResult(result, want.Result)
}
