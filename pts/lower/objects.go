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
	fields, ok := l.objectFields(t, node)
	if !ok {
		l.shapesByType[t] = nil
		return nil
	}
	key := fieldsKey(fields)
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
// types with the same properties of the same types are one interface, except that a
// recursive type is one of its own. It reports (at node) why a type can't be one and
// returns nil.
func (l *lowerer) interfaceOf(t *checker.Type, node *ast.Node) *ir.Interface {
	if iface, ok := l.interfacesByType[t]; ok {
		return iface // nil if it failed, which was reported
	}
	iface := &ir.Interface{}
	l.interfacesByType[t] = iface
	fields, ok := l.objectFields(t, node)
	if !ok {
		l.interfacesByType[t] = nil
		return nil
	}
	key := fieldsKey(fields)
	if existing, ok := l.interfacesByKey[key]; ok {
		l.interfacesByType[t] = existing
		return existing
	}
	iface.Fields = fields
	l.interfacesByKey[key] = iface
	return iface
}

// objectFields makes a field (of no class yet) for each property of an object type, in
// name order, or reports a property it can't hold and returns false.
func (l *lowerer) objectFields(t *checker.Type, node *ast.Node) ([]*ir.Field, bool) {
	var fields []*ir.Field
	for _, property := range l.checker.GetPropertiesOfType(t) {
		switch {
		case property.Flags&ast.SymbolFlagsOptional != 0:
			l.notSupportedYet(node, fmt.Sprintf("optional properties (`%s?` in %s) are", property.Name, l.checker.TypeToString(t)))
			return nil, false
		case property.Flags&(ast.SymbolFlagsMethod|ast.SymbolFlagsAccessor) != 0:
			l.notSupportedYet(node, fmt.Sprintf("methods and accessors in object types (`%s` in %s) are", property.Name, l.checker.TypeToString(t)))
			return nil, false
		case isArrayIndex(property.Name):
			l.notSupportedYet(node, fmt.Sprintf("numeric property names (`%s` in %s) are", property.Name, l.checker.TypeToString(t)))
			return nil, false
		}
		fieldType := l.propertyType(property, node)
		if fieldType == nil {
			return nil, false
		}
		fields = append(fields, &ir.Field{Name: property.Name, Type: fieldType})
	}
	slices.SortFunc(fields, func(a, b *ir.Field) int { return strings.Compare(a.Name, b.Name) })
	return fields, true
}

// propertyType is the IR type of a property of an object type: the numeric type the
// semantic pass gave its declaration, or the type tsc gives it.
func (l *lowerer) propertyType(property *ast.Symbol, node *ast.Node) ir.Type {
	if declaration := property.ValueDeclaration; declaration != nil && declaration.Name() != nil {
		if t := l.numericType(declaration.Name()); t != nil {
			return t
		}
	}
	return l.irType(l.checker.GetTypeOfSymbol(property), node)
}

// fieldsKey identifies a shape or interface by its fields' names and types.
func fieldsKey(fields []*ir.Field) string {
	var key strings.Builder
	for _, field := range fields {
		fmt.Fprintf(&key, "%q:", field.Name)
		switch t := field.Type.(type) {
		case *ir.Class, *ir.Interface:
			fmt.Fprintf(&key, "%p;", t) // the same class or interface
		default:
			fmt.Fprintf(&key, "%s;", t)
		}
	}
	return key.String()
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
// type of the program: in an interface or type literal, or in an object literal.
func (l *lowerer) isShapeProperty(member *ast.Node) bool {
	if member == nil {
		return false
	}
	switch member.Kind {
	case ast.KindPropertySignature, ast.KindPropertyAssignment, ast.KindShorthandPropertyAssignment:
		file := ast.GetSourceFileOfNode(member)
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
// (classCast), or an interface whose properties the object's fields provide, with the
// same types, so that every access through the interface finds a field it can use as is.
func (l *lowerer) objectCast(value ir.Expr, target ir.Type, node *ast.Node) ir.Expr {
	if class, ok := target.(*ir.Class); ok {
		if _, fromInterface := value.Type().(*ir.Interface); !fromInterface {
			return l.classCast(value, class, node)
		}
		if l.isNarrowedTo(node, class) {
			return &ir.ClassCast{X: value, To: class}
		}
	} else if missing := unprovided(value.Type(), target.(*ir.Interface)); missing == nil {
		return &ir.ClassCast{X: value, To: target}
	} else if t, ok := value.Type().(*ir.Class); ok && !t.Shape && t.Method(missing.Name, ir.Getter) != nil {
		l.notSupportedYet(node, fmt.Sprintf("using a `%s` as a `%s`, whose `%s` its getter provides, is", value.Type(), target, missing.Name))
		return nil
	}
	l.notSupportedYet(node, fmt.Sprintf("using a `%s` as a `%s` is", value.Type(), target))
	return nil
}

// unprovided returns a property of an interface that values of type t (a class, shape or
// interface) have no field of the same type for, or nil if they provide them all.
func unprovided(t ir.Type, iface *ir.Interface) *ir.Field {
	for _, property := range iface.Fields {
		var field *ir.Field
		switch from := t.(type) {
		case *ir.Class:
			if !isPrivateName(property.Name) {
				field = from.Field(property.Name)
			}
		case *ir.Interface:
			field = from.Field(property.Name)
		}
		if field == nil || field.Type != property.Type {
			return property
		}
	}
	return nil
}
