package lower

import (
	"fmt"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/pts/ir"
)

// isUnionReceiver reports whether a property access reads a member of a value of a union
// of object types (`shape.kind` for `shape: Circle | Square`, D-067), other than a member
// they share as one declaration (isMemberOfAll).
func (l *lowerer) isUnionReceiver(node *ast.Node) bool {
	if node.Kind != ast.KindPropertyAccessExpression || l.isMemberOfAll(node) {
		return false
	}
	t := l.checker.GetNonNullableType(l.typeAt(node.Expression()))
	return t.Flags()&checker.TypeFlagsUnion != 0 && isUnionValue(t)
}

// unionProperty returns the object of a property access on a union of object types,
// as an interface with just that property, and the property's field: one that members
// holding it as different types (all within its type) make Mixed, read by the field's
// kind. It reports why it can't and returns nils.
func (l *lowerer) unionProperty(node *ast.Node) (ir.Expr, *ir.Field) {
	name := node.AsPropertyAccessExpression().Name().Text()
	t := l.checker.GetNonNullableType(l.typeAt(node.Expression()))
	members, ok := l.unionObjectMembers(t, name, node)
	if !ok {
		return nil, nil
	}
	property := l.checker.GetPropertyOfType(t, name)
	if property == nil || isMethodSymbol(property) {
		l.notSupportedYet(node, fmt.Sprintf("using the method `%s` of a %s (a union) as a value is", name, l.checker.TypeToString(t)))
		return nil, nil
	}
	fieldType := l.irType(l.typeOfSymbol(property), node)
	if fieldType == nil {
		return nil, nil
	}
	optional := property.Flags&ast.SymbolFlagsOptional != 0
	if optional {
		fieldType = l.withUndefined(fieldType)
	}
	field := &ir.Field{Name: name, Type: fieldType, Optional: optional}
	for _, member := range members {
		switch {
		case providesProperty(member, field):
		case holdsWithin(member, field):
			field.Mixed = true
		default:
			l.notSupportedYet(node, fmt.Sprintf("reading `%s` of a %s, whose member %s holds it otherwise, is", name, l.checker.TypeToString(t), objectTypeString(member)))
			return nil, nil
		}
	}
	iface := l.internInterface([]*ir.Field{field}, nil)
	object := l.expression(node.Expression())
	if object == nil {
		return nil, nil
	}
	return l.asUnionInterface(object, iface, node), iface.Fields[0]
}

// unionMethodCall lowers `u.m(args)` for u of a union of object types whose members all
// have the method m with one signature: a call through an interface with just that method.
func (l *lowerer) unionMethodCall(call *ast.CallExpression) ir.Expr {
	callee := ast.SkipParentheses(call.Expression)
	name := callee.AsPropertyAccessExpression().Name().Text()
	t := l.checker.GetNonNullableType(l.typeAt(callee.Expression()))
	members, ok := l.unionObjectMembers(t, name, callee)
	if !ok {
		return nil
	}
	property := l.checker.GetPropertyOfType(t, name)
	if property == nil || !isMethodSymbol(property) {
		l.notSupportedYet(callee, fmt.Sprintf("calling the function-valued property `%s` of a %s (a union) is", name, l.checker.TypeToString(t)))
		return nil
	}
	signatures := l.checker.GetSignaturesOfType(l.typeOfSymbol(property), checker.SignatureKindCall)
	if len(signatures) != 1 {
		l.notSupportedYet(callee, fmt.Sprintf("calling `%s` of a %s, whose members' signatures differ, is", name, l.checker.TypeToString(t)))
		return nil
	}
	ft := l.signatureType(signatures[0], callee)
	if ft == nil {
		return nil
	}
	method := &ir.InterfaceMethod{Name: name, Params: ft.Params, Result: ft.Result}
	for _, member := range members {
		if !providesMethod(member, method) {
			l.notSupportedYet(callee, fmt.Sprintf("calling `%s` of a %s, whose member %s has it with another signature, is", name, l.checker.TypeToString(t), objectTypeString(member)))
			return nil
		}
	}
	iface := l.internInterface(nil, []*ir.InterfaceMethod{method})
	receiver := l.expression(callee.Expression())
	if receiver == nil {
		return nil
	}
	args := l.typedArguments(call.Arguments.Nodes, method.Params)
	if args == nil {
		return nil
	}
	return &ir.InterfaceCall{Method: iface.Methods[0], Args: append([]ir.Expr{l.asUnionInterface(receiver, iface, callee)}, args...)}
}

// isMethodSymbol reports whether every declaration of a property symbol (a union's,
// which tsc makes of its members') declares a method.
func isMethodSymbol(property *ast.Symbol) bool {
	for _, declaration := range property.Declarations {
		if declaration.Kind != ast.KindMethodDeclaration && declaration.Kind != ast.KindMethodSignature {
			return false
		}
	}
	return len(property.Declarations) > 0
}

// unionObjectMembers returns the IR types of the members of t, a union of object types
// (classes, shapes and interfaces), or reports (reading name) a member of another type.
func (l *lowerer) unionObjectMembers(t *checker.Type, name string, node *ast.Node) ([]ir.Type, bool) {
	var members []ir.Type
	for _, member := range t.Types() {
		m := l.irType(member, node)
		switch m.(type) {
		case nil:
			return nil, false // reported
		case *ir.Class, *ir.Interface:
			members = append(members, m)
		default:
			l.notSupportedYet(node, fmt.Sprintf("reading `%s` of a %s (a union of objects and other values) is", name, l.checker.TypeToString(t)))
			return nil, false
		}
	}
	return members, true
}

// holdsWithin reports whether values of type t (a class, shape or interface) hold an
// interface's property in a field whose values are all values of its type (a union's
// member's field), or, if it is optional, may lack it (holdsAsOptional).
func holdsWithin(t ir.Type, property *ir.Field) bool {
	u, ok := property.Type.(*ir.Union)
	if !ok {
		return false
	}
	var field *ir.Field
	switch from := t.(type) {
	case *ir.Class:
		field = from.Field(property.Name)
	case *ir.Interface:
		field = from.Field(property.Name)
	}
	if field == nil {
		return property.Optional && holdsAsOptional(t, property)
	}
	if fu, ok := field.Type.(*ir.Union); ok {
		return isNarrowing(u, fu)
	}
	return !field.Method && u.Has(field.Type)
}

// asUnionInterface converts object, a value of a union of object types, to an interface
// its members provide or hold.
func (l *lowerer) asUnionInterface(object ir.Expr, iface *ir.Interface, node *ast.Node) ir.Expr {
	if isUnion(object.Type()) {
		return &ir.FromUnion{X: object, To: iface}
	}
	return l.convert(object, iface, node)
}

// isUpcastOfMembers reports whether every member of u is an object that target, a class
// or interface, is as it is: a subclass of the class, or a type that provides the
// interface's members (`unprovided`).
func isUpcastOfMembers(u *ir.Union, target ir.Type) bool {
	for _, member := range u.Members {
		switch to := target.(type) {
		case *ir.Class:
			from, ok := member.(*ir.Class)
			if !ok || to.Shape || !isSubclassOf(from, to) {
				return false
			}
		case *ir.Interface:
			if !isObject(member) || isArray(member) || unprovided(member, to) != "" {
				return false
			}
		default:
			return false
		}
	}
	return true
}

// isSubclassOf reports whether class is ancestor or one of its subclasses.
func isSubclassOf(class *ir.Class, ancestor *ir.Class) bool {
	for ; class != nil; class = class.Parent {
		if class == ancestor {
			return true
		}
	}
	return false
}

// internInterface returns the interface with these members, made if there is none yet.
func (l *lowerer) internInterface(fields []*ir.Field, methods []*ir.InterfaceMethod) *ir.Interface {
	key := membersKey(fields, methods)
	if existing, ok := l.interfacesByKey[key]; ok {
		return existing
	}
	iface := &ir.Interface{Fields: fields, Methods: methods}
	l.interfacesByKey[key] = iface
	l.interfaceList = append(l.interfaceList, iface)
	return iface
}

// keyIn lowers `"p" in o` looked up at run time (D-067): for o of a union, or of an
// object type that doesn't declare p, whose objects may have it all the same.
func (l *lowerer) keyIn(expr *ast.BinaryExpression) ir.Expr {
	object := l.expression(expr.Right)
	if object == nil {
		return nil
	}
	if object.Type() == ir.Value { // any (D-070)
		return &ir.CallBuiltin{Builtin: ir.DynamicHas, Args: []ir.Expr{object, box(&ir.StringConst{Value: expr.Left.Text()})}}
	}
	switch object.Type().(type) {
	case *ir.Union:
		object = &ir.FromUnion{X: object, To: l.internInterface(nil, nil)}
	case *ir.Class, *ir.Interface:
	default:
		l.notSupportedYet(expr.Right, fmt.Sprintf("`in` on a %s is", object.Type()))
		return nil
	}
	name := expr.Left.Text()
	l.inKeys[name] = true
	return &ir.HasKey{X: object, Name: name}
}
