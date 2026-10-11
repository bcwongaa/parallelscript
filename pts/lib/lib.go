// Package lib holds ParallelScript's own declaration files (the prelude now; the
// pts: modules later) and serves them to the front end from memory.
package lib

import (
	"embed"
	"io/fs"
	"strings"

	"github.com/microsoft/typescript-go/internal/vfs"
	"github.com/microsoft/typescript-go/internal/vfs/wrapvfs"
)

//go:embed *.d.ts
var files embed.FS

// scheme is a URL root, which tspath treats like a drive, so lib paths never clash with real files.
const scheme = "ptslib:///"

// PreludePath is the path of the prelude inside a file system wrapped by WrapFS.
const PreludePath = scheme + "prelude.d.ts"

// IsLibPath reports whether path names one of ParallelScript's lib files.
func IsLibPath(path string) bool {
	return strings.HasPrefix(path, scheme)
}

// WrapFS returns fs with the lib files readable at their ptslib:/// paths.
func WrapFS(inner vfs.FS) vfs.FS {
	return wrapvfs.Wrap(inner, wrapvfs.Replacements{
		FileExists: func(path string) bool {
			if name, ok := strings.CutPrefix(path, scheme); ok {
				_, err := fs.Stat(files, name)
				return err == nil
			}
			return inner.FileExists(path)
		},
		ReadFile: func(path string) (string, bool) {
			if name, ok := strings.CutPrefix(path, scheme); ok {
				content, err := files.ReadFile(name)
				return string(content), err == nil
			}
			return inner.ReadFile(path)
		},
		DirectoryExists: func(path string) bool {
			if name, ok := strings.CutPrefix(path, scheme); ok {
				return name == ""
			}
			return inner.DirectoryExists(path)
		},
		Stat: func(path string) vfs.FileInfo {
			if name, ok := strings.CutPrefix(path, scheme); ok {
				info, err := fs.Stat(files, name)
				if err != nil {
					return nil
				}
				return info
			}
			return inner.Stat(path)
		},
		Realpath: func(path string) string {
			if IsLibPath(path) {
				return path
			}
			return inner.Realpath(path)
		},
	})
}
