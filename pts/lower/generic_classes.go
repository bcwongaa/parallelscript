package lower

import (
	"fmt"
	"slices"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/internal/scanner"
	"github.com/microsoft/typescript-go/pts/ir"
)

// classMembers are the members of classes by declaration. A generic class's instances
// each have their own (D-050); the other classes share the lowerer's.
type classMembers struct {
	fields         map[*ast.Node]*ir.Field  // by property declaration or parameter property (a redeclaration's is the inherited field)
	methods        map[*ast.Node]*ir.Method // methods, getters and setters, by declaration; nil if it failed
	abstractFields map[*ast.Node]property   // the getter and setter of each abstract field, by declaration
}

func newClassMembers() *classMembers {
	return &classMembers{fields: map[*ast.Node]*ir.Field{}, methods: map[*ast.Node]*ir.Method{}, abstractFields: map[*ast.Node]property{}}
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
	if declaring == nil {
		return l.members
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
	if t.Flags()&checker.TypeFlagsTypeParameter != 0 {
		if constraint := l.checker.GetBaseConstraintOfType(t); constraint != nil {
			t = constraint
		}
	}
	class, _ := l.irType(t, receiver).(*ir.Class)
	return class
}

// declareGenericClass records a generic class declaration, whose instances the types
// that name it make. Static members, which every instance would share, are reported.
func (l *lowerer) declareGenericClass(node *ast.Node) {
	symbol := l.declaredSymbol(node)
	for _, member := range node.Members() {
		if ast.IsStatic(member) {
			l.notSupportedYet(nameOf(member), "static members of generic classes are")
			l.genericClasses[symbol] = nil
			return
		}
	}
	generic := &genericClass{node: node, module: l.function}
	for _, parameter := range node.TypeParameters() {
		generic.parameters = append(generic.parameters, l.checker.GetDeclaredTypeOfSymbol(parameter.Symbol()))
	}
	l.genericClasses[symbol] = generic
}

// classInstanceOf returns the class of a generic class's instance for the type arguments
// of t, a reference to it (in terms of the instance being lowered, if any), making it if
// it is new; nil if it failed (reported at node).
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
	class := &ir.Class{Name: generic.node.Name().Text(), Module: generic.module.Module, Abstract: ast.HasSyntacticModifier(generic.node, ast.ModifierFlagsAbstract)}
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
	if class == nil || l.membersStarted[class] {
		return
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
	l.classBodiesOf(inst.generic.node, inst.class)
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
