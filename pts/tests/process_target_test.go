package tests

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/microsoft/typescript-go/pts/driver"
	"gotest.tools/v3/assert"
)

func TestProcessNamesItsTargetAndResolvesItsExecutableLink(t *testing.T) {
	t.Parallel()
	platform, arch := runtime.GOOS, runtime.GOARCH
	if arch == "amd64" {
		arch = "x64"
	}
	switch target := os.Getenv("PTS_TARGET"); target {
	case "":
	case "x86_64-linux":
		platform, arch = "linux", "x64"
	case "aarch64-linux":
		platform, arch = "linux", "arm64"
	case "aarch64-macos":
		platform, arch = "darwin", "arm64"
	default:
		t.Fatalf("no process identity expectation for PTS_TARGET=%q", target)
	}
	dir := t.TempDir()
	exe := filepath.Join(dir, "identity")
	assert.NilError(t, driver.Build(t.Context(), "testdata/h36_memory_usage/identity.pts", exe))
	link := filepath.Join(dir, "identity-link")
	assert.NilError(t, os.Symlink(exe, link))
	real, err := filepath.EvalSymlinks(exe)
	assert.NilError(t, err)

	got := execute(t, dir, nil, link)

	assert.Equal(t, got.exitCode, 0, got.stderr)
	assert.Equal(t, got.stdout, platform+" "+arch+"\ntrue true true\n"+real+"\ntrue true\n")
}
