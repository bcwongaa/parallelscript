# ParallelScript independent review (Opus), M0–M7

Snapshot: detached `f2323ccf4` (tag `m7`), 2026-10-13. No tracked file was changed. Scratch work was in `/tmp/pts-review-opus/`. Everything cited here is in `review-opus-probes/`, with a README on how to rerun it.

## Verdict

**Yes, with caveats.** ParallelScript is a working language for small-to-medium programs. I wrote three realistic programs from scratch, each about 290–390 lines: a CLI, an HTTP service with a `fetch` client, and a parallel data job. Two compiled on the first try and the third after two clearly explained fixes. All three gave correct output that, for the plain-TS parts, matched Node byte for byte, apart from one bug.

About 40 probes found **no runtime crash**: data races, GC under parallel load, stack overflow on every kind of thread, malformed and abusive HTTP traffic. The parallel features gave the same results at 1, 2 and 4 threads over 20 repeated runs. The previous review's four segfaults are gone.

The caveats are real, though:
- I found **three silent wrong-result bugs** in core built-ins: `flat`/`flatMap` and `sort` with `undefined`.
- One very common idiom, `const [k, v] = s.split(":")`, throws when `v` lands in an optional property.
- The compiler **panics with a Go stack trace** on `(rec[k] ??= []).push(x)` in generic code.
- Builds are slow: 5–11 s for a 300-line program.
- Several web globals are missing: `URL`, `structuredClone`, `TextEncoder`, `crypto`.

None of these blocks the language from being usable, but the first four make it untrustworthy in spots. An M8 should start with correctness, before any performance work.

## Baseline

- **Build:** `go build -o /tmp/pts-review-opus/pts ./cmd/pts` succeeded (5 min 41 s cold). `make build` and the README's Quick start worked as written. The first `pts run hello.pts` took 15.7 s (cold runtime cache), later runs 0.7 s.
- **Tests:** `GOMAXPROCS=2 go test -p 1 -count=1 -timeout 90m ./pts/... ./cmd/pts/` exited 0. All 10 tested packages were `ok`, including `pts/tests` (765.6 s). Log: `/tmp/pts-review-opus/baseline-test.log`.
- **Corpus:** 33 of the 300 corpus programs are `// skip:` (8 by design, 24 unsupported, 1 bug), which matches the README's 267/300.
- **Linking:** `ldd` of the binaries shows only libc and libm.

## Scorecard

| Area / milestone | Status | Evidence |
|---|---|---|
| §2.1 `.pts`, `parallel` keyword, compatibility principle | solid | Corpus 267/300. My plain-TS probes for async ordering, JSON/`any`, generics, closures, classes, Unicode, numbers, iterators, modules and a reducer (p02, p05, p06, p07, p09, p10, p12, p22, p26) match Node except the findings below. |
| §2.2 Fork | solid | `git diff --stat typescript/v7.0.2 HEAD -- internal`: 22 files, +482/−26, all in `docs/FORK.md`. Most of it is new files (`checker/pts.go`, `parser/pts.go`). Rebase risk is low. |
| §2.3 Pipeline, IR, readable C, `#line` | usable with gaps | The C is readable, with `#line` directives (1,805 in `analytics.c`). The state machines are still built in the emitter (D-109, deferred to M8). The front end takes 0.38 s and clang -O2 about 11 s on 23k lines of C (finding 7). |
| §2.4 Types and values | usable with gaps | Number formatting, UTF-16 semantics and the integer rules (p11: wrapping, saturation, `MIN/-1`, 64-bit shifts) are correct. Wrong results: findings 1–3. |
| §2.5 Race-safety invariant | solid (as tested) | p04 (array/Map/Set/union/string/record races; 20 runs at 4 threads), p15 (GC plus mutation during iteration; 10 runs): 0 crashes. Typed arrays now use relaxed atomics (`pts/runtime/include/pts.h:1595-1599`). |
| §2.6 Concurrency, `.par`, `parallel for`, `pts:sync` | solid | p16 (nested `.par`, tasks inside `.par`, cross-loop promises, unbuffered channels, async `Mutex`, 100k tasks, `join`): identical at 1/2/4 threads over 20 runs. p27: a throwing `withLock`/`Once` releases correctly. p13: timers cleared from other loops work. |
| §2.7 Modules and stdlib | usable with gaps | Imports, re-exports and cycles match Node (p22). Present: fs, path, process, http, fetch, RegExp, Date. Missing: `URL`, `URLSearchParams`, `structuredClone`, `TextEncoder`, `crypto`, `atob`, `Intl`, `process.stdin`, https (finding 9). |
| §4 Testing | usable with gaps | The suite is green and the differential corpus is strong, but skipped programs are never compiled, there's no panic guard, and TSan covers only runtime C tests (finding 10). |
| §5 Benchmarks | solid, honest | My spot checks on a loaded host: fib 3.21× Node (claimed 2.50×); hello starts in 2.4 ms vs Node's 77 ms; mandelbrot 3.20× from 1 to 4 threads (claimed 3.87×). Losses are disclosed. |
| M0 Bootstrap, M1 Core | solid | As above. |
| M2 Data and memory | usable with gaps | `flat` on union or tuple elements is wrong (finding 1). |
| M3 TypeScript feel | usable with gaps | Findings 2, 3 and 5. |
| M4 Async | solid | p02 (microtasks, timers, thenables, async generators, `finally`, `withResolvers`) matches Node. |
| M5 Parallel | solid | As §2.6. |
| M6 Real programs | usable with gaps | HTTP survives malformed input and RSTs, but has no timeouts (finding 6). No `URL` and no https. |
| M7 Editor and tooling | usable with gaps | `pts test` works like Node's reporter. The LSP works and the extension packages, but PTS1001 errors don't reach the editor and the compiler can panic (finding 4). |

## New-user experience

I followed README's Quick start and the GUIDE's tutorial literally, and both worked. Then I wrote three programs, reading only the GUIDE's API sections.

### 1. `programs/csvq/csvq.pts` (389 lines): CSV query CLI

It has quoted-field parsing, `--where`/`--group-by`/`--agg`/`--sort`/`--format`, and uses union types, `Record`, generics, regexes and custom errors.

- It **compiled first time**.
- I ran the 15 command lines in `cmds.txt` against Node (`pts:` replaced by `node:`, `--experimental-transform-types`). **14 matched exactly**: stdout, stderr and exit code.
- The fifth, `--group-by region --agg count,sum:units,…`, threw `TypeError: Expected string, got undefined` (finding 3). Its stack trace named the caller's line (`main`, :334) rather than `parseArgs`.
- A small `pts test` suite (`test/`) gave Node's spec report, a correct failure diff and exit 1.

### 2. `programs/todo_service/todo_service.pts` (285 lines): JSON service plus client

It is a router with `:id` patterns, uses `serve({ reusePort: true })`, keeps state in `Mutex<Map>` with `AtomicInt` ids, and has a client that uses `fetch`, `Promise.all`, `parallel` fetches and `AbortSignal.timeout`.

- It **compiled first time** and all 18 printed checks were correct; 40 concurrent POSTs got unique ids.
- **Load:** oha with 256 connections ran at about 16k req/s with 100% success on this busy host.
- **Malformed input:** garbage request lines, bad and negative Content-Length, and bad chunked bodies all got 400. A 100 KB header got 431, a 70 MB body 413, NUL bytes 404, and invalid UTF-8 was replaced with U+FFFD. Pipelining worked, and 4,000 RST connections leaked no file descriptors (34 open after the storm, and still 34 a second later).
- **Missing timeouts:** 3,000 slowloris connections stayed open indefinitely (finding 6).

### 3. `programs/analytics/analytics.pts` (286 lines): parallel analytics job

It parses 400k events with `.par.map` and builds the report five ways: `.par.map`, `.par.reduce`, `parallel` shards, a `Channel` pipeline, and `parallel for` with a `Mutex`. It also sorts with `join` and runs k-means.

- It hit **two compile errors**, both with clear messages:
  - `processed.add(batch.length)` is PTS2001 because `length` is a `number`, not an `int`.
  - `toLocaleString("en-US", {…})` is PTS1001 (documented).
- After those fixes, every strategy agreed with the sequential result at 1, 2 and 4 threads. The output also matched Node running the same file through sequential shims (`tools/tonode.sh`).
- **Wall time:** 3.6 s on 1 thread, 2.8 s on 4, Node 3.7 s. The job allocates heavily, and k-means with tuples and closures ran 2× slower than in Node.

### Friction on the way

- I fell twice into `parallel (async () => {…})()`. The hint names the problem and the fix, so I fixed it, but it remains a trap.
- `range()` has no `.map`.
- Several messages leak internal names, for example ``using a `object7` as a `{a: number; c: boolean}`…``.

## Findings

### 1. High: `flat` and `flatMap` silently under-flatten

This happens when the element type is a union or a tuple.

- `[[1,[2]],[3]].flat(2)` gives `[ 1, [ 2 ], 3 ]`; Node gives `[ 1, 2, 3 ]`.
- `[1,[2,[3,[4,[5]]]]].flat(1)` doesn't flatten at all.
- `nested.flatMap(x => x)` over `(number | number[])[]` doesn't flatten.
- `[["b",2],["a",1]].flat()` over a tuple array doesn't flatten.
- Through `any` it works.

Repro: `probes/p18_flat.pts`, `p23_array_unions.pts`.

Cause: `pts/lower/arrays.go:520-527` computes the result type by walking `*ir.Array` element types and stops at the first union or tuple. The runtime then flattens only that many levels.

**Recommendation:** flatten by runtime element kind, as the `any` path already does, and give the result the union element type. If that isn't possible, make it PTS1001. Add these cases to the corpus.

### 2. High: `sort` places `undefined` and `null` wrongly

- `[3, undefined, 1].sort()` gives `[ undefined, 1, 3 ]`; JS gives `[ 1, 3, undefined ]`.
- With a comparator, `undefined` is passed to it instead of going last.
- `[3, null, 1].sort()` gives `[ null, 1, 3 ]`; JS gives `[ 1, 3, null ]`.
- `toSorted` behaves the same.

Repro: `probes/p24_sort_undefined.pts`.

Cause: `element_string` (`pts/runtime/src/array.c:687-704`) gives undefined and null the key `""` (`join`'s rule, not `SortCompare`'s), and `pts_array_sort_with` moves only holes to the end (`:843-852`).

**Recommendation:** follow ECMA-262 SortCompare. `undefined` always goes last and is never passed to the comparator; `null` sorts as `"null"`.

### 3. High: destructured elements throw when stored in an optional property

```ts
interface Agg { kind: string; column?: string }
const [kind, column] = "count".split(":");
const agg: Agg = { kind, column };   // TypeError: Expected string, got undefined
```

Node stores `undefined`. This is the idiom that broke program 1. The loose-local rule (`pts/lower/loose_locals.go:10-16`, SPEC §8 "Loose locals") checks such a local wherever it's used as `T`, even when the target, an optional property, can hold `undefined`.

Repro: `probes/p01_split_destructure.pts`, `p28_stack_lines.pts`.

**Recommendation:** don't check a loose local where the target type admits `undefined`: optional properties, optional or `| undefined` parameters, and shorthand properties of such types. Add a corpus program built around `split` and destructuring.

### 4. High: the compiler panics with a Go stack trace

The trigger is a logical compound assignment on a hash-map element in erased generic code:
- `(acc[f(x)] ||= []).push(x)` in a generic arrow const;
- `(this.handlers[event] ??= []).push(h)` in a generic class over a mapped type.

The error is `panic: interface conversion: ir.Type is *ir.Primitive, not *ir.MapType` at `pts/lower/records.go:280`.

Repro: `probes/p08b_panic_mapped.pts`, `p08d_panic_propertykey.pts`, `p08f_generic_arrow_record.pts`. The same body as a generic `function` works (`p08g`).

This matters beyond the one bug:
- `pts build` has no `recover`; only the LSP does (`pts/lsp/lsp.go:110`).
- `pts/lower` contains about 190 single-value `.(*ir.X)` assertions.

**Recommendation:**
- Fix the dynamic-record case.
- Add a `recover` in the driver that prints `internal compiler error at file:line:col` plus a short "please report" note.
- Make "no panic on any program tsc accepts" a test target: run the error and skip corpora, and fuzzed variants, through `pts build`.

### 5. Medium: `const o: any = {}` can't gain properties

`o.name = "x"` throws `TypeError: Cannot add property name, object is not extensible`, with no hint. Objects from `JSON.parse("{}")` accept new properties, so the behaviour is inconsistent. It is documented (SPEC §8), but it's one of the most common JS-in-TS idioms.

Repro: `probes/p21_any_object.pts`.

**Recommendation:** make an empty or untyped literal that flows into `any` a hash-map object, as JSON objects are. At least, have the message suggest `Record<string, any>`.

### 6. Medium: the HTTP server has no timeouts

`pts/lib/http.pts` has no header, request, idle or keep-alive timeout (the only match for "timeout" is the 408 status text, `:656`). 3,000 half-sent requests held 3,000 connections open (`tools/slowloris.py`). Node's defaults are `headersTimeout` 60 s, `requestTimeout` 300 s and `keepAliveTimeout` 5 s. A client that half-closes loses its response (known issue, D-163).

**Recommendation:** add `serve({ idleTimeout, headersTimeout })` with Node-like defaults, and a cap on connections.

### 7. Medium: slow builds hurt the edit-run loop

| Program | Build time |
|---|---|
| `hello.pts` | 0.7 s |
| `csvq.pts` (389 lines) | 5.5 s |
| `analytics.pts` (286 lines) | 11.5 s |

`emit-c` for `analytics.pts` takes 0.38 s, so clang -O2 on its 23,317 lines of C is about 97% of the time. The included library code (`pts:parallel`, `pts:sync`) is recompiled every time.

**Recommendation:**
- Use `-O1` or `-O0` for `pts run` and `pts test`, keeping `-O2` for `pts build`.
- Cache compiled `pts:` library instances.
- Split the C into translation units and compile them in parallel.

### 8. Medium: PTS1001 on everyday idioms, with internal names in messages

Each of these hit me while writing ordinary code:
- `...(cond ? { due } : {})`: "spreading a {due: string} | {} into an object" (`p26b`);
- tagged templates and `String.raw` (`p17`);
- `{} as Pick<T, K>` builders, reported as ``using a `object7` as …`` (`p06b`);
- `this` parameters with `call`, `apply` and `bind` on plain functions;
- `const [a, , ...rest]` on a variadic tuple ("converting number | string to string");
- an iterator `next()` returning `cond ? {…} : {…}`, reported as "converting object2 | object9 to …" (`p12b`);
- classes inside functions, which rules out mixins ("values of type Inner are not supported yet");
- `copyWithin` and `concat(...arrays)`.

**Recommendation:** rank these by how often they appear in real code. Conditional spread and tagged templates come first. Never print `objectN`; print the source text of the type or expression.

### 9. Medium: missing web-platform globals

`URL`, `URLSearchParams`, `structuredClone`, `TextEncoder`/`TextDecoder`, `crypto.randomUUID`/`getRandomValues`, `atob`/`btoa` are all tsc TS2304 errors (`p19c`). `Intl.NumberFormat`, `Object.groupBy`, `setImmediate` and `WeakMap` are PTS1001 (`p19d`). Without `URL`, HTTP handlers have to parse URLs by hand, as the GUIDE admits. `process.stdin` is missing, though `readFileSync("/dev/stdin")` works (`p19`).

**Recommendation:** add `URL`/`URLSearchParams` (WHATWG parser), `structuredClone`, `TextEncoder`, `crypto.getRandomValues` and `randomUUID` first.

### 10. Medium: the test infrastructure would miss crash regressions

- A `// skip:` corpus program is never compiled or run (`pts/tests/run_test.go:251-252`). If one of the 33 regressed from a clean PTS1001 to a panic or a segfault, nothing would notice.
- `pts/lower` (19,619 lines) has 1,134 lines of unit tests; the rest of its coverage comes from end-to-end tests.
- TSan runs only the runtime's C tests, with the GC off (`pts/toolchain/runtime_test.go:107`), never generated parallel programs.

**Recommendation:** compile and run skipped programs too, asserting their documented outcome. Add a generated differential fuzzer: random typed expressions and array/string built-in calls compared with Node. That class of test would have caught findings 1 and 2.

### 11. Medium: integer footguns that pass silently

- `[250, 251, 252]` as a `u8[]`: `.reduce((s, v) => s + v, 0)` gives 241, because the accumulator becomes `u8`.
- `-1 as u32` is 0, since the literal is a `number`, which saturates.
- `9223372036854775807 as u64` rounds to 2^63.

Repro: `probes/p11_integers.pts`. All of this is documented in PROGRESS and SPEC, but none of it produces a warning.

**Recommendation:** make a literal operand of `as` exact for its target type. Emit a hint when a `reduce` accumulator gets a narrow integer type from a literal initial value.

### 12. Low

- **Unawaited task rejection:** an unawaited `parallel` task that rejects is reported only at exit (`p03`, item 9). A plain async rejection exits immediately, as in Node (`p03b`).
- **Eager zero-fill:** `new Array<number>(2**31)` zero-fills 16 GB (11 GB RSS, 34 s) where Node allocates nothing (D-085).
- **`Object.prototype` methods through `any`:** `toString` and `hasOwnProperty` are missing through `any` on typed objects (`p05b`), though JSON objects have them.
- **LSP gaps:**
  - PTS1001 isn't shown in the editor (documented, D-168).
  - `serverInfo` says `typescript-go 7.0.2`.
  - The server exited with code 1 after `shutdown` and `exit`; my script sent `exit` without waiting for the reply, so this is unverified.
- **Racing on a shared `Map` or `Set`** silently leaves duplicate keys (p04: `size > 1000` for 1,000 distinct keys). This is memory-safe and documented (D-172), but it's the kind of mistake a newcomer makes first. The owner's D-172 decision on a check for `push`/`set`/`add` on captured collections is worth taking.

## Previous review (2026-10-12) follow-up

I reran all 15 of its probes against this build (`docs/reviews/2026-10-12-gpt-6.1-sol/probes`).

| # | Finding | Status | Evidence |
|---|---|---|---|
| 1 | Clearing a foreign timer segfaults | fixed | probe 11 prints `cleared`; p13 clears an interval from another loop |
| 2 | Narrowed captured union segfaults | fixed (now a documented TypeError) | probe 01: `TypeError: Expected string, got number` (D-140, SPEC §8) |
| 3 | Captured TDZ segfaults | fixed | probe 12 matches Node (`ReferenceError`) |
| 4 | `toString` ABI segfault | fixed | probe 14 matches Node (`7,7`) |
| 5 | Thenable resolution | fixed | probe 05 matches Node |
| 6 | Reads deferred past a suspension | mostly fixed | probe 06 matches Node; two documented cases remain (a getter or `any` as a compound target, method lookup; Known issues) |
| 7 | Iterator protocol order and close | fixed | probe 13 matches Node |
| 8 | Erased generic factories | partly fixed | probe 03 prints `box 9`; erasure remains for generic arrows and values from `any` (D-078 Known issue), and finding 4's panic is in the same erased paths |
| 9 | Extra `parallel` restriction; `reduce` initial value | fixed | probe 15 prints `42`; probe 09's `reduce` gives 7998010, the sequential value |
| 10 | Race-safety contract unestablished | largely fixed | typed arrays use relaxed atomics; ARCHITECTURE §4.13 contract (D-143). Plain fields still rely on clang's x86 code (owner to review, REVIEW_LATER). No crash in my race runs. |
| 11 | Green infrastructure overstates assurance | partly fixed | skips are classified (`TestCorpusSkipsAreClassified`) but still never run; TSan scope unchanged (finding 10) |

The effect-classification default it flagged is now conservative (`pts/cgen/order.go:196`).

## Top 10 next actions (M8 candidates)

1. **Correctness of array built-ins.** Fix `flat`/`flatMap` (finding 1) and `sort` (finding 2). Then audit every built-in in `arrays.go` and `array.c` for union, tuple and undefined element types, using a generated differential test against Node.
2. **Loose locals:** no check where the target admits `undefined` (finding 3).
3. **No compiler panics:** fix `records.go:280`, add a driver `recover` with an "internal compiler error" message, and run every error, skip and fuzz program through `pts build` in CI (findings 4 and 10).
4. **Run the skipped corpus programs** and assert their documented outcome (no crash, the expected PTS1001), so regressions can't hide (finding 10).
5. **Faster builds:** `-O1` for `pts run` and `pts test`, a cache of library instances, parallel translation units (finding 7).
6. **HTTP hardening:** header, request and idle timeouts, a connection cap, and half-close handling (finding 6; D-163).
7. **Web globals:** `URL`/`URLSearchParams`, `structuredClone`, `TextEncoder`/`TextDecoder`, `crypto.getRandomValues` and `randomUUID`, `atob`/`btoa`, `process.stdin` (finding 9). Then https.
8. **Everyday PTS1001s:** conditional spread, tagged templates and `String.raw`, `{} as T` builders, classes in functions; and no `objectN` names in messages (finding 8). Make `const o: any = {}` a hash-map object (finding 5).
9. **Editor parity:** show the lowering's PTS1001 errors in `pts lsp` (D-168), so the editor and `pts build` agree.
10. **Then the planned M8 performance work:** a collector with per-thread allocation (D-131/D-172 scaling), the state machines as an IR pass (D-109), and integer inference. Decide the D-172 collection-mutation check with the owner.
