# ParallelScript: Mission Brief

You are going to build **ParallelScript**, a new programming language, over a long autonomous run. This document is your mission brief and the source of truth for every design decision made so far. Read all of it before doing anything.

## 0. The pitch

ParallelScript is TypeScript that compiles to fast native binaries, has a garbage collector, and can actually use every core.

- **Syntax:** TypeScript, plus a tiny number of new keywords for parallelism (currently one: `parallel`). A TypeScript developer should be able to read any ParallelScript program without explanation.
- **Execution:** compiled ahead of time to a native binary. No VM, no JIT, no JavaScript engine.
- **Memory:** garbage collected. No ownership, no borrow checker, no lifetimes.
- **Concurrency:** one event loop per core over a shared heap. Plain `await` keeps exact JavaScript semantics. Parallelism is always explicit and opt-in.

**About the owner, so you understand the taste behind these decisions.** The owner has been a TypeScript developer for four years and loves the language. They dislike Go because its syntax and conventions feel ugly and over-opinionated. They respect Rust, and especially Rayon, but find its strict type system too much for application code. ParallelScript should feel like TypeScript with superpowers. It should never feel like a stricter language wearing a TypeScript costume.

This is a fun side project: ambitious, but nobody's production depends on it. Optimise for things that work end to end and are fun to show off.

## 1. How to run this project

You have a Linux machine with sudo and GitHub access. This run will last far longer than your context window, so the repository is your memory.

### 1.1 Setup

- Create a **private** GitHub repo named `parallelscript`. The owner can make it public later.
- Inspect the machine: `uname -m`, distro, core count, RAM. The primary target is this machine's platform.
- Install what you need:
  - a Go toolchain matching typescript-go's `go.mod`
  - clang, make, cmake
  - Node.js LTS (for differential testing) and Bun (for benchmarks)
  - hyperfine, plus `oha` or `wrk` for HTTP benchmarks

### 1.2 Memory files (create these first and keep them current)

- **`docs/SPEC.md`** is the language reference. Start it from section 2 of this brief and extend it as you implement. If a behaviour isn't in the spec, it isn't decided.
- **`docs/DECISIONS.md`** is an append-only log. Each entry has the date, the decision, the reason and the alternatives rejected. Enter section 2's decisions first, marked "locked by owner".
- **`PROGRESS.md`** records the current milestone, what's done, what's next, known issues, and the exact commands to build and test. Update it at every milestone and before any long or risky step.
- **`docs/BENCHMARKS.md`** holds a results table per milestone.

At the start of every session, and whenever you lose track, read these files in order before writing code: `PROGRESS.md`, then `docs/DECISIONS.md`, then the relevant parts of `docs/SPEC.md`.

### 1.3 Working rules

- **Work in vertical slices.** Get something running end to end, then widen it. A feature isn't done until it has tests and they pass.
- **Keep `main` green.** Commit small and often, and push regularly. Tag each finished milestone (`m0`, `m1`, …).
- **Don't edit tests to make them pass.** The exception is a test that is itself wrong; say why in the commit message.
- **Don't reopen locked decisions.** If you become convinced one is wrong, write `docs/proposals/NNN-title.md` and message the owner. Keep working on other things in the meantime.
- **Time-box rabbit holes.** If a bug survives about five serious attempts:
  - record it in `PROGRESS.md` under "Known issues"
  - add a skipped test that reproduces it
  - move on
- **Set up CI early.** GitHub Actions should build and run the full test suite on every push.

### 1.4 Talking to the owner (Telegram)

- **Message at each finished milestone.** Include what now works, a short code sample that shows it off, and benchmark numbers if there are any. Telegram caps messages at 4096 characters, so keep it short.
- **Ask only when it matters.** That means a choice that changes user-visible language semantics, where neither the spec nor section 3's tie-breakers settle it. Otherwise decide, log it in `DECISIONS.md`, and keep going.
- **Never wait idle for a reply.** Work on something else.

## 2. Locked design decisions

### 2.1 Syntax and files

- Source files use the extension `.pts`. The syntax is TypeScript plus the `parallel` keyword (section 2.6).
- `parallel` is a **contextual** keyword, like `async` and `await`. Existing code that uses `parallel` as an identifier must keep working. It is a keyword only in two positions:
  - `parallel <expression>` in expression position
  - `parallel for (...)` in statement position
- **The compatibility principle.** Consider a `.pts` program that uses only plain TypeScript features: no `parallel`, no integer types, no `pts:` imports. It must print exactly what Node prints when run as TypeScript, except where this spec documents a difference.

### 2.2 Front end: a fork of TypeScript 7

- TypeScript 7 (released July 2026) is a native port of the TypeScript compiler to Go: https://github.com/microsoft/typescript-go.
  - Fork it and pin to the latest 7.x release tag.
  - The fork gives you the scanner, parser, binder, checker and language server.
- **The compiler (`pts`) is written in Go, inside the fork.** typescript-go keeps most of its code under `internal/`, and Go only lets packages in the same module import those. So the backend must live in the fork's module, not in a separate one. Verify this against the actual repo layout and adapt.
- **Keep upstream modifications minimal and greppable.**
  - Mark each one with `// PTS:` and list it in `docs/FORK.md`, so rebasing onto newer TypeScript releases stays manageable.
  - Put new code in new directories, such as `pts/` for the backend, runtime, stdlib, tests and docs.
- Keep the upstream parser and checker test suites passing, at least for the areas your changes touch.
- Keep the Apache-2.0 `LICENSE` and `NOTICE` intact. ParallelScript's own code can use the same license.
- Declare the prelude types and the `pts:` modules in ParallelScript's own lib declaration files, so that both the checker and the editor resolve them.

### 2.3 Compilation pipeline

The pipeline runs in this order:

1. TS AST plus checker
2. **PTS semantic checks**: the rules from this spec that tsc doesn't know
3. Lowering to a typed **ParallelScript IR**
4. IR passes
5. Emit **C11**
6. Compile with **clang -O2** and link with the runtime into one native binary

**The IR.** Use a structured, fully typed IR in which all of the following are explicit:

- boxing
- closure environments
- monomorphized generics
- async functions and generators, already turned into state machines

Structured rather than SSA is fine, because clang does the heavy optimisation. The IR is what makes monomorphization, closure conversion and later escape analysis possible, so don't emit C directly from the TS AST.

**Generated C** must be readable. It must carry `#line` directives pointing at the `.pts` source, so that gdb, lldb and crash backtraces show ParallelScript lines.

**The runtime** is written in C (`pts/runtime/`) and statically linked.
- Vendor its dependencies (bdwgc, libuv) as pinned submodules and build them from source.
- The goal is that `pts build` output depends only on libc.

**The CLI:**

| Command | Purpose |
|---|---|
| `pts build file.pts -o out` | Build a native binary |
| `pts run file.pts` | Build and run |
| `pts emit-c file.pts` | Print the generated C, for debugging |
| `pts test` | Test runner (later) |
| `pts lsp` | Language server (milestone 7) |

Cross-compiling with `pts build --cc zig` is a stretch goal.

### 2.4 Types and values

**Variables have fixed types.** `let x = 1; x = "s"` is a compile error, which tsc already catches.

**Numbers.**
- `number` keeps JavaScript semantics exactly: IEEE-754 f64, including `NaN`, `Infinity` and `-0`.
- **The added types** are `int` (meaning i64) plus `i8 i16 i32 i64 u8 u16 u32 u64 f32 f64`, declared in a global prelude.
  - Start them as aliases of `number` in the prelude, so the editor accepts them immediately.
  - Enforce the numeric rules in the PTS semantic pass.
  - Later, surface those diagnostics in the language server too.
- **Literals take their type from context.** `const x: u8 = 200` is fine, and `const y = 5` is `number`, as in TS.
- **Implicit conversions are lossless only.**
  - Allowed implicitly: any integer type to `number`, and a smaller integer to a larger integer of compatible sign.
  - Anything lossy needs `as`.
  - Mixing `int` and `number` in arithmetic yields `number`.
- **`as` between numeric types converts the value**, following Rust's rules:
  - float to int truncates toward zero and saturates at the target type's range
  - `NaN` becomes 0
- **Integer arithmetic:**
  - Overflow wraps (two's complement).
  - Integer `/` is truncating integer division.
  - Integer division or modulo by zero throws `RangeError`.
- `typeof` on any numeric type returns `"number"`.

**Strings.**
- Strings are immutable, stored as UTF-8, and each one records whether it is pure ASCII.
- **Indexing keeps JavaScript's semantics**, so ported code behaves identically. `.length`, `s[i]`, `charCodeAt` and `slice` indices all count UTF-16 code units.
  - ASCII strings take an O(1) fast path.
  - Non-ASCII strings may build an index lazily.
- **Number-to-string conversion must match JS exactly.** For example, `0.1 + 0.2` prints `0.30000000000000004`. Use shortest round-trip formatting, such as a Ryu-style algorithm.

**`any`** is a checked dynamic value.
- Anything is assignable to it.
- **Inside the dynamic world, operations follow JavaScript semantics.** Property access, calls, indexing and operators are resolved at runtime using type descriptors:
  - a missing property reads as `undefined`
  - calling a non-function throws `TypeError`
  - `+` concatenates when either side is a string
- **The check happens at the boundary into typed code.** Assigning an `any` to a typed location verifies the value at runtime and throws `TypeError` on mismatch. For example, `const n: number = parsed.count` throws if `count` isn't a number.
- This combination keeps `JSON.parse(s).user?.name` working while making `any` honest.
- It requires every runtime type to carry a descriptor (name, field names mapped to offsets, methods). Reuse those descriptors for `console.log` and JSON.

**`unknown`** works exactly as in TypeScript: you must narrow before use.

**`as` on non-numeric types:**
- Upcasts are free.
- Downcasts and casts to unrelated types are checked at runtime and throw `TypeError`.
- `x!` is checked too, and throws on `null` or `undefined`.
- `as unknown as T` cannot be used to lie about a value's type.

**Objects.**
- **Classes** have fixed layouts and use vtables. Support:
  - single inheritance, fields, methods, getters and setters, static members
  - `private` and `#private`, abstract classes, `instanceof`
- **Interfaces and object types stay structural**; tsc checks them.
  - At runtime, a value used through an interface type dispatches through a table of field offsets and method pointers, one per (concrete type, interface) pair.
  - Build these tables at compile time where possible, and cache them at runtime otherwise.
- **Object literals** become instances of anonymous struct types derived from their checked type. Optional properties are stored as `T | undefined`.
- **Properties cannot be added or deleted after construction.** `delete` only works on index-signature types. `Record<string, T>` and `{ [k: string]: T }` are hash maps under the hood.
- **`null` and `undefined`** remain distinct values. Optional chaining and `??` are supported.

**Unions** are tagged values.
- Narrowing must work with every form: `typeof`, `instanceof`, `in`, discriminant properties, equality and user-defined type guards.
- The checker does the reasoning; the backend emits tag tests.

**Generics** are monomorphized.

**Equality.**
- `===` compares objects by reference identity, and strings and numbers by value, with JS's `NaN` rules.
- `==` is supported with its JS meaning for the types involved; in practice it's mostly `x == null`.

**Exceptions.**
- `throw`, `try`, `catch` and `finally` have JS semantics, and any value can be thrown.
- `Error` and its subclasses have `message`, `name`, `cause` and `stack`. `stack` should show `.pts` file and line numbers; it can come in a later milestone.

**Not supported.** Each of these gets a clear compile error explaining why:
- `eval`, `new Function`, `with`
- `var` (the error suggests `let`)
- `arguments`
- prototype manipulation: `__proto__`, `Object.setPrototypeOf`, assigning to `.prototype`
- `Proxy`, `Reflect`
- declaration merging
- namespaces, until there's a reason for them

Decorators, `Symbol` beyond `Symbol.iterator` and `Symbol.asyncIterator`, and `WeakRef` can come later.

### 2.5 Memory and the shared heap

- Memory is garbage collected. Start with **bdwgc** (Boehm–Demers–Weiser) in thread-aware mode with thread-local allocation.
- Allocate pointer-free data, such as string bytes and numeric arrays, with the atomic (no-scan) allocator. This is a large performance win.
- A precise collector, and later a generational one, is a later milestone, not a starting point.

**Non-negotiable invariant.** A program with data races may observe stale or surprising values, but it must never crash the runtime, corrupt the heap, or read or write out of bounds. This is Java's guarantee, not Go's. Concretely:

- **No torn values.** Any value in a heap slot that can be read concurrently must be one machine word (a pointer, a small scalar, or a box), or be written and read atomically. No multi-word values that can tear in shared fields.
- **No out-of-bounds writes.** Growable arrays keep their capacity inside the backing buffer, and bounds checks use the length and capacity of the buffer actually being accessed. Racing `push`es can lose elements but never write past the end.
- **Strings are immutable**, so sharing them is free.

### 2.6 Concurrency: the reason this language exists

**The model.**
- The runtime starts one event loop per core. The count is configurable, for example with `PTS_THREADS`.
- Each loop runs on its own OS thread, and all loops share one heap.
- Use **libuv**, one `uv_loop_t` per thread, for timers, sockets, filesystem, DNS and child processes. Use `uv_async_send` to wake a loop from another thread.

**Plain `await` keeps exact JavaScript semantics.**
- A task belongs to one loop for its whole life, and a loop runs one task at a time.
- So code between two `await`s runs without interference from other tasks on the same loop. Existing TS code silently relies on this property.
- For single-loop code, the ordering of microtasks, timers and I/O callbacks must match Node. Differential tests check this.

**Async functions and generators** compile to state machines, as in C#, JS engines and Rust.
- Locals that live across an `await` or `yield` are stored in a GC-managed frame object.
- Don't use a separate native stack per task. Heap frames keep the GC simple and make 100k concurrent tasks cheap.

**Task parallelism with `parallel <expr>`:**
- It evaluates `<expr>` as a new task on another loop and immediately returns a `Promise<T>`. The promise is flattened if `<expr>` is itself a promise.
- Captured variables are shared by reference, because the heap is shared.
- `await parallel f(x)` is the common idiom, and it composes with the rest of the language:
  ```ts
  const report = await parallel buildReport(user);
  const pages = await Promise.all(urls.map((u) => parallel crawl(u)));
  ```
- Pick the target loop by load, for example least-loaded, or round-robin with stealing of tasks that haven't started yet.
- A task never migrates once it has started.
- Promises can be awaited from any loop, and settling one wakes the loop that is awaiting it.

**Data parallelism: Rayon for TypeScript.**

The pool:
- Build a work-stealing fork-join pool with Chase–Lev deques, one per worker, sized to the core count. It is separate from the event-loop threads.
- A thread that starts a parallel operation helps execute work until the operation finishes.
- Study Rayon's design (`join`, adaptive splitting) and copy it shamelessly.

The `.par` view:
- `.par` on arrays, typed arrays and ranges returns a parallel view with the familiar methods: `map`, `filter`, `forEach`, `reduce(fn, initial)`, `flatMap`, `some`, `every`, `find` (any match), `count`, `sum`, `min`, `max`, `sort`/`sortBy` (parallel merge sort) and `toArray`.
- These methods are synchronous: they return when the work is done.
- `.par.map` preserves order.
- `reduce` documents that its function must be associative.

Helpers:
- `range(start, end)`, from `pts:parallel` or the prelude, makes `range(0, n).par.forEach(...)` work.
- `join(a, b)` runs two closures, potentially in parallel, and returns both results. It's for recursive divide-and-conquer.

`parallel for`:
- `parallel for (const x of iterable) { ... }` is sugar for a parallel `forEach`. The statement finishes when every iteration has.
- Inside the body, `continue` is allowed. `break`, `return`, `yield`, `await` and labelled jumps out of the loop are compile errors.
- If an iteration throws, cancel the remaining iterations as best you can, and rethrow the first error after joining.

Example:
```ts
const hashes = files.par.map((f) => sha256(f));
const total = orders.par.map((o) => o.amount).sum();

parallel for (const img of images) {
  img.resize(800, 600);
}
```

**The single guardrail.**
- A *parallel body* is any of:
  - the operand of `parallel`
  - a `parallel for` body
  - a function passed to a `.par` method or to `join`
- Inside a parallel body, assigning to a captured local variable (a `let` declared outside the body) is a compile error. The message suggests `.par.reduce`, `.par.sum` or an `Atomic`.
- To make this checkable, mark the parallel-callback parameters in the stdlib declarations with a branded function type, so the check knows which closures run in parallel.
- Mutating object fields from parallel code is allowed.
- **Do not add any other parallelism checks without asking the owner.** They explicitly don't want Rust-style strictness.

```ts
let count = 0;
files.par.forEach(() => { count++; }); // compile error: use .par.reduce() or an Atomic
```

**`pts:sync`** provides:
- `Mutex<T>`: a blocking `withLock(fn)` for short critical sections, plus an async `lock()` that doesn't block the loop
- the `AtomicInt` / `Atomic<int>` family: `load`, `store`, `add`, `compareExchange`
- `Channel<T>`: bounded or unbounded, async `send` and `recv`, `close`, and iterable with `for await`
- `Once`

Add a concurrent map later if benchmarks or examples want one.

### 2.7 Modules and standard library

**Modules.**
- ES modules with relative imports.
- Standard library modules use the `pts:` prefix: `import { Mutex } from "pts:sync"`.
- No npm or `node_modules` in v1. Compiling pure-TS npm packages that fit the supported subset is a stretch goal.

**Globals.**
- `console`. Its output must match Node's `util.inspect` for primitives, arrays, plain objects, class instances (like `Point { x: 1, y: 2 }`), `Map` and `Set`. The differential tests depend on this.
- `Math` and `JSON`.
- `Promise`, including `all`, `allSettled`, `race`, `any` and `withResolvers`.
- `setTimeout`, `setInterval`, `clearTimeout` and `queueMicrotask`.
- `performance.now()`.
- `fetch` (with `pts:http`).

**Built-ins.**
- The commonly used parts of `String`, `Array`, `Map`, `Set` and `Number`.
- `Object.keys`, `Object.values` and `Object.entries`.
- `Date` (basic).
- `RegExp`, backed by a library such as PCRE2 or a port. It can arrive in a later milestone.

**`pts:` modules.**

| Module | Contents |
|---|---|
| `pts:fs` | Sync and promise-based file APIs |
| `pts:path` | Path utilities |
| `pts:process` | `argv`, `env`, `exit`, `cwd` |
| `pts:http` | A server with an option to accept connections on every loop, plus `fetch` as a global |
| `pts:sync` | Synchronization primitives (section 2.6) |
| `pts:parallel` | `range`, `join` and related helpers |

## 3. Tie-breakers when the spec is silent

Apply these in order, and log every such call in `DECISIONS.md`.

1. Memory safety under races (section 2.5) is never traded away.
2. For plain TypeScript code, do what Node does.
3. Don't add strictness the owner didn't ask for. Prefer a runtime check that throws over a compile error that blocks a reasonable program.
4. For parallel semantics, do what Rayon does for data parallelism, or what Go does for tasks and channels.
5. Simple and working beats clever and half-done.

## 4. Testing

**`pts/tests/run/`** holds programs with expected stdout, either as `name.pts` plus `name.out` or with an `// expect:` block. This is the bulk of the suite.

**`pts/tests/errors/`** holds programs that must fail to compile, with the expected message substring.

**Differential testing against Node.**
- Every `run/` test that uses only plain TypeScript is also run with Node, and the outputs must match.
- Use Node's built-in type stripping, or compile with tsc for constructs that stripping can't handle, such as enums.
- This is your oracle for semantics. Use it relentlessly, and keep growing the corpus with realistic programs.

**Parallel tests.**
- Run them many times (for example 100 iterations in a nightly CI job) to catch flakiness.
- Results must be deterministic wherever the spec says so; for example, `.par.map` preserves order.

**Runtime C code** gets unit tests run under AddressSanitizer and UndefinedBehaviorSanitizer. Try ThreadSanitizer on the fork-join pool and channels; bdwgc may need suppressions.

**Fork changes** must keep the relevant upstream typescript-go tests green.

## 5. Benchmarks

Put a script in `pts/bench/` that compares ParallelScript against Node, Bun and Go using hyperfine. Record the results in `docs/BENCHMARKS.md` at each milestone.

The suite:
- **Single-threaded:** recursive fib, nbody, spectral-norm, binary-trees (GC stress), string building, `Map`-heavy word frequency, JSON parse and stringify.
- **Parallel:**
  - mandelbrot with `.par`
  - parallel sum/reduce over 100M numbers
  - parallel word count over many files
  - `parallel` fan-out of CPU-heavy tasks

  For each one, report the speedup over a single thread.
- **HTTP:** hello-world and JSON-echo servers, measured in requests per second with `oha` or `wrk`.

Goals (aspirations, not gates):
- beat Node and Bun on every CPU benchmark
- land within about 2x of Go on single-threaded benchmarks
- scale parallel benchmarks close to linearly with core count

## 6. Milestones

Each milestone ends with all of the following:
- all tests green
- `PROGRESS.md` and `docs/BENCHMARKS.md` updated
- a tag pushed
- a Telegram message to the owner

**M0: Bootstrap.**
- Repo created; fork pinned and building; memory files written; CI running.
- `.pts` recognised by the front end.
- The pipeline works end to end: `console.log("Hello, ParallelScript")` goes through IR and C to a native binary.
- The test harness works, including the Node differential runner.

**M1: Core language.**
- `number` and the integer types, with the numeric rules.
- Booleans; strings with basic operations and template literals.
- `let`/`const`, functions, recursion, all control flow and operators.
- Exact JS number formatting.
- fib and nbody benchmarks.

**M2: Data and memory.**
- bdwgc integrated.
- Classes and inheritance; interfaces with structural dispatch; object literals.
- Arrays, `Map`, `Set`.
- Generics via monomorphization; closures; enums; destructuring; spread and rest.
- binary-trees benchmark.

**M3: The TypeScript feel.**
- Unions with every narrowing form; `null` and `undefined`; optional chaining; `??`.
- Exceptions.
- `any` as a checked dynamic; `as` casts; `unknown`.
- JSON; the common `String`, `Array` and `Object` methods; `console.log` output matching Node.
- Grow the differential corpus to a few hundred programs.

**M4: Async on one loop.**
- The state-machine transform for async functions and generators, including `function*`, `for...of` over iterables and `for await`.
- Promises with Node-identical ordering; timers; the libuv loop.
- Async `pts:fs` and `pts:process`.

**M5: Parallel.** This is the headline milestone, so make the demo great.
- Add `parallel` to the fork's scanner, parser and checker. It's a contextual keyword, and the checker types `parallel e` as `Promise<Awaited<T>>`, where `T` is the type of `e`.
- Extend the lib declarations with `.par`.
- Multiple loops and cross-loop promises.
- The fork-join pool, `.par`, `range`, `join` and `parallel for`.
- The guardrail and `pts:sync`.
- Stress tests and scaling benchmarks.

**M6: Real programs.**
- `pts:http` (server and `fetch`) and `pts:path`.
- Example apps in `examples/`:
  - a parallel file hasher
  - a JSON API server that uses every core
  - a mandelbrot renderer that writes a PNG
  - a parallel log analyzer
- A README that sells the language using these examples.

**M7: Editor support.**
- A VS Code extension that runs the forked language server for `.pts` files.
- Syntax highlighting, including `parallel`.
- PTS semantic diagnostics shown in the editor.

**M8: Performance.**
- Escape analysis, so non-escaping objects are stack-allocated.
- Integer inference for `number` locals that provably only hold integers (loop counters especially).
- Unboxed numeric arrays.
- Inline caches for `any` property access.
- Then an experiment with a precise GC.

**Stretch goals, in any order:**
- RegExp, if not done already
- decorators
- compiling pure-TS npm packages
- cross-compiling with `zig cc`
- a WebAssembly target
- `pts fmt`
- stack traces with `.pts` line numbers, if not done already
- a web playground

## 7. First actions

1. Inspect the machine and install the toolchains.
2. Learn typescript-go's structure: where the scanner, parser, checker, program and language server live, and how the project is built and tested. Skim Rayon's `join` and work-stealing design.
3. Create the repo, fork and pin TypeScript 7, and write `docs/SPEC.md`, `docs/DECISIONS.md` and `PROGRESS.md` from this brief.
4. Start M0. Send the owner one short message confirming you've started, with your M0 plan.

Have fun with it. ParallelScript is supposed to be a delight.
