package tests

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/microsoft/typescript-go/pts/driver"
	"gotest.tools/v3/assert"
)

// buildExample builds examples/<name>/main.pts and returns the executable's path.
func buildExample(t *testing.T, name string) string {
	t.Helper()
	source, err := filepath.Abs(filepath.Join("..", "..", "examples", name, "main.pts"))
	assert.NilError(t, err)
	exe := filepath.Join(buildDir, "example_"+name)
	heavyJobs <- struct{}{}
	err = driver.Build(context.Background(), source, exe)
	<-heavyJobs
	assert.NilError(t, err)
	return exe
}

// TestExampleHasher runs examples/hasher (D-165) on files whose sizes cover SHA-256's
// padding cases, a few megabytes of random bytes and a nested directory, under one thread
// and four, and compares its lines with Go's digests; a file that doesn't exist is
// reported on stderr with exit code 1.
func TestExampleHasher(t *testing.T) {
	t.Parallel()
	exe := buildExample(t, "hasher")
	dir := t.TempDir()
	random := rand.New(rand.NewPCG(1, 2))
	sizes := map[string]int{"empty": 0, "one": 1, "b55": 55, "b56": 56, "b63": 63, "b64": 64, "b65": 65, "b119": 119, "b120": 120, "nested/deeper/big": 3_000_001}
	var want []string
	for name, size := range sizes {
		data := make([]byte, size)
		for i := range data {
			data[i] = byte(random.Uint32())
		}
		path := filepath.Join(dir, name)
		assert.NilError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		assert.NilError(t, os.WriteFile(path, data, 0o644))
		digest := sha256.Sum256(data)
		want = append(want, hex.EncodeToString(digest[:])+"  "+path)
	}
	sortLinesByPath(want)
	for _, threads := range []string{"1", "4"} {
		cmd := exec.CommandContext(t.Context(), exe, dir)
		cmd.Env = append(os.Environ(), "PTS_THREADS="+threads)
		var stderr strings.Builder
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		assert.NilError(t, err, "stderr: %s", stderr.String())
		assert.DeepEqual(t, strings.Split(strings.TrimSuffix(string(out), "\n"), "\n"), want)
		assert.Assert(t, strings.HasPrefix(stderr.String(), fmt.Sprintf("hashed %d files, 3.0 MB in ", len(sizes))), stderr.String())
	}

	missing := filepath.Join(dir, "missing")
	cmd := exec.CommandContext(t.Context(), exe, filepath.Join(dir, "one"), missing)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	assert.ErrorContains(t, err, "exit status 1")
	assert.Assert(t, strings.HasSuffix(string(out), "  "+filepath.Join(dir, "one")+"\n"), string(out))
	assert.Assert(t, strings.Contains(stderr.String(), "hasher: ENOENT: no such file or directory, open '"+missing+"'"), stderr.String())
}

// sortLinesByPath sorts "<digest>  <path>" lines by path, as the hasher orders a directory's files.
func sortLinesByPath(lines []string) {
	pathOf := func(line string) string { return line[strings.Index(line, "  ")+2:] }
	slices.SortFunc(lines, func(a, b string) int { return strings.Compare(pathOf(a), pathOf(b)) })
}
