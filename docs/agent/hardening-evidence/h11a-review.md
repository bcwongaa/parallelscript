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
