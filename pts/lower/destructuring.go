package lower

import (
	"fmt"
	"math"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/internal/scanner"
	"github.com/microsoft/typescript-go/pts/ir"
)

// Destructuring (D-054) holds the value in a local and binds each name of the pattern
// to a part of it: an object's property (a field, or a class's getter), a tuple's element
// (a field), or an array's element (a checked read, D-015, unless the element has a
// default for when the array is too short), recursively for nested patterns.

// parameterBinding is a parameter that its function's body starts by binding: one with
// a default, which value takes if the call left the argument out or passed undefined
// (given is false), and arg otherwise (D-055); a rest parameter, whose array arg passes to
// value (D-057); or one written as a pattern, which the body destructures from value (the
// argument itself, if the parameter has no default and isn't a rest parameter).
type parameterBinding struct {
	node     *ast.Node // the parameter declaration
	value    *ir.Local
	arg      *ir.Local   // with a default or for a rest parameter
	given    *ir.Local   // with a default
	elements []*ir.Local // for a rest parameter of a tuple type: its elements' parameters
}

// destructure binds the names of a binding pattern to the parts of value, declaring their
// locals. It reports what it can't lower and returns false.
func (l *lowerer) destructure(pattern *ast.Node, value ir.Expr, at ir.Pos) ([]ir.Stmt, bool) {
	reports := l.reports
	stmts, ok := l.destructureParts(pattern, value, at)
	if !ok && l.reports == reports && l.unexplained == nil {
		l.noteUnexplained(pattern) // reported at the end if nothing else is (unexplainedFailure), never dropped silently
	}
	return stmts, ok
}

func (l *lowerer) destructureParts(pattern *ast.Node, value ir.Expr, at ir.Pos) ([]ir.Stmt, bool) {
	if names := namesOfTuple(pattern, value); names != nil {
		return l.bindTupleValues(names, value.(*ir.NewObject).Values)
	}
	switch {
	case pattern.Kind != ast.KindArrayBindingPattern:
	case isMap(value.Type()):
		value = &ir.CallBuiltin{Builtin: ir.ValueToArray, Args: []ir.Expr{box(value), &ir.StringConst{Value: patternSource(pattern)}}} // a set's elements, a map's entries (D-110)
	case isIterablePatternSource(value.Type()):
		return l.destructureIterable(pattern, value, at) // its items (D-078), or its iterator's (D-111), as the pattern takes them
	case value.Type() == ir.String:
		value = &ir.CallBuiltin{Builtin: ir.ArrayFromString, Args: []ir.Expr{value}} // its code points
	}
	whole := l.newLocal("destructured", value.Type())
	stmts := []ir.Stmt{&ir.LocalDecl{At: at, Local: whole, Init: value}}
	for i, element := range pattern.AsBindingPattern().Elements.Nodes {
		if element.Kind == ast.KindOmittedExpression || element.Name() == nil {
			continue // an elision: `[a, , c]`
		}
		binding := element.AsBindingElement()
		var part ir.Expr
		if pattern.Kind == ast.KindObjectBindingPattern {
			if part = l.propertyPart(&ir.LocalRef{Local: whole}, element, binding.PropertyName, binding.DotDotDotToken != nil); part != nil {
				part = l.withDefault(part, binding.Initializer, binding.Name())
			}
		} else {
			part = l.elementPart(&ir.LocalRef{Local: whole}, element, i, binding.Initializer, binding.DotDotDotToken != nil)
		}
		if part == nil {
			return nil, false
		}
		bound, ok := l.bindElement(element, part)
		if !ok {
			return nil, false
		}
		stmts = append(stmts, bound...)
	}
	return stmts, true
}

// destructureIterable binds the names of an array pattern to what an iterable value
// yields, as JS's IteratorBindingInitialization does (D-109, H03): one next() per
// element, elisions included, until the iterator is done (undefined after), each element
// bound, with its default or by its nested pattern, before the next is stepped; a rest
// element takes all that is left. The iterator is closed at the end unless it is done,
// and if binding throws (a default, a nested pattern, a checked conversion), it is closed
// before the error goes on, as a loop's is (iteratorLoop); one whose next() threw isn't.
// An array's, typed array's or string's items are read by index instead (ir.IterationOpen).
func (l *lowerer) destructureIterable(pattern *ast.Node, value ir.Expr, at ir.Pos) ([]ir.Stmt, bool) {
	source, index, done := l.newLocal("iteration", ir.Value), l.newLocal("index", ir.Number), l.newLocal("done", ir.Bool)
	ref := func(local *ir.Local) ir.Expr { return &ir.LocalRef{Local: local} }
	set := func(local *ir.Local, value ir.Expr) ir.Stmt {
		return &ir.ExprStmt{At: at, X: &ir.Assign{Local: local, Value: value}}
	}
	stmts := []ir.Stmt{
		&ir.LocalDecl{At: at, Local: source, Init: &ir.CallBuiltin{Builtin: ir.IterationOpen, Args: []ir.Expr{box(value), &ir.StringConst{Value: patternSource(pattern)}}}},
		&ir.LocalDecl{At: at, Local: index, Init: &ir.NumberConst{Value: 0}},
		&ir.LocalDecl{At: at, Local: done, Init: &ir.BoolConst{Value: false}},
	}
	var body []ir.Stmt
	mayThrow := false // whether binding may throw with the iterator open
	for _, element := range pattern.AsBindingPattern().Elements.Nodes {
		if element.Kind == ast.KindBindingElement && element.AsBindingElement().DotDotDotToken != nil {
			remaining, items := l.newLocal("remaining", ir.Bool), l.newLocal("rest", ir.ValueArray)
			body = append(body,
				&ir.LocalDecl{At: at, Local: remaining, Init: &ir.Unary{Op: ir.Not, X: ref(done)}},
				set(done, &ir.BoolConst{Value: true}), // drained, or if next() throws, not closed
				&ir.LocalDecl{At: l.pos(element), Local: items, Init: &ir.CallBuiltin{Builtin: ir.IterationRest, Args: []ir.Expr{ref(source), ref(index), &ir.Unary{Op: ir.Not, X: ref(remaining)}}}})
			bound, ok := l.bindElement(element, ref(items))
			if !ok {
				return nil, false
			}
			body = append(body, bound...)
			continue
		}
		step := []ir.Stmt{
			set(done, &ir.BoolConst{Value: true}), // if next() throws, the iterator isn't closed
			set(done, &ir.CallBuiltin{Builtin: ir.IterationNext, Args: []ir.Expr{ref(source), ref(index)}}),
		}
		body = append(body, &ir.If{At: at, Cond: &ir.Unary{Op: ir.Not, X: ref(done)}, Then: &ir.Block{At: at, Stmts: step}})
		advance := set(index, &ir.Binary{Op: ir.Add, X: ref(index), Y: &ir.NumberConst{Value: 1}})
		if element.Kind == ast.KindOmittedExpression || element.Name() == nil {
			body = append(body, advance) // an elision: `[a, , c]` steps past an item
			continue
		}
		item := l.newLocal("item", ir.Value)
		read := &ir.Conditional{Cond: ref(done), Then: box(&ir.UndefinedConst{}), Else: &ir.CallBuiltin{Builtin: ir.IterationValue, Args: []ir.Expr{ref(source), ref(index)}}}
		body = append(body, &ir.LocalDecl{At: l.pos(element), Local: item, Init: read}, advance)
		initializer := element.AsBindingElement().Initializer
		part := l.withDefault(ref(item), initializer, element.Name())
		if part == nil {
			return nil, false
		}
		bound, ok := l.bindElement(element, part)
		if !ok {
			return nil, false
		}
		mayThrow = mayThrow || initializer != nil || !isPlainBinding(bound, item)
		body = append(body, bound...)
	}
	closeIt := func() []ir.Stmt {
		return []ir.Stmt{&ir.ExprStmt{At: at, X: &ir.CallBuiltin{Builtin: ir.IterationClose, Args: []ir.Expr{ref(source)}}}}
	}
	if !mayThrow || isTypedArray(value.Type()) { // a typed array's items are read by index: there is nothing to close
		return append(append(stmts, body...), &ir.If{At: at, Cond: &ir.Unary{Op: ir.Not, X: ref(done)}, Then: &ir.Block{At: at, Stmts: closeIt()}}), true
	}
	// The names are declared outside the protected code, which only assigns them, so that
	// they stay in scope after it.
	var assigned []ir.Stmt
	for _, stmt := range body {
		if decl, ok := stmt.(*ir.LocalDecl); ok {
			stmts = append(stmts, &ir.LocalDecl{At: decl.At, Local: decl.Local})
			if decl.Init == nil {
				continue
			}
			stmt = &ir.ExprStmt{At: decl.At, X: &ir.Assign{Local: decl.Local, Value: decl.Init}}
		}
		assigned = append(assigned, stmt)
	}
	protected := func() *ir.Block { return &ir.Block{At: at, Stmts: assigned} }
	closing := func(f *finallyContext) []ir.Stmt { return l.iteratorClosing(at, f, done, closeIt) }
	finally := l.withFinally(at, len(l.targets), protected, closing) // a pattern has no jumps; every target is outside
	if finally == nil {
		return nil, false
	}
	return append(stmts, finally...), true
}

// isPlainBinding reports whether an element's binding is one name declared with the item
// itself, a dynamic value: nothing that could throw.
func isPlainBinding(bound []ir.Stmt, item *ir.Local) bool {
	if len(bound) != 1 {
		return false
	}
	decl, ok := bound[0].(*ir.LocalDecl)
	if !ok {
		return false
	}
	init, ok := decl.Init.(*ir.LocalRef)
	return ok && init.Local == item
}

// isIterablePatternSource reports whether an array pattern binds values of type t from
// what their iterator yields (destructureIterable): dynamic values, and classes, shapes
// and interfaces, which tsc lets a pattern take only with a [Symbol.iterator] method.
func isIterablePatternSource(t ir.Type) bool {
	return t == ir.Value || isPlainObject(t)
}

// bindElement binds an element of a pattern to its part: its name, or the names of its
// nested pattern.
func (l *lowerer) bindElement(element *ast.Node, part ir.Expr) ([]ir.Stmt, bool) {
	if element.Name().Kind != ast.KindIdentifier {
		return l.destructure(element.Name(), part, l.pos(element))
	}
	stmt := l.bindName(element.Name(), part, element)
	if stmt == nil {
		return nil, false
	}
	return []ir.Stmt{stmt}, true
}

// patternSource is the source text V8 names in its TypeError for destructuring a value
// that isn't iterable with an array pattern: the initializer the pattern binds, or the
// pattern itself.
func patternSource(pattern *ast.Node) string {
	if parent := pattern.Parent; parent != nil && parent.Initializer() != nil && parent.Name() == pattern {
		return scanner.GetTextOfNode(parent.Initializer())
	}
	return scanner.GetTextOfNode(pattern)
}

// namesOfTuple returns the names of an array pattern that binds a tuple made in place
// (`const [a, b] = [x, y]`, or the [key, value] pair of a map's entry) one name per
// element, or nil for any other pattern or value. Such a pattern needs no tuple: each name
// takes its element's value (a default on a tuple's element never applies, D-054).
func namesOfTuple(pattern *ast.Node, value ir.Expr) []*ast.Node {
	tuple, ok := value.(*ir.NewObject)
	elements := pattern.AsBindingPattern().Elements.Nodes
	if !ok || !tuple.Shape.Tuple || pattern.Kind != ast.KindArrayBindingPattern || len(elements) != len(tuple.Values) {
		return nil
	}
	for _, element := range elements {
		if element.Kind != ast.KindBindingElement || element.Name() == nil || element.Name().Kind != ast.KindIdentifier || element.AsBindingElement().DotDotDotToken != nil {
			return nil
		}
	}
	names := make([]*ast.Node, len(elements))
	for i, element := range elements {
		names[i] = element.Name()
	}
	return names
}

// bindTupleValues binds each of names to the value at its index, in order.
func (l *lowerer) bindTupleValues(names []*ast.Node, values []ir.Expr) ([]ir.Stmt, bool) {
	stmts := make([]ir.Stmt, len(names))
	for i, name := range names {
		if stmts[i] = l.bindName(name, values[i], name.Parent); stmts[i] == nil {
			return nil, false
		}
	}
	return stmts, true
}

// bindName declares the local of a name that a pattern binds (a module-scope binding's
// exists already), initialised with value; nil if it failed (reported).
func (l *lowerer) bindName(name *ast.Node, value ir.Expr, node *ast.Node) ir.Stmt {
	symbol := l.checker.GetSymbolAtLocation(name)
	local, declared := l.locals[symbol]
	if !declared {
		if t := l.bindingType(symbol, name); t != nil {
			local = l.newLocal(name.Text(), t)
		}
		l.locals[symbol] = local
	}
	if local == nil {
		return nil
	}
	if value.Type() == ir.Value && !isTypedElementRead(value) {
		value = l.patternItem(value, local.Type, l.isLoose(symbol), node)
	}
	if value == nil {
		l.locals[symbol] = nil
		return nil
	}
	if value = l.convert(value, local.Type, node); value == nil {
		l.locals[symbol] = nil
		return nil
	}
	return &ir.LocalDecl{At: l.pos(node), Local: local, Init: value}
}

// patternItem is what a local of type t that a pattern binds holds of a dynamic item:
// the item converted once, at the binding, to a type without an in-place run-time check
// (a tuple, array, shape or interface, or a union with one), so that every read shares a
// value of the type's own layout; for a loose local (`T | undefined`, D-090), undefined
// stays undefined. Stored as it was, a map's entry (an array) was copied into a new tuple
// at each read of a loose name, losing its writes and identity, or, narrowed out of a
// union, read as a tuple's fields past the array's memory.
func (l *lowerer) patternItem(item ir.Expr, t ir.Type, loose bool, node *ast.Node) ir.Expr {
	target := t
	if union, ok := t.(*ir.Union); ok && loose {
		target = l.withoutUndefined(union)
	}
	if isCheckable(target) || target == ir.Value || !loose && !isUnion(target) {
		return item // checked at each read, or converted once by bindName
	}
	once := func(x ir.Expr) ir.Expr {
		if !isUnion(target) {
			return l.convert(x, target, node)
		}
		if l.prepareDynamicTarget(target, node, map[ir.Type]bool{}) != "" {
			return &ir.ToUnion{X: x, To: target.(*ir.Union)} // a member dynamic data can't convert to yet (a generator, an iterator): as convert stores it
		}
		return &ir.FromUnion{X: x, To: target}
	}
	if !loose {
		return once(item)
	}
	held := l.newLocal("item", ir.Value)
	ref := &ir.LocalRef{Local: held}
	present := once(ref)
	if present == nil {
		return nil
	}
	if present = l.convert(present, t, node); present == nil {
		return nil
	}
	missing := l.convert(&ir.UndefinedConst{}, t, node)
	if missing == nil {
		return nil
	}
	return &ir.Bind{Local: held, Value: item, In: &ir.Conditional{Cond: &ir.IsNullish{X: ref, Only: ir.Undefined}, Then: missing, Else: present}}
}

// isTypedElementRead reports whether value reads an element of an array whose elements
// have a type, so that it is one of it already, or undefined past the end (D-090).
func isTypedElementRead(value ir.Expr) bool {
	read, ok := value.(*ir.ArrayCall)
	if !ok || read.Op != ir.ArrayElement {
		return false
	}
	array, ok := read.Args[0].Type().(*ir.Array)
	return ok && array.Elem != ir.Value
}

// propertyPart reads from source the property that an element of an object pattern (or
// of an object literal being assigned to) names: property, or the element's own name.
func (l *lowerer) propertyPart(source ir.Expr, element *ast.Node, property *ast.Node, rest bool) ir.Expr {
	if rest {
		return l.restProperties(source, element)
	}
	if property == nil {
		property = element.Name()
	}
	if property.Kind != ast.KindIdentifier && property.Kind != ast.KindStringLiteral {
		l.notSupportedYet(property, "computed property names in object patterns are")
		return nil
	}
	name := property.Text()
	if source.Type() == ir.Value { // an any's property, found at run time (D-070)
		return &ir.CallBuiltin{Builtin: ir.DynamicGet, Args: []ir.Expr{source, box(&ir.StringConst{Value: name})}}
	}
	if t, ok := source.Type().(*ir.MapType); ok && t.Record && t.Value == ir.Value {
		return &ir.MapCall{Op: ir.MapGet, Args: []ir.Expr{source, &ir.StringConst{Value: name}}, Map: t} // a missing key of a Record<string, unknown> is undefined
	}
	if read := propertyOf(source, name); read != nil {
		return read
	}
	if read := lengthOrSize(source, name); read != nil {
		return read
	}
	var sourceType *checker.Type // the destructured object's, for a pattern's generic method (D-194)
	if element.Parent.Kind == ast.KindObjectBindingPattern {
		sourceType = l.typeAt(element.Parent)
	}
	if m, ok := l.objectTypeMethod(source.Type(), name); ok {
		var forward func(*ir.Local) *forwarder
		if sourceType != nil {
			forward = l.objectMethodForwardFrom(element.Name(), sourceType, m, nil)
		}
		return l.boundMethod(property, m, source, nil, name, false, forward) // bound to the object, as `o.m` is (D-044)
	}
	if class, ok := source.Type().(*ir.Class); ok && !class.Shape && sourceType != nil {
		if generic := l.inheritedGenericMethod(class, name); generic != nil && generic.static == nil {
			return l.boundGenericMethod(element.Name(), generic, source) // a class's generic method, as `o.m` is (D-194)
		}
		if classMethod := class.Method(name, ir.Plain); classMethod != nil { // and any other method of a class
			params := make([]ir.Type, len(classMethod.Func.Params)-1)
			for i, param := range classMethod.Func.Params[1:] {
				params[i] = param.Type
			}
			m := method{name, class, params, classMethod.Func.Result, func(object ir.Expr, args []ir.Expr) ir.Expr {
				return l.callMethod(object, element, classMethod, args)
			}}
			return l.boundMethod(property, m, source, nil, name, false, nil)
		}
	}
	if isObjectOrUnionOfObjects(source.Type()) {
		return l.missingProperty(source, name) // one a narrowing added (C222, C223)
	}
	l.notSupportedYet(property, fmt.Sprintf("destructuring the property `%s` of a %s is", name, source.Type()))
	return nil
}

// propertyOf reads the property name of source: a field of a class or shape, a class's
// getter, a field through an interface, or a hash-map object's entry (which throws if
// it has none); nil if it has no such property.
func propertyOf(source ir.Expr, name string) ir.Expr {
	switch t := source.Type().(type) {
	case *ir.MapType:
		if t.Record {
			return &ir.MapCall{Op: ir.MapGetChecked, Args: []ir.Expr{source, &ir.StringConst{Value: name}}, Map: t}
		}
	case *ir.Class:
		if field := t.Field(name); field != nil && !field.Method {
			return &ir.FieldRef{X: source, Field: field}
		}
		if getter := t.Method(name, ir.Getter); getter != nil && !t.Shape {
			if getter.Virtual {
				return &ir.VirtualCall{Method: getter, Args: []ir.Expr{upcast(source, getter.Slot().Class)}}
			}
			return &ir.Call{Func: getter.Func, Args: []ir.Expr{upcast(source, getter.Class)}}
		}
	case *ir.Interface:
		if field := t.Field(name); field != nil {
			return &ir.FieldRef{X: source, Field: field}
		}
	}
	return nil
}

// spreadNames are the names of the properties that spreading source (`...o`, written at
// node) copies, in the order JS copies them (D-058): a class instance's fields in the
// order its constructors make them, or the properties of an object type in the order
// objectType (source's type, or one tsc made from it) lists them; none for an object
// without properties. ok is false if it is no object, or a tuple (reported).
func (l *lowerer) spreadNames(source ir.Expr, objectType *checker.Type, node *ast.Node) (names []string, ok bool) {
	class, isClass := source.Type().(*ir.Class)
	_, isInterface := source.Type().(*ir.Interface)
	if instance := l.anyInstanceIn(objectType); source.Type() == ir.Value && instance != nil {
		class, isClass = l.classOf(instance, node), true // a C<any, …> place's object: any of C's instances, whose own properties are C's fields (H10b)
	}
	switch {
	case isClass && class == nil:
		return nil, false // reported
	case isClass && !class.Shape:
		for _, field := range class.AllFields() {
			if !field.Method && !isPrivateName(field.Name) {
				names = append(names, field.Name)
			}
		}
	case isClass && !class.Tuple || isInterface:
		for _, property := range l.checker.GetPropertiesOfType(objectType) {
			names = append(names, property.Name)
		}
	default:
		l.notSupportedYet(node, fmt.Sprintf("spreading a %s into an object is", source.Type()))
		return nil, false
	}
	return names, true
}

// restProperties is the object a rest element of an object pattern (`...others`, element)
// takes from source: a new object of its type's shape holding source's properties that
// the pattern doesn't name, in the order a spread copies them (D-058).
func (l *lowerer) restProperties(source ir.Expr, element *ast.Node) ir.Expr {
	restType := l.typeAt(restTarget(element))
	shape := l.shape(restType, element)
	if shape == nil {
		return nil
	}
	objectType := restType // tsc keeps source's order in restType
	ofAnyInstance := false
	if pattern := l.typeAt(element.Parent); source.Type() == ir.Value && l.anyInstanceIn(pattern) != nil {
		objectType, ofAnyInstance = pattern, true // a C<any, …> place's object, whose own properties are C's fields (H10b)
	}
	names, ok := l.spreadNames(source, objectType, element)
	if !ok {
		return nil
	}
	var fields []*ir.Field
	var values []ir.Expr
	for _, name := range names {
		field := shape.Field(name)
		if field == nil {
			continue // named by the pattern
		}
		read := propertyOf(source, name)
		property := l.checker.GetPropertyOfType(restType, name)
		method := property == nil || property.Flags&ast.SymbolFlagsMethod != 0
		if read == nil && !method && isPure(source) && (isObjectOrUnionOfObjects(source.Type()) || source.Type() == ir.Value) {
			read = l.missingOwnProperty(source, name) // one a narrowing added (C222, C223), which a rest copies if it is the object's own
		}
		switch {
		case read == nil && method:
			l.notSupportedYet(element, fmt.Sprintf("rest properties holding the method `%s` are", name))
			return nil
		case read == nil:
			l.notSupportedYet(element, fmt.Sprintf("rest properties holding `%s`, which the object's type lacks, are", name))
			return nil
		}
		value := l.convertMember(copiedRead(read, field), field.Type, element, ofAnyInstance)
		if value == nil {
			return nil
		}
		fields, values = append(fields, field), append(values, value)
	}
	if len(fields) != len(shape.Fields) {
		l.notSupportedYet(element, "rest properties of this object are")
		return nil
	}
	return &ir.NewObject{Shape: shape, Order: shape.Order(fields), Values: values}
}

// elementPart reads from source the element at index of an array or tuple pattern (or of
// an array literal being assigned to): a tuple's field, or an array's element. With a
// default (initializer), an array too short to have the element gives the default; a
// rest element is the elements from index on.
func (l *lowerer) elementPart(source ir.Expr, element *ast.Node, index int, initializer *ast.Node, rest bool) ir.Expr {
	switch t := source.Type().(type) {
	case *ir.Class:
		if t.Tuple && !rest && index < len(t.Fields) {
			return &ir.FieldRef{X: source, Field: t.Fields[index]} // a tuple's elements are never undefined, so a default can't apply
		}
		if t.Tuple && !rest && initializer != nil {
			return l.expression(initializer) // past the tuple's end, so undefined: the default applies
		}
		if t.Tuple {
			return l.tupleRest(source, t, index, restTarget(element))
		}
	case *ir.Array:
		position := &ir.NumberConst{Value: float64(index)}
		if rest {
			return &ir.ArrayCall{Op: ir.ArraySlice, Args: []ir.Expr{source, position, &ir.NumberConst{Value: math.Inf(1)}}}
		}
		if initializer == nil && element.Kind == ast.KindBindingElement && element.Name().Kind == ast.KindIdentifier && l.isLoose(l.checker.GetSymbolAtLocation(element.Name())) {
			return &ir.ArrayCall{Op: ir.ArrayElement, Args: []ir.Expr{source, position}} // undefined past the end (D-090)
		}
		read := &ir.ArrayCall{Op: ir.ArrayElementChecked, Args: []ir.Expr{source, position}}
		if initializer == nil {
			return read
		}
		fallback := l.expression(initializer)
		if fallback == nil {
			return nil
		}
		if fallback = l.convert(fallback, t.Elem, initializer); fallback == nil {
			return nil
		}
		inRange := &ir.Binary{Op: ir.Lt, X: position, Y: &ir.ArrayCall{Op: ir.ArrayLength, Args: []ir.Expr{source}}}
		return &ir.Conditional{Cond: inRange, Then: read, Else: fallback}
	}
	l.notSupportedYet(element, fmt.Sprintf("destructuring a %s with an array pattern is", source.Type()))
	return nil
}

// parameterPrologue binds the parameters of fn that need it, in order, at the start of
// its body, declaring their names' locals before the body is lowered: a parameter with a
// default takes it when the call didn't pass the argument, which runs in the function's
// scope after the parameters before it are bound, as in JS; a pattern is destructured.
func (l *lowerer) parameterPrologue(fn *ir.Func) []ir.Stmt {
	var stmts []ir.Stmt
	for _, binding := range l.bindings[fn] {
		switch {
		case binding.given != nil:
			value := l.parameterDefault(binding)
			if value == nil {
				continue // reported
			}
			stmts = append(stmts, &ir.LocalDecl{At: l.pos(binding.node), Local: binding.value, Init: value})
		case binding.arg != nil: // a rest parameter
			stmts = append(stmts, &ir.LocalDecl{At: l.pos(binding.node), Local: binding.value, Init: &ir.RestArgs{X: &ir.LocalRef{Local: binding.arg}}})
		case binding.elements != nil: // a rest parameter of a tuple type
			values := make([]ir.Expr, len(binding.elements))
			for i, element := range binding.elements {
				values[i] = &ir.LocalRef{Local: element}
			}
			stmts = append(stmts, &ir.LocalDecl{At: l.pos(binding.node), Local: binding.value, Init: &ir.NewObject{Shape: binding.value.Type.(*ir.Class), Values: values}})
		}
		if pattern := binding.node.Name(); pattern.Kind != ast.KindIdentifier {
			destructured, ok := l.destructure(pattern, &ir.LocalRef{Local: binding.value}, l.pos(pattern))
			if !ok {
				continue // reported
			}
			stmts = append(stmts, destructured...)
		}
	}
	return stmts
}

// parameterDefault is the value of a parameter with a default: the argument if the call
// passed it, and otherwise the default; nil if the default fails to lower (reported). An
// optional parameter (`x?: T`) defaults to undefined. If the argument's type includes
// undefined, an argument that is undefined means the default too, as in JS (D-063).
func (l *lowerer) parameterDefault(binding *parameterBinding) ir.Expr {
	at := binding.node
	var value ir.Expr = &ir.UndefinedConst{}
	if initializer := binding.node.Initializer(); initializer != nil {
		at = initializer
		if value = l.expression(initializer); value == nil {
			return nil
		}
	}
	if value = l.convert(value, binding.value.Type, at); value == nil {
		return nil
	}
	var passed ir.Expr = &ir.LocalRef{Local: binding.arg}
	var cond ir.Expr = &ir.LocalRef{Local: binding.given}
	if u, ok := binding.arg.Type.(*ir.Union); ok && u.Has(ir.Undefined) && binding.value.Type != u {
		cond = &ir.Logical{Op: ir.And, X: cond, Y: &ir.Unary{Op: ir.Not, X: &ir.IsNullish{X: passed, Only: ir.Undefined}}}
		passed = fromUnion(passed, binding.value.Type)
	} else if passed = l.convert(passed, binding.value.Type, at); passed == nil {
		return nil
	}
	return &ir.Conditional{Cond: cond, Then: passed, Else: value}
}

// isDestructuringSource reports whether node is the value a pattern destructures (or a
// literal nested in one): an initializer of a pattern, the right side of an assignment to
// an array or object literal, or what `for…of` with a pattern iterates.
func isDestructuringSource(node *ast.Node) bool {
	parent := node.Parent
	for parent != nil && parent.Kind == ast.KindParenthesizedExpression {
		node, parent = parent, parent.Parent
	}
	if parent == nil {
		return false
	}
	switch parent.Kind {
	case ast.KindVariableDeclaration, ast.KindParameter:
		return parent.Initializer() == node && parent.Name().Kind != ast.KindIdentifier
	case ast.KindBinaryExpression:
		binary := parent.AsBinaryExpression()
		left := ast.SkipParentheses(binary.Left)
		return binary.OperatorToken.Kind == ast.KindEqualsToken && binary.Right == node &&
			(left.Kind == ast.KindArrayLiteralExpression || left.Kind == ast.KindObjectLiteralExpression)
	case ast.KindArrayLiteralExpression:
		return isDestructuringSource(parent)
	case ast.KindPropertyAssignment:
		return isDestructuringSource(parent.Parent)
	}
	return false
}

// restTarget is what a rest element binds or assigns: `rest` in `...rest`.
func restTarget(element *ast.Node) *ast.Node {
	if element.Kind == ast.KindSpreadElement || element.Kind == ast.KindSpreadAssignment {
		return element.Expression()
	}
	return element.Name()
}

// tupleRest is `...rest` in an array pattern on a tuple: the elements from index on, as
// the array or tuple the rest's type (target's) is.
func (l *lowerer) tupleRest(source ir.Expr, tuple *ir.Class, index int, target *ast.Node) ir.Expr {
	element := target
	var elements []ir.Expr
	for _, field := range tuple.Fields[index:] {
		elements = append(elements, &ir.FieldRef{X: source, Field: field})
	}
	switch t := l.irType(l.typeAt(target), element).(type) {
	case *ir.Array:
		for i, x := range elements {
			if elements[i] = l.convert(x, t.Elem, element); elements[i] == nil {
				return nil
			}
		}
		return &ir.NewArray{Array: t, Elems: elements}
	case *ir.Class:
		if t.Tuple {
			return &ir.NewObject{Shape: t, Values: elements}
		}
	}
	l.notSupportedYet(element, "this rest element is")
	return nil
}

// destructuringAssignment lowers `[a, b] = value` or `({ x, y } = value)` (pattern is the
// left side): value is held in a local, then each target is assigned its part, in order;
// the assignment's value is value.
func (l *lowerer) destructuringAssignment(pattern *ast.Node, valueNode *ast.Node) ir.Expr {
	if l.storesCopyingFunction(pattern, valueNode) {
		return nil // `[c.list[0]] = [f]` (R8-F3)
	}
	value := l.expression(valueNode)
	if value == nil {
		return nil
	}
	whole := l.newLocal("destructured", value.Type())
	stores, ok := l.assignParts(pattern, &ir.LocalRef{Local: whole}, l.holdsAnyInstance(valueNode)) // `({ items: it } = boxes[0])` for a Box<any>[] (H10b)
	if !ok {
		return nil
	}
	var result ir.Expr = &ir.LocalRef{Local: whole}
	for i := len(stores) - 1; i >= 0; i-- {
		result = &ir.Sequence{X: stores[i], Y: result}
	}
	return &ir.Bind{Local: whole, Value: value, In: result}
}

// assignParts assigns each target of an array or object literal on the left of an
// assignment its part of source; asIs, for a source tsc types C<any, …>, takes a part that
// would be copied as it is (asIsConversion, D-219).
func (l *lowerer) assignParts(pattern *ast.Node, source ir.Expr, asIs bool) ([]ir.Expr, bool) {
	var stores []ir.Expr
	var elements []*ast.Node
	if pattern.Kind == ast.KindObjectLiteralExpression {
		elements = pattern.AsObjectLiteralExpression().Properties.Nodes
	} else {
		elements = pattern.AsArrayLiteralExpression().Elements.Nodes
	}
	for i, element := range elements {
		var target, initializer, property *ast.Node
		var part ir.Expr
		switch element.Kind {
		case ast.KindOmittedExpression:
			continue
		case ast.KindSpreadElement:
			target = element.Expression()
			part = l.elementPart(source, element, i, nil, true)
		case ast.KindShorthandPropertyAssignment: // `{ x }`, which assigns the variable x
			if part = l.propertyPart(source, element, element.Name(), false); part == nil {
				return nil, false
			}
			if part = l.withDefault(part, element.AsShorthandPropertyAssignment().ObjectAssignmentInitializer, element.Name()); part == nil {
				return nil, false
			}
			store := l.assignShorthand(element, part)
			if store == nil {
				return nil, false
			}
			stores = append(stores, store)
			continue
		case ast.KindPropertyAssignment: // `{ x: target }`
			target, property = element.Initializer(), element.Name()
			var fallback *ast.Node
			if binary := ast.SkipParentheses(target); ast.IsBinaryExpression(binary) && binary.AsBinaryExpression().OperatorToken.Kind == ast.KindEqualsToken {
				target, fallback = binary.AsBinaryExpression().Left, binary.AsBinaryExpression().Right
			}
			if part = l.propertyPart(source, element, property, false); part != nil {
				part = l.withDefault(part, fallback, target)
			}
		case ast.KindSpreadAssignment: // `({ a, ...rest } = o)`
			target = element.Expression()
			part = l.propertyPart(source, element, nil, true)
		default: // an array literal's element: a target, or `target = default`
			target = element
			if ast.IsBinaryExpression(element) && element.AsBinaryExpression().OperatorToken.Kind == ast.KindEqualsToken {
				target, initializer = element.AsBinaryExpression().Left, element.AsBinaryExpression().Right
			}
			part = l.elementPart(source, element, i, initializer, false)
		}
		if part == nil {
			return nil, false
		}
		store := l.assignPart(target, part, asIs)
		if store == nil {
			return nil, false
		}
		stores = append(stores, store)
	}
	return stores, true
}

// assignPart assigns value to a target of a destructuring assignment: a place, or a
// nested array or object literal; asIs as for assignParts.
func (l *lowerer) assignPart(target *ast.Node, value ir.Expr, asIs bool) ir.Expr {
	if nested := ast.SkipParentheses(target); nested.Kind == ast.KindArrayLiteralExpression || nested.Kind == ast.KindObjectLiteralExpression {
		inner := l.newLocal("destructured", value.Type())
		stores, ok := l.assignParts(nested, &ir.LocalRef{Local: inner}, asIs)
		if !ok {
			return nil
		}
		var result ir.Expr = &ir.LocalRef{Local: inner}
		for i := len(stores) - 1; i >= 0; i-- {
			result = &ir.Sequence{X: stores[i], Y: result}
		}
		return &ir.Bind{Local: inner, Value: value, In: result}
	}
	destination, ok := l.assignmentTarget(target)
	if !ok {
		return nil
	}
	if asIs && value.Type() == ir.Value && copiesInstanceObjects(destination.valueType()) {
		value = l.asIsConversion(value, destination.valueType(), target) // a C<any, …> object's member, taken as it is (D-219)
	} else {
		value = l.convert(value, destination.valueType(), target)
	}
	if value == nil {
		return nil
	}
	if destination.local != nil || destination.field != nil || destination.index != nil || destination.arrayLength {
		return destination.store(value, false)
	}
	return l.withObject(destination, func(destination place) ir.Expr { return destination.store(value, false) })
}

// assignShorthand assigns value to the variable a shorthand property `{ x }` of an object
// literal on the left of an assignment names.
func (l *lowerer) assignShorthand(element *ast.Node, value ir.Expr) ir.Expr {
	local, ok := l.locals[l.followAliases(l.checker.GetShorthandAssignmentValueSymbol(element))]
	if !ok {
		l.notSupportedYet(element, "assigning to `"+element.Name().Text()+"` is")
		return nil
	}
	if local = l.ownLocal(element.Name(), local); local == nil {
		return nil
	}
	if value = l.convert(value, local.Type, element); value == nil {
		return nil
	}
	return place{local: local}.store(value, false)
}

// lengthOrSize reads `length` of an array or a string and `size` of a map or set, the
// properties of builtins an object pattern may name; nil for others.
func lengthOrSize(source ir.Expr, name string) ir.Expr {
	switch t := source.Type().(type) {
	case *ir.Array:
		if name == "length" {
			return &ir.ArrayCall{Op: ir.ArrayLength, Args: []ir.Expr{source}}
		}
	case *ir.MapType:
		if name == "size" && !t.Record {
			return &ir.MapCall{Op: ir.MapSize, Args: []ir.Expr{source}, Map: t}
		}
	}
	if source.Type() == ir.String && name == "length" {
		return &ir.CallBuiltin{Builtin: ir.StringLength, Args: []ir.Expr{source}}
	}
	return nil
}
