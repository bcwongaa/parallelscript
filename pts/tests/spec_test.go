package tests

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
)

// testSpec is what a test file expects, read from its directive comments (see README.md).
type testSpec struct {
	stdout   *string  // expected stdout; nil if the test states none (then only Node decides)
	exitCode int      // expected exit code
	stderr   []string // substrings stderr must contain
	errors   []string // for error tests: substrings the compile errors must contain
	noDiff   string   // why the test must not be compared with Node; empty to compare when it is plain TypeScript
	skip     string   // why the test is skipped: a known issue it reproduces
}

var directivePattern = regexp.MustCompile(`^//\s*(expect|exit|stderr|error|no-diff|skip):\s?(.*)$`)

// readSpec reads the spec of the test at path, including its .out file if there is one.
func readSpec(path string) (testSpec, error) {
	source, err := os.ReadFile(path)
	if err != nil {
		return testSpec{}, err
	}
	spec, err := parseSpec(string(source))
	if err != nil {
		return testSpec{}, err
	}
	out, err := os.ReadFile(strings.TrimSuffix(path, ".pts") + ".out")
	switch {
	case errors.Is(err, os.ErrNotExist):
		return spec, nil
	case err != nil:
		return testSpec{}, err
	case spec.stdout != nil:
		return testSpec{}, errors.New("both a .out file and an // expect: block")
	}
	stdout := string(out)
	spec.stdout = &stdout
	return spec, nil
}

// parseSpec reads the directive comments of a test source.
func parseSpec(source string) (testSpec, error) {
	var spec testSpec
	lines := strings.Split(source, "\n")
	for i := 0; i < len(lines); i++ {
		match := directivePattern.FindStringSubmatch(strings.TrimSpace(lines[i]))
		if match == nil {
			continue
		}
		name, value := match[1], strings.TrimSpace(match[2])
		switch name {
		case "expect":
			if spec.stdout != nil {
				return testSpec{}, fmt.Errorf("line %d: a second // expect: block", i+1)
			}
			if value != "" {
				return testSpec{}, fmt.Errorf("line %d: // expect: starts a block; put the output on the comment lines below it", i+1)
			}
			block, end := expectBlock(lines, i+1)
			spec.stdout = &block
			i = end - 1
		case "exit":
			code, err := strconv.Atoi(value)
			if err != nil {
				return testSpec{}, fmt.Errorf("line %d: // exit: needs a number: %w", i+1, err)
			}
			spec.exitCode = code
		case "stderr":
			spec.stderr = append(spec.stderr, value)
		case "error":
			spec.errors = append(spec.errors, value)
		case "no-diff":
			if value == "" {
				return testSpec{}, fmt.Errorf("line %d: // no-diff: needs a reason", i+1)
			}
			spec.noDiff = value
		case "skip":
			if value == "" {
				return testSpec{}, fmt.Errorf("line %d: // skip: needs a reason", i+1)
			}
			spec.skip = value
		}
	}
	return spec, nil
}

// expectBlock collects the comment lines from start on as output lines; it returns the
// output and the index of the first line after the block.
func expectBlock(lines []string, start int) (string, int) {
	var out strings.Builder
	end := start
	for ; end < len(lines); end++ {
		line := strings.TrimSpace(lines[end])
		text, ok := strings.CutPrefix(line, "//")
		if !ok {
			break
		}
		out.WriteString(strings.TrimPrefix(text, " ") + "\n")
	}
	return out.String(), end
}

// ptsFeaturePattern finds code that only ParallelScript runs: pts: modules, the
// parallel keyword, .par views and the integer types (docs/SPEC.md §1.1).
var ptsFeaturePattern = regexp.MustCompile(`["']pts:|\bparallel\b|\.par\b|(?:[:<,|]\s*|\bas\s+)(?:int|i8|i16|i32|i64|u8|u16|u32|u64|f32|f64)\b`)

var commentPattern = regexp.MustCompile(`(?s)/\*.*?\*/|//[^\n]*`)

// isPlainTypeScript reports whether source uses only TypeScript, so Node must print the same.
// It errs towards false; a test it misjudges can opt out with // no-diff:.
func isPlainTypeScript(source string) bool {
	return !ptsFeaturePattern.MatchString(commentPattern.ReplaceAllString(source, ""))
}
