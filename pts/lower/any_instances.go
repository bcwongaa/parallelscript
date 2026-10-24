package lower

import (
	"fmt"
	"slices"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/pts/ir"
	"github.com/microsoft/typescript-go/pts/lib"
)

// tsc's C<any, …>, for a generic class C of the program, is the type `x instanceof C`
// narrows to where x's declared type doesn't name C (D-218), and the one a place gets
// where such a value flows on (`const m = x`, `[x]`, `ident(x)`, an inferred return, the
// element `find` gives with an inferred predicate) or where the program writes it
// (`b: Box<any>`). In TypeScript it holds any of C's instances; in PTS each instance is a
// class of its own (D-050), so such a place is a dynamic value, as an `unknown` narrowed
// to C is: its members are found at run time, and an instance of C goes in as it is (H10b).
// Code that needs the class itself (`new C<any>()`, a superclass, `this`, `super`) still
// gets C's instance for any (classOf).

// isAnyInstance reports whether t is tsc's C<any, …> for a generic class C of the program,
// not of the library (`ParallelArray<any>`, whose instance for any the library's own code
// uses as a class).
func (l *lowerer) isAnyInstance(t *checker.Type) bool {
	if !l.isPrototypeInstance(t) {
		return false
	}
	file := ast.GetSourceFileOfNode(l.genericClasses[l.checker.GetMergedSymbol(t.Symbol())].node).FileName()
	return !lib.IsLibPath(file) && !lib.IsModulePath(file)
}

// holdsAnyInstance reports whether node gives a value that tsc types C<any, …>
// (isAnyInstance), or an intersection irType takes as one (`S & C<any, …>`, which
// `instanceof` narrows a type parameter's value to), held as a dynamic value: not `super`,
// whose class the subclass extends, nor a reference whose instanceof narrowing implies C's
// type arguments, which typeAt gives as their instance (D-218).
func (l *lowerer) holdsAnyInstance(node *ast.Node) bool {
	node = ast.SkipParentheses(node)
	return node.Kind != ast.KindSuperKeyword && !l.classReceivers[node] && l.anyInstanceIn(l.typeAt(node)) != nil
}

// callsAnyInstanceGenericMethod reports whether call calls a generic method of C, whose
// result may hold generic class instances (`map<R>(f): Box<R>`), on a value tsc types
// C<any, …>: called through any, its erased copy would make C's instance for any where tsc
// says C<R> (R2-F5), so it is called as on C's instance for any, the receiver converted to it
// with a check (classReceivers): a TypeError for another instance, as the base did.
func (l *lowerer) callsAnyInstanceGenericMethod(call *ast.CallExpression) bool {
	callee := ast.SkipParentheses(call.Expression)
	if callee.Kind != ast.KindPropertyAccessExpression || callee.AsPropertyAccessExpression().Name().Kind == ast.KindPrivateIdentifier || !l.holdsAnyInstance(callee.Expression()) {
		return false
	}
	receiver := ast.SkipParentheses(callee.Expression())
	if l.narrowedByInstanceof(receiver) {
		return false // the object an instanceof narrowing holds (also through a local, `const m = o`, R4-F2), typically another instance: through any, as before
	}
	if declared := l.declaredTypeOfReference(receiver); declared != nil && declared.Flags()&(checker.TypeFlagsAny|checker.TypeFlagsUnknown) != 0 {
		return false // an `unknown` or `any` narrowed to C<any, …> (D-179)
	}
	declaration := l.memberDeclaration(callee)
	if declaration == nil || declaration.Kind != ast.KindMethodDeclaration || len(declaration.TypeParameters()) == 0 {
		return false
	}
	signature := l.checker.GetSignatureFromDeclaration(declaration)
	return signature != nil && l.holdsGenericObjects(l.checker.GetReturnTypeOfSignature(signature), signature.TypeParameters())
}

// anyInstanceIn is t if it is tsc's C<any, …> (isAnyInstance), or an intersection's covering
// member that is, which irType gives as a dynamic value; otherwise nil.
func (l *lowerer) anyInstanceIn(t *checker.Type) *checker.Type {
	if t.Flags()&checker.TypeFlagsIntersection != 0 {
		if member := l.coveringMember(t); member != nil {
			t = member
		}
	}
	if !l.isAnyInstance(t) {
		return nil
	}
	return t
}

// asIsConversion converts value, the dynamic value of a place tsc types C<any, …> or one of
// its members, objects or containers (`b.items` for a `b: Box<any>`), to target, a type a
// dynamic value converts to by a copy (`any[]`, a shape holding one: copiesInstanceofDynamicValue),
// as it is: the place may hold C's instance for any, whose `any[]` is one, or another
// instance, whose `number[]` would be a copy that writes miss, a TypeError instead (D-219).
func (l *lowerer) asIsConversion(value ir.Expr, target ir.Type, node *ast.Node) ir.Expr {
	converted, ok := l.dynamicConversion(value, target, node).(*ir.FromUnion)
	if !ok {
		return nil
	}
	converted.AsIs = true
	if node.Kind == ast.KindPropertyAssignment {
		node = node.Initializer() // `{ rows: b.matrix.map(f) }`'s
	}
	converted.Fresh = l.isNewArray(node) || l.isInstanceofContainer(node) // `[b.items]`, `b.matrix.map(f)`, `new Set(b.matrix)`: copied, its items as they are
	return converted
}

// convertMember converts value, a member a rest pattern or a spread reads (D-058), to target:
// as it is (asIsConversion) where the object is a C<any, …> place's (ofAnyInstance) and a
// conversion would copy it, as `b.items` is (R2-F4); otherwise as convert does.
func (l *lowerer) convertMember(value ir.Expr, target ir.Type, node *ast.Node, ofAnyInstance bool) ir.Expr {
	if ofAnyInstance && value.Type() == ir.Value && copiesInstanceObjects(target) {
		return l.asIsConversion(value, target, node)
	}
	return l.convert(value, target, node)
}

// asIsAdapter is value, a function of the program given to a method of, or stored in, a
// value of a C<any, …> place (`b.rows.forEach(addRow)`, `m.cfg.onRows = addAll` for an
// `addRow(row: any[])`), as a function of dynamic values that takes each argument its
// parameter would get as a copy as it is (asIsConversion): the instance for any's rows
// themselves, a TypeError for another instance's `number[]`, where the instance's code
// calls it (D-219). nil for a function with defaults or a rest parameter, which the caller
// reports as D-218 does.
func (l *lowerer) asIsAdapter(value ir.Expr, node *ast.Node) ir.Expr {
	from, ok := value.Type().(*ir.FuncType)
	if !ok {
		return nil
	}
	to := &ir.FuncType{Params: make([]ir.Type, len(from.Params)), Result: from.Result}
	for i, t := range from.Params {
		if t == ir.Given || isRest(t) {
			return nil
		}
		to.Params[i] = ir.Value
	}
	wrapped := l.newLocal("adapted", from)
	fn := &ir.Func{Name: l.closureName("adapter", node), Module: l.function.Module, Pos: l.pos(node), Result: to.Result,
		Closure: true, Outer: l.function, Adapts: true, Captures: []*ir.Local{wrapped}}
	failed := false
	l.inFunction(fn, func() {
		args := make([]ir.Expr, len(from.Params))
		for i, t := range from.Params {
			param := l.newLocal(fmt.Sprintf("arg%d", i+1), ir.Value)
			fn.Params = append(fn.Params, param)
			if copiesInstanceObjects(t) {
				args[i] = l.asIsConversion(&ir.LocalRef{Local: param}, t, node)
			} else {
				args[i] = l.convert(&ir.LocalRef{Local: param}, t, node)
			}
			if failed = args[i] == nil; failed {
				return
			}
		}
		call := ir.Expr(&ir.CallValue{Callee: &ir.LocalRef{Local: wrapped}, Args: args})
		if to.Result == ir.Void {
			fn.Body = &ir.Block{At: fn.Pos, Stmts: []ir.Stmt{&ir.ExprStmt{At: fn.Pos, X: call}}}
			return
		}
		fn.Body = &ir.Block{At: fn.Pos, Stmts: []ir.Stmt{&ir.Return{At: fn.Pos, Value: call}}}
	})
	if failed {
		return nil
	}
	return &ir.Bind{Local: wrapped, Value: value, In: &ir.MakeClosure{Func: fn, FuncType: to}}
}

// freshConversion is asIsConversion of value, a new container nothing else holds (the
// array a rest parameter gets from a spread: `pushRest(...b.matrix)`), whose items are
// taken as they are.
func (l *lowerer) freshConversion(value ir.Expr, target ir.Type, node *ast.Node) ir.Expr {
	converted, ok := l.asIsConversion(value, target, node).(*ir.FromUnion)
	if !ok {
		return nil
	}
	converted.Fresh = true
	return converted
}

// anyInstanceRoot is the value tsc types C<any, …> whose member node reads or calls (`b` in
// `b.rows.forEach`, `b.cfg.onRows`), node itself if it is one; nil if there is none.
func (l *lowerer) anyInstanceRoot(node *ast.Node) *ast.Node {
	for node = ast.SkipParentheses(node); ; node = ast.SkipParentheses(node.Expression()) {
		if l.holdsAnyInstance(node) {
			return node
		}
		if !ast.IsAccessExpression(node) && node.Kind != ast.KindNonNullExpression {
			return nil
		}
	}
}

// keepingAnyInstanceRoot is lower's lowering of a call or a store whose object is root or a
// member of it (anyInstanceRoot), and which gives functions (givesFunction) that
// anyInstanceFunction may test root for: a root whose evaluation may have an effect, or isn't
// a local's read (`this.bus.on(f)`, `buses[0].off(f)`, `busOf().on(f)`, a getter), is
// evaluated once, where the call or store first evaluates it, into a local both read (R4-F1).
func (l *lowerer) keepingAnyInstanceRoot(root *ast.Node, functions []*ast.Node, lower func() ir.Expr) ir.Expr {
	if root == nil || !l.mayHaveEffect(root) || !slices.ContainsFunc(functions, l.givesFunction) {
		return lower()
	}
	if _, ok := l.kept[root]; ok {
		return lower()
	}
	value := l.expression(root)
	if value == nil {
		return nil
	}
	local := l.newLocal("instance", value.Type())
	l.kept[root] = &ir.LocalRef{Local: local}
	x := lower()
	delete(l.kept, root)
	if x == nil {
		return nil
	}
	return &ir.Bind{Local: local, Value: value, In: x}
}

// givesFunction reports whether tsc types node, an argument or a stored value, as a function.
func (l *lowerer) givesFunction(node *ast.Node) bool {
	return node.Kind != ast.KindSpreadElement && len(l.checker.GetSignaturesOfType(l.checker.GetNonNullableType(l.typeAt(node)), checker.SignatureKindCall)) > 0
}

// anyInstanceFunction is value, a function of the program given to a method of, or stored in,
// a member of root, a value tsc types C<any, …> (R2-F2, R3-F1): as it is where root is C's
// instance for any, whose code takes the function's own types, keeping its identity (`off(f)`
// finds it), else the as-is adapter, which takes another instance's objects as they are or
// throws; one asIsAdapter can't make (defaults, a rest parameter) goes as through any (D-075).
// root is read again for the test: a local, or what keepingAnyInstanceRoot keeps of it.
func (l *lowerer) anyInstanceFunction(value ir.Expr, node, root *ast.Node) ir.Expr {
	adapted := func(fn ir.Expr) ir.Expr {
		if adapter := l.asIsAdapter(fn, node); adapter != nil {
			return adapter
		}
		return fn
	}
	if root == nil {
		return adapted(value)
	}
	rootValue := l.expression(root)
	class := l.classOf(l.anyInstanceIn(l.typeAt(root)), root)
	if rootValue == nil || !isPure(rootValue) || class == nil {
		return adapted(value)
	}
	fn := l.newLocal("callback", value.Type())
	ref := &ir.LocalRef{Local: fn}
	isAnyInstance := &ir.InstanceOf{X: box(rootValue), Class: class}
	return &ir.Bind{Local: fn, Value: value, In: &ir.Conditional{Cond: isAnyInstance, Then: box(ref), Else: box(adapted(ref))}}
}
