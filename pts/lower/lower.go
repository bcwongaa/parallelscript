// Package lower turns a type-checked TypeScript AST into ParallelScript IR. It is
// where the compiler decides what it can compile: anything it cannot lower yet
// becomes a diagnostic.
package lower

import (
	"context"
	"fmt"
	"math"
	"path/filepath"
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
		classes: map[*ast.Symbol]*ir.Class{}, classNodes: map[*ir.Class]*ast.Node{}, fields: map[*ast.Node]*ir.Field{}, methods: map[*ast.Node]*ir.Method{},
		methodDeclarations: map[*ir.Method]*ast.Node{}, abstractFields: map[*ast.Node]property{}, this: map[*ir.Func]*ir.Local{}}
	result := &ir.Program{}
	for _, file := range program.Modules {
		module := newModule(file, file == program.Entry)
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
	// Function bodies come last, when every module-scope binding they may use is known.
	for len(l.bodies) > 0 {
		body := l.bodies[0]
		l.bodies = l.bodies[1:]
		l.functionBody(body)
	}
	result.Funcs = append(result.Funcs, l.lowered...)
	result.Classes = l.classList
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
	program            *compiler.Program
	checker            *checker.Checker
	info               *check.Info
	locals             map[*ast.Symbol]*ir.Local // nil for a declaration that failed to lower
	owners             map[*ir.Local]*ir.Func    // the function each local belongs to
	moduleScope        map[*ir.Local]bool        // the locals declared at module scope, which functions and other modules may use
	funcs              map[*ast.Symbol]*ir.Func  // every function declaration, made before any body is lowered; nil if it failed
	classes            map[*ast.Symbol]*ir.Class // every class declaration, made before any code is lowered; nil if it failed
	classNodes         map[*ir.Class]*ast.Node   // the declaration of each class
	classList          []*ir.Class               // the classes that didn't fail, superclasses first
	fields             map[*ast.Node]*ir.Field   // the fields of those classes, by property declaration (a redeclaration's is the inherited field)
	methods            map[*ast.Node]*ir.Method  // their methods, getters and setters, by declaration; nil if it failed
	methodDeclarations map[*ir.Method]*ast.Node  // the declaration of each method (for an accessor that implements an abstract field, the field's)
	abstractFields     map[*ast.Node]property    // the getter and setter of each abstract field, by declaration
	this               map[*ir.Func]*ir.Local    // the `this` parameter of each constructor and method
	lowered            []*ir.Func                // the functions whose bodies are lowered
	bodies             []*ast.Node               // function declarations whose bodies are still to be lowered
	function           *ir.Func                  // the function being lowered
	targets            []jumpTarget              // the statements around the current one that break or continue can leave
	errors             []diagnostic.Diagnostic
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
	l.errors = append(l.errors, diagnostic.At(node, diagnostic.NotSupportedYet, "%s not supported yet", what))
}

func (l *lowerer) unsupported(node *ast.Node, format string, args ...any) {
	l.errors = append(l.errors, diagnostic.At(node, diagnostic.Unsupported, format, args...))
}

// ---------------------------------------------------------------- statements

func (l *lowerer) block(node *ast.Node, statements []*ast.Node) *ir.Block {
	block := &ir.Block{At: l.pos(node)}
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
	case ast.KindWhileStatement, ast.KindDoStatement, ast.KindForStatement, ast.KindSwitchStatement:
		return l.breakable(node, "")
	case ast.KindLabeledStatement:
		return l.labeledStatement(node)
	case ast.KindFunctionDeclaration:
		l.bodies = append(l.bodies, node)
	case ast.KindClassDeclaration:
		if _, ok := l.classes[l.declaredSymbol(node)]; ok {
			l.bodies = append(l.bodies, node) // its constructor and methods
		} else {
			l.notSupportedYet(node, "classes inside functions or blocks are")
		}
	case ast.KindReturnStatement:
		return l.returnStatement(node)
	case ast.KindBreakStatement:
		return l.jump(node, node.AsBreakStatement().Label, false)
	case ast.KindContinueStatement:
		return l.jump(node, node.AsContinueStatement().Label, true)
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
	case ast.KindWhileStatement, ast.KindDoStatement, ast.KindForStatement, ast.KindSwitchStatement:
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
	failed := false
	switch {
	case s.Initializer == nil:
	case s.Initializer.Kind == ast.KindVariableDeclarationList:
		init = l.declarationList(node, s.Initializer)
		failed = init == nil
	default:
		x := l.expression(s.Initializer)
		failed = x == nil
		init = []ir.Stmt{&ir.ExprStmt{At: l.pos(s.Initializer), X: x}}
	}
	loop := &ir.Loop{At: l.pos(node), Label: label}
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
	return []ir.Stmt{&ir.Block{At: l.pos(node), Stmts: []ir.Stmt{&ir.LocalDecl{At: l.pos(node), Local: tag, Init: value}, result}}}
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
		decl := l.variableDeclaration(declaration)
		if decl == nil {
			return nil
		}
		stmts = append(stmts, decl)
	}
	return stmts
}

func (l *lowerer) variableDeclaration(node *ast.Node) ir.Stmt {
	name := node.Name()
	if name.Kind != ast.KindIdentifier {
		l.notSupportedYet(name, "destructuring is")
		return nil
	}
	symbol := l.checker.GetSymbolAtLocation(name)
	local, declared := l.locals[symbol] // module-scope bindings are declared before any code is lowered
	if !declared {
		l.locals[symbol] = nil // until it lowers, uses of it are silent: the declaration already failed
		if t := l.bindingType(symbol, name); t != nil {
			local = l.newLocal(name.Text(), t)
		}
	}
	if local == nil {
		return nil
	}
	var init ir.Expr
	if initializer := node.Initializer(); initializer != nil {
		if init = l.expression(initializer); init == nil {
			return nil
		}
		if init = l.convert(init, local.Type, initializer); init == nil {
			return nil
		}
	}
	l.locals[symbol] = local
	return &ir.LocalDecl{At: l.pos(node), Local: local, Init: init}
}

// bindingType is the IR type of a variable or `export default` binding (symbol, written
// at node): the numeric type the semantic pass gave it, or its type's; nil if it has none (reported).
func (l *lowerer) bindingType(symbol *ast.Symbol, node *ast.Node) ir.Type {
	if t := l.numericType(node); t != nil {
		return t
	}
	return l.irType(l.checker.GetTypeOfSymbol(symbol), node)
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
				if name := declaration.Name(); name.Kind == ast.KindIdentifier {
					l.declareModuleBinding(name.Text(), l.checker.GetSymbolAtLocation(name), name)
				}
			}
		case statement.Kind == ast.KindExportAssignment && l.hasDefaultBinding(statement):
			l.declareModuleBinding("default", l.declaredSymbol(statement), statement.Expression())
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
// declaration they name. It stops at a module's default binding (hasDefaultBinding),
// which holds a value of its own.
func (l *lowerer) target(node *ast.Node) *ast.Symbol {
	symbol := l.checker.GetSymbolAtLocation(node)
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
	if symbol := l.checker.GetSymbolAtLocation(node); symbol != nil {
		return symbol.ValueDeclaration
	}
	return nil
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
	case ir.IsNumeric(value.Type()) && ir.IsNumeric(target):
		return &ir.Convert{X: value, To: target}
	case isClass(value.Type()) && isClass(target):
		return l.classCast(value, target.(*ir.Class), node)
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
		if child.Kind == ast.KindFunctionDeclaration && child.Body() != nil {
			qualified = "default" // `export default function () {}`
			if name := child.Name(); name != nil {
				qualified = name.Text()
			}
			if outer != "" {
				qualified = outer + "." + qualified
			}
			l.funcs[l.declaredSymbol(child)] = l.declareFunction(child, qualified, nil)
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
	case node.TypeParameterList() != nil:
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
	if class != nil {
		fn.Params = []*ir.Local{l.newThis(fn, class)}
	}
	outer := l.function
	l.function = fn // parameters are locals of fn
	defer func() { l.function = outer }()
	for _, param := range node.Parameters() {
		local := l.parameter(param)
		if local == nil {
			return nil
		}
		fn.Params = append(fn.Params, local)
	}
	if node.Kind == ast.KindConstructor {
		fn.Result = ir.Void
	} else if fn.Result = l.resultType(node); fn.Result == nil {
		return nil
	}
	return fn
}

// parameter makes the local for a parameter, or reports why it can't and returns nil.
func (l *lowerer) parameter(node *ast.Node) *ir.Local {
	param := node.AsParameterDeclaration()
	switch {
	case param.Name().Kind != ast.KindIdentifier:
		l.notSupportedYet(param.Name(), "destructuring is")
		return nil
	case param.DotDotDotToken != nil:
		l.notSupportedYet(node, "rest parameters are")
		return nil
	case param.QuestionToken != nil || param.Initializer != nil:
		l.notSupportedYet(node, "optional parameters are")
		return nil
	}
	symbol := l.checker.GetSymbolAtLocation(param.Name())
	t := l.numericType(param.Name())
	if t == nil {
		t = l.irType(l.checker.GetTypeOfSymbol(symbol), param.Name())
	}
	if t == nil {
		return nil
	}
	local := l.newLocal(param.Name().Text(), t)
	l.locals[symbol] = local
	return local
}

// resultType is the IR type a function returns: Void, the numeric type the semantic
// pass chose, or the type tsc gives it; nil (reported) if it has no IR type.
func (l *lowerer) resultType(node *ast.Node) ir.Type {
	if n, ok := l.info.ResultType(node); ok {
		return numericTypes[n]
	}
	result := l.checker.GetReturnTypeOfSignature(l.checker.GetSignatureFromDeclaration(node))
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
		return l.identifier(node)
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
		return l.propertyAccess(node)
	case ast.KindElementAccessExpression:
		return l.stringElement(node, ir.StringElementChecked)
	case ast.KindThisKeyword:
		return l.thisExpression(node)
	case ast.KindNewExpression:
		return l.newExpression(node)
	}
	l.notSupportedYet(node, describe(node)+" is")
	return nil
}

// valueExpression lowers an expression whose context takes any value, such as an
// argument of console.log. There s[i] keeps JS's undefined for an index out of range
// (D-015); everything else lowers as usual.
func (l *lowerer) valueExpression(node *ast.Node) ir.Expr {
	if inner := ast.SkipParentheses(node); l.isStringElement(inner) {
		return l.stringElement(inner, ir.StringElement)
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
	if local, ok := l.locals[symbol]; ok {
		if local = l.ownLocal(node, local); local == nil {
			return nil, true
		}
		return &ir.LocalRef{Local: local}, true
	}
	if _, ok := l.funcs[symbol]; ok {
		l.notSupportedYet(node, "function values are")
		return nil, true
	}
	if symbol != nil && symbol.Flags&ast.SymbolFlagsValueModule != 0 {
		l.notSupportedYet(node, "using the module namespace `"+scanner.GetTextOfNode(node)+"` as a value is")
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
	callee := l.target(ast.SkipParentheses(call.Expression))
	if method, ok := l.methods[l.memberDeclaration(ast.SkipParentheses(call.Expression))]; ok {
		if method == nil {
			return nil // its declaration failed, which was reported
		}
		return l.instanceMethodCall(call, method)
	}
	fn, isUserFunction := l.funcs[callee]
	builtin := l.builtin(call.Expression)
	switch {
	case isUserFunction && fn == nil:
		return nil // its declaration failed, which was reported
	case !isUserFunction && (builtin == nil || properties[builtin]):
		l.notSupportedYet(node, "calling `"+scanner.GetTextOfNode(call.Expression)+"` is")
		return nil
	case ast.IsOptionalChain(node):
		l.notSupportedYet(node, "optional chaining `?.` is")
		return nil
	}
	for _, argument := range call.Arguments.Nodes {
		if argument.Kind == ast.KindSpreadElement {
			l.notSupportedYet(argument, "spread arguments are")
			return nil
		}
	}
	switch {
	case isUserFunction:
		return l.userCall(call, fn)
	case builtin == ir.ConsoleLog:
		return l.consoleLog(call)
	}
	return l.methodCall(node, builtin)
}

// userCall calls a function of the program.
func (l *lowerer) userCall(call *ast.CallExpression, fn *ir.Func) ir.Expr {
	args := l.arguments(call.Arguments.Nodes, fn.Params)
	if args == nil {
		return nil
	}
	return &ir.Call{Func: fn, Args: args}
}

// arguments lowers the arguments of a call of a function of the program, each converted
// to its parameter's type, or reports one and returns nil.
func (l *lowerer) arguments(arguments []*ast.Node, params []*ir.Local) []ir.Expr {
	args := make([]ir.Expr, len(params))
	for i, param := range params {
		argument := arguments[i] // tsc checked the count; optional and rest parameters aren't lowered yet
		arg := l.expression(argument)
		if arg == nil {
			return nil
		}
		if args[i] = l.convert(arg, param.Type, argument); args[i] == nil {
			return nil
		}
	}
	return args
}

func (l *lowerer) consoleLog(call *ast.CallExpression) ir.Expr {
	args := make([]ir.Expr, 0, len(call.Arguments.Nodes))
	for _, argument := range call.Arguments.Nodes {
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
		value, _ := check.IntegerLiteralValue(node) // the pass checked it is an integer in range
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
	case ir.IsNumeric(t) || t == ir.Bool || t == ir.Null || t == ir.Undefined || t == ir.Value:
		return &ir.ToString{X: x}
	}
	l.notSupportedYet(node, fmt.Sprintf("converting %s to a string is", x.Type()))
	return nil
}

// propertyAccess lowers a property read: a binding of a module namespace (`m.x` after
// `import * as m`) or a builtin property; objects don't exist yet.
func (l *lowerer) propertyAccess(node *ast.Node) ir.Expr {
	member := l.memberDeclaration(node)
	if field := l.fields[member]; field != nil {
		return l.fieldAccess(node, field)
	}
	if accessors, ok := l.accessorProperty(node); ok {
		return l.propertyRead(node, accessors)
	}
	if _, ok := l.methods[member]; ok {
		l.notSupportedYet(node, "methods as values are")
		return nil
	}
	if l.isNamespace(node.Expression()) {
		if x, ok := l.binding(node, l.target(node)); ok {
			return x
		}
	}
	builtin := l.builtin(node)
	if !properties[builtin] {
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
	return l.checker.GetTypeAtLocation(node).Flags()&flags != 0
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
	case ast.IsOptionalChain(node):
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
	}
	x := l.expression(expr.Left)
	y := l.expression(expr.Right)
	if x == nil || y == nil {
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
	target, ok := l.assignmentTarget(expr.Left)
	value := l.expression(expr.Right)
	if !ok || value == nil {
		return nil
	}
	if value = l.convert(value, target.valueType(), expr.Right); value == nil {
		return nil
	}
	if target.local != nil || target.field != nil {
		return target.store(value, false) // C stores a field after evaluating the instance
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
	return l.readAndStore(target, func(target place) ir.Expr {
		current, t := target.read(), target.valueType()
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

// place is what an assignment stores to: a local, a field of an instance, or an
// accessor property of one, whose setter stores (and getter reads).
type place struct {
	local     *ir.Local
	object    ir.Expr // the instance, for a field or accessor property
	field     *ir.Field
	accessors property
	current   *ir.Local // an accessor property's value, read once by readAndStore
	stored    *ir.Local // the value stored in an accessor property, which is the assignment's value
	at        *ast.Node // the assignment's target, for an accessor property
	l         *lowerer
}

func (p place) valueType() ir.Type {
	switch {
	case p.local != nil:
		return p.local.Type
	case p.field != nil:
		return p.field.Type
	}
	return p.stored.Type
}

func (p place) read() ir.Expr {
	switch {
	case p.local != nil:
		return &ir.LocalRef{Local: p.local}
	case p.field != nil:
		return &ir.FieldRef{X: p.object, Field: p.field}
	}
	return &ir.LocalRef{Local: p.current}
}

func (p place) store(value ir.Expr, postfix bool) ir.Expr {
	switch {
	case p.local != nil:
		return &ir.Assign{Local: p.local, Value: value, Postfix: postfix}
	case p.field != nil:
		return &ir.SetField{X: p.object, Field: p.field, Value: value, Postfix: postfix}
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
// getter runs once, before build's expression.
func (l *lowerer) readAndStore(target place, build func(place) ir.Expr) ir.Expr {
	if target.local != nil {
		return build(target)
	}
	return l.withObject(target, func(target place) ir.Expr {
		if target.field != nil {
			return build(target)
		}
		if target.accessors.getter == nil {
			l.notSupportedYet(target.at, "reading a property that has no getter is")
			return nil
		}
		target.current = l.newLocal("current", target.valueType())
		result := build(target)
		if result == nil {
			return nil
		}
		return &ir.Bind{Local: target.current, Value: l.callMethod(target.object, nil, target.accessors.getter, nil), In: result}
	})
}

// withObject builds, with build, an expression that uses the instance of a field or
// property place, evaluated once into a temporary first unless it is a local.
func (l *lowerer) withObject(target place, build func(place) ir.Expr) ir.Expr {
	if isPure(target.object) {
		return build(target)
	}
	object := l.newLocal("object", target.object.Type())
	instance := target.object
	target.object = &ir.LocalRef{Local: object}
	result := build(target)
	if result == nil {
		return nil
	}
	return &ir.Bind{Local: object, Value: instance, In: result}
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
	case ast.KindPropertyAccessExpression:
		if field := l.fields[l.memberDeclaration(node)]; field != nil {
			object := l.fieldObject(node, field)
			return place{object: object, field: field}, object != nil
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
	case ast.IsOptionalChain(node):
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

// ownLocal returns local if the function being lowered may use it: its own, or one at
// the module scope of any module, which becomes a global. Otherwise it reports the use at
// node and returns nil: functions don't capture other variables yet.
func (l *lowerer) ownLocal(node *ast.Node, local *ir.Local) *ir.Local {
	switch {
	case local == nil || l.owners[local] == l.function:
		return local
	case l.moduleScope[local]:
		local.Global = true
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
	if !ir.IsNumeric(t) {
		l.notSupportedYet(node, fmt.Sprintf("%s on %s is", describe(node), t))
		return nil
	}
	op := ir.Add
	if operator == ast.KindMinusMinusToken {
		op = ir.Sub
	}
	return l.readAndStore(target, func(target place) ir.Expr {
		return target.store(&ir.Binary{Op: op, X: target.read(), Y: one(t)}, postfix)
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
	if x.Type() != y.Type() {
		l.notSupportedYet(node, fmt.Sprintf("%s with operands of types %s and %s is", describe(node), x.Type(), y.Type()))
		return nil, nil
	}
	return x, y
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
	case ir.IsNumeric(t) || t == ir.String || t == ir.Value:
		return &ir.Truthy{X: x}
	case isClass(t):
		return discard(x, &ir.BoolConst{Value: true}) // an instance, never null
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
	if x.Type() == ir.Value {
		return &ir.CallBuiltin{Builtin: ir.ValueTypeOf, Args: []ir.Expr{x}}
	}
	word, ok := typeofWords[x.Type()]
	switch {
	case ir.IsNumeric(x.Type()):
		word, ok = "number", true
	case isClass(x.Type()):
		word, ok = "object", true
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
	case *ir.LocalRef, *ir.NumberConst, *ir.BoolConst, *ir.StringConst, *ir.IntConst, *ir.NullConst, *ir.UndefinedConst:
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
		return ir.IsNumeric(x) || x == ir.Bool || x == ir.String || isClass(x)
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
	if class, ok := l.classes[t.Symbol()]; ok && t.Flags()&checker.TypeFlagsObject != 0 {
		if class == nil {
			return nil // the class failed, which was reported
		}
		return class
	}
	flags := t.Flags()
	switch {
	case flags&checker.TypeFlagsNumberLike != 0:
		return ir.Number
	case flags&checker.TypeFlagsBooleanLike != 0:
		return ir.Bool
	case flags&checker.TypeFlagsStringLike != 0:
		return ir.String
	case flags&checker.TypeFlagsUndefined != 0:
		return ir.Undefined
	case flags&checker.TypeFlagsNull != 0:
		return ir.Null
	}
	l.notSupportedYet(node, fmt.Sprintf("values of type %s are", l.checker.TypeToString(t)))
	return nil
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
