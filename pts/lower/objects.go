package lower

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/internal/jsnum"
	"github.com/microsoft/typescript-go/pts/ir"
	"github.com/microsoft/typescript-go/pts/lib"
)

// isObjectType reports whether t is an object type of the program that a shape can hold
// (D-033): an interface, type literal or object literal type, a mapped type with
// properties only (`Partial<Point>`, D-065), or an intersection of them, not a class, a
// function type or a type of the ES library (arrays, Map, Date, …).
func (l *lowerer) isObjectType(t *checker.Type) bool {
	if t.Flags()&checker.TypeFlagsIntersection != 0 {
		return !slices.ContainsFunc(t.Types(), func(member *checker.Type) bool { return !l.isObjectType(member) }) // `Named & Aged` (D-089)
	}
	if t.Flags()&checker.TypeFlagsObject == 0 || t.ObjectFlags()&checker.ObjectFlagsClass != 0 || checker.IsTupleType(t) {
		return false
	}
	isGenericInterface := t.ObjectFlags()&checker.ObjectFlagsReference != 0 && t.Target().ObjectFlags()&checker.ObjectFlagsClass == 0 // Box<string>
	// A mapped type (`Partial<Point>`) is declared in the library, but of the program's properties.
	isMapped := t.ObjectFlags()&checker.ObjectFlagsMapped != 0
	if t.ObjectFlags()&(checker.ObjectFlagsAnonymous|checker.ObjectFlagsInterface) == 0 && !isGenericInterface && !isMapped {
		return false
	}
	if symbol := t.Symbol(); symbol != nil && !isMapped && !l.libObjectTypes[symbol] {
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
// property and method in name order, of the objects that literals of the type make; a
// method's field holds its closure (D-044). Object types with the same members of the
// same types share a shape, except that a recursive type has one of its own. It reports
// (at node) why a type has no shape and returns nil.
func (l *lowerer) shape(t *checker.Type, node *ast.Node) *ir.Class {
	if shape, ok := l.shapesByType[t]; ok {
		l.noteRecursion(shape)
		return shape // nil if it failed, which was reported
	}
	shape := &ir.Class{Shape: true}
	l.shapesByType[t] = shape // a recursive type finds itself here
	fields, methods, ok := l.membersMaking(shape, t, node)
	if !ok {
		l.shapesByType[t] = nil
		return nil
	}
	key := membersKey(fields, methods) + l.accessorsKey(t)
	if existing, ok := l.shapesByKey[key]; ok && !l.recursiveTypes[shape] {
		l.shapesByType[t] = existing
		return existing
	}
	shape.Name = "object" + strconv.Itoa(len(l.shapeList)+1)
	for _, method := range methods {
		fields = append(fields, &ir.Field{Name: method.Name, Type: l.funcType(append([]ir.Type{shape}, method.Params...), method.Result), Method: true})
	}
	fields = l.accessorFields(t, shape, fields)
	slices.SortFunc(fields, func(a, b *ir.Field) int { return strings.Compare(a.Name, b.Name) })
	for _, field := range fields {
		field.Class = shape
	}
	shape.Fields = fields
	if _, ok := l.shapesByKey[key]; !ok {
		l.shapesByKey[key] = shape
	}
	l.shapeList = append(l.shapeList, shape)
	return shape
}

// accessorFields replaces the field of each accessor property that an object literal of
// type t declares with the fields that hold its getter's and setter's closures (D-045).
func (l *lowerer) accessorFields(t *checker.Type, shape *ir.Class, fields []*ir.Field) []*ir.Field {
	for _, property := range l.literalAccessors(t) {
		name := propertyName(property) // `@@iterator` for `get [Symbol.iterator]()`, as the field is named
		i := slices.IndexFunc(fields, func(field *ir.Field) bool { return field.Name == name })
		valueType := fields[i].Type
		fields = slices.Delete(fields, i, i+1)
		if ast.GetDeclarationOfKind(property, ast.KindGetAccessor) != nil {
			fields = append(fields, &ir.Field{Name: "get " + name, Type: l.funcType([]ir.Type{shape}, valueType), Method: true, Kind: ir.Getter, Property: name})
		}
		if ast.GetDeclarationOfKind(property, ast.KindSetAccessor) != nil {
			fields = append(fields, &ir.Field{Name: "set " + name, Type: l.funcType([]ir.Type{shape, valueType}, ir.Void), Method: true, Kind: ir.Setter, Property: name})
		}
	}
	return fields
}

// literalAccessors are the properties of an object type that an object literal declares
// with a getter or setter.
func (l *lowerer) literalAccessors(t *checker.Type) []*ast.Symbol {
	var accessors []*ast.Symbol
	for _, property := range l.checker.GetPropertiesOfType(t) {
		if property.Flags&ast.SymbolFlagsAccessor != 0 && property.Declarations[0].Parent.Kind == ast.KindObjectLiteralExpression {
			accessors = append(accessors, property)
		}
	}
	return accessors
}

// literalMemberName is the name of the field of an object literal's member that isn't
// named by a computed constant: `@@iterator` for `[Symbol.iterator]`, as memberName gives.
func (l *lowerer) literalMemberName(member *ast.Node) string {
	if isIteratorName(member.Name()) {
		return memberName(member)
	}
	return l.checker.GetSymbolAtLocation(member.Name()).Name
}

// accessorsKey tells a shape whose objects have accessor properties from one whose
// objects hold the same properties as values, and a getter alone from a getter and a
// setter (a literal with both took the shape of one with the getter, without the setter's
// field).
func (l *lowerer) accessorsKey(t *checker.Type) string {
	var key strings.Builder
	for _, property := range l.literalAccessors(t) {
		getter := ast.GetDeclarationOfKind(property, ast.KindGetAccessor) != nil
		setter := ast.GetDeclarationOfKind(property, ast.KindSetAccessor) != nil
		fmt.Fprintf(&key, "get %t set %t %q;", getter, setter, propertyName(property))
	}
	return key.String()
}

// interfaceOf returns the interface an object type is, as the type of a binding: object
// types with the same members of the same types are one interface, except that a
// recursive type is one of its own. It reports (at node) why a type can't be one and
// returns nil.
func (l *lowerer) interfaceOf(t *checker.Type, node *ast.Node) *ir.Interface {
	if iface, ok := l.interfacesByType[t]; ok {
		l.noteRecursion(iface)
		return iface // nil if it failed, which was reported
	}
	iface := &ir.Interface{}
	l.interfacesByType[t] = iface
	fields, methods, ok := l.membersMaking(iface, t, node)
	if !ok {
		l.interfacesByType[t] = nil
		return nil
	}
	key := membersKey(fields, methods)
	if existing, ok := l.interfacesByKey[key]; ok && !l.recursiveTypes[iface] {
		l.interfacesByType[t] = existing
		return existing
	}
	iface.Fields, iface.Methods = fields, methods
	if _, ok := l.interfacesByKey[key]; !ok {
		l.interfacesByKey[key] = iface
	}
	l.interfaceList = append(l.interfaceList, iface)
	return iface
}

// membersMaking is objectMembers for made, the shape or interface of t being made. The types
// of t's members may refer to made (`left: Expr` in a member of the union Expr); then
// made stays a type of its own, though another type has the same members (`add` and
// `mul` members of the same fields), since those references hold it.
func (l *lowerer) membersMaking(made ir.Type, t *checker.Type, node *ast.Node) ([]*ir.Field, []*ir.InterfaceMethod, bool) {
	l.typesInProgress[made] = true
	defer delete(l.typesInProgress, made)
	return l.objectMembers(t, node)
}

// noteRecursion records that a shape or interface was found while it was being made.
func (l *lowerer) noteRecursion(t ir.Type) {
	if t != nil && l.typesInProgress[t] {
		l.recursiveTypes[t] = true
	}
}

// objectMembers makes a field (of no class yet) for each property of an object type and
// an interface method for each of its methods, both in name order, or reports a member it
// can't hold and returns false. A property may be declared with accessors, or be optional
// (D-065).
func (l *lowerer) objectMembers(t *checker.Type, node *ast.Node) ([]*ir.Field, []*ir.InterfaceMethod, bool) {
	var fields []*ir.Field
	var methods []*ir.InterfaceMethod
	for _, property := range l.checker.GetPropertiesOfType(t) {
		optional := property.Flags&ast.SymbolFlagsOptional != 0
		switch {
		case optional && property.Flags&ast.SymbolFlagsProperty == 0:
			l.notSupportedYet(node, fmt.Sprintf("optional methods and accessors (`%s?` in %s) are", property.Name, l.checker.TypeToString(t)))
			return nil, nil, false
		case isArrayIndex(property.Name):
			l.notSupportedYet(node, fmt.Sprintf("numeric property names (`%s` in %s) are", property.Name, l.checker.TypeToString(t)))
			return nil, nil, false
		case property.Flags&ast.SymbolFlagsAccessor == ast.SymbolFlagsSetAccessor && property.Declarations[0].Parent.Kind != ast.KindObjectLiteralExpression:
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
		if optional {
			fieldType = l.withUndefined(fieldType)
		}
		fields = append(fields, &ir.Field{Name: propertyName(property), Type: fieldType, Optional: optional})
	}
	slices.SortFunc(fields, func(a, b *ir.Field) int { return strings.Compare(a.Name, b.Name) })
	slices.SortFunc(methods, func(a, b *ir.InterfaceMethod) int { return strings.Compare(a.Name, b.Name) })
	return fields, methods, true
}

// propertyType is the IR type of a property of an object type: the numeric type the
// semantic pass gave its declaration (its getter's result, for an accessor property),
// or the type tsc gives it.
func (l *lowerer) propertyType(property *ast.Symbol, node *ast.Node) ir.Type {
	if property.CheckFlags&ast.CheckFlagsInstantiated != 0 {
		return l.irType(l.typeOfSymbol(property), node) // a property of Box<string>, whose declaration has Box<T>'s types
	}
	if getter := ast.GetDeclarationOfKind(property, ast.KindGetAccessor); getter != nil {
		return l.resultType(getter)
	}
	if declaration := property.ValueDeclaration; declaration != nil && declaration.Name() != nil {
		if t := l.numericType(declaration.Name()); t != nil {
			return t
		}
	}
	return l.irType(l.typeOfSymbol(property), node)
}

// interfaceMethod makes the interface method for a method of an object type, or reports
// (at node, or at a parameter) why it can't and returns nil.
func (l *lowerer) interfaceMethod(property *ast.Symbol, t *checker.Type, node *ast.Node) *ir.InterfaceMethod {
	declarations := property.Declarations
	if len(declarations) != 1 {
		l.notSupportedYet(node, fmt.Sprintf("overloaded methods (`%s` in %s) are", property.Name, l.checker.TypeToString(t)))
		return nil
	}
	if typeParameters := l.checker.GetSignatureFromDeclaration(declarations[0]).TypeParameters(); len(typeParameters) > 0 {
		outer := l.subst
		l.subst = l.erasing(typeParameters, nil) // a generic method's signature is its erased copy's (D-078)
		defer func() { l.subst = outer }()
		l.wantInMethodTables(property.Name)
	}
	if property.CheckFlags&ast.CheckFlagsInstantiated != 0 { // a method of Stack<string>, whose declaration has Stack<T>'s types
		ft := l.signatureType(l.checker.GetSignaturesOfType(l.typeOfSymbol(property), checker.SignatureKindCall)[0], node)
		if ft == nil {
			return nil
		}
		return &ir.InterfaceMethod{Name: propertyName(property), Params: ft.Params, Result: ft.Result}
	}
	method := &ir.InterfaceMethod{Name: propertyName(property)}
	var ok bool
	if method.Params, ok = l.methodParamTypes(declarations[0].Parameters()); !ok {
		return nil
	}
	if method.Result = l.resultType(declarations[0]); method.Result == nil {
		return nil
	}
	return method
}

// methodParamTypes are the types of a method's parameters, params, as an interface method
// takes them: a rest parameter's array as a Rest (D-057), a rest parameter of a tuple type
// as its elements, one each, as `parameter` makes them (`...args: [string, number]`), and
// a given flag after a parameter that is optional or has a default (D-055); ok is false if
// one fails (reported).
func (l *lowerer) methodParamTypes(params []*ast.Node) (types []ir.Type, ok bool) {
	for _, param := range params {
		paramType := l.parameterType(param)
		if paramType == nil {
			return nil, false
		}
		if param.AsParameterDeclaration().DotDotDotToken != nil {
			switch t := paramType.(type) {
			case *ir.Array:
				paramType = l.restOf(t)
			case *ir.Class:
				for _, field := range t.Fields {
					types = append(types, field.Type)
				}
				continue
			default:
				l.notSupportedYet(param, fmt.Sprintf("rest parameters of type %s are", paramType))
				return nil, false
			}
		}
		types = append(types, paramType)
		if param.Initializer() != nil || param.AsParameterDeclaration().QuestionToken != nil {
			types = append(types, ir.Given) // D-055
		}
	}
	return types, true
}

// membersKey identifies a shape or interface by its members' names and types.
func membersKey(fields []*ir.Field, methods []*ir.InterfaceMethod) string {
	var key strings.Builder
	for _, field := range fields {
		marks := ""
		if field.Optional {
			marks = "?"
		}
		if field.Mixed {
			marks += "~"
		}
		fmt.Fprintf(&key, "%q%s:%s;", field.Name, marks, typeKey(field.Type))
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

// typeKey identifies an IR type in membersKey: a class, interface or any type the
// lowering makes one of (unionOf, mapOf) by its identity, since a recursive type prints
// differently while its members are being made.
func typeKey(t ir.Type) string {
	switch t.(type) {
	case *ir.Class, *ir.Interface, *ir.Array, *ir.FuncType, *ir.Rest, *ir.Union, *ir.MapType:
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
// its properties in the order the literal writes them, or a hash-map object if its
// contextual type or its own is one (D-059).
func (l *lowerer) objectLiteral(node *ast.Node) ir.Expr {
	if t := l.recordContext(node); t != nil {
		mt, _ := l.recordType(t, node)
		if mt == nil {
			return nil // reported
		}
		return l.recordLiteral(node, mt)
	}
	shape := l.literalShape(l.literalType(node), node)
	if shape == nil {
		return nil
	}
	properties := node.AsObjectLiteralExpression().Properties.Nodes
	if slices.ContainsFunc(properties, isSpreadAssignment) {
		return l.spreadObjectLiteral(properties, shape, node)
	}
	fields := make([]*ir.Field, len(properties))
	values := make([]ir.Expr, len(properties))
	for i, property := range properties {
		var value ir.Expr
		switch property.Kind {
		case ast.KindPropertyAssignment:
			value = l.expression(property.Initializer())
		case ast.KindShorthandPropertyAssignment:
			value = l.shorthandValue(property)
		case ast.KindMethodDeclaration:
			if isIteratorName(property.Name()) || property.Name().Kind != ast.KindComputedPropertyName {
				value = l.objectMethod(property, shape.Field(l.literalMemberName(property)))
			}
		case ast.KindGetAccessor, ast.KindSetAccessor:
			if isIteratorName(property.Name()) || property.Name().Kind != ast.KindComputedPropertyName {
				kind := ir.Getter
				if property.Kind == ast.KindSetAccessor {
					kind = ir.Setter
				}
				fields[i] = shape.AccessorField(l.literalMemberName(property), kind)
				value = l.objectMethod(property, fields[i])
			}
		}
		var name string
		if isIteratorName(property.Name()) {
			name = memberName(property)
		} else if property.Name().Kind == ast.KindComputedPropertyName {
			constant, ok := l.constantName(property.Name())
			if !ok || shape.Field(constant) == nil {
				l.notSupportedYet(property.Name(), "computed property names are")
				return nil
			}
			name = constant // `{ [Color.Red]: 1 }` (D-089)
		}
		if value == nil {
			return nil
		}
		if fields[i] == nil && name != "" {
			fields[i] = shape.Field(name)
		} else if fields[i] == nil {
			fields[i] = shape.Field(l.checker.GetSymbolAtLocation(property.Name()).Name)
		}
		if values[i] = l.propertyValue(value, fields[i], property); values[i] == nil {
			return nil
		}
	}
	fields, values = l.withLeftOut(l.literalType(node), shape, fields, values)
	return &ir.NewObject{Shape: shape, Order: shape.Order(fields), Values: values}
}

// literalShape is the shape of the objects a literal of type t makes: an iterator
// result's (D-109), or t's.
func (l *lowerer) literalShape(t *checker.Type, node *ast.Node) *ir.Class {
	if _, ok := l.iteratorResultValue(t); ok {
		shape, _ := l.irType(t, node).(*ir.Class)
		return shape
	}
	return l.shape(t, node)
}

func isSpreadAssignment(node *ast.Node) bool { return node.Kind == ast.KindSpreadAssignment }

// propertyValue is value, written by the property assignment or method declaration
// property of an object literal, converted to the field it goes in.
func (l *lowerer) propertyValue(value ir.Expr, field *ir.Field, property *ast.Node) ir.Expr {
	if field.Method && field.Kind == ir.Plain && property.Kind != ast.KindMethodDeclaration {
		return l.functionAsMethod(value, field, property)
	}
	return l.convert(value, field.Type, property)
}

// constantName is the property name a computed name `[e]` gives when tsc types e as one
// string or number literal (an enum member, a constant): JS's string of its value.
func (l *lowerer) constantName(name *ast.Node) (string, bool) {
	t := l.typeAt(name.Expression())
	switch {
	case t.Flags()&checker.TypeFlagsStringLiteral != 0:
		return t.AsLiteralType().Value().(string), true
	case t.Flags()&checker.TypeFlagsNumberLiteral != 0:
		return t.AsLiteralType().Value().(jsnum.Number).String(), true
	}
	return "", false
}

// spreadProperty is a property of an object literal with spreads: a spread's object and
// the names it copies, or a property written with its field and value.
type spreadProperty struct {
	source ir.Expr
	names  []string
	of     ir.Type // the type a spread's diagnostics name, if not its source's (copiedNames)
	field  *ir.Field
	value  ir.Expr
	node   *ast.Node
}

// spreadObjectLiteral lowers an object literal with spreads (`{ ...o, x: 1 }`, D-058): an
// object of its shape whose properties come in the order JS defines them: each spread's
// in the order it copies them (spreadNames), and each property written, where a later
// value of a property replaces the earlier one in its place.
func (l *lowerer) spreadObjectLiteral(properties []*ast.Node, shape *ir.Class, node *ast.Node) ir.Expr {
	keyed := len(properties) // the computed properties at the end whose keys are found at run time
	for keyed > 0 && l.isKeyedProperty(properties[keyed-1], shape) {
		keyed--
	}
	parts := make([]spreadProperty, keyed)
	for i, property := range properties[:keyed] {
		part := &parts[i]
		part.node = property
		switch property.Kind {
		case ast.KindSpreadAssignment:
			if part.source = l.expression(property.Expression()); part.source == nil {
				return nil
			}
			var ok bool
			if part.names, part.of, ok = l.copiedNames(part.source, property.Expression(), property); !ok {
				return nil
			}
		case ast.KindPropertyAssignment, ast.KindShorthandPropertyAssignment:
			name := ""
			if property.Name().Kind == ast.KindComputedPropertyName {
				constant, ok := l.constantName(property.Name())
				if !ok || shape.Field(constant) == nil {
					l.notSupportedYet(property.Name(), "computed property names are")
					return nil
				}
				name = constant
			} else {
				name = l.checker.GetSymbolAtLocation(property.Name()).Name
			}
			part.field = shape.Field(name)
			if property.Kind == ast.KindPropertyAssignment {
				part.value = l.expression(property.Initializer())
			} else {
				part.value = l.shorthandValue(property)
			}
			if part.value == nil {
				return nil
			}
			if part.value = l.propertyValue(part.value, part.field, property); part.value == nil {
				return nil
			}
		default:
			l.notSupportedYet(property, "methods and accessors in an object literal with a spread are")
			return nil
		}
	}
	object := l.spreadObject(parts, shape, node)
	if object == nil || keyed == len(properties) {
		return object
	}
	return l.withKeyedProperties(object, properties[keyed:])
}

// isKeyedProperty reports whether property of an object literal of the given shape has a
// computed name whose type is a union of string literals that each name a field of the
// shape (`[op.kind]: …` for a `Record<Kind, number>`).
func (l *lowerer) isKeyedProperty(property *ast.Node, shape *ir.Class) bool {
	if property.Kind != ast.KindPropertyAssignment || property.Name().Kind != ast.KindComputedPropertyName {
		return false
	}
	if _, ok := l.constantName(property.Name()); ok {
		return false
	}
	key := l.typeAt(property.Name().Expression())
	if key.Flags()&checker.TypeFlagsUnion == 0 {
		return false
	}
	for _, k := range key.Types() {
		if k.Flags()&checker.TypeFlagsStringLiteral == 0 || shape.Field(k.AsLiteralType().Value().(string)) == nil {
			return false
		}
	}
	return true
}

// withKeyedProperties is object, made from the properties of its literal before keyed
// (isKeyedProperty), with each of those written in turn by the key it computes, a checked
// write found at run time (D-089). Every field exists already, so JS keeps their order.
func (l *lowerer) withKeyedProperties(object ir.Expr, keyed []*ast.Node) ir.Expr {
	local := l.newLocal("object", object.Type())
	var result ir.Expr = &ir.LocalRef{Local: local}
	for i := len(keyed) - 1; i >= 0; i-- {
		key, value := l.valueExpression(keyed[i].Name().Expression()), l.expression(keyed[i].Initializer())
		if key == nil || value == nil {
			return nil
		}
		if l.firstDynamicWrite == nil {
			l.firstDynamicWrite = keyed[i]
		}
		write := &ir.CallBuiltin{Builtin: ir.DynamicSet, Args: []ir.Expr{box(&ir.LocalRef{Local: local}), box(key), box(value)}}
		result = &ir.Sequence{X: write, Y: result}
	}
	return &ir.Bind{Local: local, Value: object, In: result}
}

// spreadObject makes the object of an object literal with spreads from its properties,
// evaluated in the order the literal writes them: an impure value or spread object goes
// into a local first, and so do a spread's property reads when anything impure follows,
// since it could change them.
func (l *lowerer) spreadObject(parts []spreadProperty, shape *ir.Class, node *ast.Node) ir.Expr {
	lastImpure := -1
	for i, part := range parts {
		if part.source != nil && !isPure(part.source) || part.value != nil && !isPure(part.value) {
			lastImpure = i
		}
	}
	var locals []*ir.Local
	var bound []ir.Expr
	bind := func(name string, value ir.Expr) ir.Expr {
		local := l.newLocal(name, value.Type())
		locals, bound = append(locals, local), append(bound, value)
		return &ir.LocalRef{Local: local}
	}
	var orderOf ir.Expr // the first spread's source, if it gives every property (D-108)
	var order []*ir.Field
	values := map[*ir.Field]ir.Expr{}
	set := func(field *ir.Field, value ir.Expr) {
		if _, ok := values[field]; !ok {
			order = append(order, field)
		}
		values[field] = value
	}
	lastWritten := map[*ir.Field]int{} // the last property written for each field
	for i, part := range parts {
		if part.source == nil {
			lastWritten[part.field] = i
		}
	}
	for i, part := range parts {
		if part.source == nil {
			if !isPure(part.value) {
				part.value = bind("value", part.value)
			}
			set(part.field, part.value)
			continue
		}
		source := part.source
		if !isPure(source) {
			source = bind("spread", source)
		}
		if i == 0 && len(part.names) == len(shape.Fields) && isObject(source.Type()) && !isArray(source.Type()) {
			orderOf = source // later properties only replace values: the copy lists them in the source's order
		}
		for _, name := range part.names {
			field := shape.Field(name)
			read := propertyOf(source, name)
			if read == nil && field != nil && isObjectOrUnionOfObjects(source.Type()) {
				read = l.missingProperty(source, name) // one a narrowing added (C222, C223)
			}
			if field == nil || read == nil {
				of := part.of
				if of == nil {
					of = source.Type()
				}
				l.notSupportedYet(part.node, fmt.Sprintf("spreading the property `%s` of a %s is", name, of))
				return nil
			}
			if last, ok := lastWritten[field]; ok && last > i {
				set(field, nil) // a later property replaces it, whatever its type (`{ ...page, items: [] }`): only its place in the order counts
				continue
			}
			if earlier, ok := values[field]; ok && isOptionalRead(read) {
				read = l.spreadOver(read.(*ir.FieldRef), earlier, field, part.node)
			} else {
				read = l.convert(copiedRead(read, field), field.Type, part.node)
			}
			if read == nil {
				return nil
			}
			if i < lastImpure {
				read = bind(name, read)
			}
			set(field, read)
		}
	}
	objectValues := make([]ir.Expr, len(order))
	for i, field := range order {
		objectValues[i] = values[field]
	}
	order, objectValues = l.withLeftOut(l.literalType(node), shape, order, objectValues)
	if len(order) != len(shape.Fields) {
		l.notSupportedYet(node, "an object literal whose spreads don't give every property is")
		return nil
	}
	var object ir.Expr = &ir.NewObject{Shape: shape, Order: shape.Order(order), Values: objectValues, OrderOf: orderOf}
	for i := len(locals) - 1; i >= 0; i-- {
		object = &ir.Bind{Local: locals[i], Value: bound[i], In: object}
	}
	return object
}

// recordContext is the hash-map object type an object literal makes a hash-map object of:
// its contextual type, its context's one hash-map member (recordMemberOfContext) or its own
// type, the first that is one; nil if none is.
func (l *lowerer) recordContext(node *ast.Node) *checker.Type {
	for _, t := range []*checker.Type{l.literalContext(node), l.recordMemberOfContext(node), l.typeAt(node)} {
		if t != nil && (l.hasOnlyNumericKeys(t) || l.hasIndexSignatures(t)) {
			return t
		}
	}
	return nil
}

// literalType is the object type an object literal makes an object of: its contextual
// type if that is an object type with the same properties but for optional ones the
// literal leaves out (D-065), so that `{ from: pixel }` written for a `Segment` holds a
// `Point`, or for a union of object types, the one member it fits that way (D-067); and
// otherwise the literal's own type.
func (l *lowerer) literalType(node *ast.Node) *checker.Type {
	own := l.typeAt(node)
	contextual := l.literalContext(node)
	if contextual == nil {
		contextual = l.impliedType(node)
	}
	if contextual == nil || len(l.literalAccessors(own)) > 0 {
		return own // a literal's accessors make objects of a shape of their own (D-045)
	}
	if contextual.Flags()&checker.TypeFlagsUnion == 0 {
		if l.fitsLiteral(own, contextual) {
			return contextual
		}
		return own
	}
	var fitting *checker.Type
	for _, member := range contextual.Types() {
		if l.fitsLiteral(own, member) && l.checker.IsTypeAssignableTo(own, member) {
			if fitting != nil {
				return own // more than one
			}
			fitting = member
		}
	}
	if fitting == nil {
		return own
	}
	return fitting
}

// fitsLiteral reports whether an object literal of type own can make an object of the
// object type t: t has every property own has, and own every required property t has.
func (l *lowerer) fitsLiteral(own *checker.Type, t *checker.Type) bool {
	if _, isIteratorResult := l.iteratorResultValue(t); !l.isObjectType(t) && !isIteratorResult {
		return false
	}
	for _, property := range l.checker.GetPropertiesOfType(own) {
		if l.checker.GetPropertyOfType(t, property.Name) == nil {
			return false
		}
	}
	for _, property := range l.checker.GetPropertiesOfType(t) {
		if property.Flags&ast.SymbolFlagsOptional == 0 && l.checker.GetPropertyOfType(own, property.Name) == nil {
			return false
		}
	}
	return true
}

// literalContext is the contextual type of an object literal without null and undefined:
// a literal written for `Point | null` makes a Point.
func (l *lowerer) literalContext(node *ast.Node) *checker.Type {
	contextual := l.contextualType(node)
	if contextual == nil {
		contextual = l.inferredResult(node)
	}
	if contextual == nil {
		return nil
	}
	return l.checker.GetNonNullableType(contextual)
}

// recordMemberOfContext is, where node goes into a union with a class among its members
// (Fetch's HeadersInit, `Headers | Record<string, string> | [string, string][]`), its one
// hash-map object member, if the literal's type is assignable to that and to no other
// object member: a literal can't make the class's instances. Else nil; other unions with
// hash-map members (recursive JSON types) make shapes (D-089).
func (l *lowerer) recordMemberOfContext(node *ast.Node) *checker.Type {
	context := l.literalContext(node)
	if context == nil || context.Flags()&checker.TypeFlagsUnion == 0 || !slices.ContainsFunc(context.Types(), func(member *checker.Type) bool {
		return member.Flags()&checker.TypeFlagsObject != 0 && member.ObjectFlags()&checker.ObjectFlagsClass != 0
	}) {
		return nil
	}
	literal := l.typeAt(node)
	var record *checker.Type
	for _, member := range context.Types() {
		if member.Flags()&checker.TypeFlagsObject == 0 || !l.checker.IsTypeAssignableTo(literal, member) {
			continue
		}
		if !l.hasIndexSignatures(member) || record != nil {
			return nil // an object type it may be, or a second record
		}
		record = member
	}
	return record
}

// inferredResult is the result type tsc inferred for the function that returns node (`return
// node`, an arrow's body, or a branch of a conditional there) if the function declares
// none, or nil. tsc infers the union of the literals it returns, each with the others'
// properties added as optional (`{ tag: string; reason?: undefined }`), so each literal
// makes an object of its member of it.
func (l *lowerer) inferredResult(node *ast.Node) *checker.Type {
	for node.Parent.Kind == ast.KindParenthesizedExpression || node.Parent.Kind == ast.KindConditionalExpression && node.Parent.AsConditionalExpression().Condition != node {
		node = node.Parent
	}
	var fn *ast.Node
	switch parent := node.Parent; {
	case parent.Kind == ast.KindReturnStatement:
		fn = ast.GetContainingFunction(parent)
	case parent.Kind == ast.KindArrowFunction && parent.Body() == node:
		fn = parent
	}
	if fn == nil || fn.Type() != nil {
		return nil
	}
	return l.returnTypeOf(l.checker.GetSignatureFromDeclaration(fn))
}

// impliedType is the type that a literal without a contextual type takes from the
// literal it is an element or a property value of, or nil (D-089). tsc types
// `[{ tags: ["a"] }, { tags: [] }]` as a `{ tags: string[] }[]`, though the second
// element alone is a `{ tags: never[] }`: each element makes a value of the array's
// element type, and each property value one of that property's type, at any depth.
func (l *lowerer) impliedType(node *ast.Node) *checker.Type {
	parent := node.Parent
	switch {
	case parent.Kind == ast.KindArrayLiteralExpression:
		outer := l.literalTarget(parent)
		if l.checker.IsArrayType(outer) {
			return l.checker.GetElementTypeOfArrayType(outer)
		}
	case parent.Kind == ast.KindPropertyAssignment && parent.Initializer() == node:
		outer := l.literalTarget(parent.Parent)
		if property := l.checker.GetPropertyOfType(outer, parent.Name().Text()); property != nil && l.isObjectType(outer) {
			return l.checker.GetNonNullableType(l.typeOfSymbol(property))
		}
	case parent.Kind == ast.KindConditionalExpression && parent.AsConditionalExpression().Condition != node:
		conditional := parent.AsConditionalExpression()
		other := conditional.WhenTrue
		if other == node {
			other = conditional.WhenFalse
		}
		if t := l.typeAt(other); l.isObjectType(t) && ast.SkipParentheses(other).Kind != ast.KindObjectLiteralExpression {
			return t // `c ? {} : options` makes an object of options' type, if it fits (D-172)
		}
	}
	return nil
}

// literalTarget is the type of the value a literal makes: its contextual type, else the
// type implied by the literal it is in (an array type, for an array literal), else its own, widened as a variable's would be
// (`[{ a: 1 }, { b: 2 }]` makes `{ a: number; b?: undefined }`s and `{ a?: undefined; b: number }`s).
func (l *lowerer) literalTarget(node *ast.Node) *checker.Type {
	if node.Kind == ast.KindObjectLiteralExpression {
		return l.literalType(node)
	}
	if contextual := l.contextualType(node); contextual != nil {
		return contextual
	}
	if implied := l.impliedType(node); implied != nil && l.checker.IsArrayType(implied) {
		return implied
	}
	return l.checker.GetWidenedType(l.typeAt(node))
}

// shorthandValue lowers the value of a shorthand property `{ x }`: the binding x.
func (l *lowerer) shorthandValue(property *ast.Node) ir.Expr {
	name := property.Name()
	if x, ok := l.kept[name]; ok {
		return x // read before a yield (D-111)
	}
	symbol := l.followAliases(l.checker.GetShorthandAssignmentValueSymbol(property))
	if x, ok := l.binding(name, symbol); ok {
		if x != nil && isUnion(x.Type()) && l.isLoose(symbol) {
			if t := l.irType(l.typeOfSymbol(symbol), name); t != nil {
				return l.looseRead(x, t, name) // D-090
			}
			return nil
		}
		return l.narrowed(name, x)
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

// isMappedProperty reports whether a property access reads a property of a mapped object
// type (`Partial<Point>`), which has no declaration of its own (D-065), also one that `in`
// added to a class's object (`Animal & Record<"bark", unknown>`, C222).
func (l *lowerer) isMappedProperty(node *ast.Node) bool {
	symbol := l.memberSymbol(node)
	t := l.typeAt(node.Expression())
	return symbol != nil && symbol.CheckFlags&ast.CheckFlagsMapped != 0 && (l.isObjectType(t) || t.Flags()&checker.TypeFlagsIntersection != 0)
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
		if l.program.IsSourceFileDefaultLibrary(file.Path()) {
			return isIteratorResultDeclaration(declaration.Parent) // its objects are shapes (D-109)
		}
		return !lib.IsLibPath(file.FileName())
	}
	return false
}

// shapeProperty lowers the object of `o.p` for a property p of an object type and
// returns it with p's field, or reports why it can't and returns nils. An object that is
// a dynamic value (an unknown that a type guard narrowed to the object type) comes back
// without a field: its property is found at run time.
func (l *lowerer) shapeProperty(node *ast.Node) (ir.Expr, *ir.Field) {
	if l.isUnloweredOptionalChain(node) {
		l.notSupportedYet(node, "optional chaining `?.` is")
		return nil, nil
	}
	object := l.expression(node.Expression())
	if object == nil || object.Type() == ir.Value {
		return object, nil
	}
	name := node.AsPropertyAccessExpression().Name().Text()
	switch t := object.Type().(type) {
	case *ir.Interface:
		return object, t.Field(name)
	case *ir.Class:
		if t.Shape && t.AccessorField(name, ir.Getter) == nil && t.AccessorField(name, ir.Setter) == nil {
			return object, t.Field(name)
		}
		if t.Shape {
			// An accessor property of the object's own shape (`this.p` in its literal's methods)
			// is read and written as through an interface, whose sites call getters and setters.
			iface := l.interfaceOf(l.typeAt(node.Expression()), node)
			if iface == nil || iface.Field(name) == nil {
				return nil, nil
			}
			return &ir.ClassCast{X: object, To: iface}, iface.Field(name)
		}
		if t.Field(name) == nil && t.AccessorField(name, ir.Getter) == nil && t.AccessorField(name, ir.Setter) == nil {
			return object, nil // a property that `in` added to a class's object (C222): found at run time
		}
	case *ir.Union:
		if isUnionOfObjects(t) {
			// An instance's union type argument that a discriminant narrowed through the
			// constraint to a member it doesn't have (`Ci` for `T = Sq | Tr` of `T extends Sq
			// | Ci | Tr`): found at run time (valueProperty), as a lie reaches it.
			return object, nil
		}
	}
	l.notSupportedYet(node, fmt.Sprintf("a property of an object type on a %s is", object.Type()))
	return nil, nil
}

// valueProperty reads `o.p` for an object o that is a dynamic value although tsc gives it
// an object type, or whose type has no p, which a narrowing added (`"p" in o`, a
// discriminant narrowed through a type parameter's constraint): the property found at run
// time, converted to p's type with a check.
func (l *lowerer) valueProperty(node *ast.Node, object ir.Expr) ir.Expr {
	l.wantDynamicMember(node)
	t := l.irType(l.typeAt(node), node)
	if t == nil {
		return nil
	}
	return l.checkedConversion(&ir.CallBuiltin{Builtin: ir.DynamicGet, Args: []ir.Expr{box(object), l.dynamicKey(node)}}, t, node)
}

// objectCast converts value, a reference to an object, to another object type: a class
// (classCast), or an interface whose members the object provides with the same types
// (provides), so that every access through the interface finds a member it can use as is.
func (l *lowerer) objectCast(value ir.Expr, target ir.Type, node *ast.Node) ir.Expr {
	if class, ok := target.(*ir.Class); ok {
		if class.Result {
			return asResult(value, class)
		}
		if _, fromInterface := value.Type().(*ir.Interface); !fromInterface {
			return l.classCast(value, class, node)
		}
		if l.isNarrowedTo(node, class) {
			return downcast(value, class)
		}
	} else if missing := unprovided(value.Type(), target.(*ir.Interface)); missing == "" {
		return l.interfaceCast(value, target.(*ir.Interface), node)
	} else if holdsOptionally(value.Type(), target.(*ir.Interface)) {
		l.relaxedCasts = append(l.relaxedCasts, relaxedCast{from: value.Type(), to: target.(*ir.Interface), node: node, in: l.function})
		return l.interfaceCast(value, target.(*ir.Interface), node)
	} else if l.isReferenceNarrowedTo(node, target) {
		// tsc narrowed it to a type that its value's type doesn't provide: by a type
		// predicate to an intersection (`x` is an `A & B` after `isB(x)` for an `x: A`), or
		// through a type parameter's union constraint to a member that the instance's type
		// isn't (`x` is an Sq after `x.kind === "sq"` for `T extends Sq | Ci`, in the
		// instance for Ci, which only a cast that lied reaches): the value it holds, which
		// converts with a check as a dynamic value does (C223), keeping the object where its
		// layout provides the type.
		return l.checkedConversion(value, target, node)
	} else {
		l.notSupportedYet(node, fmt.Sprintf("using a `%s` as a `%s`, whose `%s` it doesn't provide with the same types, is", value.Type(), target, missing))
		return nil
	}
	l.notSupportedYet(node, fmt.Sprintf("using a `%s` as a `%s` is", value.Type(), target))
	return nil
}

// isReferenceNarrowedTo reports whether node is a variable, property or element that tsc
// types as target there, which objectCast asks where its value's type doesn't provide
// target: a narrowing that the value's IR type doesn't follow.
func (l *lowerer) isReferenceNarrowedTo(node *ast.Node, target ir.Type) bool {
	switch node = ast.SkipParentheses(node); node.Kind {
	case ast.KindIdentifier, ast.KindPropertyAccessExpression, ast.KindElementAccessExpression:
		return l.irType(l.typeAt(node), node) == target
	}
	return false
}

// interfaceCast converts value, a reference to an object whose type provides iface's
// members, to iface: as it is, unless value is an iterator result, whose object may be of
// another layout than its shape's (D-178), and is then converted unless it is one of the
// shape's (ir.ClassCast.FromResult), so that reads through iface find its members as iface
// holds them; nil, reported, if iface can't take such a conversion.
func (l *lowerer) interfaceCast(value ir.Expr, iface *ir.Interface, node *ast.Node) ir.Expr {
	if class, ok := value.Type().(*ir.Class); !ok || !class.Result || iface.Result {
		return &ir.ClassCast{X: value, To: iface}
	}
	if what := l.prepareDynamicTarget(iface, node, map[ir.Type]bool{}); what != "" {
		l.notSupportedYet(node, fmt.Sprintf("using an iterator result, which may be an object of another layout, as a `%s` (%s) is", iface, what))
		return nil
	}
	return &ir.ClassCast{X: value, To: iface, FromResult: true}
}

// convertedResult is x, unless it is an iterator result cast to an interface (a branch of a
// conditional, by commonClass or asContextualInterface), which is then interfaceCast's.
func (l *lowerer) convertedResult(x ir.Expr, node *ast.Node) ir.Expr {
	if cast, ok := x.(*ir.ClassCast); ok && !cast.FromResult {
		if iface, ok := cast.To.(*ir.Interface); ok {
			return l.interfaceCast(cast.X, iface, node)
		}
	}
	return x
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
// type as an interface's. Only an optional property can be left out, so only an optional
// property provides one.
func providesProperty(t ir.Type, property *ir.Field) bool {
	switch from := t.(type) {
	case *ir.Class:
		if field := from.Field(property.Name); field != nil {
			return field.Type == property.Type && (!field.Optional || property.Optional)
		}
		getField, setField := from.AccessorField(property.Name, ir.Getter), from.AccessorField(property.Name, ir.Setter)
		if from.Shape && (getField != nil || setField != nil) {
			return (getField == nil || getField.Type.(*ir.FuncType).Result == property.Type) && (setField == nil || setField.Type.(*ir.FuncType).Params[1] == property.Type)
		}
		getter, setter := from.Method(property.Name, ir.Getter), from.Method(property.Name, ir.Setter)
		return !from.Shape && getter != nil && getter.Func.Result == property.Type && (setter == nil || setter.Func.Params[1].Type == property.Type)
	case *ir.Interface:
		field := from.Field(property.Name)
		return field != nil && field.Type == property.Type && (!field.Optional || property.Optional)
	}
	return false
}

// providesMethod reports whether values of type t have a method of the same name and
// signature as an interface's.
func providesMethod(t ir.Type, method *ir.InterfaceMethod) bool {
	switch from := t.(type) {
	case *ir.Class:
		if from.Shape {
			field := from.Field(method.Name)
			if field == nil || !field.Method {
				return false
			}
			ft := field.Type.(*ir.FuncType)
			return ft.Result == method.Result && slices.Equal(ft.Params[1:], method.Params)
		}
		own := from.Method(method.Name, ir.Plain)
		if own == nil || own.Func.Result != method.Result || len(own.Func.Params) > len(method.Params)+1 {
			return false
		}
		for i, param := range own.Func.Params[1:] { // fewer parameters are called through the method's adapter
			if param.Type != method.Params[i] {
				return false
			}
		}
		n := len(own.Func.Params) - 1
		return n == len(method.Params) || method.Params[n] != ir.Given // not a parameter the interface may leave out that the method requires
	case *ir.Interface:
		return from.Method(method.Name) != nil && slices.Equal(from.Method(method.Name).Params, method.Params) && from.Method(method.Name).Result == method.Result
	}
	return false
}

// interfaceMethodCall lowers `o.m(args)` for a method m of an object type: a call of the
// method that the class of the object o refers to has, found at run time.
func (l *lowerer) interfaceMethodCall(call *ast.CallExpression) ir.Expr {
	callee := ast.SkipParentheses(call.Expression)
	if l.isUnloweredOptionalChain(callee) {
		l.notSupportedYet(callee, "optional chaining `?.` is")
		return nil
	}
	receiver := l.expression(callee.Expression())
	if receiver == nil {
		return nil
	}
	name := l.calleeName(callee) // `m["make"]`'s too (C185)
	if shape, ok := receiver.Type().(*ir.Class); ok && shape.Shape && shape.Field(name) != nil && shape.Field(name).Method {
		field := shape.Field(name)
		ft := field.Type.(*ir.FuncType)
		if x, ok := l.namedInstanceCall(call, receiver, name, shape, ft.Params[1:], ft.Result, false, func(object ir.Expr, args []ir.Expr) ir.Expr {
			return &ir.CallValue{Callee: &ir.FieldRef{X: object, Field: field}, Args: append([]ir.Expr{object}, args...)}
		}); ok {
			return x // the instance its method's closure lists, if it lists one (D-191)
		}
		args := l.typedArguments(call.Arguments.Nodes, ft.Params[1:])
		if args == nil {
			return nil
		}
		result := l.withReceiver(receiver, func(object ir.Expr) ir.Expr {
			return &ir.CallValue{Callee: &ir.FieldRef{X: object, Field: shape.Field(name)}, Args: append([]ir.Expr{object}, args...)}
		})
		if ft.Result == ir.Value {
			return l.fromErased(result, call.AsNode()) // a generic method's result (D-078)
		}
		return result
	}
	iface, ok := receiver.Type().(*ir.Interface)
	if !ok || iface.Method(name) == nil {
		l.notSupportedYet(callee, fmt.Sprintf("calling a method of an object type on a %s is", receiver.Type()))
		return nil
	}
	method := iface.Method(name)
	if x, ok := l.namedInstanceCall(call, receiver, name, nil, method.Params, method.Result, false, func(object ir.Expr, args []ir.Expr) ir.Expr {
		return &ir.InterfaceCall{Method: method, Args: append([]ir.Expr{object}, args...)}
	}); ok {
		return x // the instance its object's class or method closure lists, if it lists one (D-191)
	}
	args := l.typedArguments(call.Arguments.Nodes, method.Params)
	if args == nil {
		return nil
	}
	result := ir.Expr(&ir.InterfaceCall{Method: method, Args: append([]ir.Expr{receiver}, args...)})
	if method.Result == ir.Value {
		return l.fromErased(result, call.AsNode()) // a generic method's result (D-078)
	}
	return result
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
	for _, shape := range l.shapeList {
		for _, field := range shape.Fields {
			if field.Kind != ir.Plain {
				accessors[field.Property] = true
			}
		}
	}
	for _, iface := range l.interfaceList {
		for _, property := range iface.Fields {
			property.Accessor = accessors[property.Name]
		}
	}
}
