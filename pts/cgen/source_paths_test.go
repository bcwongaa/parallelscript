package cgen_test

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/microsoft/typescript-go/pts/cgen"
	"github.com/microsoft/typescript-go/pts/ir"
	"gotest.tools/v3/assert"
)

func TestLineDirectivesResolveSourceFileAndDirectoryLinks(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	realDir := filepath.Join(dir, "real")
	assert.NilError(t, os.Mkdir(realDir, 0o755))
	entry := filepath.Join(realDir, "a \"b\"\\c.pts")
	assert.NilError(t, os.WriteFile(entry, []byte("console.log(1);\n"), 0o644))
	real, err := filepath.EvalSymlinks(entry)
	assert.NilError(t, err)
	fileLink := filepath.Join(dir, "file.pts")
	dirLink := filepath.Join(dir, "directory")
	chainLink := filepath.Join(dir, "chain")
	assert.NilError(t, os.Symlink(entry, fileLink))
	assert.NilError(t, os.Symlink(realDir, dirLink))
	assert.NilError(t, os.Symlink(dirLink, chainLink))

	for name, path := range map[string]string{
		"file":      fileLink,
		"directory": filepath.Join(dirLink, filepath.Base(entry)),
		"chain":     filepath.Join(chainLink, filepath.Base(entry)),
	} {
		t.Run(name, func(t *testing.T) {
			main := &ir.Func{Name: "main", Pos: ir.Pos{File: path, Line: 7}, Result: ir.Void, Body: &ir.Block{}}

			got := cgen.Emit(programWithMain(main))

			assert.Assert(t, strings.Contains(got, "#line 7 "+strconv.Quote(real)+"\n"), got)
			assert.Assert(t, !strings.Contains(got, "#line 7 "+strconv.Quote(path)+"\n"), got)
		})
	}
}

func TestLineDirectivesKeepUnresolvableSourceNames(t *testing.T) {
	t.Parallel()
	for _, path := range []string{"ptslib:///process.pts", "missing-relative.pts", filepath.Join(t.TempDir(), "missing.pts")} {
		t.Run(path, func(t *testing.T) {
			main := &ir.Func{Name: "main", Pos: ir.Pos{File: path, Line: 7}, Result: ir.Void, Body: &ir.Block{}}

			got := cgen.Emit(programWithMain(main))

			assert.Assert(t, strings.Contains(got, "#line 7 "+strconv.Quote(path)+"\n"), got)
		})
	}
}
