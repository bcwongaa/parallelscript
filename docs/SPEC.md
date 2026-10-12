# ParallelScript language specification

ParallelScript is TypeScript compiled ahead of time to native binaries, with a garbage collector and real parallelism. This document is the language reference. It starts from the owner's brief (`docs/BRIEF.md` §2) and grows as features are implemented. **If a behaviour isn't in this spec, it isn't decided.**

Order of authority: `docs/BRIEF.md` (with the owner overrides in `CLAUDE.md`) → this spec → `docs/DECISIONS.md`. Decision ids such as `D-L03` or `D-O05` point into `docs/DECISIONS.md`.

**Status.** Sections 1–8 describe the language as built through M6.2: milestones M0–M5 (tags `m0` … `m5`), RegExp and `pts:http`. What isn't built yet is marked *planned* with its milestone: `fetch` (M6.3), `pts lsp` and editor support (M7), and performance work such as escape analysis and a precise collector (M8). Valid TypeScript that the compiler can't lower yet is the compile error PTS1001 "… is not supported yet" (§2.3); the sections below name the main cases. Section 9 holds the implementation notes of each task.

---

## 1. Syntax and files

- Source files use the extension **`.pts`** (D-O01, D-004: the brief's original extension, confirmed by the owner after a brief switch to `.ps`). Declaration files stay `.d.ts`.
- `import "./x"` tries `x.pts` first, then the usual TypeScript extensions; `import "./x.pts"` names the file itself, and `./x.js` doesn't map to `x.pts` (D-001, D-004).
- Every file is an ES module in strict mode, whether or not it imports or exports anything (D-005). A `.ts` file reachable by imports is part of the program too (§6).
- The syntax is TypeScript plus one contextual keyword, `parallel` (§5).
- `parallel` is a keyword only in two positions; everywhere else it is an ordinary identifier, so existing code that uses it as a name keeps working:
  - `parallel <expression>` in expression position;
  - `parallel for (...)` in statement position.
- Disambiguation rules for `parallel` are in §5.3.

### 1.1 The compatibility principle

A `.pts` program that uses only plain TypeScript features (no `parallel`, no integer types, no `pts:` imports) must print exactly what Node prints when run as TypeScript, except where this spec documents a difference (§8).

- "Node" is Node 24 running the program as ES modules with its type transform (`--experimental-transform-types`, D-009). What must match is stdout and the exit code; stderr's text is not compared, but an uncaught error goes to stderr and exits with code 1, as in Node (D-O04).
- `pts:path`, `pts:process`, `pts:fs` and `pts:fs/promises` count as plain TypeScript: they stand for Node's `node:` modules of the same names (§7.3).
- The tests hold the implementation to this: every plain-TypeScript run test and docs sample, a corpus of 300 realistic programs (each that still diverges says why in a `// skip:` line) and generated corpora of numbers and strings are compared with Node (`pts/tests/README.md`).

---

## 2. Toolchain and pipeline

### 2.1 Front end

- The front end is a fork of TypeScript 7 (typescript-go), pinned at `typescript/v7.0.2`. It provides the scanner, parser, binder, checker, and the language server that `pts lsp` will serve (planned, M7.1).
- The compiler, `pts`, is written in Go inside the fork's module (it needs the fork's `internal/` packages). New code lives in `cmd/pts/` and `pts/`.
- Upstream modifications are minimal, marked `// PTS:` and listed in `docs/FORK.md`: the `.pts` extension in module resolution (D-001, D-004), the integer types as number types of their own in the checker (D-048), and `parallel` in the parser (D-120).
- Options are fixed (D-005): every program is checked with `strict`, target and module `ESNext`, `moduleResolution: bundler`, `allowImportingTsExtensions`, `moduleDetection: force`, `lib: ["esnext"]` (no DOM: `document` or `fetch` is an unknown name) and `types: []`. `tsconfig.json` is not read.
- The prelude, ParallelScript's globals (the numeric types, `console`, timers, `performance`, `process`, `parallel`, `.par`), is a declaration file, `pts/lib/prelude.d.ts`, and a root file of every program. The `pts:` modules are ParallelScript sources, `pts/lib/*.pts` (D-116). Both are embedded in the compiler (served at `ptslib:///`), so the checker, and later the editor, resolve them without files on disk.

### 2.2 Pipeline

1. TypeScript AST plus checker.
2. **PTS semantic checks**: the rules of this spec that tsc doesn't know (`pts/check`).
3. Lowering to a typed **ParallelScript IR**.
4. IR passes. There is no separate pass stage yet: whole-program analyses run at the end of lowering (which captured locals live in cells, `markCells`; the checks of conversions to object types with optional properties, `checkRelaxedCasts`; the copy shapes that dynamic conversions need, `prepareDynamicTypes`), and others inside the C emitter (where evaluation order needs temporaries, which functions may recurse, generator frames, which descriptors carry declared types).
5. Emit **C11**.
6. Compile with **clang** (`-O2 -g -ffp-contract=off -fno-math-errno -falign-functions=32`: D-011, D-060, D-069, D-119) and link with the runtime into one native binary.

**The IR** is structured (not SSA) and fully typed. Boxing, closure environments, monomorphized generics, and async functions and generators (already turned into state machines) are all explicit in it. C is never emitted directly from the TS AST.

**Generated C** is readable and carries `#line` directives pointing at the `.pts` source, so gdb, lldb and crash backtraces show ParallelScript lines.

**The runtime** is written in C (`pts/runtime/`) and statically linked. Its dependencies are vendored as pinned submodules and built from source: bdwgc 8.2.12 (the collector), libuv 1.53.0 (event loops), PCRE2 10.49 (RegExp, D-132), Ryu 2.0 (number formatting, D-011) and libbacktrace (stack traces, D-069). A built program depends only on the C library (glibc's libc and libm); libgcc is linked statically.

`pts` finds its runtime sources through `PTS_HOME`, or else in the nearest directory above its own executable or the working directory that holds them. It builds each vendored library and the runtime once, into `~/.cache/pts/<name>-<key>/lib<name>.a`, where `<key>` hashes the compiler's version, the flags and the sources (`PTS_CACHE_DIR` replaces `~/.cache/pts`; a file lock makes concurrent builds share one), and compiles user programs against those cached static libraries (D-O11, D-003).

**Target:** x86-64 Linux with glibc. The stack guard (D-069) and the publication of new objects to other threads (D-130) rely on x86-64; other targets aren't supported yet. Cross-compiling with `pts build --cc zig` is a stretch goal.

### 2.3 The CLI

| Command | Purpose |
|---|---|
| `pts build file.pts [-o out]` | Build a native binary (default output: the file's base name, without `.pts`) |
| `pts run file.pts [args…]` | Build and run; arguments after the file go to the program |
| `pts emit-c file.pts [-o file.c]` | Print the generated C (or write it to a file), for debugging |
| `pts help` | Show the usage |
| `pts test` | Test runner (planned, M7.3) |
| `pts lsp` | Language server (planned, M7.1) |

- **Exit codes** (D-006): 0 on success, 1 for compile or toolchain errors, 2 for a malformed command line. `pts run` exits with the program's own code, or 128 + the signal number if the program was killed.
- **Diagnostics** (D-006): one per line, `file:line:col - error CODE: message`, the file relative to the working directory when it is below it. tsc's errors keep their `TSnnnn` codes; semantic errors are reported only when there are no syntax errors, as in tsc. ParallelScript's own codes:

| Code | Meaning |
|---|---|
| PTS1001 | Valid TypeScript the compiler can't lower yet ("… is not supported yet") |
| PTS1002 | A feature left out by design, with the reason (§3.10) |
| PTS2001 | A lossy implicit numeric conversion (§3.2) |
| PTS2002 | A literal that doesn't fit its numeric type, or isn't an integer |
| PTS2003 | Two integer types with no lossless common type |
| PTS3001 | The guardrail: an outer variable assigned in a parallel body (§5.6) |
| PTS3002 | What a `parallel for` can't hold (§5.5) |

- **Environment variables:**

| Variable | Read by | Meaning |
|---|---|---|
| `PTS_HOME` | `pts` | The directory that holds `pts/runtime` |
| `PTS_CACHE_DIR` | `pts` | Where built libraries are cached (default `~/.cache/pts`) |
| `PTS_CC`, `PTS_AR` | `pts` | The C compiler and archiver (default `clang` and `ar`) |
| `PTS_THREADS` | programs | The number of event loops and pool threads (§5.1) |
| `GC_FREE_SPACE_DIVISOR`, `GC_INITIAL_HEAP_SIZE`, `GC_MARKERS` | programs | bdwgc's settings, which override the runtime's defaults (§4) |
| `TZ` | programs | The local time zone of `Date` (§7.2) |

---

## 3. Types and values

### 3.1 Variables

- Variables have fixed types. `let x = 1; x = "s"` is a compile error (tsc already reports it).
- `let` and `const` are block-scoped; `var` is not supported (§3.10).
- Reading a module-scope binding before its declaration has run (from a function called early, or in an import cycle) throws `ReferenceError: Cannot access 'x' before initialization`, as in JS (D-021, D-022). So does a local variable that a closure reads or assigns before its declaration has run (a hoisted function called above a `const` it uses, D-140).

### 3.2 Numbers

- `number` keeps JavaScript semantics exactly: IEEE-754 f64, including `NaN`, `Infinity` and `-0`.
- The added numeric types are `int` (meaning i64) and `i8 i16 i32 i64 u8 u16 u32 u64 f32 f64` (`f64` is `number`), declared in a global prelude.
  - They start as aliases of `number` in the prelude, so the editor accepts them immediately.
  - The numeric rules below are enforced by the PTS semantic pass, and later surfaced in the language server (M7).
  - To the checker each is a number type of its own (D-048), so it can be an array element, a `Map` or `Set` key or value, a parameter or result of a function type, or a type argument (`u8[]`, `Map<string, int>`, `(n: int) => int`), and inferred types keep it: `ints.map(x => x * 2)` is an `int[]`. tsc relates every numeric type to every other, as it relates `number` to itself, so overloads match as they would for `number`: on a `u8[]`, `reduce(f, 0 as int)` takes `reduce`'s `T` overload (and fails the conversion rule); write `reduce<int>(f, 0)`.
- **Literals take their type from context.** `const x: u8 = 200` is fine; `const y = 5` is `number`, as in TS. So does an array literal's element or an argument (`bytes.push(7)` for `bytes: u8[]`); without a context a literal is a `number`, so `[byte, 1]` is a `number[]`.
  - An integer literal is checked exactly against its type's range, from its source text, so a 64-bit value keeps every digit (`const b: u8 = 256` is PTS2002, D-012).
  - A literal with a fraction next to an integer operand makes the operation a `number` (`n * 1.5`).
  - Constant expressions are evaluated at run time, so they wrap rather than fail to compile: `const b: u8 = 200 + 56` is 0. A literal operand of `as` is a `number`, so `9007199254740993 as int` is 9007199254740992; annotate instead (`const x: int = 9007199254740993` is exact).
- **Implicit conversions are lossless only.**
  - Allowed implicitly: any integer type and `f32` to `number`; a smaller integer type to a larger one of the same signedness; an unsigned type to a strictly larger signed one (`u8` to `i16`). Not an integer type to `f32`, nor a signed type to an unsigned one (PTS2001).
  - Anything lossy needs `as`.
  - A function converts to a function type only if the type's parameters convert to its own and its result to the type's, losslessly: a `(b: u8) => void` can't be a `(n: number) => void`.
  - The operands of an operator convert to their common type: the wider of two compatible types, or `number` when an integer type meets `number` or `f32`. So mixing `int` and `number` in arithmetic yields `number`. Two integer types with no lossless common type (`i64` and `u64`) are PTS2003.
- **`as` between numeric types converts the value**, following Rust's rules: float to int truncates toward zero and saturates at the target type's range, and `NaN` becomes 0. Between integer types `as` wraps (modulo 2^n).
- **Integer arithmetic.**
  - Overflow wraps (two's complement), unary minus on an unsigned value included; `MIN / -1` is `MIN` and `MIN % -1` is 0.
  - Integer `/` is truncating integer division.
  - Integer `%` takes the sign of the dividend, as in JS and Rust (D-O17).
  - Integer division or modulo by zero throws `RangeError: Division by zero`; an integer `**` with a negative exponent throws `RangeError: Exponent must be non-negative` (D-012).
  - `f32` arithmetic is C's `float`; its `%` and `**` go through `double`.
- **Bitwise operators** (D-018): on `number` they have JS's int32 semantics exactly. On an integer type they work on the type's own bits: `&`, `|`, `^`, `~` as in C, `<<` wraps, `>>` is arithmetic for signed types and logical for unsigned ones, `>>>` shifts the bits as unsigned and keeps the type (`-16 as i32 >>> 0` is -16), and the shift count is taken modulo the type's width. The operands convert to their common type first, so `u8 << int` is an `int`, and an integer type meeting a `number` gives the int32 operation on numbers.
- `typeof` on any numeric type returns `"number"`.
- 64-bit integers keep every digit when printed, converted to strings (`toString(radix)` too) and in JSON (D-012, D-080, D-083). As a dynamic value (§3.4) an `int`, `i64` or `u64` is a number while its magnitude is at most 2^53 and a boxed integer beyond.
- Enum members (§3.13) and typed arrays' elements (§7.2) are `number`s: they convert to an integer type only with `as`.
- **Number-to-string conversion matches JS exactly**, using shortest round-trip formatting (Ryu, D-011) with the JS rules for choosing exponent or fixed notation. For example, `0.1 + 0.2` prints `0.30000000000000004`.

`docs/examples/spec_integers.pts`:

```ts
// SPEC §3.2: the integer types. Lossy conversions need `as`: `const b: u8 = half`
// would be PTS2001 ("a int doesn't convert to u8 implicitly").
const big: u8 = 200;
const sum = big + (100 as u8); // u8 arithmetic wraps
const half: int = 7 / 2; // integer division truncates
const mixed = half * 1.5; // an int with a fraction is a number
console.log(sum, half, mixed);
console.log(300.7 as u8, -1 as u8, NaN as int); // `as` truncates and saturates; NaN is 0
const bytes: u8[] = [1, 2, 3];
const doubled = bytes.map((b) => b * 2); // a u8[]
console.log(doubled, typeof doubled[0]);
const huge: int = 9007199254740993; // exact: every digit of an i64
console.log(huge + 1, (-16 as i32) >>> 0, (0xf0 as u8) >> 4);
try {
  console.log(half / (0 as int));
} catch (e) {
  console.log(String(e));
}

// expect:
// 44 3 4.5
// 255 0 0
// [ 2, 4, 6 ] number
// 9007199254740994 -16 15
// RangeError: Division by zero
```

### 3.3 Booleans and strings

- Strings are immutable, stored as UTF-8, and each one records whether it is pure ASCII.
- A lone surrogate, which JS strings may hold, is stored as its 3-byte (WTF-8) encoding, and concatenation joins two halves into one character, so string equality is byte equality; output writes a lone surrogate as U+FFFD, as Node does (D-014).
- Indexing keeps JavaScript's semantics so ported code behaves identically: `.length`, `s[i]`, `charCodeAt` and `slice` indices count UTF-16 code units.
  - ASCII strings take an O(1) fast path.
  - Non-ASCII strings are scanned from the start (O(n)) for now; a lazily built index is planned (D-016).
- `<`, `<=`, `>`, `>=` compare strings by UTF-16 code units, as JS does.
- Building a string piece by piece (`s += x` in a loop) takes linear time: a long string's bytes go to an append buffer that later concatenations extend in place (D-061).
- Case mapping, `localeCompare` and `normalize` use tables generated from Node's ICU (Unicode 17; D-016, D-082, D-135). Reading `s[i]` out of range follows §8.

### 3.4 `any` and `unknown`

`any` is a **checked dynamic value**.
- Anything is assignable to it.
- Inside the dynamic world, operations follow JavaScript semantics. Property access, calls, indexing and operators are resolved at runtime using type descriptors:
  - a missing property reads as `undefined`;
  - calling a non-function throws `TypeError`;
  - `+` concatenates when either side is a string.
- The check happens at the boundary into typed code. Assigning an `any` to a typed location verifies the value at runtime and throws `TypeError` on mismatch: `const n: number = parsed.count` throws if `count` isn't a number.
- This keeps `JSON.parse(s).user?.name` working while making `any` honest.
- What works (D-070 … D-079, D-089, D-091): reads, writes, `in` and destructuring of properties and elements; the operators (with JS's conversions: ToPrimitive, ToNumber, IsLessThan), `typeof`, equality; calls of functions and methods, whose arguments are checked as they enter typed code (missing ones are undefined, extra ones are ignored); the builtin methods of strings, numbers, booleans, arrays, maps, sets, dates, RegExps and functions; `Object.keys`, `values`, `entries` and `hasOwn`; `for…of`, spreads and array patterns, which iterate an array or tuple (live, by index), a string's code points, a set's elements or a map's entries, and throw `TypeError: x is not iterable` for anything else (D-078).
- Checked conversions reach every type: primitives, integer types, classes, interfaces and object types (with methods too), arrays, tuples, hash-map objects, maps, sets, function types and unions of them. A typed object whose declared types fit the target stays the same object; dynamic data (values of `any`, `any[]`, hash-map objects of `any`), and typed objects whose declared types don't fit, are copied into the target's representation (§8). A function converts to a function type as it is if its signature is the type's, and otherwise through an adapter that checks its arguments and result when it is called (D-075). A write through `any` is checked against the place's declared type: `TypeError: Cannot set property p of C: expected T, got U` (D-072).
- Not yet through `any`: the iterators of maps and sets (`m.keys()` throws `TypeError`), typed arrays' methods that take callbacks (`TypeError`), and `.par` views.
- Every runtime type therefore carries a descriptor (name, field names mapped to offsets, methods). The same descriptors drive `console.log` and JSON.
- Representation (D-002): a dynamic value (an `any`, an `unknown`, a union) is one 64-bit word, encoded as JavaScriptCore encodes its values: an object is its raw pointer (so the collector sees it), a double is its bits plus 2^49 (`NaN` canonicalised), and `null`, `undefined`, `false` and `true` are small constants; a 64-bit integer beyond 2^53 is boxed. It isn't NaN-boxing with tagged pointers, and never a separate tag and payload, so it can't tear under races (§4.1).

`unknown` works exactly as in TypeScript: you must narrow before use. Narrowing an `unknown` to a class or a primitive (by `instanceof`, `typeof` or a type guard) and `as` from it are checked at runtime and throw `TypeError` when wrong (D-068). `x as any` and `x as unknown` box the value; `{}` written as a type, and the `object` type, are dynamic values too (D-089, D-091).

### 3.5 Casts

`as` on non-numeric types:
- Upcasts are free.
- Downcasts and casts to unrelated types are checked at runtime and throw `TypeError`; to an array or object type that isn't the value's own, `as` makes a checked copy, as converting from `any` does (D-089).
- `x!` is checked too, and throws `TypeError: Non-null assertion on null` (or `undefined`). Where there is no value to assert, `a.pop()!`, `a.shift()!`, `find(…)!`, `findLast(…)!` and `map.get(k)!` throw `RangeError` (D-038, D-042, D-046).
- `as unknown as T` cannot be used to lie about a value's type.
- `as const` and `as T` on an array or object literal give the literal that type, such as a tuple (D-053). `x satisfies T` is `x` (D-091).

### 3.6 Objects

- **Classes** have fixed layouts and use vtables; a method call is virtual only where a subclass overrides the method, otherwise direct (D-028). Supported (D-026, D-028, D-030, D-031): single inheritance (`extends`, `super(...)`, `super.m()`, also across modules), fields with initialisers, constructors, methods, getters and setters, parameter properties, static fields, methods, accessors and blocks, `private`, `protected` and `readonly` (checked by tsc only), `#private` fields and methods (`#x in o` for an `o` of an object type; of a dynamic value it is not supported yet), abstract classes, methods and fields, `instanceof`, `new.target`, `o.constructor.name`, generic classes (§3.7), and classes extending `Error` and its subclasses (§3.9). An override may leave out trailing parameters and return an instance of a subclass of what the overridden method returns (D-091).
  - Not yet (PTS1001): classes declared inside functions or blocks; class expressions and classes as values (`const K = Point`, a parameter of type `typeof Point`, `this` as a value in static code); extending builtins other than the error classes (`Map`, `Date`, `RegExp`, typed arrays); auto-accessors (`accessor x`); members with computed or quoted names, other than `[Symbol.iterator]` and `[Symbol.asyncIterator]`; index signatures in classes; an override that changes a parameter's type or leaves out a parameter with a default; a getter and a setter of different types; reading a property that has only a setter; assigning an inherited static field through a subclass; calling a static method that uses `this` through a subclass.
  - Decorators aren't supported yet (PTS1001).
- **Interfaces and object types stay structural**; tsc checks them. At runtime, a value used through an interface type dispatches through a table of field offsets and method pointers, one per (concrete type, interface) pair. Tables are built at compile time where possible and cached at runtime otherwise.
  - How (D-035, D-036): each descriptor lists its fields and its methods' entry points; each place that reads, writes or calls through an interface caches, in one word, the last descriptor it met and the field's offset or the method's index.
  - Interfaces and object types may declare methods and accessors, which class methods, getters and setters and object literals' members provide. A class or object provides a property only with the same representation: an `int` field doesn't provide a `number` property (PTS1001).
  - Optional properties (`p?: T`, and mapped types: `Partial<T>`, `Required`, `Pick`, `Omit`, `Readonly`, `Record<"a" | "b", T>`) are fields of `T | undefined` that an object may leave absent: reading one gives undefined, `console.log` and `Object.keys` skip it and `"p" in o` is false (D-065). An object that lacks an optional property, or holds it without undefined, can still be used as the type (D-066, §8).
  - Not yet: optional methods and accessors, overloaded methods in object types, a property with a setter and no getter.
- **Object literals** become instances of anonymous struct types derived from their checked type ("shapes", shared by structurally identical types, with one descriptor per property order so that printing keeps the literal's order; D-033). Optional properties are stored as `T | undefined`. Literals may hold methods (closures that take the object as `this`), getters and setters (D-044, D-045). Spreads and rest properties are in §8 (D-058, D-108). Not yet: computed property names (other than in hash-map objects), methods or accessors beside a spread.
- **Properties cannot be added or deleted after construction.** `delete` only works on index-signature types (PTS1002 elsewhere). `Record<string, T>` and `{ [k: string]: T }` are hash maps under the hood:
  - *Hash-map objects* (D-059, D-093, D-097) are `Record<string, T>`, `Record<number, T>`, index-signature types (with required properties of the signature's value type too), object types whose property names are all array indices (`Record<Level, number>` for a numeric enum `Level`), and `Partial<Record<K, V>>`. They are ordered hash tables with string keys, listed in JS's order (array-index keys first, in numeric order, then the others in insertion order). Reading a missing key follows §8; `in`, `delete`, `Object.keys`/`values`/`entries`, `for…in`, JSON and `console.log` treat them as JS treats plain objects.
  - Not yet: index signatures with other key types (template patterns, symbols), index signatures mixed with methods, optional properties or properties of other types, recursive hash-map object types, methods in their literals, `toString` of one, and using an object of another type as one (its copy `{ ...o }` is one).
- **`null` and `undefined`** remain distinct values. Optional chaining (`?.` on properties, calls and elements, short-circuiting the rest of the chain) and `??`, `??=`, `||=` and `&&=` are supported (D-063, D-064).
- `Object`'s functions are in §7.2.

### 3.7 Unions and generics

- **Unions** are tagged values. Narrowing works with every form: `typeof`, `instanceof`, `in`, discriminant properties, equality and user-defined type guards. The checker does the reasoning; the backend emits tag tests.
  - How (D-062, D-067): a union is a dynamic value, one word whose encoding is its tag (§3.4, D-002), so it can't tear under races. A union of literals of one type is that type (`"a" | "b"` is a `string`); a union with an `any` or `unknown` member is dynamic.
  - Narrowing to a class is checked (`TypeError` if a type guard was wrong, §8). Members that a union's object types share are read and called through an interface made for the access; members of a union of objects and other values (`x.length` for `string | string[]`) are found at run time (D-089).
  - Not yet: unions of two numeric types (`u8 | i16`; literal unions are fine), writing a property that the members hold as different types, a union's method as a value.
- **Generics** are monomorphized.
  - A generic function has one instance per distinct list of type arguments, inferred or written, as their runtime types (D-049): `identity(1)` and `identity(1.5)` share `identity<number>`, while `identity(byte)` with `byte: u8` is `identity<u8>`, whose values keep their type.
  - Arithmetic on a value of a type parameter is done in `number`, as tsc types it, even when the instance's type argument is an integer type; `x as T` converts back by the numeric rules.
  - Generic interfaces and type aliases are instantiated like any other type (`Box<int>`).
  - A generic class has one class per instance (D-050), printed with the generic class's name (`Box { value: 1 }`); instances may extend classes and be extended (`class IntBox extends Box<int>`). Static members of a generic class are shared by all its instances, and `x instanceof Box` tests for any of them (D-089).
  - Generic methods of classes, and generic functions nested in functions or methods, have instances too (D-051). A generic method may be abstract or override a generic method; calls of such methods go by name to erased copies (D-101).
  - A function that would need ever more instances (`f<T>` calling `f<T[]>`) is a compile error after 64.
  - Generic functions as values (D-078): a generic function declaration where tsc's context is a function type that isn't generic is the instance for the type arguments that context implies (`[1, 2].map(identity)`, `identity<number>` too); anywhere else (`const id = identity`), and every generic arrow function or function expression, it is an *erased copy*, one function whose type parameters take dynamic values: inside it, a value whose type mentions a type parameter is `any`, with `any`'s run-time checks, and a call of it converts its result to the type tsc gives the call with a check (`TypeError: Expected T, got U`). Arrays and objects pass to erased copies by reference. An erased copy that makes an instance of a generic class makes the instance for `any` (`Box<any>`), which doesn't convert to another instance (`Box<number>`) (a known issue).
  - A module-scope `const` holding a generic arrow function or function expression that makes instances of generic classes, or returns objects of a generic type (`List<T>`), is monomorphized where it is called by name, as a function declaration is (D-102, D-106).
  - Generic methods (D-079): an interface's or object literal's generic method has its erased signature and is called with its result checked; a class's generic method is monomorphized for calls through the class, and its erased copy serves calls through interfaces and `any`; a generic method read as a value is the instance its context implies or the erased copy, bound to the object. An object that came through `any` with methods of other signatures than an interface's (an erased copy's `Box<any>` as a `Container<number>`) has them called through their dynamic entries, with checked results.
  - Not yet: generic functions declared inside generic functions or inside methods of generic classes, nested generic functions that use their outer function's variables, overriding a plain method with a generic one or a generic method with a plain one, generic accessors.

### 3.8 Equality

- `===` compares objects by reference identity, and strings and numbers by value, with JS's `NaN` rules.
- `==` is supported with its JS meaning for the types involved; in practice it is mostly `x == null`. With a union operand it is JS's IsLooselyEqual (D-067): null equals undefined only, booleans compare as numbers, strings convert as `Number(s)` does, and an object meets a primitive through its class's `valueOf()` if that returns a primitive, else its string (D-088).
- `Object.is` is SameValue; `Map` and `Set` keys and `includes` use SameValueZero.

### 3.9 Exceptions

- `throw`, `try`, `catch` and `finally` have JS semantics, and any value can be thrown. The catch binding is an `unknown` (D-068); `catch (e: any)` and `catch {}` work, while destructuring the catch binding is not supported yet.
- `Error` and its subclasses (`TypeError`, `RangeError`, `ReferenceError`, `SyntaxError`, `EvalError`, `URIError`, and `AggregateError` with `errors`, D-113) have `message`, `name`, `cause` and `stack`, and programs extend them (`this.name = …` or a `name` field, their own `toString`). `stack` is `Name: message`, then up to 10 frames `    at name (file.pts:line)` where the error was made (D-069): methods as `Class.method`, constructors as `new Class`, top-level code as `    at file.pts:line`, and code in a `pts:` module by the module's name (`pts:process:48`). A class derived from Error can't override `stack` yet.
- The errors of `pts:fs` are Node's system errors: an `Error` with `errno`, `code`, `syscall`, `path` and `dest`, typed `NodeJS.ErrnoException` (D-117).
- Errors the runtime detects (integer division by zero, checked reads, failed `!` and casts, `ReferenceError` before a binding is ready) are thrown as these classes and can be caught. Running out of memory can't be: it ends the program with `Uncaught RangeError: Out of memory …` and exit code 1.
- Unbounded recursion throws `RangeError: Maximum call stack size exceeded` (D-069), and so does a regular expression that exceeds PCRE2's backtracking limit (D-132).
- **Uncaught errors** (D-O07):
  - An uncaught exception prints `Uncaught ` and its stack (`<Name>: <message>` and its `.pts` frames) to stderr, then exits with code 1. Any other value prints as console.log shows it nested: `Uncaught 'text'`. An exception out of a timer, an immediate, a microtask or a task on another loop is uncaught the same way (D-114, D-121).
  - An unhandled rejection behaves like Node's default `--unhandled-rejections=throw`: once the microtasks have run, it ends the program as an uncaught exception does (D-112).
  - A top-level `await` that never settles prints Node's warning and exits with code 13, as Node does (D-113).

### 3.10 Not supported

Each of these gets a clear compile error explaining why (PTS1002, D-020; `with` is tsc's TS1101):
- `eval`, `new Function` (any use of the global `Function`, its type included), `with` (a strict-mode error, since every file is a module);
- `var` (the error suggests `let` or `const`);
- `arguments` (the error suggests a rest parameter);
- prototype manipulation: `__proto__`, `Object.setPrototypeOf`, assigning to `.prototype` or its members;
- `Proxy`, `Reflect`;
- declaration merging (function overloads, and interfaces that merge only with interfaces, are fine);
- namespaces (`namespace` and `module` declarations), until there's a reason for them; the error suggests ES modules;
- `bigint` types and literals: "BigInt is not supported yet" (D-O08); `BigInt64Array` and `BigUint64Array` too (PTS1001);
- `Symbol` and the `symbol` type, other than `Symbol.iterator` and `Symbol.asyncIterator` (D-O14);
- `delete` of anything but an entry of a hash-map object (§3.6);
- importing `pts:internal/…` outside ParallelScript's library (D-118).

Decorators and `WeakRef` can come later (D-L12): `WeakRef`, `WeakMap` and `WeakSet` are PTS1001 now, and so are decorators (§3.6).

### 3.11 Functions

- Function declarations (hoisted; nested ones to the top of their block), arrow functions, function expressions (a named one sees itself) and methods are values of function types (D-041): closures that capture variables by value, or in a shared cell when the variable may change after it is captured. A `for` loop's `let` variables are fresh per iteration; an arrow takes its method's or constructor's `this`, and `super.m()` works in it.
- Parameters may have defaults (evaluated in the callee's scope when the argument is left out or `undefined`), be optional or be rest parameters (of array or tuple types), and be destructuring patterns (D-054, D-055, D-057, D-063, D-091).
- `f.call`, `f.apply` and `f.bind` (with leading arguments), `f.name` and `f.length` (D-044, D-045, D-098). A method read as a value is bound to its object (§8).
- A function value converts to another function type when its parameters and result do (losslessly for numbers, §3.2); one that takes fewer parameters, or returns a value where the type returns nothing, goes through an adapter, except an arrow function or function expression written in place, which takes the context's shape (D-041).
- Function overloads (declarations and class methods): the implementation runs, and its result converts to the chosen overload's result type with a check (D-117).
- `console.log` shows functions as Node does: `[Function: f]`, `[Function (anonymous)]`, `[Function: bound f]`, `[GeneratorFunction: g]`, `[AsyncFunction: f]`, `[AsyncGeneratorFunction: f]`.
- Not yet: `this` parameters; overloaded and recursive function types; converting a function to a string; spreading an array into parameters other than a rest parameter (a tuple's elements may be spread into any), and spreading a set or a string into arguments; spreads in `call`, `apply` and `bind`; a callback that returns nothing passed to `map`, `flatMap` or `reduce`; the `thisArg` argument of array methods; a method of a string, an array or a map read as a value without a call (`s.trim`, `xs.push`), though Math's functions, `String`, `Number`, `Boolean` and the `Number.is…` tests are values (D-083, D-084, D-089).

### 3.12 Arrays and tuples

- `T[]` holds its elements unboxed in their own representation (numbers as doubles, a `u8[]` as bytes, objects as pointers; D-038), and arrays are race-safe (§4.1). An array can't be given holes, but it can hold them: `new Array(n)` leaves holes in arrays of strings, objects and dynamic values (D-085), a match array has one for each group that took no part (D-133), and racing pushes can leave one (D-130). A hole reads as undefined where any value can go; reading out of range follows §8.
- An array converts to an array type of other elements (`Dog[]` as `Animal[]`) by a copy, each element converted with a check, only where the copy can't be told apart from the array: a new array, a `const` whose only use is the conversion, or an argument that the callee only reads. Elsewhere it is PTS1001 ("… where it may be changed or kept", D-094).
- Tuples of fixed length are objects with typed, unboxed elements, printed as arrays (D-053); optional elements (`[string, number?]`) work, and a tuple type with a rest element (`[number, ...number[]]`) is an array (D-100). An index into a tuple must be a constant; a tuple used with the array methods or `for…of` is copied into an array (D-089).
- Destructuring (with defaults, rest elements and nesting) and spreads take arrays, tuples, strings, maps, sets and iterables (D-054, D-058, D-110).
- Typed arrays and `ArrayBuffer` are in §7.2.
- Not yet: recursive array types (`type Nested = Nested[]`), `flat` with a depth that isn't a constant, `copyWithin`, `for…of` without a declaration of its variable.

### 3.13 Enums

- Numeric, string and `const` enums compile to their members' constants (`E.m`, `E["m"]`, also across modules); an enum type is the number or string type of its members, so a numeric member converts to an integer type only with `as` (D-052).
- `E[n]` of a numeric enum is the reverse mapping, `E[E.m]` included (D-099); a value no member has follows §8.
- An enum object used as a value (`console.log(E)`, `Object.keys(E)`) is a hash-map object of its members and of the reverse mappings (D-089), a new one at each use (§8).
- Not yet: members whose values aren't constants (`A = s.length`).

### 3.14 Generators and iteration

- Generator functions (`function*` declarations, expressions, and methods of classes and object literals) with `yield` and `yield*`, and generator objects with `next`, `return` and `throw`, where `return()` runs `finally` blocks (D-109). They compile to state machines over collected frames (§5.2).
- The iteration protocol (D-110, D-111): `for…of`, spread, `Array.from`, `new Set`/`new Map`, array patterns and `yield*` take arrays, strings, maps, sets, typed arrays, generator objects and any object whose `[Symbol.iterator]()` method gives an iterator (a generator object, or an object with `next()` and, optionally, `return()` and `throw()`), and call `return()` when a loop ends early. `Iterable<T>`, `Iterator<T>` and `IterableIterator<T>` values are dynamic values. The iterators of maps, sets, arrays and strings are values, live as JS's are (`m.keys().next().value`).
- A `yield` may be anywhere in an expression; the operands JS evaluates before it are kept in the generator's frame (D-111, §8).
- Async generators and `for await` are in §5.2.
- Not yet: a `yield` (or `await`) in a loop's condition or update, a case label, a declaration's variables after the first, a default value or an optional chain; `yield*` in async generators; iterator helpers (`it.map(…)`); a static or generic `[Symbol.iterator]`, and reading `x[Symbol.iterator]` without calling it.

---

## 4. Memory and the shared heap

- Memory is garbage collected by **bdwgc** (Boehm–Demers–Weiser) in thread-aware mode with thread-local allocation and parallel marking.
- Objects are allocated with precise layouts: each object's descriptor holds bdwgc's mark descriptor, so numbers in fields are never taken for pointers (D-024; an object of more than 62 words is scanned whole). Pointer-free data, such as string bytes and numeric arrays, is allocated with the atomic (no-scan) allocator. Stacks are scanned conservatively.
- The runtime's defaults (D-060, D-112, D-131): the collector runs once the program has allocated as much as the heap holds (free-space divisor 1, as Go's GOGC=100 does), the heap starts at 4 MiB, and marking uses 2 threads. bdwgc's `GC_FREE_SPACE_DIVISOR`, `GC_INITIAL_HEAP_SIZE` and `GC_MARKERS` override them.
- A precise collector, and later a generational one, is planned (M8).

### 4.1 The race-safety invariant (non-negotiable)

A program with data races may observe stale or surprising values, but it must never crash the runtime, corrupt the heap, or read or write out of bounds (Java's guarantee, not Go's). Concretely:

- **No torn values.** Any value in a heap slot that can be read concurrently is one machine word (a pointer, a small scalar or a box), or is written and read atomically. No multi-word values that can tear in shared fields. The C-level rules that give this (which accesses are atomic, which are plain one-word ones and what that assumes of clang and x86-64, reading a slot once before checking it, no `memcpy` of shared numbers, state one thread owns) are the shared-memory contract in `docs/ARCHITECTURE.md` §4.13 (D-143).
  - Typed arrays' elements are read and written by relaxed atomic accesses of their width, and the methods that copy or move them in bulk (`set`, `slice`, `copyWithin`, `reverse`, `sort`, construction from another typed array, `ArrayBuffer`'s `slice`) copy by relaxed atomic units of the element's size or wider, so racing threads may see a stale number in an element, never a torn one or bytes outside the view (D-125, D-143). Views of one buffer with different element types see each other's bytes; an element is whole only to views of its own width or narrower.
- **No out-of-bounds writes.** Growable arrays keep their capacity inside the backing buffer, and bounds checks use the length and capacity of the buffer actually being accessed. Racing `push`es can lose elements but never write past the end.
- **Strings are immutable**, so sharing them is free.
- What races look like (D-130): a racing operation may throw `RangeError` where a sequential one couldn't (a `pop` of an array another thread emptied, a write past a length another thread shrank); a push lost to another can leave a hole, which reads as undefined where any value can go and throws `RangeError` where an element is required; a map, set or hash-map object that threads change at once may lose entries, hold a key twice and report a size that differs from its entries (never below 0). Share tables through `pts:sync`'s `Mutex`. `pts/tests/stress/race_*` test the invariant.
- **Generators and iterators shared by threads** (D-143): one thread at a time runs a generator's body; `next()`, `return()` or `throw()` while another thread runs it throws `TypeError: Generator is already running`, as a resume from its own body does. Each resume's value goes to the thread that asked for it, though threads racing on a generator or a runtime iterator (over an array, a tuple, a map or a set) may skip or repeat items. An async generator runs on the loop that made it: requests from other loops queue there in order, so each item goes to exactly one request.
- **Promises shared by loops** (D-121, D-143): a handler that another loop adds counts from the moment it is added, so a rejection that its home loop settles meanwhile isn't reported as unhandled.
- New objects are published to other threads by plain stores, which x86-64 never reorders with the stores that filled them; a port to another architecture needs release stores there (D-130).

---

## 5. Concurrency

### 5.1 The model

- The runtime starts one event loop per core. The count is configurable with `PTS_THREADS`.
- Each loop runs on its own OS thread, and all loops share one heap.
- Each loop is a libuv `uv_loop_t` with its own timers, immediates and microtask queue; `uv_async_send` wakes a loop from another thread. `pts:fs/promises` runs its system calls on libuv's threadpool and settles their promises on the loop (D-118). Sockets come with `pts:http`'s server (D-136): its connections are libuv TCP handles on the loop that accepted them (the loop that called `serve`, or with `reusePort` any loop, D-137), and a loop flushes stdout before it waits for I/O; there is no API for DNS, client sockets or child processes yet.
- How (D-121): the main thread's loop runs the program's modules; the other loops start the first time `parallel` runs, so a program that never uses it has one thread. `PTS_THREADS` is a positive integer (values above 256 count as 256); without it, or with anything else, the count is the number of cores the process may run on.
- Timers and immediates belong to the loop that made them (D-139). Any thread, another loop's or a pool thread running a `.par` callback, may pass one to `clearTimeout`, `clearInterval` or `clearImmediate`, or call its `ref`, `unref`, `hasRef` or `refresh`: clearing takes effect at once (a callback that hasn't started never starts), `hasRef` sees the new state at once, and the timer's loop applies the rest (its heap, whether it keeps the loop alive, `refresh`'s new due time) when it next takes its messages.
- The program ends when every loop has nothing left to do at once: nothing referenced to wait for (timers, I/O, as in Node) and no task or message waiting for it. A task nobody awaits keeps the program running until it is done.
- `process.exit`, an uncaught exception and an unhandled rejection on any loop end the whole program at once.

### 5.2 Plain `await`

Plain `await` keeps exact JavaScript semantics.
- A task belongs to one loop for its whole life, and a loop runs one task at a time.
- Code between two `await`s therefore runs without interference from other tasks on the same loop.
- For single-loop code, the ordering of microtasks, timers and I/O callbacks matches Node.

Async functions and generators compile to state machines. Locals that live across an `await` or `yield` are stored in a GC-managed frame object; there is no separate native stack per task.

- What there is (D-112 … D-115): async functions (declarations, expressions, arrows, and methods of classes and object literals); `Promise` with ECMAScript's algorithms in V8's job order, thenables of the program's included; async generators, and `for await` over async generators, objects with `[Symbol.asyncIterator]()` and sync iterables of promises; Node's timers and immediates and `queueMicrotask` (§7.1).
- Top-level `await` (and `for await`) works in the entry module; in other modules it is not supported yet (D-113).
- An `await` may be wherever a `yield` may (§3.14). Not yet: an `await` in a loop's condition or update (`while (await next())`), a case label, a declaration's variables after the first, a default value or an optional chain, and in an expression-bodied async arrow unless it comes first (`async () => (await p) + 1`; a block body works).

### 5.3 Task parallelism: `parallel <expr>`

- `parallel e` evaluates `e` as a new task on another loop and immediately returns a `Promise<Awaited<T>>`, where `T` is the type of `e` (the promise is flattened if `e` is itself a promise).
- Captured variables are shared by reference, because the heap is shared.
- `await parallel f(x)` is the common idiom (`docs/examples/spec_tasks.pts`):
  ```ts
  // SPEC §5.3: `parallel e` runs e as a task on another event loop and gives a promise of
  // its value at once.
  // threads: 1 2 4
  function fib(n: number): number {
    return n < 2 ? n : fib(n - 1) + fib(n - 2);
  }
  async function report(name: string): Promise<string> {
    return `${name}: ${fib(20)}`;
  }

  console.log(await parallel report("alice"));
  const results = await Promise.all([20, 21, 22].map((n) => parallel fib(n)));
  console.log(results);
  try {
    await parallel JSON.parse("{");
  } catch (e) {
    console.log("rejected with", (e as Error).name);
  }

  // expect:
  // alice: 6765
  // [ 6765, 10946, 17711 ]
  // rejected with SyntaxError
  ```
- The target loop is picked by load (least-loaded, or round-robin with stealing of tasks that haven't started). A task never migrates once it has started.
- Promises can be awaited from any loop; settling one wakes the loop that is awaiting it.
- With `PTS_THREADS=1`, `parallel e` still creates a new task, on the same loop (D-O06).
- How (D-121):
  - A task goes to the least-loaded loop other than the one starting it, where a loop's load is the tasks placed on it and not finished (ties go round-robin). A loop that runs out of work takes a task another loop has queued but not started before it sleeps.
  - A promise belongs to the loop that made it and settles there; its reactions (an `await`, a `then` handler) run on the loop that added them. So `parallel e`'s promise settles on the loop that ran `parallel`, after e's value (or the outcome of the promise e gives) is known, and an unhandled rejection of it is reported there, as for any promise.
  - A throw in e, synchronous or not, rejects the task's promise.
  - An `await` or `yield` in the operand (outside functions in it) is not supported yet; await before `parallel`, or give it an async function's call.
  - Known gaps: a loop busy with something referenced (a long timer) doesn't take tasks queued on others until it runs dry; awaiting another loop's promise that rejects races with that loop's unhandled-rejection check; an async generator driven from two loops at once runs its body on both.

**Parsing `parallel`** (D-O05):
- `parallel` is the operator only when the next token is on the same line and starts an expression **other than** `(`, `[`, `.`, `?.`, a template, `=`, an operator, `,`, `;`, `)` or `}`. Examples: `parallel foo(x)`, `parallel new X()`, `parallel async () => …`, `parallel await x`, `parallel "lit"`.
- `parallel for (` is the loop statement (§5.5).
- `parallel(expr)` parses as a normal call. The checker reinterprets it as the operator **only if** no binding named `parallel` is in scope and the call has exactly one argument.
- How (D-120): `parallel e` is a call of the prelude's `parallel<T>(task: T): Promise<Awaited<T>>` marked by the parser, so tsc types both forms; the operand is a unary expression, as `await`'s (`parallel f(x) + 1` is `(parallel f(x)) + 1`), or an arrow function. `parallel` followed by `=>`, `:`, `?`, `!`, `++`, `--`, `as`, `satisfies` or `of` is also the identifier. `parallel e` where a binding named `parallel` is in scope, and a function as the operand (whose task would only make the function), are compile errors (PTS1002).

### 5.4 Data parallelism

**The pool.** A work-stealing fork-join pool with Chase–Lev deques, one per worker, sized to the core count and separate from the event-loop threads. A thread that starts a parallel operation helps execute work until the operation finishes. The design follows Rayon (`join`, adaptive splitting).
- How (D-122): `PTS_THREADS` − 1 workers (the count of §5.1), started at the first parallel operation, since the thread that starts one works too; a loop thread gets its own deque then. `join(a, b)` offers b to thieves, runs a, then runs b itself unless it was stolen, in which case it runs other work until b is done; a throw out of either is rethrown once both are done, a's first. Operations over index ranges split with Rayon's adaptive splitter (as many pieces as threads when nothing is stolen; a stolen piece splits again), down to a minimum piece length each operation chooses.
- A callback that a pool thread runs for an operation of loop L works for L: the promises it makes belong to L, and their reactions, its `then` handlers and its `queueMicrotask` jobs run on L once L is free again (after the operation). Timers and asynchronous I/O started from such a callback on a pool worker throw `Error` for now.

**The `.par` view.** `.par` on arrays, typed arrays and ranges returns a parallel view with: `map`, `filter`, `forEach`, `reduce(fn, initial)`, `flatMap`, `some`, `every`, `find` (any match), `count`, `sum`, `min`, `max`, `sort`/`sortBy` (parallel merge sort) and `toArray`.
- These methods are synchronous: they return when the work is done.
- `.par.map` preserves order.
- `reduce`'s function must be associative.
- How (D-123): `xs.par` is a `ParallelArray<T>` of `pts:parallel` (a library written in ParallelScript); `range(start, end).par` a `ParallelRange`. `filter` keeps order; `find` gives any match (the leftmost among the pieces that ran) or undefined; `some`, `every` and `find` stop early; `reduce(fn, initial)` folds elements with `fn(a, b)` from `initial`, which must be `fn`'s identity; `count(predicate)` counts the elements it holds for; `sum`, `min` and `max` take the elements as numbers, as `Number` and `Math.min`/`Math.max` do (0, Infinity and -Infinity for none); `toArray()` copies. `sort(compare?)` is Array's sort (in place, stable, the elements' strings without a comparator) and `sortBy(key)` sorts in place by keys (numbers or strings) computed in parallel, both as a parallel merge sort (D-124); `flatMap` flattens one level, in order. Promise work in callbacks run on other threads (a `then`, `queueMicrotask`) is queued on the calling loop before the method returns.
- A `ParallelArray`'s callbacks take (element, index, array), as Array's do; a `ParallelRange`'s take the integer only, and its `sort(compare)` and `sortBy(key)` make new arrays.
- Typed arrays (D-126): `ta.par` is a `ParallelTypedArray` with the same methods over the elements as numbers; `map` and `filter` make typed arrays of `ta`'s kind (as `ta.map` and `ta.filter` do), `toArray()` a `number[]`, `flatMap` an array; `sort()` sorts in place in numeric order (-0 before 0, NaN last), as the typed array's own `sort` does, and `sort(compare)` and `sortBy(key)` sort in place too.
- `.par` exists on `T[]` and the nine typed arrays, not on `ReadonlyArray` or tuples, and not through `any` (D-123).
- With `PTS_THREADS=1`, `.par` runs sequentially on the caller (D-O06).

**Helpers** (imported from `pts:parallel`, D-123; `.par` itself needs no import):
- `range(start, end)`, the integers `start` … `end − 1` (their integer parts), and `range(end)`, from 0; so `range(0, n).par.forEach(...)` works. A `Range` is iterable and has `start`, `end`, `length`, `toArray()` and `par`.
- `join(a, b)` runs two closures, potentially in parallel, and returns both results as a tuple; it is for recursive divide-and-conquer.

### 5.5 `parallel for`

- `parallel for (const x of iterable) { ... }` is sugar for a parallel `forEach`. The statement finishes when every iteration has.
- Inside the body, `continue` is allowed. `break`, `return`, `yield`, `await` and labelled jumps out of the loop are compile errors.
- If an iteration throws, the remaining iterations are cancelled as far as possible and the first error is rethrown after joining.
- How (D-127): the items are read once: an array or typed array as it is (its length then; a typed array's elements are read live), a `Range` of `pts:parallel` as its integers, any other iterable as `Array.from` would copy it. The body runs on the pool as a closure over pieces of the indices, so it captures what it uses as an arrow function would. A piece runs its indices in runs of 256 and starts no run once an iteration has thrown; the error thrown first is rethrown. Only `for…of` over a declared variable can be `parallel` (PTS3002); `continue` may name the loop's own label; the jumps out (and `for await`) are PTS3002.

`docs/examples/spec_data_parallel.pts` (`.par`, `range`, `join`, `parallel for`, and `pts:sync` from §5.7):

```ts
// SPEC §5.4, §5.5, §5.7: data parallelism on the fork-join pool. Every method returns
// once the work is done; results keep the elements' order.
// threads: 1 2 4
import { join, range } from "pts:parallel";
import { AtomicInt, Mutex } from "pts:sync";

const words = ["apple", "kiwi", "banana", "fig", "cherry"];
console.log(words.par.map((w) => w.length));
console.log(words.par.filter((w) => w.length > 4));
console.log(range(1, 101).par.sum(), range(10).par.max());
console.log(words.par.reduce((a, b) => (a.length >= b.length ? a : b), ""));
console.log(words.slice().par.sortBy((w) => w.length).join(" "));

function fib(n: number): number {
  return n < 2 ? n : fib(n - 1) + fib(n - 2);
}
console.log(join(() => fib(20), () => fib(21)));

// A parallel body can't assign a captured `let` (§5.6): count with an atomic.
const multiples = new AtomicInt();
parallel for (const n of range(0, 1000)) {
  if (n % 7 === 0) {
    multiples.add(1);
  }
}
console.log(multiples.load());

const seen = new Mutex<string[]>([]);
parallel for (const w of words) {
  seen.withLock((list) => list.push(w));
}
console.log(seen.withLock((list) => list.length));

// expect:
// [ 5, 4, 6, 3, 6 ]
// [ 'apple', 'banana', 'cherry' ]
// 5050 9
// banana
// fig kiwi apple banana cherry
// [ 6765, 10946 ]
// 143
// 5
```

### 5.6 The single guardrail

- A *parallel body* is any of: the operand of `parallel`; a `parallel for` body; a function passed to a `.par` method or to `join`.
- Inside a parallel body, assigning to a captured local variable (a `let` declared outside the body) is a compile error. The message suggests `.par.reduce`, `.par.sum` or an `Atomic`.
- The stdlib declarations mark parallel-callback parameters with a branded function type, so the check knows which closures run in parallel.
- Mutating object fields from parallel code is allowed.
- **No other parallelism checks** are added without asking the owner.
- How (D-127): PTS3001, lexical: an assignment (`=`, compound, `++`/`--`, a destructuring or `for…of` target) anywhere in the body, functions inside it included, to a `let` or parameter declared outside it. A function passed by name (a function declaration or a const's arrow function) to a `ParallelCallback` parameter is checked too. `parallel(e)` is the operator here as it is when it runs (D-O05). The check doesn't follow calls: a function called from the body that assigns an outer variable isn't reported. Nor does it cover `pts:http`'s `fetch` handler, which runs on several loops at once with `reusePort: true` (D-137): it is an option's property, not a parallel-callback parameter, and covering it would be a new check.

From `pts/tests/errors/parallel_guardrail.pts`:

```ts
let count = 0;
const xs = [1, 2, 3];
xs.par.forEach(() => {
  count++; // error PTS3001: `count` is declared outside this parallel body, which runs on several threads at once, so assigning it here would race; combine the results with `.par.reduce` or `.par.sum`, or use an `Atomic` from pts:sync
});
```

### 5.7 `pts:sync`

- `Mutex<T>`: a blocking `withLock(fn)` for short critical sections, plus an async `lock()` that doesn't block the loop.
- The `AtomicInt` / `Atomic<int>` family: `load`, `store`, `add`, `compareExchange`.
- `Channel<T>`: bounded or unbounded, async `send` and `recv`, `close`, and iterable with `for await`.
- `Once`.

A concurrent map may come later if benchmarks or examples want one.

- How (D-128): `pts:sync` is a ParallelScript library over intrinsics that keep atomic cells and lock words in small ArrayBuffers. `new AtomicInt(value = 0)` holds an `int` (i64), sequentially consistent; `add` wraps and, like `compareExchange`, returns the value before (as JS's `Atomics` do); `Atomic<int>` is `AtomicInt`. `new Mutex<T>(value)`: `withLock(fn)` runs `fn(value)` holding the lock, blocking the thread while it waits, and returns fn's result; `lock()` gives a promise of a `MutexGuard<T>` (`value`, `unlock()`), waiting without blocking the loop; a release passes the lock to the longest-waiting `lock()` first; a guard used after `unlock()` throws `Error`. `Once.run(fn)` runs fn the first time (a concurrent call blocks until it is done; if fn throws, it hasn't run); `done` tells whether it has.
- `Channel<T>(capacity = Infinity)` (D-129): `send(v)` resolves once a receiver has v or it is queued, waiting while `capacity` items are queued (with capacity 0, until a receiver takes it); a capacity below 0 is a `RangeError`; `recv()` resolves to the next item, or undefined once the channel is closed and empty; `close()` lets receivers drain what is queued and rejects later sends and those waiting (`Error: Send on a closed channel`); closing twice does nothing; `closed` tells; `for await (const x of channel)` takes items until it is closed and empty. Items go to receivers in the order they were queued; waiting receivers and senders are served first come, first served.
- Not yet: `Atomic<T>` of integer types other than `int`, a `select` over several channels, `trySend`/`tryRecv`. `recv()` can't tell an undefined item from the end (use `for await`, or an item type without undefined). A `withLock` on a loop's thread while a `lock()` holder on the same loop awaits deadlocks, as any blocking lock would.

---

## 6. Modules

- ES modules with relative imports, in every static form: named, default and namespace imports, side-effect imports, `export` declarations and lists, re-exports, `export *`, `export * as ns`, `import type` (§1 for file extensions).
- Standard library modules use the `pts:` prefix: `import { Mutex } from "pts:sync"` (§7.3). Bare specifiers and `node:` modules don't resolve (tsc errors).
- No npm or `node_modules` in v1. Compiling pure-TS npm packages that fit the supported subset is a stretch goal.

Semantics (D-022), as in Node:
- Every `.pts` (or `.ts`) file reachable from the entry by imports is part of the program, and its top-level code runs once. Modules run in ES module evaluation order: a module after the modules it imports, in the order its `import` and `export … from` declarations name them. In an import cycle, a module that is already running is skipped, so the module that imported it runs first; that module can call the other's functions, but reading its `let`/`const` bindings before their declarations have run throws `ReferenceError: Cannot access 'x' before initialization`.
- Every import runs its module, even one that names only types; `import type` and `export type … from` don't.
- Imports are live bindings: a module sees later assignments to a `let` it imports, also through `import * as m` (`m.x`) and re-exports (`export { x as y }`, `export *`, `export * as ns`).
- `export default <expression>` stores the expression's value when the statement runs; a later assignment to a variable it named doesn't change the default export (unlike `export { x as default }`).
- A `pts:` module is compiled into the program when a module imports it; `pts:process` also when the program uses the global `process`, which runs it before every other module, and `pts:parallel` when it uses `.par` (D-116, D-123).
- Top-level `await` only in the entry module (§5.2).
- Not supported yet: a module namespace object as a value (`console.log(m)`), dynamic `import()`, `import defer`.

---

## 7. Standard library

### 7.1 Globals

- `console`. Output matches Node's `util.inspect` for primitives, arrays, plain objects, class instances (like `Point { x: 1, y: 2 }`), `Map` and `Set` (defaults per D-O16: depth 2, `breakLength` 80, Node's grouping heuristics), and Node's `util.format` for format strings (`%s`, `%d`, `%i`, `%f`, `%j`, `%o`, `%O`, `%c`, `%%`). `log`, `info`, `debug` and `dirxml` write to stdout, `error` and `warn` to stderr; `dir` (with `depth` and `showHidden`), `table`, `group`/`groupCollapsed`/`groupEnd`, `assert`, `count`/`countReset`, `time`/`timeEnd`/`timeLog` and `trace` work as Node's (D-088). `console.dir`'s options must be an object literal of `depth` and `showHidden`.
- `Math` and `JSON`: every function and constant of Math, the transcendental ones computed as V8 computes them, and `random` as xorshift128+ per thread (D-084); `JSON.parse` with a reviver and `JSON.stringify` with a replacer (function or property list) and an indent, with V8's error messages (D-080, D-092). `Math` itself as a value is not supported yet.
- `Promise`, including `all`, `allSettled`, `race`, `any` and `withResolvers` (and `then`, `catch`, `finally`, `resolve`, `reject`; D-112, D-113). `Promise.try` is not supported yet.
- `setTimeout`, `setInterval`, `setImmediate`, `clearTimeout`, `clearInterval`, `clearImmediate` and `queueMicrotask`, as Node's: the timers return a `Timeout` with `ref`, `unref`, `hasRef` and `refresh` (D-114).
- `performance.now()`: milliseconds since the program started.
- `process`: `pts:process`'s default export (§7.3).
- `parseInt`, `parseFloat`, `isNaN`, `isFinite` (D-083); `encodeURI`, `encodeURIComponent`, `decodeURI`, `decodeURIComponent` (D-092); `String`, `Number` and `Boolean` as conversions and as values (`xs.map(Number)`, `xs.filter(Boolean)`); `NaN`, `Infinity`, `undefined`; `Symbol.iterator` and `Symbol.asyncIterator`.
- `parallel` (the operator, §5.3).
- `fetch` (planned, M6.3, with `pts:http`): `http://` over HTTP/1.1; `https://` will reject with a `TypeError` until TLS is added (D-O10). Until then `fetch` is an unknown name.
- Not there: `globalThis` (PTS1001), `structuredClone` and the DOM's globals (undeclared, D-005), `Intl`, `WeakMap`, `WeakSet`, `WeakRef`, `Atomics` and `SharedArrayBuffer` (PTS1001; `pts:sync` has atomics).

### 7.2 Built-ins

- **String** (D-016, D-082, D-134, D-135): `length`, indexing, `at`, `charAt`, `charCodeAt`, `codePointAt`, `concat`, `endsWith`, `includes`, `indexOf`, `isWellFormed`, `lastIndexOf`, `localeCompare` (Node's default collation; an English locale or none), `match`, `matchAll`, `normalize`, `padEnd`, `padStart`, `repeat`, `replace` and `replaceAll` (string or RegExp patterns; replacement strings with `$` patterns, or callbacks), `search`, `slice`, `split` (string or RegExp separators), `startsWith`, `substr`, `substring`, `toLowerCase`, `toUpperCase`, `toLocaleLowerCase` and `toLocaleUpperCase` (without a locale), `toWellFormed`, `trim`, `trimStart`, `trimEnd` (and `trimLeft`/`trimRight`); `String.fromCharCode`, `String.fromCodePoint`. Not yet: `toString` and `valueOf` of a typed string (they work through `any`), `String.raw` and tagged templates.
- **Array** (D-039, D-042, D-085, D-110): `length`, indexing, `at`, `concat`, `entries`, `every`, `fill`, `filter`, `find`, `findIndex`, `findLast`, `findLastIndex`, `flat`, `flatMap`, `forEach`, `includes`, `indexOf`, `join`, `keys`, `lastIndexOf`, `map`, `pop`, `push`, `reduce`, `reduceRight`, `reverse`, `shift`, `slice`, `some`, `sort` (stable), `splice`, `toReversed`, `toSorted`, `toSpliced`, `toString`, `unshift`, `values`, `with`; `Array.isArray`, `Array.from` (of arrays, strings, iterables and `{ length: n }`, with or without a mapper), `Array.of`, `new Array(n)` and `Array(n)`. Not yet: `copyWithin`, `Array.fromAsync`.
- **Map and Set** (D-046, D-056, D-110): `new Map` and `new Set` (empty, or of an iterable or of entries), `get`, `set`, `add`, `has`, `delete`, `clear`, `size`, `forEach`, `keys`, `values`, `entries`, iteration; insertion order and SameValueZero keys, as in JS. Not yet: the ES2025 set methods (`union`, …), `Map.groupBy`.
- **Number** (D-083): `toFixed`, `toExponential`, `toPrecision`, `toString(radix)`, `toLocaleString` (Node's default English format), `valueOf`; `Number.isInteger`, `isSafeInteger`, `isFinite`, `isNaN`, `parseInt`, `parseFloat` and the constants (`MAX_SAFE_INTEGER`, `EPSILON`, …).
- **Object** (D-086, D-096, D-105): `Object.keys`, `values`, `entries`, `hasOwn`, `assign`, `is`, `freeze`, `isFrozen`, `fromEntries`; `o.hasOwnProperty(k)` on any object. Not yet: `create`, `defineProperty`, `getOwnPropertyNames`, `getPrototypeOf`, `seal`, `preventExtensions`, `isSealed`, `isExtensible`, `groupBy`.
- **`Date`** (basic, D-087): the constructor's forms (now, a time value, a string, another date, local components), `Date.now`, `Date.UTC`, `Date.parse`, the local and UTC getters and setters, `getTime`, `valueOf`, `getTimezoneOffset`, `toString`, `toDateString`, `toTimeString`, `toISOString`, `toUTCString`, `toJSON`; local time from the C library's time zone rules (`TZ`). Not yet: the `toLocale…` formats, `Date()` without `new`, `getYear`, extending `Date`.
- Typed arrays (`Int8Array`, `Uint8Array`, `Uint8ClampedArray`, `Int16Array`, `Uint16Array`, `Int32Array`, `Uint32Array`, `Float32Array`, `Float64Array`) and `ArrayBuffer`, with their methods, as Node has them (D-125). Their elements are numbers (`a[i] + 1` doesn't wrap at 255 for a `Uint8Array`), written with JS's conversions; `subarray` and views of a buffer share its bytes.
- `RegExp`, backed by PCRE2 (D-O09, D-132, D-133): regex literals, `new RegExp(pattern, flags)` and `RegExp(…)`, `source`, `flags` and the flag properties, `lastIndex` with `g` and `y`, `exec` (match arrays with `index`, `input` and `groups`), `test`, `toString`, `RegExp.escape`, printing and conversion to strings as Node does, all through `any` too. The String methods that take a RegExp (`match`, `matchAll`, `search`, `split`, `replace` and `replaceAll` with replacement patterns or callbacks) work as RegExp.prototype's `@@match` … `@@replace` do (D-134), on any pattern a program gives them, through `any` too. A match array is a `string[]` with its extra properties.
- The error classes (§3.9), `Promise` (§5.2), generators and iterators (§3.14).

### 7.3 `pts:` modules

| Module | Contents |
|---|---|
| `pts:fs` | Node's synchronous file API (D-117): `readFileSync`, `writeFileSync`, `appendFileSync`, `existsSync`, `accessSync`, `statSync` and `lstatSync` (with `throwIfNoEntry`), `mkdirSync` (`recursive`, `mode`), `readdirSync` (`withFileTypes`, `recursive`), `unlinkSync`, `rmdirSync`, `rmSync` (`recursive`, `force`), `renameSync`, `copyFileSync`, `symlinkSync`, `readlinkSync`, `realpathSync`, `chmodSync`, `mkdtempSync`; `constants`; the classes `Stats` (Node's numbers, `atime` … `birthtime` as getters, `isFile()` …) and `Dirent` (`name`, `parentPath`, `isFile()` …); `promises` (`pts:fs/promises`'s default export); the types `PathLike`, `BufferEncoding` (`"utf8"` or `"utf-8"`) and the option types. The default export is an `fs` object (`FileSystem`) with the functions as methods, `constants`, `promises` and `F_OK` … `X_OK`. Text in UTF-8 only; Node's errors (D-117) |
| `pts:fs/promises` | Node's `fs/promises` (D-118): `readFile`, `writeFile`, `appendFile`, `access`, `stat`, `lstat`, `mkdir`, `readdir`, `unlink`, `rmdir`, `rm`, `rename`, `copyFile`, `symlink`, `readlink`, `realpath`, `chmod`, `mkdtemp`, `constants`; the default export (`FilePromises`) has them as methods |
| `pts:path` | Node's `path` for POSIX (D-116): `join`, `resolve`, `relative`, `normalize`, `dirname`, `basename`, `extname`, `parse`, `format`, `isAbsolute`, `toNamespacedPath`, `sep`, `delimiter`, `posix`; the types `ParsedPath`, `FormatInputPathObject` and `PlatformPath`. The default export, also `posix`, is a `PlatformPath` object with them as members, whose `posix` is itself |
| `pts:process` | `argv`, `env`, `exit`, `cwd`, `platform`, `arch`, `pid`, `execPath`, `stdout` and `stderr` (`write`); the classes `Process` and `WriteStream`, the types `ProcessEnv` and `ErrnoException`. The default export is also the global `process`, as in Node, which has `exitCode` too (D-116) |
| `pts:parallel` | `range`, `join`, the classes `Range`, `ParallelArray<T>` (`xs.par`), `ParallelTypedArray<A>` (`ta.par`) and `ParallelRange` (`range(…).par`), and the callback brand `ParallelCallback<F>` (`F & ParallelBrand`) (§5.4; D-123, D-126) |
| `pts:sync` | Synchronization primitives (§5.7): `AtomicInt`, `Atomic<T extends int = int>`, `Mutex<T>`, `MutexGuard<T>`, `Once` (D-128), `Channel<T>` (D-129) |
| `pts:http` | An HTTP/1.1 server in the style of Bun's (D-136): `serve({ port, hostname, reusePort, fetch, error })` returns a `Server` (`port`, `hostname`, `url`, `stop()`); `fetch(request, server)` returns a `Response` or a promise of one; the Fetch standard's `Request` (`method`, `url`, `headers`, `text()`, `json()`), `Response` (`status`, `statusText`, `headers`, `ok`, `text()`, `json()`, `Response.json(data, init)`) and `Headers` (`append`, `delete`, `get`, `getSetCookie`, `has`, `set`, `forEach`, `entries`, `keys`, `values`, iteration), bodies as text. Keep-alive, pipelined requests, chunked request bodies and `Expect: 100-continue`; responses carry a `date` header. The server listens on the loop that called `serve`; with `reusePort: true`, on every loop, each with its own SO_REUSEPORT socket on the same port, so the kernel spreads connections over the loops and handlers run on several at once (D-137): state they share needs `pts:sync` (an `AtomicInt` for a counter), and the guardrail doesn't check `fetch`. `fetch` as a global is planned (M6.3) |

The `pts:` modules that mirror Node's (`pts:fs`, `pts:fs/promises`, `pts:path`, `pts:process`) behave as `node:fs`, `node:fs/promises`, `node:path` and `node:process` do, so a program that imports only those prints what Node prints with the `node:` modules (differences in §8). They are written in ParallelScript and compiled with the program that imports them; they reach the runtime through functions they declare without a body (`declare function pts_process_cwd(): string`), which user code can't name (D-116).

---

## 8. Documented differences from TypeScript and Node

The compatibility principle (§1.1) holds except for these:

- Properties cannot be added to or deleted from objects after construction (§3.6).
- `any` is checked at the boundary into typed code (§3.4); casts are checked at runtime (§3.5).
- The features in §3.10 are compile errors, and so (PTS1001) are the features the sections above list as not supported yet.
- Reading a string index out of range (`s[i]`) gives `undefined` where the context can hold any value (printing, concatenation, template literals, `===`/`!==` with a string, `<`/`>` comparisons), but throws `RangeError` where a `string` is required, e.g. `const c: string = s[9]` (D-015).
- Loose locals (D-090, D-094): a local initialized from an element read (`const x = a[i]`, `const fn = TABLE[k]`) that the program tests for undefined, and every local an array pattern binds from an array (`const [op, arg] = line.split(" ")`), holds undefined as in JS; using it as its type while it is undefined throws `TypeError: Expected T, got undefined`, where JS goes on with undefined (and fails later, if at all). An element read straight into a typed place (`const n: number = a[9]`, an argument, a result) still throws `RangeError`.
- Arrays (D-038): a program can't make holes in an array: writing past the end (`a[a.length + 1] = x`) and growing an array by setting `length` throw `RangeError`. The holes an array can hold are those `new Array(n)` leaves (D-085, below), a match array's groups that took no part (D-133, below) and those racing pushes leave (D-130, §4.1). Reading `a[i]` out of range gives `undefined` where any value can go (printing, concatenation, template literals, conditions, the object of `?.`, a conditional's branch whose type holds undefined) and throws `RangeError` where an element is required; `a.pop()!` and `a.shift()!` on an empty array throw `RangeError`.
- Function values (D-041): an arrow function or function expression that captures no variables is one static closure, so two evaluations of it give the same (`===`) function, where JS makes a new one each time; the same holds for a nested function declaration that captures nothing. A function used where its type takes more parameters, or other parameter types, may be wrapped in an adapter, which is a different function for `===` but prints as the one it wraps. Functions can't be converted to strings (JS gives their source text).
- Function lengths (D-098): a method read as a value counts its parameters before the first optional one (`m(a, b?)` has length 1, where JS counts 2), and a bound function's `length` isn't reduced by its bound arguments.
- Rest parameters (D-057): a function with fixed parameters used as a function type with a rest parameter (`const f: (...xs: number[]) => number = add`) reads each argument from the rest array with a checked read, so calling it with too few arguments throws `RangeError` where JS passes `undefined`.
- Optional properties (D-065): an optional property that a literal leaves out and that is assigned later prints after the literal's properties, in the order the object's type declares such properties, where JS prints properties in the order they were added. `{ a: 1, ...o }` keeps 1 when `o.a` is undefined, if tsc types the result's `a` without undefined (JS gives undefined). An object that lacks an optional property of a type it is used as can't gain it: writing it throws `TypeError: Cannot add property p, object is not extensible`; one that holds it without undefined (`{ verbose: true }` used as `{ verbose?: boolean }`) throws `TypeError` when undefined is written to it (D-066). A value whose objects may hold an optional property as yet another type (a subclass's field of another type) can't be used as the type (PTS1001).
- `any` (D-070 … D-077): converting dynamic data (an `any[]`, a hash-map object of `any`) to a typed array or object type makes a copy, so writes to one don't show in the other, and a copy into an object type keeps only the properties the type declares; so does converting a typed object whose declared field types don't imply the target's (a `child: Animal` field holding a Dog, converted to `{ child: Dog }`, D-072). Converting an `any` into a typed place checks it (`TypeError: Expected number, got string`), where TypeScript checks nothing; writing a property an object's type doesn't allow, or doesn't have, throws `TypeError`, where JS converts nothing or adds it. A method read through `any` is a bound closure, a new one at each read (JS: the same function each time); a function held in a property and called through `any` as a method gets no `this`, and `call` and `apply` through `any` ignore their `this` argument (D-074, D-075, D-076). An object meets `+`, `<`, `==` and ToNumber through its class's `valueOf()` if it returns a primitive, else as its string (D-088); an object literal's `valueOf` isn't called.
- JSON (D-080): `JSON.stringify` that gives undefined (of undefined or a function) where a `string` is required throws `TypeError: Expected string, got undefined`, where JS puts undefined there; printed or kept in an `any` it is undefined, as in JS. Integer types stringify exactly (an `i64` past 2^53 too).
- Exceptions (D-068, D-069): a stack frame shows the file's path and a line, without a column (`at f (/src/app.pts:12)`, where Node writes `at f (/src/app.ts:12:5)`), and top-level code without a name (`at /src/app.pts:13`, where Node writes `at <anonymous> (/src/app.ts:13:3)` followed by its module loader's frames); calls clang turns into jumps (tail recursion, inlining that leaves no trace) may be missing; the error of a stack overflow has no frames; recursion goes as deep as the thread's C stack allows (8 MB on Linux by default, less 256 KB), deeper than Node. An error's `name` is its own property, set by the constructor to its native class's name, where JS inherits it from the prototype (only `Object.keys` and JSON could tell). A class derived from Error can't redefine `stack`.
- Uncaught errors (D-O07): an uncaught exception's report on stderr is `Uncaught ` and the error's stack, where Node prints the throwing line with a caret, the stack and its version; the exit code (1) is Node's.
- Narrowed values that change behind tsc's back (D-089, D-140): tsc keeps a property, or a variable that another function assigns, narrowed across calls. If a call changes it to another member of its union, reading it as the narrowed type throws `TypeError: Expected string, got number`, where JS reads the new value (`value.length` of a number is undefined).
- Conversions to primitives (D-142): `String(x)`, templates, `+`, `join` and the rest call a class's or object literal's `toString` and `valueOf` as JS's OrdinaryToPrimitive does, with no arguments, whatever they return; a parameter that can't take undefined (`toString(times: number)`) throws `TypeError: Expected number, got undefined` where JS passes undefined.
- Unions of objects (D-067): narrowing a union to a class with a type guard or `in` that is wrong at run time throws `TypeError: The value narrowed to C is not an instance of it`, where JS carries on and reads undefined. `==` between an object and a primitive compares the object's primitive: its class's `valueOf()` if it has one, else its string (an array's join, its class's `toString()`, else "[object Object]").
- Object spread (D-058, D-108): `{ ...o }` copies the properties of o's static type: a class instance's fields in their constructor's order (as JS does), and an object type's properties in its source's order when the literal starts with a spread of an object of the same shape that gives every property (`{ ...form }`, `{ ...p, x: 5 }`), else in the order the type lists them. So an object with more properties than its static type copies those properties only, and a later spread of an object written in another order copies in the type's order, where JS copies the object's own properties in its order.
- Methods as values (D-044): `o.m` used as a value is bound to `o`, as `o.m.bind(o)` would be, where JS gives the unbound method (whose `this` is undefined when it is called plainly, so a method that uses `this` fails); each read makes a new bound function, so `o.m === o.m` is false. `f.call(x, …)` and `f.bind(x, …)` on a class's method run the method `x`'s class has; on a method of an object type, `x` must be `o` itself.
- Classes (D-028, D-030): a subclass that overrides only the getter of an inherited getter and setter keeps the parent's setter, where JS leaves the property without one (and assigning it throws `TypeError`). A base constructor that calls a method a subclass overrides, which reads a subclass field, sees the field's zero value (`""`, 0, false), where JS sees undefined, since the subclass's initialisers run after `super()` returns; `console.log(this)` there lists the subclass's fields too.
- Enums (D-052, D-089): reading a numeric enum's reverse mapping `E[n]` for a value no member has gives `undefined` where any value can go, but throws `RangeError` where a `string` is required, as string indexing does (D-015). Each use of an enum object as a value makes a new hash-map object, so `E === E` is false.
- Maps (D-046): `m.get(k)!` throws `RangeError` when `m` has no entry for `k`, where JS gives `undefined`. A map's or set's key and value types are fixed; JS's own iteration order, SameValueZero keys and live iteration are kept.
- Unions (D-062): `x!` throws `TypeError` when x is null or undefined, where TypeScript's `!` checks nothing at run time. `x++` on a union variable is a statement's increment; as a value it is the union's old value.
- Hash-map objects (D-059: `Record<string, T>`, index signatures): reading a missing key where a value of the entry's type is required (`const n: number = o.k`, `o.k + 1`, `o.k += 1`) throws `RangeError: Object has no property 'k'`, where JS gives `undefined`; printing it, testing it, comparing it with `undefined` and `??`/`||` fallbacks behave as in JS. They inherit nothing: `"toString" in o` is false and `o.__proto__` is an ordinary key. A function stored under a computed key known only at run time (`{ [k]: () => 1 }`) is anonymous, where JS names it after the key.
- Dates (D-087): a time zone's long name in `toString` comes from a table of common zones, else ICU's fallback `GMT+hh:mm`, where Node names every zone; `Date.parse` reads the ISO format and the common other forms, where V8's legacy parser reads more; the `toLocale…` formats aren't supported yet.
- Objects (D-086, D-105): `Object.assign` can't add a property to its target, and checks each value against the target's declared type (`TypeError`), as any write does; `Object.assign({}, …)` is the spread of its sources (D-089). `Object.freeze` marks the object, which `Object.isFrozen` reports, but doesn't stop writes through other references at run time (writes through the frozen type are tsc errors); `Object.seal`, `Object.preventExtensions` and their tests aren't supported yet.
- Arrays (D-085): `new Array(n)` of numbers, integers or booleans starts with zeros (or false) where JS has holes (`<n empty items>`, undefined when read); arrays of strings, objects and dynamic values have JS's holes. An `any[]` used as a typed array is a checked copy.
- Numbers (D-083): `toLocaleString` always formats as Node's default English locale does; other locales and options are compile errors (`TypeError` through `any`).
- Strings (D-082): `localeCompare` always uses the root collation that Node's default English locale has, whatever the environment's locale; letters ICU expands (`ß` as "ss", `æ` as "ae", ligatures) compare as letters of their own; locales other than English and collator options are compile errors (`TypeError` through `any`). `toLocaleUpperCase`/`toLocaleLowerCase` take no locale. `normalize` uses tables generated from Node's ICU, so it normalizes as the Node that generated them does (Unicode 17 now, D-135).
- Console (D-088): `%s` of an error inspects it even where its class overrides `toString`; `%s` of a function throws (functions have no source text); under `%o` or `showHidden` a function doesn't list its `length`, `name` and `prototype`, and a getter and a setter of one name that different classes define show as one `[Getter/Setter]`. Widths of strings for laying out arrays in columns are summed per code point, where Node measures the string's NFC form. `console.trace` shows the program's frames only; warnings (a missing count or timer label) go to stderr without Node's `(node:<pid>)` prefix. console.log of an `AggregateError` shows `errors` as a property, where Node shows `[errors]` (D-113), and of a `Timeout` prints `Timeout {}`, where Node prints its internals (D-114).
- Type-level features through run-time checks (D-089): `x as T` that isn't an upcast is checked, and to an array or object type that isn't the value's own it makes a checked copy, as converting from `any` does; keyed elements (`o[k]` for `k: keyof T`), members of unions of objects and other values, and values of unions that recur through arrays or hash-map objects (`type Json = … | Json[]`) are found at run time and checked where they become typed. A tuple used with array methods or `for…of` is copied into an array. A property tsc narrowed to a primitive that a call has changed since throws `TypeError` where it is read as that type, where JS reads the new value; printed or compared, it is read as it is.
- Generic consts (D-102): a module-scope generic const that is monomorphized (§3.7) can be called by name before its declaration has run, where JS throws `ReferenceError`.
- Generators (D-109, D-111, D-115): a `yield` (or `await`) in a loop's condition or update, a case label, a declaration's variables after the first, a default value or an optional chain is a compile error for now. A `yield` elsewhere inside an expression is hoisted: the names, literals and property reads JS would read before it are read after it, so a change to them while the generator is suspended shows (`x += yield v` reads x after the yield). A generator function's default parameter values are evaluated when its generator first runs, where JS evaluates them at the call. Destructuring more elements than an iterator yields into typed locals throws `RangeError` as reading past an array's end does (D-038). An object literal returned as an iterator result without `done` prints `done: false`. `return(v)` on a suspended async generator resumes it with v without awaiting v first.
- Promises and timers (D-112, D-114, D-118): a thenable whose `then` is a getter that throws isn't caught; numeric timer ids (`clearTimeout(+t)`) aren't supported. A promise of `pts:fs/promises` settles after fewer steps than Node's, so how its settling interleaves with other promises' reactions may differ (both settle after the current microtasks; the order in which I/O completes isn't deterministic in either).
- Process (D-116): `process.argv` starts with the executable's path twice, where Node has its own path and the script's (`argv.slice(2)` is the arguments in both); `process.env` is a copy of the environment made at startup, so writing it doesn't change what the system sees; Node's `nextTick`, `hrtime`, `memoryUsage`, `on(…)` and `stdin` are not there. `pts:path` is the POSIX path module only (no `win32`, no `matchesGlob`).
- Files (D-117, D-118): `pts:fs` reads and writes text (UTF-8) only: reading without an encoding, which gives a Buffer in Node, is a compile error (there is no `Buffer`; typed arrays are there, D-125); other encodings, file descriptors (`openSync`, `FileHandle`), `opendir`, `cp`, `utimes`, `truncate`, `chown`, streams and watchers are not there. console.log of a `Dirent` leaves out Node's `Symbol(type)` entry; `rmSync` of a directory without `recursive` throws an `Error` (Node: a `SystemError`) with Node's message and code.
- Typed arrays (D-125): reading an element out of range gives `undefined` where any value can go and throws `RangeError` where a number is required, as arrays do (D-038); writes out of range are ignored, as in JS. `Float16Array`, `BigInt64Array`, `BigUint64Array`, `DataView`, `SharedArrayBuffer`, resizable and transferable buffers, the base64 and hex methods and `thisArg` arguments are compile errors. Through `any`, elements, `length`, `byteLength`, `byteOffset`, `buffer`, `at`, `fill`, `includes`, `indexOf`, `join`, `lastIndexOf`, `reverse`, `slice`, `sort()`, `subarray` and `toString` work; other methods throw `TypeError`. A typed array's `for…of` reads its elements live, but spreads, `Array.from`, `keys()`, `values()` and `entries()` copy them first. Typed arrays don't take part in `Object.assign` or spreads into objects.
- RegExp (D-132, D-133): a group that took no part in a match is undefined as in JS where the context tests it (`m[2] || "px"`, `m[2] ?? d`, `m[2] === undefined`, conditions), prints it, makes a string of it or destructures it; elsewhere, where a `string` is required, including callbacks of a match array's methods (`m.map(f)`) and `for…of` over it, it throws `TypeError: Expected string, got undefined`, where JS goes on with undefined. A named group that took no part is undefined in `groups` too, but reading it where a string is required throws `RangeError`, as a missing key does (D-059). The `d` flag is accepted and `hasIndices` reports it, but match arrays have no `indices`; `compile`, the legacy statics (`RegExp.$1`, `RegExp.lastMatch`, …), the `v` flag, `Symbol.match` and friends, and classes extending RegExp are not supported. Without the `u` flag, `.` and classes match an astral character whole, where JS sees two code units (D-132), so patterns written against surrogate halves don't match, and an empty match next to one moves past the whole character. `\s` doesn't match U+FEFF, and `\w` with `iu` doesn't match U+017F and U+212A; duplicate group names in different alternatives (ES2025) are a SyntaxError; a match that exceeds PCRE2's backtracking limit throws `RangeError: Maximum call stack size exceeded`. A RegExp with `g` or `y` holds state in `lastIndex`, so threads sharing one and calling its `exec` or `test` race, as on any shared object; the String methods loop over a global RegExp's matches without using its `lastIndex` (they leave it at 0, as JS does), so they may share one (D-134). A replacement callback's parameters are checked against their types: `(m: string, unit: string) => …` called for a group that took no part throws `TypeError`, where JS passes undefined (`(m, unit) => …`, whose `unit` is `any`, gets undefined); callbacks with default parameter values, and callbacks with a pattern that may be a string, are not supported yet. console.log of `matchAll`'s iterator prints `Object [Array Iterator] {}`, where Node prints `Object [RegExp String Iterator] {}`.
- Array methods with callbacks (D-042): a callback that shortens the array makes `map`, `flatMap` and `filter` skip the indices it no longer has, so `map`'s result is shorter where JS's would have holes; `find(…)!` and `findLast(…)!` throw `RangeError` when no element matches, where JS gives `undefined`. `sort` with a comparator is a stable merge sort: the order is V8's for any consistent comparator, but the comparator may be called on other pairs, in another order.
- Fields read before the constructor sets them (D-141): a string or number field reads `""` or 0 where Node reads undefined. An object field reads as undefined in tests of truth, `=== undefined`, `== null`, `??=`, `??` with a value of its type, `console.log` and other dynamic uses, and reading a member of it or calling its method throws Node's `TypeError: Cannot read properties of undefined (reading 'x')`; other uses as an object (an argument, a builtin method's receiver) throw `TypeError: The field 'f' is undefined: it is read before the constructor sets it`, `typeof` gives "object", `?.` throws rather than giving undefined, and `??` with a value of another type keeps the field.

---

## 9. Implementation notes

Representation choices, runtime layouts, and anything a later session needs to know that the sections above don't say.

### 9.1 Runtime layout (M0.4)

- `pts/runtime/include/pts.h` is the only header generated C includes. Sources are `pts/runtime/src/*.c`; C unit tests are `pts/runtime/tests/*_test.c` (each its own program; `go test ./pts/toolchain/` builds and runs them under ASan and UBSan).
- `pts/toolchain` builds `libptsrt.a`, `libuv.a`, `libgc.a`, `libryu.a`, `libbacktrace.a` and `libpcre2.a` (D-132) into `~/.cache/pts/` and links programs against them with `clang -std=c11` plus `-lpthread -ldl -lm -static-libgcc` (D-003, D-069). Output binaries depend only on libc. A fresh clone needs `git submodule update --init pts/third_party`.
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
| Diagnostics | `pts/diagnostic` | One format for tsc and PTS errors; codes `PTS1001` (not supported yet), `PTS1002` (unsupported by design) (D-006), `PTS2001`–`PTS2003` (numeric types), `PTS3001` (the guardrail) and `PTS3002` (`parallel for`) (D-127). |
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

### 9.54 Maps, sets and `Object.keys` through `any` (M3.3c3b)

- **Runtime** (D-077): `pts_type.key_check`; `dynamic_methods.c`: `map_methods`, `set_methods`, `record_methods`, `lookup_slot`, `stored_slot`, `own_keys`, `pts_value_keys`, `pts_value_values`, `pts_value_entries`, `pts_value_has_own`.
- **IR:** `ir.ValueKeys`, `ir.ValueValues`, `ir.ValueEntries`, `ir.ValueHasOwn`.
- **Lowering:** `dynamicObjectStatic`, `dynamicObjectStatics` (`records.go`).
- **C:** maps' `.check` and maps' and sets' `.key_check` (`mapDescriptorDefinitions`), their checks in `collectDeclaredTypes`.
- **Tests:** `pts/tests/run/any_maps.pts` (against Node), `any_maps_error.pts`.

### 9.55 Generic function values (M3.3d1)

- **Lowering** (D-078): `substitution.erased` and `mentions`, `instantiate` (any for a type that mentions an erased type parameter), `instantiateMade`, `mentionsErased`, `erasing`, `erasedInstance`, `declareInstance`, `genericValue`, `contextualTypeArguments`, `instantiationExpression`, `calledInstance` (erased copies for erased type arguments) (`generics.go`); `typeAt` and `contextualType` of literals and `new` in erased code; erased signatures in `signatureType`, erased closures in `functionExpression`, `fromErased` in `valueCall`, checked results of adapters from erased types (`closures.go`); `for…of` over dynamic values (`forOfStatement`), `spreadPart` (`arrays.go`); dynamic spread arguments (`argumentValues`, `restArray`), rest parameters of erased code (`parameterType`) (`lower.go`); array patterns of dynamic values and strings (`destructure`, `patternSource`, `destructuring.go`). Fixed: `mapOf` no longer returns a hash-map object type for a `Map` of the same key and value types.
- **Checker** (fork): `PtsInstantiateSignatureInContextOf` (`internal/checker/pts.go`).
- **IR:** `ir.ValueIterable`, `ir.ValueIterableLength`, `ir.ValueIterableElement`, `ir.ValueToArray`, `ir.ValueArray`.
- **Runtime:** `pts_value_iterable`, `pts_value_iterable_length`, `pts_value_iterable_element`, `pts_value_to_array` (`dynamic_methods.c`); `convert_tuple` copies other tuples (`convert.c`).
- **Tests:** `pts/tests/run/generic_values.pts`, `any_iteration.pts`, `maps_and_records.pts` (against Node), `generic_values_checks.pts`, `generic_values_classes.pts` (skipped: the known issue); `TestGenericFunctionValuesAreInstancesInContextAndErasedCopiesElsewhere`; updated `errors/generics_not_yet.pts`, `errors/closures_not_yet.pts`.

### 9.56 Generic methods (M3.3d2)

- **Lowering** (D-079): erased signatures of generic methods of object types (`interfaceMethod`, `objects.go`) and erased object literal methods (`objectMethod`, `methods.go`); `fromErased` for calls through interfaces and shape method fields (`interfaceMethodCall`); `genericMethodList`, `tableMethodNames`, `allMethodsInTables`, `wantInMethodTables`, `addErasedMethod`, `valueInstance`, `genericMethodValue` (`generics.go`); `wantDynamicMember` for reads and calls through `any` (`dynamic.go`); `declareMethod` reports overriding a generic method (`classes.go`); arrows take an interface result from their context (`closureSignature`, `closures.go`).
- **Runtime:** `pts_method_find_adapted`, `pts_method_find_adapted_slow`, `pts_method_call_dynamic` (`pts.h`, `object.c`); `descriptor_implies` accepts methods of other signatures with dynamic entries (`convert.c`).
- **C:** `methodAdapter`, `collectMethodAdapters`, `methodAdapterOf`, `methodAdapterSignature`, `forwardedParams` and `forwardBody` (shared with `adapterDefinition`) (`dynamic_calls.go`); `interfaceCall` uses the adapted lookup in programs with declared types (`classes.go`).
- **Tests:** `pts/tests/run/generic_methods_values.pts` (against Node), `generic_methods_checks.pts`; updated `errors/generics_not_yet.pts`, `errors/object_types_not_yet.pts`.

### 9.57 JSON.parse and JSON.stringify (M3.4a)

- **Runtime** (D-080): `json.c`: `pts_type_any_record`, `pts_json_parse` (`parser`, `parse_value`, `parse_object`, `parse_array`, `parse_string`, `parse_number`, `parse_literal`; V8's messages in `fail_at` and `unexpected`, positions in UTF-16 code units by `units_between`), `pts_json_stringify` (`writer`, `serialize`, `serialize_object`, `serialize_array`, `put_quoted`, `call_to_json`, `property_list`, the cycle message in `push_frame` and `circular`); `inspect.c` shows an own `__proto__` key as `['__proto__']`.
- **IR:** `ir.JSONParse`, `ir.JSONStringify`.
- **Lowering:** `json.go`: `jsonParse`, `jsonStringify`; `isGlobalMember` (`records.go`, which `isObjectStatic` now uses).
- **C:** `Record<string, any>` uses the runtime's descriptor (`nameMapDescriptor`, `maps.go`); JSON.stringify turns on declared types (dynamic entries and full method tables) like calls through `any` (`collectChecks`).
- **Tests:** `pts/tests/run/json_parse.pts`, `json_stringify.pts` (against Node), `json_checks.pts`.

### 9.58 The JSON and word-frequency benchmarks (M3.4b)

- **Benchmarks** (D-081): `pts/bench/json/` and `pts/bench/word_freq/` (`main.pts`, run by Node and Bun as is, and the Go twins `main.go`); numbers in `docs/BENCHMARKS.md`.
- **Runtime:** JSON's fast paths in `json.c`: `serialize_object` reads an object's fields and a hash-map object's entries in place (`serialize_property`); `key_string` keeps one string per repeated key in a parse; `put_quoted` copies a string without escapes at once.
- **Tests:** `pts/tests/run/json_stringify.pts` gained literal getters, absent optional properties, an Error subclass and a replacer that leaves a key out.

### 9.59 Strings (M3.5a)

- **Runtime** (D-082): `string_methods.c`: `pts_string_at`, `_char_at`, `_code_point_at`, `_last_index_of` (`find_last`), `_substr`, `_replace` and `_replace_all` (`substitution`, `next_match`), `_is_well_formed`, `_to_well_formed`, `_from_char_codes` and `_from_code_points` (`string_of_units`); `collation.c`: `pts_string_locale_compare` over `collation_tables.h`, generated by `pts/runtime/gen/collation_tables.mjs` from Node's ICU (`key_of`, `implicit_key`, Hangul decomposition in `cursor_next_key`); `dynamic_methods.c`: the same methods through `any` (`string_replacing`, `replace_with_callback`, `check_default_locale`).
- **IR:** `ir.StringAt`, `StringCharAt`, `StringCodePointAt`, `StringLastIndexOf`, `StringSubstr`, `StringReplace`, `StringReplaceAll`, `StringIsWellFormed`, `StringToWellFormed`, `StringLocaleCompare`, `StringFromCharCode`, `StringFromCodePoint`; `ir.NumberArray`.
- **Lowering:** the builtins table in `lower.go` (and `libMemberName`; `methodCall` reports arguments a builtin doesn't take); `strings.go`: `stringLibraryCall` (`stringConcat`, `stringReplace` with `replaceHelper` and `replacing`, `localeCompare`, `stringFromCodes`).
- **Tests:** `pts/tests/run/string_library.pts`, `any_string_methods.pts`, `pts/tests/errors/strings_not_yet.pts`, the new methods in the generated string corpus and its `locale_compare_*` parts (`strings_test.go`), C tests in `string_methods_test.c`.

### 9.60 Numbers (M3.5b)

- **Runtime** (D-083): `number_methods.c`: `pts_number_to_fixed`, `_to_exponential`, `_to_exponential_shortest`, `_to_precision` (`exact_digits`, `round_half_up`, `digit_at`), `pts_number_to_string_radix` (`radix_string`, V8's DoubleToRadixCString), `pts_i64_to_string_radix`, `pts_u64_to_string_radix`, `pts_number_to_locale_string`, `pts_parse_int` (`power_of_two_digits`, `other_radix_digits`), `pts_parse_float` (`decimal_prefix`), `pts_value_is_integer` and the other predicates of dynamic values; `pts_number_is_integer` and the others inline in `pts.h`; `dynamic_methods.c`: `number_methods` and `boolean_methods`.
- **IR:** `ir.NumberToFixed`, `NumberToExponential`, `NumberToExponentialShortest`, `NumberToPrecision`, `NumberToStringRadix`, `NumberToLocaleString`, `I64ToStringRadix`, `U64ToStringRadix`, `NumberIsInteger`/`IsSafeInteger`/`IsFinite`/`IsNaN` and their `Value` twins, `ParseInt`, `ParseFloat`.
- **Lowering:** `numbers.go`: `numberLibraryCall` (`numberMemberCall`, `numberMethodCall`, `numberTest`, `parseNumber`, `numberConversion`, `toNumberOrReport`), `numberConstant`, `conversionFunction` (String, Number and Boolean as values, from `identifier`); unary operators convert with `convertsToNumber`/`toNumber` (`prefixUnary`).
- **Tests:** `pts/tests/run/number_library.pts`, `any_number_methods.pts` (against Node), `number_methods_integers.pts`, `pts/tests/errors/unary_plus_on_void.pts`, the `methods_*` parts of the generated number corpus (`numbers_test.go`), C tests in `number_test.c`.

### 9.61 Math (M3.5c1)

- **Runtime** (D-084): `ieee754.c` (V8 13.6's fdlibm in C: `pts_ieee754_sin` … `pts_ieee754_atan2`); `math.c`: `pts_math_max_of`, `pts_math_min_of`, `pts_math_hypot`, `pts_math_random` (xorshift128+ per thread); `pts_math_fround`, `pts_math_imul`, `pts_math_clz32` inline in `pts.h`.
- **IR:** `ir.MathAcos` … `ir.MathTanh`, `MathAtan2`, `MathPow`, `MathImul`, `MathClz32`, `MathFround`, `MathRandom`, `MathMaxOf`, `MathMinOf`, `MathHypot`.
- **Lowering:** `math.go`: `mathFunctions`, `variadicMath`, `mathCall` (ToNumber of each argument, spreads through `restArray`), `mathFunctionValue` (Math's functions as values).
- **Tests:** `pts/tests/run/math_library.pts` (against Node), `pts/tests/errors/math_not_yet.pts`, the `math` part of the generated number corpus.

### 9.62 The remaining array methods (M3.5c2)

- **Runtime** (D-085): `array.c`: `pts_array_at`, `pts_array_with`, `pts_array_of_length`, `pts_array_keys`; `inspect.c`: `format_elements` prints runs of holes as `<n empty items>` (Node's formatSpecialArray); `dynamic_methods.c`: `array_to_sorted`, `array_to_reversed`, `array_to_spliced`, `array_with`.
- **IR:** `ir.ArrayAt`, `ArrayCopy`, `ArrayWith`, `ArrayKeys`; `NewArray.Length`.
- **Lowering:** `array_library.go`: `copyingMethodCall`, `arrayConstructor` (from `call` and `newExpression`), `arrayOfCall`, `arrayFromLength` (`lengthMapHelper`), `arrayIteration` (`entriesHelper`) through `iterable`, which `for…of`, spreads (`argumentValues`) and `Array.from` use; `at` in `arrayMethods`; `convert` copies an `any[]` into a typed array.
- **Tests:** `pts/tests/run/array_library.pts`, `any_array_methods.pts` (against Node), `array_length_zeros.pts`, `pts/tests/errors/arrays_not_yet.pts`.

### 9.63 Object's functions and `for…in` (M3.5d)

- **Runtime** (D-086): `dynamic_methods.c`: `pts_value_key_array` (the own keys as a string[]), `pts_object_assign`, `pts_value_same_value`.
- **IR:** `ir.ValueKeyArray`, `ObjectAssign`, `ValueSameValue`.
- **Lowering:** `object_functions.go`: `objectFunctionCall` (`objectAssign`, freeze, is, `objectFromEntries` with `fromEntriesHelper`), `typedObjectStatic` (from `objectStaticCall`); `records.go`: `forInKeys` (for `forInStatement`), `isObjectPrototypeMember` only for Object's own members; `lower.go`: `coveringMember` (intersections in `irType`).
- **Tests:** `pts/tests/run/object_functions.pts` (against Node), `object_assign_checks.pts`, `pts/tests/errors/objects_not_yet.pts`, `records_not_yet.pts`.

### 9.64 Date (M3.5e)

- **Runtime** (D-087): `date.c`: `pts_type_builtin_Date` (kind `PTS_KIND_DATE`), ECMAScript's arithmetic (`parts_of`, `make_day`, `make_time`, `time_clip`), local time (`offset_at`, `utc_of`, `zone_name`), `pts_date_new_*`, `pts_date_utc`, `pts_date_get`, `pts_date_set`, `pts_date_set_time`, `pts_date_format`, `pts_date_to_json`, `pts_date_parse` (`parse_iso`, `parse_legacy`); dates in `console.c`, `inspect.c`, `json.c`, `string.c` (ToString), `array.c` (`pts_object_to_string`), `dynamic.c` (`to_primitive` with a hint) and `dynamic_methods.c` (`date_methods`).
- **Lowering:** `dates.go`: `declareDateClass` (the class and its runtime functions), `newDate` (from `newExpression`), `dateCall` (statics and `dateMethodCall`), `dateTime`; relational operators on two dates compare their time values (`binary`); `builtinClasses` (was `errorClasses`) holds Date too; `extends Date` is reported.
- **C:** `externFunctions` names every called function that has `Extern`.
- **Tests:** `pts/tests/run/date_library.pts` (against Node), `pts/tests/errors/dates_not_yet.pts`, `pts/runtime/tests/date_test.c`; `run_test.go` runs every program with `TZ=America/New_York` (`testTimeZone`).

### 9.65 console (M3.6)

- **Runtime** (D-088): `inspect.c` is Node's `util.inspect` and `util.format`: the inspector's `depth` (INFINITY for null) and `show_hidden` (`beyond_depth`, `hidden_entry`, `format_prototype_accessors`, arrays' and tuples' `[length]`, errors' `[stack]`, `[message]`, `[cause]`), `format_string` (Node's formatPrimitive: `... n more characters` past 10,000 units, ` +` pieces after line breaks), `string_width` over `width_tables.h` (generated by `node --expose-internals pts/runtime/gen/width_tables.mjs` from Node's ICU), `format_arguments` (formatWithOptions: `format_placeholder`, `format_as_string` with `has_program_to_string`, `format_as_json` catching the circular TypeError, `format_parsed`), and the entry points `pts_format` and `pts_inspect`. `console.c` is Node's Console: `write_line` (group indentation on every line, lone surrogates as U+FFFD, stdout flushed before stderr), `pts_console` (`spread_arguments`, `assert_value`, `trace`, `with_label`: `count`, `count_reset`, `start_timer`, `log_timer` with `format_time`), `pts_console_group_end`, `pts_console_dir`, `pts_console_table`. `table.c` is Node's console.table and cli_table: `property_rows` (a row per own key, a column per property of the object rows, array-index columns first by `order_property_columns`, "Values" for primitive rows), `entry_rows` (maps and sets), `draw` (`divider`, `render_row`), cells inspected by `pts_inspect_cell` (depth 0, or -1 for an object of more than two keys; breakLength Infinity; maxArrayLength 3, which tuples now honour too). `inspect_internal.h` shares the text buffer and these entry points. `dynamic.c`: `pts_value_to_primitive` calls a class's `valueOf()` (`call_value_of`); `pts_value_loose_equals` uses it for instances.
- **Lowering:** `console.go`: `consoleMethods` (info, debug and dirxml are `ConsoleLog`, warn is `ConsoleError`, groupCollapsed is `ConsoleGroup`), `consoleCall` (from `call`, before builtins), `consoleArguments` (each boxed, spreads as `SpreadArg`), `consoleDir`/`consoleDirOptions` (an object literal of `depth`, null meaning Infinity, and `showHidden`). The prelude declares `Console` and `ConsoleDirOptions`.
- **IR and C:** `ir.ConsoleMethods` (the console builtins that take any values); `cgen.consoleMethods` names their `pts_console_method`; `spreadArgs` covers them; they write (`order.go`) and don't make a function recursive (`mayRecurse`). Class method tables list `valueOf()` with its result kind (`isValueOf`, `resultKind`).
- **Tests:** `pts/tests/run/console_format.pts`, `console_methods.pts` (stderr checked by `// stderr:`), `console_table.pts`, `inspect_strings.pts`, `inspect_show_hidden.pts`, `value_of.pts` (all against Node), `console_timers.pts` (`TestConsoleTimersPrintTheirLabelAndTheTime`), `pts/tests/errors/console_not_yet.pts`, `inspect_test.c` (`pts_format`, long strings, `pts_table`).

### 9.66 The corpus and its gaps (M3.7a)

- **Corpus** (D-089): `pts/tests/corpus/<theme>/*.pts`, 50 programs each for algorithms, text, oop, data, functional and errors; `TestCorpusMatchesNode` (`run_test.go`, sharing `assertMatchesNode` with `TestRunMatchesNode`; built executables are named `<dir>_<name>`); `pts/tests/corpus/compare.sh` compares from the shell. A program that diverges carries `// skip: <cause>`; `PROGRESS.md` groups the causes.
- **Lowering:** `callbacks.go` (`guardedFilterResult`, helper key `narrowed`); `objects.go` (`impliedType`, `literalTarget`, `isObjectType` of intersections); `object_functions.go` (`assignToNewObject`); `lower.go` (`withArgumentTemps`, `inTemporaries`, `lastMaybePassed`, `maybeUndefinedFor`; `asCast`, `checkedCast`, `checkedConversion`; `isKeyedElement`, `keyedElementRead`, `wantKeyedMembers`; unions boxed for operators in `operation`; `neverFalsy` in `logical`; `staticSetterCall`; `constructorName`; `unnarrowed` in `valueExpression`; `hasValueOf` in `concatenation`; `holdsDynamicData` in `convert`); `union_objects.go` (`isMixedUnionReceiver`, `mixedUnionRead`); `dynamic.go` (`dynamicMethodCall`, `dynamicCallWith`); `tuples.go` (`tupleArray`, `tupleCopyMethods`); `enums.go` (`enumObjectType`, `enumObject`); `unions.go` (`recurring`, `recursiveUnions`, checked narrowing of property reads, `providedMember` by most properties); `exceptions.go` (`isDynamicData` narrowing); `classes.go` (static accessors in `declareStaticMember`, `staticsOf`); `generic_classes.go` (`declareGenericStatics`, `genericClass.statics` and `classes`); `generics.go` (instances keyed by literal type arguments, `isLiteralType`); `numbers.go` (`numberTestValue`).
- **IR, C and runtime:** `ir.InstanceOf.Family` (`familyInstanceOf`); `ir.ValueConstructorName` (`pts_value_constructor_name`, `string.c`); a function whose result may be undefined returns it when it ends without `return` (`holdsUndefined`); `pts_object_to_string` of tuples, maps and sets.
- **Tests:** `pts/tests/run/` `filter_type_guards`, `literal_arrays_implied`, `intersections`, `optional_arguments_computed`, `union_mixed_members`, `as_casts`, `keyed_elements`, `this_types`, `enum_objects`, `element_fallbacks`, `static_accessors`, `recursive_types`, `union_literal_members`, `constructor_names`, `generic_statics`, `narrowed_property_changed` (no-diff), `value_conversions_more`, `iteration_and_keys`; the corpus.

### 9.67 Corpus divergences (M3.7b)

- **Loose locals** (D-090): `pts/lower/loose_locals.go` (`collectUndefinedTests`, `testedOperands`, `isUndefinedTest`, `isTestedOperand`, `isLoose`, `readsElement`, `isElementRead`, `mayHoldUndefined`, `looseRead`); `bindingType` and `variableDeclaration` (`lower.go`), `elementPart` (`destructuring.go`), `narrowed` (`unions.go`), `shorthandValue` (`objects.go`), `logical`; element objects of `?.` and `takesAnyValue` (`chains.go`); `branch` in `sameType`; string elements in relational comparisons (`binary`), `isStringTyped` and union operands in `elementEquality`; `??` with both sides' types (`nullishCoalescing`); `isEqualityOperand` for JSON.stringify (`json.go`).
- **Lowering** (D-091): `convert` makes any dynamic value's conversion checked (`dynamicConversion`); `valueProperty` (`objects.go`) for `shapeProperty` and `unionProperty` receivers that are dynamic values; `KindSatisfiesExpression`; `{}` written as a type is a Value (`irType`); void values are undefined (`irType`), `voidResult` in `adapt` (`closures.go`); tuple rest parameters (`parameter`, `parameterBinding.elements`, `parameterPrologue`); `fitOverride` and `isCovariantResult` in `linkOverrides` (`classes.go`), `LeftOut` for parameters past the arguments (`convertArguments`); `isNullishLeft` for JSON.stringify; union results in `helperBuilder.truthy` (`callbacks.go`).
- **C:** `slotParams`, `slotPointerType`; method table entries of covariant overrides are cast, and `virtualCall` casts their results back (`classes.go`).
- **Runtime** (D-092): `uri.c` (`pts_uri_encode`, `pts_uri_decode`); `pts_json_parse_revived` (`json.c`). **IR:** `ir.URIEncode`, `ir.URIDecode`, `ir.JSONParseRevived`. **Lowering:** `uriFunction`, `uriCall` (`strings.go`); `computedKeyIn` (`union_objects.go`); revivers in `jsonParse`; `asOfDynamicVariable` in `as`; `MapGet` for records of dynamic values in `propertyPart`; `sameFields` in `providedMember`. Numeric-keyed object types (D-093): `hasOnlyNumericKeys`, `numericKeysValue` in `recordType` and `isHashMapType` (`records.go`); element reads whose type holds undefined read undefined (`expression`).
- **Tests:** `pts/tests/run/` `dynamic_object_values`, `void_values`, `tuple_rest_parameters`, `override_fits`, `loose_locals`, `uri_functions`, `computed_key_in`, `json_revivers`, `dynamic_casts_and_patterns`, `numeric_keys`; `errors/object_types_not_yet.pts` mixes a numeric name with another; `errors/unsupported_operator.pts` removed (computed `in` works); updated `errors/rest_parameters_not_yet.pts`, `class_features_not_yet.pts` and `inheritance_not_yet.pts` (tuple rests and fewer-parameter overrides work now); the corpus.
- **Array copies and function values as methods** (D-094): `isNewArray`, `isSingleUseConst`, `isReadOnlyArgument`, `onlyReads`, `isInCallbacksOf`, `isArrayRead` (`arrays.go`) for the array cases of `convert`; `tupleAsArray` (`tuples.go`); `functionAsMethod` and `adaptSkipping` (`closures.go`), `propertyValue` (`objects.go`), `objectTypeMethod` (`methods.go`) for `methodOf` and `propertyPart`; array-pattern locals in `isLoose`; `closureDynamicEntry` (`cgen/dynamic_calls.go`). **Tests:** `pts/tests/run/` `array_conversion_copies`, `function_properties_as_methods`, `c_name_clashes`; `errors/array_conversions_kept.pts`.
- **Values for object unions** (D-095): `objectUnionContext`, `objectUnion`, `contextualResult` and `resultTypeArguments` in `calledInstance` (`generics.go`), `PtsInferTypeArgumentsFromResult` (`internal/checker/pts.go`); `fromErased` (`closures.go`); `resultType` and `sameType` (`lower.go`); `inferredResult` in `literalContext` and `typeKey` (`objects.go`); uncovered intersections in `irType`; `narrowed` boxes values narrowed to dynamic-only types (`unions.go`). **Tests:** `pts/tests/run/object_union_contexts`.
- **Keyed spreads and own properties** (D-096): `isKeyedProperty`, `withKeyedProperties` in `spreadObjectLiteral` (`objects.go`); `pairParts` and the any result in `objectFromEntries`, `hasOwnPropertyCall`, `hasOwnProperty` (`object_functions.go`); `has_hidden_own`, `builtin_error_type` in `pts_value_has_own` (`dynamic_methods.c`); `isFunctionUnion` in `valueCall` (`closures.go`); `narrowed` in `tupleElementRead`. **Tests:** `pts/tests/run/keyed_spreads_and_own_properties`, `function_unions_and_tuple_narrowing`.
- **Literal methods and partial maps** (D-097): `objectMethod` (`methods.go`) adapts a method whose declared parameters differ from its field's and makes one for a function-typed property without `this`; `isPartialRecord` in `hasOnlyNumericKeys` (`records.go`); `ir.DynamicHas` makes descriptors carry declared types (`collectChecks`, `cgen/checks.go`). **Tests:** `pts/tests/run/literal_methods_and_partial_maps`.
- **Function name and length** (D-098): `functionProperty` in `propertyAccess`, `jsLength` (`lower.go`), `ir.Func.Length` set in `declareFunction`, `functionExpression`, `objectMethod`, `boundMethod`, `mathLength`, the conversion and Number-test values; `ir.FunctionName`/`FunctionLength`; `.length` in closure descriptors (`cgen/closures.go`); `pts_value_function_name`, `pts_value_function_length` (`inspect.c`) and functions in `pts_dynamic_get` (`dynamic.c`). **Tests:** `pts/tests/run/function_name_and_length`.
- **Enum names, `??` on hash-map objects, optional arguments** (D-099): `enumMember` (`enums.go`); `recordElementFallback` (`records.go`); `orLeftOut` in `arrayOperands` (`arrays.go`) and `methodCall`; `isNullish` in `sameType` (`lower.go`). **Tests:** `pts/tests/run/enum_names_and_optional_arguments`.
- **Optional and rest tuples** (D-100): `tupleType`, `tupleWithOptional`, `hasOptionalElements`, absent elements in `tupleLiteral`, `tupleAsArray` cut by `ir.TupleLength` (`tuples.go`); rest tuples as arrays in `isArrayExpression`/`isRestTuple` and `arrayLiteral` (`arrays.go`); `isArrayLiteralValue` and rest-tuple spreads in `argumentValues`; `unexplainedFailure` (`lower.go`); `pts_tuple_length` (`pts.h`) in inspect, JSON, join, dynamic get/has, spreads and conversions. **Tests:** `pts/tests/run/optional_and_rest_tuples`; updated `errors/tuples_not_yet.pts`, `arrays_not_yet.pts`, `class_features_not_yet.pts`, `rest_parameters_not_yet.pts`.
- **Dispatched generic methods** (D-101): `genericFunc.dispatched`, abstract and overriding generic methods in `declareGenericMethod`, `dispatchedGenericCall`, `erasedMethodSignature` (`generics.go`). **Tests:** `pts/tests/run/dispatched_generic_methods`; updated `errors/generics_not_yet.pts`.
- **Generic consts** (D-102): `declareGenericConsts`, `makesGenericInstances` (`generics.go`) from `declareFunctions`; `genericConsts` in `call` and `binding` (`lower.go`); `genericFunc.jsName`; expression bodies in `instanceBody`. **Tests:** `pts/tests/run/generic_values_classes` (no longer skipped).
- **Narrowing and fallbacks** (D-104): `isNarrowedMember` in `isNarrowing` (`unions.go`); the `never` operand in `asCast` (`lower.go`); `fallbackOf` in `contextTakesUndefined` (`unions.go`); `maybePassed` for arrows with defaults in `adaptedArguments` (`closures.go`); `isPlainShape`, `isUnionOfObjects` in `assertedLiteral` (`lower.go`). **Tests:** `pts/tests/run/` `nullable_narrowing_edges`, `defaults_through_function_types`.
- **Frozen mark** (D-105): `pts_value_freeze`, `pts_value_is_frozen` and the frozen table (`object.c`); `ir.ValueFreeze`, `ir.ValueIsFrozen`; `freeze` and `isFrozen` in `objectFunctionCall` (`object_functions.go`). **Tests:** `pts/tests/run/object_is_frozen`, `frozen_marks_go_with_their_objects` (`runtime/tests/object_test.c`).
- **Collections by context, recursive object types, uninhabited members** (D-106): `untypedCollectionContext` (`maps.go`) in `newMap` and `nullishCoalescing`; `typesInProgress`, `recursiveTypes`, `membersMaking`, `noteRecursion` in `shape` and `interfaceOf` (`objects.go`); `holdsMembersOf`, `isUninhabited` in `toUnion` (`unions.go`); `returnsGenericObjects` in `makesGenericInstances` (`generics.go`). **Tests:** `pts/tests/run/` `untyped_new_map_contexts`, `recursive_unions_generic_consts`.
- **Undeclared array parameters of callbacks** (D-107): `helperKey.arrayUnused`, `nthParam`, `declaresFewer` in `callbackMethodCall` and `callWith` (`callbacks.go`). **Tests:** `pts/tests/run/callback_unused_array_parameter`.
- **Spread copies in their source's order** (D-108): `orderOf` in `spreadObject` (`objects.go`); `ir.NewObject.OrderOf`; `pts_orders_<shape>` in `shapeDescriptors` and `pts_object_take_order` in `newObject` (`cgen/classes.go`, `pts.h`). **Tests:** `pts/tests/run/spread_copies_keep_order`.

### 9.68 Generators (M4.1a)

- **IR** (D-109): `ir.Func.Generator`; the statement `ir.Yield`; the expressions `ir.Resumed` and `ir.ResumedBy` (`ir.ResumeMode`); the builtins `ir.IteratorOpen`, `IteratorResume`, `IteratorValue`, `IteratorClose`, `IteratorTake`.
- **Lowering** (`pts/lower/generators.go`): `isIteratorType` and `iteratorResultValue` in `irType` (iterator types are `Value`; results are `iteratorResultShape`s), `statementYield` and `yieldingStatement` from `statement`, `yieldExpression`, `yieldPoint` and `resumeChecks`, `delegation` for `yield*`, `iteratorMethodCall` and `iteratorResult` for `next`/`return`/`throw`, `iteratorForOf` (with `withFinally`, factored out of `tryStatement` in `exceptions.go`, and `forOfBinding` in `arrays.go`), `iteratorTake` from `destructure`, `Array.from` of dynamic values (`arrays.go`); `isIteratorResultDeclaration` makes the result interfaces' members shape properties (`isObjectTypeMember`); `unionProperty` reads a union of one shape as that shape.
- **C** (`pts/cgen/generators.go`): `collectGenerators`/`layOutFrame` (frame members, resume points, the try statements around each), `frameStructs`, `generatorFunction` (starter and step function), `dispatch`, `yieldStatement`, `generatorReturn`; frame locals in `scope`, `LocalDecl`, `tryStatement` (entry labels and dispatch inside), `returnStatement`; `capturePrologue` split from `closurePrologue`.
- **Runtime** (`generator.c`): `pts_iterator`, `pts_generator_new`, `pts_iterator_resume` (and runtime iterators over an iterable's items, `pts_iterator_open`), `pts_iterator_value`, `pts_iterator_close`, `pts_iterator_to_array`, `pts_iterator_take`; `pts_handler.generator` markers in `pts_throw` (`error.c`); generator objects in `pts_value_iterable`, inspect (`Object [Generator] {}`) and `String()` (`[object Generator]`).
- **Tests:** `pts/tests/run/generators_*` (16 programs, also run under Node); `pts/tests/errors/yield_positions.pts`.

### 9.69 Iterables and iterators (M4.1b)

- **Lowering** (D-110): `MapIterator`/`SetIterator`/`ArrayIterator`/`StringIterator` in `iteratorTypes` and `isIterableType` (`generators.go`), both `Value` in `irType`; `keys`/`values`/`entries` as values in `mapMethodCall` (`ir.MapIteratorOf`) and `arrayIterator` (`isArrayIteration`, `array_library.go`); `iteratorForOf` opens what it iterates (`ir.IteratorOpen`) and runs after `mapIterableOf` and the array iterations; `newMapOf` (`maps.go`) for `new Set`/`new Map` of iterables and of another kind's items; sets and maps in array patterns (`destructure`); `libCollection` finds `ReadonlyMap` by its interface; `holdsWithin` for properties of type any.
- **Runtime** (`generator.c`): map and set iterators (`map_iterator`, `pts_map_iterator_new`, `pts_map_iterator_remaining` for inspect); `[Map Iterator] { … }` in `format_value` (`inspect.c`).
- **Tests:** `pts/tests/run/iterables_and_map_iterators`, `iterators_as_values`.
- **The protocol over the program's objects** (D-111, `pts/lower/iteration.go`): `memberName`/`propertyName` name `[Symbol.iterator]` `@@iterator` (`unsupportedMember`, `declareMethod`, `objectMembers`, `objectLiteral`); `hasIteratorMethod`/`isIterationSource` send for…of over such values to `iteratorForOf`, and spread, `Array.from`, `new Set`/`new Map` and array patterns over class, shape and interface values to `ir.ValueToArray` (`isPlainObject`); `getIteratorCall` lowers `x[Symbol.iterator]()` to `ir.IteratorGet`; `iteratorMethodCall` opens with `ir.IteratorOf`; generator methods of object literals (`objectMethod`); iterator result literals take D-109's shapes (`literalShape`, `fitsLiteral`, `objectUnion`). C: `implementsIteration` turns on declared types (dynamic entries); generator closures' descriptors have the parent `pts_generator_function`; stack names show `[Symbol.iterator]`. Runtime (`generator.c`): `object_iterator` and `resume_object_iterator`, `pts_iterator_of`, `pts_iterator_get`, `pts_value_has_iterator_method`, `pts_iterator_open` and `pts_value_iterable` for objects with `@@iterator`; `pts_name_is_symbol` in inspect (`Symbol(Symbol.iterator): …`, last), `own_keys` and JSON; `[GeneratorFunction: name]`.
- **Yields inside expressions** (D-111, `pts/lower/yield_expressions.go`): `statement` sends a generator's statement whose `hoistableExpression` has yields not first in it to `hoistingStatement`: `hoistYields` (with `evaluationOrder`, `referenceOperands`, `isSettled`, `keep`, `hoistYield`, `hoistConditional`, `hoistShortCircuit`) fills `l.yielded` and `l.kept`, which `expressionOf` and `valueExpression` read, and the statement is lowered after its hoisted statements.
- **Tests:** `pts/tests/run/iteration_protocol_classes`, `iteration_protocol_literals`, `iteration_protocol_printing`, `generators_yield_expressions`; `pts/tests/errors/yield_positions.pts` (the positions left).

### 9.70 Async functions and promises (M4.2a)

- **IR** (D-112): `ir.Func.Async` (with `Generator`); awaits are `ir.Yield`s; the builtins `PromiseNew`, `PromiseThen`, `PromiseFinally`, `PromiseResolve`, `PromiseReject`, `ValueIsPromise`.
- **Lowering** (`pts/lower/promises.go`): `isAsync`/`isStateMachine` in `declareFunction`, `functionExpression` and `objectMethod`; `isPromiseType` in `irType`; `awaitPoint` through `suspensionPoint` (from `yieldingStatement` and `hoistYield`; `isSuspension` in `statementYield`, `hasYield`, `hoistYields`); `newPromise` from `newExpression`; `promiseCall` for `then`/`catch`/`finally`/`resolve`/`reject` (other statics are PTS1001); `instanceof Promise` in `instanceOf`; labeled statements in `hoistableExpression`.
- **C**: async starters return `pts_async_start(&pts_frame->header)` (`generatorFunction`); `main` calls `pts_run_microtasks()` in programs that use promises (`usesPromises`); promise builtins turn on declared types (`collectChecks`).
- **Runtime** (`promise.c`): `pts_promise`, reactions that become jobs (`enqueue_reaction`, `settle`, `perform_then`, `resolve` with thenable jobs, `promise_resolve`), resolving functions and finally's closures (runtime closures with a `.call` entry), `run_reaction`/`run_thenable`, `pts_run_microtasks` (one handler for async bodies, `check_rejections`), `async_step`/`async_suspended`/`async_resume`, `pts_async_start`, the API functions; `pts_iterator.promise` and `.reaction`; `format_promise` (`inspect.c`), `[object Promise]`; `pts_report_uncaught` exported from `error.c`; the 4 MiB initial heap (`runtime.c`).
- **Tests:** `pts/tests/run/promises_*`, `async_*` and `unhandled_rejection` (all run under Node too); `pts/tests/errors/promises_not_yet.pts`.

### 9.71 Promise's combinators and top-level await (M4.2b)

- **IR** (D-113): `PromiseAll`, `PromiseAllSettled`, `PromiseAny`, `PromiseRace`, `PromiseWithResolvers`.
- **Lowering** (`promises.go`): the statics in `promiseMethods` (combinators take their argument's text); `promiseTypes` includes the runtime's objects, read dynamically (`isRuntimeObject` in `isAny`, `dynamic.go`); `declareAggregateError` (`errors.go`); `hasTopLevelAwait` makes the entry module's `Init` async (`Lower`); the message for other modules.
- **C**: `main` keeps the entry module's promise (`pts_entry`) and calls `pts_check_top_level` after the microtasks; `layOutFrame` leaves global locals out of frames.
- **Runtime**: `record_of`, `combination`/`element_function`/`perform_combination`/`combine`, `pts_promise_all`/`_all_settled`/`_any`/`_race`/`_with_resolvers`, `pts_check_top_level` (`promise.c`); `pts_type_builtin_AggregateError`, `pts_aggregate_error_init`/`_new` (`error.c`).
- **Tests:** `pts/tests/run/promise_combinators`, `top_level_await*` (also run under Node); `pts/tests/errors/top_level_await_imported`.

### 9.72 The event loop (M4.3)

- **Prelude** (D-114): `Timeout`, `Immediate`, `setTimeout`, `setInterval`, `setImmediate`, `clearTimeout`, `clearInterval`, `clearImmediate`, `queueMicrotask`, `Performance`/`performance`.
- **IR**: `TimerSet`, `TimerClear`, `TimerRef`, `TimerHasRef`, `TimerRefresh`, `ImmediateSet`, `ImmediateClear`, `QueueMicrotask`, `PerformanceNow`.
- **Lowering** (`pts/lower/timers.go`): `timerCall` (prelude functions by symbol, `Timeout.*` and `Performance.now` by `libMemberName`), `boxedArguments`, `anyArray`; `isTimerType` in `irType`.
- **C**: `usesLoop` (timers, immediates) makes `main` call `pts_loop_init()` first and `pts_run_loop()` after the microtasks; `queueMicrotask` counts as using promises.
- **Runtime** (`loop.c`): `loop_state` (uncollectable; libuv's loop and handles malloc'd), the timer heap (`schedule`, `unschedule`, `sift_up`/`sift_down`, `arm`), `on_timer`, `run_callback`, `pts_timer_*`, immediates (`on_check`, `pts_immediate_*`), `pts_run_loop`, `pts_performance_now` (origin set in `pts_runtime_init`); `pts_queue_microtask` (`promise.c`).
- **Tests:** `pts/tests/run/timers`, `timers_async`, `timers_uncaught` (also run under Node).

### 9.73 Async iteration (M4.4)

- **IR** (D-115): `ir.Func.AsyncGenerator`, `ir.Yield.Await`; `AsyncIteratorOpen`, `AsyncIteratorResume`, `AsyncIteratorClose`, `IteratorResultDone`, `IteratorResultValue`.
- **Lowering** (`pts/lower/async_iteration.go`): `stateMachine` (flags of declarations, expressions and literal methods), `asyncYieldPoint` (from `suspensionPoint`), `asyncIteratorMethodCall`, `asyncForOf` with `awaitStatements` (from the for…of dispatch in `arrays.go`), `isAsyncIteratorType` in `irType`; `wellKnownSymbol`/`memberName`/`propertyName`/`jsMemberName` for `@@asyncIterator` (`iteration.go`); `hasTopLevelAwait` sees `for await`; `isIteratorResultUnion` (`union_objects.go`); promise members left out of `objectUnion`.
- **C**: async generator starters return `pts_async_generator_new`; yields in them set `header.awaiting`; closure descriptors' parents `pts_async_function`/`pts_async_generator_function`.
- **Runtime** (`promise.c`): `pts_request`, `async_generator_drain`/`_step`/`_returned`, `complete_request`, `iterator_result`; the async-from-sync iterator (`from_sync_resume`); `pts_async_iterator_open`/`_resume`/`_close`, `pts_iterator_result_done`/`_value`; `pts_iterator.requests`, `.awaiting`, `.executing`.
- **Tests:** `pts/tests/run/async_generators`, `for_await_top_level`, `function_kinds_print` (also run under Node); `pts/tests/errors/async_generators_not_yet.pts`.

### 9.74 The pts: modules, process and path (M4.5a)

- **Library** (`pts/lib`): `process.pts`, `path.pts` embedded next to the prelude; `lib.ModulePaths` (the `paths` option), `IsModulePath`, `ModulePath`, `ModuleSpecifier`, `GlobalModules` (D-116).
- **Front end** (`pts/frontend/load.go`): the `paths` option; `globalModulesUsed`/`usesGlobal` put `pts:process` first in `Modules` when the program uses the global.
- **Lowering** (`pts/lower/library.go`): `intrinsicCall`/`declareIntrinsic` (bodiless declarations in `pts:` modules are extern functions), `globalModuleBindings` (used by `followAliases`); positions name modules by specifier.
- **Runtime** (`process.c`): `pts_process_argv`, `_environ`, `_exec_path`, `_pid`, `_cwd`, `_exit`, `_set_exit_code`, `_write`; `pts_runtime_exit` returns `process.exitCode`'s code; frames show `pts:` module names (`add_source_frame`).
- **Tests:** `pts/tests/run/path_posix`, `path_module`, `process_basics`, `process_exit`, `process_exit_code` (also run under Node with `node:` modules); `pts/tests/errors/pts_module_unknown.pts`; front-end tests of resolution and order.

### 9.75 pts:fs, synchronous (M4.5b)

- **Library** (`pts/lib/fs.pts`): the functions, `Stats`, `Dirent`, `constants`, the `FileSystem` default export; ports of Node's `stringToFlags`, MKDirpSync (`makeDirectories`), `readdirSyncRecursive` (`readTree`), `getDirent` (`direntOf`) and `realpathSync`; `NodeJS.ErrnoException` (prelude) is `pts:process`'s `ErrnoException` (D-117).
- **Lowering:** `overloadResult`/`isOverloadSignature`/`implementationOf` (`lower.go`): calls of overloaded functions and methods; `isBuiltinFieldOfObjectType`: Error's fields on object types extending it; `unsupportedOverloadCall`/`notSupportedYetTag` (`library.go`): the `@notSupportedYet` tag.
- **Runtime:** `fs.c` (`pts_fs_*` over `uv_fs_*` synchronous calls); `pts_system_error`, `pts_type_builtin_SystemError`, `pts_uv_error_new`, `pts_throw_uv_error` (`error.c`); `pts_string_from_utf8_lossy`, `pts_string_to_utf8` (`string.c`), also for argv and the environment.
- **Tests:** `pts/tests/run/fs_sync`, `fs_errors`, `fs_uncaught`, `overloads` (also run under Node); `pts/tests/errors/fs_buffers_not_yet.pts`; `string_test.c` (lossy decoding).

### 9.76 pts:fs, promises (M4.5c)

- **Library:** `pts/lib/fs/promises.pts` (`pts:fs/promises`); `pts/lib/internal/fs.pts` (`pts:internal/fs`, what the two modules share); `lib.IsInternalModule`, embedded subdirectories (`WrapFS`'s DirectoryExists) (D-118).
- **Front end:** `internalImports` reports imports of `pts:internal/…` outside the library (PTS1002).
- **Lowering and C:** `ir.Func.Settles` (an intrinsic returning a Promise, `declareIntrinsic`), which sets cgen's `usesLoop`.
- **Runtime:** `fs.c` jobs (`run`, `finish`, `run_now`, `start`/`work`/`done` over `uv_queue_work`); `pts_promise_pending`, `pts_promise_settle` (`promise.c`); `pts_uv_loop` (`loop.c`).
- **Tests:** `pts/tests/run/fs_promises` (also run under Node); `pts/tests/errors/pts_module_internal.pts`.

### 9.77 The `parallel` keyword (M5.1)

- **Fork:** `ast.NodeFlagsPtsParallel`; `internal/parser/pts.go` (`isPtsParallelOperator`, `parsePtsParallelOperator`, `isPtsParallelFor`, `parsePtsParallelFor`) with three hooks in `parser.go` (D-120, `docs/FORK.md`).
- **Prelude:** `declare function parallel<T>(task: T): Promise<Awaited<T>>`.
- **Check** (`pts/check/parallel.go`): `parallelOperator` (a shadowing binding; a function operand).
- **Lowering:** `isParallelCall` (`library.go`); both forms are PTS1001 until M5.2 and M5.5.
- **Tests:** `internal/parser/pts_test.go`; `pts/tests/errors/parallel_not_yet.pts` (now `parallel_unsupported.pts`), `parallel_forms.pts`; `pts/tests/run/parallel_identifier.pts`.

### 9.78 The multi-loop runtime and tasks (M5.2)

- **Lowering** (`pts/lower/parallel.go`, D-121): `parallelCall` makes the operand the body of a closure `<function>.parallel` of no parameters returning the value boxed (an arrow for `this`), and calls `ir.Parallel` with it; `suspensionIn` reports an await or yield in the operand.
- **C:** `ir.Parallel` is `pts_parallel`; it sets `usesLoop` and `usesPromises` (`checks.go`).
- **Runtime:** `scheduler.c` (`loop_slot`: libuv loop, wake handle, inbox of messages and of tasks, `sleeping`, `load`; `pts_post`, `take`, `run_inbox`, `steal`, `wait_for_work`, `run`, `pts_run_loop`, `start_workers`, `pts_configured_threads`, `pts_place_task`, `pts_task_finished`, `pts_uv_loop`); `scheduler_internal.h` (`pts_message`, `pts_current_loop`); in `promise.c`, a promise's `home` and a reaction's `owner`, `post`, the routing in `settle`, `resolve`, `perform_then` and `enqueue_reaction`, `add_reaction`, `pts_message_run`, `run_task`, `pts_parallel`, atomic `done` in resolving functions; `loop.c`'s timers on `pts_uv_loop()`; `pts_begin_exit` (`runtime.c`), taken by every path that ends the process.
- **Tests:** `pts/tests/run/parallel_tasks`, `parallel_io`, `parallel_stress` (now `pts/tests/stress/tasks_gc`, D-130), `parallel_unhandled`, `parallel_worker_uncaught`, `parallel_exit`, `parallel_unsettled` (each under several `PTS_THREADS`, the new `// threads:` directive); `pts/tests/errors/parallel_unsupported.pts`; `pts/runtime/tests/scheduler_test.c` (2,000 tasks on 4 loops, under ASan and UBSan).

### 9.79 The fork-join pool (M5.3)

- **Runtime** (`pool.c`, D-122): `deque` (Lê et al.'s Chase–Lev deque: `push`, `pop`, `steal` returning `CONTENDED` on a lost race; `grow` into a collected buffer), `worker` (deque, `own_loop`, random start for `steal_any`), `deques[]` registered under `deques_lock` and published by a release store of `deque_count`, `work_until` (own deque, then stealing, `SPINS_BEFORE_SLEEP` yields, then `sleep_until_changed`), `notify` (Rayon's sleepy count and event counter), `execute` (sets `pts_current_loop` to the job's loop, with `PTS_LOOP_FOREIGN` on another thread), `start_pool`, `current_worker`, `pts_join` (`join_job` on the stack, `run_stolen`, `run_catching`), `try_split`, `run_part`, `pts_par_run`, `pts_pool_threads`. `PTS_LOOP_FOREIGN` and `pts_configured_threads` (`scheduler_internal.h`); promise.c masks it for homes and owners, and `pts_queue_microtask` posts from a foreign thread; `own_slot` throws on a pool worker.
- **Toolchain:** `toolchain.ThreadSanitize` (`-fsanitize=thread`).
- **Tests:** `pts/runtime/tests/pool_test.c` (join, a stolen second closure, splitting, nesting, the leftmost throw, microtasks queued by pool threads running on the loop), under ASan/UBSan and, with `scheduler_test.c`, under TSan with `GC_DONT_GC=1` (`TestThreadedRuntimeTestsPassUnderThreadSanitizer`).

### 9.80 `.par` views and pts:parallel (M5.4a)

- **Library** (`pts/lib/parallel.pts`, D-123): `ParallelArray<T>`, `ParallelRange`, `Range`, `range`, `join`, `ParallelCallback<F>`/`ParallelBrand`, over the intrinsics `pts_parallel_for`, `pts_parallel_reduce`, `pts_parallel_join`. **Prelude:** `Array<T>.par`.
- **Front end:** `usesParallelView` (`load.go`) adds `lib.ParallelModule` to the modules run.
- **Lowering** (`parallel.go`): `parallelView` (`xs.par` as `ir.New` of tsc's class instance), `withoutParallelBrand`/`isParallelBrand`, used by `irType` and `functionType`.
- **Runtime** (`pool.c`): `closure_op`, `operate` (makes the loop's inbox, absorbs its messages after the operation), `pts_parallel_for`, `pts_parallel_reduce`, `pts_parallel_join`; `pts_absorb_messages` (`scheduler.c`), `pts_message_apply` (`promise.c`).
- **Tests:** `pts/tests/run/parallel_views`, `parallel_views_microtasks` (each under several `PTS_THREADS`).

### 9.81 `.par.flatMap`, `sort` and `sortBy` (M5.4b)

- **Library** (`pts/lib/parallel.pts`, D-124): `flatMap`, `sort`, `sortBy` on `ParallelArray` and `ParallelRange`; `compareDefault`, `permute`, `sortInPlace`, `sortRun`, `mergeRuns`, `search`; the intrinsic `pts_parallel_threads` (`pool.c`).
- **Tests:** `pts/tests/run/parallel_sort` (under several `PTS_THREADS`).

### 9.82 Typed arrays and ArrayBuffer (M5.4c1)

- **Runtime** (`typed_array.c`, `pts.h`, D-125): `pts_typed_array` (a view: buffer, data, length, byte offset) and `pts_arraybuffer` (bytes in the same allocation, never scanned); a descriptor per typed array (`PTS_KIND_TYPED_ARRAY`, element kind; `PTS_TYPED_ARRAYS` lists them) and `pts_type_builtin_ArrayBuffer` (`PTS_KIND_ARRAY_BUFFER`); inline `pts_X_get`/`pts_X_set`/`pts_X_new`/`pts_X_from_array`/`pts_X_from_typed`/`pts_X_on_buffer` per typed array; `pts_number_to_uint8_clamped`; `pts_typed_array_*` for the methods. console.log (`format_typed_array`, `format_array_buffer` in `inspect.c`), JSON (the generic object path over `own_keys`), ToString (`string.c`), reads, writes, `in`, keys, spreads and iteration through `any` (`dynamic.c`, `dynamic_methods.c`, `typed_array_methods`).
- **Lowering** (`typed_arrays.go`): builtin classes per typed array and one for ArrayBuffer (and SharedArrayBuffer's type) in `declareTypedArrayClasses`; `extern`/`callExtern` make the runtime functions; `typedArrayElement`, `typedArrayStore`/`typedArrayRead` (places), `typedArrayProperty`, `newTypedArray`/`typedArrayOf`/`iterableItems`, `newArrayBuffer`, `typedArrayCall` (statics, `typedArrayMethodCall`, `typedArraySet`, `typedSortWith`), `typedArrayIteration`; callback methods through `callbackHelper` (`helperKey.typed`, `typedMap`, `typedFilter`); `iterable` copies a typed array into a `number[]`; `forOfStatement` reads it live; `reportUnsupportedTypedArray` from `irType`.
- **Tests:** `pts/runtime/tests/typed_array_test.c`; `pts/tests/run/typed_arrays`, `typed_array_uses`, `typed_array_errors_dynamic`; `pts/tests/errors/typed_arrays_not_yet`; corpus `text/hex_dump`, `oop/matrix_vector`.

### 9.83 `.par` on typed arrays (M5.4c2)

- **Library** (`pts/lib/parallel.pts`, D-126): `ParallelTypedArray<A extends TypedArrayLike>`, `compareNumbers`; `sortRun` and `mergeRuns` generic over `Indexed<T>` (an array or a typed array), `sortRun` taking the leaf sort (`sortPiece`). **Prelude:** `par` on each of the nine typed arrays.
- **Lowering:** `typedArrayProperty` makes `ta.par` with `parallelView`.
- **Tests:** `pts/tests/run/parallel_typed_arrays` (under several `PTS_THREADS`).

### 9.84 `parallel for` and the guardrail (M5.5)

- **Check** (`pts/check/parallel.go`, D-127): `parallelBodies` (run per module before the numeric pass), `parallelFor`, `parallelForJumps`, `parallelCallback`/`isParallelCallbackType`, `guardrail`, `assignedOutside`, `reportOuterVariable`, `isParallelCall`; codes `ParallelAssignment` (PTS3001) and `ParallelFor` (PTS3002) in `pts/diagnostic`.
- **Lowering** (`parallel.go`): `parallelFor` (from `forOfStatement`) makes the leaf closure (`from`, `to`; the loop's own label as its only jump target) and calls `pts_parallel_for_each`; `parallelForItems`, `parallelRangeClass`. `extern`/`callExtern` moved to the lowerer (`l.externs`).
- **Runtime** (`pool.c`): `pts_parallel_for_each`, `for_each_leaf`, `PTS_PARALLEL_FOR_RUN`.
- **Tests:** `pts/runtime/tests/pool_test.c` (`a_throw_in_a_parallel_for_stops_the_runs_not_started`); `pts/tests/run/parallel_for` (under several `PTS_THREADS`); `pts/tests/errors/parallel_guardrail`.

### 9.85 pts:sync: atomics, Mutex and Once (M5.6a)

- **Library** (`pts/lib/sync.pts`, D-128): `AtomicInt`, `Atomic<T>`, `MutexGuard<T>`, `Mutex<T>` (`#release` hands the lock to the first waiter of `lock()`), `Once`.
- **Runtime** (`sync.c`): `pts_atomic_load`/`store`/`add`/`compare_exchange` on an 8-byte cell, `pts_sync_lock`/`try_lock`/`unlock` (a futex mutex any thread may release, spinning 100 times first) on a 4-byte word, both the first bytes of an ArrayBuffer.
- **Tests:** `pts/runtime/tests/sync_test.c` (also under TSan); `pts/tests/run/sync` (under several `PTS_THREADS`).

### 9.86 pts:sync: Channel (M5.6b)

- **Library** (`pts/lib/sync.pts`, D-129): `Channel<T>` (`#next`, used by `recv` and the async generator `[Symbol.asyncIterator]`), `Received<T>`, `WaitingSender<T>`, over `pts_sync_lock` (D-128).
- **Tests:** `pts/tests/run/channel` (under several `PTS_THREADS`).

### 9.87 Stress tests and the race-safety invariant (M5.7a)

- **Harness** (D-130): `pts/tests/stress/*.pts` (run-test directives; `// expect:` and `// threads:` required), `TestStress` (`pts/tests/stress_test.go`; `PTS_STRESS_REPEAT` runs per thread count), `make test-stress`, the nightly workflow `.github/workflows/pts-nightly.yml` (100 runs).
- **Stress programs:** `tasks_gc` (was `run/parallel_stress`), `par_gc`, `sync_contention`, `join_recursion`, `promises_cross_loop`, `parallel_for_throws`; **race tests** of §4.1: `race_arrays`, `race_maps`, `race_fields`, `race_strings`, `race_typed_arrays`.
- **Runtime:** `map.c` (`find` and `index_entry` stop after one round of probes; `replace` sizes from the live entries; an append after a replace re-checks room; `pts_map_size` reads a signed size below 0 as 0; the record key-order snapshot is collected memory); `array.c` (`pts_array_load`, one load of an element or false for a hole; `copy_elements` in growth and `unshift`; `shift` of an empty buffer and `with` past its copy throw RangeError; `pts_array_sort_with` loads each slot once); `pts_set_of_array` and `filter` through `any` skip holes; `pts_typed_array_sort` sorts a private copy.
- **Tests:** `pts/runtime/tests/map_test.c` (a size below zero, a full index), `array_test.c` (loads and holes, a set of an array with a hole, a shift of a length its buffer lacks).

### 9.88 Parallel benchmarks and the collector's threads (M5.7b)

- **Benchmarks** (D-131): `pts/bench/mandelbrot` (`.par.forEach` over rows into a Uint16Array), `sum_reduce` (100 million doubles filled by `parallel for`, then `.par.sum`, `.par.reduce` and `.par.max`), `word_count` (64 generated files counted by `.par.map` into maps), `fan_out` (64 uneven CPU-bound tasks started with `parallel`); each has `main.ts`, its sequential twin for Node and Bun, a goroutine twin `main.go`, and a `threads` file (`1 2 4 8`), which makes `run.sh` run ParallelScript once per `PTS_THREADS` value and print a table with the speedup over one thread.
- **Runtime:** `pts_runtime_init` sets bdwgc's marker threads to 2 (`GC_set_markers_count`) before `GC_INIT`; `GC_MARKERS` overrides it.

### 9.89 The RegExp core (M6.1a)

- **PCRE2** (D-132): `pts/third_party/pcre2` (submodule, pcre2-10.49), `pts/third_party/pcre2-config` (`config.h` from `config.h.generic` with Unicode support, `pcre2.h` and `pcre2_chartables.c` copied from the `.generic` and `.dist` files), `pcre2Library` (`pts/toolchain/libraries.go`): the 8-bit library, no JIT.
- **Runtime** (`regexp.c`): `pts_regex_compile` (`parse_flags`; `scan_groups` counts the groups and keeps their names; `translate` writes PCRE2's syntax: `escape`, `character_class`, `group_open` with `(?ims-ims:` modifiers, `braces_quantifier`, `line_start`/`line_end`/`dot`; V8's messages through `fail` and `syntax_error`), compiled with `PCRE2_UTF | PCRE2_UCP | PCRE2_MATCH_INVALID_UTF | PCRE2_ALLOW_EMPTY_CLASS | PCRE2_MATCH_UNSET_BACKREF` and the extra options `ASCII_BSD`, `ASCII_BSW` and (without `u`) `CASELESS_RESTRICT`, all memory from the collector (`general_context`); `pts_regex_exec` with a `pts_regex_cursor` (`seek_unit`, `seek_byte`); `pts_regex_group_count`, `pts_regex_group_name`, `pts_regex_flags`, `pts_regex_escape_source`.
- **Tests:** `pts/runtime/tests/regexp_test.c` (expected results from Node).

### 9.90 RegExp in the language (M6.1b)

- **Runtime** (D-133, `regexp_object.c`): `pts_regexp` (`pts_header`, `lastIndex`, the compiled `regex`, the `pattern` as written) with descriptor `pts_type_builtin_RegExp` (kind `PTS_KIND_REGEXP`); `pts_regexp_literal` (code compiled once into the literal's static, `pts_regex_N`), `pts_regexp_new`, `pts_regexp_call`, `pts_regexp_source`, `pts_regexp_flags`, `pts_regexp_has_flag`, `pts_regexp_to_string`, `pts_regexp_exec` and `pts_regexp_test` (`match`: RegExpBuiltinExec's lastIndex rules), `pts_regexp_escape`; match arrays (`pts_match_array`: a `pts_array` of strings, then `index`, `input`, `groups`; descriptor `pts_type_match_array`; copies take `pts_type_match_copy` through `pts_array_copy_type`), `pts_match_index`, `pts_match_input`, `pts_match_groups`; groups are a hash-map object of strings named `[Object: null prototype]`, a zero value in a hash-map object of pointers being a hole that `pts_map_get` doesn't find. `dynamic.c` (`regexp_get`, `match_array_get`, lastIndex writes), `dynamic_methods.c` (`regexp_methods`; `refuse_regexp` for `split`/`replace`/`replaceAll` through `any`), `inspect.c` (`format_match_properties`; holes of match arrays and their copies print as undefined), `string.c` and `array.c` (ToString), `pts_array_index_error` (a match array's hole is a TypeError).
- **Compiler:** `ir.RegExpLiteral` (cgen: `regexCaches`, a `static pts_regex *` per literal); `pts/lower/regexps.go`: `declareRegExpClass` (the field `lastIndex`), `regexLiteral`, `regexpConstruction`, `regexpCall`, `regexpProperty`, `matchArrayProperty`, `isMatchArrayType`/`isMatchArrayOrEmpty` (RegExpExecArray, RegExpMatchArray and `RegExpExecArray | []` are `string[]` in `irType` and `isArrayType`), `isMatchElement`/`matchElement` (loose element reads, D-090); `logical` takes a match element as loose.
- **Tests:** `pts/runtime/tests/regexp_object_test.c`; `pts/tests/run/regexp_objects.pts`, `regexp_exec.pts`, `regexp_dynamic.pts`, `regexp_uses.pts` (all compared with Node), `regexp_parallel.pts` (`// threads: 1 2 4`); `pts/tests/errors/regexp_not_yet.pts`; `TestRegexLiteralsKeepTheirCompiledCodeInAStatic` (cgen).

### 9.91 String methods with RegExps (M6.1c1)

- **Runtime** (D-134, `regexp_string.c`, `regexp_internal.h`): `pts_string_match`, `pts_string_match_all` (an any[] of match arrays behind `pts_iterator_get`), `pts_string_search`, `pts_string_split_value` (`split_regexp`: @@split with the sticky splitter's matches found by searches), `pts_string_replace_value` (`replace_regexp`, `substitution` and `reference`: GetSubstitution), `pts_regexp_replace_matches`, `pts_regexp_replacer_arguments`, `pts_regexp_replace_with`; the global loops' `next_match` and `next_replaced` keep their position to themselves; `pts_regexp_match` (shared with `regexp_object.c`) and `pts_regex_slice` (`regexp.c`) carry cursors. Through `any`: `string_match`, `string_match_all`, `string_search`, and `split`/`replace`/`replaceAll` with RegExps (`replace_regexp_with_callback` calls `pts_dynamic_call_closure`).
- **Compiler** (`pts/lower/regexp_strings.go`): `regexpStringCall` (from `regexpCall`; `mayBeRegExp` keeps string patterns on D-082's lowering), `regexpReplace`, `regexpReplaceHelper`/`replaceMatches`/`replacerCall` (the callback's helper: its parameters read an any[] of the match's arguments); `RegExpStringIterator` is an iterator type.
- **Tests:** `pts/runtime/tests/regexp_string_test.c`; `pts/tests/run/regexp_string_methods.pts`, `regexp_dynamic.pts` (compared with Node), `regexp_parallel.pts` (a global RegExp shared by threads); `pts/tests/errors/regexp_replace_not_yet.pts`.

### 9.92 String.prototype.normalize (M6.1c2)

- **Tables** (D-135): `pts/runtime/gen/normalization_tables.mjs` writes `pts/runtime/src/normalization_tables.h` from Node's ICU: `decomposition_pool`, `canonical_decompositions` and `compatibility_decompositions` (full NFD and NFKD of each code point, Hangul syllables aside), `combining_classes` (ranks of the canonical combining classes, found by whether NFD swaps two marks), `compositions` (primary composites by their two characters).
- **Runtime** (`normalize.c`): `pts_string_normalize` (`parse_form`; `decompose`, `reorder`, `compose`, Hangul by arithmetic in `decompose` and `composite_of`; ASCII is returned as it is). Through `any`: `string_normalize`.
- **Compiler:** `stringNormalize` (`pts/lower/strings.go`).
- **Tests:** `pts/tests/run/string_normalize.pts`; the corpus parts `normalize_bmp`, `normalize_supplementary` (every code point of planes 0 and 1 and the CJK compatibility ideographs, in the four forms) and `normalize_sequences` (random marks, Hangul jamo and compatibility characters) of `TestGeneratedStringCorpusMatchesNode`.

### 9.93 pts:http's server on one loop (M6.2a)

- **Library** (D-136): `pts/lib/http.pts`: `Headers`, `Request`, `Response` (`unsignedShort` converts the status as WebIDL does), `ServeOptions`, `Server`, `serve`, and `answer` (a request's `Request`, the handler's `Response`, `pts_http_respond`) and `failed` (the `error` handler's response, or the error on stderr and a 500).
- **Runtime** (`http.c`): `pts_http_listen` (a libuv TCP listener on this loop; Node's `listen EADDRINUSE: …` message), `on_connection`, `on_read`/`on_alloc` into a connection's buffer, `process` (one request at a time: `parse_head`, `dechunk`, 100 Continue, 400/413/431 answered by the server), `dispatch` (the request callback, with the connection's id: generation and slot in the loop's `connection_table`), `pts_http_respond` (status line with the reason phrase, the program's headers without framing ones or line breaks, `content-length`, `connection: close` or HTTP/1.0's `keep-alive`), `pts_http_stop`. `scheduler.c` flushes stdout from a `uv_prepare` handle before each loop waits.
- **Compiler:** `ir.Func.Listens` (`loopIntrinsics` in `pts/lower/library.go`): a program that listens runs the loop; an object literal's method can fill an optional function-typed property (`fieldFuncType`), and a literal for a union whose one hash-map object member it fits is that hash-map object (`recordMemberOfContext`).
- **Tests:** `TestHTTPServer` (`pts/tests/http_test.go` with `pts/tests/http/server.pts`: routes, bodies, headers, HEAD, keep-alive, pipelining, HTTP/1.0, chunked bodies, 100-continue, malformed requests, handlers that throw, an address in use, `stop()`); `pts/tests/run/http_values.pts` (checked against Node's Fetch classes), `literal_union_contexts.pts`.

### 9.94 pts:http on every loop (M6.2b)

- **Runtime** (D-137): `http.c` keeps a `server` with a `listener` record per loop (its socket, its libuv handle, its connections). `pts_http_listen` calls `pts_start_loops` (`scheduler.c`) when `every_loop`, binds every socket on the calling thread (`listening_socket`, `bind_listeners`: SO_REUSEADDR, SO_REUSEPORT, the first socket's port for the rest), then `start_listening` (`uv_tcp_open`, `uv_listen`) runs on each loop: its own directly, the others by `pts_post_call` (`promise.c`'s CALL message). `pts_http_stop` sets `stopped` with a compare-exchange and runs `stop_listening` on each listener's loop. Connection ids are `(generation · 2⁸ + loop) · 2²⁰ + slot`; `connection_of` checks the loop. `date_line` formats the `date` header once a second per thread; `parse_head` rejects NUL and CR in values.
- **Regex matching** (`regexp.c`): `match_data` keeps one malloc'd `pcre2_match_data` per thread, grown for patterns with more groups, which PCRE2 also keeps its frames in; compiled patterns still use the collector's memory.
- **Library** (`pts/lib/http.pts`): `headerName` checks names against `TOKEN_CODES`; `headerValue` trims with a regex only when needed; `addReceived`, set in `Headers`' static block, adds the server's checked request headers; `#combined` joins a field's values for `get` and iteration.
- **Tests:** `TestHTTPServerEveryLoop` (`pts/tests/http/every_loop.pts` under `PTS_THREADS` 1 and 4: two handlers inside at once on different loops, timers and `parallel` from a request's loop, 64 requests over keep-alive connections, an atomic request count, `stop()` ending the program); `TestHTTPServer` checks the `date` header and the 400 for a CR in a value; `run/http_values.pts` covers the new name and value checks.
- **Benchmarks:** `pts/bench/http_hello`, `pts/bench/http_json` (`main.pts`, `main.ts` for Node, `bun.ts`, `main.go`, `request`, `threads`), run by `pts/bench/serve.sh` (`make bench-http`) with oha.

### 9.95 Loop-owned timers (M6.0a1)

- **Runtime** (D-139, `loop.c`): `pts_timer` records its `loop`; `active` and `referenced` are atomic, `counted` (whether the loop's count of referenced timers includes it) is the owner's. `pts_timer_clear` clears `active` with an exchange, then `on_owner` runs `remove_cleared` on the timer's loop (directly, or by `pts_post_call`); `pts_timer_ref` stores `referenced` and runs `apply_ref` there; `pts_timer_refresh` runs `reschedule` there. `on_timer` skips a timer whose `active` another thread cleared. Immediates' `active` is atomic, cleared by any thread and exchanged by `on_check`. `unschedule` now also takes the timer out of the referenced count.
- **Tests:** `pts/tests/run/timers_foreign.pts` (the review's probe 11, which crashed; clearInterval, unref/ref/hasRef and refresh from another loop; clearTimeout from a pool thread; a timer made on another loop) under `PTS_THREADS` 1, 2 and 4; `immediates_foreign.pts` (an immediate cleared from another loop before its loop reaches it); `scheduler_test.c` clears a main-loop timer from another loop under ASan and TSan.

### 9.96 Checked reads of narrowed variables, and captured locals' TDZ (M6.0a2a)

- **Narrowed reads** (D-140): `collectOutsideAssignments` (`pts/lower/outside_assignments.go`) marks the variables and parameters that a function other than their own assigns (`assignmentTargets`, `markAssignedOutside`, `functionOf`); `narrowed` (`unions.go`) boxes a narrowed read of one of them, and of any property or element, and converts it with a check (`FromUnion` from a `Value`), for every narrowed type but classes, whose extraction is checked anyway. `pts_value_convert` (`convert.c`) accepts an object of the check's own descriptor at once.
- **Early cells** (D-140): `ir.Local.Early` (set by the cell pass for a local captured before its declaration ran); `cellSize` gives its cell a `bool` after the value (`PTS_CELL_READY` in `pts.h`), the declaration sets it, and `local` (`cgen.go`) checks it with `pts_check_ready` where a closure reads or writes the cell.
- **Tests:** `pts/tests/run/tdz_captured.pts` (the review's probe 12, which crashed; strings, numbers, objects, writes, loop iterations; checked against Node), `narrowed_after_change.pts` (the review's probe 1, which crashed; variables changed to another object member or null; a property narrowed to an array).

### 9.97 Class TDZ and unset object fields (M6.0a2b)

- **Class TDZ** (D-141): `readyBinding` (`pts/lower/classes.go`) gives each non-generic class a module-scope binding `C.class`, declared after the class's static initializers (`lower.go`, the class statement); `ir.New.Ready` names it, and `newInstance` (`pts/cgen/classes.go`) checks its ready flag first where `readyCheck` says the declaration may not have run.
- **Unset fields** (D-141): `mayBeUnset` and `checkedFieldRead` (`classes.go`): a read of a class instance's field of an object type goes through `pts_check_field_set` (`pts.h`; `pts_throw_unset_field` in `runtime.c`), with the member read from it (`receiverMembers`: field reads and stores, method, virtual and interface calls) or none; `unsetAllowed` skips the check where the use sees NULL as undefined (`Truthy`, `Box` through `pts_value_from_unset_object`, `Eq`/`Ne` operands, a `Sequence`'s discarded value). The lowering tests objects' truth for real (`condition`, `truthOf`), lowers `x === undefined` and `x == null` of an object as `!x` (`nullishTest`), and lowers `??=` (`nullishAssignment`) and `??` of a field of a local's object (`unsetFieldFallback`) with a test.
- **Tests:** `pts/tests/run/class_tdz.pts`, `unset_fields.pts` (both against Node), `unset_fields_differences.pts`.

### 9.98 Checked conversions to primitives (M6.0a3)

- **Runtime** (D-142, `dynamic.c`): `call_conversion_method` calls a `toString` or `valueOf` method through its dynamic entry with no arguments; `ordinary_to_primitive` tries them in OrdinaryToPrimitive's order for the hint (Object.prototype's "[object Object]" where there is no toString, Node's TypeError where neither gives a primitive); `pts_value_to_primitive` and `pts_program_object_to_string` (which `pts_object_to_string` in `array.c` calls for instances and shapes) use it. `pts_call_getter` (was `call_getter`) is exported, and `object.c`'s optional-property accessors call through it and `pts_call_setter` by the accessor's kind.
- **C** (`pts/cgen/generators.go`): `definesConversions` (with `implementsIteration`, over `hasMethodNamed`) gives a program whose classes or shapes have a `toString` or `valueOf` method dynamic entries and declared types, as `collectChecks` does for programs that use `any`.
- **Tests:** `pts/tests/run/to_primitive.pts` (the review's probe 14, which crashed; results of other types, optional parameters, valueOf returning an object, neither giving a primitive; checked against Node), `to_primitive_parameters.pts`.

### 9.99 The shared-memory contract (M6.0a4)

- **Contract** (D-143): `docs/ARCHITECTURE.md` §4.13, the review's finding 10 and the owner's ruling 10.
- **Typed arrays** (`pts.h`, `typed_array.c`): `PTS_ELEMENT_LOAD`/`PTS_ELEMENT_STORE` (relaxed `__atomic_load`/`__atomic_store` of the element's C type) in `pts_typed_array_as_number`, `pts_typed_array_store` and every `pts_X_get`/`pts_X_set`; `copy_shared(to, from, count, unit)` is `memmove` by relaxed atomic units (`unit`, the element's size, up to 8 bytes where both addresses agree modulo 8: an alignment ladder at each end, then words), used by `set`, `slice`, `copyWithin`, `sort` (to and from its private copy), construction from a typed array and `ArrayBuffer.prototype.slice`; `reverse` swaps by `load_unit`/`store_unit`.
- **Generators** (`generator.c`, `error.c`): `pts_iterator_resume` claims a generator's `executing` flag (`claim`: a relaxed load and store while `pts_threaded` is false, an atomic exchange after) and `resume_claimed_generator` releases it on every way out; `pts_throw` releases the generators whose markers it passes. `set_value` records a resume's value in the thread's uncollectable `last_resumed` slot once the program is threaded, and `pts_iterator_value` reads it from there. `items_step` loads its position once; `map_step` steps a private `pts_map_iterator` made from one load each of the shared table and position (`next_entry`), and `follow` and `pts_map_next` keep the table and position in locals.
- **Threads** (`scheduler.c`, `pool.c`): `pts_threaded` is set before the first loop or pool thread is created. The main loop's slot is made by `make_main_slot` under its own `pthread_once`, from `own_slot` or from `start_workers` on any thread.
- **The audit's fixes** (D-143): `format_error` (`inspect.c`) reads `name`, `message` and `cause` once and allocates entries for every field; `format_slot` loads a string, value or object slot once; `pts_time_format` formats a time value (`pts_date_format` and `pts_date_to_json` call it with one load); `pts_cell_copy` uses `pts_copy_shared` (`shared_memory.c`, with `pts_load_unit` and `pts_store_unit`); `json.c`'s frames (`GC_REALLOC`) and reviver keys (`GC_MALLOC`) are collected memory.
- **Async generators** (`promise.c`): `pts_iterator.home` is the loop `pts_async_generator_new` ran on; `pts_async_iterator_resume` makes the request's promise on the calling loop and queues the request (`queue_request`) directly on the home loop or through `pts_post_call` from another loop or a pool thread.
- **Unhandled rejections** (`promise.c`): `perform_then` for another loop's promise stores `handled` (sequentially consistent) before it posts the THEN message; `check_rejections` loads it sequentially consistently; `add_reaction` and `settle` use relaxed accesses.
- **Tests:** `pts/runtime/tests/typed_array_test.c` (every alignment, overlap and length of `copyWithin` and `set` against `memmove`; `ArrayBuffer` slices from every offset), `typed_array_race_test.c` (threads racing element accesses and every bulk copy; under ThreadSanitizer too, which fails on the old `memcpy` code), `pts/tests/run/async_generators_foreign.pts` (one async generator drained from four loops at once: segfaulted before), `remote_rejection.pts` (a rejection in the same macrotask as another loop's await: reported unhandled before), `pts/tests/stress/race_iterators.pts` (a generator whose narrowed local two threads would break, and iterators over tuples, arrays, maps and sets shared by threads), `race_typed_arrays.pts` (now with bulk copies of doubles), `race_inspect.pts` (errors and dates printed and stringified while other threads change them), `run/parallel_in_par_callback.pts`, `run/json_cycle_collections.pts`, `object_test.c` (cell copies of every size).

### 9.100 Thenables (M6.0b1)

- **Runtime** (D-144, `promise.c`): `thenable` returns `NOT_THENABLE`, `THENABLE` or `THEN_THREW`, reading `then` once under a handler; `resolve` rejects on `THEN_THREW`; `run_thenable` calls the `then` it captured through `call_catching` (which lost its receiver and method-name parameters). `dynamic.c`'s `object_get` binds an object literal's method (a field holding a function whose shape lists a method of that name) with `bind_method`.
- **Tests:** `pts/tests/run/thenables.pts` (the review's probe 5; await, a class's then, first call wins, a then that isn't a function or throws; against Node).

### 9.101 Values read before a suspension (M6.0b2)

- **Lowering** (D-145, `pts/lower/yield_expressions.go`): `isSettled` keeps every property and element read, and settles a name only through `isUnchangedWhileSuspended` (constants, functions and the like; the function's own variables unless `assignedOutside`; outer variables unless `assignedAnywhere`, which `collectOutsideAssignments` now also fills); unary `+`/`-`/`~` and templates need primitive operands (`isPrimitive`); `hoistYields` keeps a template's non-primitive substitution as its string (`keepString`) and, for a compound assignment whose right side yields (`readsTargetFirst`), the target's value (`keepCurrent`, into `l.keptCurrent`, which `compoundAssignment` reads); `evaluationOrder` puts a called name before the arguments.
- **C** (`pts/cgen/order.go`): `effectsOf` handles every IR expression (constants, `Resumed`/`ResumedBy`; anything else reads, writes and throws); `builtinEffects` classifies builtins (`computedBuiltins`, the String/Number/Math members, `throwingBuiltins`, the order-dependent `Math.random` and `performance.now`, console); `ToString` of a dynamic value counts as a call.
- **Tests:** `pts/tests/run/suspension_snapshots.pts` (the review's probe 6; compound assignments to names, fields and elements; an element, a name and a callee read before an await; a template's toString before a yield; unchanged locals; against Node).

### 9.102 The iteration protocol's order (M6.0b3)

- **Runtime** (D-146, `generator.c`): `resume_object_iterator` reads `done`, then `value` only for a result that isn't done; a done result goes in `object_iterator.finished` with the value `PTS_VALUE_ABSENT`, which `pts_iterator_value` turns into a read of the result's `value`.
- **Lowering** (`exceptions.go`, `generators.go`, `async_iteration.go`): `withFinally` passes its `finallyContext` to the finally code; `iteratorClosing` (used by `iteratorForOf` and `asyncForOf`) closes the iterator inside a try that drops its error when the completion is a throw, and plainly otherwise.
- **Tests:** `pts/tests/run/iterator_close_order.pts` (the review's probe 13; break, return and non-object results; yield*'s done then value; for await; against Node).

