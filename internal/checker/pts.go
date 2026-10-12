package checker

// PTS: ParallelScript's numeric types (docs/FORK.md, D-048). This file is new; the other
// edits are the calls in getDeclaredTypeOfTypeAlias, checkPrefixUnaryExpression,
// getUnaryResultType and checkBinaryLikeExpression.

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/pts/lib"
	"github.com/microsoft/typescript-go/pts/numeric"
)

// ptsNumericAliases are the ParallelScript prelude's numeric aliases of number that are
// types of their own. i64 is declared as int and f64 as number, so neither is listed.
var ptsNumericAliases = map[string]bool{
	"int": true, "i8": true, "i16": true, "i32": true, "u8": true, "u16": true, "u32": true, "u64": true, "f32": true,
}

// ptsNumericAliasType is the type of a type alias declared as t. ParallelScript's prelude
// declares its integer types and f32 as aliases of number; each becomes a number type of
// its own, named as the alias, so that it survives in the types tsc builds from it
// (u8[], Map<string, int>, Box<int>) and its name shows in messages. The types have
// number's flags, so tsc relates them to number and to each other exactly as it relates
// number to itself; ParallelScript's semantic pass enforces the conversion rules.
func (c *Checker) ptsNumericAliasType(symbol *ast.Symbol, declaration *ast.Node, t *Type) *Type {
	if t != c.numberType || !ptsNumericAliases[symbol.Name] || !lib.IsLibPath(ast.GetSourceFileOfNode(declaration).FileName()) {
		return t
	}
	return c.newIntrinsicType(TypeFlagsNumber, symbol.Name)
}

// ptsArithmeticType is the type of an arithmetic or bitwise operation on two numbers,
// left and right, by ParallelScript's rules (numeric.Operation): a u8 times a u8, or a u8
// times an integer literal, is a u8. Anything else is a number, as in tsc.
func (c *Checker) ptsArithmeticType(left *ast.Node, leftType *Type, right *ast.Node, rightType *Type) *Type {
	leftNumeric, leftRepresentative, leftOk := c.ptsNumericType(leftType)
	rightNumeric, rightRepresentative, rightOk := c.ptsNumericType(rightType)
	if !leftOk || !rightOk {
		return c.numberType
	}
	result, ok := numeric.Operation(left, leftNumeric, right, rightNumeric)
	switch {
	case !ok || result == numeric.Number:
		return c.numberType
	case result == leftNumeric:
		return leftRepresentative
	default:
		return rightRepresentative
	}
}

// ptsUnaryType is the type of -x, +x, ~x, ++x or x--: x's numeric type, or a number.
func (c *Checker) ptsUnaryType(operandType *Type) *Type {
	if _, representative, ok := c.ptsNumericType(operandType); ok {
		return representative
	}
	return c.numberType
}

// ptsNumericType returns the ParallelScript numeric type of a type tsc sees as a number,
// and the type that stands for it: number itself for number, number literals and numeric
// enums; one of the prelude's number types for itself; for a union of them, their common
// type, if they have one.
func (c *Checker) ptsNumericType(t *Type) (numeric.Type, *Type, bool) {
	switch {
	case t.flags&TypeFlagsNumber != 0:
		if n, ok := numeric.Names[t.AsIntrinsicType().intrinsicName]; ok {
			return n, t, true
		}
		return numeric.Number, c.numberType, true
	case t.flags&TypeFlagsNumberLike != 0:
		return numeric.Number, c.numberType, true
	case t.flags&TypeFlagsUnion != 0:
		var result numeric.Type
		var representative *Type
		for i, member := range t.Types() {
			n, memberRepresentative, ok := c.ptsNumericType(member)
			if !ok {
				return 0, nil, false
			}
			if i == 0 {
				result, representative = n, memberRepresentative
				continue
			}
			unified, ok := numeric.Unify(result, n)
			switch {
			case !ok:
				return 0, nil, false
			case unified == numeric.Number:
				representative = c.numberType
			case unified == n:
				representative = memberRepresentative
			}
			result = unified
		}
		return result, representative, true
	}
	return 0, nil, false
}

// PtsTypeArgumentsOfSignature returns the type arguments an instantiated signature (such
// as a generic call's resolved signature) gives its target's type parameters, or nil for
// a signature that isn't an instantiation. ParallelScript monomorphizes generic functions
// (D-049) and needs them.
func (c *Checker) PtsTypeArgumentsOfSignature(signature *Signature) []*Type {
	if signature.target == nil || signature.mapper == nil {
		return nil
	}
	arguments := make([]*Type, len(signature.target.typeParameters))
	for i, parameter := range signature.target.typeParameters {
		arguments[i] = signature.mapper.Map(parameter)
	}
	return arguments
}

// PtsInstantiateType returns t with each of sources replaced by the target at the same
// index: a generic function's types in one of its instantiations (D-049).
func (c *Checker) PtsInstantiateType(t *Type, sources []*Type, targets []*Type) *Type {
	return c.instantiateType(t, newTypeMapper(sources, targets))
}

// PtsInstantiateSignatureInContextOf returns a generic signature instantiated with the
// type arguments tsc infers from a non-generic contextual signature, as when it relates
// the two: the instance a generic function used as a value of that type stands for (D-078).
func (c *Checker) PtsInstantiateSignatureInContextOf(signature *Signature, contextual *Signature) *Signature {
	return c.instantiateSignatureInContextOf(signature, contextual, nil /*inferenceContext*/, nil /*compareTypes*/)
}

// PtsInferTypeArgumentsFromResult returns the type arguments tsc infers for a generic
// signature's type parameters from its return type related to target alone, as for a call
// whose result goes where a value of type target goes, or nil if it infers none for one
// of them. ParallelScript instantiates a call so where the instance its arguments imply
// makes values of another representation than target's (D-095).
func (c *Checker) PtsInferTypeArgumentsFromResult(signature *Signature, target *Type) []*Type {
	context := c.newInferenceContext(signature.typeParameters, signature, InferenceFlagsNone, nil)
	c.inferTypes(context.inferences, target, c.getReturnTypeOfSignature(signature), InferencePriorityNone, false)
	arguments := make([]*Type, len(context.inferences))
	for i, inference := range context.inferences {
		if len(inference.candidates) == 0 {
			return nil
		}
		arguments[i] = c.getInferredType(context, i)
	}
	return arguments
}
