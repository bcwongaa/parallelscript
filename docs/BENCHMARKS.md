# Benchmarks

Honest numbers, losses included (D-O15). Produced by `pts/bench/run.sh` (`make bench`; hyperfine, 2 warm-ups, 10 runs) on the dev machine: x86_64 Linux Mint 22.2, Intel Core i5-8250U (4 cores, 8 hardware threads), 15 GB RAM, clang 18, Node v24.21.0, Bun 1.4.3, Go 1.26.9. Times are wall-clock means including process start (the `startup` row shows what that costs); lower is better. Since M6.5a (D-167) the runner gives mean ± standard deviation over runs split into interleaved rounds, keeps every run's time as JSON in `pts/bench/results/` (a milestone's in `pts/bench/results/mN/`), and labels rows: "warm cache" for benchmarks that read files the page cache holds, so they measure the file system's code paths, not the disk. "PTS vs X" is X's time divided by ParallelScript's: above 1× means ParallelScript is faster. Node and Bun run the same source as ParallelScript (D-023). Parallel benchmarks also report speedup over `PTS_THREADS=1`.

## Summary

The latest numbers for every benchmark. All but the HTTP servers, spectral_norm and startup come from the M5 section below (M5.8, measured 2026-10-12 on a busy machine); the servers come from the M6.2b section, spectral_norm and startup from M6.5a. M6.5a made several twins do the same work as `main.pts` (D-167): the M5 rows of fan_out, file_io, json, mandelbrot, string_building, sum_reduce, word_count and word_freq were measured with the old twins, which did less work in Node's sum_reduce and mandelbrot and in several Go twins; M6.6's table is the first with the audited twins. The "vs Bun" columns are computed here from the same times; the milestone tables don't list them. The milestone sections below are the history, unchanged.

Single-threaded:

| Benchmark | ParallelScript | Node | Bun | Go | PTS vs Node | PTS vs Bun | PTS vs Go |
|---|---|---|---|---|---|---|---|
| async_fib | 162 ms | 259 ms | 272 ms | – | 1.60× | 1.68× | – |
| binary_trees | 1774 ms | 1379 ms | 1598 ms | 3126 ms | 0.78× | 0.90× | 1.76× |
| fib | 254 ms | 670 ms | 505 ms | 272 ms | 2.64× | 1.99× | 1.07× |
| fib_int | 132 ms | – | – | 235 ms | – | – | 1.78× |
| file_io | 519 ms | 825 ms | 361 ms | 350 ms | 1.59× | 0.70× | 0.67× |
| json | 1119 ms | 765 ms | 419 ms | 1770 ms | 0.68× | 0.37× | 1.58× |
| nbody | 810 ms | 853 ms | 870 ms | 592 ms | 1.05× | 1.07× | 0.73× |
| spectral_norm | 2676 ms | 1954 ms | 2235 ms | 1433 ms | 0.73× | 0.84× | 0.54× |
| startup (process start only) | 1.2 ms | 74 ms | 4.7 ms | 1.3 ms | 61.48× | 3.93× | 1.11× |
| string_building | 1096 ms | 1760 ms | 6632 ms | 403 ms | 1.61× | 6.05× | 0.37× |
| word_freq | 1988 ms | 1769 ms | 1474 ms | 754 ms | 0.89× | 0.74× | 0.38× |

Parallel: ParallelScript under `PTS_THREADS` = 1, 4 and 8; Node and Bun run the same program without `.par` and `parallel`, on one thread; Go uses goroutines.

| Benchmark | PTS ×1 | PTS ×4 | PTS ×8 | Speedup ×8 | Node | Bun | Go | PTS ×8 vs Node | PTS ×8 vs Bun | PTS ×8 vs Go |
|---|---|---|---|---|---|---|---|---|---|---|
| fan_out | 935 ms | 347 ms | 343 ms | 2.72× | 1011 ms | 924 ms | 312 ms | 2.95× | 2.69× | 0.91× |
| mandelbrot | 1525 ms | 521 ms | 328 ms | 4.65× | 1649 ms | 1661 ms | 321 ms | 5.03× | 5.06× | 0.98× |
| sum_reduce | 4080 ms | 2886 ms | 2390 ms | 1.71× | 5423 ms | 6649 ms | 907 ms | 2.27× | 2.78× | 0.38× |
| word_count | 2073 ms | 948 ms | 1737 ms | 1.19× | 2275 ms | 1360 ms | 220 ms | 1.31× | 0.78× | 0.13× |

HTTP servers, in requests per second (higher is better; `pts/bench/serve.sh` with oha): ParallelScript on 1 and 4 loops (`reusePort`), Node and Bun on one thread, Go on every core.

| Benchmark | PTS ×1 | PTS ×4 | Node | Bun | Go | PTS ×4 vs Node | PTS ×4 vs Bun | PTS ×4 vs Go |
|---|---|---|---|---|---|---|---|---|
| http_hello | 56,818 | 84,739 | 20,939 | 59,753 | 69,483 | 4.05× | 1.42× | 1.22× |
| http_json | 34,096 | 56,132 | 15,592 | 34,189 | 48,436 | 3.60× | 1.64× | 1.16× |

Wins and losses:
- HTTP: one loop is 2.7× Node on hello world and 2.2× on JSON, and just behind or level with Bun (0.95×, 1.00×); four loops beat Go's net/http by 16 to 22%.
- Against Node: ParallelScript is faster on async_fib, fib, file_io and string_building, and barely on nbody (1.05×). It is slower on binary_trees, json, spectral_norm and word_freq. On 8 threads it beats Node's single thread on all four parallel benchmarks.
- Against Bun: faster on async_fib, fib, nbody and string_building; slower on binary_trees, file_io, json, spectral_norm and word_freq. On 8 threads, faster on fan_out, mandelbrot and sum_reduce, slower on word_count (its 4-thread time, 948 ms, does beat Bun's 1360 ms).
- Against Go: faster on binary_trees, fib, fib_int and json; slower on file_io, nbody, spectral_norm, string_building and word_freq. On 8 threads, close on mandelbrot (0.98×) and fan_out (0.91×), far behind on sum_reduce (0.38×) and word_count (0.13×).
- The M5.7b run, an hour before M5.8 on a calmer machine, scaled better: 8-thread speedups of 5.78× (mandelbrot), 3.26× (fan_out), 2.22× (word_count) and 1.83× (sum_reduce).

What's slow and why: programs that allocate a lot (binary_trees, json, word_freq, word_count) pay for bdwgc, a conservative, non-moving, non-generational collector, and with threads bdwgc refills each thread's free lists under one lock and stops every thread to collect, so allocation-heavy parallel code stops gaining past 4 threads (D-131; the fixes, allocating less through escape analysis or a collector with per-thread heaps, are M8 work). nbody trails Go because its loop indices are doubles until integer inference (M8), and sum_reduce is bound by memory bandwidth and by one thread zeroing its 800 MB array.

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

## M5

Measured on 2026-10-12 (M5.8), `pts/bench/run.sh` with 2 warm-ups and 10 runs, on the machine of the M5.7b section above, which was busier during this run than an hour earlier: Node's word_count took 2275 ms here against 1110 ms in M5.7b's run, and every parallel row lost to it (mandelbrot 4.65× on 8 threads here, 5.78× there). Both runs are kept as measured; the M5.7b table is the calmer one. New in M5: the four parallel benchmarks (D-131). The single-threaded rows now run with bdwgc marking on 2 threads instead of one per core.

| Benchmark | ParallelScript | Node | Bun | Go | PTS vs Node | PTS vs Go |
|---|---|---|---|---|---|---|
| async_fib | 162 ms | 259 ms | 272 ms | – | 1.60× | – |
| binary_trees | 1774 ms | 1379 ms | 1598 ms | 3126 ms | 0.78× | 1.76× |
| fib | 254 ms | 670 ms | 505 ms | 272 ms | 2.64× | 1.07× |
| fib_int | 132 ms | – | – | 235 ms | – | 1.78× |
| file_io | 519 ms | 825 ms | 361 ms | 350 ms | 1.59× | 0.67× |
| json | 1119 ms | 765 ms | 419 ms | 1770 ms | 0.68× | 1.58× |
| nbody | 810 ms | 853 ms | 870 ms | 592 ms | 1.05× | 0.73× |
| string_building | 1096 ms | 1760 ms | 6632 ms | 403 ms | 1.61× | 0.37× |
| word_freq | 1988 ms | 1769 ms | 1474 ms | 754 ms | 0.89× | 0.38× |

| Benchmark | PTS ×1 | PTS ×2 | PTS ×4 | PTS ×8 | Speedup ×8 | Node | Bun | Go | PTS ×8 vs Node | PTS ×8 vs Go |
|---|---|---|---|---|---|---|---|---|---|---|
| fan_out | 935 ms | 492 ms | 347 ms | 343 ms | 2.72× | 1011 ms | 924 ms | 312 ms | 2.95× | 0.91× |
| mandelbrot | 1525 ms | 861 ms | 521 ms | 328 ms | 4.65× | 1649 ms | 1661 ms | 321 ms | 5.03× | 0.98× |
| sum_reduce | 4080 ms | 2978 ms | 2886 ms | 2390 ms | 1.71× | 5423 ms | 6649 ms | 907 ms | 2.27× | 0.38× |
| word_count | 2073 ms | 1213 ms | 948 ms | 1737 ms | 1.19× | 2275 ms | 1360 ms | 220 ms | 1.31× | 0.13× |

Notes:
- Single-threaded rows against M4: async_fib 183 → 162 ms, binary_trees 1861 → 1774 ms, json 1206 → 1119 ms (the collector's 2 markers, D-131); word_freq 1510 → 1988 ms and nbody, string_building within the run's noise (Node's word_freq also went from 1488 to 1769 ms).
- Parallel rows: see the M5.7b notes. word_count's ×8 (1737 ms, slower than ×4) is allocation-heavy code meeting a loaded machine: every thread allocates a string per word, and bdwgc refills free lists under one lock and stops all threads to collect.

## M6.2b: HTTP servers

Measured on 2026-10-12 with `pts/bench/serve.sh` (`make bench-http`, D-137): each server is loaded by oha with 64 keep-alive connections, 2 s of warm-up, then 10 s measured; the numbers are requests per second, higher is better, and "PTS ×4 vs X" is ParallelScript's rate on 4 loops divided by X's. ParallelScript serves with `reusePort: true` under `PTS_THREADS` 1, 2 and 4; Node (`node:http`) and Bun (`Bun.serve`) are one process each, on one thread; Go's `net/http` uses every core. `http_hello` answers `GET /` with a 13-byte text; `http_json` takes a POSTed order of three items as JSON, parses it into typed records, totals it and answers JSON. oha runs on the same machine and takes 2 to 3 of its 8 hardware threads, and the load average was 4 to 5, so 4 loops is as far as the server can usefully go here.

| Benchmark (requests/s) | PTS ×1 | PTS ×2 | PTS ×4 | Speedup ×4 | Node | Bun | Go | PTS ×4 vs Node | PTS ×4 vs Go |
|---|---|---|---|---|---|---|---|---|---|
| http_hello | 56,818 | 70,329 | 84,739 | 1.49× | 20,939 | 59,753 | 69,483 | 4.05× | 1.22× |
| http_json | 34,096 | 42,268 | 56,132 | 1.65× | 15,592 | 34,189 | 48,436 | 3.60× | 1.16× |

Notes:
- A second run right after gave the same picture: hello 49,905 / 66,784 / 84,751 (Node 21,064, Bun 56,661, Go 69,585), JSON 32,195 / 48,564 / 55,826 (Node 15,152, Bun 33,660, Go 48,606).
- On one loop ParallelScript is 2.7× Node on hello world and 2.2× on JSON; against Bun it is 0.95× on hello world and level on JSON (0.88× and 0.96× in the second run). On 4 loops it is ahead of Go's every-core server by 16 to 22%.
- Before D-137's fixes, one loop served 26k hello-world requests a second and four loops 23k: every regex match allocated through the collector's global lock, and `pts:http` checked each header with regexes. An unloaded quick run after the fixes measured 54k, 93k and 114k on 1, 2 and 4 loops, which shows how much oha and the background load take in the table's run.
- What still limits the scaling: each request allocates its `Request`, `Headers`, `Response`, strings and promises, and bdwgc refills threads' free lists under one lock (D-131).

## M6.5a: the benchmark audit

Measured on 2026-10-13 with the audited runner (`RESULTS=pts/bench/results/m6.5a pts/bench/run.sh startup spectral_norm`; 2 warm-ups per round, 10 runs in 2 interleaved rounds; the JSON is in `pts/bench/results/m6.5a/`). Only the new benchmarks were measured; M6.6 runs them all.

| Benchmark | ParallelScript | Node | Bun | Go | PTS vs Node | PTS vs Go |
|---|---|---|---|---|---|---|
| startup (process start only) | 1.2 ± 0.1 ms | 74 ± 6 ms | 4.7 ± 0.1 ms | 1.3 ± 0.1 ms | 61.48× | 1.11× |
| spectral_norm | 2676 ± 8 ms | 1954 ± 43 ms | 2235 ± 64 ms | 1433 ± 15 ms | 0.73× | 0.54× |

Notes:
- `startup` prints one line. Every other row includes this cost: about 74 ms for Node, 5 ms for Bun, about 1 ms for ParallelScript and Go.
- `spectral_norm` (the Benchmarks Game's, n = 4000) is a loss: 0.73× Node, 0.54× Go. Its inner loops read the module-level `const N`, which a function checks for readiness and reloads at every read (D-021); with the typed-array reads being relaxed atomics (D-143), clang hoists neither out of the loop. Written as a literal, the same program runs in 2.0 s. A literal constant that is ready before any function can run could be emitted as a C constant (M8).
- What the audit changed in the twins (D-167): Node's sum_reduce made one pass for the sum and the max, and Node's mandelbrot counted with `filter(…).length`; Go's mandelbrot, word_count and fan_out fused a total into another loop; Go's file_io sorted the directory listing and counted bytes without making strings; Go's word_freq and the json and string_building checksums used integers where JS uses doubles. Kept as differences of the languages: Go's one-lookup `m[k]++` against a JS Map's `get` and `set`, Go substrings sharing their bytes, `encoding/json` decoding into structs, Go's `strings.Builder`.

