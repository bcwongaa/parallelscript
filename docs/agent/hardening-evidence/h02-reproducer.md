# H02 reproduction and RED regressions (opus-reproducer)

> **Parent note.** Copied unchanged from the reproducer's commit `e220b6521` (worktree `.claude/worktrees/agent-aeec0a258ffd04771`); the parent re-ran its seven tests on the base itself ([h02.md](h02.md), RED). After the review the parent edited two of them: `record_any_entries.pts` gained a section (`+=`/`++` on missing entries) and the header comments of `logical_assign_narrowed.pts` and `logical_assign_erased.pts` were corrected. The fix's own tests are listed in [h02.md](h02.md).

Reproducer: subagent for H02, model `claude-opus-5-5`, effort xhigh, 2026-10-15.
Scope: H02 in `docs/agent/HARDENING_PLAN.md`, ledger rows C01 and C19, and the parent's new C21.
Deliverable: seven RED suite regressions under `pts/tests/run/` and this report. No compiler, runtime, library, harness or `tools/agent/` file was changed, and no plan item was ticked.

## Summary

| File | Row | Today (exact first failure) | Controls inside |
|---|---|---|---|
| `logical_assign_erased.pts` | C01 (erased code) | `pts build` panics: `interface conversion: ir.Type is *ir.Primitive, not *ir.MapType` at `pts/lower/records.go:280` | last line |
| `logical_assign_any.pts` | C01 (`any` objects) | same panic | last line |
| `logical_assign_any_checks.pts` | C01 (checked dynamics, `no-diff`) | same panic | the five `=` lines |
| `logical_assign_any_places.pts` | C21 (`any` places) | wrong stdout from line 1, then exit 1: `Uncaught TypeError: Expected number, got undefined` (line 82) | `\|\|=`/`&&=` values in the last line |
| `record_any_entries.pts` | C21 (`Record<string, any>`) | wrong stdout from line 1, then exit 1: `Uncaught RangeError: Object has no property 'missing'; …` (line 24) | last two lines |
| `record_erased_keys.pts` | C19 | 25 × `error PTS1001: a key of type value is not supported yet` | last line |
| `logical_assign_narrowed.pts` | C22 (new; H02 boundary) | 4 × PTS1001 `converting number \| undefined to number is not supported yet` (and the `null \| number`, `string \| undefined` variants); the member lines generate C that clang rejects | last line |

Every Node-compared file's `// expect:` block is Node's exact stdout. Each of those six files was also checked to be reachable: a desugared copy that replaces only the RED constructs by equivalents that work today prints exactly Node's output under today's PTS (see "Expectations are reachable").

**Integration warning.** The three panicking files (`logical_assign_erased`, `logical_assign_any`, `logical_assign_any_checks`) panic inside the `go test` process, because the harness compiles in-process: `go test ./pts/tests/` dies at the first one (`panic: … [recovered, repanicked]`, package `FAIL`), and no later test runs. Land them only together with the fix, never ahead of it.

## Root causes (verified in code at `389baf63d`)

- **C01.** `compoundAssignment` (`pts/lower/lower.go:2791-2792`) sends every place with `target.key != nil` and `??=`/`||=` to `recordElementOrSet` (`records.go:306-320`). Dynamic places from `dynamicPlace` (`pts/lower/dynamic.go:106-116`, `place.dynamic`) also carry a key, but their object is a boxed `any`, so `recordFallback`'s `record.Type().(*ir.MapType)` (`records.go:280`) panics. The harness stack shows exactly `lower.go:2792 → records.go:307/309 (recordElementOrSet) → lower.go:2976 (withObject) → records.go:280 (recordFallback)`. `assignmentTarget` (`lower.go:2991-2992`) takes the dynamic path whenever `isAny(node.Expression())`, which is true for erased generic code's records, shapes, arrays and generic classes' mapped-type fields.
- **C21, places.** `nullishAssignment` (`pts/lower/unions.go:542-569`) returns `current` unless the place's type is an object, function, map, union, null or undefined; `ir.Value` (`any`, `unknown`, an erased type parameter) is none of them, so the right side is never evaluated or stored.
- **C21, records.** `recordFallback` (`records.go:279-302`) tests truthiness only for bool, numeric, string and union entry types, and nullishness only for unions; `ir.Value` entries are taken as present values. `&&=` uses `readAndStore` → `place.read()` → `MapGetChecked`, which throws for a missing key even though a plain read of an `any` entry (`records.go:200`) uses `MapGet` and gives undefined.
- **C19.** `recordKey` (`records.go:229-241`) accepts only string or numeric IR keys; a dynamic key (an erased call's result, a `K extends string` value in erased code, a variable of type `any`) is PTS1001.

## Environment and provenance

- Worktree `/home/bryan/projects/parallelscript/.claude/worktrees/agent-aeec0a258ffd04771`, branch `worktree-agent-aeec0a258ffd04771`, base `389baf63dc1d8eaee930facc8c079996f3e14bb3`.
- Submodules (none in a fresh worktree), each exit 0: `git submodule update --init --reference /home/bryan/projects/parallelscript/.git/modules/pts/third_party/<lib> pts/third_party/<lib>` for ryu (72e18e8d1), bdwgc (4fab5386d), libuv (840404ce8), libbacktrace (0b9b49cf4), pcre2 (6f9d7c137).
- CLI: `GOMAXPROCS=2 go build -p 2 -o pts-bin/pts ./cmd/pts`, exit 0 (3m46s). `pts version`: `pts 0.7.0 (commit 389baf63d; typescript-go 7.0.2)`; sha256 `b2fd0a2ebc13fcc278c1a564063213fa4ff2a84d5edec484aa8dfa020a7d5896`.
- `go version go1.26.9 linux/amd64`; Node `v24.21.0` (`~/.local/node/bin/node`); `Ubuntu clang version 18.1.3 (1ubuntu1)`.
- Runtime cache isolated from the parent: `PTS_CACHE_DIR=/tmp/pts-h02-repro/cache`; `PTS_HOME` = the worktree.
- Scratch `/tmp/pts-h02-repro`: probe sources `p/*.src` (split into `p/<group>/*.pts`), scripts `bin/`, Node outputs `node/`, per-program outputs `out/<name>/{node,pts}.{stdout,stderr}`, desugared copies `desugared/`, `go test` logs `gotest/<name>.log`.

## Commands and exit codes

Node (all exit 0), per file, from a copy in `/tmp/pts-h02-repro/node/`:
`~/.local/node/bin/node --no-warnings --experimental-transform-types <name>.mts` (`bin/node_out.sh`).

Harness, per file, from the worktree root with `PATH=~/.local/node/bin:~/.local/go/bin:$PATH GOMAXPROCS=2 PTS_CACHE_DIR=/tmp/pts-h02-repro/cache PTS_REQUIRE_NODE=1` (`bin/gotest.sh`). The `-run` pattern also matches `TestRunMatchesNode/<name>`.

| Command | Exit | Result |
|---|---|---|
| `go test -p 2 ./pts/tests/ -run 'TestRun/logical_assign_erased$' -count=1 -v` | 1 | `--- FAIL: TestRun/logical_assign_erased`, then `panic: interface conversion: ir.Type is *ir.Primitive, not *ir.MapType [recovered, repanicked]`; the process dies before `TestRunMatchesNode` reports |
| `… -run 'TestRun/logical_assign_any$' …` | 1 | same panic (rerun after the last edit of the file: same) |
| `… -run 'TestRun/logical_assign_any_checks$' …` | 1 | same panic; `TestRunMatchesNode/logical_assign_any_checks` SKIP (no-diff) |
| `… -run 'TestRun/logical_assign_any_places$' …` | 1 | `TestRun` and `TestRunMatchesNode` FAIL at `run_test.go:69` / `:98` (stdout differs) |
| `… -run 'TestRun/record_any_entries$' …` | 1 | both FAIL, stdout differs |
| `… -run 'TestRun/record_erased_keys$' …` | 1 | both FAIL: `error is not nil: …record_erased_keys.pts:21:12 - error PTS1001: a key of type value is not supported yet` |
| `… -run 'TestRun/logical_assign_narrowed$' …` | 1 | both FAIL: `error is not nil: …logical_assign_narrowed.pts:10:26 - error PTS1001: converting number \| undefined to number is not supported yet` |

CLI cross-check of the final files (`bin/cmp.sh`, `pts run <abs path>` from a scratch dir; recorded in `/tmp/pts-h02-repro/final-cli.txt`): erased, any and any_checks exit 2 with the panic; any_places exit 1 (`Uncaught TypeError: Expected number, got undefined` at `logical_assign_any_places.pts:82`); record_any_entries exit 1 (`Uncaught RangeError: Object has no property 'missing'; if it may be missing, read it with `?? fallback`, give the value type `| undefined`, or check `key in object` first` at `record_any_entries.pts:24`); record_erased_keys and logical_assign_narrowed exit 1 with the PTS1001 lists below.

Expect blocks were round-tripped with a parser that mirrors `spec_test.go`'s directive regex (`bin/roundtrip.py`): all seven equal their source stdout byte for byte.

## Per file: what is RED and how

### `logical_assign_erased.pts` (C01, 18 output lines)
Generic arrows and a generic function expression with `??=`/`||=` on `Record<string, T[]>` (expression and statement forms), the value used as the entry, `Record<K, number[]>` with `K` = `string` and `Partial<Record<K, number>>` with literal keys, keep-versus-replace of existing 0, NaN, "", false, null and undefined entries, erased shape fields and array elements, a generic class instance's field written from a generic arrow, a generic class's mapped-type field from generic methods and from a non-generic method, and an evaluation-order trace (receiver and key once, right side only when needed).
- RED: output lines 1-17 (the build panics).
- Control: line 18 (`&&=` in erased code; a generic declaration's instance), which passes today (probes `er_and_control`, `er_generic_decl_control` matched Node).
- Lines 4, 13, 14, 15 and 17 also guard **identity** (C24 below): with a fix that gives the right side instead of the stored value they print `false 0 { a: [], b: [] }`, `1 1 1 2 { a: [ 2, 3 ], b: [] }`, `{ a: [ 3 ], b: [] }`, `1 1 1 1`, `{ a: [ 2 ], b: [ 4 ] }` (seen in the desugared copy before it read the stored value back).

### `logical_assign_any.pts` (C01, 33 output lines)
Parsed-JSON objects through `any`: present null / undefined / 0 / "" / false / NaN / a truthy value versus a missing key for `??=` and `||=` (computed keys, values used), property forms as statements and values, number keys, nested `any` (`tree.a.b ??= 1`), typed class fields / record entries / array elements reached through `any`, and order traces through a class's getter/setter, an object literal's getter/setter and a plain JSON object (`object, key p, get, rhs 1, set 1, object, key p, get, …`).
- RED: lines 1-32 (the build panics).
- Control: line 33 (`&&=`, `=`, `+=`, and `&&=` not creating a missing key).
- Getters/setters through `any` are supported for `=`, `+=` and `&&=` today, for classes and object literals (probes `ao_getset_class_assign_control`, `lg_literal_getset_controls` matched), so the traces are in.

### `logical_assign_any_checks.pts` (C01 "checked dynamics", `no-diff`)
`??=`/`||=` through `any` into typed places must check the stored value as `=` does (D-072), and only when a store happens. The expected text of each `??=`/`||=` line is the message `=` gives today for the same store: the expect block is the PTS stdout of a copy where each `X ??= Y` became `X ?? (X = Y)` (`bin/desugar.py`), and its `=` lines are what `=` prints today (two of them, `label = 5` and `a = Animal`, are the same stores and messages as in `any_writes_error.pts`).
- RED: the ten `??=`/`||=` lines and the final state line (the build panics).
- Controls: `label = 5`, `a = Animal`, `n = 1`, `added = 1`, `erased =`, which give those messages today.
- `note ??= 1 (not needed) ok`: the right side is not evaluated, so nothing is checked.

### `logical_assign_any_places.pts` (C21, 10 output lines)
`??=` on module-scope `any` variables (one without initializer), an `unknown` variable, a function local, a captured variable, an optional parameter as a default (`options ??= {…}`), instance, constructor-set (`late: any;`) and static fields, object-literal fields typed `any` including an optional one, a parameter's field, `any[]` elements, and an erased type parameter's parameter and local; plus a trace of right-side evaluations.
- Today PTS prints 8 lines, all wrong where a `??=` was due, then exits 1 at line 82: `orDefault`'s `x ??= fallback` never assigns, and converting the result to `number` throws `Uncaught TypeError: Expected number, got undefined`. PTS's first lines: `null undefined 0 null undefined 0` / `undefined` / `[ undefined, undefined ] null true undefined { verbose: true } 0` / `null null undefined null null` / `Lazy { value: undefined, late: undefined }`.
- RED: lines 1-9 and the trace in line 10 (`a, b, d, e, f`). Values that coincide today (`zero ??= …` keeping 0, `withOptions(0)`, `xs[2]`, `xs[3]`) are part of the same lines.
- Control: `5 6 [ 'set', 'kept' ]` in line 10 (`||=` and `&&=` work on these places).

### `record_any_entries.pts` (C21, 41 output lines)
`Record<string, any>` entries holding null / undefined / 0 / "" / false / NaN / a truthy value and a missing key: reads with `??` and `||` by name and by computed key; `??=`, `||=`, `&&=` (values and resulting records); property-form statements; `Record<string, unknown>`, `{ [key: string]: any }` and `Record<number, any>`.
- Today: line 1 `null undefined 0 "" false NaN kept 7`, line 2 `null undefined 0  false NaN kept 7`, the `??`/`||` maps print the entries unchanged, `??=` and `||=` don't store (only the missing key is set), and the `&&=` map throws `Uncaught RangeError: Object has no property 'missing'; …` at line 24 (exit 1).
- RED: every print before the controls (output lines 1-39; some inner lines of the multi-line prints coincide).
- Controls: lines 40-41 (typed `Record<string, number | null>` entries; `r.n ? … : …`, `!r.z`, a plain missing read and its `typeof`), which pass today (probes `ra_typed_union_control`, `ra_ternary_truthiness`, `mo_recany_missing_reads` matched).

### `record_erased_keys.pts` (C19, 11 output lines)
Concrete `Record<string, number>` / `Record<string, number[]>` / `Record<number, number>` indexed directly by an erased callback's call: read, write, `+=`, `++`, `??=`, `||=`, with a call counter showing each key expression evaluated once; a `K extends string` key on a concrete record; `in`, `delete` and `Object.hasOwn` with an erased-call key; and keys of type `any` holding `"a"`, `1`, `1.5`, `-0` and `"2"` on `Record<string, …>` and `Record<number, …>` (read, write, `+=`, `++`, `??=`, `||=`; JS's `String(k)`).
- Today: compile error, 25 diagnostics, all `error PTS1001: a key of type value is not supported yet`, at 21:12, 21:35, 28:10, 29:10, 36:12, 43:10, 55:12, 55:38, 56:12, 64:10, 64:25, 65:17, 71:99, 72:122, 73:114, 80:19, 81:7, 82:7, 83:7, 84:20, 89:7, 90:7, 91:19, 94:10, 95:22. (80:35, the second key in the same `console.log`, isn't listed; the call's lowering apparently stops at its first failed argument.)
- RED: lines 1-10.
- Control: line 11 (the erased call's key bound to a typed local first), which passes today (probe `c19_control_local_key` matched; H01b's `c19-control-parent-erased-callback-key-local.pts`).

### `logical_assign_narrowed.pts` (C22, new; 6 output lines)
The value of `??=`/`||=` on a union-typed place where tsc narrows it: declared (`const n: number = (count ??= 1)`), passed (`twice(maybe ||= 4)`), returned (`return (this.value ??= make())` in a class and `||=` in a generic class's instance), a record entry's (`(totals.n ??= 5)`), and members of the value (`(text ??= "ab").length`, `(other ||= "cd").toUpperCase()`, `(named.name ??= "abc").toUpperCase()`, `(labels.a ??= "xyz").length`). No generics or `any` needed.
- Today: PTS1001 at 10:26 `converting number | undefined to number`, 15:23 `converting null | number to number`, 23:12 `converting number | undefined to number`, 30:12 `converting string | undefined to string`. Line 13 (`twice(maybe ||= 4)`) is PTS1001 too when compiled alone (probe `nw_or_arg`: `converting null | number to number` at 3:19); in the file its call apparently isn't lowered because its first argument `declared` already failed. Lines 40 and 46, compiled alone (probe `nw_members_only`), generate C that clang rejects: `error: member reference type 'pts_value' (aka 'unsigned long') is not a pointer` for `.length` and `error: incompatible integer to pointer conversion passing 'pts_value' (aka 'unsigned long') to parameter of type 'pts_string *'` for `.toUpperCase()`; the C reads `((pts_value_is_nullish(text) ? (text = pts_value_from_string(pts_str_1)) : text))->utf16_len`.
- RED: lines 1-5.
- Control: line 6 (the value printed and used in `+`), which works today.
- Cause (from the code): `nullishAssignment` and the `||=` branch of `compoundAssignment` give the place's IR type (the union), not the type tsc gives the expression; `recordElementOrSet` passes `target.valueType()` as the result type. This file is a boundary for the parent to scope: it is not erased code and not a panic, but it is the same operators' value semantics, it hits the lazy-initialization idiom in generic classes (`return (this.cache ??= make())`), and the member case is an internal failure (clang), not a diagnostic. If H02 doesn't take it, give it `// skip: …` with a PROGRESS known issue, or move it to H04.

## Expectations are reachable

`bin/desugar.py` writes a copy of each file in which every RED construct is replaced by an equivalent that works today with the same evaluation counts: `X ?? (X = Y)` / `X || (X = Y)` with the object and key in locals for the traces; for erased code `X ?? ((X = Y), X)`, reading the stored value back; explicit `== null` / truthiness tests or helper functions for `Record<string, any>` entries; typed locals or `String(k)` / `Number(k)` for C19 keys; a statement and then a read for C22. Every replacement must match (the script exits otherwise). Results with today's PTS: `logical_assign_erased`, `logical_assign_any`, `logical_assign_any_places`, `record_any_entries`, `record_erased_keys`, `logical_assign_narrowed`: **MATCH** Node, and each desugared copy's Node output equals the original file's Node output; `logical_assign_any_checks`: its PTS output is the file's expect block. So no expected line depends on another missing feature.

## Boundaries found

Works today (controls, all matched Node): `&&=`, `=` and `+=` on dynamic places in erased code and through `any`; getters/setters through `any` (class and literal) for `=`, `+=`, `&&=`; `??=`/`||=` in generic function declarations, generic methods of non-generic classes and on a generic class's `Record<string, T[]>` field; `??=`/`||=` in a generic arrow on a record whose type doesn't mention the type parameter; `??` reads of erased values and of `any` locals; `Map<string, any>.get(k) ?? d`; conditions and plain reads of `Record<string, any>` entries (a missing key reads as undefined); `??=`/`||=` on typed union records; a union place's `??=` value printed or used in `+`; an erased (dynamic) record indexed by an erased call; a concrete record indexed by a typed local holding an erased call's result; `JSON.parse` objects and records reached through `any` take new keys (also `1.5` and `-0`).

Fails today, beyond the parent's four findings:
1. **C01 is wider:** a generic class instance's field written from a generic arrow (`(holder.value ??= x)` with `Holder<T>`), typed class fields / record entries / array elements reached through `any`, nested `any`, number keys, and every getter/setter case all panic the same way.
2. **C21 is wider:** `unknown` variables, optional parameters (`options ??= {}`), static fields, optional properties (`p?: any`), fields declared without initializer and set in the constructor (`this.late ??= …`), and erased type parameters' parameters and locals (`x ??= d` with `x: T | undefined` in a generic arrow; the unassigned result then fails its checked conversion at run time: `Uncaught TypeError: Expected number, got undefined`). For records: `Record<string, unknown>`, `{ [k: string]: any }`, `Record<number, any>`; and `&&=` on a missing key of `Record<string, any>` throws `RangeError` while a plain read gives undefined.
3. **C19 is wider:** `in`, `delete` and `Object.hasOwn` with an erased call's key; a key of type `K extends string` on a concrete record in erased code (`counts[key]`, read and `??=`); number keys from erased callbacks; `any` keys on `Record<number, …>`.
4. **C22 (new):** see `logical_assign_narrowed.pts`.
5. **C23 (new, not in a test):** in a generic arrow with `K extends string`, `const out = {} as Record<K, number[]>` is PTS1001 when `K` is inferred as a union of literals: `using a `object1` as a `{a: number[]; b: number[]}`, whose `a` it doesn't provide with the same types, is not supported yet` (probe `kk_literal_keys_cast_only`, even with no assignment at all); passing a `Record<string, number[]>` in for `Record<K, …>` is `converting Record<string, number[]> to {a: number[]; b: number[]} is not supported yet` (probe `kk_passed_record`). `K` = `string` and `Partial<Record<K, …>>` work, which is what the regression uses. **H01b's `probes/h01b/c01-red-generic-arrow-keyed-record.pts` depends on this cast, so it will still fail after the C01 panic is fixed** (with this PTS1001 instead of the panic).
6. **C24 (new, a trap for the fix):** in erased code, storing an array literal created there into a concrete typed place (a `Record<string, number[]>` entry, a `{ v: number[] }` field, a `number[][]` element, a `Holder<number>` field) stores a converted copy, while the assignment's value is the original: `const put = <T,>(r: Record<string, T[]>, k: string): T[] => (r[k] = []); put(rec, "a").push(1)` leaves `rec` as `{ a: [] }` and `got === rec.a` is false (Node: `{ a: [ 1 ] } true`; probes `id_record_assign_value`, `id_shape_assign_value`, `id_array_assign_value`, `id_class_field_assign_value`). Reading the place back (`r[k] = []; r[k].push(x)`) and storing an array passed in from typed code keep identity. A `??=`/`||=` fix must give the stored value, or `(record[key] ??= []).push(x)` loses its pushes; `logical_assign_erased.pts` checks this. Plain `=` has the divergence today; no test covers `=` alone.
7. **By design, not a defect (kept out of the Node tests):** an object literal reached through `any` has a fixed layout (D-L10), so adding a key throws `Cannot add property x, object is not extensible`, and its properties keep the literal's types (D-072), so `box.n = 1` on `{ n: null }` throws `Cannot set property n of Object: expected null, got number`. The Node-compared tests therefore add keys only to `JSON.parse` objects; `logical_assign_any_checks.pts` asserts both messages for `??=`/`||=`.
8. **Ambiguous under D-059, left out of the tests:** `r.missing &&= 6` on `Record<string, number>` throws `RangeError: Object has no property 'missing'` (Node: undefined, nothing stored). D-059 keeps JS's meaning for `??=`/`||=` and gives undefined for a missing key "in conditions", but doesn't name `&&=`.

## Not tested

- Symbol keys, and `any` keys holding booleans, null, objects or values with side-effecting `toString` (ToPropertyKey timing); records only have string keys (D-059), and what a dynamic non-string, non-number key should do is a design choice for the fix.
- `??=`/`||=` with `await` or `yield` in the right side (D-145's kept reads; H08's area), and inside `.par` tasks.
- Frozen objects (`Object.freeze`) and `Proxy` through `any`.
- A separate `tsc` type-check; PTS's own checker (tsgo) accepted all seven files' desugared copies without TS errors, and Node ran all seven.
- The predecessor's H01b probes were read but not rerun; the four parent findings were each reproduced independently by my own probes (`/tmp/pts-h02-repro/p/*.src`).
