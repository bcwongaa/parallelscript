# ParallelScript review (grok)

Snapshot: detached `f2323ccf4` in `/home/bryan/projects/pts-review-grok`. No tracked files were edited. Programs and probes are in `review-grok-probes/`. Commands used `GOMAXPROCS=2`, `PTS_CACHE_DIR=$HOME/.cache/pts-review-grok`, `PTS_HOME` this worktree, and `PTS_THREADS` of 2 or 4. `pts version` is `pts 0.7.0 (typescript-go 7.0.2)`.

## Verdict

**Yes, with caveats.** This is a usable language for real small programs, not a demo. A CLI, a JSON notes service plus a fetch client, and a parallel directory indexer all compiled and did the job. Plain-TypeScript pieces matched Node. The PTS suite is green. I did not get a segfault or abort from a program the compiler accepted.

The caveats are real. One valid TypeScript program panics the compiler. Building a normal `fetch` options object is rejected, so the HTTP client had to be written twice. `instanceof` on a generic class still throws, and that is the corpus's only classified bug. A few Node differences remain, some documented, and they will surprise someone who treats "plain TypeScript prints what Node prints" as absolute. There is no HTTPS and no npm. I did not remeasure the benchmark table.

## Scorecard

| Area | Status | Evidence |
|---|---|---|
| M0–M7 as a whole | usable with gaps | Suite below; three programs ran; known gaps in README/PROGRESS still match what I hit |
| Plain TS semantics | usable with gaps | Unicode, closures, integers, async exceptions matched Node where probed; unset fields and one yield side effect do not |
| Parallelism | solid (sampled) | Indexer sequential and `.par` maps matched; `.par.reduce` initial-value bug from the last review is gone; PTS3001 message is clear |
| HTTP / fetch | usable with gaps | Notes API passed; malformed requests get 400/431; `RequestInit` variables do not compile; slow half-close still drops the response |
| `pts test` / errors | solid | Ledger tests: 5 pass. PTS1001/PTS2002/PTS3001 name the construct. The fetch PTS1001 is accurate but very hard to read |
| LSP | solid (pull) | `textDocument/diagnostic` returned PTS2002 and PTS3001. A file that still has a tsc error returned TS2322 and withheld the PTS checks |
| VS Code packaging | unverified | `pts/editors/vscode/` has sources and a lockfile; no `node_modules`. `make vscode` was not run |
| Runtime safety | usable with gaps | No crash on the probes that compiled, including 40 connections and a 500k recursion. The "a race cannot crash you" claim was not stress-tested |
| Fork | solid | `git diff --stat typescript/v7.0.2 HEAD -- internal`: 22 files, +482/−26 (was 12 files, +403/−17 at the last review) |

Suite, this session: `go test -count=1 -p 1 -timeout 40m ./pts/... ./cmd/pts/` exited 0. Tested packages all `ok`, including `pts/tests` in 554s, `pts/toolchain` in 21s, `cmd/pts` in 19s. Log: `/tmp/pts-review-grok/suite2.log`.

Corpus classifier (`pts/tests/corpus/report.sh`), not a fresh 300-way execution: **267 of 300** Node-identical, 8 intentional differences, 24 unsupported (PTS1001), **1 failing bug** (`instanceof` on `MapLRU`). That matches the README's 267 figure. I did not re-run the programs behind the other 33 labels.

Startup 1.2 ms and fib 2.5× Node were **not** remeasured. The host was running other reviewers' suites. README and `docs/BENCHMARKS.md` quote the same fib and startup cells; that is consistency, not a reproduction.

## New-user experience

Quick start worked: `make build`, then `pts run docs/examples/hello.pts` printed `Hello, ParallelScript`.

**Ledger CLI** (`review-grok-probes/programs/ledger/`, lib 256 lines + CLI 157; the test file is another 54). CSV with quotes, escaped quotes, a newline inside quotes, leap days, money to cents. `pts:fs` `readFileSync` and `process.argv.slice(2)`. It compiled the first time. On `expenses.csv`:

```
line 10: bad date "not-a-date"
line 11: bad amount "1.009"
8 expenses, total $2804.75
```

Exit code 1, which is the CLI's choice when the file has bad rows. `pts test` on `ledger.test.pts`: 5 pass, about 4.6s, with Node-like `actual`/`expected` text. The same tests under Node (`node:test` on a `.ts` copy of the lib) also passed 5.

**Notes service** (`programs/notes/server.pts` 267, `client.pts` 122). `serve({port: 0, hostname: "127.0.0.1", reusePort: true})`, a `Mutex` around the database, `Response.json`. The server compiled immediately. The client did not. A `RequestInit` in a variable, and a conditional `headers` object, are both PTS1001 (`31_fetch_init.pts:9` and `:18`). The message says the value's `body` is not provided "with the same types" as `body?: null | string | undefined`. Inline object literals in each branch of a ternary compile (`client.pts:37–43`). After that workaround, a fresh server (port 40939) passed every check: create, tag filter, patch, delete, 400, 404, 405.

**Indexer** (`programs/indexer/indexer.pts`, 219). Walks a directory, tokenizes with `\p{L}\p{N}`, compares a sequential count with `.par.map`, and bumps an `AtomicInt`. `--demo` (4 threads): `5 files, 71 lines, 320 words`, `café` 45, `naïve` 27, `東京` 2, then `maps match, order ok, seen 5`, exit 0. About 2 ms of parallel work on that tiny corpus; not a benchmark.

`argv` is the executable path twice, so `slice(2)` is the portable argument list. There is no `URL` global; the server parses the path with `indexOf`. Both are learnable. The fetch rejection is the one that stops a TypeScript developer mid-program.

## Findings

1. **High. The compiler panics on a valid program.** `review-grok-probes/probes/30_iterator_getter_panic.pts` is a `for…of` over an `any` iterator whose `next()` returns `{ get done() { return true }, get value() { throw … } }`. Node reads `done` and does not enter the loop. `pts build` dies with `panic: cgen: no check for void` at `pts/cgen/checks.go:305` (exit 2), not a PTS diagnostic. A getter on an ordinary `any` object compiles; the panic is this iterator-result shape. **Fix:** give `void` a check, or reject the construct with PTS1001. Add this file as a regression. Until it compiles, the old "read `done` before `value`" bug cannot be re-checked.

2. **Medium. A compound assignment still reads its getter after `yield`.** `06_yield_order.pts`. `box.value + (yield 0)` is 11 on both PTS and Node (the old "12" bug is gone). `left.value += yield 0` calls the getter **0** times before the first `next()` returns on PTS, and **1** time on Node. The final number is 11 either way. SPEC §8 already names this remainder (`docs/SPEC.md` around the D-145 paragraph: a compound target read through a getter still happens after the suspension). **Fix:** snapshot that read, and test it. The numeric result hiding the side effect is why a test of the value alone stays green.

3. **Medium. `instanceof` a generic class throws.** `17_instanceof_generic.pts` prints `threw Expected MapLRU<any, any>, got MapLRU<string, number>`. Same bug the corpus classifier files under `algorithms/lru_cache_map.pts` (D-149). It is the only corpus row marked failing. **Fix:** the one already written in PROGRESS: narrow with the type arguments the object actually has, not `MapLRU<any, any>`.

4. **Medium. A `fetch` options object in a variable does not compile.** `31_fetch_init.pts:9` and `:18`, PTS1001. `body: string` is not accepted where `body?: null | string` is expected, and `headers: undefined | { "content-type": string }` is not accepted as `Headers | Record<string, string> | …`. Two inline `fetch` calls work. This is what blocked the notes client. **Fix:** if every present field is a subtype of `RequestInit`'s field, accept the object. And shorten that diagnostic to one sentence that names the field and the missing constituent (`null`).

5. **Medium. A method with a default does not implement an interface method that requires the argument.** `27_method_iface.pts:9`: `class Sq implements Labeller` with `label(suffix = "sq")` fails PTS1001, "whose `label` it doesn't provide with the same types". Calling `new Sq().label()` with no argument is fine; the failure is the implements check. Known, and still there. **Fix:** a default parameter should satisfy a required parameter of the same type, or the error should say "default parameter" instead of the structural essay.

6. **Low. A client that half-closes during a slow handler gets no response.** Known (D-163): a half-close is treated as the client leaving. Confirmed, and it depends on timing. `GET /half` (handler returns immediately) still returned `HTTP/1.1 200` after `SHUT_WR`. `GET /slow` (1.5s `setTimeout`) returned nothing. Other HTTP probes were fine: garbage, missing version, and a bad header got 400; a 200KB header got 431; POST echoed; 40 connections all got 200; a slow reader pulled 5100 bytes of `/big` and the process stayed up. A request that advertises `Content-Length: 5` and sends 2 bytes produced no response within 2s, which is the server waiting for the rest of the body, not a wedge (later requests succeeded). **Fix:** none required if the nginx-like choice stays. Say it next to the HTTP sample in the guide, not only in PROGRESS.

7. **Low. Unset primitive fields are zero values, and string methods succeed.** `26_unset_primitives.pts`, class fields `name!: string`, `count!: number`, `flag!: boolean`. PTS prints `""`, `0`, `false`, and `name.length` is 0. Node prints `undefined` and throws `TypeError` on `.length` and `.toUpperCase()`. Documented (`docs/SPEC.md` zero-value / D-141 paragraphs, including `""`, 0, and false). **Fix:** keep it, but put a one-line example in the guide's "differences" section. Silent `.length === 0` is a worse footgun than a throw.

Not findings: `await` in a `while` condition is PTS1001 with a clear message (`25_await_while.pts:5`). `parallel (() => 7)` evaluates to the function, which SPEC says is correct. Integer edges wrapped, and `1/0` threw `RangeError` (no SIGFPE on `INT64_MIN / -1`). Recursion of 500_000 returned; 1_000_000 threw `RangeError: Maximum call stack size exceeded` (`29_stack_overflow.pts`). Unicode matched Node (`12_unicode.pts`). `fetch("https://…")` throws `https: is not supported yet`.

## Previous review (2026-10-12, M0–M5)

| # | Status | This session |
|---|---|---|
| 1 Foreign-timer SIGSEGV | fixed | `01_foreign_timer.pts` prints `cleared`, exit 0 |
| 2 Captured-union SIGSEGV | fixed | `02_union_capture.pts` throws `TypeError: Expected string, got number` at line 9, exit 1. No segfault. The throw (Node would print `undefined`) is the documented re-check |
| 3 Captured TDZ SIGSEGV | fixed | `03_tdz.pts` prints `ReferenceError` |
| 4 `toString(): any` SIGSEGV | fixed | `04_tostring.pts` prints `7,7` |
| 5 Thenables | fixed | `05_thenable.pts` matched Node, including a throwing `then` getter rejecting instead of throwing out of `Promise.resolve` |
| 6 Yield snapshots | partly fixed | The `+ (yield)` value is 11, matching Node. Getter timing is finding 2 |
| 7 Iterator order | partly fixed | Close-vs-body matches Node (`07b_iterator_close.pts`: `caught body` on both). The done-before-value getter program is finding 1, so that half was not re-run |
| 8 Generic factory `Expected Box, got Box` | fixed | `08_generic_factory.pts` prints `box 9` |
| 9 `parallel function` rejected; `.par.reduce` initial | fixed | Function expressions compile and yield the function (SPEC). `10_par_reduce.pts`: sequential and parallel both `7998010` |
| 10 Shared-memory contract unproven | not reconfirmed | `18_map_race.pts` did not crash in one short run. That is not a proof. TSan of generated programs was not run here. `pts/toolchain` passing in 21s does not close this |
| 11 Green tests hiding skips | partly fixed | `report.sh` now separates identical / intentional / unsupported / 1 bug. The bug is still open (finding 3). I did not re-read the skip harness |

## Top 10 next actions

1. Stop `cgen` panicking on `void` (`checks.go:305`); land `30_iterator_getter_panic.pts`, then re-check done-before-value.
2. Accept a subtype of `RequestInit`, and rewrite that PTS1001 so a normal client compiles.
3. Fix generic `instanceof` (the corpus's only failing row).
4. Let a default parameter satisfy an interface method, with a short error if it still cannot.
5. Snapshot a compound-assignment getter before `yield` (finding 2). The other documented hole, `o.m(yield v)` looking up `m` after the yield, is the same pass.
6. Per-thread GC heaps. This is the project's own scaling item. I did not measure allocation, so I am not confirming the bottleneck, only not dropping it.
7. HTTPS, or a guide example that fails in the way `22_https_and_refused.pts` does, so people stop discovering it from a `TypeError`.
8. One sentence in the HTTP chapter on half-close (finding 6).
9. Friendlier structural PTS1001s in general. The parallel-async hint (call the function, don't pass it) is the standard; the fetch message is the problem.
10. Package the VS Code extension once on a clean tree (`make vscode` was not run here), and add the iterator-getter panic to the suite so the next green run cannot miss it.

## What I did not do

No full upstream `go test`. No benchmark rerun. No VS Code vsix. No multi-minute race stress. Probes live under `review-grok-probes/` and can be rebuilt with `pts-bin/pts build`.
