package cgen

import (
	"fmt"
	"strings"

	"github.com/microsoft/typescript-go/pts/ir"
)

// collectMaps names the descriptor of each map, set and hash-map object type the program
// makes maps of (D-046, D-059).
func (e *emitter) collectMaps(program *ir.Program) {
	for _, f := range program.Funcs {
		ir.Walk(f.Body.Stmts, func(ir.Stmt) {}, func(x ir.Expr) {
			if n, ok := x.(*ir.NewMap); ok {
				e.nameMapDescriptor(n.Map)
			}
		})
	}
}

// nameMapDescriptor names the descriptor of a map, set or hash-map object type, unless it
// has one. Record<string, any>'s is the runtime's, which JSON.parse's objects have too
// (D-080), so that converting one to it keeps the object.
func (e *emitter) nameMapDescriptor(mt *ir.MapType) {
	switch {
	case e.mapDescriptors[mt] != "":
	case mt.Record && mt.Value == ir.Value:
		e.mapDescriptors[mt] = "pts_type_any_record"
	default:
		e.mapDescriptors[mt] = fmt.Sprintf("pts_type_%s_%d", strings.ToLower(mapKind(mt)), len(e.mapOrder)+1)
		e.mapOrder = append(e.mapOrder, mt)
	}
}

// mapKind is the descriptor kind of a map type: MAP, SET or RECORD.
func mapKind(mt *ir.MapType) string {
	switch {
	case mt.Set:
		return "SET"
	case mt.Record:
		return "RECORD"
	}
	return "MAP"
}

// collectionName is the name of a map type's descriptor, which console.log prints: a
// hash-map object's is "Object", as a plain object's constructor is.
func collectionName(mt *ir.MapType) string {
	switch {
	case mt.Set:
		return "Set"
	case mt.Record:
		return "Object"
	}
	return "Map"
}

// mapDescriptorDefinitions writes the descriptors of the map, set and hash-map object
// types: their key and value kinds, and the GC layout of a pts_map.
func (e *emitter) mapDescriptorDefinitions() {
	for _, mt := range e.mapOrder {
		e.line(0, "static const pts_type %s = {", e.mapDescriptors[mt])
		e.line(1, ".kind = PTS_KIND_%s,", mapKind(mt))
		e.line(1, ".gc_descr = PTS_GC_POINTER_WORDS(PTS_GC_WORD(pts_map, table)),")
		e.line(1, ".name = %q,", collectionName(mt))
		e.line(1, ".size = sizeof(pts_map),")
		e.line(1, ".key = %s,", fieldKind(mt.Key))
		if !mt.Set {
			e.line(1, ".element = %s,", fieldKind(mt.Value))
		}
		if e.declaredTypes {
			if mt.Value != nil {
				e.line(1, ".check = &%s,", e.checks[mt.Value])
			}
			if !mt.Record {
				e.line(1, ".key_check = &%s,", e.checks[mt.Key])
			}
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
		return fmt.Sprintf("pts_set_of_array(%s, %s, %t)", descriptor, e.expr(n.From), ir.HoldsUndefined(n.Map.Key)) // a hole is undefined where the set holds undefined (D-183)
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
	operands := call.Args
	if call.Op == ir.MapGetInto {
		operands = operands[:2] // the local is written, not evaluated
	}
	args, prelude := e.operands(operands)
	key := func() string { return fmt.Sprintf("(%s[]){%s}", e.cDeclaredType(mt.Key), args[1]) }
	var c string
	switch call.Op {
	case ir.MapSize:
		c = "pts_map_size(" + args[0] + ")"
	case ir.MapGet:
		c = "pts_map_get_value(" + args[0] + ", " + key() + ")"
	case ir.MapGetChecked:
		get := "PTS_MAP_GET"
		if mt.Record {
			get = "PTS_RECORD_GET" // its error names the key
		}
		c = fmt.Sprintf("%s(%s, %s, %s)", get, e.cDeclaredType(mt.Value), args[0], key())
	case ir.MapGetInto:
		c = "pts_map_get(" + args[0] + ", " + key() + ", &" + e.local(call.Args[2].(*ir.LocalRef).Local) + ")"
	case ir.MapPut:
		c = fmt.Sprintf("PTS_RECORD_PUT(%s, %s, %s, %s)", e.cDeclaredType(mt.Value), args[0], key(), args[2])
	case ir.MapSet:
		value := "NULL"
		if !mt.Set {
			value = fmt.Sprintf("(%s[]){%s}", e.cDeclaredType(mt.Value), args[2])
		}
		c = mapFunction(mt, "set") + "(" + args[0] + ", " + key() + ", " + value + ")"
	case ir.MapAssign:
		c = "pts_map_assign(" + args[0] + ", " + args[1] + ")"
	case ir.MapHas:
		c = "pts_map_has(" + args[0] + ", " + key() + ")"
	case ir.MapDelete:
		c = mapFunction(mt, "delete") + "(" + args[0] + ", " + key() + ")"
	case ir.MapClear:
		c = "pts_map_clear(" + args[0] + ")"
	case ir.MapIterate:
		if mt.Record {
			c = "pts_record_iterate(" + args[0] + ")" // in JS's key order
		} else {
			c = "pts_map_iterate(" + args[0] + ")"
		}
	default:
		panic(fmt.Sprintf("cgen: unexpected map operation %s", call.Op))
	}
	return ordered(prelude, c)
}

// mapFunction is the runtime's function for a write to a map: pts_map_<name>, or for a
// hash-map object, which may be frozen, pts_record_<name> (D-157).
func mapFunction(mt *ir.MapType, name string) string {
	if mt.Record {
		return "pts_record_" + name
	}
	return "pts_map_" + name
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
	case ir.MapPut, ir.MapDelete: // a hash-map object may be frozen (D-157)
		return readsLocals | writes | mayThrow
	}
	return readsLocals | writes
}
