package lower

import (
	"fmt"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/internal/scanner"
	"github.com/microsoft/typescript-go/pts/ir"
)

// Promises and async functions (D-112). Promise<T> and PromiseLike<T> are dynamic values
// (Value): the runtime's promises, which it settles and reacts to from its microtask
// queue. The handlers a program passes to then(), catch() and finally(), and the executor
// of `new Promise`, are boxed closures that the runtime calls through their dynamic
// entries. An async function is a generator function (D-109) marked Async, whose yields
// are its awaits: the runtime resumes it with the awaited promise's value, or by throw
// with its reason. An await can be where a yield can (D-109, D-111).

func isAsync(flags ast.FunctionFlags) bool { return flags&ast.FunctionFlagsAsync != 0 }

func isAsyncGenerator(flags ast.FunctionFlags) bool {
	return isAsync(flags) && flags&ast.FunctionFlagsGenerator != 0
}

// isStateMachine reports whether a function with these flags compiles to a state machine
// over a heap frame: a generator or an async function.
func isStateMachine(flags ast.FunctionFlags) bool {
	return flags&(ast.FunctionFlagsGenerator|ast.FunctionFlagsAsync) != 0
}

// isPromiseType reports whether t is the ES library's Promise<T> or PromiseLike<T>.
func (l *lowerer) isPromiseType(t *checker.Type) bool {
	symbol := t.Symbol()
	return symbol != nil && t.Flags()&checker.TypeFlagsObject != 0 && (symbol.Name == "Promise" || symbol.Name == "PromiseLike") && l.isLibInterface(symbol)
}

// isPromiseConstructor reports whether a callee is the global Promise.
func (l *lowerer) isPromiseConstructor(callee *ast.Node) bool {
	return callee.Kind == ast.KindIdentifier && callee.Text() == "Promise" && l.isDeclaredInDefaultLib(l.target(callee))
}

// isSuspension reports whether node is a yield or an await: where a state machine suspends.
func isSuspension(node *ast.Node) bool {
	return node.Kind == ast.KindYieldExpression || node.Kind == ast.KindAwaitExpression
}

// suspensionPoint lowers a yield or an await: the statements that suspend the function,
// and the expression for what it evaluates to once the function resumes.
func (l *lowerer) suspensionPoint(node *ast.Node) ([]ir.Stmt, ir.Expr) {
	if node.Kind == ast.KindAwaitExpression {
		return l.awaitPoint(node)
	}
	return l.yieldPoint(node)
}

// awaitPoint lowers `await x`: the function suspends with x, which the runtime turns into
// a promise and waits for; it resumes with its value, converted to the await's type with
// a check, or by throw with its reason, which is thrown where the await is.
func (l *lowerer) awaitPoint(node *ast.Node) ([]ir.Stmt, ir.Expr) {
	at := l.pos(node)
	x := l.valueExpression(node.Expression())
	t := l.irType(l.typeAt(node), node)
	if x == nil || t == nil {
		return nil, nil
	}
	var value ir.Expr = &ir.Resumed{}
	if t != ir.Void && t != ir.Undefined && t != ir.Value {
		if value = l.checkedConversion(value, t, node); value == nil {
			return nil, nil
		}
	}
	rethrow := &ir.Block{At: at, Stmts: []ir.Stmt{&ir.Throw{At: at, Value: &ir.Resumed{}}}}
	return []ir.Stmt{
		&ir.Yield{At: at, Value: box(x)},
		&ir.If{At: at, Cond: &ir.ResumedBy{Mode: ir.ResumeThrow}, Then: rethrow},
	}, value
}

// newPromise lowers `new Promise(executor)`.
func (l *lowerer) newPromise(node *ast.Node) ir.Expr {
	args := node.Arguments()
	if len(args) != 1 || l.hasSpread(args) {
		l.notSupportedYet(node, "`new Promise` without exactly one executor argument is")
		return nil
	}
	executor := l.expression(args[0])
	if executor == nil {
		return nil
	}
	return &ir.CallBuiltin{Builtin: ir.PromiseNew, Args: []ir.Expr{box(executor), &ir.StringConst{Value: scanner.GetTextOfNode(args[0])}}}
}

// promiseMethods are the builtins of the promise methods and statics this milestone has,
// with how many arguments each takes.
var promiseMethods = map[string]struct {
	builtin *ir.Builtin
	params  int
}{
	"Promise.then":               {ir.PromiseThen, 2},
	"PromiseLike.then":           {ir.PromiseThen, 2},
	"Promise.catch":              {ir.PromiseThen, 2}, // then(undefined, onRejected)
	"Promise.finally":            {ir.PromiseFinally, 1},
	"PromiseConstructor.resolve": {ir.PromiseResolve, 1},
	"PromiseConstructor.reject":  {ir.PromiseReject, 1},
}

// promiseCall lowers p.then(…), p.catch(…), p.finally(…), Promise.resolve(…) and
// Promise.reject(…); handled is false for other calls.
func (l *lowerer) promiseCall(call *ast.CallExpression) (x ir.Expr, handled bool) {
	callee := ast.SkipParentheses(call.Expression)
	if callee.Kind != ast.KindPropertyAccessExpression {
		return nil, false
	}
	name := l.libMemberName(callee)
	method, ok := promiseMethods[name]
	if !ok {
		if l.isPromiseStatic(name) {
			l.notSupportedYet(callee, fmt.Sprintf("`%s` is", name[len("PromiseConstructor."):]))
			return nil, true
		}
		return nil, false
	}
	args := call.Arguments.Nodes
	if l.hasSpreadArgument(call) || len(args) > method.params {
		l.notSupportedYet(call.AsNode(), fmt.Sprintf("`%s` with these arguments is", name))
		return nil, true
	}
	var values []ir.Expr
	if method.builtin == ir.PromiseThen || method.builtin == ir.PromiseFinally {
		receiver := l.expression(callee.Expression())
		if receiver == nil {
			return nil, true
		}
		values = append(values, box(receiver))
	}
	if name == "Promise.catch" {
		values = append(values, box(&ir.UndefinedConst{}))
	}
	for _, arg := range args {
		value := l.valueExpression(arg)
		if value == nil {
			return nil, true
		}
		values = append(values, box(value))
	}
	for len(values) < len(method.builtin.Params) {
		values = append(values, box(&ir.UndefinedConst{}))
	}
	return &ir.CallBuiltin{Builtin: method.builtin, Args: values}, true
}

// isPromiseStatic reports whether name ("PromiseConstructor.all") is a static method of Promise.
func (l *lowerer) isPromiseStatic(name string) bool {
	return len(name) > len("PromiseConstructor.") && name[:len("PromiseConstructor.")] == "PromiseConstructor."
}
