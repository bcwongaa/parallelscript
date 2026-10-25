# ParallelScript for TypeScript developers

ParallelScript is TypeScript compiled ahead of time to a native binary. The compiler, `pts`, checks your program with TypeScript 7's own type checker, turns it into C and builds it with clang. The binary has a garbage collector and can use every core: next to `async` and `await`, there are `parallel` tasks, parallel array operations and `parallel for` loops.

This guide is for people who already write TypeScript. It covers what carries over, what doesn't, and what is new. The language reference is [SPEC.md](SPEC.md), and the reasons behind each choice are in [DECISIONS.md](DECISIONS.md).

Every program in this guide is a file in [docs/examples/](examples/), with its expected output at the end. The test suite runs each one, and runs the plain TypeScript ones under Node too. The programs that show compile errors are in [pts/tests/errors/](../pts/tests/errors/), each with the exact errors it must produce.

## Contents

1. [Getting started](#getting-started)
2. [Tutorial: from a loop to every core](#tutorial-from-a-loop-to-every-core)
3. [Questions TypeScript developers ask](#questions-typescript-developers-ask)
4. [What stays the same](#what-stays-the-same)
5. [What's different](#whats-different)
6. [Integer types](#integer-types)
7. [Parallelism](#parallelism)
8. [The `pts:` modules](#the-pts-modules)
9. [Testing](#testing)
10. [Common compile errors](#common-compile-errors)
11. [Where to go next](#where-to-go-next)

## Getting started

Build the compiler once, from the repository root, with `make build`. It lands at `pts-bin/pts`.

| Command | What it does |
|---|---|
| `pts run app.pts [args…]` | Compiles and runs the program. Arguments after the file go to the program. |
| `pts build app.pts -o app` | Compiles to a native executable. Without `-o`, the executable is named after the file. |
| `pts emit-c app.pts` | Prints the generated C, whose `#line` directives point at your `.pts` lines (gdb and lldb show those lines too). |
| `pts test [paths…]` | Builds and runs the test files (`name.test.pts` and Node's other test file names) under the paths ([Testing](#testing)). |
| `pts help [command]` | Prints the usage, or a command's (as does `pts <command> --help`); `pts version` prints the version. |

```sh
pts-bin/pts run docs/examples/hello.pts
```

That prints `Hello, ParallelScript`. The first build also compiles the runtime and its C libraries (the garbage collector, libuv, PCRE2 and a few more) and caches them under `~/.cache/pts/`, so later builds are faster. The executable needs nothing but the C library.

`pts` exits with 0 on success, 1 on compile errors and 2 on a malformed command line. `pts run` exits with the program's own exit code. If the compiler runs into a bug of its own, it says so, `error PTS9001: internal compiler error …` at the line it was compiling, and exits with 3; `PTS_DEBUG=1 pts run file.pts` shows what a bug report needs. (A `pts` built from one version with `PTS_HOME` at another version's runtime sources can make C that doesn't compile, which is reported the same way.)

For VS Code, `pts/editors/vscode` is an extension that highlights `parallel` as a keyword and runs `pts lsp`, the language server: completions, hovers, go to definition (into the `pts:` modules too) and errors, ParallelScript's own among tsc's. `make vscode` builds `pts/editors/vscode/dist/parallelscript.vsix`; install it with `code --install-extension`, then put `pts` on your PATH or point the setting `parallelscript.server.path` at it ([its README](../pts/editors/vscode/README.md) has the details). Other editors that speak the Language Server Protocol can run `pts lsp --stdio` for `.pts` files.

For whole programs to start from, [examples/](../examples/README.md) at the repository root has four, each a single `main.pts` that the test suite runs: a file hasher, a mandelbrot renderer that writes a PNG, a JSON API server and a log analyzer.

There is no `tsconfig.json`: the options are fixed.

- TypeScript runs in strict mode.
- Every file is an ES module. Top-level `await` works in the file you run, though not yet in the modules it imports.
- The standard library is ECMAScript's, without the DOM: `document` is "Cannot find name 'document'".
- Source files end in `.pts`. `import "./util"` finds `util.pts` first, then the usual TypeScript extensions.
- Imports are relative files or the standard library's `pts:` modules. There is no npm or `node_modules` yet.

## Tutorial: from a loop to every core

This walk-through takes one question, how many numbers below a million have a digit sum that is a multiple of 7, and answers it four ways. The whole program is [examples/guide_tutorial.pts](examples/guide_tutorial.pts); run it with `pts-bin/pts run docs/examples/guide_tutorial.pts`.

```ts
// The guide's tutorial: one question answered by a plain loop, by `.par`, by tasks on
// other threads and with integer types, each giving the same answer.
import { range } from "pts:parallel";

const LIMIT = 1_000_000;

// The sum of n's decimal digits.
function digitSum(n: number): number {
  let sum = 0;
  while (n > 0) {
    sum += n % 10;
    n = Math.floor(n / 10);
  }
  return sum;
}

// 1. Plain TypeScript, on one thread: how many numbers below LIMIT have a digit sum
//    that is a multiple of 7?
function countBetween(from: number, to: number): number {
  let found = 0;
  for (let n = from; n < to; n++) {
    if (digitSum(n) % 7 === 0) {
      found++;
    }
  }
  return found;
}
console.log("loop: ", countBetween(1, LIMIT));

// 2. The same test over every core: `.par.count` runs it on pieces of the range at once.
console.log(".par: ", range(1, LIMIT).par.count((n) => digitSum(n) % 7 === 0));

// 3. Two tasks, each on another thread's event loop, awaited together.
const halves = await Promise.all([parallel countBetween(1, LIMIT / 2), parallel countBetween(LIMIT / 2, LIMIT)]);
console.log("tasks:", halves[0] + halves[1]);

// 4. With an integer type, `/` divides as integers, so there is no Math.floor.
function digitSumInt(n: int): int {
  let sum: int = 0;
  while (n > 0) {
    sum += n % 10;
    n = n / 10;
  }
  return sum;
}
console.log("int:  ", range(1, LIMIT).par.count((n) => digitSumInt(n as int) % 7 === 0));
```

```text
loop:  142879
.par:  142879
tasks: 142879
int:   142879
```

1. **Plain TypeScript.** `digitSum` and `countBetween` are what you'd write for Node, and they compile as they are: no annotations beyond TypeScript's, no new syntax. The executable runs them on one thread.
2. **`.par` for data.** `range(1, LIMIT)` is the numbers from 1 to LIMIT − 1 without an array, and `.par` is its parallel view: `count` cuts the range into pieces, tests them on every core, and adds up the pieces' counts. Arrays have the same view (`xs.par.map(f)`, `xs.par.sum()`). The callback runs on several threads at once, so it may read anything but must not assign variables from outside it: `let found = 0; range(1, LIMIT).par.forEach((n) => { if (digitSum(n) % 7 === 0) found++; })` is the compile error PTS3001, whose message suggests `.par.reduce`, `.par.sum`, an `AtomicInt` or a `Mutex` ([The guardrail](#the-guardrail)).
3. **`parallel` for tasks.** `parallel countBetween(1, LIMIT / 2)` starts the call on another thread's event loop and gives a promise of its result, so `Promise.all` waits for both halves. A task can do anything a function can, async work included ([Tasks](#tasks-parallel-e)).
4. **Integer types.** In `digitSumInt`, `n: int` makes `n / 10` integer division and `n % 10` integer arithmetic, so `Math.floor` goes. The integer types are opt-in: `number` is still JavaScript's double ([Integer types](#integer-types)).

The four agree on every run and with any number of threads. On an 8-thread laptop the program takes 0.40 s with `PTS_THREADS=1` and 0.21 s with all threads; the plain loop stays on one thread whatever `PTS_THREADS` says. To check it in a test, see [Testing](#testing).

## Questions TypeScript developers ask

**Will my TypeScript run as it is?** Mostly. A program that uses only TypeScript prints what Node prints; of 300 realistic programs in the test suite, 267 print exactly Node's output, and each other one says why. What can't compile is an error that names the construct: features left out by design, such as `eval`, `Proxy` and `var` (PTS1002), and features not built yet, such as decorators and `bigint` (PTS1001). See [What's different](#whats-different) and [Common compile errors](#common-compile-errors).

**Why does my program throw where Node printed `undefined`?** Types are enforced at run time where JavaScript would carry on with a wrong value: reading past an array's end into a `number`, a cast that isn't true, a missing key of a `Record`. The error says how to write a read that may miss (`?? fallback`, `| undefined`, `in`). See [Checks that throw where Node carries on](#checks-that-throw-where-node-carries-on).

**Can I use npm packages, `node_modules` or a `tsconfig.json`?** Not yet. Imports are relative files and the standard library's `pts:` modules, which cover files, paths, the process, HTTP, `fetch`, tests and parallelism. The compiler's options are fixed: strict TypeScript, ES modules, no DOM.

**Is `number` still a double?** Yes, `number` is JavaScript's number. `int`, `u8`, `i32` and the other integer types are for code that wants integer arithmetic; a program that doesn't name them never meets them.

**How do I use every core?** `.par` on arrays and ranges for data, `parallel f()` for tasks, `parallel for` for loops, as in the tutorial above. Objects are shared between threads, not copied, so passing big data to a task is free.

**Is sharing objects between threads safe?** It can't crash the program or corrupt memory, whatever the threads do. Two threads updating one value without coordination can still lose an update, as in any language with threads: the compiler rejects the commonest case, assigning an outer variable in a parallel body, and `pts:sync` has `AtomicInt`, `Mutex` and `Channel` for the rest ([Sharing state](#sharing-state-ptssync)).

**How fast is it?** Executables start in about a millisecond, where Node takes about 70 ms. On one thread, numeric code ranges from somewhat slower than Node (spectral norm) to two and a half times as fast (recursive `fib`), and it scales with cores; code that allocates many small objects is slower than Node, stops gaining past four threads, and can be slower on eight threads than on one ([Performance](#performance)). [BENCHMARKS.md](BENCHMARKS.md) has the numbers, losses included.

**How do I debug?** `console.log` prints what Node prints, and an uncaught error's stack shows `.pts` files and lines. `pts emit-c` shows the generated C, whose `#line` directives let gdb and lldb step through the `.pts` source.

**How do I write tests?** As for Node's built-in runner, with `pts:test` and `pts:assert`, run by `pts test` ([Testing](#testing)).

**Does my editor understand `.pts` files?** VS Code does with the extension in `pts/editors/vscode`; other editors can run `pts lsp --stdio` ([Getting started](#getting-started)).

**Can I call C libraries?** Not yet: there is no foreign function interface for programs. The standard library reaches the runtime's C through declarations that only it may write.

**Which platforms?** x86-64 Linux. The executables need only libc and libm.

## What stays the same

Most TypeScript compiles unchanged. That includes:

- classes with single inheritance, fields, getters and setters, static members, `private` and `#private`, abstract classes and `instanceof`;
- interfaces and object types (structural, as tsc checks them), object literals, spread and rest, destructuring, optional chaining and `??`;
- unions with every kind of narrowing, generics, enums and tuples;
- closures, and default and rest parameters;
- `async` and `await`, promises (with `all`, `allSettled`, `race`, `any` and `withResolvers`), generators, async generators and `for await`;
- `throw`, `try`, `catch` and `finally`, with `Error` and its subclasses, whose `stack` points at `.pts` lines, and `ReferenceError` for a variable or class used before its declaration has run;
- `Map`, `Set`, `JSON`, `Math`, `RegExp`, typed arrays, basic `Date`, timers and `queueMicrotask`, and `Object.freeze`, after which writes throw `TypeError`, as in strict-mode JavaScript;
- `fetch`, with `Request`, `Response`, `Headers` and `AbortController` as globals, over `http://` and `https://` (see [`fetch`](#fetch)).

Generic functions can be passed as values or returned by factories, retaining their captures. To limit generated code, the compiler follows immutable aliases, closed named parameters, returns of synchronous non-generic factories and static paths through inferred literal trees such as `held.inner[0].factory`, omitting only instances proved unreachable. Literal-container proofs check the entire tree and all whole-root aliases, including immutable object patterns and dense ordinary-array patterns whose every binding leaf selects a literal child container. For example, `const rows = [{ factory }]; const [child] = rows;` can preserve `child.factory`'s proven source. Tuple-shaped fresh arrays, omissions/defaults/rest and terminal-value bindings remain conservative, as does an intermediate container that escapes, mutates or has uncertain representation. Unknown sources, including values read back from `any` or uncertain containers, stay conservative. This optimization does not weaken run-time type checks or impose a new source-language restriction.

Node is the reference for behaviour. A program that uses only plain TypeScript must print exactly what Node prints, unless [SPEC.md §8](SPEC.md#8-documented-differences-from-typescript-and-node) documents a difference. `console.log` formats values as Node's `util.inspect` does: the same quotes, line breaks, `[Array]` past depth 2, `Map(2) {…}` and class names. Numbers print as JavaScript prints them, so `0.1 + 0.2` is `0.30000000000000004`.

The tests hold the compiler to this. They compare 300 ordinary TypeScript programs with Node: 267 print exactly the same, and each of the other 33 says why it differs (24 use something not supported yet, 8 differ by design and 1 is a known bug).

[examples/guide_plain_typescript.pts](examples/guide_plain_typescript.pts):

```ts
// Ordinary TypeScript compiles as it is, and prints what Node prints.
interface Shape {
  kind: "circle" | "square";
  size: number;
  label?: string;
}

class Inventory<T> {
  private items = new Map<string, T>();

  add(name: string, item: T): this {
    this.items.set(name, item);
    return this;
  }

  get(name: string): T | undefined {
    return this.items.get(name);
  }

  get size(): number {
    return this.items.size;
  }
}

function area(shape: Shape): number {
  switch (shape.kind) {
    case "circle":
      return Math.PI * shape.size ** 2;
    case "square":
      return shape.size * shape.size;
  }
}

async function loadShapes(): Promise<Shape[]> {
  await new Promise((resolve) => setTimeout(resolve, 10));
  return [
    { kind: "circle", size: 1, label: "unit circle" },
    { kind: "square", size: 3 },
  ];
}

const shapes = await loadShapes();
const inventory = new Inventory<Shape>();
for (const [i, shape] of shapes.entries()) {
  inventory.add(`shape${i}`, shape);
}
console.log(inventory.size, inventory.get("shape1"), inventory.get("missing")?.label ?? "none");
console.log(shapes.map(area), shapes.map(({ label }) => label?.replace(/\b\w/g, (c) => c.toUpperCase())));
console.log(0.1 + 0.2, 2 ** 53 + 1, [3, 1, 2].sort(), { nested: { list: [1, [2, [3, [4]]]] } });
console.log(JSON.stringify({ shapes }));
console.log(inventory);
```

It prints this, as Node does:

```text
2 { kind: 'square', size: 3 } none
[ 3.141592653589793, 9 ] [ 'Unit Circle', undefined ]
0.30000000000000004 9007199254740992 [ 1, 2, 3 ] { nested: { list: [ 1, [Array] ] } }
{"shapes":[{"kind":"circle","size":1,"label":"unit circle"},{"kind":"square","size":3}]}
Inventory {
  items: Map(2) {
    'shape0' => { kind: 'circle', size: 1, label: 'unit circle' },
    'shape1' => { kind: 'square', size: 3 }
  }
}
```

## What's different

### Checks that throw where Node carries on

At run time every value has a fixed type. Where TypeScript's types are only a promise, ParallelScript checks them, and throws rather than carry on with a wrong value:

- `any` is checked where it flows into a typed place: `const n: number = parsed.count` throws `TypeError` if `count` isn't a number.
- Objects keep the properties their type gives them. You can't add or delete properties after construction. For keys that come and go, use a `Map` or a `Record<string, T>`, which is a hash map underneath.
- Writes make no holes in arrays: writing past the end throws `RangeError`. Reading past the end gives `undefined` where any value can go (printing, building a string, comparing with `undefined`, a place of type `any`, `unknown` or `T | undefined`), but throws `RangeError` where an element is required. A name that destructuring binds past the end (`const [kind, column] = line.split(":")`, also in parameters and `for…of`) holds `undefined`: it goes into optional properties and `| undefined` places as `undefined` (`const agg: Agg = { kind, column }` for `column?: string`), and throws `TypeError` where its own type is required. So does a variable declared `T | undefined` that is given such a name or element (`x = list[3]`, `best = cached || items[i]`) where TypeScript then treats it as a `T`, and a parameter whose default reads an element (`p: string | undefined = s.split(":")[1]`). A destructuring default applies whenever the value is `undefined`, also where its type is exactly `undefined`, and a default of another type widens the name's type as TypeScript does (`const [a, b = "none"] = nums`). `new Array(n)` leaves holes in arrays of strings and objects: the callback methods skip them as JS's do, copies keep them, `for…of` gives `undefined` for one (checked where it is used as the element type, as a destructured name is), `find`, its kin and `Array.from`'s mapper give one to an optional or defaulted callback parameter as left out, and other typed reads of one throw `RangeError` (an element type with `| undefined` reads it as undefined). Arrays of numbers get zeros.
- `x!` throws on `null` and `undefined`, and `m.get(k)!` throws `RangeError` when the map has no entry for `k`. A downcast with `as` is checked, and so is a type guard (`x is Dog`) that is wrong about a value's class.
- A value that tsc keeps narrowed across a call that changed it (a property, or a variable another function assigns) is checked where it is read as the narrowed type: if it now holds another member of its union, the read throws `TypeError`. Tested again (`typeof`, `instanceof`, `===`, a switch, a type predicate's argument), it is read as what it holds, as in Node, and so is a second test's value that tsc types `never`.
- A property that `"k" in x` finds on an object whose type doesn't declare it is read and written by name, as in Node; writing one that a class's object has no field for throws `TypeError`, since objects can't gain properties. A branded type such as `string & { __brand: "id" }` is just its string or number.
- A method read as a value is bound to its object, so `const f = obj.method; f()` works.
- A generic function is compiled for each of its type arguments, so a branch that narrows a `T` to something its type argument can't be (`typeof x === "number"` when T is string) can't run, and is compiled to throw `TypeError`. Only a type predicate, assertion or cast that lied can reach it, and only where the branch uses the value as the narrowed type (`x + 1`, or a copy `const y = x`); printing it, comparing it or concatenating it with a string works as in Node.
- Plain code that tsc narrows to `never` by testing what kind of value something is (`typeof x === "number"` for an `x: string`, `Array.isArray(x)` for a number) is compiled the same way, where the value can't have changed since it was tested: a `const`, or a parameter or `let` that only its own function assigns. Passing it on where its own type goes (`assertNever(x)`, `return x`, `x as string`) works as in Node too. Where tsc's `never` comes from comparing values (an exhausted switch over an enum, which can hold any number, or over a union of literal kinds) or the value may have changed (a property, a variable another function assigns), it holds what it really holds, as in Node.

[examples/guide_differences.pts](examples/guide_differences.pts):

```ts
// Documented differences from Node (docs/SPEC.md §8): checks that throw where Node goes
// on with undefined or an unchecked value.
function attempt(label: string, run: () => unknown): void {
  try {
    console.log(label, "->", run());
  } catch (e) {
    const error = e as Error;
    console.log(label, "->", `${error.name}: ${error.message}`);
  }
}

// `any` is checked where it meets a typed place.
const parsed: any = JSON.parse('{"count": "seven"}');
attempt("any into number", () => {
  const count: number = parsed.count;
  return count;
});

// Objects keep the properties their type has.
const user = { name: "Ada" };
attempt("add a property", () => {
  (user as any).email = "ada@example.com";
  return user;
});

// Reading past an array's end gives undefined only where any value can go.
const scores = [10, 20, 30];
console.log("printed", scores[5]);
attempt("element past the end", () => {
  const score: number = scores[5];
  return score;
});

// `!` and casts are checked.
const ages = new Map<string, number>([["ada", 36]]);
attempt("missing map entry with !", () => ages.get("bob")!);
class Animal {}
class Dog extends Animal {
  bark(): string {
    return "woof";
  }
}
const pet: Animal = new Animal();
attempt("wrong downcast", () => (pet as Dog).bark());

// A method read as a value is bound to its object.
class Counter {
  count = 0;
  increment(): number {
    return ++this.count;
  }
}
const increment = new Counter().increment;
attempt("detached method", () => increment());
```

ParallelScript prints:

```text
any into number -> TypeError: Expected number, got string
add a property -> TypeError: Cannot add property email, object is not extensible
printed undefined
element past the end -> RangeError: Array index 5 is out of range (length 3); if it may be missing, read it with `?? fallback`, give the element type `| undefined`, or check the length first
missing map entry with ! -> RangeError: Map has no entry for the key
wrong downcast -> TypeError: Expected Dog, got Animal
detached method -> 1
```

Node prints:

```text
any into number -> seven
add a property -> { name: 'Ada', email: 'ada@example.com' }
printed undefined
element past the end -> undefined
missing map entry with ! -> undefined
wrong downcast -> TypeError: pet.bark is not a function
detached method -> TypeError: Cannot read properties of undefined (reading 'count')
```

### Other differences you may meet

[SPEC.md §8](SPEC.md#8-documented-differences-from-typescript-and-node) lists every difference. These are the ones you are most likely to run into:

- `process.argv` starts with the executable's path twice, where Node has its own path and the script's. `process.argv.slice(2)` is the arguments in both. `process.env` is a copy made at startup.
- `pts:fs` reads a file without an encoding as bytes, a `Uint8Array` where Node gives a `Buffer` (a `Uint8Array` with more methods): indexing, `length`, `subarray` and the typed array methods work, and `console.log` shows `Uint8Array(n) [ … ]` where Node shows `<Buffer …>`. A Buffer's own methods don't exist. Its text (`toString()`, `${buffer}`, `String(buffer)`) and its `slice` (a view in Node, a copy for a `Uint8Array`) are compile errors that say what to write instead: read the file with `"utf8"`, or use `subarray`. Writing takes text or a `Uint8Array` ([Reading bytes](#reading-bytes)).
- `toLocaleString` on numbers always formats as Node's default English locale does. `Date`'s `toLocaleDateString` and the other locale formats aren't supported yet, and `localeCompare` takes no locale but English.
- A `yield`, or an `await`, can't be in a loop's condition or update, a `case` label, a declaration's variables after the first, a default value or an optional chain yet: `while (await next())` is PTS1001. Await before the loop and at the end of its body instead.
- A stack frame shows the file and line without a column: `at check (/src/app.pts:2)`.
- A function can't be converted to a string, where JavaScript gives its source text.

### Left out by design

Some JavaScript features don't fit a language that is compiled ahead of time and keeps fixed object layouts. They are compile errors with code PTS1002, and the message says why:

| Left out | Use instead |
|---|---|
| `var` | `let` or `const` |
| `arguments` | a rest parameter, `...args` |
| `eval`, `new Function` | nothing: there is no compiler at run time |
| `delete` on an ordinary object | a `Map`, a `Record<string, T>`, or an optional property set to `undefined` |
| namespaces | ES modules |
| `bigint` and `10n` | the 64-bit types `int` and `u64` (BigInt "is not supported yet") |
| `Symbol`, except `Symbol.iterator` and `Symbol.asyncIterator` | string keys or class members |
| `Proxy`, `Reflect`, `__proto__`, `Object.setPrototypeOf`, assigning to `.prototype` | classes |
| declaration merging (overloads, and interfaces merging with interfaces, still work) | a single declaration |

`with` is rejected by tsc itself, because every file is a strict-mode module. [Common compile errors](#pts1002-left-out-by-design) shows the messages.

### Not supported yet

Some valid TypeScript isn't supported yet. It is a compile error with code PTS1001 that names the construct, so nothing is miscompiled silently. These are the ones TypeScript developers meet most, each with a rewrite that works today:

| Not supported yet | Rewrite |
|---|---|
| a class declared inside a function or a block | declare it at module scope |
| `for (x of xs)` with a variable declared earlier | `for (const x of xs)` |
| `map` with a callback that returns nothing | `forEach` |
| the type `Function` | a function type, such as `(...args: any[]) => unknown` |
| a `Dog[]` used as an `Animal[]` that may be changed or kept, where a copy would behave differently | declare the array as `Animal[]` |
| an object with known properties passed as a `Record<string, T>` | pass a copy, `{ ...o }` |
| a class that extends `Map`, `Date`, `RegExp` or a typed array | a class that holds one (extending `Error` works) |
| `toLocaleDateString` and `Date`'s other locale formats | `toISOString()` |
| `readFileSync(path).toString()`, a Buffer's text | `readFileSync(path, "utf8")` |
| dynamic `import()` | a static `import` |
| `debugger` | gdb or lldb on the binary |
| `await` inside the operand of `parallel` | await first, then start the task |

[Common compile errors](#pts1001-not-supported-yet) shows the messages and the rewrites. The "Known issues" section of [PROGRESS.md](../PROGRESS.md) lists the current gaps.

## Integer types

TypeScript has one number type. ParallelScript keeps `number` exactly as JavaScript has it (a 64-bit float, with `NaN`, `Infinity` and `-0`) and adds integer types and a 32-bit float:

| Type | Values |
|---|---|
| `i8`, `i16`, `i32` | signed integers of 8, 16 and 32 bits |
| `int`, also called `i64` | signed 64-bit integers |
| `u8`, `u16`, `u32`, `u64` | unsigned integers of 8, 16, 32 and 64 bits |
| `f32` | 32-bit floats |
| `f64` | another name for `number` |

They are global; there is nothing to import. To the type checker each is a number type of its own, so it can be an array element, a map key or value, a parameter or a type argument (`u8[]`, `Map<string, int>`, `(n: int) => int`). Inference keeps them: `bytes.map((b) => b * 2)` is a `u8[]`.

The rules:

- **Literals take their type from context.** In `const level: u8 = 200`, 200 is a `u8`. Without a context a literal is a `number`, as in TypeScript, so `const n = 5` is a `number`. A literal must fit its type.
- **Only lossless conversions are implicit.** Any integer type converts to `number` (an `int` beyond 2^53 rounds, as any `number` does). A smaller integer type converts to a larger one of compatible sign, such as `u8` to `int` or `i8` to `i32`, and an unsigned type converts to a wider signed one. Anything else needs `as`.
- **`as` converts the value, by Rust's rules.** From `number` (or a plain literal), it truncates toward zero, saturates at the type's limits, and turns `NaN` into 0. Between integer types it wraps, keeping the low bits.
- **Arithmetic stays in the type and wraps on overflow.** `/` is integer division and truncates, `%` takes the sign of the dividend, and dividing by zero throws `RangeError: Division by zero`. An integer `**` with a negative exponent throws `RangeError` too.
- **An integer that meets a `number` gives a `number`.** `seven * 1.5` and `seven * ratio`, with `ratio: number`, are `number`s. But an integer literal takes the other operand's type, so to divide as numbers, convert first: `(seven as number) / 2`. A literal with a decimal point can't be an integer, so `seven / 2.0` is a compile error rather than integer division.
- **Two integer types meet as the wider one.** If neither converts to the other without loss, as with `int` and `u64`, it is a compile error.
- **Bitwise operators work on the type's own bits.** `>>` is arithmetic on signed types and logical on unsigned ones, and `<<` drops the bits that overflow.
- `typeof` gives `"number"` for all of them, and `console.log` prints 64-bit values exactly.

[examples/guide_integers.pts](examples/guide_integers.pts):

```ts
// The integer types: literals take their type from context, arithmetic wraps, `/`
// truncates, and `as` converts the value.
const level: u8 = 200;
const big: int = 9007199254740993;
console.log(level, big, big + 1);

// Arithmetic stays in the type: it wraps on overflow, and `/` and `%` are integer ops.
console.log(level + 100, level * 2);
const seven: int = 7;
console.log(seven / 2, -seven / 2, -seven % 3);
const limit: i32 = 2147483647;
console.log(limit + 1);

// Mixing an integer with a fraction or a `number` gives a `number`. A literal with a
// decimal point, like 2.0, can't be an integer, so convert first to divide as numbers.
const ratio = 2.5;
console.log(seven * 1.5, seven * ratio, (seven as number) / 2, typeof seven);

// Lossless conversions are implicit; lossy ones need `as`.
const wide: i64 = level;
const asNumber: number = seven;
const price = 19.99;
console.log(wide, asNumber, price as int, -price as int);
console.log(300.5 as u8, -1 as u8, NaN as i32, Infinity as i16);
const count: i32 = 300;
console.log(count as u8, count as i8);

// Integer types work in arrays, maps and function types, and inference keeps them.
const bytes: u8[] = [250, 251, 252];
const doubled = bytes.map((b) => b * 2);
console.log(doubled);
// reduce's accumulator takes the element type unless you name another, so this u8 sum wraps.
console.log(bytes.reduce((sum, b) => sum + b, 0), bytes.reduce<int>((sum, b) => sum + b, 0));
const totals = new Map<string, int>([["apples", 3]]);
totals.set("pears", 9007199254740993);
console.log(totals);
const half: (n: int) => int = (n) => n / 2;
console.log(half(9));

// Bitwise operators work on the type's own bits.
const flags: u8 = 0b1000_0001;
console.log(flags >> 1, ~flags, flags << 1);

// f32 is a 32-bit float.
const tenth: f32 = 0.1;
console.log(tenth, tenth * 2);

// Division by zero throws instead of giving Infinity.
const zero: int = 0;
try {
  console.log(seven / zero);
} catch (e) {
  console.log((e as Error).name, (e as Error).message);
}
```

Output:

```text
200 9007199254740993 9007199254740994
44 144
3 -3 -1
-2147483648
10.5 17.5 3.5 number
200 7 19 -19
255 0 0 32767
44 44
[ 244, 246, 248 ]
241 753
Map(2) { 'apples' => 3, 'pears' => 9007199254740993 }
4
64 126 2
0.10000000149011612 0.20000000298023224
RangeError Division by zero
```

### What the compiler rejects

Lossy conversions are PTS2001, literals that don't fit are PTS2002, and operands without a common type are PTS2003. [pts/tests/errors/guide_integer_errors.pts](../pts/tests/errors/guide_integer_errors.pts):

```ts
// The integer types' rules are compile errors: lossy implicit conversions (PTS2001),
// literals that don't fit (PTS2002) and operands with no common type (PTS2003).
const price = 19.99;
const cents: int = price;
const big: i64 = 1;
const small: i32 = big;
const offset: i8 = -1;
const index: u16 = offset;
const level: u8 = 256;
const whole: int = 2.5;
const bytes: u8[] = [1, 2, -3];
const signed: i64 = 1;
const unsigned: u64 = 2;
console.log(signed + unsigned);
```

```text
guide_integer_errors.pts:4:20 - error PTS2001: a number doesn't convert to int implicitly because the value could change; write `as int` to convert it
guide_integer_errors.pts:6:20 - error PTS2001: an int doesn't convert to i32 implicitly because the value could change; write `as i32` to convert it
guide_integer_errors.pts:8:20 - error PTS2001: an i8 doesn't convert to u16 implicitly because the value could change; write `as u16` to convert it
guide_integer_errors.pts:9:19 - error PTS2002: 256 doesn't fit in u8 (0 to 255)
guide_integer_errors.pts:10:20 - error PTS2002: 2.5 is not an integer, so it can't be an int; write `as int` to convert it
guide_integer_errors.pts:11:28 - error PTS2002: -3 doesn't fit in u8 (0 to 255)
guide_integer_errors.pts:14:13 - error PTS2003: `+` can't mix int and u64, because neither converts to the other without loss; convert one with `as`
```

A literal written with a decimal point is never an integer, so `seven / 2.0` with `seven: int`, which would divide as integers and give 3, is PTS2002 too: write `seven / 2` for integer division, or `(seven as number) / 2` for 3.5. Exponents are fine: `const n: int = 1e6`.

The PTS2001 messages say what to write: `price as int` truncates 19.99 to 19, and `big as i32` wraps.

### Things to know

- Constant expressions are computed at run time, so they wrap instead of failing to compile: `const b: u8 = 200 + 56` is 0. This is a known issue.
- The literal operand of `as` is a `number` first, so `9007199254740993 as int` is 9007199254740992. Annotate instead: `const x: int = 9007199254740993` keeps every digit.
- `reduce` on an integer array accumulates in the element type unless you name another, so a `u8[]`'s sum wraps. Write `bytes.reduce<int>(…, 0)`, as above. `bytes.reduce(f, 0 as int)` is an error, because tsc then picks the overload whose accumulator is the element type.
- The elements of typed arrays are `number`s, as in JavaScript: an element of a `Uint8Array` plus 1 can be 256.
- `pts:sync`'s atomic integer holds an `int`. There is no `Atomic<i32>` yet.

## Parallelism

### The model

A ParallelScript program can use every core, through two kinds of threads that share one heap:

- **Event loops.** Each one is a libuv loop with its own timers, I/O and microtasks. Plain `async` and `await` work exactly as in Node: a task stays on its loop, and the code between two `await`s runs without interference from the loop's other tasks. `parallel` tasks are spread over the loops.
- **A fork-join pool.** It runs the data-parallel operations (`.par`, `parallel for`, `join`) with work stealing, as Rust's Rayon does. The thread that starts an operation helps run it.

`PTS_THREADS` sets the number of threads: both the number of event loops and the number of threads an operation runs on. Without it, or with anything but a positive number, it is the number of cores the process may use, and it is at most 256. Set it when you run the program, `PTS_THREADS=4 ./app`. The extra loops and pool threads start the first time the program uses them.

With `PTS_THREADS=1` everything still works: `parallel e` still makes a new task (on the same loop, run later), and `.par` runs sequentially. Every parallel sample in this guide prints the same with 1, 2 and 4 threads.

| Tool | Use it for |
|---|---|
| `parallel e` | independent jobs, async or not, such as file I/O or uneven pieces of computation |
| `xs.par.map(…)` and the other `.par` methods | one operation over every element of an array, typed array or range |
| `parallel for (const x of xs)` | a loop whose iterations can all run at once |
| `join(a, b)` | divide and conquer |
| `pts:sync` | state that threads share: atomics, locks and channels |

### Tasks: `parallel e`

`parallel e` runs `e` as a new task on another event loop (the least loaded) and returns at once with a promise of its value. If `e` is itself a promise, such as an async function's call, the promise is flattened: `parallel slowLength("x")` is a `Promise<number>`.

[examples/guide_parallel_tasks.pts](examples/guide_parallel_tasks.pts):

```ts
// `parallel e` runs e as a task on another event loop and gives a promise of its value.
function fib(n: number): number {
  return n < 2 ? n : fib(n - 1) + fib(n - 2);
}

async function slowLength(text: string): Promise<number> {
  await new Promise((resolve) => setTimeout(resolve, 5));
  return text.length;
}

function parseConfig(text: string): unknown {
  return JSON.parse(text);
}

// One task: await its promise.
console.log(await parallel fib(30));

// Many tasks at once; Promise.all keeps their order.
console.log(await Promise.all([25, 26, 27, 28].map((n) => parallel fib(n))));

// A task that is an async call: its promise is flattened.
const length: number = await parallel slowLength("parallel");
console.log(length);

// A throw in the task rejects its promise.
try {
  await parallel parseConfig("{");
} catch (e) {
  console.log("caught", (e as Error).name);
}

// Captured objects are shared: the task's writes are there once it is done.
const report = { lines: 0 };
async function countLines(text: string): Promise<void> {
  report.lines = text.split("\n").length;
}
await parallel countLines("a\nb\nc");
console.log(report);

// `await` inside the operand isn't supported yet: await first, then start the task.
const size = await slowLength("abc");
console.log(await parallel fib(size * 10));
```

Output:

```text
832040
[ 75025, 121393, 196418, 317811 ]
8
caught SyntaxError
{ lines: 3 }
832040
```

- A task shares the heap: it sees the variables it captures, and its writes to objects are there once it is done. Nothing is copied.
- A throw in the task, synchronous or not, rejects the promise. A task that nobody awaits still runs to the end, and the program waits for it.
- `parallel` binds like `await`: `parallel f(x) + 1` is `(parallel f(x)) + 1`. You can also write `parallel(e)`, unless something named `parallel` is in scope.
- Because `parallel(` starts that call form, `parallel (async () => { … })()` calls the promise `parallel (…)` returns, a type error whose hint says so. Put the whole call inside the parentheses, `parallel((async () => { … })())`, or name the function: `parallel work()`.
- `parallel` is a keyword only in `parallel e` and `parallel for`. Elsewhere it is an ordinary name, so code with a variable called `parallel` still compiles.
- The operand can't contain `await` or `yield` yet. Await first, as the last lines of the sample do, or pass an async function's call.
- The operand may be any expression, a function too: `await parallel async () => …` gives the function the task made. To run a function in a task, pass its call: `parallel work()`.

### Parallel views: `.par`

`xs.par` is a parallel view of an array. Typed arrays have one too, and so do ranges from `pts:parallel`. Its methods split the work across the pool and return when it is done. They are synchronous, so there is nothing to await.

| Method | Gives |
|---|---|
| `map(fn)` | a new array, in order |
| `filter(fn)` | the elements `fn` holds for, in order |
| `forEach(fn)` | nothing |
| `reduce(fn, initial)` | the elements folded with `fn(a, b)`, which must be associative, from `initial`: the same as `reduce` gives, for any initial value |
| `sum()`, `min()`, `max()` | the elements taken as numbers (0, Infinity and -Infinity when there are none) |
| `count(fn)` | how many elements `fn` holds for |
| `some(fn)`, `every(fn)` | a boolean; they stop early |
| `find(fn)` | *a* matching element, not necessarily the first, or `undefined` |
| `flatMap(fn)` | the results flattened one level, in order |
| `sort(compare?)`, `sortBy(key)` | the array itself, sorted in place and stably, as Array's `sort` does (a range gives a new array) |
| `toArray()` | a copy |

`.par` on an array needs no import. `range` and `join` come from `pts:parallel`.

[examples/guide_par_views.pts](examples/guide_par_views.pts):

```ts
// `.par` gives a parallel view of an array, a typed array or a range. Its methods run on
// the fork-join pool and return when the work is done, with results in element order.
import { join, range } from "pts:parallel";

interface Order {
  id: number;
  amount: number;
  country: string;
}
const countries = ["DE", "FR", "JP"];
const orders: Order[] = Array.from({ length: 10_000 }, (_, i) => ({
  id: i,
  amount: (i % 100) + 0.5,
  country: countries[i % 3],
}));

const amounts = orders.par.map((order) => order.amount);
console.log(amounts.slice(0, 4), amounts.par.sum(), amounts.par.min(), amounts.par.max());

const large = orders.par.filter((order) => order.amount > 99);
console.log(large.length, large[0]);

console.log(
  orders.par.count((order) => order.country === "JP"),
  orders.par.some((order) => order.amount > 100),
  orders.par.every((order) => order.amount > 0),
  orders.par.find((order) => order.id === 4242),
);

// reduce's function must be associative; the initial value goes in front once, as Array's reduce has it.
console.log(amounts.par.reduce((a, b) => Math.max(a, b), -Infinity));

// sort and sortBy sort in place, like Array's sort, and return the array.
const words = ["pear", "fig", "apple", "kiwi", "banana"];
console.log(words.par.sort());
console.log(words.par.sortBy((word) => word.length));
console.log(["to be", "or not"].par.flatMap((phrase) => phrase.split(" ")));

// Typed arrays and ranges.
const samples = new Float64Array([0.5, 1.5, 2.5]);
console.log(samples.par.map((x) => x * 2), range(1, 101).par.sum());
console.log(range(0, 5).par.map((i) => i * i));

// join runs two closures, perhaps at once, for divide and conquer.
function sumTo(from: number, to: number): number {
  if (to - from < 1000) {
    let sum = 0;
    for (let i = from; i < to; i++) sum += i;
    return sum;
  }
  const middle = Math.floor((from + to) / 2);
  const [left, right] = join(() => sumTo(from, middle), () => sumTo(middle, to));
  return left + right;
}
console.log(sumTo(0, 1_000_000));
```

Output:

```text
[ 0.5, 1.5, 2.5, 3.5 ] 500000 0.5 99.5
100 { id: 99, amount: 99.5, country: 'DE' }
3333 false true { id: 4242, amount: 42.5, country: 'DE' }
99.5
[ 'apple', 'banana', 'fig', 'kiwi', 'pear' ]
[ 'fig', 'kiwi', 'pear', 'apple', 'banana' ]
[ 'to', 'be', 'or', 'not' ]
Float64Array(3) [ 1, 3, 5 ] 5050
[ 0, 1, 4, 9, 16 ]
499999500000
```

- `reduce` isn't Array's `reduce`. Its function combines two elements, or two partial results, in any grouping, so the result has the element type. To fold into another type, `map` first.
- `map`, `filter` and `flatMap` give arrays, so their parallel methods are on `.par` again: `xs.par.map(f).par.sum()`. To count matches, `xs.par.count(predicate)` skips building the filtered array. Calling a view's method on an array (`xs.par.filter(f).count()`, `xs.sum()`) is tsc's "Property 'count' does not exist", with a hint saying this.
- On a typed array, `map` and `filter` give a typed array of the same kind, and `toArray()` gives a `number[]`.
- A throw in a callback is rethrown once the operation is done.
- Callbacks run on pool threads. Promise work they start (`then` handlers, `queueMicrotask`) runs on the calling loop once the operation returns. A timer, `pts:fs/promises` I/O or a server started from a callback on a pool thread throws `Error: Timers and asynchronous I/O can't be started from a parallel operation's callback yet`. Start such work in a `parallel` task instead. `fetch` works there (its request runs on the loop that started the operation), and so do `parallel`, clearing a timer and `pts:fs`'s synchronous functions.

### `parallel for`

`parallel for (const x of items) { … }` runs the body for every item on the pool. The statement finishes when every iteration has.

[examples/guide_parallel_for.pts](examples/guide_parallel_for.pts):

```ts
// `parallel for (const x of items)` runs the body for every item on the fork-join pool
// and finishes when every iteration has.
import { range } from "pts:parallel";

// Each iteration writes its own element: nothing races.
const squares = new Array<number>(8).fill(0);
parallel for (const i of range(0, 8)) {
  squares[i] = i * i;
}
console.log(squares);

// Objects' fields may change; `continue` skips to the next item.
class Photo {
  constructor(public name: string, public width: number) {}

  resize(width: number): void {
    this.width = width;
  }
}
const photos = [new Photo("beach", 4000), new Photo("icon", 64), new Photo("city", 3000)];
parallel for (const photo of photos) {
  if (photo.width <= 800) continue;
  photo.resize(800);
}
console.log(photos);

// Any iterable works: arrays, typed arrays and ranges, and sets, maps, strings and
// generators, which are copied into an array first.
const pixels = new Uint8Array([10, 20, 30]);
const brighter = new Uint8Array(pixels.length);
parallel for (const [i, value] of pixels.entries()) {
  brighter[i] = Math.min(255, value * 2);
}
console.log(brighter);

// The first error thrown stops the iterations that haven't started, and is rethrown
// once the running ones finish.
try {
  parallel for (const photo of photos) {
    if (photo.name === "icon") throw new Error(`${photo.name} is too small`);
  }
} catch (e) {
  console.log("caught", (e as Error).message);
}
```

Output:

```text
[
   0,  1,  4,  9,
  16, 25, 36, 49
]
[
  Photo { name: 'beach', width: 800 },
  Photo { name: 'icon', width: 64 },
  Photo { name: 'city', width: 800 }
]
Uint8Array(3) [ 20, 40, 60 ]
caught icon is too small
```

- Only a `for…of` loop over a declared variable can be `parallel`.
- `continue` works, also to the loop's own label. `break`, `return`, `await`, `yield` and labelled jumps out of the body are compile errors: the iterations run at the same time, so there is no "after this one" to stop at.
- The items are read once, when the loop starts.
- If an iteration throws, the iterations that haven't started are skipped, and the first error is rethrown once the running ones finish.

### Sharing state: `pts:sync`

[examples/guide_sync.pts](examples/guide_sync.pts):

```ts
// pts:sync's AtomicInt, Mutex and Once share state between threads safely.
import { AtomicInt, Mutex, Once } from "pts:sync";
import { range } from "pts:parallel";

// AtomicInt: add, load, store and compareExchange are atomic; add returns the old value.
const hits = new AtomicInt();
parallel for (const i of range(0, 1000)) {
  if (i % 3 === 0) hits.add(1);
}
console.log(hits.load(), hits.add(10), hits.load(), hits.compareExchange(344, 0), hits.load());

// Mutex.withLock: a short critical section that blocks the thread while it waits.
const wordCounts = new Mutex(new Map<string, number>());
const words = ["to", "be", "or", "not", "to", "be"];
words.par.forEach((word) => {
  wordCounts.withLock((counts) => counts.set(word, (counts.get(word) ?? 0) + 1));
});
console.log(wordCounts.withLock((counts) => [...counts.entries()].sort()));

// Mutex.lock: an async lock that doesn't block the event loop, held across awaits.
const account = new Mutex({ balance: 100 });
async function withdraw(amount: number): Promise<boolean> {
  const guard = await account.lock();
  try {
    if (guard.value.balance < amount) return false;
    await Promise.resolve();
    guard.value.balance -= amount;
    return true;
  } finally {
    guard.unlock();
  }
}
const outcomes = await Promise.all([60, 60, 30].map((amount) => parallel withdraw(amount)));
console.log(outcomes.filter((ok) => ok).length, account.withLock((a) => a.balance));

// Once: the first run() runs the function; the others wait for it, then skip it.
const setup = new Once();
const setups = new AtomicInt();
range(0, 100).par.forEach(() => {
  setup.run(() => {
    setups.add(1);
  });
});
console.log(setups.load(), setup.done);
```

Output:

```text
334 334 344 344 0
[ [ 'be', 2 ], [ 'not', 1 ], [ 'or', 1 ], [ 'to', 2 ] ]
2 10
1 true
```

- `AtomicInt`, also spelled `Atomic<int>`, holds an `int`. `load`, `store`, `add` and `compareExchange` are atomic. `add` wraps, and `add` and `compareExchange` return the value from before, as JavaScript's `Atomics` do.
- `new Mutex(value)` guards a value. `withLock(fn)` runs `fn(value)` holding the lock, and blocks the thread while it waits, so keep it short. `lock()` returns a promise of a guard (`guard.value`, `guard.unlock()`) and waits without blocking the event loop, so you can hold it across `await`s. A guard used after `unlock()` throws.
- Don't call `withLock` on a loop while a `lock()` holder on the same loop is awaiting: that deadlocks, as any blocking lock would.
- `Once.run(fn)` runs `fn` the first time. A `run` at the same time waits for it to finish. If `fn` throws, it counts as not run.

Channels pass items between tasks on any loops. [examples/guide_channel.pts](examples/guide_channel.pts):

```ts
// pts:sync's Channel<T>: a queue between tasks on any loops, with async send and recv.
import { Channel } from "pts:sync";

const jobs = new Channel<number>(4);
const results = new Channel<string>();

async function worker(): Promise<number> {
  let handled = 0;
  for await (const job of jobs) {
    await results.send(`${job} squared is ${job * job}`);
    handled++;
  }
  return handled;
}

// Three workers on other loops; send waits while 4 jobs are queued.
const workers = Promise.all([1, 2, 3].map(() => parallel worker()));
for (let job = 1; job <= 6; job++) {
  await jobs.send(job);
}
jobs.close();
const handled = await workers;
results.close();

const lines: string[] = [];
for await (const line of results) {
  lines.push(line);
}
console.log(handled.reduce((a, b) => a + b, 0), lines.sort());

// recv() gives undefined once the channel is closed and empty; a send then rejects.
console.log(await jobs.recv(), jobs.closed);
try {
  await jobs.send(7);
} catch (e) {
  console.log((e as Error).message);
}
```

Output:

```text
6 [
  '1 squared is 1',
  '2 squared is 4',
  '3 squared is 9',
  '4 squared is 16',
  '5 squared is 25',
  '6 squared is 36'
]
undefined true
Send on a closed channel
```

- `new Channel<T>(capacity)` is unbounded without a capacity. `send` waits while `capacity` items are queued; with a capacity of 0 it waits until a receiver takes the item.
- `recv()` gives the next item, or `undefined` once the channel is closed and empty, which is where `for await` stops. If your items can be `undefined`, use `for await`.
- `close()` lets receivers drain what is queued. Sends that are waiting, and later ones, reject with `Error: Send on a closed channel`.
- There is no `select` over several channels yet.

### The guardrail

A *parallel body* is code that may run on several threads at once: the operand of `parallel`, the body of a `parallel for`, and a function passed to a `.par` method or to `join`, whether written inline or passed by name. ParallelScript has one compile-time rule about it (PTS3001): **a parallel body can't assign a `let` variable or a parameter declared outside it.** That covers `=`, `+=` and the other compound assignments, `++` and `--`, and destructuring and `for…of` targets, also in functions made inside the body. The body's own locals are fine, and so are objects' fields and array elements. The rules for `parallel for` above are PTS3002.

[pts/tests/errors/guide_guardrail.pts](../pts/tests/errors/guide_guardrail.pts):

```ts
// The guardrail: a parallel body can't assign a `let` or parameter declared outside it
// (PTS3001), and nothing but `continue` or a throw can leave a `parallel for` (PTS3002).
const prices = [12, 5, 30, 8];

let total = 0;
prices.par.forEach((price) => {
  total += price;
});

function addAll(sum: number): number {
  parallel for (const price of prices) {
    sum += price;
  }
  return sum;
}

let last = 0;
const task = parallel (last = prices.length);

function firstOver(limit: number): number {
  parallel for (const price of prices) {
    if (price > limit) return price;
  }
  return -1;
}

parallel for (const price of prices) {
  if (price === 5) break;
}

parallel for (let i = 0; i < prices.length; i++) {}
```

```text
guide_guardrail.pts:7:3 - error PTS3001: `total` is declared outside this parallel body, which runs on several threads at once, so assigning it here would race; combine the results with `.par.reduce` or `.par.sum`, or use pts:sync: an `AtomicInt` for an integer count, a `Mutex` for anything else
guide_guardrail.pts:12:5 - error PTS3001: `sum` is declared outside this parallel body, which runs on several threads at once, so assigning it here would race; combine the results with `.par.reduce` or `.par.sum`, or use pts:sync: an `AtomicInt` for an integer count, a `Mutex` for anything else
guide_guardrail.pts:18:24 - error PTS3001: `last` is declared outside this parallel body, which runs on several threads at once, so assigning it here would race; combine the results with `.par.reduce` or `.par.sum`, or use pts:sync: an `AtomicInt` for an integer count, a `Mutex` for anything else
guide_guardrail.pts:22:24 - error PTS3002: `return` can't leave a `parallel for` body: its iterations run at once, so nothing can leave it but `continue` and a throw
guide_guardrail.pts:28:20 - error PTS3002: `break` can't leave a `parallel for` body: its iterations run at once, so nothing can leave it but `continue` and a throw
guide_guardrail.pts:31:1 - error PTS3002: `parallel for` takes a `for...of` loop: `parallel for (const x of items) { … }`
```

The fixes combine results instead of assigning shared variables:

| Rejected | Instead |
|---|---|
| `total += x` | `.par.sum()`, or `.par.reduce((a, b) => a + b, 0)` |
| `count++` | `.par.count(fn)`, or an `AtomicInt` |
| building a result in an outer variable | `.par.map` or `.par.filter`, or each iteration writing its own element, `out[i] = …` |
| assigning a variable from a task | returning the value from the task and awaiting it |
| `return` or `break` in a `parallel for` | `.par.some`, `.par.filter(…)[0]`, or a plain loop |
| `parallel for (let i = 0; …)` | `parallel for (const i of range(0, n))` |

[examples/guide_guardrail_fixes.pts](examples/guide_guardrail_fixes.pts):

```ts
// Rewrites of the code pts/tests/errors/guide_guardrail.pts rejects: combine results
// instead of assigning outer variables, and leave a loop with a search, not a jump.
import { AtomicInt } from "pts:sync";
import { range } from "pts:parallel";

const prices = [12, 5, 30, 8];

// A total: .par.sum, or .par.reduce with an associative function.
console.log(prices.par.sum(), prices.par.reduce((a, b) => a + b, 0));

// A counter several threads change: an AtomicInt.
const expensive = new AtomicInt();
parallel for (const price of prices) {
  if (price > 10) expensive.add(1);
}
console.log(expensive.load(), prices.par.count((price) => price > 10));

// Each iteration may write its own element, and objects' fields may change.
const withTax = new Array<number>(prices.length).fill(0);
parallel for (const i of range(0, prices.length)) {
  withTax[i] = prices[i] * 1.25;
}
console.log(withTax, prices.par.map((price) => price * 1.25));

// A task's result is its promise's value: await it instead of assigning from inside.
const last = await parallel prices.length;
console.log(last);

// Searching instead of `return` or `break`: some and every stop early; filter keeps order.
console.log(prices.par.some((price) => price > 25), prices.par.filter((price) => price > 10)[0]);

// The call, not the function, is the task.
async function countPrices(): Promise<number> {
  return prices.length;
}
console.log(await parallel countPrices());
```

Output:

```text
55 55
2 2
[ 15, 6.25, 37.5, 10 ] [ 15, 6.25, 37.5, 10 ]
4
true 12
4
```

What the guardrail doesn't check: the rule is lexical, so it doesn't follow calls (a function called from the body that assigns an outer variable isn't reported), and writes to fields and elements are allowed even when two threads make them at once. Such races can't crash the program: the runtime guarantees that a program with data races may see stale or lost values, but never crashes, corrupts memory or reads out of bounds. They can leave the data wrong, though. A `Map`, `Set` or array that several threads change at once can lose entries, hold a key twice, report a `size` that differs from what iterating finds, or show an element pushed once twice (SPEC §4.1), so share it through a `Mutex`, as the `pts:sync` sample does, or build it with `.par.map`, `.par.filter` or `.par.reduce`.

### Performance

[BENCHMARKS.md](BENCHMARKS.md) has the numbers, losses included. On 8 threads, mandelbrot runs 6.09 times as fast as on one (M6's run). Code that allocates a lot stops gaining past about 4 threads, because the garbage collector refills each thread's free lists under one lock and stops every thread to collect, and code that does little but allocate can get much slower beyond that: `.par.map((s) => s.split(" ").length)` over 300,000 short strings takes 93 ms on one thread, 61 ms on two, 85 ms on four and about 500 ms on eight, where a plain `map` takes 100 ms. For such programs, set `PTS_THREADS=4` (or 2), or do more work per element. A binary starts in about a millisecond, where Node takes 74 ms.

[The examples' README](../examples/README.md) has the times of whole programs: the PNG renderer runs 6.8 times as fast on eight threads as on one, and the log analyzer, which allocates about ten objects per line, stops gaining past four.

## The `pts:` modules

| Module | Contents |
|---|---|
| `pts:fs` | Node's synchronous file functions (`readFileSync`, `writeFileSync`, `appendFileSync`, `existsSync`, `statSync`, `mkdirSync`, `readdirSync`, `rmSync`, `renameSync`, `copyFileSync`, `mkdtempSync` and more), with Node's errors; text in UTF-8, or bytes as a `Uint8Array` |
| `pts:fs/promises` | the promise versions (`readFile`, `writeFile`, `readdir`, `rm` and more), also `fs.promises` |
| `pts:path` | Node's POSIX `path` |
| `pts:test`, `pts:assert` | tests in the style of Node's `node:test`, and Node's `node:assert/strict` ([Testing](#testing)) |
| `pts:process` | `argv`, `env`, `exit`, `exitCode`, `cwd`, `platform`, `arch`, `pid`, `execPath`, `stdout.write`, `stderr.write`; the default export is the global `process` |
| `pts:parallel` | `range`, `join` and the classes of the `.par` views |
| `pts:sync` | `AtomicInt`, `Atomic<int>`, `Mutex`, `Once` and `Channel` |
| `pts:http` | `serve`, an HTTP/1.1 server in the style of Bun's, and the `Request`, `Response` and `Headers` classes, which the global `fetch` uses too |

### Serving HTTP: `pts:http`

`pts:http` serves HTTP/1.1 the way Bun's `serve` does, with the Fetch standard's `Request`, `Response` and `Headers` (SPEC §7.3):

```ts
import { serve, Response } from "pts:http";

const server = serve({
  port: 0, // any free port; a real server names one, such as 3000
  hostname: "127.0.0.1",
  async fetch(request) {
    if (request.method === "POST") {
      return Response.json({ received: await request.json() });
    }
    return new Response(`hello from ${request.url}`);
  },
});
console.log(server.url.startsWith("http://127.0.0.1:"));
server.stop(); // a real server keeps serving; this sample stops so that it ends
```

It prints `true` (`docs/examples/guide_http.pts`).

With `reusePort: true` the server accepts connections on every event loop (one per core, or `PTS_THREADS`), each loop with its own socket on the same port, so requests are handled in parallel. The handler then runs on several threads at once, as a `.par` callback does, but the guardrail doesn't check it: count with an `AtomicInt`, not a `let`.

```ts
import { serve, Response } from "pts:http";
import { AtomicInt } from "pts:sync";

const served = new AtomicInt();
const server = serve({
  port: 0,
  hostname: "127.0.0.1",
  reusePort: true, // a listener on every loop
  fetch() {
    const n = served.add(1) + 1; // `count++` on a shared `let` would lose counts
    return new Response(`request ${n}`);
  },
});
console.log(server.port > 0, served.load());
server.stop();
```

It prints `true 0` (`docs/examples/guide_http_every_loop.pts`).

A handler that waits a long time can watch `request.signal`, which aborts if the client leaves before the answer; the server closes that connection at once either way. `server.stop()` lets requests in flight finish; `server.stop(true)` closes their connections too, as Bun's does. On the development machine, with the load generator on the same machine, hello world runs at about 57,000 requests a second on one loop and 85,000 on four, against 21,000 for Node and 69,000 for Go ([BENCHMARKS.md](BENCHMARKS.md#m62b-http-servers)).

Bodies are text, read whole (up to 64 MB): the server has no TLS (HTTPS), no streaming and no WebSocket yet. [examples/api-server](../examples/api-server/main.pts) is a whole JSON API built on `serve`: shared state behind a `Mutex`, atomic counters, `request.signal`, and JSON errors through `serve`'s `error` option.

### `fetch`

`fetch` is a global, as in Node, and so are `Request`, `Response`, `Headers`, `AbortController` and `AbortSignal` (with `AbortSignal.timeout` and `AbortSignal.any`). A response with a status that isn't 2xx still fulfils the promise. A failure rejects it with Node's `TypeError: fetch failed`, whose `cause` says what went wrong (`connect ECONNREFUSED 127.0.0.1:8080`, `getaddrinfo ENOTFOUND host`), and an abort rejects it with the signal's reason. Redirects are followed as the Fetch standard says, and `redirect: "manual"` and `"error"` work too. Controllers and signals work on every thread: each `.par` callback, `parallel` task or handler can make its own, and one signal can be shared too, so a single `abort()` cancels fetches running on several threads at once.

[examples/guide_fetch.pts](examples/guide_fetch.pts) fetches from a server of its own, so it runs anywhere:

```ts
// fetch is a global, as in Node, with Request, Response, Headers and AbortController.
// The sample fetches from a pts:http server of its own, so that it runs anywhere.
import { serve } from "pts:http";

const server = serve({
  port: 0,
  hostname: "127.0.0.1",
  async fetch(request) {
    if (request.url.endsWith("/slow")) {
      // Waits until the client leaves, which aborts request.signal.
      await new Promise<void>((resolve) => request.signal.addEventListener("abort", () => resolve()));
      return new Response("nobody reads this");
    }
    if (request.method === "POST") {
      const order = await request.json();
      return Response.json({ item: order.item, total: order.count * 2.5 }, { status: 201 });
    }
    return new Response("no such page", { status: 404 });
  },
});
const base = `http://127.0.0.1:${server.port}`;

// JSON in and out; the status and the headers are the response's.
const created = await fetch(`${base}/orders`, {
  method: "POST",
  headers: { "content-type": "application/json" },
  body: JSON.stringify({ item: "tea", count: 4 }),
});
console.log(created.status, created.headers.get("content-type"), await created.json());

// A 404 fulfils the promise too: check `ok` or `status`.
const missing = await fetch(`${base}/menu`);
console.log(missing.ok, missing.status, await missing.text());

// An AbortController cancels a request: fetch rejects with an AbortError.
const controller = new AbortController();
setTimeout(() => controller.abort(), 20);
try {
  await fetch(`${base}/slow`, { signal: controller.signal });
} catch (e) {
  console.log((e as Error).name, (e as Error).message);
}

// Failures reject with Node's TypeError, whose cause says why.
try {
  await fetch("ftp://example.com/");
} catch (e) {
  console.log((e as Error).message, "|", ((e as Error).cause as Error).message);
}

server.stop();
```

Output:

```text
201 application/json { item: 'tea', total: 10 }
false 404 no such page
AbortError This operation was aborted
fetch failed | unknown scheme
```

- `fetch` works on any event loop, in a `parallel` task and in a `.par` callback, whose request runs on the loop that started the operation. A request in flight keeps the program running.
- `https://` works over TLS 1.2 and 1.3, and the server's certificate is always checked: its chain against the system's certificate authorities (the CA bundle in `/etc/ssl/certs/ca-certificates.crt` or wherever the distribution keeps it), and its names against the URL's host. A certificate that doesn't check out rejects with Node's cause and code (`unable to get local issuer certificate`, `certificate has expired`, `ERR_TLS_CERT_ALTNAME_INVALID`, …); there is no way to turn the check off. `NODE_EXTRA_CA_CERTS=ca.pem` adds certificate authorities, as in Node, which suits a development server with a certificate of its own; `SSL_CERT_FILE` and `SSL_CERT_DIR` replace the system's, as with OpenSSL.
- Bodies are text. A request's `body` is a string, and a response's is read whole (up to 64 MB) and decoded as UTF-8 by `text()` or `json()`: there is no `arrayBuffer()` and no streaming.
- Each request opens a connection of its own, without keep-alive.
- There is no `URL` class yet, so the sample tests the request's URL with string methods.

[SPEC §7.1](SPEC.md#71-globals) has the details, and the "Known issues" section of [PROGRESS.md](../PROGRESS.md) the smaller gaps.

### Node's modules: `pts:fs`, `pts:path` and `pts:process`

`pts:fs`, `pts:path` and `pts:process` behave as Node's `node:fs`, `node:path` and `node:process`. Change `node:` to `pts:` in the imports and the program prints what it printed under Node, apart from the [differences](#other-differences-you-may-meet) such as `process.argv`. Importing `node:fs` itself is a tsc error ([below](#tscs-errors)). `pts:fs` has no streams, file descriptors (`openSync`, `FileHandle`) or watchers yet.

[examples/guide_node_modules.pts](examples/guide_node_modules.pts):

```ts
// pts:fs, pts:path and pts:process behave as Node's node:fs, node:path and node:process.
import { existsSync, mkdtempSync, readdirSync, readFileSync, rmSync, writeFileSync } from "pts:fs";
import { readFile } from "pts:fs/promises";
import path from "pts:path";
import { argv, platform } from "pts:process";

const dir = mkdtempSync("/tmp/guide-");
const notes = path.join(dir, "notes.txt");
writeFileSync(notes, "first\nsecond\n");
writeFileSync(path.join(dir, "todo.md"), "- write the guide\n");

console.log(readdirSync(dir), readFileSync(notes, "utf8").split("\n").length);
console.log(JSON.stringify(await readFile(notes, "utf8")));
console.log(path.basename(notes), path.extname(notes), path.relative(dir, notes));
console.log(path.join("src", "../lib", "main.pts"), path.parse("/home/ada/report.tar.gz"));

try {
  readFileSync(path.join(dir, "missing.txt"), "utf8");
} catch (e) {
  console.log((e as NodeJS.ErrnoException).code);
}

rmSync(dir, { recursive: true });
console.log(existsSync(dir));

// The global `process` is pts:process's default export; argv.slice(2) is the arguments.
console.log(argv.slice(2), platform, process.exitCode);
```

It prints this, as Node does with the `node:` modules:

```text
[ 'notes.txt', 'todo.md' ] 3
"first\nsecond\n"
notes.txt .txt notes.txt
lib/main.pts {
  root: '/',
  dir: '/home/ada',
  base: 'report.tar.gz',
  ext: '.gz',
  name: 'report.tar'
}
ENOENT
false
[] linux undefined
```

### Reading bytes

Without an encoding, `readFileSync` and `readFile` give the file's bytes as a `Uint8Array`, where Node gives a `Buffer`, which is a `Uint8Array` too. `writeFileSync`, `appendFileSync` and their promise versions take a string or a `Uint8Array`. Code that indexes the bytes, reads `length`, takes `subarray`s or passes them on as a `Uint8Array` works unchanged. What a `Buffer` does differently is a compile error ([above](#other-differences-you-may-meet)).

[examples/guide_bytes.pts](examples/guide_bytes.pts):

```ts
// Without an encoding, pts:fs reads a file as its bytes: a Uint8Array, where Node gives a
// Buffer (a Uint8Array with more methods). Writing takes text or a Uint8Array.
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from "pts:fs";
import path from "pts:path";

const dir = mkdtempSync("/tmp/guide-");
const file = path.join(dir, "logo.png");
writeFileSync(file, new Uint8Array([137, 80, 78, 71, 13, 10, 26, 10]));

const bytes = readFileSync(file);
console.log(bytes instanceof Uint8Array, bytes.length, bytes[1], bytes.join(" "));
console.log(Array.from(bytes.subarray(1, 4)));

// For the text, read with "utf8": `bytes.toString()` and `${bytes}` are compile errors.
writeFileSync(file, "héllo");
console.log(readFileSync(file).length, readFileSync(file, "utf8"));

rmSync(dir, { recursive: true });
```

It prints this, as Node does with `node:fs`:

```text
true 8 80 137 80 78 71 13 10 26 10
[ 80, 78, 71 ]
6 héllo
```

[examples/hasher](../examples/hasher/main.pts) hashes files read this way, and [examples/mandelbrot](../examples/mandelbrot/main.pts) writes a PNG from a `Uint8Array`.

## Testing

`pts test` runs tests written as Node's built-in test runner has them. `pts:test` has `test` (also `it`), `describe` (also `suite`), the hooks `before`, `after`, `beforeEach` and `afterEach`, and `skip` and `todo` as options: `test("name", { skip: "why" }, fn)`. `pts:assert` is Node's `node:assert/strict`: `equal`, `notEqual`, `deepEqual`, `notDeepEqual`, `ok`, `throws`, `rejects`, `match`, `doesNotMatch` and `fail`, as named exports or as methods of its default export, with Node's messages and an `AssertionError` that has Node's `code`, `actual`, `expected` and `operator`.

[examples/testing/stats.test.pts](examples/testing/stats.test.pts), which tests [stats.pts](examples/testing/stats.pts):

```ts
// Tests in the style of Node's node:test, with pts:assert for node:assert/strict.
import { describe, it, test } from "pts:test";
import assert from "pts:assert";
import { mean, median } from "./stats";

describe("mean", () => {
  it("averages the values", () => {
    assert.equal(mean([1, 2, 3, 4]), 2.5);
  });
  it("throws for no values", () => {
    assert.throws(() => mean([]), { name: "RangeError", message: /no values/ });
  });
});

test("the median of an even count is the mean of the middle two", () => {
  assert.equal(median([4, 1, 3, 2]), 2.5);
});

test("median leaves its input as it was", async () => {
  const values = [3, 1, 2];
  median(values);
  assert.deepEqual(values, [3, 1, 2]);
});
```

```sh
pts-bin/pts test docs/examples/testing
```

```text
▶ mean
  ✔ averages the values (0.956209ms)
  ✔ throws for no values (0.063986ms)
✔ mean (1.062088ms)
✔ the median of an even count is the mean of the middle two (0.006348ms)
✔ median leaves its input as it was (0.014621ms)
ℹ tests 4
ℹ suites 1
ℹ pass 4
ℹ fail 0
ℹ cancelled 0
ℹ skipped 0
ℹ todo 0
ℹ duration_ms 3603.46
```

- `pts test` takes files and directories (by default the working directory). In a directory it finds, at any depth, the files named `name.test.pts`, `name-test.pts`, `name_test.pts`, `test-name.pts` and `test.pts`, leaving out `node_modules` and dot directories. It builds them a few at a time and runs them one after another. Most of the duration is the build.
- A failed test is listed again after the totals, where it was registered (`test at stats.test.pts:7`) and with its error, and the exit code is 1. So is a file that doesn't compile, or that ends before its tests report, for example through an error thrown at its top level.
- A test file is an ordinary program: `pts run stats.test.pts` prints the same results and totals.
- Register tests from the file's own code on the main thread. `test`, `describe` and the hooks throw an `Error` in a `parallel` task or a `.par` or `parallel for` callback (until its first `await`): compute in parallel, then register the tests.
- Tests run one at a time, in order, after the file's top-level code. A test function may be `async`; it gets a context whose `skip()` and `todo()` mark it while it runs. There are no subtests (`t.test`), `only`, mocks or snapshots, and `test.skip(…)` is written `test(…, { skip: true }, …)`, because a function can't have properties in ParallelScript.
- `pts:assert`'s default export has no call form: write `assert.ok(x)` for `assert(x)`. `throws` and `rejects` take a RegExp, a function or an object of the error's properties, but not a class: write `(e) => e instanceof TypeError` for `TypeError`. A message that shows two objects lays them out as `console.log` does, where Node puts each property on a line.

## Common compile errors

Errors print one per line, as `file:line:col - error CODE: message`, and stop the build. Some of tsc's errors that ParallelScript's features lead to have a hint on the next line, saying what to write instead; the editor shows it as information at the same place. They come in rounds: syntax errors first, then tsc's type errors, then ParallelScript's own rules, then what isn't supported yet. Fixing one round can bring up the next.

| Code | Meaning |
|---|---|
| `TS…` | tsc's own errors |
| PTS1001 | not supported yet |
| PTS1002 | left out by design |
| PTS2001, PTS2002, PTS2003 | the integer types' rules ([Integer types](#what-the-compiler-rejects)) |
| PTS3001 | the guardrail ([The guardrail](#the-guardrail)) |
| PTS3002 | what a `parallel for` can't do ([The guardrail](#the-guardrail)) |

### tsc's errors

ParallelScript runs TypeScript's checker in strict mode, and a type error stops the build. Node, which only strips the types, would run the program anyway. [pts/tests/errors/guide_tsc_errors.pts](../pts/tests/errors/guide_tsc_errors.pts):

```ts
// tsc's own errors stop the build: ParallelScript type-checks (strict) where Node only
// strips the types. Node's modules are the pts: modules here.
import { readFileSync } from "node:fs";

let total = 0;
total = "none";
const user = { name: "Ada" };
user.email = "ada@example.com";
function greet(name: string): string {
  return "Hello, " + name;
}
greet(42);
```

```text
guide_tsc_errors.pts:3:30 - error TS2591: Cannot find name 'node:fs'. Do you need to install type definitions for node? Try `npm i --save-dev @types/node` and then add 'node' to the types field in your tsconfig.
guide_tsc_errors.pts:6:1 - error TS2322: Type 'string' is not assignable to type 'number'.
guide_tsc_errors.pts:8:6 - error TS2339: Property 'email' does not exist on type '{ name: string; }'.
guide_tsc_errors.pts:12:7 - error TS2345: Argument of type 'number' is not assignable to parameter of type 'string'.
```

The fixes:

- Import Node's modules as `pts:` modules: `import { readFileSync } from "pts:fs"`.
- Give a variable one type: `total` is a `number`, so keep strings in another variable.
- Declare the properties an object can have, such as `{ name: string; email?: string }`, or keep extra keys in a `Map`.
- Pass the argument type the function declares.

### PTS1002: left out by design

[pts/tests/errors/guide_by_design.pts](../pts/tests/errors/guide_by_design.pts):

```ts
// Features left out by design (docs/SPEC.md §3.10) are PTS1002 errors that say why.
var count = 0;
function sum(): number {
  let total = 0;
  for (const n of arguments) total += n;
  return total;
}
namespace Shapes {
  export const unit = 1;
}
const big = 2n ** 64n;
const id = Symbol("id");
console.log(eval("1 + 1"));
```

```text
guide_by_design.pts:2:1 - error PTS1002: `var` is not supported; use `let` or `const`
guide_by_design.pts:5:19 - error PTS1002: `arguments` is not supported; use a rest parameter (`...args`)
guide_by_design.pts:8:1 - error PTS1002: namespaces are not supported; use ES modules (`import` and `export`)
guide_by_design.pts:11:13 - error PTS1002: BigInt is not supported yet
guide_by_design.pts:11:19 - error PTS1002: BigInt is not supported yet
guide_by_design.pts:12:12 - error PTS1002: `Symbol` is not supported, except `Symbol.iterator` and `Symbol.asyncIterator`
guide_by_design.pts:13:13 - error PTS1002: `eval` is not supported: programs are compiled ahead of time, so there is no compiler at run time
```

`delete` on an ordinary object is PTS1002 too, but it is reported in the last round, with the PTS1001 errors. [pts/tests/errors/guide_fixed_shape.pts](../pts/tests/errors/guide_fixed_shape.pts):

```ts
// Objects keep the shape their type gives them, so `delete` works only on hash-map
// objects (`Record<string, T>` and index signatures).
interface Settings {
  theme: string;
  debug?: boolean;
}
const settings: Settings = { theme: "dark", debug: true };
delete settings.debug;
console.log(settings);
```

```text
guide_fixed_shape.pts:8:1 - error PTS1002: `delete` works only on hash-map objects (`Record<string, T>` and index signatures): other objects have fixed layouts and arrays have no holes
```

`with` gets tsc's own error, `with_statement.pts:3:1 - error TS1101: 'with' statements are not allowed in strict mode.` ([with_statement.pts](../pts/tests/errors/with_statement.pts)).

The rewrites are in the [table above](#left-out-by-design), and the last part of [guide_rewrites.pts](examples/guide_rewrites.pts), below, shows them.

### PTS1001: not supported yet

[pts/tests/errors/guide_not_yet.pts](../pts/tests/errors/guide_not_yet.pts):

```ts
// Valid TypeScript that ParallelScript can't compile yet is a PTS1001 error naming the
// construct. docs/GUIDE.md shows a rewrite of each that works today.
import { readFileSync } from "pts:fs";

function main(): void {
  class Point {
    x = 0;
  }
}

const names = ["ada", "grace"];
let name = "";
for (name of names) {
  console.log(name);
}
names.map((n) => {
  console.log(n);
});

class Animal {
  constructor(readonly name: string) {}
}
class Dog extends Animal {
  bark(): string {
    return "woof";
  }
}
function adopt(shelter: Animal[], animal: Animal): void {
  shelter.push(animal);
}
const dogs = [new Dog("rex")];
adopt(dogs, new Animal("tom"));
console.log(dogs.length);

function total(counts: Record<string, number>): number {
  return Object.values(counts).reduce((a, b) => a + b, 0);
}
const fruit = { apples: 3, pears: 2 };
console.log(total(fruit));

class Registry extends Map<string, number> {}

console.log(new Date(0).toLocaleDateString());

const host = readFileSync("/etc/hostname").toString();

async function double(n: number): Promise<number> {
  return n * 2;
}
async function run(): Promise<number> {
  return await parallel double(await double(1));
}

debugger;
```

```text
guide_not_yet.pts:41:24 - error PTS1001: extending `Map` is not supported yet
guide_not_yet.pts:13:6 - error PTS1001: `for...of` without a declaration of its variable is not supported yet
guide_not_yet.pts:16:1 - error PTS1001: `map` with a callback that returns nothing is not supported yet
guide_not_yet.pts:32:7 - error PTS1001: using a Dog[] as a Animal[] where it may be changed or kept (a copy wouldn't show the changes; a new array, or one passed to a parameter that is only read, is copied) is not supported yet
guide_not_yet.pts:39:19 - error PTS1001: using a `{apples: number; pears: number}` as a hash-map object (`Record<string, number>`) is not supported yet; a copy of its properties, `{ ...o }`, is one
guide_not_yet.pts:43:13 - error PTS1001: the Date method `toLocaleDateString` is not supported yet
guide_not_yet.pts:45:14 - error PTS1001: a Buffer's text (`toString` of a file's bytes, which Node decodes as UTF-8; read the file with an encoding, "utf8") is not supported yet
guide_not_yet.pts:54:1 - error PTS1001: debugger statement is not supported yet
guide_not_yet.pts:6:3 - error PTS1001: classes inside functions or blocks are not supported yet
guide_not_yet.pts:51:32 - error PTS1001: `await` and `yield` in the operand of `parallel` are not supported yet
```

These rewrites compile today. [examples/guide_rewrites.pts](examples/guide_rewrites.pts):

```ts
// Rewrites that compile today of the code in pts/tests/errors/guide_not_yet.pts,
// guide_by_design.pts and guide_fixed_shape.pts.

// A class at module scope instead of inside a function.
class Point {
  x = 0;
}
function origin(): Point {
  return new Point();
}

// A declared loop variable, and forEach for a callback that returns nothing.
const names = ["ada", "grace"];
for (const name of names) {
  console.log(name);
}
names.forEach((n) => {
  console.log(n.toUpperCase());
});

// An array declared with the element type it is used as.
class Animal {
  constructor(readonly name: string) {}
}
class Dog extends Animal {
  bark(): string {
    return "woof";
  }
}
function adopt(shelter: Animal[], animal: Animal): void {
  shelter.push(animal);
}
const shelter: Animal[] = [new Dog("rex")];
adopt(shelter, new Animal("tom"));
console.log(shelter.length);

// A copy of an object's properties is a hash-map object.
function total(counts: Record<string, number>): number {
  return Object.values(counts).reduce((a, b) => a + b, 0);
}
const fruit = { apples: 3, pears: 2 };
console.log(total({ ...fruit }));

// A class that holds a Map instead of extending it.
class Registry {
  private readonly entries = new Map<string, number>();

  register(name: string, id: number): void {
    this.entries.set(name, id);
  }

  get size(): number {
    return this.entries.size;
  }
}
const registry = new Registry();
registry.register("main", 1);
console.log(registry.size, origin());

// ISO dates instead of the locale formats.
console.log(new Date(0).toISOString().slice(0, 10));

// let instead of var, a rest parameter instead of arguments.
let count = 0;
function sum(...numbers: number[]): number {
  let total = 0;
  for (const n of numbers) total += n;
  return total;
}
count = sum(1, 2, 3);
console.log(count);

// An optional property set to undefined, and a Record or a Map for keys that come and go.
interface Settings {
  theme: string;
  debug?: boolean;
}
const settings: Settings = { theme: "dark", debug: true };
settings.debug = undefined;
console.log(settings);
const flags: Record<string, boolean> = { debug: true, verbose: false };
delete flags.debug;
const sessions = new Map<string, number>([["ada", 1]]);
sessions.delete("ada");
console.log(flags, sessions);
```

It prints this, as Node does:

```text
ada
grace
ADA
GRACE
2
5
1 Point { x: 0 }
1970-01-01
6
{ theme: 'dark', debug: undefined }
{ verbose: false } Map(0) {}
```

The rewrite of `await` inside `parallel` is the last part of the [tasks sample](#tasks-parallel-e): `const size = await slowLength("abc");` first, then `parallel fib(size * 10)`.

## Where to go next

- [SPEC.md](SPEC.md): the language reference, with every documented difference from Node in §8.
- [DECISIONS.md](DECISIONS.md): why each choice was made.
- [PROGRESS.md](../PROGRESS.md): what works today and the known issues.
- [BENCHMARKS.md](BENCHMARKS.md): the performance numbers.
- [docs/examples/](examples/): the samples from this guide, and from the other docs.
- [examples/](../examples/README.md): four whole programs (a file hasher, a PNG renderer, a JSON API server and a log analyzer) with their numbers.
