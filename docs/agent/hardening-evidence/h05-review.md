# H05 review: opus-reviewer

## Round 1

Agent `ab51f9c72a90cd152` (`opus-reviewer`, claude-opus-5-5, xhigh, read-only), run by the previous session from 01:28 to 01:51 on 2026-10-17 against the snapshot `/tmp/h05/snapshot1.diff` (the change before `pts_stack_check`, `append_loaded`, `ReadonlyArray<U>` and `run/flat_cycles.pts`). That session ended before it read the report; this session recovered the report verbatim from the agent's transcript (`~/.claude/projects/…/e622af05-…/subagents/agent-ab51f9c72a90cd152.jsonl`, its last long assistant text) and archived its probes (`/tmp/tmp.jUgch9FEbM`, without built binaries and the scratch PTS_HOME) as [h05-review-probes.tar.gz](h05-review-probes.tar.gz). Paths in the report are under that scratch directory.

I found 8 problems in the snapshot. Five are regressions against the base and one adds a new false TypeError. The parent's suite (`/tmp/h05/suite1.jsonl`) is green, so no existing test catches any of them.

**How I ran things.** Your working tree changed after the snapshot: `pts_stack_check`, an `append_loaded` fast path, `ReadonlyArray<U>` in `parallel.pts` and `run/flat_cycles.pts`. I didn't review those edits. `pts run` builds the runtime from `PTS_HOME`, so every "new" run used a copy of the snapshot's runtime with the 01:27 snapshot CLI:
- new: `cd <dir> && PTS_HOME=/tmp/tmp.jUgch9FEbM/home /home/bryan/projects/parallelscript/pts-bin/pts run f.pts`
- base: `PTS_HOME=/tmp/h05-base /tmp/h05-base/pts-bin/pts run f.pts`
- Node: `node --no-warnings --experimental-transform-types f.mts`

The probes are under `/tmp/tmp.jUgch9FEbM/{p,x,b,d,c,u,e,m,l}/`. Everything below marked VERIFIED was run on all three.

## 1. HIGH, regression: `?.flat()` and `?.flatMap()` no longer compile (VERIFIED)
- **Where:**
  - `pts/lower/arrays.go:526-529`, `flat`
  - `pts/lower/callbacks.go:105-108`, `key.flattened`
- **Cause:** both take tsc's type at the call node. In an optional chain that type is `X[] | undefined`, so the `*ir.Array` assertion fails and the code returns nil without reporting anything.
- **Repro** (`p/oc1.pts`, `p/oc2.pts`):
```ts
const c = Math.random() < 2;
const maybe: number[][] | undefined = c ? [[3]] : undefined;
console.log(maybe?.flat());            // oc2: maybe?.flatMap((x) => x)
```
- **New:** `oc1.pts:3:13 - error PTS1001: call expression (which the compiler failed to lower without saying why) is not supported yet`
- **Base and Node:** `[ 3 ]`
- `b.items?.flat()` on an optional property fails the same way (`p/oc3.pts`; Node gives `[ 1, 2, 3 ] undefined`).
- **Fix:** strip the chain's `undefined`, for example by taking the resolved signature's return type, and report when the assertion fails.

## 2. MEDIUM, regression: unmatched regex groups are dropped as holes (VERIFIED)
- **Where:**
  - `array.c:915` (`is_hole` in `flatten_element`)
  - `array.c:975` (`pts_array_has`)
  - `dynamic_methods.c:625`
- **Cause:** match arrays and their copies store `undefined` as a zero word, which looks like a hole. `pts_has_undefined_holes()` already exists in `pts.h:1932` for this, but the new code doesn't use it.
- **Repro** (`x/x3.pts`):
```ts
const m = /(a)|(b)/.exec("b")!;
const anyM: any = m;
console.log(anyM.flatMap((g: any) => [g ?? "none"]));
console.log(anyM.flat());
```
- **New:** `[ 'b', 'b' ]` twice.
- **Base and Node:** `[ 'b', 'none', 'b' ]` and `[ 'b', undefined, 'b' ]`.
- **Typed `flatMap`** (`x/x2.pts`): `m.flatMap((g) => [g ?? "none"])` now silently gives `[ 'b', 'b' ]`. The base threw an honest TypeError ("group 1 of the match took no part in it"); Node gives `[ 'b', 'none', 'b' ]`.
- **`split` with capture groups** (`x/x3` original): `"a,b".split(/(,)|(;)/).flatMap(p => [p ?? "none"])` gives `[ 'a', ',', 'b' ]`; the base threw RangeError; Node gives `[ 'a', ',', 'none', 'b' ]`.
- **Pre-existing, base equally wrong:** typed `m.flat()` and `[m].flat()` (`x/x4.pts`).

## 3. MEDIUM, regression to SIGSEGV: cyclic arrays under `flat(Infinity)` (VERIFIED on the snapshot)
- **Where:** `array.c:944`. The recursive `flatten_into` has no `pts_stack_check()`.
- **Repro** (`d/cy1.pts`):
```ts
const a: any[] = [1];
a.push(a);
try { console.log(a.flat(Infinity).length); } catch (e) { console.log("caught", (e as Error).name); }
```
- **New:** exit 139.
- **Base:** `2` (wrong, but no crash).
- **Node:** `caught RangeError`.
- The same happens for `object[]` (`d/cy6`). Through `any` (`d/cy5`) the base segfaults too, so that path is pre-existing.
- Your in-progress tree adds the check and `flat_cycles.pts`; I haven't verified it.

## 4. MEDIUM, new false TypeError: i64/u64 values above 2^53 going into a `number` result (VERIFIED)
- **Where:** `array.c:893-913`. `kind_holds(NUMBER/F32)` rejects boxed INT64/UINT64, and `pts_store_value_in_slot` would not convert them anyway.
- **Rule broken:** the GUIDE says any integer converts implicitly to `number` and rounds.
- **Repro** (`b/i7.pts`, `b/i9.pts`):
```ts
const big: i64 = (1 as i64) << (62 as i64);
const xs: i64[] = [big];
const n: (number | i64[])[] = [1.5, xs];
console.log(n.flat());                                   // also [1.5].flatMap((v) => (v > 1 ? xs : v))
const asNumber: number = big; console.log(asNumber);     // 4611686018427388000
```
- **New:** `TypeError: Expected number, got int`, from both `flat` and `flatMap`. A u64 gives `Expected number, got u64`.
- **Base:** didn't throw (it didn't flatten).
- **Expected:** `[ 1.5, 4611686018427388000 ]`.

## 5. MEDIUM-LOW, compile regression: explicit union type arguments with two array members (VERIFIED)
- **Where:**
  - `unions.go:306` (`holdsAsItIs`)
  - `lower.go:3365` (`takesContextualUnion`)
- **Cause:** every array-literal branch now takes the contextual union. When the literal's element type is a union that is only a subset of an array member's element union, `holdsAsItIs` doesn't match it, and with two array members `holdingArray` returns nil.
- **Repro A** (`p/r6.pts`):
```ts
console.log([1, 2].map<number | number[] | (number | string | number[])[]>((x) => (x > 1 ? [String(x), x] : x)));
```
  - New: `PTS1001: converting (number | string)[] to (number | number[] | string)[] | number | number[] is not supported yet`
  - Base and Node: `[ 1, [ '2', 2 ] ]`
  - `p/r12.pts` (strings) behaves the same.
- **Repro B**, which is H05's own scope (`p/p3.pts`):
```ts
const values: number[] = [1, 2];
console.log(values.flatMap<number | string | number[]>((x) => (x > 1 ? [String(x), x] : x)));
```
  - New: PTS1001.
  - Base: wrong, `[ 1, [ '2', 2 ] ]`.
  - Node: `[ 1, '2', 2 ]`.
  - The `.par` form fails on both base and new.
- **Fix:** let a union whose members are all in `place`'s union count as held as it is, or fall back to the old path when `memberFor` fails.

## 6. LOW, C20 acceptance gaps (VERIFIED; pre-existing, not regressions)
C20 says `.par.flatMap` should accept the type arguments Array's `flatMap` does. Two cases still don't:
- `values.par.flatMap<number | number[]>(fn)`, where `fn(x: number): number | number[][]` is a named function (`p/fr.pts`). New: `converting number | number[][] to (number | number[])[] | number | number[] is not supported yet`. Array's `flatMap` with the same argument prints `[ 1, [ 2 ] ]` on new, matching Node.
- `values.par.flatMap<number | string>((x) => (x > 1 ? shared : x))`, where `shared` is an existing `number[]` (`p/s2.pts`). It's PTS1001, while Array's `flatMap` gives `[ 1, 7, 8 ]`.

The header of `run/parallel_flatmap_unions.pts` ("takes what Array's flatMap takes") overstates this.

## 7. LOW: `flatMap` with a tuple or array type argument (VERIFIED)
- `[1, 2].flatMap<[number, string]>((x) => [x, "a"])` (`p/tu1.pts`): new throws `TypeError: Expected object, got number`. The base silently printed `[ [ 1, 'a' ], [ 2, 'a' ] ]`. Node prints `4 [ 1, 'a', 2, 'a' ]`.
- `flatMap<number[]>` (`p/tu4.pts`) throws on both base and new.
- The kind check keeps this memory-safe. tsc's type is wrong here, as it is for odd depths, so a compile-time PTS1001 would be better.
- The message names the slot kind (`object`), not the type. It comes from `pts_check_declared(into, NULL)->name` at `array.c:927`.

## 8. LOW: diagnostics (VERIFIED)
- **Internal name leaks:** `xs.flat(a)` with `a: any` reports "`flat` with a depth of type value" (`arrays.go:560`).
- **Wrong fix advice:** for `depths: 1 | -2`, the message says "JS flattens as `flat(0)` does, so write that", which is wrong whenever the depth is 1. `errors/flat_odd_depths.pts` cuts off that expectation, so the advice isn't pinned.
- **`number | undefined` depths are PTS1001:** that covers the common `depth?: number` parameter, where JS treats undefined as 1.
- **Odd enum depths:** `cube.flat(E.Neg)` and `cube.flat(E.Half)` (`b/en.pts`) ran correctly on the base and are now PTS1001. That follows D-180's choice, but it's still a change.

## Checked and fine
- **Depths:**
  - 0, 1, 2, 25, Infinity, NaN.
  - `0 | 2` unions, `number` variables, `int` depths, enum members.
  - A generic `D extends number`, including an instance with -1, which is correctly reported.
  - Receiver-before-depth evaluation order.
  - Lying `as 1` / `as 2` depth casts throw a TypeError rather than crash.
- **Element types:** u8, i64 (2^62 kept exact), `(u8 | u8[])`, `(i64[] | u8[])`, and `int[][]`, whose result stays `int[]`.
- **Tuples:** optional elements and explicit `undefined`, rest elements (both forms), nested tuples, readonly tuples, frozen tuples and arrays.
- **Object types:** interface and class shapes in different layouts, subclasses, Set, Map, Uint8Array, functions, null.
- **`Iterable`/`object`/`{}` elements:** typed reads afterwards match JS.
- **Generics:** monomorphized cases.
- **`flatMap` callbacks mutating the source:** pushing, shrinking and filling a later hole all match Node on the typed and dynamic paths.
- **`.par.flatMap`:** 300k elements at 1/2/4 threads, run 3 times each (union results, holes, tuples, all matching Node). Ranges and typed arrays with union `U` work.
- **C20 closure adoption and conditional unions:** I tried user HOFs, `Promise.then`, `Promise.resolve`, `map`, `find ??`, `push`, `Map.set`, object literals, recursive named functions, interface arrays and integer contexts. No silent identity loss: D-094 still rejects kept or mutated copies (`c/keep*.pts`, `c/id*.pts`). Integer range errors stay PTS2002/PTS2001.
- **GC:** the runtime relies on bdwgc's conservative stack scan, so locals holding pointers during pushes are fine.
- **Performance:** callgrind shows new `flat` uses fewer instructions than the base (149M vs 207M).

## Missing tests
`?.flat()`/`?.flatMap()`, match arrays with unmatched groups, i64/u64 leaves into a number result, cycles (in progress), explicit union type arguments with two array members, and a `.par.flatMap` callback that is a named function.

## Round 2

Agent `opus-reviewer` (claude-opus-5-5, xhigh, read-only), 2026-10-17 02:47–03:27, against the round-1 fixes: CLI `/tmp/h05/s2/pts-r1fix3` (sha256 `077e784f…`) with the frozen runtime home `/tmp/h05/s2/review-home`, the diff `/tmp/h05/s2/suite.diff` plus `suite.untracked`. Its harness blocked the report file, so the report below is its final message, verbatim. Its probes (scratch `/tmp/h05r2.bz2s`, without built binaries and callgrind outputs): [h05-review2-probes.tar.gz](h05-review2-probes.tar.gz).

I found no memory-safety hole in the change, but there are two new regressions against the base (F1, F2) and one silent wrong result the base shares (F3). I couldn't write `REPORT.md`: the harness blocks report files from subagents, so the full report is below. Every probe is in `/tmp/h05r2.bz2s`, with minimal repros in `/tmp/h05r2.bz2s/repro/`.

**How I ran things** (`/tmp/h05r2.bz2s/bin/t.sh f.pts [nonode]`):
- **New:** `cd <dir> && PTS_HOME=/tmp/h05/s2/review-home /tmp/h05/s2/pts-r1fix3 run f.pts`. I checked the sha256 (`077e784f…`), and `diff -rq` shows review-home's `pts/runtime` and `pts/lib` are identical to the working tree's.
- **Base:** `PTS_HOME=/tmp/h05-base /tmp/h05-base/pts-bin/pts run f.pts`.
- **Node:** `~/.local/node/bin/node --no-warnings --experimental-transform-types f.mts` on a `.mts` copy. I compared `.par` programs against a sequential version (`.par.flatMap` replaced by `.flatMap`).
- I didn't run `go test`, and I didn't touch either repo tree or `/tmp/h05-suite`.

**Overall:**
- Every leaf the new runtime stores is checked, either by its kind or against the result's declared element check. The round-1 SIGSEGV shapes now throw a TypeError.
- Round-1 fixes #1, #3 and #5–#8 hold. #2 (match groups) and #4 (64-bit integers) are only partly fixed.

---

### F1. MEDIUM, regression: a second unmatched match group in the same result throws (VERIFIED)
- **Where:** `pts/runtime/src/array.c:944` (`push_leaf`):
  ```c
  if (value == PTS_VALUE_UNDEFINED && type == &pts_array_types[PTS_FIELD_STRING]) {
    result->type = &pts_type_match_copy; ...
  ```
- **Cause:**
  - The first undefined group switches the result's type to `pts_type_match_copy`.
  - The next undefined group fails the `type ==` test. `pts_type_match_copy` has no `check` (`regexp_object.c:32`), so it falls through to `kind_holds(STRING, undefined)` and throws at array.c:953.
  - So fix #2 only works when a result receives exactly one undefined group.
- **Who is affected:** typed `flat`, typed `flatMap` whose callback returns match arrays, and `.par.flatMap`. The dynamic path is fine because its result is an `any[]`.
- **Fix:** also accept `type == &pts_type_match_copy` (or `pts_has_undefined_holes(result)`) in that test.

**Repro A** (`repro/F1_match_two_undefined.pts`):
```ts
const parts = "a,b;c".split(/(,)|(;)/);
console.log(parts.flat());
```
- New: `Uncaught TypeError: Expected string, got undefined`, exit 1.
- Base: `[ 'a', ',', 'b', ';', 'c' ]` (wrong, but no throw).
- Node: `[ 'a', ',', undefined, 'b', undefined, ';', 'c' ]`.

**Repro B** (`repro/F1b_two_matches.pts`):
```ts
const m = /(a)|(b)/.exec("b")!;
const n = /(a)|(b)/.exec("a")!;
console.log([m, n].flat());
```
- New: the same TypeError.
- Base: `[ 'b', 'b', 'a', 'a' ]`.
- Node: `[ 'b', undefined, 'b', 'a', 'a', undefined ]`.
- The same happens for one match with two undefined groups (`m/m4.pts`) and for a `string[][]` of two such matches (`m/m5.pts`).

**Repro C** (`repro/F1c_typed_flatmap_matches.pts`): `["a","b"].flatMap((s) => /(a)|(b)/.exec(s)!)`
- New: `TypeError: Expected string, got undefined`.
- Base: the same TypeError with "(group 2 of the match took no part in it)" appended.
- Node: `[ 'a', 'a', undefined, 'b', undefined, 'b' ]`.
- Nothing here reads a group as a string. D-180 says flatMap keeps the groups, so the TypeError is wrong.

**Test gap:** `run/flat_match_groups.pts` and the C unit test `flat_keeps_the_undefined_groups_of_a_match` each feed only one undefined group into a result.

### F2. MEDIUM, regression: `flatMap<Interface>` over dynamic data throws a false TypeError, unless unrelated code converts `any` to that interface (VERIFIED)
- **Where:**
  - `array.c:950`: `pts_value_convert(value, type->check)` runs for every leaf `pts_array_push_flat` stores, since those leaves are never treated as already of the result's type.
  - The lowering never readies the result's element type as a dynamic-conversion target. `prepareDynamicTarget`/`copyShape` (`pts/lower/dynamic.go:255-323`) are what set `Interface.Copy`.
- **Cause:**
  - A JSON object is a record, not an object of the interface's layout, so it has to be copied. Copying needs `check->type`, which only `Interface.Copy` provides.
  - `describeArray`/`e.check` emit the result's check without a copy shape, so the conversion fails with `Expected {x: number}, got Object`.
  - If any other line converts `any` to the same interface, `Copy` gets set and the program works.
  - D-180 says such a leaf "is a copy (D-071)". Here it's a TypeError.
  - The base converted at the helper's push, through a lowered conversion that readies the target, so it worked.
- **Fix:** run `prepareDynamicTarget` on the element type of flat's and flatMap's result.

**Repro** (`repro/F2_interface_any_leaves.pts`):
```ts
interface J { x: number }
const parsed: any = JSON.parse('[{"x":1},{"x":2}]');
const js = [0, 1].flatMap<J>((i) => [parsed[i]]);
console.log(js.length, js[0].x + js[1].x);
```
- New: `Uncaught TypeError: Expected {x: number}, got Object`, exit 1.
- Base and Node: `2 3`.
- **Control** (`repro/F2b_with_unrelated_conversion.pts`): the same program plus `const unrelated: J = parsed[0];` after the print gives `2 3` on new.

**Same failure elsewhere:**
- `dyn/d3.pts`: a non-array leaf, `(i) => parsed[i]`.
- `dyn/d13.pts`: an interface with a `tags: string[]` property; base prints `2 3`.
- `id/id9.pts`: an `{x: number | string}` interface.
- `dyn/d14.pts`: `groups.flatMap<J>((g) => g)`; the base fails here too, with a different error.

**Not affected:** `flatMap((i): J[] => …)`, `[parsed[i] as J]`, `const ls: J[] = xs.flatMap(…)`, `.par.flatMap<J>` (`dyn/d5.pts`), and `flat()` (`dyn/d4.pts`, `d10.pts`).

### F3. MEDIUM, silent wrong result (the base shares it; fix #4 is incomplete): 64-bit integer leaves in a union result with `number` read as NaN (VERIFIED)
- **Where:**
  - `array.c:906` (`slots_of`): `pts_check_implies(kind_checks[I64], into)` is true for a `number` member. The rule at `convert.c:242-243` says "as values; places compare their kinds", but `slots_of` doesn't compare kinds.
  - `push_leaf` then stores the boxed i64 unconverted in the result's value slot (array.c:950). The ToNumber conversion at array.c:952-956 only runs for results without a check, i.e. `number[]`.
- **Fix:** convert boxed 64-bit integers with ToNumber when the result's check isn't that integer type.

**Repro** (`repro/F3_i64_union_leaves.pts`, ParallelScript-only):
```ts
const big: i64 = (1 as i64) << (62 as i64);
const bigs: i64[] = [big, 5];
const r = [1].flatMap<number | boolean>(() => bigs);
console.log(r);
let total = 0;
for (const v of r) if (typeof v === "number") total += v;
console.log(total);
```
- New and base: `[ 4611686018427387904, 5 ]`, then `NaN`.
- Number oracle (`i64/a5x.mts`, i64 replaced by number): `[ 4611686018427388000, 5 ]`, then `4611686018427388000`.
- By contrast, `const u: number | string = big` converts correctly, and a dynamic `number | boolean` conversion throws `Expected boolean | number, got int`. Only the flatten path keeps the box.
- It isn't memory-unsafe, but the arithmetic is wrong with no error.

### F4. LOW-MEDIUM: `.par.flatMap` over match arrays depends on the thread count (VERIFIED)
**Repro A** (`repro/F4_par_match_holes.pts`): `["ab","b"].par.flatMap((s) => /(a)?(b)/.exec(s)!)`
- New at `PTS_THREADS=1`: `[ 'ab', 'a', 'b', 'b', undefined, 'b' ]` (correct).
- New at `PTS_THREADS=2` or `4`: `<1 empty item>` in place of the `undefined`.
- Base: `<1 empty item>` at every thread count. Sequential Node: `undefined`.
- **Cause:** the combine step `joined.push(...(right as U[]))` in `pts/lib/parallel.pts`. Spreading a match copy into a plain `string[]` turns undefined groups into holes. That part predates H05 (`m/m9.pts`: `a.push(...m)` gives holes on base and new).

**Repro B** (`repro/F4b_par_one_thread_throws.pts`): `["a","b"].par.flatMap((s) => /(a)|(b)/.exec(s)!)`
- New at 1 thread: TypeError (F1). New at 2 threads: correct.
- Base: holes at both.
- `m/m1.pts` (4000 inputs) throws at 1, 2 and 4 threads.

### F5. LOW, regression: `flatMap<J>` with class or object-literal leaves whose layout doesn't match J throws at run time (VERIFIED)
**Repro** (`repro/F6_interface_class_leaf.pts`):
```ts
interface J { x: number | string }
class C { constructor(public x: number) {} }
const c = new C(1);
const r = [1].flatMap<J>(() => [c]);
console.log(r.length, r[0].x);
```
- New: `TypeError: Expected {x: number | string}, got C`.
- Base and Node: `1 1` (the base lost identity, `id/id6.pts`).
- The equivalent `map<J>` is a compile-time PTS1001 ("doesn't provide x with the same types"). `spreadsOtherElements` (`pts/lower/arrays.go:559`) only asks tsc whether C is assignable to J, so this now fails late, with a TypeError JS doesn't have.

### F6. LOW: the depth is converted before the receiver's length is read (VERIFIED)
**Repro** (`repro/F5_depth_before_length.pts`):
```ts
const xs: (number | number[])[] = [1, [2]];
const depth: any = { valueOf() { xs.push(99); return 1; } };
console.log(xs.flat(depth));
```
- New: `[ 1, 2, 99 ]`. Node: `[ 1, 2 ]`, because the spec reads the length before converting the depth. Base: PTS1001.
- **Where:** `pts_array_flat_depth` is evaluated before `pts_array_flat` reads the length. The same applies to `array_flat` in `dynamic_methods.c`.

### F7. LOW, performance: each leaf stored into an interface-typed result runs a structural check with string compares (VERIFIED, callgrind, 1 thread)
| Program | Instructions | In the check (`descriptor_implies`) | In `strcmp` |
|---|---|---|---|
| `perf/pf1.pts`: 200k 6-property records, `flatMap((r) => r.delta ? [r, r] : r)` | 1278M | 239M | 210M |
| `perf/pf1m.pts`: the same work as a manual push loop | 401M | – | – |
| `perf/pf2.pts`: `(Rec \| Rec[])[]` `flat()` | 936M | 239M (25%) | 210M (22%) |

- That's about 2,250 instructions per checked leaf, roughly O(properties²) name compares.
- Not a regression: the base threw TypeError on both programs.
- A one-entry "this descriptor fits this check" cache per flatten call would remove most of it.

Other numbers, all fine:
| Program | Base | New |
|---|---|---|
| `.par.flatMap` number pairs, 1 thread (`perf/pp.pts`) | 1303M | 667M |
| class leaves (`perf/pq.pts`) | TypeError | 853M (`pts_array_has` is about 70 instructions per element) |

### F8. LOW: `takesContextualUnion` lowers the contextual type and can add a diagnostic (VERIFIED, noise only)
- **Where:** `pts/lower/lower.go:3370`, `l.irType(contextual, node)`.
- **Example** (`cond/c3.pts`): `[1,2].concat(c ? [3] : 4)` gets an extra `PTS1001: object types with an index signature … (ConcatArray<number>)` before the existing `converting number | number[] to number` error.
- I found no program the base compiles that now fails this way. Promise.resolve/all, Set, Map, Array.from, Uint8Array(.from), Object.entries, Map.set and push contexts all behave the same.

### F9. Test quality
- The `// expect:` blocks of all 17 Node-comparable new tests equal Node's real output (`/tmp/h05r2.bz2s/tests/*.exp` vs `*.node`).
- `parallel_flatmap_holes` and `parallel_flatmap_unions` match the sequential Node version.
- **Missing tests:**
  - two undefined match groups in one result (F1);
  - dynamic data into `flatMap<Interface>` (F2);
  - a boxed i64 into a union result (F3);
  - `.par.flatMap` over match arrays at 2 or more threads (F4).
- **Fragility, not a bug today:** the `.par.flatMap` leaf checks only work because the program's descriptors carry declared types. For the lib's `pts_parallel_push_flat`, that switch is turned on only incidentally, by the library's `left as U[]` / `flattened as U[]` casts. Nothing marks it the way `ArrayPushFlat` is marked in `collectChecks` (`pts/cgen/checks.go:42`). A refactor of `parallel.pts` that drops those casts would silently remove the Point-for-Point[] check. `par/p1.pts` currently throws correctly.

### Round-1 findings re-run
I re-ran all 97 round-1 probes on new, base and Node (`/tmp/h05r2.bz2s/r1/all.out`), with `.par` ones against the sequential version (`r1seq/`).
1. **`?.flat()` / `?.flatMap()`:** fixed. `p/oc1`–`oc3` and the extra chains in `oc/o1.pts` match Node.
2. **Unmatched groups:** fixed for one undefined group (`x/x3`, the test, `m6` through `any`). Typed `m.flatMap` throws the same D-133 error as the base (`x/x2`). Two or more undefined groups now throw (F1).
3. **Cycles:**
   - Fixed: `cy1`, `cy5` and `cy6` match Node, and `cy4` gives RangeError like Node.
   - In `.par` workers, a cyclic or 200k-deep `flat(Infinity)` throws RangeError at 1 and 4 threads, with no crash (`cy/w1.pts`).
   - `cy3` (100k deep) flattens where Node overflows, which D-180 records.
4. **i64/u64 into `number[]`:** fixed (`b/i7`, `b/i9`, `run/flat_integer_leaves.pts`). Union results are not fixed (F3).
5. **Two array members:** fixed. `p/r6`, `r12` and `p3` match Node.
6. **C20:** `p/fr` now matches Node. `p/s2` stays PTS1001 as documented. `p1`, `p2`, `p5`, `s3`–`s5`, `rg` and `m/pt` match the sequential version.
7. **Tuple/array type arguments:** fixed.
   - `p/tu1`, `tu2` and `tu4` are now compile-time PTS1001.
   - `[1].flatMap<Point[]>(() => pts)` is a compile-time PTS1001.
   - `.par.flatMap<Point[]>` throws `TypeError: Expected Point[], got Point` at 1, 2 and 4 threads, where the base exited with 139.
8. **Depth diagnostics:** fixed (`b/en`, `e/e2`).

### Checked and fine
- **Identity:** class, subclass, interface, object-literal and function leaves keep identity through union slots (`id/id2`, `fn/f1`, `ty/t3`, `im/i1`). This includes interfaces with methods, methods with fewer parameters and getters. Only dynamic data is copied (D-071, pre-existing; `id/id4` shows the same copy on a plain assignment).
- **Element types:**
  - `object[]`, `{}[]`, `unknown[]` and `Iterable<number>[]` flatten like JS, with typed reads afterwards matching (`ty/t1`, `t2`).
  - Callbacks typed `object`, `{}` or `unknown` that return arrays are spread like JS (`mb/*`).
  - undefined and null leaves, and boolean unions (`nu/n1`).
  - Arrays the runtime makes: `Object.entries`, `Array.from(map)`, `split`, `matchAll`, and others (`rt/r1`).
  - Frozen arrays (`fz/f1`), holes in the typed and dynamic paths (`ho/h1`), tuple receivers (`misc/tr`), dynamic flat (`dy/y1`).
- **Generics:** monomorphized cases work (`gen/g1`). An `as T[]` lie throws TypeError rather than misreading (`gen/g3`). `gen/g2` gets the spread PTS1001 in the instance.
- **Generated matrix:** 32 programs (`gen2/`, generator `bin/gen.py`). Eight element types are crossed with four shapes, each at depths default, 0, 1, 2 and Infinity, plus three flatMap forms. Everything matches Node. The only difference is `Array.isArray` returning false for a statically-typed tuple, which predates H05 (`misc/ia.pts`).
- **`spreadsOtherElements`:** no false positives found (`sp/s1`). tsc itself rejects the optional-tuple case (`sp/s2`).
- **Conditional and union changes:** no regression against the base found.
  - Existing arrays and tuples beside array literals: `cu/u1`–`u10`.
  - Unions with several array members: `mf/f1`–`f4`.
  - Tuple identity through unions: `tp/t1`–`t4`.
  - Object, interface and tuple literals in union contexts: `cond/c2`, `cond/c9`, `ctx/x1`–`x3`.
  - User higher-order functions: `hof/h1`–`h3`. D-094 still rejects kept or changed copies.
  - The one case that got worse is `cu/u9`: a frozen tuple beside a literal is now a compile-time PTS1001 where the base threw at run time. It fails on both.
- **Concurrency:** `.par.flatMap` over 100k elements at 1, 2 and 4 threads, three runs each. Results match the sequential version and are deterministic (`par/p2`), apart from F4.
- **Evaluation order:** receiver before depth (`ord/o1`), apart from F6.

### Pre-existing (not H05)
- `a.push(...m)` with a match or match copy turns undefined groups into holes (`m/m9`); this causes F4.
- `Array.isArray` on a statically-typed tuple is false (`misc/ia`).
- `map` throws RangeError at holes in string and object arrays (C74).
- Tuples held as `any` have no `flat` method (C75).

## Round 3

Agent `opus-reviewer` (claude-opus-5-5, xhigh, read-only), 2026-10-17 04:05–04:30, against round 2's fixes: CLI `/tmp/h05/s2/pts-final` (sha256 `b421fa4c…`) with the frozen runtime home `/tmp/h05/s2/review-home3`, the diff `/tmp/h05/s2/suite3.diff` plus `suite3.untracked`. Its harness blocked the report file, so the report below is its final message, verbatim. Its probes (scratch `/tmp/h05r3.OCtU`, without built binaries and the diffs of the tree): [h05-review3-probes.tar.gz](h05-review3-probes.tar.gz).

I found two new regressions, both in the round-2 fix for F5 (the compile-time `convertsLeaves` check). Programs that compile and print Node's output on the base and on the round-2 tree now fail to compile: one shape with an internal compiler error (PTS9001, exit 3), the other with PTS1001. F1–F4 and F6 hold, and I think the F7 memo is sound. The corpus can't catch either regression: all 45 corpus programs that use `flat`/`flatMap` give identical results on the final and base CLIs. I couldn't write a report file, so the report is this message.

**How I ran things.** Every probe is under `/tmp/h05r3.OCtU`. The harness `/tmp/h05r3.OCtU/bin/t.sh f.pts [nonode|noprev]` runs four ways from the file's directory:
- **Final:** `PTS_HOME=/tmp/h05/s2/review-home3 /tmp/h05/s2/pts-final run f.pts`. I checked the sha256 (b421fa4c…). `review-home3`'s runtime and lib match the working tree, and the working tree's code diff matches `suite3.diff`.
- **Round 2:** `PTS_HOME=/tmp/h05/s2/review-home /tmp/h05/s2/pts-r1fix3 run f.pts`.
- **Base:** `PTS_HOME=/tmp/h05-base /tmp/h05-base/pts-bin/pts run f.pts`.
- **Node:** `~/.local/node/bin/node --no-warnings --experimental-transform-types f.mts` on a `.mts` copy.
- **`.par` programs:** built with `pts build` and run with `PTS_THREADS=1/2/4`, twice each, against a sequential oracle.

I didn't run `go test`, didn't touch `/tmp/h05-suite`, and the repo's `git status` is unchanged.

---

### N1. HIGH, regression: function-typed leaves of another function type than the result's element type cause PTS9001 (VERIFIED)
- **Where:**
  - `pts/lower/arrays.go:623` and `:636`: `convertsLeaves` probes with `l.convert(&ir.LeftOut{…}, elem, …)`.
  - That reaches `lower.go:988` `l.adapt` → `adaptSkipping` (`closures.go:611`) → `l.inFunction`, which appends the adapter to `l.lowered` (`classes.go:774`).
  - The probe throws away the `ir.Bind{wrapped…}`/`MakeClosure` that captures `adapted`. The orphaned adapter is still emitted, so cgen panics at `pts/cgen/closures.go:51` (`captureField`).
- **Cause:** the probe isn't side-effect free. Any typed `flatMap` whose leaf function type needs an adapter hits it: fewer parameters, a different result type, or a union element type with a function member. The runtime adapts those leaves (`check->adapt`), which is why round 2 ran them.
- **Minimal repro** (`repro/N1_function_leaf_ice.pts`):
  ```ts
  const f = (): number => 7;
  const r = [1].flatMap<(a: number) => number>(() => [f]);
  console.log(r.length, r[0](5));
  ```
  - Final: `error PTS9001: internal compiler error while emitting C: cgen: adapter doesn't capture adapted`, exit 3. `PTS_DEBUG=1` shows the panic in `cgen.(*emitter).captureField`.
  - Round 2, base and Node: `1 7`.
- **Realistic repro** (`repro/N1b_listeners_ice.pts`): event handlers with fewer parameters.
  ```ts
  buttons.flatMap((b): Listener[] => [b.onClick, b.onHover])
  ```
  - Final: PTS9001.
  - Round 2, base and Node: the four handler lines.
- **Same failure elsewhere:**
  - an existing array as the leaves: `(() => number)[]` into `flatMap<(a: number) => number>` (`fa/fa2`);
  - a result-type union: `flatMap<() => number | string>` (`repro/N1d`, `fa/fa4`);
  - a union element type: `flatMap<string | ((n: number) => string)>(() => ["a", f])` (`repro/N1c`);
  - an annotated callback over an existing array (`fa/fb3`);
  - two function types in one literal (`fa/fa7`).
- **Fix:**
  - Either make the probe side-effect free: snapshot `len(l.lowered)` (and `l.relaxedCasts`) and truncate after probing, or add a dry-run check for function types.
  - Or skip function-typed leaves in `convertsLeaves`. Closures keep no identity across adapters anyway, and the runtime adapts them.
- **Test gap:** `errors/flatmap_leaf_conversions.pts` only covers class-to-interface layouts.

### N2. MEDIUM, regression: an annotated callback returning a new array of narrower objects is rejected (VERIFIED)
- **Where:** `pts/lower/arrays.go:629-630`. For a returned value that isn't a literal, `convertsLeavesOf` takes the leaves from tsc's type for the returned expression.
- **Cause:**
  - With a declared return type, the closure's own return already converts the value to that type. A new array is copied there (D-094), so the values flatMap stores are already elements of `ft.Result`.
  - The probe instead converts each narrower leaf (`{size: number}`) to the interface through `objectCast`, which requires exactly the interface's property types, so it reports PTS1001.
- **Repro** (`repro/N2_annotated_map.pts`):
  ```ts
  interface Named { name: string; size: number | string }
  const r = [[1, 2], [3]].flatMap((ns): Named[] => ns.map((n) => ({ name: "n" + n, size: n })));
  console.log(r);
  ```
  - Final: `2:33 - error PTS1001: using a {name: string; size: number} as a {name: string; size: number | string}, whose size it doesn't provide…`.
  - Round 2, base and Node: the three objects.
- **Realistic repro** (`repro/N2b_entries_filter_map.pts`):
  ```ts
  Object.entries(groups).flatMap(([key, vs]): Row[] => { return vs.filter(…).map((v) => ({ key, value: v })); })
  ```
  with `value: number | null`. Final: PTS1001 at 3:52. Round 2, base and Node: `[ { key: 'a', value: 1 }, { key: 'b', value: 3 } ]`.
- **Same failure elsewhere:**
  - `(n): Named[] => base.slice()` (`repro/N2c`): PTS1001 on final; round 2 and base ran it.
  - `const row = {…}; return [row]` behind a `Named[]` annotation (`lt/l2`) fails on all three trees, so it's not new.
- **Related, weaker:** `flatMap<Named>((ns) => ns.map(…))` with no annotation (`repro/N2d`).
  - Base: correct output (it ignored U). Round 2: TypeError. Final: PTS1001.
  - This follows D-180's stated rule, but the objects are fresh, so a copy couldn't be told apart.
- **Fix:** when the callback has a return annotation (`callback.Type() != nil`), check `leafTypes(ft.Result)` only. Optionally, treat an `isNewArray` returned value as copyable, as the closure-return rule does.

### N3. LOW: an extra diagnostic on a program that already fails (VERIFIED, noise only)
`repro/N3_extra_diagnostic.pts` (round 2's `mb/mb6`) is `xs.flatMap((x): { length: number } => [x, x])`. It now also reports `2:50 converting number to {length: number}`, after the existing `2:49` error.

### N4. LOW, informational (pre-existing D-133 behavior, newly reachable)
- Flat results now hold a match's undefined groups, as Node does. Iterating them with typed `for…of`, or reading one by index, throws `RangeError: Array index 2 is out of range (length 7)`, a misleading message.
- `repro/N4_forof_groups.pts`:
  - Final: RangeError.
  - Base: ran with wrong output (`5 0`, the groups dropped).
  - Node: `7 2`.
- The same RangeError happens on the base for `for (const p of "a,b;c".split(/(,)|(;)/))` (`mg/g4`), so it isn't H05's. It just reaches more programs now.

---

### Round 2's findings re-run
- **F1, fixed.** Two groups in one result, two matches, and typed flatMap over matches all match Node (`repro/F1*`). The test expectations equal Node.
- **F2, fixed.** `repro/F2_interface_any_leaves` prints `2 3`. So do `dyn/d1`, `d3`, `d13` and `d14`, without any unrelated conversion in the program.
- **F3, fixed.** `F3_i64_union_leaves` gives `4611686018427388000`, as does `i64/a1`.
- **F4, fixed.** I ran `par/p1_matches` (3000 inputs, one or two undefined groups) at 1, 2 and 4 threads, twice each. Results are identical to the sequential flatMap, with no holes, and the counts match Node (9000 elements, 2000 and 3000 undefined).
- **F5:** the round-2 repros are now compile-time PTS1001 (`id6`–`id8`, `F6_interface_class_leaf`). Its implementation causes N1 and N2.
- **F6, fixed.** Typed and dynamic `flat` match Node for these depths: `"2"`, `"abc"`, null, true, false, `{}`, `[]`, `[3]`, `[1,2]`, `" 1 "`, `"Infinity"`, -1, 0.9, 1.9, NaN, `"0x2"` and undefined (`dp/d1`). A `valueOf` that throws, pushes or shrinks the array also matches (`dp/d2`). So do tuple receivers and an optional `depth?: number` (`dp/d3`).
- **F9:** checked. A `.par.map`-only program emits the same C as the base, because the library's `flatMap` is lowered only when it's used.
- **Whole round-2 probe set** (288 programs, final vs round 2, `PTS_THREADS=4`): 31 differ. Every difference is one of the intended fixes, the F5 move to compile time, a D-071 copy (`id9`), or N3. Outputs are in `r2all/**/*.final` and `*.r2`.

### Checked and fine
- **F7 memo:** I think it's sound.
  - It records only as-is results. In `convert()`, as-is acceptance of a `PTS_KIND_OBJECT` object depends only on the check and the object's descriptor: same descriptor, `object_fits`/`descriptor_implies`, results by flag, union pass order.
  - Copies (content-dependent, and the only path that calls getters) are never recorded.
  - Checks are static, and frozen twins are never freed (`object.c:446`), so a stale pointer can't produce a false hit.
  - It is thread-local; alternating C and D leaves into `J` at 4 threads were correct (`par/p2`).
- **`convertsLeaves`, no false positives found** in: generics, subclass leaves, unions of classes into interfaces, readonly arrays, optional and rest tuples, `Object.entries`, `this`, integers, literal unions and enums, nullable unions, shape variables, block returns, spreads, generic class instances, getters and methods, `as` casts, `as const`, async callbacks, a `never` branch, and nested literals (`cl/`, `cm/`, `lt/`, `as/`).
- **`prepareDynamicTarget` on result types:** no new diagnostics across recursive interfaces, Map, Set, Date, RegExp, Promise, Record, Uint8Array, private fields, function types and optional properties (`pt/e1_types`).
- **`.par.flatMap`** at 1, 2 and 4 threads, twice each: holes in a union array, 200k numbers, and object results all match the sequential version and are deterministic (`par/`).
- **`memberFor`/`takesContextualUnion`:** tuples and multi-array unions are improvements over the base (`un/`).
- **Realistic programs:** permutations, a graph walk, CSV-style rows and regex tokenizing all match Node (`rp/`).
- **New tests' `// expect:` blocks** equal Node for `flat_depth_order`, `flatmap_dynamic_interfaces`, `flatmap_literal_leaves`, `flat_match_groups` and `parallel_flatmap_match_groups` (sequential); see `tn/`.

All probes are under `/tmp/h05r3.OCtU`, with the minimal repros in `/tmp/h05r3.OCtU/repro`. `/tmp` is wiped on reboot, so archive them if you want to keep them.

## Round 4

Agent `opus-reviewer` (claude-opus-5-5, xhigh, read-only), 2026-10-17 04:43–05:04, narrow scope: round 3's fix (the pure leaf check `keepsLeaves`/`keepsLeaf`), CLI `/tmp/h05/s2/pts-final2` (sha256 `45f5291c…`) with `/tmp/h05/s2/review-home4`, the diff `/tmp/h05/s2/suite4.diff`. Its harness blocked the report file, so the report below is its final message, verbatim. Its probes (scratch `/tmp/h05r4.Dwwf`, without binaries and diffs of the tree): [h05-review4-probes.tar.gz](h05-review4-probes.tar.gz).

I found three verified problems, of which two are medium severity:
- **Round-4 regression (MEDIUM):** `keepsLeaf` now waves through typed leaves whose static type is a union. The runtime then copies them silently, so identity is lost. Round 3 rejected these programs at compile time; the base gave the same wrong output.
- **Regression against the base (MEDIUM):** a generic function passed straight to `flatMap` is rejected with PTS1001. This comes from `spreadsOtherElements`, which round 4 didn't change.
- **Silent copies into other result types (LOW-MEDIUM):** a tuple of another layout, an object going into a union of interfaces, and a nested array literal are all copied silently.

N1 and N2 are fixed. The new check is side-effect free, and nothing round 3 found fine has regressed. I couldn't write a report file, so the report is this message.

**How I ran things.** Scratch dir `/tmp/h05r4.Dwwf`; minimal repros are in `/tmp/h05r4.Dwwf/final-repro/`. `/tmp/h05r4.Dwwf/bin/t.sh f.pts [quick]` runs, from the file's directory:
- **final2:** `PTS_HOME=/tmp/h05/s2/review-home4 /tmp/h05/s2/pts-final2 run f.pts`
- **R3:** `PTS_HOME=/tmp/h05/s2/review-home3 /tmp/h05/s2/pts-final run`
- **R2:** `PTS_HOME=/tmp/h05/s2/review-home /tmp/h05/s2/pts-r1fix3 run` (left out with `quick`)
- **base:** `PTS_HOME=/tmp/h05-base /tmp/h05-base/pts-bin/pts run`
- **Node:** `node --no-warnings --experimental-transform-types` on a `.mts` copy

I checked that the sha256 is `45f5291c…`, that `review-home4`'s runtime and lib equal the working tree, and that `git diff -- pts` is byte-identical to `suite4.diff`. I didn't run `go test`, didn't touch `/tmp/h05-suite`, and the repo's `git status` is unchanged.

---

### R4-1. MEDIUM, regression vs round 3: union-typed leaves skip the check and are copied silently (VERIFIED)
- **Where:** `pts/lower/arrays.go:646-670`, `keepsLeaf`.
  - A leaf that is an `*ir.Union` (an array's union element, `C | null`, an optional property, a conditional element) matches no case and reaches `return true` at :670.
  - At run time `leaf_of` → `pts_value_convert` copies the C into J. That's the D-071 path, since `flattenedResult` prepared J as a dynamic target.
  - Round 3's `convert` probe rejected these with the same message the regular conversion gives (`g06b`: `const rows: Row[] = g.map(x => x)` → "converting Filled | Missing to Row").
- **Repro** (`final-repro/A1_union_leaves.pts`):
  ```ts
  interface J { x: number | string }
  class C { constructor(public x: number) {} }
  class D { constructor(public x: string) {} }
  const c = new C(1);
  const cds: (C | D)[] = [c, new D("a")];
  const r = [1].flatMap<J>(() => cds);
  c.x = 5;
  console.log(r.length, r[0].x, r[1].x);
  ```
  | final2 | R3 | R2 | base | Node |
  |---|---|---|---|---|
  | `2 1 a` | `6:26 PTS1001 converting C \| D to {x: number \| string}` | `TypeError: Expected {x: number \| string}, got C` | `2 1 a` | `2 5 a` |
- **Same failure in other shapes**, each with final2 = base (wrong), R3 = PTS1001, Node correct:
  - `A2_named_fn_union.pts`: a named function returning `(Filled | Missing)[]` into `flatMap<Row>`. final2 `1=10 2=null`, Node `1=99 2=null`.
  - `A3_rows.pts`: a block-free arrow returning the same union array.
  - `A4_optional_prop.pts`: `hs.flatMap<J | undefined>((h) => [h.c])` with `c?: C`. final2 `1`, Node `5`.
  - `g/g05`: `[n > 1 ? c : d]`.
  - `g/g07`: `C | null` into `J | null`.
  - `g/g08`: an array kept inside a union, `(number[] | string)[]` into `(number | string)[] | string`. final2 `[1, 2]`, Node `[1, 2, 3]`.
- **Fix:** for a union leaf, return the AND of `keepsLeaf(member, elem, …)` over its members.

### R4-2. MEDIUM, regression vs base (in `spreadsOtherElements`, not changed in round 4): generic function callbacks are rejected (VERIFIED)
- **Where:** `pts/lower/arrays.go:563-568`.
  - `GetSignaturesOfType(typeAt(callback))` returns the uninstantiated generic signature.
  - `returnTypeOf` only applies the enclosing instance's substitution, so the return type stays `T[]`, and `T` isn't assignable to U.
- **Repro** (`final-repro/G1_generic_callback.pts`):
  ```ts
  function dup<T>(x: T): T[] { return [x, x]; }
  console.log([1, 2].flatMap(dup));
  ```
  - final2, R3 and R2: `2:28 PTS1001 flatMap with a callback that returns a T[] … return it in an array to keep it whole`. The advice is misleading.
  - base and Node: `[ 1, 1, 2, 2 ]`.
- **`G2_generic_refs.pts`:** `maybe.flatMap(orNone)` with `orNone<T>(x: T | null): T[]` is also PTS1001; base and Node print `[1, 3]`. A generic arrow const and a static generic method are also rejected; the base got those wrong anyway (unflattened, C05).
- **Fix:** instantiate the signature, or skip `spreadsOtherElements` for generic signatures and leave them to the runtime check.

### R4-3. LOW-MEDIUM: a tuple leaf into a tuple element type of another layout is copied silently (VERIFIED)
- **Where:** `arrays.go:657`. `!isArray(elem)` returns true when elem is a tuple.
- **Repro** (`final-repro/C1_tuple_leaf.pts`): `const p: [number, string] = [1, "a"]`, then `[1].flatMap<[number, string | number]>(() => [p])`, then `p[1] = "b"`, then print `r[0][1]`.
  | final2 | R3 | R2 | base | Node |
  |---|---|---|---|---|
  | `1 a` (silent) | PTS1001 `using a [number, string] as a [number, number \| string]` | `1 a` | TypeError | `1 b` |
- The regular `const r: [number, string | number][] = [p]` gives the same PTS1001 on every tree (`g/b4b`).
- Against the base this turns a loud failure into a silent wrong result.

### R4-4. LOW-MEDIUM: an object into a union of interfaces it provides none of is copied silently (VERIFIED)
- **Where:** `arrays.go:648-649`. When `memberFor` returns nil, the comment says "the runtime checks the leaf against" the union. In fact the runtime copies it.
- **Repro** (`final-repro/B1_iface_union_elem.pts`): `flatMap<J1 | J2>(() => [c])`, then mutate `c.tag`.
  | final2 | R3 | R2 | base | Node |
  |---|---|---|---|---|
  | `1 t` | PTS1001 `converting C to J1 \| J2` | TypeError | `1 t` | `1 changed` |
- The regular `const u: (J1 | J2)[] = [c]` is that same PTS1001 on every tree (`a/b6`).

### R4-5. LOW: nested array literals are skipped, but they aren't made as the context's type (VERIFIED)
- **Where:** `arrays.go:618-619`, and D-180's sentence "a nested literal made as its context's type". That holds for object literals (`fn/n01` and `g/g04` are rejected by the regular lowering) but not for array literals.
- **Repro** (`final-repro/E1_nested_literal.pts`): `[1].flatMap<J[]>(() => [[cobj]])`, then `cobj.x = 5`.
  | final2 | R3 | R2 | base | Node |
  |---|---|---|---|---|
  | `1 1` | `1 1` | TypeError | `1 1` | `1 5` |
- The regular `const r: J[][] = [[cobj]]` is PTS1001 on every tree (`g/g03`).
- **Fix:** recurse into the nested literal with `elem.(*ir.Array).Elem`.

### R4-6. LOW: a class or interface leaf into another class fails at run time only (VERIFIED)
- **Where:** `arrays.go:663-664`. A class elem returns true.
- **Repros:**
  - `final-repro/D1_class_leaf.pts`, `flatMap<D>(() => [c])`: final2 `TypeError: Expected D, got C`; R3 PTS1001 `using a C as a D`; R2 and base TypeError; Node `1 5 false`.
  - `g/g09`, `D | number`: same pattern.
  - `g/g10`, an interface into a class: same pattern.
- Not a regression against the base.

### R4-7. LOW: relaxed casts (D-066) aren't registered, so `checkRelaxedCasts` is bypassed (VERIFIED, memory-safe)
- **Where:** `arrays.go:664` accepts `holdsOptionally` leaves without appending to `l.relaxedCasts`, which `objectCast` does (`objects.go:935-937`).
- **Repro** (`final-repro/J1_relaxed_subclass.pts`): `A` lacks `note?`, and its subclass `B` has `note = 5`. Then `[1].flatMap<Opt>(() => as)`.
  - final2: `TypeError: Cannot read property note of B as string | undefined: it is declared number` at the read.
  - R3: PTS1001 at compile time (D-066's message).
  - R2: the same TypeError. Base: a different TypeError.
  - Node: `2 a:undefined:undefined b2:number:5`.
- The program fails safely, at run time instead of compile time.

### R4-8. LOW, false positive vs base and R2 (present in R3 too): new inner arrays are treated as kept (VERIFIED)
- **Where:** `arrays.go:629` and `:634`. The non-literal path passes `made=false` for every array leaf.
- **Repro** (`final-repro/H1_new_inner_arrays.pts`): `[1].flatMap<(number | string)[]>(() => ns.map((m) => [m]))`, and `() => out` with a single-use const `out = [[1], [2]]`.
  - final2 and R3: PTS1001 with D-094's message.
  - R2, base and Node: `[ [ 1 ], [ 2 ] ]`.
  - The regular `const r: (number | string)[][] = ns.map((m) => [m])` compiles on every tree (`fp/p05b`).

### R4-9. LOW, noise only: an extra diagnostic for an object literal returned directly (VERIFIED)
- **Where:** `arrays.go:627-629`. Only literals inside a returned array literal are skipped.
- **Repro** (`final-repro/I1_extra_diag.pts`): `flatMap<{ c: J }>(() => ({ c: cobj }))`. final2 and R3 add `4:33` after base's `4:42`. The program fails on every tree anyway.

### Pre-existing, not H05
These are the same in the regular path on every tree:
- `const r: J[] = [...cs]` copies the C objects silently (`g/g01b`). flatMap's skip of spreads matches it (`g/g01`).
- `const r: J[][] = [cs.slice()]` copies them silently too (`g/g02b`), as does flatMap with a new array (`g/g02`).
- A typed object held as `any` and converted to J is copied (`a/b7`).

---

### N1 and N2 re-run
- **N1, fixed.** N1, N1b, N1c and N1d print Node's output on final2; R3 gives PTS9001.
- **N2, fixed.** N2, N2b and N2c match Node.
- **N2d** stays PTS1001, as D-180 says.
- **N3:** the extra diagnostic is gone.
- **N4:** unchanged (pre-existing D-133).

### Checked and fine
- **The check is side-effect free.**
  - `memberFor`, `tupleArray`, `holdingArray`, `providedMember`, `unprovided`, `holdsOptionally`, `isNewArray`, `isSingleUseConst` and `libMemberName` only read.
  - `irType` only fills type caches and `firstResult`, and `keepsLeaves` runs after the callback is lowered (`callbacks.go:105-106`).
  - The emitted C for N1, N1b, N1c, N1d, N2 and N2b equals round 2's, except one unrelated hole-test line, so the check adds no IR.
  - I found no new diagnostics from `irType`: `unknown` narrowed to `Function` fails the same way on the base, and guard intersections, brands, enums, optional chains and generics are fine.
- **Round 3's 301 flatMap probes,** final2 vs R3: 21 differ, all of them the intended N1, N2 and N3 changes.
- **No false positives** for:
  - block bodies with try/finally, labels, switch, loops and continue (`h/h01`, `h02`), with nested functions not visited (`ex/e02`);
  - comma, `as` and `!` returns (`h03`);
  - `this` (`p07`), generic classes and functions (`p08`, `e01`);
  - named functions, `.bind` and default parameters (`p09`, `e07`), async callbacks (`p10`);
  - readonly arrays and tuples (`e04`), optional properties (`e06`, `a/b1`, identity kept), enums (`p06`), brands (`h06`);
  - `[identity]` as a leaf (`p03`): it now works, where R3 gave PTS9001;
  - `unknown` narrowing (`h05`): final2 matches Node, the base was wrong.
- **Integer leaves (`e05`):** a kept `i32[]` as a `number[]` leaf is PTS1001 on final2. That is D-094 by design; the base ran it.
- **New tests:** the `// expect:` blocks of `flatmap_typed_leaves` and `flatmap_literal_leaves` equal Node's and final2's output, and `flatmap_leaf_conversions`' three errors reproduce exactly.
- **Test gaps:** no test covers union, tuple, nested-literal or generic-callback leaves, and `flatmap_typed_leaves` never checks identity (`r[0] === original`).

All probes are under `/tmp/h05r4.Dwwf`, minimal repros in `/tmp/h05r4.Dwwf/final-repro/`. `/tmp` is wiped on reboot, so archive them if you want to keep them.

## Round 5

Agent `opus-reviewer` (claude-opus-5-5, xhigh, read-only), 2026-10-17 05:12–05:41, narrow scope: round 4's changes to the leaf check, CLI `/tmp/h05/s2/pts-final3` (sha256 `ffb98281…`) with `/tmp/h05/s2/review-home5`, the diff `/tmp/h05/s2/suite5.diff`. The parent's 05:22 edit (tuples where arrays go) was made while the round ran and isn't in that CLI. The report below is its final message, verbatim. Its probes (scratch `/tmp/h05r5.pBs2`, without binaries, diffs and its rebuilt sources): [h05-review5-probes.tar.gz](h05-review5-probes.tar.gz).

Round 4's findings are mostly fixed, but the new rules add two false-positive regressions on valid programs (F1, F2). They compile on R4 and the base, and the same conversion compiles outside flatMap. The R4-8 fix also brings back silent identity loss that R4 had rejected at compile time (F3).

**The working tree changed after final3 was built.** `pts/lower/arrays.go`, `pts/tests/errors/flatmap_leaf_conversions.pts` and `docs/DECISIONS.md` were edited at 05:22; `pts-final3` and `suite5.diff` date from 05:10. I reviewed final3. All line numbers below are final3's, from `/tmp/h05r5.pBs2/src/arrays.go`, which I rebuilt from the base plus `suite5.diff`. In the current tree, lines after 691 are 9 lower down.

**How to rerun.** Repros are in `/tmp/h05r5.pBs2/final-repro5/`.
- `/tmp/h05r5.pBs2/bin/cmp.sh <dir>` runs every `.pts` in the directory on final3, R4 and base, and on Node from a `.mts` copy. Outputs land in `<dir>/out/`.
- final3 alone: `PTS_HOME=/tmp/h05/s2/review-home5 /tmp/h05/s2/pts-final3 run F.pts`
- R4: `review-home4` with `pts-final2`. Base: `PTS_HOME=/tmp/h05-base /tmp/h05-base/pts-bin/pts run`.
- I checked that `review-home5` equals the tree's runtime and lib, and that `git diff -- pts` matched `suite5.diff` at the start. I didn't run `go test`, didn't touch `/tmp/h05-suite`, and wrote nothing into the repo.

### F1. MEDIUM, regression vs R4 and base: iterator results going into an `IteratorResult` union are rejected (VERIFIED)
- **Where:** `arrays.go:676-681`. When `memberFor` finds no member and the leaf is an object, the R4-4 rule reports `converting X to U`. `toUnion` first turns any program object into a result union with `asResult` (`unions.go:202-204`), so the regular conversion compiles the same program.
- **Repro:** `F1_iterator_result_union.pts`
  ```ts
  function* g() { yield 1; yield 2; }
  const it = g();
  const r = [1].flatMap<IteratorResult<number>>(() => [it.next()]);
  const o = { done: false as const, value: 7 };
  const r2 = [1].flatMap<IteratorResult<number>>(() => [o]);
  ```

  | final3 | R4 | base | Node |
  |---|---|---|---|
  | `3:54 PTS1001 converting object3 to object1 \| object2`, and `6:55` the same for `{done: boolean; value: number}` | `1 false 1` / `true` | same as R4 | same as R4 |

- **Regular analog** (`F1b_regular.pts`, `const r: IteratorResult<number>[] = [it.next()]`) prints Node's output on every tree.
- **Same failure:** `it/i06` (a shape), `it/i07` (a class instance) and line 4 of `it/i09` (a kept result).
- **Fix:** before the error, `if isResultUnion(u) && isObjectOfProgram(leaf) { return true }`.

### F2. MEDIUM, regression vs R4 and base: a conditional element with a new-array branch is rejected (VERIFIED)
- **Where:** `arrays.go:635` gives a conditional element `made = l.isNewArray(element)`, which is false for a `ConditionalExpression`. R4-1's member split (`:673-674`) then checks the branch's `number[]` against `(number | string)[]`.
- **Repro:** `F2_cond_new_array_element.pts`
  ```ts
  [1, 2].flatMap<(number | string)[] | null>((i) => [i === 1 ? [i] : null])
  ```
  - final3: `1:62 PTS1001 using a number[] as a (number | string)[] where it may be changed or kept…`
  - R4, base and Node: `[[1],null]`.
- **Regular analog** (`F2b_regular.pts`): `const r: ((number | string)[] | null)[] = [i === 1 ? [i] : null]` compiles on final3 and R4 and prints `[[1]]`.
- **Same failure:** `fp2/u01` (`… : "s"`), line 3 of `fp2/u02`, line 3 of `fp3/v06`, `fp3/v07` (inside a nested literal).
- **Same cause, already in R4:** `fp3/v01` (`[i === 1 ? [i] : [i, i]]`) is PTS1001 on final3 and R4, while the base and the regular analog `v01b` run.
- **Fix:** in `keepsLiteralLeaves`, check each branch of a conditional element as an element of its own: a literal branch goes through `arrayTarget`, any other branch gets `isNewArray(branch)`.

### F3. MEDIUM-LOW, silent identity loss, regression vs R4 (base and the regular D-094 path do the same) (VERIFIED)
- **Where:** `arrays.go:619`. `made = l.isNewArray(value)` says the returned container is new, and the rule applies that to its elements, which may be kept.
- **Single-use consts:** `isSingleUseConst` counts uses in the source, but the callback runs once per element.
- **Repro:** `F3_new_outer_kept_inner.pts`
  ```ts
  class H { arr = [1, 2]; }
  const hs = [new H()];
  const r = [1].flatMap<(number | string)[]>(() => hs.map((h) => h.arr));
  hs[0].arr.push(3);
  const out = [[1], [2]];
  const r2 = [1, 2].flatMap<(number | string)[]>(() => out);
  console.log(r2.length, r2[0] === r2[2]);
  ```

  | final3 | R4 | base | Node |
  |---|---|---|---|
  | `[[1,2]]` / `4 false` | PTS1001 (D-094) | `[[1,2]]` / `4 false` | `[[1,2,3]]` / `4 true` |

- **Same failure:** `mk/m02` (`out = [a, [2]]` with a kept `a`), `mk/m03`, `mk/m04`, `mk/m05` (`nested.slice()`).
- **R4-1's `g/g08` is still wrong** for this reason: the single-use `items` makes its kept `ns` count as made.
- **Regular analogs** `m02b`, `m04b`, `m01b` and `m03b` (in a loop) lose identity the same way on every tree.
- **Suggestion:** narrow `made` to leaves that are provably new: map callbacks whose returns are new arrays, and literal initializers of consts declared inside the callback. Otherwise record it as a known gap of D-180/D-094 with a skipped test.
- **Test gap:** `flatmap_leaf_identity`'s `[1].flatMap(() => out)` calls the callback once, so it can't catch the cross-call loss.

### F4. LOW-MEDIUM, false negative left over from R4-5: nested tuple literals are still copied silently (VERIFIED)
- **Where:** `arrays.go:629-633` and `:644-652`. `arrayTarget` returns only an `*ir.Array`, so a tuple target, or a union with two array members, is skipped.
- **Repro:** `F4_nested_tuple_literal.pts`, `flatMap<[J, number]>(() => [[cobj, 1]])`, then `cobj.x = 5`.

  | final3 | R4 | base | Node |
  |---|---|---|---|
  | `1 1` | `1 1` | TypeError | `1 5` |

- The regular `const r: [J, number][] = [[cobj, 1]]` (`nl/n02b`) is PTS1001 on every tree.
- **Same failure:** `nl/n09` (a tuple in a union) and `nl/n10` (a kept array in a tuple; base TypeError, regular `n10b` PTS1001).
- `nl/n01` (`flatMap<J[] | string[]>(() => [[cobj]])`) is silent on every tree, while the regular conversion is PTS1001.

### F5. LOW-MEDIUM, regression vs R4 and base: made tuple leaves where an array goes throw at run time (VERIFIED)
- **Where:** `arrays.go:692`. With `made` true the check passes, but the runtime can't convert a tuple into an array.
- **Repro:** `F5_made_tuple_into_array.pts`: a single-use `pairs: [number, string][]` into `flatMap<(number | string)[]>`.

  | final3 | R4 | base | Node |
  |---|---|---|---|
  | `TypeError: Expected (number \| string)[], got Array` | PTS1001 | `[[1,"a"],[2,"b"]]` | `[[1,"a"],[2,"b"]]` |

- This is the one real diff in the 301-probe corpus (`cm/t1_tuple_leaves`). `tu/t02` (`ns.map((m): [number, string] => …)`) behaves the same way.
- The 05:22 edit (`case isTuple(leaf) && isArray(elem)`) turns it into PTS1001, and the tree's errors test now expects that tenth error at 63:46. final3 produces only the other nine. **That edit is in no binary I tested, so it is unverified.**
- The regular `t02b` throws the same TypeError on every tree, so that part is pre-existing.

### Smaller items (VERIFIED)
- **R4-6, partly fixed:** an interface leaf going where a class goes still fails only at run time (`g/g10`: `TypeError: Expected D, got Object`), the same as R4 and the base. Class leaves are fixed.
- **R4-7, partly fixed:** flatMap's leaf conversions aren't counted as sources by `interfaceCasts`. A later relaxed cast from the result's interface misses them.
  - `rc/r01` (`Opt` to `Opt2`, where a `B` holds `extra` as a string) gives a TypeError at run time on every tree; the regular `r01b` is PTS1001.
  - Memory-safe.
- **R4-9, still there:** the second diagnostic has moved to the call (4:11). It now comes from the helper's push conversion, and D-180's known gaps list it. It is noise only: valid object literals returned directly compile (`ol/o01`, `ol/o02`).
- **Consistent with the regular conversion, though R4 or the base ran them:**
  - `cl/c01` (a `Sub` into `A | B`)
  - `it/i02` (a result into an interface whose `done` it doesn't provide)
  - line 6 of `it/i09`
  - `rc/r02`, `r04`, `r05` (D-066 is conservative: `r05` errors for a subclass that is never instantiated, as `r05b` does)
  - `fp2/u07` (a tuple into `[number, string?]`)
- **Generic callbacks fail safely:** `gen/q01` (`flatMap<number[]>(one)`, tsc's type is wrong) gives a TypeError at run time on final3 and the base.

### Round 4 status
- **Fixed:** R4-1 (except `g08`, see F3), R4-2 (G1, G2, and the generic static `q03`), R4-3, R4-4, R4-5 for arrays, R4-6 for classes, R4-7 (J1), R4-8 (H1).
- **Not fixed:** R4-9 (moved to the call, now a documented gap), R4-6 for interface leaves, R4-5 for tuple targets.

### Checked and fine
- **No IR side effects:** 22 programs that both final3 and R4 accept emit byte-identical C. The only state the check changes is the `relaxedCasts` append, and it fires only for leaves the callback can return, as `objectCast` does.
- **Round 3's corpus (301 probes):** apart from the scratch path, only `cm/t1_tuple_leaves` differs from R4's outputs (F5).
- **No false positives:**
  - shapes of the same layout, readonly and optional, with methods, nested (`fp/s01`–`s04`, `sh/h01`–`h04`)
  - class hierarchies, interfaces, abstract classes, `this` (`s05`, `s06`, `c03`, `c06`)
  - generic classes (`s07`, `c05`)
  - unions with null, undefined, functions and numbers (`s08`–`s10`)
  - enums, readonly arrays and tuples, `as const`, named tuples (`s12`, `k01`, `u06`)
  - iterator results: inferred, custom iterators, `| null` (`i03`, `i04`, `i05`, `i08`)
  - interface unions (`c04`), object literals in a union context (`u05`)
  - valid nested literals at several depths (`n03`, `n07`, `n08`), valid relaxed casts (`r03`)
- **Tests:** `flatmap_leaf_identity`'s `expect:` equals Node's and final3's output.
- **Test gaps:** no test covers `flatMap<IteratorResult<…>>`, conditional elements, nested tuple literals, or identity across calls.

Everything is in `/tmp/h05r5.pBs2`, with the minimal repros in `/tmp/h05r5.pBs2/final-repro5/`. `/tmp` is wiped on reboot, so archive it if you want to keep it.

## Round 6

Agent `opus-reviewer` (claude-opus-5-5, xhigh, read-only), 2026-10-17 06:35–07:00, a confirmation round after the commit: round 5's fixes in commit `c158778d4` (CLI `/tmp/h05-close/pts-bin/pts`, sha256 `c2cacc50…`, built in a worktree of that commit; the reviewer ran it with its own home holding the same runtime and lib, because the parent's worktree lacked its submodules when the round started, as its report says), against round 5's CLI `pts-final3` and the base. Unreviewed delta: `/tmp/h05-r6/r5-to-final-src.diff` (`pts/lower/arrays.go`, `lower.go`). The report below is its saved `report.md`, verbatim. Its probes (scratch `/tmp/h05r6.Zxx3`, without its runtime home, `.mts` copies and its copies of rounds 3 and 4's probe sets, which those rounds' archives hold): [h05-review6-probes.tar.gz](h05-review6-probes.tar.gz).

NOT READY

Scratch: /tmp/h05r6.Zxx3. Final CLI run with PTS_HOME=/tmp/h05r6.Zxx3/home: lib and runtime copied from /tmp/h05-close, third_party copied from review-home5. `diff -rq` showed both identical to the repo's. I built it because /tmp/h05-close lacked its submodules when I started. No result below comes from the broken home.
Compare helper: /tmp/h05r6.Zxx3/bin/cmp.sh <dir> runs each .pts in <dir> on final, R5 and base, and on Node from a .mts copy, one compile at a time. Outputs go to <dir>/out/.

1. MEDIUM-LOW, VERIFIED. F3's fix covers returned values only. A single-use const used as an element of a returned array literal is still copied at each call, so identity is lost silently.
   p1/a1_single_use_const_elem.pts:
     const out = [1];
     const r = [1, 2].flatMap<(number | string)[]>(() => [out]);
     console.log(r.length, r[0] === r[1]);
     r[0].push(9);
     console.log(JSON.stringify(r));
   final, R5 and base: `2 false` / `[[1,9],[1]]`. Node: `2 true` / `[[1,9],[1,9]]`.
   Same result:
   - a3 (`[out.sort()]`)
   - a4 (`[[out]]` into `flatMap<(number|string)[][]>`)
   - p5/n6 (`[[i, ws]]` into `[number, (string|number)[]]`: final `false`, Node `true`)
   Regression vs R5: a2 (`(i) => [i > 0 ? out : [i]]`) was PTS1001 on R5. Final prints `2 false`, Node `2 true`; base is the same as final.
   Cause: arrays.go:659 `l.keepsLeaf(t, elem, l.isNewArray(element), element)`. isNewArray calls isSingleUseConst (:1142), which counts uses in the source, not calls. makesNewArrays (:681) excludes consts for a returned value, but keepsElement doesn't. This contradicts D-180's own rule ("a const's arrays, even one used once, are the same at each call").
   The regular D-094 analog in a loop (a6) loses identity on every tree, so that part is shared and pre-existing.
   A const declared inside the callback (w1, `const a = [i]; return [a];`) is genuinely new and must stay accepted.

2. LOW-MEDIUM, VERIFIED. A spread inside a nested tuple literal shifts the field check: target(i) uses the literal's index, not the tuple's position (arrays.go:648-653, with the spread skipped at :639).
   - False negative, p2/c1_spread_shift_tuple.pts:
       interface J { x: number | string }
       class C { constructor(public x: number) {} }
       const c = new C(1);
       const p: [number, number] = [1, 2];
       const r = [1].flatMap<[number, number, J]>(() => [[...p, c]]);
       c.x = 5;
       console.log(r.length, r[0][2].x);
     final: `1 1`, R5: `1 1`, base: TypeError, Node: `1 5`. The regular analog p2/c1b_regular (`const r: [number, number, J][] = [[...p, c]]`) is PTS1001 on every tree.
   - False positive, regression vs R5: p3/c12_spread_shift_fp.pts uses `const q: [J, J]` and `flatMap<[J, J, C]>(() => [[...q, c]])`. c is checked against J.
     final: `5:48 PTS1001 using a C as a {x: number | string}…`, R5: `1 5` (= Node), base: TypeError.

3. LOW, VERIFIED, regression vs R5 and base (a compile error, never wrong code). makesNewArrays accepts only `xs.map(cb)` whose cb returns bare array literals. These new-at-each-call returns are now PTS1001 "using a number[] as a (number | string)[] where it may be changed or kept…", where R5 and base printed Node's output:
   - p1/b1 `ns.map((m) => (m > 1 ? [m] : []))`
   - b2 `ns.map((m) => [m] as number[])`
   - b3 `ns.map((m) => [m]).reverse()`
   - b4 `Array.from({ length: 2 }, (_, i) => [i])`
   - b5 `ns.map((m) => m.slice())`
   - b6, a const declared inside the callback:
       const r = [1, 2].flatMap<(number | string)[]>((i) => {
         const a = [[i], [i + 1]];
         return a;
       });
     Node, R5 and base print `[[1],[2],[2],[3]]`.
   Round 5 suggested counting consts declared inside the callback as new. The single-call `[1].flatMap(() => out)` in round 4's H1/p05 is also PTS1001 now; D-180 does that on purpose.

4. LOW, VERIFIED, pre-existing on R5. `as const` and `satisfies` around a nested tuple literal skip F4's field check: the node isn't an ArrayLiteral, so it reaches `case made: return true` (arrays.go:756) for tuple to tuple.
   p5/n1_as_const_tuple_elem.pts: `[1].flatMap<readonly [J, number]>(() => [[c, 1] as const])`, then `c.x = 5`.
   final and R5: `1 1`, base: TypeError, Node: `1 5`. The regular analog n1b is PTS1001. p6/o4 does the same with `satisfies [C, number]`.
   Copies of map-made tuples (p6/o2 and o2b) are silent on every tree; D-180 already lists that gap.

5. LOW, VERIFIED, pre-existing on every tree. A nested literal shorter than an optional tuple fails at run time.
   p7/v2_short_optional_tuple.pts: `[1, 2].flatMap<[number, string?]>((i) => [[i], [i, "s"]])`.
   final, R5 and base: `TypeError: Expected [number, string | undefined], got Array`. Node: `[[1],[1,"s"],[2],[2,"s"]]`. The regular p8/w2 works. Memory-safe. It isn't in D-180's known gaps.

6. Nits, VERIFIED.
   - D-180's known gap "a nested array literal where a union with two array members goes isn't checked" also covers two tuple members. p2/c5 (`flatMap<[J, number] | [string, number]>(() => [[c, 1]])`) prints `1 1` on final and R5, Node `1 5`.
   - For a tuple leaf that isn't made (F5's `pairs`), the message is reportKeptCopy's. It says "a new array … is copied", but even new tuples are rejected.

Round 5 status
- F1 fixed. The F1 repro and it/i01, i06, i07 and i09 line 4 equal Node; so do p3/i3 (a kept array of results) and i5 (a getter object).
  Unions of results with other members (`| undefined` in p3/i1, `| number` in i6) are still PTS1001, as the regular conversion is (i1b).
- F2 fixed. The F2 repro and fp2/u01, u02 and fp3/v01, v02, v06, v07 equal Node.
- F3 partly fixed. The returned-value forms are PTS1001 now: the F3 repro, mk/m02–m05, g/g08, p1/b7 (Object.values) and p4/k8 (readonly target); R5 lost identity on all of them. Literal elements are not fixed (finding 1).
- F4 partly fixed. Plain nested tuple literals are checked: the F4 repro, nl/n02, n09, n10, p2/c6, c9 and p5/n4. Spreads (finding 2), `as const` and `satisfies` (finding 4), and two tuple members (finding 6) slip past.
- F5 fixed, as PTS1001 (a documented gap). The F5 repro, tu/t01, t02 and cm/t1_tuple_leaves give it.

Checked and fine
- Round 3's 301-probe corpus (bin/r3all.sh): only cm/t1_tuple_leaves differs from R5 (F5, now PTS1001 as on R4).
- Round 5's 116 probes (bin/rerun.sh, logs r5p-a.log and r5p-b.log): 22 changed. All are the fixes above; g05's error moved to the branch.
- Round 4's 79 probes (r4.log): only H1/p05 line 5 (by design), g05 (position) and g08 (now PTS1001) differ from R5.
- No segfault, PTS9001 or panic in any final output.
- `.par.flatMap` (par/q1.pts) at PTS_THREADS=1, 2 and 4 equals Node's sequential run, including identity for kept arrays, objects and iterator results. q2 is C73's documented PTS1001.
- Valid nested tuples with function, enum, subclass, null-union and kept same-type array fields: p5/n2, n3, n5 and p2/c7.
- Also fine: interface unions (j1), let, closure and method returns (j5, j6), block-bodied map (b8), an inner const element (w1), any[] and unknown[] targets (k1–k3, k7, now PTS1001 where R5 threw or lost identity).
- lower.go's change is message-identical.
- Tests: flatmap_leaf_identity's expect equals Node's output and final's. flatmap_leaf_conversions' expected 12 errors equal final's output, in order. They pin F1, F2, F3 (returned const only), F4 (plain literal) and F5.
  They don't pin F3's `hs.map((h) => h.arr)` (a mapper that isn't a literal), and nothing covers findings 1 and 2.

## Round 7

Agent `opus-reviewer` (claude-opus-5-5, xhigh, read-only), 2026-10-17 07:14–07:33, on the parent's first round-6 fix (uncommitted; snapshot `/tmp/h05-r6/suite`, CLI `/tmp/h05-r6/cli/pts`, sha256 `7a873c9e…`; diff `/tmp/h05-r6/round6-fix.diff`), against commit `c158778d4` and the base. The harness blocked its `report.md`, so the report below is its final message, verbatim but for its headings, one level down. Its probes (scratch `/tmp/h05r7.4PRr`, without `.mts` copies, built binaries and its copies of rounds 3–5's probe sets): [h05-review7-probes.tar.gz](h05-review7-probes.tar.gz).

NOT READY

The harness blocked writing `/tmp/h05r7.4PRr/report.md`, so the full report is below. `/tmp/h05-r6/review7.done` has been touched. Scratch is `/tmp/h05r7.4PRr`. The helper `bin/cmp.sh <dir>` runs each `.pts` file on the fix, the commit and the base, and on Node from a `.mts` copy; outputs go to `<dir>/out/`. The repo's working-tree `arrays.go`, `lower.go`, `convert.c` and test files are byte-identical to `/tmp/h05-r6/suite`.

### Findings

**1. MEDIUM, VERIFIED. Silent identity loss, a regression vs the commit (the base is wrong the same way).**
The check now treats object literals as "made as their context's type" in three places where the flatMap type gives them no context: a map or Array.from mapper's return values, the initializer of a returned single-use const, and a literal under `as`. Kept arrays or objects in their properties are then copied without an error. The commit gave PTS1001 for each.

Repro `g/g1_realistic_mapper_obj.pts`:
```ts
interface Tagged { name: string; tags: (string | number)[] }
const groups = [{ name: "g", items: [{ tags: ["a"] }] }];
const flat = groups.flatMap<Tagged>((g) => g.items.map((i) => ({ name: g.name, tags: i.tags })));
flat[0].tags.push("b");
console.log(JSON.stringify(groups[0].items[0].tags), JSON.stringify(flat));
```
- Fix: `["a"] [{"name":"g","tags":["a","b"]}]`. Base: the same.
- Commit: PTS1001 "using a `{name: string; tags: string[]}` as a `{name: string; tags: (number | string)[]}`, whose `tags` it doesn't provide with the same types".
- Node: `["a","b"] [{"name":"g","tags":["a","b"]}]`.

Same result (fix and base wrong, commit PTS1001) in:
- `d/d1`: `ns.map(() => ({ arr: kept }))`
- `d/d2`: `ns.map(() => ({ c }))` into `{ c: J }`. Fix `2 1`, Node `2 5`.
- `d/d4`: the Array.from version.
- `g/g6`: a conditional object literal in a mapper.
- `e/e1`: `const a = [{ arr: kept }]; return a;`
- `e/e2`: the same with `{ c }`.
- `e/e3`: `return [{ arr: kept }] as { arr: number[] }[]`
- `e/e5`: `a.slice(0, 1)` of such a const.

Cause: `arrays.go:780` (`case ast.KindOmittedExpression, ast.KindObjectLiteralExpression: return true`). It is now reached through the new entry points: `arrayMapper` at :633-634, the `singleUseConstInitializer` recursion at :629, and `castLiteral` at :618 and :764.
- Still wrong on every tree (pre-existing): `d/d3` (an object in a cast nested literal) and `d/d8` (`ns.map(() => [{ arr: kept }])`).
- Must keep working after a fix: corpus `lt/l8` and `repro/N2d` (fresh objects with no kept properties) were PTS1001 on the commit and now match Node.

**2. LOW-MEDIUM, VERIFIED. A compile error became a runtime TypeError (memory-safe). Regression vs the commit; the base also threw.**
D-180 says "a tuple leaf where an array goes is PTS1001", and the commit did that. A tuple-typed literal where an array goes now throws `Uncaught TypeError: Expected (number | string)[], got Array` at run time instead.

Repro `a/a1_as_tuple_elem.pts`:
```ts
const r = [1, 2].flatMap<(number | string)[]>((i) => [[i, "a"] as [number, string]]);
console.log(JSON.stringify(r));
```
- Fix: TypeError. Base: TypeError.
- Commit: PTS1001 "using a `[number, string]` as a `(number | string)[]` is not supported yet".
- Node: `[[1,"a"],[2,"a"]]`.

The same happens in:
- `a2`: an annotated mapper, `ns.map((m): [number, string] => [m, "a"])`.
- `a3`: `satisfies`. `a4`: `as const`.
- `a5`: `const a: [number, string][] = [[i, "a"]]; return a;`
- `a6`: an outer cast. `a7`: Array.from with an annotated mapper. `a8`: a function-expression mapper. `a9`: nested. `a12`: a block-bodied callback.
- `k/k1`–`k3`: union element types, also TypeErrors.
- Earlier rounds' probes: round 5's `r5p/tu/t02`, and round 6's `p4/k1`, `k2` and `p8/w3`.

Cause: `castLiteral` (:618, :764), `arrayMapper` (:633) and the const recursion (:629) check the literal against `nestedLiteralTarget(elem)`. They ignore the cast, the mapper's return annotation or the const's annotation, any of which makes the value a tuple.

**3. LOW, VERIFIED. False PTS1001 in regular D-094 code from the narrowed `isSingleUseConst`. Regression vs the commit, and vs the base for fb3, b1 and b2.**
These programs are valid, compiled on the commit and matched Node:
- Corpus `r2all/cu/u6` (copied to `u/u6.pts`). The fix gives PTS1001 "using a string[] as a (number | string)[] where it may be changed or kept…". The commit printed `[ 1, 's', 't' ]`, which matches Node; the base threw at run time. `u1`, `u5` and `u8` do the same with a `[number, string]` const. flatMap spreads the array, so the copy can't be observed.
  ```ts
  const shared: string[] = ["s", "t"];
  const r = [1, 2].flatMap<number | string>((x) => (x > 1 ? shared : [x]));
  console.log(r);
  ```
- `u/fb3_method_leaf.pts`: `const cbs: (() => number)[] = [g]; [1, 2].flatMap((): Cb[] => cbs)`. Fix PTS1001; the commit and base print `2 1`, which matches Node.
- `b/b1`: a static field, `static values: (number | string)[] = DEFAULTS`. A static initializer runs once. Fix PTS1001; commit and base `[1,2,"x"]`, matching Node.
- `b/b2`: `for (const x of tag(xs))`. A for-of's expression runs once. Fix PTS1001; commit and base `1 2 end`, matching Node.
- By design, but a common program shape worth a line in D-094: `h/h5`, `const DATA = [3, 1, 2]; function main() { show(DATA); } main();`.
- `r2all/r1/p/s5` (`.par.flatMap<number | string>(() => shared)`) is now PTS1001. That fits D-180's "an existing number[]" rule.

Cause: `arrays.go:1281-1282`. The walk counts every loop, function or class boundary, including parts that run once: a for-of's expression, static initializers, and callbacks whose returned array flatMap spreads.

**4. LOW, VERIFIED. False PTS1001, regression vs the commit.**
A literal element that is a new array of new arrays is now rejected. Repro `c/c1`:
```ts
const ns = [1, 2];
const r = [1].flatMap<(number | string)[][]>(() => [ns.map((m) => [m])]);
```
Fix PTS1001; the commit and base print `[[[1],[2]]]`, matching Node. `c2` (Array.from) and `c3` (concat of literals) behave the same.

Cause: a made leaf goes to `keepsElementsOf` (:857, :882), which judges the inner arrays by type, as kept arrays. The stricter rule is still right for `r4/g02` (`[cs.slice()]`), which lost identity on the commit. Checking the element's expression instead (calling `keepsLeavesOf` on the element) would get both cases right.

**5. LOW, VERIFIED. False PTS1001, regression vs the commit and the base.**
`filter` with a type-guard predicate is checked against the receiver's wider element type. Repro `g/g2`: with `all: (C | D)[]`, `[1].flatMap<J>(() => all.filter((s): s is D => s instanceof D))`. Fix: PTS1001 "using a `C` as a `{x: number | string}`…". The commit and base print `1 d`, matching Node.

Cause: the `sameElementsReceiver` recursion (:636) ignores the call's narrowed result type.

**6. Nits, VERIFIED.**
- `optional_tuple_conversions.pts` line 8 (`short.map((t) => t)` into `[number, string?][]`) pins a conversion that copies kept tuples. My `m/m1` adds `short[0][0] = 9`: the fix prints `[[1],[2]]`, Node `[[9],[2]]`, and the commit and base threw. `m/m2`, the same-length case, loses identity on every tree. That is D-180's documented "a new array's elements" gap, now extended from a TypeError to a silent copy. The test's comment shouldn't present this as plain JS behaviour. `m/m4` (dynamic data) is D-071's documented copy.
- `DECISIONS.md` is unchanged. D-180 still states the map-of-literals rule and "a tuple leaf where an array goes is PTS1001", which finding 2 makes untrue. D-094 doesn't record the narrowed single-use rule.

### Round 6 status
1. **Fixed.** a1–a5 and a6 (the regular-loop case) are now PTS1001. A const declared inside the callback (w1, b6) still works.
2. **Fixed.** c1 is now PTS1001 and c12 matches Node. The inexact-spread false PTS1001 is the accepted limitation.
3. **Fixed.** b1–b6 match Node. b7 (Object.values) is PTS1001 by design.
4. **Fixed.** n1 and o4 are now PTS1001, as are o1, o2 and o3, which were silent.
5. **Fixed.** v2 matches Node, as do f1, f2, f8, f10 and f11. Too few required elements, an empty array and too many elements still throw (f3, f4, f5). Valgrind reports no invalid accesses.
6. **Partly fixed.** The message nit is fixed. The union with two tuple members (`p2/c5`) still prints `1 1` against Node's `1 5`, and D-180's wording still covers only two array members.

### Checked and fine
- **New tests.** Both run tests' `expect` blocks equal Node's output and the fix's. Both errors tests' expected diagnostics equal the fix's output exactly and in order. Each listed error is a real identity loss in Node, except the by-design `make()` and `K` cases.
- **Round 3 corpus (301 probes):** 9 differ from the commit. They are `cm/t1` (message only), `lt/l8` and `N2d` (now match Node), and `u1`, `u5`, `u6`, `u8`, `fb3` and `s5` (finding 3).
- **Round 4 and 5 probes (195):** 8 differ. g01 and g02 (in both rounds), m03b and m04 are improvements. t01 is the message change and t02 is finding 2.
- **`.par.flatMap`** (`par/q2.pts`) at PTS_THREADS=1, 2 and 4 matches Node's sequential run. `par/q1`'s PTS1001s are the same as on the commit.
- **Crashes:** no panic, PTS9001, segfault or internal compiler error in any output. The new scope walk can't be reached from another file's global const, because module detection is forced (`x/` gives TS2304).
- **Everyday regular code** is unaffected: b4–b9, h1–h3, h6, n1–n3, c5–c9, c11–c14, g3 and g4.
- **`convert.c`** stays in bounds: source fields are read only below the source's length, and `properties[length]` is read only when length is less than count. An optional element left out stays zero (absent), where the tuple's length stops counting.

### Commands (at most 2 compiles at a time, all scratch in `/tmp/h05r7.4PRr`)
- `bin/r6fix.sh`: round 6's p1–p8 run on the fix, compared with the stored Node and commit outputs (`r6.log`).
- `bin/cmp.sh` on `a/`, `b/`, `c/`, `d/`, `e/`, `f/`, `g/`, `h/`, `k/`, `m/`, `n/` and `u/`.
- Run tests: Node on `.mts` copies compared with the `// expect:` blocks, plus `pts run`. Errors tests: `pts run` compared with the `// error:` lines (`t/`).
- `bin/r3fix.sh 0|1` and `bin/r45fix.sh 0|1`: the corpora compared with the commit's stored outputs (`r3-*.log`, `r45-*.log`).
- `par/`: `PTS_THREADS=1|2|4 PTS_HOME=/tmp/h05-r6/suite /tmp/h05-r6/cli/pts run q1.pts` and `q2.pts`, compared with Node on `q*seq.mts`.
- `vg/`: `pts build`, then `GC_DONT_GC=1 PTS_THREADS=1 valgrind -q --undef-value-errors=no --error-exitcode=99` on f1, f2, f4, f5, f8 and `optional_tuple_conversions`.

## Round 8

Agent `opus-reviewer` (claude-opus-5-5, xhigh, read-only), 2026-10-17 08:00–08:16, on the parent's reworked fix (uncommitted; snapshot `/tmp/h05-r6/suite2`, CLI `/tmp/h05-r6/cli3/pts`, sha256 `4f36293c…`; diff `/tmp/h05-r6/round7-fix.diff`), against round 7's CLI, commit `c158778d4` and the base. The report below is its final message, verbatim but for its headings, one level down. Its probes (scratch `/tmp/h05r8.vXBS`, without `.mts` copies, built binaries and its copies of round 7's probes): [h05-review8-probes.tar.gz](h05-review8-probes.tar.gz).

**NOT READY**

The rework fixes all of round 7's code findings (1–5). Three new silent wrong-output paths are regressions against the commit: the commit gives PTS1001 on each, and the new CLI compiles them and prints the wrong result. There are also a few low-severity false PTS1001s. Unless noted, every probe is under `/tmp/h05r8.vXBS/<dir>/` and its outputs are in `<dir>/out/<name>.{node,new,r7,commit,base}`.

### Findings

**1. Medium, VERIFIED. Object copies lose extra properties and property order.** Regression against the commit (PTS1001 there). Base and round 7 give the same wrong output.

`keepsObjectLiteral` (`pts/lower/arrays.go:819-849`) approves copying a non-made object literal into an interface property by property. It skips properties the interface doesn't have (`field == nil`, line 843) and ignores order. The runtime copy writes the target shape, so both the extra properties and the literal's order are lost.

`o/o26_order.pts`:
```ts
interface K { x: number | string; y: number }
const ns = [1, 2];
const r = [1].flatMap<K>(() => ns.map((m) => ({ y: m, x: m })));
console.log(JSON.stringify(r));
```
- Node: `[{"y":1,"x":1},{"y":2,"x":2}]`
- New, round 7 and base: `[{"x":1,"y":1},{"x":2,"y":2}]`
- Commit: PTS1001 at 6:26

`o/o25_realistic_extra.pts` is a realistic case: `orders.flatMap<Item>((o) => o.lines.map((l) => ({ name: l, qty: 1, order: o.id })))`.
- Node keeps `"order":1`; the new CLI drops it.

The same happens through a const (`o20`), a cast (`o21`), `Array.from` with a mapper (`o22`), a nested literal (`o23`), extra properties (`o1`) and an `unknown` field (`w/w2_unknown_field`, reordered). Each prints the wrong result on the new CLI and gives PTS1001 on the commit.
- Related, pre-existing on every tree: the made literal returned directly (`o19`) and the regular conversion `const ks: K[] = ns.map(...)` (`r/r2_order`, `r/r1_regular_extra`).
- Suggested fix: fall back to `keepsLeaf` unless the literal's property names are exactly the target's fields, in the target's order.

**2. Medium, VERIFIED. Spreads of non-array iterables aren't checked.** Regression against the commit when the literal sits in a const or a cast. The direct literal and the mapper cases (`p2`, `p17`, `p18`, `p20`) were already wrong on the commit and base.

`keepsElements` (`arrays.go:686`) and `keepsTupleElements` (`arrays.go:731`) skip a spread of a `Set`, a `Map`'s values or a generator, on the grounds that "the runtime checks" it. In fact the runtime copies the typed `number[]` values into the new element type, so their identity is lost.

`p/p19_iter_spread_in_const.pts`:
```ts
const kept = [1];
const r = [1, 2].flatMap<(number | string)[]>(() => { const a = [...new Set([kept])]; return a; });
kept.push(2);
console.log(JSON.stringify(r));
```
- Node: `[[1,2],[1,2]]`
- New, round 7 and base: `[[1],[1]]`
- Commit: PTS1001 at 2:47

`q/q1_cast_iter_spread.pts` (`[...new Set([kept])] as number[][]`) gives the same split.
- Suggested fix: check the iterated element type with `keepsLeaf(…, made=false)`.

**3. Medium, VERIFIED. Shorthand property uses aren't counted as uses.** Regression against the commit for consts inside callbacks and mappers. Already wrong on every tree for the regular D-094 rule (`s/s2`, and `export { a }` in `x/main.pts`) and for a returned literal (`s/s1`).

`singleUseConstInitializer` (`arrays.go:1405`) counts uses with `GetSymbolAtLocation(n) == symbol`. For a `{ a }` shorthand name that returns the property's symbol, not the const's. So a const that escapes through `{ a }` looks single-use, and `constMadeIn` and `isNewArrayIn` treat it as new.

`s/s3_shorthand_escape_mapper.pts`:
```ts
const seen: { a: number[] }[] = [];
const ns = [1, 2];
const r = [1].flatMap<(number | string)[]>(() =>
  ns.map((m) => { const a = [m]; seen.push({ a }); return a; }));
seen[0].a.push(99);
console.log(JSON.stringify(r));
```
- Node: `[[1,99],[2]]`
- New and base: `[[1],[2]]`
- Commit: PTS1001

Writing `{ a: a }` instead (`s4`) gives PTS1001 on the new CLI, which confirms the cause. `s/s5_shorthand_escape_returned_const.pts` (a callback const returned directly) gives the same split.

The same root cause gives a false PTS1001 for a fresh `const arr = [m]; return { arr };` (`o/o5`). The commit gives PTS1001 there too, but `{ arr: arr }` compiles (`o24`).
- Suggested fix: also count shorthand names, using `checker.GetShorthandAssignmentValueSymbol`, and export specifiers.

**4. Low, VERIFIED. False PTS1001s that the commit and base compiled and ran like Node.**
- **`p/p1_loop_return.pts`:** `const a = [i]; for (const m of ns) { if (m > i) return [a]; } return [];` gives PTS1001 at 5:24. The cause is the new rule that no loop may lie between the declaration and the use (`repeats`, `arrays.go:1431`).
- **`q/q2_nested_flatmap`, `q3_array_of`, `q4_flat_literal`:** a new array of new arrays made by a nested `flatMap`, by `Array.of([i], [i + 1])` or by `[[[i]], [[i + 1]]].flat()` gives PTS1001. Only `map`, `Array.from` with a mapper, `concat` and the same-elements methods are followed. The rest fall back to a type check that assumes the elements are kept. This is left over from round 7's finding 4.

**5. Nit.** `docs/DECISIONS.md` is still unchanged. D-180 and D-094 don't describe the per-call const rule, the property-by-property object rule, or the shorter-optional-tuple conversion.

### Round 7 status
1. **Fixed.** `d1`–`d4`, `d8`, `e1`–`e3`, `e5`, `g1` and `g6` now give PTS1001, where round 7's CLI printed the wrong result. Finding 1 above is a new hole in the same path.
2. **Fixed.** `a1`–`a12` and `k1`–`k3` give PTS1001 instead of a TypeError. `a11` compiled on round 7 but is PTS1001 again, as on the commit; this matches D-180's tuple rule.
3. **Fixed.** `b1`–`b3`, `h4`, `h5`, `fb3` and `u1`/`u5`/`u6`/`u8` match Node again.
4. **Fixed for `map`, `Array.from` and `concat`** (`c1`–`c3`, `p9`, `p10`, `p16`). Other sources remain (finding 4).
5. **Fixed.** `g2` and `p22` (guard inferred from `instanceof`) match Node. `p11` (a generic receiver) is PTS1001 on both the new CLI and the commit, a gap that already existed.
6. **Test nit fixed** (`short.map` is no longer pinned). DECISIONS is still open.

### Checked and fine
- The new tests: both run tests' `expect` blocks equal Node's output and the new CLI's. `flatmap_kept_elements` gives exactly the 20 listed errors and `flatmap_leaf_conversions` the 12 listed; I reviewed each of the 20 and all are justified.
- The round 7 probe rerun: nothing is worse than the commit apart from error positions, so I agree with your rerun.
- `.par.flatMap` probes (`r7/par/q1`, `q2`, `par/s1`–`s3`) at `PTS_THREADS` 1, 2 and 4 give the same results as the commit.
- Labeled, switch, try/finally and block-scoped consts (`p23`).
- `concat` with kept or non-array arguments (`p7`, `p8`).
- Mappers with several returns or none (`p13`, `p14`).
- `Array.from` without a mapper (`p15`).
- Inherited, intersection, generic and `| undefined` targets (`p3`–`p6`).
- Rest and fresh tuple consts (`q7`, `q9`).
- New PTS1001s where the commit printed the wrong result (`q6`, `q10`, `o12`).
- No PTS9001 or crash in any probe, including computed, getter and spread object literals (`w1`).
- Valgrind on `flatmap_new_arrays`, `optional_tuple_conversions`, `w2`, `q7` and `q9`: no invalid reads or writes, only the collector's usual start-up warnings.

### Commands
- `bin/runnew.sh` reran the 98 round 7 probes from copies in `r7/`, via `xargs -P2`. Summary: `/tmp/h05r8.vXBS/r7.results`.
- `bin/cmp.sh F.pts` runs Node (`--experimental-transform-types` on an `.mts` copy) and then new, round 7, commit and base, each with its `PTS_HOME`; I used it for every new probe.
- Par probes ran with `PTS_THREADS=1/2/4`, against a sequential `.mts` for Node.
- Test checks: the `expect` and `// error:` blocks extracted with sed and compared with `cmp`.
- Memory: `pts build` then `valgrind -q`, in `vg/`.
- I edited nothing in the repo (checked with `git status`). The marker `/tmp/h05-r6/review8.done` is touched.

## Round 9

Agent `opus-reviewer` (claude-opus-5-5, xhigh, read-only), 2026-10-17 08:31–08:48, on the parent's round-8 fixes (uncommitted; snapshot `/tmp/h05-r6/suite3`, CLI `/tmp/h05-r6/cli4/pts`, sha256 `36f29da3…`; diff `/tmp/h05-r6/round8-fix.diff`), against round 8's CLI, commit `c158778d4` and the base. The report below is its final message, verbatim but for its headings, one level down. Its probes (scratch `/tmp/h05r9.73aJ`, without `.mts` copies, built binaries and its copies of round 8's probes): [h05-review9-probes.tar.gz](h05-review9-probes.tar.gz).

**NOT READY.** The round 8 fixes work on round 8's own probes, but the new object-property-order check compares against the wrong order. It wrongly accepts some copies and wrongly rejects others, and some of those rejections break ordinary code that the commit compiled and ran correctly. Separately, the shorthand change stops counting one kind of use.

Scratch: `/tmp/h05r9.73aJ`. Columns: new = cli4/suite3, r8 = cli3/suite2, commit = /tmp/h05-close, base = /tmp/h05-base, Node v24 on a `.mts` copy. Outputs are in `<dir>/out/<name>.{node,new,r8,commit,base}`.

### Findings

**1. High, VERIFIED. The order check uses alphabetical order, but the copy uses a different order. Wrong output where the commit reported PTS1001, and false PTS1001s where round 8 ran correctly.**
- **Cause:** `keepsObjectLiteral` (`pts/lower/arrays.go:871`) looks for the literal's property names as a subsequence of `iface.Fields`. `objectMembers` sorts those fields by name (`pts/lower/objects.go:218`). The runtime copy instead uses `iface.Copy` with `CopyOrder`, which `copyShape` (`pts/lower/dynamic.go:300-324`) takes from `GetPropertiesOfType` of some object type that maps to the interface. That gives declaration order, with an extending interface's own fields before the inherited ones.
- **Wrong output.** Repro `d/d4_class_implements_like.pts`:
  ```ts
  interface P { z: number | string; a: number; }
  const ns = [1, 2];
  const r = [1].flatMap<P>(() => ns.map((m) => ({ a: m, z: m })));
  console.log(JSON.stringify(r));
  ```
  - Node: `[{"a":1,"z":1},{"a":2,"z":2}]`
  - new, r8 and base: `[{"z":1,"a":1},...]`
  - commit: PTS1001
- **Same bug, other forms (all regressions vs the commit):**
  - `a/a1_name_order_approved` and `d/d3_merged_iface`: interfaces declared out of alphabetical order, including a merged interface.
  - `a/a5_extends`: `interface B extends A { b }` with literal `{a, b}`. new gives `b,a`, Node gives `a,b`.
  - `g/g7_arrayof_objlit`: the same through `Array.of`.
- **Interface identity:** in `a/a7_dedup_order2`, the target is A (`x, y`), but the program also has an identical interface B (`y, x`) that was met first. The two share one `ir.Interface`, so the copy uses B's order (`y,x`). Using A's declaration order would therefore also be wrong. The check needs the actual copy order, `iface.Copy.Orders[iface.CopyOrder]`. `flattenedResult` already sets up the copy shape before `keepsLeaves` runs.
- **False positives:** in `a/a2_decl_order_rejected` and r8's `o3_nested_obj`, the literal follows the declaration order, which is also the copy order. Node, r8 and base match; new and the commit give PTS1001. So these are regressions vs round 8 only.

**2. High, VERIFIED, regression vs commit, base and r8. False PTS1001 for object literals that are already made as the interface.**
- Common code now fails to compile whenever the literal's keys aren't in alphabetical order. Repro `d/d12_realistic_users.pts`:
  ```ts
  interface User { name: string; age: number | string; }
  const groups = [[1, 2], [3]];
  const users = groups.flatMap((g) => g.map((n): User => ({ name: "u" + n, age: n })));
  console.log(JSON.stringify(users));
  ```
  - Node, commit, base and r8: `[{"name":"u1","age":1},...]`
  - new: `6:57 PTS1001: using a {age: number; name: string} as a {age: number | string; name: string}`
- The same happens with:
  - `Array.of<P>({ z, a })` (`d/d5`)
  - `(m): P => ({ z, a })` (`d/d6`)
  - `const xs: P[] = [{ z, a }]; return xs;` (`d/d7`)
  - `[{ z, a }] as P[]` (`d/d9`)
  - `ns.map<P>(...)` (`d/d11`)
- **Cause:** these literals reach `keepsObjectLiteral` with `made=false`, although the annotation, type argument or cast makes them as the interface (no copy happens). When the order check fails, the fallback `keepsLeaf(t, …)` checks the literal's inferred type (`age: number`) instead of the type it is made as.

**3. Medium, VERIFIED, regression vs commit and r8. A shorthand default in a destructuring assignment no longer counts as a use.**
- **Cause:** in `({ a = ns } = …)` the default `ns` is a child of the `ShorthandPropertyAssignment`. `valueSymbolAt` (`pts/lower/arrays.go:1478-1480`) therefore returns `a`'s symbol, so that use of `ns` is missed. Fix: only redirect when `name == name.Parent.Name()`.
- **Regular code (`v/v4_regular_default_in_fn.pts`):**
  ```ts
  function f(): number {
    const ns = [1, 2];
    let a: number[] = [];
    ({ a = ns } = {} as { a?: number[] });
    const xs: (number | string)[] = ns;
    xs.push(3);
    return a.length;
  }
  console.log(f());
  ```
  - Node: 3
  - new: 2
  - r8, commit and base: PTS1001
- **flatMap (`v/v2_shorthand_default_flatmap.pts`):**
  - Node: `3 3 true`
  - new and base: `2 3 false`
  - r8 and commit: PTS1001

**4. Low–medium, VERIFIED, pre-existing on commit and base. An `export const` used once in its own file is copied silently.**
- Only the declaring file is searched for uses, so an importer still sees the original array. Repro `m/lib/a.pts` + `m/m2.pts`:
  ```ts
  // lib/a.pts
  export const ns = [1, 2];
  const xs: (number | string)[] = ns;
  export function push(): void { xs.push(3); }
  // m2.pts
  import { ns, push } from "./lib/a.pts";
  push();
  console.log(ns.length);
  ```
  - Node: 3
  - new, commit and base: 2
- `export { ns }` and `export default ns` are now counted correctly (`m/lib/b.pts`, `m/lib/c.pts`).

**5. Test quality, VERIFIED.**
- Every interface in the tests is declared in alphabetical order (`K {x, y}` at errors line 81, `Named {name, size}`, `Item {name, qty}`), so the tests can't catch findings 1 and 2.
- The errors-test header says the copy "has the interface's properties in its order", but the code compares against alphabetical order.
- Suggested additions:
  - Run test: `interface P { z; a }` with `(m): P => ({ z, a })`.
  - Run test: `extends` with a name-order literal.
  - Errors test: `({ a = ns } = …)`.

### Round 8 status
1. **Partly fixed.** Round 8's probes o1, o20–o23, o25 and o26 now give PTS1001, but the check is still wrong (findings 1 and 2).
2. **Fixed.** p2, p17–p20 and q1 give PTS1001, and t1–t3 do too. t7 shows the Set-of-C case is a real problem: Node gives `instanceof` true.
3. **Fixed** for shorthand names and export specifiers: s1–s5 give PTS1001, and o5 and o24 match Node. The new gap is finding 3.
4. **Fixed.** p1, q2, q3 and q4 match Node, as do n1, n5, g3, f4, f6 and c1.

### Checked and fine
- **Errors tests:** `flatmap_kept_elements` produces exactly its 27 listed errors, and each one matches a real Node difference. `flatmap_leaf_conversions` matches its 12.
- **Run tests:** the `expect` blocks of `flatmap_new_arrays` and `optional_tuple_conversions` equal Node's output and the new CLI's.
- **Property order cases that work:** optional fields left out or set to `undefined` (a3, a4), quoted keys (d2), and union and Mixed targets (u1–u3) match Node.
- **constMadeIn:**
  - Returns inside loops and nested functions are handled.
  - Nested flatMap works with block bodies, consts from the outer callback, named, typed and generic inner callbacks (n1–n8).
  - No nil `fn` path and no recursion loop.
- **Array.of:** spreads and kept arguments are handled (g1–g6).
- **`.flat()` on literals:** kept, conditional and nested elements work; holes give the existing "arrays with holes" PTS1001 on every build.
- **Optional numeric tuple elements:** match Node (k1).
- **Exports and re-exports:** no crashes; re-exports aren't miscounted (x1).
- **No PTS9001 or panics anywhere.**
- **Memory:** the runtime is unchanged since round 8. Valgrind on 6 binaries from the newly approved paths shows no invalid reads, writes or frees; only Boehm GC's conservative-marking noise.

### Commands run
- `bin/cmp.sh <probe>.pts`: Node, then the new, r8, commit and base CLIs, each as `PTS_HOME=<tree> <cli> run`.
- `ONLY=new bin/cmp.sh` over all of round 8's probes, copied to `r8/`.
- In `tests/`:
  - `pts run` on both errors tests, with a `diff` of the output against the `// error:` lines.
  - Node and `pts run` on both run tests, compared with each other and with the `expect` blocks.
- `pts build` followed by `valgrind --error-limit=no` in `vg/`.
- 10–12 recompiles of `a6` and `a7` under different file names to check whether the copy order is stable (it was, in every run).
- `git status` in the repo: it is unchanged by me.
- `touch /tmp/h05-r6/review9.done`.

## Round 10

Agent `opus-reviewer` (claude-opus-5-5, xhigh, read-only), 2026-10-17 09:05–09:24, on the parent's round-9 fixes (uncommitted; snapshot `/tmp/h05-r6/suite4`, CLI `/tmp/h05-r6/cli5/pts`, sha256 `4bf355d0…`; diff `/tmp/h05-r6/round9-fix.diff`), against round 9's CLI, commit `c158778d4` and the base. The report below is its final message, verbatim but for its headings, one level down. Its probes (scratch `/tmp/h05r10.8v8w`, without `.mts` copies, built binaries and its copies of earlier rounds' probes): [h05-review10-probes.tar.gz](h05-review10-probes.tar.gz).

**NOT READY.** Round 9's four findings are fixed. But the property-by-property approval now covers literals written in the usual declaration order, and that exposes two silent-wrong-output paths and one new run-time TypeError. The commit reported PTS1001 for all of them. Separately, whether a program compiles can now change from one build to the next.

Scratch: `/tmp/h05r10.8v8w`. Columns: new = cli5/suite4, r9 = cli4/suite3, commit = /tmp/h05-close, base = /tmp/h05-base, Node v24 on a `.mts` copy. Outputs are in `<dir>/out/<name>.{node,new,r9,commit,base}`.

### Findings

**1. High, VERIFIED. A copied object gains its left-out optional properties as `undefined`.** Regression vs commit, which gave PTS1001. Round 9's build had it too (r9 = DIFF), but round 9 didn't report it.
- **Realistic repro:** `cond/q6_returns_optional.pts`
  ```ts
  interface Item { id: number | string; tag?: string }
  const groups = [[1, 2], [3]];
  const items = groups.flatMap<Item>((g) => g.map((n) => { if (n % 2 === 0) { return { id: n, tag: "even" }; } return { id: n }; }));
  for (const it of items) console.log(Object.keys(it).join(), "tag" in it);
  ```
  - Node: `id false` / `id,tag true` / `id false`
  - new and r9: `id,tag true` three times
  - commit: PTS1001
  - base: DIFF
- **Other forms:**
  - An annotated mapper over an interface with optional fields (`cond/q5_annot_optional.pts`). Node prints `{ id: 1, score: 2 }`; new prints `{ id: 1, note: undefined, score: 2 }`.
  - Conditional literals (`adv/p01_opt_middle.pts` r3, `cond/q1`, `cond/q3`).
  - A nested property (`nest/n5_nested_optional_cond.pts`: r9 and commit give PTS1001, new is DIFF).
  - `JSON.stringify` hides the problem; `Object.keys`, `in`, spread and `console.log` show it.
- **Pre-existing on commit and base** in a regular D-094 copy: `cond/q4_cond_direct.pts` (`const s: P[] = r`).
- **Cause (runtime).** `literalType` makes `{ id: n }` as the inferred member `{id; tag?: undefined}`, with `tag` stored as `PTS_VALUE_ABSENT`.
  - `copy_object` (`pts/runtime/src/convert.c:306-327`) reads properties through `source_property` (`:284-301`), which calls `pts_value_of_slot`.
  - `pts_value_of_slot` (`pts/runtime/src/array.c:174-176`) turns a hole (zero word, which is ABSENT) into `PTS_VALUE_UNDEFINED`. So the `value == PTS_VALUE_ABSENT` skip at convert.c:313-318 never fires for a field the source has but left out.
- **Fix:** for `PTS_FIELD_VALUE` fields, `source_property` should return the raw word, as `pts_object_has` (`object.c:376-377`) does.

**2. High, VERIFIED. Record, Map and Set properties pass the per-property check: a kept object is copied silently, and Map/Set throw a TypeError.** Regression vs commit (PTS1001). Round 9's design had the same gap; round 9 only reported PTS1001 because of the alphabetical-order luck: `nest/n10b`, the same literal in name order, prints the wrong result on r9.
- **Silent wrong output:** `nest/n10_record_kept.pts`
  ```ts
  interface Outer { r: Record<string, number | string>; id: number }
  const shared: Record<string, number> = { a: 1 };
  const ns = [1, 2];
  const xs = [1].flatMap<Outer>(() => ns.map((k) => ({ r: shared, id: k })));
  shared.b = 2; xs[0].r.c = "x";
  console.log(JSON.stringify(xs[0].r), JSON.stringify(shared), xs[0].r === (shared as unknown));
  ```
  - Node: `{"a":1,"b":2,"c":"x"} {"a":1,"b":2,"c":"x"} true`
  - new and base: `{"a":1,"c":"x"} {"a":1,"b":2} false`
  - r9 and commit: PTS1001
  - Same with a kept plain object put into a Record property: `nest/n13_object_into_record.pts`.
- **TypeError at run time:** `nest/n9_set_field_realistic.pts`, `groups.flatMap<Node>((g) => g.map((name) => ({ id: name, deps: new Set([name.length]) })))` where `deps: Set<string | number>`.
  - Node: `a:1 b:1 c:1`
  - new and base: `Uncaught TypeError: Expected {deps: Set<number | string>; id: string}, got Object`
  - r9 and commit: PTS1001
  - Same with a Map value: `nest/n6_set_field.pts`, `nest/n3_map_field.pts`.
- **Pre-existing on commit and base** for a direct leaf: `nest/n12_record_leaf_direct.pts` (silent copy) and `nest/n7_map_leaf_direct.pts` (TypeError). The regular conversion `nest/n11` correctly says "converting Record<string, number> to Record<string, number | string> is not supported yet".
- **Cause:** `keepsObjectLiteral` passes each property to `keepsValue`, which falls back to `keepsLeaf` (`pts/lower/arrays.go:1004-1056`). `keepsLeaf` has no `*ir.MapType` handling: `isObject` excludes MapType, and the object case's `switch target` has no MapType arm. So it returns true. At run time `convert_record` copies the record, and Map/Set conversion requires the same descriptor (`convert.c:533-535`).

**3. Medium, VERIFIED. Whether a program compiles changes from build to build.** Regression vs commit, which is always PTS1001. The order nondeterminism itself is pre-existing.
- **Cause:** `copyShape` (`pts/lower/dynamic.go:307`) picks the copy order by ranging over the Go map `l.interfacesByType`. Structurally identical types share one `ir.Interface`, so the order comes from whichever one the map yields first. The new check reads that order.
- **Repro:** `det/det2.pts`
  ```ts
  interface A { x: number | string; y: number }
  const ns = [1, 2];
  const other = { y: 5, x: "s" as number | string };
  const r = [1].flatMap<A>(() => ns.map((m) => ({ x: m, y: m * 10 })));
  console.log(JSON.stringify(r), JSON.stringify(other));
  ```
  The twin type can be any same-typed object literal elsewhere in the program.
- **Results over 12 builds, each copy differing only by a trailing comment:**
  - new: 11 × PTS1001 and 1 × correct output
  - commit: always PTS1001
  - base: wrong order in 7 of 8 builds
  - Round 9's own `r9/a/nd/n1..n10` reproduce it: n3 and n5 give PTS1001, the others run.
- **Pre-existing, broader:** `det/det1.pts`, where `JSON.parse` output is converted to `A` while a twin `B {y, x}` exists, prints `{"y":2,"x":1}` in 1 of 12 builds on both new and base.
- **Fix:** pick the candidate deterministically (for example, prefer the type that was asked for, then the lowest declaration position).

**4. Low, VERIFIED, pre-existing (new, r9, commit and base). A literal assumed "made as the target" is actually its own type when two union members fit it.**
- Repro `mk/m2_made_two_members_array.pts`:
  ```ts
  interface P { a: number | string; b: number; z?: number }
  interface Q { a: number | string; b: number; y?: number }
  const r = [1, 2].flatMap<P | Q>((m) => [{ b: m, a: m }]);
  ```
  - Node: `b,a`
  - every build: `a,b`
- **Cause:** with `made=true`, `keepsValue` returns true for the object literal. But `literalType` falls back to the literal's own type when two members fit, so the runtime copies it into P's shape.

**5. Test quality, VERIFIED.**
- **They pass:**
  - `flatmap_new_arrays` and `optional_tuple_conversions`: the `expect` blocks equal Node's output and the new CLI's.
  - `flatmap_kept_elements`: exactly its 31 listed errors. `flatmap_leaf_conversions`: its 12.
  - All four results were stable over 5 rebuilds of each test with varied trailing comments.
  - The four new error cases (lines 123, 130, 135, 140) are the right errors.
- **They can't catch findings 1 and 2:** the run tests compare copies only through `JSON.stringify`, which omits `undefined` properties, and no test puts a Record, Map or Set in a property.
  - Suggested run test: a mapper whose returns differ by an optional key, checked with `Object.keys` or `in`.
  - Suggested errors test: a kept Record property and a `Set<number>` going where a `Set<number | string>` goes.
- **Header wording:** the errors-test header says the copy order is "declaration order, its own before an extended interface's". For `C extends B extends A` the actual order is `c,a,b` (`adv/p02c`). The check is still consistent with the runtime; only the wording is loose.

### Round 9 status
1. **Fixed.** `d4`, `a1`, `a5`, `a7` and `g7` now give PTS1001 (honest; the copy order differs from the literal's). `a2` and r8's `o3` now match Node. Merged interfaces (`d3`), multiple extends (`adv/p03`), a generic interface used with two type arguments (`p05`) and a nested interface (`p06`) all follow the copy order, with no wrong output.
2. **Fixed.** `d5`, `d6`, `d7`, `d9`, `d11` and `d12` match Node.
3. **Fixed.**
   - `v2` and `v4` give PTS1001, as do nested patterns: `sh/s1` (object pattern), `s2` (array pattern), `s4` (flatMap), `s5` and `s6`.
   - `v5` (top-level) goes from round 9's correct output to PTS1001. That is conservative and the same as the commit.
4. **Fixed.**
   - Exported consts: `m2` gives PTS1001, and `m1` reports all of `a.pts`, `b.pts` and `c.pts`.
   - `ex/e1` (`export { ns as default }`, two declarators in one `export const`) and `ex/e3` (the const used through an exported holder's shorthand) give PTS1001.
   - Side effect: a single-file program with an exported const used once (`ex/e4`) is now PTS1001, where the commit printed the right answer. That follows from the intended conservative rule.

### Checked and fine
- **No regressions on earlier probes:**
  - All 101 of round 7's probes (from round 8's dir) give byte-identical output to round 8's run.
  - Of round 9's 166 probes, only the expected ones changed (listed in "Round 9 status").
- **Copy fallbacks work:**
  - When `Copy` is nil, the fallback gives PTS1001 (`adv/p07`, a nested accessor-only type).
  - A class target gives PTS1001 (`p08`).
- **Union and other targets match Node:**
  - Iterator results (`p09`); unions with null or undefined (`p10`; the misordered version `p10b` gives PTS1001).
  - A literal made as another interface (`p11`); optional-only fits (`p12`); Record and interface-or-Record unions (`p13`, `misc/u1`, `u2`).
  - Nested arrays, tuples and recursive interfaces (`nest/n1`, `n2`, `n4`).
- **No PTS9001 or panics anywhere.**
- **Valgrind:** `q6` and `p06` binaries show only Boehm GC noise.
- **Already accepted, noted only:** `.par.flatMap<J>` drops an extra property (`r8x/par/s3`). Node prints `y:"extra"`; new and commit drop it; base gives PTS1001. This falls under the accepted "regular D-094 copies convert their elements".
- The repo is unchanged by me.

### Commands
- `bin/cmp.sh <probe>.pts`: Node first, then each CLI as `PTS_HOME=<tree> <cli> run`. `ONLY=new` for the round 7, 8 and 9 reruns, run as 2 streams.
- Determinism: 12 builds of `det1` and `det2` (8 for `det2` on commit and base), each a copy with a random trailing comment.
- `emit-c`, `pts build`, then `gdb -batch` on `copy_object` and `push_leaf` to find the cause of finding 1.
- Tests: sed-extracted the `expect` and `// error:` blocks and compared them with `cmp`; rebuilt both new tests 5 times.
- `valgrind --error-limit=no` in `vg/`.
- Last step: `touch /tmp/h05-r6/review10.done`.

## Round 11

Agent `opus-reviewer` (claude-opus-5-5, xhigh, read-only), 2026-10-17 09:35–09:56, on the parent's round-10 fixes (uncommitted; snapshot `/tmp/h05-r6/suite5`, CLI `/tmp/h05-r6/cli6/pts`, sha256 `0c17a14b…`; diff `/tmp/h05-r6/round10-fix.diff`), against round 10's CLI, commit `c158778d4` and the base. The previous session was cut off while fixing its findings; the next recovered the report below, its final message, from the child's transcript, verbatim but for its headings, one level down. Its probes (scratch `/tmp/h05r11.MccN`, without `.mts` copies and its `src/` copies of rounds 3–10's probes, which those rounds' archives hold): [h05-review11-probes.tar.gz](h05-review11-probes.tar.gz).

**NOT READY.** Round 10's four findings are fixed or no longer reachable through flatMap, and the sweep found no new silent wrong output, run-time TypeError or crash compared with the commit. But some programs that the commit, the base and round 10 compiled and ran correctly are now PTS1001. These are object literals with computed or numeric keys (so tsc types them as hash maps) that are already made as the target type, through every recorded workaround. That is not the accepted c13 class.

Scratch: `/tmp/h05r11.MccN`. Outputs are in `<dir>/out/<name>.{node,new,r10,commit,base}`. Builds: new = cli6/suite5, r10 = cli5/suite4, commit = `/tmp/h05-close`, base = `/tmp/h05-base`. Node v24 runs on a `.mts` copy.

### Findings

**1. Medium-low, VERIFIED. A hash-map object literal made as the target is checked by its own type.** Regression vs commit, base and round 10. Every workaround fails: an annotated mapper, an annotated const with an array-literal initializer, `Array.of<T>` and a cast.
- Repro `m/m30_m11_annotated.pts`:
  ```ts
  const groups = [["a", "b"], ["c"]];
  const xs = groups.flatMap<Record<string, number | string>>((g) => g.map((k): Record<string, number | string> => ({ [k]: k.length })));
  console.log(JSON.stringify(xs));
  ```
  - Node, r10, commit and base: `[{"a":1},{"b":1},{"c":1}]`
  - new: `2:114 PTS1001: converting Record<string, number> to Record<string, number | string> is not supported yet`
- The same happens in:
  - `m/m42` (`const recs: Record<string, number | string>[] = [{ [g[0]]: g.length }]; return recs;`)
  - `m/m35` (`Array.of<Record<…>>({…})`)
  - `m/m36` (`[{…}] as Record<…>[]`)
- The only working workaround is annotating the flatMap callback itself (`m/m43`).
- **Cause:**
  - `keepsObjectLiteral` (`pts/lower/arrays.go:847-856`) takes the made type from `literalType`. `literalType` never returns a hash-map context, because `fitsLiteral` requires `isObjectType`.
  - But `objectLiteral` (`pts/lower/objects.go:350-358`) makes the literal as the first hash-map type among `literalContext`, `recordMemberOfContext` and `typeAt`. Here that is the target, `Record<string, number | string>`.
- **Fix:** share one "made type" helper between the two functions. Watch out for `recordType`'s diagnostics being reported twice.
- **Checked:** no false approval in the other direction. A record literal going where an interface goes is reported by the regular conversion on every tree (`m/m40`, `m/m41`).

**2. Low, VERIFIED. Hash-map leaves that the commit copied without visible effect are now PTS1001** ("converting Record<…> to Record<…>"). Regression vs commit, base and round 10. Node, r10, commit and base give the same correct output for all of them.
- `o14_numeric_key` (in round 8's set, `src/h05r8.vXBS/o/`). Your sweep missed it:
  ```ts
  interface N { 0: number | string }
  const ns = [1, 2];
  const r = [1].flatMap<N>(() => ns.map((m) => ({ 0: m })));
  ```
- `m/m11`: a computed-key literal in a mapper.
- `m/m25`: `{ 0: m, 1: m * 2 }` into `Record<number, …>`.
- `m/m12`: a const record used more than once in the callback.
- `m/m13`: a function's result.
- `m/m24`: a `Record<string, any>`, reported as "Record<string, value>".
- **Assessment:**
  - m12, m13 and m24 match the regular rule. `m/m20` is PTS1001 on every tree, so treating them as by design is defensible.
  - o14, m11 and m25 are the hash-map analogue of c13. The commit's rule didn't cover them, so the acceptance text needs widening.
  - Alternative: for a new literal, check only the values with `keepsLeaf(t.Value, member.Value)`.
- Inconsistency: `N | null` instead of `N` compiles and matches Node (`m/m28`), because unions skip the check (finding 4).

**3. Low, VERIFIED. "Annotate the const" doesn't help when the initializer is a mapper.** Regression vs commit, base and round 10, in the c13 class.
- Repro `ac/a1_annot_const_map_iface.pts`:
  ```ts
  interface P { a: number; z: number | string }
  const ns = [1, 2];
  const xs = [1, 2].flatMap<P>((i) => { const a: P[] = ns.map((m) => ({ a: m, z: m + i })); return a; });
  ```
  - Node, r10, commit and base: `[{"a":1,"z":2},{"a":2,"z":3},{"a":1,"z":3},{"a":2,"z":4}]`
  - new: `4:33 PTS1001 … whose z …`
  - `ac/a3` adds `.filter`: same result.
  - With an annotated mapper it compiles (`ac/a2`).
- **Cause:** `constMadeIn` walks into the initializer even though the annotation already converted it at the const.
- **Trade-off:** that walk also catches real identity loss. In `ac/a6`, `{ arr: kept }`: the commit's JSON hides a copy.
- **Suggestion:** at minimum, correct the recorded workaround to "annotate the mapper or the callback".

**4. Medium, VERIFIED, pre-existing (new, commit and base). Round 10's finding 2 is still open for union targets and for objects stored where a hash map goes.**
- **Silent copy** (Node prints `{"a":1,"b":2} true`; every build prints `{"a":1} false`):
  - `m/m01`: `flatMap<Record<string, number | string> | null>(() => [shared])`
  - `m/m17`: a union of two Records.
  - `mk/k15`: `A | Record<…>`.
- **Kept object into a Record** (`m/m18`):
  ```ts
  const shared = { a: 1 };
  const xs = [1].flatMap<Record<string, number>>(() => [shared]);
  shared.a = 5;
  ```
  Node prints `{"a":5} true`; every build prints `{"a":1} false`.
- **TypeError on every build:** `m/m02`, a Map into `Map<…> | undefined`.
- The regular rules report all of these: `toUnion` at `unions.go:212`, and `convert`'s "using a X as a hash-map object" in `lower.go`.
- **Cause:** in `keepsLeaf` (`arrays.go:975-987`), when `memberFor` returns nil for a MapType the code returns true ("left to the runtime's check"). The object case has no `*ir.MapType` target arm.
- **Fix caution:** an object-into-Record check must exempt new literals, because `lt/l13` is correct on every tree. Otherwise, record this as a ledger row.

**5. Nits.**
- The errors-test header still describes the round 8–9 copy-order rules, which no longer exist.
- Pre-existing diagnostic wording:
  - `misc/u1` says "whose `note`…"; the property that differs is `z`.
  - `any` prints as `value` (`m/m24`, `mk/k13`).

### Round 10 status
1. **No longer reachable through flatMap.**
   - `q6`, `q5`, `p01`, `q1`, `q3` and `n5` give PTS1001, as on the commit.
   - Relaxed interface casts keep identity and match Node on every tree (`f1/o1`–`o6`).
   - `q4`, a regular D-094 copy, is still DIFF on every tree. That needs its ledger row. `convert.c` hasn't changed since round 9.
2. **Fixed for direct leaves and properties.**
   - `n3`, `n6`, `n7`, `n9`, `n10`, `n10b`, `n12` and `n13` give PTS1001.
   - These went from DIFF or TypeError to PTS1001: `m04`, `m06`, `m10`, `m14`, `m15`, `m19`, `m22`, `m23` and `k06`.
   - Unions and objects into Records are still open (finding 4).
3. **Fixed.** `det2` gave the same PTS1001 in 8 of 8 builds. Round 9's `nd` probes are all PTS1001.
4. **Pre-existing and unchanged.** `mk/m2` is DIFF on every tree.

Tests:
- `flatmap_kept_elements` gives exactly its 35 errors, in order.
- `flatmap_leaf_conversions` gives its 12.
- In both run tests the `expect` block equals Node's output and the new CLI's.
- `convert_test.c` wasn't run (no `go test`), but the runtime is unchanged since round 9.

### Checked and fine
- **Sweep of rounds 3–10:** 870 unique probes (deduplicated by hash). New output differs from the commit's in 234:
  - 159 are compile errors on both, with only message or position changes.
  - Improvements: 19 errors → OK, 7 TypeErrors → OK, 35 wrong outputs → PTS1001, 3 TypeErrors → PTS1001.
  - New PTS1001 where the commit was OK, 9 files:
    - accepted: `c13`, `t6`
    - exported consts, round 10's accepted side effect: `e4`, `f`, `i`, `a`, `b`, `other`
    - not accepted: `o14` (finding 2)
  - Wrong output where the commit threw: `m1` and `m4`, accepted in round 7.
  - Nowhere is the new build wrong, throwing or crashing where the commit was right or reported an error. No PTS9xxx and no panics.
- **Map identity matches Node on every tree:**
  - the same Map or Set, and ReadonlyMap or ReadonlySet targets (`m03`)
  - an index-signature interface for a Record (`m05`)
  - value types that are structurally equal or the same interface in another order (`m07`, `m08`)
  - readonly array values (`m09`)
  - enum keys and values, and literal keys (`k01`–`k05`, `k07`)
  - class values (`k08`)
  - same-typed Map or Record properties (`k09`, `k10`)
  - union members (`m16`, `k11`)
  - new arrays of maps (`k12`)
- **The `literalType` path matches the commit and Node:**
  - unions with null or undefined (`l02`, `l03`, `l18`)
  - iterator results, including conditional, reordered and extra-property forms (`l04`, `l05`, `l21`)
  - nested literals (`l06`, `l07`) and discriminated unions (`l08`, `l09`)
  - casts, `Array.of` and `concat` (`l10`–`l12`)
  - getters, spreads, methods and generics (`l14`–`l16`, `l20`)
  - `Object.keys` on a made literal (`l22`)
- No duplicate diagnostics from `irType` in the check (`dup/d1`). gofmt is clean and every helper added in the diff is still used. The repo is unchanged.

### Commands
- `bin/one.sh <probe> [variants]`: Node on a `.mts` copy, then `PTS_HOME=<tree> <cli> run` for each build.
- Sweep: `xargs -P 2` over `reps.txt`; `bin/diffs.sh` lists probes where new and commit outputs differ.
- Tests: `sed` extraction of the `// error:` and `expect` blocks, compared with `cmp`.
- Determinism: 8 copies of `det2` with random trailing comments.
- `gofmt -l`. Last step: `touch /tmp/h05-r6/review11.done`.

## Round 12

Agent `opus-reviewer` (claude-opus-5-5, xhigh, read-only), 2026-10-17 10:32–10:55, a time-boxed confirmation round on the parent's round-11 fixes (uncommitted; snapshot `/tmp/h05-r6/suite6`, CLI `/tmp/h05-r6/cli7/pts`, sha256 `fb381cf3…`; delta `/tmp/h05-r6/round11-delta.diff`), against round 11's CLI, commit `c158778d4` and the base. Its brief defined a BLOCKING finding as wrong output, a run-time error, a crash, a PTS9001 or a panic where the commit gave Node's output or a compile error, or any memory-safety problem; everything else is recorded, not fixed (CLAUDE.md's rabbit-hole rule, D-181). The harness blocked its `report.md`, so the report below is its final message, verbatim but for its headings, one level down. Its probes (scratch `/tmp/h05r12.eD0Y`, without `.mts` copies, built binaries, its `emit/` comparison outputs and its copy of round 11's probes): [h05-review12-probes.tar.gz](h05-review12-probes.tar.gz).

READY. I found no BLOCKING finding.

Against the commit `c158778d4`, the new build (cli7/suite6) never gives wrong output, a TypeError, a crash, a PTS9xxx or a panic where the commit gave Node's output or a compile error. That holds over 962 earlier probes and about 60 new probes; valgrind found no memory errors. Round 11's findings 1–5 are fixed. Two outcome changes vs r11 matter; both are NON-BLOCKING because the commit and the base behave the same:
- **Annotated consts.** The annotated-const skip (F3) brings back silent identity loss that r11 reported.
- **Hash maps, maps and arrays where an interface or a numeric Record goes.** These leaves are still not reported by flatMap's check (a pre-existing hole).

The harness wouldn't let me save `report.md` (subagents can't write report files), so this message is the report.

Scratch is `/tmp/h05r12.eD0Y`. Outputs are in `<dir>/out/<name>.{node,n12,r11,commit,base}`; in round 11's copied probes, `.new` is the r11 output. Builds:
- **n12 (new):** `/tmp/h05-r6/cli7/pts` with home `/tmp/h05-r6/suite6`
- **r11:** cli6/suite5
- **commit:** `/tmp/h05-close`
- **base:** `/tmp/h05-base`
- **Node:** v24 on a `.mts` copy

### Findings

#### N1. NON-BLOCKING, medium-low, VERIFIED: an annotated const's initializer isn't walked, so kept values lose identity silently
The commit and the base do the same; r11 reported these. The cause is `pts/lower/arrays.go:635` (`initializer.Parent.Type() == nil`).

**`p/a/a01_annot_const_kept_identity.pts`** (round 11's ac/a6 plus a mutation):
```ts
interface H { arr: (number | string)[] }
const kept = [1];
const xs = [1, 2].flatMap<H>(() => { const a: H[] = [0].map(() => ({ arr: kept })); return a; });
kept.push(2);
console.log(JSON.stringify(xs));
```
- Node: `[{"arr":[1,2]},{"arr":[1,2]}]`
- new, commit, base: `[{"arr":[1]},{"arr":[1]}]`
- r11: `6:33 PTS1001 using a {arr: number[]} as a {arr: (number | string)[]}…`
- This is the regular C80 copy: the same const outside flatMap (`a02`) is wrong on every build.

**`p/a/a06_any_annot_const.pts`**: `const a: any[] = [kept]; return a;` into `flatMap<(number | string)[]>`.
- Node: `[[1,2],[1,2]]`
- new, commit, base: `[[1],[1]]`
- r11: `3:21 PTS1001`
- Here the copy isn't made at the declaration. `any[]` boxes `kept` as it is, and flatMap's own dynamic conversion copies it (D-071).
- So the comment, and D-181's "an annotated one's elements are converted to its type where it is declared", are false for `any[]` and `unknown[]` annotations.
- A cheap fix: keep walking when the annotation's elements are dynamic (`isDynamicArray`).

**`p/a/a08_annot_const_union_elem.pts`**: `const a: P[] = ns.map((m) => (m > 1 ? { a: m, z: m } : shared))`.
- Node: `[{"a":1,"z":7},…]`
- new, commit, base: `[{"a":1,"z":1},…]`
- r11: `8:41 PTS1001`
- This is C80 again.

#### N2. NON-BLOCKING, medium, VERIFIED, pre-existing on every build: a hash-map, Map or array leaf where an interface or numeric Record goes isn't reported
`keepsLeaf` (`arrays.go:1033-1091`) has no arm for a `*ir.MapType` leaf with an Interface or Class target, and the array arm at `arrays.go:1059` approves any target that isn't an array. The regular conversion reports these cases; F4 only covered the union form, so the two are now inconsistent.

**`p/u/u11_kept_hashmap_into_iface.pts`**:
```ts
type HV = { [k: string]: number; v: number };
const h: HV = { v: 1, a: 2 };
const xs = [1].flatMap<{ v: number }>(() => [h]);
h.v = 5;
console.log(JSON.stringify(xs));
```
- Node: `[{"v":5,"a":2}]`
- new, r11, commit, base: `[{"v":1}]`
- The regular `const o: { v: number } = h` (`u13`) is PTS1001 on every build.

**`p/u/u07b_plain_only.pts`**: a computed-key literal through a mapper into `flatMap<{ v: number }>`.
- Node: `[{"v":1,"a":1},…]`
- every build: `[{"v":1},…]`
- Its union form (`u07a`, also round 11's `dup/d2`) is now correctly PTS1001 on new.

**`p/u/u14_map_into_iface.pts`**: a Map into `flatMap<{ size: number }>`.
- Node: `2`
- every build: `TypeError: Expected {size: number}, got Map`
- The regular conversion (`u15`) gives PTS1001.

**`p/u/u16_array_into_numeric_record.pts`**: a kept `number[]` into `flatMap<Record<number, number>>`.
- Node: `[[1,2,3]]`
- every build: `TypeError: Expected Record<string, number>, got Array`

The fix is a "converting X to Y" arm for these leaves (and an array-into-hash-map arm), or a ledger row. D-181's "Hash maps, maps and sets" bullet over-claims here, in both the draft and the repo version.

#### N3. NON-BLOCKING, low, VERIFIED: new false PTS1001s from F4's object-into-hash-map arm
These compiled correctly on r11, the commit and the base.

**`p/u/u04b_alias_order_into_record.pts`**, with `type I = { b: number; a: number }`, into `flatMap<Record<string, number | string>>`:
- **Line 10:** `({ a: m, b: m * 2 }) as I` in a mapper.
- **Line 12:** `const v: I = {…}; return v` in a mapper.
- **new:** `PTS1001: using a {a: number; b: number} as a hash-map object (Record<string, number | string>)` on both lines.
- **Node, r11, commit, base:** `[{"a":1,"b":2},{"a":2,"b":4}] a,b`.

**`p/d/d05_spread_literal_record.pts`**: `({ ...base, a: m })` into `Record<string, number>`.
- new: PTS1001
- everyone else: `[{"z":0,"a":1},{"z":0,"a":2}]`

Annotating the mapper works around both (`u04c`, `u12`); annotating the const doesn't. Round 11's m12, m13 and m24 stay PTS1001 by design, but m13 (a function's result) and m24 (`Record<string, any>`) have no workaround. So the Reason's "have a workaround (annotate the mapper or the callback)" isn't always true.

Message nit: the regular message's hint "; a copy of its properties, `{ ...o }`, is one" is dropped in keepsLeaf's version (`dd3`).

#### N4. NON-BLOCKING, low, VERIFIED, pre-existing: optional-tuple leaves inside a conditional element
**`p/v/v07`**: `[1, 2, 3].flatMap<[number, string?]>((i) => [i % 2 ? [i] : [i, "s"]])`
- Node: `[[1],[2,"s"],[3]]`
- new, r11, commit: `PTS1001 using a [number] as a [number, string | undefined]` at 1:65 and 1:71
- base: TypeError
- The plain form (`v08`) and the regular conditional (`v06`) work. This belongs in D-181's known gaps.

#### N5. NON-BLOCKING, low: test gaps
None of these new branches has a test, so deleting any of them leaves the suite green:
- **`onlyRecordMember` approval:** an interface-typed literal into `Record<…> | null` or `| string`. In u01, u06, r05, v03, u09 and u10 the commit gives PTS1001 and new gives Node's output.
- **Per-property rejection of a kept value:** `p/d/d06` line 8, `({ kept })` into `Record<string, (number | string)[]>`.
- **The accessor fallback:** `p/d/d04`. r11, commit and base silently print `[{},{}] 0`, Node prints `[{"a":1},{"a":2}] 2`, new gives PTS1001.
- **The `hasOptionalFields` guard:** `p/u/u18a`. r11, commit and base silently print `a,b a,b`, Node prints `a a,b`, new gives PTS1001.

I suggest adding u01 and u06 lines to `flatmap_record_leaves.pts`, and d04, d06 and u18a lines to `flatmap_kept_elements.pts`.

#### N6. NON-BLOCKING: D-181 claims
The draft `/tmp/h05-r6/d181.md` doesn't describe round 11's changes:
- `recordContext` (F1)
- the value-by-value check of new literals going where a hash map goes, with its fallbacks (F2)
- the annotated-const exception (F3)
- the union and object-into-hash-map arms (F4)

Its "checked by that type, as on the commit" is also wrong for objects into hash maps: the commit approved those silently.

The parent edited D-181 in `docs/DECISIONS.md` during this review, and that version covers F1–F4. These are still wrong or missing there:
- the hash-map bullet's over-claim (N2)
- the `any[]` case in the annotated-const rationale (N1)
- known gaps for:
  - N1 (a01 and a08 under C80, a06 under D-071)
  - N2
  - N3 (no workaround for m13 and m24)
  - N4

#### N7. Nits
- `arrays.go:1031` is a 116-character comment line that wasn't rewrapped.
- The `keepsLeavesOf` doc comment doesn't mention the annotated-const exception.
- The `keepsObjectLiteral` doc says "of its own type", but the branch also covers a literal made as an annotated mapper's type.
- `any` still prints as `value` (pre-existing).
- `{ __proto__: m }` keeps a `__proto__` key on every build (`v04`); the regular literal does too, so it's outside H05.

### Round 11 status
1. **F1 fixed.** m30, m33, m35, m36 and m42 are OK on new (CE on r11).
2. **F2 fixed.** o14 (both copies), m29, m11 and m25 are OK. m12, m13 and m24 stay PTS1001 by design.
3. **F3 fixed.** a1 and a3 are OK. a6 is now silently wrong, as on the commit (N1).
4. **F4 fixed for unions and objects into Records.**
   - m01, m17, m18 and k15 went from wrong output to PTS1001, and m02 from TypeError to PTS1001.
   - k03 (wrong output) and k08 (TypeError) are now correctly PTS1001 too.
   - Interface targets are still open (N2).
5. **F5 fixed.** The errors-test header is accurate.

### Checked and fine
- **Sweep, 962 probes** (round 11's 870 representatives plus its 92 own):
  - No regression vs the commit, and no PTS9xxx or panic.
  - 22 probes changed outcome vs r11; all are explained above:
    - Fixed or improved: 12 compile errors became correct output, 4 wrong outputs and 1 TypeError became PTS1001.
    - dup/d2 is a correct new report.
    - Others are message or position changes only.
- **Tests:**
  - Each of the three new run tests' `// expect:` blocks equals Node's output and new's.
  - The errors tests' `// error:` lines equal new's output exactly and in order: 40/40 and 12/12.
  - The five new kept_elements errors are real identity losses or run-time TypeErrors.
  - The `flatmap_record_leaves` header claim holds: the commit and base print its expect block, r11 doesn't compile.
- **`objectLiteral` refactor:** `emit-c` from r11 and new is byte-identical (C and diagnostics) for 535 of the 537 run and errors tests; only the two flatMap tests differ. `o01` (HeadersInit-style unions, computed and enum keys, numeric-key interfaces, Partial<Record>, nested records) is OK on every build.
- **Record branch and unions:**
  - Correct output, identity checked by mutation, in r01–r06, u01, u02, u05b, u06, u09, u10, v01–v03, v05, v09, k01, k02 and k04–k07.
  - No double diagnostics (dd1–dd5, d01).
- **`.par.flatMap`:** at PTS_THREADS=1, 2 and 4 it equals Node's sequential run: `par2` three times each, and the GC-heavy `par3` with 400,000 `Record | null` leaves.
- **Memory:**
  - `pts build` plus `valgrind -q --undef-value-errors=no --error-exitcode=99` exits 0 with no reports on 11 binaries.
  - The full runs' undefined-value reports are all inside bdwgc's conservative scan.
  - The runtime's `convert_test.c` passes and is valgrind-clean.
- **Determinism and format:** the 8 determinism copies give identical output, and `gofmt -l pts/lower` is clean.
- **Repo:** I didn't edit the repo or `/tmp/h05-r6/suite6`; the code files still equal suite6's copies. The parent changed PROGRESS.md, DECISIONS.md, SPEC.md, h05.md and CONSOLIDATED.md, and added h05-review11-probes.tar.gz, during the review; none of that is mine.

### Commands
- `bin/one.sh <probe> node,n12,r11,commit,base` runs Node on a `.mts` copy, then `PTS_HOME=<home> <cli> run` for each build.
- The sweep ran `xargs -P 1` over `all.txt`; `bin/cmp.sh` classifies each probe and flags regressions and changes vs r11.
- Test checks: `awk` extraction of the expect blocks and `// error:` lines, compared with `cmp`.
- `pts emit-c` with r11 and new over copies of `pts/tests/{run,errors}`.
- `pts build`, then `PTS_THREADS=1|2|4` runs and valgrind.
- A `clang` build of `convert_test.c` against the cached runtime archives.
- `gofmt -l` and `git status`. Last step: `touch /tmp/h05-r6/review12.done` (done).

Files are in `/tmp/h05r12.eD0Y`:
- `cmp.txt`: sweep classification
- `sweep1.txt`, `sweep2.txt`: raw sweep logs
- `p/{u,r,k,a,d,dd,v,o,par}/`: my probes
- `tests/`: test-file checks
- `emit/all/`: the emit-c comparison
- `vg/`, `ct/`: valgrind runs and the convert test
