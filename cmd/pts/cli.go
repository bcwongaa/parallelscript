package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"strings"
	"syscall"

	"github.com/microsoft/typescript-go/internal/core"
	"github.com/microsoft/typescript-go/internal/vfs/osvfs"
	"github.com/microsoft/typescript-go/pts/diagnostic"
	"github.com/microsoft/typescript-go/pts/driver"
	"github.com/microsoft/typescript-go/pts/lsp"
)

// version is ParallelScript's, the milestone it reached as the minor version.
const version = "0.7.0"

// commitDigits is how much of a commit's hash the version shows, as git's short hashes do.
const commitDigits = 9

const (
	exitFailure = 1 // compile errors, or the toolchain failed
	exitUsage   = 2
	// exitSignalBase plus the signal number is the exit code of a program killed by a signal, as in shells.
	exitSignalBase = 128
)

const usage = `pts compiles ParallelScript (TypeScript with real parallelism) to native executables.

Usage: pts <command> [arguments]

Commands:
  run <file.pts> [args...]         Compile and run; arguments after the file go to the program
  build <file.pts> [-o <output>]   Compile to a native executable (default output: the file's base name)
  emit-c <file.pts> [-o <file.c>]  Print the generated C (or write it to a file)
  test [paths...]                  Build and run the test files (name.test.pts, …) under paths
  lsp [--stdio]                    Run the language server for editors, over stdin and stdout
  version                          Print pts's version
  help [<command>]                 Show this help, or a command's

Environment:
  PTS_THREADS=<n>   Event loops and pool threads a compiled program uses (default: its cores)
  PTS_HOME=<dir>    The repository root, where pts finds the runtime's sources
                    (default: the nearest one above pts or the working directory)
  PTS_CACHE_DIR     Where the built runtime is cached (default: ~/.cache/pts)
  PTS_CC, PTS_AR    The C compiler and archiver (default: clang and ar)

Exit codes: 0 on success, 1 for compile errors, 2 for a malformed command line;
` + "`pts run`" + ` exits with the program's code. The guide is docs/GUIDE.md in the repository.
`

// commandUsage is each command's own help, for ` + "`pts help <command>`" + ` and ` + "`pts <command> --help`" + `.
var commandUsage = map[string]string{
	"run": `Usage: pts run <file.pts> [args...]

Compiles the program whose entry module is file.pts, with the modules it imports, and
runs it. The arguments after the file go to the program (process.argv); pts exits with
the program's exit code, or 128 plus the signal's number if a signal ended it.
`,
	"build": `Usage: pts build <file.pts> [-o <output>]

Compiles the program whose entry module is file.pts, with the modules it imports, to a
native executable that needs only libc and libm.

Options:
  -o, --output <output>   The executable's path (default: the file's base name)
`,
	"emit-c": `Usage: pts emit-c <file.pts> [-o <file.c>]

Prints the C that pts compiles the program to. Its #line directives point at the .pts
lines, so debuggers and crash backtraces show those.

Options:
  -o, --output <file.c>   Write the C to a file instead
`,
	"test": `Usage: pts test [paths...]

Builds and runs each test file under paths (by default the working directory): files
named as paths, and in directories, searched recursively past node_modules and dot
directories, the files named name.test.pts, name-test.pts, name_test.pts, test-name.pts
or test.pts. Test files use pts:test and pts:assert, which follow Node's node:test and
node:assert/strict. Each file runs on its own, one after another; pts test prints their
tests' results, then the totals and every failure, and exits with 1 if any test failed
or a file didn't compile or ended before its tests reported.
`,
	"lsp": `Usage: pts lsp [--stdio]

Runs the language server over stdin and stdout (the only transport) for an editor:
TypeScript's completions, hovers, definitions and errors for .pts files, with
ParallelScript's prelude, pts: modules and errors. pts/editors/vscode is VS Code's
extension for it.
`,
	"version": `Usage: pts version

Prints pts's version, the commit it was built from, and typescript-go's version.
`,
	"help": `Usage: pts help [<command>]

Shows pts's help, or a command's.
`,
}

// cli is the command-line interface, with its environment injected so tests can run it in-process.
type cli struct {
	stdin          io.Reader
	stdout, stderr io.Writer
	cwd            string // relative paths in arguments are relative to this
}

// usageError is a malformed command line; it prints with the usage text of its command
// (or pts's, if it names none) and exits with exitUsage.
type usageError struct{ command, message string }

func (e *usageError) Error() string { return e.message }

// run executes one pts command and returns the process exit code.
func (c *cli) run(ctx context.Context, args []string) int {
	if len(args) == 0 {
		return c.fail(&usageError{message: "no command given"})
	}
	command, rest := args[0], args[1:]
	if wantsHelp(command, rest) {
		fmt.Fprint(c.stdout, commandUsage[command])
		return 0
	}
	switch command {
	case "build":
		return c.result(c.build(ctx, rest))
	case "emit-c":
		return c.result(c.emitC(ctx, rest))
	case "run":
		return c.runProgram(ctx, rest)
	case "test":
		return c.test(ctx, rest)
	case "lsp":
		return c.result(c.languageServer(ctx, rest))
	case "version", "--version":
		fmt.Fprintln(c.stdout, versionText())
		return 0
	case "help", "-h", "--help":
		return c.help(rest)
	}
	return c.fail(&usageError{message: fmt.Sprintf("unknown command %q", command)})
}

// wantsHelp reports whether a command's arguments ask for its help: -h or --help where an
// option may go (for run, before the file, as later ones are the program's).
func wantsHelp(command string, args []string) bool {
	if commandUsage[command] == "" || command == "help" {
		return false
	}
	for _, arg := range args {
		if arg == "-h" || arg == "--help" {
			return true
		}
		if command == "run" && !strings.HasPrefix(arg, "-") {
			return false
		}
	}
	return false
}

// help prints the usage, or the usage of the command args names.
func (c *cli) help(args []string) int {
	switch {
	case len(args) == 0:
		fmt.Fprint(c.stdout, usage)
	case len(args) == 1 && commandUsage[args[0]] != "":
		fmt.Fprint(c.stdout, commandUsage[args[0]])
	default:
		return c.fail(&usageError{"help", fmt.Sprintf("no help for %q", strings.Join(args, " "))})
	}
	return 0
}

// versionText is pts's version with the commit it was built from, when Go recorded it.
func versionText() string {
	commit := ""
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, setting := range info.Settings {
			switch {
			case setting.Key == "vcs.revision" && len(setting.Value) >= commitDigits:
				commit = setting.Value[:commitDigits]
			case setting.Key == "vcs.modified" && setting.Value == "true" && commit != "":
				commit += ", modified"
			}
		}
	}
	if commit == "" {
		return fmt.Sprintf("pts %s (typescript-go %s)", version, core.Version())
	}
	return fmt.Sprintf("pts %s (commit %s; typescript-go %s)", version, commit, core.Version())
}

func (c *cli) result(err error) int {
	if err != nil {
		return c.fail(err)
	}
	return 0
}

// fail reports err the way its kind asks for and returns the matching exit code.
func (c *cli) fail(err error) int {
	var usageErr *usageError
	var compileErr *diagnostic.Error
	switch {
	case errors.As(err, &usageErr):
		text := usage
		if commandUsage[usageErr.command] != "" {
			text = commandUsage[usageErr.command]
		}
		fmt.Fprintf(c.stderr, "pts: %s\n\n%s", usageErr.message, text)
		return exitUsage
	case errors.As(err, &compileErr):
		for _, d := range compileErr.Diagnostics {
			fmt.Fprintln(c.stderr, d.Format(c.cwd))
		}
		return exitFailure
	default:
		fmt.Fprintf(c.stderr, "pts: %s\n", err)
		return exitFailure
	}
}

func (c *cli) path(p string) string {
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(c.cwd, p)
}

func (c *cli) build(ctx context.Context, args []string) error {
	source, output, err := parseFileAndOutput("build", args)
	if err != nil {
		return err
	}
	if output == "" {
		output = strings.TrimSuffix(filepath.Base(source), filepath.Ext(source))
	}
	return driver.Build(ctx, c.path(source), c.path(output))
}

func (c *cli) emitC(ctx context.Context, args []string) error {
	source, output, err := parseFileAndOutput("emit-c", args)
	if err != nil {
		return err
	}
	code, err := driver.EmitC(ctx, c.path(source))
	if err != nil {
		return err
	}
	if output == "" {
		_, err = io.WriteString(c.stdout, code)
		return err
	}
	return os.WriteFile(c.path(output), []byte(code), 0o644)
}

// parseFileAndOutput reads "<file> [-o <output>]" with the option in any position.
func parseFileAndOutput(command string, args []string) (source string, output string, err error) {
	var files []string
	for i := 0; i < len(args); i++ {
		switch arg := args[i]; {
		case arg == "-o" || arg == "--output":
			if i+1 == len(args) {
				return "", "", &usageError{command, arg + " needs a value"}
			}
			i++
			output = args[i]
		case strings.HasPrefix(arg, "-o="), strings.HasPrefix(arg, "--output="):
			output = arg[strings.IndexByte(arg, '=')+1:]
		case strings.HasPrefix(arg, "-") && arg != "-":
			return "", "", &usageError{command, fmt.Sprintf("unknown option %q for %s", arg, command)}
		default:
			files = append(files, arg)
		}
	}
	if len(files) != 1 {
		return "", "", &usageError{command, command + " needs exactly one source file"}
	}
	return files[0], output, nil
}

// runProgram builds the program into a temporary directory and runs it with the
// remaining arguments, returning its exit code.
func (c *cli) runProgram(ctx context.Context, args []string) int {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return c.fail(&usageError{"run", "run needs a source file first"})
	}
	dir, err := os.MkdirTemp("", "pts-run-")
	if err != nil {
		return c.fail(err)
	}
	defer os.RemoveAll(dir)
	exe := filepath.Join(dir, strings.TrimSuffix(filepath.Base(args[0]), filepath.Ext(args[0])))
	if err := driver.Build(ctx, c.path(args[0]), exe); err != nil {
		return c.fail(err)
	}
	return c.execute(exe, args[1:])
}

// execute runs exe with the CLI's stdio, forwarding interrupts to it rather than
// dying first, so the temporary build is still cleaned up.
func (c *cli) execute(exe string, args []string) int {
	cmd := exec.Command(exe, args...)
	cmd.Dir = c.cwd
	cmd.Stdin, cmd.Stdout, cmd.Stderr = c.stdin, c.stdout, c.stderr
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signals)
	if err := cmd.Start(); err != nil {
		return c.fail(err)
	}
	done := make(chan struct{})
	defer close(done)
	go func() {
		for {
			select {
			case sig := <-signals:
				cmd.Process.Signal(sig)
			case <-done:
				return
			}
		}
	}()
	err := cmd.Wait()
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		if status, ok := exitErr.Sys().(syscall.WaitStatus); ok && status.Signaled() {
			return exitSignalBase + int(status.Signal())
		}
		return exitErr.ExitCode()
	}
	if err != nil {
		return c.fail(err)
	}
	return 0
}

// languageServer serves an editor over stdin and stdout until it exits (D-168).
func (c *cli) languageServer(ctx context.Context, args []string) error {
	for _, arg := range args {
		if arg != "--stdio" { // stdio is the only transport, and the default
			return &usageError{"lsp", fmt.Sprintf("unknown option %q for lsp", arg)}
		}
	}
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	return lsp.Run(ctx, c.stdin, c.stdout, c.stderr, c.cwd, osvfs.FS())
}
