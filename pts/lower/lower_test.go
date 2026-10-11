package lower_test

import (
	"strings"
	"testing"

	"github.com/microsoft/typescript-go/internal/vfs/vfstest"
	"github.com/microsoft/typescript-go/pts/diagnostic"
	"github.com/microsoft/typescript-go/pts/frontend"
	"github.com/microsoft/typescript-go/pts/ir"
	"github.com/microsoft/typescript-go/pts/lower"
	"gotest.tools/v3/assert"
)

// lowerSource type-checks source as /p/main.pts, which must have no tsc errors, and lowers it.
func lowerSource(t *testing.T, source string) (*ir.Program, []diagnostic.Diagnostic) {
	t.Helper()
	fs := vfstest.FromMap(map[string]string{"/p/main.pts": source}, true /*useCaseSensitiveFileNames*/)
	program, errors := frontend.Load(t.Context(), fs, "/p/main.pts")
	assert.DeepEqual(t, errors, []diagnostic.Diagnostic(nil))
	return lower.Program(t.Context(), program)
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
	err := lowerInvalid(t, `console.log("a" + 1);`)
	assert.Equal(t, err.Code, diagnostic.NotSupportedYet)
	assert.Equal(t, err.Message, "the `+` operator on string and number is not supported yet")
}

func TestUnsupportedOperatorIsNamed(t *testing.T) {
	t.Parallel()
	err := lowerInvalid(t, "console.log(!true);")
	assert.Equal(t, err.Code, diagnostic.NotSupportedYet)
	assert.Equal(t, err.Message, "the prefix `!` operator is not supported yet")
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
