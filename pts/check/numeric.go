package check

import (
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/pts/numeric"
)

// Numeric is the ParallelScript type of a value tsc types as number (docs/SPEC.md §3.2).
type Numeric = numeric.Type

const (
	Number = numeric.Number
	I8     = numeric.I8
	I16    = numeric.I16
	I32    = numeric.I32
	I64    = numeric.I64
	U8     = numeric.U8
	U16    = numeric.U16
	U32    = numeric.U32
	U64    = numeric.U64
	F32    = numeric.F32
)

// IsNumber reports whether a checker type is a number: one of the numeric types, a number
// literal, or a union of them (such as i64 | u64, which has no common type).
func IsNumber(t *checker.Type) bool {
	if t.Flags()&checker.TypeFlagsUnion != 0 {
		for _, member := range t.Types() {
			if member.Flags()&checker.TypeFlagsNumberLike == 0 {
				return false
			}
		}
		return true
	}
	return t.Flags()&checker.TypeFlagsNumberLike != 0
}

// NumericOf returns the numeric type of a checker type that is a number: number, one of
// the prelude's numeric types (each a type of its own to the checker, D-048), a number
// literal, or a union of them (their common type, if they have one).
func NumericOf(t *checker.Type) (Numeric, bool) {
	flags := t.Flags()
	switch {
	case flags&checker.TypeFlagsNumber != 0:
		if n, ok := numeric.Names[t.AsIntrinsicType().IntrinsicName()]; ok {
			return n, true
		}
		return Number, true
	case flags&checker.TypeFlagsNumberLike != 0:
		return Number, true // a literal or a numeric enum
	case flags&checker.TypeFlagsUnion != 0:
		return unionNumeric(t.Types())
	}
	return 0, false
}

// unionNumeric is the common numeric type of the members of a union, if every member is
// a number and they have one: a u8 | i32 is an i32.
func unionNumeric(members []*checker.Type) (Numeric, bool) {
	var result Numeric
	for i, member := range members {
		n, ok := NumericOf(member)
		if !ok {
			return 0, false
		}
		if i == 0 {
			result = n
		} else if result, ok = numeric.Unify(result, n); !ok {
			return 0, false
		}
	}
	return result, true
}
