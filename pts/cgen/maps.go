package cgen

import (
	"fmt"
	"strings"

	"github.com/microsoft/typescript-go/pts/ir"
)

// collectMaps names the descriptor of each map and set type the program makes maps of (D-046).
func (e *emitter) collectMaps(program *ir.Program) {
	for _, f := range program.Funcs {
		ir.Walk(f.Body.Stmts, func(ir.Stmt) {}, func(x ir.Expr) {
			if n, ok := x.(*ir.NewMap); ok && e.mapDescriptors[n.Map] == "" {
				e.mapDescriptors[n.Map] = fmt.Sprintf("pts_type_%s_%d", strings.ToLower(collectionName(n.Map)), len(e.mapOrder)+1)
				e.mapOrder = append(e.mapOrder, n.Map)
			}
		})
	}
}

func collectionName(mt *ir.MapType) string {
	if mt.Set {
		return "Set"
	}
	return "Map"
}

// mapDescriptorDefinitions writes the descriptors of the map and set types: their key
// and value kinds, and the GC layout of a pts_map.
func (e *emitter) mapDescriptorDefinitions() {
	for _, mt := range e.mapOrder {
		e.line(0, "static const pts_type %s = {", e.mapDescriptors[mt])
		e.line(1, ".kind = PTS_KIND_%s,", strings.ToUpper(collectionName(mt)))
		e.line(1, ".gc_descr = PTS_GC_POINTER_WORDS(PTS_GC_WORD(pts_map, table)),")
		e.line(1, ".name = %q,", collectionName(mt))
		e.line(1, ".size = sizeof(pts_map),")
		e.line(1, ".key = %s,", fieldKind(mt.Key))
		if !mt.Set {
			e.line(1, ".element = %s,", fieldKind(mt.Value))
		}
		e.line(0, "};")
	}
	if len(e.mapOrder) > 0 {
		e.line(0, "")
	}
}

// newMap writes `new Map()`, `new Set()` and their copies, and a set of an array.
func (e *emitter) newMap(n *ir.NewMap) string {
	descriptor := "&" + e.mapDescriptors[n.Map]
	switch {
	case n.From == nil:
		return "pts_map_new(" + descriptor + ")"
	case isArrayType(n.From.Type()):
		return "pts_set_of_array(" + descriptor + ", " + e.expr(n.From) + ")"
	}
	return "pts_map_copy(" + descriptor + ", " + e.expr(n.From) + ")"
}

func isArrayType(t ir.Type) bool {
	_, ok := t.(*ir.Array)
	return ok
}

// mapCall writes an operation on a map or set; keys and values are passed by address.
func (e *emitter) mapCall(call *ir.MapCall) string {
	mt := call.Map
	switch call.Op {
	case ir.MapNext:
		return "pts_map_next(&" + e.local(call.Args[0].(*ir.LocalRef).Local) + ")"
	case ir.MapIteratorKey:
		return fmt.Sprintf("PTS_MAP_ITERATOR_KEY(%s, %s)", e.cDeclaredType(mt.Key), e.local(call.Args[0].(*ir.LocalRef).Local))
	case ir.MapIteratorValue:
		return fmt.Sprintf("PTS_MAP_ITERATOR_VALUE(%s, %s)", e.cDeclaredType(mt.Value), e.local(call.Args[0].(*ir.LocalRef).Local))
	}
	args, prelude := e.operands(call.Args)
	key := func() string { return fmt.Sprintf("(%s[]){%s}", e.cDeclaredType(mt.Key), args[1]) }
	var c string
	switch call.Op {
	case ir.MapSize:
		c = "pts_map_size(" + args[0] + ")"
	case ir.MapGet:
		c = "pts_map_get_value(" + args[0] + ", " + key() + ")"
	case ir.MapGetChecked:
		c = fmt.Sprintf("PTS_MAP_GET(%s, %s, %s)", e.cDeclaredType(mt.Value), args[0], key())
	case ir.MapSet:
		value := "NULL"
		if !mt.Set {
			value = fmt.Sprintf("(%s[]){%s}", e.cDeclaredType(mt.Value), args[2])
		}
		c = "pts_map_set(" + args[0] + ", " + key() + ", " + value + ")"
	case ir.MapHas:
		c = "pts_map_has(" + args[0] + ", " + key() + ")"
	case ir.MapDelete:
		c = "pts_map_delete(" + args[0] + ", " + key() + ")"
	case ir.MapClear:
		c = "pts_map_clear(" + args[0] + ")"
	case ir.MapIterate:
		c = "pts_map_iterate(" + args[0] + ")"
	default:
		panic(fmt.Sprintf("cgen: unexpected map operation %s", call.Op))
	}
	return ordered(prelude, c)
}

// cDeclaredType is a type's C type as a cast or compound literal takes it: "double", "pts_string *".
func (e *emitter) cDeclaredType(t ir.Type) string {
	return strings.TrimSuffix(e.declarator(t), " ")
}

// mapEffects are the effects of a map operation beyond its operands'.
func mapEffects(op ir.MapOp) effects {
	switch op {
	case ir.MapSize, ir.MapGet, ir.MapHas, ir.MapIterate, ir.MapIteratorKey, ir.MapIteratorValue:
		return readsLocals
	case ir.MapGetChecked:
		return readsLocals | mayThrow
	}
	return readsLocals | writes
}
