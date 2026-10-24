package lower

import (
	"math"
	"slices"
	"strconv"
	"strings"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/internal/scanner"
	"github.com/microsoft/typescript-go/pts/ir"
)

// Yields inside larger expressions (D-111). A statement whose expression holds a yield
// that isn't first in it (statementYield) is lowered in two parts: statements that
// evaluate, in JS's order, each of its yields and each operand JS evaluates before one,
// keeping their values in locals (which live in the generator's frame, D-109); then the
// statement itself, whose lowering finds those values in place of the expressions
// (l.yielded, l.kept). An operand whose value can't change while the function is
// suspended (a literal, a function expression, `this`, a name nothing else assigns meanwhile,
// operators of those) isn't kept but evaluated where it is, after the yields (D-145); a
// property or element read is always kept, since a getter or other code could change it.
// A spread is kept as what it spreads then: a copy of the array, tuple, map or set it
// iterates, a dynamic value's items, or an object's property values (C139). A compound
// assignment whose right side yields keeps its target's value first. A yield in a
// branch of `?:` or in the right operand of `&&`, `||` or `??` makes the expression an if
// statement that assigns a local. A yield anywhere else (a loop's condition or update, a
// case label, a later declaration of a variable statement, a default value, an optional
// chain) is reported where it is lowered.

// hoistableExpression is the expression of a statement that runs once, before the rest
// of it, so that the yields in it can run before the statement: an expression statement's,
// the first variable's initializer, a return's or throw's value, an if's or switch's
// condition, or a for…of's or for…in's iterated value (labeled or not); nil for any other
// statement.
func hoistableExpression(node *ast.Node) *ast.Node {
	switch node.Kind {
	case ast.KindLabeledStatement:
		return hoistableExpression(node.AsLabeledStatement().Statement)
	case ast.KindExpressionStatement, ast.KindReturnStatement, ast.KindThrowStatement, ast.KindIfStatement,
		ast.KindSwitchStatement, ast.KindForOfStatement, ast.KindForInStatement:
		return node.Expression()
	case ast.KindVariableStatement:
		return node.AsVariableStatement().DeclarationList.AsVariableDeclarationList().Declarations.Nodes[0].Initializer()
	}
	return nil
}

// hasYield reports whether node holds a yield of the function being lowered that hasn't
// been lowered yet: one outside nested functions and classes.
func (l *lowerer) hasYield(node *ast.Node) bool {
	switch {
	case node == nil || ast.IsFunctionLike(node) || ast.IsClassLike(node):
		return false
	case isSuspension(node) && l.yielded[node] == nil:
		return true
	}
	return node.ForEachChild(l.hasYield)
}

// hoistingStatement lowers a statement whose expression holds yields that aren't first
// in it: the statements that hoist them (hoistYields), then the statement.
func (l *lowerer) hoistingStatement(node *ast.Node, expression *ast.Node) []ir.Stmt {
	l.hoisting[node] = true
	defer delete(l.hoisting, node)
	defer l.forgetHoisted(len(l.hoistedNodes))
	hoisted, ok := l.hoistYields(expression)
	if !ok {
		return nil
	}
	if node.Kind == ast.KindExpressionStatement && ast.SkipParentheses(expression).Kind == ast.KindYieldExpression {
		return hoisted // `yield (yield x);`: its value is unused
	}
	return append(hoisted, l.statement(node)...)
}

// hoistedExpression lowers expression, which holds yields, after the statements that hoist
// them (hoistYields): an arrow function's expression body (`async (f) => await read(f)`),
// which no statement holds, is lowered as a return's value is (D-172). value is nil if
// either failed, which was reported.
func (l *lowerer) hoistedExpression(expression *ast.Node) (hoisted []ir.Stmt, value ir.Expr) {
	defer l.forgetHoisted(len(l.hoistedNodes))
	hoisted, ok := l.hoistYields(expression)
	if !ok {
		return nil, nil
	}
	return hoisted, l.expression(expression)
}

// forgetHoisted drops what lowering the expressions hoisted since mark kept for them.
func (l *lowerer) forgetHoisted(mark int) {
	for _, hoisted := range l.hoistedNodes[mark:] {
		delete(l.yielded, hoisted)
		delete(l.kept, hoisted)
		delete(l.keptCurrent, hoisted)
		delete(l.keptSpreads, hoisted)
	}
	l.hoistedNodes = l.hoistedNodes[:mark]
}

// hoistYields lowers the yields in node, and the operands JS evaluates before each, to
// statements that keep their values for node's lowering; ok is false if it failed, which
// was reported. A yield it can't hoist is left for its lowering to report.
func (l *lowerer) hoistYields(node *ast.Node) (stmts []ir.Stmt, ok bool) {
	switch {
	case !l.hasYield(node):
		return nil, true
	case isSuspension(node):
		return l.hoistYield(node)
	case node.Kind == ast.KindConditionalExpression:
		return l.hoistConditional(node)
	case ast.IsLogicalOrCoalescingBinaryExpression(node) && l.hasYield(node.AsBinaryExpression().Right):
		return l.hoistShortCircuit(node)
	case ast.IsLogicalOrCoalescingAssignmentExpression(node) && l.hasYield(node.AsBinaryExpression().Right):
		return l.hoistLogicalAssignment(node)
	}
	operands := evaluationOrder(node)
	last := -1
	for i, operand := range operands {
		if l.hasYield(operand) && !isMemberCallee(operand) { // its yields are in its object and key, operands before it
			last = i
		}
	}
	for i, operand := range operands[:last+1] {
		if i == len(operands)-1 && l.readsTargetFirst(node) {
			kept, ok := l.keepCurrent(node)
			if !ok {
				return nil, false
			}
			stmts = append(stmts, kept...)
		}
		inner, ok := l.hoistYields(operand)
		if !ok {
			return nil, false
		}
		stmts = append(stmts, inner...)
		if i < last && node.Kind == ast.KindTemplateExpression && !l.isPrimitive(operand) {
			kept, ok := l.keepString(operand)
			if !ok {
				return nil, false
			}
			stmts = append(stmts, kept)
		} else if i < last && !l.isSettled(operand) {
			kept, ok := l.keep(operand)
			if !ok {
				return nil, false
			}
			stmts = append(stmts, kept...)
		}
	}
	return stmts, true
}

// hoistYield lowers a yield or an await (after those in its operand) to the statements
// that suspend, and keeps what it evaluates to once the function resumes in a local.
func (l *lowerer) hoistYield(node *ast.Node) ([]ir.Stmt, bool) {
	stmts, ok := l.hoistYields(node.Expression())
	if !ok {
		return nil, false
	}
	yielded, value := l.suspensionPoint(node)
	if yielded == nil {
		return nil, false
	}
	resumed := l.newLocal("resumed", value.Type())
	l.hoisted(node)
	l.yielded[node] = &ir.LocalRef{Local: resumed}
	return append(append(stmts, yielded...), &ir.LocalDecl{At: l.pos(node), Local: resumed, Init: value}), true
}

// hoistConditional lowers `c ? a : b` whose branches hold yields to an if statement that
// assigns the branch it takes to a local, the conditional's value.
func (l *lowerer) hoistConditional(node *ast.Node) ([]ir.Stmt, bool) {
	expr := node.AsConditionalExpression()
	stmts, ok := l.hoistYields(expr.Condition)
	if !ok {
		return nil, false
	}
	t := l.irType(l.typeAt(node), node)
	cond := l.condition(expr.Condition)
	if t == nil || cond == nil {
		return nil, false
	}
	chosen := l.newLocal("chosen", t)
	then, thenOK := l.hoistedBranch(expr.WhenTrue, chosen)
	otherwise, otherwiseOK := l.hoistedBranch(expr.WhenFalse, chosen)
	if !thenOK || !otherwiseOK {
		return nil, false
	}
	at := l.pos(node)
	l.keepAs(node, chosen)
	return append(stmts, &ir.LocalDecl{At: at, Local: chosen}, &ir.If{At: at, Cond: cond, Then: then, Else: otherwise}), true
}

// hoistShortCircuit lowers `a && b`, `a || b` or `a ?? b` whose right operand holds
// yields to a, kept in a local, and an if statement whose branches give the expression's
// value: b, evaluated where JS does, or a.
func (l *lowerer) hoistShortCircuit(node *ast.Node) ([]ir.Stmt, bool) {
	expr := node.AsBinaryExpression()
	stmts, ok := l.hoistYields(expr.Left)
	if !ok {
		return nil, false
	}
	keptLeft, ok := l.keep(expr.Left)
	if !ok {
		return nil, false
	}
	t := l.irType(l.typeAt(node), node)
	if t == nil {
		return nil, false
	}
	left := l.kept[expr.Left]
	var test ir.Expr
	switch expr.OperatorToken.Kind {
	case ast.KindAmpersandAmpersandToken:
		test = l.condition(expr.Left)
	case ast.KindBarBarToken:
		if test = l.condition(expr.Left); test != nil {
			test = &ir.Unary{Op: ir.Not, X: test}
		}
	default: // ??
		test = &ir.BoolConst{Value: false} // never null or undefined
		if isDynamic(left.Type()) {
			test = &ir.IsNullish{X: left}
		}
	}
	chosen := l.newLocal("chosen", t)
	then, ok := l.hoistedBranch(expr.Right, chosen)
	if test == nil || !ok {
		return nil, false
	}
	var result ir.Expr
	if isDynamic(left.Type()) && !isDynamic(t) {
		result = fromUnion(left, t) // the test found it to be of the result's type: `name || "?"` is a string
	} else if result = l.convert(left, t, expr.Left); result == nil {
		return nil, false
	}
	at := l.pos(node)
	otherwise := &ir.Block{At: at, Stmts: []ir.Stmt{&ir.ExprStmt{At: at, X: &ir.Assign{Local: chosen, Value: result}}}}
	l.keepAs(node, chosen)
	return append(append(stmts, keptLeft...), &ir.LocalDecl{At: at, Local: chosen}, &ir.If{At: at, Cond: test, Then: then, Else: otherwise}), true
}

// hoistLogicalAssignment lowers `x ??= v`, `x ||= v` or `x &&= v` whose right side holds
// yields: x's operands and its value, read once, kept in locals, then an if statement
// whose branch that stores evaluates v, its yields included, and assigns it as `x = v`
// does, without reading x again, so that nothing suspends when nothing is stored (C25).
// The expression's value is a local both branches assign.
func (l *lowerer) hoistLogicalAssignment(node *ast.Node) ([]ir.Stmt, bool) {
	expr := node.AsBinaryExpression()
	var stmts []ir.Stmt
	for _, operand := range referenceOperands(expr.Left) {
		inner, ok := l.hoistYields(operand)
		if !ok {
			return nil, false
		}
		stmts = append(stmts, inner...)
		if !l.isSettled(operand) {
			kept, ok := l.keep(operand)
			if !ok {
				return nil, false
			}
			stmts = append(stmts, kept...)
		}
	}
	// `??=` and `||=` store over a class's field the constructor hasn't set (D-141); `&&=`
	// would hand it on as its value, so its read keeps the check.
	read := l.placeValue(expr.Left, expr.OperatorToken.Kind != ast.KindAmpersandAmpersandEqualsToken)
	if read == nil {
		return nil, false
	}
	at := l.pos(node)
	current := l.newLocal("current", read.Type())
	stmts = append(stmts, &ir.LocalDecl{At: at, Local: current, Init: read})
	value := &ir.LocalRef{Local: current}
	var stores ir.Expr
	switch expr.OperatorToken.Kind {
	case ast.KindAmpersandAmpersandEqualsToken:
		stores = truthOf(value)
	case ast.KindBarBarEqualsToken:
		stores = &ir.Unary{Op: ir.Not, X: truthOf(value)}
	default:
		stores = storesNullish(value)
	}
	then, ok := l.hoistYields(expr.Right)
	if !ok {
		return nil, false
	}
	stored := l.assignment(node)
	if stored == nil {
		return nil, false
	}
	if isDiscarded(node) {
		l.hoisted(node)
		l.kept[node] = &ir.UndefinedConst{}
		then = append(then, &ir.ExprStmt{At: at, X: stored})
		return append(stmts, &ir.If{At: at, Cond: stores, Then: &ir.Block{At: at, Stmts: then}}), true
	}
	t := l.irType(l.typeAt(node), node)
	if t == nil {
		return nil, false
	}
	chosen := l.newLocal("chosen", t)
	assign := func(x ir.Expr) ir.Stmt {
		switch {
		case x.Type() == ir.Value && !isDynamic(t):
			x = l.checkedConversion(x, t, node) // a keyed element's property, read unconverted for the test
		case isDynamic(x.Type()) && !isDynamic(t):
			x = fromUnion(x, t) // the test found the value kept to be of the result's type
		default:
			x = l.convert(x, t, node)
		}
		if x == nil {
			return nil
		}
		return &ir.ExprStmt{At: at, X: &ir.Assign{Local: chosen, Value: x}}
	}
	assignStored, assignKept := assign(stored), assign(value)
	if assignStored == nil || assignKept == nil {
		return nil, false
	}
	then = append(then, assignStored)
	otherwise := &ir.Block{At: at, Stmts: []ir.Stmt{assignKept}}
	l.keepAs(node, chosen)
	return append(stmts, &ir.LocalDecl{At: at, Local: chosen}, &ir.If{At: at, Cond: stores, Then: &ir.Block{At: at, Stmts: then}, Else: otherwise}), true
}

// placeValue reads the value an assignment's target (node) holds, as JS reads it: a
// variable's as it is, whatever tsc narrowed it to; anything else's as an expression,
// undefined for an element or entry that isn't there. With unsetAsUndefined, a class's
// field the constructor hasn't set (D-141) reads as undefined where a read of it would
// throw: as NULL (FieldRef.Raw), or as a keyed element's dynamic value, unconverted.
func (l *lowerer) placeValue(node *ast.Node, unsetAsUndefined bool) ir.Expr {
	inner := ast.SkipParentheses(node)
	if inner.Kind == ast.KindIdentifier {
		if local, ok := l.locals[l.target(inner)]; ok {
			if local = l.ownLocal(inner, local); local == nil {
				return nil // its declaration failed, which was reported
			}
			return &ir.LocalRef{Local: local}
		}
	}
	if unsetAsUndefined && l.isKeyedElement(inner) {
		return l.keyedElementValue(inner)
	}
	read := l.valueExpression(node)
	if ref, ok := read.(*ir.FieldRef); ok && unsetAsUndefined && !ref.Field.Optional {
		ref.Raw = true
	}
	return read
}

// storesNullish is whether `??=` stores over a place's value, x: when it is null or
// undefined, as a class's field the constructor hasn't set yet is (D-141).
func storesNullish(x ir.Expr) ir.Expr {
	switch t := x.Type(); {
	case isDynamic(t):
		return &ir.IsNullish{X: x}
	case t == ir.Null || t == ir.Undefined:
		return &ir.BoolConst{Value: true}
	case isObject(t) || isFunction(t) || isMap(t):
		return &ir.Unary{Op: ir.Not, X: &ir.Truthy{X: x}}
	}
	return &ir.BoolConst{Value: false}
}

// hoistedBranch is a block that evaluates node, its yields included, into the local chosen.
func (l *lowerer) hoistedBranch(node *ast.Node, chosen *ir.Local) (*ir.Block, bool) {
	stmts, ok := l.hoistYields(node)
	if !ok {
		return nil, false
	}
	x := l.expression(node)
	if x == nil {
		return nil, false
	}
	if x = l.convert(x, chosen.Type, node); x == nil {
		return nil, false
	}
	at := l.pos(node)
	return &ir.Block{At: at, Stmts: append(stmts, &ir.ExprStmt{At: at, X: &ir.Assign{Local: chosen, Value: x}})}, true
}

// keep evaluates an operand into a local that its lowering then reads: what a spread
// spreads (keepSpread, keepObjectSpread), or the operand itself (a call that returns
// nothing is run, and is undefined). A C<any, …> root inside a callee or an operand is kept
// first, in a local of its own (keepAnyInstanceRoot).
func (l *lowerer) keep(node *ast.Node) ([]ir.Stmt, bool) {
	one := func(stmt ir.Stmt, ok bool) ([]ir.Stmt, bool) { return []ir.Stmt{stmt}, ok }
	switch node.Kind {
	case ast.KindSpreadElement:
		return one(l.keepSpread(node.Expression()))
	case ast.KindSpreadAssignment:
		return one(l.keepObjectSpread(node.Expression()))
	}
	var stmts []ir.Stmt
	if isMemberCallee(node) {
		stmts = l.keepAnyInstanceRoot(node.Expression())
	} else {
		stmts = l.keepAnyInstanceRoot(node)
	}
	if isMemberCallee(node) && l.isFoundAtRunTime(node) {
		kept, ok := l.keepCallee(node)
		return append(stmts, kept), ok
	}
	kept, ok := l.keepOperand(node)
	return append(stmts, kept), ok
}

// keepOperand is keep's for an operand kept as it is.
func (l *lowerer) keepOperand(node *ast.Node) (ir.Stmt, bool) {
	var x ir.Expr
	if parent := node.Parent; parent.Kind == ast.KindShorthandPropertyAssignment && parent.Name() == node {
		x = l.shorthandValue(parent) // `{ x }`'s x
	} else {
		x = l.expression(node)
	}
	if x == nil {
		return nil, false
	}
	at := l.pos(node)
	if x.Type() == ir.Void {
		l.hoisted(node)
		l.kept[node] = &ir.UndefinedConst{}
		return &ir.ExprStmt{At: at, X: x}, true
	}
	local := l.newLocal("kept", x.Type())
	l.keepAs(node, local)
	return &ir.LocalDecl{At: at, Local: local, Init: x}, true
}

// keepSpread keeps what a spread of a call's or `new`'s arguments or of an array literal
// (`...x`, node is x) spreads, before a yield after it: JS iterates x then, so what changes
// meanwhile doesn't show (C139). The array, tuple, typed array, map or set it iterates (the
// one that `a.keys()`, `a.values()` or `a.entries()` iterates, which their lowering reads
// again) is copied, and a dynamic value or an object iterated into an array of its items,
// which the spread's lowering then spreads as it would have spread x (spreadSnapshot).
func (l *lowerer) keepSpread(node *ast.Node) (ir.Stmt, bool) {
	if receiver := l.iterationReceiver(node); receiver != nil {
		node = receiver
	}
	x := l.expression(node)
	if x == nil {
		return nil, false
	}
	snapshot := l.spreadSnapshot(x, node)
	local := l.newLocal("kept", snapshot.Type())
	l.keepAs(node, local)
	return &ir.LocalDecl{At: l.pos(node), Local: local, Init: snapshot}, true
}

// iterationReceiver is the array, typed array, map or set whose items `a.keys()`,
// `a.values()` or `a.entries()` (node) spreads, which their lowering reads (arrayIteration,
// typedArrayIteration, mapIterableOf); nil for any other node.
func (l *lowerer) iterationReceiver(node *ast.Node) *ast.Node {
	inner := ast.SkipParentheses(node)
	if inner.Kind != ast.KindCallExpression {
		return nil
	}
	callee := ast.SkipParentheses(inner.AsCallExpression().Expression)
	switch {
	case l.isArrayIteration(inner), l.isTypedArrayIteration(inner):
		return callee.Expression()
	case l.isMapMethodCall(inner):
		if _, ok := mapMethodItems[callee.AsPropertyAccessExpression().Name().Text()]; ok {
			return callee.Expression()
		}
	}
	return nil
}

// spreadSnapshot is what a spread spreads after a yield in place of x, its value before
// the yield, so that it spreads what spreading x then would: a copy of an array, tuple,
// typed array, map or set, an array of a dynamic value's or an object's items (as
// argumentValues makes, D-078, D-111), or x itself (a string, a new array or tuple of a
// literal's elements, or what no spread takes, which the spread's lowering reports).
func (l *lowerer) spreadSnapshot(x ir.Expr, node *ast.Node) ir.Expr {
	switch t := x.Type(); {
	case isArrayLiteral(x):
		return x
	case isArray(t):
		return &ir.ArrayCall{Op: ir.ArraySlice, Args: []ir.Expr{x, &ir.NumberConst{Value: 0}, &ir.NumberConst{Value: math.Inf(1)}}}
	case isTuple(t):
		tuple, local := t.(*ir.Class), l.newLocal("tuple", t)
		values := make([]ir.Expr, len(tuple.Fields))
		for i, field := range tuple.Fields {
			values[i] = &ir.FieldRef{X: &ir.LocalRef{Local: local}, Field: field, Raw: field.Optional} // an element left out stays out (D-100)
		}
		return &ir.Bind{Local: local, Value: x, In: &ir.NewObject{Shape: tuple, Order: tuple.Order(tuple.Fields), Values: values}}
	case l.isTypedArray(t):
		return l.callExtern("pts_typed_array_slice", t, x, &ir.NumberConst{Value: 0}, &ir.NumberConst{Value: math.Inf(1)})
	case isMap(t) && !t.(*ir.MapType).Record:
		return &ir.NewMap{Map: t.(*ir.MapType), From: x}
	case isUnion(t) || t == ir.Value || isPlainObject(t):
		return &ir.CallBuiltin{Builtin: ir.ValueToArray, Args: []ir.Expr{box(x), &ir.StringConst{Value: scanner.GetTextOfNode(node)}}}
	}
	return x
}

// keepObjectSpread keeps what a spread in an object literal (`...o`, node is o) copies,
// before a yield after it: JS reads o's properties then, through their getters too, so
// what changes meanwhile doesn't show (C139). A hash-map object is copied; an object's
// properties are read into an object of their own (propertiesCopy), whose properties
// the literal copies by the names kept (copiedNames).
func (l *lowerer) keepObjectSpread(node *ast.Node) (ir.Stmt, bool) {
	x := l.expression(node)
	if x == nil {
		return nil, false
	}
	snapshot := x
	switch t := x.Type(); {
	case isRecord(t):
		snapshot = &ir.NewMap{Map: t.(*ir.MapType), From: x}
	case isPlainObject(t) && !isTuple(t):
		if copied, names := l.propertiesCopy(x, node); copied != nil {
			snapshot = copied
			l.keptSpreads[node] = keptSpread{names: names, of: t}
		}
	}
	local := l.newLocal("kept", snapshot.Type())
	l.keepAs(node, local)
	return &ir.LocalDecl{At: l.pos(node), Local: local, Init: snapshot}, true
}

// propertiesCopy is a new object holding the values of the properties that spreading x
// (an object, node) copies, read in the order the spread reads them, and their names; nil
// if a property is a method, which the spread's lowering reports. Its shape holds them
// as values, of the types read, optional where x's are (so a property x leaves out stays
// out), and its properties take x's order where x is of its shape (D-108).
func (l *lowerer) propertiesCopy(x ir.Expr, node *ast.Node) (ir.Expr, []string) {
	names, ok := l.spreadNames(x, l.typeAt(node), node)
	if !ok {
		return nil, nil
	}
	source := l.newLocal("spread", x.Type())
	reads := make([]ir.Expr, len(names))
	fields := make([]*ir.Field, len(names))
	for i, name := range names {
		if reads[i] = propertyOf(&ir.LocalRef{Local: source}, name); reads[i] == nil {
			return nil, nil
		}
		fields[i] = &ir.Field{Name: name, Type: reads[i].Type(), Optional: isOptionalRead(reads[i])}
	}
	shape := l.valuesShape(fields)
	order := make([]*ir.Field, len(names))
	for i, name := range names {
		order[i] = shape.Field(name)
		reads[i] = copiedRead(reads[i], order[i])
	}
	return &ir.Bind{Local: source, Value: x, In: &ir.NewObject{Shape: shape, Order: shape.Order(order), Values: reads, OrderOf: &ir.LocalRef{Local: source}}}, names
}

// valuesShape is the shape of the objects holding fields (of no class yet) as values: the
// one the program's objects of the same properties have, if any, so that a copy of one
// takes its order (D-108).
func (l *lowerer) valuesShape(fields []*ir.Field) *ir.Class {
	fields = slices.SortedFunc(slices.Values(fields), func(a, b *ir.Field) int { return strings.Compare(a.Name, b.Name) })
	key := membersKey(fields, nil)
	if existing, ok := l.shapesByKey[key]; ok {
		return existing
	}
	shape := &ir.Class{Shape: true, Name: "object" + strconv.Itoa(len(l.shapeList)+1), Fields: fields}
	for _, field := range fields {
		field.Class = shape
	}
	l.shapesByKey[key] = shape
	l.shapeList = append(l.shapeList, shape)
	return shape
}

// keptSpread is what keepObjectSpread read of the object an object literal's spread copies.
type keptSpread struct {
	names []string // the properties read, in the order the spread copies them
	of    ir.Type  // the object's type, which diagnostics name rather than the copy's shape
}

// copiedNames is spreadNames for the object (source) that a spread in an object literal
// copies (`...o`, node is o), and the type diagnostics name: the names of the properties
// keepObjectSpread read before a yield after it and the object's type, if it read them.
func (l *lowerer) copiedNames(source ir.Expr, node *ast.Node, at *ast.Node) ([]string, ir.Type, bool) {
	if kept, ok := l.keptSpreads[node]; ok {
		return kept.names, kept.of, true
	}
	names, ok := l.spreadNames(source, l.typeAt(node), at)
	return names, source.Type(), ok
}

// keepCallee keeps what a call through `any` (callee, `o.m` or `o[k]`) calls, found before
// its arguments are evaluated (DynamicMethodOf), for dynamicMethodCall.
func (l *lowerer) keepCallee(callee *ast.Node) (ir.Stmt, bool) {
	object := l.expression(callee.Expression())
	key := l.dynamicKey(callee)
	if object == nil || key == nil {
		return nil, false
	}
	l.wantDynamicMember(callee)
	local := l.newLocal("callee", ir.Value)
	l.keepAs(callee, local)
	found := &ir.CallBuiltin{Builtin: ir.DynamicMethodOf, Args: []ir.Expr{box(object), key}}
	return &ir.LocalDecl{At: l.pos(callee), Local: local, Init: found}, true
}

// keepAs makes node's lowering read local.
func (l *lowerer) keepAs(node *ast.Node, local *ir.Local) {
	l.hoisted(node)
	l.kept[node] = &ir.LocalRef{Local: local}
}

// hoisted records a node whose value hoistYields keeps, to forget once its statement is lowered.
func (l *lowerer) hoisted(node *ast.Node) {
	l.hoistedNodes = append(l.hoistedNodes, node)
}

// isSettled reports whether an operand evaluated after the yields that follow it gives
// what it gives before them, and runs no code of the program's out of order: its value is
// kept, or nothing can change it while the function is suspended (D-145).
func (l *lowerer) isSettled(node *ast.Node) bool {
	if l.kept[node] != nil {
		return true
	}
	if isMemberCallee(node) {
		return l.isSettledCallee(node)
	}
	switch node.Kind {
	case ast.KindYieldExpression, ast.KindAwaitExpression:
		return l.yielded[node] != nil
	case ast.KindIdentifier:
		return l.isUnchangedWhileSuspended(node)
	case ast.KindThisKeyword, ast.KindSuperKeyword, ast.KindNumericLiteral, ast.KindBigIntLiteral, ast.KindStringLiteral,
		ast.KindNoSubstitutionTemplateLiteral, ast.KindTrueKeyword, ast.KindFalseKeyword, ast.KindNullKeyword,
		ast.KindRegularExpressionLiteral, ast.KindArrowFunction, ast.KindFunctionExpression:
		return true
	case ast.KindParenthesizedExpression, ast.KindAsExpression, ast.KindSatisfiesExpression, ast.KindNonNullExpression,
		ast.KindTypeAssertionExpression, ast.KindTypeOfExpression, ast.KindVoidExpression:
		return l.isSettled(node.Expression())
	case ast.KindSpreadElement: // it reads the items it spreads then: a string's, or a new array's of a literal
		return l.isSettled(node.Expression()) && (l.isPrimitive(node.Expression()) || ast.SkipParentheses(node.Expression()).Kind == ast.KindArrayLiteralExpression)
	case ast.KindSpreadAssignment:
		return false // it reads the object's properties then, which could change meanwhile, or run getters
	case ast.KindPropertyAccessExpression, ast.KindElementAccessExpression:
		return false // a getter could run, or other code change the property meanwhile
	case ast.KindPrefixUnaryExpression:
		expr := node.AsPrefixUnaryExpression()
		switch expr.Operator {
		case ast.KindPlusPlusToken, ast.KindMinusMinusToken:
			return false
		case ast.KindExclamationToken:
			return l.isSettled(expr.Operand)
		}
		return l.isSettled(expr.Operand) && l.isPrimitive(expr.Operand) // ToNumber of an object would call its valueOf now
	case ast.KindPostfixUnaryExpression:
		return false // `i++` changes i, so evaluating it again changes it again (C08)
	case ast.KindBinaryExpression:
		expr := node.AsBinaryExpression()
		switch op := expr.OperatorToken.Kind; {
		case ast.IsAssignmentOperator(op), op == ast.KindInKeyword: // `in` reads what keys the object has now
			return false
		case op == ast.KindEqualsEqualsEqualsToken, op == ast.KindExclamationEqualsEqualsToken, op == ast.KindInstanceOfKeyword,
			op == ast.KindCommaToken, ast.IsLogicalOrCoalescingBinaryOperator(op):
			return l.isSettled(expr.Left) && l.isSettled(expr.Right)
		}
		// Converting an object operand would call its valueOf or toString now.
		return l.isSettled(expr.Left) && l.isSettled(expr.Right) && l.isPrimitive(expr.Left) && l.isPrimitive(expr.Right)
	case ast.KindTemplateExpression:
		for _, span := range node.AsTemplateExpression().TemplateSpans.Nodes {
			if !l.isSettled(span.Expression()) || !l.isPrimitive(span.Expression()) { // ToString of an object would call its toString now
				return false
			}
		}
		return true
	}
	operands := evaluationOrder(node)
	if operands == nil || node.Kind == ast.KindCallExpression || node.Kind == ast.KindNewExpression {
		return false
	}
	for _, operand := range operands {
		if !l.isSettled(operand) {
			return false
		}
	}
	return true // an array or object literal of settled operands
}

// isUnchangedWhileSuspended reports whether a name's value can't change while the function
// reading it is suspended: a constant, a function, class, enum or import, or a variable of
// the function itself that no other function assigns (only the function's own code, which
// isn't running, could), or a variable of an enclosing scope that nothing assigns.
func (l *lowerer) isUnchangedWhileSuspended(name *ast.Node) bool {
	symbol := l.followAliases(l.valueSymbolAt(name)) // `{ x }`'s x, not the property
	if symbol == nil || symbol.ValueDeclaration == nil {
		return name.Text() == "undefined"
	}
	declaration := symbol.ValueDeclaration
	switch declaration.Kind {
	case ast.KindVariableDeclaration, ast.KindParameter, ast.KindBindingElement:
		if declaration.Kind == ast.KindVariableDeclaration && ast.IsVarConst(declaration) {
			return true
		}
		if functionOf(declaration) == functionOf(name) {
			return !l.assignedOutside[symbol]
		}
		return !l.assignedAnywhere[symbol]
	}
	return true
}

// isPrimitive reports whether node's type is primitive, or a union of primitives, so
// converting its value runs no code.
func (l *lowerer) isPrimitive(node *ast.Node) bool {
	t := l.typeAt(node)
	if t.Flags()&checker.TypeFlagsUnion != 0 {
		return allOf(t.Types(), checker.TypeFlagsPrimitive)
	}
	return t.Flags()&checker.TypeFlagsPrimitive != 0
}

// isMemberCallee reports whether node is the callee of a call, in parentheses or not, that
// is a property or element access (`o.m` in `o.m(x)`).
func isMemberCallee(node *ast.Node) bool {
	if !ast.IsAccessExpression(node) {
		return false
	}
	parent := node.Parent
	for parent != nil && parent.Kind == ast.KindParenthesizedExpression {
		parent = parent.Parent
	}
	return parent != nil && parent.Kind == ast.KindCallExpression && ast.SkipParentheses(parent.AsCallExpression().Expression) == node
}

// isSettledCallee reports whether what a call's callee, a property or element access,
// names can't change while the function is suspended once its object is evaluated: a
// method (none can be assigned), a function, class or namespace, or a library's member. A
// property's or element's value, what a getter gives and what is found through `any` can;
// the call reads them before a yield in its arguments, as JS does (keep, H08).
func (l *lowerer) isSettledCallee(callee *ast.Node) bool {
	switch {
	case l.isFoundAtRunTime(callee):
		return false
	case l.isUnionReceiver(callee):
		return true // unionMethodCall calls only methods, and reports any other member
	}
	var symbol *ast.Symbol
	if callee.Kind == ast.KindPropertyAccessExpression {
		symbol = l.memberSymbol(callee)
	} else if key := callee.AsElementAccessExpression().ArgumentExpression; ast.IsStringOrNumericLiteralLike(key) {
		symbol = l.checker.GetSymbolAtLocation(key) // the member `o["m"]` names
	} else if l.namesMethods(callee) {
		return true // `o[key](yield x)` for a key whose type names methods: the key is kept, and the method found after (D-194)
	}
	switch symbol = l.followAliases(symbol); { // `ns.f` re-exported from another module
	case symbol == nil:
		return false
	case l.isDeclaredInDefaultLib(symbol) || l.isDeclaredInPtsLib(symbol) || isMethodSymbol(symbol):
		return true
	}
	return symbol.Flags&(ast.SymbolFlagsFunction|ast.SymbolFlagsClass|ast.SymbolFlagsEnum|ast.SymbolFlagsValueModule) != 0
}

// namesMethods reports whether callee, an element access, has a key whose type is string
// literals each naming a method of its object's type, which no code can assign.
func (l *lowerer) namesMethods(callee *ast.Node) bool {
	t := l.checker.GetNonNullableType(l.typeAt(callee.Expression()))
	names := stringLiterals(l.typeAt(callee.AsElementAccessExpression().ArgumentExpression))
	if names == nil || t.Flags()&(checker.TypeFlagsAny|checker.TypeFlagsUnknown) != 0 {
		return false
	}
	for _, name := range names {
		if property := l.checker.GetPropertyOfType(t, name); property == nil || !isMethodSymbol(property) {
			return false
		}
	}
	return true
}

// isFoundAtRunTime reports whether a callee, a property or element access, is a member of
// a dynamic value (or one that stays dynamic though tsc narrowed it, or of a union of
// objects and other values) that is not a library's: the call finds it at run time
// (dynamicCall), and so does keepCallee, before the arguments.
func (l *lowerer) isFoundAtRunTime(callee *ast.Node) bool {
	receiver := callee.Expression()
	if !l.isAny(receiver) && !l.isNarrowedDynamic(receiver) && !l.isMixedUnionReceiver(callee) {
		return false
	}
	if callee.Kind == ast.KindPropertyAccessExpression {
		symbol := l.memberSymbol(callee)
		return !l.isDeclaredInDefaultLib(symbol) && !l.isDeclaredInPtsLib(symbol)
	}
	return true
}

// readsTargetFirst reports whether node is a compound assignment that reads its target
// before evaluating its right side, which holds a yield: `x += yield v` adds to x's value
// from before the yield. The logical assignments read it to decide whether to evaluate
// the right side, which their lowering does in order.
func (l *lowerer) readsTargetFirst(node *ast.Node) bool {
	if node.Kind != ast.KindBinaryExpression {
		return false
	}
	expr := node.AsBinaryExpression()
	switch expr.OperatorToken.Kind {
	case ast.KindEqualsToken, ast.KindAmpersandAmpersandEqualsToken, ast.KindBarBarEqualsToken, ast.KindQuestionQuestionEqualsToken:
		return false
	}
	return ast.IsAssignmentOperator(expr.OperatorToken.Kind) && l.hasYield(expr.Right)
}

// keepCurrent keeps the value of a compound assignment's target before its right side's
// yields, for the assignment's lowering (l.keptCurrent), which then stores without reading
// it again: a name's, a field's, an element's, a getter's or a property's through `any`.
func (l *lowerer) keepCurrent(node *ast.Node) ([]ir.Stmt, bool) {
	target := ast.SkipParentheses(node.AsBinaryExpression().Left)
	switch target.Kind {
	case ast.KindIdentifier:
		if l.isUnchangedWhileSuspended(target) {
			return nil, true
		}
	case ast.KindPropertyAccessExpression, ast.KindElementAccessExpression:
	default:
		return nil, true
	}
	x := l.expression(target)
	if x == nil {
		return nil, false
	}
	local := l.newLocal("current", x.Type())
	l.hoisted(node)
	l.keptCurrent[node] = &ir.LocalRef{Local: local}
	return []ir.Stmt{&ir.LocalDecl{At: l.pos(target), Local: local, Init: x}}, true
}

// keepString keeps a template's substitution as its string before a later yield: JS
// converts each substitution when it reaches it, so an object's toString runs before the
// yield (D-145).
func (l *lowerer) keepString(node *ast.Node) (ir.Stmt, bool) {
	x := l.valueExpression(node)
	if x == nil {
		return nil, false
	}
	if x = l.toString(x, node); x == nil {
		return nil, false
	}
	local := l.newLocal("kept", ir.String)
	l.keepAs(node, local)
	return &ir.LocalDecl{At: l.pos(node), Local: local, Init: x}, true
}

// evaluationOrder is the operands of an expression in the order JS evaluates them, where
// the yields in them can be hoisted; nil for an expression whose yields can't be.
func evaluationOrder(node *ast.Node) []*ast.Node {
	switch node.Kind {
	case ast.KindParenthesizedExpression, ast.KindAsExpression, ast.KindSatisfiesExpression, ast.KindNonNullExpression,
		ast.KindTypeAssertionExpression, ast.KindTypeOfExpression, ast.KindVoidExpression,
		ast.KindDeleteExpression, ast.KindSpreadElement, ast.KindSpreadAssignment:
		return []*ast.Node{node.Expression()}
	case ast.KindPrefixUnaryExpression:
		return []*ast.Node{node.AsPrefixUnaryExpression().Operand}
	case ast.KindPostfixUnaryExpression:
		return []*ast.Node{node.AsPostfixUnaryExpression().Operand}
	case ast.KindBinaryExpression:
		expr := node.AsBinaryExpression()
		if ast.IsAssignmentOperator(expr.OperatorToken.Kind) {
			return append(referenceOperands(expr.Left), expr.Right)
		}
		return []*ast.Node{expr.Left, expr.Right}
	case ast.KindCallExpression:
		if ast.IsOptionalChain(node) {
			return nil
		}
		call := node.AsCallExpression()
		switch callee := ast.SkipParentheses(call.Expression); {
		case callee.Kind == ast.KindIdentifier:
			return append([]*ast.Node{callee}, call.Arguments.Nodes...) // the function a name holds is read before the arguments
		case ast.IsAccessExpression(callee):
			return append(append(referenceOperands(callee), callee), call.Arguments.Nodes...) // its object (and key), then the function found there
		}
		return append(referenceOperands(call.Expression), call.Arguments.Nodes...)
	case ast.KindNewExpression:
		if args := node.AsNewExpression().Arguments; args != nil {
			return args.Nodes
		}
	case ast.KindPropertyAccessExpression:
		if !ast.IsOptionalChain(node) {
			return []*ast.Node{node.Expression()}
		}
	case ast.KindElementAccessExpression:
		if !ast.IsOptionalChain(node) {
			return []*ast.Node{node.Expression(), node.AsElementAccessExpression().ArgumentExpression}
		}
	case ast.KindArrayLiteralExpression:
		return node.AsArrayLiteralExpression().Elements.Nodes
	case ast.KindObjectLiteralExpression:
		var values []*ast.Node
		for _, property := range node.AsObjectLiteralExpression().Properties.Nodes {
			switch property.Kind {
			case ast.KindPropertyAssignment:
				if name := property.Name(); name.Kind == ast.KindComputedPropertyName && !isIteratorName(name) {
					values = append(values, name.Expression()) // a computed key, before its value
				}
				values = append(values, property.Initializer())
			case ast.KindShorthandPropertyAssignment:
				values = append(values, property.Name()) // `{ x }` reads x
			case ast.KindSpreadAssignment:
				values = append(values, property) // the properties it copies, read then (keepObjectSpread)
			}
		}
		return values
	case ast.KindTemplateExpression:
		var values []*ast.Node
		for _, span := range node.AsTemplateExpression().TemplateSpans.Nodes {
			values = append(values, span.Expression())
		}
		return values
	}
	return nil
}

// referenceOperands are the operands of a callee or an assignment's target that JS
// evaluates before the arguments or the right side: a property's object, an element's
// object and index, or a callee of another kind itself (`make()(x)`).
func referenceOperands(node *ast.Node) []*ast.Node {
	switch node = ast.SkipParentheses(node); node.Kind {
	case ast.KindPropertyAccessExpression:
		return []*ast.Node{node.Expression()}
	case ast.KindElementAccessExpression:
		return []*ast.Node{node.Expression(), node.AsElementAccessExpression().ArgumentExpression}
	case ast.KindIdentifier, ast.KindThisKeyword, ast.KindSuperKeyword, ast.KindArrayLiteralExpression, ast.KindObjectLiteralExpression:
		return nil // a binding, or a destructuring pattern, assigned after the right side
	}
	return []*ast.Node{node}
}
