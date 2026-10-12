package main

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
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
	dir := projectWith(t, map[string]string{"debug.pts": "debugger;\n"})

	got := runIn(t, dir, "build", "debug.pts")

	assert.Equal(t, got, result{code: exitFailure, stderr: "debug.pts:1:1 - error PTS1001: debugger statement is not supported yet\n"})
	_, err := os.Stat(filepath.Join(dir, "debug"))
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

func TestEachCommandHasItsOwnHelp(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{{"help", "build"}, {"build", "--help"}, {"build", "a.pts", "-h"}} {
		assert.Equal(t, runIn(t, t.TempDir(), args...), result{code: 0, stdout: commandUsage["build"]})
	}
	assert.Equal(t, runIn(t, t.TempDir(), "run", "--help"), result{code: 0, stdout: commandUsage["run"]})
	for command := range commandUsage {
		assert.Assert(t, strings.Contains(usage, "\n  "+command+" "), "pts's usage doesn't list %s", command)
	}
}

// After `run`'s source file, --help is the program's argument.
func TestHelpAfterRunsFileGoesToTheProgram(t *testing.T) {
	t.Parallel()
	dir := projectWith(t, map[string]string{"args.pts": "console.log(process.argv.slice(2));\n"})
	got := runIn(t, dir, "run", "args.pts", "--help")
	assert.Equal(t, got.code, 0, got.stderr)
	assert.Equal(t, got.stdout, "[ '--help' ]\n")
}

func TestUsageErrorsShowTheirCommandsUsage(t *testing.T) {
	t.Parallel()
	got := runIn(t, t.TempDir(), "emit-c")
	assert.Equal(t, got, result{code: exitUsage, stderr: "pts: emit-c needs exactly one source file\n\n" + commandUsage["emit-c"]})
	got = runIn(t, t.TempDir(), "help", "frob")
	assert.Equal(t, got, result{code: exitUsage, stderr: "pts: no help for \"frob\"\n\n" + commandUsage["help"]})
}

func TestVersionNamesPtsAndTypeScriptGo(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{{"version"}, {"--version"}} {
		got := runIn(t, t.TempDir(), args...)
		assert.Equal(t, got.code, 0)
		assert.Assert(t, strings.HasPrefix(got.stdout, "pts "+version+" (") && strings.HasSuffix(got.stdout, "typescript-go 7.0.2)\n"), got.stdout)
	}
}

func TestBinaryRunsProgramsAndFailsOnCompileErrors(t *testing.T) {
	t.Parallel()
	pts := filepath.Join(t.TempDir(), "pts")
	build := exec.CommandContext(t.Context(), "go", "build", "-o", pts, ".")
	out, err := build.CombinedOutput()
	assert.NilError(t, err, string(out))
	home, err := toolchain.FindHome()
	assert.NilError(t, err)
	dir := projectWith(t, map[string]string{"hello.pts": helloSource, "bad.pts": "const weak = new WeakMap<object, number>();\n"})
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

// frame writes a JSON-RPC message as the language server protocol frames it.
func frame(message string) string {
	return "Content-Length: " + strconv.Itoa(len(message)) + "\r\n\r\n" + message
}

// readFrame reads one framed message.
func readFrame(t *testing.T, r *bufio.Reader) string {
	t.Helper()
	header, err := r.ReadString('\n')
	assert.NilError(t, err)
	length, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(header, "Content-Length:")))
	assert.NilError(t, err, header)
	_, err = r.ReadString('\n') // the blank line ending the header
	assert.NilError(t, err)
	body := make([]byte, length)
	_, err = io.ReadFull(r, body)
	assert.NilError(t, err)
	return string(body)
}

// `pts lsp` serves the language server protocol over stdin and stdout: it answers
// initialize with its capabilities and ends after shutdown and exit, with exit code 0.
func TestLSPServesOverStdio(t *testing.T) {
	t.Parallel()
	stdinReader, stdin := io.Pipe()
	stdoutReader, stdout := io.Pipe()
	c := &cli{stdin: stdinReader, stdout: stdout, stderr: io.Discard, cwd: t.TempDir()}
	exited := make(chan int, 1)
	go func() {
		exited <- c.run(t.Context(), []string{"lsp", "--stdio"})
		stdout.Close()
	}()
	send := func(message string) {
		_, err := io.WriteString(stdin, frame(message))
		assert.NilError(t, err)
	}
	messages := bufio.NewReader(stdoutReader)
	send(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"processId":null,"rootUri":null,"capabilities":{}}}`)
	for {
		message := readFrame(t, messages)
		if strings.Contains(message, `"id":1`) {
			assert.Assert(t, strings.Contains(message, `"result":{"capabilities":`), message)
			break
		}
	}
	go func() { _, _ = io.Copy(io.Discard, messages) }()
	send(`{"jsonrpc":"2.0","id":2,"method":"shutdown"}`)
	send(`{"jsonrpc":"2.0","method":"exit"}`)
	stdin.Close() // as a client does once it has told the server to exit
	assert.Equal(t, <-exited, 0)

	r := runIn(t, t.TempDir(), "lsp", "--socket")
	assert.Equal(t, r.code, exitUsage)
	assert.Assert(t, strings.Contains(r.stderr, `unknown option "--socket" for lsp`), r.stderr)
}
