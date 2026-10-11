package lower

import (
	"fmt"
	"slices"

	"github.com/microsoft/typescript-go/internal/ast"
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
// type and the callback type.
type helperKey struct {
	method     string
	array      *ir.Array
	callback   *ir.FuncType
	collection *ir.MapType
}

// callbackMethodCall lowers `a.m(callback, …)` for an array method m that takes a
// callback. checked is true under `!`, where find and findLast return the element
// itself and throw if there is none.
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
	ft, ok := callback.Type().(*ir.FuncType)
	if !ok {
		l.notSupportedYet(args[0], fmt.Sprintf("a callback of type %s is", callback.Type()))
		return nil
	}
	arrayType := array.Type().(*ir.Array)
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
	helper := l.callbackHelper(helperKey{method: method, array: arrayType, callback: ft}, call.Expression)
	if helper == nil {
		return nil
	}
	return &ir.Call{Func: helper, Args: operands}
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
	ft, ok := mapper.Type().(*ir.FuncType)
	if !ok {
		l.notSupportedYet(call.Arguments.Nodes[1], fmt.Sprintf("a mapping function of type %s is", mapper.Type()))
		return nil
	}
	helper := l.callbackHelper(helperKey{method: "map", array: source.Type().(*ir.Array), callback: ft}, call.Expression)
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
	h.fn = &ir.Func{Name: name, Module: l.function.Module, Pos: l.pos(h.node), Result: result}
	l.inFunction(h.fn, func() {
		h.array, h.callback = l.newLocal("array", key.array), l.newLocal("callback", key.callback)
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
	elem, returns := key.array.Elem, key.callback.Result
	switch base := baseMethod(key.method); {
	case returns == ir.Void && (base == "map" || base == "flatMap" || base == "reduce" || base == "reduceRight"):
		l.notSupportedYet(h.node, "`"+base+"` with a callback that returns nothing is")
		return nil
	case base == "forEach":
		return ir.Void
	case base == "map":
		return l.arrayOf(returns)
	case base == "flatMap":
		if isArray(returns) {
			return returns
		}
		return l.arrayOf(returns)
	case base == "filter":
		return key.array
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
	case "map", "flatMap":
		result := h.l.newLocal("result", h.fn.Result)
		return h.loop(false, []ir.Stmt{h.declare(result, &ir.NewArray{Array: h.fn.Result.(*ir.Array)})}, func(element, index ir.Expr) []ir.Stmt {
			mapped := h.call(element, index)
			if base == "flatMap" && isArray(mapped.Type()) {
				return h.appendAll(result, mapped)
			}
			return []ir.Stmt{h.stmt(h.push(result, mapped))}
		}, h.ret(&ir.LocalRef{Local: result}))
	case "filter":
		result := h.l.newLocal("result", h.key.array)
		return h.loop(false, []ir.Stmt{h.declare(result, &ir.NewArray{Array: h.key.array})}, func(element, index ir.Expr) []ir.Stmt {
			return []ir.Stmt{h.when(h.truthy(h.call(element, index)), h.stmt(h.push(result, element)))}
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
		}
		return []ir.Stmt{h.when(h.truthy(h.call(element, i)), h.ret(found))}
	}, notFound)
}

// reduce builds reduce and reduceRight: the callback folds the elements into an
// accumulator, which starts as the initial value or, without one, the first element.
func (h *helperBuilder) reduce(right bool) []ir.Stmt {
	accumulator := h.l.newLocal("accumulator", h.fn.Result)
	var start []ir.Stmt
	from := ir.Expr(nil)
	if h.key.method == "reduce with initial value" || h.key.method == "reduceRight with initial value" {
		initial := h.l.newLocal("initial", h.fn.Result)
		h.fn.Params = append(h.fn.Params, initial)
		start = []ir.Stmt{h.declare(accumulator, &ir.LocalRef{Local: initial})}
	} else {
		empty := &ir.Binary{Op: ir.Eq, X: h.length(), Y: &ir.NumberConst{Value: 0}}
		first := ir.Expr(&ir.NumberConst{Value: 0})
		from = &ir.NumberConst{Value: 1}
		if right {
			first = &ir.Binary{Op: ir.Sub, X: h.length(), Y: &ir.NumberConst{Value: 1}}
			from = &ir.Binary{Op: ir.Sub, X: h.length(), Y: &ir.NumberConst{Value: 2}}
		}
		start = []ir.Stmt{
			h.when(empty, h.stmt(&ir.CallBuiltin{Builtin: ir.ThrowError, Args: []ir.Expr{&ir.StringConst{Value: "TypeError"}, &ir.StringConst{Value: "Reduce of empty array with no initial value"}}})),
			h.declare(accumulator, h.l.convert(h.element(first), h.fn.Result, h.node)),
		}
	}
	return h.loopFrom(right, from, start, func(element, index ir.Expr) []ir.Stmt {
		value := h.callWith([]ir.Expr{&ir.LocalRef{Local: accumulator}, element, index, &ir.LocalRef{Local: h.array}})
		return []ir.Stmt{h.stmt(&ir.Assign{Local: accumulator, Value: h.l.convert(value, h.fn.Result, h.node)})}
	}, h.ret(&ir.LocalRef{Local: accumulator}))
}

// loop builds `start; length = array.length; for each index i below length (from the
// end if backwards) that the array still has: each(array[i], i); end`.
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
	// JS visits only the indices the array still has: a callback may shorten it.
	present := &ir.Binary{Op: ir.Lt, X: i, Y: h.length()}
	element := l.newLocal("element", h.key.array.Elem)
	body := append([]ir.Stmt{h.declare(element, h.element(i))}, each(&ir.LocalRef{Local: element}, i)...)
	stmts = append(stmts, h.declare(index, from), &ir.Loop{
		At:     at,
		Label:  &ir.Label{},
		Cond:   cond,
		Update: &ir.Assign{Local: index, Value: &ir.Binary{Op: step, X: i, Y: &ir.NumberConst{Value: 1}}},
		Body:   &ir.Block{At: at, Stmts: []ir.Stmt{&ir.If{At: at, Cond: present, Then: &ir.Block{At: at, Stmts: body}}}},
	})
	if end != nil {
		stmts = append(stmts, end)
	}
	return stmts
}

// call calls the callback with as many of (element, index, array) as it takes.
func (h *helperBuilder) call(element, index ir.Expr) ir.Expr {
	return h.callWith([]ir.Expr{element, index, &ir.LocalRef{Local: h.array}})
}

// callWith calls the callback with the first of args, as many as it takes (callbackArguments).
func (h *helperBuilder) callWith(args []ir.Expr) ir.Expr {
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

// appendAll pushes each element of an array value onto result (flatMap's flattening).
func (h *helperBuilder) appendAll(result *ir.Local, values ir.Expr) []ir.Stmt {
	l, at := h.l, h.fn.Pos
	inner, j := l.newLocal("mapped", values.Type()), l.newLocal("j", ir.Number)
	ref := &ir.LocalRef{Local: j}
	value := &ir.ArrayCall{Op: ir.ArrayElementChecked, Args: []ir.Expr{&ir.LocalRef{Local: inner}, ref}}
	return []ir.Stmt{
		h.declare(inner, values),
		h.declare(j, &ir.NumberConst{Value: 0}),
		&ir.Loop{
			At:     at,
			Label:  &ir.Label{},
			Cond:   &ir.Binary{Op: ir.Lt, X: ref, Y: &ir.ArrayCall{Op: ir.ArrayLength, Args: []ir.Expr{&ir.LocalRef{Local: inner}}}},
			Update: &ir.Assign{Local: j, Value: &ir.Binary{Op: ir.Add, X: ref, Y: &ir.NumberConst{Value: 1}}},
			Body:   &ir.Block{At: at, Stmts: []ir.Stmt{h.stmt(h.push(result, value))}},
		},
	}
}

// truthy is JS's ToBoolean of a callback's result, which is still evaluated for its effects.
func (h *helperBuilder) truthy(x ir.Expr) ir.Expr {
	switch t := x.Type(); {
	case t == ir.Bool:
		return x
	case t == ir.Void || t == ir.Undefined || t == ir.Null:
		return &ir.Sequence{X: x, Y: &ir.BoolConst{Value: false}}
	case ir.IsNumeric(t) || t == ir.String || t == ir.Value:
		return &ir.Truthy{X: x}
	}
	return &ir.Sequence{X: x, Y: &ir.BoolConst{Value: true}} // an object or function, never null
}

func (h *helperBuilder) length() ir.Expr {
	return &ir.ArrayCall{Op: ir.ArrayLength, Args: []ir.Expr{&ir.LocalRef{Local: h.array}}}
}

func (h *helperBuilder) element(index ir.Expr) ir.Expr {
	return &ir.ArrayCall{Op: ir.ArrayElementChecked, Args: []ir.Expr{&ir.LocalRef{Local: h.array}, index}}
}

func (h *helperBuilder) push(result *ir.Local, value ir.Expr) ir.Expr {
	elem := result.Type.(*ir.Array).Elem
	if converted := h.l.convert(value, elem, h.node); converted != nil {
		value = converted
	} else {
		h.failed = true
	}
	return &ir.ArrayCall{Op: ir.ArrayPush, Args: []ir.Expr{&ir.LocalRef{Local: result}, value}}
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
