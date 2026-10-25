package tests

import (
	"bufio"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"testing"

	"github.com/microsoft/typescript-go/pts/driver"
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

// TestOutputToAReaderThatLeftEndsTheProgram: the runtime ignores SIGPIPE, so that a socket
// whose peer has reset fails its own write only (C350), but output to stdout or stderr
// whose reader has gone (a program's output into `| head`) still ends the program by
// SIGPIPE, as a C or Go program's does and pts's did before; Node goes on, ignoring the error.
func TestOutputToAReaderThatLeftEndsTheProgram(t *testing.T) {
	t.Parallel()
	for name, write := range map[string]string{
		"console.log":          `console.log("line", i);`,
		"process.stdout.write": `process.stdout.write("line " + i + "\n");`,
		"console.error":        `console.error("line", i);`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			source := filepath.Join(t.TempDir(), "main.pts")
			assert.NilError(t, os.WriteFile(source, []byte("for (let i = 0; i < 5_000_000; i++) {\n  "+write+"\n}\n"), 0o644))
			exe := filepath.Join(buildDir, "output_reader_gone_"+strings.ReplaceAll(name, ".", "_"))
			heavyJobs <- struct{}{}
			err := driver.Build(context.Background(), source, exe)
			<-heavyJobs
			assert.NilError(t, err)
			cmd := exec.CommandContext(t.Context(), exe)
			pipe, err := cmd.StdoutPipe()
			if name == "console.error" {
				pipe, err = cmd.StderrPipe()
			}
			assert.NilError(t, err)
			assert.NilError(t, cmd.Start())
			line, err := bufio.NewReader(pipe).ReadString('\n')
			assert.NilError(t, err)
			assert.Equal(t, line, "line 0\n")
			assert.NilError(t, pipe.Close())
			err = cmd.Wait()
			var exit *exec.ExitError
			assert.Assert(t, errors.As(err, &exit), "the program ended by itself: %v", err)
			status := exit.Sys().(syscall.WaitStatus)
			assert.Assert(t, status.Signaled() && status.Signal() == syscall.SIGPIPE, "the program ended with %v, not by SIGPIPE", exit)
		})
	}
}
