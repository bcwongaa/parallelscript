package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"gotest.tools/v3/assert"
)

// testProject writes files, by path relative to a fresh directory, and returns it.
func testProject(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		path := filepath.Join(dir, name)
		assert.NilError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		assert.NilError(t, os.WriteFile(path, []byte(content), 0o644))
	}
	return dir
}

var (
	durationPattern   = regexp.MustCompile(`\(\d+(\.\d+)?ms\)|duration_ms \d+(\.\d+)?`)
	stackFramePattern = regexp.MustCompile(`\n +at [^\n]*?( \{)?\n`)
)

// normalized is a report with its durations as N, dir as <dir>, and no stack frames
// (their lines are the C compiler's to attribute).
func normalized(report string, dir string) string {
	report = durationPattern.ReplaceAllStringFunc(report, func(d string) string {
		if strings.HasPrefix(d, "duration_ms") {
			return "duration_ms N"
		}
		return "(Nms)"
	})
	report = strings.ReplaceAll(report, dir, "<dir>")
	for stackFramePattern.MatchString(report) {
		report = stackFramePattern.ReplaceAllString(report, "$1\n")
	}
	return report
}

const mathTests = `import { after, afterEach, before, beforeEach, describe, it, test } from "pts:test";
import assert from "pts:assert";

test("adds", () => {
  assert.equal(1 + 1, 2);
});
test("compares", () => {
  assert.equal(2 * 2, 5);
});
test("waits", async () => {
  assert.equal(await Promise.resolve("done"), "done");
});
test("skipped", { skip: "not on this machine" }, () => {
  assert.fail("ran");
});
test("to do", { todo: true }, () => {
  assert.fail("not yet");
});
test("skips itself", (t) => {
  t.skip();
});
describe("a suite", () => {
  before(() => console.log("before"));
  beforeEach(() => console.log("beforeEach"));
  afterEach(() => console.log("afterEach"));
  after(() => console.log("after"));
  it("runs", () => console.log("runs"));
  describe("nested", () => {
    it("deep-compares", () => {
      assert.deepEqual({ list: [1, 2] }, { list: [1, 3] });
    });
  });
});
`

const mathResults = `✔ adds (Nms)
✖ compares (Nms)
✔ waits (Nms)
﹣ skipped (Nms) # not on this machine
✖ to do (Nms) # TODO
﹣ skips itself (Nms) # SKIP
▶ a suite
before
beforeEach
runs
afterEach
  ✔ runs (Nms)
  ▶ nested
beforeEach
afterEach
    ✖ deep-compares (Nms)
  ✖ nested (Nms)
after
✖ a suite (Nms)
`

const mathFailures = `✖ failing tests:

test at math.test.pts:7
✖ compares (Nms)
  AssertionError: Expected values to be strictly equal:
  
  4 !== 5
   {
    code: 'ERR_ASSERTION',
    actual: 4,
    expected: 5,
    operator: 'strictEqual',
    generatedMessage: true
  }
`

// pts test finds the test files under a directory (not under node_modules or dot
// directories), runs each, and reports their tests, totals and failures as Node's spec
// reporter does; a failed test makes the exit code 1.
func TestTestRunsTheTestFilesAndReportsLikeNode(t *testing.T) {
	t.Parallel()
	dir := testProject(t, map[string]string{
		"math.test.pts":                 mathTests,
		"sub/strings_test.pts":          "import { test } from \"pts:test\";\nimport { match } from \"pts:assert\";\n\ntest(\"matches\", () => {\n  match(\"parallel\", /^par/);\n});\n",
		"node_modules/dep/dep.test.pts": "import { test } from \"pts:test\";\ntest(\"dependency\", () => { throw new Error(\"ran\"); });\n",
		".hidden/hidden.test.pts":       "import { test } from \"pts:test\";\ntest(\"hidden\", () => { throw new Error(\"ran\"); });\n",
		"helper.pts":                    "export const helper = 1;\n",
	})
	got := runIn(t, dir, "test")
	assert.Equal(t, got.code, exitFailure, got.stderr)
	report := normalized(got.stdout, dir)
	assert.Assert(t, strings.HasPrefix(report, mathResults+"✔ matches (Nms)\nℹ tests 9\nℹ suites 2\nℹ pass 4\nℹ fail 2\nℹ cancelled 0\nℹ skipped 2\nℹ todo 1\nℹ duration_ms N\n\n"+mathFailures), report)
	assert.Assert(t, strings.Contains(report, "\n✖ deep-compares (Nms)\n  AssertionError: Expected values to be strictly deep-equal:\n  + actual - expected\n  \n  + { list: [ 1, 2 ] }\n  - { list: [ 1, 3 ] }\n"), report)
}

// A test file is a program: run on its own, it prints its own totals and failures.
func TestATestFileRunsOnItsOwn(t *testing.T) {
	t.Parallel()
	dir := testProject(t, map[string]string{"math.test.pts": mathTests})
	got := runIn(t, dir, "run", "math.test.pts")
	assert.Equal(t, got.code, exitFailure, got.stderr)
	report := normalized(got.stdout, dir)
	assert.Assert(t, strings.HasPrefix(report, mathResults+"ℹ tests 8\nℹ suites 2\nℹ pass 3\nℹ fail 2\n"), report)
	assert.Assert(t, strings.Contains(report, "\n\n"+mathFailures), report)
}

// Files named as arguments run whatever their names; a file that doesn't compile, or ends
// before its tests report, is a failure of its own. Passing files alone exit with 0.
func TestTestReportsFilesThatFailToCompileOrToReport(t *testing.T) {
	t.Parallel()
	dir := testProject(t, map[string]string{
		"passes.pts":      "import { test } from \"pts:test\";\ntest(\"passes\", () => {});\n",
		"broken.test.pts": "const n: number = \"one\";\n",
		"crash.test.pts":  "import { test } from \"pts:test\";\ntest(\"never runs\", () => {});\nthrow new Error(\"at the top level\");\n",
	})
	got := runIn(t, dir, "test", "passes.pts")
	assert.Equal(t, got.code, 0, got.stdout+got.stderr)
	assert.Assert(t, strings.HasPrefix(normalized(got.stdout, dir), "✔ passes (Nms)\nℹ tests 1\nℹ suites 0\nℹ pass 1\nℹ fail 0\n"), got.stdout)

	got = runIn(t, dir, "test", "broken.test.pts", "crash.test.pts")
	assert.Equal(t, got.code, exitFailure)
	assert.Assert(t, strings.Contains(got.stderr, "broken.test.pts:1:7 - error TS2322"), got.stderr)
	assert.Assert(t, strings.Contains(got.stderr, "Uncaught Error: at the top level"), got.stderr)
	report := normalized(got.stdout, dir)
	assert.Assert(t, strings.Contains(report, "ℹ tests 2\nℹ suites 0\nℹ pass 0\nℹ fail 2\n"), report)
	assert.Assert(t, strings.Contains(report, "✖ broken.test.pts\n  it didn't compile: <dir>/broken.test.pts:1:7 - error TS2322"), report)
	assert.Assert(t, strings.Contains(report, "✖ crash.test.pts\n  it ended before its tests reported: exited with code 1"), report)
}

func TestTestWithoutTestFilesFails(t *testing.T) {
	t.Parallel()
	dir := testProject(t, map[string]string{"main.pts": "console.log(1);\n"})
	got := runIn(t, dir, "test")
	assert.Equal(t, got.code, exitFailure)
	assert.Assert(t, strings.Contains(got.stderr, "pts: no test files in . (test files are named name.test.pts"), got.stderr)
	got = runIn(t, dir, "test", "--watch")
	assert.Equal(t, got.code, exitUsage)
}

// The guide's testing sample (docs/GUIDE.md, "Testing") passes, printing what the guide shows.
func TestGuidesTestingSamplePasses(t *testing.T) {
	t.Parallel()
	dir, err := filepath.Abs(filepath.Join("..", "..", "docs", "examples", "testing"))
	assert.NilError(t, err)
	got := runIn(t, dir, "test")
	assert.Equal(t, got.code, 0, got.stdout+got.stderr)
	assert.Equal(t, normalized(got.stdout, dir), `▶ mean
  ✔ averages the values (Nms)
  ✔ throws for no values (Nms)
✔ mean (Nms)
✔ the median of an even count is the mean of the middle two (Nms)
✔ median leaves its input as it was (Nms)
ℹ tests 4
ℹ suites 1
ℹ pass 4
ℹ fail 0
ℹ cancelled 0
ℹ skipped 0
ℹ todo 0
ℹ duration_ms N
`)
}

func TestTestReportsATestFileTheCompilerFailsOn(t *testing.T) {
	t.Parallel()
	dir := testProject(t, map[string]string{"bug.test.pts": "import { test } from \"pts:test\";\ntest(\"adds\", () => { console.log(1 + 2); });\n"})

	got := runWithABug(t, false, "bug.test.pts", dir, "test")

	assert.Equal(t, got.code, exitFailure)
	assert.Assert(t, strings.Contains(got.stderr, "bug.test.pts:2:34 - error PTS9001: internal compiler error while lowering: injected failure\n"+
		"  This is a bug in pts, not in the program."), got.stderr)
	report := normalized(got.stdout, dir)
	assert.Assert(t, strings.Contains(report, "✖ bug.test.pts\n  it didn't compile: <dir>/bug.test.pts:2:34 - error PTS9001: internal compiler error while lowering: injected failure\n"), report)
}
