// Package cgen emits C11 for an IR program. The C is meant to be read: locals keep
// their source names where C allows, and #line directives map every statement back
// to its .pts line, so debuggers and crash backtraces show ParallelScript source.
package cgen

import (
	"fmt"
	"math"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/microsoft/typescript-go/pts/ir"
)

// Emit returns the C translation unit for program. It includes pts.h and defines main.
func Emit(program *ir.Program) string {
	e := &emitter{checks: map[ir.Type]string{}, methodAdapters: map[string]*methodAdapter{}, strings: map[string]string{}, funcNames: functionNames(program), globals: map[*ir.Local]string{},
		globalOwners: map[*ir.Local]*ir.Func{}, initOrder: map[*ir.Func]int{}, classNames: map[*ir.Class]string{}, methods: map[*ir.Func]*ir.Method{},
		interfaceMembers: map[*ir.Class][]*ir.Method{}, entryNames: map[*ir.Func]string{}, closureNames: map[*ir.Func]string{}, captureFields: map[*ir.Local][]captureField{}, comparators: map[ir.Type]string{}, mapDescriptors: map[*ir.MapType]string{},
		arrayDescriptors: map[*ir.Array]string{}, funcTypes: map[string]*ir.FuncType{}}
	e.nameClasses(program)
	e.externFunctions(program)
	e.collectClosures(program)
	e.collectComparators(program)
	e.collectMaps(program)
	e.collectChecks(program)
	e.indexMethods(program)
	e.collectInterfaceMembers(program)
	e.collectDeclaredTypes(program)
	e.collectDynamicEntries(program)
	e.collectMethodAdapters(program)
	e.collectStrings(program)
	e.collectGlobals(program)
	e.collectGenerators(program)
	e.header(program)
	for _, f := range program.Funcs {
		e.function(f)
	}
	return e.out.String()
}

type emitter struct {
	out
	strings     map[string]string // string constant value → C name
	stringOrder []string          // values in order of first use
	funcNames   map[*ir.Func]string
	classNames  map[*ir.Class]string    // the suffix of each class's C names (pts_class_<suffix>, pts_type_<suffix>)
	methods     map[*ir.Func]*ir.Method // the method each method's function implements
	// interfaceMembers are the methods, getters and setters of each class that interfaces
	// may call, in its descriptor's method table; entryNames name the functions that
	// interfaces call them through, which take `this` as any object (D-036).
	interfaceMembers map[*ir.Class][]*ir.Method
	entryNames       map[*ir.Func]string
	entryOrder       []*ir.Method
	closureNames     map[*ir.Func]string // the suffix of the C names of each function made a value of (D-041)
	closureOrder     []*ir.Func
	comparators      map[ir.Type]string     // the function that calls comparators of each element type sorted with one
	mapDescriptors   map[*ir.MapType]string // the descriptor of each map and set type the program makes maps of
	mapOrder         []*ir.MapType
	comparatorOrder  []ir.Type
	captureFields    map[*ir.Local][]captureField // the members of closure structs that hold each captured local
	globals          map[*ir.Local]string         // module-scope bindings that other functions use, by C name
	globalOrder      []*ir.Local
	globalOwners     map[*ir.Local]*ir.Func // the top-level code that declares each global
	initOrder        map[*ir.Func]int       // each module's top-level code: its place in the order modules run
	current          *ir.Func
	locals           map[*ir.Local]string
	usedNames        map[string]bool
	nextSuffix       map[string]int // per base name, the suffix to try first, so many same-named locals stay linear
	temporaries      int            // pts_tmpN locals made so far in the function
	sites            int            // pts_siteN caches made so far in the function (fieldRef)
	labels           map[*ir.Label]string
	usedLabels       map[string]bool
	gotoTargets      map[string]bool // C labels some goto jumps to, so only those are written
	loops            []*ir.Label     // the C loops around the current statement, innermost last
	// tries are the handlers of the try blocks around the current statement, innermost
	// last; labelTries is how many there were where each label's statement is, so a jump
	// to it leaves those inside it (D-068).
	tries      []string
	labelTries map[*ir.Label]int
	handlers   int                // pts_tryN handlers made so far in the function
	volatile   map[*ir.Local]bool // locals a try block assigns that are declared outside it
	frames     map[*ir.Func]*generatorFrame
	frame      *generatorFrame // of the generator whose step function is being written, or nil

	hasFunctionNames bool // whether the program has a table of its functions' JS names (functionNameTable)

	checks     map[ir.Type]string // the pts_check of each type dynamic values are converted to, and of the types in it (D-071)
	checkOrder []ir.Type
	// declaredTypes is whether the program uses dynamic values beyond primitives (converts
	// them to types valueTest can't test, or writes or calls through `any`), so that
	// descriptors carry the declared types of fields, accessors, elements and hash-map
	// values (D-072), and functions and methods have dynamic entries (D-074) and signatures
	// (D-075); arrayDescriptors name the program's descriptors of arrays whose element kind
	// doesn't tell their element type, and funcTypes the one function type of each
	// signature, by the identities of its parameter and result types.
	declaredTypes    bool
	arrayDescriptors map[*ir.Array]string
	arrayOrder       []*ir.Array
	funcTypes        map[string]*ir.FuncType
	// methodAdapters are the adapters of the interface methods the program calls, by name
	// and check of their function type, in a program whose objects may come through `any`
	// with methods of other function types (D-078).
	methodAdapters     map[string]*methodAdapter
	methodAdapterOrder []*methodAdapter
}

func (e *emitter) header(program *ir.Program) {
	e.line(0, "/* Generated by pts from %s. */", strings.ReplaceAll(filepath.Base(program.Main.Pos.File), "*/", "* /"))
	e.line(0, `#include "pts.h"`)
	e.line(0, "")
	for _, value := range e.stringOrder {
		e.line(0, "%s", staticString(e.strings[value], value))
	}
	if len(e.stringOrder) > 0 {
		e.line(0, "")
	}
	e.classDeclarations(program.Classes)
	e.closureStructs()
	e.frameStructs(program)
	for _, global := range e.globalOrder {
		e.line(0, "static %s%s;", e.declarator(global.Type), e.globals[global])
		e.line(0, "static bool %s_ready; /* its declaration has run */", e.globals[global])
	}
	if len(e.globalOrder) > 0 {
		e.line(0, "")
	}
	for _, f := range program.Funcs {
		e.line(0, "static %s;", e.signature(f, nil))
	}
	e.line(0, "")
	e.entryFunctions()
	e.dynamicEntryDeclarations(program)
	e.checkDeclarations()
	e.classDescriptors(program.Classes)
	e.closureDefinitions()
	e.comparatorFunctions()
	e.mapDescriptorDefinitions()
	e.arrayDescriptorDefinitions()
	e.checkDefinitions()
	e.dynamicEntryDefinitions(program)
	e.functionNameTable(program)
	e.line(0, "int main(int argc, char **argv) {")
	e.line(1, "pts_runtime_init(argc, argv);")
	if e.hasFunctionNames {
		e.line(1, "pts_register_function_names(pts_function_names, sizeof pts_function_names / sizeof pts_function_names[0]);")
	}
	for _, module := range program.Modules {
		e.line(1, "%s();", e.funcNames[module.Init])
	}
	e.line(1, "return pts_runtime_exit(0);")
	e.line(0, "}")
}

// builtinOwners name the helpers lowering makes for builtin methods (Array.map), which
// stack traces show by their full names, as Node does.
var builtinOwners = map[string]bool{"Array": true, "Map": true, "Set": true, "Object": true}

// functionNameTable writes the JS name of each function of the program by its C name,
// sorted, which stack traces show (D-069); constructors of classes derived from Error are
// marked, since an error's stack leaves out the constructors that made it.
func (e *emitter) functionNameTable(program *ir.Program) {
	constructors := map[*ir.Func]*ir.Class{}
	classNames := map[string]bool{}
	for _, class := range program.Classes {
		constructors[class.Constructor] = class
		classNames[class.Name] = true
	}
	type entry struct {
		c, js            string
		errorConstructor bool
	}
	entries := make([]entry, 0, len(program.Funcs))
	for _, f := range program.Funcs {
		js := ""
		class, isConstructor := constructors[f]
		method := e.methods[f]
		switch {
		case f == f.Module.Init:
			// top-level code, which Node shows without a name
		case isConstructor:
			js = "new " + class.Name
		case method != nil:
			js = method.Class.Name + "." + strings.Replace(method.Name, "@@iterator", "[Symbol.iterator]", 1)
		case f.Closure:
			js = f.JSName
		default:
			parts := strings.Split(f.Name, ".")
			if f.JSName != "" {
				parts[len(parts)-1] = f.JSName // a generic function's instance (`identity_number`) is the function
			}
			js = parts[len(parts)-1]
			if len(parts) > 1 && (classNames[parts[0]] || builtinOwners[parts[0]]) {
				js = strings.Join(parts, ".") // a static method, or a helper such as Array.map
			}
		}
		if errorConstructor := isConstructor && root(class).Builtin; js != "" || errorConstructor {
			entries = append(entries, entry{e.funcNames[f], js, errorConstructor}) // a frame of a function not listed shows no name
		}
	}
	e.hasFunctionNames = len(entries) > 0
	if !e.hasFunctionNames {
		return
	}
	slices.SortFunc(entries, func(a, b entry) int { return strings.Compare(a.c, b.c) })
	e.line(0, "static const pts_function_name pts_function_names[] = {")
	for _, entry := range entries {
		e.line(1, "{%s, %s, %t},", cString(entry.c), cString(entry.js), entry.errorConstructor)
	}
	e.line(0, "};")
	e.line(0, "")
}

// functionNames gives each function its C name (D-020, D-022): the entry module's
// top-level code is pts_main and another module's pts_module_<module>; functions are
// pts_fn_<name>, with a nested function's qualified name joined by _, and outside the
// entry module the module's name first: pts_fn_<module>_<name>.
func functionNames(program *ir.Program) map[*ir.Func]string {
	names := map[*ir.Func]string{program.Main: "pts_main"}
	used := map[string]bool{"pts_main": true}
	for _, f := range program.Funcs {
		if f == program.Main {
			continue
		}
		var base string
		if f == f.Module.Init {
			base = "pts_module_" + moduleIdentifier(f.Module.Name)
		} else {
			parts := strings.Split(f.Name, ".")
			for i, part := range parts {
				parts[i] = memberIdentifier(part)
			}
			base = "pts_fn_" + modulePrefix(f.Module) + strings.Join(parts, "_")
		}
		names[f] = unique(used, base)
	}
	return names
}

// modulePrefix starts the C names of a module's functions and globals: "" in the entry
// module, "<module>_" in others.
func modulePrefix(module *ir.Module) string {
	if module.Name == "" {
		return ""
	}
	return moduleIdentifier(module.Name) + "_"
}

// uniqueGlobal is unique for a global, whose name with _ready appended names its ready flag.
func uniqueGlobal(used map[string]bool, base string) string {
	name := base
	for i := 2; used[name] || used[name+"_ready"]; i++ {
		name = fmt.Sprintf("%s_%d", base, i)
	}
	used[name], used[name+"_ready"] = true, true
	return name
}

// unique returns base, or base_2, base_3, … if it is already used, and marks the result used.
func unique(used map[string]bool, base string) string {
	name := base
	for i := 2; used[name]; i++ {
		name = fmt.Sprintf("%s_%d", base, i)
	}
	used[name] = true
	return name
}

// signature is a function's C declarator. paramNames are the parameters' C names; with
// nil (a prototype) they are the source names as C allows them.
func (e *emitter) signature(f *ir.Func, paramNames []string) string {
	params := make([]string, len(f.Params))
	used := map[string]bool{}
	for i, param := range f.Params {
		name := unique(used, cIdentifier(param.Name))
		if paramNames != nil {
			name = paramNames[i]
		}
		params[i] = e.declarator(param.Type) + name
		if paramNames != nil {
			params[i] = e.localDeclarator(param) + name // the definition's
		}
		if receiver := e.receiverClass(f); i == 0 && receiver != nil {
			params[i] = e.declarator(receiver) + name
		}
	}
	if f.Closure {
		params = append([]string{"pts_closure *pts_env"}, params...)
	}
	if len(params) == 0 {
		params = []string{"void"}
	}
	return fmt.Sprintf("%s%s(%s)", e.declarator(f.Result), e.funcNames[f], strings.Join(params, ", "))
}

// collectGlobals names the module-scope locals that other functions use:
// pts_global_<name>, or pts_global_<module>_<name> outside the entry module.
func (e *emitter) collectGlobals(program *ir.Program) {
	used := map[string]bool{}
	for i, module := range program.Modules {
		e.initOrder[module.Init] = i
		ir.Walk(module.Init.Body.Stmts, func(stmt ir.Stmt) {
			if decl, ok := stmt.(*ir.LocalDecl); ok && decl.Local.Global {
				e.globals[decl.Local] = uniqueGlobal(used, "pts_global_"+modulePrefix(module)+qualifiedIdentifier(decl.Local.Name))
				e.globalOrder = append(e.globalOrder, decl.Local)
				e.globalOwners[decl.Local] = module.Init
			}
		}, func(ir.Expr) {})
	}
}

func (e *emitter) function(f *ir.Func) {
	if f.Generator {
		e.generatorFunction(f)
		return
	}
	e.startFunction(f)
	paramNames := make([]string, len(f.Params))
	for i, param := range f.Params {
		if param.Cell {
			paramNames[i] = unique(e.usedNames, "pts_arg_"+cIdentifier(param.Name)) // the argument, which the prologue moves into the cell
		} else {
			paramNames[i] = e.declareLocal(param)
		}
	}
	this := ""
	if receiver := e.receiverClass(f); receiver != nil && receiver != f.Params[0].Type {
		this, paramNames[0] = paramNames[0], "pts_this" // `this` as the slot's class, which its struct starts with
	}
	e.line(0, "")
	e.at(f.Pos)
	e.line(0, "static %s {", e.signature(f, paramNames))
	if this != "" {
		class := e.declarator(f.Params[0].Type)
		e.line(1, "%s%s = (%s)pts_this;", class, this, strings.TrimSuffix(class, " "))
	}
	e.closurePrologue(f, paramNames)
	if f != f.Module.Init && mayRecurse(f) { // a module's top-level code runs once
		e.line(1, "pts_stack_check();")
	}
	e.scope(f.Body.Stmts, 1)
	switch {
	case f.Result == ir.Void || endsWithReturn(f.Body.Stmts):
	case holdsUndefined(f.Result):
		e.line(1, "return PTS_VALUE_UNDEFINED; /* JS's implicit undefined (D-089) */")
	default:
		e.line(1, "pts_unreachable(); /* tsc checked that every path returns */")
	}
	e.line(0, "}")
}

// startFunction resets what the emitter knows of the function it writes to f's.
func (e *emitter) startFunction(f *ir.Func) {
	e.current = f
	e.locals = map[*ir.Local]string{}
	e.usedNames = map[string]bool{}
	e.nextSuffix = map[string]int{}
	e.temporaries = 0
	e.sites = 0
	e.labels = map[*ir.Label]string{}
	e.usedLabels = map[string]bool{}
	e.gotoTargets = map[string]bool{}
	e.tries = nil
	e.labelTries = map[*ir.Label]int{}
	e.handlers = 0
	e.volatile = volatileLocals(f)
}

func endsWithReturn(stmts []ir.Stmt) bool {
	if len(stmts) == 0 {
		return false
	}
	_, ok := stmts[len(stmts)-1].(*ir.Return)
	return ok
}

// ---------------------------------------------------------------- statements

func (e *emitter) statements(stmts []ir.Stmt, depth int) {
	for _, stmt := range stmts {
		e.statement(stmt, depth)
	}
}

// scope writes the statements of a block, after making the cells of the locals it
// declares that live in one: on entry, since a closure may capture one before its
// declaration runs (D-041).
func (e *emitter) scope(stmts []ir.Stmt, depth int) {
	for _, local := range cellsOf(stmts) {
		if e.frameLocal(local) {
			e.line(depth, "%s = %s;", e.locals[local], e.cellAllocation(local))
			continue
		}
		e.line(depth, "%s*%s = %s;", e.declarator(local.Type), e.declareLocal(local), e.cellAllocation(local))
	}
	e.statements(stmts, depth)
}

func (e *emitter) statement(stmt ir.Stmt, depth int) {
	e.at(stmt.Pos())
	switch s := stmt.(type) {
	case *ir.Block:
		e.line(depth, "{")
		e.scope(s.Stmts, depth+1)
		e.line(depth, "}")
	case *ir.ExprStmt:
		e.line(depth, "%s;", e.effect(s.X))
	case *ir.LocalDecl:
		init := zeroValue(s.Local.Type, e.strings[""])
		if s.Init != nil {
			init = e.expr(s.Init)
		}
		if name, ok := e.globals[s.Local]; ok {
			e.line(depth, "%s = %s;", name, init)
			e.line(depth, "%s_ready = true;", name)
			return
		}
		if s.Local.Cell {
			e.line(depth, "*%s = %s;", e.locals[s.Local], init) // its block made the cell
			return
		}
		if e.frameLocal(s.Local) {
			e.line(depth, "%s = %s;", e.locals[s.Local], init)
			return
		}
		e.line(depth, "%s%s = %s;", e.localDeclarator(s.Local), e.declareLocal(s.Local), init)
	case *ir.If:
		e.ifStatement(s, depth)
	case *ir.Loop:
		e.loop(s, depth)
	case *ir.Switch:
		e.switchStatement(s, depth)
	case *ir.LabeledBlock:
		name := e.label(s.Label, "block")
		e.line(depth, "{")
		e.scope(s.Body.Stmts, depth+1)
		e.line(depth, "}")
		e.labelHere(depth, name+"_break")
	case *ir.Break:
		e.jump(depth, s.Target, "break", "_break")
	case *ir.Continue:
		e.jump(depth, s.Target, "continue", "_continue")
	case *ir.Return:
		e.returnStatement(s, depth)
	case *ir.Try:
		e.tryStatement(s, depth)
	case *ir.Throw:
		e.line(depth, "pts_throw(%s);", e.expr(s.Value))
	case *ir.Yield:
		e.yieldStatement(s, depth)
	default:
		panic(fmt.Sprintf("cgen: unexpected statement %T", stmt))
	}
}

func (e *emitter) ifStatement(s *ir.If, depth int) {
	e.line(depth, "if (%s) {", e.expr(s.Cond))
	e.scope(s.Then.Stmts, depth+1)
	for s.Else != nil {
		elseIf, ok := s.Else.(*ir.If)
		if !ok {
			e.line(depth, "} else {")
			e.scope(s.Else.(*ir.Block).Stmts, depth+1)
			break
		}
		e.at(elseIf.At)
		e.line(depth, "} else if (%s) {", e.expr(elseIf.Cond))
		e.scope(elseIf.Then.Stmts, depth+1)
		s = elseIf
	}
	e.line(depth, "}")
}

func (e *emitter) loop(loop *ir.Loop, depth int) {
	cond := "1"
	if loop.Cond != nil {
		cond = e.expr(loop.Cond)
	}
	name := e.label(loop.Label, "loop")
	update := e.perIterationCopies(loop)
	if loop.Update != nil {
		update = append(update, e.effect(loop.Update))
	}
	switch {
	case loop.PostTest:
		e.line(depth, "do {")
	case len(update) > 0:
		e.line(depth, "for (; %s; %s) {", cond, strings.Join(update, ", "))
	case loop.Cond == nil:
		e.line(depth, "for (;;) {")
	default:
		e.line(depth, "while (%s) {", cond)
	}
	e.loops = append(e.loops, loop.Label)
	e.scope(loop.Body.Stmts, depth+1)
	e.loops = e.loops[:len(e.loops)-1]
	e.labelHere(depth+1, name+"_continue")
	if loop.PostTest {
		e.line(depth, "} while (%s);", cond)
	} else {
		e.line(depth, "}")
	}
	e.labelHere(depth, name+"_break")
}

// perIterationCopies give the next iteration of a for loop its own copy of each `let`
// variable of the loop's initialiser that lives in a cell, as JS does, so that closures
// made in this iteration keep this iteration's (D-041).
func (e *emitter) perIterationCopies(loop *ir.Loop) []string {
	var copies []string
	for _, local := range loop.Bindings {
		if local.Cell {
			name := e.locals[local]
			copies = append(copies, fmt.Sprintf("%s = pts_cell_copy(%s, sizeof(%s), %t)", name, name, e.cellType(local), holdsPointer(local.Type)))
		}
	}
	return copies
}

// switchStatement tests the cases in order and jumps to the first that matches, or to
// the default case; the bodies follow one another, so control falls through as in JS.
func (e *emitter) switchStatement(s *ir.Switch, depth int) {
	name := e.label(s.Label, "switch")
	caseLabels := make([]string, len(s.Cases))
	otherwise := name + "_break"
	for i, c := range s.Cases {
		caseLabels[i] = fmt.Sprintf("%s_case_%d", name, i+1)
		if c.Match == nil {
			caseLabels[i] = name + "_default"
			otherwise = caseLabels[i]
			continue
		}
		e.at(c.At)
		e.line(depth, "if (%s) goto %s;", e.expr(c.Match), caseLabels[i])
	}
	e.line(depth, "goto %s;", otherwise)
	e.gotoTargets[otherwise] = true
	for i, c := range s.Cases {
		e.at(c.At)
		e.line(depth, "%s: ;", caseLabels[i])
		e.statements(c.Body, depth+1)
	}
	e.labelHere(depth, name+"_break")
}

// returnStatement writes a return, which leaves every try block around it once its value
// is computed: a throw while computing it still lands in them.
func (e *emitter) returnStatement(s *ir.Return, depth int) {
	if e.frame != nil {
		e.generatorReturn(s, depth)
		return
	}
	switch {
	case len(e.tries) == 0 && s.Value == nil:
		e.line(depth, "return;")
	case len(e.tries) == 0:
		e.line(depth, "return %s;", e.expr(s.Value))
	case s.Value == nil:
		e.line(depth, "pts_try_leave(&%s);", e.tries[0])
		e.line(depth, "return;")
	default:
		result := e.temporary()
		e.line(depth, "{")
		e.line(depth+1, "%s%s = %s;", e.declarator(e.current.Result), result, e.expr(s.Value))
		e.line(depth+1, "pts_try_leave(&%s);", e.tries[0])
		e.line(depth+1, "return %s;", result)
		e.line(depth, "}")
	}
}

// tryStatement writes a try (D-068): a handler pushed on the thread's chain, the body,
// which pops it when it ends, and the catch block, which the handler's long jump runs
// with the exception (pts_throw has popped the handler).
func (e *emitter) tryStatement(s *ir.Try, depth int) {
	e.handlers++
	handler := fmt.Sprintf("pts_try%d", e.handlers)
	entry := ""
	if e.frame != nil {
		entry = e.frame.entries[s] // a resume point inside: the dispatch jumps here, then on (D-109)
	}
	if entry != "" {
		e.line(depth, "%s: ;", entry)
	}
	e.line(depth, "{")
	e.line(depth+1, "pts_handler %s;", handler)
	e.line(depth+1, "pts_try_enter(&%s);", handler)
	e.line(depth+1, "if (_setjmp(%s.jump) == 0) {", handler)
	e.tries = append(e.tries, handler)
	if entry != "" {
		e.dispatch(depth+2, s)
	}
	e.scope(s.Body.Stmts, depth+2)
	e.tries = e.tries[:len(e.tries)-1]
	e.line(depth+2, "pts_try_leave(&%s);", handler)
	e.line(depth+1, "} else {")
	caught := "pts_caught(&" + handler + ")"
	if e.frameLocal(s.Exception) {
		if s.Exception.Cell {
			e.line(depth+2, "%s = %s;", e.locals[s.Exception], e.cellAllocation(s.Exception))
		}
		e.line(depth+2, "%s = %s;", e.local(s.Exception), caught)
	} else if s.Exception.Cell {
		cell := e.declareLocal(s.Exception)
		e.line(depth+2, "%s*%s = %s;", e.declarator(s.Exception.Type), cell, e.cellAllocation(s.Exception))
		e.line(depth+2, "*%s = %s;", cell, caught)
	} else {
		e.line(depth+2, "%s%s = %s;", e.localDeclarator(s.Exception), e.declareLocal(s.Exception), caught)
	}
	e.scope(s.Catch.Stmts, depth+2)
	e.line(depth+1, "}")
	e.line(depth, "}")
}

// mayRecurse reports whether f runs other code of the program, so that a recursion may
// go through it: it calls a function, a method or a function value, makes an instance,
// reads or writes a property through an interface (a getter or setter), or calls the
// runtime, which may call back (a comparator, a toString). Every cycle of calls has such
// a function, whose prologue checks the stack (D-069).
func mayRecurse(f *ir.Func) bool {
	found := false
	ir.Walk(f.Body.Stmts, func(ir.Stmt) {}, func(x ir.Expr) {
		switch x := x.(type) {
		case *ir.Call, *ir.CallValue, *ir.VirtualCall, *ir.InterfaceCall, *ir.New, *ir.ArrayCall, *ir.ToString:
			found = true
		case *ir.CallBuiltin: // one that may convert an object to a string calls its toString()
			found = found || !ir.IsConsole(x.Builtin) && slices.ContainsFunc(x.Args, func(arg ir.Expr) bool { return isDynamic(arg.Type()) || isObject(arg.Type()) })
		case *ir.FieldRef:
			found = found || !isClass(x.X.Type())
		case *ir.SetField:
			found = found || !isClass(x.X.Type())
		}
	})
	return found
}

// volatileLocals are the locals that a try block of f assigns and that are declared
// outside it. After a throw long-jumps out of the block, C keeps their values only if
// they are volatile (C11 7.13.2.1); locals in cells and globals are in memory anyway.
func volatileLocals(f *ir.Func) map[*ir.Local]bool {
	volatile := map[*ir.Local]bool{}
	ir.Walk(f.Body.Stmts, func(stmt ir.Stmt) {
		try, ok := stmt.(*ir.Try)
		if !ok {
			return
		}
		declared := map[*ir.Local]bool{}
		var assigned []*ir.Local
		ir.Walk(try.Body.Stmts, func(stmt ir.Stmt) {
			switch s := stmt.(type) {
			case *ir.LocalDecl:
				declared[s.Local] = true
			case *ir.Try:
				declared[s.Exception] = true
			}
		}, func(x ir.Expr) {
			switch x := x.(type) {
			case *ir.Assign:
				assigned = append(assigned, x.Local)
			case *ir.Bind:
				declared[x.Local] = true
			}
		})
		for _, local := range assigned {
			if !declared[local] && !local.Cell && !local.Global {
				volatile[local] = true
			}
		}
	}, func(ir.Expr) {})
	return volatile
}

// localDeclarator is declarator for a local, volatile if a try block needs it to be.
func (e *emitter) localDeclarator(local *ir.Local) string {
	declarator := e.declarator(local.Type)
	switch {
	case !e.volatile[local]:
		return declarator
	case strings.HasSuffix(declarator, "*"):
		return declarator + "volatile "
	}
	return "volatile " + declarator
}

// jump writes a break or continue: the C statement when the target is the innermost C
// loop, or a goto to the target's label otherwise. A jump out of try blocks leaves them.
func (e *emitter) jump(depth int, target *ir.Label, keyword string, suffix string) {
	if inside := e.labelTries[target]; inside < len(e.tries) {
		e.line(depth, "pts_try_leave(&%s);", e.tries[inside])
	}
	if len(e.loops) > 0 && e.loops[len(e.loops)-1] == target {
		e.line(depth, "%s;", keyword)
		return
	}
	label := e.labels[target] + suffix
	e.gotoTargets[label] = true
	e.line(depth, "goto %s;", label)
}

// label names a target's C labels: its source label if it has one, else what it is.
func (e *emitter) label(label *ir.Label, kind string) string {
	base := kind
	if label.Name != "" {
		base = cIdentifier(label.Name)
	}
	name := base
	for i := 2; e.usedLabels[name]; i++ {
		name = fmt.Sprintf("%s_%d", base, i)
	}
	e.usedLabels[name] = true
	e.labels[label] = name
	e.labelTries[label] = len(e.tries)
	return name
}

// labelHere writes a C label if some goto jumps to it. The empty statement after it
// lets a declaration follow, which C11 doesn't allow straight after a label.
func (e *emitter) labelHere(depth int, label string) {
	if e.gotoTargets[label] {
		e.line(depth, "%s: ;", label)
	}
}

// zeroValue is what `let x: T;` starts as; empty is the C name of the "" constant.
func zeroValue(t ir.Type, empty string) string {
	switch {
	case t == ir.Bool:
		return "false"
	case t == ir.String:
		return empty
	case t == ir.Null:
		return "PTS_VALUE_NULL"
	case t == ir.Undefined || t == ir.Value || isUnion(t):
		return "PTS_VALUE_UNDEFINED"
	case t == ir.F32:
		return "0.0f"
	case t == ir.Number:
		return "0.0"
	}
	return "0" // the integer types
}

// declareLocal picks the local's C name: its source name if C allows it, made unique in the function.
func (e *emitter) declareLocal(local *ir.Local) string {
	base := cIdentifier(local.Name)
	name := base
	for e.usedNames[name] {
		suffix := max(e.nextSuffix[base], 2)
		e.nextSuffix[base] = suffix + 1
		name = fmt.Sprintf("%s_%d", base, suffix)
	}
	e.usedNames[name] = true
	e.locals[local] = name
	return name
}

// ---------------------------------------------------------------- expressions

func (e *emitter) expr(expr ir.Expr) string {
	switch x := expr.(type) {
	case *ir.NumberConst:
		return numberLiteral(x.Value)
	case *ir.BoolConst:
		return strconv.FormatBool(x.Value)
	case *ir.StringConst:
		return e.strings[x.Value]
	case *ir.Resumed:
		return "pts_frame->header.sent"
	case *ir.ResumedBy:
		return "(pts_frame->header.mode == " + resumeModes[x.Mode] + ")"
	case *ir.UndefinedConst:
		return "PTS_VALUE_UNDEFINED"
	case *ir.NullConst:
		return "PTS_VALUE_NULL"
	case *ir.GivenConst:
		return strconv.FormatBool(x.Value)
	case *ir.LeftOut:
		return zeroValue(x.T, e.strings[""])
	case *ir.Absent:
		return "PTS_VALUE_ABSENT"
	case *ir.LocalRef:
		if check := e.readyCheck(x.Local); check != "" {
			return "(" + check + ", " + e.local(x.Local) + ")"
		}
		return e.local(x.Local)
	case *ir.Assign:
		if x.Postfix {
			old := e.temporary()
			check := e.readyCheck(x.Local)
			if check != "" {
				check += "; "
			}
			name := e.local(x.Local)
			return fmt.Sprintf("({ %s%s%s = %s; %s = %s; %s; })", check, e.declarator(x.Local.Type), old, name, name, e.expr(x.Value), old)
		}
		return "(" + e.assignment(x) + ")"
	case *ir.Truthy:
		return truthy(x.X.Type(), e.expr(x.X))
	case *ir.Conditional:
		return "(" + e.expr(x.Cond) + " ? " + e.expr(x.Then) + " : " + e.expr(x.Else) + ")"
	case *ir.Logical:
		return e.logical(x)
	case *ir.Sequence:
		return "(" + e.expr(x.X) + ", " + e.expr(x.Y) + ")"
	case *ir.Bind:
		return e.bind(x)
	case *ir.New:
		return e.newInstance(x)
	case *ir.NewObject:
		return e.newObject(x)
	case *ir.FieldRef:
		if x.Field.Optional && !x.Raw {
			return "pts_value_present(" + e.fieldRead(e.expr(x.X), x.X.Type(), x.Field) + ")"
		}
		return e.fieldRead(e.expr(x.X), x.X.Type(), x.Field)
	case *ir.HasField:
		return e.hasField(x)
	case *ir.IsAbsent:
		return "(" + e.expr(x.X) + " == PTS_VALUE_ABSENT)"
	case *ir.HasKey:
		return fmt.Sprintf("pts_object_has(%s, %s)", e.expr(x.X), cString(x.Name))
	case *ir.SetField:
		return e.setField(x)
	case *ir.Box:
		return e.box(x.X)
	case *ir.ToUnion:
		return e.box(x.X)
	case *ir.FromUnion:
		return e.fromUnion(x)
	case *ir.IsNullish:
		switch x.Only {
		case ir.Null:
			return "(" + e.expr(x.X) + " == PTS_VALUE_NULL)"
		case ir.Undefined:
			return "(" + e.expr(x.X) + " == PTS_VALUE_UNDEFINED)"
		}
		return "pts_value_is_nullish(" + e.expr(x.X) + ")"
	case *ir.RestArgs:
		return e.expr(x.X)
	case *ir.SpreadArg:
		return e.box(x.X)
	case *ir.CallBuiltin:
		return e.callBuiltin(x)
	case *ir.Call:
		operands := x.Args
		if x.Closure != nil {
			operands = append([]ir.Expr{x.Closure}, x.Args...)
		}
		args, prelude := e.operands(operands)
		if receiver := e.receiverClass(x.Func); receiver != nil && receiver != x.Args[0].Type() {
			args[0] = "(" + strings.TrimSuffix(e.declarator(receiver), " ") + ")(" + args[0] + ")"
		}
		return ordered(prelude, e.funcNames[x.Func]+"("+strings.Join(args, ", ")+")")
	case *ir.MakeClosure:
		return e.makeClosure(x)
	case *ir.NewMap:
		return e.newMap(x)
	case *ir.MapCall:
		return e.mapCall(x)
	case *ir.CallValue:
		return e.callValue(x)
	case *ir.VirtualCall:
		return e.virtualCall(x)
	case *ir.InterfaceCall:
		return e.interfaceCall(x)
	case *ir.NewArray:
		return e.newArray(x)
	case *ir.ArrayCall:
		return e.arrayCall(x)
	case *ir.ClassCast:
		return e.classCast(x)
	case *ir.InstanceOf:
		if x.Family != nil {
			return e.familyInstanceOf(x)
		}
		if isUnion(x.X.Type()) || x.X.Type() == ir.Value {
			return "pts_value_instance_of(" + e.expr(x.X) + ", " + e.descriptor(x.Class) + ")"
		}
		return "pts_instance_of(" + e.expr(x.X) + ", " + e.descriptor(x.Class) + ")"
	case *ir.Binary:
		return e.binary(x)
	case *ir.Unary:
		return e.unary(x)
	case *ir.ToString:
		return e.toString(x.X)
	case *ir.IntConst:
		return intLiteral(x)
	case *ir.Convert:
		return e.convert(x)
	default:
		panic(fmt.Sprintf("cgen: unexpected expression %T", expr))
	}
}

// assignment writes `x = value`, after the ready check if x is a global used from a function.
func (e *emitter) assignment(assign *ir.Assign) string {
	text := e.local(assign.Local) + " = " + e.expr(assign.Value)
	if check := e.readyCheck(assign.Local); check != "" {
		return check + ", " + text
	}
	return text
}

// local is the C lvalue of a local: its C name, the global it became, or its cell's contents.
func (e *emitter) local(local *ir.Local) string {
	if name, ok := e.globals[local]; ok {
		return name
	}
	if local.Cell {
		return "(*" + e.locals[local] + ")"
	}
	return e.locals[local]
}

// readyCheck is the check that a global's declaration has run, or "" where it surely
// has: in its own module's top-level code (tsc rejects uses above the declaration) and
// in the top-level code of a module that runs later. Functions may run at any time, and in
// an import cycle a module may run before a module it imports.
func (e *emitter) readyCheck(local *ir.Local) string {
	if !local.Global {
		return ""
	}
	owner := e.globalOwners[local]
	if order, isInit := e.initOrder[e.current]; isInit && order >= e.initOrder[owner] {
		return ""
	}
	name, _, _ := strings.Cut(local.Name, ".") // a static field "C.f" is unready because its class is, which JS reports
	return fmt.Sprintf("pts_check_ready(%s_ready, %s)", e.globals[local], cString(name))
}

// effect writes an expression evaluated only for its effects (a statement or a for
// loop's update), where an assignment needs no parentheses and x++ no old value.
func (e *emitter) effect(expr ir.Expr) string {
	switch x := expr.(type) {
	case *ir.Assign:
		return e.assignment(x)
	case *ir.SetField:
		if !x.Postfix {
			return e.storeField(x)
		}
	}
	return e.expr(expr)
}

func (e *emitter) unary(u *ir.Unary) string {
	x := e.expr(u.X)
	t := u.X.Type()
	switch {
	case u.Op == ir.Not:
		return "(!" + x + ")"
	case u.Op == ir.BitNot && ir.IsInteger(t):
		return "((" + cType(t) + ")~" + x + ")"
	case u.Op == ir.BitNot && t == ir.F32:
		return "((float)pts_number_bitnot(" + x + "))"
	case u.Op == ir.BitNot:
		return "pts_number_bitnot(" + x + ")"
	case ir.IsInteger(t):
		return "pts_" + t.String() + "_neg(" + x + ")"
	}
	return "(-" + x + ")"
}

// logical writes && and ||. On booleans they are C's; otherwise the left operand is
// kept in a temporary, since it is both tested and possibly the value.
func (e *emitter) logical(l *ir.Logical) string {
	x, y := e.expr(l.X), e.expr(l.Y)
	t := l.X.Type()
	if t == ir.Bool || t == ir.Given {
		return "(" + x + " " + l.Op.String() + " " + y + ")"
	}
	tmp := e.temporary()
	then, otherwise := y, tmp
	if l.Op == ir.Or {
		then, otherwise = tmp, y
	}
	return fmt.Sprintf("({ %s%s = %s; %s ? %s : %s; })", e.declarator(t), tmp, x, truthy(t, tmp), then, otherwise)
}

// holdsUndefined reports whether values of type t may be undefined, as a pts_value.
func holdsUndefined(t ir.Type) bool {
	if u, ok := t.(*ir.Union); ok {
		return u.Has(ir.Undefined)
	}
	return t == ir.Undefined || t == ir.Value
}

// familyInstanceOf writes `x instanceof C` for a generic class C: whether x is an
// instance of one of C's instances (D-089).
func (e *emitter) familyInstanceOf(x *ir.InstanceOf) string {
	tmp := e.temporary()
	test, declarator := "pts_instance_of", e.declarator(x.X.Type())
	if isUnion(x.X.Type()) || x.X.Type() == ir.Value {
		test = "pts_value_instance_of"
	}
	tests := []string{"false"}
	for _, class := range *x.Family {
		tests = append(tests, test+"("+tmp+", "+e.descriptor(class)+")")
	}
	return fmt.Sprintf("({ %s%s = %s; %s; })", declarator, tmp, e.expr(x.X), strings.Join(tests, " || "))
}

// fromUnion writes a union's value as the type the checker narrowed it to (D-062).
func (e *emitter) fromUnion(x *ir.FromUnion) string {
	value := e.expr(x.X)
	if x.Checked {
		value = "pts_value_non_null(" + value + ")"
	}
	if x.X.Type() == ir.Value && x.To != ir.Value {
		return e.fromValue(value, x.To)
	}
	switch t := x.To; {
	case t == ir.Number:
		return "pts_value_as_number(" + value + ")"
	case t == ir.I64:
		return "pts_value_as_i64(" + value + ")"
	case t == ir.U64:
		return "pts_value_as_u64(" + value + ")"
	case ir.IsNumeric(t):
		return "((" + cType(t) + ")pts_value_as_number(" + value + "))" // exact as a double
	case t == ir.Bool:
		return "(" + value + " == PTS_VALUE_TRUE)"
	case isClass(t):
		return "((" + e.cDeclaredType(t) + ")pts_value_as_instance(" + value + ", " + e.descriptor(t.(*ir.Class)) + "))"
	case t == ir.String || isObject(t):
		return "((" + e.cDeclaredType(t) + ")pts_value_as_object(" + value + "))"
	}
	return value // null, undefined, or a smaller union: the same pts_value
}

// fromValue converts value, a dynamic value (`any`, `unknown`), to t with a check, since
// nothing but the program's logic says it is one (D-068, D-070): TypeError if it isn't.
func (e *emitter) fromValue(value string, t ir.Type) string {
	switch {
	case t == ir.Number:
		return "pts_value_to_number_checked(" + value + ")" // a boxed 64-bit integer too
	case t == ir.String:
		return "pts_value_as_string(" + value + ")"
	case t == ir.Bool:
		return "pts_value_as_bool(" + value + ")"
	}
	if !hasValueTest(t) { // an object type: checked, and dynamic data copied into it (D-071)
		converted := "pts_value_convert(" + value + ", &" + e.checks[t] + ")"
		if isUnion(t) {
			return converted
		}
		return "((" + e.cDeclaredType(t) + ")pts_value_as_object(" + converted + "))"
	}
	tmp := e.temporary()
	var converted string
	switch {
	case t == ir.I64:
		converted = "pts_value_as_i64(" + tmp + ")"
	case t == ir.U64:
		converted = "pts_value_as_u64(" + tmp + ")"
	case ir.IsNumeric(t):
		converted = "((" + cType(t) + ")pts_value_as_number(" + tmp + "))" // exact: checked
	case isObject(t):
		converted = "((" + e.cDeclaredType(t) + ")pts_value_as_object(" + tmp + "))"
	default:
		converted = tmp // null, undefined or a union: the same pts_value
	}
	return fmt.Sprintf("({ pts_value %s = %s; if (!(%s)) pts_throw_type_mismatch(%s, %s); %s; })", tmp, value, e.valueTest(t, tmp), tmp, cString(t.String()), converted)
}

// valueTest is a C test of whether v, a pts_value, holds a value of type t: a number, a
// string, a boolean, null, undefined, an integer the integer type holds exactly, an
// instance of a class, or one of a union's members. The lowering converts dynamic values
// only to these (D-070).
func (e *emitter) valueTest(t ir.Type, v string) string {
	switch {
	case t == ir.Value:
		return "true"
	case t == ir.Number || t == ir.F32:
		return "pts_value_is_number(" + v + ")"
	case ir.IsInteger(t):
		return "pts_value_is_integer_of(" + v + ", " + fieldKind(t) + ")"
	case t == ir.String:
		return "pts_value_is_string(" + v + ")"
	case t == ir.Bool:
		return "(" + v + " == PTS_VALUE_TRUE || " + v + " == PTS_VALUE_FALSE)"
	case t == ir.Null:
		return "(" + v + " == PTS_VALUE_NULL)"
	case t == ir.Undefined:
		return "(" + v + " == PTS_VALUE_UNDEFINED)"
	case isClass(t):
		return "pts_value_instance_of(" + v + ", " + e.descriptor(t.(*ir.Class)) + ")"
	case isUnion(t):
		tests := make([]string, len(t.(*ir.Union).Members))
		for i, member := range t.(*ir.Union).Members {
			tests[i] = e.valueTest(member, v)
		}
		return "(" + strings.Join(tests, " || ") + ")"
	}
	panic("cgen: no test for a dynamic value as " + t.String())
}

// isClass reports whether t is a class, not a shape: a union narrowed to one is checked (D-067).
func isClass(t ir.Type) bool {
	class, ok := t.(*ir.Class)
	return ok && !class.Shape
}

// isDynamic reports whether values of t are pts_values: unions and dynamic values.
func isDynamic(t ir.Type) bool {
	return t == ir.Value || isUnion(t)
}

func isUnion(t ir.Type) bool {
	_, ok := t.(*ir.Union)
	return ok
}

// truthy writes JS's ToBoolean of x, a C expression of type t.
func truthy(t ir.Type, x string) string {
	switch {
	case t == ir.Bool:
		return x
	case ir.IsInteger(t):
		return "(" + x + " != 0)"
	case t == ir.String:
		return "((" + x + ")->byte_len != 0)"
	case t == ir.Value || isUnion(t):
		return "pts_value_truthy(" + x + ")"
	case t == ir.Number || t == ir.F32:
		return "pts_number_truthy(" + x + ")"
	}
	panic("cgen: no truthiness for " + t.String())
}

// temporary names a new C local for an intermediate value.
func (e *emitter) temporary() string {
	e.temporaries++
	return fmt.Sprintf("pts_tmp%d", e.temporaries)
}

// operands writes exprs as C. C leaves the evaluation order of operands and arguments
// unspecified; where that could change what JS's left-to-right order does, each is
// evaluated into a temporary first, and prelude declares them for ordered.
func (e *emitter) operands(exprs []ir.Expr) (args []string, prelude string) {
	args = make([]string, len(exprs))
	if !orderMatters(exprs) {
		for i, x := range exprs {
			args[i] = e.expr(x)
		}
		return args, ""
	}
	var declarations strings.Builder
	for i, x := range exprs {
		args[i] = e.temporary()
		fmt.Fprintf(&declarations, "%s%s = %s; ", e.declarator(x.Type()), args[i], e.expr(x))
	}
	return args, declarations.String()
}

// ordered wraps result, which uses the temporaries prelude declares, in a statement expression.
func ordered(prelude string, result string) string {
	if prelude == "" {
		return result
	}
	return "({ " + prelude + result + "; })"
}

// cOperators are the binary operators C spells like JS (for the operand types the IR allows).
var cOperators = map[ir.BinaryOp]string{
	ir.Add: "+", ir.Sub: "-", ir.Mul: "*", ir.Div: "/",
	ir.Lt: "<", ir.Le: "<=", ir.Gt: ">", ir.Ge: ">=", ir.Eq: "==", ir.Ne: "!=",
}

// integerOperations name the runtime's wrapping and checked integer operations: pts_<type>_<name>.
var integerOperations = map[ir.BinaryOp]string{ir.Add: "add", ir.Sub: "sub", ir.Mul: "mul", ir.Div: "div", ir.Rem: "rem", ir.Pow: "pow",
	ir.Shl: "shl", ir.Shr: "shr", ir.UShr: "ushr"}

// bitwiseOperators are C's spelling of the bitwise operators on integer types.
var bitwiseOperators = map[ir.BinaryOp]string{ir.BitAnd: "&", ir.BitOr: "|", ir.BitXor: "^"}

// numberBitwise name the runtime's int32 bitwise operations on numbers: pts_number_<name>.
var numberBitwise = map[ir.BinaryOp]string{ir.BitAnd: "bitand", ir.BitOr: "bitor", ir.BitXor: "bitxor", ir.Shl: "shl", ir.Shr: "shr", ir.UShr: "ushr"}

// stringComparisons write string ordering through pts_string_compare's sign.
var stringComparisons = map[ir.BinaryOp]string{ir.Lt: "<", ir.Le: "<=", ir.Gt: ">", ir.Ge: ">="}

func (e *emitter) binary(b *ir.Binary) string {
	args, prelude := e.operands([]ir.Expr{b.X, b.Y})
	return ordered(prelude, e.binaryOf(b.Op, b.X.Type(), args[0], args[1]))
}

// binaryOf writes op applied to x and y, C expressions of type operandType.
func (e *emitter) binaryOf(op ir.BinaryOp, operandType ir.Type, x string, y string) string {
	if operandType == ir.String {
		switch op {
		case ir.Concat:
			return "pts_string_concat(" + x + ", " + y + ")"
		case ir.Eq:
			return "pts_string_equals(" + x + ", " + y + ")"
		case ir.Ne:
			return "(!pts_string_equals(" + x + ", " + y + "))"
		default:
			return "(pts_string_compare(" + x + ", " + y + ") " + stringComparisons[op] + " 0)"
		}
	}
	if name, ok := integerOperations[op]; ok && ir.IsInteger(operandType) {
		return fmt.Sprintf("pts_%s_%s(%s, %s)", operandType, name, x, y)
	}
	if c, ok := bitwiseOperators[op]; ok && ir.IsInteger(operandType) {
		return "((" + cType(operandType) + ")(" + x + " " + c + " " + y + "))"
	}
	if name, ok := numberBitwise[op]; ok {
		if operandType == ir.F32 {
			return "((float)pts_number_" + name + "(" + x + ", " + y + "))"
		}
		return "pts_number_" + name + "(" + x + ", " + y + ")"
	}
	prefix := "pts_number"
	if operandType == ir.F32 {
		prefix = "pts_f32"
	}
	switch op {
	case ir.Rem:
		return prefix + "_rem(" + x + ", " + y + ")"
	case ir.Pow:
		return prefix + "_pow(" + x + ", " + y + ")"
	}
	return "(" + x + " " + cOperators[op] + " " + y + ")"
}

// toString writes JS's ToString of a primitive. The words true, false, null and
// undefined are string constants, interned by collectStrings.
func (e *emitter) toString(value ir.Expr) string {
	x := e.expr(value)
	switch value.Type() {
	case ir.Bool:
		return "(" + x + " ? " + e.strings["true"] + " : " + e.strings["false"] + ")"
	case ir.Null:
		return e.strings["null"]
	case ir.Undefined:
		return e.strings["undefined"]
	case ir.I64:
		return "pts_string_from_i64(" + x + ")"
	case ir.U64:
		return "pts_string_from_u64(" + x + ")"
	case ir.Value:
		return "pts_value_to_string(" + x + ")"
	}
	if isUnion(value.Type()) {
		return "pts_value_to_string(" + x + ")"
	}
	return "pts_number_to_string(" + x + ")" // numbers, f32 and the narrower integers, exact as doubles
}

// convert writes a numeric conversion. Float to integer saturates (a runtime helper);
// everything else is a C cast, which wraps between integer types as `as` requires.
func (e *emitter) convert(c *ir.Convert) string {
	x := e.expr(c.X)
	if ir.IsInteger(c.To) && !ir.IsInteger(c.X.Type()) {
		return "pts_number_to_" + c.To.String() + "(" + x + ")"
	}
	return "((" + cType(c.To) + ")" + x + ")"
}

// intLiteral writes an integer constant in its C type.
func intLiteral(c *ir.IntConst) string {
	switch c.Ty {
	case ir.I64:
		if int64(c.Bits) == math.MinInt64 {
			return "INT64_MIN"
		}
		return fmt.Sprintf("INT64_C(%d)", int64(c.Bits))
	case ir.U64:
		return fmt.Sprintf("UINT64_C(%d)", c.Bits)
	case ir.I8, ir.I16, ir.I32:
		return fmt.Sprintf("((%s)%d)", cType(c.Ty), int64(c.Bits))
	default:
		return fmt.Sprintf("((%s)%du)", cType(c.Ty), c.Bits)
	}
}

func (e *emitter) box(value ir.Expr) string {
	return boxAs(e.expr(value), value.Type())
}

// boxAs is inner, a C expression of type t, as a dynamic value.
func boxAs(inner string, t ir.Type) string {
	switch t {
	case ir.Number:
		return "pts_value_from_number(" + inner + ")"
	case ir.Bool:
		return "pts_value_from_bool(" + inner + ")"
	case ir.String:
		return "pts_value_from_string(" + inner + ")"
	case ir.Undefined, ir.Null, ir.Value:
		return inner // already a pts_value
	case ir.I64:
		return "pts_value_from_i64(" + inner + ")"
	case ir.U64:
		return "pts_value_from_u64(" + inner + ")"
	case ir.I8, ir.I16, ir.I32, ir.U8, ir.U16, ir.U32, ir.F32:
		return "pts_value_from_number(" + inner + ")" // exact as a double
	}
	switch t.(type) {
	case *ir.Class, *ir.Interface, *ir.Array, *ir.FuncType, *ir.MapType:
		return "pts_value_from_object(" + inner + ")"
	case *ir.Union:
		return inner // already a pts_value
	default:
		panic("cgen: cannot box " + t.String())
	}
}

// builtinFunctions are the runtime functions that take a builtin's arguments as they are.
var builtinFunctions = map[*ir.Builtin]string{
	ir.MathSqrt:                    "__builtin_sqrt",
	ir.MathAbs:                     "__builtin_fabs",
	ir.MathFloor:                   "__builtin_floor",
	ir.MathCeil:                    "__builtin_ceil",
	ir.MathTrunc:                   "__builtin_trunc",
	ir.MathRound:                   "pts_math_round",
	ir.MathSign:                    "pts_math_sign",
	ir.MathMax:                     "pts_math_max",
	ir.MathMin:                     "pts_math_min",
	ir.MathAcos:                    "pts_ieee754_acos",
	ir.MathAcosh:                   "pts_ieee754_acosh",
	ir.MathAsin:                    "pts_ieee754_asin",
	ir.MathAsinh:                   "pts_ieee754_asinh",
	ir.MathAtan:                    "pts_ieee754_atan",
	ir.MathAtanh:                   "pts_ieee754_atanh",
	ir.MathAtan2:                   "pts_ieee754_atan2",
	ir.MathCbrt:                    "pts_ieee754_cbrt",
	ir.MathClz32:                   "pts_math_clz32",
	ir.MathCos:                     "pts_ieee754_cos",
	ir.MathCosh:                    "pts_ieee754_cosh",
	ir.MathExp:                     "pts_ieee754_exp",
	ir.MathExpm1:                   "pts_ieee754_expm1",
	ir.MathFround:                  "pts_math_fround",
	ir.MathImul:                    "pts_math_imul",
	ir.MathLog:                     "pts_ieee754_log",
	ir.MathLog10:                   "pts_ieee754_log10",
	ir.MathLog1p:                   "pts_ieee754_log1p",
	ir.MathLog2:                    "pts_ieee754_log2",
	ir.MathPow:                     "pts_number_pow",
	ir.MathRandom:                  "pts_math_random",
	ir.MathSin:                     "pts_ieee754_sin",
	ir.MathSinh:                    "pts_ieee754_sinh",
	ir.MathTan:                     "pts_ieee754_tan",
	ir.MathTanh:                    "pts_ieee754_tanh",
	ir.MathMaxOf:                   "pts_math_max_of",
	ir.MathMinOf:                   "pts_math_min_of",
	ir.MathHypot:                   "pts_math_hypot",
	ir.StringSplit:                 "pts_string_split",
	ir.ArrayFromString:             "pts_array_from_string",
	ir.StringElement:               "pts_string_element",
	ir.StringElementChecked:        "pts_string_element_checked",
	ir.StringElementEquals:         "pts_string_element_equals",
	ir.StringCharCodeAt:            "pts_string_char_code_at",
	ir.StringSlice:                 "pts_string_slice",
	ir.StringSubstring:             "pts_string_substring",
	ir.StringIndexOf:               "pts_string_index_of",
	ir.StringIncludes:              "pts_string_includes",
	ir.StringStartsWith:            "pts_string_starts_with",
	ir.StringEndsWith:              "pts_string_ends_with",
	ir.StringToUpperCase:           "pts_string_to_upper_case",
	ir.StringToLowerCase:           "pts_string_to_lower_case",
	ir.StringTrim:                  "pts_string_trim",
	ir.StringTrimStart:             "pts_string_trim_start",
	ir.StringTrimEnd:               "pts_string_trim_end",
	ir.StringPadStart:              "pts_string_pad_start",
	ir.StringPadEnd:                "pts_string_pad_end",
	ir.StringRepeat:                "pts_string_repeat",
	ir.StringAt:                    "pts_string_at",
	ir.StringCharAt:                "pts_string_char_at",
	ir.StringCodePointAt:           "pts_string_code_point_at",
	ir.StringLastIndexOf:           "pts_string_last_index_of",
	ir.StringSubstr:                "pts_string_substr",
	ir.StringReplace:               "pts_string_replace",
	ir.StringReplaceAll:            "pts_string_replace_all",
	ir.StringIsWellFormed:          "pts_string_is_well_formed",
	ir.StringToWellFormed:          "pts_string_to_well_formed",
	ir.StringLocaleCompare:         "pts_string_locale_compare",
	ir.StringFromCharCode:          "pts_string_from_char_codes",
	ir.StringFromCodePoint:         "pts_string_from_code_points",
	ir.URIEncode:                   "pts_uri_encode",
	ir.URIDecode:                   "pts_uri_decode",
	ir.NumberToFixed:               "pts_number_to_fixed",
	ir.NumberToExponential:         "pts_number_to_exponential",
	ir.NumberToExponentialShortest: "pts_number_to_exponential_shortest",
	ir.NumberToPrecision:           "pts_number_to_precision",
	ir.NumberToStringRadix:         "pts_number_to_string_radix",
	ir.NumberToLocaleString:        "pts_number_to_locale_string",
	ir.I64ToStringRadix:            "pts_i64_to_string_radix",
	ir.U64ToStringRadix:            "pts_u64_to_string_radix",
	ir.NumberIsInteger:             "pts_number_is_integer",
	ir.NumberIsSafeInteger:         "pts_number_is_safe_integer",
	ir.NumberIsFinite:              "pts_number_is_finite",
	ir.NumberIsNaN:                 "pts_number_is_nan",
	ir.ValueIsInteger:              "pts_value_is_integer",
	ir.ValueIsSafeInteger:          "pts_value_is_safe_integer",
	ir.ValueIsFinite:               "pts_value_is_finite",
	ir.ValueIsNaN:                  "pts_value_is_nan",
	ir.ParseInt:                    "pts_parse_int",
	ir.ParseFloat:                  "pts_parse_float",
	ir.ValueTypeOf:                 "pts_value_typeof",
	ir.ValueConstructorName:        "pts_value_constructor_name",
	ir.ValueStrictEquals:           "pts_value_strict_equals",
	ir.ValueLooseEquals:            "pts_value_loose_equals",
	ir.DynamicGet:                  "pts_dynamic_get",
	ir.DynamicSet:                  "pts_dynamic_set",
	ir.DynamicHas:                  "pts_dynamic_has",
	ir.ValueIsArray:                "pts_value_is_array",
	ir.ValueKeys:                   "pts_value_keys",
	ir.ValueValues:                 "pts_value_values",
	ir.ValueEntries:                "pts_value_entries",
	ir.ValueHasOwn:                 "pts_value_has_own",
	ir.TupleLength:                 "pts_value_tuple_length",
	ir.FunctionName:                "pts_value_function_name",
	ir.FunctionLength:              "pts_value_function_length",
	ir.ValueKeyArray:               "pts_value_key_array",
	ir.ObjectAssign:                "pts_object_assign",
	ir.ValueSameValue:              "pts_value_same_value",
	ir.ValueFreeze:                 "pts_value_freeze",
	ir.IteratorOpen:                "pts_iterator_open",
	ir.IteratorResume:              "pts_iterator_resume",
	ir.IteratorValue:               "pts_iterator_value",
	ir.IteratorClose:               "pts_iterator_close",
	ir.IteratorTake:                "pts_iterator_take",
	ir.IteratorGet:                 "pts_iterator_get",
	ir.IteratorOf:                  "pts_iterator_of",
	ir.MapIteratorOf:               "pts_map_iterator_new",
	ir.ValueIsFrozen:               "pts_value_is_frozen",
	ir.ValueIterable:               "pts_value_iterable",
	ir.ValueIterableLength:         "pts_value_iterable_length",
	ir.ValueIterableElement:        "pts_value_iterable_element",
	ir.ValueToArray:                "pts_value_to_array",
	ir.JSONParse:                   "pts_json_parse",
	ir.JSONParseRevived:            "pts_json_parse_revived",
	ir.JSONStringify:               "pts_json_stringify",
	ir.ValueAdd:                    "pts_value_add",
	ir.ValueToNumber:               "pts_value_to_number",
	ir.ValueLess:                   "pts_value_less",
	ir.ValueLessEqual:              "pts_value_less_equal",
	ir.ThrowError:                  "pts_throw_error",
}

func (e *emitter) callBuiltin(call *ir.CallBuiltin) string {
	args, prelude := e.operands(call.Args)
	if spread := spreadArgs(call); spread != nil {
		fixed := len(args) - len(spread) // a dynamic call's callee, key and text
		spreading := fmt.Sprintf("%d, (pts_value[]){%s}, (bool[]){%s}", len(spread), strings.Join(args[fixed:], ", "), strings.Join(spread, ", "))
		switch call.Builtin {
		case ir.DynamicCall:
			return ordered(prelude, fmt.Sprintf("pts_dynamic_call_spread(%s, %s, %s)", args[0], args[1], spreading))
		case ir.DynamicCallMethod:
			return ordered(prelude, fmt.Sprintf("pts_dynamic_call_method_spread(%s, %s, %s, %s)", args[0], args[1], args[2], spreading))
		}
		if call.Builtin == ir.ConsoleLog {
			return ordered(prelude, "pts_console_log_spread("+spreading+")")
		}
		return ordered(prelude, fmt.Sprintf("pts_console(%s, %s)", consoleMethods[call.Builtin], spreading))
	}
	return ordered(prelude, builtinCall(call.Builtin, args))
}

// consoleMethods names the runtime's pts_console_method of each console builtin but log,
// which has a function of its own (D-088).
var consoleMethods = map[*ir.Builtin]string{
	ir.ConsoleError: "PTS_CONSOLE_ERROR", ir.ConsoleGroup: "PTS_CONSOLE_GROUP", ir.ConsoleAssert: "PTS_CONSOLE_ASSERT",
	ir.ConsoleTrace: "PTS_CONSOLE_TRACE", ir.ConsoleCount: "PTS_CONSOLE_COUNT", ir.ConsoleCountReset: "PTS_CONSOLE_COUNT_RESET",
	ir.ConsoleTime: "PTS_CONSOLE_TIME", ir.ConsoleTimeEnd: "PTS_CONSOLE_TIME_END", ir.ConsoleTimeLog: "PTS_CONSOLE_TIME_LOG",
}

// spreadArgs says, for a console method or a dynamic call with a spread argument (`...xs`),
// which of its arguments are spread ("true") and which aren't; nil for any other call.
func spreadArgs(call *ir.CallBuiltin) []string {
	fixed := map[*ir.Builtin]int{ir.DynamicCall: 2, ir.DynamicCallMethod: 3}
	n, ok := fixed[call.Builtin]
	ok = ok || ir.ConsoleMethods[call.Builtin]
	if !ok || !slices.ContainsFunc(call.Args, isSpreadArg) {
		return nil
	}
	spread := make([]string, len(call.Args)-n)
	for i, arg := range call.Args[n:] {
		spread[i] = strconv.FormatBool(isSpreadArg(arg))
	}
	return spread
}

func isSpreadArg(x ir.Expr) bool {
	_, ok := x.(*ir.SpreadArg)
	return ok
}

func builtinCall(builtin *ir.Builtin, args []string) string {
	if method, ok := consoleMethods[builtin]; ok {
		if len(args) == 0 {
			return fmt.Sprintf("pts_console(%s, 0, NULL, NULL)", method)
		}
		return fmt.Sprintf("pts_console(%s, %d, (pts_value[]){%s}, NULL)", method, len(args), strings.Join(args, ", "))
	}
	switch builtin {
	case ir.ConsoleLog:
		if len(args) == 0 {
			return "pts_console_log(0, NULL)"
		}
		return fmt.Sprintf("pts_console_log(%d, (pts_value[]){%s})", len(args), strings.Join(args, ", "))
	case ir.ConsoleGroupEnd:
		return "pts_console_group_end()"
	case ir.ConsoleDir:
		return fmt.Sprintf("pts_console_dir(%s)", strings.Join(args, ", "))
	case ir.ConsoleTable:
		return fmt.Sprintf("pts_console_table(%s)", strings.Join(args, ", "))
	case ir.StringLength:
		return "((double)(" + args[0] + ")->utf16_len)"
	case ir.DynamicCall:
		return fmt.Sprintf("pts_dynamic_call(%s, %s, %s)", args[0], args[1], dynamicCallArguments(args[2:]))
	case ir.DynamicCallMethod:
		return fmt.Sprintf("pts_dynamic_call_method(%s, %s, %s, %s)", args[0], args[1], args[2], dynamicCallArguments(args[3:]))
	}
	function, ok := builtinFunctions[builtin]
	if !ok {
		panic("cgen: unexpected builtin " + builtin.Name)
	}
	return function + "(" + strings.Join(args, ", ") + ")"
}

// numberLiteral writes a double as a C constant that parses back to exactly the same value.
func numberLiteral(value float64) string {
	switch {
	case math.IsNaN(value):
		return `__builtin_nan("")`
	case math.IsInf(value, 1):
		return "__builtin_inf()"
	case math.IsInf(value, -1):
		return "(-__builtin_inf())"
	case math.Signbit(value):
		return "(-" + numberLiteral(-value) + ")"
	}
	text := strconv.FormatFloat(value, 'g', -1, 64)
	if !strings.ContainsAny(text, ".e") {
		text += ".0"
	}
	return text
}

// ---------------------------------------------------------------- types

func cType(t ir.Type) string {
	switch t {
	case ir.Number:
		return "double"
	case ir.Bool:
		return "bool"
	case ir.String:
		return "pts_string *"
	case ir.Undefined, ir.Null, ir.Value:
		return "pts_value"
	case ir.Void:
		return "void"
	case ir.I8:
		return "int8_t"
	case ir.I16:
		return "int16_t"
	case ir.I32:
		return "int32_t"
	case ir.I64:
		return "int64_t"
	case ir.U8:
		return "uint8_t"
	case ir.U16:
		return "uint16_t"
	case ir.U32:
		return "uint32_t"
	case ir.U64:
		return "uint64_t"
	case ir.F32:
		return "float"
	case ir.MapIterator:
		return "pts_map_iterator"
	case ir.Given:
		return "bool"
	default:
		panic("cgen: no C type for " + t.String())
	}
}

// declarator is the C type followed by what separates it from a name: "double " but
// "pts_string *"; a class instance is a pointer to its struct.
func (e *emitter) declarator(t ir.Type) string {
	switch t := t.(type) {
	case *ir.Class:
		return "pts_class_" + e.classNames[t] + " *"
	case *ir.Interface:
		return "pts_object *" // any object that provides the interface's members
	case *ir.Array:
		return "pts_array *"
	case *ir.FuncType:
		return "pts_closure *"
	case *ir.MapType:
		return "pts_map *"
	case *ir.Rest:
		return "pts_array *"
	case *ir.Union:
		return "pts_value "
	}
	c := cType(t)
	if strings.HasSuffix(c, "*") {
		return c
	}
	return c + " "
}

// ---------------------------------------------------------------- string constants

// toStringWords are the constants ToString of each type needs.
var toStringWords = map[ir.Type][]string{ir.Bool: {"true", "false"}, ir.Null: {"null"}, ir.Undefined: {"undefined"}}

func (e *emitter) collectStrings(program *ir.Program) {
	for _, f := range program.Funcs {
		ir.Walk(f.Body.Stmts, func(stmt ir.Stmt) {
			if decl, ok := stmt.(*ir.LocalDecl); ok && decl.Init == nil && decl.Local.Type == ir.String {
				e.intern("") // the zero value of `let s: string;`
			}
		}, func(expr ir.Expr) {
			switch x := expr.(type) {
			case *ir.StringConst:
				e.intern(x.Value)
			case *ir.LeftOut:
				if x.T == ir.String {
					e.intern("")
				}
			case *ir.ToString:
				for _, word := range toStringWords[x.X.Type()] {
					e.intern(word)
				}
			}
		})
	}
}

func (e *emitter) intern(value string) {
	if _, seen := e.strings[value]; !seen {
		e.strings[value] = fmt.Sprintf("pts_str_%d", len(e.stringOrder)+1)
		e.stringOrder = append(e.stringOrder, value)
	}
}

func staticString(name string, value string) string {
	flags := "0"
	if isASCII(value) {
		flags = "PTS_STRING_ASCII"
	}
	return fmt.Sprintf("PTS_STATIC_STRING(%s, %s, %d, %d, %s);", name, flags, len(value), utf16Length(value), cString(value))
}

// ---------------------------------------------------------------- output

// out writes C lines and tracks the source position the C compiler will attribute to the next one.
type out struct {
	strings.Builder
	nextFile string // file and line the next output line has, per the last #line; 0 = none yet
	nextLine int
}

func (o *out) line(depth int, format string, args ...any) {
	o.WriteString(strings.Repeat("  ", depth))
	fmt.Fprintf(&o.Builder, format, args...)
	o.WriteByte('\n')
	if o.nextLine > 0 {
		o.nextLine++
	}
}

// at makes the next output line count as pos, emitting #line only when needed.
func (o *out) at(pos ir.Pos) {
	if pos.Line == 0 || (pos.File == o.nextFile && pos.Line == o.nextLine) {
		return
	}
	fmt.Fprintf(&o.Builder, "#line %d %s\n", pos.Line, cString(pos.File))
	o.nextFile, o.nextLine = pos.File, pos.Line
}
