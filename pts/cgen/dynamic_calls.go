package cgen

import (
	"fmt"
	"strings"

	"github.com/microsoft/typescript-go/pts/ir"
)

// Calls through `any` (D-074) and function types from `any` (D-075). In a program that
// uses dynamic values beyond primitives, every function made a value of has a dynamic
// entry, pts_dynamic_<f>, which its closures' descriptor holds (.call) with its signature
// (the check of its function type), and every method in a method table has one (.dynamic)
// and a signature. A dynamic entry converts each argument to its parameter's type with a
// check (a missing one is undefined), calls the function and returns its result as a
// dynamic value. Each function type that dynamic values are converted to has an adapter:
// a closure of the type that calls a closure of another type through its dynamic entry.

// collectDynamicEntries names the checks that the dynamic entries' conversions need, and
// the functions' and methods' signatures.
func (e *emitter) collectDynamicEntries(program *ir.Program) {
	if !e.declaredTypes {
		return
	}
	for _, f := range e.closureOrder {
		e.checkParams(paramTypes(f.Params))
		e.check(e.canonicalFuncType(paramTypes(f.Params), f.Result))
	}
	for _, class := range program.Classes {
		for _, method := range e.interfaceMembers[class] {
			if method.Kind == ir.Plain {
				e.checkParams(paramTypes(method.Func.Params[1:]))
				e.check(e.canonicalFuncType(paramTypes(method.Func.Params[1:]), method.Func.Result))
			}
		}
		if class.Shape {
			for _, field := range methodFields(class) {
				if ft := field.Type.(*ir.FuncType); field.Kind == ir.Plain {
					e.checkParams(ft.Params[1:])
					e.check(e.canonicalFuncType(ft.Params[1:], ft.Result))
				}
			}
		}
	}
}

// checkParams names the checks of the parameter types that a dynamic value is converted
// to with one, and the descriptor of a rest parameter's array.
func (e *emitter) checkParams(params []ir.Type) {
	for _, t := range params {
		if rest, ok := t.(*ir.Rest); ok {
			e.describeArray(rest.Array)
			t = rest.Array.Elem
		}
		if t != ir.Given && !hasValueTest(t) {
			e.check(t)
		}
	}
}

func paramTypes(params []*ir.Local) []ir.Type {
	types := make([]ir.Type, len(params))
	for i, param := range params {
		types[i] = param.Type
	}
	return types
}

// dynamicEntryDeclarations declares the dynamic entries, which descriptors and method
// tables refer to before dynamicEntryDefinitions defines them, after the checks and
// descriptors their conversions use.
func (e *emitter) dynamicEntryDeclarations(program *ir.Program) {
	e.dynamicEntries(program, func(name string, self string, _ func()) {
		e.line(0, "static pts_value %s(%s, uint32_t argc, const pts_value *argv);", name, self)
	})
	for _, ft := range e.adapterFunctionTypes() {
		e.line(0, "static pts_closure *%s(pts_closure *function);", adapterMaker(e.checks[ft]))
	}
	if e.declaredTypes {
		e.line(0, "")
	}
}

// dynamicEntryDefinitions writes the dynamic entries.
func (e *emitter) dynamicEntryDefinitions(program *ir.Program) {
	e.dynamicEntries(program, func(name string, self string, body func()) {
		e.line(0, "static pts_value %s(%s, uint32_t argc, const pts_value *argv) {", name, self)
		e.line(1, "(void)argc, (void)argv;")
		body()
		e.line(0, "}")
	})
	for _, ft := range e.adapterFunctionTypes() {
		e.adapterDefinition(ft)
	}
	if e.declaredTypes {
		e.line(0, "")
	}
}

// dynamicEntries calls write with the name, the `self` parameter and a writer of the body
// of each dynamic entry: of each function made a value of, which calls its closure's code;
// of each class's method in a method table, which calls its entry point; and of each
// shape's method, which calls the closure the object's field holds.
func (e *emitter) dynamicEntries(program *ir.Program, write func(name string, self string, body func())) {
	if !e.declaredTypes {
		return
	}

	for _, f := range e.closureOrder {
		write("pts_dynamic_"+e.closureNames[f], "pts_closure *self", func() {
			params := paramTypes(f.Params)
			args := append([]string{"self"}, e.dynamicArguments(params)...)
			code := "((" + e.functionPointer(&ir.FuncType{Params: params, Result: f.Result}) + ")self->code)"
			e.returnDynamic(code+"("+strings.Join(args, ", ")+")", f.Result)
		})
	}
	for _, method := range e.entryOrder {
		if method.Kind != ir.Plain {
			continue
		}
		f := method.Func
		write(dynamicEntryName(e.entryNames[f]), "pts_object *self", func() {
			args := append([]string{"self"}, e.dynamicArguments(paramTypes(f.Params[1:]))...)
			e.returnDynamic(e.entryNames[f]+"("+strings.Join(args, ", ")+")", f.Result)
		})
	}
	for _, shape := range program.Classes {
		if !shape.Shape {
			continue
		}
		name := e.classNames[shape]
		for _, field := range methodFields(shape) {
			if field.Kind != ir.Plain {
				continue
			}
			ft := field.Type.(*ir.FuncType)
			write(dynamicEntryName(shapeEntryName(name, field)), "pts_object *self", func() {
				e.line(1, "pts_closure *method = ((pts_class_%s *)self)->%s;", name, fieldIdentifier(field.Name))
				args := append([]string{"method", "(pts_class_" + name + " *)self"}, e.dynamicArguments(ft.Params[1:])...)
				e.returnDynamic("(("+e.functionPointer(ft)+")method->code)("+strings.Join(args, ", ")+")", ft.Result)
			})
		}
	}
}

// adapterFunctionTypes are the function types that have checks, and so adapters.
func (e *emitter) adapterFunctionTypes() []*ir.FuncType {
	var types []*ir.FuncType
	for _, t := range e.checkOrder {
		if ft, ok := t.(*ir.FuncType); ok {
			types = append(types, ft)
		}
	}
	return types
}

// adapterDefinition writes a function type's adapters (D-075): their code, which boxes
// its arguments (one not given, D-055, is undefined; a rest array's elements are spread),
// calls the adapted closure through its dynamic entry and converts the result to the
// type's; their descriptor; and the maker the type's check points to.
func (e *emitter) adapterDefinition(ft *ir.FuncType) {
	suffix := strings.TrimPrefix(e.checks[ft], "pts_check_")
	used := map[string]bool{"self": true, "argv": true, "argc": true, "result": true}
	params := []string{"pts_closure *self"}
	var boxed []string
	var rest string
	for i, t := range ft.Params {
		name := unique(used, fmt.Sprintf("arg%d", i+1))
		params = append(params, e.declarator(t)+name)
		switch {
		case t == ir.Given:
			boxed[len(boxed)-1] = name + " ? " + boxed[len(boxed)-1] + " : PTS_VALUE_UNDEFINED"
		case isRest(t):
			rest = name
		default:
			boxed = append(boxed, boxAs(name, t))
		}
	}
	e.line(0, "static %spts_adapter_%s(%s) {", e.declarator(ft.Result), suffix, strings.Join(params, ", "))
	call := fmt.Sprintf("pts_dynamic_call_closure(((pts_adapter *)self)->function, %d, %s)", len(boxed), "argv")
	switch {
	case rest != "":
		e.line(1, "size_t argc = %d + pts_array_length_of(%s);", len(boxed), rest)
		e.line(1, "pts_value argv[argc + 1];")
		for i, value := range boxed {
			e.line(1, "argv[%d] = %s;", i, value)
		}
		e.line(1, "for (size_t i = %d; i < argc; i++) {", len(boxed))
		e.line(2, "argv[i] = pts_array_element(%s, (double)(i - %d));", rest, len(boxed))
		e.line(1, "}")
		call = "pts_dynamic_call_closure(((pts_adapter *)self)->function, (uint32_t)argc, argv)"
	case len(boxed) > 0:
		e.line(1, "pts_value argv[] = {%s};", strings.Join(boxed, ", "))
	default:
		call = "pts_dynamic_call_closure(((pts_adapter *)self)->function, 0, NULL)"
	}
	if ft.Result == ir.Void {
		e.line(1, "%s;", call)
	} else {
		e.line(1, "pts_value result = %s;", call)
		e.line(1, "return %s;", e.fromValue("result", ft.Result))
	}
	e.line(0, "}")
	e.line(0, "static const pts_type pts_type_adapter_%s = {", suffix)
	e.line(1, ".kind = PTS_KIND_FUNCTION,")
	e.line(1, ".gc_descr = PTS_GC_POINTER_WORDS(PTS_GC_WORD(pts_adapter, function)),")
	e.line(1, ".name = NULL, /* an adapter: console.log shows the function it adapts */")
	e.line(1, ".size = sizeof(pts_adapter),")
	e.line(1, ".call = pts_adapter_call,")
	e.line(1, ".signature = &%s,", e.checks[ft])
	e.line(0, "};")
	e.line(0, "static pts_closure *%s(pts_closure *function) {", adapterMaker(e.checks[ft]))
	e.line(1, "pts_adapter *adapter = pts_object_new(&pts_type_adapter_%s);", suffix)
	e.line(1, "adapter->closure.code = (pts_function)pts_adapter_%s;", suffix)
	e.line(1, "adapter->function = function;")
	e.line(1, "return &adapter->closure;")
	e.line(0, "}")
}

func isRest(t ir.Type) bool {
	_, ok := t.(*ir.Rest)
	return ok
}

// adapterMaker is the name of the function that makes adapters for the function type whose check is check.
func adapterMaker(check string) string {
	return "pts_adapt_" + strings.TrimPrefix(check, "pts_check_")
}

// dynamicEntryName is the name of the dynamic entry of the method whose entry point is entry.
func dynamicEntryName(entry string) string {
	return "pts_dynamic_" + strings.TrimPrefix(entry, "pts_")
}

// dynamicMethodMembers are the initializer members of a method table entry that give its
// dynamic entry and its signature (its function type after `this`), or "" if the program
// doesn't use dynamic values beyond primitives.
func (e *emitter) dynamicMethodMembers(entry string, params []ir.Type, result ir.Type) string {
	if !e.declaredTypes {
		return ""
	}
	return ", .dynamic = " + dynamicEntryName(entry) + ", .signature = " + e.signatureCheck(params, result)
}

// dynamicArguments writes the conversion of the dynamic arguments (argv, argc of them) to
// params, in order, into C locals, and returns the C arguments for params. A parameter
// followed by its Given flag (D-055) is given if its argument is there and not undefined;
// a rest parameter takes the arguments left, each converted to its element type.
func (e *emitter) dynamicArguments(params []ir.Type) []string {
	var args []string
	index := 0
	for i := 0; i < len(params); i++ {
		t := params[i]
		name := fmt.Sprintf("pts_arg%d", len(args)+1)
		if rest, ok := t.(*ir.Rest); ok {
			e.line(1, "pts_array *%s = pts_array_of(%s, NULL, 0);", name, e.arrayDescriptor(rest.Array))
			e.line(1, "for (uint32_t i = %d; i < argc; i++) {", index)
			e.line(2, "%selement = %s;", e.declarator(rest.Array.Elem), e.fromValue("argv[i]", rest.Array.Elem))
			e.line(2, "pts_array_push(%s, &element, 1);", name)
			e.line(1, "}")
			args = append(args, name)
			continue
		}
		if i+1 < len(params) && params[i+1] == ir.Given {
			given := name + "_given"
			e.line(1, "bool %s = argc > %d && argv[%d] != PTS_VALUE_UNDEFINED;", given, index, index)
			e.line(1, "%s%s = %s ? %s : (%s){0};", e.declarator(t), name, given, e.fromValue(fmt.Sprintf("argv[%d]", index), t), strings.TrimSuffix(e.declarator(t), " "))
			args = append(args, name, given)
			i++
		} else {
			e.line(1, "%s%s = %s;", e.declarator(t), name, e.fromValue(fmt.Sprintf("(argc > %d ? argv[%d] : PTS_VALUE_UNDEFINED)", index, index), t))
			args = append(args, name)
		}
		index++
	}
	return args
}

// returnDynamic writes the return of call's result, of type result, as a dynamic value.
func (e *emitter) returnDynamic(call string, result ir.Type) {
	if result == ir.Void {
		e.line(1, "%s;", call)
		e.line(1, "return PTS_VALUE_UNDEFINED;")
		return
	}
	e.line(1, "return %s;", boxAs(call, result))
}

// dynamicCallArguments is the argument count and array of a dynamic call's arguments.
func dynamicCallArguments(args []string) string {
	if len(args) == 0 {
		return "0, NULL"
	}
	return fmt.Sprintf("%d, (pts_value[]){%s}", len(args), strings.Join(args, ", "))
}
