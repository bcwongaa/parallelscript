package lower

import (
	"fmt"
	"slices"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/internal/scanner"
	"github.com/microsoft/typescript-go/pts/ir"
)

// Values of type `any` are dynamic values, ir.Value (D-070): their properties and
// elements are found at run time (DynamicGet, DynamicSet), operators apply JS's
// conversions to them, and converting one to a typed location checks it.

// isAny reports whether tsc types node as any, or as a generic class's C<any, …>, which
// holds any of C's instances as a dynamic value (holdsAnyInstance).
func (l *lowerer) isAny(node *ast.Node) bool {
	t := l.typeAt(node)
	return t.Flags()&checker.TypeFlagsAny != 0 || l.isRuntimeObject(t) || l.holdsAnyInstance(node)
}

// isDynamicSource reports whether tsc types node as any or unknown: a value nothing but
// the program's logic vouches for, which a conversion checks.
func (l *lowerer) isDynamicSource(node *ast.Node) bool {
	return node != nil && l.typeAt(node).Flags()&(checker.TypeFlagsAny|checker.TypeFlagsUnknown) != 0
}

// storedDynamic reports whether node reads a value kept as a dynamic value whatever tsc
// has narrowed it to: a variable or parameter declared `any` or `unknown` (`if (x
// instanceof Map)`), a member of a dynamic value or of one that stays dynamic though tsc
// narrowed it (isNarrowedDynamic) or what a method of the latter returns (`u.slice(1)`),
// or the value of `=` on one of these (`(o.m = new Map()).size`, D-179).
func (l *lowerer) storedDynamic(node *ast.Node) bool {
	node = ast.SkipParentheses(node)
	if l.isInstanceofContainer(node) {
		return true // `[...c.matrix]`, `new Set(c.matrix)` (C285)
	}
	switch node.Kind {
	case ast.KindIdentifier:
		symbol := l.checker.GetSymbolAtLocation(node)
		if symbol == nil {
			return false
		}
		declared := l.checker.GetTypeOfSymbol(symbol)
		if declared.Flags()&(checker.TypeFlagsAny|checker.TypeFlagsUnknown) != 0 || l.isInstanceofDynamicLocalRef(node) {
			return true
		}
		// In an instance, a variable of a type parameter's type holds its type argument's
		// values, dynamic ones where its IR type is (`x: T` for unknown or a Promise<number>).
		if l.subst == nil || !l.subst.instance {
			return false
		}
		instantiated := l.typeOfSymbol(symbol)
		return instantiated != declared && l.isDynamicIRType(instantiated, node)
	case ast.KindPropertyAccessExpression, ast.KindElementAccessExpression:
		// `u[1]` of a narrowed `u: unknown`; `c.next` of a `c` narrowed by instanceof, which
		// tsc types C<any, …> (isInstanceofDynamicMember)
		return l.isDynamicSource(node.Expression()) || l.isNarrowedDynamic(node.Expression()) || l.isInstanceofDynamicMember(node)
	case ast.KindBinaryExpression:
		expr := node.AsBinaryExpression()
		return expr.OperatorToken.Kind == ast.KindEqualsToken && l.storedDynamic(expr.Left)
	case ast.KindNonNullExpression:
		return l.storedDynamic(node.Expression()) // `m.get(k)!`
	case ast.KindCallExpression: // the result of a method of a narrowed dynamic value
		if l.callsAnyInstanceGenericMethod(node.AsCallExpression()) {
			return false // `b.map(f)` called as on C's instance for any: of tsc's type (H10b)
		}
		callee := ast.SkipParentheses(node.AsCallExpression().Expression)
		return ast.IsAccessExpression(callee) && l.isNarrowedDynamic(callee.Expression()) || l.isInstanceofDynamicMember(node)
	}
	return false
}

// trustsDynamic reports whether x, a dynamic value that tsc types as a union of other
// types, holds only values of that union as it is, so that it goes into a place of the
// union unchecked: an element that the own method of an array or map of typed elements
// reads (`pop`, `get`, `find`). Any other dynamic value goes there with a check, as into other typed
// places (D-192): a call through a union of object types' erased result (D-191), a value
// declared `any` or `unknown` that a type guard narrowed, wherever it is held, and either
// inside `||`, a comma or a conditional; an erased copy's object may hold dynamic values
// where tsc says typed ones (C187).
func (l *lowerer) trustsDynamic(x ir.Expr) bool {
	switch x := x.(type) {
	case *ir.Bind:
		return l.trustsDynamic(x.In)
	case *ir.ArrayCall:
		array, ok := x.Args[0].Type().(*ir.Array)
		return ok && !holdsDynamicValues(array.Elem, map[ir.Type]bool{}) // not an `unknown[]`'s element
	case *ir.MapCall:
		return x.Map != nil && !holdsDynamicValues(x.Map.Key, map[ir.Type]bool{}) && (x.Map.Value == nil || !holdsDynamicValues(x.Map.Value, map[ir.Type]bool{}))
	case *ir.UndefinedConst, *ir.NullConst:
		return true
	case *ir.Conditional:
		return l.trustsDynamic(x.Then) && l.trustsDynamic(x.Else)
	case *ir.Call:
		array, ok := x.Args[0].Type().(*ir.Array)
		if !ok || holdsDynamicValues(array.Elem, map[ir.Type]bool{}) {
			return false // an `unknown[]`'s element, which a type-guard callback narrowed
		}
		for key, helper := range l.helpers {
			if helper == x.Func {
				method := baseMethod(key.method)
				return method == "find" || method == "findLast"
			}
		}
	}
	return false
}

// isCheckable reports whether a dynamic value converts to t with a check the runtime
// makes (cgen's valueTest): a primitive, an integer type, a class, or a union of them.
// Interfaces, arrays and the rest wait for M3.3b's runtime type checks.
func isCheckable(t ir.Type) bool {
	switch {
	case t == ir.Number || t == ir.String || t == ir.Bool || t == ir.Null || t == ir.Undefined || ir.IsNumeric(t):
		return true
	case isClass(t):
		return !t.(*ir.Class).Shape // a shape's objects are checked by M3.3b's descriptors
	case isUnion(t):
		for _, member := range t.(*ir.Union).Members {
			if !isCheckable(member) {
				return false
			}
		}
		return true
	}
	return false
}

// dynamicKey is the key of `o.name` or `o[k]` as a dynamic value.
func (l *lowerer) dynamicKey(node *ast.Node) ir.Expr {
	if node.Kind == ast.KindPropertyAccessExpression {
		return box(&ir.StringConst{Value: node.AsPropertyAccessExpression().Name().Text()})
	}
	key := l.valueExpression(node.AsElementAccessExpression().ArgumentExpression)
	if key == nil {
		return nil
	}
	return box(key)
}

// wantDynamicMember makes the generic methods that `o.name` or `o[k]` through `any` may
// find, a method call or a method read as a value, have erased copies in their classes'
// method tables (D-078): those named name, or k if it is a literal, or else all.
func (l *lowerer) wantDynamicMember(node *ast.Node) {
	if node.Kind == ast.KindPropertyAccessExpression {
		l.wantInMethodTables(node.AsPropertyAccessExpression().Name().Text())
		return
	}
	if key := ast.SkipParentheses(node.AsElementAccessExpression().ArgumentExpression); ast.IsStringLiteralLike(key) {
		l.wantInMethodTables(key.Text())
		return
	}
	for _, generic := range l.genericMethodList {
		l.wantInMethodTables(generic.node.Name().Text())
	}
	l.allMethodsInTables = true
}

// dynamicRead lowers `o.name` or `o[k]` for an o of type any: the property found at run time.
func (l *lowerer) dynamicRead(node *ast.Node) ir.Expr {
	if node.Kind == ast.KindPropertyAccessExpression {
		if x, ok := l.familyPrivateField(node); ok {
			return x // `Number(o.#v)` (C274)
		}
	}
	l.wantDynamicMember(node)
	object := l.expression(node.Expression())
	key := l.dynamicKey(node)
	if object == nil || key == nil {
		return nil
	}
	return &ir.CallBuiltin{Builtin: ir.DynamicGet, Args: []ir.Expr{box(object), key}}
}

// dynamicPlace is the place `o.name` or `o[k]` names for an o of type any.
func (l *lowerer) dynamicPlace(node *ast.Node) (place, bool) {
	object := l.expression(node.Expression())
	if object == nil {
		return place{}, false
	}
	return l.valuePlace(node, object)
}

// missingProperty reads the property name of object, whose type lacks it, as a pattern or
// a spread does where a narrowing added it (`"k" in o`, a discriminant narrowed through a
// type parameter's constraint): found at run time, as valueProperty reads `o.k`.
func (l *lowerer) missingProperty(object ir.Expr, name string) ir.Expr {
	l.wantInMethodTables(name)
	return &ir.CallBuiltin{Builtin: ir.DynamicGet, Args: []ir.Expr{box(object), box(&ir.StringConst{Value: name})}}
}

// missingOwnProperty is missingProperty for a spread or a rest element, which copy own
// properties only: undefined where object holds name on its prototype (a class's getter or
// method, which `in` finds). object is pure.
func (l *lowerer) missingOwnProperty(object ir.Expr, name string) ir.Expr {
	return &ir.Conditional{
		Cond: &ir.CallBuiltin{Builtin: ir.ValueHasOwn, Args: []ir.Expr{box(object), box(&ir.StringConst{Value: name})}},
		Then: l.missingProperty(object, name),
		Else: box(&ir.UndefinedConst{}),
	}
}

// valuePlace is the place `o.name` or `o[k]` (node) names in object, written by name at run
// time with a check (D-072): object is a dynamic value, or an object whose type has no such
// property, which a narrowing added (`"k" in o`, a discriminant narrowed through a type
// parameter's constraint), as valueProperty reads it.
func (l *lowerer) valuePlace(node *ast.Node, object ir.Expr) (place, bool) {
	key := l.dynamicKey(node)
	if key == nil {
		return place{}, false
	}
	if l.firstDynamicWrite == nil {
		l.firstDynamicWrite = node
	}
	return place{object: box(object), key: key, dynamic: true}, true
}

// dynamicCall lowers a call whose callee is of type any, or is a method of a value of type
// any (D-074): DynamicCall or DynamicCallMethod, with the arguments as dynamic values.
func (l *lowerer) dynamicCall(call *ast.CallExpression, callee *ast.Node) ir.Expr {
	if l.firstDynamicCall == nil {
		l.firstDynamicCall = call.AsNode()
	}
	if x, ok := l.familyPrivateCall(call, callee); ok {
		return x
	}
	if callee.Kind == ast.KindPropertyAccessExpression && functionMethods[callee.AsPropertyAccessExpression().Name().Text()] {
		if x, ok := l.dynamicMethodWithThis(call, callee, ast.SkipParentheses(callee.Expression())); ok {
			return x // `o.m.call(other)` through any: o's method on other (R2-F6)
		}
	}
	// A method of an array that erased code makes (`[x].slice(1)`, `new Array<T>(n).fill(x)`),
	// whose signature mentions an erased type parameter, is found at run time too.
	if ast.IsAccessExpression(callee) && (l.isAny(callee.Expression()) || l.isNarrowedDynamic(callee.Expression()) || l.isMixedUnionReceiver(callee) || callee.Kind == ast.KindPropertyAccessExpression && l.isArrayExpression(callee.Expression())) {
		object := l.expression(callee.Expression())
		if object == nil {
			return nil
		}
		return l.dynamicMethodCall(call, callee, object)
	}
	function := l.expression(callee)
	if function == nil {
		return nil
	}
	return l.dynamicCallWith(call, callee, ir.DynamicCall, []ir.Expr{box(function)})
}

// dynamicMethodCall lowers `o.m(args)` (callee) for object, the lowered o, a dynamic value
// or one whose member m is found at run time: DynamicCallMethod.
func (l *lowerer) dynamicMethodCall(call *ast.CallExpression, callee *ast.Node, object ir.Expr) ir.Expr {
	if l.firstDynamicCall == nil {
		l.firstDynamicCall = call.AsNode()
	}
	l.wantDynamicMember(callee)
	key := l.dynamicKey(callee)
	if key == nil {
		return nil
	}
	if found, ok := l.kept[callee]; ok { // found before a yield in the arguments (keepCallee)
		return l.dynamicCallWith(call, callee, ir.DynamicCallFound, []ir.Expr{box(object), key, found})
	}
	return l.dynamicCallWith(call, callee, ir.DynamicCallMethod, []ir.Expr{box(object), key})
}

// dynamicCallWith is the dynamic call builtin with args, the callee's, then its text and
// the call's arguments as dynamic values.
func (l *lowerer) dynamicCallWith(call *ast.CallExpression, callee *ast.Node, builtin *ir.Builtin, args []ir.Expr) ir.Expr {
	args = append(args, &ir.StringConst{Value: scanner.GetTextOfNode(callee)})
	for i, argument := range call.Arguments.Nodes {
		if argument.Kind == ast.KindSpreadElement {
			if l.copiesSpreadCallbacks(callee, argument) {
				return nil
			}
			items := l.dynamicSpread(argument)
			if items == nil {
				return nil
			}
			args = append(args, &ir.SpreadArg{X: items})
			continue
		}
		value := l.valueExpression(argument)
		if value == nil {
			return nil
		}
		if value = l.instanceCallbackArgument(call, callee, i, argument, value); value == nil {
			return nil
		}
		args = append(args, box(value))
	}
	return &ir.CallBuiltin{Builtin: builtin, Args: args}
}

// dynamicSpread is what a spread argument of a call through `any` passes, whose elements
// the runtime spreads at the call: an array as it is, or anything else as ValueSpreadItems
// hands it over (an array, tuple or typed array as it is; a string's, set's, map's,
// iterator's or other iterable's items taken into a new array where the spread is, D-078).
func (l *lowerer) dynamicSpread(argument *ast.Node) ir.Expr {
	value := l.valueExpression(argument.Expression())
	if value == nil || isArray(value.Type()) {
		return value
	}
	return &ir.CallBuiltin{Builtin: ir.ValueSpreadItems, Args: []ir.Expr{box(value), &ir.StringConst{Value: scanner.GetTextOfNode(argument.Expression())}}}
}

// dynamicOperation lowers a binary operator with an operand of type any, as JS applies
// it: + adds numbers or concatenates (ValueAdd) where tsc doesn't type the result as a
// string; the other arithmetic, bitwise and shift operators convert both operands to
// numbers (ValueToNumber); the comparisons follow JS's rules (ValueLess, ValueLessEqual,
// with the operands swapped for > and >=, evaluated in order); === and !== compare
// dynamic values. ok is false for the operators it leaves to operation.
func (l *lowerer) dynamicOperation(node *ast.Node, op ir.BinaryOp, x ir.Expr, y ir.Expr) (result ir.Expr, ok bool) {
	switch op {
	case ir.Eq, ir.Ne:
		return unionEquality(op, x, y), true
	case ir.Add:
		return &ir.CallBuiltin{Builtin: ir.ValueAdd, Args: []ir.Expr{box(x), box(y)}}, true
	case ir.Lt:
		return &ir.CallBuiltin{Builtin: ir.ValueLess, Args: []ir.Expr{box(x), box(y)}}, true
	case ir.Le:
		return &ir.CallBuiltin{Builtin: ir.ValueLessEqual, Args: []ir.Expr{box(x), box(y)}}, true
	case ir.Gt, ir.Ge:
		left := l.newLocal("left", ir.Value)
		builtin := ir.ValueLess
		if op == ir.Ge {
			builtin = ir.ValueLessEqual
		}
		return &ir.Bind{Local: left, Value: box(x), In: &ir.CallBuiltin{Builtin: builtin, Args: []ir.Expr{box(y), &ir.LocalRef{Local: left}}}}, true
	case ir.Concat:
		return nil, false
	}
	return &ir.Binary{Op: op, X: toNumber(x), Y: toNumber(y)}, true
}

// toNumber is JS's ToNumber of x: x itself for a number, a number for another numeric type.
func toNumber(x ir.Expr) ir.Expr {
	switch {
	case x.Type() == ir.Number:
		return x
	case ir.IsNumeric(x.Type()):
		return &ir.Convert{X: x, To: ir.Number}
	}
	return &ir.CallBuiltin{Builtin: ir.ValueToNumber, Args: []ir.Expr{box(x)}}
}

// dynamicIncrement lowers ++ and -- on a place of type any: the place gets ToNumber of
// its value plus or minus one, and the expression is the new number, or if postfix, the old.
func (l *lowerer) dynamicIncrement(target place, op ir.BinaryOp, postfix bool) ir.Expr {
	return l.readAndStore(target, func(target place) ir.Expr {
		old := l.newLocal("old", ir.Number)
		next := &ir.Binary{Op: op, X: &ir.LocalRef{Local: old}, Y: &ir.NumberConst{Value: 1}}
		var result ir.Expr = next
		if postfix {
			result = &ir.LocalRef{Local: old}
		}
		return &ir.Bind{Local: old, Value: toNumber(target.read()), In: &ir.Sequence{X: target.store(box(next), false), Y: result}}
	})
}

// dynamicConversion converts value, a dynamic value from an expression tsc types as any or
// unknown, to target with a check (D-070), or for object types a check and perhaps a copy
// (D-071); it reports (at node) the targets it can't convert to yet.
func (l *lowerer) dynamicConversion(value ir.Expr, target ir.Type, node *ast.Node) ir.Expr {
	if what := l.prepareDynamicTarget(target, node, map[ir.Type]bool{}); what != "" {
		l.notSupportedYet(node, fmt.Sprintf("converting %s to %s (%s) is", l.checker.TypeToString(l.typeAt(node)), target, what))
		return nil
	}
	return &ir.FromUnion{X: value, To: target}
}

// prepareDynamicTarget readies t, and the types of its properties and elements, to take
// dynamic values converted to them: each interface gets a shape that dynamic data is
// copied into (Interface.Copy, with its properties in declaration order). It returns what
// a dynamic value can't be converted to yet, or "".
func (l *lowerer) prepareDynamicTarget(t ir.Type, node *ast.Node, seen map[ir.Type]bool) string {
	if seen[t] || isCheckable(t) || t == ir.Value {
		return ""
	}
	seen[t] = true
	switch t := t.(type) {
	case *ir.Union:
		for _, member := range t.Members {
			if what := l.prepareDynamicTarget(member, node, seen); what != "" {
				return what
			}
		}
		return ""
	case *ir.Array:
		return l.prepareDynamicTarget(t.Elem, node, seen)
	case *ir.MapType:
		if what := l.prepareDynamicTarget(t.Key, node, seen); what != "" || t.Value == nil {
			return what
		}
		return l.prepareDynamicTarget(t.Value, node, seen)
	case *ir.Class: // a shape or tuple; classes are checkable
		for _, field := range t.Fields {
			if what := l.prepareDynamicTarget(field.Type, node, seen); what != "" {
				return what
			}
		}
		return ""
	case *ir.Interface:
		for _, field := range t.Fields {
			if what := l.prepareDynamicTarget(field.Type, node, seen); what != "" {
				return what
			}
		}
		if len(t.Methods) > 0 {
			return "" // only objects with the methods convert to it; dynamic data has none (D-075)
		}
		return l.copyShape(t, node)
	case *ir.FuncType:
		return "" // other closures are adapted (D-075)
	}
	return "a " + t.String()
}

// copyShape gives an interface the shape its copies of dynamic data take: the shape of
// the object type it is, with its properties in the type's declaration order. An object
// literal's type with accessors doesn't do: its shape holds the getters' and setters'
// closures, not values (D-179).
func (l *lowerer) copyShape(iface *ir.Interface, node *ast.Node) string {
	if iface.Copy != nil {
		return ""
	}
	if what := l.makeCopyShape(iface, node); what != "" && !l.copyShapesOptional {
		return what
	}
	return ""
}

// makeCopyShape gives iface the shape of an object type it is (copyShape), or says why it can't.
func (l *lowerer) makeCopyShape(iface *ir.Interface, node *ast.Node) string {
	accessors := false
	for checkerType, candidate := range l.interfacesByType {
		if candidate != iface {
			continue
		}
		if len(l.literalAccessors(checkerType)) > 0 {
			accessors = true
			continue
		}
		shape := l.shape(checkerType, node)
		if shape == nil {
			return "an object type without a shape"
		}
		var order []*ir.Field
		for _, property := range l.checker.GetPropertiesOfType(checkerType) {
			order = append(order, shape.Field(property.Name))
		}
		iface.Copy, iface.CopyOrder = shape, shape.Order(order)
		return ""
	}
	if accessors {
		return "an object type known only from object literals with getters or setters"
	}
	return "an object type of no declaration"
}

// prepareCheckedUnion readies u, as prepareDynamicTarget does, for a dynamic value that goes
// into a place of it with a check, and reports whether it could: an object type of no copy
// shape (one known only from object literals with getters) is left without one, so that
// objects convert to it as they are and dynamic data doesn't (D-192).
func (l *lowerer) prepareCheckedUnion(u *ir.Union, node *ast.Node) bool {
	l.copyShapesOptional = true
	defer func() { l.copyShapesOptional = false }()
	return l.prepareDynamicTarget(u, node, map[ir.Type]bool{}) == ""
}

// prepareDynamicTypes readies the types that dynamic data may be converted to at run time
// without a conversion in the program: in a program that writes through `any`, the
// declared types of every class's and shape's fields and of the arrays and hash-map
// objects it makes, which a write converts to (D-072), and in one that calls through
// `any`, every function's parameter types, which a call converts to (D-074), and the
// types of iterator results' properties, which reads by name convert to (D-178). Each
// interface among them gets the shape its copies take. Types that dynamic values can't be
// converted to yet are left as they are, and converting to them throws.
func (l *lowerer) prepareDynamicTypes(funcs []*ir.Func) {
	seen := map[ir.Type]bool{}
	if l.firstDynamicCall != nil {
		for _, f := range funcs {
			for _, param := range f.Params {
				if rest, ok := param.Type.(*ir.Rest); ok {
					l.prepareDynamicTarget(rest.Array, l.firstDynamicCall, seen)
				} else if param.Type != ir.Given {
					l.prepareDynamicTarget(param.Type, l.firstDynamicCall, seen)
				}
			}
		}
	}
	if l.firstResult != nil { // a result's done and value found by name on an object of another layout are converted to their types (D-178)
		for _, shape := range l.shapeList {
			if shape.Result {
				l.prepareDynamicTarget(shape, l.firstResult, seen)
			}
		}
		for _, iface := range l.interfaceList {
			if iface.Result {
				l.prepareDynamicTarget(iface.Fields[0].Type, l.firstResult, seen)
			}
		}
	}
	if l.firstDynamicWrite == nil {
		return
	}
	prepare := func(t ir.Type) { l.prepareDynamicTarget(t, l.firstDynamicWrite, seen) }
	for _, class := range slices.Concat(l.classList, l.instanceClasses) {
		for _, field := range class.Fields {
			prepare(field.Type)
		}
	}
	for i := 0; i < len(l.shapeList); i++ { // copy shapes are added as it goes
		for _, field := range l.shapeList[i].Fields {
			prepare(field.Type)
		}
	}
	for _, f := range funcs {
		ir.Walk(f.Body.Stmts, func(ir.Stmt) {}, func(x ir.Expr) {
			switch x := x.(type) {
			case *ir.NewArray:
				prepare(x.Array)
			case *ir.NewMap:
				if x.Map.Record {
					prepare(x.Map)
				}
			}
		})
	}
}

// dynamicWithDefault applies a pattern's default to part, a dynamic value (an any's
// property): the default if it is undefined, else part, converted to the name's type.
func (l *lowerer) dynamicWithDefault(part ir.Expr, initializer *ast.Node, name *ast.Node) ir.Expr {
	t := ir.Type(ir.Value)
	if name.Kind == ast.KindIdentifier {
		if t = l.bindingType(l.checker.GetSymbolAtLocation(name), name); t == nil {
			return nil
		}
	}
	fallback := l.expression(initializer)
	if fallback == nil {
		return nil
	}
	value := l.newLocal("property", ir.Value)
	ref := &ir.LocalRef{Local: value}
	fallback, present := l.convert(fallback, t, initializer), l.convert(ref, t, name)
	if fallback == nil || present == nil {
		return nil
	}
	return &ir.Bind{Local: value, Value: part, In: &ir.Conditional{Cond: &ir.IsNullish{X: ref, Only: ir.Undefined}, Then: fallback, Else: present}}
}
