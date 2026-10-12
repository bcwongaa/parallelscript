# Benchmarks

Honest numbers, losses included (D-O15). Produced by `pts/bench/run.sh` (`make bench`; hyperfine, 2 warm-ups, 10 runs) on the dev machine: x86_64 Linux Mint 22.2, 8 cores, 15 GB RAM, clang 18, Node v24.21.0, Bun 1.4.3, Go 1.26.9. Times are wall-clock means including process start; lower is better. "PTS vs X" is X's time divided by ParallelScript's: above 1× means ParallelScript is faster. Node and Bun run the same source as ParallelScript (D-023). Parallel benchmarks also report speedup over `PTS_THREADS=1`.

## Template

| Benchmark | ParallelScript | Node | Bun | Go | PTS vs Node | PTS vs Go |
|---|---|---|---|---|---|---|
| … | … ms | … ms | … ms | … ms | …× | …× |

## M0

n/a: no benchmarks yet. Baseline numbers for hello world (`pts/tests/run/hello.pts`), measured with hyperfine on 2026-10-11:

| Measure | ParallelScript | Node | Bun |
|---|---|---|---|
| Compile (`pts build`, warm runtime cache) | 202 ms | – | – |
| Binary size (static runtime, bdwgc, libuv; `-g`) | 554 KB | – | – |
| Run, including process start | 1.4 ms | 75.4 ms | 4.3 ms |

## M1

Measured on 2026-10-11 (M1.5). `fib` is recursive Fibonacci of 38 on `number` (Go: `float64`); `fib_int` is the same on ParallelScript's `int` (Go: `int`), which Node and Bun can't run as written.

| Benchmark | ParallelScript | Node | Bun | Go | PTS vs Node | PTS vs Go |
|---|---|---|---|---|---|---|
| fib | 213 ms | 647 ms | 398 ms | 244 ms | 3.03× | 1.14× |
| fib_int | 135 ms | – | – | 212 ms | – | 1.57× |

nbody moved to M2.7 (D-023).

## M2

Measured on 2026-10-11 (M2.8), `pts/bench/run.sh` with 2 warm-ups and 10 runs, on a machine that also runs other services (σ is up to 5% for most rows, 26% for Bun's string_building). New since M1: binary-trees (depth 18, the GC stress test; leaves are their own children until null arrives in M3.1), nbody (5,000,000 steps, energies unrounded until `toFixed` in M3.5) and string building (a million pieces appended with `+=`, template literals and `join`; Go uses a `strings.Builder`). See D-060 and D-061 for how each is written.

| Benchmark | ParallelScript | Node | Bun | Go | PTS vs Node | PTS vs Go |
|---|---|---|---|---|---|---|
| binary_trees | 2144 ms | 1369 ms | 1526 ms | 3176 ms | 0.64× | 1.48× |
| fib | 249 ms | 691 ms | 433 ms | 260 ms | 2.77× | 1.04× |
| fib_int | 126 ms | – | – | 215 ms | – | 1.71× |
| nbody | 791 ms | 869 ms | 858 ms | 509 ms | 1.10× | 0.64× |
| string_building | 1088 ms | 1771 ms | 5855 ms | 376 ms | 1.63× | 0.35× |

Notes:
- binary_trees: bdwgc (conservative, non-moving, non-generational) is the cost; ParallelScript uses 127 MB, Node 264 MB. Collecting only when the program has allocated as much as the heap holds (D-060) took it from about 2.9 s to 2.1 s. A generational or precise collector is M8 work.
- nbody: loop indices are `number` (doubles), so every `bodies[i]` converts and bounds-checks a double; Go's are ints. Integer inference is M8 work; with `int` indices the gap narrows, as fib_int shows.
- string_building: before D-061 every concatenation copied both strings, so this benchmark would have taken minutes.

## M3

Measured on 2026-10-12 (M3.8), `pts/bench/run.sh` with 2 warm-ups and 10 runs, on a machine that also runs other services. New in M3: json (stringify 50,000 typed records, parse them back as `any`, read them, stringify the parsed data plain and indented; three rounds) and word_freq (two million generated words counted in a `Map<string, number>`, sorted by count and word), both first measured at M3.4b (D-081). The M2 benchmarks now run with M3's unions (binary-trees' children are `TreeNode | null`), exceptions and stack checks in place.

| Benchmark | ParallelScript | Node | Bun | Go | PTS vs Node | PTS vs Go |
|---|---|---|---|---|---|---|
| binary_trees | 2381 ms | 1381 ms | 1466 ms | 3261 ms | 0.58× | 1.37× |
| fib | 259 ms | 663 ms | 424 ms | 267 ms | 2.56× | 1.03× |
| fib_int | 132 ms | – | – | 214 ms | – | 1.62× |
| json | 1254 ms | 715 ms | 374 ms | 1754 ms | 0.57× | 1.40× |
| nbody | 841 ms | 995 ms | 860 ms | 500 ms | 1.18× | 0.59× |
| string_building | 1058 ms | 1810 ms | 5811 ms | 366 ms | 1.71× | 0.35× |
| word_freq | 1569 ms | 1513 ms | 967 ms | 599 ms | 0.96× | 0.38× |

Notes:
- Against M2 the single-threaded numbers are within the machine's noise (fib 249 → 259 ms, nbody 791 → 841 ms, string_building 1088 → 1058 ms), except binary_trees (2144 → 2381 ms, about 10% slower): its children became `TreeNode | null` unions in M3.1a (measured about 2.2 s then), so part of the gap is the null tests and narrowing M3 added and part noise; not investigated yet.
- json: parsed data is dynamic (hash-map objects of `any`, D-080), so reading it goes through `pts_dynamic_get`; stringify reads fields in place. The rest is number formatting (Ryu), allocation and marking by bdwgc, and hashing keys. Go's `encoding/json` reflects over structs and builds maps, hence its time.
- word_freq: the `Map` hashes each word's bytes; the sort's comparator is a closure called through its function pointer with tuples; Go's map and `sort.Slice` are tighter.
- Parallelism arrives in M5; every number here is one thread.

## M4

Measured on 2026-10-12 (M4.6), `pts/bench/run.sh` with 2 warm-ups and 10 runs, on a machine that also runs other services. New in M4: async_fib (fib(29) as an async function that awaits both of its subcalls: about 1.7 million async calls and 1.6 million awaits through the microtask queue; Go has no twin) and file_io (2,000 small files written 100 at a time, read back, every tenth stat'ed, the directory listed and removed, three rounds, through `fs/promises`; Go's twin uses a goroutine per file).

| Benchmark | ParallelScript | Node | Bun | Go | PTS vs Node | PTS vs Go |
|---|---|---|---|---|---|---|
| async_fib | 183 ms | 246 ms | 231 ms | – | 1.34× | – |
| binary_trees | 1861 ms | 1408 ms | 1455 ms | 3386 ms | 0.76× | 1.82× |
| fib | 238 ms | 759 ms | 433 ms | 268 ms | 3.19× | 1.13× |
| fib_int | 135 ms | – | – | 214 ms | – | 1.59× |
| file_io | 553 ms | 896 ms | 354 ms | 344 ms | 1.62× | 0.62× |
| json | 1206 ms | 769 ms | 391 ms | 1820 ms | 0.64× | 1.51× |
| nbody | 826 ms | 1001 ms | 916 ms | 546 ms | 1.21× | 0.66× |
| string_building | 1045 ms | 1883 ms | 6123 ms | 447 ms | 1.80× | 0.43× |
| word_freq | 1510 ms | 1488 ms | 1048 ms | 663 ms | 0.99× | 0.44× |

Notes:
- Programs and the runtime now compile with `-falign-functions=32` (D-119): with the same machine code, fib_int had become 35% slower than at M3 only because the grown runtime moved it off a 32-byte boundary.
- async_fib: an async call is a generator frame, an await of an already settled promise one reaction and one microtask (D-112); no allocation per await beyond the frame.
- file_io: each call is a job on libuv's threadpool of 4 (as Node's), its promise settled from the event loop; Bun and Go do these system calls more directly. The machine's ext4 disk, warm page cache.
- binary_trees, json, word_freq: the collector and dynamic data, as at M3; parallelism arrives in M5, every number here is one thread.

## M5.7b: parallel benchmarks

Measured on 2026-10-12 (M5.7b), `pts/bench/run.sh` with 1 warm-up and 5 runs. The machine is a laptop part, an Intel i5-8250U: 4 cores with 2 hardware threads each (8 in all), clocked lower when all cores run, and it runs other services (load average 2 to 5 during these runs), so ×8 means hyperthreads and its timings vary most. ParallelScript runs each program under `PTS_THREADS` = 1, 2, 4 and 8 (D-O06: with 1, `.par` and `parallel` run on the one thread). Node and Bun run `main.ts`, the same program without `.par` and `parallel` (they have no shared-memory threads to compare with), so their column is one thread; Go's twin uses a goroutine per core or per task. "Speedup ×8" is ParallelScript ×1's time divided by ×8's; "PTS ×8 vs X" is X's time divided by ParallelScript ×8's (D-131).

| Benchmark | PTS ×1 | PTS ×2 | PTS ×4 | PTS ×8 | Speedup ×8 | Node | Bun | Go | PTS ×8 vs Node | PTS ×8 vs Go |
|---|---|---|---|---|---|---|---|---|---|---|
| mandelbrot | 1469 ms | 729 ms | 455 ms | 254 ms | 5.78× | 1661 ms | 1614 ms | 235 ms | 6.53× | 0.93× |
| sum_reduce | 3610 ms | 2282 ms | 1740 ms | 1978 ms | 1.83× | 4427 ms | 4781 ms | 688 ms | 2.24× | 0.35× |
| word_count | 1552 ms | 1082 ms | 713 ms | 700 ms | 2.22× | 1110 ms | 1166 ms | 187 ms | 1.58× | 0.27× |
| fan_out | 1045 ms | 495 ms | 310 ms | 320 ms | 3.26× | 1060 ms | 923 ms | 362 ms | 3.31× | 1.13× |

Notes:
- mandelbrot (2400×1600, at most 500 iterations, `.par.forEach` over rows into a Uint16Array): floating-point multiplies and adds, which hyperthreads share well, so ×8 still gains on ×4.
- fan_out (64 tasks of 1 to 8 million terms with a square root and a division, started with `parallel`): tasks go to the least-loaded loop and idle loops take tasks not started yet (D-121); ×8 gains nothing on ×4, because the two hyperthreads of a core share its divider and square-root unit.
- sum_reduce (100 million doubles, 800 MB: filled by `parallel for`, then `.par.sum`, `.par.reduce` and `.par.max`, two rounds): bound by memory bandwidth from ×4 (a pass of `sum` reads 800 MB in about 75 ms), and the 800 MB array is zeroed by one thread when it is made (about 0.4 s), as the collector's memory isn't known to be zero. The fill uses `&`, not `%`: `%` on doubles is C's fmod until integer inference (M8).
- word_count (64 files, 6.4 million words, `.par.map` counting into maps): every word is a new string and a map lookup, so the threads allocate all the time; bdwgc refills each thread's free lists under one lock and stops every thread to collect, so it stops gaining past ×4. Go's substrings share their text and allocate nothing.
- The collector now marks on 2 threads instead of one per core (D-131): with 8 marker threads, programs that allocate were up to 60% slower on one thread (the extra markers burn the cores), and single-threaded binary_trees, json and word_freq are as fast or faster with 2.
