# ParallelScript architecture

This document explains how ParallelScript is built, for a developer joining the project. It describes what exists today (milestones M0 to M7 are done, which completes the plan) and says so where something is only planned.

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

`driver.EmitC` runs the front end, the checks, the lowering and the C emitter, and checks the C with the C compiler a build uses (`clang -fsyntax-only`). `driver.Build` instead writes the C to a temporary directory and compiles it; both go through one loop (`compileC`), so emit-c prints the C the build compiles. Each stage returns diagnostics, and any error stops the pipeline, except that a lowering that fails only in code some speculative instances made (D-190, D-191) is done again without those (`lower.Narrowing`: each report is blamed on the pairings of demands with values, or the call sites, that the code came from, where a later fallback doesn't make them and doesn't keep them either, D-199), and a lowering or a C compiler's rejection that still fails with the instances D-191 and D-190 add is done again without D-191's, then with D-148's only, as programs compiled before H09 (`lower.Fallbacks`: `AllInstances`, `ValueInstances`, `DeclaredInstances`; `driver.EmitCWith` takes one, for tests, and `driver.EmitCNarrowedWith` narrows at one). C that doesn't link is a compiler bug that no fallback hides: emit-c, which doesn't link, couldn't print the C such a build would fall back to. tsc's errors and ParallelScript's print in one format, `file:line:col - error CODE: message` (D-006). A bug the compiler runs into is an internal compiler error, `PTS9001` (D-179): the checks, the lowering and the C emitter each record the node or source line they work on and turn a panic into a `diagnostic.InternalError` there (`diagnostic.Locate`), which `driver.EmitC` returns (`diagnostic.Contain`); C that clang rejects becomes one at the line its `#line` gives. The CLI prints it as a bug to report and exits with 3.

Narrowing keys distinguish different instantiated demands at one source call and methods of different enclosing generic class instances, using checker-type bindings stable across relowerings of the same checked program (D-200). Reusing an existing descriptor entry still records its new user; a trial’s signature/body dependencies follow the shared function instance, while its reports still belong to the trial. Before a substitution’s proposed blame can remove code, a forward-worklist least-fixed-point check verifies that the code no longer exists without those pairings. Unproved cuts conservatively fall back. These refinements have regression coverage; H09b5b’s independent acceptance remains blocked, as recorded in its hardening evidence.

Ordinary generic function-value calls also carry their actual callee expression (`demand.source`). Before making a speculative value instance, D-201's source analysis (`value_flow.go`) can exclude declarations a proven immutable flow cannot reach: expressions/declarations, const aliases, conditional/logical unions, comma RHS and unassigned parameters of closed non-generic named functions. A single source-reference index accounts for callers, escapes and writes across modules, distinguishing shorthand names from default initializers. Parameter declaration proofs share their caller joins across reads. D-202 extends the proof through calls of proven synchronous non-generic functions (`returned_values.go`): declaration-shared return summaries join every explicit return, including catch/finally/unreachable arms, but skip nested callable/class bodies. Bare returns and fallthrough add no functions; an unknown return arm makes the whole summary unknown. Parameter origins still join all closed direct callers, never a call-specific environment cached under a shared declaration. Bounded, cached recursion charges uncached nodes, caller/callee visits and body traversal, returning unknown on cycles or budget exhaustion. D-205 registers exact wrapper creation sites only when bound-method lowering actually records a forwarder (`wrapper_sources.go`); bind uses the whole call rather than its `.bind` property. The lexical ancestor proof rejects generic classes/functions, including checker-contextually generic functions, and excessive depth. Registered sites are opaque source tokens, representing every closure and bound receiver from that site. `valueForward` synthetic demands carry the captured value expression, and instantiating adapters carry the adapted expression, so the same bounded source proof filters their inner value targets. Unregistered/unknown/generic-context wrapper origins remain conservative. Unknown sources, any/uncertain-container readback and async/generator/generic factories retain compatible pairs. Source AST identities represent every enclosing generic instance, not an inferred per-instance identity.

Direct method demands separately carry `receiverSource` (D-206; `receiver_sources.go`). `sourceProof` reuses bounded alias/union/caller/return traversal but keeps function and receiver expression/return/active caches disjoint. Receiver calls resolve their callees with function proofs before joining receiver-return summaries. A terminal is an actual directly named, unwritten, non-generic class construction outside generic lexical contexts; its class and every directly named ordinary ancestor must have no constructor expression-return. The shared reference index tracks class-binding writes too. Assertions, dynamic storage, aliases of constructors, uncertain/generic ancestors and exhausted/cyclic proofs are unknown. `receiverMayReach` is independent of `mayReceive`'s static-type cache: exact constructed classes retain their own and ancestor candidates (including override-blocking entries), while excluding unrelated classes and literal methods/function-as-method forwarders. No runtime receiver or capture is replaced. D-207 carries the actual captured receiver expression through class/interface method-wrapper factories into `methodForward`'s synthetic `receiverSource`; an explicit bind uses its `thisArg`, not the method-lookup object. The bounded lexical generic-context check still applies, and synthetic/destructured reads supply no source. Receiver summaries join every closed caller at a shared creation site, so every forwarding signature retains every receiver that site can capture; the forwarding closure continues to share the original wrapper's actual capture locals. The existing receiver proof, caches, budgets, inherited targets and override barriers are reused unchanged. The shared assignment index also descends assertion/non-null/satisfies targets, including nested patterns, so a parameter written through those wrappers cannot prove its initial receiver or function source (C258); cross-closure writes still mark narrowed reads as checked. D-208 extends both source-proof kinds through property/element projections (`container_sources.go`). Fresh object/array data literals are validated in full, including unselected members. A const container additionally requires a complete, capped cross-module reference index proving every use a supported data-key read or an eligible immutable alias initializer (D-209). Alias initializers follow only parentheses and simple const names backwards to one supported literal; forward traversal checks all transitive root/intermediate/leaf/sibling uses before publishing any positive component member. Each alias edge checks local/immutable/non-dynamic eligibility and unchanged checker data-member contracts; changed function-as-method, optionality, array/tuple/union and call/index-signature contracts stay unknown. Fanout charges work without accumulating sibling depth. Exports, uncertain aliases, mutation, dynamic keys/array methods and method-call/tag implicit `this` invalidate the entire component. Wrapped writes/deletes/updates and assignment patterns are checked with bounded ascent. Assertions/dynamic origins, accessors/method members, spreads/holes/computed/duplicate keys and `__proto__` remain unknown. Canonical nonnegative integer parser text avoids numeric-key guesses (source spellings can be normalized); missing/unsupported members are unknown too. The member/use tree cache is declaration-shared, while selected-expression function and receiver summaries remain disjoint. D-210 (`nested_container_sources.go`) follows static projection chains through direct nested data literals and validates every nested sibling before publishing a component. Intermediate literal-valued prefixes must continue to a static projection or an eligible immutable extraction (D-211); every extracted declaration retains its selected subtree, and every transitive alias/sibling use is checked. D-212 also starts this traversal at a fresh inline literal's only syntactic projection path into the first const binding. That first edge reuses finite literal-tree contract validation to normalize freshness; named downstream edges still compare raw declared contracts. Passing, storage and returns of intermediate containers invalidate the entire component. Nested positives initially require inferred/uncontextualized literals outside generic lexical contexts. Raw checker own/target contracts propagate down the finite syntax tree, normalizing freshness only at direct literal child edges; complete required data keys, plain array layouts and exact nonliteral terminal contracts prevent adapters or layout guesses. Substitution-dependent `typeAt`/`literalType` facts are never cached there. Literal members, use visits and wrapper ascents charge the existing work/depth bounds; storage per binding is capped and exhaustion stays conservative. Selected values compose existing aliases/callers/returns/wrappers without changing runtime evaluation or captures. D-213 (`destructured_container_sources.go`) resolves const object-binding elements through their owning variable's initializer, never through defaults. Complete forward validation accepts static identifier/string keys and nested object patterns only when every identifier leaf selects a literal-valued child container; all leaves join the same deferred-publication component and capped reference index. Every named edge preserves raw declared data contracts; a fresh first edge normalizes only finite literal syntax. `ContextFlagsSkipBindingPatterns` excludes the checker's inferred pattern pseudo-context while preserving declared/contextual conversion barriers. Numeric/computed/array/default/rest/terminal-value patterns and explicit owner annotations remain conservative. Runtime destructuring and wrapper/capture identity are unchanged. Parameter/returned/remaining-destructured or nonliteral-origin nested containers and broader caller-return flow remain H09b5c2b2b2b2b2b2b; no parent-task completion or H09b5b acceptance is implied.

Containing-code availability is separate from raw narrowing identity (C251, D-204; `containing_code.go`). Every pairing/site occurrence records the actual instance containing its demand and value, not the statement's report-blame heuristic. After lowering, a bounded positive graph joins all occurrences and completed instance users, requiring both demand/value code and any enclosing class. Its least fixed point starts at program-owned/unknown sources; shared late descriptor hits can raise availability, while unseeded cycles cannot create code. `Narrowing.Within` uses the resulting effective last fallback; exclusions still use unchanged intrinsic keys. Unknown/unregistered sources and exhausted 100,000-context or node/rule budgets retain intrinsic availability. Statement-only users are marked and conservatively treated as live for this proof. H09b5b's blocked independent acceptance remains separate from the new caps slice.

### 2.1 Front end: the forked tsc (`pts/frontend`, `pts/lib`)

`frontend.Load` builds a typescript-go `compiler.Program` and type-checks it.

- **Fixed options** (D-005). No `tsconfig.json` is read. Every program is `strict`, targets ESNext with ES modules, uses `moduleResolution: bundler` and `moduleDetection: force` (every file is a module), and has `lib: esnext` with no DOM and no `@types`.
- **The prelude.** The root files are the entry file and `ptslib:///prelude.d.ts`. `pts/lib` embeds its files in the compiler binary, and `lib.WrapFS` serves them under the `ptslib:///` URL root.
- **`pts:` imports.** The compiler option `paths` maps `pts:*` to `ptslib:///*.pts`, so `import … from "pts:path"` resolves to the embedded `pts/lib/path.pts` (D-116). This needs no fork edit.
- **Errors.** As in tsc, syntax errors hide semantic errors. `frontend.Hint` (`hints.go`) adds a hint to tsc's errors that ParallelScript's features lead to, such as a `.par` view's method called on an array or `parallel (f)(x)`; the CLI prints it on the line after the error, and the language server shows it as information (D-170).
- **Module order.** `Program.Modules` lists the source files in ES module evaluation order, entry last (D-022). `import type` requests nothing. If a module uses the global `process`, `fetch`, `AbortController` or another global of a library module (`lib.GlobalModules`), or a `.par` view, the front end runs that module (`pts:process`, `pts:http`, `pts:internal/abort`, `pts:parallel`) first, each once.
- An import of `pts:internal/…` outside the library is an error (D-118).

The fork's own edits (`docs/FORK.md`) are in four areas:

- **The `.pts` extension.** `.pts` is a TypeScript extension, and an extensionless import tries `x.pts` first (`internal/tspath`, `internal/core`, `internal/module`; D-001, D-004).
- **Integer types in the checker.** `internal/checker/pts.go` makes the prelude's aliases `int`, `i8` … `u64` and `f32` number types of their own, and types arithmetic on them by `pts/numeric`'s rules (D-048). It also exports a few helpers that the lowering uses to instantiate generics (D-049, D-078, D-095).
- **The `parallel` keyword.** `internal/parser/pts.go` parses `parallel e` as a call of the prelude's `parallel<T>(task: T): Promise<Awaited<T>>`, and `parallel for` as a for statement. Both are marked with a new node flag, `NodeFlagsPtsParallel` (D-120). There are no new AST node kinds, so tsc types `parallel e` with no checker change.
- **The language server.** Three hooks, nil in upstream's own use, that `pts/lsp` sets: `project.ProgramCommandLine` (`internal/project`), `ls.ExtraDiagnostics` (`internal/ls`) and `lsp.ContentSchemes` (`internal/lsp/pts_content.go`); and a line each so that `ptslib:///` URIs round-trip, `.pts` files are watched and globbed, and the keyword of `parallel e` gets no semantic token (D-168, D-169; §2.7).

### 2.2 PTS semantic checks (`pts/check`, `pts/numeric`, `pts/diagnostic`)

`check.Program` enforces the parts of the spec that tsc doesn't know. It takes the modules in order, dependencies first, and walks each one three times:

- **Features left out by design** (`unsupported.go`, SPEC §3.10, D-020): `var`, `eval`, `new Function`, `with`, `arguments`, `Proxy`, `Reflect`, BigInt, namespaces, symbols other than `Symbol.iterator` and `Symbol.asyncIterator`, and so on. Each is error PTS1002 with a reason.
- **The guardrail** (`parallel.go`, SPEC §5.6, D-127). A *parallel body* is the operand of `parallel`, a `parallel for` body, or a function passed for a `ParallelCallback` parameter (the `.par` methods and `join`). Inside one, assigning a `let` or parameter declared outside is PTS3001. A malformed `parallel for` is PTS3002.
- **Numeric types** (`check.go`, `functions.go`, `numeric.go`; SPEC §3.2, D-012). The pass works out a numeric type for every number-typed expression and variable. It checks that implicit conversions are lossless (PTS2001), that literals fit their type and that a literal written with a decimal point isn't made an integer (PTS2002, D-170), and that mixed operands have a common type (PTS2003). It records the results in `check.Info` for the lowering.

`pts/numeric` holds the numeric rules, such as `Unify` and `ImplicitlyConvertible`. Both this pass and the fork's checker use it. `pts/diagnostic` defines the diagnostic type and the PTS codes.

### 2.3 Lowering (`pts/lower`)

`lower.Program` turns the checked AST into an `*ir.Program`. It is the largest package, about 40 files named after what they handle (`classes.go`, `closures.go`, `generics.go`, `unions.go`, `dynamic.go`, `generators.go`, `promises.go`, `parallel.go`, …).

**Lowering defines the supported subset.** Any construct it doesn't handle is reported as PTS1001, "… is not supported yet" (D-006), so nothing unknown is silently miscompiled.

The order of work (`Program` in `lower.go`):

1. Walk the AST for facts that every function's lowering needs: the library module export that each global the program uses stands for (`globalModuleBindings`), and which variables a function other than their own assigns, whose narrowed reads are therefore checked (`collectOutsideAssignments`, D-140, D-145).
2. Declare every class, function and module-scope binding of every module, the runtime's classes (Error, Date, the typed arrays, RegExp) first. Code can then use any of them above its declaration and across import cycles.
3. Lower each module's top-level code into that module's `main` function.
4. Lower the function bodies, then the generic instances they need (including those that typed calls of erased copies look up, D-148), until none are left.
5. Run whole-program passes over the IR: `markCells` decides which captured variables live in heap cells (D-041), `checkRelaxedCasts` checks casts of objects that lack an optional property (D-066), and `prepareDynamicTypes` readies the types that writes and calls through `any` convert to, and those of iterator results' properties, which reads by name convert to (D-072, D-074, D-178).

The lowering asks tsc's checker for types everywhere and maps them to IR types (`irType`). Its main choices:

- **Generics are monomorphized.** Each generic function or class gets one instance per distinct list of IR type arguments (D-049, D-050). A generic function used as a value where no single instance fits becomes an erased copy over dynamic values (D-078). Its closure's descriptor, and the descriptors of classes whose generic methods are dispatched by name (D-101), list the instances that typed calls look up by signature at run time, so that what such a call gets is made for its type arguments (D-148); a generic arrow function's instances run with its closure, reading the variables it captures, and calls whose result is a promise or generator look theirs up by their type arguments too (D-190). Calls through interfaces and of object literals' methods look instances up by the method's name, in the object's class or in its literal's method closure, whose instances take the literal as `this` (D-191).
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

- **Functions** are `static` C functions named `pts_fn_<name>`. The entry module's top-level code is `pts_main`, and another module's is `pts_module_<name>` (D-020, D-022). `main` calls `pts_runtime_init`, then each module in order, then `pts_runtime_exit`. Programs that use promises also drain the microtask queue, and programs with timers, tasks, asynchronous I/O or a server also run the event loop (an intrinsic that returns a promise is marked `ir.Func.Settles`, and `pts_http_listen` `ir.Func.Listens`).
- **Names.** Locals keep their source names where C allows it (`names.go`).
- **`#line` directives.** One precedes each function, and each statement whose line the C compiler doesn't already presume, so none is redundant (D-007). gdb, lldb and stack traces show `.pts` lines. The library modules' lines name the module, as in `#line 415 "pts:parallel"`.
- **Evaluation order.** C leaves the order of evaluating operands unspecified, and JS doesn't. `order.go` finds operands that could tell the difference, and those go through `pts_tmpN` temporaries in a statement expression, `({ … })`, a GNU extension that clang supports (D-019).
- **Classes** become structs `pts_class_<Name>` whose first member points to the descriptor, plus a static descriptor `pts_type_<Name>` (D-024, D-026).
- **Ready flags.** A module-scope binding that a function may read before its declaration has run is a C global with a `<name>_ready` flag, which such reads check (`pts_check_ready`, D-021). A class has a hidden binding of this kind, which `new` checks (D-141). A local that a closure captures before its declaration has run keeps its flag in its cell (`PTS_CELL_READY`, D-140). Each check throws Node's `ReferenceError: Cannot access 'x' before initialization`.
- **The stack guard.** A function that may recurse starts with `pts_stack_check()` (D-069).
- **`try`** is a `_setjmp` on a handler pushed onto the thread's chain of handlers (D-068).
- **Generators and async functions** each become a frame struct, a starter and a step function, which starts with a `switch` on the resume point (D-109). This state machine is built here, in the emitter, not by a pass over the IR as brief §2.3 has it; that move is left to M8 (D-153).

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
| `pts test [paths…]` | Builds and runs the test files under the paths, and prints the totals and failures of all (below) |
| `pts lsp [--stdio]` | Runs the language server over stdin and stdout (below) |
| `pts help [command]` | Prints the usage, or a command's (also `pts <command> --help`, D-170) |
| `pts version` | Prints the version, the commit it was built from and typescript-go's version |

Exit codes (D-006): 0 on success, 1 for compile or toolchain errors, and 2 for a malformed command line. `pts run` exits with the program's own code, or with 128 plus the signal number if a signal killed it. `pts lsp` runs the language server (`pts/lsp`, D-168): typescript-go's own, with three hooks set, one making every project's program the compiler's (the fixed options and the prelude as a root), one adding ParallelScript's checks to a file's diagnostics (or, where tsc has errors, their hints), and one naming the schemes of the library files, which exist only in its memory (`ptslib:///`), whose text it serves through LSP 3.18's `workspace/textDocumentContent` (D-169). It gives the keyword of `parallel e` no semantic token, so the VS Code extension (`pts/editors/vscode`, D-169) shows it with its grammar's keyword color. The errors that only the lowering finds (most PTS1001s) aren't shown in the editor yet. `pts test` (`cmd/pts/test.go`, D-171) builds each test file, runs it with `PTS_TEST_RESULTS` naming a file where `pts:test` (a library module, as `pts:assert` is) writes its totals and failures, and prints the totals of all files.

## 3. The `pts:` library modules (`pts/lib`)

The standard modules are written in ParallelScript (D-116). Each is a `.pts` file embedded in the compiler. It is compiled with the program that imports it, like the program's own modules.

| Module | Source in `pts/lib/` | Contents |
|---|---|---|
| `pts:process` | `process.pts` | Node's `process`; its default export is also the global `process` |
| `pts:path` | `path.pts` | Node's POSIX `path`, ported line by line |
| `pts:fs`, `pts:fs/promises` | `fs.pts`, `fs/promises.pts`, `internal/fs.pts` | Node's file API, synchronous and promise-based (D-117, D-118) |
| `pts:parallel` | `parallel.pts` | `.par` views, `range` and `join` (D-123, D-124, D-126) |
| `pts:sync` | `sync.pts` | `AtomicInt`, `Mutex`, `Once` and `Channel` (D-128, D-129) |
| `pts:http` | `http.pts` | Bun-style `serve` and `Server`, the Fetch standard's `Request`, `Response` and `Headers`, and `fetch`; all but `serve` and `Server` are also globals (D-136, D-160) |
| `pts:test` | `test.pts` | Node's `node:test`: tests, suites, hooks, the spec reporter's output, and the results file `pts test` reads (D-171) |
| `pts:assert` | `assert.pts` | Node's `node:assert/strict`, with Node's messages and `AssertionError` (D-171) |
| `pts:internal/abort` | `internal/abort.pts` | `AbortController`, `AbortSignal`, `DOMException`, `Event` and `EventTarget`, as the DOM standard has them; reached only as globals (D-162) |

`internal/` modules can't be imported from programs (D-118). A global that is a library module's binding of the same name is listed in `lib.GlobalModules` (`pts/lib/lib.go`): the front end adds the module when a program uses the global (§2.1), and the lowering binds the global to the module's export.

A module reaches the runtime through **intrinsics**: functions it declares without a body, named `pts_…`. For example, in `parallel.pts`:

```ts
declare function pts_parallel_for(leaf: (from: number, to: number) => void, count: number): void;
```

The lowering turns such a declaration into an external function, so a call is a direct call of the runtime's C function of that name, with the arguments converted to the declared types (`intrinsicCall` in `pts/lower/library.go`). Only bodiless declarations inside `pts:` modules count, and they aren't exported, so user code can't call them. Intrinsics stay thin: the modules build their classes and objects in ParallelScript, and the runtime does only what needs C. For example, `pts:http` parses URLs, builds `Headers` and follows redirects in ParallelScript, and its six intrinsics only listen, respond, report the bound port, stop, fetch and abort a fetch. An intrinsic can take callbacks: `pts_http_listen` takes the function that each request is handed to, and `pts_http_fetch` takes the one that a response or a failure is handed to.

`prelude.d.ts` declares the globals that `lib.esnext.d.ts` lacks: `console`, the numeric type aliases, timers, `queueMicrotask`, `performance`, `process` (as `import("pts:process").Process`), `fetch`, `Request`, `Response` and `Headers` (from `pts:http`), the abort classes (from `pts:internal/abort`), `parallel<T>`, and `par` on arrays and typed arrays (as `import("pts:parallel").ParallelArray<T>` and friends).

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
- `src/` holds the implementation: 39 `.c` files, one per area (see the table in §7). The `*_internal.h` headers are shared by a few files and aren't part of the API.
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

`Object.freeze` swaps an object's descriptor for its *frozen twin* (D-157): a copy, method table included, whose `frozen` flag writes test and whose `parent` is the original, so `instanceof`, field lookups and virtual calls work unchanged. A store site's inline cache never holds a twin, so a store that hits its cache needs no test; the runtime's mutators (array methods, hash-map objects, `any` writes, `lastIndex`) test the flag, and generated stores to class fields test it only in programs that call `Object.freeze`. Code that compares descriptors sees through a twin with `pts_unfrozen`.

### 4.3 Strings

A `pts_string` is the header, flags (pure ASCII; in an append buffer; known to hold no lone surrogate, so its bytes are valid UTF-8 and regexes match it without PCRE2 checking it, D-166), the byte length, the UTF-16 length (JS's `.length`, so reading it is O(1)) and a pointer to the bytes (D-014, D-061). Strings are immutable, so threads share them freely. A plain string's bytes follow its header in one block that the collector never scans.

- **WTF-8.** The bytes are UTF-8, except that a lone surrogate (which a JS string may hold) is stored as its 3-byte encoding. Concatenation joins surrogate halves at the seam, so each string value has one representation, and byte equality is string equality.
- **UTF-16 indices.** Indices count UTF-16 code units, as in JS. An ASCII string's units are its bytes, so ASCII strings take O(1) paths (`string_methods.c`).
- **Building strings is linear.** A long enough concatenation puts its result in an append buffer, and the string points into it. When a string's bytes end where its buffer's used part ends, concatenating onto it claims the next bytes with a compare-and-swap instead of copying (`string.c`, D-061).

### 4.4 Arrays, maps, sets and hash-map objects

- **Arrays** (`array.c`, D-038). A `pts_array` is a descriptor, a buffer and a length, and the buffer holds its own capacity. Elements are stored unboxed by kind: a `number[]` holds doubles. Each element is copied with one load or store, and bounds checks use the buffer actually being accessed. So racing pushes may lose elements, but never write out of bounds. `flat` and `flatMap` walk the slots by their kinds and spread whatever is an array or a tuple by its descriptor, skipping holes (not a match's undefined groups), so elements of union, tuple and `any` types flatten as in JS. Each other element is stored as the result's element type holds it: copied as it is from slots of that type and kind, else converted and, unless its slot's declared type implies the result's, checked against the declared element type that the result's descriptor carries (the emitter turns declared types on for programs that flatten into arrays of objects or unions; a per-thread memo of the last check and descriptor that fitted skips repeated structural checks; D-180).
- **Map and Set** (`map.c`, D-046). An ordered hash table: entries are appended in insertion order, deleted ones are marked in place, and an open-addressing index finds them. Growing or compacting publishes a new table instead of resizing in place. Iterators follow the chain of tables, which gives JS's live iteration.
- **Hash-map objects** (D-059). Object types with index signatures, such as `Record<string, T>`, use the same table with kind `PTS_KIND_RECORD`. `JSON.parse` makes these for objects (D-080).
- **Typed arrays** (`typed_array.c`, D-125). A view (buffer, data pointer, length and offset) of an `ArrayBuffer`. Elements are read and written by relaxed atomic accesses of their width, and bulk copies by `pts_copy_shared` (`shared_memory.c`), so no number tears (D-143). `pts:fs` reads a file without an encoding into a new `Uint8Array`, which stands in for Node's `Buffer` (D-165).

### 4.5 Classes, object types and closures

- **Classes** (D-026, D-028). Each class is a struct, and a subclass's struct embeds its parent's first. Method calls are direct, unless the method is abstract or overridden below the receiver's static class. Then the call goes through a vtable (`pts_vtable_<C>`) inside the descriptor. A field of an object type is NULL until its initializer or the constructor sets it. JS reads undefined there, so tests of truth, `== null`, `??` and boxing treat NULL as undefined, and other uses go through `pts_check_field_set`, which throws Node's `TypeError` instead of dereferencing it (D-141).
- **Object types** (D-033, D-035, D-036). An object literal's type is a *shape*: one struct shared by every object type with the same properties of the same types. A value typed by an interface may be any object that has the interface's properties. So an access through an interface finds the field by name, through a one-word inline cache at the access site (`pts_site`: the last descriptor met there and the field's offset). Method calls through interfaces use the descriptor's method table the same way.
- **Closures** (D-041). A `pts_closure` is a descriptor, a code pointer and the captured values. A captured variable that may change after it is captured lives in a heap cell, which the closures share. A cell of a local that is captured before its declaration runs holds a ready flag after the value (D-140, §2.5).
- **Errors** (D-068). Error and its subclasses are classes the runtime defines (`pts_error`, `error.c`). So `extends Error` and `instanceof RangeError` work, also on the errors the runtime throws.

### 4.6 Dynamic values and `any`

`any` and `unknown` are `pts_value`s, and operations on them dispatch at run time through the descriptors (D-070).

- **Reads, writes and operators** (`dynamic.c`): properties by name, and operators with JS's conversions.
- **Conversions to static types** (`convert.c`, D-071). An object that already has the target type is kept as it is. Dynamic data, such as an `any[]` or parsed JSON, is checked and copied into the target's representation; so are maps and sets of other key or value types, whose keys are kept (D-192). A mismatch throws, for example, `TypeError: Expected number, got string`. An array or a tuple shorter than a tuple type by its optional elements converts to it, leaving them out (D-181). A union of object types takes a value as a member that declares every property of it that another member declares, since tsc may narrow the value to any such member (tags are strings in the IR), before a member that leaves one out (D-192), and a class's instance is copied into a member's shape only if no member takes it as it is; the members each property is declared by are computed once per union check, in a table threads share. The lowering converts a dynamic value into a union-typed place with a check unless an array's or map's own method read it from typed elements.
- **Typed reads verify** (`object.c`, D-072, D-192). A site that reads a property through an interface caches a field or getter only if its declared type implies the property's type, or if it reads an object as an object type (whose own properties are verified at their sites in turn). Otherwise it reads the value converted as it is (unboxed, or a closure adapted) into a fresh cell, or throws `TypeError: Cannot read property p of C as T: it is declared U`. So an object that a union took as one member (an erased copy's `{ tag: "some", value: any[] }` as `{ tag: "none" }`) is never read as another member's representation.
- **Calls through `any`** (D-074). In a program that makes them, each function value and method gets a *dynamic entry*. The entry takes boxed arguments, checks them against the parameter types and boxes the result (`pts/cgen/dynamic_calls.go`).
- **Builtin methods through `any`** (`dynamic_methods.c`, D-076): the methods of strings, numbers, arrays and functions, found by name, and those of generators and the runtime's iterators (`pts_iterator_method_call`, D-178).
- **The runtime calls program code through dynamic entries too** (D-142). A method's C signature can be anything TypeScript allows, so the runtime never calls one blind: `toString` and `valueOf` (in OrdinaryToPrimitive's order), `toJSON`, timers' callbacks and promise handlers go through dynamic entries, and getters and setters are called by their declared kind.
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

- **Generators** (`generator.c`, `pts/cgen/generators.go`, D-109). The C emitter keeps the body structured. Every statement-level local lives in a frame struct on the GC heap, which starts with a `pts_iterator`. A `yield` stores its value and its resume point, then returns. The step function starts with a `switch` that jumps to the resume point. There is no native stack per generator. A `yield*` hands the result objects its inner iterator gave through to its caller (`pts_generator_delegate`), and a typed `it.next()` gives the object an iterator's `next()` returned: typed code reads `done` and `value` as fields of the runtime's result shapes and by name on any other object (D-178).
- **Async functions** (D-112) are generator functions whose yields are awaits. `pts_async_start` makes the function's promise and runs the body until its first await. Later steps run as microtasks.
- **Promises** (`promise.c`) follow ECMAScript's algorithms in V8's order of jobs, so programs print in Node's order. Each thread has a microtask queue, which `pts_run_microtasks` drains. An unhandled rejection ends the program with code 1.
- **Async generators and `for await`** (D-115) use the same frames, plus the spec's queue of requests. An async generator belongs to the loop that made it: requests from other loops reach its queue by message (D-143).
- **Generators shared by threads.** One thread at a time runs a generator's body; a resume while it runs elsewhere throws `TypeError` (D-143, §4.13).

### 4.10 Event loops and tasks

The loops and their inboxes live in `scheduler.c`, timers and immediates in `loop.c`, and the messages and what they do in `promise.c` (D-114, D-121).

- **One loop per thread.** Each event loop is a libuv `uv_loop_t` on its own thread, and all loops share one heap. The main thread's loop runs the modules. The other loops start the first time `parallel` runs, or when a `pts:http` server listens with `reusePort`, so a program that uses neither has one loop. There are `PTS_THREADS` loops (at most 256), or one per core.
- **Timers** are Node's: a heap of timers behind one `uv_timer_t`, `setImmediate` on a check handle, and a microtask drain after every callback (`loop.c`).
- **Loop-owned handles** (D-139). A timer or immediate belongs to the loop that made it, and only that loop touches its heap, queue and counts. Any thread (another loop, or a pool thread in a `.par` callback) may still clear, ref, unref or refresh a timer. Clearing exchanges the timer's atomic `active` flag, so the callback doesn't start after `clearTimeout` returns. The rest (taking the timer out of the heap, applying `ref`, `refresh`) runs on the owning loop: directly when the caller is that loop, else by a CALL message. An immediate's `active` flag is atomic too, so any thread may clear it, and its loop skips it. `pts:http`'s connections and `fetch`'s requests belong to their loop in the same way (§4.16).
- **Inboxes.** Each loop has an inbox of messages and a queue of tasks not yet started, guarded by a mutex, and a `uv_async_t` that other threads wake it with. The message kinds are SETTLE, RESOLVE, THEN and REACTION (promise work), TASK (a `parallel` task to start) and CALL. A CALL message (`pts_post_call(loop, f, data)`, D-137) runs a runtime function on the given loop, in order with that loop's other messages, then runs the microtasks it queued. It is how one thread acts on state that another loop owns: a timer, an HTTP listener, a fetch request, an async generator's queue (D-143).
- **Output.** Each loop flushes stdout from a `uv_prepare` handle before it waits for I/O, so a server's log lines appear as it runs (D-136).
- **Promises never change on two threads.** A promise belongs to the loop that made it and settles only there; a reaction runs on the loop that added it. Settling another loop's promise is a message to that loop. So single-loop code takes no locks, and an async function resumes on the loop where it started.
- **Tasks.** `parallel e` calls `pts_parallel(closure)`, which makes a promise on the calling loop and posts a task to the least-loaded other loop. That loop runs the closure, and the promise settles back on the caller's loop. A loop out of work takes a task that another loop has queued but not started, then sleeps.
- **I/O.** `pts:fs/promises` runs its system calls on libuv's thread pool and settles its promises on the loop (D-118). The HTTP server and the `fetch` client run on the loops' libuv TCP handles (§4.16).
- **Ending.** When every loop sleeps, none can wake another, and the program ends, as Node does when nothing is left to do. A loop sleeps until a message or its first timer (only unreferenced ones are left then); at the timer's time it counts itself awake again only if another loop still is, and runs its due timers itself, since libuv runs none while nothing referenced is left (D-186). `process.exit`, an uncaught error and a panic take one process-wide exit lock first (`pts_begin_exit`).

### 4.11 The fork-join pool

The pool lives in `pool.c` (D-122). `docs/agent/NOTES-rayon.md` explains the design it copies.

- **Threads.** The pool has `PTS_THREADS` − 1 worker threads, separate from the loops, started at the first parallel operation. The thread that starts an operation works too, so operations run on `PTS_THREADS` threads.
- **Deques.** Each worker has a Chase–Lev deque of job pointers (Lê et al.'s version for weak memory models). The owner pushes and pops at the bottom, and thieves steal the oldest job from the top. A loop thread gets a deque the first time it starts an operation.
- **`pts_join(a, b)`** is Rayon's join. It pushes b, runs a, then pops b and runs it, unless a thief took it. In that case it runs other work until b is done. b's job lives on the caller's stack, so join allocates nothing. If either closure throws, join rethrows once both are done, a's error first.
- **Splitting.** `pts_par_run` splits a range of indices with Rayon's adaptive splitter. `pts:parallel` builds `.par` on the intrinsics `pts_parallel_for` and `pts_parallel_reduce`, which run on it, and `join` on `pts_parallel_join`, which is `pts_join`. The lowering calls `pts_parallel_for_each` for `parallel for`.
- **Promise work from workers.** Each job records the loop of its operation. Promise work that a callback does on a worker becomes messages to that loop, which the loop applies before the operation returns (D-123). A `fetch` from such a callback starts its request on that loop by a CALL message (D-162), and clearing a timer or stopping a server works from any thread (D-139, D-163). Starting a timer, a `pts:fs/promises` call or a server from a callback on a worker throws `Error` for now.
- With `PTS_THREADS=1` there are no workers, and everything runs on the caller (D-O06).

### 4.12 Garbage collection

Memory is managed by bdwgc, the Boehm–Demers–Weiser collector (SPEC §4, D-024).

- **Build.** bdwgc is built with threads, parallel marking, thread-local allocation and gcj-style allocation (`pts/toolchain/libraries.go`). `pts.h` defines `GC_THREADS` before it includes `gc.h`, so every thread the runtime creates is registered with the collector.
- **What gets scanned.** Instances of classes and object types are scanned only in the words their descriptor names (every word, for instances of more than 62 words). Other blocks that hold pointers, such as the buffers of object arrays, map tables, cells and generator frames, are scanned conservatively, word by word, as are thread stacks and registers. Pointer-free blocks, such as plain strings, the buffers of number arrays and objects with only numbers, are never scanned.
- **Roots outside the heap.** Runtime structures that the heap doesn't reach, such as loop slots, deques, timer state and microtask queues, are allocated uncollectable, so the collector scans them as roots. So are an HTTP server (which holds the program's callbacks), each connection's cell holding the `Request` the program is handling, and a `fetch` request's callback until it is called. libuv's handles and the bytes the server and client read and write are `malloc`'d and hold no collected pointers.
- **Tuning** (`pts_runtime_init` in `runtime.c`). A collection runs once the program has allocated as much as the heap holds (free-space divisor 1, D-060). The heap starts at 4 MiB (D-112), and marking runs on 2 threads (D-131). The environment variables `GC_FREE_SPACE_DIVISOR`, `GC_INITIAL_HEAP_SIZE` and `GC_MARKERS` override these.
- **Planned.** SPEC §4 plans a precise collector, and later a generational one. The master plan puts an experiment with a precise collector after M7.

### 4.13 Race safety: the shared-memory contract

All threads share one heap, so a program can race on it. The guarantee (SPEC §4.1, D-L13, the owner's ruling 10 in `docs/agent/OWNER_ANSWERS.md`) is that a racy program may read stale values, but never a torn value, a crash or an out-of-bounds access. This is what that asks of the runtime's C and of the C the compiler emits (D-143). Any heap object can reach several threads: through a `parallel` closure's captures, a task's result, a `.par` callback, a module-scope binding or a `pts:sync` channel.

1. **Shared slots are one aligned word or less.** Every heap slot that another thread can read holds at most 8 bytes, aligned to its size: a `pts_value`, a pointer, a double, an integer or a bool. Nothing shared is a multi-word value. Pairs that must agree are kept safe by protocol instead: an array publishes a new buffer before the length that needs it, a map publishes a new table instead of resizing in place, and readers check every index against the buffer or table they loaded.
2. **Atomic where it matters.** The runtime uses `__atomic` builtins (relaxed unless ordering is needed) for array lengths and buffers, array elements that may be holes, map tables, counts and entries, inline caches, string buffers' claims (compare-and-swap), typed arrays' elements and bulk copies, runtime iterators' positions, generators' claims, promises' `handled` flags, timers' and immediates' flags, the pool's deques, the loops' counters and `pts:sync`'s cells.
3. **Plain one-word accesses elsewhere.** Generated code reads and writes object fields, captured cells, module-scope bindings and the elements of number and boolean arrays with plain C loads and stores. ISO C calls these data races (undefined behaviour). The contract relies instead on what clang emits for x86-64: an aligned load or store of up to 8 bytes is one instruction, which x86-64 performs atomically (Intel SDM vol. 3A, "Guaranteed Atomic Operations"), and LLVM doesn't split such an access or reload a heap word that the source reads once (it rematerializes only loads it knows are invariant). That is the machine-level guarantee Java and Go give racing programs. Relaxed atomics there would be ISO C, but would stop clang from merging, hoisting and vectorizing field and element reads in every program (D-143).
4. **Read once, check, then use.** Code that checks a shared slot (an index against a length, a tag, a NULL, a state) reads it once into a local and uses only that local, with an atomic load where the slot is in rule 2's list. Reading a field again between the check and the use is a bug, even when each read is atomic: the array iterators over tuples, the map iterators and `console.log` of errors (which counted its entries, then read the fields again to fill them) did this until D-143.
5. **No `memcpy` or `memmove` of numbers that other threads can read.** Those functions may copy in any order and size (`rep movsb`, unaligned vector moves), so they can tear a number that another thread is writing. Typed arrays copy with `pts_copy_shared` (`shared_memory.c`), a `memmove` made of relaxed atomic accesses as wide as both addresses allow (the element's size, up to 8 bytes). A loop's per-iteration copy of a captured cell (`pts_cell_copy`) and `pts:fs`'s writes of a `Uint8Array` use it too (D-165). Arrays copy element by element (D-130). `memcpy` is fine for memory that no other thread can reach yet (a new buffer before it is published) and for memory that never changes (strings' bytes).
6. **Publication.** A new object is filled with plain stores and published by storing its pointer in a shared slot. x86-64 doesn't reorder a store with earlier stores, so a thread that loads the pointer sees the object filled (D-130). A port to another architecture needs release stores at the points of publication.
7. **State that one thread owns.** What these rules don't make safe belongs to one thread, and other threads reach it by message (`pts_post`, `pts_post_call`) or claim it:
   - a promise belongs to the loop that made it: only that loop settles it or changes its reactions (D-121). Another loop that adds a handler sets the promise's `handled` flag at once, sequentially consistent with the home loop's check for unhandled rejections, so a rejection that comes before that loop takes the message isn't reported (D-143);
   - an async generator belongs to the loop that made it, which alone runs its queue of requests and its body. A request from another loop is a message, and its promise is made on the loop that asked (D-143);
   - timers, immediates, HTTP listeners and connections, and `fetch` clients belong to their loop, which other threads reach by CALL messages (D-137, D-139, D-162). The only `fetch` state that threads share is the table of request ids, under a mutex;
   - a sync generator's frame belongs to the thread that claimed it (`executing`, exchanged atomically once the program has threads, `pts_threaded`). A resume while it runs, from its own body or from another thread, throws `TypeError: Generator is already running`, so its body never runs on two threads (D-143);
   - microtask queues, timer heaps and inboxes' internals are per loop, behind the inbox's mutex where other threads post. A timer, and a server's first listener, record the loop whose thread made them, also when that thread helps another loop's operation (D-186);
   - pts:test's registry belongs to the main thread: `test`, `describe` and the hooks throw in a `parallel` task's closure or a parallel operation (`pts_parallel_on_main_thread`, D-186). Code after an `await` there runs as its loop's code, so only the main thread's can register.
8. **Results handed back per thread.** Where another thread's call can overwrite a result in a shared object, the runtime returns it per thread: a resume's value, and the result object it handed back, go through the thread's slot in `generator.c` (once the program has threads), and a map iterator's step works on a private copy of its place.
9. **Library state under its object's lock.** Library code has no compare-and-swap on fields, so state that the library itself changes in an object threads share is under a lock: an `EventTarget`'s listener list and an `AbortSignal`'s abort state (`aborted`, its reason, its dependents) change only under the target's own futex lock (pts:sync's intrinsics, D-128), which is held for plain reads and writes and never while a listener runs (the one checked write, `aborted`'s, throws only for a frozen signal, which is caught, put back and unlocked); a request's lazily made signal is installed under one lock that the server's disconnect takes too (D-185). The library's check-then-act sites add their listener first and check `aborted` after, so the lock orders them against an abort.

What racing programs can see: lost updates (pushes, map entries, unsynchronized counters), stale values, a key twice in a map, an entry whose key and value come from two racing appends, a `RangeError` from an operation that another thread made impossible, and a shared generator's `TypeError` when two threads resume it at once (SPEC §4.1).

How it is tested: the `pts/tests/stress/race_*` programs (run 100 times a night) race threads on every kind of shared object; ThreadSanitizer runs the pool's, the loops', `pts:sync`'s and the typed arrays' C tests (`typed_array_race_test.c` races element accesses and bulk copies, so TSan reports any access left plain); ASan and UBSan run every runtime unit test. TSan can't run whole programs that collect on several threads (bdwgc's signals), so rule 3's plain accesses rest on the argument above and the stress tests.

### 4.14 RegExp and the Unicode tables

- **RegExp** (D-132 to D-134). `regexp.c` translates a JS pattern into PCRE2's syntax, keeping JS's meaning where the two differ. It compiles the pattern with PCRE2's 8-bit library in UTF mode, without the JIT, and reports matches in UTF-16 indices. Each pattern is compiled twice: once to match strings that may hold lone surrogates (invalid UTF-8, which PCRE2 then checks on every match), and once to match strings known to be well-formed unchecked, so loops over a long string's matches stay linear (D-166). `regexp_object.c` implements the `RegExp` class and match arrays. `regexp_string.c` implements the String methods that take a RegExp. A regex literal's compiled code is cached in a static.
- **Generated tables.** Four scripts in `gen/` read Node's ICU and write tables into `src/`:
  - `case_tables.mjs`: case mapping (D-016);
  - `collation_tables.mjs`: `localeCompare` (D-082);
  - `width_tables.mjs`: terminal widths for `console.log` (D-088);
  - `normalization_tables.mjs`: `normalize` (D-135).

  `normalize.c` implements the four normalization forms over its table. Regenerate the tables when Node's Unicode version changes. The generated string corpus tests fail until then.

### 4.15 The other built-ins

The remaining built-ins follow Node's and V8's algorithms, so that results match to the last digit: number to string with Ryu's digits and ECMAScript's notation rules (`number.c`, D-011), `toFixed`, `parseInt` and friends (`number_methods.c`, D-083), Math as V8's fdlibm converted to C (`ieee754.c`, `math.c`, D-084), JSON with V8's messages (`json.c`, D-080), Date (`date.c`, D-087), the URI functions (`uri.c`, D-092) and `localeCompare` (`collation.c`, D-082). `process.c`, `fs.c` and `sync.c` hold the intrinsics of `pts:process`, `pts:fs` and `pts:sync` (D-116 to D-118, D-128); `fs.c` reads files as text or, without an encoding, as bytes (D-165). `http.c` and `fetch.c` hold `pts:http`'s (§4.16). `shared_memory.c` has the copies of memory that other threads may write (§4.13). `runtime.c` has startup (`pts_runtime_init`), the stack guard, `pts_panic` and the exit lock.

### 4.16 HTTP: the server and `fetch`

`pts:http` is split like the other library modules. `pts/lib/http.pts` has the API in ParallelScript: `serve`, `Server`, the Fetch standard's `Request`, `Response` and `Headers`, and `fetch`, which parses URLs, sets headers, follows redirects and makes Node's errors. `pts/lib/internal/abort.pts` has the abort classes. The connections, the parsing and the I/O are C on libuv's TCP: `http.c` is the server, `fetch.c` is the client, and `http_internal.h` holds the header parsing and chunked decoding that they share. Bodies are whole strings, decoded as UTF-8; there is no streaming and no TLS (`https://` rejects).

- **The server** (D-136). `pts_http_listen` listens on the loop that calls `serve`. A connection reads requests one at a time, and pipelined ones wait in its buffer, so responses keep their order. It parses the head (up to 64 KB, else 431) and the body (Content-Length or chunked, up to 64 MB, else 413), and answers `Expect: 100-continue` and malformed requests (400) itself. Then it calls the library's request callback with a connection id, the method, the target, the headers and the body. The library makes the `Request`, runs the program's `fetch` handler, and answers with `pts_http_respond(id, …)`. The framing headers (`content-length`, `connection`, `date`) are the server's.
- **Connection ids.** An id is `(generation · 2⁸ + loop) · 2²⁰ + slot`, exact in a double. An answer for a connection that has closed, one of another loop's connections, or a forged id finds nothing and is dropped, so the program can't misuse ids to reach memory.
- **Every loop** (D-137). With `reusePort: true`, `serve` starts the loops, and the calling thread binds a SO_REUSEPORT socket per loop, so bind errors still throw from `serve`. Each loop then takes over its socket by a CALL message, and the kernel spreads new connections over them. A connection stays on the loop that accepted it, and so do the promises, timers and tasks its handlers make. Handlers on different loops run in parallel and share module state as any parallel code does: memory-safe, but a counter needs `AtomicInt`. The guardrail doesn't extend to them (D-137, listed in `docs/agent/REVIEW_LATER.md`).
- **Lifecycle** (D-163). A connection goes on reading while the program has its request, so a client that leaves is seen at once. The connection then closes, and the disconnect callback gets the `Request` back (kept in an uncollectable cell per connection) and aborts its `signal`. Once more than 1 MB of responses waits unwritten, a connection takes no more requests until half of that drains. `stop(closeActiveConnections)` sets an atomic flag once, then stops each loop's listener on that loop (by CALL messages), so it works from any loop or pool thread.
- **The client** (D-160, D-162). `pts_http_fetch` makes one connection per request on the calling loop. It resolves the host with `uv_getaddrinfo`, tries the addresses in turn, writes the request with `connection: close`, and reads the whole response (framed by Content-Length, chunks or the connection's end; 1xx responses skipped; up to 64 MB). Then it calls the library's callback with the status, headers and body, or with the cause that `TypeError: fetch failed` reports. A `fetch` on a pool thread starts its request on its operation's loop.
- **Aborting.** Each request has an id: a slot and generation in one table under a mutex, the only client state that threads share. `pts_http_fetch_abort(id)`, called from any thread when the request's `AbortSignal` aborts, ends the request on its loop (by a CALL message from elsewhere) and closes its connection. Only that loop frees the client: while libuv's resolver or a closing handle still holds it, that callback frees it.

`examples/api-server` is a JSON API served on every loop, with shared state, `.par` in a handler and `request.signal` (D-166).

## 5. Vendored third-party code (`pts/third_party`)

These are git submodules, pinned in `.gitmodules` and listed in `docs/FORK.md`. A fresh clone needs `git submodule update --init pts/third_party`. `pts/toolchain` builds them all with clang, not with their own build systems (D-003).

| Directory | Version | Used for |
|---|---|---|
| `bdwgc` | v8.2.12 | The garbage collector |
| `libuv` | v1.53.0 | Event loops, timers, file system calls, the thread pool behind `pts:fs/promises`, and TCP and DNS for `pts:http` and `fetch` |
| `ryu` | v2.0 | Shortest round-trip digits for number-to-string conversion (D-011) |
| `libbacktrace` | commit 0b9b49c (it has no release tags) | Capturing and symbolizing error stacks (D-069) |
| `pcre2` | pcre2-10.49 | RegExp: the 8-bit library with Unicode support, no JIT (D-132) |
| `libbacktrace-config`, `pcre2-config` | checked in | The configure output those two libraries need, for x86-64 Linux |

## 6. Tests

`make test` runs `go test ./pts/... ./cmd/pts/`. `pts/tests/README.md` describes the harness and its directives in full.

- **Go unit tests** in each package. The lowering tests compare printed IR, and the cgen tests check generated C. The fork's parser changes are tested in `internal/parser/pts_test.go`.
- **Runtime C tests.** Each `pts/runtime/tests/*_test.c` file is its own program. `go test ./pts/toolchain/` runs them under ASan and UBSan, and the threaded ones (pool, scheduler, sync, and the typed arrays' race test) under ThreadSanitizer too, with collection off (bdwgc's signals don't work under TSan).
- **Programs** in `pts/tests/`:
  - `run/*.pts` (about 350) must build, run and print their `// expect:` block (`TestRun`). Directives also give the exit code, stderr texts, or `// threads: 1 2 4` to run under each `PTS_THREADS` value.
  - `errors/*.pts` (about 80) must fail to compile with the `// error:` texts they name (`TestErrors`).
  - `http/*.pts` are servers and clients that Go tests drive over TCP (`http_test.go`): `TestHTTPServer`, `TestHTTPServerEveryLoop`, `TestHTTPServerLifecycle` (disconnects, slow readers, `stop()`), `TestFetchFromGoServer`, and `TestFetchUnderSanitizers` (ASan and UBSan). Simpler fetch and server cases are run tests (`run/fetch_*.pts`, `run/http_values.pts`).
  - `examples_test.go` builds each program in `examples/` and checks it under 1 and 4 threads against an independent answer (Go's `crypto/sha256`, Go's PNG decoder, a Go HTTP client, a Go log generator's counts).
  - `corpus/<theme>/*.pts`: 300 realistic plain-TypeScript programs in six themes, compared with Node (`TestCorpusMatchesNode`).
  - `stress/*.pts`: 13 parallel programs that race on the scheduler, the pool, the collector and shared data (`TestStress`). A nightly CI job runs them 100 times.
  - Generated corpora (`numbers_test.go`, `strings_test.go`): programs written at test time, with thousands of number literals and operations, String methods on random strings, and the case mapping and normalization of planes 0 and 1, compared with Node line by line.
  - `docs/examples/*.pts`: the docs' samples, run as run tests (named `docs/<name>`).
- **Differential tests against Node** (D-O04, D-009). Every test named `…MatchesNode` is one. A program that uses no ParallelScript feature must print exactly what Node prints and exit with the same code; stderr isn't compared. The features are `parallel`, `.par`, the integer types, and `pts:` imports other than the Node-like `pts:fs`, `pts:fs/promises`, `pts:path`, `pts:process` and `pts:assert`, which the harness maps to Node's `node:` modules (`node:assert/strict` for `pts:assert`). Node runs a copy of each module as an `.mts` file, with `--experimental-transform-types`. Without Node on PATH these tests skip, unless `PTS_REQUIRE_NODE` is set; `make test-diff` and CI set it.
- **Tools.** `pts/lsp/lsp_test.go` drives the language server in process over pipes, and `TestLSPServesOverStdio` (`cmd/pts`) over real stdio. `pts/lsp/grammar_test.go` and the extension's grammar test (`make vscode`, with vscode-textmate) check the parser and the grammar against the same samples of `parallel` (`pts/editors/vscode/test/parallel_samples.json`). `npm run test:vscode` runs the extension in a headless VS Code. `cmd/pts/test_test.go` runs `pts test` over projects with suites, hooks, skips and failures. CI's `vscode` job runs `make vscode` and the headless suite.
- **Upstream suites.** `make test-upstream` runs the upstream packages the fork touches, plus `TestLocal`. `make test-upstream-cli` runs the CLI and language-server suites.

CI runs `.github/workflows/pts.yml` (the PTS tests, the upstream CLI and LSP suites, and the extension) on every push, and `pts-nightly.yml` for the stress programs. The benchmarks in `pts/bench/` aren't gates (D-O15). `make bench` (`run.sh`) runs them with hyperfine against Node, Bun and Go twins (`main.ts` where Node and Bun need their own, `main.go`), in interleaved rounds (D-167). `make bench-http` (`serve.sh`) loads the server benchmarks (`http_hello`, `http_json`) with oha (D-137). Both keep the raw JSON of every run in `pts/bench/results/latest/` (not committed); a milestone's runs are kept as `pts/bench/results/<milestone>/`, such as `m6.5a/`. `docs/BENCHMARKS.md` records the tables.

## 7. Where things live

| Path | Purpose |
|---|---|
| `cmd/pts/` | The command line: `build`, `run`, `emit-c`, `test`, `lsp`, `help`, `version` |
| `pts/frontend/` | Loads a program through the forked tsc, with fixed options; orders modules; hints on tsc's errors |
| `pts/lsp/` | `pts lsp`: upstream's language server with the compiler's programs and ParallelScript's diagnostics (D-168) |
| `pts/editors/vscode/` | The VS Code extension: starts `pts lsp --stdio` for `.pts` files, with a grammar generated from TypeScript's that highlights `parallel` (D-169) |
| `pts/lib/prelude.d.ts` | Global declarations: `console`, numeric types, timers, `process`, `fetch` and the Fetch classes, the abort classes, `parallel`, `.par` |
| `pts/lib/*.pts`, `pts/lib/fs/`, `pts/lib/internal/` | The `pts:` modules, in ParallelScript |
| `pts/lib/lib.go` | Embeds the library and serves it at `ptslib:///`; `GlobalModules` |
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
| `pts/tests/` | The test harness, `run/`, `errors/`, `corpus/`, `stress/` and `http/`, and the examples' tests |
| `pts/bench/` | Benchmarks with their Node, Bun and Go twins; `run.sh` and `serve.sh` |
| `pts/bench/results/` | Benchmark runs' raw JSON: `latest/` (not committed) and one directory per milestone |
| `examples/` | Four example programs (`hasher`, `mandelbrot`, `api-server`, `log-analyzer`) and their `README.md` |
| `internal/parser/pts.go` | Fork: the `parallel` keyword |
| `internal/checker/pts.go` | Fork: numeric types in the checker; exports for the lowering |
| `internal/tspath/`, `internal/module/`, `internal/core/` | Fork: `.pts` as a TypeScript extension (`docs/FORK.md` lists every edit) |
| `internal/project/`, `internal/ls/`, `internal/lsp/` | Fork: the language server's hooks for `pts lsp` (`internal/lsp/pts_content.go` serves the library's files) |
| `docs/` | The spec, decisions, guide, fork ledger, benchmarks, plan and this document; `reviews/` holds independent reviews |
| `docs/examples/` | The docs' samples, run as tests |
| `Makefile` | `build`, `test`, `test-diff`, `test-stress`, `test-upstream`, `test-upstream-cli`, `bench`, `bench-http`, `vscode` |
| `pts-bin/pts` | The compiler, as `make build` makes it (not committed) |
| Everything else | Upstream typescript-go |

The runtime's sources by area:

| Area | Files in `pts/runtime/src/` |
|---|---|
| Objects, cells, interface sites | `object.c` |
| Strings | `string.c`, `string_methods.c`, `string_case.c`, `collation.c`, `normalize.c`, `uri.c` |
| Numbers and Math | `number.c`, `number_methods.c`, `integer.c`, `math.c`, `ieee754.c` |
| Arrays and collections | `array.c`, `map.c`, `typed_array.c` |
| Copies of shared memory | `shared_memory.c` |
| `any` | `dynamic.c`, `dynamic_methods.c`, `convert.c` |
| Errors | `error.c`, `runtime.c` |
| Output | `console.c`, `inspect.c`, `table.c` |
| JSON, Date, RegExp | `json.c`, `date.c`, `regexp.c`, `regexp_object.c`, `regexp_string.c` |
| Generators and promises | `generator.c`, `promise.c` |
| Loops, tasks and the pool | `loop.c`, `scheduler.c`, `pool.c` |
| Library intrinsics | `process.c`, `fs.c`, `sync.c` |
| HTTP server and `fetch` client | `http.c`, `fetch.c`, `http_internal.h` |

## 8. Adding a feature

SPEC §9.2 gives the recipe for a new construct:

1. Handle it in `pts/lower`, with a lowering test on the printed IR.
2. Add any IR node it needs, with its printer case.
3. Add the `pts/cgen` case, and any runtime functions, with C unit tests.
4. Add a run test (and an error test for what stays unsupported).
5. Run `go test ./pts/... ./cmd/pts/`.

Log the design in `docs/DECISIONS.md`, and add a section to SPEC §9 that names the functions and files involved.

## 9. What is planned

The plan's milestones, M0 to M7, are done; M7 added the language server, the VS Code extension, hints on tsc's errors and `pts test` (D-168 … D-172).

- **M8**, if the run is extended: escape analysis, integer inference, unboxed arrays, inline caches for `any`, an experiment with a precise collector, and the generator and async state machines moved into an IR pass (D-153).
- **Known limits of `pts:http`** that later work may lift: no TLS, no streamed bodies, no WebSocket, and one connection per `fetch` request, with no keep-alive pool (D-136, D-160).
- **Known limits of the tools:** `pts lsp` doesn't show the errors that only the lowering finds, most PTS1001s (D-168); `pts:test` has no subtests, `only`, mocks or snapshots, and `pts test` runs files one after another (D-171); the extension isn't published to the Marketplace (D-169).
