package tests

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/microsoft/typescript-go/pts/driver"
	"github.com/microsoft/typescript-go/pts/toolchain"
	"gotest.tools/v3/assert"
)

// A binding's ready flag (a module-scope binding's `_ready`, an early cell's
// PTS_CELL_READY, D-140) is set after its value. A thread that sees the flag set must see
// the value: on x86-64 the store order gives that by itself, on arm64 only a release store
// of the flag and an acquire load of it do (C357, D-226), which ThreadSanitizer's builds
// make as arm64's do. ThreadSanitizer models C11's orderings on any host, so it reports the
// race on the flag and the binding until they are there. A binding initialised with a
// literal holds it from the program's start, so its flag needs no order.
var readyFlagPrograms = map[string]string{
	"module_binding.pts": `function limitWhenReady(): number {
  for (;;) {
    try {
      return config.limit;
    } catch (e) {
      if (!(e instanceof ReferenceError)) throw e;
    }
  }
}
const waiting = parallel limitWhenReady();
let spin = 0;
for (let i = 0; i < 2_000_000; i++) spin += i % 7;
const config = { limit: 42 };
console.log(await waiting, spin > 0);
`,
	"literal_binding.pts": `function limitWhenReady(): number {
  for (;;) {
    try {
      return limit;
    } catch (e) {
      if (!(e instanceof ReferenceError)) throw e;
    }
  }
}
const waiting = parallel limitWhenReady();
let spin = 0;
for (let i = 0; i < 2_000_000; i++) spin += i % 7;
const limit = 42;
console.log(await waiting, spin > 0);
`,
	"early_cell.pts": `function waitFor(read: () => number): number {
  for (;;) {
    try {
      return read();
    } catch (e) {
      if (!(e instanceof ReferenceError)) throw e;
    }
  }
}
async function round(): Promise<number> {
  const read = () => config.limit;
  const waiting = parallel waitFor(read);
  let spin = 0;
  for (let i = 0; i < 2_000_000; i++) spin += i % 7;
  const config = { limit: 42 };
  return (await waiting) + (spin > 0 ? 0 : 1);
}
console.log(await round(), true);
`,
}

func TestReadyFlagsPublishTheirValuesToOtherThreads(t *testing.T) {
	t.Parallel()
	for name, source := range readyFlagPrograms {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), name)
			assert.NilError(t, os.WriteFile(path, []byte(source), 0o644))
			exe := buildThreadSanitized(t, path)

			got := execute(t, filepath.Dir(path), []string{"PTS_THREADS=2", "GC_DONT_GC=1", "TSAN_OPTIONS=halt_on_error=1"}, exe)

			assert.Equal(t, got.stdout, "42 true\n", "stderr:\n%s", got.stderr)
			assert.Equal(t, got.exitCode, 0, "stderr:\n%s", got.stderr)
			assert.Assert(t, !strings.Contains(got.stderr, "ThreadSanitizer"), "stderr:\n%s", got.stderr)
		})
	}
}

// buildThreadSanitized builds the program at source with ThreadSanitizer
// (toolchain.ThreadSanitize) and returns the executable's path. Run it with GC_DONT_GC=1:
// bdwgc's stop-the-world signals fail under TSan (D-122).
func buildThreadSanitized(t *testing.T, source string) string {
	t.Helper()
	heavyJobs <- struct{}{}
	defer func() { <-heavyJobs }()
	c, err := driver.EmitC(t.Context(), source)
	assert.NilError(t, err)
	tc, err := toolchain.New()
	assert.NilError(t, err)
	rt, err := tc.Runtime(t.Context(), toolchain.ThreadSanitize)
	assert.NilError(t, err)
	cFile := filepath.Join(t.TempDir(), "main.c")
	assert.NilError(t, os.WriteFile(cFile, []byte(c), 0o644))
	exe := filepath.Join(t.TempDir(), "tsan_"+strings.TrimSuffix(filepath.Base(source), ".pts"))
	assert.NilError(t, tc.CompileProgram(t.Context(), rt, []string{cFile}, exe))
	return exe
}
