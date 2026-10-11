package cgen

import "github.com/microsoft/typescript-go/pts/ir"

// effects says how evaluating an expression can interact with evaluating others, which
// is what decides whether C's unspecified evaluation order could differ from JS's.
type effects uint8

const (
	readsLocals effects = 1 << iota
	writes              // assigns a local or does output
	mayThrow            // may stop with an error, whose order among errors and effects is observable
)

// throwingBuiltins are the builtins that can throw (beyond running out of memory).
var throwingBuiltins = map[*ir.Builtin]bool{
	ir.StringElementChecked: true, ir.StringRepeat: true, ir.StringPadStart: true, ir.StringPadEnd: true,
}

func effectsOf(expr ir.Expr) effects {
	switch x := expr.(type) {
	case *ir.LocalRef:
		return readsLocals
	case *ir.Assign:
		return readsLocals | writes | effectsOf(x.Value)
	case *ir.CallBuiltin:
		var f effects
		for _, arg := range x.Args {
			f |= effectsOf(arg)
		}
		if x.Builtin == ir.ConsoleLog {
			f |= writes
		}
		if throwingBuiltins[x.Builtin] {
			f |= mayThrow
		}
		return f
	case *ir.Binary:
		f := effectsOf(x.X) | effectsOf(x.Y)
		if ir.IsInteger(x.X.Type()) && (x.Op == ir.Div || x.Op == ir.Rem || x.Op == ir.Pow) {
			f |= mayThrow
		}
		return f
	case *ir.Unary:
		return effectsOf(x.X)
	case *ir.Convert:
		return effectsOf(x.X)
	case *ir.Box:
		return effectsOf(x.X)
	case *ir.ToString:
		return effectsOf(x.X)
	case *ir.Truthy:
		return effectsOf(x.X)
	case *ir.Conditional:
		return effectsOf(x.Cond) | effectsOf(x.Then) | effectsOf(x.Else)
	case *ir.Logical:
		return effectsOf(x.X) | effectsOf(x.Y)
	case *ir.Sequence:
		return effectsOf(x.X) | effectsOf(x.Y)
	}
	return 0 // constants
}

// orderMatters reports whether evaluating operands in some order other than left to
// right could be observed: an operand that writes, next to another that isn't a
// constant, or two operands that may throw.
func orderMatters(operands []ir.Expr) bool {
	nonConstant, writers, throwers := 0, 0, 0
	for _, operand := range operands {
		f := effectsOf(operand)
		if f != 0 {
			nonConstant++
		}
		if f&writes != 0 {
			writers++
		}
		if f&mayThrow != 0 {
			throwers++
		}
	}
	return (writers > 0 && nonConstant > 1) || throwers > 1
}
