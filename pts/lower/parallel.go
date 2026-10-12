package lower

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/pts/ir"
	"github.com/microsoft/typescript-go/pts/lib"
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

// parallelView lowers `xs.par` (D-123): a new object of pts:parallel's ParallelArray, the
// class instance that tsc's type of the expression names, viewing xs.
func (l *lowerer) parallelView(node *ast.Node) ir.Expr {
	t := l.irType(l.typeAt(node), node)
	if t == nil {
		return nil
	}
	class, ok := t.(*ir.Class)
	if !ok || class.Constructor == nil {
		l.notSupportedYet(node, "`.par` of this array is")
		return nil
	}
	items := l.expression(node.Expression())
	if items == nil {
		return nil
	}
	if items = l.convert(items, class.Constructor.Params[1].Type, node.Expression()); items == nil {
		return nil
	}
	return &ir.New{Class: class, Args: []ir.Expr{items}}
}

// withoutParallelBrand is t without pts:parallel's ParallelBrand if t is a callback type
// that the brand marks (`F & ParallelBrand`, D-L17): the brand only tells the check that
// the callback runs in parallel, so the function type is F's.
func (l *lowerer) withoutParallelBrand(t *checker.Type) *checker.Type {
	if t.Flags()&checker.TypeFlagsIntersection == 0 || len(t.Types()) != 2 {
		return t
	}
	for i, member := range t.Types() {
		if isParallelBrand(member) {
			return t.Types()[1-i]
		}
	}
	return t
}

// isParallelBrand reports whether t is pts:parallel's ParallelBrand.
func isParallelBrand(t *checker.Type) bool {
	symbol := t.Symbol()
	return symbol != nil && symbol.Name == "ParallelBrand" && len(symbol.Declarations) > 0 &&
		ast.GetSourceFileOfNode(symbol.Declarations[0]).FileName() == lib.ModulePath(lib.ParallelModule)
}
