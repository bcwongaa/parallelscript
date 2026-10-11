package ir

import (
	"fmt"
	"strconv"
	"strings"
)

// String prints the program in a compact text form, for tests and debugging.
func (p *Program) String() string {
	var out strings.Builder
	for _, f := range p.Funcs {
		fmt.Fprintf(&out, "func %s() %s {\n", f.Name, f.Result)
		printStmts(&out, f.Body.Stmts, 1)
		out.WriteString("}\n")
	}
	return out.String()
}

func printStmts(out *strings.Builder, stmts []Stmt, depth int) {
	for _, stmt := range stmts {
		fmt.Fprintf(out, "%s%d: ", strings.Repeat("  ", depth), stmt.Pos().Line)
		switch s := stmt.(type) {
		case *Block:
			out.WriteString("{\n")
			printStmts(out, s.Stmts, depth+1)
			out.WriteString(strings.Repeat("  ", depth) + "}\n")
		case *ExprStmt:
			out.WriteString(FormatExpr(s.X) + "\n")
		case *LocalDecl:
			fmt.Fprintf(out, "let %s: %s = %s\n", s.Local.Name, s.Local.Type, FormatExpr(s.Init))
		default:
			panic(fmt.Sprintf("ir: cannot print %T", stmt))
		}
	}
}

// FormatExpr prints one expression.
func FormatExpr(expr Expr) string {
	switch e := expr.(type) {
	case *NumberConst:
		return strconv.FormatFloat(e.Value, 'g', -1, 64)
	case *BoolConst:
		return strconv.FormatBool(e.Value)
	case *StringConst:
		return strconv.Quote(e.Value)
	case *UndefinedConst:
		return "undefined"
	case *NullConst:
		return "null"
	case *LocalRef:
		return e.Local.Name
	case *IntConst:
		if e.Ty == I8 || e.Ty == I16 || e.Ty == I32 || e.Ty == I64 {
			return strconv.FormatInt(int64(e.Bits), 10) + ":" + e.Ty.String()
		}
		return strconv.FormatUint(e.Bits, 10) + ":" + e.Ty.String()
	case *Convert:
		return e.To.String() + "(" + FormatExpr(e.X) + ")"
	case *Box:
		return "box(" + FormatExpr(e.X) + ")"
	case *ToString:
		return "str(" + FormatExpr(e.X) + ")"
	case *Binary:
		return "(" + FormatExpr(e.X) + " " + e.Op.String() + " " + FormatExpr(e.Y) + ")"
	case *Unary:
		return "(" + e.Op.String() + FormatExpr(e.X) + ")"
	case *CallBuiltin:
		args := make([]string, len(e.Args))
		for i, arg := range e.Args {
			args[i] = FormatExpr(arg)
		}
		return e.Builtin.Name + "(" + strings.Join(args, ", ") + ")"
	default:
		panic(fmt.Sprintf("ir: cannot print %T", expr))
	}
}
