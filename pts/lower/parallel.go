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

// parallelFor lowers `parallel for (const x of items) body` (D-127): the pool runs a leaf
// closure over pieces of the items' indices (pts_parallel_for_each), which runs body for
// each index of its piece with x bound to the item there; `continue` in body goes to the
// next index, the check having reported the other jumps out. The items are read once: an
// array or a typed array as it is, a Range of pts:parallel as its integers, anything else
// as Array.from would copy it.
func (l *lowerer) parallelFor(node *ast.Node, label *ir.Label, declaration *ast.Node) []ir.Stmt {
	s := node.AsForInOrOfStatement()
	source := l.parallelForItems(s.Expression)
	if source.items == nil {
		return nil
	}
	at := l.pos(node)
	items := l.newLocal("items", source.items.Type())
	name := "parallel_for"
	if l.function != l.function.Module.Init {
		name = l.function.Name + "." + name
	}
	fn := &ir.Func{Name: name, Module: l.function.Module, Pos: at, Closure: true, Outer: l.function, Result: ir.Void}
	l.arrows[fn] = true
	ok := true
	l.inFunction(fn, func() {
		from, to := l.newLocal("from", ir.Number), l.newLocal("to", ir.Number)
		fn.Params = []*ir.Local{from, to}
		l.capture(items)
		l.targets = []jumpTarget{{label: label, loop: true, inner: true}}
		index := l.newLocal("index", ir.Number)
		element, destructured, bound := l.forOfBinding(declaration, source.elem)
		if !bound {
			ok = false
			return
		}
		value := l.convert(source.element(&ir.LocalRef{Local: items}, &ir.LocalRef{Local: index}), element.Type, declaration)
		body := l.statementBlock(s.Statement)
		if value == nil {
			ok = false
			return
		}
		body.Stmts = append(append([]ir.Stmt{&ir.LocalDecl{At: l.pos(declaration), Local: element, Init: value}}, destructured...), body.Stmts...)
		i := &ir.LocalRef{Local: index}
		fn.Body = &ir.Block{At: at, Stmts: []ir.Stmt{
			&ir.LocalDecl{At: at, Local: index, Init: &ir.LocalRef{Local: from}},
			&ir.Loop{
				At:     at,
				Label:  label,
				Cond:   &ir.Binary{Op: ir.Lt, X: i, Y: &ir.LocalRef{Local: to}},
				Update: &ir.Assign{Local: index, Value: &ir.Binary{Op: ir.Add, X: i, Y: &ir.NumberConst{Value: 1}}},
				Body:   body,
			},
		}}
	})
	if !ok {
		return nil
	}
	leaf := &ir.MakeClosure{Func: fn, FuncType: l.typeOfFunc(fn)}
	run := l.callExtern("pts_parallel_for_each", ir.Void, leaf, source.length(&ir.LocalRef{Local: items}))
	return []ir.Stmt{&ir.Block{At: at, Stmts: []ir.Stmt{
		&ir.LocalDecl{At: at, Local: items, Init: source.items},
		&ir.ExprStmt{At: at, X: run},
	}}}
}

// parallelItems are the items a `parallel for` runs over: a value read once, its count of
// items, and the item at an index, of type elem.
type parallelItems struct {
	items   ir.Expr
	elem    ir.Type
	length  func(items ir.Expr) ir.Expr
	element func(items, index ir.Expr) ir.Expr
}

// parallelForItems lowers the items of a `parallel for`; items is nil if that failed (reported).
func (l *lowerer) parallelForItems(node *ast.Node) parallelItems {
	if l.typedArrayClass(node) != nil {
		return parallelItems{
			items:   l.expression(node),
			elem:    ir.Number,
			length:  func(items ir.Expr) ir.Expr { return l.callExtern("pts_typed_array_length", ir.Number, items) },
			element: l.typedArrayRead,
		}
	}
	if class := l.parallelRangeClass(node); class != nil {
		start, end := class.Field("start"), class.Field("end")
		return parallelItems{
			items: l.expression(node),
			elem:  ir.Number,
			length: func(items ir.Expr) ir.Expr {
				return &ir.CallBuiltin{Builtin: ir.MathMax, Args: []ir.Expr{&ir.NumberConst{Value: 0}, &ir.Binary{Op: ir.Sub, X: &ir.FieldRef{X: items, Field: end}, Y: &ir.FieldRef{X: items, Field: start}}}}
			},
			element: func(items, index ir.Expr) ir.Expr {
				return &ir.Binary{Op: ir.Add, X: &ir.FieldRef{X: items, Field: start}, Y: index}
			},
		}
	}
	items := l.iterableItems(node)
	if items == nil {
		return parallelItems{}
	}
	return parallelItems{
		items:  items,
		elem:   items.Type().(*ir.Array).Elem,
		length: func(items ir.Expr) ir.Expr { return &ir.ArrayCall{Op: ir.ArrayLength, Args: []ir.Expr{items}} },
		element: func(items, index ir.Expr) ir.Expr {
			return &ir.ArrayCall{Op: ir.ArrayElementChecked, Args: []ir.Expr{items, index}}
		},
	}
}

// parallelRangeClass is the class of pts:parallel's Range if tsc's type of node is it, else nil.
func (l *lowerer) parallelRangeClass(node *ast.Node) *ir.Class {
	symbol := l.typeAt(node).Symbol()
	if symbol == nil || symbol.Name != "Range" || len(symbol.Declarations) == 0 || ast.GetSourceFileOfNode(symbol.Declarations[0]).FileName() != lib.ModulePath(lib.ParallelModule) {
		return nil
	}
	class, _ := l.irType(l.typeAt(node), node).(*ir.Class)
	return class
}
