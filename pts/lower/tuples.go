package lower

import (
	"fmt"
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
	for _, flag := range flags {
		if flag&checker.ElementFlagsRequired == 0 {
			l.notSupportedYet(node, fmt.Sprintf("tuples with optional or rest elements (%s) are", l.checker.TypeToString(t)))
			return nil
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
		if types[i] = l.irType(element, node); types[i] == nil {
			return nil
		}
	}
	return l.tupleOf(types)
}

// tupleOf is the shape of the tuples whose elements have the given types.
func (l *lowerer) tupleOf(types []ir.Type) *ir.Class {
	keys := make([]string, len(types))
	for i, elementType := range types {
		keys[i] = typeKey(elementType)
	}
	key := strings.Join(keys, ",")
	if shape, ok := l.tuples[key]; ok {
		return shape
	}
	shape := &ir.Class{Name: "tuple" + strconv.Itoa(len(l.tuples)+1), Shape: true, Tuple: true}
	for i, elementType := range types {
		shape.Fields = append(shape.Fields, &ir.Field{Name: strconv.Itoa(i), Type: elementType, Class: shape})
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
	return &ir.NewObject{Shape: shape, Order: 0, Values: values}
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
	return &ir.FieldRef{X: object, Field: field}
}

// tupleArray copies the elements of x, a tuple of type t, into a new array of the union of
// their types, for `for…of` and the array methods that don't change an array (D-089).
func (l *lowerer) tupleArray(x ir.Expr, t *checker.Type, node *ast.Node) ir.Expr {
	shape := x.Type().(*ir.Class)
	elementTypes := l.checker.GetTypeArguments(t)
	elem := l.irType(l.checker.GetUnionType(elementTypes[:min(len(elementTypes), len(shape.Fields))]), node)
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
	return &ir.Bind{Local: tuple, Value: x, In: &ir.NewArray{Array: array, Elems: elems}}
}

// tupleCopyMethods are the array methods a tuple has as an array of its elements: the
// ones that don't change the array.
var tupleCopyMethods = map[string]bool{
	"map": true, "forEach": true, "filter": true, "reduce": true, "reduceRight": true, "some": true, "every": true,
	"find": true, "findIndex": true, "findLast": true, "findLastIndex": true, "flatMap": true, "join": true,
	"slice": true, "includes": true, "indexOf": true, "lastIndexOf": true, "concat": true, "toString": true, "at": true,
	"toSorted": true, "toReversed": true,
}
