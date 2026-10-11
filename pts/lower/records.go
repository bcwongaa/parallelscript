package lower

import (
	"fmt"
	"slices"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/pts/ir"
)

// recordType maps an object type with string or number index signatures
// (`Record<string, T>`, `{ [key: string]: T }`, `Record<number, T>`) to its IR type, a
// hash-map object (D-059), or reports why it can't (at node) and returns nil; ok is false
// for any other type.
func (l *lowerer) recordType(t *checker.Type, node *ast.Node) (mt *ir.MapType, ok bool) {
	if !l.hasIndexSignatures(t) {
		return nil, false
	}
	if !l.isHashMapType(t) {
		l.notSupportedYet(node, fmt.Sprintf("object types with an index signature and methods, optional properties or properties of other types (%s) are", l.checker.TypeToString(t)))
		return nil, true
	}
	if l.mapsInProgress[t] {
		l.notSupportedYet(node, fmt.Sprintf("recursive hash-map object types (%s) are", l.checker.TypeToString(t)))
		return nil, true
	}
	l.mapsInProgress[t] = true
	defer delete(l.mapsInProgress, t)
	var value ir.Type
	for _, info := range l.checker.GetIndexInfosOfType(t) {
		if key := info.KeyType(); key.Flags()&(checker.TypeFlagsString|checker.TypeFlagsNumber) == 0 {
			l.notSupportedYet(node, fmt.Sprintf("index signatures with keys of type %s are", l.checker.TypeToString(key)))
			return nil, true
		}
		v := l.irType(info.ValueType(), node)
		switch {
		case v == nil:
			return nil, true
		case value != nil && v != value:
			l.notSupportedYet(node, fmt.Sprintf("string and number index signatures of different types (%s) are", l.checker.TypeToString(t)))
			return nil, true
		}
		value = v
	}
	return l.recordOf(value), true
}

// hasIndexSignatures reports whether t is an object type with index signatures, other
// than a class, an array, a tuple or a function type.
func (l *lowerer) hasIndexSignatures(t *checker.Type) bool {
	if t.Flags()&checker.TypeFlagsObject == 0 || t.ObjectFlags()&checker.ObjectFlagsClass != 0 || l.checker.IsArrayType(t) || checker.IsTupleType(t) {
		return false
	}
	return len(l.checker.GetIndexInfosOfType(t)) > 0 && len(l.checker.GetSignaturesOfType(t, checker.SignatureKindCall)) == 0 &&
		len(l.checker.GetSignaturesOfType(t, checker.SignatureKindConstruct)) == 0
}

// isHashMapType reports whether t is an object type with index signatures whose
// properties, if it has any, are required properties of an index signature's value type
// (`{ [key: string]: number; total: number }`, or the type of `{ [k]: 1, other: 2 }`):
// entries that its values always have.
func (l *lowerer) isHashMapType(t *checker.Type) bool {
	if !l.hasIndexSignatures(t) {
		return false
	}
	infos := l.checker.GetIndexInfosOfType(t)
	for _, property := range l.checker.GetPropertiesOfType(t) {
		if property.Flags&ast.SymbolFlagsProperty == 0 || property.Flags&ast.SymbolFlagsOptional != 0 {
			return false
		}
		propertyType := l.checker.GetTypeOfSymbol(property)
		if !slices.ContainsFunc(infos, func(info *checker.IndexInfo) bool { return info.ValueType() == propertyType }) {
			return false
		}
	}
	return true
}

// recordOf is the one hash-map object type with values of type value.
func (l *lowerer) recordOf(value ir.Type) *ir.MapType {
	for _, mt := range l.mapTypes {
		if mt.Record && mt.Value == value {
			return mt
		}
	}
	mt := &ir.MapType{Key: ir.String, Value: value, Record: true}
	l.mapTypes = append(l.mapTypes, mt)
	return mt
}

func isRecord(t ir.Type) bool {
	mt, ok := t.(*ir.MapType)
	return ok && mt.Record
}

// isRecordExpression reports whether tsc types node as a hash-map object.
func (l *lowerer) isRecordExpression(node *ast.Node) bool {
	return l.isHashMapType(l.typeAt(node))
}

// isRecordElement reports whether node reads or writes an entry of a hash-map object:
// `o[k]`, or `o.k` unless k names a method every object has (`o.hasOwnProperty`).
func (l *lowerer) isRecordElement(node *ast.Node) bool {
	switch node.Kind {
	case ast.KindElementAccessExpression:
		return l.isRecordExpression(node.Expression())
	case ast.KindPropertyAccessExpression:
		return l.isRecordExpression(node.Expression()) && !l.isObjectPrototypeMember(node)
	}
	return false
}

// isObjectPrototypeMember reports whether the property access node names a member of
// Object.prototype, which tsc finds on every object type.
func (l *lowerer) isObjectPrototypeMember(node *ast.Node) bool {
	symbol := l.checker.GetSymbolAtLocation(node.AsPropertyAccessExpression().Name())
	return symbol != nil && l.isDeclaredInDefaultLib(symbol)
}

// recordElement lowers a read of o[k] or o.k for a hash-map object o: MapGet where any
// value can go, which is undefined if o has no key k, and MapGetChecked elsewhere, which
// throws (D-059).
func (l *lowerer) recordElement(node *ast.Node, op ir.MapOp) ir.Expr {
	record, key := l.recordElementOperands(node)
	if record == nil {
		return nil
	}
	return &ir.MapCall{Op: op, Args: []ir.Expr{record, key}, Map: record.Type().(*ir.MapType)}
}

// recordElementOperands lowers the object and the key of o[k] or o.k, or reports the
// access and returns nils.
func (l *lowerer) recordElementOperands(node *ast.Node) (ir.Expr, ir.Expr) {
	if ast.IsOptionalChain(node) {
		l.notSupportedYet(node, "optional chaining `?.` is")
		return nil, nil
	}
	record := l.expression(node.Expression())
	var key ir.Expr
	if node.Kind == ast.KindPropertyAccessExpression {
		key = &ir.StringConst{Value: node.AsPropertyAccessExpression().Name().Text()}
	} else {
		key = l.recordKey(node.AsElementAccessExpression().ArgumentExpression)
	}
	if record == nil || key == nil {
		return nil, nil
	}
	return record, key
}

// recordKey lowers a key of a hash-map object: a string, or a number, which JS converts
// to its string.
func (l *lowerer) recordKey(node *ast.Node) ir.Expr {
	key := l.expression(node)
	switch {
	case key == nil:
		return nil
	case ir.IsNumeric(key.Type()):
		return &ir.ToString{X: key}
	case key.Type() != ir.String:
		l.notSupportedYet(node, fmt.Sprintf("a key of type %s is", key.Type()))
		return nil
	}
	return key
}

// recordElementFallback lowers `o[k] ?? fallback` (nullish) and `o[k] || fallback` for a
// hash-map object o: o's value for k if it has one (that is truthy, for ||; not null or
// undefined, for ??), and otherwise fallback, which is evaluated only then. JS's
// undefined for a missing key makes the fallback apply exactly then. The result has the
// type tsc gives node. nil if it fails (reported).
func (l *lowerer) recordElementFallback(node *ast.Node, element *ast.Node, fallback *ast.Node, nullish bool) ir.Expr {
	record, key := l.recordElementOperands(element)
	result := l.irType(l.typeAt(node), node)
	if record == nil || result == nil {
		return nil
	}
	return l.recordFallback(record, key, nullish, result, node, func(t ir.Type) ir.Expr {
		value := l.expression(fallback)
		if value == nil {
			return nil
		}
		return l.convert(value, t, fallback)
	})
}

// recordFallback is, as type result, the value of the key in record if it has one (that
// is truthy, unless nullish; not null or undefined, if nullish), else what fallback makes
// of type result (at node, for errors).
func (l *lowerer) recordFallback(record ir.Expr, key ir.Expr, nullish bool, result ir.Type, node *ast.Node, fallback func(ir.Type) ir.Expr) ir.Expr {
	mt := record.Type().(*ir.MapType)
	value := l.newLocal("value", mt.Value)
	ref := &ir.LocalRef{Local: value}
	var found ir.Expr = &ir.MapCall{Op: ir.MapGetInto, Args: []ir.Expr{record, key, ref}, Map: mt}
	switch t := mt.Value; {
	case !nullish && (t == ir.Bool || ir.IsNumeric(t) || t == ir.String || isUnion(t)):
		found = &ir.Logical{Op: ir.And, X: found, Y: truthOf(ref)} // objects and functions are never falsy
	case nullish && isUnion(t):
		found = &ir.Logical{Op: ir.And, X: found, Y: &ir.Unary{Op: ir.Not, X: &ir.IsNullish{X: ref}}}
	}
	var then ir.Expr = ref
	if isUnion(mt.Value) {
		then = fromUnion(ref, result) // a value the test let through
	} else if then = l.convert(ref, result, node); then == nil {
		return nil
	}
	otherwise := fallback(result)
	if otherwise == nil {
		return nil
	}
	return &ir.Bind{Local: value, Value: &ir.LeftOut{T: mt.Value}, In: &ir.Conditional{Cond: found, Then: then, Else: otherwise}}
}

// recordElementOrSet lowers `o[k] ??= value` (nullish) and `o[k] ||= value` for an entry
// of a hash-map object: o's value for k if it has one (that is truthy, for ||=), and
// otherwise value, which is evaluated and set only then.
func (l *lowerer) recordElementOrSet(target place, valueNode *ast.Node, nullish bool) ir.Expr {
	return l.withObject(target, func(target place) ir.Expr {
		t := target.valueType()
		return l.recordFallback(target.object, target.key, nullish, t, valueNode, func(ir.Type) ir.Expr {
			value := l.expression(valueNode)
			if value == nil {
				return nil
			}
			if value = l.convert(value, t, valueNode); value == nil {
				return nil
			}
			return target.store(value, false)
		})
	})
}

// recordElementComparedWithUndefined recognises o[k] compared with undefined (either way
// round, with ===, !==, == or !=) for a hash-map object o and returns the element.
func (l *lowerer) recordElementComparedWithUndefined(expr *ast.BinaryExpression) (*ast.Node, bool) {
	element, other := ast.SkipParentheses(expr.Left), expr.Right
	if !l.isRecordElement(element) {
		element, other = ast.SkipParentheses(expr.Right), expr.Left
	}
	return element, l.isRecordElement(element) && isUndefined(ast.SkipParentheses(other))
}

// recordMayHoldNullish reports whether the values of the hash-map object that element
// (o[k] or o.k) reads may be null or undefined themselves.
func (l *lowerer) recordMayHoldNullish(element *ast.Node) bool {
	t := l.checker.GetIndexInfosOfType(l.typeAt(element.Expression()))[0].ValueType()
	return t.Flags()&(checker.TypeFlagsNull|checker.TypeFlagsUndefined|checker.TypeFlagsUnion) != 0
}

// recordHas lowers `k in o` (and `o[k] !== undefined`, negated for ===) for a hash-map
// object o: whether o has the key k.
func (l *lowerer) recordHas(record ir.Expr, key ir.Expr, negated bool) ir.Expr {
	if record == nil || key == nil {
		return nil
	}
	var has ir.Expr = &ir.MapCall{Op: ir.MapHas, Args: []ir.Expr{record, key}, Map: record.Type().(*ir.MapType)}
	if negated {
		has = &ir.Unary{Op: ir.Not, X: has}
	}
	return has
}

// inRecord lowers `k in o` for a hash-map object o, evaluating k first, as JS does.
func (l *lowerer) inRecord(expr *ast.BinaryExpression) ir.Expr {
	key, record := l.recordKey(expr.Left), l.expression(expr.Right)
	if key == nil || record == nil {
		return nil
	}
	if isPure(key) {
		return l.recordHas(record, key, false)
	}
	keyLocal := l.newLocal("key", ir.String)
	return &ir.Bind{Local: keyLocal, Value: key, In: l.recordHas(record, &ir.LocalRef{Local: keyLocal}, false)}
}

// deleteExpression lowers `delete o[k]` (or `delete o.k`) for a hash-map object o, whose
// value is true as in JS. Other objects have fixed layouts (D-L10).
func (l *lowerer) deleteExpression(node *ast.Node) ir.Expr {
	operand := ast.SkipParentheses(node.Expression())
	if !l.isRecordElement(operand) {
		l.unsupported(node, "`delete` works only on hash-map objects (`Record<string, T>` and index signatures): other objects have fixed layouts and arrays have no holes")
		return nil
	}
	record, key := l.recordElementOperands(operand)
	if record == nil {
		return nil
	}
	return &ir.Sequence{X: &ir.MapCall{Op: ir.MapDelete, Args: []ir.Expr{record, key}, Map: record.Type().(*ir.MapType)}, Y: &ir.BoolConst{Value: true}}
}

// recordLiteral lowers an object literal of a hash-map object type mt: a new hash-map
// object with each property set in the literal's order, the values evaluated in it too.
// A spread copies another hash-map object's entries in its key order, or an object's
// properties in the order D-058 gives them. Computed names are keys like o[k]'s.
func (l *lowerer) recordLiteral(node *ast.Node, mt *ir.MapType) ir.Expr {
	var record ir.Expr = &ir.NewMap{Map: mt}
	for _, property := range node.AsObjectLiteralExpression().Properties.Nodes {
		if record = l.recordLiteralProperty(record, property); record == nil {
			return nil
		}
	}
	return record
}

// recordLiteralProperty sets the property of a hash-map object literal in record, the
// literal so far, and returns the result.
func (l *lowerer) recordLiteralProperty(record ir.Expr, property *ast.Node) ir.Expr {
	mt := record.Type().(*ir.MapType)
	set := func(key ir.Expr, value ir.Expr, at *ast.Node) ir.Expr {
		if key == nil || value == nil {
			return nil
		}
		if value = l.convert(value, mt.Value, at); value == nil {
			return nil
		}
		return &ir.MapCall{Op: ir.MapSet, Args: []ir.Expr{record, key, value}, Map: mt}
	}
	switch property.Kind {
	case ast.KindPropertyAssignment:
		key := l.literalKey(property.Name())
		return set(key, l.expression(property.Initializer()), property)
	case ast.KindShorthandPropertyAssignment:
		return set(&ir.StringConst{Value: property.Name().Text()}, l.shorthandValue(property), property)
	case ast.KindSpreadAssignment:
		return l.spreadIntoRecord(record, property.Expression())
	}
	l.notSupportedYet(property, "methods and accessors in hash-map objects are")
	return nil
}

// literalKey is the key a property name of an object literal gives: its text (tsc keeps
// a numeric literal's as JS's string of the number), or a computed name's value.
func (l *lowerer) literalKey(name *ast.Node) ir.Expr {
	if name.Kind == ast.KindComputedPropertyName {
		return l.recordKey(name.Expression())
	}
	return &ir.StringConst{Value: name.Text()}
}

// spreadIntoRecord copies into record (an expression of a hash-map object type) what
// `...source` spreads: a hash-map object's entries, or an object's properties.
func (l *lowerer) spreadIntoRecord(record ir.Expr, sourceNode *ast.Node) ir.Expr {
	mt := record.Type().(*ir.MapType)
	source := l.expression(sourceNode)
	switch {
	case source == nil:
		return nil
	case source.Type() == mt:
		return &ir.MapCall{Op: ir.MapAssign, Args: []ir.Expr{record, source}, Map: mt}
	case isRecord(source.Type()):
		l.notSupportedYet(sourceNode, fmt.Sprintf("spreading a %s into a %s is", source.Type(), mt))
		return nil
	}
	names := l.spreadNames(source, l.typeAt(sourceNode), sourceNode)
	if names == nil {
		return nil
	}
	built, object := l.newLocal("record", mt), l.newLocal("spread", source.Type()) // evaluated in this order
	var result ir.Expr = &ir.LocalRef{Local: built}
	for _, name := range names {
		value := l.convert(propertyOf(&ir.LocalRef{Local: object}, name), mt.Value, sourceNode)
		if value == nil {
			return nil
		}
		result = &ir.MapCall{Op: ir.MapSet, Args: []ir.Expr{result, &ir.StringConst{Value: name}, value}, Map: mt}
	}
	return &ir.Bind{Local: built, Value: record, In: &ir.Bind{Local: object, Value: source, In: result}}
}

// isObjectStatic reports whether a callee is the static method name of the global Object.
func (l *lowerer) isObjectStatic(callee *ast.Node, names ...string) bool {
	if callee.Kind != ast.KindPropertyAccessExpression || !slices.Contains(names, callee.AsPropertyAccessExpression().Name().Text()) {
		return false
	}
	object := callee.Expression()
	return object.Kind == ast.KindIdentifier && object.Text() == "Object" && l.isDeclaredInDefaultLib(l.target(object))
}

// objectStatics are the items each of Object.keys, Object.values and Object.entries lists.
var objectStatics = map[string]mapItems{"keys": mapKeys, "values": mapValues, "entries": mapEntries}

// objectStaticCall lowers Object.keys, Object.values and Object.entries (a new array of
// the keys, values or [key, value] pairs in JS's key order) and Object.hasOwn of a
// hash-map object (D-059). For other objects they wait for M3.5.
func (l *lowerer) objectStaticCall(call *ast.CallExpression) ir.Expr {
	name := ast.SkipParentheses(call.Expression).AsPropertyAccessExpression().Name().Text()
	args := call.Arguments.Nodes
	if len(args) == 0 || !l.isRecordExpression(args[0]) {
		l.notSupportedYet(call.Expression, fmt.Sprintf("`Object.%s` of anything but a hash-map object (`Record<string, T>`, an index signature) is", name))
		return nil
	}
	if l.hasSpreadArgument(call) {
		return nil
	}
	record := l.expression(args[0])
	if record == nil {
		return nil
	}
	if name == "hasOwn" {
		return l.recordHas(record, l.recordKey(args[1]), false)
	}
	return l.mapItemsArray(mapIterable{record, objectStatics[name]}, call.Expression)
}

// isRecordMethodCall reports whether node calls a method of Object.prototype on a
// hash-map object (`o.hasOwnProperty(k)`).
func (l *lowerer) isRecordMethodCall(node *ast.Node) bool {
	callee := ast.SkipParentheses(node.AsCallExpression().Expression)
	return callee.Kind == ast.KindPropertyAccessExpression && l.isRecordExpression(callee.Expression()) && l.isObjectPrototypeMember(callee)
}

// recordMethodCall lowers `o.hasOwnProperty(k)` for a hash-map object o.
func (l *lowerer) recordMethodCall(call *ast.CallExpression) ir.Expr {
	callee := ast.SkipParentheses(call.Expression)
	if name := callee.AsPropertyAccessExpression().Name().Text(); name != "hasOwnProperty" || len(call.Arguments.Nodes) != 1 {
		l.notSupportedYet(callee, fmt.Sprintf("the method `%s` of hash-map objects is", name))
		return nil
	}
	record := l.expression(callee.Expression())
	return l.recordHas(record, l.recordKey(call.Arguments.Nodes[0]), false)
}

// forInStatement lowers `for (const k in o)` for a hash-map object o (D-059): a loop over
// the keys o has when the loop starts, in JS's key order, that skips the keys deleted
// meanwhile and doesn't visit the keys added, as V8 does.
func (l *lowerer) forInStatement(node *ast.Node, label *ir.Label) []ir.Stmt {
	s := node.AsForInOrOfStatement()
	switch {
	case !l.isRecordExpression(s.Expression):
		l.notSupportedYet(node, "`for...in` over anything but a hash-map object (`Record<string, T>`, an index signature) is")
		return nil
	case s.Initializer.Kind != ast.KindVariableDeclarationList || len(s.Initializer.AsVariableDeclarationList().Declarations.Nodes) != 1:
		l.notSupportedYet(s.Initializer, "`for...in` without a declaration of its variable is")
		return nil
	}
	declaration := s.Initializer.AsVariableDeclarationList().Declarations.Nodes[0]
	record := l.expression(s.Expression)
	name := declaration.Name()
	symbol := l.checker.GetSymbolAtLocation(name)
	if record == nil {
		return nil
	}
	if t := l.bindingType(symbol, name); t != ir.String {
		if t != nil {
			l.notSupportedYet(name, fmt.Sprintf("a `for...in` variable of type %s is", t))
		}
		return nil
	}
	at := l.pos(node)
	recordLocal := l.newLocal("record", record.Type())
	keys := l.mapItemsArray(mapIterable{&ir.LocalRef{Local: recordLocal}, mapKeys}, node)
	if keys == nil {
		return nil
	}
	keysLocal, index := l.newLocal("keys", keys.Type()), l.newLocal("index", ir.Number)
	key := l.newLocal(name.Text(), ir.String)
	l.locals[symbol] = key
	body := l.statementBlock(s.Statement)
	present := l.recordHas(&ir.LocalRef{Local: recordLocal}, &ir.LocalRef{Local: key}, false)
	loop := &ir.Loop{
		At:     at,
		Label:  label,
		Cond:   &ir.Binary{Op: ir.Lt, X: &ir.LocalRef{Local: index}, Y: &ir.ArrayCall{Op: ir.ArrayLength, Args: []ir.Expr{&ir.LocalRef{Local: keysLocal}}}},
		Update: &ir.Assign{Local: index, Value: &ir.Binary{Op: ir.Add, X: &ir.LocalRef{Local: index}, Y: &ir.NumberConst{Value: 1}}},
		Body: &ir.Block{At: at, Stmts: []ir.Stmt{
			&ir.LocalDecl{At: l.pos(declaration), Local: key, Init: &ir.ArrayCall{Op: ir.ArrayElementChecked, Args: []ir.Expr{&ir.LocalRef{Local: keysLocal}, &ir.LocalRef{Local: index}}}},
			&ir.If{At: at, Cond: present, Then: body},
		}},
	}
	return []ir.Stmt{&ir.Block{At: at, Stmts: []ir.Stmt{
		&ir.LocalDecl{At: at, Local: recordLocal, Init: record},
		&ir.LocalDecl{At: at, Local: keysLocal, Init: keys},
		&ir.LocalDecl{At: at, Local: index, Init: &ir.NumberConst{Value: 0}},
		loop,
	}}}
}
