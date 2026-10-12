package cgen

import (
	"strings"

	"github.com/microsoft/typescript-go/pts/ir"
)

// effects says how evaluating an expression can interact with evaluating others, which
// is what decides whether C's unspecified evaluation order could differ from JS's.
type effects uint8

const (
	readsLocals effects = 1 << iota // reads locals or fields
	writes                          // assigns a local or a field, or does output
	mayThrow                        // may stop with an error, whose order among errors and effects is observable
)

// throwingBuiltins are the builtins of String, Number and Math that can throw (beyond
// running out of memory), and others that can only throw.
var throwingBuiltins = map[*ir.Builtin]bool{
	ir.StringElementChecked: true, ir.StringRepeat: true, ir.StringPadStart: true, ir.StringPadEnd: true, ir.ThrowError: true,
	ir.StringFromCodePoint: true, ir.NumberToFixed: true, ir.NumberToExponential: true, ir.NumberToPrecision: true,
	ir.NumberToStringRadix: true, ir.I64ToStringRadix: true, ir.U64ToStringRadix: true, ir.ObjectAssign: true, ir.ValueKeyArray: true,
}

// computedBuiltins are builtins outside String, Number and Math that compute their result
// from their arguments alone, without running the program's code.
var computedBuiltins = map[*ir.Builtin]bool{
	ir.ValueTypeOf: true, ir.ValueStrictEquals: true, ir.ValueIsArray: true, ir.ValueSameValue: true, ir.TupleLength: true,
	ir.FunctionName: true, ir.FunctionLength: true, ir.ValueIsPromise: true, ir.ValueConstructorName: true, ir.ParseInt: true,
	ir.ParseFloat: true, ir.ArrayFromString: true,
}

// builtinEffects are what calling a builtin does beyond evaluating its arguments. The
// members of String, Number and Math and computedBuiltins compute from their arguments
// (some may throw); Math.random and performance.now give each call a value that depends
// on the calls before it; console methods write. Any other builtin may run the program's
// code (a getter, a toString, a callback) or change what other expressions read, so it
// writes and may throw.
func builtinEffects(b *ir.Builtin) effects {
	switch {
	case ir.IsConsole(b), b == ir.MathRandom, b == ir.PerformanceNow:
		return writes
	case b == ir.ObjectAssign:
		return writes | mayThrow
	case throwingBuiltins[b]:
		return mayThrow
	case computedBuiltins[b], strings.HasPrefix(b.Name, "String."), strings.HasPrefix(b.Name, "Number."), strings.HasPrefix(b.Name, "Math."):
		return 0
	}
	return writes | mayThrow
}

// effectsOf classifies every kind of expression; one it doesn't know is taken to do
// anything (D-145).
func effectsOf(expr ir.Expr) effects {
	switch x := expr.(type) {
	case *ir.NumberConst, *ir.IntConst, *ir.BoolConst, *ir.StringConst, *ir.NullConst, *ir.UndefinedConst, *ir.GivenConst,
		*ir.Absent, *ir.LeftOut:
		return 0
	case *ir.Resumed, *ir.ResumedBy:
		return readsLocals // what resumed the generator, kept in its frame
	case *ir.LocalRef:
		return readsLocals
	case *ir.Assign:
		return readsLocals | writes | effectsOf(x.Value)
	case *ir.CallBuiltin:
		f := builtinEffects(x.Builtin)
		for _, arg := range x.Args {
			f |= effectsOf(arg)
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
	case *ir.InstanceCall:
		return callEffects(append([]ir.Expr{x.X}, x.Args...)) | effectsOf(x.Otherwise)
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
	case *ir.RegExpLiteral:
		return mayThrow // an invalid pattern tsc let through is a SyntaxError
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
		if isDynamic(x.X.Type()) {
			return callEffects([]ir.Expr{x.X}) // a boxed object's toString or valueOf runs (D-142)
		}
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
		return readsLocals | writes | mayThrow | effectsOf(x.X) | effectsOf(x.Value) // the object may be frozen (D-157), or not hold it (D-066)
	}
	return readsLocals | writes | mayThrow
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
