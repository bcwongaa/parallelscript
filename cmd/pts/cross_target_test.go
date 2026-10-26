package main

import (
	"context"
	"debug/elf"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/microsoft/typescript-go/pts/toolchain"
	"gotest.tools/v3/assert"
)

// requireCrossVar makes a missing aarch64 cross toolchain or emulator a failure instead of
// a skip, as PTS_REQUIRE_NODE does for Node: CI sets it where both are installed (D-225).
const requireCrossVar = "PTS_REQUIRE_CROSS"

// aarch64Sysroot is where Debian and Ubuntu's cross packages (libc6-dev-arm64-cross) put
// the aarch64 C library, which qemu-aarch64 also needs to load a dynamic executable.
const aarch64Sysroot = "/usr/aarch64-linux-gnu"

// crossSource exercises what a port can get wrong: the fork-join pool on several threads,
// the stack guard, number formatting, Unicode tables, RegExp and process.arch.
const crossSource = `const xs = Array.from({ length: 200_000 }, (_, i) => i);
console.log(xs.par.map((x) => x * 2).reduce((a, b) => a + b, 0));
function fib(k: number): number { return k < 2 ? k : fib(k - 1) + fib(k - 2); }
console.log(fib(20));
try { (function deep(d: number): number { return deep(d + 1) + 1; })(0); } catch (e) { console.log((e as Error).name, (e as Error).message); }
console.log(0.1 + 0.2, (123.456).toFixed(2), 2 ** 53 + 1, "é".normalize("NFD").length, "ß".toUpperCase(), /(\d+)-(\d+)/.exec("10-20")?.slice(1));
console.log(process.platform, process.arch);
`

const crossOutput = `39999800000
6765
RangeError Maximum call stack size exceeded
0.30000000000000004 123.46 9007199254740992 2 SS [ '10', '20' ]
linux arm64
`

// archSource prints the platform and the processor as Node names them.
const archSource = "console.log(process.platform, process.arch);\n"

func TestBuildForAarch64LinuxMakesAnArmExecutableThatRuns(t *testing.T) {
	t.Parallel()
	requireAarch64(t)
	dir := projectWith(t, map[string]string{"cross.pts": crossSource})

	got := runIn(t, dir, "build", "--target", "aarch64-linux", "cross.pts", "-o", "cross-arm64")

	assert.Equal(t, got, result{code: 0})
	exe := filepath.Join(dir, "cross-arm64")
	assert.Equal(t, elfMachine(t, exe), elf.EM_AARCH64)
	assert.Equal(t, runAarch64(t, exe, "PTS_THREADS=4"), crossOutput)
}

func TestBuildAcceptsTheTargetAsOneArgument(t *testing.T) {
	t.Parallel()
	requireAarch64(t)
	dir := projectWith(t, map[string]string{"hello.pts": helloSource})

	got := runIn(t, dir, "build", "hello.pts", "--target=aarch64-linux")

	assert.Equal(t, got, result{code: 0})
	assert.Equal(t, elfMachine(t, filepath.Join(dir, "hello")), elf.EM_AARCH64)
}

func TestBuildForThisMachinesTargetRunsHere(t *testing.T) {
	t.Parallel()
	host := thisMachine(t)
	dir := projectWith(t, map[string]string{"arch.pts": archSource})

	assert.Equal(t, runIn(t, dir, "build", "arch.pts", "--target", host.target), result{code: 0})
	assert.Equal(t, output(t, filepath.Join(dir, "arch")), host.platform+"\n")
}

// Not parallel: PTS_TARGET is the process's.
func TestPTSTargetIsTheTargetOfABuildWithoutOne(t *testing.T) {
	requireAarch64(t)
	t.Setenv("PTS_TARGET", "aarch64-linux")
	dir := projectWith(t, map[string]string{"hello.pts": helloSource})

	assert.Equal(t, runIn(t, dir, "build", "hello.pts"), result{code: 0})
	assert.Equal(t, elfMachine(t, filepath.Join(dir, "hello")), elf.EM_AARCH64)
}

// Not parallel: PTS_TARGET is the process's.
func TestTargetOptionWinsOverPTSTarget(t *testing.T) {
	host := thisMachine(t)
	dir := projectWith(t, map[string]string{"arch.pts": archSource})
	for _, setting := range []string{"aarch64-linux", "bogus"} {
		t.Setenv("PTS_TARGET", setting)

		assert.Equal(t, runIn(t, dir, "build", "arch.pts", "--target", host.target), result{code: 0}, "PTS_TARGET=%s", setting)
		assert.Equal(t, output(t, filepath.Join(dir, "arch")), host.platform+"\n", "PTS_TARGET=%s", setting)
	}
}

func TestBuildRefusesAnUnknownTarget(t *testing.T) {
	t.Parallel()
	dir := projectWith(t, map[string]string{"hello.pts": helloSource})

	got := runIn(t, dir, "build", "--target=arm64", "hello.pts")

	assert.Equal(t, got, result{code: exitUsage, stderr: "pts: unknown target \"arm64\": pts builds for x86_64-linux, aarch64-linux or aarch64-macos\n\n" + commandUsage["build"]})
	_, err := os.Stat(filepath.Join(dir, "hello"))
	assert.Assert(t, errors.Is(err, os.ErrNotExist), "%v", err)
}

func TestBuildRejectsAMissingTarget(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{{"build", "a.pts", "--target"}, {"build", "--target=", "a.pts"}} {
		got := runIn(t, t.TempDir(), args...)
		assert.Equal(t, got, result{code: exitUsage, stderr: "pts: --target needs a value\n\n" + commandUsage["build"]}, "args %q", args)
	}
}

func TestBuildsHelpNamesEveryTarget(t *testing.T) {
	t.Parallel()
	assert.Assert(t, strings.Contains(commandUsage["build"], toolchain.TargetNames()), commandUsage["build"])
	assert.Assert(t, strings.Contains(usage, "PTS_TARGET"), usage)
}

// thisMachine is the target of the machine the tests run on and Node's names for its
// system and processor, as archSource prints them, or skips the test if pts has no target for it.
func thisMachine(t *testing.T) struct{ target, platform string } {
	t.Helper()
	host, ok := map[string]struct{ target, platform string }{
		"linux/amd64":  {"x86_64-linux", "linux x64"},
		"linux/arm64":  {"aarch64-linux", "linux arm64"},
		"darwin/arm64": {"aarch64-macos", "darwin arm64"},
	}[runtime.GOOS+"/"+runtime.GOARCH]
	if !ok {
		t.Skipf("no target name for %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	return host
}

// requireAarch64 skips the test (fails it with PTS_REQUIRE_CROSS=1) unless clang can link
// for aarch64 Linux and this machine can run what it links: natively on an arm64 host,
// else under qemu-aarch64.
func requireAarch64(t *testing.T) {
	t.Helper()
	missing := ""
	if err := linkAarch64(t); err != nil {
		missing = "an aarch64 cross toolchain (clang --target=aarch64-linux-gnu, " + aarch64Sysroot + "): " + err.Error()
	} else if runtime.GOARCH != "arm64" && qemuAarch64() == "" {
		missing = "qemu-aarch64 (qemu-user-static) to run aarch64 programs"
	}
	if missing == "" {
		return
	}
	if os.Getenv(requireCrossVar) == "1" {
		t.Fatalf("%s=1 but this machine lacks %s", requireCrossVar, missing)
	}
	t.Skipf("needs %s", missing)
}

// linkAarch64 links an empty C program for aarch64 Linux, to see that the cross toolchain is there.
func linkAarch64(t *testing.T) error {
	t.Helper()
	dir := t.TempDir()
	source := filepath.Join(dir, "probe.c")
	if err := os.WriteFile(source, []byte("int main(void) { return 0; }\n"), 0o644); err != nil {
		return err
	}
	out, err := exec.CommandContext(t.Context(), "clang", "--target=aarch64-linux-gnu", "-o", filepath.Join(dir, "probe"), source).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// qemuAarch64 is the path of qemu's aarch64 user-mode emulator, or "".
func qemuAarch64() string {
	for _, name := range []string{"qemu-aarch64-static", "qemu-aarch64"} {
		if path, err := exec.LookPath(name); err == nil {
			return path
		}
	}
	return ""
}

// emulatedProgramTimeout bounds a program run under qemu, many times slower than natively.
const emulatedProgramTimeout = 2 * time.Minute

// runAarch64 runs an aarch64 executable, with env's "NAME=value" settings, natively on an
// arm64 host and else under qemu-aarch64, and returns its stdout.
func runAarch64(t *testing.T, exe string, env ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), emulatedProgramTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe)
	if runtime.GOARCH != "arm64" {
		cmd = exec.CommandContext(ctx, qemuAarch64(), "-L", aarch64Sysroot, exe)
	}
	cmd.Env = append(os.Environ(), env...)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	assert.NilError(t, err, "running %s; stderr:\n%s", exe, stderr.String())
	return string(out)
}

func elfMachine(t *testing.T, exe string) elf.Machine {
	t.Helper()
	file, err := elf.Open(exe)
	assert.NilError(t, err)
	defer file.Close()
	return file.Machine
}
