# Pre-decisions made by the orchestrator on the owner's behalf (2026-10-11)

The owner delegated every decision they would otherwise be asked about. These entries carry the owner's authority and rank just below the locked brief decisions. In M0.1, copy them into `docs/DECISIONS.md` as entries `D-O01…`, marked **decided by orchestrator for owner**.

- **O01 · `.pts` extension (owner instruction).** Source files use `.pts`, as in the brief. (The owner first asked for `.ps`, then reverted to `.pts` on 2026-10-11 before M0.5.) `.gitattributes` maps `*.pts` to TypeScript for GitHub highlighting. The VS Code extension registers `.pts` with language id `parallelscript`. Declaration files stay `.d.ts`. Module resolution: `import "./x"` tries `x.pts` first, then the TS extensions.
- **O02 · Repo and git flow.** `origin` is `bcwongaa/parallelscript`. The owner created it public; leave it that way. Commit directly to `main`, keep it green, and tag each milestone. The PR flow from LLM-dev-guides does not apply; the brief overrides it.
- **O03 · Telegram cadence.** One message per finished milestone, plus proposals, plus rare questions. Use thread `telegram:-1004441160190:228`. The session driver sends its own operational alerts (model switches, stalls).
- **O04 · Differential-test contract.** Compare **stdout and the exit code** exactly. stderr is not compared, but an uncaught error must go to stderr and exit with code 1, like Node. Run Node with `--experimental-strip-types` (or the default type stripping in Node 24) on a temp copy renamed to `.ts`. Fall back to `tsc` emit for enums and parameter properties.
- **O05 · Disambiguating `parallel`.**
  - The parser treats `parallel` as the operator only when the next token is on the same line and starts an expression **other than** `(`, `[`, `.`, `?.`, a template, `=`, an operator, `,`, `;`, `)` or `}`. Examples: `parallel foo(x)`, `parallel new X()`, `parallel async () => …`, `parallel await x`, `parallel "lit"`.
  - `parallel for (` is the loop statement.
  - `parallel(expr)` parses as a normal call. The checker then reinterprets it as the operator **only if** no binding named `parallel` is in scope and it has exactly one argument.
  - This keeps existing code that uses `parallel` as an identifier working, and `await parallel(f(x))` still reads naturally.
- **O06 · `PTS_THREADS=1`.** `parallel e` still creates a new task, on the same loop. `.par` runs sequentially on the caller. Programs stay correct with one thread.
- **O07 · Uncaught errors and rejections.**
  - An uncaught exception prints `Uncaught <Name>: <message>` and, once available, a `.pts` stack trace to stderr, then exits with code 1.
  - An unhandled rejection behaves like Node's default `--unhandled-rejections=throw`.
  - Until M3 exceptions land, runtime errors (such as integer division by zero) abort with the same message format and exit code 1.
- **O08 · BigInt.** Out of scope for v1. Using a `bigint` type or literal gives a clear compile error, "BigInt is not supported yet", logged as a documented difference.
- **O09 · RegExp.**
  - Needed for `split`/`replace`/`match` and for the M6 log-analyzer example, so it moves into scope as M6 task M6.1.
  - Vendor **PCRE2** (BSD licence) as a pinned submodule.
  - Translate JS regex syntax and flags (`g i m s u y`) to PCRE2 options.
  - Until then, string-only overloads of `split`/`replace`/`replaceAll` work, and a regex literal is a compile error saying "RegExp arrives in M6".
- **O10 · `fetch`.**
  - In M6, `fetch` supports `http://` (HTTP/1.1, chunked, JSON/text bodies).
  - `https://` is a stretch goal: vendor mbedTLS if time allows. Until then it rejects with a clear `TypeError`.
- **O11 · Runtime distribution.**
  - `pts` finds its runtime sources relative to its own executable or via `PTS_HOME`.
  - It builds the vendored bdwgc, libuv and PCRE2 once into `~/.cache/pts/<version-hash>/`.
  - It compiles user programs against those cached static libs, so a rebuild of a user program never rebuilds the vendored libraries.
- **O12 · Definition of "usable" (the stop condition).** M0 through M7 are complete: tests green, examples working, README written, VS Code extension packaged. M8 and the stretch goals happen only if the owner extends the run.
- **O13 · Model fallback.**
  - Sessions may run on claude-opus-5-5 (preferred), gpt-6.1-sol or grok-4.7. The driver picks the model.
  - Every session follows the same rules whatever its model. A session on a fallback model should prefer well-specified tasks, and must leave thorough notes in `PROGRESS.md`.
- **O14 · `Symbol`.** Only `Symbol.iterator` and `Symbol.asyncIterator` are supported (per the brief). Any other `Symbol` use is a compile error naming the feature.
- **O15 · Benchmarks are not gates.** Record honest numbers, including the losses. Never game a benchmark, for example by special-casing it in the compiler.
- **O16 · `console.log` parity.** Match `util.inspect` defaults:
  - depth 2
  - `breakLength` 80, with the 72-char/6-entry heuristics for arrays and objects
  - numeric-array grouping for more than 6 items
  - strings in single quotes when nested
  - `-0`, `[Function: name]`, `[class X]`, `Map(n) {…}`, `Set(n) {…}`

  Port the algorithm from Node's `lib/internal/util/inspect.js` rather than guessing. Build the differential corpus around its edge cases.
- **O17 · Integer `%`.** Integer `%` takes the sign of the dividend, matching JS and Rust. An integer `%` by zero throws `RangeError`, as the brief says for division.
