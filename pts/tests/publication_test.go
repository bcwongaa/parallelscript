package tests

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/microsoft/typescript-go/pts/driver"
	"github.com/microsoft/typescript-go/pts/toolchain"
	"gotest.tools/v3/assert"
)

// The publication checker (toolchain.PublicationCheck, pts/runtime/src/publication_check.c)
// ends a program with publicationReport on stderr where a thread makes a new object
// reachable from memory other threads can read before a store-store barrier orders the
// object's fill before that store (ARCHITECTURE §4.13 rule 6, H35b). x86-64 keeps stores
// in order by itself and arm64 doesn't, so the checker is how x86-64 tests the barriers.
const publicationReport = "pts publication violation"

var publicationThreads = []string{"1", "2", "4"}

// publicationSample are run and corpus programs that also run under the checker, chosen to
// cover what makes objects: classes, closures, generators, async functions, collections,
// JSON, RegExp, strings, errors, typed arrays and the parallel APIs. PTS_BUILD_PROFILE=
// publication runs every program of the suite under it instead.
var publicationSample = []string{
	"run/arrays_objects.pts", "run/array_methods.pts", "run/async_functions.pts", "run/async_generators.pts",
	"run/channel.pts", "run/classes.pts", "run/closures.pts", "run/closures_gc.pts", "run/error_classes.pts",
	"run/generators_basics.pts", "run/generators_delegate.pts", "run/json_parse.pts", "run/json_stringify.pts",
	"run/map_entries.pts", "stress/promises_cross_loop.pts", "run/sync.pts",
	"stress/race_arrays.pts", "stress/race_fields.pts", "stress/race_maps.pts", "stress/race_strings.pts",
	"stress/tasks_gc.pts",
	"corpus/algorithms/dijkstra_binary_heap.pts", "corpus/algorithms/lru_cache_map.pts",
	"corpus/algorithms/trie_autocomplete.pts", "corpus/data/csv_rows_objects.pts",
	"corpus/data/json_roundtrip_orders.pts", "corpus/data/mini_query_engine.pts",
}

// TestNewObjectsReachOtherThreadsFilled runs programs that publish each kind of new object
// to other threads (publication/*.pts, the stress litmus race_publication.pts) and
// publicationSample under the checker on 1, 2 and 4 threads: none may publish an object
// before its fill, and each must print what it prints without the checker.
func TestNewObjectsReachOtherThreadsFilled(t *testing.T) {
	t.Parallel()
	requirePublicationChecker(t)
	paths := testFiles(t, "publication")
	for _, sample := range append([]string{"stress/race_publication.pts"}, publicationSample...) {
		absolute, err := filepath.Abs(sample)
		assert.NilError(t, err)
		paths = append(paths, absolute)
	}
	for _, path := range paths {
		t.Run(publicationTestName(path), func(t *testing.T) {
			t.Parallel()
			spec := ensureSpec(t, path)
			exe := buildPublicationChecked(t, path)
			for _, threads := range publicationThreads {
				t.Run("threads="+threads, func(t *testing.T) {
					env := []string{"PTS_THREADS=" + threads}
					got := execute(t, filepath.Dir(path), env, exe)
					assert.Assert(t, !strings.Contains(got.stderr, publicationReport), "stderr:\n%s", got.stderr)
					want := expectedRun(t, spec, path, env)
					assert.Equal(t, got.stdout, want.stdout)
					assert.Equal(t, got.exitCode, want.exitCode, "stderr:\n%s", got.stderr)
				})
			}
		})
	}
}

// TestConstructionsReachOtherThreadsWhole runs the programs of publication/ that only make
// objects and publish them (`// publication: strict`: none is changed after its
// construction) under the checker's strict mode, where every store into a new object
// before its publication is part of its construction, with release stores ordering
// nothing for other paths (partial): a construction whose last store no barrier follows,
// in generated code or the runtime, is reported (D-226). The default mode lets later
// changes reach other threads late, which a program that changes objects may.
func TestConstructionsReachOtherThreadsWhole(t *testing.T) {
	t.Parallel()
	requirePublicationChecker(t)
	for _, path := range testFiles(t, "publication") {
		spec, err := readSpec(path)
		assert.NilError(t, err)
		if spec.publication == "" {
			continue
		}
		t.Run(testName(path), func(t *testing.T) {
			t.Parallel()
			exe := buildPublicationChecked(t, path)
			for _, threads := range spec.threads {
				t.Run("threads="+threads, func(t *testing.T) {
					got := execute(t, filepath.Dir(path), []string{"PTS_THREADS=" + threads, "PTS_PUBLICATION_CHECK=" + spec.publication + ",partial"}, exe)
					assert.Assert(t, !strings.Contains(got.stderr, publicationReport), "stderr:\n%s", got.stderr)
					assert.Equal(t, got.stdout, *spec.stdout)
					assert.Equal(t, got.exitCode, spec.exitCode, "stderr:\n%s", got.stderr)
				})
			}
		})
	}
}

// TestPublicationCheckerAcceptsObjectsNoOtherThreadSees pins that the checker doesn't
// report new objects of every kind of construction that are stored only into locals and
// into other new objects, which no other thread can reach (publication/private_objects.pts;
// pts/toolchain's TestPublicationChecker* pin what it reports), and that it saw them.
func TestPublicationCheckerAcceptsObjectsNoOtherThreadSees(t *testing.T) {
	t.Parallel()
	requirePublicationChecker(t)
	path, err := filepath.Abs("publication/private_objects.pts")
	assert.NilError(t, err)
	exe := buildPublicationChecked(t, path)

	got := execute(t, filepath.Dir(path), []string{"PTS_PUBLICATION_CHECK=stats"}, exe)

	assert.Equal(t, got.stdout, "414760\n")
	assert.Equal(t, got.exitCode, 0, "stderr:\n%s", got.stderr)
	assert.Assert(t, strings.Contains(got.stderr, "pts publication check: ") && !strings.Contains(got.stderr, " 0 allocations"),
		"the checker saw the program's objects:\n%s", got.stderr)
}

// expectedRun is what the program at path must print: its spec's expected output, or for a
// program that states none (a corpus program, which Node decides), what it prints built
// without the checker.
func expectedRun(t *testing.T, spec testSpec, path string, env []string) runResult {
	t.Helper()
	if spec.stdout != nil {
		return runResult{stdout: *spec.stdout, exitCode: spec.exitCode}
	}
	return runPts(t, path, env...)
}

func publicationTestName(path string) string {
	return filepath.Base(filepath.Dir(path)) + "/" + testName(path)
}

// buildPublicationChecked builds the program at source under the publication checker and
// returns the executable's path.
func buildPublicationChecked(t *testing.T, source string) string {
	t.Helper()
	heavyJobs <- struct{}{}
	defer func() { <-heavyJobs }()
	c, err := driver.EmitC(t.Context(), source)
	assert.NilError(t, err)
	tc, err := toolchain.New()
	assert.NilError(t, err)
	rt, err := tc.Runtime(t.Context(), toolchain.PublicationCheck)
	assert.NilError(t, err)
	cFile := filepath.Join(t.TempDir(), "main.c")
	assert.NilError(t, os.WriteFile(cFile, []byte(c), 0o644))
	exe := filepath.Join(t.TempDir(), "publication_"+strings.TrimSuffix(filepath.Base(source), ".pts"))
	assert.NilError(t, tc.CompileProgram(t.Context(), rt, []string{cFile}, exe))
	return exe
}

// requirePublicationChecker skips the test where the publication checker doesn't build: it
// links bdwgc's allocators and pthread's locks through GNU ld's --wrap, which Apple's linker
// doesn't have, so it runs on Linux only (D-226, H36).
func requirePublicationChecker(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("the publication checker builds on Linux only")
	}
}
