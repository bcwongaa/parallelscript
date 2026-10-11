package lower

import (
	"fmt"
	"strings"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/scanner"
	"github.com/microsoft/typescript-go/pts/ir"
)

// declareClasses makes the IR class of every class a module declares at its top level,
// still without superclass or members, so that any type can name any class.
func (l *lowerer) declareClasses(file *ast.SourceFile) {
	for _, statement := range file.Statements.Nodes {
		if statement.Kind == ast.KindClassDeclaration && statement.TypeParameterList() != nil && statement.Name() != nil {
			l.declareGenericClass(statement)
			continue
		}
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
	if _, ok := l.genericClasses[parentSymbol]; ok { // C extends Box<int>: an instance of the generic class
		bases := l.checker.GetBaseTypes(l.checker.GetDeclaredTypeOfSymbol(symbol))
		if len(bases) == 1 {
			class.Parent, _ = l.irType(bases[0], heritage).(*ir.Class)
		}
		if class.Parent == nil {
			l.classes[symbol] = nil // reported with the generic class or its instance
		}
		return
	}
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
		l.ensureMembers(class)
	}
	for i := 0; i < len(l.instanceClasses); i++ { // instances made so far; declaring members may make more
		l.ensureMembers(l.instanceClasses[i])
	}
	for _, class := range append(l.classList, l.instanceClasses...) {
		l.linkOverrides(class)
	}
	l.membersDeclared = true
}

func (l *lowerer) declareMembers(node *ast.Node, class *ir.Class) {
	for _, param := range parameterProperties(node) {
		l.declareField(param, class)
	}
	for _, member := range node.Members() {
		if member.Kind == ast.KindSemicolonClassElement {
			continue
		}
		if what := unsupportedMember(member); what != "" {
			l.notSupportedYet(nameOf(member), what)
			continue
		}
		switch {
		case ast.IsStatic(member):
			l.declareStaticMember(member, class)
			continue
		}
		switch member.Kind {
		case ast.KindPropertyDeclaration:
			if ast.HasSyntacticModifier(member, ast.ModifierFlagsAbstract) {
				l.declareAbstractField(member, class)
			} else {
				l.declareField(member, class)
			}
		case ast.KindMethodDeclaration, ast.KindGetAccessor, ast.KindSetAccessor:
			l.declareMethod(member, class)
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

// methodKinds are the kinds of methods each declaration declares.
var methodKinds = map[ast.Kind]ir.MethodKind{ast.KindMethodDeclaration: ir.Plain, ast.KindGetAccessor: ir.Getter, ast.KindSetAccessor: ir.Setter}

// accessorPrefixes start the names of getters' and setters' functions: "Point.get_x".
var accessorPrefixes = map[ir.MethodKind]string{ir.Getter: "get_", ir.Setter: "set_"}

// declareMethod makes the method a method, getter or setter declaration declares.
func (l *lowerer) declareMethod(member *ast.Node, class *ir.Class) {
	abstract := ast.HasSyntacticModifier(member, ast.ModifierFlagsAbstract)
	if member.Body() == nil && !abstract {
		return // an overload signature
	}
	name, kind := member.Name().Text(), methodKinds[member.Kind]
	var method *ir.Method
	if fn := l.declareFunction(member, class.Name+"."+accessorPrefixes[kind]+name, class); fn != nil {
		method = &ir.Method{Name: name, Kind: kind, Class: class, Func: fn, Abstract: abstract, Virtual: abstract}
		l.addMethod(method, member)
	}
	l.declaringMembers().methods[member] = method
}

// addMethod adds a method to its class; declaration is where errors about it go.
func (l *lowerer) addMethod(method *ir.Method, declaration *ast.Node) {
	method.Class.Methods = append(method.Class.Methods, method)
	l.methodDeclarations[method] = declaration
}

// declareAbstractField makes an abstract field's getter and, unless it is readonly, its
// setter: abstract methods that subclasses implement with a field or accessors.
func (l *lowerer) declareAbstractField(member *ast.Node, class *ir.Class) {
	t := l.bindingType(l.declaredSymbol(member), member.Name())
	if t == nil {
		return
	}
	name, pos := member.Name().Text(), l.pos(member)
	var accessors property
	accessors.getter = &ir.Method{Name: name, Kind: ir.Getter, Class: class, Func: l.newMethod(class, class.Name+".get_"+name, pos), Abstract: true, Virtual: true}
	accessors.getter.Func.Result = t
	l.addMethod(accessors.getter, member)
	if !ast.HasSyntacticModifier(member, ast.ModifierFlagsReadonly) {
		accessors.setter = &ir.Method{Name: name, Kind: ir.Setter, Class: class, Func: l.newSetter(class, class.Name+".set_"+name, t, pos), Abstract: true, Virtual: true}
		l.addMethod(accessors.setter, member)
	}
	l.declaringMembers().abstractFields[member] = accessors
}

// newSetter makes an IR function of class taking `this` and a value of type t, with no body yet.
func (l *lowerer) newSetter(class *ir.Class, name string, t ir.Type, pos ir.Pos) *ir.Func {
	setter := l.newMethod(class, name, pos)
	value := &ir.Local{Name: "value", Type: t}
	l.owners[value] = setter
	setter.Params = append(setter.Params, value)
	return setter
}

// declareStaticMember makes the variable of a static field (a module-scope binding of
// the class's module, initialised where the class declaration runs) or the function of a
// static method. Static blocks run with the field initialisers.
func (l *lowerer) declareStaticMember(member *ast.Node, class *ir.Class) {
	switch member.Kind {
	case ast.KindPropertyDeclaration:
		t := l.bindingType(l.declaredSymbol(member), member.Name())
		if t == nil {
			return
		}
		field := &ir.Local{Name: class.Name + "." + member.Name().Text(), Type: t, Global: true}
		l.owners[field] = class.Module.Init
		l.moduleScope[field] = true
		l.statics[member] = field
	case ast.KindMethodDeclaration:
		if member.Body() == nil {
			return // an overload signature
		}
		fn := l.declareFunction(member, class.Name+"."+member.Name().Text(), nil)
		l.staticMethods[member] = fn
		if fn != nil {
			l.staticClasses[fn] = class
			fn.JSName = member.Name().Text()
		}
	}
}

// staticInitializers run where a class declaration is: each static field's initialiser
// (or its type's zero value) and each static block, in declaration order, with `this`
// standing for the class.
func (l *lowerer) staticInitializers(node *ast.Node, class *ir.Class) []ir.Stmt {
	outer := l.staticContext
	l.staticContext = class
	defer func() { l.staticContext = outer }()
	var stmts []ir.Stmt
	for _, member := range node.Members() {
		switch {
		case member.Kind == ast.KindClassStaticBlockDeclaration:
			body := member.AsClassStaticBlockDeclaration().Body
			stmts = append(stmts, l.block(body, body.Statements()))
		case l.statics[member] != nil:
			field := l.statics[member]
			var value ir.Expr
			if initializer := member.Initializer(); initializer != nil {
				if value = l.expression(initializer); value == nil {
					continue
				}
				if value = l.convert(value, field.Type, initializer); value == nil {
					continue
				}
			}
			stmts = append(stmts, &ir.LocalDecl{At: l.pos(member), Local: field, Init: value})
		}
	}
	return stmts
}

// staticClass is the class `this` stands for in the code being lowered, a static
// member's, or nil outside static members.
func (l *lowerer) staticClass() *ir.Class {
	if l.staticContext != nil {
		return l.staticContext
	}
	return l.staticClasses[l.thisFunction()]
}

// staticOwner returns the class a static member access `C.m` (or `this.m` in a static
// member) goes through, or reports that its object isn't a class and returns nil.
func (l *lowerer) staticOwner(node *ast.Node) *ir.Class {
	object := ast.SkipParentheses(node.Expression())
	if object.Kind == ast.KindThisKeyword {
		return l.staticClass()
	}
	if class, ok := l.classes[l.target(object)]; ok && (object.Kind == ast.KindIdentifier || object.Kind == ast.KindPropertyAccessExpression) {
		return class // nil if the class failed, which was reported
	}
	l.notSupportedYet(object, "a static member of anything but a class name or `this` is")
	return nil
}

// staticField lowers `C.f` (or `this.f`) for a static field f to its variable, or reports
// why it can't and returns nil. Assigning through a subclass would give the subclass a
// field of its own in JS, so only reads may go through one.
func (l *lowerer) staticField(node *ast.Node, field *ir.Local, assigned bool) *ir.Local {
	owner := l.staticOwner(node)
	switch {
	case owner == nil || field == nil:
		return nil // reported
	case assigned && owner != l.classes[l.declaredSymbol(ast.GetContainingClass(l.memberDeclaration(node)))]:
		l.notSupportedYet(node, "assigning to an inherited static field through a subclass is")
		return nil
	}
	return l.ownLocal(node, field)
}

// staticCall lowers `C.m(args)` (or `this.m(args)`) for a static method m. Through a
// subclass, `this` in m would be the subclass, so that only works if m doesn't use `this`.
func (l *lowerer) staticCall(call *ast.CallExpression, fn *ir.Func) ir.Expr {
	callee := ast.SkipParentheses(call.Expression)
	declaration := l.memberDeclaration(callee)
	owner := l.staticOwner(callee)
	switch {
	case owner == nil || fn == nil:
		return nil // reported
	case owner != l.staticClasses[fn] && usesThis(declaration.Body()):
		l.notSupportedYet(callee, "calling a static method that uses `this` through a subclass is")
		return nil
	}
	return l.userCall(call, fn)
}

// usesThis reports whether `this` appears under node, outside nested functions and
// classes, which have their own.
func usesThis(node *ast.Node) bool {
	found := false
	node.ForEachChild(func(child *ast.Node) bool {
		switch child.Kind {
		case ast.KindThisKeyword:
			found = true
		case ast.KindFunctionDeclaration, ast.KindFunctionExpression, ast.KindClassDeclaration, ast.KindClassExpression:
		default:
			found = found || usesThis(child)
		}
		return found
	})
	return found
}

// parameterProperties are the parameters of a class's constructor that declare fields
// (`constructor(public x: number)`).
func parameterProperties(node *ast.Node) []*ast.Node {
	var params []*ast.Node
	for _, member := range node.Members() {
		if member.Kind == ast.KindConstructor && member.Body() != nil {
			for _, param := range member.Parameters() {
				if ast.IsParameterPropertyDeclaration(param, member) {
					params = append(params, param)
				}
			}
		}
	}
	return params
}

// declareField makes the field a property declaration declares. A field that redeclares
// an inherited one (to give it another initialiser) is the inherited field.
func (l *lowerer) declareField(member *ast.Node, class *ir.Class) {
	t := l.bindingType(l.declaredSymbol(member), member.Name())
	if t == nil {
		return
	}
	field := &ir.Field{Name: member.Name().Text(), Type: t, Class: class}
	if class.Parent != nil && !isPrivateName(field.Name) {
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
		l.implementAbstractAccessors(field, member)
	}
	l.declaringMembers().fields[member] = field
}

// implementAbstractAccessors implements, with a field a class declares, the abstract
// getter and setter of the same name it inherits (from an abstract field, say).
func (l *lowerer) implementAbstractAccessors(field *ir.Field, member *ast.Node) {
	class, pos := field.Class, l.pos(member)
	if class.Parent == nil {
		return
	}
	if class.Parent.Method(field.Name, ir.Getter) != nil {
		getter := l.newMethod(class, class.Name+".get_"+field.Name, pos)
		getter.Result = field.Type
		read := &ir.FieldRef{X: &ir.LocalRef{Local: getter.Params[0]}, Field: field}
		getter.Body = &ir.Block{At: pos, Stmts: []ir.Stmt{&ir.Return{At: pos, Value: read}}}
		l.addMethod(&ir.Method{Name: field.Name, Kind: ir.Getter, Class: class, Func: getter}, member)
		l.lowered = append(l.lowered, getter)
	}
	if class.Parent.Method(field.Name, ir.Setter) != nil {
		setter := l.newSetter(class, class.Name+".set_"+field.Name, field.Type, pos)
		store := &ir.SetField{X: &ir.LocalRef{Local: setter.Params[0]}, Field: field, Value: &ir.LocalRef{Local: setter.Params[1]}}
		setter.Body = &ir.Block{At: pos, Stmts: []ir.Stmt{&ir.ExprStmt{At: pos, X: store}}}
		l.addMethod(&ir.Method{Name: field.Name, Kind: ir.Setter, Class: class, Func: setter}, member)
		l.lowered = append(l.lowered, setter)
	}
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
	for _, method := range class.Methods {
		overridden := class.Parent.Method(method.Name, method.Kind)
		switch {
		case overridden == nil || isPrivateName(method.Name): // a subclass's #m is a name of its own
			continue
		case !sameSignature(method.Func, overridden.Func):
			l.notSupportedYet(nameOf(l.methodDeclarations[method]), fmt.Sprintf("overriding `%s.%s` with other parameter or result types is", overridden.Class.Name, method.Name))
			continue
		}
		method.Overrides = overridden
		for m := method; m != nil; m = m.Overrides {
			m.Virtual = true
		}
	}
}

// isPrivateName reports whether a member's name is a `#private` one, which only its
// class's code can use, so that a subclass's member of the same name is another member.
func isPrivateName(name string) bool {
	return strings.HasPrefix(name, "#")
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

// unsupportedMember says which class member isn't supported yet ("index signatures in classes are"), or "".
func unsupportedMember(member *ast.Node) string {
	switch {
	case member.Kind == ast.KindIndexSignature:
		return "index signatures in classes are"
	case member.Name() != nil && member.Name().Kind != ast.KindIdentifier && member.Name().Kind != ast.KindPrivateIdentifier:
		return "members with computed or quoted names are"
	case ast.IsStatic(member) && (member.Kind == ast.KindGetAccessor || member.Kind == ast.KindSetAccessor):
		return "static getters and setters are"
	case member.Kind == ast.KindPropertyDeclaration && member.PostfixToken() != nil:
		return "optional fields are"
	case ast.HasSyntacticModifier(member, ast.ModifierFlagsAccessor):
		return "auto-accessors (`accessor`) are"
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

// classBodies lowers the constructor and the method bodies of a class, static methods' too.
func (l *lowerer) classBodies(node *ast.Node) {
	if class := l.classes[l.declaredSymbol(node)]; class != nil {
		l.classBodiesOf(node, class)
	}
}

// classBodiesOf lowers the bodies of the constructor and methods class has for a class
// declaration (node). The parameters' locals are bound again, since another instance of
// a generic class may have declared its own since.
func (l *lowerer) classBodiesOf(node *ast.Node, class *ir.Class) {
	if class.Constructor != nil {
		for _, member := range node.Members() {
			if member.Kind == ast.KindConstructor && member.Body() != nil {
				l.bindParameters(member, class.Constructor)
			}
		}
		l.inFunction(class.Constructor, func() { class.Constructor.Body = l.constructorBody(node, class) })
	}
	for _, member := range node.Members() {
		if _, isMethod := methodKinds[member.Kind]; isMethod && member.Body() != nil {
			if method := l.membersOf(class).methods[member]; method != nil {
				l.bindParameters(member, method.Func)
				l.inFunction(method.Func, func() { method.Func.Body = l.block(member.Body(), member.Body().Statements()) })
			}
			if fn := l.staticMethods[member]; fn != nil {
				l.inFunction(fn, func() { fn.Body = l.block(member.Body(), member.Body().Statements()) })
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

// fieldInitializers store each parameter property's argument in its field, then give
// each field the class declares, in declaration order, its initialiser's value, or the
// zero value of its type ("" for a string, undefined for a dynamic value; numbers and
// booleans are zero already), as `let x: T;` does. A `declare` redeclaration of an
// inherited field changes nothing.
func (l *lowerer) fieldInitializers(node *ast.Node, class *ir.Class) []ir.Stmt {
	var stmts []ir.Stmt
	this := &ir.LocalRef{Local: l.this[class.Constructor]}
	for _, param := range parameterProperties(node) {
		field, arg := l.membersOf(class).fields[param], l.locals[l.checker.GetSymbolAtLocation(param.Name())]
		if field != nil && arg != nil {
			stmts = append(stmts, &ir.ExprStmt{At: l.pos(param), X: &ir.SetField{X: this, Field: field, Value: &ir.LocalRef{Local: arg}}})
		}
	}
	for _, member := range node.Members() {
		field := l.membersOf(class).fields[member]
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

// thisExpression lowers `this` in a constructor or method, or an arrow function in one.
func (l *lowerer) thisExpression(node *ast.Node) ir.Expr {
	if this := l.thisLocal(); this != nil {
		return &ir.LocalRef{Local: this}
	}
	if l.staticClass() != nil {
		l.notSupportedYet(node, "`this` as a value in a static member is")
	} else {
		l.notSupportedYet(node, "`this` outside a class is")
	}
	return nil
}

// newExpression lowers `new C(args)` for a class of the program.
func (l *lowerer) newExpression(node *ast.Node) ir.Expr {
	if l.isMapConstructor(node) {
		return l.newMap(node)
	}
	class, ok := l.classes[l.target(node.Expression())]
	if _, isGeneric := l.genericClasses[l.target(node.Expression())]; isGeneric {
		class, ok = l.genericClassReference(node), true
	}
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
	if l.function.Closure {
		l.notSupportedYet(node, "`super(...)` inside an arrow function is")
		return nil
	}
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
	return l.this[l.thisFunction()].Type.(*ir.Class)
}

// instanceMethodCall lowers `o.m(args)` for a method m of a class of the program.
func (l *lowerer) instanceMethodCall(call *ast.CallExpression, method *ir.Method) ir.Expr {
	receiverNode := call.Expression.Expression()
	if receiverNode.Kind == ast.KindSuperKeyword {
		return l.superMethodCall(call, method)
	}
	receiver := l.expression(receiverNode)
	args := l.arguments(call.Arguments.Nodes, method.Func.Params[1:])
	if receiver == nil || args == nil {
		return nil
	}
	return l.callMethod(receiver, receiverNode, method, args)
}

// callMethod calls a method (or getter or setter) on receiver, an instance of its class
// or one the checker narrowed (at receiverNode) to its class: directly when no subclass
// of the receiver's static class overrides the method, so that the implementation is
// known, and otherwise through the instance's method table.
func (l *lowerer) callMethod(receiver ir.Expr, receiverNode *ast.Node, method *ir.Method, args []ir.Expr) ir.Expr {
	if receiver = l.asInstanceOf(receiver, method.Class, receiverNode); receiver == nil {
		return nil
	}
	static := receiver.Type().(*ir.Class)
	if l.mayBeOverriddenByAnInstance(static, method) {
		for m := method; m != nil; m = m.Overrides {
			m.Virtual = true
		}
	}
	if method.Virtual && (method.Abstract || l.isOverriddenBelow(static, method) || l.mayBeOverriddenByAnInstance(static, method)) {
		receiver = upcast(receiver, method.Slot().Class)
		return &ir.VirtualCall{Method: method, Args: append([]ir.Expr{receiver}, args...)}
	}
	return &ir.Call{Func: method.Func, Args: append([]ir.Expr{upcast(receiver, method.Class)}, args...)}
}

// superMethodCall lowers `super.m(args)`: the implementation the parent has, called on `this`
// (`super.p` for a getter p is propertyRead's).
func (l *lowerer) superMethodCall(call *ast.CallExpression, method *ir.Method) ir.Expr {
	args := l.arguments(call.Arguments.Nodes, method.Func.Params[1:])
	if args == nil {
		return nil
	}
	this := upcast(&ir.LocalRef{Local: l.thisLocal()}, method.Class)
	return &ir.Call{Func: method.Func, Args: append([]ir.Expr{this}, args...)}
}

// isOverriddenBelow reports whether a subclass of class overrides method.
func (l *lowerer) isOverriddenBelow(class *ir.Class, method *ir.Method) bool {
	for _, other := range append(l.classList, l.instanceClasses...) {
		if other != class && other.IsSubclassOf(class) && other.OwnMethod(method.Name, method.Kind) != nil {
			return true
		}
	}
	return false
}

// property is an accessor property of a class: its getter and setter, either of which
// may be missing.
type property struct {
	getter, setter *ir.Method
}

// accessorProperty returns the accessor property a property access names, if it names
// one: through getter and setter declarations, or an abstract field. ok is false for
// anything else; a property whose getter or setter failed has nil there (reported).
func (l *lowerer) accessorProperty(node *ast.Node) (accessors property, ok bool) {
	if node.Kind != ast.KindPropertyAccessExpression {
		return property{}, false
	}
	symbol := l.memberSymbol(node)
	if symbol == nil {
		return property{}, false
	}
	members := l.membersFor(symbol.ValueDeclaration, node.Expression())
	for _, declaration := range symbol.Declarations {
		switch declaration.Kind {
		case ast.KindGetAccessor:
			accessors.getter, ok = members.methods[declaration], true
		case ast.KindSetAccessor:
			accessors.setter, ok = members.methods[declaration], true
		case ast.KindPropertyDeclaration:
			if abstract, isAbstract := members.abstractFields[declaration]; isAbstract {
				accessors, ok = abstract, true
			}
		}
	}
	return accessors, ok
}

// propertyRead lowers `o.p` for an accessor property p: a call of its getter.
func (l *lowerer) propertyRead(node *ast.Node, accessors property) ir.Expr {
	if accessors.getter == nil {
		l.notSupportedYet(node, "reading a property that has no getter is")
		return nil
	}
	if ast.IsOptionalChain(node) {
		l.notSupportedYet(node, "optional chaining `?.` is")
		return nil
	}
	if node.Expression().Kind == ast.KindSuperKeyword { // the parent's getter, called on `this`
		this := upcast(&ir.LocalRef{Local: l.thisLocal()}, accessors.getter.Class)
		return &ir.Call{Func: accessors.getter.Func, Args: []ir.Expr{this}}
	}
	object := l.expression(node.Expression())
	if object == nil {
		return nil
	}
	return l.callMethod(object, node.Expression(), accessors.getter, nil)
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
	narrowed, ok := l.irType(l.typeAt(node), node).(*ir.Class)
	return ok && narrowed != nil && narrowed.IsSubclassOf(class)
}

// upcast converts x, an instance of to or of a subclass, to an instance of to.
func upcast(x ir.Expr, to *ir.Class) ir.Expr {
	if x.Type() == to {
		return x
	}
	return &ir.ClassCast{X: x, To: to}
}

// commonClass converts two references to objects to one type, if they have one: their
// classes' nearest common ancestor, or an interface that one is and the other provides.
// Other values stay as they are.
func commonClass(x ir.Expr, y ir.Expr) (ir.Expr, ir.Expr) {
	if iface, ok := x.Type().(*ir.Interface); ok && isObject(y.Type()) && y.Type() != iface && unprovided(y.Type(), iface) == "" {
		return x, &ir.ClassCast{X: y, To: iface}
	}
	if iface, ok := y.Type().(*ir.Interface); ok && isObject(x.Type()) && x.Type() != iface && unprovided(x.Type(), iface) == "" {
		return &ir.ClassCast{X: x, To: iface}, y
	}
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

// privateIn lowers `#f in o`: whether o has the private member, which only instances of
// the class that declares it (and of its subclasses) have.
func (l *lowerer) privateIn(node *ast.Node) ir.Expr {
	expr := node.AsBinaryExpression()
	declaring := ast.GetContainingClass(l.checker.GetSymbolAtLocation(expr.Left).ValueDeclaration)
	class := l.classes[l.declaredSymbol(declaring)]
	if _, isGeneric := l.genericClasses[l.declaredSymbol(declaring)]; isGeneric {
		class = l.currentClass() // `#f in o` is in the class's own code, so in the instance being lowered
	}
	x := l.expression(expr.Right)
	switch {
	case x == nil || class == nil:
		return nil
	case !isObject(x.Type()):
		l.notSupportedYet(expr.Right, fmt.Sprintf("`#private in` on a %s is", x.Type()))
		return nil
	}
	return &ir.InstanceOf{X: x, Class: class}
}

// instanceOf lowers `x instanceof C` for an instance x and a class C of the program.
func (l *lowerer) instanceOf(node *ast.Node) ir.Expr {
	expr := node.AsBinaryExpression()
	class, ok := l.classes[l.target(expr.Right)]
	if _, isGeneric := l.genericClasses[l.target(expr.Right)]; isGeneric {
		l.notSupportedYet(expr.Right, "`instanceof` of a generic class is")
		return nil
	}
	if !ok {
		l.notSupportedYet(expr.Right, "`instanceof` of anything but a class of the program is")
		return nil
	}
	x := l.expression(expr.Left)
	switch {
	case x == nil || class == nil:
		return nil // reported, or the class failed, which was
	case !isObject(x.Type()):
		l.notSupportedYet(expr.Left, fmt.Sprintf("`instanceof` on a %s is", x.Type()))
		return nil
	}
	return &ir.InstanceOf{X: x, Class: class}
}
