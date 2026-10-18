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
// (whose erased signature is erased), or of a dispatched generic method (method).
type demand struct {
	in        *ir.Func    // the function the call is in
	callee    *ast.Symbol // what the call's callee names, if it is a name
	dropped   bool        // in code a failed trial dropped (D-190)
	erased    *ir.FuncType
	method    *genericFunc
	signature *checker.Signature // the call's resolved signature
	subst     *substitution      // the instance being lowered where the call is, if any
	want      *ir.FuncType       // the instance's function type, after `this`: the resolved signature's
	key       string             // for a dynamic result, its type arguments' (ir.Instance.Key, instanceKey)
	node      *ast.Node
}

// erasedValue is a function whose closures are a generic function's erased copy as a
// value, whose descriptor lists the generic function's instances that calls look up.
type erasedValue struct {
	fn      *ir.Func
	generic *genericFunc
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
// type arguments don't mention erased type parameters, it passes no rest parameter, and
// its result converts from the erased copy's to the type of the resolved signature's;
// else nil. A dynamic result is one too: a promise or generator object, whose values an
// instance makes for the call's type arguments (D-190).
func (l *lowerer) demandAt(call *ast.CallExpression, erasedParams []ir.Type) *demand {
	resolved := l.checker.GetResolvedSignature(call.AsNode())
	if resolved.HasRestParameter() || slices.ContainsFunc(l.checker.PtsTypeArgumentsOfSignature(resolved), l.mentionsErased) {
		return nil
	}
	want := l.signatureType(resolved, call.AsNode())
	if want == nil || want.Result == ir.Value && l.specialization == DeclaredInstances || want.Result != l.erasedTarget(call.AsNode()) || len(want.Params) != len(erasedParams) {
		return nil
	}
	for i, param := range want.Params {
		if erasedParams[i] != param && erasedParams[i] != ir.Value {
			return nil
		}
	}
	d := &demand{in: l.function, signature: resolved, subst: l.subst, want: want, node: call.AsNode()}
	if want.Result == ir.Value {
		d.key = l.instanceKey(resolved, call.AsNode()) // its values are made for its type arguments (D-190)
	}
	return d
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
		return l.fromErased(&ir.CallValue{Callee: closure, Args: l.erasedArguments(args, ft.Params, call.AsNode())}, call.AsNode())
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
	d.method = generic
	return l.instanceCall(d, call, receiver, method.Name, func(object ir.Expr, args []ir.Expr) ir.Expr {
		object = &ir.ClassCast{X: object, To: &ir.Interface{}} // any object, whose method is found by name
		return l.fromErased(&ir.InterfaceCall{Method: method, Args: append([]ir.Expr{object}, l.erasedArguments(args, method.Params, call.AsNode())...)}, call.AsNode())
	}), true
}

// specialize makes the instances that the demands so far look up, of the erased values
// and generic methods so far, each pair once. It reports whether it met a new pair: the
// bodies of the instances it made, which may add demands, values and methods, are then
// still to be lowered.
func (l *lowerer) specialize() bool {
	met := false
	for i, d := range l.demands {
		switch {
		case d.dropped:
			continue
		case d.method == nil:
			for j, v := range l.erasedValues {
				if key := [3]int{i, j, 0}; !l.specialized[key] {
					l.specialized[key], met = true, true
					if !v.dropped && (v.generic.calledOnly == nil || v.generic.calledOnly == d.callee) {
						l.specializeValue(d, v)
					}
				}
			}
			continue
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
	if l.typeOfFunc(v.fn) != d.erased || slices.ContainsFunc(v.fn.Instances, func(inst *ir.Instance) bool { return inst.Signature == d.want && inst.Key == d.key }) {
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
	fits := func(fn *ir.Func) bool {
		return (!fn.Closure || fn.Shares == v.fn) && fitsInstance(fn.Params, fn.Result, d.want)
	}
	if fn := l.tryInstance(d, v.generic, arguments, fits); fn != nil {
		v.fn.Instances = append(v.fn.Instances, &ir.Instance{Signature: d.want, Key: d.key, Func: fn})
	}
}

// tryInstance returns the instance of generic for a demand's type arguments if it fits
// the demand (fits) and lowers, else nil. It makes it in a trial: the instance's body, and
// the bodies of the instances it calls, are lowered at once, and if they fail to lower,
// or the instance doesn't fit, everything the trial made is dropped, so the call keeps
// the erased copy (D-190). Pairing every demand with every erased value infers instances
// no call can use, and an instance's code may not lower where its erased copy did (a
// branch tsc narrows to an intersection for its type arguments), where the program
// compiled with the erased copy. A failure the trial can't take back (another function's)
// stands; the build then falls back to DeclaredInstances.
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
// subclass), g's instance for the call's type arguments, if it can be called as one of
// the call's signature. Otherwise, or where g's erased copy makes no generic class
// instances, the entry has no instance, so that the call takes g's erased copy rather
// than finding an ancestor's instance.
func (l *lowerer) specializeMethod(d *demand, g *genericFunc) {
	name := d.method.node.Name().Text()
	if g.static != nil || g.node.Name().Text() != name || !descendsFrom(g.class, d.method.class) ||
		slices.ContainsFunc(g.class.Instances, func(inst *ir.Instance) bool {
			return inst.Name == name && inst.Signature == d.want && inst.Key == d.key
		}) {
		return
	}
	entry := &ir.Instance{Name: name, Signature: d.want, Key: d.key}
	g.class.Instances = append(g.class.Instances, entry)
	arguments := l.checker.PtsTypeArgumentsOfSignature(d.signature)
	if len(arguments) != len(g.parameters) || !l.makesGenericInstances(g, map[*genericFunc]bool{}) {
		return
	}
	outer := l.subst
	l.subst = d.subst
	defer func() { l.subst = outer }()
	if l.narrowsAway(g, l.instantiateAll(arguments), map[*genericFunc]bool{}) {
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
