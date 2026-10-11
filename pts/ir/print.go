package ir

import (
	"fmt"
	"strconv"
	"strings"
)

// String prints the program in a compact text form, for tests and debugging.
func (p *Program) String() string {
	pr := &printer{labels: map[*Label]string{}}
	for _, c := range p.Classes {
		fields := make([]string, len(c.Fields))
		for i, field := range c.Fields {
			fields[i] = field.Name + ": " + field.Type.String()
		}
		fmt.Fprintf(&pr.out, "class %s { %s }\n", c.Name, strings.Join(fields, "; "))
	}
	for _, f := range p.Funcs {
		params := make([]string, len(f.Params))
		for i, param := range f.Params {
			params[i] = param.Name + ": " + param.Type.String()
		}
		fmt.Fprintf(&pr.out, "func %s(%s) %s {\n", f.QualifiedName(), strings.Join(params, ", "), f.Result)
		pr.stmts(f.Body.Stmts, 1)
		pr.out.WriteString("}\n")
	}
	return pr.out.String()
}

type printer struct {
	out    strings.Builder
	labels map[*Label]string // unlabelled targets are #1, #2, … in order of appearance
}

func (pr *printer) label(label *Label) string {
	if name, ok := pr.labels[label]; ok {
		return name
	}
	name := label.Name
	if name == "" {
		name = "#" + strconv.Itoa(len(pr.labels)+1)
	}
	pr.labels[label] = name
	return name
}

func (pr *printer) stmts(stmts []Stmt, depth int) {
	for _, stmt := range stmts {
		pr.stmt(stmt, depth)
	}
}

func (pr *printer) stmt(stmt Stmt, depth int) {
	indent := strings.Repeat("  ", depth)
	fmt.Fprintf(&pr.out, "%s%d: ", indent, stmt.Pos().Line)
	switch s := stmt.(type) {
	case *Block:
		pr.out.WriteString("{\n")
		pr.stmts(s.Stmts, depth+1)
		pr.out.WriteString(indent + "}\n")
	case *ExprStmt:
		pr.out.WriteString(FormatExpr(s.X) + "\n")
	case *LocalDecl:
		keyword := "let"
		if s.Local.Global {
			keyword = "global"
		}
		fmt.Fprintf(&pr.out, "%s %s: %s", keyword, s.Local.Name, s.Local.Type)
		if s.Init != nil {
			pr.out.WriteString(" = " + FormatExpr(s.Init))
		}
		pr.out.WriteString("\n")
	case *If:
		pr.out.WriteString("if " + FormatExpr(s.Cond) + " {\n")
		pr.stmts(s.Then.Stmts, depth+1)
		if s.Else != nil {
			pr.out.WriteString(indent + "} else {\n")
			if block, ok := s.Else.(*Block); ok {
				pr.stmts(block.Stmts, depth+1)
			} else {
				pr.stmt(s.Else, depth+1)
			}
		}
		pr.out.WriteString(indent + "}\n")
	case *Loop:
		pr.out.WriteString(pr.label(s.Label) + ": " + loopHeader(s) + " {\n")
		pr.stmts(s.Body.Stmts, depth+1)
		pr.out.WriteString(indent + "}\n")
	case *Switch:
		pr.out.WriteString(pr.label(s.Label) + ": switch {\n")
		for _, c := range s.Cases {
			if c.Match == nil {
				fmt.Fprintf(&pr.out, "%s  %d: default:\n", indent, c.At.Line)
			} else {
				fmt.Fprintf(&pr.out, "%s  %d: case %s:\n", indent, c.At.Line, FormatExpr(c.Match))
			}
			pr.stmts(c.Body, depth+2)
		}
		pr.out.WriteString(indent + "}\n")
	case *LabeledBlock:
		pr.out.WriteString(pr.label(s.Label) + ": {\n")
		pr.stmts(s.Body.Stmts, depth+1)
		pr.out.WriteString(indent + "}\n")
	case *Break:
		pr.out.WriteString("break " + pr.label(s.Target) + "\n")
	case *Continue:
		pr.out.WriteString("continue " + pr.label(s.Target) + "\n")
	case *Return:
		if s.Value == nil {
			pr.out.WriteString("return\n")
		} else {
			pr.out.WriteString("return " + FormatExpr(s.Value) + "\n")
		}
	default:
		panic(fmt.Sprintf("ir: cannot print %T", stmt))
	}
}

func loopHeader(loop *Loop) string {
	cond := "true"
	if loop.Cond != nil {
		cond = FormatExpr(loop.Cond)
	}
	switch {
	case loop.PostTest:
		return "do-while " + cond
	case loop.Update != nil:
		return "for " + cond + "; " + FormatExpr(loop.Update)
	}
	return "while " + cond
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
	case *Truthy:
		return "truthy(" + FormatExpr(e.X) + ")"
	case *Binary:
		return "(" + FormatExpr(e.X) + " " + e.Op.String() + " " + FormatExpr(e.Y) + ")"
	case *Unary:
		return "(" + e.Op.String() + FormatExpr(e.X) + ")"
	case *Assign:
		assign := "(" + e.Local.Name + " = " + FormatExpr(e.Value) + ")"
		if e.Postfix {
			return "old" + assign
		}
		return assign
	case *Conditional:
		return "(" + FormatExpr(e.Cond) + " ? " + FormatExpr(e.Then) + " : " + FormatExpr(e.Else) + ")"
	case *Logical:
		return "(" + FormatExpr(e.X) + " " + e.Op.String() + " " + FormatExpr(e.Y) + ")"
	case *Sequence:
		return "(" + FormatExpr(e.X) + ", " + FormatExpr(e.Y) + ")"
	case *Bind:
		return "(let " + e.Local.Name + " = " + FormatExpr(e.Value) + " in " + FormatExpr(e.In) + ")"
	case *New:
		return "new " + e.Class.Name + formatArgs(e.Args)
	case *FieldRef:
		return FormatExpr(e.X) + "." + e.Field.Name
	case *SetField:
		set := "(" + FormatExpr(e.X) + "." + e.Field.Name + " = " + FormatExpr(e.Value) + ")"
		if e.Postfix {
			return "old" + set
		}
		return set
	case *CallBuiltin:
		return e.Builtin.Name + formatArgs(e.Args)
	case *Call:
		return e.Func.QualifiedName() + formatArgs(e.Args)
	default:
		panic(fmt.Sprintf("ir: cannot print %T", expr))
	}
}

func formatArgs(args []Expr) string {
	formatted := make([]string, len(args))
	for i, arg := range args {
		formatted[i] = FormatExpr(arg)
	}
	return "(" + strings.Join(formatted, ", ") + ")"
}
