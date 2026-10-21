package lower

import (
	"slices"
	"strings"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/pts/diagnostic"
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
	if part.Type() == ir.Value && initializer != nil {
		return l.dynamicWithDefault(part, initializer, name)
	}
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

// propertyIn lowers `"p" in o` for a constant key p: whether o has p. A property o's type
// declares only an optional one may leave out (D-065), so it is true for any other; a key
// of a union, or one the type doesn't declare, is looked up at run time (keyIn, D-067).
func (l *lowerer) propertyIn(expr *ast.BinaryExpression) ir.Expr {
	name := expr.Left.Text()
	if t := l.typeAt(expr.Right); isUnionValue(t) || l.checker.GetPropertyOfType(t, name) == nil {
		return l.keyIn(expr)
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

// relaxedCast is a conversion of values of type from to an interface that they don't
// provide as it is, since their objects lack some of its optional properties or hold
// them as the one type besides undefined (D-066).
type relaxedCast struct {
	from ir.Type
	to   *ir.Interface
	node *ast.Node
	in   *ir.Func // the function it is in, which a failed trial may drop (D-190)
	// What a report of it is blamed on, as one where it is lowered would be (C176): it
	// is checked after lowering.
	blame *pairing
}

// holdsOptionally reports whether values of type t may be used as an interface whose
// members they provide (providesProperty, providesMethod), except for optional properties
// that they lack or hold in a field of the one type besides undefined they have; reads and
// writes of those through the interface find what the object has (D-066).
func holdsOptionally(t ir.Type, iface *ir.Interface) bool {
	for _, property := range iface.Fields {
		if !providesProperty(t, property) && !(property.Optional && holdsAsOptional(t, property)) {
			return false
		}
	}
	for _, method := range iface.Methods {
		if !providesMethod(t, method) {
			return false
		}
	}
	return true
}

// holdsAsOptional reports whether values of type t have an optional property of an
// interface in a field of the one type besides undefined the property has, or have no
// member of its name at all.
func holdsAsOptional(t ir.Type, property *ir.Field) bool {
	plain := optionalMember(property.Type)
	switch from := t.(type) {
	case *ir.Class:
		if field := from.Field(property.Name); field != nil {
			return plain != nil && field.Type == plain && !field.Method
		}
		if from.Shape {
			return from.AccessorField(property.Name, ir.Getter) == nil && from.AccessorField(property.Name, ir.Setter) == nil
		}
		return from.Method(property.Name, ir.Plain) == nil && from.Method(property.Name, ir.Getter) == nil && from.Method(property.Name, ir.Setter) == nil
	case *ir.Interface:
		if field := from.Field(property.Name); field != nil {
			return plain != nil && field.Type == plain && !field.Optional
		}
		return from.Method(property.Name) == nil
	}
	return false
}

// optionalMember is the one type besides undefined of an optional property's type, t, if
// it has one and that isn't a union: the type of a field the runtime can convert from and
// to (D-066). Otherwise nil.
func optionalMember(t ir.Type) ir.Type {
	u, ok := t.(*ir.Union)
	if !ok || len(u.Members) != 2 {
		return nil
	}
	for _, member := range u.Members {
		if member != ir.Undefined {
			return member
		}
	}
	return nil
}

// checkRelaxedCasts reports each relaxed conversion whose values may refer to an object
// that holds one of the interface's optional properties some other way: an instance of
// a subclass that declares it, or of a type converted to the interface converted from.
// Every interface property read through a site then finds what the compiler allowed.
func (l *lowerer) checkRelaxedCasts(funcs []*ir.Func) {
	if len(l.relaxedCasts) == 0 {
		return
	}
	sources := interfaceCasts(funcs)
	for _, cast := range l.relaxedCasts {
		if l.droppedFuncs[cast.in] {
			continue // in code a failed trial dropped (D-190)
		}
		for _, object := range l.concreteTypes(cast.from, sources, map[ir.Type]bool{}) {
			if property := unheld(object, cast.to); property != nil {
				l.reportBlamed(diagnostic.At(cast.node, diagnostic.NotSupportedYet, "using a `%s` as a `%s`, when it may refer to a `%s` that holds `%s` as another type, is not supported yet",
					cast.from, cast.to, objectTypeString(object), property.Name), cast.blame)
				break
			}
		}
	}
}

// unheld returns the property of an interface that objects of type t (a class or shape)
// neither provide nor hold as an optional property may be held, or nil.
func unheld(t ir.Type, iface *ir.Interface) *ir.Field {
	for _, property := range iface.Fields {
		if !providesProperty(t, property) && !(property.Optional && holdsAsOptional(t, property)) {
			return property
		}
	}
	return nil
}

// objectTypeString writes a class as its name and a shape as its fields.
func objectTypeString(t ir.Type) string {
	if shape, ok := t.(*ir.Class); ok && shape.Shape {
		return strings.TrimPrefix(shape.ShapeString(), shape.Name)
	}
	return t.String()
}

// interfaceCasts lists, for each interface, the types of the values the program converts
// to it.
func interfaceCasts(funcs []*ir.Func) map[*ir.Interface][]ir.Type {
	sources := map[*ir.Interface][]ir.Type{}
	for _, fn := range funcs {
		if fn.Body == nil {
			continue
		}
		ir.Walk(fn.Body.Stmts, func(ir.Stmt) {}, func(x ir.Expr) {
			if cast, ok := x.(*ir.ClassCast); ok {
				if iface, ok := cast.To.(*ir.Interface); ok && !slices.Contains(sources[iface], cast.X.Type()) {
					sources[iface] = append(sources[iface], cast.X.Type())
				}
			}
		})
	}
	return sources
}

// concreteTypes are the classes and shapes of the objects that values of type t may
// refer to: a shape; a class and its subclasses; or for an interface, those of the types
// converted to it (sources).
func (l *lowerer) concreteTypes(t ir.Type, sources map[*ir.Interface][]ir.Type, seen map[ir.Type]bool) []ir.Type {
	if seen[t] {
		return nil
	}
	seen[t] = true
	var types []ir.Type
	switch from := t.(type) {
	case *ir.Class:
		if from.Shape {
			return []ir.Type{from}
		}
		for _, class := range append(slices.Clone(l.classList), l.instanceClasses...) {
			if isSubclassOf(class, from) {
				types = append(types, class)
			}
		}
	case *ir.Interface:
		for _, source := range sources[from] {
			types = append(types, l.concreteTypes(source, sources, seen)...)
		}
	}
	return types
}
