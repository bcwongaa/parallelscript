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
// undefined: compare with undefined or null, test for truth, `??`, `?.` or `typeof`; and
// the values that assignments store in variables (writtenValues, patternWrites).
func (l *lowerer) collectUndefinedTests(files []*ast.SourceFile) {
	l.undefinedTested = map[*ast.Symbol]bool{}
	l.assignedValues = map[*ast.Symbol][]*ast.Node{}
	l.patternWrites = map[*ast.Symbol][]*ast.Node{}
	var visit ast.Visitor
	visit = func(node *ast.Node) bool {
		if isInstanceofTest(node) {
			node.ForEachChild(visit)
			return false // reads a loose local as its union, but doesn't make one (a dispatch over `shapes[i]`)
		}
		if target, value := assignedVariable(node); target != nil {
			if symbol := l.assignedSymbol(target); symbol != nil {
				l.assignedValues[symbol] = append(l.assignedValues[symbol], value)
			}
		}
		for _, element := range arrayPatternTargets(node) {
			if symbol := l.checker.GetSymbolAtLocation(element); symbol != nil {
				l.patternWrites[symbol] = append(l.patternWrites[symbol], element)
			}
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

// assignedVariable is the variable that node, an assignment (`x = v`, `x ??= v`, `x ||= v`,
// `x &&= v`) or a shorthand target's default in an object assignment pattern (`({ p = v } =
// o)`, review round 3, R3-F3), stores in, and the value it stores; nil for any other node.
func assignedVariable(node *ast.Node) (target *ast.Node, value *ast.Node) {
	if node.Kind == ast.KindShorthandPropertyAssignment {
		if initializer := node.AsShorthandPropertyAssignment().ObjectAssignmentInitializer; initializer != nil {
			return node.Name(), initializer
		}
	}
	if node.Kind != ast.KindBinaryExpression {
		return nil, nil
	}
	binary := node.AsBinaryExpression()
	switch binary.OperatorToken.Kind {
	case ast.KindEqualsToken, ast.KindQuestionQuestionEqualsToken, ast.KindBarBarEqualsToken, ast.KindAmpersandAmpersandEqualsToken:
		if left := ast.SkipParentheses(binary.Left); left.Kind == ast.KindIdentifier {
			return left, binary.Right
		}
	}
	return nil, nil
}

// assignedSymbol is the variable that target, an assignedVariable, names: a shorthand
// target's is its value's, not the property's.
func (l *lowerer) assignedSymbol(target *ast.Node) *ast.Symbol {
	if target.Parent.Kind == ast.KindShorthandPropertyAssignment && target.Parent.Name() == target {
		return l.checker.GetShorthandAssignmentValueSymbol(target.Parent)
	}
	return l.checker.GetSymbolAtLocation(target)
}

// arrayPatternTargets are the variables that node, a destructuring assignment that isn't
// itself part of a pattern, assigns elements of arrays to without a default: an identifier
// that is an element of an array literal anywhere in its pattern, nested in array
// literals, in object literals' property values and under defaults (`({ a: [h, s] } =
// o)`, `[[h, s] = d] = rows`; review round 2, R2-F1); none for any other node.
func arrayPatternTargets(node *ast.Node) []*ast.Node {
	if !isDestructuringAssignment(node) || isPatternElement(node) {
		return nil
	}
	var targets []*ast.Node
	var collect func(pattern *ast.Node)
	collect = func(pattern *ast.Node) {
		switch pattern = ast.SkipParentheses(pattern); pattern.Kind {
		case ast.KindArrayLiteralExpression:
			for _, element := range pattern.AsArrayLiteralExpression().Elements.Nodes {
				if target := ast.SkipParentheses(element); target.Kind == ast.KindIdentifier {
					targets = append(targets, target)
					continue
				}
				collect(element)
			}
		case ast.KindObjectLiteralExpression:
			for _, property := range pattern.AsObjectLiteralExpression().Properties.Nodes {
				if property.Kind == ast.KindPropertyAssignment {
					collect(property.Initializer())
				}
			}
		case ast.KindBinaryExpression: // `target = default`, whose default replaces undefined, but not a nested pattern's elements'
			if binary := pattern.AsBinaryExpression(); binary.OperatorToken.Kind == ast.KindEqualsToken {
				if left := ast.SkipParentheses(binary.Left); left.Kind == ast.KindArrayLiteralExpression || left.Kind == ast.KindObjectLiteralExpression {
					collect(left)
				}
			}
		}
	}
	collect(node.AsBinaryExpression().Left)
	return targets
}

// isDestructuringAssignment reports whether node is `=` with an array or object literal on
// its left.
func isDestructuringAssignment(node *ast.Node) bool {
	if node.Kind != ast.KindBinaryExpression || node.AsBinaryExpression().OperatorToken.Kind != ast.KindEqualsToken {
		return false
	}
	left := ast.SkipParentheses(node.AsBinaryExpression().Left)
	return left.Kind == ast.KindArrayLiteralExpression || left.Kind == ast.KindObjectLiteralExpression
}

// isPatternElement reports whether node, an expression, is part of the pattern on the left
// of a destructuring assignment (`[[h, s] = d] = rows`'s `[h, s] = d`), not a value.
func isPatternElement(node *ast.Node) bool {
	for parent := node.Parent; parent != nil; node, parent = parent, parent.Parent {
		switch parent.Kind {
		case ast.KindParenthesizedExpression, ast.KindArrayLiteralExpression, ast.KindObjectLiteralExpression, ast.KindPropertyAssignment, ast.KindSpreadElement, ast.KindSpreadAssignment:
		case ast.KindBinaryExpression:
			// the left of a destructuring assignment, or of a `target = default` in a pattern; its
			// right (a default, or the value assigned) is a value
			binary := parent.AsBinaryExpression()
			return binary.OperatorToken.Kind == ast.KindEqualsToken && binary.Left == node
		default:
			return false
		}
	}
	return false
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
// but for a call's argument, only where it calls a function of the program whose
// parameter's type may be undefined (a type guard taking an unknown,
// `isPlainObject(existing)`); `f?.()` tests its callee.
func (l *lowerer) isUndefinedTest(node *ast.Node, tested *ast.Node) bool {
	if node.Kind != ast.KindCallExpression || ast.SkipParentheses(node.Expression()) == ast.SkipParentheses(tested) {
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
	case ast.KindSwitchStatement:
		clauses := node.AsSwitchStatement().CaseBlock.AsCaseBlock().Clauses.Nodes
		if slices.ContainsFunc(clauses, func(clause *ast.Node) bool {
			return clause.Kind == ast.KindCaseClause && isNullishLiteral(clause.Expression())
		}) {
			return []*ast.Node{node.Expression()} // `case undefined:`
		}
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
		// decided once for every instance of the code symbol is in, whichever is being lowered
		// (or none: a module variable written a generic function's local; review round 3, R3-F2)
		l.decidingLooseness++
		l.decideLooseness(symbol)
		l.decidingLooseness--
		loose = l.looseSymbols[symbol]
	}
	return loose
}

// decideLooseness decides whether root is a loose local, with every variable whose
// looseness root's depends on through the values written to it (`x = y`: x is loose if y
// is), as the least fixpoint over that graph of writes: a cycle (`x = y; y = x; y =
// list[3]`) is decided as a whole, whichever variable is asked about first (review rounds
// 1 and 2, R1-F2, R2-F2: deciding each variable by recursion took time exponential in a
// dense graph's size).
func (l *lowerer) decideLooseness(root *ast.Symbol) {
	loose := map[*ast.Symbol]bool{}
	dependents := map[*ast.Symbol][]*ast.Symbol{}
	var decided, queue []*ast.Symbol
	pending := []*ast.Symbol{root}
	seen := map[*ast.Symbol]bool{root: true}
	for len(pending) > 0 {
		symbol := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		decided = append(decided, symbol)
		own, sources := l.ownLooseness(symbol)
		if own {
			loose[symbol] = true
			queue = append(queue, symbol)
			continue
		}
		for _, source := range sources {
			if known, ok := l.looseSymbols[source]; ok {
				if known && !loose[symbol] {
					loose[symbol] = true
					queue = append(queue, symbol)
				}
				continue
			}
			dependents[source] = append(dependents[source], symbol)
			if !seen[source] {
				seen[source] = true
				pending = append(pending, source)
			}
		}
	}
	for len(queue) > 0 {
		source := queue[0]
		queue = queue[1:]
		for _, symbol := range dependents[source] {
			if !loose[symbol] {
				loose[symbol] = true
				queue = append(queue, symbol)
			}
		}
	}
	for _, symbol := range decided {
		l.looseSymbols[symbol] = loose[symbol]
	}
}

// ownLooseness reports whether a variable (symbol) is a loose local whatever the other
// variables are, and else the variables whose looseness would make it one: those written
// to it where it narrows by writes. A local declared `T | undefined` holds undefined
// whether or not it is tested, but tsc narrows it to T by its initializer or by an
// assignment: its reads as a T are checked too (D-172, C326).
func (l *lowerer) ownLooseness(symbol *ast.Symbol) (bool, []*ast.Symbol) {
	if symbol.ValueDeclaration == nil {
		return false, nil
	}
	declaredWithUndefined := mayHoldUndefined(l.normalized(l.checker.GetTypeOfSymbol(symbol))) // a T's instance for `string | undefined` holds a union already: tsc doesn't narrow a T
	tested := l.undefinedTested[symbol] || symbol.ValueDeclaration.Kind == ast.KindBindingElement || l.iteratesHoles(symbol.ValueDeclaration)
	if (tested || declaredWithUndefined) && l.readsElement(symbol.ValueDeclaration) {
		return true, nil
	}
	if !l.narrowsByWrites(symbol) {
		return false, nil
	}
	if slices.ContainsFunc(l.patternWrites[symbol], l.patternElementMayBeMissing) {
		return true, nil
	}
	var sources []*ast.Symbol
	for _, value := range l.writtenValues(symbol) {
		if mayHoldUndefined(l.checker.GetTypeAtLocation(value)) {
			continue // tsc's own type leaves the undefined in, as narrowsByWrites
		}
		missing, loose := l.missingSources(value)
		if missing {
			return true, nil
		}
		sources = append(sources, loose...)
	}
	return false, sources
}

// narrowsByWrites reports whether tsc narrows a variable (symbol) to the type of what is
// written to it, which may leave out the undefined its declared type holds: a union with
// undefined, or unknown; not any, whose type tsc keeps. tsc's own declared type decides, a
// type parameter's as it is, so every instance of a generic function agrees (review round
// 1, R1-F3).
func (l *lowerer) narrowsByWrites(symbol *ast.Symbol) bool {
	declared := l.checker.GetTypeOfSymbol(symbol)
	return mayHoldUndefined(declared) && declared.Flags()&checker.TypeFlagsAny == 0
}

// writtenValues are the values written to a variable or parameter (symbol): its
// initializer or default (`const x: string | undefined = a`, `p: string | undefined =
// s.split(":")[1]`) and what assignments store (`x = list[3]`, `x ??= v`).
func (l *lowerer) writtenValues(symbol *ast.Symbol) []*ast.Node {
	values := l.assignedValues[symbol]
	declaration := symbol.ValueDeclaration
	if declaration.Kind == ast.KindVariableDeclaration || declaration.Kind == ast.KindParameter {
		if initializer := declaration.Initializer(); initializer != nil && declaration.Name().Kind == ast.KindIdentifier {
			values = append([]*ast.Node{initializer}, values...)
		}
	}
	return values
}

// missingSources reports whether value, written to a place whose type holds undefined,
// may be undefined though tsc types it without: an element read (D-015), or a conditional,
// `||`, `??`, `&&`, `,` or `satisfies` that may give one, whose operands the lowering reads
// in the whole's place (contextTakesUndefined; review rounds 3 and 4, R3-F1, R4-F2); and
// else the variables
// whose looseness would make it so, since a loose local goes there as the union it holds
// (D-220).
func (l *lowerer) missingSources(value *ast.Node) (bool, []*ast.Symbol) {
	value = ast.SkipParentheses(value)
	switch {
	case value.Kind == ast.KindConditionalExpression:
		conditional := value.AsConditionalExpression()
		return l.missingSourcesOfEither(conditional.WhenTrue, conditional.WhenFalse)
	case value.Kind == ast.KindSatisfiesExpression:
		return l.missingSources(value.Expression()) // `arr[3] satisfies string | undefined`, whose context is the satisfied type (review round 4, R4-F2)
	case value.Kind == ast.KindBinaryExpression:
		binary := value.AsBinaryExpression()
		switch binary.OperatorToken.Kind {
		case ast.KindBarBarToken, ast.KindQuestionQuestionToken, ast.KindCommaToken:
			return l.missingSources(binary.Right) // the left's undefined is never the result
		case ast.KindAmpersandAmpersandToken:
			return l.missingSourcesOfEither(binary.Left, binary.Right) // `lb && "s"` is lb's undefined
		}
	case l.isElementRead(value):
		return true, nil
	case value.Kind == ast.KindIdentifier:
		if symbol := l.target(value); symbol != nil && symbol.ValueDeclaration != nil {
			return false, []*ast.Symbol{symbol}
		}
	}
	return false, nil
}

// missingSourcesOfEither is missingSources of a value that is a or b.
func (l *lowerer) missingSourcesOfEither(a, b *ast.Node) (bool, []*ast.Symbol) {
	missing, sources := l.missingSources(a)
	otherMissing, others := l.missingSources(b)
	return missing || otherMissing, append(sources, others...)
}

// patternElementMayBeMissing reports whether element, an identifier that an array
// assignment pattern assigns without a default, may be assigned an element past the end of
// what the pattern destructures, which tsc types without undefined: the pattern's source
// isn't a tuple, or is a rest tuple (`[h, s] = r` for an `[number, ...string[]]`), as a
// binding element's name (readsElement); a nested pattern's, whatever its source (review
// round 2, R2-F1: a nested pattern with a default isn't destructuring its default).
func (l *lowerer) patternElementMayBeMissing(element *ast.Node) bool {
	pattern := element.Parent
	for pattern.Kind == ast.KindParenthesizedExpression {
		pattern = pattern.Parent
	}
	assignment := pattern.Parent
	for assignment.Kind == ast.KindParenthesizedExpression {
		pattern, assignment = assignment, assignment.Parent
	}
	if assignment.Kind != ast.KindBinaryExpression || assignment.AsBinaryExpression().Left != pattern || isPatternElement(assignment) {
		return true
	}
	t := l.checker.GetTypeAtLocation(assignment.AsBinaryExpression().Right)
	return !checker.IsTupleType(t) || isRestTuple(t)
}

// readsElement reports whether a variable declaration's initializer reads an element of
// an array, a string or a hash-map object, or it is a `for…of`'s variable over an array
// that may have holes (iteratesHoles), or a binding element of an array pattern binds,
// without a default, one of the items of what the pattern destructures other than a
// tuple: an array, string, map, set, iterator, iterable or dynamic value, which may end
// before the pattern does (D-172, H03), in a declaration, a parameter, a `for…of`
// binding, a nested pattern or a rest element's pattern (H11).
func (l *lowerer) readsElement(declaration *ast.Node) bool {
	switch declaration.Kind {
	case ast.KindVariableDeclaration:
		initializer := declaration.Initializer()
		if declaration.Name().Kind != ast.KindIdentifier {
			return false
		}
		if initializer == nil {
			return l.iteratesHoles(declaration)
		}
		return l.isElementRead(initializer)
	case ast.KindParameter:
		initializer := declaration.Initializer()
		return declaration.Name().Kind == ast.KindIdentifier && initializer != nil && l.isElementRead(initializer) // `p = s.split(":")[1]` (C306)
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

// iteratesHoles reports whether declaration is the variable of a `for…of` (not `for await`
// or `parallel for`) over an array, or its values(), whose slots may be holes: of
// strings, objects, functions or dynamic values (D-085), whose element type doesn't hold
// undefined already. JS's array iterator reads a hole as undefined (H11b). tsc's own
// types decide, with a type parameter's constraint, so every instance agrees (D-220).
func (l *lowerer) iteratesHoles(declaration *ast.Node) bool {
	list := declaration.Parent
	if list == nil || list.Kind != ast.KindVariableDeclarationList || list.Parent.Kind != ast.KindForOfStatement {
		return false
	}
	loop := list.Parent
	if loop.Flags&ast.NodeFlagsPtsParallel != 0 || loop.AsForInOrOfStatement().AwaitModifier != nil {
		return false
	}
	iterated := ast.SkipParentheses(loop.AsForInOrOfStatement().Expression)
	if l.isArrayIteration(iterated) {
		callee := ast.SkipParentheses(iterated.AsCallExpression().Expression)
		if callee.Name().Text() != "values" {
			return false // keys() are numbers; entries() are pairs
		}
		iterated = callee.Expression()
	}
	t := l.checker.GetTypeAtLocation(iterated)
	if !l.checker.IsArrayType(t) {
		return false
	}
	elem := l.checker.GetElementTypeOfArrayType(t)
	if elem.Flags()&checker.TypeFlagsTypeParameter != 0 {
		if constraint := l.checker.GetBaseConstraintOfType(elem); constraint != nil && constraint.Flags()&(checker.TypeFlagsAny|checker.TypeFlagsUnknown) == 0 {
			elem = constraint // a T extends number's elements are numbers in every instance
		} else {
			return true
		}
	}
	if mayHoldUndefined(elem) {
		return false // read as undefined already
	}
	slot := l.irTypeOrNone(elem, iterated)
	return slot == nil || !ir.IsNumeric(slot) && slot != ir.Bool
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
		if shorthandDefaultTarget(node) == nil {
			return l.literalFieldType(node.Parent)
		}
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
	if len(parameters) > 0 && ast.IsThisParameter(parameters[0]) {
		parameters = parameters[1:] // `bind<T>(this: T, thisArg: …)`: no argument goes to `this`
	}
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
	case ast.KindVariableDeclaration, ast.KindPropertyDeclaration, ast.KindPropertyAssignment, ast.KindParameter:
		return parent.Initializer() == node // a parameter's default too, which a parameter of a type with undefined holds as it is (C306)
	case ast.KindShorthandPropertyAssignment:
		return parent.Name() == node || parent.AsShorthandPropertyAssignment().ObjectAssignmentInitializer == node // `({ p = v } = o)` stores v as `p = v` does
	case ast.KindBinaryExpression:
		binary := parent.AsBinaryExpression()
		switch binary.OperatorToken.Kind {
		case ast.KindEqualsToken, ast.KindBarBarEqualsToken, ast.KindQuestionQuestionEqualsToken, ast.KindAmpersandAmpersandEqualsToken:
			return binary.Right == node // `v2 ||= v1` stores v1 as `=` does (review round 2, R2-F3)
		}
		return false
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
// since it may be undefined. It holds a value of its own type or undefined, so as its own
// type only undefined is tested for (H11b); narrowed to a class of its union, undefined is
// tested for and the class's narrowed read checks the rest (D-067); narrowed to anything
// else (a primitive, a tuple or an object type a type predicate may lie about), what it
// holds is checked as a dynamic value's is.
func (l *lowerer) looseRead(x ir.Expr, narrow ir.Type, node *ast.Node) ir.Expr {
	if narrow == ir.I64 || narrow == ir.U64 {
		return &ir.FromUnion{X: box(x), To: narrow} // exact past 2^53: it holds one of these integers or undefined, never a double (D-012)
	}
	u, ok := x.Type().(*ir.Union)
	if !ok {
		return l.checkedConversion(x, narrow, node)
	}
	own := l.withoutUndefined(u)
	defined := &ir.FromUnion{X: x, To: own, Defined: true}
	switch {
	case own == narrow:
		return defined
	case isCheckedClass(narrow) && isUnion(own) && isNarrowing(own.(*ir.Union), narrow):
		return l.narrowedUnionRead(defined, narrow, node)
	}
	return l.checkedConversion(x, narrow, node)
}
