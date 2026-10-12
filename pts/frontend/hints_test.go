package frontend_test

import (
	"strings"
	"testing"

	"github.com/microsoft/typescript-go/pts/frontend"
	"gotest.tools/v3/assert"
)

// hintOf is frontend.Hint of tsc's first error in program.
func hintOf(t *testing.T, program string) string {
	t.Helper()
	loaded, errors := load(t, map[string]string{"/p/main.pts": program})
	assert.Assert(t, len(errors) > 0, "%q has no errors", program)
	diagnostics := loaded.TS.GetSemanticDiagnostics(t.Context(), loaded.TS.GetSourceFile("/p/main.pts"))
	assert.Assert(t, len(diagnostics) > 0)
	return frontend.Hint(t.Context(), loaded.TS, diagnostics[0])
}

func TestParallelMethodsOnArraysGetAHint(t *testing.T) {
	t.Parallel()
	for program, want := range map[string]string{
		"const n = [1, 2].par.filter((x) => x > 1).count();": "`.par.filter` returns an array: count in parallel with `.par.count(predicate)`",
		"const n = [1, 2].par.map((x) => x * 2).sum();":      "`.par.map` returns an array, whose parallel methods are on `.par` again: `.par.map(…).par.sum()`",
		"const n = [1, 2].sum();":                            "Arrays have no `sum`; their parallel view has: `.par.sum()`",
		"const n = [1, 2].count();":                          "Arrays have no `count`: take `.length`",
		"const n = new Float64Array(2).max();":               "Arrays have no `max`; their parallel view has: `.par.max()`",
	} {
		assert.Assert(t, strings.HasPrefix(hintOf(t, program), want), "%q: %q", program, hintOf(t, program))
	}
}

// The CLI shows the hint on the line after tsc's message.
func TestHintsFollowTscsMessage(t *testing.T) {
	t.Parallel()
	_, errors := load(t, map[string]string{"/p/main.pts": "const n = [1, 2].sum();"})
	assert.Equal(t, errors[0].Message, "Property 'sum' does not exist on type 'number[]'.\n  Arrays have no `sum`; their parallel view has: `.par.sum()`.")
}

func TestParallelThenACallGetsAHint(t *testing.T) {
	t.Parallel()
	hint := hintOf(t, "const p = parallel (async () => 1)();")
	assert.Assert(t, strings.Contains(hint, "A `(` right after `parallel` starts a call of `parallel`"), hint)
}

// Errors that only look alike keep tsc's message alone: a class's missing `sum`, a missing
// method that `.par` doesn't have either, and a program's own function named `parallel`.
func TestErrorsOutsideParallelScriptsFeaturesGetNoHint(t *testing.T) {
	t.Parallel()
	for _, program := range []string{
		"class Bag {}\nconst n = new Bag().sum();",
		"const n = [1, 2].average();",
		"function parallel(f: () => number): number { return f(); }\nconst p = parallel(() => 1)();",
		"const n: number = \"one\";",
	} {
		assert.Equal(t, hintOf(t, program), "", program)
	}
}
