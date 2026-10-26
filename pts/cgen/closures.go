package cgen

import (
	"fmt"
	"slices"
	"strings"

	"github.com/microsoft/typescript-go/pts/ir"
)

// Function values are closures (D-041): a pts_closure (the descriptor, then the code
// pointer) followed by the values of the variables the function captures, or for a
// variable in a cell, the cell. A function of the program used as a value has one
// descriptor, pts_type_closure_<f>, and either a struct for its closures,
// pts_closure_<f>, which pts_new_closure_<f> allocates, or if it captures nothing, one
// static closure, pts_static_<f>. A closure's function takes the closure first, as
// pts_env; a function that takes none is reached through a wrapper, pts_code_<f>.

// collectClosures finds the functions the program makes values of and names their C
// structs, descriptors and capture fields.
func (e *emitter) collectClosures(program *ir.Program) {
	named := func(f *ir.Func) {
		if e.closureNames[f] == "" {
			e.closureNames[f] = strings.TrimPrefix(e.funcNames[f], "pts_fn_")
			e.closureOrder = append(e.closureOrder, f)
		}
	}
	for _, f := range program.Funcs {
		ir.Walk(f.Body.Stmts, func(ir.Stmt) {}, func(x ir.Expr) {
			if closure, ok := x.(*ir.MakeClosure); ok {
				named(closure.Func)
			}
		})
	}
	for _, f := range program.Funcs {
		if f.Shares != nil {
			named(f.Shares) // its struct, which the instance reads, though nothing made a closure of it
		}
	}
	for _, f := range e.closureOrder {
		used := map[string]bool{"closure": true}
		for _, local := range f.Captures {
			e.captureFields[local] = append(e.captureFields[local], captureField{f, unique(used, cIdentifier(local.Name))})
		}
	}
}

// captureField is the member of a function's closure struct that holds a captured local.
type captureField struct {
	f    *ir.Func
	name string
}

// captureField is the member of f's closure struct that holds local.
func (e *emitter) captureField(f *ir.Func, local *ir.Local) string {
	for _, field := range e.captureFields[local] {
		if field.f == f {
			return field.name
		}
	}
	panic("cgen: " + f.Name + " doesn't capture " + local.Name)
}

// closureStructs writes the struct of each function's closures that capture variables.
func (e *emitter) closureStructs() {
	for _, f := range e.closureOrder {
		if len(f.Captures) == 0 {
			continue
		}
		e.line(0, "typedef struct pts_closure_%s {", e.closureNames[f])
		e.line(1, "pts_closure closure;")
		for _, local := range f.Captures {
			e.line(1, "%s%s;", e.capturedDeclarator(local), e.captureField(f, local))
		}
		e.line(0, "} pts_closure_%s;", e.closureNames[f])
		e.line(0, "")
	}
}

// capturedDeclarator is the C declarator of a captured local's value in a closure: its
// own type, or a pointer to its cell.
func (e *emitter) capturedDeclarator(local *ir.Local) string {
	if local.Cell {
		return e.declarator(local.Type) + "*"
	}
	return e.declarator(local.Type)
}

// closureDefinitions writes, for each function made a value of, its descriptor, and its
// static closure (with the wrapper a function without a closure parameter needs) or the
// function that makes its closures.
func (e *emitter) closureDefinitions() {
	for _, f := range e.closureOrder {
		name := e.closureNames[f]
		size, layout := "sizeof(pts_closure)", "PTS_GC_NO_POINTERS"
		if len(f.Captures) > 0 {
			size, layout = "sizeof(pts_closure_"+name+")", e.closureLayout(f)
		}
		jsName := cString(f.JSName)
		switch {
		case f.Adapts:
			jsName = "NULL /* an adapter: console.log shows the function it adapts */"
		case f.Binds:
			jsName = "NULL /* bound: console.log shows \"bound \" and the name of the function it binds */"
		}
		instances := e.instanceTable("closure_"+name, f.Instances)
		e.line(0, "static const pts_type pts_type_closure_%s = {", name)
		e.line(1, ".kind = PTS_KIND_FUNCTION,")
		e.line(1, ".gc_descr = %s,", layout)
		e.line(1, ".name = %s,", jsName)
		if f.Binds {
			e.line(1, ".parent = &pts_bound_function,")
		}
		switch {
		case f.AsyncGenerator:
			e.line(1, ".parent = &pts_async_generator_function,")
		case f.Async:
			e.line(1, ".parent = &pts_async_function,")
		case f.Generator:
			e.line(1, ".parent = &pts_generator_function,")
		}
		e.line(1, ".size = %s,", size)
		if f.Length > 0 && !f.Adapts && !f.Binds {
			e.line(1, ".length = %d,", f.Length)
		}
		if e.declaredTypes {
			e.line(1, ".call = %s,", closureDynamicEntry(name))
			e.line(1, ".signature = %s,", e.signatureCheck(paramTypes(f.Params), f.Result))
		}
		for _, member := range instances {
			e.line(1, "%s,", member)
		}
		e.line(0, "};")
		code := e.funcNames[f]
		if !f.Closure {
			code = "pts_code_" + name
			e.codeWrapper(f, code)
		}
		if len(f.Captures) == 0 {
			e.line(0, "static pts_closure pts_static_%s = {&pts_type_closure_%s, (pts_function)%s};", name, name, code)
		} else {
			e.closureConstructor(f)
		}
		e.line(0, "")
	}
}

// closureLayout is the GC layout of a function's closures: the captured values that may
// be pointers, and every cell.
func (e *emitter) closureLayout(f *ir.Func) string {
	var words []string
	for _, local := range f.Captures {
		if local.Cell || holdsPointer(local.Type) {
			words = append(words, fmt.Sprintf("PTS_GC_WORD(pts_closure_%s, %s)", e.closureNames[f], e.captureField(f, local)))
		}
	}
	switch {
	case len(words) == 0:
		return "PTS_GC_NO_POINTERS"
	case len(f.Captures)+2 > maxBitmapWords:
		return "PTS_GC_ALL_WORDS(sizeof(pts_closure_" + e.closureNames[f] + "))"
	}
	return "PTS_GC_POINTER_WORDS(" + strings.Join(words, " | ") + ")"
}

// codeWrapper writes the function a closure of f, which takes no closure, runs.
func (e *emitter) codeWrapper(f *ir.Func, name string) {
	used := map[string]bool{"pts_env": true}
	params := []string{"pts_closure *pts_env"}
	args := make([]string, len(f.Params))
	for i, param := range f.Params {
		args[i] = unique(used, cIdentifier(param.Name))
		params = append(params, e.declarator(param.Type)+args[i])
	}
	call := e.funcNames[f] + "(" + strings.Join(args, ", ") + ");"
	if f.Result != ir.Void {
		call = "return " + call
	}
	e.line(0, "static %s%s(%s) { %s }", e.declarator(f.Result), name, strings.Join(params, ", "), call)
}

// closureConstructor writes pts_new_closure_<f>, which makes a closure of f holding the
// captured values it is passed.
func (e *emitter) closureConstructor(f *ir.Func) {
	name := e.closureNames[f]
	params := make([]string, len(f.Captures))
	for i, local := range f.Captures {
		params[i] = e.capturedDeclarator(local) + e.captureField(f, local)
	}
	e.line(0, "static pts_closure *pts_new_closure_%s(%s) {", name, strings.Join(params, ", "))
	e.line(1, "pts_closure_%[1]s *closure = pts_object_new(&pts_type_closure_%[1]s);", name)
	e.line(1, "closure->closure.code = (pts_function)%s;", e.funcNames[f])
	for _, local := range f.Captures {
		field := e.captureField(f, local)
		e.line(1, "closure->%s = %s;", field, field)
	}
	e.line(1, "pts_publish();")
	e.line(1, "return &closure->closure;")
	e.line(0, "}")
}

// closurePrologue starts the body of a closure's function: a C local for each captured
// value (or cell) and for the function's own closure (Self), and a cell for each
// parameter that lives in one, initialised with the argument.
func (e *emitter) closurePrologue(f *ir.Func, paramNames []string) {
	e.capturePrologue(f)
	for i, param := range f.Params {
		if param.Cell {
			cell := e.declareLocal(param)
			e.line(1, "%s*%s = %s;", e.declarator(param.Type), cell, e.cellAllocation(param))
			e.line(1, "*%s = %s;", cell, paramNames[i])
		}
	}
}

// capturePrologue reads the locals a closure captures out of its closure, pts_env: one
// of its own function's, or for an instance of a generic arrow function, its erased
// copy's (D-190).
func (e *emitter) capturePrologue(f *ir.Func) {
	owner := f
	if f.Shares != nil {
		owner = f.Shares
	}
	for _, local := range f.Captures {
		e.line(1, "%s%s = ((pts_closure_%s *)pts_env)->%s;", e.capturedDeclarator(local), e.declareLocal(local), e.closureNames[owner], e.captureField(owner, local))
	}
	if f.Self != nil && !slices.Contains(f.Captures, f.Self) && usesLocal(f, f.Self) {
		e.line(1, "pts_closure *%s = pts_env;", e.declareLocal(f.Self))
	}
}

// usesLocal reports whether f's body reads local or makes a closure that captures it.
func usesLocal(f *ir.Func, local *ir.Local) bool {
	used := false
	ir.Walk(f.Body.Stmts, func(ir.Stmt) {}, func(x ir.Expr) {
		switch x := x.(type) {
		case *ir.LocalRef:
			used = used || x.Local == local
		case *ir.MakeClosure:
			used = used || slices.Contains(x.Func.Captures, local)
		}
	})
	return used
}

// cellAllocation allocates the cell of a local that lives in one.
func (e *emitter) cellAllocation(local *ir.Local) string {
	return fmt.Sprintf("pts_cell_new(%s, %t)", e.cellSize(local), holdsPointer(local.Type))
}

// cellSize is the size of a local's cell: its value's, and an early cell's ready flag after it (D-140).
func (e *emitter) cellSize(local *ir.Local) string {
	if local.Early {
		return fmt.Sprintf("sizeof(%s) + sizeof(bool)", e.cellType(local))
	}
	return fmt.Sprintf("sizeof(%s)", e.cellType(local))
}

// cellType is the C type a local's cell holds.
func (e *emitter) cellType(local *ir.Local) string {
	return strings.TrimSuffix(e.declarator(local.Type), " ")
}

// cellsOf are the locals that live in cells which a statement list declares, directly or
// in the cases of a switch in it, which share the list's scope.
func cellsOf(stmts []ir.Stmt) []*ir.Local {
	var cells []*ir.Local
	for _, stmt := range stmts {
		switch s := stmt.(type) {
		case *ir.LocalDecl:
			if s.Local.Cell && !s.Local.Global {
				cells = append(cells, s.Local)
			}
		case *ir.Switch:
			for _, c := range s.Cases {
				cells = append(cells, cellsOf(c.Body)...)
			}
		}
	}
	return cells
}

// makeClosure writes a function value: a new closure of its function with the current
// values of its captured variables (or their cells), or its static closure.
func (e *emitter) makeClosure(m *ir.MakeClosure) string {
	f := m.Func
	if len(f.Captures) == 0 {
		return "(&pts_static_" + e.closureNames[f] + ")"
	}
	args := make([]string, len(f.Captures))
	for i, local := range f.Captures {
		args[i] = e.cellOrValue(local)
	}
	return "pts_new_closure_" + e.closureNames[f] + "(" + strings.Join(args, ", ") + ")"
}

// cellOrValue is what a closure holds of a captured local: its cell, or its value.
func (e *emitter) cellOrValue(local *ir.Local) string {
	if local.Cell {
		return e.locals[local]
	}
	return e.local(local)
}

// callValue writes a call of a function value: its code, cast to the function's type,
// called with the closure and the arguments.
func (e *emitter) callValue(call *ir.CallValue) string {
	args, prelude := e.operands(append([]ir.Expr{call.Callee}, call.Args...))
	callee := args[0]
	if !isIdentifier(callee) {
		closure := e.temporary()
		prelude += "pts_closure *" + closure + " = " + callee + "; "
		callee = closure
	}
	ft := call.Callee.Type().(*ir.FuncType)
	args[0] = callee
	return ordered(prelude, "(("+e.functionPointer(ft)+")"+callee+"->code)("+strings.Join(args, ", ")+")")
}

// functionPointer is the C type of the code of closures of a function type.
func (e *emitter) functionPointer(ft *ir.FuncType) string {
	params := []string{"pts_closure *"}
	for _, param := range ft.Params {
		params = append(params, strings.TrimSuffix(e.declarator(param), " "))
	}
	return e.declarator(ft.Result) + "(*)(" + strings.Join(params, ", ") + ")"
}
