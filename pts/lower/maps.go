package lower

import (
	"fmt"
	"slices"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/internal/scanner"
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
	if symbol == nil || t.Flags()&checker.TypeFlagsObject == 0 || !l.isLibInterface(symbol) { // ReadonlyMap has no value declaration
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
		if mt.Key == key && mt.Value == value && mt.Set == set && !mt.Record {
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
	_, ok := l.libCollection(l.typeAt(node))
	return ok && !l.storedDynamic(node)
}

// isMapConstructor reports whether a `new` expression's class is the global Map or Set.
func (l *lowerer) isMapConstructor(node *ast.Node) bool {
	callee := node.Expression()
	return callee.Kind == ast.KindIdentifier && (callee.Text() == "Map" || callee.Text() == "Set") && l.isDeclaredInDefaultLib(l.target(callee))
}

// untypedCollectionContext is the map or set type the context of `new Map()` or `new
// Set()` without arguments or type arguments (node) gives it, with null and undefined
// left out (`current = new Map()` for a `Map<K, V> | null`); nil for any other node.
func (l *lowerer) untypedCollectionContext(node *ast.Node) *checker.Type {
	if node.Kind != ast.KindNewExpression || !l.isMapConstructor(node) || len(node.Arguments()) > 0 || node.TypeArgumentList() != nil {
		return nil
	}
	contextual := l.contextualType(node)
	if contextual == nil {
		return nil
	}
	contextual = l.checker.GetNonNullableType(contextual)
	if _, ok := l.libCollection(contextual); !ok {
		return nil
	}
	return contextual
}

// newMap lowers `new Map()`, `new Set()`, a copy (`new Map(map)`, `new Map(m.entries())`,
// `new Set(set)`), `new Set(array)` and `new Map(entries)` of an array of [key, value]
// pairs.
func (l *lowerer) newMap(node *ast.Node) ir.Expr {
	checkerType := l.typeAt(node)
	if contextual := l.untypedCollectionContext(node); contextual != nil {
		checkerType = contextual // `new Map()` is a Map<any, any> to tsc; its context says what it holds
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
	if it, ok := l.mapIterableOf(args[0]); ok {
		switch {
		case it.m == nil:
			return nil // reported
		case it.m.Type() == mt && it.items == mapItemsOf(mt):
			return &ir.NewMap{Map: mt, From: it.m}
		}
		items := map[mapItems]int{mapValues: 0, mapKeys: 1, mapEntries: 2}[it.items] // `new Set(m.keys())`: through an iterator (D-110)
		iterator := &ir.CallBuiltin{Builtin: ir.MapIteratorOf, Args: []ir.Expr{box(it.m), &ir.IntConst{Bits: uint64(items), Ty: ir.I32}}}
		return l.newMapOf(node, mt, iterator, args[0])
	}
	var from ir.Expr
	if list := ast.SkipParentheses(args[0]); list.Kind == ast.KindArrayLiteralExpression && !mt.Set {
		from = l.arrayLiteralOf(list, l.arrayOf(l.pairOf(mt))) // tsc types `[["a", true]]` by its elements
	} else {
		from = l.expression(args[0])
	}
	if from == nil {
		return nil
	}
	return l.newMapOf(node, mt, from, args[0])
}

// newMapOf makes a map or set of type mt from from, the argument (arg) of `new Map` or
// `new Set` that isn't a map of the same kind: an array (a set's elements, a map's
// [key, value] pairs), a string's code points, or what an iterable yields.
func (l *lowerer) newMapOf(node *ast.Node, mt *ir.MapType, from ir.Expr, arg *ast.Node) ir.Expr {
	if mt.Set && from.Type() == ir.String {
		from = &ir.CallBuiltin{Builtin: ir.ArrayFromString, Args: []ir.Expr{from}} // `new Set("hello")`: its code points (D-089)
	}
	if from.Type() == ir.Value || isPlainObject(from.Type()) { // an Iterable<T>, a generator object, an iterator, an object with a [Symbol.iterator]: what it yields, as an any[] (D-110, D-111)
		from = &ir.CallBuiltin{Builtin: ir.ValueToArray, Args: []ir.Expr{box(from), &ir.StringConst{Value: scanner.GetTextOfNode(arg)}}}
		if !mt.Set {
			if from = l.convert(from, l.arrayOf(l.pairOf(mt)), arg); from == nil { // its [key, value] pairs, checked
				return nil
			}
		}
	}
	switch {
	case mt.Set && isArray(from.Type()):
		if from = l.convert(from, l.arrayOf(mt.Key), arg); from == nil {
			return nil
		}
		return &ir.NewMap{Map: mt, From: from}
	case from.Type() == l.arrayOf(l.pairOf(mt)):
		return &ir.Call{Func: l.mapOfEntries(mt, node), Args: []ir.Expr{from}}
	}
	l.notSupportedYet(arg, fmt.Sprintf("a %s of a %s is", mt, from.Type()))
	return nil
}

// mapOfEntries returns the helper that makes a map of type mt from an array of its
// [key, value] pairs, setting each in order as `new Map(entries)` does, making it the
// first time.
func (l *lowerer) mapOfEntries(mt *ir.MapType, node *ast.Node) *ir.Func {
	key := helperKey{method: "Map.of entries", collection: mt}
	if helper, ok := l.helpers[key]; ok {
		return helper
	}
	at, pair := l.pos(node), l.pairOf(mt)
	fn := &ir.Func{Name: "Map.of", Module: l.function.Module, Pos: at, Result: mt}
	l.inFunction(fn, func() {
		entries := l.newLocal("entries", l.arrayOf(pair))
		fn.Params = []*ir.Local{entries}
		m, index, entry := l.newLocal("map", mt), l.newLocal("index", ir.Number), l.newLocal("entry", pair)
		set := &ir.MapCall{Op: ir.MapSet, Map: mt, Args: []ir.Expr{&ir.LocalRef{Local: m},
			&ir.FieldRef{X: &ir.LocalRef{Local: entry}, Field: pair.Fields[0]}, &ir.FieldRef{X: &ir.LocalRef{Local: entry}, Field: pair.Fields[1]}}}
		fn.Body = &ir.Block{At: at, Stmts: []ir.Stmt{
			&ir.LocalDecl{At: at, Local: m, Init: &ir.NewMap{Map: mt}},
			&ir.LocalDecl{At: at, Local: index, Init: &ir.NumberConst{Value: 0}},
			&ir.Loop{
				At:     at,
				Label:  &ir.Label{},
				Cond:   &ir.Binary{Op: ir.Lt, X: &ir.LocalRef{Local: index}, Y: &ir.ArrayCall{Op: ir.ArrayLength, Args: []ir.Expr{&ir.LocalRef{Local: entries}}}},
				Update: &ir.Assign{Local: index, Value: &ir.Binary{Op: ir.Add, X: &ir.LocalRef{Local: index}, Y: &ir.NumberConst{Value: 1}}},
				Body: &ir.Block{At: at, Stmts: []ir.Stmt{
					&ir.LocalDecl{At: at, Local: entry, Init: &ir.ArrayCall{Op: ir.ArrayElementChecked, Args: []ir.Expr{&ir.LocalRef{Local: entries}, &ir.LocalRef{Local: index}}}},
					&ir.ExprStmt{At: at, X: set},
				}},
			},
			&ir.Return{At: at, Value: &ir.LocalRef{Local: m}},
		}}
	})
	l.helpers[key] = fn
	return fn
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
	if l.isUnloweredOptionalChain(callee) {
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
		items := map[string]int{"values": 0, "keys": 1, "entries": 2}[name] // an iterator as a value (D-110)
		return &ir.CallBuiltin{Builtin: ir.MapIteratorOf, Args: []ir.Expr{box(m), &ir.IntConst{Bits: uint64(items), Ty: ir.I32}}}
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

// mapItems is what an iteration over a map or set yields: its keys (a set's elements),
// its values, or its entries, [key, value] pairs (tuples, D-053; a set's are
// [element, element]).
type mapItems int

const (
	mapValues mapItems = iota
	mapKeys
	mapEntries
)

// itemWords name each kind of items.
var itemWords = [...]string{mapValues: "values", mapKeys: "keys", mapEntries: "entries"}

// mapItemsOf is what iterating over a map of type mt yields: its entries, or a set's elements.
func mapItemsOf(mt *ir.MapType) mapItems {
	if mt.Set {
		return mapKeys
	}
	return mapEntries
}

// mapMethodItems are the items the iteration methods of maps and sets yield.
var mapMethodItems = map[string]mapItems{"keys": mapKeys, "values": mapValues, "entries": mapEntries}

// arrayFromMethods name the helpers of `Array.from` over each kind of items, and
// objectMethods those of Object.values, Object.keys and Object.entries of a hash-map object.
var (
	arrayFromMethods = [...]string{mapValues: "Array.from values", mapKeys: "Array.from keys", mapEntries: "Array.from entries"}
	objectMethods    = [...]string{mapValues: "Object.values", mapKeys: "Object.keys", mapEntries: "Object.entries"}
)

// itemsOfHelper is the kind of items the helper method of `Array.from` or of an Object
// static method lists.
func itemsOfHelper(method string) mapItems {
	if i := slices.Index(objectMethods[:], method); i >= 0 {
		return mapItems(i)
	}
	return mapItems(slices.Index(arrayFromMethods[:], method))
}

// mapIterable is what a `for...of`, `Array.from` or `new Map` iterates over a map or set:
// the map, and the items it yields.
type mapIterable struct {
	m     ir.Expr
	items mapItems
}

// mapIterableOf lowers node if it is a map or set, or `keys()`, `values()` or `entries()`
// of one; ok is false for anything else. A map yields its entries and a set its elements.
func (l *lowerer) mapIterableOf(node *ast.Node) (iterable mapIterable, ok bool) {
	node = ast.SkipParentheses(node)
	if l.isMapMethodCall(node) {
		callee := ast.SkipParentheses(node.AsCallExpression().Expression)
		if items, ok := mapMethodItems[callee.AsPropertyAccessExpression().Name().Text()]; ok {
			m := l.expression(callee.Expression())
			if m == nil {
				return mapIterable{}, true
			}
			if items == mapValues && m.Type().(*ir.MapType).Set {
				items = mapKeys // a set's values are its elements
			}
			return mapIterable{m, items}, true
		}
	}
	if !l.isMapExpression(node) {
		return mapIterable{}, false
	}
	m := l.expression(node)
	if m == nil {
		return mapIterable{}, true
	}
	return mapIterable{m, mapItemsOf(m.Type().(*ir.MapType))}, true
}

// mapItemType is the type of the items an iteration over a map of type mt yields.
func (l *lowerer) mapItemType(mt *ir.MapType, items mapItems) ir.Type {
	switch items {
	case mapKeys:
		return mt.Key
	case mapEntries:
		return l.pairOf(mt)
	}
	return mt.Value
}

// pairOf is the tuple of a map's entries, [key, value], or of a set's, [element, element].
func (l *lowerer) pairOf(mt *ir.MapType) *ir.Class {
	if mt.Set {
		return l.tupleOf([]ir.Type{mt.Key, mt.Key})
	}
	return l.tupleOf([]ir.Type{mt.Key, mt.Value})
}

// mapItem is the item an iteration over a map of type mt is at: its key, its value, or a
// new [key, value] pair.
func (l *lowerer) mapItem(iteration *ir.Local, mt *ir.MapType, items mapItems) ir.Expr {
	switch items {
	case mapKeys:
		return current(iteration, mt, true)
	case mapValues:
		return current(iteration, mt, false)
	}
	return &ir.NewObject{Shape: l.pairOf(mt), Values: []ir.Expr{current(iteration, mt, true), current(iteration, mt, mt.Set)}}
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

// mapForOf lowers the loop of `for (const x of it)` over a map or set, or the keys,
// values or entries of one: x (name), or the names of a pattern, are declared from the
// iteration's item at the start of each iteration's body. `for (const [k, v] of map)`
// binds the key and value without making a pair (destructure).
func (l *lowerer) mapForOf(node *ast.Node, label *ir.Label, it mapIterable, name *ast.Node, statement *ast.Node) []ir.Stmt {
	at := l.pos(node)
	mt := it.m.Type().(*ir.MapType)
	failed := false
	stmts := l.mapLoop(at, label, it.m, func(iteration *ir.Local) []ir.Stmt {
		item := l.mapItem(iteration, mt, it.items)
		var bound []ir.Stmt
		if name.Kind == ast.KindIdentifier {
			if stmt := l.bindName(name, item, name.Parent); stmt != nil {
				bound = []ir.Stmt{stmt}
			}
		} else {
			bound, _ = l.destructure(name, item, l.pos(name))
		}
		failed = bound == nil
		return append(bound, l.statementBlock(statement).Stmts...)
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

// mapItemsArray is a new array of the items an iteration over a map, set or hash-map
// object yields, made by a helper; nil if it fails (reported at node).
func (l *lowerer) mapItemsArray(it mapIterable, node *ast.Node) ir.Expr {
	mt := it.m.Type().(*ir.MapType)
	method := arrayFromMethods[it.items]
	if mt.Record {
		method = objectMethods[it.items]
	}
	helper := l.mapHelper(helperKey{method: method, collection: mt}, node, l.arrayOf(l.mapItemType(mt, it.items)), nil)
	if helper == nil {
		return nil
	}
	return &ir.Call{Func: helper, Args: []ir.Expr{it.m}}
}

// arrayFromMap lowers `Array.from(it)` and `Array.from(it, mapper)` for a map or set, or
// the keys, values or entries of one: an array of what the iteration yields, each mapped
// with (item, index) if there is a mapper, through a helper.
func (l *lowerer) arrayFromMap(call *ast.CallExpression, it mapIterable) ir.Expr {
	mt := it.m.Type().(*ir.MapType)
	method := arrayFromMethods[it.items]
	if len(call.Arguments.Nodes) < 2 {
		return l.mapItemsArray(it, call.Expression)
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
			return []ir.Expr{l.mapItem(iteration, mt, it.items), index}, true
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
				value = l.mapItem(iteration, mt, itemsOfHelper(key.method))
			} else {
				all, _ := args(fn, iteration, collected)
				callArgs := l.callbackArguments(all, key.callback, node)
				if callArgs == nil {
					failed = true
					return nil
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
