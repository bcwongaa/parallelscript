# ParallelScript language specification

ParallelScript is TypeScript compiled ahead of time to native binaries, with a garbage collector and real parallelism. This document is the language reference. It starts from the owner's brief (`docs/BRIEF.md` §2) and grows as features are implemented. **If a behaviour isn't in this spec, it isn't decided.**

Order of authority: `docs/BRIEF.md` (with the owner overrides in `CLAUDE.md`) → this spec → `docs/DECISIONS.md`. Decision ids such as `D-L03` or `D-O05` point into `docs/DECISIONS.md`.

---

## 1. Syntax and files

- Source files use the extension **`.pts`** (D-O01, the brief's original extension, confirmed by the owner after a brief switch to `.ps`). Declaration files stay `.d.ts`.
- `import "./x"` tries `x.pts` first, then the usual TypeScript extensions.
- The syntax is TypeScript plus one contextual keyword, `parallel` (§5).
- `parallel` is a keyword only in two positions; everywhere else it is an ordinary identifier, so existing code that uses it as a name keeps working:
  - `parallel <expression>` in expression position;
  - `parallel for (...)` in statement position.
- Disambiguation rules for `parallel` are in §5.3.

### 1.1 The compatibility principle

A `.pts` program that uses only plain TypeScript features (no `parallel`, no integer types, no `pts:` imports) must print exactly what Node prints when run as TypeScript, except where this spec documents a difference (§8).

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

**Generated C** is readable and carries `#line` directives pointing at the `.pts` source, so gdb, lldb and crash backtraces show ParallelScript lines.

**The runtime** is written in C (`pts/runtime/`) and statically linked. Its dependencies (bdwgc, libuv, later PCRE2) are vendored as pinned submodules and built from source. `pts build` output depends only on libc.

`pts` finds its runtime sources relative to its own executable or through `PTS_HOME`. It builds the vendored libraries once into `~/.cache/pts/<version-hash>/` and compiles user programs against those cached static libraries (D-O11).

### 2.3 The CLI

| Command | Purpose |
|---|---|
| `pts build file.pts -o out` | Build a native binary |
| `pts run file.pts` | Build and run |
| `pts emit-c file.pts` | Print the generated C, for debugging |
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
- `Error` and its subclasses have `message`, `name`, `cause` and `stack`. `stack` shows `.pts` file and line numbers (it may arrive in a later milestone).
- **Uncaught errors** (D-O07):
  - An uncaught exception prints `Uncaught <Name>: <message>` (and, once available, a `.pts` stack trace) to stderr, then exits with code 1.
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

Semantics (D-022), as in Node:
- Every `.pts` (or `.ts`) file reachable from the entry by imports is part of the program, and its top-level code runs once. Modules run in ES module evaluation order: a module after the modules it imports, in the order its `import` and `export … from` declarations name them. In an import cycle, a module that is already running is skipped, so the module that imported it runs first; that module can call the other's functions, but reading its `let`/`const` bindings before their declarations have run throws `ReferenceError: Cannot access 'x' before initialization`.
- Every import runs its module, even one that names only types; `import type` and `export type … from` don't.
- Imports are live bindings: a module sees later assignments to a `let` it imports, also through `import * as m` (`m.x`) and re-exports (`export { x as y }`, `export *`, `export * as ns`).
- `export default <expression>` stores the expression's value when the statement runs; a later assignment to a variable it named doesn't change the default export (unlike `export { x as default }`).
- Not supported yet: a module namespace object as a value (`console.log(m)`), dynamic `import()`, `import defer`.

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
- Reading a string index out of range (`s[i]`) gives `undefined` where the context can hold any value (printing, concatenation, template literals, `===`/`!==` with a string), but throws `RangeError` where a `string` is required, e.g. `const c: string = s[9]` (D-015).

---

## 9. Implementation notes

Representation choices, runtime layouts, and anything a later session needs to know that the sections above don't say.

### 9.1 Runtime layout (M0.4)

- `pts/runtime/include/pts.h` is the only header generated C includes. Sources are `pts/runtime/src/*.c`; C unit tests are `pts/runtime/tests/*_test.c` (each its own program; `go test ./pts/toolchain/` builds and runs them under ASan and UBSan).
- `pts/toolchain` builds `libptsrt.a`, `libuv.a` and `libgc.a` into `~/.cache/pts/` and links programs against them with `clang -std=c11` plus `-lpthread -ldl -lm` (D-003). Output binaries depend only on libc. A fresh clone needs `git submodule update --init pts/third_party`.
- Static types compile to native C types: `number` → `double`, `boolean` → `bool`, `string` → `pts_string *`.
- **Heap objects** start with `const pts_type *type`, their type's descriptor: kind, name, GC layout, size, fields and methods (§9.10, D-024).
- **Strings** (`pts_string`): the header, `flags` (bit 0 = pure ASCII), `byte_len`, `utf16_len` (the JS `.length`), then NUL-terminated UTF-8 bytes. They are allocated with `GC_MALLOC_ATOMIC` (no pointers inside). Literals are static objects declared with `PTS_STATIC_STRING`. Lengths are `uint32_t`, and strings over 2^30 bytes throw `RangeError: Invalid string length`.
- **Dynamic values** (`pts_value`): one 64-bit word (D-002). Raw object pointer; double + 2^49; or the constants `null` 0x02, `false` 0x06, `true` 0x07, `undefined` 0x0a.
- **Number formatting** (`pts_number_format`): the shortest round-trip digits via correctly rounded `%.*e` at increasing precision, then ECMAScript's Number::toString notation rules. It is checked against `internal/jsnum` on 25,000 values. M1.1 replaces the digit search with a Ryu-style algorithm.
- **`console.log`** (`pts_console_log(argc, argv)`) takes boxed values. It prints strings raw, `-0` as `-0` and other primitives as `String(x)`, separated by spaces. Format specifiers (`%s`, `%d`, …) are not interpreted yet (M3.6).
- **Errors:** `pts_panic(name, fmt, …)` flushes stdout, prints `Uncaught <name>: <message>` to stderr and exits with 1 (D-O07). It stands in for `throw` until M3.2.

### 9.2 Compiler pipeline (M0.5)

| Stage | Package | Notes |
|---|---|---|
| Declarations | `pts/lib` | `*.d.ts` embedded in the binary, served at `ptslib:///` by `lib.WrapFS` (D-005). `prelude.d.ts` is a root file of every program. |
| Semantic checks | `pts/check` | `Program(ctx, program)` → `*check.Info` (numeric types of expressions, variable names and binary operands) plus PTS diagnostics (D-012). |
| Load and type-check | `pts/frontend` | `Load(ctx, fs, entry)` → `*Program{TS, Entry}` plus tsc errors (syntax errors hide semantic ones). Options are fixed (D-005). |
| Diagnostics | `pts/diagnostic` | One format for tsc and PTS errors; codes `PTS1001` (not supported yet), `PTS1002` (unsupported by design) (D-006). |
| Lowering | `pts/lower` | `Program(ctx, program)` → `*ir.Program` plus diagnostics. Every construct it doesn't handle is reported as `PTS1001`, so the supported subset is exactly what it lowers. |
| IR | `pts/ir` | Data types and a text printer (`Program.String()`) used by lowering tests. |
| C emitter | `pts/cgen` | `Emit(program)` → one C translation unit (D-007). |
| Driver | `pts/driver` | `EmitC(ctx, entry)` and `Build(ctx, entry, output)`: the pipeline end to end; compile errors come back as `*diagnostic.Error`. |
| CLI | `cmd/pts` | `build`, `run`, `emit-c`, `help`; exit codes in D-006. |

- **Added in M2.2a:** class declarations at module scope, `new`, `this`, fields, constructors and methods (§9.11).
- **Added in M1.4b2:** `import` and `export` in every static form between modules (§6, §9.9).
- **Supported subset (M0.5):** `console.log(...)` with any number of arguments; `number`, `string`, `boolean`, `null` and `undefined` literals (numeric literals in every TS syntax); `let`/`const` with an initializer and a primitive type; reading locals; parentheses; nested blocks; type-only declarations (`interface`, `type`, `export {}`), which emit nothing.
- **Added in M1.4b1:** functions use and assign module-scope `let`/`const` (D-021).
- **Added in M1.4a2:** function declarations (also nested, if they use nothing from outside), parameters, `return`, calls and recursion (§9.8).
- **Added in M1.4a1:** `if`, `while`, `do`, `for`, `switch`, labelled statements, `break`/`continue` (with labels), `let` without an initializer; `=` and every compound assignment to locals, `++`/`--`, `?:`, `&&`/`||` (and `&&=`/`||=`) on operands of one type, comma, `typeof`, `void`, the bitwise operators, and `!` on any primitive (§9.7).
- **Added in M1.3b:** `s[i]` on strings (three builtins by context, D-015) and calls of the String methods listed in §9.6, as `ir.CallBuiltin` with the receiver first; the arguments convert to the builtin's `Params`, and left-out ones get the values in `lower.leftOut`. A builtin method can only be called directly (`s.trim` as a value or `s?.trim()` is `PTS1001`).
- **Added in M1.3a:** `+` with a string (the other side through `ir.ToString`), template literals, `.length` on strings (the builtin `String.length`), `=== !== < <= > >=` on strings, and `!` on booleans.
- **Added in M1.2:** the numeric types on `let`/`const` annotations, numeric `as`, and every operator above on them (`ir.IntConst`, `ir.Convert`).
- **Added in M1.1:** `+ - * / % **`, unary `-` and `+`, and `< <= > >=` on numbers; `=== !== == !=` on two numbers or two booleans (`ir.Binary`, `ir.Unary`); `NaN` and `Infinity`.
- **IR types** are singletons compared by identity: `ir.Number` (C `double`), `ir.Bool` (`bool`), `ir.String` (`pts_string *`), `ir.Undefined` and `ir.Null` (a `pts_value` holding the constant, so boxing them is free), `ir.Value` (boxed `pts_value`), `ir.Void`. Boxing is an explicit `ir.Box` node.
- **Statements** carry an `ir.Pos` (absolute file, 1-based line); `cgen` turns positions into `#line` directives.
- **Builtins:** a call lowers to `ir.CallBuiltin` only if the callee resolves to a member declared in a PTS lib file and listed in `lower.preludeBuiltins` (keyed `"Interface.member"`, e.g. `"Console.log"`). A user binding that shadows `console` is therefore never mistaken for the builtin.
- **Top-level code** of each module is an IR function `main` of that module (C `pts_main` for the entry module, `pts_module_<module>` for others, D-020, D-022); top-level `let`/`const` are its C locals, except those another function or module uses, which are C globals `pts_global_<name>` with a ready flag (D-021).
- **String literals** reach the IR exactly as the TS scanner decodes them: UTF-8, except that a lone surrogate is its 3-byte encoding (WTF-8, `stringutil.EncodeJSStringRune`). `cgen` computes `utf16_len` and the ASCII flag at compile time.
- **Adding a construct:** handle it in `pts/lower` (with a lowering test on the printed IR), add the IR node and its printer case, and the `cgen` case (with a compile-and-run test). Run `go test ./pts/... ./cmd/pts/`.

### 9.3 Numbers (M1.1)

- `number` is C `double`. `+ - * /` and the comparisons are the C operators (fully parenthesised in generated C); `%` is `pts_number_rem` (C `fmod`) and `**` is `pts_number_pow` (C `pow` plus JS's NaN cases), both inline in `pts.h` (D-011).
- Everything is compiled with `-ffp-contract=off` (in `toolchain.Profile.flags`), so no expression is fused into an FMA. x86-64 evaluates doubles in SSE2 registers (`FLT_EVAL_METHOD` 0), so there is no excess precision either.
- Constants: `cgen` writes the shortest round-trip decimal with a `.0` or exponent (`1.0`, `1e+21`); `-0` is `(-0.0)`; `NaN` and `Infinity` are `__builtin_nan("")` and `__builtin_inf()`.
- Number::toString (`pts_number_format`) takes the shortest digits from Ryu (`pts/third_party/ryu`, `libryu.a`) and applies ECMAScript's notation rules itself.
- Tests: `pts/runtime/tests/number_test.c` (formatting, `%` and `**` edge cases, and the 25,000-value comparison with `jsnum`), `pts/tests/run/{arithmetic,comparisons,special_numbers}.pts`, and `TestGeneratedNumberCorpusMatchesNode` (`pts/tests/numbers_test.go`): 4,000 literals and every operator on 16 special operands and 1,200 random pairs, compared line by line with Node.

### 9.4 Integer types (M1.2)

- `pts/check` computes a `check.Numeric` for every number-typed expression and variable name, and for each binary operator on numbers the type its operands convert to (`Info.OperandType`). The lowering inserts `ir.Convert` wherever an expression's type differs from what its context needs.
- Typing rules (D-012): annotations name the type; an unannotated variable takes its initializer's type; literals are untyped constants that take the expected type; operands unify to the wider compatible type, or to `number` when an integer meets a float.
- C representation: the `<stdint.h>` types and `float`. Constants are `INT64_C(...)`, `UINT64_C(...)`, `INT64_MIN` or a cast decimal. Arithmetic and negation call `pts_<type>_<op>` from `pts.h` (wrapping, through `uint64_t`); comparisons are C operators; float → integer is `pts_number_to_<type>` (Rust: truncate, saturate, NaN → 0); other conversions are C casts, which wrap between integer types (clang converts to signed types modulo 2^n).
- Boxing: `i8 … u32` and `f32` box as numbers (exact as doubles); `int`/`i64` and `u64` box through `pts_value_from_i64`/`pts_value_from_u64`, which allocate a `pts_boxed_int64`/`pts_boxed_uint64` (kinds `PTS_KIND_INT64`/`PTS_KIND_UINT64`) only beyond ±2^53. `console.log` prints them exactly.
- Covered since M1.4a1: assignments (the value converts to the local's type), compound assignments (typed as `x = x op value`), `++`/`--`, `?:`, `&&`/`||` and `switch` cases (both sides take one type, as a binary operator's operands do), the bitwise operators (D-018), `typeof` (always `"number"`).
- Covered since M1.4a2: parameters (from their annotations), call arguments (converted to the parameter's type), returns (converted to the result type) and result types (annotated, or the common type of what the function returns, D-020).
- Not yet covered (arrive with the features): integer types inside unions, arrays and generics (M2, M3).

### 9.5 Strings (M1.3a)

- Representation (D-014): UTF-8 bytes with lone surrogates as 3-byte WTF-8 sequences; `pts_string_concat` joins surrogate halves at the seam; `pts_string_write_for_output` (used by `console.log`) writes lone surrogates as U+FFFD.
- `ir.ToString` is JS's ToString for primitives: `pts_number_to_string` for `number`, `f32` and the narrow integers; `pts_string_from_i64`/`pts_string_from_u64` for 64-bit integers; interned constants `"true"`, `"false"`, `"null"` and `"undefined"` (cgen's `toStringWords`).
- Concatenation is `ir.Binary{Op: Concat}` → `pts_string_concat`. Template literals lower to a chain of concatenations, leaving out empty parts.
- Equality is `pts_string_equals` (byte equality, which the canonical representation makes exact); ordering is `pts_string_compare` (UTF-16 code units, memcmp for two ASCII strings).
- `.length` is `utf16_len`, computed when the string is made, so O(1).

### 9.6 String methods (M1.3b)

- **Indexing** (D-015): `s[i]` lowers to `String.element` (`pts_string_element`, a `pts_value` that is `undefined` out of range) where the context takes any value, `String.elementEquals` for `===`/`!==`/`==`/`!=` with a string, and `String.elementChecked` (throws `RangeError`) elsewhere. A valid index is an integer in range; `-0` is index 0. `ir.ToString` of a `Value` is `pts_value_to_string`.
- **Methods** (D-016): `charCodeAt`, `slice`, `substring`, `indexOf`, `includes`, `startsWith`, `endsWith`, `toUpperCase`, `toLowerCase`, `trim`, `trimStart`, `trimEnd` (and `trimLeft`/`trimRight`), `padStart`, `padEnd`, `repeat`. Each is a runtime function `pts_string_<method>` (`pts/runtime/src/string_methods.c`, `string_case.c`). Index arguments are doubles; the runtime applies ToIntegerOrInfinity, so a left-out index is `NaN` (means 0) or `Infinity` (means the length).
- **UTF-16 indices:** an ASCII string's units are its bytes (O(1)); a non-ASCII string is scanned from the start (`locate` in `string_methods.c`). One-character ASCII results are static strings, so `s[i]` on ASCII text doesn't allocate. Cutting a surrogate pair leaves lone surrogates (3-byte WTF-8); padding and repeating join halves at seams like `pts_string_concat`.
- **Searching:** `memmem` on the bytes, then counting units up to the match; a search string that starts with a lone low surrogate or ends with a lone high one is compared unit by unit, because it can match half of a pair.
- **Case mapping:** full Unicode mappings and Final_Sigma, from `pts/runtime/src/case_tables.h`, generated from Node's ICU by `node pts/runtime/gen/case_tables.mjs > pts/runtime/src/case_tables.h` (Unicode 17.0, ICU 78.3, Node 24.21). Regenerate when Node's Unicode version changes; the generated corpus test fails until then.
- **Whitespace** for `trim*` is JS's WhiteSpace and LineTerminator: U+0009–U+000D, U+0020, U+00A0, U+1680, U+2000–U+200A, U+2028, U+2029, U+202F, U+205F, U+3000, U+FEFF.
- **Errors** (V8's messages): `RangeError: Invalid count value: <count>` from `repeat`, `RangeError: Invalid string length` beyond 2^30 bytes.
- **Tests:** `pts/runtime/tests/string_methods_test.c`; `pts/tests/run/string_*.pts`; `TestGeneratedStringCorpusMatchesNode` (`pts/tests/strings_test.go`): 400 random cases of every method on strings with lone surrogates and special index arguments, the upper- and lowercase of every code point in planes 0 and 1, and Final_Sigma contexts, compared line by line with Node.

### 9.7 Statements and operators (M1.4a1)

- **IR:** statements `If`, `Loop` (while, do-while and for: `Cond`, `Update`, `PostTest`), `Switch` with `Case`s (`Match` nil for default), `LabeledBlock`, `Break` and `Continue`; every loop and switch has an `ir.Label`, so jumps name their target. Expressions `Assign` (`Postfix` for `x++`, whose value is the old one), `Truthy` (ToBoolean), `Conditional`, `Logical`, `Sequence`; binary `BitAnd BitOr BitXor Shl Shr UShr`, unary `BitNot`.
- **Lowering:** `for (init; cond; update)` is a block holding the initialiser and the loop; `switch` is a block that stores the value in a local `tag`, then the cases compare it with `===` (numeric operands converted as the semantic pass chose). Compound assignment is `x = x op value`; `x ||= v` is `x || (x = v)`. Conditions, `!`, `typeof` and `?:` conditions take any value, so `s[i]` there keeps JS's `undefined` (D-015). `typeof` of a static type is a constant (evaluating the operand if it may have effects).
- **C** (D-019): operands that could observe C's unspecified order go through `pts_tmpN` temporaries in a statement expression (`cgen/order.go` decides); `switch` is an if-chain of `goto`s to `<name>_case_N`/`<name>_default`; jumps that aren't to the innermost C loop are `goto <name>_break`/`<name>_continue`, where `<name>` is the source label or `loop`/`switch`/`block`, and labels appear only where jumped to.
- **Runtime:** `pts_number_truthy`, `pts_value_truthy`, `pts_value_typeof`; `pts_number_to_int32` and `pts_number_{bitand,bitor,bitxor,bitnot,shl,shr,ushr}` (JS int32 semantics); `pts_<int>_{shl,shr,ushr}` with the count modulo the width (D-018).
- **Tests:** `pts/tests/run/{control_flow,operators,evaluation_order,integer_operators}.pts`; the bitwise operators, `!` and `?:` joined the generated number corpus (every special and random operand pair, against Node).

### 9.8 Functions (M1.4a2)

- **IR:** `Func.Params` (locals), `Func.Result` (`Void` for none), `Call{Func, Args}`, `Return{Value}`. A nested function's IR name is qualified (`outer.inner`).
- **Lowering:** `declareFunctions` makes every function's IR header (parameters, result type) before any body is lowered; bodies are lowered where their declarations are, with their own jump targets. Each local records the function it belongs to, and a use from another function is `PTS1001` ("using `x` from outside the function"). A call of a function that returns nothing is `undefined` where a value is needed; `return f()` in a void function calls f, then returns.
- **C** (D-020): `static <result> pts_fn_<name>(<params>)`, prototypes first, so order and recursion don't matter; the top-level code is `pts_main`. Calls take part in the evaluation-order rule (D-019). A non-void function that doesn't end with `return` ends with `pts_unreachable()`.
- **Semantic pass** (`pts/check/functions.go`): parameter types, return statements and call arguments; a function's body is checked once, early if a call needs its inferred result type. `pts/check/unsupported.go` reports the features left out by design (SPEC §3.10, D-020).
- **Module-scope bindings** (M1.4b1, D-021): `lower.ownLocal` marks a module-scope local that another function uses as `Global`; cgen declares `static T pts_global_<name>;` and `static bool pts_global_<name>_ready;`, the declaration in `pts_main` assigns both, and uses from functions go through `pts_check_ready(…_ready, "name")` (ReferenceError before the declaration has run). Function bodies are lowered after the top-level code.
- **Tests:** `pts/tests/run/{functions,integer_functions,module_scope_bindings,module_scope_before_initialization}.pts`; `pts/tests/errors/{function_*,unsupported_by_design,with_statement}.pts`.

### 9.9 Modules (M1.4b2)

- **Frontend:** `frontend.Program.Modules` lists the program's source files in evaluation order, entry last (`evaluationOrder` in `pts/frontend/load.go`: depth first over `moduleRequests`, skipping modules already seen, declaration files, `import type` and `export type … from`).
- **IR:** `ir.Program.Modules` (same order), each an `ir.Module{Name, Init}`; `Name` is the file's base name without its extension, empty for the entry module. Every `ir.Func` points to its module; the printer qualifies names outside the entry module (`func lib:add`, `lib:main` for its top-level code).
- **Semantic pass:** checks every module, dependencies first. Names resolve through imports with `GetAliasedSymbol`; `bindingType` types a module-scope `let`/`const` or `export default` value on first use when the pass hasn't reached its declaration yet (`declared` keeps it from being checked twice).
- **Lowering:** first every module's functions (`declareFunctions`) and module-scope bindings (`declareModuleBindings`, including the `default` binding of `export default <expression>`), then each module's top-level code in order, then the function bodies. `lower.target` resolves a name with `GetImmediateAliasedSymbol` step by step, stopping at a `default` binding (`hasDefaultBinding`); `binding` lowers a name or a namespace member (`m.x`) to a `LocalRef`, a reported function value or a reported namespace value. Imports and `export` declarations produce no statements; `export default <expression>` is a `LocalDecl` of the `default` binding. A module-scope binding used by any other function, including another module's top-level code, becomes `Global` (`ownLocal`).
- **C:** `main` calls each module's function in order. Functions outside the entry module are `pts_fn_<module>_<name>` and their globals `pts_global_<module>_<name>` (`modulePrefix`; characters C doesn't allow in a module name become `_`); clashes get `_2`, … (`unique`, and `uniqueGlobal`, which also keeps a global clear of another's `_ready` flag). `readyCheck` leaves the check out in the declaring module's top-level code and in modules that run after it (`initOrder`).
- **Tests:** `pts/tests/run/modules*.pts`, each with its imported modules in the directory of the same name (the harness gives Node a copy with `.mts` files, `pts/tests/README.md`); `pts/tests/errors/module_*.pts`; unit tests `TestModulesAreOrderedAsTheyRun`, `TestNumericTypesHoldAcrossModulesAndAboveDeclarations`, `TestImportedBindingsAreTheExportingModulesOwn`, `TestOnlyCodeThatMayRunEarlyChecksThatAnImportedBindingIsReady`.

### 9.10 Objects and the collector (M2.1)

- **Descriptors** (`pts_type` in `pts.h`, D-024): `kind` (`PTS_KIND_OBJECT` for instances of classes and object types), `gc_descr` (must stay the second word: bdwgc's gcj allocator reads it there), `name`, `parent`, `size` (header included), `fields` (`pts_field`: name, offset, `pts_field_kind`; inherited first) and `methods` (`pts_method`: name, `void (*)(void)` to cast to the method's signature). Descriptors are `static const` data.
- **Layouts:** `PTS_GC_NO_POINTERS` (atomic), `PTS_GC_POINTER_WORDS(bits)` (bit i set: word i may hold a pointer; up to 62 words), `PTS_GC_ALL_WORDS(size)` (every word, for larger instances). String, object and `pts_value` fields are pointer words; numbers, booleans and integers are not.
- **Allocation:** `pts_object_new(type)` returns a zeroed instance with its header set: `GC_gcj_malloc` for layouts with pointers, `GC_MALLOC_ATOMIC` otherwise; both take thread-local free lists, and out of memory throws `RangeError`. A zeroed `pts_value` field is 0, which is no valid value: constructors must initialise every field.
- **bdwgc build:** threads, parallel marking, thread-local allocation and gcj support (`GC_GCJ_SUPPORT`, `GC_ENABLE_SUSPEND_THREAD`), per its CMake defaults (`pts/toolchain/libraries.go`). `pts.h` defines `GC_THREADS` before `gc.h`, so a `pthread_create` in code that includes it registers the thread with the collector.
- **Tests:** `pts/runtime/tests/object_test.c` (under ASan and UBSan): descriptors, zeroed instances, atomic allocation of pointer-free types, methods through the descriptor, a 20,000-node list surviving collections amid garbage, bounded heap growth for 64 MB of short-lived nodes, and four threads building lists while one collects.

### 9.11 Classes (M2.2a)

- **IR:** `ir.Program.Classes`; `ir.Class` (Name, Module, Fields, Constructor, Methods) is also the type of an instance reference. `ir.New{Class, Args}`, `ir.FieldRef{X, Field}`, `ir.SetField{X, Field, Value, Postfix}`, and `ir.Bind{Local, Value, In}` (a value computed once, used twice). Methods and the constructor are `ir.Func`s named `Class.method` / `Class.constructor` whose first parameter is `this`.
- **Lowering** (`pts/lower/classes.go`, D-026): `declareClasses` (all modules, before anything else, so types can name classes), then `declareClassMembers` with the functions, then bodies with the function bodies. The constructor's body is the field initialisers (`fieldInitializers`) followed by the declared constructor's statements. Fields and methods are looked up by declaration (`memberDeclaration`). Assignments go through a `place` (a local or a field); `readAndStore` binds a field's instance to a temporary unless it is a local.
- **C** (`pts/cgen/classes.go`): typedefs, then structs `pts_class_<Name>` (first member `pts_header`), after the globals; field tables `pts_fields_<Name>`, method tables `pts_methods_<Name>` and descriptors `pts_type_<Name>` after the prototypes. `new` is `({ args…; pts_class_C *t = pts_object_new(&pts_type_C); pts_fn_C_constructor(t, args…); t; })`. Field reads are `o->f`; a field read counts as reading state and `new` as a call for the evaluation-order rule (`order.go`). An instance boxes as its pointer; `typeof` is `"object"`; `===` compares pointers; an instance is truthy.
- **Semantic pass:** property declarations are typed like variables (annotation, or the initializer's type); `o.f` takes the field's numeric type (normalised to the declaration's symbol); methods and constructors are checked like functions, and `new` arguments like call arguments.
- **Tests:** `pts/tests/run/classes{,_modules,_integers}.pts`, `pts/tests/errors/class_{features_not_yet,field_conversions}.pts`, `TestClassesLowerToFieldsAConstructorAndMethods`.
- **`console.log` of instances** (M2.2b, D-027): `console.c` hands a `PTS_KIND_OBJECT` to `pts_inspect_write` (`pts/runtime/src/inspect.c`), a port of Node's `util.inspect` for instances over the descriptor's fields: `Point { x: 1, y: 2 }`, multi-line past 80 columns, `[Name]` beyond depth 2, `<ref *1>`/`[Circular *1]`, strings quoted as Node quotes them. Tests: `pts/tests/run/console_log_instances.pts` (against Node), `pts/runtime/tests/inspect_test.c` (under the sanitizers).

### 9.12 Inheritance (M2.2c)

- **IR** (D-028): `ir.Class.Parent`, `.Abstract`, `.Fields` (own only; `AllFields()` in layout order), `.Methods []*ir.Method` (`Name`, `Class`, `Func` with a nil `Body` if abstract, `Overrides`, `Virtual`; `Slot()` is the topmost method of its override chain). `ir.VirtualCall{Method, Args}` (the receiver is an instance of the slot's class), `ir.ClassCast{X, To}` (to an ancestor, or to a subclass the checker narrowed to), `ir.InstanceOf{X, Class}`. `ir.Program.Classes` lists superclasses first.
- **Lowering** (`pts/lower/classes.go`): `declareClasses`, then `declareSuperclasses` (a class extending a failed or foreign class fails), then members in superclass-first order (`orderedClasses`), then `linkOverrides`. `instanceMethodCall` emits a `VirtualCall` only if the method is abstract or `isOverriddenBelow` the receiver's static class. A derived constructor's body gets the field initialisers after its `super(...)` statement (`superCallStatement`); an implicit one forwards its parameters. `convert` handles class to class (`classCast`); `commonClass` meets related classes for `===`, `?:`, `&&` and `||`.
- **C** (`pts/cgen/classes.go`): `struct pts_class_D { pts_class_B pts_base; … }`; `pts_vtable_<C>` structs (`pts_descriptor` or `pts_base`, then one function pointer per slot the class introduces) for classes whose hierarchy has slots, whose descriptor `pts_type_<C>` is then of that type (`descriptor()` gives the address of the `pts_type` inside); slot implementations take `this` as the slot's class (`receiverClass`) and cast it to their own on entry (`pts_this`), and direct calls cast their receiver to match. `pts_instance_of(object, type)` (inline, `pts.h`) walks `parent`.
- **Semantic pass:** `constructorOf`/`superConstructor` find the nearest declared constructor up the `extends` chain; abstract methods are typed by their signature; an override without a result annotation takes the overridden method's numeric result type (`overriddenResultType`), and a redeclared field without one the inherited field's (`inheritedFieldType`).
- **Tests:** `pts/tests/run/inheritance{,_integers,_gc,_modules}.pts`, `pts/tests/errors/inheritance_not_yet.pts`, `TestOverriddenMethodsAreCalledThroughTheMethodTable`, `object_test.c` (`pts_instance_of`).

### 9.13 Accessors and parameter properties (M2.2d1)

- **IR** (D-030): `ir.Method.Kind` (`Plain`, `Getter`, `Setter`) and `.Abstract`; `Class.Method(name, kind)` and `OwnMethod`. Getter and setter functions are `<Class>.get_<name>` and `<Class>.set_<name>`.
- **Lowering:** `declareMethod` (methods and accessors), `declareAbstractField` (abstract getter and setter, kept in `abstractFields`), `implementAbstractAccessors` (generated accessors over a subclass's field), `parameterProperties` (fields first, stored first by `fieldInitializers`). `accessorProperty` finds a property's getter and setter through the symbol's declarations; `propertyRead` calls the getter; `propertyPlace` makes an assignment place whose `store` calls the setter, and `readAndStore`/`withObject` evaluate the instance and the getter once. `callMethod` is the one place that picks a direct or virtual call.
- **C:** slots `pts_get_<name>`/`pts_set_<name>` in the method tables (`slotName`); the descriptor's method table lists plain methods only.
- **Semantic pass:** accessors are function-like (`isFunctionWithBody`); `accessorType` types an accessor property; a parameter property's field takes its parameter's numeric type (`bindingType`); `inheritedMember` matches by kind, so an overriding getter without an annotation returns the overridden getter's type.
- **Tests:** `pts/tests/run/accessors{,_integers}.pts`, `pts/tests/errors/accessors_not_yet.pts`, `TestAccessorsAreCallsAndFieldsImplementAbstractFields`.
