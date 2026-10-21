package lower

import (
	"github.com/microsoft/typescript-go/internal/ast"
)

// Which tests narrowed a read (D-197). tsc's never for a read is reliable for ParallelScript
// only where kind tests produced it (`typeof`, instanceof, Array.isArray, a type predicate,
// `=== null`, the value's own truthiness): every value of the read's IR type that they rule
// out is one its declared type leaves out too. Tests of its value against other values (an
// equality, a switch on it, a property's truthiness) narrow through literal and enum types
// whose IR types hold more than tsc's types say: an enum holds any number, a class's literal
// field may hold what its base class writes there, an object or string cast to a type of
// literals isn't checked for them. Code they leave no value for may run without a lie.

// maxFlowNodes bounds how much of a read's flow graph testsValues follows.
const maxFlowNodes = 4096

// testsValues reports whether a test that may have narrowed node, a read of a variable,
// compares the variable's value, a property of it or a variable destructured beside it
// with other values (comparesValues). It follows tsc's flow graph back from node to its
// function's start, and from a closure's start to where the closure was made, and answers
// true where the graph is too large to follow.
func (l *lowerer) testsValues(node *ast.Node) bool {
	data := node.FlowNodeData()
	if data == nil || data.FlowNode == nil {
		return false
	}
	related := l.relatedSymbols(node)
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
			if l.comparesValues(flow.Node, related, 0) {
				return true
			}
		case flow.Flags&ast.FlowFlagsSwitchClause != 0:
			if l.switchComparesValues(flow.Node.AsFlowSwitchClauseData().SwitchStatement, related) {
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

// maxAliasDepth bounds how many aliased conditions (`const isAdd = a.kind === "add"`)
// comparesValues follows.
const maxAliasDepth = 8

// comparesValues reports whether condition, a test, compares a value of one of related's
// variables, or a property of one, with another value: an equality (`===`, `!==`, `==`,
// `!=`) neither of whose operands is null, undefined or a `typeof`, or the truthiness of a
// property (a discriminant's), also inside `!`, `&&`, `||`, `??` and the const conditions it
// names. A predicate's call, instanceof, `in` and the variable's own truthiness don't.
func (l *lowerer) comparesValues(condition *ast.Node, related map[*ast.Symbol]bool, depth int) bool {
	condition = ast.SkipParentheses(condition)
	switch condition.Kind {
	case ast.KindPrefixUnaryExpression:
		return l.comparesValues(condition.AsPrefixUnaryExpression().Operand, related, depth)
	case ast.KindBinaryExpression:
		binary := condition.AsBinaryExpression()
		switch binary.OperatorToken.Kind {
		case ast.KindAmpersandAmpersandToken, ast.KindBarBarToken, ast.KindQuestionQuestionToken:
			return l.comparesValues(binary.Left, related, depth) || l.comparesValues(binary.Right, related, depth)
		case ast.KindEqualsEqualsEqualsToken, ast.KindExclamationEqualsEqualsToken, ast.KindEqualsEqualsToken, ast.KindExclamationEqualsToken:
			left, right := ast.SkipParentheses(binary.Left), ast.SkipParentheses(binary.Right)
			if isNullishLiteral(left) || isNullishLiteral(right) || left.Kind == ast.KindTypeOfExpression || right.Kind == ast.KindTypeOfExpression {
				return false
			}
			return l.mentions(left, related) || l.mentions(right, related)
		}
	case ast.KindPropertyAccessExpression, ast.KindElementAccessExpression:
		return l.mentions(condition, related)
	case ast.KindIdentifier:
		if depth < maxAliasDepth {
			if initializer := aliasedCondition(l.checker.GetSymbolAtLocation(condition)); initializer != nil {
				return l.comparesValues(initializer, related, depth+1)
			}
		}
	}
	return false
}

// switchComparesValues reports whether a switch statement whose clauses narrow a read
// compares values (comparesValues): one on a value that mentions related's variables, not
// on `typeof` of one; for `switch (true)`, a case whose expression does.
func (l *lowerer) switchComparesValues(statement *ast.Node, related map[*ast.Symbol]bool) bool {
	switch expression := ast.SkipParentheses(statement.Expression()); expression.Kind {
	case ast.KindTypeOfExpression:
		return false
	case ast.KindTrueKeyword:
		for _, clause := range statement.AsSwitchStatement().CaseBlock.AsCaseBlock().Clauses.Nodes {
			if clause.Kind == ast.KindCaseClause && l.comparesValues(clause.Expression(), related, 0) {
				return true
			}
		}
		return false
	default:
		return l.mentions(expression, related)
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

// mentions reports whether expression reads one of related's variables.
func (l *lowerer) mentions(expression *ast.Node, related map[*ast.Symbol]bool) bool {
	found := false
	var visit ast.Visitor
	visit = func(node *ast.Node) bool {
		if found {
			return true
		}
		if node.Kind == ast.KindIdentifier && related[l.readSymbol(node)] {
			found = true
			return true
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
