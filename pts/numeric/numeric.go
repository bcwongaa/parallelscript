// Package numeric holds ParallelScript's numeric types and the rules for combining them
// (docs/SPEC.md §3.2, D-012). Both the semantic pass and the checker's arithmetic typing
// (D-048) use it, so it depends on nothing above the AST.
package numeric

import (
	"math"
	"math/big"
	"strings"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/scanner"
)

// Type is the ParallelScript type of a value tsc types as a number.
type Type int

const (
	Number Type = iota // JS number, also spelled f64
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

// Names maps the prelude's numeric type names to their types.
var Names = map[string]Type{
	"int": I64, "i8": I8, "i16": I16, "i32": I32, "i64": I64,
	"u8": U8, "u16": U16, "u32": U32, "u64": U64, "f32": F32, "f64": Number,
}

var typeNames = [...]string{Number: "number", I8: "i8", I16: "i16", I32: "i32", I64: "int", U8: "u8", U16: "u16", U32: "u32", U64: "u64", F32: "f32"}

func (n Type) String() string { return typeNames[n] }

func (n Type) IsInteger() bool { return n >= I8 && n <= U64 }

func (n Type) IsSigned() bool { return n >= I8 && n <= I64 }

// Bits is the width of an integer type.
func (n Type) Bits() int {
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
func (n Type) Min() int64 {
	if !n.IsSigned() {
		return 0
	}
	return -1 << (n.Bits() - 1)
}

func (n Type) Max() uint64 {
	if n.IsSigned() {
		return 1<<(n.Bits()-1) - 1
	}
	return math.MaxUint64 >> (64 - n.Bits())
}

// ImplicitlyConvertible reports whether a value of type from may be used where to is
// expected without `as`: the conversions the spec calls lossless.
func ImplicitlyConvertible(from Type, to Type) bool {
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

// Unify is the type both operands of a binary operator convert to, if there is one:
// the wider of two compatible types, or number when an integer meets a float.
func Unify(a Type, b Type) (Type, bool) {
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

// WithUntyped is the type an operation takes when one operand is of type typed and the
// other is untyped: integer constants take the typed side's type; other constants make a number.
func WithUntyped(typed Type, untyped *ast.Node) Type {
	if typed.IsInteger() && !IsIntegerConstant(untyped) {
		return Number
	}
	return typed
}

// Operation is the type of an arithmetic or bitwise operation on two numbers, typed left
// and right, without a context to give it a type: both untyped makes a number. ok is
// false if the operands have no common type.
func Operation(left *ast.Node, leftType Type, right *ast.Node, rightType Type) (result Type, ok bool) {
	leftUntyped, rightUntyped := IsUntyped(left), IsUntyped(right)
	switch {
	case leftUntyped && rightUntyped:
		return Number, true
	case leftUntyped:
		return WithUntyped(rightType, left), true
	case rightUntyped:
		return WithUntyped(leftType, right), true
	}
	return Unify(leftType, rightType)
}

// IsUntyped reports whether node is a constant number expression with no type of its
// own: literals, possibly negated, parenthesised or combined by arithmetic.
func IsUntyped(node *ast.Node) bool {
	switch node.Kind {
	case ast.KindNumericLiteral:
		return true
	case ast.KindParenthesizedExpression:
		return IsUntyped(node.Expression())
	case ast.KindPrefixUnaryExpression:
		unary := node.AsPrefixUnaryExpression()
		return (unary.Operator == ast.KindMinusToken || unary.Operator == ast.KindPlusToken || unary.Operator == ast.KindTildeToken) && IsUntyped(unary.Operand)
	case ast.KindBinaryExpression:
		binary := node.AsBinaryExpression()
		return IsArithmetic(binary.OperatorToken.Kind) && IsUntyped(binary.Left) && IsUntyped(binary.Right)
	}
	return false
}

// IsIntegerConstant reports whether every literal in an untyped expression is an integer.
func IsIntegerConstant(node *ast.Node) bool {
	if node.Kind == ast.KindNumericLiteral {
		_, ok := IntegerLiteralValue(node)
		return ok
	}
	integer := true
	node.ForEachChild(func(child *ast.Node) bool {
		integer = integer && (!ast.IsExpression(child) || IsIntegerConstant(child))
		return !integer
	})
	return integer
}

// IsArithmetic reports whether a binary operator computes a number from two numbers:
// the arithmetic and the bitwise operators.
func IsArithmetic(operator ast.Kind) bool {
	switch operator {
	case ast.KindPlusToken, ast.KindMinusToken, ast.KindAsteriskToken, ast.KindSlashToken, ast.KindPercentToken, ast.KindAsteriskAsteriskToken,
		ast.KindAmpersandToken, ast.KindBarToken, ast.KindCaretToken,
		ast.KindLessThanLessThanToken, ast.KindGreaterThanGreaterThanToken, ast.KindGreaterThanGreaterThanGreaterThanToken:
		return true
	}
	return false
}

// IntegerLiteralValue is the exact value of a numeric literal, if it is an integer.
// It reads the source text, so integers above 2^53 keep every digit.
func IntegerLiteralValue(literal *ast.Node) (*big.Int, bool) {
	// Not literal.Text(): the scanner normalises that through a double.
	text := strings.ReplaceAll(scanner.GetTextOfNode(literal), "_", "")
	if value, ok := new(big.Int).SetString(text, 0); ok {
		return value, true
	}
	rat, ok := new(big.Rat).SetString(text) // decimals and exponents, e.g. 1e3
	if !ok || !rat.IsInt() {
		return nil, false
	}
	return rat.Num(), true
}
