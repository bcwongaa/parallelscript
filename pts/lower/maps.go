package lower

import (
	"fmt"
	"slices"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/pts/ir"
)

// mapType maps the ES library's Map<K, V>, ReadonlyMap<K, V>, Set<T> and ReadonlySet<T>
// to their IR type (D-046), or reports why it can't (at node) and returns nil; ok is false
// for any other type.
func (l *lowerer) mapType(t *checker.Type, node *ast.Node) (mt *ir.MapType, ok bool) {
	name, ok := l.libCollection(t)
	if !ok {
		return nil, false
	}
	args := l.checker.GetTypeArguments(t)
	set := name == "Set" || name == "ReadonlySet"
	if set && len(args) != 1 || !set && len(args) != 2 {
		return nil, false
	}
	if l.mapsInProgress[t] {
		l.notSupportedYet(node, fmt.Sprintf("recursive map and set types (%s) are", l.checker.TypeToString(t)))
		return nil, true
	}
	l.mapsInProgress[t] = true
	defer delete(l.mapsInProgress, t)
	types := make([]ir.Type, len(args))
	for i, arg := range args {
		if types[i] = l.irType(arg, node); types[i] == nil {
			return nil, true
		}
	}
	if set {
		return l.mapOf(types[0], nil, true), true
	}
	return l.mapOf(types[0], types[1], false), true
}

// libCollection returns the name of t's type if it is the ES library's Map, Set or their readonly views.
func (l *lowerer) libCollection(t *checker.Type) (string, bool) {
	symbol := t.Symbol()
	if symbol == nil || t.Flags()&checker.TypeFlagsObject == 0 || !l.isDeclaredInDefaultLib(symbol) {
		return "", false
	}
	switch symbol.Name {
	case "Map", "ReadonlyMap", "Set", "ReadonlySet":
		return symbol.Name, true
	}
	return "", false
}

// mapOf is the one map (or set) type with the given key and value types.
func (l *lowerer) mapOf(key ir.Type, value ir.Type, set bool) *ir.MapType {
	for _, mt := range l.mapTypes {
		if mt.Key == key && mt.Value == value && mt.Set == set {
			return mt
		}
	}
	mt := &ir.MapType{Key: key, Value: value, Set: set}
	l.mapTypes = append(l.mapTypes, mt)
	return mt
}

func isMap(t ir.Type) bool {
	_, ok := t.(*ir.MapType)
	return ok
}

// isMapExpression reports whether tsc types node as a Map or Set.
func (l *lowerer) isMapExpression(node *ast.Node) bool {
	_, ok := l.libCollection(l.checker.GetTypeAtLocation(node))
	return ok
}

// isMapConstructor reports whether a `new` expression's class is the global Map or Set.
func (l *lowerer) isMapConstructor(node *ast.Node) bool {
	callee := node.Expression()
	return callee.Kind == ast.KindIdentifier && (callee.Text() == "Map" || callee.Text() == "Set") && l.isDeclaredInDefaultLib(l.target(callee))
}

// newMap lowers `new Map()`, `new Set()`, a copy `new Map(map)` or `new Set(set)`, and
// `new Set(array)`. A Map of an array of entries needs tuples (M2.6).
func (l *lowerer) newMap(node *ast.Node) ir.Expr {
	checkerType := l.checker.GetTypeAtLocation(node)
	if contextual := l.checker.GetContextualType(node, checker.ContextFlagsNone); contextual != nil && len(node.Arguments()) == 0 && node.TypeArgumentList() == nil {
		if _, ok := l.libCollection(contextual); ok {
			checkerType = contextual // `new Map()` is a Map<any, any> to tsc; its context says what it holds
		}
	}
	t := l.irType(checkerType, node)
	if t == nil {
		return nil
	}
	mt := t.(*ir.MapType)
	args := node.Arguments()
	if len(args) == 0 {
		return &ir.NewMap{Map: mt}
	}
	if list := ast.SkipParentheses(args[0]); isUndefined(args[0]) || list.Kind == ast.KindArrayLiteralExpression && len(list.AsArrayLiteralExpression().Elements.Nodes) == 0 {
		return &ir.NewMap{Map: mt} // no entries
	}
	from := l.expression(args[0])
	switch {
	case from == nil:
		return nil
	case from.Type() == mt:
		return &ir.NewMap{Map: mt, From: from}
	case mt.Set && isArray(from.Type()):
		if from = l.convert(from, l.arrayOf(mt.Key), args[0]); from == nil {
			return nil
		}
		return &ir.NewMap{Map: mt, From: from}
	case isArray(from.Type()):
		l.notSupportedYet(args[0], "a Map of an array of entries is")
		return nil
	}
	l.notSupportedYet(args[0], fmt.Sprintf("a %s of a %s is", mt, from.Type()))
	return nil
}

// isMapMethodCall reports whether node calls a method of a Map or Set.
func (l *lowerer) isMapMethodCall(node *ast.Node) bool {
	if node.Kind != ast.KindCallExpression {
		return false
	}
	callee := ast.SkipParentheses(node.AsCallExpression().Expression)
	return callee.Kind == ast.KindPropertyAccessExpression && l.isMapExpression(callee.Expression())
}

// mapMethodCall lowers `m.name(args)` for a map or set m. checked is true under `!`
// (`m.get(k)!`), where get returns the value itself and throws if there is none.
func (l *lowerer) mapMethodCall(call *ast.CallExpression, checked bool) ir.Expr {
	callee := ast.SkipParentheses(call.Expression)
	name := callee.AsPropertyAccessExpression().Name().Text()
	if ast.IsOptionalChain(callee) {
		l.notSupportedYet(callee, "optional chaining `?.` is")
		return nil
	}
	if l.hasSpreadArgument(call) {
		return nil
	}
	m := l.expression(callee.Expression())
	if m == nil {
		return nil
	}
	mt := m.Type().(*ir.MapType)
	args := call.Arguments.Nodes
	var op ir.MapOp
	var params []ir.Type
	switch {
	case name == "get" && !mt.Set:
		op, params = ir.MapGet, []ir.Type{mt.Key}
		if checked {
			op = ir.MapGetChecked
		}
	case name == "set" && !mt.Set:
		op, params = ir.MapSet, []ir.Type{mt.Key, mt.Value}
	case name == "add" && mt.Set:
		op, params = ir.MapSet, []ir.Type{mt.Key}
	case name == "has":
		op, params = ir.MapHas, []ir.Type{mt.Key}
	case name == "delete":
		op, params = ir.MapDelete, []ir.Type{mt.Key}
	case name == "clear":
		op = ir.MapClear
	case name == "forEach":
		return l.mapForEach(call, m)
	case name == "keys" || name == "values" || name == "entries":
		l.notSupportedYet(callee, fmt.Sprintf("`%s()` outside `for...of` and `Array.from` is", name))
		return nil
	default:
		l.notSupportedYet(callee, fmt.Sprintf("the %s method `%s` is", collectionName(mt), name))
		return nil
	}
	operands := l.typedArguments(args, params)
	if operands == nil {
		return nil
	}
	return &ir.MapCall{Op: op, Args: append([]ir.Expr{m}, operands...), Map: mt}
}

func collectionName(mt *ir.MapType) string {
	if mt.Set {
		return "Set"
	}
	return "Map"
}

// mapProperty lowers `m.size` for a map or set m.
func (l *lowerer) mapProperty(node *ast.Node) ir.Expr {
	name := node.AsPropertyAccessExpression().Name().Text()
	if name != "size" {
		l.notSupportedYet(node, fmt.Sprintf("the %s property `%s` is", "Map", name))
		return nil
	}
	m := l.expression(node.Expression())
	if m == nil {
		return nil
	}
	return &ir.MapCall{Op: ir.MapSize, Args: []ir.Expr{m}, Map: m.Type().(*ir.MapType)}
}

// mapIterable is what a `for...of` or `Array.from` iterates over a map or set: the map,
// and whether it yields the keys (a set's elements, `keys()`) or the values (`values()`).
type mapIterable struct {
	m    ir.Expr
	keys bool
}

// mapIterableOf lowers node if it is a set, or `keys()` or `values()` of a map or set;
// ok is false for anything else. It reports a map iterated directly or `entries()`, whose
// items are [key, value] pairs (tuples, M2.6).
func (l *lowerer) mapIterableOf(node *ast.Node) (iterable mapIterable, ok bool) {
	node = ast.SkipParentheses(node)
	if l.isMapMethodCall(node) {
		callee := ast.SkipParentheses(node.AsCallExpression().Expression)
		name := callee.AsPropertyAccessExpression().Name().Text()
		if name == "keys" || name == "values" || name == "entries" {
			m := l.expression(callee.Expression())
			if m == nil {
				return mapIterable{}, true
			}
			if name == "entries" {
				l.notSupportedYet(node, "iterating over `entries()` (pairs need tuples) is")
				return mapIterable{}, true
			}
			return mapIterable{m, name == "keys" || m.Type().(*ir.MapType).Set}, true
		}
	}
	if !l.isMapExpression(node) {
		return mapIterable{}, false
	}
	m := l.expression(node)
	if m == nil {
		return mapIterable{}, true
	}
	if !m.Type().(*ir.MapType).Set {
		l.notSupportedYet(node, "iterating over a Map directly (its [key, value] pairs need tuples; use `keys()` or `values()`) is")
		return mapIterable{}, true
	}
	return mapIterable{m, true}, true
}

// itemType is the type of what an iteration yields.
func (it mapIterable) itemType() ir.Type {
	mt := it.m.Type().(*ir.MapType)
	if it.keys {
		return mt.Key
	}
	return mt.Value
}

// mapLoop builds `let map = m; let iteration = iterate(map); while next(iteration) {
// body(iteration) }` and returns its statements.
func (l *lowerer) mapLoop(at ir.Pos, label *ir.Label, m ir.Expr, body func(iteration *ir.Local) []ir.Stmt) []ir.Stmt {
	mt := m.Type().(*ir.MapType)
	mapLocal, iteration := l.newLocal("map", mt), l.newLocal("iteration", ir.MapIterator)
	return []ir.Stmt{
		&ir.LocalDecl{At: at, Local: mapLocal, Init: m},
		&ir.LocalDecl{At: at, Local: iteration, Init: &ir.MapCall{Op: ir.MapIterate, Args: []ir.Expr{&ir.LocalRef{Local: mapLocal}}, Map: mt}},
		&ir.Loop{At: at, Label: label, Cond: &ir.MapCall{Op: ir.MapNext, Args: []ir.Expr{&ir.LocalRef{Local: iteration}}, Map: mt}, Body: &ir.Block{At: at, Stmts: body(iteration)}},
	}
}

// current is the key, or value, of the entry an iteration over a map of type mt is at.
func current(iteration *ir.Local, mt *ir.MapType, keys bool) ir.Expr {
	op := ir.MapIteratorValue
	if keys {
		op = ir.MapIteratorKey
	}
	return &ir.MapCall{Op: op, Args: []ir.Expr{&ir.LocalRef{Local: iteration}}, Map: mt}
}

// mapForOf lowers the loop of `for (const x of it)` over a set, or the keys or values of a
// map or set: x is declared from the iteration at the start of each iteration's body.
func (l *lowerer) mapForOf(node *ast.Node, label *ir.Label, it mapIterable, item *ir.Local, statement *ast.Node) []ir.Stmt {
	at := l.pos(node)
	mt := it.m.Type().(*ir.MapType)
	failed := false
	stmts := l.mapLoop(at, label, it.m, func(iteration *ir.Local) []ir.Stmt {
		value := l.convert(current(iteration, mt, it.keys), item.Type, node)
		body := l.statementBlock(statement)
		failed = value == nil
		return append([]ir.Stmt{&ir.LocalDecl{At: at, Local: item, Init: value}}, body.Stmts...)
	})
	if failed {
		return nil
	}
	return []ir.Stmt{&ir.Block{At: at, Stmts: stmts}}
}

// mapForEach lowers `m.forEach(callback)` through a helper (D-042's, per map and callback
// type): the callback gets as many of (value, key, map) as it takes; a set passes each
// element as both value and key, as JS does.
func (l *lowerer) mapForEach(call *ast.CallExpression, m ir.Expr) ir.Expr {
	args := call.Arguments.Nodes
	if len(args) > 1 {
		l.notSupportedYet(args[1], "the `thisArg` argument of `forEach` is")
		return nil
	}
	callback := l.callbackArgument(args[0])
	if callback == nil {
		return nil
	}
	mt := m.Type().(*ir.MapType)
	ft := callback.Type().(*ir.FuncType)
	helper := l.mapHelper(helperKey{method: collectionName(mt) + ".forEach", callback: ft, collection: mt}, call.Expression, ir.Void,
		func(fn *ir.Func, iteration *ir.Local, result *ir.Local) ([]ir.Expr, bool) {
			return []ir.Expr{current(iteration, mt, mt.Set), current(iteration, mt, true), &ir.LocalRef{Local: fn.Params[0]}}, true
		})
	if helper == nil {
		return nil
	}
	return &ir.Call{Func: helper, Args: []ir.Expr{m, callback}}
}

// callbackArgument lowers an argument that must be a function value, or reports it.
func (l *lowerer) callbackArgument(arg *ast.Node) ir.Expr {
	callback := l.expression(arg)
	if callback == nil {
		return nil
	}
	if _, ok := callback.Type().(*ir.FuncType); !ok {
		l.notSupportedYet(arg, fmt.Sprintf("a callback of type %s is", callback.Type()))
		return nil
	}
	return callback
}

// arrayFromMap lowers `Array.from(it)` and `Array.from(it, mapper)` for a set, or the keys
// or values of a map or set: an array of what the iteration yields, each mapped with
// (item, index) if there is a mapper, through a helper.
func (l *lowerer) arrayFromMap(call *ast.CallExpression, it mapIterable) ir.Expr {
	mt := it.m.Type().(*ir.MapType)
	elem := mt.Key
	if !it.keys {
		elem = mt.Value
	}
	method := "Array.from values"
	if it.keys {
		method = "Array.from keys"
	}
	if len(call.Arguments.Nodes) < 2 {
		helper := l.mapHelper(helperKey{method: method, collection: mt}, call.Expression, l.arrayOf(elem), nil)
		if helper == nil {
			return nil
		}
		return &ir.Call{Func: helper, Args: []ir.Expr{it.m}}
	}
	mapper := l.callbackArgument(call.Arguments.Nodes[1])
	if mapper == nil {
		return nil
	}
	ft := mapper.Type().(*ir.FuncType)
	if ft.Result == ir.Void {
		l.notSupportedYet(call.Arguments.Nodes[1], "`Array.from` with a mapping function that returns nothing is")
		return nil
	}
	helper := l.mapHelper(helperKey{method: method + " mapped", callback: ft, collection: mt}, call.Expression, l.arrayOf(ft.Result),
		func(fn *ir.Func, iteration *ir.Local, result *ir.Local) ([]ir.Expr, bool) {
			index := &ir.ArrayCall{Op: ir.ArrayLength, Args: []ir.Expr{&ir.LocalRef{Local: result}}} // the items pushed so far
			return []ir.Expr{current(iteration, mt, it.keys), index}, true
		})
	if helper == nil {
		return nil
	}
	return &ir.Call{Func: helper, Args: []ir.Expr{it.m, mapper}}
}

// mapHelper returns the helper for key, making it the first time: a function of the map
// (and the callback, if key has a callback type) that iterates over the map. With a
// callback, args gives what it is called with for each entry (the callback takes as many
// as it has parameters), and a result of an array type collects what it returns; without
// one, the array collects the iteration's items. It reports (at node) why a callback
// doesn't fit and returns nil.
func (l *lowerer) mapHelper(key helperKey, node *ast.Node, result ir.Type, args func(fn *ir.Func, iteration *ir.Local, result *ir.Local) ([]ir.Expr, bool)) *ir.Func {
	if helper, ok := l.helpers[key]; ok {
		return helper
	}
	mt := key.collection
	at := l.pos(node)
	name := key.method
	if i := slices.Index([]byte(name), ' '); i >= 0 {
		name = name[:i]
	}
	fn := &ir.Func{Name: name, Module: l.function.Module, Pos: at, Result: result}
	failed := false
	l.inFunction(fn, func() {
		fn.Params = []*ir.Local{l.newLocal("map", mt)}
		var callback, collected *ir.Local
		if key.callback != nil {
			callback = l.newLocal("callback", key.callback)
			fn.Params = append(fn.Params, callback)
		}
		var stmts []ir.Stmt
		if array, ok := result.(*ir.Array); ok {
			collected = l.newLocal("result", array)
			stmts = append(stmts, &ir.LocalDecl{At: at, Local: collected, Init: &ir.NewArray{Array: array}})
		}
		stmts = append(stmts, l.mapLoop(at, &ir.Label{}, &ir.LocalRef{Local: fn.Params[0]}, func(iteration *ir.Local) []ir.Stmt {
			var value ir.Expr
			if callback == nil {
				value = current(iteration, mt, key.method == "Array.from keys")
			} else {
				all, _ := args(fn, iteration, collected)
				params := key.callback.Params
				if len(params) > len(all) {
					l.notSupportedYet(node, fmt.Sprintf("a callback of type %s, which takes more parameters than the method passes, is", key.callback))
					failed = true
					return nil
				}
				callArgs := make([]ir.Expr, len(params))
				for i, param := range params {
					if callArgs[i] = l.convert(all[i], param, node); callArgs[i] == nil {
						failed = true
						return nil
					}
				}
				value = &ir.CallValue{Callee: &ir.LocalRef{Local: callback}, Args: callArgs}
			}
			if collected == nil {
				return []ir.Stmt{&ir.ExprStmt{At: at, X: value}}
			}
			return []ir.Stmt{&ir.ExprStmt{At: at, X: &ir.ArrayCall{Op: ir.ArrayPush, Args: []ir.Expr{&ir.LocalRef{Local: collected}, value}}}}
		})...)
		if collected != nil {
			stmts = append(stmts, &ir.Return{At: at, Value: &ir.LocalRef{Local: collected}})
		}
		fn.Body = &ir.Block{At: at, Stmts: stmts}
	})
	if failed {
		fn = nil
	}
	l.helpers[key] = fn
	return fn
}
