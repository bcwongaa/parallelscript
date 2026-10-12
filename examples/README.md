# Examples

Four small programs that use ParallelScript the way real programs would. Each one is a
single `main.pts`. A Go test in `pts/tests/examples_test.go` builds it, runs it under one
thread and under four, and checks its output against an independent answer, so
`go test ./pts/tests/` (and CI) runs all four.

Build `pts` first (`make build`), then build an example with `pts build`. Every program
uses all cores by default; `PTS_THREADS=n` sets how many threads it uses.

| Example | What it shows | Test |
|---|---|---|
| [`hasher`](hasher/main.pts) | SHA-256 of files with `u32` arithmetic, files read as bytes (`Uint8Array`), one file per `.par.map` job | `TestExampleHasher`: digests against Go's `crypto/sha256` |
| [`mandelbrot`](mandelbrot/main.pts) | Rows computed by `range(…).par.forEach` into a PNG's scanlines; a PNG writer in ParallelScript (stored deflate, CRC-32, Adler-32) | `TestExampleMandelbrot`: Go's `image/png` decodes the file; 1 and 4 threads give the same bytes |
| [`api-server`](api-server/main.pts) | A JSON API answered on every core (`serve({ reusePort: true })`), shared state behind a `pts:sync` `Mutex`, atomic counters, `.par` inside a handler, `request.signal` | `TestExampleAPIServer`: concurrent clients from Go, every route and error, a client that gives up, shutdown |
| [`log-analyzer`](log-analyzer/main.pts) | Access logs parsed with a `RegExp` per line, counted in `Map`s by `.par.map` over pieces of the files, merged into a report | `TestExampleLogAnalyzer`: the report against the counts of Go's own log generator |

The numbers below were measured on the development machine, a 4-core, 8-thread laptop
(i5-8250U) that also runs other services, so they vary by 5 to 10%. They are wall-clock
times of whole runs, file reading included, with the files in the page cache.

## hasher

```sh
pts build examples/hasher/main.pts -o hasher
./hasher src/ README.md
```

It prints `<digest>  <path>` for each file, as `sha256sum` does, and a summary on stderr.
A directory stands for all the files under it, in sorted order. 200 MB in 8 files take
5.8 s on one thread and 2.1 s on four. Node runs the same SHA-256 code in 4.6 s on its
one thread.

## mandelbrot

```sh
pts build examples/mandelbrot/main.pts -o mandelbrot
./mandelbrot set.png 1920 1280
```

It renders 1920×1280 pixels in 2.07 s on one thread and 0.31 s on eight (6.8×). The files
are byte for byte the same.

## api-server

```sh
pts build examples/api-server/main.pts -o api-server
./api-server 8080
curl -X POST -d '{"name":"apple","price":1.5}' localhost:8080/items
curl localhost:8080/items/1
curl 'localhost:8080/primes?below=10000000'
curl -X POST localhost:8080/shutdown
```

The routes are `GET`/`POST /items`, `GET`/`DELETE /items/<id>`, `GET /primes?below=n`,
`GET /slow?ms=n`, `GET /stats` and `POST /shutdown`. Errors come back as JSON with their
status (400, 404, 405). Every event loop accepts connections. The loops share the items
through a `Mutex<Map<number, Item>>`, so each request takes the lock briefly.

With `oha -z 5s -c 64` on the same machine, `GET /items/1` was answered 51,000 times a
second on one loop and 99,000 times on four. `POST /items` (a write under the lock) went
from 37,000 to 74,000 a second. `GET /primes?below=10000000` counts primes with
`range(…).par.count` on every core: 2.4 s on one thread, 0.61 s on four.

## log-analyzer

```sh
pts build examples/log-analyzer/main.pts -o log-analyzer
./log-analyzer /var/log/nginx/access.log old-logs/
```

It reads lines in the Common Log Format, or the Combined format, whose extra fields it
ignores. It prints the lines and the malformed ones, the requests, the clients and the
bytes sent, then requests by status, the ten busiest paths, and requests and bytes per
hour. The report is in the same form as this one:

```
4 files, 1,600,000 lines (15,854 malformed)
1,584,146 requests from 1,000 clients, 36292.3 MB sent

status    requests   share
200      1,055,727   66.6%
301        132,179    8.3%
…
```

On 4 files of 400,000 lines (146 MB), it took 2.79 s on one thread, 1.55 s on two,
1.16 s on four and 1.26 s on eight. Node runs the same program, with `.map` in place of
`.par.map`, in 1.72 s. On one thread, ParallelScript spends most of its time in PCRE2's
interpreter, where Node's regexes are compiled to machine code. Past four threads it
stops gaining, because each line allocates about ten objects and bdwgc refills every
thread's free lists under one lock (see D-131 in `docs/DECISIONS.md`).
