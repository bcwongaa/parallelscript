package lower

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/jsnum"
	"github.com/microsoft/typescript-go/pts/check"
	"github.com/microsoft/typescript-go/pts/ir"
)

// Enums compile away (D-052): a member is the constant tsc computes for it, an enum type
// is the number or string type of its members, and a numeric enum's reverse mapping, E[n],
// compares n with each member's value. The enum object used as a value is made where it
// is used (D-089).

// enumDeclaration checks an enum declaration, which makes no code: a member whose value
// tsc can't compute is reported.
func (l *lowerer) enumDeclaration(node *ast.Node) {
	for _, member := range node.Members() {
		if l.checker.GetConstantValue(member) == nil {
			l.notSupportedYet(member, "enum members whose values aren't constants are")
		}
	}
}

// enumMember lowers `E.m` or `E["m"]` for a member m of an enum E to m's value. ok is
// false if node names no enum member.
func (l *lowerer) enumMember(node *ast.Node) (x ir.Expr, ok bool) {
	name := node
	switch node.Kind {
	case ast.KindPropertyAccessExpression:
		name = node.AsPropertyAccessExpression().Name()
	case ast.KindElementAccessExpression:
		if l.enumOf(node.Expression()) == nil {
			return nil, false // `totals[Color.Red]` reads totals (D-089)
		}
		name = ast.SkipParentheses(node.AsElementAccessExpression().ArgumentExpression)
		if !ast.IsStringLiteralLike(name) {
			return nil, false // `E[E.m]` is E's reverse mapping of m's value
		}
	default:
		return nil, false
	}
	symbol := l.checker.GetSymbolAtLocation(name)
	if symbol == nil || symbol.Flags&ast.SymbolFlagsEnumMember == 0 {
		return nil, false
	}
	switch value := l.checker.GetConstantValue(symbol.ValueDeclaration).(type) {
	case jsnum.Number:
		return &ir.NumberConst{Value: float64(value)}, true
	case string:
		return &ir.StringConst{Value: value}, true
	}
	return nil, true // reported with the declaration
}

// enumOf returns the declaration of the (non-const) enum an expression names, or nil.
func (l *lowerer) enumOf(node *ast.Node) *ast.Node {
	symbol := l.target(node)
	if symbol == nil || symbol.Flags&ast.SymbolFlagsRegularEnum == 0 || len(symbol.Declarations) != 1 {
		return nil
	}
	return symbol.ValueDeclaration
}

// isEnumReverseMapping reports whether node is E[n] for an enum E and a number n: the
// name of E's member whose value is n.
func (l *lowerer) isEnumReverseMapping(node *ast.Node) bool {
	return node.Kind == ast.KindElementAccessExpression && l.enumOf(node.Expression()) != nil &&
		check.IsNumber(l.typeAt(node.AsElementAccessExpression().ArgumentExpression))
}

// enumReverseMapping lowers E[n]: the name of the last member of E whose value is n, as
// JS's enum object maps it. Where any value can go, a value that no member has gives
// undefined, as in JS; where a string is required (checked), it throws RangeError (D-015).
func (l *lowerer) enumReverseMapping(node *ast.Node, checked bool) ir.Expr {
	access := node.AsElementAccessExpression()
	enum := l.enumOf(access.Expression)
	index := l.expression(access.ArgumentExpression)
	if index == nil {
		return nil
	}
	if index = l.convert(index, ir.Number, access.ArgumentExpression); index == nil {
		return nil
	}
	value := l.newLocal("value", ir.Number)
	var name ir.Expr = &ir.UndefinedConst{}
	if checked {
		message := concat(&ir.ToString{X: &ir.LocalRef{Local: value}}, &ir.StringConst{Value: " is not a value of the enum " + enum.Name().Text()})
		name = discard(&ir.CallBuiltin{Builtin: ir.ThrowError, Args: []ir.Expr{&ir.StringConst{Value: "RangeError"}, message}}, &ir.StringConst{})
	}
	for _, member := range enum.Members() {
		memberValue, isNumber := l.checker.GetConstantValue(member).(jsnum.Number)
		if !isNumber {
			continue // a string member has no reverse mapping
		}
		var memberName ir.Expr = &ir.StringConst{Value: member.Name().Text()}
		if !checked {
			memberName = box(memberName)
		}
		matches := &ir.Binary{Op: ir.Eq, X: &ir.LocalRef{Local: value}, Y: &ir.NumberConst{Value: float64(memberValue)}}
		name = &ir.Conditional{Cond: matches, Then: memberName, Else: name} // the last member that matches wins, as in JS
	}
	return &ir.Bind{Local: value, Value: index, In: name}
}

// enumObjectType is the type of an enum's object as a value: a hash-map object of
// strings, or of numbers and strings if a member is a number.
func (l *lowerer) enumObjectType(enum *ast.Node) *ir.MapType {
	for _, member := range enum.Members() {
		if _, isNumber := l.checker.GetConstantValue(member).(jsnum.Number); isNumber {
			return l.recordOf(l.unionOf([]ir.Type{ir.Number, ir.String}))
		}
	}
	return l.recordOf(ir.String)
}

// enumObject makes the object an enum's name gives as a value, as JS's enum object: a
// hash-map object with each member's value under its name and, for a number, the name
// under the number.
func (l *lowerer) enumObject(enum *ast.Node) ir.Expr {
	mt := l.enumObjectType(enum)
	var record ir.Expr = &ir.NewMap{Map: mt}
	set := func(key string, value ir.Expr) {
		record = &ir.MapCall{Op: ir.MapSet, Args: []ir.Expr{record, &ir.StringConst{Value: key}, l.convert(value, mt.Value, enum)}, Map: mt}
	}
	for _, member := range enum.Members() {
		name := member.Name().Text()
		switch value := l.checker.GetConstantValue(member).(type) {
		case jsnum.Number:
			set(name, &ir.NumberConst{Value: float64(value)})
			set(value.String(), &ir.StringConst{Value: name})
		case string:
			set(name, &ir.StringConst{Value: value})
		}
	}
	return record
}
