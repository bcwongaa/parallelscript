package lower

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/internal/scanner"
	"github.com/microsoft/typescript-go/pts/ir"
)

// Async iteration (D-115). An async generator is a generator function marked
// AsyncGenerator: its awaits and its yields are both ir.Yields, an await marked Await, and
// `yield v` awaits v before it yields it, as the spec's AsyncGeneratorYield does. Its
// objects, and every async iterator, are dynamic values whose next(), return() and
// throw() give promises. `for await` opens an async iterator over its value (an async
// generator, a [Symbol.asyncIterator] method's result, or a sync iterable's items as
// promises) and awaits each next().

// asyncIteratorTypes are the ES library's types of async iterators and iterables.
var asyncIteratorTypes = map[string]bool{"AsyncGenerator": true, "AsyncIterator": true, "AsyncIterable": true,
	"AsyncIterableIterator": true, "AsyncIteratorObject": true}

// isAsyncIteratorType reports whether t is one of asyncIteratorTypes.
func (l *lowerer) isAsyncIteratorType(t *checker.Type) bool {
	symbol := t.Symbol()
	return symbol != nil && t.Flags()&checker.TypeFlagsObject != 0 && asyncIteratorTypes[symbol.Name] && l.isLibInterface(symbol)
}

// stateMachine marks fn, a function with these flags, as the state machine it compiles to.
func stateMachine(fn *ir.Func, flags ast.FunctionFlags) {
	fn.Generator = isStateMachine(flags)
	fn.AsyncGenerator = isAsyncGenerator(flags)
	fn.Async = isAsync(flags) && !fn.AsyncGenerator
}

// asyncYieldPoint lowers `yield v` in an async generator: v awaited, then the awaited
// value yielded, then what the generator does when it resumes.
func (l *lowerer) asyncYieldPoint(node *ast.Node) ([]ir.Stmt, ir.Expr) {
	y := node.AsYieldExpression()
	if y.AsteriskToken != nil {
		l.notSupportedYet(node, "`yield*` in async generators is")
		return nil, nil
	}
	at := l.pos(node)
	var value ir.Expr = &ir.UndefinedConst{}
	if y.Expression != nil {
		if value = l.valueExpression(y.Expression); value == nil {
			return nil, nil
		}
	}
	rethrow := &ir.Block{At: at, Stmts: []ir.Stmt{&ir.Throw{At: at, Value: &ir.Resumed{}}}}
	stmts := []ir.Stmt{
		&ir.Yield{At: at, Value: box(value), Await: true},
		&ir.If{At: at, Cond: &ir.ResumedBy{Mode: ir.ResumeThrow}, Then: rethrow},
		&ir.Yield{At: at, Value: &ir.Resumed{}},
	}
	return append(stmts, l.resumeChecks(at)...), &ir.Resumed{}
}

// asyncIteratorMethodCall lowers next(v), return(v) and throw(e) on an async iterator: a
// promise of its result. handled is false for other calls.
func (l *lowerer) asyncIteratorMethodCall(call *ast.CallExpression) (x ir.Expr, handled bool) {
	callee := ast.SkipParentheses(call.Expression)
	if callee.Kind != ast.KindPropertyAccessExpression || !l.isAsyncIteratorType(l.checker.GetNonNullableType(l.typeAt(callee.Expression()))) {
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
	if iterator == nil {
		return nil, true
	}
	var sent ir.Expr = &ir.UndefinedConst{}
	if args := call.Arguments.Nodes; len(args) > 0 {
		if sent = l.valueExpression(args[0]); sent == nil {
			return nil, true
		}
	}
	return &ir.CallBuiltin{Builtin: ir.AsyncIteratorResume, Args: []ir.Expr{box(iterator), modeConst(mode), box(sent)}}, true
}

// awaitStatements are the statements that await x and leave what it gives in a new local.
func (l *lowerer) awaitStatements(at ir.Pos, x ir.Expr) ([]ir.Stmt, *ir.Local) {
	settled := l.newLocal("settled", ir.Value)
	rethrow := &ir.Block{At: at, Stmts: []ir.Stmt{&ir.Throw{At: at, Value: &ir.Resumed{}}}}
	return []ir.Stmt{
		&ir.Yield{At: at, Value: box(x), Await: true},
		&ir.If{At: at, Cond: &ir.ResumedBy{Mode: ir.ResumeThrow}, Then: rethrow},
		&ir.LocalDecl{At: at, Local: settled, Init: &ir.Resumed{}},
	}, settled
}

// asyncForOf lowers `for await (const x of it)`: an async iterator over it, each next()
// awaited until a result is done, and if the loop ends early, its return() awaited.
func (l *lowerer) asyncForOf(node *ast.Node, label *ir.Label, declaration *ast.Node) []ir.Stmt {
	s := node.AsForInOrOfStatement()
	at := l.pos(node)
	iterable := l.expression(s.Expression)
	elemType := l.irType(l.typeAt(declaration.Name()), declaration)
	if iterable == nil || elemType == nil {
		return nil
	}
	it, done := l.newLocal("iterator", ir.Value), l.newLocal("done", ir.Bool)
	opened := &ir.CallBuiltin{Builtin: ir.AsyncIteratorOpen, Args: []ir.Expr{box(iterable), &ir.StringConst{Value: scanner.GetTextOfNode(s.Expression)}}}
	stmts := []ir.Stmt{
		&ir.LocalDecl{At: at, Local: it, Init: opened},
		&ir.LocalDecl{At: at, Local: done, Init: &ir.BoolConst{Value: false}},
	}
	ref := func(local *ir.Local) ir.Expr { return &ir.LocalRef{Local: local} }
	protected := func() *ir.Block {
		element, destructured, ok := l.forOfBinding(declaration, elemType)
		if !ok {
			return nil
		}
		awaited, result := l.awaitStatements(at, &ir.CallBuiltin{Builtin: ir.AsyncIteratorResume, Args: []ir.Expr{ref(it), modeConst(ir.ResumeNext), box(&ir.UndefinedConst{})}})
		value := l.checkedConversion(&ir.CallBuiltin{Builtin: ir.IteratorResultValue, Args: []ir.Expr{ref(result)}}, element.Type, declaration)
		if value == nil {
			return nil
		}
		body := l.statementBlock(s.Statement)
		next := []ir.Stmt{&ir.ExprStmt{At: at, X: &ir.Assign{Local: done, Value: &ir.BoolConst{Value: true}}}} // if next() throws, the iterator isn't closed
		next = append(next, awaited...)
		next = append(next,
			&ir.ExprStmt{At: at, X: &ir.Assign{Local: done, Value: &ir.CallBuiltin{Builtin: ir.IteratorResultDone, Args: []ir.Expr{ref(result)}}}},
			&ir.If{At: at, Cond: ref(done), Then: &ir.Block{At: at, Stmts: []ir.Stmt{&ir.Break{At: at, Target: label}}}},
			&ir.LocalDecl{At: l.pos(declaration), Local: element, Init: value},
		)
		body.Stmts = append(append(next, destructured...), body.Stmts...)
		return &ir.Block{At: at, Stmts: []ir.Stmt{&ir.Loop{At: at, Label: label, Body: body}}}
	}
	closing := func(f *finallyContext) []ir.Stmt {
		return l.iteratorClosing(at, f, done, func() []ir.Stmt {
			awaited, _ := l.awaitStatements(at, &ir.CallBuiltin{Builtin: ir.AsyncIteratorClose, Args: []ir.Expr{ref(it)}})
			return awaited
		})
	}
	finally := l.withFinally(at, len(l.targets)-1, protected, closing)
	if finally == nil {
		return nil
	}
	return []ir.Stmt{&ir.Block{At: at, Stmts: append(stmts, finally...)}}
}
