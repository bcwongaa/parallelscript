package lower

import (
	"fmt"
	"strconv"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/internal/scanner"
	"github.com/microsoft/typescript-go/pts/ir"
)

// Generators (D-109). A generator function is an ir.Func marked Generator, whose calls
// return a generator object, a dynamic value (Value) that the runtime knows by its
// descriptor; its body yields with ir.Yield statements and returns its return value boxed.
// The iterator types of the ES library (Generator, Iterator, IterableIterator,
// IteratorObject) are dynamic values too, and their results (IteratorYieldResult,
// IteratorReturnResult) shapes of a done and a value field.
//
// A yield can only come first in its statement: as the statement (`yield x;`), as the
// initializer of the first variable a statement declares (`const v = yield x;`), as the
// right side of an assignment (`v = yield x;`) or as a return's value (`return yield x;`).
// Then nothing the statement evaluated before it has to be kept in the frame while the
// generator is suspended.

// iteratorTypes are the ES library's types of iterator objects.
var iteratorTypes = map[string]bool{"Generator": true, "Iterator": true, "IterableIterator": true, "IteratorObject": true,
	"MapIterator": true, "SetIterator": true, "ArrayIterator": true, "StringIterator": true, "RegExpStringIterator": true}

// isIteratorType reports whether t is one of the ES library's iterator types.
func (l *lowerer) isIteratorType(t *checker.Type) bool {
	symbol := t.Symbol()
	return symbol != nil && t.Flags()&checker.TypeFlagsObject != 0 && iteratorTypes[symbol.Name] && l.isLibInterface(symbol)
}

// isIterableType reports whether t is the ES library's Iterable<T>: any value that a
// for…of can iterate over, which is a dynamic value (D-110).
func (l *lowerer) isIterableType(t *checker.Type) bool {
	symbol := t.Symbol()
	return symbol != nil && t.Flags()&checker.TypeFlagsObject != 0 && symbol.Name == "Iterable" && l.isLibInterface(symbol)
}

// iteratorResultValue is, for IteratorYieldResult<T> or IteratorReturnResult<R> of the
// ES library, T or R; ok is false for any other type.
func (l *lowerer) iteratorResultValue(t *checker.Type) (value *checker.Type, ok bool) {
	symbol := t.Symbol()
	if symbol == nil || t.Flags()&checker.TypeFlagsObject == 0 || symbol.Name != "IteratorYieldResult" && symbol.Name != "IteratorReturnResult" || !l.isLibInterface(symbol) {
		return nil, false
	}
	args := l.checker.GetTypeArguments(t)
	if len(args) == 0 {
		return nil, false
	}
	return args[0], true
}

// isLibInterface reports whether symbol is declared in the ES library.
func (l *lowerer) isLibInterface(symbol *ast.Symbol) bool {
	return len(symbol.Declarations) > 0 && l.program.IsSourceFileDefaultLibrary(ast.GetSourceFileOfNode(symbol.Declarations[0]).Path())
}

// iterationTypes are the types an iterator type yields, returns and takes from next():
// its type arguments, or any where it leaves them out.
func (l *lowerer) iterationTypes(t *checker.Type) (yield, result, next *checker.Type) {
	types := []*checker.Type{l.checker.GetAnyType(), l.checker.GetAnyType(), l.checker.GetAnyType()}
	if l.isIteratorType(t) || l.isIterableType(t) {
		copy(types, l.checker.GetTypeArguments(t))
	}
	return types[0], types[1], types[2]
}

// iteratorResultShape is the shape of the results of next(), return() and throw() whose
// value is of type value: `{ done: boolean; value: T }`, printed value first, as V8 makes
// them. A value of it may be any object an iterator handed back (ir.Class.Result).
func (l *lowerer) iteratorResultShape(value ir.Type) *ir.Class {
	fields := []*ir.Field{{Name: "done", Type: ir.Bool}, {Name: "value", Type: value}}
	key := membersKey(fields, nil)
	if existing, ok := l.shapesByKey[key]; ok {
		existing.Result = true // an object type of the program's with the same fields is one, its reads testing the descriptor too
		return existing
	}
	shape := &ir.Class{Shape: true, Result: true, Name: "object" + strconv.Itoa(len(l.shapeList)+1), Fields: fields}
	for _, field := range fields {
		field.Class = shape
	}
	shape.Order([]*ir.Field{fields[1], fields[0]}) // V8's order, which copies of dynamic results take (D-115)
	l.shapesByKey[key] = shape
	l.shapeList = append(l.shapeList, shape)
	return shape
}

// generatorType is the type of the generator objects the current generator function
// makes: what tsc says it returns.
func (l *lowerer) generatorType() *checker.Type {
	return l.returnTypeOf(l.checker.GetSignatureFromDeclaration(l.funcNodes[l.function]))
}

// statementYield is the yield (or yield*, or await, D-112) that statement node evaluates
// first, in one of the places a yield can be (see above), or nil.
func statementYield(node *ast.Node) *ast.Node {
	var x *ast.Node
	switch node.Kind {
	case ast.KindExpressionStatement:
		x = ast.SkipParentheses(node.Expression())
		if x.Kind == ast.KindBinaryExpression && x.AsBinaryExpression().OperatorToken.Kind == ast.KindEqualsToken &&
			ast.SkipParentheses(x.AsBinaryExpression().Left).Kind == ast.KindIdentifier {
			x = ast.SkipParentheses(x.AsBinaryExpression().Right)
		}
	case ast.KindVariableStatement:
		declarations := node.AsVariableStatement().DeclarationList.AsVariableDeclarationList().Declarations.Nodes
		if initializer := declarations[0].Initializer(); initializer != nil {
			x = ast.SkipParentheses(initializer)
		}
	case ast.KindReturnStatement:
		if node.Expression() != nil {
			x = ast.SkipParentheses(node.Expression())
		}
	}
	if x != nil && isSuspension(x) {
		return x
	}
	return nil
}

// yieldingStatement lowers a statement that yields first: the yield, then the rest of the
// statement with the yield expression's value in its place.
func (l *lowerer) yieldingStatement(node *ast.Node, yield *ast.Node) []ir.Stmt {
	stmts, value := l.suspensionPoint(yield)
	if stmts == nil {
		return nil
	}
	if node.Kind == ast.KindExpressionStatement && ast.SkipParentheses(node.Expression()) == yield {
		return stmts // `yield x;`: its value is unused
	}
	l.yielded[yield] = value
	defer delete(l.yielded, yield)
	rest := l.statement(node)
	if rest == nil {
		return nil
	}
	return append(stmts, rest...)
}

// yieldExpression is the value of a yield expression whose statement has yielded already
// (yieldingStatement, hoistingStatement); one anywhere else is reported.
func (l *lowerer) yieldExpression(node *ast.Node) ir.Expr {
	if value, ok := l.yielded[node]; ok {
		return value
	}
	keyword := "yield"
	if node.Kind == ast.KindAwaitExpression {
		keyword = "await"
	}
	if !l.function.Generator {
		l.notSupportedYet(node, "top-level `await` outside the entry module is")
		return nil
	}
	l.notSupportedYet(node, "`"+keyword+"` in a loop's condition or update, a case label, a later variable of a declaration, a default value or an optional chain is")
	return nil
}

// yieldPoint lowers `yield x` (or `yield* x`, delegation): the statements that yield, and
// the expression for what the yield expression evaluates to once the generator resumes.
func (l *lowerer) yieldPoint(node *ast.Node) ([]ir.Stmt, ir.Expr) {
	y := node.AsYieldExpression()
	if y.AsteriskToken != nil {
		return l.delegation(node)
	}
	at := l.pos(node)
	var value ir.Expr = &ir.UndefinedConst{}
	if y.Expression != nil {
		yieldType, _, _ := l.iterationTypes(l.generatorType())
		t := l.irType(yieldType, node)
		x := l.expression(y.Expression)
		if t == nil || x == nil {
			return nil, nil
		}
		if value = l.convert(x, t, y.Expression); value == nil {
			return nil, nil
		}
	}
	stmts := append([]ir.Stmt{&ir.Yield{At: at, Value: box(value)}}, l.resumeChecks(at)...)
	return stmts, &ir.Resumed{}
}

// resumeChecks are what a generator does when it resumes after a yield by throw() or
// return(): throw what throw() passed where it was suspended, or return what return()
// passed, through the finally blocks around it.
func (l *lowerer) resumeChecks(at ir.Pos) []ir.Stmt {
	return []ir.Stmt{
		&ir.If{At: at, Cond: &ir.ResumedBy{Mode: ir.ResumeThrow}, Then: &ir.Block{At: at, Stmts: []ir.Stmt{&ir.Throw{At: at, Value: &ir.Resumed{}}}}},
		&ir.If{At: at, Cond: &ir.ResumedBy{Mode: ir.ResumeReturn}, Then: &ir.Block{At: at, Stmts: l.leaveFunction(at, &ir.Resumed{})}},
	}
}

// delegation lowers `yield* x`: a loop that resumes an iterator over x as this generator
// is resumed, by next(), throw() or return() with its value, and yields what it yields,
// until it is done. If return() finished it, this generator returns too; otherwise the
// yield* expression is what it returned, the value of its last result read once it is done.
func (l *lowerer) delegation(node *ast.Node) ([]ir.Stmt, ir.Expr) {
	y := node.AsYieldExpression()
	at := l.pos(node)
	iterable := l.valueExpression(y.Expression)
	if iterable == nil {
		return nil, nil
	}
	inner, mode, sent := l.newLocal("delegate", ir.Value), l.newLocal("mode", ir.I32), l.newLocal("sent", ir.Value)
	ref := func(local *ir.Local) ir.Expr { return &ir.LocalRef{Local: local} }
	value := &ir.CallBuiltin{Builtin: ir.IteratorValue, Args: []ir.Expr{ref(inner)}}
	resume := &ir.CallBuiltin{Builtin: ir.IteratorResume, Args: []ir.Expr{ref(inner), ref(mode), ref(sent)}}
	returned := &ir.If{At: at, Cond: &ir.Binary{Op: ir.Eq, X: ref(mode), Y: modeConst(ir.ResumeReturn)}, Then: &ir.Block{At: at, Stmts: l.leaveFunction(at, value)}}
	label := &ir.Label{}
	resumedMode := &ir.Conditional{Cond: &ir.ResumedBy{Mode: ir.ResumeThrow}, Then: modeConst(ir.ResumeThrow),
		Else: &ir.Conditional{Cond: &ir.ResumedBy{Mode: ir.ResumeReturn}, Then: modeConst(ir.ResumeReturn), Else: modeConst(ir.ResumeNext)}}
	loop := &ir.Loop{At: at, Label: label, Body: &ir.Block{At: at, Stmts: []ir.Stmt{
		&ir.If{At: at, Cond: resume, Then: &ir.Block{At: at, Stmts: []ir.Stmt{returned, &ir.Break{At: at, Target: label}}}},
		&ir.Yield{At: at, Delegate: ref(inner)}, // its value, or the inner's result object as it is
		&ir.ExprStmt{At: at, X: &ir.Assign{Local: mode, Value: resumedMode}},
		&ir.ExprStmt{At: at, X: &ir.Assign{Local: sent, Value: &ir.Resumed{}}},
	}}}
	result := l.newLocal("delegated", ir.Value)
	stmts := []ir.Stmt{
		&ir.LocalDecl{At: at, Local: inner, Init: &ir.CallBuiltin{Builtin: ir.IteratorOpen, Args: []ir.Expr{box(iterable), &ir.StringConst{Value: scanner.GetTextOfNode(y.Expression)}}}},
		&ir.LocalDecl{At: at, Local: mode, Init: modeConst(ir.ResumeNext)},
		&ir.LocalDecl{At: at, Local: sent, Init: box(&ir.UndefinedConst{})},
		loop,
		&ir.LocalDecl{At: at, Local: result, Init: value}, // read whether or not the expression's value is used, as JS's yield* reads it
	}
	return stmts, ref(result)
}

func modeConst(mode ir.ResumeMode) ir.Expr {
	return &ir.IntConst{Bits: uint64(mode), Ty: ir.I32}
}

// iteratorMethodCall lowers next(v), return(v) and throw(e) on an iterator: the iterator
// (or a runtime iterator that calls an object's own methods) called, and the result object
// its method returned, as it is (an object's own next()'s, or what a generator's yield*
// handed back, H03b), or one made of what it reports. handled is false for other calls.
func (l *lowerer) iteratorMethodCall(call *ast.CallExpression) (x ir.Expr, handled bool) {
	callee := ast.SkipParentheses(call.Expression)
	if callee.Kind == ast.KindNonNullExpression { // `it.return!(v)`: an Iterator<T>'s return and throw are optional
		callee = ast.SkipParentheses(callee.Expression())
	}
	if callee.Kind != ast.KindPropertyAccessExpression || !l.isIteratorType(l.checker.GetNonNullableType(l.typeAt(callee.Expression()))) {
		return nil, false
	}
	modes := map[string]ir.ResumeMode{"next": ir.ResumeNext, "throw": ir.ResumeThrow, "return": ir.ResumeReturn}
	mode, ok := modes[callee.Name().Text()]
	if !ok {
		return nil, false
	}
	if l.hasSpreadArgument(call) {
		return nil, true
	}
	iterator := l.expression(callee.Expression())
	resultType := l.irType(l.typeAt(call.AsNode()), call.AsNode())
	if iterator == nil || resultType == nil {
		return nil, true
	}
	var sent ir.Expr = &ir.UndefinedConst{}
	if args := call.Arguments.Nodes; len(args) > 0 {
		if sent = l.valueExpression(args[0]); sent == nil {
			return nil, true
		}
	}
	it, step := l.newLocal("iterator", ir.Value), l.newLocal("step", ir.Value)
	done := &ir.CallBuiltin{Builtin: ir.IteratorCallDone, Args: []ir.Expr{&ir.LocalRef{Local: step}}}
	made := l.iteratorResult(done, &ir.LocalRef{Local: it}, resultType, l.typeAt(call.AsNode()), call.AsNode())
	if made == nil {
		return nil, true
	}
	result := &ir.Conditional{Cond: &ir.CallBuiltin{Builtin: ir.IteratorHanded, Args: []ir.Expr{&ir.LocalRef{Local: step}}},
		Then: &ir.FromUnion{X: &ir.LocalRef{Local: step}, To: resultType}, Else: made} // the object as it is (D-178)
	called := &ir.CallBuiltin{Builtin: ir.IteratorCall, Args: []ir.Expr{&ir.LocalRef{Local: it}, modeConst(mode), box(sent), &ir.StringConst{Value: scanner.GetTextOfNode(callee)}}}
	opened := &ir.CallBuiltin{Builtin: ir.IteratorOf, Args: []ir.Expr{box(iterator)}} // an object with its own next() is wrapped (D-111)
	return &ir.Bind{Local: it, Value: opened, In: &ir.Bind{Local: step, Value: called, In: result}}, true
}

// iteratorResult makes the result object of an iterator's method: done, whether the
// iterator is done, then its value, in an object of type t (IteratorResult<T, R>, checker
// type ct): one shape, or a union of the yield result's and the return result's shapes.
func (l *lowerer) iteratorResult(done ir.Expr, iterator ir.Expr, t ir.Type, ct *checker.Type, node *ast.Node) ir.Expr {
	finished := l.newLocal("done", ir.Bool)
	value := &ir.CallBuiltin{Builtin: ir.IteratorValue, Args: []ir.Expr{iterator}}
	object := func(shape *ir.Class) ir.Expr {
		v := l.checkedConversion(value, shape.Field("value").Type, node) // a runtime iterator's entries are any[] pairs, copied into tuples
		if v == nil {
			return nil
		}
		order := []*ir.Field{shape.Field("value"), shape.Field("done")}
		return &ir.NewObject{Shape: shape, Order: shape.Order(order), Values: []ir.Expr{v, &ir.LocalRef{Local: finished}}}
	}
	var made ir.Expr
	switch t := t.(type) {
	case *ir.Class:
		made = object(t)
	case *ir.Union:
		var yielded, returned *ir.Class
		for _, member := range ct.Types() {
			shape, ok := l.irType(member, node).(*ir.Class)
			if !ok {
				break
			}
			if member.Symbol().Name == "IteratorReturnResult" {
				returned = shape
			} else {
				yielded = shape
			}
		}
		if yielded == nil || returned == nil {
			break
		}
		then, otherwise := object(returned), object(yielded)
		if then == nil || otherwise == nil {
			return nil
		}
		made = &ir.Conditional{Cond: &ir.LocalRef{Local: finished}, Then: &ir.ToUnion{X: then, To: t}, Else: &ir.ToUnion{X: otherwise, To: t}}
	}
	if made == nil {
		l.notSupportedYet(node, fmt.Sprintf("iterator results of type %s are", t))
		return nil
	}
	return &ir.Bind{Local: finished, Value: done, In: made}
}

// iteratorForOf lowers `for (const x of it)` over an iterator or an Iterable: a loop over
// its iterator (iteratorLoop) whose elements have its yield type.
func (l *lowerer) iteratorForOf(node *ast.Node, label *ir.Label, declaration *ast.Node) []ir.Stmt {
	s := node.AsForInOrOfStatement()
	iterable := l.expression(s.Expression)
	if iterable == nil {
		return nil
	}
	yieldType, _, _ := l.iterationTypes(l.typeAt(s.Expression))
	elemType := l.irType(yieldType, s.Expression)
	if elemType == nil {
		return nil
	}
	return l.iteratorLoop(node, label, declaration, iterable, elemType)
}

// iteratorLoop lowers a `for…of` over iterable as JS's iteration protocol runs it: the
// iterable's iterator (a generator itself, an object's [Symbol.iterator]() result, a
// map's or set's) resumed by next() once per iteration until it is done, and if the loop
// ends early (break, return, a jump out, a throw), closed, as JS's IteratorClose does; an
// array's, typed array's or string's items are read by index instead, which JS's
// iterators over them can't be told apart from (ir.IterationOpen).
func (l *lowerer) iteratorLoop(node *ast.Node, label *ir.Label, declaration *ast.Node, iterable ir.Expr, elemType ir.Type) []ir.Stmt {
	s := node.AsForInOrOfStatement()
	at := l.pos(node)
	source, index, done := l.newLocal("iteration", ir.Value), l.newLocal("index", ir.Number), l.newLocal("done", ir.Bool)
	opened := &ir.CallBuiltin{Builtin: ir.IterationOpen, Args: []ir.Expr{box(iterable), &ir.StringConst{Value: scanner.GetTextOfNode(s.Expression)}}}
	stmts := []ir.Stmt{
		&ir.LocalDecl{At: at, Local: source, Init: opened},
		&ir.LocalDecl{At: at, Local: index, Init: &ir.NumberConst{Value: 0}},
		&ir.LocalDecl{At: at, Local: done, Init: &ir.BoolConst{Value: false}},
	}
	protected := func() *ir.Block {
		element, destructured, ok := l.forOfBinding(declaration, elemType)
		if !ok {
			return nil
		}
		value := l.checkedConversion(&ir.CallBuiltin{Builtin: ir.IterationValue, Args: []ir.Expr{&ir.LocalRef{Local: source}, &ir.LocalRef{Local: index}}}, element.Type, declaration)
		if value == nil {
			return nil
		}
		step := &ir.CallBuiltin{Builtin: ir.IterationNext, Args: []ir.Expr{&ir.LocalRef{Local: source}, &ir.LocalRef{Local: index}}}
		body := l.statementBlock(s.Statement)
		next := []ir.Stmt{
			&ir.ExprStmt{At: at, X: &ir.Assign{Local: done, Value: &ir.BoolConst{Value: true}}}, // if next() throws, the iterator isn't closed
			&ir.ExprStmt{At: at, X: &ir.Assign{Local: done, Value: step}},
			&ir.If{At: at, Cond: &ir.LocalRef{Local: done}, Then: &ir.Block{At: at, Stmts: []ir.Stmt{&ir.Break{At: at, Target: label}}}},
			&ir.LocalDecl{At: l.pos(declaration), Local: element, Init: value},
			&ir.ExprStmt{At: at, X: &ir.Assign{Local: index, Value: &ir.Binary{Op: ir.Add, X: &ir.LocalRef{Local: index}, Y: &ir.NumberConst{Value: 1}}}},
		}
		body.Stmts = append(append(next, destructured...), body.Stmts...)
		return &ir.Block{At: at, Stmts: []ir.Stmt{&ir.Loop{At: at, Label: label, Body: body}}}
	}
	closing := func(f *finallyContext) []ir.Stmt {
		return l.iteratorClosing(at, f, done, func() []ir.Stmt {
			return []ir.Stmt{&ir.ExprStmt{At: at, X: &ir.CallBuiltin{Builtin: ir.IterationClose, Args: []ir.Expr{&ir.LocalRef{Local: source}}}}}
		})
	}
	// The loop's own label is inside the protected code: its break and continue stay there.
	finally := l.withFinally(at, len(l.targets)-1, protected, closing)
	if finally == nil {
		return nil
	}
	return []ir.Stmt{&ir.Block{At: at, Stmts: append(stmts, finally...)}}
}

// isIteratorResultDeclaration reports whether declaration declares one of the ES
// library's iterator result interfaces, whose objects are shapes.
func isIteratorResultDeclaration(declaration *ast.Node) bool {
	if declaration.Kind != ast.KindInterfaceDeclaration {
		return false
	}
	name := declaration.Name().Text()
	return name == "IteratorYieldResult" || name == "IteratorReturnResult"
}
