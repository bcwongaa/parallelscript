package lower

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
)

// Which tests narrowed a read (D-197). tsc's never for a read is reliable for ParallelScript
// only where kind tests produced it (`typeof`, instanceof, Array.isArray, a type predicate,
// `=== null`, the value's own truthiness) or comparisons of the variable itself with literals
// (`x === "skip"`, whose literal types the declared type's widest IR type is narrowed by
// again: noValueOfItsIRTypePasses): every value of the read's IR type that they rule out is
// one its declared type leaves out too. Tests of a property of it, of a variable
// destructured beside it or aliasing part of it, or of it against values that aren't
// literals, narrow through literal and enum types whose IR types hold more than tsc's types
// say: a class's literal field may hold what its base class writes there, an enum holds
// any number, an object or string cast to a type of literals isn't checked for them. Code
// they leave no value for may run without a lie.

// maxFlowNodes bounds how much of a read's flow graph testsValues follows.
const maxFlowNodes = 4096

// testedRead is what the tests that may narrow a read of a variable are about: the
// variable, and the variables its tests may narrow it through (relatedSymbols).
type testedRead struct {
	self    *ast.Symbol
	related map[*ast.Symbol]bool
}

// testsValues reports whether a test that may have narrowed node, a read of a variable,
// compares values (comparesValues). It follows tsc's flow graph back from node to its
// function's start, and from a closure's start to where the closure was made, and answers
// true where the graph is too large to follow.
func (l *lowerer) testsValues(node *ast.Node) bool {
	data := node.FlowNodeData()
	if data == nil || data.FlowNode == nil {
		return false
	}
	read := &testedRead{self: l.readSymbol(node), related: l.relatedSymbols(node)}
	seen := map[*ast.FlowNode]bool{}
	pending := []*ast.FlowNode{data.FlowNode}
	for len(pending) > 0 {
		flow := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		if flow == nil || seen[flow] {
			continue
		}
		if seen[flow] = true; len(seen) > maxFlowNodes {
			return true
		}
		switch {
		case flow.Flags&ast.FlowFlagsCondition != 0:
			if l.comparesValues(flow.Node, read, 0) {
				return true
			}
		case flow.Flags&ast.FlowFlagsSwitchClause != 0:
			if l.switchComparesValues(flow.Node.AsFlowSwitchClauseData().SwitchStatement, read) {
				return true
			}
		case flow.Flags&ast.FlowFlagsStart != 0:
			if container := flow.Node; container != nil && container.FlowNodeData() != nil {
				pending = append(pending, container.FlowNodeData().FlowNode) // a closure: where it was made
			}
			continue
		}
		if flow.Flags&ast.FlowFlagsLabel != 0 {
			for list := flow.Antecedents; list != nil; list = list.Next {
				pending = append(pending, list.Flow)
			}
			continue
		}
		pending = append(pending, flow.Antecedent)
	}
	return false
}

// maxAliasDepth bounds how many aliases (`const isAdd = a.kind === "add"`, `const kind =
// a.kind`) comparesValues and mentions follow.
const maxAliasDepth = 8

// comparesValues reports whether condition, a test, compares a value of read's variables,
// or a property of one, with another value: an equality (`===`, `!==`, `==`, `!=`) neither
// of whose operands is null, undefined or a `typeof`, other than of the variable itself
// with a literal (comparesItselfWithLiteral); or the truthiness of a property (a
// discriminant's); also inside `!`, `&&`, `||`, `??` and the const conditions it names. A
// predicate's call, instanceof, `in` and the variable's own truthiness don't.
func (l *lowerer) comparesValues(condition *ast.Node, read *testedRead, depth int) bool {
	condition = ast.SkipParentheses(condition)
	switch condition.Kind {
	case ast.KindPrefixUnaryExpression:
		return l.comparesValues(condition.AsPrefixUnaryExpression().Operand, read, depth)
	case ast.KindBinaryExpression:
		binary := condition.AsBinaryExpression()
		switch binary.OperatorToken.Kind {
		case ast.KindAmpersandAmpersandToken, ast.KindBarBarToken, ast.KindQuestionQuestionToken:
			return l.comparesValues(binary.Left, read, depth) || l.comparesValues(binary.Right, read, depth)
		case ast.KindEqualsEqualsEqualsToken, ast.KindExclamationEqualsEqualsToken, ast.KindEqualsEqualsToken, ast.KindExclamationEqualsToken:
			left, right := ast.SkipParentheses(binary.Left), ast.SkipParentheses(binary.Right)
			if isNullishLiteral(left) || isNullishLiteral(right) || left.Kind == ast.KindTypeOfExpression || right.Kind == ast.KindTypeOfExpression ||
				l.comparesItselfWithLiteral(left, right, read) || l.comparesItselfWithLiteral(right, left, read) {
				return false
			}
			return l.mentions(left, read, 0) || l.mentions(right, read, 0)
		}
	case ast.KindPropertyAccessExpression, ast.KindElementAccessExpression:
		return l.mentions(condition, read, 0)
	case ast.KindIdentifier:
		if depth < maxAliasDepth {
			if initializer := aliasedCondition(l.checker.GetSymbolAtLocation(condition)); initializer != nil {
				return l.comparesValues(initializer, read, depth+1)
			}
		}
	}
	return false
}

// comparesItselfWithLiteral reports whether an equality compares operand, the read's own
// variable, with literal: a literal (`"skip"`, `-1`, a template without substitutions, an
// enum member), whose type the declared type's widest IR type is narrowed by again
// (noValueOfItsIRTypePasses), or true or false for a variable whose declared type holds no
// literal types (`boolean`, `boolean | string`), since a boolean holds no other value.
func (l *lowerer) comparesItselfWithLiteral(operand, literal *ast.Node, read *testedRead) bool {
	if operand.Kind != ast.KindIdentifier || l.readSymbol(operand) != read.self {
		return false
	}
	switch literal.Kind {
	case ast.KindTrueKeyword, ast.KindFalseKeyword:
		return l.literalFree(l.checker.GetTypeOfSymbol(read.self), map[*checker.Type]bool{}, 0)
	}
	return l.isLiteralExpression(literal)
}

// isLiteralExpression reports whether node is a string, number or bigint literal (also
// negated), a template literal without substitutions, or an enum member.
func (l *lowerer) isLiteralExpression(node *ast.Node) bool {
	node = ast.SkipParentheses(node)
	switch node.Kind {
	case ast.KindStringLiteral, ast.KindNumericLiteral, ast.KindBigIntLiteral, ast.KindNoSubstitutionTemplateLiteral:
		return true
	case ast.KindPrefixUnaryExpression:
		unary := node.AsPrefixUnaryExpression()
		return unary.Operator == ast.KindMinusToken && l.isLiteralExpression(unary.Operand)
	case ast.KindPropertyAccessExpression, ast.KindElementAccessExpression:
		symbol := l.checker.GetSymbolAtLocation(node)
		return symbol != nil && symbol.Flags&ast.SymbolFlagsEnumMember != 0
	}
	return false
}

// switchComparesValues reports whether a switch statement whose clauses narrow a read
// compares values (comparesValues): one on a value that mentions read's variables, other
// than on the variable itself with literal cases, and not on `typeof` of one; for `switch
// (true)`, a case whose expression does.
func (l *lowerer) switchComparesValues(statement *ast.Node, read *testedRead) bool {
	clauses := statement.AsSwitchStatement().CaseBlock.AsCaseBlock().Clauses.Nodes
	switch expression := ast.SkipParentheses(statement.Expression()); expression.Kind {
	case ast.KindTypeOfExpression:
		return false
	case ast.KindTrueKeyword:
		for _, clause := range clauses {
			if clause.Kind == ast.KindCaseClause && l.comparesValues(clause.Expression(), read, 0) {
				return true
			}
		}
		return false
	default:
		if expression.Kind == ast.KindIdentifier && l.readSymbol(expression) == read.self {
			for _, clause := range clauses {
				if clause.Kind == ast.KindCaseClause && !l.isLiteralExpression(clause.Expression()) {
					return true
				}
			}
			return false // `switch (x)` with literal cases: as `x === "a"`
		}
		return l.mentions(expression, read, 0)
	}
}

// aliasedCondition is the initializer of symbol if it is a const that holds a condition
// tsc narrows by where the const is tested (`const isAdd = a.kind === "add"`), else nil.
func aliasedCondition(symbol *ast.Symbol) *ast.Node {
	if symbol == nil || symbol.ValueDeclaration == nil || symbol.ValueDeclaration.Kind != ast.KindVariableDeclaration || !ast.IsVarConst(symbol.ValueDeclaration) {
		return nil
	}
	return symbol.ValueDeclaration.Initializer()
}

// aliasedPart is what symbol, a const, holds part of in the two forms tsc narrows a value
// through (getCandidateDiscriminantPropertyAccess): `const kind = a.kind`'s access, and
// `const { kind } = a`'s initializer, a variable or an access; nil otherwise (an object
// literal's table, a call), which keeps mentions from walking initializers.
func aliasedPart(symbol *ast.Symbol) *ast.Node {
	if symbol == nil || symbol.ValueDeclaration == nil {
		return nil
	}
	declaration := symbol.ValueDeclaration
	destructured := declaration.Kind == ast.KindBindingElement
	if destructured {
		declaration = ast.GetRootDeclaration(declaration)
	}
	if declaration.Kind != ast.KindVariableDeclaration || !ast.IsVarConst(declaration) || declaration.Initializer() == nil {
		return nil
	}
	initializer := declaration.Initializer()
	for initializer.Kind == ast.KindParenthesizedExpression || initializer.Kind == ast.KindNonNullExpression {
		initializer = initializer.Expression()
	}
	if ast.IsAccessExpression(initializer) || destructured && initializer.Kind == ast.KindIdentifier {
		return initializer
	}
	return nil
}

// mentions reports whether expression reads one of read's variables, also through the
// consts that alias part of one (aliasedPart).
func (l *lowerer) mentions(expression *ast.Node, read *testedRead, depth int) bool {
	found := false
	var visit ast.Visitor
	visit = func(node *ast.Node) bool {
		if found {
			return true
		}
		if node.Kind == ast.KindIdentifier {
			symbol := l.readSymbol(node)
			if read.related[symbol] {
				found = true
			} else if part := aliasedPart(symbol); part != nil && depth < maxAliasDepth && l.mentions(part, read, depth+1) {
				found = true
			}
			return found
		}
		node.ForEachChild(visit)
		return found
	}
	visit(expression)
	return found
}

// relatedSymbols are the variables whose tests may narrow node, a read of a variable: the
// variable, and where it is destructured from an object or array, every variable that its
// pattern binds beside it (`kind` beside `payload` in `const { kind, payload } = action`).
func (l *lowerer) relatedSymbols(node *ast.Node) map[*ast.Symbol]bool {
	symbol := l.readSymbol(node)
	related := map[*ast.Symbol]bool{symbol: true}
	if symbol == nil || symbol.ValueDeclaration == nil || symbol.ValueDeclaration.Kind != ast.KindBindingElement {
		return related
	}
	pattern := symbol.ValueDeclaration.Parent
	for pattern.Parent != nil && pattern.Parent.Kind == ast.KindBindingElement {
		pattern = pattern.Parent.Parent // the outermost pattern
	}
	var bind func(pattern *ast.Node)
	bind = func(pattern *ast.Node) {
		for _, element := range pattern.Elements() {
			if element.Kind != ast.KindBindingElement {
				continue
			}
			if name := element.Name(); ast.IsBindingPattern(name) {
				bind(name)
			} else if sibling := l.checker.GetSymbolAtLocation(name); sibling != nil {
				related[sibling] = true
			}
		}
	}
	bind(pattern)
	return related
}
