package lower

import (
	"fmt"
	"slices"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/pts/ir"
)

// Wrappers forward the instances of what they wrap (D-194). A generic method read as a
// value is a closure bound to its object (D-044), `bind` makes another of a function value,
// and an adapter wraps a function value for a method field (D-041): each is a closure of a
// function of its own, whose descriptor listed no instances, so a typed call of one through
// a generic function type called the erased copy, whose `Box<any>` doesn't convert to the
// `Box<number>` the call's type names (C169). Such a wrapper is an erased value whose
// instances (forwardingInstance) run with its closure and make the call it forwards, for
// the demand's signature: a demand of their own, on the object it is bound to or the
// function value it wraps. And a generic function value given where a function type that
// isn't generic goes is adapted to it by a demand (instantiatingAdapter): the adapter calls
// the instance the value lists for the type arguments tsc infers relating the two.

// forwarder is what a wrapper's closures forward their calls to (D-194): call makes that
// call for a demand, in fn (the forwarding instance), with args of the demand's parameter
// types, or is nil if it can't. name, shape and literal are an adapter's for a method field
// (functionAsMethod): demands by the method's name reach it, through its literal's type or
// its shape, as they reach an object literal's generic method (D-191), and its instances
// take the object first.
type forwarder struct {
	source  *ast.Node // exact wrapper creation site, not the captured value's source (D-205)
	name    string
	shape   *ir.Class
	literal *checker.Type
	call    func(d *demand, fn *ir.Func, args []ir.Expr) ir.Expr
}

// addForwarder records fn, a wrapper whose closures forward their calls (f), as an erased
// value whose instances demands look up, in a build that makes demands by name
// (AllInstances), outside erased code, whose demands' type arguments would mention the
// erased ones.
func (l *lowerer) addForwarder(fn *ir.Func, f *forwarder) {
	if l.specialization == AllInstances && (l.subst == nil || len(l.subst.erased) == 0) {
		l.erasedValues = append(l.erasedValues, &erasedValue{fn: fn, forward: f})
		if f.source != nil {
			if l.valueFlow.wrappers == nil {
				l.valueFlow.wrappers = map[*ast.Node]bool{}
			}
			l.valueFlow.wrappers[f.source] = true
		}
	}
}

// forwardingInstance lists, in the descriptor of a wrapper (v), its instance for a demand's
// signature: a function that runs with the wrapper's closure (Shares) and makes the call the
// wrapper forwards for that signature (forwarder.call). If that call doesn't lower, the
// wrapper lists none, and the demand's call takes the wrapper's own function, as before.
func (l *lowerer) forwardingInstance(d *demand, v *erasedValue) {
	wrapper, f := v.fn, v.forward
	outer := l.subst
	l.subst = d.subst
	defer func() { l.subst = outer }()
	fn := &ir.Func{Name: wrapper.Name + ".instance", Module: wrapper.Module, Pos: wrapper.Pos, Result: d.want.Result,
		Closure: true, Outer: wrapper.Outer, Shares: wrapper, JSName: wrapper.JSName, Captures: slices.Clone(wrapper.Captures)}
	mark := l.tentatively()
	lowered := false
	l.inFunction(fn, func() {
		if f.name != "" {
			fn.Params = append(fn.Params, l.newLocal("this", f.shape)) // an arrow function has none
		}
		args := make([]ir.Expr, len(d.want.Params))
		for i, t := range d.want.Params {
			param := l.newLocal(fmt.Sprintf("arg%d", i+1), t)
			fn.Params = append(fn.Params, param)
			args[i] = &ir.LocalRef{Local: param}
		}
		call := f.call(d, fn, args)
		if lowered = call != nil; !lowered {
			return
		}
		var stmt ir.Stmt = &ir.Return{At: fn.Pos, Value: call}
		if d.want.Result == ir.Void {
			stmt = &ir.ExprStmt{At: fn.Pos, X: call}
		}
		fn.Body = &ir.Block{At: fn.Pos, Stmts: []ir.Stmt{stmt}}
	})
	if !lowered || l.failedSince(mark) {
		l.takeBack(mark, fn)
		return
	}
	wrapper.Instances = append(wrapper.Instances, &ir.Instance{Name: f.name, Signature: d.want, Key: d.key, Func: fn})
}

// takesAs reports whether a function taking erased, an erased copy's or a wrapper's
// parameters, can be called with an instance's parameters want, as demandAt requires: the
// same, or a dynamic value where the instance's is typed, or a rest parameter of other
// elements (C170).
func takesAs(erased, want []ir.Type) bool {
	if len(erased) != len(want) {
		return false
	}
	for i, param := range want {
		if erased[i] != param && erased[i] != ir.Value && !isErasedRest(erased[i], param) {
			return false
		}
	}
	return true
}

// fromErasedTo converts value, the dynamic result of a call of an erased copy or a wrapper,
// to result, an instance's result type, with a check (D-078).
func (l *lowerer) fromErasedTo(value ir.Expr, result ir.Type, node *ast.Node) ir.Expr {
	if result == ir.Value || value == nil {
		return value
	}
	return l.dynamicConversion(value, result, node)
}

// valueForward forwards a wrapper's calls to the function value it captures in wrapped, of
// the function type erased (a generic function type's, whose values are erased copies,
// D-078): a demand's call of the instance that value lists for the demand's signature, or
// else of the value, its result converted to the demand's with a check.
func (l *lowerer) valueForward(wrapped *ir.Local, erased *ir.FuncType, source *ast.Node) func(d *demand, fn *ir.Func, args []ir.Expr) ir.Expr {
	return func(d *demand, fn *ir.Func, args []ir.Expr) ir.Expr {
		if !takesAs(erased.Params, d.want.Params) {
			return nil
		}
		otherwise := l.fromErasedTo(&ir.CallValue{Callee: &ir.LocalRef{Local: wrapped}, Args: l.erasedArguments(args, erased.Params, d.node)}, d.want.Result, d.node)
		if otherwise == nil {
			return nil
		}
		l.demands = append(l.demands, &demand{in: fn, source: source, erased: erased, signature: d.signature, subst: d.subst, want: d.want, key: d.key, node: d.node, narrowable: true, allOnly: true})
		return &ir.InstanceCall{X: &ir.LocalRef{Local: wrapped}, Signature: d.want, Key: d.key, Args: args, Otherwise: otherwise}
	}
}

// methodForward forwards a method value's calls to the generic method m (named name) of the
// object it is bound to (receiver): a demand's call of the instance the object's class lists
// for the demand's signature (for a class's method generic, a dispatched demand of its
// overrides', D-101), or that its object literal's method closure lists (by name, through
// receiverType, or for a literal's own type, its shape, D-191), or else of m, its result
// converted to the demand's with a check.
func (l *lowerer) methodForward(receiver *ir.Local, m method, generic *genericFunc, shape *ir.Class, receiverType *checker.Type) func(d *demand, fn *ir.Func, args []ir.Expr) ir.Expr {
	return func(d *demand, fn *ir.Func, args []ir.Expr) ir.Expr {
		if !takesAs(m.params, d.want.Params) {
			return nil
		}
		otherwise := l.fromErasedTo(m.call(&ir.LocalRef{Local: receiver}, l.erasedArguments(args, m.params, d.node)), d.want.Result, d.node)
		if otherwise == nil {
			return nil
		}
		inner := &demand{in: fn, signature: d.signature, subst: d.subst, want: d.want, key: d.key, node: d.node, name: m.name, method: generic, narrowable: true, allOnly: true}
		if generic == nil {
			inner.shape, inner.receiver = shape, receiverType
		}
		l.demands = append(l.demands, inner)
		return &ir.InstanceCall{X: &ir.LocalRef{Local: receiver}, Name: m.name, Signature: d.want, Key: d.key, Args: args, Otherwise: otherwise}
	}
}

// genericSignature is the one call signature of node's type, if it is generic and its
// result may hold generic class instances (holdsGenericObjects): the values such a type
// takes are erased copies whose instances typed calls look up (D-148).
func (l *lowerer) genericSignature(node *ast.Node) *checker.Signature {
	signatures := l.checker.GetSignaturesOfType(l.checker.GetNonNullableType(l.typeAt(node)), checker.SignatureKindCall)
	if len(signatures) != 1 || len(signatures[0].TypeParameters()) == 0 ||
		!l.holdsGenericObjects(l.checker.GetReturnTypeOfSignature(signatures[0]), signatures[0].TypeParameters()) {
		return nil
	}
	return signatures[0]
}

// quietly runs f, taking back what it reports: for a type that may not lower, whose
// lowering is optional.
func (l *lowerer) quietly(f func() bool) bool {
	reported := l.reportedSoFar()
	foreign := l.trial != nil && l.trial.foreign
	if f() {
		return true
	}
	l.restoreReported(reported) // a trial counts reports (tryInstance)
	if l.trial != nil {
		l.trial.foreign = foreign
	}
	return false
}

// tentative is where lowering stood before a function that may be taken back
// (takeBack): its reports, and the functions, demands and erased values made so far.
type tentative struct {
	reported
	foreign                  bool
	lowered, demands, values int
}

// tentatively marks where lowering stands, for takeBack.
func (l *lowerer) tentatively() tentative {
	return tentative{reported: l.reportedSoFar(), foreign: l.trial != nil && l.trial.foreign,
		lowered: len(l.lowered), demands: len(l.demands), values: len(l.erasedValues)}
}

// failedSince reports whether lowering reported, or failed unexplained, since t.
func (l *lowerer) failedSince(t tentative) bool {
	return l.reports != t.reports || l.unexplained != t.unexplained
}

// takeBack drops fn, made since t, as a failed trial drops its code (tryInstance): its
// reports, fn and the functions lowered in it (an adapter's nested adapters), and the
// demands and erased values made in them. What else lowering made since then stands.
func (l *lowerer) takeBack(t tentative, fn *ir.Func) {
	l.restoreReported(t.reported)
	if l.trial != nil {
		l.trial.foreign = t.foreign
	}
	in := func(f *ir.Func) bool {
		for ; f != nil; f = f.Outer {
			if f == fn {
				return true
			}
		}
		return false
	}
	l.lowered = append(l.lowered[:t.lowered], slices.DeleteFunc(slices.Clone(l.lowered[t.lowered:]), func(f *ir.Func) bool {
		if in(f) {
			l.droppedFuncs[f] = true
		}
		return in(f)
	})...)
	l.droppedFuncs[fn] = true
	for _, d := range l.demands[t.demands:] {
		d.dropped = d.dropped || in(d.in)
	}
	for _, v := range l.erasedValues[t.values:] {
		v.dropped = v.dropped || in(v.fn)
	}
}

// instantiatingAdapter adapts value, a value of a generic function type at node (an erased
// copy, or a wrapper's closure), to to, a function type that isn't generic, where node's
// context is a function type that isn't generic (`[1, 2].map(mk)`, `const t: (x: number) =>
// Box<number> = mk`): an adapter that calls the instance value lists for the type arguments
// tsc infers relating value's signature to the context's, as a demand (D-148), or else
// value, its result converted with a check, as before; and so is `f<number>`, a generic
// value given type arguments. A nil to is the instance's function type: for a callback that
// a library method calls as it is. ok is false if value isn't such a value, or the adapter
// doesn't lower (its context's result is one the instance's doesn't convert to as it is:
// `(x: number) => Box<number | string>`); then it is adapted as before.
func (l *lowerer) instantiatingAdapter(value ir.Expr, to *ir.FuncType, node *ast.Node) (x ir.Expr, ok bool) {
	return l.instantiatingAdapterSkipping(value, to, 0, node)
}

// instantiatingAdapterSkipping is instantiatingAdapter for an adapter whose first skip
// parameters value doesn't take (functionAsMethod's `this`).
func (l *lowerer) instantiatingAdapterSkipping(value ir.Expr, to *ir.FuncType, skip int, node *ast.Node) (x ir.Expr, ok bool) {
	from, isFunc := value.Type().(*ir.FuncType)
	if l.specialization != AllInstances || l.subst != nil && len(l.subst.erased) > 0 || node == nil || !isFunc || from.Result != ir.Value {
		return nil, false
	}
	node = adaptedExpression(node)
	var source *checker.Signature
	var contextual *checker.Type
	if node.Kind == ast.KindExpressionWithTypeArguments { // `f<number>`: its instantiated type is f's for those type arguments
		source, contextual = l.genericSignature(node.Expression()), l.typeAt(node)
		node = node.Expression()
	} else {
		source, contextual = l.genericSignature(node), l.checker.GetContextualType(node, checker.ContextFlagsNone)
	}
	if source == nil || contextual == nil {
		return nil, false
	}
	targets := l.checker.GetSignaturesOfType(l.checker.GetNonNullableType(contextual), checker.SignatureKindCall)
	if len(targets) != 1 || len(targets[0].TypeParameters()) > 0 {
		return nil, false
	}
	instantiated := l.checker.PtsInstantiateSignatureInContextOf(source, targets[0])
	if slices.ContainsFunc(l.checker.PtsTypeArgumentsOfSignature(instantiated), l.mentionsErased) ||
		!l.namesResult(l.checker.GetReturnTypeOfSignature(targets[0]), l.checker.GetReturnTypeOfSignature(instantiated)) {
		return nil, false
	}
	var erased, want *ir.FuncType
	if !l.quietly(func() bool {
		erased, want = l.signatureType(source, node), l.signatureType(instantiated, node)
		return erased != nil && want != nil
	}) || erased != from || !takesAs(from.Params, want.Params) {
		return nil, false // node's type isn't value's, or the instance's types don't lower
	}
	if to == nil {
		to = want
	}
	d := &demand{source: node, erased: from, signature: instantiated, subst: l.subst, want: want, node: node, narrowable: true, allOnly: true}
	if want.Result == ir.Value {
		d.key = l.instanceKey(instantiated, node) // its values are made for its type arguments (D-190)
	}
	wrapped := l.newLocal("adapted", from)
	fn := &ir.Func{Name: l.closureName("adapter", node), Module: l.function.Module, Pos: l.pos(node), Result: to.Result,
		Closure: true, Outer: l.function, Adapts: true, Captures: []*ir.Local{wrapped}}
	d.in = fn
	mark := l.tentatively()
	failed := false
	l.inFunction(fn, func() {
		for i, t := range to.Params {
			fn.Params = append(fn.Params, l.newLocal(fmt.Sprintf("arg%d", i+1), t))
		}
		args := l.adaptedArguments(fn.Params[skip:], want, &ir.FuncType{Params: to.Params[skip:], Result: to.Result}, node)
		if failed = args == nil; failed {
			return
		}
		locals := make([]*ir.Local, len(args))
		refs := make([]ir.Expr, len(args))
		for i, arg := range args {
			locals[i] = l.newLocal(fmt.Sprintf("argument%d", i+1), arg.Type())
			refs[i] = &ir.LocalRef{Local: locals[i]}
		}
		otherwise := l.fromErasedTo(&ir.CallValue{Callee: &ir.LocalRef{Local: wrapped}, Args: l.erasedArguments(refs, from.Params, node)}, want.Result, node)
		if failed = otherwise == nil; failed {
			return
		}
		call := ir.Expr(&ir.InstanceCall{X: &ir.LocalRef{Local: wrapped}, Signature: want, Key: d.key, Args: refs, Otherwise: otherwise})
		for i := len(args) - 1; i >= 0; i-- {
			call = &ir.Bind{Local: locals[i], Value: args[i], In: call}
		}
		var stmt ir.Stmt
		switch {
		case to.Result == ir.Void:
			stmt = &ir.ExprStmt{At: fn.Pos, X: call}
		case want.Result == ir.Void:
			stmt = &ir.Return{At: fn.Pos, Value: l.voidResult(call, to.Result)}
		default:
			result := l.convert(call, to.Result, node)
			if failed = result == nil; failed {
				return
			}
			stmt = &ir.Return{At: fn.Pos, Value: result}
		}
		fn.Body = &ir.Block{At: fn.Pos, Stmts: []ir.Stmt{stmt}}
	})
	if failed || l.failedSince(mark) {
		// Taken back: an adapter as before reports what doesn't fit, and a report here would
		// send the build to a fallback with fewer instances (Fallbacks).
		l.takeBack(mark, fn)
		return nil, false
	}
	l.demands = append(l.demands, d)
	return &ir.Bind{Local: wrapped, Value: value, In: &ir.MakeClosure{Func: fn, FuncType: to}}, true
}

// objectMethodForward is what a value of the method m of an object type (node, `o.m`, reads
// it), bound to its object, forwards its calls to, if m is generic and its result may hold
// generic class instances (D-194): demands by name on the object (methodForward), through
// its interface, or for a literal's own type, its shape (D-191). It is nil otherwise.
func (l *lowerer) objectMethodForward(node *ast.Node, m method) func(*ir.Local) *forwarder {
	if node.Kind != ast.KindPropertyAccessExpression {
		return nil
	}
	return l.objectMethodForwardFrom(node, l.typeAt(node.Expression()), m)
}

// objectMethodForwardFrom is objectMethodForward for a value of m that node's type is,
// read from an object of type receiverType (a pattern's `{ m }`).
func (l *lowerer) objectMethodForwardFrom(node *ast.Node, receiverType *checker.Type, m method) func(*ir.Local) *forwarder {
	if m.result != ir.Value || receiverType == nil || l.genericSignature(node) == nil {
		return nil
	}
	var shape *ir.Class
	switch t := m.receiver.(type) {
	case *ir.Class:
		if !t.Shape {
			return nil // a class's method, which genericMethodValue binds
		}
		shape = t
	case *ir.Interface:
	default:
		return nil
	}
	receiverType = l.instantiate(l.checker.GetNonNullableType(receiverType))
	return func(receiver *ir.Local) *forwarder {
		return &forwarder{call: l.methodForward(receiver, m, nil, shape, receiverType)}
	}
}

// namesResult reports whether wanted, a context's result type, is result, an instance's,
// or a union with it as a member (a promise callback's `T | PromiseLike<T>`; an array may be
// a readonly one, sameResult). A context that
// drops the result (`forEach`'s callback, `=> void`), takes any value (`=> unknown`) or
// another or wider type (`Box<number | string>`) gains nothing from the instance, whose types
// would make generic class instances the program never makes, and their bodies may not
// lower, outside any trial (review round 3's R3-1).
func (l *lowerer) namesResult(wanted, result *checker.Type) bool {
	wanted, result = l.instantiate(wanted), l.instantiate(result)
	same := func(t *checker.Type) bool { return l.sameResult(t, result) }
	return same(wanted) || wanted.Flags()&checker.TypeFlagsUnion != 0 && slices.ContainsFunc(wanted.Types(), same)
}

// sameResult reports whether a and b are one type, or arrays (readonly or not) of one
// element type (`flatMap`'s `U | ReadonlyArray<U>` for a callback returning a `U[]`).
func (l *lowerer) sameResult(a, b *checker.Type) bool {
	if a == b {
		return true
	}
	return l.checker.IsArrayType(a) && l.checker.IsArrayType(b) && l.sameResult(l.checker.GetElementTypeOfArrayType(a), l.checker.GetElementTypeOfArrayType(b))
}

// adaptedExpression is the expression whose value convert adapts at node: the operand of
// an assertion or of parentheses, an object literal property's value (`{ f: v }`, `{ v }`).
func adaptedExpression(node *ast.Node) *ast.Node {
	for {
		switch node.Kind {
		case ast.KindAsExpression, ast.KindTypeAssertionExpression, ast.KindSatisfiesExpression, ast.KindParenthesizedExpression:
			node = node.Expression()
		case ast.KindPropertyAssignment:
			node = node.Initializer()
		case ast.KindShorthandPropertyAssignment:
			node = node.Name()
		default:
			return node
		}
	}
}
