package tests

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/microsoft/typescript-go/pts/driver"
	"github.com/microsoft/typescript-go/pts/toolchain"
	"gotest.tools/v3/assert"
)

func TestMemoryUsageKeepsHeapCountersFromOneSnapshot(t *testing.T) {
	t.Parallel()
	if runtime.GOOS != "linux" {
		t.Skip("controlled collector growth uses GNU ld's --wrap; the normal API fixture runs everywhere")
	}
	tc, err := toolchain.New()
	assert.NilError(t, err)
	rt, err := tc.Runtime(t.Context(), toolchain.Release)
	assert.NilError(t, err)
	dir := t.TempDir()
	c, err := driver.EmitC(t.Context(), "testdata/h36_memory_usage/snapshot.pts")
	assert.NilError(t, err)
	cFile := filepath.Join(dir, "snapshot.c")
	assert.NilError(t, os.WriteFile(cFile, []byte(c), 0o644))
	shim, err := filepath.Abs("testdata/h36_memory_usage/grow_at_rss.c")
	assert.NilError(t, err)
	cc := filepath.Join(dir, "wrapped-cc")
	quotedCompiler := "'" + strings.ReplaceAll(tc.CC, "'", "'\"'\"'") + "'"
	assert.NilError(t, os.WriteFile(cc, []byte("#!/bin/sh\nexec "+quotedCompiler+" -Wl,--wrap=uv_resident_set_memory \"$@\"\n"), 0o755))
	tc.CC = cc
	exe := filepath.Join(dir, "snapshot")
	assert.NilError(t, tc.CompileProgram(t.Context(), rt, []string{cFile, shim}, exe))

	got := execute(t, dir, []string{"PTS_THREADS=1"}, exe)

	assert.Equal(t, got.exitCode, 0, got.stderr)
	assert.Assert(t, strings.Contains(got.stderr, "test grew collector by "), "the growth interposition did not run: %s", got.stderr)
	assert.Equal(t, got.stdout, "true true true\n", "heap size and free bytes must be sampled under the same collector lock; stderr:\n%s", got.stderr)
}
