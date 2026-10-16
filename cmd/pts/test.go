package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/microsoft/typescript-go/pts/diagnostic"
	"github.com/microsoft/typescript-go/pts/driver"
)

// `pts test` (D-171) builds every test file it finds, runs each in turn with
// PTS_TEST_RESULTS naming a file for pts:test's counts and failures, and prints the
// totals and the failures of all of them, as Node's spec reporter does for `node --test`.

// maxParallelBuilds bounds the test files compiled at once (each build runs clang).
const maxParallelBuilds = 4

// testFileSuffixes end the names `pts test` takes for test files when it searches a
// directory, as do test-name.pts and test.pts (isTestFileName), after Node's patterns.
var testFileSuffixes = []string{".test.pts", "-test.pts", "_test.pts"}

// testResults is what pts:test writes to PTS_TEST_RESULTS.
type testResults struct {
	Tests    int      `json:"tests"`
	Suites   int      `json:"suites"`
	Pass     int      `json:"pass"`
	Fail     int      `json:"fail"`
	Skipped  int      `json:"skipped"`
	Todo     int      `json:"todo"`
	Failures []string `json:"failures"`
}

func (r *testResults) add(other testResults) {
	r.Tests += other.Tests
	r.Suites += other.Suites
	r.Pass += other.Pass
	r.Fail += other.Fail
	r.Skipped += other.Skipped
	r.Todo += other.Todo
	r.Failures = append(r.Failures, other.Failures...)
}

func (c *cli) test(ctx context.Context, args []string) int {
	files, err := c.findTestFiles(args)
	if err != nil {
		return c.fail(err)
	}
	dir, err := os.MkdirTemp("", "pts-test-")
	if err != nil {
		return c.fail(err)
	}
	defer os.RemoveAll(dir)

	started := time.Now()
	builds := c.buildTestFiles(ctx, files, dir)
	var total testResults
	for i, file := range files {
		total.add(c.runTestFile(file, builds[i], filepath.Join(dir, fmt.Sprintf("results-%d.json", i))))
	}
	c.printTestSummary(total, time.Since(started))
	if total.Fail > 0 {
		return exitFailure
	}
	return 0
}

// findTestFiles lists the test files under paths (the working directory if there are
// none): files named as paths, and the test files in directories, searched recursively
// past node_modules and dot directories. Sorted, each once.
func (c *cli) findTestFiles(paths []string) ([]string, error) {
	if len(paths) == 0 {
		paths = []string{"."}
	}
	var files []string
	for _, path := range paths {
		if strings.HasPrefix(path, "-") {
			return nil, &usageError{"test", fmt.Sprintf("unknown option %q for test", path)}
		}
		root := c.path(path)
		info, err := os.Stat(root)
		if err != nil {
			return nil, err
		}
		if !info.IsDir() {
			files = append(files, root)
			continue
		}
		err = filepath.WalkDir(root, func(file string, entry fs.DirEntry, err error) error {
			switch {
			case err != nil:
				return err
			case entry.IsDir() && file != root && (entry.Name() == "node_modules" || strings.HasPrefix(entry.Name(), ".")):
				return filepath.SkipDir
			case !entry.IsDir() && isTestFileName(entry.Name()):
				files = append(files, file)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	slices.Sort(files)
	files = slices.Compact(files)
	if len(files) == 0 {
		return nil, fmt.Errorf("no test files in %s (test files are named name.test.pts, name-test.pts, name_test.pts, test-name.pts or test.pts)", strings.Join(paths, ", "))
	}
	return files, nil
}

func isTestFileName(name string) bool {
	if name == "test.pts" || (strings.HasPrefix(name, "test-") && strings.HasSuffix(name, ".pts")) {
		return true
	}
	return slices.ContainsFunc(testFileSuffixes, func(suffix string) bool { return strings.HasSuffix(name, suffix) })
}

// testBuild is a test file's executable, or the error that kept it from being built.
type testBuild struct {
	exe string
	err error
}

// buildTestFiles compiles files into dir, a few at a time.
func (c *cli) buildTestFiles(ctx context.Context, files []string, dir string) []testBuild {
	builds := make([]testBuild, len(files))
	slots := make(chan struct{}, min(maxParallelBuilds, max(1, runtime.NumCPU()/2)))
	var wg sync.WaitGroup
	for i, file := range files {
		wg.Go(func() {
			slots <- struct{}{}
			defer func() { <-slots }()
			exe := filepath.Join(dir, fmt.Sprintf("test-%d", i))
			builds[i] = testBuild{exe: exe, err: driver.Build(ctx, file, exe)}
		})
	}
	wg.Wait()
	return builds
}

// runTestFile runs a built test file with its output going to the CLI's, and returns its
// results; a file that didn't build, or ended without reporting or with a failure exit
// code its tests don't account for, counts as one failure.
func (c *cli) runTestFile(file string, build testBuild, resultsPath string) testResults {
	name := c.relative(file)
	if build.err != nil {
		var compileErr *diagnostic.Error
		var internal *diagnostic.InternalError
		switch {
		case errors.As(build.err, &compileErr):
			for _, d := range compileErr.Diagnostics {
				fmt.Fprintln(c.stderr, d.Format(c.cwd))
			}
		case errors.As(build.err, &internal):
			c.reportInternalError(internal)
		}
		fmt.Fprintf(c.stdout, "✖ %s (it didn't compile)\n", name)
		return fileFailure(name, "it didn't compile: "+firstLine(build.err.Error()))
	}
	started := time.Now()
	cmd := exec.Command(build.exe)
	cmd.Dir = c.cwd
	cmd.Stdin, cmd.Stdout, cmd.Stderr = c.stdin, c.stdout, c.stderr
	cmd.Env = append(os.Environ(), "PTS_TEST_RESULTS="+resultsPath)
	runErr := cmd.Run()
	var results testResults
	data, readErr := os.ReadFile(resultsPath)
	if readErr == nil {
		readErr = json.Unmarshal(data, &results)
	}
	switch {
	case readErr != nil:
		fmt.Fprintf(c.stdout, "✖ %s (%s)\n", name, milliseconds(time.Since(started)))
		return fileFailure(name, "it ended before its tests reported: "+exitDescription(runErr))
	case runErr != nil && results.Fail == 0:
		results.add(fileFailure(name, "its tests passed, but it "+exitDescription(runErr)))
	}
	return results
}

func fileFailure(name string, why string) testResults {
	return testResults{Tests: 1, Fail: 1, Failures: []string{fmt.Sprintf("✖ %s\n  %s", name, why)}}
}

// exitDescription says how a test program ended, from the error cmd.Run returned.
func exitDescription(err error) string {
	var exitErr *exec.ExitError
	switch {
	case err == nil:
		return "exited with code 0"
	case errors.As(err, &exitErr) && exitErr.ExitCode() >= 0:
		return fmt.Sprintf("exited with code %d", exitErr.ExitCode())
	case errors.As(err, &exitErr):
		return "was killed by " + exitErr.String()
	}
	return err.Error()
}

func (c *cli) printTestSummary(total testResults, duration time.Duration) {
	fmt.Fprintf(c.stdout, "ℹ tests %d\nℹ suites %d\nℹ pass %d\nℹ fail %d\nℹ cancelled 0\nℹ skipped %d\nℹ todo %d\nℹ duration_ms %s\n",
		total.Tests, total.Suites, total.Pass, total.Fail, total.Skipped, total.Todo, strings.TrimSuffix(milliseconds(duration), "ms"))
	if len(total.Failures) > 0 {
		fmt.Fprintf(c.stdout, "\n✖ failing tests:\n\n%s\n", strings.Join(total.Failures, "\n\n"))
	}
}

func milliseconds(d time.Duration) string {
	return fmt.Sprintf("%.6gms", float64(d.Nanoseconds())/1e6)
}

func firstLine(text string) string {
	line, _, _ := strings.Cut(text, "\n")
	return line
}

// relative is path relative to the working directory, if it is below it.
func (c *cli) relative(path string) string {
	if rel, err := filepath.Rel(c.cwd, path); err == nil && !strings.HasPrefix(rel, "..") {
		return rel
	}
	return path
}
