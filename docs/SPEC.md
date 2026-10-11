# ParallelScript language specification

ParallelScript is TypeScript compiled ahead of time to native binaries, with a garbage collector and real parallelism. This document is the language reference. It starts from the owner's brief (`docs/BRIEF.md` §2) and grows as features are implemented. **If a behaviour isn't in this spec, it isn't decided.**

Order of authority: `docs/BRIEF.md` (with the owner overrides in `CLAUDE.md`) → this spec → `docs/DECISIONS.md`. Decision ids such as `D-L03` or `D-O05` point into `docs/DECISIONS.md`.

---

## 1. Syntax and files

- Source files use the extension **`.ps`** (D-O01, an owner override of the brief's `.pts`). Declaration files stay `.d.ts`.
- `import "./x"` tries `x.ps` first, then the usual TypeScript extensions.
- The syntax is TypeScript plus one contextual keyword, `parallel` (§5).
- `parallel` is a keyword only in two positions; everywhere else it is an ordinary identifier, so existing code that uses it as a name keeps working:
  - `parallel <expression>` in expression position;
  - `parallel for (...)` in statement position.
- Disambiguation rules for `parallel` are in §5.3.

### 1.1 The compatibility principle

A `.ps` program that uses only plain TypeScript features (no `parallel`, no integer types, no `pts:` imports) must print exactly what Node prints when run as TypeScript, except where this spec documents a difference (§8).

---

## 2. Toolchain and pipeline

### 2.1 Front end

- The front end is a fork of TypeScript 7 (typescript-go), pinned at `typescript/v7.0.2`. It provides the scanner, parser, binder, checker and language server.
- The compiler, `pts`, is written in Go inside the fork's module (it needs the fork's `internal/` packages). New code lives in `cmd/pts/` and `pts/`.
- Upstream modifications are minimal, marked `// PTS:` and listed in `docs/FORK.md`.
- The prelude types and the `pts:` modules are declared in ParallelScript's own lib declaration files, so that both the checker and the editor resolve them.

### 2.2 Pipeline

1. TypeScript AST plus checker.
2. **PTS semantic checks**: the rules of this spec that tsc doesn't know.
3. Lowering to a typed **ParallelScript IR**.
4. IR passes.
5. Emit **C11**.
6. Compile with **clang -O2** and link with the runtime into one native binary.

**The IR** is structured (not SSA) and fully typed. Boxing, closure environments, monomorphized generics, and async functions and generators (already turned into state machines) are all explicit in it. C is never emitted directly from the TS AST.

**Generated C** is readable and carries `#line` directives pointing at the `.ps` source, so gdb, lldb and crash backtraces show ParallelScript lines.

**The runtime** is written in C (`pts/runtime/`) and statically linked. Its dependencies (bdwgc, libuv, later PCRE2) are vendored as pinned submodules and built from source. `pts build` output depends only on libc.

`pts` finds its runtime sources relative to its own executable or through `PTS_HOME`. It builds the vendored libraries once into `~/.cache/pts/<version-hash>/` and compiles user programs against those cached static libraries (D-O11).

### 2.3 The CLI

| Command | Purpose |
|---|---|
| `pts build file.ps -o out` | Build a native binary |
| `pts run file.ps` | Build and run |
| `pts emit-c file.ps` | Print the generated C, for debugging |
| `pts test` | Test runner (later) |
| `pts lsp` | Language server (milestone 7) |

Cross-compiling with `pts build --cc zig` is a stretch goal.

---

## 3. Types and values

### 3.1 Variables

Variables have fixed types. `let x = 1; x = "s"` is a compile error (tsc already reports it).

### 3.2 Numbers

- `number` keeps JavaScript semantics exactly: IEEE-754 f64, including `NaN`, `Infinity` and `-0`.
- The added numeric types are `int` (meaning i64) and `i8 i16 i32 i64 u8 u16 u32 u64 f32 f64`, declared in a global prelude.
  - They start as aliases of `number` in the prelude, so the editor accepts them immediately.
  - The numeric rules below are enforced by the PTS semantic pass, and later surfaced in the language server.
- **Literals take their type from context.** `const x: u8 = 200` is fine; `const y = 5` is `number`, as in TS.
- **Implicit conversions are lossless only.**
  - Allowed implicitly: any integer type to `number`, and a smaller integer type to a larger integer type of compatible sign.
  - Anything lossy needs `as`.
  - Mixing `int` and `number` in arithmetic yields `number`.
- **`as` between numeric types converts the value**, following Rust's rules: float to int truncates toward zero and saturates at the target type's range, and `NaN` becomes 0.
- **Integer arithmetic.**
  - Overflow wraps (two's complement).
  - Integer `/` is truncating integer division.
  - Integer `%` takes the sign of the dividend, as in JS and Rust (D-O17).
  - Integer division or modulo by zero throws `RangeError`.
- `typeof` on any numeric type returns `"number"`.
- **Number-to-string conversion matches JS exactly**, using shortest round-trip formatting (Ryu-style) with the JS rules for choosing exponent or fixed notation. For example, `0.1 + 0.2` prints `0.30000000000000004`.

### 3.3 Booleans and strings

- Strings are immutable, stored as UTF-8, and each one records whether it is pure ASCII.
- Indexing keeps JavaScript's semantics so ported code behaves identically: `.length`, `s[i]`, `charCodeAt` and `slice` indices count UTF-16 code units.
  - ASCII strings take an O(1) fast path.
  - Non-ASCII strings may build an index lazily.

### 3.4 `any` and `unknown`

`any` is a **checked dynamic value**.
- Anything is assignable to it.
- Inside the dynamic world, operations follow JavaScript semantics. Property access, calls, indexing and operators are resolved at runtime using type descriptors:
  - a missing property reads as `undefined`;
  - calling a non-function throws `TypeError`;
  - `+` concatenates when either side is a string.
- The check happens at the boundary into typed code. Assigning an `any` to a typed location verifies the value at runtime and throws `TypeError` on mismatch: `const n: number = parsed.count` throws if `count` isn't a number.
- This keeps `JSON.parse(s).user?.name` working while making `any` honest.
- Every runtime type therefore carries a descriptor (name, field names mapped to offsets, methods). The same descriptors drive `console.log` and JSON.

`unknown` works exactly as in TypeScript: you must narrow before use.

### 3.5 Casts

`as` on non-numeric types:
- Upcasts are free.
- Downcasts and casts to unrelated types are checked at runtime and throw `TypeError`.
- `x!` is checked too, and throws on `null` or `undefined`.
- `as unknown as T` cannot be used to lie about a value's type.

### 3.6 Objects

- **Classes** have fixed layouts and use vtables. Supported: single inheritance, fields, methods, getters and setters, static members, `private` and `#private`, abstract classes and `instanceof`.
- **Interfaces and object types stay structural**; tsc checks them. At runtime, a value used through an interface type dispatches through a table of field offsets and method pointers, one per (concrete type, interface) pair. Tables are built at compile time where possible and cached at runtime otherwise.
- **Object literals** become instances of anonymous struct types derived from their checked type. Optional properties are stored as `T | undefined`.
- **Properties cannot be added or deleted after construction.** `delete` only works on index-signature types. `Record<string, T>` and `{ [k: string]: T }` are hash maps under the hood.
- **`null` and `undefined`** remain distinct values. Optional chaining and `??` are supported.

### 3.7 Unions and generics

- **Unions** are tagged values. Narrowing works with every form: `typeof`, `instanceof`, `in`, discriminant properties, equality and user-defined type guards. The checker does the reasoning; the backend emits tag tests.
- **Generics** are monomorphized.

### 3.8 Equality

- `===` compares objects by reference identity, and strings and numbers by value, with JS's `NaN` rules.
- `==` is supported with its JS meaning for the types involved; in practice it is mostly `x == null`.

### 3.9 Exceptions

- `throw`, `try`, `catch` and `finally` have JS semantics, and any value can be thrown.
- `Error` and its subclasses have `message`, `name`, `cause` and `stack`. `stack` shows `.ps` file and line numbers (it may arrive in a later milestone).
- **Uncaught errors** (D-O07):
  - An uncaught exception prints `Uncaught <Name>: <message>` (and, once available, a `.ps` stack trace) to stderr, then exits with code 1.
  - An unhandled rejection behaves like Node's default `--unhandled-rejections=throw`.
  - Until exceptions land in M3, runtime errors (such as integer division by zero) abort with the same message format and exit code 1.

### 3.10 Not supported

Each of these gets a clear compile error explaining why:
- `eval`, `new Function`, `with`;
- `var` (the error suggests `let`);
- `arguments`;
- prototype manipulation: `__proto__`, `Object.setPrototypeOf`, assigning to `.prototype`;
- `Proxy`, `Reflect`;
- declaration merging;
- namespaces, until there's a reason for them;
- `bigint` types and literals: "BigInt is not supported yet" (D-O08);
- `Symbol` other than `Symbol.iterator` and `Symbol.asyncIterator` (D-O14);
- regular-expression literals until M6: "RegExp arrives in M6" (D-O09).

Decorators and `WeakRef` can come later.

---

## 4. Memory and the shared heap

- Memory is garbage collected, starting with **bdwgc** (Boehm–Demers–Weiser) in thread-aware mode with thread-local allocation.
- Pointer-free data, such as string bytes and numeric arrays, is allocated with the atomic (no-scan) allocator.
- A precise collector, and later a generational one, is a later milestone.

### 4.1 The race-safety invariant (non-negotiable)

A program with data races may observe stale or surprising values, but it must never crash the runtime, corrupt the heap, or read or write out of bounds (Java's guarantee, not Go's). Concretely:

- **No torn values.** Any value in a heap slot that can be read concurrently is one machine word (a pointer, a small scalar or a box), or is written and read atomically. No multi-word values that can tear in shared fields.
- **No out-of-bounds writes.** Growable arrays keep their capacity inside the backing buffer, and bounds checks use the length and capacity of the buffer actually being accessed. Racing `push`es can lose elements but never write past the end.
- **Strings are immutable**, so sharing them is free.

---

## 5. Concurrency

### 5.1 The model

- The runtime starts one event loop per core. The count is configurable with `PTS_THREADS`.
- Each loop runs on its own OS thread, and all loops share one heap.
- Each loop is a libuv `uv_loop_t`, used for timers, sockets, filesystem, DNS and child processes. `uv_async_send` wakes a loop from another thread.

### 5.2 Plain `await`

Plain `await` keeps exact JavaScript semantics.
- A task belongs to one loop for its whole life, and a loop runs one task at a time.
- Code between two `await`s therefore runs without interference from other tasks on the same loop.
- For single-loop code, the ordering of microtasks, timers and I/O callbacks matches Node.

Async functions and generators compile to state machines. Locals that live across an `await` or `yield` are stored in a GC-managed frame object; there is no separate native stack per task.

### 5.3 Task parallelism: `parallel <expr>`

- `parallel e` evaluates `e` as a new task on another loop and immediately returns a `Promise<Awaited<T>>`, where `T` is the type of `e` (the promise is flattened if `e` is itself a promise).
- Captured variables are shared by reference, because the heap is shared.
- `await parallel f(x)` is the common idiom:
  ```ts
  const report = await parallel buildReport(user);
  const pages = await Promise.all(urls.map((u) => parallel crawl(u)));
  ```
- The target loop is picked by load (least-loaded, or round-robin with stealing of tasks that haven't started). A task never migrates once it has started.
- Promises can be awaited from any loop; settling one wakes the loop that is awaiting it.
- With `PTS_THREADS=1`, `parallel e` still creates a new task, on the same loop (D-O06).

**Parsing `parallel`** (D-O05):
- `parallel` is the operator only when the next token is on the same line and starts an expression **other than** `(`, `[`, `.`, `?.`, a template, `=`, an operator, `,`, `;`, `)` or `}`. Examples: `parallel foo(x)`, `parallel new X()`, `parallel async () => …`, `parallel await x`, `parallel "lit"`.
- `parallel for (` is the loop statement (§5.5).
- `parallel(expr)` parses as a normal call. The checker reinterprets it as the operator **only if** no binding named `parallel` is in scope and the call has exactly one argument.

### 5.4 Data parallelism

**The pool.** A work-stealing fork-join pool with Chase–Lev deques, one per worker, sized to the core count and separate from the event-loop threads. A thread that starts a parallel operation helps execute work until the operation finishes. The design follows Rayon (`join`, adaptive splitting).

**The `.par` view.** `.par` on arrays, typed arrays and ranges returns a parallel view with: `map`, `filter`, `forEach`, `reduce(fn, initial)`, `flatMap`, `some`, `every`, `find` (any match), `count`, `sum`, `min`, `max`, `sort`/`sortBy` (parallel merge sort) and `toArray`.
- These methods are synchronous: they return when the work is done.
- `.par.map` preserves order.
- `reduce`'s function must be associative.
- With `PTS_THREADS=1`, `.par` runs sequentially on the caller (D-O06).

**Helpers** (from `pts:parallel` or the prelude):
- `range(start, end)`, so `range(0, n).par.forEach(...)` works.
- `join(a, b)` runs two closures, potentially in parallel, and returns both results; it is for recursive divide-and-conquer.

### 5.5 `parallel for`

- `parallel for (const x of iterable) { ... }` is sugar for a parallel `forEach`. The statement finishes when every iteration has.
- Inside the body, `continue` is allowed. `break`, `return`, `yield`, `await` and labelled jumps out of the loop are compile errors.
- If an iteration throws, the remaining iterations are cancelled as far as possible and the first error is rethrown after joining.

```ts
const hashes = files.par.map((f) => sha256(f));
const total = orders.par.map((o) => o.amount).sum();

parallel for (const img of images) {
  img.resize(800, 600);
}
```

### 5.6 The single guardrail

- A *parallel body* is any of: the operand of `parallel`; a `parallel for` body; a function passed to a `.par` method or to `join`.
- Inside a parallel body, assigning to a captured local variable (a `let` declared outside the body) is a compile error. The message suggests `.par.reduce`, `.par.sum` or an `Atomic`.
- The stdlib declarations mark parallel-callback parameters with a branded function type, so the check knows which closures run in parallel.
- Mutating object fields from parallel code is allowed.
- **No other parallelism checks** are added without asking the owner.

```ts
let count = 0;
files.par.forEach(() => { count++; }); // compile error: use .par.reduce() or an Atomic
```

### 5.7 `pts:sync`

- `Mutex<T>`: a blocking `withLock(fn)` for short critical sections, plus an async `lock()` that doesn't block the loop.
- The `AtomicInt` / `Atomic<int>` family: `load`, `store`, `add`, `compareExchange`.
- `Channel<T>`: bounded or unbounded, async `send` and `recv`, `close`, and iterable with `for await`.
- `Once`.

A concurrent map may come later if benchmarks or examples want one.

---

## 6. Modules

- ES modules with relative imports.
- Standard library modules use the `pts:` prefix: `import { Mutex } from "pts:sync"`.
- No npm or `node_modules` in v1. Compiling pure-TS npm packages that fit the supported subset is a stretch goal.

---

## 7. Standard library

### 7.1 Globals

- `console`. Output matches Node's `util.inspect` for primitives, arrays, plain objects, class instances (like `Point { x: 1, y: 2 }`), `Map` and `Set` (defaults per D-O16: depth 2, `breakLength` 80, Node's grouping heuristics).
- `Math` and `JSON`.
- `Promise`, including `all`, `allSettled`, `race`, `any` and `withResolvers`.
- `setTimeout`, `setInterval`, `clearTimeout` and `queueMicrotask`.
- `performance.now()`.
- `fetch` (with `pts:http`): `http://` over HTTP/1.1; `https://` rejects with a `TypeError` until TLS is added (D-O10).

### 7.2 Built-ins

- The commonly used parts of `String`, `Array`, `Map`, `Set` and `Number`.
- `Object.keys`, `Object.values` and `Object.entries`.
- `Date` (basic).
- `RegExp`, backed by PCRE2, in M6 (D-O09). Until then, string-only overloads of `split`/`replace`/`replaceAll` work.

### 7.3 `pts:` modules

| Module | Contents |
|---|---|
| `pts:fs` | Sync and promise-based file APIs |
| `pts:path` | Path utilities |
| `pts:process` | `argv`, `env`, `exit`, `cwd` |
| `pts:http` | A server with an option to accept connections on every loop, plus `fetch` as a global |
| `pts:sync` | Synchronization primitives (§5.7) |
| `pts:parallel` | `range`, `join` and related helpers |

---

## 8. Documented differences from TypeScript and Node

The compatibility principle (§1.1) holds except for these:

- Properties cannot be added to or deleted from objects after construction (§3.6).
- `any` is checked at the boundary into typed code (§3.4); casts are checked at runtime (§3.5).
- The features in §3.10 are compile errors.

---

## 9. Implementation notes

(Filled in as features land: representation choices, runtime layouts, and anything a later session needs to know that the sections above don't say.)
