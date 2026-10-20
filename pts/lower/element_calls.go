package lower

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/pts/ir"
)

// constantKey is the property name an element access names by a key whose value is that
// name, and the key: a string literal (`m["make"]`), or a name of a const initialised
// with one (`m[K]` for `const K = "make"`), either perhaps asserted or parenthesized,
// where tsc's type of the key is that string literal too; ok is false otherwise. A key
// whose type alone says the name (a parameter, a variable declared of a string literal
// type, an assertion of another string) may hold another string, as conversions from
// `any` don't check string literal types.
func (l *lowerer) constantKey(node *ast.Node) (name string, key *ast.Node, ok bool) {
	if node.Kind != ast.KindElementAccessExpression {
		return "", nil, false
	}
	key = node.AsElementAccessExpression().ArgumentExpression
	t := l.typeAt(key)
	if t.Flags()&checker.TypeFlagsStringLiteral == 0 {
		return "", nil, false
	}
	name = t.AsLiteralType().Value().(string)
	if value := l.keyValue(key, 0); value == nil || value.Text() != name {
		return "", nil, false
	}
	return name, key, true
}

// keyValue is the string literal a key's value is: the key itself or the initialiser of the
// const it names, perhaps asserted or parenthesized; or nil.
func (l *lowerer) keyValue(key *ast.Node, depth int) *ast.Node {
	switch key = ast.SkipParentheses(key); key.Kind {
	case ast.KindStringLiteral, ast.KindNoSubstitutionTemplateLiteral:
		return key
	case ast.KindAsExpression, ast.KindSatisfiesExpression, ast.KindTypeAssertionExpression:
		return l.keyValue(key.Expression(), depth)
	case ast.KindIdentifier:
		symbol := l.target(key)
		if symbol == nil || depth > maxKeyConsts {
			return nil
		}
		declaration := symbol.ValueDeclaration
		if declaration == nil || declaration.Kind != ast.KindVariableDeclaration || !ast.IsVarConst(declaration) || declaration.Initializer() == nil {
			return nil
		}
		return l.keyValue(declaration.Initializer(), depth+1)
	}
	return nil
}

// maxKeyConsts bounds the consts keyValue follows (`const K = J`).
const maxKeyConsts = 8

// isLiteralKey reports whether a key is a string literal, perhaps asserted or
// parenthesized, which reading runs nothing.
func isLiteralKey(key *ast.Node) bool {
	switch key = ast.SkipParentheses(key); key.Kind {
	case ast.KindStringLiteral, ast.KindNoSubstitutionTemplateLiteral:
		return true
	case ast.KindAsExpression, ast.KindSatisfiesExpression, ast.KindTypeAssertionExpression:
		return isLiteralKey(key.Expression())
	}
	return false
}

// calleeName is the name of the member a callee names: a property access's, or an element
// access's constant key (constantKey).
func (l *lowerer) calleeName(callee *ast.Node) string {
	if callee.Kind == ast.KindPropertyAccessExpression {
		return callee.AsPropertyAccessExpression().Name().Text()
	}
	name, _, _ := l.constantKey(callee)
	return name
}

// constantKeyCall lowers `o["m"](args)`, or `o[K](args)` for `const K = "m"`, for a generic
// method m, as `o.m(args)` lowers: a call of its instance, or a demand (D-148, D-191), where
// the element access read the method as a value, a bound wrapper of the erased copy, which
// lists no instances (C185). handled is false for any other call, which lowers as before:
// in the last fallback, which makes what the base made, and for `o?.["m"]?.(args)`, whose
// object only its chain's value of the method has (calledMethodReceiver).
func (l *lowerer) constantKeyCall(call *ast.CallExpression) (x ir.Expr, handled bool) {
	callee := call.Expression // not parenthesized: genericMethodCall reads its receiver as call.Expression.Expression()
	name, key, ok := l.constantKey(callee)
	_, read := l.chainObjects[callee]
	if !ok || l.specialization == DeclaredInstances || read && calledMethodReceiver(call.AsNode()) == nil {
		return nil, false // `o?.["m"]?.(args)` calls the value its chain read
	}
	receiver := callee.Expression()
	t := l.checker.GetNonNullableType(l.typeAt(receiver))
	property := l.checker.GetPropertyOfType(t, name)
	if property == nil || !isMethodSymbol(property) || !isGenericMethodSymbol(property) {
		return nil, false
	}
	dotted, static := l.dottedCall(call, t, implementationOf(property.ValueDeclaration))
	if dotted == nil {
		return nil, false
	}
	if read || isLiteralKey(key) {
		return dotted(), true // a literal, or `o[K]?.(args)`'s key, which its chain has read
	}
	return l.withKeyRead(receiver, key, static, dotted), true
}

// dottedCall is how `o.m(args)` lowers a call of a generic method m (declaration) on a
// receiver of type t, for constantKeyCall's `o["m"](args)`: through a union of object
// types, as a class's method (static, if it is a static one), or through an object type;
// nil if it is none of those.
func (l *lowerer) dottedCall(call *ast.CallExpression, t *checker.Type, declaration *ast.Node) (dotted func() ir.Expr, static bool) {
	if t.Flags()&checker.TypeFlagsUnion != 0 {
		if !isUnionValue(t) {
			return nil, false
		}
		return func() ir.Expr { return l.unionMethodCall(call) }, false
	}
	if generic := l.membersFor(declaration, call.Expression.Expression()).genericMethods[declaration]; generic != nil {
		return func() ir.Expr { return l.genericMethodCall(call, generic) }, false
	}
	if generic := l.genericStatics[declaration]; generic != nil {
		return func() ir.Expr { return l.genericCall(call, generic) }, true
	}
	if l.isObjectTypeMethod(declaration) {
		return func() ir.Expr { return l.interfaceMethodCall(call) }, false
	}
	return nil, false
}

// withKeyRead lowers a call of a method a named key names (dotted), reading the key after
// the receiver and before the arguments, as JS does: a global's or a captured local's read
// throws ReferenceError before its declaration has run (D-140). The key is read first
// where the receiver is `super`, or a class (static), whose read the lowering doesn't
// check for its declaration having run, as for `C.m(args)` (C201).
func (l *lowerer) withKeyRead(receiver, key *ast.Node, static bool, dotted func() ir.Expr) ir.Expr {
	read := l.expression(key)
	if read == nil {
		return nil
	}
	if l.readsSafely(read) {
		return dotted()
	}
	if static || receiver.Kind == ast.KindSuperKeyword {
		if x := dotted(); x != nil {
			return &ir.Sequence{X: read, Y: x}
		}
		return nil
	}
	object := l.expression(receiver)
	if object == nil {
		return nil
	}
	local := l.newLocal("object", object.Type())
	kept, wasKept := l.kept[receiver]
	l.kept[receiver] = &ir.LocalRef{Local: local} // the receiver dotted lowers
	x := dotted()
	if wasKept {
		l.kept[receiver] = kept
	} else {
		delete(l.kept, receiver)
	}
	if x == nil {
		return nil
	}
	return &ir.Bind{Local: local, Value: object, In: &ir.Sequence{X: read, Y: x}}
}

// readsSafely reports whether evaluating x, a key's read, can neither throw nor have an
// effect: a constant, or a local of the function being lowered that isn't a global.
func (l *lowerer) readsSafely(x ir.Expr) bool {
	if ref, ok := x.(*ir.LocalRef); ok {
		return !ref.Local.Global && l.owners[ref.Local] == l.function
	}
	return isPure(x)
}

// isGenericMethodSymbol reports whether every declaration of a method symbol declares type
// parameters of its own.
func isGenericMethodSymbol(property *ast.Symbol) bool {
	for _, declaration := range property.Declarations {
		if len(declaration.TypeParameters()) == 0 {
			return false
		}
	}
	return true
}
