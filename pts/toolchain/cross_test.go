package toolchain_test

import (
	"debug/elf"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/microsoft/typescript-go/pts/toolchain"
	"gotest.tools/v3/assert"
)

// requireCrossVar makes a missing aarch64 cross toolchain or emulator a failure instead of
// a skip (D-225), as PTS_REQUIRE_NODE does for Node.
const requireCrossVar = "PTS_REQUIRE_CROSS"

// aarch64Sysroot is the aarch64 C library of Debian and Ubuntu's cross packages.
const aarch64Sysroot = "/usr/aarch64-linux-gnu"

// The runtime and every vendored library compile for aarch64 Linux, and a program linked
// against them runs: it starts the runtime and the pool's threads, allocates in the
// collector, formats a number and prints.
func TestRuntimeBuildsAndRunsForAarch64(t *testing.T) {
	t.Parallel()
	requireAarch64(t)
	tc := newToolchain(t)
	tc.Target = toolchain.Aarch64Linux
	rt, err := tc.Runtime(t.Context(), toolchain.Release)
	assert.NilError(t, err)
	source := filepath.Join(t.TempDir(), "main.c")
	assert.NilError(t, os.WriteFile(source, []byte(`#include "pts.h"
PTS_STATIC_STRING(greeting, PTS_STRING_ASCII, 21, 21, "Hello, ParallelScript");
int main(int argc, char **argv) {
  pts_runtime_init(argc, argv);
  pts_console_log(2, (pts_value[]){pts_value_from_string(greeting), pts_value_from_number(pts_parallel_threads())});
  pts_console_log(2, (pts_value[]){pts_value_from_number(0.1 + 0.2), pts_value_from_string(pts_string_from_cstr("héllo"))});
  return pts_runtime_exit(0);
}
`), 0o644))
	exe := filepath.Join(t.TempDir(), "prog")

	assert.NilError(t, tc.CompileProgram(t.Context(), rt, []string{source}, exe))

	file, err := elf.Open(exe)
	assert.NilError(t, err)
	defer file.Close()
	assert.Equal(t, file.Machine, elf.EM_AARCH64)
	result := runAarch64(t, exe, "PTS_THREADS=3")
	assert.Equal(t, result.exitCode, 0, result.stderr)
	assert.Equal(t, result.stdout, "Hello, ParallelScript 3\n0.30000000000000004 héllo\n")
}

// requireAarch64 skips the test (fails it with PTS_REQUIRE_CROSS=1) unless clang can link
// for aarch64 Linux and this machine can run what it links, natively or under qemu-aarch64.
func requireAarch64(t *testing.T) {
	t.Helper()
	missing := ""
	dir := t.TempDir()
	probe := filepath.Join(dir, "probe.c")
	assert.NilError(t, os.WriteFile(probe, []byte("int main(void) { return 0; }\n"), 0o644))
	if out, err := exec.CommandContext(t.Context(), "clang", "--target=aarch64-linux-gnu", "-o", filepath.Join(dir, "probe"), probe).CombinedOutput(); err != nil {
		missing = fmt.Sprintf("an aarch64 cross toolchain (clang --target=aarch64-linux-gnu, %s): %s", aarch64Sysroot, strings.TrimSpace(string(out)))
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

func qemuAarch64() string {
	for _, name := range []string{"qemu-aarch64-static", "qemu-aarch64"} {
		if path, err := exec.LookPath(name); err == nil {
			return path
		}
	}
	return ""
}

// runAarch64 runs an aarch64 executable natively on an arm64 host, else under qemu-aarch64.
func runAarch64(t *testing.T, exe string, env ...string) runResult {
	t.Helper()
	if runtime.GOARCH == "arm64" {
		return runWithEnv(t, exe, env, "")
	}
	return runWithEnv(t, qemuAarch64(), env, "", "-L", aarch64Sysroot, exe)
}
