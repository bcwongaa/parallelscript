package main

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/microsoft/typescript-go/pts/toolchain"
	"gotest.tools/v3/assert"
)

const helloSource = "console.log(\"Hello, ParallelScript\");\n"

type result struct {
	code           int
	stdout, stderr string
}

// runIn runs the CLI in-process with dir as its working directory.
func runIn(t *testing.T, dir string, args ...string) result {
	t.Helper()
	var stdout, stderr bytes.Buffer
	c := &cli{stdin: strings.NewReader(""), stdout: &stdout, stderr: &stderr, cwd: dir}
	code := c.run(t.Context(), args)
	return result{code: code, stdout: stdout.String(), stderr: stderr.String()}
}

// projectWith writes files into a fresh directory and returns it.
func projectWith(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		assert.NilError(t, os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644))
	}
	return dir
}

func output(t *testing.T, exe string) string {
	t.Helper()
	out, err := exec.CommandContext(t.Context(), exe).Output()
	assert.NilError(t, err)
	return string(out)
}

func TestRunPrintsHelloWorld(t *testing.T) {
	t.Parallel()
	dir := projectWith(t, map[string]string{"hello.pts": helloSource})

	got := runIn(t, dir, "run", "hello.pts")

	assert.Equal(t, got, result{code: 0, stdout: "Hello, ParallelScript\n"})
}

func TestBuildWritesExecutableNamedAfterTheSource(t *testing.T) {
	t.Parallel()
	dir := projectWith(t, map[string]string{"hello.pts": helloSource})

	got := runIn(t, dir, "build", "hello.pts")

	assert.Equal(t, got, result{code: 0})
	assert.Equal(t, output(t, filepath.Join(dir, "hello")), "Hello, ParallelScript\n")
}

func TestBuildAcceptsOutputBeforeOrAfterTheSource(t *testing.T) {
	t.Parallel()
	dir := projectWith(t, map[string]string{"hello.pts": helloSource})

	assert.Equal(t, runIn(t, dir, "build", "hello.pts", "-o", "after"), result{code: 0})
	assert.Equal(t, runIn(t, dir, "build", "--output=before", "hello.pts"), result{code: 0})

	assert.Equal(t, output(t, filepath.Join(dir, "after")), "Hello, ParallelScript\n")
	assert.Equal(t, output(t, filepath.Join(dir, "before")), "Hello, ParallelScript\n")
}

func TestEmitCPrintsCWithLineDirectives(t *testing.T) {
	t.Parallel()
	dir := projectWith(t, map[string]string{"hello.pts": helloSource})

	got := runIn(t, dir, "emit-c", "hello.pts")

	assert.Equal(t, got.code, 0, got.stderr)
	assert.Assert(t, strings.Contains(got.stdout, `#line 1 "`+filepath.Join(dir, "hello.pts")+`"`), got.stdout)
	assert.Assert(t, strings.Contains(got.stdout, "pts_console_log("), got.stdout)
}

func TestEmitCWritesToAFileWithOutput(t *testing.T) {
	t.Parallel()
	dir := projectWith(t, map[string]string{"hello.pts": helloSource})

	got := runIn(t, dir, "emit-c", "hello.pts", "-o", "hello.c")

	assert.Equal(t, got, result{code: 0})
	c, err := os.ReadFile(filepath.Join(dir, "hello.c"))
	assert.NilError(t, err)
	assert.Assert(t, strings.Contains(string(c), `#include "pts.h"`))
}

func TestCompileErrorsArePrintedRelativeToTheWorkingDirectory(t *testing.T) {
	t.Parallel()
	dir := projectWith(t, map[string]string{"bad.pts": "const n: number = \"one\";\nfor (const c of \"ab\") {}\n"})

	got := runIn(t, dir, "run", "bad.pts")

	assert.Equal(t, got, result{code: exitFailure, stderr: "bad.pts:1:7 - error TS2322: Type 'string' is not assignable to type 'number'.\n"})
}

func TestUnsupportedSyntaxIsACompileError(t *testing.T) {
	t.Parallel()
	dir := projectWith(t, map[string]string{"loop.pts": "for (const c in { a: 1 }) {}\n"})

	got := runIn(t, dir, "build", "loop.pts")

	assert.Equal(t, got, result{code: exitFailure, stderr: "loop.pts:1:1 - error PTS1001: for in statement is not supported yet\n"})
	_, err := os.Stat(filepath.Join(dir, "loop"))
	assert.Assert(t, os.IsNotExist(err), "no executable on failure")
}

func TestMissingSourceFileIsReported(t *testing.T) {
	t.Parallel()
	got := runIn(t, t.TempDir(), "run", "missing.pts")

	assert.Equal(t, got.code, exitFailure)
	assert.Assert(t, strings.Contains(got.stderr, "TS6053"), got.stderr)
}

func TestMalformedCommandLinesPrintUsage(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{{}, {"frob"}, {"build"}, {"build", "a.pts", "b.pts"}, {"build", "a.pts", "-o"},
		{"emit-c", "--fast", "a.pts"}, {"run"}, {"run", "-x", "a.pts"}} {
		got := runIn(t, t.TempDir(), args...)
		assert.Equal(t, got.code, exitUsage, "args %q", args)
		assert.Assert(t, strings.Contains(got.stderr, "Usage: pts"), "args %q: %s", args, got.stderr)
	}
}

func TestHelpPrintsUsageToStdout(t *testing.T) {
	t.Parallel()
	got := runIn(t, t.TempDir(), "help")

	assert.Equal(t, got, result{code: 0, stdout: usage})
}

func TestBinaryRunsProgramsAndFailsOnCompileErrors(t *testing.T) {
	t.Parallel()
	pts := filepath.Join(t.TempDir(), "pts")
	build := exec.CommandContext(t.Context(), "go", "build", "-o", pts, ".")
	out, err := build.CombinedOutput()
	assert.NilError(t, err, string(out))
	home, err := toolchain.FindHome()
	assert.NilError(t, err)
	dir := projectWith(t, map[string]string{"hello.pts": helloSource, "bad.pts": "for (const c of \"ab\") {}\n"})
	// Like an installed pts, the binary is outside the source tree and finds the runtime through PTS_HOME.
	command := func(args ...string) *exec.Cmd {
		cmd := exec.CommandContext(t.Context(), pts, args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "PTS_HOME="+home)
		return cmd
	}

	out, err = command("run", "hello.pts").Output()
	assert.NilError(t, err)
	assert.Equal(t, string(out), "Hello, ParallelScript\n")

	err = command("run", "bad.pts").Run()
	var exitErr *exec.ExitError
	assert.Assert(t, errors.As(err, &exitErr), "%v", err)
	assert.Equal(t, exitErr.ExitCode(), exitFailure)
}

func TestExecutePassesThroughTheProgramsExitCode(t *testing.T) {
	t.Parallel()
	c := &cli{stdin: strings.NewReader(""), stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}, cwd: t.TempDir()}

	assert.Equal(t, c.execute("/bin/sh", []string{"-c", "exit 3"}), 3)
	assert.Equal(t, c.execute("/bin/sh", []string{"-c", "kill -TERM $$"}), exitSignalBase+15)
}
