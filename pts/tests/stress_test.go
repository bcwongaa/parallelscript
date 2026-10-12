package tests

import (
	"fmt"
	"os"
	"strconv"
	"testing"

	"gotest.tools/v3/assert"
)

// stressRepeatVar sets how many times TestStress runs each stress program under each of its
// thread counts; the nightly CI job sets 100 (.github/workflows/pts-nightly.yml).
const stressRepeatVar = "PTS_STRESS_REPEAT"

// TestStress runs the programs under stress/: parallel programs whose threads race on the
// scheduler, the pool, the collector and each other's data (the race-safety invariant,
// D-L13). Each must print what it expects every time, under every PTS_THREADS it lists.
func TestStress(t *testing.T) {
	t.Parallel()
	repeat := stressRepeat(t)
	for _, path := range testFiles(t, "stress") {
		t.Run(testName(path), func(t *testing.T) {
			t.Parallel()
			spec := ensureSpec(t, path)
			assert.Assert(t, spec.stdout != nil && len(spec.threads) > 0, "a stress test needs // expect: and // threads:")
			for _, threads := range spec.threads {
				t.Run("threads="+threads, func(t *testing.T) {
					t.Parallel()
					for run := 1; run <= repeat; run++ {
						got := runPts(t, path, "PTS_THREADS="+threads)
						if got.stdout != *spec.stdout || got.exitCode != spec.exitCode {
							t.Fatalf("run %d of %d: exit code %d, stdout:\n%s\nwant exit code %d, stdout:\n%s\nstderr:\n%s",
								run, repeat, got.exitCode, got.stdout, spec.exitCode, *spec.stdout, got.stderr)
						}
					}
				})
			}
		})
	}
}

func stressRepeat(t *testing.T) int {
	t.Helper()
	value := os.Getenv(stressRepeatVar)
	if value == "" {
		return 1
	}
	repeat, err := strconv.Atoi(value)
	assert.Assert(t, err == nil && repeat > 0, fmt.Sprintf("%s=%q: want a positive number", stressRepeatVar, value))
	return repeat
}
