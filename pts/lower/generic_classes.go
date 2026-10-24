package lower

import (
	"fmt"
	"maps"
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
// IR types a generic class is instantiated with makes a class of its own (and, where a
// program is lowered splitting classes, type arguments of the same IR types whose computed
// types differ, C177), named as the generic class (console.log prints `Box { value: 1 }`
// for every instance), whose members are declared, and whose constructor and methods are
// lowered, with the class's type parameters replaced by the type arguments.

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
	switch object, ok := l.chainObjects[receiver]; {
	case ok:
		t = object.checkerType // the object of a `?.`, which is neither null nor undefined (D-064)
	case l.chainDepth > 0 && ast.IsOptionalChain(receiver) && !l.chainRests[receiver]:
		t = l.checker.GetNonOptionalType(t) // a link inside the chain being lowered (`o?.box(5).value`, as typeAt)
	}
	wantClass := receiver.Kind == ast.KindSuperKeyword // the class a subclass extends, also C<any, …> (H10b)
	if t.Flags()&checker.TypeFlagsTypeParameter != 0 {
		wantClass = wantClass || t.AsTypeParameter().IsThisType()
		if constraint := l.checker.GetBaseConstraintOfType(t); constraint != nil {
			t = constraint
		}
	}
	if instance := l.instanceofInstance(receiver, l.instantiate(t)); instance != nil {
		t = instance
	}
	class, _ := l.irTypeOf(t, receiver, wantClass).(*ir.Class)
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
	for _, existing := range generic.instances {
		if slices.Equal(existing.arguments, arguments) && (!l.splitClasses || slices.Equal(existing.subst.arguments, concrete) || l.sharesClass(generic, existing.subst.arguments, concrete)) {
			l.use(existing.subst)
			return existing.class
		}
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
	inst := &classInstance{generic: generic, arguments: arguments, class: class, members: newClassMembers(),
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

// sharesClass reports whether generic's instance for type arguments (concrete) of the IR
// types of another's (made) is that one's class (C177). It is unless the class computes
// types from its type parameters (classMembersIdentity) that differ for them: its
// superclass, which a program converts instances to (`Sub2<T> extends Base2<Awaited<T>>`),
// or a member's type where TypeScript converts neither instance's to the other's (a
// method's parameters both ways, as it compares methods), so no program reads both
// through one, or where they name other classes (classesIn), whose objects the IR doesn't
// convert between: Holder<1> and Holder<number>, whose `pick(v: R<T>): R<T>` takes and
// returns a Box<string> and a Box<number>, are two classes, as are those whose `R<T>`
// picks one of two classes of the same members, while Task<Promise<number>> and
// Task<unknown>, whose `Awaited<T>` members are a number and unknown, are one, as are
// Box<1> and Box<number>, whose members compute nothing. (tsc relates whole instance types
// by their type arguments' variance, which a conditional type doesn't constrain: it takes
// Holder<1> for a Holder<number>.)
func (l *lowerer) sharesClass(generic *genericClass, made, concrete []*checker.Type) bool {
	mine, theirs := l.classMembersIdentity(generic, concrete), l.classMembersIdentity(generic, made)
	if mine.superclass != theirs.superclass {
		return false
	}
	for i, member := range mine.members {
		if member.part == theirs.members[i].part {
			continue
		}
		a, b := l.checker.PtsInstantiateType(member.t, generic.parameters, concrete), l.checker.PtsInstantiateType(member.t, generic.parameters, made)
		if !l.checker.IsTypeAssignableTo(a, b) && !l.checker.IsTypeAssignableTo(b, a) || !slices.Equal(l.classesIn(a, nil), l.classesIn(b, nil)) {
			return false
		}
	}
	return true
}

// classesIn appends to classes the program's classes and generic classes that t names, in
// the order it names them first, as far as maxKeyParts types go.
func (l *lowerer) classesIn(t *checker.Type, classes []*ast.Symbol) []*ast.Symbol {
	seen := map[*checker.Type]bool{}
	var visit func(t *checker.Type)
	visit = func(t *checker.Type) {
		if seen[t] || len(seen) > maxKeyParts {
			return
		}
		seen[t] = true
		if t.Flags()&(checker.TypeFlagsUnion|checker.TypeFlagsIntersection) != 0 {
			for _, member := range t.Types() {
				visit(member)
			}
			return
		}
		if t.Flags()&checker.TypeFlagsObject == 0 {
			return
		}
		if symbol := t.Symbol(); symbol != nil && !slices.Contains(classes, symbol) {
			_, class := l.classes[symbol]
			_, generic := l.genericClasses[symbol]
			if class || generic {
				classes = append(classes, symbol)
			}
		}
		if t.ObjectFlags()&checker.ObjectFlagsReference != 0 && t.ObjectFlags()&checker.ObjectFlagsAnonymous == 0 {
			for _, argument := range l.checker.GetTypeArguments(t) {
				visit(argument)
			}
			return
		}
		for _, property := range l.checker.GetPropertiesOfType(t) {
			visit(l.checker.GetTypeOfSymbol(property))
		}
		for _, kind := range []checker.SignatureKind{checker.SignatureKindCall, checker.SignatureKindConstruct} {
			for _, signature := range l.checker.GetSignaturesOfType(t, kind) {
				for _, parameter := range signature.Parameters() {
					visit(l.checker.GetTypeOfSymbol(parameter))
				}
				visit(l.checker.GetReturnTypeOfSignature(signature))
			}
		}
	}
	visit(t)
	return classes
}

// classIdentity is what classMembersIdentity writes for a generic class's instance.
type classIdentity struct {
	superclass string         // its superclass, if the class computes its type arguments
	members    []computedType // its members whose types the class computes
}

// computedType is a member (or a constructor's parameter) of a generic class whose type
// the class computes from its type parameters: its declared type, and its part of
// classMembersIdentity for an instance.
type computedType struct {
	name string
	t    *checker.Type
	part string
}

// classMembersIdentity is what may tell apart instances of a generic class for type
// arguments (concrete) of the same IR types: its superclass, where the class computes its
// type arguments, and the types of the members the class declares (its properties' and
// methods', and its constructors' parameters') that it computes from its type parameters
// (computesTypes: `R<T> = Box<T extends 1 ? string : number>`, `T["kind"]`, `Awaited<T>`),
// as their IR types tell them apart (memberPart), without making the classes they name.
// Types that a class builds from its type parameters (`T[]`, `Box<T>`, `{ v: T }`) have IR
// types made of its type arguments' IR types, which are the same. Remembered by type
// arguments.
func (l *lowerer) classMembersIdentity(generic *genericClass, concrete []*checker.Type) classIdentity {
	key := fmt.Sprintf("%p", generic)
	for _, t := range concrete {
		key += fmt.Sprintf(",%d", t.Id())
	}
	if identity, ok := l.classIdentities[key]; ok {
		return identity
	}
	computed := l.classComputedTypes(generic)
	outer := l.subst
	l.subst = &substitution{declaration: generic.node, parameters: generic.parameters, arguments: concrete, instance: true}
	defer func() { l.subst = outer }()
	walk := &typeWalk{budget: maxKeyParts}
	identity := classIdentity{members: slices.Clone(computed.members)}
	for _, base := range computed.superclasses {
		identity.superclass += l.memberPart(walk, base.t) + ";"
	}
	for i := range identity.members {
		identity.members[i].part = l.memberPart(walk, identity.members[i].t)
	}
	l.classIdentities[key] = identity
	return identity
}

// classComputed are the superclasses and the members (or constructors' parameters) whose
// types a generic class computes from its type parameters (computesTypes).
type classComputed struct {
	superclasses, members []computedType
}

// classComputedTypes are generic's classComputed, remembered; while they are being found
// (a member's type names the class), none.
func (l *lowerer) classComputedTypes(generic *genericClass) *classComputed {
	if computed, ok := l.classComputed[generic]; ok {
		if computed == nil {
			return &classComputed{}
		}
		return computed
	}
	l.classComputed[generic] = nil
	computed := &classComputed{}
	symbol := l.declaredSymbol(generic.node)
	declared := l.checker.GetDeclaredTypeOfSymbol(symbol)
	for _, base := range l.checker.GetBaseTypes(declared) {
		if l.computesTypes(base, map[*checker.Type]bool{}) {
			computed.superclasses = append(computed.superclasses, computedType{name: "extends", t: base})
		}
	}
	for _, property := range l.checker.GetPropertiesOfType(declared) {
		if t := l.checker.GetTypeOfSymbol(property); l.computesTypes(t, map[*checker.Type]bool{}) {
			computed.members = append(computed.members, computedType{name: property.Name, t: t})
		}
	}
	for _, signature := range l.checker.GetSignaturesOfType(l.checker.GetTypeOfSymbol(symbol), checker.SignatureKindConstruct) {
		for i, parameter := range signature.Parameters() {
			if t := l.checker.GetTypeOfSymbol(parameter); l.computesTypes(t, map[*checker.Type]bool{}) {
				computed.members = append(computed.members, computedType{name: fmt.Sprintf("new(%d)", i), t: t})
			}
		}
	}
	l.classComputed[generic] = computed
	return computed
}

// computesTypes reports whether t, a type in a generic declaration, is or holds a type
// that tsc computes from type parameters, whose IR type for type arguments is not made of
// their IR types alone: a conditional type (`T extends 1 ? string : number`, `Awaited<T>`,
// `ReturnType<F>`), an indexed access (`T["kind"]`), `keyof T`, a mapped type or a
// template literal type, also in a generic interface's members; and where the program is
// lowered splitting classes, a generic class that computes types from its own
// (classComputedTypes), whose instances of one IR type may then be other classes. An
// intersection with a type parameter (`T & any[]`, which a narrowing makes) is an
// instance's as narrowedInstance and D-195 say.
func (l *lowerer) computesTypes(t *checker.Type, seen map[*checker.Type]bool) bool {
	if seen[t] || len(seen) > maxKeyParts {
		return false
	}
	seen[t] = true
	flags := t.Flags()
	switch {
	case flags&(checker.TypeFlagsConditional|checker.TypeFlagsIndexedAccess|checker.TypeFlagsIndex|checker.TypeFlagsSubstitution|checker.TypeFlagsTemplateLiteral|checker.TypeFlagsStringMapping) != 0:
		return true
	case flags&(checker.TypeFlagsUnion|checker.TypeFlagsIntersection) != 0:
		return slices.ContainsFunc(t.Types(), func(member *checker.Type) bool { return l.computesTypes(member, seen) })
	case flags&checker.TypeFlagsObject == 0:
		return false
	case t.ObjectFlags()&checker.ObjectFlagsMapped != 0:
		return true
	case t.ObjectFlags()&checker.ObjectFlagsReference != 0 && t.ObjectFlags()&checker.ObjectFlagsAnonymous == 0:
		if generic := l.genericClasses[t.Symbol()]; generic != nil && l.splitClasses {
			if computed := l.classComputedTypes(generic); len(computed.superclasses)+len(computed.members) > 0 {
				return true
			}
		}
		if slices.ContainsFunc(l.checker.GetTypeArguments(t), func(argument *checker.Type) bool { return l.computesTypes(argument, seen) }) {
			return true
		}
		if !l.isProgramInterface(t) {
			return false
		}
		// A program's generic interface, whose members may compute from its type arguments
		// (`interface Wrap<X> { v: Awaited<X> }`): as the object type it is.
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

// memberPart is t's part of classMembersIdentity: the same for types whose IR types are
// the same, as far as it goes. It is keyPart's walk, but a dynamic value (any, unknown, a
// promise, an iterator, an iterable) is one whatever it holds, as its IR type is; a union
// is the set of its members' parts, so `true | false` is a boolean and `"a" | "b"` a
// string (one part is that part); a type that isn't an object, a union or an intersection
// is its IR type (a literal type its primitive's); an object type is its type reference's
// target and type arguments, or its members, not an IR type, whose making could make the
// class being told apart. A type parameter that isn't the instance's (a method's own) is
// its place among its signature's, or itself.
func (l *lowerer) memberPart(w *typeWalk, t *checker.Type) string {
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
	if irType := l.irTypeOrNone(t, l.subst.declaration); irType != nil {
		return irType.String()
	}
	return fmt.Sprintf("t%d", t.Id()) // no IR type: the same as itself only
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
		parent = l.classOf(bases[0], heritage)
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
	inst := l.classInstances[class]
	var outerParams map[*ast.Symbol]*ir.Local
	if inst != nil {
		l.subst, l.declaring = inst.subst, inst.members
		outerParams = l.memberParameterLocals(l.classNodes[class])
	}
	l.declareMembers(l.classNodes[class], class)
	l.subst, l.declaring, l.function = outerSubst, outerDeclaring, outerFunction
	maps.Copy(l.locals, outerParams) // a method being lowered may be another instance's of this class (R4, H10)
}

// memberParameterLocals are the locals that the parameters of a class's constructor,
// methods and accessors have now, by symbol: an instance whose members are declared while
// another instance's method is lowered (`new Box<boolean>(…)` or `o instanceof Box` in
// Box<number>'s) declares its own for the same symbols, as declareInstance's do.
func (l *lowerer) memberParameterLocals(node *ast.Node) map[*ast.Symbol]*ir.Local {
	locals := map[*ast.Symbol]*ir.Local{}
	for _, member := range node.Members() {
		if ast.IsFunctionLike(member) {
			maps.Copy(locals, l.parameterLocals(member))
		}
	}
	return locals
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
	return l.classOf(l.typeAt(node), node)
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

// isProgramInterface reports whether t is a reference to an interface the program declares
// (`Wrap<number>`), not a class or the library's.
func (l *lowerer) isProgramInterface(t *checker.Type) bool {
	symbol := t.Symbol()
	if symbol == nil || symbol.Flags&ast.SymbolFlagsInterface == 0 || symbol.Flags&ast.SymbolFlagsClass != 0 || l.isLibInterface(symbol) {
		return false
	}
	_, class := l.classes[symbol]
	_, generic := l.genericClasses[symbol]
	return !class && !generic
}
