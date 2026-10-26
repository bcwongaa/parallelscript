package tests

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gotest.tools/v3/assert"
)

// nodeArchNumbersFile holds expressions whose results depend on the machine Node runs on, and a few
// that pin related behaviour, with what Node printed on x86-64 and arm64 Linux and arm64 macOS (D-227,
// D-228): Node's linux-arm64 binary is built by GCC, whose default -ffp-contract=fast fuses
// multiply-adds in V8's C++ (Math's fdlibm, parseInt in radixes that aren't powers of two, MakeDate),
// macOS's by Apple clang, which fuses only within one expression, and the runtime does the same where
// it ports that code.
const nodeArchNumbersFile = "testdata/node_arch_numbers.tsv"

// The platform-arch names a program prints (process.platform + "-" + process.arch) and the
// column of the file that holds what Node printed there.
var nodeArchColumns = map[string]int{"linux-x64": 1, "linux-arm64": 2, "darwin-arm64": 3}

type nodeArchCase struct {
	expr string
	want []string // indexed by column: want[1] on linux-x64, want[2] on linux-arm64, want[3] on darwin-arm64
}

// TestNumbersPrintAsNodeOfTheProgramsArchitecture builds one program of every expression in
// nodeArchNumbersFile for the tests' target (PTS_TARGET, else this machine) and requires each line to
// be what Node printed on the architecture the program runs on. It needs no Node: the expectations
// were recorded from Node 24's official x86-64 and arm64 Linux and arm64 macOS binaries.
func TestNumbersPrintAsNodeOfTheProgramsArchitecture(t *testing.T) {
	t.Parallel()
	cases := readNodeArchCases(t)
	source := nodeArchProgram(cases)
	path := filepath.Join(t.TempDir(), "node_arch_numbers.pts")
	assert.NilError(t, os.WriteFile(path, []byte(source), 0o644))

	got := runPts(t, path)

	assert.Equal(t, got.exitCode, 0, got.stderr)
	lines := strings.Split(strings.TrimSuffix(got.stdout, "\n"), "\n")
	column, ok := nodeArchColumns[lines[0]]
	if !ok {
		t.Fatalf("%s has no expectations for %s", nodeArchNumbersFile, lines[0])
	}
	assert.Equal(t, len(lines)-1, len(cases))
	differ := 0
	for i, c := range cases {
		if lines[i+1] != c.want[column] {
			differ++
			if differ <= maxReportedDiffers {
				t.Errorf("%s on %s\n  pts:  %s\n  node: %s", c.expr, lines[0], lines[i+1], c.want[column])
			}
		}
	}
	assert.Equal(t, differ, 0, "lines that differ from Node on %s", lines[0])
}

// readNodeArchCases reads nodeArchNumbersFile: "#" lines are comments, the others
// "expression<TAB>linux-x64 output<TAB>linux-arm64 output<TAB>darwin-arm64 output".
func readNodeArchCases(t *testing.T) []nodeArchCase {
	t.Helper()
	content, err := os.ReadFile(nodeArchNumbersFile)
	assert.NilError(t, err)
	var cases []nodeArchCase
	for _, line := range strings.Split(strings.TrimSuffix(string(content), "\n"), "\n") {
		if strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Split(line, "\t")
		assert.Equal(t, len(fields), 4, "malformed line in %s: %q", nodeArchNumbersFile, line)
		cases = append(cases, nodeArchCase{expr: fields[0], want: fields})
	}
	return cases
}

// nodeArchProgram prints where it runs, then each case's expression on a line of its own.
func nodeArchProgram(cases []nodeArchCase) string {
	var source strings.Builder
	source.WriteString("console.log(process.platform + \"-\" + process.arch);\n")
	for _, c := range cases {
		source.WriteString("console.log(" + c.expr + ");\n")
	}
	return source.String()
}
