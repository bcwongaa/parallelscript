# H01b independent defect reproduction (opus-reproducer)

Reproducer: named subagent `opus-reproducer`, model `claude-opus-5-5`, effort xhigh, 2026-10-15.
Scope: rows C01, C02, C05, C06 and C07 of `docs/reviews/2026-10-13-final/CONSOLIDATED.md`.
This is reproduction only. No compiler, runtime, library, test or `tools/agent/` file was changed, nothing was committed, and no plan item was ticked.
Every result below was executed fresh in this session. The killed predecessor's `raw/` outputs were not used (see "Predecessor drafts").

> **Parent integration note (2026-10-15).** This report is the reproducer's, kept as written. When integrating, the parent renamed seven programs so that their rows match the defect: `c02-red-typed-iteratorresult-getters` → `c03-red-typed-iteratorresult-getters` (ledger C03, the typed getter layout); NEW-1's `c02-red-any-iterator-getter-sequence`, `c02-red-any-iterator-break-return` and `c02-red-any-generator-break` → `c18-red-…`, and their typed controls `c02-control-typed-iterator-getter-results`, `c02-control-typed-iterator-break-return` and `c02-control-iterable-param-generator-break` → `c18-control-…` (NEW-1 is ledger row C18; NEW-2 is C20, NEW-3 is C19). Only names, header row fields and cross-references changed. The parent's own re-execution of every program is in [h01b.md](h01b.md) and [h01b-probes.json](h01b-probes.json).

## Verdicts

| Row | Verdict | Boundary in one line |
|---|---|---|
| C01 | **reproduced**, wider than the ledger states | `??=` / `\|\|=` on any keyed place whose object is a dynamic value panics `pts build` (exit 2): erased records in generic arrows and generic function expressions, mapped-type fields of generic classes, and also a plain `any` object with no generics at all (property or element). `&&=`, `+=`, plain `=`, reads, generic function declarations and methods all work. |
| C02 | **reproduced**; the root cause is a void/never getter, not the iterator protocol | Any getter whose type is `void` (body never returns, so tsc infers `void`) or declared `never`, in an object literal or a class, panics cgen (exit 2), even when never read and without `any` or iteration. The Grok iterator shape panics only because its `get value()` only throws. The typed `IteratorResult` getter literal is a source-located PTS1001 (known D-146), not a panic. Non-void getter results through `any` don't panic and read `done` before `value`, but hit a separate drain defect (NEW-1). |
| C05 | **reproduced** | `flat` / `flatMap` don't flatten through union-typed elements or tuples: union arrays, tuple arrays, heterogeneous and inferred deep nesting at depths 1, 2 and Infinity, nested unions past level 1, union-typed `flatMap` mapper results, union arrays with holes. Homogeneous arrays, `any` arrays and `.par.flatMap` over unions are correct. |
| C06 | **reproduced**, wider than the ledger states | Without a comparator undefined and null sort first (as `""`); with one, undefined is passed to it and sorted by its result. This holds for `sort`, `toSorted`, holes (also sorted first; `toSorted` keeps them as holes), `any[]`, arrays held as `any`, object and boolean unions, and `.par.sort(cmp)` at 1, 2 and 4 threads. Defined values, null reaching a comparator, and `.par.sort()` without a comparator are correct. |
| C07 | **reproduced** | Four `.par.map` tasks constructing local `AbortController`s (or `AbortSignal.abort()` signals) fail with `TypeError: Illegal constructor` in 20/20 runs at 2 and at 4 threads, 0/20 at 1 thread; the main-thread control passes 5/5 at 1 and 4 threads. |

New defects seen on the way are listed under "New defects seen (not fixed)": NEW-1, `for...of` over an `any` iterable drains it eagerly (wrong side-effect order, no `return()` on break, out of memory on an infinite iterator), plus three smaller ones.

## Environment and provenance

- Worktree: `/home/bryan/projects/parallelscript/.claude/worktrees/agent-a530c2db85ed065e7`, branch `worktree-agent-a530c2db85ed065e7`.
- `git rev-parse HEAD`: `5d0bd01bc54aa71c298229525b3a08fc05a83837`. The only changes in the worktree are untracked files under `docs/agent/hardening-evidence/` (the programs, this report, the `.done` marker).
- The worktree had no submodules, so the first `pts build` of a control failed: `pts: hashing pts/third_party/ryu/ryu/d2s.c (are the submodules checked out? run git submodule update --init pts/third_party)`. Following PROGRESS.md's fresh-clone note, each vendored submodule was checked out inside this worktree, borrowing objects read-only from the main repo's module stores:
  `git -C <worktree> submodule update --init --reference /home/bryan/projects/parallelscript/.git/modules/pts/third_party/<lib> pts/third_party/<lib>` for `ryu` (72e18e8d1), `bdwgc` (4fab5386d), `libuv` (840404ce8), `libbacktrace` (0b9b49cf4) and `pcre2` (6f9d7c137), which are the pinned gitlink commits. `_submodules/TypeScript` was not initialised. (The compiler panics of C01/C02 happen before the runtime is hashed, so they reproduced even before this step.)
- CLI: `cd <worktree> && GOMAXPROCS=2 go build -p 2 -o /tmp/pts-h01b-repro.OkBkyK/pts ./cmd/pts` (exit 0).
  - sha256 `57e7a278d77f3dc3cde610934df827084cd50ffed85f6a2d1dc9a44467ebd903`
  - `pts version`: `pts 0.7.0 (commit 5d0bd01bc, modified; typescript-go 7.0.2)`. "modified" refers to the untracked evidence files.
- `go version go1.26.9 linux/amd64`; `node --version` `v24.21.0` (`~/.local/node/bin`); `clang --version | head -1` `Ubuntu clang version 18.1.3 (1ubuntu1)`.
- Scratch: `/tmp/pts-h01b-repro.OkBkyK` (raw outputs per program under `raw/<name>/`, built binaries under `bin/`, Node copies under `node/`, boundary checks under `explore/`).
- Host note: the first build attempt failed with `compile: writing output: write $WORK/b189/_pkg_.a: no space left on device`, with 510 MB free on `/` (the Go build cache `~/.cache/go-build` is 144 GB; `/tmp/pts-tests-*` held about 8.5 GB). A minute later 3.1 GB were free (released by another process) and the build succeeded. I deleted nothing outside my scratch dir. Fix-task worktrees will need both disk space and `git submodule update --init pts/third_party`.

## How every program was run

From the worktree root, with `S=/tmp/pts-h01b-repro.OkBkyK`, `export PATH="$HOME/.local/node/bin:$HOME/.local/go/bin:$PATH"`, `export PTS_HOME=<worktree root>` and `set -o pipefail`:

```sh
$S/pts build docs/agent/hardening-evidence/probes/h01b/<name>.pts -o $S/bin/<name>     # build exit code
PTS_THREADS=<first header thread count> timeout 60 $S/bin/<name>                        # run exit code, stdout, stderr
cp <name>.pts $S/node/<name>.mts            # oracle node-self; for node:<x>.oracle.mts, cp that file instead
(cd $S/node && timeout 60 node --no-warnings --experimental-transform-types <name>.mts) # Node exit code, stdout
cmp -s run.stdout node.stdout               # "identical" below means byte-identical stdout
```

Programs whose header has several thread counts or more than one run were then run `runs` times at each `threads` count: `PTS_THREADS=$t timeout 60 $S/bin/<name>`. A run fails when its exit code isn't 0 or its stdout isn't byte-identical to the oracle's Node stdout.
The outputs quoted below come from a final single-run pass that rebuilt and reran all 61 programs after the last edit to any of them. The multi-run counts come from a final multi-run pass over the 6 programs that ask for one, after their last edit. Panic frames are quoted as `function file:line`, with the worktree prefix removed. In the evidence blocks below, each panic shows the panic line and the top two `pts/` frames; the full top frames are shown once per row. All panics in a row have the same panic line and top six frames (compared by md5).

### Predecessor drafts

I reviewed the killed predecessor's 37 `.pts` / 3 `.oracle.mts` drafts (read-only, `.claude/worktrees/agent-a17b90036b7e4d65d/docs/agent/hardening-evidence/probes/h01b/`). Their expectations were sound, but none had the required header. I wrote every program here myself, with the header, my own identifiers and data, and re-executed everything.
- Shapes kept from the drafts, rewritten: the C01 generic-arrow, mapped-class and control programs; C02's getter iterators, typed `IteratorResult` and break/`return()` programs; C05's union, tuple, depth and `flatMap` programs; C06's default, comparator, `toSorted`, null and `.par.sort` programs; C07's two `.par.map` programs and the sequential control.
- Dropped as redundant: the class-field generic arrow (it panics in the same way, scratch `x07`) and the `any`-object void getter, which `c02-red-object-void-getter*` covers.
- Changed: C06 comparator probes log every call and print an order-independent summary instead of a single flag.
- New here: the C01 `any`-target, keyed-record, function-expression, non-generic-method, falsy-value and evaluation-order programs and four C01 controls; the C02 class, `never`, unread-getter and generator programs and four C02 controls; the C05 holes and `.par.flatMap` programs; the C06 holes, `any`, other-kinds, null-comparator and `.par.sort()` programs.

One correction of my own: my first `c02-control-object-void-method` used an object-literal method that only throws. tsc infers `never` for that, not `void`, so it controlled nothing. It now declares `: void`.

## C01: erased-record logical assignment panic

**Verdict: reproduced, and wider than the ledger's "erased record" framing.**

Triggers. All 12 red programs fail `pts build` with exit 2, the same panic line and the same frames, and produce no binary:
- `??=` and `||=`, in expression form and statement form, on a `Record<string, T[]>` inside a generic arrow (`c01-red-generic-arrow-*`, including the falsy-value and evaluation-order programs);
- a `Record<K, number[]>` with `K extends string` in a generic arrow (`c01-red-generic-arrow-keyed-record`);
- a generic function **expression** (`c01-red-generic-function-expression-nullish-assign`);
- a mapped-type field `{ [K in keyof E]?: ... }` of a generic class, from a generic method (`c01-red-generic-class-mapped-*-assign`) **and from a non-generic method** (`c01-red-generic-class-mapped-nongeneric-method`), so the generic method is not the trigger;
- **a plain `any` object with no generics**: `box.list ??= []` and `box.name ||= "x"` (`c01-red-any-property-nullish-assign`), and `box[key] ||= 5` (`c01-red-any-element-or-assign`).

Controls. All 9 build, exit 0 and print byte-identical stdout to Node:
- the same idiom in generic function **declarations** (`c01-control-generic-function`, the reviewer's p08g shape), in a generic method of a non-generic class (`c01-control-generic-method`), and on a generic class's `Record<string, T[]>` field with non-generic methods (`c01-control-generic-class-record`). None of these is erased; D-078 erases generic arrows and function expressions.
- non-generic arrows and class (`c01-control-nongeneric`), and the same receiver/key/RHS evaluation-order program, non-generic (`c01-control-nongeneric-eval-order`, Node order `receiver key rhs receiver key receiver key rhs`). This gives H02 a known-good reference for the order.
- reading `record[key] ?? []` / `|| []` without assigning (`c01-control-generic-arrow-read-fallback`); `&&=` on the same erased record and `&&=` / `+=` on the same `any` object (`c01-control-dynamic-and-assign`); plain `=` after `=== undefined` (`c01-control-generic-arrow-plain-assign`); `??=` inside a generic arrow on a record type that doesn't mention `T` (`c01-control-generic-arrow-concrete-record`).

Where it fails (source, read-only). `compoundAssignment` sends every keyed place to `recordElementOrSet`:

```go
// pts/lower/lower.go:2791-2792
if target.key != nil && (operator == ast.KindBarBarToken || operator == ast.KindQuestionQuestionToken) {
    return l.recordElementOrSet(target, expr.Right, operator == ast.KindQuestionQuestionToken)
```

Its `recordFallback` then asserts a hash-map type:

```go
// pts/lower/records.go:280
mt := record.Type().(*ir.MapType)
```

The traces show that dynamic places (an erased record or an `any` object, whose IR type is the primitive `ir.Value`) reach this path for both property and element access. `&&=` goes through `readAndStore` instead, which is why it works.

Full top frames (identical for all 12; shown for `c01-red-any-property-nullish-assign`):

```text
panic: interface conversion: ir.Type is *ir.Primitive, not *ir.MapType
  pts/lower.(*lowerer).recordFallback pts/lower/records.go:280
  pts/lower.(*lowerer).compoundAssignment.(*lowerer).recordElementOrSet.func3 pts/lower/records.go:309
  pts/lower.(*lowerer).withObject pts/lower/lower.go:2976
  pts/lower.(*lowerer).recordElementOrSet pts/lower/records.go:307
  pts/lower.(*lowerer).compoundAssignment pts/lower/lower.go:2792
  pts/lower.(*lowerer).binary pts/lower/lower.go:2668
  pts/lower.(*lowerer).expressionOf pts/lower/lower.go:1311
  pts/lower.(*lowerer).expression.func1 pts/lower/lower.go:1262
  pts/lower.(*lowerer).withArgumentTemps pts/lower/lower.go:1765
  pts/lower.(*lowerer).expression pts/lower/lower.go:1262
  pts/lower.(*lowerer).statement pts/lower/lower.go:399
  pts/lower.(*lowerer).block pts/lower/lower.go:379
```

Scratch boundary checks (not deliverables; same commands):
- `x07`, a generic arrow held in a class field with `||=`, panics identically.
- `x02`, a concrete `Record<string, number[]>` inside a generic arrow keyed by an erased callback's result, `(counts[keyOf(items[i])] ??= []).push(i)` with `keyOf: (item: T) => string`, is not a panic but a diagnostic: `x02-generic-arrow-concrete-record.pts:6:17 - error PTS1001: a key of type value is not supported yet` (exit 1; Node prints `{ a: [ 0, 2 ], b: [ 1 ] }`). See NEW-3.

### C01 evidence, per program (final pass)

#### `c01-control-dynamic-and-assign`

build exit 0; run exit 0 (PTS_THREADS=1); node exit 0; stdout identical.

Stdout (byte-identical in PTS and Node; PTS stderr empty):
```text
5 0 { one: 5, zero: 0 }
{ count: 6 }
```

#### `c01-control-generic-arrow-concrete-record`

build exit 0; run exit 0 (PTS_THREADS=1); node exit 0; stdout identical.

Stdout (byte-identical in PTS and Node; PTS stderr empty):
```text
{ a: [ 0, 2 ], b: [ 1 ] }
```

#### `c01-control-generic-arrow-plain-assign`

build exit 0; run exit 0 (PTS_THREADS=1); node exit 0; stdout identical.

Stdout (byte-identical in PTS and Node; PTS stderr empty):
```text
{ odd: [ 1, 3, 5 ], even: [ 2, 4 ] }
```

#### `c01-control-generic-arrow-read-fallback`

build exit 0; run exit 0 (PTS_THREADS=1); node exit 0; stdout identical.

Stdout (byte-identical in PTS and Node; PTS stderr empty):
```text
2 0 2 0
```

#### `c01-control-generic-class-record`

build exit 0; run exit 0 (PTS_THREADS=1); node exit 0; stdout identical.

Stdout (byte-identical in PTS and Node; PTS stderr empty):
```text
{ a: [ 1, 3 ], b: [ 2 ] }
{ x: [ 'p', 'r' ], y: [ 'q' ] }
```

#### `c01-control-generic-function`

build exit 0; run exit 0 (PTS_THREADS=1); node exit 0; stdout identical.

Stdout (byte-identical in PTS and Node; PTS stderr empty):
```text
{ odd: [ 1, 3, 5 ], even: [ 2, 4 ] }
{ '1': [ 'a' ], '2': [ 'bb', 'cc' ] }
```

#### `c01-control-generic-method`

build exit 0; run exit 0 (PTS_THREADS=1); node exit 0; stdout identical.

Stdout (byte-identical in PTS and Node; PTS stderr empty):
```text
{ odd: [ 1, 3, 5 ], even: [ 2, 4 ] }
```

#### `c01-control-nongeneric-eval-order`

build exit 0; run exit 0 (PTS_THREADS=1); node exit 0; stdout identical.

Stdout (byte-identical in PTS and Node; PTS stderr empty):
```text
receiver key rhs receiver key receiver key rhs
{ a: [ 1, 2 ], b: [ 3 ] }
```

#### `c01-control-nongeneric`

build exit 0; run exit 0 (PTS_THREADS=1); node exit 0; stdout identical.

Stdout (byte-identical in PTS and Node; PTS stderr empty):
```text
{ odd: [ 1, 3, 5 ], even: [ 2, 4 ] }
{ odd: [ 1, 3, 5 ], even: [ 2, 4 ] }
{ a: [ 1, 3 ], b: [ 2 ] }
```

#### `c01-red-any-element-or-assign`

build exit 2; node exit 0.

```text
panic: interface conversion: ir.Type is *ir.Primitive, not *ir.MapType
  pts/lower.(*lowerer).recordFallback pts/lower/records.go:280
  pts/lower.(*lowerer).compoundAssignment.(*lowerer).recordElementOrSet.func3 pts/lower/records.go:309
```

Node stdout:
```text
{ count: 5 }
```

#### `c01-red-any-property-nullish-assign`

build exit 2; node exit 0.

```text
panic: interface conversion: ir.Type is *ir.Primitive, not *ir.MapType
  pts/lower.(*lowerer).recordFallback pts/lower/records.go:280
  pts/lower.(*lowerer).compoundAssignment.(*lowerer).recordElementOrSet.func3 pts/lower/records.go:309
```

Node stdout:
```text
{ list: [ 1 ], name: 'x' }
```

#### `c01-red-generic-arrow-eval-order`

build exit 2; node exit 0.

```text
panic: interface conversion: ir.Type is *ir.Primitive, not *ir.MapType
  pts/lower.(*lowerer).recordFallback pts/lower/records.go:280
  pts/lower.(*lowerer).compoundAssignment.(*lowerer).recordElementOrSet.func3 pts/lower/records.go:309
```

Node stdout:
```text
receiver key rhs receiver key receiver key rhs
{ a: [ 1, 2 ], b: [ 3 ] }
```

#### `c01-red-generic-arrow-falsy-values`

build exit 2; node exit 0.

```text
panic: interface conversion: ir.Type is *ir.Primitive, not *ir.MapType
  pts/lower.(*lowerer).recordFallback pts/lower/records.go:280
  pts/lower.(*lowerer).compoundAssignment.(*lowerer).recordElementOrSet.func3 pts/lower/records.go:309
```

Node stdout:
```text
0 6 7 { zero: 6, fresh: 7 }
"" b { empty: 'b' }
```

#### `c01-red-generic-arrow-keyed-record`

build exit 2; node exit 0.

```text
panic: interface conversion: ir.Type is *ir.Primitive, not *ir.MapType
  pts/lower.(*lowerer).recordFallback pts/lower/records.go:280
  pts/lower.(*lowerer).compoundAssignment.(*lowerer).recordElementOrSet.func3 pts/lower/records.go:309
```

Node stdout:
```text
{ a: [ 0, 2 ], b: [ 1 ] }
```

#### `c01-red-generic-arrow-nullish-assign`

build exit 2; node exit 0.

```text
panic: interface conversion: ir.Type is *ir.Primitive, not *ir.MapType
  pts/lower.(*lowerer).recordFallback pts/lower/records.go:280
  pts/lower.(*lowerer).compoundAssignment.(*lowerer).recordElementOrSet.func3 pts/lower/records.go:309
```

Node stdout:
```text
{ odd: [ 1, 3, 5 ], even: [ 2, 4 ] }
```

#### `c01-red-generic-arrow-or-assign`

build exit 2; node exit 0.

```text
panic: interface conversion: ir.Type is *ir.Primitive, not *ir.MapType
  pts/lower.(*lowerer).recordFallback pts/lower/records.go:280
  pts/lower.(*lowerer).compoundAssignment.(*lowerer).recordElementOrSet.func3 pts/lower/records.go:309
```

Node stdout:
```text
{ odd: [ 1, 3, 5 ], even: [ 2, 4 ] }
```

#### `c01-red-generic-arrow-statement-nullish-assign`

build exit 2; node exit 0.

```text
panic: interface conversion: ir.Type is *ir.Primitive, not *ir.MapType
  pts/lower.(*lowerer).recordFallback pts/lower/records.go:280
  pts/lower.(*lowerer).compoundAssignment.(*lowerer).recordElementOrSet.func3 pts/lower/records.go:309
```

Node stdout:
```text
{ odd: [ 1, 3, 5 ], even: [ 2, 4 ] }
```

#### `c01-red-generic-class-mapped-nongeneric-method`

build exit 2; node exit 0.

```text
panic: interface conversion: ir.Type is *ir.Primitive, not *ir.MapType
  pts/lower.(*lowerer).recordFallback pts/lower/records.go:280
  pts/lower.(*lowerer).compoundAssignment.(*lowerer).recordElementOrSet.func3 pts/lower/records.go:309
```

Node stdout:
```text
{ a: [ 1, 3 ], b: [ 2 ] }
```

#### `c01-red-generic-class-mapped-nullish-assign`

build exit 2; node exit 0.

```text
panic: interface conversion: ir.Type is *ir.Primitive, not *ir.MapType
  pts/lower.(*lowerer).recordFallback pts/lower/records.go:280
  pts/lower.(*lowerer).compoundAssignment.(*lowerer).recordElementOrSet.func3 pts/lower/records.go:309
```

Node stdout:
```text
{ a: [ 1, 2 ], b: [ 'x' ] }
```

#### `c01-red-generic-class-mapped-or-assign`

build exit 2; node exit 0.

```text
panic: interface conversion: ir.Type is *ir.Primitive, not *ir.MapType
  pts/lower.(*lowerer).recordFallback pts/lower/records.go:280
  pts/lower.(*lowerer).compoundAssignment.(*lowerer).recordElementOrSet.func3 pts/lower/records.go:309
```

Node stdout:
```text
{ a: [ 1, 2 ], b: [ 'x' ] }
```

#### `c01-red-generic-function-expression-nullish-assign`

build exit 2; node exit 0.

```text
panic: interface conversion: ir.Type is *ir.Primitive, not *ir.MapType
  pts/lower.(*lowerer).recordFallback pts/lower/records.go:280
  pts/lower.(*lowerer).compoundAssignment.(*lowerer).recordElementOrSet.func3 pts/lower/records.go:309
```

Node stdout:
```text
{ odd: [ 1, 3, 5 ], even: [ 2, 4 ] }
```


## C02: dynamic IteratorResult getter / void panic

**Verdict: reproduced. The panic's root cause is a getter of type `void` or `never`; the iterator protocol is incidental.**

Triggers (panic). All 6 programs fail `pts build` with exit 2 and the same panic line and frames:
- the Grok shape with my own code: an `any` iterable whose result has `get done()` (true) and a `get value()` that only throws (`c02-red-any-iterator-done-getter-throwing-value`, Node `read done` / `end`);
- the same with a data property `done: true` (`c02-red-any-iterator-data-done-throwing-value`, Node `end`);
- **no iteration and no `any`**: an object-literal getter that only throws, read directly (`c02-red-object-void-getter`) or **never read** (`c02-red-object-void-getter-unread`); a class getter that only throws (`c02-red-class-void-getter`); a getter declared `: never` (`c02-red-object-never-getter`).

For a body that only throws, tsc infers `void` for a getter but `never` for a function expression, an arrow or an object-literal method (its `mayReturnNever` rule). A getter declared `: never` reaches cgen as `void` as well, since the message is the same. The panic happens while cgen emits the run-time check table (`checkDefinitions`, from `header`), so it doesn't depend on the getter being read:

```go
// pts/cgen/checks.go:304-305
default:
    panic("cgen: no check for " + t.String())
```

Full top frames (identical for all 6; shown for `c02-red-object-void-getter-unread`):

```text
panic: cgen: no check for void
  pts/cgen.(*emitter).checkDefinition pts/cgen/checks.go:305
  pts/cgen.(*emitter).checkDefinitions pts/cgen/checks.go:277
  pts/cgen.(*emitter).header pts/cgen/cgen.go:159
  pts/cgen.Emit pts/cgen/cgen.go:38
  pts/driver.EmitC pts/driver/driver.go:39
  pts/driver.Build pts/driver/driver.go:45
```

Not a panic, but red against Node:
- **Typed variant** (`c02-red-typed-iteratorresult-getters`, `next(): IteratorResult<number>` returning `get done(): true` / `get value(): number`): `pts build` exit 1, `c02-red-typed-iteratorresult-getters.pts:11:24 - error PTS1001: converting object4 to object1 | object2 is not supported yet`. This is the known D-146 limitation (PROGRESS "Known issues"). It is source-located, which the ledger's acceptance allows for unsupported input, but it names internal shapes (`object4`, ledger V08). The reviewer's `07_residual.pts` gives the same message at its `5:16`.
- **Getter sequence through `any`** (`c02-red-any-iterator-getter-sequence`, done = false, false, true; value getters return numbers, so nothing is `void`): no panic, exit 0. Within each step PTS reads `done` before `value` and never reads the done result's `value`, as the ledger's acceptance requires. But all three steps run before the loop body: PTS prints `read done 0, read value 0, read done 1, read value 1, read done 2, item 0, item 10, end`, where Node interleaves `item 0` and `item 10` after each step. That is NEW-1, and so are the two related red programs `c02-red-any-iterator-break-return` and `c02-red-any-generator-break`.

Controls. All 7 build, exit 0 and print byte-identical stdout to Node:
- the same throwing getter declared `: number` and read through `any` (`c02-control-number-getter-throws`); an object-literal method declared `: void` that only throws (`c02-control-object-void-method`); a `void`-typed data property (`c02-control-void-data-property`). So `void` is a problem only as a getter's type.
- data-property `{ done, value }` results through `any` (`c02-control-any-iterator-data-properties`; it doesn't log in `next()`, so the drain isn't visible).
- the getter-result sequence through a typed `Iterable<number>` class with `next(): any` (`c02-control-typed-iterator-getter-results`). It is identical to Node: `done` before `value`, lazy steps, the done result's `value` never read.
- a typed iterator with `break` and `return()` (`c02-control-typed-iterator-break-return`), and a generator through a typed `Iterable<number>` parameter with `break` (`c02-control-iterable-param-generator-break`). Both are lazy and closed on break, as in Node.

In the Grok shape `done` is already true on the first step, so draining makes no difference to it. An inference, not checked: the getter-sequence program shows that the drain reads `done` first and stops at the done result without reading its `value`. So once the panic is fixed, `c02-red-any-iterator-done-getter-throwing-value` and `-data-done-throwing-value` should go GREEN without NEW-1, while `c02-red-any-iterator-getter-sequence` also needs NEW-1 fixed.

### C02 evidence, per program (final pass)

#### `c02-control-any-iterator-data-properties`

build exit 0; run exit 0 (PTS_THREADS=1); node exit 0; stdout identical.

Stdout (byte-identical in PTS and Node; PTS stderr empty):
```text
item 0
item 10
end
```

#### `c02-control-iterable-param-generator-break`

build exit 0; run exit 0 (PTS_THREADS=1); node exit 0; stdout identical.

Stdout (byte-identical in PTS and Node; PTS stderr empty):
```text
yield 0
item 0
yield 1
item 1
closed
[ 0, 1 ]
```

#### `c02-control-number-getter-throws`

build exit 0; run exit 0 (PTS_THREADS=1); node exit 0; stdout identical.

Stdout (byte-identical in PTS and Node; PTS stderr empty):
```text
caught thrown by getter
```

#### `c02-control-object-void-method`

build exit 0; run exit 0 (PTS_THREADS=1); node exit 0; stdout identical.

Stdout (byte-identical in PTS and Node; PTS stderr empty):
```text
caught thrown by method
```

#### `c02-control-typed-iterator-break-return`

build exit 0; run exit 0 (PTS_THREADS=1); node exit 0; stdout identical.

Stdout (byte-identical in PTS and Node; PTS stderr empty):
```text
next 0
item 0
next 1
item 1
return
end
```

#### `c02-control-typed-iterator-getter-results`

build exit 0; run exit 0 (PTS_THREADS=1); node exit 0; stdout identical.

Stdout (byte-identical in PTS and Node; PTS stderr empty):
```text
read done 0
read value 0
item 0
read done 1
read value 1
item 10
read done 2
end
```

#### `c02-control-void-data-property`

build exit 0; run exit 0 (PTS_THREADS=1); node exit 0; stdout identical.

Stdout (byte-identical in PTS and Node; PTS stderr empty):
```text
undefined
```

#### `c02-red-any-generator-break`

build exit 0; run exit 1 (PTS_THREADS=1); node exit 0; stdout differs.

PTS stdout:
```text
yield 0
yield 1
yield 2
yield 3
closed
```
PTS stderr:
```text
Uncaught Error: resumed after the loop broke
    at docs/agent/hardening-evidence/probes/h01b/c02-red-any-generator-break.pts:12
    at docs/agent/hardening-evidence/probes/h01b/c02-red-any-generator-break.pts:22
```

Node stdout:
```text
yield 0
item 0
yield 1
item 1
closed
end
```

#### `c02-red-any-iterator-break-return`

build exit 0; run exit 0 (PTS_THREADS=1); node exit 0; stdout differs.

PTS stdout:
```text
next 0
next 1
next 2
next 3
next 4
item 0
item 1
end
```

Node stdout:
```text
next 0
item 0
next 1
item 1
return
end
```

#### `c02-red-any-iterator-data-done-throwing-value`

build exit 2; node exit 0.

```text
panic: cgen: no check for void
  pts/cgen.(*emitter).checkDefinition pts/cgen/checks.go:305
  pts/cgen.(*emitter).checkDefinitions pts/cgen/checks.go:277
```

Node stdout:
```text
end
```

#### `c02-red-any-iterator-done-getter-throwing-value`

build exit 2; node exit 0.

```text
panic: cgen: no check for void
  pts/cgen.(*emitter).checkDefinition pts/cgen/checks.go:305
  pts/cgen.(*emitter).checkDefinitions pts/cgen/checks.go:277
```

Node stdout:
```text
read done
end
```

#### `c02-red-any-iterator-getter-sequence`

build exit 0; run exit 0 (PTS_THREADS=1); node exit 0; stdout differs.

PTS stdout:
```text
read done 0
read value 0
read done 1
read value 1
read done 2
item 0
item 10
end
```

Node stdout:
```text
read done 0
read value 0
item 0
read done 1
read value 1
item 10
read done 2
end
```

#### `c02-red-class-void-getter`

build exit 2; node exit 0.

```text
panic: cgen: no check for void
  pts/cgen.(*emitter).checkDefinition pts/cgen/checks.go:305
  pts/cgen.(*emitter).checkDefinitions pts/cgen/checks.go:277
```

Node stdout:
```text
caught thrown by getter
```

#### `c02-red-object-never-getter`

build exit 2; node exit 0.

```text
panic: cgen: no check for void
  pts/cgen.(*emitter).checkDefinition pts/cgen/checks.go:305
  pts/cgen.(*emitter).checkDefinitions pts/cgen/checks.go:277
```

Node stdout:
```text
caught thrown by getter
```

#### `c02-red-object-void-getter`

build exit 2; node exit 0.

```text
panic: cgen: no check for void
  pts/cgen.(*emitter).checkDefinition pts/cgen/checks.go:305
  pts/cgen.(*emitter).checkDefinitions pts/cgen/checks.go:277
```

Node stdout:
```text
caught thrown by getter
```

#### `c02-red-object-void-getter-unread`

build exit 2; node exit 0.

```text
panic: cgen: no check for void
  pts/cgen.(*emitter).checkDefinition pts/cgen/checks.go:305
  pts/cgen.(*emitter).checkDefinitions pts/cgen/checks.go:277
```

Node stdout:
```text
1
```

#### `c02-red-typed-iteratorresult-getters`

build exit 1; node exit 0.

PTS build stderr:
```text
docs/agent/hardening-evidence/probes/h01b/c02-red-typed-iteratorresult-getters.pts:11:24 - error PTS1001: converting object4 to object1 | object2 is not supported yet
```

Node stdout:
```text
read done
end
```


## C05: flat / flatMap over union, tuple and heterogeneous arrays

**Verdict: reproduced.** All 6 red programs build and exit 0 with wrong output. All 3 controls are byte-identical to Node, and `.par.flatMap` matches the oracle at 1, 2 and 4 threads.

Red:
- `(number | number[])[]` and `(string | string[])[]`: `flat()` returns the elements unflattened (`c05-red-flat-union-array`).
- `[string, number][]` and `[number, number][]`: `flat()` returns the tuples unflattened (`c05-red-flat-tuple-array`).
- `(number | number[] | number[][])[]` at depth 0, default, 1, 2 and Infinity: every call returns the input unchanged. Depth 0 is right only because it is a copy. The inferred `[1, [2, [3, [4]]]]` is unchanged at depth 1, 2 and Infinity (`c05-red-flat-heterogeneous-depths`).
- `(number | number[])[][]`: depth 1 is right (level 1 is all arrays), but depth 2 and Infinity stop at the union level and print `[ 1, [ 2 ], 3 ]` (`c05-red-flat-nested-union-depth`).
- `flatMap` whose mapper result is a union (identity over a union array; `x === 1 ? x : [[x]]` with `flatMap<number | number[]>`): not flattened (`[ 1, [ 2, 3 ], 4 ]`, `[ 1, [ [ 2 ] ] ]`). The mapper that always returns an array is right (`c05-red-flatmap-union`, second output line).
- A union array with a hole: the hole is skipped as in JS, but the array element isn't spread (`c05-red-flat-union-holes`).

Controls:
- homogeneous `number[][]` / `number[][][]` at depth 1, 2 and Infinity, `flatMap` with array mappers, and a homogeneous array with a hole (`c05-control-flat-homogeneous`);
- the red shapes held as `any`, which go through the runtime's dynamic `flat`/`flatMap` (`c05-control-flat-any`);
- `.par.flatMap` over the union array with an identity mapper, and with a `[[x]]` mapper, which flattens once (`c05-control-par-flatmap-union`, oracle `c05-control-par-flatmap-union.oracle.mts`; 1 of 1 runs identical at each of 1, 2 and 4 threads).

Where it fails (source, read-only):
- `flat` (`pts/lower/arrays.go:510-529`, the type walk at `:520-527`) computes the result type by walking `*ir.Array` element types `depth` times and stops at the first non-array (a union or a tuple).
- The runtime's `flatten_into` (`pts/runtime/src/array.c:871-888`) recurses only when `depth >= 1 && kind == PTS_FIELD_OBJECT && ...->type->kind == PTS_KIND_ARRAY` (`:882`). The outputs are consistent with union-typed slots (boxed values) and tuples not meeting that condition. This is an inference from source and outputs; I didn't inspect the generated C.

### C05 evidence, per program (final pass)

#### `c05-control-flat-any`

build exit 0; run exit 0 (PTS_THREADS=1); node exit 0; stdout identical.

Stdout (byte-identical in PTS and Node; PTS stderr empty):
```text
[ 1, 2, [ 3 ] ]
[ 1, 2, 3 ]
[ 1, 2, 3 ]
[ 'b', 2, 'a', 1 ]
[ 1, 2, 3, 4 ]
[ 1, [ 2 ] ]
```

#### `c05-control-flat-homogeneous`

build exit 0; run exit 0 (PTS_THREADS=1); node exit 0; stdout identical.

Stdout (byte-identical in PTS and Node; PTS stderr empty):
```text
[ 1, 2, 3 ]
[ [ 1 ], [ 2 ], [ 3 ] ]
[ 1, 2, 3 ]
[ 1, 2, 3 ]
[ 1, 2, 3 ]
[ 1, 1, 2, 2, 3, 3 ]
[ 1, 2, 3 ]
```

#### `c05-control-par-flatmap-union`

build exit 0; run exit 0 (PTS_THREADS=1); node exit 0; stdout identical.

Stdout (byte-identical in PTS and Node; PTS stderr empty):
```text
[ 1, 2, 3, 4 ]
[ [ 1 ], [ 2 ] ]
```

#### `c05-red-flat-heterogeneous-depths`

build exit 0; run exit 0 (PTS_THREADS=1); node exit 0; stdout differs.

PTS stdout:
```text
[ 1, [ 2 ], [ [ 3 ] ] ]
[ 1, [ 2 ], [ [ 3 ] ] ]
[ 1, [ 2 ], [ [ 3 ] ] ]
[ 1, [ 2 ], [ [ 3 ] ] ]
[ 1, [ 2 ], [ [ 3 ] ] ]
[ 1, [ 2, [ 3, [Array] ] ] ]
[ 1, [ 2, [ 3, [Array] ] ] ]
[ 1, [ 2, [ 3, [Array] ] ] ]
```

Node stdout:
```text
[ 1, [ 2 ], [ [ 3 ] ] ]
[ 1, 2, [ 3 ] ]
[ 1, 2, [ 3 ] ]
[ 1, 2, 3 ]
[ 1, 2, 3 ]
[ 1, 2, [ 3, [ 4 ] ] ]
[ 1, 2, 3, [ 4 ] ]
[ 1, 2, 3, 4 ]
```

#### `c05-red-flatmap-union`

build exit 0; run exit 0 (PTS_THREADS=1); node exit 0; stdout differs.

PTS stdout:
```text
[ 1, [ 2, 3 ], 4 ]
[ 1, 1, 2, 3, 4, 4 ]
[ 1, [ [ 2 ] ] ]
```

Node stdout:
```text
[ 1, 2, 3, 4 ]
[ 1, 1, 2, 3, 4, 4 ]
[ 1, [ 2 ] ]
```

#### `c05-red-flat-nested-union-depth`

build exit 0; run exit 0 (PTS_THREADS=1); node exit 0; stdout differs.

PTS stdout:
```text
[ 1, [ 2 ], 3 ]
[ 1, [ 2 ], 3 ]
[ 1, [ 2 ], 3 ]
```

Node stdout:
```text
[ 1, [ 2 ], 3 ]
[ 1, 2, 3 ]
[ 1, 2, 3 ]
```

#### `c05-red-flat-tuple-array`

build exit 0; run exit 0 (PTS_THREADS=1); node exit 0; stdout differs.

PTS stdout:
```text
[ [ 'b', 2 ], [ 'a', 1 ] ]
[ [ 1, 2 ], [ 3, 4 ] ]
```

Node stdout:
```text
[ 'b', 2, 'a', 1 ]
[ 1, 2, 3, 4 ]
```

#### `c05-red-flat-union-array`

build exit 0; run exit 0 (PTS_THREADS=1); node exit 0; stdout differs.

PTS stdout:
```text
[ 1, [ 2, 3 ], 4, [ 5 ] ]
[ [ 'a' ], 'b', [ 'c' ] ]
```

Node stdout:
```text
[ 1, 2, 3, 4, 5 ]
[ 'a', 'b', 'c' ]
```

#### `c05-red-flat-union-holes`

build exit 0; run exit 0 (PTS_THREADS=1); node exit 0; stdout differs.

PTS stdout:
```text
[ 1, <1 empty item>, [ 2, 3 ] ] [ 1, [ 2, 3 ] ]
```

Node stdout:
```text
[ 1, <1 empty item>, [ 2, 3 ] ] [ 1, 2, 3 ]
```


## C06: sort / toSorted with undefined and null

**Verdict: reproduced, and wider than the ledger states.** All 8 red programs build and exit 0 with wrong output. All 3 controls are byte-identical to Node or the oracle, including `.par.sort()` at 1, 2 and 4 threads.

Red:
- `sort()` with no comparator puts undefined first: `[ undefined, undefined, 1, 20, 3 ]` and `[ undefined, 'a', 'v', 'z' ]` (`c06-red-sort-undefined-default`). It sorts as `""`, not as `"undefined"`, which would land between `'a'` and `'v'`.
- `sort(comparator)` passes undefined to the comparator and orders it by the comparator's result (`c06-red-sort-undefined-comparator`). Every call is logged; the program prints the distinct arguments seen and the number of calls that got undefined, because the ledger says pair order need not equal V8's. PTS: `arguments seen: 1 2 3 undefined`, `calls with undefined: 5`. Node: `1 2 3`, `0`.
- `toSorted()` with and without a comparator, same as `sort`; the source array is unchanged, which is correct (`c06-red-tosorted-undefined`).
- null without a comparator sorts first instead of as `"null"`: `[ null, 1, 20, 3 ]` vs Node `[ 1, 20, 3, null ]`, and `[ null, 'a', 'nuk', 'nz' ]` vs Node `[ 'a', 'nuk', null, 'nz' ]` (`c06-red-sort-null`).
- **Holes** (`c06-red-sort-holes`, `new Array<number \| undefined>(5)` with slots 0, 1, 3 set): `toSorted()` gives `[ undefined, <2 empty items>, 1, 3 ]`, where Node gives `[ 1, 3, undefined, undefined, undefined ]`. That is two errors: holes and undefined first, and holes kept as holes. `sort()` gives `[ undefined, <2 empty items>, 1, 3 ]`. `sort(cmp)` gives `[ undefined, 1, 3, <2 empty items> ]`: holes correctly last, undefined wrongly first. Node gives `[ 1, 3, undefined, <2 empty items> ]` for both.
- **`any[]` and arrays held as `any`** (dynamic dispatch, `c06-red-sort-any-arrays`): both default sorts give `[ undefined, null, 1, 3, 'b' ]` (Node `[ 1, 3, 'b', null, undefined ]`). The dynamic comparator path passes undefined and doesn't move it last: `[ 3, undefined, 1, null, 'b' ]` (Node `[ 3, 1, null, 'b', undefined ]`).
- **Other element kinds** (`c06-red-sort-undefined-other-kinds`): `(Item | undefined)[]` with `toString` gives `undefined a b` (Node `a b undefined`); `(boolean | undefined)[]` gives `[ undefined, false, true ]` (Node `[ false, true, undefined ]`).
- **`.par.sort(comparator)`** (`c06-red-par-sort-undefined-comparator`, oracle `.oracle.mts`; 6000 values, every tenth undefined): 3 of 3 runs differ at each of 1, 2 and 4 threads. All 9 stdouts are identical (md5 `2187554cf8f1...`, oracle `f523f4909b85...`): `last [ 5997, 5998, 5999 ]`, `comparator saw undefined: true`. At 1 thread `sortInPlace` calls Array's sort. At 2 and 4 threads, with more than 2048 elements, the library's own merge (`mergeRuns`, `pts/lib/parallel.pts:670-673`) also calls `compare(source[right], source[left])` on whatever elements it holds, so a runtime-only fix won't cover this path.

Controls:
- defined values with `sort` / `toSorted`, with and without a comparator (`c06-control-sort-defined`);
- null **is** passed to a comparator, as in JS (`c06-control-sort-null-comparator`: `[ null, 1, 3 ]`, `comparator saw null: true`);
- `.par.sort()` **without** a comparator, on a small array and on 6000 values with 600 undefined and one null (`c06-control-par-sort-default`; 3 of 3 runs identical at each of 1, 2 and 4 threads, md5 equal to the oracle's). It sorts by `String(value)` keys with `compareDefault` (`pts/lib/parallel.pts:185`, `595`), which puts undefined last, so it doubles as a reference implementation.

Where it fails (source, read-only):
- `element_string` (`pts/runtime/src/array.c:688-712`, comment at `:687`: `"" for undefined, null and holes`, test at `:701`) is join's rule, and `pts_array_sort` uses it as the sort key (`:804`).
- `pts_array_sort_with` (`:838-868`) moves only holes to the end (`:843`, `:852`) and passes every other element, undefined included, to `compare_sort` (`:856`).

Verbatim comparator calls (scratch `w03`, not a deliverable, since the call order is engine-specific). Source: `const values: (number | undefined)[] = [3, undefined, 1, undefined, 2]; values.sort((a, b) => { console.log("compare", a, b); return (a ?? -100) - (b ?? -100); }); console.log(values);`

```text
PTS (exit 0)                 Node (exit 0)
compare 3 undefined          compare 1 3
compare undefined 2          compare 2 1
compare 1 undefined          compare 2 3
compare 1 2                  compare 2 1
compare undefined undefined  [ 1, 2, 3, undefined, undefined ]
compare 3 undefined
compare 3 1
compare 3 2
[ undefined, undefined, 1, 2, 3 ]
```

### C06 evidence, per program (final pass)

#### `c06-control-par-sort-default`

build exit 0; run exit 0 (PTS_THREADS=1); node exit 0; stdout identical.

Stdout (byte-identical in PTS and Node; PTS stderr empty):
```text
[ 1, 20, 3, null, undefined ]
first [ 1, 1001, 1002, 1003, 1004 ]
last [ undefined, undefined, undefined ]
null at 5399
```

#### `c06-control-sort-defined`

build exit 0; run exit 0 (PTS_THREADS=1); node exit 0; stdout identical.

Stdout (byte-identical in PTS and Node; PTS stderr empty):
```text
[ 1, 20, 3 ]
[ 'a', 'v', 'z' ]
[ 1, 3, 20 ]
[ 3, 1, 20 ]
```

#### `c06-control-sort-null-comparator`

build exit 0; run exit 0 (PTS_THREADS=1); node exit 0; stdout identical.

Stdout (byte-identical in PTS and Node; PTS stderr empty):
```text
[ null, 1, 3 ]
comparator saw null: true
```

#### `c06-red-par-sort-undefined-comparator`

build exit 0; run exit 0 (PTS_THREADS=1); node exit 0; stdout differs.

PTS stdout:
```text
first [ 1, 2, 3, 4, 5 ]
last [ 5997, 5998, 5999 ]
undefined count 600
defined ascending: true
comparator saw undefined: true
```

Node stdout:
```text
first [ 1, 2, 3, 4, 5 ]
last [ undefined, undefined, undefined ]
undefined count 600
defined ascending: true
comparator saw undefined: false
```

#### `c06-red-sort-any-arrays`

build exit 0; run exit 0 (PTS_THREADS=1); node exit 0; stdout differs.

PTS stdout:
```text
[ undefined, null, 1, 3, 'b' ]
[ undefined, null, 1, 3, 'b' ]
[ 3, undefined, 1, null, 'b' ]
comparator saw undefined: true
```

Node stdout:
```text
[ 1, 3, 'b', null, undefined ]
[ 1, 3, 'b', null, undefined ]
[ 3, 1, null, 'b', undefined ]
comparator saw undefined: false
```

#### `c06-red-sort-holes`

build exit 0; run exit 0 (PTS_THREADS=1); node exit 0; stdout differs.

PTS stdout:
```text
[ 3, undefined, <1 empty item>, 1, <1 empty item> ]
[ undefined, <2 empty items>, 1, 3 ]
[ undefined, 1, 3, <2 empty items> ]
[ undefined, <2 empty items>, 1, 3 ]
```

Node stdout:
```text
[ 3, undefined, <1 empty item>, 1, <1 empty item> ]
[ 1, 3, undefined, undefined, undefined ]
[ 1, 3, undefined, <2 empty items> ]
[ 1, 3, undefined, <2 empty items> ]
```

#### `c06-red-sort-null`

build exit 0; run exit 0 (PTS_THREADS=1); node exit 0; stdout differs.

PTS stdout:
```text
[ null, 1, 20, 3 ]
[ null, 'a', 'nuk', 'nz' ]
[ null, 'a', 'nuk', 'nz' ]
```

Node stdout:
```text
[ 1, 20, 3, null ]
[ 'a', 'nuk', null, 'nz' ]
[ 'a', 'nuk', null, 'nz' ]
```

#### `c06-red-sort-undefined-comparator`

build exit 0; run exit 0 (PTS_THREADS=1); node exit 0; stdout differs.

PTS stdout:
```text
[ undefined, undefined, 1, 2, 3 ]
arguments seen: 1 2 3 undefined
calls with undefined: 5
```

Node stdout:
```text
[ 1, 2, 3, undefined, undefined ]
arguments seen: 1 2 3
calls with undefined: 0
```

#### `c06-red-sort-undefined-default`

build exit 0; run exit 0 (PTS_THREADS=1); node exit 0; stdout differs.

PTS stdout:
```text
[ undefined, undefined, 1, 20, 3 ]
[ undefined, 'a', 'v', 'z' ]
```

Node stdout:
```text
[ 1, 20, 3, undefined, undefined ]
[ 'a', 'v', 'z', undefined ]
```

#### `c06-red-sort-undefined-other-kinds`

build exit 0; run exit 0 (PTS_THREADS=1); node exit 0; stdout differs.

PTS stdout:
```text
undefined a b
[ undefined, false, true ]
```

Node stdout:
```text
a b undefined
[ false, true, undefined ]
```

#### `c06-red-tosorted-undefined`

build exit 0; run exit 0 (PTS_THREADS=1); node exit 0; stdout differs.

PTS stdout:
```text
[ undefined, 1, 20, 3 ]
[ undefined, 'a', 'v', 'z' ]
[ undefined, 1, 3, 20 ]
comparator saw undefined: true
[ 3, undefined, 1, 20 ]
```

Node stdout:
```text
[ 1, 20, 3, undefined ]
[ 'a', 'v', 'z', undefined ]
[ 1, 3, 20, undefined ]
comparator saw undefined: false
[ 3, undefined, 1, 20 ]
```


## C07: independent local AbortController construction race

**Verdict: reproduced.** Both red programs fail in every run at 2 and at 4 threads and never at 1 thread. The sequential control never fails. All runs exit 0; the failures are caught `TypeError`s that the program counts.

Run counts, final pass. Each run has 4 `.par.map` tasks of 20,000 constructions, so 80,000 per run. A failing run is one whose stdout isn't the oracle's 4 `failed 0` lines.

| Program | `PTS_THREADS` | Failing runs | Failed constructions per run (of 80,000) | Per task (of 20,000) | First error (task results) | Exit codes |
|---|---|---|---|---|---|---|
| `c07-red-local-abortcontroller-par` | 1 | 0 / 20 | 0 | 0 | none | 20 x 0 |
| | 2 | 20 / 20 | 1488 .. 1898 | 309 .. 602 | `TypeError: Illegal constructor` (80 / 80) | 20 x 0 |
| | 4 | 20 / 20 | 4069 .. 6373 | 719 .. 1978 | `TypeError: Illegal constructor` (80 / 80) | 20 x 0 |
| `c07-red-abortsignal-abort-par` (`AbortSignal.abort("local")`) | 1 | 0 / 20 | 0 | 0 | none | 20 x 0 |
| | 2 | 20 / 20 | 1294 .. 1851 | 219 .. 525 | `TypeError: Illegal constructor` (80 / 80) | 20 x 0 |
| | 4 | 20 / 20 | 4172 .. 5054 | 657 .. 1667 | `TypeError: Illegal constructor` (80 / 80) | 20 x 0 |
| `c07-control-local-abortcontroller-sequential` (no `.par`) | 1 | 0 / 5 | 0 | 0 | none | 5 x 0 |
| | 4 | 0 / 5 | 0 | 0 | none | 5 x 0 |

An earlier identical series in this session gave the same picture: 20/20 failing at 2 and at 4 threads, 0/20 at 1. The ranges were 1361..1970 and 4410..7301 per run (controllers), and 1139..1906 and 3812..4915 (`AbortSignal.abort`). The reviewer's `abort-local-min.pts`, run unchanged at `PTS_THREADS=4`, printed `[{"passed":9456,"failed":544,"first":"TypeError: Illegal constructor"},{"passed":9487,"failed":513,...},{"passed":9433,"failed":567,...},{"passed":9479,"failed":521,...}]`, exit 0.

Sample failing stdouts (final pass, run 1):

```text
PTS_THREADS=2 c07-red-local-abortcontroller-par     PTS_THREADS=4 c07-red-local-abortcontroller-par
task 0: failed 376 (first TypeError: Illegal constructor)   task 0: failed 876 (first TypeError: Illegal constructor)
task 1: failed 316 (first TypeError: Illegal constructor)   task 1: failed 1071 (first TypeError: Illegal constructor)
task 2: failed 470 (first TypeError: Illegal constructor)   task 2: failed 995 (first TypeError: Illegal constructor)
task 3: failed 348 (first TypeError: Illegal constructor)   task 3: failed 1183 (first TypeError: Illegal constructor)
```

The first error's stack, from scratch `v01` (a `.par.map` at `PTS_THREADS=4` that returns the first error's `stack`), was the same in all four tasks:

```text
TypeError: Illegal constructor
    at new AbortSignal (pts:internal/abort:229)
    at newSignal (pts:internal/abort:211)
    at new AbortController (pts:internal/abort:299)
    at /tmp/pts-h01b-repro.OkBkyK/explore/v01-abort-stack.pts:8
    at pts:parallel:463
    at ParallelRange.map (pts:parallel:461)
```

Mechanism (source, read-only, `pts/lib/internal/abort.pts`), matching GPT's claim:
- module-level `let constructing = false;` (`:205`);
- `newSignal()` sets it (`:209`), constructs (`:211`) and clears it in `finally` (`:213`);
- the `AbortSignal` constructor throws unless it is set (`:228-229`).

Another thread's `finally` clears the shared flag between this thread's `:209` and `:228`. `AbortSignal.abort()` goes through the same `newSignal`.

Not attempted:
- named in H07's acceptance: `reusePort` handlers, `fetch` setup in `.par` tasks, and an audit of the standard library's other mutable globals;
- not named there: `AbortSignal.timeout` / `AbortSignal.any`.

### C07 evidence, per program (final pass, single run at the header's first thread count)

#### `c07-control-local-abortcontroller-sequential`

build exit 0; run exit 0 (PTS_THREADS=1); node exit 0; stdout identical.

Stdout (byte-identical in PTS and Node; PTS stderr empty):
```text
task 0: failed 0
task 1: failed 0
task 2: failed 0
task 3: failed 0
```

#### `c07-red-abortsignal-abort-par`

build exit 0; run exit 0 (PTS_THREADS=1); node exit 0; stdout identical.

Stdout (byte-identical in PTS and Node; PTS stderr empty):
```text
task 0: failed 0
task 1: failed 0
task 2: failed 0
task 3: failed 0
```

#### `c07-red-local-abortcontroller-par`

build exit 0; run exit 0 (PTS_THREADS=1); node exit 0; stdout identical.

Stdout (byte-identical in PTS and Node; PTS stderr empty):
```text
task 0: failed 0
task 1: failed 0
task 2: failed 0
task 3: failed 0
```


## The reviewers' probes, run unchanged (for comparison)

Same commands as above, with the probe path under `docs/reviews/2026-10-13-final/` and Node run directly on a `.mts` copy:

| Probe | PTS | Node | Agrees with the claim |
|---|---|---|---|
| OP `p08b_panic_mapped.pts` | build exit 2, `panic: interface conversion: ir.Type is *ir.Primitive, not *ir.MapType` at `recordFallback pts/lower/records.go:280` | exit 0, `ok` | yes |
| OP `p08d_panic_propertykey.pts` | build exit 2, same panic | exit 0, `{ odd: [ 1, 3, 5 ], even: [ 2, 4 ] }` | yes |
| OP `p08f_generic_arrow_record.pts` | build exit 2, same panic | exit 0, same as p08d | yes |
| OP `p08g_generic_fn_record.pts` (control) | exit 0, identical to Node | exit 0 | yes |
| KP `30_iterator_getter_panic.pts` | build exit 2, `panic: cgen: no check for void` at `checkDefinition pts/cgen/checks.go:305` | exit 0, prints nothing | yes |
| GP `previous/07_residual.pts` | build exit 1, `07_residual.pts:5:16 - error PTS1001: converting object4 to object1 \| object2 is not supported yet` | exit 0, `done` | the typed variant is PTS1001, not a panic |
| OP `p18_flat.pts` | exit 0; 3 of its 4 lines differ. `a.flat(2)` and `a.flat(Infinity)` print `[ 1, [ 2 ], 3 ]`, every `deep.flat(n)` prints `[ 1, [ 2, [ 3, [Array] ] ] ]`, `mixed.flat(2)` prints `[ 1, [ 2 ], [ [ 3 ] ] ]`; the `any` line is right | exit 0 (`[ 1, 2, 3 ]` for `a.flat(2)` and `mixed.flat(2)`) | yes |
| OP `p23_array_unions.pts` | exit 0; 4 of its lines differ, all in `flat`/`flatMap` of unions or tuples and `sort` of `(string \| undefined)[]` | exit 0 | yes |
| OP `p24_sort_undefined.pts` | exit 0, `[ undefined, undefined, 1, 2, 3 ]` twice; `[ undefined, 'a', 'b' ] [ 'a', 'b', undefined ] [ undefined, 'a', 'b' ]`; `[ null, 1, 3 ]` | exit 0, `[ 1, 2, 3, undefined, undefined ]` twice; `[ 'a', 'b', undefined ]` x3; `[ 1, 3, null ]` | yes (its `localeCompare` comparator line matches only because `"undefined"` sorts after `"b"`) |
| GP `abort-local-min.pts` | `PTS_THREADS=4`, exit 0, 2145 of 40,000 constructions failed, first `TypeError: Illegal constructor` in all 4 tasks | not runnable (imports `pts:parallel`) | yes |

## New defects seen (not fixed)

**NEW-1 (high): `for...of` and array destructuring over an `any` iterable drain it eagerly.**
A value typed `any` that is an object with `[Symbol.iterator]` or a generator is fully drained before the first loop iteration. `forOfStatement`'s `ir.Value` case (`pts/lower/arrays.go`, the `ir.ValueIterable` branch) calls `pts_value_iterable`, which collects with `pts_iterator_to_array` (`pts/runtime/src/dynamic_methods.c:1342` for generators, `:1345` for objects with `@@iterator`). Consequences:
- the loop body's side effects interleave wrongly (`c02-red-any-iterator-getter-sequence`);
- `return()` isn't called on `break` (`c02-red-any-iterator-break-return`: PTS `next 0..next 4, item 0, item 1, end`, Node `next 0, item 0, next 1, item 1, return, end`);
- a generator is resumed past the `break` and its `finally` runs at exhaustion, not on `break` (`c02-red-any-generator-break`: exit 1 with `Uncaught Error: resumed after the loop broke`);
- an **infinite** iterator never reaches the loop body.
  - Scratch `y10`: `function* naturals(): Generator<number> { let n = 0; while (true) { yield n++; } }`, then `const numbers: any = naturals(); for (const n of numbers) { if (n >= 3) { break; } console.log(n); } console.log("end");`.
  - Run with `ulimit -v 1048576; PTS_THREADS=1 timeout 10 $S/bin/y10-any-infinite-generator-break`: exit 1, empty stdout, stderr `GC Warning: Failed to expand heap by 540676 KiB` / `GC Warning: Failed to expand heap by 524292 KiB` / `GC Warning: Out of Memory! Heap size: 546 MiB. Returning NULL!` / `Uncaught RangeError: Out of memory allocating an array of 67108864 elements`. Node prints `0`, `1`, `2`, `end`, exit 0.
  - Without the cap it would grow until the host ran out of memory, so don't run it uncapped on this shared host.
- Array destructuring takes the same path. Scratch `y18`: `const [first, second] = numbers` with `numbers: any` the bounded generator gives PTS `yield 0..yield 3, closed`, then `Uncaught Error: resumed after the pattern was done`, exit 1. Node gives `yield 0, yield 1, closed, 0 1`, exit 0.

Typed paths are correct: a typed `Iterable<number>` parameter, a typed generator and a typed iterator class are lazy and closed on `break` (`c02-control-iterable-param-generator-break`, `c02-control-typed-iterator-break-return`, scratch `y14`). SPEC §9 (`docs/SPEC.md:305`) promises that `for...of` and array patterns over any object with `[Symbol.iterator]()` "call `return()` when a loop ends early". I found this in no ledger row. It bears on C02's acceptance ("read done before value"), so it is placed next to C02 here, but it is a separate defect.

**NEW-2 (low): `.par.flatMap` with an explicit union type argument is PTS1001 where Array's `flatMap` compiles.**
Scratch `z04`: `const ones: number[] = [1, 2]; console.log(ones.flatMap<number | number[]>((x) => (x === 1 ? x : [[x]]))); console.log(ones.par.flatMap<number | number[]>((x) => (x === 1 ? x : [[x]])));`. `pts build` exit 1 with only `z04-par-flatmap-explicit-type.pts:5:49 - error PTS1001: converting number | number[][] to number | number | number[][] | number[] is not supported yet` (line 4 is accepted). The message also lists `number` twice. Node would print `[ 1, [ 2 ] ]` for both lines.

**NEW-3 (low): a string key from an erased callback is "a key of type value".**
Scratch `x02`: inside `<T,>(items: T[], keyOf: (item: T) => string)`, `(counts[keyOf(items[i])] ??= []).push(i)` on a concrete `const counts: Record<string, number[]> = {}` gives `pts build` exit 1, `x02-generic-arrow-concrete-record.pts:6:17 - error PTS1001: a key of type value is not supported yet`. Node prints `{ a: [ 0, 2 ], b: [ 1 ] }`. tsc types the key `string`; the erased call's result is dynamic. This is not a panic, but H02 should know about it: the C01 red programs use the same kind of key (`keyOf(item)` in erased code), so H02 may meet this diagnostic once the panic is gone.

**NEW-4 (instance of V08): internal shape names in diagnostics**, e.g. `converting object4 to object1 | object2` for the typed `IteratorResult` getter literal.

## Notes for the fix tasks (H02, H03, H05, H06, H07)

- Each red program's `expected (Node)` line and Node stdout are the GREEN target. Red C01/C02 programs currently produce no binary.
- C01 (H02): the dispatch covers dynamic places in non-generic code too (`c01-red-any-*`). The evaluation-order program and its non-generic control pin receiver, key and RHS order. The falsy-value program pins `??=` keeping `0`/`""` and `\|\|=` replacing them through erased `T`.
- C02 (H03): giving cgen a check for `void` alone may not be the whole fix, since a `never` getter is `void` here too. Six programs cover object-literal, class, read, unread and `never` getters. `c02-red-typed-iteratorresult-getters` may legitimately end as an honest diagnostic rather than GREEN, per the ledger's "unsupported input has an honest source-located diagnostic". The three NEW-1 programs need the drain fixed, which may be out of H03's scope.
- C05 (H05): fix both the lowering's result type and the runtime's recursion condition. `.par.flatMap` and the `any` path already do it right.
- C06 (H06): the runtime default key (`element_string`), the comparator path (`pts_array_sort_with`), the dynamic `any` path and `.par.sort(cmp)`'s library merge are separate places. `.par.sort()`'s `compareDefault` is a working reference.
- C07 (H07): across both series the per-run failure counts ranged from 1,139 to 7,301 of 80,000, so a GREEN check should require 0 failures in every one of many runs at 2 and 4 threads, not just a lower count.

## Files

Programs (61 `.pts`, 5 `.oracle.mts`), all under `docs/agent/hardening-evidence/probes/h01b/`:

- C01 red (12): `c01-red-any-element-or-assign`, `c01-red-any-property-nullish-assign`, `c01-red-generic-arrow-eval-order`, `c01-red-generic-arrow-falsy-values`, `c01-red-generic-arrow-keyed-record`, `c01-red-generic-arrow-nullish-assign`, `c01-red-generic-arrow-or-assign`, `c01-red-generic-arrow-statement-nullish-assign`, `c01-red-generic-class-mapped-nongeneric-method`, `c01-red-generic-class-mapped-nullish-assign`, `c01-red-generic-class-mapped-or-assign`, `c01-red-generic-function-expression-nullish-assign`.
- C01 control (9): `c01-control-dynamic-and-assign`, `c01-control-generic-arrow-concrete-record`, `c01-control-generic-arrow-plain-assign`, `c01-control-generic-arrow-read-fallback`, `c01-control-generic-class-record`, `c01-control-generic-function`, `c01-control-generic-method`, `c01-control-nongeneric`, `c01-control-nongeneric-eval-order`.
- C02 red (10): `c02-red-any-iterator-done-getter-throwing-value`, `c02-red-any-iterator-data-done-throwing-value`, `c02-red-object-void-getter`, `c02-red-object-void-getter-unread`, `c02-red-class-void-getter`, `c02-red-object-never-getter`, `c02-red-typed-iteratorresult-getters`, `c02-red-any-iterator-getter-sequence`, and NEW-1's `c02-red-any-iterator-break-return` and `c02-red-any-generator-break`.
- C02 control (7): `c02-control-any-iterator-data-properties`, `c02-control-iterable-param-generator-break`, `c02-control-number-getter-throws`, `c02-control-object-void-method`, `c02-control-typed-iterator-break-return`, `c02-control-typed-iterator-getter-results`, `c02-control-void-data-property`.
- C05 red (6): `c05-red-flat-union-array`, `c05-red-flat-tuple-array`, `c05-red-flat-heterogeneous-depths`, `c05-red-flat-nested-union-depth`, `c05-red-flatmap-union`, `c05-red-flat-union-holes`.
- C05 control (3): `c05-control-flat-homogeneous`, `c05-control-flat-any`, `c05-control-par-flatmap-union` (+ `.oracle.mts`).
- C06 red (8): `c06-red-sort-undefined-default`, `c06-red-sort-undefined-comparator`, `c06-red-tosorted-undefined`, `c06-red-sort-null`, `c06-red-sort-holes`, `c06-red-sort-any-arrays`, `c06-red-sort-undefined-other-kinds`, `c06-red-par-sort-undefined-comparator` (+ `.oracle.mts`).
- C06 control (3): `c06-control-sort-defined`, `c06-control-sort-null-comparator`, `c06-control-par-sort-default` (+ `.oracle.mts`).
- C07 red (2): `c07-red-local-abortcontroller-par` (+ `.oracle.mts`), `c07-red-abortsignal-abort-par` (+ `.oracle.mts`).
- C07 control (1): `c07-control-local-abortcontroller-sequential`.

The scratch-only boundary checks (`x01`-`x13`, `y01`-`y18`, `z01`-`z04`, `w01`-`w05`, `v01`) and every raw output are in `/tmp/pts-h01b-repro.OkBkyK/` (`explore/`, `raw/<name>/{build,run,node}.{stdout,stderr,exit}`, `raw/<name>/multi/<threads>-<run>.*`). Those that matter are quoted above with their source.
