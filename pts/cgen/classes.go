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
		if class.Builtin { // the runtime's pts_type_builtin_<name>, which no other class may take
			e.classNames[class] = unique(used, "builtin_"+class.Name)
		}
	}
	for _, class := range program.Classes {
		if class.Builtin {
			continue
		}
		prefix := ""
		if class.Module != nil { // not a shape
			prefix = modulePrefix(class.Module)
		}
		e.classNames[class] = unique(used, prefix+prefixedIdentifier(class.Name))
	}
}

// externFunctions names the runtime functions that implement the constructors and
// methods of builtin classes (D-068), and any other function the program calls that the
// runtime implements (Date's, D-087).
func (e *emitter) externFunctions(program *ir.Program) {
	for _, class := range program.Classes {
		if !class.Builtin {
			continue
		}
		if class.Constructor != nil {
			e.funcNames[class.Constructor] = class.Constructor.Extern
		}
		for _, method := range class.Methods {
			e.funcNames[method.Func] = method.Func.Extern
		}
	}
	for _, f := range program.Funcs {
		ir.Walk(f.Body.Stmts, func(ir.Stmt) {}, func(x ir.Expr) {
			if call, ok := x.(*ir.Call); ok && call.Func.Extern != "" {
				e.funcNames[call.Func] = call.Func.Extern
			}
		})
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

// collectInterfaceMembers lists, for each class, the methods, getters and setters that
// calls through interfaces may need: those named like a method of some interface, the
// accessors named like a property of one that some class provides with accessors, and
// toString(), which the runtime calls to convert an instance to a string.
// Each is the implementation the class has, its own or inherited. Their functions get
// entry points, named pts_entry_<function>.
func (e *emitter) collectInterfaceMembers(program *ir.Program) {
	wanted := map[ir.MethodKind]map[string]bool{ir.Plain: {}, ir.Getter: {}, ir.Setter: {}}
	for _, key := range program.InKeys {
		wanted[ir.Plain][key], wanted[ir.Getter][key], wanted[ir.Setter][key] = true, true, true
	}
	for _, iface := range program.Interfaces {
		for _, method := range iface.Methods {
			wanted[ir.Plain][method.Name] = true
		}
		for _, property := range iface.Fields {
			if property.Accessor {
				wanted[ir.Getter][property.Name], wanted[ir.Setter][property.Name] = true, true
			}
		}
	}
	for _, class := range program.Classes {
		if class.Builtin {
			continue // its descriptor is the runtime's
		}
		for owner := class; owner != nil; owner = owner.Parent {
			for _, method := range owner.Methods {
				callable := wanted[ir.Plain][method.Name] || isToString(method) || isValueOf(method) || e.declaredTypes && !strings.HasPrefix(method.Name, "#")
				if class.Method(method.Name, method.Kind) != method || method.Abstract || method.Kind == ir.Plain && !callable {
					continue // overridden below, not implemented, or a method not called through interfaces, by join or through `any` (D-074); every getter and setter is, for `any` (D-070)
				}
				e.interfaceMembers[class] = append(e.interfaceMembers[class], method)
				if _, named := e.entryNames[method.Func]; !named {
					e.entryNames[method.Func] = "pts_entry_" + strings.TrimPrefix(e.funcNames[method.Func], "pts_fn_")
					e.entryOrder = append(e.entryOrder, method)
				}
			}
		}
	}
}

// isToString reports whether a method is a toString() returning a string, which the
// runtime calls where JS converts an object to a string (join).
func isToString(method *ir.Method) bool {
	return method.Kind == ir.Plain && method.Name == "toString" && len(method.Func.Params) == 1 && method.Func.Result == ir.String
}

// isValueOf reports whether a method is a valueOf() returning a primitive, which the
// runtime calls where JS converts an object to a primitive (ToPrimitive, D-088).
func isValueOf(method *ir.Method) bool {
	if method.Kind != ir.Plain || method.Name != "valueOf" || len(method.Func.Params) != 1 {
		return false
	}
	result := method.Func.Result
	return result == ir.String || result == ir.Bool || ir.IsNumeric(result)
}

// entryFunctions writes the entry point of each method that interfaces call: a function
// that takes `this` as any object and calls the method with it as its class.
func (e *emitter) entryFunctions() {
	for _, method := range e.entryOrder {
		f := method.Func
		used := map[string]bool{"this": true}
		params := []string{"pts_object *this"}
		args := []string{"(" + strings.TrimSuffix(e.declarator(e.receiverClass(f)), " ") + ")this"}
		for _, param := range f.Params[1:] {
			name := unique(used, cIdentifier(param.Name))
			params = append(params, e.declarator(param.Type)+name)
			args = append(args, name)
		}
		call := e.funcNames[f] + "(" + strings.Join(args, ", ") + ");"
		if f.Result != ir.Void {
			call = "return " + call
		}
		e.line(0, "static %s%s(%s) { %s }", e.declarator(f.Result), e.entryNames[f], strings.Join(params, ", "), call)
	}
	if len(e.entryOrder) > 0 {
		e.line(0, "")
	}
}

// classDeclarations writes each class's struct, after typedefs that let fields name any
// class, and the method-table structs of the classes that have virtual methods. A
// subclass's struct starts with its parent's, as its member pts_base.
func (e *emitter) classDeclarations(classes []*ir.Class) {
	for _, class := range classes {
		if class.Builtin {
			continue // pts.h declares pts_class_builtin_<class> (D-068)
		}
		e.line(0, "typedef struct pts_class_%[1]s pts_class_%[1]s;", e.classNames[class])
	}
	for _, class := range classes {
		if class.Builtin {
			continue
		}
		e.line(0, "")
		e.line(0, "struct pts_class_%s {", e.classNames[class])
		if class.Parent == nil {
			e.line(1, "const pts_type *pts_header;")
		} else {
			e.line(1, "pts_class_%s pts_base;", e.classNames[class.Parent])
		}
		for _, field := range class.Fields {
			e.line(1, "%s%s;", e.declarator(field.Type), fieldIdentifier(field.Name))
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
		e.line(1, "%s(*%s)(%s);", e.declarator(slot.Func.Result), slotName(slot), e.slotParams(slot))
	}
	e.line(0, "} pts_vtable_%s;", name)
}

// slotParams are the C parameter types of a method table entry: its class's instance
// first, then the slot method's parameters.
func (e *emitter) slotParams(slot *ir.Method) string {
	params := []string{e.declarator(slot.Class)}
	for _, param := range slot.Func.Params[1:] {
		params = append(params, e.declarator(param.Type))
	}
	for i, param := range params {
		params[i] = strings.TrimSuffix(param, " ")
	}
	return strings.Join(params, ", ")
}

// slotPointerType is the C type of a method table entry, a function pointer.
func (e *emitter) slotPointerType(slot *ir.Method) string {
	return fmt.Sprintf("%s(*)(%s)", e.declarator(slot.Func.Result), e.slotParams(slot))
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
// ancestor's describes: "pts_base." once per class between them, except for the builtin
// classes, which are laid out as their parents.
func basePath(class *ir.Class, ancestor *ir.Class) string {
	var path strings.Builder
	for ; class != ancestor; class = class.Parent {
		if !class.Builtin {
			path.WriteString("pts_base.")
		}
	}
	return path.String()
}

// classDescriptors writes each class's pts_type with its field and method tables, and
// for a class with virtual methods, the implementation of each slot; a shape has a
// pts_type for each of its property orders.
func (e *emitter) classDescriptors(classes []*ir.Class) {
	for _, class := range classes {
		if class.Builtin {
			continue // the runtime's
		}
		if class.Shape {
			e.shapeDescriptors(class)
			continue
		}
		name := e.classNames[class]
		fields, methods := "NULL", "NULL"
		visible := visibleFields(class)
		if len(visible) > 0 {
			fields = "pts_fields_" + name
			e.line(0, "static const pts_field %s[] = {", fields)
			for _, field := range visible {
				e.line(1, "{%s, offsetof(pts_class_%s, %s), %s%s},", cString(field.Name), name, fieldPath(class, field), fieldKind(field.Type), e.declaredType(field.Type))
			}
			e.line(0, "};")
		}
		callable := e.interfaceMembers[class]
		if len(callable) > 0 {
			methods = "pts_methods_" + name
			e.line(0, "static const pts_method %s[] = {", methods)
			for _, method := range callable {
				declared := ""
				if method.Kind != ir.Plain {
					declared = e.declaredType(accessorType(method.Kind, method.Func.Params, method.Func.Result))
				}
				if method.Kind == ir.Plain {
					declared = e.dynamicMethodMembers(e.entryNames[method.Func], paramTypes(method.Func.Params[1:]), method.Func.Result)
				}
				e.line(1, "{%s, %s, (pts_function)%s, %s%s},", cString(method.Name), methodKinds[method.Kind], e.entryNames[method.Func], resultKind(method), declared)
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
			fmt.Sprintf(".field_count = %d", len(visible)),
			".fields = " + fields,
			fmt.Sprintf(".method_count = %d", len(callable)),
			".methods = " + methods,
		}
		if class.Display != "" {
			members = append(members, ".display_name = "+cString(class.Display))
		}
		members = append(members, e.instanceTable(name, class.Instances)...)
		if hasMethodTable(class) {
			members = append(members, ".descriptor_size = sizeof(pts_vtable_"+name+")") // a frozen twin copies the method table too (D-157)
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
				if implementation := class.Method(slot.Name, slot.Kind); !implementation.Abstract {
					function := e.funcNames[implementation.Func]
					if implementation.Func.Result != slot.Func.Result { // a covariant result, a pointer to a subclass's instance
						function = fmt.Sprintf("(%s)%s", e.slotPointerType(slot), function)
					}
					e.line(1, ".%s%s = %s,", basePath(class, owner), slotName(slot), function)
				}
			}
		}
		e.line(0, "};")
		e.line(0, "")
	}
}

// shapeDescriptors writes a pts_type for each property order of a shape's objects:
// pts_type_<shape> for the first, pts_type_<shape>_<n> for the nth.
func (e *emitter) shapeDescriptors(shape *ir.Class) {
	methods := e.shapeMethodTable(shape)
	for i, order := range shape.Orders {
		name := shapeDescriptorName(e.classNames[shape], i)
		fields := "NULL"
		entries := shapeFieldEntries(shape, order)
		if len(entries) > 0 {
			fields = "pts_fields_" + name
			e.line(0, "static const pts_field %s[] = {", fields)
			for _, entry := range entries {
				declared := ""
				if entry.field.Kind == ir.Plain {
					declared = e.declaredType(entry.field.Type)
				}
				e.line(1, "{%s, offsetof(pts_class_%s, %s), %s%s},", cString(entry.name), e.classNames[shape], fieldIdentifier(entry.field.Name), entry.kind, declared)
			}
			e.line(0, "};")
		}
		e.line(0, "static const pts_type pts_type_%s = {", name)
		if shape.Tuple {
			e.line(1, ".kind = PTS_KIND_TUPLE,")
		} else {
			e.line(1, ".kind = PTS_KIND_OBJECT,")
		}
		e.line(1, ".gc_descr = %s,", layout(shape, e.classNames[shape]))
		if shape.Tuple {
			e.line(1, ".name = \"Array\", /* a tuple is an array for console.log */")
		} else {
			e.line(1, ".name = \"Object\", /* a plain object for console.log */")
		}
		e.line(1, ".size = sizeof(pts_class_%s),", e.classNames[shape])
		e.line(1, ".field_count = %d,", len(entries))
		e.line(1, ".fields = %s,", fields)
		if methods != "" {
			e.line(1, ".method_count = %d,", len(methodFields(shape)))
			e.line(1, ".methods = %s,", methods)
		}
		e.line(0, "};")
		e.line(0, "")
	}
	if len(shape.Orders) > 1 { // for spread copies that take their source's order (D-108)
		descriptors := make([]string, len(shape.Orders))
		for i := range shape.Orders {
			descriptors[i] = "&pts_type_" + shapeDescriptorName(e.classNames[shape], i)
		}
		e.line(0, "static const pts_type *const pts_orders_%s[] = {%s};", e.classNames[shape], strings.Join(descriptors, ", "))
		e.line(0, "")
	}
}

// fieldEntry is an entry of a descriptor's field table: a property's name, the field that
// holds it (for an accessor property, its getter's or setter's closure) and its kind.
type fieldEntry struct {
	name  string
	field *ir.Field
	kind  string
}

// shapeFieldEntries are the field table of a shape's descriptor for one property order:
// a field per property, and an entry that console.log shows as [Getter], [Setter] or
// [Getter/Setter] for each accessor property, where its getter or setter comes first (D-045).
func shapeFieldEntries(shape *ir.Class, order []*ir.Field) []fieldEntry {
	var entries []fieldEntry
	listed := map[string]bool{}
	for _, field := range order {
		if field.Kind == ir.Plain {
			entries = append(entries, fieldEntry{field.Name, field, fieldKind(field.Type)})
			continue
		}
		if listed[field.Property] {
			continue
		}
		listed[field.Property] = true
		getter, setter := shape.AccessorField(field.Property, ir.Getter), shape.AccessorField(field.Property, ir.Setter)
		kind := "PTS_FIELD_GETTER_SETTER"
		switch {
		case setter == nil:
			kind = "PTS_FIELD_GETTER"
		case getter == nil:
			kind = "PTS_FIELD_SETTER"
		}
		entries = append(entries, fieldEntry{field.Property, field, kind})
	}
	return entries
}

// methodFields are the fields of a shape that hold its objects' methods (D-044) and its
// accessor properties' getters and setters (D-045).
func methodFields(shape *ir.Class) []*ir.Field {
	var methods []*ir.Field
	for _, field := range shape.Fields {
		if field.Method {
			methods = append(methods, field)
		}
	}
	return methods
}

// shapeMethodTable writes the entry point of each method of a shape's objects, which
// interfaces call, and the method table its descriptors share; it returns the table's
// name, or "" if the objects have no methods. An entry point takes `this` as any object
// and calls the closure the object's field holds with it (D-036, D-044).
func (e *emitter) shapeMethodTable(shape *ir.Class) string {
	methods := methodFields(shape)
	if len(methods) == 0 {
		return ""
	}
	name := e.classNames[shape]
	entries := make([]string, len(methods))
	for i, field := range methods {
		ft := field.Type.(*ir.FuncType)
		entries[i] = shapeEntryName(name, field)
		if field.Kind != ir.Plain {
			entries[i] = "pts_entry_" + name + "_" + strings.ToLower(methodKinds[field.Kind][len("PTS_"):]) + "_" + memberIdentifier(field.Property)
		}
		params := []string{"pts_object *this"}
		args := []string{"method", "(pts_class_" + name + " *)this"}
		for j, param := range ft.Params[1:] {
			arg := fmt.Sprintf("arg%d", j+1)
			params = append(params, e.declarator(param)+arg)
			args = append(args, arg)
		}
		call := fmt.Sprintf("((%s)method->code)(%s);", e.functionPointer(ft), strings.Join(args, ", "))
		if ft.Result != ir.Void {
			call = "return " + call
		}
		e.line(0, "static %s%s(%s) {", e.declarator(ft.Result), entries[i], strings.Join(params, ", "))
		e.line(1, "pts_closure *method = ((pts_class_%s *)this)->%s;", name, fieldIdentifier(field.Name))
		e.line(1, "%s", call)
		e.line(0, "}")
	}
	table := "pts_methods_" + name
	e.line(0, "static const pts_method %s[] = {", table)
	for i, field := range methods {
		ft := field.Type.(*ir.FuncType)
		params := make([]*ir.Local, len(ft.Params))
		for j, param := range ft.Params {
			params[j] = &ir.Local{Type: param}
		}
		if field.Kind != ir.Plain {
			e.line(1, "{%s, %s, (pts_function)%s, %s%s},", cString(field.Property), methodKinds[field.Kind], entries[i], accessorKind(field.Kind, params, ft.Result), e.declaredType(shapeAccessorType(field)))
		} else {
			e.line(1, "{%s, PTS_METHOD, (pts_function)%s, 0%s},", cString(field.Name), entries[i], e.dynamicMethodMembers(entries[i], ft.Params[1:], ft.Result))
		}
	}
	e.line(0, "};")
	return table
}

// shapeEntryName is the name of the entry point of a shape's method, which its field holds.
func shapeEntryName(shape string, field *ir.Field) string {
	return "pts_entry_" + shape + "_" + memberIdentifier(field.Name)
}

func shapeDescriptorName(shape string, order int) string {
	if order == 0 {
		return shape
	}
	return fmt.Sprintf("%s_%d", shape, order+1)
}

// newObject writes an object literal: a zeroed object of its shape, then each
// property's value stored in the literal's order.
func (e *emitter) newObject(n *ir.NewObject) string {
	object := e.temporary()
	var c strings.Builder
	fmt.Fprintf(&c, "({ %s%s = pts_object_new(&pts_type_%s); ", e.declarator(n.Shape), object, shapeDescriptorName(e.classNames[n.Shape], n.Order))
	for i, field := range n.Shape.Orders[n.Order] {
		fmt.Fprintf(&c, "%s->%s = %s; ", object, fieldIdentifier(field.Name), e.expr(n.Values[i]))
	}
	if n.OrderOf != nil && len(n.Shape.Orders) > 1 {
		fmt.Fprintf(&c, "pts_object_take_order((pts_object *)%s, (const pts_object *)(%s), pts_orders_%s, %d); ", object, e.expr(n.OrderOf), e.classNames[n.Shape], len(n.Shape.Orders))
	}
	fmt.Fprintf(&c, "%s; })", object)
	return c.String()
}

// visibleFields are the fields of an instance that console.log and JSON see: all but
// the `#private` ones, inherited first.
func visibleFields(class *ir.Class) []*ir.Field {
	var fields []*ir.Field
	for _, field := range class.AllFields() {
		if !strings.HasPrefix(field.Name, "#") {
			fields = append(fields, field)
		}
	}
	return fields
}

// methodKinds are the runtime's names for the kinds of methods.
var methodKinds = map[ir.MethodKind]string{ir.Plain: "PTS_METHOD", ir.Getter: "PTS_GETTER", ir.Setter: "PTS_SETTER"}

// resultKind is a class method's pts_method.result: what an accessor returns or takes,
// or what a valueOf() returns, for ToPrimitive (D-088); 0 for other methods.
func resultKind(method *ir.Method) string {
	if isValueOf(method) {
		return fieldKind(method.Func.Result)
	}
	return accessorKind(method.Kind, method.Func.Params, method.Func.Result)
}

// accessorKind is what a getter returns, or a setter takes (its parameter after `this`),
// as a field kind, for reads and writes through `any` (D-070); 0 for a method.
func accessorKind(kind ir.MethodKind, params []*ir.Local, result ir.Type) string {
	if kind == ir.Plain {
		return "0"
	}
	return fieldKind(accessorType(kind, params, result))
}

// accessorType is the type a getter returns or a setter takes.
func accessorType(kind ir.MethodKind, params []*ir.Local, result ir.Type) ir.Type {
	if kind == ir.Getter {
		return result
	}
	return params[1].Type
}

// shapeAccessorType is the type of the accessor property whose getter or setter a shape's
// field holds.
func shapeAccessorType(field *ir.Field) ir.Type {
	ft := field.Type.(*ir.FuncType)
	if field.Kind == ir.Getter {
		return ft.Result
	}
	return ft.Params[1]
}

// slotName is the member of a method table that holds a slot: the method's name, or for
// a getter or setter, pts_get_<name> or pts_set_<name>.
func slotName(slot *ir.Method) string {
	switch slot.Kind {
	case ir.Getter:
		return "pts_get_" + prefixedIdentifier(slot.Name)
	case ir.Setter:
		return "pts_set_" + prefixedIdentifier(slot.Name)
	}
	return cIdentifier(slot.Name)
}

// fieldPath is the C member designator of a field in an instance of class: its name,
// after pts_base. once per class between class and the one that declares it.
func fieldPath(class *ir.Class, field *ir.Field) string {
	return basePath(class, field.Class) + fieldIdentifier(field.Name)
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
	return isObject(t) || t == ir.String || t == ir.Value || t == ir.Undefined || t == ir.Null || isUnion(t)
}

func fieldKind(t ir.Type) string {
	if isObject(t) {
		return "PTS_FIELD_OBJECT"
	}
	if isUnion(t) {
		return "PTS_FIELD_VALUE"
	}
	return fieldKinds[t]
}

// mayBeUnset reports whether a read of a field may find it unset, NULL: a field of an
// object type of a class's instance stays NULL until its initializer or the constructor
// sets it, which a method the constructor calls can read first; a shape's fields are set
// when it is made (D-141).
func mayBeUnset(x *ir.FieldRef) bool {
	if x.Raw || x.Field.Optional || !isObject(x.Field.Type) {
		return false
	}
	switch t := x.X.Type().(type) {
	case *ir.Class:
		return !t.Shape
	case *ir.Interface:
		return true // it may refer to a class's instance
	}
	return false
}

// checkedFieldRead writes a read of a field that throws TypeError if the field is unset
// (mayBeUnset), naming the member read from it when it is a receiver, as Node does; where
// its use sees an unset field as undefined (a test of truth, boxing), the read is plain.
func (e *emitter) checkedFieldRead(x *ir.FieldRef) string {
	read := e.fieldRead(e.expr(x.X), x.X.Type(), x.Field)
	if !mayBeUnset(x) || e.unsetAllowed[x] {
		return read
	}
	member := "NULL"
	if name, ok := e.receiverMembers[x]; ok {
		member = cString(name)
	}
	return fmt.Sprintf("((%s)pts_check_field_set(%s, %s, %s))", strings.TrimSuffix(e.declarator(x.Field.Type), " "), read, cString(x.Field.Name), member)
}

// isObject reports whether t is the type of a reference to an object: a class (or
// shape), an interface, an array or a function (a closure).
func isObject(t ir.Type) bool {
	switch t.(type) {
	case *ir.Class, *ir.Interface, *ir.Array, *ir.FuncType, *ir.MapType:
		return true
	}
	return false
}

// newInstance writes `new C(args)`: the arguments, then a zeroed instance, then the constructor.
func (e *emitter) newInstance(n *ir.New) string {
	ready := ""
	if n.Ready != nil {
		if check := e.readyCheck(n.Ready); check != "" {
			ready = check + "; " // before the arguments, as JS evaluates the class first
		}
	}
	args, prelude := e.operands(n.Args)
	instance := e.temporary()
	return fmt.Sprintf("({ %s%s%s%s = pts_object_new(%s); %s(%s); %s; })",
		ready, prelude, e.declarator(n.Class), instance, e.descriptor(n.Class), e.funcNames[n.Class.Constructor], strings.Join(append([]string{instance}, args...), ", "), instance)
}

// fieldRef writes a field of object, a reference of type t: an instance of a class, or
// through an interface, the field that holds the property, found by the runtime and
// cached at this place in the code (D-035).
func (e *emitter) fieldRef(object string, t ir.Type, field *ir.Field) string {
	return e.fieldPlace(object, t, field, "pts_field_address")
}

// fieldPlace is fieldRef with the runtime function that finds a field through an
// interface: pts_field_address, or for a store, pts_field_store_address, which throws for
// a frozen object (D-157).
func (e *emitter) fieldPlace(object string, t ir.Type, field *ir.Field, find string) string {
	class, ok := t.(*ir.Class)
	if !ok {
		site := e.site()
		pointer := strings.TrimSuffix(e.declarator(field.Type), " ") + " *"
		return fmt.Sprintf("(*({ static pts_site %s; (%s)%s(%s, &%s, %s, %s); }))", site, pointer, find, object, site, cString(field.Name), e.siteCheck(field))
	}
	if !isIdentifier(object) {
		object = "(" + object + ")"
	}
	return object + "->" + fieldPath(class, field)
}

// fieldRead writes a read of a field of object, a reference of type t (fieldRef), or of a
// property through an interface that a getter may provide (D-036), or through an
// interface, of an optional property, which may be absent (D-066).
func (e *emitter) fieldRead(object string, t ir.Type, field *ir.Field) string {
	if isOptionalThroughInterface(t, field) {
		site := e.site()
		return fmt.Sprintf("({ static pts_site %s; pts_optional_get(%s, &%s, %s, %s); })", site, object, site, cString(field.Name), e.siteCheck(field))
	}
	if !field.Accessor {
		return e.fieldRef(object, t, field)
	}
	site := e.site()
	return fmt.Sprintf("({ static pts_site %s; PTS_GET(%s, %s, &%s, %s, %s); })", site, strings.TrimSuffix(e.declarator(field.Type), " "), object, site, cString(field.Name), e.siteCheck(field))
}

// hasField writes whether an object has an optional property: whether the field that
// holds it isn't absent, or through an interface, whatever holds it (D-066).
func (e *emitter) hasField(h *ir.HasField) string {
	if isOptionalThroughInterface(h.X.Type(), h.Field) {
		return fmt.Sprintf("pts_object_has(%s, %s)", e.expr(h.X), cString(h.Field.Name))
	}
	return "(" + e.fieldRef(e.expr(h.X), h.X.Type(), h.Field) + " != PTS_VALUE_ABSENT)"
}

// isOptionalThroughInterface reports whether field is an optional property read or
// written through an interface (t), whose objects may hold it in several ways (D-066), or
// one a union's members hold as different types (D-067), read the same way.
func isOptionalThroughInterface(t ir.Type, field *ir.Field) bool {
	_, ok := t.(*ir.Interface)
	return ok && (field.Optional || field.Mixed)
}

// fieldStore writes a store of value to a field of object, a reference of type t
// (fieldRef), or to a property through an interface that a setter may provide (D-036),
// or through an interface, to an optional property (D-066).
func (e *emitter) fieldStore(object string, t ir.Type, field *ir.Field, value string) string {
	if isOptionalThroughInterface(t, field) {
		site, stored := e.site(), e.temporary()
		return fmt.Sprintf("({ static pts_site %s; pts_value %s = %s; pts_optional_set(%s, &%s, %s, %s, %s); %s; })", site, stored, value, object, site, cString(field.Name), e.siteCheck(field), stored, stored)
	}
	_, throughInterface := t.(*ir.Interface)
	if !field.Accessor && !(throughInterface && e.declaredTypes) {
		if e.freezes && !throughInterface && !strings.HasPrefix(field.Name, "#") { // JS leaves a frozen object's #private fields writable
			stored := e.temporary() // the value first: JS evaluates it before the write throws
			writable := fmt.Sprintf("PTS_WRITABLE(%s, %s)", object, cString(field.Name))
			return fmt.Sprintf("({ %s%s = %s; %s = %s; })", e.declarator(field.Type), stored, value, e.fieldRef(writable, t, field), stored)
		}
		return e.fieldPlace(object, t, field, "pts_field_store_address") + " = " + value
	}
	site, macro := e.site(), "PTS_FIELD_SET"
	if field.Accessor {
		macro = "PTS_SET"
	}
	return fmt.Sprintf("({ static pts_site %s; %s(%s, %s, &%s, %s, %s, %s); })", site, macro, strings.TrimSuffix(e.declarator(field.Type), " "), object, site, cString(field.Name), e.siteCheck(field), value)
}

// siteCheck is the C expression of the check that a site reading or writing a property
// through an interface passes for the property's type (D-072): its pts_check in a program
// whose descriptors carry declared types, else NULL.
func (e *emitter) siteCheck(field *ir.Field) string {
	if !e.declaredTypes {
		return "NULL"
	}
	return "&" + e.checks[field.Type]
}

// site names a new cache for a place in the function that finds a member at run time.
func (e *emitter) site() string {
	e.sites++
	return fmt.Sprintf("pts_site%d", e.sites)
}

// setField writes a store to a field; its value is the new value, or the old one if postfix.
func (e *emitter) setField(s *ir.SetField) string {
	if s.Postfix {
		old := e.temporary()
		object := e.expr(s.X) // pure: the lowering binds anything else to a local first
		t := s.X.Type()
		read := e.fieldRead(object, t, s.Field)
		if s.Field.Optional {
			read = "pts_value_present(" + read + ")"
		}
		return fmt.Sprintf("({ %s%s = %s; %s; %s; })", e.declarator(s.Field.Type), old, read, e.fieldStore(object, t, s.Field, e.expr(s.Value)), old)
	}
	return "(" + e.storeField(s) + ")"
}

// storeField writes a store to a field as a statement would: the instance, then the value, in JS's order.
func (e *emitter) storeField(s *ir.SetField) string {
	operands, prelude := e.operands([]ir.Expr{s.X, s.Value})
	return ordered(prelude, e.fieldStore(operands[0], s.X.Type(), s.Field, operands[1]))
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
	function := fmt.Sprintf("((const pts_vtable_%s *)%s)->%s", e.classNames[slot.Class], header, slotName(slot))
	result := function + "(" + strings.Join(args, ", ") + ")"
	if call.Method.Func.Result != slot.Func.Result { // an override's covariant result (fitOverride)
		result = "((" + strings.TrimSuffix(e.declarator(call.Method.Func.Result), " ") + ")" + result + ")"
	}
	return ordered(prelude, result)
}

// interfaceCall writes a call of a method through an interface: the receiver is evaluated
// once, before the arguments, and the function its class has for the method is found
// through a cache at this place in the code (D-036).
func (e *emitter) interfaceCall(call *ir.InterfaceCall) string {
	args, prelude := e.operands(call.Args)
	if !isIdentifier(args[0]) {
		receiver := e.temporary()
		prelude = "pts_object *" + receiver + " = " + args[0] + "; " + prelude
		args[0] = receiver
	}
	params := []string{"pts_object *"}
	for _, param := range call.Method.Params {
		params = append(params, strings.TrimSuffix(e.declarator(param), " "))
	}
	site := e.site()
	find := fmt.Sprintf("pts_method_find(%s, &%s, %s, PTS_METHOD)", args[0], site, cString(call.Method.Name))
	if adapter, ok := e.methodAdapters[call.Method.Name+"\x00"+e.checks[e.canonicalFuncType(call.Method.Params, call.Method.Result)]]; ok {
		find = fmt.Sprintf("pts_method_find_adapted(%s, &%s, %s, %s, (pts_function)%s)", args[0], site, cString(call.Method.Name),
			e.signatureCheck(call.Method.Params, call.Method.Result), adapter.c) // its object may have come through `any` (D-078)
	}
	function := fmt.Sprintf("((%s(*)(%s))({ static pts_site %s; %s; }))", e.declarator(call.Method.Result), strings.Join(params, ", "), site, find)
	return ordered(prelude, function+"("+strings.Join(args, ", ")+")")
}

// instanceCall writes a call of the instance (D-148) that the closure or object X lists
// for the call's signature, or if it lists none, of the erased copy (Otherwise). X and the
// arguments are locals, which both calls read.
func (e *emitter) instanceCall(call *ir.InstanceCall) string {
	x := e.expr(call.X)
	name, params := "NULL", []string{"pts_closure *"}
	if call.Name != "" {
		name, params = cString(call.Name), []string{"pts_object *"}
	}
	args := []string{"(" + params[0] + ")" + x}
	for i, arg := range call.Args {
		params = append(params, strings.TrimSuffix(e.declarator(call.Signature.Params[i]), " "))
		args = append(args, e.expr(arg))
	}
	found := e.temporary()
	return fmt.Sprintf("({ pts_function %s = pts_instance_find(%s, %s, %s); %s != NULL ? ((%s(*)(%s))%s)(%s) : %s; })", found, x, name,
		e.signatureCheck(call.Signature.Params, call.Signature.Result), found, e.declarator(call.Signature.Result), strings.Join(params, ", "), found,
		strings.Join(args, ", "), e.expr(call.Otherwise))
}

// instanceTable writes the table of the instances (D-148) that a closure's or class's
// descriptor (owner) lists, with an entry of each one's signature: it takes the closure
// or the object, then the call's arguments, and calls the instance with those it takes.
// It returns the descriptor's members for the table, or nil if there is none.
func (e *emitter) instanceTable(owner string, instances []*ir.Instance) []string {
	if len(instances) == 0 {
		return nil
	}
	entries := make([]string, len(instances))
	for i, inst := range instances {
		entries[i] = "NULL"
		if inst.Func == nil {
			continue
		}
		entries[i] = fmt.Sprintf("pts_instance_%s_%d", owner, i+1)
		params, args, takes := []string{"pts_closure *pts_env"}, []string(nil), inst.Func.Params
		if inst.Name != "" {
			params = []string{"pts_object *self"}
			args = []string{"(" + strings.TrimSuffix(e.declarator(takes[0].Type), " ") + ")self"}
			takes = takes[1:]
		}
		for j, param := range inst.Signature.Params {
			arg := fmt.Sprintf("a%d", j+1)
			params = append(params, e.declarator(param)+arg)
			if j < len(takes) {
				args = append(args, arg)
			}
		}
		call := e.funcNames[inst.Func] + "(" + strings.Join(args, ", ") + ")"
		switch {
		case inst.Signature.Result == ir.Void:
		case inst.Func.Result != inst.Signature.Result: // a covariant result, a pointer to a subclass's instance
			call = "return (" + strings.TrimSuffix(e.declarator(inst.Signature.Result), " ") + ")" + call
		default:
			call = "return " + call
		}
		e.line(0, "static %s%s(%s) { %s; }", e.declarator(inst.Signature.Result), entries[i], strings.Join(params, ", "), call)
	}
	table := "pts_instances_" + owner
	e.line(0, "static const pts_instance %s[] = {", table)
	for i, inst := range instances {
		name := "NULL"
		if inst.Name != "" {
			name = cString(inst.Name)
		}
		e.line(1, "{%s, %s, (pts_function)%s},", name, e.signatureCheck(inst.Signature.Params, inst.Signature.Result), entries[i])
	}
	e.line(0, "};")
	return []string{fmt.Sprintf(".instance_count = %d", len(instances)), ".instances = " + table}
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
