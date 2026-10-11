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

// indexMethods records the method each method's function implements.
func (e *emitter) indexMethods(program *ir.Program) {
	for _, class := range program.Classes {
		for _, method := range class.Methods {
			e.methods[method.Func] = method
		}
	}
}

// classDeclarations writes each class's struct, after typedefs that let fields name any
// class, and the method-table structs of the classes that have virtual methods. A
// subclass's struct starts with its parent's, as its member pts_base.
func (e *emitter) classDeclarations(classes []*ir.Class) {
	for _, class := range classes {
		e.line(0, "typedef struct pts_class_%[1]s pts_class_%[1]s;", e.classNames[class])
	}
	for _, class := range classes {
		e.line(0, "")
		e.line(0, "struct pts_class_%s {", e.classNames[class])
		if class.Parent == nil {
			e.line(1, "const pts_type *pts_header;")
		} else {
			e.line(1, "pts_class_%s pts_base;", e.classNames[class.Parent])
		}
		for _, field := range class.Fields {
			e.line(1, "%s%s;", e.declarator(field.Type), cIdentifier(field.Name))
		}
		e.line(0, "};")
	}
	for _, class := range classes {
		if hasMethodTable(class) {
			e.methodTableStruct(class)
		}
	}
	if len(classes) > 0 {
		e.line(0, "")
	}
}

// methodTableStruct writes the struct of a class's descriptor extended with its method
// table: the parent's, as pts_base, or the pts_type itself, then a function pointer for
// each slot the class introduces.
func (e *emitter) methodTableStruct(class *ir.Class) {
	name := e.classNames[class]
	e.line(0, "")
	e.line(0, "typedef struct pts_vtable_%[1]s {", name)
	if class.Parent != nil && hasMethodTable(class.Parent) {
		e.line(1, "pts_vtable_%s pts_base;", e.classNames[class.Parent])
	} else {
		e.line(1, "pts_type pts_descriptor;")
	}
	for _, slot := range class.Slots() {
		params := []string{e.declarator(class)}
		for _, param := range slot.Func.Params[1:] {
			params = append(params, e.declarator(param.Type))
		}
		for i, param := range params {
			params[i] = strings.TrimSuffix(param, " ")
		}
		e.line(1, "%s(*%s)(%s);", e.declarator(slot.Func.Result), cIdentifier(slot.Name), strings.Join(params, ", "))
	}
	e.line(0, "} pts_vtable_%s;", name)
}

// hasMethodTable reports whether a class or an ancestor has virtual methods, so that its
// descriptor carries a method table.
func hasMethodTable(class *ir.Class) bool {
	for ; class != nil; class = class.Parent {
		if len(class.Slots()) > 0 {
			return true
		}
	}
	return false
}

// descriptor is the C expression of a class's pts_type.
func (e *emitter) descriptor(class *ir.Class) string {
	return "&pts_type_" + e.classNames[class] + descriptorPath(class)
}

// descriptorPath is where a class's pts_type is in pts_type_<class>: all of it, or inside
// its method table, if it has one.
func descriptorPath(class *ir.Class) string {
	if !hasMethodTable(class) {
		return ""
	}
	top := class
	for top.Parent != nil && hasMethodTable(top.Parent) {
		top = top.Parent
	}
	return "." + basePath(class, top) + "pts_descriptor"
}

// root is the class at the top of a class's hierarchy, whose struct holds the header.
func root(class *ir.Class) *ir.Class {
	for class.Parent != nil {
		class = class.Parent
	}
	return class
}

// basePath is the path from a subclass's struct (or method table) to the part that an
// ancestor's describes: "pts_base." once per class between them.
func basePath(class *ir.Class, ancestor *ir.Class) string {
	var path strings.Builder
	for ; class != ancestor; class = class.Parent {
		path.WriteString("pts_base.")
	}
	return path.String()
}

// classDescriptors writes each class's pts_type with its field and method tables, and
// for a class with virtual methods, the implementation of each slot.
func (e *emitter) classDescriptors(classes []*ir.Class) {
	for _, class := range classes {
		name := e.classNames[class]
		fields, methods := "NULL", "NULL"
		allFields := class.AllFields()
		if len(allFields) > 0 {
			fields = "pts_fields_" + name
			e.line(0, "static const pts_field %s[] = {", fields)
			for _, field := range allFields {
				e.line(1, "{%s, offsetof(pts_class_%s, %s), %s},", cString(field.Name), name, fieldPath(class, field), fieldKind(field.Type))
			}
			e.line(0, "};")
		}
		implemented := implementedMethods(class)
		if len(implemented) > 0 {
			methods = "pts_methods_" + name
			e.line(0, "static const pts_method %s[] = {", methods)
			for _, method := range implemented {
				e.line(1, "{%s, (void (*)(void))%s},", cString(method.Name), e.funcNames[method.Func])
			}
			e.line(0, "};")
		}
		parent := "NULL"
		if class.Parent != nil {
			parent = e.descriptor(class.Parent)
		}
		members := []string{
			".kind = PTS_KIND_OBJECT",
			".gc_descr = " + layout(class, name),
			".name = " + cString(class.Name),
			".parent = " + parent,
			".size = sizeof(pts_class_" + name + ")",
			fmt.Sprintf(".field_count = %d", len(allFields)),
			".fields = " + fields,
			fmt.Sprintf(".method_count = %d", len(implemented)),
			".methods = " + methods,
		}
		if !hasMethodTable(class) {
			e.line(0, "static const pts_type pts_type_%s = {", name)
			for _, member := range members {
				e.line(1, "%s,", member)
			}
			e.line(0, "};")
			e.line(0, "")
			continue
		}
		e.line(0, "static const pts_vtable_%[1]s pts_type_%[1]s = {", name)
		e.line(1, "%s = {", descriptorPath(class))
		for _, member := range members {
			e.line(2, "%s,", member)
		}
		e.line(1, "},")
		for owner := class; owner != nil; owner = owner.Parent {
			for _, slot := range owner.Slots() {
				if implementation := class.Method(slot.Name); implementation.Func.Body != nil {
					e.line(1, ".%s%s = %s,", basePath(class, owner), cIdentifier(slot.Name), e.funcNames[implementation.Func])
				}
			}
		}
		e.line(0, "};")
		e.line(0, "")
	}
}

// implementedMethods are the methods a class declares that have a body.
func implementedMethods(class *ir.Class) []*ir.Method {
	var methods []*ir.Method
	for _, method := range class.Methods {
		if method.Func.Body != nil {
			methods = append(methods, method)
		}
	}
	return methods
}

// fieldPath is the C member designator of a field in an instance of class: its name,
// after pts_base. once per class between class and the one that declares it.
func fieldPath(class *ir.Class, field *ir.Field) string {
	return basePath(class, field.Class) + cIdentifier(field.Name)
}

// layout is the C expression of a class's GC layout: which words of an instance may hold
// pointers. Every field takes at most a word, so a class of at most 61 fields fits a bitmap.
func layout(class *ir.Class, name string) string {
	var words []string
	allFields := class.AllFields()
	for _, field := range allFields {
		if holdsPointer(field.Type) {
			words = append(words, fmt.Sprintf("PTS_GC_WORD(pts_class_%s, %s)", name, fieldPath(class, field)))
		}
	}
	switch {
	case len(words) == 0:
		return "PTS_GC_NO_POINTERS"
	case len(allFields)+1 > maxBitmapWords:
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
	return fmt.Sprintf("({ %s%s%s = pts_object_new(%s); %s(%s); %s; })",
		prelude, e.declarator(n.Class), instance, e.descriptor(n.Class), e.funcNames[n.Class.Constructor], strings.Join(append([]string{instance}, args...), ", "), instance)
}

// fieldRef writes a field of object, an instance of class.
func (e *emitter) fieldRef(object string, class *ir.Class, field *ir.Field) string {
	if !isIdentifier(object) {
		object = "(" + object + ")"
	}
	return object + "->" + fieldPath(class, field)
}

// setField writes a store to a field; its value is the new value, or the old one if postfix.
func (e *emitter) setField(s *ir.SetField) string {
	if s.Postfix {
		old := e.temporary()
		object := e.expr(s.X) // pure: the lowering binds anything else to a local first
		class := s.X.Type().(*ir.Class)
		return fmt.Sprintf("({ %s%s = %s; %s = %s; %s; })", e.declarator(s.Field.Type), old, e.fieldRef(object, class, s.Field),
			e.fieldRef(object, class, s.Field), e.expr(s.Value), old)
	}
	return "(" + e.storeField(s) + ")"
}

// storeField writes a store to a field as a statement would: the instance, then the value, in JS's order.
func (e *emitter) storeField(s *ir.SetField) string {
	operands, prelude := e.operands([]ir.Expr{s.X, s.Value})
	return ordered(prelude, e.fieldRef(operands[0], s.X.Type().(*ir.Class), s.Field)+" = "+operands[1])
}

// virtualCall writes a call through the method table of the receiver's class: the
// receiver, an instance of the slot's class, is evaluated once, before the arguments.
func (e *emitter) virtualCall(call *ir.VirtualCall) string {
	args, prelude := e.operands(call.Args)
	slot := call.Method.Slot()
	if !isIdentifier(args[0]) {
		receiver := e.temporary()
		prelude = e.declarator(slot.Class) + receiver + " = " + args[0] + "; " + prelude
		args[0] = receiver
	}
	header := args[0] + "->" + basePath(slot.Class, root(slot.Class)) + "pts_header"
	function := fmt.Sprintf("((const pts_vtable_%s *)%s)->%s", e.classNames[slot.Class], header, cIdentifier(slot.Name))
	return ordered(prelude, function+"("+strings.Join(args, ", ")+")")
}

// classCast writes an instance as one of a related class: their structs start alike.
func (e *emitter) classCast(cast *ir.ClassCast) string {
	return "((" + strings.TrimSuffix(e.declarator(cast.To), " ") + ")" + e.expr(cast.X) + ")"
}

// receiverClass is the class a method's C function takes `this` as: for a virtual
// method, the class of its slot, so that every implementation fits the slot's type.
func (e *emitter) receiverClass(f *ir.Func) *ir.Class {
	method := e.methods[f]
	if method == nil {
		return nil
	}
	if method.Virtual {
		return method.Slot().Class
	}
	return method.Class
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
