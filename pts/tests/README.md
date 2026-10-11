# The ParallelScript test corpus

`go test ./pts/tests/` (or `make test`) runs every file here in parallel.

- **`run/*.pts`**: programs that must build and run. `TestRun` compares their stdout and exit code with what they expect. `TestRunMatchesNode` (the differential test, D-O04) runs each plain-TypeScript program under Node too and requires the same stdout and exit code; stderr is never compared.
- **`corpus/<theme>/*.pts`**: 300 realistic plain-TypeScript programs (algorithms, text, oop, data, functional, errors; D-089). `TestCorpusMatchesNode` requires each to print what Node prints and exit as it does; they have no expected output of their own. A program that still diverges carries `// skip: <cause>`. `corpus/compare.sh file.pts…` compares from the shell (it ignores `// skip:`; set `PTS=` to try another compiler).
- **Generated corpora**: `TestGeneratedNumberCorpusMatchesNode` (`numbers_test.go`) writes programs with thousands of number literals and operator applications, and `TestGeneratedStringCorpusMatchesNode` (`strings_test.go`) programs that call every String method on random strings and change the case of every code point; both compare with Node line by line. Keep each generated part small: top-level code is one C function, and clang's time grows faster than its size. Differential tests are named `…MatchesNode`, so `make test-diff` runs them all.
- **Modules**: a test that imports modules of its own keeps them in the directory named after it (`run/modules.pts` imports `./modules/counter.pts`); only `run/*.pts` and `errors/*.pts` are tests. The differential test gives Node a copy of the test and those modules as `.mts` files, with relative specifiers (`./x` or `./x.pts`) renamed to match.
- **`errors/*.pts`**: programs that must fail to compile. `TestErrors` requires each `// error:` text to appear in the compiler's errors, formatted as `file.pts:line:col - error CODE: message`.

## Directives

Directives are whole-line comments anywhere in the file.

| Directive | Meaning |
|---|---|
| `// expect:` | Starts the expected stdout: every following line that starts with `//` is one output line (`// text` → `text`, a bare `//` → an empty line). The block ends at the first line that isn't a comment or is another directive (use a `.out` file for output that looks like a directive). |
| `name.out` | Alternatively, a file next to the test holding the exact expected stdout. Not both. |
| `// exit: N` | Expected exit code (default 0). |
| `// stderr: text` | stderr must contain `text`; repeatable. |
| `// error: text` | Error tests: some compile error must contain `text`; repeatable, at least one. |
| `// no-diff: reason` | Don't compare with Node (for output Node can't match, e.g. ParallelScript-only behaviour). |
| `// skip: reason` | Skip the test everywhere: it reproduces a known issue listed in `PROGRESS.md`. |

A run test with no expected output is checked only against Node, which suits plain-TypeScript corpus programs.

## Plain TypeScript

A test is compared with Node unless it uses ParallelScript features: a `pts:` import, the word `parallel`, a `.par` view, or an integer type (`int`, `i32`, `u8`, …) after `:`, `<`, `,`, `|` or `as`. The check ignores comments and errs towards skipping; opt out explicitly with `// no-diff:`.

Node runs the source as an ES module (`main.mts`) with `--experimental-transform-types`, so enums and parameter properties work (D-009).
