package lower

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/pts/ir"
	"github.com/microsoft/typescript-go/pts/lib"
)

// Loose locals (D-090). tsc types `const next = argv[i + 1]` as a string, though the
// element may be past the array's end, and programs check such locals for undefined
// later (`if (next === undefined)`). A local whose initializer reads an element of an
// array, a string or a hash-map object, or that an array pattern binds, and that the
// program tests for undefined, holds `T | undefined`: its initializer reads undefined
// past the end as JS does, and each use of it as a T is checked (TypeError if it is
// undefined), as a dynamic value's is.

// collectUndefinedTests records the symbols of the variables that files test for
// undefined: compare with undefined or null, test for truth, `??`, `?.` or `typeof`.
func (l *lowerer) collectUndefinedTests(files []*ast.SourceFile) {
	l.undefinedTested = map[*ast.Symbol]bool{}
	var visit ast.Visitor
	visit = func(node *ast.Node) bool {
		for _, tested := range testedOperands(node) {
			if tested = ast.SkipParentheses(tested); tested.Kind == ast.KindIdentifier && l.isUndefinedTest(node, tested) {
				if symbol := l.checker.GetSymbolAtLocation(tested); symbol != nil {
					l.undefinedTested[symbol] = true
				}
			}
		}
		node.ForEachChild(visit)
		return false
	}
	for _, file := range files {
		file.AsNode().ForEachChild(visit)
	}
}

// isTestedOperand reports whether an identifier is an operand that its context tests for
// undefined (testedOperands), which reads a loose local as the union it holds.
func (l *lowerer) isTestedOperand(identifier *ast.Node) bool {
	node := identifier
	for node.Parent != nil && node.Parent.Kind == ast.KindParenthesizedExpression {
		node = node.Parent
	}
	if node.Parent == nil {
		return false
	}
	for _, tested := range testedOperands(node.Parent) {
		if tested == node {
			return l.isUndefinedTest(node.Parent, identifier)
		}
	}
	return false
}

// isLooseIdentifier reports whether node names a loose local.
func (l *lowerer) isLooseIdentifier(node *ast.Node) bool {
	node = ast.SkipParentheses(node)
	return node.Kind == ast.KindIdentifier && l.isLoose(l.target(node))
}

// isUndefinedTest reports whether node tests its operand tested for undefined: always,
// but for a call, only where it calls a function of the program whose parameter's type
// may be undefined (a type guard taking an unknown, `isPlainObject(existing)`).
func (l *lowerer) isUndefinedTest(node *ast.Node, tested *ast.Node) bool {
	if node.Kind != ast.KindCallExpression {
		return true
	}
	signature := l.checker.GetResolvedSignature(node)
	if signature == nil || signature.Declaration() == nil {
		return false
	}
	file := ast.GetSourceFileOfNode(signature.Declaration())
	if l.program.IsSourceFileDefaultLibrary(file.Path()) || lib.IsLibPath(file.FileName()) {
		return false // `n.toFixed(d)`: a builtin's optional parameter
	}
	contextual := l.checker.GetContextualType(tested, checker.ContextFlagsNone)
	return contextual != nil && mayHoldUndefined(contextual)
}

// testedOperands are the operands that node tests for undefined, or may (a call's arguments).
func testedOperands(node *ast.Node) []*ast.Node {
	switch node.Kind {
	case ast.KindBinaryExpression:
		binary := node.AsBinaryExpression()
		switch binary.OperatorToken.Kind {
		case ast.KindEqualsEqualsEqualsToken, ast.KindExclamationEqualsEqualsToken, ast.KindEqualsEqualsToken, ast.KindExclamationEqualsToken:
			switch { // `x === undefined`, `x == null`, and so on, either way round
			case isNullishLiteral(binary.Right):
				return []*ast.Node{binary.Left}
			case isNullishLiteral(binary.Left):
				return []*ast.Node{binary.Right}
			}
		case ast.KindQuestionQuestionToken, ast.KindQuestionQuestionEqualsToken, ast.KindAmpersandAmpersandToken, ast.KindBarBarToken:
			return []*ast.Node{binary.Left}
		}
	case ast.KindPrefixUnaryExpression:
		if node.AsPrefixUnaryExpression().Operator == ast.KindExclamationToken {
			return []*ast.Node{node.AsPrefixUnaryExpression().Operand}
		}
	case ast.KindTypeOfExpression, ast.KindIfStatement, ast.KindWhileStatement, ast.KindDoStatement:
		return []*ast.Node{node.Expression()}
	case ast.KindConditionalExpression:
		return []*ast.Node{node.AsConditionalExpression().Condition}
	case ast.KindForStatement:
		if condition := node.AsForStatement().Condition; condition != nil {
			return []*ast.Node{condition}
		}
	case ast.KindPropertyAccessExpression, ast.KindElementAccessExpression:
		if ast.IsOptionalChain(node) && node.QuestionDotToken() != nil {
			return []*ast.Node{node.Expression()}
		}
	case ast.KindCallExpression:
		if ast.IsOptionalChain(node) && node.QuestionDotToken() != nil {
			return []*ast.Node{node.Expression()}
		}
		return node.Arguments() // passed where undefined may go (isUndefinedTest)
	}
	return nil
}

// isNullishLiteral reports whether node is `undefined`, `null` or `void x`.
func isNullishLiteral(node *ast.Node) bool {
	node = ast.SkipParentheses(node)
	return node.Kind == ast.KindNullKeyword || node.Kind == ast.KindVoidExpression || node.Kind == ast.KindIdentifier && node.Text() == "undefined"
}

// isLoose reports whether a variable is a loose local: one of a type without undefined
// that an array pattern binds from an array (`const [op, arg] = line.split(" ")`, where
// JS binds undefined past the end and a program may use arg only where it is there), or
// whose initializer reads an element and that the program tests for undefined.
func (l *lowerer) isLoose(symbol *ast.Symbol) bool {
	if symbol == nil || symbol.ValueDeclaration == nil {
		return false
	}
	loose, known := l.looseSymbols[symbol]
	if !known {
		// A local declared `T | undefined` holds undefined whether or not it is tested, but
		// tsc narrows it to T by its initializer: its reads as a T are checked too (D-172).
		declaredWithUndefined := mayHoldUndefined(l.typeOfSymbol(symbol))
		tested := l.undefinedTested[symbol] || symbol.ValueDeclaration.Kind == ast.KindBindingElement
		loose = (tested || declaredWithUndefined) && l.readsElement(symbol.ValueDeclaration)
		l.looseSymbols[symbol] = loose
	}
	return loose
}

// readsElement reports whether a variable declaration's initializer reads an element of
// an array, a string or a hash-map object, or a binding element of an array pattern
// binds one of an array's elements, without a default.
func (l *lowerer) readsElement(declaration *ast.Node) bool {
	switch declaration.Kind {
	case ast.KindVariableDeclaration:
		initializer := declaration.Initializer()
		if declaration.Name().Kind != ast.KindIdentifier || initializer == nil {
			return false
		}
		return l.isElementRead(initializer)
	case ast.KindBindingElement:
		pattern := declaration.Parent
		element := declaration.AsBindingElement()
		if pattern.Kind != ast.KindArrayBindingPattern || element.DotDotDotToken != nil || element.Initializer != nil || pattern.Parent.Kind != ast.KindVariableDeclaration {
			return false
		}
		initializer := pattern.Parent.Initializer()
		return initializer != nil && l.isArrayType(l.typeAt(initializer))
	}
	return false
}

// isElementRead reports whether node reads an element of an array, a string or a
// hash-map object, which JS gives undefined for when it isn't there.
func (l *lowerer) isElementRead(node *ast.Node) bool {
	node = ast.SkipParentheses(node)
	return l.isArrayElement(node) || l.isTypedArrayElement(node) || l.isStringElement(node) || l.isRecordElement(node)
}

// mayHoldUndefined reports whether a type has undefined among its values, or may hold
// anything.
func mayHoldUndefined(t *checker.Type) bool {
	const undefined = checker.TypeFlagsUndefined | checker.TypeFlagsVoid | checker.TypeFlagsAny | checker.TypeFlagsUnknown
	if t.Flags()&checker.TypeFlagsUnion != 0 {
		for _, member := range t.Types() {
			if member.Flags()&undefined != 0 {
				return true
			}
		}
		return false
	}
	return t.Flags()&undefined != 0
}

// looseRead is the use of a loose local, x, where tsc gives it the type narrow: checked,
// since it may be undefined.
func (l *lowerer) looseRead(x ir.Expr, narrow ir.Type, node *ast.Node) ir.Expr {
	return l.checkedConversion(x, narrow, node)
}
