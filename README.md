# ParallelScript

**TypeScript, compiled to native code, running on every core.**

ParallelScript is TypeScript compiled ahead of time to native binaries, with a garbage collector and real parallelism.

You write TypeScript, plus one new keyword, `parallel`. The compiler, `pts`, type-checks the program with a fork of the TypeScript 7 compiler ([typescript-go](https://github.com/microsoft/typescript-go), pinned at `typescript/v7.0.2`), lowers it to a typed intermediate representation, emits C and compiles that with clang into one executable. There is no VM, no JIT and no JavaScript engine. Memory is garbage collected (bdwgc). The runtime starts one event loop per core over one shared heap: `await` keeps its JavaScript meaning, and the parallel features spread work over the cores.

The aim is a language that reads and feels like TypeScript, runs as native code and can use every core. Parallelism is always explicit: nothing runs on another thread unless the program says `parallel`, `.par` or `parallel for`.

A plain TypeScript program (no `parallel`, no integer types, no `pts:` imports) must print exactly what Node prints. The test suite checks this by running such programs under Node too. The exceptions are listed in [SPEC §8](docs/SPEC.md#8-documented-differences-from-typescript-and-node).

Why use it:

- **It is TypeScript.** The front end is the TypeScript 7 compiler itself, so the types, the inference and the error messages are tsc's. The test suite runs hundreds of programs under Node too and compares what they print, among them a corpus of 300 realistic programs, 269 of which are classified as printing exactly what Node prints (each of the others says why).
- **Native binaries that start in a millisecond.** `pts build` makes one executable that needs only libc and libm. Hello world starts in about 1 ms, where Node takes 74 ms.
- **Every core, one heap.** `parallel f()` runs a task on another core's event loop, `xs.par.map(f)` splits work over a work-stealing pool, and `parallel for` runs a loop's iterations in parallel. Threads share objects directly; nothing is copied or serialized. A data race can make a program read a stale value, but it can't crash it or corrupt memory.
- **One guardrail, not a borrow checker.** The compiler rejects assigning a captured outer `let` inside a parallel body, and checks nothing else about how threads share data. `pts:sync` has atomics, a mutex and channels for the code that needs them.
- **Batteries for servers and tools.** `pts:http` is a server in the style of Bun's `serve` that accepts connections on every core, and `fetch`, `pts:fs`, `pts:path`, `pts:process`, RegExp and JSON behave as they do in Node, and `pts test` runs tests written for Node's `node:test` and `node:assert`.

**Status:** experimental. Milestones M0 to M7 are done, the last with a language server, a VS Code extension and `pts test` ([status](#status)). It runs on x86-64 Linux only.

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
- [Examples](#examples)
- [Benchmarks](#benchmarks)
- [Differences from TypeScript and Node](#differences-from-typescript-and-node)
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
| `pts test [paths...]` | Build and run the test files (`name.test.pts`, …) under paths, written with `pts:test` and `pts:assert` as for Node's `node:test` and `node:assert/strict`; Node's spec report, exit code 1 on a failure |
| `pts help [command]`, `pts version` | The usage, or a command's (also `pts <command> --help`); the version and the commit it was built from |
| `pts lsp [--stdio]` | The language server, for any editor that speaks the Language Server Protocol: TypeScript's own completions, hovers, definitions and errors for `.pts` files, with ParallelScript's prelude and `pts:` modules, plus ParallelScript's errors (numeric rules, the guardrail). |

**In VS Code:** build the extension and install it, then put `pts` on PATH or set `parallelscript.server.path` to the absolute path of `pts-bin/pts` (details in [pts/editors/vscode/README.md](pts/editors/vscode/README.md)):

```sh
make vscode                                                   # needs npm; builds pts/editors/vscode/dist/parallelscript.vsix
code --install-extension pts/editors/vscode/dist/parallelscript.vsix
```

`.pts` files then get TypeScript's highlighting with `parallel` as a keyword, and the language server's completions, hovers, definitions (into the `pts:` modules too) and errors, ParallelScript's among tsc's.

`PTS_THREADS=n` sets the number of event loops and pool threads; by default it is the number of cores the process may use. With `PTS_THREADS=1`, `parallel` and `.par` still work, on the one thread. A copy of `pts` outside the repository needs `PTS_HOME` set to the repository root, where it finds the runtime sources.

Tests and benchmarks:

| Command | What it runs |
|---|---|
| `make test` | Every ParallelScript test: Go unit tests, the runtime's C tests (under ASan and UBSan, the pool and loop tests under TSan), the run, error and stress tests, the 300-program corpus, and the comparisons with Node when `node` is on PATH |
| `make test-diff` | Only the comparisons with Node (fails if Node is missing) |
| `make test-stress` | The parallel stress programs (`PTS_STRESS_REPEAT=n` repeats them) |
| `make test-upstream` | The upstream typescript-go suites the fork touches |
| `make bench` | The benchmarks against Node, Bun and Go, with hyperfine |
| `make vscode` | The VS Code extension's tests (its grammar against the parser's samples), then its package; `npm run test:vscode` in `pts/editors/vscode` runs it in a headless VS Code |

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

### HTTP and `fetch`

`pts:http` serves HTTP/1.1 with the Fetch standard's `Request` and `Response`, in the style of Bun's `serve`. With `reusePort: true`, every event loop accepts connections, so requests are handled on every core. `fetch` is global, as in Node, with `AbortController` for cancelling requests.

```ts
import { serve, Response } from "pts:http";

// An HTTP/1.1 server in the style of Bun's serve; reusePort accepts connections on every core.
const server = serve({
  port: 0, // any free port; a real server names one, such as 3000
  hostname: "127.0.0.1",
  reusePort: true,
  async fetch(request) {
    const { name } = await request.json();
    return Response.json({ greeting: `hello, ${name}` });
  },
});

// fetch, Request, Response and Headers are globals, as in Node.
const response = await fetch(server.url, { method: "POST", body: JSON.stringify({ name: "Ada" }) });
console.log(response.status, response.headers.get("content-type"), await response.json());
server.stop(); // a real server keeps serving; this sample stops so that it ends
```

```
200 application/json { greeting: 'hello, Ada' }
```

### Also there

Unions with every form of narrowing, optional chaining and `??`, enums, tuples, destructuring, spread and rest, generic functions and classes, exceptions with `.pts` stack traces, `any` as a checked dynamic value (`JSON.parse(s).user?.name` works; assigning a wrong type from it throws `TypeError`), `JSON`, `Map` and `Set`, `Math`, basic `Date`, typed arrays, generators and iterators, ES modules with relative imports, `console` with Node's formatting, and the `pts:fs` (sync and promises, text and bytes), `pts:path` and `pts:process` modules, which behave as Node's `node:` modules, and tests written with `pts:test` and `pts:assert` as for `node:test` and `node:assert/strict`, run by `pts test`. The full list is [docs/SPEC.md](docs/SPEC.md).

## Examples

[examples/](examples/README.md) has four programs written the way real ones are. The test suite builds each one, runs it under one thread and under four, and checks its output against an independent answer. The numbers are from the development machine, a 4-core, 8-thread laptop.

| Example | What it does | Numbers |
|---|---|---|
| [hasher](examples/hasher/main.pts) | SHA-256 of files, written in ParallelScript with `u32` arithmetic, one file per `.par.map` job | 200 MB in 8 files: 5.8 s on one thread, 2.1 s on four (Node runs the same code in 4.6 s) |
| [mandelbrot](examples/mandelbrot/main.pts) | Renders the set into a PNG, rows painted by `range(…).par.forEach`, with a PNG writer in ParallelScript | 1920×1280: 2.07 s on one thread, 0.31 s on eight (6.8×) |
| [api-server](examples/api-server/main.pts) | A JSON API that answers on every core, its state in a `pts:sync` `Mutex<Map>`, with `request.signal` for clients that leave | `GET /items/1`: 51,000 requests/s on one loop, 99,000 on four |
| [log-analyzer](examples/log-analyzer/main.pts) | Parses access logs with a RegExp per line, counts with `.par.map` over pieces of the files, prints a report | 146 MB of logs: 2.79 s on one thread, 1.16 s on four (Node runs the same program in 1.72 s) |

```sh
pts-bin/pts build examples/log-analyzer/main.pts -o log-analyzer
./log-analyzer /var/log/nginx/access.log      # PTS_THREADS=1 ./log-analyzer … uses one thread
```


## Benchmarks

The latest numbers, from the M6 run (2026-10-13). The machine is a laptop with an Intel Core i5-8250U (4 cores, 8 hardware threads, clocked lower when all cores run) and 15 GB of RAM, running Linux Mint 22.2, clang 18, Node 24.21.0, Bun 1.4.3 and Go 1.26.9. It also runs other services, so timings vary; [docs/BENCHMARKS.md](docs/BENCHMARKS.md) gives each mean with its standard deviation, every milestone's table and notes on each benchmark, and `pts/bench/results/m6/` keeps every run's time. Times are wall-clock means of 10 runs in 2 interleaved rounds, process start included; lower is better. "vs Node" is Node's time divided by ParallelScript's, and "vs Go" likewise: above 1× means ParallelScript is faster. Node and Bun run the same program as ParallelScript, and every implementation must print the same output; an audit made every twin do the same work (D-167).

One thread:

| Benchmark | ParallelScript | Node | Bun | Go | vs Node | vs Go |
|---|---|---|---|---|---|---|
| startup (hello world) | 1.2 ms | 73 ms | 4.7 ms | 1.3 ms | 60.48× | 1.08× |
| fib (recursive, `number`) | 254 ms | 636 ms | 416 ms | 258 ms | 2.50× | 1.01× |
| fib_int (the same on `int`) | 127 ms | – | – | 214 ms | – | 1.69× |
| async_fib (1.7 million async calls) | 148 ms | 228 ms | 205 ms | – | 1.53× | – |
| string_building | 1064 ms | 1654 ms | 3924 ms | 572 ms | 1.56× | 0.54× |
| file_io (`fs/promises`, warm cache) | 428 ms | 710 ms | 299 ms | 292 ms | 1.66× | 0.68× |
| nbody | 779 ms | 853 ms | 842 ms | 504 ms | 1.09× | 0.65× |
| word_freq (`Map` of 2 million words) | 1493 ms | 1302 ms | 1035 ms | 1051 ms | 0.87× | 0.70× |
| binary_trees (GC stress) | 1684 ms | 1424 ms | 1579 ms | 2883 ms | 0.85× | 1.71× |
| spectral_norm | 2788 ms | 1948 ms | 2227 ms | 1430 ms | 0.70× | 0.51× |
| json (stringify and parse) | 1097 ms | 729 ms | 471 ms | 1807 ms | 0.67× | 1.65× |

Parallel, with `PTS_THREADS` at 1, 4 and 8. "Speedup ×8" is the 1-thread time divided by the 8-thread time. Node and Bun run the same program without `.par` and `parallel`, on one thread; Go uses goroutines.

| Benchmark | PTS ×1 | PTS ×4 | PTS ×8 | Speedup ×8 | Node | Go | ×8 vs Node | ×8 vs Go |
|---|---|---|---|---|---|---|---|---|
| mandelbrot (`.par.forEach` over rows) | 1465 ms | 379 ms | 241 ms | 6.09× | 1592 ms | 223 ms | 6.62× | 0.93× |
| fan_out (64 `parallel` tasks) | 874 ms | 238 ms | 250 ms | 3.49× | 998 ms | 249 ms | 3.99× | 0.99× |
| sum_reduce (100 million doubles) | 3542 ms | 1779 ms | 1677 ms | 2.11× | 4514 ms | 686 ms | 2.69× | 0.41× |
| word_count (`.par.map` into maps, warm cache) | 1344 ms | 587 ms | 1018 ms | 1.32× | 1087 ms | 185 ms | 1.07× | 0.18× |

HTTP servers, in requests per second, higher is better: `pts:http` on 1 and 4 loops (`reusePort: true`), Node's `node:http` and `Bun.serve` on one thread, Go's `net/http` on every core, each loaded by oha with 64 connections on the same machine.

| Benchmark | PTS ×1 | PTS ×4 | Node | Bun | Go | ×4 vs Node | ×4 vs Go |
|---|---|---|---|---|---|---|---|
| http_hello (13-byte text) | 61,572 | 109,696 | 24,133 | 71,162 | 85,174 | 4.55× | 1.29× |
| http_json (parse an order, total it, answer JSON) | 30,878 | 59,681 | 18,548 | 41,334 | 48,238 | 3.22× | 1.24× |

What the numbers say:

- **Wins.** A ParallelScript program starts in about a millisecond. Recursive calls, async calls and string building are faster than Node and Bun, and file_io is 1.66× Node's speed. One `pts:http` loop serves 1.7 to 2.6 times as many requests as Node, and four loops beat Go's `net/http` by 24 to 29%. fib_int is 1.69× Go's speed, and binary_trees and json beat Go. On 8 threads, every parallel benchmark beats Node's single thread, and mandelbrot and fan_out match Go (0.93× and 0.99×).
- **Losses.** Programs that allocate a lot are slower than Node: binary_trees (0.85×), json (0.67×) and word_freq (0.87×); json is 0.43× Bun's speed. spectral_norm is 0.70× Node. Against Go, ParallelScript loses on string_building (0.54×), nbody (0.65×), spectral_norm (0.51×), file_io (0.68×) and word_freq (0.70×), and on 8 threads on sum_reduce (0.41×) and word_count (0.18×).
- **Why.** bdwgc is a conservative, non-moving, non-generational collector. With threads, it refills each thread's free lists under one lock and stops every thread to collect, so allocation-heavy parallel code stops gaining past 4 threads: word_count is slower on 8 threads than on 4, and code that does little but allocate can be several times slower on 8 threads than on one (`PTS_THREADS=4` avoids it; the guide's Performance section has an example). nbody's and spectral_norm's loop indices are doubles until integer inference (M8), and spectral_norm's inner loop rechecks a module-level constant at every read. sum_reduce is bound by memory bandwidth, and one thread zeroes its 800 MB array.

## Differences from TypeScript and Node

ParallelScript keeps JavaScript's semantics wherever it can; [SPEC §8](docs/SPEC.md#8-documented-differences-from-typescript-and-node) lists every difference, and [docs/GUIDE.md](docs/GUIDE.md) explains them for TypeScript developers. The main ones:

- **Types are enforced at run time.** Values have fixed representations: a `number` is a double, an object type has a fixed shape. Objects can't gain or lose properties, except values of hash-map types (`Record<string, T>`, index signatures). `any` is a checked dynamic value: where it flows into typed code, and in every cast, a value of the wrong type throws `TypeError` instead of travelling on.
- **Integer types.** `int`, `i8` … `u64` and `f32` are real machine types with wrapping arithmetic, conversions without loss are implicit, and the rest need `as`.
- **Writes make no holes.** Writing past an array's end throws `RangeError` instead of leaving holes. `new Array(n)` of strings, objects or dynamic values has JS's holes: the callback methods skip them, as JS's do, and where the element type can hold undefined, reads give undefined for them. Copies of a `string[]`, an object array or a union without undefined (`(string | null)[]`) keep them, and typed reads of one throw `RangeError`, but `for…of` reads one as `undefined`, and `find` and its kin pass one to a callback whose element parameter is optional or has a default as left out. Arrays of numbers and booleans start as zeros. Reading an array or a string past its end, or a missing key of a hash-map object, gives `undefined` where the context can hold it and throws `RangeError` where a value of the element's type is required.
- **Left out by design,** each with a compile error that says why: `eval`, `new Function`, `with`, `var`, `arguments`, prototype manipulation, `Proxy`, `Reflect`, namespaces and declaration merging other than of interfaces.
- **One new keyword.** `parallel` is a keyword only in `parallel <expression>` and `parallel for`; as a name it still works.

## Status

The historical M0–M7 plan is [docs/MASTER_PLAN.md](docs/MASTER_PLAN.md); each finished milestone is a git tag. The active post-M7 work is ordered in [docs/agent/HARDENING_PLAN.md](docs/agent/HARDENING_PLAN.md), with findings tracked in the [consolidated review ledger](docs/reviews/2026-10-13-final/CONSOLIDATED.md).

| Milestone | What it covers | State |
|---|---|---|
| M0 Bootstrap | `.pts` in the forked front end, the C runtime skeleton, the pipeline end to end, the test harness, CI | done (`m0`) |
| M1 Core language | Numbers, integer types, strings, statements, functions, modules | done (`m1`) |
| M2 Data and memory | The GC and object model, classes, object literals, interfaces, arrays, closures, `Map`/`Set`, generics, enums, tuples, destructuring, spread | done (`m2`) |
| M3 The TypeScript feel | Unions, optional chaining, exceptions and stack traces, checked `any`, JSON, the standard built-ins, `console.log` parity, a 300-program corpus compared with Node | done (`m3`) |
| M4 Async on one loop | Generators, iterators, async/await, promises, the libuv event loop and timers, async iteration, `pts:fs`, `pts:path`, `pts:process` | done (`m4`) |
| M5 Parallel | The `parallel` keyword, one event loop per core, the fork-join pool, `.par`, typed arrays, `parallel for` and the guardrail, `pts:sync`, stress tests, parallel benchmarks | done (`m5`) |
| M6 Real programs | RegExp, String methods with RegExps and `normalize`, the documentation refresh, `pts:http`'s server on one loop or every loop, the global `fetch` with `AbortController`, bytes in `pts:fs`, the example apps in [examples/](examples/README.md) (a file hasher, a mandelbrot PNG renderer, a JSON API server on every core, a log analyzer), the fixes from an independent review, a benchmark audit | done (`m6`) |
| M7 Editor support | `pts lsp`, the language server; a VS Code extension; a usability pass: hints on tsc's errors, `pts help` and `pts version`, `pts test` with `pts:test` and `pts:assert`, fixes for the papercuts that two programs written from scratch found, the guide's tutorial | done (`m7`) |

M7 completes the usable prototype, not correctness or production hardening. The owner extended the project to H01–H34: reproduce and correct review findings first, then server/API hardening, verification/tooling, measured performance work and broader TypeScript coverage. H01a records the baseline/consolidation without changing compiler/runtime behavior; H01b's independent defect reproduction is blocked by an upstream content refusal and remains incomplete ([evidence and scope](docs/agent/hardening-evidence/h01.md#blocked-h01b-assignment)).

## Known gaps

Generic function-value instances are pruned only when bounded immutable-source analysis proves them unreachable, including returns of synchronous non-generic factories and static paths through non-escaping inferred literal trees such as `held.inner[0].factory`. Immutable object patterns and dense ordinary-array patterns can also extract literal child containers when every binding leaf selects a container and the complete root, contracts and uses are proven safe. Unknown/`any`, escaped or uncertain container origins stay conservative; tuple-shaped arrays, omitted/default/rest elements and terminal-value bindings do not gain this precision. Broader method/wrapper flow and remaining specialization-growth work are still open; see [the hardening plan](docs/agent/HARDENING_PLAN.md).

Not built yet:

- `https://` for `fetch` (no TLS yet), and streamed request and response bodies.
- Platforms other than x86-64 Linux. The vendored libraries are configured for it, and publishing new objects to other threads relies on x86's store order (D-130).
- npm packages and `node_modules`; `bigint`, decorators, `Symbol` beyond `Symbol.iterator` and `Symbol.asyncIterator`, `WeakRef`, `WeakMap` and `WeakSet` (each a compile error saying it isn't supported yet).

Known issues ([PROGRESS.md](PROGRESS.md#known-issues) has them all):

- The 300-program corpus inventory classifies 269 as compatible, 23 as unsupported, 8 as intentional differences and none as a bug (H10a fixed the last one, `algorithms/lru_cache_map.pts`, D-218). The default suite compares 264 of the compatible programs with Node: feature detection falsely skips five ordinary programs (H01a verified them by hand). The [executed corpus baseline](pts/tests/corpus/REPORT.md) separates these counts; H18 will fix enforcement and execute the other classifications.
- Overrides must keep the overridden method's parameter types.
- RegExp: no `indices` for the `d` flag, no `v` flag, and without `u`, `.` and character classes match an astral character whole.
- Indexing a long non-ASCII string at a large index is O(n); ASCII strings are O(1).
- Parallel: a loop busy with a long timer or a server doesn't take tasks queued on other loops; timers, `pts:fs/promises` and `serve` started from a `.par` callback on a pool thread throw `Error` (`fetch` works there). Allocation-heavy parallel code stops gaining past four threads, because bdwgc refills every thread's free lists under one lock (D-131), and code that does little but allocate can be several times slower on eight threads than on one (D-172). `pts:test`'s `test` and `describe` throw in a `parallel` task or a `.par` callback, before its first `await` (D-186).
- The language server doesn't show the errors that only the lowering finds (most PTS1001 "not supported yet"); `pts build` reports them (D-168).

## Repository layout

The repository is a fork of [microsoft/typescript-go](https://github.com/microsoft/typescript-go). ParallelScript's code is in `cmd/pts/` and `pts/`. Its edits to upstream code are kept minimal, marked `// PTS:` and listed in [docs/FORK.md](docs/FORK.md).

| Path | Contents |
|---|---|
| `cmd/pts/` | The `pts` command |
| `pts/frontend/` | Loads `.pts` programs through the forked TypeScript front end; the hints on tsc's errors |
| `pts/lsp/` | `pts lsp`: the forked language server with ParallelScript's programs and checks |
| `pts/editors/vscode/` | The VS Code extension: the language server for `.pts` files and a grammar that highlights `parallel` |
| `pts/check/` | The semantic pass: the rules tsc doesn't know (numeric types, the guardrail, unsupported features) |
| `pts/numeric/` | The numeric types and their conversion rules |
| `pts/lower/` | Lowering from the checked AST to IR: classes, closures, generics, unions, generator and async state machines, `parallel` and `.par` |
| `pts/ir/` | The typed, structured IR |
| `pts/cgen/` | IR to readable C11 with `#line` directives |
| `pts/driver/` | The pipeline end to end: load, check, lower, emit C, compile |
| `pts/toolchain/` | Builds and caches the runtime and the vendored libraries; compiles and links programs with clang |
| `pts/diagnostic/` | One error format for tsc's and ParallelScript's errors |
| `pts/runtime/` | The C runtime: values, strings, objects, arrays, maps, promises, event loops and the scheduler, the fork-join pool, console, JSON, RegExp (`src/`, `include/pts.h`, C unit tests in `tests/`, Unicode table generators in `gen/`) |
| `pts/lib/` | The prelude (`prelude.d.ts`) and the `pts:` modules, written in ParallelScript: `parallel.pts`, `sync.pts`, `fs.pts`, `path.pts`, `process.pts`, `http.pts`, `test.pts`, `assert.pts` |
| `pts/third_party/` | Vendored C libraries as pinned submodules: bdwgc, libuv, PCRE2, Ryu, libbacktrace |
| `pts/tests/` | `run/` (programs and their expected output), `errors/` (expected compile errors), `corpus/` (realistic programs compared with Node), `stress/` (parallel stress programs); see [pts/tests/README.md](pts/tests/README.md) |
| `pts/bench/` | One directory per benchmark: `main.pts`, its Go twin `main.go` (where Go has one) and, where Node and Bun can't run `main.pts` as it is, a `main.ts` for them; `run.sh` checks that they all print the same and times them, keeping every run's time in `results/` |
| `examples/` | Example programs (a file hasher, a mandelbrot renderer, a JSON API server, a log analyzer), each tested by `pts/tests/examples_test.go`; see [examples/README.md](examples/README.md) |
| `docs/` | The documentation below; `docs/examples/` holds the docs' samples, which run as tests |
| `internal/` and the rest | Upstream typescript-go |

## Documentation

- [docs/README.md](docs/README.md): an index of all the docs.
- [docs/GUIDE.md](docs/GUIDE.md): a tutorial and a tour for TypeScript developers: common questions, what's the same, what's different, the parallel features, testing, common compile errors.
- [docs/SPEC.md](docs/SPEC.md): the language reference, with the differences from Node in §8.
- [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md): the compiler pipeline and the runtime.
- [docs/DECISIONS.md](docs/DECISIONS.md): every design decision, with its reasons.
- [docs/BENCHMARKS.md](docs/BENCHMARKS.md): every benchmark run, losses included.
- [PROGRESS.md](PROGRESS.md) and [docs/MASTER_PLAN.md](docs/MASTER_PLAN.md): where the work stands and what comes next.
- [docs/FORK.md](docs/FORK.md): the edits to upstream code.
- [docs/UPSTREAM_README.md](docs/UPSTREAM_README.md): typescript-go's own README.

The repository keeps upstream's Apache-2.0 [LICENSE](LICENSE) and [NOTICE.txt](NOTICE.txt).
