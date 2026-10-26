package toolchain

import (
	"path/filepath"
	"strings"
	"testing"

	"gotest.tools/v3/assert"
)

func TestMacOSTargetPropagatesToEveryBuildStep(t *testing.T) {
	t.Parallel()
	mac, err := ParseTarget("aarch64-macos")
	assert.NilError(t, err)
	dir := t.TempDir()
	tc := &Toolchain{
		Home: "../..", CacheDir: filepath.Join(dir, "cache"), Target: mac,
		CC: recordingTool(t, dir, "cc", "-o"), AR: recordingTool(t, dir, "ar", ""),
	}

	rt, err := tc.Runtime(t.Context(), Release)
	assert.NilError(t, err)
	assert.NilError(t, tc.CheckProgram(t.Context(), Release, []string{"main.c"}))
	assert.NilError(t, tc.CompileProgram(t.Context(), rt, []string{"main.c"}, filepath.Join(dir, "program")))

	runs := recordedRuns(t, tc.CC)
	assert.Assert(t, len(runs) > len(builtLibraries), "no library compilation was recorded")
	for _, run := range runs {
		assert.Equal(t, strings.Count(run, "--target="), 1, run)
		assert.Assert(t, strings.HasPrefix(run, "--target=arm64-apple-macos11 "), run)
		for _, forbidden := range []string{"-lpthread", "-ldl", "-lm ", "-static-libgcc", "-Wl,--wrap="} {
			assert.Assert(t, !strings.Contains(run, forbidden), run)
		}
	}
	commands := strings.Join(runs, "\n")
	for _, source := range []string{"src/unix/kqueue.c", "src/unix/fsevents.c", "src/unix/darwin.c", "src/unix/random-getentropy.c", "libbacktrace/macho.c"} {
		assert.Assert(t, strings.Contains(commands, source), "missing %s in recorded commands", source)
	}
	for _, forbidden := range []string{"src/unix/linux.c", "src/unix/procfs-exepath.c", "libbacktrace/elf.c", "-DHAVE_DL_ITERATE_PHDR", "libbacktrace-config/"} {
		assert.Assert(t, !strings.Contains(commands, forbidden), "Linux-only %s in recorded commands", forbidden)
	}
	for _, flag := range []string{"-DNO_MPROTECT_VDB", "-D_DARWIN_UNLIMITED_SELECT=1", "-D_DARWIN_USE_64_BIT_INODE=1", "libbacktrace-config-darwin"} {
		assert.Assert(t, strings.Contains(commands, flag), "missing %s in recorded commands", flag)
	}

	cached, err := tc.Runtime(t.Context(), Release)
	assert.NilError(t, err)
	assert.DeepEqual(t, cached.Archives, rt.Archives)
	assert.DeepEqual(t, recordedRuns(t, tc.CC), runs)
}

func TestMacOSAndLinuxUseSeparateArchivesWithTheSameCompiler(t *testing.T) {
	t.Parallel()
	mac, err := ParseTarget("aarch64-macos")
	assert.NilError(t, err)
	dir := t.TempDir()
	tc := &Toolchain{
		Home: "../..", CacheDir: filepath.Join(dir, "cache"), Target: X86_64Linux,
		CC: recordingTool(t, dir, "cc", "-o"), AR: recordingTool(t, dir, "ar", ""),
	}
	linux, err := tc.Runtime(t.Context(), Release)
	assert.NilError(t, err)

	tc.Target = mac
	darwin, err := tc.Runtime(t.Context(), Release)
	assert.NilError(t, err)

	assert.Equal(t, len(linux.Archives), len(builtLibraries))
	assert.Equal(t, len(darwin.Archives), len(linux.Archives))
	for i := range linux.Archives {
		assert.Assert(t, linux.Archives[i] != darwin.Archives[i], "shared archive: %s", linux.Archives[i])
		assert.Equal(t, filepath.Base(linux.Archives[i]), filepath.Base(darwin.Archives[i]))
	}
	archives, err := filepath.Glob(filepath.Join(dir, "cache", "*", "*.a"))
	assert.NilError(t, err)
	assert.Equal(t, len(archives), 2*len(builtLibraries))
}
