// Package lower turns a type-checked TypeScript AST into ParallelScript IR. It is
// where the compiler decides what it can compile: anything it cannot lower yet
// becomes a diagnostic.
package lower

import (
	"context"
	"fmt"
	"math"
	"path/filepath"
	"slices"
	"strings"
	"unicode"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/internal/compiler"
	"github.com/microsoft/typescript-go/internal/jsnum"
	"github.com/microsoft/typescript-go/internal/scanner"
	"github.com/microsoft/typescript-go/internal/tspath"
	"github.com/microsoft/typescript-go/pts/check"
	"github.com/microsoft/typescript-go/pts/diagnostic"
	"github.com/microsoft/typescript-go/pts/frontend"
	"github.com/microsoft/typescript-go/pts/ir"
	"github.com/microsoft/typescript-go/pts/lib"
	"github.com/microsoft/typescript-go/pts/numeric"
)

// binaryOps maps TypeScript operator tokens to IR operators. == and != mean === and
// !== here because lowering only accepts operands of one primitive type.
var binaryOps = map[ast.Kind]ir.BinaryOp{
	ast.KindPlusToken:                              ir.Add,
	ast.KindMinusToken:                             ir.Sub,
	ast.KindAsteriskToken:                          ir.Mul,
	ast.KindSlashToken:                             ir.Div,
	ast.KindPercentToken:                           ir.Rem,
	ast.KindAsteriskAsteriskToken:                  ir.Pow,
	ast.KindLessThanToken:                          ir.Lt,
	ast.KindLessThanEqualsToken:                    ir.Le,
	ast.KindGreaterThanToken:                       ir.Gt,
	ast.KindGreaterThanEqualsToken:                 ir.Ge,
	ast.KindEqualsEqualsEqualsToken:                ir.Eq,
	ast.KindExclamationEqualsEqualsToken:           ir.Ne,
	ast.KindEqualsEqualsToken:                      ir.Eq,
	ast.KindExclamationEqualsToken:                 ir.Ne,
	ast.KindAmpersandToken:                         ir.BitAnd,
	ast.KindBarToken:                               ir.BitOr,
	ast.KindCaretToken:                             ir.BitXor,
	ast.KindLessThanLessThanToken:                  ir.Shl,
	ast.KindGreaterThanGreaterThanToken:            ir.Shr,
	ast.KindGreaterThanGreaterThanGreaterThanToken: ir.UShr,
}

// logicalOps maps && and || to IR operators.
var logicalOps = map[ast.Kind]ir.LogicalOp{ast.KindAmpersandAmpersandToken: ir.And, ast.KindBarBarToken: ir.Or}

// typeofWords are what typeof gives for each static type.
var typeofWords = map[ir.Type]string{ir.Bool: "boolean", ir.String: "string", ir.Undefined: "undefined", ir.Null: "object"}

// libConstants are the global constants of the ES library that lower to number constants.
var libConstants = map[string]float64{
	"NaN":      math.NaN(),
	"Infinity": math.Inf(1),
}

// builtins maps members of the prelude and of the ES library, by "Interface.member",
// to the runtime functions that implement them.
var builtins = map[string]*ir.Builtin{
	"Console.log":        ir.ConsoleLog,
	"String.length":      ir.StringLength,
	"String.charCodeAt":  ir.StringCharCodeAt,
	"String.slice":       ir.StringSlice,
	"String.substring":   ir.StringSubstring,
	"String.indexOf":     ir.StringIndexOf,
	"String.includes":    ir.StringIncludes,
	"String.startsWith":  ir.StringStartsWith,
	"String.endsWith":    ir.StringEndsWith,
	"String.toUpperCase": ir.StringToUpperCase,
	"String.toLowerCase": ir.StringToLowerCase,
	"String.trim":        ir.StringTrim,
	"String.trimStart":   ir.StringTrimStart,
	"String.trimEnd":     ir.StringTrimEnd,
	"String.trimLeft":    ir.StringTrimStart, // deprecated aliases
	"String.trimRight":   ir.StringTrimEnd,
	"String.padStart":    ir.StringPadStart,
	"String.padEnd":      ir.StringPadEnd,
	"String.repeat":      ir.StringRepeat,
	"String.split":       ir.StringSplit,
}

// properties are the builtins that are read rather than called.
var properties = map[*ir.Builtin]bool{ir.StringLength: true}

// Arguments for left-out index parameters. The runtime applies ToIntegerOrInfinity, so
// NaN gives 0, as undefined does in JS, and Infinity gives the length.
var (
	fromStart = &ir.NumberConst{Value: math.NaN()}
	toEnd     = &ir.NumberConst{Value: math.Inf(1)}
)

// leftOut gives, for each builtin method with optional parameters, the argument a call
// that leaves one out (or passes undefined) gets instead, per parameter after the
// receiver; nil marks a required parameter. The constant nodes are shared.
var leftOut = map[*ir.Builtin][]ir.Expr{
	ir.StringSlice:      {fromStart, toEnd},
	ir.StringSubstring:  {nil, toEnd},
	ir.StringIndexOf:    {nil, fromStart},
	ir.StringIncludes:   {nil, fromStart},
	ir.StringStartsWith: {nil, fromStart},
	ir.StringEndsWith:   {nil, toEnd},
	ir.StringPadStart:   {nil, &ir.StringConst{Value: " "}},
	ir.StringPadEnd:     {nil, &ir.StringConst{Value: " "}},
	ir.StringSplit:      {nil, toEnd},
}

// numericTypes maps the semantic pass's numeric types to IR types.
var numericTypes = map[check.Numeric]ir.Type{
	check.Number: ir.Number, check.F32: ir.F32,
	check.I8: ir.I8, check.I16: ir.I16, check.I32: ir.I32, check.I64: ir.I64,
	check.U8: ir.U8, check.U16: ir.U16, check.U32: ir.U32, check.U64: ir.U64,
}

// Program lowers a program that loaded and passed the semantic checks (info is what
// they learned). It reports every construct it cannot lower; the IR is complete only
// if there are none.
func Program(ctx context.Context, program *frontend.Program, info *check.Info) (*ir.Program, []diagnostic.Diagnostic) {
	c, release := program.TS.GetTypeCheckerForFile(ctx, program.Entry)
	defer release()
	l := &lowerer{program: program.TS, checker: c, info: info,
		locals: map[*ast.Symbol]*ir.Local{}, owners: map[*ir.Local]*ir.Func{}, moduleScope: map[*ir.Local]bool{}, funcs: map[*ast.Symbol]*ir.Func{},
		classes: map[*ast.Symbol]*ir.Class{}, classNodes: map[*ir.Class]*ast.Node{}, members: newClassMembers(),
		methodDeclarations: map[*ir.Method]*ast.Node{}, this: map[*ir.Func]*ir.Local{},
		statics: map[*ast.Node]*ir.Local{}, staticMethods: map[*ast.Node]*ir.Func{}, staticClasses: map[*ir.Func]*ir.Class{},
		shapesByType: map[*checker.Type]*ir.Class{}, shapesByKey: map[string]*ir.Class{},
		interfacesByType: map[*checker.Type]*ir.Interface{}, interfacesByKey: map[string]*ir.Interface{}, arrays: map[ir.Type]*ir.Array{ir.String: ir.StringArray}, arraysInProgress: map[*checker.Type]bool{},
		funcTypesInProgress: map[*checker.Type]bool{}, arrows: map[*ir.Func]bool{}, funcNodes: map[*ir.Func]*ast.Node{}, helpers: map[helperKey]*ir.Func{},
		mapsInProgress: map[*checker.Type]bool{}, chainObjects: map[*ast.Node]chainObject{}, chainRests: map[*ast.Node]bool{}, generics: map[*ast.Symbol]*genericFunc{},
		genericClasses: map[*ast.Symbol]*genericClass{}, genericStatics: map[*ast.Node]*genericFunc{}, classInstances: map[*ir.Class]*classInstance{}, membersStarted: map[*ir.Class]bool{},
		tuples: map[string]*ir.Class{}, tuplesInProgress: map[*checker.Type]bool{}, bindings: map[*ir.Func][]*parameterBinding{}, paramLocals: map[*ir.Func][]*ir.Local{}, rests: map[*ir.Array]*ir.Rest{}}
	result := &ir.Program{}
	for _, file := range program.Modules {
		module := newModule(file, file == program.Entry)
		l.funcNodes[module.Init] = file.AsNode()
		result.Modules = append(result.Modules, module)
		result.Funcs = append(result.Funcs, module.Init)
	}
	result.Main = result.Modules[len(result.Modules)-1].Init
	// Every class, function and module-scope binding exists before any code is lowered:
	// code may use them above their declarations, and in an import cycle before their
	// module runs. Classes come first, since any declaration's type may name one.
	for i, file := range program.Modules {
		l.function = result.Modules[i].Init
		l.declareClasses(file)
	}
	for _, file := range program.Modules {
		l.declareSuperclasses(file)
	}
	l.classList = l.orderedClasses(program.Modules)
	for i, file := range program.Modules {
		l.function = result.Modules[i].Init
		l.declareFunctions(file.AsNode(), "")
		l.declareModuleBindings(file)
	}
	l.declareClassMembers()
	for i, file := range program.Modules {
		l.function = result.Modules[i].Init
		l.function.Body = l.block(file.AsNode(), file.Statements.Nodes)
	}
	// Function bodies come last, when every module-scope binding they may use is known;
	// then the instances of generic functions that the bodies call, and that they call.
	for len(l.bodies) > 0 || len(l.instances) > 0 || len(l.classInstanceBodies) > 0 {
		switch {
		case len(l.bodies) > 0:
			body := l.bodies[0]
			l.bodies = l.bodies[1:]
			l.functionBody(body)
		case len(l.instances) > 0:
			inst := l.instances[0]
			l.instances = l.instances[1:]
			l.instanceBody(inst)
		default:
			inst := l.classInstanceBodies[0]
			l.classInstanceBodies = l.classInstanceBodies[1:]
			l.classInstanceBody(inst)
		}
	}
	result.Funcs = append(result.Funcs, l.lowered...)
	result.Classes = append(orderedByParent(append(l.classList, l.instanceClasses...)), l.shapeList...)
	result.Interfaces = l.interfaceList
	l.markAccessorProperties()
	l.checkRelaxedCasts(result.Funcs)
	markCells(result.Funcs)
	return result, l.errors
}

// newModule makes the IR module for a source file, with an empty function for its top-level code.
func newModule(file *ast.SourceFile, entry bool) *ir.Module {
	module := &ir.Module{}
	if !entry {
		base := tspath.GetBaseFileName(file.FileName())
		module.Name = strings.TrimSuffix(base, filepath.Ext(base))
		if module.Name == "" {
			module.Name = "module"
		}
	}
	module.Init = &ir.Func{Name: "main", Module: module, Pos: ir.Pos{File: file.FileName(), Line: 1}, Result: ir.Void}
	return module
}

type lowerer struct {
	program             *compiler.Program
	checker             *checker.Checker
	info                *check.Info
	locals              map[*ast.Symbol]*ir.Local        // nil for a declaration that failed to lower
	owners              map[*ir.Local]*ir.Func           // the function each local belongs to
	moduleScope         map[*ir.Local]bool               // the locals declared at module scope, which functions and other modules may use
	funcs               map[*ast.Symbol]*ir.Func         // every function declaration, made before any body is lowered; nil if it failed
	generics            map[*ast.Symbol]*genericFunc     // every generic function declaration, whose instances calls make
	genericStatics      map[*ast.Node]*genericFunc       // every generic static method, by declaration
	instances           []*instance                      // the instances whose bodies are still to be lowered
	subst               *substitution                    // the instance being lowered or declared, or nil
	genericClasses      map[*ast.Symbol]*genericClass    // every generic class declaration, whose instances types make; nil if it failed
	classInstances      map[*ir.Class]*classInstance     // the generic class instance each instance's class is
	instanceClasses     []*ir.Class                      // the classes of generic class instances, in the order they were made
	classInstanceBodies []*classInstance                 // the class instances whose constructors and methods are still to be lowered
	membersDeclared     bool                             // the classes' members are declared, so a new class instance declares its own at once
	membersStarted      map[*ir.Class]bool               // the classes whose members are declared, or being declared
	tuples              map[string]*ir.Class             // the shape of each tuple type met, by its element types
	rests               map[*ir.Array]*ir.Rest           // the rest parameter type of each array type
	tuplesInProgress    map[*checker.Type]bool           // the tuple types whose element types are being mapped
	bindings            map[*ir.Func][]*parameterBinding // the parameters each function's body starts by binding: those with defaults or patterns
	paramLocals         map[*ir.Func][]*ir.Local         // the local each function's body sees for each parameter it declares; nil for a pattern
	classes             map[*ast.Symbol]*ir.Class        // every class declaration, made before any code is lowered; nil if it failed
	classNodes          map[*ir.Class]*ast.Node          // the declaration of each class
	classList           []*ir.Class                      // the classes that didn't fail, superclasses first
	members             *classMembers                    // the members of those classes that aren't generic, by declaration
	declaring           *classMembers                    // where the members being declared go: members, or a generic class instance's own
	methodDeclarations  map[*ir.Method]*ast.Node         // the declaration of each method (for an accessor that implements an abstract field, the field's)
	statics             map[*ast.Node]*ir.Local          // the variable of each static field, by declaration; nil if it failed
	staticMethods       map[*ast.Node]*ir.Func           // the function of each static method, by declaration; nil if it failed
	staticClasses       map[*ir.Func]*ir.Class           // the class of each static method, which `this` stands for in it
	staticContext       *ir.Class                        // the class whose static initialisers are being lowered, or nil
	shapesByType        map[*checker.Type]*ir.Class      // the shape of each object type met; nil if it has none
	shapesByKey         map[string]*ir.Class             // the shapes, by their fields (membersKey)
	shapeList           []*ir.Class                      // the shapes, in the order they were made
	interfacesByType    map[*checker.Type]*ir.Interface  // the interface of each object type met; nil if it has none
	interfacesByKey     map[string]*ir.Interface         // the interfaces, by their members (membersKey)
	interfaceList       []*ir.Interface                  // the interfaces, in the order they were made
	relaxedCasts        []relaxedCast                    // conversions to interfaces whose objects may hold optional properties otherwise (D-066)
	arrays              map[ir.Type]*ir.Array            // the array type of each element type met
	arraysInProgress    map[*checker.Type]bool           // the array types whose element type is being mapped
	funcTypes           []*ir.FuncType                   // the function types met
	funcTypesInProgress map[*checker.Type]bool           // the function types whose parameter and result types are being mapped
	arrows              map[*ir.Func]bool                // the closures' functions that are arrow functions, whose `this` is their outer function's
	funcNodes           map[*ir.Func]*ast.Node           // the declaration, function expression or source file of each function
	helpers             map[helperKey]*ir.Func           // the helpers of the array methods that take callbacks; nil if one failed
	mapTypes            []*ir.MapType                    // the map and set types met
	unions              []*ir.Union                      // the union types met
	chainObjects        map[*ast.Node]chainObject        // the objects of the `?.` links whose chains are being lowered
	chainRests          map[*ast.Node]bool               // the optional chains being lowered past their `?.` test
	chainDepth          int                              // how many optional chains are being lowered past their tests
	mapsInProgress      map[*checker.Type]bool           // the map and set types whose key and value types are being mapped
	this                map[*ir.Func]*ir.Local           // the `this` parameter of each constructor and method
	lowered             []*ir.Func                       // the functions whose bodies are lowered
	bodies              []*ast.Node                      // function declarations whose bodies are still to be lowered
	function            *ir.Func                         // the function being lowered
	targets             []jumpTarget                     // the statements around the current one that break or continue can leave
	errors              []diagnostic.Diagnostic
}

// jumpTarget is a statement that break (and for a loop, continue) can target.
type jumpTarget struct {
	label *ir.Label
	loop  bool // a loop, which continue can target
	inner bool // a loop or switch, which an unlabelled break leaves
}

func (l *lowerer) pos(node *ast.Node) ir.Pos {
	file := ast.GetSourceFileOfNode(node)
	start := scanner.GetTokenPosOfNode(node, file, false)
	return ir.Pos{File: file.FileName(), Line: scanner.GetECMALineOfPosition(file, start) + 1}
}

func (l *lowerer) notSupportedYet(node *ast.Node, what string) {
	l.report(diagnostic.At(node, diagnostic.NotSupportedYet, "%s not supported yet", what))
}

func (l *lowerer) unsupported(node *ast.Node, format string, args ...any) {
	l.report(diagnostic.At(node, diagnostic.Unsupported, format, args...))
}

// report records an error once: each instance of a generic function lowers its body again (D-049).
func (l *lowerer) report(d diagnostic.Diagnostic) {
	if !slices.Contains(l.errors, d) {
		l.errors = append(l.errors, d)
	}
}

// ---------------------------------------------------------------- statements

func (l *lowerer) block(node *ast.Node, statements []*ast.Node) *ir.Block {
	block := &ir.Block{At: l.pos(node), Stmts: l.hoistedFunctions(statements)}
	for _, statement := range statements {
		block.Stmts = append(block.Stmts, l.statement(statement)...)
	}
	return block
}

// statement lowers one statement to zero or more IR statements.
func (l *lowerer) statement(node *ast.Node) []ir.Stmt {
	switch node.Kind {
	case ast.KindExpressionStatement:
		if isSuperCall(node.Expression()) {
			return l.superCallStatement(node)
		}
		if x := l.expression(node.Expression()); x != nil {
			return []ir.Stmt{&ir.ExprStmt{At: l.pos(node), X: x}}
		}
	case ast.KindVariableStatement:
		return l.variableStatement(node)
	case ast.KindBlock:
		return []ir.Stmt{l.block(node, node.Statements())}
	case ast.KindIfStatement:
		if s := l.ifStatement(node); s != nil {
			return []ir.Stmt{s}
		}
	case ast.KindWhileStatement, ast.KindDoStatement, ast.KindForStatement, ast.KindForOfStatement, ast.KindForInStatement, ast.KindSwitchStatement:
		return l.breakable(node, "")
	case ast.KindLabeledStatement:
		return l.labeledStatement(node)
	case ast.KindFunctionDeclaration:
		l.bodies = append(l.bodies, node)
	case ast.KindClassDeclaration:
		if _, ok := l.genericClasses[l.declaredSymbol(node)]; ok {
			break // each instance lowers its constructor and methods
		}
		if class, ok := l.classes[l.declaredSymbol(node)]; ok {
			l.bodies = append(l.bodies, node) // its constructor and methods
			if class != nil {
				return l.staticInitializers(node, class)
			}
		} else {
			l.notSupportedYet(node, "classes inside functions or blocks are")
		}
	case ast.KindReturnStatement:
		return l.returnStatement(node)
	case ast.KindBreakStatement:
		return l.jump(node, node.AsBreakStatement().Label, false)
	case ast.KindContinueStatement:
		return l.jump(node, node.AsContinueStatement().Label, true)
	case ast.KindEnumDeclaration:
		l.enumDeclaration(node)
	case ast.KindEmptyStatement, ast.KindInterfaceDeclaration, ast.KindTypeAliasDeclaration:
		// No runtime effect.
	case ast.KindImportDeclaration, ast.KindExportDeclaration:
		// No code: a use of an imported binding is a use of the exporting module's own,
		// and the frontend puts the modules in the order they run.
		if node.Kind == ast.KindImportDeclaration && isDeferred(node) {
			l.notSupportedYet(node, "`import defer` is")
		}
	case ast.KindExportAssignment:
		return l.exportDefault(node)
	default:
		l.notSupportedYet(node, describe(node)+" is")
	}
	return nil
}

// isDeferred reports whether an import declaration is `import defer`, whose module would
// run when first used rather than before the importing module.
func isDeferred(node *ast.Node) bool {
	clause := node.AsImportDeclaration().ImportClause
	return clause != nil && clause.AsImportClause().PhaseModifier == ast.KindDeferKeyword
}

// statementBlock lowers the body of an if, a loop or a labelled statement as a block.
func (l *lowerer) statementBlock(node *ast.Node) *ir.Block {
	if node.Kind == ast.KindBlock {
		return l.block(node, node.Statements())
	}
	return &ir.Block{At: l.pos(node), Stmts: l.statement(node)}
}

// ifStatement lowers an if statement, or reports it and returns nil.
func (l *lowerer) ifStatement(node *ast.Node) *ir.If {
	s := node.AsIfStatement()
	cond := l.condition(s.Expression)
	then := l.statementBlock(s.ThenStatement)
	var otherwise ir.Stmt
	switch {
	case s.ElseStatement == nil:
	case s.ElseStatement.Kind == ast.KindIfStatement:
		if elseIf := l.ifStatement(s.ElseStatement); elseIf != nil {
			otherwise = elseIf
		}
	default:
		otherwise = l.statementBlock(s.ElseStatement)
	}
	if cond == nil {
		return nil
	}
	return &ir.If{At: l.pos(node), Cond: cond, Then: then, Else: otherwise}
}

// labeledStatement lowers `name: statement`. A labelled loop or switch takes the label;
// any other statement becomes a block that `break name` leaves.
func (l *lowerer) labeledStatement(node *ast.Node) []ir.Stmt {
	labeled := node.AsLabeledStatement()
	name := labeled.Label.Text()
	switch labeled.Statement.Kind {
	case ast.KindWhileStatement, ast.KindDoStatement, ast.KindForStatement, ast.KindForOfStatement, ast.KindForInStatement, ast.KindSwitchStatement:
		return l.breakable(labeled.Statement, name)
	}
	label := &ir.Label{Name: name}
	l.targets = append(l.targets, jumpTarget{label: label})
	body := l.statementBlock(labeled.Statement)
	l.targets = l.targets[:len(l.targets)-1]
	return []ir.Stmt{&ir.LabeledBlock{At: l.pos(node), Label: label, Body: body}}
}

// breakable lowers a loop or switch statement; name is its source label, or "".
func (l *lowerer) breakable(node *ast.Node, name string) []ir.Stmt {
	label := &ir.Label{Name: name}
	loop := node.Kind != ast.KindSwitchStatement
	l.targets = append(l.targets, jumpTarget{label: label, loop: loop, inner: true})
	defer func() { l.targets = l.targets[:len(l.targets)-1] }()
	switch node.Kind {
	case ast.KindWhileStatement:
		return l.whileStatement(node, label)
	case ast.KindDoStatement:
		return l.doStatement(node, label)
	case ast.KindForStatement:
		return l.forStatement(node, label)
	case ast.KindForOfStatement:
		return l.forOfStatement(node, label)
	case ast.KindForInStatement:
		return l.forInStatement(node, label)
	default:
		return l.switchStatement(node, label)
	}
}

func (l *lowerer) whileStatement(node *ast.Node, label *ir.Label) []ir.Stmt {
	s := node.AsWhileStatement()
	cond := l.condition(s.Expression)
	body := l.statementBlock(s.Statement)
	if cond == nil {
		return nil
	}
	return []ir.Stmt{&ir.Loop{At: l.pos(node), Label: label, Cond: cond, Body: body}}
}

func (l *lowerer) doStatement(node *ast.Node, label *ir.Label) []ir.Stmt {
	s := node.AsDoStatement()
	body := l.statementBlock(s.Statement)
	cond := l.condition(s.Expression)
	if cond == nil {
		return nil
	}
	return []ir.Stmt{&ir.Loop{At: l.pos(node), Label: label, Cond: cond, PostTest: true, Body: body}}
}

// forStatement lowers `for (init; cond; update) body` to a block holding the
// initialisation and a loop, so its declarations are scoped to the loop.
func (l *lowerer) forStatement(node *ast.Node, label *ir.Label) []ir.Stmt {
	s := node.AsForStatement()
	var init []ir.Stmt
	var bindings []*ir.Local
	failed := false
	switch {
	case s.Initializer == nil:
	case s.Initializer.Kind == ast.KindVariableDeclarationList:
		init = l.declarationList(node, s.Initializer)
		failed = init == nil
		if s.Initializer.Flags&ast.NodeFlagsLet != 0 {
			bindings = declaredLocals(init)
		}
	default:
		x := l.expression(s.Initializer)
		failed = x == nil
		init = []ir.Stmt{&ir.ExprStmt{At: l.pos(s.Initializer), X: x}}
	}
	loop := &ir.Loop{At: l.pos(node), Label: label, Bindings: bindings}
	if s.Condition != nil {
		loop.Cond = l.condition(s.Condition)
		failed = failed || loop.Cond == nil
	}
	if s.Incrementor != nil {
		loop.Update = l.expression(s.Incrementor)
		failed = failed || loop.Update == nil
	}
	loop.Body = l.statementBlock(s.Statement)
	if failed {
		return nil
	}
	if len(init) == 0 {
		return []ir.Stmt{loop}
	}
	return []ir.Stmt{&ir.Block{At: l.pos(node), Stmts: append(init, loop)}}
}

// declaredLocals are the locals that declarations (LocalDecls) declare.
func declaredLocals(stmts []ir.Stmt) []*ir.Local {
	locals := make([]*ir.Local, len(stmts))
	for i, stmt := range stmts {
		locals[i] = stmt.(*ir.LocalDecl).Local
	}
	return locals
}

// switchStatement lowers a switch to a block that stores the value in a local, then the
// cases, each testing the local with === against its expression.
func (l *lowerer) switchStatement(node *ast.Node, label *ir.Label) []ir.Stmt {
	s := node.AsSwitchStatement()
	value := l.expression(s.Expression)
	var tag *ir.Local
	if value != nil {
		tag = l.newLocal("tag", value.Type())
	}
	result := &ir.Switch{At: l.pos(node), Label: label}
	failed := value == nil
	var statements []*ast.Node // of every clause, which share one scope
	for _, clause := range s.CaseBlock.AsCaseBlock().Clauses.Nodes {
		statements = append(statements, clause.Statements()...)
	}
	hoisted := l.hoistedFunctions(statements)
	for _, clause := range s.CaseBlock.AsCaseBlock().Clauses.Nodes {
		c := &ir.Case{At: l.pos(clause)}
		if clause.Kind == ast.KindCaseClause && tag != nil {
			c.Match = l.caseMatch(clause, tag)
			failed = failed || c.Match == nil
		}
		for _, statement := range clause.Statements() {
			c.Body = append(c.Body, l.statement(statement)...)
		}
		result.Cases = append(result.Cases, c)
	}
	if failed {
		return nil
	}
	stmts := append(append([]ir.Stmt{&ir.LocalDecl{At: l.pos(node), Local: tag, Init: value}}, hoisted...), result)
	return []ir.Stmt{&ir.Block{At: l.pos(node), Stmts: stmts}}
}

// caseMatch compares a switch's value, stored in tag, with a case's expression.
func (l *lowerer) caseMatch(clause *ast.Node, tag *ir.Local) ir.Expr {
	test := l.expression(clause.Expression())
	if test == nil {
		return nil
	}
	return l.operation(clause, ir.Eq, &ir.LocalRef{Local: tag}, test, clause, clause.Expression())
}

// jump lowers break and continue, with or without a label.
func (l *lowerer) jump(node *ast.Node, labelNode *ast.Node, isContinue bool) []ir.Stmt {
	for i := len(l.targets) - 1; i >= 0; i-- {
		target := l.targets[i]
		found := target.inner && (target.loop || !isContinue)
		if labelNode != nil {
			found = target.label.Name == labelNode.Text()
		}
		if !found {
			continue
		}
		if isContinue {
			return []ir.Stmt{&ir.Continue{At: l.pos(node), Target: target.label}}
		}
		return []ir.Stmt{&ir.Break{At: l.pos(node), Target: target.label}}
	}
	panic("lower: tsc accepted a break or continue without a target")
}

func (l *lowerer) variableStatement(node *ast.Node) []ir.Stmt {
	if ast.HasSyntacticModifier(node, ast.ModifierFlagsAmbient) {
		l.notSupportedYet(node, "`declare` is")
		return nil
	}
	return l.declarationList(node, node.AsVariableStatement().DeclarationList)
}

// declarationList lowers the declarations of a variable statement or a for loop's
// initialiser (node), or reports them and returns nil.
func (l *lowerer) declarationList(node *ast.Node, list *ast.Node) []ir.Stmt {
	if list.Flags&ast.NodeFlagsUsing != 0 {
		l.notSupportedYet(node, "`using` is")
		return nil
	}
	var stmts []ir.Stmt
	for _, declaration := range list.AsVariableDeclarationList().Declarations.Nodes {
		if declaration.Name().Kind != ast.KindIdentifier {
			destructured, ok := l.destructuringDeclaration(declaration)
			if !ok {
				return nil
			}
			stmts = append(stmts, destructured...)
			continue
		}
		decl := l.variableDeclaration(declaration)
		if decl == nil {
			return nil
		}
		stmts = append(stmts, decl)
	}
	return stmts
}

// destructuringDeclaration lowers `const { x, y } = value` or `let [a, b] = value`.
func (l *lowerer) destructuringDeclaration(node *ast.Node) ([]ir.Stmt, bool) {
	value := l.expression(node.Initializer()) // tsc requires one
	if value == nil {
		return nil, false
	}
	return l.destructure(node.Name(), value, l.pos(node))
}

func (l *lowerer) variableDeclaration(node *ast.Node) ir.Stmt {
	name := node.Name()
	symbol := l.checker.GetSymbolAtLocation(name)
	local, declared := l.locals[symbol] // module-scope bindings are declared before any code is lowered
	if !declared {
		if t := l.bindingType(symbol, name); t != nil {
			local = l.newLocal(name.Text(), t)
		}
		l.locals[symbol] = local // a closure in the initialiser may use it; nil makes uses silent, since the declaration failed
	}
	if local == nil {
		return nil
	}
	var init ir.Expr
	if initializer := node.Initializer(); initializer != nil {
		if init = l.expression(initializer); init == nil {
			l.locals[symbol] = nil
			return nil
		}
		if init = l.convert(init, local.Type, initializer); init == nil {
			l.locals[symbol] = nil
			return nil
		}
	}
	return &ir.LocalDecl{At: l.pos(node), Local: local, Init: init}
}

// bindingType is the IR type of a variable or `export default` binding (symbol, written
// at node): the numeric type the semantic pass gave it, or its type's; nil if it has none (reported).
func (l *lowerer) bindingType(symbol *ast.Symbol, node *ast.Node) ir.Type {
	if t := l.numericType(node); t != nil {
		return t
	}
	return l.irType(l.typeOfSymbol(symbol), node)
}

// ---------------------------------------------------------------- modules

// declareModuleBindings makes the locals of a module's top-level `let` and `const`
// declarations and of its `export default` value, owned by the module's top-level code.
func (l *lowerer) declareModuleBindings(file *ast.SourceFile) {
	for _, statement := range file.Statements.Nodes {
		switch {
		case statement.Kind == ast.KindVariableStatement && !ast.HasSyntacticModifier(statement, ast.ModifierFlagsAmbient):
			list := statement.AsVariableStatement().DeclarationList
			if list.Flags&ast.NodeFlagsUsing != 0 {
				continue // reported when the statement is lowered
			}
			for _, declaration := range list.AsVariableDeclarationList().Declarations.Nodes {
				l.declarePatternBindings(declaration.Name())
			}
		case statement.Kind == ast.KindExportAssignment && l.hasDefaultBinding(statement):
			l.declareModuleBinding("default", l.declaredSymbol(statement), statement.Expression())
		}
	}
}

// declarePatternBindings declares the module-scope binding a declaration's name makes,
// or each one its binding pattern makes.
func (l *lowerer) declarePatternBindings(name *ast.Node) {
	if name.Kind == ast.KindIdentifier {
		l.declareModuleBinding(name.Text(), l.checker.GetSymbolAtLocation(name), name)
		return
	}
	for _, element := range name.AsBindingPattern().Elements.Nodes {
		if element.Kind == ast.KindBindingElement && element.Name() != nil { // not an elision
			l.declarePatternBindings(element.Name())
		}
	}
}

// declareModuleBinding makes the local of a module-scope binding, typed at node; it is
// nil, so that uses of it are silent, if its type is reported.
func (l *lowerer) declareModuleBinding(name string, symbol *ast.Symbol, node *ast.Node) {
	var local *ir.Local
	if t := l.bindingType(symbol, node); t != nil {
		local = l.newLocal(name, t)
		l.moduleScope[local] = true
	}
	l.locals[symbol] = local
}

// hasDefaultBinding reports whether `export default value` (node) makes a binding of its
// own, which holds the value as it was when the statement ran, as in JS. A value that
// names a function (which can't change) or only a type needs none: importing the
// default names the function or type itself.
func (l *lowerer) hasDefaultBinding(node *ast.Node) bool {
	value := node.Expression()
	if !ast.IsEntityNameExpression(value) {
		return true
	}
	symbol := l.checker.GetSymbolAtLocation(value)
	if symbol != nil && symbol.Flags&ast.SymbolFlagsAlias != 0 {
		symbol = l.checker.GetAliasedSymbol(symbol)
	}
	return symbol != nil && symbol.Flags&ast.SymbolFlagsValue != 0 && symbol.Flags&ast.SymbolFlagsFunction == 0
}

// exportDefault lowers `export default value`: it stores the value in the module's default binding.
func (l *lowerer) exportDefault(node *ast.Node) []ir.Stmt {
	local := l.locals[l.declaredSymbol(node)]
	if local == nil {
		return nil // it names a function or a type, so there is no binding; or its type was reported
	}
	value := l.expression(node.Expression())
	if value == nil {
		return nil
	}
	if value = l.convert(value, local.Type, node.Expression()); value == nil {
		return nil
	}
	return []ir.Stmt{&ir.LocalDecl{At: l.pos(node), Local: local, Init: value}}
}

// target returns the symbol node refers to, following imports and re-exports to the
// declaration they name (followAliases).
func (l *lowerer) target(node *ast.Node) *ast.Symbol {
	return l.followAliases(l.checker.GetSymbolAtLocation(node))
}

// followAliases follows an imported or re-exported symbol to the declaration it names.
// It stops at a module's default binding (hasDefaultBinding), which holds a value of its own.
func (l *lowerer) followAliases(symbol *ast.Symbol) *ast.Symbol {
	for symbol != nil && symbol.Flags&ast.SymbolFlagsAlias != 0 {
		if declaration := symbol.Declarations[0]; declaration.Kind == ast.KindExportAssignment && l.hasDefaultBinding(declaration) {
			return symbol
		}
		symbol = l.checker.GetImmediateAliasedSymbol(symbol)
	}
	return symbol
}

// memberDeclaration is the declaration of the member a property access names, or nil.
// The checker may give an instance's member a symbol of its own (instantiated for the
// instance's `this` type), so members are known by their declarations.
func (l *lowerer) memberDeclaration(node *ast.Node) *ast.Node {
	if node.Kind != ast.KindPropertyAccessExpression {
		return nil
	}
	if symbol := l.memberSymbol(node); symbol != nil {
		return symbol.ValueDeclaration
	}
	return nil
}

// memberSymbol is the symbol of the member a property access names. In an instance of a
// generic function it is the member of the object's type in the instance: x.name for
// `x: T` with `T extends Named` is the name field of the class T is (D-049).
func (l *lowerer) memberSymbol(node *ast.Node) *ast.Symbol {
	if name := node.AsPropertyAccessExpression().Name(); l.subst != nil && name.Kind == ast.KindIdentifier {
		if member := l.checker.GetPropertyOfType(l.typeAt(node.Expression()), name.Text()); member != nil {
			return member
		}
	}
	return l.checker.GetSymbolAtLocation(node)
}

// isNamespace reports whether node names a module namespace object: `m` after `import * as m`.
func (l *lowerer) isNamespace(node *ast.Node) bool {
	symbol := l.target(node)
	return symbol != nil && symbol.Flags&ast.SymbolFlagsValueModule != 0
}

// declaredSymbol is the symbol a declaration declares, also one without a name (`export default`).
func (l *lowerer) declaredSymbol(declaration *ast.Node) *ast.Symbol {
	return l.checker.GetMergedSymbol(declaration.Symbol())
}

// numericType is the IR type the semantic pass gave a number-typed node, or nil.
func (l *lowerer) numericType(node *ast.Node) ir.Type {
	if n, ok := l.info.TypeOf(node); ok {
		return numericTypes[n]
	}
	return nil
}

// convert adapts value to the type target, or reports why it cannot. Numeric
// conversions are always possible here: the semantic pass rejected the lossy implicit ones.
func (l *lowerer) convert(value ir.Expr, target ir.Type, node *ast.Node) ir.Expr {
	switch {
	case value.Type() == target:
		return value
	case target == ir.Value:
		return box(value)
	case isUnion(target) && value.Type() == ir.Value:
		return &ir.ToUnion{X: value, To: target.(*ir.Union)} // a dynamic value tsc types as the union, such as `a.find(…)`'s
	case isUnion(target):
		return l.toUnion(value, target.(*ir.Union), node)
	case ir.IsNumeric(value.Type()) && ir.IsNumeric(target):
		return &ir.Convert{X: value, To: target}
	case isFunction(value.Type()) && isFunction(target):
		return l.adapt(value, target.(*ir.FuncType), node)
	case isObject(value.Type()) && isObject(target) && !isArray(value.Type()) && !isArray(target):
		return l.objectCast(value, target, node)
	case isEmptyArrayLiteral(value) && isArray(target):
		return &ir.NewArray{Array: target.(*ir.Array)} // `[]` nested in a literal, typed never[], fits any array type
	case isObject(value.Type()) && isRecord(target):
		l.report(diagnostic.At(node, diagnostic.NotSupportedYet, "using a `%s` as a hash-map object (`%s`) is not supported yet; a copy of its properties, `{ ...o }`, is one", value.Type(), target))
		return nil
	default:
		l.notSupportedYet(node, fmt.Sprintf("converting %s to %s is", value.Type(), target))
		return nil
	}
}

// ---------------------------------------------------------------- functions

// declareFunctions makes the IR function, with its parameters and result type, of every
// function declaration under node, before any body is lowered: a call may come before
// the declaration it calls. outer is the qualified name of the enclosing function, or "".
func (l *lowerer) declareFunctions(node *ast.Node, outer string) {
	node.ForEachChild(func(child *ast.Node) bool {
		qualified := outer
		if child.Kind == ast.KindClassDeclaration && child.TypeParameterList() != nil && l.subst == nil {
			return false // each instance declares what its methods declare
		}
		if child.Kind == ast.KindFunctionDeclaration && child.Body() != nil && l.isGeneric(child) {
			name := "default"
			if child.Name() != nil {
				name = child.Name().Text()
			}
			if outer != "" {
				name = outer + "." + name
			}
			l.declareGeneric(child, name) // its instances, and what they declare, come with the calls
			return false
		}
		if child.Kind == ast.KindFunctionDeclaration && child.Body() != nil {
			qualified = "default" // `export default function () {}`
			if name := child.Name(); name != nil {
				qualified = name.Text()
			}
			jsName := qualified
			if outer != "" {
				qualified = outer + "." + qualified
			}
			fn := l.declareFunction(child, qualified, nil)
			if fn != nil {
				fn.JSName, fn.Closure = jsName, isNestedFunction(child)
			}
			l.funcs[l.declaredSymbol(child)] = fn
		}
		l.declareFunctions(child, qualified)
		return false
	})
}

// declareFunction makes the IR function for a function, method or constructor
// declaration, or reports why it can't and returns nil. A method or constructor of class
// takes `this` first.
func (l *lowerer) declareFunction(node *ast.Node, name string, class *ir.Class) *ir.Func {
	flags := ast.GetFunctionFlags(node)
	switch {
	case l.isGeneric(node) && node.Kind == ast.KindMethodDeclaration:
		l.notSupportedYet(nameOf(node), "generic methods are")
		return nil
	case l.isGeneric(node):
		l.notSupportedYet(nameOf(node), "generic functions are")
		return nil
	case flags&ast.FunctionFlagsGenerator != 0:
		l.notSupportedYet(nameOf(node), "generator functions are")
		return nil
	case flags&ast.FunctionFlagsAsync != 0:
		l.notSupportedYet(nameOf(node), "async functions are")
		return nil
	}
	fn := &ir.Func{Name: name, Module: l.function.Module, Pos: l.pos(node)}
	l.funcNodes[fn] = node
	if class != nil {
		fn.Params = []*ir.Local{l.newThis(fn, class)}
	}
	outer := l.function
	l.function = fn // parameters are locals of fn
	defer func() { l.function = outer }()
	for _, param := range node.Parameters() {
		locals := l.parameter(param)
		if locals == nil {
			return nil
		}
		fn.Params = append(fn.Params, locals...)
	}
	if node.Kind == ast.KindConstructor {
		fn.Result = ir.Void
	} else if fn.Result = l.resultType(node); fn.Result == nil {
		return nil
	}
	return fn
}

// parameter makes the parameters of fn for a parameter (node): its local, or for one
// with a default, the local of the argument and its Given flag (D-055). The body starts
// by binding a parameter with a default or a pattern (parameterPrologue). It reports why
// it can't and returns nil.
func (l *lowerer) parameter(node *ast.Node) []*ir.Local {
	t := l.parameterType(node)
	if t == nil {
		return nil
	}
	name := node.Name()
	var local *ir.Local // what the body sees
	if name.Kind == ast.KindIdentifier {
		local = l.newLocal(name.Text(), t)
		l.locals[parameterSymbol(node)] = local
		l.paramLocals[l.function] = append(l.paramLocals[l.function], local)
	} else {
		local = l.newLocal("destructured", t) // a pattern, which the body starts by destructuring
		l.paramLocals[l.function] = append(l.paramLocals[l.function], nil)
	}
	if node.AsParameterDeclaration().DotDotDotToken != nil { // the array the caller makes (D-057)
		binding := &parameterBinding{node: node, value: local, arg: l.newLocal(local.Name+"_rest", l.restOf(t.(*ir.Array)))}
		l.bindings[l.function] = append(l.bindings[l.function], binding)
		return []*ir.Local{binding.arg}
	}
	optional := node.AsParameterDeclaration().QuestionToken != nil
	if node.Initializer() == nil && !(optional && node.Parent.Body() != nil) {
		if name.Kind != ast.KindIdentifier {
			l.bindings[l.function] = append(l.bindings[l.function], &parameterBinding{node: node, value: local})
		}
		if optional { // of an abstract method, whose calls pass the flag too
			return []*ir.Local{local, l.newLocal(local.Name+"_given", ir.Given)}
		}
		return []*ir.Local{local}
	}
	if node.Initializer() == nil { // `x?: T`: the call passes a T and whether it did; the body sees T | undefined
		local.Type = l.withUndefined(t)
	} else if u, ok := t.(*ir.Union); ok && u.Has(ir.Undefined) { // an undefined argument means the default
		local.Type = l.withoutUndefined(u)
	}
	binding := &parameterBinding{node: node, value: local, arg: l.newLocal(local.Name+"_arg", t), given: l.newLocal(local.Name+"_given", ir.Given)}
	l.bindings[l.function] = append(l.bindings[l.function], binding)
	return []*ir.Local{binding.arg, binding.given}
}

// restOf is the type of a rest parameter of the given array type.
func (l *lowerer) restOf(array *ir.Array) *ir.Rest {
	rest, ok := l.rests[array]
	if !ok {
		rest = &ir.Rest{Array: array}
		l.rests[array] = rest
	}
	return rest
}

// parameterSymbol is the symbol of a parameter named by an identifier, which references
// to it resolve to. A parameter property's declaration has the property's symbol, and
// the parameter's is among its constructor's locals.
func parameterSymbol(node *ast.Node) *ast.Symbol {
	if ast.IsParameterPropertyDeclaration(node, node.Parent) {
		return node.Parent.Locals()[node.Name().Text()]
	}
	return node.Symbol()
}

// parameterType is the IR type of a parameter's value, once a default it has applies;
// nil if it has none or is of a kind not supported yet (reported). An optional parameter
// of a signature (`m(x?: number)` in an interface) takes a value of the type without
// undefined, and a Given flag (D-055).
func (l *lowerer) parameterType(node *ast.Node) ir.Type {
	param := node.AsParameterDeclaration()
	switch {
	case param.DotDotDotToken != nil && !l.checker.IsArrayType(l.typeOfSymbol(node.Symbol())):
		l.notSupportedYet(node, fmt.Sprintf("rest parameters of type %s are", l.checker.TypeToString(l.typeOfSymbol(node.Symbol()))))
		return nil
	}
	var t *checker.Type
	switch {
	case param.Name().Kind != ast.KindIdentifier:
		t = l.typeOfSymbol(node.Symbol()) // a pattern's
	case l.numericType(param.Name()) != nil:
		return l.numericType(param.Name())
	default:
		t = l.typeOfSymbol(l.checker.GetSymbolAtLocation(param.Name()))
	}
	if passedOrNot(node) {
		t = l.checker.RemoveMissingOrUndefinedType(t)
	}
	return l.irType(t, param.Name())
}

// passedOrNot reports whether a parameter declaration (node) holds undefined only when
// the call leaves its argument out, which it may: one with a question mark, and one with
// a default and no type, which takes the contextual type `T | undefined` of an optional
// parameter (tsc removes the undefined in the body).
func passedOrNot(node *ast.Node) bool {
	param := node.AsParameterDeclaration()
	return param.QuestionToken != nil || param.Initializer != nil && param.Type == nil
}

// resultType is the IR type a function returns: Void, the numeric type the semantic
// pass chose, or the type tsc gives it; nil (reported) if it has no IR type.
func (l *lowerer) resultType(node *ast.Node) ir.Type {
	if n, ok := l.info.ResultType(node); ok {
		return numericTypes[n]
	}
	result := l.returnTypeOf(l.checker.GetSignatureFromDeclaration(node))
	if result.Flags()&checker.TypeFlagsVoid != 0 {
		return ir.Void
	}
	return l.irType(result, nameOf(node))
}

// nameOf is a declaration's name, or the declaration itself if it has none (`export default function () {}`).
func nameOf(declaration *ast.Node) *ast.Node {
	if name := declaration.Name(); name != nil {
		return name
	}
	return declaration
}

// functionBody lowers the body of a function declaration into the function made for
// it, or a class's constructor and methods.
func (l *lowerer) functionBody(node *ast.Node) {
	if node.Kind == ast.KindClassDeclaration {
		l.classBodies(node)
		return
	}
	fn := l.funcs[l.declaredSymbol(node)]
	if fn == nil || node.Body() == nil {
		return // a failed declaration (reported), or an overload signature
	}
	l.inFunction(fn, func() { fn.Body = l.block(node.Body(), node.Body().Statements()) })
}

// returnStatement lowers `return value`, converting the value to the function's result type.
func (l *lowerer) returnStatement(node *ast.Node) []ir.Stmt {
	at := l.pos(node)
	if node.Expression() == nil {
		return []ir.Stmt{&ir.Return{At: at}}
	}
	value := l.expression(node.Expression())
	if value == nil {
		return nil
	}
	if l.function.Result == ir.Void {
		// `return f()` in a function that returns nothing still calls f.
		if isPure(value) {
			return []ir.Stmt{&ir.Return{At: at}}
		}
		return []ir.Stmt{&ir.ExprStmt{At: at, X: value}, &ir.Return{At: at}}
	}
	if value = l.convert(value, l.function.Result, node.Expression()); value == nil {
		return nil
	}
	return []ir.Stmt{&ir.Return{At: at, Value: value}}
}

// ---------------------------------------------------------------- expressions

// expression lowers an expression, or reports it and returns nil.
func (l *lowerer) expression(node *ast.Node) ir.Expr {
	if object, ok := l.chainObjects[node]; ok {
		return object.value
	}
	if ast.IsOptionalChain(node) && ast.IsOutermostOptionalChain(node) && !l.chainRests[node] {
		return l.optionalChain(node)
	}
	switch node.Kind {
	case ast.KindNumericLiteral:
		return l.numericLiteral(node, false)
	case ast.KindAsExpression:
		return l.as(node)
	case ast.KindStringLiteral, ast.KindNoSubstitutionTemplateLiteral:
		return &ir.StringConst{Value: node.Text()}
	case ast.KindTrueKeyword:
		return &ir.BoolConst{Value: true}
	case ast.KindFalseKeyword:
		return &ir.BoolConst{Value: false}
	case ast.KindNullKeyword:
		return &ir.NullConst{}
	case ast.KindIdentifier:
		return l.narrowed(node, l.identifier(node))
	case ast.KindParenthesizedExpression:
		return l.expression(node.Expression())
	case ast.KindCallExpression:
		return l.call(node)
	case ast.KindBinaryExpression:
		return l.binary(node)
	case ast.KindPrefixUnaryExpression:
		return l.prefixUnary(node)
	case ast.KindPostfixUnaryExpression:
		postfix := node.AsPostfixUnaryExpression()
		return l.increment(node, postfix.Operand, postfix.Operator, true)
	case ast.KindConditionalExpression:
		return l.conditional(node)
	case ast.KindTypeOfExpression:
		return l.typeOf(node)
	case ast.KindVoidExpression:
		x := l.expression(node.Expression())
		if x == nil {
			return nil
		}
		return discard(x, &ir.UndefinedConst{})
	case ast.KindTemplateExpression:
		return l.template(node)
	case ast.KindPropertyAccessExpression:
		if x, ok := l.enumMember(node); ok {
			return x
		}
		return l.narrowed(node, l.propertyAccess(node))
	case ast.KindElementAccessExpression:
		if x, ok := l.enumMember(node); ok {
			return x
		}
		if l.isEnumReverseMapping(node) {
			return l.enumReverseMapping(node, true)
		}
		if field, ok := l.tupleElement(node); ok {
			if field == nil {
				return nil // reported
			}
			return l.tupleElementRead(node, field)
		}
		if l.isArrayElement(node) {
			return l.narrowed(node, l.arrayElement(node, ir.ArrayElementChecked))
		}
		if l.isRecordElement(node) {
			return l.narrowed(node, l.recordElement(node, ir.MapGetChecked))
		}
		return l.stringElement(node, ir.StringElementChecked)
	case ast.KindArrayLiteralExpression:
		return l.arrayLiteral(node)
	case ast.KindNonNullExpression:
		return l.nonNull(node)
	case ast.KindThisKeyword:
		return l.thisExpression(node)
	case ast.KindNewExpression:
		return l.newExpression(node)
	case ast.KindObjectLiteralExpression:
		return l.objectLiteral(node)
	case ast.KindArrowFunction, ast.KindFunctionExpression:
		return l.functionExpression(node)
	case ast.KindDeleteExpression:
		return l.deleteExpression(node)
	}
	l.notSupportedYet(node, describe(node)+" is")
	return nil
}

// valueExpression lowers an expression whose context takes any value, such as an
// argument of console.log. There s[i] and a[i] keep JS's undefined for an index out of
// range (D-015), and o[k] for a key a hash-map object doesn't have (D-059); everything
// else lowers as usual.
func (l *lowerer) valueExpression(node *ast.Node) ir.Expr {
	inner := ast.SkipParentheses(node)
	switch {
	case ast.IsOptionalChain(inner) && ast.IsOutermostOptionalChain(inner) && !l.chainRests[inner]:
		return l.optionalChain(inner)
	case l.isEnumReverseMapping(inner):
		return l.enumReverseMapping(inner, false)
	case l.isStringElement(inner):
		return l.stringElement(inner, ir.StringElement)
	case l.isArrayElement(inner):
		return l.arrayElement(inner, ir.ArrayElement)
	case l.isRecordElement(inner):
		return l.recordElement(inner, ir.MapGet)
	}
	return l.expression(node)
}

func (l *lowerer) identifier(node *ast.Node) ir.Expr {
	symbol := l.target(node)
	if x, ok := l.binding(node, symbol); ok {
		return x
	}
	if node.Text() == "undefined" {
		return &ir.UndefinedConst{} // a local named undefined was found above
	}
	if value, ok := libConstants[node.Text()]; ok && l.isDeclaredInDefaultLib(symbol) {
		return &ir.NumberConst{Value: value}
	}
	if l.isDeclaredInDefaultLib(symbol) || l.isDeclaredInPtsLib(symbol) {
		l.notSupportedYet(node, fmt.Sprintf("the global `%s` is", node.Text()))
	} else {
		l.notSupportedYet(node, fmt.Sprintf("using `%s` is", node.Text()))
	}
	return nil
}

// binding lowers a reference (node) to a binding of the program (symbol): a local, or a
// module-scope binding of this module or an imported one. It reports, and returns nil, a
// function or module namespace used as a value, and a binding that failed to lower. ok
// is false if symbol is no binding of the program.
func (l *lowerer) binding(node *ast.Node, symbol *ast.Symbol) (x ir.Expr, ok bool) {
	if _, ok := l.locals[symbol]; !ok {
		l.declareAhead(symbol)
	}
	if local, ok := l.locals[symbol]; ok {
		if local = l.ownLocal(node, local); local == nil {
			return nil, true
		}
		return &ir.LocalRef{Local: local}, true
	}
	if _, ok := l.generics[symbol]; ok {
		l.notSupportedYet(node, "generic functions as values are")
		return nil, true
	}
	if fn, ok := l.funcs[symbol]; ok {
		if fn == nil {
			return nil, true // its declaration failed, which was reported
		}
		x := l.functionValue(node, fn)
		return x, true
	}
	if symbol != nil && symbol.Flags&ast.SymbolFlagsValueModule != 0 {
		l.notSupportedYet(node, "using the module namespace `"+scanner.GetTextOfNode(node)+"` as a value is")
		return nil, true
	}
	if symbol != nil && symbol.Flags&ast.SymbolFlagsEnum != 0 {
		l.notSupportedYet(node, "using the enum object `"+scanner.GetTextOfNode(node)+"` as a value is")
		return nil, true
	}
	return nil, false
}

func (l *lowerer) call(node *ast.Node) ir.Expr {
	call := node.AsCallExpression()
	switch call.Expression.Kind {
	case ast.KindImportKeyword:
		l.notSupportedYet(node, "dynamic `import()` is")
		return nil
	case ast.KindSuperKeyword:
		return l.superCall(node)
	}
	if l.isFunctionMethod(ast.SkipParentheses(call.Expression)) {
		return l.functionMethodCall(call)
	}
	callee := l.target(ast.SkipParentheses(call.Expression))
	if generic := l.genericMethodAt(ast.SkipParentheses(call.Expression)); generic != nil {
		if generic.static != nil {
			return l.genericCall(call, generic)
		}
		return l.genericMethodCall(call, generic)
	}
	if fn, ok := l.staticMethods[l.memberDeclaration(ast.SkipParentheses(call.Expression))]; ok {
		return l.staticCall(call, fn)
	}
	if method, ok := l.membersAt(ast.SkipParentheses(call.Expression)).methods[l.memberDeclaration(ast.SkipParentheses(call.Expression))]; ok {
		switch {
		case method == nil:
			return nil // its declaration failed, which was reported
		case method.Kind != ir.Plain:
			return l.valueCall(call) // `o.p()` calls the function a getter returns
		}
		return l.instanceMethodCall(call, method)
	}
	if l.isObjectTypeMethod(l.memberDeclaration(ast.SkipParentheses(call.Expression))) {
		return l.interfaceMethodCall(call)
	}
	if l.isArrayMethodCall(node) {
		return l.arrayMethodCall(call, false)
	}
	if l.isMapMethodCall(node) {
		return l.mapMethodCall(call, false)
	}
	if l.isArrayStatic(ast.SkipParentheses(call.Expression), "isArray") {
		return l.arrayIsArray(call)
	}
	if l.isArrayStatic(ast.SkipParentheses(call.Expression), "from") {
		return l.arrayFrom(call)
	}
	if name := l.mathMember(ast.SkipParentheses(call.Expression)); name != "" {
		return l.mathCall(call, name)
	}
	if l.isObjectStatic(ast.SkipParentheses(call.Expression), "keys", "values", "entries", "hasOwn") {
		return l.objectStaticCall(call)
	}
	if l.isRecordMethodCall(node) {
		return l.recordMethodCall(call)
	}
	if generic, ok := l.generics[callee]; ok {
		if generic == nil {
			return nil // its declaration failed, which was reported
		}
		return l.genericCall(call, generic)
	}
	fn, isUserFunction := l.funcs[callee]
	builtin := l.builtin(call.Expression)
	switch {
	case isUserFunction && fn == nil:
		return nil // its declaration failed, which was reported
	case !isUserFunction && builtin == nil && !l.isUnloweredOptionalChain(node) && l.isValueCall(call.Expression):
		return l.valueCall(call)
	case !isUserFunction && (builtin == nil || properties[builtin]):
		l.notSupportedYet(node, "calling `"+scanner.GetTextOfNode(call.Expression)+"` is")
		return nil
	case l.isUnloweredOptionalChain(node):
		l.notSupportedYet(node, "optional chaining `?.` is")
		return nil
	}
	switch {
	case isUserFunction:
		return l.userCall(call, fn)
	case builtin == ir.ConsoleLog:
		return l.consoleLog(call)
	case l.hasSpreadArgument(call):
		return nil
	}
	return l.methodCall(node, builtin)
}

// hasSpreadArgument reports a call's spread argument, if it has one.
func (l *lowerer) hasSpreadArgument(call *ast.CallExpression) bool {
	for _, argument := range call.Arguments.Nodes {
		if argument.Kind == ast.KindSpreadElement {
			l.notSupportedYet(argument, "spread arguments are")
			return true
		}
	}
	return false
}

// userCall calls a function of the program; a nested function declaration with the
// closure its block made.
func (l *lowerer) userCall(call *ast.CallExpression, fn *ir.Func) ir.Expr {
	var closure ir.Expr
	if fn.Closure {
		if closure = l.closureOf(call.Expression, fn); closure == nil {
			return nil
		}
	}
	args := l.arguments(call.Arguments.Nodes, fn.Params)
	if args == nil {
		return nil
	}
	return &ir.Call{Func: fn, Closure: closure, Args: args}
}

// arguments lowers the arguments of a call of a function of the program, each converted
// to its parameter's type, or reports one and returns nil.
func (l *lowerer) arguments(arguments []*ast.Node, params []*ir.Local) []ir.Expr {
	types := make([]ir.Type, len(params))
	for i, param := range params {
		types[i] = param.Type
	}
	return l.typedArguments(arguments, types)
}

// typedArguments lowers the arguments of a call, each converted to its parameter's type
// in params, or reports one and returns nil. A parameter with a default, which its Given
// flag follows in params, gets a true flag after its argument, or if the call leaves the
// argument out or passes undefined, LeftOut and a false flag (D-055). A rest parameter
// gets an array of the arguments from its position on (D-057); a spread argument passes
// a tuple's elements, or an array's into a rest parameter.
func (l *lowerer) typedArguments(arguments []*ast.Node, params []ir.Type) []ir.Expr {
	if len(params) == 0 || !isRest(params[len(params)-1]) {
		arguments = arguments[:min(len(arguments), arity(params))] // tsc rejects more
	}
	var values []ir.Expr
	var at []*ast.Node
	for _, argument := range arguments {
		passed := l.argumentValues(argument)
		if passed == nil {
			return nil
		}
		for _, value := range passed {
			values, at = append(values, value), append(at, argument)
		}
	}
	return l.convertArguments(values, at, params)
}

// spreadValue is an array that a spread argument (`...x`) passes the elements of, while
// typedArguments matches arguments to parameters; it never reaches the IR.
type spreadValue struct{ x ir.Expr }

func (s *spreadValue) Type() ir.Type { return s.x.Type() }

// argumentValues lowers an argument of a call: its value, or for a spread argument, the
// elements of a tuple (a literal's, or a variable's, read once per element) or an array
// to spread (spreadValue). It reports what it can't lower and returns nil.
func (l *lowerer) argumentValues(argument *ast.Node) []ir.Expr {
	if argument.Kind != ast.KindSpreadElement {
		if value := l.expression(argument); value != nil {
			return []ir.Expr{value}
		}
		return nil
	}
	x := l.expression(argument.Expression())
	switch {
	case x == nil:
		return nil
	case isArray(x.Type()):
		return []ir.Expr{&spreadValue{x}}
	case isTuple(x.Type()) && isNewObject(x): // `...[a, b]`
		return append([]ir.Expr{}, x.(*ir.NewObject).Values...)
	case isTuple(x.Type()) && isPure(x):
		elements := make([]ir.Expr, len(x.Type().(*ir.Class).Fields))
		for i, field := range x.Type().(*ir.Class).Fields {
			elements[i] = &ir.FieldRef{X: x, Field: field}
		}
		return elements
	case isTuple(x.Type()):
		l.notSupportedYet(argument, "spreading a tuple that isn't in a variable into arguments is")
		return nil
	}
	l.notSupportedYet(argument, fmt.Sprintf("spreading a %s into arguments is", x.Type()))
	return nil
}

func isNewObject(x ir.Expr) bool {
	_, ok := x.(*ir.NewObject)
	return ok
}

func isRest(t ir.Type) bool {
	_, ok := t.(*ir.Rest)
	return ok
}

// restArray is the array a rest parameter of type array gets from values (each written at
// the node of the same index in at): each value converted to the element type, and the
// elements of each spread array in its place. It reports what it can't convert and
// returns nil.
func (l *lowerer) restArray(values []ir.Expr, at []*ast.Node, array *ir.Array) ir.Expr {
	parts := make([]ir.Expr, len(values))
	spread := false
	for i, value := range values {
		if s, ok := value.(*spreadValue); ok {
			if s.x.Type() != array {
				l.notSupportedYet(at[i], fmt.Sprintf("spreading a %s into a rest parameter of type %s is", s.x.Type(), array))
				return nil
			}
			parts[i], spread = s.x, true
			continue
		}
		if parts[i] = l.convert(value, array.Elem, at[i]); parts[i] == nil {
			return nil
		}
	}
	if !spread {
		return &ir.NewArray{Array: array, Elems: parts}
	}
	return &ir.ArrayCall{Op: ir.ArrayConcat, Args: append([]ir.Expr{&ir.NewArray{Array: array}}, parts...)}
}

// convertArguments converts values, the arguments a call passes (each written at the node
// of the same index in at), to params as typedArguments does.
func (l *lowerer) convertArguments(values []ir.Expr, at []*ast.Node, params []ir.Type) []ir.Expr {
	args := make([]ir.Expr, 0, len(params))
	next := 0
	for i := 0; i < len(params); i++ {
		param, optional := params[i], hasDefault(params, i)
		if rest, ok := param.(*ir.Rest); ok { // the last
			array := l.restArray(values[next:], at[next:], rest.Array)
			if array == nil {
				return nil
			}
			return append(args, array)
		}
		if optional {
			i++ // past the flag
		}
		if optional && next >= len(values) {
			args = append(args, notPassed(param)...)
			continue
		}
		arg, node := values[next], at[next] // tsc checked the count
		next++
		if _, ok := arg.(*spreadValue); ok {
			l.notSupportedYet(node, "spreading an array into parameters other than a rest parameter is")
			return nil
		}
		if optional && arg.Type() == ir.Undefined {
			args = append(args, discard(arg, &ir.LeftOut{T: param}), &ir.GivenConst{Value: false})
			continue
		}
		if u, ok := arg.Type().(*ir.Union); optional && ok && u.Has(ir.Undefined) && !l.takesUnion(param, u) {
			passed := l.maybePassed(arg, u, param, node) // `f(x)` with x: T | undefined for `f(p?: T)`
			if passed == nil {
				return nil
			}
			args = append(args, passed...)
			continue
		}
		if arg = l.convert(arg, param, node); arg == nil {
			return nil
		}
		args = append(args, arg)
		if optional {
			args = append(args, &ir.GivenConst{Value: true})
		}
	}
	return args
}

// hasDefault reports whether params[i] is a parameter with a default, which its Given
// flag follows (D-055).
func hasDefault(params []ir.Type, i int) bool {
	return i+1 < len(params) && params[i+1] == ir.Given
}

// notPassed is what a call passes for a parameter of type t with a default when it
// leaves the argument out: LeftOut and a false flag.
func notPassed(t ir.Type) []ir.Expr {
	return []ir.Expr{&ir.LeftOut{T: t}, &ir.GivenConst{Value: false}}
}

// arity is the number of arguments a function with parameter types params takes, its
// Given flags aside.
func arity(params []ir.Type) int {
	n := 0
	for _, param := range params {
		if param != ir.Given {
			n++
		}
	}
	return n
}

// consoleLog lowers console.log(…): each argument boxed; a spread argument passes an
// array's elements (SpreadArg) or a tuple's.
func (l *lowerer) consoleLog(call *ast.CallExpression) ir.Expr {
	args := make([]ir.Expr, 0, len(call.Arguments.Nodes))
	for _, argument := range call.Arguments.Nodes {
		if argument.Kind == ast.KindSpreadElement {
			values := l.argumentValues(argument)
			if values == nil {
				return nil
			}
			for _, value := range values {
				if spread, ok := value.(*spreadValue); ok {
					args = append(args, &ir.SpreadArg{X: spread.x})
				} else {
					args = append(args, box(value))
				}
			}
			continue
		}
		arg := l.valueExpression(argument)
		if arg == nil {
			return nil
		}
		args = append(args, box(arg))
	}
	return &ir.CallBuiltin{Builtin: ir.ConsoleLog, Args: args}
}

// methodCall lowers a call of a builtin method: the receiver, then each argument
// converted to its parameter's type, or what a left-out argument means.
func (l *lowerer) methodCall(node *ast.Node, builtin *ir.Builtin) ir.Expr {
	call := node.AsCallExpression()
	receiver := l.expression(call.Expression.Expression())
	if receiver == nil {
		return nil
	}
	args := []ir.Expr{receiver}
	for i, param := range builtin.Params[1:] {
		at, arg := node, ir.Expr(nil)
		if i < len(call.Arguments.Nodes) {
			at = call.Arguments.Nodes[i]
			if arg = l.argument(at); arg == nil {
				return nil
			}
		}
		if arg == nil || arg.Type() == ir.Undefined {
			if arg = leftOutArgument(builtin, i); arg == nil {
				l.notSupportedYet(at, "leaving out this argument is")
				return nil
			}
		}
		if arg = l.convert(arg, param, at); arg == nil {
			return nil
		}
		args = append(args, arg)
	}
	return &ir.CallBuiltin{Builtin: builtin, Args: args}
}

// argument lowers an argument of a builtin method. An undefined argument means the
// parameter's default, so it must be one whose evaluation has no effects.
func (l *lowerer) argument(node *ast.Node) ir.Expr {
	arg := l.expression(node)
	if arg == nil || arg.Type() != ir.Undefined {
		return arg
	}
	switch arg.(type) {
	case *ir.UndefinedConst, *ir.LocalRef:
		return arg
	}
	l.notSupportedYet(node, "passing this undefined value is")
	return nil
}

// leftOutArgument is what the i-th parameter after the receiver gets when a call leaves it out, or nil if it is required.
func leftOutArgument(builtin *ir.Builtin, i int) ir.Expr {
	if defaults := leftOut[builtin]; i < len(defaults) {
		return defaults[i]
	}
	return nil
}

func (l *lowerer) isDeclaredInDefaultLib(symbol *ast.Symbol) bool {
	return symbol != nil && symbol.ValueDeclaration != nil &&
		l.program.IsSourceFileDefaultLibrary(ast.GetSourceFileOfNode(symbol.ValueDeclaration).Path())
}

func (l *lowerer) isDeclaredInPtsLib(symbol *ast.Symbol) bool {
	return symbol != nil && symbol.ValueDeclaration != nil && lib.IsLibPath(ast.GetSourceFileOfNode(symbol.ValueDeclaration).FileName())
}

// numericLiteral lowers a literal (negated if it is the operand of unary minus) to a
// constant of the type the semantic pass gave it.
func (l *lowerer) numericLiteral(node *ast.Node, negated bool) ir.Expr {
	t := l.numericType(node)
	if ir.IsInteger(t) {
		value, _ := numeric.IntegerLiteralValue(node) // the pass checked it is an integer in range
		if negated {
			value.Neg(value)
		}
		bits := value.Uint64()
		if value.Sign() < 0 {
			bits = uint64(value.Int64())
		}
		return &ir.IntConst{Bits: bits, Ty: t}
	}
	var number ir.Expr = &ir.NumberConst{Value: float64(jsnum.FromString(node.Text()))}
	if negated {
		number = &ir.Unary{Op: ir.Neg, X: number}
	}
	if t == ir.F32 {
		return &ir.Convert{X: number, To: ir.F32}
	}
	return number
}

// as lowers `x as T` between numeric types, which converts the value.
func (l *lowerer) as(node *ast.Node) ir.Expr {
	target := l.numericType(node)
	if instantiated := l.typeAt(node); target == nil && l.subst != nil && check.IsNumber(instantiated) {
		target = l.irType(instantiated, node) // `x as T` in an instance whose T is a numeric type
	}
	if operand := ast.SkipParentheses(node.Expression()); target == nil && (operand.Kind == ast.KindArrayLiteralExpression || operand.Kind == ast.KindObjectLiteralExpression) {
		return l.assertedLiteral(node, operand)
	}
	if t := l.typeAt(node); target == nil && isUnionValue(t) {
		return l.asUnion(node, t)
	}
	if target == nil {
		l.notSupportedYet(node, "`as` other than between numeric types is")
		return nil
	}
	x := l.expression(node.Expression())
	if x == nil {
		return nil
	}
	return l.convert(x, target, node)
}

// assertedLiteral lowers `literal as T` (or `as const`) for an array or object literal,
// which takes T as its contextual type (with `as const`, its own readonly type), and so
// is a T.
func (l *lowerer) assertedLiteral(node *ast.Node, literal *ast.Node) ir.Expr {
	x := l.expression(literal)
	if x == nil {
		return nil
	}
	t := l.irType(l.typeAt(node), node)
	if t == nil {
		return nil
	}
	return l.convert(x, t, node)
}

// template lowers `head${x}middle${y}tail` to concatenations of the parts.
func (l *lowerer) template(node *ast.Node) ir.Expr {
	template := node.AsTemplateExpression()
	var result ir.Expr = &ir.StringConst{Value: template.Head.Text()}
	for _, span := range template.TemplateSpans.Nodes {
		span := span.AsTemplateSpan()
		x := l.valueExpression(span.Expression)
		if x == nil {
			return nil
		}
		str := l.toString(x, span.Expression)
		if str == nil {
			return nil
		}
		result = concat(concat(result, str), &ir.StringConst{Value: span.Literal.Text()})
	}
	return result
}

// concat joins two strings, leaving out empty constants.
func concat(x ir.Expr, y ir.Expr) ir.Expr {
	if s, ok := x.(*ir.StringConst); ok && s.Value == "" {
		return y
	}
	if s, ok := y.(*ir.StringConst); ok && s.Value == "" {
		return x
	}
	return &ir.Binary{Op: ir.Concat, X: x, Y: y}
}

// toString converts a primitive value to a string as JS's ToString does, or reports it.
func (l *lowerer) toString(x ir.Expr, node *ast.Node) ir.Expr {
	switch t := x.Type(); {
	case t == ir.String:
		return x
	case ir.IsNumeric(t) || t == ir.Bool || t == ir.Null || t == ir.Undefined || t == ir.Value || isUnion(t):
		return &ir.ToString{X: x}
	case isArray(t):
		return &ir.ArrayCall{Op: ir.ArrayJoin, Args: []ir.Expr{x, &ir.StringConst{Value: ","}}} // JS's Array.prototype.toString
	}
	l.notSupportedYet(node, fmt.Sprintf("converting %s to a string is", x.Type()))
	return nil
}

// propertyAccess lowers a property read: a binding of a module namespace (`m.x` after
// `import * as m`) or a builtin property; objects don't exist yet.
func (l *lowerer) propertyAccess(node *ast.Node) ir.Expr {
	if receiver := l.typeAt(node.Expression()); isUnionValue(receiver) && !l.isMemberOfAll(node) && !ast.IsOptionalChain(node) {
		l.notSupportedYet(node, fmt.Sprintf("reading `%s` of a %s (a union) is", node.AsPropertyAccessExpression().Name().Text(), l.checker.TypeToString(receiver)))
		return nil
	}
	if shape := l.tupleShapeOf(node.Expression()); shape != nil && node.AsPropertyAccessExpression().Name().Text() == "length" {
		object := l.expression(node.Expression())
		if object == nil {
			return nil
		}
		return discard(object, &ir.NumberConst{Value: float64(len(shape.Fields))})
	}
	if l.isArrayExpression(node.Expression()) {
		return l.arrayProperty(node)
	}
	if l.isMapExpression(node.Expression()) {
		return l.mapProperty(node)
	}
	if l.isRecordElement(node) {
		return l.recordElement(node, ir.MapGetChecked)
	}
	member := l.memberDeclaration(node)
	if field := l.membersAt(node).fields[member]; field != nil {
		return l.fieldAccess(node, field)
	}
	if static, ok := l.statics[member]; ok {
		if local := l.staticField(node, static, false); local != nil {
			return &ir.LocalRef{Local: local}
		}
		return nil
	}
	if l.isShapeProperty(member) || l.isMappedProperty(node) {
		if object, field := l.shapeProperty(node); field != nil {
			return &ir.FieldRef{X: object, Field: field}
		}
		return nil
	}
	if accessors, ok := l.accessorProperty(node); ok {
		return l.propertyRead(node, accessors)
	}
	if l.genericMethodAt(node) != nil {
		l.notSupportedYet(node, "generic methods as values are")
		return nil
	}
	if _, ok := l.membersAt(node).methods[member]; ok || l.isObjectTypeMethod(member) {
		return l.methodValue(node)
	}
	if _, ok := l.staticMethods[member]; ok {
		return l.methodValue(node)
	}
	if l.isNamespace(node.Expression()) {
		if x, ok := l.binding(node, l.target(node)); ok {
			return x
		}
	}
	if name := l.mathMember(node); name != "" {
		return l.mathConstant(node, name)
	}
	builtin := l.builtin(node)
	switch {
	case builtin != nil && !properties[builtin]:
		l.notSupportedYet(node, "methods as values are")
		return nil
	case !properties[builtin]:
		l.notSupportedYet(node, "`"+scanner.GetTextOfNode(node)+"` is")
		return nil
	}
	receiver := l.expression(node.Expression())
	if receiver == nil {
		return nil
	}
	return &ir.CallBuiltin{Builtin: builtin, Args: []ir.Expr{receiver}}
}

// isStringElement reports whether node is s[i] for a string s and a number i.
func (l *lowerer) isStringElement(node *ast.Node) bool {
	if node.Kind != ast.KindElementAccessExpression {
		return false
	}
	access := node.AsElementAccessExpression()
	return l.hasTypeFlags(access.Expression, checker.TypeFlagsStringLike) && l.hasTypeFlags(access.ArgumentExpression, checker.TypeFlagsNumberLike)
}

func (l *lowerer) hasTypeFlags(node *ast.Node, flags checker.TypeFlags) bool {
	return l.typeAt(node).Flags()&flags != 0
}

// stringElement lowers s[i] with one of the StringElement builtins (D-015).
func (l *lowerer) stringElement(node *ast.Node, builtin *ir.Builtin) ir.Expr {
	s, i := l.stringElementOperands(node)
	if s == nil {
		return nil
	}
	return &ir.CallBuiltin{Builtin: builtin, Args: []ir.Expr{s, i}}
}

// stringElementOperands lowers the string and the index of s[i], or reports the access and returns nils.
func (l *lowerer) stringElementOperands(node *ast.Node) (ir.Expr, ir.Expr) {
	switch {
	case !l.isStringElement(node):
		l.notSupportedYet(node, "element access other than indexing a string is")
		return nil, nil
	case l.isUnloweredOptionalChain(node):
		l.notSupportedYet(node, "optional chaining `?.` is")
		return nil, nil
	}
	access := node.AsElementAccessExpression()
	s := l.expression(access.Expression)
	i := l.expression(access.ArgumentExpression)
	if s == nil || i == nil {
		return nil, nil
	}
	if i = l.convert(i, ir.Number, access.ArgumentExpression); i == nil {
		return nil, nil
	}
	return s, i
}

// elementComparedWithString recognises s[i] compared with a string (either way round)
// and returns the two sides.
func (l *lowerer) elementComparedWithString(expr *ast.BinaryExpression) (element *ast.Node, other *ast.Node, ok bool) {
	element, other = ast.SkipParentheses(expr.Left), expr.Right
	if !l.isStringElement(element) {
		element, other = ast.SkipParentheses(expr.Right), expr.Left
	}
	ok = l.isStringElement(element) && l.hasTypeFlags(other, checker.TypeFlagsStringLike)
	return element, other, ok
}

// elementEquality lowers s[i] === t (or !==) so that an index out of range makes it
// false (true for !==), as undefined does in JS (D-015).
func (l *lowerer) elementEquality(op ir.BinaryOp, element *ast.Node, other *ast.Node) ir.Expr {
	s, i := l.stringElementOperands(element)
	t := l.expression(other)
	if s == nil || t == nil {
		return nil
	}
	var equals ir.Expr = &ir.CallBuiltin{Builtin: ir.StringElementEquals, Args: []ir.Expr{s, i, t}}
	if op == ir.Ne {
		equals = &ir.Unary{Op: ir.Not, X: equals}
	}
	return equals
}

func (l *lowerer) binary(node *ast.Node) ir.Expr {
	expr := node.AsBinaryExpression()
	operator := expr.OperatorToken.Kind
	switch {
	case operator == ast.KindPlusToken && l.hasTypeFlags(node, checker.TypeFlagsStringLike):
		return l.concatenation(node)
	case operator == ast.KindEqualsToken:
		return l.assignment(node)
	case operator == ast.KindInstanceOfKeyword:
		return l.instanceOf(node)
	case operator == ast.KindInKeyword && expr.Left.Kind == ast.KindPrivateIdentifier:
		return l.privateIn(node)
	case operator == ast.KindInKeyword && l.isRecordExpression(expr.Right):
		return l.inRecord(expr)
	case operator == ast.KindInKeyword && ast.IsStringLiteralLike(expr.Left):
		return l.propertyIn(expr)
	case operator == ast.KindQuestionQuestionToken && !l.isRecordElement(ast.SkipParentheses(expr.Left)):
		return l.nullishCoalescing(node)
	case (operator == ast.KindBarBarToken || operator == ast.KindQuestionQuestionToken) && l.isRecordElement(ast.SkipParentheses(expr.Left)):
		return l.recordElementFallback(node, ast.SkipParentheses(expr.Left), expr.Right, operator == ast.KindQuestionQuestionToken)
	case operator == ast.KindCommaToken:
		x, y := l.expression(expr.Left), l.expression(expr.Right)
		if x == nil || y == nil {
			return nil
		}
		return &ir.Sequence{X: x, Y: y}
	}
	if op, ok := logicalOps[operator]; ok {
		return l.logical(node, op, expr.Left, expr.Right)
	}
	if _, ok := check.CompoundOperator(operator); ok {
		return l.compoundAssignment(node)
	}
	op, ok := binaryOps[operator]
	if !ok {
		l.notSupportedYet(node, describe(node)+" is")
		return nil
	}
	if op == ir.Eq || op == ir.Ne {
		if element, other, ok := l.elementComparedWithString(expr); ok {
			return l.elementEquality(op, element, other)
		}
		if element, ok := l.recordElementComparedWithUndefined(expr); ok && !l.recordMayHoldNullish(element) {
			record, key := l.recordElementOperands(element)
			return l.recordHas(record, key, op == ir.Eq) // a missing key's undefined
		}
		if operand, matches, ok := isNullishTest(expr); ok {
			return l.nullishTest(op, operand, matches)
		}
	}
	x := l.expression(expr.Left)
	y := l.expression(expr.Right)
	if x == nil || y == nil {
		return nil
	}
	if loose := operator == ast.KindEqualsEqualsToken || operator == ast.KindExclamationEqualsToken; loose && (isUnion(x.Type()) || isUnion(y.Type())) {
		l.notSupportedYet(node, describe(node)+" on a union, other than with null or undefined, is") // JS converts between types
		return nil
	}
	return l.operation(node, op, x, y, expr.Left, expr.Right)
}

// operation applies op to x and y, the lowered left and right operands of node,
// converting them to the type the semantic pass chose for them.
func (l *lowerer) operation(node *ast.Node, op ir.BinaryOp, x ir.Expr, y ir.Expr, left *ast.Node, right *ast.Node) ir.Expr {
	if n, ok := l.info.OperandType(node); ok {
		x, y = l.convert(x, numericTypes[n], left), l.convert(y, numericTypes[n], right)
		if x == nil || y == nil {
			return nil
		}
	}
	if op == ir.Eq || op == ir.Ne {
		if isUnion(x.Type()) || isUnion(y.Type()) {
			return unionEquality(op, x, y)
		}
		x, y = commonClass(x, y)
	}
	if !acceptsOperands(op, x.Type(), y.Type()) {
		l.notSupportedYet(node, fmt.Sprintf("%s on %s and %s is", describe(node), x.Type(), y.Type()))
		return nil
	}
	return &ir.Binary{Op: op, X: x, Y: y}
}

// assignment lowers `target = value`.
func (l *lowerer) assignment(node *ast.Node) ir.Expr {
	expr := node.AsBinaryExpression()
	if left := ast.SkipParentheses(expr.Left); left.Kind == ast.KindArrayLiteralExpression || left.Kind == ast.KindObjectLiteralExpression {
		return l.destructuringAssignment(left, expr.Right)
	}
	target, ok := l.assignmentTarget(expr.Left)
	value := l.expression(expr.Right)
	if !ok || value == nil {
		return nil
	}
	if value = l.convert(value, target.valueType(), expr.Right); value == nil {
		return nil
	}
	if target.local != nil || target.field != nil || target.index != nil || target.arrayLength || target.key != nil {
		return target.store(value, false) // the store evaluates the instance (or array and index) first
	}
	return l.withObject(target, func(target place) ir.Expr { return target.store(value, false) })
}

// compoundAssignment lowers `x op= value` to `x = x op value`, and `x ||= value` to
// `x || (x = value)` (&&= likewise).
func (l *lowerer) compoundAssignment(node *ast.Node) ir.Expr {
	expr := node.AsBinaryExpression()
	operator, _ := check.CompoundOperator(expr.OperatorToken.Kind)
	target, ok := l.assignmentTarget(expr.Left)
	if !ok {
		return nil
	}
	if target.key != nil && (operator == ast.KindBarBarToken || operator == ast.KindQuestionQuestionToken) {
		return l.recordElementOrSet(target, expr.Right, operator == ast.KindQuestionQuestionToken)
	}
	if operator == ast.KindQuestionQuestionToken {
		return l.readAndStore(target, func(target place) ir.Expr { return l.nullishAssignment(target, expr.Right) })
	}
	return l.readAndStore(target, func(target place) ir.Expr {
		current, t := l.narrowed(expr.Left, target.read()), target.valueType()
		if op, ok := logicalOps[operator]; ok {
			value := l.expression(expr.Right)
			if value == nil {
				return nil
			}
			if value = l.convert(value, t, expr.Right); value == nil {
				return nil
			}
			return &ir.Logical{Op: op, X: current, Y: target.store(value, false)}
		}
		var value ir.Expr
		switch op, ok := binaryOps[operator]; {
		case operator == ast.KindPlusToken && t == ir.String:
			if y := l.valueExpression(expr.Right); y != nil {
				if y = l.toString(y, expr.Right); y != nil {
					value = &ir.Binary{Op: ir.Concat, X: current, Y: y}
				}
			}
		case ok:
			if y := l.expression(expr.Right); y != nil {
				value = l.operation(node, op, current, y, expr.Left, expr.Right)
			}
		default:
			l.notSupportedYet(node, describe(node)+" is")
		}
		if value == nil {
			return nil
		}
		if value = l.convert(value, t, node); value == nil {
			return nil
		}
		return target.store(value, false)
	})
}

// place is what an assignment stores to: a local, a field of an instance (or through an
// interface, a property of an object), an accessor property of an instance, whose
// setter stores (and getter reads), or an entry of a hash-map object.
type place struct {
	local       *ir.Local
	object      ir.Expr // the instance, for a field or accessor property; the array, for an element or the length; the hash-map object, for an entry
	index       ir.Expr // the index, for an element of an array
	key         ir.Expr // the key, for an entry of a hash-map object
	arrayLength bool    // the length of an array
	field       *ir.Field
	accessors   property
	current     *ir.Local // an accessor property's value, or a property's through an interface, read once by readAndStore
	stored      *ir.Local // the value stored in an accessor property, which is the assignment's value
	at          *ast.Node // the assignment's target, for an accessor property
	l           *lowerer
}

func (p place) valueType() ir.Type {
	switch {
	case p.local != nil:
		return p.local.Type
	case p.field != nil:
		return p.field.Type
	case p.index != nil:
		return p.object.Type().(*ir.Array).Elem
	case p.arrayLength:
		return ir.Number
	case p.key != nil:
		return p.object.Type().(*ir.MapType).Value
	}
	return p.stored.Type
}

func (p place) read() ir.Expr {
	switch {
	case p.local != nil:
		return &ir.LocalRef{Local: p.local}
	case p.current != nil:
		return &ir.LocalRef{Local: p.current}
	case p.index != nil:
		return &ir.ArrayCall{Op: ir.ArrayElementChecked, Args: []ir.Expr{p.object, p.index}}
	case p.arrayLength:
		return &ir.ArrayCall{Op: ir.ArrayLength, Args: []ir.Expr{p.object}}
	case p.key != nil:
		return &ir.MapCall{Op: ir.MapGetChecked, Args: []ir.Expr{p.object, p.key}, Map: p.object.Type().(*ir.MapType)}
	}
	return &ir.FieldRef{X: p.object, Field: p.field}
}

func (p place) store(value ir.Expr, postfix bool) ir.Expr {
	var stored ir.Expr
	switch {
	case p.local != nil:
		return &ir.Assign{Local: p.local, Value: value, Postfix: postfix}
	case p.field != nil && (!postfix || p.current == nil):
		return &ir.SetField{X: p.object, Field: p.field, Value: value, Postfix: postfix}
	case p.field != nil:
		stored = &ir.SetField{X: p.object, Field: p.field, Value: value}
	case p.index != nil:
		stored = &ir.ArrayCall{Op: ir.ArraySetElement, Args: []ir.Expr{p.object, p.index, value}}
	case p.arrayLength:
		stored = &ir.ArrayCall{Op: ir.ArraySetLength, Args: []ir.Expr{p.object, value}}
	case p.key != nil:
		stored = &ir.MapCall{Op: ir.MapPut, Args: []ir.Expr{p.object, p.key, value}, Map: p.object.Type().(*ir.MapType)}
	}
	if stored != nil {
		if postfix { // readAndStore read the old value into current
			return &ir.Sequence{X: stored, Y: &ir.LocalRef{Local: p.current}}
		}
		return stored
	}
	result := p.stored
	if postfix {
		result = p.current
	}
	set := p.l.callMethod(p.object, nil, p.accessors.setter, []ir.Expr{&ir.LocalRef{Local: p.stored}})
	return &ir.Bind{Local: p.stored, Value: value, In: &ir.Sequence{X: set, Y: &ir.LocalRef{Local: result}}}
}

// readAndStore builds, with build, an expression that both reads and stores target.
// A field's or property's instance is evaluated once, first, as in JS: if it isn't a
// local, build gets a place whose instance is a temporary holding it. A property's
// getter runs once, before build's expression; so does the read of a property through an
// interface, which a getter may provide.
func (l *lowerer) readAndStore(target place, build func(place) ir.Expr) ir.Expr {
	if target.local != nil {
		return build(target)
	}
	return l.withObject(target, func(target place) ir.Expr {
		var read ir.Expr
		_, viaInterface := target.object.Type().(*ir.Interface)
		switch {
		case target.field != nil && !viaInterface:
			return build(target)
		case target.field != nil || target.index != nil || target.arrayLength || target.key != nil:
			read = target.read()
		case target.accessors.getter == nil:
			l.notSupportedYet(target.at, "reading a property that has no getter is")
			return nil
		default:
			read = l.callMethod(target.object, nil, target.accessors.getter, nil)
		}
		target.current = l.newLocal("current", target.valueType())
		result := build(target)
		if read == nil || result == nil {
			return nil
		}
		return &ir.Bind{Local: target.current, Value: read, In: result}
	})
}

// withObject builds, with build, an expression that uses the instance of a field or
// property place (or the array and index of an element), each evaluated once into a
// temporary first unless it is a local or constant.
func (l *lowerer) withObject(target place, build func(place) ir.Expr) ir.Expr {
	var binds []*ir.Bind // outermost first
	bind := func(x *ir.Expr, name string) {
		if *x != nil && !isPure(*x) {
			local := l.newLocal(name, (*x).Type())
			binds = append(binds, &ir.Bind{Local: local, Value: *x})
			*x = &ir.LocalRef{Local: local}
		}
	}
	bind(&target.object, "object")
	bind(&target.index, "index")
	bind(&target.key, "key")
	result := build(target)
	if result == nil {
		return nil
	}
	for i := len(binds) - 1; i >= 0; i-- {
		binds[i].In = result
		result = binds[i]
	}
	return result
}

// assignmentTarget returns the place an assignment stores to, or reports the target
// and returns false.
func (l *lowerer) assignmentTarget(node *ast.Node) (place, bool) {
	node = ast.SkipParentheses(node)
	switch node.Kind {
	case ast.KindIdentifier:
		if local, ok := l.locals[l.target(node)]; ok {
			local = l.ownLocal(node, local) // nil also if its declaration failed, which was reported
			return place{local: local}, local != nil
		}
	case ast.KindElementAccessExpression:
		if field, ok := l.tupleElement(node); ok {
			if field == nil {
				return place{}, false // reported
			}
			object := l.expression(node.Expression())
			return place{object: object, field: field}, object != nil
		}
		if l.isArrayElement(node) {
			array, index := l.arrayElementOperands(node)
			return place{object: array, index: index}, array != nil
		}
		if l.isRecordElement(node) {
			record, key := l.recordElementOperands(node)
			return place{object: record, key: key}, record != nil
		}
	case ast.KindPropertyAccessExpression:
		if l.isRecordElement(node) {
			record, key := l.recordElementOperands(node)
			return place{object: record, key: key}, record != nil
		}
		if l.isArrayExpression(node.Expression()) && node.AsPropertyAccessExpression().Name().Text() == "length" {
			array := l.arrayObject(node)
			return place{object: array, arrayLength: true}, array != nil
		}
		if field := l.membersAt(node).fields[l.memberDeclaration(node)]; field != nil {
			object := l.fieldObject(node, field)
			return place{object: object, field: field}, object != nil
		}
		if static, ok := l.statics[l.memberDeclaration(node)]; ok {
			local := l.staticField(node, static, true)
			return place{local: local}, local != nil
		}
		if l.isShapeProperty(l.memberDeclaration(node)) || l.isMappedProperty(node) {
			object, field := l.shapeProperty(node)
			return place{object: object, field: field}, field != nil
		}
		if accessors, ok := l.accessorProperty(node); ok {
			return l.propertyPlace(node, accessors)
		}
	}
	l.notSupportedYet(node, "assigning to `"+scanner.GetTextOfNode(node)+"` is")
	return place{}, false
}

// propertyPlace makes the place of an assignment to an accessor property, or reports
// why it can't and returns false.
func (l *lowerer) propertyPlace(node *ast.Node, accessors property) (place, bool) {
	getter, setter := accessors.getter, accessors.setter
	switch {
	case setter == nil:
		l.notSupportedYet(node, "assigning to a property that has no setter is")
		return place{}, false
	case getter != nil && (getter.Class != setter.Class || getter.Func.Result != setter.Func.Params[1].Type):
		l.notSupportedYet(node, "a property whose getter and setter differ in class or type is")
		return place{}, false
	case l.isUnloweredOptionalChain(node):
		l.notSupportedYet(node, "optional chaining `?.` is")
		return place{}, false
	}
	object := l.expression(node.Expression())
	if object == nil {
		return place{}, false
	}
	if object = l.asInstanceOf(object, setter.Class, node.Expression()); object == nil {
		return place{}, false
	}
	stored := l.newLocal("value", setter.Func.Params[1].Type)
	return place{object: object, accessors: accessors, stored: stored, at: node, l: l}, true
}

// newLocal makes a local of the function being lowered.
func (l *lowerer) newLocal(name string, t ir.Type) *ir.Local {
	local := &ir.Local{Name: name, Type: t}
	l.owners[local] = l.function
	return local
}

// ownLocal returns local if the function being lowered may use it: its own, one at the
// module scope of any module, which becomes a global, or one of an enclosing function,
// which it captures (D-041). Otherwise it reports the use at node and returns nil: a
// class's methods can't use the locals of the code around the class.
func (l *lowerer) ownLocal(node *ast.Node, local *ir.Local) *ir.Local {
	switch {
	case local == nil || l.owners[local] == l.function:
		return local
	case l.moduleScope[local]:
		local.Global = true
		return local
	case l.capture(local):
		return local
	}
	l.notSupportedYet(node, "using `"+scanner.GetTextOfNode(node)+"` from outside the function is")
	return nil
}

// increment lowers ++ and -- (operator) on operand, prefix or postfix.
func (l *lowerer) increment(node *ast.Node, operand *ast.Node, operator ast.Kind, postfix bool) ir.Expr {
	target, ok := l.assignmentTarget(operand)
	if !ok {
		return nil
	}
	t := target.valueType()
	if u, ok := t.(*ir.Union); ok { // a union that tsc narrowed to its numeric member here
		if n := l.irType(l.typeAt(operand), operand); n != nil && ir.IsNumeric(n) && u.Has(n) {
			t = n
		}
	}
	if !ir.IsNumeric(t) {
		l.notSupportedYet(node, fmt.Sprintf("%s on %s is", describe(node), target.valueType()))
		return nil
	}
	op := ir.Add
	if operator == ast.KindMinusMinusToken {
		op = ir.Sub
	}
	return l.readAndStore(target, func(target place) ir.Expr {
		value := l.convert(&ir.Binary{Op: op, X: fromUnion(target.read(), t), Y: one(t)}, target.valueType(), node)
		return target.store(value, postfix)
	})
}

// one is the constant 1 of a numeric type.
func one(t ir.Type) ir.Expr {
	switch {
	case ir.IsInteger(t):
		return &ir.IntConst{Bits: 1, Ty: t}
	case t == ir.F32:
		return &ir.Convert{X: &ir.NumberConst{Value: 1}, To: ir.F32}
	}
	return &ir.NumberConst{Value: 1}
}

// conditional lowers `cond ? a : b`; the branches must have one type.
func (l *lowerer) conditional(node *ast.Node) ir.Expr {
	expr := node.AsConditionalExpression()
	cond := l.condition(expr.Condition)
	then, otherwise := l.sameType(node, expr.WhenTrue, expr.WhenFalse)
	if cond == nil || then == nil {
		return nil
	}
	return &ir.Conditional{Cond: cond, Then: then, Else: otherwise}
}

// logical lowers && and ||, whose operands must have one type.
func (l *lowerer) logical(node *ast.Node, op ir.LogicalOp, left *ast.Node, right *ast.Node) ir.Expr {
	if l.involvesUnion(node, left, right) {
		x, y := l.expression(left), l.expression(right)
		if x == nil || y == nil {
			return nil
		}
		return l.unionLogical(node, op, x, y, right)
	}
	x, y := l.sameType(node, left, right)
	if x == nil {
		return nil
	}
	return &ir.Logical{Op: op, X: x, Y: y}
}

// sameType lowers two expressions that must end up with one type (the branches of ?:,
// the operands of && and ||), converting numbers to the type the semantic pass chose.
// Either both results are nil or neither is.
func (l *lowerer) sameType(node *ast.Node, a *ast.Node, b *ast.Node) (ir.Expr, ir.Expr) {
	x, y := l.expression(a), l.expression(b)
	if x == nil || y == nil {
		return nil, nil
	}
	if t := l.numericType(node); t != nil {
		x, y = l.convert(x, t, a), l.convert(y, t, b)
		if x == nil || y == nil {
			return nil, nil
		}
	}
	x, y = commonClass(x, y)
	if x.Type() != y.Type() && isObject(x.Type()) && isObject(y.Type()) {
		x, y = l.asContextualInterface(node, x, y)
	}
	if u, ok := l.irType(l.typeAt(node), node).(*ir.Union); ok && x.Type() != y.Type() { // `c ? 1 : null`
		x, y = l.convert(x, u, a), l.convert(y, u, b)
		if x == nil || y == nil {
			return nil, nil
		}
	}
	if x.Type() != y.Type() {
		l.notSupportedYet(node, fmt.Sprintf("%s with operands of types %s and %s is", describe(node), x.Type(), y.Type()))
		return nil, nil
	}
	return x, y
}

// asContextualInterface converts two references to objects to the object type that the
// context of node expects (`const s: Shape = c ? circle : rect`), if both provide it.
// Otherwise they stay as they are.
func (l *lowerer) asContextualInterface(node *ast.Node, x ir.Expr, y ir.Expr) (ir.Expr, ir.Expr) {
	contextual := l.contextualType(node)
	if contextual == nil || !l.isObjectType(contextual) {
		return x, y
	}
	iface := l.interfaceOf(contextual, node)
	if iface == nil || unprovided(x.Type(), iface) != "" || unprovided(y.Type(), iface) != "" {
		return x, y
	}
	return &ir.ClassCast{X: x, To: iface}, &ir.ClassCast{X: y, To: iface}
}

// condition lowers an expression tested for truth: if and loop conditions, ?:, !.
// Any value can be tested, so s[i] keeps JS's undefined out of range (D-015).
func (l *lowerer) condition(node *ast.Node) ir.Expr {
	x := l.valueExpression(node)
	if x == nil {
		return nil
	}
	switch t := x.Type(); {
	case t == ir.Bool:
		return x
	case t == ir.Null || t == ir.Undefined:
		return discard(x, &ir.BoolConst{Value: false})
	case ir.IsNumeric(t) || t == ir.String || t == ir.Value || isUnion(t):
		return &ir.Truthy{X: x}
	case isObject(t) || isFunction(t) || isMap(t):
		return discard(x, &ir.BoolConst{Value: true}) // an object or function, never null
	}
	l.notSupportedYet(node, fmt.Sprintf("testing a %s for truth is", x.Type()))
	return nil
}

// typeOf lowers typeof, which is a constant for every static type.
func (l *lowerer) typeOf(node *ast.Node) ir.Expr {
	x := l.valueExpression(node.Expression())
	if x == nil {
		return nil
	}
	if x.Type() == ir.Value || isUnion(x.Type()) {
		return &ir.CallBuiltin{Builtin: ir.ValueTypeOf, Args: []ir.Expr{box(x)}}
	}
	word, ok := typeofWords[x.Type()]
	switch {
	case ir.IsNumeric(x.Type()):
		word, ok = "number", true
	case isObject(x.Type()) || isMap(x.Type()):
		word, ok = "object", true
	case isFunction(x.Type()):
		word, ok = "function", true
	}
	if !ok {
		l.notSupportedYet(node, fmt.Sprintf("typeof on %s is", x.Type()))
		return nil
	}
	return discard(x, &ir.StringConst{Value: word})
}

// discard evaluates x for its effects, if it may have any, and then has the value of then.
func discard(x ir.Expr, then ir.Expr) ir.Expr {
	if isPure(x) {
		return then
	}
	return &ir.Sequence{X: x, Y: then}
}

// isPure reports whether evaluating x can't have effects: a constant or a local.
func isPure(x ir.Expr) bool {
	switch x.(type) {
	case *ir.LocalRef, *ir.NumberConst, *ir.BoolConst, *ir.StringConst, *ir.IntConst, *ir.NullConst, *ir.UndefinedConst, *ir.Absent:
		return true
	}
	return false
}

// concatenation lowers `+` with a string operand: both sides become strings.
func (l *lowerer) concatenation(node *ast.Node) ir.Expr {
	expr := node.AsBinaryExpression()
	x := l.valueExpression(expr.Left)
	y := l.valueExpression(expr.Right)
	if x == nil || y == nil {
		return nil
	}
	x, y = l.toString(x, expr.Left), l.toString(y, expr.Right)
	if x == nil || y == nil {
		return nil
	}
	return &ir.Binary{Op: ir.Concat, X: x, Y: y}
}

// acceptsOperands reports whether the IR can apply op to operands of these types.
func acceptsOperands(op ir.BinaryOp, x ir.Type, y ir.Type) bool {
	if x != y {
		return false
	}
	switch op {
	case ir.Eq, ir.Ne:
		return ir.IsNumeric(x) || x == ir.Bool || x == ir.String || isObject(x) || isFunction(x) || isMap(x)
	case ir.Lt, ir.Le, ir.Gt, ir.Ge:
		return ir.IsNumeric(x) || x == ir.String
	}
	return ir.IsNumeric(x)
}

func (l *lowerer) prefixUnary(node *ast.Node) ir.Expr {
	expr := node.AsPrefixUnaryExpression()
	switch expr.Operator {
	case ast.KindExclamationToken:
		x := l.condition(expr.Operand)
		if x == nil {
			return nil
		}
		return &ir.Unary{Op: ir.Not, X: x}
	case ast.KindPlusPlusToken, ast.KindMinusMinusToken:
		return l.increment(node, expr.Operand, expr.Operator, false)
	case ast.KindMinusToken, ast.KindPlusToken, ast.KindTildeToken:
	default:
		l.notSupportedYet(node, describe(node)+" is")
		return nil
	}
	if expr.Operand.Kind == ast.KindNumericLiteral && expr.Operator != ast.KindTildeToken {
		return l.numericLiteral(expr.Operand, expr.Operator == ast.KindMinusToken)
	}
	x := l.expression(expr.Operand)
	if x == nil {
		return nil
	}
	if !ir.IsNumeric(x.Type()) {
		l.notSupportedYet(node, fmt.Sprintf("%s on %s is", describe(node), x.Type()))
		return nil
	}
	switch expr.Operator {
	case ast.KindPlusToken:
		return x // +x is x for a numeric value
	case ast.KindTildeToken:
		return &ir.Unary{Op: ir.BitNot, X: x}
	}
	return &ir.Unary{Op: ir.Neg, X: x}
}

// builtin returns the runtime function a member access refers to, or nil if it isn't
// a builtin member of the prelude or the ES library.
func (l *lowerer) builtin(member *ast.Node) *ir.Builtin {
	symbol := l.checker.GetSymbolAtLocation(member)
	if symbol == nil || symbol.Parent == nil || symbol.ValueDeclaration == nil {
		return nil
	}
	file := ast.GetSourceFileOfNode(symbol.ValueDeclaration)
	if !lib.IsLibPath(file.FileName()) && !l.program.IsSourceFileDefaultLibrary(file.Path()) {
		return nil
	}
	return builtins[symbol.Parent.Name+"."+symbol.Name]
}

func isClass(t ir.Type) bool {
	_, ok := t.(*ir.Class)
	return ok
}

// isObject reports whether t is the type of a reference to an object: a class (or
// shape), an interface or an array.
func isObject(t ir.Type) bool {
	_, isInterface := t.(*ir.Interface)
	return isClass(t) || isInterface || isArray(t)
}

func box(value ir.Expr) ir.Expr {
	switch value.Type() {
	case ir.Value:
		return value
	case ir.Void:
		return &ir.Box{X: &ir.Sequence{X: value, Y: &ir.UndefinedConst{}}} // a call of a function that returns nothing is undefined
	}
	return &ir.Box{X: value}
}

// ---------------------------------------------------------------- types

// irType maps a checker type to its IR type, or reports it (at node) and returns nil.
func (l *lowerer) irType(t *checker.Type, node *ast.Node) ir.Type {
	t = l.instantiate(t)
	if generic, ok := l.genericClasses[t.Symbol()]; ok && t.Flags()&checker.TypeFlagsObject != 0 {
		if generic == nil {
			return nil // the class failed, which was reported
		}
		return l.classInstanceOf(generic, t, node)
	}
	if class, ok := l.classes[t.Symbol()]; ok && t.Flags()&checker.TypeFlagsObject != 0 {
		if class == nil {
			return nil // the class failed, which was reported
		}
		return class
	}
	if l.checker.IsArrayType(t) {
		return l.arrayType(t, node)
	}
	if checker.IsTupleType(t) {
		return l.tupleType(t, node)
	}
	if mt, ok := l.mapType(t, node); ok {
		if mt == nil {
			return nil // reported
		}
		return mt
	}
	if mt, ok := l.recordType(t, node); ok {
		if mt == nil {
			return nil // reported
		}
		return mt
	}
	if ft, ok := l.functionType(t, node); ok {
		if ft == nil {
			return nil // reported
		}
		return ft
	}
	if l.isObjectType(t) {
		if iface := l.interfaceOf(t, node); iface != nil {
			return iface
		}
		return nil
	}
	if n, ok := check.NumericOf(t); ok {
		return numericTypes[n] // number, a prelude numeric type, or a union of them such as -1 | 0 | 1
	}
	flags := t.Flags()
	switch {
	case flags&checker.TypeFlagsBooleanLike != 0:
		return ir.Bool
	case flags&checker.TypeFlagsStringLike != 0:
		return ir.String
	case flags&checker.TypeFlagsUndefined != 0:
		return ir.Undefined
	case flags&checker.TypeFlagsNull != 0:
		return ir.Null
	case flags&checker.TypeFlagsUnion != 0 && allOf(t.Types(), checker.TypeFlagsStringLike):
		return ir.String
	case flags&checker.TypeFlagsUnion != 0:
		return l.unionType(t, node)
	}
	l.notSupportedYet(node, fmt.Sprintf("values of type %s are", l.checker.TypeToString(t)))
	return nil
}

// allOf reports whether every one of types has one of flags.
func allOf(types []*checker.Type, flags checker.TypeFlags) bool {
	for _, t := range types {
		if t.Flags()&flags == 0 {
			return false
		}
	}
	return true
}

// ---------------------------------------------------------------- messages

// describe names a node's construct for an error message: "if statement", "the `+` operator".
func describe(node *ast.Node) string {
	switch node.Kind {
	case ast.KindBinaryExpression:
		return "the `" + scanner.TokenToString(node.AsBinaryExpression().OperatorToken.Kind) + "` operator"
	case ast.KindPrefixUnaryExpression:
		return "the prefix `" + scanner.TokenToString(node.AsPrefixUnaryExpression().Operator) + "` operator"
	case ast.KindPostfixUnaryExpression:
		return "the postfix `" + scanner.TokenToString(node.AsPostfixUnaryExpression().Operator) + "` operator"
	}
	return kindWords(node.Kind)
}

// kindWords turns KindIfStatement into "if statement".
func kindWords(kind ast.Kind) string {
	name := strings.TrimPrefix(kind.String(), "Kind")
	var words strings.Builder
	for i, r := range name {
		if unicode.IsUpper(r) {
			if i > 0 {
				words.WriteByte(' ')
			}
			r = unicode.ToLower(r)
		}
		words.WriteRune(r)
	}
	return words.String()
}
