package check

import "math"

// Numeric is the ParallelScript type of a value tsc types as number (docs/SPEC.md §3.2).
type Numeric int

const (
	Number Numeric = iota // JS number, also spelled f64
	I8
	I16
	I32
	I64 // also spelled int
	U8
	U16
	U32
	U64
	F32
)

// preludeNumerics maps the prelude's numeric type names to their types.
var preludeNumerics = map[string]Numeric{
	"int": I64, "i8": I8, "i16": I16, "i32": I32, "i64": I64,
	"u8": U8, "u16": U16, "u32": U32, "u64": U64, "f32": F32, "f64": Number,
}

var numericNames = [...]string{Number: "number", I8: "i8", I16: "i16", I32: "i32", I64: "int", U8: "u8", U16: "u16", U32: "u32", U64: "u64", F32: "f32"}

func (n Numeric) String() string { return numericNames[n] }

func (n Numeric) IsInteger() bool { return n >= I8 && n <= U64 }

func (n Numeric) IsSigned() bool { return n >= I8 && n <= I64 }

// Bits is the width of an integer type.
func (n Numeric) Bits() int {
	switch n {
	case I8, U8:
		return 8
	case I16, U16:
		return 16
	case I32, U32:
		return 32
	default:
		return 64
	}
}

// Min and Max bound an integer type's values; Max is a uint64 so u64's fits.
func (n Numeric) Min() int64 {
	if !n.IsSigned() {
		return 0
	}
	return -1 << (n.Bits() - 1)
}

func (n Numeric) Max() uint64 {
	if n.IsSigned() {
		return 1<<(n.Bits()-1) - 1
	}
	return math.MaxUint64 >> (64 - n.Bits())
}

// ImplicitlyConvertible reports whether a value of type from may be used where to is
// expected without `as`: the conversions the spec calls lossless.
func ImplicitlyConvertible(from Numeric, to Numeric) bool {
	switch {
	case from == to:
		return true
	case to == Number:
		return from.IsInteger() || from == F32 // every integer type converts to number, by the spec
	case from.IsInteger() && to.IsInteger():
		if from.IsSigned() == to.IsSigned() {
			return from.Bits() < to.Bits()
		}
		return !from.IsSigned() && from.Bits() < to.Bits() // u8 → i16 keeps every value; i8 → u16 doesn't
	}
	return false
}

// unify is the type both operands of a binary operator convert to, if there is one:
// the wider of two compatible types, or number when an integer meets a float.
func unify(a Numeric, b Numeric) (Numeric, bool) {
	switch {
	case ImplicitlyConvertible(a, b):
		return b, true
	case ImplicitlyConvertible(b, a):
		return a, true
	case a.IsInteger() && b.IsInteger():
		return 0, false // e.g. i64 and u64: no lossless common type
	}
	return Number, true // an integer or f32 meets number or f32
}
