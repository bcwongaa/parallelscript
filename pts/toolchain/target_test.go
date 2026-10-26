package toolchain

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"testing"

	"gotest.tools/v3/assert"
)

func TestParseTargetKnowsEveryTarget(t *testing.T) {
	for _, want := range Targets {
		got, err := ParseTarget(want.Name)
		assert.NilError(t, err)
		assert.Equal(t, got, want)
	}
}

func TestParseTargetRefusesAnUnknownName(t *testing.T) {
	_, err := ParseTarget("riscv64-linux")
	assert.Error(t, err, `unknown target "riscv64-linux": pts builds for x86_64-linux, aarch64-linux or aarch64-macos`)
}

func TestHostTargetBuildsWithTheCompilersDefault(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("macOS's builds always name their triple (TestMacOSBuildsNameTheOldestMacOSTheyRunOn)")
	}
	for _, target := range []Target{{}, hostTarget()} {
		tc := &Toolchain{Target: target}
		assert.Assert(t, !tc.cross())
		assert.Assert(t, tc.targetFlags() == nil, "%v", tc.targetFlags())
	}
}

// On a Mac, clang's default target is the Mac's own macOS version, so pts names macOS 11,
// the first for Apple silicon, for its executables to run on any Apple silicon Mac.
func TestMacOSBuildsNameTheOldestMacOSTheyRunOn(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("needs macOS")
	}
	for _, target := range []Target{{}, hostTarget(), Aarch64Macos} {
		tc := &Toolchain{Target: target}
		assert.Assert(t, !tc.cross() && tc.darwin())
		assert.DeepEqual(t, tc.targetFlags(), []string{"--target=arm64-apple-macos11"})
	}
}

func TestOtherTargetPassesItsTriple(t *testing.T) {
	other := otherTarget(t)
	tc := &Toolchain{Target: other}
	assert.Assert(t, tc.cross())
	assert.DeepEqual(t, tc.targetFlags(), []string{"--target=" + other.triple})
}

func TestPTSTargetSetsTheTarget(t *testing.T) {
	t.Setenv("PTS_TARGET", "aarch64-linux")
	target, err := targetFromEnv()
	assert.NilError(t, err)
	assert.Equal(t, target, Aarch64Linux)
	t.Setenv("PTS_TARGET", "")
	target, err = targetFromEnv()
	assert.NilError(t, err)
	assert.Equal(t, target, Target{})
	t.Setenv("PTS_TARGET", "arm64")
	_, err = targetFromEnv()
	assert.Error(t, err, `PTS_TARGET: unknown target "arm64": pts builds for x86_64-linux, aarch64-linux or aarch64-macos`)
}

// A build for another target compiles, checks and links everything with its triple, into
// archives of its own; one for this machine passes no target, as before targets.
func TestCrossBuildsCompileForTheirTargetIntoArchivesOfTheirOwn(t *testing.T) {
	dir := t.TempDir()
	other := otherTarget(t)
	native := &Toolchain{Home: "../..", CacheDir: filepath.Join(dir, "cache"), AR: recordingTool(t, dir, "ar", "")}
	cross := *native
	native.CC = recordingTool(t, dir, "native-cc", "-o")
	cross.CC = recordingTool(t, dir, "cross-cc", "-o")
	cross.Target = other

	for _, tc := range []*Toolchain{native, &cross} {
		rt, err := tc.Runtime(t.Context(), Release)
		assert.NilError(t, err)
		assert.NilError(t, tc.CompileProgram(t.Context(), rt, []string{"main.c"}, filepath.Join(dir, "program")))
		assert.NilError(t, tc.CheckProgram(t.Context(), Release, []string{"main.c"}))
	}

	nativeRuns, crossRuns := recordedRuns(t, native.CC), recordedRuns(t, cross.CC)
	assert.Equal(t, len(nativeRuns), len(crossRuns))
	for i := range nativeRuns {
		assert.Assert(t, !strings.Contains(nativeRuns[i], "--target"), nativeRuns[i])
		assert.Equal(t, crossRuns[i], "--target="+other.triple+" "+nativeRuns[i])
	}
	nativeArchives, err := filepath.Glob(filepath.Join(dir, "cache", "*", "*.a"))
	assert.NilError(t, err)
	assert.Equal(t, len(nativeArchives), 2*len(builtLibraries), "each target's archives in directories of their own: %v", nativeArchives)
}

// builtLibraries are the libraries Runtime builds.
var builtLibraries = []library{runtimeLibrary, mbedtlsLibrary, ryuLibrary, pcre2Library, libuvLibrary, bdwgcLibrary, libbacktraceLibrary}

// recordingTool writes a program that records its arguments, one run a line, and creates
// the file after output if output isn't empty (else the file its second argument names,
// as ar's), so a build through it succeeds without compiling anything.
func recordingTool(t *testing.T, dir string, name string, output string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	script := `#!/bin/sh
case "$1" in --version) echo "recording tool"; exit 0;; esac
echo "$*" >> "$0.log"
if [ -z "` + output + `" ]; then : > "$2"; exit 0; fi
while [ $# -gt 0 ]; do if [ "$1" = "` + output + `" ]; then : > "$2"; fi; shift; done
`
	assert.NilError(t, os.WriteFile(path, []byte(script), 0o755))
	return path
}

// recordedRuns are the argument lists a recording tool was run with, sorted, with the
// archives' cache keys and the object files' temporary directories left out.
func recordedRuns(t *testing.T, tool string) []string {
	t.Helper()
	log, err := os.ReadFile(tool + ".log")
	assert.NilError(t, err)
	keyed := regexp.MustCompile(`/([a-z0-9]+)-(?:tmp-[0-9]+|[0-9a-f]{16})/`)
	runs := strings.Split(strings.TrimSpace(keyed.ReplaceAllString(string(log), "/$1/")), "\n")
	slices.Sort(runs)
	return runs
}

// otherTarget is a Linux target this machine doesn't run: aarch64-linux, or on an arm64
// machine x86_64-linux. It skips the test on another system, whose libraries differ.
func otherTarget(t *testing.T) Target {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("builds for another Linux machine are tested on Linux")
	}
	if hostTarget() == Aarch64Linux {
		return X86_64Linux
	}
	return Aarch64Linux
}
