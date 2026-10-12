package lower

import (
	"fmt"
	"slices"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/pts/ir"
)

// Exceptions (D-068): `throw` throws a dynamic value, `try`/`catch` is an ir.Try, and
// `finally` is lowered to tries and jumps. A try with a finally block becomes
//
//	{ completion = normal
//	  finally: { try { <try and catch> } catch (x) { completion = throw; thrown = x } }
//	  <finally block>
//	  if completion == throw: throw thrown
//	  if completion == return: return result
//	  if completion == k: <the jump the protected code made> … }
//
// where a return, break or continue in the protected code that leaves the statement
// records itself in completion (and the returned value in result) and breaks out of
// `finally:`. The dispatch after the finally block is lowered outside the statement, so a
// jump that leaves an enclosing try with a finally block goes through its finally block
// too. A finally block that itself returns, breaks or throws discards the pending
// completion, as in JS.

// Completions of the protected code of a try with a finally block; a jump has its own,
// from completionJumps on.
const (
	completionNormal = iota
	completionThrow
	completionReturn
	completionJumps
)

// finallyContext is a try statement with a finally block whose protected code is being lowered.
type finallyContext struct {
	label      *ir.Label // `finally:` in the comment above
	targets    int       // how many jump targets were around the statement: the ones a jump leaves it for
	completion *ir.Local
	result     *ir.Local // the value a return in the protected code returns; nil until one does
	returns    bool      // whether a return in the protected code leaves through it
	jumps      []pendingJump
}

// pendingJump is a break or continue that leaves a try statement through its finally
// block: its completion is completionJumps plus its index.
type pendingJump struct {
	at         ir.Pos
	target     *ir.Label
	isContinue bool
}

// throwStatement lowers `throw x`: x of any type, as a dynamic value.
func (l *lowerer) throwStatement(node *ast.Node) []ir.Stmt {
	x := l.valueExpression(node.Expression())
	if x == nil {
		return nil
	}
	return []ir.Stmt{&ir.Throw{At: l.pos(node), Value: box(x)}}
}

// tryStatement lowers try/catch, try/finally and try/catch/finally.
func (l *lowerer) tryStatement(node *ast.Node) []ir.Stmt {
	statement := node.AsTryStatement()
	if statement.FinallyBlock == nil {
		if try := l.tryCatch(node); try != nil {
			return []ir.Stmt{try}
		}
		return nil
	}
	at := l.pos(node)
	protected := func() *ir.Block {
		if statement.CatchClause == nil {
			return l.block(statement.TryBlock, statement.TryBlock.Statements())
		}
		if try := l.tryCatch(node); try != nil {
			return &ir.Block{At: at, Stmts: []ir.Stmt{try}}
		}
		return nil
	}
	finally := func() []ir.Stmt {
		return []ir.Stmt{l.block(statement.FinallyBlock, statement.FinallyBlock.Statements())}
	}
	if stmts := l.withFinally(at, len(l.targets), protected, finally); stmts != nil {
		return []ir.Stmt{&ir.Block{At: at, Stmts: stmts}}
	}
	return nil
}

// withFinally lowers code (protected) that finally follows however it completes, as a
// try statement's finally block does (see above): the jumps out of it to the first
// targets of l.targets and its returns go through finally. It returns nil if protected
// fails, which reported why.
func (l *lowerer) withFinally(at ir.Pos, targets int, protected func() *ir.Block, finally func() []ir.Stmt) []ir.Stmt {
	f := &finallyContext{label: &ir.Label{}, targets: targets, completion: l.newLocal("completion", ir.I32)}
	l.finallies = append(l.finallies, f)
	body := protected()
	l.finallies = l.finallies[:len(l.finallies)-1]
	if body == nil {
		return nil
	}

	thrown, exception := l.newLocal("thrown", ir.Value), l.newLocal("exception", ir.Value)
	catchAll := &ir.Try{At: at, Body: body, Exception: exception, Catch: &ir.Block{At: at, Stmts: []ir.Stmt{
		f.complete(at, completionThrow),
		&ir.ExprStmt{At: at, X: &ir.Assign{Local: thrown, Value: &ir.LocalRef{Local: exception}}},
	}}}
	stmts := []ir.Stmt{
		&ir.LocalDecl{At: at, Local: f.completion, Init: completionConst(completionNormal)},
		&ir.LocalDecl{At: at, Local: thrown, Init: &ir.UndefinedConst{}},
	}
	if f.result != nil {
		stmts = append(stmts, &ir.LocalDecl{At: at, Local: f.result})
	}
	stmts = append(stmts, &ir.LabeledBlock{At: at, Label: f.label, Body: &ir.Block{At: at, Stmts: []ir.Stmt{catchAll}}})
	stmts = append(stmts, finally()...)
	stmts = append(stmts, f.when(at, completionThrow, &ir.Throw{At: at, Value: &ir.LocalRef{Local: thrown}}))
	if f.returns {
		var value ir.Expr
		if f.result != nil {
			value = &ir.LocalRef{Local: f.result}
		}
		stmts = append(stmts, f.when(at, completionReturn, l.leaveFunction(at, value)...))
	}
	for i, jump := range f.jumps {
		target := slices.IndexFunc(l.targets, func(t jumpTarget) bool { return t.label == jump.target })
		stmts = append(stmts, f.when(jump.at, completionJumps+i, l.jumpTo(jump.at, target, jump.isContinue)...))
	}
	return stmts
}

// tryCatch lowers the try block and catch clause of a try statement. The catch clause's
// variable is an `unknown`, which reads as the type tsc narrows it to (narrowedValue).
func (l *lowerer) tryCatch(node *ast.Node) *ir.Try {
	statement := node.AsTryStatement()
	body := l.block(statement.TryBlock, statement.TryBlock.Statements())
	clause := statement.CatchClause.AsCatchClause()
	exception := l.newLocal("exception", ir.Value)
	if declaration := clause.VariableDeclaration; declaration != nil {
		name := declaration.Name()
		if name.Kind != ast.KindIdentifier {
			l.notSupportedYet(name, "destructuring a caught exception is")
			return nil
		}
		symbol := l.checker.GetSymbolAtLocation(name)
		if t := l.typeOfSymbol(symbol); t.Flags()&(checker.TypeFlagsUnknown|checker.TypeFlagsAny) == 0 {
			l.notSupportedYet(name, fmt.Sprintf("a caught exception of type %s is", l.checker.TypeToString(t)))
			return nil
		}
		exception.Name = name.Text()
		l.locals[symbol] = exception
	}
	handler := l.block(clause.Block, clause.Block.Statements())
	return &ir.Try{At: l.pos(node), Body: body, Exception: exception, Catch: handler}
}

// leaveFunction lowers `return value` (value nil for none): a return, or inside the
// protected code of a try with a finally block, a jump to the finally block that records it.
func (l *lowerer) leaveFunction(at ir.Pos, value ir.Expr) []ir.Stmt {
	if len(l.finallies) == 0 {
		return []ir.Stmt{&ir.Return{At: at, Value: value}}
	}
	f := l.finallies[len(l.finallies)-1]
	f.returns = true
	var stmts []ir.Stmt
	if value != nil {
		if f.result == nil {
			f.result = l.newLocal("result", l.function.Result)
		}
		stmts = append(stmts, &ir.ExprStmt{At: at, X: &ir.Assign{Local: f.result, Value: value}})
	}
	return append(stmts, f.complete(at, completionReturn), &ir.Break{At: at, Target: f.label})
}

// jumpTo lowers a break (or continue) to the jump target l.targets[i]: the jump, or if it
// leaves a try statement with a finally block, a jump to the finally block that records it.
func (l *lowerer) jumpTo(at ir.Pos, i int, isContinue bool) []ir.Stmt {
	target := l.targets[i]
	if n := len(l.finallies); n > 0 && l.finallies[n-1].targets > i {
		f := l.finallies[n-1]
		completion := completionJumps + len(f.jumps)
		f.jumps = append(f.jumps, pendingJump{at: at, target: target.label, isContinue: isContinue})
		return []ir.Stmt{f.complete(at, completion), &ir.Break{At: at, Target: f.label}}
	}
	if isContinue {
		return []ir.Stmt{&ir.Continue{At: at, Target: target.label}}
	}
	return []ir.Stmt{&ir.Break{At: at, Target: target.label}}
}

// complete records how the protected code of f completes.
func (f *finallyContext) complete(at ir.Pos, completion int) ir.Stmt {
	return &ir.ExprStmt{At: at, X: &ir.Assign{Local: f.completion, Value: completionConst(completion)}}
}

// when runs stmts if the protected code of f completed as completion.
func (f *finallyContext) when(at ir.Pos, completion int, stmts ...ir.Stmt) ir.Stmt {
	test := &ir.Binary{Op: ir.Eq, X: &ir.LocalRef{Local: f.completion}, Y: completionConst(completion)}
	return &ir.If{At: at, Cond: test, Then: &ir.Block{At: at, Stmts: stmts}}
}

func completionConst(completion int) ir.Expr {
	return &ir.IntConst{Bits: uint64(completion), Ty: ir.I32}
}

// narrowedValue converts x, a dynamic value (an `unknown`), to the type tsc narrows it
// to at node, if that is one a checked conversion reaches: a class (checked as D-067's
// narrowings are), a primitive, or a union of them (D-068). Others stay dynamic.
func (l *lowerer) narrowedValue(node *ast.Node, x ir.Expr) ir.Expr {
	t := l.typeAt(node)
	if t.Flags()&(checker.TypeFlagsUnknown|checker.TypeFlagsAny|checker.TypeFlagsNever) != 0 {
		return x
	}
	if !l.isCheckedValueType(t) {
		if !l.checker.IsArrayType(t) && !l.hasIndexSignatures(t) {
			return x
		}
		if narrow := l.irType(t, node); isDynamicData(narrow) {
			return &ir.FromUnion{X: x, To: narrow} // `Array.isArray(json)`: the array itself, checked (D-089)
		}
		return x
	}
	if narrow := l.irType(t, node); narrow != nil && narrow != ir.Value {
		return &ir.FromUnion{X: x, To: narrow}
	}
	return x
}

// isDynamicData reports whether t is an array or hash-map object of dynamic values, which
// a dynamic value holding one converts to without a copy (D-071).
func isDynamicData(t ir.Type) bool {
	if mt, ok := t.(*ir.MapType); ok {
		return mt.Record && mt.Value == ir.Value
	}
	return t == ir.ValueArray
}

// isCheckedValueType reports whether a dynamic value converts to the IR type of t with a
// check: t is a class, a primitive, or a union of them.
func (l *lowerer) isCheckedValueType(t *checker.Type) bool {
	if t.Flags()&checker.TypeFlagsUnion != 0 {
		return !slices.ContainsFunc(t.Types(), func(member *checker.Type) bool { return !l.isCheckedValueType(member) })
	}
	if t.Flags()&checker.TypeFlagsObject != 0 {
		class, ok := l.classes[t.Symbol()]
		return ok && class != nil
	}
	return t.Flags()&(checker.TypeFlagsStringLike|checker.TypeFlagsNumberLike|checker.TypeFlagsBooleanLike|checker.TypeFlagsNull|checker.TypeFlagsUndefined) != 0
}

// asFromValue lowers `x as T` for x of type any or unknown: a checked conversion to T,
// which copies dynamic data into an object type's representation (docs/SPEC.md §3.5, D-071).
func (l *lowerer) asFromValue(node *ast.Node) ir.Expr {
	x := l.expression(node.Expression())
	target := l.irType(l.typeAt(node), node)
	if numeric := l.numericType(node); numeric != nil {
		target = numeric // `e as u8`
	}
	if x == nil || target == nil {
		return nil
	}
	return l.checkedCast(x, target, node)
}

// stringCall lowers `String(x)`: JS's ToString of x, or "" for `String()`.
func (l *lowerer) stringCall(call *ast.CallExpression) ir.Expr {
	switch len(call.Arguments.Nodes) {
	case 0:
		return &ir.StringConst{}
	case 1:
		x := l.valueExpression(call.Arguments.Nodes[0])
		if x == nil {
			return nil
		}
		return l.toString(x, call.Arguments.Nodes[0])
	}
	l.notSupportedYet(call.AsNode(), "`String` with more than one argument is")
	return nil
}

// isStringFunction reports whether a call calls the global String function.
func (l *lowerer) isStringFunction(call *ast.CallExpression) bool {
	callee := ast.SkipParentheses(call.Expression)
	return callee.Kind == ast.KindIdentifier && callee.Text() == "String" && l.isDeclaredInDefaultLib(l.target(callee))
}

// isObjectToString reports whether a call is `o.toString()` of an object whose class
// declares no toString: Object.prototype.toString, or one a class derived from Error may
// override, which the runtime finds in the object's descriptor (D-068).
func (l *lowerer) isObjectToString(call *ast.CallExpression) bool {
	callee := ast.SkipParentheses(call.Expression)
	if callee.Kind != ast.KindPropertyAccessExpression || len(call.Arguments.Nodes) > 0 {
		return false
	}
	declaration := l.memberDeclaration(callee)
	return declaration != nil && declaration.Name().Text() == "toString" && declaration.Parent.Kind == ast.KindInterfaceDeclaration &&
		declaration.Parent.Name().Text() == "Object" && l.isDeclaredInDefaultLib(l.memberSymbol(callee))
}

// objectToString lowers `o.toString()` (isObjectToString): JS's ToString of the object,
// or for `super.toString()` in a class derived from Error, Error's.
func (l *lowerer) objectToString(call *ast.CallExpression) ir.Expr {
	receiver := ast.SkipParentheses(call.Expression).Expression()
	if receiver.Kind == ast.KindSuperKeyword {
		method := l.currentClass().Parent.Method("toString", ir.Plain)
		if method == nil {
			l.notSupportedYet(call.AsNode(), "`super.toString()` of a class that doesn't extend Error is")
			return nil
		}
		return &ir.Call{Func: method.Func, Args: []ir.Expr{upcast(&ir.LocalRef{Local: l.thisLocal()}, method.Class)}}
	}
	x := l.expression(receiver)
	if x == nil {
		return nil
	}
	if !isObject(x.Type()) || isArray(x.Type()) || isFunction(x.Type()) {
		l.notSupportedYet(call.AsNode(), fmt.Sprintf("toString() of a %s is", x.Type()))
		return nil
	}
	return &ir.ToString{X: box(x)}
}

// isDynamic reports whether values of t are pts_values whose type is known only at run
// time: unions and dynamic values (D-062, D-068).
func isDynamic(t ir.Type) bool {
	return t == ir.Value || isUnion(t)
}
