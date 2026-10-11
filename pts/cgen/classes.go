package cgen

import (
	"fmt"
	"strings"

	"github.com/microsoft/typescript-go/pts/ir"
)

// maxBitmapWords is the largest instance, in words, a bitmap layout can describe (D-024).
const maxBitmapWords = 62

// fieldKinds are the runtime's names for what each field type holds.
var fieldKinds = map[ir.Type]string{
	ir.Number: "PTS_FIELD_NUMBER", ir.Bool: "PTS_FIELD_BOOL", ir.String: "PTS_FIELD_STRING",
	ir.Value: "PTS_FIELD_VALUE", ir.Undefined: "PTS_FIELD_VALUE", ir.Null: "PTS_FIELD_VALUE",
	ir.I8: "PTS_FIELD_I8", ir.I16: "PTS_FIELD_I16", ir.I32: "PTS_FIELD_I32", ir.I64: "PTS_FIELD_I64",
	ir.U8: "PTS_FIELD_U8", ir.U16: "PTS_FIELD_U16", ir.U32: "PTS_FIELD_U32", ir.U64: "PTS_FIELD_U64",
	ir.F32: "PTS_FIELD_F32",
}

// nameClasses gives each class the suffix of its C names: pts_class_<suffix> for its
// struct, pts_type_<suffix> for its descriptor. Outside the entry module the module's
// name comes first, as for functions.
func (e *emitter) nameClasses(program *ir.Program) {
	used := map[string]bool{}
	for _, class := range program.Classes {
		e.classNames[class] = unique(used, modulePrefix(class.Module)+prefixedIdentifier(class.Name))
	}
}

// classDeclarations writes each class's struct, after typedefs that let fields name any class.
func (e *emitter) classDeclarations(classes []*ir.Class) {
	for _, class := range classes {
		e.line(0, "typedef struct pts_class_%[1]s pts_class_%[1]s;", e.classNames[class])
	}
	for _, class := range classes {
		e.line(0, "")
		e.line(0, "struct pts_class_%s {", e.classNames[class])
		e.line(1, "const pts_type *pts_header;")
		for _, field := range class.Fields {
			e.line(1, "%s%s;", e.declarator(field.Type), cIdentifier(field.Name))
		}
		e.line(0, "};")
	}
	if len(classes) > 0 {
		e.line(0, "")
	}
}

// classDescriptors writes each class's pts_type with its field and method tables.
func (e *emitter) classDescriptors(classes []*ir.Class) {
	for _, class := range classes {
		name := e.classNames[class]
		fields, methods := "NULL", "NULL"
		if len(class.Fields) > 0 {
			fields = "pts_fields_" + name
			e.line(0, "static const pts_field %s[] = {", fields)
			for _, field := range class.Fields {
				e.line(1, "{%s, offsetof(pts_class_%s, %s), %s},", cString(field.Name), name, cIdentifier(field.Name), fieldKind(field.Type))
			}
			e.line(0, "};")
		}
		if len(class.Methods) > 0 {
			methods = "pts_methods_" + name
			e.line(0, "static const pts_method %s[] = {", methods)
			for _, method := range class.Methods {
				e.line(1, "{%s, (void (*)(void))%s},", cString(method.Name[len(class.Name)+1:]), e.funcNames[method])
			}
			e.line(0, "};")
		}
		e.line(0, "static const pts_type pts_type_%s = {", name)
		e.line(1, ".kind = PTS_KIND_OBJECT,")
		e.line(1, ".gc_descr = %s,", layout(class, name))
		e.line(1, ".name = %s,", cString(class.Name))
		e.line(1, ".size = sizeof(pts_class_%s),", name)
		e.line(1, ".field_count = %d,", len(class.Fields))
		e.line(1, ".fields = %s,", fields)
		e.line(1, ".method_count = %d,", len(class.Methods))
		e.line(1, ".methods = %s,", methods)
		e.line(0, "};")
		e.line(0, "")
	}
}

// layout is the C expression of a class's GC layout: which words of an instance may hold
// pointers. Every field takes at most a word, so a class of at most 61 fields fits a bitmap.
func layout(class *ir.Class, name string) string {
	var words []string
	for _, field := range class.Fields {
		if holdsPointer(field.Type) {
			words = append(words, fmt.Sprintf("PTS_GC_WORD(pts_class_%s, %s)", name, cIdentifier(field.Name)))
		}
	}
	switch {
	case len(words) == 0:
		return "PTS_GC_NO_POINTERS"
	case len(class.Fields)+1 > maxBitmapWords:
		return "PTS_GC_ALL_WORDS(sizeof(pts_class_" + name + "))"
	}
	return "PTS_GC_POINTER_WORDS(" + strings.Join(words, " | ") + ")"
}

func holdsPointer(t ir.Type) bool {
	_, isClass := t.(*ir.Class)
	return isClass || t == ir.String || t == ir.Value || t == ir.Undefined || t == ir.Null
}

func fieldKind(t ir.Type) string {
	if _, isClass := t.(*ir.Class); isClass {
		return "PTS_FIELD_OBJECT"
	}
	return fieldKinds[t]
}

// newInstance writes `new C(args)`: the arguments, then a zeroed instance, then the constructor.
func (e *emitter) newInstance(n *ir.New) string {
	args, prelude := e.operands(n.Args)
	instance := e.temporary()
	name := e.classNames[n.Class]
	return fmt.Sprintf("({ %spts_class_%s *%s = pts_object_new(&pts_type_%s); %s(%s); %s; })",
		prelude, name, instance, name, e.funcNames[n.Class.Constructor], strings.Join(append([]string{instance}, args...), ", "), instance)
}

// fieldRef writes a field of an instance.
func (e *emitter) fieldRef(object string, field *ir.Field) string {
	if !isIdentifier(object) {
		object = "(" + object + ")"
	}
	return object + "->" + cIdentifier(field.Name)
}

// setField writes a store to a field; its value is the new value, or the old one if postfix.
func (e *emitter) setField(s *ir.SetField) string {
	if s.Postfix {
		old := e.temporary()
		object := e.expr(s.X) // pure: the lowering binds anything else to a local first
		return fmt.Sprintf("({ %s%s = %s; %s = %s; %s; })", e.declarator(s.Field.Type), old, e.fieldRef(object, s.Field),
			e.fieldRef(object, s.Field), e.expr(s.Value), old)
	}
	return "(" + e.storeField(s) + ")"
}

// storeField writes a store to a field as a statement would: the instance, then the value, in JS's order.
func (e *emitter) storeField(s *ir.SetField) string {
	operands, prelude := e.operands([]ir.Expr{s.X, s.Value})
	return ordered(prelude, e.fieldRef(operands[0], s.Field)+" = "+operands[1])
}

// bind writes a value evaluated once into a local, then the expression that uses it.
func (e *emitter) bind(b *ir.Bind) string {
	value := e.expr(b.Value)
	return fmt.Sprintf("({ %s%s = %s; %s; })", e.declarator(b.Local.Type), e.declareLocal(b.Local), value, e.expr(b.In))
}

func isIdentifier(s string) bool {
	for i, r := range s {
		if !(r == '_' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || i > 0 && r >= '0' && r <= '9') {
			return false
		}
	}
	return s != ""
}
