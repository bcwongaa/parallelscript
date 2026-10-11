package lower

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/pts/ir"
)

// declareClasses makes the IR class of every class a module declares at its top level,
// still without members, so that any type can name any class.
func (l *lowerer) declareClasses(file *ast.SourceFile) {
	for _, statement := range file.Statements.Nodes {
		if statement.Kind == ast.KindClassDeclaration {
			l.classes[l.declaredSymbol(statement)] = l.declareClass(statement)
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
	case ast.GetExtendsHeritageClauseElement(node) != nil:
		l.notSupportedYet(ast.GetExtendsHeritageClauseElement(node), "inheritance (`extends`) is")
		return nil
	case ast.HasSyntacticModifier(node, ast.ModifierFlagsAbstract):
		l.notSupportedYet(node.Name(), "abstract classes are")
		return nil
	case ast.HasSyntacticModifier(node, ast.ModifierFlagsAmbient):
		l.notSupportedYet(node.Name(), "`declare` is")
		return nil
	}
	return &ir.Class{Name: node.Name().Text(), Module: l.function.Module}
}

// declareClassMembers makes the fields of each class a module declares, and the IR
// functions of its constructor and methods, whose bodies are lowered later.
func (l *lowerer) declareClassMembers(file *ast.SourceFile) {
	for _, statement := range file.Statements.Nodes {
		if statement.Kind != ast.KindClassDeclaration {
			continue
		}
		if class := l.classes[l.declaredSymbol(statement)]; class != nil {
			l.declareMembers(statement, class)
		}
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
			field := &ir.Field{Name: member.Name().Text(), Class: class}
			if field.Type = l.bindingType(l.declaredSymbol(member), member.Name()); field.Type != nil {
				class.Fields = append(class.Fields, field)
				l.fields[member] = field
			}
		case ast.KindMethodDeclaration:
			if member.Body() != nil { // not an overload signature
				method := l.declareFunction(member, class.Name+"."+member.Name().Text(), class)
				l.methods[member] = method
				if method != nil {
					class.Methods = append(class.Methods, method)
				}
			}
		case ast.KindConstructor:
			if member.Body() != nil {
				class.Constructor = l.declareFunction(member, class.Name+".constructor", class)
			}
		}
	}
	if class.Constructor == nil && !hasConstructor(node) {
		class.Constructor = l.newMethod(class, class.Name+".constructor", l.pos(node))
	}
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
		var body *ast.Node
		for _, member := range node.Members() {
			if member.Kind == ast.KindConstructor && member.Body() != nil {
				body = member.Body()
			}
		}
		l.inFunction(class.Constructor, func() {
			stmts := l.fieldInitializers(node, class)
			if body != nil {
				stmts = append(stmts, l.block(body, body.Statements()).Stmts...)
			}
			class.Constructor.Body = &ir.Block{At: class.Constructor.Pos, Stmts: stmts}
		})
	}
	for _, member := range node.Members() {
		if member.Kind == ast.KindMethodDeclaration && member.Body() != nil {
			if method := l.methods[member]; method != nil {
				l.inFunction(method, func() { method.Body = l.block(member.Body(), member.Body().Statements()) })
			}
		}
	}
}

// fieldInitializers start a constructor: each field in declaration order gets its
// initialiser's value, or the zero value of its type ("" for a string, undefined for a
// dynamic value; numbers and booleans are zero already), as `let x: T;` does.
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

// instanceMethodCall lowers `o.m(args)` for a method m of a class of the program.
func (l *lowerer) instanceMethodCall(call *ast.CallExpression, method *ir.Func) ir.Expr {
	receiver := l.expression(call.Expression.Expression())
	args := l.arguments(call.Arguments.Nodes, method.Params[1:])
	if receiver == nil || args == nil {
		return nil
	}
	return &ir.Call{Func: method, Args: append([]ir.Expr{receiver}, args...)}
}

// fieldAccess lowers `o.f` for a field f of a class of the program.
func (l *lowerer) fieldAccess(node *ast.Node, field *ir.Field) ir.Expr {
	object := l.fieldObject(node)
	if object == nil {
		return nil
	}
	return &ir.FieldRef{X: object, Field: field}
}

// fieldObject lowers the instance o of a field access `o.f`, or reports it and returns nil.
func (l *lowerer) fieldObject(node *ast.Node) ir.Expr {
	if ast.IsOptionalChain(node) {
		l.notSupportedYet(node, "optional chaining `?.` is")
		return nil
	}
	return l.expression(node.Expression())
}
