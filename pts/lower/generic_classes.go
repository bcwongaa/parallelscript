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
// IR types a generic class is instantiated with makes a class of its own (and type
// arguments of the same IR types whose members' types differ, C177), named as the
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
	identity  string    // its members' types (classMembersIdentity)
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
	switch object, ok := l.chainObjects[receiver]; {
	case ok:
		t = object.checkerType // the object of a `?.`, which is neither null nor undefined (D-064)
	case l.chainDepth > 0 && ast.IsOptionalChain(receiver) && !l.chainRests[receiver]:
		t = l.checker.GetNonOptionalType(t) // a link inside the chain being lowered (`o?.box(5).value`, as typeAt)
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
		concrete[i] = l.typeArgument(typeArguments[i])
		if arguments[i] = l.irType(concrete[i], node); arguments[i] == nil {
			return nil
		}
	}
	identity := ""
	for _, existing := range generic.instances {
		if !slices.Equal(existing.arguments, arguments) {
			continue
		}
		if !slices.Equal(existing.subst.arguments, concrete) {
			if identity == "" {
				identity = l.classMembersIdentity(generic, concrete)
			}
			if existing.identity != identity {
				continue
			}
		}
		l.use(existing.subst)
		return existing.class
	}
	if identity == "" {
		identity = l.classMembersIdentity(generic, concrete)
	}
	limit, recursive := l.instanceLimitAt(generic.node, true)
	if limit != withinLimits {
		l.reportInstanceLimit(limit, generic.node.Name().Text(), node)
		return nil
	}
	if recursive {
		l.countInstance(generic.node, true)
	}
	class := &ir.Class{Name: generic.node.Name().Text(), Display: l.instanceDisplay(generic.node.Name().Text(), concrete), Module: generic.module.Module, Abstract: ast.HasSyntacticModifier(generic.node, ast.ModifierFlagsAbstract)}
	inst := &classInstance{generic: generic, arguments: arguments, identity: identity, class: class, members: newClassMembers(),
		subst: &substitution{declaration: generic.node, parameters: generic.parameters, arguments: concrete, instance: true, made: l.subst}}
	generic.instances = append(generic.instances, inst)
	l.use(inst.subst)
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

// classMembersIdentity is what tells apart instances of a generic class for type arguments
// of the same IR types (concrete, C177): the types of the members such an instance
// declares (its properties' and methods', and its constructors' parameters') that the
// class computes from its type parameters (computesTypes: `R<T> = Box<T extends 1 ?
// string : number>`, `T["kind"]`, `Awaited<T>`), as their IR types tell them apart
// (memberPart), without making the classes they name. Its other members' IR types are
// those of its type arguments', so they don't tell instances apart. So Holder<1> and
// Holder<number>, whose `pick(v: R<T>): R<T>` takes and returns a Box<string> and a
// Box<number>, are two classes, while Box<1> and Box<number>, or Box<Json> and
// Box<unknown>, are one, which the program may convert between as TypeScript does.
// Remembered by type arguments.
func (l *lowerer) classMembersIdentity(generic *genericClass, concrete []*checker.Type) string {
	key := fmt.Sprintf("%p", generic)
	for _, t := range concrete {
		key += fmt.Sprintf(",%d", t.Id())
	}
	if identity, ok := l.classIdentities[key]; ok {
		return identity
	}
	symbol := l.declaredSymbol(generic.node)
	var members []string
	var computed []*checker.Type
	for _, property := range l.checker.GetPropertiesOfType(l.checker.GetDeclaredTypeOfSymbol(symbol)) {
		if t := l.checker.GetTypeOfSymbol(property); l.computesTypes(t, map[*checker.Type]bool{}) {
			members, computed = append(members, property.Name), append(computed, t)
		}
	}
	for _, signature := range l.checker.GetSignaturesOfType(l.checker.GetTypeOfSymbol(symbol), checker.SignatureKindConstruct) {
		for i, parameter := range signature.Parameters() {
			if t := l.checker.GetTypeOfSymbol(parameter); l.computesTypes(t, map[*checker.Type]bool{}) {
				members, computed = append(members, fmt.Sprintf("new(%d)", i)), append(computed, t)
			}
		}
	}
	outer := l.subst
	l.subst = &substitution{declaration: generic.node, parameters: generic.parameters, arguments: concrete, instance: true}
	defer func() { l.subst = outer }()
	w := &memberWalk{budget: maxKeyParts}
	var identity strings.Builder
	for i, t := range computed {
		identity.WriteString(members[i] + ":" + l.memberPart(w, t) + ";")
	}
	l.classIdentities[key] = identity.String()
	return identity.String()
}

// computesTypes reports whether t, a type in a generic declaration, is or holds a type
// that tsc computes from type parameters, whose IR type for type arguments is not made of
// their IR types alone: a conditional type (`T extends 1 ? string : number`, `Awaited<T>`,
// `ReturnType<F>`), an indexed access (`T["kind"]`), `keyof T`, a mapped type, a template
// literal type, or an intersection with a type parameter, which may reduce.
func (l *lowerer) computesTypes(t *checker.Type, seen map[*checker.Type]bool) bool {
	if seen[t] || len(seen) > maxKeyParts {
		return false
	}
	seen[t] = true
	flags := t.Flags()
	switch {
	case flags&(checker.TypeFlagsConditional|checker.TypeFlagsIndexedAccess|checker.TypeFlagsIndex|checker.TypeFlagsSubstitution|checker.TypeFlagsTemplateLiteral|checker.TypeFlagsStringMapping) != 0:
		return true
	case flags&checker.TypeFlagsIntersection != 0 && slices.ContainsFunc(t.Types(), func(member *checker.Type) bool { return member.Flags()&checker.TypeFlagsTypeParameter != 0 }):
		return true
	case flags&(checker.TypeFlagsUnion|checker.TypeFlagsIntersection) != 0:
		return slices.ContainsFunc(t.Types(), func(member *checker.Type) bool { return l.computesTypes(member, seen) })
	case flags&checker.TypeFlagsObject == 0:
		return false
	case t.ObjectFlags()&checker.ObjectFlagsMapped != 0:
		return true
	case t.ObjectFlags()&checker.ObjectFlagsReference != 0 && t.ObjectFlags()&checker.ObjectFlagsAnonymous == 0:
		return slices.ContainsFunc(l.checker.GetTypeArguments(t), func(argument *checker.Type) bool { return l.computesTypes(argument, seen) })
	}
	for _, property := range l.checker.GetPropertiesOfType(t) {
		if l.computesTypes(l.checker.GetTypeOfSymbol(property), seen) {
			return true
		}
	}
	for _, info := range l.checker.GetIndexInfosOfType(t) {
		if l.computesTypes(info.KeyType(), seen) || l.computesTypes(info.ValueType(), seen) {
			return true
		}
	}
	for _, kind := range []checker.SignatureKind{checker.SignatureKindCall, checker.SignatureKindConstruct} {
		for _, signature := range l.checker.GetSignaturesOfType(t, kind) {
			for _, parameter := range signature.Parameters() {
				if l.computesTypes(l.checker.GetTypeOfSymbol(parameter), seen) {
					return true
				}
			}
			if l.computesTypes(l.checker.GetReturnTypeOfSignature(signature), seen) {
				return true
			}
		}
	}
	return false
}

// memberWalk is what memberPart has written so far.
type memberWalk struct {
	path    []*checker.Type   // the types being written, outermost first
	budget  int               // the types it may still write by their parts
	binders [][]*checker.Type // the type parameters of the signatures being written, outermost first
}

// memberPart is t's part of classMembersIdentity: the same for types whose IR types are
// the same, as far as it goes. It is keyPart's walk, but a dynamic value (any, unknown, a
// promise, an iterator, an iterable) is one whatever it holds, as its IR type is; a union
// is the set of its members' parts, so `true | false` is a boolean and `"a" | "b"` a string
// (one part is that part), and a type that isn't
// an object, a union or an intersection is its IR type (a literal type its primitive's);
// an object type is its type reference's target and type arguments, or its members, not
// an IR type, whose making could make the class being told apart. A type parameter that
// isn't the instance's (a method's own) is its place among its signature's, or itself.
func (l *lowerer) memberPart(w *memberWalk, t *checker.Type) string {
	t = l.instantiate(t)
	if i := slices.Index(w.path, t); i >= 0 {
		return fmt.Sprintf("^%d", len(w.path)-i)
	}
	for depth := len(w.binders) - 1; depth >= 0; depth-- {
		if i := slices.Index(w.binders[depth], t); i >= 0 {
			return fmt.Sprintf("$%d.%d", len(w.binders)-depth, i)
		}
	}
	if len(w.path) > maxKeyDepth || w.budget == 0 {
		return "?" // the same for what is that deep
	}
	w.budget--
	w.path = append(w.path, t)
	defer func() { w.path = w.path[:len(w.path)-1] }()
	objectFlags := checker.ObjectFlags(0)
	if t.Flags()&checker.TypeFlagsObject != 0 {
		objectFlags = t.ObjectFlags()
	}
	switch {
	case t.Flags()&(checker.TypeFlagsAny|checker.TypeFlagsUnknown) != 0:
		return "value"
	case t.Flags()&(checker.TypeFlagsUnion|checker.TypeFlagsIntersection) != 0:
		var members []string
		for _, member := range t.Types() {
			members = append(members, l.memberPart(w, member))
		}
		slices.Sort(members)
		if members = slices.Compact(members); len(members) == 1 {
			return members[0] // `"a" | "b"` is a string
		}
		return fmt.Sprintf("(%d:%s)", t.Flags()&(checker.TypeFlagsUnion|checker.TypeFlagsIntersection), strings.Join(members, ","))
	case objectFlags != 0 && (l.isPromiseType(t) || l.isIteratorType(t) || l.isAsyncIteratorType(t) || l.isIterableType(t)):
		return "value"
	case objectFlags&checker.ObjectFlagsReference != 0 && objectFlags&checker.ObjectFlagsAnonymous == 0:
		arguments := make([]string, 0, len(l.checker.GetTypeArguments(t)))
		for _, argument := range l.checker.GetTypeArguments(t) {
			arguments = append(arguments, l.memberPart(w, argument))
		}
		return fmt.Sprintf("r%d<%s>", t.Target().Id(), strings.Join(arguments, ","))
	case objectFlags != 0:
		var b strings.Builder
		b.WriteString("{")
		for _, property := range l.checker.GetPropertiesOfType(t) {
			optional := ""
			if property.Flags&ast.SymbolFlagsOptional != 0 {
				optional = "?"
			}
			b.WriteString(property.Name + optional + ":" + l.memberPart(w, l.checker.GetTypeOfSymbol(property)) + ";")
		}
		for _, info := range l.checker.GetIndexInfosOfType(t) {
			b.WriteString("[" + l.memberPart(w, info.KeyType()) + "]:" + l.memberPart(w, info.ValueType()) + ";")
		}
		for i, kind := range []checker.SignatureKind{checker.SignatureKindCall, checker.SignatureKindConstruct} {
			for _, signature := range l.checker.GetSignaturesOfType(t, kind) {
				fmt.Fprintf(&b, "%d<%d>(", i, len(signature.TypeParameters()))
				w.binders = append(w.binders, signature.TypeParameters())
				for _, parameter := range signature.Parameters() {
					b.WriteString(l.memberPart(w, l.checker.GetTypeOfSymbol(parameter)) + ",")
				}
				b.WriteString(")" + l.memberPart(w, l.checker.GetReturnTypeOfSignature(signature)) + ";")
				w.binders = w.binders[:len(w.binders)-1]
			}
		}
		b.WriteString("}")
		return b.String()
	case t.Flags()&(checker.TypeFlagsTypeParameter|checker.TypeFlagsIndex|checker.TypeFlagsIndexedAccess|checker.TypeFlagsConditional|checker.TypeFlagsSubstitution|checker.TypeFlagsTemplateLiteral|checker.TypeFlagsStringMapping) != 0:
		return fmt.Sprintf("t%d", t.Id()) // what isn't resolved for the instance: its own
	}
	reported := l.reportedSoFar()
	foreign := l.trial != nil && l.trial.foreign
	if irType := l.irType(t, l.subst.declaration); irType != nil {
		return irType.String()
	}
	l.restoreReported(reported) // no IR type: the same as itself only
	if l.trial != nil {
		l.trial.foreign = foreign
	}
	return fmt.Sprintf("t%d", t.Id())
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
