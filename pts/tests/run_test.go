// Package tests runs the ParallelScript test corpus: programs under run/ must build
// and print what they expect (and what Node prints, for plain TypeScript), and
// programs under errors/ must fail to compile with the errors they name.
package tests

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/microsoft/typescript-go/pts/diagnostic"
	"github.com/microsoft/typescript-go/pts/driver"
	"gotest.tools/v3/assert"
)

const (
	maxHeavyJobs   = 6 // the host runs other services (CLAUDE.md)
	programTimeout = 30 * time.Second
	// requireNodeVar makes a missing Node a failure instead of a skip; CI sets it.
	requireNodeVar = "PTS_REQUIRE_NODE"
)

var (
	buildDir  string
	heavyJobs = make(chan struct{}, min(runtime.NumCPU(), maxHeavyJobs))
	builds    sync.Map // test path → *programBuild
)

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "pts-tests-")
	if err != nil {
		panic(err)
	}
	buildDir = dir
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

type runResult struct {
	stdout, stderr string
	exitCode       int
}

func TestRun(t *testing.T) {
	t.Parallel()
	for _, path := range testFiles(t, "run") {
		t.Run(testName(path), func(t *testing.T) {
			t.Parallel()
			spec := ensureSpec(t, path)
			if spec.stdout == nil {
				t.Skip("no expected output; TestRunMatchesNode compares it with Node")
			}
			got := runPts(t, path)
			assert.Equal(t, got.stdout, *spec.stdout)
			assert.Equal(t, got.exitCode, spec.exitCode, "stderr:\n%s", got.stderr)
			for _, want := range spec.stderr {
				assert.Assert(t, strings.Contains(got.stderr, want), "stderr lacks %q:\n%s", want, got.stderr)
			}
		})
	}
}

// TestRunMatchesNode is the differential test (D-O04): a plain-TypeScript program
// must print exactly what Node prints and exit with the same code.
func TestRunMatchesNode(t *testing.T) {
	t.Parallel()
	node := findNode(t)
	for _, path := range testFiles(t, "run") {
		t.Run(testName(path), func(t *testing.T) {
			t.Parallel()
			spec := ensureSpec(t, path)
			source, err := os.ReadFile(path)
			assert.NilError(t, err)
			switch {
			case spec.noDiff != "":
				t.Skip("no-diff: " + spec.noDiff)
			case !isPlainTypeScript(string(source)):
				t.Skip("uses ParallelScript features")
			}
			want := runNode(t, node, string(source))
			got := runPts(t, path)
			assert.Equal(t, got.stdout, want.stdout, "pts and Node print different output")
			assert.Equal(t, got.exitCode, want.exitCode, "pts and Node exit differently; pts stderr:\n%s\nNode stderr:\n%s", got.stderr, want.stderr)
		})
	}
}

func TestErrors(t *testing.T) {
	t.Parallel()
	for _, path := range testFiles(t, "errors") {
		t.Run(testName(path), func(t *testing.T) {
			t.Parallel()
			spec := ensureSpec(t, path)
			assert.Assert(t, len(spec.errors) > 0, "an error test needs at least one // error: line")
			_, err := driver.EmitC(t.Context(), path)
			var compileErr *diagnostic.Error
			assert.Assert(t, errors.As(err, &compileErr), "expected compile errors, got %v", err)
			report := formatDiagnostics(compileErr.Diagnostics, filepath.Dir(path))
			for _, want := range spec.errors {
				assert.Assert(t, strings.Contains(report, want), "no error contains %q; errors:\n%s", want, report)
			}
		})
	}
}

func testFiles(t *testing.T, dir string) []string {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(dir, "*.pts"))
	assert.NilError(t, err)
	assert.Assert(t, len(paths) > 0, "no tests in %s", dir)
	for i, path := range paths {
		paths[i], err = filepath.Abs(path)
		assert.NilError(t, err)
	}
	return paths
}

func testName(path string) string {
	return strings.TrimSuffix(filepath.Base(path), ".pts")
}

// ensureSpec reads the test's spec, skipping the test if the spec says so.
func ensureSpec(t *testing.T, path string) testSpec {
	t.Helper()
	spec, err := readSpec(path)
	assert.NilError(t, err, "malformed test directives")
	if spec.skip != "" {
		t.Skip("skip: " + spec.skip)
	}
	return spec
}

func formatDiagnostics(diagnostics []diagnostic.Diagnostic, dir string) string {
	lines := make([]string, len(diagnostics))
	for i, d := range diagnostics {
		lines[i] = d.Format(dir)
	}
	return strings.Join(lines, "\n")
}

// ---------------------------------------------------------------- running programs

type programBuild struct {
	once sync.Once
	exe  string
	err  error
}

// runPts builds the program at path (once per test run, shared by the tests that need it) and runs it.
func runPts(t *testing.T, path string) runResult {
	t.Helper()
	entry, _ := builds.LoadOrStore(path, &programBuild{})
	build := entry.(*programBuild)
	build.once.Do(func() {
		heavyJobs <- struct{}{}
		defer func() { <-heavyJobs }()
		build.exe = filepath.Join(buildDir, testName(path))
		build.err = driver.Build(context.Background(), path, build.exe)
	})
	assert.NilError(t, build.err)
	return execute(t, filepath.Dir(path), build.exe)
}

// runNode runs source as an ES module under Node with TypeScript support (D-O04, D-009).
func runNode(t *testing.T, node string, source string) runResult {
	t.Helper()
	dir := t.TempDir()
	module := filepath.Join(dir, "main.mts")
	assert.NilError(t, os.WriteFile(module, []byte(source), 0o644))
	return execute(t, dir, node, "--no-warnings", "--experimental-transform-types", module)
}

func execute(t *testing.T, dir string, name string, args ...string) runResult {
	t.Helper()
	heavyJobs <- struct{}{}
	defer func() { <-heavyJobs }()
	ctx, cancel := context.WithTimeout(t.Context(), programTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	result := runResult{stdout: stdout.String(), stderr: stderr.String()}
	var exitErr *exec.ExitError
	switch {
	case errors.As(err, &exitErr):
		result.exitCode = exitErr.ExitCode()
	case err != nil:
		t.Fatalf("running %s: %v", name, err)
	}
	return result
}

func findNode(t *testing.T) string {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		if os.Getenv(requireNodeVar) != "" {
			t.Fatalf("node is not on PATH and %s is set", requireNodeVar)
		}
		t.Skip("node is not on PATH; differential tests skipped")
	}
	return node
}
