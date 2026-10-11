// Package driver runs the whole compiler pipeline: load and type-check the program,
// run the semantic checks, lower it to IR, emit C and compile that with clang
// against the runtime.
package driver

import (
	"context"
	"os"
	"path/filepath"

	"github.com/microsoft/typescript-go/internal/vfs/osvfs"
	"github.com/microsoft/typescript-go/pts/cgen"
	"github.com/microsoft/typescript-go/pts/check"
	"github.com/microsoft/typescript-go/pts/diagnostic"
	"github.com/microsoft/typescript-go/pts/frontend"
	"github.com/microsoft/typescript-go/pts/lower"
	"github.com/microsoft/typescript-go/pts/toolchain"
)

// EmitC compiles the program whose entry module is the file at entry to C.
// Compile errors come back as a *diagnostic.Error.
func EmitC(ctx context.Context, entry string) (string, error) {
	entry, err := filepath.Abs(entry)
	if err != nil {
		return "", err
	}
	program, errors := frontend.Load(ctx, osvfs.FS(), entry)
	if len(errors) > 0 {
		return "", &diagnostic.Error{Diagnostics: errors}
	}
	info, errors := check.Program(ctx, program)
	if len(errors) > 0 {
		return "", &diagnostic.Error{Diagnostics: errors}
	}
	lowered, errors := lower.Program(ctx, program, info)
	if len(errors) > 0 {
		return "", &diagnostic.Error{Diagnostics: errors}
	}
	return cgen.Emit(lowered), nil
}

// Build compiles the program whose entry module is the file at entry into a native
// executable at output. Compile errors come back as a *diagnostic.Error.
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
	return tc.CompileProgram(ctx, rt, []string{cFile}, output)
}
