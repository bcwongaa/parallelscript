// Package ir is ParallelScript's typed intermediate representation (D-L04): structured
// rather than SSA, fully typed, with boxing and every runtime operation explicit.
// pts/lower produces it from the checked TypeScript AST and pts/cgen turns it into C.
package ir

// Program is a whole lowered program.
type Program struct {
	Funcs []*Func
	Main  *Func // runs the entry module's top-level code; also in Funcs
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
)

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

// Func is a function with a body. A nested function's Name is qualified: "outer.inner".
type Func struct {
	Name   string
	Pos    Pos
	Params []*Local
	Result Type // Void if it returns nothing
	Body   *Block
}

// Local is a local variable. Each declaration gets its own Local, so two locals may share a Name.
type Local struct {
	Name string
	Type Type
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

func (s *Block) Pos() Pos        { return s.At }
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

// Call calls a function of the program with arguments of its parameter types.
type Call struct {
	Func *Func
	Args []Expr
}

// Box turns a statically typed value into a dynamic Value.
type Box struct{ X Expr }

// Builtin is a runtime function with a fixed signature.
type Builtin struct {
	Name   string // as written in TypeScript
	Params []Type // the receiver first for a member; nil for console.log
	Result Type
}

// stringMember is a member of String.prototype; it takes the string, then params.
func stringMember(name string, result Type, params ...Type) *Builtin {
	return &Builtin{Name: "String." + name, Params: append([]Type{String}, params...), Result: result}
}

var (
	// ConsoleLog is console.log; it takes any number of Value arguments.
	ConsoleLog = &Builtin{Name: "console.log", Result: Void}
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

	// ValueTypeOf is typeof of a boxed value; typeof of a static type is a constant.
	ValueTypeOf = &Builtin{Name: "typeof", Params: []Type{Value}, Result: String}
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

func (*NumberConst) Type() Type    { return Number }
func (*BoolConst) Type() Type      { return Bool }
func (*StringConst) Type() Type    { return String }
func (*UndefinedConst) Type() Type { return Undefined }
func (*NullConst) Type() Type      { return Null }
func (e *LocalRef) Type() Type     { return e.Local.Type }
func (e *Assign) Type() Type       { return e.Local.Type }
func (*Truthy) Type() Type         { return Bool }
func (e *Conditional) Type() Type  { return e.Then.Type() }
func (e *Logical) Type() Type      { return e.X.Type() }
func (e *Sequence) Type() Type     { return e.Y.Type() }
func (*Box) Type() Type            { return Value }
func (e *CallBuiltin) Type() Type  { return e.Builtin.Result }
func (e *Call) Type() Type         { return e.Func.Result }
func (e *Unary) Type() Type        { return e.X.Type() }
func (e *IntConst) Type() Type     { return e.Ty }
func (e *Convert) Type() Type      { return e.To }
func (*ToString) Type() Type       { return String }

func (e *Binary) Type() Type {
	if e.Op.IsComparison() {
		return Bool
	}
	return e.X.Type()
}
