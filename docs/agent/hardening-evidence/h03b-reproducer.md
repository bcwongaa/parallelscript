# H03b reproduction and RED regressions (opus-reproducer)

Reproducer: subagent for H03b, model `claude-opus-5-5`, effort xhigh, 2026-10-16.
Scope: H03b in `docs/agent/HARDENING_PLAN.md`, ledger rows C28, C30, C32, C33 and C35, plus a check of C03's typed half.

Deliverables:
- seven new RED suite regressions under `pts/tests/run/`, each skipped as a known issue;
- a verification of the three existing skipped tests, whose expect blocks are unchanged because each equals Node's stdout;
- this report.

Nothing else was changed: no compiler, runtime, library, harness or `tools/agent/` file, no plan item ticked, and PROGRESS, DECISIONS and the plan are untouched.

## Summary

| File | Row | On the base (exact first failure) | Kind | Controls inside |
|---|---|---|---|---|
| `iterator_methods_through_any.pts` (new) | C28 | exit 0, 23 lines vs 56. Line 1 is `next escaped: h.next is not a function` (Node `next 1 { value: 1, done: false }`). Every generator, map/set/array iterator and async-generator case escapes with `h.next is not a function`; `not started` gives `returned.return is not a function` and `async` gives `ag.next is not a function` | wrong stdout | three guards (runtime iterators have no `return`/`throw`, `loose.return is not a function`: they pass today only because nothing is a method); an object literal's own `next()` through `any` |
| `iterator_next_result_identity.pts` (new) | C30 | exit 0, 8 lines vs 8, the first 7 differ. Line 1 is `same object false` (Node `true`); `later write 5` (6); `printed { value: 5, done: false }` (Node `{ done: false, value: 5, note: 'kept' }`); `extra properties undefined 2 false` (Node `step 2 2 false`) | wrong stdout | a done result's value of another type (`Iterator<number, string>`) |
| `iterator_return_throw_results.pts` (new) | C30 (and N3) | exit 1: 6 × `error PTS1001: \`it.return\` is not supported yet` (or `it.throw`) at 49:20, 56:5, 62:20, 69:20, 89:22 and 91:20 | build error | none |
| `yield_star_result_passthrough.pts` (new) | C32 | exit 0, 66 lines vs 73. The first difference is at line 2: Node's second `for-of next 0 read done` is missing. Later: `done first got result, same object: false`, an extra `unread next 0 read value` (C33's eager read), `next through any escaped: h.next is not a function` (C28), and `return not done got result, same object: false` | wrong stdout | the done-result paths of `return`/`throw` (`outer finally`, `yield* gave 80`, `{ value: 50, done: false }`) |
| `array_pattern_elision_done_only.pts` (new) | C33 | exit 0, 50 lines vs 42. The first difference is an extra `class read value 0` after `class read done 0`, and there are 8 extra `read value` lines in all, one per elision before the iterator is done | wrong stdout | elisions after the iterator is done |
| `iterator_result_as_layouts.pts` (new) | C35 | exit 1, 9 lines vs 11. Line 1 is `parameter escaped: Expected number, got function` (Node `parameter: 42`). Then `write: 5 { done: false, extra: 5.5, value: 1 }`, `getter done: 44` (48), `array element: t` (49), `field: holder` (50), `done value: 3 end` (`8 finished`), `from any next: step 1` (100) and `from any getter escaped: Expected number, got function`. The run ends with stderr `Uncaught Error: console.log got a malformed value 0x0`. Identical over three runs (one with `PTS_THREADS=1`) | wrong stdout, then an uncatchable runtime error | none (all through the narrowed layout) |
| `iterator_result_as_one_shape.pts` (new; **parent decides**) | C35 | exit 1: 4 × ``error PTS1001: using a `object2` as a `object1` is not supported yet`` (`object3` for the extras) at 15:20, 26:20, 32:20 and 43:20 | build error (honest, internal names) | none |
| `iterator_next_result_getters.pts` (existing, unchanged) | C30 | exit 0, 24 lines vs 21. Line 3 is `manual read value 0`, where Node prints `first done: false`. Later `manual escaped: the value of a done result was read` and `unread escaped: …` | wrong stdout | the same results through `for…of` |
| `iterator_result_as_cast.pts` (existing, unchanged) | C35 | exit 1, 1 line (`false`) vs 3, then `Uncaught TypeError: Expected number, got function` at `:11`. Its second half alone (`probes/c35_as_cast_second_half.pts`) is SIGSEGV, exit 139, on this base too | wrong stdout, then an uncaught error | none |
| `iterator_result_getters_typed.pts` (existing, unchanged) | C03 typed half | exit 1: 2 × ``error PTS1001: an object literal with getters or setters as an iterator result (`IteratorResult<number, any>`) is not supported yet; a `next(): any` can return one`` at 21:24 and 55:24 (D-177's diagnostic) | build error | none |

How the expect blocks were checked:
- Every `// expect:` block is Node's exact stdout (Node `v24.21.0`, exit 0 for all ten).
- `checkexpect` parsed each block with `spec_test.go`'s own regexps and `expectBlock` logic and compared it byte for byte with a fresh `node --no-warnings --experimental-transform-types` run of an `.mts` copy under `TZ=America/New_York`.
- That check also counted ParallelScript-feature matches with the harness's `isPlainTypeScript` patterns: none, so `TestRunMatchesNode` compares all ten.

Every expectation is reachable: a twin of each file changes only the RED construct to a form that works today and prints exactly the expectation under today's PTS (see "Expectations are reachable").

**Integration notes.**
- No file panics the in-process compiler. All ten fail normally in both `TestRun/<name>` and `TestRunMatchesNode/<name>`:
  - wrong stdout: `run_test.go:69` and `run_test.go:98`;
  - build errors: `run_test.go:69: assertion failed: error is not nil: …PTS1001…`.
- Two files stop their binary before the end on the base: `iterator_result_as_layouts`, with an uncatchable `console.log got a malformed value`, and `iterator_result_as_cast`, with an uncaught TypeError (and SIGSEGV for its second half alone). The binary runs out of process, so this is just a failed run. The crashing case is last in `iterator_result_as_layouts`, so every other case prints first.
- To run one file alone, remove its `// skip:` line first. These are the commands that showed the RED failures (the skip line was removed in place and restored byte for byte after each run, checked with `cmp`):

```sh
PATH=$HOME/.local/go/bin:$HOME/.local/node/bin:$PATH PTS_HOME=<worktree> PTS_CACHE_DIR=/tmp/pts-h03b-repro/cache GOMAXPROCS=2 \
  go test -count=1 -p 2 ./pts/tests/ -run 'TestRun/<name>$|TestRunMatchesNode/<name>$' -v
```

  `<name>` is one of `iterator_methods_through_any`, `iterator_next_result_identity`, `iterator_return_throw_results`, `yield_star_result_passthrough`, `array_pattern_elision_done_only`, `iterator_result_as_layouts`, `iterator_result_as_one_shape`, `iterator_next_result_getters`, `iterator_result_as_cast` or `iterator_result_getters_typed`. The linker writes a test binary of about 69 MB, so a `ulimit -f` cap must allow it (40 MiB failed with `mapping output file failed: file too large`).

**Couplings a fix must respect.**
1. **C30 identity and C35 need one representation.** C30's identity cases (`it.next() === it.next()`, later writes, extra properties) bind the result to a local of tsc's type `IteratorResult<number, any>`. If `it.next()` just returned `next()`'s object as a `Value` and then converted it to D-109's union, the conversion would copy it again (N2 below), and identity would be lost. So C30 needs a D-109 result union value that holds the object as it is and reads `done`/`value` by name, or checks the descriptor before a field read. That is C35's by-name fix.
2. **C32 also fails on C33's root cause.** The base reads every non-done inner result's `value` at the `yield*` step (`unread next 0 read value`). Its `next through any` case also needs C28.
3. **`iterator_return_throw_results` needs the `!` call form.** It needs `it.return!(v)` and `it.throw!(e)` to reach `iteratorMethodCall`; the strict ESNext library makes `Iterator<T>`'s `return`/`throw` optional, so a type-sound call needs `!`, `?.` or a narrowing `if (it.return)` (N3). Past the diagnostic, the call has C30's defect: a `Generator<number>` view (`probes/c30_return_throw_generator_view.pts`) prints `return done return read done`, `return done return read value`, `return done escaped: the value of a done result was read`.
4. **`iterator_result_as_layouts` has two doors.** It covers the `as` door and the `any` door (`const result: IteratorResult<number> = source.next()` from an `any` source, with no assertion at all). A fix that only rejects `as` leaves `from any next: step 1`.
5. **If the parent chooses diagnostics over by-name reads for C35**, the C35 run tests become errors/ tests: `iterator_result_as_layouts`'s `any` cases still need by-name reads or a checked read, since there is no assertion to reject.

## Root causes (verified in code at `a65a75d62`)

- **C28.** A method call through `any` is `pts_dynamic_call_method` (`pts/runtime/src/dynamic.c:807-827`). For a receiver that isn't a program object it asks `pts_builtin_method_call` (`pts/runtime/src/dynamic_methods.c:1375-1419`), whose kind switch has no `PTS_KIND_GENERATOR` case (`default: return false;` at `:1409-1410`).
  - Generators, async generators and every runtime iterator (map, set, array, string, object iterators) are `PTS_KIND_GENERATOR`, so the call falls to `pts_dynamic_call(pts_dynamic_get(object, key), …)` (`dynamic.c:818`). There the property is undefined, giving `%s is not a function` (`not_a_function`, `dynamic.c:716-718`), worded as Node words a missing method.
  - The fix must not give runtime iterators `return`/`throw`, which JS's map, set and array iterators don't have; the tests' guards pin that.
  - Map and set `keys()`/`values()`/`entries()` through `any` do work (`map_methods` and `set_methods`, `dynamic_methods.c:943-951`); it is their results that fail.
- **C30.** `iteratorMethodCall` (`pts/lower/generators.go:236-268`) lowers `next`/`return`/`throw` on an iterator type in four steps:
  1. it wraps the receiver in an object iterator (`IteratorOf`, `:266`; `pts_iterator_of`, `generator.c:168-184`);
  2. it resumes it;
  3. `iteratorResult` (`:273-315`) builds a new D-109 shape (`NewObject`, `:282`; a `Conditional` over the two shapes, `:308`) whose `done` is the step's result and whose `value` is `IteratorValue`;
  4. the object iterator's step (`resume_object_iterator`, `generator.c:187-215`) reads `done` (`:207`) and, if it is false, `value` (`:208`) at the call; for a done result, `IteratorValue` reads `value` from `finished` (`pts_iterator_value`, `:469-480`), since `iteratorResult` always asks for it.

  So getters run at the call in a fixed order, a done result's value is read, and the caller gets a copy: identity, later writes, extra properties and the literal's own property order are lost.
- **C30, the `!` form (N3).** `iteratorMethodCall` sees the callee through `ast.SkipParentheses` only (`generators.go:237`), so `it.return!(v)` (a `NonNullExpression`) isn't recognised. The callee is then lowered as a property read of a library method, which reports `` `it.return` is `` not supported yet (`pts/lower/lower.go:2502-2504`).
- **C32.** `delegation` (`generators.go:199-227`) opens the inner iterator (`IteratorOpen`, `:221`) and yields `IteratorValue(inner)` (`:208`, `:216`): the inner value, not the inner result. The outer generator's consumers then build or read a generator result.
  - JS's `yield*` instead does GeneratorYield(innerResult) for a sync generator: it hands the inner result object through after reading its `done` once. For return/throw it hands through a result that isn't done.
  - With getter results, Node reads `done` twice (once more per nesting level) and the caller sees the inner object (`===`).
- **C33.** An array pattern's elision only steps (`IterationNext`; `pts/lower/destructuring.go:108-116`, `// an elision: [a, , c] steps past an item`). The step itself, `pts_iteration_next` → `resume_object_iterator`, reads `value` of every result that isn't done (`generator.c:207-209`). JS's IteratorStep reads only `done`, and IteratorValue runs only for an element that takes the value.
- **C35.** A D-109 result shape narrowed out of its union is written as an unchecked cast: `fromUnion` (`pts/cgen/cgen.go:1087-1088`) gives `((pts_class_objectN *)pts_value_as_object(r))`. Only non-shape classes get `pts_value_as_instance`'s descriptor check (`:1085-1086`, `isClass` at `:1169-1172`). The union is assumed to hold only its member shapes, and two doors put objects of other layouts into it:
  - **`as`.** `assertedLiteral` (`pts/lower/lower.go:2307-2320`) keeps a literal whose shape is no member's as it is: `ToUnion{X: x, To: u}`, `:2317`, written for `{ type: "@@init" } as Action`.
  - **Conversion from `any`.** `pts_value_convert` → `object_fits` → `descriptor_implies` (`pts/runtime/src/convert.c:155-178`, `:268-272`) keeps an object "as it is" when its declared types imply the shape's properties. Extra fields are ignored, and a getter counts when its declared result implies the property and has the same slot (a getter or field typed `any`, `:171-173`).

  Shapes of other layouts are converted by copying (`copy_object`, `:297-319`), which calls getters (N2). General unions of the program's object types are safe (`probes/adj_p8_general_union.pts` matches Node for `as` and `any` with extras and getters), so the hole is D-109's shapes.
- **C35, single shape.** `as IteratorYieldResult<number>` or `as IteratorResult<number, number>` of another layout converts to the one shape and fails with ``using a `object2` as a `object1` is not supported yet`` (`pts/lower/objects.go:905-907`). `assertedLiteral` converts at the `as` node (`lower.go:2319`), and `reportedGetterResult` (`:915-926`) words only an object-literal node, so the C03 text isn't used.
- **C03's typed half.** D-177's deliberate diagnostic: `reportedGetterResult` (`objects.go:915-926`) for a literal with a `done`/`value` accessor whose conversion to the result fails.

## Environment and provenance

- Worktree `/home/bryan/projects/parallelscript/.claude/worktrees/agent-a230de3063844d81e`, branch `worktree-agent-a230de3063844d81e`, base `a65a75d62724c7cc78d14748aedb9ac0e51a6fca` (main). Tests commit `46e1946b1`.
- Submodules (none in a fresh worktree), each exit 0: `git submodule update --init --reference /home/bryan/projects/parallelscript/.git/modules/pts/third_party/<lib> pts/third_party/<lib>` for ryu (`72e18e8d1`), bdwgc (`4fab5386d`), libuv (`840404ce8`), libbacktrace (`0b9b49cf4`) and pcre2 (`6f9d7c137`).
- CLI: `GOMAXPROCS=2 go build -p 2 -o pts-bin/pts ./cmd/pts`, exit 0. `pts version` printed `pts 0.7.0 (commit a65a75d62; typescript-go 7.0.2)`, exit 0; sha256 `f2b6af9399f67ce64d6c9f849fa6149ebdfbe0966fcc6653f8318e1ca403731e`.
- Tools: `go version go1.26.9 linux/amd64`; Node `v24.21.0` (`~/.local/node/bin/node`); `Ubuntu clang version 18.1.3 (1ubuntu1)`.
- Disk: 123G free of 234G before starting, 118G at the end.
- Every PTS run used `PTS_HOME=<worktree>` and `PTS_CACHE_DIR=/tmp/pts-h03b-repro/cache`, so the runtime is the base's.
- Scratch `/tmp/pts-h03b-repro`:
  - test drafts `scratch/`, twins `twins/`, probes `probes/`;
  - scripts `tools/`;
  - `pts run` outputs `red/<name>.{stdout,stderr,exit,expect}` and `red/summary.txt`;
  - `go test` logs `gotest/<name>.log`, `gotest/summary.txt`, `gotest/skipcheck.log` and `gotest/unittests.log`.

Test files (sha256):

| File | sha256 |
|---|---|
| `iterator_methods_through_any.pts` | `256042d5bc96d9c64aa115ec3ace6760c7b2f8707a38d6c5431743547e20d77b` |
| `iterator_next_result_identity.pts` | `24ea63ac9dc42f205426207230727c739021aa8ac4e11823b48de644d924e721` |
| `iterator_return_throw_results.pts` | `31563f653944b434b9feb81e70fa9215dd3a6ef4a1689a885e1dbee8a776e277` |
| `yield_star_result_passthrough.pts` | `8580b76a5393a2f89a7bf902794c5c20c48c08ead4755beeda1f4d78205d9ecb` |
| `array_pattern_elision_done_only.pts` | `bd9a7207d63aa9000ded6ec6ebce17ec8498039e7fddb5b2a4bd482fdb731a7f` |
| `iterator_result_as_layouts.pts` | `247edf98569b9f4c2c53448c487ce0021e5ceb83d7136e6e428d2c5b8fde4b82` |
| `iterator_result_as_one_shape.pts` | `91f494d3bc0d728b6e1b2b9364d42139f3e45caa409fcd5afbb77616861d7c60` |
| `iterator_next_result_getters.pts` (unchanged) | `00895de10c3d8d658fd7e822da32f3a239ad1d68e55bf9dd5b4c9d75f0411b1b` |
| `iterator_result_as_cast.pts` (unchanged) | `909dd1486a82b5d994c948b4555421560a52ab33d99f629152c387b18c49de32` |
| `iterator_result_getters_typed.pts` (unchanged) | `48a422cb3dd97dc2e366f1acf8fd14ffea0f9078214233536f4e0fa9141863d3` |

## Commands and exit codes

All scripts use `set -o pipefail`, cap written files with `ulimit -f` (40 MiB; 4 GiB for `go test`'s linker) and time each run out. Every program is bounded: each file runs in well under a second under Node and as a base binary.

1. `tools/mkexpect.sh <draft> pts/tests/run/<name>.pts` for the seven new files, exit 0 each. It appends Node's stdout as the `// expect:` block and refuses trailing whitespace, directive-like lines, a missing final newline or a non-zero Node exit.
2. `tools/check.sh <ten files>` (`checkexpect`, a Go port of `spec_test.go`'s `parseSpec`/`expectBlock` and `isPlainTypeScript`): exit 0, with 10 × `expect==node true, node exit 0, plain true` (`red/checkexpect.txt`).
3. `tools/redrun.sh <ten names>` (`pts run pts/tests/run/<name>.pts`, 8 GiB address-space cap): exit 0, meaning all ten are RED.
   - Per-file `pts run` exit codes: 0 for `iterator_methods_through_any`, `iterator_next_result_identity`, `yield_star_result_passthrough`, `array_pattern_elision_done_only` and `iterator_next_result_getters`; 1 for `iterator_return_throw_results`, `iterator_result_as_layouts`, `iterator_result_as_one_shape`, `iterator_result_as_cast` and `iterator_result_getters_typed`.
   - The C35 layouts binary, built once and run three times (once with `PTS_THREADS=1`), exited 1 each time with identical output.
4. `tools/gotest.sh <ten names>`: exit 1 for each file, both `TestRun/<name>` and `TestRunMatchesNode/<name>` FAIL, no panic, and each file was restored byte for byte. For each file the script removes the `// skip:` line, runs the command above with `-timeout 20m`, and restores the file.
   - The five wrong-stdout files fail at `run_test.go:69` (`--- got.stdout +++ spec.stdout`) and `run_test.go:98` ("pts and Node print different output"). So do `iterator_result_as_layouts` and `iterator_result_as_cast`.
   - The three build-error files fail at `run_test.go:69: assertion failed: error is not nil: …PTS1001…` in both tests.
5. `tools/skipcheck.sh`, with the committed skip lines:
   - `go test … -run '^(TestRun|TestRunMatchesNode)$/^(<ten names>)$' -v`: exit 0, 20 × `--- SKIP` (10 files × 2 tests);
   - the harness's unit tests (`-run 'Directive|ExpectBlock|PlainTypeScript|SkipCauses|SkipsAreClassified'`): exit 0, 7 PASS.
6. `tools/twincheck.sh <ten names>`: exit 0, 10 × `twin exit 0, stdout == expect block: yes` (`red/twincheck.txt`). The script builds and runs each twin with the base's `pts`, and compares its stdout with the committed expect block.
7. `git status` after the runs: only the seven new files were untracked, and `git diff --stat` was empty (the three existing tests unchanged).

## The files

### `iterator_methods_through_any.pts` (C28)

Sync generators held as `any`:
- `next(v)`: the sent value reaches the generator (`counter got 41`);
- `.value`/`.done` of results, and calls after the generator is done;
- `return(v)` running a `finally`, and `return(v)` whose `finally` yields again (`{ value: 'cleaning', done: false }`, then `{ value: 'closed', done: true }`);
- `throw(e)` caught inside the generator, which yields `recovered`;
- `throw(e)` not caught: rethrown to the caller, and the generator is then done;
- `return`/`throw` on a generator that hasn't started: it never runs.

Runtime iterators held as `any`: map `keys()`/`values()`/`entries()`, set `values()`, and `values()`/`keys()`/`entries()` of a `number[]` (called typed, the iterator then held as `any`), printed whole and read by `.value`/`.done`.

Guards: `return`/`throw` of runtime iterators are not functions (`loose.return is not a function`, as Node words it).

Mixing:
- `h.next()` through `any`, then `for…of` over the same `any` handle (a generator and a map iterator);
- a typed handle stepped first, then `h.next(10)` through `any`.

Control: an object literal's own `next(v)` through `any`.

An async generator held as `any`: `ag.next()` is a `Promise` of `{ value, done }`, and the file awaits `next()` three times and `return("stopped")`.

Not tested: `typeof h.next`. Node prints `function`; PTS prints `undefined` (N6).

### `iterator_next_result_identity.pts` (C30)

Typed `Iterator<number>` holders of `next(): any` iterators:
- a class whose `next()` returns the same object each time: `it.next() === it.next()`, and `=== reused`;
- a later write to that object, seen through the result (`later write 6`);
- the result printed whole, in its own property order with its extra property;
- extra properties read through `any`, and printed;
- an object literal implementing `Iterator<number>`.

Control: a done result's `string` value through `Iterator<number, string>`.

### `iterator_return_throw_results.pts` (C30; N3)

`it.return!(v)` and `it.throw!(e)` on typed `Iterator<number>` holders of a class and of an object literal, both with `next(): any`, `return(): any` and `throw(): any`. Their results have logging getters, and a done result's value getter throws if read.

Cases:
- a done `return` result whose `done` is read and whose `value` is not;
- an unread `return` result;
- a `throw` result read value first, and one read done first;
- the literal: `return` then `throw`, read later in the other order.

### `yield_star_result_passthrough.pts` (C32)

`outer` does `yield* inner` inside `try/finally`, then logs the `yield*` value, yields 50 and returns 99. `inner` is a user iterator whose results have logging getters and which remembers its last result.

Consumers:
- `for…of`: `done` read twice, then `value`;
- spread;
- typed `o.next()`, read `.done` first, `.value` first, and left unread;
- `h.next()` through `any`;
- nested `outer(outer(inner))`: `done` read three times;
- identity, `result === inner.last`, in each case.

Forwarding:
- `o.return(7)` with a not-done inner `return` result: handed through;
- `o.return(7)` with a done one: `outer finally` and a new `{ value: 70, done: true }`;
- `o.throw(e)` with a not-done inner `throw` result: handed through;
- `o.throw(e)` with a done one: `yield* gave 80`, then `{ value: 50, done: false }` and a later `{ value: 99, done: true }`.

### `array_pattern_elision_done_only.pts` (C33)

Patterns with elisions over iterators whose results have logging getters:
- `[, b]` over a class instance with `[Symbol.iterator]`;
- `[, , c]` over `any`;
- `[a, , ]` (a trailing elision) and `[a, , c]` over `Iterable<number>`;
- a parameter pattern `([, second]: Iterable<number>)`;
- a `for…of` binding `[, second]` over `Iterable<number>[]`;
- a nested `[[, inner]]`.

Control: `[a, , , d]` over an iterator that is done after one item, where the elisions after the end don't step it.

### `iterator_result_as_layouts.pts` (C35)

Literals asserted `as IteratorResult<number>` (or `<number, string>`), each read after narrowing on `done`:
- a getter `value` passed to an `IteratorYieldResult<number>` parameter;
- a write `result.value = 5` to a literal with an extra property before `value`;
- a getter `done` with a plain `value`;
- an extra property, in an `IteratorResult<number>[]` element;
- an extra property, in a class field;
- a done result's `string` value with an extra property.

From `any`, without an assertion:
- an object with an extra property and an `any` value, from an `any` iterator's `next()`;
- an `any`-typed getter `value`.

Last, because it stops the program on the base: a literal with two extra properties, read narrowed, then through `any`.

### `iterator_result_as_one_shape.pts` (C35, parent decides)

A getter literal and an extras literal, each asserted `as IteratorYieldResult<number>` and `as IteratorResult<number, number>` (one shape, D-109). On the base this is an honest PTS1001 with internal names. It is a ready target if the fix reads by name; if the fix chooses a diagnostic, this file becomes an errors/ test.

### The existing files

- `iterator_next_result_getters.pts` (C30): its expect block equals Node's; RED as in H03.
- `iterator_result_as_cast.pts` (C35): its expect block equals Node's; RED as in the H03 review.
- `iterator_result_getters_typed.pts` (C03's typed half): see below. None of the three was edited, since all three expectations are right.

## Expectations are reachable

Each twin changes only the RED construct, and `tools/twincheck.sh` builds it on the base and compares its stdout with the committed expectation. All ten are identical, and every build and run exits 0. The twins are in `/tmp/pts-h03b-repro/twins/`:

| Twin | Change |
|---|---|
| `iterator_methods_through_any` | each RED holder typed instead of `any` (`const h = counter()`, `const h = prices.keys()`, `const ag = ticks()` …), so the calls take today's typed paths; the guards and the control keep their `any` holders. Making the test itself type-check under a typed view needed two edits to the test (not the twin): `single`'s result type is `number \| void`, and the async return passes a string. |
| `iterator_next_result_identity` | `const it: any` instead of `const it: Iterator<number>` (a dynamic `next()` returns the object itself) |
| `iterator_return_throw_results` | `const it: any` and `it.return(…)`/`it.throw(…)` without `!` |
| `yield_star_result_passthrough` | hand-written: `outer` replaced by a class `Outer` implementing exactly its body as JS's `yield*` runs it (reads each inner result's `done` once, hands a not-done result on, `yield* gave …`, `yield 50`, `return 99`, `outer finally`), returned as `any` |
| `array_pattern_elision_done_only` | hand-written: each pattern replaced by `take(source, binds)`, JS's IteratorBindingInitialization through an `any` handle (one `next()` per element and elision, `value` read only for a bound element, `return()` if not done); the control unchanged |
| `iterator_result_as_layouts` | `as any` instead of the assertions, and `any` for the destinations (`IteratorYieldResult<number>` parameter, `IteratorResult<number>[]`, field, `const result: IteratorResult<number>`) |
| `iterator_result_as_one_shape` | `as any` instead of the assertions |
| `iterator_next_result_getters` | `const iterator: any` (as in H03) |
| `iterator_result_as_cast` | `as any` and `(): any` |
| `iterator_result_getters_typed` | `next(): any` (as in H03) |

## C03's typed half

`iterator_result_getters_typed.pts`'s expect block equals Node's byte for byte, so it is the right target.

What it needs:
1. **Representation.** It is representable today for all its consumers: with only `as IteratorResult<number>` added to its two returns (`scratch/c03/c03_typed_with_as.pts`), the base prints exactly its expect block (`cmp` equal). `for…of`, spread, `Array.from` and `new Set` read `done`/`value` by name, which confirms the H03 review's note.
2. **Its own blocks.** The PTS1001 of D-177 is `reportedGetterResult`, `objects.go:915-926`. The `return`'s conversion would also have to keep the literal as `assertedLiteral` does (`lower.go:2316-2318`).
3. **Safety.** That is safe only together with C35's fix: narrowed typed reads of D-109's shapes by name or checked. A typed caller's `r = it.next(); if (!r.done) r.value` would otherwise be the raw read.
4. **Identity.** It needs C30's fix too, so that a typed `it.next()` hands back the literal rather than reading both getters at the call.
5. **What changes.** `errors/iterator_result_getters.pts` would go, and D-177's "stays unsupported" would be superseded by a new decision. That is the parent's call, not reopened here.

## New findings (minimal repros, Node vs base)

- **N1. Closing an iterator reads `return()`'s result.** It happens at a `break` of `for…of`, at the end of an array pattern, and in a `for…of` over `any`.
  - Repro, `probes/close_reads_result.pts`: an iterator whose `return()` gives `{ get done(), get value() }` with logs; `for (const n of src) { break; }` and `const [a] = src`.
  - Node logs only `return()`. The base also logs `… close result read done` and `… close result read value` for each.
  - JS's IteratorClose only checks that the result is an object.
  - Cause: `pts_iterator_close` (`generator.c:482-487`) resumes with RETURN through `resume_object_iterator`, which reads `done` and `value` (`:207-208`).
  - Same function as C33/C32. Not covered by a test file. Suggested for H03b or a new row.
- **N2. Converting an `any` object to `IteratorResult<T>` copies it when its declared types don't fit.** A getter typed `number`, or a plain `number` `value`, is copied: getters run (a done result's value too), and identity and extra properties are lost.
  - Repro, `probes/adj_p4_convert.pts`: `const loose: any = { get done() {…}, get value(): number {…} }; const typed: IteratorResult<number> = loose; typed === loose`.
  - Node: no getter runs, and `same: true` (three cases).
  - Base: logs `read done`, `read value`, and `same: false`; a plain `{ done: false, value: 8, tag: "x" }` prints `{ value: 8, done: false }`.
  - Cause: `copy_object` (`convert.c:297-319`). Verified for a typed variable; array elements, fields and parameters typed `IteratorResult` take the same conversion from `any` (not probed separately). It is C30's coupling 1 above.
  - When the declared types do fit, the object is kept and the narrowed read is raw (C35's `any` door, in `iterator_result_as_layouts.pts`).
- **N3. Optional iterator methods can't be called on `Iterator<T>`.** `it.return!(v)`, `it.throw!(e)`, `it.return?.(v)` and `if (it.return) …` are PTS1001 (`` `it.return` is not supported yet ``).
  - Repro, `probes/c30_p2_forms.pts`: `const it: Iterator<number> = new Steps(); it.return?.(1); if (it.return) { it.return(2); }`.
  - Node: `return called with 1`, `optional call { done: true, value: 1 }`, …
  - Base: exit 1, PTS1001 at the optional call and at the `if` condition.
  - These are the type-sound ways to call them under `strict` and the ESNext library. Only `!` is in a test (`iterator_return_throw_results.pts`); `?.` goes through optional-chain lowering, and the condition reads a library method as a value.
- **N4. A D-109 result shape can't be destructured or spread, without any `as`.**
  - `const { value } = r` for `r` narrowed to `IteratorYieldResult<number>`, and `{ ...y }` of an `IteratorYieldResult<number>`: `error PTS1001: converting number | undefined to number is not supported yet`.
  - `{ ...r }` of `IteratorResult<number, string>`: ``spreading a object1 | object2 into an object is not supported yet``.
  - Repro, `probes/c35_p3_plain.pts`, with `r` from a generator. Node: `destructure 1`, `spread { value: 1, done: false }`, `yield result 3 { done: false, value: 3 }`.
  - Honest diagnostics. The cause is D-109's `value: T | undefined` field. Left out of the C35 tests, since these aren't the assertion's.
- **N5. An `any` array has no `values()`, `keys()` or `entries()`.**
  - Repro, `probes/adj_p1.pts`: `const list: any = [1, 2]; [...list.values()]; list.entries().next()`.
  - Node: `[ 1, 2 ]` and `{ value: [ 0, 1 ], done: false }`. Base: `list.values is not a function`, `list.entries is not a function`.
  - Cause: `array_methods` (`dynamic_methods.c:737-772`) lacks them.
  - Next to C28 (runtime iterators through `any`). The C28 test uses typed arrays' iterators held as `any`, so it doesn't cover this.
- **N6. Methods through `any` aren't values (record).** With `h` a generator held as `any` and `list` an `any` array, `typeof h.next`, `typeof h.return` and `typeof list.push` are `undefined undefined undefined` on the base and `function function function` on Node (`probes/adj_p1.pts`). Builtin methods through `any` are not values today. Not tested, as asked.
- **N7. A typed result literal prints in V8's generator order.**
  - Repro, `probes/adj_p7_misc.pts`: `next(): IteratorResult<number> { return { done: false, value: 1 }; }`, then `console.log(it.next())`.
  - Node `{ done: false, value: 1 }`; base `{ value: 1, done: false }`.
  - The literal becomes D-109's shape at its `return`, which prints value first, so C30's hand-back alone wouldn't change it. Minor.
- **N8. `h[Symbol.iterator]()` through `any` is PTS1001** (`` `Symbol.iterator` is not supported yet ``).
  - Repro: `const h: any = g(); h[Symbol.iterator]() === h`. Node `true`.
  - Honest; next to D-111's known gap for reading `x[Symbol.iterator]`.
- **N9. The single-shape assertion's diagnostic names internal shapes.** It reads ``using a `object2` as a `object1` `` (`iterator_result_as_one_shape.pts`): the V08/H22 wording family, the same as the old C03 text.
- **Checked, no defect beyond C30.**
  - A typed `next()` on `Iterator<number, string>`: a generator's yield and done results narrowed (`first.value + 1` → 2, `second.value.toUpperCase()` → `FIN`), and a user literal's union result (`b.value.length` → 3) all match Node (`probes/adj_p1.pts`), apart from N7's print order.
  - General discriminated unions with `as` of extra properties or getters, and `any` conversions of them, match Node (`probes/adj_p8_general_union.pts`): the raw narrowed read is specific to D-109's shapes.
