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
