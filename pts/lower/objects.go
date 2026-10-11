package lower

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/pts/ir"
	"github.com/microsoft/typescript-go/pts/lib"
)

// isObjectType reports whether t is an object type of the program that a shape can hold
// (D-033): an interface, type literal or object literal type, not a class, a function
// type or a type of the ES library (arrays, Map, Date, …).
func (l *lowerer) isObjectType(t *checker.Type) bool {
	if t.Flags()&checker.TypeFlagsObject == 0 || t.ObjectFlags()&(checker.ObjectFlagsAnonymous|checker.ObjectFlagsInterface) == 0 ||
		t.ObjectFlags()&checker.ObjectFlagsClass != 0 {
		return false
	}
	if symbol := t.Symbol(); symbol != nil {
		for _, declaration := range symbol.Declarations {
			if l.program.IsSourceFileDefaultLibrary(ast.GetSourceFileOfNode(declaration).Path()) {
				return false
			}
		}
	}
	return len(l.checker.GetSignaturesOfType(t, checker.SignatureKindCall)) == 0 &&
		len(l.checker.GetSignaturesOfType(t, checker.SignatureKindConstruct)) == 0 && len(l.checker.GetIndexInfosOfType(t)) == 0
}

// shape returns the shape of an object type: the anonymous struct type, with a field per
// property in name order, of the objects that literals of the type make. Object types
// with the same properties of the same types share a shape, except that a recursive type
// has one of its own. It reports (at node) why a type has no shape and returns nil.
func (l *lowerer) shape(t *checker.Type, node *ast.Node) *ir.Class {
	if shape, ok := l.shapesByType[t]; ok {
		return shape // nil if it failed, which was reported
	}
	shape := &ir.Class{Shape: true}
	l.shapesByType[t] = shape // a recursive type finds itself here
	fields, methods, ok := l.objectMembers(t, node)
	if ok && len(methods) > 0 {
		l.notSupportedYet(node, "methods and accessors in object literals are")
		ok = false
	}
	if !ok {
		l.shapesByType[t] = nil
		return nil
	}
	key := membersKey(fields, nil)
	if existing, ok := l.shapesByKey[key]; ok {
		l.shapesByType[t] = existing
		return existing
	}
	shape.Name = "object" + strconv.Itoa(len(l.shapeList)+1)
	for _, field := range fields {
		field.Class = shape
	}
	shape.Fields = fields
	l.shapesByKey[key] = shape
	l.shapeList = append(l.shapeList, shape)
	return shape
}

// interfaceOf returns the interface an object type is, as the type of a binding: object
// types with the same members of the same types are one interface, except that a
// recursive type is one of its own. It reports (at node) why a type can't be one and
// returns nil.
func (l *lowerer) interfaceOf(t *checker.Type, node *ast.Node) *ir.Interface {
	if iface, ok := l.interfacesByType[t]; ok {
		return iface // nil if it failed, which was reported
	}
	iface := &ir.Interface{}
	l.interfacesByType[t] = iface
	fields, methods, ok := l.objectMembers(t, node)
	if !ok {
		l.interfacesByType[t] = nil
		return nil
	}
	key := membersKey(fields, methods)
	if existing, ok := l.interfacesByKey[key]; ok {
		l.interfacesByType[t] = existing
		return existing
	}
	iface.Fields, iface.Methods = fields, methods
	l.interfacesByKey[key] = iface
	l.interfaceList = append(l.interfaceList, iface)
	return iface
}

// objectMembers makes a field (of no class yet) for each property of an object type and
// an interface method for each of its methods, both in name order, or reports a member it
// can't hold and returns false. A property may be declared with accessors.
func (l *lowerer) objectMembers(t *checker.Type, node *ast.Node) ([]*ir.Field, []*ir.InterfaceMethod, bool) {
	var fields []*ir.Field
	var methods []*ir.InterfaceMethod
	for _, property := range l.checker.GetPropertiesOfType(t) {
		switch {
		case property.Flags&ast.SymbolFlagsOptional != 0:
			l.notSupportedYet(node, fmt.Sprintf("optional properties (`%s?` in %s) are", property.Name, l.checker.TypeToString(t)))
			return nil, nil, false
		case isArrayIndex(property.Name):
			l.notSupportedYet(node, fmt.Sprintf("numeric property names (`%s` in %s) are", property.Name, l.checker.TypeToString(t)))
			return nil, nil, false
		case property.Flags&ast.SymbolFlagsAccessor == ast.SymbolFlagsSetAccessor:
			l.notSupportedYet(node, fmt.Sprintf("a property with a setter but no getter (`%s` in %s) is", property.Name, l.checker.TypeToString(t)))
			return nil, nil, false
		case property.Flags&ast.SymbolFlagsMethod != 0:
			method := l.interfaceMethod(property, t, node)
			if method == nil {
				return nil, nil, false
			}
			methods = append(methods, method)
			continue
		}
		fieldType := l.propertyType(property, node)
		if fieldType == nil {
			return nil, nil, false
		}
		fields = append(fields, &ir.Field{Name: property.Name, Type: fieldType})
	}
	slices.SortFunc(fields, func(a, b *ir.Field) int { return strings.Compare(a.Name, b.Name) })
	slices.SortFunc(methods, func(a, b *ir.InterfaceMethod) int { return strings.Compare(a.Name, b.Name) })
	return fields, methods, true
}

// propertyType is the IR type of a property of an object type: the numeric type the
// semantic pass gave its declaration (its getter's result, for an accessor property),
// or the type tsc gives it.
func (l *lowerer) propertyType(property *ast.Symbol, node *ast.Node) ir.Type {
	if getter := ast.GetDeclarationOfKind(property, ast.KindGetAccessor); getter != nil {
		return l.resultType(getter)
	}
	if declaration := property.ValueDeclaration; declaration != nil && declaration.Name() != nil {
		if t := l.numericType(declaration.Name()); t != nil {
			return t
		}
	}
	return l.irType(l.checker.GetTypeOfSymbol(property), node)
}

// interfaceMethod makes the interface method for a method of an object type, or reports
// (at node, or at a parameter) why it can't and returns nil.
func (l *lowerer) interfaceMethod(property *ast.Symbol, t *checker.Type, node *ast.Node) *ir.InterfaceMethod {
	declarations := property.Declarations
	switch {
	case len(declarations) != 1:
		l.notSupportedYet(node, fmt.Sprintf("overloaded methods (`%s` in %s) are", property.Name, l.checker.TypeToString(t)))
		return nil
	case declarations[0].TypeParameterList() != nil:
		l.notSupportedYet(node, fmt.Sprintf("generic methods (`%s` in %s) are", property.Name, l.checker.TypeToString(t)))
		return nil
	}
	method := &ir.InterfaceMethod{Name: property.Name}
	for _, param := range declarations[0].Parameters() {
		paramType := l.parameterType(param)
		if paramType == nil {
			return nil
		}
		method.Params = append(method.Params, paramType)
	}
	if method.Result = l.resultType(declarations[0]); method.Result == nil {
		return nil
	}
	return method
}

// membersKey identifies a shape or interface by its members' names and types.
func membersKey(fields []*ir.Field, methods []*ir.InterfaceMethod) string {
	var key strings.Builder
	for _, field := range fields {
		fmt.Fprintf(&key, "%q:%s;", field.Name, typeKey(field.Type))
	}
	for _, method := range methods {
		fmt.Fprintf(&key, "%q(", method.Name)
		for _, param := range method.Params {
			fmt.Fprintf(&key, "%s,", typeKey(param))
		}
		fmt.Fprintf(&key, "):%s;", typeKey(method.Result))
	}
	return key.String()
}

// typeKey identifies an IR type in membersKey: a class or interface by its identity.
func typeKey(t ir.Type) string {
	switch t.(type) {
	case *ir.Class, *ir.Interface, *ir.Array:
		return fmt.Sprintf("%p", t)
	}
	return t.String()
}

// isArrayIndex reports whether a property name is an array index ("0", "42"), which JS
// orders before every other property of an object.
func isArrayIndex(name string) bool {
	n, err := strconv.ParseUint(name, 10, 32)
	return err == nil && n < 1<<32-1 && strconv.FormatUint(n, 10) == name
}

// objectLiteral lowers `{ x: 1, y }`: an object of the shape of the literal's type, with
// its properties in the order the literal writes them.
func (l *lowerer) objectLiteral(node *ast.Node) ir.Expr {
	shape := l.shape(l.literalType(node), node)
	if shape == nil {
		return nil
	}
	properties := node.AsObjectLiteralExpression().Properties.Nodes
	fields := make([]*ir.Field, len(properties))
	values := make([]ir.Expr, len(properties))
	for i, property := range properties {
		var value ir.Expr
		switch property.Kind {
		case ast.KindPropertyAssignment:
			value = l.expression(property.Initializer())
		case ast.KindShorthandPropertyAssignment:
			value = l.shorthandValue(property)
		case ast.KindSpreadAssignment:
			l.notSupportedYet(property, "spreading `...` into an object literal is")
			return nil
		default:
			l.notSupportedYet(property, "methods and accessors in object literals are")
			return nil
		}
		if property.Name().Kind == ast.KindComputedPropertyName {
			l.notSupportedYet(property.Name(), "computed property names are")
			return nil
		}
		if value == nil {
			return nil
		}
		fields[i] = shape.Field(l.checker.GetSymbolAtLocation(property.Name()).Name)
		if values[i] = l.convert(value, fields[i].Type, property); values[i] == nil {
			return nil
		}
	}
	return &ir.NewObject{Shape: shape, Order: shape.Order(fields), Values: values}
}

// literalType is the object type an object literal makes an object of: its contextual
// type if that is an object type with the same properties, so that `{ from: pixel }`
// written for a `Segment` holds a `Point`, and otherwise the literal's own type.
func (l *lowerer) literalType(node *ast.Node) *checker.Type {
	own := l.checker.GetTypeAtLocation(node)
	contextual := l.checker.GetContextualType(node, checker.ContextFlagsNone)
	if contextual == nil || !l.isObjectType(contextual) {
		return own
	}
	names := func(t *checker.Type) []string {
		var names []string
		for _, property := range l.checker.GetPropertiesOfType(t) {
			names = append(names, property.Name)
		}
		slices.Sort(names)
		return names
	}
	if !slices.Equal(names(own), names(contextual)) {
		return own
	}
	return contextual
}

// shorthandValue lowers the value of a shorthand property `{ x }`: the binding x.
func (l *lowerer) shorthandValue(property *ast.Node) ir.Expr {
	name := property.Name()
	if x, ok := l.binding(name, l.followAliases(l.checker.GetShorthandAssignmentValueSymbol(property))); ok {
		return x
	}
	l.notSupportedYet(name, fmt.Sprintf("using `%s` is", name.Text()))
	return nil
}

// isShapeProperty reports whether a member declaration declares a property of an object
// type of the program, plain or with accessors: in an interface or type literal, or in an
// object literal.
func (l *lowerer) isShapeProperty(member *ast.Node) bool {
	if member == nil {
		return false
	}
	switch member.Kind {
	case ast.KindPropertySignature, ast.KindPropertyAssignment, ast.KindShorthandPropertyAssignment, ast.KindGetAccessor, ast.KindSetAccessor:
		return l.isObjectTypeMember(member)
	}
	return false
}

// isObjectTypeMethod reports whether a member declaration declares a method of an object
// type of the program.
func (l *lowerer) isObjectTypeMethod(member *ast.Node) bool {
	return member != nil && (member.Kind == ast.KindMethodSignature || member.Kind == ast.KindMethodDeclaration) && l.isObjectTypeMember(member)
}

// isObjectTypeMember reports whether a declaration declares a member of an object type of
// the program: in an interface, a type literal or an object literal, not in the libraries.
func (l *lowerer) isObjectTypeMember(declaration *ast.Node) bool {
	switch declaration.Parent.Kind {
	case ast.KindInterfaceDeclaration, ast.KindTypeLiteral, ast.KindObjectLiteralExpression:
		file := ast.GetSourceFileOfNode(declaration)
		return !lib.IsLibPath(file.FileName()) && !l.program.IsSourceFileDefaultLibrary(file.Path())
	}
	return false
}

// shapeProperty lowers the object of `o.p` for a property p of an object type and
// returns it with p's field, or reports why it can't and returns nils.
func (l *lowerer) shapeProperty(node *ast.Node) (ir.Expr, *ir.Field) {
	if ast.IsOptionalChain(node) {
		l.notSupportedYet(node, "optional chaining `?.` is")
		return nil, nil
	}
	object := l.expression(node.Expression())
	if object == nil {
		return nil, nil
	}
	name := node.AsPropertyAccessExpression().Name().Text()
	switch t := object.Type().(type) {
	case *ir.Interface:
		return object, t.Field(name)
	case *ir.Class:
		if t.Shape {
			return object, t.Field(name)
		}
	}
	l.notSupportedYet(node, fmt.Sprintf("a property of an object type on a %s is", object.Type()))
	return nil, nil
}

// objectCast converts value, a reference to an object, to another object type: a class
// (classCast), or an interface whose members the object provides with the same types
// (provides), so that every access through the interface finds a member it can use as is.
func (l *lowerer) objectCast(value ir.Expr, target ir.Type, node *ast.Node) ir.Expr {
	if class, ok := target.(*ir.Class); ok {
		if _, fromInterface := value.Type().(*ir.Interface); !fromInterface {
			return l.classCast(value, class, node)
		}
		if l.isNarrowedTo(node, class) {
			return &ir.ClassCast{X: value, To: class}
		}
	} else if missing := unprovided(value.Type(), target.(*ir.Interface)); missing == "" {
		return &ir.ClassCast{X: value, To: target}
	} else {
		l.notSupportedYet(node, fmt.Sprintf("using a `%s` as a `%s`, whose `%s` it doesn't provide with the same types, is", value.Type(), target, missing))
		return nil
	}
	l.notSupportedYet(node, fmt.Sprintf("using a `%s` as a `%s` is", value.Type(), target))
	return nil
}

// unprovided returns the name of a member of an interface that values of type t (a class,
// shape or interface) don't provide with the same types, or "" if they provide them all.
// A class provides a property with a field, or with a getter (and maybe a setter), and a
// method with a method; a shape provides properties with fields; an interface provides
// what it declares.
func unprovided(t ir.Type, iface *ir.Interface) string {
	for _, property := range iface.Fields {
		if !providesProperty(t, property) {
			return property.Name
		}
	}
	for _, method := range iface.Methods {
		if !providesMethod(t, method) {
			return method.Name
		}
	}
	return ""
}

// providesProperty reports whether values of type t have a property of the same name and
// type as an interface's.
func providesProperty(t ir.Type, property *ir.Field) bool {
	switch from := t.(type) {
	case *ir.Class:
		if field := from.Field(property.Name); field != nil {
			return field.Type == property.Type
		}
		getter, setter := from.Method(property.Name, ir.Getter), from.Method(property.Name, ir.Setter)
		return !from.Shape && getter != nil && getter.Func.Result == property.Type && (setter == nil || setter.Func.Params[1].Type == property.Type)
	case *ir.Interface:
		field := from.Field(property.Name)
		return field != nil && field.Type == property.Type
	}
	return false
}

// providesMethod reports whether values of type t have a method of the same name and
// signature as an interface's.
func providesMethod(t ir.Type, method *ir.InterfaceMethod) bool {
	switch from := t.(type) {
	case *ir.Class:
		own := from.Method(method.Name, ir.Plain)
		if own == nil || own.Func.Result != method.Result || len(own.Func.Params) != len(method.Params)+1 {
			return false
		}
		for i, param := range method.Params {
			if own.Func.Params[i+1].Type != param {
				return false
			}
		}
		return true
	case *ir.Interface:
		return from.Method(method.Name) != nil && slices.Equal(from.Method(method.Name).Params, method.Params) && from.Method(method.Name).Result == method.Result
	}
	return false
}

// interfaceMethodCall lowers `o.m(args)` for a method m of an object type: a call of the
// method that the class of the object o refers to has, found at run time.
func (l *lowerer) interfaceMethodCall(call *ast.CallExpression) ir.Expr {
	callee := ast.SkipParentheses(call.Expression)
	if ast.IsOptionalChain(callee) {
		l.notSupportedYet(callee, "optional chaining `?.` is")
		return nil
	}
	receiver := l.expression(callee.Expression())
	if receiver == nil {
		return nil
	}
	iface, ok := receiver.Type().(*ir.Interface)
	if !ok || iface.Method(callee.AsPropertyAccessExpression().Name().Text()) == nil {
		l.notSupportedYet(callee, fmt.Sprintf("calling a method of an object type on a %s is", receiver.Type()))
		return nil
	}
	method := iface.Method(callee.AsPropertyAccessExpression().Name().Text())
	args := l.typedArguments(call.Arguments.Nodes, method.Params)
	if args == nil {
		return nil
	}
	return &ir.InterfaceCall{Method: method, Args: append([]ir.Expr{receiver}, args...)}
}

// markAccessorProperties marks the properties of interfaces that some class of the
// program has a getter or setter for, so that reading and writing them through an
// interface may call it.
func (l *lowerer) markAccessorProperties() {
	accessors := map[string]bool{}
	for _, class := range l.classList {
		for _, method := range class.Methods {
			if method.Kind != ir.Plain {
				accessors[method.Name] = true
			}
		}
	}
	for _, iface := range l.interfaceList {
		for _, property := range iface.Fields {
			property.Accessor = accessors[property.Name]
		}
	}
}
