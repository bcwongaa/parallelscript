# Decisions log

Append-only. Every entry has a date, the decision, the reason and the alternatives rejected. Never edit or reopen a locked entry; if one looks wrong, write `docs/proposals/NNN-title.md` and notify the owner.

Id scheme:
- **D-Lnn**: locked by owner (brief §2). Reason: the owner's brief, cited per entry. Alternatives: not recorded, because the owner chose.
- **D-Onn**: decided by orchestrator for owner (`docs/agent/PREDECISIONS.md`, 2026-10-11). Ranks just below D-L.
- **D-nnn**: decided by an agent session, applying the brief's §3 tie-breakers where the spec is silent.

---

## Locked by owner (brief §2)

### D-L01 · Syntax and files (locked by owner)
- **Date:** 2026-10-11
- **Decision:** The syntax is TypeScript plus one contextual keyword, `parallel`, which is a keyword only in `parallel <expression>` (expression position) and `parallel for (...)` (statement position). Existing code that uses `parallel` as an identifier keeps working. The brief names the extension `.pts`; the owner later overrode it to `.ps` (see D-O01).
- **Reason:** Brief §2.1.

### D-L02 · The compatibility principle (locked by owner)
- **Date:** 2026-10-11
- **Decision:** A source program that uses only plain TypeScript features (no `parallel`, no integer types, no `pts:` imports) prints exactly what Node prints when run as TypeScript, except where the spec documents a difference.
- **Reason:** Brief §2.1.

### D-L03 · Front end is a fork of TypeScript 7 (locked by owner)
- **Date:** 2026-10-11
- **Decision:** Fork typescript-go pinned to the latest 7.x release tag (`typescript/v7.0.2`). The compiler `pts` is written in Go inside the fork's module (it needs `internal/`). Upstream modifications are minimal, marked `// PTS:` and listed in `docs/FORK.md`; new code goes in new directories (`pts/`, `cmd/pts/`). Upstream parser and checker suites stay green for the areas touched. `LICENSE` and `NOTICE` stay intact; ParallelScript's own code may use Apache-2.0. Prelude types and `pts:` modules are declared in ParallelScript's own lib declaration files.
- **Reason:** Brief §2.2.

### D-L04 · Compilation pipeline and IR (locked by owner)
- **Date:** 2026-10-11
- **Decision:** TS AST plus checker → PTS semantic checks → lowering to a typed ParallelScript IR → IR passes → C11 → clang -O2, linked with the runtime into one native binary. The IR is structured (SSA not required) and fully typed, with explicit boxing, closure environments, monomorphized generics and async/generator state machines. C is never emitted directly from the AST. Generated C is readable and carries `#line` directives pointing at the source.
- **Reason:** Brief §2.3.

### D-L05 · Runtime and CLI (locked by owner)
- **Date:** 2026-10-11
- **Decision:** The runtime is C in `pts/runtime/`, statically linked. bdwgc and libuv are vendored as pinned submodules and built from source; `pts build` output depends only on libc. The CLI is `pts build file -o out`, `pts run`, `pts emit-c`, `pts test` (later) and `pts lsp` (M7). `--cc zig` cross-compiling is a stretch goal.
- **Reason:** Brief §2.3.

### D-L06 · Fixed variable types and `number` (locked by owner)
- **Date:** 2026-10-11
- **Decision:** Variables have fixed types. `number` keeps exact JS semantics (IEEE-754 f64, `NaN`, `Infinity`, `-0`). Number-to-string conversion matches JS exactly via shortest round-trip formatting.
- **Reason:** Brief §2.4.

### D-L07 · Integer types and numeric rules (locked by owner)
- **Date:** 2026-10-11
- **Decision:** `int` (i64) and `i8 i16 i32 i64 u8 u16 u32 u64 f32 f64` are declared in a global prelude, starting as aliases of `number`; the PTS semantic pass enforces the rules (and later the language server shows them). Literals take their type from context. Implicit conversions are lossless only (integer → `number`; smaller → larger integer of compatible sign); anything lossy needs `as`. `int` mixed with `number` yields `number`. Numeric `as` converts with Rust rules (truncate toward zero, saturate, `NaN` → 0). Integer overflow wraps; integer `/` truncates; integer division or modulo by zero throws `RangeError`. `typeof` of any numeric type is `"number"`.
- **Reason:** Brief §2.4.

### D-L08 · Strings (locked by owner)
- **Date:** 2026-10-11
- **Decision:** Strings are immutable UTF-8 with a pure-ASCII flag. `.length`, `s[i]`, `charCodeAt` and `slice` count UTF-16 code units; ASCII strings take an O(1) fast path and non-ASCII strings may build an index lazily.
- **Reason:** Brief §2.4.

### D-L09 · `any`, `unknown` and casts (locked by owner)
- **Date:** 2026-10-11
- **Decision:** `any` is a checked dynamic value: operations follow JS semantics at runtime via type descriptors, and assigning an `any` to a typed location checks the value and throws `TypeError` on mismatch. Every runtime type carries a descriptor (name, fields → offsets, methods), reused by `console.log` and JSON. `unknown` is as in TS. Non-numeric `as`: upcasts free; downcasts and unrelated casts checked at runtime (`TypeError`); `x!` checked; `as unknown as T` cannot lie.
- **Reason:** Brief §2.4.

### D-L10 · Objects, classes and interfaces (locked by owner)
- **Date:** 2026-10-11
- **Decision:** Classes have fixed layouts and vtables, with single inheritance, fields, methods, accessors, statics, `private`/`#private`, abstract classes and `instanceof`. Interfaces stay structural; runtime dispatch goes through per-(concrete type, interface) tables built at compile time where possible and cached at runtime otherwise. Object literals are anonymous struct types; optional properties are stored as `T | undefined`. Properties cannot be added or deleted after construction; `delete` works only on index-signature types; `Record<string, T>` and index signatures are hash maps. `null` and `undefined` are distinct; `?.` and `??` are supported.
- **Reason:** Brief §2.4.

### D-L11 · Unions, generics and equality (locked by owner)
- **Date:** 2026-10-11
- **Decision:** Unions are tagged values; narrowing works for `typeof`, `instanceof`, `in`, discriminants, equality and user-defined type guards (the checker reasons, the backend emits tag tests). Generics are monomorphized. `===` compares objects by identity and strings and numbers by value with JS `NaN` rules; `==` has its JS meaning.
- **Reason:** Brief §2.4.

### D-L12 · Exceptions and unsupported features (locked by owner)
- **Date:** 2026-10-11
- **Decision:** `throw`/`try`/`catch`/`finally` have JS semantics and any value can be thrown. `Error` and subclasses have `message`, `name`, `cause` and `stack` (with source lines, possibly later). Compile errors with clear explanations for: `eval`, `new Function`, `with`, `var` (suggest `let`), `arguments`, prototype manipulation (`__proto__`, `Object.setPrototypeOf`, assigning `.prototype`), `Proxy`, `Reflect`, declaration merging and namespaces. Decorators, `Symbol` beyond `iterator`/`asyncIterator`, and `WeakRef` can come later.
- **Reason:** Brief §2.4.

### D-L13 · Memory and the race-safety invariant (locked by owner)
- **Date:** 2026-10-11
- **Decision:** GC with bdwgc in thread-aware mode with thread-local allocation; pointer-free data uses the atomic allocator; a precise/generational GC is later. **Non-negotiable:** racy programs may see stale values but never crash, corrupt the heap or access out of bounds. No torn multi-word values in shared slots; growable arrays keep capacity inside the buffer and bounds-check against the buffer actually accessed; strings are immutable.
- **Reason:** Brief §2.5.

### D-L14 · Event loops and `await` (locked by owner)
- **Date:** 2026-10-11
- **Decision:** One libuv event loop per core (configurable, e.g. `PTS_THREADS`), each on its own OS thread over one shared heap; `uv_async_send` wakes a loop. Plain `await` keeps exact JS semantics: a task belongs to one loop for life and a loop runs one task at a time; single-loop ordering of microtasks, timers and I/O matches Node. Async functions and generators are state machines with GC-managed heap frames, no per-task native stacks.
- **Reason:** Brief §2.6.

### D-L15 · `parallel <expr>` (locked by owner)
- **Date:** 2026-10-11
- **Decision:** `parallel e` runs `e` as a new task on another loop and immediately returns a promise (flattened if `e` is a promise), typed `Promise<Awaited<T>>`. Captures are shared by reference. The target loop is chosen by load (least-loaded, or round-robin with stealing of unstarted tasks); tasks never migrate. Promises can be awaited from any loop and settling wakes the awaiting loop.
- **Reason:** Brief §2.6 and §6 (M5).

### D-L16 · Data parallelism, `.par`, helpers and `parallel for` (locked by owner)
- **Date:** 2026-10-11
- **Decision:** A Chase–Lev work-stealing fork-join pool, one deque per worker, separate from loop threads; a thread that starts a parallel operation helps until it finishes; Rayon's design is copied. `.par` on arrays, typed arrays and ranges offers `map` (order-preserving), `filter`, `forEach`, `reduce(fn, initial)` (associative), `flatMap`, `some`, `every`, `find` (any match), `count`, `sum`, `min`, `max`, `sort`/`sortBy` (parallel merge sort) and `toArray`, all synchronous. `range(start, end)` and `join(a, b)` come from `pts:parallel` or the prelude. `parallel for (const x of it)` is sugar for a parallel `forEach`: `continue` allowed; `break`, `return`, `yield`, `await` and labelled jumps out are compile errors; the first thrown error cancels the rest and is rethrown after joining.
- **Reason:** Brief §2.6.

### D-L17 · The single guardrail (locked by owner)
- **Date:** 2026-10-11
- **Decision:** Inside a parallel body (the operand of `parallel`, a `parallel for` body, or a function passed to a `.par` method or `join`), assigning to a captured outer `let` is a compile error suggesting `.par.reduce`, `.par.sum` or an `Atomic`. Parallel-callback parameters are marked with a branded function type in the stdlib declarations. Mutating object fields is allowed. **No other parallelism checks without asking the owner.**
- **Reason:** Brief §2.6.

### D-L18 · `pts:sync` (locked by owner)
- **Date:** 2026-10-11
- **Decision:** `Mutex<T>` (blocking `withLock(fn)`, async `lock()`), `AtomicInt`/`Atomic<int>` (`load`, `store`, `add`, `compareExchange`), `Channel<T>` (bounded or unbounded, async `send`/`recv`, `close`, `for await`) and `Once`. A concurrent map only if benchmarks or examples want it.
- **Reason:** Brief §2.6.

### D-L19 · Modules and standard library (locked by owner)
- **Date:** 2026-10-11
- **Decision:** ES modules with relative imports; stdlib modules use the `pts:` prefix (`pts:fs`, `pts:path`, `pts:process`, `pts:http`, `pts:sync`, `pts:parallel`); no npm or `node_modules` in v1. Globals: `console` (matching `util.inspect`), `Math`, `JSON`, `Promise` (with `all`, `allSettled`, `race`, `any`, `withResolvers`), timers, `queueMicrotask`, `performance.now()`, `fetch`. Built-ins: the common parts of `String`, `Array`, `Map`, `Set`, `Number`; `Object.keys`/`values`/`entries`; basic `Date`; `RegExp` (PCRE2 or a port, may come later).
- **Reason:** Brief §2.7.

### D-L20 · Tie-breakers when the spec is silent (locked by owner)
- **Date:** 2026-10-11
- **Decision:** In order: (1) memory safety under races is never traded away; (2) for plain TS code, do what Node does; (3) don't add strictness the owner didn't ask for, preferring a runtime check that throws over a compile error that blocks a reasonable program; (4) for parallel semantics, do what Rayon does for data parallelism and what Go does for tasks and channels; (5) simple and working beats clever and half-done. Every such call is logged here.
- **Reason:** Brief §3.

---

## Decided by orchestrator for owner (`docs/agent/PREDECISIONS.md`)

### D-O01 · `.ps` extension (decided by orchestrator for owner; owner instruction)
- **Date:** 2026-10-11
- **Decision:** Source files use `.ps`, overriding `.pts` in the brief. `.gitattributes` maps `*.ps` to TypeScript for GitHub highlighting. The VS Code extension registers `.ps` with language id `parallelscript`. Declaration files stay `.d.ts`. `import "./x"` tries `x.ps` first, then the TS extensions. Every other PTS name stays: the CLI `pts`, the `pts:` prefix, `// PTS:` markers, the `pts/` directory.
- **Reason:** Owner instruction.
- **Alternatives rejected:** `.pts` (the brief's original choice).

### D-O02 · Repo and git flow (decided by orchestrator for owner)
- **Date:** 2026-10-11
- **Decision:** `origin` is `bcwongaa/parallelscript`, public as the owner created it. Commit directly to `main`, keep it green, tag each milestone. No PR flow.
- **Reason:** The brief's "keep main green" rule; one autonomous writer.
- **Alternatives rejected:** The LLM-dev-guides PR flow; a private repo.

### D-O03 · Telegram cadence (decided by orchestrator for owner)
- **Date:** 2026-10-11
- **Decision:** One message per finished milestone, plus proposals, plus rare questions, on thread `telegram:-1004441160190:228`. The session driver sends its own operational alerts.
- **Reason:** Keep the owner informed without noise.
- **Alternatives rejected:** Per-session or per-task messages.

### D-O04 · Differential-test contract (decided by orchestrator for owner)
- **Date:** 2026-10-11
- **Decision:** Compare stdout and the exit code exactly. stderr is not compared, but an uncaught error must go to stderr and exit with code 1, like Node. Run Node with type stripping (`--experimental-strip-types`, default in Node 24) on a temp copy renamed to `.ts`; fall back to `tsc` emit for enums and parameter properties.
- **Reason:** stderr text (stack traces) can't match Node byte for byte; stdout and exit code capture the semantics.
- **Alternatives rejected:** Comparing stderr; running only through `tsc`.

### D-O05 · Disambiguating `parallel` (decided by orchestrator for owner)
- **Date:** 2026-10-11
- **Decision:** The parser treats `parallel` as the operator only when the next token is on the same line and starts an expression other than `(`, `[`, `.`, `?.`, a template, `=`, an operator, `,`, `;`, `)` or `}`. `parallel for (` is the loop statement. `parallel(expr)` parses as a normal call, which the checker reinterprets as the operator only if no binding named `parallel` is in scope and it has exactly one argument.
- **Reason:** Keeps existing code that uses `parallel` as an identifier working while `await parallel(f(x))` still reads naturally.
- **Alternatives rejected:** A reserved keyword; always treating `parallel(` as the operator.

### D-O06 · `PTS_THREADS=1` (decided by orchestrator for owner)
- **Date:** 2026-10-11
- **Decision:** `parallel e` still creates a new task, on the same loop; `.par` runs sequentially on the caller.
- **Reason:** Programs stay correct with one thread.
- **Alternatives rejected:** Running `parallel e` inline (would change ordering).

### D-O07 · Uncaught errors and rejections (decided by orchestrator for owner)
- **Date:** 2026-10-11
- **Decision:** An uncaught exception prints `Uncaught <Name>: <message>` (and, once available, a `.ps` stack trace) to stderr and exits with code 1. An unhandled rejection behaves like Node's default `--unhandled-rejections=throw`. Until M3 exceptions land, runtime errors abort with the same format and exit code 1.
- **Reason:** Matches Node's exit code, which the differential tests compare.
- **Alternatives rejected:** Silent abort; exit codes other than 1.

### D-O08 · BigInt (decided by orchestrator for owner)
- **Date:** 2026-10-11
- **Decision:** Out of scope for v1. A `bigint` type or literal gives the compile error "BigInt is not supported yet", logged as a documented difference.
- **Reason:** Scope; the integer types cover most needs.
- **Alternatives rejected:** A BigInt runtime in v1.

### D-O09 · RegExp (decided by orchestrator for owner)
- **Date:** 2026-10-11
- **Decision:** RegExp is in scope as M6.1, backed by vendored PCRE2 (BSD) as a pinned submodule, translating JS syntax and flags (`g i m s u y`). Until then, string-only `split`/`replace`/`replaceAll` work and a regex literal is the compile error "RegExp arrives in M6".
- **Reason:** Needed by `split`/`replace`/`match` and the M6 log analyzer.
- **Alternatives rejected:** Porting a JS regex engine; leaving RegExp as a stretch goal.

### D-O10 · `fetch` (decided by orchestrator for owner)
- **Date:** 2026-10-11
- **Decision:** In M6, `fetch` supports `http://` (HTTP/1.1, chunked, JSON/text bodies). `https://` is a stretch goal (vendor mbedTLS if time allows); until then it rejects with a clear `TypeError`.
- **Reason:** Scope.
- **Alternatives rejected:** TLS in M6.

### D-O11 · Runtime distribution (decided by orchestrator for owner)
- **Date:** 2026-10-11
- **Decision:** `pts` finds its runtime sources relative to its own executable or via `PTS_HOME`, builds the vendored bdwgc, libuv and PCRE2 once into `~/.cache/pts/<version-hash>/`, and compiles user programs against those cached static libraries.
- **Reason:** A user rebuild never rebuilds the vendored libraries.
- **Alternatives rejected:** Rebuilding the libraries per program; system packages.

### D-O12 · Definition of "usable" (decided by orchestrator for owner)
- **Date:** 2026-10-11
- **Decision:** The run stops when M0–M7 are complete: tests green, examples working, README written, VS Code extension packaged. M8 and stretch goals only if the owner extends the run.
- **Reason:** A clear stop condition.
- **Alternatives rejected:** Open-ended work.

### D-O13 · Model fallback (decided by orchestrator for owner)
- **Date:** 2026-10-11
- **Decision:** Sessions may run on claude-opus-5-5 (preferred), gpt-6.1-sol or grok-4.7, chosen by the driver. Every session follows the same rules; fallback-model sessions prefer well-specified tasks and leave thorough notes in `PROGRESS.md`.
- **Reason:** Availability.
- **Alternatives rejected:** Stopping when the preferred model is unavailable.

### D-O14 · `Symbol` (decided by orchestrator for owner)
- **Date:** 2026-10-11
- **Decision:** Only `Symbol.iterator` and `Symbol.asyncIterator`. Any other `Symbol` use is a compile error naming the feature.
- **Reason:** Brief §2.4 lists the rest as "later".
- **Alternatives rejected:** A general symbol runtime.

### D-O15 · Benchmarks are not gates (decided by orchestrator for owner)
- **Date:** 2026-10-11
- **Decision:** Record honest numbers, including losses. Never game a benchmark (for example by special-casing it in the compiler).
- **Reason:** Honesty.
- **Alternatives rejected:** Treating the brief's goals as gates.

### D-O16 · `console.log` parity (decided by orchestrator for owner)
- **Date:** 2026-10-11
- **Decision:** Match `util.inspect` defaults: depth 2; `breakLength` 80 with the 72-char/6-entry heuristics for arrays and objects; numeric-array grouping above 6 items; strings in single quotes when nested; `-0`, `[Function: name]`, `[class X]`, `Map(n) {…}`, `Set(n) {…}`. Port the algorithm from Node's `lib/internal/util/inspect.js` and build the differential corpus around its edge cases.
- **Reason:** The differential tests compare stdout byte for byte.
- **Alternatives rejected:** An approximate formatter.

### D-O17 · Integer `%` (decided by orchestrator for owner)
- **Date:** 2026-10-11
- **Decision:** Integer `%` takes the sign of the dividend (as in JS and Rust). Integer `%` by zero throws `RangeError`.
- **Reason:** Consistency with JS and Rust, and with the brief's rule for division.
- **Alternatives rejected:** Floored modulo.

---

## Decided by agent sessions

### D-001 · How `.ps` plugs into the front end
- **Date:** 2026-10-11
- **Decision:** `.ps` is a TypeScript implementation extension (`ScriptKindTS`; declarations emit as `.d.ts`; implied module format as for `.ts`). In the grouped extension lists used by tsconfig `include`, `.ps` is its own priority group, appended last, so `a.ps` and `a.ts` side by side are both included and upstream's ordering is unchanged. Module resolution: an extensionless import (`./x`) tries `x.ps` first, then upstream's order (D-O01); an explicit `./x.ps` resolves to that file (like `./x.ts`, it needs `allowImportingTsExtensions`, which `pts` sets). `./x.js` does not map to `x.ps`. A missed `.ps` probe is not written to the resolution trace.
- **Reason:** Smallest upstream diff that meets D-O01. Untraced misses keep every upstream `traceResolution` baseline byte-identical, so upstream tests stay green without baseline churn; appending `.ps` last keeps upstream's extension priorities and messages stable apart from the extra entry.
- **Alternatives rejected:** Putting `.ps` in the `.ts` group (would make `a.ps` shadow or be shadowed by `a.ts` in `include`). Tracing `.ps` misses (changes upstream trace baselines). Trying `.ps` only from `.ps` importers (a `.ts` file in a PTS program couldn't import a `.ps` module). Mapping `./x.js` to `x.ps` (no PTS code writes `.js` specifiers; can be added later).

### D-002 · Dynamic values are one word, encoded as in JavaScriptCore
- **Date:** 2026-10-11
- **Decision:** `pts_value`, the runtime's dynamic value (used for `console.log` arguments now, and later for unions and `any` where a static C type isn't enough), is a `uint64_t`. Heap objects are stored as raw pointers (top 16 bits zero, at least `0x10000`). Doubles are stored as their bits plus 2^49, with `NaN` canonicalised. `undefined`, `null`, `false` and `true` are small constants (`0x0a`, `0x02`, `0x06`, `0x07`). Every heap object starts with a pointer to its `pts_type` descriptor. 64-bit integers that need to live in a `pts_value` will be boxed.
- **Reason:** One word means a racing reader can never see a torn value (D-L13, tie-breaker 1). Keeping pointers raw means bdwgc's conservative scan still sees them; classic NaN-boxing puts tag bits in the pointer's top bits, which hides the reference from the GC.
- **Alternatives rejected:** A two-word `{tag, payload}` struct (can tear under races). NaN-boxing with tagged pointers (invisible to bdwgc). Boxing every number (an allocation per dynamic number).

### D-003 · The toolchain builds the C libraries itself, without CMake
- **Date:** 2026-10-11
- **Decision:** `pts/toolchain` (Go) compiles bdwgc (as the single translation unit `extra/gc.c`, with the defines its CMake build uses for threads, parallel mark and thread-local allocation), libuv (its Linux source list and defines, copied from its `CMakeLists.txt`) and the runtime (`pts/runtime/src/*.c`) with clang, archives them with `ar`, and caches each archive in `~/.cache/pts/<name>-<key>/` (`$PTS_CACHE_DIR` overrides). The key hashes the compiler version, flags, source list and the library's version header (vendored) or all its sources (runtime). Builds take a file lock and publish with an atomic rename, so parallel tests and processes build each archive once. Vendored libraries are always `-O2`; the runtime also has a `Sanitize` profile (ASan + UBSan) for its unit tests.
- **Reason:** One implementation serves the CLI and the tests. Users need only clang, not cmake or make. The cache keys keep rebuilds of user programs from rebuilding vendored libraries (D-O11). MASTER_PLAN M0.4 says "CMake (or Make)"; this meets its intent (a cached build of the vendored libraries) with fewer moving parts.
- **Alternatives rejected:** Driving the libraries' own CMake builds (needs cmake and ninja on users' machines, and slower). A Makefile (a second build implementation next to the Go one).

### D-004 · Source extension reverts to `.pts` (owner instruction; supersedes the extension in D-O01 and D-001)
- **Date:** 2026-10-11
- **Decision:** ParallelScript source files use **`.pts`**, the brief's original extension. Everything D-O01 and D-001 say about `.ps` now applies to `.pts`: `ExtensionPts` in `tspath`, its own group in the grouped extension lists, extensionless imports try `.pts` first (untraced), and `.gitattributes` maps `*.pts` to TypeScript. The `.ps` extension is no longer recognised.
- **Reason:** Owner instruction after M0.4, before any `.ps` programs existed outside the front-end tests.
- **Alternatives rejected:** Accepting both `.ps` and `.pts` (two extensions for one language, with `.ps` clashing with PostScript).

### D-005 · Fixed front-end options and an embedded prelude
- **Date:** 2026-10-11
- **Decision:** `pts` type-checks every program with fixed options: `strict`, target and module `ESNext`, `moduleResolution: bundler`, `allowImportingTsExtensions`, `moduleDetection: force` (every file is a module, so top-level names never clash with globals), `lib: ["esnext"]` (no DOM) and `types: []` (no `@types` from a `node_modules` nearby). A `tsconfig.json` is not read. ParallelScript's own declarations (`pts/lib/*.d.ts`, the prelude now) are embedded in the binary and served by a wrapping file system at `ptslib:///…`; the prelude is a root file of every program. The prelude declares only what the compiler can lower, starting with `console.log`.
- **Reason:** The language has one dialect, so its options aren't user settings (tie-breaker 5). Without the DOM lib, `document` and friends are type errors instead of link errors. Embedding keeps the declarations in step with the compiler that implements them, and a URL root can't clash with real paths. Upstream's lib loading needs no edit.
- **Alternatives rejected:** Reading `tsconfig.json` (options would change semantics; can come later for `include` lists). Registering the prelude in upstream's `LibMap` and generated lib tables (upstream edits plus regeneration). Reading the declarations from `PTS_HOME` on disk (a second thing to keep in sync).

### D-006 · One diagnostic format, PTS codes and CLI exit codes
- **Date:** 2026-10-11
- **Decision:** tsc's errors and ParallelScript's own are printed alike, one per line: `file:line:col - error CODE: message`, with the file relative to the working directory when below it. tsc errors keep `TSnnnn`. ParallelScript's codes: `PTS1001` "… is not supported yet" (valid TypeScript the compiler can't lower yet; any construct the lowering doesn't handle gets it automatically) and `PTS1002` for features left out by design (D-L12, e.g. `var`). Semantic errors are only reported when there are no syntax errors, as in tsc. `pts` exits 0 on success, 1 on compile or toolchain errors, 2 on a malformed command line; `pts run` exits with the program's own code, or 128 + the signal number if it was killed.
- **Reason:** Error tests (M0.6) match message substrings, so one stable plain-text format is enough; a code-frame "pretty" mode can come with M7.3. A blanket `PTS1001` makes the supported subset explicit and fail-safe.
- **Alternatives rejected:** Upstream's pretty formatter (hard-codes the `TS` prefix). Numeric codes in tsc's range (would read as tsc errors).

### D-007 · Names and `#line` in generated C
- **Date:** 2026-10-11
- **Decision:** IR functions become `static` C functions named `pts_fn_<name>`; top-level code is `pts_fn_main`, called from a generated `main` between `pts_runtime_init` and `pts_runtime_exit`. Locals keep their source name unless it is a C keyword, a lowercase macro the headers may define (`errno`, `stdin`, …), all-caps (likely a macro), starts with `_`, `pts_`, `PTS_` or `GC_`, or has characters C doesn't allow (`$` → `_S`, others `_uXXXX`); such names get a trailing `_`. Names are made unique per function with `_2`, `_3`, …. `#line N "<absolute .pts path>"` precedes each function and each statement whose line differs from the line the C compiler already presumes, so there are no redundant directives. String literals are `PTS_STATIC_STRING` constants `pts_str_N`, one per distinct value, written as readable UTF-8 with octal escapes for anything unprintable and `\?` against trigraphs.
- **Reason:** Readable C (brief §2.3) and exact source mapping: gdb stops at `hello.pts:1` and shows the `.pts` line. Absolute paths make the mapping independent of where clang ran.
- **Alternatives rejected:** Prefixing every local (unreadable). Relative paths in `#line` (resolved against the temporary build directory). A `#line` before every statement (noise).

### D-008 · Plan change: modules get their own task (M1.4b)
- **Date:** 2026-10-11
- **Decision:** `MASTER_PLAN.md` had no task for relative imports between `.pts` files, which D-L19 requires. M1.4 becomes M1.4a (unchanged scope) and a new M1.4b covers ES module imports and exports between `.pts` files, module initialisation order, and top-level bindings that functions capture becoming C globals.
- **Reason:** Real programs (and the M6 examples) need modules; M1.4 brings functions, after which module-level state is needed anyway.
- **Alternatives rejected:** Folding modules into M1.4 (already the largest M1 task).

### D-009 · Test corpus format and how Node runs differential tests
- **Date:** 2026-10-11
- **Decision:** Corpus tests are `.pts` files under `pts/tests/run/` and `pts/tests/errors/`, configured by whole-line directive comments (`// expect:` block or a `.out` file, `// exit:`, `// stderr:`, `// error:`, `// no-diff:`, `// skip:`; see `pts/tests/README.md`). One Go package runs them: `TestRun` (stated expectations), `TestRunMatchesNode` (stdout and exit code equal Node's, for plain TypeScript) and `TestErrors`. A run test may state no output, in which case Node alone is the oracle. Plain TypeScript is detected automatically (no `pts:` import, `parallel`, `.par` or integer type annotation), with `// no-diff:` to opt out. Node runs a copy named `main.mts` with `--no-warnings --experimental-transform-types`. Without Node on PATH the differential tests skip, unless `PTS_REQUIRE_NODE` is set (CI and `make test-diff` set it).
- **Reason:** `.mts` makes Node treat the file as an ES module (strict mode, top-level `this` undefined), matching ParallelScript, where every file is a module (D-005); a `.ts` file without imports would run as CommonJS in sloppy mode. Node's transform mode covers enums and parameter properties, which is what D-O04's tsc fallback was for, without a second toolchain. Node-as-oracle lets the M3.7 corpus be written without hand-computed outputs.
- **Alternatives rejected:** `.ts` with `--experimental-strip-types` plus a tsc fallback (CommonJS semantics for files without imports; two code paths). A separate differential runner script outside `go test` (two harnesses).

### D-010 · CI: one PTS workflow gates pushes; upstream CI on demand
- **Date:** 2026-10-11
- **Decision:** `.github/workflows/pts.yml` runs on every push and pull request on `ubuntu-24.04`: it checks out only the `pts/third_party` submodules, installs Node 24, Go (upstream's `setup-go` action) and clang, caches the Go and `~/.cache/pts` builds, then runs `make build`, `PTS_REQUIRE_NODE=1 make test` and `make test-upstream`; a second job runs `make test-upstream-cli` (upstream's CLI and LSP suites with the known pre-existing failure skipped by name). Upstream's `ci.yml` is reduced to `workflow_dispatch`, and dependabot's version-update PRs are turned off (ledger in `docs/FORK.md`).
- **Reason:** The brief asks for CI on every push that covers PTS and the upstream areas the fork touches. Upstream's matrix needs custom runners, the TypeScript submodule and about 75 minutes, and would fail on the pre-existing upstream failure; running it on demand (before merging a new upstream tag) keeps its value without blocking every push.
- **Alternatives rejected:** Keeping upstream CI on push (slow, red for reasons outside the fork). Deleting upstream workflows (bigger diff, loses the on-demand option).

### D-011 · Number semantics in C: Ryu, `fmod`, `pow` and no FP contraction
- **Date:** 2026-10-11
- **Decision:** Number::toString takes its shortest round-trip digits from Ryu (`d2s_buffered_n`), vendored as the submodule `pts/third_party/ryu` pinned at tag `v2.0` (Apache-2.0) and built by the toolchain as `libryu.a`; the runtime keeps ECMAScript's notation rules. `+ - * /` and comparisons are C operators on `double`; `%` is `fmod` (`pts_number_rem`); `**` is `pow` except that `x ** NaN` and `(±1) ** ±Infinity` are NaN (`pts_number_pow`). These helpers use clang builtins, so `pts.h` doesn't pull `<math.h>` macros into generated C. The runtime and programs compile with `-ffp-contract=off`. `==`/`!=` lower to `===`/`!==` because the lowering only accepts operands of one primitive type. `NaN` and `Infinity` lower to constants when they resolve to the ES library's globals.
- **Reason:** Ryu is proven, small and fast, and it implements exactly ECMAScript's choice (shortest digits, closest value, ties to even); the existing 25,000-value differential test against `jsnum` passes unchanged. `fmod` is exactly JS's `%`. glibc's `pow` matched Node's `**` bit for bit on 200,000 random inputs, so only the spec's NaN cases need code. Without `-ffp-contract=off`, clang may fuse `a*b+c` into an FMA on targets that have one (e.g. `-march=native`), which rounds once instead of twice and breaks JS semantics.
- **Alternatives rejected:** Writing a Ryu-style algorithm by hand (more code to trust, same result). Porting V8's fdlibm `pow` (no observed difference). Including `<math.h>` in `pts.h` (its `isnan`/`signbit`/… macros would collide with user names).

### D-012 · How the integer types work
- **Date:** 2026-10-11
- **Decision:** The prelude declares `int i8 i16 i32 i64 u8 u16 u32 u64 f32 f64` as aliases of `number`. A new semantic pass, `pts/check`, recovers numeric types from annotations (prelude aliases, and user type aliases of them) and records them for the lowering; an unannotated variable takes its initializer's numeric type, and a bare literal is a `number`. Literals are untyped constants, as in Go: they take the type their context expects (a declaration's annotation, or the other operand of a binary operator), integer literals are range-checked exactly from their source text (so 64-bit values keep every digit), and a non-integer constant next to an integer operand stays a `number`. Operands of a binary operator convert to their common type: the wider of two compatible types, or `number` when an integer meets a float. Unsigned to a strictly wider signed type counts as lossless; `int → f32` is not implicit (the spec lists only integer → `number`). Mixing two integer types with no lossless common type (e.g. `i64` and `u64`) is a compile error. Arithmetic wraps (computed in `uint64_t`), `MIN / -1` wraps to `MIN` and `MIN % -1` is 0, `/` and `%` by zero throw `RangeError: Division by zero`, an integer `**` with a negative exponent throws `RangeError: Exponent must be non-negative` (BigInt's message), and unary minus on an unsigned value wraps. `f32` arithmetic is C `float`; its `%` and `**` go through `double`. In a dynamic value a 64-bit integer is a number while `|v| ≤ 2^53` and a boxed integer beyond, so `console.log` prints every digit. Error codes: `PTS2001` (lossy implicit conversion), `PTS2002` (literal doesn't fit or isn't an integer), `PTS2003` (no common type).
- **Reason:** tsc erases the aliases to `number`, so only a pass of our own can see them; annotations are where the brief says the types come from. Go's untyped constants give the brief's "literals take their type from context" a precise meaning. A compile error for `i64 + u64` follows "anything lossy needs `as`" rather than silently switching to floating point. Wrapping through `uint64_t` avoids C's undefined signed overflow (UBSan runs on the runtime tests).
- **Alternatives rejected:** Branded types in the prelude (tsc would reject `const x: int = 5`). Doing the numeric typing inside the lowering (the language server needs the diagnostics without lowering, M7.1). Converting mixed integer types to `number` (surprising loss of integer semantics). Compile-time evaluation of constant expressions (`const b: u8 = 200 + 56` wraps at run time to 0 instead of failing; can come later).

### D-013 · M1.3 split into M1.3a and M1.3b; `split` moves to M2.4
- **Date:** 2026-10-11
- **Decision:** M1.3 becomes M1.3a (booleans' `!`, the string representation work, concatenation, template literals, `length`, string comparison, and the lone-surrogate output fix) and M1.3b (indexing and the listed string methods). `String.prototype.split` moves to M2.4 because it returns an array, which doesn't exist before M2.4. No scope is dropped.
- **Reason:** The session protocol's rule for tasks too big for one session; and `split` can't be honest without arrays.
- **Alternatives rejected:** A temporary array-free `split` (throwaway code).

### D-014 · Strings hold lone surrogates as WTF-8
- **Date:** 2026-10-11
- **Decision:** A string's bytes are UTF-8, except that a lone surrogate (which JS strings may contain) is its 3-byte generalised encoding, as the TS scanner already produces for literals. Concatenation joins a trailing lone high surrogate and a leading lone low one into the 4-byte encoding of their code point, so `"\uD83D" + "\uDE00" === "😀"` and the representation stays canonical (byte equality is string equality). Output (`console.log`) writes a lone surrogate as U+FFFD, as Node does. `<`/`>` compare by UTF-16 code units, decoding non-ASCII strings on the fly (`pts_string_compare`). Members of the ES library that the runtime implements are listed with the prelude's in `lower.builtins`, keyed `"Interface.member"` (first `String.length`).
- **Reason:** D-L08 requires UTF-8 with UTF-16 semantics, and JS programs can build lone surrogates; WTF-8 is the standard way to keep them losslessly in UTF-8. Joining on concatenation keeps one representation per string value.
- **Alternatives rejected:** Replacing lone surrogates with U+FFFD on creation (changes `.length` and equality). UTF-16 storage (contradicts D-L08).

### D-015 · `s[i]` out of range: JS's undefined where it fits, RangeError where a string is required
- **Date:** 2026-10-11
- **Decision:** tsc types `s[i]` as `string` (D-005 doesn't enable `noUncheckedIndexedAccess`), but in JS an index out of range (negative, past the end, or not an integer) gives `undefined`, which a `string` can't hold (D-L06). The lowering keeps JS's result wherever the context can take it: as a `console.log` argument or other place that takes any value (`String.element` → `pts_value`), as an operand of string concatenation or a template literal (ToString gives `"undefined"`), and compared with a string by `===`, `!==`, `==` or `!=` (`String.elementEquals`: false, or true for `!==`). Everywhere else (a `const`, a method receiver, a relational operator, the other operand of `s[i] === t[j]`) the read is checked and an index out of range throws `RangeError: String index 5 is out of range (length 3)`. `charCodeAt` is exact JS (`NaN` out of range). Arrays (M2.4) should follow the same rule.
- **Reason:** Tie-breaker 2 (do what Node does) covers the common idioms exactly: printing, building strings, and the peek-ahead test `s[i + 1] === "="` at the end of the input. Where the value would flow into a typed `string`, a runtime check that throws (tie-breaker 3) beats a compile error, and is the same boundary check D-L09 makes for `any`. RangeError names the actual cause.
- **Alternatives rejected:** Always throwing (breaks `console.log(s[9])` and peek-ahead comparisons, which are common in plain TypeScript). Turning on `noUncheckedIndexedAccess` (rejects ordinary TypeScript and makes every array read `T | undefined`). An empty string (silently wrong). A TypeError (the cause is an index, and the message says so).

### D-016 · How the String methods are implemented
- **Date:** 2026-10-11
- **Decision:** The methods of M1.3b (`charCodeAt`, `slice`, `substring`, `indexOf`, `includes`, `startsWith`, `endsWith`, `toUpperCase`, `toLowerCase`, `trim`, `trimStart`, `trimEnd` and the aliases `trimLeft`/`trimRight`, `padStart`, `padEnd`, `repeat`) are runtime functions taking doubles for indices and applying ToIntegerOrInfinity, so a left-out (or `undefined`) index argument is passed as `NaN` where the spec turns undefined into 0 and `Infinity` where it means the length; `padStart`/`padEnd` get `" "`. Integer-typed arguments convert to `number`. Index work on an ASCII string is O(1); a non-ASCII string is scanned from the start (no lazy index yet). Searches use `memmem` on the bytes, except for a search string that starts with a lone low surrogate or ends with a lone high one, which can match half of a pair and is compared unit by unit. Results that cut a surrogate pair hold lone surrogates; padding and repeating join halves at the seams, as concatenation does. Case mapping uses Unicode's full mappings (`ß` → `SS`) plus Final_Sigma, from tables generated out of Node's ICU (`pts/runtime/gen/case_tables.mjs` → `pts/runtime/src/case_tables.h`, Unicode 17.0); `TestGeneratedStringCorpusMatchesNode` compares every code point in planes 0 and 1 with Node. Errors match V8's messages: `RangeError: Invalid count value: -1`, `RangeError: Invalid string length`.
- **Reason:** One runtime function per method keeps the generated C readable and the semantics in one place, tested under the sanitizers. Taking the tables from Node makes Node the oracle (D-L02); Go's `unicode` package is Unicode 15 and has no full (SpecialCasing) mappings. A lazy UTF-16 index needs a traced field in `pts_string`, which is allocated pointer-free today; it can come with the object model (M2.1) or performance work (M8).
- **Alternatives rejected:** Generating the tables from Go's `unicode` package (older Unicode, simple mappings only). Decoding strings to UTF-16 for every operation (an allocation per call). A lazy index now (needs a GC-traced field).

### D-017 · M1.4a split into M1.4a1 (statements and operators) and M1.4a2 (functions)
- **Date:** 2026-10-11
- **Decision:** M1.4a becomes M1.4a1 (statements, every operator, and the numeric rules on them) and M1.4a2 (functions, recursion, calls, and the unsupported-feature errors). No scope is dropped. The sub-tasks are numbered M1.4a1/M1.4a2 rather than M1.4b/M1.4c so that M1.4b (modules) keeps the name D-008 and the spec use.
- **Reason:** The session protocol's rule for tasks too big for one session.
- **Alternatives rejected:** Renumbering M1.4b (breaks references in D-008 and SPEC §9.2).

### D-018 · Bitwise operators and shifts on the integer types
- **Date:** 2026-10-11
- **Decision:** On `number`, the bitwise operators have JS's int32 semantics exactly (ToInt32 of both operands, ToUint32 for `>>>`, the shift count modulo 32). On an integer type they work on the type's own bits: `&`, `|`, `^` and `~` as in C; `<<` wraps; `>>` is arithmetic for signed types and logical for unsigned ones; `>>>` shifts the bits as unsigned and keeps the type (`-16 as i32 >>> 0` is still -16). The shift count is taken modulo the type's width, as in Java. The operands convert to their common type first, as for arithmetic (D-012), so `u8 << int` is an `int`, and an integer meeting a `number` is the int32 operation on numbers.
- **Reason:** Plain TypeScript must match Node (D-L02), which fixes the `number` case. The integer types are ParallelScript's own; Java's rules are the closest familiar ones for fixed-width types with `>>>`, never undefined behaviour in C, and agree with JS for `i32`'s `<<`, `>>`, `&`, `|`, `^` and `~`. Applying D-012's common-type rule keeps one rule for every binary operator.
- **Alternatives rejected:** Rust's rules (overshifting panics in debug builds; no `>>>`). Making `>>>` on `i32` return a `u32` like JS's uint32 (the result type would depend on the operator, unlike every other operator). Typing the shift count separately from the value (a second rule for one operator family).

### D-019 · How statements and evaluation order compile to C
- **Date:** 2026-10-11
- **Decision:** C leaves the evaluation order of operands and function arguments unspecified (and `x + (x = 5)` undefined), so `cgen` keeps JS's left-to-right order: when an operand writes (an assignment, output) next to another non-constant operand, or two operands may throw, the operands are evaluated into temporaries `pts_tmpN` inside a GNU statement expression `({ … })`, which clang accepts under `-std=c11`. Otherwise the C stays a plain expression. `x++` whose value is used, and `&&`/`||` on non-booleans, also use a statement expression. A JS `switch` (any type, cases tried in order, fallthrough, `default` anywhere) becomes an if-chain of `goto`s to case labels; its value is stored once in a local `tag`. Loops are C loops; `break`/`continue` to the innermost C loop stay C's, others are `goto <label>_break`/`<label>_continue`, and labels are written only where a `goto` jumps. `let x: T;` starts at T's zero value (tsc ensures it is assigned before use). `&&`, `||` and `?:` need operands of one type until unions arrive (M3.1), and `??` waits for M3.1.
- **Reason:** Exact JS semantics (tie-breaker 2) with readable C: the temporaries appear only in the rare expressions that need them. Statement expressions keep each expression's temporaries local without a pass that hoists them into statements. C's `switch` only takes integer constants and has no ordered, lazily evaluated cases.
- **Alternatives rejected:** Always evaluating into temporaries (unreadable C). An IR pass that splits expressions into statements (much more machinery for the same result now; may come with the async state machines in M4). C `switch` for integer cases (an optimisation for later).

### D-020 · Functions in the IR and C; the features left out by design are semantic-pass errors
- **Date:** 2026-10-11
- **Decision:** Every function declaration becomes an IR function before any body is lowered (so calls may precede declarations and recurse), with typed parameters (`ir.Func.Params`) and a result type: the numeric type the semantic pass gives it, `void`, or tsc's type. The semantic pass types parameters from their annotations, return values against an annotated result type, and arguments against parameter types; an unannotated function that returns a number takes the common type of what it returns (untyped constants follow the others, as for operands), so `function half(n: int) { return n / 2; }` returns an `int`. In C, functions are `pts_fn_<name>` (a nested one `pts_fn_<outer>_<inner>`) and the top-level code moves from `pts_fn_main` (D-007) to `pts_main`, so a user function named `main` keeps its name. Calls count as writing and throwing for the evaluation order (D-019). A non-void function whose last statement isn't a `return` ends with `pts_unreachable()`. Not yet: using variables from outside the function (top-level ones with M1.4b, closures with M2.5), function values, optional, default and rest parameters, generics, generators and async functions (PTS1001). The features left out by design (SPEC §3.10) are reported by `pts/check` (so the language server will show them) as PTS1002 with the reason: `var`, `arguments`, `eval`, `Function`, `Proxy`, `Reflect`, `__proto__`, `Object.setPrototypeOf`, assignments to `.prototype`, namespaces, BigInt, `Symbol` other than `Symbol.iterator`/`Symbol.asyncIterator`, and declaration merging; a regex literal is PTS1001 "RegExp arrives in M6". `with` needs nothing of ours: every file is a strict-mode module, so tsc rejects it. Declaration merging excludes function overloads and interfaces that merge only with interfaces.
- **Reason:** Hoisting and recursion need every signature first. Inferring an unannotated result from its returns matches how unannotated variables take their initializer's type (D-012). `pts_main` can't clash with a user name, which matters because `function main()` is common. The design exclusions are language rules, which belong with the other rules in the semantic pass (D-012). Merged interfaces and overloads change nothing at run time, so rejecting them would add strictness the owner didn't ask for (tie-breaker 3).
- **Alternatives rejected:** A `number` result for every unannotated function (a needless conversion and a surprise for `int` code). Keeping `pts_fn_main` and renaming a user `main` (confusing in a debugger). Reporting the exclusions in the lowering (the editor wouldn't see them). Rejecting interface merging (no runtime reason).

### D-021 · M1.4b split; module-scope bindings that functions use are C globals with a ready flag
- **Date:** 2026-10-11
- **Decision:** M1.4b becomes M1.4b1 (module-scope bindings in functions) and M1.4b2 (imports and exports between modules); no scope is dropped. A `let`/`const` declared directly at module scope that some function uses becomes a C global `pts_global_<name>` (the IR marks the `Local` as `Global`), with a flag `pts_global_<name>_ready` set when its declaration runs. A function's read or write checks the flag first (`pts_check_ready`) and throws `ReferenceError: Cannot access 'x' before initialization` before then, as JS's temporal dead zone does; the top-level code needs no check, because tsc rejects its uses before the declaration. Bindings no function uses stay C locals of `pts_main`. Function bodies are lowered after the top-level code, when every module-scope binding is known (function declarations are hoisted). Variables of an enclosing function or block are still not usable in a function (closures, M2.5).
- **Reason:** The session protocol's split rule. Globals keep C readable and let clang keep the other locals in registers; the flag gives JS's behaviour for the one case tsc can't see (a call before the declaration) at the cost of a well-predicted branch.
- **Alternatives rejected:** Making every module-scope binding a global (slower top-level loops, noisier C). No TDZ check (a function would read a zero value where Node throws).

### D-022 · How modules compile: evaluation order, bindings shared across modules, C names
- **Date:** 2026-10-11
- **Decision:** Every source file reachable from the entry by imports is a module of the program (declaration files have no code and aren't). `frontend.Load` lists them in ES module evaluation order (`Program.Modules`): depth first from the entry, a module after the modules it requests, in source order, each once; a module already being evaluated (an import cycle) is skipped, so it runs after the module that imported it. Every `import` and `export … from` requests its module, also one that names only types (Node, like `verbatimModuleSyntax`, keeps those imports), except `import type` and `export type … from`, which Node erases. Each module's top-level code is its own IR function, and C `main` calls them in that order (the entry's is still `pts_main`, others `pts_module_<module>`). Imports produce no code: the lowering follows an imported name through imports and re-exports (`GetImmediateAliasedSymbol`) to the exporting module's own binding, so imports are live bindings, as in JS, and a namespace import's `m.x` is the same binding. Module-scope bindings that another function or module uses are globals (D-021), and all of them, with every function, are declared before any code is lowered. `export default <expression>` is a binding of its own (`default`), set when the statement runs, as JS requires (a later assignment to the exported variable doesn't change it); when the expression names a function (which can't be reassigned) or only a type, importing the default names that directly. The ready check (D-021) is left out where the declaration has surely run: in its own module's top-level code and in the top-level code of modules that run later; functions, and modules that run earlier in a cycle, check. C names outside the entry module carry the module's file name: `pts_fn_<module>_<name>`, `pts_global_<module>_<name>`; clashes (two `util.pts` in different directories) get `_2`, `_3`, …. Names after a `pts_` prefix no longer get the C-keyword underscore (`pts_fn_main`, as D-020 intended). Not yet (PTS1001): a namespace object as a value (`console.log(m)`), dynamic `import()`, `import defer`. The semantic pass checks every module and types a module-scope binding on first use if it hasn't reached its declaration yet (a function above a typed binding it uses, or an import cycle), which also fixes a wrong PTS2001 for a function declared above an integer-typed binding it uses.
- **Reason:** Static imports fix the evaluation order at compile time, so calling each module's code from `main` in a fixed order is exactly Node's behaviour, cycles included, with no runtime module records. Resolving imports to the exporting binding gives live bindings for free and keeps generated C as plain global accesses. A snapshot binding for `export default <expression>` is what the ES spec does; skipping it for functions keeps the common `export default function` and `export default f` as direct calls. Leaving out ready checks that can never fail keeps importer code readable and fast.
- **Alternatives rejected:** One combined top-level function for all modules (loses the per-module order in cycles and the TDZ boundary). Runtime module objects with export tables (needless indirection; namespace objects can come with objects in M2). Eliding imports that name only types, as tsc does without `verbatimModuleSyntax` (Node, the oracle, doesn't). Treating `export default x` as an alias of `x` (differs from JS when `x` is reassigned later). Ready checks on every cross-module use (noise in the common acyclic case).

### D-023 · Benchmark layout; nbody moves to M2.7
- **Date:** 2026-10-11
- **Decision:** A benchmark is a directory `pts/bench/<name>/` holding `main.pts` and its Go twin `main.go` (a `main` package of the repo's Go module). Node and Bun run `main.ts` if the directory has one, otherwise `main.pts` itself (copied to a `.mts` file) when it is plain TypeScript, so the JS runtimes run exactly the program ParallelScript compiles; a `main.pts` that uses the integer types and has no `main.ts` runs under ParallelScript and Go only (`fib_int`). `pts/bench/run.sh` (`make bench`) builds everything, checks that every implementation prints the same output, runs hyperfine (`--shell=none`, 2 warm-ups, 10 runs) and prints the `docs/BENCHMARKS.md` table, where "PTS vs X" is X's mean time divided by ParallelScript's (above 1 means ParallelScript is faster). nbody moves from M1.5 to M2.7: its standard form keeps the bodies in an array of objects and needs `Math.sqrt`, none of which exists before M2.
- **Reason:** Sharing the source keeps the comparison honest (D-O15) and avoids twins drifting apart; the `.ts` escape hatch remains for programs that need ParallelScript-only features. A scalar nbody (35 module-scope variables and hand-unrolled pairs) would be throwaway code measuring something other than the usual benchmark (tie-breaker 5; the same reasoning as D-013 for `split`).
- **Alternatives rejected:** A hand-written `.ts` twin for every benchmark (drifts from the `.pts`). A scalar nbody in M1 (throwaway, unrepresentative). Pulling arrays and objects into M1 (out of order, large).

### D-024 · The object model: descriptors that are also GC layouts
- **Date:** 2026-10-11
- **Decision:** `pts_type`, the descriptor every heap object points to from its first word, holds the kind, a bdwgc mark descriptor (`gc_descr`, the descriptor's second word), the name, the superclass (`parent`), the instance size, the fields (name, byte offset, `pts_field_kind`: number, boolean, string, dynamic value, object, each integer type, f32; inherited fields first) and the methods (name, function pointer). `pts_object_new(type)` allocates a zeroed instance: with bdwgc's gcj-style allocator (`GC_gcj_malloc`, which stores the type pointer and scans only the words `gc_descr` names) when it holds pointers, and with the atomic allocator, never scanned, when it doesn't. `gc_descr` is a bitmap descriptor (`PTS_GC_POINTER_WORDS`, bit i for word i) for instances of up to 62 words and `PTS_GC_ALL_WORDS(size)` (scan every word) beyond; string, object and dynamic-value fields count as pointers. Both allocators use thread-local free lists. bdwgc is now built with `GC_GCJ_SUPPORT` and `GC_ENABLE_SUSPEND_THREAD`, as its CMake defaults have it, and `pts_runtime_init` calls `GC_init_gcj_malloc`. Descriptors are static constant data; the compiler will compute `gc_descr` from the layout when it emits a class (M2.2). The runtime's C tests run under ASan with `detect_stack_use_after_return=0`, because bdwgc can't see the fake stack frames that option puts locals in.
- **Reason:** The descriptor every object already carries becomes its precise layout at no cost per object, so number fields (doubles, and the encoded doubles of D-002) are never taken for pointers and never keep garbage alive, while allocation keeps the thread-local fast path that parallel code needs (D-L13). One descriptor serves the collector, `console.log` and JSON (D-L09).
- **Alternatives rejected:** Conservative `GC_MALLOC` for every object (number fields pin garbage, and every word is scanned). `GC_malloc_explicitly_typed` (an extra descriptor word per object and no thread-local free lists). A custom mark procedure per type (more code and slower than bdwgc's descriptor interpreter).

### D-025 · M2.2 split into M2.2a–M2.2d
- **Date:** 2026-10-11
- **Decision:** M2.2 (Classes) becomes M2.2a (class declarations, fields, constructors, `new`, `this`, methods, field access and method calls), M2.2b (`console.log` of instances in Node's format), M2.2c (inheritance, `super`, vtables, abstract classes, `instanceof`) and M2.2d (accessors, statics, `private`/`#private`, parameter properties). No scope is dropped.
- **Reason:** The session protocol's rule for tasks too big for one session. Each part is testable on its own: M2.2a's tests print fields one by one, which Node prints the same way.
- **Alternatives rejected:** One task (several sessions without a green, ticked step). Splitting by compiler stage (no part would be usable alone).

### D-026 · How classes compile (M2.2a)
- **Date:** 2026-10-11
- **Decision:** A class declared at a module's top level is an `ir.Class`, and the static type of a reference to an instance. In C it is a struct `pts_class_<Name>` (the `pts_header` type pointer, then the fields in declaration order with their static C types; instances are pointers to it) and a `static const pts_type pts_type_<Name>` with the field and method tables and a GC layout the C compiler computes with `offsetof` (`PTS_GC_WORD`, D-024); outside the entry module the names carry the module's, as functions' do. The constructor and methods are C functions `pts_fn_<Class>_<name>` taking `this` first. `new C(args)` evaluates the arguments, allocates a zeroed instance (`pts_object_new`) and calls the constructor, which runs the field initialisers in declaration order, then its body; a field without an initialiser starts at its type's zero value (`""`, 0, `false`, `undefined` for a dynamic value), as `let x: T;` does (D-019). Method calls are direct calls. Fields and methods are found by their declarations, because the checker gives an instance's members symbols of their own. A compound assignment or `++` on a field of an instance computed by an expression with effects evaluates that expression once (`ir.Bind`). References to instances are never null until unions arrive (M3.1). The semantic pass applies the numeric rules to fields (initialisers, reads, assignments), method parameters and results, and constructor arguments. Inheritance, accessors, statics, `#private`, parameter properties, optional fields, generic and abstract classes, classes in functions, and methods as values are PTS1001 until their tasks (M2.2c, M2.2d, M2.6, M3.1, M2.5).
- **Reason:** Fixed layouts (D-L10) make a field access one load and keep the C readable, and one descriptor serves the collector, `console.log` and JSON. Declaration order is JS's property order for classes compiled with `useDefineForClassFields` (the ESNext default), so `console.log` (M2.2b) can list fields as Node does. Keeping allocation out of the constructor leaves room for `super(...)` (M2.2c), where a base constructor initialises an instance the derived `new` allocated.
- **Alternatives rejected:** Hash-map objects (slow, and against D-L10). Constructors that allocate and return the instance (no way to run a base constructor on a derived instance). Keying members by symbol (misses the checker's instantiated member symbols). Evaluating the instance twice in `o().x += 1` (wrong when `o()` has effects).

### D-027 · `console.log` of instances is a runtime port of `util.inspect`, driven by descriptors
- **Date:** 2026-10-11
- **Decision:** The runtime (`pts/runtime/src/inspect.c`) formats a class instance from its `pts_type`: the class name, then each field in descriptor order as `key: value`, following Node's `formatValue`, `formatRaw`, `reduceToSingleString` and `isBelowBreakLength` with the default options (depth 2, `breakLength` 80, `compact` 3), `strEscape` for strings (quote choice, escapes, lone surrogates as `\uXXXX`), Node's `keyStrRegExp` for bare keys, `[Name]` beyond depth 2 (but `Name {}` for an instance without fields at any depth), and `<ref *n>`/`[Circular *n]` for cycles. Line lengths count UTF-16 units, as Node's `.length` does. 64-bit integer fields print every digit. An object field still NULL (read before the constructor assigned it) prints `undefined`. The output is built in C heap buffers, never the GC heap.
- **Reason:** One generic formatter over descriptors serves every class without generated code per class (D-L09), and porting Node's functions line for line makes the output match byte for byte (D-O16); it grows into the full port in M3.6 (arrays, `Map`, `Set`, format strings).
- **Alternatives rejected:** Generating a print function per class in C (code bloat; the inspect rules would be spread over generated code). Formatting in the compiler (instances only exist at run time).

### D-028 · How inheritance compiles (M2.2c)
- **Date:** 2026-10-11
- **Decision:** A subclass's C struct starts with its parent's struct as the member `pts_base`, so an inherited field is `o->pts_base.f` and an instance converts to an ancestor with a cast (`ir.ClassCast`), which C allows because a struct pointer converts to a pointer to its first member. `ir.Class` gains `Parent` and `Abstract`; `Fields` are the class's own, and the descriptor lists `AllFields` (inherited first) with `offsetof` paths. A method is an `ir.Method`; it is *virtual* when it is abstract, overridden or an override, and every chain of overrides has one *slot*, its topmost method. A class whose hierarchy has slots gets a generated method-table struct `pts_vtable_<C>` that starts with the parent's (`pts_base`) or with the `pts_type` itself (`pts_descriptor`), followed by a typed function pointer per slot the class introduces; its descriptor `pts_type_<C>` is that struct, so the instance header points at the `pts_type` inside it, and a virtual call is `((const pts_vtable_<Slot> *)o->pts_header)->m(o, …)`: two loads, as in C++. Every implementation of a slot takes `this` as the slot's class and casts it on entry (`pts_this`), so the table holds the functions without casts and every call is through the function's own type. A call is virtual only when a subclass of the receiver's static class overrides the method (or it is abstract); otherwise it is a direct call, which C can inline (class hierarchy analysis over the whole program). `super.m()` is a direct call; `super(args)` calls the parent constructor on `this`, and the subclass's field initialisers run right after it returns, as in JS; a subclass without a constructor passes its arguments on. A field redeclared with the same type is the inherited field with a new initialiser. `instanceof` is `pts_instance_of`, a walk up the `parent` chain. Narrowing is trusted: an instance converts to a subclass only where the checker narrowed it to that subclass. The semantic pass gives an override without a result annotation the overridden method's numeric result type, a redeclared field without an annotation the inherited field's, and `new` and `super(...)` arguments the nearest declared constructor's parameter types. PTS1001 for now: overrides with other parameter or result types (covariant results, fewer parameters), abstract fields, extending anything but a class of the program (`Error` arrives in M3.2), using an instance where a structurally equal but unrelated class is expected, `super(...)` inside a larger expression in a class with fields, and `instanceof` with anything but a class of the program on the right.
- **Reason:** Embedding keeps the layout prefix-compatible in standard C, so clang's type-based alias analysis stays correct (a flattened copy of the parent's fields would be a different struct type, which struct-path TBAA may assume doesn't alias). Typed method tables keep calls readable and free of function-pointer casts while costing what C++ vtables cost, and only hierarchies that override anything pay for them. Devirtualising by the whole-program class hierarchy makes the common case (no override below the static type) a direct, inlinable call. Running initialisers after `super()` returns and dispatching virtually from constructors match JS's order of effects.
- **Alternatives rejected:** Flattened subclass structs (alias-analysis hazards). An untyped `void (**)(void)` table in `pts_type` (casts at every call, and calls through mismatched function types unless the thunks are generated). A third load through a `vtable` pointer in `pts_type`. Dispatch by comparing type pointers (cost grows with the number of subclasses). Thunks per override to adapt `this` (an extra jump and more generated code). Making every method virtual (no inlining).

### D-029 · M2.2d split into M2.2d1 and M2.2d2
- **Date:** 2026-10-11
- **Decision:** M2.2d (accessors, statics and privacy) becomes M2.2d1 (getters and setters, abstract fields, parameter properties, and the `private`, `protected` and `readonly` modifiers) and M2.2d2 (static fields, methods and blocks, and `#private` fields and methods). No scope is dropped.
- **Reason:** The session protocol's rule for tasks too big for one session. M2.2d1 is about instance members and dispatch (accessors reuse the method tables of D-028); M2.2d2 adds storage outside instances (statics) and members hidden from `console.log` (`#private`). Each part is testable on its own against Node.
- **Alternatives rejected:** One task (too big for one green step). Splitting accessors from abstract fields (abstract fields are accessors underneath).

### D-030 · How accessors, abstract fields and parameter properties compile (M2.2d1)
- **Date:** 2026-10-11
- **Decision:** A getter or setter is an `ir.Method` of kind `Getter` or `Setter` (`ir.MethodKind`), with a function named `<Class>.get_<name>` / `<Class>.set_<name>`; overriding, slots and devirtualisation work as for methods (D-028), keyed by name and kind, and a slot's member in the method table is `pts_get_<name>` / `pts_set_<name>`, which can't collide with a method's. Reading an accessor property calls its getter; assigning calls its setter and has the stored value as its value (`o.p = v` evaluates `o`, then `v`, then calls the setter); a compound assignment or `++` calls the getter once, then the setter, with the instance evaluated once (`place` gains accessor properties: `current` and `stored` temporaries). `super.p` calls the parent's getter directly. An abstract field (`abstract name: T`) is an abstract getter and, unless `readonly`, an abstract setter; a subclass that declares the field implements them with generated accessors over its field (`implementAbstractAccessors`), or declares accessors itself; reads through a reference of the subclass's static type stay plain field reads. A parameter property is a field declared before the class's other fields, stored at the start of the constructor (in a subclass, right after `super(...)`, before the field initialisers), which is the property order Node shows. `private`, `protected` and `readonly` change nothing at run time: tsc enforces them, and `console.log` shows such fields, as Node does. The semantic pass types accessors like functions, gives an accessor property its getter's numeric type (or its setter's parameter type), and gives a parameter property its parameter's. `ir.Method.Abstract` replaces testing for a missing body, since bodies are lowered after calls may be. PTS1001: a getter and setter of different types or classes, reading a property that has only a setter, assigning through `super.p`, and auto-accessors (`accessor x`). A subclass that overrides only the getter of a get/set pair keeps the parent's setter, where JS would leave the property without one (and throw TypeError on assignment); tsc rejects that assignment through the subclass's own type.
- **Reason:** Accessors are methods with a calling convention, so reusing methods (and their tables) gives overriding and devirtualisation for free and keeps the C a plain call. An abstract field has no storage of its own (each subclass decides), so reading it through the base is necessarily a dispatch, which accessors already are. Storing parameter properties first matches Node's printed property order (it runs TypeScript through swc, which assigns them before the field initialisers).
- **Alternatives rejected:** Accessor properties as a separate IR concept with their own tables (duplicated dispatch logic). Storing abstract fields at a fixed offset in the base (subclasses may implement them with accessors, and their own fields come after the base's). Parameter properties stored where tsc's emit puts them (after the field initialisers), which Node doesn't do.

### D-031 · How statics and `#private` members compile (M2.2d2)
- **Date:** 2026-10-11
- **Decision:** A static field is a module-scope binding of its class's module (an `ir.Local` named `<Class>.<field>`, always `Global`, so a C global `pts_global_<Class>_<field>` with a ready flag), declared by a `LocalDecl` at the class declaration's place in the module's top-level code; static fields' initialisers and static blocks run there in declaration order. Reading one before the class declaration has run throws ReferenceError naming the class, as JS does for the class binding. A static method is a plain function `<Class>.<method>` without `this`. `C.f`, `m.C.f` and, in static code, `this.f` and `this.m()` name the declaring class's members statically; reads may go through a subclass (`Derived.count` reads `Base.count`). Two cases where JS would differ are PTS1001: assigning to an inherited static field through a subclass (JS gives the subclass a field of its own) and calling, through a subclass, a static method that uses `this` (JS's `this` would be the subclass). `this` as a value in static code is PTS1001 (classes as values come later). A `#private` member is a member whose name keeps its `#`: it never overrides or redeclares an inherited member (a subclass's `#x` is a field of its own, in its own part of the struct), its C names are `pts_private_<x>` for a field and `private_<x>` inside function and global names, and it is left out of the descriptor's field table, so `console.log` (and later JSON) don't show it, as in Node, while the GC layout still covers it. `#x in o` is `pts_instance_of(o, C)` for the class C that declares `#x`, since only instances of C and its subclasses have it. Static getters and setters are PTS1001.
- **Reason:** Reusing module-scope bindings gives statics their storage, their initialisation order and their TDZ check with no new machinery, and keeps every access a plain global load. Resolving static members statically is what makes them free; the two subclass cases are the only ones where the static answer and JS's dynamic one can differ, so they are errors rather than silent divergences. Keeping `#` in the name makes private members distinct from public ones of the same name and from a subclass's private ones without a separate namespace.
- **Alternatives rejected:** A per-class statics struct (one more indirection, and a second kind of global). Copying statics into subclasses (wrong after an assignment to the base). Dispatching static calls on a hidden class argument (cost on every static call for a rare pattern). Name-mangling `#x` to `x_` (collides with a public `x_`).

### D-032 · M2.3 split into M2.3a and M2.3b; optional properties move to M3.1
- **Date:** 2026-10-11
- **Decision:** M2.3 becomes M2.3a (object literals as anonymous struct types, printed as Node prints plain objects, and object types used exactly: every value of an object type is a literal of that type) and M2.3b (interfaces: structural dispatch through per-(concrete type, interface) tables, so class instances and object values of other types can be used where an object type is expected). Optional properties, which D-L10 stores as `T | undefined`, move to M3.1 with unions, because reading one gives a union. No other scope is dropped.
- **Reason:** The session protocol's rule for tasks too big for one session. M2.3a is the common record pattern (an object type and literals of it), testable against Node on its own; its exact layouts stay the fast path once M2.3b adds dispatch for everything else. An optional property is only useful with narrowing (`p.x !== undefined`, `??`), which is M3.1's.
- **Alternatives rejected:** One task (too big for one green step). Dispatch tables first (the literals and their printing are needed by both parts anyway). Storing optional properties as dynamic values now, with nothing able to use them.

### D-033 · How object literals and exact object types compile (M2.3a)
- **Date:** 2026-10-11
- **Decision:** An object type of the program (an interface, type literal, alias of one, or an object literal's type; not a class, a function type, a type with index signatures or a type of the ES library) maps to a *shape*: an `ir.Class` with `Shape` set, one field per property in name order, and nothing else. Object types with the same properties of the same IR types share a shape (`shapeKey`), so `interface Point`, `{ x: number; y: number }` and the type of `{ y: 1, x: 2 }` are one C struct `pts_class_object<n>`; a recursive object type gets a shape of its own. Until M2.3b, a value of an object type is always an object of exactly its shape: a class instance or an object of another shape where one is expected is PTS1001. An object literal (`ir.NewObject`) allocates an object of its type's shape and stores each property's value in the literal's order. Because JS lists an object's properties in the order they were created, every distinct property order of a shape gets its own descriptor (`pts_type_object<n>`, `pts_type_object<n>_2`, …), sharing the struct and GC layout; descriptors of shapes are named "Object", which `inspect.c` prints as Node prints plain objects (`{ x: 1 }`, `{}`, `[Object]` beyond depth 2), as Node does for any constructor named Object. A property access whose member is declared in an object type of the program reads or writes the shape's field. The semantic pass types an interface or type literal property like a field, and a literal's number-typed property by the property of the literal's contextual type (`GetContextualType`), so `const p: Pixel = { r: 250 }` checks 250 against `u8`; without a context a property takes its value's type. PTS1001: optional properties (M3.1, D-032), methods and accessors in object types and literals (function values arrive in M2.5), spread (M2.6), computed property names, and property names that are array indices (JS orders those first).
- **Reason:** Shapes are D-L10's anonymous struct types, with the same fixed layouts, descriptors and printing as classes, so every property access is one load and no new runtime machinery is needed. Interning by structure is what makes structurally identical types interchangeable without conversions, which is how TypeScript treats them. Per-order descriptors keep Node's property order without giving up a canonical layout; the descriptor pointer every object already has costs nothing extra.
- **Alternatives rejected:** One shape per TypeScript type (identical types written twice wouldn't convert). Layout in each literal's order (two literals of one type would have different layouts). Hash-map objects (against D-L10). A flag in `pts_type` for plain objects (the name "Object" is exactly Node's own rule).

### D-034 · M2.3b split into M2.3b1 (fields) and M2.3b2 (methods and accessors)
- **Date:** 2026-10-11
- **Decision:** M2.3b becomes M2.3b1 (values of object types that may be objects of any class or shape whose fields provide the type's properties, with structural dispatch cached at run time) and M2.3b2 (interfaces with methods, and class methods and accessors that provide an interface's members, through per-(concrete type, interface) tables). No scope is dropped.
- **Reason:** The session protocol's rule for tasks too big for one session. Field access through interfaces is the common case and needs only a field's offset; methods and accessors need function tables and the method-table machinery of D-028 and D-030. Each part is testable against Node on its own.
- **Alternatives rejected:** One task (too big for one green step). Methods first (they build on the dispatch that fields introduce).

### D-035 · How interfaces over fields compile (M2.3b1)
- **Date:** 2026-10-11
- **Decision:** Every object type of the program (D-033's) is now an `ir.Interface` as the type of a binding, field, parameter or result: structural, interned by its properties' names and IR types, a one-word `pts_object *` in C. Shapes stay the concrete classes of object literals; a literal written where an object type with the same properties is expected takes that type's shape (`literalType`), so its values convert to the context's property types. A reference to an object of a class, a shape or another interface converts to an interface (`ir.ClassCast`, a pointer cast) when, for every property of the interface, the object has a field of the same name and the same IR type: then every access through the interface finds a field it can use as it is, and an assignment through it can't break what the object's own type promises. An interface value converts to a class where the checker narrowed it (`instanceof`). A property read or write through an interface finds the field's address with `pts_field_address(object, &site, "name")`, where `site` is a `static pts_site` at that place in the C: a one-word cache of the last descriptor met there and the field's offset in it (descriptor | offset << 48, read and written with relaxed atomics, so racing threads never see half an update); on a miss the runtime finds the field by name in the descriptor's field table. `===`, `?:`, `&&` and `||` meet a class and an interface it provides in the interface. PTS1001 until M2.3b2: a property that a class provides with a getter, interfaces with methods; and always for now, property types that differ in IR type (an `int` field where an interface says `number`).
- **Reason:** D-L10 asks for dispatch per (concrete type, interface) pair, built at compile time where possible and cached at run time otherwise. With one-word values (D-002) the table has to be found from the object at the place of use, and a per-site cache keyed by the descriptor is the fastest way to find it: one load and one compare on a hit, the same as an inline cache in a JS engine. Fields need no table beyond the descriptor's field list, which already exists; methods and accessors (M2.3b2) will add per-pair tables of functions. Requiring identical property types keeps every object's fixed layout honest under writes through any interface.
- **Alternatives rejected:** Fat pointers carrying a table (two words, against D-002, and lost on boxing). Exact shapes for annotated object types (a class instance could never be passed where the object type is expected). A global (descriptor, interface) hash table looked up on every access (slower than a per-site cache, and still a lookup on every call). Converting representations (an `int` field read as a `number`) at run time (every access would need the field's kind).

### D-036 · How methods and accessors through interfaces compile (M2.3b2)
- **Date:** 2026-10-11
- **Decision:** An `ir.Interface` now also has `Methods` (`ir.InterfaceMethod`: name, parameter and result IR types), from the method signatures of interfaces and type literals; accessor signatures (`get x(): T`, with or without `set`) declare properties like `x: T`. A class provides an interface's property with a field of the same IR type, or with a getter returning that type (and a setter taking it, if it has one); it provides a method with a method of the same name, parameter types and result type. `o.m(args)` on an interface value is `ir.InterfaceCall`. Each class's descriptor carries, in its `methods` table, the methods, getters and setters that interfaces may call (named like a method of some interface, or like an accessor-provided property of one), inherited ones included with the implementation the class has, each through an *entry point* `pts_entry_<function>` that takes `this` as `pts_object *` and calls the method with it as its class (so every call is through the function's own type). A call site through an interface is `((R (*)(pts_object *, P…))pts_method_find(o, &site, "m", PTS_METHOD))(o, args)`, where the site's one-word cache (D-035's `pts_site`) holds the last descriptor met there and the method's index in its table. A property of an interface that some class of the program provides with an accessor (`ir.Field.Accessor`, set by a whole-program pass over the classes' getters and setters) is read and written with `PTS_GET`/`PTS_SET`, whose site caches either the field's offset or, flagged, the accessor's index; every other interface property keeps D-035's field-only site. Reading such a property in a compound assignment or `++` reads it once (`current`), as JS does. Writing, through an interface, a property whose class has only a getter throws `TypeError: Cannot set property x of #<C> which has only a getter`, as in JS. Branches of `?:`, `&&` and `||` of unrelated classes convert to their contextual interface when both provide it. PTS1001: object literals with methods or accessors (function values arrive in M2.5), methods as values, setter-only properties in object types, overloaded and generic methods in object types, and members whose IR types differ from the interface's (as in D-035).
- **Reason:** D-L10 asks for per-(concrete type, interface) tables built at compile time and cached at run time. With one-word values (D-002) the table must be found from the object where it is used, so the compile-time part is each descriptor's member table (fixed per class, inherited members resolved), and the run-time part is the per-site cache: one load and one compare on a hit, then one indexed load, like an inline cache. Entry points keep calls type-correct in C without a function-pointer cast at every call, and clang turns them into a jump. Choosing the accessor-capable path only for property names some class has accessors for keeps plain field access through interfaces exactly as fast as in M2.3b1.
- **Alternatives rejected:** Tables per (class, interface) pair computed as a whole-program closure of conversions (fragile through interface-to-interface conversions and subclasses, and the site still has to find the pair's table from the object). Fat pointers (two words, against D-002). Calling methods through `pts_object *` without entry points (calls through mismatched function types). Making every interface property access a call (slower for the common field case). A branch on field-or-accessor at every interface property site (needless where no class has an accessor of that name).

### D-037 · M2.4 split into M2.4a, M2.4b and M2.4c; the callback methods follow closures
- **Date:** 2026-10-11
- **Decision:** M2.4 (Arrays) becomes M2.4a (the array type, literals, `length`, indexing, `push`/`pop`/`shift`/`unshift`, `for…of` over arrays, `Array.isArray`, and `console.log` of arrays), M2.4b (the methods that take no callback, and `split`) and M2.4c (the methods that take callbacks). M2.4c moves after M2.5, because callbacks are function values, which arrive with closures and arrow functions in M2.5. No scope is dropped.
- **Reason:** The session protocol's rule for tasks too big for one session. Callback methods without function values would need a throwaway special case for each method. Each part is testable against Node on its own.
- **Alternatives rejected:** Pulling closures forward into M2.4 (M2.5 is a large task of its own). Lowering callbacks only for arrow-function literals written at the call site, with no general function values (thrown away in M2.5).
