package lower_test

import (
	"strings"
	"testing"

	"github.com/microsoft/typescript-go/internal/vfs/vfstest"
	"github.com/microsoft/typescript-go/pts/check"
	"github.com/microsoft/typescript-go/pts/diagnostic"
	"github.com/microsoft/typescript-go/pts/frontend"
	"github.com/microsoft/typescript-go/pts/ir"
	"github.com/microsoft/typescript-go/pts/lower"
	"gotest.tools/v3/assert"
)

// lowerSource type-checks source as /p/main.pts, which must pass tsc and the semantic pass, and lowers it.
func lowerSource(t *testing.T, source string) (*ir.Program, []diagnostic.Diagnostic) {
	t.Helper()
	fs := vfstest.FromMap(map[string]string{"/p/main.pts": source}, true /*useCaseSensitiveFileNames*/)
	program, errors := frontend.Load(t.Context(), fs, "/p/main.pts")
	assert.DeepEqual(t, errors, []diagnostic.Diagnostic(nil))
	info, errors := check.Program(t.Context(), program)
	assert.DeepEqual(t, errors, []diagnostic.Diagnostic(nil))
	return lower.Program(t.Context(), program, info)
}

// lowerValid lowers source, which must lower without errors, and prints the IR.
func lowerValid(t *testing.T, source string) string {
	t.Helper()
	program, errors := lowerSource(t, source)
	assert.DeepEqual(t, errors, []diagnostic.Diagnostic(nil))
	return program.String()
}

// lowerInvalid lowers source, which must fail with exactly one error, and returns it.
func lowerInvalid(t *testing.T, source string) diagnostic.Diagnostic {
	t.Helper()
	_, errors := lowerSource(t, source)
	assert.Equal(t, len(errors), 1, "errors: %v", errors)
	return errors[0]
}

func TestHelloWorldLowersToOneConsoleLogCall(t *testing.T) {
	t.Parallel()
	assert.Equal(t, lowerValid(t, `console.log("Hello, ParallelScript");`), `func main() void {
  1: console.log(box("Hello, ParallelScript"))
}
`)
}

func TestPrimitiveLiteralsAreBoxedForConsoleLog(t *testing.T) {
	t.Parallel()
	got := lowerValid(t, "console.log(1, 0x10, 1_000, 2.5e-3, true, false, null, undefined, 'x', `t`, (\"p\"));")
	assert.Equal(t, got, `func main() void {
  1: console.log(box(1), box(16), box(1000), box(0.0025), box(true), box(false), box(null), box(undefined), box("x"), box("t"), box("p"))
}
`)
}

func TestConsoleLogWithoutArgumentsStaysEmpty(t *testing.T) {
	t.Parallel()
	assert.Equal(t, lowerValid(t, "console.log();"), "func main() void {\n  1: console.log()\n}\n")
}

func TestLocalsTakeTheirWidenedStaticType(t *testing.T) {
	t.Parallel()
	got := lowerValid(t, `const greeting = "hi";
let count = 3;
const flag: boolean = true;
const nothing = null;
console.log(greeting, count, flag, nothing);
`)
	assert.Equal(t, got, `func main() void {
  1: let greeting: string = "hi"
  2: let count: number = 3
  3: let flag: boolean = true
  4: let nothing: null = null
  5: console.log(box(greeting), box(count), box(flag), box(nothing))
}
`)
}

func TestShadowingLocalsAreDistinct(t *testing.T) {
	t.Parallel()
	program, errors := lowerSource(t, `const x = 1;
{
  const x = "inner";
  console.log(x);
}
console.log(x);
`)
	assert.DeepEqual(t, errors, []diagnostic.Diagnostic(nil))
	stmts := program.Main.Body.Stmts
	outer := stmts[0].(*ir.LocalDecl).Local
	inner := stmts[1].(*ir.Block).Stmts[0].(*ir.LocalDecl).Local
	assert.Assert(t, outer != inner)
	assert.Equal(t, loggedLocal(stmts[1].(*ir.Block).Stmts[1]), inner)
	assert.Equal(t, loggedLocal(stmts[2]), outer)
}

// loggedLocal returns the local passed to console.log(local) in stmt.
func loggedLocal(stmt ir.Stmt) *ir.Local {
	call := stmt.(*ir.ExprStmt).X.(*ir.CallBuiltin)
	return call.Args[0].(*ir.Box).X.(*ir.LocalRef).Local
}

func TestStatementsCarryTheirSourceLines(t *testing.T) {
	t.Parallel()
	program, errors := lowerSource(t, "\n\n  // comment\n  console.log(1);\n")
	assert.DeepEqual(t, errors, []diagnostic.Diagnostic(nil))
	assert.Equal(t, program.Main.Body.Stmts[0].Pos(), ir.Pos{File: "/p/main.pts", Line: 4})
	assert.Equal(t, program.Main.Pos, ir.Pos{File: "/p/main.pts", Line: 1})
}

func TestTypeOnlyDeclarationsProduceNoCode(t *testing.T) {
	t.Parallel()
	got := lowerValid(t, "interface Point { x: number }\ntype Label = string;\nexport {};\n;\nconsole.log(1);\n")
	assert.Equal(t, got, "func main() void {\n  5: console.log(box(1))\n}\n")
}

func TestUnsupportedStatementIsReportedAtItsPosition(t *testing.T) {
	t.Parallel()
	err := lowerInvalid(t, "console.log(1);\n  if (true) {}\n")
	assert.Equal(t, err.Format("/p"), "main.pts:2:3 - error PTS1001: if statement is not supported yet")
}

func TestArithmeticFollowsTypeScriptPrecedence(t *testing.T) {
	t.Parallel()
	got := lowerValid(t, "console.log(1 + 2 * 3 - 4 / 5 % 6 ** 2 ** 0.5);")
	assert.Equal(t, got, "func main() void {\n  1: console.log(box(((1 + (2 * 3)) - ((4 / 5) % (6 ** (2 ** 0.5))))))\n}\n")
}

func TestComparisonsYieldBooleans(t *testing.T) {
	t.Parallel()
	got := lowerValid(t, `const a = 1;
const small: boolean = a < 2;
console.log(a <= 2, a > 2, a >= 2, a === 1, a !== 1, a == 1, a != 1, small === true, small != false);
`)
	assert.Equal(t, got, `func main() void {
  1: let a: number = 1
  2: let small: boolean = (a < 2)
  3: console.log(box((a <= 2)), box((a > 2)), box((a >= 2)), box((a === 1)), box((a !== 1)), box((a === 1)), box((a !== 1)), box((small === true)), box((small !== false)))
}
`)
}

func TestUnaryMinusNegatesAndUnaryPlusIsDropped(t *testing.T) {
	t.Parallel()
	got := lowerValid(t, "console.log(-1, -(-2), +3, -0);")
	assert.Equal(t, got, "func main() void {\n  1: console.log(box((-1)), box((-(-2))), box(3), box((-0)))\n}\n")
}

func TestNaNAndInfinityAreConstants(t *testing.T) {
	t.Parallel()
	got := lowerValid(t, "console.log(NaN, Infinity, -Infinity);")
	assert.Equal(t, got, "func main() void {\n  1: console.log(box(NaN), box(+Inf), box((-+Inf)))\n}\n")
}

func TestALocalNamedNaNShadowsTheGlobal(t *testing.T) {
	t.Parallel()
	got := lowerValid(t, "const NaN = 1;\nconsole.log(NaN);\n")
	assert.Equal(t, got, "func main() void {\n  1: let NaN: number = 1\n  2: console.log(box(NaN))\n}\n")
}

func TestOperatorOnUnsupportedOperandsIsNamed(t *testing.T) {
	t.Parallel()
	err := lowerInvalid(t, `console.log(!1);`)
	assert.Equal(t, err.Code, diagnostic.NotSupportedYet)
	assert.Equal(t, err.Message, "the prefix `!` operator on number is not supported yet")
}

func TestUnsupportedOperatorIsNamed(t *testing.T) {
	t.Parallel()
	err := lowerInvalid(t, "console.log(~1);")
	assert.Equal(t, err.Code, diagnostic.NotSupportedYet)
	assert.Equal(t, err.Message, "the prefix `~` operator is not supported yet")
}

func TestUnsupportedTypeIsNamed(t *testing.T) {
	t.Parallel()
	err := lowerInvalid(t, "const xs: number[] = [];")
	assert.Equal(t, err.Code, diagnostic.NotSupportedYet)
	assert.Equal(t, err.Message, "values of type number[] are not supported yet")
}

func TestVarIsRejectedWithSuggestion(t *testing.T) {
	t.Parallel()
	err := lowerInvalid(t, "var count = 1;")
	assert.Equal(t, err.Code, diagnostic.Unsupported)
	assert.Assert(t, strings.Contains(err.Message, "use `let` or `const`"), err.Message)
}

func TestUsesOfAFailedDeclarationAreNotReportedAgain(t *testing.T) {
	t.Parallel()
	err := lowerInvalid(t, "const xs: number[] = [];\nconsole.log(xs);\n")
	assert.Equal(t, err.Line, 1)
}

func TestEveryUnsupportedConstructIsReported(t *testing.T) {
	t.Parallel()
	_, errors := lowerSource(t, "while (false) {}\nconsole.log(1 << 2);\n")
	assert.Equal(t, len(errors), 2)
	assert.Equal(t, errors[0].Line, 1)
	assert.Equal(t, errors[1].Line, 2)
}

func TestIntegerLocalsAndLiteralsTakeTheirDeclaredType(t *testing.T) {
	t.Parallel()
	got := lowerValid(t, `const small: u8 = 200;
const big: int = -9223372036854775808;
const huge: u64 = 18446744073709551615;
const ratio: f32 = 0.5;
const neg: i8 = -5;
console.log(small, big, huge, ratio, neg);
`)
	assert.Equal(t, got, `func main() void {
  1: let small: u8 = 200:u8
  2: let big: i64 = -9223372036854775808:i64
  3: let huge: u64 = 18446744073709551615:u64
  4: let ratio: f32 = f32(0.5)
  5: let neg: i8 = -5:i8
  6: console.log(box(small), box(big), box(huge), box(ratio), box(neg))
}
`)
}

func TestOperandsAreConvertedToTheirCommonType(t *testing.T) {
	t.Parallel()
	got := lowerValid(t, `const a: i32 = 1;
const b: u8 = 2;
const sum = a + b;
const scaled = a * 1.5;
const next = a + 1;
const wide: i64 = b;
console.log(sum < 3, a === 1);
`)
	assert.Equal(t, got, `func main() void {
  1: let a: i32 = 1:i32
  2: let b: u8 = 2:u8
  3: let sum: i32 = (a + i32(b))
  4: let scaled: number = (number(a) * 1.5)
  5: let next: i32 = (a + 1:i32)
  6: let wide: i64 = i64(b)
  7: console.log(box((sum < 3:i32)), box((a === 1:i32)))
}
`)
}

func TestAsConvertsBetweenNumericTypes(t *testing.T) {
	t.Parallel()
	got := lowerValid(t, "const n = 3.9;\nconst i = n as int;\nconst b = i as u8;\nconst back = b as number;\nconsole.log(-i);\n")
	assert.Equal(t, got, `func main() void {
  1: let n: number = 3.9
  2: let i: i64 = i64(n)
  3: let b: u8 = u8(i)
  4: let back: number = number(b)
  5: console.log(box((-i)))
}
`)
}

func TestConcatenationConvertsTheOtherSideToAString(t *testing.T) {
	t.Parallel()
	got := lowerValid(t, `const n: int = 3;
console.log("a" + 1, 2 + "b", "c" + true + null + undefined, "n=" + n, "x" + "y");
`)
	assert.Equal(t, got, `func main() void {
  1: let n: i64 = 3:i64
  2: console.log(box(("a" ++ str(1))), box((str(2) ++ "b")), box(((("c" ++ str(true)) ++ str(null)) ++ str(undefined))), box(("n=" ++ str(n))), box(("x" ++ "y")))
}
`)
}

func TestTemplateLiteralsConcatenateTheirParts(t *testing.T) {
	t.Parallel()
	got := lowerValid(t, "const who = \"world\";\nconsole.log(`hello ${who}!`, `${1}${2}`, `${who}`);\n")
	assert.Equal(t, got, `func main() void {
  1: let who: string = "world"
  2: console.log(box((("hello " ++ who) ++ "!")), box((str(1) ++ str(2))), box(who))
}
`)
}

func TestStringLengthComparisonAndNot(t *testing.T) {
	t.Parallel()
	got := lowerValid(t, "let s = \"abc\";\nconsole.log(s.length, s === \"abc\", s < \"abd\", !(s !== \"x\"));\n")
	assert.Equal(t, got, `func main() void {
  1: let s: string = "abc"
  2: console.log(box(String.length(s)), box((s === "abc")), box((s < "abd")), box((!(s !== "x"))))
}
`)
}
