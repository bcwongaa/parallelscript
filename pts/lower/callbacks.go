package lower

import (
	"fmt"
	"slices"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/scanner"
	"github.com/microsoft/typescript-go/pts/ir"
)

// The array methods that take callbacks (D-042) call generated helper functions: one
// per method, array type, callback type and variant, a loop in IR that calls the
// callback with as many of (element, index, array) as it takes.

// callbackMethods are the array methods whose first argument is a callback.
var callbackMethods = map[string]bool{
	"forEach": true, "map": true, "filter": true, "flatMap": true, "some": true, "every": true,
	"find": true, "findIndex": true, "findLast": true, "findLastIndex": true, "reduce": true, "reduceRight": true,
}

// helperKey identifies a generated helper: the method (with its variant), the array
// type (or for a typed array's method, its class), the callback type and, for a filter
// whose callback is a type guard, the narrower array it makes.
type helperKey struct {
	method      string
	array       *ir.Array
	typed       *ir.Class
	callback    *ir.FuncType
	collection  *ir.MapType
	narrowed    *ir.Array
	flattened   *ir.Array // flatMap's result, tsc's type for the call (D-180)
	arrayUnused bool      // the callback's array parameter, of another type, is one its literal doesn't declare
}

// source is the type of the array a helper's method is called on.
func (k helperKey) source() ir.Type {
	if k.typed != nil {
		return k.typed
	}
	return k.array
}

// elem is the type of the elements of the array a helper's method is called on: a typed array's are numbers.
func (k helperKey) elem() ir.Type {
	if k.typed != nil {
		return ir.Number
	}
	return k.array.Elem
}

// callbackMethodCall lowers `a.m(callback, …)` for an array method m that takes a
// callback, of an array or a typed array a. checked is true under `!`, where find and
// findLast return the element itself and throw if there is none.
func (l *lowerer) callbackMethodCall(call *ast.CallExpression, name string, array ir.Expr, checked bool) ir.Expr {
	args := call.Arguments.Nodes
	if (name == "reduce" || name == "reduceRight") && len(args) > 2 || name != "reduce" && name != "reduceRight" && len(args) > 1 {
		l.notSupportedYet(args[len(args)-1], "the `thisArg` argument of array methods is")
		return nil
	}
	callback := l.expression(args[0])
	if callback == nil {
		return nil
	}
	if callback.Type() == ir.Value && !l.isTypedArray(array.Type()) {
		return l.dynamicCallbackMethodCall(call, name, array, callback)
	}
	if instantiated, ok := l.instantiatingAdapter(callback, nil, args[0]); ok {
		// `[1, 2].map(<T>(x: T) => new Box(x))`: the instance for the elements (D-194)
		if callback = instantiated; callback == nil {
			return nil
		}
	}
	ft, ok := callback.Type().(*ir.FuncType)
	if !ok {
		l.notSupportedYet(args[0], fmt.Sprintf("a callback of type %s is", callback.Type()))
		return nil
	}
	method := name
	if checked && (name == "find" || name == "findLast") {
		method += "!"
	}
	operands := []ir.Expr{array, callback}
	if (name == "reduce" || name == "reduceRight") && len(args) == 2 {
		method += " with initial value"
		initial := l.expression(args[1])
		if initial == nil {
			return nil
		}
		if initial = l.convert(initial, ft.Result, args[1]); initial == nil {
			return nil
		}
		operands = append(operands, initial)
	}
	key := helperKey{method: method, callback: ft}
	if l.isTypedArray(array.Type()) {
		key.typed = array.Type().(*ir.Class)
	} else {
		key.array = array.Type().(*ir.Array)
	}
	arrayAt := 2 // in (element, index, array), or reduce's (accumulator, element, index, array)
	if name == "reduce" || name == "reduceRight" {
		arrayAt = 3
	}
	if param := nthParam(ft, arrayAt); param != nil && param != key.source() && declaresFewer(args[0], arrayAt+1) {
		key.arrayUnused = true // `(a) => …` shaped as an `(a: unknown, i: number, all: unknown[]) => …` for a number[]
	}
	if name == "filter" && key.array != nil {
		key.narrowed = l.guardedFilterResult(call, key.array)
	}
	if name == "flatMap" {
		if key.flattened = l.flattenedResult(call, name); key.flattened == nil || l.spreadsOtherElements(call) || !l.keepsLeaves(args[0], ft.Result, key.flattened.Elem) {
			return nil
		}
	}
	helper := l.callbackHelper(key, call.Expression)
	if helper == nil {
		return nil
	}
	return &ir.Call{Func: helper, Args: operands}
}

// guardedFilterResult is the array `a.filter(guard)` makes when its callback is a type
// guard that narrows a's union elements (`shapes.filter(isCircle)` is a Circle[]), or
// nil when it makes an array of a's own type.
func (l *lowerer) guardedFilterResult(call *ast.CallExpression, array *ir.Array) *ir.Array {
	result, ok := l.irType(l.typeAt(call.AsNode()), call.AsNode()).(*ir.Array)
	if !ok || result == array {
		return nil
	}
	if union, ok := array.Elem.(*ir.Union); ok && isNarrowing(union, result.Elem) {
		return result
	}
	if isSubclassElement(array.Elem, result.Elem) {
		return result // `staff.filter((e) => e instanceof Salaried)` is a Salaried[]
	}
	return nil
}

// isSubclassElement reports whether to is a class (not a shape) that descends from from, a class.
func isSubclassElement(from, to ir.Type) bool {
	base, ok := from.(*ir.Class)
	sub, ok2 := to.(*ir.Class)
	return ok && ok2 && !base.Shape && !sub.Shape && sub != base && sub.IsSubclassOf(base)
}

// arrayFromWithMapper lowers `Array.from(source, mapper)`: the mapper applied to each
// element of an array, or to each code point of a string, as `map` would.
func (l *lowerer) arrayFromWithMapper(call *ast.CallExpression, source ir.Expr) ir.Expr {
	if source.Type() == ir.String {
		source = &ir.CallBuiltin{Builtin: ir.ArrayFromString, Args: []ir.Expr{source}}
	}
	mapper := l.expression(call.Arguments.Nodes[1])
	if mapper == nil {
		return nil
	}
	if instantiated, ok := l.instantiatingAdapter(mapper, nil, call.Arguments.Nodes[1]); ok {
		if mapper = instantiated; mapper == nil { // a generic mapper's instance for the elements (D-194)
			return nil
		}
	}
	ft, ok := mapper.Type().(*ir.FuncType)
	if !ok {
		l.notSupportedYet(call.Arguments.Nodes[1], fmt.Sprintf("a mapping function of type %s is", mapper.Type()))
		return nil
	}
	helper := l.callbackHelper(helperKey{method: "map from", array: source.Type().(*ir.Array), callback: ft}, call.Expression)
	if helper == nil {
		return nil
	}
	return &ir.Call{Func: helper, Args: []ir.Expr{source, mapper}}
}

// callbackHelper returns the helper for key, making it the first time, or reports (at
// node) why the callback doesn't fit the method and returns nil.
func (l *lowerer) callbackHelper(key helperKey, node *ast.Node) *ir.Func {
	if helper, ok := l.helpers[key]; ok {
		return helper
	}
	h := &helperBuilder{l: l, key: key, node: node}
	helper := h.build()
	l.helpers[key] = helper
	return helper
}

// helperBuilder builds the body of a helper function.
type helperBuilder struct {
	l        *lowerer
	key      helperKey
	node     *ast.Node
	fn       *ir.Func
	array    *ir.Local
	callback *ir.Local
	failed   bool
}

func (h *helperBuilder) build() *ir.Func {
	l, key := h.l, h.key
	result := h.resultType()
	if result == nil {
		return nil
	}
	name := "Array." + baseMethod(key.method)
	if key.typed != nil {
		name = key.typed.Name + "." + baseMethod(key.method)
	}
	h.fn = &ir.Func{Name: name, Module: l.function.Module, Pos: l.pos(h.node), Result: result}
	l.inFunction(h.fn, func() {
		h.array, h.callback = l.newLocal("array", key.source()), l.newLocal("callback", key.callback)
		h.fn.Params = []*ir.Local{h.array, h.callback}
		h.fn.Body = &ir.Block{At: h.fn.Pos, Stmts: h.body()}
	})
	if h.failed {
		return nil
	}
	return h.fn
}

// resultType is what the helper returns, or nil (reported) if the callback can't be used.
func (h *helperBuilder) resultType() ir.Type {
	l, key := h.l, h.key
	elem, returns := key.elem(), key.callback.Result
	switch base := baseMethod(key.method); {
	case returns == ir.Void && (base == "map" || base == "flatMap" || base == "reduce" || base == "reduceRight"):
		l.notSupportedYet(h.node, "`"+base+"` with a callback that returns nothing is")
		return nil
	case base == "forEach":
		return ir.Void
	case base == "map" && key.typed != nil:
		return key.typed
	case base == "map":
		return l.arrayOf(returns)
	case base == "flatMap":
		return key.flattened
	case base == "filter" && key.narrowed != nil:
		return key.narrowed
	case base == "filter":
		return key.source()
	case base == "some" || base == "every":
		return ir.Bool
	case base == "findIndex" || base == "findLastIndex":
		return ir.Number
	case key.method == "find!" || key.method == "findLast!":
		return elem
	case base == "find" || base == "findLast":
		return ir.Value
	}
	return returns // reduce and reduceRight: the accumulator
}

// baseMethod is the method a helper's key names, without its variant.
func baseMethod(method string) string {
	for i, r := range method {
		if r == '!' || r == ' ' {
			return method[:i]
		}
	}
	return method
}

// body is the helper's code for its method.
func (h *helperBuilder) body() []ir.Stmt {
	switch base := baseMethod(h.key.method); base {
	case "forEach":
		return h.loop(false, nil, func(element, index ir.Expr) []ir.Stmt {
			return []ir.Stmt{h.stmt(h.call(element, index))}
		}, nil)
	case "map":
		if h.key.typed != nil {
			return h.typedMap()
		}
		if h.key.method == "map from" {
			return h.mapFrom()
		}
		// map's result has the array's length, holes (or zeros of a primitive element type,
		// D-085) where the array has holes, which the callback isn't called for.
		result := h.l.newLocal("result", h.fn.Result)
		made := &ir.NewArray{Array: h.fn.Result.(*ir.Array), Length: h.length()}
		return h.loop(false, []ir.Stmt{h.declare(result, made)}, func(element, index ir.Expr) []ir.Stmt {
			return []ir.Stmt{h.stmt(h.store(result, index, h.call(element, index)))}
		}, h.ret(&ir.LocalRef{Local: result}))
	case "flatMap":
		return h.flatMap()
	case "filter":
		if h.key.typed != nil {
			return h.typedFilter()
		}
		resultType := h.fn.Result.(*ir.Array)
		result := h.l.newLocal("result", resultType)
		return h.loop(false, []ir.Stmt{h.declare(result, &ir.NewArray{Array: resultType})}, func(element, index ir.Expr) []ir.Stmt {
			kept := element
			if h.key.narrowed != nil && isSubclassElement(h.key.array.Elem, resultType.Elem) {
				kept = downcast(element, resultType.Elem.(*ir.Class)) // the guard held for it, unless it lies (checked)
			} else if h.key.narrowed != nil {
				kept = &ir.FromUnion{X: element, To: resultType.Elem} // the guard held for it
			} else if h.widened() { // a string[]'s
				keep := &ir.ArrayCall{Op: ir.ArrayPushFlat, Args: []ir.Expr{&ir.LocalRef{Local: result}, box(element)}} // a match's undefined group stays one (D-133)
				return []ir.Stmt{h.when(h.truthy(h.call(element, index)), h.stmt(keep))}
			}
			return []ir.Stmt{h.when(h.truthy(h.call(element, index)), h.stmt(h.push(result, kept)))}
		}, h.ret(&ir.LocalRef{Local: result}))
	case "some", "every":
		found := base == "some"
		return h.loop(false, nil, func(element, index ir.Expr) []ir.Stmt {
			test := h.truthy(h.call(element, index))
			if !found {
				test = &ir.Unary{Op: ir.Not, X: test}
			}
			return []ir.Stmt{h.when(test, h.ret(&ir.BoolConst{Value: found}))}
		}, h.ret(&ir.BoolConst{Value: !found}))
	case "find", "findLast", "findIndex", "findLastIndex":
		return h.find(base)
	}
	return h.reduce(baseMethod(h.key.method) == "reduceRight")
}

// mapFrom builds Array.from's mapping: a new array of the mapper's results for every
// index of the source, a hole read as undefined, as Array.from iterates it.
func (h *helperBuilder) mapFrom() []ir.Stmt {
	result := h.l.newLocal("result", h.fn.Result)
	return h.loop(false, []ir.Stmt{h.declare(result, &ir.NewArray{Array: h.fn.Result.(*ir.Array)})}, func(element, index ir.Expr) []ir.Stmt {
		return []ir.Stmt{h.stmt(h.push(result, h.call(element, index)))}
	}, h.ret(&ir.LocalRef{Local: result}))
}

// typedMap builds a typed array's map: a new typed array of its kind, of the callback's
// results for its elements.
func (h *helperBuilder) typedMap() []ir.Stmt {
	l, class := h.l, h.key.typed
	result := l.newLocal("result", class)
	made := l.callExtern(typedArrayFunction(class, "new"), class, h.length())
	return h.loop(false, []ir.Stmt{h.declare(result, made)}, func(element, index ir.Expr) []ir.Stmt {
		mapped := l.convert(h.call(element, index), ir.Number, h.node)
		if mapped == nil {
			h.failed = true
			return nil
		}
		return []ir.Stmt{h.stmt(l.typedArrayStore(&ir.LocalRef{Local: result}, index, mapped))}
	}, h.ret(&ir.LocalRef{Local: result}))
}

// typedFilter builds a typed array's filter: a new typed array of its kind, of the
// elements the callback holds for.
func (h *helperBuilder) typedFilter() []ir.Stmt {
	l, class := h.l, h.key.typed
	kept := l.newLocal("kept", ir.NumberArray)
	result := l.callExtern(typedArrayFunction(class, "from_array"), class, &ir.LocalRef{Local: kept})
	return h.loop(false, []ir.Stmt{h.declare(kept, &ir.NewArray{Array: ir.NumberArray})}, func(element, index ir.Expr) []ir.Stmt {
		return []ir.Stmt{h.when(h.truthy(h.call(element, index)), h.stmt(h.push(kept, element)))}
	}, h.ret(result))
}

// find builds find, findLast, findIndex and findLastIndex: the first element (or its
// index) the callback holds for, searching from the end for the Last ones.
func (h *helperBuilder) find(base string) []ir.Stmt {
	backwards := base == "findLast" || base == "findLastIndex"
	index := base == "findIndex" || base == "findLastIndex"
	checked := h.key.method == "find!" || h.key.method == "findLast!"
	var notFound ir.Stmt
	switch {
	case index:
		notFound = h.ret(&ir.NumberConst{Value: -1})
	case checked:
		notFound = h.stmt(&ir.CallBuiltin{Builtin: ir.ThrowError, Args: []ir.Expr{&ir.StringConst{Value: "RangeError"}, &ir.StringConst{Value: base + "() found no element"}}})
	default:
		notFound = h.ret(box(&ir.UndefinedConst{}))
	}
	return h.loop(backwards, nil, func(element, i ir.Expr) []ir.Stmt {
		var found ir.Expr = element
		switch {
		case index:
			found = i
		case !checked:
			found = box(element)
		case h.widened():
			found = h.narrowedElement(element, h.fn.Result) // the element as its type, which a hole or an undefined group isn't
		}
		return []ir.Stmt{h.when(h.truthy(h.call(element, i)), h.ret(found))}
	}, notFound)
}

// reduce builds reduce and reduceRight: the callback folds the elements into an
// accumulator, which starts as the initial value or, without one, the first element
// there is (holes skipped).
func (h *helperBuilder) reduce(right bool) []ir.Stmt {
	accumulator := h.l.newLocal("accumulator", h.fn.Result)
	var start []ir.Stmt
	from := ir.Expr(nil)
	if h.key.method == "reduce with initial value" || h.key.method == "reduceRight with initial value" {
		initial := h.l.newLocal("initial", h.fn.Result)
		h.fn.Params = append(h.fn.Params, initial)
		start = []ir.Stmt{h.declare(accumulator, &ir.LocalRef{Local: initial})}
	} else if h.hasHoles() {
		start, from = h.firstElement(right, accumulator)
	} else {
		empty := &ir.Binary{Op: ir.Eq, X: h.length(), Y: &ir.NumberConst{Value: 0}}
		first := ir.Expr(&ir.NumberConst{Value: 0})
		from = &ir.NumberConst{Value: 1}
		if right {
			first = &ir.Binary{Op: ir.Sub, X: h.length(), Y: &ir.NumberConst{Value: 1}}
			from = &ir.Binary{Op: ir.Sub, X: h.length(), Y: &ir.NumberConst{Value: 2}}
		}
		start = []ir.Stmt{
			h.when(empty, h.throwEmptyReduce()),
			h.declare(accumulator, h.narrowedElement(h.element(first), h.fn.Result)),
		}
	}
	return h.loopFrom(right, from, start, func(element, index ir.Expr) []ir.Stmt {
		value := h.callWith([]ir.Expr{&ir.LocalRef{Local: accumulator}, element, index, &ir.LocalRef{Local: h.array}})
		return []ir.Stmt{h.stmt(&ir.Assign{Local: accumulator, Value: h.l.convert(value, h.fn.Result, h.node)})}
	}, h.ret(&ir.LocalRef{Local: accumulator}))
}

// firstElement starts reduce or reduceRight without an initial value over an array that
// may have holes: the accumulator is the first (or last) element there is, or it throws
// TypeError for none, and the fold goes on from the index after it, which it returns.
func (h *helperBuilder) firstElement(right bool, accumulator *ir.Local) (start []ir.Stmt, from ir.Expr) {
	l, at := h.l, h.fn.Pos
	first := l.newLocal("first", ir.Number)
	i := &ir.LocalRef{Local: first}
	begin, step := ir.Expr(&ir.NumberConst{Value: 0}), ir.Add
	inside := ir.Expr(&ir.Binary{Op: ir.Lt, X: i, Y: h.length()})
	if right {
		begin, step = &ir.Binary{Op: ir.Sub, X: h.length(), Y: &ir.NumberConst{Value: 1}}, ir.Sub
		inside = &ir.Binary{Op: ir.Ge, X: i, Y: &ir.NumberConst{Value: 0}}
	}
	next := &ir.Binary{Op: step, X: i, Y: &ir.NumberConst{Value: 1}}
	start = []ir.Stmt{
		h.declare(first, begin),
		&ir.Loop{At: at, Label: &ir.Label{}, Cond: &ir.Logical{Op: ir.And, X: inside, Y: &ir.Unary{Op: ir.Not, X: h.has(i)}}, Update: &ir.Assign{Local: first, Value: next}, Body: &ir.Block{At: at}},
		h.when(&ir.Unary{Op: ir.Not, X: inside}, h.throwEmptyReduce()),
		h.declare(accumulator, h.narrowedElement(h.element(i), h.fn.Result)),
	}
	return start, next
}

func (h *helperBuilder) throwEmptyReduce() ir.Stmt {
	return h.stmt(&ir.CallBuiltin{Builtin: ir.ThrowError, Args: []ir.Expr{&ir.StringConst{Value: "TypeError"}, &ir.StringConst{Value: "Reduce of empty array with no initial value"}}})
}

// loop builds `start; length = array.length; for each index i below length (from the
// end if backwards) that the array still has: each(array[i], i); end`. The methods that
// skip holes visit only the elements there are (JS's HasProperty).
func (h *helperBuilder) loop(backwards bool, start []ir.Stmt, each func(element, index ir.Expr) []ir.Stmt, end ir.Stmt) []ir.Stmt {
	return h.loopFrom(backwards, nil, start, each, end)
}

// loopFrom is loop starting at index from (nil for the first, or last, index).
func (h *helperBuilder) loopFrom(backwards bool, from ir.Expr, start []ir.Stmt, each func(element, index ir.Expr) []ir.Stmt, end ir.Stmt) []ir.Stmt {
	l, at := h.l, h.fn.Pos
	length, index := l.newLocal("length", ir.Number), l.newLocal("index", ir.Number)
	stmts := append([]ir.Stmt{h.declare(length, h.length())}, start...)
	if from == nil {
		from = &ir.NumberConst{Value: 0}
		if backwards {
			from = &ir.Binary{Op: ir.Sub, X: &ir.LocalRef{Local: length}, Y: &ir.NumberConst{Value: 1}}
		}
	}
	i := &ir.LocalRef{Local: index}
	step, cond := ir.Add, ir.Expr(&ir.Binary{Op: ir.Lt, X: i, Y: &ir.LocalRef{Local: length}})
	if backwards {
		step, cond = ir.Sub, &ir.Binary{Op: ir.Ge, X: i, Y: &ir.NumberConst{Value: 0}}
	}
	// JS visits only the indices the array still has: a callback may shorten it. find and
	// its kin visit every index below the length they start with, reading what isn't
	// there as undefined, where the element type holds undefined (D-183).
	present := ir.Expr(&ir.Binary{Op: ir.Lt, X: i, Y: h.length()})
	switch {
	case h.hasHoles():
		present = h.has(i)
	case h.readsEveryIndex():
		present = nil
	}
	element := l.newLocal("element", h.elementType())
	body := append([]ir.Stmt{h.declare(element, h.element(i))}, each(&ir.LocalRef{Local: element}, i)...)
	if present != nil {
		body = []ir.Stmt{&ir.If{At: at, Cond: present, Then: &ir.Block{At: at, Stmts: body}}}
	}
	stmts = append(stmts, h.declare(index, from), &ir.Loop{
		At:     at,
		Label:  &ir.Label{},
		Cond:   cond,
		Update: &ir.Assign{Local: index, Value: &ir.Binary{Op: step, X: i, Y: &ir.NumberConst{Value: 1}}},
		Body:   &ir.Block{At: at, Stmts: body},
	})
	if end != nil {
		stmts = append(stmts, end)
	}
	return stmts
}

// call calls the callback with as many of (element, index, array) as it takes; for
// Array.from's mapper, of (element, index).
func (h *helperBuilder) call(element, index ir.Expr) ir.Expr {
	if h.key.method == "map from" { // Array.from's mapper gets (element, index)
		return h.callWith([]ir.Expr{element, index})
	}
	return h.callWith([]ir.Expr{element, index, &ir.LocalRef{Local: h.array}})
}

// callWith calls the callback with the first of args, as many as it takes (callbackArguments).
func (h *helperBuilder) callWith(args []ir.Expr) ir.Expr {
	if last := len(args) - 1; h.key.arrayUnused {
		args = append(slices.Clone(args[:last]), &ir.LeftOut{T: nthParam(h.key.callback, last)}) // never read
	}
	converted := h.l.callbackArguments(args, h.key.callback, h.node)
	if converted == nil {
		h.failed = true
	}
	return &ir.CallValue{Callee: &ir.LocalRef{Local: h.callback}, Args: converted}
}

// callbackArguments are the arguments a builtin method that passes args calls a callback
// of type ft with: the first of args, as many as it takes, each converted to its
// parameter's type; a parameter with a default that args don't reach is left out (D-055).
// They are nil if the callback takes more than args or one doesn't convert (reported at node).
func (l *lowerer) callbackArguments(args []ir.Expr, ft *ir.FuncType, node *ast.Node) []ir.Expr {
	params := ft.Params
	converted := make([]ir.Expr, 0, len(params))
	next := 0
	for i := 0; i < len(params); i++ {
		param, optional := params[i], hasDefault(params, i)
		if rest, ok := param.(*ir.Rest); ok { // the last: an array of the arguments left
			elems := make([]ir.Expr, 0, len(args)-next)
			for _, arg := range args[next:] {
				elem := l.convert(arg, rest.Array.Elem, node)
				if elem == nil {
					return nil
				}
				elems = append(elems, elem)
			}
			return append(converted, &ir.NewArray{Array: rest.Array, Elems: elems})
		}
		if optional {
			i++ // past the flag
		}
		switch {
		case next < len(args):
		case optional:
			converted = append(converted, notPassed(param)...)
			continue
		default:
			l.notSupportedYet(node, fmt.Sprintf("a callback of type %s, which takes more parameters than the method passes, is", ft))
			return nil
		}
		arg := l.convert(args[next], param, node)
		if arg == nil {
			return nil
		}
		next++
		converted = append(converted, arg)
		if optional {
			converted = append(converted, &ir.GivenConst{Value: true})
		}
	}
	return converted
}

// nthParam is the type of the parameter of ft at position n, Given flags aside; nil if it has fewer.
func nthParam(ft *ir.FuncType, n int) ir.Type {
	for _, param := range ft.Params {
		if param == ir.Given {
			continue
		}
		if n == 0 {
			return param
		}
		n--
	}
	return nil
}

// declaresFewer reports whether node is an arrow function or function expression that
// declares fewer than n parameters, none of them a rest parameter: those its type has
// past them only give it its context's shape (closureSignature).
func declaresFewer(node *ast.Node, n int) bool {
	node = ast.SkipParentheses(node)
	if node.Kind != ast.KindArrowFunction && node.Kind != ast.KindFunctionExpression {
		return false
	}
	params := node.Parameters()
	return len(params) < n && (len(params) == 0 || params[len(params)-1].AsParameterDeclaration().DotDotDotToken == nil)
}

// flatMap builds flatMap: the callback's values for the elements the array has when it
// starts (holes skipped, as in JS), each array or tuple among them spread one level into
// the result by the runtime, which tells them by their descriptors (D-180).
func (h *helperBuilder) flatMap() []ir.Stmt {
	l := h.l
	result := l.newLocal("result", h.fn.Result)
	ref := &ir.LocalRef{Local: result}
	return h.loop(false, []ir.Stmt{h.declare(result, &ir.NewArray{Array: h.fn.Result.(*ir.Array)})}, func(element, index ir.Expr) []ir.Stmt {
		mapped := h.call(element, index)
		if !mayBeFlattened(mapped.Type()) {
			return []ir.Stmt{h.stmt(h.push(result, mapped))}
		}
		return []ir.Stmt{h.stmt(&ir.ArrayCall{Op: ir.ArrayPushFlat, Args: []ir.Expr{ref, box(mapped)}})}
	}, h.ret(ref))
}

// mayBeFlattened reports whether a value of type t may be an array or a tuple, which flat
// and flatMap spread: arrays can't be held as interfaces or classes (D-180).
func mayBeFlattened(t ir.Type) bool {
	switch t := t.(type) {
	case *ir.Array:
		return true
	case *ir.Class:
		return t.Tuple
	case *ir.Union:
		return slices.ContainsFunc(t.Members, mayBeFlattened)
	}
	return t == ir.Value
}

// truthy is JS's ToBoolean of a callback's result, which is still evaluated for its effects.
func (h *helperBuilder) truthy(x ir.Expr) ir.Expr {
	switch t := x.Type(); {
	case t == ir.Bool:
		return x
	case t == ir.Void || t == ir.Undefined || t == ir.Null:
		return &ir.Sequence{X: x, Y: &ir.BoolConst{Value: false}}
	case ir.IsNumeric(t) || t == ir.String || t == ir.Value || isUnion(t):
		return &ir.Truthy{X: x}
	}
	return &ir.Sequence{X: x, Y: &ir.BoolConst{Value: true}} // an object or function, never null
}

func (h *helperBuilder) length() ir.Expr {
	if h.key.typed != nil {
		return h.l.callExtern("pts_typed_array_length", ir.Number, &ir.LocalRef{Local: h.array})
	}
	return &ir.ArrayCall{Op: ir.ArrayLength, Args: []ir.Expr{&ir.LocalRef{Local: h.array}}}
}

// element reads the element at index: for the methods that read every index, a hole
// (or for find, an index past the end) is undefined where the element type holds
// undefined, as JS reads it, and elsewhere it throws RangeError (D-085).
func (h *helperBuilder) element(index ir.Expr) ir.Expr {
	if h.key.typed != nil {
		return h.l.typedArrayRead(&ir.LocalRef{Local: h.array}, index)
	}
	if h.widened() { // a slot of pointers holds its element as the wider type holds it (D-002)
		return &ir.ArrayCall{Op: ir.ArrayElementOrUndefined, Args: []ir.Expr{&ir.LocalRef{Local: h.array}, index}, As: h.elementParam()}
	}
	op := ir.ArrayElementChecked
	if !h.skipsHoles() && ir.HoldsUndefined(h.key.array.Elem) {
		op = ir.ArrayElementOrUndefined
	}
	return &ir.ArrayCall{Op: op, Args: []ir.Expr{&ir.LocalRef{Local: h.array}, index}}
}

// skipsHoles reports whether the helper's method visits only the elements an array has,
// as JS's callback methods do, but find and its kin, Array.from's mapping and entries,
// which read every index.
func (h *helperBuilder) skipsHoles() bool {
	switch baseMethod(h.key.method) {
	case "find", "findLast", "findIndex", "findLastIndex", "Array.entries":
		return false
	}
	return h.key.method != "map from"
}

// readsEveryIndex reports whether the helper is find or one of its kin over an array whose
// elements it reads as values that may be undefined, which reads every index below the
// length it starts with.
func (h *helperBuilder) readsEveryIndex() bool {
	switch baseMethod(h.key.method) {
	case "find", "findLast", "findIndex", "findLastIndex":
		return h.key.typed == nil && (ir.HoldsUndefined(h.key.array.Elem) || h.widened())
	}
	return false
}

// elementType is the type the helper reads each element as: the array's element type,
// or the callback's element parameter's where it takes undefined and the array's elements
// can't be (widened).
func (h *helperBuilder) elementType() ir.Type {
	if h.widened() {
		return h.elementParam()
	}
	return h.key.elem()
}

// widened reports whether the helper reads the elements as the callback's element
// parameter takes them, where that reaches it as JS has it: a hole that find, its kin and
// Array.from's mapping visit, in an array of strings or objects, or a match's group that
// took no part (D-133), in a string[]. Only where the parameter holds an element as it is:
// a dynamic value, or a union with undefined that has the element type among its members
// (holdsAsItIs); others convert each element (review round 2: a tuple read as an array).
func (h *helperBuilder) widened() bool {
	if h.key.typed != nil || h.key.callback == nil || h.key.narrowed != nil {
		return false
	}
	elem, param := h.key.array.Elem, h.elementParam()
	if param == nil || ir.HoldsUndefined(elem) || ir.IsNumeric(elem) || elem == ir.Bool {
		return false
	}
	if param != ir.Value && !(isUnion(param) && ir.HoldsUndefined(param) && holdsAsItIs(param, elem)) {
		return false
	}
	return !h.skipsHoles() || elem == ir.String
}

// narrowedElement is x, an element as the helper reads it, converted to t: through a
// dynamic value where it was widened and t can't be undefined, which a hole or a match's
// undefined group then throws for.
func (h *helperBuilder) narrowedElement(x ir.Expr, t ir.Type) ir.Expr {
	if h.widened() && !ir.HoldsUndefined(t) {
		x = box(x)
	}
	return h.l.convert(x, t, h.node)
}

// elementParam is the type of the callback's element parameter, nil if it has none.
func (h *helperBuilder) elementParam() ir.Type {
	if base := baseMethod(h.key.method); base == "reduce" || base == "reduceRight" {
		return nthParam(h.key.callback, 1) // after the accumulator
	}
	return nthParam(h.key.callback, 0)
}

// hasHoles reports whether the helper skips the holes of its arrays, which only arrays of
// pointers and dynamic values have (D-085).
func (h *helperBuilder) hasHoles() bool {
	elem := h.key.elem()
	return h.skipsHoles() && h.key.typed == nil && !ir.IsNumeric(elem) && elem != ir.Bool
}

// has is whether the array has an element at index, not a hole (JS's HasProperty).
func (h *helperBuilder) has(index ir.Expr) ir.Expr {
	return &ir.ArrayCall{Op: ir.ArrayHas, Args: []ir.Expr{&ir.LocalRef{Local: h.array}, index}}
}

func (h *helperBuilder) push(result *ir.Local, value ir.Expr) ir.Expr {
	return &ir.ArrayCall{Op: ir.ArrayPush, Args: []ir.Expr{&ir.LocalRef{Local: result}, h.convertedElement(result, value)}}
}

// store writes value to result[index], within its length.
func (h *helperBuilder) store(result *ir.Local, index, value ir.Expr) ir.Expr {
	return &ir.ArrayCall{Op: ir.ArraySetElement, Args: []ir.Expr{&ir.LocalRef{Local: result}, index, h.convertedElement(result, value)}}
}

// convertedElement is value converted to the element type of result, an array.
func (h *helperBuilder) convertedElement(result *ir.Local, value ir.Expr) ir.Expr {
	converted := h.l.convert(value, result.Type.(*ir.Array).Elem, h.node)
	if converted == nil {
		h.failed = true
		return value
	}
	return converted
}

func (h *helperBuilder) declare(local *ir.Local, init ir.Expr) ir.Stmt {
	if init == nil {
		h.failed = true // a conversion failed, which was reported
	}
	return &ir.LocalDecl{At: h.fn.Pos, Local: local, Init: init}
}

func (h *helperBuilder) stmt(x ir.Expr) ir.Stmt { return &ir.ExprStmt{At: h.fn.Pos, X: x} }

func (h *helperBuilder) ret(x ir.Expr) ir.Stmt { return &ir.Return{At: h.fn.Pos, Value: x} }

func (h *helperBuilder) when(cond ir.Expr, then ...ir.Stmt) ir.Stmt {
	return &ir.If{At: h.fn.Pos, Cond: cond, Then: &ir.Block{At: h.fn.Pos, Stmts: slices.Clone(then)}}
}

// dynamicCallbackMethodCall lowers `a.m(callback, …)` for a callback that is a dynamic
// value (any, or a function of erased code's whose type mentions its type parameters,
// D-078): the array's method found at run time calls it, and its result is converted
// to the call's type with a check.
func (l *lowerer) dynamicCallbackMethodCall(call *ast.CallExpression, name string, array ir.Expr, callback ir.Expr) ir.Expr {
	if l.firstDynamicCall == nil {
		l.firstDynamicCall = call.AsNode()
	}
	t := l.irType(l.typeAt(call.AsNode()), call.AsNode())
	if t == nil {
		return nil
	}
	operands := []ir.Expr{box(array), box(&ir.StringConst{Value: name}), &ir.StringConst{Value: scanner.GetTextOfNode(call.Expression)}, box(callback)}
	for _, argument := range call.Arguments.Nodes[1:] {
		value := l.valueExpression(argument)
		if value == nil {
			return nil
		}
		operands = append(operands, box(value))
	}
	return l.convert(&ir.CallBuiltin{Builtin: ir.DynamicCallMethod, Args: operands}, t, call.AsNode())
}
