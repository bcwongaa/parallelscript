// Package check is ParallelScript's semantic pass (pipeline step 2): the rules of
// docs/SPEC.md that tsc doesn't know, starting with the numeric types (§3.2). It runs
// on a program that type-checked, reports what breaks the rules, and records the
// numeric types it worked out for the lowering.
package check

import (
	"context"
	"math/big"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/internal/compiler"
	"github.com/microsoft/typescript-go/internal/scanner"
	"github.com/microsoft/typescript-go/pts/diagnostic"
	"github.com/microsoft/typescript-go/pts/frontend"
	"github.com/microsoft/typescript-go/pts/numeric"
)

// Info is what the pass learned about a program.
type Info struct {
	results  map[*ast.Node]Numeric // function declarations that return a number: the type they return
	types    map[*ast.Node]Numeric // number-typed expressions and the names of number-typed variables and parameters
	operands map[*ast.Node]Numeric // binary operators (compound assignments and switch case clauses too) on numbers: the type both operands convert to
}

// TypeOf returns the numeric type of a number-typed expression or variable name.
func (i *Info) TypeOf(node *ast.Node) (Numeric, bool) {
	n, ok := i.types[node]
	return n, ok
}

// ResultType returns the numeric type a function declaration that returns a number returns.
func (i *Info) ResultType(declaration *ast.Node) (Numeric, bool) {
	n, ok := i.results[declaration]
	return n, ok
}

// OperandType returns the type both operands of a binary operator on numbers convert to;
// for a compound assignment, the operator's operands; for a switch case clause, the
// switch's value and the case's.
func (i *Info) OperandType(node *ast.Node) (Numeric, bool) {
	n, ok := i.operands[node]
	return n, ok
}

// Program checks a program that loaded without errors.
func Program(ctx context.Context, program *frontend.Program) (*Info, []diagnostic.Diagnostic) {
	tc, release := program.TS.GetTypeCheckerForFile(ctx, program.Entry)
	defer release()
	c := &pass{
		program:   program.TS,
		tc:        tc,
		info:      &Info{results: map[*ast.Node]Numeric{}, types: map[*ast.Node]Numeric{}, operands: map[*ast.Node]Numeric{}},
		symbols:   map[*ast.Symbol]Numeric{},
		declared:  map[*ast.Node]bool{},
		functions: map[*ast.Node]*function{},
	}
	for _, module := range program.Modules {
		c.unsupportedFeatures(module.AsNode())
		c.visit(module.AsNode())
	}
	return c.info, c.errors
}

type pass struct {
	program   *compiler.Program
	tc        *checker.Checker
	info      *Info
	symbols   map[*ast.Symbol]Numeric // numeric type of each number-typed variable, parameter and `export default` value
	declared  map[*ast.Node]bool      // the variable declarations and `export default`s checked so far
	functions map[*ast.Node]*function // by declaration
	current   *functionBody           // the function whose body is being checked, or nil at the top level
	errors    []diagnostic.Diagnostic
}

func (c *pass) report(node *ast.Node, code string, format string, args ...any) {
	c.errors = append(c.errors, diagnostic.At(node, code, format, args...))
}

// visit checks node and everything below it.
func (c *pass) visit(node *ast.Node) {
	switch {
	case node.Kind == ast.KindVariableDeclaration || node.Kind == ast.KindPropertyDeclaration || node.Kind == ast.KindPropertySignature:
		c.variableDeclaration(node)
		return
	case node.Kind == ast.KindObjectLiteralExpression:
		c.objectLiteral(node)
		return
	case node.Kind == ast.KindExportAssignment:
		c.exportDefault(node)
		return
	case node.Kind == ast.KindBinaryExpression && isComparison(node) && c.isNumber(node.AsBinaryExpression().Left):
		c.operands(node, nil)
		return
	case node.Kind == ast.KindSwitchStatement && c.isNumber(node.Expression()):
		c.switchStatement(node)
		return
	case isFunctionWithBody(node):
		if fn := c.functionOf(node); !fn.visited {
			c.checkBody(node, fn)
		}
		c.visitSignatureTypes(node)
		if node.Kind == ast.KindArrowFunction || node.Kind == ast.KindFunctionExpression {
			c.functionConversion(node)
		}
		return
	case isSignature(node):
		c.functionOf(node) // its parameter and result types
		c.visitSignatureTypes(node)
		return
	case node.Kind == ast.KindReturnStatement && c.current != nil:
		c.returnStatement(node)
		return
	case ast.IsExpression(node) && c.isNumber(node):
		c.contextualExpr(node)
		return
	case node.Kind == ast.KindCallExpression && c.calledFunction(node) != nil:
		c.call(node, c.calledFunction(node))
		return
	case node.Kind == ast.KindNewExpression && c.constructorOf(node) != nil:
		c.call(node, c.constructorOf(node))
		return
	case isSuperCall(node) && c.superConstructor(node) != nil:
		c.call(node, c.superConstructor(node))
		return
	case ast.IsExpression(node) && !ast.IsPartOfTypeNode(node) && !ast.IsDeclarationName(node):
		c.functionConversion(node)
	}
	node.ForEachChild(func(child *ast.Node) bool {
		c.visit(child)
		return false
	})
}

// visitSignatureTypes visits the types a function's parameters and result are annotated
// with, so that the methods and properties of object types written there are typed.
func (c *pass) visitSignatureTypes(node *ast.Node) {
	for _, param := range node.Parameters() {
		if param.Type() != nil {
			c.visit(param.Type())
		}
	}
	if node.Type() != nil {
		c.visit(node.Type())
	}
}

func (c *pass) isNumber(node *ast.Node) bool {
	return IsNumber(c.tc.GetTypeAtLocation(node))
}

// variableDeclaration types a variable or a class field.
func (c *pass) variableDeclaration(node *ast.Node) {
	if c.declared[node] {
		return // checked early, when a function or another module used it
	}
	c.declared[node] = true
	name, initializer := node.Name(), node.Initializer()
	named := name.Kind == ast.KindIdentifier || name.Kind == ast.KindPrivateIdentifier // not a destructuring pattern
	if named && initializer == nil && c.isNumber(name) {
		symbol := c.tc.GetSymbolAtLocation(name)
		declared, _ := NumericOf(c.tc.GetTypeOfSymbol(symbol)) // `let n: u8;`, or b in `for (const b of bytes)`
		c.symbols[symbol] = declared
		c.info.types[name] = declared
		return
	}
	if !named || initializer == nil || !c.isNumber(name) {
		node.ForEachChild(func(child *ast.Node) bool {
			c.visit(child)
			return false
		})
		return
	}
	symbol := c.tc.GetSymbolAtLocation(name)
	declared, annotated := c.annotationType(node.Type())
	if !annotated && node.Kind == ast.KindPropertyDeclaration {
		declared, annotated = c.inheritedFieldType(node) // a redeclared field keeps its type
	}
	if !annotated {
		declared = c.expr(initializer, nil) // `let n = i` takes i's type; a bare literal is a number
	} else {
		c.requireConvertible(initializer, c.expr(initializer, &declared), declared)
	}
	c.symbols[symbol] = declared
	c.info.types[name] = declared
}

// objectLiteral types the number-typed properties of an object literal: each takes the
// numeric type of the property of the literal's contextual type, if it has one (and
// must convert to it), and otherwise its value's type.
func (c *pass) objectLiteral(node *ast.Node) {
	contextual := c.tc.GetContextualType(node, checker.ContextFlagsNone)
	for _, property := range node.AsObjectLiteralExpression().Properties.Nodes {
		var value *ast.Node
		switch property.Kind {
		case ast.KindPropertyAssignment:
			value = property.Initializer()
		case ast.KindShorthandPropertyAssignment:
			value = property.Name()
		}
		if value == nil || !c.isNumber(value) {
			c.visit(property)
			continue
		}
		var n Numeric
		if expected, ok := c.contextualPropertyType(contextual, property); ok {
			n = expected
			c.requireConvertible(value, c.expr(value, &n), n)
		} else {
			n = c.expr(value, nil)
		}
		c.symbols[c.tc.GetMergedSymbol(property.Symbol())] = n
		c.info.types[property.Name()] = n
	}
}

// contextualPropertyType returns the numeric type of the property of a literal's
// contextual type that a property of the literal gives a value, or of its index
// signature: a hash-map object's values (D-059).
func (c *pass) contextualPropertyType(contextual *checker.Type, property *ast.Node) (Numeric, bool) {
	if contextual == nil {
		return 0, false
	}
	if property.Symbol() != nil {
		if symbol := c.tc.GetPropertyOfType(contextual, property.Symbol().Name); symbol != nil {
			if n, ok := c.bindingType(symbol); ok {
				return n, true
			}
			return NumericContext(c.tc.GetTypeOfSymbol(symbol)) // value in Box<int>, declared as `value: T`
		}
	}
	for _, info := range c.tc.GetIndexInfosOfType(contextual) {
		if info.KeyType().Flags()&(checker.TypeFlagsString|checker.TypeFlagsNumber) != 0 {
			return NumericContext(info.ValueType()) // the lowering requires string and number index signatures to agree
		}
	}
	return 0, false
}

// exportDefault types `export default value`, which is a binding of its own in JS.
func (c *pass) exportDefault(node *ast.Node) {
	if c.declared[node] {
		return
	}
	c.declared[node] = true
	if value := node.Expression(); c.isNumber(value) {
		c.symbols[c.tc.GetMergedSymbol(node.Symbol())] = c.expr(value, nil)
	} else {
		c.visit(value)
	}
}

// bindingType returns the numeric type of a number-typed variable or `export default`
// value, checking its declaration first if the pass hasn't reached it yet: a function may
// use a module-scope binding declared below it, and a module one of a module checked later.
func (c *pass) bindingType(symbol *ast.Symbol) (Numeric, bool) {
	if declaration := symbol.ValueDeclaration; declaration != nil {
		switch {
		case declaration.Kind == ast.KindPropertyDeclaration || declaration.Kind == ast.KindPropertySignature ||
			declaration.Kind == ast.KindPropertyAssignment || declaration.Kind == ast.KindShorthandPropertyAssignment:
			symbol = c.tc.GetMergedSymbol(declaration.Symbol()) // not the instance's own copy of the member
		case declaration.Kind == ast.KindGetAccessor || declaration.Kind == ast.KindSetAccessor:
			return c.accessorType(symbol)
		case ast.IsParameterPropertyDeclaration(declaration, declaration.Parent):
			c.functionOf(declaration.Parent) // the constructor's signature types the parameter, and so the field
			symbol = declaration.Symbol()
		}
	}
	if n, ok := c.symbols[symbol]; ok {
		return n, true
	}
	if declaration := symbol.ValueDeclaration; declaration != nil && !c.declared[declaration] &&
		(declaration.Kind == ast.KindVariableDeclaration || declaration.Kind == ast.KindPropertyDeclaration || declaration.Kind == ast.KindPropertySignature ||
			declaration.Kind == ast.KindExportAssignment) {
		outer := c.current
		c.current = nil // the declaration's own code, not the function's
		c.visit(declaration)
		c.current = outer
	}
	n, ok := c.symbols[symbol]
	return n, ok
}

// accessorType returns the numeric type of an accessor property: its getter's result
// type, or without a getter, its setter's parameter type.
func (c *pass) accessorType(symbol *ast.Symbol) (Numeric, bool) {
	for _, declaration := range symbol.Declarations {
		if declaration.Kind == ast.KindGetAccessor && (declaration.Body() != nil || isSignature(declaration)) {
			if result := c.functionOf(declaration).result; result != nil {
				return *result, true
			}
		}
	}
	for _, declaration := range symbol.Declarations {
		if declaration.Kind == ast.KindSetAccessor {
			if params := c.functionOf(declaration).params; len(params) == 1 && params[0] != nil {
				return *params[0], true
			}
		}
	}
	return 0, false
}

// target returns the symbol node refers to, following imports and re-exports to what they name.
func (c *pass) target(node *ast.Node) *ast.Symbol {
	symbol := c.tc.GetSymbolAtLocation(node)
	if symbol != nil && symbol.Flags&ast.SymbolFlagsAlias != 0 {
		symbol = c.tc.GetAliasedSymbol(symbol)
	}
	return symbol
}

// switchStatement types the cases of a switch on a number: each case compares with the
// switch's value as === would.
func (c *pass) switchStatement(node *ast.Node) {
	tag := node.Expression()
	tagType := c.expr(tag, nil)
	for _, clause := range node.AsSwitchStatement().CaseBlock.AsCaseBlock().Clauses.Nodes {
		if clause.Kind == ast.KindCaseClause {
			c.info.operands[clause] = c.pair(clause, tag, &tagType, clause.Expression(), nil, "case")
		}
		for _, statement := range clause.Statements() {
			c.visit(statement)
		}
	}
}

// requireConvertible reports node if its type doesn't convert to target without `as`.
func (c *pass) requireConvertible(node *ast.Node, from Numeric, target Numeric) {
	if !numeric.ImplicitlyConvertible(from, target) {
		c.report(node, diagnostic.NumericConversion,
			"a %s doesn't convert to %s implicitly because the value could change; write `as %s` to convert it", from, target, target)
	}
}

// annotationType returns the numeric type a type annotation names, if it names one:
// number, a prelude numeric type, or a type alias of one.
func (c *pass) annotationType(typeNode *ast.Node) (Numeric, bool) {
	if typeNode == nil {
		return 0, false
	}
	return NumericOf(c.tc.GetTypeFromTypeNode(typeNode))
}

// ---------------------------------------------------------------- expressions

// expr types a number-typed expression. expected is the type its context wants, if any;
// literals take it. The result is recorded for the lowering.
func (c *pass) expr(node *ast.Node, expected *Numeric) Numeric {
	result := c.exprType(node, expected)
	c.info.types[node] = result
	return result
}

func (c *pass) exprType(node *ast.Node, expected *Numeric) Numeric {
	switch node.Kind {
	case ast.KindNumericLiteral:
		return c.literal(node, node, false, expected)
	case ast.KindParenthesizedExpression:
		return c.expr(node.Expression(), expected)
	case ast.KindPrefixUnaryExpression:
		unary := node.AsPrefixUnaryExpression()
		switch unary.Operator {
		case ast.KindPlusPlusToken, ast.KindMinusMinusToken:
			return c.expr(unary.Operand, nil)
		case ast.KindMinusToken, ast.KindPlusToken, ast.KindTildeToken:
			if unary.Operand.Kind == ast.KindNumericLiteral {
				result := c.literal(unary.Operand, node, unary.Operator == ast.KindMinusToken, expected)
				c.info.types[unary.Operand] = result
				return result
			}
			return c.expr(unary.Operand, expected)
		}
	case ast.KindPostfixUnaryExpression:
		return c.expr(node.AsPostfixUnaryExpression().Operand, nil)
	case ast.KindConditionalExpression:
		conditional := node.AsConditionalExpression()
		c.visit(conditional.Condition)
		return c.pair(node, conditional.WhenTrue, nil, conditional.WhenFalse, expected, "?:")
	case ast.KindBinaryExpression:
		binary := node.AsBinaryExpression()
		switch operator := binary.OperatorToken.Kind; {
		case numeric.IsArithmetic(node.AsBinaryExpression().OperatorToken.Kind):
			return c.operands(node, expected)
		case operator == ast.KindEqualsToken:
			return c.assignment(node)
		case operator == ast.KindAmpersandAmpersandToken || operator == ast.KindBarBarToken || operator == ast.KindQuestionQuestionToken:
			return c.pair(node, binary.Left, nil, binary.Right, expected, scanner.TokenToString(operator))
		case operator == ast.KindCommaToken:
			c.visit(binary.Left)
			return c.expr(binary.Right, expected)
		}
		if _, ok := CompoundOperator(binary.OperatorToken.Kind); ok {
			return c.compoundAssignment(node)
		}
	case ast.KindCallExpression:
		if declaration := c.calledFunction(node); declaration != nil {
			return c.call(node, declaration)
		}
	case ast.KindAsExpression:
		if target, ok := c.annotationType(node.Type()); ok {
			c.expr(node.Expression(), nil)
			return target
		}
	case ast.KindIdentifier:
		if symbol := c.target(node); symbol != nil {
			if n, ok := c.bindingType(symbol); ok {
				return n
			}
			if n, ok := NumericOf(c.tc.GetTypeOfSymbol(symbol)); ok {
				return n // its declared type, not the literal an equality may narrow it to
			}
			if n, ok := NumericContext(c.tc.GetTypeAtLocation(node)); ok {
				return n // a union variable, narrowed to its numeric member here
			}
		}
		return Number
	case ast.KindPropertyAccessExpression:
		if symbol := c.target(node); symbol != nil {
			if n, ok := c.bindingType(symbol); ok {
				c.visit(node.Expression())
				return n // a field, or m.x for a namespace import m
			}
			if n, ok := NumericContext(c.tc.GetTypeAtLocation(node)); ok {
				c.visit(node.Expression())
				return n // a property of a union type (`hits?: int`), narrowed to its numeric member here
			}
		}
	}
	node.ForEachChild(func(child *ast.Node) bool {
		c.visit(child)
		return false
	})
	if n, ok := NumericOf(c.tc.GetTypeAtLocation(node)); ok {
		return n // e.g. an element of a u8[], or what a function value returns
	}
	return Number
}

// contextualExpr types a number-typed expression whose context may expect a numeric
// type the pass doesn't otherwise follow, such as an element of an array literal or an
// argument of a built-in method: the expression takes the contextual type, and must
// convert to it.
func (c *pass) contextualExpr(node *ast.Node) {
	contextual := c.tc.GetContextualType(node, checker.ContextFlagsNone)
	if contextual == nil {
		c.expr(node, nil)
		return
	}
	expected, ok := NumericContext(contextual)
	if !ok {
		c.expr(node, nil)
		return
	}
	c.requireConvertible(node, c.expr(node, &expected), expected)
}

// literal types a numeric literal (negated if its parent is unary minus) from the
// type its context expects. at is the node to report at.
func (c *pass) literal(literal *ast.Node, at *ast.Node, negated bool, expected *Numeric) Numeric {
	if expected == nil || !expected.IsInteger() {
		if expected != nil && *expected == F32 {
			return F32
		}
		return Number
	}
	value, ok := numeric.IntegerLiteralValue(literal)
	if !ok {
		c.report(at, diagnostic.NumericLiteral, "%s is not an integer, so it can't be a %s; write `as %s` to convert it", scanner.GetTextOfNode(literal), *expected, *expected)
		return *expected
	}
	if negated {
		value.Neg(value)
	}
	if value.Cmp(big.NewInt(expected.Min())) < 0 || value.Cmp(new(big.Int).SetUint64(expected.Max())) > 0 {
		c.report(at, diagnostic.NumericLiteral, "%s doesn't fit in %s (%d to %d)", value, *expected, expected.Min(), expected.Max())
	}
	return *expected
}

// operands types a binary operator on numbers and records the type its operands
// convert to. For arithmetic that is also the result's type.
func (c *pass) operands(node *ast.Node, expected *Numeric) Numeric {
	binary := node.AsBinaryExpression()
	left, right := binary.Left, binary.Right
	leftUntyped, rightUntyped := numeric.IsUntyped(left), numeric.IsUntyped(right)
	var operandType Numeric
	switch {
	case leftUntyped && rightUntyped:
		operandType = Number
		if expected != nil && !isComparison(node) {
			operandType = *expected
		}
		c.expr(left, &operandType)
		c.expr(right, &operandType)
	case leftUntyped:
		operandType = c.withUntyped(c.expr(right, nil), left)
	case rightUntyped:
		operandType = c.withUntyped(c.expr(left, nil), right)
	default:
		leftType, rightType := c.expr(left, nil), c.expr(right, nil)
		var ok bool
		if operandType, ok = numeric.Unify(leftType, rightType); !ok {
			c.report(node, diagnostic.NumericMix, "`%s` can't mix %s and %s, because neither converts to the other without loss; convert one with `as`",
				scanner.TokenToString(binary.OperatorToken.Kind), leftType, rightType)
			operandType = leftType
		}
	}
	c.info.operands[node] = operandType
	return operandType
}

// assignment types `x = value` for a number-typed x: the value converts to x's type.
func (c *pass) assignment(node *ast.Node) Numeric {
	binary := node.AsBinaryExpression()
	target := c.expr(binary.Left, nil)
	if n, ok := NumericContext(c.tc.GetTypeAtLocation(binary.Left)); ok && !c.isNumber(binary.Left) {
		target = n // a union's numeric member: `b = 300` for `b: u8 | null`
	}
	c.requireConvertible(binary.Right, c.expr(binary.Right, &target), target)
	return target
}

// compoundAssignment types `x op= value` as `x = x op value`.
func (c *pass) compoundAssignment(node *ast.Node) Numeric {
	target := c.expr(node.AsBinaryExpression().Left, nil)
	if op, _ := CompoundOperator(node.AsBinaryExpression().OperatorToken.Kind); isLogical(op) {
		c.requireConvertible(node.AsBinaryExpression().Right, c.expr(node.AsBinaryExpression().Right, &target), target)
		return target
	}
	c.requireConvertible(node, c.operands(node, &target), target)
	return target
}

// pair types two expressions that must end up with one type: the branches of ?:, the
// operands of && and ||, a switch value and a case. Either may already be typed (aType
// non-nil, for a switch's value). The type is recorded for node and returned.
func (c *pass) pair(node *ast.Node, a *ast.Node, aType *Numeric, b *ast.Node, expected *Numeric, what string) Numeric {
	var result Numeric
	aUntyped, bUntyped := aType == nil && numeric.IsUntyped(a), numeric.IsUntyped(b)
	switch {
	case aUntyped && bUntyped:
		result = Number
		if expected != nil {
			result = *expected
		}
		c.expr(a, &result)
		c.expr(b, &result)
	case bUntyped:
		if aType == nil {
			aType = ptr(c.expr(a, expected))
		}
		result = c.withUntyped(*aType, b)
	case aUntyped:
		result = c.withUntyped(c.expr(b, expected), a)
	default:
		if aType == nil {
			aType = ptr(c.expr(a, expected))
		}
		bType := c.expr(b, expected)
		var ok bool
		if result, ok = numeric.Unify(*aType, bType); !ok {
			c.report(node, diagnostic.NumericMix, "`%s` can't mix %s and %s, because neither converts to the other without loss; convert one with `as`", what, *aType, bType)
			result = *aType
		}
	}
	return result
}

func ptr(n Numeric) *Numeric { return &n }

// withUntyped types an untyped operand next to an operand of type typed: integer
// constants take the typed side's type; other constants are numbers.
func (c *pass) withUntyped(typed Numeric, untyped *ast.Node) Numeric {
	operandType := numeric.WithUntyped(typed, untyped)
	c.expr(untyped, &operandType)
	return operandType
}

// isNamespace reports whether node names a module namespace object: `m` after `import * as m`.
func (c *pass) isNamespace(node *ast.Node) bool {
	symbol := c.target(node)
	return symbol != nil && symbol.Flags&ast.SymbolFlagsValueModule != 0
}

// ---------------------------------------------------------------- syntax helpers

func isLogical(operator ast.Kind) bool {
	return operator == ast.KindAmpersandAmpersandToken || operator == ast.KindBarBarToken || operator == ast.KindQuestionQuestionToken
}

// compoundOperators map each compound assignment operator to the binary operator it applies.
var compoundOperators = map[ast.Kind]ast.Kind{
	ast.KindPlusEqualsToken: ast.KindPlusToken, ast.KindMinusEqualsToken: ast.KindMinusToken,
	ast.KindAsteriskEqualsToken: ast.KindAsteriskToken, ast.KindSlashEqualsToken: ast.KindSlashToken,
	ast.KindPercentEqualsToken: ast.KindPercentToken, ast.KindAsteriskAsteriskEqualsToken: ast.KindAsteriskAsteriskToken,
	ast.KindAmpersandEqualsToken: ast.KindAmpersandToken, ast.KindBarEqualsToken: ast.KindBarToken, ast.KindCaretEqualsToken: ast.KindCaretToken,
	ast.KindLessThanLessThanEqualsToken: ast.KindLessThanLessThanToken, ast.KindGreaterThanGreaterThanEqualsToken: ast.KindGreaterThanGreaterThanToken,
	ast.KindGreaterThanGreaterThanGreaterThanEqualsToken: ast.KindGreaterThanGreaterThanGreaterThanToken,
	ast.KindAmpersandAmpersandEqualsToken:                ast.KindAmpersandAmpersandToken, ast.KindBarBarEqualsToken: ast.KindBarBarToken,
	ast.KindQuestionQuestionEqualsToken: ast.KindQuestionQuestionToken,
}

// CompoundOperator returns the binary operator a compound assignment operator applies: + for +=.
func CompoundOperator(operator ast.Kind) (ast.Kind, bool) {
	op, ok := compoundOperators[operator]
	return op, ok
}

func isComparison(node *ast.Node) bool {
	switch node.AsBinaryExpression().OperatorToken.Kind {
	case ast.KindLessThanToken, ast.KindLessThanEqualsToken, ast.KindGreaterThanToken, ast.KindGreaterThanEqualsToken,
		ast.KindEqualsEqualsEqualsToken, ast.KindExclamationEqualsEqualsToken, ast.KindEqualsEqualsToken, ast.KindExclamationEqualsToken:
		return true
	}
	return false
}
