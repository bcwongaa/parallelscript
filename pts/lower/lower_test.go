package lower_test

import (
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
	return lowerFiles(t, map[string]string{"/p/main.pts": source})
}

// lowerFiles is lowerSource for a program of several modules; /p/main.pts is the entry.
func lowerFiles(t *testing.T, files map[string]string) (*ir.Program, []diagnostic.Diagnostic) {
	t.Helper()
	fs := vfstest.FromMap(files, true /*useCaseSensitiveFileNames*/)
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
	err := lowerInvalid(t, "console.log(1);\n  debugger;\n")
	assert.Equal(t, err.Format("/p"), "main.pts:2:3 - error PTS1001: debugger statement is not supported yet")
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
	err := lowerInvalid(t, "function f(): void {}\nconsole.log(+f());\n")
	assert.Equal(t, err.Code, diagnostic.NotSupportedYet)
	assert.Equal(t, err.Message, "the prefix `+` operator on void is not supported yet")
}

func TestUnsupportedTypeIsNamed(t *testing.T) {
	t.Parallel()
	err := lowerInvalid(t, `const weak: WeakMap<object, number> = new WeakMap();`)
	assert.Equal(t, err.Code, diagnostic.NotSupportedYet)
	assert.Equal(t, err.Message, "values of type WeakMap<object, number> are not supported yet")
}

func TestUsesOfAFailedDeclarationAreNotReportedAgain(t *testing.T) {
	t.Parallel()
	err := lowerInvalid(t, "const weak: WeakMap<object, number> = new WeakMap();\nconsole.log(weak);\n")
	assert.Equal(t, err.Line, 1)
}

func TestEveryUnsupportedConstructIsReported(t *testing.T) {
	t.Parallel()
	_, errors := lowerSource(t, "const weak = new WeakMap<object, number>();\nfunction f(): void {}\nconsole.log(+f());\n")
	assert.Equal(t, len(errors), 2)
	assert.Equal(t, errors[0].Line, 1)
	assert.Equal(t, errors[1].Line, 3)
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

func TestLeftOutMethodArgumentsGetWhatUndefinedMeans(t *testing.T) {
	t.Parallel()
	got := lowerValid(t, `const s = "abc";
console.log(s.slice(), s.slice(1), s.slice(undefined, 2), s.indexOf("b"), s.endsWith("c"), s.padStart(5));
`)
	assert.Equal(t, got, `func main() void {
  1: let s: string = "abc"
  2: console.log(box(String.slice(s, NaN, +Inf)), box(String.slice(s, 1, +Inf)), box(String.slice(s, NaN, 2)), box(String.indexOf(s, "b", NaN)), box(String.endsWith(s, "c", +Inf)), box(String.padStart(s, 5, " ")))
}
`)
}

func TestMethodArgumentsConvertToTheirParameterTypes(t *testing.T) {
	t.Parallel()
	got := lowerValid(t, `const i: int = 1;
console.log("abc".charCodeAt(i), "abc".repeat(i + 1), "abc".trimLeft(), "abc".trimRight().toUpperCase());
`)
	assert.Equal(t, got, `func main() void {
  1: let i: i64 = 1:i64
  2: console.log(box(String.charCodeAt("abc", number(i))), box(String.repeat("abc", number((i + 1:i64)))), box(String.trimStart("abc")), box(String.toUpperCase(String.trimEnd("abc"))))
}
`)
}

func TestStringElementKeepsUndefinedWhereAnyValueFits(t *testing.T) {
	t.Parallel()
	got := lowerValid(t, "const s = \"abc\";\nconsole.log(s[0], (s[1]), `${s[2]}`, s[3] + \"!\", s[0] === \"a\", \"b\" !== s[1]);\nconst c = s[0];\n")
	assert.Equal(t, got, `func main() void {
  1: let s: string = "abc"
  2: console.log(String.element(s, 0), String.element(s, 1), box(str(String.element(s, 2))), box((str(String.element(s, 3)) ++ "!")), box(String.elementEquals(s, 0, "a")), box((!String.elementEquals(s, 1, "b"))))
  3: let c: string = String.elementChecked(s, 0)
}
`)
}

func TestOnlyDirectCallsOfImplementedMethodsLower(t *testing.T) {
	t.Parallel()
	err := lowerInvalid(t, "console.log(\"abc\".anchor(\"x\"));\n")
	assert.Equal(t, err.Message, "calling `\"abc\".anchor` is not supported yet")
}

func TestJumpsNameTheLoopTheyLeave(t *testing.T) {
	t.Parallel()
	got := lowerValid(t, "outer: for (let i = 0; i < 3; i++) {\n  while (true) {\n    if (i === 1) continue outer;\n    break;\n  }\n}\n")
	assert.Equal(t, got, `func main() void {
  1: {
    1: let i: number = 0
    1: outer: for (i < 3); old(i = (i + 1)) {
      2: #2: while true {
        3: if (i === 1) {
          3: continue outer
        }
        4: break #2
      }
    }
  }
}
`)
}

func TestSwitchStoresItsValueAndComparesEachCase(t *testing.T) {
	t.Parallel()
	got := lowerValid(t, "const day: u8 = 2;\nswitch (day) {\n  case 1:\n  case 2:\n    console.log(\"low\");\n    break;\n  default:\n    console.log(\"other\");\n}\n")
	assert.Equal(t, got, `func main() void {
  1: let day: u8 = 2:u8
  2: {
    2: let tag: u8 = day
    2: #1: switch {
      3: case (tag === 1:u8):
      4: case (tag === 2:u8):
        5: console.log(box("low"))
        6: break #1
      7: default:
        8: console.log(box("other"))
    }
  }
}
`)
}

func TestCompoundAssignmentAndIncrementStoreInTheLocal(t *testing.T) {
	t.Parallel()
	got := lowerValid(t, "let n: u8 = 1;\nn += 2;\nn++;\nconst m = n--;\nlet s = \"a\";\ns += n;\n")
	assert.Equal(t, got, `func main() void {
  1: let n: u8 = 1:u8
  2: (n = (n + 2:u8))
  3: old(n = (n + 1:u8))
  4: let m: u8 = old(n = (n - 1:u8))
  5: let s: string = "a"
  6: (s = (s ++ str(n)))
}
`)
}

func TestConditionsTestAnyPrimitiveForTruth(t *testing.T) {
	t.Parallel()
	got := lowerValid(t, "const s = \"abc\";\nconst n = 0;\nif (s) console.log(!n, s[9] ? 1 : 2, n || 3, typeof s[9], void n);\n")
	assert.Equal(t, got, `func main() void {
  1: let s: string = "abc"
  2: let n: number = 0
  3: if truthy(s) {
    3: console.log(box((!truthy(n))), box((truthy(String.element(s, 9)) ? 1 : 2)), box((n || 3)), box(typeof(String.element(s, 9))), box(undefined))
  }
}
`)
}

func TestAVariableWithoutInitializerStartsAtItsZeroValue(t *testing.T) {
	t.Parallel()
	got := lowerValid(t, "done: {\n  let late: string;\n  late = \"x\";\n  break done;\n}\n")
	assert.Equal(t, got, `func main() void {
  1: done: {
    2: let late: string
    3: (late = "x")
    4: break done
  }
}
`)
}

func TestFunctionsAreDeclaredBeforeTheirBodiesAreLowered(t *testing.T) {
	t.Parallel()
	got := lowerValid(t, `console.log(twice(2));
function twice(n: u8): int {
  function inner(m: int): int { return m * 2; }
  return inner(n);
}
function shout(s: string): void {
  console.log(s.toUpperCase());
  return;
}
shout("hi");
`)
	assert.Equal(t, got, `func main() void {
  1: console.log(box(twice(2:u8)))
  10: shout("hi")
}
func twice(n: u8) i64 {
  3: let inner: (i64) => i64 = closure twice.inner
  4: return twice.inner[inner](i64(n))
}
func shout(s: string) void {
  7: console.log(box(String.toUpperCase(s)))
  8: return
}
func twice.inner(m: i64) i64 closure [] {
  3: return (m * 2:i64)
}
`)
}

func TestModuleScopeBindingsThatFunctionsUseBecomeGlobals(t *testing.T) {
	t.Parallel()
	got := lowerValid(t, "const limit = 3;\nconst local = 1;\nfunction below(n: number) { return n < limit; }\nconsole.log(below(local));\n")
	assert.Equal(t, got, `func main() void {
  1: global limit: number = 3
  2: let local: number = 1
  4: console.log(box(below(local)))
}
func below(n: number) boolean {
  3: return (n < limit)
}
`)
}

func TestImportedBindingsAreTheExportingModulesOwn(t *testing.T) {
	t.Parallel()
	program, errors := lowerFiles(t, map[string]string{
		"/p/main.pts": `import total, { add, limit } from "./lib.pts";
import * as lib from "./lib.pts";
import twice from "./twice.pts";
console.log(add(limit, 1), lib.limit, total, lib.add(1, 2), twice(4));
`,
		"/p/lib.pts": `export const limit = 3;
export function add(a: number, b: number) { return a + b; }
export default limit * 2;
`,
		"/p/twice.pts": `function twice(n: number) { return n * 2; }
export default twice;
`,
	})
	assert.DeepEqual(t, errors, []diagnostic.Diagnostic(nil))
	// The default export of a value is a binding of its own; of a function, the function.
	assert.Equal(t, program.String(), `func lib:main() void {
  1: global limit: number = 3
  3: global default: number = (limit * 2)
}
func twice:main() void {
}
func main() void {
  4: console.log(box(lib:add(limit, 1)), box(limit), box(default), box(lib:add(1, 2)), box(twice:twice(4)))
}
func lib:add(a: number, b: number) number {
  2: return (a + b)
}
func twice:twice(n: number) number {
  1: return (n * 2)
}
`)
}

func TestClassesLowerToFieldsAConstructorAndMethods(t *testing.T) {
	t.Parallel()
	got := lowerValid(t, `class Point {
  x: number;
  label = "p";
  constructor(x: number) { this.x = x; }
  twice(): number { return this.x * 2; }
}
function make(): Point { return new Point(3); }
const p = new Point(1);
p.x += p.twice();
make().x++;
console.log(p.label);
`)
	// Initialisers start the constructor; an instance with effects is evaluated once.
	assert.Equal(t, got, `class Point { x: number; label: string }
func main() void {
  8: let p: Point = new Point(1)
  9: (p.x = (p.x + Point.twice(p)))
  10: (let object = make() in old(object.x = (object.x + 1)))
  11: console.log(box(p.label))
}
func Point.constructor(this: Point, x: number) void {
  3: (this.label = "p")
  4: (this.x = x)
}
func Point.twice(this: Point) number {
  5: return (this.x * 2)
}
func make() Point {
  7: return new Point(3)
}
`)
}

func TestOverriddenMethodsAreCalledThroughTheMethodTable(t *testing.T) {
	t.Parallel()
	got := lowerValid(t, `abstract class Shape {
  abstract area(): number;
  twice(): number { return this.area() * 2; }
}
class Square extends Shape {
  side = 1;
  area(): number { return this.side * this.side; }
}
class Cube extends Square {
  area(): number { return super.area() * 6; }
}
class Tile extends Square {}
function measure(shape: Shape, tile: Tile, cube: Cube): number {
  if (shape instanceof Square) {
    shape.side += 1;
  }
  return shape.area() + tile.area() + cube.area() + tile.twice() + tile.side;
}
`)
	// Only calls whose receiver could be of a class that overrides go through the table;
	// receivers convert to the class that has the method (or its slot).
	assert.Equal(t, got, `abstract class Shape { virtual area }
class Square extends Shape { side: number; virtual area }
class Cube extends Square { virtual area }
class Tile extends Square {  }
func main() void {
}
func Shape.constructor(this: Shape) void {
}
func Shape.twice(this: Shape) number {
  3: return (virtual Shape.area(this) * 2)
}
func Square.constructor(this: Square) void {
  5: Shape.constructor((this as Shape))
  6: (this.side = 1)
}
func Square.area(this: Square) number {
  7: return (this.side * this.side)
}
func Cube.constructor(this: Cube) void {
  9: Square.constructor((this as Square))
}
func Cube.area(this: Cube) number {
  10: return (Square.area((this as Square)) * 6)
}
func Tile.constructor(this: Tile) void {
  12: Square.constructor((this as Square))
}
func measure(shape: Shape, tile: Tile, cube: Cube) number {
  14: if (shape instanceof Square) {
    15: (let object = (shape as Square) in (object.side = (object.side + 1)))
  }
  17: return ((((virtual Shape.area(shape) + Square.area((tile as Square))) + Cube.area(cube)) + Shape.twice((tile as Shape))) + tile.side)
}
`)
}

func TestAccessorsAreCallsAndFieldsImplementAbstractFields(t *testing.T) {
	t.Parallel()
	got := lowerValid(t, `abstract class Named {
  abstract name: string;
  constructor(public id: number) {}
}
class Person extends Named {
  name = "ann";
  get upper(): string { return this.name; }
  set upper(value: string) { this.name = value; }
}
function rename(named: Named, person: Person): string {
  named.name = "bo";
  person.upper += "!";
  return named.name + person.upper;
}
`)
	// A field that implements an abstract field gets a getter and setter; reads and writes
	// of the abstract field go through them, and compound assignments read once.
	assert.Equal(t, got, `abstract class Named { id: number; virtual get name; virtual set name }
class Person extends Named { name: string; virtual get name; virtual set name; get upper; set upper }
func main() void {
}
func Person.get_name(this: Person) string {
  6: return this.name
}
func Person.set_name(this: Person, value: string) void {
  6: (this.name = value)
}
func Named.constructor(this: Named, id: number) void {
  3: (this.id = id)
}
func Person.constructor(this: Person, id: number) void {
  5: Named.constructor((this as Named), id)
  6: (this.name = "ann")
}
func Person.get_upper(this: Person) string {
  7: return this.name
}
func Person.set_upper(this: Person, value: string) void {
  8: (this.name = value)
}
func rename(named: Named, person: Person) string {
  11: (let value = "bo" in (virtual Named.set_name(named, value), value))
  12: (let current = Person.get_upper(person) in (let value = (current ++ "!") in (Person.set_upper(person, value), value)))
  13: return (virtual Named.get_name(named) ++ Person.get_upper(person))
}
`)
}

func TestStaticFieldsAreGlobalsInitialisedWhereTheClassIs(t *testing.T) {
	t.Parallel()
	got := lowerValid(t, `console.log("first");
class Ids {
  static next = 1;
  static { Ids.next *= 10; }
  #secret = 0;
  static take(): number { return this.next++; }
  peek(): number { return this.#secret; }
}
console.log(Ids.take());
`)
	assert.Equal(t, got, `class Ids { #secret: number }
func main() void {
  1: console.log(box("first"))
  3: global Ids.next: number = 1
  4: {
    4: (Ids.next = (Ids.next * 10))
  }
  9: console.log(box(Ids.take()))
}
func Ids.constructor(this: Ids) void {
  5: (this.#secret = 0)
}
func Ids.take() number {
  6: return old(Ids.next = (Ids.next + 1))
}
func Ids.peek(this: Ids) number {
  7: return this.#secret
}
`)
}

func TestObjectTypesWithTheSamePropertiesShareAShape(t *testing.T) {
	t.Parallel()
	got := lowerValid(t, `interface Point { x: number; y: number }
function swap(p: { y: number; x: number }): Point {
  return { y: p.x, x: p.y };
}
const q = swap({ x: 1, y: 2 });
q.x += 1;
console.log({ label: "q", at: q });
`)
	// The three spellings of {x, y} are one interface, and literals of it one shape, each
	// literal keeping its own order; a literal converts to the interface it is used as.
	// `q.x += 1` reads q.x once, since a getter may provide it.
	assert.Equal(t, got, `shape object1{x: number; y: number}
shape object2{at: {x: number; y: number}; label: string}
func main() void {
  5: let q: {x: number; y: number} = swap((object1{x: 1, y: 2} as {x: number; y: number}))
  6: (let current = q.x in (q.x = (current + 1)))
  7: console.log(box(object2{label: "q", at: q}))
}
func swap(p: {x: number; y: number}) {x: number; y: number} {
  3: return (object1{y: p.x, x: p.y} as {x: number; y: number})
}
`)
}

func TestLeftOutOptionalPropertiesAreAbsent(t *testing.T) {
	t.Parallel()
	got := lowerValid(t, `interface Options { name: string; level?: number }
const o: Options = { name: "a" };
console.log(o.level, "level" in o, { ...o });
`)
	// The literal stores the absent value in the property it leaves out; a read sees
	// undefined, `in` tests for absent, and a spread copies the field as it is (D-065).
	assert.Equal(t, got, `shape object1{level?: number | undefined; name: string}
func main() void {
  2: let o: {level?: number | undefined; name: string} = (object1{name: "a", level: absent} as {level?: number | undefined; name: string})
  3: console.log(box(o.level), box(has(o.level)), box(object1{name: o.name, level: o.raw level}))
}
`)
}

func TestMethodsAndAccessorsProvideInterfaces(t *testing.T) {
	t.Parallel()
	got := lowerValid(t, `interface Shape { name: string; area(): number; scale(by: number): void }
class Square {
  constructor(public side: number) {}
  get name(): string { return "square"; }
  area(): number { return this.side * this.side; }
  scale(by: number): void { this.side *= by; }
}
function grow(s: Shape): string {
  s.scale(2);
  return s.name + s.area();
}
console.log(grow(new Square(2)));
`)
	// A getter provides name and methods provide area and scale; calls through the
	// interface go to whatever the object's class has.
	assert.Equal(t, got, `class Square { side: number; get name }
func main() void {
  12: console.log(box(grow((new Square(2) as {name: string; area(): number; scale(number): void}))))
}
func Square.constructor(this: Square, side: number) void {
  3: (this.side = side)
}
func Square.get_name(this: Square) string {
  4: return "square"
}
func Square.area(this: Square) number {
  5: return (this.side * this.side)
}
func Square.scale(this: Square, by: number) void {
  6: (this.side = (this.side * by))
}
func grow(s: {name: string; area(): number; scale(number): void}) string {
  9: interface scale(s, 2)
  10: return (s.name ++ str(interface area(s)))
}
`)
}

func TestArrayElementsAndForOfLowerToArrayOperations(t *testing.T) {
	t.Parallel()
	got := lowerValid(t, `function grid(): number[][] { return [[1, 2], [3]]; }
grid()[0][1] += 5;
for (const row of grid()) {
  console.log(row[9], row.pop()!, row.length);
}
`)
	// A compound assignment evaluates the array and index once; for...of reads the length
	// before each iteration; a[i] is unchecked only where any value can go (D-015).
	assert.Equal(t, got, `func main() void {
  2: (let object = array.elementChecked(grid(), 0) in (let current = array.elementChecked(object, 1) in array.setElement(object, 1, (current + 5))))
  3: {
    3: let array: number[][] = grid()
    3: let index: number = 0
    3: #1: for (index < array.length(array)); (index = (index + 1)) {
      3: let row: number[] = array.elementChecked(array, index)
      4: console.log(array.element(row, 9), box(array.popChecked(row)), box(array.length(row)))
    }
  }
}
func grid() number[][] {
  1: return number[][][number[][1, 2], number[][3]]
}
`)
}

func TestOnlyCapturedVariablesThatMayChangeLiveInCells(t *testing.T) {
	t.Parallel()
	got := lowerValid(t, `function make(a: number, b: number): () => number {
  const fixed = 1;
  let changed = 2;
  changed = 3;
  b++;
  const read = () => fixed + changed + a + b + early;
  const early = 4;
  return read;
}
const loops: (() => number)[] = [];
for (let i = 0; i < 2; i++) {
  loops.push(() => i);
}
`)
	assert.Equal(t, got, `func main() void {
  10: let loops: (() => number)[] = (() => number)[][]
  11: {
    11: let cell i: number = 0
    11: #1: for (i < 2); old(i = (i + 1)) copying cells [i] {
      12: array.push(loops, closure arrow)
    }
  }
}
func arrow() number closure [cell i] {
  12: return i
}
func make.read() number closure [fixed, cell changed, a, cell b, cell early] {
  6: return ((((fixed + changed) + a) + b) + early)
}
func make(a: number, b: number) () => number {
  2: let fixed: number = 1
  3: let cell changed: number = 2
  4: (changed = 3)
  5: old(b = (b + 1))
  6: let read: () => number = closure make.read
  7: let cell early: number = 4
  8: return read
}
`)
}

func TestArrowFunctionsCaptureTheThisOfTheirMethod(t *testing.T) {
	t.Parallel()
	got := lowerValid(t, `class Box {
  value = 1;
  getter(): () => () => number {
    return () => () => this.value;
  }
}
`)
	assert.Equal(t, got, `class Box { value: number }
func main() void {
}
func Box.constructor(this: Box) void {
  2: (this.value = 1)
}
func Box.getter.arrow.arrow() number closure [this] {
  4: return this.value
}
func Box.getter.arrow() () => number closure [this] {
  4: return closure Box.getter.arrow.arrow
}
func Box.getter(this: Box) () => () => number {
  4: return closure Box.getter.arrow
}
`)
}

func TestFunctionValuesWithFewerParametersOrAResultAreAdapted(t *testing.T) {
	t.Parallel()
	got := lowerValid(t, `function call(f: (a: string, b: number) => void): void {}
const one = (a: string) => a.length;
call(one);
call((a) => a.length);
`)
	assert.Equal(t, got, `func main() void {
  2: let one: (string) => number = closure one
  3: call((let adapted = one in closure adapter))
  4: call(closure arrow)
}
func one(a: string) number closure [] {
  2: return String.length(a)
}
func adapter(arg1: string, arg2: number) void closure [adapted] {
  3: call adapted(arg1)
}
func arrow(a: string, unused: number) void closure [] {
  4: String.length(a)
}
func call(f: (string, number) => void) void {
}
`)
}

func TestCallbackMethodsCallOneHelperPerArrayAndCallbackType(t *testing.T) {
	t.Parallel()
	got := lowerValid(t, `const odd = [1, 2, 3].filter((n) => n % 2 === 1);
const big = odd.filter((n) => n > 2);
`)
	assert.Equal(t, got, `func main() void {
  1: let odd: number[] = Array.filter(number[][1, 2, 3], closure arrow)
  2: let big: number[] = Array.filter(odd, closure arrow)
}
func arrow(n: number) boolean closure [] {
  1: return ((n % 2) === 1)
}
func Array.filter(array: number[], callback: (number) => boolean) number[] {
  1: let length: number = array.length(array)
  1: let result: number[] = number[][]
  1: let index: number = 0
  1: #1: for (index < length); (index = (index + 1)) {
    1: if (index < array.length(array)) {
      1: let element: number = array.elementChecked(array, index)
      1: if call callback(element) {
        1: array.push(result, element)
      }
    }
  }
  1: return result
}
func arrow(n: number) boolean closure [] {
  2: return (n > 2)
}
`)
}

func TestObjectLiteralMethodsAreClosuresThatTakeTheObject(t *testing.T) {
	t.Parallel()
	got := lowerValid(t, `function make(step: number) {
  return {
    count: 0,
    inc(): number {
      this.count += step;
      return this.count;
    },
  };
}
const counter = make(2);
const inc = counter.inc;
`)
	assert.Equal(t, got, `shape object1{count: number; inc: (object1) => number}
func main() void {
  10: let counter: {count: number; inc(): number} = make(2)
  11: let inc: () => number = (let receiver = counter in closure inc)
}
func inc() number closure [receiver] {
  11: return interface inc(receiver)
}
func make.inc(this: object1) number closure [step] {
  5: (this.count = (this.count + step))
  6: return this.count
}
func make(step: number) {count: number; inc(): number} {
  2: return (object1{count: 0, inc: closure make.inc} as {count: number; inc(): number})
}
`)
}

func TestForOfOverASetIteratesItsLiveTable(t *testing.T) {
	t.Parallel()
	got := lowerValid(t, `const seen = new Set<string>();
seen.add("a");
for (const item of seen) {
  console.log(item);
}
`)
	assert.Equal(t, got, `func main() void {
  1: let seen: Set<string> = new Set<string>()
  2: map.set(seen, "a")
  3: {
    3: let map: Set<string> = seen
    3: let iteration: map iterator = map.iterate(map)
    3: #1: while map.next(iteration) {
      3: let item: string = map.key(iteration)
      4: console.log(box(item))
    }
  }
}
`)
}

func TestGenericFunctionsHaveAnInstancePerListOfTypeArguments(t *testing.T) {
	t.Parallel()
	got := lowerValid(t, `function wrap<T>(x: T): T[] {
  function inner(y: T): T { return y; }
  return [inner(x)];
}
function twice<T>(x: T): T[] {
  return wrap(x).concat(wrap(x));
}
const byte: u8 = 1;
console.log(twice(byte), twice("a"), wrap<u8>(2));
`)
	assert.Equal(t, got, `func main() void {
  8: let byte: u8 = 1:u8
  9: console.log(box(twice_u8(byte)), box(twice_string("a")), box(wrap_u8(2:u8)))
}
func twice_u8(x: u8) u8[] {
  6: return array.concat(wrap_u8(x), wrap_u8(x))
}
func twice_string(x: string) string[] {
  6: return array.concat(wrap_string(x), wrap_string(x))
}
func wrap_u8(x: u8) u8[] {
  2: let inner: (u8) => u8 = closure wrap_u8.inner
  3: return u8[][wrap_u8.inner[inner](x)]
}
func wrap_u8.inner(y: u8) u8 closure [] {
  2: return y
}
func wrap_string(x: string) string[] {
  2: let inner: (string) => string = closure wrap_string.inner
  3: return string[][wrap_string.inner[inner](x)]
}
func wrap_string.inner(y: string) string closure [] {
  2: return y
}
`)
}

func TestParametersWithDefaultsTakeAFlagThatSaysWhetherTheCallPassedThem(t *testing.T) {
	t.Parallel()
	got := lowerValid(t, `function greet(name: string, greeting = "Hello"): string {
  return greeting + name;
}
greet("a");
greet("a", undefined);
greet("a", "b");
const shaped: (s: string, g: string) => string = (s, g = "x") => g + s;
const value: (s: string, g?: string) => string = greet;
value("c");
`)
	assert.Equal(t, got, `func main() void {
  4: greet("a", left-out string, given(false))
  5: greet("a", left-out string, given(false))
  6: greet("a", "b", given(true))
  7: let shaped: (string, string) => string = closure shaped
  8: let value: (string, string, given) => string = closure greet
  9: call value("c", left-out string, given(false))
}
func shaped(s: string, g: string) string closure [] {
  7: return (g ++ s)
}
func greet(name: string, greeting_arg: string, greeting_given: given) string {
  1: let greeting: string = (greeting_given ? greeting_arg : "Hello")
  2: return (greeting ++ name)
}
`)
}

func TestMapEntriesDestructuredInForOfMakeNoPairs(t *testing.T) {
	t.Parallel()
	got := lowerValid(t, `const ages = new Map<string, number>();
for (const [name, age] of ages) {
  console.log(name, age);
}
for (const entry of ages) {
  console.log(entry);
}
`)
	assert.Equal(t, got, `shape tuple1{0: string; 1: number}
func main() void {
  1: let ages: Map<string, number> = new Map<string, number>()
  2: {
    2: let map: Map<string, number> = ages
    2: let iteration: map iterator = map.iterate(map)
    2: #1: while map.next(iteration) {
      2: let name: string = map.key(iteration)
      2: let age: number = map.value(iteration)
      3: console.log(box(name), box(age))
    }
  }
  5: {
    5: let map: Map<string, number> = ages
    5: let iteration: map iterator = map.iterate(map)
    5: #2: while map.next(iteration) {
      5: let entry: [string, number] = tuple1{0: map.key(iteration), 1: map.value(iteration)}
      6: console.log(box(entry))
    }
  }
}
`)
}

func TestFinallyRunsAfterTheProtectedCodeRecordsHowItCompleted(t *testing.T) {
	t.Parallel()
	got := lowerValid(t, `function f(): number {
  try {
    return 1;
  } finally {
    console.log("done");
  }
}
console.log(f());
`)
	assert.Equal(t, got, `func main() void {
  8: console.log(box(f()))
}
func f() number {
  2: {
    2: let completion: i32 = 0:i32
    2: let thrown: value = undefined
    2: let result: number
    2: #1: {
      2: try {
        3: (result = 1)
        3: (completion = 2:i32)
        3: break #1
      } catch (exception) {
        2: (completion = 1:i32)
        2: (thrown = exception)
      }
    }
    4: {
      5: console.log(box("done"))
    }
    2: if (completion === 1:i32) {
      2: throw thrown
    }
    2: if (completion === 2:i32) {
      2: return result
    }
  }
}
`)
}

func TestGenericFunctionValuesAreInstancesInContextAndErasedCopiesElsewhere(t *testing.T) {
	t.Parallel()
	got := lowerValid(t, `function wrap<T>(x: T): T[] {
  return [x];
}
const byString: (s: string) => string[] = wrap;
const any = wrap;
const n: number[] = any(1);
console.log(byString("a"), n);
`)
	assert.Equal(t, got, `func main() void {
  4: let byString: (string) => string[] = closure wrap_string
  5: let any: (value) => value = closure wrap_dynamic
  6: let n: number[] = number[](call any(box(1)))
  7: console.log(box(call byString("a")), box(n))
}
func wrap_string(x: string) string[] {
  2: return string[][x]
}
func wrap_dynamic(x: value) value {
  2: return box(value[][x])
}
`)
}
