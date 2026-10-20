# H09b2 reproducer report (reproducer, claude-opus-5-5, xhigh)

Saved by the parent from the child's final message, unedited below the rule. Probes: [h09b2-reproducer-probes.tar.gz](h09b2-reproducer-probes.tar.gz) (`INDEX.txt` inside; unpacked under `/tmp/h09b2/repro/` in the session). The reproducer worked in its own worktree of `6093263fe` (`.claude/worktrees/agent-accc90938bd79391e`), left clean, nothing committed.

---

# H09b2 reproducer report (C187 and C186): HEAD `6093263fe`

Every repro you named fails on HEAD exactly as described. On a 567-program matrix HEAD gets 165 wrong values, 8 crashes and 113 TypeErrors (plus 23 compile diagnostics and 9 conversion errors), and 247 match Node. The best prototype, runtime direction (a) plus a refined (b) I'll call **(b2)** with (b)'s lowering change, gets 419 programs right and leaves 1 wrong value and no crashes. Its remaining errors are lies through `any`, plus honest `Box<T>`, `Map` and `Set` containers. Neither direction is enough alone. Nothing was committed and the worktree is clean.

**Setup.** CLI `pts-bin/pts` built with `make build`, exit 0. Programs run with `PTS_HOME=<worktree> pts-bin/pts run x.pts`. Node runs an `.mts` copy with `TZ=America/New_York node --no-warnings --experimental-transform-types x.mts`; for the repros below Node exits 0. The wrapper is `/tmp/h09b2/repro/tools/tri.sh`. Archive: `/tmp/h09b2/repro/probes.tar.gz`, with `INDEX.txt` inside. `/tmp/h09b2/repro/DONE` is touched.

## 1. RED on HEAD
Command: `cd /tmp/h09b2/repro && tools/tri.sh red/src/<name>.pts`. Outputs are in `red/red1.txt`, `red2.txt` and `red3.txt`.

| repro | HEAD (exit code) | Node |
|---|---|---|
| `residual` | `first TypeError Expected Box<number>, got Box<any>`, then `typed read 8.5` (0) | `first 7`, `typed read 8` |
| `r07` | `head 8.5` … `honest 8.5` (0) | `head 8`, `honest 8` |
| `r16` | `optional call, then instance { tag: 'some', value: [ 9 ] }` (0) | `[ 8 ]` |
| `r02b` | no output (**139**) | `typeof number` / `typed function number` / `length undefined` |
| `om3` | `typed read Cannot read property value of Object as {id: number; zip?: string \| undefined}: it is declared value`, twice (0) | `typed read 1100`, `typed read 2 1100` |
| `om2`, `r08`, `r12`, `r14`, `r15` | match Node, because D-191's restriction keeps these calls on the erased copy | |
| `r04` | lines 1–2 `TypeError Expected Box<number>, got Box<any>` (an erased copy returning `Box<any>`, not C187); line 3 `8.5` | `… 5 6`, `true true`, `8` |
| skipped `generic_method_object_arguments` | 6 of 8 lines `TypeError Expected Box<…>, got Box<any>` | `ana` / `2` / `1` / `ana` / `ana` / `4` / `6` / `2` |

The wrong numbers are a dynamic value's bits read as a double: 7 reads as 7.5 and 8 as 9 (the 2^49 offset). In `residual`'s generated C, the erased result goes through `pts_value_convert(…, &pts_check_2)`. That check's members are `{tag: string; value: number[]}` and `{tag: string}`, and the erased object passes as the second member unchanged.

## 2. Routes by which unverified objects reach typed reads

**Where the object gets in without a proper check:**
- **G1, the runtime conversion to a union** (`convert`'s union case in `convert.c`). An object is taken as the first member it fits by declared types. Since tags are strings in the IR, `{tag: string; value: any[]}` fits `{tag: "none"}`. Erased results, `?.` calls, element-access calls, `any` values, adapters, callbacks, generators and awaits all pass through here.
- **G1′, `implies` with a union target.** "Implies some member" is enough, so a union nested in an object type, or an array of unions, is kept as it is.
- **G2, the lowering's unchecked `ToUnion`** (`lower.go:1015-1016`). A dynamic value that tsc types as a union gets no conversion at all. This covers union calls' dynamic results (`r02b`, `q04`) and builtin results such as `find`.
- **G4, the static `toUnion` → `memberFor` → `providedMember`** (`unions.go:340`). Typed code's own object with an `any[]` field goes to the member it partly provides. No erased copy or lie is needed (`q07b`, `q09b`, `q10`: 15 instead of 14). A plain object or interface target is PTS1001 instead.

**Where it is then misused:**
- **Reads.** `pts_field_address_slow` and `pts_property_find_slow` check only the kind; `pts_optional_get_slow` checks `implies` but then throws (that is C186). Array element reads (`PTS_ARRAY_GET`) have no check. A function-typed field is called through the member's signature with no adapter (`q11`: SIGSEGV on an honest program).
- **Writes.** A typed `push` into the `any[]` stores raw double bits, so `console.log` shows `[ 7, 7.5 ]` where Node shows `[ 7, 8 ]` (`q13`). A typed write `c.value = 9` into a dynamic field throws "Cannot read property value … declared value".

**Matrix.** 567 cases from `tools/gen.py`, compared to Node by `tools/run_matrix.py` with 2 jobs. Abbreviations below: MIS = wrong value, TE = TypeError where Node prints a value.
- **Producers.** Wrong or TE: element access, `?.`, method value, `any` argument, JSON, `any` receiver, union call with an `any` argument, interface call with an `any` argument, interface element access, lie. OK (an instance is found): dotted call, interface call, honest union call, `[7].map(arrow)`, declared function with `any`, class method with `any`.
- **Containers (T = number).** Array, tuple, record, Set, Map, `Box<T>` and `() => T` give MIS. `T` directly, getter, nested `{n: T}` and optional give TE "declared value" (C186). `T | undefined` is OK.
- **Elements.** number, `int` (prints -9.2e18) and `u8` (prints 1 for 8) read wrong; a `[bool, bool]` tuple reads wrong. Strings and objects (class, interface, object literal) are right by accident, because a pointer and its dynamic value share one encoding.
- **Wrappers.** Only unions with two or more object members are affected. A plain object type, an interface or `W | undefined` is converted with a copy: OK, except that `Box`/`Map`/`Set` throw at the conversion (pre-existing).
- **Consumers.** Narrowed read, destructuring, typed parameter, generic function instance, interface view, `for…of`, return and `.par` (`q06b`: `15,15,15` and `22.5`) give MIS. `console.log` and `JSON.stringify` are OK. A generic method's instance is OK only because of D-191's restriction. A spread of the narrowed member is PTS1001 (unrelated).
- **Lies through `any`.** A number read as a string, class, interface or literal (array and Map containers) crashes with SIGSEGV, 8 cases. A string read as a number prints the pointer's bits (`1.03e-309`), which leaks an address.

## 3. Proposed RED tests
All are in `/tmp/h09b2/repro/tests/`; `verdicts.txt` there records Node, HEAD, (a) and (a)+(b2) for each.

| file | HEAD | Node | (a) | (a)+(b2) |
|---|---|---|---|---|
| `erased_objects_typed_reads.pts` (17 lines: producers, containers, `u8`, `for…of`, destructuring, C186) | 16 of 17 wrong | PASS | PASS | PASS |
| `erased_objects_closure_calls.pts` | SIGSEGV | PASS | PASS | PASS |
| `erased_objects_union_routes.pts` (nested union, array of unions, class field, callback, generator, await, union call, typed object) | 8 of 8 wrong | PASS | 1 line wrong (the union call's array) | PASS |
| `typed_any_fields_to_unions.pts` (G4 routes) | 5 of 5 wrong | PASS | line 5 TE | line 5 TE |
| `erased_objects_typed_writes.pts` | 2 of 4 lines wrong (TE and `7.5`) | PASS | FAIL (the push is lost) | PASS |
| `erased_objects_par.pts` (TestRun only; Node twin replaces `.par.` with `.`) | `15,15,15` | `14,14,14` / `21` | PASS | PASS |
| `erased_objects_lies.pts` (`// no-diff:`, prints only `e.name`, expects `TypeError` ×4 then `14`) | SIGSEGV | differs by design | PASS | PASS |

Line 5 of `typed_any_fields_to_unions.pts` fails in every prototype because `{n: number}` has no copy shape in that program. The lowering has to prepare copy shapes for types read through sites, or convert at the static `toUnion`.

## 4. The two fix directions
Prototypes are runtime copies under `/tmp` plus separate CLI binaries; diffs are in `proto/`. Results on the 567-case matrix:

| | HEAD | (a) | (b) | (a)+(b) | (a)+(b2) |
|---|---|---|---|---|---|
| OK | 247 | 412 | 408 | 408 | **419** |
| wrong value | 165 | 0 | 9 | 9 | 1 (a getter copied as a value, as copies already do) |
| crash | 8 | 0 | 0 | 0 | 0 |
| TypeError | 113 | 119 | 114 | 114 | 111 (honest `Box`/`Map`/`Set` and lies only) |

On the 152-file subset of `pts/tests/run` (generics, `any`, unions, dynamic, JSON and so on), only (a)+(b) fails an existing test, `generic_method_erased_objects`: its "a lie through any" line throws, uncaught. (a) and (a)+(b2) fail nothing that HEAD passes.

**(a) Verify at typed reads** (`object.c`: convert with `pts_value_try_convert` or throw, never cached; getters and optional properties too).
- Fixes `residual`, `r07`, `r16`, `om3` (C186), `q01`, `q03`, the `q09b`/`q10` typed-object routes, `q11` (via an adapter), all lies (now TypeError) and `.par`.
- Doesn't fix arrays that arrive through `ToUnion` with no field read: `r02b` still exits 139, and `r04` line 3 and `q04` still print the wrong number.
- Needs copy shapes for every site type, and it relies on sites getting checks. Every probe I built had them, but `siteCheck` returns `NULL` when `declaredTypes` is off, so the fix must guarantee they're on.
- Costs: every read of a mismatched pair copies anew, so writes are lost (7 matrix write cases become RangeErrors, plus `q13`) and identity is lost. `Box`/`Map`/`Set` become TypeErrors where some read right by accident before (29 OK→TE).
- Performance: reading an erased copy's 8-element array in a loop costs **3,064 instructions per read against 63** on HEAD. Megamorphic interface sites go from 288 to 387 instructions per iteration, or **308** with a `declared == check` pointer fast path; cache hits are unchanged.

**(b) Choose the union member properly at conversion** (`convert.c` `foreign_implied`, the same rule in `implies`, plus a checked conversion replacing that `ToUnion`).
- Fixes everything (a) fixes except G4, plus `r02b` (now a TypeError for the lie) and `q04`. Writes are kept because the object is copied once; the loop above costs 64 instructions per read.
- When a field can't be converted, (b) copies into `{tag}` and drops the data. `console.log` then prints `{ tag: 'some' }`, and the erased copy's dynamic reads see `undefined`: `r12` and `r14` regress, and so does an existing test.
- It still picks "the first member that converts". HEAD already has that flaw (`q08`: a hash-map member taken before the nested-object member, giving a TypeError). Literal discriminant types in the checks would make the choice principled.
- Costs: converting `any` into a union goes from 466 to 894 instructions; `find` over `Opt<number>[]` goes from 411 to 636 when every `ToUnion` is checked. Better to limit the check to erased and union-call results.

**(b2) + (a) is what I recommend.** (b2) copies only into a member that keeps every property another member declares; otherwise it keeps the object as HEAD does and lets (a) convert or throw at the read. On top, G4 needs a static conversion or PTS1001, and copy shapes for site types. The open choice is `Map`/`Set`: copy them at conversion (one copy, writes kept), or accept TypeErrors until H09b3's instances. `Box<T>` can only wait for instances.

**Lifting `verifiesObjects`.** With the restriction lifted, `r07`, `r08`, `r15` and `om2` stay OK under (a) or (a)+(b2); on HEAD's runtime they misread and `r14` crashes. Two things block a clean lift:
- `generic_method_erased_objects.pts` changes on 2 lines: "a lie through any" becomes a TypeError (D-155 allows that, but the test must catch it), and "a Map field" becomes a TypeError on an honest program. Either copy maps, or keep the restriction for parameters holding `Map`, `Set` or generic-class `T`.
- `generic_method_object_arguments.pts` reaches 7 of 8 lines. The "union of object types" line still takes the erased copy because its object-literal argument is lowered to `Opt<any>`; a variable argument works (`q12`). Unskipping it needs that fixed as well.

**Side findings, all separate from C187:**
- A spread of a narrowed union member is PTS1001 (`` `c.value` is not supported yet ``).
- `.par.map` over an array of unions is PTS1001 in `ptslib:///parallel.pts:286`.
- `Map<string, u8>.get(k)!` gives PTS2001.
- The write path's TypeError says "Cannot read property" for a write.

Files are in /tmp/h09b2/repro:
- probes.tar.gz
- INDEX.txt
- tests/
- matrix/counts.md
- set/table.md
- set/lines.md
- bench/cg-summary.txt
- proto/
