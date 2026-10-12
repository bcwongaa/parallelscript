package lower

import (
	"fmt"
	"strings"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/internal/scanner"
	"github.com/microsoft/typescript-go/pts/ir"
)

// Typed arrays and ArrayBuffer (D-125) are builtin classes the runtime defines
// (pts_typed_array, pts_arraybuffer). Elements are numbers to the program: reads give
// numbers and writes take them, converted by the runtime as JS converts them. Every
// operation is a runtime function that calls reach directly; the reads and writes of
// elements are inline functions per typed array (pts_X_get, pts_X_set).

// typedArrayNames are the lib's typed arrays that the runtime defines; BigInt64Array,
// BigUint64Array (D-O08) and Float16Array aren't among them.
var typedArrayNames = []string{
	"Int8Array", "Uint8Array", "Uint8ClampedArray", "Int16Array", "Uint16Array", "Int32Array", "Uint32Array", "Float32Array", "Float64Array",
}

// typedArrays holds the builtin classes of typed arrays and ArrayBuffer and the runtime
// functions behind them, made once per program.
type typedArrays struct {
	classes map[*ir.Class]bool
	buffer  *ir.Class // ArrayBuffer, also SharedArrayBuffer's type (which can't be made)
	externs map[string]*ir.Func
}

// declareTypedArrayClasses makes the builtin classes of the lib's typed arrays and of
// ArrayBuffer, which the runtime defines.
func (l *lowerer) declareTypedArrayClasses() {
	l.typed = &typedArrays{classes: map[*ir.Class]bool{}, externs: map[string]*ir.Func{}}
	for _, name := range typedArrayNames {
		symbol := l.checker.GetGlobalSymbol(name, ast.SymbolFlagsValue, nil)
		class := &ir.Class{Name: name, Builtin: true}
		l.classes[symbol] = class
		l.builtinClasses = append(l.builtinClasses, class)
		l.typed.classes[class] = true
	}
	l.typed.buffer = &ir.Class{Name: "ArrayBuffer", Builtin: true}
	l.builtinClasses = append(l.builtinClasses, l.typed.buffer)
	for _, name := range []string{"ArrayBuffer", "SharedArrayBuffer"} {
		l.classes[l.checker.GetGlobalSymbol(name, ast.SymbolFlagsValue, nil)] = l.typed.buffer
	}
}

// extern returns the runtime function c, which takes params and returns result, making
// it the first time; a function the runtime defines for each typed array names it `pts_X_…`.
func (l *lowerer) extern(c string, result ir.Type, params ...ir.Type) *ir.Func {
	key := fmt.Sprintf("%s %p", c, result)
	for _, param := range params {
		key += fmt.Sprintf(" %p", param)
	}
	if fn, ok := l.typed.externs[key]; ok {
		return fn
	}
	fn := &ir.Func{Name: strings.TrimPrefix(c, "pts_"), Result: result, Extern: c}
	for i, t := range params {
		fn.Params = append(fn.Params, &ir.Local{Name: fmt.Sprintf("p%d", i), Type: t})
	}
	l.typed.externs[key] = fn
	return fn
}

// callExtern calls the runtime function c with args, which it takes as their types.
func (l *lowerer) callExtern(c string, result ir.Type, args ...ir.Expr) ir.Expr {
	params := make([]ir.Type, len(args))
	for i, arg := range args {
		params[i] = arg.Type()
	}
	return &ir.Call{Func: l.extern(c, result, params...), Args: args}
}

// isTypedArray reports whether t is a typed array's class.
func (l *lowerer) isTypedArray(t ir.Type) bool { return isTypedArray(t) }

// isTypedArray reports whether t is the class of one of the runtime's typed arrays.
func isTypedArray(t ir.Type) bool {
	class, ok := t.(*ir.Class)
	return ok && class.Builtin && bytesPerElement[class.Name] != 0
}

// typedArrayClass is the class of the typed array tsc's type of node names, or nil.
func (l *lowerer) typedArrayClass(node *ast.Node) *ir.Class {
	t := l.typeAt(node)
	if t.Flags()&checker.TypeFlagsObject == 0 {
		return nil
	}
	if class, ok := l.classes[t.Symbol()]; ok && l.typed.classes[class] {
		return class
	}
	return nil
}

// isArrayBufferExpression reports whether tsc's type of node is ArrayBuffer (or SharedArrayBuffer).
func (l *lowerer) isArrayBufferExpression(node *ast.Node) bool {
	t := l.typeAt(node)
	return t.Flags()&checker.TypeFlagsObject != 0 && l.classes[t.Symbol()] == l.typed.buffer
}

// isTypedArrayElement reports whether node is a[i] for a typed array a.
func (l *lowerer) isTypedArrayElement(node *ast.Node) bool {
	return node.Kind == ast.KindElementAccessExpression && l.typedArrayClass(node.Expression()) != nil
}

// typedArrayElement lowers a[i] of a typed array: a number, which throws RangeError for an
// index the array doesn't have, or if dynamic (where any value can go), undefined for one,
// as in JS (D-015).
func (l *lowerer) typedArrayElement(node *ast.Node, dynamic bool) ir.Expr {
	array, index := l.arrayElementOperands(node)
	if array == nil {
		return nil
	}
	if dynamic {
		return l.callExtern("pts_typed_array_element", ir.Value, array, index)
	}
	return l.callExtern(typedArrayFunction(array.Type(), "get"), ir.Number, array, index)
}

// typedArrayFunction is the runtime's function pts_X_name for the typed array of class t.
func typedArrayFunction(t ir.Type, name string) string {
	return "pts_" + t.(*ir.Class).Name + "_" + name
}

// typedArrayStore is a[i] = value for a typed array a: value, a number, converted for the
// array, at i if the array has it. Its value is value.
func (l *lowerer) typedArrayStore(array, index, value ir.Expr) ir.Expr {
	return l.callExtern(typedArrayFunction(array.Type(), "set"), ir.Number, array, index, value)
}

// typedArrayRead is the a[i] of a place that reads and stores it.
func (l *lowerer) typedArrayRead(array, index ir.Expr) ir.Expr {
	return l.callExtern(typedArrayFunction(array.Type(), "get"), ir.Number, array, index)
}

// typedArrayProperty lowers a property of a typed array or an ArrayBuffer, or of a typed
// array's constructor (BYTES_PER_ELEMENT); handled is false for any other property.
func (l *lowerer) typedArrayProperty(node *ast.Node) (x ir.Expr, handled bool) {
	name := node.AsPropertyAccessExpression().Name().Text()
	if owner, member, ok := strings.Cut(l.libMemberName(node), "."); ok && member == "BYTES_PER_ELEMENT" && strings.HasSuffix(owner, "Constructor") {
		if class := l.typedArrayNamed(strings.TrimSuffix(owner, "Constructor")); class != nil {
			return &ir.NumberConst{Value: bytesPerElement[class.Name]}, true
		}
	}
	isBuffer := l.isArrayBufferExpression(node.Expression())
	if l.typedArrayClass(node.Expression()) == nil && !isBuffer {
		return nil, false
	}
	object := l.expression(node.Expression())
	if object == nil {
		return nil, true
	}
	if isBuffer {
		if name != "byteLength" {
			l.notSupportedYet(node, fmt.Sprintf("the ArrayBuffer property `%s` is", name))
			return nil, true
		}
		return l.callExtern("pts_arraybuffer_byte_length", ir.Number, object), true
	}
	if !l.isTypedArray(object.Type()) {
		l.notSupportedYet(node, fmt.Sprintf("`%s` of a %s is", name, object.Type()))
		return nil, true
	}
	switch name {
	case "length", "byteLength", "byteOffset":
		return l.callExtern("pts_typed_array_"+snakeCase(name), ir.Number, object), true
	case "BYTES_PER_ELEMENT":
		return discard(object, &ir.NumberConst{Value: bytesPerElement[object.Type().(*ir.Class).Name]}), true
	case "buffer":
		return l.callExtern("pts_typed_array_buffer", l.typed.buffer, object), true
	}
	l.notSupportedYet(node, fmt.Sprintf("the typed array property `%s` is", name))
	return nil, true
}

// bytesPerElement is each typed array's BYTES_PER_ELEMENT.
var bytesPerElement = map[string]float64{
	"Int8Array": 1, "Uint8Array": 1, "Uint8ClampedArray": 1, "Int16Array": 2, "Uint16Array": 2,
	"Int32Array": 4, "Uint32Array": 4, "Float32Array": 4, "Float64Array": 8,
}

// snakeCase turns byteLength into byte_length.
func snakeCase(name string) string {
	var words strings.Builder
	for _, r := range name {
		if r >= 'A' && r <= 'Z' {
			words.WriteByte('_')
			r += 'a' - 'A'
		}
		words.WriteRune(r)
	}
	return words.String()
}

// typedArrayNamed is the class of the typed array of that name, or nil.
func (l *lowerer) typedArrayNamed(name string) *ir.Class {
	for class := range l.typed.classes {
		if class.Name == name {
			return class
		}
	}
	return nil
}

// typedArrayConstructor is the class of the typed array a `new` expression makes, or
// nil if it makes something else.
func (l *lowerer) typedArrayConstructor(callee *ast.Node) *ir.Class {
	if callee.Kind != ast.KindIdentifier || !l.isDeclaredInDefaultLib(l.target(callee)) {
		return nil
	}
	return l.typedArrayNamed(callee.Text())
}

// isArrayBufferConstructor reports whether callee is the global ArrayBuffer or SharedArrayBuffer.
func (l *lowerer) isArrayBufferConstructor(callee *ast.Node) bool {
	return callee.Kind == ast.KindIdentifier && (callee.Text() == "ArrayBuffer" || callee.Text() == "SharedArrayBuffer") && l.isDeclaredInDefaultLib(l.target(callee))
}

// unsupportedTypedArrays are the lib's typed arrays the runtime doesn't define, and why.
var unsupportedTypedArrays = map[string]string{
	"BigInt64Array": "BigInt64Array, an array of BigInts (D-O08), is", "BigUint64Array": "BigUint64Array, an array of BigInts (D-O08), is",
	"Float16Array": "Float16Array is", "DataView": "DataView is",
}

// reportUnsupportedTypedArray reports (at node) a type of the lib's typed arrays that the
// runtime doesn't define, and whether t is one.
func (l *lowerer) reportUnsupportedTypedArray(t *checker.Type, node *ast.Node) bool {
	symbol := t.Symbol()
	if symbol == nil || unsupportedTypedArrays[symbol.Name] == "" || !l.isDeclaredInDefaultLib(symbol) {
		return false
	}
	l.notSupportedYet(node, unsupportedTypedArrays[symbol.Name])
	return true
}

// newTypedArray lowers `new X(…)` for a typed array X: empty, of a length, of the
// elements of an array, a typed array or another iterable, or a view of an ArrayBuffer.
func (l *lowerer) newTypedArray(node *ast.Node, class *ir.Class) ir.Expr {
	args := node.Arguments()
	if l.hasSpread(args) {
		l.notSupportedYet(node, "spread arguments of a typed array's constructor are")
		return nil
	}
	if len(args) == 0 {
		return l.callExtern(typedArrayFunction(class, "new"), class, &ir.NumberConst{Value: 0})
	}
	if l.isArrayBufferExpression(args[0]) {
		values := l.expressions(args)
		if values == nil {
			return nil
		}
		operands := []ir.Expr{values[0], fromStart, &ir.NumberConst{Value: 0}, &ir.BoolConst{Value: len(values) > 2}}
		for i, value := range values[1:] {
			if operands[i+1] = l.toNumberOrReport(value, args[i+1]); operands[i+1] == nil {
				return nil
			}
		}
		return l.callExtern(typedArrayFunction(class, "on_buffer"), class, operands...)
	}
	if len(args) > 1 {
		l.notSupportedYet(args[1], "these arguments of a typed array's constructor are")
		return nil
	}
	if t := l.typeAt(args[0]); t.Flags()&checker.TypeFlagsNumberLike != 0 {
		length := l.expression(args[0])
		if length == nil {
			return nil
		}
		return l.callExtern(typedArrayFunction(class, "new"), class, toNumber(length))
	}
	return l.typedArrayOf(class, args[0])
}

// typedArrayOf is a new typed array of class with the items of the iterable at node: an
// array's elements, a typed array's, or the items of another iterable, each converted by
// ToNumber and then for the typed array.
func (l *lowerer) typedArrayOf(class *ir.Class, node *ast.Node) ir.Expr {
	if l.typedArrayClass(node) != nil {
		source := l.expression(node)
		if source == nil {
			return nil
		}
		return l.callExtern(typedArrayFunction(class, "from_typed"), class, source)
	}
	items := l.iterableItems(node)
	if items == nil {
		return nil
	}
	return l.callExtern(typedArrayFunction(class, "from_array"), class, items)
}

// iterableItems is the array of the items of the iterable at node, as Array.from would
// make it (the array itself for an array), or nil (reported).
func (l *lowerer) iterableItems(node *ast.Node) ir.Expr {
	if it, ok := l.mapIterableOf(node); ok {
		if it.m == nil {
			return nil // reported
		}
		return l.mapItemsArray(it, node)
	}
	x := l.iterable(node)
	if x == nil {
		return nil
	}
	switch t := x.Type(); {
	case isArray(t):
		return x
	case t == ir.String:
		return &ir.CallBuiltin{Builtin: ir.ArrayFromString, Args: []ir.Expr{x}}
	case isTuple(t):
		return l.tupleArray(x, l.typeAt(node), node)
	case t == ir.Value || l.hasIteratorMethod(l.typeAt(node)):
		return &ir.CallBuiltin{Builtin: ir.ValueToArray, Args: []ir.Expr{box(x), &ir.StringConst{Value: scanner.GetTextOfNode(node)}}}
	}
	l.notSupportedYet(node, fmt.Sprintf("the items of a %s are", x.Type()))
	return nil
}

// newArrayBuffer lowers `new ArrayBuffer(length)`.
func (l *lowerer) newArrayBuffer(node *ast.Node) ir.Expr {
	if node.Expression().Text() == "SharedArrayBuffer" {
		l.notSupportedYet(node, "SharedArrayBuffer is")
		return nil
	}
	args := node.Arguments()
	if len(args) != 1 || args[0].Kind == ast.KindSpreadElement {
		l.notSupportedYet(node, "`new ArrayBuffer` without a length, or with options (resizable buffers), is")
		return nil
	}
	length := l.expression(args[0])
	if length == nil {
		return nil
	}
	if length = l.toNumberOrReport(length, args[0]); length == nil {
		return nil
	}
	return l.callExtern("pts_arraybuffer_new", l.typed.buffer, length)
}

// typedArrayCall lowers a call of a typed array's method or static (from, of), or of
// ArrayBuffer's (slice, isView); handled is false for any other call.
func (l *lowerer) typedArrayCall(call *ast.CallExpression) (x ir.Expr, handled bool) {
	callee := ast.SkipParentheses(call.Expression)
	if callee.Kind != ast.KindPropertyAccessExpression {
		if l.typedArrayConstructor(callee) != nil || l.isArrayBufferConstructor(callee) {
			l.notSupportedYet(call.AsNode(), "calling `"+callee.Text()+"` without `new` is")
			return nil, true
		}
		return nil, false
	}
	name := callee.Name().Text()
	if l.typedArrayClass(callee.Expression()) != nil { // by the receiver's type, which generic code's instance gives
		return l.typedArrayMethodCall(call, callee, name), true
	}
	if l.isArrayBufferExpression(callee.Expression()) {
		return l.arrayBufferMethodCall(call, callee, name), true
	}
	owner, _, ok := strings.Cut(l.libMemberName(callee), ".")
	if !ok {
		return nil, false
	}
	switch {
	case owner == "ArrayBufferConstructor" && name == "isView":
		if len(call.Arguments.Nodes) != 1 || l.hasSpreadArgument(call) {
			return nil, true
		}
		value := l.expression(call.Arguments.Nodes[0])
		if value == nil {
			return nil, true
		}
		return l.callExtern("pts_arraybuffer_is_view", ir.Bool, box(value)), true
	case strings.HasSuffix(owner, "Constructor") && l.typedArrayNamed(strings.TrimSuffix(owner, "Constructor")) != nil:
		return l.typedArrayStaticCall(call, l.typedArrayNamed(strings.TrimSuffix(owner, "Constructor")), name), true
	}
	return nil, false
}

// arrayBufferMethodCall lowers buffer.m(…) for a method m of ArrayBuffer: slice.
func (l *lowerer) arrayBufferMethodCall(call *ast.CallExpression, callee *ast.Node, name string) ir.Expr {
	if name != "slice" {
		l.notSupportedYet(callee, "the ArrayBuffer method `"+name+"` is")
		return nil
	}
	buffer := l.expression(callee.Expression())
	if buffer == nil {
		return nil
	}
	operands := l.arrayOperands(call.Arguments.Nodes, []arrayParam{startParam, endParam}, nil)
	if operands == nil {
		return nil
	}
	return l.callExtern("pts_arraybuffer_slice", l.typed.buffer, append([]ir.Expr{buffer}, operands...)...)
}

// typedArrayStaticCall lowers X.from(items, mapper?) and X.of(…items) for a typed array X.
func (l *lowerer) typedArrayStaticCall(call *ast.CallExpression, class *ir.Class, name string) ir.Expr {
	args := call.Arguments.Nodes
	switch name {
	case "of":
		if l.hasSpreadArgument(call) {
			return nil
		}
		values := l.expressions(args)
		if values == nil && len(args) > 0 {
			return nil
		}
		for i, value := range values {
			if values[i] = l.toNumberOrReport(value, args[i]); values[i] == nil {
				return nil
			}
		}
		return l.callExtern(typedArrayFunction(class, "from_array"), class, &ir.NewArray{Array: ir.NumberArray, Elems: values})
	case "from":
		switch {
		case len(args) > 0 && lengthObject(args[0]) != nil: // `X.from({ length: n }, f)`
			items := l.arrayFromLength(call, lengthObject(args[0]))
			if items == nil {
				return nil
			}
			return l.callExtern(typedArrayFunction(class, "from_array"), class, items)
		case len(args) > 2:
			l.notSupportedYet(args[2], "the `thisArg` argument of a typed array's `from` is")
			return nil
		case len(args) == 2:
			items := l.iterableItems(args[0])
			if items == nil {
				return nil
			}
			if l.isTypedArray(items.Type()) {
				items = l.callExtern("pts_typed_array_to_array", ir.NumberArray, items)
			}
			if !isArray(items.Type()) {
				l.notSupportedYet(args[0], fmt.Sprintf("`from` with a mapping function of a %s is", items.Type()))
				return nil
			}
			mapped := l.arrayFromWithMapper(call, items)
			if mapped == nil {
				return nil
			}
			return l.callExtern(typedArrayFunction(class, "from_array"), class, mapped)
		}
		return l.typedArrayOf(class, args[0])
	}
	l.notSupportedYet(call.Expression, "the typed array static `"+name+"` is")
	return nil
}

// typedArrayMethods are the parameters of the typed array methods without callbacks that
// call a runtime function pts_typed_array_<method> with the array and them, and the
// methods' results (nil for the array's own type).
var typedArrayMethods = map[string]struct {
	params []arrayParam
	result ir.Type
}{
	"at":             {[]arrayParam{startParam}, ir.Value},
	"copyWithin":     {[]arrayParam{{ir.Number, nil}, startParam, endParam}, nil},
	"fill":           {[]arrayParam{{ir.Number, nil}, startParam, endParam}, nil},
	"includes":       {[]arrayParam{{ir.Number, nil}, startParam}, ir.Bool},
	"indexOf":        {[]arrayParam{{ir.Number, nil}, startParam}, ir.Number},
	"lastIndexOf":    {[]arrayParam{{ir.Number, nil}, endParam}, ir.Number},
	"join":           {[]arrayParam{{ir.String, &ir.StringConst{Value: ","}}}, ir.String},
	"toString":       {nil, ir.String},
	"toLocaleString": {nil, ir.String},
	"reverse":        {nil, nil},
	"toReversed":     {nil, nil},
	"slice":          {[]arrayParam{startParam, endParam}, nil},
	"subarray":       {[]arrayParam{startParam, endParam}, nil},
	"with":           {[]arrayParam{{ir.Number, nil}, {ir.Number, nil}}, nil},
}

// typedArrayMethodCall lowers a.m(…) for a method m of a typed array a.
func (l *lowerer) typedArrayMethodCall(call *ast.CallExpression, callee *ast.Node, name string) ir.Expr {
	if l.hasSpreadArgument(call) {
		return nil
	}
	if l.isTypedArrayIteration(call.AsNode()) {
		items, _ := l.typedArrayIteration(call.AsNode())
		if items == nil {
			return nil
		}
		return &ir.CallBuiltin{Builtin: ir.IteratorOpen, Args: []ir.Expr{box(items), &ir.StringConst{Value: scanner.GetTextOfNode(call.AsNode())}}}
	}
	array := l.expression(callee.Expression())
	if array == nil {
		return nil
	}
	if !l.isTypedArray(array.Type()) {
		l.notSupportedYet(callee, fmt.Sprintf("`%s` of a %s is", name, array.Type()))
		return nil
	}
	class := array.Type().(*ir.Class)
	args := call.Arguments.Nodes
	switch name {
	case "valueOf":
		return array
	case "sort", "toSorted":
		if len(args) > 0 {
			return l.typedSortWith(array, args[0], name == "toSorted")
		}
		return l.callExtern("pts_typed_array_"+snakeCase(name), class, array)
	case "set":
		return l.typedArraySet(array, args)
	}
	if callbackMethods[name] {
		return l.typedCallbackMethodCall(call, name, array)
	}
	method, ok := typedArrayMethods[name]
	if !ok {
		l.notSupportedYet(callee, fmt.Sprintf("the typed array method `%s` is", name))
		return nil
	}
	operands := l.arrayOperands(args, method.params, nil)
	if operands == nil {
		return nil
	}
	result := method.result
	if result == nil {
		result = class
	}
	c := "pts_typed_array_" + snakeCase(name)
	if name == "toString" {
		c, operands = "pts_typed_array_join", []ir.Expr{&ir.StringConst{Value: ","}}
	}
	x := l.callExtern(c, result, append([]ir.Expr{array}, operands...)...)
	if result == ir.Value { // at: number | undefined
		t := l.irType(l.typeAt(call.AsNode()), call.AsNode())
		if t == nil {
			return nil
		}
		return l.convert(x, t, call.AsNode())
	}
	return x
}

// typedArraySet lowers a.set(source, offset?): source an array or a typed array.
func (l *lowerer) typedArraySet(array ir.Expr, args []*ast.Node) ir.Expr {
	if len(args) == 0 {
		return nil
	}
	offset := ir.Expr(&ir.NumberConst{Value: 0})
	if len(args) > 1 {
		value := l.expression(args[1])
		if value == nil {
			return nil
		}
		if offset = l.toNumberOrReport(value, args[1]); offset == nil {
			return nil
		}
	}
	if l.typedArrayClass(args[0]) != nil {
		source := l.expression(args[0])
		if source == nil {
			return nil
		}
		return l.callExtern("pts_typed_array_set_typed", ir.Void, array, source, offset)
	}
	source := l.iterableItems(args[0])
	if source == nil {
		return nil
	}
	return l.callExtern("pts_typed_array_set_array", ir.Void, array, source, offset)
}

// typedSortWith lowers a.sort(compare) (or a.toSorted(compare), on a copy) for a typed
// array a: Array's sort of its elements as numbers, written back.
func (l *lowerer) typedSortWith(array ir.Expr, compare *ast.Node, copy bool) ir.Expr {
	class := array.Type().(*ir.Class)
	if copy {
		array = l.callExtern("pts_typed_array_slice", class, array, fromStart, toEnd)
	}
	sorted, numbers := l.newLocal("sorted", class), l.newLocal("numbers", ir.NumberArray)
	sort := l.sortWith(&ir.LocalRef{Local: numbers}, compare)
	if sort == nil {
		return nil
	}
	write := l.callExtern("pts_typed_array_set_array", ir.Void, &ir.LocalRef{Local: sorted}, &ir.LocalRef{Local: numbers}, &ir.NumberConst{Value: 0})
	return &ir.Bind{Local: sorted, Value: array, In: &ir.Bind{
		Local: numbers,
		Value: l.callExtern("pts_typed_array_to_array", ir.NumberArray, &ir.LocalRef{Local: sorted}),
		In:    &ir.Sequence{X: sort, Y: &ir.Sequence{X: write, Y: &ir.LocalRef{Local: sorted}}},
	}}
}

// isTypedArrayIteration reports whether node is `a.keys()`, `a.values()` or `a.entries()` of a typed array.
func (l *lowerer) isTypedArrayIteration(node *ast.Node) bool {
	node = ast.SkipParentheses(node)
	if node.Kind != ast.KindCallExpression || len(node.Arguments()) > 0 {
		return false
	}
	callee := ast.SkipParentheses(node.AsCallExpression().Expression)
	if callee.Kind != ast.KindPropertyAccessExpression || l.typedArrayClass(callee.Expression()) == nil {
		return false
	}
	name := callee.Name().Text()
	return name == "keys" || name == "values" || name == "entries"
}

// typedArrayIteration lowers `a.keys()`, `a.values()` and `a.entries()` of a typed array
// a where they are iterated, as Array's of a's elements (arrayIteration); ok is false for
// any other node.
func (l *lowerer) typedArrayIteration(node *ast.Node) (x ir.Expr, ok bool) {
	if !l.isTypedArrayIteration(node) {
		return nil, false
	}
	callee := ast.SkipParentheses(ast.SkipParentheses(node).AsCallExpression().Expression)
	array := l.expression(callee.Expression())
	if array == nil {
		return nil, true
	}
	numbers := l.callExtern("pts_typed_array_to_array", ir.NumberArray, array)
	switch callee.Name().Text() {
	case "keys":
		return &ir.ArrayCall{Op: ir.ArrayKeys, Args: []ir.Expr{numbers}}, true
	case "values":
		return numbers, true
	}
	return &ir.Call{Func: l.entriesHelper(ir.NumberArray, node), Args: []ir.Expr{numbers}}, true
}

// typedCallbackMethodCall lowers `a.m(callback, …)` for a typed array a and a method m
// that takes a callback, as callbackMethodCall does for arrays.
func (l *lowerer) typedCallbackMethodCall(call *ast.CallExpression, name string, array ir.Expr) ir.Expr {
	if name == "flatMap" {
		l.notSupportedYet(call.Expression, "the typed array method `flatMap` is")
		return nil
	}
	return l.callbackMethodCall(call, name, array, false)
}
