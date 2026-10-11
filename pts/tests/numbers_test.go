package tests

import (
	"fmt"
	"math"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"gotest.tools/v3/assert"
)

const (
	literalsPerPart          = 2000
	literalsPerLog           = 8
	randomPairsPerPart       = 600
	maxReportedDiffers       = 10
	numberMethodCasesPerPart = 400
)

// specialOperands are the values where number operators are easiest to get wrong.
var specialOperands = []string{"0", "-0", "1", "-1", "0.5", "-2.5", "3", "NaN", "Infinity", "-Infinity",
	"9007199254740992", "1e308", "5e-324", "0.1", "1e-7", "123456.789"}

// numberCorpusParts generate the corpus in parts, so that clang compiles several
// smaller programs in parallel rather than one huge function. Each part writes its
// statements with emit; the seeds keep the corpus the same on every run.
var numberCorpusParts = []struct {
	name     string
	seed     uint64
	generate func(rng *rand.Rand, emit func(string))
}{
	{"literals_1", 1, emitLiterals},
	{"literals_2", 2, emitLiterals},
	{"special_operands", 3, emitSpecialOperands},
	{"random_operands_1", 4, emitRandomOperands},
	{"random_operands_2", 5, emitRandomOperands},
	{"methods_1", 6, emitNumberMethods},
	{"methods_2", 7, emitNumberMethods},
	{"math", 8, emitMathFunctions},
}

// TestGeneratedNumberCorpusMatchesNode is the large differential corpus for numbers:
// thousands of literals printed with Number::toString, and every operator applied to
// special and random operands, run by pts and by Node.
func TestGeneratedNumberCorpusMatchesNode(t *testing.T) {
	t.Parallel()
	node := findNode(t)
	for _, part := range numberCorpusParts {
		t.Run(part.name, func(t *testing.T) {
			t.Parallel()
			source, statements := generateCorpus(rand.New(rand.NewPCG(part.seed, 11)), part.generate)
			path := filepath.Join(t.TempDir(), "number_"+part.name+".pts")
			assert.NilError(t, os.WriteFile(path, []byte(source), 0o644))
			compareWithNode(t, node, path, source, statements)
		})
	}
}

// compareWithNode runs the program under pts and Node and reports the lines that differ.
func compareWithNode(t *testing.T, node string, path string, source string, statements []string) {
	t.Helper()
	want := runNode(t, node, map[string]string{"main.pts": source}, "main.pts")
	got := runPts(t, path)
	assert.Equal(t, got.exitCode, 0, got.stderr)
	assert.Equal(t, want.exitCode, 0, want.stderr)
	gotLines, wantLines := strings.Split(got.stdout, "\n"), strings.Split(want.stdout, "\n")
	assert.Equal(t, len(gotLines), len(wantLines))
	differ := 0
	for i := range gotLines {
		if gotLines[i] != wantLines[i] {
			differ++
			if differ <= maxReportedDiffers {
				t.Errorf("%s\n  pts:  %s\n  node: %s", statements[i], gotLines[i], wantLines[i])
			}
		}
	}
	assert.Equal(t, differ, 0, "lines that differ from Node")
}

// generateCorpus returns a program and, for each line it prints, the code that prints it.
func generateCorpus(rng *rand.Rand, generate func(rng *rand.Rand, emit func(string))) (string, []string) {
	var source strings.Builder
	var statements []string
	generate(rng, func(code string) {
		source.WriteString(code + "\n")
		statements = append(statements, code)
	})
	return source.String(), statements
}

func emitLiterals(rng *rand.Rand, emit func(string)) {
	for i := 0; i < literalsPerPart; i += literalsPerLog {
		literals := make([]string, literalsPerLog)
		for j := range literals {
			literals[j] = numberLiteral(randomNumber(rng, i+j))
		}
		emit("console.log(" + strings.Join(literals, ", ") + ");")
	}
}

func emitSpecialOperands(_ *rand.Rand, emit func(string)) {
	for _, a := range specialOperands {
		for _, b := range specialOperands {
			emit(operatorBlock(a, b))
		}
	}
}

func emitRandomOperands(rng *rand.Rand, emit func(string)) {
	for i := range randomPairsPerPart {
		emit(operatorBlock(numberLiteral(randomNumber(rng, i)), numberLiteral(randomNumber(rng, i+1))))
	}
}

// emitNumberMethods prints Number's formatting methods, and parseInt and parseFloat of
// their results, for random numbers and arguments (D-083).
func emitNumberMethods(rng *rand.Rand, emit func(string)) {
	for i := range numberMethodCasesPerPart {
		x := numberLiteral(randomNumber(rng, i))
		if i%9 == 0 {
			x = specialOperands[rng.IntN(len(specialOperands))]
		}
		radix := 2 + rng.IntN(35)
		emit(fmt.Sprintf("{ const x = %s; console.log(x.toFixed(%d), x.toPrecision(%d), x.toExponential(%d), x.toExponential(), x.toString(%d), "+
			"x.toLocaleString(), parseFloat(String(x)), parseInt(x.toString(%d), %d), parseInt(String(x))); }",
			x, rng.IntN(21), 1+rng.IntN(21), rng.IntN(21), radix, radix, radix))
	}
}

// emitMathFunctions prints every function of Math of random numbers, which must agree
// with Node's (fdlibm's) to the last bit (D-084).
func emitMathFunctions(rng *rand.Rand, emit func(string)) {
	for i := range numberMethodCasesPerPart {
		x, y := numberLiteral(randomNumber(rng, i)), numberLiteral(randomNumber(rng, i+1))
		if i%3 == 0 { // near the ranges where the functions do their work
			x = numberLiteral((rng.Float64() - 0.5) * math.Pow(10, float64(rng.IntN(5))))
		}
		emit(fmt.Sprintf("{ const x = %s; const y = %s; console.log(Math.sin(x), Math.cos(x), Math.tan(x), Math.asin(x), Math.acos(x), Math.atan(x), "+
			"Math.sinh(x), Math.cosh(x), Math.tanh(x), Math.asinh(x), Math.acosh(x), Math.atanh(x), Math.exp(x), Math.expm1(x), Math.log(x), "+
			"Math.log1p(x), Math.log10(x), Math.log2(x), Math.cbrt(x), Math.atan2(x, y), Math.pow(x, y), Math.hypot(x, y), Math.fround(x), "+
			"Math.imul(x, y), Math.clz32(x)); }", x, y))
	}
}

// operatorBlock prints every number operator applied to a and b, in a block of its own.
// The operands are let-bound so tsc types them as number, not as literal types.
func operatorBlock(a string, b string) string {
	return fmt.Sprintf("{ let a = %s; let b = %s; console.log(a + b, a - b, a * b, a / b, a %% b, a ** b, "+
		"a < b, a <= b, a > b, a >= b, a === b, a !== b, a == b, -a, +b, "+
		"a & b, a | b, a ^ b, a << b, a >> b, a >>> b, ~a, !a, a ? 1 : 0); }", a, b)
}

// randomNumber draws from a mix of distributions, chosen by kind: raw bit patterns,
// small integers, short decimals and powers of two near the edges of the range.
func randomNumber(rng *rand.Rand, kind int) float64 {
	switch kind % 4 {
	case 0:
		for {
			if v := math.Float64frombits(rng.Uint64()); !math.IsNaN(v) && !math.IsInf(v, 0) {
				return v
			}
		}
	case 1:
		return float64(rng.IntN(201) - 100)
	case 2:
		return float64(rng.IntN(2_000_000)-1_000_000) / math.Pow(10, float64(rng.IntN(30)-8))
	default:
		return math.Ldexp(1+rng.Float64(), rng.IntN(2098)-1074)
	}
}

// numberLiteral writes v as TypeScript source that evaluates to exactly v.
func numberLiteral(v float64) string {
	switch {
	case math.IsNaN(v):
		return "NaN"
	case math.IsInf(v, 1):
		return "Infinity"
	case math.IsInf(v, -1):
		return "-Infinity"
	case v == 0 && math.Signbit(v):
		return "-0"
	}
	return strconv.FormatFloat(v, 'g', -1, 64)
}
