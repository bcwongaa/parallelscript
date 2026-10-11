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
	elemType := l.irType(elem, node)
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
	return l.checker.IsArrayType(l.checker.GetTypeAtLocation(node))
}

// arrayLiteral lowers `[a, b, c]`: an array of the literal's contextual type if that is
// an array type, so that `const s: Shape[] = [circle]` holds Shapes, and otherwise of its own.
func (l *lowerer) arrayLiteral(node *ast.Node) ir.Expr {
	t := l.checker.GetTypeAtLocation(node)
	if contextual := l.checker.GetContextualType(node, checker.ContextFlagsNone); contextual != nil && l.checker.IsArrayType(contextual) {
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
	array := arrayType.(*ir.Array)
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

// arrayMethods are the array methods lowered so far, by name: those that take values of
// the element type, and those that return an element or undefined (the dynamic op, then
// the one a `!` after the call asks for).
var (
	arrayInserters = map[string]ir.ArrayOp{"push": ir.ArrayPush, "unshift": ir.ArrayUnshift}
	arrayRemovers  = map[string][2]ir.ArrayOp{"pop": {ir.ArrayPop, ir.ArrayPopChecked}, "shift": {ir.ArrayShift, ir.ArrayShiftChecked}}
)

// arrayMethodCall lowers `a.m(args)` for an array a. checked is true under `!`
// (`stack.pop()!`), where pop and shift return the element itself and throw if a is empty.
func (l *lowerer) arrayMethodCall(call *ast.CallExpression, checked bool) ir.Expr {
	callee := ast.SkipParentheses(call.Expression)
	name := callee.AsPropertyAccessExpression().Name().Text()
	inserter, inserts := arrayInserters[name]
	removers, removes := arrayRemovers[name]
	if !inserts && !removes {
		l.notSupportedYet(callee, fmt.Sprintf("the array method `%s` is", name))
		return nil
	}
	array := l.arrayObject(callee)
	if array == nil {
		return nil
	}
	if removes {
		op := removers[0]
		if checked {
			op = removers[1]
		}
		return &ir.ArrayCall{Op: op, Args: []ir.Expr{array}}
	}
	args := []ir.Expr{array}
	elem := array.Type().(*ir.Array).Elem
	for _, argument := range call.Arguments.Nodes {
		if argument.Kind == ast.KindSpreadElement {
			l.notSupportedYet(argument, "spread arguments are")
			return nil
		}
		value := l.expression(argument)
		if value == nil {
			return nil
		}
		if value = l.convert(value, elem, argument); value == nil {
			return nil
		}
		args = append(args, value)
	}
	return &ir.ArrayCall{Op: inserter, Args: args}
}

// isArrayMethodCall reports whether node calls a method of an array.
func (l *lowerer) isArrayMethodCall(node *ast.Node) bool {
	if node.Kind != ast.KindCallExpression {
		return false
	}
	callee := ast.SkipParentheses(node.AsCallExpression().Expression)
	return callee.Kind == ast.KindPropertyAccessExpression && l.isArrayExpression(callee.Expression())
}

// isArrayIsArray reports whether a callee is the global Array.isArray.
func (l *lowerer) isArrayIsArray(callee *ast.Node) bool {
	if callee.Kind != ast.KindPropertyAccessExpression || callee.AsPropertyAccessExpression().Name().Text() != "isArray" {
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

// nonNull lowers `x!`. Under it, a.pop() and a.shift() give the element itself, and
// throw if a is empty; elsewhere `!` changes nothing (values that may be undefined or
// null have no IR type yet).
func (l *lowerer) nonNull(node *ast.Node) ir.Expr {
	inner := ast.SkipParentheses(node.Expression())
	if l.isArrayMethodCall(inner) {
		if _, removes := arrayRemovers[ast.SkipParentheses(inner.Expression()).AsPropertyAccessExpression().Name().Text()]; removes {
			return l.arrayMethodCall(inner.AsCallExpression(), true)
		}
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
