package lower

import (
	"fmt"
	"slices"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/scanner"
	"github.com/microsoft/typescript-go/pts/ir"
)

// declareClasses makes the IR class of every class a module declares at its top level,
// still without superclass or members, so that any type can name any class.
func (l *lowerer) declareClasses(file *ast.SourceFile) {
	for _, statement := range file.Statements.Nodes {
		if statement.Kind == ast.KindClassDeclaration {
			class := l.declareClass(statement)
			l.classes[l.declaredSymbol(statement)] = class
			if class != nil {
				l.classNodes[class] = statement
			}
		}
	}
}

// declareClass makes the IR class for a declaration, or reports why it can't and returns nil.
func (l *lowerer) declareClass(node *ast.Node) *ir.Class {
	switch {
	case node.Name() == nil:
		l.notSupportedYet(node, "classes without a name are")
		return nil
	case node.TypeParameterList() != nil:
		l.notSupportedYet(node.Name(), "generic classes are")
		return nil
	case ast.HasSyntacticModifier(node, ast.ModifierFlagsAmbient):
		l.notSupportedYet(node.Name(), "`declare` is")
		return nil
	}
	return &ir.Class{Name: node.Name().Text(), Module: l.function.Module, Abstract: ast.HasSyntacticModifier(node, ast.ModifierFlagsAbstract)}
}

// declareSuperclasses links each class a module declares to its superclass. A class
// extending anything but a class of the program, or a class that failed, fails too.
func (l *lowerer) declareSuperclasses(file *ast.SourceFile) {
	for _, statement := range file.Statements.Nodes {
		if statement.Kind == ast.KindClassDeclaration {
			l.declareSuperclass(statement)
		}
	}
}

func (l *lowerer) declareSuperclass(node *ast.Node) {
	symbol := l.declaredSymbol(node)
	class := l.classes[symbol]
	heritage := ast.GetExtendsHeritageClauseElement(node)
	if class == nil || heritage == nil || class.Parent != nil {
		return
	}
	parentSymbol := l.target(heritage.Expression())
	parent, ok := l.classes[parentSymbol]
	if parent != nil {
		l.declareSuperclass(parentSymbol.ValueDeclaration) // tsc rejects cycles
		parent = l.classes[parentSymbol]
	}
	switch {
	case !ok:
		l.notSupportedYet(heritage, "extending `"+scanner.GetTextOfNode(heritage.Expression())+"` is")
		l.classes[symbol] = nil
	case parent == nil:
		l.classes[symbol] = nil // the superclass failed, which was reported
	default:
		class.Parent = parent
	}
}

// orderedClasses are the classes of the program, each after its superclass.
func (l *lowerer) orderedClasses(files []*ast.SourceFile) []*ir.Class {
	var ordered []*ir.Class
	added := map[*ir.Class]bool{}
	var add func(class *ir.Class)
	add = func(class *ir.Class) {
		if class == nil || added[class] {
			return
		}
		add(class.Parent)
		added[class] = true
		ordered = append(ordered, class)
	}
	for _, file := range files {
		for _, statement := range file.Statements.Nodes {
			if statement.Kind == ast.KindClassDeclaration {
				add(l.classes[l.declaredSymbol(statement)])
			}
		}
	}
	return ordered
}

// declareClassMembers makes the fields of each class, and the IR functions of its
// constructor and methods, whose bodies are lowered later; superclasses go first. Then
// it links overrides to the methods they override.
func (l *lowerer) declareClassMembers() {
	for _, class := range l.classList {
		l.function = class.Module.Init
		l.declareMembers(l.classNodes[class], class)
	}
	for _, class := range l.classList {
		l.linkOverrides(class)
	}
}

func (l *lowerer) declareMembers(node *ast.Node, class *ir.Class) {
	for _, member := range node.Members() {
		if member.Kind == ast.KindSemicolonClassElement {
			continue
		}
		if what := unsupportedMember(member); what != "" {
			l.notSupportedYet(nameOf(member), what)
			continue
		}
		switch member.Kind {
		case ast.KindPropertyDeclaration:
			l.declareField(member, class)
		case ast.KindMethodDeclaration:
			abstract := ast.HasSyntacticModifier(member, ast.ModifierFlagsAbstract)
			if member.Body() == nil && !abstract {
				continue // an overload signature
			}
			name := member.Name().Text()
			var method *ir.Method
			if fn := l.declareFunction(member, class.Name+"."+name, class); fn != nil {
				method = &ir.Method{Name: name, Class: class, Func: fn, Virtual: abstract}
				class.Methods = append(class.Methods, method)
			}
			l.methods[member] = method
		case ast.KindConstructor:
			if member.Body() != nil {
				class.Constructor = l.declareFunction(member, class.Name+".constructor", class)
			}
		}
	}
	if !hasConstructor(node) {
		class.Constructor = l.implicitConstructor(node, class)
	}
}

// declareField makes the field a property declaration declares. A field that redeclares
// an inherited one (to give it another initialiser) is the inherited field.
func (l *lowerer) declareField(member *ast.Node, class *ir.Class) {
	t := l.bindingType(l.declaredSymbol(member), member.Name())
	if t == nil {
		return
	}
	field := &ir.Field{Name: member.Name().Text(), Type: t, Class: class}
	if class.Parent != nil {
		if inherited := class.Parent.Field(field.Name); inherited != nil {
			if inherited.Type != t {
				l.notSupportedYet(member.Name(), fmt.Sprintf("redeclaring the %s field `%s` as %s is", inherited.Type, field.Name, t))
				return
			}
			field = inherited
		}
	}
	if field.Class == class {
		class.Fields = append(class.Fields, field)
	}
	l.fields[member] = field
}

// implicitConstructor is the constructor of a class that doesn't declare one: it takes
// no arguments, or in a subclass, the parent constructor's, and passes them on to it
// (JS's `constructor(...args) { super(...args); }`). It is nil if the parent's failed.
func (l *lowerer) implicitConstructor(node *ast.Node, class *ir.Class) *ir.Func {
	constructor := l.newMethod(class, class.Name+".constructor", l.pos(node))
	if class.Parent == nil {
		return constructor
	}
	if class.Parent.Constructor == nil {
		return nil // reported with the parent
	}
	outer := l.function
	l.function = constructor
	for _, param := range class.Parent.Constructor.Params[1:] {
		constructor.Params = append(constructor.Params, l.newLocal(param.Name, param.Type))
	}
	l.function = outer
	return constructor
}

// linkOverrides links each method of class that overrides an inherited one to it, and
// marks both virtual. An override must take and return what the method it overrides does.
func (l *lowerer) linkOverrides(class *ir.Class) {
	if class.Parent == nil {
		return
	}
	for _, member := range l.classNodes[class].Members() {
		method := l.methods[member]
		if method == nil {
			continue
		}
		overridden := class.Parent.Method(method.Name)
		switch {
		case overridden == nil:
			continue
		case !sameSignature(method.Func, overridden.Func):
			l.notSupportedYet(member.Name(), fmt.Sprintf("overriding `%s.%s` with other parameter or result types is", overridden.Class.Name, method.Name))
			continue
		}
		method.Overrides = overridden
		for m := method; m != nil; m = m.Overrides {
			m.Virtual = true
		}
	}
}

// sameSignature reports whether two methods take the same arguments and return the same type.
func sameSignature(a *ir.Func, b *ir.Func) bool {
	if len(a.Params) != len(b.Params) || a.Result != b.Result {
		return false
	}
	for i := 1; i < len(a.Params); i++ { // both take their own class's `this` first
		if a.Params[i].Type != b.Params[i].Type {
			return false
		}
	}
	return true
}

// unsupportedMember says which class member isn't supported yet ("getters are"), or "".
func unsupportedMember(member *ast.Node) string {
	switch {
	case member.Kind == ast.KindGetAccessor || member.Kind == ast.KindSetAccessor:
		return "getters and setters are"
	case member.Kind == ast.KindClassStaticBlockDeclaration || ast.IsStatic(member):
		return "static members are"
	case member.Kind == ast.KindIndexSignature:
		return "index signatures in classes are"
	case member.Name() != nil && ast.IsPrivateIdentifier(member.Name()):
		return "`#private` members are"
	case member.Name() != nil && member.Name().Kind != ast.KindIdentifier:
		return "members with computed or quoted names are"
	case member.Kind == ast.KindPropertyDeclaration && member.PostfixToken() != nil:
		return "optional fields are"
	case member.Kind == ast.KindPropertyDeclaration && ast.HasSyntacticModifier(member, ast.ModifierFlagsAbstract):
		return "abstract fields are"
	}
	return ""
}

// hasConstructor reports whether a class declares a constructor, with a body or not.
func hasConstructor(node *ast.Node) bool {
	for _, member := range node.Members() {
		if member.Kind == ast.KindConstructor {
			return true
		}
	}
	return false
}

// newMethod makes an IR function of class taking only `this`, with no body yet.
func (l *lowerer) newMethod(class *ir.Class, name string, pos ir.Pos) *ir.Func {
	fn := &ir.Func{Name: name, Module: class.Module, Pos: pos, Result: ir.Void}
	fn.Params = []*ir.Local{l.newThis(fn, class)}
	return fn
}

// newThis makes the `this` parameter of a constructor or method of class.
func (l *lowerer) newThis(fn *ir.Func, class *ir.Class) *ir.Local {
	this := &ir.Local{Name: "this", Type: class}
	l.owners[this] = fn
	l.this[fn] = this
	return this
}

// classBodies lowers the constructor and the method bodies of a class.
func (l *lowerer) classBodies(node *ast.Node) {
	class := l.classes[l.declaredSymbol(node)]
	if class == nil {
		return
	}
	if class.Constructor != nil {
		l.inFunction(class.Constructor, func() { class.Constructor.Body = l.constructorBody(node, class) })
	}
	for _, member := range node.Members() {
		if member.Kind == ast.KindMethodDeclaration && member.Body() != nil {
			if method := l.methods[member]; method != nil {
				l.inFunction(method.Func, func() { method.Func.Body = l.block(member.Body(), member.Body().Statements()) })
			}
		}
	}
}

// constructorBody is the body of a class's constructor. The field initialisers come
// first, or in a subclass, right after super(...) returns, as in JS.
func (l *lowerer) constructorBody(node *ast.Node, class *ir.Class) *ir.Block {
	var body *ast.Node
	for _, member := range node.Members() {
		if member.Kind == ast.KindConstructor && member.Body() != nil {
			body = member.Body()
		}
	}
	var stmts []ir.Stmt
	switch {
	case body != nil && class.Parent != nil:
		stmts = l.block(body, body.Statements()).Stmts // super(...) brings the initialisers
	case body != nil:
		stmts = append(l.fieldInitializers(node, class), l.block(body, body.Statements()).Stmts...)
	case class.Parent != nil:
		constructor := class.Constructor
		args := make([]ir.Expr, len(constructor.Params)-1)
		for i, param := range constructor.Params[1:] {
			args[i] = &ir.LocalRef{Local: param}
		}
		super := &ir.ExprStmt{At: constructor.Pos, X: l.parentConstructorCall(class, args)}
		stmts = append([]ir.Stmt{super}, l.fieldInitializers(node, class)...)
	default:
		stmts = l.fieldInitializers(node, class)
	}
	return &ir.Block{At: class.Constructor.Pos, Stmts: stmts}
}

// fieldInitializers give each field the class declares, in declaration order, its
// initialiser's value, or the zero value of its type ("" for a string, undefined for a
// dynamic value; numbers and booleans are zero already), as `let x: T;` does. A
// `declare` redeclaration of an inherited field changes nothing.
func (l *lowerer) fieldInitializers(node *ast.Node, class *ir.Class) []ir.Stmt {
	var stmts []ir.Stmt
	this := &ir.LocalRef{Local: l.this[class.Constructor]}
	for _, member := range node.Members() {
		field := l.fields[member]
		if member.Kind != ast.KindPropertyDeclaration || field == nil {
			continue
		}
		var value ir.Expr
		switch initializer := member.Initializer(); {
		case initializer != nil:
			if value = l.expression(initializer); value != nil {
				value = l.convert(value, field.Type, initializer)
			}
		case field.Class != class && ast.HasSyntacticModifier(member, ast.ModifierFlagsAmbient):
		case field.Type == ir.String:
			value = &ir.StringConst{}
		case field.Type == ir.Value || field.Type == ir.Undefined:
			value = &ir.UndefinedConst{}
		case field.Type == ir.Null:
			value = &ir.NullConst{}
		}
		if value != nil {
			stmts = append(stmts, &ir.ExprStmt{At: l.pos(member), X: &ir.SetField{X: this, Field: field, Value: value}})
		}
	}
	return stmts
}

// inFunction lowers with fn as the function being lowered.
func (l *lowerer) inFunction(fn *ir.Func, lower func()) {
	outer, outerTargets := l.function, l.targets
	l.function, l.targets = fn, nil
	lower()
	l.function, l.targets = outer, outerTargets
	l.lowered = append(l.lowered, fn)
}

// thisExpression lowers `this` in a constructor or method.
func (l *lowerer) thisExpression(node *ast.Node) ir.Expr {
	if this := l.this[l.function]; this != nil {
		return &ir.LocalRef{Local: this}
	}
	l.notSupportedYet(node, "`this` outside a class is")
	return nil
}

// newExpression lowers `new C(args)` for a class of the program.
func (l *lowerer) newExpression(node *ast.Node) ir.Expr {
	class, ok := l.classes[l.target(node.Expression())]
	switch {
	case !ok:
		l.notSupportedYet(node, "`new` of anything but a class of the program is")
		return nil
	case class == nil || class.Constructor == nil:
		return nil // reported with the class
	}
	args := l.arguments(node.Arguments(), class.Constructor.Params[1:])
	if args == nil {
		return nil
	}
	return &ir.New{Class: class, Args: args}
}

// superCallStatement lowers the statement `super(args);` in a subclass's constructor:
// the parent's constructor runs on `this`, then the class's field initialisers.
func (l *lowerer) superCallStatement(node *ast.Node) []ir.Stmt {
	call := l.superCall(node.Expression())
	if call == nil {
		return nil
	}
	class := l.currentClass()
	return append([]ir.Stmt{&ir.ExprStmt{At: l.pos(node), X: call}}, l.fieldInitializers(l.classNodes[class], class)...)
}

// superCall lowers `super(args)`, a call of the parent's constructor on `this`. As a
// statement, superCallStatement adds the field initialisers after it; inside an
// expression, that is only supported for a class that declares no fields.
func (l *lowerer) superCall(node *ast.Node) ir.Expr {
	class := l.currentClass()
	if node.Parent.Kind != ast.KindExpressionStatement && declaresFields(l.classNodes[class]) {
		l.notSupportedYet(node, "`super(...)` inside an expression in a class with fields is")
		return nil
	}
	parent := class.Parent.Constructor
	if parent == nil {
		return nil // reported with the parent
	}
	args := l.arguments(node.Arguments(), parent.Params[1:])
	if args == nil {
		return nil
	}
	return l.parentConstructorCall(class, args)
}

// parentConstructorCall calls the constructor of class's parent on `this` with args.
func (l *lowerer) parentConstructorCall(class *ir.Class, args []ir.Expr) ir.Expr {
	this := &ir.ClassCast{X: &ir.LocalRef{Local: l.this[class.Constructor]}, To: class.Parent}
	return &ir.Call{Func: class.Parent.Constructor, Args: append([]ir.Expr{this}, args...)}
}

// declaresFields reports whether a class declaration declares fields.
func declaresFields(node *ast.Node) bool {
	for _, member := range node.Members() {
		if member.Kind == ast.KindPropertyDeclaration && !ast.IsStatic(member) {
			return true
		}
	}
	return false
}

// isSuperCall reports whether node is `super(args)`.
func isSuperCall(node *ast.Node) bool {
	return node.Kind == ast.KindCallExpression && node.Expression().Kind == ast.KindSuperKeyword
}

// currentClass is the class whose constructor or method is being lowered.
func (l *lowerer) currentClass() *ir.Class {
	return l.this[l.function].Type.(*ir.Class)
}

// instanceMethodCall lowers `o.m(args)` for a method m of a class of the program: a
// direct call when no subclass of o's static class overrides m, so that the
// implementation is known, and otherwise a call through the instance's method table.
func (l *lowerer) instanceMethodCall(call *ast.CallExpression, method *ir.Method) ir.Expr {
	receiverNode := call.Expression.Expression()
	if receiverNode.Kind == ast.KindSuperKeyword {
		return l.superMethodCall(call, method)
	}
	receiver := l.expression(receiverNode)
	if receiver == nil {
		return nil
	}
	if receiver = l.asInstanceOf(receiver, method.Class, receiverNode); receiver == nil {
		return nil
	}
	args := l.arguments(call.Arguments.Nodes, method.Func.Params[1:])
	if args == nil {
		return nil
	}
	static := receiver.Type().(*ir.Class)
	if method.Virtual && (method.Func.Body == nil || l.isOverriddenBelow(static, method.Name)) {
		receiver = upcast(receiver, method.Slot().Class)
		return &ir.VirtualCall{Method: method, Args: append([]ir.Expr{receiver}, args...)}
	}
	return &ir.Call{Func: method.Func, Args: append([]ir.Expr{upcast(receiver, method.Class)}, args...)}
}

// superMethodCall lowers `super.m(args)`: the implementation the parent has, called on `this`.
func (l *lowerer) superMethodCall(call *ast.CallExpression, method *ir.Method) ir.Expr {
	args := l.arguments(call.Arguments.Nodes, method.Func.Params[1:])
	if args == nil {
		return nil
	}
	this := upcast(&ir.LocalRef{Local: l.this[l.function]}, method.Class)
	return &ir.Call{Func: method.Func, Args: append([]ir.Expr{this}, args...)}
}

// isOverriddenBelow reports whether a subclass of class declares a method named name.
func (l *lowerer) isOverriddenBelow(class *ir.Class, name string) bool {
	for _, other := range l.classList {
		if other != class && other.IsSubclassOf(class) && slices.ContainsFunc(other.Methods, func(m *ir.Method) bool { return m.Name == name }) {
			return true
		}
	}
	return false
}

// fieldAccess lowers `o.f` for a field f of a class of the program.
func (l *lowerer) fieldAccess(node *ast.Node, field *ir.Field) ir.Expr {
	object := l.fieldObject(node, field)
	if object == nil {
		return nil
	}
	return &ir.FieldRef{X: object, Field: field}
}

// fieldObject lowers the instance o of a field access `o.f`, or reports it and returns nil.
func (l *lowerer) fieldObject(node *ast.Node, field *ir.Field) ir.Expr {
	if ast.IsOptionalChain(node) {
		l.notSupportedYet(node, "optional chaining `?.` is")
		return nil
	}
	object := l.expression(node.Expression())
	if object == nil {
		return nil
	}
	return l.asInstanceOf(object, field.Class, node.Expression())
}

// asInstanceOf makes object, an instance, usable as an instance of class: as it is if its
// static class is class or a subclass, converted if the checker narrowed it to one.
func (l *lowerer) asInstanceOf(object ir.Expr, class *ir.Class, node *ast.Node) ir.Expr {
	if static, ok := object.Type().(*ir.Class); ok && static.IsSubclassOf(class) {
		return object
	}
	return l.convert(object, class, node)
}

// classCast converts value, an instance, to the class to: an ancestor of its static
// class, or a subclass the checker narrowed it to (with instanceof, say). TypeScript
// also lets an instance stand for another class with the same members; that is
// reported (at node) as not supported yet.
func (l *lowerer) classCast(value ir.Expr, to *ir.Class, node *ast.Node) ir.Expr {
	from := value.Type().(*ir.Class)
	if from.IsSubclassOf(to) || to.IsSubclassOf(from) && l.isNarrowedTo(node, to) {
		return &ir.ClassCast{X: value, To: to}
	}
	l.notSupportedYet(node, fmt.Sprintf("using a `%s` as a `%s` is", from, to))
	return nil
}

// isNarrowedTo reports whether the checker knows node to be an instance of class.
func (l *lowerer) isNarrowedTo(node *ast.Node, class *ir.Class) bool {
	narrowed, ok := l.classes[l.checker.GetTypeAtLocation(node).Symbol()]
	return ok && narrowed != nil && narrowed.IsSubclassOf(class)
}

// upcast converts x, an instance of to or of a subclass, to an instance of to.
func upcast(x ir.Expr, to *ir.Class) ir.Expr {
	if x.Type() == to {
		return x
	}
	return &ir.ClassCast{X: x, To: to}
}

// commonClass converts two instances to their nearest common ancestor class, if they
// have one; other values stay as they are.
func commonClass(x ir.Expr, y ir.Expr) (ir.Expr, ir.Expr) {
	a, aIsClass := x.Type().(*ir.Class)
	b, bIsClass := y.Type().(*ir.Class)
	if !aIsClass || !bIsClass {
		return x, y
	}
	for ancestor := a; ancestor != nil; ancestor = ancestor.Parent {
		if b.IsSubclassOf(ancestor) {
			return upcast(x, ancestor), upcast(y, ancestor)
		}
	}
	return x, y
}

// instanceOf lowers `x instanceof C` for an instance x and a class C of the program.
func (l *lowerer) instanceOf(node *ast.Node) ir.Expr {
	expr := node.AsBinaryExpression()
	class, ok := l.classes[l.target(expr.Right)]
	if !ok {
		l.notSupportedYet(expr.Right, "`instanceof` of anything but a class of the program is")
		return nil
	}
	x := l.expression(expr.Left)
	switch {
	case x == nil || class == nil:
		return nil // reported, or the class failed, which was
	case !isClass(x.Type()):
		l.notSupportedYet(expr.Left, fmt.Sprintf("`instanceof` on a %s is", x.Type()))
		return nil
	}
	return &ir.InstanceOf{X: x, Class: class}
}
