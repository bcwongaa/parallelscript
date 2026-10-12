package lower

import (
	"fmt"
	"slices"

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
	erased    *ir.FuncType
	method    *genericFunc
	signature *checker.Signature // the call's resolved signature
	subst     *substitution      // the instance being lowered where the call is, if any
	want      *ir.FuncType       // the instance's function type, after `this`: the resolved signature's
	node      *ast.Node
}

// erasedValue is a function whose closures are a generic function's erased copy as a
// value, whose descriptor lists the generic function's instances that calls look up.
type erasedValue struct {
	fn      *ir.Func
	generic *genericFunc
}

// addErasedValue records fn, a function whose closures are generic's erased copy as a
// value, if the erased copy makes generic class instances, which only an instance makes
// for the type arguments a call has.
func (l *lowerer) addErasedValue(fn *ir.Func, generic *genericFunc) {
	if slices.ContainsFunc(l.erasedValues, func(v *erasedValue) bool { return v.fn == fn }) || !l.makesGenericInstances(generic, map[*genericFunc]bool{}) {
		return
	}
	l.erasedValues = append(l.erasedValues, &erasedValue{fn, generic})
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
// else nil.
func (l *lowerer) demandAt(call *ast.CallExpression, erasedParams []ir.Type) *demand {
	resolved := l.checker.GetResolvedSignature(call.AsNode())
	if resolved.HasRestParameter() || slices.ContainsFunc(l.checker.PtsTypeArgumentsOfSignature(resolved), l.mentionsErased) {
		return nil
	}
	want := l.signatureType(resolved, call.AsNode())
	if want == nil || want.Result == ir.Value || want.Result != l.erasedTarget(call.AsNode()) || len(want.Params) != len(erasedParams) {
		return nil
	}
	for i, param := range want.Params {
		if erasedParams[i] != param && erasedParams[i] != ir.Value {
			return nil
		}
	}
	return &demand{signature: resolved, subst: l.subst, want: want, node: call.AsNode()}
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
	result := ir.Expr(&ir.InstanceCall{X: &ir.LocalRef{Local: object}, Name: name, Signature: d.want, Args: refs, Otherwise: otherwise})
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
		if d.method == nil {
			for j, v := range l.erasedValues {
				if key := [3]int{i, j, 0}; !l.specialized[key] {
					l.specialized[key], met = true, true
					l.specializeValue(d, v)
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
	if l.typeOfFunc(v.fn) != d.erased || slices.ContainsFunc(v.fn.Instances, func(inst *ir.Instance) bool { return inst.Signature == d.want }) {
		return
	}
	outer := l.subst
	l.subst = d.subst
	defer func() { l.subst = outer }()
	instantiated := l.checker.PtsInstantiateSignatureInContextOf(l.checker.GetSignatureFromDeclaration(v.generic.node), d.signature)
	arguments := l.checker.PtsTypeArgumentsOfSignature(instantiated)
	if len(arguments) != len(v.generic.parameters) || slices.ContainsFunc(arguments, l.mentionsErased) || l.narrowsAway(v.generic, l.instantiateAll(arguments), map[*genericFunc]bool{}) {
		return
	}
	if fn := l.genericInstance(v.generic, arguments, d.node); fn != nil && !fn.Closure && fitsInstance(fn.Params, fn.Result, d.want) {
		v.fn.Instances = append(v.fn.Instances, &ir.Instance{Signature: d.want, Func: fn})
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
		slices.ContainsFunc(g.class.Instances, func(inst *ir.Instance) bool { return inst.Name == name && inst.Signature == d.want }) {
		return
	}
	entry := &ir.Instance{Name: name, Signature: d.want}
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
	if fn := l.genericInstance(g, arguments, d.node); fn != nil && fitsInstance(fn.Params[1:], fn.Result, d.want) {
		entry.Func = fn
	}
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
