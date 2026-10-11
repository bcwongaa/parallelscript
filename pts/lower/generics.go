package lower

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"unicode"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/pts/ir"
)

// Generic functions are monomorphized (D-049): each distinct list of IR types a generic
// function declaration is called with makes an instance, a function of its own whose
// signature and body are lowered with the declaration's type parameters replaced by the
// call's type arguments, in every checker type the lowering asks about.

// maxInstances bounds the instances of one generic function, which a function that calls
// itself with ever larger type arguments (f<T> calling f<T[]>) would make without end.
const maxInstances = 64

// genericFunc is a generic function or method declaration and its instances so far.
type genericFunc struct {
	node       *ast.Node
	name       string          // its qualified IR name, which an instance's extends
	module     *ir.Func        // the top-level code of its module
	parameters []*checker.Type // its type parameters
	instances  []*instance
	class      *ir.Class     // for a method, the class whose instances it runs on (`this`)
	static     *ir.Class     // for a static method, its class
	outer      *substitution // for a method of a generic class's instance, the instance's
}

// instance is a generic function's instance for a list of type arguments.
type instance struct {
	generic   *genericFunc
	arguments []ir.Type
	fn        *ir.Func // nil if it failed, which was reported
	subst     *substitution
}

// substitution replaces type parameters by type arguments in the checker types of an
// instance's declaration.
type substitution struct {
	declaration *ast.Node
	parameters  []*checker.Type
	arguments   []*checker.Type
}

// instantiate is t in the instance being lowered, if any: with its type parameters replaced.
func (l *lowerer) instantiate(t *checker.Type) *checker.Type {
	if l.subst == nil {
		return t
	}
	return l.checker.PtsInstantiateType(t, l.subst.parameters, l.subst.arguments)
}

// typeAt, typeOfSymbol, contextualType and returnTypeOf are the checker's answers,
// instantiated for the instance being lowered.
func (l *lowerer) typeAt(node *ast.Node) *checker.Type {
	return l.instantiate(l.checker.GetTypeAtLocation(node))
}

func (l *lowerer) typeOfSymbol(symbol *ast.Symbol) *checker.Type {
	return l.instantiate(l.checker.GetTypeOfSymbol(symbol))
}

func (l *lowerer) contextualType(node *ast.Node) *checker.Type {
	if isDestructuringSource(node) {
		return nil // the type tsc derives from the pattern (`{ x: any }`) says nothing about the value
	}
	if t := l.checker.GetContextualType(node, checker.ContextFlagsNone); t != nil {
		return l.instantiate(t)
	}
	return nil
}

func (l *lowerer) returnTypeOf(signature *checker.Signature) *checker.Type {
	return l.instantiate(l.checker.GetReturnTypeOfSignature(signature))
}

// isGeneric reports whether a declaration has type parameters of its own, other than as
// the instance being lowered.
func (l *lowerer) isGeneric(declaration *ast.Node) bool {
	return declaration.TypeParameterList() != nil && (l.subst == nil || l.subst.declaration != declaration)
}

// declareGeneric records a generic function declaration (named qualified) at module
// scope, whose instances are made as calls need them. Nested ones are reported.
func (l *lowerer) declareGeneric(node *ast.Node, qualified string) {
	if l.subst != nil {
		l.notSupportedYet(nameOf(node), "generic functions inside generic functions or classes are")
		l.generics[l.declaredSymbol(node)] = nil
		return
	}
	generic := &genericFunc{node: node, name: qualified, module: l.function}
	for _, parameter := range node.TypeParameters() {
		generic.parameters = append(generic.parameters, l.checker.GetDeclaredTypeOfSymbol(parameter.Symbol()))
	}
	l.generics[l.declaredSymbol(node)] = generic
}

// genericCall lowers a call of a generic function or static method: a call of its
// instance for the type arguments tsc inferred or the call gives.
func (l *lowerer) genericCall(call *ast.CallExpression, generic *genericFunc) ir.Expr {
	fn := l.calledInstance(call, generic)
	if fn == nil {
		return nil
	}
	return l.userCall(call, fn)
}

// genericMethodCall lowers `o.m(args)` for a generic method m of a class: a direct call
// of m's instance for the call's type arguments, on o.
func (l *lowerer) genericMethodCall(call *ast.CallExpression, generic *genericFunc) ir.Expr {
	fn := l.calledInstance(call, generic)
	if fn == nil {
		return nil
	}
	receiverNode := call.Expression.Expression()
	var receiver ir.Expr
	if receiverNode.Kind == ast.KindSuperKeyword {
		receiver = &ir.LocalRef{Local: l.thisLocal()}
	} else if receiver = l.expression(receiverNode); receiver != nil {
		receiver = l.asInstanceOf(receiver, generic.class, receiverNode)
	}
	args := l.arguments(call.Arguments.Nodes, fn.Params[1:])
	if receiver == nil || args == nil {
		return nil
	}
	return &ir.Call{Func: fn, Args: append([]ir.Expr{upcast(receiver, generic.class)}, args...)}
}

// calledInstance is the instance of a generic function or method that a call calls,
// for the type arguments of tsc's resolved signature; nil if it failed (reported).
func (l *lowerer) calledInstance(call *ast.CallExpression, generic *genericFunc) *ir.Func {
	arguments := l.checker.PtsTypeArgumentsOfSignature(l.checker.GetResolvedSignature(call.AsNode()))
	if len(arguments) != len(generic.parameters) {
		l.notSupportedYet(call.AsNode(), "this call of a generic function is")
		return nil
	}
	return l.genericInstance(generic, arguments, call.AsNode())
}

// genericMethodAt returns the generic method a callee (`o.m` or `C.m`) names, or nil.
func (l *lowerer) genericMethodAt(callee *ast.Node) *genericFunc {
	if callee.Kind != ast.KindPropertyAccessExpression {
		return nil
	}
	member := l.memberDeclaration(callee)
	if generic := l.membersAt(callee).genericMethods[member]; generic != nil {
		return generic
	}
	return l.genericStatics[member]
}

// declareGenericMethod records a generic method (or a static one) of class, whose
// instances calls make. One that overrides or is abstract is reported.
func (l *lowerer) declareGenericMethod(member *ast.Node, class *ir.Class) {
	name := member.Name().Text()
	switch {
	case member.Kind != ast.KindMethodDeclaration || member.Body() == nil:
		l.notSupportedYet(nameOf(member), "generic accessors and abstract generic methods are")
		return
	case !ast.IsStatic(member) && class.Parent != nil && (class.Parent.Method(name, ir.Plain) != nil || l.inheritedGenericMethod(class.Parent, name) != nil):
		l.notSupportedYet(nameOf(member), "overriding with a generic method is")
		return
	}
	generic := &genericFunc{node: member, name: class.Name + "." + name, module: class.Module.Init, class: class, outer: l.subst}
	for _, parameter := range member.TypeParameters() {
		generic.parameters = append(generic.parameters, l.checker.GetDeclaredTypeOfSymbol(parameter.Symbol()))
	}
	if ast.IsStatic(member) {
		generic.class, generic.static = nil, class
		l.genericStatics[member] = generic
		return
	}
	l.declaringMembers().genericMethods[member] = generic
}

// inheritedGenericMethod returns the generic method of the given name that class declares
// or inherits, or nil.
func (l *lowerer) inheritedGenericMethod(class *ir.Class, name string) *genericFunc {
	for ; class != nil; class = class.Parent {
		for member, generic := range l.membersOf(class).genericMethods {
			if generic.class == class && member.Name().Text() == name {
				return generic
			}
		}
	}
	return nil
}

// genericInstance returns the function of a generic function's instance for some type
// arguments (in terms of the instance being lowered, if any), making it if it is new; nil
// if it failed (reported at node).
func (l *lowerer) genericInstance(generic *genericFunc, typeArguments []*checker.Type, node *ast.Node) *ir.Func {
	arguments := make([]ir.Type, len(typeArguments))
	concrete := make([]*checker.Type, len(typeArguments))
	for i, t := range typeArguments {
		concrete[i] = l.instantiate(t)
		if arguments[i] = l.irType(concrete[i], node); arguments[i] == nil {
			return nil
		}
	}
	for _, existing := range generic.instances {
		if slices.Equal(existing.arguments, arguments) {
			return existing.fn
		}
	}
	if len(generic.instances) == maxInstances {
		l.notSupportedYet(node, fmt.Sprintf("a generic function with more than %d instances (`%s`) is", maxInstances, generic.name))
		return nil
	}
	subst := &substitution{declaration: generic.node, parameters: generic.parameters, arguments: concrete}
	if outer := generic.outer; outer != nil {
		subst.parameters = append(slices.Clone(outer.parameters), subst.parameters...)
		subst.arguments = append(slices.Clone(outer.arguments), subst.arguments...)
	}
	inst := &instance{generic: generic, arguments: arguments, subst: subst}
	generic.instances = append(generic.instances, inst)
	outerSubst, outerFunction, outerParams := l.subst, l.function, l.parameterLocals(generic.node)
	l.subst, l.function = inst.subst, generic.module
	inst.fn = l.declareFunction(generic.node, generic.name+"_"+typeArgumentsName(arguments), generic.class)
	l.subst, l.function = outerSubst, outerFunction
	maps.Copy(l.locals, outerParams) // the instance being lowered may be another of this function's
	if inst.fn != nil {
		inst.fn.JSName = generic.name
		if generic.static != nil {
			l.staticClasses[inst.fn] = generic.static
		}
		l.instances = append(l.instances, inst)
	}
	return inst.fn
}

// instanceBody lowers an instance's body, and the bodies of the functions declared in it,
// whose declarations are the instance's own.
func (l *lowerer) instanceBody(inst *instance) {
	node, fn := inst.generic.node, inst.fn
	l.subst = inst.subst
	defer func() { l.subst = nil }()
	l.forgetLocals(node) // another instance's
	l.bindParameters(node, fn)
	outerBodies, outerFunction := l.bodies, l.function
	l.bodies, l.function = nil, inst.generic.module
	l.declareFunctions(node.Body(), fn.Name)
	l.function = outerFunction
	l.inFunction(fn, func() { fn.Body = l.block(node.Body(), node.Body().Statements()) })
	for len(l.bodies) > 0 {
		body := l.bodies[0]
		l.bodies = l.bodies[1:]
		l.functionBody(body)
	}
	l.bodies = outerBodies
}

// typeArgumentsName names an instance's type arguments for its function's name:
// "number", "string_array", "int_string".
func typeArgumentsName(arguments []ir.Type) string {
	names := make([]string, len(arguments))
	for i, t := range arguments {
		name := strings.ReplaceAll(t.String(), "[]", "_array")
		name = strings.Map(func(r rune) rune {
			if r < unicode.MaxASCII && (unicode.IsLetter(r) || unicode.IsDigit(r)) {
				return r
			}
			return '_'
		}, name)
		for strings.Contains(name, "__") {
			name = strings.ReplaceAll(name, "__", "_")
		}
		names[i] = strings.Trim(name, "_")
	}
	return strings.Join(names, "_")
}

// forgetLocals forgets the locals of the declarations under node, which belong to
// another instance of the generic function node is in.
func (l *lowerer) forgetLocals(node *ast.Node) {
	node.ForEachChild(func(child *ast.Node) bool {
		if ast.IsDeclaration(child) && child.Name() != nil && child.Name().Kind == ast.KindIdentifier {
			delete(l.locals, l.declaredSymbol(child))
		}
		l.forgetLocals(child)
		return false
	})
}

// parameterLocals are the locals a function's parameters have now, by symbol.
func (l *lowerer) parameterLocals(node *ast.Node) map[*ast.Symbol]*ir.Local {
	locals := map[*ast.Symbol]*ir.Local{}
	for _, param := range node.Parameters() {
		symbol := l.checker.GetSymbolAtLocation(param.Name())
		if local, ok := l.locals[symbol]; ok {
			locals[symbol] = local
		}
	}
	return locals
}

// bindParameters makes the locals of a function's parameters (after `this`, for a method
// or constructor) those of its declaration's parameters again: the declaration of another
// instance of a generic function or class may have bound them since.
func (l *lowerer) bindParameters(declaration *ast.Node, fn *ir.Func) {
	params := fn.Params
	if l.this[fn] != nil {
		params = params[1:]
	}
	for i, param := range declaration.Parameters() {
		if i < len(params) && param.Name().Kind == ast.KindIdentifier {
			l.locals[l.checker.GetSymbolAtLocation(param.Name())] = params[i]
		}
	}
}
