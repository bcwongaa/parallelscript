package lower

import (
	"fmt"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/pts/ir"
)

// arrayOf returns the array type of an element type: one per element type.
func (l *lowerer) arrayOf(elem ir.Type) *ir.Array {
	array, ok := l.arrays[elem]
	if !ok {
		array = &ir.Array{Elem: elem}
		l.arrays[elem] = array
	}
	return array
}

// arrayType maps an array type of the checker (T[], Array<T>, readonly T[]) to its IR
// type, or reports why it can't (at node) and returns nil. The elements of an empty
// literal's type (never[]) are dynamic values, which they can't be anyway.
func (l *lowerer) arrayType(t *checker.Type, node *ast.Node) ir.Type {
	elem := l.checker.GetElementTypeOfArrayType(t)
	if elem.Flags()&checker.TypeFlagsNever != 0 {
		return l.arrayOf(ir.Value)
	}
	if l.arraysInProgress[t] {
		l.notSupportedYet(node, fmt.Sprintf("recursive array types (%s) are", l.checker.TypeToString(t)))
		return nil
	}
	l.arraysInProgress[t] = true
	elemType := l.irType(elem, node)
	delete(l.arraysInProgress, t)
	if elemType == nil {
		return nil
	}
	return l.arrayOf(elemType)
}

func isArray(t ir.Type) bool {
	_, ok := t.(*ir.Array)
	return ok
}

// isArrayExpression reports whether tsc types node as an array.
func (l *lowerer) isArrayExpression(node *ast.Node) bool {
	return l.checker.IsArrayType(l.typeAt(node))
}

// arrayLiteral lowers `[a, b, c]`: an array of the literal's contextual type if that is
// an array type, so that `const s: Shape[] = [circle]` holds Shapes, and otherwise of its own.
func (l *lowerer) arrayLiteral(node *ast.Node) ir.Expr {
	t := l.typeAt(node)
	if contextual := l.contextualType(node); contextual != nil && l.checker.IsArrayType(contextual) {
		t = contextual
	}
	if !l.checker.IsArrayType(t) {
		l.notSupportedYet(node, fmt.Sprintf("array literals of type %s are", l.checker.TypeToString(t)))
		return nil
	}
	arrayType := l.arrayType(t, node)
	if arrayType == nil {
		return nil
	}
	return l.arrayLiteralOf(node, arrayType.(*ir.Array))
}

// arrayLiteralOf lowers an array literal to a new array of type array, or reports why it
// can't and returns nil.
func (l *lowerer) arrayLiteralOf(node *ast.Node, array *ir.Array) ir.Expr {
	elements := node.AsArrayLiteralExpression().Elements.Nodes
	elems := make([]ir.Expr, len(elements))
	for i, element := range elements {
		switch element.Kind {
		case ast.KindSpreadElement:
			l.notSupportedYet(element, "spreading `...` into an array literal is")
			return nil
		case ast.KindOmittedExpression:
			l.notSupportedYet(node, "arrays with holes are")
			return nil
		}
		elem := l.expression(element)
		if elem == nil {
			return nil
		}
		if elems[i] = l.convert(elem, array.Elem, element); elems[i] == nil {
			return nil
		}
	}
	return &ir.NewArray{Array: array, Elems: elems}
}

// isEmptyArrayLiteral reports whether x is the array of a literal without elements.
func isEmptyArrayLiteral(x ir.Expr) bool {
	literal, ok := x.(*ir.NewArray)
	return ok && len(literal.Elems) == 0
}

// isArrayElement reports whether node is a[i] for an array a.
func (l *lowerer) isArrayElement(node *ast.Node) bool {
	return node.Kind == ast.KindElementAccessExpression && l.isArrayExpression(node.Expression())
}

// arrayElement lowers a[i]: ArrayElement where any value can go, which is undefined
// for an index out of range, and ArrayElementChecked elsewhere, which throws (D-015).
func (l *lowerer) arrayElement(node *ast.Node, op ir.ArrayOp) ir.Expr {
	array, index := l.arrayElementOperands(node)
	if array == nil {
		return nil
	}
	return &ir.ArrayCall{Op: op, Args: []ir.Expr{array, index}}
}

// arrayElementOperands lowers the array and the index of a[i], or reports the access and returns nils.
func (l *lowerer) arrayElementOperands(node *ast.Node) (ir.Expr, ir.Expr) {
	if ast.IsOptionalChain(node) {
		l.notSupportedYet(node, "optional chaining `?.` is")
		return nil, nil
	}
	access := node.AsElementAccessExpression()
	array := l.expression(access.Expression)
	index := l.expression(access.ArgumentExpression)
	if array == nil || index == nil {
		return nil, nil
	}
	if !ir.IsNumeric(index.Type()) {
		l.notSupportedYet(access.ArgumentExpression, fmt.Sprintf("indexing an array with a %s is", index.Type()))
		return nil, nil
	}
	if index = l.convert(index, ir.Number, access.ArgumentExpression); index == nil {
		return nil, nil
	}
	return array, index
}

// arrayProperty lowers `a.length`, the one property of arrays (methods are calls).
func (l *lowerer) arrayProperty(node *ast.Node) ir.Expr {
	if name := node.AsPropertyAccessExpression().Name().Text(); name != "length" {
		l.notSupportedYet(node, fmt.Sprintf("the array property `%s` is", name))
		return nil
	}
	array := l.arrayObject(node)
	if array == nil {
		return nil
	}
	return &ir.ArrayCall{Op: ir.ArrayLength, Args: []ir.Expr{array}}
}

// arrayObject lowers the array of `a.p` or `a.m(…)`, or reports why it can't and returns nil.
func (l *lowerer) arrayObject(access *ast.Node) ir.Expr {
	if ast.IsOptionalChain(access) {
		l.notSupportedYet(access, "optional chaining `?.` is")
		return nil
	}
	return l.expression(access.Expression())
}

// arrayRemovers are the array methods that return an element or undefined: the dynamic
// op, then the one a `!` after the call asks for.
var arrayRemovers = map[string][2]ir.ArrayOp{"pop": {ir.ArrayPop, ir.ArrayPopChecked}, "shift": {ir.ArrayShift, ir.ArrayShiftChecked}}

// arrayParam is a parameter of an array method: its type (nil for the element type) and
// the argument a call that leaves it out gets (nil if it can't be left out).
type arrayParam struct {
	t       ir.Type
	leftOut ir.Expr
}

var (
	elementParam = arrayParam{}
	startParam   = arrayParam{ir.Number, fromStart}
	endParam     = arrayParam{ir.Number, toEnd}
)

// arrayMethods are the array methods with fixed parameters, and their ops.
var arrayMethods = map[string]struct {
	op     ir.ArrayOp
	params []arrayParam
}{
	"slice":       {ir.ArraySlice, []arrayParam{startParam, endParam}},
	"indexOf":     {ir.ArrayIndexOf, []arrayParam{elementParam, startParam}},
	"lastIndexOf": {ir.ArrayLastIndexOf, []arrayParam{elementParam, endParam}},
	"includes":    {ir.ArrayIncludes, []arrayParam{elementParam, startParam}},
	"join":        {ir.ArrayJoin, []arrayParam{{ir.String, &ir.StringConst{Value: ","}}}},
	"reverse":     {ir.ArrayReverse, nil},
	"sort":        {ir.ArraySort, nil},
	"fill":        {ir.ArrayFill, []arrayParam{elementParam, startParam, endParam}},
}

// arrayMethodCall lowers `a.m(args)` for an array a. checked is true under `!`
// (`stack.pop()!`), where pop and shift return the element itself and throw if a is empty.
func (l *lowerer) arrayMethodCall(call *ast.CallExpression, checked bool) ir.Expr {
	callee := ast.SkipParentheses(call.Expression)
	name := callee.AsPropertyAccessExpression().Name().Text()
	method, fixed := arrayMethods[name]
	removers, removes := arrayRemovers[name]
	sortWith := name == "sort" && len(call.Arguments.Nodes) > 0
	if !fixed && !removes && !callbackMethods[name] && name != "push" && name != "unshift" && name != "splice" && name != "concat" && name != "flat" {
		l.notSupportedYet(callee, fmt.Sprintf("the array method `%s` is", name))
		return nil
	}
	for _, argument := range call.Arguments.Nodes {
		if argument.Kind == ast.KindSpreadElement {
			l.notSupportedYet(argument, "spread arguments are")
			return nil
		}
	}
	array := l.arrayObject(callee)
	if array == nil {
		return nil
	}
	arrayType := array.Type().(*ir.Array)
	args := call.Arguments.Nodes
	switch {
	case callbackMethods[name]:
		return l.callbackMethodCall(call, name, array, checked)
	case sortWith:
		return l.sortWith(array, args[0])
	case removes:
		op := removers[0]
		if checked {
			op = removers[1]
		}
		return &ir.ArrayCall{Op: op, Args: []ir.Expr{array}}
	case fixed:
		operands := l.arrayOperands(args, method.params, arrayType)
		if operands == nil {
			return nil
		}
		return &ir.ArrayCall{Op: method.op, Args: append([]ir.Expr{array}, operands...)}
	case name == "push" || name == "unshift":
		values := l.arrayValues(args, arrayType, false)
		if values == nil {
			return nil
		}
		op := ir.ArrayPush
		if name == "unshift" {
			op = ir.ArrayUnshift
		}
		return &ir.ArrayCall{Op: op, Args: append([]ir.Expr{array}, values...)}
	case name == "splice":
		return l.splice(array, args)
	case name == "concat":
		values := l.arrayValues(args, arrayType, true)
		if values == nil {
			return nil
		}
		return &ir.ArrayCall{Op: ir.ArrayConcat, Args: append([]ir.Expr{array}, values...)}
	}
	return l.flat(array, args)
}

// sortWith lowers a.sort(compare): compare becomes a function value that takes two
// elements and returns a number.
func (l *lowerer) sortWith(array ir.Expr, arg *ast.Node) ir.Expr {
	compare := l.expression(arg)
	if compare == nil {
		return nil
	}
	elem := array.Type().(*ir.Array).Elem
	if compare = l.convert(compare, l.funcType([]ir.Type{elem, elem}, ir.Number), arg); compare == nil {
		return nil
	}
	return &ir.ArrayCall{Op: ir.ArraySortWith, Args: []ir.Expr{array, compare}}
}

// arrayOperands lowers the arguments of an array method with fixed parameters, giving
// left-out ones their defaults, or reports one and returns nil.
func (l *lowerer) arrayOperands(args []*ast.Node, params []arrayParam, array *ir.Array) []ir.Expr {
	operands := make([]ir.Expr, len(params))
	for i, param := range params {
		t := param.t
		if t == nil {
			t = array.Elem
		}
		if i >= len(args) || isUndefined(args[i]) && param.leftOut != nil {
			operands[i] = param.leftOut // tsc checked that required arguments are there
			continue
		}
		value := l.expression(args[i])
		if value == nil {
			return nil
		}
		if operands[i] = l.convert(value, t, args[i]); operands[i] == nil {
			return nil
		}
	}
	return operands
}

// isUndefined reports whether node is the identifier undefined, which a left-out argument is.
func isUndefined(node *ast.Node) bool {
	return node.Kind == ast.KindIdentifier && node.Text() == "undefined"
}

// arrayValues lowers arguments that are values of an array's element type, or with
// arrays true, also arrays of its type (concat's).
func (l *lowerer) arrayValues(args []*ast.Node, array *ir.Array, arrays bool) []ir.Expr {
	values := make([]ir.Expr, len(args))
	for i, arg := range args {
		var value ir.Expr
		if literal := ast.SkipParentheses(arg); arrays && literal.Kind == ast.KindArrayLiteralExpression {
			value = l.arrayLiteralOf(literal, array) // tsc types [7] in a.concat([7]) number[], whatever a's elements are
		} else {
			value = l.expression(arg)
		}
		if value == nil {
			return nil
		}
		if arrays && value.Type() == array {
			values[i] = value
			continue
		}
		if values[i] = l.convert(value, array.Elem, arg); values[i] == nil {
			return nil
		}
	}
	return values
}

// splice lowers a.splice(start, deleteCount, items…). Without arguments it removes
// nothing; without a delete count it removes everything from start on.
func (l *lowerer) splice(array ir.Expr, args []*ast.Node) ir.Expr {
	arrayType := array.Type().(*ir.Array)
	if len(args) == 0 {
		return &ir.ArrayCall{Op: ir.ArraySplice, Args: []ir.Expr{array, &ir.NumberConst{Value: 0}, &ir.NumberConst{Value: 0}}}
	}
	bounds := l.arrayOperands(args[:min(len(args), 2)], []arrayParam{startParam, endParam}, arrayType)
	items := l.arrayValues(args[min(len(args), 2):], arrayType, false)
	if bounds == nil || items == nil {
		return nil
	}
	return &ir.ArrayCall{Op: ir.ArraySplice, Args: append(append([]ir.Expr{array}, bounds...), items...)}
}

// flat lowers a.flat(depth) for a constant depth (1 if left out): a new array with the
// nested arrays depth levels down spread into it.
func (l *lowerer) flat(array ir.Expr, args []*ast.Node) ir.Expr {
	depth := 1.0
	if len(args) > 0 {
		constant, ok := l.expression(args[0]).(*ir.NumberConst)
		if !ok {
			l.notSupportedYet(args[0], "`flat` with a depth that isn't a constant is")
			return nil
		}
		depth = constant.Value
	}
	result := array.Type().(*ir.Array)
	for d := depth; d >= 1; d-- {
		inner, ok := result.Elem.(*ir.Array)
		if !ok {
			break
		}
		result = inner
	}
	return &ir.ArrayCall{Op: ir.ArrayFlat, Args: []ir.Expr{array, &ir.NumberConst{Value: depth}}, To: result}
}

// isArrayMethodCall reports whether node calls a method of an array.
func (l *lowerer) isArrayMethodCall(node *ast.Node) bool {
	if node.Kind != ast.KindCallExpression {
		return false
	}
	callee := ast.SkipParentheses(node.AsCallExpression().Expression)
	return callee.Kind == ast.KindPropertyAccessExpression && l.isArrayExpression(callee.Expression())
}

// isArrayStatic reports whether a callee is the static method name of the global Array.
func (l *lowerer) isArrayStatic(callee *ast.Node, name string) bool {
	if callee.Kind != ast.KindPropertyAccessExpression || callee.AsPropertyAccessExpression().Name().Text() != name {
		return false
	}
	object := callee.Expression()
	return object.Kind == ast.KindIdentifier && object.Text() == "Array" && l.isDeclaredInDefaultLib(l.target(object))
}

// arrayIsArray lowers Array.isArray(x), which the static type of x decides.
func (l *lowerer) arrayIsArray(call *ast.CallExpression) ir.Expr {
	x := l.valueExpression(call.Arguments.Nodes[0])
	if x == nil {
		return nil
	}
	if x.Type() == ir.Value {
		l.notSupportedYet(call.Arguments.Nodes[0], "`Array.isArray` of a dynamic value is")
		return nil
	}
	return discard(x, &ir.BoolConst{Value: isArray(x.Type())})
}

// arrayFrom lowers Array.from(x) for an array x (a copy) or a string (its code points).
func (l *lowerer) arrayFrom(call *ast.CallExpression) ir.Expr {
	args := call.Arguments.Nodes
	if len(args) > 2 {
		l.notSupportedYet(args[2], "the `thisArg` argument of `Array.from` is")
		return nil
	}
	if it, ok := l.mapIterableOf(args[0]); ok {
		if it.m == nil {
			return nil // reported
		}
		return l.arrayFromMap(call, it)
	}
	x := l.expression(args[0])
	switch {
	case x == nil:
		return nil
	case len(args) == 2 && (isArray(x.Type()) || x.Type() == ir.String):
		return l.arrayFromWithMapper(call, x)
	case isArray(x.Type()):
		return &ir.ArrayCall{Op: ir.ArraySlice, Args: []ir.Expr{x, fromStart, toEnd}}
	case x.Type() == ir.String:
		return &ir.CallBuiltin{Builtin: ir.ArrayFromString, Args: []ir.Expr{x}}
	}
	l.notSupportedYet(args[0], fmt.Sprintf("`Array.from` of a %s is", x.Type()))
	return nil
}

// nonNull lowers `x!`. Under it, a.pop() and a.shift() give the element itself, and
// throw if a is empty; elsewhere `!` changes nothing (values that may be undefined or
// null have no IR type yet).
func (l *lowerer) nonNull(node *ast.Node) ir.Expr {
	inner := ast.SkipParentheses(node.Expression())
	if l.isArrayMethodCall(inner) {
		name := ast.SkipParentheses(inner.Expression()).AsPropertyAccessExpression().Name().Text()
		if _, removes := arrayRemovers[name]; removes || name == "find" || name == "findLast" {
			return l.arrayMethodCall(inner.AsCallExpression(), true)
		}
	}
	if l.isMapMethodCall(inner) && ast.SkipParentheses(inner.Expression()).AsPropertyAccessExpression().Name().Text() == "get" {
		return l.mapMethodCall(inner.AsCallExpression(), true)
	}
	return l.expression(inner)
}

// forOfStatement lowers `for (const x of a) body` for an array a to a loop over its
// indices: like JS's array iterator, it reads the length before each iteration, so it
// sees elements pushed meanwhile.
func (l *lowerer) forOfStatement(node *ast.Node, label *ir.Label) []ir.Stmt {
	s := node.AsForInOrOfStatement()
	switch {
	case s.AwaitModifier != nil:
		l.notSupportedYet(node, "`for await` is")
		return nil
	case s.Initializer.Kind != ast.KindVariableDeclarationList || len(s.Initializer.AsVariableDeclarationList().Declarations.Nodes) != 1:
		l.notSupportedYet(s.Initializer, "`for...of` without a declaration of its variable is")
		return nil
	}
	declaration := s.Initializer.AsVariableDeclarationList().Declarations.Nodes[0]
	if declaration.Name().Kind != ast.KindIdentifier {
		l.notSupportedYet(declaration.Name(), "destructuring is")
		return nil
	}
	if it, ok := l.mapIterableOf(s.Expression); ok {
		if it.m == nil {
			return nil // reported
		}
		symbol := l.checker.GetSymbolAtLocation(declaration.Name())
		t := l.bindingType(symbol, declaration.Name())
		if t == nil {
			return nil
		}
		item := l.newLocal(declaration.Name().Text(), t)
		l.locals[symbol] = item
		return l.mapForOf(node, label, it, item, s.Statement)
	}
	iterable := l.expression(s.Expression)
	if iterable == nil {
		return nil
	}
	arrayType, ok := iterable.Type().(*ir.Array)
	if !ok {
		l.notSupportedYet(s.Expression, fmt.Sprintf("`for...of` over a %s is", iterable.Type()))
		return nil
	}
	at := l.pos(node)
	array, index := l.newLocal("array", arrayType), l.newLocal("index", ir.Number)
	symbol := l.checker.GetSymbolAtLocation(declaration.Name())
	t := l.bindingType(symbol, declaration.Name())
	if t == nil {
		return nil
	}
	element := l.newLocal(declaration.Name().Text(), t)
	l.locals[symbol] = element
	value := l.convert(&ir.ArrayCall{Op: ir.ArrayElementChecked, Args: []ir.Expr{&ir.LocalRef{Local: array}, &ir.LocalRef{Local: index}}}, t, declaration)
	if value == nil {
		return nil
	}
	body := l.statementBlock(s.Statement)
	body.Stmts = append([]ir.Stmt{&ir.LocalDecl{At: l.pos(declaration), Local: element, Init: value}}, body.Stmts...)
	loop := &ir.Loop{
		At:     at,
		Label:  label,
		Cond:   &ir.Binary{Op: ir.Lt, X: &ir.LocalRef{Local: index}, Y: &ir.ArrayCall{Op: ir.ArrayLength, Args: []ir.Expr{&ir.LocalRef{Local: array}}}},
		Update: &ir.Assign{Local: index, Value: &ir.Binary{Op: ir.Add, X: &ir.LocalRef{Local: index}, Y: &ir.NumberConst{Value: 1}}},
		Body:   body,
	}
	return []ir.Stmt{&ir.Block{At: at, Stmts: []ir.Stmt{
		&ir.LocalDecl{At: at, Local: array, Init: iterable},
		&ir.LocalDecl{At: at, Local: index, Init: &ir.NumberConst{Value: 0}},
		loop,
	}}}
}
