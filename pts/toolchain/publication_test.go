package toolchain_test

import (
	"runtime"
	"strings"
	"testing"

	"github.com/microsoft/typescript-go/pts/toolchain"
	"gotest.tools/v3/assert"
)

// publicationViolationExit is the exit code of a program the publication checker stops.
const publicationViolationExit = 70

// The publication checker's cases (pts/runtime/tests/publication_cases.c, H35b): a new
// object stored where other threads read it before a store-store barrier is reported,
// with where it was made and stored; objects only private memory holds, a barrier, a
// release store the runtime announces, a lock held over the store, malloc's scratch
// memory, elements whose zero is a value (PTS_PUBLICATION_SLOTS) and a thread's own record
// (PTS_PUBLICATION_PRIVATE) are not.
func TestPublicationCheckerReportsOnlyObjectsPublishedBeforeTheirFill(t *testing.T) {
	t.Parallel()
	if runtime.GOOS != "linux" {
		t.Skip("the publication checker links through GNU ld's --wrap, on Linux only")
	}
	exe := buildC(t, toolchain.PublicationCheck, runtimeTestSource(t, "publication_cases.c"))
	for _, c := range []struct {
		name     string
		options  string // PTS_PUBLICATION_CHECK
		reported bool
		slot     string // what a report says the object was stored into
		madeBy   string // the function a report says made the object, if not new_box
		stdout   string
	}{
		{name: "unfenced", reported: true, slot: "global published+0"},
		{name: "fenced"},
		{name: "private", stdout: "3\n"},
		{name: "held_dirty", reported: true, slot: "global published+0"},
		{name: "mutation"},
		{name: "mutation", options: "strict", reported: true, slot: "global published+0"},
		{name: "release"},
		{name: "locked"},
		{name: "after_unlock", reported: true, slot: "global published+0"},
		{name: "compare_and_swap", reported: true, slot: "global published+0"},
		{name: "thread_end", reported: true, slot: "global published+0"},
		{name: "malloc_scratch", stdout: "2\n"},
		{name: "malloc_scratch", options: "malloc", reported: true, slot: "malloc's memory"},
		{name: "other_stack", reported: true, slot: "another thread's stack"},
		{name: "pushed"},
		{name: "pushed", options: "partial", reported: true, slot: "global published_array+0", madeBy: "with_room"},
		{name: "slots"},
		{name: "unmarked_slots", reported: true, slot: "global published+0", madeBy: "fill_after_barrier"},
		{name: "private_record"},
		{name: "shared_record", reported: true, slot: "word 2 of the heap object"},
	} {
		t.Run(c.name+"/"+c.options, func(t *testing.T) {
			t.Parallel()
			got := runWithEnv(t, exe, []string{"PTS_PUBLICATION_CHECK=" + c.options}, "", c.name)

			if !c.reported {
				assert.Equal(t, got.exitCode, 0, "stderr:\n%s", got.stderr)
				assert.Equal(t, got.stdout, c.stdout)
				return
			}
			assert.Equal(t, got.exitCode, publicationViolationExit, "stderr:\n%s", got.stderr)
			assert.Assert(t, strings.Contains(got.stderr, "pts publication violation"), "stderr:\n%s", got.stderr)
			madeBy := c.madeBy
			if madeBy == "" {
				madeBy = "new_box"
			}
			assert.Assert(t, strings.Contains(got.stderr, madeBy+" (") && strings.Contains(got.stderr, "publication_cases.c:"),
				"the report names where the object was made and stored:\n%s", got.stderr)
			assert.Assert(t, strings.Contains(got.stderr, c.slot), "the report names the slot:\n%s", got.stderr)
		})
	}
}
