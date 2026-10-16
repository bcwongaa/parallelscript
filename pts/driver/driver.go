// Package driver runs the whole compiler pipeline: load and type-check the program,
// run the semantic checks, lower it to IR, emit C and compile that with clang
// against the runtime.
package driver

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strconv"

	"github.com/microsoft/typescript-go/internal/vfs/osvfs"
	"github.com/microsoft/typescript-go/pts/cgen"
	"github.com/microsoft/typescript-go/pts/check"
	"github.com/microsoft/typescript-go/pts/diagnostic"
	"github.com/microsoft/typescript-go/pts/frontend"
	"github.com/microsoft/typescript-go/pts/lower"
	"github.com/microsoft/typescript-go/pts/toolchain"
)

// EmitC compiles the program whose entry module is the file at entry to C.
// Compile errors come back as a *diagnostic.Error, and a bug the compiler ran into
// as a *diagnostic.InternalError (D-179).
func EmitC(ctx context.Context, entry string) (code string, err error) {
	defer diagnostic.Contain(&err)
	entry, err = filepath.Abs(entry)
	if err != nil {
		return "", err
	}
	program, diagnostics := frontend.Load(ctx, osvfs.FS(), entry)
	if len(diagnostics) > 0 {
		return "", &diagnostic.Error{Diagnostics: diagnostics}
	}
	info, diagnostics := check.Program(ctx, program)
	if len(diagnostics) > 0 {
		return "", &diagnostic.Error{Diagnostics: diagnostics}
	}
	lowered, diagnostics := lower.Program(ctx, program, info)
	if len(diagnostics) > 0 {
		return "", &diagnostic.Error{Diagnostics: diagnostics}
	}
	return cgen.Emit(lowered), nil
}

// Build compiles the program whose entry module is the file at entry into a native
// executable at output. Compile errors come back as a *diagnostic.Error, and a bug the
// compiler ran into, C that the C compiler rejects included, as a *diagnostic.InternalError.
func Build(ctx context.Context, entry string, output string) error {
	c, err := EmitC(ctx, entry)
	if err != nil {
		return err
	}
	tc, err := toolchain.New()
	if err != nil {
		return err
	}
	rt, err := tc.Runtime(ctx, toolchain.Release)
	if err != nil {
		return err
	}
	dir, err := os.MkdirTemp("", "pts-build-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	cFile := filepath.Join(dir, "main.c")
	if err := os.WriteFile(cFile, []byte(c), 0o644); err != nil {
		return err
	}
	output, err = filepath.Abs(output)
	if err != nil {
		return err
	}
	if err := tc.CompileProgram(ctx, rt, []string{cFile}, output); err != nil {
		return generatedCFailure(err, cFile)
	}
	return nil
}

// cCompilerError is the C compiler's first error, "file:line:column: error: message",
// without the warning flag that may follow the message.
var cCompilerError = regexp.MustCompile(`(?m)^(.*):(\d+):\d+: error: (.*?)(?: \[-W[^\]]*\])?$`)

// undefinedReference is the linker's error for a function that the C calls and nothing defines.
var undefinedReference = regexp.MustCompile("undefined reference to `[^']*'")

// archiveMember is the linker naming an object of an archive as where a reference comes
// from (`…/libptsrt.a(008-runtime.o): in function …`): the runtime's, not the generated C's.
var archiveMember = regexp.MustCompile(`\.a\([^)]*\): in function`)

// generatedCFailure reports the C compiler rejecting the C generated into cFile, or the
// linker the functions it calls, as the bug in the compiler that it is: an internal
// error at the source line that the C compiler's first error has through #line (D-179).
// Other failures (the C compiler killed, a full disk, a runtime archive missing what it
// calls) are returned as they are.
func generatedCFailure(err error, cFile string) error {
	var command *toolchain.CommandError
	if !errors.As(err, &command) {
		return err
	}
	at := diagnostic.Diagnostic{Code: diagnostic.Internal}
	if match := cCompilerError.FindStringSubmatch(command.Output); match != nil {
		at.Message = "internal compiler error: the generated C doesn't compile: " + match[3]
		if match[1] != cFile { // a line of the source, not of declarations the C begins with
			at.File = match[1]
			at.Line, _ = strconv.Atoi(match[2])
		}
	} else if reference := undefinedReference.FindString(command.Output); reference != "" && !archiveMember.MatchString(command.Output) {
		at.Message = "internal compiler error: the generated C doesn't link: " + reference
	} else {
		return err
	}
	return &diagnostic.InternalError{Diagnostic: at, Detail: command.Error()}
}
