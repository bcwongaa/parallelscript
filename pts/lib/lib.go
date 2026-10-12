// Package lib holds ParallelScript's own library: the prelude, a declaration file of
// globals, and the pts: modules, sources in ParallelScript that are compiled with the
// programs that import them (D-116). The front end reads them from memory.
package lib

import (
	"embed"
	"io/fs"
	"strings"

	"github.com/microsoft/typescript-go/internal/vfs"
	"github.com/microsoft/typescript-go/internal/vfs/wrapvfs"
)

//go:embed *.d.ts *.pts
var files embed.FS

// scheme is a URL root, which tspath treats like a drive, so lib paths never clash with real files.
const scheme = "ptslib:///"

// PreludePath is the path of the prelude inside a file system wrapped by WrapFS.
const PreludePath = scheme + "prelude.d.ts"

// ModulePaths maps the pts: modules' specifiers to their sources, as the compiler
// option paths: `pts:path` is the source ptslib:///path.pts.
var ModulePaths = map[string][]string{"pts:*": {scheme + "*.pts"}}

// IsLibPath reports whether path names one of ParallelScript's declaration files.
func IsLibPath(path string) bool {
	return strings.HasPrefix(path, scheme) && strings.HasSuffix(path, ".d.ts")
}

// IsModulePath reports whether path names the source of a pts: module.
func IsModulePath(path string) bool {
	return strings.HasPrefix(path, scheme) && strings.HasSuffix(path, ".pts")
}

// GlobalModules name the globals that are a pts: module's default export, with the module:
// the global `process` is pts:process's (D-116).
var GlobalModules = map[string]string{"process": "pts:process"}

// ModulePath is the path of the source of the pts: module named by specifier (`pts:path`).
func ModulePath(specifier string) string {
	return scheme + strings.TrimPrefix(specifier, "pts:") + ".pts"
}

// ModuleSpecifier is the specifier of the pts: module whose source is at path
// (`pts:path` for ptslib:///path.pts), or path itself for any other file.
func ModuleSpecifier(path string) string {
	if !IsModulePath(path) {
		return path
	}
	return "pts:" + strings.TrimSuffix(strings.TrimPrefix(path, scheme), ".pts")
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
