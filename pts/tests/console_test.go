package tests

import (
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"gotest.tools/v3/assert"
)

// TestConsoleTimersPrintTheirLabelAndTheTime checks run/console_timers.pts, whose times
// differ from run to run and so from Node's: each line is the label and the time in
// Node's format, and misused labels are warned about on stderr (D-088).
func TestConsoleTimersPrintTheirLabelAndTheTime(t *testing.T) {
	t.Parallel()
	path, err := filepath.Abs(filepath.Join("run", "console_timers.pts"))
	assert.NilError(t, err)
	got := runPts(t, path)
	assert.Equal(t, got.exitCode, 0, "stderr:\n%s", got.stderr)
	lines := strings.Split(strings.TrimSuffix(got.stdout, "\n"), "\n")
	patterns := []string{`^loop: \d+(\.\d{1,3})?ms halfway \{ n: 1 \}$`, `^loop: \d+(\.\d{1,3})?ms$`, `^default: \d+(\.\d{1,3})?ms$`}
	assert.Equal(t, len(lines), len(patterns), got.stdout)
	for i, pattern := range patterns {
		assert.Assert(t, regexp.MustCompile(pattern).MatchString(lines[i]), "line %d: %q", i+1, lines[i])
	}
	assert.Assert(t, strings.Contains(got.stderr, "No such label 'missing' for console.timeEnd()"), got.stderr)
	assert.Assert(t, strings.Contains(got.stderr, "Label 'twice' already exists for console.time()"), got.stderr)
}
