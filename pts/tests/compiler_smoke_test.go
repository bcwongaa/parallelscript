package tests

// TestCompilerFailsCleanly checks that the compiler never runs into a bug or hangs on a
// program, valid or not (H04, D-179): it compiles to C in process (driver.EmitCWith, no clang)
// every test program and mutants made from them by fixed-seed edits, and fails listing
// each distinct internal compiler error (or panic outside the compiler's boundary, or
// hang) with the smallest program that hits it. With PTS_SMOKE_CLANG=1 it also asks clang
// to check the C of each program that compiled. It compiles with lower.AllInstances alone,
// and fails for a seed that compiles only by leaving out speculative instances that fail
// (lower.Narrowing, C176), but those in needsNarrowing, or only with the driver's fallback
// to lower.DeclaredInstances (D-190), but those in needsFallback.

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"maps"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"runtime/debug"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/microsoft/typescript-go/pts/diagnostic"
	"github.com/microsoft/typescript-go/pts/driver"
	"github.com/microsoft/typescript-go/pts/lower"
	"github.com/microsoft/typescript-go/pts/toolchain"
	"gotest.tools/v3/assert"
)

const (
	smokeMutantsVar     = "PTS_SMOKE_MUTANTS" // mutants per seed program (default defaultSmokeMutants; 0 for the seeds alone)
	smokeSeedVar        = "PTS_SMOKE_SEED"    // the mutations' random seed (default 1)
	smokeFilterVar      = "PTS_SMOKE_FILTER"  // a regexp the seed programs' paths must match
	smokeClangVar       = "PTS_SMOKE_CLANG"   // "1": also check the C of each program that compiled with clang -fsyntax-only
	smokeJobsVar        = "PTS_SMOKE_JOBS"    // programs compiled at once (default min(GOMAXPROCS, maxHeavyJobs))
	smokeOutVar         = "PTS_SMOKE_OUT"     // a directory to write each failure's program to
	defaultSmokeMutants = 1
	smokeCompileTimeout = time.Minute // a compile running longer is reported as a hang
)

// needsNarrowing are the seeds, by the end of their path, that compile with
// lower.AllInstances only by leaving out the speculative instances whose failures the
// lowering's trials don't take back (driver.EmitCNarrowedWith, C176), and needsFallback
// those that compile only with the driver's fallback to a later one of lower.Fallbacks
// (D-190). D-201 removes impossible pairs from nine former entries; those seeds now
// compile with AllInstances alone, which the smoke test requires without an exemption.
// D-205 removes the unrelated-adapter exemption; D-206 removes three exact-receiver exemptions.
var (
	needsNarrowing = []string{"/run/generic_instances_fall_back.pts",
		"/run/generic_fallback_spares_unrelated_call_site.pts", "/run/generic_fallback_spares_unrelated_relaxed_cast.pts",
		"/run/generic_fallback_spares_unrelated_keyed_call_sites.pts",
		"/run/generic_fallback_spares_unrelated_optional_call_site.pts",
		"/run/generic_fallback_spares_unrelated_several.pts",
		"/run/generic_fallback_spares_unrelated_total.pts", "/run/generic_fallback_spares_unrelated_value_call_site.pts",
		"/run/generic_fallback_blames_the_trial_whose_code_failed.pts", "/run/generic_fallback_keeps_value_pairings_for_by_name_casts.pts",
		"/run/generic_fallback_spares_unrelated_constant_key_site.pts", "/run/generic_fallback_spares_unrelated_dispatched_rest_site.pts",
		"/run/generic_fallback_spares_other_instances_at_one_call.pts", "/run/generic_fallback_spares_other_class_instances_at_one_call.pts",
		"/run/generic_fallback_spares_other_cast_instances_at_one_call.pts", "/run/generic_fallback_spares_other_method_owners.pts",
		"/run/generic_containing_value_pairing.pts"}
	needsFallback = []string{"/run/generic_fallback_keeps_value_pairings_for_by_name_totals.pts"}
)

// smokeProgram is one program the smoke test compiles: a seed, or a mutant of one.
type smokeProgram struct {
	seed     string // the seed's path
	mutation string // how the mutant was made from the seed ("" for the seed itself)
	source   string
}

func (p smokeProgram) name() string {
	if p.mutation == "" {
		return p.seed
	}
	return p.seed + " [" + p.mutation + "]"
}

// smokeFailure is how compiling a program went wrong.
type smokeFailure struct {
	kind    string // "internal error", "panic", "hang", "clang" or "fallback"
	key     string // what tells distinct failures apart: the error and the compiler's frame that raised it
	detail  string // the panic's frames, or clang's first errors
	program smokeProgram
}

func TestCompilerFailsCleanly(t *testing.T) {
	programs := smokePrograms(t)
	started := time.Now()
	failures, compiled := compileAll(t, programs)
	elapsed := time.Since(started)
	t.Logf("compiled %d programs (%d compiled to C) in %v, %v each on average, %d jobs",
		len(programs), compiled, elapsed.Round(time.Millisecond), (elapsed / time.Duration(max(len(programs), 1))).Round(time.Millisecond), smokeJobs())
	if len(failures) == 0 {
		return
	}
	report := smokeReport(t, failures)
	t.Errorf("%d programs failed to compile cleanly, %d distinct failures:\n%s", len(failures), strings.Count(report, "\n--- "), report)
}

// smokePrograms are the seeds (the run, error, corpus and docs programs) and their mutants.
func smokePrograms(t *testing.T) []smokeProgram {
	filter := regexp.MustCompile(os.Getenv(smokeFilterVar))
	perSeed := envInt(t, smokeMutantsVar, defaultSmokeMutants)
	seed := uint64(envInt(t, smokeSeedVar, 1))
	var seeds []string
	for _, dir := range []string{"run", "errors", docExamples} {
		seeds = append(seeds, testFiles(t, dir)...)
	}
	corpus, err := filepath.Glob("corpus/*/*.pts")
	assert.NilError(t, err)
	for _, path := range corpus {
		abs, err := filepath.Abs(path)
		assert.NilError(t, err)
		seeds = append(seeds, abs)
	}
	var programs []smokeProgram
	for _, path := range seeds {
		if !filter.MatchString(path) {
			continue
		}
		source, err := os.ReadFile(path)
		assert.NilError(t, err)
		programs = append(programs, smokeProgram{seed: path, source: string(source)})
		if importsRelative(string(source)) {
			continue // a mutant outside the seed's directory couldn't find its modules
		}
		programs = append(programs, mutants(path, string(source), perSeed, seed)...)
	}
	return programs
}

var relativeImport = regexp.MustCompile(`from\s+["']\.\.?/`)

func importsRelative(source string) bool { return relativeImport.MatchString(source) }

// compileAll compiles every program, some at once, and returns the failures and how many
// programs compiled to C.
func compileAll(t *testing.T, programs []smokeProgram) ([]smokeFailure, int) {
	dir := t.TempDir()
	cc := smokeClangChecker(t)
	var (
		mu       sync.Mutex
		failures []smokeFailure
		compiled int
		wg       sync.WaitGroup
	)
	work := make(chan int)
	for range smokeJobs() {
		wg.Go(func() {
			for i := range work {
				c, failure := compileProgram(filepath.Join(dir, "p"+strconv.Itoa(i)), programs[i])
				if failure == nil && c != "" && cc != nil {
					failure = cc.check(filepath.Join(dir, "p"+strconv.Itoa(i)), programs[i], c)
				}
				mu.Lock()
				if c != "" {
					compiled++
				}
				if failure != nil {
					failures = append(failures, *failure)
				}
				mu.Unlock()
			}
		})
	}
	for i := range programs {
		work <- i
	}
	close(work)
	wg.Wait()
	return failures, compiled
}

func smokeJobs() int {
	if jobs, err := strconv.Atoi(os.Getenv(smokeJobsVar)); err == nil && jobs > 0 {
		return jobs
	}
	return min(runtime.GOMAXPROCS(0), maxHeavyJobs)
}

// compileProgram compiles one program to C in its own directory (dir) and returns the C,
// or "" if it had compile errors, and its failure if it panicked or hung.
func compileProgram(dir string, program smokeProgram) (string, *smokeFailure) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		panic(err)
	}
	path := filepath.Join(dir, "main.pts")
	if program.mutation == "" {
		path = program.seed // in place, for its modules
	} else if err := os.WriteFile(path, []byte(program.source), 0o644); err != nil {
		panic(err)
	}
	type outcome struct {
		c       string
		failure *smokeFailure
	}
	done := make(chan outcome, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil { // outside EmitC's containment
				done <- outcome{failure: panicFailure(r, debug.Stack(), program)}
			}
		}()
		c, err := driver.EmitCWith(context.Background(), path, lower.AllInstances) // without EmitC's fallback, which would hide what only its instances hit (D-190)
		var internal *diagnostic.InternalError
		var compileErr *diagnostic.Error
		switch {
		case errors.As(err, &internal):
			done <- outcome{failure: internalFailure(internal, program)}
		case err != nil && !errors.As(err, &compileErr):
			panic(fmt.Sprintf("EmitC failed without diagnostics: %v", err))
		default:
			done <- outcome{c: c, failure: fallbackFailure(path, program, err)}
		}
	}()
	select {
	case o := <-done:
		return o.c, o.failure
	case <-time.After(smokeCompileTimeout):
		return "", &smokeFailure{kind: "hang", key: "hang", detail: "compiling took longer than " + smokeCompileTimeout.String(), program: program}
	}
}

// fallbackFailure is a seed's failure if it compiles only by narrowing or with the driver's
// fallback and isn't in needsNarrowing or needsFallback, or is and no longer needs that;
// err is its compile errors with lower.AllInstances, if any.
func fallbackFailure(path string, program smokeProgram, err error) *smokeFailure {
	if program.mutation != "" {
		return nil
	}
	listed := func(seeds []string) bool {
		return slices.ContainsFunc(seeds, func(end string) bool { return strings.HasSuffix(program.seed, end) })
	}
	narrowing, fallback := listed(needsNarrowing), listed(needsFallback)
	failure := func(key string) *smokeFailure {
		detail := ""
		if err != nil {
			detail = strings.SplitN(err.Error(), "\n", 2)[0]
		}
		return &smokeFailure{kind: "fallback", key: key, detail: detail, program: program}
	}
	if err == nil {
		if narrowing || fallback {
			return failure("compiles with AllInstances alone: remove it from needsNarrowing or needsFallback")
		}
		return nil
	}
	if _, narrowed := driver.EmitCNarrowedWith(context.Background(), path, lower.AllInstances); narrowed == nil {
		switch {
		case fallback:
			return failure("compiles by narrowing (C176): move it from needsFallback to needsNarrowing")
		case !narrowing:
			return failure("compiles only by leaving out speculative instances that fail (C176)")
		}
		return nil
	}
	if narrowing {
		return failure("no longer compiles by narrowing (C176): remove it from needsNarrowing")
	}
	if _, declared := driver.EmitCWith(context.Background(), path, lower.DeclaredInstances); declared != nil || fallback {
		return nil // an error either way, or listed
	}
	return failure("compiles only with the driver's fallback (D-190)")
}

// compilerFrame matches a stack frame's location in the compiler: pts/<package>/<file>.go:<line>
// (not pts/diagnostic, whose Locate records an internal error's stack).
var compilerFrame = regexp.MustCompile(`/(pts/(?:check|lower|cgen|ir|frontend|driver|numeric)/[a-z_]+\.go:\d+)`)

func panicFailure(r any, stack []byte, program smokeProgram) *smokeFailure {
	return stackFailure("panic", fmt.Sprint(r), string(stack), program)
}

// internalFailure is an internal compiler error EmitC returned: its message, and the
// compiler's frames in the panic's stack.
func internalFailure(internal *diagnostic.InternalError, program smokeProgram) *smokeFailure {
	return stackFailure("internal error", internal.Message, internal.Detail, program)
}

func stackFailure(kind string, message string, stack string, program smokeProgram) *smokeFailure {
	frames := compilerFrame.FindAllStringSubmatch(stack, 4)
	sites := make([]string, len(frames))
	for i, frame := range frames {
		sites[i] = frame[1]
	}
	key := message
	if len(sites) > 0 {
		key += " @ " + sites[0]
	}
	return &smokeFailure{kind: kind, key: key, detail: strings.Join(sites, " < "), program: program}
}

// ---------------------------------------------------------------- clang

// smokeClang checks generated C with clang -fsyntax-only against the runtime's headers.
type smokeClang struct {
	cc          string
	includeDirs []string
}

func smokeClangChecker(t *testing.T) *smokeClang {
	if os.Getenv(smokeClangVar) != "1" {
		return nil
	}
	tc, err := toolchain.New()
	assert.NilError(t, err)
	rt, err := tc.Runtime(t.Context(), toolchain.Release)
	assert.NilError(t, err)
	return &smokeClang{cc: tc.CC, includeDirs: rt.IncludeDirs}
}

var clangError = regexp.MustCompile(`(?m)error: (.*)$`)

func (s *smokeClang) check(dir string, program smokeProgram, c string) *smokeFailure {
	file := filepath.Join(dir, "main.c")
	if err := os.WriteFile(file, []byte(c), 0o644); err != nil {
		panic(err)
	}
	args := []string{"-fsyntax-only", "-std=c11", "-w", "-ferror-limit=3"}
	for _, include := range s.includeDirs {
		args = append(args, "-I", include)
	}
	out, err := exec.Command(s.cc, append(args, file)...).CombinedOutput()
	if err == nil {
		return nil
	}
	first := clangError.FindStringSubmatch(string(out))
	key := "clang"
	if first != nil {
		key = "clang: " + clangMessageShape(first[1])
	}
	return &smokeFailure{kind: "clang", key: key, detail: firstLines(string(out), 6), program: program}
}

var clangQuoted = regexp.MustCompile(`'[^']*'`)

// clangMessageShape is a clang error without its quoted names, which differ between programs.
func clangMessageShape(message string) string { return clangQuoted.ReplaceAllString(message, "'…'") }

// ---------------------------------------------------------------- reporting

// smokeReport describes each distinct failure once, with how many programs hit it and the
// smallest of them, written to PTS_SMOKE_OUT if it is set.
func smokeReport(t *testing.T, failures []smokeFailure) string {
	byKey := map[string][]smokeFailure{}
	for _, f := range failures {
		byKey[f.key] = append(byKey[f.key], f)
	}
	keys := slices.Sorted(maps.Keys(byKey))
	out := os.Getenv(smokeOutVar)
	var report strings.Builder
	for i, key := range keys {
		group := byKey[key]
		smallest := slices.MinFunc(group, func(a, b smokeFailure) int {
			return cmp.Or(cmp.Compare(len(a.program.source), len(b.program.source)), strings.Compare(a.program.name(), b.program.name()))
		})
		fmt.Fprintf(&report, "\n--- %s (%d programs)\n    %s\n    smallest: %s\n", key, len(group), smallest.detail, smallest.program.name())
		fmt.Fprintf(&report, "    seeds: %s\n", strings.Join(seedsOf(group), ", "))
		if out != "" {
			file := filepath.Join(out, fmt.Sprintf("failure%02d.pts", i+1))
			assert.NilError(t, os.MkdirAll(out, 0o755))
			header := fmt.Sprintf("// %s\n// %s\n// from %s\n", key, smallest.detail, smallest.program.name())
			assert.NilError(t, os.WriteFile(file, []byte(header+smallest.program.source), 0o644))
			fmt.Fprintf(&report, "    written to %s\n", file)
		}
	}
	return report.String()
}

// seedsOf names the seeds of a group's programs, each once, relative to the test directory.
func seedsOf(group []smokeFailure) []string {
	wd, _ := os.Getwd()
	var seeds []string
	for _, f := range group {
		seed, err := filepath.Rel(wd, f.program.seed)
		if err != nil {
			seed = f.program.seed
		}
		if !slices.Contains(seeds, seed) {
			seeds = append(seeds, seed)
		}
	}
	slices.Sort(seeds)
	return seeds
}

func firstLines(s string, n int) string {
	lines := strings.SplitN(s, "\n", n+1)
	return strings.Join(lines[:min(n, len(lines))], "\n    ")
}

func envInt(t *testing.T, name string, fallback int) int {
	value := os.Getenv(name)
	if value == "" {
		return fallback
	}
	n, err := strconv.Atoi(value)
	assert.NilError(t, err, "%s=%q", name, value)
	return n
}

// ---------------------------------------------------------------- mutations

// A mutation edits a program's lines, picking what to edit with r; ok is false if the
// program has nothing it applies to.
type mutation struct {
	name  string
	apply func(lines []string, r *rand.Rand) (edited []string, where string, ok bool)
}

// mutations are the edits mutants are made by. The first ones mostly break the program
// (its compile must then fail with diagnostics); the last ones mostly keep it valid
// TypeScript while sending it down the compiler's dynamic paths.
var mutations = []mutation{
	{"delete line", deleteLine},
	{"truncate after line", truncateAfterLine},
	{"duplicate line", duplicateLine},
	{"swap lines", swapLines},
	{"annotation to any", annotationToAny},
	{"const as any", constAsAny},
	{"annotation to unknown", annotationToUnknown},
}

// mutants makes n mutants of a seed's source, the same ones on every run for one seed value.
func mutants(path string, source string, n int, seed uint64) []smokeProgram {
	hash := fnv.New64a()
	hash.Write([]byte(filepath.Base(path)))
	r := rand.New(rand.NewPCG(seed, hash.Sum64()))
	lines := strings.Split(source, "\n")
	var made []smokeProgram
	for attempt := 0; len(made) < n && attempt < 4*n; attempt++ {
		m := mutations[r.IntN(len(mutations))]
		edited, where, ok := m.apply(slices.Clone(lines), r)
		if !ok {
			continue
		}
		made = append(made, smokeProgram{seed: path, mutation: m.name + " " + where, source: strings.Join(edited, "\n")})
	}
	return made
}

// codeLines are the indexes of the lines that hold code, not blank or comment lines.
func codeLines(lines []string) []int {
	var code []int
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" && !strings.HasPrefix(trimmed, "//") {
			code = append(code, i)
		}
	}
	return code
}

func pick(r *rand.Rand, indexes []int) (int, bool) {
	if len(indexes) == 0 {
		return 0, false
	}
	return indexes[r.IntN(len(indexes))], true
}

func deleteLine(lines []string, r *rand.Rand) ([]string, string, bool) {
	i, ok := pick(r, codeLines(lines))
	return slices.Delete(lines, i, i+1), strconv.Itoa(i + 1), ok
}

func truncateAfterLine(lines []string, r *rand.Rand) ([]string, string, bool) {
	var boundaries []int // lines that end a statement or open or close a block
	for _, i := range codeLines(lines) {
		if end := strings.TrimSpace(lines[i]); strings.HasSuffix(end, ";") || strings.HasSuffix(end, "{") || strings.HasSuffix(end, "}") {
			boundaries = append(boundaries, i)
		}
	}
	i, ok := pick(r, boundaries)
	return lines[:i+1], strconv.Itoa(i + 1), ok
}

func duplicateLine(lines []string, r *rand.Rand) ([]string, string, bool) {
	i, ok := pick(r, codeLines(lines))
	return slices.Insert(lines, i, lines[i]), strconv.Itoa(i + 1), ok
}

func swapLines(lines []string, r *rand.Rand) ([]string, string, bool) {
	code := codeLines(lines)
	if len(code) < 2 {
		return nil, "", false
	}
	k := r.IntN(len(code) - 1)
	i, j := code[k], code[k+1]
	lines[i], lines[j] = lines[j], lines[i]
	return lines, fmt.Sprintf("%d,%d", i+1, j+1), true
}

// typeAnnotation matches the type of a declaration's or parameter's annotation, or of a
// function's result, up to the `=`, `,`, `)` or `{` after it: `: number[]`, `): Map<K, V>`.
var typeAnnotation = regexp.MustCompile(`([A-Za-z0-9_\])?]\s*):\s*([A-Za-z_][A-Za-z0-9_.]*(?:<[^=(){};]*?>)?(?:\[\])*)(\s*[=,){;])`)

func annotationToAny(lines []string, r *rand.Rand) ([]string, string, bool) {
	return replaceAnnotation(lines, r, "any")
}

func annotationToUnknown(lines []string, r *rand.Rand) ([]string, string, bool) {
	return replaceAnnotation(lines, r, "unknown")
}

func replaceAnnotation(lines []string, r *rand.Rand, with string) ([]string, string, bool) {
	type site struct{ line, start, end int }
	var sites []site
	for _, i := range codeLines(lines) {
		for _, m := range typeAnnotation.FindAllStringSubmatchIndex(lines[i], -1) {
			if lines[i][m[4]:m[5]] != with {
				sites = append(sites, site{i, m[4], m[5]})
			}
		}
	}
	if len(sites) == 0 {
		return nil, "", false
	}
	s := sites[r.IntN(len(sites))]
	where := fmt.Sprintf("%d:%s", s.line+1, lines[s.line][s.start:s.end])
	lines[s.line] = lines[s.line][:s.start] + with + lines[s.line][s.end:]
	return lines, where, true
}

var constDeclaration = regexp.MustCompile(`^(\s*(?:export\s+)?(?:const|let)\s+[A-Za-z_][A-Za-z0-9_]*)(\s*=)`)

// constAsAny gives an unannotated declaration the type any: `const x = e` → `const x: any = e`.
func constAsAny(lines []string, r *rand.Rand) ([]string, string, bool) {
	var sites []int
	for _, i := range codeLines(lines) {
		if constDeclaration.MatchString(lines[i]) {
			sites = append(sites, i)
		}
	}
	i, ok := pick(r, sites)
	if !ok {
		return nil, "", false
	}
	lines[i] = constDeclaration.ReplaceAllString(lines[i], "$1: any$2")
	return lines, strconv.Itoa(i + 1), true
}
