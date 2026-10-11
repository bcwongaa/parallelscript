package cgen

import "github.com/microsoft/typescript-go/pts/ir"

// effects says how evaluating an expression can interact with evaluating others, which
// is what decides whether C's unspecified evaluation order could differ from JS's.
type effects uint8

const (
	readsLocals effects = 1 << iota // reads locals or fields
	writes                          // assigns a local or a field, or does output
	mayThrow                        // may stop with an error, whose order among errors and effects is observable
)

// throwingBuiltins are the builtins that can throw (beyond running out of memory).
var throwingBuiltins = map[*ir.Builtin]bool{
	ir.StringElementChecked: true, ir.StringRepeat: true, ir.StringPadStart: true, ir.StringPadEnd: true, ir.ThrowError: true,
	ir.StringFromCodePoint: true, ir.NumberToFixed: true, ir.NumberToExponential: true, ir.NumberToPrecision: true,
	ir.NumberToStringRadix: true, ir.I64ToStringRadix: true, ir.U64ToStringRadix: true, ir.ObjectAssign: true, ir.ValueKeyArray: true,
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
		if ir.IsConsole(x.Builtin) || x.Builtin == ir.ObjectAssign {
			f |= writes
		}
		if throwingBuiltins[x.Builtin] {
			f |= mayThrow
		}
		return f
	case *ir.Call:
		if x.Closure != nil {
			return callEffects(append([]ir.Expr{x.Closure}, x.Args...))
		}
		return callEffects(x.Args)
	case *ir.CallValue:
		return callEffects(append([]ir.Expr{x.Callee}, x.Args...))
	case *ir.MapCall:
		f := mapEffects(x.Op)
		for _, arg := range x.Args {
			f |= effectsOf(arg)
		}
		return f
	case *ir.NewMap:
		if x.From != nil {
			return readsLocals | effectsOf(x.From)
		}
		return 0
	case *ir.MakeClosure:
		if len(x.Func.Captures) > 0 {
			return readsLocals // the captured values
		}
		return 0
	case *ir.VirtualCall:
		return callEffects(x.Args)
	case *ir.InterfaceCall:
		return callEffects(x.Args)
	case *ir.ArrayCall:
		f := arrayEffects(x.Op)
		for _, arg := range x.Args {
			f |= effectsOf(arg)
		}
		return f
	case *ir.NewArray:
		var f effects
		for _, elem := range x.Elems {
			f |= effectsOf(elem)
		}
		if x.Length != nil {
			f |= effectsOf(x.Length) | mayThrow
		}
		return f
	case *ir.ClassCast:
		return effectsOf(x.X)
	case *ir.InstanceOf:
		return effectsOf(x.X) // an instance's class never changes
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
	case *ir.ToUnion:
		return effectsOf(x.X)
	case *ir.FromUnion:
		if x.Checked {
			return mayThrow | effectsOf(x.X)
		}
		return effectsOf(x.X)
	case *ir.IsNullish:
		return effectsOf(x.X)
	case *ir.RestArgs:
		return effectsOf(x.X)
	case *ir.SpreadArg:
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
	case *ir.Bind:
		return writes | effectsOf(x.Value) | effectsOf(x.In)
	case *ir.New:
		f := writes | mayThrow // the constructor's body
		for _, arg := range x.Args {
			f |= effectsOf(arg)
		}
		return f
	case *ir.NewObject:
		var f effects
		for _, value := range x.Values {
			f |= effectsOf(value)
		}
		if x.OrderOf != nil {
			f |= effectsOf(x.OrderOf)
		}
		return f
	case *ir.FieldRef:
		if x.Field.Accessor {
			return callEffects([]ir.Expr{x.X}) // a getter may run
		}
		return readsLocals | effectsOf(x.X)
	case *ir.HasField:
		return readsLocals | effectsOf(x.X)
	case *ir.IsAbsent:
		return effectsOf(x.X)
	case *ir.HasKey:
		return readsLocals | effectsOf(x.X)
	case *ir.SetField:
		if x.Field.Accessor {
			return callEffects([]ir.Expr{x.X, x.Value}) // a setter may run
		}
		if isOptionalThroughInterface(x.X.Type(), x.Field) {
			return readsLocals | writes | mayThrow | effectsOf(x.X) | effectsOf(x.Value) // an object may not hold it (D-066)
		}
		return readsLocals | writes | effectsOf(x.X) | effectsOf(x.Value)
	}
	return 0 // constants
}

// callEffects are the effects of a call of a function of the program with args.
func callEffects(args []ir.Expr) effects {
	f := writes | mayThrow // anything a function body does
	for _, arg := range args {
		f |= effectsOf(arg)
	}
	return f
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
