# ParallelScript architecture

This document explains how ParallelScript is built, for a developer joining the project. It describes what exists today (milestones M0 to M5 are done, M6 is under way) and says so where something is only planned.

It is an overview. For detail, see `docs/SPEC.md` (§9 has implementation notes for every task, down to function names), `docs/DECISIONS.md` (cited here as D-nnn), `docs/FORK.md` (every edit to upstream) and the study notes `docs/agent/NOTES-tsgo.md` and `docs/agent/NOTES-rayon.md`.

## 1. The big picture

ParallelScript compiles TypeScript (`.pts` files) ahead of time to native executables. It has a garbage collector and real parallelism. The compiler, `pts`, is a Go program inside a fork of typescript-go (TypeScript 7), pinned at `typescript/v7.0.2`. It lives in the fork's Go module because it needs the fork's `internal/` packages: the parser, binder and checker. New code goes in `cmd/pts/` and `pts/`. Upstream edits are few, marked `// PTS:` and listed in `docs/FORK.md` (D-L03).

The pipeline (D-L04):

```
.pts sources ──┐
prelude.d.ts ──┼─► front end ─► PTS checks ─► lowering ─► IR ─► C emitter ─► clang -O2 ─► executable
pts: modules ──┘   (forked tsc)  pts/check    pts/lower   pts/ir  pts/cgen     + runtime and vendored C libraries
```

`pts/driver` runs the stages in order, `pts/toolchain` runs clang, and `cmd/pts` is the command line.

Three ideas shape the whole design:

- **C is never emitted from the TypeScript AST.** Lowering first builds a typed IR, where boxing, closure environments, monomorphized generics and the state machines of generators and async functions are explicit (D-L04).
- **Static types become native C types.** `number` is a `double`, a class instance is a pointer to a C struct. A one-word dynamic value, `pts_value`, is used only where a static type isn't enough: unions, `any`, `unknown` and `console.log`'s arguments (SPEC §9.1, D-002).
- **A data race can't crash the runtime.** Every value that threads can share is one machine word, or is read and written atomically. A racing program may see stale values, but never a torn value, a corrupted heap or an out-of-bounds access (the race-safety invariant, SPEC §4.1, D-L13). This rule shapes values, arrays, maps and strings below.

## 2. The compiler, stage by stage

`driver.EmitC` runs the front end, the checks, the lowering and the C emitter. `driver.Build` then writes the C to a temporary directory and compiles it. Each stage returns diagnostics, and any error stops the pipeline. tsc's errors and ParallelScript's print in one format, `file:line:col - error CODE: message` (D-006).

### 2.1 Front end: the forked tsc (`pts/frontend`, `pts/lib`)

`frontend.Load` builds a typescript-go `compiler.Program` and type-checks it.

- **Fixed options** (D-005). No `tsconfig.json` is read. Every program is `strict`, targets ESNext with ES modules, uses `moduleResolution: bundler` and `moduleDetection: force` (every file is a module), and has `lib: esnext` with no DOM and no `@types`.
- **The prelude.** The root files are the entry file and `ptslib:///prelude.d.ts`. `pts/lib` embeds its files in the compiler binary, and `lib.WrapFS` serves them under the `ptslib:///` URL root.
- **`pts:` imports.** The compiler option `paths` maps `pts:*` to `ptslib:///*.pts`, so `import … from "pts:path"` resolves to the embedded `pts/lib/path.pts` (D-116). This needs no fork edit.
- **Errors.** As in tsc, syntax errors hide semantic errors.
- **Module order.** `Program.Modules` lists the source files in ES module evaluation order, entry last (D-022). `import type` requests nothing. If a module uses the global `process` or a `.par` view, the front end runs `pts:process` or `pts:parallel` first.
- An import of `pts:internal/…` outside the library is an error (D-118).

The fork's own edits (`docs/FORK.md`) are in three areas:

- **The `.pts` extension.** `.pts` is a TypeScript extension, and an extensionless import tries `x.pts` first (`internal/tspath`, `internal/core`, `internal/module`; D-001, D-004).
- **Integer types in the checker.** `internal/checker/pts.go` makes the prelude's aliases `int`, `i8` … `u64` and `f32` number types of their own, and types arithmetic on them by `pts/numeric`'s rules (D-048). It also exports a few helpers that the lowering uses to instantiate generics (D-049, D-078, D-095).
- **The `parallel` keyword.** `internal/parser/pts.go` parses `parallel e` as a call of the prelude's `parallel<T>(task: T): Promise<Awaited<T>>`, and `parallel for` as a for statement. Both are marked with a new node flag, `NodeFlagsPtsParallel` (D-120). There are no new AST node kinds, so tsc types `parallel e` with no checker change.

### 2.2 PTS semantic checks (`pts/check`, `pts/numeric`, `pts/diagnostic`)

`check.Program` enforces the parts of the spec that tsc doesn't know. It takes the modules in order, dependencies first, and walks each one three times:

- **Features left out by design** (`unsupported.go`, SPEC §3.10, D-020): `var`, `eval`, `new Function`, `with`, `arguments`, `Proxy`, `Reflect`, BigInt, namespaces, symbols other than `Symbol.iterator` and `Symbol.asyncIterator`, and so on. Each is error PTS1002 with a reason.
- **The guardrail** (`parallel.go`, SPEC §5.6, D-127). A *parallel body* is the operand of `parallel`, a `parallel for` body, or a function passed for a `ParallelCallback` parameter (the `.par` methods and `join`). Inside one, assigning a `let` or parameter declared outside is PTS3001. A malformed `parallel for` is PTS3002.
- **Numeric types** (`check.go`, `functions.go`, `numeric.go`; SPEC §3.2, D-012). The pass works out a numeric type for every number-typed expression and variable. It checks that implicit conversions are lossless (PTS2001), that literals fit their type (PTS2002), and that mixed operands have a common type (PTS2003). It records the results in `check.Info` for the lowering.

`pts/numeric` holds the numeric rules, such as `Unify` and `ImplicitlyConvertible`. Both this pass and the fork's checker use it. `pts/diagnostic` defines the diagnostic type and the PTS codes.

### 2.3 Lowering (`pts/lower`)

`lower.Program` turns the checked AST into an `*ir.Program`. It is the largest package, about 40 files named after what they handle (`classes.go`, `closures.go`, `generics.go`, `unions.go`, `dynamic.go`, `generators.go`, `promises.go`, `parallel.go`, …).

**Lowering defines the supported subset.** Any construct it doesn't handle is reported as PTS1001, "… is not supported yet" (D-006), so nothing unknown is silently miscompiled.

The order of work (`Program` in `lower.go`):

1. Declare every class, function and module-scope binding of every module, the runtime's classes (Error, Date, the typed arrays, RegExp) first. Code can then use any of them above its declaration and across import cycles.
2. Lower each module's top-level code into that module's `main` function.
3. Lower the function bodies, then the generic instances they need, until none are left.
4. Run whole-program passes over the IR: `markCells` decides which captured variables live in heap cells (D-041), `checkRelaxedCasts` checks casts of objects that lack an optional property (D-066), and `prepareDynamicTypes` readies the types that writes and calls through `any` convert to (D-072, D-074).

The lowering asks tsc's checker for types everywhere and maps them to IR types (`irType`). Its main choices:

- **Generics are monomorphized.** Each generic function or class gets one instance per distinct list of IR type arguments (D-049, D-050). A generic function used as a value where no single instance fits becomes an erased copy over dynamic values (D-078).
- **Narrowing is tsc's.** Where tsc narrows a union, the lowering inserts a conversion (D-062).
- **`finally` is lowered** to plain tries and jumps, so the C emitter never sees it (D-068).
- **Generators and async functions stay structured.** `yield` and `await` become `ir.Yield` statements. A yield inside a larger expression is hoisted into frame locals first (D-109, D-111, D-112).
- **Array methods with callbacks** (`map`, `filter`, …) become generated helper functions, one per array type and callback type (D-042).
- **`parallel e`** becomes a closure with no parameters, passed to `ir.Parallel`. **`parallel for`** becomes a closure over a range of indices, passed to the pool (D-121, D-127).

### 2.4 The IR (`pts/ir`)

The IR is structured, not SSA, and fully typed (D-L04). `ir.go` defines about 80 types:

- **Types** are compared by identity. The primitives are `Number` (C `double`), `Bool`, `String`, `Value` (a boxed `pts_value`), `Undefined`, `Null`, `Void`, and the integer types and `f32`. The composite types are `Class` (a class, or a *shape*: the struct of an object literal's type, D-033), `Interface` (a structural object type, D-035), `Array`, `MapType` (maps, sets and hash-map objects), `Union`, `FuncType` and `Rest`.
- **Statements** are `Block`, `LocalDecl`, `If`, `Loop`, `Switch`, `LabeledBlock`, `Break`, `Continue`, `Return`, `Try`, `Throw` and `Yield`. Each carries a `Pos` (a file and a line), which becomes a `#line` directive.
- **Expressions** make every runtime step explicit, for example `Box`, `ToUnion` and `FromUnion`, `Convert` (between numeric types), `ClassCast`, `MakeClosure`, `CallValue`, `VirtualCall`, `InterfaceCall`, `ArrayCall`, `MapCall`, and `CallBuiltin` for runtime functions.

`Program.String()` prints the IR as text, and the lowering tests compare that text. For example, `TestClassesLowerToFieldsAConstructorAndMethods` in `pts/lower/lower_test.go` expects this (excerpt; the numbers are source lines):

```
class Point { x: number; label: string }
func main() void {
  8: let p: Point = new Point(1)
  9: (p.x = (p.x + Point.twice(p)))
  ...
func Point.twice(this: Point) number {
  5: return (this.x * 2)
}
```

`ir.Walk` and `ir.WalkExpr` (`walk.go`) visit the tree. There is no separate package of IR passes. The passes that exist run at the end of lowering (§2.3), or as analyses inside the C emitter (§2.5).

### 2.5 C emission (`pts/cgen`)

`cgen.Emit` writes one C11 translation unit that includes only `pts.h` (D-007). It first collects what the program needs (closures, descriptors, runtime type checks, method tables, string literals, globals, generator frames), then writes the declarations, the descriptors, `main` and every function.

What the generated C looks like:

- **Functions** are `static` C functions named `pts_fn_<name>`. The entry module's top-level code is `pts_main`, and another module's is `pts_module_<name>` (D-020, D-022). `main` calls `pts_runtime_init`, then each module in order, then `pts_runtime_exit`. Programs that use promises or timers also drain the microtask queue and run the event loop.
- **Names.** Locals keep their source names where C allows it (`names.go`).
- **`#line` directives.** One precedes each function, and each statement whose line the C compiler doesn't already presume, so none is redundant (D-007). gdb, lldb and stack traces show `.pts` lines. The library modules' lines name the module, as in `#line 415 "pts:parallel"`.
- **Evaluation order.** C leaves the order of evaluating operands unspecified, and JS doesn't. `order.go` finds operands that could tell the difference, and those go through `pts_tmpN` temporaries in a statement expression, `({ … })`, a GNU extension that clang supports (D-019).
- **Classes** become structs `pts_class_<Name>` whose first member points to the descriptor, plus a static descriptor `pts_type_<Name>` (D-024, D-026).
- **The stack guard.** A function that may recurse starts with `pts_stack_check()` (D-069).
- **`try`** is a `_setjmp` on a handler pushed onto the thread's chain of handlers (D-068).
- **Generators and async functions** each become a frame struct, a starter and a step function, which starts with a `switch` on the resume point (D-109).

**Example.** `docs/examples/arch_point.pts` runs as a test, and it prints the same under Node:

```ts
class Point {
  constructor(public x: number, public y: number) {}
}

function lengthOf(p: Point): number {
  return Math.sqrt(p.x * p.x + p.y * p.y);
}

const p = new Point(3, 4);
console.log(p, lengthOf(p)); // Point { x: 3, y: 4 } 5
```

`pts-bin/pts emit-c docs/examples/arch_point.pts` prints this (trimmed, paths shortened):

```c
struct pts_class_Point {
  const pts_type *pts_header;
  double x;
  double y;
};
...
static const pts_type pts_type_Point = {
  .kind = PTS_KIND_OBJECT,
  .gc_descr = PTS_GC_NO_POINTERS,
  .name = "Point",
  ...
};
...
int main(int argc, char **argv) {
  pts_runtime_init(argc, argv);
  pts_register_function_names(pts_function_names, sizeof pts_function_names / sizeof pts_function_names[0]);
  pts_main();
  return pts_runtime_exit(0);
}

#line 1 "/…/docs/examples/arch_point.pts"
static void pts_main(void) {
#line 11 "/…/docs/examples/arch_point.pts"
  pts_class_Point *p = ({ pts_class_Point *pts_tmp1 = pts_object_new(&pts_type_Point); pts_fn_Point_constructor(pts_tmp1, 3.0, 4.0); pts_tmp1; });
  ({ pts_value pts_tmp2 = pts_value_from_object(p); pts_value pts_tmp3 = pts_value_from_number(pts_fn_lengthOf(p)); pts_console_log(2, (pts_value[]){pts_tmp2, pts_tmp3}); });
}
...
static double pts_fn_lengthOf(pts_class_Point *p) {
  return __builtin_sqrt(((p->x * p->x) + (p->y * p->y)));
}
```

Some things to notice:

- `lengthOf` takes a struct pointer and computes in doubles, with no boxing. Values are boxed only to pass them to `console.log`.
- `Point` holds no pointers, so its descriptor says `PTS_GC_NO_POINTERS`, and the collector never scans its instances.
- `pts_function_names` maps C names to JS names for stack traces (D-069).

### 2.6 Toolchain and driver (`pts/toolchain`, `pts/driver`)

`pts/toolchain` builds the C libraries, caches them, and compiles and links programs (D-O11, D-003).

- **Finding the runtime.** `FindHome` uses `$PTS_HOME`, else the nearest ancestor of the `pts` executable (or of the working directory) that contains `pts/runtime/include/pts.h`. So `pts-bin/pts`, built inside the repo, needs no setup.
- **Building libraries.** The toolchain compiles six static archives with clang and `ar`, without CMake or make: `libptsrt.a` (the runtime), `libryu.a`, `libpcre2.a`, `libuv.a`, `libgc.a` (bdwgc as one translation unit) and `libbacktrace.a`. `libraries.go` holds their source lists and defines, copied from each library's own build files.
- **The cache.** Each archive goes to `~/.cache/pts/<name>-<key>/lib<name>.a` (`$PTS_CACHE_DIR` overrides the root). The key hashes the compiler version, the flags and the sources (for vendored libraries, their version header). So a change to the runtime rebuilds only `libptsrt.a`. A file lock and an atomic rename let parallel processes share the cache.
- **Profiles.** `Release` builds the runtime and programs with `-O2 -g -ffp-contract=off -fno-math-errno -falign-functions=32`. `-ffp-contract=off` stops clang from fusing `a*b+c`, which JS's rounding forbids (D-011); the alignment keeps timings from depending on code placement (D-119). `Sanitize` (ASan and UBSan) and `ThreadSanitize` are for the runtime's C tests.
- **Linking.** `clang <flags> -std=c11 main.c <archives> -lpthread -ldl -lm -static-libgcc`. The executable needs only glibc's `libc` and `libm` (`TestProgramDependsOnlyOnLibc`). `$PTS_CC` and `$PTS_AR` replace clang and ar.

### 2.7 The CLI (`cmd/pts`)

| Command | What it does |
|---|---|
| `pts build file.pts [-o out]` | Compiles to an executable, named after the file by default |
| `pts run file.pts [args…]` | Builds into a temporary directory and runs the program with the arguments; forwards Ctrl-C to it |
| `pts emit-c file.pts [-o file.c]` | Prints the generated C, or writes it to a file |
| `pts help` | Prints the usage |

Exit codes (D-006): 0 on success, 1 for compile or toolchain errors, and 2 for a malformed command line. `pts run` exits with the program's own code, or with 128 plus the signal number if a signal killed it. `pts test` (M7.3) and `pts lsp` (M7.1) are planned.

## 3. The `pts:` library modules (`pts/lib`)

The standard modules are written in ParallelScript (D-116). Each is a `.pts` file embedded in the compiler. It is compiled with the program that imports it, like the program's own modules.

| Module | Source in `pts/lib/` | Contents |
|---|---|---|
| `pts:process` | `process.pts` | Node's `process`; its default export is also the global `process` |
| `pts:path` | `path.pts` | Node's POSIX `path`, ported line by line |
| `pts:fs`, `pts:fs/promises` | `fs.pts`, `fs/promises.pts`, `internal/fs.pts` | Node's file API, synchronous and promise-based (D-117, D-118) |
| `pts:parallel` | `parallel.pts` | `.par` views, `range` and `join` (D-123, D-124, D-126) |
| `pts:sync` | `sync.pts` | `AtomicInt`, `Mutex`, `Once` and `Channel` (D-128, D-129) |

A module reaches the runtime through **intrinsics**: functions it declares without a body, named `pts_…`. For example, in `parallel.pts`:

```ts
declare function pts_parallel_for(leaf: (from: number, to: number) => void, count: number): void;
```

The lowering turns such a declaration into an external function, so a call is a direct call of the runtime's C function of that name, with the arguments converted to the declared types (`intrinsicCall` in `pts/lower/library.go`). Only bodiless declarations inside `pts:` modules count, and they aren't exported, so user code can't call them. Intrinsics stay thin: the modules build their classes and objects in ParallelScript, and the runtime does only what needs C.

`prelude.d.ts` declares the globals that `lib.esnext.d.ts` lacks: `console`, the numeric type aliases, timers, `process` (as `import("pts:process").Process`), `parallel<T>`, and `par` on arrays and typed arrays (as `import("pts:parallel").ParallelArray<T>` and friends).

**Example.** `docs/examples/arch_parallel.pts` uses both parallel features, and runs as a test under 1, 2 and 4 threads:

```ts
function fib(n: number): number {
  return n < 2 ? n : fib(n - 1) + fib(n - 2);
}

const task = parallel fib(25);
const squares = [1, 2, 3, 4].par.map((x) => x * x);
console.log(squares, await task); // [ 1, 4, 9, 16 ] 75025
```

Three things happen in its C, shown trimmed below:

- The top-level `await` makes the entry module an async function, so its body is a step function over a frame.
- `parallel fib(25)` calls `pts_parallel` with a closure whose code runs `fib(25)`.
- `.par` makes a `ParallelArray` of `pts:parallel`. That is a generic class whose `map` is instantiated for `number`, and it calls the pool's intrinsic.

```c
#line 7 "/…/docs/examples/arch_parallel.pts"
  pts_frame->task = pts_parallel(pts_value_from_object((&pts_static_parallel)));
  pts_frame->squares = pts_fn_parallel_ParallelArray_map_number(({ pts_class_parallel_ParallelArray *pts_tmp1 = pts_object_new(&pts_type_parallel_ParallelArray); pts_fn_parallel_ParallelArray_constructor(pts_tmp1, pts_array_of(&pts_array_types[PTS_FIELD_NUMBER], (double[]){1.0, 2.0, 3.0, 4.0}, 4)); pts_tmp1; }), (&pts_static_arrow));
  pts_frame->header.value = pts_frame->task;
  pts_frame->header.state = 1;
  return false;
  pts_resume_1: pts_frame->header.state = PTS_GENERATOR_RUNNING;
...
#line 7 "/…/docs/examples/arch_parallel.pts"
static pts_value pts_fn_parallel(pts_closure *pts_env) {
  pts_stack_check();
#line 7 "/…/docs/examples/arch_parallel.pts"
  return pts_value_from_number(pts_fn_fib(25.0));
}
...
static pts_array *pts_fn_parallel_ParallelArray_map_number(pts_class_parallel_ParallelArray *this, pts_closure *callback) {
  pts_stack_check();
#line 30 "pts:parallel"
  pts_array *items = this->items;
  pts_array *results = pts_array_of_length(&pts_array_types[PTS_FIELD_NUMBER], pts_array_length(items));
  pts_parallel_for(pts_new_closure_parallel_ParallelArray_map_number_arrow(results, callback, items), pts_array_length(items));
#line 37 "pts:parallel"
  return results;
}
```

## 4. The runtime (`pts/runtime`)

The runtime is written in C11 and linked statically into every program as `libptsrt.a` (D-L05). It has four parts:

- `include/pts.h` is the only header that generated C includes. It declares the whole runtime, with inline fast paths for array access, numeric operations, try handlers and the stack check.
- `src/` holds the implementation: 36 `.c` files, one per area (see the table in §7). The `*_internal.h` headers are shared by a few files and aren't part of the API.
- `gen/*.mjs` are Node scripts that generate Unicode tables from Node's ICU into `src/*_tables.h`.
- `tests/*_test.c` are the C unit tests (§6).

### 4.1 Values

Static types compile to native C types: `number` → `double`, `boolean` → `bool`, `string` → `pts_string *`, `int`/`i64` → `int64_t`, `u8` → `uint8_t`, …, `f32` → `float`, and a class instance → a pointer to its struct (SPEC §9.1, §9.4).

Where a static type isn't enough (a union, `any`, `unknown`, an argument of `console.log`), the value is a `pts_value`: one 64-bit word (D-002). The encoding is the one JavaScriptCore uses:

| Word | Meaning |
|---|---|
| `0x0000_PPPP_PPPP_PPPP` | A raw pointer to a heap object (never below `0x10000`) |
| A double's bits + 2^49 | A number (NaN canonicalized) |
| `0x02`, `0x06`, `0x07`, `0x0a` | `null`, `false`, `true`, `undefined` |
| `0` | "Absent": an optional property that an object leaves out (D-065) |

This is not classic NaN-boxing, which hides tag bits in pointers. Here pointers stay raw, so the conservative collector still sees them. Being one word, a value can't tear when threads race. A 64-bit integer stays a number while a double holds it exactly, and is boxed beyond ±2^53.

### 4.2 Heap objects and descriptors

Every heap object starts with a pointer to its type's descriptor, a static `pts_type` (D-024). The descriptor holds:

- `kind`: `PTS_KIND_OBJECT`, `PTS_KIND_ARRAY`, `PTS_KIND_MAP`, `PTS_KIND_RECORD`, `PTS_KIND_FUNCTION`, `PTS_KIND_PROMISE`, `PTS_KIND_REGEXP`, and so on.
- `gc_descr`: bdwgc's mark descriptor, saying which words of an instance hold pointers. It must be the descriptor's second word, because bdwgc reads it there.
- `name`, `parent` (the superclass) and `size`.
- `fields` (name, offset and kind) and `methods` (name, kind and function). Programs that need them for `any` also give these entries declared types and dynamic entries (§4.6).
- For collections, the kinds of their elements and keys.

One descriptor serves the collector, `console.log`, JSON, `instanceof` and the lookups of `any`. `pts_object_new(type)` allocates a zeroed instance (`object.c`). An instance with pointers comes from bdwgc's gcj allocator, which scans only the words `gc_descr` names. An instance without pointers is allocated atomic and never scanned.

### 4.3 Strings

A `pts_string` is the header, flags (bit 0 means pure ASCII), the byte length, the UTF-16 length (JS's `.length`, so reading it is O(1)) and a pointer to the bytes (D-014, D-061). Strings are immutable, so threads share them freely. A plain string's bytes follow its header in one block that the collector never scans.

- **WTF-8.** The bytes are UTF-8, except that a lone surrogate (which a JS string may hold) is stored as its 3-byte encoding. Concatenation joins surrogate halves at the seam, so each string value has one representation, and byte equality is string equality.
- **UTF-16 indices.** Indices count UTF-16 code units, as in JS. An ASCII string's units are its bytes, so ASCII strings take O(1) paths (`string_methods.c`).
- **Building strings is linear.** A long enough concatenation puts its result in an append buffer, and the string points into it. When a string's bytes end where its buffer's used part ends, concatenating onto it claims the next bytes with a compare-and-swap instead of copying (`string.c`, D-061).

### 4.4 Arrays, maps, sets and hash-map objects

- **Arrays** (`array.c`, D-038). A `pts_array` is a descriptor, a buffer and a length, and the buffer holds its own capacity. Elements are stored unboxed by kind: a `number[]` holds doubles. Each element is copied with one load or store, and bounds checks use the buffer actually being accessed. So racing pushes may lose elements, but never write out of bounds.
- **Map and Set** (`map.c`, D-046). An ordered hash table: entries are appended in insertion order, deleted ones are marked in place, and an open-addressing index finds them. Growing or compacting publishes a new table instead of resizing in place. Iterators follow the chain of tables, which gives JS's live iteration.
- **Hash-map objects** (D-059). Object types with index signatures, such as `Record<string, T>`, use the same table with kind `PTS_KIND_RECORD`. `JSON.parse` makes these for objects (D-080).
- **Typed arrays** (`typed_array.c`, D-125). A view (buffer, data pointer, length and offset) of an `ArrayBuffer`.

### 4.5 Classes, object types and closures

- **Classes** (D-026, D-028). Each class is a struct, and a subclass's struct embeds its parent's first. Method calls are direct, unless the method is abstract or overridden below the receiver's static class. Then the call goes through a vtable (`pts_vtable_<C>`) inside the descriptor.
- **Object types** (D-033, D-035, D-036). An object literal's type is a *shape*: one struct shared by every object type with the same properties of the same types. A value typed by an interface may be any object that has the interface's properties. So an access through an interface finds the field by name, through a one-word inline cache at the access site (`pts_site`: the last descriptor met there and the field's offset). Method calls through interfaces use the descriptor's method table the same way.
- **Closures** (D-041). A `pts_closure` is a descriptor, a code pointer and the captured values. A captured variable that may change after it is captured lives in a heap cell, which the closures share.
- **Errors** (D-068). Error and its subclasses are classes the runtime defines (`pts_error`, `error.c`). So `extends Error` and `instanceof RangeError` work, also on the errors the runtime throws.

### 4.6 Dynamic values and `any`

`any` and `unknown` are `pts_value`s, and operations on them dispatch at run time through the descriptors (D-070).

- **Reads, writes and operators** (`dynamic.c`): properties by name, and operators with JS's conversions.
- **Conversions to static types** (`convert.c`, D-071). An object that already has the target type is kept as it is. Dynamic data, such as an `any[]` or parsed JSON, is checked and copied into the target's representation. A mismatch throws, for example, `TypeError: Expected number, got string`.
- **Calls through `any`** (D-074). In a program that makes them, each function value and method gets a *dynamic entry*. The entry takes boxed arguments, checks them against the parameter types and boxes the result (`pts/cgen/dynamic_calls.go`).
- **Builtin methods through `any`** (`dynamic_methods.c`, D-076): the methods of strings, numbers, arrays and functions, found by name.
- **Type checks** (`pts_check`) are emitted only for programs that need them (`pts/cgen/checks.go`, D-071, D-072).

### 4.7 Exceptions and stack traces

These live in `error.c` (D-068, D-069).

- **Throwing.** Each `try` pushes a `pts_handler`, which holds a `jmp_buf`, onto a thread-local chain and calls `_setjmp`. `pts_throw` long-jumps to the innermost handler. Calls cost nothing, and entering a try costs one `_setjmp`. Locals declared outside a try block and assigned inside it are `volatile`, as C requires.
- **Uncaught errors.** An uncaught exception prints `Uncaught …` and the stack to stderr, and exits with code 1 (D-O07).
- **Stacks.** An error captures return addresses when it is made, using libbacktrace. The first read of `stack` symbolizes them from the program's own DWARF debug info. Thanks to `#line`, its line tables point at `.pts` lines. JS names come from the table that `main` registers.
- **Stack overflow.** Each thread has a stack limit. On x86-64, `pts_stack_check()` is one compare and one jump. On overflow it throws `RangeError: Maximum call stack size exceeded`.

### 4.8 console and inspect

`inspect.c` is a port of Node's `util.inspect` and `util.format`, driven by descriptors (D-027, D-088). `console.c` ports Node's Console (`log`, `error`, `group`, `count`, `time`, …), and `table.c` ports `console.table`. The aim is Node's output byte for byte, with Node's defaults (D-O16). Terminal widths of characters come from the generated `width_tables.h`.

### 4.9 Generators, async functions and promises

- **Generators** (`generator.c`, `pts/cgen/generators.go`, D-109). The C emitter keeps the body structured. Every statement-level local lives in a frame struct on the GC heap, which starts with a `pts_iterator`. A `yield` stores its value and its resume point, then returns. The step function starts with a `switch` that jumps to the resume point. There is no native stack per generator.
- **Async functions** (D-112) are generator functions whose yields are awaits. `pts_async_start` makes the function's promise and runs the body until its first await. Later steps run as microtasks.
- **Promises** (`promise.c`) follow ECMAScript's algorithms in V8's order of jobs, so programs print in Node's order. Each thread has a microtask queue, which `pts_run_microtasks` drains. An unhandled rejection ends the program with code 1.
- **Async generators and `for await`** (D-115) use the same frames, plus the spec's queue of requests.

### 4.10 Event loops and tasks

These live in `loop.c` and `scheduler.c` (D-114, D-121).

- **One loop per thread.** Each event loop is a libuv `uv_loop_t` on its own thread, and all loops share one heap. The main thread's loop runs the modules. The other loops start the first time `parallel` runs, or when a `pts:http` server listens with `reusePort`, so a program that uses neither has one thread. There are `PTS_THREADS` loops (at most 256), or one per core.
- **Timers** are Node's: a heap of timers behind one `uv_timer_t`, `setImmediate` on a check handle, and a microtask drain after every callback (`loop.c`).
- **Inboxes.** Each loop has an inbox of messages and tasks, guarded by a mutex, and a `uv_async_t` that other threads wake it with. Besides promise work and tasks, a message can run a runtime function on its loop (`pts_post_call`, D-137).
- **Promises never change on two threads.** A promise belongs to the loop that made it and settles only there; a reaction runs on the loop that added it. Settling another loop's promise is a message to that loop. So single-loop code takes no locks, and an async function resumes on the loop where it started.
- **Tasks.** `parallel e` calls `pts_parallel(closure)`, which makes a promise on the calling loop and posts a task to the least-loaded other loop. That loop runs the closure, and the promise settles back on the caller's loop. A loop out of work takes a task that another loop has queued but not started, then sleeps.
- **HTTP.** `http.c` is `pts:http`'s HTTP/1.1 server over libuv TCP (D-136): it parses requests, hands each to the library's callback with a connection id, and writes the response the library gives back. With `reusePort` (D-137) the calling thread binds a SO_REUSEPORT socket per loop and each loop listens on its own, so the kernel spreads connections over the loops; a connection stays on its loop.
- **Ending.** When every loop sleeps, none can wake another, and the program ends, as Node does when nothing is left to do. `process.exit`, an uncaught error and a panic take one process-wide exit lock first (`pts_begin_exit`).

### 4.11 The fork-join pool

The pool lives in `pool.c` (D-122). `docs/agent/NOTES-rayon.md` explains the design it copies.

- **Threads.** The pool has `PTS_THREADS` − 1 worker threads, separate from the loops, started at the first parallel operation. The thread that starts an operation works too, so operations run on `PTS_THREADS` threads.
- **Deques.** Each worker has a Chase–Lev deque of job pointers (Lê et al.'s version for weak memory models). The owner pushes and pops at the bottom, and thieves steal the oldest job from the top. A loop thread gets a deque the first time it starts an operation.
- **`pts_join(a, b)`** is Rayon's join. It pushes b, runs a, then pops b and runs it, unless a thief took it. In that case it runs other work until b is done. b's job lives on the caller's stack, so join allocates nothing. If either closure throws, join rethrows once both are done, a's error first.
- **Splitting.** `pts_par_run` splits a range of indices with Rayon's adaptive splitter. `pts:parallel` builds `.par` on the intrinsics `pts_parallel_for` and `pts_parallel_reduce`, which run on it, and `join` on `pts_parallel_join`, which is `pts_join`. The lowering calls `pts_parallel_for_each` for `parallel for`.
- **Promise work from workers.** Each job records the loop of its operation. Promise work that a callback does on a worker becomes messages to that loop, which the loop applies before the operation returns (D-123). Timers and asynchronous I/O from a callback on a worker throw `Error` for now.
- With `PTS_THREADS=1` there are no workers, and everything runs on the caller (D-O06).

### 4.12 Garbage collection

Memory is managed by bdwgc, the Boehm–Demers–Weiser collector (SPEC §4, D-024).

- **Build.** bdwgc is built with threads, parallel marking, thread-local allocation and gcj-style allocation (`pts/toolchain/libraries.go`). `pts.h` defines `GC_THREADS` before it includes `gc.h`, so every thread the runtime creates is registered with the collector.
- **What gets scanned.** Instances of classes and object types are scanned only in the words their descriptor names (every word, for instances of more than 62 words). Other blocks that hold pointers, such as the buffers of object arrays, map tables, cells and generator frames, are scanned conservatively, word by word, as are thread stacks and registers. Pointer-free blocks, such as plain strings, the buffers of number arrays and objects with only numbers, are never scanned.
- **Roots outside the heap.** Runtime structures that the heap doesn't reach, such as loop slots, deques, timer state and microtask queues, are allocated uncollectable, so the collector scans them as roots.
- **Tuning** (`pts_runtime_init` in `runtime.c`). A collection runs once the program has allocated as much as the heap holds (free-space divisor 1, D-060). The heap starts at 4 MiB (D-112), and marking runs on 2 threads (D-131). The environment variables `GC_FREE_SPACE_DIVISOR`, `GC_INITIAL_HEAP_SIZE` and `GC_MARKERS` override these.
- **Planned.** SPEC §4 plans a precise collector, and later a generational one. The master plan puts an experiment with a precise collector after M7.

### 4.13 Race safety

All threads share one heap, so a program can race on it. The runtime's guarantee (SPEC §4.1, D-L13, D-130) rests on the pieces described above:

- values are one word;
- an array's capacity is in its buffer;
- maps publish new tables instead of resizing in place;
- strings are immutable;
- inline caches pack the descriptor and offset into one word;
- a typed array's elements may tear, but no access leaves the view.

The `pts/tests/stress/race_*` programs test the guarantee.

### 4.14 RegExp and the Unicode tables

- **RegExp** (D-132 to D-134). `regexp.c` translates a JS pattern into PCRE2's syntax, keeping JS's meaning where the two differ. It compiles the pattern with PCRE2's 8-bit library in UTF mode, without the JIT, and reports matches in UTF-16 indices. `regexp_object.c` implements the `RegExp` class and match arrays. `regexp_string.c` implements the String methods that take a RegExp. A regex literal's compiled code is cached in a static.
- **Generated tables.** Four scripts in `gen/` read Node's ICU and write tables into `src/`:
  - `case_tables.mjs`: case mapping (D-016);
  - `collation_tables.mjs`: `localeCompare` (D-082);
  - `width_tables.mjs`: terminal widths for `console.log` (D-088);
  - `normalization_tables.mjs`: `normalize` (D-135).

  `normalize.c` implements the four normalization forms over its table. Regenerate the tables when Node's Unicode version changes. The generated string corpus tests fail until then.

### 4.15 The other built-ins

The remaining built-ins follow Node's and V8's algorithms, so that results match to the last digit: number to string with Ryu's digits and ECMAScript's notation rules (`number.c`, D-011), `toFixed`, `parseInt` and friends (`number_methods.c`, D-083), Math as V8's fdlibm converted to C (`ieee754.c`, `math.c`, D-084), JSON with V8's messages (`json.c`, D-080), Date (`date.c`, D-087), the URI functions (`uri.c`, D-092) and `localeCompare` (`collation.c`, D-082). `process.c`, `fs.c` and `sync.c` hold the intrinsics of `pts:process`, `pts:fs` and `pts:sync` (D-116 to D-118, D-128). `runtime.c` has startup (`pts_runtime_init`), the stack guard, `pts_panic` and the exit lock.

## 5. Vendored third-party code (`pts/third_party`)

These are git submodules, pinned in `.gitmodules` and listed in `docs/FORK.md`. A fresh clone needs `git submodule update --init pts/third_party`. `pts/toolchain` builds them all with clang, not with their own build systems (D-003).

| Directory | Version | Used for |
|---|---|---|
| `bdwgc` | v8.2.12 | The garbage collector |
| `libuv` | v1.53.0 | Event loops, timers, file system calls, and the thread pool behind `pts:fs/promises` |
| `ryu` | v2.0 | Shortest round-trip digits for number-to-string conversion (D-011) |
| `libbacktrace` | commit 0b9b49c (it has no release tags) | Capturing and symbolizing error stacks (D-069) |
| `pcre2` | pcre2-10.49 | RegExp: the 8-bit library with Unicode support, no JIT (D-132) |
| `libbacktrace-config`, `pcre2-config` | checked in | The configure output those two libraries need, for x86-64 Linux |

## 6. Tests

`make test` runs `go test ./pts/... ./cmd/pts/`. `pts/tests/README.md` describes the harness and its directives in full.

- **Go unit tests** in each package. The lowering tests compare printed IR, and the cgen tests check generated C. The fork's parser changes are tested in `internal/parser/pts_test.go`.
- **Runtime C tests.** Each `pts/runtime/tests/*_test.c` file is its own program. `go test ./pts/toolchain/` runs them under ASan and UBSan, and the threaded ones (pool, scheduler, sync) under ThreadSanitizer too, with collection off (bdwgc's signals don't work under TSan).
- **Programs** in `pts/tests/`:
  - `run/*.pts` (about 300) must build, run and print their `// expect:` block (`TestRun`). Directives also give the exit code, stderr texts, or `// threads: 1 2 4` to run under each `PTS_THREADS` value.
  - `errors/*.pts` (about 70) must fail to compile with the `// error:` texts they name (`TestErrors`).
  - `corpus/<theme>/*.pts`: 300 realistic plain-TypeScript programs in six themes, compared with Node (`TestCorpusMatchesNode`).
  - `stress/*.pts`: 11 parallel programs that race on the scheduler, the pool, the collector and shared data (`TestStress`). A nightly CI job runs them 100 times.
  - Generated corpora (`numbers_test.go`, `strings_test.go`): programs written at test time, with thousands of number literals and operations, String methods on random strings, and the case mapping and normalization of planes 0 and 1, compared with Node line by line.
  - `docs/examples/*.pts`: the docs' samples, run as run tests (named `docs/<name>`).
- **Differential tests against Node** (D-O04, D-009). Every test named `…MatchesNode` is one. A program that uses no ParallelScript feature must print exactly what Node prints and exit with the same code; stderr isn't compared. The features are `parallel`, `.par`, the integer types, and `pts:` imports other than the Node-like `pts:fs`, `pts:fs/promises`, `pts:path` and `pts:process`, which the harness maps to Node's `node:` modules. Node runs a copy of each module as an `.mts` file, with `--experimental-transform-types`. Without Node on PATH these tests skip, unless `PTS_REQUIRE_NODE` is set; `make test-diff` and CI set it.
- **Upstream suites.** `make test-upstream` runs the upstream packages the fork touches, plus `TestLocal`. `make test-upstream-cli` runs the CLI and language-server suites.

CI runs `.github/workflows/pts.yml` on every push, and `pts-nightly.yml` for the stress programs. The benchmarks in `pts/bench/` aren't gates (D-O15). `make bench` runs them against Node, Bun and Go twins (`main.go`), and `docs/BENCHMARKS.md` records the results.

## 7. Where things live

| Path | Purpose |
|---|---|
| `cmd/pts/` | The command line: `build`, `run`, `emit-c`, `help` |
| `pts/frontend/` | Loads a program through the forked tsc, with fixed options; orders modules |
| `pts/lib/prelude.d.ts` | Global declarations: `console`, numeric types, timers, `process`, `parallel`, `.par` |
| `pts/lib/*.pts`, `pts/lib/fs/`, `pts/lib/internal/` | The `pts:` modules, in ParallelScript |
| `pts/lib/lib.go` | Embeds the library and serves it at `ptslib:///` |
| `pts/check/` | PTS semantic checks: unsupported features, numeric types, the guardrail |
| `pts/numeric/` | Numeric types and their rules, shared with the fork's checker |
| `pts/diagnostic/` | The one diagnostic format and the PTS error codes |
| `pts/lower/` | AST to IR; defines the supported subset |
| `pts/ir/` | IR types, printer and walker |
| `pts/cgen/` | IR to C |
| `pts/driver/` | The pipeline, end to end |
| `pts/toolchain/` | Finds the runtime, builds and caches the C libraries, runs clang |
| `pts/runtime/include/pts.h` | The runtime's one header |
| `pts/runtime/src/` | The runtime's C sources |
| `pts/runtime/gen/` | Node scripts that generate the Unicode tables |
| `pts/runtime/tests/` | The runtime's C unit tests |
| `pts/third_party/` | Vendored C libraries (submodules) |
| `pts/tests/` | The test harness, `run/`, `errors/`, `corpus/` and `stress/` |
| `pts/bench/` | Benchmarks with Go twins, and `run.sh` |
| `internal/parser/pts.go` | Fork: the `parallel` keyword |
| `internal/checker/pts.go` | Fork: numeric types in the checker; exports for the lowering |
| `internal/tspath/`, `internal/module/`, `internal/core/` | Fork: `.pts` as a TypeScript extension (`docs/FORK.md` lists every edit) |
| `docs/` | The spec, decisions, fork ledger, benchmarks and this document |
| `docs/examples/` | The docs' samples, run as tests |
| `Makefile` | `build`, `test`, `test-diff`, `test-stress`, `test-upstream`, `test-upstream-cli`, `bench` |
| `pts-bin/pts` | The compiler, as `make build` makes it (not committed) |
| Everything else | Upstream typescript-go |

The runtime's sources by area:

| Area | Files in `pts/runtime/src/` |
|---|---|
| Objects, cells, interface sites | `object.c` |
| Strings | `string.c`, `string_methods.c`, `string_case.c`, `collation.c`, `normalize.c`, `uri.c` |
| Numbers and Math | `number.c`, `number_methods.c`, `integer.c`, `math.c`, `ieee754.c` |
| Arrays and collections | `array.c`, `map.c`, `typed_array.c` |
| `any` | `dynamic.c`, `dynamic_methods.c`, `convert.c` |
| Errors | `error.c`, `runtime.c` |
| Output | `console.c`, `inspect.c`, `table.c` |
| JSON, Date, RegExp | `json.c`, `date.c`, `regexp.c`, `regexp_object.c`, `regexp_string.c` |
| Generators and promises | `generator.c`, `promise.c` |
| Loops, tasks and the pool | `loop.c`, `scheduler.c`, `pool.c` |
| Library intrinsics | `process.c`, `fs.c`, `sync.c`, `http.c` |

## 8. Adding a feature

SPEC §9.2 gives the recipe for a new construct:

1. Handle it in `pts/lower`, with a lowering test on the printed IR.
2. Add any IR node it needs, with its printer case.
3. Add the `pts/cgen` case, and any runtime functions, with C unit tests.
4. Add a run test (and an error test for what stays unsupported).
5. Run `go test ./pts/... ./cmd/pts/`.

Log the design in `docs/DECISIONS.md`, and add a section to SPEC §9 that names the functions and files involved.

## 9. What is planned

- **M6.3** A global `fetch` over HTTP/1.1.
- **M6.4 and M6.5** Example apps in `examples/`, and a README that presents them.
- **M7** Editor support: `pts lsp` (the forked language server, with the prelude and PTS diagnostics), a VS Code extension, and a usability pass that adds `pts test`.
- **After M7**, if the run is extended: escape analysis, integer inference, unboxed arrays, inline caches for `any`, and an experiment with a precise collector.
