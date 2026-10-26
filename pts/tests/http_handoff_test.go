package tests

import (
	"path/filepath"
	"testing"

	"github.com/microsoft/typescript-go/pts/toolchain"
	"gotest.tools/v3/assert"
)

func TestMacOSHandoffBeforeListenerStartup(t *testing.T) {
	t.Parallel()
	tc, err := toolchain.New()
	assert.NilError(t, err)
	rt, err := tc.Runtime(t.Context(), toolchain.Release)
	assert.NilError(t, err)
	rt.IncludeDirs = append(rt.IncludeDirs, filepath.Join(tc.Home, "pts/runtime/src"))
	dir := t.TempDir()
	exe := filepath.Join(dir, "handoff")
	assert.NilError(t, tc.CompileProgram(t.Context(), rt, []string{"testdata/h36_handoff/handoff.c"}, exe))

	for _, mode := range []string{"startup", "stopped"} {
		t.Run(mode, func(t *testing.T) {
			got := execute(t, dir, nil, exe, mode)

			assert.Equal(t, got.exitCode, 0, "stdout: %s\nstderr: %s", got.stdout, got.stderr)
			if mode == "startup" {
				assert.Equal(t, got.stdout, "mode=startup before: listening=0 stopped=0; after: connection_kept=1 duplicate_open=1\n")
			} else {
				assert.Equal(t, got.stdout, "mode=stopped before: listening=0 stopped=1; after: connection_kept=0 duplicate_open=0\n")
			}
		})
	}
}
