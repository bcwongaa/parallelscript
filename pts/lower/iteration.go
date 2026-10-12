package lower

import (
	"slices"
	"strings"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/internal/scanner"
	"github.com/microsoft/typescript-go/pts/ir"
)

// The iteration protocol over the program's objects (D-111). A class's or object
// literal's method [Symbol.iterator] is a method named iteratorMethodName, which the
// runtime calls through its dynamic entry to open an iterator over the object (for…of,
// spread, Array.from, yield*, an Iterable<T>); an object with its own next() is an
// iterator, whose next(), return() and throw() the runtime calls the same way.

// iteratorMethodName names the method [Symbol.iterator] in the IR and in method tables;
// [Symbol.asyncIterator] is `@@asyncIterator` (D-115).
const iteratorMethodName = "@@iterator"

// wellKnownSymbols are the symbols members may be named after.
var wellKnownSymbols = []string{"iterator", "asyncIterator"}

// jsMemberName is the name JS gives the function of a member named name in the IR:
// "[Symbol.iterator]" for `@@iterator`.
func jsMemberName(name string) string {
	if symbol, ok := strings.CutPrefix(name, "@@"); ok {
		return "[Symbol." + symbol + "]"
	}
	return name
}

// wellKnownSymbol is the name of the well-known symbol node is (`Symbol.iterator` is
// "iterator"), or "".
func wellKnownSymbol(node *ast.Node) string {
	node = ast.SkipParentheses(node)
	if node.Kind != ast.KindPropertyAccessExpression {
		return ""
	}
	access := node.AsPropertyAccessExpression()
	if access.Expression.Kind != ast.KindIdentifier || access.Expression.Text() != "Symbol" || !slices.Contains(wellKnownSymbols, access.Name().Text()) {
		return ""
	}
	return access.Name().Text()
}

// isSymbolIterator reports whether node is the expression `Symbol.iterator`.
func isSymbolIterator(node *ast.Node) bool { return wellKnownSymbol(node) == "iterator" }

// isIteratorName reports whether a member's name is a well-known symbol's
// (`[Symbol.iterator]`, `[Symbol.asyncIterator]`).
func isIteratorName(name *ast.Node) bool {
	return name != nil && name.Kind == ast.KindComputedPropertyName && wellKnownSymbol(name.Expression()) != ""
}

// memberName is the name of a class member or object literal property in the IR: its
// identifier, or `@@` and the symbol's name for a well-known symbol (`@@iterator`).
func memberName(member *ast.Node) string {
	if isIteratorName(member.Name()) {
		return "@@" + wellKnownSymbol(member.Name().Expression())
	}
	return member.Name().Text()
}

// propertyName is the name of a checker type's property in the IR: its own, or `@@` and
// the symbol's name for one tsc names after a well-known unique symbol.
func propertyName(property *ast.Symbol) string {
	for _, symbol := range wellKnownSymbols {
		if strings.HasPrefix(property.Name, ast.InternalSymbolNamePrefix+"@"+symbol+"@") {
			return "@@" + symbol
		}
	}
	return property.Name
}

// hasIteratorMethod reports whether values of t are objects of the program's with a
// [Symbol.iterator] method: a class's or an object literal's.
func (l *lowerer) hasIteratorMethod(t *checker.Type) bool {
	if t.Flags()&checker.TypeFlagsObject == 0 || l.checker.IsArrayType(t) || checker.IsTupleType(t) || l.isIteratorType(t) || l.isIterableType(t) {
		return false
	}
	for _, property := range l.checker.GetPropertiesOfType(t) {
		if propertyName(property) == iteratorMethodName {
			return property.ValueDeclaration != nil && !l.isDeclaredInDefaultLib(property)
		}
	}
	return false
}

// isIterationSource reports whether for…of, spread and Array.from iterate over values of
// t through the protocol (pts_iterator_open): iterators, Iterable<T>s, and the program's
// objects with a [Symbol.iterator] method.
func (l *lowerer) isIterationSource(t *checker.Type) bool {
	return l.isIteratorType(t) || l.isIterableType(t) || l.hasIteratorMethod(t)
}

// isPlainObject reports whether t is a class, an interface or a shape other than a tuple:
// where tsc lets such a value be iterated, it has a [Symbol.iterator] method.
func isPlainObject(t ir.Type) bool {
	switch t := t.(type) {
	case *ir.Class:
		return !t.Tuple
	case *ir.Interface:
		return true
	}
	return false
}

// getIteratorCall lowers `x[Symbol.iterator]()`: the iterator x's method makes, or the
// runtime's over an array's, string's, map's or set's items; a generator object's is
// itself. handled is false for other calls.
func (l *lowerer) getIteratorCall(call *ast.CallExpression) (x ir.Expr, handled bool) {
	callee := ast.SkipParentheses(call.Expression)
	if callee.Kind != ast.KindElementAccessExpression || !isSymbolIterator(callee.AsElementAccessExpression().ArgumentExpression) {
		return nil, false
	}
	if len(call.Arguments.Nodes) > 0 {
		l.notSupportedYet(call.AsNode(), "arguments to [Symbol.iterator]() are")
		return nil, true
	}
	iterable := l.expression(callee.Expression())
	t := l.irType(l.typeAt(call.AsNode()), call.AsNode())
	if iterable == nil || t == nil {
		return nil, true
	}
	iterator := &ir.CallBuiltin{Builtin: ir.IteratorGet, Args: []ir.Expr{box(iterable), &ir.StringConst{Value: scanner.GetTextOfNode(callee.Expression())}}}
	return l.checkedConversion(iterator, t, call.AsNode()), true
}
