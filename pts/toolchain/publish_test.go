package toolchain_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"testing"

	"gotest.tools/v3/assert"
)

// publishSource fills a new object, publishes it with pts_publish and stores its pointer
// where other threads load it, as generated code does at the end of a construction (D-226).
const publishSource = `#include "pts.h"
typedef struct fresh { const void *type; double number; void *pointer; } fresh;
fresh *published;
void fill_and_publish(fresh *object, void *pointer) {
  object->number = 1.5;
  object->pointer = pointer;
  pts_publish();
  published = object;
}
`

// On aarch64, which may make stores visible to other cores in any order, pts_publish is a
// store barrier between the stores that fill an object and the store that publishes it.
func TestPublishIsAStoreBarrierOnAarch64(t *testing.T) {
	t.Parallel()
	requireAarch64(t)
	lines := assembly(t, "aarch64-linux-gnu")

	barrier := slices.IndexFunc(lines, func(line string) bool { return line == "dmb ishst" })
	assert.Assert(t, barrier >= 0, "no dmb ishst in:\n%s", strings.Join(lines, "\n"))
	publishing := indexOfMatch(lines, `^str x0, \[x\d+, :lo12:published\]$`)
	assert.Assert(t, publishing > barrier, "the publishing store isn't after the barrier:\n%s", strings.Join(lines, "\n"))
	fills := matching(lines, `^(str|stp|stur) .*\[x0, #\d+\]$`)
	assert.Assert(t, len(fills) > 0 && slices.Max(fills) < barrier, "the filling stores aren't all before the barrier:\n%s", strings.Join(lines, "\n"))
}

// On x86-64, whose stores other cores see in program order, pts_publish adds no
// instruction: the filling stores, then the publishing store, as before (D-226).
func TestPublishAddsNoInstructionOnX86(t *testing.T) {
	t.Parallel()
	requireX86Headers(t)
	lines := assembly(t, "x86_64-linux-gnu")

	assert.Assert(t, len(matching(lines, `^(mfence|sfence|lfence|lock|xchg)`)) == 0, "a fence in:\n%s", strings.Join(lines, "\n"))
	publishing := indexOfMatch(lines, `^movq %rdi, published\(%rip\)$`)
	assert.Assert(t, publishing >= 0, "no publishing store in:\n%s", strings.Join(lines, "\n"))
	fills := matching(lines, `^mov[a-z]* \S+, \d+\(%rdi\)$`)
	assert.Assert(t, len(fills) == 2 && slices.Max(fills) < publishing, "the two filling stores aren't before the publishing store:\n%s", strings.Join(lines, "\n"))
}

// assembly compiles publishSource for clang's target triple as the toolchain compiles the
// runtime (-O2) and returns fill_and_publish's instructions, without directives or labels.
func assembly(t *testing.T, triple string) []string {
	t.Helper()
	tc := newToolchain(t)
	dir := t.TempDir()
	source := filepath.Join(dir, "publish.c")
	assert.NilError(t, os.WriteFile(source, []byte(publishSource), 0o644))
	args := []string{"--target=" + triple, "-O2", "-std=c11", "-S", "-o", "-"}
	for _, include := range []string{"pts/runtime/include", "pts/third_party/bdwgc/include"} {
		args = append(args, "-I", filepath.Join(tc.Home, include))
	}
	out, err := exec.CommandContext(t.Context(), tc.CC, append(args, source)...).Output()
	assert.NilError(t, err)
	var lines []string
	inside := false
	comment := "//" // aarch64's; x86-64's is #, which is aarch64's immediate
	if strings.HasPrefix(triple, "x86_64") {
		comment = "#"
	}
	for _, line := range strings.Split(string(out), "\n") {
		line, _, _ = strings.Cut(line, comment)
		line = strings.TrimSpace(line)
		switch {
		case line == "fill_and_publish:":
			inside = true
		case !inside || line == "" || strings.HasPrefix(line, ".") || strings.HasSuffix(line, ":"):
		case line == "ret" || line == "retq":
			return lines
		default:
			lines = append(lines, strings.Join(strings.Fields(line), " "))
		}
	}
	t.Fatalf("no fill_and_publish in:\n%s", out)
	return nil
}

// requireX86Headers skips the test on a machine of another processor that lacks the
// x86-64 C library's headers, which pts.h includes, as the arm64 CI runner does; the
// x86-64 job runs it.
func requireX86Headers(t *testing.T) {
	t.Helper()
	if runtime.GOARCH == "amd64" {
		return
	}
	probe := filepath.Join(t.TempDir(), "probe.c")
	assert.NilError(t, os.WriteFile(probe, []byte("#include <stdlib.h>\n"), 0o644))
	out, err := exec.CommandContext(t.Context(), newToolchain(t).CC, "--target=x86_64-linux-gnu", "-fsyntax-only", probe).CombinedOutput()
	if err != nil {
		t.Skipf("needs the x86-64 C library's headers: %s", strings.TrimSpace(string(out)))
	}
}

func indexOfMatch(lines []string, pattern string) int {
	return slices.IndexFunc(lines, regexp.MustCompile(pattern).MatchString)
}

// matching are the indexes of the lines that match pattern.
func matching(lines []string, pattern string) []int {
	re := regexp.MustCompile(pattern)
	var indexes []int
	for i, line := range lines {
		if re.MatchString(line) {
			indexes = append(indexes, i)
		}
	}
	return indexes
}
