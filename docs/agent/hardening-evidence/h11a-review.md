# H11a review report

Child: `reviewer` (claude-opus-5-5, xhigh), read-only, 2026-10-24. Each round's report is its final message, saved here by the parent unchanged.

## Round 1 (snapshot 2: `/tmp/h11/snap2`, HEAD `476ad4cf8` plus the change; [archive](h11a-review1-probes.tar.gz))

**Verdict: NOT READY.** Besides the three causes you already have, I found 3 High, 3 Medium, 4 Low and 2 Note findings.

## How I tested
- **Your three known causes:** I reproduced all three independently and won't re-report them. These programs add to them and are worth keeping as regression tests for your fixes:
  - `repro/r11_unknown_return.pts`: `function get(o: Record<string, any>, k: string) { return o[k]; }`
  - `b11_generic_first.pts`: `first<unknown>` with an inferred result
  - `b08`/`b09`: `const o = { v: xs[3] }` with `xs: unknown[]`, and the same for `Record<string, any>`
  - `g02_inferred_any_results.pts`
  - `nar/j13_forOf.pts`: a cause-3 variant inside `for…of`
- **Commands:**
  - `tools/run.sh F` builds and runs F with `PTS_HOME=<tree> <tree>/pts-bin/pts build F -o bin; ./bin` for the snapshot and the base, then deletes the binary.
  - For Node it runs `node --no-warnings --experimental-transform-types` on an `.mts` copy.
  - Outputs are in `/tmp/h11/review1/out/<name>.{node,snap,base}`.
- **Repros:** all in `/tmp/h11/review1/repro/`.

## High (regressions: the base compiled these and printed Node's output)

**R1-F1: a loose local narrowed past `null`, by `typeof` or by `instanceof` loses tsc's narrowing where it goes where undefined goes, and the program no longer compiles (PTS1001).**
- `r1_narrowed_return.pts`:
  ```ts
  const cache: Record<string, Rec | null> = { a: { n: 1 }, b: null };
  function find(key: string): Rec | undefined {
    const hit = cache[key];
    if (hit === undefined) return undefined;
    if (hit !== null) return hit;
    return undefined;
  }
  ```
  - Node and base print `{ n: 1 } undefined undefined`.
  - Snapshot: `8:28 PTS1001: converting null | undefined | {n: number} to undefined | {n: number}`.
- `r1c_forof_typeof.pts` (`for (const [k, v] of rows) if (typeof v === "number") return v;` into `number | undefined`):
  - Node and base print `1`.
  - Snapshot: `PTS1001 converting number | string | undefined to number | undefined`.
- The same failure, base correct each time: `nar/j01` (`const s: string | undefined = v` after `v !== null`), `j02` (`{ s: v }` after `typeof`), `j07` (after `instanceof A`), `j10`.
- **Cause:** `pts/lower/unions.go:181` returns `x`, the whole union the local holds. `objects.go:848` does the same for shorthands.
- **Fix:** return `&ir.FromUnion{X: x, To: l.withUndefined(narrow)}`. cgen treats a smaller union as the same `pts_value`. Your fixes 1–3 don't touch this.

**R1-F2: library optional parameters now receive the union, and the builtins' lowerings can't take it. This is PTS1001 even when the value is present.**
- `r2_tofixed_present.pts`:
  ```ts
  const [value, digits, radix] = "3.14159 2 16".split(" ").map(Number);
  console.log(value.toFixed(digits), (255).toString(radix), parseInt("ff", radix));
  ```
  - Node and base print `3.14 ff 255`.
  - Snapshot: `PTS1001: converting number | undefined to number is not supported yet`.
  - `toPrecision` and `toExponential` fail the same way (`bi/`, `bi2/`).
- `r2b_new_set_present.pts` (`const [left, right] = rows; new Set(left)`):
  - Node and base print `… 2 1`.
  - Snapshot: `PTS1001: a Set<string> of a string[] | undefined is not supported yet`.
  - `new Map(x)` fails the same way. `el/k11` shows both from `for…of` and parameter patterns.
- **Cause:**
  - `numbers.go:150` does `l.convert(arg, ir.Number, …)` and `maps.go:183` is `newMapOf`.
  - D-090 deliberately excluded library optional parameters for exactly this reason: `loose_locals.go:77`, "`n.toFixed(d)`: a builtin's optional parameter".
- **Your fix 2 won't catch this:** these parameters are unions with undefined.
- **Suggested rule:** for a library callee, count an argument as a place only when the parameter's declared type itself holds undefined (push's `T` = `string | undefined`, `Map.set`, `Set.add`), not for `?` optional parameters.
- **Related, already on the base:** `(1.2345).toFixed(ds[0])` is PTS1001 there too (`m/m01`).

**R1-F3: `placeType` calls the reporting `irType` on arbitrary contextual types, so plain element reads passed to builtins become PTS1001.**
- `r3_array_from_concat.pts` (`Array.from(rows[0])`, `rows[0].concat(rows[1])`):
  - Node and base print `[ 'a', 'b' ] [ 'a', 'b', 'c' ]`.
  - Snapshot: `PTS1001 … (ArrayLike<string>) are not supported yet` and the same for `ConcatArray<string>`.
- Also `el/k05` (`Object.fromEntries(tables[0])`: "of a value is not supported yet") and `k06`/`k07` (loose `for…of` and parameter names).
- **Cause:** `chains.go:229`, `l.irType(contextual, node)`, inside a predicate.
- Fix 2 may cover these if it rejects non-union parameters before calling `irType`. Either way `placeType` should use a quiet conversion (`irTypeOrNone`).

## Medium

**R1-F4: explicit undefined is now treated as a left-out argument, so these give wrong output where the base threw TypeError.**
- `r5_splice_lastindexof.pts`, with `count` missing:
  ```ts
  xs.splice(start, count);
  [1, 2, 1].lastIndexOf(1, count);
  ```
  - Node: `[ 1, 2, 3 ] 0`.
  - Snapshot: `[ 1 ] 2`.
  - Base: `TypeError: Expected number, got undefined`.
- The underlying bug is pre-existing for declared `number | undefined` values (`el/k12`: the base is wrong too); this change reaches it from loose locals. The F2 rule would avoid it.

**R1-F5: a nested conditional now fails to compile.**
- `r4_nested_conditional.pts`: `const v: string | undefined = c ? (c ? column : "a") : "b";`
  - Node: `undefined`.
  - Base: compiles, then `TypeError`.
  - Snapshot: `3:31 PTS1001: conditional expression with operands of types string | undefined and string is not supported yet`.
- **Cause:** `takesOperandsUndefined` (`lower.go:3709`) only recognises operands that are loose identifiers or element reads. The inner conditional is already the union.
- **Fix:** convert both operands to `placeType` whenever their types differ and the node goes where undefined goes.

**R1-F6: the ledger marks C297 fixed, but its own cited form still skips the default.**
- `r6_c297_literal.pts` (`[a = 1] = [undefined]`, and `[e = 4] = t` with `t: [number | undefined, string]`):
  - Node: `1 4`.
  - Snapshot and base: `undefined undefined`.
- `f01s/f01b` (`const [b = 2] = [undefined]`) and `f01c` are PTS1001 on both.
- **Cause:** the tuple branch in `destructuring.go:524-526` ("a tuple's elements are never undefined") and `namesOfTuple`.
- Your test avoids this with `as (number | undefined)[]`. Either fix the tuple case or reword C297 and the evidence as "array sources only".

## Low
- **R1-F7: optional class field initializers are still checked.** `r7_class_field.pts`, `col?: string = column`: Node `undefined`, snapshot and base `TypeError`. `placeType` uses tsc's contextual type there, which is the annotation `string` without undefined. `other: string | undefined = column` works (`cf2`). This is within C11's "optional properties".
- **R1-F8: in generic functions, the first instance lowered decides whether a pattern name is loose** (the cache in `isLoose`). `r8_generic_order.pts`: the tuple instance comes first, so the later `string[]` instance throws `RangeError`. Node prints `2 undefined`. The base does the same; D-220 claims every array pattern's names.
- **R1-F9: D-220 overstates where the hint stops.** It says the `| undefined` hint "no longer reaches a `(T | undefined)[]` pattern", but assignment patterns still throw it. `r9_assignment_hint.pts`, `[p, q] = [1] as (number | undefined)[]`: Node `1 undefined`, snapshot and base `RangeError … give the element type | undefined`. Pre-existing; narrow the wording.
- **R1-F10: the tests miss every regression class above.** None covers results or literal properties of type any/unknown without a contextual type, builtin arguments, narrowed loose locals, or nested conditionals. The guard `array_patterns_typed_sources.pts` only compares output, so it would still pass if tuple names became loose. `isTypedElementRead` (`destructuring.go:318`) is dead in the snapshot.

## Notes
- **R1-F11: gaps D-220 doesn't list.** These are still checked where Node gives undefined:
  - `yield v` into `Generator<string | undefined>` (`r10_yield.pts`: snapshot `TypeError`, Node `[ 'x', undefined ]`)
  - `as`, `satisfies` and `!` around the read
  - `Record` and index-signature places
  - a contextually typed arrow whose inferred result is narrower (`b04`)
- **R1-F12: performance.** D-220's costs reproduce exactly: 187.3M→244.3M, 117.8M→234.8M, 222.0M→258.0M, 700.3M→244.3M. Other cases I measured (`perf/callgrind.txt`):

| Program | Base | Snapshot | Ratio |
|---|---|---|---|
| sort comparator `([a], [b]) => a - b` | 374.7M | 472.8M | 1.26× |
| map callback `([x, y]) => x * y` | 168.9M | 197.8M | 1.17× |
| reduce `(acc, [x, y]) =>` | 268.4M | 355.4M | 1.32× |
| `for…of` over object rows | 205.6M | 265.6M | 1.29× |
| defaults `[x = 0, y = 0]` | 214.3M | 247.3M | 1.15× |

  The defaults slowdown is in code that didn't become loose, and D-220 doesn't mention it.

## Pre-existing (the base has them too)
- the splice/lastIndexOf semantics for declared unions (`k12`)
- `toFixed(ds[0])` is PTS1001 (`m01`)
- loose `f32`/`i32`/`u8` locals in template literals throw `TypeError` (`probes/sz/`)
- a shorthand narrowed past null is PTS1001 (`r1b`)
- `satisfies` object literals are PTS1001 (`c01`, `c03`)
- getters in a literal for an `Agg` (`c04_05`)
- destructuring a caught exception is PTS1001 (`e01_04`)
- defaults over tuples (F6), and defaults over an intersection-narrowed array (`j13_withDefault`)

## Checked and fine
- **Matrix:** the reproducer's families A, B, C, F and G on the snapshot (1,420 cases) go from 361 to 1,105 OK. No case that was OK got worse, and there is no PTS9001 or crash. Only two by-design rows changed (`C1173`/`C1193`, rests of `string[]` and object arrays).
- **Filtered smoke test:** `TestCompilerFailsCleanly` over the new tests with 12 mutants each passes.
- **Defaults:** evaluation order, side effects, defaults that throw, assignment targets, and `null` versus undefined all match Node (`e03b`).
- **Iterable patterns:** close-on-throw and names past the end match Node (`e05`).
- **Parallel code:** `.par` and `parallel for` with loose names and captures work (`par/p01`).
- **Element kinds:** booleans, objects, `any`, unions, `null`, readonly, frozen, match arrays, holes and rest copies all work (`i01`). `i64` stays exact (`sz/s_i64_sum`).
- **Other places:** async returns, generator returns and async generators, the optional-field, array-slot, record-slot and conditional-branch places (`h01`, `d01`), and `for (let [i, n] = …)` (`m04`).
- **Generated C:** the per-kind reads in cgen match `pts_value_of_slot`'s representations, and the inline number read has the same race and trust model as `PTS_ARRAY_GET`.

The probes are archived at `/tmp/h11/h11a-review1-probes.tar.gz`, without binaries; `/tmp/h11/review1.done` is written.

## Round 2 (snapshot 4 = `898a0a52b`; [archive](h11a-review2-probes.tar.gz))

# H11a review round 2 (snapshot 4 = `898a0a52b`)

**Verdict: NOT READY.** 2 High, 2 Medium, 1 Low, plus doc corrections and notes. The worst is a new memory-safety hole: a loose local whose narrowing is stale now reaches a typed place unchecked and crashes with SIGSEGV, where the base throws a clean TypeError.

## How I tested
- **Snapshot check:** `/tmp/h11/snap2` matches `898a0a52b` byte for byte (`pts/lower`, `pts/cgen`, `ir.go`, `pts.h`).
- **Commands:** `tools/run.sh F` builds and runs F with `PTS_HOME=<tree> <tree>/pts-bin/pts build F -o B && ./B` for the snapshot and the base, and runs `node --no-warnings --experimental-transform-types` on an `.mts` copy. Outputs are in `/tmp/h11/review2/out/`.
- **Repros:** all in `/tmp/h11/review2/repro/`.

## Round-1 findings: rerun on snapshot 4
- **All 14 round-1 repros now print Node's output** (r1–r4, r6–r11), except `r5`. `r5` (splice/lastIndexOf with a missing `count`) is back to the base's `TypeError: Expected number, got undefined`, as you intended.
- **The 8 new tests** pass on the snapshot. Seven fail on the base for the stated reasons; `element_reads_any_places` also passes on the base, which is right because it guards the round-1 snapshot's crash.
- **Matrix** (families A, B, C, F, G; 1,420 cases on snapshot 4): 361 → 1,106 OK (F: 161 → 168). No case is worse than HEAD or than the round-1 snapshot, and there is no PTS1001, PTS9001 or crash.

## The STILL class (spot-checked)
Nearly all are by design or pre-existing:
- D-090's TypeError where a T is required.
- A builtin's own optional parameter (`slice`, `padStart` and the like, now D-220's known gap).
- Literals made as their own type.
- Checked casts: `f01e`/`f01f`, `undefined as unknown as number`.
- `satisfies` literals, which are PTS1001 on the base too.

Two are not:
- **`j05` and `pa01`–`pa03` belong to the same class as R1-F1**, and D-220 claims that case: "an argument (of the program's functions)" is narrowed-or-undefined. See R2-F2.
- **`m05` (`String(c ? xs[9] : 1)`):** the round-1 snapshot fixed it, and it is a `RangeError` again on snapshot 4. The base also throws, and D-220 no longer claims builtin `any` parameters, so it is not a regression.

## High

**R2-F1: `looseValue` drops D-140's check, so a loose local whose narrowing is stale (assigned in a closure) reaches typed places unchecked and crashes (SIGSEGV).**

`repro/s01_stale_narrowing.pts`:
```ts
const items: (string | number)[] = ["a", 1];
let v = items[0];
function setNumber(): void { v = 5; }
if (v !== undefined && typeof v === "string") {
  setNumber();
  const s: string | undefined = v;
  console.log(typeof s, s === undefined ? "u" : s.length);
}
```
- Node: `number undefined`.
- Base: `Uncaught TypeError: Expected string, got number` (exit 1).
- Snapshot 4: exit 139, segmentation fault.
- The emitted C (`tmp/s01.c`) shows the unchecked copy, then the read of a number as a string:
  ```c
  pts_value s = pts_global_v;
  ... ((pts_string *)pts_value_as_object(s))->utf16_len
  ```

The same crash in other shapes, each a clean TypeError on the base:

| Repro | Shape |
|---|---|
| `n/s05_stale_null.pts` | narrowed past `null` |
| `repro/s06_stale_return.pts` | returned into `string \| undefined` |
| `repro/s07_stale_pattern_name.pts` | a newly loose `for…of` pattern name |
| `repro/s08_stale_via_property.pts` | `{ label: v }`, then `const lab = o.label` |
| `n/s09_stale_via_array.pts` | `out.push(v)` into a `(string \| undefined)[]` |

- **Cause:** in `pts/lower/unions.go:184-185`, `narrowed` returns `l.looseValue(x, narrow)`, an unchecked `FromUnion`, before the `mayHaveChanged` branch that would check it. `looseValue` (`loose_locals.go`) and `shorthandValue` (`objects.go`) have the same hole.
- The round-1 snapshot had it too (`return x`); I missed it in round 1.
- **Fix:** when `l.mayHaveChanged(node)`, convert with a check to `withUndefined(narrow)`, i.e. `FromUnion{X: box(x), To: withUndefined(narrow)}`, which is checked. Add `s01`, `s06` and `s08` as tests.

**R2-F2: a narrowed loose local passed to the program's own function still goes as its whole union, so newly loose pattern names fail to compile (PTS1001).**

`repro/pa04_forof_program_arg.pts`:
```ts
const rows: (string | number)[][] = [["k", "a"], ["k", 1], ["k"]];
function opt(s: string | undefined): string { return "opt:" + s; }
for (const [x, y] of rows) {
  if (typeof y === "number") console.log("num", y);
  else console.log(opt(y));
}
```
- Node: `opt:a num 1 opt:undefined`.
- Base: compiles and prints `opt:a num 1`, then RangeError past the end.
- Snapshot 4: `7:24 PTS1001: converting number | string | undefined to string | undefined`.

`repro/pa05_param_pattern_program_arg.pts` (`label(v)` after `v === null ? … :`, with `label(s?: string)`):
- Node: `v null none`.
- Base: compiles, then RangeError at run time.
- Snapshot 4: `PTS1001 converting null | string to string`.

Declaration-loose locals (`pa01`, `pa02` line 10, round 1's `j05`) are PTS1001 on the base too; the change spreads the failure to every parameter and `for…of` pattern name.
- **Cause:** in `narrowed`, `isTestedOperand(node)` returns `x`, the whole union, for a call argument to a program function before `looseValue` is reached.
- **Fix:** for call arguments use `looseValue` (with R2-F1's check), and keep the whole union only for real test operands (`===`, `typeof`, truthiness).
- h11a.md says R1-F1 is fixed, which is true only for non-call places.

## Medium

**R2-F3: an object literal for `Partial<Record<"a" | "b", V>>` with a loose value is PTS1001 even when the value is present.**
- `repro/pr05_partial_record_present.pts`: `const [, column] = "sum:units".split(":"); const r: Partial<Record<"column" | "x", string>> = { column };`
  - Node and base: `{ column: 'units' }`.
  - Snapshot 4: `2:54 PTS1001: converting string | undefined to string is not supported yet`.
- The `column: column` form fails the same way (`pr07`); with a missing value (`pr01`) the base throws a TypeError at run time.
- **Cause:** `literalFieldType` reports the optional field's type `string | undefined`, but the literal is made with a `string` value slot. An element read into this literal is PTS1001 on the base too (`pr06`), so the underlying limit is pre-existing.
- **Fix:** take the place type from the IR type the literal is actually made as, or don't treat the property as a place when that slot can't hold undefined.
- `Partial<Record<string, V>>`, `Record<"a" | "b", V | undefined>` and `Partial<Interface>` all work (`pr02`–`pr04`).

**R2-F4: `patternSourceType` without the instance's substitution treats a variadic tuple `[...T]` as fixed-length, so a declaration over an array instance regressed.**
- `repro/g01_variadic_decl.pts`:
  ```ts
  function second<T extends unknown[]>(xs: [...T]): string {
    const [a, b] = xs;
    return String(a) + "|" + String(b);
  }
  second(["x"] as string[]);
  ```
  - Node and base: `x|undefined 1|2`.
  - Snapshot 4: `RangeError: Array index 1 is out of range`.
- **Cause:** `loose_locals.go` `patternSourceType` → `checker.IsTupleType([...T])` is true. The base used the instance's `typeAt`, which gives `string[]`.
- Pre-existing relatives with the same root:
  - the parameter form `([a, b]: [...T])` (`n/g02`);
  - a tuple with a rest element, `const [a, b] = t` with `t: [string, ...string[]]` (`repro/g08_tuple_rest_names.pts`), which throws RangeError on both where Node gives `undefined`.
- **Fix:** count a tuple as fixed-length only if it has no rest or variadic element.
- Conditional types, indexed access, class type parameters, tuple constraints and generic `for…of` all behave (`g03`–`g07`).

## Low
**R2-F5 (performance, not in D-220's costs): making `x instanceof C` an undefined test makes every element-read local tested by `instanceof` loose.**
- `repro/perf_instanceof_dispatch.pts`: 1,000 shapes × 3,000 passes of `const s = shapes[i]; if (s instanceof Circle) … else if (s instanceof Square) …`.
- Callgrind: 235.2M → 422.7M instructions (1.80×), on code that was typed before.
- Correctness holds in my probes `i01`–`i05`: class hierarchies, generic classes, `unknown[]`, generic instances.
- Either list it in D-220's costs or limit the rule to locals already loose for another reason.

## Docs
- **h11a.md, R1-F1 "Fixed: looseValue":** add the program-function argument gap (R2-F2) and the stale-narrowing crash (R2-F1).
- **D-220, "an argument (of the program's functions)… read as the type tsc narrowed it to or undefined":** not true for narrowed arguments (R2-F2).
- **D-220 costs:** add R2-F5. I re-measured the defaults: `[x = 0, y = 0]` over a `number[]` is 214.3M on both; over a `string[]` it is 246.0M → 261.0M (1.06×).

## Notes (pre-existing or by design)
- An optional class field initialised from an element read (`private p?: number = [1][3]`, `n/dn04`) still throws RangeError; only the loose-local case uses the declared type.
- `Record<number, string | undefined>` literals are not recognised as places (`rc01_04`).
- Defaults of another type over a `number[]` or `boolean[]` (`[a, b = "none"] = nums`, `[f = null] = flags`) are PTS1001 on both.
- Nested tuple defaults (`t01_05`) and a default before a rest in a tuple with a rest element (`t01_14`) are PTS1001 on both.
- A `[...T]` instance shorter than the pattern gives a misleading "this rest element" PTS1001 on both (`g10`).
- An unannotated async generator yielding a declared `string | undefined` throws TypeError, because the inferred yield type is `string` (by design).

## Checked and fine
- **Tuple defaults** (`t01`, 13 of 15): evaluation order, optional, `any` and `null` elements, earlier names in defaults, readonly tuples, returned tuples, Map and `Object.entries` with defaults, assignment, swap, parameters.
- **Nested conditionals with element reads** (`nc01`, 8 of 9; the ninth is a required place and throws by design).
- **Yields:** generator methods, `unknown` yields, generic generators, async generators (`AsyncGenerator`, `AsyncIterableIterator`, `AsyncIterable<unknown>`), `IterableIterator`/`Iterable`/`Iterator` annotations, conditionals in yields.
- **Record contexts:** `Record<string, V | undefined>`, `Record<string, unknown>`, index signatures, nested records, arguments and returns.
- **Type-parameter builtins** (`ta/`, all 24 that apply): push, unshift, Map.set/get/has, Set.add/delete, includes, indexOf, lastIndexOf, fill, with, splice, toSpliced, `Array.of`, `Promise.resolve<unknown>`, `reduce<T | undefined>`, `Object.freeze<…>`.
- **Instance agreement for generic pattern names** (`r8`, `g06`, `g07`).
- **Assignment targets** over `(T | undefined)[]`.

The probes are archived at `/tmp/h11/h11a-review2-probes.tar.gz`, without binaries; `/tmp/h11/review2.done` is written.
