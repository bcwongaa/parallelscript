package cgen

import (
	"fmt"
	"strings"

	"github.com/microsoft/typescript-go/pts/ir"
)

// Runtime descriptions of types (pts_check, D-071): for each type that a dynamic value is
// converted to and that valueTest can't test, and for the types of its properties and
// elements, a static pts_check that pts_value_convert reads.

// collectChecks names the checks of the types dynamic values are converted to.
func (e *emitter) collectChecks(program *ir.Program) {
	for _, f := range program.Funcs {
		ir.Walk(f.Body.Stmts, func(ir.Stmt) {}, func(x ir.Expr) {
			if from, ok := x.(*ir.FromUnion); ok && from.X.Type() == ir.Value && !hasValueTest(from.To) {
				e.check(from.To)
			}
		})
	}
}

// hasValueTest reports whether valueTest can test a dynamic value for t.
func hasValueTest(t ir.Type) bool {
	switch {
	case t == ir.Value || t == ir.Number || t == ir.String || t == ir.Bool || t == ir.Null || t == ir.Undefined || ir.IsNumeric(t) || isClass(t):
		return true
	case isUnion(t):
		for _, member := range t.(*ir.Union).Members {
			if !hasValueTest(member) {
				return false
			}
		}
		return true
	}
	return false
}

// check names the check of t, and of the types it contains, the first time it is asked for.
func (e *emitter) check(t ir.Type) string {
	if name, ok := e.checks[t]; ok {
		return name
	}
	name := fmt.Sprintf("pts_check_%d", len(e.checkOrder)+1)
	e.checks[t] = name
	e.checkOrder = append(e.checkOrder, t)
	switch t := t.(type) {
	case *ir.Union:
		for _, member := range t.Members {
			e.check(member)
		}
	case *ir.Array:
		e.check(t.Elem)
	case *ir.MapType:
		e.check(t.Key)
		if t.Value != nil {
			e.check(t.Value)
		}
		if e.mapDescriptors[t] == "" { // a copy may make one
			e.mapDescriptors[t] = fmt.Sprintf("pts_type_%s_%d", strings.ToLower(mapKind(t)), len(e.mapOrder)+1)
			e.mapOrder = append(e.mapOrder, t)
		}
	case *ir.Interface:
		for _, field := range t.Fields {
			e.check(field.Type)
		}
	case *ir.Class:
		for _, field := range t.Fields {
			e.check(field.Type)
		}
	}
	return name
}

// checkDefinitions writes the checks: declarations first, since types may contain each other.
func (e *emitter) checkDefinitions() {
	if len(e.checkOrder) == 0 {
		return
	}
	for _, t := range e.checkOrder {
		e.line(0, "static const pts_check %s;", e.checks[t])
	}
	for _, t := range e.checkOrder {
		e.checkDefinition(t)
	}
	e.line(0, "")
}

func (e *emitter) checkDefinition(t ir.Type) string {
	name := e.checks[t]
	members := []string{"", ".slot = " + fieldKind(t), ".name = " + cString(t.String())}
	switch t := t.(type) {
	case *ir.Primitive:
		switch {
		case t == ir.Value:
			members[0] = ".kind = PTS_CHECK_ANY"
		case t == ir.Number || t == ir.F32:
			members[0] = ".kind = PTS_CHECK_NUMBER"
		case ir.IsInteger(t):
			members[0] = ".kind = PTS_CHECK_INTEGER"
		case t == ir.String:
			members[0] = ".kind = PTS_CHECK_STRING"
		case t == ir.Bool:
			members[0] = ".kind = PTS_CHECK_BOOLEAN"
		case t == ir.Null:
			members[0] = ".kind = PTS_CHECK_NULL"
		case t == ir.Undefined:
			members[0] = ".kind = PTS_CHECK_UNDEFINED"
		default:
			panic("cgen: no check for " + t.String())
		}
	case *ir.Union:
		list := make([]string, len(t.Members))
		for i, member := range t.Members {
			list[i] = "&" + e.checks[member]
		}
		e.line(0, "static const pts_check *const %s_members[] = {%s};", name, strings.Join(list, ", "))
		members[0] = ".kind = PTS_CHECK_UNION"
		members = append(members, fmt.Sprintf(".count = %d", len(t.Members)), ".members = "+name+"_members")
	case *ir.Array:
		members[0] = ".kind = PTS_CHECK_ARRAY"
		members = append(members, ".element = &"+e.checks[t.Elem])
	case *ir.MapType:
		members[0] = ".kind = PTS_CHECK_" + mapKind(t)
		members = append(members, ".type = &"+e.mapDescriptors[t])
		if t.Value != nil {
			members = append(members, ".element = &"+e.checks[t.Value])
		}
	case *ir.Interface:
		members[0] = ".kind = PTS_CHECK_OBJECT"
		members = append(members, e.checkProperties(name, t.Fields, t.Copy, t.Copy != nil)...)
		if t.Copy != nil {
			members = append(members, ".type = &pts_type_"+shapeDescriptorName(e.classNames[t.Copy], t.CopyOrder))
		}
	case *ir.Class:
		if !t.Shape {
			members[0] = ".kind = PTS_CHECK_INSTANCE"
			members = append(members, ".type = "+e.descriptor(t))
			break
		}
		members[0] = ".kind = PTS_CHECK_OBJECT"
		if t.Tuple {
			members[0] = ".kind = PTS_CHECK_TUPLE"
		}
		members = append(members, e.checkProperties(name, t.Fields, t, true)...)
		members = append(members, ".type = &pts_type_"+shapeDescriptorName(e.classNames[t], 0))
	default:
		panic("cgen: no check for " + t.String())
	}
	e.line(0, "static const pts_check %s = {%s};", name, strings.Join(members, ", "))
	return name
}

// checkProperties writes the properties of an object type's check, with their offsets in
// shape (the one copies are made of) if hasShape, and returns the check's members for them.
func (e *emitter) checkProperties(name string, fields []*ir.Field, shape *ir.Class, hasShape bool) []string {
	if len(fields) == 0 {
		return nil
	}
	e.line(0, "static const pts_check_property %s_properties[] = {", name)
	for _, field := range fields {
		offset := "0"
		if hasShape {
			offset = fmt.Sprintf("offsetof(pts_class_%s, %s)", e.classNames[shape], fieldIdentifier(field.Name))
		}
		e.line(1, "{%s, &%s, %t, %s},", cString(field.Name), e.checks[field.Type], field.Optional, offset)
	}
	e.line(0, "};")
	return []string{fmt.Sprintf(".count = %d", len(fields)), ".properties = " + name + "_properties"}
}
