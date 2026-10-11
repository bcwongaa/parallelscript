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
	kind := fieldKind(n.Array.Elem)
	if len(n.Elems) == 0 {
		return fmt.Sprintf("pts_array_of(%s, NULL, 0)", kind)
	}
	elems, prelude := e.operands(n.Elems)
	return ordered(prelude, fmt.Sprintf("pts_array_of(%s, (%s[]){%s}, %d)", kind, strings.TrimSuffix(e.declarator(n.Array.Elem), " "), strings.Join(elems, ", "), len(elems)))
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
	case ir.ArrayPop, ir.ArrayShift:
		c = fmt.Sprintf("pts_array_%s_value(%s)", call.Op, args[0])
	case ir.ArrayPopChecked:
		c = fmt.Sprintf("PTS_ARRAY_POP(%s, %s)", elem, args[0])
	case ir.ArrayShiftChecked:
		c = fmt.Sprintf("PTS_ARRAY_SHIFT(%s, %s)", elem, args[0])
	default:
		panic(fmt.Sprintf("cgen: unexpected array operation %s", call.Op))
	}
	return ordered(prelude, c)
}

// arrayEffects are the effects of an array operation beyond its operands'.
func arrayEffects(op ir.ArrayOp) effects {
	switch op {
	case ir.ArrayLength, ir.ArrayElement:
		return readsLocals
	case ir.ArrayElementChecked:
		return readsLocals | mayThrow
	case ir.ArrayPush, ir.ArrayUnshift, ir.ArrayPop, ir.ArrayShift:
		return readsLocals | writes
	}
	return readsLocals | writes | mayThrow
}
