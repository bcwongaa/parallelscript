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

## M3 (in progress)

Measured on 2026-10-12 (M3.4b), `pts/bench/run.sh json word_freq` with 2 warm-ups and 10 runs (σ under 5% except Bun's, up to 16%). New: json (stringify 50,000 typed records, parse them back as `any`, read them, stringify the parsed data plain and indented; three rounds) and word_freq (two million generated words counted in a `Map<string, number>`, sorted by count and word). See D-081. The full M3 table comes with M3.8.

| Benchmark | ParallelScript | Node | Bun | Go | PTS vs Node | PTS vs Go |
|---|---|---|---|---|---|---|
| json | 1145 ms | 727 ms | 481 ms | 1784 ms | 0.63× | 1.56× |
| word_freq | 1629 ms | 1292 ms | 1139 ms | 567 ms | 0.79× | 0.35× |

Notes:
- json: parsed data is dynamic (hash-map objects of `any`, D-080), so reading it goes through `pts_dynamic_get`; stringify reads fields in place. The rest is number formatting (Ryu), allocation and marking by bdwgc, and hashing keys. Go's `encoding/json` reflects over structs and builds maps, hence its time.
- word_freq: the `Map` hashes each word's bytes; the sort's comparator is a closure called through its function pointer with tuples; Go's map and `sort.Slice` are tighter.
