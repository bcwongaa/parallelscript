// Package lower turns a type-checked TypeScript AST into ParallelScript IR. It is
// where the compiler decides what it can compile: anything it cannot lower yet
// becomes a diagnostic.
package lower

import (
	"context"
	"fmt"
	"maps"
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
	"String.length":            ir.StringLength,
	"String.charCodeAt":        ir.StringCharCodeAt,
	"String.slice":             ir.StringSlice,
	"String.substring":         ir.StringSubstring,
	"String.indexOf":           ir.StringIndexOf,
	"String.includes":          ir.StringIncludes,
	"String.startsWith":        ir.StringStartsWith,
	"String.endsWith":          ir.StringEndsWith,
	"String.toUpperCase":       ir.StringToUpperCase,
	"String.toLowerCase":       ir.StringToLowerCase,
	"String.trim":              ir.StringTrim,
	"String.trimStart":         ir.StringTrimStart,
	"String.trimEnd":           ir.StringTrimEnd,
	"String.trimLeft":          ir.StringTrimStart, // deprecated aliases
	"String.trimRight":         ir.StringTrimEnd,
	"String.padStart":          ir.StringPadStart,
	"String.padEnd":            ir.StringPadEnd,
	"String.repeat":            ir.StringRepeat,
	"String.split":             ir.StringSplit,
	"String.at":                ir.StringAt,
	"String.charAt":            ir.StringCharAt,
	"String.codePointAt":       ir.StringCodePointAt,
	"String.lastIndexOf":       ir.StringLastIndexOf,
	"String.substr":            ir.StringSubstr,
	"String.isWellFormed":      ir.StringIsWellFormed,
	"String.toWellFormed":      ir.StringToWellFormed,
	"String.toLocaleUpperCase": ir.StringToUpperCase, // the default locale's mappings are the root's
	"String.toLocaleLowerCase": ir.StringToLowerCase,
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
	ir.StringSlice:       {fromStart, toEnd},
	ir.StringSubstring:   {nil, toEnd},
	ir.StringIndexOf:     {nil, fromStart},
	ir.StringIncludes:    {nil, fromStart},
	ir.StringStartsWith:  {nil, fromStart},
	ir.StringEndsWith:    {nil, toEnd},
	ir.StringPadStart:    {nil, &ir.StringConst{Value: " "}},
	ir.StringPadEnd:      {nil, &ir.StringConst{Value: " "}},
	ir.StringSplit:       {nil, toEnd},
	ir.StringAt:          {fromStart},
	ir.StringCharAt:      {fromStart},
	ir.StringCodePointAt: {fromStart},
	ir.StringLastIndexOf: {nil, fromStart},
	ir.StringSubstr:      {fromStart, toEnd},
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
		statics: map[*ast.Node]*ir.Local{}, staticMethods: map[*ast.Node]*ir.Func{}, staticClasses: map[*ir.Func]*ir.Class{}, staticAccessors: map[*ast.Symbol]staticProperty{}, unionsInProgress: map[*checker.Type]int{}, recursiveUnions: map[*checker.Type]bool{}, genericConsts: map[*ast.Symbol]*genericFunc{},
		yielded: map[*ast.Node]ir.Expr{}, kept: map[*ast.Node]ir.Expr{}, hoisting: map[*ast.Node]bool{}, shapesByType: map[*checker.Type]*ir.Class{}, shapesByKey: map[string]*ir.Class{}, typesInProgress: map[ir.Type]bool{}, recursiveTypes: map[ir.Type]bool{},
		interfacesByType: map[*checker.Type]*ir.Interface{}, interfacesByKey: map[string]*ir.Interface{}, arrays: map[ir.Type]*ir.Array{ir.String: ir.StringArray, ir.Value: ir.ValueArray, ir.Number: ir.NumberArray}, arraysInProgress: map[*checker.Type]bool{},
		funcTypesInProgress: map[*checker.Type]bool{}, arrows: map[*ir.Func]bool{}, funcNodes: map[*ir.Func]*ast.Node{}, helpers: map[helperKey]*ir.Func{},
		mapsInProgress: map[*checker.Type]bool{}, chainObjects: map[*ast.Node]chainObject{}, chainRests: map[*ast.Node]bool{}, generics: map[*ast.Symbol]*genericFunc{}, tableMethodNames: map[string]bool{},
		genericClasses: map[*ast.Symbol]*genericClass{}, genericStatics: map[*ast.Node]*genericFunc{}, classInstances: map[*ir.Class]*classInstance{}, membersStarted: map[*ir.Class]bool{},
		tuples: map[string]*ir.Class{}, inKeys: map[string]bool{}, tuplesInProgress: map[*checker.Type]bool{}, bindings: map[*ir.Func][]*parameterBinding{}, paramLocals: map[*ir.Func][]*ir.Local{}, rests: map[*ir.Array]*ir.Rest{}, libObjectTypes: map[*ast.Symbol]bool{},
		looseSymbols: map[*ast.Symbol]bool{}, intrinsics: map[*ast.Symbol]*ir.Func{}, externs: map[string]*ir.Func{}}
	l.globalBindings = l.globalModuleBindings(program.Modules)
	l.collectUndefinedTests(program.Modules)
	result := &ir.Program{}
	for _, file := range program.Modules {
		module := newModule(file, file == program.Entry)
		if file == program.Entry && hasTopLevelAwait(file.AsNode()) {
			module.Init.Generator, module.Init.Async, module.Init.Result = true, true, ir.Value // D-113
		}
		l.funcNodes[module.Init] = file.AsNode()
		result.Modules = append(result.Modules, module)
		result.Funcs = append(result.Funcs, module.Init)
	}
	result.Main = result.Modules[len(result.Modules)-1].Init
	// Every class, function and module-scope binding exists before any code is lowered:
	// code may use them above their declarations, and in an import cycle before their
	// module runs. Classes come first, since any declaration's type may name one.
	l.function = result.Main
	l.declareErrorClasses()
	l.declareDateClass()
	l.declareTypedArrayClasses()
	l.declareRegExpClass()
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
	l.prepareDynamicTypes(result.Funcs)
	result.Classes = append(orderedByParent(slices.Concat(l.builtinClasses, l.classList, l.instanceClasses)), l.shapeList...)
	result.Interfaces = l.interfaceList
	result.InKeys = slices.Sorted(maps.Keys(l.inKeys))
	l.markAccessorProperties()
	l.checkRelaxedCasts(result.Funcs)
	markCells(result.Funcs)
	l.unexplainedFailure()
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
	module.Init = &ir.Func{Name: "main", Module: module, Pos: ir.Pos{File: lib.ModuleSpecifier(file.FileName()), Line: 1}, Result: ir.Void}
	return module
}

type lowerer struct {
	program             *compiler.Program
	checker             *checker.Checker
	argumentTemps       []*ir.Bind // the call being lowered's arguments evaluated before it (withArgumentTemps)
	staticAccessors     map[*ast.Symbol]staticProperty
	unionsInProgress    map[*checker.Type]int  // the unions irType is mapping, with how often each is
	recursiveUnions     map[*checker.Type]bool // the unions found to recur through arrays or hash-map objects (D-089)
	genericClassList    []*genericClass
	info                *check.Info
	locals              map[*ast.Symbol]*ir.Local        // nil for a declaration that failed to lower
	owners              map[*ir.Local]*ir.Func           // the function each local belongs to
	moduleScope         map[*ir.Local]bool               // the locals declared at module scope, which functions and other modules may use
	funcs               map[*ast.Symbol]*ir.Func         // every function declaration, made before any body is lowered; nil if it failed
	generics            map[*ast.Symbol]*genericFunc     // every generic function declaration, whose instances calls make
	genericStatics      map[*ast.Node]*genericFunc       // every generic static method, by declaration
	instances           []*instance                      // the instances whose bodies are still to be lowered
	genericMethodList   []*genericFunc                   // the generic instance methods of every class, in the order they were declared
	tableMethodNames    map[string]bool                  // the names whose generic methods have erased copies in method tables (D-078)
	allMethodsInTables  bool                             // every generic method has its erased copy in its class's method table
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
	typesInProgress     map[ir.Type]bool                 // the shapes and interfaces being made
	recursiveTypes      map[ir.Type]bool                 // shapes and interfaces their own members' types refer to
	shapeList           []*ir.Class                      // the shapes, in the order they were made
	interfacesByType    map[*checker.Type]*ir.Interface  // the interface of each object type met; nil if it has none
	interfacesByKey     map[string]*ir.Interface         // the interfaces, by their members (membersKey)
	interfaceList       []*ir.Interface                  // the interfaces, in the order they were made
	relaxedCasts        []relaxedCast                    // conversions to interfaces whose objects may hold optional properties otherwise (D-066)
	inKeys              map[string]bool                  // the keys `in` looks up at run time (D-067)
	firstDynamicWrite   *ast.Node                        // the first write through `any`, if any (D-072)
	firstDynamicCall    *ast.Node                        // the first call through `any`, if any (D-074)
	reports             int                              // how many diagnostics were reported, repeats included
	genericConsts       map[*ast.Symbol]*genericFunc     // module-scope consts of generic arrow functions, whose calls are monomorphized (D-102)
	unexplained         *ast.Node                        // the first expression that failed without a diagnostic
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
	finallies           []*finallyContext                // the try statements with finally blocks whose protected code is being lowered, innermost last (D-068)
	yielded             map[*ast.Node]ir.Expr            // the value of each yield expression whose statement yielded already (D-109)
	kept                map[*ast.Node]ir.Expr            // the value of each operand evaluated before a yield in its statement (D-111)
	hoisting            map[*ast.Node]bool               // the statements being lowered after their yields were hoisted
	hoistedNodes        []*ast.Node                      // the nodes in yielded and kept by hoisting, to forget after their statements
	builtinClasses      []*ir.Class                      // Error and its native subclasses (D-068), Date (D-087), typed arrays and ArrayBuffer (D-125), the runtime's
	dateClass           *ir.Class                        // Date (D-087)
	dates               *dateFunctions                   // the runtime functions behind Date
	typed               *typedArrays                     // typed arrays and ArrayBuffer (D-125)
	regexpClass         *ir.Class                        // RegExp (D-133)
	externs             map[string]*ir.Func              // the runtime functions calls reach directly, by C name and types (extern)
	libObjectTypes      map[*ast.Symbol]bool             // the library's interfaces that are object types: ErrorOptions
	errors              []diagnostic.Diagnostic
	undefinedTested     map[*ast.Symbol]bool        // variables the program tests for undefined (collectUndefinedTests)
	looseSymbols        map[*ast.Symbol]bool        // loose locals and the variables known not to be (isLoose)
	intrinsics          map[*ast.Symbol]*ir.Func    // the extern functions of the pts: modules' intrinsics (D-116); nil if reported
	globalBindings      map[*ast.Symbol]*ast.Symbol // the globals that are a pts: module's default export, with the module's binding
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
	return ir.Pos{File: lib.ModuleSpecifier(file.FileName()), Line: scanner.GetECMALineOfPosition(file, start) + 1}
}

func (l *lowerer) notSupportedYet(node *ast.Node, what string) {
	l.report(diagnostic.At(node, diagnostic.NotSupportedYet, "%s not supported yet", what))
}

func (l *lowerer) unsupported(node *ast.Node, format string, args ...any) {
	l.report(diagnostic.At(node, diagnostic.Unsupported, format, args...))
}

// report records an error once: each instance of a generic function lowers its body again (D-049).
func (l *lowerer) report(d diagnostic.Diagnostic) {
	l.reports++
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
	if l.function.Generator && !l.hoisting[node] {
		if yield := statementYield(node); yield != nil && l.yielded[yield] == nil && !l.hasYield(yield.Expression()) {
			return l.yieldingStatement(node, yield)
		}
		if expression := hoistableExpression(node); expression != nil && l.hasYield(expression) {
			return l.hoistingStatement(node, expression) // D-111
		}
	}
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
		if generic, ok := l.genericClasses[l.declaredSymbol(node)]; ok {
			l.bodies = append(l.bodies, node) // its static methods; each instance lowers its constructor and methods
			if generic != nil {
				return l.staticInitializers(node, generic.statics)
			}
			break
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
	case ast.KindThrowStatement:
		return l.throwStatement(node)
	case ast.KindTryStatement:
		return l.tryStatement(node)
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
		return l.jumpTo(l.pos(node), i, isContinue)
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
		read := l.expression
		if l.isLoose(symbol) {
			read = l.valueExpression // the element, or undefined past the end (D-090)
		}
		if init = read(initializer); init == nil {
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
	t := l.numericType(node)
	if t == nil {
		t = l.irType(l.typeOfSymbol(symbol), node)
	}
	if t != nil && l.isLoose(symbol) {
		return l.withUndefined(t) // D-090
	}
	return t
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
// A global that is a pts: module's default export names the module's binding (D-116).
func (l *lowerer) followAliases(symbol *ast.Symbol) *ast.Symbol {
	if binding, ok := l.globalBindings[symbol]; ok {
		return binding
	}
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
		return implementationOf(symbol.ValueDeclaration)
	}
	return nil
}

// implementationOf is the implementation of a method declared with overloads, the
// declaration with a body, for declaration, one of its overloads; otherwise declaration.
func implementationOf(declaration *ast.Node) *ast.Node {
	if declaration == nil || !isOverloadSignature(declaration) {
		return declaration
	}
	for _, d := range declaration.Symbol().Declarations {
		if d.Kind == declaration.Kind && d.Body() != nil {
			return d
		}
	}
	return declaration
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
	case value.Type() == ir.Void && target != ir.Void: // a call of a function that returns never
		return &ir.Sequence{X: value, Y: &ir.LeftOut{T: target}}
	case target == ir.Value:
		return box(value)
	case value.Type() == ir.Value && isCheckable(target): // a dynamic value into a typed place: checked (D-070)
		return &ir.FromUnion{X: value, To: target}
	case value.Type() == ir.Value && l.isDynamicSource(node):
		return l.dynamicConversion(value, target, node)
	case isUnion(target) && value.Type() == ir.Value && !isCheckable(target) && l.holdsDynamicData(target.(*ir.Union)):
		return l.dynamicConversion(value, target, node) // an element of a recursive type's array, which may be any object (D-089)
	case isUnion(target) && value.Type() == ir.Value:
		return &ir.ToUnion{X: value, To: target.(*ir.Union)} // a dynamic value tsc types as the union, such as `a.find(…)`'s
	case value.Type() == ir.Value:
		return l.dynamicConversion(value, target, node)
	case isUnion(target):
		return l.toUnion(value, target.(*ir.Union), node)
	case ir.IsNumeric(value.Type()) && ir.IsNumeric(target):
		return &ir.Convert{X: value, To: target}
	case isFunction(value.Type()) && isFunction(target):
		return l.adapt(value, target.(*ir.FuncType), node)
	case isObject(value.Type()) && isObject(target) && !isArray(value.Type()) && !isArray(target):
		return l.objectCast(value, target, node)
	case isUnion(value.Type()) && isUpcastOfMembers(value.Type().(*ir.Union), target):
		return &ir.FromUnion{X: value, To: target} // `Dog | Bird` as an Animal
	case isEmptyArrayLiteral(value) && isArray(target):
		return &ir.NewArray{Array: target.(*ir.Array)} // `[]` nested in a literal, typed never[], fits any array type
	case isDynamicArray(value.Type()) && isArray(target):
		return l.dynamicConversion(box(value), target, node) // an any[] (`new Array(n).fill(0)`, `Array.from(…, () => [])`) copied and checked, as dynamic data is (D-071)
	case (isArray(value.Type()) || isTuple(value.Type())) && isArray(target) && (l.isNewArray(node) || l.isReadOnlyArgument(node)):
		if isTuple(value.Type()) {
			return l.tupleAsArray(value, target.(*ir.Array), node)
		}
		return l.dynamicConversion(box(value), target, node) // `leaves.map(…)` as an HNode[]: a copy no one can tell from the array (D-094)
	case (isArray(value.Type()) || isTuple(value.Type())) && isArray(target):
		l.notSupportedYet(node, fmt.Sprintf("using a %s as a %s where it may be changed or kept (a copy wouldn't show the changes; a new array, or one passed to a parameter that is only read, is copied) is", value.Type(), target))
		return nil
	case isObject(value.Type()) && isRecord(target):
		l.report(diagnostic.At(node, diagnostic.NotSupportedYet, "using a `%s` as a hash-map object (`%s`) is not supported yet; a copy of its properties, `{ ...o }`, is one", value.Type(), target))
		return nil
	default:
		l.notSupportedYet(node, fmt.Sprintf("converting %s to %s is", value.Type(), target))
		return nil
	}
}

// holdsDynamicData reports whether a member of u is dynamic data (isDynamicData), which a
// dynamic value converts to by a checked conversion, since its object may be of another
// representation (a literal's shape where a recursive type's hash-map object goes).
func (l *lowerer) holdsDynamicData(u *ir.Union) bool {
	return slices.ContainsFunc(u.Members, isDynamicData)
}

// isDynamicArray reports whether t is an array of dynamic values, or of such arrays.
func isDynamicArray(t ir.Type) bool {
	array, ok := t.(*ir.Array)
	return ok && (array.Elem == ir.Value || isDynamicArray(array.Elem))
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
		if child.Kind == ast.KindVariableStatement && node.Kind == ast.KindSourceFile && l.subst == nil {
			l.declareGenericConsts(child)
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
	}
	fn := &ir.Func{Name: name, Module: l.function.Module, Pos: l.pos(node), Length: jsLength(node)}
	stateMachine(fn, flags)
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
// functionProperty is the builtin that reads `f.name` or `f.length` (node) of a function
// value f, or nil if node is no such read (D-098).
func (l *lowerer) functionProperty(node *ast.Node) *ir.Builtin {
	name := node.AsPropertyAccessExpression().Name().Text()
	if name != "name" && name != "length" || ast.IsWriteAccess(node) {
		return nil
	}
	t := l.typeAt(node.Expression())
	if len(l.checker.GetSignaturesOfType(t, checker.SignatureKindCall)) == 0 || len(l.checker.GetPropertiesOfType(t)) > 0 && l.checker.GetPropertyOfType(t, name) != nil && !l.isDeclaredInDefaultLib(l.checker.GetPropertyOfType(t, name)) {
		return nil // not a function, or one with a property of that name of its own
	}
	if name == "name" {
		return ir.FunctionName
	}
	return ir.FunctionLength
}

// jsLength is a function's `length` in JS: how many parameters its declaration has
// before the first with a default and the rest parameter, TS's `this` not counted (D-098).
func jsLength(node *ast.Node) int {
	n := 0
	for _, param := range node.Parameters() {
		declaration := param.AsParameterDeclaration()
		if declaration.Initializer != nil || declaration.DotDotDotToken != nil {
			break
		}
		if name := param.Name(); name.Kind != ast.KindIdentifier || name.Text() != "this" {
			n++
		}
	}
	return n
}

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
	if tuple, ok := t.(*ir.Class); ok && node.AsParameterDeclaration().DotDotDotToken != nil {
		// `...args: [string, number]`: a parameter per element, as tsc expands it in
		// function types (signatureType), which the body starts by making the tuple of.
		binding := &parameterBinding{node: node, value: local, elements: make([]*ir.Local, 0, len(tuple.Fields))}
		for i, field := range tuple.Fields {
			binding.elements = append(binding.elements, l.newLocal(fmt.Sprintf("%s_%d", local.Name, i), field.Type))
		}
		l.bindings[l.function] = append(l.bindings[l.function], binding)
		return binding.elements
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
	var t *checker.Type
	switch {
	case param.DotDotDotToken != nil:
		t = l.instantiateMade(l.checker.GetTypeOfSymbol(node.Symbol())) // in erased code, any[] for a T[] (D-078)
		if !l.checker.IsArrayType(t) && !checker.IsTupleType(t) {
			l.notSupportedYet(node, fmt.Sprintf("rest parameters of type %s are", l.checker.TypeToString(t)))
			return nil
		}
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
	if result.Flags()&(checker.TypeFlagsVoid|checker.TypeFlagsNever) != 0 {
		return ir.Void // a function that returns never only throws
	}
	if contextual := l.contextualResult(node, result); contextual != nil {
		result = contextual // `(input) => c ? { ok: true, … } : { ok: false, … }` as a Parser (D-095)
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
		return l.leaveFunction(at, nil)
	}
	value := l.expression(node.Expression())
	if value == nil {
		return nil
	}
	if l.function.Result == ir.Void {
		// `return f()` in a function that returns nothing still calls f.
		if isPure(value) {
			return l.leaveFunction(at, nil)
		}
		return append([]ir.Stmt{&ir.ExprStmt{At: at, X: value}}, l.leaveFunction(at, nil)...)
	}
	if value = l.convert(value, l.function.Result, node.Expression()); value == nil {
		return nil
	}
	return l.leaveFunction(at, value)
}

// ---------------------------------------------------------------- expressions

// expression lowers an expression, or reports it and returns nil.
func (l *lowerer) expression(node *ast.Node) ir.Expr {
	reports := l.reports
	x := l.withArgumentTemps(func() ir.Expr { return l.expressionOf(node) })
	if x == nil && l.reports == reports && l.unexplained == nil {
		l.unexplained = node // reported at the end if nothing else is (unexplainedFailure)
	}
	return x
}

// unexplainedFailure reports the first expression whose lowering failed without saying
// why, if the program has no other error: a use of a declaration that failed fails
// quietly, but without an error to explain it, the statement holding it would be dropped
// from a program that builds.
func (l *lowerer) unexplainedFailure() {
	if l.unexplained != nil && len(l.errors) == 0 {
		l.report(diagnostic.At(l.unexplained, diagnostic.NotSupportedYet, "%s (which the compiler failed to lower without saying why) is not supported yet", describe(l.unexplained)))
	}
}

func (l *lowerer) expressionOf(node *ast.Node) ir.Expr {
	if object, ok := l.chainObjects[node]; ok {
		return object.value
	}
	if x, ok := l.kept[node]; ok {
		return x // evaluated before a yield (D-111)
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
	case ast.KindRegularExpressionLiteral:
		return l.regexLiteral(node)
	case ast.KindTrueKeyword:
		return &ir.BoolConst{Value: true}
	case ast.KindFalseKeyword:
		return &ir.BoolConst{Value: false}
	case ast.KindNullKeyword:
		return &ir.NullConst{}
	case ast.KindIdentifier:
		return l.narrowed(node, l.identifier(node))
	case ast.KindParenthesizedExpression, ast.KindSatisfiesExpression:
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
		if l.isAny(node.Expression()) {
			return l.dynamicRead(node)
		}
		if x, ok := l.enumMember(node); ok {
			return x
		}
		if l.isMatchElement(node) {
			return l.matchElement(node)
		}
		if (l.isArrayElement(node) || l.isTypedArrayElement(node) || l.isStringElement(node) || l.isRecordElement(node)) && (l.contextTakesUndefined(node) || mayHoldUndefined(l.typeAt(node))) {
			x := l.valueExpression(node)
			if x == nil {
				return nil
			}
			t := l.contextualType(node)
			if !l.contextTakesUndefined(node) {
				t = l.typeAt(node) // an element of a `Partial<Record<K, V>>`, which may be undefined
			}
			return l.convert(x, l.irType(t, node), node) // `return list[9]` for a T | undefined: undefined, as in JS
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
		if l.isTypedArrayElement(node) {
			return l.typedArrayElement(node, false)
		}
		if l.isRecordElement(node) {
			return l.narrowed(node, l.recordElement(node, ir.MapGetChecked))
		}
		if l.isKeyedElement(node) {
			return l.keyedElementRead(node)
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
	case ast.KindExpressionWithTypeArguments:
		return l.instantiationExpression(node)
	case ast.KindDeleteExpression:
		return l.deleteExpression(node)
	case ast.KindYieldExpression, ast.KindAwaitExpression:
		return l.yieldExpression(node)
	}
	l.notSupportedYet(node, describe(node)+" is")
	return nil
}

// valueExpression lowers an expression whose context takes any value, such as an
// argument of console.log. There s[i] and a[i] keep JS's undefined for an index out of
// range (D-015), and o[k] for a key a hash-map object doesn't have (D-059); everything
// else lowers as usual.
func (l *lowerer) valueExpression(node *ast.Node) ir.Expr {
	if x, ok := l.kept[node]; ok {
		return x // evaluated before a yield (D-111)
	}
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
	case l.isTypedArrayElement(inner):
		return l.typedArrayElement(inner, true)
	case l.isRecordElement(inner):
		return l.recordElement(inner, ir.MapGet)
	}
	return unnarrowed(l.expression(node))
}

// unnarrowed is x as the union value it was narrowed from, if it was narrowed, for a
// place that takes any value: tsc narrows `const v: T | undefined = list[9]` to T, though
// the value is undefined (D-089).
func unnarrowed(x ir.Expr) ir.Expr {
	if narrowing, ok := x.(*ir.FromUnion); ok && !narrowing.Checked {
		if inner, boxed := narrowing.X.(*ir.Box); boxed && isUnion(inner.X.Type()) {
			return inner.X
		}
		if isUnion(narrowing.X.Type()) {
			return narrowing.X
		}
	}
	return x
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
	if l.isConversionFunction(node) {
		return l.conversionFunction(node.Text(), node)
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
	if generic, ok := l.genericConsts[symbol]; ok && l.makesGenericInstances(generic, map[*genericFunc]bool{}) {
		if arguments := l.contextualTypeArguments(node, generic, l.typeAt(node)); arguments != nil {
			if fn := l.genericInstance(generic, arguments, node); fn != nil {
				return l.functionValue(node, fn), true // the instance its context implies, as a declaration's (D-078, D-102)
			}
			return nil, true
		}
	}
	if local, ok := l.locals[symbol]; ok {
		if local = l.ownLocal(node, local); local == nil {
			return nil, true
		}
		return &ir.LocalRef{Local: local}, true
	}
	if generic, ok := l.generics[symbol]; ok {
		if generic == nil {
			return nil, true // its declaration failed, which was reported
		}
		return l.genericValue(node, generic), true
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
	if enum := l.enumOf(node); enum != nil {
		return l.enumObject(enum), true
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
	if callee := ast.SkipParentheses(call.Expression); l.isAny(callee) || ast.IsAccessExpression(callee) && l.isAny(callee.Expression()) {
		return l.dynamicCall(call, callee)
	}
	if object, key, ok := l.hasOwnPropertyCall(call); ok {
		return l.hasOwnProperty(object, key)
	}
	if l.isParallelCall(call) {
		return l.parallelCall(call)
	}
	if x, handled := l.intrinsicCall(call); handled {
		return x
	}
	if l.unsupportedOverloadCall(call) {
		return nil
	}
	if x, handled := l.dateCall(call); handled {
		return x
	}
	if x, handled := l.regexpCall(call); handled {
		return x
	}
	if x, handled := l.typedArrayCall(call); handled {
		return x
	}
	if x, handled := l.asyncIteratorMethodCall(call); handled {
		return x
	}
	if x, handled := l.iteratorMethodCall(call); handled {
		return x
	}
	if x, handled := l.getIteratorCall(call); handled {
		return x
	}
	if x, handled := l.promiseCall(call); handled {
		return x
	}
	if x, handled := l.timerCall(call); handled {
		return x
	}
	if name := l.consoleMethodName(ast.SkipParentheses(call.Expression)); name != "" {
		return l.consoleCall(node, name)
	}
	if l.isFunctionMethod(ast.SkipParentheses(call.Expression)) {
		return l.functionMethodCall(call)
	}
	if callee := ast.SkipParentheses(call.Expression); l.isMixedUnionReceiver(callee) {
		t := l.irType(l.typeAt(node), node)
		result := l.dynamicCall(call, callee) // `x.slice(1)` for `x: string | string[]` (D-089)
		if t == nil || result == nil {
			return nil
		}
		return l.convert(result, t, node)
	}
	if l.isUnionReceiver(ast.SkipParentheses(call.Expression)) {
		return l.unionMethodCall(call)
	}
	callee := l.target(ast.SkipParentheses(call.Expression))
	if generic := l.genericMethodAt(ast.SkipParentheses(call.Expression)); generic != nil {
		if generic.static != nil {
			return l.genericCall(call, generic)
		}
		return l.genericMethodCall(call, generic)
	}
	if fn, ok := l.staticMethods[l.memberDeclaration(ast.SkipParentheses(call.Expression))]; ok {
		return l.overloadResult(node, l.staticCall(call, fn))
	}
	if method, ok := l.membersAt(ast.SkipParentheses(call.Expression)).methods[l.memberDeclaration(ast.SkipParentheses(call.Expression))]; ok {
		switch {
		case method == nil:
			return nil // its declaration failed, which was reported
		case method.Kind != ir.Plain:
			return l.valueCall(call) // `o.p()` calls the function a getter returns
		}
		return l.overloadResult(node, l.instanceMethodCall(call, method))
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
	if l.isArrayStatic(ast.SkipParentheses(call.Expression), "of") {
		return l.arrayOfCall(call)
	}
	if l.isArrayConstructor(ast.SkipParentheses(call.Expression)) {
		return l.arrayConstructor(node, call.Arguments.Nodes)
	}
	if name := l.mathMember(ast.SkipParentheses(call.Expression)); name != "" {
		return l.mathCall(call, name)
	}
	if l.isObjectStatic(ast.SkipParentheses(call.Expression), "keys", "values", "entries", "hasOwn") {
		return l.objectStaticCall(call)
	}
	if x, handled := l.objectFunctionCall(call); handled {
		return x
	}
	if l.isGlobalMember(ast.SkipParentheses(call.Expression), "JSON", "parse") {
		return l.jsonParse(call)
	}
	if l.isGlobalMember(ast.SkipParentheses(call.Expression), "JSON", "stringify") {
		return l.jsonStringify(call)
	}
	if l.isRecordMethodCall(node) {
		return l.recordMethodCall(call)
	}
	if l.isObjectToString(call) {
		return l.objectToString(call)
	}
	if l.isStringFunction(call) {
		return l.stringCall(call)
	}
	if x, handled := l.stringLibraryCall(call); handled {
		return x
	}
	if x, handled := l.numberLibraryCall(call); handled {
		return x
	}
	if generic, ok := l.generics[callee]; ok {
		if generic == nil {
			return nil // its declaration failed, which was reported
		}
		return l.genericCall(call, generic)
	}
	if generic, ok := l.genericConsts[callee]; ok && ast.SkipParentheses(call.Expression).Kind == ast.KindIdentifier && l.makesGenericInstances(generic, map[*genericFunc]bool{}) {
		return l.genericCall(call, generic) // `make(5)` for `const make = <T>(x: T) => …` (D-102)
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
		return l.overloadResult(node, l.userCall(call, fn))
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

// overloadResult converts x, the result of a call (node) of a function or method declared
// with overloads, from its implementation's result type to the result type of the
// overload tsc chose, checked as a cast is (D-117): `pick("x")` is a string where the
// implementation returns `string | number`.
func (l *lowerer) overloadResult(node *ast.Node, x ir.Expr) ir.Expr {
	if x == nil || x.Type() == ir.Void {
		return x
	}
	signature := l.checker.GetResolvedSignature(node)
	if signature == nil || signature.Declaration() == nil || !isOverloadSignature(signature.Declaration()) {
		return x
	}
	target := l.irType(l.typeAt(node), node)
	if target == nil || target == x.Type() || target == ir.Void {
		return x
	}
	if isUnion(x.Type()) && !isUnion(target) && !isUpcastOfMembers(x.Type().(*ir.Union), target) {
		return l.checkedConversion(x, target, node)
	}
	return l.convert(x, target, node)
}

// isOverloadSignature reports whether declaration is an overload of a function or method
// that has an implementation: a declaration of it without a body.
func isOverloadSignature(declaration *ast.Node) bool {
	switch declaration.Kind {
	case ast.KindFunctionDeclaration, ast.KindMethodDeclaration:
	default:
		return false
	}
	if declaration.Body() != nil || declaration.Symbol() == nil {
		return false
	}
	return slices.ContainsFunc(declaration.Symbol().Declarations, func(d *ast.Node) bool { return d.Kind == declaration.Kind && d.Body() != nil })
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

// withArgumentTemps lowers an expression with lower, and evaluates the arguments of its
// calls that inTemporaries put in temporaries before it: an argument that passes a
// computed `T | undefined` to an optional parameter gives both the value and its Given flag.
func (l *lowerer) withArgumentTemps(lower func() ir.Expr) ir.Expr {
	outer := l.argumentTemps
	l.argumentTemps = nil
	x := lower()
	temps := l.argumentTemps
	l.argumentTemps = outer
	if x == nil {
		return nil
	}
	for i := len(temps) - 1; i >= 0; i-- {
		temps[i].In = x
		x = temps[i]
	}
	return x
}

// inTemporaries puts the arguments in values up to the one at last in temporaries that
// withArgumentTemps evaluates before the call, in order, all but the pure ones, and
// returns the arguments with the temporaries in their place; values if last is -1.
func (l *lowerer) inTemporaries(values []ir.Expr, last int) []ir.Expr {
	if last < 0 {
		return values
	}
	values = slices.Clone(values)
	for i, value := range values[:last+1] {
		if _, spread := value.(*spreadValue); spread || isPure(value) {
			continue
		}
		local := l.newLocal("arg", value.Type())
		l.argumentTemps = append(l.argumentTemps, &ir.Bind{Local: local, Value: value})
		values[i] = &ir.LocalRef{Local: local}
	}
	return values
}

// lastMaybePassed is the index in values of the last argument that is computed and that
// passes a value or undefined to an optional parameter in params (`f(m.get(k))` for
// `f(p?: T)`), so that it needs a temporary for its value and its Given flag; or -1.
func (l *lowerer) lastMaybePassed(values []ir.Expr, params []ir.Type) int {
	last, next := -1, 0
	for i := 0; i < len(params) && next < len(values); i++ {
		param, optional := params[i], hasDefault(params, i)
		if isRest(param) {
			break
		}
		if optional {
			i++ // past the flag
		}
		value := values[next]
		next++
		if optional && !isPure(value) && l.maybeUndefinedFor(value.Type(), param) {
			last = next - 1
		}
	}
	return last
}

// maybeUndefinedFor reports whether a value of type t passed to an optional parameter of
// type param may be undefined, which leaves the argument out: a union with undefined that
// the parameter doesn't take as it is, or a dynamic value.
func (l *lowerer) maybeUndefinedFor(t ir.Type, param ir.Type) bool {
	if u, ok := t.(*ir.Union); ok {
		return u.Has(ir.Undefined) && !l.takesUnion(param, u)
	}
	return t == ir.Value && param != ir.Value
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
	x := l.iterable(argument.Expression())
	switch {
	case x == nil:
		return nil
	case isArray(x.Type()) && isArrayLiteralValue(x): // `...[10, 20] as [number, ...number[]]`: its elements
		return append([]ir.Expr{}, x.(*ir.NewArray).Elems...)
	case isArray(x.Type()) && isPure(x) && isRestTuple(l.typeAt(argument.Expression())): // its leading elements, which tsc says are there, then the rest
		required := 0
		for _, flag := range l.typeAt(argument.Expression()).TargetTupleType().ElementFlags() {
			if flag&checker.ElementFlagsRequired != 0 {
				required++
			}
		}
		values := make([]ir.Expr, 0, required+1)
		for i := range required {
			values = append(values, &ir.ArrayCall{Op: ir.ArrayElementChecked, Args: []ir.Expr{x, &ir.NumberConst{Value: float64(i)}}})
		}
		rest := &ir.ArrayCall{Op: ir.ArraySlice, Args: []ir.Expr{x, &ir.NumberConst{Value: float64(required)}, &ir.NumberConst{Value: math.Inf(1)}}}
		return append(values, &spreadValue{rest})
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
	case isTuple(x.Type()): // `...p.dims`: a copy into an array (D-089)
		if array := l.tupleArray(x, l.typeAt(argument.Expression()), argument.Expression()); array != nil {
			return []ir.Expr{&spreadValue{array}}
		}
		return nil
	case isUnion(x.Type()) || x.Type() == ir.Value || isPlainObject(x.Type()): // a dynamic value's items (D-078), a union's (`...(set ?? [])`, D-089), or an object's by its [Symbol.iterator] (D-111)
		return []ir.Expr{&spreadValue{&ir.CallBuiltin{Builtin: ir.ValueToArray, Args: []ir.Expr{box(x), &ir.StringConst{Value: scanner.GetTextOfNode(argument.Expression())}}}}}
	}
	l.notSupportedYet(argument, fmt.Sprintf("spreading a %s into arguments is", x.Type()))
	return nil
}

// isArrayLiteralValue reports whether x makes an array of the elements it lists.
func isArrayLiteralValue(x ir.Expr) bool {
	made, ok := x.(*ir.NewArray)
	return ok && made.Length == nil
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
			if s.x.Type() != array && s.x.Type() != ir.ValueArray {
				l.notSupportedYet(at[i], fmt.Sprintf("spreading a %s into a rest parameter of type %s is", s.x.Type(), array))
				return nil
			}
			if parts[i] = l.spreadPart(s, array, at[i]); parts[i] == nil {
				return nil
			}
			spread = true
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
	values = l.inTemporaries(values, l.lastMaybePassed(values, params))
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
		if next >= len(values) { // a parameter an override doesn't declare (fitOverride), which tsc doesn't count
			args = append(args, &ir.LeftOut{T: param})
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
		if optional && l.maybeUndefinedFor(arg.Type(), param) {
			passed := l.maybePassed(arg, param, node) // `f(x)` with x: T | undefined for `f(p?: T)`
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

// methodCall lowers a call of a builtin method: the receiver, then each argument
// converted to its parameter's type, or what a left-out argument means.
func (l *lowerer) methodCall(node *ast.Node, builtin *ir.Builtin) ir.Expr {
	call := node.AsCallExpression()
	receiver := l.expression(call.Expression.Expression())
	if receiver == nil {
		return nil
	}
	if extra := len(builtin.Params) - 1; len(call.Arguments.Nodes) > extra {
		name := builtin.Name
		if callee := ast.SkipParentheses(call.Expression); callee.Kind == ast.KindPropertyAccessExpression {
			name = callee.Name().Text() // as written: toLocaleUpperCase is toUpperCase's builtin
		}
		l.notSupportedYet(call.Arguments.Nodes[extra], "this argument of `"+name+"` is")
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
		} else if leftOut := leftOutArgument(builtin, i); leftOut != nil {
			arg = l.orLeftOut(arg, leftOut)
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

// as lowers `x as T`: between numeric types it converts the value.
func (l *lowerer) as(node *ast.Node) ir.Expr {
	if l.isDynamicSource(node) { // `x as any` and `x as unknown`: any value is one
		if x := l.valueExpression(node.Expression()); x != nil {
			return box(x)
		}
		return nil
	}
	if l.isDynamicSource(node.Expression()) {
		return l.asFromValue(node)
	}
	if x, ok := l.asOfDynamicVariable(node); ok {
		return x
	}
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
		return l.asCast(node)
	}
	x := l.expression(node.Expression())
	if x == nil {
		return nil
	}
	if isUnion(x.Type()) {
		return l.checkedCast(x, target, node) // `x as number` for x: number | null
	}
	return l.convert(x, target, node)
}

// asOfDynamicVariable lowers `e as T` for a variable e that holds a dynamic value (a
// catch binding, an unknown) whatever tsc narrowed it to: e's value converted to T with a
// check, so that `(e as Error).name` works for any error, though tsc may have narrowed e
// to a class of the same members as another. ok is false for other operands.
func (l *lowerer) asOfDynamicVariable(node *ast.Node) (x ir.Expr, ok bool) {
	operand := ast.SkipParentheses(node.Expression())
	if operand.Kind != ast.KindIdentifier {
		return nil, false
	}
	value := l.identifier(operand)
	if value == nil || value.Type() != ir.Value {
		return nil, false
	}
	target := l.irType(l.typeAt(node), node)
	if target == nil {
		return nil, true
	}
	if n := l.numericType(node); n != nil {
		target = n
	}
	return l.checkedConversion(value, target, node.Expression()), true
}

// asCast lowers `x as T` for a T that isn't numeric (D-089): free where x is a T as it is
// (an upcast, which converts as an assignment would, or the same representation), else
// checked at run time as a dynamic value converting to T is (D-070), which throws
// TypeError where x isn't one (SPEC §3.5).
func (l *lowerer) asCast(node *ast.Node) ir.Expr {
	t := l.typeAt(node)
	target := l.irType(t, node)
	x := l.expression(node.Expression())
	switch {
	case target == nil || x == nil:
		return nil
	case x.Type() == target:
		return x // `Object.keys(o) as Key[]`: literal types aren't kept at run time
	case l.checker.IsTypeAssignableTo(l.typeAt(node.Expression()), t) && l.typeAt(node.Expression()).Flags()&checker.TypeFlagsNever == 0:
		return l.convert(x, target, node)
	}
	return l.checkedCast(x, target, node) // a downcast, or x narrowed to never where tsc missed an assignment in a callback
}

// checkedCast converts x to target for `x as T` (node) as a dynamic value converts to
// it: checked, and for an integer type, a number converted by `as`'s rules.
func (l *lowerer) checkedCast(x ir.Expr, target ir.Type, node *ast.Node) ir.Expr {
	return l.checkedConversion(x, target, node.Expression())
}

// checkedConversion converts x, written at node, to target as a dynamic value converts
// to it (D-070): checked, and for an integer type, a number converted by `as`'s rules.
func (l *lowerer) checkedConversion(x ir.Expr, target ir.Type, node *ast.Node) ir.Expr {
	switch {
	case ir.IsNumeric(target) && target != ir.Number:
		return &ir.Convert{X: &ir.FromUnion{X: box(x), To: ir.Number}, To: target}
	case isCheckable(target):
		return &ir.FromUnion{X: box(x), To: target}
	}
	return l.dynamicConversion(box(x), target, node)
}

// isKeyedElement reports whether node is `o[k]` for an object o of the program (a class
// instance, an object type or a union of them or of tuples) and a key that tsc checked
// names one of its properties: `totals[status]` for `Record<Status, number>`, `o[key]`
// for `key: keyof T`. Such an element is found at run time, as through `any` (D-089).
func (l *lowerer) isKeyedElement(node *ast.Node) bool {
	if node.Kind != ast.KindElementAccessExpression || l.isTypedArrayElement(node) {
		return false
	}
	switch receiver := l.irType(l.checker.GetNonNullableType(l.typeAt(node.Expression())), node).(type) {
	case *ir.Class, *ir.Interface:
		return true
	case *ir.Union:
		return isUnionOfObjects(receiver)
	}
	return false
}

// keyedElementRead lowers a read of a keyed element: the property found at run time,
// converted to the type tsc gives it with a check.
func (l *lowerer) keyedElementRead(node *ast.Node) ir.Expr {
	t := l.irType(l.typeAt(node), node)
	l.wantKeyedMembers(node)
	object := l.expression(node.Expression())
	key := l.dynamicKey(node)
	if t == nil || object == nil || key == nil {
		return nil
	}
	return l.checkedConversion(&ir.CallBuiltin{Builtin: ir.DynamicGet, Args: []ir.Expr{box(object), key}}, t, node)
}

// wantKeyedMembers makes the generic methods a keyed element may find have erased copies
// in their classes' method tables: those its key's literal types name, or else all.
func (l *lowerer) wantKeyedMembers(node *ast.Node) {
	key := l.typeAt(node.AsElementAccessExpression().ArgumentExpression)
	keys := []*checker.Type{key}
	if key.Flags()&checker.TypeFlagsUnion != 0 {
		keys = key.Types()
	}
	for _, k := range keys {
		if k.Flags()&checker.TypeFlagsStringLiteral == 0 {
			l.wantDynamicMember(node)
			return
		}
	}
	for _, k := range keys {
		l.wantInMethodTables(k.AsLiteralType().Value().(string))
	}
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
	if u, ok := t.(*ir.Union); ok && isPlainShape(x.Type()) && isUnionOfObjects(u) && !u.Has(x.Type()) && l.memberFor(x.Type(), u) == nil {
		return &ir.ToUnion{X: x, To: u} // `{ type: "@@init" } as Action`: an object of no member's type, kept as it is, as in JS
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
	case isObject(t) && !isFunction(t) && !isMap(t):
		return &ir.ToString{X: box(x)} // its toString(), else "[object Object]"
	}
	l.notSupportedYet(node, fmt.Sprintf("converting %s to a string is", x.Type()))
	return nil
}

// propertyAccess lowers a property read: a binding of a module namespace (`m.x` after
// `import * as m`) or a builtin property; objects don't exist yet.
func (l *lowerer) propertyAccess(node *ast.Node) ir.Expr {
	if x, ok := l.constructorName(node); ok {
		return x
	}
	if l.libMemberName(node) == "Array.par" {
		return l.parallelView(node)
	}
	if x, handled := l.typedArrayProperty(node); handled {
		return x
	}
	if x, handled := l.regexpProperty(node); handled {
		return x
	}
	if builtin := l.functionProperty(node); builtin != nil {
		object := l.expression(node.Expression())
		if object == nil {
			return nil
		}
		return &ir.CallBuiltin{Builtin: builtin, Args: []ir.Expr{box(object)}}
	}
	if l.isAny(node.Expression()) {
		return l.dynamicRead(node)
	}
	if l.isMixedUnionReceiver(node) {
		return l.mixedUnionRead(node)
	}
	if l.isUnionReceiver(node) {
		object, field := l.unionProperty(node)
		switch {
		case field != nil:
			return &ir.FieldRef{X: object, Field: field}
		case object != nil:
			return l.valueProperty(node, object)
		}
		return nil
	}
	if shape := l.tupleShapeOf(node.Expression()); shape != nil && node.AsPropertyAccessExpression().Name().Text() == "length" {
		object := l.expression(node.Expression())
		if object == nil {
			return nil
		}
		if hasOptionalElements(shape) {
			return &ir.CallBuiltin{Builtin: ir.TupleLength, Args: []ir.Expr{box(object)}}
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
	if field := l.membersAt(node).fields[member]; field != nil && !l.isBuiltinFieldOfObjectType(node, field) {
		return l.fieldAccess(node, field)
	}
	if static, ok := l.statics[member]; ok {
		if local := l.staticField(node, static, false); local != nil {
			return &ir.LocalRef{Local: local}
		}
		return nil
	}
	if accessors, ok := l.staticAccessors[l.memberSymbol(node)]; ok {
		if accessors.getter == nil {
			return &ir.UndefinedConst{} // a static property with only a setter reads as undefined
		}
		return &ir.Call{Func: accessors.getter}
	}
	if l.isShapeProperty(member) || l.isMappedProperty(node) || l.membersAt(node).fields[member] != nil {
		object, field := l.shapeProperty(node)
		switch {
		case field != nil:
			return &ir.FieldRef{X: object, Field: field}
		case object != nil:
			return l.valueProperty(node, object)
		}
		return nil
	}
	if accessors, ok := l.accessorProperty(node); ok {
		return l.propertyRead(node, accessors)
	}
	if generic := l.genericMethodAt(node); generic != nil {
		return l.genericMethodValue(node, generic)
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
	if member, ok := strings.CutPrefix(l.libMemberName(node), "NumberConstructor."); ok && numberTests[member][0] != nil {
		return l.numberTestValue(node, member)
	}
	if x, ok := l.numberConstant(node); ok {
		return x
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

// isBuiltinFieldOfObjectType reports whether `o.p` (node) reads a field of a builtin class
// (Error's message) on an object of an object type that extends it (`interface
// ErrnoException extends Error`), whose fields are the object type's: found by name.
func (l *lowerer) isBuiltinFieldOfObjectType(node *ast.Node, field *ir.Field) bool {
	if !field.Class.Builtin {
		return false
	}
	switch t := l.irType(l.typeAt(node.Expression()), node.Expression()).(type) {
	case *ir.Interface:
		return true
	case *ir.Class:
		return t.Shape
	}
	return false
}

// constructorName lowers `o.constructor.name`, and `new.target.name` in a constructor
// (the class being made, `this`'s): the name of the class of o's value (D-089). ok is
// false for other property accesses.
func (l *lowerer) constructorName(node *ast.Node) (x ir.Expr, ok bool) {
	constructor := ast.SkipParentheses(node.Expression())
	if node.AsPropertyAccessExpression().Name().Text() != "name" {
		return nil, false
	}
	var object ir.Expr
	switch {
	case constructor.Kind == ast.KindPropertyAccessExpression && constructor.AsPropertyAccessExpression().Name().Text() == "constructor" && !l.isAny(constructor.Expression()):
		object = l.valueExpression(constructor.Expression())
	case constructor.Kind == ast.KindMetaProperty && constructor.AsMetaProperty().KeywordToken == ast.KindNewKeyword && l.currentClass() != nil:
		object = l.thisExpression(constructor)
	default:
		return nil, false
	}
	if object == nil {
		return nil, true
	}
	return &ir.CallBuiltin{Builtin: ir.ValueConstructorName, Args: []ir.Expr{box(object)}}, true
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
	ok = l.isStringElement(element) && l.isStringTyped(other)
	return element, other, ok
}

// isStringTyped reports whether tsc types node as a string: a string type, or a union of
// string literals (`"*" | "_"`).
func (l *lowerer) isStringTyped(node *ast.Node) bool {
	t := l.typeAt(node)
	return t.Flags()&checker.TypeFlagsStringLike != 0 || t.Flags()&checker.TypeFlagsUnion != 0 && allOf(t.Types(), checker.TypeFlagsStringLike)
}

// arrayElementComparedWithString recognises a[i] compared with a string (either way
// round) and returns the two sides.
func (l *lowerer) arrayElementComparedWithString(expr *ast.BinaryExpression) (element *ast.Node, other *ast.Node, ok bool) {
	element, other = ast.SkipParentheses(expr.Left), expr.Right
	if !l.isArrayElement(element) {
		element, other = ast.SkipParentheses(expr.Right), expr.Left
	}
	ok = l.isArrayElement(element) && l.isStringTyped(other)
	return element, other, ok
}

// elementEquality lowers s[i] === t (or !==) so that an index out of range makes it
// false (true for !==), as undefined does in JS (D-015).
func (l *lowerer) elementEquality(op ir.BinaryOp, element *ast.Node, other *ast.Node) ir.Expr {
	t := l.expression(other)
	if t != nil && t.Type() != ir.String { // a loose local, which may be undefined (D-090)
		x := l.valueExpression(element)
		if x == nil {
			return nil
		}
		return unionEquality(op, box(x), box(t))
	}
	s, i := l.stringElementOperands(element)
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
	case operator == ast.KindInKeyword:
		return l.computedKeyIn(expr)
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
		if element, other, ok := l.arrayElementComparedWithString(expr); ok && (operator == ast.KindEqualsEqualsEqualsToken || operator == ast.KindExclamationEqualsEqualsToken) {
			x, y := l.valueExpression(element), l.expression(other) // `rest[0] === "at"`: undefined past the end, as in JS (D-089)
			if x == nil || y == nil {
				return nil
			}
			return unionEquality(op, x, box(y))
		}
		if element, ok := l.recordElementComparedWithUndefined(expr); ok && !l.recordMayHoldNullish(element) {
			record, key := l.recordElementOperands(element)
			return l.recordHas(record, key, op == ir.Eq) // a missing key's undefined
		}
		if operand, matches, ok := isNullishTest(expr); ok {
			return l.nullishTest(op, operand, matches)
		}
	}
	operand := l.expression
	if op >= ir.Lt && op <= ir.Ge && (l.isStringElement(ast.SkipParentheses(expr.Left)) || l.isStringElement(ast.SkipParentheses(expr.Right))) {
		operand = l.valueExpression // `fmt[i] >= "0"` past the end compares undefined, which is false, as in JS
	}
	x := operand(expr.Left)
	y := operand(expr.Right)
	if x == nil || y == nil {
		return nil
	}
	if op >= ir.Lt && op <= ir.Ge && x.Type() == l.dateClass && y.Type() == l.dateClass {
		return &ir.Binary{Op: op, X: l.dateTime(x), Y: l.dateTime(y)} // dates compare by their time values (D-087)
	}
	if loose := operator == ast.KindEqualsEqualsToken || operator == ast.KindExclamationEqualsToken; loose && (isDynamic(x.Type()) || isDynamic(y.Type())) {
		return looseEquality(op, x, y)
	}
	return l.operation(node, op, x, y, expr.Left, expr.Right)
}

// operation applies op to x and y, the lowered left and right operands of node,
// converting them to the type the semantic pass chose for them.
func (l *lowerer) operation(node *ast.Node, op ir.BinaryOp, x ir.Expr, y ir.Expr, left *ast.Node, right *ast.Node) ir.Expr {
	if op != ir.Eq && op != ir.Ne && (isUnion(x.Type()) || isUnion(y.Type())) {
		x, y = box(x), box(y) // `a[key] < b[key]` for key: keyof Item: JS's rules for the values the operands hold (D-089)
	}
	if x.Type() == ir.Value || y.Type() == ir.Value {
		if result, ok := l.dynamicOperation(node, op, x, y); ok {
			return result
		}
	}
	if n, ok := l.info.OperandType(node); ok {
		x, y = l.convert(x, numericTypes[n], left), l.convert(y, numericTypes[n], right)
		if x == nil || y == nil {
			return nil
		}
	}
	if op == ir.Eq || op == ir.Ne {
		if isDynamic(x.Type()) || isDynamic(y.Type()) {
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
	if left := ast.SkipParentheses(expr.Left); left.Kind == ast.KindPropertyAccessExpression {
		if accessors, ok := l.staticAccessors[l.memberSymbol(left)]; ok && accessors.setter != nil {
			return l.staticSetterCall(accessors.setter, expr.Right)
		}
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

// staticSetterCall lowers `C.p = value` for a static property p with a setter: the
// setter called with the value, which is the assignment's value.
func (l *lowerer) staticSetterCall(setter *ir.Func, valueNode *ast.Node) ir.Expr {
	value := l.expression(valueNode)
	if value == nil {
		return nil
	}
	if value = l.convert(value, setter.Params[0].Type, valueNode); value == nil {
		return nil
	}
	stored := l.newLocal("value", value.Type())
	ref := &ir.LocalRef{Local: stored}
	return &ir.Bind{Local: stored, Value: value, In: &ir.Sequence{X: &ir.Call{Func: setter, Args: []ir.Expr{ref}}, Y: ref}}
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
	dynamic     bool      // a property of a dynamic value (any), whose key is key (D-070)
	l           *lowerer
}

func (p place) valueType() ir.Type {
	switch {
	case p.dynamic:
		return ir.Value
	case p.local != nil:
		return p.local.Type
	case p.field != nil:
		return p.field.Type
	case p.index != nil && isTypedArray(p.object.Type()):
		return ir.Number
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
	case p.index != nil && isTypedArray(p.object.Type()):
		return p.l.typedArrayRead(p.object, p.index)
	case p.index != nil:
		return &ir.ArrayCall{Op: ir.ArrayElementChecked, Args: []ir.Expr{p.object, p.index}}
	case p.arrayLength:
		return &ir.ArrayCall{Op: ir.ArrayLength, Args: []ir.Expr{p.object}}
	case p.dynamic:
		return &ir.CallBuiltin{Builtin: ir.DynamicGet, Args: []ir.Expr{p.object, p.key}}
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
	case p.index != nil && isTypedArray(p.object.Type()):
		stored = p.l.typedArrayStore(p.object, p.index, value)
	case p.index != nil:
		stored = &ir.ArrayCall{Op: ir.ArraySetElement, Args: []ir.Expr{p.object, p.index, value}}
	case p.arrayLength:
		stored = &ir.ArrayCall{Op: ir.ArraySetLength, Args: []ir.Expr{p.object, value}}
	case p.dynamic:
		stored = &ir.CallBuiltin{Builtin: ir.DynamicSet, Args: []ir.Expr{p.object, p.key, value}}
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
	if (node.Kind == ast.KindPropertyAccessExpression || node.Kind == ast.KindElementAccessExpression) && l.isAny(node.Expression()) {
		return l.dynamicPlace(node)
	}
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
		if l.isKeyedElement(node) {
			l.wantKeyedMembers(node)
			return l.dynamicPlace(node) // a checked write (D-072)
		}
		if l.isArrayElement(node) {
			array, index := l.arrayElementOperands(node)
			return place{object: array, index: index}, array != nil
		}
		if l.isTypedArrayElement(node) {
			array, index := l.arrayElementOperands(node)
			return place{object: array, index: index, l: l}, array != nil
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
		if l.isUnionReceiver(node) {
			object, field := l.unionProperty(node)
			if field == nil && object != nil { // a recursive union's dynamic data
				return place{object: object, key: l.dynamicKey(node), dynamic: true}, true
			}
			if field != nil && field.Mixed {
				l.notSupportedYet(node, fmt.Sprintf("writing `%s` of a %s, whose members hold it as different types, is", field.Name, l.checker.TypeToString(l.typeAt(node.Expression()))))
				return place{}, false
			}
			return place{object: object, field: field}, field != nil
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
			if field == nil && object != nil { // a dynamic value tsc narrowed to an object type
				return place{object: object, key: l.dynamicKey(node), dynamic: true}, true
			}
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
	if t == ir.Value {
		op := ir.Add
		if operator == ast.KindMinusMinusToken {
			op = ir.Sub
		}
		return l.dynamicIncrement(target, op, postfix)
	}
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

// branch lowers a branch of a conditional whose result may be undefined: an element it
// reads is undefined when it isn't there, as in JS.
func (l *lowerer) branch(node *ast.Node) ir.Expr {
	if l.isElementRead(node) {
		return l.valueExpression(node)
	}
	return l.expression(node)
}

// logical lowers && and ||, whose operands must have one type.
func (l *lowerer) logical(node *ast.Node, op ir.LogicalOp, left *ast.Node, right *ast.Node) ir.Expr {
	loose := l.isLooseIdentifier(left) || l.isMatchElement(ast.SkipParentheses(left)) // a union of its type and undefined (D-090, D-133)
	if op == ir.And && !loose && !l.involvesUnion(node, left, right) && neverFalsy(l.irType(l.typeAt(left), left)) {
		x, y := l.expression(left), l.expression(right) // `obj && test` is test, after obj (D-089)
		if x == nil || y == nil {
			return nil
		}
		return &ir.Sequence{X: x, Y: y}
	}
	if loose || l.involvesUnion(node, left, right) {
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

// neverFalsy reports whether every value of type t is truthy: an object, an array, a
// function or a map, which `&&` passes over (`last && last.done`).
func neverFalsy(t ir.Type) bool {
	switch t.(type) {
	case *ir.Class, *ir.Interface, *ir.Array, *ir.FuncType, *ir.MapType:
		return t != ir.String
	}
	return false
}

// sameType lowers two expressions that must end up with one type (the branches of ?:,
// the operands of && and ||), converting numbers to the type the semantic pass chose.
// Either both results are nil or neither is.
func (l *lowerer) sameType(node *ast.Node, a *ast.Node, b *ast.Node) (ir.Expr, ir.Expr) {
	operand := l.expression
	if node.Kind == ast.KindConditionalExpression && mayHoldUndefined(l.typeAt(node)) {
		operand = l.branch // `c ? room.exits[dir] : undefined`
	}
	x, y := operand(a), operand(b)
	if x == nil || y == nil {
		return nil, nil
	}
	if t := l.numericType(node); t != nil && !isNullish(x.Type()) && !isNullish(y.Type()) { // `c ? undefined : n` is a number | undefined
		x, y = l.convert(x, t, a), l.convert(y, t, b)
		if x == nil || y == nil {
			return nil, nil
		}
	}
	switch { // `ok ? value : fail("…")` with a fail that returns never
	case x.Type() == ir.Void && y.Type() != ir.Void:
		x = l.convert(x, y.Type(), a)
	case y.Type() == ir.Void && x.Type() != ir.Void:
		y = l.convert(y, x.Type(), b)
	}
	if contextual := l.contextualType(node); contextual == nil || contextual.Flags()&checker.TypeFlagsUnion != 0 {
		t := l.typeAt(node)
		if objects := l.objectUnion(contextual); objects != nil && l.checker.IsTypeAssignableTo(t, objects) {
			t = objects // `c ? { ok: true, … } : { ok: false, … }` where a Result goes (D-095)
		}
		if u, ok := l.irType(t, node).(*ir.Union); ok && x.Type() != y.Type() { // `c ? new Dog() : new Bird()` is a Dog | Bird
			x, y = l.convert(x, u, a), l.convert(y, u, b)
			if x == nil || y == nil {
				return nil, nil
			}
			return x, y
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
	if x.Type() != y.Type() && (x.Type() == ir.Value || y.Type() == ir.Value || l.irType(l.typeAt(node), node) == ir.Value) { // `c ? String(e) : e` for an unknown e
		x, y = box(x), box(y)
	}
	if x.Type() != y.Type() && (isEmptyArrayLiteral(x) || isEmptyArrayLiteral(y)) { // `c ? [x] : []` (D-089)
		if t, ok := l.irType(l.typeAt(node), node).(*ir.Array); ok {
			if x, y = l.convert(x, t, a), l.convert(y, t, b); x == nil || y == nil {
				return nil, nil
			}
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
	if hasValueOf(x.Type()) || hasValueOf(y.Type()) { // `c + ""` is `c.valueOf() + ""`, as ToPrimitive says (D-089)
		return l.checkedConversion(&ir.CallBuiltin{Builtin: ir.ValueAdd, Args: []ir.Expr{box(x), box(y)}}, ir.String, node)
	}
	x, y = l.toString(x, expr.Left), l.toString(y, expr.Right)
	if x == nil || y == nil {
		return nil
	}
	return &ir.Binary{Op: ir.Concat, X: x, Y: y}
}

// hasValueOf reports whether t is a class with a valueOf method, which `+` calls.
func hasValueOf(t ir.Type) bool {
	class, ok := t.(*ir.Class)
	return ok && class.Method("valueOf", ir.Plain) != nil
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
	if !convertsToNumber(x.Type()) {
		l.notSupportedYet(node, fmt.Sprintf("%s on %s is", describe(node), x.Type()))
		return nil
	}
	if !ir.IsNumeric(x.Type()) {
		x = toNumber(x) // JS's ToNumber (D-070, D-083)
	}
	switch expr.Operator {
	case ast.KindPlusToken:
		return x // +x is x for a numeric value
	case ast.KindTildeToken:
		return &ir.Unary{Op: ir.BitNot, X: x}
	}
	return &ir.Unary{Op: ir.Neg, X: x}
}

// coveringMember returns the member of the intersection t that has every property t
// has, whose values are therefore t's values, or nil if none has.
func (l *lowerer) coveringMember(t *checker.Type) *checker.Type {
	properties := l.checker.GetPropertiesOfType(t)
	for _, member := range t.Types() {
		if !slices.ContainsFunc(properties, func(p *ast.Symbol) bool { return l.checker.GetPropertyOfType(member, p.Name) == nil }) {
			return member
		}
	}
	return nil
}

// builtin returns the runtime function a member access refers to, or nil if it isn't
// a builtin member of the prelude or the ES library.
func (l *lowerer) builtin(member *ast.Node) *ir.Builtin {
	return builtins[l.libMemberName(member)]
}

// libMemberName names the member of the prelude or the ES library a member access
// refers to as "Interface.member" ("String.slice", "StringConstructor.fromCharCode"),
// or is "" if it refers to something else.
func (l *lowerer) libMemberName(member *ast.Node) string {
	symbol := l.checker.GetSymbolAtLocation(member)
	if symbol == nil || symbol.Parent == nil || symbol.ValueDeclaration == nil {
		return ""
	}
	file := ast.GetSourceFileOfNode(symbol.ValueDeclaration)
	if !lib.IsLibPath(file.FileName()) && !l.program.IsSourceFileDefaultLibrary(file.Path()) {
		return ""
	}
	return symbol.Parent.Name + "." + symbol.Name
}

// isNullish reports whether t is the type of null or undefined.
func isNullish(t ir.Type) bool { return t == ir.Null || t == ir.Undefined }

func isClass(t ir.Type) bool {
	_, ok := t.(*ir.Class)
	return ok
}

// isObject reports whether t is the type of a reference to an object: a class (or
// shape), an interface or an array.
// isPlainShape reports whether t is the shape of an object type other than a tuple.
func isPlainShape(t ir.Type) bool {
	class, ok := t.(*ir.Class)
	return ok && class.Shape && !class.Tuple
}

// isUnionOfObjects reports whether every member of u is a class, shape or interface.
func isUnionOfObjects(u *ir.Union) bool {
	return !slices.ContainsFunc(u.Members, func(m ir.Type) bool { return !isObject(m) || isArray(m) })
}

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
	if t.Flags()&checker.TypeFlagsTypeParameter != 0 && t.AsTypeParameter().IsThisType() {
		t = l.checker.GetConstraintOfTypeParameter(t) // a fluent method's `this`: its class (D-089)
	}
	t = l.withoutParallelBrand(t)
	if t.Flags()&checker.TypeFlagsIntersection != 0 {
		if member := l.coveringMember(t); member != nil {
			t = member // `Settings & { width: number }`, which Object.assign gives, is a Settings (D-086)
		} else if !l.isObjectType(t) {
			return ir.Value // `E & Error`, which `e instanceof Error` narrows an E of object types to: no class's objects are both (D-095)
		}
	}
	if symbol := t.Symbol(); symbol != nil && symbol.Flags&ast.SymbolFlagsRegularEnum != 0 && t.Flags()&checker.TypeFlagsObject != 0 && len(symbol.Declarations) == 1 {
		return l.enumObjectType(symbol.ValueDeclaration) // `typeof Color`
	}
	if generic, ok := l.genericClasses[t.Symbol()]; ok && t.Flags()&checker.TypeFlagsObject != 0 && t.ObjectFlags()&checker.ObjectFlagsAnonymous == 0 { // not `typeof Box`
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
	if l.reportUnsupportedTypedArray(t, node) {
		return nil
	}
	if l.isMatchArrayType(t) || l.isMatchArrayOrEmpty(t) {
		return l.arrayOf(ir.String) // RegExpExecArray, RegExpMatchArray, and `re.exec(s) ?? []`'s RegExpExecArray | [] (D-133)
	}
	if l.checker.IsArrayType(t) {
		return l.arrayType(t, node)
	}
	if checker.IsTupleType(t) {
		return l.tupleType(t, node)
	}
	if l.isIteratorType(t) || l.isIterableType(t) || l.isPromiseType(t) || l.isTimerType(t) || l.isAsyncIteratorType(t) {
		return ir.Value // an iterator object, which the runtime knows by its descriptor (D-109), any iterable (D-110), or a promise (D-112)
	}
	if value, ok := l.iteratorResultValue(t); ok {
		v := l.irType(value, node)
		if v == nil {
			return nil
		}
		// A finished generator's next() gives undefined, whatever it returned; a yield
		// result holds it too, so that IteratorResult<T, T> is one shape.
		return l.iteratorResultShape(l.withUndefined(v))
	}
	if mt, ok := l.mapType(t, node); ok {
		if mt == nil {
			return nil // reported
		}
		return mt
	}
	if l.mapsInProgress[t] {
		if value, ok := l.recurring(); ok {
			return value // `type Context = { [k: string]: string | Context[] }`
		}
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
	if t.Flags()&checker.TypeFlagsObject != 0 && (t.Symbol() == nil || t.Symbol().Flags&ast.SymbolFlagsObjectLiteral == 0) && l.checker.IsEmptyAnonymousObjectType(t) {
		return ir.Value // `{}` written as a type, or `unknown ?? "x"`'s: any value but null and undefined
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
	case flags&checker.TypeFlagsVoid != 0:
		return ir.Undefined // a value of type void (`R | undefined` for an R of void) is undefined
	case flags&checker.TypeFlagsNull != 0:
		return ir.Null
	case flags&checker.TypeFlagsUnion != 0 && allOf(t.Types(), checker.TypeFlagsStringLike):
		return ir.String
	case flags&checker.TypeFlagsUnion != 0:
		return l.unionType(t, node)
	case flags&checker.TypeFlagsUnknown != 0:
		return ir.Value // narrowed before use (narrowedValue, D-068)
	case flags&checker.TypeFlagsAny != 0:
		return ir.Value // a dynamic value (D-070)
	case flags&checker.TypeFlagsNonPrimitive != 0:
		return ir.Value // `object`: any object, a dynamic value (D-089)
	case flags&checker.TypeFlagsNever != 0:
		return ir.Value // no value ever has it: `assertNever(x: never)`'s parameter
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
