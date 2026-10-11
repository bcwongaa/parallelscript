// Package toolchain builds the C runtime and its vendored libraries into a
// cache, and compiles and links generated C against them (D-O11).
package toolchain

import (
	"errors"
	"os"
	"path/filepath"
)

// runtimeMarker is a file that exists only in a PTS home: the directory that
// contains pts/runtime and pts/third_party.
const runtimeMarker = "pts/runtime/include/pts.h"

// FindHome locates the PTS home: $PTS_HOME if set, otherwise the nearest
// ancestor of the executable or of the working directory that contains the runtime.
func FindHome() (string, error) {
	if home := os.Getenv("PTS_HOME"); home != "" {
		if !isHome(home) {
			return "", errors.New("PTS_HOME=" + home + " does not contain " + runtimeMarker)
		}
		return home, nil
	}
	var starts []string
	if exe, err := os.Executable(); err == nil {
		if resolved, err := filepath.EvalSymlinks(exe); err == nil {
			starts = append(starts, filepath.Dir(resolved))
		}
	}
	if cwd, err := os.Getwd(); err == nil {
		starts = append(starts, cwd)
	}
	for _, start := range starts {
		if home, ok := findHomeAbove(start); ok {
			return home, nil
		}
	}
	return "", errors.New("cannot find the ParallelScript runtime sources; set PTS_HOME to the directory that contains " + runtimeMarker)
}

func findHomeAbove(dir string) (string, bool) {
	for {
		if isHome(dir) {
			return dir, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}

func isHome(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, runtimeMarker))
	return err == nil
}

// DefaultCacheDir is $PTS_CACHE_DIR, else the user cache dir plus "pts" (~/.cache/pts on Linux).
func DefaultCacheDir() (string, error) {
	if dir := os.Getenv("PTS_CACHE_DIR"); dir != "" {
		return dir, nil
	}
	base, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "pts"), nil
}
