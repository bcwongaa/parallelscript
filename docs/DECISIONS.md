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
