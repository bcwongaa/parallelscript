package parser

// PTS: ParallelScript's contextual keyword `parallel` (D-O05, D-120). `parallel e` parses
// as a call of `parallel` with the operand as its argument, marked NodeFlagsPtsParallel,
// which the prelude declares as `parallel<T>(task: T): Promise<Awaited<T>>`, so the
// checker types it; `parallel for (…)` is the for statement, marked the same way. Any
// other `parallel` is an identifier, so programs that use the name keep working.

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/core"
)

const ptsParallel = "parallel"

func (p *Parser) atPtsParallel() bool {
	return p.token == ast.KindIdentifier && p.scanner.TokenValue() == ptsParallel
}

// isPtsParallelOperator reports whether the current token is `parallel` as the operator:
// followed on the same line by a token that starts an expression other than `(`, `[`,
// `.`, `?.`, a template, `=`, an operator, `,`, `;`, `)` or `}` (D-O05).
func (p *Parser) isPtsParallelOperator() bool {
	return p.atPtsParallel() && p.lookAhead((*Parser).nextTokenStartsPtsParallelOperand)
}

func (p *Parser) nextTokenStartsPtsParallelOperand() bool {
	p.nextToken()
	if p.hasPrecedingLineBreak() {
		return false
	}
	switch p.token {
	case ast.KindOpenParenToken, ast.KindOpenBracketToken, ast.KindDotToken, ast.KindQuestionDotToken,
		ast.KindNoSubstitutionTemplateLiteral, ast.KindTemplateHead,
		ast.KindCommaToken, ast.KindSemicolonToken, ast.KindCloseParenToken, ast.KindCloseBraceToken,
		// Operators that follow an identifier: `parallel => …`, `c ? parallel : x`, `parallel!`, `parallel++`, `parallel as T`.
		ast.KindEqualsGreaterThanToken, ast.KindColonToken, ast.KindQuestionToken, ast.KindExclamationToken,
		ast.KindPlusPlusToken, ast.KindMinusMinusToken, ast.KindAsKeyword, ast.KindSatisfiesKeyword,
		ast.KindOfKeyword: // `for (parallel of items)`
		return false
	}
	if ast.IsAssignmentOperator(p.token) || ast.GetBinaryOperatorPrecedence(p.token) != ast.OperatorPrecedenceInvalid {
		return false
	}
	return p.isStartOfExpression()
}

// parsePtsParallelOperator parses `parallel e`: e is a unary expression, as await's
// operand is, or an arrow function (`parallel async () => …`).
func (p *Parser) parsePtsParallelOperator() *ast.Node {
	pos := p.nodePos()
	callee := p.parseIdentifier()
	var operand *ast.Node
	if p.token == ast.KindAsyncKeyword || p.lookAhead((*Parser).nextTokenIsEqualsGreaterThan) {
		operand = p.parseAssignmentExpressionOrHigher()
	} else {
		operand = p.parseSimpleUnaryExpression()
	}
	arguments := p.newNodeList(core.NewTextRange(operand.Pos(), operand.End()), []*ast.Node{operand})
	call := p.factory.NewCallExpression(callee, nil /*questionDotToken*/, nil /*typeArguments*/, arguments, ast.NodeFlagsNone)
	call.Flags |= ast.NodeFlagsPtsParallel // the factory keeps only a call's optional-chain flag
	return p.finishNode(call, pos)
}

func (p *Parser) nextTokenIsEqualsGreaterThan() bool {
	return p.nextToken() == ast.KindEqualsGreaterThanToken
}

// isPtsParallelFor reports whether the current token starts `parallel for (…)`.
func (p *Parser) isPtsParallelFor() bool {
	return p.atPtsParallel() && p.lookAhead((*Parser).nextTokenIsForOnSameLine)
}

func (p *Parser) nextTokenIsForOnSameLine() bool {
	return p.nextToken() == ast.KindForKeyword && !p.hasPrecedingLineBreak()
}

// parsePtsParallelFor parses `parallel for (…) body`: the for statement, marked, its
// range starting at `parallel`.
func (p *Parser) parsePtsParallelFor() *ast.Node {
	pos := p.nodePos()
	p.nextToken()
	statement := p.parseForOrForInOrForOfStatement()
	statement.Flags |= ast.NodeFlagsPtsParallel
	statement.Loc = core.NewTextRange(pos, statement.End())
	return statement
}
