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
	threads  []string // PTS_THREADS values to run the program with, each expecting the same; none to run it once with the default
	// publication is the publication checker's options a program runs with in
	// TestConstructionsReachOtherThreadsWhole ("strict"): it only makes and publishes
	// objects, never changing one after its construction (publication_test.go).
	publication string
}

var directivePattern = regexp.MustCompile(`^//\s*(expect|exit|stderr|error|no-diff|skip|threads|publication):\s?(.*)$`)

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
		case "publication":
			if value != "strict" {
				return testSpec{}, fmt.Errorf("line %d: // publication: takes strict", i+1)
			}
			spec.publication = value
		case "threads":
			for _, count := range strings.Fields(value) {
				if n, err := strconv.Atoi(count); err != nil || n < 1 {
					return testSpec{}, fmt.Errorf("line %d: // threads: needs positive numbers", i+1)
				}
				spec.threads = append(spec.threads, count)
			}
		}
	}
	return spec, nil
}

// expectBlock collects the comment lines from start on as output lines, up to a line
// that isn't a comment or is another directive; it returns the output and the index of
// the first line after the block.
func expectBlock(lines []string, start int) (string, int) {
	var out strings.Builder
	end := start
	for ; end < len(lines); end++ {
		line := strings.TrimSpace(lines[end])
		text, ok := strings.CutPrefix(line, "//")
		if !ok || directivePattern.MatchString(line) {
			break
		}
		out.WriteString(strings.TrimPrefix(text, " ") + "\n")
	}
	return out.String(), end
}

// ptsFeaturePattern finds code that only ParallelScript runs: the parallel keyword, .par
// views and the integer types (docs/SPEC.md §1.1); ptsModulePattern finds pts: modules.
var (
	ptsFeaturePattern = regexp.MustCompile(`\bparallel\b|\.par\b|(?:[:<,|]\s*|\bas\s+)(?:int|i8|i16|i32|i64|u8|u16|u32|u64|f32|f64)\b`)
	ptsModulePattern  = regexp.MustCompile(`(["'])pts:([^"'\n]*)(["'])`)
)

// nodeModules are the pts: modules that are Node's modules (D-116, D-171), each with the
// name of Node's: a program that imports only these is compared with Node, which imports
// `node:path` for `pts:path` and `node:assert/strict` for `pts:assert`.
var nodeModules = map[string]string{"path": "path", "process": "process", "fs": "fs", "fs/promises": "fs/promises", "assert": "assert/strict"}

var commentPattern = regexp.MustCompile(`(?s)/\*.*?\*/|//[^\n]*`)

// isPlainTypeScript reports whether source uses only TypeScript, so Node must print the same.
// It errs towards false; a test it misjudges can opt out with // no-diff:.
func isPlainTypeScript(source string) bool {
	code := commentPattern.ReplaceAllString(source, "")
	for _, module := range ptsModulePattern.FindAllStringSubmatch(code, -1) {
		if nodeModules[module[2]] == "" {
			return false
		}
	}
	return !ptsFeaturePattern.MatchString(code)
}
