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
	"strings"
	"syscall"

	"github.com/microsoft/typescript-go/internal/vfs/osvfs"
	"github.com/microsoft/typescript-go/pts/diagnostic"
	"github.com/microsoft/typescript-go/pts/driver"
	"github.com/microsoft/typescript-go/pts/lsp"
)

const (
	exitFailure = 1 // compile errors, or the toolchain failed
	exitUsage   = 2
	// exitSignalBase plus the signal number is the exit code of a program killed by a signal, as in shells.
	exitSignalBase = 128
)

const usage = `Usage: pts <command> [arguments]

Commands:
  build <file.pts> [-o <output>]   Compile to a native executable (default output: the file's base name)
  run <file.pts> [args...]         Compile and run; arguments after the file go to the program
  emit-c <file.pts> [-o <file.c>]  Print the generated C (or write it to a file)
  lsp [--stdio]                    Run the language server for editors, over stdin and stdout
  help                             Show this help
`

// cli is the command-line interface, with its environment injected so tests can run it in-process.
type cli struct {
	stdin          io.Reader
	stdout, stderr io.Writer
	cwd            string // relative paths in arguments are relative to this
}

// usageError is a malformed command line; it prints with the usage text and exits with exitUsage.
type usageError struct{ message string }

func (e *usageError) Error() string { return e.message }

// run executes one pts command and returns the process exit code.
func (c *cli) run(ctx context.Context, args []string) int {
	if len(args) == 0 {
		return c.fail(&usageError{"no command given"})
	}
	command, rest := args[0], args[1:]
	switch command {
	case "build":
		return c.result(c.build(ctx, rest))
	case "emit-c":
		return c.result(c.emitC(ctx, rest))
	case "run":
		return c.runProgram(ctx, rest)
	case "lsp":
		return c.result(c.languageServer(ctx, rest))
	case "help", "-h", "--help":
		fmt.Fprint(c.stdout, usage)
		return 0
	}
	return c.fail(&usageError{fmt.Sprintf("unknown command %q", command)})
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
		fmt.Fprintf(c.stderr, "pts: %s\n\n%s", usageErr.message, usage)
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
				return "", "", &usageError{arg + " needs a value"}
			}
			i++
			output = args[i]
		case strings.HasPrefix(arg, "-o="), strings.HasPrefix(arg, "--output="):
			output = arg[strings.IndexByte(arg, '=')+1:]
		case strings.HasPrefix(arg, "-") && arg != "-":
			return "", "", &usageError{fmt.Sprintf("unknown option %q for %s", arg, command)}
		default:
			files = append(files, arg)
		}
	}
	if len(files) != 1 {
		return "", "", &usageError{command + " needs exactly one source file"}
	}
	return files[0], output, nil
}

// runProgram builds the program into a temporary directory and runs it with the
// remaining arguments, returning its exit code.
func (c *cli) runProgram(ctx context.Context, args []string) int {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return c.fail(&usageError{"run needs a source file first"})
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
			return &usageError{fmt.Sprintf("unknown option %q for lsp", arg)}
		}
	}
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	return lsp.Run(ctx, c.stdin, c.stdout, c.stderr, c.cwd, osvfs.FS())
}
