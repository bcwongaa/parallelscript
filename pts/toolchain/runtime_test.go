package toolchain_test

import (
	"bytes"
	"fmt"
	"math"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/microsoft/typescript-go/internal/jsnum"
	"github.com/microsoft/typescript-go/pts/toolchain"
	"gotest.tools/v3/assert"
)

func newToolchain(t *testing.T) *toolchain.Toolchain {
	t.Helper()
	tc, err := toolchain.New()
	assert.NilError(t, err)
	return tc
}

// buildC compiles C sources against the runtime and returns the executable's path.
func buildC(t *testing.T, profile toolchain.Profile, sources ...string) string {
	t.Helper()
	tc := newToolchain(t)
	rt, err := tc.Runtime(t.Context(), profile)
	assert.NilError(t, err)
	exe := filepath.Join(t.TempDir(), "prog")
	assert.NilError(t, tc.CompileProgram(t.Context(), rt, sources, exe))
	return exe
}

// buildCSource writes one C source to a temp dir and builds it.
func buildCSource(t *testing.T, source string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "main.c")
	assert.NilError(t, os.WriteFile(path, []byte(source), 0o644))
	return buildC(t, toolchain.Release, path)
}

type runResult struct {
	stdout, stderr string
	exitCode       int
}

func run(t *testing.T, exe string, stdin string, args ...string) runResult {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), exe, args...)
	cmd.Stdin = strings.NewReader(stdin)
	// bdwgc's heap looks like leaks to LeakSanitizer, and bdwgc can't see the fake stack
	// frames ASan keeps locals in to detect use after return, so it would miss those roots.
	cmd.Env = append(os.Environ(), "ASAN_OPTIONS=detect_leaks=0:detect_stack_use_after_return=0")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	result := runResult{stdout: stdout.String(), stderr: stderr.String()}
	if exitErr, ok := err.(*exec.ExitError); ok {
		result.exitCode = exitErr.ExitCode()
	} else {
		assert.NilError(t, err)
	}
	return result
}

func runtimeTestSource(t *testing.T, name string) string {
	t.Helper()
	tc := newToolchain(t)
	return filepath.Join(tc.Home, "pts", "runtime", "tests", name)
}

func TestRuntimeUnitTestsPassUnderSanitizers(t *testing.T) {
	t.Parallel()
	sources, err := filepath.Glob(runtimeTestSource(t, "*_test.c"))
	assert.NilError(t, err)
	assert.Assert(t, len(sources) > 0)
	for _, source := range sources {
		t.Run(filepath.Base(source), func(t *testing.T) {
			t.Parallel()
			exe := buildC(t, toolchain.Sanitize, source)
			result := run(t, exe, "")
			assert.Equal(t, result.exitCode, 0, "stderr:\n%s", result.stderr)
		})
	}
}

func TestNumberFormattingMatchesJavaScript(t *testing.T) {
	t.Parallel()
	exe := buildC(t, toolchain.Release, runtimeTestSource(t, "number_test.c"))

	values := []float64{0, math.Copysign(0, -1), math.NaN(), math.Inf(1), math.Inf(-1), 1, -1, 0.1, 0.2, 0.1 + 0.2,
		1e21, 1e-7, 1e-6, 123e-20, 5e-324, math.MaxFloat64, math.SmallestNonzeroFloat64, 9007199254740993, 2.5e-7, 1.0 / 3}
	rng := rand.New(rand.NewPCG(1, 2))
	for range 20000 {
		values = append(values, math.Float64frombits(rng.Uint64()))
	}
	for range 5000 {
		// Short decimals and integers stress the digit-trimming and notation boundaries.
		values = append(values, float64(rng.IntN(2_000_000)-1_000_000)/math.Pow(10, float64(rng.IntN(30)-8)))
	}
	var input strings.Builder
	for _, v := range values {
		fmt.Fprintf(&input, "%016x\n", math.Float64bits(v))
	}

	result := run(t, exe, input.String(), "--format-stdin")
	assert.Equal(t, result.exitCode, 0, result.stderr)
	lines := strings.Split(strings.TrimSuffix(result.stdout, "\n"), "\n")
	assert.Equal(t, len(lines), len(values))
	mismatches := 0
	for i, v := range values {
		if expected := jsnum.Number(v).String(); lines[i] != expected {
			mismatches++
			if mismatches <= 10 {
				t.Errorf("%016x: runtime printed %q, JavaScript prints %q", math.Float64bits(v), lines[i], expected)
			}
		}
	}
	assert.Equal(t, mismatches, 0)
}

func TestConsoleLogPrintsPrimitivesLikeNode(t *testing.T) {
	t.Parallel()
	exe := buildCSource(t, `#include "pts.h"
#include <math.h>
PTS_STATIC_STRING(greeting, PTS_STRING_ASCII, 21, 21, "Hello, ParallelScript");
int main(int argc, char **argv) {
  pts_runtime_init(argc, argv);
  pts_console_log(1, (pts_value[]){pts_value_from_string(greeting)});
  pts_console_log(4, (pts_value[]){pts_value_from_number(1), pts_value_from_number(-0.0),
                                   pts_value_from_number(0.1 + 0.2), pts_value_from_number(NAN)});
  pts_console_log(4, (pts_value[]){PTS_VALUE_TRUE, PTS_VALUE_FALSE, PTS_VALUE_NULL, PTS_VALUE_UNDEFINED});
  pts_console_log(0, NULL);
  pts_console_log(2, (pts_value[]){pts_value_from_string(pts_string_from_cstr("héllo")), pts_value_from_number(1e21)});
  return pts_runtime_exit(0);
}
`)
	result := run(t, exe, "")
	assert.Equal(t, result.exitCode, 0, result.stderr)
	assert.Equal(t, result.stdout, "Hello, ParallelScript\n1 -0 0.30000000000000004 NaN\ntrue false null undefined\n\nhéllo 1e+21\n")
}

func TestPanicReportsUncaughtErrorAndExitsWithOne(t *testing.T) {
	t.Parallel()
	exe := buildCSource(t, `#include "pts.h"
int main(int argc, char **argv) {
  pts_runtime_init(argc, argv);
  pts_console_log(1, (pts_value[]){pts_value_from_number(1)});
  pts_panic("RangeError", "Division by zero");
}
`)
	result := run(t, exe, "")
	assert.Equal(t, result.exitCode, 1)
	assert.Equal(t, result.stdout, "1\n")
	assert.Equal(t, result.stderr, "Uncaught RangeError: Division by zero\n")
}

func TestProgramDependsOnlyOnLibc(t *testing.T) {
	t.Parallel()
	exe := buildCSource(t, `#include "pts.h"
int main(int argc, char **argv) { pts_runtime_init(argc, argv); return pts_runtime_exit(0); }
`)
	out, err := exec.CommandContext(t.Context(), "ldd", exe).Output()
	assert.NilError(t, err)
	allowed := regexp.MustCompile(`^\s*(linux-vdso\.so\.1|libc\.so\.6|/lib64/ld-linux-x86-64\.so\.2|libm\.so\.6)\b`)
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		assert.Assert(t, allowed.MatchString(line), "unexpected dynamic dependency: %s", line)
	}
}
