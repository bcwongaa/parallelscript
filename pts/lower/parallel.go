package lower

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/pts/ir"
)

// parallelCall lowers `parallel e` (D-120, D-121): e becomes the body of a task, a
// closure of no parameters that returns e's value boxed, which the runtime runs on
// another loop; the call is the promise of the task's value. The closure captures what
// e uses by reference, as an arrow function would, `this` included.
func (l *lowerer) parallelCall(call *ast.CallExpression) ir.Expr {
	operand := call.Arguments.Nodes[0]
	if suspension := suspensionIn(operand); suspension != nil {
		l.notSupportedYet(suspension, "`await` and `yield` in the operand of `parallel` are")
		return nil
	}
	name := "parallel"
	if l.function != l.function.Module.Init {
		name = l.function.Name + "." + name
	}
	at := l.pos(operand)
	fn := &ir.Func{Name: name, Module: l.function.Module, Pos: at, Closure: true, Outer: l.function, Result: ir.Value}
	l.arrows[fn] = true
	l.inFunction(fn, func() {
		fn.Body = &ir.Block{At: at}
		if value := l.valueExpression(operand); value != nil {
			fn.Body.Stmts = []ir.Stmt{&ir.Return{At: at, Value: box(value)}}
		}
	})
	task := &ir.MakeClosure{Func: fn, FuncType: l.typeOfFunc(fn)}
	return &ir.CallBuiltin{Builtin: ir.Parallel, Args: []ir.Expr{box(task)}}
}

// suspensionIn is the first await or yield in node outside the functions in it, or nil.
func suspensionIn(node *ast.Node) *ast.Node {
	var found *ast.Node
	var visit func(*ast.Node) bool
	visit = func(n *ast.Node) bool {
		switch {
		case ast.IsFunctionLike(n) || ast.IsClassLike(n):
			return false
		case isSuspension(n):
			found = n
			return true
		}
		return n.ForEachChild(visit)
	}
	visit(node)
	return found
}
