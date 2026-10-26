package toolchain

import (
	"os/exec"
	"regexp"
	"slices"
	"strings"
	"testing"

	"gotest.tools/v3/assert"
)

// bdwgcAllocator matches the bdwgc functions that allocate or free collected memory, and
// bdwgcSetup those of its matches that set allocation up instead (GC_init_gcj_malloc).
var (
	bdwgcAllocator = regexp.MustCompile(`^GC_\w*(malloc|realloc|free|strdup|strndup|memalign)\w*$`)
	bdwgcSetup     = regexp.MustCompile(`^GC_(init|set|get)_`)
)

func TestBuildProfileNamesAProfileOrReleaseWhenUnset(t *testing.T) {
	for name, want := range map[string]Profile{"": Release, "release": Release, "sanitize": Sanitize, "tsan": ThreadSanitize, "publication": PublicationCheck} {
		t.Setenv(BuildProfileVar, name)
		got, err := BuildProfile()
		assert.NilError(t, err)
		assert.Equal(t, got, want, "%s=%q", BuildProfileVar, name)
	}
	t.Setenv(BuildProfileVar, "fast")
	_, err := BuildProfile()
	assert.ErrorContains(t, err, `PTS_BUILD_PROFILE: unknown profile "fast": pts builds with release, sanitize, tsan or publication`)
}

// The publication checker sees only the allocations it wraps (H35b), so every bdwgc
// function the runtime calls to allocate or free must be one of publicationWraps.
func TestPublicationWrapsCoverEveryAllocatorTheRuntimeCalls(t *testing.T) {
	t.Parallel()
	tc, err := New()
	assert.NilError(t, err)
	rt, err := tc.Runtime(t.Context(), Release)
	assert.NilError(t, err)
	runtimeArchive := rt.Archives[0]
	assert.Assert(t, strings.HasSuffix(runtimeArchive, "/libptsrt.a"), runtimeArchive)

	out, err := exec.CommandContext(t.Context(), "nm", "--undefined-only", "--format=just-symbols", runtimeArchive).Output()
	assert.NilError(t, err)

	var called []string
	for _, symbol := range strings.Fields(string(out)) {
		if bdwgcAllocator.MatchString(symbol) && !bdwgcSetup.MatchString(symbol) && !slices.Contains(called, symbol) {
			called = append(called, symbol)
			assert.Assert(t, slices.Contains(publicationWraps, symbol), "the runtime calls %s, which the publication checker doesn't wrap", symbol)
		}
	}
	assert.Assert(t, slices.Contains(called, "GC_malloc"), "nm found the runtime's calls: %q", called)
}
