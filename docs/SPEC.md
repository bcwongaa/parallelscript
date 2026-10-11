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
  - To the checker each is a number type of its own (D-048), so it can be an array element, a `Map` or `Set` key or value, a parameter or result of a function type, or a type argument (`u8[]`, `Map<string, int>`, `(n: int) => int`), and inferred types keep it: `ints.map(x => x * 2)` is an `int[]`. tsc relates every numeric type to every other, as it relates `number` to itself, so overloads match as they would for `number`: on a `u8[]`, `reduce(f, 0 as int)` takes `reduce`'s `T` overload (and fails the conversion rule); write `reduce<int>(f, 0)`.
- **Literals take their type from context.** `const x: u8 = 200` is fine; `const y = 5` is `number`, as in TS. So does an array literal's element or an argument (`bytes.push(7)` for `bytes: u8[]`); without a context a literal is a `number`, so `[byte, 1]` is a `number[]`.
- **Implicit conversions are lossless only.**
  - Allowed implicitly: any integer type to `number`, and a smaller integer type to a larger integer type of compatible sign.
  - Anything lossy needs `as`.
  - A function converts to a function type only if the type's parameters convert to its own and its result to the type's, losslessly: a `(b: u8) => void` can't be a `(n: number) => void`.
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
- Status (D-070, D-071): reads, writes, `in` and destructuring of properties and elements, the operators, `typeof`, equality and checked conversions to primitives, integer types, classes, interfaces, arrays, tuples, hash-map objects, maps, sets and unions of them work; a typed object that fits the target stays the same object, and dynamic data (values of `any`, `any[]`, hash-map objects of `any`) is copied into the target's representation. Writes of objects through `any` come with M3.3b2, calls through `any` and conversions to function types with M3.3c.
- Every runtime type therefore carries a descriptor (name, field names mapped to offsets, methods). The same descriptors drive `console.log` and JSON.

`unknown` works exactly as in TypeScript: you must narrow before use. Narrowing an `unknown` to a class or a primitive (by `instanceof`, `typeof` or a type guard) and `as` from it are checked at runtime and throw `TypeError` when wrong (D-068).

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
  - A generic function has one instance per distinct list of type arguments, inferred or written, as their runtime types (D-049): `identity(1)` and `identity(1.5)` share `identity<number>`, while `identity(byte)` with `byte: u8` is `identity<u8>`, whose values keep their type.
  - Arithmetic on a value of a type parameter is done in `number`, as tsc types it, even when the instance's type argument is an integer type; `x as T` converts back by the numeric rules.
  - Generic interfaces and type aliases are instantiated like any other type (`Box<int>`).
  - A generic class has one class per instance (D-050), printed with the generic class's name (`Box { value: 1 }`); instances may extend classes and be extended (`class IntBox extends Box<int>`).
  - Generic methods of classes, and generic functions nested in functions or methods, have instances too (D-051).
  - A function that would need ever more instances (`f<T>` calling `f<T[]>`) is a compile error after 64.
  - Not yet: generic functions as values and generic methods of object literals and interfaces (M3.3), static members of generic classes, `instanceof` of a generic class, overriding with or overriding a generic method, generic functions nested in generic code.

### 3.8 Equality

- `===` compares objects by reference identity, and strings and numbers by value, with JS's `NaN` rules.
- `==` is supported with its JS meaning for the types involved; in practice it is mostly `x == null`.

### 3.9 Exceptions

- `throw`, `try`, `catch` and `finally` have JS semantics, and any value can be thrown. The catch binding is an `unknown` (D-068).
- `Error` and its subclasses (`TypeError`, `RangeError`, `ReferenceError`, `SyntaxError`, `EvalError`, `URIError`) have `message`, `name`, `cause` and `stack`, and programs extend them. `stack` is `Name: message`, then up to 10 frames `    at name (file.pts:line)` where the error was made (D-069).
- Errors the runtime detects (integer division by zero, checked reads, failed `!` and casts, `ReferenceError` before a binding is ready) are thrown as these classes and can be caught. Running out of memory can't be.
- Unbounded recursion throws `RangeError: Maximum call stack size exceeded` (D-069).
- **Uncaught errors** (D-O07):
  - An uncaught exception prints `Uncaught ` and its stack (`<Name>: <message>` and its `.pts` frames) to stderr, then exits with code 1. Any other value prints as console.log shows it nested: `Uncaught 'text'`.
  - An unhandled rejection behaves like Node's default `--unhandled-rejections=throw`.

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
- Arrays have no holes (D-038). Reading `a[i]` out of range gives `undefined` where any value can go (printing, concatenation, template literals, conditions) and throws `RangeError` where an element is required; `a.pop()!` and `a.shift()!` on an empty array throw `RangeError`. Writing past the end (`a[a.length + 1] = x`) and growing an array by setting `length` throw `RangeError`.
- Function values (D-041): an arrow function or function expression that captures no variables is one static closure, so two evaluations of it give the same (`===`) function, where JS makes a new one each time; the same holds for a nested function declaration that captures nothing. A function used where its type takes more parameters, or other parameter types, may be wrapped in an adapter, which is a different function for `===` but prints as the one it wraps. Functions can't be converted to strings (JS gives their source text).
- Rest parameters (D-057): a function with fixed parameters used as a function type with a rest parameter (`const f: (...xs: number[]) => number = add`) reads each argument from the rest array with a checked read, so calling it with too few arguments throws `RangeError` where JS passes `undefined`.
- Optional properties (D-065): an optional property that a literal leaves out and that is assigned later prints after the literal's properties, in the order the object's type declares such properties, where JS prints properties in the order they were added. `{ a: 1, ...o }` keeps 1 when `o.a` is undefined, if tsc types the result's `a` without undefined (JS gives undefined). An object that lacks an optional property of a type it is used as can't gain it: writing it throws `TypeError: Cannot add property p, object is not extensible`; one that holds it without undefined (`{ verbose: true }` used as `{ verbose?: boolean }`) throws `TypeError` when undefined is written to it (D-066). A value whose objects may hold an optional property as yet another type (a subclass's field of another type) can't be used as the type (PTS1001).
- `any` (D-070, D-071): converting dynamic data (an `any[]`, a hash-map object of `any`) to a typed array or object type makes a copy, so writes to one don't show in the other, and a copy into an object type keeps only the properties the type declares; converting an `any` into a typed place checks it (`TypeError: Expected number, got string`), where TypeScript checks nothing; writing a property an object's type doesn't allow, or doesn't have, throws `TypeError`, where JS converts nothing or adds it. Objects have no `valueOf`: an object meets `+`, `<` and ToNumber as its string.
- Exceptions (D-068, D-069): a stack frame shows the file's path as the program was built with it and a line, without a column (`at f (/src/app.pts:12)`, where Node writes `at f (file:///src/app.ts:12:5)`); calls clang turns into jumps (tail recursion, inlining that leaves no trace) may be missing; the error of a stack overflow has no frames; recursion goes as deep as the thread's C stack allows (8 MB on Linux by default, less 256 KB), deeper than Node. An error's `name` is its own property, set by the constructor to its native class's name, where JS inherits it from the prototype (only `Object.keys` and JSON could tell). A class derived from Error can't redefine `stack`.
- Unions of objects (D-067): narrowing a union to a class with a type guard or `in` that is wrong at run time throws `TypeError: The value narrowed to C is not an instance of it`, where JS carries on and reads undefined. `==` between an object and a primitive compares the object's string (an array's join, its class's `toString()`, else "[object Object]"); objects have no `valueOf`.
- Object spread (D-058): `{ ...o }` copies the properties of o's static type, in the order its type lists them (a class instance's fields in their constructor's order, as JS does). An object made by a literal that wrote them in another order, or one with more properties than its static type, copies in that order and those properties only, where JS copies the object's own properties in its order.
- Methods as values (D-044): `o.m` used as a value is bound to `o`, as `o.m.bind(o)` would be, where JS gives the unbound method (whose `this` is undefined when it is called plainly, so a method that uses `this` fails); each read makes a new bound function, so `o.m === o.m` is false. `f.call(x, …)` and `f.bind(x, …)` on a class's method run the method `x`'s class has; on a method of an object type, `x` must be `o` itself.
- Enums (D-052): reading a numeric enum's reverse mapping `E[n]` for a value no member has gives `undefined` where any value can go, but throws `RangeError` where a `string` is required, as string indexing does (D-015). The enum object itself can't be used as a value yet.
- Maps (D-046): `m.get(k)!` throws `RangeError` when `m` has no entry for `k`, where JS gives `undefined`. A map's or set's key and value types are fixed; JS's own iteration order, SameValueZero keys and live iteration are kept.
- Unions (D-062): `x!` throws `TypeError` when x is null or undefined, where TypeScript's `!` checks nothing at run time. `x++` on a union variable is a statement's increment; as a value it is the union's old value.
- Hash-map objects (D-059: `Record<string, T>`, index signatures): reading a missing key where a value of the entry's type is required (`const n: number = o.k`, `o.k + 1`, `o.k += 1`) throws `RangeError: Object has no property 'k'`, where JS gives `undefined`; printing it, testing it, comparing it with `undefined` and `??`/`||` fallbacks behave as in JS. They inherit nothing: `"toString" in o` is false and `o.__proto__` is an ordinary key. A function stored under a computed key known only at run time (`{ [k]: () => 1 }`) is anonymous, where JS names it after the key.
- Array methods with callbacks (D-042): a callback that shortens the array makes `map`, `flatMap` and `filter` skip the indices it no longer has, so `map`'s result is shorter where JS's would have holes; `find(…)!` and `findLast(…)!` throw `RangeError` when no element matches, where JS gives `undefined`. `sort` with a comparator is a stable merge sort: the order is V8's for any consistent comparator, but the comparator may be called on other pairs, in another order.

---

## 9. Implementation notes

Representation choices, runtime layouts, and anything a later session needs to know that the sections above don't say.

### 9.1 Runtime layout (M0.4)

- `pts/runtime/include/pts.h` is the only header generated C includes. Sources are `pts/runtime/src/*.c`; C unit tests are `pts/runtime/tests/*_test.c` (each its own program; `go test ./pts/toolchain/` builds and runs them under ASan and UBSan).
- `pts/toolchain` builds `libptsrt.a`, `libuv.a`, `libgc.a`, `libryu.a` and `libbacktrace.a` into `~/.cache/pts/` and links programs against them with `clang -std=c11` plus `-lpthread -ldl -lm -static-libgcc` (D-003, D-069). Output binaries depend only on libc. A fresh clone needs `git submodule update --init pts/third_party`.
- Static types compile to native C types: `number` → `double`, `boolean` → `bool`, `string` → `pts_string *`.
- **Heap objects** start with `const pts_type *type`, their type's descriptor: kind, name, GC layout, size, fields and methods (§9.10, D-024).
- **Strings** (`pts_string`): the header, `flags` (bit 0 = pure ASCII), `byte_len`, `utf16_len` (the JS `.length`), then NUL-terminated UTF-8 bytes. They are allocated with `GC_MALLOC_ATOMIC` (no pointers inside). Literals are static objects declared with `PTS_STATIC_STRING`. Lengths are `uint32_t`, and strings over 2^30 bytes throw `RangeError: Invalid string length`.
- **Dynamic values** (`pts_value`): one 64-bit word (D-002). Raw object pointer; double + 2^49; or the constants `null` 0x02, `false` 0x06, `true` 0x07, `undefined` 0x0a.
- **Number formatting** (`pts_number_format`): the shortest round-trip digits via correctly rounded `%.*e` at increasing precision, then ECMAScript's Number::toString notation rules. It is checked against `internal/jsnum` on 25,000 values. M1.1 replaces the digit search with a Ryu-style algorithm.
- **`console.log`** (`pts_console_log(argc, argv)`) takes boxed values. It prints strings raw, `-0` as `-0` and other primitives as `String(x)`, separated by spaces. Format specifiers (`%s`, `%d`, …) are not interpreted yet (M3.6).
- **Errors:** `pts_throw(value)` throws to the innermost `try` (§9.45); `pts_panic(name, fmt, …)` flushes stdout, prints `Uncaught <name>: <message>` to stderr and exits with 1 (D-O07), for running out of memory and internal errors.

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
- Covered since M2.6a: integer types inside arrays, maps, sets, function types and object types, through the checker (§9.25).
- Not yet covered (arrive with the features): integer types inside unions (M3.1) and generics (M2.6b).

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

### 9.14 Statics and `#private` (M2.2d2)

- **Lowering** (D-031): `declareStaticMember` makes a static field's `ir.Local` (`<Class>.<field>`, `Global`, owned by the module's init) in `statics`, or a static method's function in `staticMethods` (its class in `staticClasses`). The class declaration statement lowers to `staticInitializers` (field `LocalDecl`s and static blocks, with `staticContext` set so that `this.f` works). `staticOwner` accepts `C`, `m.C` or `this` as the object; `staticField` and `staticCall` apply the two subclass restrictions (`usesThis` scans a static method's body). `isPrivateName` keeps `#` members out of overriding and redeclaration; `privateIn` lowers `#x in o`.
- **C:** `qualifiedIdentifier`, `memberIdentifier` and `fieldIdentifier` (`names.go`) spell static and private names; `visibleFields` leaves `#private` fields out of the descriptor's field table; the ready check of a static field names its class.
- **Semantic pass:** `inheritedMember` matches static with static.
- **Tests:** `pts/tests/run/statics_{and_private,modules,integers,before_class}.pts`, `pts/tests/errors/statics_not_yet.pts`, `TestStaticFieldsAreGlobalsInitialisedWhereTheClassIs`.

### 9.15 Object literals and exact object types (M2.3a)

- **IR** (D-033): `ir.Class.Shape` and `.Orders` (each distinct property order of a shape's objects; `Order(fields)` interns one); `ir.NewObject{Shape, Order, Values}`. A shape prints as `{x: number; y: number}`; `Program.Classes` lists the shapes after the classes.
- **Lowering** (`pts/lower/objects.go`): `isObjectType` decides which checker types are shapes, `shape` interns them (`shapesByType`, `shapesByKey`, `shapeList`), `propertyType` takes a property's numeric type from the semantic pass. `objectLiteral` lowers a literal (`shorthandValue` for `{ x }`); `isShapeProperty` and `shapeProperty` lower `o.p` and assignments to it.
- **C:** shapes are structs like classes (`pts_class_object<n>`); `shapeDescriptors` writes a descriptor per order, named "Object"; `newObject` allocates and stores the values in order. `inspect.c` prints a descriptor named "Object" without a name.
- **Semantic pass:** property signatures are typed like fields; `objectLiteral` types literal properties against `contextualPropertyType`.
- **Tests:** `pts/tests/run/object_literals{,_integers}.pts`, `pts/tests/errors/object_types_not_yet.pts`, `pts/tests/errors/object_literal_conversions.pts`, `TestObjectTypesWithTheSamePropertiesShareAShape`, `inspect_test.c` (plain objects).

### 9.16 Interfaces over fields (M2.3b1)

- **IR** (D-035): `ir.Interface{Fields}` (property fields have no `Class`); `ir.ClassCast.To` may be an interface; `FieldRef`/`SetField` on an interface-typed `X` read and write the property's field in whatever object `X` refers to.
- **Lowering:** `irType` maps object types to `interfaceOf` (interned in `interfacesByType`/`interfacesByKey`, fields from `objectFields`); `objectLiteral` builds the shape of `literalType`; `convert` sends object-to-object conversions to `objectCast`, which checks `unprovided` (a property the source has no same-typed field for); `commonClass` also meets a class and an interface it provides; `isObject` covers classes and interfaces wherever any object is accepted (truthiness, `typeof`, `===`, `instanceof`, `#x in`).
- **C:** an interface value is a `pts_object *`; `fieldRef` writes `(*({ static pts_site pts_siteN; (T *)pts_field_address(o, &pts_siteN, "name"); }))`. Runtime: `pts_site`, `pts_field_address` (inline, `pts.h`) and `pts_field_address_slow` (`object.c`).
- **Tests:** `pts/tests/run/interfaces.pts`, `pts/tests/errors/object_types_not_yet.pts`, `object_test.c` (cache per layout, four threads racing on one site).

### 9.17 Methods and accessors through interfaces (M2.3b2)

- **IR** (D-036): `ir.Interface.Methods` (`ir.InterfaceMethod{Name, Params, Result}`), `ir.InterfaceCall{Method, Args}`, `ir.Field.Accessor` (an interface property some class provides with accessors), `ir.Program.Interfaces`.
- **Lowering:** `objectMembers` makes an object type's fields and methods (`interfaceMethod`, `parameterType`); `providesProperty`/`providesMethod` decide conversions (`unprovided`); `interfaceMethodCall` lowers `o.m(args)`; `markAccessorProperties` sets `Field.Accessor`; `readAndStore` reads a property through an interface once; `asContextualInterface` meets unrelated classes in their contextual interface.
- **C:** `collectInterfaceMembers` picks each class's members that interfaces may call, `entryFunctions` writes their `pts_entry_*` functions, and the descriptor's `pts_methods_<C>` lists them with their kinds; `interfaceCall` writes a call through `pts_method_find`; `fieldRead`/`fieldStore` write `PTS_GET`/`PTS_SET` for accessor-provided properties. Runtime: `pts_method`, `pts_function`, `pts_method_find`, `pts_property_find`, `PTS_GET`, `PTS_SET` (`pts.h`), and their slow paths in `object.c`.
- **Semantic pass:** method and accessor signatures of object types are typed like abstract methods (`isSignature`); annotations of parameters and results are visited, so object types written there are typed; `#private` fields are typed like others.
- **Tests:** `pts/tests/run/interface_methods{,_integers}.pts`, `interface_getter_only.pts`, `pts/tests/errors/object_types_not_yet.pts`, `TestMethodsAndAccessorsProvideInterfaces`, `TestOnlyInterfacePropertiesThatAccessorsMayProvideCallThem`, `TestPrivateFieldsAndInterfaceMembersFollowTheConversionRules`, `object_test.c` (method and property sites).

### 9.18 Arrays (M2.4a)

- **IR** (D-038): `ir.Array{Elem}` (one per element type, `lower.arrayOf`), `ir.NewArray`, `ir.ArrayCall{Op, Args}` with the `ir.ArrayOp`s (length, elements checked or as dynamic values, stores, push, unshift, pop, shift, truncation).
- **Lowering** (`pts/lower/arrays.go`): `arrayType` from the checker's array types (`never[]` elements are dynamic values); `arrayLiteral` takes its contextual array type; `arrayElement` picks the checked or dynamic read (`valueExpression`); `place` has `index` and `arrayLength` for stores, read once by `readAndStore` and `withObject`; `arrayMethodCall`, `arrayIsArray` (static), `nonNull` (`pop()!`), `forOfStatement` (an index loop that reads the length each iteration).
- **C:** an array is a `pts_array *`; `pts/cgen/arrays.go` writes `pts_array_of`, `PTS_ARRAY_GET`/`PTS_ARRAY_GET_WORD` (pointer and dynamic elements, where a zero word is a hole), `PTS_ARRAY_SET`, `pts_array_push`/`unshift` of a compound literal, `PTS_ARRAY_POP`/`SHIFT`. Runtime: `pts.h` (arrays section), `array.c` (one descriptor per element kind, single-word element copies, acquire/release on the length and buffer), and `inspect.c` (`format_elements`, `group_array_elements`, `reduce_to_single_string`: Node's `formatArray`, `groupArrayElements` and `reduceToSingleString`; string widths count code points).
- **Semantic pass:** arrays of integer types and f32 were PTS1001 (`integerElements`) until M2.6a (§9.25).
- **Tests:** `pts/tests/run/arrays.pts`, `arrays_objects.pts`, `array_{index,hole,length,pop}_error.pts`, `pts/tests/errors/arrays_not_yet.pts`, `TestArrayElementsAndForOfLowerToArrayOperations`, `array_test.c` (growth, both ends, boxing, threads racing pushes against a reader).

### 9.19 Array methods without callbacks (M2.4b)

- **IR** (D-039): the `ir.ArrayOp`s `ArraySlice` … `ArrayFill` (`ArrayCall.To` is `flat`'s result type); builtins `ir.StringSplit` and `ir.ArrayFromString`, returning `ir.StringArray` (the lowering's one `string[]`).
- **Lowering** (`pts/lower/arrays.go`): `arrayMethods` (fixed parameters, defaults by `arrayParam`), `arrayValues` (push, unshift, concat), `splice`, `flat`, `arrayFrom`; `toString` of an array is `join(",")`; `convert` retypes an empty literal.
- **C:** `pts/cgen/arrays.go` (`elements` for compound literals, `concat`); `collectInterfaceMembers` also lists `toString()` methods (`isToString`). Runtime: `array.c` (the methods; `element_string` is JS's ToString of an element; `joined`; `merge_sort`; `flatten_into`), `pts_string_split` in `string_methods.c`.
- **Tests:** `pts/tests/run/array_methods.pts`, `pts/tests/errors/arrays_not_yet.pts`, `array_test.c` (join of a cycle, splice bounds, sort stability).

### 9.20 Closures and function values (M2.5a)

- **IR** (D-041): `ir.FuncType` (one per signature, made by `funcType`); `ir.Func` gains `Closure`, `Outer`, `Captures`, `Self`, `JSName` and `Adapts`; `ir.Local.Cell`; `ir.Loop.Bindings` (a for loop's `let` variables, copied per iteration when they are cells); expressions `ir.MakeClosure` and `ir.CallValue`, and `ir.Call.Closure` for nested function declarations. `ir.Walk`/`ir.WalkExpr` (moved from cgen) walk the IR.
- **Lowering** (`pts/lower/closures.go`): `functionType`/`signatureType` map checker types with one call signature; `functionExpression` lowers arrows and function expressions inline, shaped by `contextualFuncType` (extra parameters, a void result, an inferred integer result widened to `number`); `jsName` is JS's NamedEvaluation; `hoistedFunctions` makes nested declarations' closures at block entry (`block`, `switchStatement`); `capture` records captured locals through the `Outer` chain (`ownLocal`); `declareAhead` makes the local of a variable a closure uses above its declaration; `thisFunction`/`thisLocal` give arrows their method's `this`; `adapt` builds adapters; `markCells` (run at the end of `Program`) decides `Local.Cell`.
- **Semantic pass:** arrows and function expressions are functions with bodies (`isFunctionWithBody`); an expression body is checked as a return value (`returnValue`).
- **C** (`pts/cgen/closures.go`): per function made a value of, `pts_type_closure_<f>` and either `pts_closure_<f>` + `pts_new_closure_<f>` or `pts_static_<f>` (with a `pts_code_<f>` wrapper for functions that take no closure); closure functions take `pts_closure *pts_env` first and copy their captures into C locals in `closurePrologue`; cells are `T *` locals made in `scope` when their block is entered, read as `(*x)`; `perIterationCopies` adds `pts_cell_copy` to a for loop's update; `callValue` casts `code` to `functionPointer(ft)`. Runtime: `pts_closure`, `PTS_KIND_FUNCTION`, `pts_cell_new`/`pts_cell_copy` (`object.c`), `format_function` (`inspect.c`), `typeof` "function" (`string.c`).
- **Tests:** `pts/tests/run/closures.pts`, `function_values.pts`, `function_values_classes.pts`, `arrow_this.pts`, `closures_modules.pts`, `closures_integers.pts`, `closures_gc.pts`; `pts/tests/errors/closures_not_yet.pts`; lowering tests `TestOnlyCapturedVariablesThatMayChangeLiveInCells`, `TestArrowFunctionsCaptureTheThisOfTheirMethod`, `TestFunctionValuesWithFewerParametersOrAResultAreAdapted`; cgen test `TestCellsAreMadeOnBlockEntryAndCopiedForEachIteration`.

### 9.21 Array methods with callbacks (M2.4c)

- **IR** (D-042): no new nodes for the loops: each method is a generated helper function (`Array.map`, `Array.filter`, …), a typed loop that calls the callback through `ir.CallValue`; `ir.ArraySortWith` (`a.sort(compare)`) and the builtin `ir.ThrowError`.
- **Lowering** (`pts/lower/callbacks.go`): `callbackMethods`, `callbackMethodCall` (and `arrayFromWithMapper`) lower the callback as a value and call `callbackHelper`, which builds (once per `helperKey`: method and variant, array type, callback type) the helper with `helperBuilder` (`loopFrom`, `callWith`, `find`, `reduce`, `appendAll`, `truthy`); `sortWith` converts the comparator to `(T, T) => number`. `irType` maps unions of number literals to `number` and of string literals to `string`.
- **C:** helpers are ordinary functions (`pts_fn_Array_map`, `_2`, … per type); `collectComparators`/`comparatorFunctions` write `pts_compare_<n>`, which loads two elements and calls the comparator as their type. Runtime: `pts_array_sort_with` and `compare_sort` (`array.c`), `pts_throw_error` (`runtime.c`).
- **Tests:** `pts/tests/run/array_callbacks.pts`, `array_reduce_empty.pts`, `array_find_none.pts`; `errors/arrays_not_yet.pts`; `TestCallbackMethodsCallOneHelperPerArrayAndCallbackType`; `array_test.c` (`sort_with_a_comparator_is_stable`).

### 9.22 Methods in object literals and methods as values (M2.5b1)

- **IR** (D-044): `ir.Field.Method` marks a shape's field that holds a method's closure, typed `ir.FuncType` with the shape as its first parameter (`this`); `ir.Func.Binds` marks closures `bind` made of function values.
- **Lowering:** `shape` adds a method field per method (`objectMembers`' interface methods; shapes are interned by fields and methods); `objectMethod` (`pts/lower/methods.go`) lowers a literal's method to a closure function whose first parameter is `this` (`newThis`); `interfaceMethodCall` calls a shape's method through its field; `providesMethod` accepts shapes' method fields; `methodValue`, `methodOf` and `boundMethod` make bound closures of methods (static methods are plain functions); `isFunctionMethod`, `functionMethodCall` and `methodCallOrBind` lower `bind` and `call` (`sameReference` for object types); `signatureType` expands tuple rest parameters (`GetExpandedParameters`).
- **C:** `shapeMethodTable` writes `pts_entry_<shape>_<m>` (loads the closure from the object, calls it with the object) and `pts_methods_<shape>`, shared by the shape's descriptors; bound closures' descriptors have `.name = NULL` and `.parent = &pts_bound_function` (`inspect.c` prints "bound " and the wrapped function's name).
- **Tests:** `pts/tests/run/object_methods.pts`, `method_values_bound.pts`; `errors/object_types_not_yet.pts`, `class_features_not_yet.pts`; `TestObjectLiteralMethodsAreClosuresThatTakeTheObject`.

### 9.23 Accessors in object literals and `apply` (M2.5b2)

- **IR** (D-045): a shape's accessor property p has fields `get p` and/or `set p` (`ir.Field.Kind` Getter or Setter, `Property` p, `Method` set) that hold its getter's and setter's closures; `ir.Class.AccessorField` finds them.
- **Lowering:** `accessorFields`, `literalAccessors` and `accessorsKey` (`pts/lower/objects.go`) give a literal's accessors their fields; `literalType` keeps a literal with accessors at its own type; `objectLiteral` lowers getters and setters with `objectMethod`; `shapeProperty` reads and writes a shape's accessor property through the interface of its type; `providesProperty` and `markAccessorProperties` count shapes' accessors; `appliedArguments` turns `apply`'s array literal into `call`'s arguments.
- **C and runtime:** `shapeFieldEntries` writes one field-table entry per accessor property with kind `PTS_FIELD_GETTER`, `PTS_FIELD_SETTER` or `PTS_FIELD_GETTER_SETTER` (printed `[Getter]`, `[Setter]`, `[Getter/Setter]`; `pts_field_address_slow` and `pts_property_find_slow` skip them); `shapeMethodTable` adds `PTS_GETTER`/`PTS_SETTER` entries (`pts_entry_<shape>_getter_<p>`); `fieldIdentifier` names the closure fields `pts_get_<p>`/`pts_set_<p>`.
- **Tests:** `pts/tests/run/object_accessors.pts`; `errors/object_types_not_yet.pts`, `class_features_not_yet.pts`; `object_test.c` (`property_sites_skip_the_field_entries_of_accessor_properties`).

### 9.24 Map and Set (M2.5c)

- **IR** (D-046): `ir.MapType` (key, value, `Set`; one per type, `mapOf`), `ir.MapIterator` (a C struct in a local), `ir.MapCall` with the `ir.MapOp`s, `ir.NewMap`.
- **Lowering** (`pts/lower/maps.go`): `mapType`/`libCollection` map the ES library's Map, Set and their readonly views; `newMap`; `mapMethodCall` (`get`, `get(…)!` through `nonNull`, `set`/`add`, `has`, `delete`, `clear`, `forEach`), `mapProperty` (`size`); `mapIterableOf` (a set, `keys()`, `values()`), `mapLoop`, `mapForOf` (`for…of`), `arrayFromMap` (`Array.from`, with a mapper); `mapHelper` generates `forEach` and `Array.from` helpers like D-042's. Maps and sets of integer types arrived in M2.6a (§9.25).
- **C:** `pts/cgen/maps.go` (`collectMaps` and `mapDescriptorDefinitions` write `pts_type_map_<n>`/`pts_type_set_<n>` with key and value kinds in the new `pts_type.key` and `element`; `newMap`, `mapCall`). Runtime: `map.c` (`pts_map`, `pts_map_table`, `pts_map_iterator`; `replace`, `follow`), `pts_value_of_slot` (`array.c`), `format_map_entries` (`inspect.c`).
- **Tests:** `pts/tests/run/maps_sets.pts`, `maps_gc.pts`; `errors/maps_not_yet.pts`; `map_test.c`; `TestForOfOverASetIteratesItsLiveTable`.

### 9.25 Integer types as checker types (M2.6a)

- **Checker** (D-048, fork edits in `docs/FORK.md`): `internal/checker/pts.go` makes each prelude alias `int`, `i8 … u32`, `u64`, `f32` an intrinsic type with `number`'s flags (`ptsNumericAliasType`, called from `getDeclaredTypeOfTypeAlias`; `i64` is declared as `int` in `pts/lib/prelude.d.ts`), and types arithmetic and unary operators by `numeric.Operation` (`ptsArithmeticType`, `ptsUnaryType`). `nodebuilderimpl.go` prints the types by name.
- **Shared rules:** `pts/numeric` (the old `check.Numeric`, now `numeric.Type`, aliased as `check.Numeric`): `Unify`, `ImplicitlyConvertible`, `WithUntyped`, `Operation`, `IsUntyped`, `IntegerLiteralValue`.
- **Semantic pass:** `check.NumericOf` and `check.IsNumber` read checker types; `annotationType`, parameters, `for…of` variables and identifiers without a recorded type use them; `contextualExpr` gives number-typed expressions their contextual type and checks the conversion; `functionConversion` checks function values against their contextual function type; `call` visits the callee and takes a generic function's result from tsc's type at the call.
- **Lowering:** `irType` maps numeric checker types through `check.NumericOf`; `arrayValues` lowers `concat`'s array literal arguments with the receiver's type; `newMap` uses the contextual type for `new Map()`. The runtime already had element and key kinds for every numeric type.
- **Tests:** `pts/tests/run/arrays_integers.pts`, `maps_integers.pts`, `function_values_integers.pts`; `errors/integer_elements_lossy.pts`; `pts/check/numeric_types_test.go`.

### 9.26 Generic functions and object types (M2.6b1)

- **Checker exports** (`internal/checker/pts.go`): `PtsTypeArgumentsOfSignature` (a resolved signature's type arguments, from its mapper) and `PtsInstantiateType`.
- **Lowering** (`pts/lower/generics.go`, D-049): `declareFunctions` records each generic function declaration at module scope (`declareGeneric`; nested ones are PTS1001) instead of declaring it; a call (`genericCall`) takes the type arguments of tsc's resolved signature, instantiated for the instance being lowered, and `genericInstance` finds or makes the instance for their IR types: a function named `<name>_<types>` (`typeArgumentsName`), declared with `l.subst` set, whose body `instanceBody` lowers after the other bodies (`Program`'s loop), with the locals of the declaration's previous instance forgotten (`forgetLocals`) and its nested function declarations declared again. With `l.subst` set, `irType` and the query helpers `typeAt`, `typeOfSymbol`, `contextualType` and `returnTypeOf` instantiate every checker type, and `memberSymbol` resolves `x.name` against the instantiated object type. `report` drops repeated diagnostics. Generic functions as values stay PTS1001.
- **Object types:** `isObjectType` takes references to generic interfaces (`Box<string>`); `propertyType` and `interfaceMethod` read an instantiated member's type rather than its declaration's.
- **Semantic pass:** `contextualPropertyType` falls back to the instantiated property type, so literals in a `Box<int>` take `int`.
- **Tests:** `pts/tests/run/generics.pts` (with `run/generics/pairs.pts`), `generics_integers.pts`; `errors/generics_not_yet.pts`; `TestGenericFunctionsHaveAnInstancePerListOfTypeArguments`.

### 9.27 Generic classes (M2.6b2)

- **Lowering** (`pts/lower/generic_classes.go`, D-050): `declareClasses` records generic class declarations (`declareGenericClass`; static members are PTS1001); `irType` makes or finds an instance's class for a reference to one (`classInstanceOf`, keyed on the IR types of its type arguments), linking its superclass from the declaration's base type in the instance (`declareInstanceSuperclass`). Members are declared by `ensureMembers`, superclass first, into the instance's own `classMembers` (the tables of fields, methods and abstract fields that the other classes share in `l.members`); instances made before `declareClassMembers` are declared there, later ones at once. Lookups go through `membersAt`/`membersFor`, which find the instance the receiver's class (`receiverClass`; for `this`, its constraint's) is or extends. Constructors and methods are lowered per instance (`classInstanceBody`, `classBodiesOf`), with parameters bound again (`bindParameters`). A non-generic class may extend an instance (`declareSuperclass`); `new` and narrowing take the instance from the expression's type; `instanceof` of a generic class is PTS1001. `result.Classes` orders every class after its superclass (`orderedByParent`).
- **Tests:** `pts/tests/run/generic_classes.pts` (with `run/generic_classes/queue.pts`), `generic_classes_integers.pts`; `errors/generics_not_yet.pts`, `class_features_not_yet.pts`.

### 9.28 Generic methods and nested generic functions (M2.6b3)

- **Lowering** (`pts/lower/generics.go`, D-051): `genericFunc` also describes a generic method: its `class` (`this`), `static` class, and `outer` substitution (a generic class instance's), which an instance's substitution extends. `declareMethod` and `declareStaticMember` hand generic methods to `declareGenericMethod`, which records them in the class's `classMembers.genericMethods` (statics in `l.genericStatics`); `call` finds them with `genericMethodAt` and lowers `genericMethodCall` (a direct `ir.Call` with the receiver first) or `genericCall`; `calledInstance` takes the resolved signature's type arguments. `declareGeneric` takes nested generic functions unless `l.subst` is set. `declareFunctions` skips generic classes, whose instances declare their methods' nested functions in `classInstanceBody`.
- **Dispatch:** `callMethod` calls through the method table when a generic class extending the receiver's class redeclares the method (`mayBeOverriddenByAnInstance`), since the overriding instance may be made after the call is lowered (fix to M2.6b2).
- **Tests:** `pts/tests/run/generic_methods.pts`; `generic_classes.pts` (late overriding instances); `errors/generics_not_yet.pts`.

### 9.29 Enums (M2.6c)

- **Lowering** (`pts/lower/enums.go`, D-052): an enum declaration makes no code (`enumDeclaration` reports members whose values tsc can't compute); `enumMember` lowers `E.m` and `E["m"]` (also through imports and namespace imports) to the member's value from `GetConstantValue` on its declaration; enum types are numbers or strings through `irType` (`check.NumericOf` maps numeric enum literals to `number`); `enumReverseMapping` lowers `E[n]` to a `Bind` of `n` and a chain of `Conditional`s, last member first, ending in `undefined` (from `valueExpression`) or a `RangeError` thrown with `ThrowError` (from `expression`); `binding` reports the enum object as a value.
- **Numbers:** a numeric member is a `number` to the semantic pass, so it converts to an integer type only with `as`.
- **Tests:** `pts/tests/run/enums.pts` (with `run/enums/palette.pts`), `enum_reverse_mapping_error.pts`; `errors/enums_not_yet.pts`, `enum_member_to_integer.pts`.

### 9.30 Tuples (M2.6d1)

- **IR** (D-053): a tuple type is a shape (`ir.Class` with `Shape` and the new `Tuple`) whose fields are named `0`, `1`, …; one per list of element IR types (`l.tuples`). `Class.String` writes it as `[string, number]`.
- **Lowering** (`pts/lower/tuples.go`): `irType` maps fixed-length tuple types (`tupleType`; optional and rest elements are PTS1001); `arrayLiteral` makes a tuple (`tupleLiteral`, an `ir.NewObject`) where the literal's type or contextual type is a tuple; `t[i]` with a constant index reads and writes the field (`tupleElement`); `t.length` is a constant; `as` on an array or object literal (`as T`, `as const`) gives the literal the asserted type (`assertedLiteral`).
- **C and runtime:** tuple descriptors have the new kind `PTS_KIND_TUPLE` and the name `Array`; `inspect.c` prints them as arrays (`format_tuple_elements`, grouping with `is_numeric_tuple`); fields are `pts_element_<i>` (`fieldIdentifier`).
- **Tests:** `pts/tests/run/tuples.pts`, `tuples_integers.pts`; `errors/tuples_not_yet.pts`.

### 9.31 Destructuring (M2.6d2a)

- **Lowering** (`pts/lower/destructuring.go`, D-054): `destructure` holds the value in a local and binds each element of an object or array pattern (`bindName`, or recursively for nested patterns) to its part: `propertyPart` (a class's or shape's field, a class's getter, an interface's field), `elementPart` (a tuple's field, the default past a tuple's end; an array's checked element, or the default when the array is too short; a rest element as `slice`, or for a tuple as a new array or tuple, `tupleRest`). Declarations (`destructuringDeclaration`; module-scope names are declared ahead by `declarePatternBindings`), parameters (`parameter` keeps the argument in a local and `inFunction` starts the body with `parameterPrologue`), `for…of` over arrays, and assignments (`destructuringAssignment`, `assignParts`, `assignPart`, `assignShorthand`; the value of the assignment is the right side). `contextualType` ignores the type tsc derives from a pattern for its value (`isDestructuringSource`). Generic instances forget their parameters' and patterns' locals (`forgetLocals` on the whole declaration).
- **Tests:** `pts/tests/run/destructuring.pts`, `destructuring_integers.pts`; `errors/destructuring_not_yet.pts`.

### 9.32 Default parameter values (M2.6d2b)

- **IR** (D-055): the primitive type `ir.Given` (C `bool`) is the type of the flag that follows each parameter with a default in `Func.Params`, `FuncType.Params` and `InterfaceMethod.Params`; `ir.GivenConst` is a flag a call passes and `ir.LeftOut{T}` the zero value it passes for an argument it leaves out. The IR prints `left-out T` and `given(true)`.
- **Lowering:** `parameter` (`pts/lower/lower.go`) makes the argument local (`x_arg`) and the flag (`x_given`) for a parameter with a default, records a `parameterBinding`, and records the local the body sees per parameter (`paramLocals`, which `bindParameters` restores for generic instances); `parameterPrologue` (`destructuring.go`) declares `x = x_given ? x_arg : default` (`parameterDefault`) and destructures patterns, in order. Callers fill in flags: `typedArguments` (left-out arguments and `undefined` → `notPassed`), `callbackArguments` (`callbacks.go`; array and map helpers), `adaptedArguments` (`closures.go`), `passedParams` (`bind` and `call`, `methods.go`). `signatureType` gives optional parameters (`isOptionalParameter`, or past `MinArgumentCount` outside an expanded tuple rest) their type without `undefined` and a flag; `contextualFuncType` lets an arrow with defaults take the shape of a context that always passes the argument, and `bindAlwaysPassed` drops those flags (`alwaysPassed`), except in a named function expression, which may call itself leaving the argument out. An abstract method's optional parameter (`x?: T`) takes a flag like a default's. `Array.from`'s mapper is called with (element, index) (helper key `map from`). `parameterSymbol` finds a parameter property's own symbol among its constructor's locals (tsc's binder gives the declaration the property's symbol), so references to it resolve. `markCells` skips functions that failed to lower.
- **Semantic pass:** `parameterDefaults` (`pts/check/functions.go`) types defaults against numeric parameters; `parameterNumeric` strips `undefined` from optional parameters (`passedOrNot`); `call` skips `undefined` arguments.
- **Tests:** `pts/tests/run/default_parameters.pts`, `default_parameters_integers.pts`, `default_parameters_modules.pts`; `errors/default_parameters_not_yet.pts`, `errors/default_parameter_conversions.pts`; `TestParametersWithDefaultsTakeAFlagThatSaysWhetherTheCallPassedThem` in `pts/lower`.

### 9.33 Map entries as pairs (M2.6d3)

- **Lowering** (`pts/lower/maps.go`, D-056): `mapIterableOf` gives what a `for…of`, `Array.from` or `new Map` iterates over a map or set: its keys, values or entries (`mapItems`; a map itself yields entries, a set its elements). `mapItem` reads an iteration's item, an entry as a new pair (`pairOf`, a tuple shape from `tupleOf` in `tuples.go`); `mapForOf` binds a name (`bindName`) or a pattern (`destructure`) from it; `arrayFromMap` names its helpers `Array.from keys|values|entries` (`arrayFromMethods`). `destructure` binds the names of an array pattern over a tuple made in place directly to its values (`namesOfTuple`, `bindTupleValues`), which keeps `for (const [k, v] of map)` free of allocations. `newMap` copies a map from `m` or `m.entries()`, and makes one from an array of pairs with the `Map.of` helper (`mapOfEntries`); an array literal argument is lowered with `arrayLiteralOf` as an array of the map's pairs. `apply` with a tuple (`methods.go`: `callArguments`) binds the tuple in a local whose fields are the arguments (`convertArguments`, split from `typedArguments`).
- **Tests:** `pts/tests/run/map_entries.pts`, `map_entries_integers.pts`; `errors/maps_not_yet.pts`; `TestMapEntriesDestructuredInForOfMakeNoPairs` in `pts/lower`.

### 9.34 Rest parameters and spread arguments (M2.6e1)

- **IR** (D-057): `ir.Rest` is the type of a rest parameter in `Func.Params`, `FuncType.Params` and `InterfaceMethod.Params` (`...number[]`, C `pts_array *`); `ir.RestArgs` reads it as its array; `ir.SpreadArg` is a spread argument of console.log, which C passes to `pts_console_log_spread` with a flag per argument.
- **Lowering:** `parameter` makes a rest parameter's IR parameter (`xs_rest`, `restOf`) and a `parameterBinding` that `parameterPrologue` binds as `xs = RestArgs(xs_rest)`; `signatureType` and interface methods (`objects.go`) give rest parameters their `Rest` type. `typedArguments` lowers arguments with `argumentValues` (a spread of an array becomes a `spreadValue` placeholder; a spread tuple literal or variable becomes its elements) and `convertArguments` packs the values for a `Rest` parameter with `restArray` (`NewArray`, or `concat` with spread arrays). `adaptedArguments` packs (`packedArguments`) or unpacks (checked reads of the rest array); `callbackArguments` packs; `bindsRest` reports `bind` into a rest parameter.
- **Runtime:** `pts_console_log_spread` (`console.c`).
- **Tests:** `pts/tests/run/rest_parameters.pts`, `rest_parameters_integers.pts`; `errors/rest_parameters_not_yet.pts`, `errors/rest_parameter_conversions.pts`.

### 9.35 Spread in literals and rest properties (M2.6e2)

- **Lowering** (D-058): `arrayLiteralOf` hands a literal with spreads to `spreadArray` (`arrays.go`), which builds the array with `concat`; `spreadParts` lowers a spread element (sets and maps through `mapItemsArray`, strings through `ArrayFromString`, otherwise `argumentValues`). `push`/`unshift` with spreads use the new `ArrayPushAll`/`ArrayUnshiftAll` over such an array. `tupleLiteral` expands spread tuples. `objectLiteral` hands a literal with spreads to `spreadObjectLiteral` and `spreadObject` (`objects.go`), which read properties with `propertyOf` in `spreadNames` order (`destructuring.go`) and bind values to locals in the literal's order where needed. `propertyPart` lowers a rest element with `restProperties`.
- **Runtime:** `pts_array_push_all`, `pts_array_unshift_all` (`array.c`).
- **Tests:** `pts/tests/run/spread_literals.pts`, `spread_literals_integers.pts`; the spread lines of `errors/arrays_not_yet.pts`, `object_types_not_yet.pts`, `destructuring_not_yet.pts`.

### 9.36 Hash-map objects (M2.6e3)

- **IR** (D-059): `ir.MapType` with `Record` (key `String`); new `MapOp`s `MapPut` (`o[k] = v`, valued v), `MapGetInto` (found flag, value into a local) and `MapAssign` (copy entries in, for spreads).
- **Lowering** (`pts/lower/records.go`): `recordType` (from `irType`) and `isHashMapType`/`isRecordExpression`; `isRecordElement`, `recordElement` (`MapGet` from `valueExpression`, `MapGetChecked` elsewhere), `recordKey` (numbers through `ToString`); `place.key` for writes and compound assignments; `recordElementFallback`/`recordFallback` (`??`, `||`), `recordElementOrSet` (`??=`, `||=`), `recordElementComparedWithUndefined`, `inRecord`, `deleteExpression`; `recordLiteral` (from `objectLiteral`) with `spreadIntoRecord`; `objectStaticCall` (`Object.keys`/`values`/`entries` through `mapItemsArray`'s helpers named `Object.*`, `Object.hasOwn`), `recordMethodCall` (`hasOwnProperty`), `forInStatement`. `propertyOf` reads entries for destructuring. `jsName` names functions after constant computed and numeric keys (`constantPropertyName`). The semantic pass (`contextualPropertyType`) falls back to the index signature's value type.
- **C:** `pts/cgen/maps.go` (`mapKind`, `PTS_KIND_RECORD` descriptors `pts_type_record_<n>` named "Object", `PTS_RECORD_GET`, `PTS_MAP_PUT`, `pts_record_iterate` for `MapIterate`). Runtime: `map.c` (`pts_record_iterate`, `ordered_snapshot`, `array_index_of`, `pts_map_assign`, `pts_record_key_error`), `format_record_entries` and `format_string_key` (`inspect.c`).
- **Tests:** `pts/tests/run/records.pts`, `records_integers.pts`, `records_gc.pts`, `record_missing_key.pts`; `errors/records_not_yet.pts`; `record_keys_*` in `map_test.c`.

### 9.37 binary-trees, nbody and Math basics (M2.7a)

- **Benchmarks** (D-060): `pts/bench/binary_trees` (depth 18; leaves are their own children until null arrives in M3.1) and `pts/bench/nbody` (5,000,000 steps; unrounded energies), each with its Go twin; `pts/bench/run.sh` runs them under Node and Bun too.
- **Math** (`pts/lower/math.go`): `mathMember` recognises the global Math; `mathCall` lowers `sqrt`, `abs`, `floor`, `ceil`, `round`, `trunc`, `sign` (`ir.MathSqrt` …, clang builtins or `pts_math_round`/`pts_math_sign` in `pts.h`) and folds `max`/`min` of any number of arguments into `pts_math_max`/`pts_math_min` of two; `mathConstant` lowers the eight constants. Tests: `pts/tests/run/math_basics.pts`, `errors/math_not_yet.pts`.
- **Collector and compiler flags:** `pts_runtime_init` sets bdwgc's free-space divisor to 1 unless `GC_FREE_SPACE_DIVISOR` is set; `pts/toolchain` compiles with `-fno-math-errno`.

### 9.38 String building (M2.7b)

- **Runtime** (D-061): `pts_string.bytes` is a pointer; `pts_string_alloc` points it just past the header; `PTS_STATIC_STRING` and the one-character table in `string_methods.c` point it at static bytes. `pts_string_concat` (`string.c`) tries `appended` (claim the bytes after the left string in its `pts_string_buffer` with a CAS on `used`), else copies, into a new buffer (`new_buffer`, `buffered_string`) when the result is at least `BUFFERED_MIN_BYTES`.
- **Benchmark:** `pts/bench/string_building` (`+=`, template literals and `join` of a million pieces, then a checksum through `charCodeAt`), with its Go twin.
- **Tests:** `appending_leaves_earlier_strings_unchanged`, `building_a_string_piece_by_piece_reuses_its_buffer`, `racing_appends_never_mix_bytes` in `string_test.c`.

### 9.39 Union values (M3.1a)

- **IR** (D-062): `ir.Union` (members; `Has`), `ir.ToUnion`, `ir.FromUnion` (`Checked` for `x!`), `ir.IsNullish` (`Only` null or undefined); the builtin `ValueStrictEquals`.
- **Lowering** (`pts/lower/unions.go`): `unionType` (from `irType`; `unionOf` interns), `narrowed` (applied in `expression` to identifiers, property and element accesses, and in compound assignments and `++`), `toUnion` (from `convert`; `memberFor` picks the member a number, function or object converts to), `isNullishTest`/`nullishTest`, `unionEquality` (from `operation`), `nullishCoalescing`, `unionLogical` (from `logical`, when `involvesUnion`), `asUnion`, `isUnionValue`; `nonNull` (`arrays.go`) makes checked conversions; `sameType` converts `?:` branches to a union result; `instanceOf` accepts unions; union fields default to undefined. Reading a property of a union is PTS1001 (`propertyAccess`, until M3.1c).
- **Semantic pass:** `NumericContext` (`pts/check/numeric.go`) types numbers in union contexts; identifiers narrowed to a numeric member take its type; `function.returned` checks returns of union results.
- **C:** `pts_value` for unions in `declarator`, `fieldKind` (`PTS_FIELD_VALUE`), `holdsPointer`, `zeroValue` (undefined), `box`, `truthy`, `toString`; `fromUnion` unboxes. Runtime: `pts_value_as_i64`/`as_u64` (`integer.c`), `pts_value_is_nullish`, `pts_value_non_null` (inline; `pts_throw_non_null`), `pts_value_strict_equals` (`string.c`), `pts_value_instance_of`.
- **Tests:** `pts/tests/run/unions.pts`, `unions_integers.pts`, `non_null_error.pts`; `errors/unions_not_yet.pts`; binary-trees uses `TreeNode | null` children.

### 9.40 Optional parameters (M3.1b1)

- **Lowering** (D-063): `parameter` gives an optional parameter with a body a `T | undefined` local (`withUndefined`) bound by `parameterDefault` (default undefined), and a parameter with a default whose type includes undefined a `T` local (`withoutUndefined`) whose default also applies to an undefined argument; `parameterType` no longer rejects them. `convertArguments` passes a `T | undefined` value to an optional `T` parameter through `maybePassed`; `adaptedArguments` maps a left-out argument to undefined for a `T | undefined` parameter; `nullishAssignment` lowers `??=` (from `compoundAssignment`). cgen's `logical` takes `given` flags as booleans.
- **Tests:** `pts/tests/run/optional_parameters.pts`.

### 9.41 Optional chaining (M3.1b2)

- **Lowering** (`pts/lower/chains.go`, D-064): `optionalChain` (dispatched from `expression` and `valueExpression` for an outermost chain not already being lowered, `chainRests`), `chainObjects` (the substituted object and its non-nullable checker type, used by `expression` and `typeAt`), `isUnloweredOptionalChain` (replaces the old PTS1001 checks; `chainDepth`). `convert` takes a dynamic value to a union as it is; `nonNull` uses `GetNonNullableType`; `literalContext` (`objects.go`) strips null and undefined from an object literal's context.
- **Tests:** `pts/tests/run/optional_chaining.pts`.

### 9.42 Optional properties (M3.1b3a)

- **IR** (D-065): `ir.Field.Optional`, `ir.FieldRef.Raw`, `ir.HasField`, `ir.IsAbsent`, `ir.Absent`; `Interface.String` writes a recursive reference to itself as `{...}`.
- **Lowering** (`pts/lower/optional.go`): `objectMembers` makes optional fields (optional methods are PTS1001); `membersKey` and `providesProperty` tell them apart; `isObjectType` accepts mapped types and `isMappedProperty` routes their property accesses to `shapeProperty`; `literalType` takes a contextual type whose extra properties are optional and `withLeftOut` fills them with `Absent`; `copiedRead` and `spreadOver` (spreads, `restProperties`); `withDefault` (object patterns, assignment patterns); `propertyIn` (`"p" in o`); `fieldType` (classes, parameter properties).
- **Semantic pass:** a property access of a union type takes the union's numeric member (`NumericContext`).
- **C:** `fieldRead` results of optional fields are wrapped in `pts_value_present`; `hasField` compares with `PTS_VALUE_ABSENT` (through an interface, see §9.43). Runtime: `PTS_VALUE_ABSENT`, `pts_value_present` (`pts.h`); `inspect.c`'s `has_field` and `present_field_count`.
- **Tests:** `pts/tests/run/optional_properties.pts`, `optional_properties_integers.pts`, `TestLeftOutOptionalPropertiesAreAbsent`, `inspect_test.c` (`absent_properties_are_not_printed`).

### 9.43 Objects without an optional property (M3.1b3b)

- **Lowering** (`pts/lower/optional.go`, D-066): `objectCast` falls back to `holdsOptionally` (`holdsAsOptional`, `optionalMember`) and records a `relaxedCast`; `checkRelaxedCasts` (from `lower`, after everything is lowered) walks the IR for every interface cast (`interfaceCasts`), finds each relaxed cast's `concreteTypes` and reports the first that holds a property otherwise (`unheld`, `objectTypeString`).
- **C:** `fieldRead`, `fieldStore` and `hasField` use `pts_optional_get`, `pts_optional_set` and `pts_object_has` for an optional field through an interface (`isOptionalThroughInterface`); `order.go` marks such stores as possibly throwing. Runtime: `pts.h`'s inline fast paths, `object.c`'s slow paths (`find_field`, `cache_offset`, `cache_accessor`), `pts_store_value_in_slot` (`array.c`).
- **Tests:** `pts/tests/run/optional_properties.pts` (last section), `optional_property_add_error.pts`, `pts/tests/errors/optional_properties_not_yet.pts`, `object_test.c` (`optional_sites_use_whatever_holds_the_property`).

### 9.44 Unions of objects, `in` and `==` (M3.1c)

- **IR** (D-067): `ir.Field.Mixed`, `ir.HasKey`, `ir.Program.InKeys`, the builtin `ValueLooseEquals`.
- **Lowering** (`pts/lower/union_objects.go`): `isUnionReceiver` routes property reads (`propertyAccess`), writes (`assignmentTarget`) and calls (`call`) on unions of objects to `unionProperty` and `unionMethodCall` (`unionObjectMembers`, `holdsWithin`, `isMethodSymbol`, `asUnionInterface`, `internInterface`); `keyIn` (from `propertyIn`); `isUpcastOfMembers` (from `convert`); `looseEquality` (`unions.go`); `contextTakesUndefined` (element reads in `expression`); `literalType` picks a union member (`fitsLiteral`); `memberFor` falls back to `providedMember`; `sameType` makes a union of `?:` branches whose context is a union or nothing.
- **C:** `fromUnion` checks a class target with `pts_value_as_instance`; Mixed fields go through `pts_optional_get` (`isOptionalThroughInterface`); `HasKey` is `pts_object_has`; `collectInterfaceMembers` adds `InKeys` to the method tables. Runtime: `pts_value_as_instance`/`pts_throw_not_instance`, `pts_value_loose_equals` (`string.c`), `pts_string_to_number` (`string_methods.c`), `pts_object_to_string` (`array.c`), `pts_object_has` (`object.c`).
- **Tests:** `pts/tests/run/union_objects.pts`, `loose_equality.pts` (a matrix of every pair of 43 values against Node), `union_narrowing_error.pts`, `errors/unions_not_yet.pts`, `string_methods_test.c` (`strings_convert_to_numbers_as_number_does`).

### 9.45 Exceptions (M3.2a)

- **Runtime** (D-068): `pts_handler`, `pts_handlers` (thread-local), `pts_try_enter`/`pts_try_leave`/`pts_caught` (`pts.h`), `pts_throw` and `report_uncaught`, the native error classes (`pts_error`, `pts_type_builtin_<Name>`, `pts_class_builtin_<Name>`), `pts_error_init`, `pts_error_new`, `pts_error_to_string`, `pts_error_get_stack`/`_set_stack`, `pts_throw_new` (printf-style), `pts_throw_error` (by class name), `pts_is_error` (`error.c`); `format_error`, `improve_stack` and `pts_inspect_value_write` (`inspect.c`); `pts_value_as_string`, `pts_value_to_number_checked`, `pts_value_as_bool` and `pts_throw_not_primitive` (`string.c`); `pts_value_to_string` converts objects through `pts_object_to_string`. `pts_panic` is only for running out of memory and internal errors.
- **IR:** `ir.Try`, `ir.Throw`, `ir.Class.Builtin`, `ir.Func.Extern`.
- **Lowering** (`pts/lower/exceptions.go`, `errors.go`): `throwStatement`, `tryStatement` (the finally transform: `finallyContext`, `leaveFunction` from `returnStatement`, `jumpTo` from `jump`), `tryCatch` (the catch binding); `declareErrorClasses` (before `declareClasses`; `ErrorOptions` is an object type through `libObjectTypes`); `narrowedValue` (from `narrowed`), `asFromValue` (from `as`), `stringCall` (`String(x)`), `isObjectToString`/`objectToString` (`o.toString()`, `super.toString()`), `isDynamic`. `irType` maps `unknown` and `never` to Value; `resultType` maps a never result to Void and `convert` sequences a call of one with a zero value; `linkOverrides` keeps Error's methods non-virtual; `typeAt` strips tsc's optional marker from links inside a chain being lowered (`s?.split(",")[0]`).
- **C:** `tryStatement`, `returnStatement`, `jump` (leaves the tries a label is outside of: `labelTries`), `volatileLocals` and `localDeclarator` (`cgen.go`); builtin classes get no struct, descriptor or method entries of their own, names `builtin_<Name>`, and their extern functions' names (`externFunctions`); `basePath` skips them.
- **Tests:** `pts/tests/run/exceptions.pts`, `exceptions_edges.pts`, `error_classes.pts`, `caught_unknown.pts` (all against Node), `runtime_errors_caught.pts`, `caught_unknown_cast_error.pts`, `errors_printed.pts`, `uncaught_error.pts`, `uncaught_value.pts`; `errors/exceptions_not_yet.pts`; `error_test.c`; `TestLocalsATryBlockAssignAreVolatileAndJumpsOutOfItLeaveIt`, `TestAThrowLandsInTheCatchBlockWithTheThrownValue`, `TestFinallyRunsAfterTheProtectedCodeRecordsHowItCompleted`.

### 9.46 Stack traces and the stack guard (M3.2b)

- **Runtime** (D-069): `capture_frames` (from `pts_error_init`), `format_stack` and `add_source_frame` (from `pts_error_get_stack`), `pts_register_function_names` and the `pts_function_name` table (`error.c`); `pts_stack_limit`, `pts_stack_limit_init` (from `pts_runtime_init`), `pts_throw_stack_overflow` and `pts_stack_overflow_stub` (`runtime.c`); `pts_stack_check` (`pts.h`). libbacktrace: `pts/third_party/libbacktrace` (submodule, commit 0b9b49c), `pts/third_party/libbacktrace-config`, `libbacktraceLibrary` (`pts/toolchain/libraries.go`). A new thread must call `pts_stack_limit_init` (M5).
- **Lowering:** Error's `#frames` field (`errors.go`).
- **C:** `functionNameTable` (`builtinOwners`) and its registration in `main`; `mayRecurse` decides which functions start with `pts_stack_check()`.
- **Tests:** `pts/tests/run/stack_frames.pts`, `stack_overflow.pts` (no longer skipped), `stack_overflow_caught.pts`, `errors_printed.pts` (now against Node, with frameless stacks).

### 9.47 `any` values (M3.3a)

- **Runtime** (D-070): `dynamic.c`: `pts_dynamic_get`, `pts_dynamic_set`, `pts_dynamic_has`, `pts_value_add`, `pts_value_to_number`, `pts_value_less`, `pts_value_less_equal`, `pts_throw_type_mismatch`, `pts_value_is_integer_of`; `pts_method.result` (the kind a getter returns or a setter takes); `pts_value_is_string` (`pts.h`).
- **IR:** the builtins `DynamicGet`, `DynamicSet`, `DynamicHas`, `ValueAdd`, `ValueToNumber`, `ValueLess`, `ValueLessEqual`.
- **Lowering** (`pts/lower/dynamic.go`): `isAny`, `isDynamicSource`, `isCheckable`, `dynamicRead` (from `propertyAccess` and element accesses), `dynamicPlace` (from `assignmentTarget`; `place.dynamic`), `dynamicOperation` (from `operation`), `toNumber`, `dynamicIncrement`, `dynamicConversion` (from `convert`), `dynamicWithDefault` (from `withDefault`); `irType` maps any to Value; `as` boxes into any and unknown; `keyIn` and `propertyPart` handle Values; calls through any are reported in `call`.
- **Semantic pass:** `expr` gives an any the numeric type expected of it.
- **C:** `fromValue` and `valueTest` (`cgen.go`) check every conversion of a Value; `accessorKind` fills `pts_method.result`; every getter and setter is in its class's method table.
- **Tests:** `pts/tests/run/any_values.pts` (against Node), `any_checks_error.pts`; `errors/any_not_yet.pts`; `dynamic_test.c`; `TestAnAnyConvertsToEveryNumericTypeAndIsCheckedAtRunTime`.

### 9.48 Converting dynamic values to object types (M3.3b1)

- **Runtime** (D-071): `pts_check`, `pts_check_property`, `pts_check_kind` (`pts.h`); `convert.c`: `pts_value_convert` (`convert`, `object_fits`, `field_fits`, `copy_object`, `convert_tuple`, `convert_array`, `convert_record`, the cycle stack `converting`).
- **IR:** `ir.Interface.Copy`, `CopyOrder`.
- **Lowering:** `dynamicConversion` (now for object types too), `prepareDynamicTarget`, `copyShape` (`dynamic.go`); `asFromValue` uses them (`exceptions.go`).
- **C:** `checks.go`: `collectChecks`, `hasValueTest`, `check`, `checkDefinitions`, `checkProperties`; `fromValue` calls `pts_value_convert` for types without a value test; a check of a map type makes sure its descriptor exists.
- **Tests:** `pts/tests/run/any_conversions.pts` (against Node), `any_conversions_copy.pts`; `errors/any_not_yet.pts`; `convert_test.c`.

### 9.49 Declared types and checked writes through `any` (M3.3b2a)

- **Runtime** (D-072): `pts_field.check`, `pts_method.check`, `pts_type.check` (an array's or hash-map object's element type), `PTS_CHECK_OPAQUE`, `pts_array_types` (the runtime's array descriptors, one per element kind), `pts_array_of(type, …)` and `pts_array_flat(…, type)` taking descriptors (`pts.h`); `convert.c`: declared types (`pts_check_declared`, `kind_checks`, `same_type`), implication (`implies`, `place_implies`, `descriptor_implies`, `properties_imply`, the pair stack `implying`), `object_fits` by declared types, arrays, tuples and hash-map objects as they are only by their own descriptor's types, `pts_value_try_convert`, cycles of copied data failing; `dynamic.c`: `storable` (checked writes to fields, setters, array elements and hash-map values), `value_description`.
- **Lowering:** `prepareWrittenTypes` and `lowerer.dynamicWrite` (`dynamic.go`): copy shapes for the interfaces written values may be converted to.
- **C:** `checks.go`: `emitter.declaredTypes`, `collectDeclaredTypes`, `describeArray`, `arrayDescriptor`, `arrayDescriptorDefinitions`, `declaredType`, `checkDeclarations` (before the descriptors), opaque checks of function types and object types with methods; `classes.go`: `accessorType`, `shapeAccessorType`, declared types in field and method tables; `maps.go`: hash-map objects' `.check`; `arrays.go`: array literals and `flat` with descriptors.
- **Tests:** `pts/tests/run/any_writes.pts` (against Node), `any_writes_error.pts`; `TestDescriptorsCarryDeclaredTypesOnlyInProgramsThatWriteThroughAny` (`cgen_test.go`); `convert_test.c`.

### 9.50 Guarded interface sites (M3.3b2b)

- **Runtime** (D-073): the site functions and macros take the property's check (`pts_field_address`, `PTS_GET`, `PTS_SET`, `pts_property_find`, `pts_optional_get`, `pts_optional_set`; NULL where descriptors carry no declared types), `PTS_FIELD_SET` and `pts_field_set_slow`; `object.c`: `wrong_kind`, `exact`, `store_converted`, the guarded slow paths; `pts_check_same`, `pts_check_implies` (`convert.c`), `pts_storable`, `pts_call_setter` (`dynamic.c`).
- **C:** `classes.go`: `siteCheck`, `fieldStore` writing `PTS_FIELD_SET` through interfaces; `checks.go`: `checkSite` (the checks of properties read and written through interfaces).
- **Tests:** `pts/tests/run/any_views.pts`; `object_test.c`.

### 9.51 Calls through `any` (M3.3c1)

- **Runtime** (D-074): `pts_dynamic_function`, `pts_dynamic_method`, `pts_type.call`, `pts_method.dynamic` (`pts.h`); `dynamic.c`: `pts_dynamic_call`, `pts_dynamic_call_method`, `not_a_function`; Error's `to_string_dynamic` (`error.c`).
- **IR:** `ir.DynamicCall`, `ir.DynamicCallMethod`.
- **Lowering:** `dynamicCall`, `lowerer.firstDynamicCall`, `prepareDynamicTypes` (parameter types; was `prepareWrittenTypes`) (`dynamic.go`).
- **C:** `dynamic_calls.go`: `emitter.dynamicCalls`, `collectDynamicEntries`, `checkParams`, `dynamicEntryDeclarations`, `dynamicEntryDefinitions`, `dynamicEntries`, `dynamicArguments`, `returnDynamic`, `dynamicMethodMember`, `dynamicCallArguments`; `boxAs` (`cgen.go`); every method of every class in method tables when the program calls through `any` (`collectInterfaceMembers`); `shapeEntryName`.
- **Tests:** `pts/tests/run/any_calls.pts` (against Node), `any_calls_error.pts`; `errors/any_not_yet.pts`.

### 9.52 Function types and object types with methods from `any` (M3.3c2)

- **Runtime** (D-075): `PTS_CHECK_FUNCTION`, `pts_check.adapt`, `pts_check_method` and `pts_check.methods`, `pts_type.signature`, `pts_method.signature`, `pts_adapter`, `pts_adapter_call`, `pts_dynamic_call_closure`, `pts_dynamic_call_spread`, `pts_dynamic_call_method_spread` (`pts.h`); `convert.c`: function checks in `convert`, methods in `descriptor_implies` and `properties_imply`, no copies into object types with methods; `dynamic.c`: bound methods (`bound_method`, `bound_method_type`, `bind_method`), `spread_length`, `spread_element`, `spread_arguments`.
- **Lowering:** `prepareDynamicTarget` accepts function types and object types with methods (`dynamic.go`); spread arguments in `dynamicCall`.
- **C:** `checks.go`: `canonicalFuncType`, `signatureCheck`, function-type checks with `.adapt`, methods in interfaces' checks; `dynamic_calls.go`: `adapterFunctionTypes`, `adapterDefinition`, `adapterMaker`, `dynamicMethodMembers` (dynamic entry and signature); closures' `.signature` (`closures.go`); `callBuiltin` and `spreadArgs` for spread arguments of dynamic calls (`cgen.go`); `emitter.dynamicCalls` folded into `declaredTypes`.
- **Tests:** `pts/tests/run/any_functions.pts` (against Node), `any_functions_error.pts`; updated `any_writes_error.pts`, `any_calls_error.pts`, `errors/any_not_yet.pts`.

### 9.53 Builtin methods through `any` (M3.3c3a)

- **Runtime** (D-076): `dynamic_methods.c` (the tables `string_methods`, `primitive_methods`, `array_methods`, `function_methods`; `pts_builtin_method_call`, `pts_value_is_array`, `bound_function`), `dynamic_internal.h` (`pts_value_description`, `pts_spread_length`, `pts_spread_element`, `pts_builtin_method_call`); `pts_dynamic_call_method` dispatches to them, then to a property's function.
- **IR:** `ir.ValueIsArray`.
- **Lowering:** `arrayIsArray` for dynamic values and unions (`arrays.go`).
- **Tests:** `pts/tests/run/any_builtin_methods.pts` (against Node), `any_builtin_methods_error.pts`; updated `any_calls_error.pts`, `any_checks_error.pts`, `errors/any_not_yet.pts`.
