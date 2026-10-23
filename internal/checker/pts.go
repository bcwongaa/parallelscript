package checker

// PTS: ParallelScript's numeric types (docs/FORK.md, D-048). This file is new; the other
// edits are the calls in getDeclaredTypeOfTypeAlias, checkPrefixUnaryExpression,
// getUnaryResultType and checkBinaryLikeExpression.

import (
	"slices"

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

// PtsIntersectionType returns the intersection of types as tsc forms it, never where tsc
// reduces it to never (disjoint primitives or literals): whether a value of an instance's
// declared type can have the type tsc narrows it to (D-195).
func (c *Checker) PtsIntersectionType(types []*Type) *Type {
	return c.getIntersectionType(types)
}

// PtsReducedType returns t as tsc reduces it where it is used: never for an intersection
// of object types whose discriminants conflict (`{ kind: "a" } & { kind: "b" }`) (D-195).
func (c *Checker) PtsReducedType(t *Type) *Type {
	return c.getReducedType(t)
}

// PtsFlowTypeOfReference returns the type that control flow analysis narrows reference to
// where it is, starting from declaredType instead of the reference's declared type: what
// the tests before it leave of a wider type's values, such as an enum's representation's
// (D-197). The flow caches key on the declared type, so this doesn't disturb them.
func (c *Checker) PtsFlowTypeOfReference(reference *ast.Node, declaredType *Type) *Type {
	return c.getFlowTypeOfReference(reference, declaredType)
}

// PtsTypePredicateParameterIndex returns the index of the parameter that signature's type
// predicate (`x is T`, `asserts x is T`, declared or inferred) narrows, or -1 if it has
// none or narrows `this`: a call's argument there is a test of what it holds (D-197).
func (c *Checker) PtsTypePredicateParameterIndex(signature *Signature) int {
	predicate := c.getTypePredicateOfSignature(signature)
	if predicate == nil || predicate.kind != TypePredicateKindIdentifier && predicate.kind != TypePredicateKindAssertsIdentifier {
		return -1
	}
	return int(predicate.parameterIndex)
}

// PtsInstantiateSignatureInContextOf returns a generic signature instantiated with the
// type arguments tsc infers from a non-generic contextual signature, as when it relates
// the two: the instance a generic function used as a value of that type stands for (D-078).
func (c *Checker) PtsInstantiateSignatureInContextOf(signature *Signature, contextual *Signature) *Signature {
	return c.instantiateSignatureInContextOf(signature, contextual, nil /*inferenceContext*/, nil /*compareTypes*/)
}

// PtsWithoutCallChain returns a `?.` call's resolved signature without the call-chain
// flags tsc adds to it (getOptionalCallSignature), so that its return type lacks the
// undefined (or inside the chain, the optional marker) the chain adds: the signature of the
// same call written without `?.`, whose instance ParallelScript looks up (D-193). Other
// signatures are returned as they are.
func (c *Checker) PtsWithoutCallChain(signature *Signature) *Signature {
	if signature.flags&SignatureFlagsCallChainFlags != 0 {
		return c.cloneSignature(signature) // which keeps the propagating flags only
	}
	// A generic call's resolved signature is an instantiation of the candidate tsc made
	// optional (reorderCandidates), whose flags instantiation doesn't propagate.
	if target := signature.target; target != nil && target.flags&SignatureFlagsCallChainFlags != 0 && signature.mapper != nil {
		return c.instantiateSignatureEx(c.cloneSignature(target), signature.mapper, true /*eraseTypeParameters*/)
	}
	return signature
}

// PtsIsOuterCallChain reports whether a resolved signature is that of the outermost call
// of an optional chain (`f?.(x)`, `o?.m(x)`), whose return type tsc makes optional.
func (c *Checker) PtsIsOuterCallChain(signature *Signature) bool {
	return signature.flags&SignatureFlagsIsOuterCallChain != 0 || signature.target != nil && signature.target.flags&SignatureFlagsIsOuterCallChain != 0
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

// PtsInferInstanceTypeArguments returns instance, a reference to a generic class (the
// `C<any, …>` that `x instanceof C` narrows x to where x's declared type isn't derived from
// C), with the type arguments that source, a type C's objects are held as, implies, where it
// implies one instance alone (D-218): a class's place holds the instances of its subclasses
// whose superclass is that class instance, so a class source decides a type parameter that
// C's `extends` chain passes up as it is (`Option<number>` for `Some<T> extends Option<T>`;
// not `Bag<A | string>`, which several A give); an interface's place holds the objects that
// provide its members with the same types, so an interface or object type decides those its
// members mention, as tsc infers them from it structurally (`Cache<string, number>` to
// `MapLRU<K, V>`'s `get(key: K): V | undefined`; not a phantom parameter), and an argument
// that C's `implements` clause passes up as it is must agree. A union's members decide alone
// and must agree; one that implies no instance but can hold any of C (`object`, `{}`) leaves
// it undecided. It returns nil where C's type arguments aren't decided, or the instance isn't
// assignable to source. ParallelScript makes an instance of a generic class per type
// arguments (D-050), and the object in a branch of `x instanceof C` is the instance its
// declared type implies, not C<any, …> (H10).
func (c *Checker) PtsInferInstanceTypeArguments(source *Type, instance *Type) *Type {
	if instance.flags&TypeFlagsObject == 0 || instance.objectFlags&ObjectFlagsReference == 0 {
		return nil
	}
	target := instance.AsTypeReference().target
	if target.objectFlags&ObjectFlagsClassOrInterface == 0 {
		return nil
	}
	typeParameters := target.AsInterfaceType().TypeParameters()
	if len(typeParameters) == 0 {
		return nil
	}
	if source.flags&TypeFlagsUnion == 0 {
		return c.ptsInferInstance(source, target, typeParameters)
	}
	var result *Type
	for _, member := range source.Types() {
		switch inferred := c.ptsInferInstance(member, target, typeParameters); {
		case inferred == nil && c.isTypeAssignableTo(instance, member):
			return nil // `object`, `{}`: any instance of C
		case inferred == nil: // `undefined`, `string`, an unrelated interface
		case result == nil:
			result = inferred
		case inferred != result:
			return nil // `Sink<number> | Sink<string>`
		}
	}
	return result
}

// ptsInferInstance is PtsInferInstanceTypeArguments's instance of target, a generic class
// declaration with typeParameters, for source, a type that isn't a union.
func (c *Checker) ptsInferInstance(source *Type, target *Type, typeParameters []*Type) *Type {
	if source.flags&TypeFlagsObject == 0 {
		return nil
	}
	sourceIsClass := source.symbol != nil && source.symbol.Flags&ast.SymbolFlagsClass != 0
	passed := make([]*Type, len(typeParameters)) // as C's heritage passes them up to source's declaration
	for _, base := range c.ptsHeritageTypes(target, nil, true, map[*Type]bool{}) {
		if base.t.objectFlags&ObjectFlagsReference == 0 || source.objectFlags&ObjectFlagsReference == 0 || base.t.AsTypeReference().target != source.AsTypeReference().target || sourceIsClass && !base.viaExtends {
			continue
		}
		arguments, sourceArguments := c.getTypeArguments(base.t), c.getTypeArguments(source)
		for i, argument := range arguments[:min(len(arguments), len(sourceArguments))] {
			if j := slices.Index(typeParameters, argument); j >= 0 {
				if passed[j] != nil && passed[j] != sourceArguments[i] {
					return nil
				}
				passed[j] = sourceArguments[i]
			}
		}
	}
	arguments := passed
	if !sourceIsClass {
		structural := c.newInferenceContext(typeParameters, nil, InferenceFlagsNone, nil)
		c.inferTypes(structural.inferences, source, target, InferencePriorityNone, false)
		arguments = make([]*Type, len(typeParameters))
		for i := range typeParameters {
			if !ptsHasCandidates(structural.inferences[i]) {
				return nil // a phantom parameter, which the source's members don't decide
			}
			arguments[i] = c.getInferredType(structural, i)
			if passed[i] != nil && passed[i] != arguments[i] {
				return nil
			}
		}
	} else if slices.Contains(arguments, nil) {
		return nil
	}
	result := c.createTypeReference(target, arguments)
	if !c.isTypeAssignableTo(result, source) {
		return nil
	}
	return result
}

func ptsHasCandidates(inference *InferenceInfo) bool {
	return len(inference.candidates) != 0 || len(inference.contraCandidates) != 0
}

// ptsHeritage is a type a class or interface declaration extends or implements, directly
// or through its ancestors, and whether it is reached through `extends` clauses of classes
// alone.
type ptsHeritage struct {
	t          *Type
	viaExtends bool
}

// ptsHeritageTypes lists the types a class or interface declaration (target) extends and
// implements, and theirs in turn, in terms of target's type parameters; mapper instantiates
// an ancestor's in terms of its own, and viaExtends says whether target is reached through
// classes' `extends` clauses alone.
func (c *Checker) ptsHeritageTypes(target *Type, mapper *TypeMapper, viaExtends bool, seen map[*Type]bool) []ptsHeritage {
	if seen[target] {
		return nil
	}
	seen[target] = true
	isClass := target.symbol.Flags&ast.SymbolFlagsClass != 0
	var direct []ptsHeritage
	for _, base := range c.getBaseTypes(target) {
		direct = append(direct, ptsHeritage{base, viaExtends && isClass})
	}
	for _, declaration := range target.symbol.Declarations {
		if ast.IsClassLike(declaration) {
			for _, node := range ast.GetImplementsTypeNodes(declaration) {
				direct = append(direct, ptsHeritage{c.getTypeFromTypeNode(node.AsNode()), false})
			}
		}
	}
	var all []ptsHeritage
	for _, base := range direct {
		if mapper != nil {
			base.t = c.instantiateType(base.t, mapper)
		}
		all = append(all, base)
		if base.t.flags&TypeFlagsObject == 0 || base.t.objectFlags&ObjectFlagsReference == 0 {
			continue
		}
		ancestor := base.t.AsTypeReference().target
		if ancestor.objectFlags&ObjectFlagsClassOrInterface == 0 {
			continue
		}
		var inner *TypeMapper
		if parameters := ancestor.AsInterfaceType().TypeParameters(); len(parameters) > 0 {
			arguments := c.getTypeArguments(base.t)
			if len(arguments) < len(parameters) {
				continue
			}
			inner = newTypeMapper(parameters, arguments[:len(parameters)])
		}
		all = append(all, c.ptsHeritageTypes(ancestor, inner, base.viaExtends, seen)...)
	}
	return all
}
