package lower

import (
	"fmt"
	"slices"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/pts/ir"
)

// The functions of the global Object beyond hash-map objects (D-086): keys, values,
// entries and hasOwn of any object, assign, freeze, isFrozen, is and fromEntries.

// objectFunctionCall lowers Object.assign, Object.freeze, Object.isFrozen, Object.is
// and Object.fromEntries; handled is false for any other call.
func (l *lowerer) objectFunctionCall(call *ast.CallExpression) (x ir.Expr, handled bool) {
	callee := ast.SkipParentheses(call.Expression)
	if !l.isObjectStatic(callee, "assign", "freeze", "isFrozen", "is", "fromEntries") {
		return nil, false
	}
	if callee.Name().Text() != "assign" && l.hasSpreadArgument(call) {
		return nil, true
	}
	switch callee.Name().Text() {
	case "assign":
		return l.objectAssign(call), true
	case "freeze": // Readonly<T> to tsc; writes to the object then throw (D-157)
		x := l.expression(call.Arguments.Nodes[0])
		t := l.irType(l.typeAt(call.AsNode()), call.AsNode())
		if x == nil || t == nil {
			return nil, true
		}
		frozen := l.newLocal("frozen", x.Type())
		ref := &ir.LocalRef{Local: frozen}
		result := l.convert(ref, t, call.AsNode())
		if result == nil {
			return nil, true
		}
		return &ir.Bind{Local: frozen, Value: x, In: &ir.Sequence{X: &ir.CallBuiltin{Builtin: ir.ValueFreeze, Args: []ir.Expr{box(ref)}}, Y: result}}, true
	case "isFrozen":
		x := l.valueExpression(call.Arguments.Nodes[0])
		if x == nil {
			return nil, true
		}
		return &ir.CallBuiltin{Builtin: ir.ValueIsFrozen, Args: []ir.Expr{box(x)}}, true
	case "is":
		operands := l.expressions(call.Arguments.Nodes)
		if operands == nil {
			return nil, true
		}
		return &ir.CallBuiltin{Builtin: ir.ValueSameValue, Args: []ir.Expr{box(operands[0]), box(operands[1])}}, true
	}
	return l.objectFromEntries(call), true
}

// typedObjectStatic lowers Object.keys, values, entries and hasOwn of a value that isn't a
// hash-map object: its own enumerable properties as JS lists them (class instances'
// fields in their constructor's order, a literal's in its order, an array's indices),
// read through its dynamic value; values and entries convert to the type tsc gives them.
func (l *lowerer) typedObjectStatic(call *ast.CallExpression, name string) ir.Expr {
	args := call.Arguments.Nodes
	if l.hasSpreadArgument(call) {
		return nil
	}
	x := l.valueExpression(args[0])
	if x == nil {
		return nil
	}
	if x.Type() == ir.Void {
		l.notSupportedYet(args[0], fmt.Sprintf("`Object.%s` of nothing is", name))
		return nil
	}
	object := box(x)
	switch name {
	case "keys":
		return &ir.CallBuiltin{Builtin: ir.ValueKeyArray, Args: []ir.Expr{object}}
	case "hasOwn":
		key := l.expression(args[1])
		if key == nil {
			return nil
		}
		return &ir.CallBuiltin{Builtin: ir.ValueHasOwn, Args: []ir.Expr{object, box(key)}}
	}
	builtin := ir.ValueValues
	if name == "entries" {
		builtin = ir.ValueEntries
	}
	t := l.irType(l.typeAt(call.AsNode()), call.AsNode())
	if t == nil {
		return nil
	}
	return l.dynamicConversion(&ir.CallBuiltin{Builtin: builtin, Args: []ir.Expr{object}}, t, call.AsNode())
}

// objectAssign lowers Object.assign(target, ...sources): each source's own enumerable
// properties written to the target through its dynamic value, which checks them against
// the target's declared types (TypeError for a property the target doesn't have); its
// value is the target, as its own type.
func (l *lowerer) objectAssign(call *ast.CallExpression) ir.Expr {
	args := call.Arguments.Nodes
	if target := ast.SkipParentheses(args[0]); target.Kind == ast.KindObjectLiteralExpression && len(target.AsObjectLiteralExpression().Properties.Nodes) == 0 {
		return l.assignToNewObject(call)
	}
	target := l.expression(args[0])
	if target == nil {
		return nil
	}
	var values []ir.Expr
	var at []*ast.Node
	for _, argument := range args[1:] {
		lowered := l.argumentValues(argument)
		if lowered == nil {
			return nil
		}
		for _, value := range lowered {
			values, at = append(values, value), append(at, argument)
		}
	}
	sources := l.restArray(values, at, ir.ValueArray)
	if sources == nil {
		return nil
	}
	local := l.newLocal("target", target.Type())
	ref := &ir.LocalRef{Local: local}
	assign := &ir.CallBuiltin{Builtin: ir.ObjectAssign, Args: []ir.Expr{box(ref), sources}}
	return &ir.Bind{Local: local, Value: target, In: &ir.Sequence{X: assign, Y: ref}}
}

// assignToNewObject lowers `Object.assign({}, ...sources)` as the object literal
// `{ ...sources }` it is equivalent to, an object of the shape of the type tsc gives it.
func (l *lowerer) assignToNewObject(call *ast.CallExpression) ir.Expr {
	node := call.AsNode()
	sources := call.Arguments.Nodes[1:]
	if i := slices.IndexFunc(sources, isSpreadElement); i >= 0 {
		l.notSupportedYet(sources[i], "spread arguments of `Object.assign({}, …)` are")
		return nil
	}
	t := l.typeAt(node)
	if !l.isObjectType(t) {
		l.notSupportedYet(node, fmt.Sprintf("`Object.assign({}, …)` of type %s is", l.checker.TypeToString(t)))
		return nil
	}
	shape := l.shape(t, node)
	if shape == nil {
		return nil
	}
	var parts []spreadProperty
	for _, argument := range sources {
		part := spreadProperty{node: argument, source: l.expression(argument)}
		if part.source == nil {
			return nil
		}
		var ok bool
		if part.names, ok = l.spreadNames(part.source, l.typeAt(argument), argument); !ok {
			return nil
		}
		parts = append(parts, part)
	}
	return l.spreadObject(parts, shape, node)
}

// objectFromEntries lowers Object.fromEntries(pairs) of an array of [key, value] tuples
// (or a map's entries): a hash-map object with each pair's entry, in order, keys
// converted to strings.
func (l *lowerer) objectFromEntries(call *ast.CallExpression) ir.Expr {
	node := call.AsNode()
	t := l.irType(l.typeAt(node), node)
	if t == ir.Value {
		t = l.recordOf(ir.Value) // of pairs tsc types as arrays (`[key(x), x]` in a callback), it is any
	}
	record, ok := t.(*ir.MapType)
	if !ok || !record.Record {
		l.notSupportedYet(node, "this `Object.fromEntries` is")
		return nil
	}
	var pairs ir.Expr
	if it, isMap := l.mapIterableOf(call.Arguments.Nodes[0]); isMap {
		if it.m == nil {
			return nil
		}
		pairs = l.mapItemsArray(mapIterable{it.m, mapEntries}, call.Arguments.Nodes[0])
	} else {
		pairs = l.iterable(call.Arguments.Nodes[0])
	}
	if pairs == nil {
		return nil
	}
	array, ok := pairs.Type().(*ir.Array)
	if !ok || !isArray(array.Elem) && (!isTuple(array.Elem) || len(array.Elem.(*ir.Class).Fields) < 2) {
		l.notSupportedYet(call.Arguments.Nodes[0], fmt.Sprintf("`Object.fromEntries` of a %s is", pairs.Type()))
		return nil
	}
	helper := l.fromEntriesHelper(array, record, node)
	if helper == nil {
		return nil
	}
	return &ir.Call{Func: helper, Args: []ir.Expr{pairs}}
}

// pairParts are the key and value of element, a [key, value] tuple or an array, whose
// elements past its end are undefined as in JS.
func pairParts(element ir.Expr) (key ir.Expr, value ir.Expr) {
	if tuple, ok := element.Type().(*ir.Class); ok {
		return &ir.FieldRef{X: element, Field: tuple.Fields[0]}, &ir.FieldRef{X: element, Field: tuple.Fields[1]}
	}
	part := func(i float64) ir.Expr {
		return &ir.ArrayCall{Op: ir.ArrayElement, Args: []ir.Expr{element, &ir.NumberConst{Value: i}}}
	}
	return part(0), part(1)
}

// fromEntriesHelper returns the helper that makes a hash-map object of type record from
// an array of pairs of type pairs, making it the first time.
func (l *lowerer) fromEntriesHelper(pairs *ir.Array, record *ir.MapType, node *ast.Node) *ir.Func {
	key := helperKey{method: "Object.fromEntries", array: pairs, collection: record}
	if helper, ok := l.helpers[key]; ok {
		return helper
	}
	h := &helperBuilder{l: l, key: key, node: node}
	h.fn = &ir.Func{Name: "Object.fromEntries", Module: l.function.Module, Pos: l.pos(node), Result: record}
	l.inFunction(h.fn, func() {
		h.array = l.newLocal("pairs", pairs)
		h.fn.Params = []*ir.Local{h.array}
		result := l.newLocal("result", record)
		h.fn.Body = &ir.Block{At: h.fn.Pos, Stmts: h.loop(false, []ir.Stmt{h.declare(result, &ir.NewMap{Map: record})}, func(element, _ ir.Expr) []ir.Stmt {
			key, value := pairParts(element)
			name := l.toString(key, node)
			value = l.convert(value, record.Value, node)
			if name == nil || value == nil {
				h.failed = true
				return nil
			}
			return []ir.Stmt{h.stmt(&ir.MapCall{Op: ir.MapPut, Map: record, Args: []ir.Expr{&ir.LocalRef{Local: result}, name, value}})}
		}, h.ret(&ir.LocalRef{Local: result}))}
	})
	if h.failed {
		l.helpers[key] = nil
		return nil
	}
	l.helpers[key] = h.fn
	return h.fn
}

// hasOwnPropertyCall reports whether call is `o.hasOwnProperty(k)` for an object o that
// isn't a hash-map object, or `Object.prototype.hasOwnProperty.call(o, k)`, and returns
// o and k.
func (l *lowerer) hasOwnPropertyCall(call *ast.CallExpression) (object *ast.Node, key *ast.Node, ok bool) {
	callee := ast.SkipParentheses(call.Expression)
	args := call.Arguments.Nodes
	if callee.Kind != ast.KindPropertyAccessExpression || slices.ContainsFunc(args, ast.IsSpreadElement) {
		return nil, nil, false
	}
	switch {
	case callee.Name().Text() == "hasOwnProperty" && len(args) == 1 && l.isObjectPrototypeMember(callee) && !l.isRecordExpression(callee.Expression()):
		return callee.Expression(), args[0], true
	case callee.Name().Text() == "call" && len(args) == 2 && l.libMemberName(ast.SkipParentheses(callee.Expression())) == "Object.hasOwnProperty":
		return args[0], args[1], true
	}
	return nil, nil, false
}

// hasOwnProperty lowers the test whether object has an own property key, as
// Object.hasOwn: through the object's dynamic value (D-086).
func (l *lowerer) hasOwnProperty(object *ast.Node, key *ast.Node) ir.Expr {
	o, k := l.valueExpression(object), l.expression(key)
	if o == nil || k == nil {
		return nil
	}
	return &ir.CallBuiltin{Builtin: ir.ValueHasOwn, Args: []ir.Expr{box(o), box(k)}}
}
