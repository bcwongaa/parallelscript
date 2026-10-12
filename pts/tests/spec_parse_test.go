package tests

import (
	"testing"

	"gotest.tools/v3/assert"
)

func TestExpectBlockBecomesExpectedStdout(t *testing.T) {
	t.Parallel()
	spec, err := parseSpec("console.log(1);\n// expect:\n// 1\n//\n//   indented\nconsole.log(2);\n// not output\n")

	assert.NilError(t, err)
	assert.Equal(t, *spec.stdout, "1\n\n  indented\n")
}

func TestADirectiveEndsTheExpectBlock(t *testing.T) {
	t.Parallel()
	spec, err := parseSpec("// expect:\n// before\n// exit: 1\n// stderr: boom\n")

	assert.NilError(t, err)
	assert.Equal(t, *spec.stdout, "before\n")
	assert.Equal(t, spec.exitCode, 1)
	assert.DeepEqual(t, spec.stderr, []string{"boom"})
}

func TestDirectivesAreCollected(t *testing.T) {
	t.Parallel()
	spec, err := parseSpec("// exit: 1\n// stderr: Uncaught RangeError\n// stderr: second\n// error: TS2322\n// no-diff: prints a pointer\n// skip: known issue\n")

	assert.NilError(t, err)
	assert.Assert(t, spec.stdout == nil)
	assert.Equal(t, spec.exitCode, 1)
	assert.DeepEqual(t, spec.stderr, []string{"Uncaught RangeError", "second"})
	assert.DeepEqual(t, spec.errors, []string{"TS2322"})
	assert.Equal(t, spec.noDiff, "prints a pointer")
	assert.Equal(t, spec.skip, "known issue")
}

func TestMalformedDirectivesAreErrors(t *testing.T) {
	t.Parallel()
	for _, source := range []string{"// expect:\n// a\nx;\n// expect:\n// b\n", "// expect: inline\n", "// exit: one\n", "// no-diff:\n", "// skip:\n"} {
		_, err := parseSpec(source)
		assert.Assert(t, err != nil, "accepted %q", source)
	}
}

func TestPtsFeaturesAreNotPlainTypeScript(t *testing.T) {
	t.Parallel()
	for _, source := range []string{
		`import { Mutex } from "pts:sync";`,
		`import { join } from "pts:path"; import { range } from "pts:parallel";`,
		`const r = await parallel work();`,
		`const s = xs.par.sum();`,
		`let n: int = 1;`,
		`const b = x as u8;`,
		`const m = new Map<string, i64>();`,
	} {
		assert.Assert(t, !isPlainTypeScript(source), "%q is PTS-only", source)
	}
}

func TestOrdinaryTypeScriptIsPlain(t *testing.T) {
	t.Parallel()
	for _, source := range []string{
		`const interval = 5; console.log("int", interval);`,
		"// runs parallel to the other test\nconsole.log(1);",
		`const parse = 1; console.log(parse);`,
		`import { join } from "pts:path"; import process from 'pts:process';`,
	} {
		assert.Assert(t, isPlainTypeScript(source), "%q is plain TypeScript", source)
	}
}
