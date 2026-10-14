# ParallelScript independent review

Snapshot: `f2323ccf4`, 2026-10-13. Read-only review; no tracked edits, commits, pushes, or coordination with the other reviewers. Repros and recorded outputs are in `review-gpt-probes/`; its README gives rerun commands.

## Verdict: **yes-with-caveats**

This is a functional native-language prototype, not merely a collection of milestone demonstrations.
Three independently written, roughly 200-line programs performed useful work successfully.
Two compiled unchanged and matched Node; the HTTP service worked after correcting two mistakes in my own code.
The earlier four crash repros no longer crash, and I found no new SIGSEGV in the bounded probes.
However, independent thread-local AbortControllers fail under concurrency, and suspension expressions still produce wrong results.
HTTP framing also needs hardening before exposing services through intermediaries.
M0–M7 can reasonably count as a usable first version; “DONE” must not mean correctness work is finished or M8 should be performance-only.

## Build, baseline, and claim checks

Environment: Go 1.26.9, clang 18.1.3, Node 24.21.0; `GOMAXPROCS=2`, private cache, `PTS_HOME` this checkout, `PTS_THREADS≤4`, Go package parallelism 1/test parallelism 2.

- `go build -p 1 -o /tmp/pts-review-gpt/pts ./cmd/pts` succeeded; version was `pts 0.7.0 (typescript-go 7.0.2)`; README hello printed `Hello, ParallelScript`. First build attempt hit my 10-minute task limit; the longer retry succeeded.
- Capped baseline passed: **10 tested packages, 0 failed; 1274 pass/247 skip test-result events** (includes parents/subtests), 19 no-test packages. Command: `go test -json -p 1 -parallel 2 -timeout 40m -skip='.*/.*/threads=([5-9]|[1-9][0-9]+)$' ./pts/... ./cmd/pts/`. High-thread cases were intentionally omitted; no full upstream suite was run. I stopped an earlier run after noticing its incorrect skip regex allowed eight-thread subtests; only the corrected run supplies these counts. Corpus execution was **262 pass/38 skip**, versus inventory's 267/33: five ordinary programs are falsely excluded (finding 7). All five passed separate manual Node comparisons, bringing the independently verified total to **267**.
- GUIDE tutorial printed **142879 four times**. `pts test docs/examples/testing` passed **4/4**; an intentional failing assertion was located correctly and exited **1**.
- Twenty targeted probes cover any boundaries, microtasks, closures, destructuring, mutable collections, Unicode/surrogates, generics, coercion, finally/generators, integer edges, optional evaluation, freezing, channels/locks, parallel exceptions, races, HTTP, and competing checked errors. Eleven final plain-TS comparisons matched Node byte-for-byte. Initial failures and reviewer corrections are retained, rather than silently rewritten away. The packaged rerun was also executed in fresh scratch: the successful applications/comparisons passed again and the reported defects reproduced (`review-gpt-probes/validation/`).
- Two short `hyperfine -r 3` checks: startup **2.6 ms PTS / 183.4 ms Node**; fib(38) **559.5 ms / 1680 ms**, same answer. Shared-host load and type stripping make these unsuitable replacement headline numbers, but the direction of both claims holds. Checked-in M6 JSON independently contains the advertised **1.206/72.921 ms** startup and **254.273/636.308 ms** fib means. `ldd` showed only libc/libm plus the loader, supporting the native-dependency claim.

## Scorecard

| Area | Assessment | Evidence |
|---|---|---|
| Core TS, classes, collections, Unicode | **Usable with gaps** | Ledger, telemetry, and 11 differential probes pass; default destructuring and higher-order specialization still fail below. |
| Async, exceptions, generators | **Usable with gaps** | Microtask/finally probes and HTTP client pass; suspension snapshots remain wrong. |
| Real parallel work and synchronization | **Usable with gaps** | One/four-thread telemetry agrees; channel sum 4950, Once once, protected counter 1100; hidden AbortSignal race. |
| Universal race-safety assurance | **Weak** | Bounded container/typed-array probes do not crash or tear; collecting generated programs are outside selected C-only TSan coverage. |
| Files/process/HTTP stdlib | **Usable with gaps** | Real CSV/NDJSON I/O and CRUD service work; invalid HTTP framing accepted. |
| CLI/testing/editor | **Usable with gaps** | Test pass/fail exits work; LSP TS2322/PTS2002/PTS3001 work; extension packages; shutdown and lowering-diagnostic gaps. |
| Tests and public guidance | **Solid, not proof of completeness** | Broad differential corpus, useful guide and explicit differences; skips and platform assumptions still matter. |
| Ecosystem/portability | **Missing by declared scope** | No npm, HTTPS/body streams, FFI, or non-x86-64-Linux target; not newly discovered M7 failures. |

### Architecture and fork

The Go frontend → structured typed IR → C11 pipeline is credible, and readable generated C has source `#line` mappings. The fork is restrained: against `typescript/v7.0.2`, `internal/` changes **22 files, +482/−26 lines**; inspected hooks are PTS-marked and recorded in `docs/FORK.md`. Rebase risks center on the parser flag and global LSP hooks (`internal/ast/nodeflags.go:48`, `pts/lsp/lsp.go:37`).

Emitter-owned async/generator state machines are a **recorded owner-authorized deferral**, not a fresh failure (orchestrator ruling, `docs/agent/OWNER_ANSWERS.md:5,11`). They nevertheless concentrate semantic risk. IR walkers have silent unknown-node fallbacks (`pts/ir/walk.go:59`); future additions need exhaustive traversal tests. Lowering/emission total about **24.6k hand-maintained lines**. Some representation/effect metadata relies on spelling or shape: `Partial<Record<…>>` alias names (`pts/lower/records.go:18`), intersection property-name coverage (`pts/lower/lower.go:3488`), and builtin purity prefixes (`pts/cgen/order.go:41`). These are maintenance risks, **unverified as runtime defects**, not evidence that all user names spoof builtins; most recognizers check declaration provenance. The corpus regex below is a demonstrated heuristic failure. No genuinely dead implementation was established. An unchecked nonfinite range-count cast (`pts/runtime/src/pool.c:450`) is another static validation concern; no associated runtime crash was verified.

## New-user experience

I wrote these from README/GUIDE before reading the program tests:

1. **`review-gpt-probes/ledger-v1.pts` — 221-line expense CLI.** Quoted CSV, exact cents, Unicode normalization, generic grouping, sorted summaries, JSON/text output, invalid records and usage errors. Compiled first try. Five accepted/two rejected rows totaled **5250 cents**; JSON/text and exit codes matched Node. A quoted final field also matched. No workaround required.
2. **`review-gpt-probes/task-service-v1.pts` → `task-service.pts` — 204/207-line HTTP JSON service/client.** Mutex-protected tasks, atomic counters, concurrent creates, optimistic updates, validation and deletion. My `(error: Error)` callback was rightly rejected: the API supplies `unknown`; narrowing fixed it. My `server.url + '/tasks'` made a doubled slash and 404s; trimming the base fixed it. These are reviewer mistakes, not compiler findings. Final one/four-thread runs gave **12 creates, update statuses [200,409], completed=1, invalid/malformed=400, delete=204 with empty body, missing=404, 20 requests**.
3. **`review-gpt-probes/telemetry-v1.pts` — 206-line parallel NDJSON processor.** Async file reads, validated C/F readings, per-worker maps/sets/histograms, deterministic merge, sorted JSON. Compiled first try. **777 readings, 7 sensors, 3 groups, 4 rejected**; both one- and four-thread report files were byte-identical to the sequential Node twin.

Diagnostics generally have good source locations and actionable numeric/guardrail messages. Two probe mistakes were also ordinary tooling issues: a broad string key violated strict TS indexing; Node strip-only cannot erase parameter properties. Narrowing the key and writing an explicit constructor field restored valid comparisons. VS Code typechecking, four grammar tests and VSIX packaging passed in a scratch copy; actual GUI installation/completion UX is **unverified**.

## Findings, ranked

### 1. High — Independent AbortControllers race on library-global permission state

`PTS_THREADS=4 pts run review-gpt-probes/abort-local-min.pts` constructs four batches of independent controllers. First recheck: **2776/40000** constructions threw **`TypeError: Illegal constructor`**; two more runs also failed. `PTS_THREADS=1` had **0 failures**. No controller is shared. `pts/lib/internal/abort.pts:205–214` toggles one global `constructing` flag; another thread can clear it before the constructor checks at `:226–229`.

**Recommendation:** replace mutable global permission with a library-only construction token/factory; add concurrent independent-controller regressions, including reusePort handlers and fetch setup. Users should not have to serialize unrelated controllers.

### 2. High — Accepted suspension expressions still select the wrong values

`review-gpt-probes/previous/06_residual.pts:8,18,28` prints **accessor 12; any 12; method new 10**; Node prints **11; 11; old 10**. Compound targets/method lookup are evaluated after suspension instead of before it. The source explicitly excludes accessor/dynamic targets (`pts/lower/yield_expressions.go:393`). This is already disclosed in SPEC §8, but remains a substantive functional limitation, not optimization debt.

**Recommendation:** complete pre-suspension reference/value snapshots for getter/any compound assignments and object method calls, retaining receiver/argument order; test both yield and await.

### 3. Medium — Valid generic factories and generic instanceof can throw incorrect type errors

`review-gpt-probes/previous/08_residual.pts:9` passes an inline generic factory to a higher-order function: PTS throws **`Expected Box<number>, got Box<any>`**, Node prints **`box 9`**. Named factories now work. Specialization registration is restricted in `pts/lower/closures.go:164`. Separately, running the skipped LRU corpus program throws **`Expected MapLRU<any, any>, got MapLRU<string, number>`** at its line 117; Node with parameter-property transformation completes normally (`review-gpt-probes/known-lru.pts`).

**Recommendation:** preserve specialization metadata through generic callable values/inline expressions; make generic instanceof narrowing recognize valid specialized instances. Preserve intentional checked-any errors rather than removing boundary checks.

### 4. Medium — HTTP request framing accepts invalid/ambiguous messages

`python3 -I review-gpt-probes/http-framing-compare.py` sends bounded local requests. PTS answers **200** to CL+TE, `Transfer-Encoding: chunked, gzip`, unsupported TE plus CL, and `Content-Length: +2`; Node's HTTP server answers **400** to all four. After CL+TE, PTS also processes a second pipelined request on the same connection. `pts/runtime/src/http.c:364–378` uses permissive `strtoll` and mere membership of “chunked”, without full framing validation.

**Recommendation:** validate digit-only lengths and ordered transfer codings; reject ambiguous framing, or implement RFC 9112's required close behavior. Add independent wire tests. This establishes parser disagreement, **not a demonstrated proxy exploit**. HEAD/204 framing, bad chunks, oversized-body rejection, eight slow clients and 64 bounded ordinary connections passed.

### 5. Medium — Exact-undefined object defaults fail compilation

`review-gpt-probes/destructuring-undefined-min.pts` is `const { value = 4 } = { value: undefined }; console.log(value);`. Node prints **4**; PTS emits **PTS1001: converting undefined to number is not supported yet** at 1:9. `pts/lower/optional.go:72–75` handles unions but returns an exact undefined value without applying its default. Annotating the source member `number | undefined` works.

**Recommendation:** apply the default to exact undefined as well as union cases, preserving getter/read side effects; cover binding, assignment and parameter patterns. The LSP currently reports no error for this source, so document or surface lowering limitations before build.

### 6. Medium — “Never crashes with races” exceeds demonstrated assurance

`pts/toolchain/runtime_test.go:98–109` runs TSan on four selected C tests **with GC disabled**; it does not cover generated code or `.pts` stdlib concurrency such as finding 1. `docs/ARCHITECTURE.md:403–421` expressly relies on clang/x86 assumptions for plain C races. No new memory corruption was reproduced; this is an assurance gap, not a claim that bounded stress proves unsafety.

**Recommendation:** add generated-program concurrency tests with collecting GC, randomized schedules and supported clang-version gates; keep the existing platform constraint and state test coverage honestly. Treat changed compiler optimization behavior as a safety regression risk.

### 7. Medium — Corpus feature detection silently skips five ordinary programs

The green baseline actually compared **262**, not inventory's **267**, corpus programs. `pts/tests/spec_test.go:122,136–142` strips comments but then treats words inside strings, and an `int` property name, as PTS syntax. It skips `debounce_throttle_clock`, `lcs_diff`, `word_search_puzzle`, `spell_suggest`, and `memento_game_save` as “uses ParallelScript features”; none needs such features. `review-gpt-probes/check-excluded-corpus.py` independently ran all five: all match Node. Thus 267 is true at this snapshot, but the suite is not enforcing it.

**Recommendation:** detect syntax with the parser/scanner, not a raw-text regex; derive the compatibility report from executed results and test that every classified-compatible program is actually compared.

### 8. Low — Graceful LSP exit is reported as failure

The corrected `review-gpt-probes/lsp-smoke.py` receives the shutdown response, then sends a parameterless `exit` while stdin remains open. Server exits **1**, stderr **`pts: context canceled`**. `cmd/pts/cli.go:144,357` forwards cancellation as command failure; `internal/lsp/server.go:1313–1314` implements exit via EOF/cancellation. Diagnostics themselves work.

**Recommendation:** distinguish successful protocol shutdown from interrupted/failed sessions, and add a subprocess test requiring exit 0 without forcing stdin EOF.

## Previous review follow-up

All eleven findings were rechecked; original small repros, fingerprints, commands and outputs are in `review-gpt-probes/previous/verification_report.txt` and `concise_output.txt`.

| Previous # | Status | Current evidence |
|---|---|---|
| 1 Foreign timer clear | **Fixed** | Original prints `cleared`, exit 0; owner-loop marshaling. |
| 2 Captured-union narrowing crash | **Fixed for safety** | Catchable/uncaught TypeError instead of SIGSEGV; Node divergence explicitly documented, SPEC:594. |
| 3 Captured TDZ crash | **Fixed** | `ReferenceError`, matching Node. |
| 4 Coercion ABI crash | **Fixed** | `7,7`, matching Node. |
| 5 Thenable getters | **Fixed** | Captured callable/getter errors now match Node. |
| 6 Suspension snapshots | **Partly fixed** | Original fixed; getter/any/method residuals remain (finding 2). |
| 7 Iterator protocol | **Partly fixed** | done-before-value and closing fixed; typed getter-result still PTS1001 (`review-gpt-probes/previous/07_residual.pts:5`). |
| 8 Generic factories | **Partly fixed** | Named factory works; inline residual fails (finding 3). |
| 9 Parallel restriction/reduce initial | **Fixed** | Formerly restricted operand works; original reduce gives `7998010 7998010`; my initial=100 probe gives 110. |
| 10 Race safety | **Partly addressed** | Atomic typed arrays/foreign generators/rejections fixed; universal assurance remains unestablished. |
| 11 Green-suite interpretation | **Partly addressed** | Classification improved; known bug still skipped by `pts/tests/run_test.go:246–254`; TSan scope unchanged. |

## Top 10 next actions for M8

1. Remove AbortSignal's hidden construction race; audit stdlib internal module-global mutable state.
2. Finish suspension snapshots before moving their implementation; add yield/await differential matrices.
3. Harden HTTP framing and connection-close policy with independent wire tests.
4. Fix inline higher-order generic specialization and generic instanceof narrowing; unskip the LRU bug when green.
5. Extend generated-program/stdlib race and collecting-GC coverage; gate supported clang versions.
6. Fix exact-undefined defaults and typed IteratorResult getter layouts; keep first-failure repros as regressions.
7. Introduce an explicit suspension/reference-sequencing IR pass, as already approved; make visitors exhaustive.
8. Expose lowering-only editor limitations, fix clean LSP exit, and run real VS Code lifecycle tests.
9. Replace corpus regex feature detection; execute every compatible case and known-bug expected-failure check; report compatibility categories separately.
10. Only then optimize measured allocation/bandwidth bottlenecks, using interleaved benchmarks and equivalence checks—not semantic shortcuts.
