// Package ir is ParallelScript's typed intermediate representation (D-L04): structured
// rather than SSA, fully typed, with boxing and every runtime operation explicit.
// pts/lower produces it from the checked TypeScript AST and pts/cgen turns it into C.
package ir

import (
	"slices"
	"strings"
)

// Program is a whole lowered program.
type Program struct {
	Classes    []*Class // superclasses before their subclasses
	Interfaces []*Interface
	Funcs      []*Func
	Modules    []*Module // in the order their top-level code runs (ES module evaluation order); the entry module is last
	Main       *Func     // runs the entry module's top-level code; also in Funcs
	// InKeys are the keys that `in` looks up at run time (HasKey): the methods, getters and
	// setters of these names go in the descriptors' method tables (D-067).
	InKeys []string
}

// Module is one source file of the program.
type Module struct {
	// Name is the file's base name without its extension, which qualifies the names of
	// its functions and bindings; it is empty for the entry module, whose names stay plain.
	Name string
	Init *Func // its top-level code; also in Program.Funcs
}

// Pos is a source position. The zero Pos means "no position".
type Pos struct {
	File string // absolute path of the source file
	Line int    // 1-based
}

// Type is the static type of an IR value. Types are compared by identity.
type Type interface {
	String() string
}

// Primitive is a type with a fixed native representation.
type Primitive struct {
	name string
}

func (p *Primitive) String() string { return p.name }

var (
	Number    = &Primitive{"number"}    // IEEE-754 double
	Bool      = &Primitive{"boolean"}   // C bool
	String    = &Primitive{"string"}    // immutable runtime string
	Undefined = &Primitive{"undefined"} // the one value undefined
	Null      = &Primitive{"null"}      // the one value null
	Void      = &Primitive{"void"}      // no value: the result of a call made for its effect
	Value     = &Primitive{"value"}     // a boxed dynamic value (pts_value, D-002)

	// The integer types and f32 (docs/SPEC.md §3.2), as C's fixed-width types and float.
	I8  = &Primitive{"i8"}
	I16 = &Primitive{"i16"}
	I32 = &Primitive{"i32"}
	I64 = &Primitive{"i64"}
	U8  = &Primitive{"u8"}
	U16 = &Primitive{"u16"}
	U32 = &Primitive{"u32"}
	U64 = &Primitive{"u64"}
	F32 = &Primitive{"f32"}

	// MapIterator is an iteration over a map or set (D-046), held in a local.
	MapIterator = &Primitive{"map iterator"}

	// Given is the type of the flag that follows each parameter with a default in a
	// parameter list (D-055): whether the call passed the argument, rather than leaving it
	// out or passing undefined. A C bool.
	Given = &Primitive{"given"}
)

// Class is a class declared in the program, and the type of a reference to one of its
// instances (never null), or to an instance of a subclass. An instance holds its
// parent's fields, then its own Fields in order; Constructor and the methods' functions
// take the instance as their first parameter, `this`.
type Class struct {
	Name        string
	Display     string  // a generic class instance's name with its type arguments (`Box<number>`), for error messages; else ""
	Module      *Module // nil for a shape
	Parent      *Class  // the superclass, or nil
	Abstract    bool
	Fields      []*Field    // the fields it declares, not those it inherits or redeclares
	Constructor *Func       // runs the field initialisers, then the constructor's body; in a subclass, the initialisers run when super(...) returns
	Methods     []*Method   // the methods it declares, overrides included
	Instances   []*Instance // the instances of the generic methods it declares that dispatched calls find (D-148)

	// Shape marks the anonymous struct type of an object type (D-033): only Fields, in
	// name order. Its objects print without a class name, each listing its properties in
	// one of Orders, the order the object literal that made it wrote them.
	Shape  bool
	Orders [][]*Field

	// Tuple marks a shape whose fields, named 0, 1, …, are a tuple's elements (D-053). Its
	// objects print as arrays.
	Tuple bool

	// Builtin marks a class the runtime defines (D-068): Error and its native subclasses.
	// Their layout and descriptors are the runtime's, their constructor and methods are
	// runtime functions (Func.Extern), and a native subclass's instances are laid out as
	// its parent's, adding nothing.
	Builtin bool

	// Result marks a shape of iterator results (D-109): a value of it may be any object an
	// iterator handed back (an object's own next()'s result, an asserted literal, one with
	// getters), so its fields are read and written only on objects of the shape, and found
	// by name on others (H03b).
	Result bool
}

func (c *Class) String() string {
	if c.Tuple {
		elements := make([]string, len(c.Fields))
		for i, field := range c.Fields {
			elements[i] = field.Type.String()
		}
		return "[" + strings.Join(elements, ", ") + "]"
	}
	return c.Name
}

// Order returns the index in Orders of a property order of a shape, adding it if new.
func (c *Class) Order(fields []*Field) int {
	for i, order := range c.Orders {
		if slices.Equal(order, fields) {
			return i
		}
	}
	c.Orders = append(c.Orders, fields)
	return len(c.Orders) - 1
}

// IsSubclassOf reports whether c is ancestor or extends it, directly or not, so that
// every instance of c is an instance of ancestor.
func (c *Class) IsSubclassOf(ancestor *Class) bool {
	for ; c != nil; c = c.Parent {
		if c == ancestor {
			return true
		}
	}
	return false
}

// AllFields are the fields of an instance in layout order: inherited ones first.
func (c *Class) AllFields() []*Field {
	if c.Parent == nil {
		return c.Fields
	}
	return append(append([]*Field{}, c.Parent.AllFields()...), c.Fields...)
}

// Field returns the field name of an instance, its class's own or inherited, or nil.
func (c *Class) Field(name string) *Field {
	for ; c != nil; c = c.Parent {
		for _, field := range c.Fields {
			if field.Name == name {
				return field
			}
		}
	}
	return nil
}

// Method returns the method of the given name and kind an instance has, its class's own
// or the nearest ancestor's, or nil.
func (c *Class) Method(name string, kind MethodKind) *Method {
	for ; c != nil; c = c.Parent {
		if method := c.OwnMethod(name, kind); method != nil {
			return method
		}
	}
	return nil
}

// OwnMethod returns the method of the given name and kind that c declares, or nil.
func (c *Class) OwnMethod(name string, kind MethodKind) *Method {
	for _, method := range c.Methods {
		if method.Name == name && method.Kind == kind {
			return method
		}
	}
	return nil
}

// Slots are the virtual methods c declares that override nothing: each is an entry of
// the method table of c and of every subclass.
func (c *Class) Slots() []*Method {
	var slots []*Method
	for _, method := range c.Methods {
		if method.Virtual && method.Overrides == nil {
			slots = append(slots, method)
		}
	}
	return slots
}

// Interface is an object type, used structurally (D-035, D-036): the type of a reference
// to an object of any class or shape that provides its properties, Fields, with fields or
// accessors, and its Methods with methods; both in name order.
type Interface struct {
	Fields  []*Field // their Class is nil
	Methods []*InterfaceMethod
	// Copy is the shape that dynamic data converted to the interface is copied into
	// (D-071), whose objects list their properties in its order CopyOrder; nil if no
	// conversion from a dynamic value reaches the interface.
	Copy      *Class
	CopyOrder int
	// Result marks the interface of a property of a union of iterator results (D-109),
	// whose objects may be of any layout: the property is found by name and converted to
	// its type with a check (H03b).
	Result bool
	// writing is set while String writes the interface, so that a recursive object type
	// (`{ value: number; next?: List }`) writes its references to itself as "{...}".
	writing bool
}

func (i *Interface) String() string {
	if i.writing {
		return "{...}"
	}
	i.writing = true
	defer func() { i.writing = false }()
	members := []string{}
	if len(i.Fields) > 0 {
		members = append(members, fieldList(i.Fields))
	}
	for _, method := range i.Methods {
		members = append(members, method.String())
	}
	return "{" + strings.Join(members, "; ") + "}"
}

// Method returns the method name of the interface, or nil.
func (i *Interface) Method(name string) *InterfaceMethod {
	for _, method := range i.Methods {
		if method.Name == name {
			return method
		}
	}
	return nil
}

// InterfaceMethod is a method of an interface. A class provides it with a method of the
// same name that takes the same parameter types and returns the same type.
type InterfaceMethod struct {
	Name   string
	Params []Type // after `this`
	Result Type
}

func (m *InterfaceMethod) String() string {
	params := make([]string, len(m.Params))
	for i, param := range m.Params {
		params[i] = param.String()
	}
	return m.Name + "(" + strings.Join(params, ", ") + "): " + m.Result.String()
}

// Field returns the property name of the interface, or nil.
func (i *Interface) Field(name string) *Field {
	for _, field := range i.Fields {
		if field.Name == name {
			return field
		}
	}
	return nil
}

// Array is the type of a reference to an array whose elements have type Elem (D-038).
// The lowering makes one Array per element type, so array types compare by identity too.
type Array struct {
	Elem Type
}

// StringArray is string[], which builtins such as split return; the lowering uses it as
// the one array type of strings.
var StringArray = &Array{Elem: String}

// ValueArray is any[], which ValueToArray makes; the lowering uses it as the one array
// type of dynamic values.
var ValueArray = &Array{Elem: Value}

// NumberArray is number[], which String.fromCharCode and String.fromCodePoint take; the
// lowering uses it as the one array type of numbers.
var NumberArray = &Array{Elem: Number}

func (a *Array) String() string {
	switch a.Elem.(type) {
	case *FuncType, *Union:
		return "(" + a.Elem.String() + ")[]" // TypeScript's (number | number[])[], not number | number[][]
	}
	return a.Elem.String() + "[]"
}

// MapType is the type of a reference to a Map from Key to Value, or if Set, to a Set of
// Key (Value nil) (D-046), or if Record, to a hash-map object (D-059: Record<string, T>
// or an index signature) from String keys to Value. The lowering makes one per key and
// value type and kind, so map types compare by identity too.
type MapType struct {
	Key, Value Type
	Set        bool
	Record     bool
}

func (m *MapType) String() string {
	switch {
	case m.Set:
		return "Set<" + m.Key.String() + ">"
	case m.Record:
		return "Record<" + m.Key.String() + ", " + m.Value.String() + ">"
	}
	return "Map<" + m.Key.String() + ", " + m.Value.String() + ">"
}

// Union is the type of a value of one of several types (D-062): its Members, each a
// type other than a union, in a canonical order. It is a pts_value in C, as Value is:
// numbers (and the integer types and f32 that a double holds exactly) as numbers, i64 and
// u64 boxed, booleans, null and undefined as constants, and references as pointers. The
// lowering makes one per set of members, so union types compare by identity.
type Union struct{ Members []Type }

func (u *Union) String() string {
	names := make([]string, len(u.Members))
	for i, member := range u.Members {
		names[i] = member.String()
		if _, ok := member.(*FuncType); ok {
			names[i] = "(" + names[i] + ")"
		}
	}
	return strings.Join(names, " | ")
}

// Has reports whether t is one of u's members.
func (u *Union) Has(t Type) bool { return slices.Contains(u.Members, t) }

// HoldsUndefined reports whether undefined is a value of t: any (or unknown), undefined, or a union with it.
func HoldsUndefined(t Type) bool {
	if u, ok := t.(*Union); ok {
		return u.Has(Undefined)
	}
	return t == Value || t == Undefined
}

// Rest is the type of a rest parameter in a parameter list (D-057): the array of the
// arguments from its position on, which the caller makes. A pts_array * in C, like Array.
// A function reads it as an array through RestArgs. The lowering makes one per array type.
type Rest struct {
	Array *Array
}

func (r *Rest) String() string { return "..." + r.Array.String() }

// FuncType is the type of a function value (D-041): a reference to a closure whose code
// takes Params and returns Result (Void if it returns nothing). The lowering makes one per
// signature, so function types compare by identity too.
type FuncType struct {
	Params []Type
	Result Type
}

func (t *FuncType) String() string {
	params := make([]string, len(t.Params))
	for i, param := range t.Params {
		params[i] = param.String()
	}
	return "(" + strings.Join(params, ", ") + ") => " + t.Result.String()
}

// ShapeString writes a shape as its name and its fields: "object1{x: number; y: number}".
func (c *Class) ShapeString() string { return c.Name + "{" + fieldList(c.Fields) + "}" }

func fieldList(fields []*Field) string {
	members := make([]string, len(fields))
	for i, field := range fields {
		if field.Optional {
			members[i] = field.Name + "?: " + field.Type.String()
		} else {
			members[i] = field.Name + ": " + field.Type.String()
		}
	}
	return strings.Join(members, "; ")
}

// Field is a field of a class's instances, or a property of an interface.
type Field struct {
	Name  string
	Type  Type
	Class *Class // nil for an interface's property
	// Accessor marks an interface's property that some class of the program has a getter
	// or setter of the same name for: through the interface, reading it may call a getter
	// and writing it a setter.
	Accessor bool
	// Method marks a method of an object literal's shape (D-044): the field holds the
	// method's closure, whose code takes the object first, as `this`. A Getter or Setter
	// field holds the getter's or setter's closure of the shape's accessor property
	// Property, and is named "get <Property>" or "set <Property>" (D-045).
	Method   bool
	Kind     MethodKind
	Property string
	// Optional marks an optional property (`p?: T`) of an object type, of type T | undefined
	// (D-065): an object may leave it out, and its field then holds the absent value, which
	// every read (FieldRef) sees as undefined.
	Optional bool
	// Mixed marks an interface's property that its objects hold in fields of different
	// types, all members of its type (a union's members', D-067): it is read by the field's
	// kind and can't be written.
	Mixed bool
}

// AccessorField returns the field of a shape that holds the getter (or setter) of its
// accessor property name, or nil.
func (c *Class) AccessorField(name string, kind MethodKind) *Field {
	for _, field := range c.Fields {
		if field.Kind == kind && field.Property == name {
			return field
		}
	}
	return nil
}

// MethodKind tells methods from the getters and setters of accessor properties.
type MethodKind int

const (
	Plain MethodKind = iota
	Getter
	Setter
)

// Method is a method a class declares, or an accessor property's getter or setter. A
// virtual method, which is abstract, overridden or an override, is called through the
// method table of its instance's class, unless the caller knows which implementation
// runs; others are called directly. A class implements an abstract field's getter and
// setter with its field.
type Method struct {
	Name      string
	Kind      MethodKind
	Class     *Class
	Func      *Func   // takes `this` (an instance of Class) first; it has no Body if Abstract
	Overrides *Method // the nearest ancestor's method of the same name and kind, or nil
	Abstract  bool
	Virtual   bool
}

// Slot is the method whose class's method table has the entry a virtual method fills:
// the topmost method of the chain of overrides it is part of.
func (m *Method) Slot() *Method {
	for m.Overrides != nil {
		m = m.Overrides
	}
	return m
}

// IsInteger reports whether t is one of the integer types.
func IsInteger(t Type) bool {
	switch t {
	case I8, I16, I32, I64, U8, U16, U32, U64:
		return true
	}
	return false
}

// IsNumeric reports whether t is number, f32 or an integer type.
func IsNumeric(t Type) bool {
	return t == Number || t == F32 || IsInteger(t)
}

// Func is a function with a body. A nested function's Name is qualified: "outer.inner";
// a module's top-level code is named "main".
type Func struct {
	Name   string
	Module *Module // the module it is declared in
	Pos    Pos
	Params []*Local
	Result Type // Void if it returns nothing
	Body   *Block

	// Closure marks a function that runs with a closure (D-041): an arrow function, a
	// function expression, a function declaration inside another function, or an adapter.
	// Its code takes the closure first, which holds the values of Captures, the locals of
	// enclosing functions that it or the closures it makes use (for a Cell, the cell).
	// Outer is the function whose code makes the closure.
	Closure  bool
	Outer    *Func
	Captures []*Local
	// Self is the local of Outer that holds the closure of a nested function declaration:
	// in the function's own body it is the closure it runs with, so it isn't captured.
	Self *Local
	// JSName is the function's `name` in JS, which console.log shows: "" if anonymous.
	JSName string
	// Length is the function's `length` in JS: how many parameters it declares before the
	// first with a default and the rest parameter (D-098).
	Length int
	// Adapts marks an adapter: a closure that calls the function value it captures,
	// adapting its arguments and result to another function type (D-041). Binds marks a
	// function value that `bind` made of another, which it captures first (D-044). Both
	// show as the function they wrap.
	Adapts bool
	Binds  bool
	// Instances, for a function whose closures are a generic function's erased copy, are
	// the instances of the generic function that typed calls of the closures find (D-148).
	Instances []*Instance
	// Shares, for an instance of a generic arrow function or function expression, is the
	// function of its erased copy, whose closures the instance runs with (D-190): the two
	// have the same Captures, read from the erased copy's closure struct.
	Shares *Func
	// Extern is the runtime function that implements a method of a Builtin class; such a
	// function has no Body and isn't among the program's functions.
	Extern string
	// Settles marks an extern function that returns a promise it settles from the event
	// loop (an intrinsic of pts:fs/promises, D-118), so a program that calls it runs the loop.
	Settles bool
	// Listens marks an extern function that starts I/O the event loop serves (pts:http's
	// listen, D-136): a program that calls it runs the loop after its top-level code.
	Listens bool
	// Generator marks a generator function (`function*`, D-109). A call makes a generator
	// object, a Value (Result), and returns it without running Body, which runs when the
	// object is resumed, holding every local in the object's frame. A Yield in Body
	// suspends it; a Return's Value, a Value, is the generator's return value.
	Generator bool
	// Async marks an async function (D-112), also marked Generator: a call runs Body as a
	// generator's until it yields, each Yield being an await of its Value, and returns the
	// promise (a Value) that the Return's Value or a throw out of Body settles. The
	// runtime resumes Body when the awaited promise settles.
	Async bool
	// AsyncGenerator marks an async generator (`async function*`, D-115), also marked
	// Generator: a call makes an AsyncGenerator object (a Value) without running Body,
	// whose Yields marked Await are awaits and the others its yields.
	AsyncGenerator bool
}

// QualifiedName is the function's name with its module's: "math:add" ("add" in the entry module).
func (f *Func) QualifiedName() string {
	if f.Module == nil || f.Module.Name == "" {
		return f.Name
	}
	return f.Module.Name + ":" + f.Name
}

// Local is a local variable. Each declaration gets its own Local, so two locals may share a Name.
type Local struct {
	Name string
	Type Type
	// Global marks a module-scope binding that other functions use: functions of its
	// module, or the code of modules that import it. It lives for the whole program, and a
	// use before its declaration has run throws ReferenceError, as in JS.
	Global bool
	// Cell marks a local that closures capture and that may change after one does: it is
	// assigned after its declaration, or captured before its declaration has run. It lives
	// in a heap cell that its function and the closures share (D-041).
	Cell bool
	// Early marks a Cell captured before its declaration has run: its cell has a ready
	// flag, which the declaration sets and the closures' reads and writes check, so a use
	// before the declaration throws ReferenceError, as in JS (D-140).
	Early bool
}

// Stmt is a statement. Every statement knows the source line it came from.
type Stmt interface {
	Pos() Pos
}

// Block is a sequence of statements with its own scope.
type Block struct {
	At    Pos
	Stmts []Stmt
}

// ExprStmt evaluates X for its effects.
type ExprStmt struct {
	At Pos
	X  Expr
}

// LocalDecl declares Local and initialises it with Init, whose type is Local.Type. A nil
// Init (`let x: T;`) means the type's zero value; tsc makes sure the local is assigned before it is read.
type LocalDecl struct {
	At    Pos
	Local *Local
	Init  Expr
}

// If runs Then when Cond (a boolean) holds, and otherwise Else: nil, a *Block or an *If.
type If struct {
	At   Pos
	Cond Expr
	Then *Block
	Else Stmt
}

// Label is a target for Break and Continue. Every loop and switch has one, labelled in
// the source or not, so a Break or Continue always names its target.
type Label struct {
	Name string // the source label, or ""
}

// Loop is while, do-while and for. Cond (a boolean; nil means forever) is tested before
// each iteration, or after it if PostTest; Update runs after each iteration, also after a continue.
type Loop struct {
	At       Pos
	Label    *Label
	Cond     Expr
	Update   Expr
	PostTest bool
	Body     *Block
	// Bindings are the `let` variables of a for loop's initialiser. As in JS, each
	// iteration has its own copy, made before the update, so a closure made in one
	// iteration keeps that iteration's variable.
	Bindings []*Local
}

// Switch runs the cases from the first whose Match holds, trying them in order, or from
// the default case (Match nil) if none does; control falls through into the following cases.
type Switch struct {
	At    Pos
	Label *Label
	Cases []*Case
}

type Case struct {
	At    Pos
	Match Expr
	Body  []Stmt
}

// LabeledBlock is a labelled statement other than a loop or switch; Break can leave it.
type LabeledBlock struct {
	At    Pos
	Label *Label
	Body  *Block
}

type Break struct {
	At     Pos
	Target *Label
}

// Return leaves the function with Value, which is nil in a function whose Result is Void.
type Return struct {
	At    Pos
	Value Expr
}

// Continue starts the next iteration of the loop with label Target.
type Continue struct {
	At     Pos
	Target *Label
}

// Try runs Body, and if Body throws, Catch, which starts with Exception, a Value local of
// its own, holding what Body threw (D-068). A break, continue or return that leaves Body
// leaves the try; `finally` is lowered to tries and jumps.
type Try struct {
	At        Pos
	Body      *Block
	Exception *Local
	Catch     *Block
}

// Throw throws Value, a Value, to the innermost Try around it in this function or its
// callers; with none, the program ends with it uncaught.
type Throw struct {
	At    Pos
	Value Expr
}

// Yield suspends the generator whose Body runs it, giving its consumer Value, a Value
// (D-109). The generator resumes after it when next(), throw() or return() is called on
// it: Resumed is the value that call passed and ResumedBy which call it was.
type Yield struct {
	At    Pos
	Value Expr
	Await bool // in an async generator, an await (D-115)
	// Delegate, in a yield* (D-109), is the inner iterator whose step this yield passes
	// on: its value, or the result object it handed back, which the generator hands back
	// in turn (GeneratorYield(innerResult), H03b). Value is nil then.
	Delegate Expr
}

// ResumeMode is how a suspended generator was resumed: by next(), throw() or return().
type ResumeMode int

const (
	ResumeNext ResumeMode = iota
	ResumeThrow
	ResumeReturn
)

// Resumed is the value that resumed the generator after a Yield: next(v)'s v, the error
// throw(e) threw in, return(r)'s r; a Value.
type Resumed struct{}

// ResumedBy holds when the generator was resumed after a Yield by Mode's call.
type ResumedBy struct{ Mode ResumeMode }

func (s *Block) Pos() Pos        { return s.At }
func (s *Try) Pos() Pos          { return s.At }
func (s *Throw) Pos() Pos        { return s.At }
func (s *Yield) Pos() Pos        { return s.At }
func (s *ExprStmt) Pos() Pos     { return s.At }
func (s *LocalDecl) Pos() Pos    { return s.At }
func (s *If) Pos() Pos           { return s.At }
func (s *Loop) Pos() Pos         { return s.At }
func (s *Switch) Pos() Pos       { return s.At }
func (s *LabeledBlock) Pos() Pos { return s.At }
func (s *Break) Pos() Pos        { return s.At }
func (s *Continue) Pos() Pos     { return s.At }
func (s *Return) Pos() Pos       { return s.At }

// Expr is an expression.
type Expr interface {
	Type() Type
}

type NumberConst struct{ Value float64 }

type BoolConst struct{ Value bool }

// StringConst is a JS string literal. Value is UTF-8, except that a lone surrogate is
// the 3-byte sequence the TypeScript scanner produces for it (stringutil.EncodeJSStringRune).
type StringConst struct{ Value string }

// IntConst is a constant of an integer type, held as its two's complement bits
// (sign-extended to 64 bits for signed types).
type IntConst struct {
	Bits uint64
	Ty   Type
}

// Convert converts a numeric value to another numeric type: losslessly for the
// implicit conversions, with Rust's `as` rules otherwise (docs/SPEC.md §3.2).
type Convert struct {
	X  Expr
	To Type
}

type UndefinedConst struct{}

type NullConst struct{}

// GivenConst is the flag a call passes after an argument for a parameter with a default.
type GivenConst struct{ Value bool }

// RegExpLiteral is a regex literal's value: a new instance of Class (the builtin RegExp)
// each time, over the pattern's code, compiled the first time and kept per literal (D-133).
type RegExpLiteral struct {
	Pattern, Flags string
	Class          *Class
}

// HasKey is `"Name" in X` looked up at run time in the object X refers to (D-067).
type HasKey struct {
	X    Expr
	Name string
}

// IsAbsent is whether X, an optional field's value read Raw, is the absent value.
type IsAbsent struct{ X Expr }

// Absent is what an object stores in an optional property it leaves out (D-065); it has
// T, the field's type, for the IR's sake.
type Absent struct{ T Type }

// LeftOut is what a call passes for an argument it leaves out (or passes undefined for):
// the zero value of T, which the callee replaces with the parameter's default (D-055).
type LeftOut struct{ T Type }

// LocalRef reads a local.
type LocalRef struct{ Local *Local }

// Assign stores Value (of the local's type) in Local. Its value is the new value, or the
// old one if Postfix (x++).
type Assign struct {
	Local   *Local
	Value   Expr
	Postfix bool
}

// Truthy is JS's ToBoolean of a primitive value, static or boxed.
type Truthy struct{ X Expr }

// Conditional is Cond ? Then : Else, with Cond a boolean and the branches of one type.
type Conditional struct {
	Cond, Then, Else Expr
}

// LogicalOp is && or ||, which yield one of their operands.
type LogicalOp int

const (
	And LogicalOp = iota
	Or
)

var logicalOpText = [...]string{And: "&&", Or: "||"}

func (op LogicalOp) String() string { return logicalOpText[op] }

// Logical is X && Y or X || Y, with operands of one type: Y is evaluated only if X is
// truthy (&&) or falsy (||), and the value is the last operand evaluated.
type Logical struct {
	Op   LogicalOp
	X, Y Expr
}

// Sequence evaluates X for its effects, then Y, whose value it has (the comma operator).
type Sequence struct{ X, Y Expr }

// Bind evaluates Value into Local, a new local of the current function, then In, whose
// value it has: a value used twice but evaluated once.
type Bind struct {
	Local *Local
	Value Expr
	In    Expr
}

// New makes an instance of Class with every field zero and runs its constructor with Args.
// Ready, if set, is the module-scope binding the class's declaration sets: where the
// declaration may not have run yet, New first checks that it has (D-141).
type New struct {
	Class *Class
	Args  []Expr
	Ready *Local
}

// NewObject makes an object of a shape from an object literal: Values are the
// properties' values in Shape.Orders[Order], evaluated in that order.
type NewObject struct {
	Shape  *Class
	Order  int
	Values []Expr
	// OrderOf, if not nil, is an object whose property order the new one takes if it is
	// an object of Shape (D-108): `{ ...o }` lists o's properties in o's order.
	OrderOf Expr
}

// NewArray makes an array from an array literal: Elems, of Array's element type, evaluated in order.
type NewArray struct {
	Array *Array
	Elems []Expr
	// Length, if not nil, makes `new Array(n)` instead (no Elems): n holes, or zeros of
	// a primitive element type (D-085); RangeError for an invalid length.
	Length Expr
}

// ArrayOp is an operation on an array.
type ArrayOp int

const (
	ArrayLength             ArrayOp = iota // a.length
	ArraySetLength                         // a.length = n, for n at most the length; its value is n
	ArrayElement                           // a[i] as a dynamic value: undefined for an index out of range (D-015)
	ArrayElementChecked                    // a[i], which throws RangeError for an index out of range
	ArraySetElement                        // a[i] = v, for i at most the length (which appends); its value is v
	ArrayPush                              // a.push(v, …), whose value is the new length
	ArrayPushAll                           // a.push(...b): the elements of b, a new array of a's type, at the end of a
	ArrayUnshiftAll                        // a.unshift(...b): the elements of b, a new array of a's type, at the start of a
	ArrayUnshift                           // a.unshift(v, …), whose value is the new length
	ArrayPop                               // a.pop() as a dynamic value: undefined if a is empty
	ArrayPopChecked                        // a.pop()!, which throws RangeError if a is empty
	ArrayShift                             // a.shift() as a dynamic value: undefined if a is empty
	ArrayShiftChecked                      // a.shift()!, which throws RangeError if a is empty
	ArraySlice                             // a.slice(start, end): a new array; a left-out start is 0, a left-out end Infinity
	ArraySplice                            // a.splice(start, deleteCount, v…): the removed elements, as a new array
	ArrayConcat                            // a.concat(x…): a new array; each x is an array of a's type, whose elements it takes, or an element
	ArrayIndexOf                           // a.indexOf(v, from): the first index of an element === v, or -1
	ArrayLastIndexOf                       // a.lastIndexOf(v, from): the last index of an element === v, or -1; a left-out from is Infinity
	ArrayIncludes                          // a.includes(v, from): whether an element is v by SameValueZero
	ArrayJoin                              // a.join(separator): the elements' strings, joined
	ArrayReverse                           // a.reverse(): a, reversed in place
	ArraySort                              // a.sort(): a, sorted in place by the elements' strings, stably
	ArrayFlat                              // a.flat(depth): a new array of To's type, arrays and tuples in it flattened depth levels; depth is a number or a dynamic value (D-180)
	ArrayFill                              // a.fill(v, start, end): a, with v in [start, end)
	ArraySortWith                          // a.sort(compare): a, sorted in place stably by compare, a function value taking two elements and returning a number
	ArrayAt                                // a.at(i) as a dynamic value: from the end for a negative i, undefined out of range (D-085)
	ArrayCopy                              // a new array of a's elements as the copying methods read them: holes undefined where the element type holds undefined (D-182, D-183)
	ArrayWith                              // a.with(i, v): a copy with v at i (from the end if negative); RangeError out of range
	ArrayKeys                              // a.keys() as a number[] of a's indices
	ArrayPushFlat                          // flatMap's step: pushes the elements of v, a dynamic value, if it is an array or a tuple, else v, each as a's element type holds it (D-180)
	ArrayElementOrUndefined                // a[i] for an a whose element type holds undefined, as JS reads it: undefined for a hole or an index out of range
	ArrayHas                               // whether a has an element at i, not past its end or a hole (JS's HasProperty); a match's undefined group is one (D-133)
	ArraySpread                            // [...x, …]: a new array; each x is an array of a's type whose elements it takes as iterating reads them (holes undefined where the elements may be), or an element; each x is read before the next is evaluated, as JS iterates a spread at its place
)

var arrayOpText = [...]string{ArrayLength: "length", ArraySetLength: "setLength", ArrayElement: "element", ArrayElementChecked: "elementChecked",
	ArraySetElement: "setElement", ArrayPush: "push", ArrayPushAll: "pushAll", ArrayUnshift: "unshift", ArrayUnshiftAll: "unshiftAll", ArrayPop: "pop", ArrayPopChecked: "popChecked",
	ArrayShift: "shift", ArrayShiftChecked: "shiftChecked", ArraySlice: "slice", ArraySplice: "splice", ArrayConcat: "concat",
	ArrayIndexOf: "indexOf", ArrayLastIndexOf: "lastIndexOf", ArrayIncludes: "includes", ArrayJoin: "join", ArrayReverse: "reverse",
	ArraySort: "sort", ArrayFlat: "flat", ArrayFill: "fill", ArraySortWith: "sortWith", ArrayAt: "at", ArrayCopy: "copy", ArrayWith: "with",
	ArrayKeys: "keys", ArrayPushFlat: "pushFlat", ArrayElementOrUndefined: "elementOrUndefined", ArrayHas: "has", ArraySpread: "spread"}

func (op ArrayOp) String() string { return arrayOpText[op] }

// ArrayCall applies Op to the array Args[0] and the operands after it: numbers (indices,
// lengths, counts), values of the element type, a separator string, or for concat, arrays.
type ArrayCall struct {
	Op   ArrayOp
	Args []Expr
	To   *Array // the result of ArrayFlat
	As   Type   // what ArrayElementOrUndefined reads an element of pointers as, if not the element type: a union with undefined, or a dynamic value
}

// MapOp is an operation on a map or set.
type MapOp int

const (
	MapSize          MapOp = iota // m.size
	MapGet                        // m.get(k) as a dynamic value: undefined if m has no such key
	MapGetChecked                 // m.get(k)!, which throws RangeError if m has no such key
	MapSet                        // m.set(k, v), or s.add(k) on a set: m
	MapHas                        // m.has(k)
	MapDelete                     // m.delete(k): whether m had k
	MapClear                      // m.clear()
	MapIterate                    // an iteration over m, for a local of type MapIterator
	MapNext                       // moves the iteration in the local Args[0] to its next entry: false at the end
	MapIteratorKey                // the key of the entry the iteration in Args[0] is at
	MapIteratorValue              // the value of that entry
	MapPut                        // o[k] = v on a hash-map object: v
	MapGetInto                    // whether m has the key Args[1], whose value it then stores in the local Args[2]
	MapAssign                     // copies the entries of the map Args[1], of the same type, into m, in its iteration order: m
)

var mapOpText = [...]string{MapSize: "size", MapGet: "get", MapGetChecked: "getChecked", MapSet: "set", MapHas: "has", MapDelete: "delete",
	MapClear: "clear", MapIterate: "iterate", MapNext: "next", MapIteratorKey: "key", MapIteratorValue: "value", MapPut: "put", MapGetInto: "getInto", MapAssign: "assign"}

func (op MapOp) String() string { return mapOpText[op] }

// MapCall applies Op to the map Args[0], of type Map (for the iteration ops, to the
// iteration in the local Args[0], over a map of type Map), and the keys and values after it.
type MapCall struct {
	Op   MapOp
	Args []Expr
	Map  *MapType
}

// NewMap makes a map or set of type Map: empty, or with the entries of From, a map of
// the same type, or for a set, an array of its key type.
type NewMap struct {
	Map  *MapType
	From Expr
}

// FieldRef reads a field of the instance X, or through an interface, a property of the
// object X refers to.
type FieldRef struct {
	X     Expr
	Field *Field
	// Raw reads an optional field as it is, the absent value included, to store it in
	// another optional field (a spread copies only the properties an object has), and
	// another field without the check that it is set (D-141): NULL if it isn't, which a
	// logical assignment tests before a suspension (H08).
	Raw bool
}

// HasField is whether X has the optional property Field: whether its field holds a value
// rather than the absent one (`"p" in o`, D-065).
type HasField struct {
	X     Expr
	Field *Field
}

// SetField stores Value (of the field's type) in a field of the instance X. Its value is
// the new value, or the old one if Postfix (o.x++).
type SetField struct {
	X       Expr
	Field   *Field
	Value   Expr
	Postfix bool
}

// Call calls a function of the program with arguments of its parameter types; a
// Closure function runs with Closure, one of its closures.
type Call struct {
	Func    *Func
	Closure Expr
	Args    []Expr
}

// MakeClosure is the function value of Func, of type FuncType: for a Closure function, a new
// closure holding the current values of its Captures (one static closure if it captures
// nothing); for any other function, its one static closure.
type MakeClosure struct {
	Func     *Func
	FuncType *FuncType
}

// CallValue calls the function value Callee with Args of its parameter types.
type CallValue struct {
	Callee Expr
	Args   []Expr
}

// VirtualCall calls a virtual method on the instance Args[0], an instance of the
// method's slot's class: the implementation the instance's class has, its own or
// inherited, runs.
type VirtualCall struct {
	Method *Method
	Args   []Expr
}

// Instance is a monomorphized instance of a generic function or method that typed calls
// knowing their type arguments find at run time by its signature, instead of the erased
// copy a function value or a method table holds (D-148).
type Instance struct {
	Name      string    // the method's; "" for a function value's
	Signature *FuncType // the calls' function type, after `this` for a method
	// Key, for calls whose result is a dynamic value (a promise or generator object), tells
	// apart the calls' type arguments (D-190): calls with the same Signature and another
	// Key make their values for other type arguments, so they are other entries. It names
	// the first such call's result type.
	Key  string
	Func *Func // nil for a method that has no instance of Signature, which ends the search
}

// InstanceCall calls the instance of Signature (D-148) that X lists, with X and Args: X is a
// closure, or for a method named Name (or the string ByKey holds) an object, whose class or
// nearest ancestor with an entry for the method lists it. If there is none, it is
// Otherwise, a call of the erased copy. X, ByKey and Args are local references, which
// Otherwise uses too.
type InstanceCall struct {
	X         Expr
	Name      string
	ByKey     Expr // for a method an element access's key names at run time, the key, a string (D-194)
	Signature *FuncType
	Key       string // the Key of the instances it finds (Instance.Key)
	Args      []Expr
	Otherwise Expr
	// Dynamic makes its value a dynamic value: the instance's result boxed, or Otherwise's,
	// a dynamic value (a call through a union of object types, whose erased copy's result
	// stays dynamic, D-191).
	Dynamic bool
}

// InterfaceCall calls a method of an interface on Args[0], a reference of that interface
// type: the method that the class of the object it refers to has, found at run time.
type InterfaceCall struct {
	Method *InterfaceMethod
	Args   []Expr
}

// ClassCast is X, a reference to an object, as a reference of another object type To
// (a class or an Interface): an ancestor of its static class, an interface its fields
// provide, or a class the checker narrowed it to.
type ClassCast struct {
	X  Expr
	To Type
	// FromResult marks X as an iterator result's object (Class.Result), which may be of any
	// layout (D-178): it is To as it is if it is one of X's shape's objects, and otherwise
	// converted to To with a check, as dynamic data is (D-071).
	FromResult bool
}

// InstanceOf is X instanceof Class, for an instance X.
type InstanceOf struct {
	X     Expr
	Class *Class
	// Family, for a generic class, is its instances (the program's when it is complete):
	// X is an instance of one of them, and Class is nil.
	Family *[]*Class
}

// FamilyField reads the private field Name (`#f`) of X, a dynamic value that tsc narrowed
// to a generic class whose instances are Family (the program's when it is complete): the
// field of whichever of them X is, as a dynamic value, or for another object the TypeError
// JS throws. A dynamic value's properties don't hold private names (H10).
type FamilyField struct {
	X      Expr
	Family *[]*Class
	Name   string
}

// FamilySetField stores Value, a dynamic value, in the private field Name (`#f`) of X, a
// dynamic value held where tsc says a generic class whose instances are Family: the field of
// whichever of them X is, converted to its type with a check, or for another object the
// TypeError JS throws. Its value is Value (H10b).
type FamilySetField struct {
	X      Expr
	Family *[]*Class
	Name   string
	Value  Expr
}

// FamilyCall calls the private method Name (`#m`) of X, a dynamic value held where tsc says a
// generic class whose instances are Family, with Args, dynamic values: the method of
// whichever of them X is, through its dynamic entry, which converts the arguments with
// checks, or for another object the TypeError JS throws. Its result is a dynamic value
// (H10b).
type FamilyCall struct {
	X      Expr
	Family *[]*Class
	Class  string // the generic class's name, for the TypeError
	Name   string
	Args   []Expr
}

// Box turns a statically typed value into a dynamic Value.
type Box struct{ X Expr }

// ToUnion makes X, a value of a member of To or of a union of some of its members, a
// value of the union To.
type ToUnion struct {
	X  Expr
	To *Union
}

// FromUnion is X, a value of a union, as a value of To, one of its members or a union of
// some of them, which the checker has narrowed X to (D-062). If Checked (`x!`), a null or
// undefined X throws TypeError instead.
type FromUnion struct {
	X       Expr
	To      Type
	Checked bool
	// AsIs, for X a dynamic value and To a type it converts to by a copy, converts it only
	// where it is one already: otherwise TypeError, never a copy writes would miss (D-219).
	AsIs bool
}

// IsNullish is whether X, a value of a union or a dynamic value, is null or undefined
// (`x == null`), or if Only is Null or Undefined, that one (`x === null`).
type IsNullish struct {
	X    Expr
	Only Type
}

// RestArgs is the array X, a rest parameter (of type Rest), holds.
type RestArgs struct{ X Expr }

// SpreadArg is an argument `...X` of a console method or of a call through `any`: X, an
// array (or for a call through `any`, ValueSpreadItems' value), passes its elements as
// arguments (D-057, D-075). It only appears among those calls' arguments.
type SpreadArg struct{ X Expr }

// Builtin is a runtime function with a fixed signature.
type Builtin struct {
	Name   string // as written in TypeScript
	Params []Type // the receiver first for a member; nil for console.log and the console methods like it
	Result Type
}

// ConsoleMethods are the console methods that take any number of Value arguments.
var ConsoleMethods = map[*Builtin]bool{
	ConsoleLog: true, ConsoleError: true, ConsoleGroup: true, ConsoleAssert: true, ConsoleTrace: true,
	ConsoleCount: true, ConsoleCountReset: true, ConsoleTime: true, ConsoleTimeEnd: true, ConsoleTimeLog: true,
}

// IsConsole reports whether a builtin is a method of console.
func IsConsole(builtin *Builtin) bool {
	return ConsoleMethods[builtin] || builtin == ConsoleGroupEnd || builtin == ConsoleDir || builtin == ConsoleTable
}

// mathFunction is a function of Math that takes n numbers and returns a number.
func mathFunction(name string, n int) *Builtin {
	params := make([]Type, n)
	for i := range params {
		params[i] = Number
	}
	return &Builtin{Name: "Math." + name, Params: params, Result: Number}
}

// numberMember is a member of Number.prototype; it takes the number, then params.
func numberMember(name string, result Type, params ...Type) *Builtin {
	return &Builtin{Name: "Number." + name, Params: append([]Type{Number}, params...), Result: result}
}

// numberTest is one of Number's predicates (isInteger, …) of a value of type t.
func numberTest(name string, t Type) *Builtin {
	return &Builtin{Name: "Number." + name, Params: []Type{t}, Result: Bool}
}

// stringMember is a member of String.prototype; it takes the string, then params.
func stringMember(name string, result Type, params ...Type) *Builtin {
	return &Builtin{Name: "String." + name, Params: append([]Type{String}, params...), Result: result}
}

var (
	// ConsoleLog is console.log; it takes any number of Value arguments, as the other
	// console methods in ConsoleMethods do (D-088).
	ConsoleLog        = &Builtin{Name: "console.log", Result: Void}
	ConsoleError      = &Builtin{Name: "console.error", Result: Void}
	ConsoleGroup      = &Builtin{Name: "console.group", Result: Void}
	ConsoleAssert     = &Builtin{Name: "console.assert", Result: Void}
	ConsoleTrace      = &Builtin{Name: "console.trace", Result: Void}
	ConsoleCount      = &Builtin{Name: "console.count", Result: Void}
	ConsoleCountReset = &Builtin{Name: "console.countReset", Result: Void}
	ConsoleTime       = &Builtin{Name: "console.time", Result: Void}
	ConsoleTimeEnd    = &Builtin{Name: "console.timeEnd", Result: Void}
	ConsoleTimeLog    = &Builtin{Name: "console.timeLog", Result: Void}
	// ConsoleGroupEnd is console.groupEnd(); ConsoleDir is console.dir(value, { depth,
	// showHidden }); ConsoleTable is console.table(data, properties), properties undefined or a string[].
	ConsoleGroupEnd = &Builtin{Name: "console.groupEnd", Result: Void}
	ConsoleDir      = &Builtin{Name: "console.dir", Params: []Type{Value, Number, Bool}, Result: Void}
	ConsoleTable    = &Builtin{Name: "console.table", Params: []Type{Value, Value}, Result: Void}
	// StringLength is a string's .length (UTF-16 code units).
	StringLength = stringMember("length", Number)

	// s[i] (D-015): StringElement is undefined for an index out of range, StringElementChecked
	// throws RangeError instead, and StringElementEquals is s[i] === t.
	StringElement        = stringMember("element", Value, Number)
	StringElementChecked = stringMember("elementChecked", String, Number)
	StringElementEquals  = stringMember("elementEquals", Bool, Number, String)

	// The String.prototype methods. Indices count UTF-16 code units; a left-out index is
	// NaN where JS treats undefined as 0 and Infinity where it means the length.
	StringCharCodeAt  = stringMember("charCodeAt", Number, Number)
	StringSlice       = stringMember("slice", String, Number, Number)
	StringSubstring   = stringMember("substring", String, Number, Number)
	StringIndexOf     = stringMember("indexOf", Number, String, Number)
	StringIncludes    = stringMember("includes", Bool, String, Number)
	StringStartsWith  = stringMember("startsWith", Bool, String, Number)
	StringEndsWith    = stringMember("endsWith", Bool, String, Number)
	StringToUpperCase = stringMember("toUpperCase", String)
	StringToLowerCase = stringMember("toLowerCase", String)
	StringTrim        = stringMember("trim", String)
	StringTrimStart   = stringMember("trimStart", String)
	StringTrimEnd     = stringMember("trimEnd", String)
	StringPadStart    = stringMember("padStart", String, Number, String)
	StringPadEnd      = stringMember("padEnd", String, Number, String)
	StringRepeat      = stringMember("repeat", String, Number)

	// StringSplit is split on a string separator, with a limit (Infinity if left out);
	// ArrayFromString is Array.from of a string: its code points.
	StringSplit     = stringMember("split", StringArray, String, Number)
	ArrayFromString = &Builtin{Name: "Array.from", Params: []Type{String}, Result: StringArray}

	// The String.prototype methods of M3.5a (D-082). StringAt and StringCodePointAt are
	// undefined out of range; a left-out lastIndexOf position is NaN, which means the end.
	// StringReplace and StringReplaceAll take a string pattern and a replacement string;
	// replacing with a callback is a generated helper.
	StringAt            = stringMember("at", Value, Number)
	StringCharAt        = stringMember("charAt", String, Number)
	StringCodePointAt   = stringMember("codePointAt", Value, Number)
	StringLastIndexOf   = stringMember("lastIndexOf", Number, String, Number)
	StringSubstr        = stringMember("substr", String, Number, Number)
	StringReplace       = stringMember("replace", String, String, String)
	StringReplaceAll    = stringMember("replaceAll", String, String, String)
	StringIsWellFormed  = stringMember("isWellFormed", Bool)
	StringToWellFormed  = stringMember("toWellFormed", String)
	StringLocaleCompare = stringMember("localeCompare", Number, String)
	// StringFromCharCode and StringFromCodePoint are String.fromCharCode and
	// String.fromCodePoint of an array of their arguments.
	StringFromCharCode  = &Builtin{Name: "String.fromCharCode", Params: []Type{NumberArray}, Result: String}
	StringFromCodePoint = &Builtin{Name: "String.fromCodePoint", Params: []Type{NumberArray}, Result: String}
	// URIEncode and URIDecode are encodeURI and decodeURI, or with Args[1] true,
	// encodeURIComponent and decodeURIComponent.
	URIEncode = &Builtin{Name: "encodeURI", Params: []Type{String, Bool}, Result: String}
	URIDecode = &Builtin{Name: "decodeURI", Params: []Type{String, Bool}, Result: String}

	// ValueTypeOf is typeof of a boxed value; typeof of a static type is a constant.
	ValueTypeOf = &Builtin{Name: "typeof", Params: []Type{Value}, Result: String}
	// ValueConstructorName is `o.constructor.name`: the name of the class of o, or of a primitive's wrapper.
	ValueConstructorName = &Builtin{Name: "constructorName", Params: []Type{Value}, Result: String}
	// ValueStrictEquals is === on boxed values, with JS's rules for numbers and strings.
	ValueStrictEquals = &Builtin{Name: "===", Params: []Type{Value, Value}, Result: Bool}
	// ValueLooseEquals is == on boxed values, with JS's conversions (D-067).
	ValueLooseEquals = &Builtin{Name: "==", Params: []Type{Value, Value}, Result: Bool}
	// DynamicGet and DynamicSet read and write the property (or element) a key names of a
	// dynamic value, found at run time (D-070); DynamicSet's value is the value written,
	// as stored: a checked copy where converting made one (D-176).
	DynamicGet = &Builtin{Name: "get", Params: []Type{Value, Value}, Result: Value}
	DynamicSet = &Builtin{Name: "set", Params: []Type{Value, Value, Value}, Result: Value}
	DynamicHas = &Builtin{Name: "in", Params: []Type{Value, Value}, Result: Bool}
	// DynamicCall calls a dynamic value (D-074): its arguments are the callee, the callee's
	// source text (a string, for the TypeError if it isn't a function) and the call's
	// arguments as dynamic values. DynamicCallMethod calls the method a key names of a
	// dynamic value: the object, the key, the source text, then the arguments.
	DynamicCall       = &Builtin{Name: "call", Result: Value}
	DynamicCallMethod = &Builtin{Name: "call method", Result: Value}
	// DynamicMethodOf and DynamicCallFound are DynamicCallMethod in two steps, for a call
	// whose arguments suspend (H08): DynamicMethodOf (the object, the key) finds what the
	// call calls before the arguments are evaluated, as JS does, and DynamicCallFound calls
	// it: the object, the key, what DynamicMethodOf found, the source text, then the arguments.
	DynamicMethodOf  = &Builtin{Name: "method of", Params: []Type{Value, Value}, Result: Value}
	DynamicCallFound = &Builtin{Name: "call found", Result: Value}
	// ValueIsArray is Array.isArray of a dynamic value or a union (D-076).
	ValueIsArray = &Builtin{Name: "Array.isArray", Params: []Type{Value}, Result: Bool}
	// ValueKeys, ValueValues and ValueEntries are Object.keys, values and entries of a
	// dynamic value, as a dynamic value (a string[], an any[], an any[] of [key, value]
	// any[]s), and ValueHasOwn is Object.hasOwn of one (D-077).
	ValueKeys    = &Builtin{Name: "Object.keys", Params: []Type{Value}, Result: Value}
	ValueValues  = &Builtin{Name: "Object.values", Params: []Type{Value}, Result: Value}
	ValueEntries = &Builtin{Name: "Object.entries", Params: []Type{Value}, Result: Value}
	ValueHasOwn  = &Builtin{Name: "Object.hasOwn", Params: []Type{Value, Value}, Result: Bool}
	// TupleLength is the length of a tuple with optional elements (D-100).
	TupleLength = &Builtin{Name: "tuple length", Params: []Type{Value}, Result: Number}
	// FunctionName and FunctionLength are a function value's `name` and `length` (D-098).
	FunctionName   = &Builtin{Name: "Function.name", Params: []Type{Value}, Result: String}
	FunctionLength = &Builtin{Name: "Function.length", Params: []Type{Value}, Result: Number}
	// ValueKeyArray is Object.keys of any value as a string[]; ObjectAssign is
	// Object.assign(target, ...sources) with the sources as an any[]; ValueSameValue is
	// Object.is (D-086).
	ValueKeyArray  = &Builtin{Name: "Object.keys", Params: []Type{Value}, Result: StringArray}
	ObjectAssign   = &Builtin{Name: "Object.assign", Params: []Type{Value, ValueArray}, Result: Void}
	ValueSameValue = &Builtin{Name: "Object.is", Params: []Type{Value, Value}, Result: Bool}
	// The iteration protocol (D-109): IteratorOpen is an iterator over an iterable (its
	// second argument the iterable's source text, for TypeError); IteratorResume resumes
	// one as next(v), throw(v) or return(v) do (its I32 argument a ResumeMode) and reports
	// whether it is done; IteratorValue is what it last yielded or returned; IteratorClose
	// is what a loop that leaves early calls.
	IteratorOpen   = &Builtin{Name: "iterator", Params: []Type{Value, String}, Result: Value}
	IteratorResume = &Builtin{Name: "Iterator.next", Params: []Type{Value, I32, Value}, Result: Bool}
	IteratorValue  = &Builtin{Name: "Iterator.value", Params: []Type{Value}, Result: Value}
	IteratorClose  = &Builtin{Name: "Iterator.return", Params: []Type{Value}, Result: Void}
	// A `for…of` or an array pattern over an iterable (D-109, H03) steps through what
	// IterationOpen gives (its second argument the iterable's source text): the iterable's
	// iterator, if it has one to step (a generator, runtime iterator, map, set or object
	// with a [Symbol.iterator] method), or its items (an array's, typed array's or
	// string's), which are read by index. IterationNext reports whether the source is done
	// at the index of its Number argument, resuming an iterator with next() to find out;
	// IterationValue is the item there, IterationRest an any[] of the items from there on
	// (none if its Bool argument says the source is done), and IterationClose is what a
	// loop or pattern that leaves its iterator early calls.
	IterationOpen  = &Builtin{Name: "iteration", Params: []Type{Value, String}, Result: Value}
	IterationNext  = &Builtin{Name: "iteration.next", Params: []Type{Value, Number}, Result: Bool}
	IterationValue = &Builtin{Name: "iteration.value", Params: []Type{Value, Number}, Result: Value}
	IterationRest  = &Builtin{Name: "iteration.rest", Params: []Type{Value, Number, Bool}, Result: ValueArray}
	IterationClose = &Builtin{Name: "iteration.return", Params: []Type{Value}, Result: Void}
	// IteratorGet is iterable[Symbol.iterator]() (its second argument the iterable's source
	// text); IteratorOf is an iterator itself, or one that calls an object's next() (D-111).
	IteratorGet = &Builtin{Name: "Symbol.iterator", Params: []Type{Value, String}, Result: Value}
	IteratorOf  = &Builtin{Name: "iterator of", Params: []Type{Value}, Result: Value}
	// IteratorCall is next(v), return(v) or throw(v) called on an iterator (H03b), its I32
	// argument a ResumeMode and its String the callee's source text, for the TypeError of
	// an object without the method: the result object the iterator's method returned,
	// which the call gives as it is (an object's own next()'s, or what a generator's yield*
	// handed back), or, if the iterator made its result itself, whether it is done, its
	// value then IteratorValue. IteratorHanded is whether such a step is a result object,
	// and IteratorCallDone whether one the iterator made says done.
	IteratorCall     = &Builtin{Name: "Iterator.call", Params: []Type{Value, I32, Value, String}, Result: Value}
	IteratorHanded   = &Builtin{Name: "Iterator.handed", Params: []Type{Value}, Result: Bool}
	IteratorCallDone = &Builtin{Name: "Iterator.callDone", Params: []Type{Value}, Result: Bool}
	// Promises (D-112): PromiseNew is `new Promise(executor)` (its second argument the
	// executor's source text); PromiseThen is p.then(onFulfilled, onRejected), either
	// undefined; PromiseFinally is p.finally(f); PromiseResolve and PromiseReject are
	// Promise.resolve(v) and Promise.reject(r).
	PromiseNew     = &Builtin{Name: "Promise", Params: []Type{Value, String}, Result: Value}
	PromiseThen    = &Builtin{Name: "Promise.then", Params: []Type{Value, Value, Value}, Result: Value}
	PromiseFinally = &Builtin{Name: "Promise.finally", Params: []Type{Value, Value}, Result: Value}
	PromiseResolve = &Builtin{Name: "Promise.resolve", Params: []Type{Value}, Result: Value}
	PromiseReject  = &Builtin{Name: "Promise.reject", Params: []Type{Value}, Result: Value}
	// PromiseAll, PromiseAllSettled, PromiseAny and PromiseRace are Promise's combinators
	// over an iterable (its second argument the iterable's source text);
	// PromiseWithResolvers is Promise.withResolvers() (D-113).
	PromiseAll           = &Builtin{Name: "Promise.all", Params: []Type{Value, String}, Result: Value}
	PromiseAllSettled    = &Builtin{Name: "Promise.allSettled", Params: []Type{Value, String}, Result: Value}
	PromiseAny           = &Builtin{Name: "Promise.any", Params: []Type{Value, String}, Result: Value}
	PromiseRace          = &Builtin{Name: "Promise.race", Params: []Type{Value, String}, Result: Value}
	PromiseWithResolvers = &Builtin{Name: "Promise.withResolvers", Result: Value}
	// The event loop (D-114): TimerSet is setTimeout or setInterval (its Bool argument)
	// of a callback, a delay and the callback's arguments; TimerClear is clearTimeout and
	// clearInterval; TimerRef, TimerHasRef and TimerRefresh are a Timeout's ref() or
	// unref() (its Bool argument), hasRef() and refresh(); ImmediateSet and ImmediateClear
	// are setImmediate and clearImmediate; QueueMicrotask and PerformanceNow are
	// queueMicrotask and performance.now().
	TimerSet       = &Builtin{Name: "setTimeout", Params: []Type{Value, Value, ValueArray, Bool}, Result: Value}
	TimerClear     = &Builtin{Name: "clearTimeout", Params: []Type{Value}, Result: Void}
	TimerRef       = &Builtin{Name: "Timeout.ref", Params: []Type{Value, Bool}, Result: Value}
	TimerHasRef    = &Builtin{Name: "Timeout.hasRef", Params: []Type{Value}, Result: Bool}
	TimerRefresh   = &Builtin{Name: "Timeout.refresh", Params: []Type{Value}, Result: Value}
	ImmediateSet   = &Builtin{Name: "setImmediate", Params: []Type{Value, ValueArray}, Result: Value}
	ImmediateClear = &Builtin{Name: "clearImmediate", Params: []Type{Value}, Result: Void}
	QueueMicrotask = &Builtin{Name: "queueMicrotask", Params: []Type{Value}, Result: Void}
	PerformanceNow = &Builtin{Name: "performance.now", Result: Number}
	// Parallel is `parallel e` (D-121): its argument is the task, a closure of no
	// parameters that returns e's value boxed; its result is the task's promise.
	Parallel = &Builtin{Name: "parallel", Params: []Type{Value}, Result: Value}
	// Async iteration (D-115): AsyncIteratorOpen is GetIterator(v, async) (its second
	// argument v's source text); AsyncIteratorResume is next(v), throw(v) or return(v)
	// (its I32 argument a ResumeMode) on an async iterator, a promise; AsyncIteratorClose
	// is the promise of its return() for a loop that leaves early (undefined if it has
	// none); IteratorResultDone and IteratorResultValue read a result object.
	AsyncIteratorOpen   = &Builtin{Name: "Symbol.asyncIterator", Params: []Type{Value, String}, Result: Value}
	AsyncIteratorResume = &Builtin{Name: "AsyncIterator.next", Params: []Type{Value, I32, Value}, Result: Value}
	AsyncIteratorClose  = &Builtin{Name: "AsyncIterator.return", Params: []Type{Value}, Result: Value}
	IteratorResultDone  = &Builtin{Name: "IteratorResult.done", Params: []Type{Value}, Result: Bool}
	IteratorResultValue = &Builtin{Name: "IteratorResult.value", Params: []Type{Value}, Result: Value}
	// ValueIsPromise is `x instanceof Promise`.
	ValueIsPromise = &Builtin{Name: "instanceof Promise", Params: []Type{Value}, Result: Bool}
	// ValueIsMap and ValueIsSet are `x instanceof Map` and `x instanceof Set`.
	ValueIsMap = &Builtin{Name: "instanceof Map", Params: []Type{Value}, Result: Bool}
	ValueIsSet = &Builtin{Name: "instanceof Set", Params: []Type{Value}, Result: Bool}
	// MapIteratorOf is m.values(), m.keys() or m.entries() (its I32 argument 0, 1 or 2) as a value: an iterator (D-110).
	MapIteratorOf = &Builtin{Name: "Map.iterator", Params: []Type{Value, I32}, Result: Value}
	// ValueFreeze marks an object frozen, which ValueIsFrozen tests (D-105).
	ValueFreeze   = &Builtin{Name: "Object.freeze", Params: []Type{Value}, Result: Void}
	ValueIsFrozen = &Builtin{Name: "Object.isFrozen", Params: []Type{Value}, Result: Bool}
	// ValueToArray is a new any[] of a dynamic value's items (D-078), for spreading: an
	// array's or tuple's elements, a string's code points, a set's elements, a map's entries
	// or all an iterator yields; its second argument is the value's source text, for V8's
	// `TypeError: x is not iterable`.
	ValueToArray = &Builtin{Name: "Array.from", Params: []Type{Value, String}, Result: ValueArray}
	// ValueSpreadItems is what a spread argument of a call through `any` hands the runtime,
	// which spreads arrays, tuples and typed arrays at the call: such a value as it is, or a
	// new array of anything else's items (D-078's, as ValueToArray takes them).
	ValueSpreadItems = &Builtin{Name: "spread", Params: []Type{Value, String}, Result: Value}
	// JSONParse is JSON.parse of a string: a dynamic value (D-080). JSONStringify is
	// JSON.stringify(value, replacer, space): a string, or undefined.
	JSONParse = &Builtin{Name: "JSON.parse", Params: []Type{String}, Result: Value}
	// JSONParseRevived is JSON.parse(text, reviver) for a reviver closure of type
	// (key: string, value: any) => any, boxed.
	JSONParseRevived = &Builtin{Name: "JSON.parse", Params: []Type{String, Value}, Result: Value}
	JSONStringify    = &Builtin{Name: "JSON.stringify", Params: []Type{Value, Value, Value}, Result: Value}
	// ValueAdd is + on dynamic values; ValueToNumber is JS's ToNumber; ValueLess and
	// ValueLessEqual are < and <= with JS's conversions (> and >= swap the operands).
	ValueAdd       = &Builtin{Name: "+", Params: []Type{Value, Value}, Result: Value}
	ValueToNumber  = &Builtin{Name: "Number", Params: []Type{Value}, Result: Number}
	ValueLess      = &Builtin{Name: "<", Params: []Type{Value, Value}, Result: Bool}
	ValueLessEqual = &Builtin{Name: "<=", Params: []Type{Value, Value}, Result: Bool}

	// The Math functions of numbers, with JS's semantics (NaN and -0 included). MathMax
	// and MathMin take two numbers; Math.max(a, b, c) applies MathMax twice.
	MathSqrt  = mathFunction("sqrt", 1)
	MathAbs   = mathFunction("abs", 1)
	MathFloor = mathFunction("floor", 1)
	MathCeil  = mathFunction("ceil", 1)
	MathRound = mathFunction("round", 1)
	MathTrunc = mathFunction("trunc", 1)
	MathSign  = mathFunction("sign", 1)
	MathMax   = mathFunction("max", 2)
	MathMin   = mathFunction("min", 2)
	// The rest of Math (D-084): fdlibm's functions as V8 has them, pow as **, and
	// random. MathMaxOf, MathMinOf and MathHypot take their arguments as one array.
	MathAcos   = mathFunction("acos", 1)
	MathAcosh  = mathFunction("acosh", 1)
	MathAsin   = mathFunction("asin", 1)
	MathAsinh  = mathFunction("asinh", 1)
	MathAtan   = mathFunction("atan", 1)
	MathAtanh  = mathFunction("atanh", 1)
	MathAtan2  = mathFunction("atan2", 2)
	MathCbrt   = mathFunction("cbrt", 1)
	MathClz32  = mathFunction("clz32", 1)
	MathCos    = mathFunction("cos", 1)
	MathCosh   = mathFunction("cosh", 1)
	MathExp    = mathFunction("exp", 1)
	MathExpm1  = mathFunction("expm1", 1)
	MathFround = mathFunction("fround", 1)
	MathImul   = mathFunction("imul", 2)
	MathLog    = mathFunction("log", 1)
	MathLog10  = mathFunction("log10", 1)
	MathLog1p  = mathFunction("log1p", 1)
	MathLog2   = mathFunction("log2", 1)
	MathPow    = mathFunction("pow", 2)
	MathRandom = mathFunction("random", 0)
	MathSin    = mathFunction("sin", 1)
	MathSinh   = mathFunction("sinh", 1)
	MathTan    = mathFunction("tan", 1)
	MathTanh   = mathFunction("tanh", 1)
	MathMaxOf  = &Builtin{Name: "Math.max", Params: []Type{NumberArray}, Result: Number}
	MathMinOf  = &Builtin{Name: "Math.min", Params: []Type{NumberArray}, Result: Number}
	MathHypot  = &Builtin{Name: "Math.hypot", Params: []Type{NumberArray}, Result: Number}

	// Number.prototype's formatting methods (D-083): toFixed, toExponential and toPrecision
	// round half up and throw RangeError for digits out of range;
	// NumberToExponentialShortest is toExponential(). NumberToStringRadix is toString(radix),
	// which I64ToStringRadix and U64ToStringRadix do exactly for 64-bit integers.
	NumberToFixed               = numberMember("toFixed", String, Number)
	NumberToExponential         = numberMember("toExponential", String, Number)
	NumberToExponentialShortest = numberMember("toExponential", String)
	NumberToPrecision           = numberMember("toPrecision", String, Number)
	NumberToStringRadix         = numberMember("toString", String, Number)
	NumberToLocaleString        = numberMember("toLocaleString", String)
	I64ToStringRadix            = &Builtin{Name: "Number.toString", Params: []Type{I64, Number}, Result: String}
	U64ToStringRadix            = &Builtin{Name: "Number.toString", Params: []Type{U64, Number}, Result: String}
	// Number.isInteger, isSafeInteger, isFinite and isNaN of a number, and of a dynamic
	// value (false for anything but a number).
	NumberIsInteger     = numberTest("isInteger", Number)
	NumberIsSafeInteger = numberTest("isSafeInteger", Number)
	NumberIsFinite      = numberTest("isFinite", Number)
	NumberIsNaN         = numberTest("isNaN", Number)
	ValueIsInteger      = numberTest("isInteger", Value)
	ValueIsSafeInteger  = numberTest("isSafeInteger", Value)
	ValueIsFinite       = numberTest("isFinite", Value)
	ValueIsNaN          = numberTest("isNaN", Value)
	// ParseInt is parseInt(string, radix), radix 0 if left out; ParseFloat is parseFloat.
	ParseInt   = &Builtin{Name: "parseInt", Params: []Type{String, Number}, Result: Number}
	ParseFloat = &Builtin{Name: "parseFloat", Params: []Type{String}, Result: Number}

	// ThrowError throws an error of the class its first argument names, with the message
	// its second gives; until exceptions arrive (M3.2), it ends the program as uncaught.
	ThrowError = &Builtin{Name: "throw", Params: []Type{String, String}, Result: Void}
)

// ToString is JS's ToString of a primitive value, static or boxed: "true", "null", Number::toString, …
type ToString struct{ X Expr }

// CallBuiltin calls a runtime function.
type CallBuiltin struct {
	Builtin *Builtin
	Args    []Expr
}

// BinaryOp is an operator on two primitive values.
type BinaryOp int

const (
	Add BinaryOp = iota // number + number
	Sub
	Mul
	Div
	Rem // JS %: truncating remainder
	Pow // JS **
	Lt
	Le
	Gt
	Ge
	Eq // === on two values of the same primitive type
	Ne
	Concat // string + string
	// The bitwise operators: JS's int32 semantics on numbers; on an integer type, the
	// type's own bits, with the shift count taken modulo its width (D-018).
	BitAnd
	BitOr
	BitXor
	Shl
	Shr  // sign-propagating for numbers and signed types
	UShr // zero-filling
)

var binaryOpText = [...]string{Add: "+", Sub: "-", Mul: "*", Div: "/", Rem: "%", Pow: "**", Lt: "<", Le: "<=", Gt: ">", Ge: ">=", Eq: "===", Ne: "!==", Concat: "++",
	BitAnd: "&", BitOr: "|", BitXor: "^", Shl: "<<", Shr: ">>", UShr: ">>>"}

func (op BinaryOp) String() string { return binaryOpText[op] }

// IsComparison reports whether the operator yields a boolean.
func (op BinaryOp) IsComparison() bool { return op >= Lt && op <= Ne }

// Binary applies Op to X and Y, which have the same type: numeric for arithmetic,
// numeric or string for ordering, numeric, boolean or string for Eq and Ne, and
// string for Concat.
type Binary struct {
	Op   BinaryOp
	X, Y Expr
}

// UnaryOp is an operator on one primitive value.
type UnaryOp int

const (
	Neg    UnaryOp = iota // negation of a numeric value (wrapping for integers)
	Not                   // ! on a boolean
	BitNot                // ~ on a numeric value (int32 for numbers)
)

var unaryOpText = [...]string{Neg: "-", Not: "!", BitNot: "~"}

func (op UnaryOp) String() string { return unaryOpText[op] }

// Unary applies Op to X.
type Unary struct {
	Op UnaryOp
	X  Expr
}

func (*NumberConst) Type() Type     { return Number }
func (*BoolConst) Type() Type       { return Bool }
func (*StringConst) Type() Type     { return String }
func (*UndefinedConst) Type() Type  { return Undefined }
func (*Resumed) Type() Type         { return Value }
func (*ResumedBy) Type() Type       { return Bool }
func (*NullConst) Type() Type       { return Null }
func (*GivenConst) Type() Type      { return Given }
func (e *RegExpLiteral) Type() Type { return e.Class }
func (e *LeftOut) Type() Type       { return e.T }
func (e *Absent) Type() Type        { return e.T }
func (e *LocalRef) Type() Type      { return e.Local.Type }
func (e *Assign) Type() Type        { return e.Local.Type }
func (*Truthy) Type() Type          { return Bool }
func (e *Conditional) Type() Type   { return e.Then.Type() }
func (e *Logical) Type() Type       { return e.X.Type() }
func (e *Sequence) Type() Type      { return e.Y.Type() }
func (e *Bind) Type() Type          { return e.In.Type() }
func (e *New) Type() Type           { return e.Class }
func (e *NewObject) Type() Type     { return e.Shape }
func (e *FieldRef) Type() Type      { return e.Field.Type }
func (*HasField) Type() Type        { return Bool }
func (*IsAbsent) Type() Type        { return Bool }
func (*HasKey) Type() Type          { return Bool }
func (e *SetField) Type() Type      { return e.Field.Type }
func (*Box) Type() Type             { return Value }
func (e *RestArgs) Type() Type      { return e.X.Type().(*Rest).Array }
func (*SpreadArg) Type() Type       { return Value }
func (e *CallBuiltin) Type() Type   { return e.Builtin.Result }
func (e *Call) Type() Type          { return e.Func.Result }
func (e *VirtualCall) Type() Type   { return e.Method.Func.Result }
func (e *InstanceCall) Type() Type {
	if e.Dynamic {
		return Value
	}
	return e.Signature.Result
}
func (e *InterfaceCall) Type() Type { return e.Method.Result }
func (e *NewArray) Type() Type      { return e.Array }
func (e *MakeClosure) Type() Type   { return e.FuncType }
func (e *NewMap) Type() Type        { return e.Map }

func (e *MapCall) Type() Type {
	switch e.Op {
	case MapSize:
		return Number
	case MapGet:
		return Value
	case MapGetChecked, MapIteratorValue, MapPut:
		return e.Map.Value
	case MapSet, MapAssign:
		return e.Map
	case MapHas, MapDelete, MapNext, MapGetInto:
		return Bool
	case MapClear:
		return Void
	case MapIterate:
		return MapIterator
	}
	return e.Map.Key // MapIteratorKey
}
func (e *CallValue) Type() Type { return e.Callee.Type().(*FuncType).Result }

func (e *ArrayCall) Type() Type {
	switch e.Op {
	case ArrayLength, ArraySetLength, ArrayPush, ArrayPushAll, ArrayUnshift, ArrayUnshiftAll, ArrayIndexOf, ArrayLastIndexOf:
		return Number
	case ArrayElement, ArrayPop, ArrayShift, ArrayAt:
		return Value
	case ArrayKeys:
		return NumberArray
	case ArrayIncludes, ArrayHas:
		return Bool
	case ArrayJoin:
		return String
	case ArraySlice, ArraySplice, ArrayConcat, ArraySpread, ArrayReverse, ArraySort, ArrayFill, ArraySortWith, ArrayCopy, ArrayWith:
		return e.Args[0].Type()
	case ArrayFlat:
		return e.To
	case ArrayPushFlat:
		return Void
	case ArrayElementOrUndefined:
		if e.As != nil {
			return e.As
		}
	}
	return e.Args[0].Type().(*Array).Elem
}
func (e *ClassCast) Type() Type    { return e.To }
func (*InstanceOf) Type() Type     { return Bool }
func (*FamilyField) Type() Type    { return Value }
func (*FamilySetField) Type() Type { return Value }
func (*FamilyCall) Type() Type     { return Value }
func (e *Unary) Type() Type        { return e.X.Type() }
func (e *IntConst) Type() Type     { return e.Ty }
func (e *Convert) Type() Type      { return e.To }
func (e *ToUnion) Type() Type      { return e.To }
func (e *FromUnion) Type() Type    { return e.To }
func (e *IsNullish) Type() Type    { return Bool }
func (*ToString) Type() Type       { return String }

func (e *Binary) Type() Type {
	if e.Op.IsComparison() {
		return Bool
	}
	return e.X.Type()
}
