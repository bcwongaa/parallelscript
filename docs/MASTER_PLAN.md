# ParallelScript master plan

The ordered task list. Each session takes the **first unchecked task**, finishes it end to end (code, tests, docs, commit, push), ticks it with the short hash (`- [x] M1.2 … (abc1234)`), and moves on. A task too big for one session gets split in place (`M1.2a`, `M1.2b`) without dropping scope.
Source of truth for semantics: `docs/BRIEF.md` → `docs/SPEC.md` → `docs/DECISIONS.md`. Source files use the `.pts` extension (owner override).

## Milestone checklist (run when a milestone's last task is ticked)
1. The full test suite is green locally (`make test`, or the commands in `PROGRESS.md`), and CI is green on the pushed commit (`gh run list -L 1`).
2. `PROGRESS.md` is updated.
3. `docs/BENCHMARKS.md` has a table for this milestone (`pts/bench/run.sh`). Write "n/a" if there are no benchmarks yet.
4. `git tag mN && git push origin mN`.
5. A Telegram message to the owner (see `CLAUDE.md`) with what now works, a short code sample, and the numbers.

---

## M0: Bootstrap
- [x] **M0.1 Memory files.** (3441372)
  - Write `docs/SPEC.md` from brief §2, with `.pts` substituted, and an empty "Implementation notes" section for later.
  - Write `docs/DECISIONS.md`: the brief §2 decisions marked *locked by owner*, then `docs/agent/PREDECISIONS.md` entries as D-O01…
  - Write `docs/FORK.md` (the upstream-modification ledger), `PROGRESS.md` and `docs/BENCHMARKS.md` (empty table skeleton).
  - Add `*.pts linguist-language=TypeScript` to `.gitattributes`.
- [x] **M0.2 Study notes.** (cc6210c)
  - Write `docs/agent/NOTES-tsgo.md`: where the scanner, parser, binder, checker, program, compiler host and LSP live; how to build a `Program` from Go and get a typed AST plus a checker; how upstream tests run, and which suites cover the parser and checker.
  - Write `docs/agent/NOTES-rayon.md`: `join`, Chase–Lev deques, adaptive splitting, and how a thread that starts work helps while it waits.
  - Keep both short and practical.
- [x] **M0.3 `.pts` in the front end.** (139254b)
  - Make the minimal `// PTS:`-marked edits so `.pts` files parse and check as TS and resolve in imports (the `tspath` extension lists, script kind, the module resolver).
  - Add a Go test.
  - Make sure upstream tests for the touched packages still pass.
- [x] **M0.4 Runtime skeleton.** (26476ae)
  - `pts/runtime/` with a C11 runtime: values, strings, `console.log` of primitives, panics.
  - Vendor bdwgc and libuv as pinned submodules under `pts/third_party/`, with a CMake (or Make) build into a cache dir (decision O11).
  - The runtime builds and links on its own.
- [x] **M0.5 Pipeline end to end.** (746d6fb)
  - Write the IR package `pts/ir`, a lowering for the tiny subset needed, the C emitter with `#line`, and the driver that runs clang.
  - Write the `cmd/pts` CLI with `build`, `run` and `emit-c`.
  - `console.log("Hello, ParallelScript")` must compile and run end to end.
- [x] **M0.6 Test harness.** (3c1d8a7)
  - `pts/tests/run/*.pts` with `.out` files or an `// expect:` block.
  - `pts/tests/errors/*.pts` with expected message substrings.
  - A Go test runner (`go test ./pts/tests/...`) that runs these in parallel.
  - A Node differential runner (decision O04) that runs automatically for plain-TS tests, which are marked in a header comment or detected automatically.
  - A Makefile with `build`, `test`, `test-diff` and `bench`.
- [x] **M0.7 CI.** (6ee609b)
  - A GitHub Actions workflow (`.github/workflows/pts.yml`) that installs clang, builds `pts`, runs the PTS tests, the differential tests and the touched upstream Go tests on every push.
  - Disable or limit the upstream workflows that cannot run in this repo (release, CodeQL and similar); leave a note in `docs/FORK.md`.
- [x] **M0.8 Milestone M0.** (m0) Run the checklist (benchmarks n/a). The Telegram message also states the plan ahead.

## M1: Core language
- [x] **M1.1 Numbers.** (554c989)
  - `number` arithmetic and comparisons with exact f64 semantics.
  - Number-to-string conversion matching JS exactly (a Ryu-style shortest round-trip, with JS rules for exponent and fixed notation).
  - A large differential test corpus for formatting.
- [x] **M1.2 Integer types.** (11e0412)
  - Prelude `int i8 … u64 f32 f64` declared as aliases in a PTS lib `.d.ts`.
  - A PTS semantic pass for literal typing from context, lossless-only implicit conversion and `as` conversions (Rust rules).
  - Integer arithmetic: wrapping, truncating `/`, `RangeError` on division by zero.
  - Error tests for every rule.
- [x] **M1.3a Booleans and string basics** (b554afc) (split by D-013).
  - Booleans: `!`; strings in UTF-8 with an ASCII flag and UTF-16 semantics.
  - Concatenation (`+` with a string and any primitive, with JS's ToString), template literals, `length`, `===`/`!==` and `<`/`<=`/`>`/`>=` on strings (UTF-16 code-unit order).
  - `console.log` writes lone surrogates as U+FFFD, as Node does (the known issue).
- [x] **M1.3b String methods** (58ce643) (split by D-013).
  - Indexing, `charCodeAt`, `slice`, `substring`, `indexOf`, `includes`, `startsWith`, `endsWith`, `toUpperCase`/`toLowerCase`, `trim*`, `padStart`/`padEnd`, `repeat`, with UTF-16 index semantics (O(1) for ASCII strings).
  - `split` on a string separator moves to M2.4, because it returns an array.
- [x] **M1.4a1 Statements and operators** (a441fe467) (split from M1.4a by D-017).
  - `let`/`const` (also without an initializer); `if`, `switch`, `for`, `while` and `do`; `break`/`continue` with labels; the ternary operator.
  - Every operator, including assignment, compound assignment, `++`/`--`, `**`, bitwise operators (JS int32 semantics for `number`), `typeof`, `void`, logical operators and comma, evaluated in JS's left-to-right order.
  - The numeric rules of `pts/check` on assignments, compound assignments, `switch` cases, the ternary and logical operators.
- [x] **M1.4a2 Functions** (8c4593703) (split from M1.4a by D-017).
  - Function declarations; parameters and returns (with the numeric rules); recursion; calls in JS's evaluation order.
  - Unsupported-feature compile errors: `var`, `eval`, `with`, `arguments` and the rest of the brief's list.
- [x] **M1.4b1 Module-scope bindings in functions** (464de5e4a) (split from M1.4b by D-021).
  - Top-level bindings that functions use become C globals (until then they are locals of the top-level code), with JS's "Cannot access before initialization" check.
- [x] **M1.4b2 Modules** (34b33d66a) (added by D-008, split by D-021).
  - ES module `import`/`export` between `.pts` files with relative specifiers (D-L19), including `export default`, re-exports and namespace imports (`import * as m`).
  - Module initialisation in dependency order, each module once; import cycles behave as in Node for the cases tsc accepts.
- [x] **M1.5 Benchmarks setup.** (30c0871d4)
  - `pts/bench/` with a hyperfine script comparing ParallelScript against Node, Bun and Go.
  - Implement fib (Node and Bun run the same source; a `.go` twin). Record the results.
  - nbody moves to M2.7 (D-023): its standard form needs arrays or classes and `Math.sqrt`.
- [x] **M1.6 Milestone M1.** (m1)

## M2: Data and memory
- [x] **M2.1 GC and the object model.** (1c0570727)
  - Integrate bdwgc in thread-aware mode with thread-local allocation; use atomic allocation for pointer-free data.
  - Type descriptors for every runtime type (name, fields mapped to offsets, methods), to be reused by `console.log` and JSON.
- [x] **M2.2a Class basics** (d405e8fd2) (split from M2.2 by D-025).
  - Class declarations with fixed layouts: fields (declared and initialised, in declaration order), constructors, `new`, `this`, methods.
  - Reading and assigning fields, calling methods; the numeric rules on fields, parameters and results.
- [x] **M2.2b `console.log` of instances** (c58de70a3) (split from M2.2 by D-025).
  - Node's format from the type descriptors, e.g. `Point { x: 1, y: 2 }`: nested instances, line breaking past 72/80 characters, depth 2 (`[Point]`), circular references (`<ref *1>`, `[Circular *1]`), strings quoted as Node quotes them.
- [x] **M2.2c Inheritance** (37a3a31b9) (split from M2.2 by D-025).
  - Single inheritance with `extends`; `super(...)` and `super.method()`; vtables and overriding; abstract classes; `instanceof`.
- [x] **M2.2d1 Accessors and parameter properties** (4ea61b042) (split from M2.2 by D-025, from M2.2d by D-029).
  - Getters and setters, overridable like methods; abstract fields (`abstract name: string`), dispatched like accessors (D-028).
  - Parameter properties; the `private`, `protected` and `readonly` modifiers.
- [x] **M2.2d2 Statics and `#private`** (e3957937a) (split from M2.2 by D-025, from M2.2d by D-029).
  - Static fields, methods and blocks; `#private` fields and methods.
- [x] **M2.3a Object literals and exact object types** (32645da3f) (split from M2.3 by D-032).
  - Object literals become anonymous struct types; `console.log` prints them as Node does (`{ x: 1 }`, `[Object]`).
  - Object types (interfaces, type literals, aliases) whose values are exactly such literals; the numeric rules through properties.
- [x] **M2.3b1 Interfaces over fields** (ddf29e8ac) (split from M2.3 by D-032, from M2.3b by D-034).
  - Structural dispatch, cached at run time, so that class instances and objects of other shapes can be used where an object type is expected, when fields provide its properties.
- [x] **M2.3b2 Methods and accessors through interfaces** (aa0d4a74c) (split from M2.3b by D-034).
  - Interfaces with methods, and class methods and accessors that provide an interface's methods and properties, through per-(concrete type, interface) tables built at compile time where possible.
- [x] **M2.4a Array basics** (7d9f41a0a) (split from M2.4 by D-037).
  - Growable arrays of any element type that are race-safe per brief §2.5 (capacity inside the buffer).
  - Literals, `length` (and truncating it), `a[i]` reads (D-015's rule) and writes, `push`, `pop`, `shift`, `unshift`, `for…of` over arrays and `Array.isArray`.
  - `console.log` of arrays as Node prints them.
- [x] **M2.4b Array methods without callbacks** (7f08c4a94) (split from M2.4 by D-037).
  - `String.prototype.split` on a string separator (moved from M1.3b, D-013).
  - `slice`, `splice`, `concat`, `indexOf`, `includes`, `join`, `reverse`, `sort` without a comparator (stable, with JS default comparison), `flat`, `fill` and `Array.from` of an array.
- [x] **M2.5a Closures and arrow functions** (3ae2a7eb5) (split from M2.5 by D-040).
  - Function values: function types, arrow functions and function expressions, function declarations used as values, calls of function values.
  - Closures with explicit environments in the IR: captured variables, shared where they may change; nested function declarations that capture; JS's per-iteration `let` bindings in loops.
  - `this` in arrow functions (lexical); `console.log`, `typeof` and `===` on function values.
- [x] **M2.4c Array methods with callbacks** (b737989a8) (split from M2.4 by D-037; after M2.5a, which brings function values, D-040).
  - `map`, `filter`, `reduce`, `forEach`, `find`, `findIndex`, `some`, `every`, `flatMap`, `sort` with a comparator, and `Array.from` with a mapper.
- [x] **M2.5b1 Methods in object literals and methods as values** (ebd3326a3) (split from M2.5 by D-040, from M2.5b by D-043).
  - Methods in object literals, with `this` bound to the object (moved from M2.3b2 by D-036).
  - Methods as values, with JS's `this` rules for them; `bind` and `call` where the types allow.
- [x] **M2.5b2 Accessors in object literals and `apply`** (f4c1d040f) (split from M2.5b by D-043).
  - Getters and setters in object literals, through interfaces too, printed as Node prints them (`[Getter]`, `[Setter]`, `[Getter/Setter]`).
  - `apply` where the types allow (an array argument for a function whose parameters all have its element type).
- [x] **M2.5c `Map` and `Set`** (b932f06e8) (split from M2.5 by D-040).
  - `Map` and `Set` with insertion order and SameValueZero semantics: `get`, `set`, `has`, `delete`, `clear`, `size`, `forEach`, and `for…of` over them and their `keys()`, `values()` and `entries()`.
- [x] **M2.6a Integer types as checker types** (5ebc5408e) (split from M2.6 by D-047).
  - The prelude's integer types and f32 are distinct number types inside the checker (D-048), so they flow through every type tsc builds: array elements, `Map`/`Set` keys and values, function types, object types and type arguments (`u8[]`, `Map<string, int>`, `(n: int) => int`).
  - The semantic pass types untyped literals and checks lossless conversion at the new boundaries (array literal elements, built-in method arguments, element assignments).
- [x] **M2.6b1 Generic functions and object types** (849ee7586) (split from M2.6 by D-047, from M2.6b by D-049).
  - Generic function declarations by monomorphization: inferred and explicit type arguments, integer type arguments, generic calls in generic code, recursion.
  - Generic interfaces and type aliases (`Pair<string, int>`).
- [x] **M2.6b2 Generic classes** (ff965a933) (split from M2.6 by D-047, from M2.6b by D-049, from M2.6b2 by D-050).
  - Generic classes (`Box<int>`), one class per instance, printed with the class's name; generic superclasses and subclasses.
- [x] **M2.6b3 Generic methods and nested generic functions** (dc80bbef9) (split from M2.6b2 by D-050).
  - Generic methods of classes (instance and static); generic functions inside functions and blocks. Generic methods of object literals and interfaces move to M3.3 (D-051).
- [x] **M2.6c Enums** (2ce1c5b4f) (split from M2.6 by D-047).
  - Numeric and string enums (`const enum` too), as Node runs them through `tsc` (D-O04); `console.log` of enum values and of the enum object.
- [x] **M2.6d1 Tuples** (7dd24ed8c) (split from M2.6 by D-047, from M2.6d by D-053).
  - Tuples of fixed length as fixed layouts printed as arrays: literals where a tuple goes and `as const`, constant indices, writes, `length`.
- [ ] **M2.6d2 Destructuring** (split from M2.6d by D-053).
  - Destructuring of objects, arrays and tuples, with defaults, in declarations, parameters, assignments and `for…of`; default parameter values.
- [ ] **M2.6d3 Map entries as pairs** (split from M2.6d by D-053).
  - `entries()`, `for (const [k, v] of map)`, `new Map([[k, v], …])` and iterators as values (moved from M2.5c by D-046); `apply` with tuples.
- [ ] **M2.6e Spread, rest and hash-map objects** (split from M2.6 by D-047).
  - Spread and rest in calls, array literals and object literals; rest parameters.
  - `Record<string, T>` and index signatures as hash maps (D-L10), with `delete`.
- [ ] **M2.7 binary-trees.** Add the binary-trees benchmark (GC stress) and a string-building benchmark.
  - Add nbody (moved from M1.5 by D-023), with `Math.sqrt` pulled forward from M3.5 if it isn't there yet.
- [ ] **M2.8 Milestone M2.**

## M3: The TypeScript feel
- [ ] **M3.1 Unions and narrowing.**
  - Unions as tagged values; `null` and `undefined` as distinct values.
  - Optional properties, stored as `T | undefined` (moved from M2.3 by D-032).
  - Narrowing via `typeof`, `instanceof`, `in`, discriminants, equality and user-defined type guards (the backend emits tag tests).
  - Optional chaining, `??`, `==` with JS semantics.
- [ ] **M3.2 Exceptions.**
  - `throw`, `try`, `catch` and `finally` with JS semantics, where any value can be thrown.
  - `Error` and its subclasses with `message`, `name` and `cause`; `stack` with `.pts` lines (a best effort now).
  - Uncaught exceptions are handled per decision O07.
- [ ] **M3.3 `any`, `unknown` and casts.**
  - `any` as a checked dynamic value using descriptors, with runtime checks at typed boundaries.
  - `unknown`; checked `as` downcasts; checked `x!`.
  - Generic function values and generic methods of object literals and interfaces, whose type parameters take dynamic values (moved from M2.6b3 by D-051).
- [ ] **M3.4 JSON.**
  - `JSON.parse` returning `any`; `JSON.stringify` with indent and replacer basics, matching Node byte for byte.
  - Add the JSON benchmark and the `Map`-heavy word-frequency benchmark.
- [ ] **M3.5 Library breadth.**
  - The remaining common `String`, `Array`, `Object` (`keys`/`values`/`entries`/`assign`/`freeze`) and `Number` methods (`toFixed`, `parseInt`, `parseFloat`, `isInteger` and so on).
  - `Math` in full; basic `Date`.
- [ ] **M3.6 `console.log` parity.** A full `util.inspect` port per decision O16: nesting, line breaking, `Map`/`Set`, class names, `%s`/`%d`/`%o` format strings, and `console.error`/`warn`/`info`.
- [ ] **M3.7 Corpus.**
  - Grow the differential corpus to at least 300 realistic programs. Use subagents to write them in batches by theme: algorithms, text processing, OOP, data transforms, closures and errors.
  - Fix or record every divergence.
- [ ] **M3.8 Milestone M3.**

## M4: Async on one loop
- [ ] **M4.1 Generators.** An IR state-machine transform with heap frames; `function*`, `yield`, `yield*`; iterators, `for…of` over iterables and `Symbol.iterator`.
- [ ] **M4.2 Async and promises.**
  - The async/await state machine.
  - `Promise` with `then`, `catch`, `finally`, `all`, `allSettled`, `race`, `any`, `withResolvers` and `resolve`/`reject`.
  - Microtask ordering identical to Node; add ordering differential tests.
- [ ] **M4.3 The event loop.** A libuv event loop with `setTimeout`, `setInterval`, `clearTimeout`, `queueMicrotask` and `performance.now()`, with timer and IO ordering matching Node.
- [ ] **M4.4 Async iteration.** Async generators and `for await`.
- [ ] **M4.5 `pts:fs`, `pts:process` and `pts:path`.**
  - `pts:fs` with sync and promise APIs; `pts:process` with `argv`, `env`, `exit` and `cwd`.
  - Also `pts:path`, pulled forward from M6 because it is small and useful now.
- [ ] **M4.6 Milestone M4.**

## M5: Parallel (the headline; make the demo great)
- [ ] **M5.1 The `parallel` keyword.**
  - Add `parallel` as a contextual keyword to the fork's scanner, parser and checker, following decision O05.
  - The checker types `parallel e` as `Promise<Awaited<T>>`.
  - Add `parallel for` statement parsing.
  - Upstream parser and checker tests stay green; add fork parser tests.
- [ ] **M5.2 Multi-loop runtime.**
  - N event loops (`PTS_THREADS`), one per core, over the shared heap.
  - Cross-loop promises, waking the awaiting loop with `uv_async_send`.
  - Task placement for `parallel <expr>` (least-loaded, or round-robin with stealing of unstarted tasks); tasks never migrate.
- [ ] **M5.3 The fork-join pool.** A Chase–Lev work-stealing pool, `join(a, b)`, adaptive splitting, and threads that help while they wait. Unit tests under ASan, UBSan and TSan where feasible.
- [ ] **M5.4 `.par` views.**
  - `.par` on arrays, typed arrays and `range`.
  - `map` (order-preserving), `filter`, `forEach`, `reduce`, `flatMap`, `some`, `every`, `find`, `count`, `sum`, `min`, `max`, `sort` and `sortBy` (parallel merge sort), and `toArray`.
  - `pts:parallel` exporting `range` and `join`.
  - Lib `.d.ts` declarations with branded parallel-callback types.
- [ ] **M5.5 `parallel for` and the guardrail.**
  - Lower `parallel for` to a parallel `forEach`. `continue` is allowed; `break`, `return`, `yield`, `await` and labelled jumps are compile errors.
  - If an iteration throws, cancel the rest and rethrow the first error after joining.
  - The single guardrail: assigning a captured outer `let` inside a parallel body is a compile error, with a suggestion. **No other parallelism checks.**
- [ ] **M5.6 `pts:sync`.** `Mutex<T>` (`withLock` and async `lock`), `AtomicInt`/`Atomic<int>`, `Channel<T>` (bounded and unbounded, async `send`/`recv`, `close`, `for await`) and `Once`.
- [ ] **M5.7 Stress and scaling.**
  - Parallel stress tests, with a nightly CI job that runs them 100 times.
  - Parallel benchmarks: mandelbrot `.par`, sum/reduce over 100M numbers, word count over many files, and `parallel` fan-out. Report speedup against one thread.
  - The race-safety invariant from brief §2.5, verified by targeted tests.
- [ ] **M5.8 Milestone M5.** The Telegram message must show off `parallel` and `.par`, with speedup numbers.

## M6: Real programs
- [ ] **M6.1 RegExp.** Vendor PCRE2 and support JS syntax and flags (decision O09): `test`, `exec`, `match`, `matchAll`, `replace`, `split`, named groups and `lastIndex`.
- [ ] **M6.2 `pts:http` server.** Optionally accepts connections on every loop (`SO_REUSEPORT`). Request/response API in the style of Node and Bun. Hello-world and JSON-echo benchmarks with `oha`.
- [ ] **M6.3 `fetch`.** A global `fetch` over HTTP/1.1 per decision O10, with tests against a local server.
- [ ] **M6.4 Example apps** in `examples/`, each with a test or smoke script in CI:
  - a parallel file hasher
  - a JSON API server that uses every core
  - a mandelbrot renderer that writes a PNG (with a zlib-free stored-deflate PNG writer in `.pts`)
  - a parallel log analyzer
- [ ] **M6.5 README.** A README that sells the language: the pitch, install and build steps, these examples, benchmark tables, and the documented differences from TypeScript.
- [ ] **M6.6 Milestone M6.**

## M7: Editor support
- [ ] **M7.1 `pts lsp`.** The forked language server via `pts lsp`, with the PTS prelude and lib declarations resolved and PTS semantic diagnostics published alongside tsc's.
- [ ] **M7.2 VS Code extension.**
  - `pts/editors/vscode`, starting the server for `.pts` files.
  - A TextMate grammar, extended from TS, with `parallel` highlighted.
  - Package a `.vsix` with `vsce`; installation instructions in the README.
- [ ] **M7.3 Usability pass.**
  - Error-message quality, `pts --help`, `pts test`.
  - A `docs/GUIDE.md` tutorial covering TS developers' most common questions.
  - Fix the top papercuts found by writing two new small programs from scratch.
- [ ] **M7.4 Milestone M7 and "usable".**
  - Run the checklist.
  - Write `docs/agent/DONE`.
  - Send the final Telegram report: summary, how to try it, benchmark highlights, known gaps, and a suggested M8 plan.

## After DONE (only if the owner extends the run)
M8 performance (escape analysis, integer inference, unboxed arrays, inline caches for `any`, a precise GC experiment), then the stretch goals from brief §6.
