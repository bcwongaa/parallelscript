package cgen

import (
	"fmt"
	"slices"
	"strings"

	"github.com/microsoft/typescript-go/pts/ir"
)

// Generators (D-109). A generator function becomes three C definitions: a frame struct
// that starts with a pts_iterator and holds every local of its body (its parameters, the
// locals its statements declare, and the closure it runs with); a starter, the function
// calls call, which makes the frame and stores the arguments; and a step function, the
// body as a state machine that pts_iterator_resume runs. A yield stores its value and the
// number of its resume point in the frame and returns; the step function starts with a
// switch that jumps to the resume point the frame names. C allows a goto into loops and
// blocks, so the body keeps its structure; only try blocks need care, since entering one
// pushes a handler on the thread's chain: a resume point inside one jumps to the try's
// entry, which pushes the handler again and then jumps on, one try at a time.

// generatorFrame is what the emitter knows of a generator function's frame.
type generatorFrame struct {
	name    string                  // the frame struct: pts_frame_<suffix>
	step    string                  // the step function: pts_step_<suffix>
	locals  []*ir.Local             // the frame's members, besides the closure
	members map[*ir.Local]string    // their names in the struct
	yields  map[*ir.Yield]int       // each yield's resume point, from 1
	paths   map[*ir.Yield][]*ir.Try // the try statements whose bodies hold each yield, outermost first
	entries map[*ir.Try]string      // the entry label of each try statement that holds a yield
	order   []*ir.Yield             // the yields in the order of their resume points
}

// collectGenerators lays out the frame of each generator function of the program.
func (e *emitter) collectGenerators(program *ir.Program) {
	e.frames = map[*ir.Func]*generatorFrame{}
	for _, f := range program.Funcs {
		if f.Generator {
			e.frames[f] = e.layOutFrame(f)
		}
	}
}

func (e *emitter) layOutFrame(f *ir.Func) *generatorFrame {
	suffix := strings.TrimPrefix(e.funcNames[f], "pts_")
	frame := &generatorFrame{name: "pts_frame_" + suffix, step: "pts_step_" + suffix, members: map[*ir.Local]string{},
		yields: map[*ir.Yield]int{}, paths: map[*ir.Yield][]*ir.Try{}, entries: map[*ir.Try]string{}}
	used := map[string]bool{"header": true, "pts_env": true}
	add := func(local *ir.Local) {
		if _, ok := frame.members[local]; !ok {
			frame.members[local] = unique(used, cIdentifier(local.Name))
			frame.locals = append(frame.locals, local)
		}
	}
	for _, param := range f.Params {
		add(param)
	}
	var visit func(stmts []ir.Stmt, tries []*ir.Try)
	visit = func(stmts []ir.Stmt, tries []*ir.Try) {
		for _, stmt := range stmts {
			switch s := stmt.(type) {
			case *ir.Block:
				visit(s.Stmts, tries)
			case *ir.LocalDecl:
				if !s.Local.Global { // a module-scope binding that functions use is a C global (an async entry module, D-113)
					add(s.Local)
				}
			case *ir.If:
				visit([]ir.Stmt{s.Then}, tries)
				if s.Else != nil {
					visit([]ir.Stmt{s.Else}, tries)
				}
			case *ir.Loop:
				visit(s.Body.Stmts, tries)
			case *ir.Switch:
				for _, c := range s.Cases {
					visit(c.Body, tries)
				}
			case *ir.LabeledBlock:
				visit(s.Body.Stmts, tries)
			case *ir.Try:
				add(s.Exception)
				visit(s.Body.Stmts, append(append([]*ir.Try(nil), tries...), s))
				visit(s.Catch.Stmts, tries)
			case *ir.Yield:
				frame.order = append(frame.order, s)
				frame.yields[s] = len(frame.order)
				frame.paths[s] = tries
				for _, try := range tries {
					if frame.entries[try] == "" {
						frame.entries[try] = fmt.Sprintf("pts_enter_try_%d", len(frame.entries)+1)
					}
				}
			}
		}
	}
	visit(f.Body.Stmts, nil)
	return frame
}

// frameStructs writes the frame struct of each generator function.
func (e *emitter) frameStructs(program *ir.Program) {
	for _, f := range program.Funcs {
		frame := e.frames[f]
		if frame == nil {
			continue
		}
		e.line(0, "typedef struct %s {", frame.name)
		e.line(1, "pts_iterator header;")
		if f.Closure {
			e.line(1, "pts_closure *pts_env;")
		}
		for _, local := range frame.locals {
			declarator := e.declarator(local.Type)
			if local.Cell {
				declarator += "*"
			}
			e.line(1, "%s%s;", declarator, frame.members[local])
		}
		e.line(0, "} %s;", frame.name)
		e.line(0, "static bool %s(pts_iterator *);", frame.step)
		e.line(0, "")
	}
}

// generatorFunction writes a generator function's starter and step function.
func (e *emitter) generatorFunction(f *ir.Func) {
	frame := e.frames[f]
	e.startFunction(f)
	paramNames := make([]string, len(f.Params))
	for i, param := range f.Params {
		paramNames[i] = unique(e.usedNames, "pts_arg_"+cIdentifier(param.Name))
	}
	if receiver := e.receiverClass(f); receiver != nil && receiver != f.Params[0].Type {
		paramNames[0] = "pts_this"
	}
	e.line(0, "")
	e.source = f.Pos // a function without a position has none, not the previous one's (D-179)
	e.at(f.Pos)
	e.line(0, "static %s {", e.signature(f, paramNames))
	e.line(1, "%s *pts_frame = (%s *)pts_generator_new(sizeof(%s), %s);", frame.name, frame.name, frame.name, frame.step)
	if f.Closure {
		e.line(1, "pts_frame->pts_env = pts_env;")
	}
	for i, param := range f.Params {
		member := "pts_frame->" + frame.members[param]
		if param.Cell {
			e.line(1, "%s = %s;", member, e.cellAllocation(param))
			member = "*" + member
		}
		value := paramNames[i]
		if i == 0 && paramNames[0] == "pts_this" {
			value = "(" + strings.TrimSuffix(e.declarator(param.Type), " ") + ")pts_this"
		}
		e.line(1, "%s = %s;", member, value)
	}
	if f.Prologue > 0 {
		e.parameterPrologue(f, frame)
	}
	switch {
	case f.Async:
		e.line(1, "return pts_async_start(&pts_frame->header);") // runs the body until its first await (D-112)
	case f.AsyncGenerator:
		e.line(1, "return pts_async_generator_new(&pts_frame->header);") // D-115
	default:
		e.line(1, "return pts_value_from_object(pts_frame);")
	}
	e.line(0, "}")

	e.startFunction(f)
	e.frame = frame
	defer func() { e.frame = nil }()
	for local, member := range frame.members {
		e.locals[local] = "pts_frame->" + member
		e.usedNames[member] = true
	}
	e.line(0, "")
	e.source = f.Pos // a function without a position has none, not the previous one's (D-179)
	e.at(f.Pos)
	e.line(0, "static bool %s(pts_iterator *pts_generator) {", frame.step)
	e.line(1, "%s *pts_frame = (%s *)pts_generator;", frame.name, frame.name)
	if f.Closure {
		e.line(1, "pts_closure *pts_env = pts_frame->pts_env;")
		e.capturePrologue(f)
	}
	if mayRecurse(f) {
		e.line(1, "pts_stack_check();")
	}
	e.dispatch(1, nil)
	e.line(1, "pts_frame->header.state = PTS_GENERATOR_RUNNING;")
	e.scope(f.Body.Stmts[f.Prologue:], 1)
	if !endsWithReturn(f.Body.Stmts) {
		e.finishGenerator(1, "PTS_VALUE_UNDEFINED")
	}
	e.line(0, "}")
}

// parameterPrologue writes, in a generator's starter, the statements that bind its
// parameters (f.Prologue): defaults and patterns run at the call, on the frame's members,
// before the generator object is made, as JS binds them; a throw leaves the call.
func (e *emitter) parameterPrologue(f *ir.Func, frame *generatorFrame) {
	e.frame = frame
	defer func() { e.frame = nil }()
	for local, member := range frame.members {
		e.locals[local] = "pts_frame->" + member
		e.usedNames[member] = true
	}
	if f.Closure {
		e.capturePrologue(f)
	}
	if mayRecurse(f) {
		e.line(1, "pts_stack_check();") // a default may call the generator again (review round 1, R1-F4)
	}
	e.scope(f.Body.Stmts[:f.Prologue], 1)
}

// dispatch writes the switch that jumps to a resume point: from the top of the step
// function (inside nil), or from the start of the body of the try statement inside, to
// the resume point itself or to the entry of the next try statement around it.
func (e *emitter) dispatch(depth int, inside *ir.Try) {
	var cases []string
	for _, yield := range e.frame.order {
		path := e.frame.paths[yield]
		next := 0
		if inside != nil {
			i := indexOf(path, inside)
			if i < 0 {
				continue
			}
			next = i + 1
		}
		target := resumeLabel(e.frame.yields[yield])
		if next < len(path) {
			target = e.frame.entries[path[next]]
		}
		e.gotoTargets[target] = true
		cases = append(cases, fmt.Sprintf("case %d: goto %s;", e.frame.yields[yield], target))
	}
	if len(cases) == 0 {
		return
	}
	e.line(depth, "switch (pts_frame->header.state) {")
	for _, c := range cases {
		e.line(depth, "%s", c)
	}
	e.line(depth, "default: break;")
	e.line(depth, "}")
}

func indexOf(tries []*ir.Try, try *ir.Try) int {
	for i, t := range tries {
		if t == try {
			return i
		}
	}
	return -1
}

func resumeLabel(point int) string {
	return fmt.Sprintf("pts_resume_%d", point)
}

// yieldStatement writes a yield: the value and the resume point stored in the frame, the
// try blocks around it left, a return from the step function, and the resume point.
func (e *emitter) yieldStatement(s *ir.Yield, depth int) {
	point := e.frame.yields[s]
	if s.Delegate != nil {
		e.line(depth, "pts_generator_delegate(&pts_frame->header, %s);", e.expr(s.Delegate))
	} else {
		e.line(depth, "pts_frame->header.value = %s;", e.expr(s.Value))
	}
	if e.current.AsyncGenerator {
		e.line(depth, "pts_frame->header.awaiting = %t;", s.Await) // D-115
	}
	e.line(depth, "pts_frame->header.state = %d;", point)
	if len(e.tries) > 0 {
		e.line(depth, "pts_try_leave(&%s);", e.tries[0])
	}
	e.line(depth, "return false;")
	e.line(depth, "%s: pts_frame->header.state = PTS_GENERATOR_RUNNING;", resumeLabel(point))
}

// finishGenerator writes the end of a generator's body: it returns value (C).
func (e *emitter) finishGenerator(depth int, value string) {
	e.line(depth, "pts_frame->header.value = %s;", value)
	e.line(depth, "pts_frame->header.state = PTS_GENERATOR_DONE;")
	e.line(depth, "return true;")
}

// generatorReturn writes a return in a generator's body: its value is the generator's
// return value, and the try blocks around it are left once it is computed.
func (e *emitter) generatorReturn(s *ir.Return, depth int) {
	value := "PTS_VALUE_UNDEFINED"
	if s.Value != nil {
		value = e.expr(s.Value)
	}
	e.line(depth, "{")
	e.line(depth+1, "pts_value pts_result = %s;", value)
	if len(e.tries) > 0 {
		e.line(depth+1, "pts_try_leave(&%s);", e.tries[0])
	}
	e.finishGenerator(depth+1, "pts_result")
	e.line(depth, "}")
}

// resumeModes are the C names of the ways a generator resumes.
var resumeModes = map[ir.ResumeMode]string{ir.ResumeNext: "PTS_RESUME_NEXT", ir.ResumeThrow: "PTS_RESUME_THROW", ir.ResumeReturn: "PTS_RESUME_RETURN"}

// frameLocal reports whether local lives in the frame of the generator being written.
func (e *emitter) frameLocal(local *ir.Local) bool {
	if e.frame == nil {
		return false
	}
	_, ok := e.frame.members[local]
	return ok
}

// implementsIteration reports whether objects of the program's may be iterated through
// the protocol (D-111) or awaited as thenables (D-112): a class or shape has a method or
// function field named next, [Symbol.iterator] or then, which the runtime calls through
// its dynamic entry.
func implementsIteration(program *ir.Program) bool {
	return hasMethodNamed(program, "next", "@@iterator", "then", "@@asyncIterator")
}

// definesConversions reports whether a class or shape of the program has a toString or
// valueOf method, which the runtime calls through its dynamic entry as OrdinaryToPrimitive
// does (D-142), whatever parameters and result it declares.
func definesConversions(program *ir.Program) bool {
	return hasMethodNamed(program, "toString", "valueOf")
}

// hasMethodNamed reports whether a class or shape of the program has a method, or a
// function field, of one of the names.
func hasMethodNamed(program *ir.Program, names ...string) bool {
	for _, class := range program.Classes {
		for _, method := range class.Methods {
			if method.Kind == ir.Plain && slices.Contains(names, method.Name) {
				return true
			}
		}
		for _, field := range class.Fields {
			if _, ok := field.Type.(*ir.FuncType); ok && slices.Contains(names, field.Name) {
				return true
			}
		}
	}
	return false
}
