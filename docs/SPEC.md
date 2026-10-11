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

Representation choices, runtime layouts, and anything a later session needs to know that the sections above don't say.

### 9.1 Runtime layout (M0.4)

- `pts/runtime/include/pts.h` is the only header generated C includes. Sources are `pts/runtime/src/*.c`; C unit tests are `pts/runtime/tests/*_test.c` (each its own program; `go test ./pts/toolchain/` builds and runs them under ASan and UBSan).
- `pts/toolchain` builds `libptsrt.a`, `libuv.a` and `libgc.a` into `~/.cache/pts/` and links programs against them with `clang -std=c11` plus `-lpthread -ldl -lm` (D-003). Output binaries depend only on libc. A fresh clone needs `git submodule update --init pts/third_party`.
- Static types compile to native C types: `number` → `double`, `boolean` → `bool`, `string` → `pts_string *`.
- **Heap objects** start with `const pts_type *type`, a descriptor with the kind and name. Later milestones extend `pts_type` with fields and methods (D-L09).
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

- **Supported subset (M0.5):** `console.log(...)` with any number of arguments; `number`, `string`, `boolean`, `null` and `undefined` literals (numeric literals in every TS syntax); `let`/`const` with an initializer and a primitive type; reading locals; parentheses; nested blocks; type-only declarations (`interface`, `type`, `export {}`), which emit nothing.
- **Added in M1.3a:** `+` with a string (the other side through `ir.ToString`), template literals, `.length` on strings (the builtin `String.length`), `=== !== < <= > >=` on strings, and `!` on booleans.
- **Added in M1.2:** the numeric types on `let`/`const` annotations, numeric `as`, and every operator above on them (`ir.IntConst`, `ir.Convert`).
- **Added in M1.1:** `+ - * / % **`, unary `-` and `+`, and `< <= > >=` on numbers; `=== !== == !=` on two numbers or two booleans (`ir.Binary`, `ir.Unary`); `NaN` and `Infinity`.
- **IR types** are singletons compared by identity: `ir.Number` (C `double`), `ir.Bool` (`bool`), `ir.String` (`pts_string *`), `ir.Undefined` and `ir.Null` (a `pts_value` holding the constant, so boxing them is free), `ir.Value` (boxed `pts_value`), `ir.Void`. Boxing is an explicit `ir.Box` node.
- **Statements** carry an `ir.Pos` (absolute file, 1-based line); `cgen` turns positions into `#line` directives.
- **Builtins:** a call lowers to `ir.CallBuiltin` only if the callee resolves to a member declared in a PTS lib file and listed in `lower.preludeBuiltins` (keyed `"Interface.member"`, e.g. `"Console.log"`). A user binding that shadows `console` is therefore never mistaken for the builtin.
- **Top-level code** is the IR function `main` (C `pts_fn_main`); top-level `let`/`const` are its C locals. M1.4b turns bindings that functions capture into globals.
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
- Not yet covered (arrive with the features): assignments, compound assignment and `++`/`--` (M1.4a must apply the conversion rules there), function parameters and returns (M1.4a), bitwise operators on integers (M1.4a), `typeof` (M1.4a), integer types inside unions, arrays and generics (M2, M3).

### 9.5 Strings (M1.3a)

- Representation (D-014): UTF-8 bytes with lone surrogates as 3-byte WTF-8 sequences; `pts_string_concat` joins surrogate halves at the seam; `pts_string_write_for_output` (used by `console.log`) writes lone surrogates as U+FFFD.
- `ir.ToString` is JS's ToString for primitives: `pts_number_to_string` for `number`, `f32` and the narrow integers; `pts_string_from_i64`/`pts_string_from_u64` for 64-bit integers; interned constants `"true"`, `"false"`, `"null"` and `"undefined"` (cgen's `toStringWords`).
- Concatenation is `ir.Binary{Op: Concat}` → `pts_string_concat`. Template literals lower to a chain of concatenations, leaving out empty parts.
- Equality is `pts_string_equals` (byte equality, which the canonical representation makes exact); ordering is `pts_string_compare` (UTF-16 code units, memcmp for two ASCII strings).
- `.length` is `utf16_len`, computed when the string is made, so O(1).
