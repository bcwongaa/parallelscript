// Package tests runs the ParallelScript test corpus: programs under run/ (and the docs'
// samples, docs/examples/) must build and print what they expect (and what Node
// prints, for plain TypeScript), and programs under errors/ must fail to compile with
// the errors they name.
package tests

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/microsoft/typescript-go/pts/diagnostic"
	"github.com/microsoft/typescript-go/pts/driver"
	"gotest.tools/v3/assert"
)

const (
	docExamples    = "../../docs/examples" // the docs' samples, which run as run tests do
	maxHeavyJobs   = 6                     // the host runs other services (CLAUDE.md)
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
	for _, path := range runTestFiles(t) {
		t.Run(testName(path), func(t *testing.T) {
			t.Parallel()
			spec := ensureSpec(t, path)
			if spec.stdout == nil {
				t.Skip("no expected output; TestRunMatchesNode compares it with Node")
			}
			if len(spec.threads) == 0 {
				assertRunAsExpected(t, spec, runPts(t, path))
				return
			}
			for _, threads := range spec.threads {
				t.Run("threads="+threads, func(t *testing.T) {
					assertRunAsExpected(t, spec, runPts(t, path, "PTS_THREADS="+threads))
				})
			}
		})
	}
}

func assertRunAsExpected(t *testing.T, spec testSpec, got runResult) {
	t.Helper()
	assert.Equal(t, got.stdout, *spec.stdout)
	assert.Equal(t, got.exitCode, spec.exitCode, "stderr:\n%s", got.stderr)
	for _, want := range spec.stderr {
		assert.Assert(t, strings.Contains(got.stderr, want), "stderr lacks %q:\n%s", want, got.stderr)
	}
}

// TestRunMatchesNode is the differential test (D-O04): a plain-TypeScript program
// must print exactly what Node prints and exit with the same code.
func TestRunMatchesNode(t *testing.T) {
	t.Parallel()
	node := findNode(t)
	for _, path := range runTestFiles(t) {
		t.Run(testName(path), func(t *testing.T) {
			t.Parallel()
			assertMatchesNode(t, node, path)
		})
	}
}

// runTestFiles are the programs that run as tests: run/*.pts and the docs' samples.
func runTestFiles(t *testing.T) []string {
	return append(testFiles(t, "run"), testFiles(t, docExamples)...)
}

// TestCorpusMatchesNode is the differential test over the corpus of realistic
// programs, corpus/<theme>/*.pts (M3.7), which expect only what Node prints.
func TestCorpusMatchesNode(t *testing.T) {
	t.Parallel()
	node := findNode(t)
	for _, path := range testFiles(t, filepath.Join("corpus", "*")) {
		t.Run(filepath.Base(filepath.Dir(path))+"/"+testName(path), func(t *testing.T) {
			t.Parallel()
			assertMatchesNode(t, node, path)
		})
	}
}

func assertMatchesNode(t *testing.T, node string, path string) {
	t.Helper()
	spec := ensureSpec(t, path)
	modules, err := testModules(path)
	assert.NilError(t, err)
	switch {
	case spec.noDiff != "":
		t.Skip("no-diff: " + spec.noDiff)
	case !isPlainTypeScript(strings.Join(slices.Collect(maps.Values(modules)), "\n")):
		t.Skip("uses ParallelScript features")
	}
	want := runNode(t, node, modules, filepath.Base(path))
	got := runPts(t, path)
	assert.Equal(t, got.stdout, want.stdout, "pts and Node print different output")
	assert.Equal(t, got.exitCode, want.exitCode, "pts and Node exit differently; pts stderr:\n%s\nNode stderr:\n%s", got.stderr, want.stderr)
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
	name := strings.TrimSuffix(filepath.Base(path), ".pts")
	if filepath.Base(filepath.Dir(path)) == "examples" {
		return "docs/" + name // a doc's sample, named apart from run/'s tests
	}
	return name
}

// testModules reads the sources of the program a test builds: the test file and, for a
// test with modules of its own to import, every .pts file under the directory named
// after it (run/name.pts imports from run/name/). Keys are paths relative to the test's directory.
func testModules(path string) (map[string]string, error) {
	dir := filepath.Dir(path)
	source, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	modules := map[string]string{filepath.Base(path): string(source)}
	helpers := strings.TrimSuffix(path, ".pts")
	if _, err := os.Stat(helpers); errors.Is(err, fs.ErrNotExist) {
		return modules, nil
	}
	err = filepath.WalkDir(helpers, func(file string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() || filepath.Ext(file) != ".pts" {
			return err
		}
		source, err := os.ReadFile(file)
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(dir, file)
		modules[relative] = string(source)
		return err
	})
	return modules, err
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

// runPts builds the program at path (once per test run, shared by the tests that need it)
// and runs it, with env's "NAME=value" settings added to its environment.
func runPts(t *testing.T, path string, env ...string) runResult {
	t.Helper()
	entry, _ := builds.LoadOrStore(path, &programBuild{})
	build := entry.(*programBuild)
	build.once.Do(func() {
		heavyJobs <- struct{}{}
		defer func() { <-heavyJobs }()
		build.exe = filepath.Join(buildDir, filepath.Base(filepath.Dir(path))+"_"+filepath.Base(path)) // corpus themes reuse names
		build.err = driver.Build(context.Background(), path, build.exe)
	})
	assert.NilError(t, build.err)
	return execute(t, filepath.Dir(path), env, build.exe)
}

// runNode runs a program under Node with TypeScript support (D-O04, D-009). modules maps
// relative .pts paths to sources, and entry is the one to run. Each module is written as
// an ES module (.mts), and its relative imports are renamed to match.
func runNode(t *testing.T, node string, modules map[string]string, entry string) runResult {
	t.Helper()
	dir := t.TempDir()
	for path, source := range modules {
		file := filepath.Join(dir, nodeModulePath(path))
		assert.NilError(t, os.MkdirAll(filepath.Dir(file), 0o755))
		assert.NilError(t, os.WriteFile(file, []byte(nodeModuleSource(source)), 0o644))
	}
	return execute(t, dir, nil, node, "--no-warnings", "--experimental-transform-types", filepath.Join(dir, nodeModulePath(entry)))
}

// relativeSpecifierPattern finds a relative module specifier after `from` or `import`.
var relativeSpecifierPattern = regexp.MustCompile(`(\b(?:from|import)\s*\(?\s*["'])(\.\.?/[^"'\n]*)(["'])`)

// nodeModuleSource renames the relative imports of a module to the .mts files runNode
// writes (Node needs explicit extensions and doesn't know .pts), and its pts: imports
// to Node's modules (nodeModules).
func nodeModuleSource(source string) string {
	source = relativeSpecifierPattern.ReplaceAllStringFunc(source, func(match string) string {
		parts := relativeSpecifierPattern.FindStringSubmatch(match)
		return parts[1] + nodeModulePath(parts[2]) + parts[3]
	})
	return ptsModulePattern.ReplaceAllString(source, "${1}node:${2}${3}")
}

// nodeModulePath is the .mts path runNode writes a module (or its specifier, extension optional) to.
func nodeModulePath(path string) string {
	return strings.TrimSuffix(path, ".pts") + ".mts"
}

// testTimeZone is the local time zone of every program the tests run, under pts and
// Node alike, so that local times print the same on every machine and cross DST (D-087).
const testTimeZone = "America/New_York"

func execute(t *testing.T, dir string, env []string, name string, args ...string) runResult {
	t.Helper()
	heavyJobs <- struct{}{}
	defer func() { <-heavyJobs }()
	ctx, cancel := context.WithTimeout(t.Context(), programTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Env = append(append(os.Environ(), "TZ="+testTimeZone), env...)
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
