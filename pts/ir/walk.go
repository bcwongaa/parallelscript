package ir

import "slices"

// Walk calls visitStmt for every statement in stmts and visitExpr for every expression,
// outer ones first. It doesn't enter the bodies of the functions that closures run.
func Walk(stmts []Stmt, visitStmt func(Stmt), visitExpr func(Expr)) {
	for _, stmt := range stmts {
		visitStmt(stmt)
		switch s := stmt.(type) {
		case *Block:
			Walk(s.Stmts, visitStmt, visitExpr)
		case *ExprStmt:
			WalkExpr(s.X, visitExpr)
		case *LocalDecl:
			WalkExpr(s.Init, visitExpr)
		case *Return:
			WalkExpr(s.Value, visitExpr)
		case *If:
			WalkExpr(s.Cond, visitExpr)
			Walk([]Stmt{s.Then}, visitStmt, visitExpr)
			if s.Else != nil {
				Walk([]Stmt{s.Else}, visitStmt, visitExpr)
			}
		case *Loop:
			WalkExpr(s.Cond, visitExpr)
			WalkExpr(s.Update, visitExpr)
			Walk(s.Body.Stmts, visitStmt, visitExpr)
		case *Switch:
			for _, c := range s.Cases {
				WalkExpr(c.Match, visitExpr)
				Walk(c.Body, visitStmt, visitExpr)
			}
		case *LabeledBlock:
			Walk(s.Body.Stmts, visitStmt, visitExpr)
		case *Try:
			Walk(s.Body.Stmts, visitStmt, visitExpr)
			Walk(s.Catch.Stmts, visitStmt, visitExpr)
		case *Throw:
			WalkExpr(s.Value, visitExpr)
		case *Yield:
			WalkExpr(s.Value, visitExpr)
		}
	}
}

// WalkExpr calls visit for x and every expression in it, outer ones first; nil is no expression.
func WalkExpr(x Expr, visit func(Expr)) {
	if x == nil {
		return
	}
	visit(x)
	for _, child := range children(x) {
		WalkExpr(child, visit)
	}
}

// children are the operands of an expression, in the order they are evaluated.
func children(x Expr) []Expr {
	switch x := x.(type) {
	case *RestArgs:
		return []Expr{x.X}
	case *SpreadArg:
		return []Expr{x.X}
	case *Box:
		return []Expr{x.X}
	case *ToUnion:
		return []Expr{x.X}
	case *FromUnion:
		return []Expr{x.X}
	case *IsNullish:
		return []Expr{x.X}
	case *CallBuiltin:
		return x.Args
	case *Call:
		if x.Closure != nil {
			return append([]Expr{x.Closure}, x.Args...)
		}
		return x.Args
	case *CallValue:
		return append([]Expr{x.Callee}, x.Args...)
	case *VirtualCall:
		return x.Args
	case *InterfaceCall:
		return x.Args
	case *ArrayCall:
		return x.Args
	case *MapCall:
		return x.Args
	case *NewMap:
		if x.From != nil {
			return []Expr{x.From}
		}
		return nil
	case *NewArray:
		if x.Length != nil {
			return []Expr{x.Length}
		}
		return x.Elems
	case *ClassCast:
		return []Expr{x.X}
	case *InstanceOf:
		return []Expr{x.X}
	case *Binary:
		return []Expr{x.X, x.Y}
	case *Unary:
		return []Expr{x.X}
	case *Convert:
		return []Expr{x.X}
	case *ToString:
		return []Expr{x.X}
	case *Truthy:
		return []Expr{x.X}
	case *Assign:
		return []Expr{x.Value}
	case *Conditional:
		return []Expr{x.Cond, x.Then, x.Else}
	case *Logical:
		return []Expr{x.X, x.Y}
	case *Sequence:
		return []Expr{x.X, x.Y}
	case *Bind:
		return []Expr{x.Value, x.In}
	case *New:
		return x.Args
	case *NewObject:
		if x.OrderOf != nil {
			return append(slices.Clone(x.Values), x.OrderOf)
		}
		return x.Values
	case *FieldRef:
		return []Expr{x.X}
	case *HasField:
		return []Expr{x.X}
	case *IsAbsent:
		return []Expr{x.X}
	case *HasKey:
		return []Expr{x.X}
	case *SetField:
		return []Expr{x.X, x.Value}
	}
	return nil // constants, LocalRef and MakeClosure
}
