package tests

import (
	"path/filepath"
	"testing"

	"github.com/microsoft/typescript-go/pts/toolchain"
	"gotest.tools/v3/assert"
)

func TestHTTPServerDeadlineTimerRespectsAbsoluteClock(t *testing.T) {
	heavyJobs <- struct{}{}
	defer func() { <-heavyJobs }()
	tc, err := toolchain.New()
	assert.NilError(t, err)
	for _, profile := range []toolchain.Profile{toolchain.Release, toolchain.Sanitize} {
		t.Run(profile.String(), func(t *testing.T) {
			rt, err := tc.Runtime(t.Context(), profile)
			assert.NilError(t, err)
			dir := t.TempDir()
			exe := filepath.Join(dir, "deadline-timer")
			assert.NilError(t, tc.CompileProgram(t.Context(), rt, []string{"testdata/http_deadlines/deadline_timer.c"}, exe))
			got := execute(t, dir, []string{"ASAN_OPTIONS=detect_leaks=0:detect_stack_use_after_return=0", "UBSAN_OPTIONS=halt_on_error=1"}, exe)
			assert.Equal(t, got.exitCode, 0, "stdout: %s\nstderr: %s", got.stdout, got.stderr)
			assert.Equal(t, got.stdout, "absolute deadline timer controls passed\n")
			assert.Equal(t, got.stderr, "")
		})
	}
}
