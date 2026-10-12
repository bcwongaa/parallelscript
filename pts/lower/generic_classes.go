package lower

import (
	"fmt"
	"slices"
	"strings"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/internal/scanner"
	"github.com/microsoft/typescript-go/pts/ir"
)

// classMembers are the members of classes by declaration. A generic class's instances
// each have their own (D-050); the other classes share the lowerer's.
type classMembers struct {
	fields         map[*ast.Node]*ir.Field    // by property declaration or parameter property (a redeclaration's is the inherited field)
	methods        map[*ast.Node]*ir.Method   // methods, getters and setters, by declaration; nil if it failed
	abstractFields map[*ast.Node]property     // the getter and setter of each abstract field, by declaration
	genericMethods map[*ast.Node]*genericFunc // generic methods, whose instances calls make
}

func newClassMembers() *classMembers {
	return &classMembers{fields: map[*ast.Node]*ir.Field{}, methods: map[*ast.Node]*ir.Method{}, abstractFields: map[*ast.Node]property{},
		genericMethods: map[*ast.Node]*genericFunc{}}
}

// Generic classes are monomorphized like generic functions (D-049, D-050): each list of
// IR types a generic class is instantiated with makes a class of its own, named as the
// generic class (console.log prints `Box { value: 1 }` for every instance), whose
// members are declared, and whose constructor and methods are lowered, with the class's
// type parameters replaced by the type arguments.

// genericClass is a generic class declaration and its instances so far.
type genericClass struct {
	node       *ast.Node
	module     *ir.Func // the top-level code of its module
	parameters []*checker.Type
	instances  []*classInstance
	classes    []*ir.Class // its instances' classes, for `instanceof`
	statics    *ir.Class   // the class of its static members, which all its instances share
}

// classInstance is a generic class's instance for a list of type arguments.
type classInstance struct {
	generic   *genericClass
	arguments []ir.Type
	class     *ir.Class // nil if it failed, which was reported
	subst     *substitution
	members   *classMembers
}

// declaringMembers are the tables the members being declared go to.
func (l *lowerer) declaringMembers() *classMembers {
	if l.declaring != nil {
		return l.declaring
	}
	return l.members
}

// membersOf returns the tables that hold class's members: a generic class instance's
// own, or the program's.
func (l *lowerer) membersOf(class *ir.Class) *classMembers {
	if inst := l.classInstances[class]; inst != nil {
		return inst.members
	}
	return l.members
}

// membersAt returns the tables that hold the member a property access names.
func (l *lowerer) membersAt(access *ast.Node) *classMembers {
	if access.Kind != ast.KindPropertyAccessExpression {
		return l.members
	}
	return l.membersFor(l.memberDeclaration(access), access.Expression())
}

// membersFor returns the tables that hold member, a declaration of a class member,
// reached through receiver (the object, `this` or `super`): for a member of a generic
// class, the tables of the instance that receiver's class is or extends.
func (l *lowerer) membersFor(member *ast.Node, receiver *ast.Node) *classMembers {
	if member == nil {
		return l.members
	}
	declaring := ast.GetContainingClass(member)
	if declaring == nil || ast.IsStatic(member) {
		return l.members // a static member is the generic class's own, which its instances share
	}
	if _, ok := l.genericClasses[l.declaredSymbol(declaring)]; !ok {
		return l.members
	}
	for class := l.receiverClass(receiver); class != nil; class = class.Parent {
		if inst := l.classInstances[class]; inst != nil && inst.generic.node == declaring {
			return inst.members
		}
	}
	return newClassMembers() // nothing: the access is reported as unsupported
}

// receiverClass is the class of the object an expression gives, if it is an instance of
// a class: for `this` (or a value of a type parameter), its constraint's.
func (l *lowerer) receiverClass(receiver *ast.Node) *ir.Class {
	t := l.checker.GetTypeAtLocation(receiver)
	if object, ok := l.chainObjects[receiver]; ok {
		t = object.checkerType // the object of a `?.`, which is neither null nor undefined (D-064)
	}
	if t.Flags()&checker.TypeFlagsTypeParameter != 0 {
		if constraint := l.checker.GetBaseConstraintOfType(t); constraint != nil {
			t = constraint
		}
	}
	class, _ := l.irType(t, receiver).(*ir.Class)
	return class
}

// declareGenericClass records a generic class declaration, whose instances the types
// that name it make. Its static members, which can't mention its type parameters, belong
// to one class of statics that every instance shares, as in JS (D-089).
func (l *lowerer) declareGenericClass(node *ast.Node) {
	generic := &genericClass{node: node, module: l.function, statics: &ir.Class{Name: node.Name().Text(), Module: l.function.Module}}
	for _, parameter := range node.TypeParameters() {
		generic.parameters = append(generic.parameters, l.checker.GetDeclaredTypeOfSymbol(parameter.Symbol()))
	}
	l.genericClasses[l.declaredSymbol(node)] = generic
	l.genericClassList = append(l.genericClassList, generic)
}

// declareGenericStatics declares a generic class's static members in its class of statics.
func (l *lowerer) declareGenericStatics(generic *genericClass) {
	outer := l.function
	l.function = generic.module
	defer func() { l.function = outer }()
	for _, member := range generic.node.Members() {
		if ast.IsStatic(member) && unsupportedMember(member) == "" {
			l.declareStaticMember(member, generic.statics)
		}
	}
}

// classInstanceOf returns the class of a generic class's instance for the type arguments
// of t, a reference to it (in terms of the instance being lowered, if any), making it if
// it is new; nil if it failed (reported at node).
// instanceDisplay is a generic class instance's name with its type arguments as tsc
// writes them (`Box<number>`), which conversion errors show (D-147).
func (l *lowerer) instanceDisplay(name string, arguments []*checker.Type) string {
	shown := make([]string, len(arguments))
	for i, argument := range arguments {
		shown[i] = l.checker.TypeToString(argument)
	}
	return name + "<" + strings.Join(shown, ", ") + ">"
}

func (l *lowerer) classInstanceOf(generic *genericClass, t *checker.Type, node *ast.Node) *ir.Class {
	typeArguments := l.checker.GetTypeArguments(t)
	if len(typeArguments) < len(generic.parameters) {
		l.notSupportedYet(node, "this reference to a generic class is")
		return nil
	}
	arguments := make([]ir.Type, len(generic.parameters))
	concrete := make([]*checker.Type, len(generic.parameters))
	for i := range generic.parameters {
		concrete[i] = l.instantiate(typeArguments[i])
		if arguments[i] = l.irType(concrete[i], node); arguments[i] == nil {
			return nil
		}
	}
	for _, existing := range generic.instances {
		if slices.Equal(existing.arguments, arguments) {
			return existing.class
		}
	}
	if len(generic.instances) == maxInstances {
		l.notSupportedYet(node, fmt.Sprintf("a generic class with more than %d instances (`%s`) is", maxInstances, generic.node.Name().Text()))
		return nil
	}
	class := &ir.Class{Name: generic.node.Name().Text(), Display: l.instanceDisplay(generic.node.Name().Text(), concrete), Module: generic.module.Module, Abstract: ast.HasSyntacticModifier(generic.node, ast.ModifierFlagsAbstract)}
	inst := &classInstance{generic: generic, arguments: arguments, class: class, members: newClassMembers(),
		subst: &substitution{declaration: generic.node, parameters: generic.parameters, arguments: concrete}}
	generic.instances = append(generic.instances, inst)
	l.classInstances[class] = inst
	l.classNodes[class] = generic.node
	if !l.declareInstanceSuperclass(inst) {
		inst.class = nil
		return nil
	}
	l.instanceClasses = append(l.instanceClasses, class)
	generic.classes = append(generic.classes, class)
	if l.membersDeclared {
		l.ensureMembers(class)
		l.linkOverrides(class)
	}
	l.classInstanceBodies = append(l.classInstanceBodies, inst)
	return class
}

// declareInstanceSuperclass links a generic class's instance to its superclass: the
// instance's base type, in the instance. It reports, and returns false, a superclass that
// isn't a class of the program.
func (l *lowerer) declareInstanceSuperclass(inst *classInstance) bool {
	heritage := ast.GetExtendsHeritageClauseElement(inst.generic.node)
	if heritage == nil {
		return true
	}
	bases := l.checker.GetBaseTypes(l.checker.GetDeclaredTypeOfSymbol(l.declaredSymbol(inst.generic.node)))
	outer := l.subst
	l.subst = inst.subst
	var parent *ir.Class
	if len(bases) == 1 {
		parent, _ = l.irType(bases[0], heritage).(*ir.Class)
	}
	l.subst = outer
	if parent == nil {
		l.notSupportedYet(heritage, "extending `"+scanner.GetTextOfNode(heritage.Expression())+"` is")
		return false
	}
	inst.class.Parent = parent
	return true
}

// ensureMembers declares a class's members, after its superclass's, unless that is done
// or under way: in the class's module, and for a generic class's instance, with the
// instance's types and into its tables.
func (l *lowerer) ensureMembers(class *ir.Class) {
	if class == nil || class.Builtin || l.membersStarted[class] {
		return // a builtin class's are declared with it (D-068)
	}
	l.membersStarted[class] = true
	l.ensureMembers(class.Parent)
	outerSubst, outerDeclaring, outerFunction := l.subst, l.declaring, l.function
	l.subst, l.declaring, l.function = nil, nil, class.Module.Init
	if inst := l.classInstances[class]; inst != nil {
		l.subst, l.declaring = inst.subst, inst.members
	}
	l.declareMembers(l.classNodes[class], class)
	l.subst, l.declaring, l.function = outerSubst, outerDeclaring, outerFunction
}

// classInstanceBody lowers the constructor and methods of a generic class's instance.
func (l *lowerer) classInstanceBody(inst *classInstance) {
	if inst.class == nil {
		return
	}
	l.subst = inst.subst
	defer func() { l.subst = nil }()
	l.forgetLocals(inst.generic.node) // another instance's
	outerBodies, outerFunction := l.bodies, l.function
	l.bodies, l.function = nil, inst.generic.module
	l.declareFunctions(inst.generic.node, inst.class.Name) // the functions its methods declare, in this instance
	l.function = outerFunction
	l.classBodiesOf(inst.generic.node, inst.class)
	for len(l.bodies) > 0 {
		body := l.bodies[0]
		l.bodies = l.bodies[1:]
		l.functionBody(body)
	}
	l.bodies = outerBodies
}

// genericClassReference is the class a reference to a generic class's declaration (in
// `new`, `instanceof` or `extends`) instantiates, as the type of node says.
func (l *lowerer) genericClassReference(node *ast.Node) *ir.Class {
	class, _ := l.irType(l.typeAt(node), node).(*ir.Class)
	return class
}

// orderedByParent orders classes so that each comes after its superclass.
func orderedByParent(classes []*ir.Class) []*ir.Class {
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
	for _, class := range classes {
		add(class)
	}
	return ordered
}

// mayBeOverriddenByAnInstance reports whether a generic class that extends class,
// directly or not, declares a method of method's name and kind: an instance of it, which
// may be made after a call is lowered, would override the method, so calls go through
// the method table.
func (l *lowerer) mayBeOverriddenByAnInstance(class *ir.Class, method *ir.Method) bool {
	for _, generic := range l.genericClasses {
		if generic != nil && declaresMethod(generic.node, method) && l.instancesExtend(generic.node, class) {
			return true
		}
	}
	return false
}

// declaresMethod reports whether a class declaration declares an instance method (or
// getter or setter) of method's name and kind.
func declaresMethod(node *ast.Node, method *ir.Method) bool {
	for _, member := range node.Members() {
		if kind, ok := methodKinds[member.Kind]; ok && kind == method.Kind && !ast.IsStatic(member) && member.Name() != nil && memberName(member) == method.Name {
			return true
		}
	}
	return false
}

// instancesExtend reports whether the instances of a generic class declaration (may)
// extend class: their superclass is class or extends it, or is an instance of a generic
// class that class is an instance of.
func (l *lowerer) instancesExtend(node *ast.Node, class *ir.Class) bool {
	heritage := ast.GetExtendsHeritageClauseElement(node)
	if heritage == nil {
		return false
	}
	parentSymbol := l.target(heritage.Expression())
	if parent := l.classes[parentSymbol]; parent != nil {
		return parent.IsSubclassOf(class)
	}
	if parent := l.genericClasses[parentSymbol]; parent != nil {
		if inst := l.classInstances[class]; inst != nil && inst.generic == parent {
			return true
		}
		return l.instancesExtend(parent.node, class)
	}
	return false
}
