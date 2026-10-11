// Package lower turns a type-checked TypeScript AST into ParallelScript IR. It is
// where the compiler decides what it can compile: anything it cannot lower yet
// becomes a diagnostic.
package lower

import (
	"context"
	"fmt"
	"math"
	"strings"
	"unicode"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/internal/compiler"
	"github.com/microsoft/typescript-go/internal/jsnum"
	"github.com/microsoft/typescript-go/internal/scanner"
	"github.com/microsoft/typescript-go/pts/check"
	"github.com/microsoft/typescript-go/pts/diagnostic"
	"github.com/microsoft/typescript-go/pts/frontend"
	"github.com/microsoft/typescript-go/pts/ir"
	"github.com/microsoft/typescript-go/pts/lib"
)

// binaryOps maps TypeScript operator tokens to IR operators. == and != mean === and
// !== here because lowering only accepts operands of one primitive type.
var binaryOps = map[ast.Kind]ir.BinaryOp{
	ast.KindPlusToken:                    ir.Add,
	ast.KindMinusToken:                   ir.Sub,
	ast.KindAsteriskToken:                ir.Mul,
	ast.KindSlashToken:                   ir.Div,
	ast.KindPercentToken:                 ir.Rem,
	ast.KindAsteriskAsteriskToken:        ir.Pow,
	ast.KindLessThanToken:                ir.Lt,
	ast.KindLessThanEqualsToken:          ir.Le,
	ast.KindGreaterThanToken:             ir.Gt,
	ast.KindGreaterThanEqualsToken:       ir.Ge,
	ast.KindEqualsEqualsEqualsToken:      ir.Eq,
	ast.KindExclamationEqualsEqualsToken: ir.Ne,
	ast.KindEqualsEqualsToken:            ir.Eq,
	ast.KindExclamationEqualsToken:       ir.Ne,
}

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
	l := &lowerer{program: program.TS, checker: c, info: info, file: program.Entry, locals: map[*ast.Symbol]*ir.Local{}}
	main := &ir.Func{
		Name:   "main",
		Pos:    ir.Pos{File: program.Entry.FileName(), Line: 1},
		Result: ir.Void,
		Body:   l.block(program.Entry.AsNode(), program.Entry.Statements.Nodes),
	}
	return &ir.Program{Funcs: []*ir.Func{main}, Main: main}, l.errors
}

type lowerer struct {
	program *compiler.Program
	checker *checker.Checker
	info    *check.Info
	file    *ast.SourceFile
	locals  map[*ast.Symbol]*ir.Local // nil for a declaration that failed to lower
	errors  []diagnostic.Diagnostic
}

func (l *lowerer) pos(node *ast.Node) ir.Pos {
	start := scanner.GetTokenPosOfNode(node, l.file, false)
	return ir.Pos{File: l.file.FileName(), Line: scanner.GetECMALineOfPosition(l.file, start) + 1}
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
		if x := l.expression(node.Expression()); x != nil {
			return []ir.Stmt{&ir.ExprStmt{At: l.pos(node), X: x}}
		}
	case ast.KindVariableStatement:
		return l.variableStatement(node)
	case ast.KindBlock:
		return []ir.Stmt{l.block(node, node.Statements())}
	case ast.KindEmptyStatement, ast.KindInterfaceDeclaration, ast.KindTypeAliasDeclaration:
		// No runtime effect.
	case ast.KindExportDeclaration:
		if !isEmptyExport(node) {
			l.notSupportedYet(node, describe(node)+" is")
		}
	default:
		l.notSupportedYet(node, describe(node)+" is")
	}
	return nil
}

// isEmptyExport reports whether node is `export {}`, which only marks a file as a module.
func isEmptyExport(node *ast.Node) bool {
	export := node.AsExportDeclaration()
	return export.ModuleSpecifier == nil && export.ExportClause != nil &&
		export.ExportClause.Kind == ast.KindNamedExports && len(export.ExportClause.Elements()) == 0
}

func (l *lowerer) variableStatement(node *ast.Node) []ir.Stmt {
	list := node.AsVariableStatement().DeclarationList
	switch {
	case ast.HasSyntacticModifier(node, ast.ModifierFlagsAmbient):
		l.notSupportedYet(node, "`declare` is")
		return nil
	case list.Flags&ast.NodeFlagsBlockScoped == 0:
		l.unsupported(node, "`var` is not supported; use `let` or `const`")
		return nil
	case list.Flags&ast.NodeFlagsUsing != 0:
		l.notSupportedYet(node, "`using` is")
		return nil
	}
	var stmts []ir.Stmt
	for _, declaration := range list.AsVariableDeclarationList().Declarations.Nodes {
		if decl := l.variableDeclaration(declaration); decl != nil {
			stmts = append(stmts, decl)
		}
	}
	return stmts
}

func (l *lowerer) variableDeclaration(node *ast.Node) ir.Stmt {
	name := node.Name()
	if name.Kind != ast.KindIdentifier {
		l.notSupportedYet(name, "destructuring is")
		return nil
	}
	initializer := node.Initializer()
	if initializer == nil {
		l.notSupportedYet(node, "a variable without an initializer is")
		return nil
	}
	symbol := l.checker.GetSymbolAtLocation(name)
	l.locals[symbol] = nil // until it lowers, uses of it are silent: the declaration already failed
	localType := l.numericType(name)
	if localType == nil {
		localType = l.irType(l.checker.GetTypeOfSymbol(symbol), name)
	}
	if localType == nil {
		return nil
	}
	init := l.expression(initializer)
	if init == nil {
		return nil
	}
	init = l.convert(init, localType, initializer)
	if init == nil {
		return nil
	}
	local := &ir.Local{Name: name.Text(), Type: localType}
	l.locals[symbol] = local
	return &ir.LocalDecl{At: l.pos(node), Local: local, Init: init}
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
	default:
		l.notSupportedYet(node, fmt.Sprintf("converting %s to %s is", value.Type(), target))
		return nil
	}
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
	case ast.KindTemplateExpression:
		return l.template(node)
	case ast.KindPropertyAccessExpression:
		return l.propertyAccess(node)
	case ast.KindElementAccessExpression:
		return l.stringElement(node, ir.StringElementChecked)
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
	symbol := l.checker.GetSymbolAtLocation(node)
	if local, ok := l.locals[symbol]; ok {
		if local == nil {
			return nil
		}
		return &ir.LocalRef{Local: local}
	}
	if node.Text() == "undefined" {
		return &ir.UndefinedConst{} // a local named undefined was found above
	}
	if value, ok := libConstants[node.Text()]; ok && l.isDeclaredInDefaultLib(symbol) {
		return &ir.NumberConst{Value: value}
	}
	l.notSupportedYet(node, fmt.Sprintf("the global `%s` is", node.Text()))
	return nil
}

func (l *lowerer) call(node *ast.Node) ir.Expr {
	call := node.AsCallExpression()
	builtin := l.builtin(call.Expression)
	switch {
	case builtin == nil || properties[builtin]:
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
	if builtin == ir.ConsoleLog {
		return l.consoleLog(call)
	}
	return l.methodCall(node, builtin)
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

// propertyAccess lowers a property read; only builtin properties exist so far.
func (l *lowerer) propertyAccess(node *ast.Node) ir.Expr {
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
	if expr.OperatorToken.Kind == ast.KindPlusToken && l.checker.GetTypeAtLocation(node).Flags()&checker.TypeFlagsStringLike != 0 {
		return l.concatenation(node)
	}
	op, ok := binaryOps[expr.OperatorToken.Kind]
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
	if n, ok := l.info.OperandType(node); ok {
		x, y = l.convert(x, numericTypes[n], expr.Left), l.convert(y, numericTypes[n], expr.Right)
		if x == nil || y == nil {
			return nil
		}
	}
	if !acceptsOperands(op, x.Type(), y.Type()) {
		l.notSupportedYet(node, fmt.Sprintf("%s on %s and %s is", describe(node), x.Type(), y.Type()))
		return nil
	}
	return &ir.Binary{Op: op, X: x, Y: y}
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
		return ir.IsNumeric(x) || x == ir.Bool || x == ir.String
	case ir.Lt, ir.Le, ir.Gt, ir.Ge:
		return ir.IsNumeric(x) || x == ir.String
	}
	return ir.IsNumeric(x)
}

func (l *lowerer) prefixUnary(node *ast.Node) ir.Expr {
	expr := node.AsPrefixUnaryExpression()
	if expr.Operator == ast.KindExclamationToken {
		return l.not(node)
	}
	if expr.Operator != ast.KindMinusToken && expr.Operator != ast.KindPlusToken {
		l.notSupportedYet(node, describe(node)+" is")
		return nil
	}
	if expr.Operand.Kind == ast.KindNumericLiteral {
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
	if expr.Operator == ast.KindPlusToken {
		return x // +x is x for a numeric value
	}
	return &ir.Unary{Op: ir.Neg, X: x}
}

// not lowers `!b` for a boolean b.
func (l *lowerer) not(node *ast.Node) ir.Expr {
	operand := node.AsPrefixUnaryExpression().Operand
	x := l.expression(operand)
	if x == nil {
		return nil
	}
	if x.Type() != ir.Bool {
		l.notSupportedYet(node, fmt.Sprintf("%s on %s is", describe(node), x.Type()))
		return nil
	}
	return &ir.Unary{Op: ir.Not, X: x}
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

func box(value ir.Expr) ir.Expr {
	if value.Type() == ir.Value {
		return value
	}
	return &ir.Box{X: value}
}

// ---------------------------------------------------------------- types

// irType maps a checker type to its IR type, or reports it (at node) and returns nil.
func (l *lowerer) irType(t *checker.Type, node *ast.Node) ir.Type {
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
