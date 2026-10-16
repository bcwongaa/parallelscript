package lower

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/pts/ir"
)

// Tuples are shapes whose fields, named 0, 1, …, are their elements (D-053): fixed
// layouts with each element unboxed, which print as arrays.

// tupleType returns the shape of a tuple type, one per list of element types, or reports
// (at node) why it has none and returns nil.
func (l *lowerer) tupleType(t *checker.Type, node *ast.Node) ir.Type {
	flags := t.TargetTupleType().ElementFlags()
	required := 0
	for i, flag := range flags {
		switch {
		case flag&checker.ElementFlagsVariable != 0: // `[number, ...number[]]`: an array of every element's type (D-100)
			elements := l.checker.GetTypeArguments(t)[:len(flags)]
			elem := l.irType(l.checker.GetUnionType(elements), node)
			if elem == nil {
				return nil
			}
			return l.arrayOf(elem)
		case flag&checker.ElementFlagsRequired != 0 && required < i:
			l.notSupportedYet(node, fmt.Sprintf("tuples with required elements after optional ones (%s) are", l.checker.TypeToString(t)))
			return nil
		case flag&checker.ElementFlagsRequired != 0:
			required++
		}
	}
	if l.tuplesInProgress[t] {
		l.notSupportedYet(node, fmt.Sprintf("recursive tuple types (%s) are", l.checker.TypeToString(t)))
		return nil
	}
	l.tuplesInProgress[t] = true
	defer delete(l.tuplesInProgress, t)
	elements := l.checker.GetTypeArguments(t)[:len(flags)]
	types := make([]ir.Type, len(elements))
	for i, element := range elements {
		if i >= required {
			element = l.checker.GetUnionType([]*checker.Type{element, l.checker.GetUndefinedType()})
		}
		if types[i] = l.irType(element, node); types[i] == nil {
			return nil
		}
	}
	return l.tupleWithOptional(types, required)
}

// tupleOf is the shape of the tuples whose elements have the given types.
func (l *lowerer) tupleOf(types []ir.Type) *ir.Class {
	return l.tupleWithOptional(types, len(types))
}

// tupleWithOptional is the shape of the tuples whose elements have the given types, all
// but the first required of them optional: fields marked Optional, which hold the absent
// value for an element left out, so that a tuple's length is that of the elements it has
// (pts_tuple_length, D-100).
func (l *lowerer) tupleWithOptional(types []ir.Type, required int) *ir.Class {
	keys := make([]string, len(types))
	for i, elementType := range types {
		keys[i] = typeKey(elementType)
	}
	key := strings.Join(keys, ",")
	if required < len(types) {
		key += "?" + strconv.Itoa(required)
	}
	if shape, ok := l.tuples[key]; ok {
		return shape
	}
	shape := &ir.Class{Name: "tuple" + strconv.Itoa(len(l.tuples)+1), Shape: true, Tuple: true}
	for i, elementType := range types {
		shape.Fields = append(shape.Fields, &ir.Field{Name: strconv.Itoa(i), Type: elementType, Class: shape, Optional: i >= required})
	}
	shape.Order(shape.Fields)
	l.tuples[key] = shape
	l.shapeList = append(l.shapeList, shape)
	return shape
}

// isTuple reports whether t is a tuple's shape.
func isTuple(t ir.Type) bool {
	class, ok := t.(*ir.Class)
	return ok && class.Tuple
}

// tupleLiteral lowers an array literal that makes a tuple of the given shape: its
// elements, converted to the element types, in order; a spread tuple's in its place.
func (l *lowerer) tupleLiteral(node *ast.Node, shape *ir.Class) ir.Expr {
	values := make([]ir.Expr, 0, len(shape.Fields))
	for _, element := range node.AsArrayLiteralExpression().Elements.Nodes {
		parts := l.argumentValues(element) // a spread tuple passes its elements (D-057)
		if parts == nil {
			return nil
		}
		for _, part := range parts {
			if _, ok := part.(*spreadValue); ok {
				l.notSupportedYet(element, "spreading an array into a tuple is")
				return nil
			}
			value := l.convert(part, shape.Fields[len(values)].Type, element)
			if value == nil {
				return nil
			}
			values = append(values, value)
		}
	}
	for _, field := range shape.Fields[len(values):] {
		values = append(values, &ir.Absent{T: field.Type}) // an optional element left out
	}
	return &ir.NewObject{Shape: shape, Order: 0, Values: values}
}

// hasOptionalElements reports whether a tuple's shape has optional elements, so that its
// length is known only at run time.
func hasOptionalElements(shape *ir.Class) bool {
	return len(shape.Fields) > 0 && shape.Fields[len(shape.Fields)-1].Optional
}

// tupleShapeOf returns the shape of the tuple an expression gives, or nil if it gives no tuple.
func (l *lowerer) tupleShapeOf(node *ast.Node) *ir.Class {
	if t := l.typeAt(node); checker.IsTupleType(t) {
		if shape, ok := l.irType(t, node).(*ir.Class); ok {
			return shape
		}
	}
	return nil
}

// tupleElement returns the field of the element `t[i]` reads or writes, for a tuple t and
// a constant index i; ok is false if node is no element access of a tuple. A non-constant
// index is reported.
func (l *lowerer) tupleElement(node *ast.Node) (field *ir.Field, ok bool) {
	if node.Kind != ast.KindElementAccessExpression {
		return nil, false
	}
	access := node.AsElementAccessExpression()
	shape := l.tupleShapeOf(access.Expression)
	if shape == nil {
		return nil, false
	}
	index := ast.SkipParentheses(access.ArgumentExpression)
	if index.Kind != ast.KindNumericLiteral {
		l.notSupportedYet(access.ArgumentExpression, "a tuple's elements at an index that isn't a constant are")
		return nil, true
	}
	field = shape.Field(index.Text())
	if field == nil {
		l.notSupportedYet(access.ArgumentExpression, "this tuple index is")
	}
	return field, true
}

// tupleElementRead lowers `t[i]` for a tuple t: a read of the element's field.
func (l *lowerer) tupleElementRead(node *ast.Node, field *ir.Field) ir.Expr {
	object := l.expression(node.Expression())
	if object == nil {
		return nil
	}
	return l.narrowed(node, &ir.FieldRef{X: object, Field: field}) // `found[0]` after `typeof found[0] !== "string"`
}

// tupleArray copies the elements of x, a tuple of type t, into a new array of the union of
// their types, for `for…of` and the array methods that don't change an array (D-089).
func (l *lowerer) tupleArray(x ir.Expr, t *checker.Type, node *ast.Node) ir.Expr {
	shape := x.Type().(*ir.Class)
	elementTypes := l.checker.GetTypeArguments(t)
	elementTypes = elementTypes[:min(len(elementTypes), len(shape.Fields))]
	if hasOptionalElements(shape) {
		elementTypes = append(slices.Clone(elementTypes), l.checker.GetUndefinedType())
	}
	elem := l.irType(l.checker.GetUnionType(elementTypes), node)
	if elem == nil {
		return nil
	}
	return l.tupleAsArray(x, l.arrayOf(elem), node)
}

// tupleAsArray copies the elements of x, a tuple, into a new array of type array.
func (l *lowerer) tupleAsArray(x ir.Expr, array *ir.Array, node *ast.Node) ir.Expr {
	shape := x.Type().(*ir.Class)
	tuple := l.newLocal("tuple", shape)
	elems := make([]ir.Expr, len(shape.Fields))
	for i := range elems {
		if elems[i] = l.convert(&ir.FieldRef{X: &ir.LocalRef{Local: tuple}, Field: shape.Field(strconv.Itoa(i))}, array.Elem, node); elems[i] == nil {
			return nil
		}
	}
	var made ir.Expr = &ir.NewArray{Array: array, Elems: elems}
	if hasOptionalElements(shape) { // cut to the elements the tuple has
		copied := l.newLocal("copy", array)
		length := &ir.CallBuiltin{Builtin: ir.TupleLength, Args: []ir.Expr{box(&ir.LocalRef{Local: tuple})}}
		made = &ir.Bind{Local: copied, Value: made, In: &ir.Sequence{
			X: &ir.ArrayCall{Op: ir.ArraySetLength, Args: []ir.Expr{&ir.LocalRef{Local: copied}, length}},
			Y: &ir.LocalRef{Local: copied},
		}}
	}
	return &ir.Bind{Local: tuple, Value: x, In: made}
}

// tupleCopyMethods are the array methods a tuple has as an array of its elements: the
// ones that don't change the array.
var tupleCopyMethods = map[string]bool{
	"map": true, "forEach": true, "filter": true, "reduce": true, "reduceRight": true, "some": true, "every": true,
	"find": true, "findIndex": true, "findLast": true, "findLastIndex": true, "flat": true, "flatMap": true, "join": true,
	"slice": true, "includes": true, "indexOf": true, "lastIndexOf": true, "concat": true, "toString": true, "at": true,
	"toSorted": true, "toReversed": true,
}
