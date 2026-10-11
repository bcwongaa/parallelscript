package lower

import (
	"fmt"
	"slices"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/pts/ir"
)

// The array methods and functions of M3.5c2 (D-085): the copying methods (toSorted,
// toReversed, toSpliced, with), `new Array(n)`, Array.of, Array.from({ length }), and
// keys, values and entries where they are iterated.

// copyingMethods are the array methods that return a changed copy.
var copyingMethods = map[string]bool{"toSorted": true, "toReversed": true, "toSpliced": true, "with": true}

// copyingMethodCall lowers a.toSorted(compare?), a.toReversed(), a.toSpliced(…) and
// a.with(i, v): the method that changes an array in place, on a copy.
func (l *lowerer) copyingMethodCall(call *ast.CallExpression, name string, array ir.Expr) ir.Expr {
	args := call.Arguments.Nodes
	arrayType := array.Type().(*ir.Array)
	copied := &ir.ArrayCall{Op: ir.ArrayCopy, Args: []ir.Expr{array}}
	switch name {
	case "toSorted":
		if len(args) > 0 && !isUndefined(args[0]) {
			return l.sortWith(copied, args[0])
		}
		return &ir.ArrayCall{Op: ir.ArraySort, Args: []ir.Expr{copied}}
	case "toReversed":
		return &ir.ArrayCall{Op: ir.ArrayReverse, Args: []ir.Expr{copied}}
	case "toSpliced":
		local := l.newLocal("copy", arrayType)
		splice := l.splice(&ir.LocalRef{Local: local}, args)
		if splice == nil {
			return nil
		}
		return &ir.Bind{Local: local, Value: copied, In: &ir.Sequence{X: splice, Y: &ir.LocalRef{Local: local}}}
	}
	operands := l.arrayOperands(args, []arrayParam{{ir.Number, nil}, elementParam}, arrayType) // with(index, value)
	if operands == nil {
		return nil
	}
	return &ir.ArrayCall{Op: ir.ArrayWith, Args: append([]ir.Expr{array}, operands...)}
}

// isArrayConstructor reports whether a callee is the global Array.
func (l *lowerer) isArrayConstructor(callee *ast.Node) bool {
	return callee.Kind == ast.KindIdentifier && callee.Text() == "Array" && l.isDeclaredInDefaultLib(l.target(callee))
}

// arrayConstructor lowers `new Array(…)` and `Array(…)` (node, with its arguments): one
// numeric argument is a length (holes, or zeros of a primitive element type, D-085), any
// other arguments are the elements. Without type arguments the array takes the type its
// context gives it, where tsc says any[].
func (l *lowerer) arrayConstructor(node *ast.Node, arguments []*ast.Node) ir.Expr {
	t := l.irType(l.typeAt(node), node)
	if contextual := l.contextualType(node); contextual != nil && node.TypeArgumentList() == nil {
		if ct, ok := l.irType(contextual, node).(*ir.Array); ok && ct != nil {
			t = ct
		}
	}
	array, ok := t.(*ir.Array)
	if !ok {
		if t != nil {
			l.notSupportedYet(node, fmt.Sprintf("`new Array` of type %s is", t))
		}
		return nil
	}
	if slices.ContainsFunc(arguments, isSpreadElement) {
		return l.spreadArray(arguments, array)
	}
	if len(arguments) == 1 && l.hasTypeFlags(arguments[0], checker.TypeFlagsNumberLike) {
		length := l.expression(arguments[0])
		if length == nil {
			return nil
		}
		return &ir.NewArray{Array: array, Length: toNumber(length)}
	}
	elems := l.arrayValues(arguments, array, false)
	if elems == nil {
		return nil
	}
	return &ir.NewArray{Array: array, Elems: elems}
}

// arrayOfCall lowers Array.of(…): an array of the arguments, spread arrays' elements in place.
func (l *lowerer) arrayOfCall(call *ast.CallExpression) ir.Expr {
	array, ok := l.irType(l.typeAt(call.AsNode()), call.AsNode()).(*ir.Array)
	if !ok {
		l.notSupportedYet(call.AsNode(), "this `Array.of` is")
		return nil
	}
	return l.spreadArray(call.Arguments.Nodes, array)
}

// lengthObject returns the expression of n in `{ length: n }` (or `{ length }`), the
// array-like object Array.from makes arrays of n elements from, or nil.
func lengthObject(node *ast.Node) *ast.Node {
	node = ast.SkipParentheses(node)
	if node.Kind != ast.KindObjectLiteralExpression {
		return nil
	}
	properties := node.AsObjectLiteralExpression().Properties.Nodes
	if len(properties) != 1 || properties[0].Name() == nil || properties[0].Name().Text() != "length" {
		return nil
	}
	switch property := properties[0]; property.Kind {
	case ast.KindPropertyAssignment:
		return property.Initializer()
	case ast.KindShorthandPropertyAssignment:
		return property.Name()
	}
	return nil
}

// arrayFromLength lowers Array.from({ length: n }, mapper?): the mapper's results for
// (undefined, i) for each i below n, or n undefineds.
func (l *lowerer) arrayFromLength(call *ast.CallExpression, lengthNode *ast.Node) ir.Expr {
	length := l.expression(lengthNode)
	if length == nil {
		return nil
	}
	if length = l.toNumberOrReport(length, lengthNode); length == nil {
		return nil
	}
	count := &ir.CallBuiltin{Builtin: ir.MathFloor, Args: []ir.Expr{length}} // ToLength, for the lengths a loop can count to
	if len(call.Arguments.Nodes) == 1 {
		undefineds := &ir.NewArray{Array: ir.ValueArray, Length: &ir.CallBuiltin{Builtin: ir.MathMax, Args: []ir.Expr{count, &ir.NumberConst{Value: 0}}}}
		return &ir.ArrayCall{Op: ir.ArrayFill, Args: []ir.Expr{undefineds, box(&ir.UndefinedConst{}), fromStart, toEnd}}
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
	helper := l.lengthMapHelper(ft, call.Expression)
	if helper == nil {
		return nil
	}
	return &ir.Call{Func: helper, Args: []ir.Expr{count, mapper}}
}

// lengthMapHelper returns the helper of Array.from({ length }, mapper) for a mapper of
// type ft: `result = []; for (i = 0; i < count; i++) result.push(mapper(undefined, i))`.
func (l *lowerer) lengthMapHelper(ft *ir.FuncType, node *ast.Node) *ir.Func {
	key := helperKey{method: "Array.from length", callback: ft}
	if helper, ok := l.helpers[key]; ok {
		return helper
	}
	if ft.Result == ir.Void {
		l.notSupportedYet(node, "`Array.from` with a mapping function that returns nothing is")
		l.helpers[key] = nil
		return nil
	}
	h := &helperBuilder{l: l, key: key, node: node}
	resultType := l.arrayOf(ft.Result)
	h.fn = &ir.Func{Name: "Array.from", Module: l.function.Module, Pos: l.pos(node), Result: resultType}
	l.inFunction(h.fn, func() {
		count, index, result := l.newLocal("count", ir.Number), l.newLocal("index", ir.Number), l.newLocal("result", resultType)
		h.callback = l.newLocal("mapper", ft)
		h.fn.Params = []*ir.Local{count, h.callback}
		i := &ir.LocalRef{Local: index}
		mapped := h.callWith([]ir.Expr{box(&ir.UndefinedConst{}), i})
		h.fn.Body = &ir.Block{At: h.fn.Pos, Stmts: []ir.Stmt{
			h.declare(result, &ir.NewArray{Array: resultType}),
			h.declare(index, &ir.NumberConst{Value: 0}),
			&ir.Loop{
				At:     h.fn.Pos,
				Label:  &ir.Label{},
				Cond:   &ir.Binary{Op: ir.Lt, X: i, Y: &ir.LocalRef{Local: count}},
				Update: &ir.Assign{Local: index, Value: &ir.Binary{Op: ir.Add, X: i, Y: &ir.NumberConst{Value: 1}}},
				Body:   &ir.Block{At: h.fn.Pos, Stmts: []ir.Stmt{h.stmt(h.push(result, mapped))}},
			},
			h.ret(&ir.LocalRef{Local: result}),
		}}
	})
	if h.failed {
		l.helpers[key] = nil
		return nil
	}
	l.helpers[key] = h.fn
	return h.fn
}

// arrayIteration lowers `a.keys()`, `a.values()` and `a.entries()` of an array where
// they are iterated (`for…of`, spread, Array.from), as arrays: the indices, a itself, and
// [index, element] pairs. ok is false if node is none of these; iterators as values come
// with M4.1.
func (l *lowerer) arrayIteration(node *ast.Node) (x ir.Expr, ok bool) {
	node = ast.SkipParentheses(node)
	if node.Kind != ast.KindCallExpression || len(node.Arguments()) > 0 {
		return nil, false
	}
	callee := ast.SkipParentheses(node.AsCallExpression().Expression)
	member := l.libMemberName(callee)
	if member != "Array.keys" && member != "Array.values" && member != "Array.entries" && member != "ReadonlyArray.keys" &&
		member != "ReadonlyArray.values" && member != "ReadonlyArray.entries" {
		return nil, false
	}
	array := l.arrayObject(callee)
	if array == nil {
		return nil, true
	}
	switch callee.Name().Text() {
	case "keys":
		return &ir.ArrayCall{Op: ir.ArrayKeys, Args: []ir.Expr{array}}, true
	case "values":
		return array, true
	}
	return &ir.Call{Func: l.entriesHelper(array.Type().(*ir.Array), node), Args: []ir.Expr{array}}, true
}

// entriesHelper returns the helper that makes the [index, element] pairs of an array of type at.
func (l *lowerer) entriesHelper(at *ir.Array, node *ast.Node) *ir.Func {
	key := helperKey{method: "Array.entries", array: at}
	if helper, ok := l.helpers[key]; ok {
		return helper
	}
	pair := l.tupleOf([]ir.Type{ir.Number, at.Elem})
	resultType := l.arrayOf(pair)
	h := &helperBuilder{l: l, key: key, node: node}
	h.fn = &ir.Func{Name: "Array.entries", Module: l.function.Module, Pos: l.pos(node), Result: resultType}
	l.inFunction(h.fn, func() {
		h.array = l.newLocal("array", at)
		h.fn.Params = []*ir.Local{h.array}
		result := l.newLocal("result", resultType)
		h.fn.Body = &ir.Block{At: h.fn.Pos, Stmts: h.loop(false, []ir.Stmt{h.declare(result, &ir.NewArray{Array: resultType})}, func(element, index ir.Expr) []ir.Stmt {
			return []ir.Stmt{h.stmt(h.push(result, &ir.NewObject{Shape: pair, Values: []ir.Expr{index, element}}))}
		}, h.ret(&ir.LocalRef{Local: result}))}
	})
	l.helpers[key] = h.fn
	return h.fn
}

// iterable lowers an expression that is iterated: `a.keys()`, `a.values()` and
// `a.entries()` as arrays (arrayIteration), anything else as usual.
func (l *lowerer) iterable(node *ast.Node) ir.Expr {
	if x, ok := l.arrayIteration(node); ok {
		return x
	}
	return l.expression(node)
}
