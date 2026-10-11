// Package check is ParallelScript's semantic pass (pipeline step 2): the rules of
// docs/SPEC.md that tsc doesn't know, starting with the numeric types (§3.2). It runs
// on a program that type-checked, reports what breaks the rules, and records the
// numeric types it worked out for the lowering.
package check

import (
	"context"
	"math/big"
	"strings"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/internal/scanner"
	"github.com/microsoft/typescript-go/pts/diagnostic"
	"github.com/microsoft/typescript-go/pts/frontend"
	"github.com/microsoft/typescript-go/pts/lib"
)

// Info is what the pass learned about a program.
type Info struct {
	types    map[*ast.Node]Numeric // number-typed expressions and the names of number-typed variables
	operands map[*ast.Node]Numeric // binary operators (compound assignments and switch case clauses too) on numbers: the type both operands convert to
}

// TypeOf returns the numeric type of a number-typed expression or variable name.
func (i *Info) TypeOf(node *ast.Node) (Numeric, bool) {
	n, ok := i.types[node]
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
		tc:      tc,
		info:    &Info{types: map[*ast.Node]Numeric{}, operands: map[*ast.Node]Numeric{}},
		symbols: map[*ast.Symbol]Numeric{},
	}
	c.visit(program.Entry.AsNode())
	return c.info, c.errors
}

type pass struct {
	tc      *checker.Checker
	info    *Info
	symbols map[*ast.Symbol]Numeric // numeric type of each number-typed variable
	errors  []diagnostic.Diagnostic
}

func (c *pass) report(node *ast.Node, code string, format string, args ...any) {
	c.errors = append(c.errors, diagnostic.At(node, code, format, args...))
}

// visit checks node and everything below it.
func (c *pass) visit(node *ast.Node) {
	switch {
	case node.Kind == ast.KindVariableDeclaration:
		c.variableDeclaration(node)
		return
	case node.Kind == ast.KindBinaryExpression && isComparison(node) && c.isNumber(node.AsBinaryExpression().Left):
		c.operands(node, nil)
		return
	case node.Kind == ast.KindSwitchStatement && c.isNumber(node.Expression()):
		c.switchStatement(node)
		return
	case ast.IsExpression(node) && c.isNumber(node):
		c.expr(node, nil)
		return
	}
	node.ForEachChild(func(child *ast.Node) bool {
		c.visit(child)
		return false
	})
}

func (c *pass) isNumber(node *ast.Node) bool {
	return c.tc.GetTypeAtLocation(node).Flags()&checker.TypeFlagsNumberLike != 0
}

func (c *pass) variableDeclaration(node *ast.Node) {
	name, initializer := node.Name(), node.Initializer()
	if name.Kind == ast.KindIdentifier && initializer == nil && c.isNumber(name) {
		declared, _ := c.annotationType(node.Type()) // `let n: u8;`; without an annotation tsc wouldn't type it number
		c.symbols[c.tc.GetSymbolAtLocation(name)] = declared
		c.info.types[name] = declared
		return
	}
	if name.Kind != ast.KindIdentifier || initializer == nil || !c.isNumber(name) {
		node.ForEachChild(func(child *ast.Node) bool {
			c.visit(child)
			return false
		})
		return
	}
	symbol := c.tc.GetSymbolAtLocation(name)
	declared, annotated := c.annotationType(node.Type())
	if !annotated {
		declared = c.expr(initializer, nil) // `let n = i` takes i's type; a bare literal is a number
	} else {
		c.requireConvertible(initializer, c.expr(initializer, &declared), declared)
	}
	c.symbols[symbol] = declared
	c.info.types[name] = declared
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
	if !ImplicitlyConvertible(from, target) {
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
	switch typeNode.Kind {
	case ast.KindNumberKeyword:
		return Number, true
	case ast.KindParenthesizedType:
		return c.annotationType(typeNode.Type())
	case ast.KindTypeReference:
		ref := typeNode.AsTypeReferenceNode()
		symbol := c.tc.GetSymbolAtLocation(ref.TypeName)
		if ref.TypeArguments != nil || symbol == nil || len(symbol.Declarations) == 0 {
			return 0, false
		}
		declaration := symbol.Declarations[0]
		if declaration.Kind != ast.KindTypeAliasDeclaration {
			return 0, false
		}
		if lib.IsLibPath(ast.GetSourceFileOfNode(declaration).FileName()) {
			n, ok := preludeNumerics[symbol.Name]
			return n, ok
		}
		return c.annotationType(declaration.Type())
	}
	return 0, false
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
		case binaryOperatorIsArithmetic(node):
			return c.operands(node, expected)
		case operator == ast.KindEqualsToken:
			return c.assignment(node)
		case operator == ast.KindAmpersandAmpersandToken || operator == ast.KindBarBarToken:
			return c.pair(node, binary.Left, nil, binary.Right, expected, scanner.TokenToString(operator))
		case operator == ast.KindCommaToken:
			c.visit(binary.Left)
			return c.expr(binary.Right, expected)
		}
		if _, ok := CompoundOperator(binary.OperatorToken.Kind); ok {
			return c.compoundAssignment(node)
		}
	case ast.KindAsExpression:
		if target, ok := c.annotationType(node.Type()); ok {
			c.expr(node.Expression(), nil)
			return target
		}
	case ast.KindIdentifier:
		if n, ok := c.symbols[c.tc.GetSymbolAtLocation(node)]; ok {
			return n
		}
		return Number
	}
	node.ForEachChild(func(child *ast.Node) bool {
		c.visit(child)
		return false
	})
	return Number
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
	value, ok := IntegerLiteralValue(literal)
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
	leftUntyped, rightUntyped := isUntyped(left), isUntyped(right)
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
		if operandType, ok = unify(leftType, rightType); !ok {
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
	aUntyped, bUntyped := aType == nil && isUntyped(a), isUntyped(b)
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
		if result, ok = unify(*aType, bType); !ok {
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
	operandType := typed
	if typed.IsInteger() && !isIntegerConstant(untyped) {
		operandType = Number
	}
	c.expr(untyped, &operandType)
	return operandType
}

// ---------------------------------------------------------------- syntax helpers

// isUntyped reports whether node is a constant number expression with no type of its
// own: literals, possibly negated, parenthesised or combined by arithmetic.
func isUntyped(node *ast.Node) bool {
	switch node.Kind {
	case ast.KindNumericLiteral:
		return true
	case ast.KindParenthesizedExpression:
		return isUntyped(node.Expression())
	case ast.KindPrefixUnaryExpression:
		unary := node.AsPrefixUnaryExpression()
		return (unary.Operator == ast.KindMinusToken || unary.Operator == ast.KindPlusToken || unary.Operator == ast.KindTildeToken) && isUntyped(unary.Operand)
	case ast.KindBinaryExpression:
		binary := node.AsBinaryExpression()
		return binaryOperatorIsArithmetic(node) && isUntyped(binary.Left) && isUntyped(binary.Right)
	}
	return false
}

// isIntegerConstant reports whether every literal in an untyped expression is an integer.
func isIntegerConstant(node *ast.Node) bool {
	if node.Kind == ast.KindNumericLiteral {
		_, ok := IntegerLiteralValue(node)
		return ok
	}
	integer := true
	node.ForEachChild(func(child *ast.Node) bool {
		integer = integer && (!ast.IsExpression(child) || isIntegerConstant(child))
		return !integer
	})
	return integer
}

func binaryOperatorIsArithmetic(node *ast.Node) bool {
	return isArithmetic(node.AsBinaryExpression().OperatorToken.Kind)
}

// isArithmetic reports whether a binary operator computes a number from two numbers:
// the arithmetic and the bitwise operators.
func isArithmetic(operator ast.Kind) bool {
	switch operator {
	case ast.KindPlusToken, ast.KindMinusToken, ast.KindAsteriskToken, ast.KindSlashToken, ast.KindPercentToken, ast.KindAsteriskAsteriskToken,
		ast.KindAmpersandToken, ast.KindBarToken, ast.KindCaretToken,
		ast.KindLessThanLessThanToken, ast.KindGreaterThanGreaterThanToken, ast.KindGreaterThanGreaterThanGreaterThanToken:
		return true
	}
	return false
}

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

// IntegerLiteralValue is the exact value of a numeric literal, if it is an integer.
// It reads the source text, so integers above 2^53 keep every digit.
func IntegerLiteralValue(literal *ast.Node) (*big.Int, bool) {
	// Not literal.Text(): the scanner normalises that through a double.
	text := strings.ReplaceAll(scanner.GetTextOfNode(literal), "_", "")
	if value, ok := new(big.Int).SetString(text, 0); ok {
		return value, true
	}
	rat, ok := new(big.Rat).SetString(text) // decimals and exponents, e.g. 1e3
	if !ok || !rat.IsInt() {
		return nil, false
	}
	return rat.Num(), true
}
