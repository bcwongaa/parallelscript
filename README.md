# ParallelScript

ParallelScript is TypeScript compiled ahead of time to native binaries, with a garbage collector and real parallelism.

You write TypeScript, plus one new keyword, `parallel`. The compiler, `pts`, type-checks the program with a fork of the TypeScript 7 compiler ([typescript-go](https://github.com/microsoft/typescript-go), pinned at `typescript/v7.0.2`), lowers it to a typed intermediate representation, emits C and compiles that with clang into one executable. There is no VM, no JIT and no JavaScript engine. Memory is garbage collected (bdwgc). The runtime starts one event loop per core over one shared heap: `await` keeps its JavaScript meaning, and the parallel features spread work over the cores.

The aim is a language that reads and feels like TypeScript, runs as native code and can use every core. Parallelism is always explicit: nothing runs on another thread unless the program says `parallel`, `.par` or `parallel for`.

A plain TypeScript program (no `parallel`, no integer types, no `pts:` imports) must print exactly what Node prints. The test suite checks this by running such programs under Node too. The exceptions are listed in [SPEC §8](docs/SPEC.md#8-documented-differences-from-typescript-and-node).

**Status:** experimental. Milestones M0 to M5 are done, M6 is in progress ([status](#status)). It runs on x86-64 Linux only. There is no editor support yet.

```ts
import { range } from "pts:parallel";

// Plain TypeScript: compiled ahead of time to a native binary.
function collatzSteps(n: number): number {
  let steps = 0;
  while (n !== 1) {
    n = n % 2 === 0 ? n / 2 : 3 * n + 1;
    steps++;
  }
  return steps;
}
console.log(collatzSteps(27));

// The same function over a hundred thousand numbers, spread over every core.
const steps = range(1, 100_000).par.map(collatzSteps);
console.log(steps.par.max(), steps.indexOf(350) + 1);

// A task on another thread's event loop, awaited like any promise.
console.log(await parallel collatzSteps(77_031));
```

```
111
350 77031
350
```

## Contents

- [Quick start](#quick-start)
- [Feature tour](#feature-tour)
- [Benchmarks](#benchmarks)
- [Status](#status)
- [Known gaps](#known-gaps)
- [Repository layout](#repository-layout)
- [Documentation](#documentation)

## Quick start

You need:

- Go 1.26 (what `go.mod` asks for);
- clang and `ar` (developed with clang 18; `PTS_CC` and `PTS_AR` pick others);
- git, to fetch the vendored C libraries;
- Node only to run the differential tests (developed with Node 24.21), and hyperfine, Node, Bun, Go and python3 only for the benchmarks.

```sh
git clone https://github.com/bcwongaa/parallelscript.git
cd parallelscript
git submodule update --init pts/third_party   # bdwgc, libuv, PCRE2, Ryu, libbacktrace
make build                                    # go build -o pts-bin/pts ./cmd/pts
pts-bin/pts run docs/examples/hello.pts
```

```
Hello, ParallelScript
```

Don't initialize `_submodules/TypeScript`: it is large, and only upstream's own tests use it. The first build compiles the runtime and the vendored C libraries and caches them in `~/.cache/pts/` (`PTS_CACHE_DIR` moves it); later builds reuse them. Deleting the cache is always safe.

The commands (`pts help`):

| Command | What it does |
|---|---|
| `pts build file.pts [-o out]` | Compile to a native executable (by default named after the file). It links statically against the runtime and needs only libc and libm. |
| `pts run file.pts [args...]` | Compile and run; the arguments after the file go to the program. |
| `pts emit-c file.pts [-o file.c]` | Print the generated C, which carries `#line` directives back to the `.pts` source, so gdb and crash backtraces show `.pts` lines. |

`PTS_THREADS=n` sets the number of event loops and pool threads; by default it is the number of cores the process may use. With `PTS_THREADS=1`, `parallel` and `.par` still work, on the one thread. A copy of `pts` outside the repository needs `PTS_HOME` set to the repository root, where it finds the runtime sources.

Tests and benchmarks:

| Command | What it runs |
|---|---|
| `make test` | Every ParallelScript test: Go unit tests, the runtime's C tests (under ASan and UBSan, the pool and loop tests under TSan), the run, error and stress tests, the 300-program corpus, and the comparisons with Node when `node` is on PATH |
| `make test-diff` | Only the comparisons with Node (fails if Node is missing) |
| `make test-stress` | The parallel stress programs (`PTS_STRESS_REPEAT=n` repeats them) |
| `make test-upstream` | The upstream typescript-go suites the fork touches |
| `make bench` | The benchmarks against Node, Bun and Go, with hyperfine |

[PROGRESS.md](PROGRESS.md) lists every build and test command with its current baseline.

## Feature tour

Every sample here is a file in [docs/examples/](docs/examples/), run by the test suite, which checks that it prints the output shown. The samples that are plain TypeScript are also run under Node, which must print the same.

### Types and integer types

`number` keeps JavaScript's semantics. ParallelScript adds `int` (a 64-bit integer) and `i8` to `i64`, `u8` to `u64`, `f32` and `f64`. Literals take their type from the context. Implicit conversions must be lossless; anything else needs `as`, which converts the value (truncating toward zero and saturating, as Rust does). Integer arithmetic wraps on overflow, and integer `/` truncates.

```ts
// `number` is JavaScript's double. `int` is a 64-bit integer.
const exact: int = 9_007_199_254_740_993;
const rounded: number = 9_007_199_254_740_993;
console.log(exact, rounded);

// Integer division truncates. Mixing int and number gives a number.
const a: int = 7;
const b: int = 2;
const half: number = 2;
console.log(a / b, a % b, a / half);

// Overflow wraps.
let byte: u8 = 250;
byte += 10;
console.log(byte);

// Lossy conversions need `as`, which truncates and saturates.
const x = 3.99;
console.log(x as i32, -x as i32, 300 as u8, NaN as int);

// Integer types work as type arguments.
const counts = new Map<string, int>([["a", 1]]);
const pixels: u8[] = [0, 128, 255];
console.log(counts, pixels.map((p) => p / 2), typeof byte);

// Integer division by zero throws.
try {
  console.log(a / (b - 2));
} catch (e) {
  console.log(String(e));
}
```

```
9007199254740993 9007199254740992
3 1 3.5
4
3 -3 255 0
Map(1) { 'a' => 1 } [ 0, 64, 127 ] number
RangeError: Division by zero
```

A lossy conversion without `as` is a compile error:

```
lossy.pts:2:16 - error PTS2001: a number doesn't convert to int implicitly because the value could change; write `as int` to convert it
```

### Classes

Classes have fixed layouts and vtables. Single inheritance, abstract classes, parameter properties, accessors, statics, `private`, `#private` and `instanceof` work. Generics are monomorphized: each list of type arguments gets its own compiled copy. Interfaces stay structural. `console.log` prints instances as Node does.

```ts
abstract class Shape {
  constructor(readonly name: string) {}
  abstract area(): number;
  describe(): string {
    return `${this.name}: ${this.area().toFixed(2)}`;
  }
}

class Circle extends Shape {
  constructor(public radius: number) {
    super("circle");
  }
  area(): number {
    return Math.PI * this.radius ** 2;
  }
}

class Rect extends Shape {
  constructor(public width: number, public height: number) {
    super("rect");
  }
  area(): number {
    return this.width * this.height;
  }
}

function largest<T extends Shape>(items: T[]): T {
  return items.reduce((a, b) => (b.area() > a.area() ? b : a));
}

const shapes: Shape[] = [new Circle(1), new Rect(2, 3)];
console.log(shapes.map((s) => s.describe()));
const big = largest(shapes);
console.log(big, big instanceof Rect);
```

```
[ 'circle: 3.14', 'rect: 6.00' ]
Rect { name: 'rect', width: 2, height: 3 } true
```

Properties can't be added to or deleted from an object after it is made, except on `Record<string, T>` and index-signature types, which are hash maps.

### Async and the event loop

Async functions and generators compile to state machines whose frames live on the GC heap; there is no native stack per task. The event loop is libuv's.

```ts
function sleep(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

async function load(id: number): Promise<string> {
  await sleep(10 * id);
  return `item ${id}`;
}

// Timers, microtasks and I/O run in the order Node runs them.
setTimeout(() => console.log("timer"), 0);
Promise.resolve().then(() => console.log("microtask"));
console.log("sync");

// Top-level await and the Promise combinators.
console.log(await Promise.all([3, 1, 2].map(load)));
console.log(await Promise.race([load(2), load(1)]));

// Async generators and for await.
async function* countdown(from: number): AsyncGenerator<number> {
  for (let i = from; i > 0; i--) {
    await sleep(1);
    yield i;
  }
}
for await (const n of countdown(3)) {
  console.log(n);
}
```

```
sync
microtask
timer
[ 'item 3', 'item 1', 'item 2' ]
item 1
3
2
1
```

### `parallel` tasks

`parallel e` runs `e` as a new task on the least-loaded other event loop and returns a `Promise` of its value at once. Tasks share the heap, so captured objects are shared by reference. A task stays on its loop for its whole life, and a loop runs one task at a time, so code between two `await`s is never interrupted by another task on the same loop.

```ts
function fib(n: int): int {
  return n < 2 ? n : fib(n - 1) + fib(n - 2);
}

// `parallel e` runs e as a task on another thread's event loop
// and gives back a promise of its value.
const one: Promise<int> = parallel fib(30);
console.log(await one);

// Start several tasks at once, then wait for all of them.
const inputs: int[] = [32, 33, 34, 35];
const results = await Promise.all(inputs.map((n) => parallel fib(n)));
console.log(results);

// Async functions work as tasks too, and a throw rejects the promise.
async function check(n: int): Promise<string> {
  if (n < 0) {
    throw new RangeError(`negative: ${n}`);
  }
  return `ok: ${n}`;
}
console.log(await parallel check(1));
try {
  await parallel check(-1);
} catch (e) {
  console.log(String(e));
}
```

```
832040
[ 2178309, 3524578, 5702887, 9227465 ]
ok: 1
RangeError: negative: -1
```

### `.par` views

`.par` on an array, a typed array or a `range` from `pts:parallel` gives a parallel view. Its methods (`map`, `filter`, `forEach`, `reduce`, `flatMap`, `some`, `every`, `find`, `count`, `sum`, `min`, `max`, `sort`, `sortBy`, `toArray`) run on a work-stealing fork-join pool, modelled on Rust's Rayon, and return when the work is done. `map` and `filter` keep the order; `reduce` needs an associative function and gives what `reduce` does, for any initial value.

```ts
import { join, range } from "pts:parallel";

function isPrime(n: number): boolean {
  if (n < 2) {
    return false;
  }
  for (let d = 2; d * d <= n; d++) {
    if (n % d === 0) {
      return false;
    }
  }
  return true;
}

// Each call splits the work over the fork-join pool and returns when it is done.
console.log(range(0, 200_000).par.count(isPrime));
console.log(range(1, 1_000_001).par.sum());

// Arrays and typed arrays have a .par view; map and filter keep the order.
const words = ["steal", "fork", "split", "join", "merge"];
console.log(words.par.map((w) => w.toUpperCase()));
console.log(words.par.filter((w) => w.length === 5));
console.log(words.par.sort());
const samples = Float64Array.from([4, 9, 16, 25]);
console.log(samples.par.map(Math.sqrt), samples.par.max());

// join runs two closures, perhaps on two threads, and returns both results.
const [primes, total] = join(
  () => range(0, 1000).par.filter(isPrime).length,
  () => range(0, 1000).par.reduce((a, b) => a + b, 0),
);
console.log(primes, total);
```

```
17984
500000500000
[ 'STEAL', 'FORK', 'SPLIT', 'JOIN', 'MERGE' ]
[ 'steal', 'split', 'merge' ]
[ 'fork', 'join', 'merge', 'split', 'steal' ]
Float64Array(4) [ 2, 3, 4, 5 ] 25
168 499500
```

### `parallel for`

`parallel for (const x of items)` runs the body for each item on the pool and ends when every iteration has. `continue` works; `break`, `return`, `await`, `yield` and labelled jumps out of the body are compile errors. If an iteration throws, the rest are cancelled as far as possible, and the first error is rethrown after the loop.

```ts
import { range } from "pts:parallel";

class Tile {
  pixels: Uint8Array;
  constructor(readonly name: string, size: number) {
    this.pixels = new Uint8Array(size);
  }
}
const tiles = [new Tile("a", 3), new Tile("b", 4), new Tile("c", 5)];

// Writing fields and elements from the body is allowed.
parallel for (const tile of tiles) {
  for (let i = 0; i < tile.pixels.length; i++) {
    tile.pixels[i] = (i + 1) * 50;
  }
}
for (const tile of tiles) {
  console.log(tile.name, tile.pixels.join(" "));
}

// Over a range of integers, each iteration writing its own slot.
const squares = new Array<number>(8).fill(0);
parallel for (const i of range(0, 8)) {
  if (i % 2 === 1) {
    continue;
  }
  squares[i] = i * i;
}
console.log(squares.join(" "));
```

```
a 50 100 150
b 50 100 150 200
c 50 100 150 200 250
0 0 4 0 16 0 36 0
```

There is one parallel-specific compile error, the guardrail: a parallel body (a `parallel for` body, a callback given to a `.par` method or `join`, the operand of `parallel`) can't assign a `let` or parameter declared outside it. Writing `count++` for an outer `let count` in a `.par.forEach` callback gives:

```
guard.pts:3:34 - error PTS3001: `count` is declared outside this parallel body, which runs on several threads at once, so assigning it here would race; combine the results with `.par.reduce` or `.par.sum`, or use an `Atomic` from pts:sync
```

Changing object fields and array elements from parallel code is allowed. A program with data races may see stale values, lose elements pushed at the same time or get a `RangeError`, but it must never crash the runtime, corrupt the heap, or read or write out of bounds ([SPEC §4.1](docs/SPEC.md#41-the-race-safety-invariant-non-negotiable)). The stress tests in `pts/tests/stress/` check this. Share maps and other tables through a `Mutex`.

### `pts:sync`

`AtomicInt` (also `Atomic<int>`) is a 64-bit integer with sequentially consistent `load`, `store`, `add` and `compareExchange`. `Mutex<T>` guards a value: `withLock(fn)` blocks the thread for short critical sections, and `await lock()` waits without blocking the loop. `Channel<T>` is a bounded or unbounded queue between tasks, with `send`, `recv`, `close` and `for await`. There is also `Once`.

```ts
import { AtomicInt, Channel, Mutex } from "pts:sync";
import { range } from "pts:parallel";

// An atomic counter shared by every thread of a parallel loop.
const multiplesOf3 = new AtomicInt();
parallel for (const i of range(0, 100_000)) {
  if (i % 3 === 0) {
    multiplesOf3.add(1);
  }
}
console.log(multiplesOf3.load());

// A Mutex guards a value; withLock holds the lock while the callback runs.
const seen = new Mutex(new Map<string, number>());
range(0, 1000).par.forEach((i) => {
  const key = i % 2 === 0 ? "even" : "odd";
  seen.withLock((m) => m.set(key, (m.get(key) ?? 0) + 1));
});
console.log(seen.withLock((m) => [...m.entries()].sort()));

// A Channel passes values between tasks on different threads.
const jobs = new Channel<number>(4);
async function produce(): Promise<void> {
  for (let i = 1; i <= 10; i++) {
    await jobs.send(i);
  }
  jobs.close();
}
const producer = parallel produce();
let sum = 0;
for await (const job of jobs) {
  sum += job * job;
}
await producer;
console.log(sum);
```

```
33334
[ [ 'even', 500 ], [ 'odd', 500 ] ]
385
```

### RegExp

`RegExp` is backed by PCRE2: the runtime translates JavaScript patterns and flags to PCRE2's, and reports match positions in UTF-16 code units, as JavaScript does. Regex literals, `new RegExp`, `exec`, `test`, `lastIndex`, named groups and the String methods that take a RegExp (`match`, `matchAll`, `search`, `split`, `replace`, `replaceAll`) work. `String.prototype.normalize` works too.

```ts
const log = `09:15 ERROR disk full on /dev/sda1
09:16 WARN cpu at 93%
09:20 ERROR disk full on /dev/sdb2`;

// Named groups, the g and m flags, and matchAll.
const entry = /^(?<time>[\d:]+) (?<level>[A-Z]+) (?<text>.*)$/gm;
for (const m of log.matchAll(entry)) {
  const { time, level, text } = m.groups!;
  console.log(`${level} at ${time}: ${text}`);
}

// test, exec, split, and replace with $ references or a callback.
console.log(/WARN/.test(log), /cpu at (\d+)%/.exec(log)?.[1]);
console.log("tea, coffee,  milk".split(/\s*,\s*/));
console.log("Smith, John".replace(/(\w+), (\w+)/, "$2 $1"));
console.log(log.replace(/\/dev\/(\w+)/g, (_, dev: string) => dev.toUpperCase()));
```

```
ERROR at 09:15: disk full on /dev/sda1
WARN at 09:16: cpu at 93%
ERROR at 09:20: disk full on /dev/sdb2
true 93
[ 'tea', 'coffee', 'milk' ]
John Smith
09:15 ERROR disk full on SDA1
09:16 WARN cpu at 93%
09:20 ERROR disk full on SDB2
```

### Also there

Unions with every form of narrowing, optional chaining and `??`, enums, tuples, destructuring, spread and rest, generic functions and classes, exceptions with `.pts` stack traces, `any` as a checked dynamic value (`JSON.parse(s).user?.name` works; assigning a wrong type from it throws `TypeError`), `JSON`, `Map` and `Set`, `Math`, basic `Date`, typed arrays, generators and iterators, ES modules with relative imports, `console` with Node's formatting, the `pts:fs` (sync and promises), `pts:path` and `pts:process` modules, which behave as Node's `node:` modules, and `pts:http`, an HTTP/1.1 server in the style of Bun's `serve` that can accept connections on every core (`reusePort: true`). The full list is [docs/SPEC.md](docs/SPEC.md).

## Benchmarks

These are the benchmarks added from M1 to M6, with their latest numbers, from the M5 run and, for the HTTP servers, the M6.2b run (2026-10-12). The machine is a laptop with an Intel Core i5-8250U (4 cores, 8 hardware threads, clocked lower when all cores run) and 15 GB of RAM, running Linux Mint 22.2, clang 18, Node 24.21.0, Bun 1.4.3 and Go 1.26.9. It also runs other services, so timings vary. Times are wall-clock means from hyperfine, process start included; lower is better. "vs Node" is Node's time divided by ParallelScript's, and "vs Go" likewise: above 1× means ParallelScript is faster. Node and Bun run the same program as ParallelScript. Every milestone's table, with notes on each benchmark, is in [docs/BENCHMARKS.md](docs/BENCHMARKS.md).

One thread:

| Benchmark | ParallelScript | Node | Bun | Go | vs Node | vs Go |
|---|---|---|---|---|---|---|
| fib (recursive, `number`) | 254 ms | 670 ms | 505 ms | 272 ms | 2.64× | 1.07× |
| fib_int (the same on `int`) | 132 ms | – | – | 235 ms | – | 1.78× |
| async_fib (1.7 million async calls) | 162 ms | 259 ms | 272 ms | – | 1.60× | – |
| string_building | 1096 ms | 1760 ms | 6632 ms | 403 ms | 1.61× | 0.37× |
| file_io (`fs/promises`) | 519 ms | 825 ms | 361 ms | 350 ms | 1.59× | 0.67× |
| nbody | 810 ms | 853 ms | 870 ms | 592 ms | 1.05× | 0.73× |
| word_freq (`Map` of 2 million words) | 1988 ms | 1769 ms | 1474 ms | 754 ms | 0.89× | 0.38× |
| binary_trees (GC stress) | 1774 ms | 1379 ms | 1598 ms | 3126 ms | 0.78× | 1.76× |
| json (stringify and parse) | 1119 ms | 765 ms | 419 ms | 1770 ms | 0.68× | 1.58× |

Parallel, with `PTS_THREADS` at 1, 4 and 8. "Speedup ×8" is the 1-thread time divided by the 8-thread time. Node and Bun run the same program without `.par` and `parallel`, on one thread; Go uses goroutines.

| Benchmark | PTS ×1 | PTS ×4 | PTS ×8 | Speedup ×8 | Node | Go | ×8 vs Node | ×8 vs Go |
|---|---|---|---|---|---|---|---|---|
| mandelbrot (`.par.forEach` over rows) | 1525 ms | 521 ms | 328 ms | 4.65× | 1649 ms | 321 ms | 5.03× | 0.98× |
| fan_out (64 `parallel` tasks) | 935 ms | 347 ms | 343 ms | 2.72× | 1011 ms | 312 ms | 2.95× | 0.91× |
| sum_reduce (100 million doubles) | 4080 ms | 2886 ms | 2390 ms | 1.71× | 5423 ms | 907 ms | 2.27× | 0.38× |
| word_count (`.par.map` into maps) | 2073 ms | 948 ms | 1737 ms | 1.19× | 2275 ms | 220 ms | 1.31× | 0.13× |

What the numbers say:

- **Wins.** Recursive calls, async calls and string building are faster than Node and Bun, and file_io is 1.59× Node's speed. One `pts:http` loop serves 2.2 to 2.7 times as many requests as Node, and four loops beat Go's `net/http`. fib_int is 1.78× Go's speed. On 8 threads, every parallel benchmark beats Node's single thread, and mandelbrot and fan_out come close to Go (0.98× and 0.91×).
- **Losses.** Programs that allocate a lot are slower than Node: binary_trees (0.78×), json (0.68×) and word_freq (0.89×). json is 0.37× Bun's speed, and file_io 0.70×. Against Go, ParallelScript loses on string_building (0.37×), word_freq (0.38×), nbody (0.73×) and file_io (0.67×), and on 8 threads on sum_reduce (0.38×) and word_count (0.13×).
- **Why.** bdwgc is a conservative, non-moving, non-generational collector. With threads, it refills each thread's free lists under one lock and stops every thread to collect, so allocation-heavy parallel code stops gaining past 4 threads: word_count is slower on 8 threads than on 4. nbody's loop indices are doubles until integer inference (M8). sum_reduce is bound by memory bandwidth, and one thread zeroes its 800 MB array.
- **Noise.** The machine was busier during this run than during the M5.7b run an hour earlier, which measured 8-thread speedups of 5.78× (mandelbrot), 3.26× (fan_out), 2.22× (word_count) and 1.83× (sum_reduce). Both runs are in BENCHMARKS.md.

HTTP servers (M6.2b), in requests per second, higher is better: `pts:http` on 1 and 4 loops (`reusePort: true`), Node's `node:http` and `Bun.serve` on one thread, Go's `net/http` on every core, each loaded by oha with 64 connections on the same machine.

| Benchmark | PTS ×1 | PTS ×4 | Node | Bun | Go | ×4 vs Node | ×4 vs Go |
|---|---|---|---|---|---|---|---|
| http_hello (13-byte text) | 56,818 | 84,739 | 20,939 | 59,753 | 69,483 | 4.05× | 1.22× |
| http_json (parse an order, total it, answer JSON) | 34,096 | 56,132 | 15,592 | 34,189 | 48,436 | 3.60× | 1.16× |

Start-up, measured at M0: hello world runs in 1.4 ms, against 75.4 ms for Node and 4.3 ms for Bun.

## Status

The plan is [docs/MASTER_PLAN.md](docs/MASTER_PLAN.md); each finished milestone is a git tag.

| Milestone | What it covers | State |
|---|---|---|
| M0 Bootstrap | `.pts` in the forked front end, the C runtime skeleton, the pipeline end to end, the test harness, CI | done (`m0`) |
| M1 Core language | Numbers, integer types, strings, statements, functions, modules | done (`m1`) |
| M2 Data and memory | The GC and object model, classes, object literals, interfaces, arrays, closures, `Map`/`Set`, generics, enums, tuples, destructuring, spread | done (`m2`) |
| M3 The TypeScript feel | Unions, optional chaining, exceptions and stack traces, checked `any`, JSON, the standard built-ins, `console.log` parity, a 300-program corpus compared with Node | done (`m3`) |
| M4 Async on one loop | Generators, iterators, async/await, promises, the libuv event loop and timers, async iteration, `pts:fs`, `pts:path`, `pts:process` | done (`m4`) |
| M5 Parallel | The `parallel` keyword, one event loop per core, the fork-join pool, `.par`, typed arrays, `parallel for` and the guardrail, `pts:sync`, stress tests, parallel benchmarks | done (`m5`) |
| M6 Real programs | RegExp, String methods with RegExps and `normalize`, the documentation refresh, `pts:http`'s server on one loop or every loop, the global `fetch` with `AbortController`, bytes in `pts:fs`, the first example app (`examples/hasher`) (done); the other example apps (planned) | in progress |
| M7 Editor support | `pts lsp`, a VS Code extension, a usability pass | planned |

Planned after M7, if the project goes on: M8, performance work (escape analysis, integer inference, unboxed arrays, inline caches for `any`, a precise GC experiment).

## Known gaps

Not built yet:

- `https://` for `fetch` (no TLS yet), `pts lsp`, the VS Code extension and `pts test` (M7).
- Platforms other than x86-64 Linux. The vendored libraries are configured for it, and publishing new objects to other threads relies on x86's store order (D-130).
- npm packages and `node_modules`.

Left out by design, each with a compile error that says why: `eval`, `new Function`, `with`, `var`, `arguments`, prototype manipulation, `Proxy`, `Reflect`, declaration merging, namespaces, `Symbol` other than `Symbol.iterator` and `Symbol.asyncIterator`. `bigint` is not supported yet.

Differences from Node ([SPEC §8](docs/SPEC.md#8-documented-differences-from-typescript-and-node) has the full list). The main ones:

- Objects have fixed shapes: no adding or deleting properties, except on hash-map types.
- `any` is checked where it flows into typed code, and casts are checked at run time: both throw `TypeError` on a mismatch.
- Arrays have no holes. Reading an array or a string past its end, or a missing key of a hash-map object, gives `undefined` where any value can go, but throws `RangeError` where a value of the element's type is required.

Known issues ([PROGRESS.md](PROGRESS.md#known-issues) has them all):

- 47 of the 300 corpus programs still print something other than Node; each file says why.
- No temporal dead zone checks for captured variables and classes: a closure that runs before the declaration of a variable it captures sees a zero value (or crashes, for a string or an object), and `new C()` before `class C` has run works. Node throws `ReferenceError` in both cases.
- `await` inside an expression-bodied async arrow, other than first, is a compile error; a block body works.
- Overrides must keep the overridden method's parameter types.
- RegExp: no `indices` for the `d` flag, no `v` flag, and without `u`, `.` matches an astral character whole.
- Indexing a non-ASCII string is O(n).
- Parallel: a loop busy with a long timer or a server doesn't take tasks queued on other loops; timers and async I/O started from a `.par` callback on a pool thread throw `Error`; recursive `join` is slower than sequential code, because each join allocates.

## Repository layout

The repository is a fork of [microsoft/typescript-go](https://github.com/microsoft/typescript-go). ParallelScript's code is in `cmd/pts/` and `pts/`. Its edits to upstream code are kept minimal, marked `// PTS:` and listed in [docs/FORK.md](docs/FORK.md).

| Path | Contents |
|---|---|
| `cmd/pts/` | The `pts` command |
| `pts/frontend/` | Loads `.pts` programs through the forked TypeScript front end |
| `pts/check/` | The semantic pass: the rules tsc doesn't know (numeric types, the guardrail, unsupported features) |
| `pts/numeric/` | The numeric types and their conversion rules |
| `pts/lower/` | Lowering from the checked AST to IR: classes, closures, generics, unions, generator and async state machines, `parallel` and `.par` |
| `pts/ir/` | The typed, structured IR |
| `pts/cgen/` | IR to readable C11 with `#line` directives |
| `pts/driver/` | The pipeline end to end: load, check, lower, emit C, compile |
| `pts/toolchain/` | Builds and caches the runtime and the vendored libraries; compiles and links programs with clang |
| `pts/diagnostic/` | One error format for tsc's and ParallelScript's errors |
| `pts/runtime/` | The C runtime: values, strings, objects, arrays, maps, promises, event loops and the scheduler, the fork-join pool, console, JSON, RegExp (`src/`, `include/pts.h`, C unit tests in `tests/`, Unicode table generators in `gen/`) |
| `pts/lib/` | The prelude (`prelude.d.ts`) and the `pts:` modules, written in ParallelScript: `parallel.pts`, `sync.pts`, `fs.pts`, `path.pts`, `process.pts` |
| `pts/third_party/` | Vendored C libraries as pinned submodules: bdwgc, libuv, PCRE2, Ryu, libbacktrace |
| `pts/tests/` | `run/` (programs and their expected output), `errors/` (expected compile errors), `corpus/` (realistic programs compared with Node), `stress/` (parallel stress programs); see [pts/tests/README.md](pts/tests/README.md) |
| `pts/bench/` | One directory per benchmark: `main.pts`, its Go twin `main.go` (where Go has one) and, where Node and Bun can't run `main.pts` as it is, a `main.ts` for them; `run.sh` checks that they all print the same and times them |
| `docs/` | The documentation below; `docs/examples/` holds the docs' samples, which run as tests |
| `internal/` and the rest | Upstream typescript-go |

## Documentation

- [docs/README.md](docs/README.md): an index of all the docs.
- [docs/GUIDE.md](docs/GUIDE.md): a tour for TypeScript developers: what's the same, what's different, the parallel features, common compile errors.
- [docs/SPEC.md](docs/SPEC.md): the language reference, with the differences from Node in §8.
- [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md): the compiler pipeline and the runtime.
- [docs/DECISIONS.md](docs/DECISIONS.md): every design decision, with its reasons.
- [docs/BENCHMARKS.md](docs/BENCHMARKS.md): every benchmark run, losses included.
- [PROGRESS.md](PROGRESS.md) and [docs/MASTER_PLAN.md](docs/MASTER_PLAN.md): where the work stands and what comes next.
- [docs/FORK.md](docs/FORK.md): the edits to upstream code.
- [docs/UPSTREAM_README.md](docs/UPSTREAM_README.md): typescript-go's own README.

The repository keeps upstream's Apache-2.0 [LICENSE](LICENSE) and [NOTICE.txt](NOTICE.txt).
