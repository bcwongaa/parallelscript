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
