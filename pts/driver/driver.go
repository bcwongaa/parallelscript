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
	"slices"
	"strconv"

	"github.com/microsoft/typescript-go/internal/vfs/osvfs"
	"github.com/microsoft/typescript-go/pts/cgen"
	"github.com/microsoft/typescript-go/pts/check"
	"github.com/microsoft/typescript-go/pts/diagnostic"
	"github.com/microsoft/typescript-go/pts/frontend"
	"github.com/microsoft/typescript-go/pts/lower"
	"github.com/microsoft/typescript-go/pts/toolchain"
)

// EmitC compiles the program whose entry module is the file at entry to C: the C that Build
// compiles, which the C compiler that builds it checks (C176). Compile errors come back as a
// *diagnostic.Error, and a bug the compiler ran into as a *diagnostic.InternalError (D-179).
func EmitC(ctx context.Context, entry string) (code string, err error) {
	checked, err := load(ctx, entry)
	if err != nil {
		return "", err
	}
	return checked.compileC(ctx, checkC)
}

// EmitCWith is EmitC with the given specialization alone, for tests: a failure doesn't
// fall back (lower.Fallbacks) or leave anything out (lower.Narrowing), so a bug that only
// AllInstances' instances hit isn't hidden, and the C isn't checked.
func EmitCWith(ctx context.Context, entry string, specialization lower.Specialization) (code string, err error) {
	checked, err := load(ctx, entry)
	if err != nil {
		return "", err
	}
	code, _, err = checked.emitC(ctx, specialization, nil)
	return code, err
}

// EmitCNarrowedWith is EmitCWith, leaving out what lowerings that fail blame their failures
// on, as EmitC does before it falls back (lower.Narrowing), for tests.
func EmitCNarrowedWith(ctx context.Context, entry string, specialization lower.Specialization) (code string, err error) {
	checked, err := load(ctx, entry)
	if err != nil {
		return "", err
	}
	r := checked.narrowedAt(ctx, slices.Index(lower.Fallbacks, specialization), map[int]*lowering{})
	return r.code, r.err
}

// Build compiles the program whose entry module is the file at entry into a native
// executable at output, for this machine or PTS_TARGET's. Compile errors come back as a
// *diagnostic.Error, and a bug the compiler ran into, C that the C compiler rejects or
// that doesn't link included, as a *diagnostic.InternalError.
func Build(ctx context.Context, entry string, output string) error {
	return BuildFor(ctx, entry, output, toolchain.Target{})
}

// BuildFor is Build for target, the zero Target being Build's.
func BuildFor(ctx context.Context, entry string, output string, target toolchain.Target) error {
	checked, err := load(ctx, entry)
	if err != nil {
		return err
	}
	_, err = checked.compileC(ctx, func(ctx context.Context, c string) error { return build(ctx, c, output, target) })
	return err
}

// maxLowerings bounds how often a build is lowered with one of lower.Fallbacks, each time
// leaving out more of its speculative instances (lower.Narrowing), before it falls back to
// the next.
const maxLowerings = 8

// compileC lowers the program to C, and gives it to accept (a build, or emit-c's check),
// with each of lower.Fallbacks in turn, until a lowering reports nothing and accept doesn't
// fail with the C compiler rejecting the C (rejectedC). The instances each but the last adds
// are made for calls that may never meet them, and a failure in their code that the
// lowering's trials don't take back mustn't stop a program that compiles without them
// (D-190, D-191): each is lowered leaving out what its failures are blamed on (narrowedAt).
// A generic class's instances for type arguments of the same IR types share one class,
// which the program may convert between as TypeScript lets it; only where no lowering
// succeeds that way is the program lowered again with such instances split where the types
// the class computes for them differ (lower.ProgramSplittingClasses, C177), and if that
// fails too, the first failure stands (D-216).
func (p *checkedProgram) compileC(ctx context.Context, accept func(ctx context.Context, c string) error) (code string, err error) {
	if code, err = p.compileCWith(ctx, accept); code != "" || err == nil || !fallsBack(ctx, err) {
		return code, err
	}
	split := *p
	split.splitClasses = true
	if splitCode, splitErr := split.compileCWith(ctx, accept); splitCode != "" {
		return splitCode, splitErr
	}
	return code, err
}

// compileCWith is compileC with the program's classes shared or split, as p says.
func (p *checkedProgram) compileCWith(ctx context.Context, accept func(ctx context.Context, c string) error) (code string, err error) {
	lowerings := map[int]*lowering{}
	for at := range lower.Fallbacks {
		r := p.narrowedAt(ctx, at, lowerings)
		if r.err != nil {
			if err = r.err; !fallsBack(ctx, err) {
				return "", err
			}
			continue
		}
		var rejected *rejectedC
		if err = accept(ctx, r.code); !errors.As(err, &rejected) || ctx.Err() != nil {
			return r.code, err
		}
		err = rejected.InternalError
	}
	return "", err
}

// lowering is the program lowered with lower.Fallbacks[at], leaving out narrowing: its C,
// or what stopped it.
type lowering struct {
	at        int
	code      string
	narrowing *lower.Narrowing
	err       error
}

// narrowedAt lowers the program with lower.Fallbacks[at], and while a lowering fails blaming
// every failure on pairings and call sites a narrower one leaves out (lower.Narrowing, C176),
// lowers it again without those too, at most maxLowerings times, so the rest keep their
// instances. It never leaves out what the first later fallback that lowers the program
// keeps: there the program falls back, as before (the lowering fails), so no call has fewer
// instances than the fallback would give it. lowerings holds each fallback's, made once.
func (p *checkedProgram) narrowedAt(ctx context.Context, at int, lowerings map[int]*lowering) *lowering {
	if r := lowerings[at]; r != nil {
		return r
	}
	r := &lowering{at: at}
	for range maxLowerings {
		var narrower *lower.Narrowing
		if r.code, narrower, r.err = p.emitC(ctx, lower.Fallbacks[at], r.narrowing); r.err == nil || narrower == nil || !fallsBack(ctx, r.err) {
			break
		}
		if at+1 < len(lower.Fallbacks) && !narrower.Within(nil, lower.Fallbacks[at+1]) {
			if later := p.laterLowering(ctx, at+1, lowerings); later != nil && !narrower.Within(later.narrowing, lower.Fallbacks[later.at]) {
				break
			}
		}
		r.narrowing = narrower
	}
	lowerings[at] = r
	return r
}

// laterLowering is the first of the lowerings with lower.Fallbacks[from:] that lowers the
// program (narrowedAt), or nil.
func (p *checkedProgram) laterLowering(ctx context.Context, from int, lowerings map[int]*lowering) *lowering {
	for at := from; at < len(lower.Fallbacks); at++ {
		if r := p.narrowedAt(ctx, at, lowerings); r.err == nil {
			return r
		}
	}
	return nil
}

// checkedProgram is a program that loaded and passed the semantic checks, lowered with its
// generic classes' instances split (lower.ProgramSplittingClasses) or not.
type checkedProgram struct {
	program      *frontend.Program
	info         *check.Info
	splitClasses bool
}

// load loads the program whose entry module is the file at entry and checks it.
func load(ctx context.Context, entry string) (checked *checkedProgram, err error) {
	defer diagnostic.Contain(&err)
	entry, err = filepath.Abs(entry)
	if err != nil {
		return nil, err
	}
	program, diagnostics := frontend.Load(ctx, osvfs.FS(), entry)
	if len(diagnostics) > 0 {
		return nil, &diagnostic.Error{Diagnostics: diagnostics}
	}
	info, diagnostics := check.Program(ctx, program)
	if len(diagnostics) > 0 {
		return nil, &diagnostic.Error{Diagnostics: diagnostics}
	}
	return &checkedProgram{program: program, info: info}, nil
}

// fallsBack reports whether a lowering that failed with err is tried again (compileC):
// compile errors and internal errors are.
func fallsBack(ctx context.Context, err error) bool {
	var compileErrors *diagnostic.Error
	var internal *diagnostic.InternalError
	return ctx.Err() == nil && (errors.As(err, &compileErrors) || errors.As(err, &internal))
}

// emitC lowers the program with the given specialization, leaving out what narrowing does,
// and emits its C; narrower is what lower.ProgramWith says a lowering that fails may leave out.
func (p *checkedProgram) emitC(ctx context.Context, specialization lower.Specialization, narrowing *lower.Narrowing) (code string, narrower *lower.Narrowing, err error) {
	defer diagnostic.Contain(&err)
	programWith := lower.ProgramWith
	if p.splitClasses {
		programWith = lower.ProgramSplittingClasses
	}
	lowered, diagnostics, narrower := programWith(ctx, p.program, p.info, specialization, narrowing)
	if len(diagnostics) > 0 {
		return "", narrower, &diagnostic.Error{Diagnostics: diagnostics}
	}
	return cgen.Emit(lowered), nil, nil
}

// build compiles a program's C into a native executable at output, for target unless it is the zero Target.
func build(ctx context.Context, c string, output string, target toolchain.Target) error {
	tc, err := toolchain.NewFor(target)
	if err != nil {
		return err
	}
	profile, err := toolchain.BuildProfile()
	if err != nil {
		return err
	}
	rt, err := tc.Runtime(ctx, profile)
	if err != nil {
		return err
	}
	dir, cFile, err := writeC(c)
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	output, err = filepath.Abs(output)
	if err != nil {
		return err
	}
	if err := tc.CompileProgram(ctx, rt, []string{cFile}, output); err != nil {
		return generatedCFailure(err, cFile)
	}
	return nil
}

// checkC checks a program's C with the C compiler a build compiles it with, as emit-c's
// accept (compileC): a *rejectedC if it rejects it, so emit-c gives the C a build compiles.
// Without a C compiler to check with (no PTS home, or the compiler doesn't run), the C is
// taken as it is.
func checkC(ctx context.Context, c string) error {
	tc, err := toolchain.New()
	if err != nil {
		return nil
	}
	dir, cFile, err := writeC(c)
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	if err := tc.CheckProgram(ctx, toolchain.Release, []string{cFile}); err != nil {
		var rejected *rejectedC
		if failure := generatedCFailure(err, cFile); errors.As(failure, &rejected) {
			return failure
		}
	}
	return nil
}

// writeC writes a program's C to main.c in a new temporary directory.
func writeC(c string) (dir string, cFile string, err error) {
	dir, err = os.MkdirTemp("", "pts-build-")
	if err != nil {
		return "", "", err
	}
	cFile = filepath.Join(dir, "main.c")
	if err := os.WriteFile(cFile, []byte(c), 0o644); err != nil {
		os.RemoveAll(dir)
		return "", "", err
	}
	return dir, cFile, nil
}

// cCompilerError is the C compiler's first error, "file:line:column: error: message",
// without the warning flag that may follow the message.
var cCompilerError = regexp.MustCompile(`(?m)^(.*):(\d+):\d+: error: (.*?)(?: \[-W[^\]]*\])?$`)

// undefinedReference is the linker's error for a function that the C calls and nothing defines.
var undefinedReference = regexp.MustCompile("undefined reference to `[^']*'")

// archiveMember is the linker naming an object of an archive as where a reference comes
// from (`…/libptsrt.a(008-runtime.o): in function …`): the runtime's, not the generated C's.
var archiveMember = regexp.MustCompile(`\.a\([^)]*\): in function`)

// rejectedC is the C compiler rejecting a program's C (generatedCFailure), which a build
// and emit-c's check both see, so both try the next of lower.Fallbacks (compileC); a failure
// to link falls back in neither, since emit-c doesn't link.
type rejectedC struct{ *diagnostic.InternalError }

func (r *rejectedC) Unwrap() error { return r.InternalError }

// generatedCFailure reports the C compiler rejecting the C generated into cFile (a
// *rejectedC), or the linker the functions it calls, as the bug in the compiler that it
// is: an internal error at the source line that the C compiler's first error has through
// #line (D-179). Other failures (the C compiler killed, a full disk, a runtime archive
// missing what it calls) are returned as they are.
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
		return &rejectedC{&diagnostic.InternalError{Diagnostic: at, Detail: command.Error()}}
	}
	if reference := undefinedReference.FindString(command.Output); reference != "" && !archiveMember.MatchString(command.Output) {
		at.Message = "internal compiler error: the generated C doesn't link: " + reference
		return &diagnostic.InternalError{Diagnostic: at, Detail: command.Error()}
	}
	return err
}
