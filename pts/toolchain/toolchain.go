package toolchain

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"golang.org/x/sync/errgroup"
	"golang.org/x/sys/unix"
)

// cacheFormat changes whenever the layout or meaning of cache entries changes.
const cacheFormat = "pts-toolchain-1"

const maxParallelCompiles = 6

// Profile selects how the runtime and the program are compiled.
type Profile int

const (
	// Release is the normal build: clang -O2.
	Release Profile = iota
	// Sanitize builds the runtime and program with AddressSanitizer and UndefinedBehaviorSanitizer.
	Sanitize
	// ThreadSanitize builds them with ThreadSanitizer. Programs must not collect on several
	// threads (run them with GC_DONT_GC=1): bdwgc stops the world with signals, which fail
	// under TSan (D-122).
	ThreadSanitize
)

func (p Profile) String() string {
	switch p {
	case Sanitize:
		return "sanitize"
	case ThreadSanitize:
		return "tsan"
	}
	return "release"
}

// flags compile the runtime and programs. -ffp-contract=off keeps clang from fusing
// a*b+c into one FMA, which JS's step-by-step rounding forbids; -fno-math-errno lets
// clang inline sqrt and friends, since nothing reads errno after them;
// -falign-functions=32 starts functions on 32-byte boundaries, as Go's linker does, so
// a hot function's speed doesn't depend on where the linker happens to put it (D-119).
func (p Profile) flags() []string {
	switch p {
	case Sanitize:
		return []string{"-O1", "-g", "-ffp-contract=off", "-fno-math-errno", "-fno-omit-frame-pointer", "-fsanitize=address,undefined", "-fno-sanitize-recover=undefined"}
	case ThreadSanitize:
		return []string{"-O1", "-g", "-ffp-contract=off", "-fno-math-errno", "-fno-omit-frame-pointer", "-fsanitize=thread"}
	}
	return []string{"-O2", "-g", "-ffp-contract=off", "-fno-math-errno", "-falign-functions=32"}
}

// Toolchain compiles C against the ParallelScript runtime.
type Toolchain struct {
	Home     string // contains pts/runtime and pts/third_party
	CacheDir string // built archives live in <CacheDir>/<name>-<key>/
	CC       string
	AR       string
}

// New finds the PTS home and cache dir. $PTS_CC and $PTS_AR override clang and ar.
func New() (*Toolchain, error) {
	home, err := FindHome()
	if err != nil {
		return nil, err
	}
	cacheDir, err := DefaultCacheDir()
	if err != nil {
		return nil, err
	}
	return &Toolchain{Home: home, CacheDir: cacheDir, CC: envOr("PTS_CC", "clang"), AR: envOr("PTS_AR", "ar")}, nil
}

func envOr(name string, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

// Runtime is a built runtime: what a C compiler needs to compile and link a program against it.
type Runtime struct {
	Profile     Profile
	IncludeDirs []string // absolute
	Archives    []string // absolute, in link order
}

// Runtime returns the runtime for the profile, building it and the vendored libraries
// into the cache first if needed. Concurrent callers, even in other processes, build each archive once.
func (t *Toolchain) Runtime(ctx context.Context, profile Profile) (*Runtime, error) {
	ccVersion, err := t.compilerVersion(ctx)
	if err != nil {
		return nil, err
	}
	built := &Runtime{Profile: profile, IncludeDirs: t.absolute(runtimeLibrary.includes)}
	builds := []struct {
		lib   library
		flags []string
	}{
		{runtimeLibrary, profile.flags()},
		{mbedtlsLibrary, vendoredFlags},
		{ryuLibrary, vendoredFlags},
		{pcre2Library, vendoredFlags},
		{libuvLibrary, vendoredFlags},
		{bdwgcLibrary, vendoredFlags},
		{libbacktraceLibrary, vendoredFlags},
	}
	for _, b := range builds {
		archive, err := t.ensureArchive(ctx, b.lib, b.flags, ccVersion)
		if err != nil {
			return nil, err
		}
		built.Archives = append(built.Archives, archive)
	}
	return built, nil
}

// CompileProgram compiles C files and links them with the runtime into an executable.
func (t *Toolchain) CompileProgram(ctx context.Context, rt *Runtime, cFiles []string, output string) error {
	args := slices.Concat(rt.Profile.flags(), []string{"-std=c11", "-o", output})
	for _, dir := range rt.IncludeDirs {
		args = append(args, "-I", dir)
	}
	args = slices.Concat(args, cFiles, rt.Archives, systemLibraries)
	return t.run(ctx, t.CC, args...)
}

// CheckProgram checks C files with the C compiler as CompileProgram compiles them for the
// profile, without compiling or linking them (-fsyntax-only): it rejects what the compiler
// rejects there. It needs only the runtime's headers, not its archives.
func (t *Toolchain) CheckProgram(ctx context.Context, profile Profile, cFiles []string) error {
	args := slices.Concat(profile.flags(), []string{"-std=c11", "-fsyntax-only", "-w"})
	for _, dir := range t.absolute(runtimeLibrary.includes) {
		args = append(args, "-I", dir)
	}
	return t.run(ctx, t.CC, slices.Concat(args, cFiles)...)
}

func (t *Toolchain) absolute(paths []string) []string {
	result := make([]string, len(paths))
	for i, p := range paths {
		result[i] = filepath.Join(t.Home, p)
	}
	return result
}

func (t *Toolchain) compilerVersion(ctx context.Context) (string, error) {
	out, err := exec.CommandContext(ctx, t.CC, "--version").Output()
	if err != nil {
		return "", fmt.Errorf("cannot run the C compiler %q (install clang or set PTS_CC): %w", t.CC, err)
	}
	return string(out), nil
}

// ensureArchive returns the path of lib<name>.a for this library and flags, building it if the cache lacks it.
func (t *Toolchain) ensureArchive(ctx context.Context, lib library, flags []string, ccVersion string) (string, error) {
	key, err := t.cacheKey(lib, flags, ccVersion)
	if err != nil {
		return "", err
	}
	dir := filepath.Join(t.CacheDir, lib.name+"-"+key)
	archive := filepath.Join(dir, "lib"+lib.name+".a")
	if fileExists(archive) {
		return archive, nil
	}
	if err := os.MkdirAll(t.CacheDir, 0o755); err != nil {
		return "", err
	}
	unlock, err := lockFile(dir + ".lock")
	if err != nil {
		return "", err
	}
	defer unlock()
	if fileExists(archive) {
		return archive, nil // another process built it while we waited
	}
	if err := t.buildArchive(ctx, lib, flags, dir); err != nil {
		return "", fmt.Errorf("building %s: %w", lib.name, err)
	}
	return archive, nil
}

func (t *Toolchain) cacheKey(lib library, flags []string, ccVersion string) (string, error) {
	hash := sha256.New()
	fmt.Fprintf(hash, "%s\n%s\n%q\n%q\n%q\n%q\n%q\n%s\n", cacheFormat, lib.name, lib.sources, lib.glob, lib.includes, lib.defines, slices.Concat(lib.flags, flags), ccVersion)
	paths := slices.Clone(lib.keyFiles)
	for _, dir := range lib.keyDirs {
		files, err := listFiles(filepath.Join(t.Home, dir))
		if err != nil {
			return "", err
		}
		for _, f := range files {
			rel, _ := filepath.Rel(t.Home, f)
			paths = append(paths, rel)
		}
	}
	for _, p := range paths {
		content, err := os.ReadFile(filepath.Join(t.Home, p))
		if err != nil {
			return "", fmt.Errorf("hashing %s (are the submodules checked out? run `git submodule update --init pts/third_party`): %w", p, err)
		}
		fmt.Fprintf(hash, "%s %d\n", p, len(content))
		hash.Write(content)
	}
	return hex.EncodeToString(hash.Sum(nil))[:16], nil
}

func listFiles(dir string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err == nil && !entry.IsDir() {
			files = append(files, path)
		}
		return err
	})
	return files, err
}

// buildArchive compiles the library in a temporary dir and renames it to dir, so a dir that exists is always complete.
func (t *Toolchain) buildArchive(ctx context.Context, lib library, flags []string, dir string) error {
	sources, err := t.librarySources(lib)
	if err != nil {
		return err
	}
	tmp, err := os.MkdirTemp(t.CacheDir, lib.name+"-tmp-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)

	objects := make([]string, len(sources))
	group, groupCtx := errgroup.WithContext(ctx)
	group.SetLimit(min(runtime.NumCPU(), maxParallelCompiles))
	for i, source := range sources {
		objects[i] = filepath.Join(tmp, fmt.Sprintf("%03d-%s.o", i, strings.TrimSuffix(filepath.Base(source), ".c")))
		group.Go(func() error {
			return t.run(groupCtx, t.CC, t.compileArgs(lib, flags, source, objects[i])...)
		})
	}
	if err := group.Wait(); err != nil {
		return err
	}
	if err := t.run(ctx, t.AR, slices.Concat([]string{"rcs", filepath.Join(tmp, "lib"+lib.name+".a")}, objects)...); err != nil {
		return err
	}
	for _, object := range objects {
		os.Remove(object)
	}
	return os.Rename(tmp, dir)
}

func (t *Toolchain) librarySources(lib library) ([]string, error) {
	root := filepath.Join(t.Home, lib.dir)
	if lib.glob == "" {
		sources := make([]string, len(lib.sources))
		for i, s := range lib.sources {
			sources[i] = filepath.Join(root, s)
		}
		return sources, nil
	}
	sources, err := filepath.Glob(filepath.Join(root, lib.glob))
	if err == nil && len(sources) == 0 {
		err = fmt.Errorf("no sources match %s", filepath.Join(root, lib.glob))
	}
	return sources, err
}

func (t *Toolchain) compileArgs(lib library, flags []string, source string, object string) []string {
	args := slices.Concat(flags, lib.flags)
	for _, dir := range lib.includes {
		args = append(args, "-I", filepath.Join(t.Home, dir))
	}
	for _, define := range lib.defines {
		args = append(args, "-D"+define)
	}
	return append(args, "-c", source, "-o", object)
}

// CommandError is a command of the toolchain that failed, with what it printed.
type CommandError struct {
	Command []string // the program and its arguments
	Err     error    // how it failed: its exit status, or why it didn't start
	Output  string   // its stdout and stderr, interleaved
}

func (e *CommandError) Error() string {
	return fmt.Sprintf("%s: %v\n%s", strings.Join(e.Command, " "), e.Err, e.Output)
}

func (e *CommandError) Unwrap() error { return e.Err }

// run executes a command; on failure the error is a *CommandError.
func (t *Toolchain) run(ctx context.Context, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	if err := cmd.Run(); err != nil {
		return &CommandError{Command: append([]string{name}, args...), Err: err, Output: output.String()}
	}
	return nil
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// lockFile takes an exclusive advisory lock on path, creating it if needed.
func lockFile(path string) (unlock func(), err error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	for {
		err = unix.Flock(int(file.Fd()), unix.LOCK_EX)
		if !errors.Is(err, unix.EINTR) {
			break
		}
	}
	if err != nil {
		file.Close()
		return nil, err
	}
	return func() {
		unix.Flock(int(file.Fd()), unix.LOCK_UN)
		file.Close()
	}, nil
}
