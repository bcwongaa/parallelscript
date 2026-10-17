# H08b reproducer report: spreads before a suspension (C139)

The `opus-reproducer` child (claude-opus-5-5, xhigh) wrote this as its final message; the parent saved it verbatim apart from this heading line's provenance note. It ran in its own worktree (`.claude/worktrees/agent-aa4b4f961e6ae4462`, left clean, nothing committed) against the base `88c9cc293`. Its archive is [h08b-reproducer-probes.tar.gz](h08b-reproducer-probes.tar.gz) (paths below are inside its `repro/`). The parent's dispositions are in [h08b.md](h08b.md).

---

C139 reproduces on the base for every spread site and element kind that compiles. I also found one more defect that has nothing to do with suspensions, plus a double-evaluation case. The fix needs to cover both.

- **Base:** worktree at `88c9cc293037d287a3776e4fb8ab899d50429942`, built with `GOMAXPROCS=2 make build` (exit 0). `pts-bin/pts` sha256 `b67340d5…5939`.
- **Node:** v24.21.0.
- **Runs:** `PTS_HOME=<worktree> pts-bin/pts build X.pts -o X.bin && ./X.bin`, compared against `node --no-warnings --experimental-transform-types X.mts` by `tools/cmp.sh`.
- **Archive:** `/tmp/h08b/h08b-reproducer-probes.tar.gz` (sha256 `d436a785…d539`). Marker `/tmp/h08b/repro.done`. All paths below are inside `repro/`.
- **Worktree:** left clean. Nothing was committed or pushed.

## 1. RED reproduction of the skipped test (verified)

I ran `run/suspension_spreads_evaluate_first.pts` with its skip line removed (`red/skipped_test.pts`, transcript `red/skipped_test.cmp.txt`). Both sides exit 0, and all 7 cases differ from Node:

| case | PTS | Node |
|---|---|---|
| `sum(...numbers, yield)` | `=> 113` | `=> 13` |
| `sum(...getKept(), yield)` | `getKept,yielded 0 => 113` | `… => 13` |
| `[...iterable, yield]` | `yielded 0,iterate => 4,3,2,1,10` | `iterate,yielded 0 => 2,1,10` |
| `new Pair(...parts, yield)` | `=> 1,100,10` | `=> 1,10` |
| `{ ...object, z: yield }` | `yielded 0,get a => {"a":2,"z":10}` | `get a,yielded 0 => {"a":1,"z":10}` |
| `await sum(...xs, …)` | `=> 113` | `=> 13` |
| `await { ...o, …}` | `{"a":2,"z":10}` | `{"a":1,"z":10}` |

I also reran H08a's 16 matrix cases (`operand` / `spread …` and `new with spread`, regenerated from its `gen.py`). All 16 are still WRONG (`h08a_cases/cmp.txt`).

## 2. The generated matrix (verified)

The generator and runner are one script, `matrix/matrix.py` (`gen`, `run`, `classify`, `summarize`). It is deterministic: an exhaustive grid in table order, with no randomness. Every case runs in three variants:

- **real:** the actual `yield` or `await`. For yield, the driver mutates the source between steps; for await, a microtask queued just before the await does it.
- **sync:** `now(mut)` runs the mutation at the suspension point without suspending. Node prints exactly the real line for this variant; that held for all 5,598 cases, which checks the generator itself.
- **const:** the suspension is replaced by `(10)`; this is the plain control twin.

Run command: `python3 -I matrix.py run m2 2`, exit 0, about 100 minutes. Results are in `matrix/m2/results.json`, `summary.txt`, `causes.txt` and `sync_table.txt`.

**Totals: 5,598 cases. OK 740, WRONG 4,210, DIAG 604, ICE 12, TSERR 32, CRASH 0.**

| WRONG cases, by what the controls show | count |
|---|---|
| Both controls OK: purely the suspension | 2,508 |
| const OK, sync also WRONG: C139 plus the synchronous defect (§3, cause D) | 1,356 |
| const also WRONG: pre-existing, not C139 (§4) | 346 |

- The yield and await halves are identical in every row, so each count is two equal halves.
- Every DIAG, ICE and TSERR is the same in its const control, so none is caused by the suspension.

By family:

- **Spread family (5,108 cases):** OK 694, WRONG 2,334 + 1,356 + 326, DIAG 390, TSERR 8.
- **Object-spread family (490 cases):** OK 46, WRONG 174 + 20, DIAG 214, ICE 12, TSERR 24.

**Sites with suspension-caused WRONG cases:**

- arrow, declared, generic, value and `.call` rest functions
- methods, static methods and interface methods
- `new`
- calls through `any` (function and method)
- tuples into fixed parameters and into head-plus-rest parameters
- `Math.max`, `Math.min`, `String.fromCharCode`, `Array.of`, `new Array`, `push`, `unshift`, `str.concat`, `console.log`
- array literals: plain, element before, two spreads, the same spread twice, nested, in a template, suspension inside a later spread, `[...xs]` as a whole argument
- tuple-typed literals, destructuring, a `for…of`'s iterated value
- `new Set([...])` and `new Map([...])`
- every object-literal site that compiles: two spreads, a property before, a computed key after, a nested `{ ...{ ...o } }`, a typed argument, a Record context, `f({ ...o }, S)`

The guards are right, as expected: `[S, ...xs]`, `[...one(S), ...xs]` and `{ z: S, ...o }`.

**Element kinds with suspension-caused WRONG cases:**

- Arrays: `number[]`, readonly, property, from a call, holes (both element types), `V[]`, `[number, number][]`, `string[]`.
- Tuples: fixed, rest, mixed `[number, string]`.
- Sets and maps: `Set`, `set.values()`, `set.keys()`, `set.entries()`, the map itself, `map.keys()`, `map.values()`, `map.entries()`; also `arr.keys()`, `arr.values()`, `arr.entries()`.
- Iterables: a user class iterable, an object-literal iterable, a generator call, a generator object, `Iterable<number>` of an array or a set.
- Typed arrays: Float64Array, Int32Array, Uint8Array, `.values()`.
- `any` holding an array, a set or an iterable; the unions `T[] | Set<T>` and `?? []`.
- Object sources: literal, literal with a getter, class instance, interface (of a literal, a class, a getter literal), Record and index signature (write, add key, delete key), call result, property.

Hand probes (`explore/`, outputs in `explore/out/`) show the same C139 wrongs for `Math.hypot`, `String.fromCodePoint` and `new Float64Array([...xs, S])`, and in other function forms:

- top-level await
- an async generator's awaits and yields
- `this.items` in a class generator method
- a conditional's branch
- an async arrow's expression body
- a spread between two suspensions
- `[...(yield), ...(yield)]`

**Cause check:** a Node-only "pre" variant runs the mutation before the whole statement. All 2,334 suspension-only spread cases and 160 of the 174 object cases print exactly that line in PTS. The other 14 object cases are explained:

- 12 "late elements": the operand was evaluated before the suspension, its properties after.
- 2 are a second spread (`...o2`) read late beside a reassigned first source. The spread family's 8 mixed cases in the sync-WRONG group are the same thing with `...ys`.

No case has an unexplained cause.

## 3. Causes, with file:line on 88c9cc293

**A. Classification (`pts/lower/yield_expressions.go`)**
- `isSettled` puts `KindSpreadElement` with parentheses and `as` (:451-453). So `...xs` of a constant or unassigned name is neither kept nor read before the suspension.
- `isSettled` treats an array or object literal of settled operands as settled (:486-495), so `f([...xs], S)` and `f({ ...o }, S)` are built after the resume.
- `evaluationOrder` lists a `SpreadAssignment`'s source as a plain operand (:698-699).
- A spread of a hoisted yield counts as settled through `l.yielded` (:446-447), which is why `[...(yield), ...(yield)]` is wrong.

**B. What `keep` keeps (:379-381).** It swaps a spread for its operand and keeps only that value, never the elements. The elements are read where the spread is lowered, after the resume:
- `argumentValues` (pts/lower/lower.go:1948-1995). Array parts become an `ArraySpread`; a tuple becomes `FieldRef`s (:1978) and, for a rest tuple, checked reads plus a slice (:1961); `any`, unions and iterables go through `ValueToArray` (:1990).
- `spreadParts` (arrays.go:188-206) and `mapItemsArray` (maps.go:487).
- `spreadObjectLiteral` / `spreadObject` (objects.go:475-479), which read with `propertyOf` inside the new object (:610).
- `spreadIntoRecord` (records.go:440-447, `MapAssign`).
- `SpreadArg` in `dynamicCallWith` (dynamic.go:172-177) and `consoleArguments` (console.go:67). The runtime spreads these at the call.

For `m.keys()`, `m.values()` and `m.entries()`, the kept value is a live `MapIteratorOf` iterator (maps.go:258-260, D-110), drained after the resume.

**C. Consumers that ignore `l.kept` and re-evaluate the receiver.** `arrayIteration` (array_library.go:221-238, `arrayObject` at :226), `typedArrayIteration` (typed_arrays.go:618-635, :623) and `mapIterableOf` (maps.go:343-366, :348) re-evaluate the receiver of `a.values()`, `t.values()` and `m.keys()`. `iterable` (array_library.go:287) calls the first two before `l.expression`. When the spread's operand is a call, it runs twice (verified):
- `list(...getArray().values(), yield)` logs `getArray,yielded 0,getArray`.
- `[...getM().keys()|values(), S]`, `[...getSet().values(), S]`, `[...getXs().values(), S]`, `[...getTa().values(), S]` and `Math.max(...getTa().values(), S)` each call the receiver twice.

**D. New, pre-existing, no suspension: array spreads read their elements after later operands run.** `sum(...xs, grow())` gives `-5/2/100/10` where Node gives `1/2/10`.
- The C for `ArraySpread` (`concat`, pts/cgen/arrays.go:138-152) evaluates each spread array only as a reference into a temporary, evaluates the later operands, and only then appends the elements (:148).
- `SpreadArg` (pts/cgen/cgen.go:894) boxes the array or tuple, and the runtime spreads it at the call. That covers `console.log`, and calls through `any`, including tuples and typed arrays.
- Affected: arrays, `arr.values()`, rest tuples, holes and `?? []` at every site that appends a spread array; tuples and typed arrays only through `any` or `console.log`. 1,704 sync cases are WRONG (table in `sync_table.txt`; minimal probe in `explore/sync_order.pts`).
- Right synchronously: nested `[...[...xs]]`, a whole `[...xs]` argument, tuples into typed parameters, sets, maps, iterables and object spreads (`spreadObject` already binds its reads before later impure values, objects.go:627).

## 4. Pre-existing items found on the way (verified, all happen without a suspension)

- **Optional tuple elements.** A tuple whose optional element is left out spreads it as undefined: `list(...([1] as [number, number?]))` gives `1/undefined`, and `[...t].length` is 2 (Node 1). This contradicts D-100 (`explore/side_preexisting.pts`; 128 matrix cases).
- **Spreads through `any`.** A call through `any` throws `TypeError: Cannot spread Set|Map Iterator|Generator|string|… into the arguments of a call through any` (pts/runtime/src/dynamic.c:740-753), where Node spreads the items. That matches D-075's "arrays (or tuples)", but SPEC line 191 says spreads through `any` iterate.
- **ICE.** `{ w: 0, ...anyObj }` and `{ z: v, ...anyObj }` hit PTS9001, a nil-pointer dereference in lowering (`explore/side_any_spread_ice.pts`).
- **Map entries in a literal.** `[...m.entries(), [10, 0]]` throws `TypeError: Expected number[][], got Array` (`explore/entries_literal.pts`).
- **ICE.** `[(yield 1)[0]]` with an array TNext hits PTS9001, "ir.Type is *ir.Primitive, not *ir.Array". No spread is involved (`explore/yield_element_ice.pts`).
- **Expected, not new:** `number[]` holes read 0 (D-085). The computed-key trace with a literal-typed key is C148. The PTS1001s include: Set, Map and string spreads into arguments; `splice` and `Object.assign` spread arguments; Records, unions, `any` and conditionals spread into shapes.

## 5. Proposed RED tests (in `tests/`)

1. `suspension_spreads_evaluate_first.pts`: the existing test with its skip line removed (`.unskip.diff`).
2. `suspension_spread_arguments_evaluate_first.pts`: every call site and builtin; tuples into fixed and head-plus-rest parameters; yield and await.
3. `suspension_spread_iterables_evaluate_first.pts`: every iterable kind, plus all three receiver-twice paths (cause C).
4. `suspension_object_spreads_evaluate_first.pts`: object, getter, class, interface, Record and index-signature spreads (added and deleted keys), computed key, typed argument, `f({ ...o }, S)` and `f([...xs], S)`; yield and await.
5. `suspension_spreads_in_other_forms.pts`: the function and statement forms from §2.
6. `spread_elements_read_before_later_arguments.pts`: cause D without a suspension. You may want this in another slice.

Node prints each expect block exactly, exits 0 and writes nothing to stderr (`tests/node_expect_check.txt`, exit 0). On the base, `GOMAXPROCS=2 PTS_REQUIRE_NODE=1 go test -count=1 -timeout 40m -v -run '^(TestRun|TestRunMatchesNode)$/^(<the six>)$' ./pts/tests/` exits 1. All 12 subtests fail on output assertions only (`run_test.go:69` ×6, `:98` ×6), with no build errors (`tests/red_harness_base.txt`). The only lines already right on the base are deliberate guards and the report lines that follow a `console.log`.

## 6. Risks for the fix (inferred from the code; not tested against a fix)

- **Don't snapshot a spread as one plain array.**
  - Tuples are spread field by field into typed parameters (lower.go:1978-1988). A copy of `[number, string]` as an array can't reach `(a: number, b: string)`. Keep a tuple copy or each element.
  - Rest tuples rely on a pure value plus `isRestTuple(typeAt(...))` (:1961).
  - Several consumers decide from the checker type first: the string check (arrays.go:199), `mapIterableOf` (:189), `keepsSpread` and `keepsTupleElements` (arrays.go:770-830). They would mis-lower a kept value of another IR type. Key the snapshot on the SpreadElement and check it first, or keep the source's own IR type.
- **Element types and holes.** Strings snapshot as code points, maps as tuples, typed arrays as `number[]`, iterables and `any` as `any[]` (checked later by `spreadPart`, arrays.go:172-184). A snapshot must keep holes as `pts_array_copy` does, so the target's `readsHoles` (cgen/arrays.go:141) still decides undefined (D-183).
- **Iterators.** `m.keys()` as a value is a live iterator, not a snapshot. Generators and user iterables must be drained exactly once, before the suspension, and the consumer must not iterate the original again.
- **Double evaluation.** Fixing B without C leaves the receiver evaluated twice.
- **Object spreads.** A copy-object snapshot breaks two things:
  - `spreadNames` (destructuring.go:394-411) takes a class's own fields from `AllFields`. A shape copy would make it use the checker's properties, which include prototype getters and methods.
  - D-108's `OrderOf` (objects.go:593-595) needs the original source.

  Hoisting the property reads into frame locals (`LocalDecl`s before the suspension) keeps both. `Bind` locals are C temporaries and can't cross a yield (D-109). Records need a copy made before the suspension, so keys added or deleted meanwhile don't show.
- **Behavior change through `any`.** A `ValueToArray` snapshot for `any` calls would make non-array spreads work only when a suspension follows. The same code without one still throws.
- **Cause D survives a hoisting-only fix.** Snapshotting only in `hoistYields` leaves test 6 red. Copying at the spread's position fixes both.
- **Cost.** One frame local plus one copy per spread before a suspension, and then `ArraySpread` copies again. Strings and literal arrays of settled names need no snapshot.
