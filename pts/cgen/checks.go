package cgen

import (
	"fmt"
	"strings"

	"github.com/microsoft/typescript-go/pts/ir"
)

// Runtime descriptions of types (pts_check, D-071): for each type that a dynamic value is
// converted to and that valueTest can't test, and for the types of its properties and
// elements, a static pts_check that pts_value_convert reads. A program that converts
// dynamic values to such types or writes through `any` also describes the declared type
// of every field, accessor and array element and hash-map value in its descriptors
// (D-072), which decide what a written value must be and whether an object already is
// one of an object type.

// collectChecks names the checks of the types dynamic values are converted to, and
// decides whether descriptors carry declared types.
func (e *emitter) collectChecks(program *ir.Program) {
	var targets []ir.Type
	for _, f := range program.Funcs {
		e.usesPromises = e.usesPromises || f.Async || f.AsyncGenerator
		ir.Walk(f.Body.Stmts, func(ir.Stmt) {}, func(x ir.Expr) {
			switch x := x.(type) {
			case *ir.FromUnion:
				if x.X.Type() == ir.Value && !hasValueTest(x.To) {
					targets = append(targets, x.To)
				}
			case *ir.ClassCast:
				if x.FromResult { // an object of another layout converted (classCast)
					targets = append(targets, x.To)
				}
			case *ir.FieldRef:
				if isResultReceiver(x.X.Type()) && !hasValueTest(x.Field.Type) { // a property found by name (resultRead)
					targets = append(targets, x.Field.Type)
				}
			case *ir.SetField:
				if isResultReceiver(x.X.Type()) { // a write through any's path (resultStore)
					e.declaredTypes = true
				}
			case *ir.FamilySetField:
				for _, class := range *x.Family {
					if field := ownField(class, x.Name); field != nil && !hasValueTest(field.Type) {
						targets = append(targets, field.Type)
					}
				}
			case *ir.FamilyCall:
				e.declaredTypes = true // the methods' dynamic entries convert the arguments
				e.familyCalls = append(e.familyCalls, x)
			case *ir.ArrayCall:
				if result := flattenedInto(x); result != nil && describesElements(result) {
					e.declaredTypes = true // flat checks the leaves it stores against the result's element type (D-180)
				}
			case *ir.Call:
				if x.Func.Settles { // the loop settles its promise (D-118)
					e.usesLoop = true
					e.usesPromises = true
				}
				if x.Func.Listens { // the loop serves its connections (D-136)
					e.usesLoop = true
				}
				if x.Func.Extern == "pts_parallel_push_flat" { // pts:parallel's flatMap stores leaves as flat does: checked by declared types (D-180)
					e.declaredTypes = true
				}
			case *ir.CallBuiltin:
				switch x.Builtin {
				case ir.DynamicSet, ir.DynamicHas: // `k in o` finds methods in method tables too
					e.declaredTypes = true
				case ir.DynamicCall, ir.DynamicCallMethod, ir.DynamicCallFound, ir.JSONStringify: // JSON.stringify calls toJSON methods and replacers (D-080)
					e.declaredTypes = true
				case ir.PromiseNew, ir.PromiseThen, ir.PromiseFinally: // the runtime calls executors and handlers (D-112)
					e.declaredTypes = true
					e.usesPromises = true
				case ir.AsyncIteratorOpen, ir.AsyncIteratorResume: // it may call the program's async iterators (D-115)
					e.declaredTypes = true
					e.usesPromises = true
				case ir.TimerSet, ir.ImmediateSet: // the loop calls the callbacks (D-114)
					e.declaredTypes = true
					e.usesLoop = true
				case ir.QueueMicrotask:
					e.declaredTypes = true
					e.usesPromises = true
				case ir.Parallel: // the loops run the tasks and settle their promises (D-121)
					e.usesLoop = true
					e.usesPromises = true
				case ir.PromiseResolve, ir.PromiseReject, ir.PromiseAll, ir.PromiseAllSettled, ir.PromiseAny, ir.PromiseRace, ir.PromiseWithResolvers:
					e.usesPromises = true
				case ir.ValueFreeze:
					e.freezes = true
				}
			}
		})
	}
	e.declaredTypes = e.declaredTypes || len(targets) > 0 || implementsIteration(program) || definesConversions(program) || providesWithFewerParams(program)
	for _, t := range targets {
		e.check(t)
	}
	e.collectInstanceSignatures(program)
}

// collectInstanceSignatures names the checks of the signatures that instance calls look
// instances up by and instance tables list them by (D-148).
func (e *emitter) collectInstanceSignatures(program *ir.Program) {
	signature := func(ft *ir.FuncType, key string) { e.check(e.keyedFuncType(ft.Params, ft.Result, key)) }
	for _, f := range program.Funcs {
		for _, inst := range f.Instances {
			signature(inst.Signature, inst.Key)
		}
		ir.Walk(f.Body.Stmts, func(ir.Stmt) {}, func(x ir.Expr) {
			if call, ok := x.(*ir.InstanceCall); ok {
				signature(call.Signature, call.Key)
			}
		})
	}
	for _, class := range program.Classes {
		for _, inst := range class.Instances {
			signature(inst.Signature, inst.Key)
		}
	}
}

// collectDeclaredTypes names, if descriptors carry declared types, the checks of every
// class's and shape's fields and accessors, of maps' keys and values and of the
// properties read and written through interfaces, and the descriptors of the arrays the
// program makes whose element kind doesn't tell their element type.
func (e *emitter) collectDeclaredTypes(program *ir.Program) {
	if !e.declaredTypes {
		return
	}
	for _, class := range program.Classes {
		if class.Builtin {
			continue
		}
		for _, field := range class.AllFields() {
			e.check(field.Type)
			if field.Kind != ir.Plain { // a shape's accessor's closure
				e.check(shapeAccessorType(field))
			}
		}
		for _, method := range e.interfaceMembers[class] {
			if method.Kind != ir.Plain {
				e.check(accessorType(method.Kind, method.Func.Params, method.Func.Result))
			}
		}
	}
	for _, f := range program.Funcs {
		ir.Walk(f.Body.Stmts, func(ir.Stmt) {}, func(x ir.Expr) {
			switch x := x.(type) {
			case *ir.NewArray:
				e.describeArray(x.Array)
			case *ir.ArrayCall:
				if result := flattenedInto(x); result != nil {
					e.describeArray(result)
				}
			case *ir.FieldRef:
				e.checkSite(x.X.Type(), x.Field)
			case *ir.SetField:
				e.checkSite(x.X.Type(), x.Field)
			}
		})
	}
	for i := 0; i < len(e.mapOrder); i++ { // checks may add map types
		mt := e.mapOrder[i]
		e.check(mt.Key)
		if mt.Value != nil {
			e.check(mt.Value)
		}
	}
}

// checkSite names the check of a property that a site reads or writes through an
// interface (t), which the site passes (siteCheck).
func (e *emitter) checkSite(t ir.Type, field *ir.Field) {
	if _, ok := t.(*ir.Interface); ok {
		e.check(field.Type)
	}
}

// describeArray gives arrays of type t a descriptor of the program's that carries their
// element type, if descriptors carry declared types and the element kind doesn't tell it:
// arrays of objects and of unions.
func (e *emitter) describeArray(t *ir.Array) {
	if _, ok := e.arrayDescriptors[t]; ok || !e.declaredTypes || !describesElements(t) {
		return
	}
	e.arrayDescriptors[t] = fmt.Sprintf("pts_type_array_%d", len(e.arrayOrder)+1)
	e.arrayOrder = append(e.arrayOrder, t)
	e.check(t.Elem)
}

// describesElements reports whether arrays of type t need a descriptor of the program's to
// tell their element type: arrays of objects and of unions, whose element kind doesn't.
func describesElements(t *ir.Array) bool {
	kind := fieldKind(t.Elem)
	return t.Elem != ir.Value && (kind == "PTS_FIELD_OBJECT" || kind == "PTS_FIELD_VALUE")
}

// flattenedInto is the array an operation flattens arrays into, flat's result or the array
// flatMap's step pushes onto, or nil for another operation.
func flattenedInto(call *ir.ArrayCall) *ir.Array {
	switch call.Op {
	case ir.ArrayFlat:
		return call.To
	case ir.ArrayPushFlat:
		return call.Args[0].Type().(*ir.Array)
	}
	return nil
}

// arrayDescriptor is the C expression of the descriptor of arrays of type t: the
// program's (describeArray), or the runtime's for their element kind.
func (e *emitter) arrayDescriptor(t *ir.Array) string {
	if name, ok := e.arrayDescriptors[t]; ok {
		return "&" + name
	}
	return "&pts_array_types[" + fieldKind(t.Elem) + "]"
}

// arrayDescriptorDefinitions writes the program's array descriptors.
func (e *emitter) arrayDescriptorDefinitions() {
	for _, t := range e.arrayOrder {
		e.line(0, "static const pts_type %s = {", e.arrayDescriptors[t])
		e.line(1, ".kind = PTS_KIND_ARRAY,")
		e.line(1, ".gc_descr = PTS_GC_POINTER_WORDS(PTS_GC_WORD(pts_array, buffer)),")
		e.line(1, ".name = \"Array\",")
		e.line(1, ".size = sizeof(pts_array),")
		e.line(1, ".element = %s,", fieldKind(t.Elem))
		e.line(1, ".check = &%s,", e.checks[t.Elem])
		e.line(0, "};")
	}
	if len(e.arrayOrder) > 0 {
		e.line(0, "")
	}
}

// declaredType is the C initializer member of a descriptor's entry that gives the
// declared type t of a field or accessor, or "" if descriptors don't carry them.
func (e *emitter) declaredType(t ir.Type) string {
	if !e.declaredTypes {
		return ""
	}
	return ", &" + e.checks[t]
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
	if ft, ok := t.(*ir.FuncType); ok { // one check per signature, which closures' descriptors point to
		if canonical := e.canonicalFuncType(ft.Params, ft.Result); canonical != ft && !e.keyedFuncTypes[ft] {
			e.checks[t] = e.check(canonical)
			return e.checks[t]
		}
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
		e.describeArray(t)
	case *ir.MapType:
		e.check(t.Key)
		if t.Value != nil {
			e.check(t.Value)
		}
		e.nameMapDescriptor(t) // a copy may make one
	case *ir.Interface:
		for _, field := range t.Fields {
			e.check(field.Type)
		}
		for _, method := range t.Methods {
			e.check(e.canonicalFuncType(method.Params, method.Result))
		}
	case *ir.FuncType: // its adapter converts the result
		if t.Result != ir.Void && !hasValueTest(t.Result) {
			e.check(t.Result)
		}
	case *ir.Class:
		for _, field := range t.Fields {
			e.check(field.Type)
		}
	}
	return name
}

// checkDeclarations declares the checks, which types and descriptors refer to before
// checkDefinitions defines them.
func (e *emitter) checkDeclarations() {
	for _, t := range e.checkOrder {
		e.line(0, "static const pts_check %s;", e.checks[t])
	}
	if len(e.checkOrder) > 0 {
		e.line(0, "")
	}
}

// checkDefinitions writes the checks.
func (e *emitter) checkDefinitions() {
	for _, t := range e.checkOrder {
		e.checkDefinition(t)
	}
	if len(e.checkOrder) > 0 {
		e.line(0, "")
	}
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
		members = append(members, ".element = &"+e.checks[t.Elem], ".type = "+e.arrayDescriptor(t))
	case *ir.MapType:
		members[0] = ".kind = PTS_CHECK_" + mapKind(t)
		members = append(members, ".type = &"+e.mapDescriptors[t])
		if t.Value != nil {
			members = append(members, ".element = &"+e.checks[t.Value])
		}
	case *ir.FuncType:
		members[0] = ".kind = PTS_CHECK_FUNCTION"
		members = append(members, ".adapt = "+adapterMaker(name))
	case *ir.Interface:
		members[0] = ".kind = PTS_CHECK_OBJECT"
		members = append(members, e.checkProperties(name, t.Fields, t.Copy, t.Copy != nil)...)
		if len(t.Methods) > 0 {
			e.line(0, "static const pts_check_method %s_methods[] = {", name)
			for _, method := range t.Methods {
				e.line(1, "{%s, &%s},", cString(method.Name), e.checks[e.canonicalFuncType(method.Params, method.Result)])
			}
			e.line(0, "};")
			members = append(members, fmt.Sprintf(".method_count = %d", len(t.Methods)), ".methods = "+name+"_methods")
		}
		if t.Copy != nil {
			members = append(members, ".type = &pts_type_"+shapeDescriptorName(e.classNames[t.Copy], t.CopyOrder))
		}
	case *ir.Class:
		if !t.Shape {
			members[0] = ".kind = PTS_CHECK_INSTANCE"
			members[2] = ".name = " + cString(typeName(t)) // Expected Box<number>, got Box<any> (D-147)
			members = append(members, ".type = "+e.descriptor(t))
			break
		}
		members[0] = ".kind = PTS_CHECK_OBJECT"
		if t.Tuple {
			members[0] = ".kind = PTS_CHECK_TUPLE"
		}
		members = append(members, e.checkProperties(name, t.Fields, t, true)...)
		members = append(members, ".type = &pts_type_"+shapeDescriptorName(e.classNames[t], 0))
		if t.Result {
			members = append(members, ".result = true") // any of the program's objects as it is (D-178)
		}
	default:
		panic("cgen: no check for " + t.String())
	}
	e.line(0, "static const pts_check %s = {%s};", name, strings.Join(members, ", "))
	return name
}

// canonicalFuncType is the program's one function type with these parameter and result
// types, by their identities: closures' and methods' signatures are compared by identity
// at run time (D-075), and the lowering's function types don't cover methods'.
func (e *emitter) canonicalFuncType(params []ir.Type, result ir.Type) *ir.FuncType {
	return e.keyedFuncType(params, result, "")
}

// keyedFuncType is canonicalFuncType's function type for an instance table's key
// (ir.Instance.Key): with a key, another function type of the same types, whose check is
// another object, which the runtime tells apart by its address (D-190).
func (e *emitter) keyedFuncType(params []ir.Type, result ir.Type, instanceKey string) *ir.FuncType {
	var key strings.Builder
	fmt.Fprintf(&key, "%p", result)
	for _, param := range params {
		fmt.Fprintf(&key, ",%p", param)
	}
	if instanceKey != "" {
		key.WriteString("\x00" + instanceKey)
	}
	if ft, ok := e.funcTypes[key.String()]; ok {
		return ft
	}
	ft := &ir.FuncType{Params: params, Result: result}
	e.funcTypes[key.String()] = ft
	if instanceKey != "" {
		e.keyedFuncTypes[ft] = true
	}
	return ft
}

// signatureCheck is the C expression of the check of the function type with these
// parameter and result types, which a closure's descriptor or a method table entry points to.
func (e *emitter) signatureCheck(params []ir.Type, result ir.Type) string {
	return "&" + e.checks[e.canonicalFuncType(params, result)]
}

// instanceSignatureCheck is the C expression of the check that instance tables and the
// calls that search them use for a signature and key (ir.Instance).
func (e *emitter) instanceSignatureCheck(signature *ir.FuncType, key string) string {
	return "&" + e.checks[e.keyedFuncType(signature.Params, signature.Result, key)]
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

// providesWithFewerParams reports whether a class has a method of an interface's name
// that takes fewer parameters than the interface's: calls through the interface reach it
// through the method's adapter, which needs the declared types.
func providesWithFewerParams(program *ir.Program) bool {
	for _, iface := range program.Interfaces {
		for _, method := range iface.Methods {
			for _, class := range program.Classes {
				if own := class.Method(method.Name, ir.Plain); own != nil && len(own.Func.Params) < len(method.Params)+1 {
					return true
				}
			}
		}
	}
	return false
}
