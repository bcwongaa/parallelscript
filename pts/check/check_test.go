package check_test

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/vfs/vfstest"
	"github.com/microsoft/typescript-go/pts/check"
	"github.com/microsoft/typescript-go/pts/diagnostic"
	"github.com/microsoft/typescript-go/pts/frontend"
	"gotest.tools/v3/assert"
)

// checkSource type-checks source as /p/main.pts, which must have no tsc errors, and runs the pass.
func checkSource(t *testing.T, source string) []diagnostic.Diagnostic {
	t.Helper()
	fs := vfstest.FromMap(map[string]string{"/p/main.pts": source}, true /*useCaseSensitiveFileNames*/)
	program, errors := frontend.Load(t.Context(), fs, "/p/main.pts")
	assert.DeepEqual(t, errors, []diagnostic.Diagnostic(nil))
	_, errors = check.Program(t.Context(), program)
	return errors
}

// formatted renders diagnostics relative to /p.
func formatted(diagnostics []diagnostic.Diagnostic) []string {
	lines := []string{}
	for _, d := range diagnostics {
		lines = append(lines, d.Format("/p"))
	}
	return lines
}

func TestLosslessConversionsAreImplicit(t *testing.T) {
	t.Parallel()
	errors := checkSource(t, `const small: u8 = 200;
const signed: i16 = small;
const wide: i64 = signed;
const unsigned: u64 = small;
const asNumber: number = wide;
const half: f32 = 0.5;
const fromFloat: number = half;
const big: u32 = 4000000000;
const bigger: u64 = big;
const viaInt: int = 7;
const viaF64: f64 = viaInt;
`)
	assert.DeepEqual(t, formatted(errors), []string{})
}

func TestLossyImplicitConversionsNeedAs(t *testing.T) {
	t.Parallel()
	errors := checkSource(t, `const n = 1.5;
const i: int = n;
const w: i64 = 1;
const narrow: i32 = w;
const s: i8 = -1;
const u: u16 = s;
const f: f32 = n;
const g: f32 = w;
const converted: int = n as int;
`)
	assert.DeepEqual(t, formatted(errors), []string{
		"main.pts:2:16 - error PTS2001: a number doesn't convert to int implicitly because the value could change; write `as int` to convert it",
		"main.pts:4:21 - error PTS2001: a int doesn't convert to i32 implicitly because the value could change; write `as i32` to convert it",
		"main.pts:6:16 - error PTS2001: a i8 doesn't convert to u16 implicitly because the value could change; write `as u16` to convert it",
		"main.pts:7:16 - error PTS2001: a number doesn't convert to f32 implicitly because the value could change; write `as f32` to convert it",
		"main.pts:8:16 - error PTS2001: a int doesn't convert to f32 implicitly because the value could change; write `as f32` to convert it",
	})
}

func TestIntegerLiteralsMustFitTheirType(t *testing.T) {
	t.Parallel()
	errors := checkSource(t, `const a: u8 = 255;
const b: u8 = 256;
const c: i8 = -128;
const d: i8 = -129;
const e: u8 = -1;
const f: u64 = 18446744073709551615;
const g: int = -9223372036854775808;
const h: int = 9223372036854775808;
const i: u32 = 0xFFFF_FFFF;
const j: int = 1e3;
const k: int = 1.5;
type Count = u8;
const l: Count = 300;
`)
	assert.DeepEqual(t, formatted(errors), []string{
		"main.pts:2:15 - error PTS2002: 256 doesn't fit in u8 (0 to 255)",
		"main.pts:4:15 - error PTS2002: -129 doesn't fit in i8 (-128 to 127)",
		"main.pts:5:15 - error PTS2002: -1 doesn't fit in u8 (0 to 255)",
		"main.pts:8:16 - error PTS2002: 9223372036854775808 doesn't fit in int (-9223372036854775808 to 9223372036854775807)",
		"main.pts:11:16 - error PTS2002: 1.5 is not an integer, so it can't be a int; write `as int` to convert it",
		"main.pts:13:18 - error PTS2002: 300 doesn't fit in u8 (0 to 255)",
	})
}

func TestOperandsConvertToTheirCommonType(t *testing.T) {
	t.Parallel()
	errors := checkSource(t, `const a: i32 = 1;
const b: u8 = 2;
const c: i32 = a + b;
const d: number = a * 1.5;
const e: i32 = a + 1;
const f: u8 = b + 300;
const g: i64 = 1;
const h: u64 = 2;
console.log(g + h, g < h, a < 2, c, d, e, f);
const fromConstants: u8 = 200 + 55;
`)
	assert.DeepEqual(t, formatted(errors), []string{
		"main.pts:6:19 - error PTS2002: 300 doesn't fit in u8 (0 to 255)",
		"main.pts:9:13 - error PTS2003: `+` can't mix int and u64, because neither converts to the other without loss; convert one with `as`",
		"main.pts:9:20 - error PTS2003: `<` can't mix int and u64, because neither converts to the other without loss; convert one with `as`",
	})
}

func TestMixingIntegerAndNumberYieldsNumber(t *testing.T) {
	t.Parallel()
	errors := checkSource(t, "const a: int = 3;\nconst b: int = a * 0.5;\n")
	assert.DeepEqual(t, formatted(errors), []string{
		"main.pts:2:16 - error PTS2001: a number doesn't convert to int implicitly because the value could change; write `as int` to convert it",
	})
}
