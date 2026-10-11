package lower

import (
	"fmt"
	"slices"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/pts/ir"
)

// withLeftOut adds to the fields and values of a new object of shape, made by a literal
// of type t, the optional properties the literal leaves out, absent (D-065): after the
// others, in the order t declares them, which is where JS puts properties assigned later.
func (l *lowerer) withLeftOut(t *checker.Type, shape *ir.Class, fields []*ir.Field, values []ir.Expr) ([]*ir.Field, []ir.Expr) {
	if len(fields) == len(shape.Fields) {
		return fields, values
	}
	for _, property := range l.checker.GetPropertiesOfType(t) {
		if field := shape.Field(property.Name); field != nil && field.Optional && !slices.Contains(fields, field) {
			fields, values = append(fields, field), append(values, &ir.Absent{T: field.Type})
		}
	}
	return fields, values
}

// isOptionalRead reports whether read reads the field of an optional property.
func isOptionalRead(read ir.Expr) bool {
	ref, ok := read.(*ir.FieldRef)
	return ok && ref.Field.Optional
}

// copiedRead is read, a property's value that a spread or rest element copies to field:
// an optional property's field is read as it is when field is optional too and of the
// same type, so that the copy of an object that leaves the property out leaves it out.
func copiedRead(read ir.Expr, field *ir.Field) ir.Expr {
	if ref, ok := read.(*ir.FieldRef); ok && ref.Field.Optional && field.Optional && ref.Field.Type == field.Type {
		ref.Raw = true
	}
	return read
}

// spreadOver is the value that a spread object's optional property, read, gives field,
// which an earlier part of the literal gave the value earlier (`{ ...defaults, ...o }`):
// earlier if the object leaves the property out, and otherwise the property's value. A
// field that can't hold undefined (tsc types `{ a: 1, ...o }`'s a as a number) keeps
// earlier for an undefined value too.
func (l *lowerer) spreadOver(read *ir.FieldRef, earlier ir.Expr, field *ir.Field, node *ast.Node) ir.Expr {
	u := read.Field.Type.(*ir.Union)
	value := l.newLocal(field.Name, u)
	ref := &ir.LocalRef{Local: value}
	if field.Optional && u == field.Type {
		read.Raw = true
		return &ir.Bind{Local: value, Value: read, In: &ir.Conditional{Cond: &ir.IsAbsent{X: ref}, Then: earlier, Else: ref}}
	}
	present := l.convert(fromUnion(ref, l.withoutUndefined(u)), field.Type, node)
	if present == nil {
		return nil
	}
	return &ir.Bind{Local: value, Value: read, In: &ir.Conditional{Cond: &ir.IsNullish{X: ref, Only: ir.Undefined}, Then: earlier, Else: present}}
}

// withDefault is part, the value of a property that an element of an object pattern
// with a default (initializer) binds: the default if part is undefined, as in JS, and
// otherwise part, as a value of the type tsc gives the element's name. A part that can't
// be undefined is itself.
func (l *lowerer) withDefault(part ir.Expr, initializer *ast.Node, name *ast.Node) ir.Expr {
	u, ok := part.Type().(*ir.Union)
	if initializer == nil || !ok || !u.Has(ir.Undefined) {
		return part
	}
	t := l.withoutUndefined(u)
	if name.Kind == ast.KindIdentifier {
		if t = l.bindingType(l.checker.GetSymbolAtLocation(name), name); t == nil {
			return nil
		}
	}
	fallback := l.expression(initializer)
	if fallback == nil {
		return nil
	}
	if fallback = l.convert(fallback, t, initializer); fallback == nil {
		return nil
	}
	value := l.newLocal("property", u)
	ref := &ir.LocalRef{Local: value}
	present := l.convert(fromUnion(ref, l.withoutUndefined(u)), t, name)
	if present == nil {
		return nil
	}
	return &ir.Bind{Local: value, Value: part, In: &ir.Conditional{Cond: &ir.IsNullish{X: ref, Only: ir.Undefined}, Then: fallback, Else: present}}
}

// propertyIn lowers `"p" in o` for a constant key p that o's type declares: whether o has
// p, which only an optional property may leave out (D-065), so it is true for any other.
// A key o's type doesn't declare waits for `in` on unions (M3.1c).
func (l *lowerer) propertyIn(expr *ast.BinaryExpression) ir.Expr {
	name := expr.Left.Text()
	if l.checker.GetPropertyOfType(l.typeAt(expr.Right), name) == nil {
		l.notSupportedYet(expr.Left, fmt.Sprintf("`in` with a key that %s doesn't declare is", l.checker.TypeToString(l.typeAt(expr.Right))))
		return nil
	}
	object := l.expression(expr.Right)
	if object == nil {
		return nil
	}
	var field *ir.Field
	switch t := object.Type().(type) {
	case *ir.Interface:
		field = t.Field(name)
	case *ir.Class:
		field = t.Field(name)
	}
	if field != nil && field.Optional {
		return &ir.HasField{X: object, Field: field}
	}
	return &ir.Sequence{X: object, Y: &ir.BoolConst{Value: true}}
}
