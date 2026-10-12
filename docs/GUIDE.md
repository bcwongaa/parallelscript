# ParallelScript for TypeScript developers

ParallelScript is TypeScript compiled ahead of time to a native binary. The compiler, `pts`, checks your program with TypeScript 7's own type checker, turns it into C and builds it with clang. The binary has a garbage collector and can use every core: next to `async` and `await`, there are `parallel` tasks, parallel array operations and `parallel for` loops.

This guide is for people who already write TypeScript. It covers what carries over, what doesn't, and what is new. The language reference is [SPEC.md](SPEC.md), and the reasons behind each choice are in [DECISIONS.md](DECISIONS.md).

Every program in this guide is a file in [examples/](examples/), with its expected output at the end. The test suite runs each one, and runs the plain TypeScript ones under Node too. The programs that show compile errors are in [pts/tests/errors/](../pts/tests/errors/), each with the exact errors it must produce.

## Contents

1. [Getting started](#getting-started)
2. [What stays the same](#what-stays-the-same)
3. [What's different](#whats-different)
4. [Integer types](#integer-types)
5. [Parallelism](#parallelism)
6. [The `pts:` modules](#the-pts-modules)
7. [Common compile errors](#common-compile-errors)
8. [Where to go next](#where-to-go-next)

## Getting started

Build the compiler once, from the repository root, with `make build`. It lands at `pts-bin/pts`.

| Command | What it does |
|---|---|
| `pts run app.pts [args…]` | Compiles and runs the program. Arguments after the file go to the program. |
| `pts build app.pts -o app` | Compiles to a native executable. Without `-o`, the executable is named after the file. |
| `pts emit-c app.pts` | Prints the generated C, whose `#line` directives point at your `.pts` lines (gdb and lldb show those lines too). |

```sh
pts-bin/pts run docs/examples/hello.pts
```

That prints `Hello, ParallelScript`. The first build also compiles the runtime and its C libraries (the garbage collector, libuv, PCRE2 and a few more) and caches them under `~/.cache/pts/`, so later builds are faster. The executable needs nothing but the C library.

`pts` exits with 0 on success, 1 on compile errors and 2 on a malformed command line. `pts run` exits with the program's own exit code.

There is no `tsconfig.json`: the options are fixed.

- TypeScript runs in strict mode.
- Every file is an ES module. Top-level `await` works in the file you run, though not yet in the modules it imports.
- The standard library is ECMAScript's, without the DOM: `document` is "Cannot find name 'document'".
- Source files end in `.pts`. `import "./util"` finds `util.pts` first, then the usual TypeScript extensions.
- Imports are relative files or the standard library's `pts:` modules. There is no npm or `node_modules` yet.

## What stays the same

Most TypeScript compiles unchanged. That includes:

- classes with single inheritance, fields, getters and setters, static members, `private` and `#private`, abstract classes and `instanceof`;
- interfaces and object types (structural, as tsc checks them), object literals, spread and rest, destructuring, optional chaining and `??`;
- unions with every kind of narrowing, generics, enums and tuples;
- closures, and default and rest parameters;
- `async` and `await`, promises (with `all`, `allSettled`, `race`, `any` and `withResolvers`), generators, async generators and `for await`;
- `throw`, `try`, `catch` and `finally`, with `Error` and its subclasses, whose `stack` points at `.pts` lines;
- `Map`, `Set`, `JSON`, `Math`, `RegExp`, typed arrays, basic `Date`, timers and `queueMicrotask`.

Node is the reference for behaviour. A program that uses only plain TypeScript must print exactly what Node prints, unless [SPEC.md §8](SPEC.md#8-documented-differences-from-typescript-and-node) documents a difference. `console.log` formats values as Node's `util.inspect` does: the same quotes, line breaks, `[Array]` past depth 2, `Map(2) {…}` and class names. Numbers print as JavaScript prints them, so `0.1 + 0.2` is `0.30000000000000004`.

The tests hold the compiler to this. They compare 300 ordinary TypeScript programs with Node: 253 print exactly the same, and each of the other 47 says why it differs.

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
- Arrays have no holes. Reading past the end gives `undefined` where any value can go (printing, building a string, comparing with `undefined`), but throws `RangeError` where an element is required.
- `x!` throws on `null` and `undefined`, and `m.get(k)!` throws `RangeError` when the map has no entry for `k`. A downcast with `as` is checked.
- A method read as a value is bound to its object, so `const f = obj.method; f()` works.

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

// Arrays have no holes: a missing element is undefined only where any value can go.
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
element past the end -> RangeError: Array index 5 is out of range (length 3)
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
- `pts:fs` reads and writes text only. Reading a file without an encoding, which gives a `Buffer` in Node, is a compile error. There is no `Buffer`, but typed arrays work.
- `toLocaleString` on numbers always formats as Node's default English locale does. `Date`'s `toLocaleDateString` and the other locale formats aren't supported yet, and `localeCompare` takes no locale but English.
- In a generator, `yield` must come first in its statement: as the statement itself, a variable's initializer, the right side of an assignment or a returned value.
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
| a `Dog[]` used as an `Animal[]` that may be changed or kept, where a copy would behave differently | declare the array as `Animal[]` |
| an object with known properties passed as a `Record<string, T>` | pass a copy, `{ ...o }` |
| a class that extends `Map`, `Date`, `RegExp` or a typed array | a class that holds one (extending `Error` works) |
| `toLocaleDateString` and `Date`'s other locale formats | `toISOString()` |
| `readFileSync(path)` without an encoding | `readFileSync(path, "utf8")` |
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
- **An integer that meets a `number` gives a `number`.** `seven * 1.5` and `seven * ratio`, with `ratio: number`, are `number`s. But an integer literal takes the other operand's type, and `2.0` is an integer literal, so `seven / 2.0` divides as integers. Convert first: `(seven as number) / 2`.
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

// Mixing an integer with a fraction or a `number` gives a `number`. A literal like 2.0
// is the integer 2, so convert first to divide as numbers.
const ratio = 2.5;
console.log(seven * 1.5, seven * ratio, seven / 2.0, (seven as number) / 2, typeof seven);

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
10.5 17.5 3 3.5 number
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
guide_integer_errors.pts:6:20 - error PTS2001: a int doesn't convert to i32 implicitly because the value could change; write `as i32` to convert it
guide_integer_errors.pts:8:20 - error PTS2001: a i8 doesn't convert to u16 implicitly because the value could change; write `as u16` to convert it
guide_integer_errors.pts:9:19 - error PTS2002: 256 doesn't fit in u8 (0 to 255)
guide_integer_errors.pts:10:20 - error PTS2002: 2.5 is not an integer, so it can't be a int; write `as int` to convert it
guide_integer_errors.pts:11:28 - error PTS2002: -3 doesn't fit in u8 (0 to 255)
guide_integer_errors.pts:14:13 - error PTS2003: `+` can't mix int and u64, because neither converts to the other without loss; convert one with `as`
```

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
- `parallel` is a keyword only in `parallel e` and `parallel for`. Elsewhere it is an ordinary name, so code with a variable called `parallel` still compiles.
- The operand can't contain `await` or `yield` yet. Await first, as the last lines of the sample do, or pass an async function's call.
- The operand can't be a function, as in `parallel async () => …`: the task would only make the function. Pass the call instead.

### Parallel views: `.par`

`xs.par` is a parallel view of an array. Typed arrays have one too, and so do ranges from `pts:parallel`. Its methods split the work across the pool and return when it is done. They are synchronous, so there is nothing to await.

| Method | Gives |
|---|---|
| `map(fn)` | a new array, in order |
| `filter(fn)` | the elements `fn` holds for, in order |
| `forEach(fn)` | nothing |
| `reduce(fn, initial)` | the elements folded with `fn(a, b)`, which must be associative, starting from `initial`, which must be `fn`'s identity (0 for `+`) |
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

// reduce's function must be associative, and its initial value that function's identity.
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
- On a typed array, `map` and `filter` give a typed array of the same kind, and `toArray()` gives a `number[]`.
- A throw in a callback is rethrown once the operation is done.
- Callbacks run on pool threads. Promise work they start (`then` handlers, `queueMicrotask`) runs on the calling loop once the operation returns. Timers and asynchronous I/O started from a callback on a pool thread throw `Error: Timers and asynchronous I/O can't be started from a parallel operation's callback yet`. Start such work in a `parallel` task instead.

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

const lazy = parallel async () => prices.length;
```

```text
guide_guardrail.pts:33:23 - error PTS1002: the operand of `parallel` is a function, so its task would only make the function; give it the call instead (`parallel work()`)
guide_guardrail.pts:7:3 - error PTS3001: `total` is declared outside this parallel body, which runs on several threads at once, so assigning it here would race; combine the results with `.par.reduce` or `.par.sum`, or use an `Atomic` from pts:sync
guide_guardrail.pts:12:5 - error PTS3001: `sum` is declared outside this parallel body, which runs on several threads at once, so assigning it here would race; combine the results with `.par.reduce` or `.par.sum`, or use an `Atomic` from pts:sync
guide_guardrail.pts:18:24 - error PTS3001: `last` is declared outside this parallel body, which runs on several threads at once, so assigning it here would race; combine the results with `.par.reduce` or `.par.sum`, or use an `Atomic` from pts:sync
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
| `parallel async () => …` | `parallel work()` |

[examples/guide_guardrail_fixes.pts](examples/guide_guardrail_fixes.pts):

```ts
// Rewrites of the code pts/tests/errors/guide_guardrail.pts rejects: combine results
// instead of assigning outer variables, and leave a loop with a search, not a jump.
import { AtomicInt } from "pts:sync";
import { range } from "pts:parallel";

const prices = [12, 5, 30, 8];

// A total: .par.sum, or .par.reduce with an associative function and its identity.
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

What the guardrail doesn't check: the rule is lexical, so it doesn't follow calls (a function called from the body that assigns an outer variable isn't reported), and writes to fields and elements are allowed even when two threads make them at once. Such races can't crash the program: the runtime guarantees that a program with data races may see stale or lost values, but never crashes, corrupts memory or reads out of bounds. They can lose updates, though. A `Map` that several threads change at once can lose entries, so share it through a `Mutex`, as the `pts:sync` sample does.

### Performance

[BENCHMARKS.md](BENCHMARKS.md) has the numbers, losses included. On 8 threads, mandelbrot runs 4.65 times as fast as on one (5.78 times on a quieter run). Code that allocates a lot stops gaining past about 4 threads, because the garbage collector refills each thread's free lists under one lock and stops every thread to collect.

## The `pts:` modules

| Module | Contents |
|---|---|
| `pts:fs` | Node's synchronous file functions (`readFileSync`, `writeFileSync`, `appendFileSync`, `existsSync`, `statSync`, `mkdirSync`, `readdirSync`, `rmSync`, `renameSync`, `copyFileSync`, `mkdtempSync` and more), with Node's errors; text in UTF-8 |
| `pts:fs/promises` | the promise versions (`readFile`, `writeFile`, `readdir`, `rm` and more), also `fs.promises` |
| `pts:path` | Node's POSIX `path` |
| `pts:process` | `argv`, `env`, `exit`, `exitCode`, `cwd`, `platform`, `arch`, `pid`, `execPath`, `stdout.write`, `stderr.write`; the default export is the global `process` |
| `pts:parallel` | `range`, `join` and the classes of the `.par` views |
| `pts:sync` | `AtomicInt`, `Atomic<int>`, `Mutex`, `Once` and `Channel` |

`pts:fs`, `pts:path` and `pts:process` behave as Node's `node:fs`, `node:path` and `node:process`. Change `node:` to `pts:` in the imports and the program prints what it printed under Node, apart from the [differences](#other-differences-you-may-meet) such as `process.argv`. Importing `node:fs` itself is a tsc error ([below](#tscs-errors)). An HTTP server, `pts:http`, and `fetch` are planned.

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

## Common compile errors

Errors print one per line, as `file:line:col - error CODE: message`, and stop the build. They come in rounds: syntax errors first, then tsc's type errors, then ParallelScript's own rules, then what isn't supported yet. Fixing one round can bring up the next.

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

const bytes = readFileSync("/etc/hostname");

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
guide_not_yet.pts:45:15 - error PTS1001: reading a file without an encoding (into a Buffer, until typed arrays) is not supported yet
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
- [examples/](examples/): the samples from this guide, and from the other docs.
