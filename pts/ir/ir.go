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
)

// Func is a function with a body.
type Func struct {
	Name   string
	Pos    Pos
	Result Type
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

// LocalDecl declares Local and initialises it with Init, whose type is Local.Type.
type LocalDecl struct {
	At    Pos
	Local *Local
	Init  Expr
}

func (s *Block) Pos() Pos     { return s.At }
func (s *ExprStmt) Pos() Pos  { return s.At }
func (s *LocalDecl) Pos() Pos { return s.At }

// Expr is an expression.
type Expr interface {
	Type() Type
}

type NumberConst struct{ Value float64 }

type BoolConst struct{ Value bool }

// StringConst is a JS string literal. Value is UTF-8, except that a lone surrogate is
// the 3-byte sequence the TypeScript scanner produces for it (stringutil.EncodeJSStringRune).
type StringConst struct{ Value string }

type UndefinedConst struct{}

type NullConst struct{}

// LocalRef reads a local.
type LocalRef struct{ Local *Local }

// Box turns a statically typed value into a dynamic Value.
type Box struct{ X Expr }

// Builtin is a runtime function with a fixed signature.
type Builtin struct {
	Name   string // as written in TypeScript
	Result Type
}

// ConsoleLog is console.log; it takes any number of Value arguments.
var ConsoleLog = &Builtin{Name: "console.log", Result: Void}

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
)

var binaryOpText = [...]string{Add: "+", Sub: "-", Mul: "*", Div: "/", Rem: "%", Pow: "**", Lt: "<", Le: "<=", Gt: ">", Ge: ">=", Eq: "===", Ne: "!=="}

func (op BinaryOp) String() string { return binaryOpText[op] }

// IsComparison reports whether the operator yields a boolean.
func (op BinaryOp) IsComparison() bool { return op >= Lt }

// Binary applies Op to X and Y. Arithmetic and ordering take numbers; Eq and Ne take
// two values of the same type.
type Binary struct {
	Op   BinaryOp
	X, Y Expr
}

// UnaryOp is an operator on one primitive value.
type UnaryOp int

const (
	Neg UnaryOp = iota // -number
)

var unaryOpText = [...]string{Neg: "-"}

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
func (*Box) Type() Type            { return Value }
func (e *CallBuiltin) Type() Type  { return e.Builtin.Result }
func (*Unary) Type() Type          { return Number }

func (e *Binary) Type() Type {
	if e.Op.IsComparison() {
		return Bool
	}
	return Number
}
