package tests

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"image/png"
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

// TestExampleMandelbrot runs examples/mandelbrot (D-164) under one thread and four: the
// files are the same, Go's image/png decodes them (so the stored-deflate stream, the CRCs
// and Adler-32 are right), and a point of the main cardioid is black where a corner isn't.
func TestExampleMandelbrot(t *testing.T) {
	t.Parallel()
	exe := buildExample(t, "mandelbrot")
	dir := t.TempDir()
	var files [][]byte
	for _, threads := range []string{"1", "4"} {
		path := filepath.Join(dir, "set"+threads+".png")
		cmd := exec.CommandContext(t.Context(), exe, path, "240", "160")
		cmd.Env = append(os.Environ(), "PTS_THREADS="+threads)
		out, err := cmd.CombinedOutput()
		assert.NilError(t, err, "%s", out)
		assert.Assert(t, strings.HasPrefix(string(out), path+": 240×160, rendered in "), string(out))
		file, err := os.ReadFile(path)
		assert.NilError(t, err)
		files = append(files, file)
	}
	assert.Assert(t, bytes.Equal(files[0], files[1]), "the image depends on the number of threads")
	image, err := png.Decode(bytes.NewReader(files[0]))
	assert.NilError(t, err)
	assert.Equal(t, image.Bounds().Dx(), 240)
	assert.Equal(t, image.Bounds().Dy(), 160)
	black := func(x, y int) bool {
		r, g, b, _ := image.At(x, y).RGBA()
		return r == 0 && g == 0 && b == 0
	}
	assert.Assert(t, black(154, 80), "(-0.25, 0), in the main cardioid, isn't black")
	assert.Assert(t, !black(0, 0), "a corner, far outside the set, is black")
	colors := map[[3]uint32]bool{}
	for y := 0; y < 160; y += 4 {
		for x := 0; x < 240; x += 4 {
			r, g, b, _ := image.At(x, y).RGBA()
			colors[[3]uint32{r, g, b}] = true
		}
	}
	assert.Assert(t, len(colors) > 50, "only %d colors", len(colors))
}
