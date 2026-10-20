# H09b4 reproducer report

Saved by the parent from the `reproducer` subagent's final message (claude-opus-5-5, xhigh; the Agent call passed the `opus` alias), 2026-10-20. The subagent ran in its own worktree of `617e953a4` and committed nothing. Its archive is [h09b4-reproducer-probes.tar.gz](h09b4-reproducer-probes.tar.gz) (sha256 `ed8006e208dfd27c8252235d07e0d4439c15bbf16ad8e7cb9d63f74492751f30`; `INDEX.txt` inside). The parent re-ran its matrix and proposed tests itself; see [h09b4.md](h09b4.md).

---

# H09b4 reproducer report (C171, C194)

**Result:** both defects reproduce on HEAD 617e953a4, and they are wider than the claims. Dead branches fail in plain non-generic code as well as in instances. Several **live** branches fail too, because some narrowings produce intersection types that tsc doesn't reduce. A throwaway prototype of the trap approach fixed 141 cases but regressed 4 that pass today, including the `assertNever` idiom.

## Setup
- **Worktree:** my own, at 617e953a4. I ran the five `git submodule update --init --reference …` commands (ryu, bdwgc, libuv, libbacktrace, pcre2), then `GOMAXPROCS=2 make build`, which exited 0.
- **How programs run:**
  - pts: `pts build` then the binary, with `PTS_HOME` set to the worktree and `PTS_CACHE_DIR=/tmp/h09b4/repro/.cache`.
  - Node: v24 the harness way (an `.mts` copy, `--no-warnings --experimental-transform-types`, `TZ=America/New_York`).
  - Load: at most 2 jobs at a time.
- **State afterwards:** the worktree is clean. Nothing was committed and the parent's tree was not touched.

## Prior probes on HEAD (`probes/old/HEAD.txt`)
| probe | HEAD | Node |
|---|---|---|
| n01_typeof_branch | `TypeError Expected Box<string>, got Box<any>` | `string s` |
| n02_narrow_forms | PTS1001 `` `x.length` is not supported yet `` (15:36); PTS1001 `conditional expression with operands of types Box and Box` (20:10) | runs |
| n03_direct_dead_branch | PTS1001 `converting string to number is not supported yet` (8:29) | `a number 3 / 2 s` |
| n04_dead_branch_forms | PTS1001 at 35:34 and 41:36 (string to number); `using a `Box` as a `Box`` at 36:10 | runs |
| f04_declaration_fallback | `Uncaught TypeError: Expected Box<number>, got Box<any>`, exit 1 | `3` |
| f06, f06c | PTS9001 `no member named 'utf16_len' in 'struct pts_array'` | `0 3` / `length 3, abc 2` |
| f04b, f06b | OK | OK |

**Parent's plain claims:** both confirmed.
- `function f(x: string) { if (typeof x === "number") console.log(x + 1) }` → PTS1001 `converting string to number is not supported yet`.
- `h(x: number)` with `Array.isArray(x)` → PTS1001 `` `x.length` is not supported yet ``.

**C194:** confirmed.
- `firstValue([])` and `first([])` → `RangeError: Array index 0 is out of range (length 0)…`; Node prints `undefined`.
- `String(firstPlain([]))` → PTS1001 `converting void to a string is not supported yet`.
- Correction to the claim: plain `String(e[0])` on a `never[]` does give undefined, but `(): unknown => e[0]` throws RangeError (see side finding 3).

## Matrix (633 cases, `matrix/results-head.json`)
The generated programs are in `matrix/cases/`; outputs, classes and details for every case are in the results file. The HEAD totals are **248 OK, 144 TYPEERR, 17 RANGEERR, 185 PTS1001, 39 PTS9001**, with no CRASH, WRONG or TIMEOUT. The 41 TSERR from my first run were generator mistakes, fixed by adding constraints so that `===` narrows `T`.

| family (cases) | OK | TYPEERR | RANGEERR | PTS1001 | PTS9001 |
|---|---|---|---|---|---|
| narrow: 23 forms × direct/value/plain × dead/live (112) | 45 | 22 | | 39 | 6 |
| shape: 40 dead-region shapes × direct/value/plain/live (160) | 46 | 38 | | 76 | |
| call: 17 call forms × typeof/isArray bodies × dead/live (66) | 16 | 30 | | 20 | |
| targ: 8 forms × 12 type arguments × direct/value (192) | 106 | 40 | | 24 | 22 |
| control (60) | 19 | 11 | | 21 | 9 |
| extra (16) | 7 | 3 | | 4 | 2 |
| never, C194 (27) | 9 | | 17 | 1 | |

**By context, for dead branches:**
- Direct calls by name: PTS1001. `instanceof` on primitives gives `` `instanceof` on a string ``; `=== null` gives `converting string to null`. Discriminants and `typeof x === "string"` for number[], class, null, boolean or brand give PTS9001 (`member reference type 'double' is not a pointer`, `no member named 'utf16_len' in struct pts_class_Pt`).
- Values, interfaces, abstract bases, object literals, local arrows and method values: `TypeError Expected Box<…>, got Box<any>`.
- Plain code: PTS1001 for nearly every form.

**Every dead-region shape fails** for direct and plain calls, with three exceptions that already compile. These are async (`const y = await …`), `const y = x`, and `new Box(x).value`. In each, the narrowed value lands in a never-typed local, which is IR Value, so it gets boxed and later conversions are checked (`pts_value y = pts_value_from_string(x); … pts_value_to_number_checked(y)`).

**Live branches that fail** (these are not dead code):
- `Array.isArray` for `T = number[]` narrows to `number[] & any[]`: PTS1001 by name, TYPEERR through a value.
- `switch (typeof x)` for `T = number`: PTS9001, because the dead string case reads `x.length` from a double.
- `x instanceof Pt` for `T = {v: number}`: PTS1001 `` `x.v` ``.
- Any branded type argument: PTS9001.

**`narrowsAway` is too conservative.** Through a value it falls back for `typeof x === "object"` (7 cases) and `typeof x === "undefined"`, although the same instance compiles and runs when called by name.

**Unrelated failures in the matrix, not C171:**
- `dead_local_class` ×4: local classes are not supported yet.
- `isarray_lengthy` ×3: `converting number[] to {length: number}`.
- `any_input-value`: D-155's checked boundary, by design.
- `never-control_number_empty`: D-085 RangeError for a `number[]`, by design.

## Soundness controls
**Pass today and must keep passing:**
- A lying predicate with a use that doesn't depend on the type, direct and plain: `lied string s`, as Node.
- An `assertNever` exhaustive switch reached by a cast lie, plain and generic: `caught unexpected {"kind":"tri",…}`.
- A brand under a dead `typeof`.
- `T = unknown`, a live union branch, a subclass `instanceof`, `typeof null`, the `instanceof` else-branch, a live `||` right operand, and the erased copy through `any`.

**Fail today:**
- Side effects before an early return or throw, side effects in conditions, fallthrough into a live case, and a hoisted function in a dead tail: all PTS1001.
- Lying predicate with `x + 1` and a lying assertion: PTS1001; Node prints `s1`.
- Discriminant set by a cast: PTS9001; Node prints `square at run time 3`.
- `"k" in x` on an object with an extra property: PTS9001.

## Throwaway trap prototype (`proto/trap-prototype.patch`, labelled, built with `-overlay`)
It lowers an `if` branch or `switch` clause that reads a variable narrowed to never as a catchable throw, and turns `narrowsAway` off.
- **Matrix:** 248 → 385 OK.
- **Regressions:** 4 cases go from OK to WRONG:
  - `control-lying_predicate_agnostic` direct and plain now end with an uncaught trap.
  - `extra-assert_never` plain and direct now print `caught undefined`, where Node prints the program's own message.
- **Tests:** the GREEN pin goes RED, and all 12 RED tests stay RED.

The lesson for the fix: "tsc narrows a variable to never" is not a safe test for dead code, because lies and `assertNever` reach those regions at run time. Lowering the narrowed read as a boxed dynamic value, which the three passing shapes above already do, keeps those programs running.

## Proposed tests (`tests/`; `// expect:` holds Node's stdout)
I checked them with `bin/check.py` and with the real harness: I copied them into my worktree temporarily, ran `TestRun` and `TestRunMatchesNode` with `-p 2`, then removed them. **All 12 RED tests fail both harness tests on HEAD, and Node matches every expect block.** The pin passes both.

| test | HEAD failure |
|---|---|
| generic_narrowed_dead_branches | PTS1001 at 36:40 (`converting string to number`), 50:34 (`x.length`), 55:7 (`instanceof` on a string), 62:21 (string to null), … |
| generic_narrowed_dead_regions | PTS1001 at 26:40, 27:10 (`using a Box as a Box`), … |
| generic_narrowed_dead_branch_direct_calls | PTS1001 at 17:35, 36:44 (`x.length`), … |
| generic_narrowed_dead_branch_values | exit 0; prints `declaration string TypeError Expected Box<string>, got Box<any>` where Node prints `declaration string s` (and the same for the other lines) |
| narrowed_dead_branches_plain | PTS1001 at 32:25, 38:34, 44:30 (`converting number[] to Pt`), 50:28 (`using a Pt as a Other`) |
| generic_narrowed_intersections | PTS1001 at 25:34 (`x.length`, live `number[] & any[]`), 37:30 (`x.v`) |
| narrowed_branches_live_controls | PTS1001 at 70:29, 23:29, … |
| generic_never_type_arguments | prints `value RangeError`, `by name RangeError`, … where Node prints `undefined` |
| generic_never_results | PTS1001 at 12:20 and 13:16 (`converting void to a string`) |
| narrowed_branches_reached_by_lies | PTS1001 at 45:33, 28:27. **Design question:** C59 / D-072 / D-158 made lying narrowings a catchable TypeError. If you choose that here, this test becomes a no-diff pin of the TypeError. |
| branded_primitives (side finding) | PTS9001 at 11 |
| in_narrowing_absent_member (side finding) | PTS9001 at 11 |
| narrowed_branches_pins (GREEN) | PASS on HEAD and Node |

**Existing pins a fix must update:** `run/generic_instances_that_dont_lower.pts` and `run/generic_factories_fallbacks.pts` are no-diff pins that record C171's TypeErrors. `run/generic_never_value_call.pts` is still RED.

## Other findings and how they happen
1. **C194's PTS1001.** `resultType` (lower.go ~1305) lowers a result of type never as `ir.Void` ("only throws"). That is wrong for an instance whose `T` is never.
2. **C194's RangeError.** The same `const x = xs[0]` in the `T = never` instance is lowered differently depending on the demand:
   - Direct demand: `pts_array_element`, which gives undefined.
   - D-148 value demand: the checked `PTS_ARRAY_GET_WORD`, which throws.
   - `new Box(xs[0])` uses the checked read in both.
3. **New, low severity.** D-015/D-085's "where any value can go" rule misses several contexts. An out-of-range read passed to an `unknown` or `any` parameter, returned as `unknown`/`any`, used as an arrow's `unknown` result, or written into an `unknown[]` literal throws RangeError. Meanwhile `const u: unknown = s[0]` and `String(s[0])` give undefined, as Node does. Repro: `probes/hand/d015_any_value_contexts.pts`.
4. **New PTS9001: branded primitives in plain code.** For `string & { __brand }`, `id.length` emits `(id)->utf16_len` on a `pts_value`. Repro: `brand_plain`; RED test: `branded_primitives.pts`.
5. **New PTS9001: `"k" in x` on a non-union.** It narrows to `NoK & Record<"k", unknown>` and emits `pts_dynamic_get(x, …)` with an unboxed `pts_object *`. The discriminant PTS9001 has the same missing box.
6. **No memory misreads found.** Lying predicates that narrow a class to an array or to another class are PTS1001 (`using a Len as a Pt`). A fix must keep that guard rather than cast.

## Archive
`/tmp/h09b4/repro/h09b4-reproducer-probes.tar.gz`, sha256 `ed8006e208dfd27c8252235d07e0d4439c15bbf16ad8e7cb9d63f74492751f30`. It excludes build outputs; `INDEX.txt` describes each file. `/tmp/h09b4/repro/DONE` was touched last.
