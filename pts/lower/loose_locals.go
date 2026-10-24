package lower

import (
	"slices"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/pts/ir"
	"github.com/microsoft/typescript-go/pts/lib"
)

// Loose locals (D-090). tsc types `const next = argv[i + 1]` as a string, though the
// element may be past the array's end, and programs check such locals for undefined
// later (`if (next === undefined)`). A local whose initializer reads an element of an
// array, a string or a hash-map object and that the program tests for undefined, or that
// an array pattern binds from anything but a tuple, holds `T | undefined`: its
// initializer reads undefined past the end as JS does, and each use of it as a T is
// checked (TypeError if it is undefined), as a dynamic value's is; where undefined goes
// (an optional property, a `T | undefined`, `unknown`), it goes as it is.

// collectUndefinedTests records the symbols of the variables that files test for
// undefined: compare with undefined or null, test for truth, `??`, `?.` or `typeof`.
func (l *lowerer) collectUndefinedTests(files []*ast.SourceFile) {
	l.undefinedTested = map[*ast.Symbol]bool{}
	var visit ast.Visitor
	visit = func(node *ast.Node) bool {
		if isInstanceofTest(node) {
			node.ForEachChild(visit)
			return false // reads a loose local as its union, but doesn't make one (a dispatch over `shapes[i]`)
		}
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
	if !l.callsProgramFunction(node) {
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
		case ast.KindQuestionQuestionToken, ast.KindQuestionQuestionEqualsToken, ast.KindAmpersandAmpersandToken, ast.KindBarBarToken, ast.KindInstanceOfKeyword:
			return []*ast.Node{binary.Left} // `x instanceof C` is false for undefined
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

// isInstanceofTest reports whether node is `x instanceof C`.
func isInstanceofTest(node *ast.Node) bool {
	return node.Kind == ast.KindBinaryExpression && node.AsBinaryExpression().OperatorToken.Kind == ast.KindInstanceOfKeyword
}

// isNullishLiteral reports whether node is `undefined`, `null` or `void x`.
func isNullishLiteral(node *ast.Node) bool {
	node = ast.SkipParentheses(node)
	return node.Kind == ast.KindNullKeyword || node.Kind == ast.KindVoidExpression || node.Kind == ast.KindIdentifier && node.Text() == "undefined"
}

// isLoose reports whether a variable is a loose local: one of a type without undefined
// that an array pattern binds from anything but a tuple (`const [op, arg] =
// line.split(" ")`, a parameter's or a `for…of` binding's pattern, where JS binds
// undefined past the end and a program may use arg only where it is there), or whose
// initializer reads an element and that the program tests for undefined.
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
// binds, without a default, one of the items of what the pattern destructures other than
// a tuple: an array, string, map, set, iterator, iterable or dynamic value, which may end
// before the pattern does (D-172, H03), in a declaration, a parameter, a `for…of`
// binding, a nested pattern or a rest element's pattern (H11).
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
		if pattern.Kind != ast.KindArrayBindingPattern || element.DotDotDotToken != nil || element.Initializer != nil {
			return false
		}
		t := l.patternSourceType(pattern)
		if t.Flags()&checker.TypeFlagsTypeParameter != 0 {
			if constraint := l.checker.GetBaseConstraintOfType(t); constraint != nil {
				t = constraint // the same for every instance, which share this answer
			}
		}
		return !checker.IsTupleType(t) || isRestTuple(t) // a tuple's length is known, but for its rest (`[...T]`); anything else may end before the pattern does
	}
	return false
}

// patternSourceType is the type of what a binding pattern destructures: a declaration's
// initializer, or the type tsc gives the pattern's declaration (a parameter's, a `for…of`
// binding's item, a nested pattern's element, a rest element's array).
func (l *lowerer) patternSourceType(pattern *ast.Node) *checker.Type {
	// tsc's own type, not an instance's: isLoose's answer is the same for every instance
	if parent := pattern.Parent; parent.Kind == ast.KindVariableDeclaration && parent.Initializer() != nil {
		return l.checker.GetTypeAtLocation(parent.Initializer())
	}
	return l.checker.GetTypeAtLocation(pattern)
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

// placeTakesUndefined reports whether node goes to a place whose type holds undefined: a
// union with undefined, or any or unknown as the value of a place (`const u: unknown =
// s[9]`, an `unknown` parameter or result), where an element that isn't there is
// undefined, as in JS (D-015).
func (l *lowerer) placeTakesUndefined(node *ast.Node) bool {
	if l.contextTakesUndefined(node) {
		return true
	}
	return isPlaceValue(node) && l.placeType(node) == ir.Value
}

// goesWhereUndefinedGoes reports whether node is the value of a place whose type holds
// undefined (isPlaceValue): an optional property, a `T | undefined`, any or unknown, where
// a loose local goes as the union it holds (H11).
func (l *lowerer) goesWhereUndefinedGoes(node *ast.Node) bool {
	if !isPlaceValue(node) {
		return false
	}
	t := l.placeType(node)
	return t != nil && ir.HoldsUndefined(t)
}

// placeType is the type of the place whose value node is (isPlaceValue): the function's
// result for a returned value, its yield type for a yielded one, the field of the type an object literal makes for a
// property's value, a conditional's place for its branch, else node's contextual type
// (an array literal's element's only where the literal is made as its context's array or
// tuple type, an argument's only where the callee is the program's or a builtin declares
// the parameter with a type parameter, a property declaration's its declared type); nil
// if it has none or it has no IR type.
func (l *lowerer) placeType(node *ast.Node) ir.Type {
	for node.Parent.Kind == ast.KindParenthesizedExpression {
		node = node.Parent
	}
	switch node.Parent.Kind {
	case ast.KindArrayLiteralExpression:
		if contextual := l.contextualType(node.Parent); contextual == nil || !checker.IsTupleType(contextual) && !l.checker.IsArrayType(contextual) {
			return nil // a literal made as its own type (`console.log([k, v])`'s string[]), not its context's
		}
	case ast.KindReturnStatement, ast.KindArrowFunction:
		return l.function.Result
	case ast.KindYieldExpression:
		yielded, _, _ := l.iterationTypes(l.generatorType()) // as yieldPoint converts it
		return l.irTypeOrNone(yielded, node)
	case ast.KindPropertyAssignment, ast.KindShorthandPropertyAssignment:
		return l.literalFieldType(node.Parent)
	case ast.KindConditionalExpression:
		if !isPlaceValue(node.Parent) {
			return nil
		}
		return l.placeType(node.Parent)
	case ast.KindCallExpression, ast.KindNewExpression:
		if !l.callsProgramFunction(node.Parent) && !l.goesToTypeArgument(node.Parent, node) {
			return nil // a builtin's own parameter, which its lowering reads as tsc's type (`n.toFixed(d)`, `Object.keys(o)`)
		}
	case ast.KindPropertyDeclaration:
		return l.irTypeOrNone(l.typeOfSymbol(node.Parent.Symbol()), node) // `col?: string = column`, whose annotation lacks `?`'s undefined
	}
	contextual := l.contextualType(node)
	if contextual == nil {
		return nil
	}
	return l.irTypeOrNone(contextual, node)
}

// literalFieldType is the type of the field that property, of an object literal, gives
// a value of the shape or hash-map type objectLiteral makes it as (madeLiterals):
// `column?: string` of an Agg for `{ kind, column }`, the value type of a
// `Record<string, unknown>`; nil for a computed name, or a field that isn't a union
// where tsc's type has one (a `Partial<Record<K, V>>`'s).
func (l *lowerer) literalFieldType(property *ast.Node) ir.Type {
	literal, name := property.Parent, property.Name()
	if literal.Kind != ast.KindObjectLiteralExpression || name.Kind != ast.KindIdentifier && name.Kind != ast.KindStringLiteral && name.Kind != ast.KindNumericLiteral {
		return nil
	}
	switch made := l.madeLiterals[literal].(type) {
	case *ir.MapType:
		return made.Value
	case *ir.Class:
		if field := made.Field(name.Text()); field != nil {
			return field.Type
		}
	}
	return nil // not being made by objectLiteral (a value read before a yield)
}

// callsProgramFunction reports whether call calls a function of the program, not a
// builtin or a library function.
func (l *lowerer) callsProgramFunction(call *ast.Node) bool {
	signature := l.checker.GetResolvedSignature(call)
	if signature == nil || signature.Declaration() == nil {
		return false
	}
	file := ast.GetSourceFileOfNode(signature.Declaration())
	return !l.program.IsSourceFileDefaultLibrary(file.Path()) && !lib.IsLibPath(file.FileName())
}

// goesToTypeArgument reports whether argument, of a call of a builtin, goes to a parameter
// the builtin declares with one of its type parameters (`push(...items: T[])`,
// `set(key: K, value: V)` of an `unknown[]` or a `Map<string, unknown>`), which the call
// converts to its type argument's type.
func (l *lowerer) goesToTypeArgument(call *ast.Node, argument *ast.Node) bool {
	signature := l.checker.GetResolvedSignature(call)
	index := slices.Index(call.Arguments(), argument)
	if signature == nil || signature.Declaration() == nil || index < 0 {
		return false
	}
	parameters := signature.Declaration().Parameters()
	if len(parameters) == 0 {
		return false
	}
	parameter := parameters[min(index, len(parameters)-1)]
	if index >= len(parameters) && parameter.AsParameterDeclaration().DotDotDotToken == nil || parameter.Type() == nil {
		return false
	}
	t := l.checker.GetTypeFromTypeNode(parameter.Type())
	if parameter.AsParameterDeclaration().DotDotDotToken != nil && l.checker.IsArrayType(t) {
		t = l.checker.GetElementTypeOfArrayType(t) // `...items: T[]`
	}
	return t != nil && t.Flags()&checker.TypeFlagsTypeParameter != 0
}

// isPlaceValue reports whether node is the value a place takes as it is, converted to the
// place's type: a declaration's initializer, the right side of `=`, a
// returned value or an arrow's body, a yielded value, an argument, a property's value (a
// shorthand property's name), an element of an array literal, or a conditional's branch
// where the conditional is one (sameType converts both). An operand of `&&`, `||` or `??` isn't:
// its expression has a type of its own.
func isPlaceValue(node *ast.Node) bool {
	for node.Parent.Kind == ast.KindParenthesizedExpression {
		node = node.Parent
	}
	switch parent := node.Parent; parent.Kind {
	case ast.KindVariableDeclaration, ast.KindPropertyDeclaration, ast.KindPropertyAssignment:
		return parent.Initializer() == node // not a parameter's default, which converts to the parameter's narrowed type (C306)
	case ast.KindShorthandPropertyAssignment:
		return parent.Name() == node
	case ast.KindBinaryExpression:
		binary := parent.AsBinaryExpression()
		return binary.OperatorToken.Kind == ast.KindEqualsToken && binary.Right == node
	case ast.KindReturnStatement, ast.KindArrowFunction:
		return true
	case ast.KindYieldExpression:
		return parent.AsYieldExpression().AsteriskToken == nil
	case ast.KindCallExpression, ast.KindNewExpression:
		return slices.Contains(parent.Arguments(), node)
	case ast.KindArrayLiteralExpression:
		return true
	case ast.KindConditionalExpression:
		return parent.AsConditionalExpression().Condition != node && isPlaceValue(parent)
	}
	return false
}

// looseValue is x, a loose local's union read at node, as the value of a place undefined
// goes to: the type tsc narrowed it to there, or undefined (`hit` after `hit !== null`,
// whose union holds null too); checked where a call tsc doesn't follow may have changed
// it since it was narrowed, as D-140's reads are.
func (l *lowerer) looseValue(x ir.Expr, narrow ir.Type, node *ast.Node) ir.Expr {
	t := l.withUndefined(narrow)
	switch {
	case t == x.Type():
		return x
	case l.mayHaveChanged(node):
		return &ir.FromUnion{X: box(x), To: t} // a dynamic value's checked conversion
	}
	return &ir.FromUnion{X: x, To: t}
}

// looseRead is the use of a loose local, x, where tsc gives it the type narrow: checked,
// since it may be undefined.
func (l *lowerer) looseRead(x ir.Expr, narrow ir.Type, node *ast.Node) ir.Expr {
	if narrow == ir.I64 || narrow == ir.U64 {
		return &ir.FromUnion{X: box(x), To: narrow} // exact past 2^53: it holds one of these integers or undefined, never a double (D-012)
	}
	return l.checkedConversion(x, narrow, node)
}
