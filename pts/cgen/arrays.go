package cgen

import (
	"fmt"
	"strings"

	"github.com/microsoft/typescript-go/pts/ir"
)

// elementType is the C type of an array's elements, as the PTS_ARRAY_* macros take it.
func (e *emitter) elementType(array ir.Expr) string {
	return strings.TrimSuffix(e.declarator(array.Type().(*ir.Array).Elem), " ")
}

// newArray writes an array literal: its elements, evaluated in order, copied into a new array (D-038).
func (e *emitter) newArray(n *ir.NewArray) string {
	descriptor := e.arrayDescriptor(n.Array)
	if n.Length != nil {
		length, prelude := e.operands([]ir.Expr{n.Length})
		return ordered(prelude, fmt.Sprintf("pts_array_of_length(%s, %s)", descriptor, length[0]))
	}
	if len(n.Elems) == 0 {
		return fmt.Sprintf("pts_array_of(%s, NULL, 0)", descriptor)
	}
	elems, prelude := e.operands(n.Elems)
	return ordered(prelude, fmt.Sprintf("pts_array_of(%s, (%s[]){%s}, %d)", descriptor, strings.TrimSuffix(e.declarator(n.Array.Elem), " "), strings.Join(elems, ", "), len(elems)))
}

// arrayCall writes an operation on an array, its operands evaluated left to right.
func (e *emitter) arrayCall(call *ir.ArrayCall) string {
	args, prelude := e.operands(call.Args)
	elem := e.elementType(call.Args[0])
	var c string
	switch call.Op {
	case ir.ArrayLength:
		c = fmt.Sprintf("pts_array_length(%s)", args[0])
	case ir.ArraySetLength:
		c = fmt.Sprintf("pts_array_set_length(%s, %s)", args[0], args[1])
	case ir.ArrayElement:
		c = fmt.Sprintf("pts_array_element(%s, %s)", args[0], args[1])
	case ir.ArrayElementChecked:
		get := "PTS_ARRAY_GET"
		if holdsPointer(call.Args[0].Type().(*ir.Array).Elem) {
			get = "PTS_ARRAY_GET_WORD" // a zero word is a hole (D-038)
		}
		c = fmt.Sprintf("%s(%s, %s, %s)", get, elem, args[0], args[1])
	case ir.ArraySetElement:
		c = fmt.Sprintf("PTS_ARRAY_SET(%s, %s, %s, %s)", elem, args[0], args[1], args[2])
	case ir.ArrayPush, ir.ArrayUnshift:
		c = fmt.Sprintf("pts_array_%s(%s, (%s[]){%s}, %d)", call.Op, args[0], elem, strings.Join(args[1:], ", "), len(args)-1)
	case ir.ArrayPushAll:
		c = fmt.Sprintf("pts_array_push_all(%s, %s)", args[0], args[1])
	case ir.ArrayUnshiftAll:
		c = fmt.Sprintf("pts_array_unshift_all(%s, %s)", args[0], args[1])
	case ir.ArrayPop, ir.ArrayShift:
		c = fmt.Sprintf("pts_array_%s_value(%s)", call.Op, args[0])
	case ir.ArrayPopChecked:
		c = fmt.Sprintf("PTS_ARRAY_POP(%s, %s)", elem, args[0])
	case ir.ArrayShiftChecked:
		c = fmt.Sprintf("PTS_ARRAY_SHIFT(%s, %s)", elem, args[0])
	case ir.ArraySlice:
		c = fmt.Sprintf("pts_array_slice(%s, %s, %s)", args[0], args[1], args[2])
	case ir.ArraySplice:
		c = fmt.Sprintf("pts_array_splice(%s, %s, %s, %s)", args[0], args[1], args[2], elements(elem, args[3:]))
	case ir.ArrayConcat:
		c = e.concat(call, args)
	case ir.ArrayIndexOf, ir.ArrayLastIndexOf, ir.ArrayIncludes:
		c = fmt.Sprintf("pts_array_%s(%s, (%s[]){%s}, %s)", cIdentifierOf(call.Op), args[0], elem, args[1], args[2])
	case ir.ArrayJoin:
		c = fmt.Sprintf("pts_array_join(%s, %s)", args[0], args[1])
	case ir.ArrayReverse, ir.ArraySort:
		c = fmt.Sprintf("pts_array_%s(%s)", call.Op, args[0])
	case ir.ArrayFlat:
		flat := "pts_array_flat"
		if call.Args[1].Type() == ir.Value {
			flat = "pts_array_flat_value" // a depth converted after the length is read, as JS converts it
		}
		c = fmt.Sprintf("%s(%s, %s, %s)", flat, args[0], args[1], e.arrayDescriptor(call.To))
	case ir.ArrayFill:
		c = fmt.Sprintf("pts_array_fill(%s, (%s[]){%s}, %s, %s)", args[0], elem, args[1], args[2], args[3])
	case ir.ArrayAt:
		c = fmt.Sprintf("pts_array_at(%s, %s)", args[0], args[1])
	case ir.ArrayCopy:
		c = fmt.Sprintf("pts_array_copy_as_read(%s)", args[0]) // the copying methods read holes as undefined (D-085)
	case ir.ArrayWith:
		c = fmt.Sprintf("pts_array_with(%s, %s, (%s[]){%s})", args[0], args[1], elem, args[2])
	case ir.ArrayKeys:
		c = fmt.Sprintf("pts_array_keys(%s)", args[0])
	case ir.ArrayPushFlat:
		c = fmt.Sprintf("pts_array_push_flat(%s, %s)", args[0], args[1])
	case ir.ArraySortWith:
		c = fmt.Sprintf("pts_array_sort_with(%s, %s, %s)", args[0], args[1], e.comparators[call.Args[0].Type().(*ir.Array).Elem])
	default:
		panic(fmt.Sprintf("cgen: unexpected array operation %s", call.Op))
	}
	return ordered(prelude, c)
}

// elements writes values of the C type elem as a compound literal and their count, or NULL, 0.
func elements(elem string, values []string) string {
	if len(values) == 0 {
		return "NULL, 0"
	}
	return fmt.Sprintf("(%s[]){%s}, %d", elem, strings.Join(values, ", "), len(values))
}

// cIdentifierOf is the runtime's name for an array operation: index_of for indexOf.
func cIdentifierOf(op ir.ArrayOp) string {
	var name strings.Builder
	for _, r := range op.String() {
		if r >= 'A' && r <= 'Z' {
			name.WriteByte('_')
			r += 'a' - 'A'
		}
		name.WriteRune(r)
	}
	return name.String()
}

// concat writes a.concat(x…), whose operands args are already evaluated: a copy of a,
// then each x's elements if it is an array of a's type, or else x itself, appended.
func (e *emitter) concat(call *ir.ArrayCall, args []string) string {
	result := e.temporary()
	var c strings.Builder
	fmt.Fprintf(&c, "({ pts_array *%s = pts_array_copy(%s); ", result, args[0])
	for i, x := range call.Args[1:] {
		if x.Type() == call.Args[0].Type() {
			fmt.Fprintf(&c, "pts_array_append(%s, %s); ", result, args[i+1])
		} else {
			fmt.Fprintf(&c, "pts_array_push(%s, %s); ", result, elements(e.elementType(call.Args[0]), args[i+1:i+2]))
		}
	}
	fmt.Fprintf(&c, "%s; })", result)
	return c.String()
}

// collectComparators names the function that calls a comparator with two elements, for
// each element type that arrays are sorted with a comparator of.
func (e *emitter) collectComparators(program *ir.Program) {
	for _, f := range program.Funcs {
		ir.Walk(f.Body.Stmts, func(ir.Stmt) {}, func(x ir.Expr) {
			if call, ok := x.(*ir.ArrayCall); ok && call.Op == ir.ArraySortWith {
				elem := call.Args[0].Type().(*ir.Array).Elem
				if e.comparators[elem] == "" {
					e.comparators[elem] = fmt.Sprintf("pts_compare_%d", len(e.comparatorOrder)+1)
					e.comparatorOrder = append(e.comparatorOrder, elem)
				}
			}
		})
	}
}

// comparatorFunctions writes, for each element type sorted with a comparator, the
// function pts_array_sort_with calls the comparator through: it loads the two elements
// and calls the comparator's code with them as their own type.
func (e *emitter) comparatorFunctions() {
	for _, elem := range e.comparatorOrder {
		t := strings.TrimSuffix(e.declarator(elem), " ")
		code := e.functionPointer(&ir.FuncType{Params: []ir.Type{elem, elem}, Result: ir.Number})
		e.line(0, "static double %s(pts_closure *compare, const void *a, const void *b) {", e.comparators[elem])
		e.line(1, "return ((%s)compare->code)(compare, *(%s const *)a, *(%s const *)b);", code, t, t)
		e.line(0, "}")
		e.line(0, "")
	}
}

// arrayEffects are the effects of an array operation beyond its operands'.
func arrayEffects(op ir.ArrayOp) effects {
	switch op {
	case ir.ArrayLength, ir.ArrayElement, ir.ArraySlice, ir.ArrayConcat, ir.ArrayIndexOf, ir.ArrayLastIndexOf, ir.ArrayIncludes,
		ir.ArrayAt, ir.ArrayCopy, ir.ArrayKeys:
		return readsLocals
	case ir.ArrayElementChecked, ir.ArrayWith, ir.ArrayFlat: // flat checks the leaves it converts (D-180)
		return readsLocals | mayThrow
	}
	return readsLocals | writes | mayThrow // the array may be frozen (D-157); join and sort may call toString methods
}
